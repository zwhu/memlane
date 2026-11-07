#!/usr/bin/env python3
"""U 真实App/Rust检索通道补验；复用已通过且停服的合成根，不调用模型。"""
import hashlib,json,os,socket,uuid
from pathlib import Path
from importlib.machinery import SourceFileLoader
ROOT=Path(__file__).resolve().parents[1];OUT=ROOT/'build/p3-usage';f=Path((OUT/'fixture-path.txt').read_text().strip());meta=json.loads((f/'source-info.json').read_text());app=Path(meta['app'])
G=SourceFileLoader('usagesearchguard',str(ROOT/'scripts/p3-c2-common.py')).load_module();G.validate_fixture(f,app,phase='usage')
main=json.loads((OUT/'real-app.json').read_text());assert main['status']=='passed' and main['fixture']==str(f.resolve())
U=SourceFileLoader('usagesearchui',str(ROOT/'scripts/p1-ui.py')).load_module();data=f/'client/data';ipc=f'/tmp/memlane-{os.getuid()}-{hashlib.sha256(str(data).encode()).hexdigest()[:12]}/service.sock'
def call(op,**kw):
 with socket.socket(socket.AF_UNIX) as s:
  s.settimeout(8);s.connect(ipc);s.sendall((json.dumps({'protocol':1,'id':str(uuid.uuid4()),'op':op,**kw})+'\n').encode());r=json.loads(s.makefile('rb',buffering=0).readline())
 assert r['ok'],r;return r.get('result')
ui=None;report={'status':'failed','actual_app':True,'actual_rust':True,'model_called':False,'synthetic_only':True}
try:
 ui=U.UI.__new__(U.UI);ui.__init__(app,f,ROOT/'build/p3-usage-screenshots',extra_env={'PI_CODING_AGENT_DIR':str(f/'pi-agent')});ui.press('设置');U.wait(lambda:'启动服务' in ui.labels());ui.press('启动服务');U.wait(lambda:call('health')['state']=='ready',timeout=95)
 memory=main['cases'][0]['memory'];v=call('get',memory=memory);before=call('usage',memory=memory);ui.press('U 合成源项目');ui.set('memory-search',v['text']);ui.press('搜索')
 U.wait(lambda:any(n.get('key')=='memory-'+memory for n in ui.nodes()),timeout=50);after=U.wait(lambda:(lambda u:u if u['app']['returns']>before['app']['returns'] else None)(call('usage',memory=memory)))
 assert after['app']['returns']==before['app']['returns']+1 and after['daily_agent']==before['daily_agent'] and after['connection_verification']==before['connection_verification']
 ui.press_key('memory-'+memory);U.wait(lambda:v['text'] in ui.labels() and '使用记录' in ui.labels());ui.system_capture('usage-app-search-system.png')
 ui.quit(stop=True);ui=None;assert json.loads((data/'service-state.json').read_text())['stopped'];report.update(status='passed',memory=memory,revision=v['revision'],app_return_delta=1,daily_unchanged=True,verification_unchanged=True,service_stopped=True)
except Exception as e:report['error']=repr(e);raise
finally:
 cleanup=[]
 if ui:
  try:ui.quit(stop=True)
  except Exception as e:cleanup.append(repr(e))
 try:
  G.validate_fixture(f,app,phase='usage');path=data/'service-state.json';st=json.loads(path.read_text()) if path.exists() else {};st['stopped']=True;st['stop_generation']=max(1,st.get('stop_generation',0));tmp=path.with_name('.usage-search-stop-'+uuid.uuid4().hex);tmp.write_text(json.dumps(st));os.replace(tmp,path)
  if Path(ipc).exists():
   try:call('stop')
   except OSError:pass
 except Exception as e:cleanup.append(repr(e))
 report['cleanup_errors']=cleanup
 if cleanup:report['status']='failed'
 (OUT/'app-search.json').write_text(json.dumps(report,ensure_ascii=False,indent=2)+'\n')
print(json.dumps(report,ensure_ascii=False))
if report['status']!='passed':raise SystemExit(1)
