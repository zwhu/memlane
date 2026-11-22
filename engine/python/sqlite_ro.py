"""Read-only SQLite access foundation and trusted metadata extraction for official Cognee stores.

Enforces kernel-level read-only guarantees (?mode=ro + PRAGMA query_only=ON +
SQLite set_authorizer) without ever using online ``immutable=1``.
"""
from contextlib import contextmanager
from datetime import datetime, timezone
import json
from pathlib import Path
import re
import sqlite3
import stat
import time
from urllib.parse import quote
import uuid

from project_policy import PolicyError

_RETRY_BACKOFFS_SECONDS = (0.05, 0.15, 0.30)
_TRANSIENT_RO_ERR_TOKENS = (
    'locked',
    'busy',
    'unable to open',
    'protocol',
    'shm',
    'wal',
    'disk i/o',
    'ioerr',
)


def _is_transient_ro_error(exc: sqlite3.OperationalError) -> bool:
    msg = str(exc or '').lower()
    return any(tok in msg for tok in _TRANSIENT_RO_ERR_TOKENS)

_ALLOWED_READONLY_ACTIONS = frozenset(
    action
    for action in (
        sqlite3.SQLITE_SELECT,
        sqlite3.SQLITE_READ,
        sqlite3.SQLITE_FUNCTION,
        getattr(sqlite3, 'SQLITE_RECURSIVE', None),
    )
    if action is not None
)

_READONLY_META_PRAGMAS = frozenset({'table_info', 'table_xinfo', 'index_list', 'index_info', 'shrink_memory'})

# Authoritative lesson / classification keys that user-editable external_metadata
# must never plant or override when Cognee's system_metadata is present.
_SYSTEM_AUTHORITATIVE_KEYS = frozenset({
    'lesson_type',
    'state_key',
    'first_source_date',
    'session_date',
    'source_date_status',
    'source_qa_ids',
    'session_id',
    'seen_in_sessions',
    'seen_sessions',
    'memlane_kind',
    'category',
    'common_category',
})

_KNOWLEDGE_NODE_SETS = frozenset({'session_learnings', 'user_context', 'memory'})

_LESSON_CATEGORY_LABELS = {
    'state': '项目状态',
    'pitfall': '踩坑经验',
    'convention': '项目约定',
    'location': '代码位置与关联',
    'change': '代码变更',
    'constraint': '约束条件',
    'other': '其他',
}


def _readonly_authorizer(action, arg1, arg2, db_name, trigger_name):
    """SQLite C-level authorizer permitting strictly read-only queries and deferred read snapshots.

    Note: SQLite passes ``arg1='BEGIN'`` for all BEGIN variants; write transaction intents
    (``BEGIN IMMEDIATE`` / ``BEGIN EXCLUSIVE``) are unconditionally rejected at the C layer
    by ``?mode=ro`` + ``PRAGMA query_only=ON`` with ``OperationalError``.
    """
    del db_name, trigger_name
    if action in _ALLOWED_READONLY_ACTIONS:
        return sqlite3.SQLITE_OK
    if action == sqlite3.SQLITE_TRANSACTION:
        op = str(arg1 or '').strip().upper()
        if op in ('BEGIN', 'COMMIT', 'ROLLBACK'):
            return sqlite3.SQLITE_OK
        return sqlite3.SQLITE_DENY
    if action == sqlite3.SQLITE_PRAGMA:
        name = str(arg1 or '').strip().lower()
        val = None if arg2 is None else str(arg2).strip().lower()
        if name == 'query_only' and val in (None, 'on', '1', 'true'):
            return sqlite3.SQLITE_OK
        if name == 'busy_timeout' and (val is None or val.isdigit()):
            return sqlite3.SQLITE_OK
        if name in _READONLY_META_PRAGMAS:
            return sqlite3.SQLITE_OK
        return sqlite3.SQLITE_DENY
    return sqlite3.SQLITE_DENY


def _validate_regular_db_file(db_path, error_code):
    if not db_path:
        raise PolicyError(error_code, 503)
    target = Path(db_path)
    try:
        info = target.lstat()
    except OSError as exc:
        raise PolicyError(error_code, 503) from exc
    if stat.S_ISLNK(info.st_mode) or not stat.S_ISREG(info.st_mode):
        raise PolicyError(error_code, 503)
    try:
        resolved = target.resolve(strict=True)
    except OSError as exc:
        raise PolicyError(error_code, 503) from exc
    if not resolved.is_file():
        raise PolicyError(error_code, 503)
    return resolved


def open_official_sqlite_ro(
    db_path,
    *,
    timeout_ms: int = 2000,
    error_code: str = 'official_store_unavailable',
    sleep_fn=time.sleep,
    cache_kib: int = 512,
):
    """Open an official SQLite store strictly read-only with kernel authorizer and page-read probe.

    Never uses ``immutable=1``. Retries transient ``OperationalError`` up to 3 times
    with backoff (50ms / 150ms / 300ms) before failing closed with
    ``PolicyError(error_code, 503)``.
    """
    resolved = _validate_regular_db_file(db_path, error_code)
    timeout_int = max(100, int(timeout_ms))
    cache_int = max(64, int(cache_kib))
    encoded = quote(str(resolved), safe='/')
    uri = f'file:{encoded}?mode=ro'

    last_err = None
    max_attempts = len(_RETRY_BACKOFFS_SECONDS) + 1
    for attempt in range(max_attempts):
        con = None
        try:
            con = sqlite3.connect(
                uri,
                uri=True,
                timeout=timeout_int / 1000.0,
                isolation_level=None,
                check_same_thread=False,
            )
            con.execute(f'PRAGMA cache_size = -{cache_int}')
            con.set_authorizer(_readonly_authorizer)
            con.execute('PRAGMA query_only = ON')
            con.execute(f'PRAGMA busy_timeout = {timeout_int}')
            # Force a real page read (never SELECT 1) to verify WAL/SHM readability.
            con.execute('SELECT name FROM sqlite_master LIMIT 1').fetchone()
            return con
        except sqlite3.OperationalError as exc:
            last_err = exc
            if con is not None:
                try:
                    con.close()
                except Exception:
                    pass
            if attempt < len(_RETRY_BACKOFFS_SECONDS) and _is_transient_ro_error(exc):
                sleep_fn(_RETRY_BACKOFFS_SECONDS[attempt])
                continue
            raise PolicyError(error_code, 503) from exc
        except sqlite3.Error as exc:
            if con is not None:
                try:
                    con.close()
                except Exception:
                    pass
            raise PolicyError(error_code, 503) from exc

    raise PolicyError(error_code, 503) from last_err


@contextmanager
def official_sqlite_ro(
    db_path,
    *,
    timeout_ms: int = 2000,
    error_code: str = 'official_store_unavailable',
    sleep_fn=time.sleep,
):
    con = open_official_sqlite_ro(
        db_path,
        timeout_ms=timeout_ms,
        error_code=error_code,
        sleep_fn=sleep_fn,
    )
    try:
        yield con
    finally:
        con.close()


@contextmanager
def ro_snapshot(con: sqlite3.Connection):
    """Execute read queries inside one consistent read-only WAL snapshot (BEGIN DEFERRED .. ROLLBACK).

    Fails closed if snapshot establishment fails (never silently continues in autocommit).
    """
    con.execute('BEGIN DEFERRED')
    try:
        yield con
    finally:
        if con.in_transaction:
            try:
                con.execute('ROLLBACK')
            except sqlite3.Error:
                pass


_HEX32_RE = re.compile(r'^[0-9a-fA-F]{32}$')
_UUID36_RE = re.compile(r'^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$')
_JSON_DECODE = json._default_decoder.decode


def uuid_text(value) -> str:
    if not value:
        return ''
    if isinstance(value, str):
        text = value if (value[0] > ' ' and value[-1] > ' ') else value.strip()
    else:
        text = str(value).strip()
    if not text:
        return ''
    n = len(text)
    if n == 32 and _HEX32_RE.match(text):
        low = text if text.islower() else text.lower()
        return f'{low[:8]}-{low[8:12]}-{low[12:16]}-{low[16:20]}-{low[20:]}'
    if n == 36 and text[8] == '-' and text[13] == '-' and text[18] == '-' and text[23] == '-' and _UUID36_RE.match(text):
        return text if text.islower() else text.lower()
    try:
        return str(uuid.UUID(text))
    except ValueError:
        return text


def uuid_hex(value) -> str:
    try:
        return uuid.UUID(str(value).strip()).hex
    except (ValueError, AttributeError):
        return ''


def is_valid_uuid(value) -> bool:
    if not isinstance(value, str):
        return False
    try:
        uuid.UUID(value.strip())
        return True
    except (ValueError, AttributeError):
        return False


def id_variants(value) -> tuple[str, ...]:
    text = str(value or '').strip()
    if not text:
        return ()
    variants = []
    for candidate in (uuid_hex(text), uuid_text(text), text):
        if candidate and candidate not in variants:
            variants.append(candidate)
    return tuple(variants)


def iso_text(value) -> str:
    if not value:
        return ''
    if isinstance(value, str):
        n = len(value)
        if n >= 19 and value[10] == 'T' and value[0] > ' ' and value[-1] > ' ':
            return value
        text = value if (value[0] > ' ' and value[-1] > ' ') else value.strip()
    else:
        text = str(value).strip()
    if len(text) >= 19 and text[10] == ' ':
        return text[:10] + 'T' + text[11:]
    return text


def parse_official_timestamp(value):
    """Parse Cognee/SQLite ISO-8601 or space-separated timestamps into timezone-aware UTC datetime."""
    if not isinstance(value, str):
        return None
    text = value.strip()
    if not text:
        return None
    normalized = iso_text(text)
    if normalized.endswith('Z') or normalized.endswith('z'):
        normalized = normalized[:-1] + '+00:00'
    try:
        dt = datetime.fromisoformat(normalized)
    except ValueError:
        return None
    if dt.tzinfo is None:
        dt = dt.replace(tzinfo=timezone.utc)
    return dt.astimezone(timezone.utc)


def parse_json_value(raw_value):
    if isinstance(raw_value, (dict, list)):
        return raw_value
    if not isinstance(raw_value, str):
        return None
    if raw_value == '{}':
        return {}
    if raw_value == '[]':
        return []
    if raw_value == '["session_learnings"]':
        return ['session_learnings']
    if not raw_value or not raw_value.strip():
        return None
    try:
        return _JSON_DECODE(raw_value)
    except ValueError:
        return None


def parse_json_dict(raw_value) -> dict:
    if raw_value is None or raw_value == '' or raw_value == '{}':
        return {}
    parsed = parse_json_value(raw_value)
    if isinstance(parsed, str):
        nested = parse_json_value(parsed)
        if isinstance(nested, dict):
            return dict(nested)
    return dict(parsed) if isinstance(parsed, dict) else {}


def decode_node_set(raw_value) -> list[str]:
    """Decode ``node_set`` tolerating both single- and double-JSON-encoded strings."""
    if raw_value is None or raw_value == '' or raw_value == '[]':
        return []
    if raw_value == '["session_learnings"]' or raw_value == '"[\\"session_learnings\\"]"':
        return ['session_learnings']
    if isinstance(raw_value, list):
        if len(raw_value) == 1 and isinstance(raw_value[0], str) and raw_value[0] and raw_value[0][0] != ' ':
            return raw_value
        out = []
        seen = set()
        for item in raw_value:
            if isinstance(item, str):
                cleaned = item.strip()
                if cleaned and cleaned not in seen:
                    seen.add(cleaned)
                    out.append(cleaned)
        return out
    parsed = raw_value
    # Unwrap up to 2 layers of JSON encoding (Cognee SQLite stores double-encoded JSON strings).
    for _ in range(2):
        if isinstance(parsed, str):
            text = parsed.strip()
            if not text:
                return []
            if text[0] in '["\'':
                decoded = parse_json_value(text)
                if decoded is not None:
                    parsed = decoded
                    continue
            break
    if isinstance(parsed, str):
        cleaned = parsed.strip()
        return [cleaned] if cleaned else []
    if isinstance(parsed, (list, tuple)):
        out = []
        seen = set()
        for item in parsed:
            if isinstance(item, str):
                cleaned = item.strip()
                if cleaned and cleaned not in seen:
                    seen.add(cleaned)
                    out.append(cleaned)
        return out
    return []


def extract_trusted_metadata(system_metadata, external_metadata, node_set=None) -> dict:
    """Merge metadata enforcing ``system_metadata > external_metadata`` and decoding ``node_set``.

    Security invariants:
    - When ``system_metadata`` is present (non-empty and not empty JSON ``{}``), authoritative
      lesson, date, classification keys and ``node_set`` from ``external_metadata`` are NEVER
      trusted — even if ``system_metadata`` is corrupted/non-dict JSON.
    - Only when ``system_metadata`` is genuinely absent (``None`` / ``''`` / ``'{}'`` / empty dict)
      may legacy ``external_metadata`` supply classification keys.
    """
    # Fast path when external_metadata is empty and system_metadata is a JSON dict string.
    if (external_metadata is None or external_metadata == '' or external_metadata == '{}') and isinstance(system_metadata, str) and system_metadata and system_metadata[0] == '{':
        if system_metadata != '{}':
            try:
                sys_obj = _JSON_DECODE(system_metadata)
            except ValueError:
                sys_obj = None
            if isinstance(sys_obj, dict) and sys_obj:
                col_ns = decode_node_set(node_set)
                resolved_ns = (
                    decode_node_set(sys_obj.get('node_set'))
                    if 'node_set' in sys_obj
                    else []
                ) or col_ns
                if resolved_ns:
                    sys_obj['node_set'] = resolved_ns
                return sys_obj

    sys_absent = (
        system_metadata is None
        or (isinstance(system_metadata, str) and system_metadata.strip() in ('', '{}'))
        or (isinstance(system_metadata, dict) and not system_metadata)
    )
    sys_meta = {} if sys_absent else parse_json_dict(system_metadata)
    ext_meta = parse_json_dict(external_metadata)
    col_ns = decode_node_set(node_set)

    if not sys_absent:
        # Either valid non-empty system_metadata OR corrupted/non-dict system_metadata:
        # Fail closed against external_metadata planting authoritative keys or node_set.
        trusted = {
            key: value
            for key, value in ext_meta.items()
            if key not in _SYSTEM_AUTHORITATIVE_KEYS and key != 'node_set'
        }
        if sys_meta:
            trusted.update(sys_meta)
        resolved_ns = (
            decode_node_set(sys_meta.get('node_set')) if sys_meta else []
        ) or col_ns
    else:
        trusted = dict(ext_meta)
        resolved_ns = col_ns or decode_node_set(ext_meta.get('node_set'))

    if resolved_ns:
        trusted['node_set'] = resolved_ns
    return trusted


def common_category_from_meta(meta) -> str:
    """Exact match for Go ``commonCategory`` (``common_knowledge.go:43-50``)."""
    if not isinstance(meta, dict):
        return ''
    if meta.get('memlane_kind') == 'common_knowledge':
        category = meta.get('category')
        if category in ('preference', 'experience'):
            return category
    return ''


def is_knowledge_meta(meta, *, include_legacy_common_key: bool = False) -> bool:
    if not isinstance(meta, dict):
        return False
    if common_category_from_meta(meta):
        return True
    if include_legacy_common_key and str(meta.get('common_category') or '').strip():
        return True
    return any(tag in _KNOWLEDGE_NODE_SETS for tag in decode_node_set(meta.get('node_set')))


def is_direct_memory_meta(meta) -> bool:
    """Exact match for Go ``Document.directMemory`` (``client.go:168-183``)."""
    if not isinstance(meta, dict) or common_category_from_meta(meta):
        return False
    tags = decode_node_set(meta.get('node_set'))
    if 'session_learnings' in tags:
        return False
    return any(tag in ('user_context', 'memory') for tag in tags)


def knowledge_category_label(meta) -> str:
    """Exact match for Go ``knowledgeCategoryLabel`` (``knowledge_basis.go:9-53``)."""
    if not isinstance(meta, dict) or not is_knowledge_meta(meta) or common_category_from_meta(meta):
        return ''
    if is_direct_memory_meta(meta):
        return '直接保存'
    raw_kind = meta.get('lesson_type')
    kind = raw_kind.strip() if isinstance(raw_kind, str) else ''
    label = _LESSON_CATEGORY_LABELS.get(kind)
    if label:
        return label
    if kind:
        return f'未识别分类（{kind}）'
    return '未记录'


def unique_learning_session(meta) -> str:
    """Exact match for Go ``uniqueLearningSession`` (``reading_presentation.go:43-59``)."""
    if not isinstance(meta, dict):
        return ''
    source = ''
    for tag in decode_node_set(meta.get('node_set')):
        if not tag.startswith('session_learnings:'):
            continue
        sid = tag[len('session_learnings:'):]
        if not sid or (source and sid != source):
            return ''
        source = sid
    meta_sid = meta.get('session_id')
    if isinstance(meta_sid, str) and meta_sid and meta_sid != source:
        return ''
    return source
