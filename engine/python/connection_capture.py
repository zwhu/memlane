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


class ConnectionCapture:
    def __init__(self, registry, config, *, host_factory=CodexHookAccess):
        self.registry, self.config, self.host_factory = registry, config, host_factory
        self.cache, self.tasks = {}, {}

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
        value = [row['status'], row.get('profile'), agent.get('host_profile'), agent.get('principal_id'),
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

    async def refresh(self, rows, adapters):
        pending = []
        for row in rows:
            identifier = row['instance_id']
            adapter = adapters.get(identifier)
            if row['kind'] not in KINDS or row['status'] not in ELIGIBLE or adapter is None:
                continue
            key = self.key(row, adapter)
            cached = self.cache.get(identifier)
            if cached and cached['key'] == key and time.time() - cached['value']['checked_at'] < 15:
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
        safe = ''.join(ch if ch.isascii() and (ch.isalnum() or ch in '-_.') else '_' for ch in host_key).strip('._')[:120]
        session_id = 'codex_' + safe
        try:
            saved = await asyncio.wait_for(self.registry.official.request('GET',
                '/api/v1/sessions/' + quote(session_id, safe='') + '/capture-status', token_file=agent['key_file']), 4)
        except PolicyError as error:
            if error.code == 'official_http_404':
                return value
            raise
        if (saved.get('session_id') != session_id or saved.get('user_id') != agent['principal_id']
                or saved.get('dataset_id') != project.get('dataset_id')):
            raise PolicyError('capture_saved_identity_mismatch', 503)
        last = saved.get('last_qa_at')
        if (type(last) in (int, float) and activated <= last <= time.time() + 60
                and type(saved.get('qa_count')) is int and saved['qa_count'] > 0):
            value.update(state='healthy', last_saved_at=last)
        return value

    async def allow(self, adapter, reviewed_sha):
        agent = self.registry.state['agents'][adapter.id]
        async with self.host_factory(adapter.executable, adapter.home) as host:
            await host.allow(agent['installation'], reviewed_sha)
        self.record(adapter.id, 'codex')['capture_deferred'] = False
        self.cache.pop(adapter.id, None)

    async def close(self):
        tasks = list(self.tasks.values())
        for task in tasks:
            task.cancel()
        await asyncio.gather(*tasks, return_exceptions=True)
