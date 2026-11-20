"""Shared private-file, credential-cache, hash, and manifest helpers."""
import hashlib
import json
import os
from pathlib import Path
import tempfile

from project_policy import PolicyError

LEGACY_MANAGED_PLUGIN_MANIFESTS = (
    'managed-project-plugin-pre-recall.json',
    'managed-project-plugin-pre-environment.json',
    'managed-project-plugin-pre-convergence.json',
    'managed-project-plugin-pre-empty-common.json',
)


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
        if receipt == json.loads((engine_dir / name).read_text()):
            return receipt, False
    raise PolicyError('managed_plugin_input_mismatch', 503)
