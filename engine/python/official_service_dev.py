"""Run the verified API with SDK adapters and a versioned provenance component."""
import argparse
import asyncio
import hashlib
import importlib.machinery
import importlib.util
import json
import os
from pathlib import Path
import sys
import signal
import time
import tarfile
from contextlib import AsyncExitStack
from urllib.parse import urlparse


def verify_source(config):
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
            if parts == ('pyproject.toml',):
                target = source / 'pyproject.toml'
                if target.is_symlink() or not target.is_file() or target.read_bytes() != bundle.extractfile(member).read():
                    raise ValueError('official package metadata differs from pinned archive')
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
    if config.get('project_control'):
        from lesson_provenance_patch import prepare
        component_root = Path(config['project_control']['registry_file']).parent / 'engine-components'
        return prepare(source, manifest, component_root)
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
                capacity = await stack.enter_async_context(generation_http_policy(endpoint=environment['LLM_ENDPOINT'],
                    max_concurrent_requests=profile.get('max_concurrent_requests', 2),
                    backend_kind=profile.get('bridge_kind') or profile.get('provider')))
            if profile.get('provider') == 'ollama':
                from generation_ollama import register_verified_schema
                register_verified_schema(json.loads(environment['MEMLANE_OLLAMA_SCHEMA_EVIDENCE']),
                                         environment['LLM_MODEL'], environment['LLM_ENDPOINT'])
            from cognee.api.client import app
            from session_history import install_session_history
            install_session_history(app)
            from capture_receipts import install_capture_routes
            install_capture_routes(app)
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
                from project_control import private_write
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
