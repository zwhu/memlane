"""Project-scoped UI/job coordination around the official SDK adapter."""
import asyncio
from collections import OrderedDict
import copy
from datetime import datetime, timezone
import hashlib
import json
from pathlib import Path
import sqlite3
import uuid

from aiohttp import web
from _runtime_common import private_write, read_secret
from project_policy import PolicyError
from sqlite_ro import id_variants, official_sqlite_ro
from tool_graph_policy import settings

TOOL_GRAPH_PREVIEW_BATCH_SIZE = 128
TOOL_GRAPH_BUILD_BATCH_SIZE = 64
_MAX_COLD_SESSION_CACHE = 65536
_MAX_RETAINED_TERMINAL_JOBS = 200
_TERMINAL_JOB_STATUSES = frozenset({'completed', 'cancelled'})
_SQL_IN_BATCH = 400
_MAX_HTTP_SESSIONS_OFFSET = 500_000


def _in_clause(variants):
    if len(variants) == 1:
        return '= ?', variants
    return f"IN ({','.join('?' for _ in variants)})", variants


def _cache_now_space() -> str:
    return datetime.now(timezone.utc).strftime('%Y-%m-%d %H:%M:%S.%f')


class ToolGraphControl:
    def __init__(self, registry, runtime):
        self.registry, self.runtime = registry, runtime
        self.path = Path(runtime['project_control']['registry_file']).parent / 'tool-graph-control-jobs.json'
        self.jobs = json.loads(self.path.read_text()) if self.path.exists() else {}
        self.tasks = {}
        self.previews = {}
        self.loop_task = None
        # Cold-session watermark cache: (dataset_id, user_id, session_id) -> (last_trace_count, last_pending)
        self._cold_session_cache: OrderedDict[tuple[str, str, str], tuple[int, int]] = OrderedDict()
        for row in self.jobs.values():
            if row['status'] in ('queued', 'running', 'stopping'):
                row['status'] = 'result_unknown'
        self.save()

    def _prune_terminal_jobs(self):
        terminal = [
            (str(row.get('created_at') or ''), op_id)
            for op_id, row in self.jobs.items()
            if row.get('status') in _TERMINAL_JOB_STATUSES
        ]
        if len(terminal) <= _MAX_RETAINED_TERMINAL_JOBS:
            return
        terminal.sort()
        excess = len(terminal) - _MAX_RETAINED_TERMINAL_JOBS
        for _, op_id in terminal[:excess]:
            self.jobs.pop(op_id, None)

    def save(self):
        self._prune_terminal_jobs()
        private_write(self.path, json.dumps(self.jobs, sort_keys=True).encode())

    def _set_cold_cache(self, dataset_id: str, user_id: str, session_id: str, count: int, pending: int):
        key = (dataset_id, user_id, session_id)
        self._cold_session_cache[key] = (count, pending)
        self._cold_session_cache.move_to_end(key)
        while len(self._cold_session_cache) > _MAX_COLD_SESSION_CACHE:
            self._cold_session_cache.popitem(last=False)

    def _invalidate_cold_cache(self, dataset_id: str, user_id: str, session_ids):
        for sid in session_ids:
            self._cold_session_cache.pop((dataset_id, user_id, sid), None)

    def _prefilter_sessions_sqlite(self, dataset_id: str, principal_id: str, only_sessions=None):
        """Read-only SQLite pre-filter returning {session_id: active_trace_count} for sessions with traces > 0.

        Returns ``None`` when official SQLite paths are not configured/present so callers
        can fall back cleanly to paginated official ``/api/v1/sessions``.
        Never treats trace counts as pending counts; official ``/preview`` always checks
        real ``TRACE_PERSIST_WATERMARK``.
        """
        official_data = self.runtime.get('official_data')
        if not official_data:
            return None
        db_dir = Path(official_data) / 'system' / 'databases'
        cognee_db = db_dir / 'cognee_db'
        cache_db = db_dir / 'cache.db'
        if not cognee_db.is_file() or cognee_db.is_symlink():
            return None
        if not cache_db.is_file() or cache_db.is_symlink():
            return None

        ds_variants = id_variants(dataset_id)
        user_variants = id_variants(principal_id)
        if not ds_variants or not user_variants:
            return None
        ds_sql, ds_params = _in_clause(ds_variants)
        user_sql, user_params = _in_clause(user_variants)

        only_set = set(only_sessions) if only_sessions is not None else None
        try:
            with official_sqlite_ro(cognee_db) as con:
                session_ids = []
                seen = set()
                for (sid,) in con.execute(
                    f'''SELECT session_id FROM session_records
                        WHERE dataset_id {ds_sql} AND user_id {user_sql}''',
                    (*ds_params, *user_params),
                ):
                    sid_str = str(sid or '')
                    if not sid_str or sid_str in seen:
                        continue
                    if only_set is not None and sid_str not in only_set:
                        continue
                    seen.add(sid_str)
                    session_ids.append(sid_str)
        except (PolicyError, sqlite3.Error):
            return None

        if not session_ids:
            return {}

        now_space = _cache_now_space()
        active_counts = {}
        try:
            with official_sqlite_ro(cache_db) as con:
                for start in range(0, len(session_ids), _SQL_IN_BATCH):
                    batch = session_ids[start:start + _SQL_IN_BATCH]
                    placeholders = ','.join('?' for _ in batch)
                    for sid, cnt in con.execute(
                        f'''SELECT session_id, COUNT(*)
                            FROM cache_trace_entries
                            WHERE user_id {user_sql}
                              AND session_id IN ({placeholders})
                              AND (expires_at IS NULL OR REPLACE(substr(expires_at, 1, 26), 'T', ' ') > ?)
                            GROUP BY session_id''',
                        (*user_params, *batch, now_space),
                    ):
                        cnt_int = int(cnt)
                        if cnt_int > 0:
                            active_counts[str(sid)] = cnt_int
        except (PolicyError, sqlite3.Error):
            return None
        return active_counts

    def project(self, project):
        row = self.registry.state['projects'].get(project)
        if not row or row.get('status') != 'verified' or not row.get('dataset_id'):
            raise PolicyError('tool_graph_project_unconfirmed', 409)
        return row

    async def actor(self, project, actor):
        policy = await self.registry.checked_policy(self.project(project), actor)
        if not policy['ready'] or 'write' not in policy['policy']['permissions']:
            raise PolicyError('tool_graph_write_required', 403)
        return self.registry.state['agents'][actor]

    async def call(self, method, path, actor, body=None):
        return await self.registry.official.request(method, '/api/v1/memlane/tool-graphs' + path, body,
            token_file=actor['key_file'], adapter_token=read_secret(self.runtime['project_control']['management_token_file']))

    def latest(self, project):
        rows = [row for row in self.jobs.values() if row['project_id'] == project]
        return copy.deepcopy(max(rows, key=lambda row: row['created_at'])) if rows else None

    async def preview(self, project, only=None):
        group = self.project(project)
        plan = []
        for actor_id, actor in list(self.registry.state['agents'].items()):
            if only is not None and actor_id not in only:
                continue
            policy = await self.registry.checked_policy(group, actor_id)
            if not policy['ready'] or 'write' not in policy['policy']['permissions']:
                continue
            only_for_actor = only[actor_id] if only is not None else None
            sqlite_trace_counts = await asyncio.to_thread(
                self._prefilter_sessions_sqlite,
                group['dataset_id'],
                actor['principal_id'],
                only_for_actor,
            )
            if sqlite_trace_counts is not None:
                selected = []
                for sid in sorted(sqlite_trace_counts):
                    trace_count = sqlite_trace_counts[sid]
                    cached = self._cold_session_cache.get((group['dataset_id'], actor['principal_id'], sid))
                    # Only skip when previously verified pending == 0 AND exact trace_count is unchanged.
                    # Both growth (> last) and expiry shrink (< last, which resets watermark) re-verify!
                    if cached is not None and cached == (trace_count, 0):
                        self._cold_session_cache.move_to_end((group['dataset_id'], actor['principal_id'], sid))
                        continue
                    selected.append(sid)
            else:
                selected = []
                seen_sids = set()
                for offset in range(0, _MAX_HTTP_SESSIONS_OFFSET + 1, 500):
                    page = await self.registry.official.request('GET', '/api/v1/sessions?range=all&limit=500&offset=' + str(offset), token_file=actor['key_file'])
                    rows = page.get('sessions')
                    if not isinstance(rows, list):
                        raise PolicyError('tool_graph_sources_unconfirmed', 503)
                    for row in rows:
                        if row.get('dataset_id') != group['dataset_id'] or row.get('user_id') != actor['principal_id']:
                            continue
                        sid = row.get('session_id')
                        if not sid or sid in seen_sids:
                            continue
                        if only_for_actor is not None and sid not in only_for_actor:
                            continue
                        seen_sids.add(sid)
                        selected.append(sid)
                    if not page.get('has_more'):
                        break
                    if not rows or offset == _MAX_HTTP_SESSIONS_OFFSET:
                        raise PolicyError('tool_graph_source_range_too_large', 409)

            for batch_start in range(0, len(selected), TOOL_GRAPH_PREVIEW_BATCH_SIZE):
                batch = selected[batch_start:batch_start + TOOL_GRAPH_PREVIEW_BATCH_SIZE]
                counts = await self.call('POST', '/preview', actor,
                    {'dataset_id': group['dataset_id'], 'session_ids': batch})
                limits_map = counts.get('limits') if isinstance(counts, dict) else None
                pending_map = counts.get('pending') if isinstance(counts, dict) else None
                if not isinstance(limits_map, dict) or not isinstance(pending_map, dict):
                    raise PolicyError('tool_graph_sources_unconfirmed', 503)
                for session in batch:
                    if session not in limits_map or session not in pending_map:
                        raise PolicyError('tool_graph_sources_unconfirmed', 503)
                    count, pending = limits_map[session], pending_map[session]
                    if type(count) is not int or type(pending) is not int or not 0 <= pending <= count:
                        raise PolicyError('tool_graph_sources_unconfirmed', 503)
                    self._set_cold_cache(group['dataset_id'], actor['principal_id'], session, count, pending)
                    if pending:
                        plan.append({'agent_id': actor_id, 'session_id': session, 'steps': count, 'pending_steps': pending})
        plan.sort(key=lambda row: (row['agent_id'], row['session_id']))
        snapshot = {'project_id': project, 'dataset_id': group['dataset_id'], 'revision': self.registry.state['revision'], 'plan': plan}
        preview = {**snapshot, 'session_count': len(plan), 'tool_steps': sum(row['pending_steps'] for row in plan),
                'review_token': hashlib.sha256(json.dumps(snapshot, sort_keys=True).encode()).hexdigest(),
                'settings': settings(self.registry.state, project), 'job': self.latest(project)}
        self.previews[preview['review_token']] = copy.deepcopy(preview)
        while len(self.previews) > 64:
            self.previews.pop(next(iter(self.previews)))
        return preview

    async def start(self, project, operation, review_token, *, plan=None, automatic=False):
        operation = str(uuid.UUID(operation))
        if operation in self.jobs:
            if self.jobs[operation]['project_id'] != project or self.jobs[operation]['review_token'] != review_token:
                raise PolicyError('tool_graph_operation_conflict', 409)
            return copy.deepcopy(self.jobs[operation])
        preview = plan or self.previews.get(review_token)
        if (preview is None or preview['project_id'] != project or preview['review_token'] != review_token
                or preview['revision'] != self.registry.state['revision']):
            raise PolicyError('tool_graph_scope_changed_review_again', 409)
        previous = self.latest(project)
        if previous and previous['status'] in ('queued', 'running', 'stopping', 'result_unknown'):
            raise PolicyError('tool_graph_existing_operation_requires_review', 409)
        children = []
        for actor_id in sorted({row['agent_id'] for row in preview['plan']}):
            await self.actor(project, actor_id)
            rows = [row for row in preview['plan'] if row['agent_id'] == actor_id]
            for batch_idx, start_pos in enumerate(range(0, len(rows), TOOL_GRAPH_BUILD_BATCH_SIZE)):
                chunk = rows[start_pos:start_pos + TOOL_GRAPH_BUILD_BATCH_SIZE]
                op_seed = actor_id if batch_idx == 0 else f'{actor_id}/batch/{batch_idx}'
                children.append({
                    'agent_id': actor_id,
                    'operation_id': str(uuid.uuid5(uuid.UUID(operation), op_seed)),
                    'session_ids': [row['session_id'] for row in chunk],
                    'limits': {row['session_id']: row['steps'] for row in chunk},
                    'status': 'queued',
                })
        row = {'operation_id': operation, 'project_id': project, 'dataset_id': preview['dataset_id'],
               'review_token': review_token,
               'status': 'queued', 'automatic': automatic, 'created_at': datetime.now(timezone.utc).isoformat(),
               'session_count': preview['session_count'], 'tool_steps': preview['tool_steps'], 'children': children}
        self.jobs[operation] = row
        self.save()
        self.tasks[operation] = asyncio.create_task(self.run(row))
        return copy.deepcopy(row)

    async def run(self, row):
        row['status'] = 'running'
        self.save()
        try:
            for child in row['children']:
                actor = await self.actor(row['project_id'], child['agent_id'])
                child['status'] = 'submitting'
                self.save()
                result = await self.call('POST', '', actor, {'operation_id': child['operation_id'],
                    'dataset_id': row['dataset_id'], 'session_ids': child['session_ids'], 'limits': child['limits']})
                while result['status'] in ('queued', 'running'):
                    child['status'] = result['status']
                    self.save()
                    await asyncio.sleep(2)
                    await self.actor(row['project_id'], child['agent_id'])
                    result = await self.call('GET', '/' + child['operation_id'], actor)
                child['status'] = result['status']
                self._invalidate_cold_cache(row['dataset_id'], actor['principal_id'], child['session_ids'])
                if result['status'] != 'completed':
                    row['status'] = result['status']
                    return
            row['status'] = 'completed'
        except asyncio.CancelledError:
            row['status'] = 'result_unknown'
        except Exception:
            row.update(status='result_unknown', error_code='tool_graph_submission_unconfirmed')
        finally:
            for child in row['children']:
                actor_cfg = self.registry.state['agents'].get(child['agent_id'])
                if actor_cfg and actor_cfg.get('principal_id'):
                    self._invalidate_cold_cache(row['dataset_id'], actor_cfg['principal_id'], child['session_ids'])
            self.save()

    async def review(self, project, operation):
        operation = str(uuid.UUID(operation))
        row = self.jobs.get(operation)
        if not row or row['project_id'] != project:
            raise PolicyError('tool_graph_job_not_found', 404)
        for child in row['children']:
            if child['status'] == 'queued':
                continue
            actor = await self.actor(project, child['agent_id'])
            result = await self.call('GET', '/' + child['operation_id'], actor)
            child['status'] = result['status']
        statuses = {child['status'] for child in row['children']}
        row['status'] = 'completed' if statuses <= {'completed'} else 'running' if statuses & {'running', 'queued', 'submitting'} else 'result_unknown'
        self.save()
        return copy.deepcopy(row)

    async def cancel(self, project, operation):
        operation = str(uuid.UUID(operation))
        row = self.jobs.get(operation)
        if not row or row['project_id'] != project:
            raise PolicyError('tool_graph_job_not_found', 404)
        task = self.tasks.get(operation)
        if task and not task.done():
            task.cancel()
            await task
        row['status'] = 'stopping'
        self.save()
        try:
            for child in row['children']:
                if child['status'] == 'queued':
                    child['status'] = 'cancelled'
                    continue
                actor = self.registry.state['agents'].get(child['agent_id'])
                if actor is None:
                    raise PolicyError('tool_graph_actor_unavailable', 409)
                result = await self.call('DELETE', '/' + child['operation_id'], actor)
                child['status'] = result['status']
            row['status'] = 'cancelled'
        except Exception:
            row['status'] = 'result_unknown'
        self.save()
        return copy.deepcopy(row)

    async def automatic_loop(self):
        while True:
            try:
                enabled = [project for project in self.registry.state['projects'] if settings(self.registry.state, project)['effective']]
                if enabled:
                    activity = await self.registry.official.request('GET', '/api/v1/activity/pipeline-runs?limit=500')
                    for item in reversed(activity):
                        if item.get('kind') != 'operation' or item.get('operation_name') != 'improve' or item.get('outcome') != 'succeeded':
                            continue
                        for project_id in enabled:
                            group = self.project(project_id)
                            policy = settings(self.registry.state, project_id)
                            if item.get('dataset_id') != group['dataset_id'] or not policy['changed_at']:
                                continue
                            at = datetime.fromisoformat(item['started_at'])
                            if at.tzinfo is None:
                                at = at.replace(tzinfo=timezone.utc)
                            if at < datetime.fromisoformat(policy['changed_at']):
                                continue
                            actor_id = next((key for key, actor in self.registry.state['agents'].items() if actor['principal_id'] == item.get('user_id')), None)
                            if actor_id is None or not item.get('session_ids'):
                                continue
                            access = await self.registry.checked_policy(group, actor_id)
                            if not access['ready'] or not access['policy'].get('can_auto_process'):
                                continue
                            operation = str(uuid.uuid5(uuid.UUID(self.registry.state['service_namespace']), 'tool-graph/' + item['id']))
                            if operation in self.jobs:
                                continue
                            plan = await self.preview(project_id, {actor_id: item['session_ids']})
                            await self.start(project_id, operation, plan['review_token'], plan=plan, automatic=True)
            except (PolicyError, KeyError, TypeError, ValueError):
                pass
            await asyncio.sleep(10)

    async def close(self):
        tasks = [task for task in [self.loop_task, *self.tasks.values()] if task and not task.done()]
        for task in tasks:
            task.cancel()
        if tasks:
            await asyncio.gather(*tasks, return_exceptions=True)


def attach_tool_graph(app, registry, runtime):
    control = ToolGraphControl(registry, runtime)
    async def endpoint(request):
        if not request['manager']:
            raise PolicyError('management_required', 403)
        project = request.match_info['id']
        if request.method == 'GET' and 'operation' not in request.match_info:
            return web.json_response(await control.preview(project))
        if request.method == 'GET':
            return web.json_response(await control.review(project, request.match_info['operation']))
        if request.method == 'DELETE':
            return web.json_response(await control.cancel(project, request.match_info['operation']))
        payload = await request.json()
        if set(payload) != {'operation_id', 'review_token'}:
            raise PolicyError('invalid_tool_graph_request')
        return web.json_response(await control.start(project, payload['operation_id'], payload['review_token']))
    app.add_routes([web.get('/v1/projects/{id}/tool-graph/preview', endpoint),
                    web.post('/v1/projects/{id}/tool-graph', endpoint),
                    web.get('/v1/projects/{id}/tool-graph/{operation}', endpoint),
                    web.delete('/v1/projects/{id}/tool-graph/{operation}', endpoint)])
    async def startup(_app):
        control.loop_task = asyncio.create_task(control.automatic_loop())
    async def cleanup(_app):
        await control.close()
    app.on_startup.append(startup)
    app.on_cleanup.append(cleanup)
    return control
