#!/usr/bin/env python3
"""A2 actual App/IDE roots. Copies only accepted worker/model components."""
import json,shutil,subprocess,tempfile,uuid
from pathlib import Path
ROOT=Path(__file__).resolve().parents[1];OUT=ROOT/'build/p3-ide';OUT.mkdir(parents=True,exist_ok=True)
old=Path((ROOT/'build/p0-engine/client-fixture-path.txt').read_text().strip());assert old.name.startswith('memlane-client-p0-')
cfg=json.loads((old/'runtime-config.json').read_text());f=Path(tempfile.mkdtemp(prefix='memlane-p3-agents-app-'))
(f/'client').mkdir();shutil.copytree(Path(cfg['root'])/'runtime',f/'client/runtime',symlinks=True)
ident='io.github.zwhu.memlane.agents.'+uuid.uuid4().hex[:12];cfg.update(root=str(f/'client'),service_plist=ident+'.service.plist')
(f/'runtime-config.json').write_text(json.dumps(cfg,indent=2));(f/'source-info.json').write_text(json.dumps({'identifier':ident,'synthetic_only':True,'slice':'A2','app':str(ROOT/'build/p3-ide-app/darwin-arm64/MemlaneIDE.app')}))
for name in ('repo-a','repo-b'):
 subprocess.run(['git','init',str(f/name)],check=True,capture_output=True)
for name in ('pi-agent','ide-profile','ide-extensions','ui-home'):(f/name).mkdir()
(f/'repo-a/.trae').mkdir();(f/'repo-a/.trae/mcp.json').write_text('// SYNTHETIC_USER_OPTION_KEEP\n{\n  "mcpServers": {\n    // keep unrelated server\n    "synthetic_disabled": {"command":"/usr/bin/false", "disabled": true,},\n  },\n  "synthetic_note": "keep",\n}\n')
(OUT/'fixture-path.txt').write_text(str(f)+'\n');print(json.dumps({'fixture':str(f),'identifier':ident,'synthetic_only':True}))
