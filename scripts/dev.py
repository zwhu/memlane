#!/usr/bin/env python3
"""Own the daily official Cognee service, completion endpoint and MyGo watcher."""
import argparse
import fcntl
import json
import os
from pathlib import Path
import platform
import re
import signal
import stat
import socket
import subprocess
import sys
import time
import urllib.error
import urllib.request
from contextlib import contextmanager
from datetime import datetime, timezone
import tempfile
import hashlib
import shutil

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / 'engine/python'))
GO = ROOT / '.local/toolchains/go-1.27.4/go/bin/go'
DEFAULT_CONFIG = ROOT / '.local/daily/runtime.json'
MONITOR_POLL_INTERVAL = 0.5
HOUSEKEEPING_INTERVAL = 10.0
STALE_RECEIPT_RE = re.compile(r'^\.(?:runtime-active|generation-applied)-(\d+)\.json$')
DEV_APP_PLIST = """<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
\t<key>CFBundleDisplayName</key>
\t<string>Memlane Dev</string>
\t<key>CFBundleExecutable</key>
\t<string>Memlane Dev</string>
\t<key>CFBundleIconFile</key>
\t<string>AppIcon.icns</string>
\t<key>CFBundleIdentifier</key>
\t<string>io.github.zwhu.memlane.dev</string>
\t<key>CFBundleInfoDictionaryVersion</key>
\t<string>6.0</string>
\t<key>CFBundleName</key>
\t<string>Memlane Dev</string>
\t<key>CFBundlePackageType</key>
\t<string>APPL</string>
\t<key>CFBundleShortVersionString</key>
\t<string>0.0.1</string>
\t<key>CFBundleVersion</key>
\t<string>0.0.1</string>
\t<key>LSMinimumSystemVersion</key>
\t<string>14.0</string>
\t<key>NSHighResolutionCapable</key>
\t<true/>
\t<key>NSPrincipalClass</key>
\t<string>NSApplication</string>
\t<key>NSSupportsAutomaticGraphicsSwitching</key>
\t<true/>
</dict>
</plist>
"""


def runtime_resource_identity():
    """Identify the private resource tree materialized and verified by the App.

    Checkout tasks keep their 50/55 GiB development reserve. An installed App
    must never run that checkout cleaner or inherit its free-space threshold.
    """
    marker = ROOT / '.memlane-runtime.json'
    try:
        info = marker.lstat()
    except FileNotFoundError:
        return None, None
    if not stat.S_ISREG(info.st_mode) or info.st_mode & 0o077:
        raise RuntimeError('App runtime resource marker must be a private regular file')
    identity = json.loads(marker.read_text())
    digest = identity.get('sha256', '')
    if (not isinstance(digest, str) or len(digest) != 64 or
            any(character not in '0123456789abcdef' for character in digest) or
            ROOT.name != 'runtime-' + digest[:16]):
        raise RuntimeError('App runtime resource identity differs')
    return str(ROOT), digest[:16]


def app_runtime_resources():
    return runtime_resource_identity()[0] is not None


def complete_initialization(config, config_path, now=None):
    """Commit the first successful setup once, including existing ready installs."""
    request = urllib.request.Request(config['service_url'].rstrip('/') + '/api/v1/datasets',
                                     headers={'X-Api-Key': Path(config['token_file']).read_text().strip()})
    with urllib.request.urlopen(request, timeout=10) as response:
        if response.status != 200:
            raise RuntimeError('authenticated content readiness failed')
    if config.get('generation_mode', 'agent_bridge') == 'agent_bridge':
        with urllib.request.urlopen(config['completion_url'].rstrip('/') + '/health', timeout=10) as response:
            if response.status != 200:
                raise RuntimeError('completion readiness failed')
    path = Path(config.get('client_state_file') or Path(config_path).parent / 'client-state.json')
    if not path.is_absolute():
        raise ValueError('client_state_file must be absolute')
    path.parent.mkdir(parents=True, exist_ok=True)
    with path.with_suffix('.lock').open('a+') as lock:
        os.chmod(lock.name, 0o600)
        fcntl.flock(lock.fileno(), fcntl.LOCK_EX)
        if path.exists():
            state = json.loads(path.read_text())
            if state.get('schema_version') != 1:
                raise ValueError('unsupported client state; retained unchanged')
            datetime.strptime(state['first_used_date'], '%Y-%m-%d')
            completed = datetime.fromisoformat(state['initialization_completed_at'].replace('Z', '+00:00'))
            if completed.tzinfo is None or not state.get('first_used_timezone'):
                raise ValueError('invalid initialization state; retained unchanged')
            return state
        instant = now or datetime.now().astimezone()
        if instant.tzinfo is None:
            raise ValueError('initialization time must include timezone')
        state = {'schema_version': 1,
                 'initialization_completed_at': instant.astimezone(timezone.utc).isoformat(),
                 'first_used_date': instant.date().isoformat(),
                 'first_used_timezone': str(instant.tzinfo)}
        temporary = None
        try:
            with tempfile.NamedTemporaryFile(mode='w', dir=path.parent, prefix='.client-state-', delete=False) as stream:
                temporary = Path(stream.name)
                os.chmod(temporary, 0o600)
                json.dump(state, stream, indent=2)
                stream.write('\n')
                stream.flush()
                os.fsync(stream.fileno())
            os.replace(temporary, path)
            directory = os.open(path.parent, os.O_RDONLY)
            try:
                os.fsync(directory)
            finally:
                os.close(directory)
        finally:
            if temporary is not None:
                temporary.unlink(missing_ok=True)
        return state


def load_config(path, *, verify_current_sources=True):
    config = json.loads(path.read_text())
    from agent_environments import validate_daily_environments
    if verify_current_sources:
        validate_daily_environments(config)
    for key in ('official_data', 'official_source', 'python', 'token_file', 'official_archive', 'service_environment_file'):
        target = Path(config[key]).expanduser().resolve(strict=True)
        # Resolving a venv's Python symlink bypasses its pyvenv.cfg and dependencies.
        config[key] = str(Path(config[key]).expanduser().absolute()) if key == 'python' else str(target)
    for key in ('token_file', 'service_environment_file'):
        if Path(config[key]).stat().st_mode & 0o077:
            raise ValueError(key + ' must be private (0600)')
    for key in ('completion_token_file', 'codex_executable', 'auth_home'):
        if config.get(key):
            config[key] = str(Path(config[key]).expanduser().absolute())
    data = Path(config['official_data'])
    if not data.is_dir() or not (data / 'system').is_dir() or not (data / 'data').is_dir():
        raise ValueError('official_data must name the existing daily store; no empty database will be created')
    expected = json.loads((ROOT / 'engine/coding-memory-components.json').read_text())['service']['commit']
    if config.get('service_commit') != expected:
        raise ValueError('explicit fixed official service commit required')
    for key in ('service_url', 'completion_url'):
        if key == 'completion_url' and key not in config:
            continue
        from urllib.parse import urlparse
        url = urlparse(config[key])
        if url.scheme != 'http' or url.hostname != '127.0.0.1' or not url.port or url.path not in ('', '/'):
            raise ValueError(key + ' must be an explicit loopback HTTP origin')
    control = config.get('project_control')
    if control is not None:
        validate_project_control(control, config, verify_plugin=verify_current_sources)
    return config


def validate_project_control(control, config, *, verify_plugin=True):
    from urllib.parse import urlparse
    if not isinstance(control, dict) or set(control) - {'url', 'registry_file', 'management_token_file', 'agent_control_token_files', 'plugin_dir'}:
        raise ValueError('invalid project_control configuration')
    parsed = urlparse(control['url'])
    if (parsed.scheme != 'http' or parsed.hostname != '127.0.0.1' or not parsed.port
            or parsed.path not in ('', '/') or parsed.query or parsed.fragment or parsed.username):
        raise ValueError('project_control.url must be an explicit loopback origin')
    if parsed.port in {urlparse(config[key]).port for key in ('service_url', 'completion_url') if config.get(key)}:
        raise ValueError('project control requires its own explicit port')
    for key in ('registry_file', 'management_token_file'):
        if not Path(control[key]).is_absolute():
            raise ValueError('project control configuration paths must be absolute')
    credentials = [control['management_token_file'], *control.get('agent_control_token_files', {}).values()]
    values = []
    for credential in credentials:
        target = Path(credential)
        if not target.is_absolute() or target.is_symlink() or target.stat().st_mode & 0o077:
            raise ValueError('project control credential must be a private regular file')
        value = target.read_text().strip()
        if len(value) < 32 or value in values:
            raise ValueError('project control credentials must be independent and at least 32 characters')
        values.append(value)
    if control.get('plugin_dir') and verify_plugin:
        verify_managed_plugin(Path(control['plugin_dir']))


def verify_managed_plugin(directory):
    if not directory.is_absolute():
        raise ValueError('managed plugin directory must be absolute')
    from _runtime_common import match_managed_plugin_manifest
    from project_policy import PolicyError
    receipt = json.loads((directory / '.memlane-project-access.json').read_text())
    try:
        expected, current = match_managed_plugin_manifest(ROOT / 'engine', receipt)
    except PolicyError as error:
        raise ValueError('managed plugin receipt does not match fixed component input') from error
    for path, key in (('engine/patches/project_access.py', 'patch_sha256'),
                      ('engine/python/project_plugin_policy.py', 'extension_sha256'),
                      ('engine/patches/project-access-upstream.json', 'upstream_lock_sha256')):
        if current and hashlib.sha256((ROOT / path).read_bytes()).hexdigest() != expected[key]:
            raise ValueError('managed plugin patch source changed; rebuild and review the fixed input')
    files = {str(path.relative_to(directory)): hashlib.sha256(path.read_bytes()).hexdigest()
             for path in directory.rglob('*') if path.is_file() and '__pycache__' not in path.parts
             and path.suffix != '.pyc' and path.name not in ('.memlane-project-access.json', '.memlane-installation.json')}
    if files != expected['files']:
        raise ValueError('managed plugin files do not match fixed input')
    marker = directory / '.memlane-installation.json'
    if marker.exists() or marker.is_symlink():
        if marker.is_symlink() or marker.stat().st_mode & 0o077:
            raise ValueError('managed installation binding must be private')
        environment = Path(json.loads(marker.read_text())['env_file'])
        if not environment.is_absolute() or environment.is_symlink() or environment.stat().st_mode & 0o077:
            raise ValueError('managed installation environment must be private')


def wait_project_control(control, process, timeout=60, *, setup_only=False):
    def probe():
        request = urllib.request.Request(control['url'].rstrip('/') + ('/v1/generation' if setup_only else '/v1/projects'),
                                         headers={'Authorization': 'Bearer ' + Path(control['management_token_file']).read_text().strip()})
        with urllib.request.urlopen(request, timeout=1) as response:
            return response.status == 200 and isinstance(json.load(response).get('revision'), int)
    wait_ready(probe, process, timeout)


def owner_ui_children(state):
    # Old records had exactly two services; new records explicitly name service
    # roles so the project control process is never mistaken for a UI watcher.
    if 'service_children' in state:
        services = set(state['service_children'].values())
        return [pid for pid in state.get('children', []) if pid not in services]
    return state.get('children', [])[2:]


def launch_owned(command, *, cwd=ROOT, env=None, log_file=None):
    if log_file is None:
        raise ValueError('owned process requires an independent log file')
    log_file = Path(log_file)
    log_file.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
    fd = os.open(log_file, os.O_WRONLY | os.O_CREAT | os.O_APPEND | os.O_NOFOLLOW, 0o600)
    try:
        if not stat.S_ISREG(os.fstat(fd).st_mode):
            raise ValueError('owned process log must be a regular file')
        os.fchmod(fd, 0o600)
        return subprocess.Popen(command, cwd=cwd, env=env, start_new_session=True,
                                stdin=subprocess.DEVNULL, stdout=fd, stderr=subprocess.STDOUT)
    finally:
        os.close(fd)


LOG_REDACTION_RULES = (
    (re.compile(rb'(?i)(Bearer[ \t]+)\S+'), rb'\1[REDACTED]'),
    (re.compile(rb'(?i)(Basic[ \t]+)\S+'), rb'\1[REDACTED]'),
    (re.compile(rb'(?i)(X-Api-Key[ \t]*:[ \t]*)\S+'), rb'\1[REDACTED]'),
    (re.compile(rb'(?i)(X-Memlane-Management[ \t]*:[ \t]*)\S+'), rb'\1[REDACTED]'),
    (re.compile(rb'\bsk-[A-Za-z0-9_-]{8,}'), rb'sk-[REDACTED]'),
    (re.compile(rb'\beyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\b'), rb'[REDACTED]'),
    (re.compile(rb'(?i)((?:https?|socks(?:4a?|5h?))://)[^/\s"\'<>@]+@'), rb'\1[REDACTED]@'),
    (re.compile(rb'(?i)(\b(?:api[_-]?key|access[_-]?token|auth[_-]?token|token|secret|password)\b[ \t]*[=:][ \t]*)([^\s,;]+)'), rb'\1[REDACTED]'),
)


def redact_log_bytes(raw):
    redacted = raw
    for pattern, replacement in LOG_REDACTION_RULES:
        redacted = pattern.sub(replacement, redacted)
    return redacted


def trim_service_log(path, limit=20 * 1024 * 1024):
    path = Path(path)
    if path.is_symlink():
        raise ValueError('service log cannot be a symlink')
    if not path.exists():
        return
    src_fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW)
    try:
        src_stat = os.fstat(src_fd)
        if not stat.S_ISREG(src_stat.st_mode):
            raise ValueError('service log must be a regular file')
        if src_stat.st_size <= limit:
            return
        offset = src_stat.st_size - limit
        # Keep a bounded tail and truncate the same live inode. Child processes
        # retain an O_APPEND regular-file fd, never a pipe or replaced log inode.
        with os.fdopen(src_fd, 'rb') as source:
            src_fd = -1
            if offset > 0:
                source.seek(offset - 1, os.SEEK_SET)
                raw = source.read(limit + 1)
                if len(raw) > 1:
                    prev_byte = raw[:1]
                    tail = raw[1:]
                else:
                    prev_byte = b''
                    tail = b''
            else:
                prev_byte = b'\n'
                tail = source.read(limit)
    finally:
        if src_fd >= 0:
            os.close(src_fd)
    if offset > 0 and prev_byte != b'\n':
        newline = tail.find(b'\n')
        if newline >= 0:
            tail = tail[newline + 1:]
        else:
            tail = b''
    redacted_tail = redact_log_bytes(tail)
    prev_path = Path(str(path) + '.previous')
    if prev_path.is_symlink():
        raise ValueError('previous service log cannot be a symlink')
    fd = os.open(prev_path, os.O_WRONLY | os.O_CREAT | os.O_TRUNC | os.O_NOFOLLOW, 0o600)
    try:
        if not stat.S_ISREG(os.fstat(fd).st_mode):
            raise ValueError('previous service log must be a regular file')
        os.fchmod(fd, 0o600)
        with os.fdopen(fd, 'wb') as previous:
            fd = -1
            previous.write(redacted_tail)
            previous.flush()
            os.fsync(previous.fileno())
    finally:
        if fd >= 0:
            os.close(fd)
    fd = os.open(path, os.O_WRONLY | os.O_NOFOLLOW)
    try:
        if not stat.S_ISREG(os.fstat(fd).st_mode):
            raise ValueError('service log must be a regular file')
        os.ftruncate(fd, 0)
    finally:
        os.close(fd)


def report_notice(message):
    # A terminal observer is optional; losing it must never stop owned services.
    try:
        print(message, flush=True)
    except OSError:
        pass


def acquire(data):
    # Canonical store ownership is shared across checkouts and symlink aliases.
    lock_path = Path(data).resolve(strict=True) / '.service-owner'
    if lock_path.is_symlink():
        raise ValueError('service owner lock cannot be a symlink')
    fd = os.open(lock_path, os.O_RDWR | os.O_CREAT | os.O_NOFOLLOW, 0o600)
    try:
        if not stat.S_ISREG(os.fstat(fd).st_mode):
            raise ValueError('service owner lock must be a regular file')
        os.fchmod(fd, 0o600)
        fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
    except BaseException:
        os.close(fd)
        raise
    return os.fdopen(fd, 'r+')


def read_owner_record(runfile):
    runfile = Path(runfile)
    if runfile.is_symlink():
        raise ValueError('owner receipt cannot be a symlink')
    fd = os.open(runfile, os.O_RDONLY | os.O_NOFOLLOW)
    try:
        if not stat.S_ISREG(os.fstat(fd).st_mode):
            raise ValueError('owner receipt must be a regular file')
        with os.fdopen(fd, 'r', encoding='utf-8') as stream:
            fd = -1
            return json.load(stream)
    finally:
        if fd >= 0:
            os.close(fd)


def write_owner_record(runfile, state):
    runfile = Path(runfile)
    if runfile.is_symlink():
        raise ValueError('owner receipt cannot be a symlink')
    temporary = runfile.with_name(runfile.name + '.' + str(os.getpid()) + '.tmp')
    fd = os.open(temporary, os.O_WRONLY | os.O_CREAT | os.O_TRUNC | os.O_NOFOLLOW, 0o600)
    replaced = False
    try:
        if not stat.S_ISREG(os.fstat(fd).st_mode):
            raise ValueError('owner receipt temp must be a regular file')
        os.fchmod(fd, 0o600)
        with os.fdopen(fd, 'w', encoding='utf-8') as stream:
            fd = -1
            json.dump(state, stream)
            stream.flush()
            os.fsync(stream.fileno())
        if runfile.is_symlink():
            raise ValueError('owner receipt cannot be a symlink')
        os.replace(temporary, runfile)
        replaced = True
        dir_fd = os.open(runfile.parent, os.O_RDONLY)
        try:
            os.fsync(dir_fd)
        finally:
            os.close(dir_fd)
    finally:
        if fd >= 0:
            os.close(fd)
        if not replaced:
            temporary.unlink(missing_ok=True)


def group_exists(pid):
    try:
        os.killpg(pid, 0)
        return True
    except ProcessLookupError:
        return False
    except PermissionError:
        # macOS may deny killpg(0) during exit; inspect the kernel process table.
        rows = subprocess.check_output(['ps', '-axo', 'pgid='], text=True)
        return any(row.strip() == str(pid) for row in rows.splitlines())


def stop_group(process, *, grace=4, kill_wait=2):
    for sig, seconds in ((signal.SIGTERM, grace), (signal.SIGKILL, kill_wait)):
        process.poll()
        if not group_exists(process.pid):
            process.wait(timeout=5)
            return
        try: os.killpg(process.pid, sig)
        except ProcessLookupError: pass
        except PermissionError:
            # macOS can return EPERM when the child exits between the check
            # and signal. Confirm absence; a live denied group still fails.
            process.poll()
            if group_exists(process.pid):
                raise
        deadline = time.monotonic() + seconds
        while time.monotonic() < deadline:
            process.poll()
            if not group_exists(process.pid):
                process.wait(timeout=5)
                return
            time.sleep(.1)
    raise RuntimeError('owned process group exit unconfirmed: ' + str(process.pid))


def stop_groups_parallel(processes, *, grace=4, kill_wait=2):
    """Stop owned process groups in reverse order with a shared SIGTERM -> SIGKILL budget."""
    remaining = list(reversed(processes))
    errors = []
    for sig, seconds in ((signal.SIGTERM, grace), (signal.SIGKILL, kill_wait)):
        still_live = []
        for proc in remaining:
            proc.poll()
            if not group_exists(proc.pid):
                try:
                    proc.wait(timeout=2)
                except Exception as error:
                    errors.append(str(error))
                continue
            try:
                os.killpg(proc.pid, sig)
            except ProcessLookupError:
                try:
                    proc.wait(timeout=2)
                except Exception as error:
                    errors.append(str(error))
                continue
            except PermissionError as error:
                proc.poll()
                if group_exists(proc.pid):
                    errors.append(str(error))
                continue
            still_live.append(proc)
        remaining = still_live
        if not remaining:
            return errors
        deadline = time.monotonic() + seconds
        while remaining and time.monotonic() < deadline:
            next_live = []
            for proc in remaining:
                proc.poll()
                if not group_exists(proc.pid):
                    try:
                        proc.wait(timeout=2)
                    except Exception as error:
                        errors.append(str(error))
                else:
                    next_live.append(proc)
            remaining = next_live
            if remaining:
                time.sleep(.1)
    for proc in remaining:
        proc.poll()
        if group_exists(proc.pid):
            errors.append('owned process group exit unconfirmed: ' + str(proc.pid))
        else:
            try:
                proc.wait(timeout=2)
            except Exception as error:
                errors.append(str(error))
    return errors


def ui_arch():
    machine = platform.machine().lower()
    return 'arm64' if machine in ('arm64', 'aarch64') else 'amd64'


def ui_binary_path(root=None):
    base = Path(root) if root is not None else ROOT
    return base / '.mygo/dev' / ('darwin-' + ui_arch()) / 'Memlane Dev.app/Contents/MacOS/Memlane Dev'


def ui_build_stale(binary, root=None):
    binary = Path(binary)
    if not binary.is_file() or not os.access(binary, os.X_OK):
        return True
    binary_mtime = binary.stat().st_mtime
    base = Path(root) if root is not None else ROOT
    for rel in ('go.mod', 'go.sum', 'mygo.json', 'scripts/patches/mygo-antialiased.json'):
        candidate = base / rel
        if candidate.is_file() and candidate.stat().st_mtime > binary_mtime:
            return True
    for folder in ('cmd', 'internal', 'assets'):
        root_dir = base / folder
        if not root_dir.is_dir():
            continue
        for current_root, dirs, files in os.walk(root_dir):
            dirs[:] = [d for d in dirs if d != '__pycache__' and not (Path(current_root) / d).is_symlink()]
            for name in files:
                if name == '.DS_Store' or name.endswith('_test.go'):
                    continue
                if folder in ('cmd', 'internal') and not name.endswith(('.go', '.png', '.svg')):
                    continue
                candidate = Path(current_root) / name
                if candidate.is_symlink():
                    continue
                if candidate.stat().st_mtime > binary_mtime:
                    return True
    return False


def _sync_dev_app_icon(contents_dir, base):
    src_png = Path(base) / 'assets/branding/memlane-icon.png'
    if platform.system() != 'Darwin' or not src_png.is_file():
        return
    resources = Path(contents_dir) / 'Resources'
    icns = resources / 'AppIcon.icns'
    src_mtime = src_png.stat().st_mtime
    if icns.is_file() and icns.stat().st_mtime >= src_mtime:
        return
    specs = [
        ('icon_16x16.png', 16),
        ('icon_16x16@2x.png', 32),
        ('icon_32x32.png', 32),
        ('icon_32x32@2x.png', 64),
        ('icon_128x128.png', 128),
        ('icon_128x128@2x.png', 256),
        ('icon_256x256.png', 256),
        ('icon_256x256@2x.png', 512),
        ('icon_512x512.png', 512),
        ('icon_512x512@2x.png', 1024),
    ]
    resources.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix='memlane-iconset-') as tmpdir:
        tmp = Path(tmpdir)
        iconset = tmp / 'AppIcon.iconset'
        iconset.mkdir()
        for name, px in specs:
            subprocess.run(
                ['sips', '-z', str(px), str(px), str(src_png), '--out', str(iconset / name)],
                check=True,
                stdout=subprocess.DEVNULL,
                stderr=subprocess.DEVNULL,
            )
        built_icns = tmp / 'AppIcon.icns'
        subprocess.run(
            ['iconutil', '-c', 'icns', str(iconset), '-o', str(built_icns)],
            check=True,
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
        )
        stage_icns = resources / '.AppIcon.icns.tmp'
        shutil.copy2(built_icns, stage_icns)
        os.replace(stage_icns, icns)
    os.utime(Path(contents_dir).parent, None)


def _ensure_dev_app_bundle(binary, root=None):
    binary = Path(binary)
    base = Path(root) if root is not None else ROOT
    macos_dir = binary.parent
    contents_dir = macos_dir.parent
    macos_dir.mkdir(parents=True, exist_ok=True)
    pkginfo = contents_dir / 'PkgInfo'
    if not pkginfo.exists():
        pkginfo.write_text('APPL????')
    plist = contents_dir / 'Info.plist'
    if not plist.exists():
        plist.write_text(DEV_APP_PLIST)
    _sync_dev_app_icon(contents_dir, base)


def ui_runtime_env(config_path):
    return dict(os.environ, MEMLANE_DEV_RUNTIME_CONFIG=str(config_path))


def ensure_ui_binary(config_path, *, root=None, env=None):
    base = Path(root) if root is not None else ROOT
    binary = ui_binary_path(base)
    if ui_build_stale(binary, base):
        _ensure_dev_app_bundle(binary, root=base)
        shutil.rmtree(binary.parent.parent / '_CodeSignature', ignore_errors=True)
        build_env = env if env is not None else ui_env(config_path)
        subprocess.run([str(GO), 'build', '-o', str(binary), './cmd/memlane-app'],
                       cwd=base, env=build_env, check=True)
    else:
        _sync_dev_app_icon(binary.parent.parent, base)
    return binary


def cleanup_stale_pid_receipts(directory, *, current_pid=None):
    directory = Path(directory)
    if not directory.is_dir():
        return []
    me = os.getpid() if current_pid is None else current_pid
    removed = []
    for candidate in directory.iterdir():
        match = STALE_RECEIPT_RE.match(candidate.name)
        if not match or candidate.is_symlink() or not candidate.is_file():
            continue
        pid = int(match.group(1))
        if pid <= 0 or pid == me:
            continue
        try:
            os.kill(pid, 0)
            continue
        except ProcessLookupError:
            pass
        except PermissionError:
            continue
        candidate.unlink(missing_ok=True)
        removed.append(str(candidate))
    return removed


def run_monitor_housekeeping(log_dir, processes, descendants, save_owner, *,
                             last_run=None, now=None, interval=HOUSEKEEPING_INTERVAL):
    current = time.monotonic() if now is None else now
    if last_run is not None and (current - last_run) < interval:
        return last_run
    log_path = Path(log_dir)
    if log_path.is_dir() and not log_path.is_symlink():
        for log_file in log_path.glob('*.log'):
            if log_file.is_symlink() or not log_file.is_file():
                continue
            try:
                trim_service_log(log_file)
            except (OSError, ValueError) as error:
                report_notice('skipping unsafe service log ' + str(log_file) + ': ' + str(error))
    previous_descendants = dict(descendants)
    track_descendants([p.pid for p in processes], descendants)
    if descendants != previous_descendants:
        save_owner()
    return current


def reopen_ui(current, processes, launch, config_path, *, watch_ui=False):
    try:
        if current is not None:
            stop_group(current)
            processes.remove(current)
            current = None
        if watch_ui:
            return launch([str(GO), 'tool', 'mygo', 'dev'], env=ui_env(config_path))
        binary = ensure_ui_binary(config_path)
        return launch([str(binary)], env=ui_runtime_env(config_path))
    except Exception as error:
        # A window-only failure must not tear down the daily data services.
        # Keep the old watcher reserved if its exit could not be confirmed.
        report_notice('UI reopen failed; daily services retained: ' + str(error))
        return current


def process_tree():
    rows = subprocess.check_output(['ps', '-axo', 'pid=,ppid=,pgid=,lstart='], text=True)
    result = {}
    for row in rows.splitlines():
        fields = row.split(None, 3)
        if len(fields) == 4:
            result[int(fields[0])] = (int(fields[1]), int(fields[2]), fields[3])
    return result


def track_descendants(roots, known):
    table = process_tree()
    # A PID is not a durable identity. Retain only still-live observations;
    # reparented descendants survive this filter, exited/reused PIDs do not.
    for pid, started in list(known.items()):
        if pid not in table or table[pid][2] != started:
            del known[pid]
    parents = set(roots)
    while True:
        children = {pid for pid, value in table.items() if value[0] in parents}
        new = children - parents
        if not new: break
        parents.update(new)
    for pid in parents - set(roots):
        if pid in table: known[pid] = table[pid][2]


def stop_descendants(known, *, grace=1.5, kill_wait=1.5):
    # Codex owns request sessions distinct from the endpoint group. Only signal
    # previously observed descendants whose kernel start identity still matches.
    if not known:
        return
    for sig, seconds in ((signal.SIGTERM, grace), (signal.SIGKILL, kill_wait)):
        # One snapshot per phase, not a full-system ps for every historical PID.
        # Recheck identities on each phase and poll; never signal a reused PID.
        table = process_tree()
        live = {pid: started for pid, started in known.items()
                if pid in table and table[pid][2] == started}
        if not live:
            return
        for pid in live:
            try: os.kill(pid, sig)
            except ProcessLookupError: pass
        deadline = time.monotonic() + seconds
        while time.monotonic() < deadline:
            table = process_tree()
            if not any(pid in table and table[pid][2] == started for pid, started in known.items()): return
            time.sleep(.1)
    raise RuntimeError('owned descendant exit remains unconfirmed')


class ReadinessError(RuntimeError):
    pass


def wait_ready(probe, process, timeout):
    deadline = time.monotonic() + timeout
    last = 'no_response'
    while time.monotonic() < deadline:
        code = process.poll()
        if code is not None:
            raise ReadinessError(f'process_exited exit_code={code} last_probe={last}')
        try:
            if probe():
                return
            last = 'unexpected_response'
        except urllib.error.HTTPError as error:
            last = f'http_{error.code}'
            error.close()
        except (OSError, ValueError) as error:
            last = type(error).__name__
        time.sleep(.2)
    raise ReadinessError(f'readiness_timeout last_probe={last}')


def wait_http(url, process, timeout=60):
    def probe():
        with urllib.request.urlopen(url, timeout=1) as response:
            return response.status == 200
    wait_ready(probe, process, timeout)


@contextmanager
def startup_stage(stage):
    started = time.monotonic()
    try:
        yield
    except Exception as error:
        # Ordinary exceptions can contain URLs, credentials or response bodies.
        reason = str(error) if type(error) is ReadinessError else type(error).__name__
        report_notice(f'[startup] stage={stage} result=failed elapsed_ms={int((time.monotonic() - started) * 1000)} reason={reason}')
        raise


def port_free(url):
    from urllib.parse import urlparse
    parsed = urlparse(url)
    with socket.socket() as sock:
        # Match the servers' bind semantics: TIME_WAIT is restartable, an
        # existing listener on the same address is still a conflict.
        sock.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
        sock.bind(('127.0.0.1', parsed.port))


def ui_env(config_path):
    from mygo_sdk import with_patched_sdk
    env = dict(os.environ, GOPATH=str(ROOT / '.local/gopath'), GOCACHE=str(ROOT / '.cache/go-build'),
               GOTOOLCHAIN='local', CGO_ENABLED='0', MEMLANE_DEV_RUNTIME_CONFIG=str(config_path))
    env['PATH'] = str(GO.parent) + ':' + env.get('PATH', '')
    return with_patched_sdk(env)


def generation_setup_runtime(config, config_path):
    """Configuration only; never seed active, start an engine, or initialize data."""
    from project_control import private_write
    if not config.get('project_control') or not config.get('generation_file') or not Path(config['generation_file']).is_absolute():
        raise ValueError('generation setup requires project_control and absolute generation_file')
    effective = {**config, 'generation_setup_only': True, 'generation_mode': 'unconfigured',
                 'model': '', 'graph_assets': ''}
    effective.pop('completion_url', None)
    runtime_path = config_path.parent / ('.runtime-active-' + str(os.getpid()) + '.json')
    private_write(runtime_path, json.dumps(effective).encode())
    return effective, runtime_path


def first_install_runtime(config, config_path, launch, *, health=wait_http):
    """Start authenticated Agent management without a generation model."""
    from first_install_mode import first_install_setup
    from first_install_runtime import confirm_service_owner
    from project_control import private_write
    if not first_install_setup(config):
        raise ValueError('explicit first installation setup required')
    runtime_path = config_path.parent / ('.runtime-active-' + str(os.getpid()) + '.json')
    private_write(runtime_path, json.dumps(config).encode())
    port_free(config['service_url'])
    service = launch([config['python'], str(ROOT / 'engine/python/official_service_dev.py'), '--config', str(runtime_path)])
    health(config['service_url'] + '/health', service)
    confirm_service_owner(config)
    return config, runtime_path, None, service


def start_generation_stack(config, config_path, launch, cleanup, *, health=wait_http):
    """Start one immutable revision while holding the generation writer lock.

    `launch` and `cleanup` are the launcher's existing process-owner operations.
    Hand the same private config lock to project_control only after this returns.
    """
    from generation_config import GenerationStore
    from generation_startup import start_generation
    from project_control import private_write
    from urllib.parse import urlparse
    generation_file = Path(config.get('generation_file') or config_path.parent / 'generation.json')
    if not generation_file.is_absolute():
        raise ValueError('generation_file must be absolute')
    store = GenerationStore(generation_file, config['service_commit'], legacy_model=config.get('model'))
    runtime_path = config_path.parent / ('.runtime-active-' + str(os.getpid()) + '.json')
    applied_path = config_path.parent / ('.generation-applied-' + str(os.getpid()) + '.json')
    try:
        def start(reference, digest):
            row = store.resolve(reference)
            profile = row['profile']
            effective = {**config, 'generation_file': str(generation_file), 'generation_reference': reference,
                         'generation_mode': profile['mode'], 'model': profile['model'],
                         'generation_applied_file': str(applied_path)}
            if profile['mode'] == 'provider':
                effective.pop('completion_url', None)
            endpoint = None
            if profile['mode'] == 'agent_bridge':
                with startup_stage('agent_bridge_configuration'):
                    from agent_bridge_runtime import require_bridge_startup
                    selected_bridge = require_bridge_startup(store, config, row)
                    token = Path(config['completion_token_file']).expanduser()
                    if token.is_symlink():
                        raise ValueError('completion credential must not be a symlink')
                    token = token.resolve(strict=True)
                    if token.stat().st_mode & 0o077:
                        raise ValueError('completion credential must be private')
                    effective['completion_token_file'] = str(token)
                with startup_stage('agent_bridge_port'):
                    port_free(effective['completion_url'])
            applied_path.unlink(missing_ok=True)
            private_write(runtime_path, json.dumps(effective).encode())
            if profile['mode'] == 'agent_bridge':
                command = [effective['python'], str(ROOT / 'engine/python/agent_completion_endpoint.py'),
                    '--port', str(urlparse(effective['completion_url']).port),
                    '--token-file', effective['completion_token_file'], '--agent-executable', str(selected_bridge['executable']),
                    '--auth-home', str(selected_bridge['home']), '--bridge-kind', selected_bridge['kind'], '--model', effective['model']]
                if profile.get('bridge_provider'):
                    command.extend(['--bridge-provider', profile['bridge_provider']])
                with startup_stage('agent_bridge_readiness'):
                    endpoint = launch(command)
                    health(effective['completion_url'] + '/health', endpoint)
            with startup_stage('memory_engine_port'):
                port_free(effective['service_url'])
            with startup_stage('memory_engine_readiness'):
                service = launch([effective['python'], str(ROOT / 'engine/python/official_service_dev.py'), '--config', str(runtime_path)])
                health(effective['service_url'] + '/health', service)
            with startup_stage('generation_applied_receipt'):
                if applied_path.is_symlink() or applied_path.stat().st_mode & 0o077:
                    raise RuntimeError('private generation applied receipt required')
                applied = json.loads(applied_path.read_text())
                if applied.get('pid') != service.pid or applied.get('digest') != digest or applied.get('reference') != reference:
                    raise RuntimeError('generation applied configuration unconfirmed')
            return effective, runtime_path, endpoint, service
        return start_generation(store, start, cleanup)
    finally:
        store.close()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('action', nargs='?', choices=('start', 'setup', 'prepare', 'stop', 'open', 'status', 'recover'), default='start')
    parser.add_argument('--config', type=Path, default=DEFAULT_CONFIG)
    parser.add_argument('--component-root', type=Path, help='prepare: private engine component directory, never a content store')
    parser.add_argument('--uv', help='prepare: uv executable (defaults to discovery)')
    parser.add_argument('--no-ui', action='store_true')
    parser.add_argument('--watch-ui', action='store_true', help='start/open: foreground or live-reload MyGo UI watcher instead of launching the built native binary')
    args = parser.parse_args()
    if args.watch_ui and args.action not in ('open', 'start'):
        parser.error('--watch-ui is only valid with start or open')
    if args.action == 'start' and args.no_ui and args.watch_ui:
        parser.error('--watch-ui cannot be combined with --no-ui')
    if args.action == 'prepare':
        if not args.component_root or args.no_ui or args.watch_ui:
            parser.error('prepare requires --component-root; it prepares components without starting services or UI')
        from local_prepare import prepare
        print(json.dumps(prepare(args.component_root, args.uv), indent=2))
        return
    if args.component_root or args.uv:
        parser.error('--component-root and --uv are only valid with prepare')
    path = args.config.expanduser().resolve(strict=True)
    # Stopping/inspecting the verified live owner must still work after source
    # edits. They launch no new plugin; path/lock/PID/start-time checks below
    # remain mandatory. Starts continue to verify every current fixed input.
    config = load_config(path, verify_current_sources=args.action not in ('stop', 'status', 'recover'))
    data = Path(config['official_data'])
    runfile = data / '.memlane-dev-owner.json'
    if args.action == 'recover':
        from dev_recovery import recover
        result = recover(config, path, acquire=acquire, process_tree=process_tree, port_free=port_free)
        cleanup_stale_pid_receipts(path.parent)
        print(json.dumps(result))
        return
    if args.action in ('stop', 'status', 'open'):
        state = read_owner_record(runfile)
        # Verify the live owner via a locked store and kernel PID, not a stale file.
        try:
            lock = acquire(data)
        except BlockingIOError:
            lock = None
        if lock is not None:
            lock.close()
            raise RuntimeError('no active launcher owns this store')
        pid = state['pid']
        os.kill(pid, 0)
        actual = subprocess.check_output(['ps', '-p', str(pid), '-o', 'lstart='], text=True).strip()
        actual_c = subprocess.check_output(['ps', '-p', str(pid), '-o', 'lstart='], text=True,
                                           env={**os.environ, 'LC_ALL': 'C'}).strip()
        if state['started'] not in (actual, actual_c) or state['config'] != str(path):
            raise RuntimeError('owner identity mismatch; do not signal an unknown process')
        if args.action == 'status':
            print(json.dumps(state, indent=2)); return
        if args.action == 'open':
            if state.get('runtime_config'):
                config = load_config(Path(state['runtime_config']))
            from first_install_mode import first_install_setup
            if not config.get('generation_setup_only') and not first_install_setup(config):
                complete_initialization(config, path)
            if args.watch_ui:
                # An older detached launcher may have lost its output reader.
                # Never attach a second watcher while its owned watcher is live.
                for child in owner_ui_children(state):
                    status = subprocess.run(['ps', '-p', str(child), '-o', 'stat='], capture_output=True, text=True)
                    if status.returncode == 0 and status.stdout.strip() and not status.stdout.strip().startswith('Z'):
                        raise RuntimeError('existing UI watcher is live; use ordinary open')
                with path.with_name('.ui-watch.lock').open('a+') as ui_lock:
                    fcntl.flock(ui_lock.fileno(), fcntl.LOCK_EX | fcntl.LOCK_NB)
                    def stop_ui(_signal, _frame):
                        raise KeyboardInterrupt
                    signal.signal(signal.SIGTERM, stop_ui)
                    watcher = launch_owned([str(GO), 'tool', 'mygo', 'dev'], cwd=ROOT,
                                           env=ui_env(Path(state.get('runtime_config', path))), log_file=path.parent / 'runtime-logs/ui-attached.log')
                    try:
                        last_trim = None
                        while watcher.poll() is None:
                            now = time.monotonic()
                            if last_trim is None or (now - last_trim) >= HOUSEKEEPING_INTERVAL:
                                trim_service_log(path.parent / 'runtime-logs/ui-attached.log')
                                last_trim = now
                            time.sleep(MONITOR_POLL_INTERVAL)
                    except KeyboardInterrupt:
                        pass
                    finally:
                        stop_group(watcher)
                return
            with path.with_name('.ui-watch.lock').open('a+') as ui_lock:
                try:
                    fcntl.flock(ui_lock.fileno(), fcntl.LOCK_EX | fcntl.LOCK_NB)
                except BlockingIOError:
                    print('Foreground UI watcher already attached; use the current window.')
                    return
        os.kill(pid, signal.SIGTERM if args.action == 'stop' else signal.SIGUSR1)
        if args.action == 'stop':
            deadline = time.monotonic() + 35
            while time.monotonic() < deadline:
                try:
                    lock = acquire(data)
                    lock.close()
                    if runfile.exists():
                        raise RuntimeError('launcher exited but cleanup is unconfirmed; ownership record retained')
                    print('Owned service groups exited; daily store released.'); return
                except BlockingIOError: time.sleep(.2)
            raise RuntimeError('stop not confirmed; ownership remains reserved')
        return
    from first_install_mode import first_install_setup
    first_setup = first_install_setup(config)
    if first_setup and args.action == 'setup':
        raise ValueError('first installation uses start; generation-only setup cannot replace it')
    lock = acquire(data)
    if runfile.exists():
        lock.close()
        raise RuntimeError('previous ownership record needs confirmed recovery before restart; use dev.py recover with the same config')
    cleanup_stale_pid_receipts(path.parent)
    processes = []
    child_start_times = {}
    descendants = {}
    ui = None
    reopen = False
    state = {'pid': os.getpid(), 'started': subprocess.check_output(['ps', '-p', str(os.getpid()), '-o', 'lstart='], text=True).strip(),
             'phase': 'starting', 'config': str(path), 'official_data': str(data),
             'service_url': config['service_url'], 'completion_url': config.get('completion_url'),
             'children': [], 'service_children': {}, 'child_start_times': {}}
    resources_dir, resources_hash = runtime_resource_identity()
    if resources_dir and resources_hash:
        state.update(resources_dir=resources_dir, resources_hash=resources_hash)

    def save_owner():
        live_pids = [proc.pid for proc in processes if proc.poll() is None]
        state['children'] = live_pids
        state['child_start_times'] = {str(pid): child_start_times[pid] for pid in live_pids if pid in child_start_times}
        state['observed_descendants'] = dict(descendants)
        write_owner_record(runfile, state)
    def stop(_signal, _frame): raise KeyboardInterrupt
    def open_window(_signal, _frame):
        nonlocal reopen
        reopen = True
    signal.signal(signal.SIGTERM, stop)
    signal.signal(signal.SIGUSR1, open_window)
    def launch(command, env=None, cwd=ROOT):
        entry = Path(command[1]).stem if len(command)>1 and command[1].endswith('.py') else 'ui'
        log_file = path.parent / 'runtime-logs' / (entry + '.log')
        trim_service_log(log_file)
        proc = launch_owned(command, cwd=cwd, env=env, log_file=log_file)
        processes.append(proc)
        try:
            started = subprocess.check_output(['ps', '-p', str(proc.pid), '-o', 'lstart='], text=True).strip()
            if started:
                child_start_times[proc.pid] = started
        except Exception:
            pass
        role = {'codex_completion_endpoint': 'completion', 'agent_completion_endpoint': 'completion', 'official_service_dev': 'official',
                'project_control_server': 'project_control'}.get(entry)
        if role:
            state['service_children'][role] = proc.pid
        save_owner()
        return proc
    def cleanup_stack():
        try:
            track_descendants([p.pid for p in processes], descendants)
            stop_errors = stop_groups_parallel(processes)
            if stop_errors:
                return False
            stop_descendants(descendants)
        except Exception:
            return False
        processes.clear()
        child_start_times.clear()
        descendants.clear()
        state['service_children'].clear()
        save_owner()
        return True
    try:
        save_owner()
        if args.action == 'setup':
            config, runtime_path = generation_setup_runtime(config, path)
            endpoint = service = None
        elif first_setup:
            with startup_stage('first_install_setup_engine'):
                config, runtime_path, endpoint, service = first_install_runtime(config, path, launch)
        else:
            config, runtime_path, endpoint, service = start_generation_stack(config, path, launch, cleanup_stack)
            if config.get('first_install'):
                from first_install_runtime import confirm_service_owner
                confirm_service_owner(config)
        state.update(runtime_config=str(runtime_path), generation_mode=config['generation_mode'],
                     generation_reference=config.get('generation_reference'), completion_url=config.get('completion_url'))
        save_owner()
        control = None
        if config.get('project_control'):
            with startup_stage('project_control_port'):
                port_free(config['project_control']['url'])
            with startup_stage('project_control_readiness'):
                control = launch([config['python'], str(ROOT / 'engine/python/project_control_server.py'), '--config', str(runtime_path)])
                state['service_children']['project_control'] = control.pid
                state['project_control_url'] = config['project_control']['url']
                wait_project_control(config['project_control'], control, setup_only=config.get('generation_setup_only', False))
        if not config.get('generation_setup_only') and not first_setup:
            with startup_stage('initialization_receipt'):
                complete_initialization(config, path)
                if config.get('first_install'):
                    from first_install_runtime import finish_activation
                    finish_activation(config, path)
        state['phase'] = ('first_install_setup' if first_setup else
                          'generation_setup' if config.get('generation_setup_only') else 'ready')
        save_owner()
        report_notice('First installation ready for Agent and generation setup; content processing remains unavailable.' if first_setup else
                      'Generation setup ready; save, validate and select next, then stop setup and start normally.' if config.get('generation_setup_only') else
                      'Daily official services ready; closing UI keeps them running. Ctrl+C explicitly stops owned services.')
        reopen = not args.no_ui
        last_housekeeping = None
        while True:
            if (not app_runtime_resources() and config.get('first_install') and
                    shutil.disk_usage(data).free < 55 * 1024**3):
                raise RuntimeError('first installation store disk reserve below 55 GiB; stopping owned services')
            last_housekeeping = run_monitor_housekeeping(
                path.parent / 'runtime-logs', processes, descendants, save_owner,
                last_run=last_housekeeping, interval=HOUSEKEEPING_INTERVAL,
            )
            if (endpoint is not None and endpoint.poll() is not None) or (service is not None and service.poll() is not None):
                raise RuntimeError('official service or completion endpoint exited; no automatic restart')
            if control is not None and control.poll() is not None:
                raise RuntimeError('project control exited; no automatic restart or owner fallback')
            if reopen:
                reopen = False
                ui = reopen_ui(ui, processes, launch, runtime_path, watch_ui=args.watch_ui)
            time.sleep(MONITOR_POLL_INTERVAL)
    except KeyboardInterrupt: pass
    finally:
        errors = []
        track_descendants([p.pid for p in processes], descendants)
        errors.extend(stop_groups_parallel(processes))
        try: stop_descendants(descendants)
        except Exception as error: errors.append(str(error))
        if errors:
            # Preserve state; do not claim a successful shutdown.
            raise RuntimeError('; '.join(errors))
        runfile.unlink(missing_ok=True)
        for artifact in (path.parent / ('.runtime-active-' + str(os.getpid()) + '.json'),
                         path.parent / ('.generation-applied-' + str(os.getpid()) + '.json')):
            artifact.unlink(missing_ok=True)
        lock.close()


def run_entrypoint():
    if not app_runtime_resources() and os.environ.get('MEMLANE_DISK_TASK') != '1':
        return subprocess.call([sys.executable, str(ROOT / 'scripts/task.py'), '--', sys.executable, __file__, *sys.argv[1:]])
    return main()


if __name__ == '__main__':
    raise SystemExit(run_entrypoint())
