#!/usr/bin/env python3
"""Safe Memlane uninstaller (dry-run by default).

Removes Memlane-owned components, LaunchAgent, and Memlane entries in supported
Agent host configs (Codex, Antigravity, pi, TraeX) with timestamped backups,
while preserving memory store data and configs unless explicitly confirmed.
"""
from __future__ import annotations

import argparse
from contextlib import contextmanager
import fcntl
import json
import os
import re
import shlex
import shutil
import socket
import stat
import subprocess
import sys
import time
from pathlib import Path
from urllib.parse import urlparse


TOML_KV_RE = re.compile(r'^(\s*)([A-Za-z0-9_.-]+|"[^"]+"|\'[^\']+\')(\s*=.*)$')
DEFAULT_BUNDLE_ID = 'io.github.zwhu.memlane'
LEGACY_LAUNCH_AGENT_NAMES = (
    f'{DEFAULT_BUNDLE_ID}.plist',
    'com.memlane.desktop.plist',
)
SMAPPSERVICE_NOTE = (
    f'macOS 登录项（SMAppService / {DEFAULT_BUNDLE_ID}）：移除 Memlane.app 后系统会自动清理登录项；'
    '若保留应用，请在「系统设置 › 通用 › 登录项」或 Memlane 托盘菜单中关闭「登录时启动」。'
)


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


def _parse_toml_header(line: str) -> tuple[bool, list[str]] | None:
    """Parse a TOML table `[a.b]` or array-of-tables `[[a.b]]` header line.

    Returns `(is_array_table, table_path)` or `None` if `line` is not a header.
    """
    stripped = line.lstrip()
    if not stripped.startswith('['):
        return None
    is_array = stripped.startswith('[[')
    idx = 2 if is_array else 1
    quote = ''
    escaped = False
    close_idx = -1
    n = len(stripped)
    while idx < n:
        ch = stripped[idx]
        if escaped:
            escaped = False
            idx += 1
            continue
        if ch == '\\' and quote == '"':
            escaped = True
            idx += 1
            continue
        if quote:
            if ch == quote:
                quote = ''
            idx += 1
            continue
        if ch in ('"', "'"):
            quote = ch
            idx += 1
            continue
        if ch == ']':
            if is_array:
                if idx + 1 < n and stripped[idx + 1] == ']':
                    close_idx = idx
                    break
                return None
            close_idx = idx
            break
        if ch == '[':
            return None
        idx += 1
    if close_idx < 0:
        return None
    inner = stripped[2 if is_array else 1:close_idx].strip()
    if not inner:
        return None
    rest = stripped[close_idx + (2 if is_array else 1):].strip()
    if rest and not rest.startswith('#'):
        return None
    return is_array, _split_toml_table_path(inner)


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


def _candidate_store_dirs(runtime_dir: Path, skipped: list[str]) -> list[Path]:
    dirs: list[Path] = []
    if _is_safe_under(runtime_dir, runtime_dir):
        dirs.append(runtime_dir)
        dirs.append(runtime_dir / 'library')
        dirs.append(runtime_dir / 'official-store')
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
                            dirs.append(p)
                        else:
                            skipped.append(f'symlink-rejected: {p}')
    seen: set[Path] = set()
    unique: list[Path] = []
    for d in dirs:
        if d in seen:
            continue
        seen.add(d)
        unique.append(d)
    return unique


def _process_table_snapshot() -> tuple[dict[int, tuple[int, str]], dict[int, tuple[int, str]]] | None:
    """Return `(locale_table, c_locale_table)` mapping pid -> (pgid, lstart)."""
    def _read_table(extra_env: dict[str, str] | None = None) -> dict[int, tuple[int, str]] | None:
        env = None if not extra_env else {**os.environ, **extra_env}
        try:
            out = subprocess.check_output(['ps', '-axo', 'pid=,pgid=,lstart='], text=True, env=env)
        except (OSError, subprocess.SubprocessError):
            return None
        table: dict[int, tuple[int, str]] = {}
        for row in out.splitlines():
            parts = row.split(None, 2)
            if len(parts) != 3:
                continue
            try:
                pid = int(parts[0])
                pgid = int(parts[1])
            except ValueError:
                continue
            if pid > 0:
                table[pid] = (pgid, parts[2].strip())
        return table

    table = _read_table()
    if table is None:
        return None
    table_c = _read_table({'LC_ALL': 'C'})
    return table, (table_c if table_c is not None else table)


def _loopback_port_in_use(raw_url: str) -> bool:
    if not isinstance(raw_url, str) or not raw_url.strip():
        return False
    parsed = urlparse(raw_url.strip())
    if parsed.scheme != 'http' or parsed.hostname != '127.0.0.1' or not parsed.port:
        return False
    try:
        with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as sock:
            sock.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
            sock.bind(('127.0.0.1', parsed.port))
            return False
    except OSError:
        return True


def _check_owner_receipt_active(
    receipt_path: Path,
    skipped: list[str],
    table_cache: list,
) -> str | None:
    """Check `.memlane-dev-owner.json` for still-alive PIDs/PGIDs or listening ports.

    Covers the unconfirmed-stop state where `supervisor.go` released the flock
    while deliberately preserving the owner receipt.
    """
    if receipt_path.is_symlink() or receipt_path.parent.is_symlink():
        skipped.append(f'symlink-rejected: {receipt_path}')
        return None
    if not receipt_path.exists():
        return None
    try:
        fd = os.open(receipt_path, os.O_RDONLY | os.O_NOFOLLOW)
    except OSError as exc:
        return f'{receipt_path} (unreadable owner receipt: {exc})'
    try:
        st = os.fstat(fd)
        if not stat.S_ISREG(st.st_mode):
            return f'{receipt_path} (non-regular owner receipt)'
        with os.fdopen(fd, 'r', encoding='utf-8') as fh:
            fd = -1
            state = json.load(fh)
    except (OSError, ValueError) as exc:
        return f'{receipt_path} (invalid owner receipt: {exc})'
    finally:
        if fd >= 0:
            os.close(fd)

    if not isinstance(state, dict):
        return f'{receipt_path} (malformed owner receipt)'

    if not table_cache:
        table_cache.append(_process_table_snapshot())
    snapshot = table_cache[0]
    if snapshot is None:
        return f'{receipt_path} (unable to inspect processes for retained owner receipt)'
    table, table_c = snapshot

    def _matches_start(pid: int, expected_start: str) -> bool:
        expected = expected_start.strip()
        if not expected:
            return pid in table
        if pid in table and table[pid][1] == expected:
            return True
        if pid in table_c and table_c[pid][1] == expected:
            return True
        return False

    owner_pid = state.get('pid')
    owner_started = str(state.get('started') or '')
    if isinstance(owner_pid, int) and owner_pid > 1 and _matches_start(owner_pid, owner_started):
        return f'{receipt_path} (active owner pid {owner_pid})'

    child_pids: set[int] = set()
    for raw_pid in state.get('children') or []:
        if isinstance(raw_pid, int) and raw_pid > 1:
            child_pids.add(raw_pid)
    svc_children = state.get('service_children')
    if isinstance(svc_children, dict):
        for raw_pid in svc_children.values():
            if isinstance(raw_pid, int) and raw_pid > 1:
                child_pids.add(raw_pid)
    live_pgids = {pgid for pgid, _ in table.values()}
    for cpid in sorted(child_pids):
        if cpid in table or cpid in live_pgids:
            return f'{receipt_path} (active service child/pgid {cpid})'

    descendants = state.get('observed_descendants')
    if isinstance(descendants, dict):
        for raw_dpid, started in descendants.items():
            try:
                dpid = int(raw_dpid)
            except (TypeError, ValueError):
                continue
            if dpid > 1 and _matches_start(dpid, str(started or '')):
                return f'{receipt_path} (active descendant pid {dpid})'

    for url_key in ('service_url', 'completion_url', 'project_control_url'):
        raw_url = state.get(url_key)
        if isinstance(raw_url, str) and _loopback_port_in_use(raw_url):
            return f'{receipt_path} (active endpoint {raw_url})'

    return None


@contextmanager
def _hold_service_owner_locks(runtime_dir: Path, skipped: list[str], *, hold: bool):
    """Probe and optionally hold `.service-owner` flocks across the uninstall critical section.

    Also inspects `.memlane-dev-owner.json` receipts to catch unconfirmed-stop
    states where the flock was released while a process or port remained active.
    """
    candidates = _candidate_store_dirs(runtime_dir, skipped)
    locked: list[str] = []
    held_fds: list[int] = []
    table_cache: list = []

    try:
        for store_dir in candidates:
            lock_path = store_dir / '.service-owner'
            if lock_path.is_symlink() or lock_path.parent.is_symlink():
                skipped.append(f'symlink-rejected: {lock_path}')
            elif lock_path.exists():
                fd = -1
                try:
                    fd = os.open(lock_path, os.O_RDWR | os.O_NOFOLLOW)
                    if not stat.S_ISREG(os.fstat(fd).st_mode):
                        locked.append(f'{lock_path} (non-regular lock file)')
                        os.close(fd)
                        fd = -1
                    else:
                        try:
                            fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
                        except OSError:
                            locked.append(str(lock_path))
                            os.close(fd)
                            fd = -1
                        else:
                            if hold:
                                held_fds.append(fd)
                                fd = -1
                            else:
                                fcntl.flock(fd, fcntl.LOCK_UN)
                                os.close(fd)
                                fd = -1
                except OSError as exc:
                    locked.append(f'{lock_path} ({exc})')
                    if fd >= 0:
                        os.close(fd)

            receipt_issue = _check_owner_receipt_active(store_dir / '.memlane-dev-owner.json', skipped, table_cache)
            if receipt_issue:
                locked.append(receipt_issue)

        yield locked
    finally:
        for fd in held_fds:
            try:
                fcntl.flock(fd, fcntl.LOCK_UN)
            except OSError:
                pass
            try:
                os.close(fd)
            except OSError:
                pass


def _check_service_owner_locks(runtime_dir: Path, skipped: list[str]) -> list[str]:
    """Non-holding convenience wrapper for dry-run inspection and tests."""
    with _hold_service_owner_locks(runtime_dir, skipped, hold=False) as locked:
        return list(locked)


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


def _remove_trae_ide_hooks(data, runtime_dir: Path) -> list[str]:
    if not isinstance(data, dict) or not isinstance(data.get('hooks'), dict):
        raise ValueError('invalid TRAE IDE hooks')
    managed = runtime_dir / 'managed-marketplaces'
    removed = []
    for event in ('SessionStart', 'UserPromptSubmit', 'PostToolUse', 'Stop'):
        groups = data['hooks'].get(event, [])
        for group in list(groups):
            if not isinstance(group, dict) or set(group) != {'hooks'}:
                continue
            hooks = group['hooks']
            if not isinstance(hooks, list) or len(hooks) != 1 or not isinstance(hooks[0], dict):
                continue
            hook = hooks[0]
            if set(hook) != {'type', 'command', 'timeout'} or hook.get('type') != 'command':
                continue
            try:
                args = shlex.split(hook['command'])
                if len(args) != 4 or args[1] != '-B' or args[3] != event:
                    continue
                script = Path(args[2])
                parts = script.relative_to(managed).parts
            except (AttributeError, TypeError, ValueError):
                continue
            if (len(parts) == 6 and re.fullmatch(r'trae_ide-[a-f0-9]{24}', parts[0])
                    and re.fullmatch(r'[a-f0-9]{20}', parts[1])
                    and parts[2:] == ('plugins', 'cognee', 'scripts', 'trae_ide_hook.py')):
                groups.remove(group)
                removed.append(event)
    return removed


def _backup_file(path: Path, stamp: str) -> Path:
    backup = path.with_name(f'{path.name}.memlane-uninstall-backup-{stamp}')
    content = path.read_text(encoding='utf-8')
    _atomic_write_private_text(backup, content)
    return backup


def _scan_toml_multiline_state(line: str, in_multiline: str | None) -> str | None:
    """Track TOML multiline basic (`\"\"\"`) and literal (`'''`) string state across lines."""
    i = 0
    n = len(line)
    state = in_multiline
    while i < n:
        if state == '"""':
            if line[i] == '\\':
                i += 2
                continue
            if line[i:i + 3] == '"""':
                # TOML allows up to 2 literal quotes immediately before the closing triple-quote.
                run = 3
                while i + run < n and line[i + run] == '"' and run < 5:
                    run += 1
                i += run
                state = None
                continue
            i += 1
            continue
        if state == "'''":
            if line[i:i + 3] == "'''":
                run = 3
                while i + run < n and line[i + run] == "'" and run < 5:
                    run += 1
                i += run
                state = None
                continue
            i += 1
            continue
        ch = line[i]
        if ch == '#':
            break
        if line[i:i + 3] == '"""':
            state = '"""'
            i += 3
            continue
        if line[i:i + 3] == "'''":
            state = "'''"
            i += 3
            continue
        if ch == '"':
            i += 1
            while i < n:
                if line[i] == '\\':
                    i += 2
                    continue
                if line[i] == '"':
                    i += 1
                    break
                i += 1
            continue
        if ch == "'":
            i += 1
            while i < n:
                if line[i] == "'":
                    i += 1
                    break
                i += 1
            continue
        i += 1
    return state


def _validate_toml_text(text: str) -> None:
    """Validate edited TOML before writing to disk (fail-closed)."""
    state: str | None = None
    for line in text.splitlines(keepends=True):
        state = _scan_toml_multiline_state(line, state)
    if state is not None:
        raise ValueError(f'unterminated TOML multiline string ({state})')
    try:
        import tomllib  # type: ignore[import-not-found]
    except ImportError:
        tomllib = None
    if tomllib is not None:
        try:
            tomllib.loads(text)
        except Exception as exc:
            raise ValueError(f'edited TOML failed parse validation: {exc}') from exc


def _edit_codex_toml(raw: str) -> tuple[str, list[str]]:
    lines = raw.splitlines(keepends=True)
    # First pass: locate [marketplaces.<name>] that belong to memlane, ignoring multiline string bodies.
    memlane_marketplaces: set[str] = set()
    cur_parts: list[str] = []
    cur_is_array = False
    in_multiline: str | None = None
    for line in lines:
        if in_multiline is not None:
            in_multiline = _scan_toml_multiline_state(line, in_multiline)
            continue
        header = _parse_toml_header(line)
        if header is not None:
            cur_is_array, cur_parts = header
            if not cur_is_array and len(cur_parts) >= 2 and cur_parts[0] == 'marketplaces' and cur_parts[1].lower().startswith('memlane'):
                memlane_marketplaces.add(cur_parts[1])
            continue
        if not cur_is_array and len(cur_parts) >= 2 and cur_parts[0] == 'marketplaces':
            kv = TOML_KV_RE.match(line)
            if kv and _unquote_toml_key(kv.group(2)) == 'source' and 'memlane' in kv.group(3).lower():
                memlane_marketplaces.add(cur_parts[1])
        in_multiline = _scan_toml_multiline_state(line, in_multiline)

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
    cur_is_array = False
    skip_section = False
    skip_multiline_kv = False
    in_multiline = None
    for line in lines:
        if in_multiline is not None:
            in_multiline = _scan_toml_multiline_state(line, in_multiline)
            if not skip_section and not skip_multiline_kv:
                out.append(line)
            if in_multiline is None:
                skip_multiline_kv = False
            continue
        header = _parse_toml_header(line)
        if header is not None:
            cur_is_array, cur_parts = header
            skip_section = False
            skip_multiline_kv = False
            if not cur_is_array and len(cur_parts) >= 2 and cur_parts[0] == 'marketplaces' and cur_parts[1] in memlane_marketplaces:
                skip_section = True
                removed.append(f'marketplaces.{cur_parts[1]}')
            elif not cur_is_array and len(cur_parts) >= 2 and cur_parts[0] == 'plugins' and is_memlane_plugin_key(cur_parts[1]):
                skip_section = True
                removed.append(f'plugins.{cur_parts[1]}')
            if not skip_section:
                out.append(line)
            continue
        next_multiline = _scan_toml_multiline_state(line, in_multiline)
        if skip_section:
            in_multiline = next_multiline
            continue
        if not cur_is_array and cur_parts == ['plugins']:
            kv = TOML_KV_RE.match(line)
            if kv:
                k = _unquote_toml_key(kv.group(2))
                if is_memlane_plugin_key(k):
                    removed.append(f'plugins.{k}')
                    in_multiline = next_multiline
                    if in_multiline is not None:
                        skip_multiline_kv = True
                    continue
        out.append(line)
        in_multiline = next_multiline
    updated = ''.join(out)
    if removed:
        _validate_toml_text(updated)
    return updated, removed


def _edit_traex_toml(raw: str) -> tuple[str, list[str]]:
    lines = raw.splitlines(keepends=True)
    out: list[str] = []
    removed: list[str] = []
    cur_parts: list[str] = []
    cur_is_array = False
    skip_section = False
    skip_multiline_kv = False
    in_multiline: str | None = None
    for line in lines:
        if in_multiline is not None:
            in_multiline = _scan_toml_multiline_state(line, in_multiline)
            if not skip_section and not skip_multiline_kv:
                out.append(line)
            if in_multiline is None:
                skip_multiline_kv = False
            continue
        header = _parse_toml_header(line)
        if header is not None:
            cur_is_array, cur_parts = header
            skip_section = False
            skip_multiline_kv = False
            if not cur_is_array and len(cur_parts) >= 2 and cur_parts[0] == 'marketplaces' and cur_parts[1].lower().startswith('memlane'):
                skip_section = True
                removed.append(f'marketplaces.{cur_parts[1]}')
            elif not cur_is_array and len(cur_parts) >= 2 and cur_parts[0] == 'plugins' and cur_parts[1].lower().startswith('memlane-cognee'):
                skip_section = True
                removed.append(f'plugins.{cur_parts[1]}')
            if not skip_section:
                out.append(line)
            continue
        next_multiline = _scan_toml_multiline_state(line, in_multiline)
        if skip_section:
            in_multiline = next_multiline
            continue
        if not cur_is_array and cur_parts == ['plugins']:
            kv = TOML_KV_RE.match(line)
            if kv:
                k = _unquote_toml_key(kv.group(2))
                if k.lower().startswith('memlane-cognee'):
                    removed.append(f'plugins.{k}')
                    in_multiline = next_multiline
                    if in_multiline is not None:
                        skip_multiline_kv = True
                    continue
        out.append(line)
        in_multiline = next_multiline
    updated = ''.join(out)
    if removed:
        _validate_toml_text(updated)
    return updated, removed


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
    parser.add_argument('--launch-agent', default='', help='LaunchAgent plist override (default checks io.github.zwhu.memlane.plist and legacy com.memlane.desktop.plist).')
    parser.add_argument('--remove-app', action='store_true', help='Also remove Memlane.app bundle (only when --apply is set).')
    parser.add_argument('--remove-data', action='store_true', help='Also remove memory database and runtime configs (requires --confirm-remove-data).')
    parser.add_argument('--confirm-remove-data', action='store_true', help='Confirm irreversible deletion of user memory database and configs.')
    parser.add_argument('--json', action='store_true', help='Emit machine-readable JSON summary.')
    args = parser.parse_args(argv)

    home = Path(args.home).expanduser().absolute()
    default_runtime = home / 'Library' / 'Application Support' / 'Memlane'
    runtime_dir = Path(args.runtime_dir).expanduser().absolute() if args.runtime_dir else default_runtime
    components_dir = Path(args.components_dir).expanduser().absolute() if args.components_dir else runtime_dir / 'components'
    if args.launch_agent:
        launch_agents = [Path(args.launch_agent).expanduser().absolute()]
    else:
        launch_agents = [home / 'Library' / 'LaunchAgents' / name for name in LEGACY_LAUNCH_AGENT_NAMES]
    app_path = Path(args.app_path).expanduser().absolute()

    planned: list[str] = []
    applied: list[str] = []
    backups: list[str] = []
    preserved: list[str] = []
    skipped: list[str] = []
    errors: list[str] = []
    notes: list[str] = [SMAPPSERVICE_NOTE]

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
            'notes': notes,
            'errors': errors,
        }
        return 2, result

    with _hold_service_owner_locks(runtime_dir, skipped, hold=args.apply) as locked_owners:
        if args.apply and locked_owners:
            errors.append('Memlane 服务仍在运行或停服未确认（.service-owner 锁或所有权收据仍活跃），请先停止服务再执行卸载')
            result = {
                'status': 'refused_service_running',
                'dry_run': False,
                'locked_owners': locked_owners,
                'planned': planned,
                'applied': applied,
                'backups': backups,
                'preserved': preserved,
                'skipped': skipped,
                'notes': notes,
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

        # 2. LaunchAgent plists (current io.github.zwhu.memlane + legacy com.memlane.desktop).
        for launch_agent in launch_agents:
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
                            _validate_toml_text(updated)
                            bk = _backup_file(codex_toml, stamp)
                            backups.append(str(bk))
                            _atomic_write_private_text(codex_toml, updated)
                            applied.append(f'updated: {codex_toml}')
                except (OSError, UnicodeDecodeError, ValueError) as exc:
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
                            _validate_toml_text(updated_trae)
                            bk = _backup_file(trae_toml, stamp)
                            backups.append(str(bk))
                            _atomic_write_private_text(trae_toml, updated_trae)
                            applied.append(f'updated: {trae_toml}')
                except (OSError, UnicodeDecodeError, ValueError) as exc:
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

        trae_ide = home / '.trae-cn' / 'hooks.json'
        if trae_ide.is_symlink() or trae_ide.parent.is_symlink():
            skipped.append(f'symlink-rejected: {trae_ide}')
        elif trae_ide.is_file():
            try:
                data = json.loads(trae_ide.read_text(encoding='utf-8'))
                removed = _remove_trae_ide_hooks(data, runtime_dir)
                if removed:
                    planned.append(f'edit-trae-ide-config: {trae_ide} ({", ".join(removed)})')
                    if args.apply:
                        backups.append(str(_backup_file(trae_ide, stamp)))
                        _atomic_write_private_text(trae_ide, json.dumps(data, indent=2, ensure_ascii=False) + '\n')
                        applied.append(f'updated: {trae_ide}')
            except (OSError, TypeError, ValueError) as exc:
                errors.append(f'failed processing {trae_ide}: {exc}')

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
                    name.startswith(('antigravity-', 'pi-', 'traex-', 'trae_ide-')) and (has_marker or (entry / 'last-hook-error.json').is_file() or (entry / 'pending.json').is_file())
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
            data_targets = [
                runtime_dir / 'library',
                runtime_dir / 'official-store',
                runtime_dir / 'runtime.json',
                runtime_dir / 'desktop-preferences.json',
                runtime_dir / 'client-state.json',
                runtime_dir / 'generation.json',
            ]
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
            'notes': notes,
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
            print('警告 - 检测到活跃服务锁或未确认停服收据:')
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
        if result.get('notes'):
            print('说明:')
            for note in result['notes']:
                print(f'  i {note}')
        if result['errors']:
            print('错误:', file=sys.stderr)
            for err in result['errors']:
                print(f'  x {err}', file=sys.stderr)
    return code


if __name__ == '__main__':
    sys.exit(main())
