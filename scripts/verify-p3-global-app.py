#!/usr/bin/env python3
"""全局经验真实 App/traex/Rust 验收；仅使用隔离合成数据。"""
import hashlib, json, os, socket, uuid
from pathlib import Path
from importlib.machinery import SourceFileLoader

ROOT = Path(__file__).resolve().parents[1]
OUT = ROOT / 'build/p3-global'
f = Path((OUT / 'fixture-path.txt').read_text().strip())
meta = json.loads((f / 'source-info.json').read_text())
app = Path(meta['app'])
G = SourceFileLoader('globalguard', str(ROOT / 'scripts/p3-c2-common.py')).load_module()
G.validate_fixture(f, app, phase='global')
U = SourceFileLoader('globalui', str(ROOT / 'scripts/p1-ui.py')).load_module()
data = f / 'client/data'
ipc = f'/tmp/memlane-{os.getuid()}-{hashlib.sha256(str(data).encode()).hexdigest()[:12]}/service.sock'

def response(op, **kw):
    with socket.socket(socket.AF_UNIX) as s:
        s.settimeout(8)
        s.connect(ipc)
        s.sendall((json.dumps({'protocol': 1, 'id': str(uuid.uuid4()), 'op': op, **kw}) + '\n').encode())
        return json.loads(s.makefile('rb', buffering=0).readline())

def call(op, **kw):
    r = response(op, **kw)
    assert r['ok'], r
    return r.get('result')

def visible(key):
    return any(n.get('key') == key for n in ui.nodes())

def select_memory(v):
    ui.press('全部记忆')
    U.wait(lambda: visible('memory-' + v['memory']))
    ui.press_key('memory-' + v['memory'])
    U.wait(lambda: call('get', memory=v['memory'])['text'] in ui.labels())

def start_distill(v, finish=True):
    select_memory(v)
    before = {t['task'] for t in call('organization_list')['items']}
    U.wait(lambda: visible('experience-distill'))
    ui.press_key('experience-distill')
    tid = U.wait(lambda: next((t['task'] for t in call('organization_list')['items'] if t['task'] not in before), None))
    if not finish:
        return tid
    task = U.wait(lambda: (lambda t: t if t['state'] not in ['queued', 'running', 'applying'] else None)(call('organization_get', organization_task=tid)), timeout=150)
    assert task['snapshot']['schema'] == 'memlane-organization-v3' and task['result'], task
    entry = next(e for e in task['entries'] if e['item']['action'] == 'global_experience')
    assert entry['state'] == 'review_pending' and entry['item']['conditions']
    U.wait(lambda: visible('organization-confirm-' + entry['item_id']))
    assert entry['item']['conditions'] in ui.labels()
    assert call('get', memory=v['memory'])['revision'] == v['revision']
    return task, entry

def verify_index(v):
    U.wait(lambda: call('get', memory=v['memory'])['index_state'] == 'indexed', timeout=95)
    hits = call('search', datasets=[v['dataset']], query=v['text'], limit=50)
    assert any(h['version']['memory'] == v['memory'] and h['version']['revision'] == v['revision'] for h in hits)

ui = None
report = {'status': 'failed', 'actual_app': True, 'real_organizer': 'traex', 'real_rust': True, 'model_fixture': False, 'synthetic_only': True, 'fixture': str(f.resolve()), 'identifier': meta['identifier'], 'cases': []}
try:
    ui = U.UI(app, f, ROOT / 'build/p3-global-screenshots', extra_env={'PI_CODING_AGENT_DIR': str(f / 'pi-agent')})
    U.wait(lambda: call('health')['state'] == 'ready', timeout=95)
    assert not call('organization_list')['items'], 'fresh fixture required'
    for name, repo in [('全局验收源项目', 'repo-a'), ('全局验收其他项目', 'repo-b')]:
        ui.press('项目')
        ui.set('project-name', name)
        ui.set('project-path', str(f / repo))
        ui.press_key('register-project')
        U.wait(lambda: any(s['name'] == name for s in call('scopes')))
    scopes = call('scopes')
    project = next(s['id'] for s in scopes if s['name'] == '全局验收源项目')
    other = next(s['id'] for s in scopes if s['name'] == '全局验收其他项目')
    global_id = next(s['id'] for s in scopes if s['kind'] == 'experience')
    source = call('apply', user_confirmed=True, mutation={'kind': 'save', 'operation_id': str(uuid.uuid4()), 'memory': str(uuid.uuid4()), 'dataset': project, 'title': '合成依赖管理约定', 'text': '本项目使用 pnpm 管理依赖。修改依赖前先核对项目已确认的工具约定；依赖变更与 pnpm-lock.yaml 一起提交。', 'source': '合成项目维护记录', 'source_detail': {'fragment': 'GLOBAL_PRIVATE_ORIGIN_MARKER', 'references': ['synthetic://global/private-project-only']}})
    select_memory(source)
    before = call('list', list_query={'datasets': [global_id], 'kind': 'knowledge'})['total']
    ui.press_key('experience-copy-preview')
    U.wait(lambda: visible('experience-copy-confirm'))
    assert call('list', list_query={'datasets': [global_id], 'kind': 'knowledge'})['total'] == before
    ui.system_capture('copy-preview-system.png')
    ui.press_key('experience-copy-confirm')
    copied = U.wait(lambda: next((v for v in call('list', list_query={'datasets': [global_id], 'kind': 'knowledge'})['items'] if v.get('experience', {}).get('kind') == 'copy'), None))
    assert copied['text'] == source['text'] and copied['original'] == source['text']
    assert call('get', memory=source['memory'])['revision'] == 1
    U.wait(lambda: '用户原文复制' in ui.labels())
    ui.system_capture('copy-current-system.png')
    verify_index(copied)
    report['cases'].append({'action': 'copy', 'memory': copied['memory'], 'revision': 1, 'app_preview_and_confirm': True, 'real_rust': True})

    task, entry = start_distill(source)
    assert call('list', list_query={'datasets': [global_id], 'kind': 'knowledge'})['total'] == before + 1
    ui.system_capture('distill-pending-system.png')
    ui.press_key('organization-confirm-' + entry['item_id'])
    done = U.wait(lambda: (lambda t: t if t['entries'][0]['state'] == 'applied' else None)(call('organization_get', organization_task=task['task'])))
    receipt = done['entries'][0]['receipt']
    distilled = call('get', memory=receipt['memory'])
    assert distilled['experience']['kind'] == 'distill' and distilled['experience']['conditions'] == entry['item']['conditions']
    assert distilled['text'] == entry['item']['text'] and distilled['original'] == distilled['text']
    assert 'GLOBAL_PRIVATE_ORIGIN_MARKER' not in json.dumps(distilled)
    assert call('get', memory=source['memory'])['source_detail'] == source['source_detail']
    verify_index(distilled)
    item_digest = hashlib.sha256(json.dumps(entry['item'], ensure_ascii=False, separators=(',', ':')).replace('&', '\\u0026').replace('<', '\\u003c').replace('>', '\\u003e').encode()).hexdigest()
    replay = call('organization_review', user_confirmed=True, organization_review={'task': task['task'], 'item_id': entry['item_id'], 'item_digest': item_digest, 'receipt_id': receipt['receipt_id'], 'confirm': True})
    assert replay['entries'][0]['receipt'] == receipt and call('get', memory=distilled['memory'])['revision'] == 1
    ui.press_key('organization-open-' + entry['item_id'])
    U.wait(lambda: '用户确认通用提炼' in ui.labels())
    ui.system_capture('distill-current-system.png')
    ui.press('编辑')
    edited_conditions = '适用于有已确认包管理约定的项目；项目规则优先。合成条件修订。'
    ui.set('experience-conditions', edited_conditions)
    ui.set('memory-draft', distilled['text'] + ' 变更前核对项目要求。')
    ui.press('保存')
    U.wait(lambda: call('get', memory=distilled['memory'])['revision'] == 2)
    assert call('get', memory=distilled['memory'])['experience']['conditions'] == edited_conditions
    U.wait(lambda: '历史记录' in ui.labels())
    ui.press('历史记录')
    U.wait(lambda: edited_conditions in ui.labels())
    ui.press('恢复 v1')
    U.wait(lambda: entry['item']['conditions'] in ui.labels())
    ui.press('确认恢复')
    restored = U.wait(lambda: (lambda v: v if v['revision'] == 3 else None)(call('get', memory=distilled['memory'])))
    assert restored['experience']['conditions'] == entry['item']['conditions'] and restored['text'] == distilled['text']
    assert call('organization_get', organization_task=task['task'])['entries'][0]['receipt'] == receipt
    verify_index(restored)
    ui.system_capture('conditions-restored-system.png')
    report['cases'].append({'action': 'distill', 'task': task['task'], 'item': entry['item_id'], 'receipt': receipt, 'restored_revision': 3, 'conditions_history_restore': True, 'source_preserved': True, 'real_rust': True})

    task_reject, reject = start_distill(source)
    count = call('list', list_query={'datasets': [global_id], 'kind': 'knowledge'})['total']
    ui.press_key('organization-reject-' + reject['item_id'])
    U.wait(lambda: call('organization_get', organization_task=task_reject['task'])['entries'][0]['state'] == 'rejected')
    assert call('list', list_query={'datasets': [global_id], 'kind': 'knowledge'})['total'] == count
    report['cases'].append({'action': 'reject', 'task': task_reject['task'], 'knowledge_unchanged': True})

    task_expire, expire = start_distill(source)
    select_memory(source)
    ui.press('编辑')
    ui.set('memory-draft', source['text'] + ' 用户已补充项目适用范围。')
    ui.press('保存')
    source = U.wait(lambda: (lambda v: v if v['revision'] == 2 else None)(call('get', memory=source['memory'])))
    U.wait(lambda: call('organization_get', organization_task=task_expire['task'])['entries'][0]['state'] == 'expired')
    ui.press('整理记录')
    ui.press_key('organization-tab-summary')
    U.wait(lambda: visible('organization-task-' + task_expire['task']))
    ui.press_key('organization-task-' + task_expire['task'])
    U.wait(lambda: '执行已完成 · 有过期建议' in ui.labels())
    ui.system_capture('global-expired-system.png')
    report['cases'].append({'action': 'expire', 'task': task_expire['task'], 'source_revision': 2})

    # General organization discovers a candidate but still requires approval.
    ui.press('全局验收源项目')
    ui.press('立即整理')
    ui.press_key('organization-global-mode')
    U.wait(lambda: visible('organization-choose-' + source['memory']))
    ui.press_key('organization-choose-' + source['memory'])
    old = {t['task'] for t in call('organization_list')['items']}
    ui.press_key('organization-start')
    candidate_id = U.wait(lambda: next((t['task'] for t in call('organization_list')['items'] if t['task'] not in old), None))
    candidates = U.wait(lambda: (lambda t: t if t['state'] not in ['queued', 'running', 'applying'] else None)(call('organization_get', organization_task=candidate_id)), timeout=150)
    global_entry = next(e for e in candidates['entries'] if e['item']['action'] == 'global_experience')
    assert global_entry['state'] == 'review_pending'
    U.wait(lambda: visible('organization-confirm-' + global_entry['item_id']))
    ui.system_capture('organization-candidate-system.png')
    ui.press_key('organization-reject-' + global_entry['item_id'])
    U.wait(lambda: next(e for e in call('organization_get', organization_task=candidate_id)['entries'] if e['item_id'] == global_entry['item_id'])['state'] == 'rejected')
    report['cases'].append({'action': 'organization_candidate', 'task': candidate_id, 'confirmed_boundary': True})

    cancel_id = start_distill(source, finish=False)
    U.wait(lambda: (call('organization_get', organization_task=cancel_id).get('execution') or {}).get('process_active'))
    U.wait(lambda: visible('organization-cancel'))
    ui.press_key('organization-cancel')
    cancelled = U.wait(lambda: (lambda t: t if t['state'] == 'cancelled' and (t.get('execution') or {}).get('process_reaped') and (t.get('execution') or {}).get('process_group_gone') else None)(call('organization_get', organization_task=cancel_id)))
    assert not cancelled.get('result')
    report['cases'].append({'action': 'active_cancel', 'task': cancel_id, 'real_traex': True, 'reaped': True})

    # Agent transport checks use the actual service, no model. The real daily
    # pi read is a separate acceptance command, not inferred from these calls.
    connection = call('configure_connection', user_confirmed=True, connection={'agent': 'pi', 'name': 'E 合成全局消费者', 'project': other, 'allowed_scopes': [other, global_id]})
    def agent(tool, **kw):
        return response('agent', agent_request={'protocol_version': 1, 'connection_id': connection['id'], 'generation': connection['generation'], 'workspace': str(f / 'repo-b'), 'tool': tool, 'arguments': kw})
    denied = agent('memory_get', memory_id=source['memory'], scopes=[project])
    assert not denied['ok'] and denied['code'] == 'scope_denied', denied
    read = agent('memory_get', memory_id=restored['memory'], scopes=[global_id])
    assert read['ok'] and read['result']['experience']['conditions'] == restored['experience']['conditions']
    assert 'GLOBAL_PRIVATE_ORIGIN_MARKER' not in json.dumps(read)
    hits = agent('memory_search', query=restored['text'], scopes=[other, global_id], limit=50)
    assert hits['ok'] and any(h['version']['memory'] == restored['memory'] for h in hits['result']['hits'])
    assert all(h['version']['dataset'] in [other, global_id] for h in hits['result']['hits'])
    report['agent_transport_scope'] = True
    (f / 'global-read-target.json').write_text(json.dumps({'connection': connection, 'memory': restored, 'source': source, 'project': project, 'other': other, 'global': global_id}, ensure_ascii=False, indent=2))
    ui.quit(stop=True)
    ui = None
    report.update(status='passed', service_stopped=True, daily_agent_model_read=False)
except Exception as e:
    report['error'] = repr(e)
    raise
finally:
    if ui:
        ui.quit(stop=True)
    (OUT / 'real-app.json').write_text(json.dumps(report, ensure_ascii=False, indent=2) + '\n')
print(json.dumps(report, ensure_ascii=False))
