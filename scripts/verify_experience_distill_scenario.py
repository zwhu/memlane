"""新隔离App的真实pi提炼、Native编辑确认、取消／过期／拒绝。"""
import hashlib
import json
import os
from pathlib import Path
import shutil
import sqlite3
import subprocess
import time
import uuid


def verify(ui, call, report, capture, resize, out, ipc, driver):
    root = Path(__file__).resolve().parents[1]
    client = out/'client'
    app = out/'app/darwin-arm64/MemlaneReading.app'
    adapter = out/'pi-adapter-check'
    subprocess.run(['sh', str(root/'scripts/go.sh'), 'build', '-o', str(adapter), './cmd/p2-agent-check'], cwd=root, check=True)
    # Preserve the installation's bin path so Finder's short PATH can find its Node runtime.
    executable = Path(shutil.which('pi')).absolute()
    source_pi = Path('/Users/bytedance/.pi/agent')
    originals = [source_pi/name for name in ('settings.json','models.json','auth.json')]
    hashes = {p.name: hashlib.sha256(p.read_bytes()).hexdigest() for p in originals}
    settings = json.loads((source_pi/'settings.json').read_text())
    provider, model = settings['defaultProvider'], settings['defaultModel']
    assert provider == 'deepseek', '本轮只验收实际DeepSeek配置，禁止隐式切换模型'
    pi_home = out/'pi-agent'
    pi_home.mkdir(mode=0o700)
    (pi_home/'settings.json').write_text(json.dumps(dict(defaultProvider=provider,defaultModel=model,syntheticDistillAcceptance=True))+'\n')
    for name in ('models.json','auth.json'): os.symlink(source_pi/name, pi_home/name)
    connection = options = None
    report.update(actual_pi=True, selected_provider=provider, selected_model=model,
                  model_inference_verified=False, native_candidate_edit_verified=False,
                  actual_processing_dispatch_verified=False, cleanup_model_errors=[])
    receipts = {}

    def fast_press(key):
        node = ui.node(key=key)
        assert 'disabled' not in node.get('states', []), node
        ui.req('act', id=node['id'], action='press')
        ui.req('capture')

    def adapter_call(action):
        request = dict(action=action, connection=connection, options=options,
                       run=dict(Provider=provider, Model=model, PiAuthDirectory=str(pi_home)))
        process = subprocess.run([str(adapter)], input=json.dumps(request).encode(), capture_output=True, timeout=180)
        value = json.loads(process.stdout)
        (out/(action+'-pi-receipt.json')).write_text(json.dumps(value, ensure_ascii=False, indent=2)+'\n')
        assert process.returncode == 0 and value['ok'], value
        return value['result']

    try:
        scopes = call('scopes')
        preference = next(s for s in scopes if s['kind'] == 'preference')
        experience = next(s for s in scopes if s['kind'] == 'experience')
        repo = out/'synthetic-distill-project'
        subprocess.run(['/usr/bin/git','init',str(repo)], capture_output=True, check=True)
        project = call('register_project',user_confirmed=True,project=dict(name='本轮真实提炼项目',path=str(repo)))
        source_id = str(uuid.uuid4())
        source_text = '本项目的接口服务会返回请求标识 request_id 和 HTTP 状态码。\n接口调用异常时，先保存请求标识、状态码与发生时的上下文，再核对对应日志。\n这条排查方式只适用于提供这些信息的接口，不覆盖其他项目的明确约定。'
        call('apply',user_confirmed=True,mutation=dict(kind='save',operation_id=str(uuid.uuid4()),dataset=project['id'],memory=source_id,title='接口排查保留可追踪信息',text=source_text,origin='knowledge',source='本轮合成项目输入'))
        driver.wait(lambda: call('get',memory=source_id)['index_state']=='indexed', timeout=90)
        connection = call('configure_connection',user_confirmed=True,connection=dict(agent='pi',name='本轮真实提炼pi',project='',allowed_scopes=[preference['id'],project['id']]))
        options = dict(ClientRoot=str(client),ServiceRoot=str(client/'data'),Bridge=str(app/'Contents/Resources/bin/memlane'),AgentPath=str(executable),Workspace=str(repo),PiDirectory=str(pi_home))
        adapter_call('install')
        verified = adapter_call('verify_connection')
        assert verified['process_group_gone'] and verified['provider']==provider and verified['model']==model
        report['actual_pi_connection_verified'] = True
        saved = call('processing_save',user_confirmed=True,processing_save=dict(operation_id=str(uuid.uuid4()),spec=dict(mode='agent',agent='pi',provider=provider,model=model,connection=connection['id'],connection_generation=connection['generation'])))
        validation = call('processing_validate',user_confirmed=True,processing_validation=dict(operation_id=str(uuid.uuid4()),revision=saved['revision']))
        terminal = driver.wait(lambda: (v if v['validation']['state']!='validating' else None) if (v:=call('processing_validation',processing_validation_id=validation['validation'])) else None,timeout=180)
        assert terminal['validation']['state']=='succeeded', terminal
        activation = call('processing_activate',user_confirmed=True,processing_activation=dict(operation_id=str(uuid.uuid4()),revision=saved['revision'],expected_generation=0))
        receipts['processing'] = dict(saved=saved,validation=terminal,activation=activation)
        report['actual_processing_dispatch_verified'] = True

        def tasks():
            return call('organization_list',organization_list=dict(project=project['id'],limit=20))['items']

        def task(identifier): return call('organization_get',organization_task=identifier)
        def globals_total(): return call('list',list_query=dict(datasets=[experience['id']],kind='knowledge'))['total']
        assert globals_total()==0
        driver.wait(lambda: any(n.get('key')=='project-open-'+project['id'] for n in ui.nodes()))
        ui.press_key('project-open-'+project['id'])
        driver.wait(lambda: any(n.get('key')=='memory-'+source_id for n in ui.nodes()))
        ui.press_key('memory-'+source_id)
        ui.press('更多操作')
        fast_press('experience-distill')
        first = driver.wait(lambda: (rows[0]['task'] if len(rows)==1 else None) if (rows:=tasks()) else None)

        def active_model():
            value = task(first)
            execution = value.get('execution') or {}
            if value['state'] not in ('queued','running'): raise AssertionError(('模型在活动取消前已终态',value['state']))
            with sqlite3.connect((client/'data/memlane.sqlite').as_uri()+'?mode=ro',uri=True) as db:
                row = db.execute('SELECT process_pid FROM organization_leases WHERE task=? AND released=0',(first,)).fetchone()
            return value if row and row[0]>1 and execution.get('events',{}).get('message_start',0)>=1 else None

        running = driver.wait(active_model,timeout=120)
        receipts['running_before_cancel'] = running
        (out/'running-before-cancel.json').write_text(json.dumps(running,ensure_ascii=False,indent=2)+'\n')
        ui.capture('01-generating-native-frame.png')
        report['running_capture_kind'] = 'actual_native_frame_and_tree'
        fast_press('experience-distill-cancel')
        cancelled = driver.wait(lambda: (t if t.get('cancel_requested') and (t.get('execution') or {}).get('process_group_gone') else None) if (t:=task(first)) else None,timeout=90)
        assert cancelled['execution']['process_reaped'] and cancelled['state']=='cancelled' and globals_total()==0
        receipts['cancelled'] = cancelled
        capture('02-cancelled-system.png')
        report['actual_active_generation_cancelled_without_save'] = True
        driver.wait(lambda: any(n.get('key')=='experience-distill-regenerate' for n in ui.nodes()))
        fast_press('experience-distill-regenerate')
        second = driver.wait(lambda: (next(r['task'] for r in rows if r['task']!=first) if len(rows)==2 else None) if (rows:=tasks()) else None)

        def candidate(identifier):
            value = task(identifier)
            if value['state'] in ('failed','partial_failed','interrupted','cancelled'): raise AssertionError(('真实提炼失败',value))
            return value if value['state']=='completed' and value.get('entries') else None
        generated = driver.wait(lambda:candidate(second),timeout=180)
        assert globals_total()==0 and len(generated['entries'])==1
        item = generated['entries'][0]
        assert item['state']=='review_pending' and item['item']['action']=='global_experience'
        assert generated['snapshot']['processing']['revision']['revision']==saved['revision']
        execution = generated['execution']
        assert execution['native_schema_requested'] and execution['native_schema_response_status']==200
        assert execution['process_reaped'] and execution['process_group_gone'] and execution['terminal_completed']
        assert execution['selected_provider']==provider and execution['selected_model']==model
        assert not any(count for event,count in execution.get('events',{}).items() if 'tool_execution' in event)
        for ref in item['item']['sources']:
            assert ref['memory']==source_id and ref['revision']==1 and ref['quote'] in source_text
        receipts['generated_original'] = generated
        report['model_inference_verified'] = True
        identifier = item['item_id']
        driver.wait(lambda: any(n.get('key')=='experience-review-title-'+identifier for n in ui.nodes()),timeout=30)
        resize(1280,900)
        capture('03-real-candidate-system.png')
        title='接口排查先保留可追踪信息'
        text='接口调用异常时，先保留请求标识、状态码和当时上下文，再核对对应日志。'
        conditions='适用于提供请求标识与状态码的接口。项目有明确排查约定时，优先遵循项目约定。'
        ui.set('experience-review-title-'+identifier,title)
        ui.set('experience-review-text-'+identifier,text)
        ui.set('experience-review-conditions-'+identifier,'')
        assert 'disabled' in ui.node(key='experience-distill-confirm').get('states',[])
        ui.set('experience-review-conditions-'+identifier,conditions)
        capture('04-edited-candidate-system.png')
        resize(980,620)
        body = ui.node(key='experience-distill-body')['bounds']
        ui.req('input',events=[dict(type='wheel',x=body['x']+80,y=body['y']+200,dy=650,precise=True)])
        capture('05-edited-candidate-narrow-system.png')
        footer = ui.node(key='experience-distill-confirm')['bounds']
        assert footer['y']>=54 and footer['y']+footer['height']<=620, footer
        fast_press('experience-distill-confirm')
        approved = driver.wait(lambda: (t if t['entries'][0]['state']=='applied' else None) if (t:=task(second)) else None)
        assert approved['result']==generated['result'] and approved['entries'][0]['item']==item['item']
        receipt = approved['entries'][0]['receipt']
        assert receipt['edited_experience']==dict(title=title,text=text,conditions=conditions)
        stored = driver.wait(lambda:(v if v['index_state']=='indexed' else None) if (v:=call('get',memory=receipt['memory'])) else None,timeout=90)
        assert stored['title']==title and stored['text']==text and stored['experience']['conditions']==conditions
        assert call('get',memory=source_id)['revision']==1 and globals_total()==1
        driver.wait(lambda: any(n.get('key')=='experience-distill-open-saved' for n in ui.nodes()))
        capture('06-confirmed-system.png')
        fast_press('experience-distill-open-saved')
        driver.wait(lambda: conditions in ui.labels())
        capture('07-saved-common-system.png')
        receipts['approved'] = approved
        receipts['stored'] = stored
        report['native_candidate_edit_verified'] = True
        report['original_proposal_source_and_user_approval_preserved'] = True
        report['actual_rust_index'] = True

        # A third actual candidate proves source expiry in the same Native flow.
        ui.press_key('project-open-'+project['id'])
        driver.wait(lambda: any(n.get('key')=='memory-'+source_id for n in ui.nodes()))
        ui.press_key('memory-'+source_id)
        if not any(n.get('key')=='experience-distill' for n in ui.nodes()): ui.press('更多操作')
        fast_press('experience-distill')
        third = driver.wait(lambda: (next(r['task'] for r in rows if r['task'] not in (first,second)) if len(rows)==3 else None) if (rows:=tasks()) else None)
        stale_candidate = driver.wait(lambda:candidate(third),timeout=180)
        driver.wait(lambda:any(n.get('key')=='experience-review-title-'+stale_candidate['entries'][0]['item_id'] for n in ui.nodes()))
        call('apply',user_confirmed=True,mutation=dict(kind='edit',operation_id=str(uuid.uuid4()),dataset=project['id'],memory=source_id,expected_revision=1,title='接口排查保留可追踪信息',text=source_text+'\n项目要求先核对已有排查手册。'))
        driver.wait(lambda:any(n.get('key')=='experience-distill-stale' for n in ui.nodes()))
        assert 'disabled' in ui.node(key='experience-distill-confirm').get('states',[])
        capture('08-source-expired-system.png')
        assert globals_total()==1
        receipts['stale_candidate'] = stale_candidate
        report['native_source_change_blocks_confirmation'] = True
        fast_press('experience-distill-regenerate')
        fourth = driver.wait(lambda: (next(r['task'] for r in rows if r['task'] not in (first,second,third)) if len(rows)==4 else None) if (rows:=tasks()) else None)
        rejecting = driver.wait(lambda:candidate(fourth),timeout=180)
        driver.wait(lambda:any(n.get('key')=='experience-review-title-'+rejecting['entries'][0]['item_id'] for n in ui.nodes()))
        fast_press('experience-distill-reject')
        rejected = driver.wait(lambda:(t if t['entries'][0]['state']=='rejected' else None) if (t:=task(fourth)) else None)
        assert globals_total()==1 and call('get',memory=source_id)['revision']==2
        capture('09-rejected-system.png')
        receipts['rejected'] = rejected
        report['native_rejection_preserves_project_and_common_knowledge'] = True
        (out/'distill-receipts.json').write_text(json.dumps(receipts,ensure_ascii=False,indent=2)+'\n')
        report['full_l04_all_backends_verified'] = False
    finally:
        # Always persist the completed evidence before removing temporary links.
        (out/'distill-partial-receipts.json').write_text(json.dumps(receipts,ensure_ascii=False,indent=2)+'\n')
        if connection and options:
            try: adapter_call('uninstall')
            except Exception as error: report['cleanup_model_errors'].append(repr(error))
        for name in ('models.json','auth.json'):
            path=pi_home/name
            if path.is_symlink(): path.unlink()
        report['pi_credential_links_removed']=all(not (pi_home/name).exists() for name in ('models.json','auth.json'))
        report['user_pi_configuration_unchanged']=all(hashlib.sha256(p.read_bytes()).hexdigest()==hashes[p.name] for p in originals)
        if report['cleanup_model_errors'] or not report['pi_credential_links_removed'] or not report['user_pi_configuration_unchanged']:
            raise AssertionError(('pi清理或用户配置保持核验失败',report['cleanup_model_errors']))
