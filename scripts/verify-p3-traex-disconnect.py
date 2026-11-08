#!/usr/bin/env python3
"""补足真实App最终断开回执；已有真实verify/init/cancel保留原失败记录。"""
import hashlib,json,os,socket,uuid
from pathlib import Path
from importlib.machinery import SourceFileLoader
ROOT=Path(__file__).resolve().parents[1];OUT=ROOT/'build/p3-agents';f=Path((OUT/'fixture-path.txt').read_text().strip());app=ROOT/'build/p3-agents-app-r3/darwin-arm64/MemlaneAgents.app'
G=SourceFileLoader('adisguard',str(ROOT/'scripts/p3-c2-common.py')).load_module();G.validate_fixture(f,app,'agents');U=SourceFileLoader('adisui',str(ROOT/'scripts/p1-ui.py')).load_module();data=f/'client/data';ipc=f'/tmp/memlane-{os.getuid()}-{hashlib.sha256(str(data).encode()).hexdigest()[:12]}/service.sock'
prior=json.loads((OUT/'real-app.json').read_text());assert prior['fixture']==str(f) and prior['status']=='failed' and prior['error']=='AssertionError()' and not prior['cleanup_errors'];assert {c['case'] for c in prior['cases']}=={'real_app_verify','real_app_traex_initialization_rust','real_app_cancel_after_actual_save'}
pre=json.loads((OUT/'preflight-app.json').read_text());ident=pre['connection']['id'];baseline='# SYNTHETIC_USER_OPTION_KEEP\nproject_doc_max_bytes = 8192\n'
def call(op,**kw):
 with socket.socket(socket.AF_UNIX) as s:
  s.settimeout(8);s.connect(ipc);s.sendall((json.dumps({'protocol':1,'id':str(uuid.uuid4()),'op':op,**kw})+'\n').encode());r=json.loads(s.makefile('rb',buffering=0).readline());assert r['ok'],r;return r.get('result')
def connection():return next(c for c in call('connections') if c['id']==ident)
def visible(key):return any(n.get('key')==key for n in ui.nodes())
ui=None;report={'status':'failed','actual_app':True,'model_called':False,'synthetic_only':True,'fixture':str(f),'source_model_app_report':'real-app.json','original_failure':'Verifier checked config before asynchronous uninstall completed; original failed report retained'}
try:
 assert json.loads((data/'service-state.json').read_text())['stopped'];assert (f/'repo-a/.trae/traecli.toml').read_text()==baseline
 ui=U.UI.__new__(U.UI);ui.__init__(app,f,ROOT/'build/p3-agents-screenshots',extra_env={'PI_CODING_AGENT_DIR':str(f/'pi-agent')});ui.press('设置');U.wait(lambda:'启动服务' in ui.labels());ui.press('启动服务');U.wait(lambda:call('health')['state']=='ready',timeout=95)
 old=connection();assert not old['enabled'];assert not (f/'client/agent-adapters'/ident/'installation.json').exists()
 for c in prior['cases']:
  if c['case']=='real_app_traex_initialization_rust':
   done=call('initialization_get',initialization_batch=c['batch']);assert done['saved']==done['indexed']==2 and done['receipt']==c['receipt']
   for item in c['receipt']['items']:assert call('get',memory=item['memory'])['revision']==1
 ui.press('Agent');U.wait(lambda:visible('connection-'+ident));ui.press_key('connection-'+ident);U.wait(lambda:visible('connect-agent'));ui.press_key('connect-agent');c=U.wait(lambda:(lambda c:c if c['enabled'] and c['generation']>old['generation'] else None)(connection()));U.wait(lambda:visible('disconnect-agent'))
 record=f/'client/agent-adapters'/ident/'installation.json';installed=U.wait(lambda:json.loads(record.read_text()));assert installed['agent_channel']=='internal' and installed.get('verified_generation',0)==0
 ui.press_key('disconnect-agent');U.wait(lambda:not connection()['enabled'] and not record.exists() and (f/'repo-a/.trae/traecli.toml').read_text()==baseline);U.wait(lambda:'已断开；其他配置与记忆保留。' in ui.labels());ui.system_capture('traex-disconnected-preserved-system.png')
 report.update(status='passed',generation=c['generation'],disconnected=True,metadata_removed=True,unrelated_config_preserved=True,initialization_receipt_preserved=True,service_stopped=True)
 ui.quit(stop=True);ui=None
except Exception as e:report.update(status='failed',error=repr(e));raise
finally:
 cleanup=[]
 if ui:
  try:G.close_partial_ui(ui,f,app,'agents')
  except Exception as e:cleanup.append({'step':'ui','error':repr(e)})
 try:
  G.validate_fixture(f,app,'agents');p=data/'service-state.json';st=json.loads(p.read_text());st['stopped']=True;tmp=p.with_name('.a-dis-stop-'+uuid.uuid4().hex);tmp.write_text(json.dumps(st));os.replace(tmp,p)
  if Path(ipc).exists():
   try:call('stop')
   except OSError:pass
 except Exception as e:cleanup.append({'step':'persistent_stop','error':repr(e)})
 report['cleanup_errors']=cleanup
 if cleanup:report['status']='failed'
 (OUT/'disconnect-app.json').write_text(json.dumps(report,ensure_ascii=False,indent=2)+'\n')
print(json.dumps(report,ensure_ascii=False))
if report['status']!='passed':raise SystemExit(1)
