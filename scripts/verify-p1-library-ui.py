#!/usr/bin/env python3
"""Read actual service records and register a project in the isolated P1 App."""
import argparse,hashlib,json,os,socket,uuid
from pathlib import Path
from importlib.machinery import SourceFileLoader
ROOT=Path(__file__).resolve().parents[1]
driver=SourceFileLoader('p1ui',str(ROOT/'scripts/p1-ui.py')).load_module()

def main():
    p=argparse.ArgumentParser(description=__doc__);p.add_argument('--app',type=Path,default=ROOT/'build/p1-app/darwin-arm64/MemlaneP1.app');a=p.parse_args()
    root=Path((ROOT/'build/p1/fixture-path.txt').read_text().strip());cfg=json.loads((root/'runtime-config.json').read_text());data=Path(cfg['root'])/'data'
    assert 'memlane-p1-app-' in str(root)
    ipc=f"/tmp/memlane-{os.getuid()}-{hashlib.sha256(str(data).encode()).hexdigest()[:12]}/service.sock"
    def call(op,**fields):
        with socket.socket(socket.AF_UNIX) as s:
            s.settimeout(10);s.connect(ipc);s.sendall((json.dumps({'protocol':1,'id':str(uuid.uuid4()),'op':op,**fields})+'\n').encode());r=json.loads(s.makefile('rb',buffering=0).readline())
        assert r['ok'],r;return r.get('result')
    out=ROOT/'build/p1-screenshots';report={'status':'failed','synthetic_only':True};ui=None
    try:
        ui=driver.UI(a.app.resolve(),root,out)
        ui.press('设置')
        driver.wait(lambda:'启动服务' in ui.labels() or '停止服务' in ui.labels(),timeout=85)
        if '启动服务' in ui.labels():ui.press('启动服务')
        driver.wait(lambda:call('health')['state']=='ready',timeout=85)
        ui.press('项目');ui.set('project-name','P1 本地合成项目');ui.set('project-path',str(root/'repo-a'));ui.press_key('register-project')
        driver.wait(lambda:any(s['name']=='P1 本地合成项目' for s in call('scopes')))
        project=next(s for s in call('scopes') if s['name']=='P1 本地合成项目')
        fixturepath=root/'library-ui-fixture.json'
        if fixturepath.exists():memory=json.loads(fixturepath.read_text())['memory']
        else:
            memory=str(uuid.uuid4());fixturepath.write_text(json.dumps({'memory':memory,'dataset':project['id']}))
            save={'operation_id':str(uuid.uuid4()),'kind':'save','dataset':project['id'],'memory':memory,'expected_revision':0,'text':'P1 actualOriginalSourceToken 原始来源正文','title':'P1 真实服务读取','source':'本轮合成 App 来源'}
            call('apply',mutation=save,user_confirmed=True)
            call('apply',mutation={**save,'operation_id':str(uuid.uuid4()),'kind':'edit','expected_revision':1,'text':'P1 currentReadToken 最新正文'},user_confirmed=True)
        driver.wait(lambda:call('get',memory=memory)['index_state']=='indexed',timeout=45)
        ui.press('全部记忆');driver.wait(lambda:'P1 真实服务读取' in ui.labels());ui.press('P1 真实服务读取')
        driver.wait(lambda:'P1 currentReadToken 最新正文' in ui.labels())
        assert '本轮合成 App 来源' in ui.labels();assert 'P1 actualOriginalSourceToken 原始来源正文' in ui.labels();assert '可检索' in ui.labels()
        ui.capture('real-library-detail.png');ui.system_capture('system-real-library-detail.png')
        ui.press('历史记录');driver.wait(lambda:'P1 actualOriginalSourceToken 原始来源正文' in ui.labels());assert '恢复 v1' in ui.labels();ui.capture('real-library-history.png')
        ui.press('全局记忆');driver.wait(lambda:not any(n.get('key')=='memory-'+memory for n in ui.nodes()))
        assert 'P1 真实服务读取' not in [n.get('label') for n in ui.nodes() if n.get('role')=='button']
        report.update(status='passed',project_registered_in_app=True,true_service_list_detail_source_index=True,history_versions_read=True,global_list_excludes_project=True)
        ui.quit(stop=True);ui=None
    except Exception as error:
        report['error']=repr(error)
        if ui:
            try:ui.capture('real-library-failed.png')
            except Exception:pass
        raise
    finally:
        (ROOT/'build/p1/library-ui-verification.json').write_text(json.dumps(report,ensure_ascii=False,indent=2)+'\n')
        if ui and ui.proc.poll() is None:ui.quit(stop=True)
    print(json.dumps(report,ensure_ascii=False,indent=2))
if __name__=='__main__':main()
