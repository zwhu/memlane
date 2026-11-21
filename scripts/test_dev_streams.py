"""OVR01 regression at the launch seam; stdlib-only, no official database/model."""
import errno
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import Mock, patch

ROOT = Path(__file__).resolve().parents[1]


class ServiceStreamTests(unittest.TestCase):
    def test_missing_terminal_reader_cannot_propagate_notice_failure(self):
        spec=importlib.util.spec_from_file_location('dev',ROOT/'scripts/dev.py')
        dev=importlib.util.module_from_spec(spec);spec.loader.exec_module(dev)
        for error in (
            BrokenPipeError('broken pipe'),
            OSError(errno.ENXIO, 'Device not configured'),
            OSError(errno.EBADF, 'Bad file descriptor'),
            OSError(errno.EIO, 'Input/output error'),
        ):
            with self.subTest(error=error), patch('sys.stdout', Mock(write=Mock(side_effect=error))):
                dev.report_notice('synthetic lifecycle notice')

    def test_log_retention_keeps_live_inode_and_bounded_tail(self):
        spec=importlib.util.spec_from_file_location('dev',ROOT/'scripts/dev.py')
        dev=importlib.util.module_from_spec(spec);spec.loader.exec_module(dev)
        with tempfile.TemporaryDirectory() as temporary:
            path=Path(temporary)/'official.log'
            tail_expected = b'x' * 63 + b'\n'
            path.write_bytes(b'old-prefix-' + b'a' * 150 + b'\n' + tail_expected)
            inode=path.stat().st_ino
            fd=os.open(path,os.O_WRONLY|os.O_APPEND)
            try:
                dev.trim_service_log(path,limit=64)
                self.assertEqual(path.stat().st_ino,inode)
                self.assertEqual(Path(str(path)+'.previous').read_bytes(),tail_expected)
                self.assertEqual(Path(str(path)+'.previous').stat().st_mode&0o777,0o600)
                os.write(fd,b'new diagnostic')
                self.assertEqual(path.read_bytes(),b'new diagnostic')
            finally:
                os.close(fd)

    def test_log_retention_redacts_secrets_before_writing_previous(self):
        spec = importlib.util.spec_from_file_location('dev', ROOT / 'scripts/dev.py')
        dev = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(dev)
        with tempfile.TemporaryDirectory() as temporary:
            path = Path(temporary) / 'official.log'
            payload = (
                b'prefix-' + b'A' * 120 + b'\n'
                b'Authorization: Bearer secret-bearer-xyz\n'
                b'X-Api-Key: secret-apikey-123\n'
                b'key=sk-secret987654321\n'
                b'api_key=kv-secret-abc\n'
                b'proxy=socks5://u2:p2@10.0.0.8:1080\n'
            )
            path.write_bytes(payload)
            dev.trim_service_log(path, limit=220)
            prev = Path(str(path) + '.previous').read_text(encoding='utf-8')
            for secret in ('secret-bearer-xyz', 'secret-apikey-123', 'sk-secret987654321', 'kv-secret-abc', 'u2:p2'):
                self.assertNotIn(secret, prev)
            self.assertIn('Bearer [REDACTED]', prev)
            self.assertIn('X-Api-Key: [REDACTED]', prev)
            self.assertIn('sk-[REDACTED]', prev)
            self.assertIn('api_key=[REDACTED]', prev)
            self.assertIn('socks5://[REDACTED]@10.0.0.8:1080', prev)

    def test_log_retention_drops_split_partial_first_line_before_redaction(self):
        spec = importlib.util.spec_from_file_location('dev', ROOT / 'scripts/dev.py')
        dev = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(dev)
        with tempfile.TemporaryDirectory() as temporary:
            path = Path(temporary) / 'official.log'
            prefix = b'prefix-line\nAuthorization: Bearer '
            remainder = b'split-secret-token-8888\nsafe-tail-line\n'
            path.write_bytes(prefix + remainder)
            # Seek lands immediately after b'Bearer ', splitting the credential mid-line.
            dev.trim_service_log(path, limit=len(remainder))
            prev = Path(str(path) + '.previous').read_text(encoding='utf-8')
            self.assertNotIn('split-secret-token-8888', prev)
            self.assertEqual(prev, 'safe-tail-line\n')

            # Overlong single line with NO newline inside the bounded tail must be dropped entirely.
            overlong_path = Path(temporary) / 'overlong.log'
            overlong_suffix = b'overlong-secret-without-newline-8888'
            overlong_path.write_bytes(b'Authorization: Bearer ' + overlong_suffix)
            dev.trim_service_log(overlong_path, limit=len(overlong_suffix))
            overlong_prev = Path(str(overlong_path) + '.previous').read_bytes()
            self.assertNotIn(b'overlong-secret-without-newline-8888', overlong_prev)
            self.assertEqual(overlong_prev, b'')

    def test_worker_flush_survives_closed_launcher_stderr_reader(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            child = root / 'worker.py'
            child.write_text('''import multiprocessing.util, os, sys
sys.stderr.write("synthetic pending diagnostic")
try:
    multiprocessing.util._flush_std_streams()
except BrokenPipeError:
    print("BROKEN_PIPE_STDERR_FLUSH", flush=True)
    os._exit(17)
print("worker_flush_ok", flush=True)
''')
            driver = root / 'driver.py'
            driver.write_text('''import importlib.util,json,os,pathlib,sys
spec=importlib.util.spec_from_file_location("dev",sys.argv[1]);dev=importlib.util.module_from_spec(spec);spec.loader.exec_module(dev)
reader,writer=os.pipe();os.close(reader);os.dup2(writer,2);os.close(writer)
process=dev.launch_owned([sys.executable,sys.argv[2]],cwd=pathlib.Path(sys.argv[2]).parent,log_file=pathlib.Path(sys.argv[3]))
print(json.dumps({"worker_exit":process.wait(timeout=5)}),flush=True)
''')
            log = root / 'official.log'
            result = subprocess.run([sys.executable, str(driver), str(ROOT / 'scripts/dev.py'), str(child), str(log)],
                                    capture_output=True, text=True, timeout=10)
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
            self.assertIn('"worker_exit": 0', result.stdout, result.stdout)
            self.assertIn('worker_flush_ok', log.read_text())
            self.assertIn('synthetic pending diagnostic', log.read_text())
            self.assertEqual(os.stat(log).st_mode & 0o777, 0o600)

    def test_owned_child_detaches_stdin_to_devnull_and_spawns_multiprocessing_worker(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            child = root / 'worker.py'
            child.write_text('''import json, multiprocessing as mp, os

def worker_main():
    pass

if __name__ == '__main__':
    fd0 = os.fstat(0)
    null_stat = os.stat(os.devnull)
    assert (fd0.st_dev, fd0.st_ino) == (null_stat.st_dev, null_stat.st_ino), (
        (fd0.st_dev, fd0.st_ino),
        (null_stat.st_dev, null_stat.st_ino),
    )
    ctx = mp.get_context('spawn')
    proc = ctx.Process(target=worker_main)
    proc.start()
    proc.join(timeout=15)
    assert proc.exitcode == 0, f'unexpected spawn exitcode: {proc.exitcode}'
    print(json.dumps({'spawn_ok': True}), flush=True)
''')
            driver = root / 'driver.py'
            driver.write_text('''import importlib.util, json, os, pathlib, pty, sys
spec = importlib.util.spec_from_file_location('dev', sys.argv[1])
dev = importlib.util.module_from_spec(spec)
spec.loader.exec_module(dev)
master, slave = pty.openpty()
process = None
try:
    os.dup2(slave, 0)
    os.close(slave)
    slave = -1
    process = dev.launch_owned(
        [sys.executable, sys.argv[2]],
        cwd=pathlib.Path(sys.argv[2]).parent,
        log_file=pathlib.Path(sys.argv[3]),
    )
finally:
    if slave >= 0:
        os.close(slave)
    os.close(master)
print(json.dumps({'worker_exit': process.wait(timeout=20)}), flush=True)
''')
            log = root / 'official.log'
            result = subprocess.run([sys.executable, str(driver), str(ROOT / 'scripts/dev.py'), str(child), str(log)],
                                    capture_output=True, text=True, timeout=30)
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
            self.assertIn('"worker_exit": 0', result.stdout, result.stdout)
            self.assertIn('"spawn_ok": true', log.read_text())
            self.assertEqual(os.stat(log).st_mode & 0o777, 0o600)


if __name__ == '__main__':
    unittest.main()
