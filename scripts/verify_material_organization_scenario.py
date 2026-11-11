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
from verify_ipc_lost_reply import LostIPCReply


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
    faults = set()

    recovery_mode = report['scenario'] == 'material-recovery'
    context_mode = report['scenario'] in ('material-context','material-recovery')

    def select_option(key, label):
        fast_press(key)
        def popup_option():
            nodes=ui.nodes();index={n['id']:n for n in nodes}
            return next((n for n in nodes if n.get('role')=='text' and n.get('label')==label and index.get(n.get('parent'),{}).get('role')=='popup'),None)
        option=driver.wait(popup_option);b=option['bounds']
        ui.req('input',events=[dict(type='click',x=b['x']+b['width']/2,y=b['y']+b['height']/2)])
        ui.req('idle',timeout=2000)

    def fault(name, statement=None):
        with sqlite3.connect(client/'data/memlane.sqlite', timeout=15) as db:
            if statement:
                db.execute(statement)
                faults.add(name)
            else:
                db.execute('DROP TRIGGER IF EXISTS '+name)
                faults.discard(name)

    def reopen(name):
        old = ui.pid
        ui.quit()
        assert ipc.exists(), '退出App应保留真实后台服务'
        ui.__init__(app,out,out/'screenshots',config=out/'runtime-config.json')
        resize(980,620)
        (out/(name+'-process.json')).write_text(json.dumps(dict(pid=ui.pid,previous_pid=old,automation=ui.path,native=ui.native_path,owned=True))+'\n')
        assert ui.pid != old

    def fast_press(key):
        node = driver.wait(lambda:next((n for n in ui.nodes() if n.get('key')==key),None))
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
        project = call('register_project',user_confirmed=True,project=dict(name='本轮真实材料整理项目',path=str(repo)))
        empty_project = None
        if context_mode:
            empty_repo=out/'synthetic-context-empty-project'
            subprocess.run(['/usr/bin/git','init',str(empty_repo)],capture_output=True,check=True)
            empty_project=call('register_project',user_confirmed=True,project=dict(name='本轮筛选空项目',path=str(empty_repo)))
        source_id = str(uuid.uuid4())
        source_text = '项目使用 pnpm 管理依赖。任何依赖更新都必须同时提交 pnpm-lock.yaml，以便开发环境与 CI 使用相同的依赖版本。'
        call('apply',user_confirmed=True,mutation=dict(kind='save',operation_id=str(uuid.uuid4()),dataset=project['id'],memory=source_id,title='使用 pnpm 管理依赖',text=source_text,origin='knowledge',source='本轮合成项目输入'))
        source_two = str(uuid.uuid4())
        call('apply',user_confirmed=True,mutation=dict(kind='save',operation_id=str(uuid.uuid4()),dataset=project['id'],memory=source_two,title='依赖变更同时提交锁文件',text='本项目的包管理器是 pnpm。变更依赖时，一并提交 pnpm-lock.yaml，使开发环境和 CI 中的依赖版本保持一致。这条记录与项目的依赖工具约定描述的是同一事项。',origin='knowledge',source='本轮合成项目输入'))
        for identifier in (source_id,source_two): driver.wait(lambda: call('get',memory=identifier)['index_state']=='indexed',timeout=90)
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
        report['actual_processing_configuration_activated'] = True

        def tasks(): return call('organization_list',organization_list=dict(project=project['id'],limit=30))['items']
        def task(identifier): return call('organization_get',organization_task=identifier)
        def settled(identifier):
            t=task(identifier)
            if t['state'] in ('failed','partial_failed','interrupted'): raise RuntimeError(('真实材料整理未完成',t))
            return t if t['state']=='completed' else None
        def open_config(review=False):
            ui.press_key('project-open-'+project['id'])
            driver.wait(lambda:any(n.get('key')=='memory-'+source_id for n in ui.nodes()))
            ui.press('整理记录')
            ui.press('立即整理')
            driver.wait(lambda:any(n.get('key')=='organization-project' for n in ui.nodes()))
            fast_press('organization-project')
            option=driver.wait(lambda:next((n for n in ui.nodes() if n.get('label')==project['name'] and n.get('role')=='text' and n.get('parent')),None))
            bounds=option['bounds']
            ui.req('input',events=[dict(type='click',x=bounds['x']+bounds['width']/2,y=bounds['y']+bounds['height']/2)])
            ui.req('idle',timeout=2000)
            driver.wait(lambda:any(n.get('key')=='organization-processing-executor' for n in ui.nodes()))
            assert model in ui.node(key='organization-processing-executor')['label']
            if review: fast_press('organization-review-mode')
            for identifier in (source_id,source_two):
                driver.wait(lambda:any(n.get('key')=='organization-choose-'+identifier for n in ui.nodes()))
                fast_press('organization-choose-'+identifier)

        driver.wait(lambda:any(n.get('key')=='project-open-'+project['id'] for n in ui.nodes()))
        open_config()
        assert tasks()==[]
        capture('01-materials-configured-system.png')
        resize(980,620)
        capture('02-materials-configured-narrow-system.png')
        footer=ui.node(key='organization-start')['bounds']
        assert footer['y']>=54 and footer['y']+footer['height']<=620,footer
        if recovery_mode:
            # The actual daemon commits Start; only its response is dropped.
            with LostIPCReply(ipc,'organization_start') as loss:
                fast_press('organization-start')
                driver.wait(lambda:loss.dropped.is_set(),timeout=30)
            receipts['lost_start_transport']=loss.evidence
            driver.wait(lambda:any(n.get('key')=='organization-start-receipt' for n in ui.nodes()))
            capture('02b-start-response-lost-system.png')
            fast_press('organization-start-receipt')
            driver.wait(lambda:not any(n.get('key')=='organization-project' for n in ui.nodes()))
            report['actual_unknown_start_read_recovers_same_task']=True
        else:
            fast_press('organization-start')
        first=driver.wait(lambda:(rows[0]['task'] if len(rows)==1 else None) if (rows:=tasks()) else None)
        completed=driver.wait(lambda:settled(first),timeout=180)
        assert completed['snapshot']['schema']=='memlane-organization-v1'
        assert len(completed['snapshot']['inputs'])==2 and completed['snapshot']['processing']['revision']['revision']==saved['revision']
        assert completed['execution']['selected_model']==model and completed['execution']['process_reaped'] and completed['execution']['process_group_gone']
        report['actual_processing_dispatch_verified'] = True
        assert completed['entries'] and all(e['state']=='applied' and e['item']['action'] in ('summary','add_tags') for e in completed['entries'])
        memories=[e['receipt']['memory'] for e in completed['entries'] if e['item']['action']=='summary']
        assert memories
        for identifier in memories:
            driver.wait(lambda:call('get',memory=identifier)['index_state']=='indexed',timeout=90)
            assert call('get',memory=identifier)['source']=='pi：自动摘要'
        assert call('get',memory=source_id)['revision']==1 and call('get',memory=source_two)['revision']==1
        driver.wait(lambda:'自动完成' in ui.labels())
        capture('03-automatic-results-system.png')
        receipts['automatic']=completed
        report['actual_material_processing_and_rust_index']=True
        before_ui_pid=ui.pid
        reopen('material-task-reopen')
        driver.wait(lambda:'整理记录' in ui.labels())
        ui.press('整理记录')
        driver.wait(lambda:any(n.get('key')=='organization-task-'+first for n in ui.nodes()))
        fast_press('organization-task-'+first)
        driver.wait(lambda:'自动完成' in ui.labels())
        reloaded=task(first)
        assert reloaded['attempt']==completed['attempt'] and reloaded['execution']==completed['execution'] and len(tasks())==1 and ui.pid!=before_ui_pid
        capture('04-same-task-reopened-system.png')
        report['actual_material_task_reopen_without_model_restart']=True

        if context_mode:
            resize(1280,900)
            fast_press('organization-list-filter')
            select_option('organization-filter-origin','计划')
            driver.wait(lambda:ui.node(key='organization-list-count')['label']=='0 次' and '没有匹配的整理记录' in ui.labels())
            assert '整理已完成' in ui.labels(), '筛选零结果不能清空当前任务详情'
            capture('04c-plan-filter-zero-keeps-task-system.png')
            fast_press('organization-list-clear')
            driver.wait(lambda:ui.node(key='organization-list-count')['label']=='1 次')
            select_option('organization-filter-project',empty_project['name'])
            driver.wait(lambda:'没有匹配的整理记录' in ui.labels())
            capture('04d-project-filter-zero-keeps-task-system.png')
            fast_press('organization-list-clear')
            driver.wait(lambda:ui.node(key='organization-list-count')['label']=='1 次')
            select_option('organization-filter-origin','手动')
            select_option('organization-filter-time','今天')
            driver.wait(lambda:ui.node(key='organization-list-count')['label']=='1 次')
            assert call('organization_list',organization_list=dict(project=project['id'],origin='manual',limit=10))['total']==1
            assert call('organization_list',organization_list=dict(project=project['id'],origin='schedule',limit=10))['total']==0
            capture('04e-manual-today-filter-system.png')
            fast_press('organization-list-clear')
            fast_press('organization-list-filter')
            report['actual_native_origin_project_time_filters_keep_task']=True

        open_config()
        fast_press('organization-start')
        cancelling=driver.wait(lambda:(next(r['task'] for r in rows if r['task']!=first) if len(rows)==2 else None) if (rows:=tasks()) else None)
        def active_model():
            t=task(cancelling)
            if t['state'] not in ('queued','running'):raise RuntimeError(('未赶上活动取消',t['state']))
            with sqlite3.connect((client/'data/memlane.sqlite').as_uri()+'?mode=ro',uri=True) as db:
                row=db.execute('SELECT process_pid FROM organization_leases WHERE task=? AND released=0',(cancelling,)).fetchone()
            return t if row and row[0]>1 and (t.get('execution') or {}).get('events',{}).get('message_start',0)>0 else None
        running=driver.wait(active_model,timeout=120)
        receipts['running_before_cancel']=running
        driver.wait(lambda:any(n.get('key')=='organization-cancel' for n in ui.nodes()))
        fast_press('organization-cancel')
        cancelled=driver.wait(lambda:(t if t.get('cancel_requested') and (t.get('execution') or {}).get('process_group_gone') and (t.get('execution') or {}).get('process_reaped') else None) if (t:=task(cancelling)) else None,timeout=90)
        assert cancelled['state']=='cancelled'
        driver.wait(lambda:'已取消，已提交内容保留' in ui.labels())
        capture('04b-actual-active-cancel-system.png')
        receipts['cancelled']=cancelled
        report['actual_material_model_cancel_reaped']=True

        open_config(review=True)
        capture('05-review-range-system.png')
        fast_press('organization-start')
        second=driver.wait(lambda:(next(r['task'] for r in rows if r['task'] not in (first,cancelling)) if len(rows)==3 else None) if (rows:=tasks()) else None)
        suggestions=driver.wait(lambda:settled(second),timeout=180)
        assert suggestions['snapshot']['schema']=='memlane-organization-v2'
        review_entries=[e for e in suggestions['entries'] if e['state']=='review_pending']
        assert review_entries,'本轮真实模型未提出可审核建议'
        assert call('get',memory=source_id)['revision']==1 and call('get',memory=source_two)['revision']==1
        entry=next((e for e in review_entries if e['item']['action']=='merge'),review_entries[0])

        if context_mode:
            others=[e for e in review_entries if e['item_id']!=entry['item_id']]
            assert others,'本轮模型合法建议不足两项，未取得多项审核证据'
            rejected_entry=others[0]
            reject_id=rejected_entry['item_id'];reason='本次保留独立来源，暂不接受这一项建议。'
            fast_press('organization-review-open-'+reject_id)
            reason_key='organization-reject-reason-'+reject_id
            driver.wait(lambda:any(n.get('key')==reason_key for n in ui.nodes()))
            resize(980,620)
            body=ui.node(key='organization-detail-scroll')['bounds']
            ui.req('input',events=[dict(type='wheel',x=body['x']+80,y=body['y']+200,dy=1600,precise=True)])
            ui.set(reason_key,reason)
            bounds=ui.node(key=reason_key)['bounds'];assert 54<=bounds['y'] and bounds['y']+bounds['height']<=620,bounds
            capture('05b-optional-rejection-reason-narrow-system.png')
            fast_press('organization-review-next')
            for _ in range(len(review_entries)):
                if any(n.get('key')=='organization-confirm-'+entry['item_id'] for n in ui.nodes()): break
                fast_press('organization-review-next')
            driver.wait(lambda:any(n.get('key')=='organization-confirm-'+entry['item_id'] for n in ui.nodes()))
            fast_press('organization-review-return')
            fast_press('organization-review-open-'+reject_id)
            driver.wait(lambda:ui.node(key=reason_key).get('value')==reason)
            bounds=ui.node(key=reason_key)['bounds'];assert 54<=bounds['y'] and bounds['y']+bounds['height']<=620,bounds
            capture('05c-return-keeps-item-reason-and-scroll-system.png')
            if recovery_mode:
                ui.req('act',id=ui.node(key=reason_key)['id'],action='focus')
                ui.req('capture')
                draft=call('organization_preview_get')
                # Quit performs the final durability barrier for focus/scroll.
                before_count=len(tasks())
                reopen('unsubmitted-material-draft-reopen')
                driver.wait(lambda:any(n.get('key')==reason_key for n in ui.nodes()))
                assert ui.node(key=reason_key).get('value')==reason
                tree=ui.req('tree')
                assert tree.get('focus')==ui.node(key=reason_key)['id'],tree.get('focus')
                restored=call('organization_preview_get')
                assert restored['preview']['candidate']==reject_id and restored['preview']['focus']==reason_key
                assert restored['preview']['reject_reasons'][reject_id]==reason
                assert restored['preview']['scroll']>0 and restored['task']['task']==second
                assert len(tasks())==before_count and task(second)['execution']==suggestions['execution']
                assert next(e for e in task(second)['entries'] if e['item_id']==reject_id)['state']=='review_pending'
                capture('05c1-unsubmitted-draft-focus-restored-system.png')
                receipts['unsubmitted_draft_reopened']=restored
                report['actual_unsubmitted_reason_focus_scroll_reopen_without_model']=True
                with LostIPCReply(ipc,'organization_review') as loss:
                    fast_press('organization-reject-'+reject_id)
                    driver.wait(lambda:loss.dropped.is_set(),timeout=30)
                receipts['lost_reject_transport']=loss.evidence
            else:
                fast_press('organization-reject-'+reject_id)
            rejected=driver.wait(lambda:(t if next(e for e in t['entries'] if e['item_id']==reject_id)['state']=='rejected' else None) if (t:=task(second)) else None)
            decision=next(e for e in rejected['entries'] if e['item_id']==reject_id)
            assert decision['item']==rejected_entry['item'] and decision['receipt']['rejection_reason']==reason and not decision['receipt'].get('memory')
            assert call('get',memory=source_id)['revision']==1 and call('get',memory=source_two)['revision']==1
            capture('05d-rejection-keeps-source-knowledge-system.png')
            receipts['rejected']=rejected
            reopen('rejected-material-task-reopen')
            driver.wait(lambda:'整理记录' in ui.labels())
            ui.press('整理记录')
            driver.wait(lambda:any(n.get('key')=='organization-task-'+second for n in ui.nodes()))
            fast_press('organization-task-'+second)
            if any(n.get('key')=='organization-review-return' for n in ui.nodes()):
                fast_press('organization-review-return')
            driver.wait(lambda:any(n.get('key')=='organization-review-open-'+reject_id for n in ui.nodes()))
            fast_press('organization-review-open-'+reject_id)
            driver.wait(lambda:any(n.get('key')=='organization-rejected-reason' for n in ui.nodes()))
            assert ui.node(key='organization-rejected-reason')['label']=='拒绝理由：'+reason
            reloaded=task(second);assert reloaded['execution']==suggestions['execution'] and len(tasks())==3
            capture('05e-rejected-receipt-reopened-system.png')
            receipts['rejected_reopened']=reloaded
            report['actual_native_rejection_reason_persisted_without_knowledge_change']=True
            report['actual_native_multi_item_draft_scroll_and_reopen']=True
            fast_press('organization-review-return')
        driver.wait(lambda:any(n.get('key')=='organization-review-open-'+entry['item_id'] for n in ui.nodes()))
        fast_press('organization-review-open-'+entry['item_id'])
        driver.wait(lambda:'具体影响' in ui.labels())
        resize(1280,900)
        capture('06-proposal-source-comparison-system.png')
        resize(980,620)
        capture('07-proposal-source-comparison-narrow-system.png')
        key='organization-confirm-'+entry['item_id']
        bounds=ui.node(key=key)['bounds'];assert bounds['y']+bounds['height']<=620,bounds
        fast_press(key)
        approved=driver.wait(lambda:(t if next(e for e in t['entries'] if e['item_id']==entry['item_id'])['state']=='applied' else None) if (t:=task(second)) else None)
        applied=next(e for e in approved['entries'] if e['item_id']==entry['item_id'])
        assert applied['item']==entry['item'] and applied['receipt']
        if applied['receipt'].get('memory'):
            driver.wait(lambda:call('get',memory=applied['receipt']['memory'])['index_state']=='indexed',timeout=90)
            if entry['item']['action']=='merge':assert call('get',memory=applied['receipt']['memory'])['source']=='pi：用户确认整理合并'
        driver.wait(lambda:ui.node(key='organization-review-state')['label']=='已应用')
        capture('08-approved-proposal-system.png')
        fast_press('organization-review-return')
        driver.wait(lambda:'已确认' in ui.labels())
        capture('09-grouped-decisions-system.png')
        receipts['suggestions']=suggestions;receipts['approved']=approved
        if context_mode:
            final_rejected=next(e for e in approved['entries'] if e['item_id']==reject_id)
            assert final_rejected['state']=='rejected' and final_rejected['receipt']==decision['receipt']
            report['actual_same_task_rejected_and_confirmed_decisions']=True
        report['actual_native_review_does_not_mutate_before_confirmation']=True
        report['actual_native_single_review_with_atomic_receipt']=True
        report['model_inference_verified']=True
        report['actual_rust_index']=True
        report['full_jk_all_backends_verified']=False
        (out/'material-organization-receipts.json').write_text(json.dumps(receipts,ensure_ascii=False,indent=2)+'\n')
    finally:
        for name in list(faults):
            try: fault(name)
            except Exception as error: report['cleanup_model_errors'].append(repr(error))
        (out/'material-organization-partial-receipts.json').write_text(json.dumps(receipts,ensure_ascii=False,indent=2)+'\n')
        if connection and options:
            try: adapter_call('uninstall')
            except Exception as error: report['cleanup_model_errors'].append(repr(error))
        for name in ('models.json','auth.json'):
            path=pi_home/name
            if path.is_symlink():path.unlink()
        report['pi_credential_links_removed']=all(not (pi_home/name).exists() for name in ('models.json','auth.json'))
        report['user_pi_configuration_unchanged']=all(hashlib.sha256(p.read_bytes()).hexdigest()==hashes[p.name] for p in originals)
        if report['cleanup_model_errors'] or not report['pi_credential_links_removed'] or not report['user_pi_configuration_unchanged']:
            raise AssertionError(('pi清理或用户配置保持核验失败',report['cleanup_model_errors']))
