#!/usr/bin/env python3
"""D1 真实 App/traex/Rust 计划触发与取消；不修改系统时钟。"""
import datetime, hashlib, json, os, socket, uuid
from pathlib import Path
from importlib.machinery import SourceFileLoader
ROOT = Path(__file__).resolve().parents[1]
OUT = ROOT / 'build/p3-schedules'
f = Path((OUT / 'fixture-path.txt').read_text().strip())
meta = json.loads((f / 'source-info.json').read_text()); app = Path(meta['app'])
G = SourceFileLoader('scheduleguard', str(ROOT / 'scripts/p3-c2-common.py')).load_module()
G.validate_fixture(f, app, phase='schedules')
U = SourceFileLoader('scheduleui', str(ROOT / 'scripts/p1-ui.py')).load_module()
data = f / 'client/data'
ipc = f'/tmp/memlane-{os.getuid()}-{hashlib.sha256(str(data).encode()).hexdigest()[:12]}/service.sock'
def call(op, **kw):
    with socket.socket(socket.AF_UNIX) as s:
        s.settimeout(8); s.connect(ipc)
        s.sendall((json.dumps({'protocol': 1, 'id': str(uuid.uuid4()), 'op': op, **kw}) + '\n').encode())
        r = json.loads(s.makefile('rb', buffering=0).readline())
    assert r['ok'], r
    return r.get('result')
def visible(key): return any(n.get('key') == key for n in ui.nodes())
def choose(key, label):
    U.wait(lambda: visible(key))
    before = {n['id'] for n in ui.nodes()}
    ui.press_key(key)
    def popup_option():
        nodes = ui.nodes(); popups = {n['id'] for n in nodes if n.get('role') == 'popup' and n['id'] not in before}
        return next((n for n in nodes if n.get('label') == label and n.get('parent') in popups), None)
    option = U.wait(popup_option)
    # MyGo 0.7.4 exposes Select option text inside the popup without a press
    # action. Deliver an actual pointer click at this popup option's bounds.
    b = option['bounds']; window = ui.req('windows')['windows'][0]['id']
    ui.req('input', window=window, events=[{'type': 'click', 'x': b['x'] + b['width']/2, 'y': b['y'] + b['height']/2}]); ui.req('idle', timeout=2000)
def next_clock():
    # Explicit acceptance choice for a real upcoming wall-clock minute. This
    # is not a product default, a clock fixture, or a short interval override.
    return (datetime.datetime.now(datetime.timezone.utc) + datetime.timedelta(minutes=2)).strftime('%H:%M')
def open_plan(pid):
    ui.press('设置'); ui.press_key('schedule-settings')
    U.wait(lambda: visible('schedule-plan-' + pid)); ui.press_key('schedule-plan-' + pid)
    U.wait(lambda: visible('schedule-toggle'))
ui = None
report = {'status': 'failed', 'actual_app': True, 'real_organizer': 'traex', 'real_rust': True, 'system_clock_changed': False, 'clock_fixture': False, 'model_fixture': False, 'synthetic_only': True, 'fixture': str(f.resolve()), 'identifier': meta['identifier']}
try:
    ui = U.UI(app, f, ROOT / 'build/p3-schedules-screenshots', extra_env={'PI_CODING_AGENT_DIR': str(f / 'pi-agent')})
    U.wait(lambda: call('health')['state'] == 'ready', timeout=95)
    assert not call('schedules') and not call('organization_list')['items'] and call('list', list_query={'limit': 10})['total'] == 0, 'fresh fixture required'
    ui.press('项目'); ui.set('project-name', 'D1 合成计划项目'); ui.set('project-path', str(f / 'repo-a')); ui.press_key('register-project')
    project = U.wait(lambda: next((s['id'] for s in call('scopes') if s['name'] == 'D1 合成计划项目'), None))
    sources = []
    for i, body in enumerate(['本项目使用 pnpm 管理依赖。', '依赖变更应提交 pnpm-lock.yaml，并核对持续集成。']):
        sources.append(call('apply', user_confirmed=True, mutation={'kind': 'save', 'operation_id': str(uuid.uuid4()), 'memory': str(uuid.uuid4()), 'dataset': project, 'title': f'D1 合成来源 {i+1}', 'text': body, 'source': '合成计划材料', 'source_detail': {'references': [f'synthetic://schedule/{i}']}}))
    ui.press('设置'); ui.press_key('schedule-settings'); ui.press_key('schedule-new')
    choose('schedule-project', 'D1 合成计划项目'); choose('schedule-frequency', '每日')
    ui.set('schedule-timezone', 'UTC'); ui.set('schedule-local-time', next_clock()); ui.set('schedule-window', '60'); ui.set('schedule-limit', '8')
    ui.system_capture('plan-draft-system.png')
    ui.press_key('schedule-save')
    plan = U.wait(lambda: next((p for p in call('schedules') if p['spec']['project'] == project), None))
    pid = plan['plan']; assert not plan['enabled'] and plan['next_run_ms'] == 0
    assert not call('organization_list')['items'], 'saving draft started a model'
    U.wait(lambda: visible('schedule-toggle')); ui.system_capture('plan-saved-disabled-system.png')
    ui.press_key('schedule-toggle')
    enabled = U.wait(lambda: next((p for p in call('schedules') if p['plan'] == pid and p['enabled']), None))
    due = enabled['next_run_ms']; assert due > int(datetime.datetime.now(datetime.timezone.utc).timestamp() * 1000)
    report.update(plan=pid, initially_disabled=True, explicit_enable=True, due_ms=due)
    ui.system_capture('plan-enabled-system.png')
    print(json.dumps({'step': 'enabled_real_upcoming_slot', 'due_ms': due}), flush=True)
    run = U.wait(lambda: next((r for r in call('schedule_runs', schedule=pid) if r.get('task')), None), timeout=155)
    assert run['due_ms'] == due and run['generation'] == enabled['generation']
    task = U.wait(lambda: (lambda t: t if t['state'] not in ['queued', 'running', 'applying'] else None)(call('organization_get', organization_task=run['task'])), timeout=150)
    assert task['state'] == 'completed' and task['snapshot']['schema'] == 'memlane-organization-v1' and task['result']
    summaries = [e for e in task['entries'] if e['item']['action'] == 'summary' and e['state'] == 'applied']
    assert summaries, task
    memory = summaries[0]['receipt']['memory']
    U.wait(lambda: call('get', memory=memory)['index_state'] == 'indexed', timeout=95)
    current = call('get', memory=memory)
    hits = call('search', datasets=[project], query=current['text'], limit=50)
    assert any(h['version']['memory'] == memory and h['version']['revision'] == current['revision'] for h in hits)
    assert all(call('get', memory=v['memory'])['revision'] == 1 for v in sources)
    open_plan(pid); U.wait(lambda: any('执行已完成 · 审核与应用见任务' in label for label in ui.labels()))
    ui.system_capture('plan-completed-system.png')
    assert visible('schedule-open-' + run['run'])
    ui.press_key('schedule-open-' + run['run']); U.wait(lambda: task['task'] in ui.labels())
    ui.system_capture('plan-task-system.png')
    normal_final = next(r for r in call('schedule_runs', schedule=pid) if r['run'] == run['run'])
    assert normal_final['state'] == 'completed'
    report.update(normal_run=normal_final, normal_run_discovery=run, normal_task=task['task'], real_rust_memory=memory, source_versions_preserved=True)
    print(json.dumps({'step': 'real_scheduled_task_and_rust_passed', 'task': task['task']}), flush=True)

    # A new current source allows another authorized slot. A time edit alone
    # is deliberately not a way to repeat already processed materials.
    ui.press('D1 合成计划项目'); U.wait(lambda: visible('memory-' + sources[0]['memory'])); ui.press_key('memory-' + sources[0]['memory']); U.wait(lambda: '编辑' in ui.labels())
    ui.press('编辑'); ui.set('memory-draft', sources[0]['text'] + ' 新确认约定：依赖更新前核对项目持续集成。'); ui.press('保存')
    U.wait(lambda: call('get', memory=sources[0]['memory'])['revision'] == 2)
    open_plan(pid); ui.press_key('schedule-edit'); U.wait(lambda: visible('schedule-local-time'))
    ui.set('schedule-local-time', next_clock()); ui.press_key('schedule-save')
    changed = U.wait(lambda: next((p for p in call('schedules') if p['plan'] == pid and p['generation'] > enabled['generation']), None))
    assert changed['enabled']
    print(json.dumps({'step': 'next_real_slot_for_cancel', 'due_ms': changed['next_run_ms']}), flush=True)
    second = U.wait(lambda: next((r for r in call('schedule_runs', schedule=pid) if r.get('task') and r['task'] != task['task']), None), timeout=155)
    assert second['due_ms'] == changed['next_run_ms'] and second['generation'] == changed['generation']
    ui.press('整理记录'); ui.press_key('organization-tab-summary'); U.wait(lambda: visible('organization-task-' + second['task'])); ui.press_key('organization-task-' + second['task'])
    U.wait(lambda: (call('organization_get', organization_task=second['task']).get('execution') or {}).get('process_active'), timeout=60)
    U.wait(lambda: visible('organization-cancel')); ui.press_key('organization-cancel')
    cancelled = U.wait(lambda: (lambda t: t if t['state'] == 'cancelled' and (t.get('execution') or {}).get('process_reaped') and (t.get('execution') or {}).get('process_group_gone') else None)(call('organization_get', organization_task=second['task'])), timeout=45)
    assert not cancelled.get('result') and not cancelled['entries']
    ui.system_capture('plan-cancelled-system.png')
    cancel_final = next(r for r in call('schedule_runs', schedule=pid) if r['run'] == second['run'])
    assert cancel_final['state'] == 'cancelled'
    report.update(cancel_run=cancel_final, cancel_run_discovery=second, cancel_plan=changed, real_active_cancel=True, process_reaped=True, process_group_gone=True)
    open_plan(pid); ui.press_key('schedule-toggle')
    disabled = U.wait(lambda: next((p for p in call('schedules') if p['plan'] == pid and not p['enabled']), None))
    assert disabled['next_run_ms'] == 0
    ui.system_capture('plan-disabled-system.png'); ui.quit(stop=True); ui = None
    assert json.loads((data / 'service-state.json').read_text())['stopped']
    ui = U.UI(app, f, ROOT / 'build/p3-schedules-screenshots', extra_env={'PI_CODING_AGENT_DIR': str(f / 'pi-agent')})
    ui.press('设置'); U.wait(lambda: '启动服务' in ui.labels())
    assert json.loads((data / 'service-state.json').read_text())['stopped'] and not Path(ipc).exists(), 'reopening resumed a stopped service'
    ui.system_capture('reopened-stopped-system.png'); ui.quit(); ui = None
    report.update(status='passed', app_disable=True, explicit_stop_preserved_on_reopen=True, service_stopped=True)
except Exception as e:
    report['error'] = repr(e); raise
finally:
    if ui: ui.quit(stop=True)
    (OUT / 'real-app.json').write_text(json.dumps(report, ensure_ascii=False, indent=2) + '\n')
print(json.dumps(report, ensure_ascii=False))
