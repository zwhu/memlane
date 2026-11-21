"""Run the verified official Cognee API with Memlane external runtime extensions."""
import argparse
import asyncio
import hashlib
import importlib.machinery
import importlib.util
import json
import os
from pathlib import Path
import stat
import sys
import signal
import time
import tarfile
from contextlib import AsyncExitStack
from urllib.parse import urlparse


COMPONENT_MANIFEST = Path(__file__).resolve().parents[1] / 'coding-memory-components.json'
_VERIFY_CACHE_SCHEMA_VERSION = 2
_BYTECODE_SUFFIXES = ('.pyc', '.pyo')


class _SourceOnlyFileLoader(importlib.machinery.SourceFileLoader):
    """Compile strictly from verified .py files, never executing cached .pyc bytecode."""

    def get_code(self, fullname):
        source_bytes = self.get_data(self.path)
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


def _source_verify_cache_path(config):
    control = config.get('project_control')
    if isinstance(control, dict) and control.get('registry_file'):
        return Path(control['registry_file']).parent / 'engine-components' / 'source-verify-cache.json'
    if config.get('official_data'):
        return Path(config['official_data']) / 'memlane-state' / 'source-verify-cache.json'
    return None


def _read_verify_cache(cache_path):
    if cache_path is None:
        return None
    try:
        parent_info = cache_path.parent.lstat()
        if not stat.S_ISDIR(parent_info.st_mode) or (parent_info.st_mode & 0o077):
            return None
        fd = os.open(cache_path, os.O_RDONLY | os.O_NOFOLLOW)
    except OSError:
        return None
    try:
        stream = os.fdopen(fd, 'rb')
    except Exception:
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
    parent = cache_path.parent
    try:
        if parent.is_symlink():
            return
        if parent.exists():
            if not stat.S_ISDIR(parent.lstat().st_mode):
                return
        else:
            parent.mkdir(mode=0o700, parents=True, exist_ok=True)
        parent_info = parent.lstat()
        if not stat.S_ISDIR(parent_info.st_mode):
            return
        if parent_info.st_mode & 0o077:
            os.chmod(parent, 0o700)
        from _runtime_common import private_write
        private_write(cache_path, json.dumps(payload, sort_keys=True, separators=(',', ':')).encode('utf-8'))
    except OSError:
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
                        if entry.is_dir(follow_symlinks=False):
                            stack.append((Path(entry.path), rel))
                        elif entry.name.endswith('.py'):
                            if not entry.is_file(follow_symlinks=False):
                                return None
                            found.add(rel)
                        elif entry.name.endswith(_BYTECODE_SUFFIXES):
                            has_bytecode = True
            except OSError:
                return None
    return found, has_bytecode


def _is_sha256_hex(value):
    return isinstance(value, str) and len(value) == 64 and all(ch in '0123456789abcdef' for ch in value)


def _try_verify_source_cache(source_raw, source, archive_raw, archive, manifest, cache_path):
    cache = _read_verify_cache(cache_path)
    if cache is None:
        return False
    try:
        raw_archive_info = archive_raw.lstat()
        resolved_archive_info = archive.lstat()
        raw_source_info = source_raw.lstat()
        resolved_source_info = source.lstat()
    except OSError:
        return False
    if (not stat.S_ISREG(raw_archive_info.st_mode)
            or not stat.S_ISREG(resolved_archive_info.st_mode)
            or not stat.S_ISDIR(raw_source_info.st_mode)
            or not stat.S_ISDIR(resolved_source_info.st_mode)):
        return False
    if (raw_archive_info.st_ino, raw_archive_info.st_dev) != (resolved_archive_info.st_ino, resolved_archive_info.st_dev):
        return False
    if (raw_source_info.st_ino, raw_source_info.st_dev) != (resolved_source_info.st_ino, resolved_source_info.st_dev):
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
            or archive_stat['mtime_ns'] != resolved_archive_info.st_mtime_ns
            or archive_stat['size'] != resolved_archive_info.st_size):
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
        fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW)
    except OSError:
        return None, None
    try:
        stream = os.fdopen(fd, 'rb')
    except Exception:
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
    cache_path = _source_verify_cache_path(config)
    if _try_verify_source_cache(source_raw, source, archive_raw, archive, manifest, cache_path):
        return source
    with archive.open('rb') as archive_stream:
        archive_info = os.fstat(archive_stream.fileno())
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
                if not parts or parts[0] not in manifest['packages'] or not member.isfile(): continue
                relative = Path(*parts)
                names.add(relative)
                target = source / relative
                content, info = (None, None) if not _verify_real_parents(source, relative, verified_dirs) else _read_regular_file_with_stat(target)
                if content is None or content != bundle.extractfile(member).read():
                    raise ValueError('official package differs from pinned archive: ' + str(relative))
                verified_files[relative.as_posix()] = [info.st_mtime_ns, info.st_size, hashlib.sha256(content).hexdigest()]
    for package in manifest['packages']:
        for target in (source / package).rglob('*.py'):
            if target.relative_to(source) not in names:
                raise ValueError('unexpected official Python module')
    scanned = _scan_package_files(source, manifest['packages'])
    has_bytecode = scanned is None or scanned[1]
    if (cache_path is not None
            and not has_bytecode
            and not archive_raw.is_symlink()
            and not source_raw.is_symlink()
            and stat.S_ISREG(archive_info.st_mode)):
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


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--config', required=True, type=Path)
    args = parser.parse_args()
    from owner_lifecycle import watch_owner
    watch_owner()
    config = json.loads(args.config.read_text())
    from first_install_mode import first_install_setup
    setup_only = first_install_setup(config)
    root = Path(config['official_data']).resolve(strict=True)
    source = verify_source(config)
    for variable in list(os.environ):
        if variable.upper().startswith(('LLM_', 'EMBEDDING_', 'COGNEE_', 'MEMLANE_', 'FALLBACK_', 'TRANSCRIPTION_', 'IMAGE_TRANSCRIBE_',
                                        'OPENAI_', 'ANTHROPIC_', 'GEMINI_', 'GOOGLE_', 'OLLAMA_')):
            del os.environ[variable]
    allowed = {'EMBEDDING_PROVIDER', 'EMBEDDING_MODEL', 'EMBEDDING_DIMENSIONS', 'EMBEDDING_MAX_TOKENS',
               'EMBEDDING_ENDPOINT', 'EMBEDDING_API_KEY', 'DEFAULT_USER_EMAIL', 'DEFAULT_USER_PASSWORD',
               'HF_HOME', 'HF_HUB_OFFLINE', 'ACCEPT_LOCAL_FILE_PATH',
               'FASTAPI_USERS_JWT_SECRET', 'FASTAPI_USERS_RESET_PASSWORD_TOKEN_SECRET', 'FASTAPI_USERS_VERIFICATION_TOKEN_SECRET'}
    private = json.loads(Path(config['service_environment_file']).read_text())
    if set(private) - allowed:
        raise ValueError('unsupported service environment keys')
    environment = {key: str(value) for key, value in private.items()}
    environment.update(HOME=str(root / 'home'),
                       SYSTEM_ROOT_DIRECTORY=str(root / 'system'), DATA_ROOT_DIRECTORY=str(root / 'data'),
                       CACHE_ROOT_DIRECTORY=str(root / 'cache'), CACHE_BACKEND='sqlite', CACHING='true',
                       DB_PATH=str(root / 'system/databases'),
                       LOGS_ROOT_DIRECTORY=str(root / 'logs'), COGNEE_LOGS_DIR=str(root / 'logs'),
                       TELEMETRY_DISABLED='true', COGNEE_TRACING_ENABLED='false', PYTHON_DOTENV_DISABLED='1',
                       ENABLE_BACKEND_ACCESS_CONTROL='true', REQUIRE_AUTHENTICATION='true',
                       COGNEE_AGENT_MODE='false', DISTILLATION_MODE='coding',
                       # User chose knowledge availability over full tool-output
                       # graph coverage. Raw session traces and coding distill
                       # remain upstream-owned; no filtering or cache deletion.
                       IMPROVE_STAGES_DISABLED='persist_agent_traces')
    if setup_only:
        profile, generation_environment, generation_digest = {'mode': 'unconfigured', 'model': ''}, {}, None
        environment['LITELLM_LOCAL_MODEL_COST_MAP'] = 'true'
    else:
        from generation_config import runtime_generation
        profile, generation_environment, generation_digest = runtime_generation(config)
    environment.update(generation_environment)
    os.environ.update(environment)
    sys.dont_write_bytecode = True
    _install_source_only_import_hook(source)
    sys.path.insert(0, str(source))
    # Upstream's kuzu package is its Ladybug compatibility shim. Reuse the
    # installed Python ABI-matched native extension; never install/download one.
    ladybug = importlib.util.find_spec('ladybug')
    if ladybug is None or not ladybug.submodule_search_locations or not any(
        (Path(location) / ('_lbug' + suffix)).is_file()
        for location in ladybug.submodule_search_locations
        for suffix in importlib.machinery.EXTENSION_SUFFIXES
    ):
        raise ValueError('existing Ladybug native dependency unavailable for this Python')
    import litellm
    import uvicorn
    if profile['mode'] == 'agent_bridge':
        litellm.register_model({environment['LLM_MODEL']: {'supports_response_schema': True,
                              'supports_function_calling': False, 'litellm_provider': 'openai', 'mode': 'chat'}})
        # This registration describes only our schema-checking local bridge.
        if not litellm.supports_response_schema(model=environment['LLM_MODEL']):
            raise ValueError('configured completion model must support native JSON schema')
    async def serve():
        async with AsyncExitStack() as stack:
            capacity = None
            if not setup_only:
                from generation_http import generation_http_policy
                from language_egress import rewrite_llm_request
                capacity = await stack.enter_async_context(generation_http_policy(endpoint=environment['LLM_ENDPOINT'],
                    max_concurrent_requests=profile.get('max_concurrent_requests', 2),
                    backend_kind=profile.get('bridge_kind') or profile.get('provider'),
                    request_rewriter=rewrite_llm_request))
            if profile.get('provider') == 'ollama':
                from generation_ollama import register_verified_schema
                register_verified_schema(json.loads(environment['MEMLANE_OLLAMA_SCHEMA_EVIDENCE']),
                                         environment['LLM_MODEL'], environment['LLM_ENDPOINT'])
            from cognee.api.client import app
            from session_history import install_session_history
            install_session_history(app, cache_db_path=root / 'system' / 'databases' / 'cache.db')
            state_dir = (
                Path(config['project_control']['registry_file']).parent
                if config.get('project_control')
                else root / 'memlane-state'
            )
            from external_capture_store import ExternalCaptureStore
            capture_store = ExternalCaptureStore(state_dir / 'capture-receipts.sqlite3')
            stack.callback(capture_store.close)
            # session-drift.sqlite3 is owned exclusively by project_control_server
            # (session_drift.attach_session_drift + cognee_extension_gateway) so
            # only one process holds a write connection and PUT /capture/entries
            # never double-observes traces across gateway -> official hops.
            from capture_receipts import install_capture_routes
            install_capture_routes(app, receipt_store=capture_store)
            from provenance_store import ProvenanceStore, install_provenance_middleware
            provenance_store = ProvenanceStore(
                state_dir / 'lesson-provenance.sqlite3',
                cognee_db_path=root / 'system' / 'databases' / 'cognee_db',
            )
            stack.callback(provenance_store.close)
            install_provenance_middleware(app, provenance_store)
            if not setup_only and config.get('project_control'):
                from generation_handoff_runtime import install_generation_handoff
                handoff = install_generation_handoff(app, config, profile,
                    {**generation_environment, '_digest': generation_digest}, capacity)
                stack.push_async_callback(handoff.close)
            if setup_only:
                from first_install_mode import SetupOnlyMiddleware
                app.add_middleware(SetupOnlyMiddleware)
            if config.get('project_control'):
                from tool_graph_service import install_tool_graph_routes
                install_tool_graph_routes(app, config)
            from cognee.modules.improve.config import get_improve_config
            stages_disabled = get_improve_config().stages_disabled
            if config.get('generation_applied_file'):
                if setup_only:
                    raise ValueError('unconfigured service cannot publish a generation-applied receipt')
                from _runtime_common import private_write
                from cognee.modules.cognify.config import get_cognify_config
                private_write(config['generation_applied_file'], json.dumps({
                    'pid': os.getpid(), 'digest': generation_digest, 'reference': config.get('generation_reference'),
                    'mode': profile['mode'], 'model': profile['model'],
                    'improve_stages_disabled': stages_disabled,
                    'chunks_per_batch': get_cognify_config().chunks_per_batch,
                    'max_concurrent_requests': capacity.limit,
                    'capacity_wait_timeout_seconds': capacity.wait_timeout}).encode())
            await uvicorn.Server(uvicorn.Config(
                app, host='127.0.0.1', port=urlparse(config['service_url']).port,
                access_log=False, timeout_graceful_shutdown=15)).serve()
    asyncio.run(serve())


if __name__ == '__main__':
    main()
