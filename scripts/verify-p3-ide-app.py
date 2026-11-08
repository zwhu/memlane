#!/usr/bin/env python3
"""Actual App + real Rust, NO MODEL: IDE receipt protocol/fault fixture only."""
import hashlib,json,os,socket,subprocess,uuid
from pathlib import Path
from importlib.machinery import SourceFileLoader
ROOT=Path(__file__).resolve().parents[1];OUT=ROOT/'build/p3-ide';f=Path((OUT/'fixture-path.txt').read_text().strip());meta=json.loads((f/'source-info.json').read_text());app=Path(meta['app'])
G=SourceFileLoader('ideguard',str(ROOT/'scripts/p3-c2-common.py')).load_module();cfg=G.validate_fixture(f,app,phase='agents');U=SourceFileLoader('ideui',str(ROOT/'scripts/p1-ui.py')).load_module()
data=f/'client/data';ipc=f'/tmp/memlane-{os.getuid()}-{hashlib.sha256(str(data).encode()).hexdigest()[:12]}/service.sock'
def raw(op,**kw):
 with socket.socket(socket.AF_UNIX) as s:
  s.settimeout(12);s.connect(ipc);s.sendall((json.dumps({'protocol':1,'id':str(uuid.uuid4()),'op':op,**kw})+'\n').encode());return json.loads(s.makefile('rb',buffering=0).readline())
def call(op,**kw):
 r=raw(op,**kw);assert r['ok'],r;return r.get('result')
def tool(name,args,workspace=None,generation=None,expected=True,call_id=None):
 cmd=[str(app/'Contents/Resources/bin/memlane'),'call','--root',str(data),'--connection',conn['id'],'--generation',str(generation or conn['generation']),'--workspace',workspace or str(f/'repo-a'),'--call-id',call_id or uuid.uuid4().hex,'--tool',name]
 p=subprocess.run(cmd,input=json.dumps(args),capture_output=True,text=True,timeout=125);r=json.loads(p.stdout);assert r['ok']==expected,r;return r.get('result') if expected else r

def installed():return json.loads((f/'client/agent-adapters'/conn['id']/'installation.json').read_text())
def visible(key):return any(n.get('key')==key for n in ui.nodes())
def choose(key,label):
 before={n['id'] for n in ui.nodes()};ui.press_key(key)
 def option():
  nodes=ui.nodes();popup={n['id'] for n in nodes if n.get('role')=='popup' and n['id'] not in before};return next((n for n in nodes if n.get('label')==label and n.get('parent') in popup),None)
 n=U.wait(option);b=n['bounds'];w=ui.req('windows')['windows'][0]['id'];ui.req('input',window=w,events=[{'type':'click','x':b['x']+b['width']/2,'y':b['y']+b['height']/2}]);ui.req('idle',timeout=1500)
def begin():
 ui.press_key('verify-agent');return U.wait(lambda:installed().get('ide_challenge'))
def checker(d):return call('ide_verification',ide_verification=d)
def save(d):return tool('memory_save',{'operation_id':'ide-check-'+str(d['generation'])+'-'+d['nonce'],'text':'Memlane synthetic IDE connection verification memlaneIDEConnectionCheck:'+d['connection_id']+':'+str(d['generation'])+':'+d['nonce'],'origin':'raw','source':{'fragment':'Memlane IDE connection verification; synthetic only'}})
ui=None;conn=None;report={'status':'failed','actual_app':True,'actual_rust':True,'actual_ide_model':False,'model_called':False,'fixture_kind':'NO MODEL actual bridge protocol and faults','synthetic_only':True,'fixture':str(f),'cases':[]};baseline=(f/'repo-a/.trae/mcp.json').read_text()
try:
 ui=U.UI.__new__(U.UI);ui.__init__(app,f,ROOT/'build/p3-ide-screenshots');ui.press('设置');U.wait(lambda:'启动服务' in ui.labels() or '停止服务' in ui.labels(),timeout=95)
 if '启动服务' in ui.labels():ui.press('启动服务')
 U.wait(lambda:call('health')['state']=='ready',timeout=95)
 assert call('list',list_query={'limit':5})['total']==0
 for name,repo in [('IDE 合成项目','repo-a'),('IDE 合成其他项目','repo-b')]:
  if any(scope['name']==name for scope in call('scopes')):continue
  ui.press('项目');ui.set('project-name',name);ui.set('project-path',str(f/repo));ui.press_key('register-project');U.wait(lambda:any(s['name']==name for s in call('scopes')))
 project=next(s['id'] for s in call('scopes') if s['name']=='IDE 合成项目');other=next(s['id'] for s in call('scopes') if s['name']=='IDE 合成其他项目')
 report['step']='discover';ui.press('Agent');U.wait(lambda:visible('agent-trae'));ui.press_key('agent-trae');choose('agent-project','IDE 合成项目');report['step']='connect';ui.press_key('connect-agent');conn=U.wait(lambda:next((c for c in call('connections') if c['agent']=='trae'),None));U.wait(lambda:visible('verify-agent'));report['step']='begin';d=begin();assert checker(d)['state']=='waiting';assert not installed().get('verified_generation');assert call('list',list_query={'limit':5})['total']==0
 ui.press_key('ide-copy-prompt');prompt=subprocess.run(['/usr/bin/pbpaste'],capture_output=True,text=True).stdout;assert d['nonce'] in prompt and 'memory_get' in prompt;ui.system_capture('ide-app-pending-system.png');report['cases'].append('actual_app_config_prompt_copy_no_success')
 ui.press_key('ide-cancel-verification');U.wait(lambda:installed().get('ide_challenge') is None);d2=begin();assert d2['nonce']!=d['nonce'];save(d);assert checker(d2)['state']=='waiting';report['cases'].append('cancel_late_old_save_not_verified')
 tool('memory_save',{'operation_id':'scope-denied','text':'fixture denied','scope':other},expected=False);tool('memory_scopes',{},workspace=str(f/'repo-b'),expected=False);report['cases'].append('scope_workspace_denied')
 saved=save(d2);memory=saved['record']['memory'];assert saved['persisted'];assert not checker(d2)['complete']
 status=U.wait(lambda:(v if (v:=tool('memory_status',{'memory_id':memory}))['index_state']=='indexed' else None),timeout=95);assert not checker(d2)['complete'];tool('memory_get',{'memory_id':memory});assert not checker(d2)['complete'];report['cases'].append('save_status_read_without_search_not_verified')
 ui.quit(stop=True);ui=None
 ui=U.UI.__new__(U.UI);ui.__init__(app,f,ROOT/'build/p3-ide-screenshots');ui.press('设置');U.wait(lambda:'启动服务' in ui.labels() or '停止服务' in ui.labels(),timeout=95)
 if '启动服务' in ui.labels():ui.press('启动服务')
 U.wait(lambda:call('health')['state']=='ready',timeout=95);ui.press('Agent');U.wait(lambda:visible('connection-'+conn['id']));ui.press_key('connection-'+conn['id']);assert installed()['ide_challenge']==d2;assert not installed().get('verified_generation');report['cases'].append('pending_challenge_restart_persistent')
 marker='memlaneIDEConnectionCheck:'+conn['id']+':'+str(conn['generation'])+':'+d2['nonce'];hits=tool('memory_search',{'query':marker});assert any(h['version']['memory']==memory for h in hits['hits']);proof=checker(d2);assert proof['complete'] and len(proof['evidence'])==4;U.wait(lambda:installed().get('verified_generation')==conn['generation']);report['protocol_fixture_receipt']=proof;report['cases'].append('real_rust_search_durable_receipts_app_state')
 usage=call('usage',memory=memory);assert usage['daily_agent']['reads']==0 and usage['daily_agent']['returns']==0 and usage['connection_verification']['reads']>=1 and usage['connection_verification']['returns']>=1;report['cases'].append('verification_statistics_separate')
 ui.system_capture('ide-app-protocol-verified-system.png');ui.press_key('apply-agent-scopes');conn=U.wait(lambda:next((c for c in call('connections') if c['id']==conn['id'] and c['generation']>d2['generation']),None));U.wait(lambda:installed()['connection']['generation']==conn['generation']);assert not installed().get('verified_generation');assert not raw('ide_verification',ide_verification=d2)['ok'];tool('memory_scopes',{},generation=d2['generation'],expected=False);report['cases'].append('new_generation_invalidates_proof')
 ui.press_key('disconnect-agent');U.wait(lambda:not next(c for c in call('connections') if c['id']==conn['id'])['enabled']);U.wait(lambda:not (f/'client/agent-adapters'/conn['id']/'installation.json').exists());U.wait(lambda:'已断开；其他配置与记忆保留。' in ui.labels());remaining=(f/'repo-a/.trae/mcp.json').read_text();assert 'SYNTHETIC_USER_OPTION_KEEP' in remaining and 'synthetic_disabled' in remaining and '"memlane"' not in remaining;report['cases'].append('actual_app_complete_disconnect_preserves_other_config')
 ui.system_capture('ide-app-disconnected-system.png');ui.quit(stop=True);ui=None;report['status']='passed';report['step']='completed';report['real_ide_daily_acceptance']='NOT PASSED: this driver has no IDE model; separate evidence required'
except Exception as e:
 report.update(status='failed',error=repr(e))
 if ui:
  try:ui.capture('ide-app-failure.png');report['failure_labels']=ui.labels()
  except Exception as capture:report['failure_capture_error']=repr(capture)
 raise
finally:
 cleanup=[]
 if ui:
  try:
   if conn:
    try:call('disconnect',connection_id=conn['id'],user_confirmed=True)
    except Exception:pass
   G.close_partial_ui(ui,f,app,'agents')
  except Exception as e:cleanup.append(repr(e))
 report['cleanup_errors']=cleanup
 if cleanup:report['status']='failed'
 (OUT/'app-protocol-fixture.json').write_text(json.dumps(report,ensure_ascii=False,indent=2)+'\n')
print(json.dumps(report,ensure_ascii=False))
