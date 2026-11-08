#!/usr/bin/env python3
"""G2a actual App/Enola/Rust; explicit writes only to guarded synthetic Git roots."""
import hashlib
import argparse
import functools
import http.server
from importlib.machinery import SourceFileLoader
import json
import os
from pathlib import Path
import socket
import threading
import time
import traceback
from types import SimpleNamespace
import uuid

ROOT = Path(__file__).resolve().parents[1]
OUT = ROOT / 'build/p4-code'
parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--fixture-pointer', type=Path, default=OUT / 'g2-app-fixture-path.txt')
args = parser.parse_args()
f = Path(args.fixture_pointer.read_text().strip())
meta = json.loads((f / 'source-info.json').read_text())
app = Path(meta['app'])
G = SourceFileLoader('codeupdatesguard', str(ROOT / 'scripts/p3-c2-common.py')).load_module()
G.validate_fixture(f, app, 'code')
U = SourceFileLoader('codeupdatesui', str(ROOT / 'scripts/p1-ui.py')).load_module()
repo = f / 'repo-a'
other = f / 'repo-b'
for source in (repo, other):
    assert source.is_dir() and not source.is_symlink()
    G.ensure_inside(source, f)
data = f / 'client/data'
ipc = f'/tmp/memlane-{os.getuid()}-{hashlib.sha256(str(data).encode()).hexdigest()[:12]}/service.sock'
def raw(op, **kw):
    with socket.socket(socket.AF_UNIX) as s:
        s.settimeout(125)
        s.connect(ipc)
        s.sendall((json.dumps({'protocol': 1, 'id': str(uuid.uuid4()), 'op': op, **kw}) + '\n').encode())
        return json.loads(s.makefile('rb', buffering=0).readline())
def call(op, **kw):
    r = raw(op, **kw)
    assert r['ok'], r
    return r.get('result')
def state():
    return next(c for c in call('code_list') if c['id'] == identifier)
def ready_changed(previous):
    c = state()
    return c if c['state'] == 'ready' and c['snapshot'] != previous else None
def write(relative, text):
    p = repo / relative
    G.ensure_inside(p, repo)
    assert not p.is_symlink()
    p.write_text(text)
    report['explicit_synthetic_writes'].append(relative)
def query(text):
    return call('code_query', code_query={'id': identifier, 'mode': 'symbols', 'text': text})
def display(text, proof):
    ui.set('code-query', text)
    ui.press_key('code-search')
    keys = {'code-node-' + n['id'] for n in proof['graph']['nodes']}
    U.wait(lambda: keys.issubset({n.get('key') for n in ui.nodes()}))

def choose(key, label):
    before = {n['id'] for n in ui.nodes()}
    ui.press_key(key)
    def option():
        nodes = ui.nodes()
        popup = {n['id'] for n in nodes if n.get('role') == 'popup' and n['id'] not in before}
        return next((n for n in nodes if n.get('label') == label and n.get('parent') in popup), None)
    n = U.wait(option)
    b = n['bounds']
    window = ui.req('windows')['windows'][0]['id']
    ui.req('input', window=window, events=[{'type': 'click', 'x': b['x'] + b['width']/2, 'y': b['y'] + b['height']/2}])
    ui.req('idle', timeout=1500)

class Handler(http.server.SimpleHTTPRequestHandler):
    def log_message(self, *args): pass

server = http.server.ThreadingHTTPServer(('127.0.0.1', meta['source_port']), functools.partial(Handler, directory=meta['source_root']))
thread = threading.Thread(target=server.serve_forever, daemon=True)
thread.start()

ui = None
def start_ui():
    global ui
    # Retain a partially constructed instance if Launch Services/UI setup fails.
    ui = U.UI.__new__(U.UI)
    U.UI.__init__(ui, app, f, ROOT / 'build/p4-code-g2-screenshots')

report = {'status': 'failed', 'actual_app': False, 'actual_enola': False, 'actual_rust': False,
          'model_called': False, 'synthetic_only': True, 'formal_distribution': False,
          'fixture': str(f), 'bundle': str(app), 'cases': [], 'explicit_synthetic_writes': []}
try:
    start_ui()
    report['actual_app'] = True
    ui.press('设置')
    U.wait(lambda: '启动服务' in ui.labels() or '停止服务' in ui.labels(), timeout=180)
    if '启动服务' in ui.labels():
        ui.press('启动服务')
    U.wait(lambda: call('health')['state'] == 'ready', timeout=95)
    report['actual_rust'] = True
    if not call('code_list'):
        for name, path in [('代码更新合成 A', repo), ('代码更新合成 B', other)]:
            ui.press('项目')
            ui.set('project-name', name)
            ui.set('project-path', str(path))
            ui.press_key('register-project')
            U.wait(lambda: any(s['name'] == name for s in call('scopes')))
        project = next(s for s in call('scopes') if s['name'] == '代码更新合成 A')
        ui.press('代码图谱')
        ui.press_key('code-new')
        choose('code-project', project['name'] + ' · ' + project['id'][:8])
        ui.set('code-path', str(repo))
        ui.press_key('code-enable')
    initial = U.wait(lambda: next((c for c in call('code_list') if c['worktree'] == str(repo.resolve()) and c['state'] == 'ready'), None), timeout=120)
    identifier = initial['id']
    assert initial['enabled'] and initial['state'] == 'ready'
    report['initial_state'] = initial
    ui.press('代码图谱')
    U.wait(lambda: any(n.get('key') == 'code-worktree-' + identifier for n in ui.nodes()))
    ui.press_key('code-worktree-' + identifier)
    proof = query('greet')
    display('greet', proof)
    before = initial['snapshot']
    write('src/greet.ts', 'export function revisedGreeting(name: string) { return "G2 " + name; }\n')
    write('src/main.ts', 'import { revisedGreeting } from "./greet";\nexport function run(){ return revisedGreeting("synthetic"); }\n')
    write('app/greet.py', 'def revised_greeting(name):\n    return "G2 " + name\n')
    write('src/added.ts', 'export function addedG2(){return 2;}\n')
    (other / 'unopened-g2.ts').write_text('export function neverScannedG2(){return 0;}\n')
    report['explicit_synthetic_writes'].append('repo-b/unopened-g2.ts')
    stale = raw('code_query', code_query={'id': identifier, 'mode': 'symbols', 'text': 'greet'})
    assert not stale['ok'] and stale['code'] == 'code_stale', stale
    report['cases'].append('actual_source_changes_reject_old_current')
    updated = U.wait(lambda: ready_changed(before), timeout=120)
    report['actual_enola'] = True
    proof = query('revised')
    assert {'src.revisedGreeting', 'app/greet.revised_greeting'}.issubset({n['name'] for n in proof['graph']['nodes']})
    assert all(n['file_sha'] == hashlib.sha256((repo / n['file']).read_bytes()).hexdigest() for n in proof['graph']['nodes'])
    assert updated['generation'] == initial['generation'] and updated['dataset'] == initial['dataset']
    assert len(call('code_list')) == 1
    display('revised', proof)
    ui.system_capture('g2-auto-updated-system.png')
    report['updated_state'] = updated
    report['updated_query'] = proof
    report['cases'].append('actual_app_automatic_coalesced_ts_python_update_and_unopened_project_exclusion')
    ui.press_key('code-manage')
    (repo / 'src/greet.ts').rename(repo / 'src/renamed.ts')
    (repo / 'app/greet.py').unlink()
    report['explicit_synthetic_writes'] += ['rename src/greet.ts -> src/renamed.ts', 'delete app/greet.py']
    write('src/main.ts', 'import { revisedGreeting } from "./renamed";\nexport function run(){ return revisedGreeting("synthetic"); }\n')
    renamed = U.wait(lambda: ready_changed(updated['snapshot']), timeout=120)
    proof = query('revised')
    assert len(proof['graph']['nodes']) == 1 and proof['graph']['nodes'][0]['file'] == 'src/renamed.ts'
    assert not {'src/greet.ts', 'app/greet.py'} & {p['path'] for p in renamed['manifest']['files']}
    display('revised', proof)
    ui.system_capture('g2-renamed-deleted-system.png')
    report['renamed_state'] = renamed
    report['renamed_query'] = proof
    report['cases'].append('actual_automatic_rename_delete_excludes_old_files_symbols')
    ui.press_key('code-manage')
    write('src/over-budget.ts', '//' + 'x' * (1024 * 1024))
    failed = U.wait(lambda: (c if (c := state())['state'] == 'failed' else None), timeout=90)
    assert failed['snapshot'] == renamed['snapshot']
    denied = raw('code_query', code_query={'id': identifier, 'mode': 'symbols', 'text': 'revised'})
    assert not denied['ok'], denied
    U.wait(lambda: '扫描失败' in ui.labels())
    ui.system_capture('g2-source-budget-failure-system.png')
    report['failed_state'] = failed
    report['failure_query'] = denied
    report['cases'].append('explicit_file_budget_fault_preserves_receipt_and_excludes_stale_query')
    # Source-unavailable requires explicit refresh; new content alone does not
    # silently grant retry after a capture that had no bounded digest.
    (repo / 'src/over-budget.ts').unlink()
    write('src/added.ts', 'export function recoveredG2(){return 3;}\n')
    retry_jobs = call('code_jobs', code_id=identifier)
    deadline = time.monotonic() + 17
    while time.monotonic() < deadline:
        assert state()['state'] == 'failed' and state()['observed_digest'] == ''
        time.sleep(.25)
    assert call('code_jobs', code_id=identifier) == retry_jobs
    report['cases'].append('source_capture_fault_does_not_rescan_without_explicit_retry')
    ui.press_key('code-refresh')
    recovered = U.wait(lambda: ready_changed(renamed['snapshot']), timeout=120)
    proof = query('recoveredG2')
    assert len(proof['graph']['nodes']) == 1
    report['recovered_state'] = recovered
    report['cases'].append('actual_app_explicit_retry_after_source_budget_fault')
    before_jobs = call('code_jobs', code_id=identifier)
    ui.quit(stop=True)
    ui = None
    persisted = json.loads((data / 'service-state.json').read_text())
    assert persisted['stopped'] is True
    write('src/added.ts', 'export function afterRestartG2(){return 4;}\n')
    start_ui()
    ui.press('设置')
    U.wait(lambda: '启动服务' in ui.labels(), timeout=95)
    assert json.loads((data / 'service-state.json').read_text())['stopped'] is True
    unavailable = False
    try:
        call('health')
    except (OSError, AssertionError):
        unavailable = True
    assert unavailable, 'explicitly stopped service unexpectedly running'
    report['cases'].append('actual_app_restart_preserves_user_stop_without_scanning')
    ui.press('启动服务')
    U.wait(lambda: call('health')['state'] == 'ready', timeout=95)
    restarted = U.wait(lambda: ready_changed(recovered['snapshot']), timeout=120)
    assert restarted['generation'] == recovered['generation'] and restarted['dataset'] == initial['dataset'], (initial, recovered, restarted)
    assert len(call('code_jobs', code_id=identifier)) == len(before_jobs) + 1, (before_jobs, call('code_jobs', code_id=identifier))
    assert len(query('afterRestartG2')['graph']['nodes']) == 1, query('afterRestartG2')
    ui.press('代码图谱')
    U.wait(lambda: any(n.get('key') == 'code-worktree-' + identifier for n in ui.nodes()))
    ui.press_key('code-worktree-' + identifier)
    display('afterRestartG2', query('afterRestartG2'))
    ui.system_capture('g2-restarted-system.png')
    report['restarted_state'] = restarted
    report['jobs'] = call('code_jobs', code_id=identifier)
    worker_data = Path(json.loads((f / 'client/service.json').read_text())['worker']['DataDir'])
    G.ensure_inside(worker_data, f)
    graph_directory = worker_data / 'code' / restarted['dataset']
    def retained_graphs():
        names = sorted(p.name for p in graph_directory.iterdir())
        return names if len(names) == 2 and restarted['snapshot'] in names and all(not n.startswith('.') for n in names) else None
    report['retained_graphs'] = U.wait(retained_graphs)
    assert call('health').get('code_cleanup_error') == ''
    assert len(report['jobs']) >= 4
    assert call('list', list_query={'limit': 5})['total'] == 0
    assert len(call('code_list')) == 1
    report['cases'].append('actual_app_resume_rebuilds_changed_source_and_keeps_generation_dataset_history')
    report['cases'].append('actual_service_keeps_two_graphs_without_deleting_job_source_receipt_history')
    ui.quit(stop=True)
    ui = None
    report['status'] = 'passed'
except Exception as error:
    report['error'] = repr(error)
    report['traceback'] = traceback.format_exc()
    if ui:
        try:
            ui.system_capture('g2-update-failure-system.png')
            report['failure_labels'] = ui.labels()
        except Exception as capture:
            report['capture_error'] = repr(capture)
finally:
    cleanup = []
    if ui or report['status'] != 'passed':
        try:
            call('stop')
        except (FileNotFoundError, ConnectionRefusedError):
            pass
        except Exception as error:
            cleanup.append('service stop: ' + repr(error))
        try:
            G.close_partial_ui(ui or SimpleNamespace(), f, app, 'code')
        except Exception as error:
            cleanup.append(repr(error))
    try:
        server.shutdown()
        server.server_close()
        thread.join(timeout=5)
        assert not thread.is_alive()
    except Exception as error:
        cleanup.append('source: ' + repr(error))
    report['cleanup_errors'] = cleanup
    if cleanup:
        report['status'] = 'failed'
    (OUT / 'g2-actual-app-updates.json').write_text(json.dumps(report, ensure_ascii=False, indent=2) + '\n')
print(json.dumps({k: v for k, v in report.items() if not (k.endswith('state') or k.endswith('query') or k in ('jobs', 'failure_labels'))}, ensure_ascii=False))
if report['status'] != 'passed':
    raise SystemExit(1)
