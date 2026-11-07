#!/usr/bin/env python3
"""D1 已停服合成库证据核对，真实窗口重启和 Rust 当前/过期结果补验；不调用模型。"""
import fcntl, hashlib, json, os, socket, sqlite3, uuid
from pathlib import Path
from importlib.machinery import SourceFileLoader
ROOT = Path(__file__).resolve().parents[1]
OUT = ROOT / 'build/p3-schedules'
f = Path((OUT / 'fixture-path.txt').read_text().strip())
meta = json.loads((f / 'source-info.json').read_text()); app = Path(meta['app'])
G = SourceFileLoader('schedulepresentguard', str(ROOT / 'scripts/p3-c2-common.py')).load_module()
G.validate_fixture(f, app, phase='schedules')
main = json.loads((OUT / 'real-app.json').read_text())
assert main['status'] == 'passed' and main['fixture'] == str(f.resolve())
data = f / 'client/data'
assert json.loads((data / 'service-state.json').read_text())['stopped']
report = {'status': 'failed', 'actual_app': True, 'actual_system_window': True, 'synthetic_only': True, 'model_called': False, 'fixture': str(f.resolve()), 'identifier': meta['identifier']}
ui = None
try:
    # Inspect the sole-owner stopped/checkpointed database. Bind both discovery
    # snapshots to immutable plan-operation receipts, then read terminal states.
    with (data / 'memlane.sqlite.owner.lock').open('r+b') as lock:
        fcntl.flock(lock.fileno(), fcntl.LOCK_EX | fcntl.LOCK_NB)
        assert not (data / 'memlane.sqlite-wal').exists()
        with sqlite3.connect('file:' + str(data / 'memlane.sqlite') + '?mode=ro&immutable=1', uri=True) as db:
            assert db.execute('PRAGMA user_version').fetchone()[0] == 7
            assert db.execute('PRAGMA integrity_check').fetchone()[0] == 'ok'
            receipts = [json.loads(r[0]) for r in db.execute('SELECT receipt FROM organization_plan_operations WHERE plan=?', (main['plan'],))]
            terminal = []
            for key, expected in [('normal_run', 'completed'), ('cancel_run', 'cancelled')]:
                run = main[key]
                receipt = next(p for p in receipts if p['generation'] == run['generation'])
                assert receipt['enabled'] and receipt['next_run_ms'] == run['due_ms']
                saved = db.execute('SELECT r.generation,r.due_ms,r.task,t.state FROM organization_plan_runs r JOIN organization_tasks t ON t.id=r.task WHERE r.id=? AND r.plan=?', (run['run'], main['plan'])).fetchone()
                assert saved == (run['generation'], run['due_ms'], run['task'], expected), saved
                terminal.append(dict(run, state=saved[3], bound_operation_receipt=True))
            assert not db.execute('SELECT 1 FROM organization_results WHERE task=?', (main['cancel_run']['task'],)).fetchone()
            assert db.execute('SELECT generation,enabled,next_run_ms FROM organization_plans WHERE id=?', (main['plan'],)).fetchone() == (4, 0, 0)
            assert db.execute('SELECT count(*) FROM organization_tasks').fetchone()[0] == 2
            report.update(normal_run=terminal[0], cancel_run=terminal[1], cancelled_result_absent=True, final_generation=4, final_disabled=True)
    U = SourceFileLoader('schedulepresentui', str(ROOT / 'scripts/p1-ui.py')).load_module()
    service_root = Path(json.loads((f / 'client/service.json').read_text())['root'])
    assert service_root.resolve() == data.resolve(), 'fixture service identity mismatch'
    ipc = f'/tmp/memlane-{os.getuid()}-{hashlib.sha256(str(service_root).encode()).hexdigest()[:12]}/service.sock'
    def call(op, **kw):
        with socket.socket(socket.AF_UNIX) as s:
            s.settimeout(8); s.connect(ipc)
            s.sendall((json.dumps({'protocol': 1, 'id': str(uuid.uuid4()), 'op': op, **kw}) + '\n').encode())
            r = json.loads(s.makefile('rb', buffering=0).readline())
        assert r['ok'], r
        return r.get('result')
    def visible(key): return any(n.get('key') == key for n in ui.nodes())
    ui = U.UI(app, f, ROOT / 'build/p3-schedules-screenshots', extra_env={'PI_CODING_AGENT_DIR': str(f / 'pi-agent')})
    ui.press('设置'); U.wait(lambda: '启动服务' in ui.labels()); ui.press('启动服务')
    U.wait(lambda: call('health')['state'] == 'ready', timeout=95)
    plans = call('schedules'); assert len(plans) == 1 and not plans[0]['enabled'] and plans[0]['generation'] == 4
    runs = call('schedule_runs', schedule=main['plan']); assert len(runs) == 2
    assert {r['state'] for r in runs} == {'completed', 'cancelled'}
    ui.press_key('schedule-settings'); U.wait(lambda: visible('schedule-plan-' + main['plan'])); ui.press_key('schedule-plan-' + main['plan'])
    U.wait(lambda: all(visible('schedule-open-' + r['run']) for r in runs))
    labels = ui.labels()
    assert all(any(expected in label for label in labels) for expected in ['执行已完成 · 审核与应用见任务', '已取消'])
    ui.system_capture('final-plan-history-system.png')
    summary = call('get', memory=main['real_rust_memory']); assert summary['derived']['state'] == 'stale'
    task = call('organization_get', organization_task=main['normal_task'])
    source_ids = [i['memory'] for i in task['snapshot']['inputs']]
    current = next(call('get', memory=m) for m in source_ids if call('get', memory=m)['revision'] == 2)
    U.wait(lambda: call('get', memory=current['memory'])['index_state'] == 'indexed', timeout=95)
    hits = call('search', datasets=[current['dataset']], query=current['text'], limit=50)
    assert any(h['version']['memory'] == current['memory'] and h['version']['revision'] == 2 for h in hits)
    assert all(h['version']['memory'] != summary['memory'] for h in hits)
    ui.press('D1 合成计划项目'); U.wait(lambda: visible('memory-' + current['memory'])); ui.press_key('memory-' + current['memory'])
    U.wait(lambda: any('v2' in label for label in ui.labels())); ui.system_capture('final-current-source-system.png')
    assert len(call('organization_list')['items']) == 2, 'disabled-plan restart must not create a model task'
    ui.quit(stop=True); ui = None
    assert json.loads((data / 'service-state.json').read_text())['stopped'] and not Path(ipc).exists()
    report.update(status='passed', history_persisted=True, source_current_revision=2, source_rust_current=True, old_summary_stale=True, stale_summary_excluded=True, task_count=2, service_stopped=True)
except Exception as e:
    report['error'] = repr(e); raise
finally:
    if ui: ui.quit(stop=True)
    (OUT / 'presentation.json').write_text(json.dumps(report, ensure_ascii=False, indent=2) + '\n')
print(json.dumps(report, ensure_ascii=False))
