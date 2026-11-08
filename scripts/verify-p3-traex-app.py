#!/usr/bin/env python3
"""A1最新实际App/traex/Rust：验证按钮、初始化、活动取消及配置撤销。"""
import hashlib,json,os,socket,subprocess,uuid
from pathlib import Path
from importlib.machinery import SourceFileLoader
ROOT=Path(__file__).resolve().parents[1];OUT=ROOT/'build/p3-agents';f=Path((OUT/'fixture-path.txt').read_text().strip());meta=json.loads((f/'source-info.json').read_text());app=ROOT/'build/p3-agents-app-r3/darwin-arm64/MemlaneAgents.app'
G=SourceFileLoader('aappguard',str(ROOT/'scripts/p3-c2-common.py')).load_module();cfg=G.validate_fixture(f,app,phase='agents')
pre=json.loads((OUT/'preflight-app.json').read_text());assert pre['fixture']==str(f);normal=json.loads((OUT/'real-tools-report.json').read_text());assert normal['status']=='passed' and normal['fixture']==str(f)
U=SourceFileLoader('aappui',str(ROOT/'scripts/p1-ui.py')).load_module();data=f/'client/data';ipc=f'/tmp/memlane-{os.getuid()}-{hashlib.sha256(str(data).encode()).hexdigest()[:12]}/service.sock'
def response(op,**kw):
 with socket.socket(socket.AF_UNIX) as s:
  s.settimeout(8);s.connect(ipc);s.sendall((json.dumps({'protocol':1,'id':str(uuid.uuid4()),'op':op,**kw})+'\n').encode());return json.loads(s.makefile('rb',buffering=0).readline())
def call(op,**kw):
 r=response(op,**kw);assert r['ok'],r;return r.get('result')
def visible(key):return any(n.get('key')==key for n in ui.nodes())
def installation(c):return json.loads((f/'client/agent-adapters'/c['id']/'installation.json').read_text())
def connection():return next(c for c in call('connections') if c['id']==pre['connection']['id'])
def select_connection():
 ui.press('Agent');U.wait(lambda:visible('connection-'+pre['connection']['id']));ui.press_key('connection-'+pre['connection']['id']);U.wait(lambda:visible('verify-agent'))
def verify():
 ui.set('agent-model','GPT-6.1-Sol');ui.press_key('verify-agent');U.wait(lambda:'真实 Agent 读写验证通过。' in ui.labels(),timeout=190);c=connection();assert installation(c)['verified_generation']==c['generation'];return c
ui=None;report={'status':'failed','actual_app':True,'real_agent':'traex','requested_model':'GPT-6.1-Sol','real_rust':True,'model_fixture':False,'synthetic_only':True,'fixture':str(f),'cases':[]}
try:
 assert json.loads((data/'service-state.json').read_text())['stopped'];assert not Path(ipc).exists()
 env={'PI_CODING_AGENT_DIR':str(f/'pi-agent'),'MEMLANE_TRAE_AUTH_DIRECTORY':str(Path.home()/'.trae/cli')}
 ui=U.UI.__new__(U.UI);ui.__init__(app,f,ROOT/'build/p3-agents-screenshots',extra_env=env);ui.press('设置');U.wait(lambda:'启动服务' in ui.labels());ui.press('启动服务');U.wait(lambda:call('health')['state']=='ready',timeout=95)
 select_connection();c=verify();ui.system_capture('traex-real-app-verified-system.png');report['cases'].append({'case':'real_app_verify','connection':c['id'],'generation':c['generation'],'agent_version':installation(c)['agent_version']})
 ui.press_key('agent-initialization');U.wait(lambda:visible('initialization-paths'));ui.set('initialization-paths','README.md\nconventions.md');before={b['batch'] for b in call('initialization_list')};ui.press_key('initialization-prepare')
 batch=U.wait(lambda:next((b for b in call('initialization_list') if b['batch'] not in before),None));U.wait(lambda:'等待Agent提交' in ui.labels());assert batch['saved']==0
 prompt=subprocess.run(['/usr/bin/pbpaste'],capture_output=True,text=True,check=True).stdout;assert batch['batch'] in prompt
 request={'action':'run','connection':c,'options':dict(pre['options'],Bridge=str(app/'Contents/Resources/bin/memlane')),'run':{'Prompt':prompt+'\n隔离验收要求：两份材料各提取一条，共两条，稳定id为a1-pnpm和a1-typescript，引用真实快照精确摘录。','Workspace':str(f/'repo-a'),'TraeAuthHome':str(Path.home()/'.trae/cli'),'Model':'GPT-6.1-Sol','Initialization':True}}
 proc=subprocess.run([str(OUT/'p2-agent-check')],input=json.dumps(request),capture_output=True,text=True,timeout=210);execution=json.loads(proc.stdout);(OUT/'traex-real-initialization.json').write_text(json.dumps(execution,ensure_ascii=False,indent=2)+'\n');assert execution['ok'] and execution['result']['process_group_gone'],execution
 tools={c['tool'] for c in execution['result']['calls']};assert {'memory_initialization_get','memory_initialize'}<=tools and tools<= {'memory_initialization_get','memory_initialization_status','memory_initialize'}
 done=U.wait(lambda:(lambda b:b if b['saved']==2 and b['indexed']==2 else None)(call('initialization_get',initialization_batch=batch['batch'])),timeout=95);U.wait(lambda:'已保存，可检索' in ui.labels());assert not any('此批次仍等待实际提交' in t for t in ui.labels());ui.system_capture('traex-real-initialization-system.png')
 for item in done['receipt']['items']:
  v=call('get',memory=item['memory']);assert v['origin']=='imported' and v['revision']==1 and v['source_detail'];hits=call('search',datasets=[c['project']],query=v['text'],limit=50);assert any(h['version']['memory']==v['memory'] and h['version']['revision']==1 for h in hits)
 report['cases'].append({'case':'real_app_traex_initialization_rust','batch':batch['batch'],'saved':2,'receipt':done['receipt'],'agent_version':execution['result']['version'],'channel':execution['result']['channel'],'selected_model':execution['result']['selected_model']})
 # Same declared scopes still create a new connection generation; old tool callers must reload.
 select_connection();old=connection();ui.press_key('apply-agent-scopes');c=U.wait(lambda:(lambda c:c if c['generation']>old['generation'] else None)(connection()));U.wait(lambda:visible('verify-agent'))
 assert installation(c).get('verified_generation',0)==0;late=response('agent',agent_request={'protocol_version':1,'connection_id':old['id'],'generation':old['generation'],'workspace':str(f/'repo-a'),'tool':'memory_scopes','arguments':{}});assert not late['ok'] and late['code']=='reload_required'
 ui.set('agent-model','GPT-6.1-Sol');ui.press_key('verify-agent')
 marker='memlaneConnectionCheck'+c['id'].replace('-','')+str(c['generation'])
 U.wait(lambda:any(marker in v['text'] for v in call('list',list_query={'datasets':[c['project']],'kind':'raw','limit':100})['items']),timeout=150)
 U.wait(lambda:'取消验证' in ui.labels());ui.press('取消验证');U.wait(lambda:any('验证失败' in t for t in ui.labels()),timeout=30);assert installation(c).get('verified_generation',0)==0 and installation(c)['verification_error'];ui.system_capture('traex-active-verification-cancel-system.png')
 report['cases'].append({'case':'real_app_cancel_after_actual_save','generation':c['generation'],'old_generation_code':late['code'],'verified':False})
 c=verify();ui.system_capture('traex-reloaded-verified-system.png')
 baseline='# SYNTHETIC_USER_OPTION_KEEP\nproject_doc_max_bytes = 8192\n';ui.press_key('disconnect-agent');U.wait(lambda:not connection()['enabled'] and not (f/'client/agent-adapters'/c['id']/'installation.json').exists() and (f/'repo-a/.trae/traecli.toml').read_text()==baseline);U.wait(lambda:'已断开；其他配置与记忆保留。' in ui.labels());assert (f/'repo-a/.trae/traecli.toml').read_text()==baseline
 assert not (f/'client/agent-adapters'/c['id']/'installation.json').exists();report['cases'].append({'case':'app_disconnect_preserve_unrelated_config','connection':c['id']})
 ui.quit(stop=True);ui=None;report.update(status='passed',service_stopped=True,config_preserved=True)
except Exception as e:report.update(status='failed',error=repr(e));raise
finally:
 cleanup=[]
 if ui:
  try:ui.capture('a1-failure.png')
  except Exception as e:cleanup.append({'step':'failure_capture','error':repr(e)})
  try:G.close_partial_ui(ui,f,app,'agents')
  except Exception as e:cleanup.append({'step':'ui','error':repr(e)})
 try:
  G.validate_fixture(f,app,phase='agents');p=data/'service-state.json';st=json.loads(p.read_text());st['stopped']=True;tmp=p.with_name('.a-app-stop-'+uuid.uuid4().hex);tmp.write_text(json.dumps(st));os.replace(tmp,p)
  if Path(ipc).exists():
   try:call('stop')
   except OSError:pass
 except Exception as e:cleanup.append({'step':'persistent_stop','error':repr(e)})
 report['cleanup_errors']=cleanup
 if cleanup:report['status']='failed'
 (OUT/'real-app.json').write_text(json.dumps(report,ensure_ascii=False,indent=2)+'\n')
print(json.dumps(report,ensure_ascii=False))
if report['status']!='passed':raise SystemExit(1)
