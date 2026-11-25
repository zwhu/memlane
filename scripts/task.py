#!/usr/bin/env python3
"""Run one development task with a private temporary directory and disk budget."""
import os
from pathlib import Path
import shutil
import subprocess
import sys
import signal
import tempfile

ROOT = Path(__file__).resolve().parents[1]
GUARD = ROOT / 'scripts/disk-space.py'

def main():
    command = sys.argv[1:]
    if command[:1] == ['--']: command = command[1:]
    if not command: raise SystemExit('usage: python3 scripts/task.py -- command [args...]')
    if os.environ.get("MEMLANE_DISK_TASK") == "1":
        raise SystemExit(subprocess.call(command, cwd=ROOT))
    os.environ["MEMLANE_TASK_OWNER_PID"] = str(os.getpid())
    subprocess.run([sys.executable, str(GUARD)], check=True)
    def interrupted(signum, frame):
        raise SystemExit(128+signum)
    for signum in (signal.SIGINT, signal.SIGTERM): signal.signal(signum, interrupted)
    # Reports/screenshots belong in an explicit build output, never this scratch root.
    with tempfile.TemporaryDirectory(prefix='memlane-task-') as scratch:
        env = dict(os.environ, TMPDIR=scratch+'/', TMP=scratch, TEMP=scratch, MEMLANE_DISK_TASK="1")
        process = None
        try:
            process = subprocess.Popen(command, cwd=ROOT, env=env, start_new_session=True)
            while process.poll() is None:
                # Stop before crossing the 50 GiB floor, with a 5 GiB margin.
                if shutil.disk_usage(ROOT).free < 55 * 1024**3:
                    print('磁盘余量低于55 GiB，停止任务以保护50 GiB安全空间。', file=sys.stderr)
                    os.killpg(process.pid, signal.SIGTERM)
                    try: process.wait(timeout=10)
                    except subprocess.TimeoutExpired:
                        os.killpg(process.pid, signal.SIGKILL); process.wait()
                    raise SystemExit(1)
                try:
                    process.wait(timeout=2)
                    break
                except subprocess.TimeoutExpired:
                    pass
            result = process
        finally:
            if process is not None:
                # This wrapper owns the whole group, including accidentally backgrounded children.
                try: os.killpg(process.pid, signal.SIGTERM)
                except ProcessLookupError: pass
                if process.poll() is None:
                    try: process.wait(timeout=10)
                    except subprocess.TimeoutExpired:
                        os.killpg(process.pid, signal.SIGKILL); process.wait()
            subprocess.run([sys.executable, str(GUARD), '--finish'], check=False)
    raise SystemExit(result.returncode)

if __name__ == '__main__': main()
