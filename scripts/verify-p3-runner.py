#!/usr/bin/env python3
"""真实安装 traex/Codex 的 P3 隔离实验；失败保留，绝不由夹具替代。"""
import argparse
import json
from pathlib import Path
import subprocess

ROOT = Path(__file__).resolve().parents[1]
p = argparse.ArgumentParser(description=__doc__)
p.add_argument('--mode', choices=['normal', 'cancel'], required=True)
p.add_argument('--agent',choices=['traex','codex'],default='traex')
p.add_argument('--confirm-synthetic-merge',action='store_true')
a = p.parse_args()
if a.confirm_synthetic_merge and a.mode!='normal': p.error('合成确认仅适用于 normal 模式')
out = ROOT / 'build/p3'
out.mkdir(parents=True, exist_ok=True)
cmd=[str(out / 'runner-check'), '--agent',a.agent,'--mode', a.mode]
if a.confirm_synthetic_merge: cmd.append('--confirm-synthetic-merge')
r = subprocess.run(cmd, capture_output=True, text=True, timeout=140)
try:
    report = json.loads(r.stdout)
except json.JSONDecodeError:
    report = {'status': 'failed', 'real_agent': True, 'error': 'probe did not return JSON', 'stderr': r.stderr[:2048]}
report['probe_exit_code'] = r.returncode
path = out / (a.agent+'-real-' + ('apply' if a.confirm_synthetic_merge else a.mode) + '.json')
path.write_text(json.dumps(report, ensure_ascii=False, indent=2) + '\n')
print(json.dumps({'status': report['status'], 'report': str(path), 'error': report.get('error')}, ensure_ascii=False))
raise SystemExit(r.returncode)
