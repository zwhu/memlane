"""Private, epoch-bound execution of the fixed official code pipeline.

The daemon supplies the existing task and all paths over its private socket.
HTTP callers supply identities only; no path, binary, dataset creation, model
configuration or SDK option can be selected by an Agent or graph page.
"""
import asyncio
import hmac
import json
from pathlib import Path
import re
from uuid import UUID, uuid4


def owner_path():
    from cognee_session_processing import _owner_path
    if not _owner_path:
        raise PermissionError('official_code_owner_required')
    return _owner_path


def require_owner(payload):
    from cognee_coding_memory import owner_configuration
    try:
        expected = owner_configuration(owner_path())['token']
        actual = payload.get('owner', '')
        if not isinstance(actual, str) or not hmac.compare_digest(expected, actual):
            raise ValueError()
    except Exception:
        raise PermissionError('official_code_owner_required') from None


async def authorize_owner(operation):
    """One observation request, never a trigger or implicit execution retry."""
    from cognee_coding_memory import owner_configuration
    writer = None
    try:
        config = owner_configuration(owner_path())
        request_id = str(uuid4())
        reader, writer = await asyncio.wait_for(asyncio.open_unix_connection(config['socket'], limit=65536), 5)
        body = {'protocol': 1, 'id': request_id, 'op': 'official_code_job_authorize',
                'official_code_job': operation, 'coding_memory_capability': config['token']}
        writer.write(json.dumps(body, separators=(',', ':')).encode() + b'\n')
        await asyncio.wait_for(writer.drain(), 5)
        result = json.loads(await asyncio.wait_for(reader.readline(), 5))
        if result.get('protocol') != 1 or result.get('id') != request_id or result.get('ok') is not True:
            raise ValueError()
        return result['result']
    except Exception:
        raise PermissionError('official_code_authorization_unconfirmed') from None
    finally:
        if writer:
            writer.close()
            try:
                await asyncio.wait_for(writer.wait_closed(), 2)
            except Exception:
                pass


def canonical_id(value):
    return isinstance(value, str) and str(UUID(value)) == value


def validate(payload, instance, epoch):
    fields = {'operation', 'instance', 'dataset', 'user', 'source_digest', 'execution_epoch'}
    if not isinstance(payload, dict) or set(payload) - {'owner'} != fields:
        raise ValueError()
    if any(not canonical_id(payload[k]) for k in ('operation', 'instance', 'dataset', 'user', 'execution_epoch')):
        raise ValueError()
    if payload['instance'] != instance or payload['execution_epoch'] != epoch:
        raise ValueError()
    if not isinstance(payload['source_digest'], str) or not re.fullmatch('[0-9a-f]{64}', payload['source_digest']):
        raise ValueError()
    return {k: payload[k] for k in fields}


def checked_grant(grant, identity):
    fields = {*identity, 'repository', 'scratch', 'binary', 'binary_sha256', 'deny_reads'}
    if not isinstance(grant, dict) or set(grant) != fields or any(grant.get(k) != v for k,v in identity.items()):
        raise PermissionError('official_code_scope_changed')
    for key in ('repository', 'scratch', 'binary'):
        value = grant[key]
        if not isinstance(value, str) or not Path(value).is_absolute() or str(Path(value)) != value:
            raise PermissionError('official_code_scope_changed')
    if Path(grant['repository']).parent != Path(grant['scratch']):
        raise PermissionError('official_code_scope_changed')
    if not isinstance(grant['binary_sha256'], str) or not re.fullmatch('[0-9a-f]{64}', grant['binary_sha256']):
        raise PermissionError('official_code_scope_changed')
    if not isinstance(grant['deny_reads'], list) or not 1 <= len(grant['deny_reads']) <= 16 or any(not isinstance(p,str) or not Path(p).is_absolute() for p in grant['deny_reads']):
        raise PermissionError('official_code_scope_changed')
    return json.loads(json.dumps(grant))


async def existing_dataset(identity, user, repository):
    from cognee.modules.data.methods import get_authorized_existing_datasets, get_dataset_data
    from cognee.tasks.code_graph.cluster import code_repo_path
    if user is None or str(user.id) != identity['user']:
        raise PermissionError('official_code_scope_denied')
    dataset = UUID(identity['dataset'])
    rows = await get_authorized_existing_datasets([dataset], 'write', user, strict=True)
    if len(rows) != 1 or rows[0].id != dataset:
        raise PermissionError('official_code_scope_denied')
    # A dedicated dataset holds at most this one repository Data row. The
    # official SDK may not bring another repo/document into a cluster refresh.
    data = await get_dataset_data(dataset, limit=2)
    if not isinstance(data, list) or len(data) > 1 or any(code_repo_path(row) != Path(repository) for row in data):
        raise PermissionError('official_code_dataset_changed')


def safe_result(identity, result):
    if not isinstance(result, dict):
        # The fixed official RememberResult is a result class with to_dict,
        # not a Pydantic model (the improve API uses a different contract).
        result = result.to_dict()
    out = dict(identity, status='unconfirmed')
    if result.get('dataset_id') != identity['dataset']:
        return out
    items = result.get('items')
    if not isinstance(items, list) or len(items) != 1 or items[0].get('kind') != 'code_repository':
        return out
    item = items[0]
    for source, target in (('id', 'data_id'), ('pipeline_run_id', 'pipeline_run_id')):
        value = item.get(source)
        try:
            if canonical_id(value): out[target] = value
        except Exception:
            pass
    if result.get('pipeline_run_id') not in (None, out.get('pipeline_run_id')):
        return dict(identity,status='unconfirmed')
    if result.get('status') == 'completed' and item.get('status') != 'errored' and type(result.get('items_processed')) is int and result['items_processed'] == 1 and all(k in out for k in ('data_id','pipeline_run_id')):
        out['status'] = 'completed'
    elif result.get('status') == 'errored' or item.get('status') == 'errored':
        out['status'] = 'failed'
    return out


async def execute(identity, user, grant, authorize):
    from cognee_code_guard import CodeExecution, owned_execution
    from cognee_code_process import EnolaRuntime
    from cognee import remember
    await existing_dataset(identity, user, grant['repository'])
    async def check():
        current = checked_grant(await authorize(identity['operation']), identity)
        if current != grant:
            raise PermissionError('official_code_scope_changed')
        return True
    runtime = EnolaRuntime(grant['binary'], grant['binary_sha256'], grant['scratch'], tuple(grant['deny_reads']))
    with owned_execution(CodeExecution(grant['repository'], identity['dataset'], identity['user'], check, runtime)):
        await check()
        # UUID prevents the SDK's name-based create-dataset fallback. Ordinary
        # official locks, code pipeline, graph-only storage and refresh remain.
        result = await remember(grant['repository'], dataset_id=UUID(identity['dataset']),
            user=user, content_type='code', run_in_background=False,
            index_vectors=False, raise_on_error=False)
        await check()
        await existing_dataset(identity, user, grant['repository'])
        return safe_result(identity, result)


def install(app, instance, authenticated_user, authorize=authorize_owner):
    from fastapi import Depends, HTTPException, Request
    globals()['Request'] = Request
    epoch = str(uuid4())
    active, receipts = {}, {}

    async def read(request):
        raw = bytearray()
        async for part in request.stream():
            raw.extend(part)
            if len(raw) > 4096: raise HTTPException(413, 'official_code_request_unbounded')
        try:
            payload = json.loads(raw)
            require_owner(payload)
        except Exception:
            raise HTTPException(403, 'official_code_owner_required') from None
        return payload

    @app.post('/api/v1/memlane/code/index/cancel')
    async def cancel(request: Request, user=Depends(authenticated_user)):
        payload = await read(request)
        try: identity = validate(payload,instance,epoch)
        except Exception: raise HTTPException(409,'official_code_execution_changed') from None
        key = identity['operation']
        entry = receipts.get(key)
        if user is None or str(user.id) != identity['user'] or entry is None or entry[0] != identity:
            # Absent/current-process-only state cannot prove an old task stopped.
            raise HTTPException(409,'official_code_execution_unconfirmed')
        task = active.get(key)
        if task is not None:
            task.cancel()
            await asyncio.gather(task,return_exceptions=True)
        result = receipts[key][1]
        if result['status'] == 'unconfirmed':
            raise HTTPException(409,'official_code_execution_unconfirmed')
        return result

    @app.post('/api/v1/memlane/code/index')
    async def submit(request: Request, user=Depends(authenticated_user)):
        payload = await read(request)
        try:
            identity = validate(payload,instance,epoch)
            if user is None or str(user.id) != identity['user']: raise ValueError()
        except Exception: raise HTTPException(409,'official_code_execution_changed') from None
        key=identity['operation']
        if key in receipts:
            previous,result=receipts[key]
            if previous!=identity: raise HTTPException(409,'official_code_execution_changed')
            if key in active: raise HTTPException(409,'official_code_busy')
            if result['status']=='unconfirmed': raise HTTPException(409,'official_code_execution_unconfirmed')
            return result
        if len(receipts)>=1024 or active:
            raise HTTPException(429,'official_code_busy')
        try:
            grant=checked_grant(await authorize(key),identity)
            await existing_dataset(identity,user,grant['repository'])
        except Exception: raise HTTPException(403,'official_code_scope_denied') from None
        # Authorization awaits may interleave two submissions. Reserve only
        # after repeating the identity/slot checks, with no await before task
        # creation, so concurrent preflights cannot start the same work twice.
        if key in receipts:
            previous,result=receipts[key]
            if previous!=identity:raise HTTPException(409,'official_code_execution_changed')
            if key in active:raise HTTPException(409,'official_code_busy')
            if result['status']=='unconfirmed':raise HTTPException(409,'official_code_execution_unconfirmed')
            return result
        if active or len(receipts)>=1024:raise HTTPException(429,'official_code_busy')
        receipts[key]=(identity,dict(identity,status='unconfirmed'))
        async def run():
            try:
                result=await execute(identity,user,grant,authorize)
            except asyncio.CancelledError:
                receipts[key]=(identity,dict(identity,status='cancelled'))
                raise
            except BaseException as error:
                # Reap loss is never relabeled stopped; no raw SDK diagnostic.
                from cognee_code_process import CodeProcessError
                status='unconfirmed' if isinstance(error,CodeProcessError) and str(error)=='code_process_reap_unconfirmed' else 'failed'
                receipts[key]=(identity,dict(identity,status=status))
                raise
            receipts[key]=(identity,result)
            return result
        task=asyncio.create_task(run());active[key]=task
        async def watch():
            while not task.done():
                try:
                    if await request.is_disconnected(): raise PermissionError()
                    if checked_grant(await authorize(key),identity)!=grant: raise PermissionError()
                    await existing_dataset(identity,user,grant['repository'])
                except Exception:
                    task.cancel();return
                await asyncio.sleep(0.2)
        monitor=asyncio.create_task(watch())
        try:
            return await asyncio.wait_for(asyncio.shield(task),650)
        except asyncio.CancelledError:
            task.cancel()
            await asyncio.gather(task,return_exceptions=True)
            if asyncio.current_task().cancelling(): raise
            # A monitor/cancel endpoint cancelled the owned task, not this
            # request's caller. Return its observed outcome as structured data.
            return receipts[key][1]
        except Exception:
            task.cancel()
            await asyncio.gather(task,return_exceptions=True)
            raise HTTPException(503,'official_code_execution_failed') from None
        finally:
            monitor.cancel()
            if not task.done():task.cancel()
            await asyncio.gather(monitor,task,return_exceptions=True)
            active.pop(key,None)
    return epoch
