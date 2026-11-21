"""Read-only SQLite access foundation and trusted metadata extraction for official Cognee stores.

Enforces kernel-level read-only guarantees (?mode=ro + PRAGMA query_only=ON +
SQLite set_authorizer) without ever using online ``immutable=1``.
"""
from contextlib import contextmanager
from datetime import datetime, timezone
import json
from pathlib import Path
import sqlite3
import stat
import time
from urllib.parse import quote
import uuid

from project_policy import PolicyError

_RETRY_BACKOFFS_SECONDS = (0.05, 0.15, 0.30)

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

_READONLY_META_PRAGMAS = frozenset({'table_info', 'table_xinfo', 'index_list', 'index_info'})

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
    """SQLite C-level authorizer permitting strictly read-only queries."""
    del db_name, trigger_name
    if action in _ALLOWED_READONLY_ACTIONS:
        return sqlite3.SQLITE_OK
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
):
    """Open an official SQLite store strictly read-only with kernel authorizer and page-read probe.

    Never uses ``immutable=1``. Retries transient ``OperationalError`` up to 3 times
    with backoff (50ms / 150ms / 300ms) before failing closed with
    ``PolicyError(error_code, 503)``.
    """
    resolved = _validate_regular_db_file(db_path, error_code)
    timeout_int = max(100, int(timeout_ms))
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
                check_same_thread=False,
            )
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
            if attempt < len(_RETRY_BACKOFFS_SECONDS):
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


def uuid_text(value) -> str:
    if not value:
        return ''
    text = str(value).strip()
    if not text:
        return ''
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
    if not isinstance(raw_value, str) or not raw_value.strip():
        return None
    try:
        return json.loads(raw_value)
    except ValueError:
        return None


def parse_json_dict(raw_value) -> dict:
    parsed = parse_json_value(raw_value)
    if isinstance(parsed, str):
        nested = parse_json_value(parsed)
        if isinstance(nested, dict):
            return dict(nested)
    return dict(parsed) if isinstance(parsed, dict) else {}


def decode_node_set(raw_value) -> list[str]:
    """Decode ``node_set`` tolerating both single- and double-JSON-encoded strings."""
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

    Security invariant: when ``system_metadata`` is non-empty, authoritative lesson,
    date, and classification keys from ``external_metadata`` are never trusted so
    planted external fields cannot spoof provenance dates or categories.
    """
    sys_meta = parse_json_dict(system_metadata)
    ext_meta = parse_json_dict(external_metadata)
    col_ns = decode_node_set(node_set)

    if sys_meta:
        trusted = {
            key: value
            for key, value in ext_meta.items()
            if key not in _SYSTEM_AUTHORITATIVE_KEYS and key != 'node_set'
        }
        trusted.update(sys_meta)
        resolved_ns = (
            decode_node_set(sys_meta.get('node_set'))
            or col_ns
            or decode_node_set(ext_meta.get('node_set'))
        )
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
