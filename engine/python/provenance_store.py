"""External SQLite WAL provenance date index for distilled coding lessons.

Replaces source edits to Cognee's ``distill.py`` and full-package copying.
Stores ONLY dataset/document/QA identifiers, ``lesson_type``, ``state_key``,
and normalized ``YYYY-MM-DD`` calendar dates — never lesson text or transcripts.
"""
from datetime import date, datetime, timezone
from functools import lru_cache
import json
import os
from pathlib import Path
import re
import sqlite3
import threading
import uuid

from _runtime_common import sql_in_clause
from project_policy import PolicyError
from sqlite_ro import (
    decode_node_set,
    extract_trusted_metadata,
    id_variants,
    official_sqlite_ro,
    open_official_sqlite_ro,
    uuid_text,
)

_CALENDAR_RE = re.compile(r'^\d{4}-\d{2}-\d{2}$')
_STATUS_RANK = {
    'unconfirmed': 0,
    'created_at': 1,
    'created_at_fallback': 1,
    'legacy_json_locked': 2,
    'session_date': 3,
    'linked_qa': 4,
}
_DATA_ROUTE_RE = re.compile(r'^/api/v1/datasets/([0-9a-fA-F-]{36})/data/?$')
_IMPROVE_ROUTE_RE = re.compile(r'^/api/v1/improve/?$')


def _iso_now():
    return datetime.now(timezone.utc).isoformat(timespec='microseconds')


@lru_cache(maxsize=4096)
def _validate_calendar_head(head: str):
    if len(head) != 10 or head[4] != '-' or head[7] != '-':
        return None
    try:
        date.fromisoformat(head)
    except ValueError:
        return None
    return head


def _calendar_date(value):
    """Extract a validated ``YYYY-MM-DD`` calendar date from a date or ISO-8601 timestamp."""
    if not isinstance(value, str):
        return None
    n = len(value)
    if n == 10 and value[4] == '-' and value[7] == '-':
        return _validate_calendar_head(value)
    text = value.strip()
    n = len(text)
    if n < 10:
        return None
    head = _validate_calendar_head(text[:10])
    if head is None:
        return None
    if n == 10:
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


def _select_best_date_evidence(pairs) -> tuple[str, str]:
    """Select the earliest valid calendar date paired strictly with evidence proving THAT date.

    When multiple evidence entries share the exact same earliest date, pick the
    highest evidence rank among them. A later strong date NEVER upgrades the status
    label attached to an earlier weaker date.
    """
    valid: list[tuple[str, str]] = []
    for raw_day, raw_status in pairs or ():
        day = _calendar_date(raw_day)
        if not day:
            continue
        st = raw_status if _STATUS_RANK.get(raw_status, 0) > 0 else 'created_at'
        valid.append((day, st))
    if not valid:
        return '', 'unconfirmed'
    earliest = min(d for d, _ in valid)
    tied_statuses = [st for d, st in valid if d == earliest]
    return earliest, _best_status(*tied_statuses)


def _inferred_meta_date_status(meta: dict, *, has_qa_ids: bool) -> str:
    """Resolve evidence status for ``first_source_date`` without fabricating ``linked_qa``."""
    raw_status = str(meta.get('source_date_status') or '').strip()
    if has_qa_ids:
        return 'linked_qa'
    if raw_status in _STATUS_RANK and raw_status not in ('unconfirmed', 'linked_qa'):
        return raw_status
    return 'session_date'


def _is_lesson_metadata(meta):
    if not isinstance(meta, dict):
        return False
    if meta.get('lesson_type') or meta.get('first_source_date') or meta.get('source_date_status'):
        return True
    return 'session_learnings' in decode_node_set(meta.get('node_set'))


calendar_date = _calendar_date
local_calendar_date = _local_calendar_date
is_lesson_metadata = _is_lesson_metadata


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
        con = open_official_sqlite_ro(path)
    except (PolicyError, sqlite3.Error):
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


def lesson_metadata_days(trusted, *, from_system, parse_day=calendar_date):
    """Return the preferred metadata day and the legacy date fallback."""
    first = trusted.get('first_source_date')
    session = trusted.get('session_date')
    first_day = parse_day(first) if isinstance(first, str) else None
    session_day = parse_day(session) if isinstance(session, str) else None
    return (first_day or session_day, '') if from_system else (session_day, first_day)


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
    created_day = local_calendar_date(created_at)

    if from_system:
        first_day = calendar_date(trusted.get('first_source_date'))
        session_day = calendar_date(trusted.get('session_date'))
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

    session_day, first_day = lesson_metadata_days(trusted, from_system=False)
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

    def __init__(self, path, *, cognee_db_path=None):
        self.path = Path(path)
        self.cognee_db_path = Path(cognee_db_path) if cognee_db_path else None
        self._write_lock = threading.Lock()
        self._last_synced_rowid: dict[str, int] = {}
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
        with self._write_lock:
            self.db.executescript('''
              CREATE TABLE IF NOT EXISTS provenance_meta (
                singleton INTEGER PRIMARY KEY CHECK(singleton=1),
                last_sync_seq INTEGER NOT NULL DEFAULT 0
              );
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
                sync_seq INTEGER NOT NULL DEFAULT 0,
                PRIMARY KEY(dataset_id, document_id)
              );
            ''')
            self._ensure_sync_seq_schema_locked()

    def _ensure_sync_seq_schema_locked(self):
        cols = {
            row[1]
            for row in self.db.execute('PRAGMA table_info(document_provenance)').fetchall()
        }
        self.db.execute('BEGIN IMMEDIATE')
        try:
            if 'sync_seq' not in cols:
                self.db.execute(
                    'ALTER TABLE document_provenance ADD COLUMN sync_seq INTEGER NOT NULL DEFAULT 0'
                )
                self.db.execute('UPDATE document_provenance SET sync_seq = rowid WHERE sync_seq = 0')
            self.db.execute(
                'CREATE INDEX IF NOT EXISTS idx_document_provenance_sync_seq '
                'ON document_provenance(sync_seq)'
            )
            self.db.execute(
                'INSERT OR IGNORE INTO provenance_meta(singleton, last_sync_seq) VALUES(1, 0)'
            )
            max_seq = int(
                self.db.execute('SELECT COALESCE(MAX(sync_seq), 0) FROM document_provenance').fetchone()[0]
            )
            self.db.execute(
                'UPDATE provenance_meta SET last_sync_seq = MAX(last_sync_seq, ?) WHERE singleton = 1',
                (max_seq,),
            )
            self.db.execute('COMMIT')
        except Exception:
            if self.db.in_transaction:
                self.db.execute('ROLLBACK')
            raise

    def _next_sync_seq_locked(self) -> int:
        self.db.execute(
            'UPDATE provenance_meta SET last_sync_seq = last_sync_seq + 1 WHERE singleton = 1'
        )
        return int(
            self.db.execute('SELECT last_sync_seq FROM provenance_meta WHERE singleton = 1').fetchone()[0]
        )

    def max_sync_seq(self) -> int:
        with self._write_lock:
            row = self.db.execute(
                'SELECT last_sync_seq FROM provenance_meta WHERE singleton = 1'
            ).fetchone()
            return int(row[0]) if row else 0

    def changes_since(self, after_sync_seq: int, *, limit: int = 1000) -> list[dict]:
        with self._write_lock:
            rows = self.db.execute(
                '''SELECT dataset_id, document_id, lesson_type, state_key,
                          first_source_date, source_date_status, source_qa_ids_json,
                          updated_at, sync_seq
                   FROM document_provenance
                   WHERE sync_seq > ?
                   ORDER BY sync_seq ASC
                   LIMIT ?''',
                (int(after_sync_seq), max(1, int(limit))),
            ).fetchall()
        out = []
        for row in rows:
            try:
                qas = json.loads(row[6]) if row[6] else []
            except ValueError:
                qas = []
            out.append({
                'dataset_id': row[0],
                'document_id': row[1],
                'lesson_type': row[2],
                'state_key': row[3],
                'first_source_date': row[4],
                'source_date_status': row[5],
                'source_qa_ids': sorted({str(x) for x in qas if x}),
                'updated_at': row[7],
                'sync_seq': int(row[8]),
            })
        return out

    def lookup(self, dataset_id, document_id, *, lesson_type='', state_key=''):
        ds_sql, ds_params = sql_in_clause(id_variants(dataset_id) or (str(dataset_id),))
        doc_sql, doc_params = sql_in_clause(id_variants(document_id) or (str(document_id),))
        with self._write_lock:
            doc_rows = self.db.execute(
                f'''SELECT lesson_type,state_key,first_source_date,source_date_status,source_qa_ids_json
                    FROM document_provenance WHERE dataset_id {ds_sql} AND document_id {doc_sql}''',
                (*ds_params, *doc_params),
            ).fetchall()
            lt = str(lesson_type or '').strip()
            sk = str(state_key or '').strip()
            for row in doc_rows:
                lt = lt or row[0]
                sk = sk or row[1]
            key_rows = []
            if lt and sk:
                key_rows = self.db.execute(
                    f'''SELECT first_source_date,source_date_status
                        FROM keyed_provenance WHERE dataset_id {ds_sql} AND lesson_type=? AND state_key=?''',
                    (*ds_params, lt, sk),
                ).fetchall()
        if not doc_rows and not key_rows:
            return None
        pairs = [(row[2], row[3]) for row in doc_rows] + list(key_rows)
        qas = []
        for row in doc_rows:
            try:
                parsed = json.loads(row[4])
                if isinstance(parsed, list):
                    qas.extend(parsed)
            except ValueError:
                pass
        best, status = _select_best_date_evidence(pairs)
        return {
            'first_source_date': best,
            'source_date_status': status if best else 'unconfirmed',
            'source_qa_ids': sorted({str(x) for x in qas if x}),
        }

    def _upsert_document_locked(
        self,
        dataset_id,
        document_id,
        *,
        lesson_type='',
        state_key='',
        candidate_dates=(),
        candidate_pairs=(),
        source_qa_ids=(),
        explicit_status=None,
        now=None,
    ):
        ds = str(dataset_id)
        doc = str(document_id)
        lt = str(lesson_type or '').strip()
        sk = str(state_key or '').strip()
        ts = now or _iso_now()

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

        existing_qas = []
        if doc_row and doc_row[4]:
            try:
                existing_qas = json.loads(doc_row[4])
            except ValueError:
                existing_qas = []
        merged_qas = sorted({str(x) for x in (*existing_qas, *(source_qa_ids or ())) if x})
        merged_qas_json = json.dumps(merged_qas)

        pairs: list[tuple[str, str]] = []
        cand_status = explicit_status if _STATUS_RANK.get(explicit_status, 0) > 0 else (
            'linked_qa' if (source_qa_ids or explicit_status is None) else 'created_at'
        )
        for raw_d in candidate_dates or ():
            parsed = _calendar_date(raw_d)
            if parsed:
                pairs.append((parsed, cand_status))
        for raw_d, raw_st in candidate_pairs or ():
            parsed = _calendar_date(raw_d)
            if parsed:
                st = raw_st if _STATUS_RANK.get(raw_st, 0) > 0 else 'created_at'
                pairs.append((parsed, st))
        if doc_row and _calendar_date(doc_row[2]):
            pairs.append((doc_row[2], doc_row[3] if _STATUS_RANK.get(doc_row[3], 0) > 0 else 'created_at'))
        if key_row and _calendar_date(key_row[0]):
            pairs.append((key_row[0], key_row[1] if _STATUS_RANK.get(key_row[1], 0) > 0 else 'created_at'))

        resolved_date, status = _select_best_date_evidence(pairs)
        key_status = status if resolved_date else 'unconfirmed'

        if effective_lt and effective_sk and resolved_date:
            need_key_write = (
                key_row is None
                or not key_row[0]
                or resolved_date < key_row[0]
                or (resolved_date == key_row[0] and _STATUS_RANK.get(key_status, 0) > _STATUS_RANK.get(key_row[1], 0))
            )
            if need_key_write:
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
                         source_date_status=CASE
                           WHEN keyed_provenance.first_source_date='' OR excluded.first_source_date < keyed_provenance.first_source_date
                             THEN excluded.source_date_status
                           WHEN excluded.first_source_date = keyed_provenance.first_source_date
                             THEN excluded.source_date_status
                           ELSE keyed_provenance.source_date_status
                         END,
                         updated_at=excluded.updated_at''',
                    (ds, effective_lt, effective_sk, resolved_date, key_status, ts),
                )

        # Always write if row is new, date improves, status rank upgrades (including same-day),
        # or lesson_type/state_key/source_qa_ids gain richer provenance metadata.
        existing_qas_norm = json.dumps(sorted({str(x) for x in existing_qas if x}))
        doc_changed = (
            doc_row is None
            or doc_row[0] != effective_lt
            or doc_row[1] != effective_sk
            or doc_row[2] != resolved_date
            or doc_row[3] != status
            or existing_qas_norm != merged_qas_json
        )
        if doc_changed:
            next_seq = self._next_sync_seq_locked()
            self.db.execute(
                '''INSERT INTO document_provenance
                   (dataset_id,document_id,lesson_type,state_key,first_source_date,
                    source_date_status,source_qa_ids_json,updated_at,sync_seq)
                   VALUES(?,?,?,?,?,?,?,?,?)
                   ON CONFLICT(dataset_id,document_id) DO UPDATE SET
                     lesson_type=CASE WHEN excluded.lesson_type!='' THEN excluded.lesson_type ELSE document_provenance.lesson_type END,
                     state_key=CASE WHEN excluded.state_key!='' THEN excluded.state_key ELSE document_provenance.state_key END,
                     first_source_date=excluded.first_source_date,
                     source_date_status=excluded.source_date_status,
                     source_qa_ids_json=excluded.source_qa_ids_json,
                     updated_at=excluded.updated_at,
                     sync_seq=excluded.sync_seq''',
                (ds, doc, effective_lt, effective_sk, resolved_date, status, merged_qas_json, ts, next_seq),
            )
        return {
            'first_source_date': resolved_date,
            'source_date_status': status,
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
        with self._write_lock:
            self.db.execute('BEGIN IMMEDIATE')
            try:
                result = self._upsert_document_locked(
                    dataset_id,
                    document_id,
                    lesson_type=lesson_type,
                    state_key=state_key,
                    candidate_dates=candidate_dates,
                    source_qa_ids=source_qa_ids,
                    explicit_status=explicit_status,
                )
                self.db.execute('COMMIT')
            except Exception:
                if self.db.in_transaction:
                    self.db.execute('ROLLBACK')
                raise
        return result

    def record_documents_batch(self, records):
        """Record multiple provenance entries inside a single ``BEGIN IMMEDIATE`` transaction."""
        batch = list(records or ())
        if not batch:
            return []
        now = _iso_now()
        results = []
        with self._write_lock:
            self.db.execute('BEGIN IMMEDIATE')
            try:
                for entry in batch:
                    result = self._upsert_document_locked(
                        entry['dataset_id'],
                        entry['document_id'],
                        lesson_type=entry.get('lesson_type', ''),
                        state_key=entry.get('state_key', ''),
                        candidate_dates=entry.get('candidate_dates', ()),
                        candidate_pairs=entry.get('candidate_pairs', ()),
                        source_qa_ids=entry.get('source_qa_ids', ()),
                        explicit_status=entry.get('explicit_status'),
                        now=now,
                    )
                    results.append(result)
                self.db.execute('COMMIT')
            except Exception:
                if self.db.in_transaction:
                    self.db.execute('ROLLBACK')
                raise
        return results

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

    def _resolve_cognee_db_path(self, cognee_db_path=None):
        if cognee_db_path:
            return Path(cognee_db_path)
        if self.cognee_db_path is not None:
            return self.cognee_db_path
        db_dir = os.environ.get('DB_PATH')
        if db_dir:
            return Path(db_dir) / 'cognee_db'
        return None

    def _read_cognee_dataset_rows(
        self,
        dataset_id: str,
        cognee_db_path=None,
        *,
        session_ids=None,
        min_rowid: int = 0,
    ) -> tuple[list[dict], int]:
        db_path = self._resolve_cognee_db_path(cognee_db_path)
        if db_path is None:
            return [], min_rowid
        variants = id_variants(dataset_id)
        if not variants:
            return [], min_rowid
        session_filter = {str(s).strip() for s in (session_ids or ()) if str(s or '').strip()}
        rows_out = []
        seen_ids = set()
        max_rowid = int(min_rowid or 0)
        with official_sqlite_ro(db_path) as con:
            cols = {
                str(r[1])
                for r in con.execute('PRAGMA table_info(data)').fetchall()
            }
            sys_col = 'system_metadata' if 'system_metadata' in cols else 'NULL'
            node_col = 'node_set' if 'node_set' in cols else 'NULL'
            for variant in variants:
                if min_rowid > 0 and not session_filter:
                    sql = f'''SELECT rowid, id, {sys_col}, external_metadata, {node_col}, created_at
                              FROM data
                              WHERE dataset_id = ? AND rowid > ?
                              ORDER BY rowid ASC'''
                    params = (variant, int(min_rowid))
                else:
                    sql = f'''SELECT rowid, id, {sys_col}, external_metadata, {node_col}, created_at
                              FROM data
                              WHERE dataset_id = ?
                              ORDER BY rowid ASC'''
                    params = (variant,)
                for row in con.execute(sql, params):
                    rid = int(row[0] or 0)
                    if rid > max_rowid:
                        max_rowid = rid
                    if session_filter and rid <= min_rowid:
                        # Skip already-synced historical rows that do not mention target sessions
                        raw_blob = f'{row[2] or ""} {row[3] or ""} {row[4] or ""}'
                        if not any(sid in raw_blob for sid in session_filter):
                            continue
                    doc_id = uuid_text(row[1])
                    if not doc_id or doc_id in seen_ids:
                        continue
                    seen_ids.add(doc_id)
                    rows_out.append({
                        'id': doc_id,
                        'system_metadata': row[2],
                        'external_metadata': row[3],
                        'node_set': row[4],
                        'created_at': row[5],
                    })
        return rows_out, max_rowid

    async def sync_dataset(
        self,
        dataset_id,
        *,
        fetch_rows=None,
        fetch_session_qa_dates=None,
        session_ids=None,
        cognee_db_path=None,
    ):
        """Sync provenance dates from Cognee's dataset rows (preferring trusted ``system_metadata``)."""
        ds = str(dataset_id)
        new_max_rowid = self._last_synced_rowid.get(ds, 0)
        if fetch_rows is not None:
            rows = fetch_rows(ds)
            if hasattr(rows, '__await__'):
                rows = await rows
        else:
            prev_rid = self._last_synced_rowid.get(ds, 0) if session_ids else 0
            rows, new_max_rowid = self._read_cognee_dataset_rows(
                ds,
                cognee_db_path=cognee_db_path,
                session_ids=session_ids,
                min_rowid=prev_rid,
            )
        if not rows:
            if new_max_rowid > self._last_synced_rowid.get(ds, 0):
                self._last_synced_rowid[ds] = new_max_rowid
            return
        batch = []
        for row in rows:
            if isinstance(row, dict):
                sys_meta = row.get('system_metadata')
                ext_meta = row.get('external_metadata')
                raw_node_set = row.get('node_set')
                raw_id = row.get('id')
                raw_created = row.get('created_at')
                if raw_created is None:
                    raw_created = row.get('createdAt')
            else:
                sys_meta = getattr(row, 'system_metadata', None)
                ext_meta = getattr(row, 'external_metadata', None)
                raw_node_set = getattr(row, 'node_set', None)
                raw_id = getattr(row, 'id', '')
                raw_created = getattr(row, 'created_at', None)
            # Security invariant: system_metadata unconditionally overrides external_metadata
            # and double-encoded node_set is normalized.
            trusted = extract_trusted_metadata(sys_meta, ext_meta, raw_node_set)
            if not _is_lesson_metadata(trusted):
                continue
            doc_id = uuid_text(raw_id) if raw_id else ''
            if not doc_id:
                continue
            lt = str(trusted.get('lesson_type') or '').strip()
            sk = str(trusted.get('state_key') or '').strip()
            qa_ids = list(trusted.get('source_qa_ids') or ())
            candidate_pairs = []
            if _calendar_date(trusted.get('first_source_date')):
                candidate_pairs.append((
                    trusted['first_source_date'],
                    _inferred_meta_date_status(trusted, has_qa_ids=bool(qa_ids)),
                ))
            elif _calendar_date(trusted.get('session_date')):
                candidate_pairs.append((trusted['session_date'], 'session_date'))
            if fetch_session_qa_dates is not None and (qa_ids or trusted.get('session_id') or session_ids):
                qa_dates = fetch_session_qa_dates(
                    qa_ids=qa_ids,
                    session_id=trusted.get('session_id') or (session_ids[0] if session_ids else None),
                    trusted_metadata=trusted,
                )
                if hasattr(qa_dates, '__await__'):
                    qa_dates = await qa_dates
                for d in (qa_dates or ()):
                    parsed = _calendar_date(d)
                    if parsed:
                        candidate_pairs.append((parsed, 'linked_qa'))
            if not candidate_pairs:
                created_day = _local_calendar_date(raw_created)
                if created_day:
                    candidate_pairs.append((created_day, 'created_at'))
            batch.append({
                'dataset_id': ds,
                'document_id': doc_id,
                'lesson_type': lt,
                'state_key': sk,
                'candidate_pairs': candidate_pairs,
                'source_qa_ids': qa_ids,
                'explicit_status': 'unconfirmed',
            })
        if batch:
            self.record_documents_batch(batch)
        if new_max_rowid > self._last_synced_rowid.get(ds, 0):
            self._last_synced_rowid[ds] = new_max_rowid

    def backfill_legacy_dates(self, entries) -> dict:
        """Batch-record verified legacy ``knowledge-source-dates`` entries as ``legacy_json_locked`` (rank 2).

        Never upgrades evidence to ``linked_qa`` and never overwrites an earlier
        or higher-ranked date.
        """
        if not isinstance(entries, list):
            raise ValueError('entries must be a list')
        batch = []
        skipped = 0
        for item in entries:
            if not isinstance(item, dict):
                skipped += 1
                continue
            ds = uuid_text(item.get('dataset_id'))
            doc_id = uuid_text(item.get('document_id'))
            day = _calendar_date(item.get('first_source_date'))
            if not ds or not doc_id or not day:
                skipped += 1
                continue
            batch.append({
                'dataset_id': ds,
                'document_id': doc_id,
                'lesson_type': str(item.get('lesson_type') or '').strip(),
                'state_key': str(item.get('state_key') or '').strip(),
                'candidate_dates': [day],
                'source_qa_ids': [],
                'explicit_status': 'legacy_json_locked',
            })
        if batch:
            self.record_documents_batch(batch)
        return {'imported': len(batch), 'skipped': skipped}

    def checkpoint(self, mode: str = 'TRUNCATE') -> bool:
        """Run WAL checkpoint to truncate WAL and prevent unbounded growth, safely handling locks."""
        try:
            mode_str = str(mode or 'TRUNCATE').upper()
            if mode_str not in ('PASSIVE', 'FULL', 'RESTART', 'TRUNCATE'):
                mode_str = 'TRUNCATE'
            with self._write_lock:
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
        with self._write_lock:
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


def install_provenance_routes(app, store, config):
    """Mount management-authenticated single-writer provenance backfill endpoint on official_service_dev."""
    import asyncio
    import hmac
    from fastapi import APIRouter, HTTPException, Request
    from _runtime_common import read_secret

    secret = read_secret(config['project_control']['management_token_file'])
    router = APIRouter(prefix='/api/v1/memlane/provenance')

    @router.post('/backfill-legacy-dates')
    async def backfill_legacy_dates_route(request: Request):
        if not hmac.compare_digest(request.headers.get('x-memlane-management', ''), secret):
            raise HTTPException(403, 'management_required')
        try:
            payload = await request.json()
        except ValueError:
            raise HTTPException(400, 'invalid_request_fields') from None
        if not isinstance(payload, dict) or set(payload) != {'entries'} or not isinstance(payload['entries'], list):
            raise HTTPException(400, 'invalid_request_fields')
        if len(payload['entries']) > 2000:
            raise HTTPException(400, 'invalid_request_fields')
        result = await asyncio.to_thread(store.backfill_legacy_dates, payload['entries'])
        return result

    app.include_router(router)
    return router
