#!/usr/bin/env python3
"""Create a fresh synthetic P1 App root, reusing immutable verified components."""
import json,shutil,tempfile,uuid
from pathlib import Path
ROOT=Path(__file__).resolve().parents[1]
old=Path((ROOT/'build/p0-engine/client-fixture-path.txt').read_text().strip())
cfg=json.loads((old/'runtime-config.json').read_text())
source=Path(cfg['root'])/'runtime'
assert source.is_dir() and 'memlane-client-p0-' in str(old)
root=Path(tempfile.mkdtemp(prefix='memlane-p1-app-'));client=root/'client';client.mkdir()
shutil.copytree(source,client/'runtime',symlinks=True)
identifier='io.github.zwhu.memlane.p1.'+uuid.uuid4().hex[:12]
cfg['root']=str(client);cfg['service_plist']=identifier+'.service.plist'
(root/'runtime-config.json').write_text(json.dumps(cfg,indent=2))
(root/'source-info.json').write_text(json.dumps({'identifier':identifier,'synthetic_only':True}))
for name in ['repo-a','repo-b']:(root/name/'.git').mkdir(parents=True)
out=ROOT/'build/p1';out.mkdir(parents=True,exist_ok=True)
(out/'fixture-path.txt').write_text(str(root)+'\n')
print(json.dumps({'fixture':str(root),'identifier':identifier,'reused':'verified P0 component cache only','user_data_copied':False},indent=2))
