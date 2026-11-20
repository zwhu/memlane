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
from codex_completion_endpoint import CompletionError, create_app
from pi_completion import checked_models, execute
from process_groups import group_exists


FAKE = '''#!{python}
import json,sys,re,pathlib
args=sys.argv
extension=pathlib.Path(args[args.index('--extension')+1]).read_text()
digest=re.search('const schemaDigest = "([a-f0-9]+)"',extension)[1]
def emit(value):print(json.dumps(value),flush=True)
mode={mode!r}
for line in sys.stdin:
 r=json.loads(line); kind=r['type']; data=None
 if kind=='get_state':data={{'model':{{'provider':'deepseek','id':'fixture'}},'isStreaming':False,'isCompacting':False,'pendingMessageCount':0}}
 emit({{'type':'response','id':r['id'],'command':kind,'success':True,'data':data}})
 if kind=='prompt':
  if mode=='hang':continue
  if mode=='tool':emit({{'type':'tool_execution_start'}});continue
  for event in ({{'type':'memlane_schema_bound','schema_sha256':digest}},{{'type':'memlane_schema_response','schema_sha256':digest,'status':200}}):
   print(json.dumps(event),file=sys.stderr,flush=True)
  emit({{'type':'message_end','message':{{'role':'assistant','stopReason':'stop','content':[{{'type':'text','text':'{{"text":"ok"}}'}}]}}}})
  emit({{'type':'agent_end','willRetry':mode=='retry'}})
  emit({{'type':'agent_settled'}})
'''


class PiCompletionTests(unittest.IsolatedAsyncioTestCase):
    async def asyncSetUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.root = Path(self.temp.name)
        self.home = self.root / 'daily'
        self.home.mkdir(mode=0o700)
        self.auth = self.home / 'auth.json'
        (self.home / 'settings.json').write_text('{"defaultProvider":"deepseek","defaultModel":"fixture"}')
        self.auth.write_text('{"synthetic":"do-not-export"}')
        self.auth.chmod(0o600)
        self.executable = self.root / 'pi'
        self.args = SimpleNamespace(agent_executable=self.executable, auth_home=self.home,
            bridge_kind='pi', bridge_provider='deepseek', model='fixture', timeout=3,
            owned_processes={}, ownership_changed=lambda: None, workspace_root=self.root)
        self.schema = {'type':'object','properties':{'text':{'type':'string'}},'required':['text'],'additionalProperties':False}

    async def asyncTearDown(self):
        self.assertEqual(self.args.owned_processes, {})
        self.assertEqual(self.auth.read_text(), '{"synthetic":"do-not-export"}')
        self.assertEqual(list(self.root.glob('memlane-completion-*')), [])
        self.temp.cleanup()

    def fake(self, mode):
        self.executable.write_text(FAKE.format(python=sys.executable, mode=mode))
        self.executable.chmod(0o700)

    async def test_terminal_schema_and_auth_are_preserved(self):
        self.fake('success')
        result = await execute(self.args, 'synthetic system', 'synthetic prompt', None, self.schema)
        self.assertEqual(result, 'ok')

    async def test_tool_and_retry_never_return_a_success(self):
        for mode in ('tool', 'retry'):
            with self.subTest(mode=mode):
                self.fake(mode)
                with self.assertRaises(CompletionError):
                    await execute(self.args, 'system', 'prompt', None, self.schema)

    async def test_default_provider_model_and_thinking_are_used_in_native_rpc(self):
        (self.home/'settings.json').write_text('{"defaultProvider":"deepseek","defaultModel":"fixture","defaultThinkingLevel":"high"}')
        self.args.model,self.args.bridge_provider='obsolete-model','ollama'
        self.fake('success')
        source=self.executable.read_text().replace('args=sys.argv', '''args=sys.argv
import os
settings=json.loads((pathlib.Path(os.environ['PI_CODING_AGENT_DIR'])/'settings.json').read_text())
assert settings['defaultThinkingLevel']=='high'
assert args[args.index('--model')+1]=='fixture'
assert args[args.index('--provider')+1]=='deepseek' ''')
        self.executable.write_text(source)
        await execute(self.args,'system','prompt',None,self.schema)
        self.assertEqual((self.args.model,self.args.bridge_provider),('obsolete-model','ollama'))

    async def test_cancel_reaps_the_owned_process(self):
        self.fake('hang')
        task = asyncio.create_task(execute(self.args, 'system', 'prompt', None, self.schema))
        for _ in range(100):
            if self.args.owned_processes:
                break
            await asyncio.sleep(.01)
        self.assertTrue(self.args.owned_processes)
        task.cancel()
        with self.assertRaises(asyncio.CancelledError):
            await task

    def test_command_valued_models_are_rejected_without_mutation(self):
        path = self.home / 'models.json'
        raw = '{"providers":{"deepseek":{"apiKey":"!cat forbidden"}}}'
        path.write_text(raw)
        with self.assertRaisesRegex(CompletionError, 'pi_command_credential_unsupported'):
            checked_models(path)
        self.assertEqual(path.read_text(), raw)

    def write_idle_fake(self, mode, record_path=None):
        script = f'''#!{sys.executable}
import json, os, pathlib, re, subprocess, sys, time
args = sys.argv
extension = pathlib.Path(args[args.index('--extension') + 1]).read_text()
digest = re.search('const schemaDigest = "([a-f0-9]+)"', extension)[1]
record_file = {repr(str(record_path)) if record_path else None}
record = {{'aborted': False, 'grandchild': None, 'pid': os.getpid(), 'turn_elapsed': 0.0}}
def save():
    if record_file:
        tmp = record_file + '.tmp'
        pathlib.Path(tmp).write_text(json.dumps(record))
        os.replace(tmp, record_file)
def emit(value):
    print(json.dumps(value), flush=True)
mode = {mode!r}
for line in sys.stdin:
    r = json.loads(line); kind = r['type']; data = None
    if kind == 'get_state':
        data = {{'model': {{'provider': 'deepseek', 'id': 'fixture'}}, 'isStreaming': False, 'isCompacting': False, 'pendingMessageCount': 0}}
    if kind == 'abort':
        record['aborted'] = True
        save()
    emit({{'type': 'response', 'id': r['id'], 'command': kind, 'success': True, 'data': data}})
    if kind == 'prompt':
        turn_started = time.monotonic()
        if mode == 'stream-stdout':
            for idx in range(7):
                time.sleep(0.2)
                emit({{'type': 'message_update', 'delta': f'chunk-{{idx}}'}})
        elif mode == 'stream-stderr':
            for idx in range(7):
                time.sleep(0.2)
                print(f'pi heartbeat {{idx}}', file=sys.stderr, flush=True)
        elif mode == 'silent-hang':
            child = subprocess.Popen([sys.executable, '-c', 'import time; time.sleep(300)'])
            record['grandchild'] = child.pid
            save()
            emit({{'type': 'agent_start'}})
            continue
        elif mode == 'unexpected-exit':
            print('pi unexpected crash', file=sys.stderr, flush=True)
            sys.exit(1)
        record['turn_elapsed'] = time.monotonic() - turn_started
        save()
        for event in ({{'type': 'memlane_schema_bound', 'schema_sha256': digest}}, {{'type': 'memlane_schema_response', 'schema_sha256': digest, 'status': 200}}):
            print(json.dumps(event), file=sys.stderr, flush=True)
        emit({{'type': 'message_end', 'message': {{'role': 'assistant', 'stopReason': 'stop', 'content': [{{'type': 'text', 'text': '{{"text":"ok"}}'}}]}}}})
        emit({{'type': 'agent_end', 'willRetry': False}})
        emit({{'type': 'agent_settled'}})
'''
        self.executable.write_text(script)
        self.executable.chmod(0o700)

    def http_args(self, timeout):
        token = self.root / 'token'
        token.write_text('synthetic-pi-token-for-endpoint-tests')
        return SimpleNamespace(
            agent_executable=self.executable, codex_executable=self.executable,
            auth_home=self.home, bridge_kind='pi', bridge_provider='deepseek',
            model='fixture', timeout=timeout, token_file=token,
            execute_request=execute, owned_processes=self.args.owned_processes,
            ownership_changed=lambda: None, workspace_root=self.root,
        )

    async def test_slow_active_stdout_and_stderr_past_idle_timeout_complete_with_200(self):
        body = {'model': 'fixture', 'messages': [{'role': 'user', 'content': 'synthetic'}]}
        for mode in ('stream-stdout', 'stream-stderr'):
            with self.subTest(mode=mode):
                record_path = self.root / f'pi-{mode}.json'
                self.write_idle_fake(mode, record_path=record_path)
                args = self.http_args(timeout=0.9)
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
                    self.assertGreater(json.loads(record_path.read_text())['turn_elapsed'], args.timeout)
                    self.assertEqual(payload['choices'][0]['message']['content'], 'ok')
                finally:
                    await client.close()

    async def test_silent_hang_returns_504_aborts_and_reaps_process_group(self):
        body = {'model': 'fixture', 'messages': [{'role': 'user', 'content': 'synthetic'}]}
        record_path = self.root / 'pi-hang.json'
        self.write_idle_fake('silent-hang', record_path=record_path)
        args = self.http_args(timeout=0.9)
        client = TestClient(TestServer(create_app(args)))
        await client.start_server()
        try:
            response = await client.post('/v1/chat/completions', json=body,
                                         headers={'Authorization': 'Bearer ' + args.token_file.read_text()})
            payload = await response.json()
            self.assertEqual(response.status, 504, payload)
            self.assertEqual(payload['error']['code'], 'pi_timeout')
        finally:
            await client.close()
        record = json.loads(record_path.read_text())
        self.assertTrue(record['aborted'])
        self.assertIsNotNone(record['grandchild'])
        self.assertFalse(group_exists(record['pid']))
        with self.assertRaises(ProcessLookupError):
            os.kill(record['grandchild'], 0)

    async def test_unexpected_exit_fails_promptly_without_waiting_for_idle_timeout(self):
        body = {'model': 'fixture', 'messages': [{'role': 'user', 'content': 'synthetic'}]}
        self.write_idle_fake('unexpected-exit')
        args = self.http_args(timeout=10)
        client = TestClient(TestServer(create_app(args)))
        await client.start_server()
        try:
            started = time.monotonic()
            response = await client.post('/v1/chat/completions', json=body,
                                         headers={'Authorization': 'Bearer ' + args.token_file.read_text()})
            elapsed = time.monotonic() - started
            payload = await response.json()
            self.assertEqual(response.status, 502, payload)
            self.assertEqual(payload['error']['code'], 'pi_transport_closed_or_limit')
            self.assertLess(elapsed, 3.0)
        finally:
            await client.close()


if __name__ == '__main__':
    unittest.main()
