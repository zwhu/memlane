"""Rebuildable minimal metadata projection (catalog-index.sqlite3) for O(log N + K) /v2 queries.

Strictly obeys zero-body-copy red lines: persists ONLY whitelisted structural metadata
(never body text, raw_text, why_learned, metadata_json, or session QA/trace payloads).
Uses a single writer with chunked cryptographic full-column reconciliation, atomic
generation publishing, same-direction tuple Keyset indexes, refcounted reader leases,
and corruption self-healing rebuild.
"""
import asyncio
import calendar
from concurrent.futures import ThreadPoolExecutor
from contextlib import contextmanager
from datetime import date, datetime, timezone
from functools import lru_cache
import hashlib
import heapq
import itertools
from operator import itemgetter
import os
from pathlib import Path
import re
import sqlite3
import threading
import time
from zoneinfo import ZoneInfo, ZoneInfoNotFoundError

from project_policy import PolicyError
from provenance_store import _calendar_date
from sqlite_ro import (
    _LESSON_CATEGORY_LABELS,
    extract_trusted_metadata,
    iso_text,
    official_sqlite_ro,
    open_official_sqlite_ro,
    parse_json_value,
    ro_snapshot,
    uuid_hex,
    uuid_text,
)

CATALOG_SCHEMA_VERSION = 3
_MAX_IN_BATCH = 400
_CHUNK_SPAN = 512
_CHUNK_SHIFT = 9  # 1 << 9 == 512
_NULL_HASH_SENTINEL = '\x01\x02NULL\x03'
_MULTI_INSERT_ROWS = 100
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
                local_sec = (
                    day_sec
                    + int(utc_ns_iso[11:13]) * 3600
                    + int(utc_ns_iso[14:16]) * 60
                    + int(utc_ns_iso[17:19])
                    + off
                )
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
    try:
        st_main = path.stat()
        main_sig = (st_main.st_ino, st_main.st_mtime_ns, st_main.st_size)
    except OSError:
        main_sig = (0, 0, 0)
    try:
        st_wal = Path(str(path) + '-wal').stat()
        # A 0-byte -wal file contains zero frames (created even by read-only WAL opens).
        # Normalizing size==0 to (0, 0, 0) prevents spurious resyncs after cold RO opens
        # while still detecting any frame write (size>0) or checkpoint truncation (main_sig changes).
        wal_sig = (st_wal.st_ino, st_wal.st_mtime_ns, st_wal.st_size) if st_wal.st_size > 0 else (0, 0, 0)
    except OSError:
        wal_sig = (0, 0, 0)
    return main_sig, wal_sig


def _iso_now() -> str:
    return datetime.now(timezone.utc).isoformat(timespec='microseconds')


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


def _hash_col_str(val) -> str:
    """Exact Python mirror of SQLite ``ifnull(col, _NULL_HASH_SENTINEL)``."""
    if val is None:
        return _NULL_HASH_SENTINEL
    if isinstance(val, bytes):
        return val.decode('utf-8', errors='replace')
    return str(val)


def _serialize_data_row_q(full_row) -> bytes:
    """Serialize all 13 projection input columns identically to SQLite ``ifnull(...)`` concat.

    ``full_row`` column order:
      (0:id, 1:rowid, 2:name, 3:label, 4:raw_data_location, 5:system_metadata,
       6:external_metadata, 7:node_set, 8:pipeline_status, 9:updated_at,
       10:created_at, 11:content_hash, 12:mime_type)
    """
    rid = int(full_row[1] or 0)
    return (
        f'{rid}\x1f{_hash_col_str(full_row[0])}\x1f{_hash_col_str(full_row[2])}\x1f'
        f'{_hash_col_str(full_row[3])}\x1f{_hash_col_str(full_row[4])}\x1f{_hash_col_str(full_row[12])}\x1f'
        f'{_hash_col_str(full_row[5])}\x1f{_hash_col_str(full_row[6])}\x1f{_hash_col_str(full_row[7])}\x1f'
        f'{_hash_col_str(full_row[8])}\x1f{_hash_col_str(full_row[9])}\x1f{_hash_col_str(full_row[10])}\x1f'
        f'{_hash_col_str(full_row[11])}'
    ).encode('utf-8')


def _fold_chunk_digests(chunk_map: dict[int, bytes]) -> str:
    h = hashlib.blake2b(digest_size=16)
    for bkt in sorted(chunk_map):
        h.update(f'{bkt}:'.encode('ascii'))
        h.update(chunk_map[bkt])
        h.update(b'\n')
    return h.hexdigest()


_CATALOG_INDEX_DDL = '''
        CREATE UNIQUE INDEX IF NOT EXISTS idx_cdocs_pk
            ON catalog_docs(dataset_id, doc_id);
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
        CREATE INDEX IF NOT EXISTS idx_cdocs_ds_rowid
            ON catalog_docs(dataset_id, cognee_rowid, is_knowledge, doc_id);
'''

_CATALOG_SESSION_INDEX_DDL = '''
        CREATE UNIQUE INDEX IF NOT EXISTS idx_csess_pk
            ON catalog_sessions(dataset_id, user_id, session_id);
        CREATE INDEX IF NOT EXISTS idx_csess_top_ts
            ON catalog_sessions(dataset_id, is_top_level, sort_ts_desc_key ASC, user_id ASC, session_id ASC)
            WHERE is_top_level = 1;
        CREATE INDEX IF NOT EXISTS idx_csess_root_ts
            ON catalog_sessions(dataset_id, root_session_id, sort_ts_desc_key ASC, user_id ASC, session_id ASC)
            WHERE is_top_level = 0;
        CREATE INDEX IF NOT EXISTS idx_csess_parent_ts
            ON catalog_sessions(dataset_id, parent_session_id, sort_ts_desc_key ASC, user_id ASC, session_id ASC)
            WHERE parent_session_id != '';
        CREATE INDEX IF NOT EXISTS idx_csess_ds_sid
            ON catalog_sessions(dataset_id, session_id, user_id);
        CREATE INDEX IF NOT EXISTS idx_csess_recent_ts
            ON catalog_sessions(sort_ts_desc_key ASC, user_id ASC, session_id ASC);
'''

_CATALOG_INDEX_NAMES = (
    'idx_cdocs_pk',
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
    'idx_cdocs_proj_rowid',
    'idx_cdocs_ds_rowid',
)

_CATALOG_SESSION_INDEX_NAMES = (
    'idx_csess_pk',
    'idx_csess_top_ts',
    'idx_csess_root_ts',
    'idx_csess_parent_ts',
    'idx_csess_ds_sid',
    'idx_csess_recent_ts',
)


def _create_catalog_schema(con: sqlite3.Connection, tz_name: str):
    con.execute('PRAGMA page_size=8192')
    con.execute('PRAGMA journal_mode=WAL')
    con.execute('PRAGMA journal_size_limit=4194304')
    con.execute('PRAGMA wal_autocheckpoint=1000')
    con.execute('PRAGMA synchronous=NORMAL')
    con.execute('PRAGMA temp_store=MEMORY')
    con.execute('PRAGMA cache_size=-1024')
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
            data_hash TEXT NOT NULL DEFAULT '',
            knowledge_count INTEGER NOT NULL,
            raw_doc_count INTEGER NOT NULL,
            common_count INTEGER NOT NULL,
            session_count INTEGER NOT NULL DEFAULT 0,
            session_sig TEXT NOT NULL DEFAULT '',
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
            effective_date_source TEXT NOT NULL DEFAULT '',
            day_desc_key INTEGER NOT NULL,
            sort_ts_desc_key INTEGER NOT NULL,
            recall_count INTEGER NOT NULL DEFAULT 0,
            recall_desc_key INTEGER NOT NULL DEFAULT 0,
            pipeline_status TEXT NOT NULL DEFAULT '',
            source_updated_at TEXT NOT NULL DEFAULT ''
        );

        CREATE TABLE IF NOT EXISTS catalog_day_buckets (
            dataset_id TEXT NOT NULL,
            effective_date TEXT NOT NULL,
            knowledge_count INTEGER NOT NULL,
            PRIMARY KEY (dataset_id, effective_date)
        ) WITHOUT ROWID;

        CREATE TABLE IF NOT EXISTS catalog_sessions (
            dataset_id TEXT NOT NULL,
            session_id TEXT NOT NULL,
            user_id TEXT NOT NULL DEFAULT '',
            agent_id TEXT NOT NULL DEFAULT '',
            status TEXT NOT NULL DEFAULT '',
            started_at TEXT NOT NULL DEFAULT '',
            last_activity_at TEXT NOT NULL DEFAULT '',
            sort_ts_desc_key INTEGER NOT NULL DEFAULT 0,
            parent_session_id TEXT NOT NULL DEFAULT '',
            root_session_id TEXT NOT NULL DEFAULT '',
            subagent_role TEXT NOT NULL DEFAULT '',
            nesting_depth INTEGER NOT NULL DEFAULT 0,
            is_top_level INTEGER NOT NULL DEFAULT 1 CHECK (is_top_level IN (0, 1)),
            child_count INTEGER NOT NULL DEFAULT 0
        );
    ''' + _CATALOG_INDEX_DDL + _CATALOG_SESSION_INDEX_DDL)

    # Backward-compatible in-place schema migration from v2 -> v3 using explicit literal DDL
    state_cols = {str(r[1]) for r in con.execute('PRAGMA table_info(dataset_sync_state)').fetchall()}
    if 'id_checksum' not in state_cols:
        con.execute('ALTER TABLE dataset_sync_state ADD COLUMN id_checksum INTEGER NOT NULL DEFAULT 0')
    if 'data_hash' not in state_cols:
        con.execute("ALTER TABLE dataset_sync_state ADD COLUMN data_hash TEXT NOT NULL DEFAULT ''")
    if 'session_count' not in state_cols:
        con.execute('ALTER TABLE dataset_sync_state ADD COLUMN session_count INTEGER NOT NULL DEFAULT 0')
    if 'session_sig' not in state_cols:
        con.execute("ALTER TABLE dataset_sync_state ADD COLUMN session_sig TEXT NOT NULL DEFAULT ''")

    doc_cols = {str(r[1]) for r in con.execute('PRAGMA table_info(catalog_docs)').fetchall()}
    if 'effective_date_source' not in doc_cols:
        con.execute("ALTER TABLE catalog_docs ADD COLUMN effective_date_source TEXT NOT NULL DEFAULT ''")

    con.execute(
        '''INSERT OR IGNORE INTO catalog_meta
           (singleton, schema_version, generation, local_tz_name, last_provenance_seq, last_recall_rev, built_at)
           VALUES (1, ?, 0, ?, 0, 0, ?)''',
        (CATALOG_SCHEMA_VERSION, tz_name, _iso_now()),
    )
    con.execute(
        'UPDATE catalog_meta SET schema_version = ? WHERE singleton = 1 AND schema_version < ?',
        (CATALOG_SCHEMA_VERSION, CATALOG_SCHEMA_VERSION),
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
        'cognee_rowid': int(row[2]),
        'kind': row[3],
        'is_knowledge': int(row[4]),
        'common_category': row[5],
        'lesson_type': row[6],
        'state_key': row[7],
        'category': row[8],
        'session_id': row[9],
        'doc_name': row[10],
        'doc_label': row[11] if row[11] else None,
        'raw_data_location': row[12],
        'content_hash': row[13],
        'created_at': row[14],
        'created_at_ns': row[15],
        'created_at_invalid': int(row[16]),
        'updated_at_ns': row[17],
        'effective_date': row[18],
        'effective_date_source': row[19],
        'day_desc_key': int(row[20]),
        'sort_ts_desc_key': int(row[21]),
        'recall_count': int(row[22]),
        'recall_desc_key': int(row[23]),
        'pipeline_status': row[24],
        'updated_at': row[25],
    }


def _row_to_session_dict(row) -> dict:
    return {
        'dataset_id': str(row[0]),
        'session_id': str(row[1]),
        'user_id': str(row[2]),
        'agent_id': str(row[3]),
        'status': str(row[4]),
        'started_at': str(row[5]),
        'last_activity_at': str(row[6]),
        'sort_ts_desc_key': int(row[7]),
        'parent_session_id': str(row[8]),
        'root_session_id': str(row[9]),
        'subagent_role': str(row[10]),
        'nesting_depth': int(row[11]),
        'is_top_level': int(row[12]),
        'child_count': int(row[13]),
    }


_SELECT_DOC_COLS = '''
    dataset_id, doc_id, cognee_rowid, kind, is_knowledge, common_category, lesson_type, state_key,
    category, session_id, doc_name, doc_label, raw_data_location, content_hash,
    created_at_raw, created_at_ns, created_at_invalid, updated_at_ns, effective_date,
    effective_date_source, day_desc_key, sort_ts_desc_key, recall_count, recall_desc_key,
    pipeline_status, source_updated_at
'''

_SELECT_SESS_COLS = '''
    dataset_id, session_id, user_id, agent_id, status, started_at, last_activity_at,
    sort_ts_desc_key, parent_session_id, root_session_id, subagent_role,
    nesting_depth, is_top_level, child_count
'''

_INSERT_DOC_SQL = '''INSERT INTO catalog_docs (
    dataset_id, doc_id, cognee_rowid, kind, is_knowledge,
    common_category, lesson_type, state_key, category, session_id,
    doc_name, doc_label, raw_data_location, content_hash,
    created_at_raw, created_at_ns, created_at_invalid, updated_at_ns,
    effective_date, effective_date_source, day_desc_key, sort_ts_desc_key,
    recall_count, recall_desc_key, pipeline_status, source_updated_at
) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)'''

_INSERT_DOC_MULTI_SQL = (
    _INSERT_DOC_SQL.split('VALUES')[0]
    + 'VALUES '
    + ','.join(['(' + ','.join('?' * 26) + ')'] * _MULTI_INSERT_ROWS)
)


_INSERT_SESS_SQL = '''INSERT INTO catalog_sessions (
    dataset_id, session_id, user_id, agent_id, status, started_at,
    last_activity_at, sort_ts_desc_key, parent_session_id, root_session_id,
    subagent_role, nesting_depth, is_top_level, child_count
) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)'''

_INSERT_SESS_MULTI_SQL = (
    _INSERT_SESS_SQL.split('VALUES')[0]
    + 'VALUES '
    + ','.join(['(' + ','.join('?' * 14) + ')'] * _MULTI_INSERT_ROWS)
)


def _bulk_insert_sessions(con: sqlite3.Connection, rows: list[tuple]):
    """Insert ``rows`` into ``catalog_sessions`` using 100-row batched INSERT statements."""
    n = len(rows)
    if n == 0:
        return
    full_end = (n // _MULTI_INSERT_ROWS) * _MULTI_INSERT_ROWS
    chain = itertools.chain.from_iterable
    for start in range(0, full_end, _MULTI_INSERT_ROWS):
        con.execute(_INSERT_SESS_MULTI_SQL, tuple(chain(rows[start : start + _MULTI_INSERT_ROWS])))
    if full_end < n:
        con.executemany(_INSERT_SESS_SQL, rows[full_end:])


def _bulk_insert_docs(con: sqlite3.Connection, rows: list[tuple]):
    """Insert ``rows`` into ``catalog_docs`` using 100-row batched INSERT statements."""
    n = len(rows)
    if n == 0:
        return
    full_end = (n // _MULTI_INSERT_ROWS) * _MULTI_INSERT_ROWS
    chain = itertools.chain.from_iterable
    for start in range(0, full_end, _MULTI_INSERT_ROWS):
        con.execute(_INSERT_DOC_MULTI_SQL, tuple(chain(rows[start : start + _MULTI_INSERT_ROWS])))
    if full_end < n:
        con.executemany(_INSERT_DOC_SQL, rows[full_end:])

_UPSERT_DOC_SQL = _INSERT_DOC_SQL + '''
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
  effective_date_source=excluded.effective_date_source,
  day_desc_key=excluded.day_desc_key,
  sort_ts_desc_key=excluded.sort_ts_desc_key,
  recall_count=excluded.recall_count,
  recall_desc_key=excluded.recall_desc_key,
  pipeline_status=excluded.pipeline_status,
  source_updated_at=excluded.source_updated_at'''


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
        self._closed = False
        self._last_stat_sig = None
        self._last_prov_stat = None
        self._last_recall_stat = None
        self._last_generation = 0
        self._inflight_sync = None
        self._recent_appends: dict[int, tuple[str, list[tuple[str, str, int, int]]]] = {}
        self._cat_counts_cache: dict[tuple[int, str], dict[str, int]] = {}
        self._common_cat_counts_cache: dict[tuple[int, str], dict[str, int]] = {}
        # In-memory cryptographic chunk digests per dataset: ds -> {bucket_id: 16-byte blake2b}
        self._chunk_digests: dict[str, dict[int, bytes]] = {}
        self._writer_ro_cons: dict[str, tuple[int, sqlite3.Connection]] = {}
        self._hash_worker_cons: list[tuple[str, int, sqlite3.Connection] | None] = [None] * 6
        self._writer_executor = ThreadPoolExecutor(max_workers=1, thread_name_prefix='catalog-index-writer')
        self._hash_executor = ThreadPoolExecutor(max_workers=6, thread_name_prefix='catalog-hash-ro')

        self._reader_local = threading.local()
        self._reader_epoch = 0
        self._open_readers: list[sqlite3.Connection] = []
        self._readers_lock = threading.Lock()
        self._readers_cond = threading.Condition(self._readers_lock)
        self._active_readers = 0

        self._write_con = _open_private_rw(self.path)
        _create_catalog_schema(self._write_con, self.tz_provider())

    def _check_not_rebuilding(self):
        with self._state_lock:
            if self._closed:
                raise PolicyError('catalog_index_closed', 503)
            if self._rebuilding:
                raise PolicyError('catalog_index_rebuilding', 503)

    def _open_thread_reader(self) -> sqlite3.Connection:
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
        new_con.execute('PRAGMA cache_size = -512')
        new_con.execute('PRAGMA busy_timeout = 2000')
        self._reader_local.con = new_con
        self._reader_local.epoch = epoch
        with self._readers_lock:
            self._open_readers.append(new_con)
        return new_con

    @contextmanager
    def _reader_lease(self):
        with self._readers_lock:
            with self._state_lock:
                if self._closed:
                    raise PolicyError('catalog_index_closed', 503)
                if self._rebuilding:
                    raise PolicyError('catalog_index_rebuilding', 503)
            self._active_readers += 1
        try:
            yield self._open_thread_reader()
        finally:
            with self._readers_lock:
                self._active_readers -= 1
                if self._active_readers == 0:
                    self._readers_cond.notify_all()

    def _get_reader(self) -> sqlite3.Connection:
        """Legacy helper for read-only inspection in tests/benchmarks."""
        self._check_not_rebuilding()
        return self._open_thread_reader()

    def _invalidate_readers(self):
        with self._readers_lock:
            self._reader_epoch += 1
            deadline = time.monotonic() + 5.0
            while self._active_readers > 0:
                rem = deadline - time.monotonic()
                if rem <= 0:
                    break
                self._readers_cond.wait(timeout=rem)
            stale = list(self._open_readers)
            self._open_readers.clear()
        for con in stale:
            try:
                con.close()
            except Exception:
                pass

    def _get_writer_ro(self, db_path: Path) -> sqlite3.Connection:
        """Persistent writer-thread read-only connection via ``open_official_sqlite_ro``."""
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

    def _get_hash_worker_ro(self, slot: int, db_path: Path) -> sqlite3.Connection:
        key = str(db_path)
        try:
            ino = db_path.stat().st_ino
        except OSError:
            ino = -1
        cached = self._hash_worker_cons[slot]
        if cached is not None:
            c_key, c_ino, c_con = cached
            if c_key == key and c_ino == ino and ino != -1:
                return c_con
            self._hash_worker_cons[slot] = None
            try:
                c_con.close()
            except Exception:
                pass
        con = open_official_sqlite_ro(db_path, cache_kib=256)
        con.text_factory = bytes
        self._hash_worker_cons[slot] = (key, ino, con)
        return con

    def _close_writer_ro_all(self):
        for _ino, con in list(self._writer_ro_cons.values()):
            try:
                con.close()
            except Exception:
                pass
        self._writer_ro_cons.clear()
        for i in range(len(self._hash_worker_cons)):
            item = self._hash_worker_cons[i]
            self._hash_worker_cons[i] = None
            if item is not None:
                try:
                    item[2].close()
                except Exception:
                    pass

    def close(self):
        with self._state_lock:
            self._closed = True
        self._invalidate_readers()
        self._writer_executor.shutdown(wait=True)
        self._hash_executor.shutdown(wait=True)
        with self._write_lock:
            self._close_writer_ro_all()
            if self._write_con is not None:
                try:
                    self._write_con.close()
                except Exception:
                    pass
                self._write_con = None

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

    # ============ Single-snapshot Provenance & Recall readers ============

    def _load_provenance_maps(self, allowed_datasets: list[str]) -> tuple[dict, dict, int, bool]:
        """Return ``(doc_prov, key_prov, max_seq, ok)`` from one ``BEGIN DEFERRED`` snapshot."""
        doc_prov = {}
        key_prov = {}
        max_seq = 0
        if not self.provenance_path or not self.provenance_path.is_file() or self.provenance_path.is_symlink() or not allowed_datasets:
            return doc_prov, key_prov, max_seq, True
        try:
            with official_sqlite_ro(self.provenance_path) as con:
                with ro_snapshot(con):
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
            return {}, {}, 0, False
        return doc_prov, key_prov, max_seq, True

    def _load_provenance_delta(self, after_seq: int, allowed_set: set[str]) -> tuple[list, int, bool, bool]:
        """Return ``(changes, new_max_seq, needs_full, ok)`` from one ``BEGIN DEFERRED`` snapshot."""
        changes = []
        new_max_seq = int(after_seq)
        if not self.provenance_path or not self.provenance_path.is_file() or self.provenance_path.is_symlink():
            return changes, new_max_seq, False, True
        try:
            with official_sqlite_ro(self.provenance_path) as con:
                with ro_snapshot(con):
                    cols = {str(r[1]) for r in con.execute('PRAGMA table_info(document_provenance)').fetchall()}
                    if 'sync_seq' not in cols:
                        return [], new_max_seq, True, True
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
                            changes.append((
                                str(ds),
                                str(doc_id),
                                str(lt or '').strip(),
                                str(sk or '').strip(),
                                str(fdate or '').strip(),
                            ))
                    meta_exists = con.execute(
                        "SELECT 1 FROM sqlite_master WHERE type='table' AND name='provenance_meta' LIMIT 1"
                    ).fetchone()
                    if meta_exists:
                        m = con.execute('SELECT last_sync_seq FROM provenance_meta WHERE singleton=1').fetchone()
                        if m and int(m[0] or 0) > new_max_seq:
                            new_max_seq = int(m[0])
        except (PolicyError, sqlite3.Error):
            return [], int(after_seq), False, False
        return changes, new_max_seq, False, True

    def _load_recall_delta(
        self,
        after_rev: int,
        allowed_set: set[str],
    ) -> tuple[list[tuple[str, str, int]], int, bool, bool]:
        """Return ``(changes, new_rev, needs_full, ok)`` from one ``BEGIN DEFERRED`` snapshot."""
        if not self.recall_path or not self.recall_path.is_file() or self.recall_path.is_symlink() or not allowed_set:
            return [], int(after_rev), False, True
        changes: list[tuple[str, str, int]] = []
        new_rev = int(after_rev)
        try:
            with official_sqlite_ro(self.recall_path) as con:
                with ro_snapshot(con):
                    has_mat = con.execute(
                        "SELECT 1 FROM sqlite_master WHERE type='table' AND name='recall_doc_counts' LIMIT 1"
                    ).fetchone()
                    if not has_mat:
                        return [], int(after_rev), True, True
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
            return [], int(after_rev), False, False
        return changes, new_rev, False, True

    def _load_recall_counts(self, allowed_datasets: list[str]) -> tuple[dict[tuple[str, str], int], int, bool]:
        """Return ``(counts, rev, ok)`` from one ``BEGIN DEFERRED`` snapshot."""
        counts = {}
        rev = 0
        if not self.recall_path or not self.recall_path.is_file() or self.recall_path.is_symlink() or not allowed_datasets:
            return counts, rev, True
        try:
            with official_sqlite_ro(self.recall_path) as con:
                with ro_snapshot(con):
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
            return {}, 0, False
        return counts, rev, True

    # ============ Chunked Cryptographic Full-Column Digest Engine ============

    @staticmethod
    def _build_chunk_sql(ds_vars: list[str], col_exprs: dict[str, str]) -> tuple[str, str]:
        ph = ','.join('?' for _ in ds_vars)
        ds_filter = '+dataset_id = ?' if len(ds_vars) == 1 else f'+dataset_id IN ({ph})'
        ns = _NULL_HASH_SENTINEL
        concat_expr = (
            f"rowid||'\x1f'||"
            f"ifnull({col_exprs['id']},'{ns}')||'\x1f'||"
            f"ifnull({col_exprs['name']},'{ns}')||'\x1f'||"
            f"ifnull({col_exprs['label']},'{ns}')||'\x1f'||"
            f"ifnull({col_exprs['raw_data_location']},'{ns}')||'\x1f'||"
            f"ifnull({col_exprs['mime_type']},'{ns}')||'\x1f'||"
            f"ifnull({col_exprs['system_metadata']},'{ns}')||'\x1f'||"
            f"ifnull({col_exprs['external_metadata']},'{ns}')||'\x1f'||"
            f"ifnull({col_exprs['node_set']},'{ns}')||'\x1f'||"
            f"ifnull({col_exprs['pipeline_status']},'{ns}')||'\x1f'||"
            f"ifnull({col_exprs['updated_at']},'{ns}')||'\x1f'||"
            f"ifnull({col_exprs['created_at']},'{ns}')||'\x1f'||"
            f"ifnull({col_exprs['content_hash']},'{ns}')"
        )
        chunk_sql = (
            "SELECT COUNT(*), COALESCE(MAX(rowid), 0), COALESCE(SUM(rowid), 0), "
            "COALESCE(MAX(CASE WHEN substr(updated_at, 1, 1) BETWEEN '0' AND '9' THEN updated_at END), ''), "
            f"GROUP_CONCAT({concat_expr}, '\x1e') "
            f"FROM data WHERE rowid BETWEEN ? AND ? AND {ds_filter}"
        )
        fallback_sql = (
            f"SELECT {col_exprs['id']}, rowid, {col_exprs['name']}, {col_exprs['label']}, "
            f"{col_exprs['raw_data_location']}, {col_exprs['system_metadata']}, {col_exprs['external_metadata']}, "
            f"{col_exprs['node_set']}, {col_exprs['pipeline_status']}, {col_exprs['updated_at']}, "
            f"{col_exprs['created_at']}, {col_exprs['content_hash']}, {col_exprs['mime_type']} "
            f"FROM data WHERE rowid BETWEEN ? AND ? AND {ds_filter} ORDER BY rowid ASC"
        )
        return chunk_sql, fallback_sql

    @staticmethod
    def _hash_bucket_range_on_con(
        con: sqlite3.Connection,
        buckets: list[int],
        ds_vars: list[str],
        chunk_sql: str,
        fallback_sql: str,
    ) -> tuple[dict[int, bytes], int, int, int, str]:
        """Compute cryptographic 16-byte Blake2b digest for each ``rowid`` bucket on ``con``."""
        digs: dict[int, bytes] = {}
        tot_cnt = 0
        max_rid = 0
        sum_rid = 0
        max_upd = ''
        _b2 = hashlib.blake2b
        ds_params = tuple(ds_vars)
        for bkt in buckets:
            lo = bkt << _CHUNK_SHIFT
            hi = lo + _CHUNK_SPAN - 1
            try:
                row = con.execute(chunk_sql, (lo, hi, *ds_params)).fetchone()
            except sqlite3.OperationalError:
                # Pathological giant metadata row exceeded GROUP_CONCAT limit: hash row-by-row
                h = _b2(digest_size=16)
                b_cnt = 0
                for frow in con.execute(fallback_sql, (lo, hi, *ds_params)):
                    if b_cnt > 0:
                        h.update(b'\x1e')
                    h.update(_serialize_data_row_q(frow))
                    b_cnt += 1
                    rid_v = int(frow[1] or 0)
                    tot_cnt += 1
                    if rid_v > max_rid:
                        max_rid = rid_v
                    sum_rid += rid_v
                    u_raw = frow[9]
                    if isinstance(u_raw, bytes):
                        u_raw = u_raw.decode('utf-8', errors='replace')
                    if isinstance(u_raw, str) and u_raw and '0' <= u_raw[0] <= '9' and u_raw > max_upd:
                        max_upd = u_raw
                if b_cnt > 0:
                    digs[bkt] = h.digest()
                continue
            if row is None or not row[0]:
                continue
            c_val = int(row[0])
            tot_cnt += c_val
            m_val = int(row[1] or 0)
            if m_val > max_rid:
                max_rid = m_val
            sum_rid += int(row[2] or 0)
            u_val = row[3]
            if isinstance(u_val, bytes):
                u_val = u_val.decode('utf-8', errors='replace')
            if u_val and u_val > max_upd:
                max_upd = u_val
            payload = row[4]
            if payload is not None:
                raw_b = payload if isinstance(payload, bytes) else str(payload).encode('utf-8')
                digs[bkt] = _b2(raw_b, digest_size=16).digest()
        return digs, tot_cnt, max_rid, sum_rid, max_upd

    def _scan_dataset_chunks(
        self,
        cog_con: sqlite3.Connection,
        cognee_db: Path,
        ds_vars: list[str],
        col_exprs: dict[str, str],
        expected_cnt: int | None,
        min_rid: int,
        max_rid: int,
        expected_sum_rid: int | None = None,
        *,
        cog_snap_sig=None,
        est_cnt: int = 0,
        allow_stale_signal: bool = False,
    ) -> tuple[dict[int, bytes], int, int, int, str] | None:
        """Compute full-13-column cryptographic chunk digests for one dataset.

        Uses 6 parallel read-only WAL connections pinned to the exact same SQLite
        WAL read-mark as ``cog_con`` (verified via ``cog_snap_sig == acq_after``
        plus an O(log N) ``MAX(rowid)`` seek on ``wcons[0]`` in <0.1ms).
        """
        if max_rid <= 0 or (expected_cnt is not None and expected_cnt <= 0):
            return {}, 0, 0, 0, ''
        min_bkt = min_rid >> _CHUNK_SHIFT
        max_bkt = max_rid >> _CHUNK_SHIFT
        span_buckets = max_bkt - min_bkt + 1
        ph = ','.join('?' for _ in ds_vars)
        cnt_hint = expected_cnt if expected_cnt is not None else max(est_cnt, 1000)
        if span_buckets <= max((cnt_hint // _CHUNK_SPAN) * 4 + 16, 128):
            buckets = list(range(min_bkt, max_bkt + 1))
        else:
            buckets = sorted({
                int(r[0])
                for r in cog_con.execute(
                    f'SELECT DISTINCT (rowid >> {_CHUNK_SHIFT}) FROM data WHERE dataset_id IN ({ph})',
                    ds_vars,
                )
            })
        if not buckets:
            return {}, 0, 0, 0, ''

        chunk_sql, fallback_sql = self._build_chunk_sql(ds_vars, col_exprs)

        # Parallel 6-worker RO scan for large datasets (>= 12 buckets) with <0.1ms WAL snapshot pin
        if len(buckets) >= 12 and not self._closed:
            n_workers = 6
            pinned_cons: list[sqlite3.Connection] = []
            try:
                wcons = [self._get_hash_worker_ro(slot, cognee_db) for slot in range(n_workers)]
                acq_before = cog_snap_sig if cog_snap_sig is not None else _file_stat_sig(cognee_db)
                for wcon in wcons:
                    wcon.execute('BEGIN DEFERRED')
                    wcon.execute('SELECT name FROM sqlite_master LIMIT 1').fetchone()
                    pinned_cons.append(wcon)
                acq_after = _file_stat_sig(cognee_db)
                if acq_before != acq_after:
                    if allow_stale_signal:
                        return None
                else:
                    # O(log N) MAX(rowid) seek (<0.03ms) belt-and-suspenders proof wcons[0] matches cog_con
                    w_max = 0
                    for v in ds_vars:
                        mr = wcons[0].execute(
                            'SELECT rowid FROM data WHERE dataset_id = ? ORDER BY rowid DESC LIMIT 1',
                            (v,),
                        ).fetchone()
                        if mr and int(mr[0] or 0) > w_max:
                            w_max = int(mr[0])
                    if w_max != max_rid:
                        if allow_stale_signal:
                            return None
                    else:
                        step = (len(buckets) + n_workers - 1) // n_workers
                        slices = [buckets[i * step : (i + 1) * step] for i in range(n_workers)]

                        def _worker(wcon: sqlite3.Connection, bkt_slice: list[int]):
                            try:
                                if not bkt_slice:
                                    return {}, 0, 0, 0, ''
                                return self._hash_bucket_range_on_con(wcon, bkt_slice, ds_vars, chunk_sql, fallback_sql)
                            finally:
                                if wcon.in_transaction:
                                    try:
                                        wcon.execute('ROLLBACK')
                                    except sqlite3.Error:
                                        pass

                        futs = [self._hash_executor.submit(_worker, wcons[i], slices[i]) for i in range(n_workers)]
                        pinned_cons.clear()  # ownership transferred to workers' finally blocks
                        results = [f.result() for f in futs]
                        merged_d: dict[int, bytes] = {}
                        tot_c = 0
                        tot_m = 0
                        tot_s = 0
                        tot_u = ''
                        for d_i, c_i, m_i, s_i, u_i in results:
                            merged_d.update(d_i)
                            tot_c += c_i
                            if m_i > tot_m:
                                tot_m = m_i
                            tot_s += s_i
                            if u_i > tot_u:
                                tot_u = u_i
                        if (
                            (expected_cnt is None or tot_c == expected_cnt)
                            and tot_m == max_rid
                            and (expected_sum_rid is None or tot_s == expected_sum_rid)
                        ):
                            return merged_d, tot_c, tot_m, tot_s, tot_u
                        if allow_stale_signal:
                            return None
            except (PolicyError, sqlite3.Error, RuntimeError):
                pass
            finally:
                for wcon in pinned_cons:
                    if wcon.in_transaction:
                        try:
                            wcon.execute('ROLLBACK')
                        except sqlite3.Error:
                            pass

        # Single pinned-snapshot fallback on cog_con (guaranteed consistent with cog_con's BEGIN DEFERRED)
        prev_tf = cog_con.text_factory
        cog_con.text_factory = bytes
        try:
            return self._hash_bucket_range_on_con(cog_con, buckets, ds_vars, chunk_sql, fallback_sql)
        finally:
            cog_con.text_factory = prev_tf

    # ============ Row Projection ============

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
            if len(tags) == 1 and tags[0] == 'session_learnings':
                session_id = ''
            else:
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
        effective_date_source = ''
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
                    effective_date_source = 'provenance'
            if not effective_date:
                fsd = meta.get('first_source_date')
                day = _calendar_date(fsd) if fsd else None
                if day:
                    effective_date = day
                    effective_date_source = 'meta'
                else:
                    sdate = meta.get('session_date')
                    day = _calendar_date(sdate) if sdate else None
                    if day:
                        effective_date = day
                        effective_date_source = 'meta'
            if not effective_date and not created_invalid:
                effective_date = _local_day_from_utc_ns(created_ns, tz_info, created_epoch_ns)
                if effective_date:
                    effective_date_source = 'created_at'
        else:
            if not updated_invalid:
                effective_date = _local_day_from_utc_ns(updated_ns, tz_info, updated_epoch_ns)
                if effective_date:
                    effective_date_source = 'updated_at'
            elif not created_invalid:
                effective_date = _local_day_from_utc_ns(created_ns, tz_info, created_epoch_ns)
                if effective_date:
                    effective_date_source = 'created_at'

        day_desc = (
            -int(effective_date[0:4] + effective_date[5:7] + effective_date[8:10])
            if len(effective_date) == 10
            else day_to_desc_key(effective_date)
        )
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
            effective_date_source,
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
        fingerprint: tuple[int, int, int, str, str],
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
        doc_count, max_rowid, sum_rowid, max_updated, data_hash = fingerprint
        ver_raw = f'{generation}|{dataset_id}|{doc_count}|{sum_rowid}|{max_updated}|{data_hash}|{prov_seq}|{k_cnt}'
        catalog_version = hashlib.sha256(ver_raw.encode('utf-8')).hexdigest()[:24]
        con.execute(
            '''INSERT INTO dataset_sync_state
               (dataset_id, dataset_hex, doc_count, max_rowid, sum_rowid, max_updated_at, id_checksum,
                data_hash, knowledge_count, raw_doc_count, common_count, catalog_version, synced_at)
               VALUES (?,?,?,?,?,?,0,?,?,?,?,?,?)
               ON CONFLICT(dataset_id) DO UPDATE SET
                 dataset_hex=excluded.dataset_hex,
                 doc_count=excluded.doc_count,
                 max_rowid=excluded.max_rowid,
                 sum_rowid=excluded.sum_rowid,
                 max_updated_at=excluded.max_updated_at,
                 data_hash=excluded.data_hash,
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
                data_hash,
                k_cnt,
                raw_cnt,
                com_cnt,
                catalog_version,
                now_iso,
            ),
        )

    # ============ Session Projection Sync (catalog_sessions) ============

    def _sync_sessions_into_connection(
        self,
        con: sqlite3.Connection,
        cog_con: sqlite3.Connection,
        allowed: list[str],
        *,
        relations_resolver=None,
        force_full: bool = False,
        bulk_empty: bool = False,
    ) -> bool:
        """Project ``cognee_db.session_records`` + resolved relations into ``catalog_sessions``."""
        has_sr = cog_con.execute(
            "SELECT 1 FROM sqlite_master WHERE type='table' AND name='session_records' LIMIT 1"
        ).fetchone()
        if not has_sr:
            return False
        sr_cols = {str(r[1]) for r in cog_con.execute('PRAGMA table_info(session_records)').fetchall()}
        status_col = 'status' if 'status' in sr_cols else "''"
        agent_col = 'agent_id' if 'agent_id' in sr_cols else "''"
        started_col = 'started_at' if 'started_at' in sr_cols else "''"
        last_col = 'last_activity_at' if 'last_activity_at' in sr_cols else "''"

        existing_sess_sig: dict[str, tuple[int, str]] = {}
        if not force_full:
            for ds_id, scnt, ssig in con.execute(
                'SELECT dataset_id, session_count, session_sig FROM dataset_sync_state'
            ).fetchall():
                existing_sess_sig[str(ds_id)] = (int(scnt or 0), str(ssig or ''))

        # Single-pass summary across session_records for ALL allowed datasets (1 scan instead of N scans!)
        # On cold bulk_empty builds we read all rows once directly below, skipping the summary pre-scan!
        sr_ds_map: dict[str, str] = {}
        for ds in allowed:
            h = uuid_hex(ds)
            if h:
                sr_ds_map[h] = ds
            sr_ds_map[ds] = ds
        sr_summary: dict[str, list] = {ds: [0, 0, 0, '', 0] for ds in allowed}
        cold_rows_by_ds: dict[str, list[tuple]] | None = None
        if bulk_empty:
            cold_rows_by_ds = {ds: [] for ds in allowed}
            for r_ds, r_rid, r_sid, r_uid, r_aid, r_st, r_started, r_last in cog_con.execute(
                f'''SELECT dataset_id, rowid, session_id, user_id, {agent_col}, {status_col}, {started_col}, {last_col}
                    FROM session_records'''
            ):
                canon = sr_ds_map.get(str(r_ds))
                if canon is None:
                    continue
                acc = sr_summary[canon]
                acc[0] += 1
                rid_i = int(r_rid or 0)
                if rid_i > acc[1]:
                    acc[1] = rid_i
                acc[2] += rid_i
                l_v = str(r_last or '')
                if l_v > acc[3]:
                    acc[3] = l_v
                acc[4] += len(str(r_sid or '')) + len(str(r_st or '')) + len(l_v)
                cold_rows_by_ds[canon].append((r_sid, r_uid, r_aid, r_st, r_started, r_last))
        else:
            for r_ds, r_cnt, r_max, r_sum, r_last, r_len in cog_con.execute(
                f'''SELECT dataset_id, COUNT(*), COALESCE(MAX(rowid), 0), COALESCE(SUM(rowid), 0),
                           COALESCE(MAX({last_col}), ''),
                           COALESCE(SUM(length(coalesce(session_id,'')) + length(coalesce({status_col},'')) + length(coalesce({last_col},''))), 0)
                    FROM session_records
                    GROUP BY dataset_id'''
            ):
                canon = sr_ds_map.get(str(r_ds))
                if canon is None:
                    continue
                acc = sr_summary[canon]
                acc[0] += int(r_cnt or 0)
                m_v = int(r_max or 0)
                if m_v > acc[1]:
                    acc[1] = m_v
                acc[2] += int(r_sum or 0)
                l_v = str(r_last or '')
                if l_v > acc[3]:
                    acc[3] = l_v
                acc[4] += int(r_len or 0)

        any_mutated = False
        now_iso = _iso_now()
        for ds in allowed:
            ds_vars = list(dict.fromkeys(v for v in (uuid_hex(ds), ds) if v))
            if not ds_vars:
                continue
            ph = ','.join('?' for _ in ds_vars)
            s_rows_cnt, s_max_rid, s_sum_rid, s_max_last, s_len_sum = sr_summary[ds]

            rel_sig = '0'
            rel_resolver_fn = relations_resolver
            rel_hint = getattr(rel_resolver_fn, 'relation_sig', None) if rel_resolver_fn is not None else None
            if callable(rel_hint):
                try:
                    rel_sig = str(rel_hint(ds) or '0')
                except Exception:
                    rel_sig = 'err'
            elif rel_resolver_fn is not None:
                rel_sig = ''

            base_sig = f'{s_rows_cnt}|{s_max_rid}|{s_sum_rid}|{s_max_last}|{s_len_sum}'
            prev_entry = existing_sess_sig.get(ds)
            if not force_full and rel_sig and prev_entry is not None and prev_entry[1] == f'{base_sig}|{rel_sig}':
                continue

            raw_sessions = []
            sids_set: set[str] = set()
            row_iter = (
                cold_rows_by_ds[ds]
                if cold_rows_by_ds is not None
                else cog_con.execute(
                    f'''SELECT session_id, user_id, {agent_col}, {status_col}, {started_col}, {last_col}
                        FROM session_records
                        WHERE dataset_id IN ({ph})''',
                    ds_vars,
                )
            )
            for srow in row_iter:
                sid = str(srow[0] or '').strip()
                if not sid:
                    continue
                uid = uuid_text(srow[1]) or str(srow[1] or '').strip()
                aid = str(srow[2] or '')
                st = str(srow[3] or '')
                started_iso = iso_text(srow[4])
                last_iso = iso_text(srow[5])
                _, sort_ns, inv = normalize_utc_ns(last_iso)
                if inv:
                    _, sort_ns, _ = normalize_utc_ns(started_iso)
                raw_sessions.append((sid, uid, aid, st, started_iso, last_iso, -sort_ns))
                sids_set.add(sid)
            distinct_sids = len(sids_set)

            relations = {}
            if sids_set and rel_resolver_fn is not None:
                try:
                    relations = rel_resolver_fn(ds, list(sids_set)) or {}
                except Exception:
                    relations = {}

            if not rel_sig:
                rel_h = hashlib.blake2b(digest_size=8)
                for k_sid in sorted(relations):
                    r_val = relations[k_sid]
                    if isinstance(r_val, dict):
                        rel_h.update(
                            f"{k_sid}|{r_val.get('parent_session_id','')}|{r_val.get('subagent_role','')}|{r_val.get('nesting_depth',0)}\n".encode('utf-8')
                        )
                rel_sig = rel_h.hexdigest()

            full_sig = f'{base_sig}|{rel_sig}'
            if not force_full and prev_entry is not None and prev_entry[1] == full_sig:
                continue

            # Resolve parent/root/depth/is_top_level/child_count cleanly
            parent_of: dict[str, str] = {}
            role_of: dict[str, str] = {}
            depth_of: dict[str, int] = {}
            for sid in sids_set:
                rel = relations.get(sid)
                if not isinstance(rel, dict):
                    continue
                pid = str(rel.get('parent_session_id') or '').strip()
                if pid and pid != sid and pid in sids_set:
                    parent_of[sid] = pid
                role_of[sid] = str(rel.get('subagent_role') or '')
                try:
                    d_int = int(rel.get('nesting_depth') or 0)
                except (TypeError, ValueError):
                    d_int = 0
                if d_int <= 0 and sid in parent_of:
                    d_int = 1
                depth_of[sid] = max(0, d_int)

            # Compute root ancestor for every child session (with cycle protection)
            root_of: dict[str, str] = {}
            for sid, pid in parent_of.items():
                cur = pid
                visited = {sid}
                hops = 0
                while cur in parent_of and cur not in visited and hops < 16:
                    visited.add(cur)
                    nxt = parent_of[cur]
                    if nxt in visited:
                        break
                    cur = nxt
                    hops += 1
                root_of[sid] = cur

            # Count descendants per (owner_user_id, root_sid) and direct children per (owner_user_id, parent_sid),
            # as well as dataset-wide counts when a subagent has a distinct user_id.
            root_desc_counts: dict[tuple[str, str], int] = {}
            root_desc_total: dict[str, int] = {}
            direct_child_counts: dict[tuple[str, str], int] = {}
            direct_child_total: dict[str, int] = {}
            for sid, uid, _aid, _st, _started, _last, _skey in raw_sessions:
                pid = parent_of.get(sid, '')
                if pid:
                    rid = root_of.get(sid, pid)
                    root_desc_counts[(uid, rid)] = root_desc_counts.get((uid, rid), 0) + 1
                    root_desc_total[rid] = root_desc_total.get(rid, 0) + 1
                    direct_child_counts[(uid, pid)] = direct_child_counts.get((uid, pid), 0) + 1
                    direct_child_total[pid] = direct_child_total.get(pid, 0) + 1

            # Determine which sids are shared across >1 user_id in this dataset
            sid_user_counts: dict[str, int] = {}
            for sid, uid, *_ in raw_sessions:
                sid_user_counts[sid] = sid_user_counts.get(sid, 0) + 1

            to_insert = []
            for sid, uid, aid, st, started_iso, last_iso, skey in raw_sessions:
                pid = parent_of.get(sid, '')
                is_top = 0 if pid else 1
                rid = root_of.get(sid, '') if pid else ''
                role = role_of.get(sid, '')
                depth = depth_of.get(sid, 1 if pid else 0)
                if is_top:
                    if sid_user_counts.get(sid, 1) > 1:
                        c_cnt = root_desc_counts.get((uid, sid), 0)
                    else:
                        c_cnt = root_desc_total.get(sid, 0)
                else:
                    if sid_user_counts.get(sid, 1) > 1:
                        c_cnt = direct_child_counts.get((uid, sid), 0)
                    else:
                        c_cnt = direct_child_total.get(sid, 0)
                to_insert.append((
                    ds, sid, uid, aid, st, started_iso, last_iso,
                    skey, pid, rid, role, depth, is_top, c_cnt,
                ))

            if not bulk_empty:
                con.execute('DELETE FROM catalog_sessions WHERE dataset_id = ?', (ds,))
            if to_insert:
                if bulk_empty:
                    _bulk_insert_sessions(con, to_insert)
                else:
                    to_insert.sort(key=itemgetter(0, 2, 1))
                    con.executemany(
                        '''INSERT OR REPLACE INTO catalog_sessions (
                               dataset_id, session_id, user_id, agent_id, status, started_at,
                               last_activity_at, sort_ts_desc_key, parent_session_id, root_session_id,
                               subagent_role, nesting_depth, is_top_level, child_count
                           ) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)''',
                        to_insert,
                    )
            cur_state = con.execute(
                'SELECT 1 FROM dataset_sync_state WHERE dataset_id = ?',
                (ds,),
            ).fetchone()
            if cur_state is not None:
                con.execute(
                    'UPDATE dataset_sync_state SET session_count = ?, session_sig = ? WHERE dataset_id = ?',
                    (distinct_sids, full_sig, ds),
                )
            else:
                con.execute(
                    '''INSERT INTO dataset_sync_state
                       (dataset_id, dataset_hex, doc_count, max_rowid, sum_rowid, max_updated_at,
                        id_checksum, data_hash, knowledge_count, raw_doc_count, common_count,
                        session_count, session_sig, catalog_version, synced_at)
                       VALUES (?,?,0,0,0,'',0,'',0,0,0,?,?,'',?)''',
                    (ds, uuid_hex(ds), distinct_sids, full_sig, now_iso),
                )
            any_mutated = True
        return any_mutated

    # ============ Core Sync Engine ============

    def _sync_into_connection(
        self,
        con: sqlite3.Connection,
        managed_datasets: list[str],
        *,
        force_full: bool = False,
        relations_resolver=None,
    ) -> tuple[int, bool]:
        """Synchronize ``catalog_docs``, ``catalog_day_buckets``, ``catalog_sessions``, and ``dataset_sync_state``.

        Returns ``(generation, stat_cacheable)``. ``stat_cacheable`` is False if a transient
        read error occurred on provenance or recall so the caller will retry on next sync.
        """
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

        existing_states: dict[str, tuple[int, int, int, str, str]] = {}
        if not force_full:
            for row in con.execute(
                'SELECT dataset_id, doc_count, max_rowid, sum_rowid, max_updated_at, data_hash FROM dataset_sync_state'
            ).fetchall():
                existing_states[str(row[0])] = (
                    int(row[1]),
                    int(row[2]),
                    int(row[3]),
                    str(row[4] or ''),
                    str(row[5] or ''),
                )

        live_fingerprints: dict[str, tuple[int, int, int, str, str]] = {ds: (0, 0, 0, '', '') for ds in allowed}
        query_variants = sorted(ds_hex_to_uuid.keys())
        prov_stat_now = _file_stat_sig(self.provenance_path) if self.provenance_path else None
        recall_stat_now = _file_stat_sig(self.recall_path) if self.recall_path else None
        is_cold_build = force_full or not existing_states
        stream_cold_bulk = bool(
            is_cold_build and con.execute('SELECT 1 FROM catalog_docs LIMIT 1').fetchone() is None
        )
        stream_cold_sessions = bool(
            stream_cold_bulk and con.execute('SELECT 1 FROM catalog_sessions LIMIT 1').fetchone() is None
        )
        bulk_pragmas_set = False

        prov_ok = True
        recall_ok = True
        new_chunk_digests: dict[str, dict[int, bytes]] = {}
        dataset_diffs: dict[str, tuple[list, list]] = {}
        incremental_bucket_deltas: dict[str, tuple[dict[str, int], int, int, int]] = {}
        cold_bucket_summaries: dict[str, tuple[dict[str, int], int, int, int, dict[str, int]]] = {}
        proven_pure_append_ds: set[str] = set()
        removed_datasets = [ds for ds in existing_states if ds not in allowed_set]
        changed_datasets: list[str] = []
        changed_buckets_by_ds: dict[str, list[int]] = {}
        removed_buckets_by_ds: dict[str, list[int]] = {}

        cog_con = self._get_writer_ro(cognee_db)
        cog_in_tx = False
        try:
            cog_snap_sig = _file_stat_sig(cognee_db)
            cog_con.execute('BEGIN DEFERRED')
            cog_in_tx = True

            data_cols = {str(r[1]) for r in cog_con.execute('PRAGMA table_info(data)').fetchall()}
            col_exprs = {
                'id': 'id' if 'id' in data_cols else 'NULL',
                'name': 'name' if 'name' in data_cols else 'NULL',
                'label': 'label' if 'label' in data_cols else 'NULL',
                'raw_data_location': 'raw_data_location' if 'raw_data_location' in data_cols else 'NULL',
                'mime_type': 'mime_type' if 'mime_type' in data_cols else 'NULL',
                'system_metadata': 'system_metadata' if 'system_metadata' in data_cols else 'NULL',
                'external_metadata': 'external_metadata' if 'external_metadata' in data_cols else 'NULL',
                'node_set': 'node_set' if 'node_set' in data_cols else 'NULL',
                'pipeline_status': 'pipeline_status' if 'pipeline_status' in data_cols else 'NULL',
                'updated_at': 'updated_at' if 'updated_at' in data_cols else 'NULL',
                'created_at': 'created_at' if 'created_at' in data_cols else 'NULL',
                'content_hash': 'content_hash' if 'content_hash' in data_cols else 'NULL',
            }
            select_full_cols = (
                f"{col_exprs['id']}, rowid, {col_exprs['name']}, {col_exprs['label']}, "
                f"{col_exprs['raw_data_location']}, {col_exprs['system_metadata']}, "
                f"{col_exprs['external_metadata']}, {col_exprs['node_set']}, {col_exprs['pipeline_status']}, "
                f"{col_exprs['updated_at']}, {col_exprs['created_at']}, {col_exprs['content_hash']}, "
                f"{col_exprs['mime_type']}"
            )

            # Stage 1 (O(log N) MIN/MAX rowid bounds) + 6-worker cryptographic chunk digest scan.
            # If a concurrent WAL write landed between cog_con's snapshot and worker pinning,
            # retry pinning cog_con cleanly (<0.2ms) instead of falling back to single-threaded scan.
            for _snap_attempt in range(3):
                if is_cold_build:
                    changed_datasets = list(allowed)
                    break

                changed_datasets = []
                changed_buckets_by_ds = {}
                removed_buckets_by_ds = {}
                new_chunk_digests = {}
                live_fingerprints = {ds: (0, 0, 0, '', '') for ds in allowed}
                snap_stale = False
                for ds in allowed:
                    prev_fp = existing_states.get(ds)
                    ds_vars = list(dict.fromkeys(v for v in (uuid_hex(ds), ds) if v))
                    s1_min_rid = 0
                    s1_max_rid = 0
                    for v in ds_vars:
                        r_min = cog_con.execute(
                            'SELECT rowid FROM data WHERE dataset_id = ? ORDER BY rowid ASC LIMIT 1',
                            (v,),
                        ).fetchone()
                        if r_min:
                            rv = int(r_min[0] or 0)
                            if rv > 0 and (s1_min_rid == 0 or rv < s1_min_rid):
                                s1_min_rid = rv
                        r_max = cog_con.execute(
                            'SELECT rowid FROM data WHERE dataset_id = ? ORDER BY rowid DESC LIMIT 1',
                            (v,),
                        ).fetchone()
                        if r_max:
                            rv = int(r_max[0] or 0)
                            if rv > s1_max_rid:
                                s1_max_rid = rv

                    if s1_max_rid == 0:
                        live_fingerprints[ds] = (0, 0, 0, '', _fold_chunk_digests({}))
                        new_chunk_digests[ds] = {}
                        if prev_fp is None or prev_fp[0] != 0 or prev_fp[4] != live_fingerprints[ds][4]:
                            changed_datasets.append(ds)
                            old_b = self._chunk_digests.get(ds)
                            if old_b:
                                removed_buckets_by_ds[ds] = sorted(old_b.keys())
                        continue

                    if prev_fp is None:
                        changed_datasets.append(ds)
                        continue

                    # Full-13-column cryptographic chunk digest scan (zero undetected mutations!)
                    scan_res = self._scan_dataset_chunks(
                        cog_con,
                        cognee_db,
                        ds_vars,
                        col_exprs,
                        None,
                        s1_min_rid,
                        s1_max_rid,
                        None,
                        cog_snap_sig=cog_snap_sig,
                        est_cnt=prev_fp[0],
                        allow_stale_signal=(_snap_attempt < 2),
                    )
                    if scan_res is None:
                        snap_stale = True
                        break
                    chunk_map, c_cnt, c_max_rid, c_sum_rid, c_max_upd = scan_res
                    data_hash = _fold_chunk_digests(chunk_map)
                    live_fingerprints[ds] = (c_cnt, c_max_rid, c_sum_rid, c_max_upd, data_hash)
                    new_chunk_digests[ds] = chunk_map

                    if prev_fp == live_fingerprints[ds]:
                        continue

                    changed_datasets.append(ds)
                    old_chunks = self._chunk_digests.get(ds)
                    if old_chunks is not None:
                        diff_bkts = [b for b, dig in chunk_map.items() if old_chunks.get(b) != dig]
                        del_bkts = [b for b in old_chunks if b not in chunk_map]
                        changed_buckets_by_ds[ds] = sorted(diff_bkts)
                        removed_buckets_by_ds[ds] = sorted(del_bkts)

                if snap_stale:
                    cog_con.execute('ROLLBACK')
                    cog_snap_sig = _file_stat_sig(cognee_db)
                    cog_con.execute('BEGIN DEFERRED')
                    cog_con.execute('SELECT name FROM sqlite_master LIMIT 1').fetchone()
                    continue
                break

            # Load provenance & recall deltas using single-snapshot readers (never swallow failures!)
            if is_cold_build:
                prov_changes, new_prov_seq, need_full_prov = [], last_prov_seq, True
                doc_prov, key_prov, full_prov_seq, prov_ok = self._load_provenance_maps(allowed)
                if prov_ok and full_prov_seq > new_prov_seq:
                    new_prov_seq = full_prov_seq
                recall_changes, new_recall_rev, need_full_recall = [], last_recall_rev, True
                recall_counts, full_recall_rev, recall_ok = self._load_recall_counts(allowed)
                if recall_ok and full_recall_rev > new_recall_rev:
                    new_recall_rev = full_recall_rev
            else:
                if self._last_prov_stat is not None and prov_stat_now == self._last_prov_stat:
                    prov_changes, new_prov_seq, prov_needs_full, prov_ok = [], last_prov_seq, False, True
                else:
                    prov_changes, new_prov_seq, prov_needs_full, prov_ok = self._load_provenance_delta(
                        last_prov_seq, allowed_set
                    )
                need_full_prov = prov_needs_full and prov_ok
                if need_full_prov:
                    doc_prov, key_prov, full_prov_seq, prov_ok = self._load_provenance_maps(allowed)
                    if prov_ok and full_prov_seq > new_prov_seq:
                        new_prov_seq = full_prov_seq
                else:
                    doc_prov, key_prov = {}, {}

                if self._last_recall_stat is not None and recall_stat_now == self._last_recall_stat:
                    recall_changes, new_recall_rev, recall_needs_full, recall_ok = [], last_recall_rev, False, True
                else:
                    recall_changes, new_recall_rev, recall_needs_full, recall_ok = self._load_recall_delta(
                        last_recall_rev, allowed_set
                    )
                need_full_recall = recall_needs_full and recall_ok
                if need_full_recall:
                    recall_counts, full_recall_rev, recall_ok = self._load_recall_counts(allowed)
                    if recall_ok and full_recall_rev > new_recall_rev:
                        new_recall_rev = full_recall_rev
                else:
                    recall_counts = {}

            loaded_full_prov_for_changed = need_full_prov
            loaded_full_recall_for_changed = need_full_recall

            def _ensure_changed_maps(
                target_datasets: list[str],
                specific_doc_ids: list[str] | None = None,
                specific_lesson_keys: set[tuple[str, str, str]] | None = None,
            ):
                nonlocal doc_prov, key_prov, new_prov_seq, recall_counts, new_recall_rev
                nonlocal loaded_full_prov_for_changed, loaded_full_recall_for_changed, prov_ok, recall_ok
                if specific_doc_ids is not None and len(specific_doc_ids) <= 2048 and not loaded_full_prov_for_changed:
                    if self.provenance_path and self.provenance_path.is_file() and not self.provenance_path.is_symlink():
                        try:
                            pcon = self._get_writer_ro(self.provenance_path)
                            with ro_snapshot(pcon):
                                ph_ds = ','.join('?' for _ in target_datasets)
                                for start in range(0, len(specific_doc_ids), _MAX_IN_BATCH):
                                    d_batch = specific_doc_ids[start : start + _MAX_IN_BATCH]
                                    ph_d = ','.join('?' for _ in d_batch)
                                    for r_ds, r_doc, r_lt, r_sk, r_fd in pcon.execute(
                                        f'''SELECT dataset_id, document_id, lesson_type, state_key, first_source_date
                                            FROM document_provenance
                                            WHERE dataset_id IN ({ph_ds}) AND document_id IN ({ph_d})''',
                                        (*target_datasets, *d_batch),
                                    ):
                                        lt_s = str(r_lt or '').strip()
                                        sk_s = str(r_sk or '').strip()
                                        doc_prov[(str(r_ds), str(r_doc))] = (
                                            lt_s,
                                            sk_s,
                                            str(r_fd or '').strip(),
                                        )
                                        if specific_lesson_keys is not None and lt_s and sk_s:
                                            specific_lesson_keys.add((str(r_ds), lt_s, sk_s))
                                if specific_lesson_keys is not None:
                                    keys_by_ds: dict[str, set[str]] = {}
                                    for k_ds, _k_lt, k_sk in specific_lesson_keys:
                                        keys_by_ds.setdefault(k_ds, set()).add(k_sk)
                                    for k_ds, sk_set in keys_by_ds.items():
                                        sk_list = sorted(sk_set)
                                        for start in range(0, len(sk_list), _MAX_IN_BATCH):
                                            sk_batch = sk_list[start : start + _MAX_IN_BATCH]
                                            ph_sk = ','.join('?' for _ in sk_batch)
                                            for r_lt, r_sk, r_fd in pcon.execute(
                                                f'''SELECT lesson_type, state_key, first_source_date
                                                    FROM keyed_provenance
                                                    WHERE dataset_id = ? AND state_key IN ({ph_sk})''',
                                                (k_ds, *sk_batch),
                                            ):
                                                if r_fd:
                                                    key_prov[(k_ds, str(r_lt or '').strip(), str(r_sk or '').strip())] = str(r_fd).strip()
                                else:
                                    for r_ds, r_lt, r_sk, r_fd in pcon.execute(
                                        f'''SELECT dataset_id, lesson_type, state_key, first_source_date
                                            FROM keyed_provenance
                                            WHERE dataset_id IN ({ph_ds})''',
                                        target_datasets,
                                    ):
                                        key_prov[(str(r_ds), str(r_lt or '').strip(), str(r_sk or '').strip())] = str(r_fd or '').strip()
                        except (PolicyError, sqlite3.Error):
                            prov_ok = False
                elif not loaded_full_prov_for_changed:
                    dp, kp, fseq, ok = self._load_provenance_maps(allowed)
                    if ok:
                        doc_prov.update(dp)
                        key_prov.update(kp)
                        if fseq > new_prov_seq:
                            new_prov_seq = fseq
                        loaded_full_prov_for_changed = True
                    else:
                        prov_ok = False

                if specific_doc_ids is not None and len(specific_doc_ids) <= 2048 and not loaded_full_recall_for_changed:
                    if self.recall_path and self.recall_path.is_file() and not self.recall_path.is_symlink():
                        try:
                            rcon = self._get_writer_ro(self.recall_path)
                            with ro_snapshot(rcon):
                                has_mat = rcon.execute(
                                    "SELECT 1 FROM sqlite_master WHERE type='table' AND name='recall_doc_counts' LIMIT 1"
                                ).fetchone()
                                if has_mat:
                                    ph_ds = ','.join('?' for _ in target_datasets)
                                    for start in range(0, len(specific_doc_ids), _MAX_IN_BATCH):
                                        d_batch = specific_doc_ids[start : start + _MAX_IN_BATCH]
                                        ph_d = ','.join('?' for _ in d_batch)
                                        for r_ds, r_doc, r_hits in rcon.execute(
                                            f'''SELECT dataset_id, document_id, total_hits
                                                FROM recall_doc_counts
                                                WHERE dataset_id IN ({ph_ds}) AND document_id IN ({ph_d})''',
                                            (*target_datasets, *d_batch),
                                        ):
                                            recall_counts[(str(r_ds), str(r_doc))] = int(r_hits or 0)
                        except (PolicyError, sqlite3.Error):
                            recall_ok = False
                elif not loaded_full_recall_for_changed:
                    rc, frev, ok = self._load_recall_counts(allowed)
                    if ok:
                        recall_counts.update(rc)
                        if frev > new_recall_rev:
                            new_recall_rev = frev
                        loaded_full_recall_for_changed = True
                    else:
                        recall_ok = False

            # Reconcile each changed dataset
            # Fast single-pass streaming cold bulk build across all allowed datasets at once
            if stream_cold_bulk and changed_datasets:
                _ensure_changed_maps(changed_datasets)
                con.execute('PRAGMA synchronous = OFF')
                con.execute('PRAGMA cache_size = -4096')
                con.execute('PRAGMA threads = 4')
                bulk_pragmas_set = True
                con.execute('BEGIN IMMEDIATE')
                for idx_name in _CATALOG_INDEX_NAMES:
                    con.execute(f'DROP INDEX IF EXISTS {idx_name}')
                if stream_cold_sessions:
                    for sidx_name in _CATALOG_SESSION_INDEX_NAMES:
                        con.execute(f'DROP INDEX IF EXISTS {sidx_name}')

                cold_stats: dict[str, list] = {
                    ds: [0, 0, 0, 0, '', {}, {}, 0, 0, 0]
                    for ds in changed_datasets
                }
                upserts: list[tuple] = []
                if query_variants and len(query_variants) <= _MAX_IN_BATCH:
                    ph_all = ','.join('?' for _ in query_variants)
                    cold_cursor = cog_con.execute(
                        f'SELECT dataset_id, {select_full_cols} FROM data WHERE dataset_id IN ({ph_all})',
                        query_variants,
                    )
                else:
                    cold_cursor = cog_con.execute(f'SELECT dataset_id, {select_full_cols} FROM data')
                for row_with_ds in cold_cursor:
                    canon_ds = ds_hex_to_uuid.get(str(row_with_ds[0]))
                    if canon_ds is None:
                        continue
                    st = cold_stats.get(canon_ds)
                    if st is None:
                        continue
                    full_row = row_with_ds[1:]
                    rid_val = int(full_row[1] or 0)
                    st[0] += 1
                    if st[1] == 0 or (rid_val > 0 and rid_val < st[1]):
                        st[1] = rid_val
                    if rid_val > st[2]:
                        st[2] = rid_val
                    st[3] += rid_val
                    upd_raw = full_row[9]
                    if isinstance(upd_raw, str) and upd_raw and '0' <= upd_raw[0] <= '9' and upd_raw > st[4]:
                        st[4] = upd_raw

                    projected = self._project_cognee_row(
                        full_row,
                        canon_ds,
                        doc_prov=doc_prov,
                        key_prov=key_prov,
                        recall_counts=recall_counts,
                        tz_info=tz_info,
                    )
                    if projected is not None:
                        upserts.append(projected)
                        p_kind = projected[3]
                        if projected[4]:
                            st[7] += 1
                            p_day = projected[18]
                            if p_day:
                                c_days = st[5]
                                c_days[p_day] = c_days.get(p_day, 0) + 1
                            p_cat = projected[8]
                            if p_cat:
                                c_cats = st[6]
                                c_cats[p_cat] = c_cats.get(p_cat, 0) + 1
                        elif p_kind == 'raw_doc':
                            st[8] += 1
                        if p_kind == 'common':
                            st[9] += 1
                        if len(upserts) >= 2000:
                            _bulk_insert_docs(con, upserts)
                            upserts.clear()
                if upserts:
                    _bulk_insert_docs(con, upserts)
                    upserts.clear()

                for ds in changed_datasets:
                    (
                        c_cnt, c_min_rid, c_max_rid, c_sum_rid, c_max_upd,
                        cold_days, cold_cats, cold_k, cold_raw, cold_com,
                    ) = cold_stats[ds]
                    ds_vars = list(dict.fromkeys(v for v in (uuid_hex(ds), ds) if v))
                    bkt_digests, _, _, _, _ = self._scan_dataset_chunks(
                        cog_con, cognee_db, ds_vars, col_exprs, c_cnt, c_min_rid, c_max_rid, c_sum_rid,
                    )
                    data_hash = _fold_chunk_digests(bkt_digests)
                    live_fingerprints[ds] = (c_cnt, c_max_rid, c_sum_rid, c_max_upd, data_hash)
                    new_chunk_digests[ds] = bkt_digests
                    cold_bucket_summaries[ds] = (cold_days, cold_k, cold_raw, cold_com, cold_cats)
                    dataset_diffs[ds] = ([], [])
            else:
                for ds in changed_datasets:
                    ds_vars = list(dict.fromkeys(v for v in (uuid_hex(ds), ds) if v))
                    ph = ','.join('?' for _ in ds_vars)
                    ds_filter = '+dataset_id = ?' if len(ds_vars) == 1 else f'+dataset_id IN ({ph})'

                    # Chunk-localized incremental path (cryptographically proven exact affected buckets!)
                    if not force_full and ds in changed_buckets_by_ds and len(changed_buckets_by_ds[ds]) <= 8:
                        diff_bkts = changed_buckets_by_ds[ds]
                        del_bkts = removed_buckets_by_ds.get(ds, [])
                        all_touched_bkts = sorted(set(diff_bkts) | set(del_bkts))
                        if not all_touched_bkts:
                            continue

                        # Fetch ONLY live rows inside the changed buckets (<= 8 * 1024 rows)
                        live_rows_in_bkts: list[tuple] = []
                        for bkt in diff_bkts:
                            lo = bkt << _CHUNK_SHIFT
                            hi = lo + _CHUNK_SPAN - 1
                            live_rows_in_bkts.extend(
                                cog_con.execute(
                                    f'SELECT {select_full_cols} FROM data WHERE rowid BETWEEN ? AND ? AND {ds_filter} ORDER BY rowid ASC',
                                    (lo, hi, *ds_vars),
                                ).fetchall()
                            )

                        # Fetch existing catalog rows inside ALL touched buckets (for exact diff + bucket deltas)
                        existing_in_bkts: dict[str, tuple] = {}
                        for bkt in all_touched_bkts:
                            lo = bkt << _CHUNK_SHIFT
                            hi = lo + _CHUNK_SPAN - 1
                            for crow in con.execute(
                                '''SELECT dataset_id, doc_id, cognee_rowid, kind, is_knowledge,
                                          common_category, lesson_type, state_key, category, session_id,
                                          doc_name, doc_label, raw_data_location, content_hash,
                                          created_at_raw, created_at_ns, created_at_invalid, updated_at_ns,
                                          effective_date, effective_date_source, day_desc_key, sort_ts_desc_key,
                                          recall_count, recall_desc_key, pipeline_status, source_updated_at
                                   FROM catalog_docs
                                   WHERE dataset_id = ? AND cognee_rowid BETWEEN ? AND ?''',
                                (ds, lo, hi),
                            ).fetchall():
                                existing_in_bkts[str(crow[1])] = crow

                        live_doc_ids = []
                        specific_keys: set[tuple[str, str, str]] = set()
                        for r in live_rows_in_bkts:
                            did_c = uuid_text(r[0])
                            if not did_c:
                                continue
                            live_doc_ids.append(did_c)
                            old_c = existing_in_bkts.get(did_c)
                            if old_c is None:
                                m_c = extract_trusted_metadata(r[5], r[6], r[7])
                                lt_c = str(m_c.get('lesson_type') or '').strip()
                                sk_c = str(m_c.get('state_key') or '').strip()
                                if lt_c and sk_c:
                                    specific_keys.add((ds, lt_c, sk_c))
                            else:
                                if old_c[6] and old_c[7]:
                                    specific_keys.add((ds, str(old_c[6]), str(old_c[7])))
                        _ensure_changed_maps(
                            [ds],
                            specific_doc_ids=live_doc_ids if len(live_doc_ids) <= 2048 else None,
                            specific_lesson_keys=specific_keys if len(live_doc_ids) <= 2048 else None,
                        )

                        upserts = []
                        deleted_ids = []
                        seen_live_dids: set[str] = set()
                        day_delta: dict[str, int] = {}
                        dk = dr = dc = 0
                        all_strictly_new_appends = True
                        prev_fp = existing_states.get(ds)
                        old_max_rid = prev_fp[1] if prev_fp is not None else 0

                        for full_row in live_rows_in_bkts:
                            did = uuid_text(full_row[0])
                            if not did:
                                continue
                            seen_live_dids.add(did)
                            old_row = existing_in_bkts.get(did)
                            projected = self._project_cognee_row(
                                full_row,
                                ds,
                                doc_prov=doc_prov,
                                key_prov=key_prov,
                                recall_counts=recall_counts,
                                tz_info=tz_info,
                            )
                            if old_row is None:
                                if projected is None:
                                    continue
                                # Check if this doc already exists in another bucket (e.g. rowid moved!)
                                if int(full_row[1] or 0) <= old_max_rid:
                                    all_strictly_new_appends = False
                                else:
                                    moved_old = con.execute(
                                        'SELECT kind, is_knowledge, effective_date FROM catalog_docs WHERE dataset_id = ? AND doc_id = ?',
                                        (ds, did),
                                    ).fetchone()
                                    if moved_old is not None:
                                        all_strictly_new_appends = False
                                        if int(moved_old[1]):
                                            dk -= 1
                                            if moved_old[2]:
                                                day_delta[str(moved_old[2])] = day_delta.get(str(moved_old[2]), 0) - 1
                                        if moved_old[0] == 'raw_doc':
                                            dr -= 1
                                        if moved_old[0] == 'common':
                                            dc -= 1
                                upserts.append(projected)
                                if projected[4]:
                                    dk += 1
                                    if projected[18]:
                                        day_delta[projected[18]] = day_delta.get(projected[18], 0) + 1
                                if projected[3] == 'raw_doc':
                                    dr += 1
                                if projected[3] == 'common':
                                    dc += 1
                            else:
                                # Preserve existing recall_count if recall_counts map didn't load this doc
                                if projected is not None and (ds, did) not in recall_counts and int(old_row[22]) > 0:
                                    rc_old = int(old_row[22])
                                    projected = projected[:22] + (rc_old, -rc_old) + projected[24:]
                                if projected == old_row:
                                    continue
                                all_strictly_new_appends = False
                                # Subtract old row contribution
                                if int(old_row[4]):
                                    dk -= 1
                                    if old_row[18]:
                                        day_delta[str(old_row[18])] = day_delta.get(str(old_row[18]), 0) - 1
                                if old_row[3] == 'raw_doc':
                                    dr -= 1
                                if old_row[3] == 'common':
                                    dc -= 1
                                if projected is not None:
                                    upserts.append(projected)
                                    if projected[4]:
                                        dk += 1
                                        if projected[18]:
                                            day_delta[projected[18]] = day_delta.get(projected[18], 0) + 1
                                    if projected[3] == 'raw_doc':
                                        dr += 1
                                    if projected[3] == 'common':
                                        dc += 1
                                else:
                                    deleted_ids.append(did)

                        for old_did, old_row in existing_in_bkts.items():
                            if old_did not in seen_live_dids:
                                all_strictly_new_appends = False
                                deleted_ids.append(old_did)
                                if int(old_row[4]):
                                    dk -= 1
                                    if old_row[18]:
                                        day_delta[str(old_row[18])] = day_delta.get(str(old_row[18]), 0) - 1
                                if old_row[3] == 'raw_doc':
                                    dr -= 1
                                if old_row[3] == 'common':
                                    dc -= 1

                        dataset_diffs[ds] = (upserts, deleted_ids)
                        incremental_bucket_deltas[ds] = (day_delta, dk, dr, dc)
                        if all_strictly_new_appends and upserts and not deleted_ids:
                            proven_pure_append_ds.add(ds)
                        continue

                    # Full dataset reconciliation (force_full on non-empty catalog, post-restart diff, or >8 changed chunks)
                    _ensure_changed_maps(changed_datasets)

                    if force_full or ds not in existing_states:
                        upserts = []
                        c_cnt = c_min_rid = c_max_rid = c_sum_rid = 0
                        c_max_upd = ''
                        cold_days: dict[str, int] = {}
                        cold_cats: dict[str, int] = {}
                        cold_k = cold_raw = cold_com = 0
                        for full_row in cog_con.execute(
                            f'SELECT {select_full_cols} FROM data WHERE {ds_filter} ORDER BY rowid ASC',
                            ds_vars,
                        ):
                            rid_val = int(full_row[1] or 0)
                            c_cnt += 1
                            if c_min_rid == 0 or (rid_val > 0 and rid_val < c_min_rid):
                                c_min_rid = rid_val
                            if rid_val > c_max_rid:
                                c_max_rid = rid_val
                            c_sum_rid += rid_val
                            upd_raw = full_row[9]
                            if isinstance(upd_raw, str) and upd_raw and '0' <= upd_raw[0] <= '9' and upd_raw > c_max_upd:
                                c_max_upd = upd_raw

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
                        bkt_digests, _, _, _, _ = self._scan_dataset_chunks(
                            cog_con, cognee_db, ds_vars, col_exprs, c_cnt, c_min_rid, c_max_rid, c_sum_rid,
                        )
                        data_hash = _fold_chunk_digests(bkt_digests)
                        live_fingerprints[ds] = (c_cnt, c_max_rid, c_sum_rid, c_max_upd, data_hash)
                        new_chunk_digests[ds] = bkt_digests
                        cold_bucket_summaries[ds] = (cold_days, cold_k, cold_raw, cold_com, cold_cats)
                        dataset_diffs[ds] = (upserts, [])
                        continue

                    # Full diff against existing catalog_docs (post-restart or bulk change)
                    existing_rows: dict[str, tuple] = {}
                    for crow in con.execute(
                        '''SELECT dataset_id, doc_id, cognee_rowid, kind, is_knowledge,
                                  common_category, lesson_type, state_key, category, session_id,
                                  doc_name, doc_label, raw_data_location, content_hash,
                                  created_at_raw, created_at_ns, created_at_invalid, updated_at_ns,
                                  effective_date, effective_date_source, day_desc_key, sort_ts_desc_key,
                                  recall_count, recall_desc_key, pipeline_status, source_updated_at
                           FROM catalog_docs WHERE dataset_id = ?''',
                        (ds,),
                    ):
                        existing_rows[str(crow[1])] = crow

                    upserts = []
                    deleted_ids = []
                    for full_row in cog_con.execute(
                        f'SELECT {select_full_cols} FROM data WHERE {ds_filter} ORDER BY rowid ASC',
                        ds_vars,
                    ):
                        did = uuid_text(full_row[0])
                        if not did:
                            continue
                        old_row = existing_rows.pop(did, None)
                        projected = self._project_cognee_row(
                            full_row,
                            ds,
                            doc_prov=doc_prov,
                            key_prov=key_prov,
                            recall_counts=recall_counts,
                            tz_info=tz_info,
                        )
                        if projected is not None:
                            if old_row is None or projected != old_row:
                                upserts.append(projected)
                        elif old_row is not None:
                            deleted_ids.append(did)
                    deleted_ids.extend(existing_rows.keys())
                    existing_rows.clear()
                    dataset_diffs[ds] = (upserts, deleted_ids)

            # Begin atomic write transaction on catalog-index.sqlite3
            dirty_datasets = set(changed_datasets)
            now_iso = _iso_now()
            total_upserts = sum(len(u) for u, _ in dataset_diffs.values())
            use_bulk_index_build = bool(stream_cold_bulk and con.in_transaction)
            if not con.in_transaction:
                con.execute('BEGIN IMMEDIATE')
            try:
                if (
                    not use_bulk_index_build
                    and total_upserts >= 5000
                    and con.execute('SELECT 1 FROM catalog_docs LIMIT 1').fetchone() is None
                ):
                    use_bulk_index_build = True
                    for idx_name in _CATALOG_INDEX_NAMES:
                        con.execute(f'DROP INDEX IF EXISTS {idx_name}')

                for ds in removed_datasets:
                    con.execute('DELETE FROM catalog_docs WHERE dataset_id = ?', (ds,))
                    con.execute('DELETE FROM catalog_day_buckets WHERE dataset_id = ?', (ds,))
                    con.execute('DELETE FROM catalog_sessions WHERE dataset_id = ?', (ds,))
                    con.execute('DELETE FROM dataset_sync_state WHERE dataset_id = ?', (ds,))

                for ds, (upserts, deleted_ids) in dataset_diffs.items():
                    if force_full and not stream_cold_bulk:
                        # Explicitly purge any stale/deleted source rows on force_full!
                        con.execute('DELETE FROM catalog_docs WHERE dataset_id = ?', (ds,))
                    elif deleted_ids:
                        for start in range(0, len(deleted_ids), _MAX_IN_BATCH):
                            batch = deleted_ids[start : start + _MAX_IN_BATCH]
                            ph = ','.join('?' for _ in batch)
                            con.execute(
                                f'DELETE FROM catalog_docs WHERE dataset_id = ? AND doc_id IN ({ph})',
                                (ds, *batch),
                            )
                    if upserts:
                        if use_bulk_index_build or (force_full and not stream_cold_bulk):
                            _bulk_insert_docs(con, upserts)
                        else:
                            con.executemany(_UPSERT_DOC_SQL, upserts)

                if use_bulk_index_build:
                    for stmt in _CATALOG_INDEX_DDL.strip().split(';'):
                        s = stmt.strip()
                        if s:
                            con.execute(s)

                # Apply incremental provenance date updates to ALL affected docs (including changed_datasets!)
                # AND propagate earlier dates to sibling docs sharing (dataset_id, lesson_type, state_key).
                prov_mutated_datasets: set[str] = set()
                for ds, doc_id, lt, sk, fdate in prov_changes:
                    valid_day = _calendar_date(fdate)
                    if not valid_day:
                        continue
                    row = con.execute(
                        'SELECT effective_date, effective_date_source, lesson_type, state_key FROM catalog_docs WHERE dataset_id = ? AND doc_id = ?',
                        (ds, doc_id),
                    ).fetchone()
                    eff_lt = lt
                    eff_sk = sk
                    if row is not None:
                        cur_day, cur_src = str(row[0] or ''), str(row[1] or '')
                        if not eff_lt and row[2]:
                            eff_lt = str(row[2]).strip()
                        if not eff_sk and row[3]:
                            eff_sk = str(row[3]).strip()
                        if cur_src != 'provenance' or not cur_day or valid_day < cur_day:
                            con.execute(
                                '''UPDATE catalog_docs
                                   SET effective_date = ?, effective_date_source = 'provenance', day_desc_key = ?
                                   WHERE dataset_id = ? AND doc_id = ?''',
                                (valid_day, day_to_desc_key(valid_day), ds, doc_id),
                            )
                            dirty_datasets.add(ds)
                            prov_mutated_datasets.add(ds)
                    if eff_lt and eff_sk:
                        sib_updates = []
                        for sib_id, s_day, s_src in con.execute(
                            '''SELECT doc_id, effective_date, effective_date_source
                               FROM catalog_docs
                               WHERE dataset_id = ? AND is_knowledge = 1 AND lesson_type = ? AND state_key = ?''',
                            (ds, eff_lt, eff_sk),
                        ).fetchall():
                            s_day_s = str(s_day or '')
                            s_src_s = str(s_src or '')
                            if s_src_s != 'provenance' or not s_day_s or valid_day < s_day_s:
                                sib_updates.append((valid_day, day_to_desc_key(valid_day), ds, str(sib_id)))
                        if sib_updates:
                            con.executemany(
                                '''UPDATE catalog_docs
                                   SET effective_date = ?, effective_date_source = 'provenance', day_desc_key = ?
                                   WHERE dataset_id = ? AND doc_id = ?''',
                                sib_updates,
                            )
                            dirty_datasets.add(ds)
                            prov_mutated_datasets.add(ds)

                for pds in prov_mutated_datasets:
                    incremental_bucket_deltas.pop(pds, None)
                    cold_bucket_summaries.pop(pds, None)
                    proven_pure_append_ds.discard(pds)

                # Sync recall counts: O(delta) via recall_doc_counts.rev when possible, else full diff
                if need_full_recall and recall_ok:
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
                elif recall_changes and recall_ok:
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

                # Timezone change: recompute effective_date ONLY for created_at-fallback docs (never overwrite provenance or meta!)
                if tz_changed and not force_full:
                    for ds in allowed:
                        tz_updates = []
                        for doc_id, created_ns, cur_day in con.execute(
                            '''SELECT doc_id, created_at_ns, effective_date
                               FROM catalog_docs
                               WHERE dataset_id = ? AND is_knowledge = 1
                                 AND created_at_invalid = 0
                                 AND effective_date_source = 'created_at' ''',
                            (ds,),
                        ).fetchall():
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
                            incremental_bucket_deltas.pop(ds, None)
                            cold_bucket_summaries.pop(ds, None)
                            proven_pure_append_ds.discard(ds)

                # Project sessions into catalog_sessions inside the same pinned cog_con WAL snapshot
                sessions_mutated = self._sync_sessions_into_connection(
                    con,
                    cog_con,
                    allowed,
                    relations_resolver=relations_resolver,
                    force_full=force_full,
                    bulk_empty=bool(stream_cold_sessions and use_bulk_index_build),
                )
                if stream_cold_sessions and use_bulk_index_build:
                    for stmt in _CATALOG_SESSION_INDEX_DDL.strip().split(';'):
                        s = stmt.strip()
                        if s:
                            con.execute(s)

                mutated = bool(
                    dirty_datasets
                    or removed_datasets
                    or tz_changed
                    or (prov_ok and new_prov_seq != last_prov_seq)
                    or (recall_ok and new_recall_rev != last_recall_rev)
                    or sessions_mutated
                    or force_full
                )
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
                        doc_count, max_rowid, sum_rowid, max_updated, data_hash = live_fingerprints[ds]
                        ver_raw = f'{next_gen}|{ds}|{doc_count}|{sum_rowid}|{max_updated}|{data_hash}|{new_prov_seq}|{k_cnt}'
                        catalog_version = hashlib.sha256(ver_raw.encode('utf-8')).hexdigest()[:24]
                        con.execute(
                            '''INSERT INTO dataset_sync_state
                               (dataset_id, dataset_hex, doc_count, max_rowid, sum_rowid, max_updated_at, id_checksum,
                                data_hash, knowledge_count, raw_doc_count, common_count, catalog_version, synced_at)
                               VALUES (?,?,?,?,?,?,0,?,?,?,?,?,?)
                               ON CONFLICT(dataset_id) DO UPDATE SET
                                 dataset_hex=excluded.dataset_hex,
                                 doc_count=excluded.doc_count,
                                 max_rowid=excluded.max_rowid,
                                 sum_rowid=excluded.sum_rowid,
                                 max_updated_at=excluded.max_updated_at,
                                 data_hash=excluded.data_hash,
                                 knowledge_count=excluded.knowledge_count,
                                 raw_doc_count=excluded.raw_doc_count,
                                 common_count=excluded.common_count,
                                 catalog_version=excluded.catalog_version,
                                 synced_at=excluded.synced_at''',
                            (
                                ds, uuid_hex(ds), doc_count, max_rowid, sum_rowid, max_updated,
                                data_hash, k_cnt, raw_cnt, com_cnt, catalog_version, now_iso,
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
                            doc_count, max_rowid, sum_rowid, max_updated, data_hash = live_fingerprints[ds]
                            ver_raw = f'{next_gen}|{ds}|{doc_count}|{sum_rowid}|{max_updated}|{data_hash}|{new_prov_seq}|{k_cnt}'
                            catalog_version = hashlib.sha256(ver_raw.encode('utf-8')).hexdigest()[:24]
                            con.execute(
                                '''UPDATE dataset_sync_state
                                   SET doc_count=?, max_rowid=?, sum_rowid=?, max_updated_at=?, data_hash=?,
                                       knowledge_count=?, raw_doc_count=?, common_count=?,
                                       catalog_version=?, synced_at=?
                                   WHERE dataset_id=?''',
                                (
                                    doc_count, max_rowid, sum_rowid, max_updated, data_hash,
                                    k_cnt, raw_cnt, com_cnt, catalog_version, now_iso, ds,
                                ),
                            )
                            continue
                    self._rebuild_day_buckets_and_state_locked(
                        con, ds, live_fingerprints[ds], next_gen, new_prov_seq, now_iso
                    )

                if mutated:
                    persisted_prov_seq = new_prov_seq if prov_ok else last_prov_seq
                    persisted_recall_rev = new_recall_rev if recall_ok else last_recall_rev
                    con.execute(
                        '''UPDATE catalog_meta
                           SET generation = ?, local_tz_name = ?, last_provenance_seq = ?,
                               last_recall_rev = ?, built_at = ?
                           WHERE singleton = 1''',
                        (next_gen, tz_name, persisted_prov_seq, persisted_recall_rev, now_iso),
                    )
                con.execute('COMMIT')
            except Exception:
                if con.in_transaction:
                    con.execute('ROLLBACK')
                raise

            # Commit succeeded: update in-memory chunk digest cache & stat watermarks
            for ds in removed_datasets:
                self._chunk_digests.pop(ds, None)
            for ds, cmap in new_chunk_digests.items():
                self._chunk_digests[ds] = cmap

            if use_bulk_index_build or (dirty_datasets and not incremental_bucket_deltas):
                con.execute('PRAGMA shrink_memory')
                try:
                    cog_con.execute('PRAGMA shrink_memory')
                except sqlite3.Error:
                    pass
            if prov_ok:
                self._last_prov_stat = prov_stat_now
            if recall_ok:
                self._last_recall_stat = recall_stat_now

            # Maintain O(1) pure-append reading deltas & category_counts caches ONLY when proven strictly pure-append!
            pure_append = (
                mutated
                and len(dirty_datasets) == 1
                and not removed_datasets
                and not tz_changed
                and not prov_changes
                and not recall_changes
                and not sessions_mutated
                and not force_full
            )
            if pure_append:
                only_ds = next(iter(dirty_datasets))
                if only_ds in proven_pure_append_ds:
                    upserts_only, del_only = dataset_diffs.get(only_ds, ([], []))
                    if upserts_only and not del_only:
                        appended = [
                            (str(p[1]), str(p[15]), int(p[16]), int(p[2]))
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
                            for (g_k, ds_k), cats_v in list(self._cat_counts_cache.items()):
                                if g_k == cur_gen and ds_k != only_ds:
                                    self._cat_counts_cache[(next_gen, ds_k)] = cats_v
                            if len(self._cat_counts_cache) > 64:
                                self._cat_counts_cache = {
                                    k: v for k, v in self._cat_counts_cache.items() if k[0] >= next_gen - 2
                                }
                            self._common_cat_counts_cache.pop((cur_gen, only_ds), None)
                    else:
                        with self._state_lock:
                            self._recent_appends.clear()
                            self._cat_counts_cache.clear()
                            self._common_cat_counts_cache.clear()
                else:
                    with self._state_lock:
                        self._recent_appends.clear()
                        self._cat_counts_cache.clear()
                        self._common_cat_counts_cache.clear()
            elif mutated:
                with self._state_lock:
                    self._recent_appends.clear()
                    self._cat_counts_cache.clear()
                    self._common_cat_counts_cache.clear()
                    for ds_c, (_days, _k, _r, _c, cold_cats) in cold_bucket_summaries.items():
                        self._cat_counts_cache[(next_gen, ds_c)] = dict(cold_cats)
            return next_gen, (prov_ok and recall_ok)
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
            if bulk_pragmas_set:
                try:
                    con.execute('PRAGMA synchronous = NORMAL')
                    con.execute('PRAGMA cache_size = -1024')
                    con.execute('PRAGMA threads = 2')
                    con.execute('PRAGMA shrink_memory')
                except sqlite3.Error:
                    pass
            if cog_in_tx:
                try:
                    cog_con.execute('ROLLBACK')
                except sqlite3.Error:
                    pass

    def sync(self, managed_datasets: list[str], *, force: bool = False, relations_resolver=None) -> int:
        """Synchronize catalog index on the single dedicated writer thread.

        O(1) stat short-circuit returns immediately (<0.2ms) when none of the
        underlying SQLite stores, timezone, or managed dataset set changed.
        Concurrent callers coalesce onto an in-flight sync and re-check the stat
        signature upon wakeup so read-after-write never observes a pre-write snapshot.
        """
        ds_tuple = tuple(sorted({uuid_text(ds) for ds in managed_datasets if ds}))
        rel_sig_fn = getattr(relations_resolver, 'relation_sig', None) if relations_resolver is not None else None

        def _full_stat_sig():
            base = self._compute_stat_signature(ds_tuple)
            if relations_resolver is None:
                return base + (None,)
            if callable(rel_sig_fn):
                try:
                    return base + (tuple(str(rel_sig_fn(ds) or '') for ds in ds_tuple),)
                except Exception:
                    return base + (time.monotonic_ns(),)
            return base + (time.monotonic_ns(),)

        entry_sig = _full_stat_sig() if not force else None
        for attempt in range(2):
            if not force:
                sig = entry_sig if attempt == 0 else _full_stat_sig()
                with self._state_lock:
                    if not self._rebuilding and self._last_stat_sig == sig:
                        return self._last_generation
                    inflight = self._inflight_sync
                    if inflight is not None and inflight[0] == ds_tuple:
                        shared_future = inflight[1]
                        shared_start_sig = inflight[2]
                    else:
                        shared_future = None
                        shared_start_sig = None
                if shared_future is not None:
                    gen = shared_future.result()
                    # Linearizable read-after-write guard:
                    # If the in-flight sync already started at or after our entry_sig, we observed
                    # every write committed before we arrived — return immediately without chasing
                    # future writes arriving after our call began!
                    with self._state_lock:
                        if not self._rebuilding and (
                            self._last_stat_sig == sig or shared_start_sig == sig
                        ):
                            return self._last_generation
                    if attempt == 0:
                        continue
                    return gen

            def _run():
                with self._write_lock:
                    sig_before = _full_stat_sig()
                    if not force:
                        with self._state_lock:
                            if not self._rebuilding and self._last_stat_sig == sig_before:
                                return self._last_generation
                    try:
                        gen, cacheable = self._sync_into_connection(
                            self._write_con,
                            list(ds_tuple),
                            force_full=force,
                            relations_resolver=relations_resolver,
                        )
                    except sqlite3.DatabaseError:
                        gen, cacheable = self._atomic_rebuild_locked(
                            list(ds_tuple),
                            relations_resolver=relations_resolver,
                        )
                    with self._state_lock:
                        self._last_stat_sig = sig_before if cacheable else None
                        self._last_generation = gen
                    return gen

            with self._state_lock:
                if not force:
                    sig_check = entry_sig if attempt == 0 else _full_stat_sig()
                    if not self._rebuilding and self._last_stat_sig == sig_check:
                        return self._last_generation
                    inflight = self._inflight_sync
                    if inflight is not None and inflight[0] == ds_tuple:
                        fut = inflight[1]
                        shared_start_sig = inflight[2]
                        owns_slot = False
                    else:
                        fut = self._writer_executor.submit(_run)
                        self._inflight_sync = (ds_tuple, fut, sig_check)
                        shared_start_sig = sig_check
                        owns_slot = True
                else:
                    fut = self._writer_executor.submit(_run)
                    self._inflight_sync = (ds_tuple, fut, None)
                    shared_start_sig = None
                    owns_slot = True

            try:
                gen = fut.result()
            finally:
                if owns_slot:
                    with self._state_lock:
                        if self._inflight_sync is not None and self._inflight_sync[1] is fut:
                            self._inflight_sync = None

            if force or owns_slot:
                return gen
            with self._state_lock:
                if not self._rebuilding and (
                    self._last_stat_sig == sig_check or shared_start_sig == sig_check
                ):
                    return self._last_generation
        return self._last_generation

    async def sync_async(self, managed_datasets: list[str], *, force: bool = False, relations_resolver=None) -> int:
        """Asyncio-friendly wrapper around ``sync()`` that never blocks caller worker threads.

        Returns immediately on stat short-circuit (<0.05ms) and uses ``asyncio.wrap_future``
        to await the single-writer executor without consuming any ``v2_api`` DB-pool thread.
        """
        ds_tuple = tuple(sorted({uuid_text(ds) for ds in managed_datasets if ds}))
        rel_sig_fn = getattr(relations_resolver, 'relation_sig', None) if relations_resolver is not None else None

        def _full_stat_sig():
            base = self._compute_stat_signature(ds_tuple)
            if relations_resolver is None:
                return base + (None,)
            if callable(rel_sig_fn):
                try:
                    return base + (tuple(str(rel_sig_fn(ds) or '') for ds in ds_tuple),)
                except Exception:
                    return base + (time.monotonic_ns(),)
            return base + (time.monotonic_ns(),)

        entry_sig = _full_stat_sig() if not force else None
        for attempt in range(2):
            if not force:
                sig = entry_sig if attempt == 0 else _full_stat_sig()
                with self._state_lock:
                    if not self._rebuilding and self._last_stat_sig == sig:
                        return self._last_generation
                    inflight = self._inflight_sync
                    if inflight is not None and inflight[0] == ds_tuple:
                        shared_future = inflight[1]
                        shared_start_sig = inflight[2]
                    else:
                        shared_future = None
                        shared_start_sig = None
                if shared_future is not None:
                    gen = await asyncio.wrap_future(shared_future)
                    with self._state_lock:
                        if not self._rebuilding and (
                            self._last_stat_sig == sig or shared_start_sig == sig
                        ):
                            return self._last_generation
                    if attempt == 0:
                        continue
                    return gen

            def _run():
                with self._write_lock:
                    sig_before = _full_stat_sig()
                    if not force:
                        with self._state_lock:
                            if not self._rebuilding and self._last_stat_sig == sig_before:
                                return self._last_generation
                    try:
                        gen, cacheable = self._sync_into_connection(
                            self._write_con,
                            list(ds_tuple),
                            force_full=force,
                            relations_resolver=relations_resolver,
                        )
                    except sqlite3.DatabaseError:
                        gen, cacheable = self._atomic_rebuild_locked(
                            list(ds_tuple),
                            relations_resolver=relations_resolver,
                        )
                    with self._state_lock:
                        self._last_stat_sig = sig_before if cacheable else None
                        self._last_generation = gen
                    return gen

            with self._state_lock:
                if not force:
                    sig_check = entry_sig if attempt == 0 else _full_stat_sig()
                    if not self._rebuilding and self._last_stat_sig == sig_check:
                        return self._last_generation
                    inflight = self._inflight_sync
                    if inflight is not None and inflight[0] == ds_tuple:
                        fut = inflight[1]
                        shared_start_sig = inflight[2]
                        owns_slot = False
                    else:
                        fut = self._writer_executor.submit(_run)
                        self._inflight_sync = (ds_tuple, fut, sig_check)
                        shared_start_sig = sig_check
                        owns_slot = True
                else:
                    fut = self._writer_executor.submit(_run)
                    self._inflight_sync = (ds_tuple, fut, None)
                    shared_start_sig = None
                    owns_slot = True

            try:
                gen = await asyncio.wrap_future(fut)
            finally:
                if owns_slot:
                    with self._state_lock:
                        if self._inflight_sync is not None and self._inflight_sync[1] is fut:
                            self._inflight_sync = None

            if force or owns_slot:
                return gen
            with self._state_lock:
                if not self._rebuilding and (
                    self._last_stat_sig == sig_check or shared_start_sig == sig_check
                ):
                    return self._last_generation
        return self._last_generation

    def sync_sessions(self, managed_datasets: list[str], *, relations_resolver=None, force: bool = False) -> int:
        """Synchronize ``catalog_sessions`` (and docs if changed) with the supplied relation resolver."""
        return self.sync(managed_datasets, force=force, relations_resolver=relations_resolver)

    def _atomic_rebuild_locked(self, managed_datasets: list[str], *, relations_resolver=None) -> tuple[int, bool]:
        """Self-healing atomic generation rebuild: build temp DB -> quick_check -> os.replace.

        Keeps the existing ``self._write_con`` open until ``tmp_con`` passes ``quick_check``
        so a failed rebuild never leaves the live handle closed.
        """
        with self._state_lock:
            self._rebuilding = True
        tmp_path = self.path.parent / f'{self.path.name}.rebuilding.{os.getpid()}.{time.time_ns()}'
        tmp_con = None
        write_con_closed = False
        try:
            tmp_con = _open_private_rw(tmp_path)
            _create_catalog_schema(tmp_con, self.tz_provider())
            gen, cacheable = self._sync_into_connection(
                tmp_con,
                managed_datasets,
                force_full=True,
                relations_resolver=relations_resolver,
            )
            check = tmp_con.execute('PRAGMA quick_check').fetchone()
            if not check or str(check[0]).lower() != 'ok':
                raise sqlite3.DatabaseError('catalog index quick_check failed after rebuild')
            tmp_con.execute('PRAGMA wal_checkpoint(TRUNCATE)')
            tmp_con.close()
            tmp_con = None

            # Wait for active readers and close existing writer ONLY right before atomic swap
            self._invalidate_readers()
            if self._write_con is not None:
                try:
                    self._write_con.close()
                except Exception:
                    pass
                self._write_con = None
                write_con_closed = True

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
            write_con_closed = False
            self._invalidate_readers()
            return gen, cacheable
        except Exception:
            if write_con_closed and self._write_con is None and self.path.is_file():
                try:
                    self._write_con = _open_private_rw(self.path)
                except Exception:
                    pass
            raise
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

    def rebuild_atomic(self, managed_datasets: list[str], *, relations_resolver=None) -> int:
        ds_list = sorted({uuid_text(ds) for ds in managed_datasets if ds})
        ds_tuple = tuple(ds_list)

        def _run():
            with self._write_lock:
                # Sample sig_before BEFORE reading cognee_db so concurrent writes during rebuild are not missed!
                sig_before = self._compute_stat_signature(ds_tuple) + (None,)
                gen, cacheable = self._atomic_rebuild_locked(ds_list, relations_resolver=relations_resolver)
                with self._state_lock:
                    self._last_stat_sig = sig_before if (cacheable and relations_resolver is None) else None
                    self._last_generation = gen
                return gen

        return self._writer_executor.submit(_run).result()

    # ============ Read-only O(log N + K) query methods ============

    def meta(self) -> dict:
        with self._reader_lease() as con:
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
        """Return appended knowledge tuples ``[(doc_id, created_at_ns, created_at_invalid, cognee_rowid)]``
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
        with self._reader_lease() as con:
            row = con.execute(
                '''SELECT dataset_id, doc_count, knowledge_count, raw_doc_count,
                          common_count, session_count, catalog_version
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
            'session_count': int(row[5] or 0),
            'catalog_version': str(row[6]),
        }

    def all_dataset_sync_info(self, allowed_datasets: list[str]) -> dict[str, dict]:
        if not allowed_datasets:
            return {}
        out = {}
        with self._reader_lease() as con:
            for start in range(0, len(allowed_datasets), _MAX_IN_BATCH):
                batch = [uuid_text(ds) for ds in allowed_datasets[start : start + _MAX_IN_BATCH]]
                ph = ','.join('?' for _ in batch)
                for row in con.execute(
                    f'''SELECT dataset_id, doc_count, knowledge_count, raw_doc_count,
                               common_count, session_count, catalog_version
                        FROM dataset_sync_state WHERE dataset_id IN ({ph})''',
                    batch,
                ):
                    out[str(row[0])] = {
                        'dataset_id': str(row[0]),
                        'doc_count': int(row[1]),
                        'knowledge_count': int(row[2]),
                        'raw_doc_count': int(row[3]),
                        'common_count': int(row[4]),
                        'session_count': int(row[5] or 0),
                        'catalog_version': str(row[6]),
                    }
        return out

    def overview_day_buckets(self, allowed_datasets: list[str], *, days: int = 365) -> list[dict]:
        if not allowed_datasets:
            return []
        totals: dict[str, int] = {}
        with self._reader_lease() as con:
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
        if not allowed_datasets or limit <= 0:
            return []
        ds_list = sorted({uuid_text(ds) for ds in allowed_datasets if ds})
        branches: list[list[dict]] = []
        with self._reader_lease() as con:
            for ds in ds_list:
                rows = [
                    _row_to_doc_dict(row)
                    for row in con.execute(
                        f'''SELECT {_SELECT_DOC_COLS}
                            FROM catalog_docs
                            WHERE dataset_id = ? AND is_knowledge = 1
                            ORDER BY day_desc_key ASC, doc_id ASC
                            LIMIT ?''',
                        (ds, int(limit)),
                    )
                ]
                if rows:
                    branches.append(rows)
        if not branches:
            return []
        if len(branches) == 1:
            return branches[0][:limit]
        merged = heapq.merge(
            *branches,
            key=lambda d: (int(d['day_desc_key']), str(d['dataset_id']), str(d['doc_id'])),
        )
        return list(itertools.islice(merged, int(limit)))

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
        ds_list = sorted({uuid_text(ds) for ds in allowed_datasets if ds})
        total_count = 0
        items: list[dict] = []
        has_more = False
        day_key = day_to_desc_key(day)
        with self._reader_lease() as con:
            for start in range(0, len(ds_list), _MAX_IN_BATCH):
                batch = ds_list[start : start + _MAX_IN_BATCH]
                ph = ','.join('?' for _ in batch)
                row = con.execute(
                    f'''SELECT COALESCE(SUM(knowledge_count), 0)
                        FROM catalog_day_buckets
                        WHERE effective_date = ? AND dataset_id IN ({ph})''',
                    (day, *batch),
                ).fetchone()
                total_count += int(row[0]) if row else 0

            for ds in ds_list:
                if after_key is not None and ds < after_key[0]:
                    continue
                need = (limit - len(items)) + 1
                if after_key is not None and ds == after_key[0]:
                    rows = con.execute(
                        f'''SELECT {_SELECT_DOC_COLS}
                            FROM catalog_docs
                            WHERE dataset_id = ? AND is_knowledge = 1 AND day_desc_key = ?
                              AND doc_id > ?
                            ORDER BY doc_id ASC
                            LIMIT ?''',
                        (ds, day_key, after_key[1], need),
                    ).fetchall()
                else:
                    rows = con.execute(
                        f'''SELECT {_SELECT_DOC_COLS}
                            FROM catalog_docs
                            WHERE dataset_id = ? AND is_knowledge = 1 AND day_desc_key = ?
                            ORDER BY doc_id ASC
                            LIMIT ?''',
                        (ds, day_key, need),
                    ).fetchall()
                for row in rows:
                    if len(items) < limit:
                        items.append(_row_to_doc_dict(row))
                    else:
                        has_more = True
                        break
                if has_more:
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
        before_key: tuple | None = None,
        anchor_doc_id: str = '',
    ) -> dict | None:
        """Query project knowledge with bidirectional same-direction Keyset pagination and anchor jump."""
        ds = uuid_text(dataset_id)
        anchor_id = uuid_text(anchor_doc_id) if anchor_doc_id else ''

        def _item_sort_key(d: dict) -> tuple:
            if sort == 'recall':
                return (int(d['recall_desc_key']), int(d['day_desc_key']), str(d['doc_id']))
            return (int(d['day_desc_key']), str(d['doc_id']))

        with self._reader_lease() as con:
            # Sparse subset path (when unread_only or recall_(agent_id, event_id) filter is active)
            if allowed_doc_ids is not None:
                if not allowed_doc_ids:
                    if anchor_id:
                        return None
                    return {
                        'items': [],
                        'has_more': False,
                        'has_prev': False,
                        'total_filtered': 0,
                        'category_counts': {},
                    }
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
                    cat_lbl = item['category']
                    if cat_lbl:
                        category_counts[cat_lbl] = category_counts.get(cat_lbl, 0) + 1

                filtered = [item for item in base_rows if not category or item['category'] == category]
                filtered.sort(key=_item_sort_key)
                total_filtered = len(filtered)

                if anchor_id:
                    anchor_idx = next((i for i, r in enumerate(filtered) if r['doc_id'] == anchor_id), -1)
                    if anchor_idx < 0:
                        return None
                    half = limit // 2
                    lo = max(0, anchor_idx - half)
                    hi = min(total_filtered, lo + limit)
                    lo = max(0, hi - limit)
                    return {
                        'items': filtered[lo:hi],
                        'has_more': hi < total_filtered,
                        'has_prev': lo > 0,
                        'total_filtered': total_filtered,
                        'category_counts': category_counts,
                    }

                if before_key is not None:
                    prev_all = [r for r in filtered if _item_sort_key(r) < before_key]
                    has_prev = len(prev_all) > limit
                    page = prev_all[-limit:] if limit > 0 else []
                    has_more = bool(page and _item_sort_key(page[-1]) < _item_sort_key(filtered[-1]))
                    return {
                        'items': page,
                        'has_more': has_more,
                        'has_prev': has_prev,
                        'total_filtered': total_filtered,
                        'category_counts': category_counts,
                    }

                if after_key is not None:
                    next_all = [r for r in filtered if _item_sort_key(r) > after_key]
                    page = next_all[:limit]
                    has_more = len(next_all) > limit
                    has_prev = bool(page and _item_sort_key(page[0]) > _item_sort_key(filtered[0]))
                    return {
                        'items': page,
                        'has_more': has_more,
                        'has_prev': has_prev,
                        'total_filtered': total_filtered,
                        'category_counts': category_counts,
                    }

                return {
                    'items': filtered[:limit],
                    'has_more': total_filtered > limit,
                    'has_prev': False,
                    'total_filtered': total_filtered,
                    'category_counts': category_counts,
                }

            # Unfiltered / category-filtered indexed path using same-direction tuple Keyset indexes
            meta_r = con.execute('SELECT generation FROM catalog_meta WHERE singleton = 1').fetchone()
            cur_gen = int(meta_r[0]) if meta_r else 0
            cat_cache_key = (cur_gen, ds)
            with self._state_lock:
                cached_cats = self._cat_counts_cache.get(cat_cache_key)
            if cached_cats is not None:
                category_counts = dict(cached_cats)
            else:
                category_counts = {}
                for cat_lbl, cnt in con.execute(
                    '''SELECT category, COUNT(*)
                       FROM catalog_docs
                       WHERE dataset_id = ? AND is_knowledge = 1 AND category != ''
                       GROUP BY category''',
                    (ds,),
                ):
                    category_counts[str(cat_lbl)] = int(cnt)
                with self._state_lock:
                    if len(self._cat_counts_cache) >= 64:
                        self._cat_counts_cache.clear()
                    self._cat_counts_cache[cat_cache_key] = dict(category_counts)

            if category:
                total_filtered = category_counts.get(category, 0)
                base_where = 'dataset_id = ? AND is_knowledge = 1 AND category = ?'
                base_params: tuple = (ds, category)
            else:
                srow = con.execute(
                    'SELECT knowledge_count FROM dataset_sync_state WHERE dataset_id = ?',
                    (ds,),
                ).fetchone()
                total_filtered = int(srow[0]) if srow else 0
                base_where = 'dataset_id = ? AND is_knowledge = 1'
                base_params = (ds,)

            if sort == 'recall':
                key_expr = '(recall_desc_key, day_desc_key, doc_id)'
                key_ph = '(?, ?, ?)'
                order_asc = 'recall_desc_key ASC, day_desc_key ASC, doc_id ASC'
                order_desc = 'recall_desc_key DESC, day_desc_key DESC, doc_id DESC'
            else:
                key_expr = '(day_desc_key, doc_id)'
                key_ph = '(?, ?)'
                order_asc = 'day_desc_key ASC, doc_id ASC'
                order_desc = 'day_desc_key DESC, doc_id DESC'

            def _norm_key(k: tuple) -> tuple:
                if sort == 'recall':
                    return (int(k[0]), int(k[1]), str(k[2]))
                return (int(k[0]), str(k[1]))

            def _exists_before(k: tuple) -> bool:
                return con.execute(
                    f'SELECT 1 FROM catalog_docs WHERE {base_where} AND {key_expr} < {key_ph} ORDER BY {order_desc} LIMIT 1',
                    (*base_params, *_norm_key(k)),
                ).fetchone() is not None

            def _exists_after(k: tuple) -> bool:
                return con.execute(
                    f'SELECT 1 FROM catalog_docs WHERE {base_where} AND {key_expr} > {key_ph} ORDER BY {order_asc} LIMIT 1',
                    (*base_params, *_norm_key(k)),
                ).fetchone() is not None

            if anchor_id:
                anchor_row = con.execute(
                    f'SELECT {_SELECT_DOC_COLS} FROM catalog_docs WHERE dataset_id = ? AND doc_id = ?',
                    (ds, anchor_id),
                ).fetchone()
                if anchor_row is None:
                    return None
                anchor_dict = _row_to_doc_dict(anchor_row)
                if not anchor_dict['is_knowledge']:
                    return None
                if category and anchor_dict['category'] != category:
                    return None
                a_key = _item_sort_key(anchor_dict)
                before_want = limit // 2
                before_rows = [
                    _row_to_doc_dict(r)
                    for r in con.execute(
                        f'SELECT {_SELECT_DOC_COLS} FROM catalog_docs WHERE {base_where} AND {key_expr} < {key_ph} ORDER BY {order_desc} LIMIT ?',
                        (*base_params, *a_key, before_want + 1),
                    ).fetchall()
                ]
                has_prev = len(before_rows) > before_want
                before_slice = list(reversed(before_rows[:before_want]))

                after_want = max(0, limit - 1 - len(before_slice))
                after_rows = [
                    _row_to_doc_dict(r)
                    for r in con.execute(
                        f'SELECT {_SELECT_DOC_COLS} FROM catalog_docs WHERE {base_where} AND {key_expr} > {key_ph} ORDER BY {order_asc} LIMIT ?',
                        (*base_params, *a_key, after_want + 1),
                    ).fetchall()
                ]
                has_more = len(after_rows) > after_want
                after_slice = after_rows[:after_want]

                # Near bottom: pull additional predecessors to keep a full page when possible
                if len(after_slice) < after_want and has_prev:
                    extra_want = after_want - len(after_slice)
                    ref_key = _item_sort_key(before_slice[0]) if before_slice else a_key
                    extra_rows = [
                        _row_to_doc_dict(r)
                        for r in con.execute(
                            f'SELECT {_SELECT_DOC_COLS} FROM catalog_docs WHERE {base_where} AND {key_expr} < {key_ph} ORDER BY {order_desc} LIMIT ?',
                            (*base_params, *ref_key, extra_want + 1),
                        ).fetchall()
                    ]
                    has_prev = len(extra_rows) > extra_want
                    before_slice = list(reversed(extra_rows[:extra_want])) + before_slice

                page_items = before_slice + [anchor_dict] + after_slice
                return {
                    'items': page_items,
                    'has_more': has_more,
                    'has_prev': has_prev,
                    'total_filtered': total_filtered,
                    'category_counts': category_counts,
                }

            if before_key is not None:
                bk = _norm_key(before_key)
                rows = [
                    _row_to_doc_dict(r)
                    for r in con.execute(
                        f'SELECT {_SELECT_DOC_COLS} FROM catalog_docs WHERE {base_where} AND {key_expr} < {key_ph} ORDER BY {order_desc} LIMIT ?',
                        (*base_params, *bk, limit + 1),
                    ).fetchall()
                ]
                has_prev = len(rows) > limit
                page_items = list(reversed(rows[:limit]))
                has_more = _exists_after(_item_sort_key(page_items[-1])) if page_items else False
                return {
                    'items': page_items,
                    'has_more': has_more,
                    'has_prev': has_prev,
                    'total_filtered': total_filtered,
                    'category_counts': category_counts,
                }

            if after_key is not None:
                ak = _norm_key(after_key)
                rows = [
                    _row_to_doc_dict(r)
                    for r in con.execute(
                        f'SELECT {_SELECT_DOC_COLS} FROM catalog_docs WHERE {base_where} AND {key_expr} > {key_ph} ORDER BY {order_asc} LIMIT ?',
                        (*base_params, *ak, limit + 1),
                    ).fetchall()
                ]
                has_more = len(rows) > limit
                page_items = rows[:limit]
                has_prev = _exists_before(_item_sort_key(page_items[0])) if page_items else False
                return {
                    'items': page_items,
                    'has_more': has_more,
                    'has_prev': has_prev,
                    'total_filtered': total_filtered,
                    'category_counts': category_counts,
                }

            rows = [
                _row_to_doc_dict(r)
                for r in con.execute(
                    f'SELECT {_SELECT_DOC_COLS} FROM catalog_docs WHERE {base_where} ORDER BY {order_asc} LIMIT ?',
                    (*base_params, limit + 1),
                ).fetchall()
            ]
            return {
                'items': rows[:limit],
                'has_more': len(rows) > limit,
                'has_prev': False,
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
        before_key: tuple[int, str] | None = None,
        anchor_doc_id: str = '',
    ) -> dict | None:
        """Query common knowledge sorted by full updatedAt/createdAt timestamp DESC with anchor & bidirectional cursors."""
        ds = uuid_text(dataset_id)
        anchor_id = uuid_text(anchor_doc_id) if anchor_doc_id else ''

        def _ckey(d: dict) -> tuple[int, str]:
            return (int(d['sort_ts_desc_key']), str(d['doc_id']))

        with self._reader_lease() as con:
            meta_r = con.execute('SELECT generation FROM catalog_meta WHERE singleton = 1').fetchone()
            cur_gen = int(meta_r[0]) if meta_r else 0
            cc_key = (cur_gen, ds)
            with self._state_lock:
                cached_cc = self._common_cat_counts_cache.get(cc_key)
            if cached_cc is not None:
                category_counts = dict(cached_cc)
            else:
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
                with self._state_lock:
                    if len(self._common_cat_counts_cache) >= 64:
                        self._common_cat_counts_cache.clear()
                    self._common_cat_counts_cache[cc_key] = dict(category_counts)

            if common_category in ('preference', 'experience'):
                total_filtered = category_counts[common_category]
                base_where = "dataset_id = ? AND kind = 'common' AND common_category = ?"
                base_params: tuple = (ds, common_category)
            else:
                total_filtered = category_counts['preference'] + category_counts['experience']
                base_where = "dataset_id = ? AND kind = 'common'"
                base_params = (ds,)

            order_asc = 'sort_ts_desc_key ASC, doc_id ASC'
            order_desc = 'sort_ts_desc_key DESC, doc_id DESC'

            def _exists_before(k: tuple[int, str]) -> bool:
                return con.execute(
                    f'SELECT 1 FROM catalog_docs WHERE {base_where} AND (sort_ts_desc_key, doc_id) < (?, ?) ORDER BY {order_desc} LIMIT 1',
                    (*base_params, int(k[0]), str(k[1])),
                ).fetchone() is not None

            def _exists_after(k: tuple[int, str]) -> bool:
                return con.execute(
                    f'SELECT 1 FROM catalog_docs WHERE {base_where} AND (sort_ts_desc_key, doc_id) > (?, ?) ORDER BY {order_asc} LIMIT 1',
                    (*base_params, int(k[0]), str(k[1])),
                ).fetchone() is not None

            if anchor_id:
                arow = con.execute(
                    f'SELECT {_SELECT_DOC_COLS} FROM catalog_docs WHERE dataset_id = ? AND doc_id = ?',
                    (ds, anchor_id),
                ).fetchone()
                if arow is None:
                    return None
                adict = _row_to_doc_dict(arow)
                if adict['kind'] != 'common':
                    return None
                if common_category in ('preference', 'experience') and adict['common_category'] != common_category:
                    return None
                a_key = _ckey(adict)
                before_want = limit // 2
                before_rows = [
                    _row_to_doc_dict(r)
                    for r in con.execute(
                        f'SELECT {_SELECT_DOC_COLS} FROM catalog_docs WHERE {base_where} AND (sort_ts_desc_key, doc_id) < (?, ?) ORDER BY {order_desc} LIMIT ?',
                        (*base_params, a_key[0], a_key[1], before_want + 1),
                    ).fetchall()
                ]
                has_prev = len(before_rows) > before_want
                before_slice = list(reversed(before_rows[:before_want]))

                after_want = max(0, limit - 1 - len(before_slice))
                after_rows = [
                    _row_to_doc_dict(r)
                    for r in con.execute(
                        f'SELECT {_SELECT_DOC_COLS} FROM catalog_docs WHERE {base_where} AND (sort_ts_desc_key, doc_id) > (?, ?) ORDER BY {order_asc} LIMIT ?',
                        (*base_params, a_key[0], a_key[1], after_want + 1),
                    ).fetchall()
                ]
                has_more = len(after_rows) > after_want
                after_slice = after_rows[:after_want]

                if len(after_slice) < after_want and has_prev:
                    extra_want = after_want - len(after_slice)
                    ref_key = _ckey(before_slice[0]) if before_slice else a_key
                    extra_rows = [
                        _row_to_doc_dict(r)
                        for r in con.execute(
                            f'SELECT {_SELECT_DOC_COLS} FROM catalog_docs WHERE {base_where} AND (sort_ts_desc_key, doc_id) < (?, ?) ORDER BY {order_desc} LIMIT ?',
                            (*base_params, ref_key[0], ref_key[1], extra_want + 1),
                        ).fetchall()
                    ]
                    has_prev = len(extra_rows) > extra_want
                    before_slice = list(reversed(extra_rows[:extra_want])) + before_slice

                return {
                    'items': before_slice + [adict] + after_slice,
                    'has_more': has_more,
                    'has_prev': has_prev,
                    'total_filtered': total_filtered,
                    'category_counts': category_counts,
                }

            if before_key is not None:
                rows = [
                    _row_to_doc_dict(r)
                    for r in con.execute(
                        f'SELECT {_SELECT_DOC_COLS} FROM catalog_docs WHERE {base_where} AND (sort_ts_desc_key, doc_id) < (?, ?) ORDER BY {order_desc} LIMIT ?',
                        (*base_params, int(before_key[0]), str(before_key[1]), limit + 1),
                    ).fetchall()
                ]
                has_prev = len(rows) > limit
                page_items = list(reversed(rows[:limit]))
                has_more = _exists_after(_ckey(page_items[-1])) if page_items else False
                return {
                    'items': page_items,
                    'has_more': has_more,
                    'has_prev': has_prev,
                    'total_filtered': total_filtered,
                    'category_counts': category_counts,
                }

            if after_key is not None:
                rows = [
                    _row_to_doc_dict(r)
                    for r in con.execute(
                        f'SELECT {_SELECT_DOC_COLS} FROM catalog_docs WHERE {base_where} AND (sort_ts_desc_key, doc_id) > (?, ?) ORDER BY {order_asc} LIMIT ?',
                        (*base_params, int(after_key[0]), str(after_key[1]), limit + 1),
                    ).fetchall()
                ]
                has_more = len(rows) > limit
                page_items = rows[:limit]
                has_prev = _exists_before(_ckey(page_items[0])) if page_items else False
                return {
                    'items': page_items,
                    'has_more': has_more,
                    'has_prev': has_prev,
                    'total_filtered': total_filtered,
                    'category_counts': category_counts,
                }

            rows = [
                _row_to_doc_dict(r)
                for r in con.execute(
                    f'SELECT {_SELECT_DOC_COLS} FROM catalog_docs WHERE {base_where} ORDER BY {order_asc} LIMIT ?',
                    (*base_params, limit + 1),
                ).fetchall()
            ]
            return {
                'items': rows[:limit],
                'has_more': len(rows) > limit,
                'has_prev': False,
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
        with self._reader_lease() as con:
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

    # ============ Indexed Keyset Session Queries (catalog_sessions) ============

    def query_top_level_sessions_page(
        self,
        dataset_id: str,
        *,
        limit: int = 30,
        after_key: tuple[int, str, str] | None = None,
    ) -> list[dict]:
        """Indexed O(log N + K) Keyset scan over top-level sessions (zero TEMP B-TREE)."""
        ds = uuid_text(dataset_id)
        with self._reader_lease() as con:
            if after_key is not None:
                sql = f'''SELECT {_SELECT_SESS_COLS}
                          FROM catalog_sessions
                          WHERE dataset_id = ? AND is_top_level = 1
                            AND (sort_ts_desc_key, user_id, session_id) > (?, ?, ?)
                          ORDER BY sort_ts_desc_key ASC, user_id ASC, session_id ASC
                          LIMIT ?'''
                params = (ds, int(after_key[0]), str(after_key[1]), str(after_key[2]), limit + 1)
            else:
                sql = f'''SELECT {_SELECT_SESS_COLS}
                          FROM catalog_sessions
                          WHERE dataset_id = ? AND is_top_level = 1
                          ORDER BY sort_ts_desc_key ASC, user_id ASC, session_id ASC
                          LIMIT ?'''
                params = (ds, limit + 1)
            return [_row_to_session_dict(r) for r in con.execute(sql, params).fetchall()]

    def query_session_children_page(
        self,
        dataset_id: str,
        parent_session_id: str,
        *,
        user_id: str = '',
        limit: int = 30,
        after_key: tuple[int, str, str] | None = None,
    ) -> dict:
        """Indexed O(log N + K) Keyset scan over descendant/child sessions (zero TEMP B-TREE)."""
        ds = uuid_text(dataset_id)
        uid_filter = uuid_text(user_id) if user_id else ''
        with self._reader_lease() as con:
            if uid_filter:
                parent_row = con.execute(
                    f'SELECT {_SELECT_SESS_COLS} FROM catalog_sessions WHERE dataset_id = ? AND session_id = ? AND user_id = ? LIMIT 1',
                    (ds, parent_session_id, uid_filter),
                ).fetchone()
                sibling_owner_cnt = int(
                    con.execute(
                        'SELECT COUNT(*) FROM catalog_sessions WHERE dataset_id = ? AND session_id = ?',
                        (ds, parent_session_id),
                    ).fetchone()[0]
                    or 0
                )
            else:
                parent_row = con.execute(
                    f'''SELECT {_SELECT_SESS_COLS}
                        FROM catalog_sessions
                        WHERE dataset_id = ? AND session_id = ?
                        ORDER BY sort_ts_desc_key ASC, user_id ASC
                        LIMIT 1''',
                    (ds, parent_session_id),
                ).fetchone()
                sibling_owner_cnt = 1
            if parent_row is None:
                raise PolicyError('session_not_found', 404)
            parent = _row_to_session_dict(parent_row)
            is_top = bool(parent['is_top_level'])
            scope_by_user = bool(uid_filter and sibling_owner_cnt > 1)

            if is_top:
                base_where = 'dataset_id = ? AND is_top_level = 0 AND root_session_id = ?'
                base_params: tuple = (ds, parent_session_id)
            else:
                base_where = 'dataset_id = ? AND parent_session_id = ?'
                base_params = (ds, parent_session_id)

            if after_key is not None:
                sql = f'''SELECT {_SELECT_SESS_COLS}
                          FROM catalog_sessions
                          WHERE {base_where}
                            AND (sort_ts_desc_key, user_id, session_id) > (?, ?, ?)
                          ORDER BY sort_ts_desc_key ASC, user_id ASC, session_id ASC'''
                params = (*base_params, int(after_key[0]), str(after_key[1]), str(after_key[2]))
            else:
                sql = f'''SELECT {_SELECT_SESS_COLS}
                          FROM catalog_sessions
                          WHERE {base_where}
                          ORDER BY sort_ts_desc_key ASC, user_id ASC, session_id ASC'''
                params = base_params

            if not scope_by_user:
                sql += ' LIMIT ?'
                params = (*params, limit + 1)
                rows = [_row_to_session_dict(r) for r in con.execute(sql, params).fetchall()]
                has_more = len(rows) > limit
                page_rows = rows[:limit]
            else:
                page_rows = []
                has_more = False
                for r in con.execute(sql, params):
                    item = _row_to_session_dict(r)
                    if item['user_id'] != uid_filter:
                        continue
                    if len(page_rows) < limit:
                        page_rows.append(item)
                    else:
                        has_more = True
                        break

            return {
                'total_count': int(parent['child_count']),
                'items': page_rows,
                'has_more': has_more,
            }

    def query_recent_sessions(self, allowed_datasets: list[str], *, limit: int = 20) -> list[dict]:
        """Indexed recent sessions across verified datasets via ``idx_csess_recent_ts`` (zero TEMP B-TREE)."""
        if not allowed_datasets:
            return []
        allowed_set = {uuid_text(ds) for ds in allowed_datasets}
        out = []
        with self._reader_lease() as con:
            for r in con.execute(
                f'''SELECT {_SELECT_SESS_COLS}
                    FROM catalog_sessions
                    ORDER BY sort_ts_desc_key ASC, user_id ASC, session_id ASC'''
            ):
                if str(r[0]) not in allowed_set:
                    continue
                out.append(_row_to_session_dict(r))
                if len(out) >= limit:
                    break
        return out

    def query_pending_raw_docs(self, dataset_id: str, *, limit: int = 20) -> tuple[list[dict], int]:
        ds = uuid_text(dataset_id)
        with self._reader_lease() as con:
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
        kind_clause = ''
        if filter_kind == 'knowledge':
            kind_clause = ' AND is_knowledge = 1'
        elif filter_kind == 'raw':
            kind_clause = " AND kind = 'raw_doc'"

        results = []
        with self._reader_lease() as con:
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

    def iter_knowledge_provenance_keys(self, allowed_datasets: list[str]) -> list[tuple[str, str, str, str]]:
        """Return ``[(dataset_id, doc_id, lesson_type, state_key)]`` for provenance legacy backfill."""
        if not allowed_datasets:
            return []
        out = []
        with self._reader_lease() as con:
            for start in range(0, len(allowed_datasets), _MAX_IN_BATCH):
                batch = [uuid_text(ds) for ds in allowed_datasets[start : start + _MAX_IN_BATCH]]
                ph = ','.join('?' for _ in batch)
                for ds_id, doc_id, lt, sk in con.execute(
                    f'''SELECT dataset_id, doc_id, lesson_type, state_key
                        FROM catalog_docs
                        WHERE dataset_id IN ({ph}) AND is_knowledge = 1''',
                    batch,
                ):
                    out.append((str(ds_id), str(doc_id), str(lt or '').strip(), str(sk or '').strip()))
        return out

    # ============ Sparse unread support helpers (no cross-DB ATTACH) ============

    def knowledge_docs_after_horizon(self, dataset_id: str, horizon_ns: str) -> list[tuple[str, str]]:
        """Indexed scan on idx_cdocs_proj_created for Branch B (created_at_ns > horizon_ns)."""
        ds = uuid_text(dataset_id)
        with self._reader_lease() as con:
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
        with self._reader_lease() as con:
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

    def knowledge_doc_ids_late_after_rowid(
        self,
        dataset_id: str,
        min_rowid: int,
        enabled_ns: str,
        horizon_ns: str,
    ) -> list[str]:
        """Indexed scan on ``idx_cdocs_proj_rowid`` for late-arriving backdated docs after compaction."""
        ds = uuid_text(dataset_id)
        with self._reader_lease() as con:
            return [
                str(r[0])
                for r in con.execute(
                    '''SELECT doc_id
                       FROM catalog_docs
                       WHERE dataset_id = ? AND is_knowledge = 1
                         AND cognee_rowid > ?
                         AND created_at_invalid = 0
                         AND created_at_ns > ? AND created_at_ns <= ?''',
                    (ds, int(min_rowid), enabled_ns, horizon_ns),
                ).fetchall()
            ]

    def knowledge_docs_invalid_created(self, dataset_id: str) -> list[str]:
        """Partial-index scan on idx_cdocs_proj_invalid_ts for Branch C (created_at_invalid = 1)."""
        ds = uuid_text(dataset_id)
        with self._reader_lease() as con:
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
        with self._reader_lease() as con:
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

    def knowledge_docs_in_range_with_rowid(
        self,
        dataset_id: str,
        low_ns: str,
        high_ns: str,
    ) -> list[tuple[str, str, int]]:
        """Fetch ``(doc_id, created_at_ns, cognee_rowid)`` with ``low_ns < created_at_ns <= high_ns``."""
        ds = uuid_text(dataset_id)
        with self._reader_lease() as con:
            return [
                (str(r[0]), str(r[1]), int(r[2] or 0))
                for r in con.execute(
                    '''SELECT doc_id, created_at_ns, cognee_rowid
                       FROM catalog_docs
                       WHERE dataset_id = ? AND is_knowledge = 1
                         AND created_at_ns > ? AND created_at_ns <= ?''',
                    (ds, low_ns, high_ns),
                ).fetchall()
            ]

    def max_knowledge_created_at_ns(self, dataset_id: str) -> str:
        ds = uuid_text(dataset_id)
        with self._reader_lease() as con:
            row = con.execute(
                '''SELECT created_at_ns
                   FROM catalog_docs
                   WHERE dataset_id = ? AND is_knowledge = 1 AND created_at_ns != ''
                   ORDER BY created_at_ns DESC, doc_id ASC
                   LIMIT 1''',
                (ds,),
            ).fetchone()
        return str(row[0]) if row else ''

    def max_knowledge_rowid(self, dataset_id: str) -> int:
        ds = uuid_text(dataset_id)
        with self._reader_lease() as con:
            row = con.execute(
                '''SELECT cognee_rowid
                   FROM catalog_docs
                   WHERE dataset_id = ? AND is_knowledge = 1
                   ORDER BY cognee_rowid DESC
                   LIMIT 1''',
                (ds,),
            ).fetchone()
        return int(row[0] or 0) if row else 0

    def existing_knowledge_doc_ids(self, dataset_id: str, doc_ids: list[str]) -> set[str]:
        if not doc_ids:
            return set()
        ds = uuid_text(dataset_id)
        found = set()
        with self._reader_lease() as con:
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
        out = {}
        with self._reader_lease() as con:
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
        out = []
        with self._reader_lease() as con:
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
