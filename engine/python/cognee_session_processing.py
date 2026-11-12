"""Run official improve with one ephemeral, daemon-owned model context.

No new text store, model fallback, environment mutation or session ingestion.
The SDK owns all persistence and distillation. Cross-owner cache processing is
rejected until the caller can present that owner's own service identity.
"""
import asyncio
import hashlib
import hmac
import json
import re
from contextvars import ContextVar
from uuid import UUID

# Only daemon-owned internal execution may bypass the public trigger adapter.
# This is context-local and never supplied in an HTTP/model payload.
owner_execution = ContextVar('memlane_owner_session_execution', default=False)
_owner_path = None


def configure_owner(path):
    global _owner_path
    _owner_path = path


def require_owner(payload):
    if _owner_path is None:
        return
    from cognee_coding_memory import owner_configuration
    try:
        expected = owner_configuration(_owner_path)['token']
        actual = payload.get('owner', '')
        valid = isinstance(actual, str) and hmac.compare_digest(expected, actual)
    except Exception:
        valid = False
    if not valid:
        raise PermissionError('session_processing_owner_required')


async def authorized_existing_dataset(dataset, user):
    from cognee.modules.data.methods import get_authorized_existing_datasets
    if user is None or not dataset:
        raise PermissionError('session_processing_scope_denied')
    rows = await get_authorized_existing_datasets([dataset], 'write', user, strict=True)
    if len(rows) != 1:
        raise PermissionError('session_processing_scope_denied')
    return rows[0]


def validate(payload, instance):
    expected = {'instance', 'library', 'operation', 'project', 'dataset', 'session', 'processing', 'gateway'}
    if not isinstance(payload, dict) or set(payload) - {'automatic', 'owner'} != expected:
        raise ValueError('invalid_session_processing')
    if 'automatic' in payload and payload['automatic'] is not True:
        raise ValueError('invalid_session_processing')
    for key in ('instance', 'library', 'operation', 'project', 'dataset'):
        if str(UUID(payload[key])) != payload[key]:
            raise ValueError('canonical_session_processing_identity_required')
    if payload['instance'] != instance:
        raise ValueError('session_processing_instance_changed')
    session = payload['session']
    if not isinstance(session, str) or not 1 <= len(session) <= 128 or any(c.isspace() or ord(c) < 32 for c in session):
        raise ValueError('bounded_session_identity_required')
    processing = payload['processing']
    if not isinstance(processing, dict) or not isinstance(processing.get('revision'), dict):
        raise ValueError('frozen_processing_identity_required')
    for value in (processing['revision'].get('config_digest'), processing.get('dependency_digest')):
        if not isinstance(value, str) or not re.fullmatch('[0-9a-f]{64}', value):
            raise ValueError('frozen_processing_identity_required')
    gateway = payload['gateway']
    if not isinstance(gateway, dict) or set(gateway) != {'endpoint', 'token'}:
        raise ValueError('private_owner_gateway_required')
    from cognee_processing_adapter import ProcessingAdapter
    ProcessingAdapter(gateway['endpoint'], gateway['token'])
    # A replay is tied to the frozen task, not its replaceable live capability.
    identity = {k: v for k, v in payload.items() if k not in ('gateway', 'owner')}
    return hashlib.sha256(json.dumps(identity, sort_keys=True, separators=(',', ':')).encode()).hexdigest()


def processing_model_config(gateway):
    from cognee.infrastructure.llm.config import LLMConfig
    from cognee_processing_adapter import ALIAS
    return LLMConfig(
        _env_prefix='MEMLANE_SESSION_ISOLATED_', _env_file=None,
        llm_provider='custom', llm_model=ALIAS, llm_endpoint=gateway['endpoint'],
        llm_api_key=gateway['token'], llm_max_completion_tokens=8192,
        llm_streaming=False, llm_args={},
        **{f'llm_{stage}_{field}': value for stage in ('extraction', 'summarization', 'query')
           for field, value in dict(model=ALIAS, provider='custom', endpoint=gateway['endpoint'], api_key=gateway['token']).items()})


def safe_result(result):
    # SDK errors may include raw provider/transport diagnostics. Return fixed
    # stage facts only; callers separately retain gateway execution receipts.
    if not isinstance(result, dict):
        result = result.model_dump(mode='json')
    status = result.get('status')
    if status not in ('completed', 'errored', 'skipped'):
        raise ValueError('session_processing_result_unconfirmed')
    stages = []
    for stage in result.get('stages', []):
        if not re.fullmatch('[a-z_]{1,64}', stage.get('stage', '')) or stage.get('status') not in ('completed', 'already_completed', 'skipped', 'errored'):
            raise ValueError('session_processing_result_unconfirmed')
        entry = {'stage': stage['stage'], 'status': stage['status'],
                 'counts': {k: v for k, v in stage.get('counts', {}).items()
                            if re.fullmatch('[a-z_]{1,64}', k) and type(v) is int and v >= 0}}
        if stage.get('status') == 'errored':
            entry['error_code'] = 'official_stage_failed'
        reason = stage.get('reason')
        if isinstance(reason, str) and re.fullmatch('[a-z_]{1,64}', reason):
            entry['reason'] = reason
        stages.append(entry)
    if not stages or len(stages) > 16:
        raise ValueError('session_processing_result_unconfirmed')
    return {'status': status, 'stages': stages}


async def improve_official(payload, user):
    from cognee.base_config import get_base_config
    from cognee.api.v1.serve.state import get_remote_client
    from cognee.modules.session_lifecycle.metrics import get_session_row
    from cognee.context_global_variables import llm_config
    from cognee.api.v1.improve import improve
    from cognee_processing_adapter import install

    if get_base_config().personalization_enabled or get_remote_client() is not None:
        raise PermissionError('session_processing_scope_unsupported')
    dataset = UUID(payload['dataset'])
    await authorized_existing_dataset(dataset, user)
    # No arbitrary visible owner's cache and no same-name cross-dataset session.
    row = await get_session_row(session_id=payload['session'], user_id=user.id)
    if row is None or row.user_id != user.id or row.dataset_id != dataset:
        raise PermissionError('session_processing_owner_changed')
    install()
    token = llm_config.set(processing_model_config(payload['gateway']))
    execution = owner_execution.set(True)
    try:
        try:
            result = await improve(dataset=dataset, session_ids=[payload['session']], user=user,
                                   run_in_background=False,
                                   build_global_context_index=None if payload.get('automatic') else False,
                                   review_conflicts=None if payload.get('automatic') else False,
                                   build_truth_subspace=False)
        except Exception as error:
            # Official fatal-stage failures carry the honest partial result.
            result = getattr(error, 'improve_result', None)
            if result is None:
                raise ValueError('session_processing_failed') from None
        return safe_result(result)
    finally:
        owner_execution.reset(execution)
        llm_config.reset(token)


def install(app, instance, authenticated_user):
    from fastapi import Depends, HTTPException, Request
    globals()['Request'] = Request
    active, receipts, cancelled = {}, {}, set()

    @app.get('/api/v1/memlane/coding-memory/scope')
    async def coding_memory_scope(dataset: UUID, user=Depends(authenticated_user)):
        # The principal comes from official authentication, never model text
        # or the host's proposed grant. This reads no session or document body.
        try:
            await authorized_existing_dataset(dataset, user)
        except Exception:
            raise HTTPException(403, 'coding_memory_scope_denied') from None
        managed = False
        if _owner_path is not None:
            from cognee_coding_memory import owner_configuration
            try:
                owner_configuration(_owner_path)
                managed = True
            except Exception:
                pass
        return {'instance': instance, 'dataset': str(dataset), 'user': str(user.id), 'managed': managed}

    async def read_payload(request, limit):
        raw = bytearray()
        async for part in request.stream():
            raw.extend(part)
            if len(raw) > limit:
                raise HTTPException(413, 'session_processing_request_unbounded')
        try:
            return json.loads(raw)
        except Exception:
            raise HTTPException(422, 'invalid_session_processing') from None

    @app.post('/api/v1/memlane/session-improve/cancel')
    async def cancel(request: Request, user=Depends(authenticated_user)):
        payload = await read_payload(request, 2048)
        try:
            require_owner(payload)
        except PermissionError:
            raise HTTPException(403, 'session_processing_owner_required') from None
        try:
            if set(payload) - {'owner'} != {'instance', 'library', 'operation'} or payload['instance'] != instance:
                raise ValueError()
            library, operation = (str(UUID(payload[k])) for k in ('library', 'operation'))
        except Exception:
            raise HTTPException(422, 'invalid_session_processing_cancel') from None
        key = (str(user.id), library, operation)
        if len(cancelled) >= 1024:
            raise HTTPException(429, 'session_processing_cancel_budget')
        cancelled.add(key)
        task = active.get(key)
        if task is not None:
            task.cancel()
            await asyncio.gather(task, return_exceptions=True)
        return {'instance': instance, 'library': library, 'operation': operation, 'state': 'stopped'}

    @app.post('/api/v1/memlane/session-improve')
    async def submit(request: Request, user=Depends(authenticated_user)):
        payload = await read_payload(request, 16384)
        try:
            require_owner(payload)
        except PermissionError:
            raise HTTPException(403, 'session_processing_owner_required') from None
        try:
            digest = validate(payload, instance)
        except Exception:
            raise HTTPException(422, 'invalid_session_processing') from None
        key = (str(user.id), payload['library'], payload['operation'])
        if key in cancelled:
            raise HTTPException(409, 'session_processing_cancelled')
        if key in active:
            raise HTTPException(409, 'session_processing_busy')
        if key in receipts:
            previous, result = receipts[key]
            if previous != digest:
                raise HTTPException(409, 'session_processing_changed')
            if result['status'] == 'unconfirmed':
                raise HTTPException(409, 'session_processing_unconfirmed')
            return result
        if len(receipts) >= 1024:
            raise HTTPException(429, 'session_processing_receipt_budget')
        # Reserve the identity before work, including outcomes that cannot be
        # confirmed after transport cancellation. Never automatically rerun it.
        receipts[key] = (digest, {'status': 'unconfirmed', 'stages': []})
        task = asyncio.create_task(improve_official(payload, user))
        active[key] = task
        async def disconnected():
            while not task.done():
                if await request.is_disconnected():
                    task.cancel()
                    return
                await asyncio.sleep(0.1)
        watch = asyncio.create_task(disconnected())
        try:
            result = await asyncio.wait_for(task, timeout=300)
            receipt = {k: payload[k] for k in ('instance', 'library', 'operation', 'project', 'dataset', 'session')}
            receipt.update(user=str(user.id), cognee_version='1.6.7',
                           processing_revision_digest=payload['processing']['revision']['config_digest'],
                           processing_dependency_digest=payload['processing']['dependency_digest'])
            receipt.update(result)
            receipts[key] = (digest, receipt)
            return receipt
        except PermissionError as error:
            code = str(error)
            if code not in ('session_processing_owner_changed', 'session_processing_scope_unsupported'):
                code = 'session_processing_denied'
            raise HTTPException(403, code) from None
        except asyncio.CancelledError:
            task.cancel()
            raise
        except Exception:
            raise HTTPException(503, 'session_processing_failed') from None
        finally:
            watch.cancel()
            if not task.done(): task.cancel()
            await asyncio.gather(watch, task, return_exceptions=True)
            active.pop(key, None)
