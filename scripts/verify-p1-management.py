#!/usr/bin/env python3
"""Complete the actual P1 App memory lifecycle and adversarial edit/draft paths."""
import argparse,hashlib,json,os,socket,time,uuid
from pathlib import Path
from importlib.machinery import SourceFileLoader
ROOT=Path(__file__).resolve().parents[1]
driver=SourceFileLoader('p1ui',str(ROOT/'scripts/p1-ui.py')).load_module()

def main():
    p=argparse.ArgumentParser(description=__doc__);p.add_argument('--app',type=Path,default=ROOT/'build/p1-app/darwin-arm64/MemlaneP1.app');a=p.parse_args()
    root=Path((ROOT/'build/p1/fixture-path.txt').read_text().strip());cfg=json.loads((root/'runtime-config.json').read_text());data=Path(cfg['root'])/'data';assert 'memlane-p1-app-' in str(root)
    ipc=f"/tmp/memlane-{os.getuid()}-{hashlib.sha256(str(data).encode()).hexdigest()[:12]}/service.sock"
    def call(op,**fields):
        with socket.socket(socket.AF_UNIX) as s:
            s.settimeout(15);s.connect(ipc);s.sendall((json.dumps({'protocol':1,'id':str(uuid.uuid4()),'op':op,**fields})+'\n').encode());r=json.loads(s.makefile('rb',buffering=0).readline())
        assert r['ok'],r;return r.get('result')
    def current(id_,revision=None):
        v=call('get',memory=id_)
        return v if revision is None or v['revision']==revision else None
    def indexed(id_):return call('get',memory=id_)['index_state']=='indexed'
    out=ROOT/'build/p1-screenshots';report={'status':'failed','synthetic_only':True,'steps':[]};ui=None
    def step(name,fn):
        begin=time.monotonic();result=fn();entry={'step':name,'status':'passed','seconds':round(time.monotonic()-begin,3),'evidence':result};report['steps'].append(entry)
        (ROOT/'build/p1/management-verification.json').write_text(json.dumps(report,ensure_ascii=False,indent=2)+'\n');print(json.dumps(entry,ensure_ascii=False),flush=True)
    try:
        ui=driver.UI(a.app.resolve(),root,out);ui.press('设置')
        driver.wait(lambda:'启动服务' in ui.labels() or '停止服务' in ui.labels(),timeout=85)
        if '启动服务' in ui.labels():ui.press('启动服务')
        driver.wait(lambda:call('health')['state']=='ready',timeout=85)
        project=next(s for s in call('scopes') if s['name']=='P1 本地合成项目')
        tag=uuid.uuid4().hex[:8];title='App 项目经验 '+tag;original='P1 originalAppToken_'+tag+' 保留 request_id 便于排查。';edited='P1 currentAppToken_'+tag+' 新版本改为保留 request_id 和错误码。';memory=None
        baseline=call('activities',timezone='Asia/Shanghai')['total']
        def create_view():
            nonlocal memory
            ui.press(project['name']);ui.press('新增记忆');ui.set('memory-title',title);ui.set('memory-draft',original)
            assert project['name'] in ui.labels();ui.capture('management-new.png');ui.press('保存')
            def saved():return next((v for v in call('list',list_query={'datasets':[project['id']]})['items'] if v['title']==title),None)
            record=driver.wait(saved);memory=record['memory'];assert record['revision']==1 and record['text']==original
            driver.wait(lambda:original in ui.labels());driver.wait(lambda:indexed(memory))
            ui.capture('management-created.png')
            return {'ui_save':True,'revision':1,'dataset_matches_project':record['dataset']==project['id'],'text_read_in_app':True}
        step('create-view',create_view)
        def edit_search():
            ui.press('编辑');ui.set('memory-draft',edited);ui.press('保存');driver.wait(lambda:current(memory,2));driver.wait(lambda:indexed(memory))
            ui.set('memory-search','currentAppToken_'+tag);ui.press('搜索');driver.wait(lambda:title in [n.get('label') for n in ui.nodes() if n.get('role')=='button']);ui.press(title)
            driver.wait(lambda:edited in ui.labels());assert 'v2 · 手动编辑' in ui.labels();ui.capture('management-search.png')
            assert current(memory)['text']==edited
            return {'ui_edit':True,'revision':2,'ui_search_current_version':True,'traceable_original_retained':original in ui.labels()}
        step('edit-search',edit_search)
        def history_restore():
            ui.press('历史记录');driver.wait(lambda:'恢复 v1' in ui.labels());assert original in ui.labels() and edited in ui.labels();ui.capture('management-history.png')
            ui.press('恢复 v1');assert current(memory)['revision']==2;ui.capture('management-confirm.png');ui.press('确认恢复')
            driver.wait(lambda:current(memory,3));driver.wait(lambda:original in ui.labels());driver.wait(lambda:indexed(memory));ui.capture('management-restored.png')
            assert current(memory)['text']==original;h=call('history',memory=memory)['items'];assert [v['revision'] for v in h]==[3,2,1];assert h[1]['text']==edited
            return {'ui_history_and_diff':True,'in_pane_confirmation_does_not_mutate':True,'ui_restore_adds_revision':3,'prior_versions_immutable':True}
        step('history-restore',history_restore)
        def restart():
            nonlocal ui
            ui.quit();ui=None;assert current(memory)['revision']==3
            ui=driver.UI(a.app.resolve(),root,out);ui.press(project['name']);driver.wait(lambda:title in ui.labels());ui.press(title);driver.wait(lambda:original in ui.labels());assert 'v3 · 恢复版本' in ui.labels();ui.press('历史记录');driver.wait(lambda:'恢复 v2' in ui.labels());assert edited in ui.labels();ui.capture('management-restarted.png');ui.system_capture('system-management-restarted.png')
            return {'actual_app_process_restarted':True,'restored_current_read_in_app':True,'history_survives_restart':True,'default_exit_keeps_service':True}
        step('restart-read',restart)
        def stale_edit():
            id_=str(uuid.uuid4());name='并发编辑合成夹具 '+tag
            initial={'operation_id':str(uuid.uuid4()),'kind':'save','dataset':project['id'],'memory':id_,'expected_revision':0,'title':name,'text':'initialConcurrentToken '+tag}
            call('apply',mutation=initial,user_confirmed=True);ui.press('全部记忆');driver.wait(lambda:name in ui.labels());ui.press(name);driver.wait(lambda:'编辑' in ui.labels());ui.press('编辑');draft='myStaleDraftToken '+tag;ui.set('memory-draft',draft)
            external='externalCurrentToken '+tag;call('apply',mutation={**initial,'operation_id':str(uuid.uuid4()),'kind':'edit','expected_revision':1,'text':external},user_confirmed=True)
            ui.press('保存');driver.wait(lambda:any('内容已有新版本' in label for label in ui.labels()));assert current(id_)['revision']==2 and current(id_)['text']==external;assert ui.node(key='memory-draft')['value']==draft;ui.capture('management-conflict.png')
            ui.press('按最新版本继续编辑');assert ui.node(key='memory-draft')['value']==draft;ui.press('保存');driver.wait(lambda:current(id_,3));assert current(id_)['text']==draft
            return {'expired_editor_does_not_overwrite':True,'draft_retained':True,'latest_base_requires_explicit_action':True}
        step('stale-edit-conflict',stale_edit)
        def outage_draft():
            ui.press('设置');ui.press('停止服务');driver.wait(lambda:'启动服务' in ui.labels());ui.press(project['name']);ui.press('新增记忆');name='服务中断草稿 '+tag;draft='durableRetryToken '+tag;ui.set('memory-title',name);ui.set('memory-draft',draft);ui.press('保存');driver.wait(lambda:any('保存失败' in label for label in ui.labels()));assert ui.node(key='memory-draft')['value']==draft;ui.capture('management-outage.png')
            ui.press('设置');ui.press('启动服务');driver.wait(lambda:call('health')['state']=='ready',timeout=85);ui.press(project['name']);ui.press('新增记忆');assert ui.node(key='memory-draft')['value']==draft;ui.press('保存')
            def saved():return next((v for v in call('list',list_query={'datasets':[project['id']]})['items'] if v['title']==name),None)
            record=driver.wait(saved);assert record['revision']==1;driver.wait(lambda:indexed(record['memory']));assert len(call('history',memory=record['memory'])['items'])==1
            return {'real_service_stop':True,'failed_save_retains_draft':True,'navigation_retains_failed_draft':True,'resume_and_retry_one_revision':True}
        step('service-outage-draft-retry',outage_draft)
        driver.wait(lambda:call('activities',timezone='Asia/Shanghai')['total']==baseline+3)
        assert current(memory)['revision']==3 and current(memory)['text']==original
        report.update(status='passed',main_memory=memory,project=project['id'],main_title=title,main_original=original,main_edited=edited,home_new_count=baseline+3,main_history_revisions=[3,2,1])
        (root/'management-fixture.json').write_text(json.dumps(report,ensure_ascii=False,indent=2)+'\n')
        ui.quit(stop=True);ui=None
    except Exception as error:
        report['error']=repr(error)
        if ui:
            try:ui.capture('management-failed.png')
            except Exception:pass
        raise
    finally:
        (ROOT/'build/p1/management-verification.json').write_text(json.dumps(report,ensure_ascii=False,indent=2)+'\n')
        if ui and ui.proc.poll() is None:ui.quit(stop=True)
    print(json.dumps(report,ensure_ascii=False,indent=2))
if __name__=='__main__':main()
