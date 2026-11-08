#!/usr/bin/env python3
"""Actual isolated App backup restore/reload and current Rust search; no model."""
import argparse
import functools
import hashlib
import http.server
import json
import os
import plistlib
import socket
import sqlite3
import subprocess
import threading
import traceback
import uuid
from importlib.machinery import SourceFileLoader
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--pointer', type=Path, default=ROOT/'build/p4-delivery/restore-fixture-path.txt')
parser.add_argument('--out', type=Path, default=ROOT/'build/p4-delivery/restore-actual-app.json')
parser.add_argument('--screenshots', type=Path, default=ROOT/'build/p4-restore-screenshots')
args = parser.parse_args()
fixture = Path(args.pointer.read_text().strip())
meta = json.loads((fixture/'source-info.json').read_text())
app = Path(meta['app'])
data = fixture/'client/data'
G = SourceFileLoader('restoreguard', str(ROOT/'scripts/p3-c2-common.py')).load_module()
G.validate_fixture(fixture, app, 'code')
U = SourceFileLoader('restoreui', str(ROOT/'scripts/p1-ui.py')).load_module()
ipc = f'/tmp/memlane-{os.getuid()}-{hashlib.sha256(str(data).encode()).hexdigest()[:12]}/service.sock'

def call(op, **params):
    with socket.socket(socket.AF_UNIX) as connection:
        connection.settimeout(125)
        connection.connect(ipc)
        connection.sendall((json.dumps({'protocol': 1, 'id': str(uuid.uuid4()), 'op': op, **params})+'\n').encode())
        result = json.loads(connection.makefile('rb', buffering=0).readline())
    assert result['ok'], result
    return result.get('result')

def scroll(key):
    node = U.wait(lambda: next((n for n in ui.nodes() if n.get('key') == key), None))
    if 'scrollIntoView' in node['actions']:
        ui.req('act', id=node['id'], action='scrollIntoView')
        ui.req('idle', timeout=1500)

def ready():
    if '启动服务' in ui.labels():
        node = next(n for n in ui.nodes() if n.get('label') == '启动服务' and 'press' in n.get('actions', []))
        if 'scrollIntoView' in node['actions']:
            ui.req('act', id=node['id'], action='scrollIntoView')
        ui.press('启动服务')
    U.wait(lambda: call('health')['state'] == 'ready', timeout=180)

def fingerprints(path, immutable=False):
    with sqlite3.connect(path.as_uri()+'?mode=ro'+('&immutable=1' if immutable else ''), uri=True) as database:
        database.execute('BEGIN')
        assert database.execute('PRAGMA integrity_check').fetchone()[0] == 'ok'
        result = {}
        for name, in database.execute("SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name"):
            quoted = '"'+name.replace('"', '""')+'"'
            rows = database.execute('SELECT * FROM '+quoted).fetchall()
            rows.sort(key=repr)
            result[name] = hashlib.sha256(json.dumps(rows, ensure_ascii=False, default=repr).encode()).hexdigest()
        return result

def edit(text):
    ui.press('恢复合成项目')
    U.wait(lambda: any(n.get('key') == 'memory-'+memory for n in ui.nodes()))
    ui.press_key('memory-'+memory)
    ui.press('编辑')
    U.wait(lambda: any(n.get('key') == 'memory-draft' for n in ui.nodes()))
    ui.set('memory-draft', text)
    ui.press('保存')

def reconnect_reloaded_window(old_pid):
    # Relaunch must actually create a new App process; preserve the launcher's
    # automation/native socket environment and reconnect to the new server.
    executable = app/'Contents/MacOS'/plistlib.loads((app/'Contents/Info.plist').read_bytes())['CFBundleExecutable']
    def pid():
        result = subprocess.run(['pgrep', '-f', '^'+str(executable)], capture_output=True, text=True)
        values = [int(value) for value in result.stdout.split()]
        return values[0] if len(values) == 1 and values[0] != old_pid else None
    new_pid = U.wait(pid, timeout=60)
    # Register the cleanup owner before any socket-close/reconnect can fail.
    ui.pid = new_pid
    ui.f.close()
    ui.s.close()
    ui.seq = 0
    def connect():
        connection = socket.socket(socket.AF_UNIX)
        connection.settimeout(30)
        try:
            connection.connect(ui.path)
        except OSError:
            connection.close()
            return None
        return connection
    ui.s = U.wait(connect, timeout=60)
    ui.f = ui.s.makefile('rb', buffering=0)
    U.wait(lambda: ui.req('windows').get('windows'))
    U.wait(lambda: ui.nodes())
    return new_pid

class Handler(http.server.SimpleHTTPRequestHandler):
    def log_message(self, *args):
        pass

server = http.server.ThreadingHTTPServer(('127.0.0.1', meta['source_port']), functools.partial(Handler, directory=meta['source_root']))
thread = threading.Thread(target=server.serve_forever, daemon=True)
thread.start()
ui = None
report = {'status': 'failed', 'actual_app': False, 'actual_rust': False,
          'synthetic_only': True, 'model_called': False, 'formal_distribution': False,
          'fault_fixtures': [], 'fixture': str(fixture), 'bundle': str(app)}
try:
    ui = U.UI.__new__(U.UI)
    U.UI.__init__(ui, app, fixture, args.screenshots)
    ui.press('设置')
    U.wait(lambda: '启动服务' in ui.labels() or '停止服务' in ui.labels(), timeout=180)
    ready()
    report['actual_app'] = True
    assert call('list', list_query={'limit': 5})['total'] == 0, 'fresh fixture required'
    ui.press('项目')
    ui.set('project-name', '恢复合成项目')
    ui.set('project-path', str(fixture/'repo-a'))
    ui.press_key('register-project')
    project = U.wait(lambda: next((s['id'] for s in call('scopes') if s['name'] == '恢复合成项目'), None))
    ui.press('恢复合成项目')
    ui.press('新增记忆')
    ui.set('memory-title', '恢复合成约定')
    ui.set('memory-draft', '本项目统一使用 pnpm，保留原始来源。')
    ui.press('保存')
    memory = U.wait(lambda: next((v['memory'] for v in call('list', list_query={'datasets': [project], 'limit': 5})['items'] if v['title'] == '恢复合成约定'), None))
    U.wait(lambda: call('get', memory=memory)['index_state'] == 'indexed', timeout=120)
    text2 = '本项目统一使用 pnpm，并提交 pnpm-lock.yaml；保留来源与版本。'
    edit(text2)
    U.wait(lambda: (v := call('get', memory=memory))['revision'] == 2 and v['index_state'] == 'indexed', timeout=120)
    ui.press('设置')
    scroll('backup-create')
    ui.press_key('backup-create')
    backup = U.wait(lambda: next((json.loads(p.read_text()) for p in (data/'backups/manual').glob('*/receipt.json')), None), timeout=90)
    backup_id = backup['id']
    golden = fingerprints(data/'backups/manual'/backup_id/'snapshot.sqlite', True)
    text3 = '本项目统一使用 pnpm，并提交 pnpm-lock.yaml；恢复之前增加缓存约定。'
    edit(text3)
    U.wait(lambda: (v := call('get', memory=memory))['revision'] == 3 and v['index_state'] == 'indexed', timeout=120)
    ui.press('设置')
    scroll('backup-restore-open-'+backup_id)
    ui.press_key('backup-restore-open-'+backup_id)
    U.wait(lambda: '恢复整个本机记忆库' in ui.labels())
    ui.system_capture('restore-confirm-system.png')
    ui.press_key('backup-restore-dismiss')
    assert call('get', memory=memory)['revision'] == 3
    assert not (data/'.restore-pending.json').exists() and not (data/'backups/restores').exists()
    scroll('backup-restore-open-'+backup_id)
    ui.press_key('backup-restore-open-'+backup_id)
    U.wait(lambda: '恢复整个本机记忆库' in ui.labels())
    old_pid = ui.pid
    try:
        ui.press_key('backup-restore-confirm')
    except EOFError:
        pass  # The confirmed App actually quits as part of its new UI epoch.
    restore_dir = U.wait(lambda: next((p.parent for p in (data/'backups/restores').glob('*/receipt.json')), None), timeout=120)
    receipt = json.loads((restore_dir/'receipt.json').read_text())
    report['receipt'] = receipt
    new_pid = reconnect_reloaded_window(old_pid)
    assert json.loads((data/'service-state.json').read_text())['stopped'] and not Path(ipc).exists()
    with sqlite3.connect((data/'memlane.sqlite').as_uri()+'?mode=ro', uri=True) as database:
        assert database.execute('SELECT current_revision,indexed_revision,generation FROM memories WHERE id=?', (memory,)).fetchone() == (2, 0, '')
        assert database.execute('SELECT state FROM jobs WHERE memory=? AND revision=2', (memory,)).fetchone() == ('pending',)
        assert database.execute('PRAGMA user_version').fetchone()[0] == 14
    restored = fingerprints(data/'memlane.sqlite')
    for name in ('versions', 'operations', 'activities', 'memory_metadata', 'organization_sources', 'usage_events'):
        assert restored[name] == golden[name], name
    # Actual graceful stop closed/checkpointed this fixture. Preserve every
    # archive byte: the separate IO tests cover captured main+WAL recovery.
    assert not (restore_dir/'previous-memlane.sqlite-wal').exists()
    assert fingerprints(restore_dir/'previous-memlane.sqlite', True)['versions'] != golden['versions']
    assert hashlib.sha256((data/'backups/manual'/backup_id/'snapshot.sqlite').read_bytes()).hexdigest() == backup['sha256']
    ui.press('设置')
    U.wait(lambda: '恢复记录 · '+receipt['id'] in ui.labels())
    ui.system_capture('restore-stopped-reloaded-system.png')
    ready()
    U.wait(lambda: call('get', memory=memory)['index_state'] == 'indexed', timeout=120)
    hits = call('search', datasets=[project], query=text2, limit=10)
    assert any(hit['version']['memory'] == memory and hit['version']['revision'] == 2 for hit in hits)
    assert not any(hit['version']['memory'] == memory and hit['version']['revision'] != 2 for hit in hits)
    report['actual_rust'] = True
    ui.press('恢复合成项目')
    ui.press_key('memory-'+memory)
    U.wait(lambda: text2 in ui.labels())
    ui.system_capture('restore-current-v2-system.png')
    ui.quit(stop=True)
    U.wait(lambda: json.loads((data/'service-state.json').read_text())['stopped'] and not Path(ipc).exists(), timeout=30)
    def reloaded_gone():
        try:
            os.kill(new_pid, 0)
            return False
        except ProcessLookupError:
            return True
    U.wait(reloaded_gone, timeout=30)
    ui = None
    report.update(status='passed', backup=backup_id, memory=memory, current_revision=2,
                  cancel_preserved_v3=True, pre_restore_v3_preserved=True, source_backup_unchanged=True,
                  actual_reload={'before_pid': old_pid, 'after_pid': new_pid},
                  restored_stopped=True, current_rust_v2=True, service_stopped=True)
except Exception as error:
    report['error'] = repr(error)
    report['traceback'] = traceback.format_exc()
    try:
        ui.system_capture('restore-failure-system.png')
        report['failure_labels'] = ui.labels()
    except Exception:
        pass
finally:
    cleanup = []
    try:
        call('stop')
    except (FileNotFoundError, ConnectionRefusedError):
        pass
    except Exception as error:
        cleanup.append('stop: '+repr(error))
    if ui:
        try:
            G.close_partial_ui(ui, fixture, app, 'code')
        except Exception as error:
            cleanup.append('ui: '+repr(error))
    try:
        server.shutdown()
        server.server_close()
        thread.join(timeout=5)
        assert not thread.is_alive()
    except Exception as error:
        cleanup.append('source: '+repr(error))
    report['cleanup_errors'] = cleanup
    if cleanup:
        report['status'] = 'failed'
    args.out.write_text(json.dumps(report, ensure_ascii=False, indent=2)+'\n')
print(json.dumps({k: v for k, v in report.items() if k not in ('failure_labels', 'traceback')}, ensure_ascii=False))
if report['status'] != 'passed':
    raise SystemExit(1)
