#!/usr/bin/env python3
"""Fail-fast environment probes, protected path guards, and privacy checks for UTM/local verification."""
from __future__ import annotations

import base64
from concurrent.futures import ThreadPoolExecutor, TimeoutError as FutureTimeoutError
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import socket
import stat
import subprocess
import sys
import time
from typing import Callable
from urllib.error import HTTPError, URLError
from urllib.parse import urlparse
import urllib.request

EXIT_PREFLIGHT_BLOCKED = 31
EXIT_PROTECTED_PATH_VIOLATED = 32
EXIT_ISOLATED_VERSION_MISMATCH = 33

ISOLATED_MANIFEST_NAME = '.memlane-utm-isolated.json'
ISOLATED_MANIFEST_CREATOR = 'verify_setup_cli'

MIN_INSTALL_DISK_BYTES = 2 * 1024**3
WARN_INSTALL_DISK_BYTES = 5 * 1024**3
MIN_ZERO_CACHE_DISK_BYTES = 6 * 1024**3

DNS_TIMEOUT_S = 1.5
SCUTIL_TIMEOUT_S = 1.0
PROXY_TCP_TIMEOUT_S = 1.0
HEAD_TIMEOUT_S = 2.0
JWT_MIN_REMAINING_S = 300
MAX_TOKEN_BYTES = 64 * 1024
SHA_CHUNK_SIZE = 1 << 20

MIRROR_PROBE_HOSTS = {
    'china': ('pypi.tuna.tsinghua.edu.cn', 'modelscope.cn'),
    'official': ('pypi.org', 'github.com', 'huggingface.co'),
}
MIRROR_HEAD_URLS = {
    'china': 'https://pypi.tuna.tsinghua.edu.cn/simple/',
    'official': 'https://pypi.org/simple/',
}

SECRET_MARKERS = ('sk-', 'Bearer ', 'X-Api-Key', 'management.token', 'owner.token', '.local/')
# Fixed CLI guidance mentions the private path marker without disclosing a real path.
GUIDANCE = (
    'Execute read-only checks first. Show preview and ask user confirmation before any '
    'requires=explicit_confirmation action. Never read or print tokens, API keys, .local/ '
    'paths, hooks.json raw contents, or memory data; never run xattr or edit host config files '
    'directly. On exit 20 follow next_actions after retry_after_ms (poll doctor --operation-id '
    'while active; retry install after confirmation on lock contention). Stop after 3 retries '
    '(4th failure). On exit 11 hand human_steps to user.'
)


class GuardError(RuntimeError):
    """Base error for guard and privacy violations."""


class ProtectedPathError(GuardError):
    """Raised when an operation attempts to delete or escape into a protected path."""


def sha256_file(path: Path) -> str:
    """Streamingly compute SHA-256 digest of a file without loading it all into memory."""
    digest = hashlib.sha256()
    with path.open('rb') as handle:
        for chunk in iter(lambda: handle.read(SHA_CHUNK_SIZE), b''):
            digest.update(chunk)
    return digest.hexdigest()


def json_object(raw: str, *, error_cls: type[Exception] = GuardError) -> dict:
    def unique_object(pairs):
        result = {}
        for key, value in pairs:
            if key in result:
                raise ValueError('duplicate JSON key')
            result[key] = value
        return result

    def invalid_constant(_):
        raise ValueError('invalid JSON number')

    try:
        payload = json.loads(raw, object_pairs_hook=unique_object, parse_constant=invalid_constant)
    except (ValueError, RecursionError):
        raise error_cls('expected a single valid JSON object') from None
    if not isinstance(payload, dict):
        raise error_cls('expected a single valid JSON object')
    return payload


def check_output_privacy(
    raw: str,
    payload: dict,
    extra_markers: tuple[str, ...] = (),
    *,
    guidance: str = GUIDANCE,
    error_cls: type[Exception] = GuardError,
) -> None:
    public_payload = payload.copy()
    if public_payload.get('agent_guidance') == guidance:
        public_payload.pop('agent_guidance')
        raw = raw.replace(json.dumps(guidance), '""', 1)
    decoded = json.dumps(public_payload, ensure_ascii=False)
    for marker in (*SECRET_MARKERS, *extra_markers):
        if not marker:
            continue
        if marker == 'sk-':
            # Match secret key prefixes on a token boundary so benign words like "memlane-task-"
            # or "disk-cleanup" do not false-positive while sk-*, "sk-...", and \u0073k-... are caught.
            pattern = re.compile(r'(?<![A-Za-z0-9_])sk-', re.IGNORECASE)
            if pattern.search(raw) or pattern.search(decoded):
                raise error_cls('JSON output contains a private value or path')
            continue
        needle = marker.casefold()
        if needle in raw.casefold() or needle in decoded.casefold():
            raise error_cls('JSON output contains a private value or path')


def _is_relative_to(path: Path, base: Path) -> bool:
    try:
        path.relative_to(base)
        return True
    except ValueError:
        return False


def _norm_lexical(path: Path) -> Path:
    return Path(os.path.abspath(str(path)))


def _norm_real(path: Path) -> Path:
    return Path(os.path.realpath(str(path)))


def protected_guest_paths(home: Path) -> tuple[Path, ...]:
    """Return hardcoded user/host login and business data paths that cleanup must never remove."""
    h = _norm_lexical(home)
    return (
        h / 'Library' / 'Application Support' / 'Memlane',
        Path('/Applications/Memlane.app'),
        Path('/Applications/Trae CN.app'),
        Path('/Applications/TRAE CN.app'),
        h / 'Library' / 'Application Support' / 'Trae CN',
        h / 'Library' / 'Application Support' / 'TRAE CN',
        h / '.trae-cn',
        h / '.trae-cn' / 'trae-jwt-token',
        Path('/Applications/Codex.app'),
        Path('/Applications/ChatGPT.app'),
        h / '.codex' / 'auth.json',
        Path('/Applications/Gemini.app'),
        h / '.gemini',
        h / '.pi' / 'agent' / 'auth.json',
        h / '.trae' / 'cli' / 'auth.json',
        h / '.traecli' / 'cli' / 'auth.json',
    )


def _norm_case(path: Path, *, case_insensitive: bool) -> Path:
    if not case_insensitive:
        return path
    return Path(*(part.casefold() for part in path.parts))


def _is_codex_state_db(candidate: Path, codex_dir: Path, *, case_insensitive: bool = False) -> bool:
    cand_parent = _norm_case(candidate.parent, case_insensitive=case_insensitive)
    target_dir = _norm_case(codex_dir, case_insensitive=case_insensitive)
    name = candidate.name.casefold() if case_insensitive else candidate.name
    return cand_parent == target_dir and name.startswith('state_') and '.sqlite' in name


def is_protected_overlap(
    target: Path,
    home: Path,
    *,
    case_insensitive: bool | None = None,
) -> Path | None:
    """Return the conflicting protected path if target equals, is inside, or is an ancestor of it."""
    ci = (sys.platform == 'darwin') if case_insensitive is None else case_insensitive
    t_lex = _norm_case(_norm_lexical(target), case_insensitive=ci)
    t_real = _norm_case(_norm_real(target), case_insensitive=ci)
    homes = [_norm_lexical(home)]
    real_home = _norm_lexical(Path.home())
    if _norm_case(real_home, case_insensitive=ci) not in [_norm_case(h, case_insensitive=ci) for h in homes]:
        homes.append(real_home)

    for candidate_home in homes:
        codex_lex = candidate_home / '.codex'
        codex_real = _norm_real(codex_lex)
        if _is_codex_state_db(t_lex, codex_lex, case_insensitive=ci) or _is_codex_state_db(t_real, codex_real, case_insensitive=ci):
            return _norm_lexical(target)
        # Also protect ancestors of codex state sqlite files if any exist.
        if codex_lex.is_dir():
            c_lex_cmp = _norm_case(codex_lex, case_insensitive=ci)
            c_real_cmp = _norm_case(codex_real, case_insensitive=ci)
            if _is_relative_to(c_lex_cmp, t_lex) or _is_relative_to(c_real_cmp, t_real):
                try:
                    entries = list(codex_lex.iterdir())
                except OSError:
                    entries = []
                for entry in entries:
                    if _is_codex_state_db(entry, codex_lex, case_insensitive=ci):
                        return entry
        for protected in protected_guest_paths(candidate_home):
            p_lex = _norm_case(_norm_lexical(protected), case_insensitive=ci)
            p_real = _norm_case(_norm_real(protected), case_insensitive=ci)
            for t_path, p_path in ((t_lex, p_lex), (t_real, p_real)):
                if t_path == p_path or _is_relative_to(t_path, p_path) or _is_relative_to(p_path, t_path):
                    return protected
    return None


def _has_symlink_between(anchor: Path, target: Path) -> bool:
    if anchor.is_symlink() or target.is_symlink():
        return True
    try:
        rel = _norm_lexical(target).relative_to(_norm_lexical(anchor))
    except ValueError:
        return True
    cur = _norm_lexical(anchor)
    for part in rel.parts:
        cur = cur / part
        if cur.is_symlink():
            return True
    return False


def _verify_isolated_manifest(allowed_root: Path) -> dict:
    manifest = allowed_root / ISOLATED_MANIFEST_NAME
    if manifest.is_symlink():
        raise ProtectedPathError('isolated manifest must not be a symlink')
    try:
        fd = os.open(manifest, os.O_RDONLY | os.O_NOFOLLOW)
    except OSError as exc:
        raise ProtectedPathError(f'missing or unreadable isolated manifest: {manifest}') from exc
    try:
        st = os.fstat(fd)
        if not stat.S_ISREG(st.st_mode) or st.st_size > 65536:
            raise ProtectedPathError('isolated manifest must be a bounded regular file')
        with os.fdopen(fd, 'r', encoding='utf-8') as handle:
            fd = -1
            data = json_object(handle.read(), error_cls=ProtectedPathError)
    finally:
        if fd >= 0:
            os.close(fd)
    root_real = str(_norm_real(allowed_root))
    if (
        data.get('schema') != 1
        or data.get('created_by') != ISOLATED_MANIFEST_CREATOR
        or data.get('isolated_root') != root_real
    ):
        raise ProtectedPathError('isolated manifest ownership or root binding mismatch')
    return data


def write_isolated_manifest(isolated_root: Path, *, run_id: str, extra: dict | None = None) -> Path:
    """Atomically write the ownership manifest inside an isolated UTM root."""
    if not isolated_root.is_absolute() or isolated_root.is_symlink():
        raise ProtectedPathError('isolated root must be an absolute non-symlink directory')
    root_real = _norm_real(isolated_root)
    payload = {
        'schema': 1,
        'created_by': ISOLATED_MANIFEST_CREATOR,
        'run_id': run_id,
        'isolated_root': str(root_real),
        **(extra or {}),
    }
    manifest = isolated_root / ISOLATED_MANIFEST_NAME
    tmp = isolated_root / f'.{ISOLATED_MANIFEST_NAME}.{os.getpid()}.tmp'
    fd = os.open(tmp, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
    try:
        with os.fdopen(fd, 'w', encoding='utf-8') as handle:
            fd = -1
            handle.write(json.dumps(payload, indent=2, ensure_ascii=False) + '\n')
            handle.flush()
            os.fsync(handle.fileno())
        os.replace(tmp, manifest)
    finally:
        if fd >= 0:
            os.close(fd)
        if tmp.exists():
            try:
                tmp.unlink()
            except OSError:
                pass
    return manifest


def safe_remove(
    target: Path,
    allowed_root: Path,
    *,
    home: Path | None = None,
    require_manifest: bool = True,
    allow_root_self: bool = False,
) -> bool:
    """Safely remove target only if inside an owned isolated_root and disjoint from protected paths."""
    if not target.is_absolute() or not allowed_root.is_absolute():
        raise ProtectedPathError('safe_remove requires absolute target and allowed_root paths')
    if allowed_root.is_symlink() or not allowed_root.is_dir():
        raise ProtectedPathError('allowed_root must be an existing non-symlink directory')
    if require_manifest:
        _verify_isolated_manifest(allowed_root)

    target_home = home if home is not None else Path.home()
    conflict = is_protected_overlap(target, target_home)
    if conflict is not None:
        raise ProtectedPathError(f'refused to touch protected path overlap: {conflict.name}')

    if target.is_symlink():
        raise ProtectedPathError('refused to remove symlink target')
    if _has_symlink_between(allowed_root, target):
        raise ProtectedPathError('refused symlink escape between allowed_root and target')

    root_real = _norm_real(allowed_root)
    target_real = _norm_real(target)
    if not _is_relative_to(target_real, root_real):
        raise ProtectedPathError('target escapes allowed_root')
    if target_real == root_real and not allow_root_self:
        raise ProtectedPathError('removing allowed_root itself requires allow_root_self=True')

    if not target.exists():
        return False
    if target.is_dir():
        shutil.rmtree(target)
    else:
        target.unlink()
    return True


def _snapshot_path_meta(path: Path) -> dict:
    try:
        st = os.lstat(path)
    except FileNotFoundError:
        return {'exists': False}
    except OSError as exc:
        return {'exists': False, 'error': type(exc).__name__}
    is_dir = stat.S_ISDIR(st.st_mode)
    is_file = stat.S_ISREG(st.st_mode)
    non_empty_dir = False
    if is_dir:
        try:
            non_empty_dir = any(path.iterdir())
        except OSError:
            non_empty_dir = False
    return {
        'exists': True,
        'is_dir': is_dir,
        'is_file': is_file,
        'is_symlink': stat.S_ISLNK(st.st_mode),
        'size': st.st_size if is_file else 0,
        'non_empty_dir': non_empty_dir,
        'ino': st.st_ino,
    }


class HostStateSentinel:
    """Snapshots metadata of host login paths before a run and detects deletion or truncation after."""

    def __init__(self, home: Path, *, protect_memlane_dir: bool = False):
        self.home = _norm_lexical(home)
        h = self.home
        targets = [
            ('trae_cn_app_support', h / 'Library' / 'Application Support' / 'Trae CN'),
            ('trae_cn_app_support_upper', h / 'Library' / 'Application Support' / 'TRAE CN'),
            ('trae_cn_jwt', h / '.trae-cn' / 'trae-jwt-token'),
            ('trae_cn_app', Path('/Applications/Trae CN.app')),
            ('trae_cn_app_upper', Path('/Applications/TRAE CN.app')),
            ('codex_auth', h / '.codex' / 'auth.json'),
            ('pi_auth', h / '.pi' / 'agent' / 'auth.json'),
            ('trae_cli_auth', h / '.trae' / 'cli' / 'auth.json'),
            ('traecli_auth', h / '.traecli' / 'cli' / 'auth.json'),
        ]
        if protect_memlane_dir:
            targets.append(('memlane_app_support', h / 'Library' / 'Application Support' / 'Memlane'))
        self.targets = targets
        self.before = {label: _snapshot_path_meta(path) for label, path in self.targets}

    def verify(self) -> dict:
        violations: list[dict] = []
        for label, path in self.targets:
            prev = self.before[label]
            if not prev.get('exists'):
                continue
            curr = _snapshot_path_meta(path)
            if not curr.get('exists'):
                violations.append({'label': label, 'reason': 'deleted'})
            elif prev.get('is_file') and prev.get('size', 0) > 0 and curr.get('size', 0) == 0:
                violations.append({'label': label, 'reason': 'truncated'})
            elif prev.get('is_dir') and prev.get('non_empty_dir') and not curr.get('non_empty_dir'):
                violations.append({'label': label, 'reason': 'emptied'})
        return {
            'ok': len(violations) == 0,
            'checked': len(self.targets),
            'violations': violations,
        }


def parse_scutil_proxy(output: str) -> dict:
    """Parse `scutil --proxy` output into HTTP/HTTPS/SOCKS proxy endpoints and exceptions."""
    kv: dict[str, str] = {}
    exceptions: list[str] = []
    in_exceptions = False
    for raw in output.splitlines():
        line = raw.strip()
        if not line:
            continue
        if line.startswith('ExceptionsList') and '{' in line:
            in_exceptions = True
            continue
        if in_exceptions:
            if line == '}':
                in_exceptions = False
                continue
            if ':' in line:
                val = line.split(':', 1)[1].strip()
                if val:
                    exceptions.append(val)
            continue
        if ':' in line:
            key, val = line.split(':', 1)
            kv[key.strip()] = val.strip()

    def build_url(scheme: str, enable_key: str, host_key: str, port_key: str) -> str:
        if kv.get(enable_key) != '1' or not kv.get(host_key):
            return ''
        host = kv[host_key]
        port = kv.get(port_key, '')
        if port and port != '0':
            host = f'{host}:{port}'
        return f'{scheme}://{host}'

    return {
        'http': build_url('http', 'HTTPEnable', 'HTTPProxy', 'HTTPPort'),
        'https': build_url('http', 'HTTPSEnable', 'HTTPSProxy', 'HTTPSPort'),
        'socks': build_url('socks5', 'SOCKSEnable', 'SOCKSProxy', 'SOCKSPort'),
        'exceptions': exceptions,
    }


def _safe_proxy_endpoint(raw_url: str) -> tuple[str, int] | None:
    parsed = urlparse(raw_url if '://' in raw_url else f'http://{raw_url}')
    if not parsed.hostname:
        return None
    port = parsed.port
    if port is None:
        port = 443 if parsed.scheme == 'https' else 80
    return parsed.hostname, port


def _matches_no_proxy(host: str, exceptions: list[str]) -> bool:
    h = host.lower().strip()
    if not h:
        return False
    for raw in exceptions:
        p = raw.lower().strip()
        if not p:
            continue
        if p == '*' or p == h:
            return True
        if p.startswith('*.') and h.endswith(p[1:]):
            return True
        if p.startswith('.') and h.endswith(p):
            return True
        if not p.startswith('.') and h.endswith('.' + p):
            return True
    return False


def _detect_active_proxy(
    target_host: str,
    env: dict[str, str],
    scutil_fn: Callable[[], str] | None = None,
) -> tuple[str, str]:
    no_proxy_raw = env.get('NO_PROXY') or env.get('no_proxy') or ''
    env_exceptions = [item.strip() for item in no_proxy_raw.split(',') if item.strip()]
    for key in ('HTTPS_PROXY', 'https_proxy', 'ALL_PROXY', 'all_proxy', 'HTTP_PROXY', 'http_proxy'):
        val = (env.get(key) or '').strip()
        if val:
            if _matches_no_proxy(target_host, env_exceptions):
                return '', ''
            return f'env:{key}', val
    if scutil_fn is None and sys.platform == 'darwin':
        def _run_scutil() -> str:
            res = subprocess.run(
                ['/usr/sbin/scutil', '--proxy'],
                capture_output=True, encoding='utf-8', timeout=SCUTIL_TIMEOUT_S, check=False,
            )
            return res.stdout if res.returncode == 0 else ''
        scutil_fn = _run_scutil
    if scutil_fn is None:
        return '', ''
    try:
        parsed = parse_scutil_proxy(scutil_fn())
    except (OSError, subprocess.SubprocessError):
        return '', ''
    if _matches_no_proxy(target_host, parsed['exceptions']):
        return '', ''
    for kind in ('https', 'http', 'socks'):
        if parsed[kind]:
            return f'scutil:{kind}', parsed[kind]
    return '', ''


class _NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


def probe_dns(
    mirror: str = 'china',
    *,
    extra_hosts: tuple[str, ...] = (),
    timeout_s: float = DNS_TIMEOUT_S,
    resolver: Callable[[str], object] | None = None,
) -> dict:
    started = time.monotonic()
    hosts = list(dict.fromkeys((*MIRROR_PROBE_HOSTS.get(mirror, MIRROR_PROBE_HOSTS['china']), *extra_hosts)))
    lookup = resolver or (lambda host: socket.getaddrinfo(host, 443, type=socket.SOCK_STREAM))
    failed: list[str] = []
    with ThreadPoolExecutor(max_workers=max(1, len(hosts))) as pool:
        futures = {host: pool.submit(lookup, host) for host in hosts}
        deadline = started + timeout_s
        for host, fut in futures.items():
            remaining = max(0.01, deadline - time.monotonic())
            try:
                res = fut.result(timeout=remaining)
                if not res:
                    failed.append(host)
            except (OSError, FutureTimeoutError, ValueError):
                failed.append(host)
    elapsed_ms = int((time.monotonic() - started) * 1000)
    if failed:
        return {
            'check': 'dns',
            'ok': False,
            'code': 'PREFLIGHT_DNS_FAILED',
            'mirror': mirror,
            'hosts': hosts,
            'failed_hosts': failed,
            'elapsed_ms': elapsed_ms,
            'remediation': '检查 UTM 客体 DNS 设置（已知 I-06），确认上述域名可解析后重试。',
        }
    return {
        'check': 'dns',
        'ok': True,
        'mirror': mirror,
        'hosts': hosts,
        'failed_hosts': [],
        'elapsed_ms': elapsed_ms,
    }


def probe_proxy(
    mirror: str = 'china',
    *,
    env: dict[str, str] | None = None,
    scutil_fn: Callable[[], str] | None = None,
    tcp_connect_fn: Callable[[tuple[str, int], float], object] | None = None,
    head_fn: Callable[[str, str, float], int] | None = None,
) -> dict:
    started = time.monotonic()
    effective_env = dict(os.environ) if env is None else dict(env)
    url = MIRROR_HEAD_URLS.get(mirror, MIRROR_HEAD_URLS['china'])
    target_host = urlparse(url).hostname or ''
    proxy_source, proxy_url = _detect_active_proxy(target_host, effective_env, scutil_fn=scutil_fn)
    endpoint_label = ''

    if proxy_url:
        ep = _safe_proxy_endpoint(proxy_url)
        if ep is None:
            return {
                'check': 'proxy',
                'ok': False,
                'code': 'PREFLIGHT_PROXY_BLOCKED',
                'proxy_source': proxy_source,
                'proxy_endpoint': 'invalid',
                'detail': 'invalid_proxy_url',
                'elapsed_ms': int((time.monotonic() - started) * 1000),
                'remediation': '检查环境变量或系统代理配置格式后重试。',
            }
        endpoint_label = f'{ep[0]}:{ep[1]}'
        connect = tcp_connect_fn or (lambda addr, t: socket.create_connection(addr, timeout=t))
        try:
            sock = connect(ep, PROXY_TCP_TIMEOUT_S)
            if hasattr(sock, 'close'):
                sock.close()
        except (socket.timeout, TimeoutError):
            return {
                'check': 'proxy',
                'ok': False,
                'code': 'PREFLIGHT_PROXY_BLOCKED',
                'proxy_source': proxy_source,
                'proxy_endpoint': endpoint_label,
                'detail': 'tcp_timeout',
                'elapsed_ms': int((time.monotonic() - started) * 1000),
                'remediation': '关闭冲突的宿主机/客体代理或确认代理端口正在监听后重试。',
            }
        except OSError:
            return {
                'check': 'proxy',
                'ok': False,
                'code': 'PREFLIGHT_PROXY_BLOCKED',
                'proxy_source': proxy_source,
                'proxy_endpoint': endpoint_label,
                'detail': 'tcp_refused',
                'elapsed_ms': int((time.monotonic() - started) * 1000),
                'remediation': '关闭冲突的宿主机/客体代理或确认代理端口正在监听后重试。',
            }

    # Skip urllib HEAD through SOCKS proxy (urllib lacks native SOCKS); TCP reachability already passed.
    if proxy_url and urlparse(proxy_url).scheme.startswith('socks'):
        return {
            'check': 'proxy',
            'ok': True,
            'proxy_source': proxy_source,
            'proxy_endpoint': endpoint_label,
            'elapsed_ms': int((time.monotonic() - started) * 1000),
        }

    def _default_head(target_url: str, active_proxy: str, timeout: float) -> int:
        handlers = [_NoRedirect()]
        if active_proxy:
            handlers.append(urllib.request.ProxyHandler({'http': active_proxy, 'https': active_proxy}))
        else:
            handlers.append(urllib.request.ProxyHandler({}))
        opener = urllib.request.build_opener(*handlers)
        req = urllib.request.Request(target_url, method='HEAD', headers={'User-Agent': 'memlane-preflight/1'})
        try:
            with opener.open(req, timeout=timeout) as resp:
                return int(getattr(resp, 'status', 200))
        except HTTPError as err:
            return int(err.code)

    do_head = head_fn or _default_head
    try:
        status = do_head(url, proxy_url, HEAD_TIMEOUT_S)
    except (URLError, socket.timeout, TimeoutError, OSError):
        return {
            'check': 'proxy',
            'ok': False,
            'code': 'PREFLIGHT_PROXY_BLOCKED',
            'proxy_source': proxy_source or 'direct',
            'proxy_endpoint': endpoint_label,
            'detail': 'head_timeout_or_error',
            'elapsed_ms': int((time.monotonic() - started) * 1000),
            'remediation': '镜像源 HTTPS 探活超时或被代理阻断，请检查客体网络与宿主机代理状态。',
        }

    if status in (407, 502, 503, 504):
        return {
            'check': 'proxy',
            'ok': False,
            'code': 'PREFLIGHT_PROXY_BLOCKED',
            'proxy_source': proxy_source or 'direct',
            'proxy_endpoint': endpoint_label,
            'detail': f'http_{status}',
            'elapsed_ms': int((time.monotonic() - started) * 1000),
            'remediation': f'代理或上游返回 HTTP {status}，请核对代理鉴权与连通性。',
        }
    return {
        'check': 'proxy',
        'ok': True,
        'proxy_source': proxy_source or 'direct',
        'proxy_endpoint': endpoint_label,
        'http_status': status,
        'elapsed_ms': int((time.monotonic() - started) * 1000),
    }


def _existing_ancestor(path: Path) -> Path:
    cur = _norm_lexical(path)
    while not cur.exists() and cur != cur.parent:
        cur = cur.parent
    return cur


def probe_disk(
    paths: tuple[Path, ...],
    *,
    min_bytes: int = MIN_INSTALL_DISK_BYTES,
    warn_bytes: int = WARN_INSTALL_DISK_BYTES,
    disk_usage_fn: Callable[[Path], object] = shutil.disk_usage,
) -> dict:
    started = time.monotonic()
    checked: list[dict] = []
    min_free: int | None = None
    for path in paths:
        anchor = _existing_ancestor(path)
        try:
            free = int(disk_usage_fn(anchor).free)
        except OSError:
            return {
                'check': 'disk',
                'ok': False,
                'code': 'PREFLIGHT_DISK_LOW',
                'min_bytes': min_bytes,
                'warn_bytes': warn_bytes,
                'elapsed_ms': int((time.monotonic() - started) * 1000),
                'remediation': '无法读取目标目录磁盘余量，请检查挂载路径权限。',
            }
        min_free = free if min_free is None else min(min_free, free)
        checked.append({'free_bytes': free})
    free_bytes = 0 if min_free is None else min_free
    elapsed_ms = int((time.monotonic() - started) * 1000)
    if free_bytes < min_bytes:
        return {
            'check': 'disk',
            'ok': False,
            'code': 'PREFLIGHT_DISK_LOW',
            'free_bytes': free_bytes,
            'min_bytes': min_bytes,
            'warn_bytes': warn_bytes,
            'elapsed_ms': elapsed_ms,
            'remediation': '可用磁盘空间低于安装底线，请释放空间后重试（严禁删除受保护白名单目录）。',
        }
    return {
        'check': 'disk',
        'ok': True,
        'warning': free_bytes < warn_bytes,
        'free_bytes': free_bytes,
        'min_bytes': min_bytes,
        'warn_bytes': warn_bytes,
        'elapsed_ms': elapsed_ms,
    }


def _inspect_jwt_exp(raw_text: str, now: float, min_remaining_s: int) -> tuple[bool, str, int | None]:
    """Validate JWT expiration in memory without ever returning or logging token text."""
    text = raw_text.strip()
    if not text:
        return False, 'token_empty', None
    token_str = text
    if text.startswith('{'):
        try:
            obj = json.loads(text)
        except ValueError:
            return False, 'token_invalid_json', None
        if not isinstance(obj, dict):
            return False, 'token_invalid_json', None
        found = ''
        for key in ('token', 'jwt', 'access_token', 'accessToken', 'id_token'):
            val = obj.get(key)
            if isinstance(val, str) and val.strip():
                found = val.strip()
                break
        if not found:
            return True, 'opaque_json_present', None
        token_str = found
    parts = token_str.split('.')
    if len(parts) != 3:
        return True, 'opaque_token_present', None
    payload_b64 = parts[1]
    padded = payload_b64 + '=' * (-len(payload_b64) % 4)
    try:
        decoded = base64.urlsafe_b64decode(padded.encode('ascii'))
        claims = json.loads(decoded.decode('utf-8'))
    except (ValueError, UnicodeDecodeError):
        return False, 'jwt_malformed', None
    if not isinstance(claims, dict):
        return False, 'jwt_malformed', None
    exp = claims.get('exp')
    if exp is None:
        return True, 'jwt_no_exp', None
    if not isinstance(exp, (int, float)) or isinstance(exp, bool):
        return False, 'jwt_invalid_exp', None
    remaining = int(exp - now)
    if remaining <= min_remaining_s:
        return False, 'jwt_expired', remaining
    return True, 'jwt_valid', remaining


def _read_private_regular_text(path: Path, *, require_private_mode: bool = False) -> tuple[str | None, str]:
    if path.is_symlink():
        return None, 'symlink_rejected'
    try:
        fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW)
    except FileNotFoundError:
        return None, 'file_missing'
    except OSError:
        return None, 'file_unreadable'
    try:
        st = os.fstat(fd)
        if not stat.S_ISREG(st.st_mode):
            return None, 'not_regular_file'
        if require_private_mode and (st.st_mode & 0o077) != 0:
            return None, 'insecure_permissions'
        if st.st_size == 0:
            return None, 'token_empty'
        if st.st_size > MAX_TOKEN_BYTES:
            return None, 'file_too_large'
        with os.fdopen(fd, 'r', encoding='utf-8') as handle:
            fd = -1
            return handle.read(), ''
    except (OSError, UnicodeDecodeError):
        return None, 'file_unreadable'
    finally:
        if fd >= 0:
            os.close(fd)


def probe_host_login(
    home: Path,
    *,
    require_login_agents: tuple[str, ...] = (),
    now: float | None = None,
    min_remaining_s: int = JWT_MIN_REMAINING_S,
    app_dirs: tuple[Path, ...] | None = None,
    which_fn: Callable[[str], str | None] = shutil.which,
) -> dict:
    started = time.monotonic()
    if not require_login_agents:
        return {'check': 'host_login', 'ok': True, 'checked_agents': [], 'elapsed_ms': 0}

    h = _norm_lexical(home)
    search_apps = app_dirs if app_dirs is not None else (Path('/Applications'), h / 'Applications')
    current_time = time.time() if now is None else now
    results: list[dict] = []
    failures: list[dict] = []

    for agent in require_login_agents:
        if agent == 'trae_ide':
            app_found = any((d / name).is_dir() for d in search_apps for name in ('Trae CN.app', 'TRAE CN.app'))
            if not app_found:
                entry = {'agent': agent, 'ok': False, 'reason': 'app_missing'}
                results.append(entry)
                failures.append(entry)
                continue
            support_dirs = (h / 'Library' / 'Application Support' / 'Trae CN', h / 'Library' / 'Application Support' / 'TRAE CN')
            has_support = any(d.is_dir() and not d.is_symlink() and any(d.iterdir()) for d in support_dirs)
            if not has_support:
                entry = {'agent': agent, 'ok': False, 'reason': 'app_support_missing_or_empty'}
                results.append(entry)
                failures.append(entry)
                continue
            text, err = _read_private_regular_text(h / '.trae-cn' / 'trae-jwt-token', require_private_mode=False)
            if err:
                entry = {'agent': agent, 'ok': False, 'reason': err}
                results.append(entry)
                failures.append(entry)
                continue
            ok, reason, remaining = _inspect_jwt_exp(text or '', current_time, min_remaining_s)
            entry = {'agent': agent, 'ok': ok, 'reason': reason}
            if remaining is not None:
                entry['expires_in_s'] = remaining
            results.append(entry)
            if not ok:
                failures.append(entry)
        elif agent in ('codex', 'pi', 'traex'):
            commands = {'codex': ('codex',), 'pi': ('pi',), 'traex': ('traecli', 'traex', 'trae-cli')}[agent]
            exe_found = any(bool(which_fn(cmd)) for cmd in commands)
            if not exe_found:
                extra_bins = (h / '.local/bin', h / '.bun/bin', Path('/opt/homebrew/bin'), Path('/usr/local/bin'))
                exe_found = any((b / cmd).is_file() and os.access(b / cmd, os.X_OK) for b in extra_bins for cmd in commands)
            if not exe_found and agent == 'codex':
                exe_found = any(
                    (d / app / 'Contents/Resources/codex-cli/bin/codex').is_file()
                    for d in search_apps for app in ('Codex.app', 'ChatGPT.app')
                )
            if not exe_found:
                entry = {'agent': agent, 'ok': False, 'reason': 'executable_missing'}
                results.append(entry)
                failures.append(entry)
                continue
            auth_candidates = {
                'codex': (h / '.codex' / 'auth.json',),
                'pi': (h / '.pi' / 'agent' / 'auth.json',),
                'traex': (h / '.trae' / 'cli' / 'auth.json', h / '.traecli' / 'cli' / 'auth.json'),
            }[agent]
            auth_path = next((p for p in auth_candidates if p.exists() or p.is_symlink()), auth_candidates[0])
            text, err = _read_private_regular_text(auth_path, require_private_mode=True)
            if err:
                entry = {'agent': agent, 'ok': False, 'reason': err}
                results.append(entry)
                failures.append(entry)
                continue
            try:
                parsed = json.loads(text or '')
                valid = isinstance(parsed, dict) and len(parsed) > 0
            except ValueError:
                valid = False
            entry = {'agent': agent, 'ok': valid, 'reason': 'auth_valid' if valid else 'auth_invalid_json'}
            results.append(entry)
            if not valid:
                failures.append(entry)

    elapsed_ms = int((time.monotonic() - started) * 1000)
    if failures:
        first = failures[0]
        out = {
            'check': 'host_login',
            'ok': False,
            'code': 'PREFLIGHT_HOST_LOGIN_MISSING_OR_EXPIRED',
            'agent': first['agent'],
            'reason': first['reason'],
            'checked_agents': results,
            'elapsed_ms': elapsed_ms,
            'remediation': '请先在 UTM 客体完成对应宿主登录，确认凭证未过期后再启动验收。',
        }
        if 'expires_in_s' in first:
            out['expires_in_s'] = first['expires_in_s']
        return out
    return {
        'check': 'host_login',
        'ok': True,
        'checked_agents': results,
        'elapsed_ms': elapsed_ms,
    }


def run_preflight(
    *,
    home: Path,
    component_root: Path,
    mirror: str = 'china',
    scenario: str = '',
    check_network: bool = True,
    extra_probe_hosts: tuple[str, ...] = (),
    require_login_agents: tuple[str, ...] = (),
    env: dict[str, str] | None = None,
) -> tuple[int, dict]:
    """Run P1-P4 probes concurrently within a ~3s budget. Returns (0 or 31, report)."""
    started = time.monotonic()
    tmpdir = Path((env or os.environ).get('TMPDIR') or '/tmp')
    min_disk = MIN_ZERO_CACHE_DISK_BYTES if scenario == 'clean-install' else MIN_INSTALL_DISK_BYTES
    disk_paths = (home, component_root, tmpdir)

    checks: dict[str, dict] = {}
    with ThreadPoolExecutor(max_workers=4) as pool:
        futures = {
            'disk': pool.submit(probe_disk, disk_paths, min_bytes=min_disk),
            'host_login': pool.submit(probe_host_login, home, require_login_agents=require_login_agents),
        }
        if check_network:
            futures['dns'] = pool.submit(probe_dns, mirror, extra_hosts=extra_probe_hosts)
            futures['proxy'] = pool.submit(probe_proxy, mirror, env=env)
        for name, fut in futures.items():
            checks[name] = fut.result()

    ordered = [checks[k] for k in ('dns', 'proxy', 'disk', 'host_login') if k in checks]
    failed = [item for item in ordered if not item.get('ok')]
    elapsed_ms = int((time.monotonic() - started) * 1000)
    ok = len(failed) == 0
    report = {
        'status': 'passed' if ok else 'preflight_env_blocked',
        'ok': ok,
        'returncode': 0 if ok else EXIT_PREFLIGHT_BLOCKED,
        'elapsed_ms': elapsed_ms,
        'checks': ordered,
        'failed_codes': [item.get('code', 'PREFLIGHT_FAILED') for item in failed],
    }
    return (0 if ok else EXIT_PREFLIGHT_BLOCKED), report
