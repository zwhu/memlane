#!/usr/bin/env python3
"""Release packaging orchestrator for Memlane macOS Apple Silicon beta builds.

Prepares the pinned MyGo antialiased font patch via scripts/go.sh + mygo_sdk,
invokes `mygo build`, verifies bundle metadata/signature/DMG integrity, and emits
streaming SHA256SUMS.txt without disturbing sibling directories under build/.
"""
from __future__ import annotations

import argparse
from contextlib import contextmanager
import hashlib
import json
import os
from pathlib import Path
import plistlib
import shutil
import subprocess
import sys
import tempfile


ROOT = Path(__file__).resolve().parents[1]
REQUIRED_GRAPH_FILES = (
    'web/official-graph/dist/assets.json',
    'web/official-graph/dist/index.html',
    'web/official-graph/dist/app.js',
    'web/official-graph/dist/build.json',
)
CHUNK_SIZE = 1 << 20


def load_release_manifest(root: Path = ROOT) -> dict:
    cfg_path = root / 'mygo.json'
    if cfg_path.is_symlink() or not cfg_path.is_file():
        raise ValueError(f'missing mygo.json: {cfg_path}')
    with cfg_path.open('r', encoding='utf-8') as fh:
        cfg = json.load(fh)
    version = str(cfg.get('version', '')).strip()
    slug = str(cfg.get('slug', '')).strip()
    name = str(cfg.get('name', '')).strip() or 'Memlane'
    macos = cfg.get('macos') if isinstance(cfg.get('macos'), dict) else {}
    min_sys = str(macos.get('minimumSystemVersion', '')).strip()
    signing = str(macos.get('signingIdentity', '-')).strip() or '-'
    if not version or version == '0.0.1':
        raise ValueError(f'mygo.json version must be set to release version, got {version!r}')
    if not slug:
        raise ValueError('mygo.json slug is required')
    if min_sys != '14.0':
        raise ValueError(f'mygo.json macos.minimumSystemVersion expected "14.0", got {min_sys!r}')
    return {
        'name': name,
        'slug': slug,
        'version': version,
        'minimum_system_version': min_sys,
        'signing_identity': signing,
    }


def verify_graph_assets(root: Path = ROOT) -> list[str]:
    missing: list[str] = []
    for rel in REQUIRED_GRAPH_FILES:
        path = root / rel
        if path.is_symlink() or not path.is_file() or path.stat().st_size == 0:
            missing.append(rel)
    if missing:
        raise ValueError('missing or invalid embedded graph assets: ' + ', '.join(missing))
    return list(REQUIRED_GRAPH_FILES)


def sha256_file(path: Path) -> str:
    h = hashlib.sha256()
    with path.open('rb') as fh:
        for chunk in iter(lambda: fh.read(CHUNK_SIZE), b''):
            h.update(chunk)
    return h.hexdigest()


def verify_info_plist(app_path: Path, expected_version: str, expected_min_macos: str) -> dict[str, str]:
    plist_path = app_path / 'Contents' / 'Info.plist'
    if plist_path.is_symlink() or not plist_path.is_file():
        raise ValueError(f'missing bundle Info.plist: {plist_path}')
    with plist_path.open('rb') as fh:
        info = plistlib.load(fh)
    actual_ver = str(info.get('CFBundleShortVersionString', '')).strip()
    actual_min = str(info.get('LSMinimumSystemVersion', '')).strip()
    if actual_ver != expected_version:
        raise ValueError(f'CFBundleShortVersionString={actual_ver!r} != expected {expected_version!r}')
    if actual_min != expected_min_macos:
        raise ValueError(f'LSMinimumSystemVersion={actual_min!r} != expected {expected_min_macos!r}')
    return {
        'bundle_identifier': str(info.get('CFBundleIdentifier', '')),
        'short_version': actual_ver,
        'minimum_system_version': actual_min,
    }


def normalize_dmg_filename(platform_dir: Path, slug: str, name: str, version: str, arch: str) -> Path:
    canonical_name = f'{name}-{version}-{arch}.dmg'
    raw_name = f'{slug}-{version}-{arch}.dmg'
    canonical_path = platform_dir / canonical_name

    # Handle case-insensitive APFS renames safely via an intermediate temporary name.
    entries = {p.name: p for p in platform_dir.iterdir()} if platform_dir.is_dir() else {}
    if canonical_name in entries:
        return canonical_path
    if raw_name in entries:
        tmp_path = platform_dir / f'.rename-{os.getpid()}-{canonical_name}'
        os.replace(entries[raw_name], tmp_path)
        os.replace(tmp_path, canonical_path)
        return canonical_path
    raise FileNotFoundError(f'expected DMG not found in {platform_dir}: tried {canonical_name!r} and {raw_name!r}')


def write_sha256_sums(platform_dir: Path, targets: list[Path]) -> tuple[Path, dict[str, str]]:
    sums: dict[str, str] = {}
    lines: list[str] = []
    for target in targets:
        digest = sha256_file(target)
        sums[target.name] = digest
        lines.append(f'{digest}  {target.name}\n')
    out_path = platform_dir / 'SHA256SUMS.txt'
    tmp_path = platform_dir / f'.SHA256SUMS.{os.getpid()}.tmp'
    with tmp_path.open('w', encoding='utf-8') as fh:
        fh.writelines(lines)
        fh.flush()
        os.fsync(fh.fileno())
    os.replace(tmp_path, out_path)
    return out_path, sums


def _resolve_git_commit(repo_root: Path, git_ref: str) -> str:
    proc = subprocess.run(
        ['git', 'rev-parse', '--verify', f'{git_ref}^{{commit}}'],
        cwd=repo_root,
        capture_output=True,
        text=True,
        check=False,
    )
    if proc.returncode != 0:
        detail = proc.stderr.strip() or proc.stdout.strip() or f'exit {proc.returncode}'
        raise ValueError(f'cannot resolve git ref {git_ref!r}: {detail}')
    return proc.stdout.strip()


@contextmanager
def _worktree_for_ref(repo_root: Path, commit: str):
    repo_abs = repo_root.resolve()
    wt_parent = Path(tempfile.mkdtemp(prefix='memlane-release-wt-')).resolve()
    wt = wt_parent / 'repo'
    linked: list[Path] = []
    created_wt = False
    try:
        add_proc = subprocess.run(
            ['git', 'worktree', 'add', '--detach', str(wt), commit],
            cwd=repo_abs,
            capture_output=True,
            text=True,
            check=False,
        )
        if add_proc.returncode != 0:
            detail = add_proc.stderr.strip() or add_proc.stdout.strip()
            raise RuntimeError(f'git worktree add failed: {detail}')
        created_wt = True
        for name in ('.local', '.cache'):
            src = repo_abs / name
            dst = wt / name
            if src.exists() and not dst.exists() and not dst.is_symlink():
                os.symlink(src, dst)
                linked.append(dst)
        yield wt
    finally:
        # Unlink shared toolchain/cache symlinks before any worktree directory cleanup.
        for name in ('.local', '.cache'):
            dst = wt / name
            if dst.is_symlink():
                dst.unlink()
        for lp in linked:
            if lp.is_symlink():
                lp.unlink()
        if created_wt:
            subprocess.run(
                ['git', 'worktree', 'remove', '--force', str(wt)],
                cwd=repo_abs,
                capture_output=True,
                text=True,
                check=False,
            )
            subprocess.run(
                ['git', 'worktree', 'prune'],
                cwd=repo_abs,
                capture_output=True,
                text=True,
                check=False,
            )
        shutil.rmtree(wt_parent, ignore_errors=True)


def _execute_build(
    *,
    repo_root: Path,
    build_root: Path,
    out_dir_arg: str,
    platform: str,
    skip_dmg: bool,
    dry_run: bool,
    prepare_sdk_fn,
    git_ref: str = '',
    commit: str = '',
) -> tuple[int, dict]:
    manifest = load_release_manifest(build_root)
    graph_files = verify_graph_assets(build_root)

    if prepare_sdk_fn is None:
        sys.path.insert(0, str(Path(__file__).resolve().parent))
        import mygo_sdk  # noqa: PLC0415
        modfile = str(mygo_sdk.prepare(root=build_root, manifest_path=build_root / 'scripts/patches/mygo-antialiased.json'))
    else:
        modfile = str(prepare_sdk_fn())

    out_dir = Path(out_dir_arg) if out_dir_arg else repo_root / 'build' / f'release-v{manifest["version"]}'
    if not out_dir.is_absolute():
        out_dir = (repo_root / out_dir).resolve()
    platform_slug = platform.replace('/', '-')
    arch = platform.split('/', 1)[1] if '/' in platform else 'arm64'
    platform_dir = out_dir / platform_slug
    canonical_dmg_name = f'{manifest["name"]}-{manifest["version"]}-{arch}.dmg'

    cmd = [
        'sh', str(build_root / 'scripts' / 'go.sh'),
        'tool', 'mygo', 'build',
        '-platform', platform,
        '-o', str(out_dir),
        '-sign', manifest['signing_identity'],
        '-skip-notarize',
    ]
    if skip_dmg:
        cmd.append('-skip-dmg')

    if dry_run:
        res = {
            'status': 'dry_run',
            'dry_run': True,
            'manifest': manifest,
            'graph_assets': graph_files,
            'modfile': modfile,
            'out_dir': str(out_dir),
            'platform_dir': str(platform_dir),
            'command': cmd,
            'planned_app': str(platform_dir / f'{manifest["name"]}.app'),
            'planned_dmg': '' if skip_dmg else str(platform_dir / canonical_dmg_name),
            'planned_checksums': str(platform_dir / 'SHA256SUMS.txt'),
        }
        if git_ref:
            res['git_ref'] = git_ref
            res['commit'] = commit
        return 0, res

    out_dir.mkdir(parents=True, exist_ok=True)
    proc = subprocess.run(cmd, cwd=build_root, check=False)
    if proc.returncode != 0:
        res = {
            'status': 'build_failed',
            'dry_run': False,
            'returncode': proc.returncode,
            'command': cmd,
        }
        if git_ref:
            res['git_ref'] = git_ref
            res['commit'] = commit
        return proc.returncode, res

    app_path = platform_dir / f'{manifest["name"]}.app'
    plist_info = verify_info_plist(app_path, manifest['version'], manifest['minimum_system_version'])

    sign_proc = subprocess.run(['codesign', '--verify', '--deep', '--strict', str(app_path)], capture_output=True, text=True, check=False)
    if sign_proc.returncode != 0:
        raise RuntimeError(f'codesign verify failed: {sign_proc.stderr.strip() or sign_proc.stdout.strip()}')

    checksum_targets: list[Path] = []
    dmg_path_str = ''
    if not skip_dmg:
        dmg_path = normalize_dmg_filename(platform_dir, manifest['slug'], manifest['name'], manifest['version'], arch)
        verify_proc = subprocess.run(['hdiutil', 'verify', str(dmg_path)], capture_output=True, text=True, check=False)
        if verify_proc.returncode != 0:
            raise RuntimeError(f'hdiutil verify failed: {verify_proc.stderr.strip() or verify_proc.stdout.strip()}')
        dmg_path_str = str(dmg_path)
        checksum_targets.append(dmg_path)
    else:
        exe = app_path / 'Contents' / 'MacOS' / manifest['name']
        if not exe.is_file():
            exe = app_path / 'Contents' / 'MacOS' / manifest['slug']
        checksum_targets.append(exe)

    sums_path, sums = write_sha256_sums(platform_dir, checksum_targets)
    res = {
        'status': 'ok',
        'dry_run': False,
        'manifest': manifest,
        'modfile': modfile,
        'out_dir': str(out_dir),
        'platform_dir': str(platform_dir),
        'app_path': str(app_path),
        'dmg_path': dmg_path_str,
        'checksums_path': str(sums_path),
        'sha256sums': sums,
        'info_plist': plist_info,
    }
    if git_ref:
        res['git_ref'] = git_ref
        res['commit'] = commit
    return 0, res


def run_build(argv: list[str] | None = None, *, root: Path = ROOT, prepare_sdk_fn=None) -> tuple[int, dict]:
    parser = argparse.ArgumentParser(description='Build Memlane macOS arm64 release .app/.dmg and SHA256SUMS.txt.')
    parser.add_argument('--out-dir', default='', help='Output directory (defaults to build/release-v<version>).')
    parser.add_argument('--platform', default='darwin/arm64', help='Target GOOS/GOARCH (default darwin/arm64).')
    parser.add_argument('--git-ref', default='', help='Build from a clean detached git worktree at this ref (e.g. HEAD).')
    parser.add_argument('--skip-dmg', action='store_true', help='Skip DMG creation (passed to mygo build).')
    parser.add_argument('--dry-run', action='store_true', help='Validate config/assets and print planned command without building.')
    parser.add_argument('--json', action='store_true', help='Emit machine-readable JSON result.')
    args = parser.parse_args(argv)

    git_ref = args.git_ref.strip()
    if git_ref:
        commit = _resolve_git_commit(root, git_ref)
        if args.dry_run:
            return _execute_build(
                repo_root=root,
                build_root=root,
                out_dir_arg=args.out_dir,
                platform=args.platform,
                skip_dmg=args.skip_dmg,
                dry_run=True,
                prepare_sdk_fn=prepare_sdk_fn,
                git_ref=git_ref,
                commit=commit,
            )
        with _worktree_for_ref(root, commit) as wt:
            return _execute_build(
                repo_root=root,
                build_root=wt,
                out_dir_arg=args.out_dir,
                platform=args.platform,
                skip_dmg=args.skip_dmg,
                dry_run=False,
                prepare_sdk_fn=prepare_sdk_fn,
                git_ref=git_ref,
                commit=commit,
            )

    return _execute_build(
        repo_root=root,
        build_root=root,
        out_dir_arg=args.out_dir,
        platform=args.platform,
        skip_dmg=args.skip_dmg,
        dry_run=args.dry_run,
        prepare_sdk_fn=prepare_sdk_fn,
    )


def main(argv: list[str] | None = None) -> int:
    code, result = run_build(argv)
    if '--json' in (sys.argv[1:] if argv is None else argv):
        print(json.dumps(result, ensure_ascii=False, indent=2))
    else:
        print(f'Memlane release build [{result["status"]}]: version={result.get("manifest", {}).get("version", "")}')
        if result.get('dmg_path'):
            print(f'  DMG: {result["dmg_path"]}')
        if result.get('checksums_path'):
            print(f'  SHA256SUMS: {result["checksums_path"]}')
    return code


if __name__ == '__main__':
    sys.exit(main())
