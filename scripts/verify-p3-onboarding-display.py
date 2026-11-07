#!/usr/bin/env python3
"""最新展示窄范围实际窗口检查，无模型；先要求I真实正常/故障已通过。"""
import hashlib,json,os,socket,uuid
from pathlib import Path
from importlib.machinery import SourceFileLoader
ROOT=Path(__file__).resolve().parents[1];OUT=ROOT/'build/p3-onboarding'
f=Path((OUT/'fixture-path.txt').read_text().strip());app=ROOT/'build/p3-onboarding-app-display/darwin-arm64/MemlaneOnboarding.app'
G=SourceFileLoader('idisplayguard',str(ROOT/'scripts/p3-c2-common.py')).load_module();G.validate_fixture(f,app,phase='onboarding')
main=json.loads((OUT/'real-app.json').read_text());fault=json.loads((OUT/'index-fault-app.json').read_text());assert main['status']==fault['status']=='passed' and main['fixture']==str(f)
U=SourceFileLoader('idisplayui',str(ROOT/'scripts/p1-ui.py')).load_module();data=f/'client/data'
ipc=f'/tmp/memlane-{os.getuid()}-{hashlib.sha256(str(data).encode()).hexdigest()[:12]}/service.sock'
def call(op,**kw):
 with socket.socket(socket.AF_UNIX) as s:
  s.settimeout(8);s.connect(ipc);s.sendall((json.dumps({'protocol':1,'id':str(uuid.uuid4()),'op':op,**kw})+'\n').encode());r=json.loads(s.makefile('rb',buffering=0).readline());assert r['ok'],r;return r.get('result')
def visible(key):return any(n.get('key')==key for n in ui.nodes())
ui=None;report={'status':'failed','actual_app':True,'model_called':False,'synthetic_only':True,'app':str(app),'screenshots':[]}
try:
 assert json.loads((data/'service-state.json').read_text())['stopped'];assert not Path(ipc).exists()
 ui=U.UI.__new__(U.UI);ui.__init__(app,f,ROOT/'build/p3-onboarding-screenshots',extra_env={'PI_CODING_AGENT_DIR':str(f/'pi-agent')})
 ui.press('设置');U.wait(lambda:'启动服务' in ui.labels());ui.press('启动服务');U.wait(lambda:call('health')['state']=='ready',timeout=95)
 recovered=call('initialization_get',initialization_batch=fault['batch']);assert recovered['state']=='received' and recovered['indexed']==1 and recovered['saved']==1 and any(i['memory']==fault['memory'] for i in recovered['receipt']['items']),recovered
 assert call('get',memory=fault['memory'])['index_state']=='indexed'
 report.update(fixture=str(f),fault_batch=fault['batch'],fault_memory=fault['memory'])
 activities=call('activities',timezone='Asia/Shanghai');assert activities['imported']==3 and activities['total']==0,activities
 ui.press('总览');U.wait(lambda:'今天新增 0 条' in ui.labels());ui.system_capture('initialization-import-count-system.png');report['screenshots'].append('initialization-import-count-system.png')
 memory=next(c['memory'] for c in main['cases'] if c['case']=='real_pi_import_rust_current');batch=next(b for b in call('initialization_list') if b.get('receipt') and any(i['memory']==memory for i in b['receipt']['items']))
 ui.press('设置');ui.press_key('guidance-restore');ui.press_key('guidance-initialization');U.wait(lambda:visible('initialization-batch-'+batch['batch']));ui.press_key('initialization-batch-'+batch['batch']);U.wait(lambda:'已保存，可检索' in ui.labels());assert not any('此批次仍等待实际提交' in t for t in ui.labels());ui.system_capture('initialization-final-receipt-system.png');report['screenshots'].append('initialization-final-receipt-system.png')
 ui.press_key('initialization-memory-'+memory);U.wait(lambda:'v1 · 导入' in ui.labels());ui.system_capture('initialization-final-current-system.png');report['screenshots'].append('initialization-final-current-system.png')
 ui.press('历史记录');U.wait(lambda:'v1 · 导入' in ui.labels());assert not any('手动新增' in t or '初始化导入' in t for t in ui.labels() if t.startswith('v1 ·'));ui.system_capture('initialization-final-history-system.png');report['screenshots'].append('initialization-final-history-system.png')
 ui.quit(stop=True);ui=None;report.update(status='passed',imported=3,new_knowledge=0,import_label='导入',service_stopped=True)
except Exception as e:report['error']=repr(e);raise
finally:
 cleanup=[]
 if ui:
  try:G.close_partial_ui(ui,f,app,'onboarding')
  except Exception as e:cleanup.append({'step':'ui','error':repr(e)})
 try:
  G.validate_fixture(f,app,phase='onboarding');p=data/'service-state.json';st=json.loads(p.read_text());st['stopped']=True;tmp=p.with_name('.i-display-stop-'+uuid.uuid4().hex);tmp.write_text(json.dumps(st));os.replace(tmp,p)
  if Path(ipc).exists():
   try:call('stop')
   except OSError:pass
 except Exception as e:cleanup.append({'step':'persistent_stop','error':repr(e)})
 report['cleanup_errors']=cleanup
 if cleanup:report['status']='failed'
 (OUT/'final-display.json').write_text(json.dumps(report,ensure_ascii=False,indent=2)+'\n')
print(json.dumps(report,ensure_ascii=False))
if report['status']!='passed':raise SystemExit(1)
