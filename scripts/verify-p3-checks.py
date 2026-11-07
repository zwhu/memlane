#!/usr/bin/env python3
"""保存 P3 对应的全仓检查命令、退出码及日志。"""
import json
from pathlib import Path
import subprocess

ROOT = Path(__file__).resolve().parents[1]
out = ROOT / 'build/p3'
out.mkdir(parents=True, exist_ok=True)
checks = []
for name, cmd in [
    ('go-test-all', ['sh', 'scripts/go.sh', 'test', './...']),
    ('go-vet-all', ['sh', 'scripts/go.sh', 'vet', './...']),
    ('go-race', ['env', 'CGO_ENABLED=1', 'sh', 'scripts/go.sh', 'test', '-race', './internal/store', './internal/agents', '-count=1']),
    ('diff-check', ['git', 'diff', '--check']),
    ('doc-links', ['python3', 'scripts/verify-p3-doc-links.py']),
]:
    r = subprocess.run(cmd, cwd=ROOT, capture_output=True, text=True, timeout=240)
    log = out / (name + '.txt')
    log.write_text(r.stdout + r.stderr)
    checks.append({'name': name, 'command': cmd, 'exit_code': r.returncode, 'log': str(log)})
report = {'status': 'passed' if all(c['exit_code'] == 0 for c in checks) else 'failed', 'checks': checks}
(out / 'final-checks.json').write_text(json.dumps(report, ensure_ascii=False, indent=2) + '\n')
print(json.dumps(report, ensure_ascii=False))
raise SystemExit(0 if report['status'] == 'passed' else 1)
