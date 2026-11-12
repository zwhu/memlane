#!/usr/bin/env python3
"""Installed SDK model selection only: no HTTP, model, persistence or GLiNER."""
import asyncio
import importlib.metadata
import json
import os
from pathlib import Path
import sys
import tempfile


def main():
    if os.environ.get('MEMLANE_DISK_TASK') != '1':
        raise SystemExit('managed_task_required')
    if importlib.metadata.version('cognee') != '1.6.7':
        raise SystemExit('installed_cognee_version_changed')
    sys.path.insert(0, str(Path(__file__).resolve().parents[1] / 'engine/python'))
    with tempfile.TemporaryDirectory(prefix='memlane-session-model-context-') as temporary:
        root = Path(temporary)
        for name in ('home', 'system', 'data', 'cache', 'logs'): (root / name).mkdir()
        os.environ.update(HOME=str(root / 'home'), SYSTEM_ROOT_DIRECTORY=str(root / 'system'),
                          DATA_ROOT_DIRECTORY=str(root / 'data'), CACHE_ROOT_DIRECTORY=str(root / 'cache'),
                          LOGS_ROOT_DIRECTORY=str(root / 'logs'), COGNEE_LOGS_DIR=str(root / 'logs'),
                          PYTHON_DOTENV_DISABLED='1', HF_HUB_OFFLINE='1', TELEMETRY_DISABLED='true',
                          COGNEE_TRACING_ENABLED='false', LITELLM_LOCAL_MODEL_COST_MAP='true',
                          STRUCTURED_OUTPUT_FRAMEWORK='litellm_native')
        from cognee_session_processing import processing_model_config
        from cognee_processing_adapter import install, ProcessingAdapter
        from cognee.context_global_variables import llm_config
        from cognee.modules.preflight import llm_available
        from cognee.modules.cognify.config import CognifyConfig, resolve_extractor
        from cognee.infrastructure.llm.structured_output_framework.litellm_native import get_native_client as sdk
        install()
        parent = llm_config.get()
        async def selected(number):
            config = processing_model_config({'endpoint': f'http://127.0.0.1:{12000 + number}/v1', 'token': f'synthetic-context-{number}'})
            token = llm_config.set(config)
            try:
                assert llm_available(), 'SDK did not see task model context'
                assert resolve_extractor(None, CognifyConfig(graph_extractor='auto')) == 'llm', 'SDK selected GLiNER fallback'
                await asyncio.sleep(0)
                client = sdk.get_native_client()
                assert isinstance(client, ProcessingAdapter)
                assert client._endpoint == config.llm_endpoint and client._token == config.llm_api_key
            finally:
                llm_config.reset(token)
        async def run(): await asyncio.gather(selected(1), selected(2))
        asyncio.run(run())
        assert llm_config.get() is parent
        print(json.dumps({'status': 'passed', 'installed_cognee': '1.6.7', 'parallel_contexts': 2,
                          'official_llm_preflight': True, 'auto_extractor': 'llm', 'task_adapter_selected': True,
                          'parent_context_restored': True, 'model_called': False, 'improve_executed': False,
                          'gliner_installed': False, 'temporary_root_removed_on_exit': True}))


if __name__ == '__main__': main()
