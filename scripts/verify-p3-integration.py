#!/usr/bin/env python3
"""P3 合成 SQLite/协议集成检查，可另启用现有真实 Rust worker。"""
import argparse
import json
import os
from pathlib import Path
import platform
import subprocess

ROOT = Path(__file__).resolve().parents[1]
p = argparse.ArgumentParser(description=__doc__)
p.add_argument('--real-worker', action='store_true')
a = p.parse_args()
out = ROOT / 'build/p3'
out.mkdir(parents=True, exist_ok=True)
env = os.environ.copy()
if a.real_worker:
    paths = {
        'Executable': ROOT / '.cache/cognee-target/release/memlane-cognee-worker',
        'ModelDir': ROOT / '.local/models/multilingual-e5-base-int8',
        'Library': ROOT / '.local/runtimes/onnxruntime-osx-arm64-1.28.0/lib/libonnxruntime.1.28.0.dylib',
    }
    for value in paths.values():
        if not value.exists():
            raise SystemExit('existing worker/model resource unavailable: ' + str(value))
    env['MEMLANE_P3_ENGINE_CONFIG'] = json.dumps({key: str(value) for key, value in paths.items()})
cmd = ['sh', 'scripts/go.sh', 'test', './internal/store', './internal/agents', '-count=1', '-json']
r = subprocess.run(cmd, cwd=ROOT, env=env, capture_output=True, text=True, timeout=180)
name = 'integration-real-worker' if a.real_worker else 'integration-protocol'
log = out / (name + '.jsonl')
log.write_text(r.stdout)
(out / (name + '-stderr.txt')).write_text(r.stderr)
tests = {}
for line in r.stdout.splitlines():
    event = json.loads(line)
    if event.get('Test') and event['Action'] in ('pass', 'fail', 'skip'):
        tests[event['Test']] = event['Action']
status = 'passed' if r.returncode == 0 else 'failed'
if a.real_worker and tests.get('TestP3RealRustIndexRetry') != 'pass':
    status = 'failed'
report = {'status': status, 'exit_code': r.returncode, 'command': cmd,
          'environment': platform.platform(), 'synthetic_only': True,
          'agent_protocol_is_fixture': True, 'real_agent': False,
          'real_rust_worker': a.real_worker, 'tests': tests, 'log': str(log)}
path = out / (name + '.json')
path.write_text(json.dumps(report, ensure_ascii=False, indent=2) + '\n')
print(json.dumps({'status': status, 'tests': len(tests), 'report': str(path)}, ensure_ascii=False))
raise SystemExit(0 if status == 'passed' else 1)
