"""Real owned child lifecycle against synthetic input, no SDK graph/model."""
import asyncio
import hashlib
import os
from pathlib import Path
import sys
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / 'engine/python'))
from cognee_code_process import EnolaRuntime, CodeProcessError, run_enola


@unittest.skipUnless(sys.platform == 'darwin', 'Native sandbox is macOS-specific')
class Tests(unittest.IsolatedAsyncioTestCase):
    async def execute(self, mode, action=None, timeout=5):
        with tempfile.TemporaryDirectory(prefix='code-process-') as temporary:
            parent = Path(temporary).resolve()
            scratch = parent / 'job'; scratch.mkdir(mode=0o700)
            repo = scratch / 'repository'; repo.mkdir(mode=0o700)
            denied = parent / 'source'; denied.mkdir(mode=0o700)
            (denied / 'secret').write_text('SYNTHETIC_DO_NOT_READ')
            binary = parent / 'synthetic-enola'
            binary.write_text('''#!/usr/bin/python3
import os,sys,time
from pathlib import Path
repo=Path(sys.argv[2])
assert sys.argv[1]=='--generate'
assert 'SYNTHETIC_CODE_PROCESS_SECRET' not in os.environ
try:
 (repo.parent.parent/'source'/'secret').read_text()
except PermissionError: pass
else: raise AssertionError('source read was allowed')
try:
 (repo.parent.parent/'outside-write').write_text('bad')
except PermissionError: pass
else: raise AssertionError('outside write was allowed')
(repo/'pid').write_text(str(os.getpid()))
MODE=''' + repr(mode) + '''
if MODE=='flood':
 while True: os.write(1,b'x'*8192)
if MODE=='fail':sys.exit(7)
if MODE=='sleep':time.sleep(30)
(repo/'.enola').mkdir()
(repo/'.enola'/'facts.jsonl').write_text('{}\\n')
''')
            binary.chmod(0o700)
            runtime = EnolaRuntime(str(binary), hashlib.sha256(binary.read_bytes()).hexdigest(), str(scratch), (str(denied),))
            value = SimpleNamespace(repository=str(repo), runtime=runtime)
            active = True
            async def check(**kwargs):
                if not active:
                    raise CodeProcessError('synthetic_authorization_revoked')
                return value
            original_spawn = asyncio.create_subprocess_exec
            spawned, release = asyncio.Event(), asyncio.Event()
            handles = []
            async def delayed_spawn(*args, **kwargs):
                process = await original_spawn(*args, **kwargs)
                handles.append(process)
                if action == 'spawn_cancel':
                    spawned.set()
                    await release.wait()
                return process
            with patch('cognee_code_guard.require', check), patch.dict(os.environ, {'SYNTHETIC_CODE_PROCESS_SECRET': 'never-forward'}), patch('asyncio.create_subprocess_exec', delayed_spawn):
                task = asyncio.create_task(run_enola([str(binary),'--generate',str(repo)],repo,timeout,'synthetic'))
                if action == 'spawn_cancel':
                    await asyncio.wait_for(spawned.wait(), 10)
                    task.cancel()
                    release.set()
                elif action:
                    async with asyncio.timeout(10):
                        while not (repo/'pid').exists():
                            if task.done(): await task
                            await asyncio.sleep(0.01)
                    if action=='cancel': task.cancel()
                    else: active=False
                try:
                    await task
                finally:
                    for handle in handles:
                        self.assertIsNotNone(handle.returncode, 'spawned child was not observed reaped')
                        with self.assertRaises(ProcessLookupError): os.kill(handle.pid,0)
                    if (repo/'pid').exists():
                        pid=int((repo/'pid').read_text())
                        with self.assertRaises(ProcessLookupError): os.kill(pid,0)
            self.assertTrue((repo/'.enola'/'facts.jsonl').is_file())
            self.assertFalse((parent/'outside-write').exists())

    async def test_success_is_sandboxed_and_reaped(self):
        await self.execute('success')

    async def test_failure_is_not_success(self):
        with self.assertRaisesRegex(CodeProcessError,'code_process_failed'):
            await self.execute('fail')

    async def test_timeout_reaps_child(self):
        with self.assertRaisesRegex(CodeProcessError,'code_process_timeout'):
            await self.execute('sleep',timeout=0.5)

    async def test_cancel_reaps_child(self):
        with self.assertRaises(asyncio.CancelledError):
            await self.execute('sleep',action='cancel')

    async def test_cancellation_during_spawn_still_reaps_child(self):
        with self.assertRaises(asyncio.CancelledError):
            await self.execute('sleep',action='spawn_cancel')

    async def test_revoke_reaps_child(self):
        with self.assertRaisesRegex(CodeProcessError,'synthetic_authorization_revoked'):
            await self.execute('sleep',action='revoke')

    async def test_output_limit_reaps_child(self):
        with self.assertRaisesRegex(CodeProcessError,'code_process_output_limit'):
            await self.execute('flood')


if __name__ == '__main__': unittest.main()
