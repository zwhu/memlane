"""Verify and serve the pinned Cognee SDK on loopback."""
import argparse
import asyncio
import hashlib
import importlib.machinery
import importlib.util
import json
import os
import re
from pathlib import Path
import stat
import sys
import tarfile
from urllib.parse import urlparse


from . import COMPONENT_MANIFEST, private_write, read_secret, watch_owner
_VERIFY_CACHE_SCHEMA_VERSION = 2
_BYTECODE_SUFFIXES = ('.pyc', '.pyo')


class _SourceOnlyFileLoader(importlib.machinery.SourceFileLoader):
    """Compile strictly from verified .py files, never executing cached .pyc bytecode."""

    def get_code(self, fullname):
        path = Path(self.path)
        if path.suffix != '.py':
            raise ImportError('verified SDK loader requires Python source')
        source_bytes, _ = _read_regular_file_with_stat(path)
        if source_bytes is None:
            raise ImportError('verified SDK source unavailable')
        return self.source_to_code(source_bytes, self.path)


def _install_source_only_import_hook(source):
    """Install a path hook scoped to ``source`` that refuses .pyc/.pyo bytecode loading."""
    source_root = Path(source).resolve(strict=True)

    def _source_only_path_hook(path):
        try:
            resolved = Path(path).resolve(strict=True)
            resolved.relative_to(source_root)
        except (OSError, ValueError):
            raise ImportError('outside verified source root') from None
        return importlib.machinery.FileFinder(
            path,
            (_SourceOnlyFileLoader, importlib.machinery.SOURCE_SUFFIXES),
        )

    sys.path_hooks.insert(0, _source_only_path_hook)
    for cached_path in list(sys.path_importer_cache):
        try:
            Path(cached_path).resolve(strict=True).relative_to(source_root)
        except (OSError, ValueError):
            continue
        sys.path_importer_cache.pop(cached_path, None)
    return _source_only_path_hook

def _read_verify_cache(cache_path):
    if cache_path is None:
        return None
    try:
        parent_info = cache_path.parent.lstat()
        if not stat.S_ISDIR(parent_info.st_mode) or (parent_info.st_mode & 0o077):
            return None
        fd = os.open(cache_path, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
    except OSError:
        return None
    try:
        stream = os.fdopen(fd, 'rb')
    except (OSError, ValueError):
        os.close(fd)
        return None
    try:
        with stream:
            file_info = os.fstat(stream.fileno())
            if not stat.S_ISREG(file_info.st_mode) or (file_info.st_mode & 0o077):
                return None
            data = json.loads(stream.read().decode('utf-8'))
    except (OSError, UnicodeDecodeError, ValueError):
        return None
    return data if isinstance(data, dict) else None

def _write_verify_cache(cache_path, payload):
    if cache_path is None:
        return
    try:
        private_write(cache_path, json.dumps(payload, sort_keys=True, separators=(',', ':')).encode())
    except OSError:
        # An unavailable optional cache cannot prevent full verification.
        return

def _scan_package_files(source, packages):
    """Return ``(py_files_set, has_bytecode)`` or ``None`` on symlink/filesystem error."""
    found = set()
    has_bytecode = False
    for package in packages:
        pkg_root = source / package
        try:
            if not stat.S_ISDIR(pkg_root.lstat().st_mode):
                return None
        except OSError:
            return None
        stack = [(pkg_root, package)]
        while stack:
            current_dir, rel_prefix = stack.pop()
            try:
                with os.scandir(current_dir) as entries:
                    for entry in entries:
                        if entry.is_symlink():
                            return None
                        rel = rel_prefix + '/' + entry.name
                        if entry.name.endswith(_BYTECODE_SUFFIXES):
                            has_bytecode = True
                        elif entry.is_dir(follow_symlinks=False):
                            stack.append((Path(entry.path), rel))
                        elif entry.name.endswith('.py'):
                            if not entry.is_file(follow_symlinks=False):
                                return None
                            found.add(rel)
            except OSError:
                return None
    return found, has_bytecode

def _is_sha256_hex(value):
    return isinstance(value, str) and len(value) == 64 and all(ch in '0123456789abcdef' for ch in value)

def _try_verify_source_cache(source, archive, manifest, cache_path):
    cache = _read_verify_cache(cache_path)
    if cache is None:
        return False
    try:
        archive_info = archive.lstat()
    except OSError:
        return False
    if not stat.S_ISREG(archive_info.st_mode):
        return False
    if type(cache.get('schema_version')) is not int or cache['schema_version'] != _VERIFY_CACHE_SCHEMA_VERSION:
        return False
    packages = list(manifest['packages'])
    packages_set = set(packages)
    if (cache.get('commit') != manifest['commit']
            or cache.get('archive_sha256') != manifest['archive_sha256']
            or cache.get('archive_path') != str(archive)
            or cache.get('source_path') != str(source)
            or cache.get('packages') != packages):
        return False
    archive_stat = cache.get('archive_stat')
    if (not isinstance(archive_stat, dict)
            or set(archive_stat) != {'mtime_ns', 'size'}
            or type(archive_stat['mtime_ns']) is not int
            or type(archive_stat['size']) is not int
            or archive_stat['mtime_ns'] != archive_info.st_mtime_ns
            or archive_stat['size'] != archive_info.st_size):
        return False
    files = cache.get('files')
    py_files = cache.get('py_files')
    if not isinstance(files, dict) or not files or not isinstance(py_files, list):
        return False
    expected_py_files = set()
    checked_dirs = set()
    for rel, meta in files.items():
        if not isinstance(rel, str) or not rel or rel.startswith('/') or '\\' in rel:
            return False
        rel_path = Path(rel)
        if rel_path.is_absolute() or '..' in rel_path.parts or rel_path.as_posix() != rel:
            return False
        if rel != 'pyproject.toml' and (not rel_path.parts or rel_path.parts[0] not in packages_set):
            return False
        if (not isinstance(meta, list) or len(meta) != 3
                or type(meta[0]) is not int or type(meta[1]) is not int or meta[1] < 0
                or not _is_sha256_hex(meta[2])):
            return False
        for parent in rel_path.parents:
            if parent == Path('.'):
                break
            parent_key = parent.as_posix()
            if parent_key not in checked_dirs:
                try:
                    if not stat.S_ISDIR((source / parent).lstat().st_mode):
                        return False
                except OSError:
                    return False
                checked_dirs.add(parent_key)
        content, info = _read_regular_file_with_stat(source / rel_path)
        if (content is None or info is None
                or info.st_mtime_ns != meta[0]
                or info.st_size != meta[1]
                or hashlib.sha256(content).hexdigest() != meta[2]):
            return False
        if rel_path.parts[0] in packages_set and rel_path.suffix == '.py':
            expected_py_files.add(rel)
    if len(py_files) != len(expected_py_files) or any(not isinstance(item, str) for item in py_files):
        return False
    if set(py_files) != expected_py_files:
        return False
    pyproject = source / 'pyproject.toml'
    if (pyproject.exists() or pyproject.is_symlink()) and 'pyproject.toml' not in files:
        return False
    scanned = _scan_package_files(source, packages)
    if scanned is None:
        return False
    found_py_files, has_bytecode = scanned
    # Reject fast-path cache whenever unverified .pyc/.pyo bytecode exists under source.
    if has_bytecode or found_py_files != expected_py_files:
        return False
    return True


def _read_regular_file_with_stat(path):
    try:
        fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
    except OSError:
        return None, None
    try:
        stream = os.fdopen(fd, 'rb')
    except (OSError, ValueError):
        os.close(fd)
        return None, None
    try:
        with stream:
            info = os.fstat(stream.fileno())
            if not stat.S_ISREG(info.st_mode):
                return None, None
            return stream.read(), info
    except OSError:
        return None, None

def _verify_real_parents(source, relative, verified_dirs):
    for parent in relative.parents:
        if parent == Path('.'):
            break
        key = parent.as_posix()
        if key in verified_dirs:
            continue
        try:
            if not stat.S_ISDIR((source / parent).lstat().st_mode):
                return False
        except OSError:
            return False
        verified_dirs.add(key)
    return True


def verify_source(config):
    source_raw = Path(config['official_source'])
    source = source_raw.resolve(strict=True)
    manifest = json.loads(COMPONENT_MANIFEST.read_text())['service']
    archive_raw = Path(config['official_archive'])
    archive = archive_raw.resolve(strict=True)
    if source_raw.is_symlink() or archive_raw.is_symlink():
        raise ValueError('linked official source or archive rejected')
    cache_path = Path(config['verify_cache_path']) if config.get('verify_cache_path') else None
    if _try_verify_source_cache(source, archive, manifest, cache_path):
        return source
    fd = os.open(archive_raw, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
    with os.fdopen(fd, 'rb') as archive_stream:
        archive_info = os.fstat(archive_stream.fileno())
        if not stat.S_ISREG(archive_info.st_mode):
            raise ValueError('official archive must be a regular file')
        digest = hashlib.sha256()
        for chunk in iter(lambda: archive_stream.read(1024 * 1024), b''):
            digest.update(chunk)
        if digest.hexdigest() != manifest['archive_sha256']:
            raise ValueError('fixed official archive digest mismatch')
        archive_stream.seek(0)
        # Source must be the exact upstream package, never a patched service wrapper.
        verified_files = {}
        verified_dirs = set()
        with tarfile.open(fileobj=archive_stream) as bundle:
            names = set()
            for member in bundle:
                parts = Path(member.name).parts[1:]
                if parts == ('pyproject.toml',):
                    extracted = bundle.extractfile(member) if member.isfile() else None
                    target = source / 'pyproject.toml'
                    content, info = _read_regular_file_with_stat(target)
                    if extracted is None or content is None or content != extracted.read():
                        raise ValueError('official package metadata differs from pinned archive')
                    verified_files['pyproject.toml'] = [info.st_mtime_ns, info.st_size, hashlib.sha256(content).hexdigest()]
                if not parts or parts[0] not in manifest['packages']:
                    continue
                if '..' in parts or member.issym() or member.islnk():
                    raise ValueError('invalid path in official archive')
                if Path(*parts).suffix in _BYTECODE_SUFFIXES:
                    raise ValueError('unexpected official Python bytecode')
                if not member.isfile():
                    continue
                relative = Path(*parts)
                names.add(relative)
                target = source / relative
                content, info = (None, None) if not _verify_real_parents(source, relative, verified_dirs) else _read_regular_file_with_stat(target)
                if content is None or content != bundle.extractfile(member).read():
                    raise ValueError('official package differs from pinned archive: ' + str(relative))
                verified_files[relative.as_posix()] = [info.st_mtime_ns, info.st_size, hashlib.sha256(content).hexdigest()]
    scanned = _scan_package_files(source, manifest['packages'])
    if scanned is None:
        raise ValueError('invalid official package tree')
    found, has_bytecode = scanned
    if has_bytecode:
        raise ValueError('unexpected official Python bytecode')
    if found != {p.as_posix() for p in names if p.suffix == '.py'}:
        raise ValueError('unexpected official Python module')
    pyproject = source / 'pyproject.toml'
    if (pyproject.exists() or pyproject.is_symlink()) and 'pyproject.toml' not in verified_files:
        raise ValueError('unexpected official package metadata')
    if cache_path is not None:
        _write_verify_cache(cache_path, {
            'schema_version': _VERIFY_CACHE_SCHEMA_VERSION,
            'commit': manifest['commit'],
            'archive_path': str(archive),
            'archive_stat': {'mtime_ns': archive_info.st_mtime_ns, 'size': archive_info.st_size},
            'archive_sha256': manifest['archive_sha256'],
            'source_path': str(source),
            'packages': list(manifest['packages']),
            'files': verified_files,
            'py_files': sorted(p.as_posix() for p in names if p.suffix == '.py'),
        })
    return source


def setup_request_allowed(method, path):
    if '//' in path or any(part in ('.', '..') for part in path.split('/')):
        return False
    if (method, path) in {('GET', '/internal/v1/health'), ('GET', '/internal/v1/model/status'),
                           ('POST', '/internal/v1/model/apply')}:
        return True
    if method == 'GET' and path.rstrip('/') in ('/health', '/api/v1/users/me'):
        return True
    dataset_path = path.rstrip('/')
    if dataset_path == '/api/v1/datasets':
        return method in ('GET', 'POST')
    if method == 'GET' and dataset_path.startswith('/api/v1/datasets/'):
        return (dataset_path in ('/api/v1/datasets/status', '/api/v1/datasets/status/progress')
                or re.fullmatch(r'/api/v1/datasets/[^/]+/(?:data(?:/count)?|processing-status)', dataset_path) is not None)
    if path.startswith('/api/v1/permissions/'):
        return method in ('GET', 'POST', 'PUT', 'DELETE')
    if path.rstrip('/') == '/api/v1/agents/create':
        return method == 'POST'
    if path.rstrip('/') == '/api/v1/agents':
        return method == 'GET'
    return False


class SetupOnlyMiddleware:
    def __init__(self, app, is_setup_only=lambda: True):
        self.app, self.is_setup_only = app, is_setup_only

    async def __call__(self, scope, receive, send):
        if not self.is_setup_only():
            await self.app(scope, receive, send)
            return
        kind = scope['type']
        if kind == 'websocket':
            await send({'type': 'websocket.close', 'code': 1008})
            return
        if kind == 'http' and scope['method'] == 'GET' and scope['path'] in ('/health', '/health/'):
            # Uvicorn serves requests only after the SDK lifespan/migrations
            # complete. Setup readiness must not initialize vector/graph engines
            # through the normal comprehensive health route. Owner identity is
            # checked separately through the authenticated users/me endpoint.
            body = b'{"status":"setup_ready","generation":"unconfigured","checks":"liveness_only"}'
            await send({'type': 'http.response.start', 'status': 200,
                        'headers': [(b'content-type', b'application/json'),
                                    (b'cache-control', b'no-store')]})
            await send({'type': 'http.response.body', 'body': body})
            return
        if kind == 'http' and not setup_request_allowed(scope['method'], scope['path']):
            body = b'{"detail":"generation_not_configured"}'
            await send({'type': 'http.response.start', 'status': 503,
                        'headers': [(b'content-type', b'application/json'),
                                    (b'cache-control', b'no-store')]})
            await send({'type': 'http.response.body', 'body': body})
            return
        await self.app(scope, receive, send)


def scrub_environment():
    prefixes = ('LLM_', 'EMBEDDING_', 'COGNEE_', 'MEMLANE_', 'FALLBACK_', 'TRANSCRIPTION_',
                'IMAGE_TRANSCRIBE_', 'OPENAI_', 'ANTHROPIC_', 'GEMINI_', 'GOOGLE_', 'OLLAMA_',
                'FASTAPI_USERS_', 'LITELLM_', 'DB_', 'GRAPH_DATABASE_', 'VECTOR_DB_', 'VECTOR_DATABASE_',
                'AWS_', 'AZURE_', 'HF_', 'TRANSFORMERS_', 'DEFAULT_USER_', 'RELATIONAL_DB_', 'DYNAMIC_')
    for key in list(os.environ):
        if key.upper().startswith(prefixes) or key.upper() in {
            'CHUNKS_PER_BATCH', 'STRUCTURED_OUTPUT_FRAMEWORK', 'IMPROVE_STAGES_DISABLED',
            'REQUIRE_AUTHENTICATION', 'ENABLE_BACKEND_ACCESS_CONTROL', 'CACHE_CONFIG',
        }:
            del os.environ[key]


def verify_ladybug_abi():
    ladybug = importlib.util.find_spec('ladybug')
    locations = ladybug.submodule_search_locations if ladybug else None
    # Generic .so is not evidence that a build targets this interpreter.
    import sysconfig
    suffix = sysconfig.get_config_var('EXT_SUFFIX')
    if not locations or not suffix or not any(
        (Path(location) / ('_lbug' + suffix)).is_file() for location in locations
    ):
        raise ValueError('existing Ladybug native dependency unavailable for this Python')


def load_internal_token(config):
    path = config.get('internal_token_file')
    if path is None:
        raise ValueError('internal_token_file is required')
    return read_secret(path)


def _configure_store(config, root):
    allowed = {'EMBEDDING_PROVIDER', 'EMBEDDING_MODEL', 'EMBEDDING_DIMENSIONS', 'EMBEDDING_MAX_TOKENS',
               'EMBEDDING_ENDPOINT', 'EMBEDDING_API_KEY', 'DEFAULT_USER_EMAIL', 'DEFAULT_USER_PASSWORD',
               'HF_HOME', 'HF_HUB_OFFLINE', 'ACCEPT_LOCAL_FILE_PATH',
               'FASTAPI_USERS_JWT_SECRET', 'FASTAPI_USERS_RESET_PASSWORD_TOKEN_SECRET',
               'FASTAPI_USERS_VERIFICATION_TOKEN_SECRET'}
    private = json.loads(read_secret(config['service_environment_file'])) if config.get('service_environment_file') else {}
    if not isinstance(private, dict) or set(private) - allowed:
        raise ValueError('unsupported service environment keys')
    for name in ('home', 'system', 'system/databases', 'data', 'cache', 'logs'):
        path = root / name
        if path.is_symlink():
            raise ValueError('linked official store destination rejected')
        path.mkdir(mode=0o700, exist_ok=True)
    os.environ.update({key: str(value) for key, value in private.items()})
    os.environ.update(HOME=str(root / 'home'), SYSTEM_ROOT_DIRECTORY=str(root / 'system'),
                      DATA_ROOT_DIRECTORY=str(root / 'data'), CACHE_ROOT_DIRECTORY=str(root / 'cache'),
                      DB_PATH=str(root / 'system/databases'), CACHE_BACKEND='sqlite', CACHING='true', SESSION_TTL_SECONDS='0',
                      LOGS_ROOT_DIRECTORY=str(root / 'logs'), COGNEE_LOGS_DIR=str(root / 'logs'),
                      TELEMETRY_DISABLED='true', COGNEE_TRACING_ENABLED='false', PYTHON_DOTENV_DISABLED='1',
                      ENABLE_BACKEND_ACCESS_CONTROL='true', REQUIRE_AUTHENTICATION='true',
                      COGNEE_AGENT_MODE='false', DISTILLATION_MODE='coding',
                      IMPROVE_STAGES_DISABLED='persist_agent_traces', LITELLM_LOCAL_MODEL_COST_MAP='true')


async def _serve(config, root, token):
    import uvicorn
    from cognee.api.client import app
    from .engine_api import install_engine_api
    from .handoff import ModelHandoffController
    from .http_policy import generation_http_policy
    from .language_egress import rewrite_llm_request

    port = config['port'] if 'port' in config else urlparse(config['service_url']).port
    if type(port) is not int or not 1 <= port <= 65535:
        raise ValueError('invalid runner port')
    async with generation_http_policy(request_rewriter=rewrite_llm_request, enable_capacity=True) as gate:
        handoff = ModelHandoffController(gate=gate)
        try:
            generation = config.get('generation_config')
            if generation is not None:
                receipt = await handoff.apply('startup', generation, output_language=config.get('output_language'))
                if config.get('generation_applied_file'):
                    private_write(config['generation_applied_file'], json.dumps({
                        'pid': os.getpid(), 'digest': receipt['model_digest'],
                        'mode': generation.get('mode'), 'model': handoff.environment.get('LLM_MODEL'),
                    }).encode())
            handoff.install_context_middleware(app)
            install_engine_api(app, handoff=handoff, store_path=str(root), token=token)
            app.add_middleware(SetupOnlyMiddleware, is_setup_only=lambda: handoff.mode == 'setup_ready')
            await uvicorn.Server(uvicorn.Config(app, host='127.0.0.1', port=port,
                access_log=False, timeout_graceful_shutdown=15)).serve()
        finally:
            await handoff.close()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--config', required=True, type=Path)
    args = parser.parse_args()
    stopped = watch_owner()
    try:
        config = json.loads(args.config.read_text())
        token = load_internal_token(config)
        source = verify_source(config)
        root = Path(config['official_data']).resolve(strict=True)
        scrub_environment()
        _configure_store(config, root)
        if config.get('prepare_json_extension'):
            from .ladybug_extension import prepare_json_extension
            prepare_json_extension(root / 'home')
        sys.dont_write_bytecode = True
        _install_source_only_import_hook(source)
        sys.path.insert(0, str(source))
        verify_ladybug_abi()
        asyncio.run(_serve(config, root, token))
    finally:
        stopped.set()


if __name__ == '__main__':
    main()
