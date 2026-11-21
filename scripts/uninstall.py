#!/usr/bin/env python3
"""Safe Memlane uninstaller (dry-run by default).

Removes Memlane-owned components, LaunchAgent, and Memlane entries in supported
Agent host configs (Codex, Antigravity, pi, TraeX) with timestamped backups,
while preserving memory store data and configs unless explicitly confirmed.
"""
from __future__ import annotations

import argparse
import fcntl
import json
import os
import re
import shutil
import subprocess
import sys
import time
from pathlib import Path


CODEX_SECTION_RE = re.compile(r'^\s*\[([^\[\]]+)\]\s*(?:#.*)?$')
TOML_KV_RE = re.compile(r'^(\s*)([A-Za-z0-9_.-]+|"[^"]+"|\'[^\']+\')(\s*=.*)$')


def _now_stamp() -> str:
    return time.strftime('%Y%m%d-%H%M%S')


def _unquote_toml_key(raw: str) -> str:
    s = raw.strip()
    if len(s) >= 2 and ((s[0] == '"' and s[-1] == '"') or (s[0] == "'" and s[-1] == "'")):
        return s[1:-1]
    return s


def _split_toml_table_path(header: str) -> list[str]:
    parts: list[str] = []
    cur: list[str] = []
    quote = ''
    escaped = False
    for ch in header.strip():
        if escaped:
            cur.append(ch)
            escaped = False
            continue
        if ch == '\\' and quote == '"':
            cur.append(ch)
            escaped = True
            continue
        if quote:
            cur.append(ch)
            if ch == quote:
                quote = ''
            continue
        if ch in ('"', "'"):
            quote = ch
            cur.append(ch)
        elif ch == '.':
            parts.append(_unquote_toml_key(''.join(cur)))
            cur = []
        else:
            cur.append(ch)
    if cur:
        parts.append(_unquote_toml_key(''.join(cur)))
    return parts


def _is_safe_under(target: Path, anchor: Path) -> bool:
    """Reject target if target, anchor, or any segment between them is a symlink or escapes anchor."""
    if anchor.is_symlink():
        return False
    try:
        rel = target.relative_to(anchor)
    except ValueError:
        return False
    cur = anchor
    for part in rel.parts:
        cur = cur / part
        if cur.is_symlink():
            return False
    try:
        anchor_res = anchor.resolve(strict=False)
        target_res = target.resolve(strict=False)
        target_res.relative_to(anchor_res)
    except ValueError:
        return False
    return True


def _is_safe_standalone(target: Path) -> bool:
    if target.is_symlink():
        return False
    parent = target.parent
    return not parent.is_symlink()


def _check_service_owner_locks(runtime_dir: Path, skipped: list[str]) -> list[str]:
    """Probe candidate .service-owner files via non-blocking flock."""
    candidates: list[Path] = []
    if _is_safe_under(runtime_dir, runtime_dir):
        candidates.append(runtime_dir / '.service-owner')
        candidates.append(runtime_dir / 'official-store' / '.service-owner')
        cfg_path = runtime_dir / 'runtime.json'
        if cfg_path.is_file() and _is_safe_under(cfg_path, runtime_dir):
            try:
                cfg = json.loads(cfg_path.read_text(encoding='utf-8'))
            except (OSError, ValueError):
                cfg = {}
            if isinstance(cfg, dict):
                for key in ('root', 'official_data', 'data_dir'):
                    raw = cfg.get(key)
                    if isinstance(raw, str) and raw and os.path.isabs(raw):
                        p = Path(raw)
                        if _is_safe_standalone(p):
                            candidates.append(p / '.service-owner')
                        else:
                            skipped.append(f'symlink-rejected: {p}')

    seen: set[Path] = set()
    locked: list[str] = []
    for lock_path in candidates:
        if lock_path in seen:
            continue
        seen.add(lock_path)
        if lock_path.is_symlink() or lock_path.parent.is_symlink():
            skipped.append(f'symlink-rejected: {lock_path}')
            continue
        if not lock_path.exists() or not lock_path.is_file():
            continue
        try:
            with lock_path.open('r+', encoding='utf-8') as f:
                try:
                    fcntl.flock(f.fileno(), fcntl.LOCK_EX | fcntl.LOCK_NB)
                except OSError:
                    locked.append(str(lock_path))
                else:
                    fcntl.flock(f.fileno(), fcntl.LOCK_UN)
        except OSError as exc:
            locked.append(f'{lock_path} ({exc})')
    return locked


def _atomic_write_private_text(path: Path, text: str) -> None:
    tmp = path.parent / f'.{path.name}.memlane-uninstall-{os.getpid()}'
    fd = os.open(tmp, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
    try:
        with os.fdopen(fd, 'w', encoding='utf-8') as f:
            f.write(text)
            f.flush()
            os.fsync(f.fileno())
        os.replace(tmp, path)
    finally:
        if tmp.exists():
            try:
                tmp.unlink()
            except OSError:
                pass


def _backup_file(path: Path, stamp: str) -> Path:
    backup = path.with_name(f'{path.name}.memlane-uninstall-backup-{stamp}')
    content = path.read_text(encoding='utf-8')
    _atomic_write_private_text(backup, content)
    return backup


def _edit_codex_toml(raw: str) -> tuple[str, list[str]]:
    lines = raw.splitlines(keepends=True)
    # First pass: locate [marketplaces.<name>] that belong to memlane.
    memlane_marketplaces: set[str] = set()
    cur_parts: list[str] = []
    for line in lines:
        m = CODEX_SECTION_RE.match(line)
        if m:
            cur_parts = _split_toml_table_path(m.group(1))
            if len(cur_parts) >= 2 and cur_parts[0] == 'marketplaces' and cur_parts[1].lower().startswith('memlane'):
                memlane_marketplaces.add(cur_parts[1])
            continue
        if len(cur_parts) >= 2 and cur_parts[0] == 'marketplaces':
            kv = TOML_KV_RE.match(line)
            if kv and _unquote_toml_key(kv.group(2)) == 'source' and 'memlane' in kv.group(3).lower():
                memlane_marketplaces.add(cur_parts[1])

    def is_memlane_plugin_key(key_name: str) -> bool:
        low = key_name.lower()
        if low.startswith('memlane-cognee'):
            return True
        if '@' in key_name:
            base, mp = key_name.split('@', 1)
            if base == 'cognee' and (mp in memlane_marketplaces or mp.lower().startswith('memlane')):
                return True
        return False

    out: list[str] = []
    removed: list[str] = []
    cur_parts = []
    skip_section = False
    for line in lines:
        m = CODEX_SECTION_RE.match(line)
        if m:
            cur_parts = _split_toml_table_path(m.group(1))
            skip_section = False
            if len(cur_parts) >= 2 and cur_parts[0] == 'marketplaces' and cur_parts[1] in memlane_marketplaces:
                skip_section = True
                removed.append(f'marketplaces.{cur_parts[1]}')
            elif len(cur_parts) >= 2 and cur_parts[0] == 'plugins' and is_memlane_plugin_key(cur_parts[1]):
                skip_section = True
                removed.append(f'plugins.{cur_parts[1]}')
            if not skip_section:
                out.append(line)
            continue
        if skip_section:
            continue
        if cur_parts == ['plugins']:
            kv = TOML_KV_RE.match(line)
            if kv:
                k = _unquote_toml_key(kv.group(2))
                if is_memlane_plugin_key(k):
                    removed.append(f'plugins.{k}')
                    continue
        out.append(line)
    return ''.join(out), removed


def _edit_traex_toml(raw: str) -> tuple[str, list[str]]:
    lines = raw.splitlines(keepends=True)
    out: list[str] = []
    removed: list[str] = []
    cur_parts: list[str] = []
    skip_section = False
    for line in lines:
        m = CODEX_SECTION_RE.match(line)
        if m:
            cur_parts = _split_toml_table_path(m.group(1))
            skip_section = False
            if len(cur_parts) >= 2 and cur_parts[0] == 'marketplaces' and cur_parts[1].lower().startswith('memlane'):
                skip_section = True
                removed.append(f'marketplaces.{cur_parts[1]}')
            elif len(cur_parts) >= 2 and cur_parts[0] == 'plugins' and cur_parts[1].lower().startswith('memlane-cognee'):
                skip_section = True
                removed.append(f'plugins.{cur_parts[1]}')
            if not skip_section:
                out.append(line)
            continue
        if skip_section:
            continue
        if cur_parts == ['plugins']:
            kv = TOML_KV_RE.match(line)
            if kv:
                k = _unquote_toml_key(kv.group(2))
                if k.lower().startswith('memlane-cognee'):
                    removed.append(f'plugins.{k}')
                    continue
        out.append(line)
    return ''.join(out), removed


def _is_memlane_component_dir(entry: Path) -> bool:
    name = entry.name
    if name == 'installation':
        return True
    managed_prefixes = ('uv-', 'python-', 'runtime-', 'cognee-', 'embedding-', 'plugin-integrations-')
    if not name.startswith(managed_prefixes):
        return False
    try:
        for child in entry.iterdir():
            if child.name.startswith('.memlane-') or child.name in ('requirements.lock', 'service.tar.gz', 'artifact.tar.gz', 'artifact.whl'):
                return True
    except OSError:
        return False
    return False


def _remove_dir_action(
    path: Path,
    anchor: Path,
    *,
    apply: bool,
    planned: list[str],
    applied: list[str],
    skipped: list[str],
    errors: list[str],
) -> None:
    if path.is_symlink() or not _is_safe_under(path, anchor):
        skipped.append(f'symlink-rejected: {path}')
        return
    if not path.exists():
        return
    planned.append(f'remove-dir: {path}')
    if not apply:
        return
    try:
        if path.is_dir():
            shutil.rmtree(path)
        else:
            path.unlink()
        applied.append(f'removed: {path}')
    except OSError as exc:
        errors.append(f'failed to remove {path}: {exc}')


def run_uninstall(argv: list[str] | None = None) -> tuple[int, dict]:
    parser = argparse.ArgumentParser(description='Safely uninstall Memlane owned components and agent integrations (dry-run by default).')
    parser.add_argument('--apply', action='store_true', help='Actually perform removals and edits (default is dry-run).')
    parser.add_argument('--home', default='~', help='Home directory override (defaults to current user home).')
    parser.add_argument('--runtime-dir', default='', help='Memlane runtime directory (default ~/Library/Application Support/Memlane).')
    parser.add_argument('--components-dir', default='', help='Memlane bootstrap components directory (default <runtime-dir>/components).')
    parser.add_argument('--app-path', default='/Applications/Memlane.app', help='Application bundle path to remove when --remove-app is passed.')
    parser.add_argument('--launch-agent', default='', help='LaunchAgent plist override (default ~/Library/LaunchAgents/com.memlane.desktop.plist).')
    parser.add_argument('--remove-app', action='store_true', help='Also remove Memlane.app bundle (only when --apply is set).')
    parser.add_argument('--remove-data', action='store_true', help='Also remove memory database and runtime configs (requires --confirm-remove-data).')
    parser.add_argument('--confirm-remove-data', action='store_true', help='Confirm irreversible deletion of user memory database and configs.')
    parser.add_argument('--json', action='store_true', help='Emit machine-readable JSON summary.')
    args = parser.parse_args(argv)

    home = Path(args.home).expanduser().absolute()
    default_runtime = home / 'Library' / 'Application Support' / 'Memlane'
    runtime_dir = Path(args.runtime_dir).expanduser().absolute() if args.runtime_dir else default_runtime
    components_dir = Path(args.components_dir).expanduser().absolute() if args.components_dir else runtime_dir / 'components'
    launch_agent = Path(args.launch_agent).expanduser().absolute() if args.launch_agent else home / 'Library' / 'LaunchAgents' / 'com.memlane.desktop.plist'
    app_path = Path(args.app_path).expanduser().absolute()

    planned: list[str] = []
    applied: list[str] = []
    backups: list[str] = []
    preserved: list[str] = []
    skipped: list[str] = []
    errors: list[str] = []

    if args.remove_data and not args.confirm_remove_data:
        errors.append('--remove-data requires --confirm-remove-data to prevent accidental loss of memory store data')
        result = {
            'status': 'refused_unconfirmed_data_removal',
            'dry_run': not args.apply,
            'locked_owners': [],
            'planned': planned,
            'applied': applied,
            'backups': backups,
            'preserved': preserved,
            'skipped': skipped,
            'errors': errors,
        }
        return 2, result

    locked_owners = _check_service_owner_locks(runtime_dir, skipped)
    if args.apply and locked_owners:
        errors.append('Memlane 服务仍在运行（.service-owner 已加锁），请先停止服务再执行卸载')
        result = {
            'status': 'refused_service_running',
            'dry_run': False,
            'locked_owners': locked_owners,
            'planned': planned,
            'applied': applied,
            'backups': backups,
            'preserved': preserved,
            'skipped': skipped,
            'errors': errors,
        }
        return 1, result

    stamp = _now_stamp()

    # 1. Memlane-owned components directory.
    if components_dir.is_symlink() or (components_dir.exists() and not _is_safe_under(components_dir, components_dir)):
        skipped.append(f'symlink-rejected: {components_dir}')
    elif components_dir.is_dir():
        for entry in sorted(components_dir.iterdir()):
            if not _is_safe_under(entry, components_dir):
                skipped.append(f'symlink-rejected: {entry}')
                continue
            if _is_memlane_component_dir(entry):
                _remove_dir_action(entry, components_dir, apply=args.apply, planned=planned, applied=applied, skipped=skipped, errors=errors)
            else:
                preserved.append(str(entry))

    # 2. LaunchAgent plist.
    if launch_agent.is_symlink() or launch_agent.parent.is_symlink():
        skipped.append(f'symlink-rejected: {launch_agent}')
    elif launch_agent.exists():
        planned.append(f'remove-launch-agent: {launch_agent}')
        if args.apply:
            if sys.platform == 'darwin':
                subprocess.run(['launchctl', 'bootout', f'gui/{os.getuid()}', str(launch_agent)], capture_output=True, check=False)
            try:
                launch_agent.unlink()
                applied.append(f'removed: {launch_agent}')
            except OSError as exc:
                errors.append(f'failed to remove {launch_agent}: {exc}')

    # 3. Codex (~/.codex/config.toml + ~/.cognee-plugin/codex-*).
    codex_dir = home / '.codex'
    codex_toml = codex_dir / 'config.toml'
    if codex_dir.is_symlink():
        skipped.append(f'symlink-rejected: {codex_dir}')
    elif codex_dir.is_dir():
        if codex_toml.is_file() and _is_safe_under(codex_toml, codex_dir):
            try:
                raw = codex_toml.read_text(encoding='utf-8')
                updated, removed_entries = _edit_codex_toml(raw)
                if removed_entries:
                    planned.append(f'edit-codex-config: {codex_toml} ({", ".join(removed_entries)})')
                    if args.apply:
                        bk = _backup_file(codex_toml, stamp)
                        backups.append(str(bk))
                        _atomic_write_private_text(codex_toml, updated)
                        applied.append(f'updated: {codex_toml}')
            except (OSError, UnicodeDecodeError) as exc:
                errors.append(f'failed processing {codex_toml}: {exc}')
        elif codex_toml.is_symlink():
            skipped.append(f'symlink-rejected: {codex_toml}')

        cache_dir = codex_dir / 'plugins' / 'cache'
        if cache_dir.is_dir() and _is_safe_under(cache_dir, codex_dir):
            for mp in sorted(cache_dir.iterdir()):
                if mp.name.lower().startswith('memlane'):
                    target = mp / 'cognee'
                    if target.exists():
                        _remove_dir_action(target, codex_dir, apply=args.apply, planned=planned, applied=applied, skipped=skipped, errors=errors)

    # 4. Antigravity (~/.gemini/config/config.json + plugins/memlane-cognee).
    gemini_cfg_dir = home / '.gemini' / 'config'
    gemini_json = gemini_cfg_dir / 'config.json'
    if (home / '.gemini').is_symlink() or gemini_cfg_dir.is_symlink():
        skipped.append(f'symlink-rejected: {gemini_cfg_dir}')
    elif gemini_cfg_dir.is_dir():
        if gemini_json.is_file() and _is_safe_under(gemini_json, gemini_cfg_dir):
            try:
                raw_json = gemini_json.read_text(encoding='utf-8')
                data = json.loads(raw_json)
                if isinstance(data, dict) and isinstance(data.get('plugins'), dict) and 'memlane-cognee' in data['plugins']:
                    planned.append(f'edit-antigravity-config: {gemini_json} (plugins.memlane-cognee)')
                    if args.apply:
                        bk = _backup_file(gemini_json, stamp)
                        backups.append(str(bk))
                        del data['plugins']['memlane-cognee']
                        _atomic_write_private_text(gemini_json, json.dumps(data, indent=2, ensure_ascii=False) + '\n')
                        applied.append(f'updated: {gemini_json}')
            except (OSError, ValueError) as exc:
                errors.append(f'failed processing {gemini_json}: {exc}')
        elif gemini_json.is_symlink():
            skipped.append(f'symlink-rejected: {gemini_json}')

        ag_plugin_dir = gemini_cfg_dir / 'plugins' / 'memlane-cognee'
        if ag_plugin_dir.is_symlink() or not _is_safe_under(ag_plugin_dir, gemini_cfg_dir):
            if ag_plugin_dir.exists() or ag_plugin_dir.is_symlink():
                skipped.append(f'symlink-rejected: {ag_plugin_dir}')
        elif ag_plugin_dir.is_dir():
            is_owned = (ag_plugin_dir / '.memlane-installation.json').is_file()
            manifest_path = ag_plugin_dir / 'plugin.json'
            if not is_owned and manifest_path.is_file():
                try:
                    if json.loads(manifest_path.read_text(encoding='utf-8')).get('name') == 'memlane-cognee':
                        is_owned = True
                except (OSError, ValueError):
                    pass
            if is_owned:
                _remove_dir_action(ag_plugin_dir, gemini_cfg_dir, apply=args.apply, planned=planned, applied=applied, skipped=skipped, errors=errors)

    # 5. pi (~/.pi/agent/extensions/memlane-cognee).
    pi_agent_dir = home / '.pi' / 'agent'
    pi_ext_dir = pi_agent_dir / 'extensions' / 'memlane-cognee'
    if (home / '.pi').is_symlink() or pi_agent_dir.is_symlink():
        skipped.append(f'symlink-rejected: {pi_ext_dir}')
    elif pi_ext_dir.exists() or pi_ext_dir.is_symlink():
        _remove_dir_action(pi_ext_dir, pi_agent_dir, apply=args.apply, planned=planned, applied=applied, skipped=skipped, errors=errors)

    # 6. TraeX (~/.traecli and ~/.trae).
    for trae_name in ('.traecli', '.trae'):
        trae_dir = home / trae_name
        if trae_dir.is_symlink():
            skipped.append(f'symlink-rejected: {trae_dir}')
            continue
        if not trae_dir.is_dir():
            continue
        trae_toml = trae_dir / 'traecli.toml'
        if trae_toml.is_file() and _is_safe_under(trae_toml, trae_dir):
            try:
                raw_trae = trae_toml.read_text(encoding='utf-8')
                updated_trae, removed_trae = _edit_traex_toml(raw_trae)
                if removed_trae:
                    planned.append(f'edit-traex-config: {trae_toml} ({", ".join(removed_trae)})')
                    if args.apply:
                        bk = _backup_file(trae_toml, stamp)
                        backups.append(str(bk))
                        _atomic_write_private_text(trae_toml, updated_trae)
                        applied.append(f'updated: {trae_toml}')
            except (OSError, UnicodeDecodeError) as exc:
                errors.append(f'failed processing {trae_toml}: {exc}')
        elif trae_toml.is_symlink():
            skipped.append(f'symlink-rejected: {trae_toml}')

        mp_dir = trae_dir / '.tmp' / 'marketplaces'
        if mp_dir.is_dir() and _is_safe_under(mp_dir, trae_dir):
            for entry in sorted(mp_dir.iterdir()):
                if entry.name.lower().startswith('memlane'):
                    _remove_dir_action(entry, trae_dir, apply=args.apply, planned=planned, applied=applied, skipped=skipped, errors=errors)
        cache_dir = trae_dir / 'plugins' / 'cache'
        if cache_dir.is_dir() and _is_safe_under(cache_dir, trae_dir):
            for mp in sorted(cache_dir.iterdir()):
                target = mp / 'memlane-cognee'
                if target.exists() or target.is_symlink():
                    _remove_dir_action(target, trae_dir, apply=args.apply, planned=planned, applied=applied, skipped=skipped, errors=errors)

    # 7. Per-host plugin runtime buffer dirs under ~/.cognee-plugin/.
    cognee_plugin_dir = home / '.cognee-plugin'
    if cognee_plugin_dir.is_symlink():
        skipped.append(f'symlink-rejected: {cognee_plugin_dir}')
    elif cognee_plugin_dir.is_dir():
        for entry in sorted(cognee_plugin_dir.iterdir()):
            if not _is_safe_under(entry, cognee_plugin_dir):
                skipped.append(f'symlink-rejected: {entry}')
                continue
            if not entry.is_dir():
                continue
            name = entry.name
            has_marker = any(child.name.startswith('.memlane-') for child in entry.iterdir())
            if (name.startswith('codex-') and has_marker) or (
                name.startswith(('antigravity-', 'pi-', 'traex-')) and (has_marker or (entry / 'last-hook-error.json').is_file() or (entry / 'pending.json').is_file())
            ):
                _remove_dir_action(entry, cognee_plugin_dir, apply=args.apply, planned=planned, applied=applied, skipped=skipped, errors=errors)
            else:
                preserved.append(str(entry))

    # 8. App bundle (opt-in --remove-app).
    if args.remove_app:
        if app_path.is_symlink() or app_path.parent.is_symlink():
            skipped.append(f'symlink-rejected: {app_path}')
        elif app_path.exists():
            planned.append(f'remove-app: {app_path}')
            if args.apply:
                try:
                    shutil.rmtree(app_path)
                    applied.append(f'removed: {app_path}')
                except OSError as exc:
                    errors.append(f'failed to remove {app_path}: {exc}')
    elif app_path.exists():
        preserved.append(str(app_path))

    # 9. Runtime memory store & configs (preserved unless --remove-data --confirm-remove-data).
    if runtime_dir.is_symlink():
        skipped.append(f'symlink-rejected: {runtime_dir}')
    elif runtime_dir.is_dir():
        data_targets = [runtime_dir / 'official-store', runtime_dir / 'runtime.json']
        if args.remove_data and args.confirm_remove_data:
            for item in sorted(runtime_dir.iterdir()):
                if item == components_dir:
                    continue
                _remove_dir_action(item, runtime_dir, apply=args.apply, planned=planned, applied=applied, skipped=skipped, errors=errors)
        else:
            for item in data_targets:
                if item.exists():
                    preserved.append(str(item))

    status = 'dry_run' if not args.apply else ('completed_with_errors' if errors else 'completed')
    result = {
        'status': status,
        'dry_run': not args.apply,
        'locked_owners': locked_owners,
        'planned': planned,
        'applied': applied,
        'backups': backups,
        'preserved': preserved,
        'skipped': skipped,
        'errors': errors,
    }
    return (1 if errors else 0), result


def main(argv: list[str] | None = None) -> int:
    code, result = run_uninstall(argv)
    if '--json' in (sys.argv[1:] if argv is None else argv):
        print(json.dumps(result, ensure_ascii=False, indent=2))
    else:
        mode = 'DRY-RUN（未修改任何文件，传 --apply 执行）' if result['dry_run'] else 'APPLY（已执行卸载）'
        print(f'Memlane 卸载器 [{mode}] 状态: {result["status"]}')
        if result['locked_owners']:
            print('警告 - 检测到活跃服务锁 (.service-owner):')
            for lock in result['locked_owners']:
                print(f'  ! {lock}')
        if result['planned']:
            print('计划操作:')
            for item in result['planned']:
                print(f'  - {item}')
        if result['backups']:
            print('已生成配置备份:')
            for bk in result['backups']:
                print(f'  + {bk}')
        if result['preserved']:
            print('保留目录/文件:')
            for item in result['preserved']:
                print(f'  = {item}')
        if result['skipped']:
            print('安全跳过:')
            for item in result['skipped']:
                print(f'  * {item}')
        if result['errors']:
            print('错误:', file=sys.stderr)
            for err in result['errors']:
                print(f'  x {err}', file=sys.stderr)
    return code


if __name__ == '__main__':
    sys.exit(main())
