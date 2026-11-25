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
import time

if __package__:
    from .smoke_release_contracts import run_local_contract_smoke
    from .verify_setup_cli import check_output_privacy, json_object
else:
    from smoke_release_contracts import run_local_contract_smoke
    from verify_setup_cli import check_output_privacy, json_object


ROOT = Path(__file__).resolve().parents[1]
REQUIRED_GRAPH_DIST_FILES = (
    'index.html',
    'app.js',
    'app.css',
    'host.css',
    'app.js.LEGAL.txt',
    'COGNEE-LICENSE',
    'THIRD-PARTY-LICENSES.txt',
    'build.json',
)
REQUIRED_GRAPH_FILES = (
    'web/official-graph/dist/assets.json',
    *(f'web/official-graph/dist/{name}' for name in REQUIRED_GRAPH_DIST_FILES),
)
CHUNK_SIZE = 1 << 20
# Apache-2.0 §4 requires redistributions to carry the License and NOTICE; they are
# declared as mygo.json "resources" so mygo copies them into Contents/Resources
# before ad-hoc signing and DMG creation.
REQUIRED_LICENSE_FILES = ('LICENSE', 'NOTICE', 'THIRD_PARTY_LICENSES.md')


def verify_license_sources(root: Path = ROOT) -> list[str]:
    cfg_path = root / 'mygo.json'
    with cfg_path.open('r', encoding='utf-8') as fh:
        cfg = json.load(fh)
    declared = cfg.get('resources')
    if not isinstance(declared, list):
        declared = []
    missing_decl = [name for name in REQUIRED_LICENSE_FILES if name not in declared]
    if missing_decl:
        raise ValueError('mygo.json resources must declare license files: ' + ', '.join(missing_decl))
    invalid = [
        name for name in REQUIRED_LICENSE_FILES
        if (root / name).is_symlink() or not (root / name).is_file() or (root / name).stat().st_size == 0
    ]
    if invalid:
        raise ValueError('missing or invalid license files: ' + ', '.join(invalid))
    return list(REQUIRED_LICENSE_FILES)


def verify_bundled_licenses(app_path: Path, source_root: Path) -> dict[str, str]:
    resources = app_path / 'Contents' / 'Resources'
    digests: dict[str, str] = {}
    for name in REQUIRED_LICENSE_FILES:
        bundled = resources / name
        if bundled.is_symlink() or not bundled.is_file():
            raise ValueError(f'license file not bundled in app: Contents/Resources/{name}')
        digest = sha256_file(bundled)
        expected = sha256_file(source_root / name)
        if digest != expected:
            raise ValueError(f'bundled license file differs from source: {name}')
        digests[name] = digest
    return digests


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
    if min_sys != '26.0':
        raise ValueError(f'mygo.json macos.minimumSystemVersion expected "26.0", got {min_sys!r}')
    return {
        'name': name,
        'slug': slug,
        'version': version,
        'minimum_system_version': min_sys,
        'signing_identity': signing,
    }


def verify_graph_assets(root: Path = ROOT) -> list[str]:
    dist_dir = root / 'web' / 'official-graph' / 'dist'
    if dist_dir.is_symlink() or not dist_dir.is_dir():
        raise ValueError(f'missing or invalid embedded graph assets directory: {dist_dir}')
    missing: list[str] = []
    for rel in REQUIRED_GRAPH_FILES:
        path = root / rel
        if path.is_symlink() or not path.is_file() or path.stat().st_size == 0:
            missing.append(rel)
    if missing:
        raise ValueError('missing or invalid embedded graph assets: ' + ', '.join(missing))

    assets_path = dist_dir / 'assets.json'
    try:
        with assets_path.open('r', encoding='utf-8') as fh:
            manifest = json.load(fh)
    except (OSError, ValueError) as exc:
        raise ValueError(f'invalid graph assets.json: {exc}') from exc

    if not isinstance(manifest, dict) or manifest.get('protocol') != 1 or not str(manifest.get('upstream', '')).strip():
        raise ValueError('invalid graph assets.json protocol or upstream metadata')
    files_map = manifest.get('files')
    if not isinstance(files_map, dict) or not files_map:
        raise ValueError('graph assets.json missing files digest map')

    required_set = set(REQUIRED_GRAPH_DIST_FILES)
    missing_entries = sorted(required_set - set(files_map))
    if missing_entries:
        raise ValueError('graph assets.json missing required entries: ' + ', '.join(missing_entries))

    for name, expected_digest in sorted(files_map.items()):
        if not isinstance(name, str) or not name or Path(name).name != name or '/' in name or '\\' in name:
            raise ValueError(f'invalid graph asset filename in assets.json: {name!r}')
        if not isinstance(expected_digest, str) or len(expected_digest) != 64:
            raise ValueError(f'invalid sha256 digest in assets.json for {name}')
        target = dist_dir / name
        if target.is_symlink() or not target.is_file() or target.stat().st_size == 0:
            raise ValueError(f'missing or invalid graph asset listed in assets.json: {name}')
        actual_digest = sha256_file(target)
        if actual_digest != expected_digest:
            raise ValueError(f'graph asset sha256 mismatch for {name}: got {actual_digest}, expected {expected_digest}')

    build_path = dist_dir / 'build.json'
    try:
        with build_path.open('r', encoding='utf-8') as fh:
            build_meta = json.load(fh)
    except (OSError, ValueError) as exc:
        raise ValueError(f'invalid graph build.json: {exc}') from exc
    missing_licenses = build_meta.get('missingLicenseTexts') if isinstance(build_meta, dict) else None
    if not isinstance(missing_licenses, list) or len(missing_licenses) > 0:
        raise ValueError(f'graph build.json has unresolved missingLicenseTexts: {missing_licenses!r}')

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
        'executable': str(info.get('CFBundleExecutable', '')),
    }


def verify_packaged_first_store(executable: Path) -> None:
    with tempfile.TemporaryDirectory(prefix='memlane-release-check-') as work_dir:
        probe = subprocess.run(
            [str(executable), '--check-first-store-resources', sys.executable],
            cwd=work_dir,
            env={'HOME': work_dir, 'TMPDIR': work_dir, 'PATH': os.defpath},
            capture_output=True, text=True, timeout=45, check=False,
        )
    if probe.returncode != 0:
        raise RuntimeError(f'packaged first-store check failed: {probe.stderr.strip() or probe.stdout.strip()}')


def verify_packaged_cli_doctor(executable: Path, expected_version: str = '') -> dict:
    with tempfile.TemporaryDirectory(prefix='memlane-cli-doctor-check-') as temporary:
        work_dir = str(Path(temporary).resolve())
        started = time.monotonic()
        try:
            probe = subprocess.run(
                [str(executable.absolute()), 'doctor', '--json'],
                cwd=work_dir, env={'HOME': work_dir, 'TMPDIR': work_dir, 'PATH': os.defpath},
                capture_output=True, encoding='utf-8', timeout=5, check=False,
            )
        except subprocess.TimeoutExpired:
            raise RuntimeError('packaged doctor timed out') from None
        except (OSError, UnicodeError):
            raise RuntimeError('could not execute packaged doctor or decode its output') from None
        elapsed = time.monotonic() - started
        if elapsed >= 5.0:
            raise RuntimeError('packaged doctor took at least 5 seconds')
        if probe.stderr != '':
            raise RuntimeError('packaged doctor stderr must be empty')
        if probe.returncode != 10:
            raise RuntimeError(f'packaged doctor exited {probe.returncode}, expected 10')
        payload = json_object(probe.stdout)
        user = os.environ.get('USER')
        check_output_privacy(probe.stdout, payload, (temporary, work_dir, f'/Users/{user}' if user else ''))
        if (type(payload.get('schema_version')) is not int or payload['schema_version'] != 1
                or payload.get('command') != 'doctor' or type(payload.get('exit_code')) is not int
                or payload['exit_code'] != 10 or payload.get('status') != 'action_required'
                or payload.get('ok') is not False):
            raise ValueError('packaged doctor returned an unexpected JSON envelope')
        app = payload.get('app', {})
        if not isinstance(app, dict):
            raise ValueError('packaged doctor returned invalid app metadata')
        if expected_version and app.get('version') != expected_version:
            raise ValueError('packaged doctor app version does not match the manifest')
        return {
            'status': 'passed', 'exit_code': 10, 'elapsed_s': round(elapsed, 3),
            'schema_version': 1, 'bundled_runtime_hash': app.get('bundled_runtime_hash', ''),
            'version': app.get('version', ''),
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
    skip_contract_smoke: bool = False,
    dry_run: bool,
    prepare_sdk_fn,
    git_ref: str = '',
    commit: str = '',
) -> tuple[int, dict]:
    manifest = load_release_manifest(build_root)
    graph_files = verify_graph_assets(build_root)
    license_files = verify_license_sources(build_root)

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
            'license_files': license_files,
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
    bundled_licenses = verify_bundled_licenses(app_path, build_root)

    sign_proc = subprocess.run(['codesign', '--verify', '--deep', '--strict', str(app_path)], capture_output=True, text=True, check=False)
    if sign_proc.returncode != 0:
        raise RuntimeError(f'codesign verify failed: {sign_proc.stderr.strip() or sign_proc.stdout.strip()}')
    executable_name = plist_info['executable']
    if executable_name in ('', '.', '..') or Path(executable_name).name != executable_name:
        raise ValueError('invalid CFBundleExecutable')
    executable = app_path / 'Contents' / 'MacOS' / executable_name
    verify_packaged_first_store(executable)
    cli_doctor = verify_packaged_cli_doctor(executable, expected_version=manifest['version'])
    if skip_contract_smoke:
        contract_smoke = {'status': 'skipped', 'returncode': 0}
    else:
        smoke_code, contract_smoke = run_local_contract_smoke(
            executable,
            expected_version=manifest['version'],
            repo_root=build_root,
        )
        if smoke_code != 0:
            raise RuntimeError(
                f'local contract smoke failed (exit {smoke_code}): {contract_smoke.get("status", "failed")}'
            )

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
        checksum_targets.append(executable)

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
        'bundled_licenses': bundled_licenses,
        'packaged_first_store': 'passed',
        'packaged_cli_doctor': cli_doctor,
        'contract_smoke': contract_smoke,
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
    parser.add_argument('--skip-contract-smoke', action='store_true',
                        help='Skip local Level 0 contract smoke (recorded as skipped; rejected by release gate).')
    parser.add_argument('--dry-run', action='store_true', help='Validate config/assets and print planned command without building.')
    parser.add_argument('--json', action='store_true', help='Emit machine-readable JSON result.')
    args = parser.parse_args(argv)

    git_ref = args.git_ref.strip()
    if git_ref:
        commit = _resolve_git_commit(root, git_ref)
        with _worktree_for_ref(root, commit) as wt:
            return _execute_build(
                repo_root=root,
                build_root=wt,
                out_dir_arg=args.out_dir,
                platform=args.platform,
                skip_dmg=args.skip_dmg,
                skip_contract_smoke=args.skip_contract_smoke,
                dry_run=args.dry_run,
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
        skip_contract_smoke=args.skip_contract_smoke,
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
