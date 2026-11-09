"""C Native绑定与真实服务契约；不冒称正常Agent或仓库整理已经执行。"""
import json
from pathlib import Path
import subprocess
import uuid


def verify(ui, call, report, capture, resize, out, ipc, driver):
    report.update(actual_project_binding_verified=False, actual_project_task_execution=False,
                  historical_sessions_read=False, agent_process_connected=False,
                  authorized_agent_request_contract=True)
    repo = out/'synthetic-project'
    subprocess.run(['/usr/bin/git', 'init', str(repo)], capture_output=True, check=True)
    project = call('register_project', user_confirmed=True, project=dict(name='本轮项目绑定', path=str(repo)))
    connection = call('configure_connection', user_confirmed=True,
                      connection=dict(agent='codex', name='本轮连接契约', project=project['id'], allowed_scopes=[project['id']]))
    # An authorized fixture request exercises the real daemon/Rust access
    # path. It is not a claim that a Codex executable or model was invoked.
    call('agent', agent_request=dict(protocol_version=1, connection_id=connection['id'],
         generation=connection['generation'], workspace=str(repo), tool='memory_search',
         arguments=dict(scopes=[project['id']], query='本轮绑定契约', limit=1)))
    current = next(c for c in call('connections') if c['id'] == connection['id'])
    assert current.get('last_seen')
    binding = call('project_binding', scope=project['id'])
    assert binding['state'] == 'unbound' and binding['revision'] == 0
    try:
        call('project_binding_change', project_binding_change=dict(project=project['id'], connection_id=connection['id'],
             expected_generation=connection['generation'], expected_revision=0))
    except AssertionError as error:
        assert error.args[0]['code'] == 'confirmation_required', error.args[0]
    else:
        raise AssertionError('未确认的项目绑定被保存')

    def press(key):
        node = ui.node(key=key)
        assert 'disabled' not in node.get('states', []), node
        ui.req('act', id=node['id'], action='press')
        ui.req('capture')

    driver.wait(lambda: any(n.get('key') == 'project-open-'+project['id'] for n in ui.nodes()))
    press('project-open-'+project['id'])
    driver.wait(lambda: any(n.get('key') == 'project-organize-open' for n in ui.nodes()))
    assert '知识 0' in ui.labels() and '原始记录 0' in ui.labels()
    capture('01-project-empty-system.png')
    press('project-organize-open')
    driver.wait(lambda: any(n.get('key') == 'project-organizer-choose' and 'disabled' not in n.get('states', []) for n in ui.nodes()))
    assert ui.node(key='project-organizer-history').get('value') in (None, '', 'false')
    assert 'disabled' in ui.node(key='project-organizer-history').get('states', [])
    capture('02-project-unbound-system.png')
    press('project-organizer-choose')
    driver.wait(lambda: any(n.get('key') == 'project-organizer-'+connection['id'] for n in ui.nodes()))
    press('project-organizer-'+connection['id'])
    driver.wait(lambda: call('project_binding', scope=project['id'])['revision'] == 1)
    driver.wait(lambda: ui.node(key='project-organizer-agent').get('label') == 'Codex')
    binding = call('project_binding', scope=project['id'])
    assert binding['state'] == 'available' and binding['connection_id'] == connection['id']
    assert binding['generation'] == connection['generation']
    assert call('list', list_query=dict(kind='knowledge', datasets=[project['id']]))['total'] == 0
    assert call('project_task_list', project_task_list=dict(project=project['id'], active_only=True))['items'] == []
    capture('03-project-bound-system.png')
    report['start_control_states'] = ui.node(key='project-organizer-start').get('states', [])
    press('project-organizer-close')
    driver.wait(lambda: ui.req('tree').get('focus') == ui.node(key='project-organize-open')['id'])
    press('project-organize-open')
    driver.wait(lambda: ui.node(key='project-organizer-agent').get('label') == 'Codex')
    resize(980, 620)
    driver.wait(lambda: ui.native()['bounds']['Width'] <= 1000)
    capture('04-project-bound-narrow-system.png')
    press('project-organizer-close')
    # An ordinary current-knowledge save verifies the empty/supplement boundary;
    # no generated knowledge, old library or historical chat enters this root.
    knowledge_id = str(uuid.uuid4())
    call('apply', user_confirmed=True, mutation=dict(operation_id=str(uuid.uuid4()), kind='save',
         dataset=project['id'], memory=knowledge_id, title='本轮用户知识', text='这条本轮用户知识用于验证绑定面板保留项目列表。',
         origin='knowledge', source='本轮合成输入'))
    driver.wait(lambda: any(n.get('key') == 'memory-'+knowledge_id for n in ui.nodes()))
    assert not any(n.get('key') == 'project-organize-open' for n in ui.nodes())
    resize(1200, 820)
    press('memory-'+knowledge_id)
    driver.wait(lambda: '知识详情' in ui.labels())
    press('project-menu-'+project['id'])
    driver.wait(lambda: '补充项目知识' in ui.labels())
    node = ui.node(label='补充项目知识')
    ui.req('act', id=node['id'], action='press')
    ui.req('capture')
    driver.wait(lambda: ui.node(key='project-organizer-agent').get('label') == 'Codex')
    assert any(n.get('key') == 'memory-'+knowledge_id for n in ui.nodes())
    capture('05-supplement-keeps-knowledge-system.png')
    press('project-organizer-close')
    driver.wait(lambda: ui.req('tree').get('focus') == ui.node(key='project-menu-'+project['id'])['id'])
    assert call('get', memory=knowledge_id)['revision'] == 1
    unchanged = next(c for c in call('connections') if c['id'] == connection['id'])
    assert unchanged['generation'] == connection['generation'] and unchanged['allowed_scopes'] == connection['allowed_scopes']
    assert call('project_task_list', project_task_list=dict(project=project['id']))['items'] == []
    report.update(actual_project_binding_verified=True, binding_reopen_verified=True,
                  empty_and_supplement_entry_verified=True, binding_does_not_start_task=True,
                  binding_preserves_connection_generation=True, ordinary_save_keeps_revision=True)
    (out/'binding-receipt.json').write_text(json.dumps(binding, ensure_ascii=False, indent=2)+'\n')
