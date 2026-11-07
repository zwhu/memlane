#!/usr/bin/env python3
"""为真实 Agent/App 验收创建独立合成根，仅复用已验证的组件缓存。"""
import json
import shutil
import subprocess
import tempfile
import uuid
from pathlib import Path

ROOT=Path(__file__).resolve().parents[1]
old=Path((ROOT/'build/p0-engine/client-fixture-path.txt').read_text().strip())
assert 'memlane-client-p0-' in str(old)
cfg=json.loads((old/'runtime-config.json').read_text())
root=Path(tempfile.mkdtemp(prefix='memlane-p2-app-'));client=root/'client';client.mkdir()
shutil.copytree(Path(cfg['root'])/'runtime',client/'runtime',symlinks=True)
identifier='io.github.zwhu.memlane.p2.'+uuid.uuid4().hex[:12]
cfg['root']=str(client);cfg['service_plist']=identifier+'.service.plist'
(root/'runtime-config.json').write_text(json.dumps(cfg,indent=2))
for name in ['repo-a','repo-b']:
    path=root/name
    subprocess.run(['/usr/bin/git','init',str(path)],check=True,capture_output=True)
    subprocess.run(['/usr/bin/git','-C',str(path),'-c','user.name=Synthetic','-c','user.email=synthetic@example.invalid','commit','--allow-empty','-m','synthetic'],check=True,capture_output=True)
    (path/'README.md').write_text('Memlane P2 synthetic project only.\n')
(root/'pi-agent').mkdir()
(root/'agent-home').mkdir()
(root/'source-info.json').write_text(json.dumps({'identifier':identifier,'synthetic_only':True,'real_agent_verified':False}))
out=ROOT/'build/p2';out.mkdir(parents=True,exist_ok=True)
(out/'fixture-path.txt').write_text(str(root)+'\n')
print(json.dumps({'fixture':str(root),'identifier':identifier,'reused':'P0 component cache only','user_data_copied':False}),flush=True)
