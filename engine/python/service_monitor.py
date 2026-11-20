"""In-memory 3-layer runtime monitoring for Cognee, control plane, and model bridges."""
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import time

from aiohttp import ClientSession, ClientTimeout, web

from project_policy import PolicyError

MINIMUM_FREE_BYTES = 50 * 1024 ** 3
STOP_FREE_BYTES = 55 * 1024 ** 3
_MAX_OWNER_BYTES = 65536
_MAX_RECEIPT_BYTES = 16384
_MAX_CONTROL_LOG_BYTES = 32768

_ANSI_ESCAPE_RE = re.compile(r'\x1b(?:\[[0-9;?]*[ -/]*[@-~]|\][^\x07\x1b]*(?:\x07|\x1b\\)|[@-Z\\-_])')
_CONTROL_CHAR_RE = re.compile(r'[\x00-\x08\x0b-\x1f\x7f]')

_REDACTION_PATTERNS = (
    (re.compile(r'(?i)(Bearer\s+)\S+'), r'\1[REDACTED]'),
    (re.compile(r'(?i)(X-Api-Key\s*:\s*)\S+'), r'\1[REDACTED]'),
    (re.compile(r'(?i)(X-Memlane-Management\s*:\s*)\S+'), r'\1[REDACTED]'),
    (re.compile(r'(?i)(\b(?:api[_-]?key|access[_-]?token|auth[_-]?token|token|secret|password)\b\s*[=:]\s*)(\S+)'), r'\1[REDACTED]'),
)


def redact_log_line(line: str) -> str:
    result = _ANSI_ESCAPE_RE.sub('', str(line))
    result = _CONTROL_CHAR_RE.sub('', result)
    for pattern, replacement in _REDACTION_PATTERNS:
        result = pattern.sub(replacement, result)
    return result


def read_log_tail(path: Path, *, max_lines: int = 20, max_bytes: int = 4096) -> list[str]:
    try:
        target = Path(path)
        if not target.exists() or target.is_symlink() or not target.is_file():
            return []
        size = target.stat().st_size
        if size <= 0:
            return []
        span = min(size, max_bytes * 2)
        with target.open('rb') as stream:
            if size > span:
                stream.seek(size - span)
            raw = stream.read(span)
        if size > span and b'\n' in raw:
            raw = raw.split(b'\n', 1)[1]
    except OSError:
        return []
    lines = [redact_log_line(item) for item in raw.decode('utf-8', errors='replace').splitlines() if item.strip()]
    selected = lines[-max_lines:]
    bounded = []
    used = 0
    for item in reversed(selected):
        encoded = item.encode('utf-8')
        if len(encoded) > max_bytes:
            item = encoded[:max_bytes].decode('utf-8', errors='ignore')
            encoded = item.encode('utf-8')
        cost = len(encoded) + (1 if bounded else 0)
        if used + cost > max_bytes and bounded:
            break
        bounded.append(item)
        used += cost
    bounded.reverse()
    return bounded


def default_sample_process(pid: int) -> dict:
    if type(pid) is not int or pid <= 1:
        return {'pid': pid if isinstance(pid, int) else 0, 'alive': False, 'rss_bytes': None, 'cpu_percent': None}
    try:
        os.kill(pid, 0)
        alive = True
    except ProcessLookupError:
        return {'pid': pid, 'alive': False, 'rss_bytes': None, 'cpu_percent': None}
    except PermissionError:
        alive = True
    except OSError:
        return {'pid': pid, 'alive': False, 'rss_bytes': None, 'cpu_percent': None}

    rss_bytes = None
    cpu_percent = None
    try:
        result = subprocess.run(
            ['ps', '-o', 'rss=,pcpu=', '-p', str(pid)],
            capture_output=True, text=True, timeout=2, check=False,
        )
        if result.returncode == 0:
            parts = result.stdout.strip().split()
            if len(parts) >= 1:
                rss_kib = int(parts[0])
                if rss_kib >= 0:
                    rss_bytes = rss_kib * 1024
            if len(parts) >= 2:
                cpu_percent = round(float(parts[1]), 1)
    except (OSError, ValueError, subprocess.SubprocessError):
        pass
    return {'pid': pid, 'alive': alive, 'rss_bytes': rss_bytes, 'cpu_percent': cpu_percent}


def recent_processing_duration_ms(control_log: Path) -> float | None:
    try:
        target = Path(control_log)
        if not target.exists() or target.is_symlink() or not target.is_file():
            return None
        size = target.stat().st_size
        if size <= 0:
            return None
        span = min(size, _MAX_CONTROL_LOG_BYTES)
        with target.open('rb') as stream:
            if size > span:
                stream.seek(size - span)
            raw = stream.read(span)
    except OSError:
        return None
    for line in reversed(raw.decode('utf-8', errors='replace').splitlines()):
        line = line.strip()
        if not line.startswith('{'):
            continue
        try:
            entry = json.loads(line)
        except (ValueError, TypeError):
            continue
        if isinstance(entry, dict) and entry.get('event') == 'request_finished':
            elapsed = entry.get('elapsed_ms')
            if type(elapsed) in (int, float):
                return round(float(elapsed), 1)
    return None


async def _default_probe_http(url: str):
    if not url:
        return False, None, 'service_url_unconfigured'
    target = url.rstrip('/') + '/health'
    async with ClientSession(timeout=ClientTimeout(total=5)) as session:
        async with session.get(target, allow_redirects=False) as response:
            if response.status == 200:
                return True, 200, ''
            return False, response.status, f'official_http_{response.status}'


class ServiceMonitor:
    def __init__(self, config, registry=None, *, connections=None, probe_http=None,
                 sample_process=None, disk_usage=shutil.disk_usage, clock=time.time):
        self.config = dict(config or {})
        self.registry = registry
        self.connections = connections
        self.probe_http = probe_http or _default_probe_http
        self.sample_process = sample_process or default_sample_process
        self.disk_usage = disk_usage
        self.clock = clock
        self.consecutive_failures = 0
        self.last_ok_at = 0.0
        self.last_checked_at = 0.0
        self.last_error = ''
        self.last_latency_ms = None
        self.history = []

    def _state_dir(self):
        if self.config.get('generation_file'):
            return Path(self.config['generation_file']).parent
        if self.registry is not None and getattr(self.registry, 'path', None):
            return Path(self.registry.path).parent
        return None

    def _owner_receipt(self):
        official_data = self.config.get('official_data')
        if not official_data:
            return {}
        receipt = Path(official_data) / '.memlane-dev-owner.json'
        try:
            if not receipt.exists() or receipt.is_symlink() or not receipt.is_file() or receipt.stat().st_size > _MAX_OWNER_BYTES:
                return {}
            data = json.loads(receipt.read_text(encoding='utf-8'))
            return data if isinstance(data, dict) else {}
        except (OSError, ValueError, TypeError):
            return {}

    def _generation_receipt_verified(self, owner, state_dir):
        owner_pid = owner.get('pid')
        if type(owner_pid) is not int or owner_pid <= 1 or state_dir is None:
            return None
        receipt = state_dir / f'.generation-applied-{owner_pid}.json'
        try:
            if not receipt.exists() or receipt.is_symlink() or not receipt.is_file() or receipt.stat().st_size > _MAX_RECEIPT_BYTES:
                return False
            data = json.loads(receipt.read_text(encoding='utf-8'))
            if not isinstance(data, dict):
                return False
            return data.get('reference') == owner.get('generation_reference')
        except (OSError, ValueError, TypeError):
            return False

    def _sample_disk(self, state_dir):
        candidates = [self.config.get('official_data'), state_dir, Path.cwd()]
        for item in candidates:
            if not item:
                continue
            target = Path(item)
            if target.exists():
                try:
                    free = int(self.disk_usage(target).free)
                    return {
                        'free_bytes': free,
                        'minimum_free_bytes': MINIMUM_FREE_BYTES,
                        'stop_free_bytes': STOP_FREE_BYTES,
                        'low': free < STOP_FREE_BYTES,
                    }
                except OSError:
                    continue
        return None

    def _hook_errors(self) -> list[dict]:
        adapters = getattr(self.connections, 'adapters', None)
        if not isinstance(adapters, dict):
            return []
        errors = []
        for adapter_id, adapter in adapters.items():
            fn = getattr(adapter, 'last_hook_error', None)
            if not callable(fn):
                continue
            try:
                err = fn()
            except Exception:
                continue
            if not isinstance(err, dict):
                continue
            at_val = err.get('at')
            errors.append({
                'instance_id': str(getattr(adapter, 'id', adapter_id) or adapter_id)[:80],
                'kind': str(getattr(adapter, 'kind', '') or '')[:40],
                'at': float(at_val) if type(at_val) in (int, float) else 0.0,
                'script': str(err.get('script', ''))[:80],
                'error_type': str(err.get('error_type', ''))[:80],
                'reason': str(err.get('reason', ''))[:120],
            })
        return errors

    def _hook_errors_count(self):
        return len(self._hook_errors())

    async def snapshot(self, *, include_logs: bool = False) -> dict:
        now = float(self.clock())
        self.last_checked_at = now
        service_url = str(self.config.get('service_url') or '')
        t0 = time.monotonic()
        ok = False
        reason = ''
        try:
            ok, _, reason = await self.probe_http(service_url)
        except Exception as exc:
            ok = False
            reason = type(exc).__name__
        latency_ms = round((time.monotonic() - t0) * 1000.0, 1)

        if ok:
            self.consecutive_failures = 0
            self.last_ok_at = now
            self.last_error = ''
            self.last_latency_ms = latency_ms
        else:
            self.consecutive_failures += 1
            self.last_error = str(reason or 'health_probe_failed')[:160]
            self.last_latency_ms = None

        owner = self._owner_receipt()
        service_children = owner.get('service_children') if isinstance(owner.get('service_children'), dict) else {}
        processes = {}
        for role in ('official', 'project_control', 'completion'):
            pid = service_children.get(role)
            if type(pid) is int:
                processes[role] = self.sample_process(pid)
        if 'project_control' not in processes:
            processes['project_control'] = self.sample_process(os.getpid())

        official_proc = processes.get('official')
        if isinstance(official_proc, dict) and official_proc.get('alive') is False:
            state = 'exited'
        elif ok:
            state = 'ready'
        elif self.consecutive_failures >= 3:
            state = 'degraded'
        elif self.last_ok_at > 0:
            state = 'warning'
        elif owner.get('phase') and owner.get('phase') != 'ready':
            state = 'starting'
        else:
            state = 'degraded'

        self.history.append({
            'at': now,
            'ok': bool(ok),
            'latency_ms': self.last_latency_ms,
            'status': state,
        })
        if len(self.history) > 20:
            self.history = self.history[-20:]

        state_dir = self._state_dir()
        disk = self._sample_disk(state_dir)
        log_tail = []
        if state_dir is not None and (include_logs or state in ('exited', 'degraded')):
            log_tail = read_log_tail(state_dir / 'runtime-logs/official_service_dev.log')

        recent_ms = (
            recent_processing_duration_ms(state_dir / 'runtime-logs/project_control_server.log')
            if state_dir is not None else None
        )
        hook_errors = self._hook_errors()
        summary = {
            'projects_count': len(self.registry.state.get('projects', {})) if self.registry is not None else 0,
            'active_generation': owner.get('generation_reference'),
            'generation_mode': str(owner.get('generation_mode') or ''),
            'generation_receipt_verified': self._generation_receipt_verified(owner, state_dir),
            'recent_processing_ms': recent_ms,
            'hook_errors_count': len(hook_errors),
            'hook_errors': hook_errors,
        }

        return {
            'state': state,
            'checked_at': now,
            'last_ok_at': self.last_ok_at,
            'latency_ms': self.last_latency_ms,
            'consecutive_failures': self.consecutive_failures,
            'last_error': self.last_error,
            'service_url': service_url,
            'owner_pid': owner.get('pid'),
            'owner_phase': str(owner.get('phase') or ''),
            'processes': processes,
            'disk': disk,
            'summary': summary,
            'log_tail': log_tail,
        }


def attach_service_monitor(app, config, *, registry=None, connections=None, monitor=None):
    active_monitor = monitor or ServiceMonitor(config, registry=registry, connections=connections)

    async def runtime_monitor(request):
        if not request['manager']:
            raise PolicyError('management_permission_required', 403)
        include_logs = request.query.get('include_logs', '').lower() in ('1', 'true', 'yes')
        return web.json_response(await active_monitor.snapshot(include_logs=include_logs))

    app.add_routes([web.get('/v1/runtime/monitor', runtime_monitor)])
    return active_monitor
