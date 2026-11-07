#!/usr/bin/env python3
"""I 索引故障实际App/Rust验收；初始化提交明确为无模型协议夹具。"""
import hashlib,json,os,socket,subprocess,time,uuid
from pathlib import Path
from importlib.machinery import SourceFileLoader
ROOT=Path(__file__).resolve().parents[1];OUT=ROOT/'build/p3-onboarding'
f=Path((OUT/'fixture-path.txt').read_text().strip());meta=json.loads((f/'source-info.json').read_text());app=Path(meta['app'])
G=SourceFileLoader('ifguard',str(ROOT/'scripts/p3-c2-common.py')).load_module();G.validate_fixture(f,app,phase='onboarding')
assert json.loads((OUT/'real-app.json').read_text())['status']=='passed','real normal App/model required first'
U=SourceFileLoader('ifui',str(ROOT/'scripts/p1-ui.py')).load_module();data=f/'client/data'
ipc=f'/tmp/memlane-{os.getuid()}-{hashlib.sha256(str(data).encode()).hexdigest()[:12]}/service.sock'
def call(op,**kw):
 with socket.socket(socket.AF_UNIX) as s:
  s.settimeout(8);s.connect(ipc);s.sendall((json.dumps({'protocol':1,'id':str(uuid.uuid4()),'op':op,**kw})+'\n').encode());r=json.loads(s.makefile('rb',buffering=0).readline());assert r['ok'],r;return r.get('result')
def visible(key):return any(n.get('key')==key for n in ui.nodes())
def open_ready():
 global ui
 ui=U.UI.__new__(U.UI);ui.__init__(app,f,ROOT/'build/p3-onboarding-screenshots',extra_env={'PI_CODING_AGENT_DIR':str(f/'pi-agent')})
 ui.press('设置');U.wait(lambda:'启动服务' in ui.labels());ui.press('启动服务');U.wait(lambda:call('health')['state']=='ready',timeout=95)
def stopped():
 G.validate_fixture(f,app,phase='onboarding');assert json.loads((data/'service-state.json').read_text())['stopped'];assert not Path(ipc).exists()
ui=None;blocked=None;conn=None
report={'status':'failed','fixture':str(f),'identifier':meta['identifier'],'actual_app':True,'real_rust':True,'synthetic_only':True,'fault_fixture':'Rust dataset path blocked by regular file','model_fixture':False,'no_model_protocol_import':True}
try:
 stopped();service=json.loads((f/'client/service.json').read_text());worker=Path(service['worker']['DataDir']);G.ensure_inside(worker,f)
 open_ready();project=next(s['id'] for s in call('scopes') if s['name']=='I 合成其他项目');ui.quit(stop=True);ui=None;stopped()
 blocked=worker/project;assert not blocked.exists();blocked.write_text('I explicit Rust IO fault fixture\n')
 source=f/'repo-b/README.md';source.write_text('合成故障项目使用明确类型并保留来源。\n');open_ready()
 conn=call('configure_connection',user_confirmed=True,connection={'agent':'pi','name':'I 索引故障协议夹具','project':project,'allowed_scopes':[project]})
 batch=call('initialization_start',user_confirmed=True,initialization_start={'operation_id':'i-index-fault-batch','project':project,'connection_id':conn['id'],'generation':conn['generation'],'workspace':str(f/'repo-b'),'paths':['README.md']})
 def agent(tool,args):return call('agent',agent_request={'protocol_version':1,'connection_id':conn['id'],'generation':conn['generation'],'workspace':str(f/'repo-b'),'tool_mode':'initialization','tool':tool,'arguments':args})
 snapshot=agent('memory_initialization_get',{'batch_id':batch['batch'],'scope':project});src=snapshot['sources'][0]
 receipt=agent('memory_initialize',{'batch_id':batch['batch'],'scope':project,'operation_id':'i-index-fault-import','items':[{'id':'index-fault-policy','title':'合成索引故障约定','text':src['text'],'sources':[{'path':src['path'],'sha256':src['sha256'],'quote':src['text']}]}]})
 memory=receipt['items'][0]['memory'];U.wait(lambda:call('initialization_get',initialization_batch=batch['batch'])['index_failed']==1,timeout=95)
 ui.press_key('guidance-restore');ui.press_key('guidance-initialization');U.wait(lambda:visible('initialization-batch-'+batch['batch']));ui.press_key('initialization-batch-'+batch['batch']);U.wait(lambda:'已保存，部分索引失败' in ui.labels());ui.system_capture('initialization-index-failed-system.png')
 assert call('initialization_get',initialization_batch=batch['batch'])['receipt']==receipt;ui.press_key('initialization-memory-'+memory);U.wait(lambda:src['text'] in ui.labels());assert call('get',memory=memory)['index_state']=='failed';ui.system_capture('initialization-index-failed-memory-system.png')
 ui.quit(stop=True);ui=None;stopped();assert blocked.read_text()=='I explicit Rust IO fault fixture\n';blocked.unlink();blocked=None
 open_ready();U.wait(lambda:call('get',memory=memory)['index_state']=='indexed',timeout=95);hits=call('search',datasets=[project],query=src['text'],limit=50);assert any(h['version']['memory']==memory and h['version']['revision']==1 for h in hits)
 ui.press_key('guidance-restore');ui.press_key('guidance-initialization');U.wait(lambda:visible('initialization-batch-'+batch['batch']));ui.press_key('initialization-batch-'+batch['batch']);U.wait(lambda:'已保存，可检索' in ui.labels());ui.system_capture('initialization-index-recovered-system.png')
 assert call('initialization_get',initialization_batch=batch['batch'])['receipt']==receipt
 call('disconnect',user_confirmed=True,connection_id=conn['id']);assert not next(c for c in call('connections') if c['id']==conn['id'])['enabled'];conn=None;ui.quit(stop=True);ui=None
 report.update(status='passed',batch=batch['batch'],memory=memory,receipt_preserved=True,rust_current_recovered=True,service_stopped=True)
except Exception as e:report['error']=repr(e);raise
finally:
 cleanup=[]
 if conn:
  try:call('disconnect',user_confirmed=True,connection_id=conn['id'])
  except Exception as e:cleanup.append({'step':'connection_disable','error':repr(e)})
 if ui:
  try:G.close_partial_ui(ui,f,app,'onboarding')
  except Exception as e:cleanup.append({'step':'ui','error':repr(e)})
 try:
  G.validate_fixture(f,app,phase='onboarding');path=data/'service-state.json';st=json.loads(path.read_text());st['stopped']=True;tmp=path.with_name('.i-fault-stop-'+uuid.uuid4().hex);tmp.write_text(json.dumps(st));os.replace(tmp,path)
  if Path(ipc).exists():
   try:call('stop')
   except OSError:pass
  if blocked and blocked.exists():
   assert blocked.read_text()=='I explicit Rust IO fault fixture\n';blocked.unlink()
 except Exception as e:cleanup.append({'step':'fault_cleanup','error':repr(e)})
 report['cleanup_errors']=cleanup
 if cleanup:report['status']='failed'
 (OUT/'index-fault-app.json').write_text(json.dumps(report,ensure_ascii=False,indent=2)+'\n')
print(json.dumps(report,ensure_ascii=False))
if report['status']!='passed':raise SystemExit(1)
