"""Synthetic stdio/HTTP regression checks; never calls a model or daily config."""
import asyncio
import json
import os
from pathlib import Path
import tempfile
import time
from types import SimpleNamespace
import unittest
from unittest.mock import patch

from aiohttp.test_utils import TestClient, TestServer
import agent_completion_endpoint
import codex_completion_endpoint as endpoint
from process_groups import group_exists

SCHEMA = {'type': 'object', 'properties': {'answer': {'type': 'string'}}, 'required': ['answer']}
FAKE = '''#!{python}
import json,os,sys,time
from pathlib import Path
root=Path(os.environ['HOME']).parent
config=(Path(os.environ['CODEX_HOME'])/'config.toml').read_text()
assert 'hooks = false' in config and 'plugins = false' in config and 'shell_tool = false' in config
assert not (Path(os.environ['CODEX_HOME'])/'daily-marker').exists()
def send(x): print(json.dumps(x),flush=True)
for line in sys.stdin:
 m=json.loads(line); method=m['method']; p=m.get('params',{{}})
 if method=='initialize':send({{'id':m['id'],'result':{{}}}})
 if method=='thread/start':
  send({{'id':m['id'],'result':{{'thread':{{'id':'t'}},'model':'fixture-model','modelProvider':'openai','runtimeWorkspaceRoots':[],'activePermissionProfile':{{'id':'memlane_completion'}}}}}})
 if method=='turn/start':
  send({{'id':m['id'],'result':{{'turn':{{'id':'u'}}}}}})
  if {mode!r}=='hang':continue
  if {mode!r}=='slow-success':time.sleep(.1)
  thread='wrong' if {mode!r}=='wrong-id' else 't'
  if {mode!r}=='plain':
   assert p['outputSchema']['properties']['text']['type']=='string'
   assert p['outputSchema']['additionalProperties'] is False
   item={{'type':'agentMessage','text':json.dumps({{'text':'test'}})}}
  else:
   item={{'type':'commandExecution'}} if {mode!r}=='tool' else {{'type':'agentMessage','text':json.dumps({{'answer':4 if {mode!r}=='bad-schema' else 'ok'}})}}
  send({{'method':'item/completed','params':{{'threadId':thread,'turnId':'u','item':item}}}})
  send({{'method':'turn/completed','params':{{'threadId':'t','turn':{{'id':'u','status':'completed','error':None}}}}}})
 if method=='turn/interrupt':
  send({{'id':m['id'],'result':{{}}}})
  send({{'method':'turn/completed','params':{{'threadId':'t','turn':{{'id':'u','status':'interrupted'}}}}}})
'''


class SchemaTests(unittest.TestCase):
    def test_strict_strengthens_preserves_source(self):
        schema = json.loads(json.dumps(SCHEMA))
        schema['properties']['optional'] = {'type': 'string', 'default': 'a'}
        converted = endpoint.strict_schema(schema)
        self.assertEqual(converted['required'], ['answer', 'optional'])
        self.assertFalse(converted['additionalProperties'])
        self.assertIn('default', schema['properties']['optional'])

    def test_existing_official_schemas(self):
        fixtures = Path(__file__).resolve().parent / 'testdata/coding-memory-schemas-ec6f408'
        manifest = json.loads((fixtures / 'manifest.json').read_text())
        for item in manifest['schemas']:
            with self.subTest(schema=item['file']):
                endpoint.strict_schema(json.loads((fixtures / item['file']).read_text()))

    def test_coding_union_transport_uses_supported_anyof_and_keeps_original_contract(self):
        path=Path(__file__).parent/'testdata/coding-memory-schemas-ec6f408/CodingAgentContextExtraction.json'
        source=json.loads(path.read_text())
        converted=endpoint.strict_schema(source)
        union=converted['$defs']['CodingCandidateLesson']['properties']['lesson']
        self.assertNotIn('oneOf',union)
        self.assertNotIn('discriminator',union)
        self.assertEqual(len(union['anyOf']),5)
        self.assertIn('oneOf',source['$defs']['CodingCandidateLesson']['properties']['lesson'])
        # Original validation remains authoritative even when transport differs.
        ambiguous={'lessons':[{'turn':1,'lesson':{'content':'ambiguous'}}]}
        with self.assertRaises(Exception): endpoint.validators.validate(ambiguous,source)

    def test_upstream_invalid_json_schema_is_a_client_error_not_retryable_502(self):
        raw={'codexErrorInfo':'other', 'message':json.dumps({'type':'error','status':400,
             'error':{'code':'invalid_json_schema','message':'PRIVATE MODEL TEXT'}})}
        error=endpoint.remote_error(raw)
        self.assertEqual((error.code,error.status),('codex_schema_unsupported',400))
        self.assertNotIn('PRIVATE',str(error))

    def test_open_dict_and_remote_ref_rejected(self):
        for schema in ({'type':'object','additionalProperties':{'type':'string'}}, {'$ref':'https://example.invalid/schema'}):
            with self.assertRaises(endpoint.CompletionError): endpoint.strict_schema(schema)

    def test_request_contract(self):
        body = {'model':'fixture-model','messages':[{'role':'user','content':'question'}],
                'response_format':{'type':'json_schema','json_schema':{'schema':SCHEMA}}}
        self.assertEqual(endpoint.parse_request(body, 'fixture-model')[2], SCHEMA)
        for changed in ({'stream':True}, {'model':'other'}, {'tools':[{}]}, {'response_format':{'type':'json_object'}}):
            with self.assertRaises(endpoint.CompletionError): endpoint.parse_request(body | changed, 'fixture-model')

    def test_activity_clock_and_cli_timeout_validation(self):
        from contextlib import redirect_stderr
        import io
        for bad in (0, -1, float('nan'), float('inf'), None, 'bad'):
            with self.assertRaises(ValueError):
                endpoint.ActivityClock(bad)
        base = ['--port', '8080', '--token-file', '/tmp/tok', '--agent-executable', '/tmp/bin',
                '--auth-home', '/tmp/home', '--bridge-kind', 'codex', '--model', 'fixture-model']
        parsed = agent_completion_endpoint.parse_args(base)
        self.assertEqual(parsed.timeout, 90)
        custom = agent_completion_endpoint.parse_args(base + ['--timeout', '15.5'])
        self.assertEqual(custom.timeout, 15.5)
        for bad in ('0', '-5', 'nan', 'inf'):
            with redirect_stderr(io.StringIO()), self.assertRaises(SystemExit):
                agent_completion_endpoint.parse_args(base + ['--timeout', bad])


class ExecutionTests(unittest.IsolatedAsyncioTestCase):
    async def asyncSetUp(self):
        import sys
        self.python = sys.executable
        self.temporary = tempfile.TemporaryDirectory()
        self.root = Path(self.temporary.name)
        self.auth = self.root / 'daily'
        self.auth.mkdir()
        (self.auth/'auth.json').write_text('{"synthetic":true}')
        (self.auth/'daily-marker').write_text('must not copy')
        (self.auth/'config.toml').write_text('model = "fixture-model"\n')
        (self.auth/'traecli.toml').write_text('model = "fixture-model"\n')
        self.executable = self.root / 'codex'
        self.args = SimpleNamespace(model='fixture-model',codex_executable=self.executable,auth_home=self.auth,timeout=2,diagnostics_dir=self.root/'diagnostics')
        self.processes = []
        self.peak_live = 0
        self.original_spawn = asyncio.create_subprocess_exec
        async def spawn(*args, **kwargs):
            p = await self.original_spawn(*args, **kwargs)
            self.processes.append(p)
            self.peak_live = max(self.peak_live, sum(child.returncode is None for child in self.processes))
            return p
        self.patcher = patch.object(endpoint.asyncio, 'create_subprocess_exec', spawn)
        self.patcher.start()

    async def asyncTearDown(self):
        self.patcher.stop()
        for p in self.processes:
            self.assertIsNotNone(p.returncode)
            with self.assertRaises(ProcessLookupError): os.killpg(p.pid, 0)
        self.temporary.cleanup()

    def fake(self, mode):
        self.executable.write_text(FAKE.format(python=self.python,mode=mode))
        self.executable.chmod(0o700)

    async def run_fake(self, mode):
        self.fake(mode)
        return await endpoint.execute(self.args, 'system', 'prompt', SCHEMA, endpoint.strict_schema(SCHEMA))

    async def test_success_process_exit_and_auth_unchanged(self):
        self.assertEqual(json.loads(await self.run_fake('success')), {'answer':'ok'})
        self.assertEqual((self.auth/'auth.json').read_text(), '{"synthetic":true}')

    async def test_native_request_inherits_default_effort_and_internal_memory_models(self):
        for kind in ('codex', 'traex'):
            with self.subTest(kind=kind):
                self.args.bridge_kind = kind
                self.args.model = 'obsolete-saved-model'
                filename = 'config.toml' if kind == 'codex' else 'traecli.toml'
                (self.auth/filename).write_text('model="fixture-model"\nmodel_reasoning_effort="medium"\nservice_tier="fast"\n')
                if kind == 'traex':
                    (self.auth/'cli').mkdir(exist_ok=True)
                    (self.auth/'cli/auth.json').write_text('{"synthetic":true}')
                self.fake('success')
                source = self.executable.read_text()
                if kind == 'traex':
                    source = source.replace("'openai'", "'trae'").replace("'CODEX_HOME'", "'TRAE_HOME'").replace("'config.toml'", "'traecli.toml'")
                source = source.replace('def send(x):', '''assert 'model_reasoning_effort = "medium"' in config
assert 'service_tier = "fast"' in config
assert 'extract_model = "fixture-model"' in config
assert 'consolidation_model = "fixture-model"' in config
def send(x):''')
                self.executable.write_text(source)
                self.assertEqual(json.loads(await endpoint.execute(self.args,'system','prompt',SCHEMA,endpoint.strict_schema(SCHEMA))),{'answer':'ok'})
                self.assertEqual(self.args.model,'obsolete-saved-model')

    async def test_traex_keeps_subscription_auth_in_its_own_cli_layout(self):
        self.fake('success')
        source = self.executable.read_text().replace("'openai'", "'trae'").replace("'CODEX_HOME'", "'TRAE_HOME'").replace("'config.toml'", "'traecli.toml'")
        self.executable.write_text(source)
        directory = self.auth / 'cli'
        directory.mkdir()
        (directory / 'auth.json').write_text('{"synthetic_traex":true}')
        self.args.bridge_kind = 'traex'
        result = await endpoint.execute(self.args, 'system', 'prompt', SCHEMA, endpoint.strict_schema(SCHEMA))
        self.assertEqual(json.loads(result), {'answer':'ok'})
        self.assertEqual((directory / 'auth.json').read_text(), '{"synthetic_traex":true}')

    async def test_validation_workspace_survives_unconfirmed_group_cleanup(self):
        import shutil
        self.fake('success')
        workspace=self.root/'validation-workspaces'
        workspace.mkdir(mode=0o700)
        self.args.workspace_root=workspace
        self.args.owned_processes={}
        terminate=endpoint.terminate
        try:
            with patch.object(endpoint,'terminate',side_effect=endpoint.CompletionError('codex_process_cleanup_unknown',503)):
                with self.assertRaises(endpoint.CompletionError):
                    await endpoint.execute(self.args,'s','p',SCHEMA,endpoint.strict_schema(SCHEMA))
            self.assertTrue(self.args.owned_processes)
            roots=list(workspace.glob('memlane-completion-*'))
            self.assertEqual(len(roots),1)
            self.assertTrue((roots[0]/'codex/auth.json').exists())
            self.assertEqual((roots[0]/'codex/auth.json').stat().st_mode&0o777,0o600)
        finally:
            for process in list(self.args.owned_processes.values()):
                await terminate(process)
                endpoint.track_process(self.args,process,False)
            shutil.rmtree(workspace)

    async def test_tool_identity_and_schema_rejected(self):
        for mode, code in [('tool','codex_unexpected_tool_execution'), ('wrong-id','codex_event_identity_invalid'), ('bad-schema','codex_schema_failed')]:
            with self.subTest(mode=mode):
                with self.assertRaises(endpoint.CompletionError) as caught: await self.run_fake(mode)
                self.assertEqual(caught.exception.code, code)

    async def test_startup_stderr_retained_privately_and_bounded(self):
        self.executable.write_text('#!' + self.python + '\nimport sys\nsys.stderr.write("synthetic-start-error" + "x" * 40000)\nsys.exit(1)\n')
        self.executable.chmod(0o700)
        with self.assertRaises(endpoint.CompletionError) as caught:
            await endpoint.execute(self.args, 's', 'p', SCHEMA, endpoint.strict_schema(SCHEMA))
        self.assertEqual(caught.exception.code, 'codex_transport_closed')
        diagnostic = self.args.diagnostics_dir / 'codex-start-failure.json'
        self.assertLessEqual(diagnostic.stat().st_size, 16384)
        self.assertEqual(diagnostic.stat().st_mode & 0o777, 0o600)
        record = json.loads(diagnostic.read_text())
        self.assertTrue(record['stderr'].startswith('synthetic-start-error'))
        self.assertEqual(record['failure_code'], 'codex_transport_closed')
        self.assertEqual(record['exit_code'], 1)

    async def test_timeout_interrupt_and_exit(self):
        self.args.timeout = .2
        with self.assertRaises(endpoint.CompletionError) as caught: await self.run_fake('hang')
        self.assertEqual(caught.exception.code, 'codex_timeout')

    async def test_cancel_interrupt_and_exit(self):
        self.fake('hang')
        task = asyncio.create_task(endpoint.execute(self.args, 's','p', SCHEMA,endpoint.strict_schema(SCHEMA)))
        await asyncio.sleep(.15)
        task.cancel()
        with self.assertRaises(asyncio.CancelledError): await task

    async def test_collect_stderr_fires_activity_past_retention_limit(self):
        stream = asyncio.StreamReader()
        hits = []
        for _ in range(6):
            stream.feed_data(b'x' * 4096)
        stream.feed_eof()
        retained = await endpoint.collect_stderr(stream, on_activity=lambda: hits.append(1))
        self.assertEqual(len(retained), 16384)
        self.assertGreaterEqual(len(hits), 6)

    def write_idle_fake(self, kind, mode, record_path=None):
        provider = 'trae' if kind == 'traex' else 'openai'
        home_env = 'TRAE_HOME' if kind == 'traex' else 'CODEX_HOME'
        config_name = 'traecli.toml' if kind == 'traex' else 'config.toml'
        script = f'''#!{self.python}
import json, os, subprocess, sys, time
from pathlib import Path
config = (Path(os.environ[{home_env!r}]) / {config_name!r}).read_text()
assert 'hooks = false' in config and 'plugins = false' in config
record_file = {repr(str(record_path)) if record_path else None}
record = {{'interrupted': False, 'grandchild': None, 'turn_elapsed': 0.0}}
def save():
    if record_file:
        tmp = record_file + '.tmp'
        Path(tmp).write_text(json.dumps(record))
        os.replace(tmp, record_file)
def send(x):
    print(json.dumps(x), flush=True)
mode = {mode!r}
for line in sys.stdin:
    m = json.loads(line); method = m['method']
    if method == 'initialize':
        send({{'id': m['id'], 'result': {{}}}})
    elif method == 'thread/start':
        send({{'id': m['id'], 'result': {{'thread': {{'id': 't'}}, 'model': 'fixture-model', 'modelProvider': {provider!r}, 'runtimeWorkspaceRoots': [], 'activePermissionProfile': {{'id': 'memlane_completion'}}}}}})
    elif method == 'turn/start':
        send({{'id': m['id'], 'result': {{'turn': {{'id': 'u'}}}}}})
        turn_started = time.monotonic()
        if mode == 'stream-stdout':
            for idx in range(7):
                time.sleep(0.2)
                send({{'method': 'item/reasoning/summaryTextDelta', 'params': {{'threadId': 't', 'turnId': 'u', 'delta': f'step-{{idx}}'}}}})
        elif mode == 'stream-stderr':
            for idx in range(7):
                time.sleep(0.2)
                sys.stderr.write(f'thinking heartbeat {{idx}}\\n')
                sys.stderr.flush()
        elif mode == 'silent-hang':
            child = subprocess.Popen([sys.executable, '-c', 'import time; time.sleep(300)'])
            record['grandchild'] = child.pid
            save()
            send({{'method': 'item/reasoning/summaryTextDelta', 'params': {{'threadId': 't', 'turnId': 'u', 'delta': 'started'}}}})
            continue
        elif mode == 'unexpected-exit':
            sys.stderr.write('fatal crash mid-turn\\n')
            sys.stderr.flush()
            sys.exit(1)
        record['turn_elapsed'] = time.monotonic() - turn_started
        save()
        send({{'method': 'item/completed', 'params': {{'threadId': 't', 'turnId': 'u', 'item': {{'type': 'agentMessage', 'text': json.dumps({{'answer': 'ok'}})}}}}}})
        send({{'method': 'turn/completed', 'params': {{'threadId': 't', 'turn': {{'id': 'u', 'status': 'completed', 'error': None}}}}}})
    elif method == 'turn/interrupt':
        record['interrupted'] = True
        save()
        send({{'id': m['id'], 'result': {{}}}})
        send({{'method': 'turn/completed', 'params': {{'threadId': 't', 'turn': {{'id': 'u', 'status': 'interrupted'}}}}}})
'''
        self.executable.write_text(script)
        self.executable.chmod(0o700)

    def prepare_http_args(self, kind, timeout):
        token = self.root / 'token'
        token.write_text('synthetic-token-for-endpoint-tests')
        if kind == 'traex':
            cli = self.auth / 'cli'
            cli.mkdir(exist_ok=True)
            (cli / 'auth.json').write_text('{"synthetic_traex":true}')
        return SimpleNamespace(
            model='fixture-model', codex_executable=self.executable, auth_home=self.auth,
            bridge_kind=kind, timeout=timeout, token_file=token,
            diagnostics_dir=self.root / 'diagnostics',
        )

    async def test_slow_active_stdout_and_stderr_past_idle_timeout_complete_with_200(self):
        request = {'model': 'fixture-model', 'messages': [{'role': 'user', 'content': 'q'}],
                   'response_format': {'type': 'json_schema', 'json_schema': {'schema': SCHEMA}}}
        for kind in ('codex', 'traex'):
            for mode in ('stream-stdout', 'stream-stderr'):
                with self.subTest(kind=kind, mode=mode):
                    record_path = self.root / f'active-{kind}-{mode}.json'
                    self.write_idle_fake(kind, mode, record_path=record_path)
                    args = self.prepare_http_args(kind, timeout=0.9)
                    client = TestClient(TestServer(endpoint.create_app(args)))
                    await client.start_server()
                    try:
                        started = time.monotonic()
                        response = await client.post('/v1/chat/completions', json=request,
                                                     headers={'Authorization': 'Bearer ' + args.token_file.read_text()})
                        elapsed = time.monotonic() - started
                        payload = await response.json()
                        self.assertEqual(response.status, 200, payload)
                        self.assertGreater(elapsed, args.timeout)
                        self.assertGreater(json.loads(record_path.read_text())['turn_elapsed'], args.timeout)
                        self.assertEqual(json.loads(payload['choices'][0]['message']['content']), {'answer': 'ok'})
                    finally:
                        await client.close()

    async def test_silent_hang_returns_504_interrupts_and_reaps_process_group(self):
        request = {'model': 'fixture-model', 'messages': [{'role': 'user', 'content': 'q'}],
                   'response_format': {'type': 'json_schema', 'json_schema': {'schema': SCHEMA}}}
        for kind in ('codex', 'traex'):
            with self.subTest(kind=kind):
                record_path = self.root / f'hang-{kind}.json'
                self.write_idle_fake(kind, 'silent-hang', record_path=record_path)
                args = self.prepare_http_args(kind, timeout=0.9)
                client = TestClient(TestServer(endpoint.create_app(args)))
                await client.start_server()
                before = len(self.processes)
                try:
                    response = await client.post('/v1/chat/completions', json=request,
                                                 headers={'Authorization': 'Bearer ' + args.token_file.read_text()})
                    payload = await response.json()
                    self.assertEqual(response.status, 504, payload)
                    self.assertEqual(payload['error']['code'], 'codex_timeout')
                finally:
                    await client.close()
                record = json.loads(record_path.read_text())
                self.assertTrue(record['interrupted'])
                self.assertIsNotNone(record['grandchild'])
                spawned = self.processes[before:]
                self.assertEqual(len(spawned), 1)
                self.assertFalse(group_exists(spawned[0].pid))
                with self.assertRaises(ProcessLookupError):
                    os.kill(record['grandchild'], 0)

    async def test_unexpected_exit_fails_promptly_without_waiting_for_idle_timeout(self):
        request = {'model': 'fixture-model', 'messages': [{'role': 'user', 'content': 'q'}],
                   'response_format': {'type': 'json_schema', 'json_schema': {'schema': SCHEMA}}}
        for kind in ('codex', 'traex'):
            with self.subTest(kind=kind):
                self.write_idle_fake(kind, 'unexpected-exit')
                args = self.prepare_http_args(kind, timeout=10)
                client = TestClient(TestServer(endpoint.create_app(args)))
                await client.start_server()
                try:
                    started = time.monotonic()
                    response = await client.post('/v1/chat/completions', json=request,
                                                 headers={'Authorization': 'Bearer ' + args.token_file.read_text()})
                    elapsed = time.monotonic() - started
                    payload = await response.json()
                    self.assertEqual(response.status, 502, payload)
                    self.assertEqual(payload['error']['code'], 'codex_transport_closed')
                    self.assertLess(elapsed, 3.0)
                finally:
                    await client.close()

    async def test_http_auth_completion_and_capacity(self):
        self.fake('success')
        token = self.root/'token'
        token.write_text('synthetic-token-for-endpoint-tests')
        self.args.token_file = token
        client = TestClient(TestServer(endpoint.create_app(self.args)))
        await client.start_server()
        try:
            response = await client.get('/health')
            self.assertEqual(response.status,200)
            response = await client.post('/v1/chat/completions',json={})
            self.assertEqual(response.status,401)
            request = {'model':'fixture-model','messages':[{'role':'user','content':'q'}],
                       'response_format':{'type':'json_schema','json_schema':{'schema':SCHEMA}}}
            response = await client.post('/v1/chat/completions',json=request,headers={'Authorization':'Bearer '+token.read_text()})
            self.assertEqual(response.status,200)
            result = await response.json()
            self.assertEqual(json.loads(result['choices'][0]['message']['content']),{'answer':'ok'})
        finally: await client.close()

    async def test_official_connection_plain_completion(self):
        # Fixed Cognee test_llm_connection -> response_model=str -> no format.
        # The endpoint must execute this request, not fake a connectivity answer.
        self.fake('plain')
        token = self.root / 'token'
        token.write_text('synthetic-token-for-endpoint-tests')
        self.args.token_file = token
        client = TestClient(TestServer(endpoint.create_app(self.args)))
        await client.start_server()
        try:
            response = await client.post('/v1/chat/completions', json={
                'model': 'fixture-model',
                'messages': [{'role': 'system', 'content': 'Respond with test'},
                             {'role': 'user', 'content': 'test'}]},
                headers={'Authorization': 'Bearer ' + token.read_text()})
            payload = await response.json()
            self.assertEqual(response.status, 200, payload)
            self.assertEqual(payload['choices'][0]['message']['content'], 'test')
            self.assertEqual(len(self.processes), 1)
        finally:
            await client.close()

    async def test_two_execution_slots_bound_parallel_batch(self):
        self.fake('slow-success')
        token = self.root / 'token'
        token.write_text('synthetic-token-for-endpoint-tests')
        self.args.token_file = token
        client = TestClient(TestServer(endpoint.create_app(self.args)))
        await client.start_server()
        request = {'model':'fixture-model','messages':[{'role':'user','content':'q'}],
                   'response_format':{'type':'json_schema','json_schema':{'schema':SCHEMA}}}
        async def post():
            response = await client.post('/v1/chat/completions', json=request,
                                        headers={'Authorization':'Bearer '+token.read_text()})
            self.assertEqual(response.status, 200, await response.json())
        try:
            await asyncio.gather(*(post() for _ in range(12)))
            self.assertEqual(len(self.processes), 12)
            self.assertEqual(self.peak_live, 2)
            response = await client.get('/health')
            health = await response.json()
            self.assertEqual(health['active_or_waiting'], 0)
            self.assertEqual(health['max_concurrency'], 2)
            self.assertEqual(health['max_inflight'], 32)
        finally:
            await client.close()

    async def test_capacity_and_server_shutdown_reap_process(self):
        self.fake('hang')
        self.args.timeout = 10
        token = self.root / 'token'
        token.write_text('synthetic-token-for-endpoint-tests')
        self.args.token_file = token
        client = TestClient(TestServer(endpoint.create_app(self.args)))
        await client.start_server()
        request = {'model':'fixture-model','messages':[{'role':'user','content':'q'}],
                   'response_format':{'type':'json_schema','json_schema':{'schema':SCHEMA}}}
        headers = {'Authorization': 'Bearer ' + token.read_text()}
        async def post():
            return await client.post('/v1/chat/completions', json=request, headers=headers)
        pending = [asyncio.create_task(post()) for _ in range(32)]
        try:
            for _ in range(30):
                response = await client.get('/health')
                if (await response.json())['active_or_waiting'] == 32: break
                await asyncio.sleep(.02)
            response = await post()
            self.assertEqual(response.status,429)
            self.assertEqual((await response.json())['error']['code'],'codex_busy')
            self.assertEqual(len(self.processes), 2)
            self.assertEqual(self.peak_live, 2)
        finally:
            await client.close()
            for task in pending: task.cancel()
            await asyncio.gather(*pending, return_exceptions=True)

if __name__ == '__main__': unittest.main()
