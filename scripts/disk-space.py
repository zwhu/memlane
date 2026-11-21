#!/usr/bin/env python3
"""Memlane development disk budget; only prune owned, reproducible artifacts."""
import argparse
import fcntl
import json
import os
from pathlib import Path
import re
import shlex
import shutil
import subprocess
import tempfile
import time

ROOT = Path(__file__).resolve().parents[1]
GIB = 1024 ** 3
KEEP = ROOT / 'build/goal-agent-batch-window-20261110-002'
RETAINED = ROOT / 'build/disk-cleanup/retained-package.txt'
DEPENDENCIES = ROOT / 'build/disk-cleanup/retained-dependencies.txt'
RECEIPT_NAME_RE = re.compile(r'^\d{8}-\d{6}\.json$')
DEFAULT_RECEIPT_RETENTION = 100


def _protect_absolute(protected, value, error_message):
    if not value:
        return
    path = Path(value).expanduser()
    if not path.is_absolute():
        raise ValueError(error_message)
    protected.append(path.resolve())


def retained_dependencies():
    """Protect explicit runtime references even when ps only shows --config.

    Resolve aliases before comparing paths. A candidate containing a dependency
    is protected as a whole: its surrounding runtime resources may be required
    too. Invalid existing configuration fails closed before any deletion.
    """
    protected = []
    if DEPENDENCIES.exists():
        protected.extend(Path(line.strip()).expanduser().resolve()
                         for line in DEPENDENCIES.read_text().splitlines() if line.strip())
    config_path = ROOT / '.local/daily/runtime.json'
    if config_path.exists():
        try:
            config = json.loads(config_path.read_text())
            protected.append(config_path.resolve())
            for key in ('official_data', 'official_source', 'official_archive', 'python',
                        'graph_assets', 'service_environment_file', 'token_file',
                        'completion_token_file', 'codex_executable', 'auth_home',
                        'opencode_plugin_dir'):
                _protect_absolute(protected, config.get(key), 'runtime dependency must be absolute')
            project_control = config.get('project_control')
            if project_control is not None:
                if not isinstance(project_control, dict):
                    raise TypeError('project_control must be an object')
                for key in ('plugin_dir', 'registry_file', 'management_token_file'):
                    _protect_absolute(protected, project_control.get(key), 'runtime dependency must be absolute')
                agent_tokens = project_control.get('agent_control_token_files')
                if agent_tokens is not None:
                    if not isinstance(agent_tokens, dict):
                        raise TypeError('agent_control_token_files must be an object')
                    for value in agent_tokens.values():
                        _protect_absolute(protected, value, 'runtime dependency must be absolute')
            environment_path = config.get('service_environment_file')
            if environment_path:
                environment = json.loads(Path(environment_path).expanduser().read_text())
                for key in ('HF_HOME', 'ENOLA_PATH', 'TIKTOKEN_CACHE_DIR'):
                    _protect_absolute(protected, environment.get(key), 'component dependency must be absolute')
        except (OSError, ValueError, TypeError, AttributeError):
            # Never include configuration contents or credentials in errors.
            raise RuntimeError('daily runtime dependencies unavailable; cleanup refused') from None
    return protected


def protected_path(path, dependencies):
    path = path.resolve()
    return any(path == dependency or path in dependency.parents or dependency in path.parents
               for dependency in dependencies)


def active_arguments():
    return subprocess.check_output(['ps', '-axo', 'pid=,args='], text=True).splitlines()


def _is_long_lived_dev_daemon(task_args):
    """Return True when a task.py invocation wraps a long-running dev.py start/setup daemon."""
    try:
        tokens = shlex.split(task_args)
    except ValueError:
        tokens = task_args.split()
    if len(tokens) < 2:
        return False
    rest = tokens[1:]
    if rest[:1] == ['--']:
        rest = rest[1:]
    if not rest:
        return False
    if 'python' in Path(rest[0]).name and len(rest) >= 2:
        script, dev_args = rest[1], rest[2:]
    else:
        script, dev_args = rest[0], rest[1:]
    if not (script == 'scripts/dev.py' or script.endswith('/scripts/dev.py')):
        return False
    action = 'start'
    skip_next = False
    for token in dev_args:
        if skip_next:
            skip_next = False
            continue
        if token in ('--config', '--component-root', '--uv'):
            skip_next = True
            continue
        if token.startswith('-'):
            continue
        action = token
        break
    return action in ('start', 'setup')


def other_tasks(processes):
    owner = os.environ.get('MEMLANE_TASK_OWNER_PID', '')
    prefixes = (str(ROOT / 'scripts/task.py'), 'scripts/task.py')
    for line in processes:
        parts = line.split(None, 2)
        if len(parts) != 3 or parts[0] == owner or 'python' not in Path(parts[1]).name:
            continue
        if not any(parts[2].startswith(prefix + ' ') for prefix in prefixes):
            continue
        if _is_long_lived_dev_daemon(parts[2]):
            continue
        return True
    return False


def prune_cleanup_receipts(log_dir, keep=DEFAULT_RECEIPT_RETENTION):
    """Keep only the latest timestamped YYYYMMDD-HHMMSS.json receipts.

    Strictly preserves retained-package.txt, retained-dependencies.txt,
    and any non-receipt files or symlinks.
    """
    log_dir = Path(log_dir)
    if not log_dir.is_dir():
        return []
    receipts = sorted(
        (p for p in log_dir.iterdir()
         if not p.is_symlink() and p.is_file() and RECEIPT_NAME_RE.match(p.name)),
        key=lambda p: p.name,
    )
    stale = receipts[:-keep] if keep > 0 else receipts
    removed = []
    for path in stale:
        path.unlink(missing_ok=True)
        removed.append(str(path))
    return removed


def busy(path, processes):
    relative = str(path.relative_to(ROOT)) if ROOT in path.parents else str(path)
    return any((str(path) in line or relative in line) and not any(s in line for s in ['disk-space.py', 'python3 -', '/bin/zsh -lc']) for line in processes)


def worktrees():
    return [Path(s[9:]) for s in subprocess.check_output(['git', '-C', str(ROOT), 'worktree', 'list', '--porcelain'], text=True).splitlines() if s.startswith('worktree ') and Path(s[9:]) != ROOT]


def size(path):
    if not path.exists(): return 0
    return int(subprocess.check_output(['du', '-sk', str(path)], text=True).split()[0]) * 1024


def prune_history():
    processes = active_arguments()
    keep = KEEP
    if RETAINED.exists():
        candidate = Path(RETAINED.read_text().strip()).resolve()
        if ROOT / 'build' in candidate.parents and candidate.is_dir(): keep = candidate
    protected = [p.resolve() for p in worktrees() + [keep]] + retained_dependencies()
    # A LaunchAgent may expose only "memlaned" in ps. Its mapped executable
    # still owns its bundle, including after a package was moved or aliased.
    mapped = subprocess.run(['/usr/sbin/lsof', '-n', '-P', '-a', '-c', 'memlane', '-d', 'txt', '-Fn'], capture_output=True, text=True)
    for line in mapped.stdout.splitlines():
        if not line.startswith('n/'): continue
        path = Path(line[1:]).resolve()
        for parent in path.parents:
            if parent.suffix == '.app' and ROOT / 'build' in parent.parents:
                protected.append(parent)
                break
    targets = [p for p in (ROOT / 'build').iterdir() if p.is_dir() and not p.is_symlink()]
    tmp = Path(tempfile.gettempdir())
    targets += [p for p in tmp.glob('memlane-*') if p.is_dir() and not p.is_symlink()]
    removed, skipped, logical, bundles = [], [], 0, []
    for target in targets:
        if protected_path(target, protected) or busy(target, processes):
            skipped.append(str(target)); continue
        # Preserve evidence/source/configuration, remove only large regenerable payloads.
        for base, dirs, files in os.walk(target, followlinks=False):
            dirs[:] = [d for d in dirs if d != '.git' and not (Path(base)/d).is_symlink()]
            for name in list(dirs):
                bundle = Path(base)/name
                if name.startswith('Memlane') and name.endswith('.app'):
                    if not busy(bundle, processes):
                        shutil.rmtree(bundle); bundles.append(str(bundle))
                    else: skipped.append(str(bundle))
                    dirs.remove(name)
            for name in files:
                p = Path(base)/name
                if p.is_symlink(): continue
                st = p.stat()
                if st.st_size >= 8 * 1024 ** 2 and p.suffix.lower() not in {'.png', '.jpg', '.jpeg', '.webp', '.gif', '.json', '.jsonl', '.md', '.html', '.svg', '.log', '.txt'}:
                    p.unlink(); removed.append(str(p)); logical += st.st_size
    return dict(removed_files=removed, removed_bundles=bundles, skipped_active=skipped, logical_bytes=logical)


def trim_caches(limit):
    protected = retained_dependencies()
    processes = active_arguments()
    # Do not race a compiler/worker from another terminal.
    if any(len(s.split(None, 2)) >= 2 and s.split(None, 2)[1].startswith(str(ROOT)) and Path(s.split(None, 2)[1]).name in {'go', 'rustc', 'cargo', 'uv'} for s in processes):
        return ['cache cleanup deferred: compiler is active']
    removed = []
    for name in ['go-build', 'cognee-target', 'cognee-service-build', 'python-worker-build', 'uv', 'swift-icon']:
        p = ROOT / '.cache' / name
        if p.is_symlink() or protected_path(p, protected) or busy(p, processes): continue
        if size(ROOT / '.cache') <= limit and shutil.disk_usage(ROOT).free >= 50*GIB: break
        if p.exists(): shutil.rmtree(p); removed.append(str(p))
    return removed


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--prune-history', action='store_true')
    parser.add_argument('--finish', action='store_true')
    args = parser.parse_args()
    before = shutil.disk_usage(ROOT).free
    report = {'free_before': before}
    if other_tasks(active_arguments()):
        report['cache_cleanup'] = ['cleanup deferred: another managed task is active']
    else:
        # Finishing a build does not finish the artifact's lifetime: another
        # managed task may still need to verify or run it. Scratch directories
        # belong to task.py; historical build payloads require explicit pruning.
        if args.prune_history: report['history'] = prune_history()
        report['cache_cleanup'] = trim_caches(8*GIB if args.finish else 10*GIB)
    report['free_after'] = shutil.disk_usage(ROOT).free
    report['minimum_free_bytes'] = 50*GIB
    if args.prune_history or args.finish:
        log = ROOT / 'build/disk-cleanup'
        deferred_noop = (
            args.finish
            and not args.prune_history
            and report.get('cache_cleanup') == ['cleanup deferred: another managed task is active']
        )
        if not deferred_noop:
            log.mkdir(parents=True, exist_ok=True)
            (log/(time.strftime('%Y%m%d-%H%M%S')+'.json')).write_text(json.dumps(report, indent=2)+'\n')
        if log.is_dir():
            prune_cleanup_receipts(log)
    print(json.dumps({k:v for k,v in report.items() if k!='history'}, ensure_ascii=False))
    if report['free_after'] < 50*GIB and not args.finish:
        raise SystemExit('磁盘不足50 GiB，已停止任务；请清理后再运行。不会删除源码、用户数据或活动目录。')

if __name__ == '__main__':
    # Serialize pruning across build/test entry points.
    with (ROOT / '.cache/disk-space.lock').open('a') as lock:
        fcntl.flock(lock, fcntl.LOCK_EX)
        main()
