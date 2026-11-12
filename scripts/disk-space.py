#!/usr/bin/env python3
"""Memlane development disk budget; only prune owned, reproducible artifacts."""
import argparse
import fcntl
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import time

ROOT = Path(__file__).resolve().parents[1]
GIB = 1024 ** 3
KEEP = ROOT / 'build/goal-agent-batch-window-20261110-002'
RETAINED = ROOT / 'build/disk-cleanup/retained-package.txt'
DEPENDENCIES = ROOT / 'build/disk-cleanup/retained-dependencies.txt'


def active_arguments():
    return subprocess.check_output(['ps', '-axo', 'pid=,args='], text=True).splitlines()


def other_tasks(processes):
    owner = os.environ.get('MEMLANE_TASK_OWNER_PID', '')
    return any(len(parts := line.split(None, 2)) == 3 and parts[0] != owner and 'python' in Path(parts[1]).name and any(parts[2].startswith(prefix+' ') for prefix in (str(ROOT / 'scripts/task.py'), 'scripts/task.py')) for line in processes)


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
    protected = worktrees() + [keep]
    if DEPENDENCIES.exists():
        for line in DEPENDENCIES.read_text().splitlines():
            if not line.strip(): continue
            dependency = Path(line.strip()).resolve()
            if ROOT / 'build' in dependency.parents:
                protected.append(dependency)
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
    targets = [p for p in (ROOT / 'build').iterdir() if p.is_dir() and not p.is_symlink() and not any(p == x or p in x.parents for x in protected)]
    tmp = Path(tempfile.gettempdir())
    targets += [p for p in tmp.glob('memlane-*') if p.is_dir() and not p.is_symlink()]
    removed, skipped, logical, bundles = [], [], 0, []
    for target in targets:
        if busy(target, processes):
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
    processes = active_arguments()
    # Do not race a compiler/worker from another terminal.
    if any(len(s.split(None, 2)) >= 2 and s.split(None, 2)[1].startswith(str(ROOT)) and Path(s.split(None, 2)[1]).name in {'go', 'rustc', 'cargo', 'uv'} for s in processes):
        return ['cache cleanup deferred: compiler is active']
    removed = []
    for name in ['go-build', 'cognee-target', 'cognee-service-build', 'python-worker-build', 'uv', 'swift-icon']:
        p = ROOT / '.cache' / name
        if p.is_symlink() or busy(p, processes): continue
        if size(ROOT / '.cache') <= limit and shutil.disk_usage(ROOT).free >= 100*GIB: break
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
    report['minimum_free_bytes'] = 100*GIB
    if args.prune_history or args.finish:
        log = ROOT / 'build/disk-cleanup'
        log.mkdir(exist_ok=True)
        (log/(time.strftime('%Y%m%d-%H%M%S')+'.json')).write_text(json.dumps(report, indent=2)+'\n')
    print(json.dumps({k:v for k,v in report.items() if k!='history'}, ensure_ascii=False))
    if report['free_after'] < 100*GIB and not args.finish:
        raise SystemExit('磁盘不足100 GiB，已停止任务；请清理后再运行。不会删除源码、用户数据或活动目录。')

if __name__ == '__main__':
    # Serialize pruning across build/test entry points.
    with (ROOT / '.cache/disk-space.lock').open('a') as lock:
        fcntl.flock(lock, fcntl.LOCK_EX)
        main()
