"""C2 驱动启动前的只读隔离门禁，不启动 App 或服务。"""
import json,plistlib
from pathlib import Path

def ensure_inside(path,root):
 assert path.resolve().is_relative_to(root.resolve()), "path escapes synthetic fixture"

def validate_fixture(fixture,app,phase="c2"):
 fixture=fixture.resolve();app=app.resolve()
 assert phase in ('c2','c3','c4','global','schedules','recovery','usage','onboarding','agents','code') and fixture.name.startswith('memlane-p3-'+phase+'-app-'), 'isolated synthetic root required'
 metadata=json.loads((fixture/'source-info.json').read_text())
 assert metadata.get('synthetic_only') is True, 'synthetic fixture marker required'
 identity=metadata['identifier']
 assert identity.startswith('io.github.zwhu.memlane.'+phase+'.'), 'isolated bundle identity required'
 info=plistlib.loads((app/'Contents/Info.plist').read_bytes())
 assert info['CFBundleIdentifier']==identity, 'App and fixture identities differ'
 local=json.loads((fixture/'runtime-config.json').read_text())
 bundled=json.loads((app/'Contents/Resources/runtime-config.json').read_text())
 assert local==bundled, 'App runtime config and fixture differ'
 assert Path(local['root']).resolve()==fixture/'client', 'runtime root escapes fixture'
 assert Path(local['root']).resolve().is_relative_to(fixture), 'runtime root escapes fixture'
 plist=local['service_plist']
 assert plist==identity+'.service.plist', 'service identity differs'
 service=plistlib.loads((app/'Contents/Library/LaunchAgents'/plist).read_bytes())
 assert Path(service['EnvironmentVariables']['MEMLANE_SERVICE_CONFIG']).resolve()==fixture/'client/service.json', 'service config escapes fixture'
 assert service['BundleProgram']=='Contents/Resources/bin/memlaned', 'unexpected helper path'
 for relative in ['client/data','client/data/memlane.sqlite','client/data/memlane.sqlite-wal','client/data/memlane.sqlite-shm','client/data/memlane.sqlite.owner.lock','client/data/service-state.json','client/data/.service.lock','client/service.json','pi-agent','ui-home']:
  path=fixture/relative
  if path.exists() or path.is_symlink():ensure_inside(path,fixture)
 service_config=fixture/"client/service.json"
 if service_config.exists():
  service_data=json.loads(service_config.read_text())
  assert Path(service_data["root"]).resolve()==fixture/"client/data", "service root differs"
  ensure_inside(Path(service_data["worker"]["DataDir"]),fixture)
 return local


def close_partial_ui(instance,fixture,app,phase):
 """关闭当前受门禁确认的隔离App；支持启动途中没有socket的实例。"""
 import os,re,signal,subprocess
 validate_fixture(fixture,app,phase)
 try:
  if hasattr(instance,'s'):instance.quit(stop=True);return
 except Exception:pass
 info=plistlib.loads((app/'Contents/Info.plist').read_bytes())
 binary=str((app/'Contents/MacOS'/info['CFBundleExecutable']).resolve())
 if getattr(instance,'pid',None):candidates=[instance.pid]
 else:
  proc=subprocess.run(['pgrep','-f','^'+re.escape(binary)+'$'],capture_output=True,text=True)
  candidates=[int(v) for v in proc.stdout.split()]
 for pid in candidates:
  command=subprocess.run(['ps','-p',str(pid),'-o','command='],capture_output=True,text=True).stdout.strip()
  if not command:continue
  if command!=binary:raise AssertionError('partial App pid no longer matches isolated bundle')
  os.kill(pid,signal.SIGTERM)
 proc=getattr(instance,'proc',None)
 if proc is not None:
  try:proc.wait(timeout=10)
  except subprocess.TimeoutExpired:proc.terminate();proc.wait(timeout=5)
 for name in ('s','log'):
  resource=getattr(instance,name,None)
  if resource is not None:resource.close()
