import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest


class OwnerLifecycleTests(unittest.TestCase):
    def test_real_child_exits_after_parent_disappears(self):
        with tempfile.TemporaryDirectory() as directory:
            marker = Path(directory) / 'exited'
            child = ('import os,signal,sys,time; from pathlib import Path; from owner_lifecycle import watch_owner; '
                     'signal.signal(signal.SIGTERM, lambda *_: (Path(sys.argv[1]).write_text("terminated"),sys.exit(0))); '
                     'watch_owner(); print("ready",flush=True); time.sleep(20)')
            parent = 'import subprocess,sys; subprocess.Popen([sys.executable,"-c",sys.argv[1],sys.argv[2]]); sys.stdin.read(1)'
            env = {**os.environ, 'PYTHONDONTWRITEBYTECODE': '1', 'PYTHONPATH': str(Path(__file__).parent.resolve())}
            process = subprocess.Popen([sys.executable, '-c', parent, child, str(marker)], env=env,
                stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
            try:
                self.assertEqual(process.stdout.readline().strip(), 'ready')
                output, error = process.communicate('\n', timeout=8)
                self.assertEqual(process.returncode, 0, error)
                self.assertEqual(marker.read_text(), 'terminated')
            finally:
                if process.poll() is None:
                    process.terminate()
                    process.communicate(timeout=10)
