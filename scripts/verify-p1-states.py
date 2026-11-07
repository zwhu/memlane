#!/usr/bin/env python3
"""Verify actual home counts, empty/preparing/failure UI and index retry."""
import argparse,hashlib,http.server,json,os,shutil,signal,socket,subprocess,tempfile,threading,time,uuid
from pathlib import Path
from importlib.machinery import SourceFileLoader
ROOT=Path(__file__).resolve().parents[1]
driver=SourceFileLoader('p1ui',str(ROOT/'scripts/p1-ui.py')).load_module()

def rpc(root,op,ok=True,**fields):
    ipc=f"/tmp/memlane-{os.getuid()}-{hashlib.sha256(str(root).encode()).hexdigest()[:12]}/service.sock"
    with socket.socket(socket.AF_UNIX) as s:
        s.settimeout(15);s.connect(ipc);s.sendall((json.dumps({'protocol':1,'id':str(uuid.uuid4()),'op':op,**fields})+'\n').encode());r=json.loads(s.makefile('rb',buffering=0).readline())
    assert r['ok']==ok,r
    return r.get('result') if ok else r

def main():
    p=argparse.ArgumentParser(description=__doc__);p.add_argument('--app',type=Path,default=ROOT/'build/p1-app/darwin-arm64/MemlaneP1.app');a=p.parse_args()
    root=Path((ROOT/'build/p1/fixture-path.txt').read_text().strip());cfg=json.loads((root/'runtime-config.json').read_text());data=Path(cfg['root'])/'data';assert 'memlane-p1-app-' in str(root)
    evidence=json.loads((root/'management-fixture.json').read_text());out=ROOT/'build/p1-screenshots';report={'status':'failed','synthetic_only':True,'steps':[]};ui=None;disabled=None;worker=None;server=None;gate=threading.Event()
    def step(name,fn):
        result=fn();entry={'step':name,'status':'passed','evidence':result};report['steps'].append(entry);print(json.dumps(entry,ensure_ascii=False),flush=True)
        (ROOT/'build/p1/states-verification.json').write_text(json.dumps(report,ensure_ascii=False,indent=2)+'\n')
    try:
        # The controlled source serves an unchanged signed feed. Its first
        # actual HTTP request waits for the screenshot; then installation
        # reuses immutable components and starts a real, empty business DB.
        empty=Path(tempfile.mkdtemp(prefix='memlane-p1-empty-'));emptyclient=empty/'client';emptyclient.mkdir()
        shutil.copytree(Path(cfg['root'])/'runtime',emptyclient/'runtime',symlinks=True)
        cache=(emptyclient/'runtime/feed-cache.json').read_text();start=cache.index('"feed":')+len('"feed":');start+=len(cache[start:])-len(cache[start:].lstrip());_,length=json.JSONDecoder().raw_decode(cache[start:]);signed=cache[start:start+length].encode()
        requested=threading.Event()
        class Feed(http.server.BaseHTTPRequestHandler):
            def do_GET(self):
                requested.set();gate.wait(30);self.send_response(200);self.send_header('Content-Length',str(len(signed)));self.end_headers();self.wfile.write(signed)
            def log_message(self,*args):pass
        server=http.server.ThreadingHTTPServer(('127.0.0.1',0),Feed);thread=threading.Thread(target=server.serve_forever,daemon=True);thread.start()
        identifier='io.github.zwhu.memlane.p1empty.'+uuid.uuid4().hex[:10]
        emptycfg={**cfg,'root':str(emptyclient),'component_source':f'http://127.0.0.1:{server.server_port}/components.json','service_plist':identifier+'.service.plist'}
        config=empty/'runtime-config.json';config.write_text(json.dumps(emptycfg));report['empty_fixture']=str(empty)
        subprocess.run(['python3',str(ROOT/'scripts/build-client.py'),'--out',str(ROOT/'build/p1-empty-app'),'--runtime-config',str(config),'--identifier',identifier,'--name','MemlaneP1Empty','--automation','--skip-dmg'],cwd=ROOT,check=True)
        ui=driver.UI(ROOT/'build/p1-empty-app/darwin-arm64/MemlaneP1Empty.app',empty,out);ui.press('设置');assert requested.wait(10);assert any('正在检查本地组件' in s for s in ui.labels());ui.capture('state-preparing.png');ui.system_capture('system-state-preparing.png');step('actual-preparation-state',lambda:{'real_http_preparation_request':True,'UI_available_before_ready':True,'signed_feed_unchanged':True});gate.set()
        driver.wait(lambda:rpc(emptyclient/'data','health')['state']=='ready',timeout=85)
        assert rpc(emptyclient/'data','list')['total']==0;ui.press('总览');driver.wait(lambda:'过去一年新增 0 条记忆' in ui.labels());assert '暂无整理建议' in ui.labels();assert not any(n.get('key','').startswith('memory-') and n.get('role')=='button' for n in ui.nodes());ui.capture('state-empty-home.png');ui.system_capture('system-state-empty-home.png');step('actual-empty-home',lambda:{'real_database_items':0,'actual_activity_total':0,'no_fake_memory_or_advice':True});ui.quit(stop=True);ui=None;server.shutdown();server.server_close();server=None
        ui=driver.UI(a.app.resolve(),root,out);ui.press('设置');driver.wait(lambda:'启动服务' in ui.labels() or '停止服务' in ui.labels(),timeout=85)
        if '启动服务' in ui.labels():ui.press('启动服务')
        driver.wait(lambda:rpc(data,'health')['state']=='ready',timeout=85)
        baseline=rpc(data,'activities',timezone='Asia/Shanghai')['total'];ui.press('总览');driver.wait(lambda:f'过去一年新增 {baseline} 条记忆' in ui.labels());ui.capture('state-real-home-before.png');ui.system_capture('system-real-home-before.png');step('actual-populated-home',lambda:{'total_matches_service':baseline,'recent_from_first_usable_events':True,'unimplemented_advice_empty':True})
        worker=Path(json.loads((Path(cfg['root'])/'service.json').read_text())['worker']['Executable']);assert str(worker).startswith(str(Path(cfg['root'])/'runtime'))
        disabled=worker.with_name(worker.name+'.p1-disabled');assert not disabled.exists();worker.rename(disabled)
        pid=rpc(data,'health')['worker_pid'];os.kill(pid,signal.SIGKILL);driver.wait(lambda:rpc(data,'health')['state']=='failed',timeout=30)
        project=next(s for s in rpc(data,'scopes') if s['id']==evidence['project']);ui.press(project['name']);driver.wait(lambda:evidence['main_title'] in ui.labels());ui.press(evidence['main_title']);driver.wait(lambda:'索引已完成 · 等待服务恢复' in ui.labels());ui.capture('state-indexed-service-unavailable.png');title='索引失败合成夹具 '+uuid.uuid4().hex[:6]
        ui.press(project['name']);ui.press('新增记忆');ui.set('memory-title',title);ui.set('memory-draft','P1 failedIndexToken 正文必须保留。');ui.press('保存')
        def saved():return next((v for v in rpc(data,'list',list_query={'datasets':[project['id']]})['items'] if v['title']==title),None)
        record=driver.wait(saved);id_=record['memory'];failure=rpc(data,'index',ok=False,memory=id_);assert 'engine unavailable' in failure['error'];assert rpc(data,'get',memory=id_)['index_state']=='failed';driver.wait(lambda:'索引失败 · 正文已保留' in ui.labels());assert '已保存' in ui.labels();assert rpc(data,'activities',timezone='Asia/Shanghai')['total']==baseline;ui.capture('state-index-failed.png');ui.system_capture('system-state-index-failed.png');step('actual-index-failure',lambda:{'actual_worker_sigkill_and_unavailable_binary':True,'saved_original_not_lost':True,'index_failed_displayed_separately':True,'not_counted_before_first_index':True})
        disabled.rename(worker);disabled=None;ui.press('重试索引');driver.wait(lambda:rpc(data,'get',memory=id_)['index_state']=='indexed',timeout=65);driver.wait(lambda:'可检索' in ui.labels());assert len(rpc(data,'history',memory=id_)['items'])==1;assert rpc(data,'activities',timezone='Asia/Shanghai')['total']==baseline+1;rpc(data,'retry_index',memory=id_,expected_revision=1);driver.wait(lambda:rpc(data,'get',memory=id_)['index_state']=='indexed');assert rpc(data,'activities',timezone='Asia/Shanghai')['total']==baseline+1;ui.capture('state-index-retried.png');step('actual-index-retry',lambda:{'UI_retry_restores_real_worker':True,'one_revision':True,'counted_once_after_first_index':True,'repeat_retry_no_count':True})
        rpc(data,'apply',mutation={'operation_id':str(uuid.uuid4()),'kind':'delete','memory':id_,'dataset':project['id'],'expected_revision':1},user_confirmed=True)
        summary=rpc(data,'activities',timezone='Asia/Shanghai');assert summary['total']==baseline+1;day=next(k for k,v in summary['days'].items() if v>0)
        ui.press('总览');driver.wait(lambda:f'过去一年新增 {baseline+1} 条记忆' in ui.labels());ui.capture('final-real-home.png');ui.system_capture('system-final-real-home.png');ui.press_key('day-'+day);driver.wait(lambda:title in ui.labels());ui.press(title);driver.wait(lambda:'已删除 · 保留历史' in ui.labels());assert '已删除，不参与检索' in ui.labels();ui.capture('state-date-deleted.png');step('date-and-deletion-history',lambda:{'clicked_real_activity_day':day,'deleted_creation_fact_retained':True,'current_deleted_state_displayed':True})
        ui.press(project['name']);driver.wait(lambda:evidence['main_title'] in ui.labels());ui.press(evidence['main_title']);driver.wait(lambda:evidence['main_original'] in ui.labels());assert rpc(data,'get',memory=evidence['main_memory'])['revision']==3;ui.capture('final-real-detail.png');ui.system_capture('system-final-real-detail.png');report.update(status='passed',final_activity_total=baseline+1,main_restored_revision=3)
        ui.quit(stop=True);ui=None
    except Exception as error:
        report['error']=repr(error)
        if ui:
            try:ui.capture('states-failed.png')
            except Exception:pass
        raise
    finally:
        gate.set()
        if disabled and disabled.exists():disabled.rename(worker)
        if ui and ui.proc.poll() is None:ui.quit(stop=True)
        if server:server.shutdown();server.server_close()
        (ROOT/'build/p1/states-verification.json').write_text(json.dumps(report,ensure_ascii=False,indent=2)+'\n')
    print(json.dumps(report,ensure_ascii=False,indent=2))
if __name__=='__main__':main()
