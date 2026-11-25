#!/usr/bin/env python3
"""Isolated UTM lifecycle: APFS CoW clone, version-pinned manifest, bridge resolver, and safe teardown."""
from __future__ import annotations

import fcntl, os, shutil, subprocess, time
from pathlib import Path
from typing import Callable

if __package__:
    from . import utm_guard
else:
    import utm_guard

EXIT_PROTECTED_PATH_VIOLATED = utm_guard.EXIT_PROTECTED_PATH_VIOLATED
EXIT_ISOLATED_VERSION_MISMATCH = utm_guard.EXIT_ISOLATED_VERSION_MISMATCH
MANIFEST_NAME = utm_guard.ISOLATED_MANIFEST_NAME
HOT_REUSE_FORBIDDEN_SCENARIOS = frozenset({'clean-install', 'half-installed', 'beta2-upgrade'})
RESTORABLE_HOST_CONFIGS = frozenset({'hooks.json', 'config.toml'})

class IsolatedEnvError(RuntimeError):
    def __init__(self, message: str, *, returncode: int = 1):
        super().__init__(message)
        self.returncode = returncode

def _real(path: Path) -> Path:
    return Path(os.path.realpath(str(path)))

def _assert_unprotected(path: Path, home: Path | None) -> None:
    conflict = utm_guard.is_protected_overlap(path, Path.home() if home is None else home)
    if conflict is not None:
        raise IsolatedEnvError(f'refused protected path overlap: {conflict.name}', returncode=EXIT_PROTECTED_PATH_VIOLATED)

def read_isolated_manifest(isolated_root: Path) -> dict | None:
    manifest = isolated_root / MANIFEST_NAME
    if not manifest.exists() and not manifest.is_symlink():
        return None
    try:
        return utm_guard._verify_isolated_manifest(isolated_root)
    except utm_guard.ProtectedPathError as exc:
        raise IsolatedEnvError(str(exc), returncode=EXIT_PROTECTED_PATH_VIOLATED) from exc

def compute_candidate_fingerprint(executable: Path, *, bundled_runtime_hash: str = '', component_versions: dict[str, str] | None = None) -> dict:
    if not executable.is_file():
        raise IsolatedEnvError(f'candidate executable missing: {executable}')
    return {'executable_sha256': utm_guard.sha256_file(executable), 'bundled_runtime_hash': bundled_runtime_hash, 'component_versions': dict(sorted((component_versions or {}).items()))}

def verify_fingerprint_match(manifest: dict, candidate: dict) -> None:
    if manifest.get('executable_sha256') != candidate.get('executable_sha256'):
        raise IsolatedEnvError('isolated executable SHA-256 mismatch; run --teardown-isolated first', returncode=EXIT_ISOLATED_VERSION_MISMATCH)
    for key in ('bundled_runtime_hash', 'component_versions'):
        if manifest.get(key) and candidate.get(key) and manifest[key] != candidate[key]:
            raise IsolatedEnvError(f'isolated {key} mismatch; run --teardown-isolated first', returncode=EXIT_ISOLATED_VERSION_MISMATCH)

def _rewrite_shebang(entry: Path, old_str: str, new_str: str) -> bool:
    with entry.open('rb') as handle:
        if (head := handle.read(2)) != b'#!':
            return False
        first, rest = head + handle.readline(), handle.read()
    try:
        shebang = first.decode('utf-8')
    except UnicodeDecodeError:
        return False
    if old_str not in shebang:
        return False
    entry.write_bytes(shebang.replace(old_str, new_str).encode('utf-8') + rest)
    return True

def relocate_cloned_venv(old_root: Path, new_root: Path) -> int:
    old_str, new_str = str(_real(old_root)), str(_real(new_root))
    if old_str == new_str:
        return 0
    updated = 0
    for rt in sorted(new_root.glob('runtime-*')):
        if not rt.is_dir() or rt.is_symlink():
            continue
        for venv in (rt / 'venv', rt / '.venv'):
            if not venv.is_dir() or venv.is_symlink():
                continue
            cfg = venv / 'pyvenv.cfg'
            if cfg.is_file() and not cfg.is_symlink() and old_str in (text := cfg.read_text(encoding='utf-8')):
                cfg.write_text(text.replace(old_str, new_str), encoding='utf-8')
                updated += 1
            bin_dir = venv / 'bin'
            for entry in sorted(bin_dir.iterdir()) if bin_dir.is_dir() and not bin_dir.is_symlink() else ():
                if entry.is_symlink() and old_str in (link := os.readlink(entry)):
                    tmp = bin_dir / f'.{entry.name}.{os.getpid()}.reloc'
                    os.symlink(link.replace(old_str, new_str), tmp)
                    os.replace(tmp, entry)
                    updated += 1
                elif entry.is_file() and _rewrite_shebang(entry, old_str, new_str):
                    updated += 1
    return updated

def clone_components_apfs(pool_dir: Path, target_components: Path, *, home: Path | None = None, allowed_root: Path | None = None, cp_cmd: tuple[str, ...] = ('/bin/cp', '-cR'), allow_copy_fallback: bool = False) -> dict:
    if not pool_dir.is_dir() or pool_dir.is_symlink():
        raise IsolatedEnvError(f'component pool must be an existing non-symlink directory: {pool_dir}')
    _assert_unprotected(target_components, home)
    if target_components.exists():
        if not target_components.is_dir() or any(target_components.iterdir()) or allowed_root is None:
            raise IsolatedEnvError(f'target components directory already exists: {target_components}')
        utm_guard.safe_remove(target_components, allowed_root, home=home, require_manifest=True)
    target_components.parent.mkdir(parents=True, exist_ok=True)
    proc = subprocess.run([*cp_cmd, str(pool_dir), str(target_components)], capture_output=True, encoding='utf-8', check=False)
    clone_mode = 'apfs_cow'
    if proc.returncode != 0:
        if target_components.exists() and allowed_root is not None:
            utm_guard.safe_remove(target_components, allowed_root, home=home, require_manifest=True)
        if not allow_copy_fallback:
            raise IsolatedEnvError(f'APFS CoW clone failed (rc={proc.returncode}): {proc.stderr.strip()}')
        shutil.copytree(pool_dir, target_components, symlinks=True)
        clone_mode = 'copy_fallback'
    return {'clone_mode': clone_mode, 'relocated_files': relocate_cloned_venv(pool_dir, target_components), 'pool_dir': str(_real(pool_dir))}

def prepare_isolated_env(isolated_root: Path, executable: Path, *, scenario: str, home: Path | None = None, bundled_runtime_hash: str = '', component_versions: dict[str, str] | None = None, components_source: str = 'zero_cache_download', allow_reuse: bool = False) -> dict:
    _assert_unprotected(isolated_root, home)
    isolated_root.mkdir(parents=True, exist_ok=True, mode=0o700)
    candidate = compute_candidate_fingerprint(executable, bundled_runtime_hash=bundled_runtime_hash, component_versions=component_versions)
    if (existing := read_isolated_manifest(isolated_root)) is not None:
        verify_fingerprint_match(existing, candidate)
        lib = isolated_root / 'library'
        has_store = bool(existing.get('store_id')) or (lib.is_dir() and any(lib.iterdir()))
        if scenario in HOT_REUSE_FORBIDDEN_SCENARIOS and not allow_reuse and (scenario != 'clean-install' or has_store):
            raise IsolatedEnvError(f'scenario {scenario!r} cannot reuse an already-initialized isolated root')
        return existing
    extra = {**candidate, 'store_id': '', 'components_source': components_source, 'executed_scenarios': [], 'mutated_domains': [], 'host_config_backups': {}, 'created_at': int(time.time())}
    utm_guard.write_isolated_manifest(isolated_root, run_id=f'utm-{int(time.time())}-{os.getpid()}', extra=extra)
    return read_isolated_manifest(isolated_root) or extra

def update_isolated_manifest(isolated_root: Path, **updates) -> dict:
    if (state := read_isolated_manifest(isolated_root)) is None:
        raise IsolatedEnvError(f'missing isolated manifest in {isolated_root}')
    merged = {k: v for k, v in {**state, **updates}.items() if k not in ('schema', 'created_by', 'isolated_root')}
    utm_guard.write_isolated_manifest(isolated_root, run_id=str(merged.pop('run_id', f'utm-{os.getpid()}')), extra=merged)
    return read_isolated_manifest(isolated_root) or merged

def resolve_host_bridge_params(isolated_root: Path, *, runtime_config: Path | None = None) -> dict:
    cfg_path = runtime_config or (isolated_root / 'runtime.json')
    if not cfg_path.is_file():
        raise IsolatedEnvError(f'missing isolated runtime config: {cfg_path}')
    cfg = utm_guard.json_object(cfg_path.read_text(encoding='utf-8'), error_cls=IsolatedEnvError)
    pc = cfg.get('project_control') if isinstance(cfg.get('project_control'), dict) else {}
    official_data = Path(cfg['official_data']) if isinstance(cfg.get('official_data'), str) and cfg['official_data'] else (isolated_root / 'library')
    ident = official_data / 'store-identity.json'
    store_id = str(utm_guard.json_object(ident.read_text(encoding='utf-8'), error_cls=IsolatedEnvError).get('store_id') or '') if ident.is_file() and not ident.is_symlink() else ''
    instances = {}
    if isinstance(pc.get('registry_file'), str) and (reg_p := Path(pc['registry_file'])).is_file() and not reg_p.is_symlink():
        raw_agents = utm_guard.json_object(reg_p.read_text(encoding='utf-8'), error_cls=IsolatedEnvError).get('agents')
        pairs = raw_agents.items() if isinstance(raw_agents, dict) else ((m.get('instance_id'), m) for m in (raw_agents if isinstance(raw_agents, list) else ()) if isinstance(m, dict))
        instances = {m['kind']: iid for iid, m in pairs if isinstance(iid, str) and isinstance(m, dict) and isinstance(m.get('kind'), str)}
    return {'config_path': str(cfg_path), 'official_data': str(official_data), 'store_id': store_id, 'service_url': str(cfg.get('service_url') or ''), 'control_url': str(pc.get('url') or ''), 'token_file': str(cfg.get('token_file') or ''), 'management_token_file': str(pc.get('management_token_file') or ''), 'instances': instances}

def _flock_held(path: Path) -> bool:
    try:
        fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
    except FileNotFoundError:
        return False
    with os.fdopen(fd, 'rb') as handle:
        try:
            fcntl.flock(handle, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            return True
        fcntl.flock(handle, fcntl.LOCK_UN)
    return False

def teardown_isolated_env(isolated_root: Path, *, home: Path | None = None, keep_host_config: bool = False, stop_fn: Callable[[dict], None] | None = None, flock_check_fn: Callable[[Path], bool] | None = None) -> dict:
    target_home = Path.home() if home is None else home
    _assert_unprotected(isolated_root, target_home)
    sentinel = utm_guard.HostStateSentinel(target_home, protect_memlane_dir=True)
    if not isolated_root.exists():
        return {'status': 'already_absent', 'removed': False, 'restored_configs': [], 'sentinel': sentinel.verify()}
    if (state := read_isolated_manifest(isolated_root)) is None:
        raise IsolatedEnvError(f'refused teardown without isolated manifest: {isolated_root}', returncode=EXIT_PROTECTED_PATH_VIOLATED)
    if stop_fn is not None:
        stop_fn(state)
    official_data, cfg_path = isolated_root / 'library', isolated_root / 'runtime.json'
    if cfg_path.is_file() and not cfg_path.is_symlink():
        cfg = utm_guard.json_object(cfg_path.read_text(encoding='utf-8'), error_cls=IsolatedEnvError)
        if isinstance(cfg.get('official_data'), str) and cfg['official_data']:
            official_data = Path(cfg['official_data'])
    if (flock_check_fn or _flock_held)(official_data / '.service-owner'):
        raise IsolatedEnvError('service owner lock is still held; aborting teardown')
    restored: list[str] = []
    if not keep_host_config:
        root_real = _real(isolated_root)
        for orig_str, backup_str in (state.get('host_config_backups') or {}).items():
            orig, backup = Path(orig_str), Path(backup_str)
            if orig.name not in RESTORABLE_HOST_CONFIGS or orig.is_symlink() or not backup.is_file() or backup.is_symlink() or not str(_real(backup)).startswith(str(root_real) + os.sep):
                raise IsolatedEnvError(f'invalid host config backup restore target: {orig.name}')
            orig.parent.mkdir(parents=True, exist_ok=True)
            tmp = orig.parent / f'.{orig.name}.{os.getpid()}.restore'
            shutil.copyfile(backup, tmp)
            os.replace(tmp, orig)
            restored.append(orig.name)
    try:
        removed = utm_guard.safe_remove(isolated_root, isolated_root, home=target_home, require_manifest=True, allow_root_self=True)
    except utm_guard.ProtectedPathError as exc:
        raise IsolatedEnvError(str(exc), returncode=EXIT_PROTECTED_PATH_VIOLATED) from exc
    if not (sentinel_report := sentinel.verify())['ok']:
        raise IsolatedEnvError('protected host login state was violated during teardown', returncode=EXIT_PROTECTED_PATH_VIOLATED)
    return {'status': 'torn_down', 'removed': removed, 'restored_configs': restored, 'sentinel': sentinel_report}
