"""Manager-owned backlog jobs. Journals contain counts and hashes, never bodies."""
import asyncio
from collections import Counter
import copy
from datetime import datetime, timezone
import hashlib
import json
import os
from pathlib import Path
import re
import stat
import sys
import threading
import time
from urllib.parse import quote
import uuid

from aiohttp import web
from project_control import private_write
from project_policy import PolicyError
from performance_trace import RequestTrace

ACTIVE = {'queued', 'reviewing', 'running', 'waiting', 'stopping', 'processing'}
MAX_BACKLOG_JOBS = 100
WORKER = Path(__file__).with_name('project_backlog_worker.py')
HASH_CHUNK_SIZE = 1024 * 1024
INSTALLATION_VERIFY_TTL_SECONDS = 300.0
INSTALLATION_VERIFY_MAX_ENTRIES = 256
REVIEW_CACHE_TTL_SECONDS = 25.0
REVIEW_CACHE_MAX_ENTRIES = 512
COLD_REVIEW_MAX_ENTRIES = 2048

_INSTALLATION_VERIFY_LOCK = threading.Lock()
_INSTALLATION_VERIFY_CACHE = {}


def clear_installation_verify_cache():
    with _INSTALLATION_VERIFY_LOCK:
        _INSTALLATION_VERIFY_CACHE.clear()


def _sha256_file(path, *, chunk_size=HASH_CHUNK_SIZE):
    digest = hashlib.sha256()
    with open(path, 'rb') as handle:
        while True:
            chunk = handle.read(chunk_size)
            if not chunk:
                break
            digest.update(chunk)
    return digest.hexdigest()


def _stat_signature(st):
    return (st.st_dev, st.st_ino, st.st_size, st.st_mtime_ns)


def _utc_timestamp(value):
    if not value:
        return 0.0
    try:
        parsed = datetime.fromisoformat(str(value).replace('Z', '+00:00'))
        return parsed.replace(tzinfo=parsed.tzinfo or timezone.utc).timestamp()
    except (ValueError, TypeError):
        return 0.0


def _cold_watch_valid(watch):
    if not isinstance(watch, dict):
        return False
    map_path = watch.get('map_path')
    bridge_path = watch.get('bridge_path')
    bridge_exists = watch.get('bridge_exists')
    if (not isinstance(map_path, str) or not os.path.isabs(map_path)
            or not isinstance(bridge_path, str) or not os.path.isabs(bridge_path)
            or type(bridge_exists) is not bool):
        return False
    for key in ('bridge_dev', 'bridge_ino', 'bridge_size', 'bridge_mtime_ns'):
        if type(watch.get(key)) is not int:
            return False
    return True


def _cold_watch_unchanged(watch, manifest_path, manifest_sig):
    try:
        mst = os.lstat(manifest_path)
    except OSError:
        return False
    if stat.S_ISLNK(mst.st_mode) or not stat.S_ISREG(mst.st_mode) or _stat_signature(mst) != manifest_sig:
        return False
    try:
        os.lstat(watch['map_path'])
        return False
    except FileNotFoundError:
        pass
    except OSError:
        return False
    try:
        bst = os.lstat(watch['bridge_path'])
    except FileNotFoundError:
        return not watch['bridge_exists']
    except OSError:
        return False
    if not watch['bridge_exists'] or stat.S_ISLNK(bst.st_mode) or not stat.S_ISREG(bst.st_mode):
        return False
    expected_bridge_sig = (watch['bridge_dev'], watch['bridge_ino'], watch['bridge_size'], watch['bridge_mtime_ns'])
    return _stat_signature(bst) == expected_bridge_sig


def verify_installation(installation, *, force=False, ttl=INSTALLATION_VERIFY_TTL_SECONDS, clock=time.monotonic):
    root = Path(installation['plugin_dir'])
    if not all(Path(installation[k]).is_absolute() for k in ('plugin_dir', 'env_file', 'home')):
        raise PolicyError('backlog_installation_unconfirmed', 409)
    manifest = root / '.memlane-project-access.json'
    try:
        manifest_st = os.lstat(manifest)
    except OSError:
        raise PolicyError('backlog_installation_changed', 409) from None
    if stat.S_ISLNK(manifest_st.st_mode) or not stat.S_ISREG(manifest_st.st_mode):
        raise PolicyError('backlog_installation_changed', 409)
    expected_manifest = installation['manifest_sha256']
    manifest_sig = _stat_signature(manifest_st)
    cache_key = (str(root), expected_manifest)
    now = clock()
    if not force and ttl > 0:
        with _INSTALLATION_VERIFY_LOCK:
            cached = _INSTALLATION_VERIFY_CACHE.get(cache_key)
        if cached is not None and cached[0] > now and cached[1] == manifest_sig:
            all_match = True
            for name, expected_sig in cached[2]:
                try:
                    file_st = os.lstat(root / name)
                except OSError:
                    all_match = False
                    break
                if (stat.S_ISLNK(file_st.st_mode) or not stat.S_ISREG(file_st.st_mode)
                        or _stat_signature(file_st) != expected_sig):
                    all_match = False
                    break
            if all_match:
                return
    try:
        raw = manifest.read_bytes()
    except OSError:
        raise PolicyError('backlog_installation_changed', 409) from None
    if hashlib.sha256(raw).hexdigest() != expected_manifest:
        raise PolicyError('backlog_installation_changed', 409)
    files = json.loads(raw)['files']
    if 'scripts/_memlane_policy.py' not in files or 'scripts/_plugin_common.py' not in files:
        raise PolicyError('backlog_host_unsupported', 409)
    file_stats = []
    for name, expected in files.items():
        relative = Path(name)
        path = root / relative
        if relative.is_absolute() or '..' in relative.parts:
            raise PolicyError('backlog_installation_changed', 409)
        try:
            file_st = os.lstat(path)
        except OSError:
            raise PolicyError('backlog_installation_changed', 409) from None
        if (stat.S_ISLNK(file_st.st_mode) or not stat.S_ISREG(file_st.st_mode)
                or _sha256_file(path) != expected):
            raise PolicyError('backlog_installation_changed', 409)
        file_stats.append((name, _stat_signature(file_st)))
    if ttl > 0:
        with _INSTALLATION_VERIFY_LOCK:
            expired = [key for key, value in _INSTALLATION_VERIFY_CACHE.items() if value[0] <= now]
            for key in expired:
                _INSTALLATION_VERIFY_CACHE.pop(key, None)
            while len(_INSTALLATION_VERIFY_CACHE) >= INSTALLATION_VERIFY_MAX_ENTRIES:
                _INSTALLATION_VERIFY_CACHE.pop(next(iter(_INSTALLATION_VERIFY_CACHE)))
            _INSTALLATION_VERIFY_CACHE[cache_key] = (now + ttl, manifest_sig, tuple(file_stats))


class Backlog:
    def __init__(self, registry, runtime, *, worker=None, review_cache_ttl=REVIEW_CACHE_TTL_SECONDS, clock=time.monotonic):
        self.registry, self.runtime, self.worker = registry, runtime, worker
        self.review_cache_ttl = review_cache_ttl
        self._clock = clock
        self._review_cache = {}
        self._review_inflight = {}
        self._review_gen = {}
        self._cold_reviews = {}
        self.root = registry.path.parent / 'backlog'
        self.path = self.root / 'jobs.json'
        self.root.mkdir(parents=True, exist_ok=True, mode=0o700)
        if self.path.is_symlink():
            raise PolicyError('backlog_journal_unconfirmed', 503)
        self.jobs = json.loads(self.path.read_bytes()) if self.path.exists() else {}
        self.task = None
        self.processing_task = None
        self.processing_operation = None
        self.processing_target = None
        self.monitor = None
        self.auto_cursor = 0
        self.check_cursor = 0
        self.checks = {}
        self.check_tasks = {}
        self.check_monitor = None
        self.buffer_reviews = {}
        self._reconcile_cursor = 0
        self._last_acl_verify_at = None
        for row in self.jobs.values():
            for target in list(row.get('targets', [])) + list(row.get('pending_processing', {}).values()):
                if target.get('processing') == 'unknown' and target.get('processing_reason') == 'incomplete_drain':
                    target.update(previous_processing='unknown', processing='partial',
                                  recovery_reason='official_partial_submission', next_processing_at=time.time()+300)
                elif target.get('processing') == 'unknown' and target.get('processing_reason') == 'policy_unconfirmed':
                    target.update(previous_processing='unknown', processing='deferred',
                                  recovery_reason='pre_submit_policy_unconfirmed', next_processing_at=time.time()+30)
                elif target.get('processing') == 'submitting':
                    target.update(previous_processing='submitting', processing='unknown',
                                  processing_reason='owner_interrupted')
                elif (target.get('processing') == 'disabled'
                      and target.get('reason') == 'backlog_write_denied'
                      and (target.get('recovery_reason') == 'pre_submit_policy_unconfirmed'
                           or target.get('processing_reason') == 'policy_unconfirmed')):
                    target.update(previous_processing='disabled', processing='deferred',
                                  recovery_reason='pre_submit_policy_unconfirmed', next_processing_at=time.time()+30)
                    target.pop('reason', None)
            if (row.get('automatic') and not row.get('automatic_user_paused')
                    and (row['status'] in ACTIVE or row.get('owner_shutdown'))):
                # A new owner reviews the current official buffer afresh. It
                # never resumes the old token list; ambiguous sends stay out.
                row.update(status='watching', reason='', owner_shutdown=False)
                self.cancel_file(row).unlink(missing_ok=True)
                continue
            if row['status'] in ACTIVE:
                private_write(self.cancel_file(row), b'stop')
                row.update(status='interrupted', reason='owner_interrupted')
        self.persist()

    def persist(self):
        private_write(self.path, json.dumps(self.jobs, sort_keys=True).encode())

    def cancel_file(self, row):
        return self.root / (row['operation_id'] + '.stop')

    def public(self, row):
        result = {k: v for k, v in row.items() if k not in ('targets', 'pending_processing', 'save_retries')}
        targets = row['targets']
        result.update(saved=row.get('saved_before', 0) + sum(t.get('saved', 0) for t in targets),
                      save_rejected=sum(t.get('rejected', 0) for t in targets),
                      save_deferred=sum(t.get('deferred', 0) + (t.get('save_status') == 'paused') for t in targets),
                      handled=row.get('handled_before', 0) + sum(t.get('handled', 0) for t in targets),
                      isolated=sum(t.get('unknown', 0) + t.get('isolated', 0) for t in targets),
                      remaining=sum(t.get('remaining', len(t.get('tokens', []))) for t in targets),
                      total=sum(len(t.get('tokens', [])) for t in targets),
                      sessions=len(targets),
                      processing_submitted=sum(t.get('processing') == 'submitted' for t in (row.get('pending_processing', {}).values() if row.get('automatic') else targets)),
                      processing_failed=sum(t.get('processing') in ('unknown', 'failed') for t in (row.get('pending_processing', {}).values() if row.get('automatic') else targets)),
                      processing_active=sum(t.get('processing') == 'submitting' for t in (row.get('pending_processing', {}).values() if row.get('automatic') else targets)),
                      processing_partial=sum(t.get('processing') == 'partial' for t in (row.get('pending_processing', {}).values() if row.get('automatic') else targets)),
                      processing_disabled=sum(t.get('processing') == 'disabled' for t in (row.get('pending_processing', {}).values() if row.get('automatic') else targets)),
                      processing_pending=sum(t.get('processing') in ('queued', 'deferred', 'partial') for t in row.get('pending_processing', {}).values()))
        result['review_failed'] = sum(t.get('review_status') == 'unconfirmed' for t in targets)
        return result

    def _latest_project_job(self, project):
        rows = [r for r in self.jobs.values() if r.get('project_id') == project]
        return max(rows, key=lambda r: r['created_at']) if rows else None

    @staticmethod
    def _processing_targets(row):
        if not row:
            return []
        return list(row.get('pending_processing', {}).values()) if row.get('automatic') else list(row.get('targets', []))

    def processing_issues(self, row):
        issues = []
        for target in self._processing_targets(row):
            status = target.get('processing')
            if status not in ('unknown', 'failed', 'submitting'):
                continue
            issues.append({
                'agent_id': str(target.get('agent_id') or ''),
                'host': str(target.get('host') or ''),
                'session_id': str(target.get('session_id') or ''),
                'processing': str(status),
                'reason': str(target.get('processing_reason') or target.get('reason') or ''),
                'attempted_at': float(target.get('processing_attempted_at') or 0.0),
                'reconcile_status': str(target.get('reconcile_status') or ''),
                'reconciled_at': float(target.get('reconciled_at') or 0.0),
            })
        issues.sort(key=lambda item: (item['processing'] != 'submitting', -item['attempted_at'], item['agent_id'], item['host']))
        return issues

    def get(self, project):
        latest = self._latest_project_job(project)
        enabled = bool(self.monitor and not self.monitor.done()) and (not latest or latest['status'] not in ('paused','stopped','interrupted','stopping'))
        return {'job': self.public(latest) if latest else None, 'automatic_enabled': enabled,
                'buffer_reviews': [dict(value) for value in self.buffer_reviews.values() if value['project_id'] == project],
                'checks': [dict(value) for value in self.checks.values() if value['project_id'] == project],
                'processing_issues': self.processing_issues(latest)}

    def record_buffer_review(self, project, target, snapshot):
        # Metadata projection of the installed plugin's current source buffer.
        # It is re-read after restart and never persisted as a content copy.
        if isinstance(snapshot.get('signature'), str) and type(snapshot.get('filtered')) is int and snapshot['filtered'] >= 0:
            self.buffer_reviews[(project, target['agent_id'], target['host'])] = {
                'project_id': project, 'agent_id': target['agent_id'], 'host': target['host'],
                'signature': snapshot['signature'], 'filtered': snapshot.get('filtered_unsent', snapshot['filtered']),
                **{key: snapshot.get(key, 0) for key in ('rejected', 'rejected_unsent', 'rejected_unknown', 'deferred', 'deferred_unsent')}}

    def request_check(self, project, agent_id, host, *, signature='', force=False):
        target = {'agent_id': agent_id, 'host': host, 'dataset': self.registry.state['projects'][project]['dataset_id']}
        self.checked_target(project, target, write=False)
        key = (project, agent_id, host)
        previous = self.checks.get(key, {})
        if key in self.check_tasks or (not force and previous.get('signature') == signature and previous.get('next_at', 0) > time.time()):
            return
        if len(self.check_tasks) >= 2:
            raise PolicyError('pending_check_busy', 409)
        record = {**previous, 'project_id': project, 'agent_id': agent_id, 'host': host,
                  'status': 'checking', 'signature': signature, 'reason': ''}
        self.checks[key] = record
        async def run():
            try:
                result = await self.execute({'project_id': project, 'operation_id': str(uuid.uuid4())}, target, 'check')
                if result.get('status') not in ('confirmed', 'unconfirmed'):
                    raise PolicyError('official_review_unavailable', 503)
                record.update(status=result['status'], reason=result.get('reason', ''),
                              matched=result.get('matched', 0), unknown=result.get('unknown', 0),
                              signature=result.get('unknown_signature', signature))
            except asyncio.CancelledError:
                raise
            except Exception:
                record.update(status='unconfirmed', reason='official_review_unavailable')
            finally:
                record.update(checked_at=time.time(), next_at=time.time() + 300)
                self.check_tasks.pop(key, None)
        self.check_tasks[key] = asyncio.create_task(run())

    async def automatic_check_tick(self):
        # Independent of saving/process submission: an unavailable receipt must
        # not hold the sender or require an open desktop window.
        now = self._clock()
        if hasattr(self.registry, 'verify_unconfirmed_groups') and (
                self._last_acl_verify_at is None or now - self._last_acl_verify_at >= 30.0):
            self._last_acl_verify_at = now
            try:
                await self.registry.verify_unconfirmed_groups()
            except (PolicyError, OSError, ValueError):
                pass
        bindings = list(self.registry.state['sessions'].items())
        if bindings and len(self.check_tasks) < 2:
            raw, project = bindings[self.check_cursor % len(bindings)]
            self.check_cursor += 1
            try:
                agent_id, host = json.loads(raw)
                project_row = self.registry.state['projects'].get(project)
                if project_row:
                    target = {'agent_id': agent_id, 'host': host, 'dataset': project_row['dataset_id']}
                    agent, view = self.checked_target(project, target, write=False)
                    if agent.get('installation') and 'read' in view['policy']['permissions']:
                        snapshot = await self.execute({'project_id': project, 'operation_id': str(uuid.uuid4())}, target, 'review')
                        if snapshot.get('unknown', 0) > 0:
                            self.request_check(project, agent_id, host, signature=snapshot.get('unknown_signature', ''))
                        else:
                            self.checks.pop((project, agent_id, host), None)
            except (PolicyError, OSError, ValueError, KeyError, TypeError, asyncio.TimeoutError):
                pass
        try:
            await self.reconcile_next_processing_target()
        except (PolicyError, OSError, ValueError, KeyError, TypeError, asyncio.TimeoutError):
            pass

    async def automatic_check_loop(self):
        while True:
            try:
                await self.automatic_check_tick()
            except (PolicyError, OSError, ValueError, KeyError):
                pass
            await asyncio.sleep(20)

    def checked_target(self, project_id, target, *, write=True):
        project = self.registry.state['projects'].get(project_id)
        agent = self.registry.state['agents'].get(target['agent_id'])
        binding = json.dumps([target['agent_id'], target['host']], ensure_ascii=True)
        if (not project or not agent or target['agent_id'] in self.registry.state.get('agent_retirements', {}) or self.registry.state['sessions'].get(binding) != project_id
                or project.get('dataset_id') != target['dataset']):
            raise PolicyError('backlog_binding_changed', 409)
        view = self.registry.policy(project, target['agent_id'])
        if not view['ready'] or (write and 'write' not in view['policy']['permissions']):
            raise PolicyError('backlog_write_denied', 403)
        return agent, view

    def _prune_finished_jobs(self, target_project_id, *, max_jobs=MAX_BACKLOG_JOBS):
        """FIFO-evict oldest non-active historical jobs so new operations never wedged on 100 entries."""
        if len(self.jobs) < max_jobs:
            return
        running_proc_op = (
            self.processing_operation
            if (self.processing_task and not self.processing_task.done())
            else None
        )
        hard_protected = set()
        latest_by_project = {}
        for op_id, row in self.jobs.items():
            status = row.get('status')
            if status in ACTIVE or status == 'watching' or op_id == running_proc_op:
                hard_protected.add(op_id)
            elif any(
                t.get('processing') in ('queued', 'deferred', 'partial', 'submitting', 'unknown')
                for t in self._all_job_targets(row)
            ):
                hard_protected.add(op_id)
            pid = row.get('project_id')
            created = float(row.get('created_at', 0.0))
            if pid and (pid not in latest_by_project or created >= latest_by_project[pid][0]):
                latest_by_project[pid] = (created, op_id, row)

        pause_guard = set()
        for pid, (_, op_id, row) in latest_by_project.items():
            if pid != target_project_id and row.get('status') in ('paused', 'stopped', 'interrupted'):
                pause_guard.add(op_id)

        evictable = [
            (float(row.get('created_at', 0.0)), op_id)
            for op_id, row in self.jobs.items()
            if op_id not in hard_protected and op_id not in pause_guard
        ]
        evictable.sort()
        for _, op_id in evictable:
            if len(self.jobs) < max_jobs:
                break
            removed = self.jobs.pop(op_id, None)
            if removed is not None:
                self.cancel_file(removed).unlink(missing_ok=True)

    def start(self, project_id, operation_id, *, automatic=False, selected=None):
        try:
            if str(uuid.UUID(operation_id)) != operation_id:
                raise ValueError()
        except (ValueError, TypeError, AttributeError):
            raise PolicyError('backlog_operation_id_required') from None
        old = self.jobs.get(operation_id)
        if old is not None and not (automatic and old.get('automatic') and old['status'] == 'watching'):
            if old['project_id'] != project_id:
                raise PolicyError('backlog_operation_conflict', 409)
            return self.public(old)
        # Only one sender across this manager; the installed shared locks also
        # exclude independent normal hooks and other manager instances.
        if self.task and not self.task.done():
            active = next((r for r in self.jobs.values() if r['status'] in ACTIVE), None)
            if active and active['project_id'] == project_id:
                return self.public(active)
            raise PolicyError('backlog_sender_busy', 409)
        if project_id not in self.registry.state['projects']:
            raise PolicyError('project_not_found', 404)
        if old is None and len(self.jobs) >= MAX_BACKLOG_JOBS:
            self._prune_finished_jobs(project_id, max_jobs=MAX_BACKLOG_JOBS)
        if old is None and len(self.jobs) >= MAX_BACKLOG_JOBS:
            raise PolicyError('backlog_journal_limit', 409)
        project = self.registry.state['projects'][project_id]
        targets = []
        for key, bound in self.registry.state['sessions'].items():
            if bound != project_id:
                continue
            agent_id, host = json.loads(key)
            agent = self.registry.state['agents'].get(agent_id, {})
            if not agent.get('installation') or agent_id in self.registry.state.get('agent_retirements', {}):
                continue
            if selected is not None and (agent_id, host) not in selected:
                continue
            target = {'agent_id': agent_id, 'host': host, 'dataset': project['dataset_id']}
            try:
                self.checked_target(project_id, target)
            except PolicyError as error:
                target.update(admission_blocked=True, review_status='unconfirmed', review_reason=error.code,
                              save_status='paused', save_reason=error.code, tokens=[])
            targets.append(target)
        row = {'operation_id': operation_id, 'project_id': project_id, 'status': 'queued',
               'reason': '', 'created_at': time.time(), 'targets': targets}
        if automatic:
            row['automatic'] = True
            row['save_retries'] = copy.deepcopy((old or {}).get('save_retries', {}))
            row['pending_processing'] = copy.deepcopy((old or {}).get('pending_processing', {}))
            if self.processing_task and not self.processing_task.done() and self.processing_operation == operation_id:
                for key, target in (old or {}).get('pending_processing', {}).items():
                    if target is self.processing_target:
                        row['pending_processing'][key] = target
            if old:
                previous = self.public(old)
                row.update(saved_before=previous['saved'], handled_before=previous['handled'],
                           created_at=old['created_at'], capture_retry_count=old.get('capture_retry_count', 0))
        self.jobs[operation_id] = row
        self.persist()
        self.task = asyncio.create_task(self.run(row))
        return self.public(row)

    def stop(self, project_id, operation_id, *, owner_shutdown=False):
        row = self.jobs.get(operation_id)
        if row is None or row['project_id'] != project_id:
            raise PolicyError('backlog_job_not_found', 404)
        if row.get('automatic'):
            row['owner_shutdown' if owner_shutdown else 'automatic_user_paused'] = True
        if self.processing_task and not self.processing_task.done() and self.processing_operation == operation_id:
            private_write(self.cancel_file(row), b'stop')
        if row['status'] in ACTIVE:
            private_write(self.cancel_file(row), b'stop')
            row['status'] = 'stopping'
            self.persist()
        elif row.get('automatic') and row['status'] in ('watching', 'paused'):
            row.update(status='stopped', reason='user_stopped')
            self.persist()
        elif row.get('automatic'):
            self.persist()
        return self.public(row)

    def invalidate_review_cache(self, project_id, agent_id=None, host=None):
        if agent_id is not None and host is not None:
            key = (project_id, agent_id, host)
            self._review_gen[key] = self._review_gen.get(key, 0) + 1
            self._review_cache.pop(key, None)
            self._review_inflight.pop(key, None)
            self._cold_reviews.pop(key, None)
            return
        for key in [k for k in set(self._review_cache) | set(self._review_inflight) | set(self._cold_reviews) if k[0] == project_id]:
            self._review_gen[key] = self._review_gen.get(key, 0) + 1
            self._review_cache.pop(key, None)
            self._review_inflight.pop(key, None)
            self._cold_reviews.pop(key, None)

    async def execute(self, row, target, action, update=None, *, force=False):
        cache_key = (row['project_id'], target['agent_id'], target['host'])
        if action == 'review':
            # Ordinary idle polling must not flood the diagnostic log.
            agent, _ = self.checked_target(row['project_id'], target, write=False)
            installation = agent.get('installation') or {}
            inst_sig = (
                installation.get('plugin_dir'),
                installation.get('manifest_sha256'),
                installation.get('env_file'),
                installation.get('home'),
            )
            use_cache = not force and update is None and self.review_cache_ttl > 0
            if use_cache:
                cached = self._review_cache.get(cache_key)
                if cached is not None and self._clock() < cached[0] and cached[1] == target.get('dataset'):
                    return copy.deepcopy(cached[2])
            if not force and update is None:
                cold = self._cold_reviews.get(cache_key)
                if cold is not None:
                    cold_dataset, cold_inst_sig, cold_manifest_path, cold_manifest_sig, cold_watch, cold_snapshot = cold
                    if (cold_dataset == target.get('dataset') and cold_inst_sig == inst_sig
                            and _cold_watch_unchanged(cold_watch, cold_manifest_path, cold_manifest_sig)):
                        return copy.deepcopy(cold_snapshot)
                    self._cold_reviews.pop(cache_key, None)
            if use_cache:
                inflight = self._review_inflight.get(cache_key)
                if inflight is not None and not inflight.done():
                    ok, payload = await asyncio.shield(inflight)
                    if ok:
                        return copy.deepcopy(payload)
                    raise payload
            gen = self._review_gen.get(cache_key, 0)
            fut = None
            if use_cache and cache_key not in self._review_inflight:
                fut = asyncio.get_running_loop().create_future()
                self._review_inflight[cache_key] = fut
            try:
                result = await self._execute(row, target, action, update)
                cold_watch = result.pop('cold_watch', None) if isinstance(result, dict) else None
                if isinstance(result, dict) and self._review_gen.get(cache_key, 0) == gen:
                    if result.get('session_inactive') is True and _cold_watch_valid(cold_watch) and installation.get('plugin_dir'):
                        manifest_path = str(Path(installation['plugin_dir']) / '.memlane-project-access.json')
                        try:
                            mst = os.lstat(manifest_path)
                        except OSError:
                            self._cold_reviews.pop(cache_key, None)
                        else:
                            if not stat.S_ISLNK(mst.st_mode) and stat.S_ISREG(mst.st_mode):
                                while len(self._cold_reviews) >= COLD_REVIEW_MAX_ENTRIES:
                                    self._cold_reviews.pop(next(iter(self._cold_reviews)))
                                self._cold_reviews[cache_key] = (
                                    target.get('dataset'),
                                    inst_sig,
                                    manifest_path,
                                    _stat_signature(mst),
                                    cold_watch,
                                    copy.deepcopy(result),
                                )
                            else:
                                self._cold_reviews.pop(cache_key, None)
                    else:
                        self._cold_reviews.pop(cache_key, None)
                if isinstance(result, dict) and self.review_cache_ttl > 0 and self._review_gen.get(cache_key, 0) == gen:
                    now = self._clock()
                    expired = [k for k, v in self._review_cache.items() if v[0] <= now]
                    for k in expired:
                        self._review_cache.pop(k, None)
                    while len(self._review_cache) >= REVIEW_CACHE_MAX_ENTRIES:
                        self._review_cache.pop(next(iter(self._review_cache)))
                    self._review_cache[cache_key] = (
                        now + self.review_cache_ttl, target.get('dataset'), copy.deepcopy(result))
                if fut is not None and not fut.done():
                    fut.set_result((True, result))
                return result
            except BaseException as exc:
                if fut is not None and not fut.done():
                    fut.set_result((False, exc))
                raise
            finally:
                if fut is not None and self._review_inflight.get(cache_key) is fut:
                    self._review_inflight.pop(cache_key, None)
        if action == 'check':
            # Ordinary idle polling must not flood the diagnostic log.
            self.invalidate_review_cache(*cache_key)
            try:
                return await self._execute(row, target, action, update)
            finally:
                self.invalidate_review_cache(*cache_key)
        self.invalidate_review_cache(*cache_key)
        agent, _ = self.checked_target(row['project_id'], target, write=action not in ('review', 'check'))
        kind = agent.get('kind')
        host_kind = kind if kind in {'codex', 'pi', 'traex', 'claude_code', 'cursor', 'opencode', 'antigravity'} else 'unknown'
        trace = RequestTrace('agent_capture')
        trace.transition({'save': 'save_worker', 'check': 'receipt_check'}.get(action, 'official_process_submission'))
        outcome = 'failed'
        try:
            result = await self._execute(row, target, action, update)
            outcome = 'returned'
            return result
        except asyncio.CancelledError:
            outcome = 'cancelled'
            raise
        finally:
            self.invalidate_review_cache(*cache_key)
            # A returned worker may report unknown/deferred, not knowledge
            # success. The durable official receipt remains authoritative.
            trace.finish(outcome, host_kind=host_kind, action=action if action in {'save', 'process', 'check'} else 'other')

    async def _execute(self, row, target, action, update=None):
        agent, _ = self.checked_target(row['project_id'], target, write=action not in ('review', 'check'))
        installation = copy.deepcopy(agent['installation'])
        if installation.get('host_kind') == 'opencode':
            from opencode_backlog import execute
            return await execute(self.runtime, installation, row['project_id'], target, action, self.cancel_file(row), update)
        await asyncio.to_thread(verify_installation, installation)
        if self.worker:
            return await self.worker(row, target, action, update)
        env = {k: v for k, v in os.environ.items()
               if not k.startswith(('COGNEE_', 'MEMLANE_', 'LLM_', 'EMBEDDING_'))}
        env.update(HOME=installation['home'], COGNEE_ENV_FILE=installation['env_file'],
                   COGNEE_SESSION_KEY=target['host'], COGNEE_PLUGIN_IN_VENV='1')
        payload = {**target, 'action': action, 'automatic': bool(row.get('automatic')), 'scripts': str(Path(installation['plugin_dir']) / 'scripts'),
                   'cancel_file': str(self.cancel_file(row))}
        process = await asyncio.create_subprocess_exec(
            sys.executable, str(WORKER), cwd=installation['home'], env=env,
            stdin=asyncio.subprocess.PIPE, stdout=asyncio.subprocess.PIPE, stderr=asyncio.subprocess.DEVNULL,
            limit=1024 * 1024)
        process.stdin.write(json.dumps(payload).encode())
        await process.stdin.drain()
        process.stdin.close()
        last = None
        try:
            while True:
                # No total task deadline: progress can span many old 20s batches.
                line = await asyncio.wait_for(process.stdout.readline(), 45)
                if not line:
                    break
                value = json.loads(line)
                if update:
                    update(value)
                last = value
            await asyncio.wait_for(process.wait(), 10)
            if process.returncode != 0 or last is None:
                raise PolicyError('backlog_worker_unconfirmed', 503)
            return last
        except BaseException:
            # Never abandon a sender then blindly retry it. Signal cooperative
            # stop; its HTTP result/unknown journal remains the original truth.
            if action not in ('review', 'check'):
                private_write(self.cancel_file(row), b'stop')
            try:
                process.terminate()
            except ProcessLookupError:
                pass
            try:
                await asyncio.wait_for(process.wait(), 35)
            except asyncio.TimeoutError:
                raise PolicyError('backlog_sender_exit_unconfirmed', 503) from None
            raise

    async def process_saved(self, row, target):
        if getattr(self, 'handoff', None) and self.handoff.pending():
            target.update(processing='deferred', processing_reason='generation_handoff_in_progress')
            return
        if self.cancel_file(row).exists() or not (target.get('saved') or target.get('handled')):
            return
        project = self.registry.state['projects'].get(row['project_id'])
        agent = self.registry.state['agents'].get(target['agent_id'])
        binding = json.dumps([target['agent_id'], target['host']], ensure_ascii=True)
        if (project and agent and target['agent_id'] not in self.registry.state.get('agent_retirements', {})
                and self.registry.state['sessions'].get(binding) == row['project_id']
                and project.get('dataset_id') == target['dataset']):
            pre_view = self.registry.policy(project, target['agent_id'])
            if (not pre_view['ready'] and 'write' in pre_view['policy']['permissions']
                    and pre_view['policy'].get('can_auto_process')):
                target.update(processing='deferred', processing_reason='policy_unconfirmed',
                              next_processing_at=time.time() + 30)
                target.pop('reason', None)
                self.persist()
                return
        _, policy = self.checked_target(row['project_id'], target)
        if not policy['policy']['can_auto_process']:
            target['processing'] = 'disabled'
            return
        for previous in self.jobs.values():
            candidates = list(previous.get('targets', [])) + list(previous.get('pending_processing', {}).values())
            if any(t is not target and t.get('agent_id') == target['agent_id']
                   and t.get('host') == target['host'] and t.get('processing') in ('submitting', 'unknown')
                   for t in candidates):
                target.update(processing='unknown', reason='previous_submission_unconfirmed')
                self.persist()
                return
        # Journal before POST. An unknown submit is never automatically repeated.
        target['processing'] = 'submitting'
        target['processing_reason'] = ''
        target['processing_attempted_at'] = time.time()
        for stale in ('reason', 'recovery_reason', 'reconcile_status', 'reconciled_at', 'next_reconcile_at'):
            target.pop(stale, None)
        if row.get('automatic'):
            # Match normal incremental admission; retain the official plugin's
            # own throttle, no-new-entry checks and idempotency watermarks.
            from incremental_processing import COOLDOWN_SECONDS
            target['incremental_cooldown_seconds'] = COOLDOWN_SECONDS
        self.persist()
        try:
            # Use the verified host's official plugin, including its cooldown,
            # failure backoff and no-new-entries check. The manager is not a
            # second improve scheduler or HTTP client.
            result = await self.execute(row, target, 'process')
            status = result.get('status')
            reason = str(result.get('reason') or '')
            if status == 'paused' and reason == 'policy_unconfirmed':
                status = 'deferred'
            target['processing'] = status if status in ('submitted', 'skipped', 'disabled', 'deferred', 'partial', 'failed', 'unknown') else 'unknown'
            target['processing_reason'] = reason
            if target['processing'] in ('submitted', 'skipped'):
                target.pop('reconcile_retry_count', None)
            if target['processing'] in ('deferred', 'partial'):
                from incremental_processing import COOLDOWN_SECONDS
                delay = COOLDOWN_SECONDS if target['processing_reason'] in ('cooldown', 'backoff') else 30
                target['next_processing_at'] = time.time() + delay
            else:
                target.pop('next_processing_at', None)
        except asyncio.CancelledError:
            target['processing'] = 'unknown'
            target['processing_reason'] = 'owner_interrupted'
            self.persist()
            raise
        except Exception:
            target['processing'] = 'unknown'
            target['processing_reason'] = target.get('processing_reason') or 'official_submission_unconfirmed'
        self.persist()

    def _kick_pending_processing(self, row):
        if not row or (self.processing_task is not None and not self.processing_task.done()):
            return False
        pending = sorted(self._processing_targets(row),
                         key=lambda target: target.get('processing_attempted_at', 0))
        now = time.time()
        for target in pending:
            if (target.get('processing') not in ('queued', 'deferred', 'partial')
                    or target.get('next_processing_at', 0) > now):
                continue
            async def submit(owned_row, owned_target):
                try:
                    await self.process_saved(owned_row, owned_target)
                except PolicyError as error:
                    owned_target.update(processing='disabled', reason=error.code)
                    self.persist()
            self.processing_operation = row['operation_id']
            self.processing_target = target
            self.processing_task = asyncio.create_task(submit(row, target))
            return True
        return False

    async def reconcile_processing_target(self, project_id, row, target, *, force=False, now=None):
        now = time.time() if now is None else now
        official = getattr(self.registry, 'official', None)
        if official is None or target.get('processing') not in ('unknown', 'failed') or not target.get('session_id'):
            return None
        if not force and float(target.get('next_reconcile_at') or 0) > now:
            return None
        try:
            agent, view = self.checked_target(project_id, target, write=False)
        except PolicyError:
            target.update(reconcile_status='unavailable', reconciled_at=now, next_reconcile_at=now + 60)
            self.persist()
            return target
        if 'read' not in view['policy']['permissions'] or not agent.get('key_file'):
            target.update(reconcile_status='unavailable', reconciled_at=now, next_reconcile_at=now + 60)
            self.persist()
            return target
        dataset = target['dataset']
        session_id = target['session_id']
        try:
            rows, detail = await asyncio.gather(
                official.request(
                    'GET',
                    f'/api/v1/activity/pipeline-runs?dataset_id={quote(dataset, safe="")}&session_id={quote(session_id, safe="")}&limit=50',
                    token_file=agent['key_file'],
                ),
                official.request(
                    'GET',
                    f'/api/v1/sessions/{quote(session_id, safe="")}',
                    token_file=agent['key_file'],
                ),
            )
        except (PolicyError, OSError, ValueError, asyncio.TimeoutError):
            target.update(reconcile_status='unavailable', reconciled_at=now, next_reconcile_at=now + 120)
            self.persist()
            return target
        if (not isinstance(rows, list) or not isinstance(detail, dict)
                or detail.get('session_id') != session_id or detail.get('dataset_id') != dataset):
            target.update(reconcile_status='unavailable', reconciled_at=now, next_reconcile_at=now + 120)
            self.persist()
            return target

        live_row = self.jobs.get(row.get('operation_id'))
        if live_row is not None and live_row is not row:
            if live_row.get('automatic'):
                key = json.dumps([target['agent_id'], target['host']])
                candidate = live_row.get('pending_processing', {}).get(key)
            else:
                candidate = next(
                    (t for t in live_row.get('targets', [])
                     if t.get('agent_id') == target['agent_id'] and t.get('host') == target['host']),
                    None,
                )
            if candidate is None or candidate.get('processing') not in ('unknown', 'failed'):
                return None
            row, target = live_row, candidate

        session_rows = [
            r for r in rows
            if isinstance(r, dict)
            and (not r.get('dataset_id') or r.get('dataset_id') == dataset)
            and ('session_ids' not in r or session_id in (r.get('session_ids') or ()))
        ]
        terminal_pipelines = {
            r.get('pipeline_run_id') for r in session_rows
            if r.get('kind') == 'pipeline'
            and r.get('status') in ('DATASET_PROCESSING_COMPLETED', 'DATASET_PROCESSING_ERRORED')
            and r.get('pipeline_run_id')
        }
        has_active_run = False
        for r in session_rows:
            started_at = _utc_timestamp(r.get('started_at') or r.get('created_at'))
            if started_at and now - started_at >= 3600:
                continue
            if r.get('kind') == 'operation' and r.get('operation_name') == 'improve' and not r.get('ended_at'):
                has_active_run = True
                break
            if (r.get('kind') == 'pipeline' and r.get('status') == 'DATASET_PROCESSING_STARTED'
                    and r.get('pipeline_run_id') not in terminal_pipelines):
                has_active_run = True
                break

        attempted_at = float(target.get('processing_attempted_at') or 0)
        last_activity_at = _utc_timestamp(detail.get('last_activity_at'))
        succeeded_improves = [
            (_utc_timestamp(r.get('started_at') or r.get('created_at')), _utc_timestamp(r.get('ended_at')))
            for r in session_rows
            if r.get('kind') == 'operation' and r.get('operation_name') == 'improve' and r.get('outcome') == 'succeeded'
        ]
        confirmed_a1 = bool(attempted_at > 0 and any(
            (ended >= attempted_at or started >= attempted_at - 5) for started, ended in succeeded_improves
        ))
        confirmed_a2 = False
        if not confirmed_a1 and not has_active_run and last_activity_at > 0:
            if any(started >= last_activity_at for started, _ in succeeded_improves):
                try:
                    snapshot = await self.execute(
                        {'project_id': project_id, 'operation_id': row.get('operation_id') or str(uuid.uuid4())},
                        target, 'review',
                    )
                    if isinstance(snapshot, dict) and not snapshot.get('tokens') and snapshot.get('unknown', 0) == 0:
                        confirmed_a2 = True
                except (PolicyError, OSError, ValueError):
                    pass

        if confirmed_a1 or confirmed_a2:
            target.update(
                previous_processing=target['processing'],
                processing='submitted',
                recovery_reason='official_activity_confirmed',
                processing_reason='',
                reconcile_status='confirmed',
                reconciled_at=now,
            )
            for key in ('next_reconcile_at', 'next_processing_at', 'reconcile_retry_count'):
                target.pop(key, None)
            self.persist()
            return target

        if has_active_run:
            target.update(reconcile_status='running', reconciled_at=now, next_reconcile_at=now + 60)
            self.persist()
            return target

        post_attempt_failed = bool(attempted_at > 0 and any(
            _utc_timestamp(r.get('started_at') or r.get('created_at')) >= attempted_at - 5
            and (
                (r.get('kind') == 'operation' and r.get('operation_name') == 'improve' and r.get('outcome') == 'failed')
                or (r.get('kind') == 'pipeline' and r.get('status') == 'DATASET_PROCESSING_ERRORED')
            )
            for r in session_rows
        ))
        # Never automatically resubmit either a confirmed failed run or an
        # unconfirmed unknown/owner_interrupted run (absence in the bounded
        # activity window does not prove non-execution).
        target.update(
            reconcile_status='failed' if post_attempt_failed else 'unconfirmed',
            reconciled_at=now,
            next_reconcile_at=now + 300,
        )
        self.persist()
        return target

    def _project_jobs_newest_first(self, project_id):
        rows = [r for r in self.jobs.values() if r.get('project_id') == project_id]
        rows.sort(key=lambda r: float(r.get('created_at') or 0.0), reverse=True)
        return rows

    @staticmethod
    def _all_job_targets(row):
        if not row:
            return []
        seen = set()
        result = []
        for target in list(row.get('pending_processing', {}).values()) + list(row.get('targets', [])):
            ident = id(target)
            if ident not in seen:
                seen.add(ident)
                result.append(target)
        return result

    async def reconcile_project_processing(self, project_id, *, agent_id=None, host=None, force=False):
        if project_id not in self.registry.state['projects']:
            raise PolicyError('project_not_found', 404)
        jobs = self._project_jobs_newest_first(project_id)
        if not jobs:
            return []
        updated = []
        for row in jobs:
            for target in self._all_job_targets(row):
                if target.get('processing') not in ('unknown', 'failed'):
                    continue
                if agent_id is not None and target.get('agent_id') != agent_id:
                    continue
                if host is not None and target.get('host') != host:
                    continue
                result = await self.reconcile_processing_target(project_id, row, target, force=force)
                if result is not None:
                    updated.append(result)
        return updated

    async def reconcile_next_processing_target(self):
        if getattr(self.registry, 'official', None) is None:
            return None
        now = time.time()
        candidates = []
        for project_id in sorted(self.registry.state.get('projects', {})):
            for row in self._project_jobs_newest_first(project_id):
                for target in self._all_job_targets(row):
                    if (target.get('processing') in ('unknown', 'failed')
                            and target.get('session_id')
                            and float(target.get('next_reconcile_at') or 0) <= now):
                        candidates.append((project_id, row, target))
        if not candidates:
            return None
        project_id, row, target = candidates[self._reconcile_cursor % len(candidates)]
        self._reconcile_cursor += 1
        try:
            return await self.reconcile_processing_target(project_id, row, target, force=False, now=now)
        except (PolicyError, OSError, ValueError, KeyError):
            return None

    async def retry_project_processing(self, project_id, *, agent_id=None, host=None, confirm_unconfirmed=False):
        if project_id not in self.registry.state['projects']:
            raise PolicyError('project_not_found', 404)
        if type(confirm_unconfirmed) is not bool:
            raise PolicyError('invalid_request_fields')
        explicit_single = agent_id is not None and host is not None
        if confirm_unconfirmed and not explicit_single:
            raise PolicyError('invalid_request_fields')
        await self.reconcile_project_processing(project_id, agent_id=agent_id, host=host, force=True)
        jobs = self._project_jobs_newest_first(project_id)
        if not jobs:
            return self.get(project_id)
        target_job = jobs[0]
        if confirm_unconfirmed:
            matched_job = next(
                (
                    job for job in jobs
                    if any(
                        t.get('processing') in ('unknown', 'failed')
                        and t.get('agent_id') == agent_id
                        and t.get('host') == host
                        for t in self._processing_targets(job)
                    )
                ),
                None,
            )
            if matched_job is not None:
                target_job = matched_job
        requeued = 0
        for target in self._processing_targets(target_job):
            if target.get('processing') not in ('unknown', 'failed'):
                continue
            if agent_id is not None and target.get('agent_id') != agent_id:
                continue
            if host is not None and target.get('host') != host:
                continue
            reconcile_status = str(target.get('reconcile_status') or '')
            blocked_before_submit = (
                target.get('processing') == 'unknown'
                and target.get('reason') == 'previous_submission_unconfirmed'
                and not target.get('processing_attempted_at')
            )
            # Reject blind resubmission when official status is still running,
            # unreachable/unavailable, or unconfirmed (unless an operator
            # explicitly confirms a single-target unconfirmed barrier after
            # fresh online reconciliation).
            if reconcile_status == 'running':
                continue
            if reconcile_status == 'unavailable' and not blocked_before_submit:
                continue
            if reconcile_status == 'unconfirmed' and not blocked_before_submit and not confirm_unconfirmed:
                continue
            other_targets = [
                other for job in self.jobs.values()
                for other in self._all_job_targets(job)
                if other is not target
                and other.get('agent_id') == target.get('agent_id')
                and other.get('host') == target.get('host')
            ]
            # An unresolved historical submitting/unknown remains a strict
            # barrier against duplicate submission (explicit confirm_unconfirmed
            # may also resolve historical unconfirmed records once confirmed not
            # running/unavailable on the official server).
            allowed_other_statuses = ('failed', 'unconfirmed') if confirm_unconfirmed else ('failed',)
            if any(
                other.get('processing') == 'submitting'
                or (other.get('processing') == 'unknown' and other.get('reconcile_status') not in allowed_other_statuses)
                for other in other_targets
            ):
                continue
            definite_failure = (
                reconcile_status == 'failed'
                or (target.get('processing') == 'failed' and not target.get('session_id'))
            )
            confirmed_unsubmitted = confirm_unconfirmed and reconcile_status == 'unconfirmed'
            if not (definite_failure or blocked_before_submit or confirmed_unsubmitted):
                continue
            try:
                _, view = self.checked_target(project_id, target, write=True)
                if not view['policy'].get('can_auto_process'):
                    raise PolicyError('processing_retry_denied', 403)
            except PolicyError as error:
                if explicit_single:
                    raise
                target['processing_reason'] = error.code
                continue
            # Clear resolved historical unknown records for this source so
            # process_saved / incremental_processing are not permanently wedged.
            for other in other_targets:
                if other.get('processing') == 'unknown' and other.get('reconcile_status') in allowed_other_statuses:
                    other.update(
                        previous_processing='unknown',
                        processing='failed',
                        recovery_reason=(
                            'operator_confirmed_unsubmitted'
                            if other.get('reconcile_status') == 'unconfirmed'
                            else 'official_activity_failed'
                        ),
                    )
            target.update(
                previous_processing=target['processing'],
                processing='queued',
                recovery_reason='operator_confirmed_unconfirmed_retry' if confirmed_unsubmitted else 'user_retry',
                processing_reason='',
                next_processing_at=0,
            )
            for key in ('reason', 'reconcile_status', 'reconciled_at', 'next_reconcile_at'):
                target.pop(key, None)
            requeued += 1
        if requeued:
            self.persist()
            self._kick_pending_processing(target_job)
            await asyncio.sleep(0)
        return self.get(project_id)

    async def run(self, row):
        try:
            row['status'] = 'reviewing'
            self.persist()
            # Freeze every original buffer before any POST. New arrivals remain
            # with the normal capture flow and are not silently added to consent.
            for target in row['targets']:
                if self.cancel_file(row).exists():
                    row.update(status='stopped', reason='user_stopped')
                    return
                if target.get('admission_blocked'):
                    continue
                try:
                    self.invalidate_review_cache(row['project_id'], target['agent_id'], target['host'])
                    snapshot = await self.execute(row, target, 'review')
                    inactive = snapshot.get('session_inactive') is True if isinstance(snapshot, dict) else False
                    if (not isinstance(snapshot, dict) or not isinstance(snapshot.get('session_id'), str)
                            or (not snapshot['session_id'] and not inactive)
                            or not isinstance(snapshot.get('tokens'), list)
                            or len(snapshot['tokens']) > 200000
                            or any(not isinstance(t, str) or not re.fullmatch('[0-9a-f]{64}', t) for t in snapshot['tokens'])
                            or type(snapshot.get('unknown')) is not int or snapshot['unknown'] < 0
                            or (inactive and (snapshot['tokens'] or snapshot['unknown'] > 0))):
                        reason = snapshot.get('reason') if isinstance(snapshot, dict) else None
                        raise PolicyError(reason if isinstance(reason, str) and reason else 'backlog_snapshot_unconfirmed', 503)
                except (PolicyError, OSError, ValueError) as error:
                    # Review is read-only. A missing mapping/invalid snapshot
                    # isolates this host, never authorizes its write and never
                    # prevents unrelated, verified hosts from being reviewed.
                    target.update(review_status='unconfirmed', review_reason=(
                        error.code if isinstance(error, PolicyError) else 'backlog_review_unconfirmed'))
                    self.persist()
                    continue
                target.update(snapshot)
                self.record_buffer_review(row['project_id'], target, snapshot)
                target.update(review_status='confirmed', review_reason='')
                target.update(saved=0, handled=0, isolated=0, remaining=len(target['tokens']))
                self.persist()
            for target in row['targets']:
                if self.cancel_file(row).exists():
                    row.update(status='stopped', reason='user_stopped')
                    return
                if target.get('review_status') != 'confirmed' or not target.get('tokens'):
                    continue
                def progress(value):
                    if value.get('status') not in ('running', 'waiting', 'completed', 'paused', 'stopped'):
                        raise PolicyError('backlog_progress_unconfirmed', 503)
                    for key in ('saved', 'handled', 'isolated', 'remaining'):
                        if type(value.get(key)) is not int or value[key] < 0:
                            raise PolicyError('backlog_progress_unconfirmed', 503)
                    if sum(value[k] for k in ('saved', 'handled', 'isolated', 'remaining')) != len(target['tokens']):
                        raise PolicyError('backlog_progress_unconfirmed', 503)
                    classifications = {key: value.get(key, 0) for key in ('filtered', 'rejected', 'deferred')}
                    if any(type(count) is not int or count < 0 for count in classifications.values()) or sum(classifications.values()) > value['isolated']:
                        raise PolicyError('backlog_progress_unconfirmed', 503)
                    target.update({k: value[k] for k in ('saved', 'handled', 'isolated', 'remaining')})
                    target.update(classifications)
                    row.update(status='stopping' if self.cancel_file(row).exists() else 'waiting' if value['status'] == 'waiting' else 'running',
                               reason=value.get('reason', ''))
                    self.persist()
                try:
                    result = await self.execute(row, target, 'save', progress)
                except PolicyError as error:
                    if error.code not in ('backlog_write_denied', 'backlog_binding_changed', 'backlog_installation_changed', 'backlog_installation_unconfirmed', 'backlog_host_unsupported'):
                        raise
                    target.update(save_status='paused', save_reason=error.code)
                    self.persist()
                    continue
                if not isinstance(result, dict) or result.get('status') not in ('completed', 'paused', 'stopped'):
                    raise PolicyError('backlog_worker_unconfirmed', 503)
                progress(result)
                target.update(save_status=result['status'], save_reason=result.get('reason', ''))
                retry_key = json.dumps([target['agent_id'], target['host']])
                if result['status'] == 'completed':
                    row.get('save_retries', {}).pop(retry_key, None)
                if result['status'] == 'paused' and result.get('reason') in ('write_denied', 'policy_unconfirmed', 'capture_request_rejected', 'capture_retry_pending', 'request_result_unknown', 'rate_limited'):
                    if result.get('reason') in ('capture_retry_pending', 'rate_limited'):
                        retries = row.setdefault('save_retries', {})
                        attempts = min(8, retries.get(retry_key, {}).get('attempts', 0)+1)
                        retries[retry_key] = {'attempts': attempts, 'after': time.time()+min(300, 2**attempts)}
                    self.persist()
                    continue
                if result['status'] != 'completed':
                    row.update(status=result['status'], reason=result.get('reason', 'worker_state_unconfirmed'))
                    if row['reason'] in ('capture_retry_pending', 'rate_limited'):
                        row['capture_retry_count'] = min(8, row.get('capture_retry_count', 0) + 1)
                        row['capture_retry_at'] = time.time() + min(300, 2 ** row['capture_retry_count'])
                    return
            row['status'] = 'processing'
            self.persist()
            for target in row['targets']:
                if target.get('review_status') != 'confirmed':
                    continue
                if row.get('automatic') and (target.get('saved') or target.get('handled')):
                    key = json.dumps([target['agent_id'], target['host']])
                    previous = row['pending_processing'].get(key, {})
                    if previous.get('processing') not in ('submitting', 'unknown'):
                        pending = {k: target[k] for k in ('agent_id','host','dataset','session_id','saved','handled')}
                        if 'native' in target:
                            pending['native'] = target['native']
                        row['pending_processing'][key] = {**previous, **pending,
                            'processing': 'deferred' if previous.get('processing') == 'deferred' else 'queued'}
                else:
                    await self.process_saved(row, target)
            row.update(status='stopped' if self.cancel_file(row).exists() else 'completed', reason='')
            if row['status'] == 'completed':
                if any(t.get('review_status') == 'unconfirmed' for t in row['targets']):
                    row['reason'] = 'partial_review_unconfirmed'
                if any(t.get('save_status') == 'paused' for t in row['targets']):
                    row['reason'] = 'partial_save_deferred'
                row['capture_retry_count'] = 0
                row.pop('capture_retry_at', None)
            if row.get('automatic') and row['status'] == 'completed':
                row['status'] = 'watching'
        except PolicyError as error:
            row.update(status='paused', reason=error.code)
        except Exception:
            row.update(status='paused', reason='backlog_result_unconfirmed')
        finally:
            row['updated_at'] = time.time()
            self.persist()

    async def close(self):
        if self.processing_task and not self.processing_task.done():
            row = self.jobs[self.processing_operation]
            self.stop(row['project_id'], self.processing_operation, owner_shutdown=True)
        tasks = [task for task in (self.check_monitor, *self.check_tasks.values()) if task]
        for task in tasks:
            task.cancel()
        await asyncio.gather(*tasks, return_exceptions=True)
        if self.monitor:
            self.monitor.cancel()
            try:
                await self.monitor
            except asyncio.CancelledError:
                pass
        for row in self.jobs.values():
            if row['status'] in ACTIVE:
                self.stop(row['project_id'], row['operation_id'], owner_shutdown=True)
        if self.task and not self.task.done():
            await asyncio.wait_for(asyncio.shield(self.task), 40)
        if self.processing_task and not self.processing_task.done():
            await asyncio.wait_for(asyncio.shield(self.processing_task), 40)

    async def automatic_tick(self):
        """Continue lawful, definitely-unsent buffers without requiring a window.

        Each project keeps one durable automatic operation. Explicit stops and
        unknown/failed operations remain paused; a new UUID never bypasses them.
        """
        if self.task and not self.task.done():
            return
        projects = list(self.registry.state['projects'])
        if not projects:
            return
        project_id = projects[self.auto_cursor % len(projects)]
        self.auto_cursor += 1
        rows = [r for r in self.jobs.values() if r['project_id'] == project_id]
        previous = max(rows, key=lambda r: r['created_at']) if rows else None
        recoverable = bool(previous and previous.get('automatic') and not previous.get('automatic_user_paused')
                           and previous['status'] == 'paused' and previous.get('reason') in ('policy_unconfirmed', 'capture_retry_pending', 'capture_filter_changed', 'rate_limited'))
        if recoverable and previous.get('reason') in ('capture_retry_pending', 'rate_limited'):
            if time.time() < previous.get('capture_retry_at', 0):
                return
        if previous and previous['status'] in ('paused', 'stopped', 'interrupted', 'stopping') and not recoverable:
            return
        automatic = previous if previous and previous.get('automatic') else None
        if automatic and not recoverable and self._kick_pending_processing(automatic):
            await asyncio.sleep(0)
        operation = automatic['operation_id'] if automatic else str(uuid.uuid4())
        candidate = {'operation_id': operation, 'project_id': project_id}
        selected = set()
        has_unsent = False
        reviewed = False
        review_cleared = False
        for raw, bound in list(self.registry.state['sessions'].items()):
            if bound != project_id:
                continue
            agent_id, host = json.loads(raw)
            agent = self.registry.state['agents'].get(agent_id, {})
            if not agent.get('installation') or agent_id in self.registry.state.get('agent_retirements', {}):
                continue
            target = {'agent_id': agent_id, 'host': host,
                      'dataset': self.registry.state['projects'][project_id]['dataset_id']}
            retry = (automatic or {}).get('save_retries', {}).get(json.dumps([agent_id, host]), {})
            if retry.get('after', 0) > time.time():
                continue
            try:
                _, view = self.checked_target(project_id, target)
                if not view['policy'].get('can_capture'):
                    continue
                if recoverable:
                    self.invalidate_review_cache(project_id, agent_id, host)
                snapshot = await self.execute(candidate, target, 'review')
                if not isinstance(snapshot.get('tokens'), list) or 'unknown' not in snapshot:
                    continue
                self.record_buffer_review(project_id, target, snapshot)
                if snapshot.get('blocked'):
                    continue
                reviewed = True
                if automatic:
                    for old in automatic.get('targets', []):
                        if (old.get('agent_id') == agent_id and old.get('host') == host
                                and old.get('review_status') == 'unconfirmed'
                                and old.get('review_reason') in ('backlog_write_denied', 'policy_unconfirmed')):
                            old.pop('review_status', None)
                            old.pop('review_reason', None)
                            if old.get('save_reason') in ('backlog_write_denied', 'policy_unconfirmed'):
                                old.pop('save_status', None)
                                old.pop('save_reason', None)
                            review_cleared = True
                if recoverable and (not snapshot['unknown'] or snapshot.get('tokens')):
                    for old in previous['targets']:
                        if old['agent_id'] != agent_id or old['host'] != host:
                            continue
                        # Normal official hooks may have drained this frozen
                        # batch while the manager was paused. Reconcile counts
                        # without re-sending or claiming their work as ours.
                        remaining = sum((Counter(old.get('tokens', [])) & Counter(snapshot['tokens'])).values())
                        filtered = sum((Counter(old.get('tokens', [])) & Counter(snapshot.get('filtered_tokens', []))).values())
                        isolated = max(0, old.get('isolated', 0)-old.get('filtered', 0)) + filtered
                        consumed = len(old.get('tokens', [])) - remaining - old.get('saved', 0) - isolated
                        old.update(remaining=remaining, filtered=filtered, isolated=isolated, handled=max(old.get('handled', 0), consumed))
                if snapshot.get('tokens') or snapshot.get('unknown'):
                    selected.add((agent_id, host))
                has_unsent = has_unsent or bool(snapshot.get('tokens'))
            except (PolicyError, OSError, ValueError):
                # The normal queue view exposes the original unconfirmed state;
                # do not install, repair permissions or replay in this scanner.
                continue
        if recoverable and reviewed:
            previous.update(status='watching', reason='')
            self.persist()
        elif review_cleared:
            self.persist()
        if has_unsent and not (self.task and not self.task.done()):
            self.start(project_id, operation, automatic=True, selected=selected)

    async def automatic_loop(self):
        while True:
            try:
                await self.automatic_tick()
            except (PolicyError, OSError, ValueError):
                pass
            await asyncio.sleep(30)


def attach_backlog(app, registry, runtime):
    backlog = Backlog(registry, runtime)
    async def endpoint(request):
        if not request['manager']:
            raise PolicyError('management_required', 403)
        project = request.match_info['id']
        if request.method == 'GET':
            return web.json_response(backlog.get(project))
        if request.path.endswith('/processing/check') or request.path.endswith('/processing/retry'):
            raw_body = await request.read()
            if raw_body:
                if request.content_type != 'application/json':
                    raise PolicyError('json_required', 415)
                try:
                    payload = json.loads(raw_body)
                except ValueError:
                    raise PolicyError('invalid_request_fields') from None
                is_retry = request.path.endswith('/processing/retry')
                allowed_sets = (
                    (set(), {'agent_id', 'host'}, {'agent_id', 'host', 'confirm_unconfirmed'})
                    if is_retry else (set(), {'agent_id', 'host'})
                )
                if not isinstance(payload, dict) or set(payload) not in allowed_sets:
                    raise PolicyError('invalid_request_fields')
                for field in ('agent_id', 'host'):
                    if field in payload and (not isinstance(payload[field], str) or not payload[field]):
                        raise PolicyError('invalid_request_fields')
                if 'confirm_unconfirmed' in payload and type(payload['confirm_unconfirmed']) is not bool:
                    raise PolicyError('invalid_request_fields')
            else:
                payload = {}
            agent_id = payload.get('agent_id')
            host = payload.get('host')
            if request.path.endswith('/processing/check'):
                await backlog.reconcile_project_processing(project, agent_id=agent_id, host=host, force=True)
                return web.json_response(backlog.get(project))
            return web.json_response(await backlog.retry_project_processing(
                project,
                agent_id=agent_id,
                host=host,
                confirm_unconfirmed=bool(payload.get('confirm_unconfirmed', False)),
            ))
        if request.content_type != 'application/json':
            raise PolicyError('json_required', 415)
        payload = await request.json()
        if request.path.endswith('/check'):
            if not isinstance(payload, dict) or set(payload) != {'agent_id', 'host'} or not all(isinstance(v, str) and v for v in payload.values()):
                raise PolicyError('invalid_request_fields')
            if project not in registry.state['projects']:
                raise PolicyError('project_not_found', 404)
            backlog.request_check(project, payload['agent_id'], payload['host'], force=True)
            return web.json_response(backlog.get(project))
        if not isinstance(payload, dict) or set(payload) != {'operation_id'}:
            raise PolicyError('invalid_request_fields')
        result = (backlog.stop(project, payload['operation_id']) if request.path.endswith('/stop')
                  else backlog.start(project, payload['operation_id']))
        return web.json_response({'job': result})
    app.add_routes([web.get('/v1/projects/{id}/backlog', endpoint),
                    web.post('/v1/projects/{id}/backlog', endpoint),
                    web.post('/v1/projects/{id}/backlog/check', endpoint),
                    web.post('/v1/projects/{id}/backlog/processing/check', endpoint),
                    web.post('/v1/projects/{id}/backlog/processing/retry', endpoint),
                    web.post('/v1/projects/{id}/backlog/stop', endpoint)])
    async def cleanup(_):
        await backlog.close()
    async def startup(_):
        backlog.monitor = asyncio.create_task(backlog.automatic_loop())
        backlog.check_monitor = asyncio.create_task(backlog.automatic_check_loop())
    app.on_startup.append(startup)
    app.on_cleanup.append(cleanup)
    return backlog
