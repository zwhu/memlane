#!/usr/bin/env python3
"""Run the Memlane change, full, and release verification gates."""
from __future__ import annotations

import argparse
from concurrent.futures import ThreadPoolExecutor
import json
import os
from pathlib import Path
import plistlib
import shutil
import subprocess
import sys
import time

ROOT = Path(__file__).resolve().parents[1]
GO = ('sh', 'scripts/go.sh')
GO_ALL = {
    'go.mod', 'go.sum', 'scripts/go.sh', 'scripts/mygo_sdk.py', 'scripts/patches/mygo-antialiased.json',
    'runtime_assets.go', 'arch_guard_test.go', 'lint_gate_test.go',
}


def collect_git_changes(root, *, staged=False, base_ref=''):
    def git(*args):
        raw = subprocess.check_output(['git', *args], cwd=root)
        return os.fsdecode(raw).rstrip('\0').split('\0') if raw else []

    comparison = ['--cached'] if staged else [base_ref or 'HEAD']
    fields = iter(git('diff', '--name-status', '--no-renames', '-z', *comparison, '--'))
    changes = {next(fields): status for status in fields}
    if not staged:
        changes.update(dict.fromkeys(git('ls-files', '--others', '--exclude-standard', '-z'), 'A'))
    return changes


def verification(name, *command, env=None, skip=None):
    step = {'name': name, 'command': list(command)}
    if env:
        step['env'] = env
    if skip:
        step.update(status='skipped', reason=skip)
    return step


def pi_test():
    node, pi = shutil.which('node'), shutil.which('pi')
    if node and pi:
        package = Path(os.path.realpath(pi)).parent.parent
        if (package / 'dist/core/extensions/loader.js').is_file():
            return verification('pi_extension', node, 'scripts/test_pi_extension.mjs', str(package))
    return verification('pi_extension', skip='node or installed pi extension loader unavailable')


def app_executable(value):
    path = Path(value).expanduser().resolve()
    if path.suffix != '.app' or not path.is_dir():
        return path
    with (path / 'Contents/Info.plist').open('rb') as handle:
        name = plistlib.load(handle).get('CFBundleExecutable', '')
    if not name or name in {'.', '..'} or Path(name).name != name:
        raise ValueError('invalid CFBundleExecutable')
    return path / 'Contents/MacOS' / name


def build_plan(args, root):
    changes = {}
    if args.mode in {'changed', 'staged'}:
        changes = collect_git_changes(root, staged=args.staged, base_ref=args.changed)
    paths = set(changes)
    forbidden = sorted(path for path, status in changes.items()
                       if status != 'D' and path.startswith('docs/process/') and path != 'docs/process/README.md')
    reasons = sorted(path for path in paths if path in GO_ALL or path.startswith(('engine/', 'engines/cognee/')))
    full = args.mode in {'full', 'release'}
    broad = full or args.pre_push
    engine = any(path.startswith('engines/cognee/') and path.endswith('.py') for path in paths)
    scripts = any(path.startswith('scripts/') and path.endswith('.py') for path in paths)
    ui = any(path.startswith(('app/', 'kit/ui/')) and path.endswith('.go')
             for path in paths)
    docs = any(path.startswith('docs/') and path.endswith('.md') for path in paths)
    pi_changed = any(path.startswith('engine/agents/pi/') or path == 'scripts/test_pi_extension.mjs' for path in paths)
    packages = sorted({
        './' + str(Path(path).parent) if Path(path).parent != Path('.') else '.'
        for path in paths if path.endswith('.go') and any((root / Path(path).parent).glob('*.go'))
    })
    if full or reasons:
        packages = ['./...']

    def unittest_step(name, directory, pattern='test_*.py', env=None):
        return verification(name, args.python, '-m', 'unittest', 'discover', '-s', directory, '-p', pattern, env=env)

    guard_args = []
    if args.staged:
        guard_args.append('--staged')
    elif args.changed:
        guard_args.extend(['--base', args.changed])
    for flag, values in (('--lock', args.lock), ('--allow', args.allow), ('--allow-new-py', args.allow_new_py)):
        for value in values:
            guard_args.extend([flag, value])

    static, go, suites = [], [], []
    if broad or engine or scripts or ui or docs or paths & {'lint_gate_test.go', '.githooks/pre-commit'}:
        static.extend([verification('lint', args.python, 'scripts/lint.py'),
                       unittest_step('lint_tests', 'scripts', 'test_lint.py')])
    if full or changes or guard_args:
        static.append(verification('diff_guard', args.python, 'scripts/diff_guard.py', *guard_args))
    if packages:
        go.extend([verification('go_vet', *GO, 'vet', *packages),
                   verification('go_test', *GO, 'test', '-count=1', *packages)])
    if args.race_pkg:
        go.append(verification('go_race', *GO, 'test', '-race', '-count=1', *sorted(set(args.race_pkg)),
                               env={'CGO_ENABLED': '1'}))
    if broad or engine:
        suites.append(unittest_step('cognee_tests', 'engines/cognee', env={'PYTHONPATH': '.'}))
    if broad or scripts:
        suites.append(unittest_step('scripts_tests', 'scripts'))
    if full or pi_changed:
        suites.append(pi_test())

    stages = [{'stage': number, 'name': name, 'checks': checks}
              for number, name, checks in ((1, 'static', static), (2, 'go', go), (3, 'suites', suites)) if checks]
    if args.release:
        env = {'MEMLANE_RELEASE_SMOKE_BIN': str(app_executable(args.app))} if args.app else {}
        smoke = verification('release_contract_smoke', *GO, 'test', '-count=1', '-run',
                             'TestReleaseContractSmoke', './cmd/memlane', env=env)
        stages.append({'stage': 4, 'name': 'release', 'checks': [smoke]})
    return {
        'changed_files': sorted(paths), 'deleted_files': sorted(path for path in paths if changes[path] == 'D'),
        'forbidden_paths': forbidden, 'no_changed_files': not changes and not broad,
        'escalated_to_full': bool(reasons), 'escalation_reasons': reasons, 'stages': stages,
    }


def run_check(check, root):
    if check.get('status') == 'skipped':
        return check
    started = time.monotonic()
    env = dict(os.environ, PYTHONDONTWRITEBYTECODE='1')
    env.update(check.get('env', {}))
    try:
        run = subprocess.run(check['command'], cwd=root, env=env, text=True, errors='replace',
                             stdout=subprocess.PIPE, stderr=subprocess.STDOUT, check=False)
        code, output = run.returncode, run.stdout
    except OSError as exc:
        code, output = 1, str(exc)
    return dict(check, status='passed' if code == 0 else 'failed', returncode=code, output=output,
                elapsed_s=round(time.monotonic() - started, 3))


def execute(stages, root, jobs, json_output):
    code = 0
    for stage in stages:
        checks = stage['checks']
        if code:
            stage['checks'] = [dict(step, status='skipped', reason='earlier stage failed') for step in checks]
            continue
        if stage['name'] == 'suites' and jobs > 1:
            with ThreadPoolExecutor(max_workers=min(jobs, len(checks))) as pool:
                stage['checks'] = list(pool.map(lambda check: run_check(check, root), checks))
        else:
            stage['checks'] = [run_check(check, root) for check in checks]
        for check in stage['checks']:
            if not json_output:
                print(f"Stage {stage['stage']} {check['name']} {check['status']}", flush=True)
                if check.get('output'):
                    print(check['output'].rstrip(), flush=True)
                if check.get('reason'):
                    print(check['reason'], flush=True)
            code = code or check.get('returncode', 0)
    return code


def parse_args(argv):
    parser = argparse.ArgumentParser(description=__doc__)
    modes = parser.add_mutually_exclusive_group()
    modes.add_argument('--changed', nargs='?', const='', metavar='BASE_REF')
    for flag in ('pre-push', 'full', 'release'):
        modes.add_argument('--' + flag, action='store_true')
    parser.add_argument('--staged', action='store_true')
    for flag in ('race-pkg', 'lock', 'allow', 'allow-new-py'):
        parser.add_argument('--' + flag, action='append', default=[])
    parser.add_argument('--app', default='')
    parser.add_argument('--jobs', type=int, default=max(1, min(6, (os.cpu_count() or 4) // 2)))
    parser.add_argument('--dry-run', action='store_true')
    parser.add_argument('--json', dest='json_output', action='store_true')
    args = parser.parse_args(argv)
    if args.jobs < 1:
        parser.error('--jobs must be >= 1')
    if args.staged and (args.changed or args.pre_push or args.full or args.release):
        parser.error('--staged can only be combined with --changed without BASE_REF')
    if args.app and not args.release:
        parser.error('--app requires --release')
    for mode in ('staged', 'pre_push', 'full', 'release'):
        if getattr(args, mode):
            args.mode = mode.replace('_', '-')
            return args
    if args.changed is None:
        parser.error('one of --changed, --staged, --pre-push, --full, --release is required')
    args.mode = 'changed'
    return args


def main(argv=None, *, root=ROOT):
    argv = list(sys.argv[1:] if argv is None else argv)
    args = parse_args(argv)
    if not args.dry_run and os.environ.get('MEMLANE_DISK_TASK') != '1':
        return subprocess.call([sys.executable, 'scripts/task.py', '--', sys.executable, 'scripts/check.py', *argv],
                               cwd=root)
    started = time.monotonic()
    python = root / '.local/cognee-python/bin/python'
    args.python = str(python) if python.is_file() and os.access(python, os.X_OK) else sys.executable
    try:
        plan = build_plan(args, root)
    except (OSError, ValueError, subprocess.CalledProcessError) as exc:
        plan = {'stages': [], 'forbidden_paths': [], 'error': str(exc)}
    if plan['forbidden_paths']:
        plan['error'] = 'forbidden process artifact paths: ' + ', '.join(plan['forbidden_paths'])
    code = 1 if 'error' in plan else 0
    if not code and not args.dry_run:
        code = execute(plan['stages'], root, args.jobs, args.json_output)
    receipt = dict(plan, schema_version=1, mode=args.mode, dry_run=args.dry_run, ok=code == 0, exit_code=code,
                   jobs=args.jobs, elapsed_s=round(time.monotonic() - started, 3))
    if args.json_output:
        print(json.dumps(receipt, ensure_ascii=True, indent=2))
    elif 'error' in plan:
        print(plan['error'], file=sys.stderr)
    elif args.dry_run:
        for stage in plan['stages']:
            for check in stage['checks']:
                description = check.get('reason') or ' '.join(check['command'])
                print(f"Stage {stage['stage']} {check['name']} {description}")
    else:
        print(f'check.py --{args.mode}: {"PASSED" if code == 0 else "FAILED"} (exit {code})')
    return code


if __name__ == '__main__':
    raise SystemExit(main())
