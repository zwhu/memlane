#!/usr/bin/env python3
"""Verify real packaged UI close/quit/stop policies through native menus."""
import argparse, base64, hashlib, json, os, plistlib, socket, subprocess, sys, time, uuid
from pathlib import Path
ROOT=Path(__file__).resolve().parents[1]

def main():
 p=argparse.ArgumentParser(description=__doc__);p.add_argument('--app',type=Path,required=True);p.add_argument('--fixture',type=Path,required=True);p.add_argument('--from-step',choices=['close-quit','stop-quit','keepfalse-quit'],default='close-quit');a=p.parse_args()
 cfg=json.loads((a.fixture/'runtime-config.json').read_text());data=Path(cfg['root'])/'data';record=json.loads((a.fixture/'knowledge-fixture.json').read_text())
 ipc=f'/tmp/memlane-{os.getuid()}-{hashlib.sha256(str(data).encode()).hexdigest()[:12]}/service.sock'
 meta=plistlib.loads((a.app/'Contents/Info.plist').read_bytes());binary=a.app/'Contents/MacOS'/meta['CFBundleExecutable'];out=ROOT/'build/p0-engine/lifecycle-verification.json'
 report=json.loads(out.read_text()) if out.exists() else {'steps':[]};logdir=ROOT/'build/p0-screenshots';logdir.mkdir(exist_ok=True)
 def handoff(step,result='尚未通过'):
  subprocess.run([sys.executable,str(ROOT/'scripts/p0-handoff.py'),'d2-lifecycle-'+step,'实际窗口关闭、退出、停止策略与合成原文检索','--result',result],check=True)
 def rpc(op,**fields):
  with socket.socket(socket.AF_UNIX) as s:
   s.settimeout(5);s.connect(ipc);s.sendall((json.dumps({'protocol':1,'id':str(uuid.uuid4()),'op':op,**fields})+'\n').encode());r=json.loads(s.makefile('rb',buffering=0).readline());assert r['ok'],r;return r.get('result')
 def wait(fn,timeout=45):
  end=time.monotonic()+timeout;last=None
  while time.monotonic()<end:
   try:
    v=fn()
    if v:return v
   except (OSError,AssertionError) as e:last=repr(e)
   time.sleep(.1)
  raise AssertionError(('deadline',last))
 steps=['close-quit','stop-quit','keepfalse-quit'];proc=None
 for step in steps[steps.index(a.from_step):]:
  handoff(step);entry={'step':step,'status':'failed'};conn=None;log=None
  try:
   auto=f'/tmp/memlane-lifecycle-{uuid.uuid4().hex[:10]}.sock';home=a.fixture/'ui-home';home.mkdir(exist_ok=True)
   log=(logdir/(step+'.log')).open('w');proc=subprocess.Popen([str(binary.resolve())],cwd=a.fixture,env={'PATH':'/usr/bin:/bin','HOME':str(home),'TMPDIR':str(a.fixture),'MYGO_AUTOMATION':auto},stdout=log,stderr=log)
   entry['stage']='automation_socket';wait(lambda:Path(auto).exists());conn=socket.socket(socket.AF_UNIX);conn.settimeout(8);conn.connect(auto);stream=conn.makefile('rwb',buffering=0);seq=0
   def req(method,**params):
    nonlocal seq
    seq+=1;stream.write((json.dumps({'id':seq,'method':method,'params':params})+'\n').encode());line=stream.readline()
    if not line:raise EOFError('UI closed automation stream')
    r=json.loads(line)
    if 'error' in r:raise AssertionError(r['error'])
    return r['result']
   def nodes():
    req('capture')
    return req('tree')['nodes']
   def press(label):
    n=next(n for n in nodes() if n.get('label')==label and 'press' in n.get('actions',[]));req('act',id=n['id'],action='press');req('idle',timeout=2000)
   def menu(path):
    try:req('menu',path=path)
    except EOFError:pass
   entry['stage']='content_window';wait(lambda:req('windows').get('windows'))
   wait(lambda:bool(nodes()))
   entry['stage']='settings_navigation'
   press('设置')
   def ready():return rpc('health')['state']=='ready'
   try:healthy=ready()
   except OSError:healthy=False
   if not healthy:
    entry['stage']='resume_button';wait(lambda:any(n.get('label')=='启动服务' for n in nodes()));press('启动服务');entry['stage']='service_ready';wait(ready)
   health=rpc('health');entry['worker_pid']=health['worker_pid']
   if step=='close-quit':
    press('总览');req('menu',path=['文件','关闭窗口']);req('idle',timeout=2000)
    assert not req('windows')['windows'][0]['visible'];assert proc.poll() is None;assert ready()
    req('menu',path=['文件','打开 Memlane']);req('idle',timeout=2000);assert req('windows')['windows'][0]['visible']
    (logdir/'lifecycle-home.png').write_bytes(base64.b64decode(req('capture')['png']))
    entry.update(close_hides=True,reopen_visible=True,background_alive_while_hidden=True)
   if step=='keepfalse-quit':
    n=next(n for n in nodes() if n.get('label')=='退出 App 后继续运行记忆服务')
    assert 'checked' in n.get('states',[]),n;req('act',id=n['id'],action='press');req('idle',timeout=2000)
    assert not any('checked' in n.get('states',[]) for n in nodes() if n.get('label')=='退出 App 后继续运行记忆服务')
   if step=='stop-quit':menu(['文件','停止服务并退出'])
   else:
    for path in [[meta['CFBundleName'],'Quit '+meta['CFBundleName']],[meta['CFBundleName'],'退出 '+meta['CFBundleName']]]:
     try:menu(path);break
     except AssertionError:continue
    else:raise AssertionError('native Quit menu not found')
   assert proc.wait(timeout=30)==0;entry['ui_exit_code']=0
   if step=='close-quit':
    assert ready();current=rpc('get',memory=record['memory']);assert current['text']==record['text'] and current['revision']==1
    hits=rpc('search',datasets=[record['dataset']],query='clientPersistentOriginalToken',limit=3);assert any(h['version']['memory']==record['memory'] for h in hits)
    entry.update(background_survives_ui_quit=True,original_and_search_preserved=True)
   else:
    wait(lambda:not Path(ipc).exists());assert json.loads((data/'service-state.json').read_text())['stopped']
    try:os.kill(health['worker_pid'],0)
    except ProcessLookupError:pass
    else:raise AssertionError('worker still alive')
    from importlib.machinery import SourceFileLoader
    helper=SourceFileLoader('updatecheck',str(ROOT/'scripts/verify-client-update.py')).load_module();helper.stopped_original(data,record)
    entry.update(service_stopped=True,worker_exited=True,persistent_stop=True,original_preserved=True)
   entry['status']='passed';handoff(step+'-done',json.dumps(entry,ensure_ascii=False))
  except Exception as e:
   entry['error']=repr(e)
   if conn:
    try:entry['last_ui_labels']=[n.get('label') for n in nodes()]
    except Exception as snapshot_error:entry['snapshot_error']=repr(snapshot_error)
  finally:
   if conn:conn.close()
   if proc and proc.poll() is None:
    proc.terminate()
    try:proc.wait(timeout=10)
    except subprocess.TimeoutExpired:proc.kill();proc.wait()
   if log:log.close()
   report['steps']=[s for s in report['steps'] if s['step']!=step]+[entry];report['status']='passed' if len(report['steps'])==3 and all(s['status']=='passed' for s in report['steps']) else 'in_progress';out.write_text(json.dumps(report,ensure_ascii=False,indent=2)+'\n');print(json.dumps(entry,ensure_ascii=False))
  if entry['status']!='passed':raise SystemExit(1)

if __name__=='__main__':main()
