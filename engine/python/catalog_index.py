"""Rebuildable minimal metadata projection (catalog-index.sqlite3) for O(log N + K) /v2 queries.

Strictly obeys zero-body-copy red lines: persists ONLY whitelisted structural metadata
(never body text, raw_text, why_learned, metadata_json, or session copies).
Uses a single writer with 4-tuple dataset fingerprint reconciliation, atomic generation
publishing, same-direction tuple Keyset indexes, and corruption self-healing rebuild.
"""
import calendar
from concurrent.futures import ThreadPoolExecutor
from datetime import date, datetime, timezone
from functools import lru_cache
import hashlib
from operator import itemgetter
import os
from pathlib import Path
import re
import sqlite3
import stat
import threading
import time
from zoneinfo import ZoneInfo, ZoneInfoNotFoundError

from project_policy import PolicyError
from provenance_store import _calendar_date, _is_lesson_metadata
from sqlite_ro import (
    _LESSON_CATEGORY_LABELS,
    common_category_from_meta,
    extract_trusted_metadata,
    is_knowledge_meta,
    iso_text,
    knowledge_category_label,
    official_sqlite_ro,
    open_official_sqlite_ro,
    parse_json_value,
    unique_learning_session,
    uuid_hex,
    uuid_text,
)

CATALOG_SCHEMA_VERSION = 2
_MAX_IN_BATCH = 400
_DERIVED_NODE_SETS = frozenset({'user_sessions_from_cache', 'agent_trace_feedbacks', 'session_learnings'})
_TS_FRAC_RE = re.compile(
    r'^(\d{4}-\d{2}-\d{2})[T ](\d{2}:\d{2}:\d{2})(?:\.(\d+))?(Z|[+-]\d{2}:\d{2})?$'
)


def current_local_tz_name() -> str:
    """Detect the current named IANA timezone (DST-aware) with deterministic fallback."""
    env_tz = (os.environ.get('TZ') or '').strip()
    if env_tz:
        clean = env_tz[1:] if env_tz.startswith(':') else env_tz
        try:
            ZoneInfo(clean)
            return clean
        except (ZoneInfoNotFoundError, ValueError):
            pass
    localtime = Path('/etc/localtime')
    try:
        if localtime.is_symlink():
            target = os.readlink(localtime)
            marker = 'zoneinfo/'
            if marker in target:
                candidate = target.split(marker, 1)[1].strip('/')
                try:
                    ZoneInfo(candidate)
                    return candidate
                except (ZoneInfoNotFoundError, ValueError):
                    pass
    except OSError:
        pass
    local_tz = datetime.now().astimezone().tzinfo
    return str(local_tz or 'UTC')


def _tzinfo_for_name(tz_name: str):
    if tz_name:
        try:
            return ZoneInfo(tz_name)
        except (ZoneInfoNotFoundError, ValueError):
            pass
    return datetime.now().astimezone().tzinfo or timezone.utc


@lru_cache(maxsize=4096)
def _ymd_epoch_seconds(date_part: str):
    try:
        y = int(date_part[0:4])
        m = int(date_part[5:7])
        d = int(date_part[8:10])
        date(y, m, d)
        return calendar.timegm((y, m, d, 0, 0, 0))
    except ValueError:
        return None


@lru_cache(maxsize=4096)
def _day_index_to_ymd(day_index: int) -> str:
    return date.fromordinal(day_index + 719163).isoformat()


@lru_cache(maxsize=4096)
def _utc_day_constant_offset(date_part: str, tz_info):
    """Return constant UTC offset seconds for ``date_part`` in ``tz_info``, or ``None`` on DST transition days."""
    try:
        y = int(date_part[0:4])
        m = int(date_part[5:7])
        d = int(date_part[8:10])
        o0 = datetime(y, m, d, 0, 0, 0, tzinfo=timezone.utc).astimezone(tz_info).utcoffset()
        o1 = datetime(y, m, d, 23, 59, 59, tzinfo=timezone.utc).astimezone(tz_info).utcoffset()
        if o0 is not None and o0 == o1:
            return int(o0.total_seconds())
    except (ValueError, OverflowError):
        pass
    return None


def normalize_utc_ns(raw_value) -> tuple[str, int, bool]:
    """Normalize an ISO-8601/SQLite timestamp to fixed-width 9-digit UTC nanoseconds.

    Returns ``(utc_ns_iso, epoch_ns, is_invalid)``.
    Empty or unparseable timestamps return ``('', 0, True)``.
    """
    if not isinstance(raw_value, str) or not raw_value:
        return '', 0, True
    text = raw_value if (raw_value[0] > ' ' and raw_value[-1] > ' ') else raw_value.strip()
    n = len(text)
    if n < 19:
        return '', 0, True
    # Zero-regex fast path for standard UTC / naive ISO-8601 timestamps.
    if (
        text[4] == '-'
        and text[7] == '-'
        and (text[10] == 'T' or text[10] == ' ')
        and text[13] == ':'
        and text[16] == ':'
        and '0' <= text[11] <= '2'
        and '0' <= text[12] <= '9'
        and '0' <= text[14] <= '5'
        and '0' <= text[15] <= '9'
        and '0' <= text[17] <= '5'
        and '0' <= text[18] <= '9'
    ):
        tail = text[19:]
        frac_part = None
        if not tail or tail == 'Z' or tail == '+00:00' or tail == '-00:00':
            frac_part = ''
        elif tail[0] == '.':
            if tail[-1] == 'Z':
                cand = tail[1:-1]
            elif tail.endswith('+00:00') or tail.endswith('-00:00'):
                cand = tail[1:-6]
            elif tail[1:].isdigit():
                cand = tail[1:]
            else:
                cand = None
            if cand and cand.isascii() and cand.isdigit():
                frac_part = cand
        if frac_part is not None:
            date_part = text[0:10]
            day_sec = _ymd_epoch_seconds(date_part)
            if day_sec is None:
                return '', 0, True
            hh = int(text[11:13])
            if hh > 23:
                return '', 0, True
            mm = int(text[14:16])
            ss = int(text[17:19])
            epoch_sec = day_sec + hh * 3600 + mm * 60 + ss
            fl = len(frac_part)
            if fl == 0:
                return f'{date_part}T{text[11:19]}.000000000Z', epoch_sec * 1_000_000_000, False
            if fl == 6:
                frac_ns = int(frac_part) * 1000
                return f'{date_part}T{text[11:19]}.{frac_part}000Z', epoch_sec * 1_000_000_000 + frac_ns, False
            if fl == 9:
                frac_ns = int(frac_part)
                return f'{date_part}T{text[11:19]}.{frac_part}Z', epoch_sec * 1_000_000_000 + frac_ns, False
            padded = (frac_part + '000000000')[:9]
            return f'{date_part}T{text[11:19]}.{padded}Z', epoch_sec * 1_000_000_000 + int(padded), False

    match = _TS_FRAC_RE.match(text)
    if match is None:
        return '', 0, True
    date_part, time_part, frac_part, offset_part = match.groups()
    frac_ns = int((frac_part or '').ljust(9, '0')[:9]) if frac_part else 0
    if offset_part in (None, 'Z', '+00:00', '-00:00'):
        day_sec = _ymd_epoch_seconds(date_part)
        if day_sec is None:
            return '', 0, True
        hh = int(time_part[0:2])
        mm = int(time_part[3:5])
        ss = int(time_part[6:8])
        if hh > 23 or mm > 59 or ss > 59:
            return '', 0, True
        epoch_sec = day_sec + hh * 3600 + mm * 60 + ss
        epoch_ns = epoch_sec * 1_000_000_000 + frac_ns
        return f'{date_part}T{time_part}.{frac_ns:09d}Z', epoch_ns, False
    try:
        base_dt = datetime.fromisoformat(f'{date_part}T{time_part}{offset_part}').astimezone(timezone.utc)
    except ValueError:
        return '', 0, True
    epoch_sec = int(base_dt.timestamp())
    epoch_ns = epoch_sec * 1_000_000_000 + frac_ns
    utc_iso = base_dt.strftime('%Y-%m-%dT%H:%M:%S') + f'.{frac_ns:09d}Z'
    return utc_iso, epoch_ns, False


def _local_day_from_utc_ns(utc_ns_iso: str, tz_info, epoch_ns: int | None = None) -> str:
    if not utc_ns_iso:
        return ''
    if tz_info is not None and len(utc_ns_iso) >= 19:
        date_part = utc_ns_iso[:10]
        off = _utc_day_constant_offset(date_part, tz_info)
        if off is not None:
            if epoch_ns is not None:
                local_sec = (epoch_ns // 1_000_000_000) + off
            else:
                day_sec = _ymd_epoch_seconds(date_part)
                if day_sec is None:
                    return ''
                local_sec = day_sec + int(utc_ns_iso[11:13]) * 3600 + int(utc_ns_iso[14:16]) * 60 + int(utc_ns_iso[17:19]) + off
            return _day_index_to_ymd(local_sec // 86400)
    try:
        dt = datetime.fromisoformat(utc_ns_iso[:19] + '+00:00').astimezone(tz_info)
        return dt.strftime('%Y-%m-%d')
    except ValueError:
        return ''


def day_to_desc_key(effective_date: str) -> int:
    """Convert ``YYYY-MM-DD`` to same-direction ASC key ``-YYYYMMDD`` (missing -> 0)."""
    day = _calendar_date(effective_date)
    if not day:
        return 0
    return -int(day.replace('-', ''))


def _file_stat_sig(path: Path) -> tuple[tuple[int, int, int], tuple[int, int, int]]:
    out = []
    for candidate in (path, Path(str(path) + '-wal')):
        try:
            st = candidate.stat()
            out.append((st.st_ino, st.st_mtime_ns, st.st_size))
        except OSError:
            out.append((0, 0, 0))
    return out[0], out[1]


def _iso_now() -> str:
    return datetime.now(timezone.utc).isoformat(timespec='microseconds')


def _is_data_item_completed(status_value) -> bool:
    if isinstance(status_value, dict):
        status_value = status_value.get('status')
    return status_value == 'DATA_ITEM_PROCESSING_COMPLETED'


def _extract_cognify_status(pipeline_status_raw, dataset_id: str) -> str:
    if not pipeline_status_raw or pipeline_status_raw == '{}':
        return ''
    parsed = parse_json_value(pipeline_status_raw)
    if not isinstance(parsed, dict):
        return ''
    slots = parsed.get('cognify_pipeline')
    if not isinstance(slots, dict):
        return ''
    slot = slots.get(dataset_id)
    if slot is None:
        slot = slots.get(uuid_hex(dataset_id))
    if isinstance(slot, dict):
        return str(slot.get('status') or '')
    return str(slot or '')


def _row_id_checksum(raw_id, label, content_hash) -> int:
    """Exact Python mirror of the SQLite ``id_checksum`` aggregate term."""
    id_s = raw_id if isinstance(raw_id, str) else ('' if raw_id is None else str(raw_id))
    lbl_s = label if isinstance(label, str) else ('' if label is None else str(label))
    ch_s = content_hash if isinstance(content_hash, str) else ('' if content_hash is None else str(content_hash))
    n_id = len(id_s)
    return (
        (ord(id_s[0]) * 16777216 if n_id > 0 else 0)
        + (ord(id_s[8]) * 65536 if n_id > 8 else 0)
        + (ord(id_s[16]) * 256 if n_id > 16 else 0)
        + (ord(id_s[24]) if n_id > 24 else 0)
        + len(lbl_s) * 7
        + (ord(ch_s[0]) * 31 if ch_s else 0)
    )


_CATALOG_INDEX_DDL = '''
        CREATE INDEX IF NOT EXISTS idx_cdocs_overview
            ON catalog_docs(is_knowledge, day_desc_key ASC, dataset_id ASC, doc_id ASC)
            WHERE is_knowledge = 1;
        CREATE INDEX IF NOT EXISTS idx_cdocs_proj_day
            ON catalog_docs(dataset_id, is_knowledge, day_desc_key ASC, doc_id ASC)
            WHERE is_knowledge = 1;
        CREATE INDEX IF NOT EXISTS idx_cdocs_proj_recall
            ON catalog_docs(dataset_id, is_knowledge, recall_desc_key ASC, day_desc_key ASC, doc_id ASC)
            WHERE is_knowledge = 1;
        CREATE INDEX IF NOT EXISTS idx_cdocs_proj_cat_day
            ON catalog_docs(dataset_id, is_knowledge, category, day_desc_key ASC, doc_id ASC)
            WHERE is_knowledge = 1;
        CREATE INDEX IF NOT EXISTS idx_cdocs_proj_cat_recall
            ON catalog_docs(dataset_id, is_knowledge, category, recall_desc_key ASC, day_desc_key ASC, doc_id ASC)
            WHERE is_knowledge = 1;
        CREATE INDEX IF NOT EXISTS idx_cdocs_common_ts
            ON catalog_docs(dataset_id, kind, common_category, sort_ts_desc_key ASC, doc_id ASC)
            WHERE kind = 'common';
        CREATE INDEX IF NOT EXISTS idx_cdocs_common_all_ts
            ON catalog_docs(dataset_id, kind, sort_ts_desc_key ASC, doc_id ASC)
            WHERE kind = 'common';
        CREATE INDEX IF NOT EXISTS idx_cdocs_raw_ts
            ON catalog_docs(dataset_id, kind, sort_ts_desc_key ASC, doc_id ASC)
            WHERE kind = 'raw_doc';
        CREATE INDEX IF NOT EXISTS idx_cdocs_proj_created
            ON catalog_docs(dataset_id, is_knowledge, created_at_ns DESC, doc_id ASC)
            WHERE is_knowledge = 1;
        CREATE INDEX IF NOT EXISTS idx_cdocs_proj_invalid_ts
            ON catalog_docs(dataset_id, is_knowledge, created_at_invalid, doc_id)
            WHERE created_at_invalid = 1;
'''

_CATALOG_INDEX_NAMES = (
    'idx_cdocs_overview',
    'idx_cdocs_overview_day',
    'idx_cdocs_proj_day',
    'idx_cdocs_proj_recall',
    'idx_cdocs_proj_cat_day',
    'idx_cdocs_proj_cat_recall',
    'idx_cdocs_common_ts',
    'idx_cdocs_common_all_ts',
    'idx_cdocs_raw_ts',
    'idx_cdocs_proj_created',
    'idx_cdocs_proj_invalid_ts',
)


def _create_catalog_schema(con: sqlite3.Connection, tz_name: str):
    con.execute('PRAGMA page_size=8192')
    con.execute('PRAGMA journal_mode=WAL')
    con.execute('PRAGMA journal_size_limit=4194304')
    con.execute('PRAGMA wal_autocheckpoint=1000')
    con.execute('PRAGMA synchronous=NORMAL')
    con.execute('PRAGMA temp_store=MEMORY')
    con.execute('PRAGMA cache_size=-4096')
    con.execute('PRAGMA threads=2')
    con.execute('PRAGMA busy_timeout=5000')
    con.executescript('''
        CREATE TABLE IF NOT EXISTS catalog_meta (
            singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
            schema_version INTEGER NOT NULL,
            generation INTEGER NOT NULL,
            local_tz_name TEXT NOT NULL,
            last_provenance_seq INTEGER NOT NULL,
            last_recall_rev INTEGER NOT NULL,
            built_at TEXT NOT NULL
        );

        CREATE TABLE IF NOT EXISTS dataset_sync_state (
            dataset_id TEXT PRIMARY KEY,
            dataset_hex TEXT NOT NULL,
            doc_count INTEGER NOT NULL,
            max_rowid INTEGER NOT NULL,
            sum_rowid INTEGER NOT NULL,
            max_updated_at TEXT NOT NULL,
            id_checksum INTEGER NOT NULL DEFAULT 0,
            knowledge_count INTEGER NOT NULL,
            raw_doc_count INTEGER NOT NULL,
            common_count INTEGER NOT NULL,
            catalog_version TEXT NOT NULL,
            synced_at TEXT NOT NULL
        );

        CREATE TABLE IF NOT EXISTS catalog_docs (
            dataset_id TEXT NOT NULL,
            doc_id TEXT NOT NULL,
            cognee_rowid INTEGER NOT NULL,
            kind TEXT NOT NULL CHECK (kind IN ('knowledge', 'common', 'raw_doc')),
            is_knowledge INTEGER NOT NULL CHECK (is_knowledge IN (0, 1)),
            common_category TEXT NOT NULL DEFAULT '' CHECK (common_category IN ('', 'preference', 'experience')),
            lesson_type TEXT NOT NULL DEFAULT '',
            state_key TEXT NOT NULL DEFAULT '',
            category TEXT NOT NULL DEFAULT '',
            session_id TEXT NOT NULL DEFAULT '',
            doc_name TEXT NOT NULL DEFAULT '',
            doc_label TEXT NOT NULL DEFAULT '',
            raw_data_location TEXT NOT NULL DEFAULT '',
            content_hash TEXT NOT NULL DEFAULT '',
            created_at_raw TEXT NOT NULL DEFAULT '',
            created_at_ns TEXT NOT NULL DEFAULT '',
            created_at_invalid INTEGER NOT NULL DEFAULT 0 CHECK (created_at_invalid IN (0, 1)),
            updated_at_ns TEXT NOT NULL DEFAULT '',
            effective_date TEXT NOT NULL,
            day_desc_key INTEGER NOT NULL,
            sort_ts_desc_key INTEGER NOT NULL,
            recall_count INTEGER NOT NULL DEFAULT 0,
            recall_desc_key INTEGER NOT NULL DEFAULT 0,
            pipeline_status TEXT NOT NULL DEFAULT '',
            source_updated_at TEXT NOT NULL DEFAULT '',
            PRIMARY KEY (dataset_id, doc_id)
        );

        CREATE TABLE IF NOT EXISTS catalog_day_buckets (
            dataset_id TEXT NOT NULL,
            effective_date TEXT NOT NULL,
            knowledge_count INTEGER NOT NULL,
            PRIMARY KEY (dataset_id, effective_date)
        ) WITHOUT ROWID;
    ''' + _CATALOG_INDEX_DDL)
    con.execute(
        '''INSERT OR IGNORE INTO catalog_meta
           (singleton, schema_version, generation, local_tz_name, last_provenance_seq, last_recall_rev, built_at)
           VALUES (1, ?, 0, ?, 0, 0, ?)''',
        (CATALOG_SCHEMA_VERSION, tz_name, _iso_now()),
    )


def _open_private_rw(path: Path) -> sqlite3.Connection:
    path.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    os.chmod(path.parent, 0o700)
    if path.is_symlink():
        raise ValueError('catalog index database must not be a symlink')
    fd = os.open(path, os.O_CREAT | os.O_RDWR | getattr(os, 'O_NOFOLLOW', 0), 0o600)
    os.close(fd)
    os.chmod(path, 0o600)
    return sqlite3.connect(path, isolation_level=None, check_same_thread=False)


def _row_to_doc_dict(row) -> dict:
    return {
        'dataset_id': row[0],
        'doc_id': row[1],
        'kind': row[2],
        'is_knowledge': int(row[3]),
        'common_category': row[4],
        'lesson_type': row[5],
        'state_key': row[6],
        'category': row[7],
        'session_id': row[8],
        'doc_name': row[9],
        'doc_label': row[10] if row[10] else None,
        'raw_data_location': row[11],
        'content_hash': row[12],
        'created_at': row[13],
        'created_at_ns': row[14],
        'created_at_invalid': int(row[15]),
        'updated_at_ns': row[16],
        'effective_date': row[17],
        'day_desc_key': int(row[18]),
        'sort_ts_desc_key': int(row[19]),
        'recall_count': int(row[20]),
        'recall_desc_key': int(row[21]),
        'pipeline_status': row[22],
        'updated_at': row[23],
    }


_SELECT_DOC_COLS = '''
    dataset_id, doc_id, kind, is_knowledge, common_category, lesson_type, state_key,
    category, session_id, doc_name, doc_label, raw_data_location, content_hash,
    created_at_raw, created_at_ns, created_at_invalid, updated_at_ns, effective_date,
    day_desc_key, sort_ts_desc_key, recall_count, recall_desc_key, pipeline_status,
    source_updated_at
'''


class CatalogIndex:
    """Single-writer rebuildable metadata projection with concurrent read-only queries."""

    def __init__(
        self,
        path,
        *,
        official_data=None,
        provenance_path=None,
        recall_path=None,
        tz_provider=current_local_tz_name,
    ):
        self.path = Path(path)
        self.official_data = Path(official_data) if official_data else None
        self.provenance_path = Path(provenance_path) if provenance_path else None
        self.recall_path = Path(recall_path) if recall_path else None
        self.tz_provider = tz_provider

        self._write_lock = threading.Lock()
        self._state_lock = threading.Lock()
        self._rebuilding = False
        self._last_stat_sig = None
        self._last_prov_stat = None
        self._last_recall_stat = None
        self._last_generation = 0
        self._inflight_sync = None
        self._recent_appends: dict[int, tuple[str, list[tuple[str, str, int]]]] = {}
        self._cat_counts_cache: dict[tuple[int, str], dict[str, int]] = {}
        self._writer_ro_cons: dict[str, tuple[int, sqlite3.Connection]] = {}
        self._writer_executor = ThreadPoolExecutor(max_workers=1, thread_name_prefix='catalog-index-writer')
        self._reader_local = threading.local()
        self._reader_epoch = 0
        self._open_readers: list[sqlite3.Connection] = []
        self._readers_lock = threading.Lock()

        self._write_con = _open_private_rw(self.path)
        _create_catalog_schema(self._write_con, self.tz_provider())

    def _check_not_rebuilding(self):
        with self._state_lock:
            if self._rebuilding:
                raise PolicyError('catalog_index_rebuilding', 503)

    def _get_reader(self) -> sqlite3.Connection:
        self._check_not_rebuilding()
        epoch = self._reader_epoch
        con = getattr(self._reader_local, 'con', None)
        con_epoch = getattr(self._reader_local, 'epoch', -1)
        if con is not None and con_epoch == epoch:
            return con
        if con is not None:
            try:
                con.close()
            except Exception:
                pass
        resolved = self.path.resolve()
        new_con = sqlite3.connect(f'file:{resolved}?mode=ro', uri=True, timeout=2.0, check_same_thread=False)
        new_con.execute('PRAGMA query_only = ON')
        new_con.execute('PRAGMA cache_size = -1024')
        new_con.execute('PRAGMA busy_timeout = 2000')
        self._reader_local.con = new_con
        self._reader_local.epoch = epoch
        with self._readers_lock:
            self._open_readers.append(new_con)
        return new_con

    def _invalidate_readers(self):
        self._reader_epoch += 1
        with self._readers_lock:
            stale = list(self._open_readers)
            self._open_readers.clear()
        for con in stale:
            try:
                con.close()
            except Exception:
                pass

    def _get_writer_ro(self, db_path: Path) -> sqlite3.Connection:
        """Persistent writer-thread read-only connection via ``open_official_sqlite_ro`` (auto-reopens on inode change)."""
        key = str(db_path)
        try:
            ino = db_path.stat().st_ino
        except OSError:
            ino = -1
        cached = self._writer_ro_cons.get(key)
        if cached is not None:
            cached_ino, cached_con = cached
            if cached_ino == ino and ino != -1:
                return cached_con
            self._writer_ro_cons.pop(key, None)
            try:
                cached_con.close()
            except Exception:
                pass
        con = open_official_sqlite_ro(db_path)
        self._writer_ro_cons[key] = (ino, con)
        return con

    def _close_writer_ro_all(self):
        for _ino, con in list(self._writer_ro_cons.values()):
            try:
                con.close()
            except Exception:
                pass
        self._writer_ro_cons.clear()

    def close(self):
        self._invalidate_readers()
        self._writer_executor.shutdown(wait=True)
        with self._write_lock:
            self._close_writer_ro_all()
            try:
                self._write_con.close()
            except Exception:
                pass

    def passive_checkpoint(self) -> bool:
        """Non-blocking WAL maintenance: busy_timeout=0 + PASSIVE, TRUNCATE only if uncontended."""
        if not self.path.is_file() or self.path.is_symlink():
            return False
        con = None
        try:
            con = sqlite3.connect(self.path, isolation_level=None, timeout=0.0)
            con.execute('PRAGMA busy_timeout = 0')
            row = con.execute('PRAGMA wal_checkpoint(PASSIVE)').fetchone()
            if row and int(row[0]) == 0:
                t_row = con.execute('PRAGMA wal_checkpoint(TRUNCATE)').fetchone()
                return bool(t_row and int(t_row[0]) == 0)
            return False
        except sqlite3.Error:
            return False
        finally:
            if con is not None:
                try:
                    con.close()
                except Exception:
                    pass

    def _compute_stat_signature(self, managed_datasets: tuple[str, ...]):
        cognee_db = (self.official_data / 'system' / 'databases' / 'cognee_db') if self.official_data else None
        cognee_sig = _file_stat_sig(cognee_db) if cognee_db else ((0, 0, 0), (0, 0, 0))
        prov_sig = _file_stat_sig(self.provenance_path) if self.provenance_path else ((0, 0, 0), (0, 0, 0))
        rec_sig = _file_stat_sig(self.recall_path) if self.recall_path else ((0, 0, 0), (0, 0, 0))
        tz_name = self.tz_provider()
        return (cognee_sig, prov_sig, rec_sig, tz_name, managed_datasets)

    def _load_provenance_maps(self, allowed_datasets: list[str]):
        doc_prov = {}
        key_prov = {}
        max_seq = 0
        if not self.provenance_path or not self.provenance_path.is_file() or self.provenance_path.is_symlink() or not allowed_datasets:
            return doc_prov, key_prov, max_seq
        try:
            with official_sqlite_ro(self.provenance_path) as con:
                cols = {str(r[1]) for r in con.execute('PRAGMA table_info(document_provenance)').fetchall()}
                has_seq = 'sync_seq' in cols
                seq_col = 'sync_seq' if has_seq else '0'
                for start in range(0, len(allowed_datasets), _MAX_IN_BATCH):
                    batch = allowed_datasets[start : start + _MAX_IN_BATCH]
                    ph = ','.join('?' for _ in batch)
                    for ds, doc_id, lt, sk, fdate, sseq in con.execute(
                        f'''SELECT dataset_id, document_id, lesson_type, state_key, first_source_date, {seq_col}
                            FROM document_provenance WHERE dataset_id IN ({ph})''',
                        batch,
                    ):
                        doc_prov[(str(ds), str(doc_id))] = (
                            str(lt or '').strip(),
                            str(sk or '').strip(),
                            str(fdate or '').strip(),
                        )
                        if int(sseq or 0) > max_seq:
                            max_seq = int(sseq or 0)
                    for ds, lt, sk, fdate in con.execute(
                        f'''SELECT dataset_id, lesson_type, state_key, first_source_date
                            FROM keyed_provenance WHERE dataset_id IN ({ph})''',
                        batch,
                    ):
                        key_prov[(str(ds), str(lt or '').strip(), str(sk or '').strip())] = str(fdate or '').strip()
                if has_seq:
                    meta_exists = con.execute(
                        "SELECT 1 FROM sqlite_master WHERE type='table' AND name='provenance_meta' LIMIT 1"
                    ).fetchone()
                    if meta_exists:
                        m = con.execute('SELECT last_sync_seq FROM provenance_meta WHERE singleton=1').fetchone()
                        if m and int(m[0] or 0) > max_seq:
                            max_seq = int(m[0])
        except (PolicyError, sqlite3.Error):
            return {}, {}, 0
        return doc_prov, key_prov, max_seq

    def _load_provenance_delta(self, after_seq: int, allowed_set: set[str]):
        changes = []
        new_max_seq = after_seq
        if not self.provenance_path or not self.provenance_path.is_file() or self.provenance_path.is_symlink():
            return changes, new_max_seq, False
        try:
            with official_sqlite_ro(self.provenance_path) as con:
                cols = {str(r[1]) for r in con.execute('PRAGMA table_info(document_provenance)').fetchall()}
                if 'sync_seq' not in cols:
                    return [], after_seq, True
                for ds, doc_id, lt, sk, fdate, sseq in con.execute(
                    '''SELECT dataset_id, document_id, lesson_type, state_key, first_source_date, sync_seq
                       FROM document_provenance
                       WHERE sync_seq > ?
                       ORDER BY sync_seq ASC''',
                    (int(after_seq),),
                ):
                    seq_int = int(sseq or 0)
                    if seq_int > new_max_seq:
                        new_max_seq = seq_int
                    if str(ds) in allowed_set:
                        changes.append((str(ds), str(doc_id), str(lt or '').strip(), str(sk or '').strip(), str(fdate or '').strip()))
                meta_exists = con.execute(
                    "SELECT 1 FROM sqlite_master WHERE type='table' AND name='provenance_meta' LIMIT 1"
                ).fetchone()
                if meta_exists:
                    m = con.execute('SELECT last_sync_seq FROM provenance_meta WHERE singleton=1').fetchone()
                    if m and int(m[0] or 0) > new_max_seq:
                        new_max_seq = int(m[0])
        except (PolicyError, sqlite3.Error):
            return [], after_seq, False
        return changes, new_max_seq, False

    def _load_recall_delta(
        self,
        after_rev: int,
        allowed_set: set[str],
    ) -> tuple[list[tuple[str, str, int]], int, bool]:
        """Incremental recall delta via ``recall_doc_counts WHERE rev > ?``.

        Returns ``(changes, new_rev, needs_full)``.
        """
        if not self.recall_path or not self.recall_path.is_file() or self.recall_path.is_symlink() or not allowed_set:
            return [], after_rev, False
        changes: list[tuple[str, str, int]] = []
        new_rev = int(after_rev)
        try:
            with official_sqlite_ro(self.recall_path) as con:
                has_mat = con.execute(
                    "SELECT 1 FROM sqlite_master WHERE type='table' AND name='recall_doc_counts' LIMIT 1"
                ).fetchone()
                if not has_mat:
                    return [], after_rev, True
                for ds, doc_id, total_hits, row_rev in con.execute(
                    '''SELECT dataset_id, document_id, total_hits, rev
                       FROM recall_doc_counts
                       WHERE rev > ?
                       ORDER BY rev ASC''',
                    (int(after_rev),),
                ):
                    r_int = int(row_rev or 0)
                    if r_int > new_rev:
                        new_rev = r_int
                    if str(ds) in allowed_set:
                        changes.append((str(ds), str(doc_id), int(total_hits or 0)))
                has_rev_state = con.execute(
                    "SELECT 1 FROM sqlite_master WHERE type='table' AND name='recall_rev_state' LIMIT 1"
                ).fetchone()
                if has_rev_state:
                    m = con.execute('SELECT last_rev FROM recall_rev_state WHERE singleton=1').fetchone()
                    if m and int(m[0] or 0) > new_rev:
                        new_rev = int(m[0])
        except (PolicyError, sqlite3.Error):
            return [], after_rev, False
        return changes, new_rev, False

    def _load_recall_counts(self, allowed_datasets: list[str]) -> tuple[dict[tuple[str, str], int], int]:
        counts = {}
        rev = 0
        if not self.recall_path or not self.recall_path.is_file() or self.recall_path.is_symlink() or not allowed_datasets:
            return counts, rev
        try:
            with official_sqlite_ro(self.recall_path) as con:
                has_mat = con.execute(
                    "SELECT 1 FROM sqlite_master WHERE type='table' AND name='recall_doc_counts' LIMIT 1"
                ).fetchone()
                for start in range(0, len(allowed_datasets), _MAX_IN_BATCH):
                    batch = allowed_datasets[start : start + _MAX_IN_BATCH]
                    ph = ','.join('?' for _ in batch)
                    if has_mat:
                        for ds, doc_id, total_hits, row_rev in con.execute(
                            f'''SELECT dataset_id, document_id, total_hits, rev
                                FROM recall_doc_counts WHERE dataset_id IN ({ph})''',
                            batch,
                        ):
                            counts[(str(ds), str(doc_id))] = int(total_hits or 0)
                            if int(row_rev or 0) > rev:
                                rev = int(row_rev or 0)
                    else:
                        for ds, doc_id, total_hits in con.execute(
                            f'''SELECT dataset_id, document_id, COUNT(*)
                                FROM recall_hit WHERE dataset_id IN ({ph})
                                GROUP BY dataset_id, document_id''',
                            batch,
                        ):
                            counts[(str(ds), str(doc_id))] = int(total_hits or 0)
                if has_mat:
                    has_rev_state = con.execute(
                        "SELECT 1 FROM sqlite_master WHERE type='table' AND name='recall_rev_state' LIMIT 1"
                    ).fetchone()
                    if has_rev_state:
                        m = con.execute('SELECT last_rev FROM recall_rev_state WHERE singleton=1').fetchone()
                        if m and int(m[0] or 0) > rev:
                            rev = int(m[0])
        except (PolicyError, sqlite3.Error):
            return {}, 0
        return counts, rev

    def _project_cognee_row(
        self,
        row,
        ds_id: str,
        *,
        doc_prov: dict,
        key_prov: dict,
        recall_counts: dict,
        tz_info,
    ) -> tuple | None:
        """Project a single cognee_db.data row into the zero-body catalog_docs tuple."""
        doc_id = uuid_text(row[0])
        if not doc_id:
            return None
        cognee_rowid = int(row[1] or 0)
        doc_name = str(row[2] or '')
        doc_label = str(row[3] or '')
        raw_loc = str(row[4] or '')
        meta = extract_trusted_metadata(row[5], row[6], row[7])
        pipeline_status = _extract_cognify_status(row[8], ds_id)
        updated_raw = iso_text(row[9])
        created_raw = iso_text(row[10])
        content_hash = str(row[11] or '')

        common_cat = ''
        if meta.get('memlane_kind') == 'common_knowledge':
            c_val = meta.get('category')
            if c_val == 'preference' or c_val == 'experience':
                common_cat = c_val
        tags = meta.get('node_set')
        if not isinstance(tags, list):
            tags = []
        if common_cat:
            kind = 'common'
            is_know = 1
        elif (len(tags) == 1 and tags[0] == 'session_learnings') or (
            'session_learnings' in tags or 'user_context' in tags or 'memory' in tags
        ):
            kind = 'knowledge'
            is_know = 1
        else:
            for t in tags:
                if t in _DERIVED_NODE_SETS:
                    return None
            kind = 'raw_doc'
            is_know = 0

        if is_know:
            if common_cat:
                category = ''
            elif 'session_learnings' not in tags and ('user_context' in tags or 'memory' in tags):
                category = '直接保存'
            else:
                rk = meta.get('lesson_type')
                lk = rk.strip() if isinstance(rk, str) else ''
                category = _LESSON_CATEGORY_LABELS.get(lk) or (f'未识别分类（{lk}）' if lk else '未记录')
            raw_lt = meta.get('lesson_type')
            lesson_type = raw_lt.strip() if isinstance(raw_lt, str) else ''
            raw_sk = meta.get('state_key')
            state_key = raw_sk.strip() if isinstance(raw_sk, str) else ''
            # Inline fast-path of unique_learning_session
            session_id = ''
            for tag in tags:
                if tag.startswith('session_learnings:'):
                    sid_cand = tag[18:]
                    if not sid_cand or (session_id and sid_cand != session_id):
                        session_id = ''
                        break
                    session_id = sid_cand
            meta_sid = meta.get('session_id')
            if isinstance(meta_sid, str) and meta_sid and meta_sid != session_id:
                session_id = ''
        else:
            category = ''
            lesson_type = ''
            state_key = ''
            session_id = ''

        created_ns, created_epoch_ns, created_invalid = normalize_utc_ns(created_raw)
        if updated_raw == created_raw:
            updated_ns, updated_epoch_ns, updated_invalid = created_ns, created_epoch_ns, created_invalid
        else:
            updated_ns, updated_epoch_ns, updated_invalid = normalize_utc_ns(updated_raw)

        # Effective date rule: provenance > explicit first_source_date > session_date > local(createdAt)
        effective_date = ''
        if is_know:
            if (doc_prov or key_prov) and (
                lesson_type or meta.get('first_source_date') or meta.get('source_date_status') or 'session_learnings' in tags
            ):
                dates = []
                lt = lesson_type
                sk = state_key
                prov = doc_prov.get((ds_id, doc_id)) if doc_prov else None
                if prov:
                    if not lt and prov[0]:
                        lt = prov[0]
                    if not sk and prov[1]:
                        sk = prov[1]
                    if _calendar_date(prov[2]):
                        dates.append(prov[2])
                if lt and sk and key_prov:
                    kd = key_prov.get((ds_id, lt, sk))
                    if _calendar_date(kd):
                        dates.append(kd)
                if dates:
                    effective_date = min(dates)
            if not effective_date:
                fsd = meta.get('first_source_date')
                day = _calendar_date(fsd) if fsd else None
                if day:
                    effective_date = day
                else:
                    sdate = meta.get('session_date')
                    day = _calendar_date(sdate) if sdate else None
                    if day:
                        effective_date = day
            if not effective_date and not created_invalid:
                effective_date = _local_day_from_utc_ns(created_ns, tz_info, created_epoch_ns)
        else:
            if not updated_invalid:
                effective_date = _local_day_from_utc_ns(updated_ns, tz_info, updated_epoch_ns)
            elif not created_invalid:
                effective_date = _local_day_from_utc_ns(created_ns, tz_info, created_epoch_ns)

        day_desc = -int(effective_date[0:4] + effective_date[5:7] + effective_date[8:10]) if len(effective_date) == 10 else day_to_desc_key(effective_date)
        # Common knowledge & raw docs sort by updatedAt (fallback createdAt) full timestamp DESC
        sort_epoch_ns = updated_epoch_ns if not updated_invalid else (created_epoch_ns if not created_invalid else 0)
        sort_ts_desc = -sort_epoch_ns

        rec_count = int(recall_counts.get((ds_id, doc_id), 0))
        rec_desc = -rec_count

        return (
            ds_id,
            doc_id,
            cognee_rowid,
            kind,
            is_know,
            common_cat,
            lesson_type,
            state_key,
            category,
            session_id,
            doc_name,
            doc_label,
            raw_loc,
            content_hash,
            created_raw,
            created_ns,
            1 if created_invalid else 0,
            updated_ns,
            effective_date,
            day_desc,
            sort_ts_desc,
            rec_count,
            rec_desc,
            pipeline_status,
            updated_raw,
        )

    def _rebuild_day_buckets_and_state_locked(
        self,
        con: sqlite3.Connection,
        dataset_id: str,
        fingerprint: tuple[int, int, int, str],
        generation: int,
        prov_seq: int,
        now_iso: str,
    ):
        con.execute('DELETE FROM catalog_day_buckets WHERE dataset_id = ?', (dataset_id,))
        con.execute(
            '''INSERT INTO catalog_day_buckets(dataset_id, effective_date, knowledge_count)
               SELECT dataset_id, effective_date, COUNT(*)
               FROM catalog_docs
               WHERE dataset_id = ? AND is_knowledge = 1 AND effective_date != ''
               GROUP BY effective_date''',
            (dataset_id,),
        )
        counts = con.execute(
            '''SELECT
                 COALESCE(SUM(CASE WHEN is_knowledge = 1 THEN 1 ELSE 0 END), 0),
                 COALESCE(SUM(CASE WHEN kind = 'raw_doc' THEN 1 ELSE 0 END), 0),
                 COALESCE(SUM(CASE WHEN kind = 'common' THEN 1 ELSE 0 END), 0)
               FROM catalog_docs WHERE dataset_id = ?''',
            (dataset_id,),
        ).fetchone()
        k_cnt, raw_cnt, com_cnt = int(counts[0]), int(counts[1]), int(counts[2])
        doc_count, max_rowid, sum_rowid, max_updated, id_checksum = fingerprint
        ver_raw = f'{generation}|{dataset_id}|{doc_count}|{sum_rowid}|{max_updated}|{id_checksum}|{prov_seq}|{k_cnt}'
        catalog_version = hashlib.sha256(ver_raw.encode('utf-8')).hexdigest()[:24]
        con.execute(
            '''INSERT INTO dataset_sync_state
               (dataset_id, dataset_hex, doc_count, max_rowid, sum_rowid, max_updated_at, id_checksum,
                knowledge_count, raw_doc_count, common_count, catalog_version, synced_at)
               VALUES (?,?,?,?,?,?,?,?,?,?,?,?)
               ON CONFLICT(dataset_id) DO UPDATE SET
                 dataset_hex=excluded.dataset_hex,
                 doc_count=excluded.doc_count,
                 max_rowid=excluded.max_rowid,
                 sum_rowid=excluded.sum_rowid,
                 max_updated_at=excluded.max_updated_at,
                 id_checksum=excluded.id_checksum,
                 knowledge_count=excluded.knowledge_count,
                 raw_doc_count=excluded.raw_doc_count,
                 common_count=excluded.common_count,
                 catalog_version=excluded.catalog_version,
                 synced_at=excluded.synced_at''',
            (
                dataset_id,
                uuid_hex(dataset_id),
                doc_count,
                max_rowid,
                sum_rowid,
                max_updated,
                id_checksum,
                k_cnt,
                raw_cnt,
                com_cnt,
                catalog_version,
                now_iso,
            ),
        )

    def _sync_into_connection(self, con: sqlite3.Connection, managed_datasets: list[str], *, force_full: bool = False):
        if not self.official_data:
            raise PolicyError('official_store_unavailable', 503)
        cognee_db = self.official_data / 'system' / 'databases' / 'cognee_db'

        allowed = sorted({uuid_text(ds) for ds in managed_datasets if ds})
        allowed_set = set(allowed)
        ds_hex_to_uuid = {}
        for ds in allowed:
            h = uuid_hex(ds)
            if h:
                ds_hex_to_uuid[h] = ds
            ds_hex_to_uuid[ds] = ds

        tz_name = self.tz_provider()
        tz_info = _tzinfo_for_name(tz_name)
        meta_row = con.execute(
            'SELECT generation, local_tz_name, last_provenance_seq, last_recall_rev FROM catalog_meta WHERE singleton=1'
        ).fetchone()
        cur_gen = int(meta_row[0]) if meta_row else 0
        old_tz = str(meta_row[1]) if meta_row else tz_name
        last_prov_seq = 0 if force_full else (int(meta_row[2]) if meta_row else 0)
        last_recall_rev = 0 if force_full else (int(meta_row[3]) if meta_row else 0)
        tz_changed = (old_tz != tz_name)

        state_cols = {str(r[1]) for r in con.execute('PRAGMA table_info(dataset_sync_state)').fetchall()}
        if 'id_checksum' not in state_cols:
            con.execute('ALTER TABLE dataset_sync_state ADD COLUMN id_checksum INTEGER NOT NULL DEFAULT 0')
        existing_states = {}
        if not force_full:
            for row in con.execute(
                'SELECT dataset_id, doc_count, max_rowid, sum_rowid, max_updated_at, id_checksum FROM dataset_sync_state'
            ).fetchall():
                existing_states[str(row[0])] = (int(row[1]), int(row[2]), int(row[3]), str(row[4]), int(row[5] or 0))

        # Two-stage fingerprint probe on persistent writer-thread RO connection:
        #   Stage 1 (covering index ix_data_dataset_created, ~3.7ms at 50k):
        #     COUNT(*), MAX(rowid), SUM(rowid) — detects appends, deletes, replacements.
        #   Stage 2 (table probe, ONLY when Stage 1 tuple matches existing_states while
        #     cognee_db file stat changed): MAX(updated_at) + id_checksum — catches
        #     in-place UPDATEs and hostile same-rowid reuse.
        #   Cold build (no existing_states): skip probe completely and compute 5-tuple
        #     in Python during the single sequential scan of data!
        live_fingerprints: dict[str, tuple[int, int, int, str, int]] = {ds: (0, 0, 0, '', 0) for ds in allowed}
        query_variants = sorted(ds_hex_to_uuid.keys())
        prov_stat_now = _file_stat_sig(self.provenance_path) if self.provenance_path else None
        recall_stat_now = _file_stat_sig(self.recall_path) if self.recall_path else None
        is_cold_build = force_full or not existing_states
        stream_cold_bulk = bool(
            is_cold_build and con.execute('SELECT 1 FROM catalog_docs LIMIT 1').fetchone() is None
        )
        cog_con = self._get_writer_ro(cognee_db)
        cog_in_tx = False
        try:
            # Pin one consistent read-only WAL snapshot across Stage 1 + Fast Append + Stage 2
            # so concurrent writers cannot split Stage 1 counts from WHERE rowid > old_max_rid.
            try:
                cog_con.execute('BEGIN DEFERRED')
                cog_in_tx = True
            except sqlite3.Error:
                cog_in_tx = False
            data_cols = {str(r[1]) for r in cog_con.execute('PRAGMA table_info(data)').fetchall()}
            has_sys = 'system_metadata' in data_cols
            has_pipe = 'pipeline_status' in data_cols
            sys_col = 'system_metadata' if has_sys else 'NULL'
            pipe_col = 'pipeline_status' if has_pipe else 'NULL'

            # Stage 1: ultra-fast covering index probe (skipped on cold build)
            stage1_cov: dict[str, tuple[int, int, int]] = {ds: (0, 0, 0) for ds in allowed}
            if not is_cold_build and query_variants:
                for start in range(0, len(query_variants), _MAX_IN_BATCH):
                    batch = query_variants[start : start + _MAX_IN_BATCH]
                    ph = ','.join('?' for _ in batch)
                    for raw_ds, cnt, max_rid, sum_rid in cog_con.execute(
                        f'''SELECT dataset_id, COUNT(*), COALESCE(MAX(rowid), 0),
                                   COALESCE(SUM(rowid), 0)
                            FROM data
                            WHERE dataset_id IN ({ph})
                            GROUP BY dataset_id''',
                        batch,
                    ):
                        canon = ds_hex_to_uuid.get(str(raw_ds))
                        if canon is None:
                            continue
                        p_cnt, p_max, p_sum = stage1_cov[canon]
                        stage1_cov[canon] = (p_cnt + int(cnt), max(p_max, int(max_rid)), p_sum + int(sum_rid))

            stage2_updated_rowids: dict[str, list[int]] = {}

            def _probe_full_5tuple(target_ds_list: list[str]):
                for tds in target_ds_list:
                    live_fingerprints[tds] = (0, 0, 0, '', 0)
                    t_vars = list(dict.fromkeys(v for v in (uuid_hex(tds), tds) if v))
                    if not t_vars:
                        continue
                    prev_fp_t = existing_states.get(tds)
                    old_upd_cut = prev_fp_t[3] if (prev_fp_t is not None and prev_fp_t[3]) else '9999-99-99T99:99:99'
                    ph = ','.join('?' for _ in t_vars)
                    rids_acc: list[int] = []
                    rids_overflow = False
                    for raw_ds, cnt, max_rid, sum_rid, max_upd, id_chk, upd_rids_csv in cog_con.execute(
                        f'''SELECT dataset_id, COUNT(*), COALESCE(MAX(rowid), 0),
                                   COALESCE(SUM(rowid), 0),
                                   COALESCE(MAX(CASE WHEN substr(updated_at, 1, 1) BETWEEN '0' AND '9' THEN updated_at END), ''),
                                   COALESCE(SUM(
                                       unicode(substr(id, 1, 1)) * 16777216
                                       + unicode(substr(id, 9, 1)) * 65536
                                       + unicode(substr(id, 17, 1)) * 256
                                       + unicode(substr(id, 25, 1))
                                       + length(coalesce(label, '')) * 7
                                       + unicode(substr(coalesce(content_hash, ''), 1, 1)) * 31
                                   ), 0),
                                   GROUP_CONCAT(CASE WHEN substr(updated_at, 1, 1) BETWEEN '0' AND '9' AND updated_at > ? THEN rowid END)
                            FROM data
                            WHERE dataset_id IN ({ph})
                            GROUP BY dataset_id''',
                        (old_upd_cut, *t_vars),
                    ):
                        canon = ds_hex_to_uuid.get(str(raw_ds))
                        if canon is None:
                            continue
                        prev_cnt, prev_max, prev_sum, prev_upd, prev_chk = live_fingerprints[canon]
                        upd_str = str(max_upd or '')
                        live_fingerprints[canon] = (
                            prev_cnt + int(cnt),
                            max(prev_max, int(max_rid)),
                            prev_sum + int(sum_rid),
                            max(prev_upd, upd_str),
                            prev_chk + int(id_chk or 0),
                        )
                        if upd_rids_csv and not rids_overflow:
                            parts = str(upd_rids_csv).split(',')
                            if len(rids_acc) + len(parts) <= 200:
                                for tok in parts:
                                    if tok:
                                        rids_acc.append(int(tok))
                            else:
                                rids_overflow = True
                                rids_acc.clear()
                    if rids_acc and not rids_overflow:
                        stage2_updated_rowids[tds] = rids_acc

            removed_datasets = [ds for ds in existing_states if ds not in allowed_set]
            changed_datasets: list[str] = []
            fast_append_rows: dict[str, list] = {}
            need_stage2_ds: list[str] = []

            if is_cold_build:
                changed_datasets = list(allowed)
            else:
                for ds in allowed:
                    prev_fp = existing_states.get(ds)
                    new_cnt, new_max_rid, new_sum_rid = stage1_cov[ds]
                    if prev_fp is None:
                        need_stage2_ds.append(ds)
                        changed_datasets.append(ds)
                        continue
                    old_cnt, old_max_rid, old_sum_rid, old_max_upd, old_chk = prev_fp
                    if old_cnt > 0 and new_cnt > old_cnt and new_max_rid > old_max_rid:
                        # Check O(log N + K) pure append directly via PK rowid B-tree (no Stage 2 scan!)
                        ds_vars_set = {v for v in (uuid_hex(ds), ds) if v}
                        new_rows = [
                            r[:-1]
                            for r in cog_con.execute(
                                f'''SELECT id, rowid, name, label, raw_data_location,
                                           {sys_col}, external_metadata, node_set, {pipe_col},
                                           updated_at, created_at, content_hash, dataset_id
                                    FROM data WHERE rowid > ?''',
                                (old_max_rid,),
                            ).fetchall()
                            if str(r[12]) in ds_vars_set
                        ]
                        added_cnt = len(new_rows)
                        added_sum_rid = sum(int(r[1] or 0) for r in new_rows)
                        if old_cnt + added_cnt == new_cnt and old_sum_rid + added_sum_rid == new_sum_rid:
                            added_chk = sum(_row_id_checksum(r[0], r[3], r[11]) for r in new_rows)
                            added_max_upd = max(
                                (str(r[9]) for r in new_rows if isinstance(r[9], str) and r[9] and '0' <= r[9][0] <= '9'),
                                default='',
                            )
                            live_fingerprints[ds] = (
                                new_cnt,
                                new_max_rid,
                                new_sum_rid,
                                max(old_max_upd, added_max_upd),
                                old_chk + added_chk,
                            )
                            fast_append_rows[ds] = new_rows
                            changed_datasets.append(ds)
                            continue
                    # Same (count, max_rowid, sum_rowid) or delete/replace -> run Stage 2 5-tuple check
                    need_stage2_ds.append(ds)

                if need_stage2_ds:
                    _probe_full_5tuple(need_stage2_ds)
                    for ds in need_stage2_ds:
                        if ds not in changed_datasets and existing_states.get(ds) != live_fingerprints[ds]:
                            changed_datasets.append(ds)

            # Load provenance & recall deltas ONLY when their backing DB stat changed (or full maps once on cold build)
            if is_cold_build:
                prov_changes, new_prov_seq, need_full_prov = [], last_prov_seq, True
                doc_prov, key_prov, full_prov_seq = self._load_provenance_maps(allowed)
                if full_prov_seq > new_prov_seq:
                    new_prov_seq = full_prov_seq
                recall_changes, new_recall_rev, need_full_recall = [], last_recall_rev, True
                recall_counts, full_recall_rev = self._load_recall_counts(allowed)
                if full_recall_rev > new_recall_rev:
                    new_recall_rev = full_recall_rev
            else:
                if self._last_prov_stat is not None and prov_stat_now == self._last_prov_stat:
                    prov_changes, new_prov_seq, prov_needs_full = [], last_prov_seq, False
                else:
                    prov_changes, new_prov_seq, prov_needs_full = self._load_provenance_delta(last_prov_seq, allowed_set)
                need_full_prov = prov_needs_full
                if need_full_prov:
                    doc_prov, key_prov, full_prov_seq = self._load_provenance_maps(allowed)
                    if full_prov_seq > new_prov_seq:
                        new_prov_seq = full_prov_seq
                else:
                    doc_prov, key_prov = {}, {}

                if self._last_recall_stat is not None and recall_stat_now == self._last_recall_stat:
                    recall_changes, new_recall_rev, recall_needs_full = [], last_recall_rev, False
                else:
                    recall_changes, new_recall_rev, recall_needs_full = self._load_recall_delta(last_recall_rev, allowed_set)
                need_full_recall = recall_needs_full
                if need_full_recall:
                    recall_counts, full_recall_rev = self._load_recall_counts(allowed)
                    if full_recall_rev > new_recall_rev:
                        new_recall_rev = full_recall_rev
                else:
                    recall_counts = {}

            # Helper to fetch provenance/recall maps lazily only for changed datasets that fall back or for small batches
            loaded_full_prov_for_changed = need_full_prov
            loaded_full_recall_for_changed = need_full_recall

            def _ensure_changed_maps(target_datasets: list[str], specific_doc_ids: list[str] | None = None):
                nonlocal doc_prov, key_prov, new_prov_seq, recall_counts, new_recall_rev
                nonlocal loaded_full_prov_for_changed, loaded_full_recall_for_changed
                if specific_doc_ids is not None and len(specific_doc_ids) <= 200 and not loaded_full_prov_for_changed:
                    if self.provenance_path and self.provenance_path.is_file() and not self.provenance_path.is_symlink():
                        try:
                            pcon = self._get_writer_ro(self.provenance_path)
                            ph_d = ','.join('?' for _ in specific_doc_ids)
                            ph_ds = ','.join('?' for _ in target_datasets)
                            for r_ds, r_doc, r_lt, r_sk, r_fd in pcon.execute(
                                f'''SELECT dataset_id, document_id, lesson_type, state_key, first_source_date
                                    FROM document_provenance
                                    WHERE dataset_id IN ({ph_ds}) AND document_id IN ({ph_d})''',
                                (*target_datasets, *specific_doc_ids),
                            ):
                                doc_prov[(str(r_ds), str(r_doc))] = (
                                    str(r_lt or '').strip(),
                                    str(r_sk or '').strip(),
                                    str(r_fd or '').strip(),
                                )
                            for r_ds, r_lt, r_sk, r_fd in pcon.execute(
                                f'''SELECT dataset_id, lesson_type, state_key, first_source_date
                                    FROM keyed_provenance
                                    WHERE dataset_id IN ({ph_ds})''',
                                target_datasets,
                            ):
                                key_prov[(str(r_ds), str(r_lt or '').strip(), str(r_sk or '').strip())] = str(r_fd or '').strip()
                        except (PolicyError, sqlite3.Error):
                            pass
                elif not loaded_full_prov_for_changed:
                    dp, kp, fseq = self._load_provenance_maps(allowed)
                    doc_prov.update(dp)
                    key_prov.update(kp)
                    if fseq > new_prov_seq:
                        new_prov_seq = fseq
                    loaded_full_prov_for_changed = True

                if specific_doc_ids is not None and len(specific_doc_ids) <= 200 and not loaded_full_recall_for_changed:
                    if self.recall_path and self.recall_path.is_file() and not self.recall_path.is_symlink():
                        try:
                            rcon = self._get_writer_ro(self.recall_path)
                            has_mat = rcon.execute(
                                "SELECT 1 FROM sqlite_master WHERE type='table' AND name='recall_doc_counts' LIMIT 1"
                            ).fetchone()
                            if has_mat:
                                ph_d = ','.join('?' for _ in specific_doc_ids)
                                ph_ds = ','.join('?' for _ in target_datasets)
                                for r_ds, r_doc, r_hits in rcon.execute(
                                    f'''SELECT dataset_id, document_id, total_hits
                                        FROM recall_doc_counts
                                        WHERE dataset_id IN ({ph_ds}) AND document_id IN ({ph_d})''',
                                    (*target_datasets, *specific_doc_ids),
                                ):
                                    recall_counts[(str(r_ds), str(r_doc))] = int(r_hits or 0)
                        except (PolicyError, sqlite3.Error):
                            pass
                elif not loaded_full_recall_for_changed:
                    rc, frev = self._load_recall_counts(allowed)
                    recall_counts.update(rc)
                    if frev > new_recall_rev:
                        new_recall_rev = frev
                    loaded_full_recall_for_changed = True

            # Reconcile each changed dataset inside cognee_db read connection
            dataset_diffs = {}
            incremental_bucket_deltas: dict[str, tuple[dict[str, int], int, int, int]] = {}
            cold_bucket_summaries: dict[str, tuple[dict[str, int], int, int, int, dict[str, int]]] = {}
            for ds in changed_datasets:
                ds_vars = list(dict.fromkeys(v for v in (uuid_hex(ds), ds) if v))
                ph = ','.join('?' for _ in ds_vars)

                # Fast path A: pure append already verified via Stage 1 covering index + PK rowid scan (~4ms!)
                if ds in fast_append_rows:
                    new_rows = fast_append_rows[ds]
                    new_doc_ids = [uuid_text(r[0]) for r in new_rows if uuid_text(r[0])]
                    _ensure_changed_maps([ds], specific_doc_ids=new_doc_ids)
                    upserts = []
                    day_delta: dict[str, int] = {}
                    dk = dr = dc = 0
                    for full_row in new_rows:
                        projected = self._project_cognee_row(
                            full_row,
                            ds,
                            doc_prov=doc_prov,
                            key_prov=key_prov,
                            recall_counts=recall_counts,
                            tz_info=tz_info,
                        )
                        if projected is not None:
                            upserts.append(projected)
                            p_kind = projected[3]
                            p_is_k = int(projected[4])
                            p_day = str(projected[18])
                            if p_is_k:
                                dk += 1
                                if p_day:
                                    day_delta[p_day] = day_delta.get(p_day, 0) + 1
                            if p_kind == 'raw_doc':
                                dr += 1
                            if p_kind == 'common':
                                dc += 1
                    dataset_diffs[ds] = (upserts, [])
                    incremental_bucket_deltas[ds] = (day_delta, dk, dr, dc)
                    continue

                # Fast path B: mathematically verified in-place UPDATE (updated_at > old_max_upd)
                prev_fp = existing_states.get(ds) if not force_full else None
                live_fp = live_fingerprints[ds]
                handled_fast = False
                if prev_fp is not None:
                    old_cnt, old_max_rid, old_sum_rid, old_max_upd, old_chk = prev_fp
                    new_cnt, new_max_rid, new_sum_rid, new_max_upd, new_chk = live_fp
                    if (
                        old_cnt > 0
                        and new_cnt == old_cnt
                        and new_max_rid == old_max_rid
                        and new_sum_rid == old_sum_rid
                        and new_max_upd > old_max_upd
                    ):
                        cand_rids = stage2_updated_rowids.get(ds)
                        if cand_rids:
                            rid_ph = ','.join('?' for _ in cand_rids)
                            upd_rows = cog_con.execute(
                                f'''SELECT id, rowid, name, label, raw_data_location,
                                           {sys_col}, external_metadata, node_set, {pipe_col},
                                           updated_at, created_at, content_hash
                                    FROM data
                                    WHERE rowid IN ({rid_ph})''',
                                cand_rids,
                            ).fetchall()
                        else:
                            upd_rows = []
                        if 0 < len(upd_rows) <= 200:
                            upd_doc_ids = [uuid_text(r[0]) for r in upd_rows if uuid_text(r[0])]
                            if len(upd_doc_ids) == len(upd_rows):
                                ph_u = ','.join('?' for _ in upd_doc_ids)
                                old_cat_rows = {
                                    str(r[0]): (int(r[1]), str(r[2]), str(r[3]), str(r[4]), int(r[5]), str(r[6]))
                                    for r in con.execute(
                                        f'''SELECT doc_id, cognee_rowid, doc_label, content_hash,
                                                   kind, is_knowledge, effective_date
                                            FROM catalog_docs
                                            WHERE dataset_id = ? AND doc_id IN ({ph_u})''',
                                        (ds, *upd_doc_ids),
                                    ).fetchall()
                                }
                                if len(old_cat_rows) == len(upd_rows):
                                    old_sub_chk = 0
                                    new_sub_chk = 0
                                    rowid_ok = True
                                    for r in upd_rows:
                                        did = uuid_text(r[0])
                                        o_rid, o_lbl, o_ch, _, _, _ = old_cat_rows[did]
                                        if o_rid != int(r[1] or 0):
                                            rowid_ok = False
                                            break
                                        old_sub_chk += _row_id_checksum(r[0], o_lbl, o_ch)
                                        new_sub_chk += _row_id_checksum(r[0], r[3], r[11])
                                    if rowid_ok and (old_chk - old_sub_chk + new_sub_chk == new_chk):
                                        _ensure_changed_maps([ds], specific_doc_ids=upd_doc_ids)
                                        upserts = []
                                        deleted_ids = []
                                        day_delta: dict[str, int] = {}
                                        dk = dr = dc = 0
                                        for full_row in upd_rows:
                                            did = uuid_text(full_row[0])
                                            _, _, _, o_kind, o_is_k, o_day = old_cat_rows[did]
                                            if o_is_k:
                                                dk -= 1
                                                if o_day:
                                                    day_delta[o_day] = day_delta.get(o_day, 0) - 1
                                            if o_kind == 'raw_doc':
                                                dr -= 1
                                            if o_kind == 'common':
                                                dc -= 1
                                            projected = self._project_cognee_row(
                                                full_row,
                                                ds,
                                                doc_prov=doc_prov,
                                                key_prov=key_prov,
                                                recall_counts=recall_counts,
                                                tz_info=tz_info,
                                            )
                                            if projected is not None:
                                                upserts.append(projected)
                                                p_kind = projected[3]
                                                p_is_k = int(projected[4])
                                                p_day = str(projected[18])
                                                if p_is_k:
                                                    dk += 1
                                                    if p_day:
                                                        day_delta[p_day] = day_delta.get(p_day, 0) + 1
                                                if p_kind == 'raw_doc':
                                                    dr += 1
                                                if p_kind == 'common':
                                                    dc += 1
                                            else:
                                                deleted_ids.append(did)
                                        dataset_diffs[ds] = (upserts, deleted_ids)
                                        incremental_bucket_deltas[ds] = (day_delta, dk, dr, dc)
                                        handled_fast = True

                if handled_fast:
                    continue

                _ensure_changed_maps(changed_datasets)
                existing_docs = {}
                if not force_full:
                    for doc_id, rowid_val, upd_raw, chash, pstatus in con.execute(
                        '''SELECT doc_id, cognee_rowid, source_updated_at, content_hash, pipeline_status
                           FROM catalog_docs WHERE dataset_id = ?''',
                        (ds,),
                    ):
                        existing_docs[str(doc_id)] = f'{int(rowid_val)}\x1f{upd_raw}\x1f{chash}\x1f{pstatus}'

                # Cold build for this dataset: single sequential scan computing rows, 5-tuple, day buckets & category counts!
                # When catalog_docs is empty, stream directly in bounded 4k chunks to keep RSS low (< 80 MiB).
                if not existing_docs:
                    if stream_cold_bulk and not con.in_transaction:
                        con.execute('BEGIN IMMEDIATE')
                        for idx_name in _CATALOG_INDEX_NAMES:
                            con.execute(f'DROP INDEX IF EXISTS {idx_name}')
                    _COLD_INSERT_SQL = '''INSERT INTO catalog_docs (
                                   dataset_id, doc_id, cognee_rowid, kind, is_knowledge,
                                   common_category, lesson_type, state_key, category, session_id,
                                   doc_name, doc_label, raw_data_location, content_hash,
                                   created_at_raw, created_at_ns, created_at_invalid, updated_at_ns,
                                   effective_date, day_desc_key, sort_ts_desc_key,
                                   recall_count, recall_desc_key, pipeline_status, source_updated_at
                               ) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)'''
                    upserts = []
                    c_cnt = c_max_rid = c_sum_rid = c_chk = 0
                    c_max_upd = ''
                    cold_days: dict[str, int] = {}
                    cold_cats: dict[str, int] = {}
                    cold_k = cold_raw = cold_com = 0
                    for full_row in cog_con.execute(
                        f'''SELECT id, rowid, name, label, raw_data_location,
                                   {sys_col}, external_metadata, node_set, {pipe_col},
                                   updated_at, created_at, content_hash
                            FROM data WHERE dataset_id IN ({ph})''',
                        ds_vars,
                    ):
                        rid_val = int(full_row[1] or 0)
                        c_cnt += 1
                        if rid_val > c_max_rid:
                            c_max_rid = rid_val
                        c_sum_rid += rid_val
                        upd_raw = full_row[9]
                        if isinstance(upd_raw, str) and upd_raw and '0' <= upd_raw[0] <= '9' and upd_raw > c_max_upd:
                            c_max_upd = upd_raw
                        c_chk += _row_id_checksum(full_row[0], full_row[3], full_row[11])
                        projected = self._project_cognee_row(
                            full_row,
                            ds,
                            doc_prov=doc_prov,
                            key_prov=key_prov,
                            recall_counts=recall_counts,
                            tz_info=tz_info,
                        )
                        if projected is not None:
                            upserts.append(projected)
                            p_kind = projected[3]
                            if projected[4]:
                                cold_k += 1
                                p_day = projected[18]
                                if p_day:
                                    cold_days[p_day] = cold_days.get(p_day, 0) + 1
                                p_cat = projected[8]
                                if p_cat:
                                    cold_cats[p_cat] = cold_cats.get(p_cat, 0) + 1
                            elif p_kind == 'raw_doc':
                                cold_raw += 1
                            if p_kind == 'common':
                                cold_com += 1
                            if stream_cold_bulk and len(upserts) >= 2000:
                                upserts.sort(key=itemgetter(0, 1))
                                con.executemany(_COLD_INSERT_SQL, upserts)
                                upserts.clear()
                    if stream_cold_bulk:
                        if upserts:
                            upserts.sort(key=itemgetter(0, 1))
                            con.executemany(_COLD_INSERT_SQL, upserts)
                            upserts.clear()
                    live_fingerprints[ds] = (c_cnt, c_max_rid, c_sum_rid, c_max_upd, c_chk)
                    cold_bucket_summaries[ds] = (cold_days, cold_k, cold_raw, cold_com, cold_cats)
                    dataset_diffs[ds] = ([] if stream_cold_bulk else upserts, [])
                    continue

                # Streaming single-pass diff: avoids allocating a second 50k live_scalar dict!
                to_fetch_raw_ids = []
                derived_check_existing: set[str] = set()
                for raw_id, rid, upd, chash, pstatus in cog_con.execute(
                    f'''SELECT id, rowid, updated_at, content_hash, {pipe_col}
                        FROM data WHERE dataset_id IN ({ph})''',
                    ds_vars,
                ):
                    did = uuid_text(raw_id)
                    if not did:
                        continue
                    prev = existing_docs.pop(did, None)
                    sig_now = f'{int(rid or 0)}\x1f{iso_text(upd)}\x1f{chash or ""}\x1f{_extract_cognify_status(pstatus, ds)}'
                    if prev is None or prev != sig_now:
                        to_fetch_raw_ids.append(raw_id)
                        if prev is not None:
                            derived_check_existing.add(did)

                deleted_ids = list(existing_docs.keys())
                existing_docs.clear()

                upserts = []
                for start in range(0, len(to_fetch_raw_ids), _MAX_IN_BATCH):
                    batch = to_fetch_raw_ids[start : start + _MAX_IN_BATCH]
                    id_ph = ','.join('?' for _ in batch)
                    for full_row in cog_con.execute(
                        f'''SELECT id, rowid, name, label, raw_data_location,
                                   {sys_col}, external_metadata, node_set, {pipe_col},
                                   updated_at, created_at, content_hash
                            FROM data WHERE id IN ({id_ph})''',
                        batch,
                    ):
                        projected = self._project_cognee_row(
                            full_row,
                            ds,
                            doc_prov=doc_prov,
                            key_prov=key_prov,
                            recall_counts=recall_counts,
                            tz_info=tz_info,
                        )
                        if projected is not None:
                            upserts.append(projected)
                        else:
                            # If previously indexed but now classified as derived, delete it
                            did = uuid_text(full_row[0])
                            if did in derived_check_existing:
                                deleted_ids.append(did)

                dataset_diffs[ds] = (upserts, deleted_ids)
        except (PolicyError, sqlite3.Error):
            if con.in_transaction:
                try:
                    con.execute('ROLLBACK')
                except sqlite3.Error:
                    pass
            self._close_writer_ro_all()
            raise
        except Exception:
            if con.in_transaction:
                try:
                    con.execute('ROLLBACK')
                except sqlite3.Error:
                    pass
            raise
        finally:
            if cog_in_tx:
                try:
                    cog_con.execute('COMMIT')
                except sqlite3.Error:
                    pass

        # Apply all updates atomically in one catalog_index write transaction
        dirty_datasets = set(changed_datasets)
        now_iso = _iso_now()
        total_upserts = sum(len(u) for u, _ in dataset_diffs.values())
        use_bulk_index_build = bool(stream_cold_bulk and con.in_transaction)
        if not con.in_transaction:
            con.execute('BEGIN IMMEDIATE')
        try:
            if not use_bulk_index_build and total_upserts >= 5000 and con.execute('SELECT 1 FROM catalog_docs LIMIT 1').fetchone() is None:
                use_bulk_index_build = True
                for idx_name in _CATALOG_INDEX_NAMES:
                    con.execute(f'DROP INDEX IF EXISTS {idx_name}')

            for ds in removed_datasets:
                con.execute('DELETE FROM catalog_docs WHERE dataset_id = ?', (ds,))
                con.execute('DELETE FROM catalog_day_buckets WHERE dataset_id = ?', (ds,))
                con.execute('DELETE FROM dataset_sync_state WHERE dataset_id = ?', (ds,))

            for ds, (upserts, deleted_ids) in dataset_diffs.items():
                if deleted_ids:
                    for start in range(0, len(deleted_ids), _MAX_IN_BATCH):
                        batch = deleted_ids[start : start + _MAX_IN_BATCH]
                        ph = ','.join('?' for _ in batch)
                        con.execute(
                            f'DELETE FROM catalog_docs WHERE dataset_id = ? AND doc_id IN ({ph})',
                            (ds, *batch),
                        )
                if upserts:
                    if use_bulk_index_build:
                        upserts.sort(key=itemgetter(0, 1))
                        con.executemany(
                            '''INSERT INTO catalog_docs (
                                   dataset_id, doc_id, cognee_rowid, kind, is_knowledge,
                                   common_category, lesson_type, state_key, category, session_id,
                                   doc_name, doc_label, raw_data_location, content_hash,
                                   created_at_raw, created_at_ns, created_at_invalid, updated_at_ns,
                                   effective_date, day_desc_key, sort_ts_desc_key,
                                   recall_count, recall_desc_key, pipeline_status, source_updated_at
                               ) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)''',
                            upserts,
                        )
                    else:
                        con.executemany(
                            '''INSERT INTO catalog_docs (
                                   dataset_id, doc_id, cognee_rowid, kind, is_knowledge,
                                   common_category, lesson_type, state_key, category, session_id,
                                   doc_name, doc_label, raw_data_location, content_hash,
                                   created_at_raw, created_at_ns, created_at_invalid, updated_at_ns,
                                   effective_date, day_desc_key, sort_ts_desc_key,
                                   recall_count, recall_desc_key, pipeline_status, source_updated_at
                               ) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
                               ON CONFLICT(dataset_id, doc_id) DO UPDATE SET
                                 cognee_rowid=excluded.cognee_rowid,
                                 kind=excluded.kind,
                                 is_knowledge=excluded.is_knowledge,
                                 common_category=excluded.common_category,
                                 lesson_type=excluded.lesson_type,
                                 state_key=excluded.state_key,
                                 category=excluded.category,
                                 session_id=excluded.session_id,
                                 doc_name=excluded.doc_name,
                                 doc_label=excluded.doc_label,
                                 raw_data_location=excluded.raw_data_location,
                                 content_hash=excluded.content_hash,
                                 created_at_raw=excluded.created_at_raw,
                                 created_at_ns=excluded.created_at_ns,
                                 created_at_invalid=excluded.created_at_invalid,
                                 updated_at_ns=excluded.updated_at_ns,
                                 effective_date=excluded.effective_date,
                                 day_desc_key=excluded.day_desc_key,
                                 sort_ts_desc_key=excluded.sort_ts_desc_key,
                                 recall_count=excluded.recall_count,
                                 recall_desc_key=excluded.recall_desc_key,
                                 pipeline_status=excluded.pipeline_status,
                                 source_updated_at=excluded.source_updated_at''',
                            upserts,
                        )
            if use_bulk_index_build:
                for stmt in _CATALOG_INDEX_DDL.strip().split(';'):
                    s = stmt.strip()
                    if s:
                        con.execute(s)

            # Apply incremental provenance date updates to unaffected datasets
            for ds, doc_id, lt, sk, fdate in prov_changes:
                if ds in changed_datasets:
                    continue
                valid_day = _calendar_date(fdate)
                if not valid_day:
                    continue
                row = con.execute(
                    'SELECT effective_date FROM catalog_docs WHERE dataset_id = ? AND doc_id = ?',
                    (ds, doc_id),
                ).fetchone()
                if row is not None and (not row[0] or valid_day < row[0]):
                    con.execute(
                        '''UPDATE catalog_docs
                           SET effective_date = ?, day_desc_key = ?
                           WHERE dataset_id = ? AND doc_id = ?''',
                        (valid_day, day_to_desc_key(valid_day), ds, doc_id),
                    )
                    dirty_datasets.add(ds)

            # Sync recall counts: O(delta) via recall_doc_counts.rev when possible, else full diff
            if need_full_recall:
                for ds in allowed:
                    if ds in cold_bucket_summaries:
                        continue
                    current_recalls = {}
                    for doc_id, rcnt in con.execute(
                        'SELECT doc_id, recall_count FROM catalog_docs WHERE dataset_id = ? AND recall_count > 0',
                        (ds,),
                    ).fetchall():
                        current_recalls[str(doc_id)] = int(rcnt)

                    ds_live_recalls = {
                        doc_id: cnt
                        for (r_ds, doc_id), cnt in recall_counts.items()
                        if r_ds == ds and cnt > 0
                    }
                    updates = []
                    for doc_id, new_cnt in ds_live_recalls.items():
                        if current_recalls.get(doc_id, 0) != new_cnt:
                            updates.append((new_cnt, -new_cnt, ds, doc_id))
                    for doc_id in current_recalls:
                        if doc_id not in ds_live_recalls:
                            updates.append((0, 0, ds, doc_id))
                    if updates:
                        con.executemany(
                            '''UPDATE catalog_docs
                               SET recall_count = ?, recall_desc_key = ?
                               WHERE dataset_id = ? AND doc_id = ?''',
                            updates,
                        )
                        dirty_datasets.add(ds)
            elif recall_changes:
                delta_updates = []
                for ds, doc_id, new_cnt in recall_changes:
                    delta_updates.append((new_cnt, -new_cnt, ds, doc_id))
                    dirty_datasets.add(ds)
                con.executemany(
                    '''UPDATE catalog_docs
                       SET recall_count = ?, recall_desc_key = ?
                       WHERE dataset_id = ? AND doc_id = ?''',
                    delta_updates,
                )

            # Timezone change: recompute effective_date for non-locked fallback docs
            if tz_changed and not force_full:
                if not doc_prov and allowed:
                    doc_prov, key_prov, _ = self._load_provenance_maps(allowed)
                for ds in allowed:
                    tz_updates = []
                    for doc_id, lt, sk, created_ns, cur_day in con.execute(
                        '''SELECT doc_id, lesson_type, state_key, created_at_ns, effective_date
                           FROM catalog_docs
                           WHERE dataset_id = ? AND is_knowledge = 1 AND created_at_invalid = 0''',
                        (ds,),
                    ).fetchall():
                        prov = doc_prov.get((ds, doc_id))
                        locked = (prov and _calendar_date(prov[2])) or (lt and sk and _calendar_date(key_prov.get((ds, lt, sk))))
                        if locked:
                            continue
                        recomputed = _local_day_from_utc_ns(str(created_ns), tz_info)
                        if recomputed and recomputed != cur_day:
                            tz_updates.append((recomputed, day_to_desc_key(recomputed), ds, doc_id))
                    if tz_updates:
                        con.executemany(
                            '''UPDATE catalog_docs
                               SET effective_date = ?, day_desc_key = ?
                               WHERE dataset_id = ? AND doc_id = ?''',
                            tz_updates,
                        )
                        dirty_datasets.add(ds)

            mutated = bool(dirty_datasets or removed_datasets or tz_changed or new_prov_seq != last_prov_seq or force_full)
            next_gen = cur_gen + 1 if mutated else cur_gen
            for ds in sorted(dirty_datasets):
                if ds in cold_bucket_summaries and not tz_changed:
                    cold_days, k_cnt, raw_cnt, com_cnt, _cold_cats = cold_bucket_summaries[ds]
                    con.execute('DELETE FROM catalog_day_buckets WHERE dataset_id = ?', (ds,))
                    if cold_days:
                        con.executemany(
                            'INSERT INTO catalog_day_buckets(dataset_id, effective_date, knowledge_count) VALUES (?,?,?)',
                            [(ds, day_k, cnt_v) for day_k, cnt_v in cold_days.items()],
                        )
                    doc_count, max_rowid, sum_rowid, max_updated, id_checksum = live_fingerprints[ds]
                    ver_raw = f'{next_gen}|{ds}|{doc_count}|{sum_rowid}|{max_updated}|{id_checksum}|{new_prov_seq}|{k_cnt}'
                    catalog_version = hashlib.sha256(ver_raw.encode('utf-8')).hexdigest()[:24]
                    con.execute(
                        '''INSERT INTO dataset_sync_state
                           (dataset_id, dataset_hex, doc_count, max_rowid, sum_rowid, max_updated_at, id_checksum,
                            knowledge_count, raw_doc_count, common_count, catalog_version, synced_at)
                           VALUES (?,?,?,?,?,?,?,?,?,?,?,?)
                           ON CONFLICT(dataset_id) DO UPDATE SET
                             dataset_hex=excluded.dataset_hex,
                             doc_count=excluded.doc_count,
                             max_rowid=excluded.max_rowid,
                             sum_rowid=excluded.sum_rowid,
                             max_updated_at=excluded.max_updated_at,
                             id_checksum=excluded.id_checksum,
                             knowledge_count=excluded.knowledge_count,
                             raw_doc_count=excluded.raw_doc_count,
                             common_count=excluded.common_count,
                             catalog_version=excluded.catalog_version,
                             synced_at=excluded.synced_at''',
                        (
                            ds, uuid_hex(ds), doc_count, max_rowid, sum_rowid, max_updated, id_checksum,
                            k_cnt, raw_cnt, com_cnt, catalog_version, now_iso,
                        ),
                    )
                    continue
                if ds in incremental_bucket_deltas and not tz_changed:
                    day_delta, dk, dr, dc = incremental_bucket_deltas[ds]
                    for day_str, delta_cnt in day_delta.items():
                        if delta_cnt == 0:
                            continue
                        cur_b = con.execute(
                            'SELECT knowledge_count FROM catalog_day_buckets WHERE dataset_id = ? AND effective_date = ?',
                            (ds, day_str),
                        ).fetchone()
                        new_b = (int(cur_b[0]) if cur_b else 0) + delta_cnt
                        if new_b <= 0:
                            con.execute(
                                'DELETE FROM catalog_day_buckets WHERE dataset_id = ? AND effective_date = ?',
                                (ds, day_str),
                            )
                        else:
                            con.execute(
                                '''INSERT INTO catalog_day_buckets(dataset_id, effective_date, knowledge_count)
                                   VALUES(?, ?, ?)
                                   ON CONFLICT(dataset_id, effective_date) DO UPDATE SET
                                     knowledge_count = excluded.knowledge_count''',
                                (ds, day_str, new_b),
                            )
                    prev_counts = con.execute(
                        'SELECT knowledge_count, raw_doc_count, common_count FROM dataset_sync_state WHERE dataset_id = ?',
                        (ds,),
                    ).fetchone()
                    if prev_counts is not None:
                        k_cnt = max(0, int(prev_counts[0]) + dk)
                        raw_cnt = max(0, int(prev_counts[1]) + dr)
                        com_cnt = max(0, int(prev_counts[2]) + dc)
                        doc_count, max_rowid, sum_rowid, max_updated, id_checksum = live_fingerprints[ds]
                        ver_raw = f'{next_gen}|{ds}|{doc_count}|{sum_rowid}|{max_updated}|{id_checksum}|{new_prov_seq}|{k_cnt}'
                        catalog_version = hashlib.sha256(ver_raw.encode('utf-8')).hexdigest()[:24]
                        con.execute(
                            '''UPDATE dataset_sync_state
                               SET doc_count=?, max_rowid=?, sum_rowid=?, max_updated_at=?, id_checksum=?,
                                   knowledge_count=?, raw_doc_count=?, common_count=?,
                                   catalog_version=?, synced_at=?
                               WHERE dataset_id=?''',
                            (
                                doc_count, max_rowid, sum_rowid, max_updated, id_checksum,
                                k_cnt, raw_cnt, com_cnt, catalog_version, now_iso, ds,
                            ),
                        )
                        continue
                self._rebuild_day_buckets_and_state_locked(
                    con, ds, live_fingerprints[ds], next_gen, new_prov_seq, now_iso
                )

            if mutated:
                con.execute(
                    '''UPDATE catalog_meta
                       SET generation = ?, local_tz_name = ?, last_provenance_seq = ?,
                           last_recall_rev = ?, built_at = ?
                       WHERE singleton = 1''',
                    (next_gen, tz_name, new_prov_seq, new_recall_rev, now_iso),
                )
            con.execute('COMMIT')
            if use_bulk_index_build or (dirty_datasets and not incremental_bucket_deltas):
                con.execute('PRAGMA shrink_memory')
            self._last_prov_stat = prov_stat_now
            self._last_recall_stat = recall_stat_now
            # Record pure append-only generation deltas and maintain O(1) category_counts cache!
            pure_append = (
                mutated
                and len(dirty_datasets) == 1
                and not removed_datasets
                and not tz_changed
                and not prov_changes
                and not recall_changes
                and not force_full
            )
            if pure_append:
                only_ds = next(iter(dirty_datasets))
                if only_ds in incremental_bucket_deltas:
                    upserts_only, del_only = dataset_diffs.get(only_ds, ([], []))
                    if upserts_only and not del_only:
                        appended = [
                            (str(p[1]), str(p[15]), int(p[16]))
                            for p in upserts_only
                            if int(p[4]) == 1
                        ]
                        with self._state_lock:
                            self._recent_appends[next_gen] = (only_ds, appended)
                            if len(self._recent_appends) > 64:
                                oldest_k = min(self._recent_appends)
                                self._recent_appends.pop(oldest_k, None)
                            prev_cats = self._cat_counts_cache.get((cur_gen, only_ds))
                            if prev_cats is not None:
                                updated_cats = dict(prev_cats)
                                for p in upserts_only:
                                    if int(p[4]) == 1 and p[8]:
                                        c_lbl = str(p[8])
                                        updated_cats[c_lbl] = updated_cats.get(c_lbl, 0) + 1
                                self._cat_counts_cache[(next_gen, only_ds)] = updated_cats
                            # Keep unaffected datasets valid across generation bump
                            for (g_k, ds_k), cats_v in list(self._cat_counts_cache.items()):
                                if g_k == cur_gen and ds_k != only_ds:
                                    self._cat_counts_cache[(next_gen, ds_k)] = cats_v
                            if len(self._cat_counts_cache) > 64:
                                self._cat_counts_cache = {
                                    k: v for k, v in self._cat_counts_cache.items() if k[0] >= next_gen - 2
                                }
                    else:
                        with self._state_lock:
                            self._recent_appends.clear()
                            self._cat_counts_cache.clear()
                else:
                    with self._state_lock:
                        self._recent_appends.clear()
                        self._cat_counts_cache.clear()
            elif mutated:
                with self._state_lock:
                    self._recent_appends.clear()
                    self._cat_counts_cache.clear()
                    for ds_c, (_days, _k, _r, _c, cold_cats) in cold_bucket_summaries.items():
                        self._cat_counts_cache[(next_gen, ds_c)] = dict(cold_cats)
            return next_gen
        except Exception:
            if con.in_transaction:
                con.execute('ROLLBACK')
            raise

    def sync(self, managed_datasets: list[str], *, force: bool = False) -> int:
        """Synchronize catalog index on the single dedicated writer thread.

        O(1) stat short-circuit returns immediately (<0.2ms) when none of the
        underlying SQLite stores, timezone, or managed dataset set changed.
        Concurrent callers targeting the same dataset set coalesce onto the
        single in-flight sync future instead of queueing redundant scans.
        """
        ds_tuple = tuple(sorted({uuid_text(ds) for ds in managed_datasets if ds}))
        if not force:
            sig = self._compute_stat_signature(ds_tuple)
            with self._state_lock:
                if not self._rebuilding and self._last_stat_sig == sig:
                    return self._last_generation
                inflight = self._inflight_sync
                if inflight is not None and inflight[0] == ds_tuple:
                    shared_future = inflight[1]
                else:
                    shared_future = None
            if shared_future is not None:
                return shared_future.result()

        def _run():
            with self._write_lock:
                sig_before = self._compute_stat_signature(ds_tuple)
                if not force:
                    with self._state_lock:
                        if not self._rebuilding and self._last_stat_sig == sig_before:
                            return self._last_generation
                try:
                    gen = self._sync_into_connection(self._write_con, list(ds_tuple), force_full=force)
                except sqlite3.DatabaseError:
                    gen = self._atomic_rebuild_locked(list(ds_tuple))
                with self._state_lock:
                    self._last_stat_sig = sig_before
                    self._last_generation = gen
                return gen

        with self._state_lock:
            if not force:
                sig_check = self._compute_stat_signature(ds_tuple)
                if not self._rebuilding and self._last_stat_sig == sig_check:
                    return self._last_generation
                inflight = self._inflight_sync
                if inflight is not None and inflight[0] == ds_tuple:
                    shared_future = inflight[1]
                else:
                    shared_future = None
                if shared_future is not None:
                    fut = shared_future
                    owns_slot = False
                else:
                    fut = self._writer_executor.submit(_run)
                    self._inflight_sync = (ds_tuple, fut)
                    owns_slot = True
            else:
                fut = self._writer_executor.submit(_run)
                self._inflight_sync = (ds_tuple, fut)
                owns_slot = True

        try:
            return fut.result()
        finally:
            if owns_slot:
                with self._state_lock:
                    if self._inflight_sync is not None and self._inflight_sync[1] is fut:
                        self._inflight_sync = None

    def _atomic_rebuild_locked(self, managed_datasets: list[str]) -> int:
        """Self-healing atomic generation rebuild: build temp DB -> quick_check -> os.replace."""
        with self._state_lock:
            self._rebuilding = True
        tmp_path = self.path.parent / f'{self.path.name}.rebuilding.{os.getpid()}.{time.time_ns()}'
        tmp_con = None
        try:
            self._invalidate_readers()
            try:
                self._write_con.close()
            except Exception:
                pass
            tmp_con = _open_private_rw(tmp_path)
            _create_catalog_schema(tmp_con, self.tz_provider())
            gen = self._sync_into_connection(tmp_con, managed_datasets, force_full=True)
            check = tmp_con.execute('PRAGMA quick_check').fetchone()
            if not check or str(check[0]).lower() != 'ok':
                raise sqlite3.DatabaseError('catalog index quick_check failed after rebuild')
            tmp_con.execute('PRAGMA wal_checkpoint(TRUNCATE)')
            tmp_con.close()
            tmp_con = None
            for suffix in ('-wal', '-shm'):
                for base in (tmp_path, self.path):
                    sidecar = Path(str(base) + suffix)
                    try:
                        sidecar.unlink(missing_ok=True)
                    except OSError:
                        pass
            os.replace(tmp_path, self.path)
            os.chmod(self.path, 0o600)
            self._write_con = _open_private_rw(self.path)
            _create_catalog_schema(self._write_con, self.tz_provider())
            self._invalidate_readers()
            return gen
        finally:
            if tmp_con is not None:
                try:
                    tmp_con.close()
                except Exception:
                    pass
            try:
                tmp_path.unlink(missing_ok=True)
            except OSError:
                pass
            with self._state_lock:
                self._rebuilding = False

    def rebuild_atomic(self, managed_datasets: list[str]) -> int:
        ds_list = sorted({uuid_text(ds) for ds in managed_datasets if ds})

        def _run():
            with self._write_lock:
                gen = self._atomic_rebuild_locked(ds_list)
                with self._state_lock:
                    self._last_stat_sig = self._compute_stat_signature(tuple(ds_list))
                    self._last_generation = gen
                return gen

        return self._writer_executor.submit(_run).result()

    # ============ Read-only O(log N + K) query methods ============

    def meta(self) -> dict:
        con = self._get_reader()
        row = con.execute(
            'SELECT schema_version, generation, local_tz_name, last_provenance_seq, last_recall_rev, built_at '
            'FROM catalog_meta WHERE singleton = 1'
        ).fetchone()
        if row is None:
            return {'schema_version': CATALOG_SCHEMA_VERSION, 'generation': 0, 'local_tz_name': self.tz_provider()}
        return {
            'schema_version': int(row[0]),
            'generation': int(row[1]),
            'local_tz_name': str(row[2]),
            'last_provenance_seq': int(row[3]),
            'last_recall_rev': int(row[4]),
            'built_at': str(row[5]),
        }

    def generation(self) -> int:
        return self.meta()['generation']

    def get_append_deltas(self, from_gen: int, to_gen: int, dataset_id: str):
        """Return appended knowledge tuples ``[(doc_id, created_at_ns, created_at_invalid)]``
        if EVERY generation in ``(from_gen, to_gen]`` was either a pure append on ``dataset_id``
        or touched a different dataset; otherwise return ``None``.
        """
        if to_gen < from_gen or to_gen - from_gen > 64:
            return None
        if to_gen == from_gen:
            return []
        ds = uuid_text(dataset_id)
        out = []
        with self._state_lock:
            for g in range(from_gen + 1, to_gen + 1):
                entry = self._recent_appends.get(g)
                if entry is None:
                    return None
                entry_ds, rows = entry
                if entry_ds == ds:
                    out.extend(rows)
        return out

    def dataset_sync_info(self, dataset_id: str) -> dict | None:
        con = self._get_reader()
        row = con.execute(
            '''SELECT dataset_id, doc_count, knowledge_count, raw_doc_count, common_count, catalog_version
               FROM dataset_sync_state WHERE dataset_id = ?''',
            (uuid_text(dataset_id),),
        ).fetchone()
        if row is None:
            return None
        return {
            'dataset_id': str(row[0]),
            'doc_count': int(row[1]),
            'knowledge_count': int(row[2]),
            'raw_doc_count': int(row[3]),
            'common_count': int(row[4]),
            'catalog_version': str(row[5]),
        }

    def all_dataset_sync_info(self, allowed_datasets: list[str]) -> dict[str, dict]:
        if not allowed_datasets:
            return {}
        con = self._get_reader()
        out = {}
        for start in range(0, len(allowed_datasets), _MAX_IN_BATCH):
            batch = [uuid_text(ds) for ds in allowed_datasets[start : start + _MAX_IN_BATCH]]
            ph = ','.join('?' for _ in batch)
            for row in con.execute(
                f'''SELECT dataset_id, doc_count, knowledge_count, raw_doc_count, common_count, catalog_version
                    FROM dataset_sync_state WHERE dataset_id IN ({ph})''',
                batch,
            ):
                out[str(row[0])] = {
                    'dataset_id': str(row[0]),
                    'doc_count': int(row[1]),
                    'knowledge_count': int(row[2]),
                    'raw_doc_count': int(row[3]),
                    'common_count': int(row[4]),
                    'catalog_version': str(row[5]),
                }
        return out

    def overview_day_buckets(self, allowed_datasets: list[str], *, days: int = 365) -> list[dict]:
        if not allowed_datasets:
            return []
        con = self._get_reader()
        totals: dict[str, int] = {}
        for start in range(0, len(allowed_datasets), _MAX_IN_BATCH):
            batch = [uuid_text(ds) for ds in allowed_datasets[start : start + _MAX_IN_BATCH]]
            ph = ','.join('?' for _ in batch)
            for day, cnt in con.execute(
                f'''SELECT effective_date, SUM(knowledge_count)
                    FROM catalog_day_buckets
                    WHERE dataset_id IN ({ph})
                    GROUP BY effective_date''',
                batch,
            ):
                totals[str(day)] = totals.get(str(day), 0) + int(cnt)
        ordered = sorted(totals.items(), key=lambda pair: pair[0], reverse=True)
        if days > 0:
            ordered = ordered[:days]
        return [{'day': day, 'count': count} for day, count in ordered]

    def query_overview_recent(self, allowed_datasets: list[str], *, limit: int = 20) -> list[dict]:
        if not allowed_datasets:
            return []
        allowed_set = {uuid_text(ds) for ds in allowed_datasets}
        con = self._get_reader()
        # Single equality on is_knowledge=1 traverses idx_cdocs_overview without IN(...) degradation.
        out = []
        for row in con.execute(
            f'''SELECT {_SELECT_DOC_COLS}
                FROM catalog_docs
                WHERE is_knowledge = 1
                ORDER BY day_desc_key ASC, dataset_id ASC, doc_id ASC'''
        ):
            if str(row[0]) not in allowed_set:
                continue
            out.append(_row_to_doc_dict(row))
            if len(out) >= limit:
                break
        return out

    def query_overview_day(
        self,
        allowed_datasets: list[str],
        day: str,
        *,
        limit: int = 30,
        after_key: tuple[str, str] | None = None,
    ) -> tuple[list[dict], bool, int]:
        if not allowed_datasets:
            return [], False, 0
        allowed_set = {uuid_text(ds) for ds in allowed_datasets}
        con = self._get_reader()
        total_count = 0
        for start in range(0, len(allowed_datasets), _MAX_IN_BATCH):
            batch = [uuid_text(ds) for ds in allowed_datasets[start : start + _MAX_IN_BATCH]]
            ph = ','.join('?' for _ in batch)
            row = con.execute(
                f'''SELECT COALESCE(SUM(knowledge_count), 0)
                    FROM catalog_day_buckets
                    WHERE effective_date = ? AND dataset_id IN ({ph})''',
                (day, *batch),
            ).fetchone()
            total_count += int(row[0]) if row else 0

        day_key = day_to_desc_key(day)
        if after_key is not None:
            sql = f'''SELECT {_SELECT_DOC_COLS}
                      FROM catalog_docs
                      WHERE is_knowledge = 1 AND day_desc_key = ?
                        AND (dataset_id, doc_id) > (?, ?)
                      ORDER BY dataset_id ASC, doc_id ASC'''
            params = (day_key, after_key[0], after_key[1])
        else:
            sql = f'''SELECT {_SELECT_DOC_COLS}
                      FROM catalog_docs
                      WHERE is_knowledge = 1 AND day_desc_key = ?
                      ORDER BY dataset_id ASC, doc_id ASC'''
            params = (day_key,)

        items = []
        has_more = False
        for row in con.execute(sql, params):
            if str(row[0]) not in allowed_set:
                continue
            if len(items) < limit:
                items.append(_row_to_doc_dict(row))
            else:
                has_more = True
                break
        return items, has_more, total_count

    def query_project_knowledge(
        self,
        dataset_id: str,
        *,
        sort: str = 'date',
        category: str = '',
        allowed_doc_ids: set[str] | None = None,
        limit: int = 30,
        after_key: tuple | None = None,
    ) -> dict:
        """Query project knowledge with same-direction tuple Keyset pagination (zero TEMP B-TREE)."""
        ds = uuid_text(dataset_id)
        con = self._get_reader()

        # Sparse subset path (when unread_only or recall_(agent_id, event_id) filter is active)
        if allowed_doc_ids is not None:
            if not allowed_doc_ids:
                return {'items': [], 'has_more': False, 'total_filtered': 0, 'category_counts': {}}
            id_list = sorted(allowed_doc_ids)
            base_rows = []
            for start in range(0, len(id_list), _MAX_IN_BATCH):
                batch = id_list[start : start + _MAX_IN_BATCH]
                ph = ','.join('?' for _ in batch)
                for row in con.execute(
                    f'''SELECT {_SELECT_DOC_COLS}
                        FROM catalog_docs
                        WHERE dataset_id = ? AND is_knowledge = 1 AND doc_id IN ({ph})''',
                    (ds, *batch),
                ):
                    base_rows.append(_row_to_doc_dict(row))

            category_counts: dict[str, int] = {}
            for item in base_rows:
                cat = item['category']
                if cat:
                    category_counts[cat] = category_counts.get(cat, 0) + 1

            filtered = [item for item in base_rows if not category or item['category'] == category]
            if sort == 'recall':
                filtered.sort(key=lambda r: (r['recall_desc_key'], r['day_desc_key'], r['doc_id']))
                if after_key is not None:
                    filtered = [
                        r for r in filtered
                        if (r['recall_desc_key'], r['day_desc_key'], r['doc_id']) > after_key
                    ]
            else:
                filtered.sort(key=lambda r: (r['day_desc_key'], r['doc_id']))
                if after_key is not None:
                    filtered = [
                        r for r in filtered
                        if (r['day_desc_key'], r['doc_id']) > after_key
                    ]
            total_filtered = len(base_rows) if not category else sum(
                1 for item in base_rows if item['category'] == category
            )
            page = filtered[:limit]
            return {
                'items': page,
                'has_more': len(filtered) > limit,
                'total_filtered': total_filtered,
                'category_counts': category_counts,
            }

        # Unfiltered / category-filtered indexed path using same-direction tuple Keyset indexes!
        cur_gen = self.generation()
        cat_cache_key = (cur_gen, ds)
        with self._state_lock:
            cached_cats = self._cat_counts_cache.get(cat_cache_key)
        if cached_cats is not None:
            category_counts = dict(cached_cats)
        else:
            category_counts = {}
            for cat, cnt in con.execute(
                '''SELECT category, COUNT(*)
                   FROM catalog_docs
                   WHERE dataset_id = ? AND is_knowledge = 1 AND category != ''
                   GROUP BY category''',
                (ds,),
            ):
                category_counts[str(cat)] = int(cnt)
            with self._state_lock:
                if len(self._cat_counts_cache) >= 64:
                    self._cat_counts_cache.clear()
                self._cat_counts_cache[cat_cache_key] = dict(category_counts)

        if category:
            total_filtered = category_counts.get(category, 0)
            if sort == 'recall':
                if after_key is not None:
                    sql = f'''SELECT {_SELECT_DOC_COLS}
                              FROM catalog_docs
                              WHERE dataset_id = ? AND is_knowledge = 1 AND category = ?
                                AND (recall_desc_key, day_desc_key, doc_id) > (?, ?, ?)
                              ORDER BY recall_desc_key ASC, day_desc_key ASC, doc_id ASC
                              LIMIT ?'''
                    params = (ds, category, int(after_key[0]), int(after_key[1]), str(after_key[2]), limit + 1)
                else:
                    sql = f'''SELECT {_SELECT_DOC_COLS}
                              FROM catalog_docs
                              WHERE dataset_id = ? AND is_knowledge = 1 AND category = ?
                              ORDER BY recall_desc_key ASC, day_desc_key ASC, doc_id ASC
                              LIMIT ?'''
                    params = (ds, category, limit + 1)
            else:
                if after_key is not None:
                    sql = f'''SELECT {_SELECT_DOC_COLS}
                              FROM catalog_docs
                              WHERE dataset_id = ? AND is_knowledge = 1 AND category = ?
                                AND (day_desc_key, doc_id) > (?, ?)
                              ORDER BY day_desc_key ASC, doc_id ASC
                              LIMIT ?'''
                    params = (ds, category, int(after_key[0]), str(after_key[1]), limit + 1)
                else:
                    sql = f'''SELECT {_SELECT_DOC_COLS}
                              FROM catalog_docs
                              WHERE dataset_id = ? AND is_knowledge = 1 AND category = ?
                              ORDER BY day_desc_key ASC, doc_id ASC
                              LIMIT ?'''
                    params = (ds, category, limit + 1)
        else:
            sync_info = self.dataset_sync_info(ds)
            total_filtered = sync_info['knowledge_count'] if sync_info else 0
            if sort == 'recall':
                if after_key is not None:
                    sql = f'''SELECT {_SELECT_DOC_COLS}
                              FROM catalog_docs
                              WHERE dataset_id = ? AND is_knowledge = 1
                                AND (recall_desc_key, day_desc_key, doc_id) > (?, ?, ?)
                              ORDER BY recall_desc_key ASC, day_desc_key ASC, doc_id ASC
                              LIMIT ?'''
                    params = (ds, int(after_key[0]), int(after_key[1]), str(after_key[2]), limit + 1)
                else:
                    sql = f'''SELECT {_SELECT_DOC_COLS}
                              FROM catalog_docs
                              WHERE dataset_id = ? AND is_knowledge = 1
                              ORDER BY recall_desc_key ASC, day_desc_key ASC, doc_id ASC
                              LIMIT ?'''
                    params = (ds, limit + 1)
            else:
                if after_key is not None:
                    sql = f'''SELECT {_SELECT_DOC_COLS}
                              FROM catalog_docs
                              WHERE dataset_id = ? AND is_knowledge = 1
                                AND (day_desc_key, doc_id) > (?, ?)
                              ORDER BY day_desc_key ASC, doc_id ASC
                              LIMIT ?'''
                    params = (ds, int(after_key[0]), str(after_key[1]), limit + 1)
                else:
                    sql = f'''SELECT {_SELECT_DOC_COLS}
                              FROM catalog_docs
                              WHERE dataset_id = ? AND is_knowledge = 1
                              ORDER BY day_desc_key ASC, doc_id ASC
                              LIMIT ?'''
                    params = (ds, limit + 1)

        rows = [_row_to_doc_dict(r) for r in con.execute(sql, params).fetchall()]
        has_more = len(rows) > limit
        return {
            'items': rows[:limit],
            'has_more': has_more,
            'total_filtered': total_filtered,
            'category_counts': category_counts,
        }

    def query_common_knowledge(
        self,
        dataset_id: str,
        *,
        common_category: str = 'all',
        limit: int = 30,
        after_key: tuple[int, str] | None = None,
    ) -> dict:
        """Query common knowledge sorted by full updatedAt/createdAt timestamp DESC."""
        ds = uuid_text(dataset_id)
        con = self._get_reader()
        category_counts = {'preference': 0, 'experience': 0}
        for ccat, cnt in con.execute(
            '''SELECT common_category, COUNT(*)
               FROM catalog_docs
               WHERE dataset_id = ? AND kind = 'common'
               GROUP BY common_category''',
            (ds,),
        ):
            if ccat in category_counts:
                category_counts[str(ccat)] = int(cnt)

        if common_category in ('preference', 'experience'):
            total_filtered = category_counts[common_category]
            if after_key is not None:
                sql = f'''SELECT {_SELECT_DOC_COLS}
                          FROM catalog_docs
                          WHERE dataset_id = ? AND kind = 'common' AND common_category = ?
                            AND (sort_ts_desc_key, doc_id) > (?, ?)
                          ORDER BY sort_ts_desc_key ASC, doc_id ASC
                          LIMIT ?'''
                params = (ds, common_category, int(after_key[0]), str(after_key[1]), limit + 1)
            else:
                sql = f'''SELECT {_SELECT_DOC_COLS}
                          FROM catalog_docs
                          WHERE dataset_id = ? AND kind = 'common' AND common_category = ?
                          ORDER BY sort_ts_desc_key ASC, doc_id ASC
                          LIMIT ?'''
                params = (ds, common_category, limit + 1)
        else:
            total_filtered = category_counts['preference'] + category_counts['experience']
            if after_key is not None:
                sql = f'''SELECT {_SELECT_DOC_COLS}
                          FROM catalog_docs
                          WHERE dataset_id = ? AND kind = 'common'
                            AND (sort_ts_desc_key, doc_id) > (?, ?)
                          ORDER BY sort_ts_desc_key ASC, doc_id ASC
                          LIMIT ?'''
                params = (ds, int(after_key[0]), str(after_key[1]), limit + 1)
            else:
                sql = f'''SELECT {_SELECT_DOC_COLS}
                          FROM catalog_docs
                          WHERE dataset_id = ? AND kind = 'common'
                          ORDER BY sort_ts_desc_key ASC, doc_id ASC
                          LIMIT ?'''
                params = (ds, limit + 1)

        rows = [_row_to_doc_dict(r) for r in con.execute(sql, params).fetchall()]
        return {
            'items': rows[:limit],
            'has_more': len(rows) > limit,
            'total_filtered': total_filtered,
            'category_counts': category_counts,
        }

    def query_raw_docs_page(
        self,
        dataset_id: str,
        *,
        limit: int = 30,
        after_key: tuple[int, str] | None = None,
    ) -> list[dict]:
        """Indexed keyset scan over raw_doc branch for dual-branch raw-records merge."""
        ds = uuid_text(dataset_id)
        con = self._get_reader()
        if after_key is not None:
            sql = f'''SELECT {_SELECT_DOC_COLS}
                      FROM catalog_docs
                      WHERE dataset_id = ? AND kind = 'raw_doc'
                        AND (sort_ts_desc_key, doc_id) > (?, ?)
                      ORDER BY sort_ts_desc_key ASC, doc_id ASC
                      LIMIT ?'''
            params = (ds, int(after_key[0]), str(after_key[1]), limit + 1)
        else:
            sql = f'''SELECT {_SELECT_DOC_COLS}
                      FROM catalog_docs
                      WHERE dataset_id = ? AND kind = 'raw_doc'
                      ORDER BY sort_ts_desc_key ASC, doc_id ASC
                      LIMIT ?'''
            params = (ds, limit + 1)
        return [_row_to_doc_dict(r) for r in con.execute(sql, params).fetchall()]

    def query_pending_raw_docs(self, dataset_id: str, *, limit: int = 20) -> tuple[list[dict], int]:
        ds = uuid_text(dataset_id)
        con = self._get_reader()
        total = int(
            con.execute(
                '''SELECT COUNT(*) FROM catalog_docs
                   WHERE dataset_id = ? AND kind = 'raw_doc'
                     AND pipeline_status != 'DATA_ITEM_PROCESSING_COMPLETED' ''',
                (ds,),
            ).fetchone()[0]
        )
        rows = [
            _row_to_doc_dict(r)
            for r in con.execute(
                f'''SELECT {_SELECT_DOC_COLS}
                    FROM catalog_docs
                    WHERE dataset_id = ? AND kind = 'raw_doc'
                      AND pipeline_status != 'DATA_ITEM_PROCESSING_COMPLETED'
                    ORDER BY sort_ts_desc_key ASC, doc_id ASC
                    LIMIT ?''',
                (ds, limit),
            ).fetchall()
        ]
        return rows, total

    def query_search_metadata(
        self,
        allowed_datasets: list[str],
        query: str,
        *,
        filter_kind: str = 'all',
        limit: int = 30,
    ) -> tuple[list[dict], bool]:
        if not allowed_datasets or not query.strip():
            return [], False
        needle = query.strip().lower()
        like_pat = '%' + needle.replace('\\', '\\\\').replace('%', '\\%').replace('_', '\\_') + '%'
        con = self._get_reader()
        kind_clause = ''
        if filter_kind == 'knowledge':
            kind_clause = ' AND is_knowledge = 1'
        elif filter_kind == 'raw':
            kind_clause = " AND kind = 'raw_doc'"

        results = []
        for start in range(0, len(allowed_datasets), _MAX_IN_BATCH):
            batch = [uuid_text(ds) for ds in allowed_datasets[start : start + _MAX_IN_BATCH]]
            ph = ','.join('?' for _ in batch)
            rows = con.execute(
                f'''SELECT {_SELECT_DOC_COLS}
                    FROM catalog_docs
                    WHERE dataset_id IN ({ph}){kind_clause}
                      AND (
                        lower(doc_label) LIKE ? ESCAPE '\\'
                        OR lower(doc_name) LIKE ? ESCAPE '\\'
                        OR lower(category) LIKE ? ESCAPE '\\'
                        OR lower(state_key) LIKE ? ESCAPE '\\'
                        OR lower(lesson_type) LIKE ? ESCAPE '\\'
                        OR lower(session_id) LIKE ? ESCAPE '\\'
                      )
                    ORDER BY day_desc_key ASC, dataset_id ASC, doc_id ASC
                    LIMIT ?''',
                (*batch, like_pat, like_pat, like_pat, like_pat, like_pat, like_pat, limit + 1),
            ).fetchall()
            for row in rows:
                item = _row_to_doc_dict(row)
                match_field = 'doc_name'
                for fname, fval in (
                    ('doc_label', item['doc_label'] or ''),
                    ('doc_name', item['doc_name']),
                    ('category', item['category']),
                    ('state_key', item['state_key']),
                    ('lesson_type', item['lesson_type']),
                    ('session_id', item['session_id']),
                ):
                    if needle in fval.lower():
                        match_field = fname
                        break
                item['match_field'] = match_field
                results.append(item)
        results.sort(key=lambda r: (r['day_desc_key'], r['dataset_id'], r['doc_id']))
        return results[:limit], len(results) > limit

    # ============ Sparse unread support helpers (no cross-DB ATTACH) ============

    def knowledge_docs_after_horizon(self, dataset_id: str, horizon_ns: str) -> list[tuple[str, str]]:
        """Indexed scan on idx_cdocs_proj_created for Branch B (created_at_ns > horizon_ns)."""
        ds = uuid_text(dataset_id)
        con = self._get_reader()
        return [
            (str(r[0]), str(r[1]))
            for r in con.execute(
                '''SELECT doc_id, created_at_ns
                   FROM catalog_docs
                   WHERE dataset_id = ? AND is_knowledge = 1 AND created_at_ns > ?
                   ORDER BY created_at_ns DESC, doc_id ASC''',
                (ds, horizon_ns),
            ).fetchall()
        ]

    def iter_knowledge_doc_ids_after_horizon(
        self,
        dataset_id: str,
        horizon_ns: str,
        *,
        batch_size: int = _MAX_IN_BATCH,
    ):
        """Stream post-horizon knowledge doc_ids in bounded chunks without allocating a full list."""
        ds = uuid_text(dataset_id)
        con = self._get_reader()
        cur = con.execute(
            '''SELECT doc_id
               FROM catalog_docs
               WHERE dataset_id = ? AND is_knowledge = 1 AND created_at_ns > ?
               ORDER BY created_at_ns DESC, doc_id ASC''',
            (ds, horizon_ns),
        )
        while True:
            rows = cur.fetchmany(batch_size)
            if not rows:
                break
            yield [str(r[0]) for r in rows]

    def knowledge_docs_invalid_created(self, dataset_id: str) -> list[str]:
        """Partial-index scan on idx_cdocs_proj_invalid_ts for Branch C (created_at_invalid = 1)."""
        ds = uuid_text(dataset_id)
        con = self._get_reader()
        return [
            str(r[0])
            for r in con.execute(
                '''SELECT doc_id
                   FROM catalog_docs
                   WHERE dataset_id = ? AND is_knowledge = 1 AND created_at_invalid = 1''',
                (ds,),
            ).fetchall()
        ]

    def knowledge_docs_in_range(self, dataset_id: str, low_ns: str, high_ns: str) -> list[str]:
        """Fetch knowledge doc_ids with low_ns < created_at_ns <= high_ns (for safe re-upgrade merge)."""
        ds = uuid_text(dataset_id)
        con = self._get_reader()
        return [
            str(r[0])
            for r in con.execute(
                '''SELECT doc_id
                   FROM catalog_docs
                   WHERE dataset_id = ? AND is_knowledge = 1
                     AND created_at_ns > ? AND created_at_ns <= ?''',
                (ds, low_ns, high_ns),
            ).fetchall()
        ]

    def max_knowledge_created_at_ns(self, dataset_id: str) -> str:
        ds = uuid_text(dataset_id)
        con = self._get_reader()
        row = con.execute(
            '''SELECT created_at_ns
               FROM catalog_docs
               WHERE dataset_id = ? AND is_knowledge = 1 AND created_at_ns != ''
               ORDER BY created_at_ns DESC, doc_id ASC
               LIMIT 1''',
            (ds,),
        ).fetchone()
        return str(row[0]) if row else ''

    def existing_knowledge_doc_ids(self, dataset_id: str, doc_ids: list[str]) -> set[str]:
        if not doc_ids:
            return set()
        ds = uuid_text(dataset_id)
        con = self._get_reader()
        found = set()
        for start in range(0, len(doc_ids), _MAX_IN_BATCH):
            batch = doc_ids[start : start + _MAX_IN_BATCH]
            ph = ','.join('?' for _ in batch)
            for (did,) in con.execute(
                f'''SELECT doc_id FROM catalog_docs
                    WHERE dataset_id = ? AND is_knowledge = 1 AND doc_id IN ({ph})''',
                (ds, *batch),
            ):
                found.add(str(did))
        return found

    def doc_created_at_info(self, dataset_id: str, doc_ids: list[str]) -> dict[str, tuple[str, int]]:
        """Return {doc_id: (created_at_ns, created_at_invalid)} for the given doc_ids."""
        if not doc_ids:
            return {}
        ds = uuid_text(dataset_id)
        con = self._get_reader()
        out = {}
        for start in range(0, len(doc_ids), _MAX_IN_BATCH):
            batch = doc_ids[start : start + _MAX_IN_BATCH]
            ph = ','.join('?' for _ in batch)
            for did, cns, cinv in con.execute(
                f'''SELECT doc_id, created_at_ns, created_at_invalid
                    FROM catalog_docs
                    WHERE dataset_id = ? AND doc_id IN ({ph})''',
                (ds, *batch),
            ):
                out[str(did)] = (str(cns), int(cinv))
        return out

    def fetch_docs_by_ids(self, dataset_id: str, doc_ids: list[str]) -> list[dict]:
        if not doc_ids:
            return []
        ds = uuid_text(dataset_id)
        con = self._get_reader()
        out = []
        for start in range(0, len(doc_ids), _MAX_IN_BATCH):
            batch = doc_ids[start : start + _MAX_IN_BATCH]
            ph = ','.join('?' for _ in batch)
            for row in con.execute(
                f'''SELECT {_SELECT_DOC_COLS}
                    FROM catalog_docs
                    WHERE dataset_id = ? AND doc_id IN ({ph})''',
                (ds, *batch),
            ):
                out.append(_row_to_doc_dict(row))
        return out
