#!/usr/bin/env python3
"""Exercise actual packaged component preparation UI against a local source."""
import argparse,base64,hashlib,json,os,plistlib,socket,subprocess,sys,time,urllib.request,uuid
from pathlib import Path
ROOT=Path(__file__).resolve().parents[1]
class UI:
 def __init__(self,app,fixture,config=None,native=None,rpc_timeout=8,launch_services=False,system_home=False):
  meta=plistlib.loads((app/'Contents/Info.plist').read_bytes());self.name=meta['CFBundleName'];self.seq=0
  self.path='/tmp/memlane-prep-'+uuid.uuid4().hex[:10]+'.sock';self.log=(ROOT/'build/p0-screenshots/preparation-ui.log').open('a')
  cmd=[str((app/'Contents/MacOS'/meta['CFBundleExecutable']).resolve())]
  if config:cmd+=['--p0-config',str(config)]
  env={'PATH':'/usr/bin:/bin','HOME':str(fixture/'ui-home'),'MYGO_AUTOMATION':self.path}
  if system_home:
   assert launch_services,'system GUI environment requires Launch Services'
   del env['HOME']
  if native:env['MEMLANE_NATIVE_PROBE']=native
  if launch_services:
   launch_log=ROOT/'build/p0-screenshots'/('system-launch-'+uuid.uuid4().hex[:10]+'.log')
   command=['/usr/bin/open','-n','-W','--stdout',str(launch_log),'--stderr',str(launch_log)]
   for name,value in env.items():command+=['--env',name+'='+value]
   command+=[str(app.resolve())]
   if config:command+=['--args','--p0-config',str(config)]
   self.proc=subprocess.Popen(command,cwd=fixture,stdout=self.log,stderr=self.log)
   pattern='^'+str((app/'Contents/MacOS'/meta['CFBundleExecutable']).resolve())
   def pid():
    result=subprocess.run(['pgrep','-f',pattern],capture_output=True,text=True);pids=[int(p) for p in result.stdout.split()];return pids[0] if len(pids)==1 else None
   self.pid=wait(pid)
  else:
   self.proc=subprocess.Popen(cmd,cwd=fixture,env=env,stdout=self.log,stderr=self.log)
   self.pid=self.proc.pid
  wait(lambda:Path(self.path).exists());self.s=socket.socket(socket.AF_UNIX);self.s.settimeout(rpc_timeout);self.s.connect(self.path);self.f=self.s.makefile('rwb',buffering=0)
  wait(lambda:self.req('windows').get('windows'));wait(lambda:self.nodes());self.press('设置')
 def req(self,m,**p):
  self.seq+=1;self.s.sendall((json.dumps({'id':self.seq,'method':m,'params':p})+'\n').encode());line=self.f.readline()
  if not line:raise EOFError('closed UI')
  r=json.loads(line);assert 'error' not in r,r;return r['result']
 def nodes(self):
  self.req('capture')
  return self.req('tree').get('nodes',[])
 def labels(self):return [n.get('label','') for n in self.nodes()]
 def press(self,label):
  n=next(n for n in self.nodes() if n.get('label')==label and 'press' in n.get('actions',[]));assert 'disabled' not in n.get('states',[]),n;self.req('act',id=n['id'],action='press');self.req('idle',timeout=2000)
 def capture(self,name):(ROOT/'build/p0-screenshots'/name).write_bytes(base64.b64decode(self.req('capture')['png']))
 def quit(self):
  try:self.req('menu',path=[self.name,'Quit '+self.name])
  except EOFError:pass
  self.proc.wait(timeout=30);self.s.close();self.log.close()
def wait(fn,timeout=45):
 end=time.monotonic()+timeout;last=None
 while time.monotonic()<end:
  try:
   v=fn()
   if v:return v
  except (OSError,AssertionError) as e:last=repr(e)
  time.sleep(.1)
 raise AssertionError(('deadline',last))
def main():
 p=argparse.ArgumentParser(description=__doc__);p.add_argument('--app',type=Path,required=True);p.add_argument('--fixture',type=Path,required=True);p.add_argument('--from-step',choices=['failure','cancel','retry','empty-offline'],default='failure');a=p.parse_args();cfg=json.loads((a.fixture/'runtime-config.json').read_text());info=json.loads((a.fixture/'source-info.json').read_text());(a.fixture/'ui-home').mkdir(exist_ok=True);out=ROOT/'build/p0-engine/preparation-ui-verification.json';report=json.loads(out.read_text()) if out.exists() else {'status':'in_progress','steps':[]};ui=None
 def control(**fields):
  urllib.request.urlopen(urllib.request.Request(info['http_url']+'/control',data=json.dumps(fields).encode(),headers={'Content-Type':'application/json'}),timeout=5).read()
 def step(name,fn):
  if ['failure','cancel','retry','empty-offline'].index(name)<['failure','cancel','retry','empty-offline'].index(a.from_step):return
  subprocess.run([sys.executable,str(ROOT/'scripts/p0-handoff.py'),'d-ui-'+name,'真实准备UI及受控组件源验收'],check=True);item={'step':name,'status':'failed'}
  try:item.update(fn() or {});item['status']='passed'
  except Exception as e:
   item['error']=repr(e)
   if ui:
    try:item['ui_labels']=ui.labels()
    except Exception:pass
  report['steps']=[s for s in report['steps'] if s['step']!=name]+[item];out.write_text(json.dumps(report,ensure_ascii=False,indent=2)+'\n');print(json.dumps(item,ensure_ascii=False),flush=True)
  if item['status']!='passed':raise AssertionError(item)
  subprocess.run([sys.executable,str(ROOT/'scripts/p0-handoff.py'),'d-ui-'+name+'-done','本步骤真实验收通过','--result',json.dumps(item,ensure_ascii=False)],check=True)
 try:
  control(component_mode='http_error' if a.from_step=='failure' else 'normal',component_chunk_delay=.04 if a.from_step=='cancel' else 0);ui=UI(a.app,a.fixture)
  def fail():
   wait(lambda:any('准备失败' in s for s in ui.labels()));assert any(n.get('label')=='准备 / 重试' and 'disabled' not in n.get('states',[]) for n in ui.nodes());ui.capture('preparation-failed.png');return {'actual_503_reported':True,'ui_available_without_components':True,'labels':ui.labels()}
  step('failure',fail)
  def cancel():
   control(component_mode='normal',component_chunk_delay=.04)
   if '取消准备' not in ui.labels():ui.press('准备 / 重试')
   wait(lambda:any(n.get('role')=='progress' for n in ui.nodes()));wait(lambda:any('已准备' in s and '285.75 MB' in s for s in ui.labels()))
   wait(lambda:any(0<p.stat().st_size<219498962 for p in (Path(cfg['root'])/'runtime/downloads').glob('*.part')))
   ui.capture('preparation-progress.png');ui.press('取消准备');wait(lambda:any('context canceled' in s or '已取消' in s for s in ui.labels()));partials=list((Path(cfg['root'])/'runtime').rglob('*.part'));assert partials,partials;return {'actual_progress':True,'total_component_mb':285.75,'cancellation_reported':True,'partial_bytes':sum(p.stat().st_size for p in partials)}
  step('cancel',cancel)
  def retry():
   control(component_chunk_delay=0);ui.press('准备 / 重试');wait(lambda:'服务运行中' in ui.labels(),timeout=90);ui.capture('preparation-ready.png');assert '停止服务' in ui.labels();return {'retry_ready':True,'actual_system_service_started':True,'labels':ui.labels()}
  step('retry',retry)
  ui.press('停止服务');wait(lambda:'启动服务' in ui.labels());ui.quit();ui=None
  def offline():
   config=dict(cfg);config['root']=str(a.fixture/'empty-offline');config['component_source']='http://127.0.0.1:1/components.json';path=a.fixture/'empty-offline.json';path.write_text(json.dumps(config));nonlocal ui;ui=UI(a.app,a.fixture,path);wait(lambda:any('需要联网' in s or '准备失败' in s for s in ui.labels()));assert '服务运行中' not in ui.labels();ui.capture('preparation-empty-offline.png');return {'empty_cache_not_ready':True,'actual_connection_failure':True,'labels':ui.labels()}
  step('empty-offline',offline);ui.quit();ui=None;report['status']='passed';out.write_text(json.dumps(report,ensure_ascii=False,indent=2)+'\n')
 finally:
  control(component_mode='normal',component_chunk_delay=0)
  if ui and ui.proc.poll() is None:ui.proc.terminate();ui.proc.wait(timeout=10)
if __name__=='__main__':main()
