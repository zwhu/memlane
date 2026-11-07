#!/usr/bin/env python3
"""I 真正 App/pi/Rust 初始化；故障请求单独标注，所有写入限于合成根。"""
import concurrent.futures,hashlib,json,os,signal,socket,subprocess,time,uuid
from pathlib import Path
from importlib.machinery import SourceFileLoader
ROOT=Path(__file__).resolve().parents[1];OUT=ROOT/'build/p3-onboarding'
f=Path((OUT/'fixture-path.txt').read_text().strip());meta=json.loads((f/'source-info.json').read_text());app=Path(meta['app'])
G=SourceFileLoader('iguard',str(ROOT/'scripts/p3-c2-common.py')).load_module();cfg=G.validate_fixture(f,app,phase='onboarding')
U=SourceFileLoader('iui',str(ROOT/'scripts/p1-ui.py')).load_module();data=f/'client/data'
ipc=f'/tmp/memlane-{os.getuid()}-{hashlib.sha256(str(data).encode()).hexdigest()[:12]}/service.sock'
def response(op,**kw):
 with socket.socket(socket.AF_UNIX) as s:
  s.settimeout(8);s.connect(ipc);s.sendall((json.dumps({'protocol':1,'id':str(uuid.uuid4()),'op':op,**kw})+'\n').encode());return json.loads(s.makefile('rb',buffering=0).readline())
def call(op,**kw):
 r=response(op,**kw);assert r['ok'],r;return r.get('result')
def visible(key):return any(n.get('key')==key for n in ui.nodes())
def adapter(action,require=True,**kw):
 p=subprocess.run([str(OUT/'p2-agent-check')],input=json.dumps({'action':action,**kw}),text=True,capture_output=True,timeout=210)
 r=json.loads(p.stdout)
 if require:assert r['ok'],r
 return r['result'] if require else r

def choose(key,label):
 before={n['id'] for n in ui.nodes()};ui.press_key(key)
 def option():
  nodes=ui.nodes();popup={n['id'] for n in nodes if n.get('role')=='popup' and n['id'] not in before};return next((n for n in nodes if n.get('label')==label and n.get('parent') in popup),None)
 n=U.wait(option);b=n['bounds'];w=ui.req('windows')['windows'][0]['id'];ui.req('input',window=w,events=[{'type':'click','x':b['x']+b['width']/2,'y':b['y']+b['height']/2}]);ui.req('idle',timeout=1500)
def batches():return call('initialization_list') or []
def get(b):return call('initialization_get',initialization_batch=b)
def prepare():
 before={b['batch'] for b in batches()};ui.press_key('initialization-prepare');b=U.wait(lambda:next((b for b in batches() if b['batch'] not in before),None));U.wait(lambda:'等待Agent提交' in ui.labels());return b

def agent(tool,args,**extra):
 return response('agent',agent_request={'protocol_version':1,'connection_id':conn['id'],'generation':conn['generation'],'workspace':str(f/'repo-a'),'tool_mode':'initialization','tool':tool,'arguments':args,**extra})
def run_batch(b,extra=''):
 ui.press_key('initialization-copy-existing');prompt=subprocess.run(['/usr/bin/pbpaste'],capture_output=True,text=True,check=True).stdout
 assert b['batch'] in prompt and 'memory_initialize' in prompt
 return adapter('run',require=False,connection=conn,options=options,run={'Prompt':prompt+'\n隔离验收要求：每个批准文件提取一条知识，共两条；id分别为pnpm-policy和typescript-policy，引用对应文件精确原文。'+extra,'Workspace':str(f/'repo-a'),'PiAuthDirectory':str(Path.home()/'.pi/agent'),'Provider':'deepseek','Model':'deepseek-v4-pro','Initialization':True})

ui=None;options=None;conn=None
report={'status':'failed','actual_app':True,'real_agent':'pi','real_rust':True,'model_fixture':False,'synthetic_only':True,'fixture':str(f),'cases':[]}
try:
 found=next(x for x in adapter('discover',home=str(Path.home())) if x['agent']=='pi');assert found.get('path') and not found.get('error')
 (f/'client/agent-paths.json').write_text(json.dumps({'pi':found['path']}))
 settings=f/'pi-agent/settings.json';settings.write_text(json.dumps({'synthetic_user_option':'preserve','defaultProvider':'deepseek','defaultModel':'deepseek-v4-pro'}))
 env={'PI_CODING_AGENT_DIR':str(f/'pi-agent'),'MEMLANE_PI_AUTH_DIRECTORY':str(Path.home()/'.pi/agent')}
 ui=U.UI.__new__(U.UI);ui.__init__(app,f,ROOT/'build/p3-onboarding-screenshots',extra_env=env)
 U.wait(lambda:call('health')['state']=='ready',timeout=95);assert call('list',list_query={'limit':10})['total']==0,'fresh root required'
 U.wait(lambda:visible('guidance-open'));ui.system_capture('home-guide-system.png');ui.press_key('guidance-skip');U.wait(lambda:not visible('guidance-open'))
 assert json.loads((f/'client/guidance.json').read_text())['hidden'];ui.press('设置');ui.press_key('guidance-restore');U.wait(lambda:'已准备，Rust记忆服务可用' in ui.labels());ui.system_capture('guide-real-state-system.png')
 for name,repo in [('I 合成源项目','repo-a'),('I 合成其他项目','repo-b')]:
  ui.press('项目');ui.set('project-name',name);ui.set('project-path',str(f/repo));ui.press_key('register-project');U.wait(lambda:any(s['name']==name for s in call('scopes')))
 project=next(s['id'] for s in call('scopes') if s['name']=='I 合成源项目');other=next(s['id'] for s in call('scopes') if s['name']=='I 合成其他项目')
 ui.press('Agent');U.wait(lambda:visible('agent-pi'));ui.press_key('agent-pi');choose('agent-project','I 合成源项目');ui.press_key('connect-agent')
 conn=U.wait(lambda:next((c for c in call('connections') if c['agent']=='pi'),None));U.wait(lambda:visible('verify-agent'))
 options={'ClientRoot':cfg['root'],'ServiceRoot':str(data),'Bridge':str(app/'Contents/Resources/bin/memlane'),'AgentPath':found['path'],'Workspace':str(f/'repo-a'),'PiDirectory':str(f/'pi-agent')}
 ui.set('agent-provider','deepseek');ui.set('agent-model','deepseek-v4-pro');ui.press_key('verify-agent');U.wait(lambda:'真实 Agent 读写验证通过。' in ui.labels(),timeout=190)
 report['cases'].append({'case':'skip_restore_and_real_app_pi_connection','agent_version':found['version'],'connection':conn['id'],'actual_receipt':True})
 ui.press_key('agent-initialization');U.wait(lambda:visible('initialization-paths'));choose('initialization-project','I 合成源项目')
 ui.set('initialization-paths','README.md\nconventions.md');count=call('list',list_query={'datasets':[project],'kind':'knowledge'})['total'];b=prepare()
 assert get(b['batch'])['saved']==0 and call('list',list_query={'datasets':[project],'kind':'knowledge'})['total']==count
 ui.press_key('initialization-show-prompt');ui.system_capture('initialization-pending-system.png')
 execution=run_batch(b);(OUT/'pi-real-initialization.json').write_text(json.dumps(execution,ensure_ascii=False,indent=2)+'\n');assert execution['ok'],execution
 calls=execution['result']['calls'];imports=[c for c in calls if c['tool']=='memory_initialize' and c['response']['ok']];assert imports and any(c['tool']=='memory_initialization_get' and c['response']['ok'] for c in calls)
 assert all(c['tool'] in ('memory_initialization_get','memory_initialization_status','memory_initialize') for c in calls)
 done=U.wait(lambda:(lambda b:b if b['saved']==2 and b['indexed']==2 else None)(get(b['batch'])),timeout=95);receipt=done['receipt'];U.wait(lambda:'已保存，可检索' in ui.labels() and not any('此批次仍等待实际提交' in t for t in ui.labels()));ui.system_capture('initialization-received-system.png')
 for item in receipt['items']:
  v=call('get',memory=item['memory']);assert v['origin']=='imported' and v['revision']==1 and v.get('source_detail')
  hits=call('search',datasets=[project],query=v['text'],limit=50);assert any(h['version']['memory']==v['memory'] and h['version']['revision']==1 for h in hits)
  report['cases'].append({'case':'real_pi_import_rust_current','stable_id':item['id'],'memory':v['memory'],'origin':v['origin'],'source':v['source_detail']})
 item=receipt['items'][0];ui.press_key('initialization-memory-'+item['memory']);U.wait(lambda:call('get',memory=item['memory'])['text'] in ui.labels() and 'v1 · 导入' in ui.labels());ui.system_capture('initialization-current-memory-system.png')
 # Exact receipt replay is a no-model protocol request after source mutation.
 submit=imports[0]['arguments'];original=(f/'repo-a/README.md').read_text();(f/'repo-a/README.md').write_text(original+'合成验收后修改，不改变已保存来源。\n')
 replay=agent('memory_initialize',submit);assert replay['ok'] and replay['result']==receipt,replay
 (f/'repo-a/README.md').write_text(original);report['cases'].append({'case':'no_model_receipt_replay_after_source_change','same_receipt':True})
 # Real model cancellation: get approved source, then deliberately ask actual status tools before submit.
 ui.press('设置');ui.press_key('guidance-restore');ui.press_key('guidance-initialization');U.wait(lambda:visible('initialization-new'));ui.press_key('initialization-new');U.wait(lambda:visible('initialization-paths'))
 cancelled=prepare()
 with concurrent.futures.ThreadPoolExecutor(max_workers=1) as pool:
  future=pool.submit(run_batch,cancelled,'先读取材料，然后连续调用memory_initialization_status至少8次，确认最后状态仍pending才提交。如果返回cancelled，停止并报告取消；不得调用memory_initialize。')
  U.wait(lambda:get(cancelled['batch']).get('source_read_ms',0)>0,timeout=140);ui.press_key('initialization-cancel');U.wait(lambda:'已取消接收' in ui.labels());cancel_result=future.result(timeout=200)
 (OUT/'pi-real-cancel.json').write_text(json.dumps(cancel_result,ensure_ascii=False,indent=2)+'\n');assert get(cancelled['batch'])['state']=='cancelled' and get(cancelled['batch'])['saved']==0
 assert any(c['tool']=='memory_initialization_get' for c in cancel_result.get('result',{}).get('calls',[]));ui.system_capture('initialization-cancel-system.png')
 report['cases'].append({'case':'real_pi_app_cancel_after_source_read','batch':cancelled['batch'],'saved':0})
 # Source expiration shown in App; protocol fixture does not invoke a model.
 ui.press_key('initialization-new');expired=prepare();(f/'repo-a/README.md').write_text(original+'合成过期变更。\n')
 expire_args=dict(submit,batch_id=expired['batch'],operation_id='expire-init-check');denied=agent('memory_initialize',expire_args);assert not denied['ok'],denied
 U.wait(lambda:get(expired['batch'])['state']=='expired');U.wait(lambda:'材料已变化，批次过期' in ui.labels());ui.system_capture('initialization-expired-system.png');(f/'repo-a/README.md').write_text(original)
 report['cases'].append({'case':'no_model_source_expiration_app','batch':expired['batch'],'error':denied['code']})
 # New batch cannot repeat stable IDs; malformed quotes and wrong scope have no side effects.
 ui.press_key('initialization-new');duplicate=prepare();dup_args=dict(submit,batch_id=duplicate['batch'],operation_id='duplicate-init-check')
 before=call('list',list_query={'datasets':[project],'kind':'knowledge'})['total'];denied=agent('memory_initialize',dup_args);assert not denied['ok'] and get(duplicate['batch'])['saved']==0 and call('list',list_query={'datasets':[project],'kind':'knowledge'})['total']==before
 cross=agent('memory_initialization_get',{'batch_id':duplicate['batch'],'scope':other});assert not cross['ok'] and cross['code']=='scope_denied'
 malformed=json.loads(json.dumps(dup_args));malformed['operation_id']='bad-quote-check';malformed['items'][0]['id']='bad-quote';malformed['items'][0]['sources'][0]['quote']='UNAPPROVED QUOTE';bad=agent('memory_initialize',malformed);assert not bad['ok'] and get(duplicate['batch'])['saved']==0
 report['cases'].append({'case':'no_model_duplicate_scope_quote_atomic_rejection','duplicate_code':denied['code'],'scope_code':cross['code'],'quote_code':bad['code']})
 ui.press_key('initialization-cancel');U.wait(lambda:get(duplicate['batch'])['state']=='cancelled')
 ui.quit(stop=True);ui=None
 ui=U.UI.__new__(U.UI);ui.__init__(app,f,ROOT/'build/p3-onboarding-screenshots',extra_env=env);ui.press('设置');U.wait(lambda:'启动服务' in ui.labels());ui.press('启动服务');U.wait(lambda:call('health')['state']=='ready',timeout=95)
 ui.press_key('guidance-restore');ui.press_key('guidance-initialization');U.wait(lambda:visible('initialization-batch-'+b['batch']));ui.press_key('initialization-batch-'+b['batch']);U.wait(lambda:'已保存，可检索' in ui.labels());assert get(b['batch'])['receipt']==receipt and get(cancelled['batch'])['state']=='cancelled';ui.system_capture('initialization-restart-system.png')
 ui.press('Agent');ui.press_key('connection-'+conn['id']);U.wait(lambda:visible('disconnect-agent'));ui.press_key('disconnect-agent');U.wait(lambda:not next(c for c in call('connections') if c['id']==conn['id'])['enabled']);assert json.loads(settings.read_text())['synthetic_user_option']=='preserve'
 ui.quit(stop=True);ui=None;report.update(status='passed',service_stopped=True,config_preserved=True,model_fixture=False)
except Exception as e:report['error']=repr(e);raise
finally:
 cleanup=[]
 if conn and options:
  try:adapter('uninstall',connection=conn,options=options)
  except Exception as e:cleanup.append({'step':'uninstall','error':repr(e)})
 if ui:
  try:ui.capture('failure.png')
  except Exception as e:cleanup.append({'step':'failure_capture','error':repr(e)})
  try:G.close_partial_ui(ui,f,app,'onboarding')
  except Exception as e:cleanup.append({'step':'ui','error':repr(e)})
 try:
  G.validate_fixture(f,app,phase='onboarding');path=data/'service-state.json';st=json.loads(path.read_text()) if path.exists() else {};st['stopped']=True;st['stop_generation']=max(1,st.get('stop_generation',0));st['last_stopped_ms']=int(time.time()*1000);temp=path.with_name('.onboarding-stop-'+uuid.uuid4().hex);temp.write_text(json.dumps(st));os.replace(temp,path)
  if Path(ipc).exists():
   try:response('stop')
   except OSError:pass
 except Exception as e:cleanup.append({'step':'persistent_stop','error':repr(e)})
 report['cleanup_errors']=cleanup
 if cleanup:report['status']='failed'
 (OUT/'real-app.json').write_text(json.dumps(report,ensure_ascii=False,indent=2)+'\n')
print(json.dumps(report,ensure_ascii=False))
if report['status']!='passed':raise SystemExit(1)
