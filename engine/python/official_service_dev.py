"""Run the pinned upstream API without Memlane business routes or monkey patches."""
import argparse
import hashlib
import importlib.machinery
import importlib.util
import json
import os
from pathlib import Path
import sys
import signal
import threading
import time
import tarfile
from urllib.parse import urlparse


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--config', required=True, type=Path)
    args = parser.parse_args()
    parent = os.getppid()
    def watch_owner():
        while os.getppid() == parent:
            time.sleep(.5)
        os.kill(os.getpid(), signal.SIGTERM)
    threading.Thread(target=watch_owner, daemon=True).start()
    config = json.loads(args.config.read_text())
    root = Path(config['official_data']).resolve(strict=True)
    source = Path(config['official_source']).resolve(strict=True)
    manifest = json.loads((Path(__file__).resolve().parents[1] / 'coding-memory-components.json').read_text())['service']
    archive = Path(config['official_archive']).resolve(strict=True)
    if hashlib.sha256(archive.read_bytes()).hexdigest() != manifest['archive_sha256']:
        raise ValueError('fixed official archive digest mismatch')
    # Source must be the exact upstream package, never a patched service wrapper.
    with tarfile.open(archive) as bundle:
        names = set()
        for member in bundle:
            parts = Path(member.name).parts[1:]
            if not parts or parts[0] not in manifest['packages'] or not member.isfile(): continue
            relative = Path(*parts)
            names.add(relative)
            target = source / relative
            if target.is_symlink() or not target.is_file() or target.read_bytes() != bundle.extractfile(member).read():
                raise ValueError('official package differs from pinned archive: ' + str(relative))
    for package in manifest['packages']:
        for target in (source / package).rglob('*.py'):
            if target.relative_to(source) not in names:
                raise ValueError('unexpected official Python module')
    for variable in list(os.environ):
        if variable.upper().startswith(('LLM_', 'EMBEDDING_', 'COGNEE_', 'MEMLANE_', 'FALLBACK_', 'TRANSCRIPTION_', 'IMAGE_TRANSCRIBE_')):
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
                       LLM_PROVIDER='openai', LLM_MODEL='openai/' + config['model'],
                       LLM_ENDPOINT=config['completion_url'].rstrip('/') + '/v1',
                       LLM_API_KEY=Path(config['completion_token_file']).read_text().strip(),
                       STRUCTURED_OUTPUT_FRAMEWORK='litellm_native', LITELLM_LOCAL_MODEL_COST_MAP='true')
    os.environ.update(environment)
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
    litellm.register_model({environment['LLM_MODEL']: {'supports_response_schema': True,
                          'supports_function_calling': False, 'litellm_provider': 'openai', 'mode': 'chat'}})
    # The fixed official adapter consults LiteLLM's public capability table.
    # Fail before serving instead of selecting its prompted-JSON fallback.
    if not litellm.supports_response_schema(model=environment['LLM_MODEL']):
        raise ValueError('configured completion model must support native JSON schema')
    from cognee.api.client import app
    uvicorn.run(app, host='127.0.0.1', port=urlparse(config['service_url']).port,
                access_log=False, timeout_graceful_shutdown=15)


if __name__ == '__main__':
    main()
