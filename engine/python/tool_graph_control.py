"""Project-scoped UI/job coordination around the official SDK adapter."""
import asyncio
import copy
from datetime import datetime, timezone
import hashlib
import json
from pathlib import Path
import uuid

from aiohttp import web
from _runtime_common import private_write, read_secret
from project_policy import PolicyError
from tool_graph_policy import settings


class ToolGraphControl:
    def __init__(self, registry, runtime):
        self.registry, self.runtime = registry, runtime
        self.path = Path(runtime['project_control']['registry_file']).parent / 'tool-graph-control-jobs.json'
        self.jobs = json.loads(self.path.read_text()) if self.path.exists() else {}
        self.tasks = {}
        self.previews = {}
        self.loop_task = None
        for row in self.jobs.values():
            if row['status'] in ('queued', 'running', 'stopping'):
                row['status'] = 'result_unknown'
        self.save()

    def save(self):
        private_write(self.path, json.dumps(self.jobs, sort_keys=True).encode())

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
            selected = []
            for offset in range(0, 10001, 500):
                page = await self.registry.official.request('GET', '/api/v1/sessions?range=all&limit=500&offset=' + str(offset), token_file=actor['key_file'])
                rows = page.get('sessions')
                if not isinstance(rows, list):
                    raise PolicyError('tool_graph_sources_unconfirmed', 503)
                for row in rows:
                    if row.get('dataset_id') != group['dataset_id'] or row.get('user_id') != actor['principal_id']:
                        continue
                    if only is not None and row['session_id'] not in only[actor_id]:
                        continue
                    selected.append(row['session_id'])
                if not page.get('has_more'):
                    break
                if not rows or offset == 10000:
                    raise PolicyError('tool_graph_source_range_too_large', 409)
            if len(selected) > 128:
                raise PolicyError('tool_graph_source_range_too_large', 409)
            if selected:
                counts = await self.call('POST', '/preview', actor,
                    {'dataset_id': group['dataset_id'], 'session_ids': selected})
                for session in selected:
                    count, pending = counts['limits'][session], counts['pending'][session]
                    if type(count) is not int or type(pending) is not int or not 0 <= pending <= count:
                        raise PolicyError('tool_graph_sources_unconfirmed', 503)
                    if pending:
                        plan.append({'agent_id': actor_id, 'session_id': session, 'steps': count, 'pending_steps': pending})
        if len(plan) > 128:
            raise PolicyError('tool_graph_source_range_too_large', 409)
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
            children.append({'agent_id': actor_id, 'operation_id': str(uuid.uuid5(uuid.UUID(operation), actor_id)),
                             'session_ids': [row['session_id'] for row in rows],
                             'limits': {row['session_id']: row['steps'] for row in rows}, 'status': 'queued'})
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
                if result['status'] != 'completed':
                    row['status'] = result['status']
                    return
            row['status'] = 'completed'
        except asyncio.CancelledError:
            row['status'] = 'result_unknown'
        except Exception:
            row.update(status='result_unknown', error_code='tool_graph_submission_unconfirmed')
        finally:
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
