"""Controlled local file body & preview reader with dir_fd + O_NOFOLLOW + fstat.

Eliminates Cognee 1.6.7's full-table ``get_dataset_data`` scan on ``/raw`` (H8)
for unencrypted local files under ``official_root/data``, while enforcing zero
resolve-to-open TOCTOU, zero symlink escape, UTF-8 rune-safe preview truncation,
and strict serialized JSON byte budgets.
"""
import asyncio
import hashlib
import json
import os
from pathlib import Path
import re
import stat
import threading
import time
from urllib.parse import unquote, urlparse

import aiohttp
from aiohttp import web

from project_control import read_secret
from project_policy import PolicyError
from sqlite_ro import id_variants, pooled_official_sqlite_ro, uuid_text

MAX_BODY_BYTES = 8 * 1024 * 1024
DEFAULT_PREVIEW_MAX_RUNES = 240
DEFAULT_PREVIEW_MAX_UTF8_BYTES = 720
DEFAULT_LIST_JSON_BUDGET_BYTES = 256 * 1024
DEFAULT_OVERVIEW_JSON_BUDGET_BYTES = 128 * 1024
_PREVIEW_RAW_READ_BYTES = 4096

_SESSION_HEADER_RE = re.compile(r'^# Session learning \(session ([^\r\n]+)\)$')
_TRUTHY_FLAGS = frozenset({'1', 'true', 'yes', 'on'})
_LOCAL_TEXT_MIME_EXACT = frozenset({
    '',
    'application/json',
    'application/xml',
    'application/x-yaml',
    'application/yaml',
    'application/toml',
    'application/javascript',
    'application/typescript',
    'application/x-sh',
})
_AUTH_CACHE_LOCK = threading.Lock()
_AUTH_CACHE: dict[tuple[int, str, str], tuple[float, frozenset[str]]] = {}
_AUTH_CACHE_TTL_SECONDS = 2.0


def _is_local_text_mime(mime_type: str) -> bool:
    raw = str(mime_type or '').split(';', 1)[0].strip().lower()
    if not raw or raw.startswith('text/'):
        return True
    if raw in _LOCAL_TEXT_MIME_EXACT:
        return True
    return raw.endswith('+json') or raw.endswith('+xml')


def invalidate_official_read_access_cache():
    with _AUTH_CACHE_LOCK:
        _AUTH_CACHE.clear()


async def verify_official_read_access(official, dataset_id: str, *, ttl_seconds: float = _AUTH_CACHE_TTL_SECONDS) -> None:
    """Re-verify live official read permission for ``dataset_id`` before direct local reads.

    - Never caches 401/403 or missing-dataset denials (Fail-Closed).
    - Preserves official 401 (``official_unauthorized``) and 403 (``dataset_access_denied``).
    - No-op when ``official`` is None or has no active HTTP session (offline/unit-test stubs).
    """
    if official is None:
        return
    verifier = getattr(official, 'verify_dataset_read_access', None)
    if callable(verifier):
        res = verifier(dataset_id)
        if asyncio.iscoroutine(res) or hasattr(res, '__await__'):
            await res
        return
    session = getattr(official, 'session', None)
    base_url = getattr(official, 'url', None)
    if session is None or not base_url:
        return
    ds_canon = uuid_text(dataset_id)
    token_file = getattr(official, 'token_file', None)
    secret = read_secret(token_file) if token_file else ''
    secret_digest = hashlib.sha256(secret.encode('utf-8')).hexdigest()[:16]
    cache_key = (id(official), str(base_url).rstrip('/'), secret_digest)
    now = time.monotonic()
    with _AUTH_CACHE_LOCK:
        cached = _AUTH_CACHE.get(cache_key)
        if cached is not None and cached[0] > now and ds_canon in cached[1]:
            return

    headers = {'X-Api-Key': secret} if secret else {}
    url = str(base_url).rstrip('/') + '/api/v1/datasets'
    try:
        async with session.get(
            url,
            headers=headers,
            allow_redirects=False,
            timeout=aiohttp.ClientTimeout(total=10),
        ) as response:
            if response.status == 401:
                raise PolicyError('official_unauthorized', 401)
            if response.status == 403:
                raise PolicyError('dataset_access_denied', 403)
            if response.status != 200:
                raise PolicyError('official_store_unavailable', 503)
            raw = await response.read()
    except PolicyError:
        raise
    except (aiohttp.ClientError, asyncio.TimeoutError):
        raise PolicyError('official_store_unavailable', 503) from None

    try:
        rows = json.loads(raw)
    except ValueError as exc:
        raise PolicyError('official_store_unavailable', 503) from exc
    if not isinstance(rows, list):
        raise PolicyError('official_store_unavailable', 503)
    authorized = frozenset(
        uuid_text(r.get('id'))
        for r in rows
        if isinstance(r, dict) and r.get('id')
    )
    if ds_canon not in authorized:
        raise PolicyError('dataset_access_denied', 403)
    with _AUTH_CACHE_LOCK:
        if len(_AUTH_CACHE) >= 32:
            _AUTH_CACHE.clear()
        _AUTH_CACHE[cache_key] = (time.monotonic() + max(0.0, float(ttl_seconds)), authorized)


class LocalPathSecurityError(PolicyError):
    """Raised Fail-Closed when a raw_data_location escapes official_root/data or uses symlinks."""

    def __init__(self, code: str = 'document_path_denied', status: int = 403):
        super().__init__(code, status)


def is_file_encryption_enabled(encryption_enabled=None) -> bool:
    if encryption_enabled is not None:
        return bool(encryption_enabled)
    return os.environ.get('FILE_ENCRYPTION_ENABLED', '').strip().lower() in _TRUTHY_FLAGS


def _parse_local_location(raw_data_location) -> str | None:
    """Return local filesystem path string, or None for empty / non-local URIs (e.g., s3://)."""
    if not isinstance(raw_data_location, str):
        return None
    text = raw_data_location.strip()
    if not text:
        return None
    if '://' in text:
        parsed = urlparse(text)
        if parsed.scheme.lower() != 'file':
            return None
        if parsed.netloc not in ('', 'localhost'):
            return None
        path_str = unquote(parsed.path)
        return path_str if path_str else None
    return text


def _relative_parts_within_data_root(raw_path_str: str, official_data) -> tuple[Path, tuple[str, ...]] | None:
    """Validate and split a path relative to ``official_data/data`` without resolving target symlinks.

    Raises ``LocalPathSecurityError`` on path traversal (``..``) or out-of-root absolute paths.
    Returns ``None`` if ``official_data/data`` directory does not exist yet.
    """
    if '\x00' in raw_path_str:
        raise LocalPathSecurityError('document_path_denied', 403)

    raw_data_dir = Path(official_data) / 'data'
    try:
        root_lstat = raw_data_dir.lstat()
    except FileNotFoundError:
        return None
    except OSError as exc:
        raise LocalPathSecurityError('document_path_denied', 403) from exc
    if stat.S_ISLNK(root_lstat.st_mode) or not stat.S_ISDIR(root_lstat.st_mode):
        raise LocalPathSecurityError('document_path_denied', 403)

    resolved_data_dir = raw_data_dir.resolve()
    raw_prefix = str(raw_data_dir).rstrip('/')
    resolved_prefix = str(resolved_data_dir).rstrip('/')

    if raw_path_str.startswith('/'):
        # Check whether the path is prefixed by either the logical or resolved data root
        # (macOS /var -> /private/var symlink in parent directories).
        if raw_path_str.startswith(resolved_prefix + '/'):
            rel_str = raw_path_str[len(resolved_prefix) + 1 :]
        elif raw_path_str.startswith(raw_prefix + '/'):
            rel_str = raw_path_str[len(raw_prefix) + 1 :]
        else:
            raise LocalPathSecurityError('document_path_denied', 403)
    else:
        rel_str = raw_path_str

    raw_segments = rel_str.split('/')
    parts = []
    for seg in raw_segments:
        if seg == '' or seg == '.':
            continue
        if seg == '..' or '\\' in seg or '\x00' in seg:
            raise LocalPathSecurityError('document_path_denied', 403)
        parts.append(seg)
    if not parts:
        raise LocalPathSecurityError('document_path_denied', 403)
    return resolved_data_dir, tuple(parts)


def open_local_data_file_fd(
    raw_data_location,
    official_data,
    *,
    encryption_enabled=None,
) -> tuple[int, os.stat_result] | None:
    """Safely open a regular file under ``official_data/data`` using dir_fd + O_NOFOLLOW + fstat.

    Returns ``(fd, fstat_result)`` on success (caller owns closing ``fd``), ``None`` when
    fallback to official ``/raw`` is appropriate (encryption enabled, non-local URI, or
    missing local file), and raises ``LocalPathSecurityError`` on symlink/traversal attacks.
    """
    if not official_data or is_file_encryption_enabled(encryption_enabled):
        return None
    local_path = _parse_local_location(raw_data_location)
    if not local_path:
        return None
    located = _relative_parts_within_data_root(local_path, official_data)
    if located is None:
        return None
    resolved_root, parts = located

    o_nofollow = getattr(os, 'O_NOFOLLOW', 0)
    o_directory = getattr(os, 'O_DIRECTORY', 0)
    o_cloexec = getattr(os, 'O_CLOEXEC', 0)
    o_nonblock = getattr(os, 'O_NONBLOCK', 0)

    # Pin official_data root directory fd first, then open 'data' via openat(root_fd)
    # to close the root-resolve TOCTOU gap and reject symlinked data directories.
    official_root = Path(official_data)
    try:
        base_fd = os.open(str(official_root.resolve()), os.O_RDONLY | o_directory | o_nofollow | o_cloexec)
    except FileNotFoundError:
        return None
    except OSError as exc:
        raise LocalPathSecurityError('document_path_denied', 403) from exc

    cur_fd = -1
    try:
        try:
            cur_fd = os.open('data', os.O_RDONLY | o_directory | o_nofollow | o_cloexec, dir_fd=base_fd)
        except FileNotFoundError:
            return None
        except OSError as exc:
            raise LocalPathSecurityError('document_path_denied', 403) from exc
    finally:
        os.close(base_fd)

    leaf_fd = -1
    try:
        root_stat = os.fstat(cur_fd)
        if not stat.S_ISDIR(root_stat.st_mode):
            raise LocalPathSecurityError('document_path_denied', 403)
        for segment in parts[:-1]:
            try:
                next_fd = os.open(segment, os.O_RDONLY | o_directory | o_nofollow | o_cloexec, dir_fd=cur_fd)
            except FileNotFoundError:
                return None
            except OSError as exc:
                raise LocalPathSecurityError('document_path_denied', 403) from exc
            os.close(cur_fd)
            cur_fd = next_fd

        try:
            # O_NONBLOCK prevents blocking indefinitely if an attacker plants a FIFO/named pipe.
            leaf_fd = os.open(parts[-1], os.O_RDONLY | o_nofollow | o_cloexec | o_nonblock, dir_fd=cur_fd)
        except FileNotFoundError:
            return None
        except OSError as exc:
            raise LocalPathSecurityError('document_path_denied', 403) from exc
    finally:
        if cur_fd >= 0:
            os.close(cur_fd)

    try:
        info = os.fstat(leaf_fd)
        if not stat.S_ISREG(info.st_mode):
            raise LocalPathSecurityError('document_path_denied', 403)
    except LocalPathSecurityError:
        os.close(leaf_fd)
        raise
    except OSError as exc:
        os.close(leaf_fd)
        raise LocalPathSecurityError('document_path_denied', 403) from exc

    return leaf_fd, info


def _decode_utf8_bounded(raw_bytes: bytes, *, truncated_at_limit: bool) -> str:
    if b'\x00' in raw_bytes:
        raise PolicyError('document_not_utf8_text', 415)
    if not truncated_at_limit:
        try:
            return raw_bytes.decode('utf-8')
        except UnicodeDecodeError as exc:
            raise PolicyError('document_not_utf8_text', 415) from exc
    # When truncated at a byte cap, trim up to 3 trailing continuation/lead bytes so
    # UTF-8 decoding never splits a multi-byte character.
    for trim in range(4):
        end = len(raw_bytes) - trim
        if end < 0:
            break
        try:
            return raw_bytes[:end].decode('utf-8')
        except UnicodeDecodeError:
            continue
    raise PolicyError('document_not_utf8_text', 415)


def read_local_file_body(
    raw_data_location,
    official_data,
    *,
    max_bytes: int = MAX_BODY_BYTES,
    encryption_enabled=None,
    mime_type: str = '',
) -> dict | None:
    """Read local file body strictly bounded to ``max_bytes`` via dir_fd + O_NOFOLLOW."""
    limit = max(1, int(max_bytes))
    if mime_type and not _is_local_text_mime(mime_type):
        return None
    opened = open_local_data_file_fd(
        raw_data_location,
        official_data,
        encryption_enabled=encryption_enabled,
    )
    if opened is None:
        return None
    fd, info = opened
    chunks = bytearray()
    try:
        remaining = limit + 1
        while remaining > 0:
            chunk = os.read(fd, min(65536, remaining))
            if not chunk:
                break
            chunks.extend(chunk)
            remaining -= len(chunk)
    finally:
        os.close(fd)

    truncated = len(chunks) > limit or int(info.st_size) > limit
    bounded = bytes(chunks[:limit])
    text = _decode_utf8_bounded(bounded, truncated_at_limit=truncated)
    return {
        'body': text,
        'truncated': truncated,
        'bytes_total': int(info.st_size),
        'source': 'local_fastpath',
    }


def _strip_session_learning_wrapper(text: str) -> str:
    lines = text.splitlines(keepends=True)
    idx = 0
    while idx < len(lines) and not lines[idx].strip():
        idx += 1
    while idx < len(lines):
        stripped = lines[idx].rstrip('\r\n')
        if stripped.startswith('Lesson-Type: ') or stripped.startswith('State-Key: '):
            idx += 1
            continue
        break
    while idx < len(lines) and not lines[idx].strip():
        idx += 1
    if idx < len(lines) and _SESSION_HEADER_RE.match(lines[idx].rstrip('\r\n')):
        idx += 1
        while idx < len(lines) and not lines[idx].strip():
            idx += 1
        return ''.join(lines[idx:])
    return text


def truncate_utf8_runes(
    text: str,
    *,
    max_runes: int = DEFAULT_PREVIEW_MAX_RUNES,
    max_utf8_bytes: int = DEFAULT_PREVIEW_MAX_UTF8_BYTES,
) -> tuple[str, bool]:
    """Truncate text strictly on Unicode rune boundaries respecting both rune and UTF-8 byte caps."""
    if not text:
        return '', False
    rune_cap = max(0, int(max_runes))
    byte_cap = max(0, int(max_utf8_bytes))
    if rune_cap == 0 or byte_cap == 0:
        return '', bool(text)

    if len(text) <= rune_cap and len(text.encode('utf-8')) <= byte_cap:
        return text, False

    out_chars = []
    used_bytes = 0
    truncated = False
    for ch in text:
        if len(out_chars) >= rune_cap:
            truncated = True
            break
        ch_len = len(ch.encode('utf-8'))
        if used_bytes + ch_len > byte_cap:
            truncated = True
            break
        out_chars.append(ch)
        used_bytes += ch_len
    return ''.join(out_chars), truncated


def read_local_preview_snippet(
    raw_data_location,
    official_data,
    *,
    max_runes: int = DEFAULT_PREVIEW_MAX_RUNES,
    max_utf8_bytes: int = DEFAULT_PREVIEW_MAX_UTF8_BYTES,
    encryption_enabled=None,
) -> tuple[str, bool]:
    """Read a bounded, UTF-8-rune-safe preview snippet without ever returning full body text."""
    try:
        opened = open_local_data_file_fd(
            raw_data_location,
            official_data,
            encryption_enabled=encryption_enabled,
        )
    except LocalPathSecurityError:
        return '', False
    if opened is None:
        return '', False
    fd, info = opened
    try:
        raw = os.read(fd, _PREVIEW_RAW_READ_BYTES)
    except OSError:
        return '', False
    finally:
        os.close(fd)

    if not raw or b'\x00' in raw:
        return '', False
    file_larger = int(info.st_size) > len(raw)
    try:
        decoded = _decode_utf8_bounded(raw, truncated_at_limit=file_larger)
    except PolicyError:
        return '', False
    visible = _strip_session_learning_wrapper(decoded).strip()
    snippet, rune_truncated = truncate_utf8_runes(
        visible,
        max_runes=max_runes,
        max_utf8_bytes=max_utf8_bytes,
    )
    return snippet, bool(rune_truncated or file_larger)


def encode_json_utf8(payload) -> bytes:
    """Canonical compact UTF-8 JSON encoder used for BOTH byte budgeting and wire emission."""
    return json.dumps(payload, ensure_ascii=False, separators=(',', ':')).encode('utf-8')


def json_utf8_response(payload, *, status: int = 200, raw_bytes: bytes | None = None) -> web.Response:
    """Emit the exact UTF-8 JSON bytes measured by ``serialized_json_bytes`` (no ASCII \\uXXXX bloat)."""
    body = raw_bytes if raw_bytes is not None else encode_json_utf8(payload)
    return web.Response(body=body, status=status, content_type='application/json', charset='utf-8')


def serialized_json_bytes(payload) -> int:
    return len(encode_json_utf8(payload))


def fit_items_to_json_budget(
    build_payload,
    items: list[dict],
    *,
    max_json_bytes: int = DEFAULT_LIST_JSON_BUDGET_BYTES,
    preview_key: str = 'preview_text',
    truncated_key: str = 'preview_truncated',
) -> tuple[dict, list[dict], bool]:
    """Enforce actual UTF-8 JSON serialized byte budget (matching ``json_utf8_response`` byte-for-byte).

    First shrinks previews on rune boundaries, then drops trailing items down to 1 item;
    if the sole remaining item still exceeds budget, shrinks oversized free-text fields
    instead of ever returning an empty ``items=[]`` with ``has_more=true``.
    Returns ``(payload, kept_items, items_trimmed)``.
    """
    working = [dict(row) for row in items]
    payload = build_payload(working, False)
    if serialized_json_bytes(payload) <= max_json_bytes:
        return payload, working, False

    for preview_cap in (120, 60, 0):
        shrunk = []
        for row in working:
            copy_row = dict(row)
            if isinstance(copy_row.get(preview_key), str) and copy_row[preview_key]:
                trimmed_preview, was_cut = truncate_utf8_runes(
                    copy_row[preview_key],
                    max_runes=preview_cap,
                    max_utf8_bytes=preview_cap * 3,
                )
                copy_row[preview_key] = trimmed_preview
                if was_cut:
                    copy_row[truncated_key] = True
            shrunk.append(copy_row)
        payload = build_payload(shrunk, False)
        if serialized_json_bytes(payload) <= max_json_bytes:
            return payload, shrunk, False
        working = shrunk

    # Still over budget: drop trailing items down to 1 item.
    while len(working) > 1:
        working.pop()
        payload = build_payload(working, True)
        if serialized_json_bytes(payload) <= max_json_bytes:
            return payload, working, True

    if working:
        trimmed_flag = len(items) > 1
        payload = build_payload(working, trimmed_flag)
        if serialized_json_bytes(payload) <= max_json_bytes:
            return payload, working, trimmed_flag
        # Never drop the sole item into an empty page with has_more=true; shrink free-text display fields first.
        sole = dict(working[0])
        for field_cap in (120, 48, 16, 0):
            for field in ('preview_text', 'label', 'title', 'doc_label', 'doc_name', 'state_key', 'category'):
                val = sole.get(field)
                if isinstance(val, str) and len(val) > field_cap:
                    cut_val, was_cut = truncate_utf8_runes(val, max_runes=field_cap, max_utf8_bytes=field_cap * 3)
                    sole[field] = cut_val
                    if was_cut and truncated_key in sole:
                        sole[truncated_key] = True
            working = [sole]
            payload = build_payload(working, trimmed_flag)
            if serialized_json_bytes(payload) <= max_json_bytes:
                return payload, working, trimmed_flag
        raise PolicyError('response_budget_exceeded', 500)
    return build_payload([], False), [], False


def _lookup_document_location(official_data, dataset_id: str, doc_id: str) -> dict:
    db_path = Path(official_data) / 'system' / 'databases' / 'cognee_db'
    doc_variants = id_variants(doc_id)
    with pooled_official_sqlite_ro(db_path) as con:
        cols = {str(r[1]) for r in con.execute('PRAGMA table_info(data)').fetchall()}
        has_legacy = 'legacy_id' in cols
        row = None
        for variant in doc_variants:
            row = con.execute(
                'SELECT id, dataset_id, raw_data_location, mime_type, content_hash FROM data WHERE id = ? LIMIT 1',
                (variant,),
            ).fetchone()
            if row is not None:
                break
        if row is None and has_legacy:
            for variant in doc_variants:
                row = con.execute(
                    'SELECT id, dataset_id, raw_data_location, mime_type, content_hash FROM data WHERE legacy_id = ? LIMIT 1',
                    (variant,),
                ).fetchone()
                if row is not None:
                    break
    if row is None or uuid_text(row[1]) != uuid_text(dataset_id):
        raise PolicyError('document_not_found', 404)
    return {
        'id': uuid_text(row[0]),
        'dataset_id': uuid_text(row[1]),
        'raw_data_location': str(row[2] or ''),
        'mime_type': str(row[3] or ''),
        'content_hash': str(row[4] or ''),
    }


async def _fetch_official_raw_body(official, dataset_id: str, doc_id: str, *, max_bytes: int) -> dict:
    if official is None or getattr(official, 'session', None) is None:
        raise PolicyError('document_body_unavailable', 503)
    headers = {}
    token_file = getattr(official, 'token_file', None)
    if token_file:
        headers['X-Api-Key'] = read_secret(token_file)
    url = official.url.rstrip('/') + f'/api/v1/datasets/{dataset_id}/data/{doc_id}/raw'
    limit = max(1, int(max_bytes))
    async with official.session.get(
        url,
        headers=headers,
        allow_redirects=False,
        timeout=aiohttp.ClientTimeout(total=30),
    ) as response:
        if response.status == 401:
            raise PolicyError('official_unauthorized', 401)
        if response.status == 403:
            raise PolicyError('dataset_access_denied', 403)
        if response.status == 404:
            raise PolicyError('document_not_found', 404)
        if response.status != 200:
            raise PolicyError('document_body_unavailable', 503)
        buf = bytearray()
        truncated = False
        async for chunk in response.content.iter_chunked(16384):
            if len(buf) + len(chunk) > limit:
                need = limit - len(buf)
                if need > 0:
                    buf.extend(chunk[:need])
                truncated = True
                break
            buf.extend(chunk)
    text = _decode_utf8_bounded(bytes(buf), truncated_at_limit=truncated)
    return {
        'body': text,
        'truncated': truncated,
        'bytes_total': len(buf),
        'source': 'official_fallback',
    }


async def read_document_body_with_fallback(
    official_data,
    dataset_id: str,
    doc_id: str,
    *,
    official=None,
    max_bytes: int = MAX_BODY_BYTES,
    encryption_enabled=None,
) -> dict:
    """Read document body via safe local fast-path, falling back to official ``/raw`` only when needed.

    Security violations (symlink escape, ``..`` traversal, non-regular files, 401/403 official denials)
    fail closed and NEVER fall back.
    """
    await verify_official_read_access(official, dataset_id)
    loc = await asyncio.to_thread(_lookup_document_location, official_data, dataset_id, doc_id)
    local = None
    try:
        local = await asyncio.to_thread(
            read_local_file_body,
            loc['raw_data_location'],
            official_data,
            max_bytes=max_bytes,
            encryption_enabled=encryption_enabled,
            mime_type=loc.get('mime_type', ''),
        )
    except LocalPathSecurityError:
        raise
    except PolicyError as err:
        if err.code == 'document_not_utf8_text' and official is not None and getattr(official, 'session', None) is not None:
            local = None
        else:
            raise
    if local is not None:
        return {
            'id': loc['id'],
            'dataset_id': loc['dataset_id'],
            'mime_type': loc['mime_type'],
            'content_hash': loc['content_hash'],
            **local,
        }
    fallback = await _fetch_official_raw_body(official, loc['dataset_id'], loc['id'], max_bytes=max_bytes)
    return {
        'id': loc['id'],
        'dataset_id': loc['dataset_id'],
        'mime_type': loc['mime_type'],
        'content_hash': loc['content_hash'],
        **fallback,
    }
