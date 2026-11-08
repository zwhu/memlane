#!/usr/bin/env python3
"""仅复现同隔离bundle换路径后的启动服务问题，不调用模型。"""
import hashlib,json,os,plistlib,socket,subprocess,time,uuid
from pathlib import Path
from importlib.machinery import SourceFileLoader
ROOT=Path(__file__).resolve().parents[1];OUT=ROOT/'build/p3-agents';f=Path((OUT/'fixture-path.txt').read_text().strip());app=ROOT/'build/p3-agents-app-r3/darwin-arm64/MemlaneAgents.app'
G=SourceFileLoader('startguard',str(ROOT/'scripts/p3-c2-common.py')).load_module();G.validate_fixture(f,app,'agents');U=SourceFileLoader('startui',str(ROOT/'scripts/p1-ui.py')).load_module();data=f/'client/data';meta=json.loads((f/'source-info.json').read_text());ipc=f'/tmp/memlane-{os.getuid()}-{hashlib.sha256(str(data).encode()).hexdigest()[:12]}/service.sock'
def health():
 try:
  with socket.socket(socket.AF_UNIX) as s:
   s.settimeout(1);s.connect(ipc);s.sendall((json.dumps({'protocol':1,'id':str(uuid.uuid4()),'op':'health'})+'\n').encode());r=json.loads(s.makefile('rb',buffering=0).readline());return r.get('result',{}).get('state')=='ready'
 except OSError:return False
ui=None;report={'status':'failed','model_called':False,'actual_app':True,'fixture':str(f),'app':str(app),'bundle':meta['identifier']}
try:
 ui=U.UI.__new__(U.UI);ui.__init__(app,f,OUT/'startup-screenshots',extra_env={'PI_CODING_AGENT_DIR':str(f/'pi-agent')});ui.press('设置');U.wait(lambda:'启动服务' in ui.labels());ui.press('启动服务')
 started=time.monotonic()
 while time.monotonic()-started<20:
  if health():report['status']='passed';break
  labels=ui.labels()
  if any('启动失败' in x or 'Bootstrap' in x or '注册失败' in x for x in labels):break
  time.sleep(.2)
 report['elapsed_seconds']=round(time.monotonic()-started,2);report['socket_exists']=Path(ipc).exists();report['labels']=ui.labels();report['native']=ui.native()
 label=meta['identifier']+'.service';r=subprocess.run(['launchctl','print',f'gui/{os.getuid()}/{label}'],capture_output=True,text=True);report['launchctl_code']=r.returncode;(OUT/'startup-launchctl-live.txt').write_text(r.stdout+r.stderr)
 ui.capture('helper-startup.png')
except Exception as e:report.update(status='failed',error=repr(e));raise
finally:
 if ui:G.close_partial_ui(ui,f,app,'agents')
 (OUT/'startup-repro.json').write_text(json.dumps(report,ensure_ascii=False,indent=2)+'\n')
print(json.dumps({'status':report['status'],'elapsed_seconds':report.get('elapsed_seconds'),'socket_exists':report.get('socket_exists'),'launchctl_code':report.get('launchctl_code')},ensure_ascii=False))
if report['status']!='passed':raise SystemExit(1)
