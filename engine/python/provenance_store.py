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

from _runtime_common import id_variants, sql_in_clause, uuid_text

_CALENDAR_RE = re.compile(r'^\d{4}-\d{2}-\d{2}$')
_STATUS_RANK = {'unconfirmed': 0, 'created_at': 1, 'session_date': 2, 'linked_qa': 3}
_DATA_ROUTE_RE = re.compile(r'^/api/v1/datasets/([0-9a-fA-F-]{36})/data/?$')
_IMPROVE_ROUTE_RE = re.compile(r'^/api/v1/improve/?$')


def _iso_now():
    return datetime.now(timezone.utc).isoformat(timespec='microseconds')


def calendar_date(value):
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


def local_calendar_date(value):
    """Extract a local ``YYYY-MM-DD`` calendar date from a date or ISO-8601 timestamp."""
    if not isinstance(value, str):
        return None
    text = value.strip()
    if len(text) == 10:
        return calendar_date(text)
    if not calendar_date(text):
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


def is_lesson_metadata(meta):
    if not isinstance(meta, dict):
        return False
    if meta.get('lesson_type') or meta.get('first_source_date') or meta.get('source_date_status'):
        return True
    node_set = meta.get('node_set')
    return isinstance(node_set, list) and 'session_learnings' in node_set


_calendar_date = calendar_date
_local_calendar_date = local_calendar_date
_is_lesson_metadata = is_lesson_metadata


def load_provenance_maps(provenance_path, allowed_datasets):
    """Read-only load of locked earliest dates keyed by canonical dataset/document UUIDs."""
    doc_dates = {}
    key_dates = {}
    if not provenance_path or not allowed_datasets:
        return doc_dates, key_dates
    path = Path(provenance_path)
    if path.is_symlink() or not path.is_file():
        return doc_dates, key_dates
    variant_to_canon = {}
    for ds in allowed_datasets:
        canon = uuid_text(ds)
        if not canon:
            continue
        for variant in id_variants(canon):
            variant_to_canon[variant] = canon
    if not variant_to_canon:
        return doc_dates, key_dates
    ds_variants = tuple(sorted(variant_to_canon.keys()))
    ds_sql, ds_params = sql_in_clause(ds_variants)
    try:
        con = sqlite3.connect(f'file:{path}?mode=ro', uri=True, timeout=2.0)
    except sqlite3.Error:
        return doc_dates, key_dates
    try:
        for ds, doc_id, lt, sk, first_date in con.execute(
            f'''SELECT dataset_id, document_id, lesson_type, state_key, first_source_date
                FROM document_provenance WHERE dataset_id {ds_sql}''',
            ds_params,
        ):
            canon_ds = variant_to_canon.get(str(ds), uuid_text(ds))
            canon_doc = uuid_text(doc_id)
            if not canon_ds or not canon_doc:
                continue
            cand_lt = str(lt or '').strip()
            cand_sk = str(sk or '').strip()
            cand_day = calendar_date(first_date) or ''
            prev = doc_dates.get((canon_ds, canon_doc))
            if prev is None:
                doc_dates[(canon_ds, canon_doc)] = (cand_lt, cand_sk, cand_day)
            else:
                merged_lt = prev[0] or cand_lt
                merged_sk = prev[1] or cand_sk
                merged_dates = [d for d in (prev[2], cand_day) if d]
                doc_dates[(canon_ds, canon_doc)] = (merged_lt, merged_sk, min(merged_dates) if merged_dates else '')
        for ds, lt, sk, first_date in con.execute(
            f'''SELECT dataset_id, lesson_type, state_key, first_source_date
                FROM keyed_provenance WHERE dataset_id {ds_sql}''',
            ds_params,
        ):
            canon_ds = variant_to_canon.get(str(ds), uuid_text(ds))
            cand_lt = str(lt or '').strip()
            cand_sk = str(sk or '').strip()
            cand_day = calendar_date(first_date)
            if not canon_ds or not cand_lt or not cand_sk or not cand_day:
                continue
            prev = key_dates.get((canon_ds, cand_lt, cand_sk))
            if not prev or cand_day < prev:
                key_dates[(canon_ds, cand_lt, cand_sk)] = cand_day
    except sqlite3.Error:
        return {}, {}
    finally:
        con.close()
    return doc_dates, key_dates


def _resolve_unlocked_metadata_date(trusted, *, from_system=False, created_at=None, include_created_fallback=True):
    """Canonical unlocked fallback order shared by SQLite fast path and HTTP enrichment.

    When ``from_system=True`` (server-written ``system_metadata``):
      1. ``first_source_date`` with non-empty ``source_qa_ids`` -> ``linked_qa``
      2. ``first_source_date`` (trusted server date even without QA IDs) -> declared / ``session_date`` / ``unconfirmed``
      3. ``session_date`` -> ``session_date``
      4. local calendar day of ``created_at`` -> ``created_at``
    When ``from_system=False`` (user-editable ``external_metadata`` only; never emits ``linked_qa``):
      1. ``session_date`` -> ``session_date``
      2. local calendar day of ``created_at`` -> ``created_at``
      3. ``first_source_date`` (only when ``created_at`` is absent) -> ``unconfirmed``
    """
    qa_ids = sorted({str(x) for x in (trusted.get('source_qa_ids') or ()) if x})
    first_day = calendar_date(trusted.get('first_source_date'))
    session_day = calendar_date(trusted.get('session_date'))
    created_day = local_calendar_date(created_at)

    if from_system:
        if first_day:
            if qa_ids:
                return first_day, 'linked_qa', qa_ids
            declared = trusted.get('source_date_status')
            if declared in ('session_date', 'created_at'):
                status = declared
            elif session_day and first_day == session_day:
                status = 'session_date'
            else:
                status = 'unconfirmed'
            return first_day, status, qa_ids
        if session_day:
            return session_day, 'session_date', qa_ids
        if created_day:
            if include_created_fallback:
                return created_day, 'created_at', qa_ids
            return '', 'created_at', qa_ids
        return '', 'unconfirmed', qa_ids

    if session_day:
        return session_day, 'session_date', []
    if created_day:
        if include_created_fallback:
            return created_day, 'created_at', []
        return '', 'created_at', []
    if first_day:
        return first_day, 'unconfirmed', []
    return '', 'unconfirmed', []


def resolve_lesson_date(
    dataset_id,
    document_id,
    *,
    system_metadata=None,
    external_metadata=None,
    created_at=None,
    doc_prov=None,
    key_prov=None,
    include_created_fallback=True,
):
    """Resolve the authoritative lesson calendar day without mutating SQLite.

    Priority: external provenance_store lock -> trusted metadata
    (``system_metadata`` preferred over ``external_metadata``) via the same
    canonical unlocked fallback order as ``ProvenanceStore.enrich_data_items``.
    """
    canon_ds = uuid_text(dataset_id)
    canon_doc = uuid_text(document_id)
    sys_meta = system_metadata if isinstance(system_metadata, dict) and system_metadata else None
    ext_meta = external_metadata if isinstance(external_metadata, dict) else {}
    from_system = sys_meta is not None
    trusted = sys_meta if from_system else ext_meta

    lt = str(trusted.get('lesson_type') or '').strip()
    sk = str(trusted.get('state_key') or '').strip()
    prov_dates = []
    if isinstance(doc_prov, dict):
        row = doc_prov.get((canon_ds, canon_doc))
        if row:
            if not lt and row[0]:
                lt = row[0]
            if not sk and row[1]:
                sk = row[1]
            if calendar_date(row[2]):
                prov_dates.append(row[2])
    if lt and sk and isinstance(key_prov, dict):
        kd = key_prov.get((canon_ds, lt, sk))
        if calendar_date(kd):
            prov_dates.append(kd)
    if prov_dates:
        return min(prov_dates)

    resolved_date, _, _ = _resolve_unlocked_metadata_date(
        trusted,
        from_system=from_system,
        created_at=created_at,
        include_created_fallback=include_created_fallback,
    )
    return resolved_date


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
        self.db.execute('PRAGMA journal_size_limit=4194304')
        self.db.execute('PRAGMA wal_autocheckpoint=1000')
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
        ds_variants = id_variants(dataset_id) or (str(dataset_id),)
        doc_variants = id_variants(document_id) or (str(document_id),)
        ds_sql, ds_params = sql_in_clause(ds_variants)
        doc_sql, doc_params = sql_in_clause(doc_variants)
        doc_rows = self.db.execute(
            f'''SELECT lesson_type,state_key,first_source_date,source_date_status,source_qa_ids_json
                FROM document_provenance WHERE dataset_id {ds_sql} AND document_id {doc_sql}''',
            (*ds_params, *doc_params),
        ).fetchall()
        lt = str(lesson_type or '').strip()
        sk = str(state_key or '').strip()
        for row in doc_rows:
            if not lt and row[0]:
                lt = str(row[0]).strip()
            if not sk and row[1]:
                sk = str(row[1]).strip()
        key_rows = []
        if lt and sk:
            key_rows = self.db.execute(
                f'''SELECT first_source_date,source_date_status
                    FROM keyed_provenance WHERE dataset_id {ds_sql} AND lesson_type=? AND state_key=?''',
                (*ds_params, lt, sk),
            ).fetchall()
        if not doc_rows and not key_rows:
            return None
        dates = []
        qas = []
        statuses = []
        for doc_row in doc_rows:
            if calendar_date(doc_row[2]):
                dates.append(doc_row[2])
                if _STATUS_RANK.get(doc_row[3], 0) > 0:
                    statuses.append(doc_row[3])
            elif doc_row[3]:
                statuses.append(doc_row[3])
            try:
                parsed_qas = json.loads(doc_row[4])
                if isinstance(parsed_qas, list):
                    qas.extend(parsed_qas)
            except ValueError:
                pass
        for key_row in key_rows:
            if calendar_date(key_row[0]):
                dates.append(key_row[0])
                if _STATUS_RANK.get(key_row[1], 0) > 0:
                    statuses.append(key_row[1])
        merged_qas = sorted({str(x) for x in qas if x})
        if merged_qas:
            statuses.append('linked_qa')
        best = min(dates) if dates else ''
        status = _best_status(*statuses)
        return {
            'first_source_date': best,
            'source_date_status': status if best else 'unconfirmed',
            'source_qa_ids': merged_qas,
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
        ds = uuid_text(dataset_id) or str(dataset_id)
        doc = uuid_text(document_id) or str(document_id)
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
                parsed = calendar_date(raw_d)
                if parsed:
                    dates.append(parsed)
            if doc_row and calendar_date(doc_row[2]):
                dates.append(doc_row[2])
            if key_row and calendar_date(key_row[0]):
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
                    if explicit_status != 'linked_qa' or merged_qas:
                        inferred.append(explicit_status)
                if doc_row and calendar_date(doc_row[2]) and _STATUS_RANK.get(doc_row[3], 0) > 0:
                    inferred.append(doc_row[3])
                if key_row and calendar_date(key_row[0]) and _STATUS_RANK.get(key_row[1], 0) > 0:
                    inferred.append(key_row[1])
                status = _best_status(*inferred)
                key_status = _best_status(
                    status,
                    key_row[1] if (key_row and calendar_date(key_row[0])) else 'unconfirmed',
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
        """Read-only enrichment of Cognee ``/datasets/{id}/data`` items.

        Strictly side-effect free: never writes to SQLite from GET requests,
        prefers ``system_metadata`` over user-editable ``external_metadata``,
        and never labels ``source_date_status='linked_qa'`` without QA evidence.
        """
        if not isinstance(items, list):
            return items
        ds = uuid_text(dataset_id) or str(dataset_id)
        enriched = []
        for item in items:
            if not isinstance(item, dict) or not item.get('id'):
                enriched.append(item)
                continue
            meta_key = ('externalMetadata' if isinstance(item.get('externalMetadata'), dict)
                        else ('external_metadata' if isinstance(item.get('external_metadata'), dict) else None))
            sys_meta = (item.get('systemMetadata') if isinstance(item.get('systemMetadata'), dict) and item.get('systemMetadata')
                        else (item.get('system_metadata') if isinstance(item.get('system_metadata'), dict) and item.get('system_metadata')
                              else None))
            ext_meta = item[meta_key] if meta_key is not None else {}
            trusted = sys_meta if sys_meta is not None else ext_meta
            if meta_key is None or not is_lesson_metadata(trusted):
                enriched.append(item)
                continue
            meta = dict(ext_meta)
            doc_id = uuid_text(item['id']) or str(item['id'])
            lt = str(trusted.get('lesson_type') or '').strip()
            sk = str(trusted.get('state_key') or '').strip()
            found = self.lookup(ds, doc_id, lesson_type=lt, state_key=sk)
            if found and found['first_source_date']:
                meta['first_source_date'] = found['first_source_date']
                meta['source_date_status'] = found['source_date_status']
                if found['source_qa_ids']:
                    meta['source_qa_ids'] = found['source_qa_ids']
                enriched.append({**item, meta_key: meta})
                continue

            resolved_date, origin_status, qa_ids = _resolve_unlocked_metadata_date(
                trusted,
                from_system=(sys_meta is not None),
                created_at=item.get('createdAt') or item.get('created_at'),
                include_created_fallback=True,
            )
            if resolved_date:
                meta['first_source_date'] = resolved_date
            meta['source_date_status'] = origin_status
            if qa_ids:
                meta['source_qa_ids'] = qa_ids
            enriched.append({**item, meta_key: meta})
        return enriched

    async def sync_dataset(self, dataset_id, *, fetch_rows=None, fetch_session_qa_dates=None, session_ids=None):
        """Sync provenance dates from Cognee's dataset rows (preferring trusted ``system_metadata``)."""
        ds = uuid_text(dataset_id) or str(dataset_id)
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
            if not is_lesson_metadata(trusted):
                continue
            doc_id = str(row.get('id') if isinstance(row, dict) else getattr(row, 'id', ''))
            if not doc_id:
                continue
            lt = str(trusted.get('lesson_type') or '').strip()
            sk = str(trusted.get('state_key') or '').strip()
            qa_ids = [str(x) for x in (trusted.get('source_qa_ids') or ()) if x]
            candidates = []
            origin_status = 'unconfirmed'
            if calendar_date(trusted.get('first_source_date')):
                candidates.append(trusted['first_source_date'])
                if qa_ids:
                    origin_status = 'linked_qa'
                elif calendar_date(trusted.get('session_date')):
                    origin_status = 'session_date'
            elif calendar_date(trusted.get('session_date')):
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
                qa_parsed = [parsed for d in (qa_dates or ()) if (parsed := calendar_date(d))]
                if qa_parsed:
                    candidates.extend(qa_parsed)
                    origin_status = 'linked_qa'
            if not candidates:
                raw_created = row.get('created_at') if isinstance(row, dict) else getattr(row, 'created_at', None)
                if raw_created is None and isinstance(row, dict):
                    raw_created = row.get('createdAt')
                created_day = local_calendar_date(raw_created)
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

    def checkpoint(self, mode: str = 'TRUNCATE') -> bool:
        """Run WAL checkpoint to truncate WAL and prevent unbounded growth, safely handling locks."""
        try:
            mode_str = str(mode or 'TRUNCATE').upper()
            if mode_str not in ('PASSIVE', 'FULL', 'RESTART', 'TRUNCATE'):
                mode_str = 'TRUNCATE'
            cur = self.db.execute(f'PRAGMA wal_checkpoint({mode_str})')
            row = cur.fetchone()
            return bool(row and row[0] == 0)
        except (sqlite3.OperationalError, sqlite3.Error):
            return False
        except Exception:
            return False

    def close(self):
        try:
            self.checkpoint('TRUNCATE')
        except Exception:
            pass
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
