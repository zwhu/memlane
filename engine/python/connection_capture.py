"""Connection execution permission and stored-message evidence, never content."""
import asyncio
import copy
import hashlib
import json
from pathlib import Path
import time
from urllib.parse import quote

from codex_hook_access import CodexHookAccess
from project_policy import DEFAULTS, PolicyError, effective

KINDS = {'codex', 'pi', 'traex', 'antigravity'}
ELIGIBLE = {'configured_pending_new_session', 'environment_receipt_unconfirmed'}


def valid_saved_timestamp(saved, activated, now):
    """Return latest post-activation saved timestamp across QA and tool trace writes."""
    candidates = []
    for count_key, ts_key in (('qa_count', 'last_qa_at'), ('trace_count', 'last_trace_at')):
        ts = saved.get(ts_key)
        count = saved.get(count_key)
        if type(ts) in (int, float) and type(count) is int and count > 0 and activated <= ts <= now + 60:
            candidates.append(ts)
    return max(candidates) if candidates else None


class ConnectionCapture:
    def __init__(self, registry, config, *, host_factory=CodexHookAccess):
        self.registry, self.config, self.host_factory = registry, config, host_factory
        self.cache, self.tasks, self.last_verified_session = {}, {}, {}

    def record(self, identifier, kind):
        return self.registry.state.get('codex_connections' if kind == 'codex' else 'connections', {}).get(identifier, {})

    def key(self, row, adapter):
        agent = self.registry.state['agents'].get(row['instance_id'], {})
        record = self.record(row['instance_id'], row['kind'])
        fingerprints = []
        for path in (getattr(adapter, 'executable', ''),
                     str(Path(row.get('profile') or row.get('codex_home', '')) / 'config.toml') if row['kind'] == 'codex' else ''):
            try:
                stat = Path(path).stat()
                fingerprints.append((stat.st_ino, stat.st_size, stat.st_mtime_ns))
            except (OSError, ValueError):
                fingerprints.append(None)
        receipt = self.registry.state.get('connection_receipts', {}).get(row['instance_id'], {})
        project = self.registry.state['projects'].get(receipt.get('project_id'), {})
        value = [row['status'], bool(row.get('environment_verified')), row.get('profile'), agent.get('host_profile'), agent.get('principal_id'),
                 agent.get('installation', {}).get('manifest_sha256'), agent.get('installation', {}).get('marketplace_name'),
                 record.get('activated_at', 0), record.get('capture_deferred', False), fingerprints,
                 receipt.get('host_session_key'), receipt.get('environment_verified'),
                 self.registry.state.get('defaults', DEFAULTS), project.get('overrides', {}).get(row['instance_id'], {}),
                 project.get('status'), project.get('desired_revision'), project.get('applied', {}).get(row['instance_id'])]
        return hashlib.sha256(json.dumps(value, sort_keys=True).encode()).hexdigest()

    def view(self, row, adapter):
        if row['kind'] not in KINDS or row['status'] not in ELIGIBLE:
            return None
        key = self.key(row, adapter)
        cached = self.cache.get(row['instance_id'])
        if cached and cached['key'] == key:
            return copy.deepcopy(cached['value'])
        return {'state': 'checking', 'permission': 'unknown'}

    async def refresh(self, rows, adapters, *, force=False):
        pending = []
        for row in rows:
            identifier = row['instance_id']
            adapter = adapters.get(identifier)
            if row['kind'] not in KINDS or row['status'] not in ELIGIBLE or adapter is None:
                continue
            key = self.key(row, adapter)
            cached = self.cache.get(identifier)
            if not force and cached and cached['key'] == key and time.time() - cached['value']['checked_at'] < 15:
                continue
            task_key = (identifier, key)
            task = self.tasks.get(task_key)
            if task is None:
                task = asyncio.create_task(self._refresh_one(copy.deepcopy(row), adapter, key))
                self.tasks[task_key] = task
                task.add_done_callback(lambda _, k=task_key: self.tasks.pop(k, None))
            pending.append(asyncio.shield(task))
        if pending:
            await asyncio.gather(*pending)

    async def _refresh_one(self, row, adapter, key):
        previous = self.cache.get(row['instance_id'], {})
        try:
            value = await asyncio.wait_for(self.inspect(row, adapter), 10)
        except (PolicyError, OSError, ValueError, KeyError, TypeError, asyncio.TimeoutError):
            value = {'state': 'check_failed', 'permission': 'unknown', 'reason': 'capture_status_unavailable'}
            if previous.get('key') == key:
                value['last_saved_at'] = previous['value'].get('last_saved_at', 0)
        value['checked_at'] = time.time()
        if self.key(row, adapter) == key:
            self.cache[row['instance_id']] = {'key': key, 'value': value}

    def _eligible_project(self, identifier, cand_host_key, cand_project_id):
        if not cand_host_key or not cand_project_id:
            return None
        if self.registry.state.get('sessions', {}).get(json.dumps([identifier, cand_host_key])) != cand_project_id:
            return None
        project = self.registry.state.get('projects', {}).get(cand_project_id, {})
        policy = effective(self.registry.state.get('defaults', DEFAULTS), project.get('overrides', {}).get(identifier, {}))
        if not policy['can_capture']:
            return None
        applied = project.get('applied', {}).get(identifier, {})
        if (project.get('status') != 'verified' or applied.get('status') != 'verified'
                or applied.get('revision') != project.get('desired_revision')):
            return None
        return project

    async def _probe_saved_at(self, agent, project, host_key, activated, timeout):
        safe = ''.join(ch if ch.isascii() and (ch.isalnum() or ch in '-_.') else '_' for ch in host_key).strip('._')[:120]
        session_id = 'codex_' + safe
        try:
            saved = await asyncio.wait_for(self.registry.official.request('GET',
                '/api/v1/sessions/' + quote(session_id, safe='') + '/capture-status', token_file=agent['key_file']), timeout)
        except PolicyError as error:
            if error.code == 'official_http_404':
                return None
            raise
        if (saved.get('session_id') != session_id or saved.get('user_id') != agent['principal_id']
                or saved.get('dataset_id') != project.get('dataset_id')):
            raise PolicyError('capture_saved_identity_mismatch', 503)
        return valid_saved_timestamp(saved, activated, time.time())

    async def inspect(self, row, adapter):
        identifier, kind = row['instance_id'], row['kind']
        agent = self.registry.state['agents'][identifier]
        record = self.record(identifier, kind)
        installation = agent['installation']
        value = {'state': 'waiting_session', 'permission': 'unverified'}
        if kind == 'codex':
            async with self.host_factory(adapter.executable, adapter.home) as host:
                access = await host.inspect(installation)
            value.update(permission=access['permission'], review_sha=access['review_sha'])
            if access['permission'] != 'granted':
                value['state'] = 'deferred' if record.get('capture_deferred') else 'needs_permission'
                return value
        receipt = self.registry.state.get('connection_receipts', {}).get(identifier, {})
        activated = record.get('activated_at') or 0
        if not receipt.get('environment_verified') or receipt.get('received_at', 0) < activated:
            return value
        # A verified resolve proves hook/extension execution, not a completed save.
        value['permission'] = 'granted' if kind == 'codex' else 'observed'
        value['state'] = 'waiting_message'
        host_key = receipt.get('host_session_key', '')
        if not host_key or self.registry.state['sessions'].get(json.dumps([identifier, host_key])) != receipt.get('project_id'):
            raise PolicyError('capture_session_identity_unconfirmed', 503)
        project = self.registry.state['projects'].get(receipt['project_id'], {})
        policy = effective(self.registry.state.get('defaults', DEFAULTS), project.get('overrides', {}).get(identifier, {}))
        if not policy['can_capture']:
            return {**value, 'state': 'project_disabled', 'project_id': receipt['project_id']}
        applied = project.get('applied', {}).get(identifier, {})
        if (project.get('status') != 'verified' or applied.get('status') != 'verified'
                or applied.get('revision') != project.get('desired_revision')):
            return {**value, 'state': 'project_pending', 'project_id': receipt['project_id']}
        last = await self._probe_saved_at(agent, project, host_key, activated, 4)
        if last is not None:
            self.last_verified_session[identifier] = (host_key, receipt['project_id'])
            value.update(state='healthy', last_saved_at=last)
            return value
        # New empty sessions must not regress previously verified auto-capture health.
        candidates = []
        seen_keys = {host_key}
        last_verified = self.last_verified_session.get(identifier)
        if last_verified:
            cand_key, cand_pid = last_verified
            if cand_key not in seen_keys:
                cand_proj = self._eligible_project(identifier, cand_key, cand_pid)
                if cand_proj is not None:
                    seen_keys.add(cand_key)
                    candidates.append((cand_key, cand_pid, cand_proj))
        for item in receipt.get('recent_sessions') or []:
            if len(candidates) >= 6:
                break
            if not isinstance(item, dict):
                continue
            cand_key, cand_pid = item.get('host_session_key', ''), item.get('project_id', '')
            if cand_key in seen_keys:
                continue
            cand_proj = self._eligible_project(identifier, cand_key, cand_pid)
            if cand_proj is not None:
                seen_keys.add(cand_key)
                candidates.append((cand_key, cand_pid, cand_proj))
        before_activation = set(record.get('sessions_before_activation') or ())
        for session_json, cand_pid in reversed(list(self.registry.state.get('sessions', {}).items())):
            if len(candidates) >= 6:
                break
            if session_json in before_activation:
                continue
            try:
                parsed = json.loads(session_json)
            except (ValueError, TypeError):
                continue
            if not isinstance(parsed, list) or len(parsed) != 2 or parsed[0] != identifier:
                continue
            cand_key = parsed[1]
            if cand_key in seen_keys:
                continue
            cand_proj = self._eligible_project(identifier, cand_key, cand_pid)
            if cand_proj is not None:
                seen_keys.add(cand_key)
                candidates.append((cand_key, cand_pid, cand_proj))
        for cand_key, cand_pid, cand_proj in candidates:
            last = await self._probe_saved_at(agent, cand_proj, cand_key, activated, 2)
            if last is not None:
                self.last_verified_session[identifier] = (cand_key, cand_pid)
                value.update(state='healthy', last_saved_at=last)
                return value
        self.last_verified_session.pop(identifier, None)
        return value

    async def allow(self, adapter, reviewed_sha):
        agent = self.registry.state['agents'][adapter.id]
        async with self.host_factory(adapter.executable, adapter.home) as host:
            await host.allow(agent['installation'], reviewed_sha)
        self.record(adapter.id, 'codex')['capture_deferred'] = False
        self.cache.pop(adapter.id, None)
        self.last_verified_session.pop(adapter.id, None)

    async def close(self):
        tasks = list(self.tasks.values())
        for task in tasks:
            task.cancel()
        await asyncio.gather(*tasks, return_exceptions=True)
