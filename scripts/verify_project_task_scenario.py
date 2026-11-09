"""实际包C右栏启动、取消、同任务恢复，真实Codex及Rust保存/索引。"""
import hashlib
import json
from pathlib import Path
import subprocess
import sqlite3
import time
import uuid


def verify(ui, call, report, capture, resize, out, ipc, driver):
    root = Path(__file__).resolve().parents[1]
    assert 'memlane-p2-' in str(out), '隔离适配器要求新memlane-p2证据根'
    client = out/'client'
    app = out/'app/darwin-arm64/MemlaneProjectTask.app'
    executable = Path('/Applications/ChatGPT.app/Contents/Resources/codex-cli/bin/codex')
    auth = Path('/Users/bytedance/.codex')
    adapter = out/'agent-adapter-check'
    subprocess.run(['sh', str(root/'scripts/go.sh'), 'build', '-o', str(adapter), './cmd/p2-agent-check'], cwd=root, check=True)
    repo = out/'synthetic-project'
    subprocess.run(['/usr/bin/git', 'init', str(repo)], capture_output=True, check=True)
    materials = {'README.md': '# TicketBox 本轮合成项目\n\n本项目使用 pnpm 管理依赖；依赖变化时提交 pnpm-lock.yaml。\n知识只适用于 TicketBox，不推断其他仓库。\n',
                 'ticket.py': "def normalize_ticket(value):\n    if not isinstance(value, str):\n        raise TypeError('str required')\n    return '-'.join(value.strip().split()).lower()\n",
                 'AGENTS.md': '这是本轮合成仓库。核对README及代码，不执行项目脚本或编造测试结果。'}
    for name, text in materials.items(): (repo/name).write_text(text)
    project = call('register_project', user_confirmed=True, project=dict(name='C真实任务本轮项目', path=str(repo)))
    connection = call('configure_connection', user_confirmed=True, connection=dict(agent='codex', name='本轮真实Codex', project=project['id'], allowed_scopes=[project['id']]))
    options = dict(Project=project['id'], ClientRoot=str(client), ServiceRoot=str(client/'data'),
                   Bridge=str(app/'Contents/Resources/bin/memlane'), AgentPath=str(executable), Workspace=str(repo), CodexDirectory=str(auth))

    def adapter_call(action):
        request = dict(action=action, connection=connection, options=options,
                       run=dict(Workspace=str(repo), CodexAuthHome=str(auth)))
        result = subprocess.run([str(adapter)], input=json.dumps(request).encode(), capture_output=True, timeout=180)
        response = json.loads(result.stdout)
        (out/(action+'-receipt.json')).write_text(json.dumps(response, ensure_ascii=False, indent=2)+'\n')
        assert result.returncode == 0 and response['ok'], response
        return response['result']

    adapter_call('install')
    verified = adapter_call('verify_connection')
    assert verified['calls'] and verified['process_group_gone']
    report['actual_connection_verified'] = True
    binding = call('project_binding_change', user_confirmed=True, project_binding_change=dict(project=project['id'], connection_id=connection['id'], expected_generation=connection['generation'], expected_revision=0))
    def press(key):
        node = ui.node(key=key)
        assert 'disabled' not in node.get('states', []), node
        ui.req('act', id=node['id'], action='press')
        ui.req('capture')

    def tasks(active=False):
        return call('project_task_list', project_task_list=dict(project=project['id'], active_only=active, limit=10))['items']

    def menu_open(label):
        press('project-menu-'+project['id'])
        driver.wait(lambda: label in ui.labels())
        node = ui.node(label=label)
        ui.req('act', id=node['id'], action='press')
        ui.req('capture')

    def progress():
        menu_open('查看整理进度')

    def live_pid(task_id):
        # Registration is live owner data. The IPC execution receipt is sealed
        # only after exit, so it cannot be used to wait for a running process.
        with sqlite3.connect((client/'data/memlane.sqlite').as_uri()+'?mode=ro', uri=True) as db:
            row = db.execute('SELECT process_pid FROM project_task_runs WHERE task=? AND released=0', (task_id,)).fetchone()
            return row[0] if row else 0

    def restart(reduced):
        ui.quit()
        state_path = client/'first-launch.json'
        state = json.loads(state_path.read_text())
        state['reduced_motion'] = reduced
        state_path.write_text(json.dumps(state)+'\n')
        ui.__init__(app, out, out/'screenshots', config=out/'runtime-config.json')
        (out/('ui-reduced-process.json' if reduced else 'ui-regular-reopen-process.json')).write_text(
            json.dumps(dict(pid=ui.pid, automation=ui.path, native=ui.native_path, owned=True))+'\n')
        driver.wait(lambda: any(n.get('key') == 'project-open-'+project['id'] for n in ui.nodes()))
        press('project-open-'+project['id'])
        if tasks(True):
            progress()
        elif call('list', list_query=dict(kind='knowledge', datasets=[project['id']]))['total'] > 0:
            menu_open('补充项目知识')
        else:
            driver.wait(lambda: any(n.get('key') == 'project-organize-open' for n in ui.nodes()))
            press('project-organize-open')

    driver.wait(lambda: any(n.get('key') == 'project-open-'+project['id'] for n in ui.nodes()))
    press('project-open-'+project['id'])
    driver.wait(lambda: any(n.get('key') == 'project-organize-open' for n in ui.nodes()))
    press('project-organize-open')
    driver.wait(lambda: any(n.get('key') == 'project-organizer-start' and 'disabled' not in n.get('states', []) for n in ui.nodes()))
    assert not tasks()
    capture('01-c-ready-system.png')
    press('project-organizer-start')
    assert 'disabled' in ui.node(key='project-organizer-start').get('states', [])
    first = driver.wait(lambda: tasks(True))
    assert len(first) == 1
    cancelled_id = first[0]['task']
    driver.wait(lambda: call('project_task', project_task=cancelled_id)['state'] == 'organizing' and live_pid(cancelled_id) > 1, timeout=90)
    driver.wait(lambda: any(n.get('key') == 'project-organizer-stop' for n in ui.nodes()))
    capture('02-c-running-system.png')
    before = call('project_task', project_task=cancelled_id)
    before_pid = live_pid(cancelled_id)
    press('project-organizer-close')
    assert call('project_task', project_task=cancelled_id)['state'] not in ('cancelled', 'cancelling')
    progress()
    driver.wait(lambda: any(n.get('key') == 'project-organizer-stop' for n in ui.nodes()))
    assert len(tasks()) == 1
    same = call('project_task', project_task=cancelled_id)
    assert same['task'] == before['task'] and same['attempt'] == before['attempt']
    capture('03-c-reopened-same-task-system.png')
    restart(True)
    driver.wait(lambda: any(n.get('key') == 'project-organizer-stop' for n in ui.nodes()))
    same = call('project_task', project_task=cancelled_id)
    assert len(tasks()) == 1 and same['attempt'] == before['attempt']
    assert live_pid(cancelled_id) == before_pid
    capture('03b-c-reduced-reopened-running-system.png')
    report.update(task_survives_actual_app_restart=True, reduced_motion_native_window_verified=True)
    press('project-organizer-stop')
    driver.wait(lambda: call('project_task', project_task=cancelled_id)['state'] in ('cancelled','failed','interrupted'), timeout=60)
    cancelled = call('project_task', project_task=cancelled_id)
    (out/'project-task-cancel-receipt.json').write_text(json.dumps(cancelled, ensure_ascii=False, indent=2)+'\n')
    assert cancelled['state'] == 'cancelled' and cancelled['cancel_requested']
    assert cancelled['execution']['process_reaped'] and cancelled['execution']['process_group_gone']
    driver.wait(lambda: '已停止整理' in ui.labels())
    capture('04-c-cancelled-system.png')
    resize(980, 620)
    capture('05-c-cancelled-narrow-system.png')
    press('project-organizer-reset')
    driver.wait(lambda: any(n.get('key') == 'project-organizer-start' and 'disabled' not in n.get('states', []) for n in ui.nodes()))
    restart(False)
    driver.wait(lambda: any(n.get('key') == 'project-organizer-start' and 'disabled' not in n.get('states', []) for n in ui.nodes()))
    resize(1200, 820)
    press('project-organizer-start')

    def start_result():
        new = [t for t in tasks() if t['task'] != cancelled_id]
        if new:
            return new
        if any(label.startswith('项目整理启动未确认') for label in ui.labels()):
            return 'startup_unconfirmed'

    second = driver.wait(start_result, timeout=60)
    if second == 'startup_unconfirmed':
        report['startup_retry_feedback'] = '真实启动未确认；重新读取后以同意图重试一次'
        assert len(tasks()) == 1
        capture('05b-c-startup-failed-system.png')
        press('project-organizer-retry')
        driver.wait(lambda: any(n.get('key') == 'project-organizer-start' and 'disabled' not in n.get('states', []) for n in ui.nodes()))
        press('project-organizer-start')
        second = driver.wait(start_result, timeout=60)
    assert isinstance(second, list) and len(second) == 1 and second[0]['task'] != cancelled_id and len(tasks()) == 2, second
    task = call('project_task', project_task=second[0]['task'])
    report.update(task_start_control_native_verified=True, task_cancel_control_native_verified=True,
                  task_navigation_does_not_cancel=True, task_reopen_keeps_identity_and_attempt=True,
                  task_reset_creates_new_intent=True,
                  cancel_reaps_real_agent_group=cancelled['execution']['interrupt_sent'],
                  cancellation_preserved_saved_knowledge=len(cancelled.get('receipt', {}).get('added', [])))
    driver.wait(lambda: call('project_task', project_task=task['task'])['state'] in ('completed', 'failed', 'cancelled', 'interrupted'), timeout=240)
    finished = call('project_task', project_task=task['task'])
    (out/'project-task-receipt.json').write_text(json.dumps(finished, ensure_ascii=False, indent=2)+'\n')
    assert finished['state'] == 'completed', finished
    execution, receipt = finished['execution'], finished['receipt']
    assert execution['terminal_completed'] and execution['process_reaped'] and execution['process_group_gone']
    assert receipt['persisted'] and receipt['indexed'] == len(receipt['added'])
    assert receipt['index_failed'] == 0 and receipt['source_index_failed'] == 0
    completed_heading = '项目知识已经就绪' if receipt['added'] else '本次没有新增知识'
    driver.wait(lambda: completed_heading in ui.labels())
    # Sample the completed book after its documented 900ms visual transition.
    # This wait belongs to the verifier; it never advances the service task.
    time.sleep(1)
    capture('06-c-completed-system.png')
    resize(980, 620)
    capture('07-c-completed-narrow-system.png')
    press('project-organizer-view')
    sources = {saved['memory']: call('get', memory=saved['memory']) for saved in receipt['sources']}
    assert all(v['origin'] == 'raw' and v['index_state'] == 'indexed' for v in sources.values())
    for saved in receipt['added']:
        v = call('get', memory=saved['memory'])
        assert v['origin'] == 'knowledge' and v['index_state'] == 'indexed'
        provenance = call('organization_sources', memory=v['memory'], expected_revision=v['revision'])
        assert provenance, provenance
        for source in provenance:
            q = dict(project=project['id'], owner=v['memory'], owner_revision=v['revision'], memory=source['source_memory'], revision=source['source_revision'])
            original = call('source_read', source_read=q)
            assert original['state'] == 'available' and original['record']['origin'] == 'raw'
            assert original['quote'] and original['quote'] in original['record']['text']
            assert original['record']['text'] in materials.values()
    report.update(actual_project_task_execution=True, model_inference_verified=True, actual_rust_index=True,
                  actual_repository_sources_saved=True, source_quote_version_verified=True,
                  current_bound_model=execution['model'], saved_knowledge=len(receipt['added']),
                  saved_repository_sources=len(receipt['sources']), historical_sessions_read=False,
                  full_c_motion_verified=False, native_stage_motion_attached=True,
                  fact_quality='requires_itemwise_review')
    ui.press_key('project-open-'+project['id'])
    available = receipt['added'] or cancelled.get('receipt', {}).get('added', [])
    assert available, '合成仓库未产生可核对知识，质量验收未完成'
    selected = available[0]['memory']
    driver.wait(lambda: any(n.get('key') == 'memory-'+selected for n in ui.nodes()))
    ui.press_key('memory-'+selected)
    driver.wait(lambda: '知识详情' in ui.labels())
    resize(1200, 820)
    capture('08-c-generated-knowledge-system.png')
    resize(980, 620)
    capture('09-c-generated-knowledge-narrow-system.png')
    report['native_generated_knowledge_lineage_verified'] = True
    hashes = {name: hashlib.sha256((repo/name).read_bytes()).hexdigest() for name in materials}
    assert hashes == {name: hashlib.sha256(text.encode()).hexdigest() for name, text in materials.items()}
    report['repository_materials_unchanged'] = True
