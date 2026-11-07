#!/usr/bin/env python3
"""隔离合成库：实际旧 v6 helper 拒绝 v7，并以 v6 备份恢复读取条件。"""
import fcntl, hashlib, json, os, shutil, socket, sqlite3, subprocess, tempfile, time, uuid
from pathlib import Path
ROOT = Path(__file__).resolve().parents[1]
OUT = ROOT / 'build/p3-schedules'
f = Path((OUT / 'fixture-path.txt').read_text().strip()).resolve()
old = Path((ROOT / 'build/p3-global/fixture-path.txt').read_text().strip()).resolve()
assert f.name.startswith('memlane-p3-schedules-app-') and old.name.startswith('memlane-p3-global-app-')
assert json.loads((f / 'source-info.json').read_text())['synthetic_only'] and json.loads((old / 'source-info.json').read_text())['synthetic_only']
assert json.loads((OUT / 'real-app.json').read_text())['status'] == 'passed'
helper = ROOT / 'build/p3-global-production/darwin-arm64/Memlane.app/Contents/Resources/bin/memlaned'
assert helper.is_file() and b'organization_plans' not in helper.read_bytes(), 'original E schema6 helper required'
target = json.loads((old / 'global-read-target.json').read_text())['memory']
parent = Path(tempfile.mkdtemp(prefix='memlane-p3-schedules-compat-'))
base = json.loads((old / 'client/service.json').read_text())
assert 'DataDir' in base['worker']
report = {'status': 'failed', 'synthetic_only': True, 'actual_old_helper': True, 'model_called': False, 'old_helper_sha256': hashlib.sha256(helper.read_bytes()).hexdigest(), 'index_rebuild_verified': False}
def copy_stopped(source, destination, version):
    data = source / 'client/data'
    with (data / 'memlane.sqlite.owner.lock').open('r+b') as lock:
        fcntl.flock(lock.fileno(), fcntl.LOCK_EX | fcntl.LOCK_NB)
        assert json.loads((data / 'service-state.json').read_text())['stopped'] and not (data / 'memlane.sqlite-wal').exists(), 'stopped checkpointed source required'
        with sqlite3.connect('file:' + str(data / 'memlane.sqlite') + '?mode=ro&immutable=1', uri=True) as db:
            assert db.execute('PRAGMA user_version').fetchone()[0] == version
            assert db.execute('PRAGMA integrity_check').fetchone()[0] == 'ok'
        shutil.copy2(data / 'memlane.sqlite', destination / 'memlane.sqlite')
def config(folder):
    cfg = json.loads(json.dumps(base)); cfg['root'] = str(folder); cfg['worker']['DataDir'] = str(folder / 'engine')
    path = folder / 'service.json'; path.write_text(json.dumps(cfg)); return path
def call(folder, op, **kw):
    path = f'/tmp/memlane-{os.getuid()}-{hashlib.sha256(str(folder).encode()).hexdigest()[:12]}/service.sock'
    with socket.socket(socket.AF_UNIX) as s:
        s.settimeout(8); s.connect(path); s.sendall((json.dumps({'protocol': 1, 'id': str(uuid.uuid4()), 'op': op, **kw}) + '\n').encode())
        response = json.loads(s.makefile('rb', buffering=0).readline())
    assert response['ok'], response
    return response.get('result')
proc = None
try:
    future = parent / 'v7'; future.mkdir(); copy_stopped(f, future, 7)
    rejected = subprocess.run([str(helper), '--config', str(config(future))], text=True, capture_output=True, timeout=10)
    assert rejected.returncode != 0 and 'database schema is newer than this client' in rejected.stderr, rejected.stderr
    report.update(v7_rejected=True, rejection=rejected.stderr.strip())
    legacy = parent / 'v6'; legacy.mkdir(); copy_stopped(old, legacy, 6)
    log = (parent / 'v6-helper.log').open('w')
    proc = subprocess.Popen([str(helper), '--config', str(config(legacy))], stdout=log, stderr=log)
    deadline = time.monotonic() + 95
    ready = False
    while time.monotonic() < deadline and proc.poll() is None:
        try: ready = call(legacy, 'health')['state'] == 'ready'
        except (OSError, AssertionError): pass
        if ready: break
        time.sleep(.1)
    assert ready, 'old helper did not prepare on restored v6 synthetic backup'
    v = call(legacy, 'get', memory=target['memory'])
    assert v['revision'] == 3 and v['text'] == target['text'] and v['experience']['conditions'] == target['experience']['conditions']
    call(legacy, 'stop', user_confirmed=True); proc.wait(timeout=30); proc = None; log.close()
    report.update(status='passed', v6_restored_get=True, global_revision=3, conditions_preserved=True, old_helper_stopped=True, temporary_root=str(parent))
except Exception as e:
    report['error'] = repr(e); raise
finally:
    if proc and proc.poll() is None: proc.terminate(); proc.wait(timeout=30)
    (OUT / 'old-version-compat.json').write_text(json.dumps(report, ensure_ascii=False, indent=2) + '\n')
print(json.dumps(report, ensure_ascii=False))
