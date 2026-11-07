"""C2 驱动启动前的只读隔离门禁，不启动 App 或服务。"""
import json,plistlib
from pathlib import Path

def ensure_inside(path,root):
 assert path.resolve().is_relative_to(root.resolve()), "path escapes synthetic fixture"

def validate_fixture(fixture,app):
 fixture=fixture.resolve();app=app.resolve()
 assert fixture.name.startswith('memlane-p3-c2-app-'), 'isolated synthetic root required'
 metadata=json.loads((fixture/'source-info.json').read_text())
 assert metadata.get('synthetic_only') is True, 'synthetic fixture marker required'
 identity=metadata['identifier']
 assert identity.startswith('io.github.zwhu.memlane.c2.'), 'isolated bundle identity required'
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
