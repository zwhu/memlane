"""通用知识真实 IPC 保存、分类、版本与独立授权的隔离 Native 验收。"""
import json
import subprocess
import uuid


def verify(ui, call, report, capture, resize, out, ipc, driver):
    scopes = call('scopes')
    preference = next(s for s in scopes if s['kind'] == 'preference')
    experience = next(s for s in scopes if s['kind'] == 'experience')
    projects = []
    for name in ('通用知识来源项目', '独立授权项目'):
        repo = out / ('repository-' + str(len(projects)))
        subprocess.run(['/usr/bin/git', 'init', str(repo)], capture_output=True, check=True)
        projects.append(call('register_project', user_confirmed=True, project=dict(name=name, path=str(repo))))
    project, other = projects
    source_id, other_id = str(uuid.uuid4()), str(uuid.uuid4())
    original = '接口调用异常时，先保留请求标识与状态码，再核对对应日志。\n项目已有约定时，按项目约定执行。'
    call('apply', user_confirmed=True, mutation=dict(operation_id=str(uuid.uuid4()), kind='save',
         dataset=project['id'], memory=source_id, title='可追踪的接口排查', text=original,
         origin='knowledge', source='本轮合成来源'))
    call('apply', user_confirmed=True, mutation=dict(operation_id=str(uuid.uuid4()), kind='save',
         dataset=other['id'], memory=other_id, title='其他项目私有内容', text='这条只能在其他项目查看。',
         origin='knowledge', source='本轮合成来源'))
    driver.wait(lambda: project['name'] in ui.labels())
    ui.press_key('nav-通用知识')
    driver.wait(lambda: '知识 0' in ui.labels())
    ui.press_key('common-category-experience')
    driver.wait(lambda: '没有匹配的内容' in ui.labels())
    assert not any(n.get('key') in ('memory-'+source_id, 'memory-'+other_id) for n in ui.nodes())
    capture('01-common-empty-system.png')
    ui.press_key('common-category-preference')
    ui.press('新增记忆')
    driver.wait(lambda: any(n.get('key') == 'memory-title' for n in ui.nodes()))
    ui.set('memory-title', '偏好简洁的技术说明')
    ui.set('memory-draft', '默认先说明结果，再给必要的证据；项目有格式约定时遵循项目约定。')
    ui.press('保存')
    pref = driver.wait(lambda: (page['items'][0] if page['total'] == 1 else None)
                       if (page := call('list', list_query=dict(datasets=[preference['id']], limit=10))) else None)
    driver.wait(lambda: call('get', memory=pref['memory'])['index_state'] == 'indexed', timeout=90)
    driver.wait(lambda: any(n.get('key') == 'memory-'+pref['memory'] for n in ui.nodes()))
    ui.press_key('memory-'+pref['memory'])
    ui.press('编辑')
    ui.set('memory-draft', '先说明结果，再给证据。项目明确格式约定优先于个人偏好。')
    ui.press('保存')
    driver.wait(lambda: call('get', memory=pref['memory'])['revision'] == 2)
    driver.wait(lambda: '知识 1' in ui.labels())
    capture('02-preference-edited-system.png')
    ui.press_key('knowledge-history-open')
    driver.wait(lambda: 'v1' in ui.labels() and 'v2' in ui.labels())
    capture('03-preference-history-system.png')
    ui.req('input', events=[dict(type='key', key='Escape')])
    driver.wait(lambda: not any(n.get('key') == 'knowledge-history-dialog' for n in ui.nodes()))
    report['personal_preference_native_save_edit_history_index'] = True

    ui.press_key('project-open-'+project['id'])
    driver.wait(lambda: any(n.get('key') == 'memory-'+source_id for n in ui.nodes()))
    ui.press_key('memory-'+source_id)
    ui.press('更多操作')
    ui.press_key('experience-copy-preview')
    driver.wait(lambda: any(n.get('key') == 'experience-copy-original' for n in ui.nodes()))
    assert ui.node(key='experience-copy-original')['label'] == original
    assert 'disabled' in ui.node(key='experience-copy-confirm').get('states', [])
    capture('04-copy-preview-empty-conditions-system.png')
    ui.set('experience-copy-title', '接口排查保留可追踪信息')
    conditions = '适用于提供请求标识与状态码的接口；项目明确约定优先。'
    ui.set('experience-copy-conditions', conditions)
    resize(980, 620)
    capture('05-copy-preview-narrow-system.png')
    confirm = ui.node(key='experience-copy-confirm')['bounds']
    assert confirm['y'] >= 54 and confirm['y']+confirm['height'] <= 620, confirm
    ui.press_key('experience-copy-cancel')
    assert call('list', list_query=dict(datasets=[experience['id']]))['total'] == 0
    ui.press_key('experience-copy-preview')
    ui.set('experience-copy-title', '接口排查保留可追踪信息')
    ui.set('experience-copy-conditions', conditions)
    capture('06-copy-preview-ready-system.png')
    ui.press_key('experience-copy-confirm')
    copied = driver.wait(lambda: (page['items'][0] if page['total'] == 1 else None)
                         if (page := call('list', list_query=dict(datasets=[experience['id']]))) else None)
    driver.wait(lambda: call('get', memory=copied['memory'])['index_state'] == 'indexed', timeout=90)
    v = call('get', memory=copied['memory'])
    assert v['text'] == original and v['title'] == '接口排查保留可追踪信息'
    assert v['experience']['conditions'] == conditions and v['scope_kind'] == 'experience'
    driver.wait(lambda: any(n.get('key') == 'nav-通用知识' for n in ui.nodes()) and conditions in ui.labels())
    assert call('get', memory=source_id)['revision'] == 1
    capture('07-copy-saved-narrow-system.png')
    resize(1200, 820)
    ui.press_key('nav-通用知识')
    driver.wait(lambda: '知识 2' in ui.labels())
    # The prior preference category is intentionally kept when leaving a page.
    if not any(n.get('key') == 'memory-'+copied['memory'] for n in ui.nodes()):
        ui.press_key('common-category-preference')
    driver.wait(lambda: all(any(n.get('key') == 'memory-'+m for n in ui.nodes()) for m in (pref['memory'], copied['memory'])))
    ui.press_key('memory-'+copied['memory'])
    ui.press_key('common-category-experience')
    driver.wait(lambda: any(n.get('key') == 'memory-'+copied['memory'] for n in ui.nodes()) and conditions in ui.labels())
    assert not any(n.get('key') == 'memory-'+pref['memory'] for n in ui.nodes())
    capture('08-common-experience-filter-system.png')
    ui.press_key('common-category-preference')
    driver.wait(lambda: any(n.get('key') == 'memory-'+pref['memory'] for n in ui.nodes()) and '先说明结果，再给证据。项目明确格式约定优先于个人偏好。' in ui.labels())
    assert conditions not in ui.labels()
    ui.press_key('common-category-preference')
    driver.wait(lambda: any(n.get('key') == 'memory-'+copied['memory'] for n in ui.nodes()))
    assert '先说明结果，再给证据。项目明确格式约定优先于个人偏好。' in ui.labels()
    capture('09-common-both-keeps-detail-system.png')
    ui.set('reading-search', '本轮不存在的分类关键词')
    ui.req('act', id=ui.node(key='reading-search')['id'], action='focus')
    ui.req('input', events=[dict(type='key', key='Enter')])
    ui.press_key('common-category-experience')
    driver.wait(lambda: '没有匹配的内容' in ui.labels())
    assert original not in ui.labels() and conditions not in ui.labels()
    assert '知识 2' in ui.labels()
    capture('10-common-filter-zero-system.png')
    ui.press('清除内容筛选')
    driver.wait(lambda: any(n.get('key') == 'memory-'+copied['memory'] for n in ui.nodes()))
    ui.press_key('memory-'+copied['memory'])
    capture('11-common-recovered-system.png')
    resize(980, 620)
    capture('12-common-filter-narrow-system.png')
    ui.press_key('reading-tab-raw')
    driver.wait(lambda: not any(n.get('key', '').startswith('common-category-') for n in ui.nodes()))
    assert not any(n.get('key') in ('memory-'+source_id, 'memory-'+other_id) for n in ui.nodes())
    ui.press_key('reading-tab-knowledge')
    driver.wait(lambda: conditions in ui.labels())
    report['common_category_real_dataset_filter_selection_zero_and_raw'] = True
    report['copy_preview_cancel_readonly_title_conditions_source_preserved'] = True

    # Source version changes never alter the approved independent copy.
    source_new = '来源项目已调整排查流程；独立副本仍保持用户确认时的正文。'
    call('apply', user_confirmed=True, mutation=dict(operation_id=str(uuid.uuid4()), kind='edit',
         dataset=project['id'], memory=source_id, expected_revision=1, title='可追踪的接口排查', text=source_new))
    refs = call('organization_sources', memory=copied['memory'], expected_revision=1)
    (out/'source-version-receipt.json').write_text(json.dumps(refs, ensure_ascii=False, indent=2)+'\n')
    assert len(refs) == 1 and refs[0]['source_revision'] == 1 and refs[0]['source_changed'] and refs[0]['quote'] == original
    driver.wait(lambda: any('来源知识 · 来源已变化' in label for label in ui.labels()))
    capture('12b-common-source-changed-system.png')
    ui.press_key('knowledge-source-'+source_id)
    driver.wait(lambda: source_new in ui.labels())
    if not any(n.get('key') == 'experience-copy-preview' for n in ui.nodes()): ui.press('更多操作')
    ui.press_key('experience-copy-preview')
    ui.set('experience-copy-conditions', conditions)
    call('apply', user_confirmed=True, mutation=dict(operation_id=str(uuid.uuid4()), kind='edit',
         dataset=project['id'], memory=source_id, expected_revision=2, title='可追踪的接口排查', text=source_new+'\n再次变更'))
    driver.wait(lambda: any(n.get('key') == 'experience-copy-reload' for n in ui.nodes()))
    assert 'disabled' in ui.node(key='experience-copy-confirm').get('states', [])
    capture('13-copy-stale-source-system.png')
    ui.press_key('experience-copy-reload')
    assert ui.node(key='experience-copy-original')['label'] == source_new+'\n再次变更'
    ui.press_key('experience-copy-cancel')
    assert call('list', list_query=dict(datasets=[experience['id']]))['total'] == 1
    report['copy_source_versions_stale_repreview_and_independent_snapshot'] = True

    # This is the formal IPC Agent permission contract, not a model run.
    conn = call('configure_connection', user_confirmed=True, connection=dict(
        agent='pi', name='仅通用经验授权', project=other['id'], allowed_scopes=[other['id'], experience['id']]))
    def agent_get(memory):
        return call('agent', agent_request=dict(protocol_version=1, connection_id=conn['id'],
                    generation=conn['generation'], workspace=other['path'], tool='memory_get',
                    arguments=dict(memory_id=memory)))
    allowed = agent_get(copied['memory'])
    for memory in (source_id, pref['memory']):
        try: agent_get(memory)
        except AssertionError as error:
            response = error.args[0]
            assert response['ok'] is False and response['code'] == 'scope_denied', response
        else: raise AssertionError('来源关联或分类名称扩大了权限')
    assert call('get', memory=copied['memory'])['text'] == original
    driver.wait(lambda: call('get', memory=pref['memory'])['index_state'] == 'indexed', timeout=90)
    report['actual_rust_index'] = True
    report['formal_ipc_common_authorization_independent_of_project_source'] = True
    report['model_inference_verified'] = False
    report['agent_installation_verified'] = False
    (out/'common-receipts.json').write_text(json.dumps(dict(scopes=scopes, preference=pref, copied=v,
                    sources=refs, connection=conn, allowed=allowed), ensure_ascii=False, indent=2)+'\n')
