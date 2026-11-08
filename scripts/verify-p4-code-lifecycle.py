#!/usr/bin/env python3
"""Actual App cancellation during Rust graph builds; synthetic sources only."""
import hashlib
from importlib.machinery import SourceFileLoader
import json
import os
from pathlib import Path
import socket
import time
import traceback
import uuid

ROOT = Path(__file__).resolve().parents[1]
OUT = ROOT / 'build/p4-code'
f = Path((OUT / 'g2-app-fixture-path.txt').read_text().strip())
meta = json.loads((f / 'source-info.json').read_text())
app = Path(meta['app'])
G = SourceFileLoader('codelifeguard', str(ROOT / 'scripts/p3-c2-common.py')).load_module()
G.validate_fixture(f, app, 'code')
U = SourceFileLoader('codelifeui', str(ROOT / 'scripts/p1-ui.py')).load_module()
data = f / 'client/data'
ipc = f'/tmp/memlane-{os.getuid()}-{hashlib.sha256(str(data).encode()).hexdigest()[:12]}/service.sock'
def call(op, **fields):
    with socket.socket(socket.AF_UNIX) as s:
        s.settimeout(125)
        s.connect(ipc)
        s.sendall((json.dumps({'protocol': 1, 'id': str(uuid.uuid4()), 'op': op, **fields}) + '\n').encode())
        r = json.loads(s.makefile('rb', buffering=0).readline())
    assert r['ok'], r
    return r.get('result')
def state():
    return next(c for c in call('code_list') if c['id'] == identifier)
def gone(pid):
    try:
        os.kill(pid, 0)
        return False
    except ProcessLookupError:
        return True
def group_gone(pid):
    try:
        os.killpg(pid, 0)
        return False
    except ProcessLookupError:
        return True
ui = None
report = {'status': 'failed', 'synthetic_only': True, 'actual_app': False, 'actual_rust': False,
          'actual_enola': False, 'model_called': False, 'protocol_fixture': False,
          'cases': [], 'fixture': str(f), 'bundle': str(app)}
try:
    ui = U.UI.__new__(U.UI)
    U.UI.__init__(ui, app, f, ROOT / 'build/p4-code-g2-lifecycle-screenshots')
    report['actual_app'] = True
    ui.press('设置')
    U.wait(lambda: '启动服务' in ui.labels() or '停止服务' in ui.labels(), timeout=95)
    if '启动服务' in ui.labels(): ui.press('启动服务')
    initial_health = U.wait(lambda: (h if (h := call('health'))['state'] == 'ready' else None), timeout=95)
    config = json.loads((f / 'client/service.json').read_text())
    worker_data = Path(config['worker']['DataDir'])
    G.ensure_inside(worker_data, f)
    initial = next(c for c in call('code_list') if c['enabled'])
    identifier = initial['id']
    source = Path(initial['worktree']) / 'src/cancel-load.ts'
    G.ensure_inside(source, f / 'repo-a')
    assert not source.is_symlink()
    graph_directory = worker_data / 'code' / initial['dataset']
    initial_budget = json.loads((data / 'service-state.json').read_text()).get('restarts', [])
    ui.press('代码图谱')
    U.wait(lambda: any(n.get('key') == 'code-worktree-' + identifier for n in ui.nodes()))
    ui.press_key('code-worktree-' + identifier)
    report['initial_state'] = initial
    for attempt in range(3):
        before = state()
        source.write_text(''.join(f'export function syntheticCancel{attempt}_{n}(){{return {n};}}\n' for n in range(3500)))
        source_sha = hashlib.sha256(source.read_bytes()).hexdigest()
        prior_stages = {p.name for p in graph_directory.iterdir() if p.name.startswith('.building-')}
        if not before['enabled']:
            ui.press_key('code-enable')
        else:
            ui.press_key('code-refresh')
        def active_build():
            c = state()
            h = call('health')
            stages = [p.name for p in graph_directory.iterdir() if p.name.startswith('.building-') and p.name not in prior_stages]
            job = next((j for j in call('code_jobs', code_id=identifier) if j['state'] == 'running' and j['generation'] == c['generation']), None)
            bound = job and any(p['path'] == 'src/cancel-load.ts' and p['sha256'] == source_sha for p in job['manifest']['files'])
            return (c, h, stages, job) if c['state'] == 'indexing' and h['worker_busy'] and stages and bound else None
        building, health, stages, job = U.wait(active_build, timeout=120)
        report['actual_enola'] = True
        report['actual_rust'] = True
        worker_pid = health['worker_pid']
        ui.press_key('code-disable')
        off = U.wait(lambda: (c if not (c := state())['enabled'] else None))
        assert off['generation'] == building['generation'] + 1 and off['snapshot'] == before['snapshot']
        ended = U.wait(lambda: next((j for j in call('code_jobs', code_id=identifier) if j['id'] == job['id'] and j['state'] == 'cancelled'), None))
        U.wait(lambda: gone(worker_pid), timeout=15)
        U.wait(lambda: group_gone(worker_pid), timeout=15)
        new_health = U.wait(lambda: (h if (h := call('health'))['state'] == 'ready' and h['worker_pid'] != worker_pid else None), timeout=95)
        settled = state()
        assert not settled['enabled'] and settled['generation'] == off['generation']
        assert settled['snapshot'] == before['snapshot'] and settled.get('receipt') == before.get('receipt')
        assert next(j for j in call('code_jobs', code_id=identifier) if j['id'] == job['id'])['state'] == 'cancelled'
        assert json.loads((data / 'service-state.json').read_text()).get('restarts', []) == initial_budget
        report['cases'].append({'case': 'actual_app_cancel_active_enola_rust_build', 'iteration': attempt + 1,
                                'job': job['id'], 'staging': stages, 'old_worker_pid': worker_pid,
                                'prior_stages': sorted(prior_stages), 'source_sha': source_sha,
                                'new_worker_pid': new_health['worker_pid'], 'old_worker_gone': True, 'old_process_group_gone': True,
                                'late_publication_excluded_after_reap': True,
                                'fault_budget_unchanged': True, 'terminal': ended['state'], 'generation': off['generation']})
    ui.system_capture('g2-three-active-build-cancels-system.png')
    source.unlink()
    ui.press_key('code-enable')
    current = U.wait(lambda: (c if (c := state())['state'] == 'ready' else None), timeout=120)
    proof = call('code_query', code_query={'id': identifier, 'mode': 'symbols', 'text': 'afterRestartG2'})
    assert len(proof['graph']['nodes']) == 1
    assert not any(p['path'] == 'src/cancel-load.ts' for p in current['manifest']['files'])
    def cleanup_done():
        names = sorted(p.name for p in graph_directory.iterdir())
        return names if len(names) <= 2 and current['snapshot'] in names and all(not p.startswith('.') for p in names) else None
    report['retained_graphs'] = U.wait(cleanup_done)
    assert call('health')['code_cleanup_error'] == ''
    assert not list((data / 'code-extraction').iterdir())
    assert call('list', list_query={'limit': 5})['total'] == 0
    report['cases'].append({'case': 'actual_resume_current_query_and_orphan_staging_cleanup', 'state': current,
                            'jobs': call('code_jobs', code_id=identifier)})
    ui.system_capture('g2-cancel-recovered-system.png')
    ui.quit(stop=True)
    ui = None
    report['status'] = 'passed'
except Exception as error:
    report['error'] = repr(error)
    report['traceback'] = traceback.format_exc()
    if ui:
        try: ui.system_capture('g2-lifecycle-failure-system.png')
        except Exception as capture: report['capture_error'] = repr(capture)
finally:
    cleanup = []
    if ui:
        try: call('stop')
        except (FileNotFoundError, ConnectionRefusedError): pass
        except Exception as error: cleanup.append('service stop: ' + repr(error))
        try: G.close_partial_ui(ui, f, app, 'code')
        except Exception as error: cleanup.append('App close: ' + repr(error))
    report['cleanup_errors'] = cleanup
    if cleanup: report['status'] = 'failed'
    (OUT / 'g2-actual-app-lifecycle.json').write_text(json.dumps(report, ensure_ascii=False, indent=2) + '\n')
print(json.dumps({k: v for k, v in report.items() if k not in ('initial_state', 'cases')}, ensure_ascii=False))
if report['status'] != 'passed': raise SystemExit(1)
