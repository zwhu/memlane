"""隔离包：真实Codex保存、Rust索引发布故障、同版本独立恢复与App重开。"""
import json
from pathlib import Path
import sqlite3
import subprocess
import uuid


def verify(ui, call, report, capture, resize, out, ipc, driver):
    root = Path(__file__).resolve().parents[1]
    assert 'memlane-p2-' in str(out)
    client = out/'client'
    app = out/'app/darwin-arm64/MemlaneProjectTask.app'
    repo = out/'synthetic-project'
    subprocess.run(['/usr/bin/git', 'init', str(repo)], capture_output=True, check=True)
    materials = {'README.md': '# TicketBox 本轮合成项目\n\n本项目使用 pnpm 管理依赖；依赖变化时提交 pnpm-lock.yaml。\n',
                 'ticket.py': "def normalize_ticket(value):\n    if not isinstance(value, str):\n        raise TypeError('str required')\n    return '-'.join(value.strip().split()).lower()\n",
                 'AGENTS.md': '本轮合成仓库。核对README及代码，不执行项目脚本或编造测试结果。'}
    for name, text in materials.items(): (repo/name).write_text(text)
    project = call('register_project', user_confirmed=True, project=dict(name='C索引恢复本轮项目', path=str(repo)))
    connection = call('configure_connection', user_confirmed=True, connection=dict(agent='codex', name='本轮真实Codex', project=project['id'], allowed_scopes=[project['id']]))
    adapter = out/'agent-adapter-check'
    subprocess.run(['sh', str(root/'scripts/go.sh'), 'build', '-o', str(adapter), './cmd/p2-agent-check'], cwd=root, check=True)
    options = dict(Project=project['id'], ClientRoot=str(client), ServiceRoot=str(client/'data'),
                   Bridge=str(app/'Contents/Resources/bin/memlane'), AgentPath='/Applications/ChatGPT.app/Contents/Resources/codex-cli/bin/codex',
                   Workspace=str(repo), CodexDirectory='/Users/bytedance/.codex')
    for action in ('install', 'verify_connection'):
        request = dict(action=action, connection=connection, options=options, run=dict(Workspace=str(repo), CodexAuthHome='/Users/bytedance/.codex'))
        result = subprocess.run([str(adapter)], input=json.dumps(request).encode(), capture_output=True, timeout=180)
        response = json.loads(result.stdout)
        (out/(action+'-receipt.json')).write_text(json.dumps(response, ensure_ascii=False, indent=2)+'\n')
        assert result.returncode == 0 and response['ok'], response
        if action == 'verify_connection': assert response['result']['calls'] and response['result']['process_group_gone']
    call('project_binding_change', user_confirmed=True, project_binding_change=dict(project=project['id'], connection_id=connection['id'], expected_generation=connection['generation'], expected_revision=0))

    def press(key):
        node = ui.node(key=key)
        assert 'disabled' not in node.get('states', []), node
        ui.req('act', id=node['id'], action='press')
        ui.req('capture')

    def fault(sql, params=()):
        # Explicit SQL faults operate solely on this new synthetic business root.
        assert client.is_relative_to(out) and 'memlane-p2-' in str(client)
        with sqlite3.connect(client/'data/memlane.sqlite', timeout=15) as db:
            db.execute(sql, params)

    def task(): return call('project_task', project_task=task_id)

    def persist(name, value): (out/name).write_text(json.dumps(value, ensure_ascii=False, indent=2)+'\n')

    fault("CREATE TRIGGER c_index_publish_fault BEFORE UPDATE OF state ON jobs WHEN NEW.state='completed' AND EXISTS(SELECT 1 FROM memories m WHERE m.id=NEW.memory AND m.dataset='"+project['id']+"') BEGIN SELECT RAISE(ABORT,'explicit synthetic C index publication fault'); END")
    driver.wait(lambda: any(n.get('key') == 'project-open-'+project['id'] for n in ui.nodes()))
    press('project-open-'+project['id'])
    driver.wait(lambda: any(n.get('key') == 'project-organize-open' for n in ui.nodes()))
    press('project-organize-open')
    driver.wait(lambda: any(n.get('key') == 'project-organizer-start' and 'disabled' not in n.get('states', []) for n in ui.nodes()))
    capture('01-c-index-ready-system.png')
    press('project-organizer-start')
    summaries = driver.wait(lambda: call('project_task_list', project_task_list=dict(project=project['id'], limit=10))['items'], timeout=60)
    assert len(summaries) == 1
    task_id = summaries[0]['task']
    driver.wait(lambda: task()['state'] in ('failed','completed','cancelled','interrupted'), timeout=240)
    failed = task()
    persist('project-task-index-failed.json', failed)
    assert failed['state'] == 'failed' and failed['error_code'] == 'project_index_failed', failed
    proof, receipt = failed['execution'], failed['receipt']
    assert proof['terminal_completed'] and proof['process_reaped'] and proof['process_group_gone']
    assert receipt['added'] and receipt['sources'] and receipt['index_failed']+receipt['source_index_failed'] > 0
    saved = receipt['added']+receipt['sources']
    baseline = {v['memory']:call('get', memory=v['memory']) for v in saved}
    driver.wait(lambda: '知识已保存，索引未完成' in ui.labels())
    capture('02-c-index-failed-saved-system.png')
    resize(980,620)
    capture('03-c-index-failed-narrow-system.png')
    press('project-organizer-close')
    memory = receipt['added'][0]['memory']
    driver.wait(lambda: any(n.get('key') == 'memory-'+memory for n in ui.nodes()))
    press('memory-'+memory)
    driver.wait(lambda: '知识详情' in ui.labels())
    capture('04-c-index-failed-readable-system.png')

    def reopen_progress():
        press('project-menu-'+project['id'])
        driver.wait(lambda: '查看整理进度' in ui.labels())
        node = ui.node(label='查看整理进度')
        ui.req('act', id=node['id'], action='press')
        ui.req('capture')

    reopen_progress()
    driver.wait(lambda: any(n.get('key') == 'project-organizer-index-recover' and 'disabled' not in n.get('states', []) for n in ui.nodes()))
    press('project-organizer-index-recover')
    driver.wait(lambda: task().get('index_recovery',{}).get('state') in ('failed','partial_failed'), timeout=90)
    first_recovery = task()['index_recovery']
    persist('first-index-recovery-failed.json', first_recovery)
    driver.wait(lambda: '索引恢复未完成' in ui.labels())
    capture('05-c-index-recovery-failed-system.png')
    # Hold the next real queue at its claim boundary to prove pending recovery
    # survives the App process. This gate is a verifier fault, not fake progress.
    fault("CREATE TRIGGER c_index_claim_gate BEFORE UPDATE OF state ON jobs WHEN NEW.state='indexing' AND EXISTS(SELECT 1 FROM memories m WHERE m.id=NEW.memory AND m.dataset='"+project['id']+"') BEGIN SELECT RAISE(ABORT,'explicit synthetic C index claim gate'); END")
    fault('DROP TRIGGER c_index_publish_fault')
    driver.wait(lambda: 'disabled' not in ui.node(key='project-organizer-index-recover').get('states', []))
    press('project-organizer-index-recover')
    driver.wait(lambda: task().get('index_recovery',{}).get('state') == 'running')
    recovery = task()['index_recovery']
    assert recovery['id'] != first_recovery['id'] and recovery['project_task'] == task_id
    assert sorted((i['memory'],i['revision']) for i in recovery['items']) == sorted((i['memory'],i['revision']) for i in saved)
    driver.wait(lambda: '正在恢复索引' in ui.labels())
    capture('06-c-index-recovery-pending-system.png')
    ui.quit()
    ui.__init__(app,out,out/'screenshots',config=out/'runtime-config.json')
    persist('ui-recovery-reopen-process.json',dict(pid=ui.pid,automation=ui.path,native=ui.native_path,owned=True))
    driver.wait(lambda: any(n.get('key') == 'project-open-'+project['id'] for n in ui.nodes()))
    press('project-open-'+project['id'])
    reopen_progress()
    driver.wait(lambda: '正在恢复索引' in ui.labels())
    assert task()['index_recovery']['id'] == recovery['id'] and task()['attempt'] == failed['attempt']
    capture('07-c-index-recovery-reopened-system.png')
    fault('DROP TRIGGER c_index_claim_gate')
    driver.wait(lambda: task()['index_recovery']['state'] == 'completed', timeout=90)
    finished = task()
    persist('project-task-index-recovered.json', finished)
    assert finished['state'] == 'failed' and finished['execution'] == proof
    assert finished['receipt']['indexed'] == len(receipt['added']) and finished['receipt']['source_indexed'] == len(receipt['sources'])
    assert len(call('project_task_list', project_task_list=dict(project=project['id'], limit=10))['items']) == 1
    for memory, old in baseline.items():
        new = call('get', memory=memory)
        assert new['revision'] == old['revision'] and new['title'] == old['title'] and new['text'] == old['text'] and new['index_state'] == 'indexed'
    driver.wait(lambda: '已保存内容的索引已恢复' in ui.labels())
    capture('08-c-index-recovered-system.png')
    resize(980,620)
    capture('09-c-index-recovered-narrow-system.png')
    press('project-organizer-view')
    for item in receipt['added']:
        links = call('organization_sources', memory=item['memory'], expected_revision=item['revision'])
        for link in links:
            original = call('source_read', source_read=dict(project=project['id'],owner=item['memory'],owner_revision=item['revision'],memory=link['source_memory'],revision=link['source_revision']))
            assert original['state'] == 'available' and original['quote'] in original['record']['text'] and original['record']['text'] in materials.values()
    report.update(actual_connection_verified=True, model_inference_verified=True, actual_project_task_execution=True,
                  actual_rust_index=True, actual_repository_sources_saved=True, source_quote_version_verified=True,
                  index_publication_failure_injected=True, saved_knowledge_readable_after_index_failure=True,
                  independent_index_recovery_verified=True, recovery_failure_retry_verified=True,
                  recovery_app_restart_keeps_task_attempt_and_operation=True, recovery_does_not_rerun_model=True,
                  saved_versions_unchanged=True, saved_knowledge=len(receipt['added']), saved_repository_sources=len(receipt['sources']),
                  current_bound_model=proof['model'], historical_sessions_read=False, full_c_motion_verified=False,
                  fact_quality='requires_itemwise_review')
