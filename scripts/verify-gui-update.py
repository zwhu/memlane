#!/usr/bin/env python3
"""Exercise GUI update installation, recovery and actual Launch Services relaunch."""
import argparse,hashlib,json,os,plistlib,socket,subprocess,sys,time,urllib.request,uuid
from pathlib import Path
from importlib.machinery import SourceFileLoader
ROOT=Path(__file__).resolve().parents[1];helper=SourceFileLoader('prepui',str(ROOT/'scripts/verify-preparation-ui.py')).load_module()
def main():
 p=argparse.ArgumentParser(description=__doc__);p.add_argument('--app',type=Path,required=True);p.add_argument('--fixture',type=Path,required=True);p.add_argument('--from-step',choices=['failure','success'],default='failure');a=p.parse_args();cfg=json.loads((a.fixture/'runtime-config.json').read_text());info=json.loads((a.fixture/'source-info.json').read_text());record=json.loads((a.fixture/'knowledge-fixture.json').read_text());data=Path(cfg['root'])/'data';ipc=f'/tmp/memlane-{os.getuid()}-{hashlib.sha256(str(data).encode()).hexdigest()[:12]}/service.sock';app=a.app.resolve();meta=plistlib.loads((app/'Contents/Info.plist').read_bytes());binary=app/'Contents/MacOS'/meta['CFBundleExecutable'];ui=helper.UI(app,a.fixture);out=ROOT/'build/p0-engine/gui-update-verification.json';report=json.loads(out.read_text()) if out.exists() else {'steps':[]};new_pid=None
 def rpc(op,**fields):
  with socket.socket(socket.AF_UNIX) as s:
   s.settimeout(5);s.connect(ipc);s.sendall((json.dumps({'protocol':1,'id':str(uuid.uuid4()),'op':op,**fields})+'\n').encode());r=json.loads(s.makefile('rb',buffering=0).readline());assert r['ok'],r;return r.get('result')
 def healthy():return rpc('health')['state']=='ready'
 def control(mode):urllib.request.urlopen(urllib.request.Request(info['http_url']+'/control',data=json.dumps({'update_mode':mode}).encode(),headers={'Content-Type':'application/json'}),timeout=5).read()
 def originals():
  v=rpc('get',memory=record['memory']);assert v['revision']==1 and v['text']==record['text'];hits=rpc('search',datasets=[record['dataset']],query='clientPersistentOriginalToken',limit=3);assert any(h['version']['memory']==record['memory'] for h in hits)
 def step(name,fn):
  if name=='failure' and a.from_step=='success':return
  subprocess.run([sys.executable,str(ROOT/'scripts/p0-handoff.py'),'d-gui-update-'+name,'真实GUI更新入口与后台/原文检查'],check=True);r={'step':name,'status':'failed'}
  try:r.update(fn() or {});r['status']='passed'
  except Exception as e:r['error']=repr(e)
  report['steps']=[s for s in report['steps'] if s['step']!=name]+[r];out.write_text(json.dumps(report,ensure_ascii=False,indent=2)+'\n');print(json.dumps(r,ensure_ascii=False),flush=True)
  if r['status']!='passed':raise AssertionError(r)
  subprocess.run([sys.executable,str(ROOT/'scripts/p0-handoff.py'),'d-gui-update-'+name+'-done','本步骤实际验收通过','--result',json.dumps(r,ensure_ascii=False)],check=True)
 try:
  try:ready=healthy()
  except OSError:ready=False
  if not ready:helper.wait(lambda:'启动服务' in ui.labels());ui.press('启动服务');helper.wait(healthy)
  old_hash=hashlib.sha256(binary.read_bytes()).hexdigest()
  def failure():
   control('signature_bad');ui.press('检查更新');helper.wait(lambda:'安装更新并重新打开' in ui.labels());ui.press('安装更新并重新打开');helper.wait(lambda:any('更新失败' in s for s in ui.labels()));assert ui.proc.poll() is None;assert hashlib.sha256(binary.read_bytes()).hexdigest()==old_hash;helper.wait(healthy);originals();ui.capture('gui-update-failure.png');return {'gui_signature_error_reported':True,'old_app_unchanged':True,'service_recovered':True,'original_search_preserved':True}
  step('failure',failure)
  def success():
   nonlocal new_pid
   control('normal');before=json.load(urllib.request.urlopen(info['http_url']+'/state',timeout=5))['requests'];ui.press('检查更新');helper.wait(lambda:'安装更新并重新打开' in ui.labels());ui.capture('gui-update-found.png');ui.press('安装更新并重新打开');assert ui.proc.wait(timeout=45)==0
   expected=json.loads((a.fixture/'latest-update.json').read_text())['version'];helper.wait(lambda:plistlib.loads((app/'Contents/Info.plist').read_bytes())['CFBundleShortVersionString']==expected,timeout=45)
   pattern='^'+str(binary)
   def launched():
    r=subprocess.run(['pgrep','-f',pattern],capture_output=True,text=True);pids=[int(p) for p in r.stdout.split() if int(p)!=ui.proc.pid];return pids[0] if len(pids)==1 else None
   new_pid=helper.wait(launched,timeout=30);helper.wait(healthy,timeout=60);originals();subprocess.run(['/usr/bin/codesign','--verify','--deep','--strict',str(app)],check=True,capture_output=True);after=json.load(urllib.request.urlopen(info['http_url']+'/state',timeout=5))['requests']
   for id in [cfg['worker_id'],cfg['model_id']]:assert before.get('/components/'+id+'.tar.gz',0)==after.get('/components/'+id+'.tar.gz',0)
   return {'old_version':meta['CFBundleShortVersionString'],'new_version':expected,'old_ui_exited':True,'new_ui_pid':new_pid,'actual_launch_services_relaunch':True,'background_ready':True,'original_search_preserved':True,'components_reused':True,'codesign_valid':True}
  step('success',success);report['status']='passed';out.write_text(json.dumps(report,ensure_ascii=False,indent=2)+'\n')
 finally:
  control('normal')
  if ui.proc.poll() is None:
   try:ui.quit()
   except Exception:ui.proc.terminate();ui.proc.wait(timeout=10)
  if new_pid:
   r=subprocess.run([str(binary),'--p0-action','stop'],capture_output=True,text=True,timeout=30);result=json.loads(r.stdout);assert 'error' not in result,result
   os.kill(new_pid,15)
if __name__=='__main__':main()
