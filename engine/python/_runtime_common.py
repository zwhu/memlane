"""Shared private-file, credential-cache, hash, SQLite, and manifest helpers."""
import hashlib
import json
import os
from pathlib import Path
import sqlite3
import tempfile
import uuid

from project_policy import PolicyError

LEGACY_MANAGED_PLUGIN_MANIFESTS = (
    'managed-project-plugin-pre-cursor-policy.json',
)

KNOWLEDGE_NODE_SETS = frozenset({'session_learnings', 'user_context', 'memory'})
DERIVED_NODE_SETS = frozenset({
    'user_sessions_from_cache',
    'agent_trace_feedbacks',
    'session_learnings',
})


def private_write(path, data):
    if isinstance(data, str):
        data = data.encode('utf-8')
    path = Path(path)
    path.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
    fd, name = tempfile.mkstemp(prefix='.' + path.name + '-', dir=path.parent)
    try:
        os.fchmod(fd, 0o600)
        with os.fdopen(fd, 'wb') as stream:
            stream.write(data)
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(name, path)
        directory = os.open(path.parent, os.O_RDONLY)
        try:
            os.fsync(directory)
        finally:
            os.close(directory)
    finally:
        if os.path.exists(name):
            os.unlink(name)


def read_secret(path):
    path = Path(path)
    if path.is_symlink() or path.stat().st_mode & 0o077:
        raise PolicyError('private_credential_required', 503)
    value = path.read_text().strip()
    if not value:
        raise PolicyError('credential_unavailable', 503)
    return value


class CachedSecretReader:
    """Cache private secret reads by (mtime_ns, size, mode), re-checking lstat each call."""

    def __init__(self):
        self._entries = {}

    def read(self, path):
        target = Path(path)
        if target.is_symlink():
            raise PolicyError('private_credential_required', 503)
        info = target.stat()
        if info.st_mode & 0o077:
            raise PolicyError('private_credential_required', 503)
        key = str(target)
        cached = self._entries.get(key)
        stamp = (info.st_mtime_ns, info.st_size, info.st_mode)
        if cached is not None and cached[0] == stamp:
            return cached[1]
        value = target.read_text().strip()
        if not value:
            raise PolicyError('credential_unavailable', 503)
        self._entries[key] = (stamp, value)
        return value


def bytes_sha256(data):
    return hashlib.sha256(data).hexdigest()


def file_sha256(path):
    digest = hashlib.sha256()
    with Path(path).open('rb') as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b''):
            digest.update(chunk)
    return digest.hexdigest()


def replace_exact(text, old, new, *, label='anchor'):
    if text.count(old) != 1:
        raise ValueError(f'{label} changed')
    return text.replace(old, new, 1)


def match_managed_plugin_manifest(engine_dir, receipt):
    engine_dir = Path(engine_dir)
    expected = json.loads((engine_dir / 'managed-project-plugin.json').read_text())
    if receipt == expected:
        return expected, True
    for name in LEGACY_MANAGED_PLUGIN_MANIFESTS:
        legacy_path = engine_dir / name
        if legacy_path.is_file() and receipt == json.loads(legacy_path.read_text()):
            return receipt, False
    raise PolicyError('managed_plugin_input_mismatch', 503)


def uuid_text(value):
    if not value:
        return ''
    text = str(value).strip()
    if not text:
        return ''
    try:
        return str(uuid.UUID(text))
    except ValueError:
        return text


def uuid_hex(value):
    try:
        return uuid.UUID(str(value)).hex
    except (ValueError, AttributeError):
        return ''


def id_variants(value):
    text = str(value or '').strip()
    if not text:
        return ()
    variants = []
    for candidate in (text, uuid_text(text), uuid_hex(text)):
        if candidate and candidate not in variants:
            variants.append(candidate)
    return tuple(variants)


def sql_in_clause(variants):
    if len(variants) == 1:
        return '= ?', variants
    return f"IN ({','.join('?' for _ in variants)})", variants


def iso_text(value):
    if not value:
        return ''
    text = str(value).strip()
    if len(text) >= 19 and text[10] == ' ':
        return text[:10] + 'T' + text[11:]
    return text


def parse_json(value):
    if isinstance(value, (dict, list)):
        return value
    if not isinstance(value, str) or not value.strip():
        return None
    try:
        return json.loads(value)
    except ValueError:
        return None


def decode_node_set(raw_value):
    parsed = parse_json(raw_value)
    if isinstance(parsed, str):
        nested = parse_json(parsed)
        if nested is not None:
            return nested
    return parsed


def _extract_doc_meta(meta_or_doc):
    if not isinstance(meta_or_doc, dict):
        return None
    if isinstance(meta_or_doc.get('externalMetadata'), dict):
        return meta_or_doc['externalMetadata']
    if isinstance(meta_or_doc.get('external_metadata'), dict):
        return meta_or_doc['external_metadata']
    return meta_or_doc


def node_sets_from_meta(meta_or_doc):
    meta = _extract_doc_meta(meta_or_doc)
    if not isinstance(meta, dict):
        return []
    raw = meta.get('node_set')
    if isinstance(raw, str):
        return [raw]
    if isinstance(raw, list):
        return [item for item in raw if isinstance(item, str)]
    return []


def is_knowledge_doc(meta_or_doc):
    meta = _extract_doc_meta(meta_or_doc)
    if not isinstance(meta, dict):
        return False
    if str(meta.get('common_category') or '').strip():
        return True
    return any(tag in KNOWLEDGE_NODE_SETS for tag in node_sets_from_meta(meta))


def is_derived_material(meta_or_doc):
    return any(tag in DERIVED_NODE_SETS for tag in node_sets_from_meta(meta_or_doc))


def sqlite_table_columns(con, table):
    return {row[1] for row in con.execute(f'PRAGMA table_info("{table}")')}


def verify_sqlite_schema(con, required_schema):
    """Verify required tables and columns exist on an open SQLite connection."""
    existing_tables = {
        row[0] for row in con.execute("SELECT name FROM sqlite_master WHERE type='table'")
    }
    for table, required_cols in required_schema.items():
        if table not in existing_tables:
            raise sqlite3.OperationalError(f'missing required table: {table}')
        cols = sqlite_table_columns(con, table)
        missing = set(required_cols) - cols
        if missing:
            raise sqlite3.OperationalError(
                f'missing required columns on {table}: {sorted(missing)}'
            )
