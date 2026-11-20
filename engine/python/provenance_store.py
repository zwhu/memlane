"""External SQLite WAL provenance date index for distilled coding lessons.

Replaces source edits to Cognee's ``distill.py`` and full-package copying.
Stores ONLY dataset/document/QA identifiers, ``lesson_type``, ``state_key``,
and normalized ``YYYY-MM-DD`` calendar dates — never lesson text or transcripts.
"""
from datetime import datetime, timezone
import json
import os
from pathlib import Path
import re
import sqlite3
import uuid

_CALENDAR_RE = re.compile(r'^\d{4}-\d{2}-\d{2}$')
_STATUS_RANK = {'unconfirmed': 0, 'created_at': 1, 'session_date': 2, 'linked_qa': 3}
_DATA_ROUTE_RE = re.compile(r'^/api/v1/datasets/([0-9a-fA-F-]{36})/data/?$')
_IMPROVE_ROUTE_RE = re.compile(r'^/api/v1/improve/?$')


def _iso_now():
    return datetime.now(timezone.utc).isoformat(timespec='microseconds')


def _calendar_date(value):
    """Extract a validated ``YYYY-MM-DD`` calendar date from a date or ISO-8601 timestamp."""
    if not isinstance(value, str):
        return None
    text = value.strip()
    if len(text) < 10 or not _CALENDAR_RE.match(text[:10]):
        return None
    head = text[:10]
    try:
        datetime.strptime(head, '%Y-%m-%d')
    except ValueError:
        return None
    if len(text) == 10:
        return head
    try:
        datetime.fromisoformat(text.replace('Z', '+00:00'))
    except ValueError:
        return None
    return head


def _local_calendar_date(value):
    """Extract a local ``YYYY-MM-DD`` calendar date from a date or ISO-8601 timestamp."""
    if not isinstance(value, str):
        return None
    text = value.strip()
    if len(text) == 10:
        return _calendar_date(text)
    if not _calendar_date(text):
        return None
    try:
        dt = datetime.fromisoformat(text.replace('Z', '+00:00'))
    except ValueError:
        return None
    if dt.tzinfo is None:
        dt = dt.replace(tzinfo=timezone.utc)
    return dt.astimezone().strftime('%Y-%m-%d')


def _best_status(*statuses):
    best = 'unconfirmed'
    best_rank = 0
    for candidate in statuses:
        rank = _STATUS_RANK.get(candidate, 0)
        if rank > best_rank:
            best = candidate
            best_rank = rank
    return best


def _is_lesson_metadata(meta):
    if not isinstance(meta, dict):
        return False
    if meta.get('lesson_type') or meta.get('first_source_date') or meta.get('source_date_status'):
        return True
    node_set = meta.get('node_set')
    return isinstance(node_set, list) and 'session_learnings' in node_set


class ProvenanceStore:
    """Private ``0600`` SQLite WAL index preserving earliest source dates across replacements."""

    def __init__(self, path):
        self.path = Path(path)
        self.path.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
        os.chmod(self.path.parent, 0o700)
        if self.path.is_symlink():
            raise ValueError('provenance database must not be a symlink')
        fd = os.open(self.path, os.O_CREAT | os.O_RDWR | getattr(os, 'O_NOFOLLOW', 0), 0o600)
        os.close(fd)
        os.chmod(self.path, 0o600)
        self.db = sqlite3.connect(self.path, isolation_level=None, check_same_thread=False)
        self.db.execute('PRAGMA journal_mode=WAL')
        self.db.execute('PRAGMA synchronous=FULL')
        self.db.execute('PRAGMA busy_timeout=5000')
        self.db.executescript('''
          CREATE TABLE IF NOT EXISTS keyed_provenance (
            dataset_id TEXT NOT NULL,
            lesson_type TEXT NOT NULL,
            state_key TEXT NOT NULL,
            first_source_date TEXT NOT NULL,
            source_date_status TEXT NOT NULL,
            updated_at TEXT NOT NULL,
            PRIMARY KEY(dataset_id, lesson_type, state_key)
          );
          CREATE TABLE IF NOT EXISTS document_provenance (
            dataset_id TEXT NOT NULL,
            document_id TEXT NOT NULL,
            lesson_type TEXT NOT NULL,
            state_key TEXT NOT NULL,
            first_source_date TEXT NOT NULL,
            source_date_status TEXT NOT NULL,
            source_qa_ids_json TEXT NOT NULL,
            updated_at TEXT NOT NULL,
            PRIMARY KEY(dataset_id, document_id)
          );
        ''')

    def lookup(self, dataset_id, document_id, *, lesson_type='', state_key=''):
        ds = str(dataset_id)
        doc = str(document_id)
        doc_row = self.db.execute(
            '''SELECT lesson_type,state_key,first_source_date,source_date_status,source_qa_ids_json
               FROM document_provenance WHERE dataset_id=? AND document_id=?''',
            (ds, doc),
        ).fetchone()
        lt = str(lesson_type or (doc_row[0] if doc_row else '') or '').strip()
        sk = str(state_key or (doc_row[1] if doc_row else '') or '').strip()
        key_row = None
        if lt and sk:
            key_row = self.db.execute(
                '''SELECT first_source_date,source_date_status
                   FROM keyed_provenance WHERE dataset_id=? AND lesson_type=? AND state_key=?''',
                (ds, lt, sk),
            ).fetchone()
        if doc_row is None and key_row is None:
            return None
        dates = []
        qas = []
        statuses = []
        if doc_row:
            if _calendar_date(doc_row[2]):
                dates.append(doc_row[2])
                statuses.append(doc_row[3] if _STATUS_RANK.get(doc_row[3], 0) > 0 else 'linked_qa')
            elif doc_row[3]:
                statuses.append(doc_row[3])
            try:
                qas = json.loads(doc_row[4])
            except ValueError:
                qas = []
        if key_row and _calendar_date(key_row[0]):
            dates.append(key_row[0])
            statuses.append(key_row[1] if _STATUS_RANK.get(key_row[1], 0) > 0 else 'linked_qa')
        best = min(dates) if dates else ''
        status = _best_status(*statuses)
        if best and status == 'unconfirmed':
            status = 'linked_qa'
        return {
            'first_source_date': best,
            'source_date_status': status if best else 'unconfirmed',
            'source_qa_ids': sorted({str(x) for x in qas if x}),
        }

    def record_document(
        self,
        dataset_id,
        document_id,
        *,
        lesson_type='',
        state_key='',
        candidate_dates=(),
        source_qa_ids=(),
        explicit_status=None,
    ):
        ds = str(dataset_id)
        doc = str(document_id)
        lt = str(lesson_type or '').strip()
        sk = str(state_key or '').strip()
        now = _iso_now()
        self.db.execute('BEGIN IMMEDIATE')
        try:
            doc_row = self.db.execute(
                '''SELECT lesson_type,state_key,first_source_date,source_date_status,source_qa_ids_json
                   FROM document_provenance WHERE dataset_id=? AND document_id=?''',
                (ds, doc),
            ).fetchone()
            effective_lt = lt or (doc_row[0] if doc_row else '')
            effective_sk = sk or (doc_row[1] if doc_row else '')
            key_row = None
            if effective_lt and effective_sk:
                key_row = self.db.execute(
                    '''SELECT first_source_date,source_date_status
                       FROM keyed_provenance WHERE dataset_id=? AND lesson_type=? AND state_key=?''',
                    (ds, effective_lt, effective_sk),
                ).fetchone()

            dates = []
            for raw_d in candidate_dates or ():
                parsed = _calendar_date(raw_d)
                if parsed:
                    dates.append(parsed)
            if doc_row and _calendar_date(doc_row[2]):
                dates.append(doc_row[2])
            if key_row and _calendar_date(key_row[0]):
                dates.append(key_row[0])

            existing_qas = []
            if doc_row and doc_row[4]:
                try:
                    existing_qas = json.loads(doc_row[4])
                except ValueError:
                    existing_qas = []
            merged_qas = sorted({str(x) for x in (*existing_qas, *(source_qa_ids or ())) if x})

            resolved_date = min(dates) if dates else ''
            if resolved_date:
                inferred = []
                if merged_qas:
                    inferred.append('linked_qa')
                if _STATUS_RANK.get(explicit_status, 0) > 0:
                    inferred.append(explicit_status)
                elif explicit_status is None and not (doc_row and _calendar_date(doc_row[2])) and not (key_row and _calendar_date(key_row[0])):
                    inferred.append('linked_qa')
                if doc_row and _calendar_date(doc_row[2]):
                    inferred.append(doc_row[3] if _STATUS_RANK.get(doc_row[3], 0) > 0 else 'linked_qa')
                if key_row and _calendar_date(key_row[0]):
                    inferred.append(key_row[1] if _STATUS_RANK.get(key_row[1], 0) > 0 else 'linked_qa')
                status = _best_status(*inferred)
                if status == 'unconfirmed':
                    status = 'linked_qa'
                key_status = _best_status(
                    status,
                    key_row[1] if (key_row and _calendar_date(key_row[0])) else 'unconfirmed',
                )
            else:
                status = 'unconfirmed'
                key_status = 'unconfirmed'

            if effective_lt and effective_sk and resolved_date:
                self.db.execute(
                    '''INSERT INTO keyed_provenance
                       (dataset_id,lesson_type,state_key,first_source_date,source_date_status,updated_at)
                       VALUES(?,?,?,?,?,?)
                       ON CONFLICT(dataset_id,lesson_type,state_key) DO UPDATE SET
                         first_source_date=CASE
                           WHEN keyed_provenance.first_source_date='' THEN excluded.first_source_date
                           WHEN excluded.first_source_date < keyed_provenance.first_source_date
                             THEN excluded.first_source_date
                           ELSE keyed_provenance.first_source_date
                         END,
                         source_date_status=excluded.source_date_status,
                         updated_at=excluded.updated_at''',
                    (ds, effective_lt, effective_sk, resolved_date, key_status, now),
                )

            self.db.execute(
                '''INSERT INTO document_provenance
                   (dataset_id,document_id,lesson_type,state_key,first_source_date,source_date_status,source_qa_ids_json,updated_at)
                   VALUES(?,?,?,?,?,?,?,?)
                   ON CONFLICT(dataset_id,document_id) DO UPDATE SET
                     lesson_type=CASE WHEN excluded.lesson_type!='' THEN excluded.lesson_type ELSE document_provenance.lesson_type END,
                     state_key=CASE WHEN excluded.state_key!='' THEN excluded.state_key ELSE document_provenance.state_key END,
                     first_source_date=excluded.first_source_date,
                     source_date_status=excluded.source_date_status,
                     source_qa_ids_json=excluded.source_qa_ids_json,
                     updated_at=excluded.updated_at''',
                (ds, doc, effective_lt, effective_sk, resolved_date, status, json.dumps(merged_qas), now),
            )
            self.db.execute('COMMIT')
        except Exception:
            if self.db.in_transaction:
                self.db.execute('ROLLBACK')
            raise
        return {
            'first_source_date': resolved_date,
            'source_date_status': status,
            'source_qa_ids': merged_qas,
        }

    def enrich_data_items(self, dataset_id, items):
        """Inject external provenance dates into Cognee ``/datasets/{id}/data`` items."""
        if not isinstance(items, list):
            return items
        ds = str(dataset_id)
        enriched = []
        for item in items:
            if not isinstance(item, dict) or not item.get('id'):
                enriched.append(item)
                continue
            meta_key = ('externalMetadata' if isinstance(item.get('externalMetadata'), dict)
                        else ('external_metadata' if isinstance(item.get('external_metadata'), dict) else None))
            if meta_key is None or not _is_lesson_metadata(item[meta_key]):
                enriched.append(item)
                continue
            meta = dict(item[meta_key])
            doc_id = str(item['id'])
            lt = str(meta.get('lesson_type') or '').strip()
            sk = str(meta.get('state_key') or '').strip()
            candidates = []
            origin_status = 'unconfirmed'
            if _calendar_date(meta.get('first_source_date')):
                candidates.append(meta['first_source_date'])
                origin_status = 'linked_qa'
            elif _calendar_date(meta.get('session_date')):
                candidates.append(meta['session_date'])
                origin_status = 'session_date'
            else:
                created_day = _local_calendar_date(item.get('createdAt') or item.get('created_at'))
                if created_day:
                    candidates.append(created_day)
                    origin_status = 'created_at'
            recorded = self.record_document(
                ds,
                doc_id,
                lesson_type=lt,
                state_key=sk,
                candidate_dates=candidates,
                source_qa_ids=meta.get('source_qa_ids') or (),
                explicit_status=origin_status,
            )
            if recorded['first_source_date']:
                meta['first_source_date'] = recorded['first_source_date']
            meta['source_date_status'] = recorded['source_date_status']
            if recorded['source_qa_ids']:
                meta['source_qa_ids'] = recorded['source_qa_ids']
            enriched.append({**item, meta_key: meta})
        return enriched

    async def sync_dataset(self, dataset_id, *, fetch_rows=None, fetch_session_qa_dates=None, session_ids=None):
        """Sync provenance dates from Cognee's dataset rows (preferring trusted ``system_metadata``)."""
        ds = str(dataset_id)
        if fetch_rows is not None:
            rows = fetch_rows(ds)
            if hasattr(rows, '__await__'):
                rows = await rows
        else:
            from cognee.modules.data.methods import get_dataset_data
            rows = await get_dataset_data(dataset_id=uuid.UUID(ds), limit=1000, offset=0)
        if not rows:
            return
        for row in rows:
            sys_meta = (row.get('system_metadata') if isinstance(row, dict)
                        else getattr(row, 'system_metadata', None)) or {}
            ext_meta = (row.get('external_metadata') if isinstance(row, dict)
                        else getattr(row, 'external_metadata', None)) or {}
            # Security invariant from Cognee lesson_store: system_metadata wins over
            # user-editable external_metadata so planted dates never displace a state_key.
            trusted = sys_meta if (isinstance(sys_meta, dict) and sys_meta) else ext_meta
            if not _is_lesson_metadata(trusted):
                continue
            doc_id = str(row.get('id') if isinstance(row, dict) else getattr(row, 'id', ''))
            if not doc_id:
                continue
            lt = str(trusted.get('lesson_type') or '').strip()
            sk = str(trusted.get('state_key') or '').strip()
            qa_ids = list(trusted.get('source_qa_ids') or ())
            candidates = []
            origin_status = 'unconfirmed'
            if _calendar_date(trusted.get('first_source_date')):
                candidates.append(trusted['first_source_date'])
                origin_status = 'linked_qa'
            elif _calendar_date(trusted.get('session_date')):
                candidates.append(trusted['session_date'])
                origin_status = 'session_date'
            if fetch_session_qa_dates is not None and (qa_ids or trusted.get('session_id') or session_ids):
                qa_dates = fetch_session_qa_dates(
                    qa_ids=qa_ids,
                    session_id=trusted.get('session_id') or (session_ids[0] if session_ids else None),
                    trusted_metadata=trusted,
                )
                if hasattr(qa_dates, '__await__'):
                    qa_dates = await qa_dates
                qa_parsed = [parsed for d in (qa_dates or ()) if (parsed := _calendar_date(d))]
                if qa_parsed:
                    candidates.extend(qa_parsed)
                    origin_status = 'linked_qa'
            if not candidates:
                raw_created = row.get('created_at') if isinstance(row, dict) else getattr(row, 'created_at', None)
                if raw_created is None and isinstance(row, dict):
                    raw_created = row.get('createdAt')
                created_day = _local_calendar_date(raw_created)
                if created_day:
                    candidates.append(created_day)
                    origin_status = 'created_at'
            self.record_document(
                ds,
                doc_id,
                lesson_type=lt,
                state_key=sk,
                candidate_dates=candidates,
                source_qa_ids=qa_ids,
                explicit_status=origin_status,
            )

    def close(self):
        self.db.close()


def install_provenance_middleware(app, store, *, sync_hook=None):
    """Attach a pure ASGI middleware synchronizing /improve and enriching /datasets/{id}/data."""

    class _ProvenanceMiddleware:
        def __init__(self, inner_app):
            self.inner_app = inner_app

        async def __call__(self, scope, receive, send):
            if scope.get('type') != 'http':
                await self.inner_app(scope, receive, send)
                return
            method = (scope.get('method') or '').upper()
            path = scope.get('path') or ''
            data_match = _DATA_ROUTE_RE.match(path) if method == 'GET' else None
            improve_match = _IMPROVE_ROUTE_RE.match(path) if method == 'POST' else None

            if not data_match and not improve_match:
                await self.inner_app(scope, receive, send)
                return

            if data_match:
                dataset_id = str(uuid.UUID(data_match.group(1)))
                start_msg = None
                body_chunks = bytearray()
                passthrough = False

                async def capture_send(message):
                    nonlocal start_msg, passthrough
                    if message['type'] == 'http.response.start':
                        headers = {k.decode('latin-1').lower(): v.decode('latin-1') for k, v in message.get('headers', [])}
                        ctype = headers.get('content-type', '').split(';', 1)[0].strip().lower()
                        if message.get('status') == 200 and ctype == 'application/json':
                            start_msg = message
                            return
                        passthrough = True
                        await send(message)
                        return
                    if passthrough or message['type'] != 'http.response.body':
                        await send(message)
                        return
                    body_chunks.extend(message.get('body', b''))
                    if message.get('more_body', False):
                        return
                    out_bytes = bytes(body_chunks)
                    try:
                        parsed = json.loads(out_bytes)
                        if isinstance(parsed, list):
                            enriched = store.enrich_data_items(dataset_id, parsed)
                            out_bytes = json.dumps(enriched, ensure_ascii=False).encode('utf-8')
                    except Exception:
                        pass
                    new_headers = [
                        (k, v) for k, v in start_msg.get('headers', [])
                        if k.decode('latin-1').lower() != 'content-length'
                    ]
                    new_headers.append((b'content-length', str(len(out_bytes)).encode('ascii')))
                    await send({**start_msg, 'headers': new_headers})
                    await send({'type': 'http.response.body', 'body': out_bytes, 'more_body': False})

                await self.inner_app(scope, receive, capture_send)
                return

            # POST /api/v1/improve: capture request metadata non-destructively, then sync on 200.
            req_buf = bytearray()
            improve_status = None

            async def tee_receive():
                msg = await receive()
                if msg['type'] == 'http.request':
                    req_buf.extend(msg.get('body', b''))
                return msg

            async def observe_send(message):
                nonlocal improve_status
                if message['type'] == 'http.response.start':
                    improve_status = message.get('status')
                await send(message)

            await self.inner_app(scope, tee_receive, observe_send)
            if improve_status == 200 and req_buf:
                try:
                    req_json = json.loads(req_buf)
                except ValueError:
                    req_json = None
                if isinstance(req_json, dict):
                    raw_ds = req_json.get('dataset_id') or req_json.get('datasetId')
                    session_ids = req_json.get('session_ids') or req_json.get('sessionIds')
                    try:
                        ds_uuid = str(uuid.UUID(str(raw_ds))) if raw_ds else None
                    except ValueError:
                        ds_uuid = None
                    if ds_uuid:
                        try:
                            fn = sync_hook or store.sync_dataset
                            await fn(ds_uuid, session_ids=session_ids if isinstance(session_ids, list) else None)
                        except Exception:
                            pass

    app.add_middleware(_ProvenanceMiddleware)
    return store
