#!/usr/bin/env python3
"""Actual App/Rust and System Settings background approval; synthetic roots only."""
import argparse
import functools
import hashlib
import http.server
import json
import os
import plistlib
import socket
import subprocess
import threading
import traceback
import uuid
from importlib.machinery import SourceFileLoader
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--out', type=Path, default=ROOT / 'build/p4-delivery/background-actual-app.json')
parser.add_argument('--resume', type=Path, help='Reuse the stopped, successfully cleaned synthetic v1 baseline')
args = parser.parse_args()
f = Path((ROOT / 'build/p4-delivery/background-fixture-path.txt').read_text().strip()).resolve()
meta = json.loads((f / 'source-info.json').read_text())
app = Path(meta['app'])
G = SourceFileLoader('backgroundguard', str(ROOT / 'scripts/p3-c2-common.py')).load_module()
G.validate_fixture(f, app, 'code')
bundle = plistlib.loads((app / 'Contents/Info.plist').read_bytes())
name = bundle['CFBundleName']
assert name in ('MemlaneBackground', 'MemlaneBackgroundFixed', 'MemlaneBackgroundFinal')
U = SourceFileLoader('backgroundui', str(ROOT / 'scripts/p1-ui.py')).load_module()
B = SourceFileLoader('backgroundtoggle', str(ROOT / 'scripts/p4-background-common.py')).load_module()
E = SourceFileLoader('backgroundevidence', str(ROOT / 'scripts/p4-diskfull-common.py')).load_module()
evidence = E.Evidence(args.out)
data = Path(json.loads((f / 'runtime-config.json').read_text())['root']) / 'data'
ipc = Path(f'/tmp/memlane-{os.getuid()}-{hashlib.sha256(str(data).encode()).hexdigest()[:12]}/service.sock')
ui, original, toggle = None, None, B.BackgroundToggle(name)
report = {'status': 'failed', 'actual_app': False, 'actual_rust': False, 'actual_system_settings': True, 'synthetic_only': True, 'model_called': False, 'formal_distribution': False, 'fixture': str(f), 'app': str(app), 'identifier': meta['identifier']}


def call(op, **kw):
    with socket.socket(socket.AF_UNIX) as connection:
        connection.settimeout(5)
        connection.connect(str(ipc))
        connection.sendall((json.dumps({'protocol': 1, 'id': str(uuid.uuid4()), 'op': op, **kw}) + '\n').encode())
        response = json.loads(connection.makefile('rb', buffering=0).readline())
    assert response['ok'], response
    return response.get('result')


def bundle_status():
    p = subprocess.run([str(app / 'Contents/MacOS' / bundle['CFBundleExecutable']), '--p0-action', 'status'], cwd=f, env={'PATH': '/usr/bin:/bin', 'HOME': str(f / 'ui-home')}, capture_output=True, text=True, timeout=20)
    assert p.returncode == 0, p.stderr
    result = next(json.loads(line) for line in p.stdout.splitlines() if line.startswith('{"action"'))
    assert result['action'] == 'status' and 'error' not in result, result
    return result['system_state']


def incarnation(pid):
    p = subprocess.run(['ps', '-p', str(pid), '-o', 'lstart=,comm='], capture_output=True, text=True, check=False)
    return {'pid': pid, 'identity': p.stdout.strip()} if p.returncode == 0 else None


class H(http.server.SimpleHTTPRequestHandler):
    def log_message(self, *args):
        pass


server = http.server.ThreadingHTTPServer(('127.0.0.1', meta['source_port']), functools.partial(H, directory=meta['source_root']))
thread = threading.Thread(target=server.serve_forever, daemon=True)
thread.start()
try:
    if args.resume:
        previous = json.loads(args.resume.read_text())
        assert previous['fixture'] == str(f) and previous['checkbox_original_restored'] and previous['cleanup_errors'] == []
        assert json.loads((data / 'service-state.json').read_text())['stopped'] and not ipc.exists()
        report['continued_from'] = str(args.resume.resolve())
    else:
        assert not (data / 'memlane.sqlite').exists(), 'fresh isolated library required'
    ui = U.UI.__new__(U.UI)
    U.UI.__init__(ui, app, f, ROOT / 'build' / (args.out.stem + '-screenshots'))
    report['actual_app'] = True
    ui.press('设置')
    if args.resume:
        U.wait(lambda: '启动服务' in ui.labels(), timeout=180)
        ui.press('启动服务')
    U.wait(lambda: call('health')['state'] == 'ready', timeout=180)
    if not args.resume:
        ui.press('项目')
        ui.set('project-name', '后台许可合成项目')
        ui.set('project-path', str(f / 'repo-a'))
        ui.press_key('register-project')
    project = U.wait(lambda: next((s['id'] for s in call('scopes') if s['name'] == '后台许可合成项目'), None))
    ui.press('后台许可合成项目')
    text = '本项目使用 pnpm；撤销后台许可不会改写原文、来源与版本。'
    if not args.resume:
        ui.press('新增记忆')
        ui.set('memory-title', '后台许可原文')
        ui.set('memory-draft', text)
        ui.press('保存')
    memory = U.wait(lambda: next((v['memory'] for v in call('list', list_query={'datasets': [project], 'limit': 5})['items'] if v['title'] == '后台许可原文'), None))
    U.wait(lambda: call('get', memory=memory)['index_state'] == 'indexed', timeout=120)
    assert any(h['version']['memory'] == memory for h in call('search', datasets=[project], query=text, limit=10))
    report['actual_rust'] = True
    report['worker_before'] = call('health')['worker_pid']
    all_before_memories = call('list', list_query={'limit': 100})
    report['memory_ids_before'] = sorted(v['memory'] for v in all_before_memories['items'])
    assert len(report['memory_ids_before']) == all_before_memories['total'], 'bounded complete synthetic list required'
    report['worker_incarnation_before'] = incarnation(report['worker_before'])
    p = subprocess.run(['lsof', '-t', str(ipc)], capture_output=True, text=True, check=True)
    helper_pids = set(int(value) for value in p.stdout.split())
    assert len(helper_pids) == 1, 'unique listening helper required'
    report['helper_incarnation_before'] = incarnation(helper_pids.pop())
    assert bundle_status() == 'enabled'
    toggle.open()
    original, all_before = U.wait(lambda: toggle.current(), timeout=30)
    assert original == 1, 'baseline system checkbox must be enabled'
    report['checkbox_identifier'] = 'background-switch-' + name
    report['checkbox_before'] = original
    report['foreign_switches_before_sha256'] = hashlib.sha256(json.dumps({k: v for k, v in all_before.items() if k != report['checkbox_identifier']}, sort_keys=True).encode()).hexdigest()
    ui.press('设置')
    toggle.set(0)
    U.wait(lambda: toggle.current()[0] == 0, timeout=20)
    report['checkbox_revoked'] = 0
    report['system_state_revoked'] = bundle_status()
    assert report['system_state_revoked'] == 'requires_approval'
    U.wait(lambda: not ipc.exists(), timeout=20)
    report['ipc_removed_by_system'] = True
    U.wait(lambda: incarnation(report['worker_before']) != report['worker_incarnation_before'], timeout=20)
    U.wait(lambda: incarnation(report['helper_incarnation_before']['pid']) != report['helper_incarnation_before'], timeout=20)
    report['original_worker_and_helper_exited'] = True
    U.wait(lambda: any('后台许可' in t or '批准' in t for t in ui.labels()) and not any('已就绪' in t for t in ui.labels()), timeout=10)
    ui.req('menu', path=['文件', '打开 Memlane'])
    ui.req('idle', timeout=2000)
    report['native_before_revoked_capture'] = {key: value for key, value in ui.native().items() if key in ('key_window', 'visible')}
    ui.system_capture('background-revoked-system.png')
    report['revoked_app_labels'] = ui.labels()
    assert not any('已就绪' in t for t in report['revoked_app_labels']), 'App retained ready after system background approval was revoked'
    U.wait(lambda: any('批准' in t or '许可' in t for t in ui.labels()), timeout=15)
    ui.press('新增记忆')
    ui.set('draft-scope', '后台许可合成项目')
    assert ui.node(key='draft-scope')['value'] == '后台许可合成项目', 'revoked draft scope mismatch'
    ui.set('memory-title', '许可撤销合成草稿')
    draft = '本段仅作为撤销期间未保存的合成草稿。'
    ui.set('memory-draft', draft)
    ui.press('保存')
    U.wait(lambda: any('保存失败' in t for t in ui.labels()), timeout=15)
    assert ui.node(key='memory-draft')['value'] == draft
    report['revoked_save_failed_draft_preserved'] = True
    ui.press('设置')
    toggle.set(original)
    U.wait(lambda: toggle.current()[0] == original, timeout=20)
    ui.press('准备 / 重试')
    U.wait(lambda: call('health')['state'] == 'ready', timeout=120)
    current = call('get', memory=memory)
    assert current['revision'] == 1 and current['text'] == text and current['original'] == text
    assert any(h['version']['memory'] == memory and h['version']['revision'] == 1 for h in call('search', datasets=[project], query=text, limit=10))
    assert call('list', list_query={'datasets': [project], 'limit': 5})['total'] == 1, 'failed draft created phantom knowledge'
    all_after_memories = call('list', list_query={'limit': 100})
    assert sorted(v['memory'] for v in all_after_memories['items']) == report['memory_ids_before'] and all_after_memories['total'] == all_before_memories['total'], 'failed save changed another scope'
    report['all_scope_memory_ids_preserved'] = True
    report['restored_current_revision'] = 1
    ui.req('menu', path=['文件', '打开 Memlane'])
    ui.system_capture('background-restored-system.png')
    ui.quit(stop=True)
    ui = None
    assert not ipc.exists() and json.loads((data / 'service-state.json').read_text())['stopped']
    report.update(status='passed', service_stopped=True)
except Exception as error:
    report['error'] = repr(error)
    report['traceback'] = traceback.format_exc()
finally:
    cleanup = []
    if original is not None:
        try:
            toggle.open()
            toggle.set(original)
            U.wait(lambda: toggle.current()[0] == original, timeout=20)
            after = toggle.current()[1]
            other = {k: v for k, v in after.items() if k != report['checkbox_identifier']}
            assert hashlib.sha256(json.dumps(other, sort_keys=True).encode()).hexdigest() == report['foreign_switches_before_sha256'], 'other background switches changed'
            report['checkbox_original_restored'] = True
        except Exception as error:
            cleanup.append('permission restore: ' + repr(error))
    if ui:
        try:
            G.close_partial_ui(ui, f, app, 'code')
        except Exception as error:
            cleanup.append('App cleanup: ' + repr(error))
    try:
        server.shutdown()
        server.server_close()
        thread.join(timeout=5)
        assert not thread.is_alive()
    except Exception as error:
        cleanup.append('source cleanup: ' + repr(error))
    report['cleanup_errors'] = cleanup
    if cleanup:
        report['status'] = 'failed'
    evidence.write(report)
    evidence.close()
print(json.dumps({k: v for k, v in report.items() if k not in ('traceback', 'revoked_app_labels')}, ensure_ascii=False))
assert report['status'] == 'passed'
