"""Controlled native-protocol processes; never invoke Google or daily memory."""
import asyncio
import json
import os
from pathlib import Path
import sys
import tempfile
import time
from types import SimpleNamespace
import unittest

from aiohttp.test_utils import TestClient, TestServer
from antigravity_completion import execute
from agent_bridge_runtime import read_agent_defaults, snapshot_agent_defaults
from codex_completion_endpoint import CompletionError, create_app
from process_groups import group_exists

SCHEMA = {'type': 'object', 'properties': {'text': {'type': 'string'}},
          'required': ['text'], 'additionalProperties': False}

FAKE = r'''
import json,os,pathlib,signal,subprocess,sys,time
cfg=json.loads(pathlib.Path(__DISPATCH__).read_text())
record=pathlib.Path(cfg['record'])
mode=cfg['mode']
argv=sys.argv[1:]
def flag(key):return argv[argv.index(key)+1]
schema=json.loads(pathlib.Path(flag('--json-schema')).read_text())
work=pathlib.Path.cwd()
agent=(work/'.agents/agents/memlane-processing.md').read_text()
assert 'tools: []' in agent and 'plugins: []' in agent
assert not any(k in os.environ for k in ('COGNEE_API_KEY','LLM_ARGS','MEMLANE_PROJECT_CONTROL_TOKEN_FILE'))
def save_record():
    tmp=str(record)+'.tmp'
    pathlib.Path(tmp).write_text(json.dumps(saved))
    os.replace(tmp,record)
saved={'arguments':argv,'home':os.environ['HOME'],'cwd':str(work),'received':False,'model':flag('--model')}
save_record()
if mode=='startup-auth':
    print(json.dumps({'event':'result','result':{'conversation_id':'controlled-conversation','status':'ERROR','error':'authentication failed'}}),flush=True)
    sys.exit(1)
init={'event':'init','conversation_id':'controlled-conversation','init':{'cwd':str(work),'model':flag('--model'),'agent':'memlane-processing','tools':['read_file'] if mode=='bad-tools' else [],'json_schema':schema}}
print(json.dumps(init),flush=True)
line=sys.stdin.readline()
if not line:sys.exit(0)
payload=json.loads(line)
assert payload['event']=='user' and isinstance(payload['message']['content'],str)
assert payload['message']['content'] not in argv
saved['received']=True;save_record()
print(json.dumps({'event':'step_update','step_update':{'conversation_id':'controlled-conversation','step_type':'agent_response','state':'ACTIVE','text_delta':'working'}}),flush=True)
turn_started=time.monotonic()
if mode=='cancel':
    child=subprocess.Popen([sys.executable,'-c','import time;time.sleep(300)'])
    saved['child']=child.pid;saved['pid']=os.getpid();save_record()
    def interrupt(signum,frame):
        child.wait(timeout=2)
        saved['interrupted']=True;save_record()
        print(json.dumps({'event':'result','result':{'conversation_id':'controlled-conversation','status':'CANCELED','error':'interrupted'}}),flush=True)
        sys.exit(0)
    signal.signal(signal.SIGINT,interrupt)
    time.sleep(300)
if mode=='stream-stdout':
    for idx in range(5):
        time.sleep(0.07)
        print(json.dumps({'event':'step_update','step_update':{'conversation_id':'controlled-conversation','step_type':'agent_response','state':'ACTIVE','text_delta':f'part-{idx}'}}),flush=True)
if mode=='stream-stderr':
    for idx in range(5):
        time.sleep(0.07)
        print(f'agy heartbeat {idx}',file=sys.stderr,flush=True)
if mode=='unexpected-exit':
    print('agy unexpected crash',file=sys.stderr,flush=True)
    sys.exit(1)
saved['turn_elapsed']=time.monotonic()-turn_started;save_record()
output={'text':17 if mode=='bad-schema' else 'controlled answer'}
print(json.dumps({'event':'result','result':{'conversation_id':'controlled-conversation','status':'ERROR' if mode=='error' else 'SUCCESS','error':'authentication required' if mode=='error' else '', 'structured_output':output,'json_schema':schema}}),flush=True)
if mode=='hang-after-result':
    child=subprocess.Popen([sys.executable,'-c','import time;time.sleep(300)'])
    saved['child']=child.pid;saved['pid']=os.getpid();save_record()
    time.sleep(300)
sys.stdin.read()
'''


class AgyBridgeTests(unittest.IsolatedAsyncioTestCase):
    @classmethod
    def setUpClass(cls):
        cls._shared_temp = tempfile.TemporaryDirectory()
        cls._shared_root = Path(cls._shared_temp.name).resolve()
        cls._dispatch_path = cls._shared_root / 'dispatch.json'
        cls._shared_script = cls._shared_root / 'native-agy'
        cls._shared_script.write_text('#!' + sys.executable + '\n' + FAKE.replace('__DISPATCH__', repr(str(cls._dispatch_path))))
        cls._shared_script.chmod(0o700)

    @classmethod
    def tearDownClass(cls):
        cls._shared_temp.cleanup()

    async def asyncSetUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.root = Path(self.temporary.name).resolve()
        self.profile = self.root / 'original/.gemini/config'
        self.profile.mkdir(parents=True)
        self.settings = self.profile.parent / 'antigravity-cli/settings.json'
        self.settings.parent.mkdir()
        self.settings.write_text(json.dumps({'model': 'configured native model'}))

    async def asyncTearDown(self):
        self.temporary.cleanup()

    def args(self, mode='success'):
        record = self.root / (mode + '.json')
        self._dispatch_path.write_text(json.dumps({'mode': mode, 'record': str(record)}))
        return SimpleNamespace(bridge_kind='antigravity', model='stale profile label', auth_home=self.profile,
                               agent_executable=self._shared_script, codex_executable=self._shared_script, timeout=10,
                               owned_processes={}, record=record)

    async def test_native_default_snapshot_and_inflight_freeze(self):
        args = self.args()
        before = self.settings.read_bytes()
        current = snapshot_agent_defaults(args)
        self.assertEqual(current.model, 'configured native model')
        self.assertEqual(current.agent_defaults_snapshot['source'], 'agent_config')
        self.assertNotIn('reasoning_effort', current.agent_defaults_snapshot)
        self.settings.write_text(json.dumps({'model': 'later native model'}))
        self.assertIs(snapshot_agent_defaults(current), current)
        self.assertEqual(current.model, 'configured native model')
        self.assertEqual(snapshot_agent_defaults(args).model, 'later native model')
        self.settings.write_bytes(before)

    async def test_schema_and_plain_transport_use_private_context_and_stdin(self):
        for contract in (SCHEMA, None):
            args = self.args()
            source = self.settings.read_bytes()
            result = await execute(args, 'Trusted processing instructions', 'private user material', contract, SCHEMA)
            self.assertEqual(json.loads(result) if contract else result,
                             {'text': 'controlled answer'} if contract else 'controlled answer')
            recorded = json.loads(args.record.read_text())
            self.assertEqual(recorded['model'], 'configured native model')
            self.assertNotIn('private user material', recorded['arguments'])
            self.assertNotEqual(Path(recorded['home']), self.profile.parent.parent)
            self.assertFalse(Path(recorded['home']).exists())
            self.assertEqual(self.settings.read_bytes(), source)
            self.assertFalse(args.owned_processes)

    async def test_nonempty_tools_rejected_before_material_is_sent(self):
        args = self.args('bad-tools')
        with self.assertRaisesRegex(CompletionError, 'antigravity_tools_not_isolated'):
            await execute(args, 'system', 'private material', SCHEMA, SCHEMA)
        self.assertFalse(json.loads(args.record.read_text())['received'])
        self.assertFalse(args.owned_processes)

    async def test_schema_failure_and_native_failure_never_become_success(self):
        for mode, code in (('bad-schema', 'antigravity_schema_failed'), ('error', 'antigravity_authentication_required')):
            with self.subTest(mode=mode), self.assertRaisesRegex(CompletionError, code):
                args = self.args(mode)
                await execute(args, 'system', 'material', SCHEMA, SCHEMA)
            self.assertFalse(args.owned_processes)

    async def test_native_startup_auth_failure_never_receives_material(self):
        args = self.args('startup-auth')
        with self.assertRaisesRegex(CompletionError, 'antigravity_authentication_required'):
            await execute(args, 'system', 'private material', SCHEMA, SCHEMA)
        self.assertFalse(json.loads(args.record.read_text())['received'])
        self.assertFalse(args.owned_processes)

    async def test_cancel_interrupts_and_reaps_owned_child_group(self):
        args = self.args('cancel')
        task = asyncio.create_task(execute(args, 'system', 'material', SCHEMA, SCHEMA))
        for _ in range(100):
            if args.record.exists() and json.loads(args.record.read_text()).get('child'):
                break
            await asyncio.sleep(.02)
        else:
            task.cancel()
            await asyncio.gather(task, return_exceptions=True)
            self.fail('controlled child did not start')
        ids = list(args.owned_processes)
        task.cancel()
        with self.assertRaises(asyncio.CancelledError):
            await task
        self.assertTrue(json.loads(args.record.read_text())['interrupted'])
        self.assertFalse(any(group_exists(pid) for pid in ids))
        self.assertFalse(args.owned_processes)

    async def test_missing_native_model_never_uses_saved_profile_fallback(self):
        self.settings.write_text('{}')
        with self.assertRaisesRegex(Exception, 'agent_default_model_unavailable'):
            read_agent_defaults('antigravity', self.profile)

    def http_args(self, mode, timeout):
        args = self.args(mode)
        token = self.root / ('token-' + mode)
        token.write_text('synthetic-antigravity-token-for-tests')
        args.token_file = token
        args.timeout = timeout
        args.execute_request = execute
        return args

    async def test_slow_active_stdout_and_stderr_past_idle_timeout_complete_with_200(self):
        body = {'model': 'stale profile label', 'messages': [{'role': 'user', 'content': 'material'}],
                'response_format': {'type': 'json_schema', 'json_schema': {'schema': SCHEMA}}}
        for mode in ('stream-stdout', 'stream-stderr'):
            with self.subTest(mode=mode):
                args = self.http_args(mode, timeout=0.25)
                client = TestClient(TestServer(create_app(args)))
                await client.start_server()
                try:
                    started = time.monotonic()
                    response = await client.post('/v1/chat/completions', json=body,
                                                 headers={'Authorization': 'Bearer ' + args.token_file.read_text()})
                    elapsed = time.monotonic() - started
                    payload = await response.json()
                    self.assertEqual(response.status, 200, payload)
                    self.assertGreater(elapsed, args.timeout)
                    self.assertGreater(json.loads(args.record.read_text())['turn_elapsed'], args.timeout)
                    self.assertEqual(json.loads(payload['choices'][0]['message']['content']), {'text': 'controlled answer'})
                    self.assertFalse(args.owned_processes)
                finally:
                    await client.close()

    async def test_silent_hang_returns_504_interrupts_and_reaps_process_group(self):
        body = {'model': 'stale profile label', 'messages': [{'role': 'user', 'content': 'material'}],
                'response_format': {'type': 'json_schema', 'json_schema': {'schema': SCHEMA}}}
        args = self.http_args('cancel', timeout=0.3)
        client = TestClient(TestServer(create_app(args)))
        await client.start_server()
        try:
            response = await client.post('/v1/chat/completions', json=body,
                                         headers={'Authorization': 'Bearer ' + args.token_file.read_text()})
            payload = await response.json()
            self.assertEqual(response.status, 504, payload)
            self.assertEqual(payload['error']['code'], 'antigravity_timeout')
        finally:
            await client.close()
        recorded = json.loads(args.record.read_text())
        self.assertTrue(recorded['interrupted'])
        self.assertFalse(group_exists(recorded['pid']))
        with self.assertRaises(ProcessLookupError):
            os.kill(recorded['child'], 0)
        self.assertFalse(args.owned_processes)

    async def test_unexpected_exit_fails_promptly_without_waiting_for_idle_timeout(self):
        body = {'model': 'stale profile label', 'messages': [{'role': 'user', 'content': 'material'}],
                'response_format': {'type': 'json_schema', 'json_schema': {'schema': SCHEMA}}}
        args = self.http_args('unexpected-exit', timeout=10)
        client = TestClient(TestServer(create_app(args)))
        await client.start_server()
        try:
            started = time.monotonic()
            response = await client.post('/v1/chat/completions', json=body,
                                         headers={'Authorization': 'Bearer ' + args.token_file.read_text()})
            elapsed = time.monotonic() - started
            payload = await response.json()
            self.assertEqual(response.status, 502, payload)
            self.assertEqual(payload['error']['code'], 'antigravity_transport_closed_or_limit')
            self.assertLess(elapsed, 3.0)
            self.assertFalse(args.owned_processes)
        finally:
            await client.close()

    async def test_hang_after_result_hits_idle_timeout_with_504_and_reaps_group(self):
        args = self.args('hang-after-result')
        args.timeout = 0.3
        with self.assertRaises(CompletionError) as caught:
            await execute(args, 'system', 'material', SCHEMA, SCHEMA)
        self.assertEqual((caught.exception.code, caught.exception.status), ('antigravity_timeout', 504))
        recorded = json.loads(args.record.read_text())
        self.assertFalse(group_exists(recorded['pid']))
        with self.assertRaises(ProcessLookupError):
            os.kill(recorded['child'], 0)
        self.assertFalse(args.owned_processes)


if __name__ == '__main__':
    unittest.main()
