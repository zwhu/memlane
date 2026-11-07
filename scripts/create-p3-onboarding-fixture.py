#!/usr/bin/env python3
"""I 实际 App 隔离根；仅复用已验证引擎/模型组件。"""
import json, shutil, subprocess, tempfile, uuid
from pathlib import Path
ROOT = Path(__file__).resolve().parents[1]
OUT = ROOT / 'build/p3-onboarding'
OUT.mkdir(parents=True, exist_ok=True)
old = Path((ROOT / 'build/p0-engine/client-fixture-path.txt').read_text().strip())
assert old.name.startswith('memlane-client-p0-')
cfg = json.loads((old / 'runtime-config.json').read_text())
fixture = Path(tempfile.mkdtemp(prefix='memlane-p3-onboarding-app-'))
client = fixture / 'client'; client.mkdir()
shutil.copytree(Path(cfg['root']) / 'runtime', client / 'runtime', symlinks=True)
ident = 'io.github.zwhu.memlane.onboarding.' + uuid.uuid4().hex[:12]
cfg.update(root=str(client), service_plist=ident + '.service.plist')
(fixture / 'runtime-config.json').write_text(json.dumps(cfg, indent=2))
(fixture / 'source-info.json').write_text(json.dumps({'identifier': ident, 'synthetic_only': True, 'app': str(ROOT / 'build/p3-onboarding-app/darwin-arm64/MemlaneOnboarding.app')}))
for name in ['repo-a', 'repo-b']:
    subprocess.run(['git', 'init', str(fixture / name)], check=True, capture_output=True)
(fixture / 'pi-agent').mkdir()
(fixture/'repo-a/README.md').write_text('本项目使用 pnpm 管理依赖，依赖变更提交 pnpm-lock.yaml。\n',encoding='utf-8')
(fixture/'repo-a/conventions.md').write_text('本项目的 TypeScript 模块使用明确类型，保留清晰函数边界。\n',encoding='utf-8')
(OUT / 'fixture-path.txt').write_text(str(fixture) + '\n')
print(json.dumps({'fixture': str(fixture), 'identifier': ident, 'user_data_copied': False}))
