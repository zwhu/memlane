"""Prepare engine components only; never initialize or replace a content store."""
import fcntl
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import tarfile
import tempfile
import urllib.request

ROOT = Path(__file__).resolve().parents[1]
PYTHON_VERSION = '3.12.12'
REQUIREMENTS = ROOT / 'engine/python/requirements.lock'
MANIFEST = ROOT / 'engine/coding-memory-components.json'
DAILY_CONFIG = ROOT / '.local/daily/runtime.json'


def digest(path):
    result = hashlib.sha256()
    with Path(path).open('rb') as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b''):
            result.update(chunk)
    return result.hexdigest()


def private_json(path, value):
    with tempfile.NamedTemporaryFile(mode='w', dir=path.parent, delete=False) as stream:
        temporary = Path(stream.name)
        try:
            os.fchmod(stream.fileno(), 0o600)
            json.dump(value, stream, indent=2)
            stream.write('\n')
            stream.flush()
            os.fsync(stream.fileno())
            os.replace(temporary, path)
        finally:
            temporary.unlink(missing_ok=True)


def validate_root(path):
    path = Path(path).expanduser().absolute()
    if DAILY_CONFIG.exists():
        data = Path(json.loads(DAILY_CONFIG.read_text())['official_data']).expanduser().resolve()
        canonical = path.resolve()
        if canonical == data or data in canonical.parents or canonical in data.parents:
            raise ValueError('component root must be separate from the existing daily content store')
    for ancestor in (path, *path.parents):
        if ancestor.is_symlink():
            raise ValueError('component root must not traverse symlinks')
    if path.exists():
        if not path.is_dir() or path.stat().st_mode & 0o077:
            raise ValueError('component root must be a private directory (0700)')
        for child in path.iterdir():
            # Python distributions and venvs contain legitimate internal links;
            # mutable preparation files and top-level destinations must not.
            if child.parent == path and child.is_symlink():
                raise ValueError('component destination must not be a symlink')
    return path


def download_archive(path, service):
    if path.exists():
        if digest(path) != service['archive_sha256']:
            raise ValueError('existing official archive digest mismatch; retained')
        return
    with tempfile.NamedTemporaryFile(dir=path.parent, delete=False) as stream:
        temporary = Path(stream.name)
        try:
            cached = ROOT / '.cache/coding-memory-components' / (service.get('commit', '') + '.tar.gz')
            if cached.is_file() and digest(cached) == service['archive_sha256']:
                with cached.open('rb') as source:
                    shutil.copyfileobj(source, stream)
            else:
                with urllib.request.urlopen(service['archive_url'], timeout=60) as response:
                    shutil.copyfileobj(response, stream)
            stream.flush()
            if digest(temporary) != service['archive_sha256']:
                raise ValueError('downloaded official archive digest mismatch')
            os.replace(temporary, path)
        finally:
            temporary.unlink(missing_ok=True)


def extract_source(archive, destination):
    # Publish only a complete tree. Reject links and traversal before extracting.
    with tempfile.TemporaryDirectory(dir=destination.parent, prefix='.source-') as staging:
        staging = Path(staging)
        with tarfile.open(archive) as bundle:
            members = bundle.getmembers()
            roots = set()
            for member in members:
                parts = Path(member.name).parts
                if not parts or member.name.startswith('/') or '..' in parts:
                    raise ValueError('unsafe official source archive')
                roots.add(parts[0])
            if len(roots) != 1:
                raise ValueError('official source archive must have one root')
            for member in members:
                relative = Path(*Path(member.name).parts[1:])
                # The pinned repo includes a .claude/skills symlink unrelated to
                # the service. Extract only the official runtime package trees.
                if not relative.parts or relative.parts[0] not in ('cognee', 'cognee_db_workers', 'kuzu', 'pyproject.toml'):
                    continue
                if not (member.isfile() or member.isdir()):
                    raise ValueError('unsafe official source archive')
                target = staging / relative
                if member.isdir():
                    target.mkdir(parents=True, exist_ok=True)
                else:
                    target.parent.mkdir(parents=True, exist_ok=True)
                    with bundle.extractfile(member) as stream, target.open('xb') as output:
                        shutil.copyfileobj(stream, output)
        staging.rename(destination)


def verify_source(root, service):
    from official_service_dev import verify_source as verify
    verify({'official_source': str(root / 'source'), 'official_archive': str(root / 'service.tar.gz')})


def run(command, *, env, cwd=None):
    subprocess.run(command, env=env, cwd=cwd, check=True)


def prepare(directory, uv=None):
    root = validate_root(directory)
    service = json.loads(MANIFEST.read_text())['service']
    inputs = {'schema': 1, 'python_version': PYTHON_VERSION,
              'service_commit': service['commit'], 'archive_sha256': service['archive_sha256'],
              'requirements_sha256': digest(REQUIREMENTS)}
    if root.exists() and not (root / 'inputs.json').exists() and any(root.iterdir()):
        raise ValueError('nonempty unowned component directory; retained unchanged')
    root.mkdir(mode=0o700, parents=True, exist_ok=True)
    lock_path = root / '.prepare.lock'
    fd = os.open(lock_path, os.O_CREAT | os.O_RDWR | os.O_NOFOLLOW, 0o600)
    with os.fdopen(fd, 'a+') as lock:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        marker = root / 'inputs.json'
        if marker.exists():
            if json.loads(marker.read_text()) != inputs:
                raise ValueError('component inputs changed; existing components retained')
        else:
            private_json(marker, inputs)
        executable = uv or shutil.which('uv')
        if not executable:
            raise ValueError('managed uv installer unavailable; no host Python fallback')
        # Ignore user pip/uv/Python overrides. Reuse the project's reproducible
        # cache while keeping interpreters and packages in the owned directory.
        env = {k: v for k, v in os.environ.items()
               if not k.startswith(('UV_', 'PIP_', 'PYTHON')) and k != 'VIRTUAL_ENV'}
        cache = ROOT / '.cache/uv'
        env.update(UV_PYTHON_INSTALL_DIR=str(root / 'python'), UV_CACHE_DIR=str(cache),
                   UV_NO_CONFIG='1', PYTHONDONTWRITEBYTECODE='1')
        installer = [executable, '--no-config', '--cache-dir', str(cache)]
        state = {'schema': 1, 'status': 'preparing', 'completed': []}
        python = root / 'venv/bin/python'
        steps = [
            ('python', lambda: prepare_python(root, installer, env)),
            ('dependencies', lambda: run([*installer, 'pip', 'sync', '--python', str(python),
                 '--require-hashes', '--build-constraints', str(REQUIREMENTS), str(REQUIREMENTS)], env=env)),
            ('archive', lambda: download_archive(root / 'service.tar.gz', service)),
            ('source', lambda: prepare_source(root, service)),
            ('probe', lambda: probe(python, root / 'source', env)),
        ]
        for name, operation in steps:
            state.update(step=name, status='preparing')
            private_json(root / 'status.json', state)
            try:
                operation()
            except BaseException as error:
                state.update(status='failed', error_type=type(error).__name__)
                private_json(root / 'status.json', state)
                raise
            state['completed'].append(name)
        state.update(status='components_ready', python=str(python),
                     official_source=str(root / 'source'), official_archive=str(root / 'service.tar.gz'),
                     service_commit=service['commit'], storage_initialized=False)
        private_json(root / 'status.json', state)
        return state


def prepare_python(root, installer, env):
    python = root / 'venv/bin/python'
    if not python.exists():
        run([*installer, 'python', 'install', PYTHON_VERSION, '--no-bin', '--no-registry'], env=env)
        run([*installer, 'venv', '--no-project', '--allow-existing', '--managed-python', '--python', PYTHON_VERSION,
             str(root / 'venv')], env=env)
    run([str(python), '-I', '-c',
         'import sys; assert sys.version_info[:3] == (3,12,12); assert sys.prefix != sys.base_prefix'], env=env)


def prepare_source(root, service):
    if not (root / 'source').exists():
        extract_source(root / 'service.tar.gz', root / 'source')
    verify_source(root, service)


def probe(python, source, env):
    # Capability checks have no content-store side effects and make no model calls.
    code = """
import importlib.metadata, importlib.util, sqlite3, sys
sys.path.insert(0, sys.argv[1])
import ladybug, lancedb, fastembed, uvicorn
from pathlib import Path
assert importlib.metadata.version('cognee') == '1.6.7'
assert Path(importlib.util.find_spec('cognee').origin).is_relative_to(Path(sys.argv[1]))
with sqlite3.connect(':memory:') as db:
    db.execute('create table readiness (id integer primary key)')
    assert db.execute('select count(*) from readiness').fetchone()[0] == 0
"""
    with tempfile.TemporaryDirectory(dir=source.parent, prefix='.probe-') as temporary:
        isolated = {**env, 'HOME': temporary, 'XDG_CACHE_HOME': temporary + '/cache',
                    'HF_HOME': temporary + '/hf', 'COGNEE_LOGS_DIR': temporary + '/logs',
                    'LOGS_ROOT_DIRECTORY': temporary + '/logs', 'TELEMETRY_DISABLED': 'true'}
        run([str(python), '-I', '-c', code, str(source)], env=isolated, cwd=temporary)
