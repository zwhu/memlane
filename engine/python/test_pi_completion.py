import asyncio
import json
from pathlib import Path
import sys
import tempfile
from types import SimpleNamespace
import unittest

from codex_completion_endpoint import CompletionError
from pi_completion import checked_models, execute


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
        self.auth.write_text('{"synthetic":"do-not-export"}')
        self.auth.chmod(0o600)
        self.executable = self.root / 'pi'
        self.args = SimpleNamespace(agent_executable=self.executable, auth_home=self.home,
            bridge_provider='deepseek', model='fixture', timeout=3,
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


if __name__ == '__main__':
    unittest.main()
