#!/usr/bin/env python3
"""D2a 实际 App 新合成根，仅复用引擎/模型，不复制业务库。"""
import json, shutil, subprocess, tempfile, uuid
from pathlib import Path
ROOT=Path(__file__).resolve().parents[1];OUT=ROOT/'build/p3-recovery';OUT.mkdir(parents=True,exist_ok=True)
old=Path((ROOT/'build/p0-engine/client-fixture-path.txt').read_text().strip())
assert old.name.startswith('memlane-client-p0-')
cfg=json.loads((old/'runtime-config.json').read_text())
f=Path(tempfile.mkdtemp(prefix='memlane-p3-recovery-app-'));client=f/'client';client.mkdir()
shutil.copytree(Path(cfg['root'])/'runtime',client/'runtime',symlinks=True)
identifier='io.github.zwhu.memlane.recovery.'+uuid.uuid4().hex[:12]
cfg.update(root=str(client),service_plist=identifier+'.service.plist')
(f/'runtime-config.json').write_text(json.dumps(cfg,indent=2))
(f/'source-info.json').write_text(json.dumps({'synthetic_only':True,'identifier':identifier,'app':str(ROOT/'build/p3-recovery-app/darwin-arm64/MemlaneRecovery.app')}))
subprocess.run(['git','init',str(f/'repo-a')],check=True,capture_output=True)
(f/'pi-agent').mkdir();(OUT/'fixture-path.txt').write_text(str(f)+'\n')
print(json.dumps({'fixture':str(f),'identifier':identifier,'user_data_copied':False}))
