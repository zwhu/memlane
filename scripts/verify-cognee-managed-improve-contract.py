#!/usr/bin/env python3
"""Actual installed SDK result/background API; synthetic owner, no model/store."""
import asyncio
import importlib
import importlib.metadata
import json
import os
from pathlib import Path
import sys
import tempfile
from types import SimpleNamespace
from uuid import UUID, uuid4


def main():
    if os.environ.get('MEMLANE_DISK_TASK') != '1':
        raise SystemExit('managed_task_required')
    report_path = Path(sys.argv[1]).resolve()
    sys.path.insert(0, str(Path(__file__).resolve().parents[1] / 'engine/python'))
    with tempfile.TemporaryDirectory(prefix='managed-improve-sdk-') as temporary:
        root = Path(temporary)
        for name in ('home', 'system', 'data', 'cache', 'logs'):
            (root / name).mkdir()
        os.environ.update(HOME=str(root / 'home'), SYSTEM_ROOT_DIRECTORY=str(root / 'system'),
            DATA_ROOT_DIRECTORY=str(root / 'data'), CACHE_ROOT_DIRECTORY=str(root / 'cache'),
            COGNEE_LOGS_DIR=str(root / 'logs'), LOGS_ROOT_DIRECTORY=str(root / 'logs'),
            COGNEE_LOG_FILE='false', TELEMETRY_DISABLED='true', COGNEE_TRACING_ENABLED='false',
            PYTHON_DOTENV_DISABLED='1', HF_HUB_OFFLINE='1', LITELLM_LOCAL_MODEL_COST_MAP='true')
        import cognee
        from cognee.modules.improve import ImproveResult, StageResult
        from cognee.infrastructure.background_tasks import pending_background_tasks, wait_for_background_tasks
        import cognee_coding_memory as adapter
        from cognee_session_processing import owner_execution
        package = importlib.import_module('cognee.api.v1.improve')
        implementation = importlib.import_module('cognee.api.v1.improve.improve')
        user, dataset, instance = SimpleNamespace(id=uuid4()), SimpleNamespace(id=uuid4()), str(uuid4())
        original_calls, owner_calls = [], []
        async def forbidden_pipeline(**kwargs):
            # The *adapter* must never enter this except in its private owner
            # context. This fixture deliberately cannot run the actual SDK.
            assert owner_execution.get()
            original_calls.append(kwargs)
            return ImproveResult(dataset_id=dataset.id, session_ids=['synthetic'],
                stages=[StageResult(stage='distill_sessions', status='completed', counts={'lessons': 1})])
        async def scope(value, principal):
            assert value == dataset.id and principal is user
            return dataset
        async def owner(path, operation, trigger):
            owner_calls.append(operation)
            if operation == 'coding_memory_trigger':
                return {'state': 'running'}
            return {'state': 'completed', 'receipt': {'status': 'completed', 'stages': [
                {'stage': 'distill_sessions', 'status': 'completed', 'counts': {'lessons': 1}}]}}
        implementation.improve = package.improve = cognee.improve = forbidden_pipeline
        adapter.authorized_existing_dataset, adapter.owner_call = scope, owner
        adapter.service_instance = lambda: instance
        adapter.install('/synthetic/owner.json')
        async def run():
            assert package.improve is implementation.improve is cognee.improve
            result = await cognee.improve(dataset=dataset.id, user=user, session_ids=['synthetic'], run_in_background=True)
            assert isinstance(result, ImproveResult) and result.status == 'running'
            assert pending_background_tasks() >= 1
            assert await result.wait() is result
            assert result.status == 'completed' and result.stages[0].counts == {'lessons': 1}
            wire = result.model_dump(mode='json')
            assert wire['dataset_id'] == str(dataset.id) and wire['session_ids'] == ['synthetic']
            assert wire['status'] == 'completed' and wire['background'] is True
            assert not original_calls and owner_calls == ['coding_memory_trigger', 'coding_memory_task']
            token = owner_execution.set(True)
            try:
                internal = await package.improve(dataset=dataset.id, user=user, session_ids=['synthetic'])
            finally:
                owner_execution.reset(token)
            assert internal.status == 'completed' and len(original_calls) == 1
            assert await wait_for_background_tasks(timeout=2)
            return {'sdk_version': importlib.metadata.version('cognee'), 'status': 'passed',
                'real_sdk_result_serialization': True, 'real_sdk_background_registry': True,
                'public_export_delegated': True, 'internal_context_recursion_blocked': True,
                'scope': 'installed SDK APIs with synthetic owner/original; no real model, pipeline, service or Native acceptance'}
        report = asyncio.run(run())
    report_path.parent.mkdir(parents=True, exist_ok=True)
    report_path.write_text(json.dumps(report, indent=2) + '\n')
    print('Installed SDK managed-improve contract passed.')


if __name__ == '__main__':
    main()
