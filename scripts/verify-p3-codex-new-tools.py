#!/usr/bin/env python3
"""A1b真实Codex新工具；复用已通过隔离App/项目，不改日常配置/模型。"""
import hashlib,json,os,socket,subprocess,uuid
from pathlib import Path
from importlib.machinery import SourceFileLoader
ROOT=Path(__file__).resolve().parents[1];OUT=ROOT/'build/p3-agents';f=Path((OUT/'fixture-path.txt').read_text().strip());app=ROOT/'build/p3-agents-app-r3/darwin-arm64/MemlaneAgents.app'
G=SourceFileLoader('cxguard',str(ROOT/'scripts/p3-c2-common.py')).load_module();cfg=G.validate_fixture(f,app,'agents');U=SourceFileLoader('cxui',str(ROOT/'scripts/p1-ui.py')).load_module();data=f/'client/data';ipc=f'/tmp/memlane-{os.getuid()}-{hashlib.sha256(str(data).encode()).hexdigest()[:12]}/service.sock'
base=json.loads((OUT/'real-tools-report.json').read_text());assert base['status']=='passed' and base['fixture']==str(f);selection=json.loads((OUT/'codex-selection-preflight.json').read_text());model=selection['model'];assert model and selection['auth_file_available'];auth=Path(os.environ.get('CODEX_HOME',str(Path.home()/'.codex')))
def call(op,**kw):
 with socket.socket(socket.AF_UNIX) as s:
  s.settimeout(8);s.connect(ipc);s.sendall((json.dumps({'protocol':1,'id':str(uuid.uuid4()),'op':op,**kw})+'\n').encode());r=json.loads(s.makefile('rb',buffering=0).readline());assert r['ok'],r;return r.get('result')
def known_model_version_failure(reason,model):
 try:
  payload=json.loads(reason.removeprefix('验证失败：'))
  return payload.get('status')==400 and payload.get('error',{}).get('type')=='invalid_request_error' and payload['error'].get('message','').startswith("The '"+model+"' model requires a newer version of Codex.")
 except (ValueError,TypeError,AttributeError,KeyError):return False
def visible(key):return any(n.get('key')==key for n in ui.nodes())
def choose(key,label):
 before={n['id'] for n in ui.nodes()};ui.press_key(key)
 def option():
  nodes=ui.nodes();popup={n['id'] for n in nodes if n.get('role')=='popup' and n['id'] not in before};return next((n for n in nodes if n.get('label')==label and n.get('parent') in popup),None)
 n=U.wait(option);b=n['bounds'];w=ui.req('windows')['windows'][0]['id'];ui.req('input',window=w,events=[{'type':'click','x':b['x']+b['width']/2,'y':b['y']+b['height']/2}]);ui.req('idle',timeout=1500)
def run(prompt,initialization=False):
 request={'action':'run','connection':conn,'options':options,'run':{'Prompt':prompt,'Workspace':str(f/'repo-a'),'CodexAuthHome':str(auth),'Model':model,'UsageChannel':'daily','Initialization':initialization}}
 env=dict(os.environ,HOME=str(f/'ui-home'))
 proc=subprocess.run([str(OUT/'p2-agent-check')],input=json.dumps(request),env=env,capture_output=True,text=True,timeout=210);return json.loads(proc.stdout)
ui=None;conn=None;options=None;report={'status':'failed','actual_app':True,'real_agent':'codex','selected_model':model,'actual_model_reported':None,'model_fixture':False,'fixture':str(f),'cases':[]}
try:
 assert json.loads((data/'service-state.json').read_text())['stopped'];ui=U.UI.__new__(U.UI);ui.__init__(app,f,ROOT/'build/p3-agents-screenshots',extra_env={'PI_CODING_AGENT_DIR':str(f/'pi-agent'),'CODEX_HOME':str(auth)})
 ui.press('设置');U.wait(lambda:'启动服务' in ui.labels());ui.press('启动服务');U.wait(lambda:call('health')['state']=='ready',timeout=95)
 config=f/'repo-a/.codex/config.toml';G.ensure_inside(config,f);assert not config.exists(),'fresh synthetic Codex config required'
 options={'ClientRoot':cfg['root'],'ServiceRoot':str(data),'Bridge':str(app/'Contents/Resources/bin/memlane'),'AgentPath':'/Users/bytedance/.nvm/versions/node/v22.23.1/bin/codex','Workspace':str(f/'repo-a')}
 ui.press('Agent');U.wait(lambda:visible('agent-codex'));ui.press_key('agent-codex');choose('agent-project','A 合成源项目');ui.press_key('connect-agent');conn=U.wait(lambda:next((c for c in call('connections') if c['agent']=='codex'),None));U.wait(lambda:visible('verify-agent'))
 ui.set('agent-model',model);ui.press_key('verify-agent');labels=U.wait(lambda:(lambda labels:labels if any('真实 Agent 读写验证通过。'==s or s.startswith('验证失败：') for s in labels) else None)(ui.labels()),timeout=195)
 if '真实 Agent 读写验证通过。' not in labels:
  reason=next(s for s in labels if s.startswith('验证失败：'));known_version_error=known_model_version_failure(reason,model);report.update(status='blocked_agent_model' if known_version_error else 'failed',reason=reason,new_tools_verified=False);ui.system_capture('codex-model-unavailable-system.png')
 else:
  report['cases'].append({'case':'actual_codex_app_verification','generation':conn['generation']});ui.system_capture('codex-real-app-verified-system.png')
  before=call('usage',memory=base['memory'])['daily_agent']['reported_adoptions'];op='codex-new-adopt-'+uuid.uuid4().hex[:12]
  prompt='仅使用Memlane工具，不读取文件/运行命令/修改知识。memory_get memory_id='+json.dumps(base['memory'])+' scope='+json.dumps(base['scope'])+'，再memory_search query="依赖变更 pnpm-lock.yaml" scope='+json.dumps(base['scope'])+'。用首次memory_get的usage_receipt.id，memory_adopt memory_id='+json.dumps(base['memory'])+' revision=1 scope='+json.dumps(base['scope'])+' exposure_receipt=上述id operation_id='+json.dumps(op)+'，再完全相同参数调用memory_adopt一次核对幂等。'
  result=run(prompt);(OUT/'codex-real-adoption.json').write_text(json.dumps(result,ensure_ascii=False,indent=2)+'\n');assert result['ok'],result
  calls=result['result']['calls'];reads=[c for c in calls if c['tool']=='memory_get' and c['response']['ok'] and c['response']['result']['memory']==base['memory']];assert reads;value=reads[0]['response']['result'];expected={'operation_id':op,'memory_id':base['memory'],'revision':1,'scope':base['scope'],'exposure_receipt':value['usage_receipt']['id']}
  adopts=[c for c in calls if c['tool']=='memory_adopt' and c['response']['ok'] and c['arguments']==expected];assert len(adopts)>=2 and adopts[0]['response']['result']==adopts[1]['response']['result'];assert call('usage',memory=base['memory'])['daily_agent']['reported_adoptions']==before+1
  report['cases'].append({'case':'real_codex_adoption_replay','memory':base['memory'],'version':result['result']['version'],'adopt_events_delta':1})
  ui.press_key('agent-initialization');U.wait(lambda:visible('initialization-paths'));ui.set('initialization-paths','README.md');before_batches={b['batch'] for b in call('initialization_list')};ui.press_key('initialization-prepare');batch=U.wait(lambda:next((b for b in call('initialization_list') if b['batch'] not in before_batches),None));prompt=subprocess.run(['/usr/bin/pbpaste'],capture_output=True,text=True,check=True).stdout;assert batch['batch'] in prompt
  result=run(prompt+'\n仅从批准README提取一条，稳定id为codex-initialization-policy，精确引用批准快照。',True);(OUT/'codex-real-initialization.json').write_text(json.dumps(result,ensure_ascii=False,indent=2)+'\n');assert result['ok'],result
  names={c['tool'] for c in result['result']['calls']};assert {'memory_initialization_get','memory_initialize'}<=names and names<={'memory_initialization_get','memory_initialization_status','memory_initialize'}
  done=U.wait(lambda:(lambda b:b if b['saved']==b['indexed']==1 else None)(call('initialization_get',initialization_batch=batch['batch'])),timeout=95);memory=done['receipt']['items'][0]['memory'];v=call('get',memory=memory);hits=call('search',datasets=[conn['project']],query=v['text'],limit=50);assert any(h['version']['memory']==memory and h['version']['revision']==1 for h in hits);ui.system_capture('codex-real-initialization-system.png');report['cases'].append({'case':'real_codex_initialization_rust','batch':batch['batch'],'memory':memory});report.update(status='passed',new_tools_verified=True)
 # Always disconnect the temporary configuration after the complete result.
 ui.press('Agent');ui.press_key('connection-'+conn['id']);U.wait(lambda:visible('disconnect-agent'));ui.press_key('disconnect-agent');U.wait(lambda:not next(c for c in call('connections') if c['id']==conn['id'])['enabled'] and not (f/'client/agent-adapters'/conn['id']/'installation.json').exists());U.wait(lambda:'已断开；其他配置与记忆保留。' in ui.labels());assert config.read_text()==''
 conn=None;ui.quit(stop=True);ui=None;report['service_stopped']=True
except Exception as e:report.update(status='failed',error=repr(e));raise
finally:
 cleanup=[]
 if conn and options:
  try:
   call('disconnect',user_confirmed=True,connection_id=conn['id'])
  except Exception as e:cleanup.append({'step':'connection_revoke','error':repr(e)})
  try:
   p=subprocess.run([str(OUT/'p2-agent-check')],input=json.dumps({'action':'uninstall','connection':conn,'options':options}),capture_output=True,text=True,timeout=20);assert json.loads(p.stdout)['ok']
  except Exception as e:cleanup.append({'step':'configuration_remove','error':repr(e)})
 if ui:
  try:G.close_partial_ui(ui,f,app,'agents')
  except Exception as e:cleanup.append({'step':'ui','error':repr(e)})
 try:
  G.validate_fixture(f,app,'agents');p=data/'service-state.json';st=json.loads(p.read_text());st['stopped']=True;tmp=p.with_name('.codex-new-stop-'+uuid.uuid4().hex);tmp.write_text(json.dumps(st));os.replace(tmp,p)
  if Path(ipc).exists():
   try:call('stop')
   except OSError:pass
 except Exception as e:cleanup.append({'step':'persistent_stop','error':repr(e)})
 report['cleanup_errors']=cleanup
 if cleanup:report['status']='failed'
 (OUT/'codex-new-tools-report.json').write_text(json.dumps(report,ensure_ascii=False,indent=2)+'\n')
print(json.dumps(report,ensure_ascii=False))
if report['status']=='failed':raise SystemExit(1)
