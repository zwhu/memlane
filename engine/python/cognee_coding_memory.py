"""Delegate public official improve triggers to the daemon-owned model run.

No collection, distillation, polling-triggered retry or content persistence.
Only the daemon's internal improve may enter the unchanged official pipeline.
"""
import asyncio
import importlib
import json
import os
from pathlib import Path
import stat
from uuid import UUID, uuid4

from cognee_session_processing import authorized_existing_dataset, owner_execution, configure_owner

_SAFE_CODES = frozenset(('scope_denied', 'scope_changed', 'coding_memory_disabled',
    'configuration_required', 'unsupported_processing', 'busy', 'stale_source',
    'conflict', 'session_processing_unavailable', 'dependency_changed',
    'verification_required', 'service_authorization_required', 'service_stopping',
    'timeout', 'cancelled', 'official_stage_failed', 'model_execution_failed',
    'session_processing_failed', 'source_changed', 'session_cleanup_unconfirmed',
    'executor_reap_failed', 'service_restarted'))


def owner_configuration(path):
    path = Path(path)
    if not path.is_absolute():
        raise ValueError('coding_memory_owner_configuration_required')
    fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW)
    try:
        info = os.fstat(fd)
        if not stat.S_ISREG(info.st_mode) or info.st_uid != os.getuid() or info.st_mode & 0o077 or info.st_size > 4096:
            raise ValueError('coding_memory_owner_configuration_invalid')
        raw = os.read(fd, 4097)
        if len(raw) > 4096:
            raise ValueError('coding_memory_owner_configuration_invalid')
        value = json.loads(raw)
    finally:
        os.close(fd)
    if not isinstance(value, dict) or set(value) != {'protocol', 'socket', 'token'} or value['protocol'] != 1:
        raise ValueError('coding_memory_owner_configuration_invalid')
    token, socket = value['token'], value['socket']
    if not isinstance(token, str) or not 32 <= len(token) <= 256 or any(c.isspace() for c in token):
        raise ValueError('coding_memory_owner_configuration_invalid')
    if not isinstance(socket, str) or not Path(socket).is_absolute():
        raise ValueError('coding_memory_owner_configuration_invalid')
    parent = Path(socket).parent.lstat()
    target = Path(socket).lstat()
    if not stat.S_ISDIR(parent.st_mode) or parent.st_uid != os.getuid() or parent.st_mode & 0o077 or not stat.S_ISSOCK(target.st_mode) or target.st_uid != os.getuid():
        raise ValueError('coding_memory_owner_configuration_invalid')
    return value


async def owner_call(path, operation, trigger):
    """One bounded private request. A timeout never resubmits paid work."""
    writer = None
    try:
        config = owner_configuration(path)
        request_id = str(uuid4())
        reader, writer = await asyncio.wait_for(asyncio.open_unix_connection(config['socket'], limit=1024 * 1024), 5)
        body = {'protocol': 1, 'id': request_id, 'op': operation,
                'coding_memory_capability': config['token'], 'coding_memory_trigger': trigger}
        writer.write(json.dumps(body, separators=(',', ':')).encode() + b'\n')
        await asyncio.wait_for(writer.drain(), 5)
        response = json.loads(await asyncio.wait_for(reader.readline(), 30))
        if response.get('protocol') != 1 or response.get('id') != request_id:
            raise ValueError('coding_memory_owner_response_invalid')
        if response.get('ok') is not True:
            code = response.get('code')
            raise ValueError('coding_memory_' + (code if code in _SAFE_CODES else 'owner_rejected'))
        task = response['result']
        request = task['request']
        if any(request.get(k) != v for k, v in trigger.items()) or not request.get('automatic_grant') or task.get('state') not in ('prepared', 'running', 'completed', 'failed', 'cancelled', 'interrupted'):
            raise ValueError('coding_memory_owner_response_invalid')
        return task
    except ValueError as error:
        # Do not stringify JSON/transport exceptions: they may include raw wire
        # data or a private config path. Only our fixed codes cross the SDK.
        message = error.args[0] if len(error.args) == 1 else ''
        allowed = {'coding_memory_' + code for code in _SAFE_CODES}
        allowed.update(('coding_memory_owner_rejected', 'coding_memory_owner_response_invalid',
                        'coding_memory_owner_configuration_required', 'coding_memory_owner_configuration_invalid'))
        raise ValueError(message if message in allowed else 'coding_memory_owner_unavailable') from None
    except Exception:
        raise ValueError('coding_memory_owner_unavailable') from None
    finally:
        if writer is not None:
            writer.close()
            try:
                await asyncio.wait_for(writer.wait_closed(), 2)
            except Exception:
                pass


async def observe(path, trigger, task, result):
    from cognee.modules.improve import StageResult
    try:
        async with asyncio.timeout(360):
            while task['state'] in ('prepared', 'running'):
                await asyncio.sleep(0.5)
                task = await owner_call(path, 'coding_memory_task', trigger)
        receipt = task.get('receipt')
        if receipt:
            # These are the official persisted run's safe stage facts. Missing
            # results are never reconstructed as invented knowledge/success.
            result.stages = [StageResult(stage=s['stage'], status=s['status'],
                counts=s.get('counts', {}), reason=s.get('reason'),
                error=s.get('error_code')) for s in receipt['stages']]
        if task['state'] != 'completed' or not receipt:
            code = task.get('error_code')
            result.error = 'coding_memory_' + (code if code in _SAFE_CODES else 'run_unconfirmed')
    except asyncio.CancelledError:
        result.error = 'coding_memory_observer_interrupted'
        raise
    except Exception:
        # Observation loss does not prove the daemon task has stopped. No new
        # run or cancellation is issued merely to recover an observer.
        result.error = 'coding_memory_observation_unconfirmed'
    finally:
        result.finished = True
    return result


def install(owner_path):
    package = importlib.import_module('cognee.api.v1.improve')
    implementation = importlib.import_module('cognee.api.v1.improve.improve')
    import cognee
    original = implementation.improve
    if getattr(original, '_memlane_managed_improve', False):
        return
    configure_owner(owner_path)

    async def managed(dataset='main_dataset', *, session_ids=None, run_in_background=False,
                      user=None, build_global_context_index=None, review_conflicts=None,
                      build_truth_subspace=False, **kwargs):
        if owner_execution.get():
            return await original(dataset=dataset, session_ids=session_ids, run_in_background=run_in_background,
                user=user, build_global_context_index=build_global_context_index,
                review_conflicts=review_conflicts, build_truth_subspace=build_truth_subspace, **kwargs)
        # Never ignore semantic SDK options or forward an unscoped all-session
        # improve into an automatically authorized project run.
        if kwargs or build_global_context_index is not None or review_conflicts is not None or build_truth_subspace or type(run_in_background) is not bool:
            raise ValueError('coding_memory_options_unsupported')
        if not isinstance(session_ids, list) or len(session_ids) != 1 or not isinstance(session_ids[0], str) or not 1 <= len(session_ids[0]) <= 128 or any(c.isspace() or ord(c) < 32 for c in session_ids[0]):
            raise ValueError('coding_memory_session_required')
        scoped = await authorized_existing_dataset(dataset, user)
        trigger = {'operation': str(uuid4()), 'instance': service_instance(),
                   'dataset': str(scoped.id), 'user': str(user.id), 'session': session_ids[0]}
        task = await owner_call(owner_path, 'coding_memory_trigger', trigger)
        from cognee.modules.improve import ImproveResult
        result = ImproveResult(dataset_id=UUID(trigger['dataset']), session_ids=session_ids,
                               background=run_in_background, finished=False)
        if run_in_background:
            from cognee.infrastructure.background_tasks import register_background_task
            watching = asyncio.create_task(observe(owner_path, trigger, task, result))
            register_background_task(watching)
            return result.attach_background_task(watching)
        return await observe(owner_path, trigger, task, result)

    managed._memlane_managed_improve = True
    package.improve = implementation.improve = cognee.improve = managed


def service_instance():
    # Identity is provisioned by cognee_browsing before this installer runs.
    path = Path(os.environ['SYSTEM_ROOT_DIRECTORY']) / 'memlane-service-identity.json'
    value = json.loads(path.read_text())
    return str(UUID(value['instance']))
