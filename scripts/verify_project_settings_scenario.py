"""项目菜单、整个项目设置和正式后台授权的隔离 Native 场景。"""
import json
import subprocess
import uuid


def verify(ui, call, report, capture, resize, out, ipc, driver):
    report.update(computer_level_agent_connection_verified=False,
                  agent_installation_verified=False, actual_project_access_verified=False)
    projects = []
    for name in ('阅读项目A', '权限项目B'):
        repo = out / ('repository-' + str(len(projects)))
        subprocess.run(['/usr/bin/git', 'init', str(repo)], capture_output=True, check=True)
        projects.append(call('register_project', user_confirmed=True,
                             project=dict(name=name, path=str(repo))))
    a, b = projects

    def press_key(key):
        node = ui.node(key=key)
        assert 'disabled' not in node.get('states', []) and 'press' in node.get('actions', []), node
        ui.req('act', id=node['id'], action='press')
        ui.req('idle', timeout=200)

    def press(label):
        node = next(n for n in ui.nodes() if n.get('label') == label and 'press' in n.get('actions', []))
        assert 'disabled' not in node.get('states', []), node
        ui.req('act', id=node['id'], action='press')
        ui.req('idle', timeout=200)

    def open_menu(project):
        bounds = ui.node(key='project-menu-' + project['id'])['bounds']
        ui.req('input', events=[dict(type='click', x=bounds['x'] + bounds['width']/2,
                                    y=bounds['y'] + bounds['height']/2)])
        ui.req('idle', timeout=2000)
        driver.wait(lambda: '项目设置' in ui.labels())

    subprocess.run(['/usr/bin/git', '-C', a['path'], '-c', 'user.name=Memlane验收',
                    '-c', 'user.email=acceptance@localhost', 'commit', '--allow-empty', '-m', '验收仓库'],
                   capture_output=True, check=True)
    worktree = out / 'repository-a-worktree'
    subprocess.run(['/usr/bin/git', '-C', a['path'], 'worktree', 'add', '--detach', str(worktree)],
                   capture_output=True, check=True)
    shared = call('register_project', user_confirmed=True,
                  project=dict(name=a['name'], path=str(worktree)))
    assert shared['id'] == a['id'], (shared, a)
    report['actual_git_worktree_shares_project'] = True

    memory = str(uuid.uuid4())
    text = '完整项目的权限独立保存。\n' + '当前知识与原始记录共用同一项目范围，阅读位置必须保留。\n' * 25
    call('apply', user_confirmed=True, mutation=dict(operation_id=str(uuid.uuid4()), kind='save',
         dataset=a['id'], memory=memory, title='跨页阅读保持', text=text, origin='knowledge', source='本轮合成输入'))
    raw = str(uuid.uuid4())
    call('apply', user_confirmed=True, mutation=dict(operation_id=str(uuid.uuid4()), kind='save',
         dataset=a['id'], memory=raw, title='授权原始材料', text='权限测试的独立原始输入。', origin='raw', source='本轮合成输入'))
    driver.wait(lambda: call('get', memory=memory)['index_state'] == 'indexed', timeout=90)
    globals_ = [s['id'] for s in call('scopes') if s['kind'] == 'preference']
    assert len(globals_) == 1
    conn = call('configure_connection', user_confirmed=True, connection=dict(
        agent='pi', name='本轮权限连接', project=a['id'], allowed_scopes=[a['id'], globals_[0]]))

    def current():
        return next(c for c in call('connections') if c['id'] == conn['id'])

    def denied(op, **fields):
        try:
            call(op, **fields)
        except AssertionError as error:
            response = error.args[0]
            assert isinstance(response, dict) and response.get('ok') is False, response
            return response
        raise AssertionError('后台错误地接受了过期或未授权请求')

    def search(generation, scope):
        return dict(protocol_version=1, connection_id=conn['id'], generation=generation,
                    workspace=a['path'], tool='memory_search',
                    arguments=dict(scopes=[scope], query='权限', limit=5))

    denied('agent', agent_request=search(conn['generation'], b['id']))
    driver.wait(lambda: a['name'] in ui.labels() and b['name'] in ui.labels())
    press_key('project-open-' + a['id'])
    driver.wait(lambda: '知识 1' in ui.labels() and '原始记录 1' in ui.labels())
    press('跨页阅读保持')
    driver.wait(lambda: text in ui.labels())
    ui.set('reading-search', '跨页')
    ui.req('act', id=ui.node(key='reading-search')['id'], action='focus')
    ui.req('input', events=[dict(type='key', key='Enter')])
    driver.wait(lambda: any(n.get('key') == 'memory-' + memory for n in ui.nodes()))
    body = ui.node(label=text)
    first_y = body['bounds']['y']
    ui.req('input', events=[dict(type='wheel', x=body['bounds']['x'] + 80,
                               y=350, dx=0, dy=180, precise=True)])
    scrolled_y = ui.node(label=text)['bounds']['y']
    assert scrolled_y < first_y

    open_menu(b)
    driver.wait(lambda: '项目设置' in ui.labels())
    assert text in ui.labels() and ui.node(key='reading-search')['value'] == '跨页'
    assert ui.node(label=text)['bounds']['y'] == scrolled_y
    capture('01-inactive-project-menu-system.png')
    ui.req('input', events=[dict(type='key', key='Escape')])
    (out / 'escape-tree.json').write_text(json.dumps(ui.req('tree'), ensure_ascii=False, indent=2)+'\n')
    driver.wait(lambda: ui.req('tree').get('focus') == ui.node(key='project-menu-' + b['id'])['id'])
    assert ui.node(label=text)['bounds']['y'] == scrolled_y
    report['inactive_menu_keeps_reading_query_scroll_and_escape_focus'] = True

    open_menu(a)
    press('项目设置')
    driver.wait(lambda: any(n.get('key') == 'project-settings-name' and n.get('label') == a['name'] for n in ui.nodes()))
    press_key('project-settings-close')
    driver.wait(lambda: text in ui.labels())
    assert ui.node(label=text)['bounds']['y'] == scrolled_y
    press('编辑')
    draft = '这是未保存的项目A草稿。'
    ui.set('memory-draft', draft)
    open_menu(b)
    assert ui.node(key='memory-draft')['value'] == draft
    press('项目设置')
    driver.wait(lambda: ui.node(key='project-settings-name')['label'] == b['name'])
    assert ui.node(key='project-settings-path')['label'] == b['path']
    driver.wait(lambda: ui.node(key='project-access-state-' + conn['id'])['label'] == '未授权本项目')
    capture('02-project-settings-ungranted-system.png')
    report['settings_owner_is_menu_project'] = True
    press_key('project-access-edit-' + conn['id'])
    press_key('project-access-allowed')
    capture('03-project-access-editor-system.png')
    press_key('project-access-save')
    granted = driver.wait(lambda: (c if c['generation'] == conn['generation'] + 1 else None) if (c := current()) else None)
    assert granted['project'] == a['id'] and granted['allowed_scopes'] == conn['allowed_scopes'] + [b['id']]
    driver.wait(lambda: '授权已保存；请重载 Agent 后重新验证。' in ui.labels())
    assert ui.node(key='project-access-state-' + conn['id'])['label'] == '已授权，待重载与验证'
    old = denied('agent', agent_request=search(conn['generation'], a['id']))
    call('agent', agent_request=search(granted['generation'], b['id']))
    report['old_generation_denied_code'] = old['code']
    capture('04-project-access-granted-system.png')

    press_key('project-access-edit-' + conn['id'])
    press_key('project-access-allowed')
    press_key('project-access-save')
    revoked = driver.wait(lambda: (c if c['generation'] == granted['generation'] + 1 else None) if (c := current()) else None)
    assert revoked['allowed_scopes'] == conn['allowed_scopes'] and revoked['project'] == a['id']
    denied('agent', agent_request=search(revoked['generation'], b['id']))
    call('agent', agent_request=search(revoked['generation'], a['id']))
    driver.wait(lambda: ui.node(key='project-access-state-' + conn['id'])['label'] == '未授权本项目')
    report['actual_project_access_verified'] = True
    report['other_project_global_grants_preserved'] = True
    report['grant_does_not_claim_agent_reload_or_connection'] = True
    (out / 'access-receipts.json').write_text(json.dumps(dict(before=conn, granted=granted, revoked=revoked),
                                                       ensure_ascii=False, indent=2) + '\n')
    resize(980, 620)
    capture('05-project-settings-narrow-system.png')
    assert ui.node(key='project-menu-' + b['id'])['bounds']['width'] > 0
    press_key('project-access-edit-' + conn['id'])
    press_key('project-access-allowed')
    checkbox = ui.node(key='project-access-allowed')['bounds']
    ui.req('input', events=[dict(type='wheel', x=checkbox['x']+40, y=400, dx=0, dy=200, precise=True)])
    capture('05b-narrow-access-editor-system.png')
    checkbox, save = ui.node(key='project-access-allowed')['bounds'], ui.node(key='project-access-save')['bounds']
    assert checkbox['y'] >= 54 and save['y']+save['height'] <= 620, (checkbox, save)
    press_key('project-access-cancel')
    assert current()['generation'] == revoked['generation'] and current()['allowed_scopes'] == revoked['allowed_scopes']
    report['narrow_scope_editor_and_cancel_keep_authorization'] = True
    press_key('project-open-' + a['id'])
    driver.wait(lambda: ui.node(key='memory-draft')['value'] == draft)
    assert ui.node(key='reading-search')['value'] == '跨页'
    assert call('get', memory=memory)['text'] == text
    capture('06-return-project-draft-system.png')
    report['project_switch_keeps_unsaved_draft'] = True
    open_menu(a)
    press('项目设置')
    driver.wait(lambda: any(n.get('key') == 'project-access-edit-' + conn['id'] for n in ui.nodes()))
    press_key('project-access-edit-' + conn['id'])
    assert 'disabled' in ui.node(key='project-access-allowed').get('states', [])
    assert 'disabled' in ui.node(key='project-access-save').get('states', [])
    press_key('project-settings-close')
    driver.wait(lambda: ui.node(key='memory-draft')['value'] == draft)
    report['bound_project_revoke_requires_disconnect'] = True
    report['settings_close_keeps_editor'] = True
    call('stop')
    driver.wait(lambda: not ipc.exists(), timeout=30)
    open_menu(b)
    press('项目设置')
    driver.wait(lambda: any(label.startswith('项目设置读取未完成：') for label in ui.labels()))
    assert '尚未配置 Agent 连接。' not in ui.labels()
    assert not any(n.get('key') == 'reading-list-count' for n in ui.nodes())
    capture('07-stopped-settings-read-failure-system.png')
    report['actual_stopped_settings_error_is_not_empty'] = True
    report['failed_project_read_does_not_show_previous_project_count'] = True
