"""Isolated synthetic probe against the pinned official native LLM adapter."""
import asyncio
import contextlib
import io
import json
import os
from pathlib import Path
import sys
import signal
import threading
import time

from official_service_dev import verify_source


async def probe(config):
    source = verify_source(config)
    sys.path.insert(0, str(source))
    if config.get('generation_mode') == 'agent_bridge':
        # Only the local bridge enforces this contract itself. Never advertise
        # native schema capability for an arbitrary API provider/model.
        import litellm
        litellm.register_model({os.environ['LLM_MODEL']: {'supports_response_schema': True,
                              'supports_function_calling': False, 'litellm_provider': 'openai', 'mode': 'chat'}})
    from pydantic import BaseModel, ConfigDict
    from cognee.infrastructure.llm.structured_output_framework.litellm_native.get_native_client import get_native_client

    class Project(BaseModel):
        model_config = ConfigDict(extra='forbid')
        name: str
        rules: list[str]
        contact: str | None

    class Extraction(BaseModel):
        model_config = ConfigDict(extra='forbid')
        project: Project
        ports: list[int]
        source: str

    client = get_native_client()
    plain = await client.acreate_structured_output('Return exactly the text after the colon, with no quotes, punctuation, or extra characters: MEMLANE-PROBE-OK', 'Follow the instruction exactly.', str)
    plain_ok = isinstance(plain, str) and plain.strip() == 'MEMLANE-PROBE-OK'
    result = await client.acreate_structured_output(
        'Synthetic facts: project name is 样例项目. Rules in order: pnpm, TypeScript. '
        'No contact is given. Ports in order: 43127 and 43128. Source is synthetic.',
        'Extract only the supplied facts. Missing contact must be null. Preserve order and Unicode. Do not invent facts.', Extraction)
    expected = {'project': {'name': '样例项目', 'rules': ['pnpm', 'TypeScript'], 'contact': None},
                'ports': [43127, 43128], 'source': 'synthetic'}
    schema_ok = isinstance(result, Extraction)
    quality_ok = schema_ok and result.model_dump() == expected
    return {'status': 'passed' if plain_ok and schema_ok and quality_ok else 'failed',
            'plain_passed': plain_ok, 'schema_passed': schema_ok, 'quality_passed': quality_ok,
            'structured_mode': 'unknown', 'sample_version': 'nested-unicode-null-2',
            'boundary': 'official_adapter_synthetic_extraction'}


def main():
    parent = os.getppid()
    def watch_owner():
        while os.getppid() == parent:
            time.sleep(.5)
        os.killpg(os.getpgrp(), signal.SIGTERM)
    threading.Thread(target=watch_owner, daemon=True).start()
    try:
        request = json.load(sys.stdin)
        # The manager supplies a fresh synthetic HOME. Every SDK path points
        # inside it before importing Cognee; no daily database is opened.
        root = Path(os.environ['HOME'])
        for key in list(os.environ):
            if key.startswith(('LLM_', 'FALLBACK_', 'COGNEE_', 'EMBEDDING_', 'MEMLANE_', 'OPENAI_', 'ANTHROPIC_', 'GEMINI_')):
                del os.environ[key]
        os.environ.update(request['environment'])
        os.environ.update(SYSTEM_ROOT_DIRECTORY=str(root / 'system'), DATA_ROOT_DIRECTORY=str(root / 'data'),
                          CACHE_ROOT_DIRECTORY=str(root / 'cache'), DB_PATH=str(root / 'system/databases'),
                          LOGS_ROOT_DIRECTORY=str(root / 'logs'), COGNEE_LOGS_DIR=str(root / 'logs'),
                          TELEMETRY_DISABLED='true', COGNEE_TRACING_ENABLED='false', PYTHON_DOTENV_DISABLED='1')
        with contextlib.redirect_stdout(io.StringIO()):
            result = asyncio.run(probe(request['runtime']))
    except Exception as error:
        code = getattr(error, 'status_code', None)
        reason = {401: 'authentication_failed', 403: 'permission_denied', 429: 'rate_or_quota_limit'}.get(code, 'probe_result_unconfirmed')
        result = {'status': 'failed', 'error_code': reason, 'structured_mode': 'unknown'}
    print(json.dumps(result))


if __name__ == '__main__':
    main()
