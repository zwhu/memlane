#!/usr/bin/env python3
"""A1真实私有IPC范围/工作区/停服错误夹具，明确不调用模型。"""
import hashlib,json,os,socket,uuid
from pathlib import Path
from importlib.machinery import SourceFileLoader
ROOT=Path(__file__).resolve().parents[1];OUT=ROOT/'build/p3-agents';f=Path((OUT/'fixture-path.txt').read_text().strip());app=ROOT/'build/p3-agents-app-r3/darwin-arm64/MemlaneAgents.app'
G=SourceFileLoader('ascopeguard',str(ROOT/'scripts/p3-c2-common.py')).load_module();G.validate_fixture(f,app,'agents');U=SourceFileLoader('ascopeui',str(ROOT/'scripts/p1-ui.py')).load_module();data=f/'client/data';ipc=f'/tmp/memlane-{os.getuid()}-{hashlib.sha256(str(data).encode()).hexdigest()[:12]}/service.sock'
normal=json.loads((OUT/'real-tools-report.json').read_text());assert normal['status']=='passed' and normal['fixture']==str(f);assert json.loads((OUT/'real-app-final.json').read_text())['status']=='passed'
def response(op,**kw):
 with socket.socket(socket.AF_UNIX) as s:
  s.settimeout(8);s.connect(ipc);s.sendall((json.dumps({'protocol':1,'id':str(uuid.uuid4()),'op':op,**kw})+'\n').encode());return json.loads(s.makefile('rb',buffering=0).readline())
def call(op,**kw):
 r=response(op,**kw);assert r['ok'],r;return r.get('result')
ui=None;conn=None;report={'status':'failed','actual_app':True,'model_called':False,'protocol_fault_fixture':True,'fixture':str(f),'cases':[]}
try:
 assert json.loads((data/'service-state.json').read_text())['stopped'];ui=U.UI.__new__(U.UI);ui.__init__(app,f,ROOT/'build/p3-agents-screenshots',extra_env={'PI_CODING_AGENT_DIR':str(f/'pi-agent')});ui.press('设置');U.wait(lambda:'启动服务' in ui.labels());ui.press('启动服务');U.wait(lambda:call('health')['state']=='ready',timeout=95)
 scopes=call('scopes');project=normal['scope'];other=next(s['id'] for s in scopes if s['name']=='A 合成其他项目');global_id=next(s['id'] for s in scopes if s['kind']=='experience')
 conn=call('configure_connection',user_confirmed=True,connection={'agent':'traex','name':'A1 范围协议夹具','project':project,'allowed_scopes':[project]})
 def agent(args,workspace='repo-a',generation=None):return response('agent',agent_request={'protocol_version':1,'connection_id':conn['id'],'generation':conn['generation'] if generation is None else generation,'workspace':str(f/workspace),'usage_channel':'verification','tool':'memory_get','arguments':args})
 allowed=agent({'memory_id':normal['memory'],'scope':project});assert allowed['ok'] and allowed['result']['revision']==1 and allowed['result']['source_detail']
 for name,args,workspace,generation,expected in [('other_scope',{'memory_id':normal['memory'],'scope':other},'repo-a',None,'scope_denied'),('global_unapproved',{'memory_id':normal['memory'],'scope':global_id},'repo-a',None,'scope_denied'),('wrong_workspace',{'memory_id':normal['memory'],'scope':project},'repo-b',None,'workspace_mismatch'),('wrong_generation',{'memory_id':normal['memory'],'scope':project},'repo-a',conn['generation']+1,'reload_required')]:
  r=agent(args,workspace,generation);assert not r['ok'] and r['code']==expected,r;report['cases'].append({'case':name,'code':r['code']})
 call('disconnect',user_confirmed=True,connection_id=conn['id']);r=agent({'memory_id':normal['memory'],'scope':project});assert not r['ok'] and r['code']=='disconnected';report['cases'].append({'case':'revoked','code':r['code']});conn=None
 ui.quit(stop=True);ui=None;assert not Path(ipc).exists();report.update(status='passed',current_version=1,service_stopped=True)
except Exception as e:report.update(status='failed',error=repr(e));raise
finally:
 cleanup=[]
 if conn:
  try:call('disconnect',user_confirmed=True,connection_id=conn['id'])
  except Exception as e:cleanup.append({'step':'revoke_fixture','error':repr(e)})
 if ui:
  try:G.close_partial_ui(ui,f,app,'agents')
  except Exception as e:cleanup.append({'step':'ui','error':repr(e)})
 try:
  G.validate_fixture(f,app,'agents');p=data/'service-state.json';st=json.loads(p.read_text());st['stopped']=True;tmp=p.with_name('.a-scope-stop-'+uuid.uuid4().hex);tmp.write_text(json.dumps(st));os.replace(tmp,p)
  if Path(ipc).exists():
   try:call('stop')
   except OSError:pass
 except Exception as e:cleanup.append({'step':'persistent_stop','error':repr(e)})
 report['cleanup_errors']=cleanup
 if cleanup:report['status']='failed'
 (OUT/'scope-contract.json').write_text(json.dumps(report,ensure_ascii=False,indent=2)+'\n')
print(json.dumps(report,ensure_ascii=False))
if report['status']!='passed':raise SystemExit(1)
