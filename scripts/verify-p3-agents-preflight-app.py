#!/usr/bin/env python3
"""A1实际App配置/实际CLI prompt-input检查；本阶段不调用模型。"""
import hashlib,json,os,socket,subprocess,time,uuid
from pathlib import Path
from importlib.machinery import SourceFileLoader
ROOT=Path(__file__).resolve().parents[1];OUT=ROOT/'build/p3-agents'
f=Path((OUT/'fixture-path.txt').read_text().strip());meta=json.loads((f/'source-info.json').read_text());app=Path(meta['app'])
G=SourceFileLoader('agentguard',str(ROOT/'scripts/p3-c2-common.py')).load_module();cfg=G.validate_fixture(f,app,phase='agents')
U=SourceFileLoader('agentui',str(ROOT/'scripts/p1-ui.py')).load_module();data=f/'client/data'
ipc=f'/tmp/memlane-{os.getuid()}-{hashlib.sha256(str(data).encode()).hexdigest()[:12]}/service.sock'
def call(op,**kw):
 with socket.socket(socket.AF_UNIX) as s:
  s.settimeout(8);s.connect(ipc);s.sendall((json.dumps({'protocol':1,'id':str(uuid.uuid4()),'op':op,**kw})+'\n').encode());r=json.loads(s.makefile('rb',buffering=0).readline());assert r['ok'],r;return r.get('result')
def adapter(action,**kw):
 p=subprocess.run([str(OUT/'p2-agent-check')],input=json.dumps({'action':action,**kw}),capture_output=True,text=True,timeout=210);r=json.loads(p.stdout);assert r['ok'],r;return r['result']
def visible(key):return any(n.get('key')==key for n in ui.nodes())
def choose(key,label):
 before={n['id'] for n in ui.nodes()};ui.press_key(key)
 def option():
  nodes=ui.nodes();popup={n['id'] for n in nodes if n.get('role')=='popup' and n['id'] not in before};return next((n for n in nodes if n.get('label')==label and n.get('parent') in popup),None)
 n=U.wait(option);b=n['bounds'];w=ui.req('windows')['windows'][0]['id'];ui.req('input',window=w,events=[{'type':'click','x':b['x']+b['width']/2,'y':b['y']+b['height']/2}]);ui.req('idle',timeout=1500)
ui=None
report={'status':'failed','actual_app':True,'actual_cli_inspection':True,'model_called':False,'synthetic_only':True,'fixture':str(f),'identifier':meta['identifier']}
try:
 (f/'client/agent-paths.json').write_text(json.dumps({'traex':'/Users/bytedance/.local/bin/traex','codex':'/Users/bytedance/.nvm/versions/node/v22.23.1/bin/codex'}))
 (f/'repo-a/.trae').mkdir();baseline='# SYNTHETIC_USER_OPTION_KEEP\nproject_doc_max_bytes = 8192\n';(f/'repo-a/.trae/traecli.toml').write_text(baseline)
 env={'PI_CODING_AGENT_DIR':str(f/'pi-agent'),'MEMLANE_TRAE_AUTH_DIRECTORY':str(Path.home()/'.trae/cli')}
 ui=U.UI.__new__(U.UI);ui.__init__(app,f,ROOT/'build/p3-agents-screenshots',extra_env=env);U.wait(lambda:call('health')['state']=='ready',timeout=95);assert call('list',list_query={'limit':5})['total']==0
 for name,repo in [('A 合成源项目','repo-a'),('A 合成其他项目','repo-b')]:
  ui.press('项目');ui.set('project-name',name);ui.set('project-path',str(f/repo));ui.press_key('register-project');U.wait(lambda:any(s['name']==name for s in call('scopes')))
 project=next(s['id'] for s in call('scopes') if s['name']=='A 合成源项目');ui.press('Agent');U.wait(lambda:visible('agent-traex'));ui.press_key('agent-traex');choose('agent-project','A 合成源项目');ui.press_key('connect-agent')
 conn=U.wait(lambda:next((c for c in call('connections') if c['agent']=='traex'),None));U.wait(lambda:visible('verify-agent'));assert baseline in (f/'repo-a/.trae/traecli.toml').read_text()
 options={'ClientRoot':cfg['root'],'ServiceRoot':str(data),'Bridge':str(app/'Contents/Resources/bin/memlane'),'AgentPath':'/Users/bytedance/.local/bin/traex','Workspace':str(f/'repo-a')}
 inspection=adapter('run',connection=conn,options=options,run={'InspectInput':True,'Prompt':'仅核对本次Memlane MCP工具清单；材料是合成数据。','Workspace':str(f/'repo-a'),'TraeAuthHome':str(Path.home()/'.trae/cli'),'Model':'GPT-6.1-Sol'})
 (OUT/'traex-prompt-input.json').write_text(json.dumps(inspection,ensure_ascii=False,indent=2)+'\n');ui.system_capture('traex-installed-pending-system.png')
 assert call('list',list_query={'limit':5})['total']==0,'preflight must not save memory'
 report.update(status='inspected_pending_tool_assertions',connection=conn,options=options,baseline_preserved=True,stage='configuration_and_no_model_inspection_only',service_stopped=True)
 ui.quit(stop=True);ui=None
except Exception as e:report.update(status='failed',error=repr(e));raise
finally:
 cleanup=[]
 if ui:
  try:G.close_partial_ui(ui,f,app,'agents')
  except Exception as e:cleanup.append({'step':'ui','error':repr(e)})
 try:
  G.validate_fixture(f,app,phase='agents');p=data/'service-state.json';st=json.loads(p.read_text()) if p.exists() else {};st['stopped']=True;tmp=p.with_name('.a-stop-'+uuid.uuid4().hex);tmp.write_text(json.dumps(st));os.replace(tmp,p)
  if Path(ipc).exists():
   try:call('stop')
   except OSError:pass
 except Exception as e:cleanup.append({'step':'persistent_stop','error':repr(e)})
 report['cleanup_errors']=cleanup
 if cleanup:report['status']='failed'
 (OUT/'preflight-app.json').write_text(json.dumps(report,ensure_ascii=False,indent=2)+'\n')
print(json.dumps(report,ensure_ascii=False))
