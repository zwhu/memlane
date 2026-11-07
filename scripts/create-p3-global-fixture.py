#!/usr/bin/env python3
"""新建全局经验验收根，仅复制已验收的组件缓存。"""
import json, shutil, subprocess, tempfile, uuid
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
OUT = ROOT / 'build/p3-global'
OUT.mkdir(parents=True, exist_ok=True)
old = Path((ROOT / 'build/p0-engine/client-fixture-path.txt').read_text().strip())
assert old.name.startswith('memlane-client-p0-')
cfg = json.loads((old / 'runtime-config.json').read_text())
fixture = Path(tempfile.mkdtemp(prefix='memlane-p3-global-app-'))
client = fixture / 'client'
client.mkdir()
shutil.copytree(Path(cfg['root']) / 'runtime', client / 'runtime', symlinks=True)
ident = 'io.github.zwhu.memlane.global.' + uuid.uuid4().hex[:12]
cfg.update(root=str(client), service_plist=ident + '.service.plist')
(fixture / 'runtime-config.json').write_text(json.dumps(cfg, indent=2))
(fixture / 'source-info.json').write_text(json.dumps({
    'identifier': ident, 'synthetic_only': True,
    'app': str(ROOT / 'build/p3-global-app/darwin-arm64/MemlaneGlobal.app'),
}))
for name in ['repo-a', 'repo-b']:
    subprocess.run(['git', 'init', str(fixture / name)], check=True, capture_output=True)
(fixture / 'pi-agent').mkdir()
(OUT / 'fixture-path.txt').write_text(str(fixture) + '\n')
print(json.dumps({'fixture': str(fixture), 'identifier': ident, 'user_data_copied': False}))
