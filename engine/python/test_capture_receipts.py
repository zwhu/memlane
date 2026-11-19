"""Pinned SDK + real temporary SQLite, no daily store or inference/network."""
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]
WORKER = r'''
import asyncio, hashlib, importlib, json, sys, uuid
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import AsyncMock
import httpx
from fastapi import FastAPI, Header, HTTPException
from sqlalchemy import event
from capture_service_patch import patch, MANAGER, ADAPTER
from capture_receipts import Capture, CaptureConflict, CaptureDuplicate, current_capture, lookup, install_capture_routes
source = Path(sys.argv[1])
for path in (ADAPTER, MANAGER):
    name = path[:-3].replace('/', '.')
    module = importlib.import_module(name)
    exec(compile(patch(path, (source / path).read_text()), path, 'exec'), module.__dict__)
from cognee.infrastructure.databases.cache.sql.SqlCacheAdapter import SqlCacheAdapter
from cognee.infrastructure.session.session_manager import SessionManager
manager_module = importlib.import_module('cognee.infrastructure.session.session_manager')
manager_module.index_session_qa = AsyncMock()
manager_module.record_session_activity = AsyncMock()
SessionManager._maybe_extract_agent_context = AsyncMock()
manager_module.send_telemetry = lambda *a, **kw: None

async def scenario():
    path = Path(sys.argv[2]) / 'capture.db'
    adapter = SqlCacheAdapter('sqlite+aiosqlite:///' + str(path), session_ttl_seconds=None, purge_interval_seconds=0)
    manager = SessionManager(adapter)
    user = SimpleNamespace(id=uuid.uuid4())
    dataset = uuid.uuid4()
    scope = dict(user=str(user.id), dataset=str(dataset), session='session')
    async def write(kind, message, digest='digest'):
        cap = Capture(**scope, message=message, digest=digest, kind=kind)
        token = current_capture.set(cap)
        try:
            if kind == 'qa':
                result = await manager.add_qa(user_id=str(user.id), session_id='session', question='Q', context='', answer='A')
            else:
                result = await manager.add_agent_trace_step(user_id=str(user.id), session_id='session',
                    origin_function='Read', status='success', generate_feedback_with_llm=False)
            assert result == cap.entry_id
            return cap
        finally:
            current_capture.reset(token)
    # Concurrent lost-response retries commit exactly one native row per ID.
    for kind in ('qa', 'trace'):
        message = str(uuid.uuid4())
        caps = await asyncio.gather(*(write(kind, message) for _ in range(8)))
        assert await lookup(adapter, caps[0]) == caps[0].receipt()
        rows = (await manager.get_session(user_id=str(user.id), session_id='session', formatted=False) if kind == 'qa'
                else await manager.get_agent_trace_session(user_id=str(user.id), session_id='session'))
        assert len(rows) == 1, (kind, len(rows))
        try:
            await write(kind, message, 'changed')
        except CaptureConflict:
            pass
        else:
            raise AssertionError('same number accepted with different body')
    # A native insert failure rolls back the receipt in the SAME transaction.
    cap = Capture(**scope, message=str(uuid.uuid4()), digest='rollback', kind='trace')
    def fail_insert(conn, cursor, statement, parameters, context, executemany):
        if statement.startswith('INSERT INTO cache_trace_entries'):
            raise RuntimeError('synthetic before native insert')
    event.listen(adapter.engine.sync_engine, 'before_cursor_execute', fail_insert)
    try:
        try:
            await write('trace', cap.message, cap.digest)
        except Exception:
            pass
        else:
            raise AssertionError('injected rollback did not run')
        assert await lookup(adapter, cap) is None
    finally:
        event.remove(adapter.engine.sync_engine, 'before_cursor_execute', fail_insert)
    await write('trace', cap.message, cap.digest)
    # Reopen the native cache, proving receipt and content outlive their writer.
    await adapter.close()
    adapter = SqlCacheAdapter('sqlite+aiosqlite:///' + str(path), session_ttl_seconds=None, purge_interval_seconds=0)
    manager = SessionManager(adapter)
    assert await lookup(adapter, cap) == cap.receipt()
    await write('trace', cap.message, cap.digest)
    assert len(await manager.get_agent_trace_session(user_id=str(user.id), session_id='session')) == 2

    denied = False
    async def authenticate(x_api_key: str = Header(default='')):
        if x_api_key != 'test-only':
            raise HTTPException(401)
        return user
    async def resolve(**kwargs):
        if denied:
            raise HTTPException(403)
        if kwargs['dataset_id'] != dataset or kwargs['session_id'] != 'session':
            raise HTTPException(409)
        return user, SimpleNamespace(id=dataset), True
    async def remember(entry, **kwargs):
        assert entry.type == 'trace'
        await manager.add_agent_trace_step(user_id=str(user.id), session_id=kwargs['session_id'],
            origin_function=entry.origin_function, status=entry.status, generate_feedback_with_llm=False,
            method_params=entry.method_params, method_return_value=entry.method_return_value, time=entry.time)
        # Content committed; simulate post-commit downstream/response failure.
        raise TimeoutError('synthetic after commit')
    bindings = SimpleNamespace(get_authenticated_user=authenticate,
        get_permitted_dataset_ids=AsyncMock(return_value=[dataset]), get_session_manager=lambda: manager,
        get_session_row=AsyncMock(return_value=SimpleNamespace(dataset_id=dataset)),
        resolve_entry_dataset=resolve, remember=remember)
    app = FastAPI()
    install_capture_routes(app, bindings=bindings)
    async with httpx.AsyncClient(transport=httpx.ASGITransport(app=app), base_url='http://fixture',
                                headers={'X-Api-Key': 'test-only'}) as client:
        message = str(uuid.uuid4())
        url = '/api/v1/capture/entries/' + message
        payload = dict(dataset_id=str(dataset), session_id='session', entry=dict(type='trace',
            origin_function='Read', status='success', generate_feedback_with_llm=False,
            method_params={'file':'synthetic.py'}, method_return_value='synthetic'))
        first = await client.put(url, json=payload)
        assert first.status_code == 200, first.text
        second = await client.put(url, json=payload)
        assert first.json() == second.json()
        assert (await client.get(url, params={k:payload[k] for k in ('dataset_id','session_id')})).json() == first.json()
        changed = {**payload, 'entry':{**payload['entry'], 'method_return_value':'different'}}
        assert (await client.put(url, json=changed)).status_code == 409
        assert (await client.put(url, json=payload, headers={'X-Api-Key':'wrong'})).status_code == 401
        adapter.__memlane_capture_protocol__ = 0
        assert (await client.put('/api/v1/capture/entries/'+str(uuid.uuid4()),json=payload)).status_code == 503
        adapter.__memlane_capture_protocol__ = 1
        denied = True
        assert (await client.put(url, json=payload)).status_code == 403
        denied = False
        bindings.get_permitted_dataset_ids.return_value = []
        assert (await client.get(url, params={k:payload[k] for k in ('dataset_id','session_id')})).status_code == 404
        assert len(await manager.get_agent_trace_session(user_id=str(user.id), session_id='session')) == 3
        if len(sys.argv) > 3:
            # Actual prepared official sender -> actual route -> native SQL.
            # Inject a dropped response AFTER the server has committed.
            import copy, importlib, time
            sys.path.insert(0, sys.argv[3])
            import _plugin_common as common, _memlane_policy as policy, _project_memory as routing
            import _memlane_capture as delivery
            bindings.get_permitted_dataset_ids.return_value = [dataset]
            common.set_session_key('capture-test-host')
            policy.require = lambda *a, **kw: None
            policy._pending_identity = lambda *a, **kw: None
            routing.route = lambda *a, **kw: {'write':str(dataset)}
            # Keep the real UUID/name resolver: managed routing returns a UUID,
            # while the official launch record may still contain a dataset name.
            legacy = dict(type='trace', origin_function='Read',status='success', generate_feedback_with_llm=False,
                          method_params={},method_return_value='legacy',_replay_ambiguous=True,_buffered_at=1)
            file = common._bridge_file('session')
            key = common._bridge_cache_key(str(dataset), 'session')
            common._write_json_file(file, {key:{'pending_entries':[copy.deepcopy(legacy)]}})
            common.append_warmup_entry(str(dataset),'session',payload['entry'])
            raw = common._load_json_file(file)[key]['pending_entries']
            message_id = raw[1]['_capture_id']
            loop = asyncio.get_running_loop()
            calls = []
            lost = [False]
            def transport(url, body=None, *, method='POST', timeout=12, **kwargs):
                calls.append((method, url))
                async def invoke():
                    response = await client.request(method,url,json=body)
                    assert response.status_code == 200, response.text
                    return response.json()
                receipt = asyncio.run_coroutine_threadsafe(invoke(),loop).result(12)
                if not lost[0]:
                    lost[0] = True
                    raise TimeoutError('synthetic response lost after COMMIT')
                return receipt
            common._json_http_request = transport
            result = await asyncio.to_thread(common.drain_warmup_entries,str(dataset),'session',budget_seconds=12)
            assert result == (0,2), result
            raw = common._load_json_file(file)
            assert raw[key]['pending_entries'][0] == legacy
            assert raw[key]['pending_entries'][1]['_capture_id'] == message_id
            assert raw[key]['pending_entries'][1]['_replay_ambiguous'] is True
            # Existing backoff prevents immediate retry; a new reader retains ID.
            assert await asyncio.to_thread(common.drain_warmup_entries,str(dataset),'session',budget_seconds=12) == (0,2)
            assert len(calls) == 1
            assert message_id in raw[key]['capture_retries']
            raw[key]['capture_retries'][message_id]['after'] = 0
            common._write_json_file(file,raw)
            importlib.reload(delivery)
            result = await asyncio.to_thread(common.drain_warmup_entries,str(dataset),'session',budget_seconds=12)
            assert result == (1,1), result
            assert common._load_json_file(file)[key]['pending_entries'] == [legacy]
            assert len(calls) == 2 and calls[0] == calls[1]
            assert len(await manager.get_agent_trace_session(user_id=str(user.id),session_id='session')) == 4
            common.append_warmup_entry(str(dataset),'session',payload['entry'])
            lost[0] = False
            assert await asyncio.to_thread(common.drain_warmup_entries,str(dataset),'session',budget_seconds=12) == (0,2)
            assert len(calls) == 3
            from project_backlog_worker import check
            common._read_map_record = lambda host: {'session_id':'session'}
            common.get_session_detail_via_http = lambda *a, **kw: {'session_id':'session','dataset_id':str(dataset)}
            result = await asyncio.to_thread(check,common,policy,str(dataset),'capture-test-host')
            assert result['unknown'] == 1 and result['reason'] == 'original_receipt_unavailable'
            assert calls[-1][0] == 'GET' and len(calls) == 4, calls
            assert common._load_json_file(file)[key]['pending_entries'] == [legacy]
            assert len(await manager.get_agent_trace_session(user_id=str(user.id),session_id='session')) == 5
            # Definite protocol rejection stops retry, retaining the exact item.
            common.append_warmup_entry(str(dataset),'session',payload['entry'])
            import urllib.error
            rejected = []
            def reject(url,*a,**kw):
                rejected.append(url)
                raise urllib.error.HTTPError(url,409,'synthetic conflict',{},None)
            common._json_http_request = reject
            assert await asyncio.to_thread(common.drain_warmup_entries,str(dataset),'session',budget_seconds=12) == (0,2)
            raw = common._load_json_file(file)
            assert len(raw[key]['capture_rejections']) == 1
            assert await asyncio.to_thread(common.drain_warmup_entries,str(dataset),'session',budget_seconds=12) == (0,2)
            assert len(rejected) == 1
            assert common._load_json_file(file)[key]['pending_entries'][0] == legacy
    final_traces = len(await manager.get_agent_trace_session(user_id=str(user.id),session_id='session'))
    await adapter.close()
    print('CAPTURE_RESULT=' + json.dumps(dict(concurrent_retry=True, atomic_rollback=True,
        reopen_idempotent=True, response_loss_recovered=True, payload_conflict=True, acl_preserved=True,
        native_trace_rows=final_traces, native_qa_rows=1)))
asyncio.run(scenario())
'''


class CaptureReceiptTests(unittest.TestCase):
    def test_pinned_native_sqlite_and_http_recovery(self):
        self.run_worker()

    def test_three_prepared_host_senders_keep_original_id_after_response_loss(self):
        import importlib.util
        from project_installation import prepare
        from project_control import private_write
        spec = importlib.util.spec_from_file_location('capture_builder',ROOT/'scripts/build_project_plugin.py')
        builder = importlib.util.module_from_spec(spec); spec.loader.exec_module(builder)
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            source = root/'plugin'
            builder.build(ROOT/'.cache/coding-memory-components/plugin-a176248914fdb32a78354854b300174203b15a5c/integrations/codex/plugins/cognee',source)
            private_write(root/'key',b'test-child')
            private_write(root/'control',b'c'*40)
            for kind in ('codex','pi','traex'):
                home = root/kind; home.mkdir()
                installed = prepare(source,root/'managed',kind+'-capture',
                    {'key_file':str(root/'key'),'control_token_file':str(root/'control')},
                    'http://127.0.0.1:18999','http://127.0.0.1:18998',str(home),host_kind=kind,python=sys.executable)
                with self.subTest(host=kind):
                    self.run_worker(str(Path(installed['plugin_dir'])/'scripts'))

    def run_worker(self, scripts=None):
        manifest = json.loads((ROOT / 'engine/coding-memory-components.json').read_text())['service']
        source = ROOT / '.cache/coding-memory-components' / ('service-' + manifest['commit'])
        with tempfile.TemporaryDirectory() as temporary:
            home = Path(temporary)
            env = {k:v for k,v in os.environ.items() if k in ('PATH','LANG','LC_ALL','TMPDIR')}
            env.update(HOME=str(home), SYSTEM_ROOT_DIRECTORY=str(home/'system'), DATA_ROOT_DIRECTORY=str(home/'data'),
                CACHE_ROOT_DIRECTORY=str(home/'cache'), LOGS_ROOT_DIRECTORY=str(home/'logs'), COGNEE_LOGS_DIR=str(home/'logs'),
                TELEMETRY_DISABLED='true', COGNEE_TRACING_ENABLED='false', PYTHON_DOTENV_DISABLED='1',
                LITELLM_LOCAL_MODEL_COST_MAP='true', PYTHONPATH=os.pathsep.join([str(source),str(ROOT/'engine/python')]))
            if scripts:
                env.update(COGNEE_BASE_URL='http://127.0.0.1:18999',COGNEE_API_KEY='test-child',
                    MEMLANE_PROJECT_CONTROL_URL='http://127.0.0.1:18998')
            result = subprocess.run([sys.executable,'-c',WORKER,str(source),str(home)]+([scripts] if scripts else []),cwd=home,env=env,
                                    capture_output=True,text=True,timeout=50)
            self.assertEqual(result.returncode,0,result.stdout[-1000:]+result.stderr[-4500:])
            report = json.loads(next(line.split('=',1)[1] for line in result.stdout.splitlines() if line.startswith('CAPTURE_RESULT=')))
            self.assertTrue(all(report[key] for key in ('concurrent_retry','atomic_rollback','reopen_idempotent','acl_preserved')))
