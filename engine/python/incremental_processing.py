"""Idle admission of saved, completed conversation units into official improve.

Only scheduling metadata is stored. The plugin/engine retain their own source
buffers, retry policy, incremental windows, deduplication and processing watermarks.
"""
import asyncio
from datetime import datetime, timezone
import hashlib
import json
from pathlib import Path
import time
from urllib.parse import quote
import uuid

from project_control import private_write, loopback_origin, read_secret
from project_policy import PolicyError
from performance_trace import RequestTrace
from aiohttp import web

QUIET_SECONDS = 30
MAX_WAIT_SECONDS = 300
COOLDOWN_SECONDS = 300


def timestamp(value):
    try:
        at = datetime.fromisoformat(str(value).replace('Z', '+00:00'))
        return at.replace(tzinfo=at.tzinfo or timezone.utc).timestamp()
    except (ValueError, TypeError):
        return 0


class IncrementalProcessing:
    def __init__(self, backlog, *, clock=time.time, available=None):
        self.backlog = backlog
        self.registry = backlog.registry
        self.clock = clock
        self.path = self.registry.path.parent / 'incremental-processing.json'
        if self.path.exists() and (self.path.is_symlink() or not self.path.is_file()
                or self.path.stat().st_mode & 0o077 or self.path.stat().st_size > 8 * 1024 * 1024):
            raise ValueError('private_incremental_state_required')
        self.state = json.loads(self.path.read_text()) if self.path.exists() else {
            'schema_version': 1, 'enabled_at': clock(), 'sessions': {}}
        if self.state.get('schema_version') != 1:
            raise ValueError('incremental_processing_state_unconfirmed')
        self.available = available or self.capacity_available
        self.cursor = 0
        self.monitor = None
        for row in self.state['sessions'].values():
            if row.get('status') == 'submitting':
                row['status'] = 'unknown'
        self.save()

    def save(self):
        private_write(self.path, json.dumps(self.state, sort_keys=True).encode())

    async def capacity_available(self):
        control = self.backlog.runtime.get('project_control')
        if control:
            capacity = await self.registry.official.request('GET', '/api/v1/memlane/tool-graphs/capacity',
                adapter_token=read_secret(control['management_token_file']))
            for field in ('pending_background_tasks', 'tool_graph_jobs_running'):
                if type(capacity.get(field)) is not int or capacity[field] < 0:
                    raise PolicyError('processing_state_unconfirmed', 503)
                if capacity[field]:
                    return False
        if self.backlog.runtime.get('generation_mode') == 'agent_bridge':
            import aiohttp
            url = loopback_origin(self.backlog.runtime['completion_url'])
            try:
                async with self.registry.official.session.get(url + '/health', timeout=aiohttp.ClientTimeout(total=3)) as response:
                    if response.status != 200:
                        return False
                    health = await response.json()
                    if health.get('active_or_waiting') != 0:
                        return False
            except (aiohttp.ClientError, asyncio.TimeoutError, ValueError):
                return False
        # Processing state is engine-owned. Pair native pipeline events by ID;
        # a STARTED event is not active after its matching terminal event.
        rows = []
        tokens = [None] + sorted({actor['key_file'] for identifier, actor in self.registry.state.get('agents', {}).items()
            if actor.get('installation') and identifier not in self.registry.state.get('agent_retirements', {})})
        # Activity visibility is identity-scoped. Management credentials alone
        # do not establish that every child Agent has no running pipeline.
        for token in tokens:
            page = await self.registry.official.request('GET', '/api/v1/activity/pipeline-runs?limit=500', token_file=token)
            if not isinstance(page, list):
                raise PolicyError('processing_state_unconfirmed', 503)
            rows.extend(page)
        terminal = {r['pipeline_run_id'] for r in rows if r.get('status') in (
            'DATASET_PROCESSING_COMPLETED', 'DATASET_PROCESSING_ERRORED')}
        for row in rows:
            if row.get('kind') == 'operation' and not row.get('ended_at'):
                return False
            if row.get('status') == 'DATASET_PROCESSING_STARTED' and row.get('pipeline_run_id') not in terminal:
                return False
        return True

    async def tick(self):
        bindings = list(self.registry.state['sessions'].items())
        if not bindings:
            return
        # Inspect a few metadata-only snapshots per tick, rotating fairly.
        for _ in range(min(4, len(bindings))):
            raw, project = bindings[self.cursor % len(bindings)]
            self.cursor += 1
            agent_id, host = json.loads(raw)
            key = hashlib.sha256(raw.encode()).hexdigest()
            target = {'agent_id': agent_id, 'host': host,
                'dataset': self.registry.state['projects'][project]['dataset_id']}
            try:
                agent, access = self.backlog.checked_target(project, target, write=False)
            except PolicyError:
                if key in self.state['sessions']:
                    self.state['sessions'][key].update(status='disabled', pending_since=0)
                    self.save()
                continue
            if not agent.get('installation') or 'read' not in access['policy']['permissions']:
                if key in self.state['sessions']:
                    self.state['sessions'][key].update(status='disabled', pending_since=0)
                    self.save()
                continue
            prior = self.state['sessions'].get(key, {})
            # A user pause/uncertain submit remains a barrier, including across
            # restarts. A new source count is never used to erase that barrier.
            blocked = any(r.get('project_id') == project and (
                r.get('automatic_user_paused') or r.get('status') in ('stopped', 'stopping', 'interrupted')
                or any(t.get('agent_id') == agent_id and t.get('host') == host
                    and t.get('processing') in ('submitting', 'unknown') for t in
                    list(r.get('targets', [])) + list(r.get('pending_processing', {}).values())))
                for r in self.backlog.jobs.values())
            if blocked or prior.get('status') in ('submitting', 'unknown'):
                continue
            operation = {'project_id': project, 'operation_id': prior.get('operation_id') or str(uuid.uuid4()), 'automatic': True}
            snapshot = await self.backlog.execute(operation, target, 'review')
            if snapshot.get('blocked') or snapshot.get('tokens') or not snapshot.get('session_id'):
                continue  # the existing sender owns unsaved material
            session = snapshot['session_id']
            detail = await self.registry.official.request('GET', '/api/v1/sessions/' + quote(session, safe=''), token_file=agent['key_file'])
            if detail.get('dataset_id') != target['dataset'] or detail.get('user_id') != agent['principal_id']:
                raise PolicyError('incremental_source_identity_unconfirmed', 409)
            counts = [detail.get('msg_count'), detail.get('tool_calls')]
            if any(type(n) is not int or n < 0 for n in counts) or counts[0] == 0:
                continue
            now = self.clock()
            source_at = timestamp(detail.get('last_activity_at'))
            # Tool-only progress is not a completed conversational unit. Never
            # start on a partly answered turn merely because tools went quiet.
            qa_at = max((timestamp(q.get('time')) for q in detail.get('qas', []) if isinstance(q, dict)), default=0)
            trace_at = max((timestamp(t.get('time')) for t in detail.get('traces', []) if isinstance(t, dict)), default=0)
            changed = prior.get('counts') != counts or prior.get('session_id') != session
            row = prior or {'session_id': session, 'status': 'watching'}
            if changed:
                # No historical replay on initial installation. A newly saved
                # unit or subsequent change starts the incremental admission.
                fresh = (not prior and source_at >= self.state['enabled_at']) or (
                    prior and prior.get('status') != 'disabled' and prior.get('session_id') == session and counts[0] > prior['counts'][0])
                row.update(counts=counts, session_id=session, changed_at=now)
                if fresh and not row.get('pending_since'):
                    row['pending_since'] = now
            allowed = access['policy'].get('can_auto_process', False)
            if not allowed:
                row.update(pending_since=0, status='disabled')
            elif row.get('status') == 'disabled':
                row['status'] = 'watching'
            self.state['sessions'][key] = row
            self.save()
            if (not allowed or not row.get('pending_since') or row.get('submitted_counts') == counts
                    or now < row.get('next_attempt_at', 0)):
                continue
            if not qa_at or trace_at > qa_at:
                row['waiting_reason'] = 'waiting_for_completed_turn'
                self.save()
                continue
            quiet = now - row['changed_at'] >= QUIET_SECONDS
            overdue = now - row['pending_since'] >= MAX_WAIT_SECONDS
            if not quiet and not overdue:
                continue
            try:
                available = await self.available()
            except (PolicyError, OSError, ValueError, asyncio.TimeoutError):
                row['waiting_reason'] = 'processing_state_unconfirmed'
                self.save()
                return
            if not available:
                row['waiting_reason'] = 'processing_busy'
                self.save()
                return
            # Recheck binding/permissions at admission; the plugin checks again
            # before submitting under the original installation credentials.
            _, access = self.backlog.checked_target(project, target)
            if not access['policy'].get('can_auto_process'):
                continue
            target.update(snapshot, incremental_cooldown_seconds=COOLDOWN_SECONDS)
            row.update(status='submitting', operation_id=str(uuid.uuid4()), waiting_reason='', dispatch_counts=list(counts))
            operation['operation_id'] = row['operation_id']
            self.save()
            trace = RequestTrace('incremental_processing')
            trace.transition('official_submission')
            try:
                result = await self.backlog.execute(operation, target, 'process')
                status = result.get('status', 'unknown')
                row.update(status=status, reason=result.get('reason', ''), attempted_at=now)
                if status in ('submitted', 'skipped'):
                    row.update(submitted_counts=list(counts), pending_since=0)
                elif status == 'deferred':
                    row['next_attempt_at'] = now + (300 if result.get('reason') in ('cooldown', 'backoff') else 30)
                elif status not in ('deferred', 'disabled'):
                    row['status'] = 'unknown'
            except BaseException:
                row['status'] = 'unknown'
                raise
            finally:
                self.save()
                trace.finish(row['status'])
            return  # at most one improve admission per pass

    async def loop(self):
        while True:
            try:
                await self.tick()
            except (PolicyError, OSError, ValueError, KeyError, TypeError, asyncio.TimeoutError):
                pass
            await asyncio.sleep(5)

    async def close(self):
        if self.monitor:
            self.monitor.cancel()
            await asyncio.gather(self.monitor, return_exceptions=True)


def attach_incremental_processing(app, backlog):
    processing = IncrementalProcessing(backlog)
    async def status(request):
        if not request['manager']:
            raise PolicyError('management_required', 403)
        now = processing.clock()
        return web.json_response({'enabled': bool(processing.monitor and not processing.monitor.done()),
            'quiet_seconds': QUIET_SECONDS, 'max_wait_seconds': MAX_WAIT_SECONDS,
            'max_wait_requires_capacity': True,
            'sessions': [{'session_key': key, 'status': row.get('status'),
                'pending_seconds': max(0, now - row['pending_since']) if row.get('pending_since') else 0,
                'waiting_reason': row.get('waiting_reason', ''), 'reason': row.get('reason', '')}
                for key, row in processing.state['sessions'].items()]})
    app.router.add_get('/v1/processing/incremental', status)
    async def startup(_):
        processing.monitor = asyncio.create_task(processing.loop())
    async def cleanup(_):
        await processing.close()
    app.on_startup.append(startup)
    # Stop the incremental submitter before the original sender is closed.
    app.on_cleanup.insert(0, cleanup)
    return processing
