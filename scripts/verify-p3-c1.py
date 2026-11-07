#!/usr/bin/env python3
"""P3-C1 临时库与正式服务验收；真实 Agent/worker 需显式 --real。"""
import argparse
import json
import os
from pathlib import Path
import subprocess

ROOT = Path(__file__).resolve().parents[1]
p = argparse.ArgumentParser(description=__doc__)
p.add_argument('--real', action='store_true')
p.add_argument('--short-path', action='store_true', help='真实服务使用 Finder/LaunchAgent 的短 PATH')
p.add_argument('--collect-log', type=Path, help='仅汇总已完成的 go test JSONL，不再调用模型')
a = p.parse_args()
out = ROOT / 'build/p3-c1'
out.mkdir(parents=True, exist_ok=True)
env = os.environ.copy()
if a.real:
    resources = {
        'Executable': ROOT / '.cache/cognee-target/release/memlane-cognee-worker',
        'ModelDir': ROOT / '.local/models/multilingual-e5-base-int8',
        'Library': ROOT / '.local/runtimes/onnxruntime-osx-arm64-1.28.0/lib/libonnxruntime.1.28.0.dylib',
    }
    for path in resources.values():
        if not path.exists():
            raise SystemExit('existing resource unavailable: ' + str(path))
    env['MEMLANE_C1_REAL'] = '1'
    if a.short_path:
        env['MEMLANE_C1_SHORT_PATH'] = '1'
    env['MEMLANE_P3_ENGINE_CONFIG'] = json.dumps({k: str(v) for k, v in resources.items()})
cmd = ['sh', 'scripts/go.sh', 'test', './internal/store', './internal/daemon', '-run', 'TestMerge', '-count=1', '-json']
if a.collect_log:
    data = a.collect_log.read_text()
    events = [json.loads(line) for line in data.splitlines()]
    failed = any(e['Action'] == 'fail' for e in events)
    complete = all(any(e['Action'] == 'pass' and not e.get('Test') and e['Package'] == pkg for e in events)
                   for pkg in ('github.com/zwhu/memlane/internal/store', 'github.com/zwhu/memlane/internal/daemon'))
    r = subprocess.CompletedProcess(cmd, 0 if complete and not failed else 1, data, '')
else:
    r = subprocess.run(cmd, cwd=ROOT, env=env, capture_output=True, text=True, timeout=240)
name = ('real-service-short-path' if a.short_path else 'real-service') if a.real else 'store-ipc'
log = out / (name + '.jsonl')
log.write_text(r.stdout + r.stderr)
tests = {}
real = None
real_output = []
for line in r.stdout.splitlines():
    e = json.loads(line)
    if e.get('Test') and e['Action'] in ('pass', 'fail', 'skip'):
        tests[e['Test']] = e['Action']
    if e.get('Test') == 'TestMergeRealServiceTraexAndRust':
        real_output.append(e.get('Output', ''))
# go test JSONL can split a long Output line across several events.
joined = ''.join(real_output)
if 'P3_C1_EVIDENCE ' in joined:
    real = json.loads(joined.split('P3_C1_EVIDENCE ', 1)[1].split('\n', 1)[0])
passed = r.returncode == 0
if a.real:
    passed = passed and tests.get('TestMergeRealServiceTraexAndRust') == 'pass' and real is not None
report = {'status': 'passed' if passed else 'failed', 'command': cmd, 'exit_code': r.returncode,
          'synthetic_only': True, 'real_agent': a.real, 'tests': tests, 'evidence': real, 'log': str(log)}
path = out / (name + '.json')
path.write_text(json.dumps(report, ensure_ascii=False, indent=2) + '\n')
print(json.dumps({'status': report['status'], 'report': str(path), 'tests': tests}, ensure_ascii=False))
raise SystemExit(0 if passed else 1)
