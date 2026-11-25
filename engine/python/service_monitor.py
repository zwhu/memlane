"""In-memory 3-layer runtime monitoring for Cognee, control plane, and model bridges."""
import asyncio
from datetime import datetime, timezone
import json
import math
import os
from pathlib import Path
import re
import shutil
import stat
import subprocess
import threading
import time

from aiohttp import ClientSession, ClientTimeout, web

from project_policy import PolicyError

MINIMUM_FREE_BYTES = 50 * 1024 ** 3
STOP_FREE_BYTES = 55 * 1024 ** 3
USER_MINIMUM_FREE_BYTES = 2 * 1024 ** 3
USER_STOP_FREE_BYTES = 5 * 1024 ** 3
DEFAULT_STORAGE_SAMPLE_INTERVAL_SECONDS = 60.0
_MAX_OWNER_BYTES = 65536
_MAX_RECEIPT_BYTES = 16384
_MAX_CONTROL_LOG_BYTES = 32768
_MAX_TRACE_SCAN_BYTES = 131072

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
        return {
            'pid': pid if isinstance(pid, int) else 0,
            'alive': False,
            'rss_bytes': None,
            'cpu_percent': None,
            'child_count': 0,
            'self_rss_bytes': None,
        }
    try:
        os.kill(pid, 0)
        alive = True
    except ProcessLookupError:
        return {
            'pid': pid,
            'alive': False,
            'rss_bytes': None,
            'cpu_percent': None,
            'child_count': 0,
            'self_rss_bytes': None,
        }
    except PermissionError:
        alive = True
    except OSError:
        return {
            'pid': pid,
            'alive': False,
            'rss_bytes': None,
            'cpu_percent': None,
            'child_count': 0,
            'self_rss_bytes': None,
        }

    rss_bytes = None
    cpu_percent = None
    child_count = 0
    self_rss_bytes = None
    try:
        result = subprocess.run(
            ['ps', '-axo', 'pid=,ppid=,rss=,pcpu='],
            capture_output=True, text=True, timeout=2, check=False,
        )
        if result.returncode == 0:
            proc_map = {}
            children_map = {}
            for line in result.stdout.splitlines():
                parts = line.strip().split()
                if len(parts) >= 4:
                    try:
                        p = int(parts[0])
                        pp = int(parts[1])
                        rss_kib = int(parts[2])
                        cpu = float(parts[3])
                        proc_map[p] = (pp, rss_kib, cpu)
                        children_map.setdefault(pp, []).append(p)
                    except ValueError:
                        continue
            if pid in proc_map:
                descendants = set()
                queue = [pid]
                while queue:
                    curr = queue.pop()
                    for child in children_map.get(curr, ()):
                        if child not in descendants and child != pid:
                            descendants.add(child)
                            queue.append(child)
                child_count = len(descendants)
                self_kib = proc_map[pid][1]
                self_rss_bytes = self_kib * 1024 if self_kib >= 0 else 0
                total_kib = self_kib if self_kib >= 0 else 0
                total_cpu = proc_map[pid][2]
                for c in descendants:
                    c_info = proc_map.get(c)
                    if c_info:
                        if c_info[1] >= 0:
                            total_kib += c_info[1]
                        total_cpu += c_info[2]
                rss_bytes = total_kib * 1024
                cpu_percent = round(total_cpu, 1)
    except (OSError, ValueError, subprocess.SubprocessError):
        pass
    return {
        'pid': pid,
        'alive': alive,
        'rss_bytes': rss_bytes,
        'cpu_percent': cpu_percent,
        'child_count': child_count,
        'self_rss_bytes': self_rss_bytes,
    }


def backlog_files_count(state_dir, *, max_count: int = 100000) -> int:
    if state_dir is None:
        return 0
    backlog_dir = Path(state_dir) / 'backlog'
    try:
        if not backlog_dir.exists() or not backlog_dir.is_dir() or backlog_dir.is_symlink():
            return 0
        count = 0
        with os.scandir(backlog_dir) as it:
            for _ in it:
                count += 1
                if count >= max_count:
                    break
        return count
    except OSError:
        return 0


def _regular_file_size(path: Path) -> int:
    try:
        info = path.lstat()
        if stat.S_ISREG(info.st_mode):
            return int(info.st_size)
    except OSError:
        pass
    return 0


def _sqlite_group_bytes(db_path: Path) -> int:
    total = 0
    for suffix in ('', '-wal', '-shm'):
        candidate = Path(str(db_path) + suffix)
        total += _regular_file_size(candidate)
    return total


def _directory_tree_stats(root: Path, *, max_entries: int = 500000) -> tuple[int, int]:
    """Iterative non-symlink-following directory size & file count sampler."""
    try:
        info = root.lstat()
        if not stat.S_ISDIR(info.st_mode):
            return 0, 0
    except OSError:
        return 0, 0

    total_bytes = 0
    file_count = 0
    visited = 0
    stack = [str(root)]
    while stack:
        current = stack.pop()
        try:
            with os.scandir(current) as it:
                for entry in it:
                    visited += 1
                    if visited > max_entries:
                        return total_bytes, file_count
                    try:
                        if entry.is_symlink():
                            continue
                        if entry.is_dir(follow_symlinks=False):
                            stack.append(entry.path)
                        elif entry.is_file(follow_symlinks=False):
                            total_bytes += int(entry.stat(follow_symlinks=False).st_size)
                            file_count += 1
                    except OSError:
                        continue
        except OSError:
            continue
    return total_bytes, file_count


def legacy_overview_cache_stats(state_dir) -> tuple[int, int]:
    """Count & measure retired legacy ``overview-ui-cache-*.json`` files under ``state_dir``."""
    if state_dir is None:
        return 0, 0
    root = Path(state_dir)
    try:
        info = root.lstat()
        if not stat.S_ISDIR(info.st_mode):
            return 0, 0
    except OSError:
        return 0, 0
    count = 0
    total_bytes = 0
    try:
        with os.scandir(root) as it:
            for entry in it:
                name = entry.name
                if not (name.startswith('overview-ui-cache-') and name.endswith('.json')):
                    continue
                try:
                    if entry.is_symlink() or not entry.is_file(follow_symlinks=False):
                        continue
                    total_bytes += int(entry.stat(follow_symlinks=False).st_size)
                    count += 1
                except OSError:
                    continue
    except OSError:
        return 0, 0
    return count, total_bytes


def sample_storage_breakdown(official_data, state_dir) -> dict:
    """Sample per-directory storage metrics off the event loop."""
    cognee_db_bytes = 0
    cache_db_bytes = 0
    lancedb_bytes = 0
    ladybug_bytes = 0
    largest_lbug = 0
    data_dir_bytes = 0
    data_dir_file_count = 0

    if official_data:
        root = Path(official_data)
        db_dir = root / 'system' / 'databases'
        cognee_db_bytes = _sqlite_group_bytes(db_dir / 'cognee_db')
        cache_db_bytes = _sqlite_group_bytes(db_dir / 'cache.db')
        lancedb_dir = db_dir / 'cognee.lancedb'
        lancedb_bytes, _ = _directory_tree_stats(lancedb_dir)

        kuzu_dir = db_dir / 'cognee_graph_kuzu'
        kuzu_bytes, _ = _directory_tree_stats(kuzu_dir)
        ladybug_bytes = kuzu_bytes

        try:
            if db_dir.exists() and db_dir.is_dir() and not db_dir.is_symlink():
                kuzu_prefix = str(kuzu_dir) + os.sep
                kuzu_exact = str(kuzu_dir)
                lance_prefix = str(lancedb_dir) + os.sep
                lance_exact = str(lancedb_dir)
                stack = [str(db_dir)]
                while stack:
                    cur = stack.pop()
                    if cur == lance_exact or cur.startswith(lance_prefix):
                        continue
                    try:
                        with os.scandir(cur) as it:
                            for entry in it:
                                try:
                                    if entry.is_symlink():
                                        continue
                                    if entry.is_dir(follow_symlinks=False):
                                        stack.append(entry.path)
                                    elif entry.is_file(follow_symlinks=False):
                                        if entry.name.endswith('.lbug'):
                                            sz = int(entry.stat(follow_symlinks=False).st_size)
                                            if sz > largest_lbug:
                                                largest_lbug = sz
                                            if not (entry.path == kuzu_exact or entry.path.startswith(kuzu_prefix)):
                                                ladybug_bytes += sz
                                        elif entry.name.endswith('.lbug.wal'):
                                            if not (entry.path == kuzu_exact or entry.path.startswith(kuzu_prefix)):
                                                ladybug_bytes += int(entry.stat(follow_symlinks=False).st_size)
                                except OSError:
                                    continue
                    except OSError:
                        continue
        except OSError:
            pass

        data_dir_bytes, data_dir_file_count = _directory_tree_stats(root / 'data')

    legacy_count, legacy_bytes = legacy_overview_cache_stats(state_dir)
    return {
        'backlog_files_count': backlog_files_count(state_dir),
        'largest_graph_db_bytes': largest_lbug,
        'storage_breakdown': {
            'cognee_db_bytes': cognee_db_bytes,
            'cache_db_bytes': cache_db_bytes,
            'lancedb_bytes': lancedb_bytes,
            'ladybug_bytes': ladybug_bytes,
            'data_dir_bytes': data_dir_bytes,
            'data_dir_file_count': data_dir_file_count,
            'legacy_overview_cache_count': legacy_count,
            'legacy_overview_cache_bytes': legacy_bytes,
        },
    }


def _parse_trace_timestamp(raw_at) -> float | None:
    if not isinstance(raw_at, str) or not raw_at:
        return None
    try:
        return datetime.fromisoformat(raw_at).astimezone(timezone.utc).timestamp()
    except (ValueError, TypeError):
        return None


def _read_trace_tail(path: Path) -> list[dict]:
    try:
        target = Path(path)
        if not target.exists() or target.is_symlink() or not target.is_file():
            return []
        size = target.stat().st_size
        if size <= 0:
            return []
        span = min(size, _MAX_TRACE_SCAN_BYTES)
        with target.open('rb') as stream:
            if size > span:
                stream.seek(size - span)
            raw = stream.read(span)
    except OSError:
        return []
    entries = []
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
            if type(elapsed) in (int, float) and math.isfinite(elapsed):
                entries.append(entry)
    return entries


def recent_processing_duration_ms(control_log: Path, *extra_logs: Path) -> float | None:
    best_generation = None  # (has_ts, ts, elapsed_ms)
    best_fallback = None    # (has_ts, ts, elapsed_ms)
    for log_path in (control_log, *extra_logs):
        if log_path is None:
            continue
        for entry in _read_trace_tail(log_path):
            component = entry.get('component')
            elapsed = float(entry['elapsed_ms'])
            ts = _parse_trace_timestamp(entry.get('at'))
            candidate = (ts is not None, ts or 0.0, elapsed)
            if component == 'generation_http':
                if best_generation is None or candidate[:2] > best_generation[:2]:
                    best_generation = candidate
            elif component != 'cognee_gateway':
                if best_fallback is None or candidate[:2] > best_fallback[:2]:
                    best_fallback = candidate
    if best_generation is not None:
        return round(best_generation[2], 1)
    if best_fallback is not None:
        return round(best_fallback[2], 1)
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
                 sample_process=None, disk_usage=shutil.disk_usage, clock=time.time,
                 recall_store=None,
                 storage_sample_interval: float = DEFAULT_STORAGE_SAMPLE_INTERVAL_SECONDS):
        self.config = dict(config or {})
        self.registry = registry
        self.connections = connections
        self.recall_store = recall_store
        self.probe_http = probe_http or _default_probe_http
        self.sample_process = sample_process or default_sample_process
        self.disk_usage = disk_usage
        self.clock = clock
        self.storage_sample_interval = max(0.0, float(storage_sample_interval))
        self.consecutive_failures = 0
        self.last_ok_at = 0.0
        self.last_checked_at = 0.0
        self.last_error = ''
        self.last_latency_ms = None
        self.history = []
        self._storage_lock = threading.Lock()
        self._storage_cache = None
        self._storage_sampled_at = None
        self._storage_refresh_task = None

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
        setup = self.config.get('first_install')
        if (owner.get('phase') == 'first_install_setup'
                or (isinstance(setup, dict) and setup.get('setup') is True
                    and owner.get('generation_reference') is None)):
            return None
        receipt = state_dir / f'.generation-applied-{owner_pid}.json'
        try:
            if receipt.is_symlink() or not receipt.is_file() or receipt.stat().st_size > _MAX_RECEIPT_BYTES:
                return False
            data = json.loads(receipt.read_text(encoding='utf-8'))
            if not isinstance(data, dict):
                return False
            reference = owner.get('generation_reference')
            return reference is not None and data.get('reference') == reference
        except (OSError, ValueError, TypeError):
            return False

    def _disk_thresholds(self) -> tuple[int, int]:
        raw_min = self.config.get('disk_minimum_free_bytes')
        raw_stop = self.config.get('disk_stop_free_bytes')
        if type(raw_min) is int and raw_min > 0:
            stop = raw_stop if (type(raw_stop) is int and raw_stop >= raw_min) else raw_min
            return raw_min, stop
        runtime_mode = str(self.config.get('runtime_mode') or '').strip().lower()
        is_installed_mode = (
            isinstance(self.config.get('first_install'), dict)
            or runtime_mode in ('app', 'installed', 'user')
            or bool(os.environ.get('MEMLANE_APP_RESOURCES'))
        )
        if is_installed_mode:
            return USER_MINIMUM_FREE_BYTES, USER_STOP_FREE_BYTES
        return MINIMUM_FREE_BYTES, STOP_FREE_BYTES

    def _sample_disk(self, state_dir):
        min_free, stop_free = self._disk_thresholds()
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
                        'minimum_free_bytes': min_free,
                        'stop_free_bytes': stop_free,
                        'low': free < stop_free,
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

    def _recall_query_summary(self):
        if self.recall_store is None:
            return None
        fn = getattr(self.recall_store, 'query_summary', None)
        if not callable(fn):
            return None
        try:
            return fn()
        except Exception:
            return None

    def _backlog_files_count(self, state_dir) -> int:
        return backlog_files_count(state_dir)

    def _sample_storage_sync(self, official_data, state_dir, sampled_at: float) -> dict:
        sampled = sample_storage_breakdown(official_data, state_dir)
        # Preserve subclass/monkeypatch override compatibility if overridden.
        if type(self)._backlog_files_count is not ServiceMonitor._backlog_files_count:
            sampled['backlog_files_count'] = self._backlog_files_count(state_dir)
        breakdown = dict(sampled['storage_breakdown'])
        breakdown['sampled_at'] = sampled_at
        result = {
            'backlog_files_count': int(sampled['backlog_files_count']),
            'largest_graph_db_bytes': int(sampled['largest_graph_db_bytes']),
            'storage_breakdown': breakdown,
        }
        with self._storage_lock:
            self._storage_cache = result
            self._storage_sampled_at = sampled_at
        return result

    async def refresh_storage_stats(self, *, now: float | None = None) -> dict:
        ts = float(self.clock()) if now is None else float(now)
        return await asyncio.to_thread(
            self._sample_storage_sync,
            self.config.get('official_data'),
            self._state_dir(),
            ts,
        )

    def _start_storage_task(self, official_data, state_dir, now: float) -> asyncio.Task:
        task = self._storage_refresh_task
        if task is not None and not task.done():
            return task

        async def _run_sample():
            return await asyncio.to_thread(self._sample_storage_sync, official_data, state_dir, now)

        task = asyncio.create_task(_run_sample())
        self._storage_refresh_task = task

        def _clear_task(done_task):
            if self._storage_refresh_task is done_task:
                self._storage_refresh_task = None
            # Consume unhandled exceptions when running detached in background.
            if not done_task.cancelled():
                done_task.exception()

        task.add_done_callback(_clear_task)
        return task

    async def _get_storage_metrics(self, official_data, state_dir, now: float) -> dict:
        with self._storage_lock:
            cached = self._storage_cache
            sampled_at = self._storage_sampled_at

        if cached is None:
            # Coalesce concurrent cold-start callers onto one in-flight disk sampling task.
            task = self._start_storage_task(official_data, state_dir, now)
            fresh = await asyncio.shield(task)
            return {
                'backlog_files_count': fresh['backlog_files_count'],
                'largest_graph_db_bytes': fresh['largest_graph_db_bytes'],
                'storage_breakdown': dict(fresh['storage_breakdown']),
            }

        stale = (
            sampled_at is None
            or (now - sampled_at) >= self.storage_sample_interval
            or now < sampled_at
        )
        if stale:
            self._start_storage_task(official_data, state_dir, now)

        return {
            'backlog_files_count': cached['backlog_files_count'],
            'largest_graph_db_bytes': cached['largest_graph_db_bytes'],
            'storage_breakdown': dict(cached['storage_breakdown']),
        }

    async def close(self):
        task = self._storage_refresh_task
        if task is not None and not task.done():
            task.cancel()
            await asyncio.gather(task, return_exceptions=True)
        self._storage_refresh_task = None

    async def _sample_process_async(self, pid: int) -> dict:
        try:
            if asyncio.iscoroutinefunction(self.sample_process):
                return await self.sample_process(pid)
            return await asyncio.to_thread(self.sample_process, pid)
        except Exception:
            return {
                'pid': pid,
                'alive': False,
                'rss_bytes': None,
                'cpu_percent': None,
                'child_count': 0,
                'self_rss_bytes': None,
            }

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
        pids_to_sample = {}
        for role in ('official', 'project_control', 'completion'):
            pid = service_children.get(role)
            if type(pid) is int:
                pids_to_sample[role] = pid
        if 'project_control' not in pids_to_sample:
            pids_to_sample['project_control'] = os.getpid()

        sampled_results = await asyncio.gather(
            *(self._sample_process_async(p) for p in pids_to_sample.values())
        )
        processes = dict(zip(pids_to_sample.keys(), sampled_results))

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
            log_tail = await asyncio.to_thread(read_log_tail, state_dir / 'runtime-logs/official_service_dev.log')

        recent_ms = (
            await asyncio.to_thread(
                recent_processing_duration_ms,
                state_dir / 'runtime-logs/official_service_dev.log',
                state_dir / 'runtime-logs/project_control_server.log',
            )
            if state_dir is not None else None
        )
        storage = await self._get_storage_metrics(self.config.get('official_data'), state_dir, now)
        hook_errors = self._hook_errors()
        summary = {
            'projects_count': len(self.registry.state.get('projects', {})) if self.registry is not None else 0,
            'active_generation': owner.get('generation_reference'),
            'generation_mode': str(owner.get('generation_mode') or ''),
            'generation_receipt_verified': self._generation_receipt_verified(owner, state_dir),
            'recent_processing_ms': recent_ms,
            'hook_errors_count': len(hook_errors),
            'hook_errors': hook_errors,
            'backlog_files_count': storage['backlog_files_count'],
            'largest_graph_db_bytes': storage['largest_graph_db_bytes'],
            'storage_breakdown': storage['storage_breakdown'],
        }
        recall_queries = self._recall_query_summary()
        if recall_queries is not None:
            summary['recall_queries'] = recall_queries

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


def attach_service_monitor(app, config, *, registry=None, connections=None, monitor=None, recall_store=None):
    active_monitor = monitor or ServiceMonitor(
        config, registry=registry, connections=connections, recall_store=recall_store
    )

    async def runtime_monitor(request):
        if not request['manager']:
            raise PolicyError('management_required', 403)
        include_logs = request.query.get('include_logs', '').lower() in ('1', 'true', 'yes')
        return web.json_response(await active_monitor.snapshot(include_logs=include_logs))

    async def cleanup(_app):
        await active_monitor.close()

    app.add_routes([web.get('/v1/runtime/monitor', runtime_monitor)])
    app.on_cleanup.append(cleanup)
    return active_monitor
