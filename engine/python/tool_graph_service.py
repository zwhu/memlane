"""Thin, authenticated SDK adapter for the official tool-trace graph pipeline.

The pinned HTTP improve API cannot scope this optional stage per project.
No extraction, chunking, vector storage, graph algorithm or watermark is
implemented here. Only bounded job identities and outcomes are retained.
"""
import asyncio
from contextlib import asynccontextmanager
from datetime import datetime, timezone
import hashlib
import hmac
import json
from pathlib import Path
import uuid

from performance_trace import RequestTrace
from _runtime_common import private_write, read_secret
from project_policy import PolicyError


def now():
    return datetime.now(timezone.utc).isoformat()


class ToolGraphJobs:
    def __init__(self, path, build, authorize):
        self.path, self.build, self.authorize = Path(path), build, authorize
        self.tasks = {}
        self.rows = {}
        if self.path.exists():
            if self.path.is_symlink() or self.path.stat().st_mode & 0o077:
                raise ValueError('private_tool_graph_jobs_required')
            payload = json.loads(self.path.read_text())
            if payload.get('schema_version') != 1:
                raise ValueError('unsupported_tool_graph_jobs')
            self.rows = payload['jobs']
            for row in self.rows.values():
                if row['status'] in ('queued', 'running'):
                    row.update(status='result_unknown', error_code='tool_graph_owner_restarted')
            self.save()

    def save(self):
        private_write(self.path, json.dumps({'schema_version': 1, 'jobs': self.rows}, sort_keys=True).encode())

    async def start(self, user, dataset, sessions, operation, limits=None):
        await self.authorize(user, dataset)
        uid, dataset, operation = str(user.id), str(uuid.UUID(str(dataset))), str(uuid.UUID(str(operation)))
        sessions = sorted(set(sessions))
        if not sessions or len(sessions) > 128 or any(not isinstance(s, str) or not s or len(s) > 256 for s in sessions):
            raise PolicyError('invalid_tool_graph_sessions')
        if limits is not None and (set(limits) != set(sessions) or any(type(n) is not int or n < 0 for n in limits.values())):
            raise PolicyError('invalid_tool_graph_snapshot')
        signature = hashlib.sha256(json.dumps([uid, dataset, sessions, limits], sort_keys=True).encode()).hexdigest()
        if operation in self.rows:
            row = self.get(user, operation)
            if row['signature'] != signature:
                raise PolicyError('tool_graph_operation_conflict', 409)
            return row
        if any(row['dataset_id'] == dataset and row['status'] in ('queued', 'running', 'result_unknown') for row in self.rows.values()):
            raise PolicyError('tool_graph_existing_operation_requires_review', 409)
        row = {'operation_id': operation, 'user_id': uid, 'dataset_id': dataset, 'session_ids': sessions,
               'signature': signature, 'limits': limits, 'status': 'queued', 'created_at': now()}
        self.rows[operation] = row
        self.save()  # Unknown writes are never repeated under a new operation.
        self.tasks[operation] = asyncio.create_task(self.run(user, row))
        self.tasks[operation].add_done_callback(lambda _task: self.tasks.pop(operation, None))
        return dict(row)

    def get(self, user, operation):
        row = self.rows.get(str(operation))
        if row is None or row['user_id'] != str(user.id):
            raise PolicyError('tool_graph_operation_not_found', 404)
        return dict(row)

    async def run(self, user, row):
        trace = RequestTrace('tool_graph')
        trace.transition('official_pipeline')
        row.update(status='running', started_at=now())
        self.save()
        try:
            result = await self.build(user, row['dataset_id'], row['session_ids'], row.get('limits'))
            row.update(status='completed', result=result)
        except asyncio.CancelledError:
            row.update(status='cancelled', error_code='tool_graph_cancelled')
        except Exception as error:
            trace.emit("pipeline_failed", error_class=type(error).__name__)
            # A failed build can have written partial data. Its original
            # operation stays reviewable; this adapter does not retry it.
            row.update(status='result_unknown', error_code='tool_graph_build_unconfirmed')
        finally:
            row['ended_at'] = now()
            self.save()
            trace.finish(row['status'])

    async def cancel(self, user, operation):
        self.get(user, operation)
        task = self.tasks.get(str(operation))
        if task is not None and not task.done():
            task.cancel()
            try:
                await task
            except asyncio.CancelledError:
                self.rows[str(operation)].update(status='cancelled', error_code='tool_graph_cancelled', ended_at=now())
                self.save()
        return self.get(user, operation)

    async def close(self):
        tasks = [task for task in self.tasks.values() if not task.done()]
        for task in tasks:
            task.cancel()
        if tasks:
            await asyncio.gather(*tasks, return_exceptions=True)
        for row in self.rows.values():
            if row['status'] in ('queued', 'running'):
                row.update(status='result_unknown', error_code='tool_graph_owner_stopped', ended_at=now())
        self.save()


async def official_authorize(user, dataset):
    from cognee.modules.data.methods import get_authorized_existing_datasets
    result = await get_authorized_existing_datasets(user=user, datasets=[dataset], permission_type='write')
    if not result:
        raise PolicyError('tool_graph_write_required', 403)


async def official_preview(user, sessions):
    from cognee.infrastructure.session.get_session_manager import get_session_manager
    from cognee.infrastructure.session.session_persist_watermark import TRACE_PERSIST_WATERMARK
    manager = get_session_manager()
    if not manager.is_available:
        raise PolicyError('tool_graph_source_unavailable', 503)
    limits, pending = {}, {}
    for session in sessions:
        total = await manager.get_agent_trace_count(user_id=str(user.id), session_id=session)
        stored = await TRACE_PERSIST_WATERMARK.read_count(manager, str(user.id), session)
        if stored > total:
            stored = 0
        limits[session], pending[session] = total, max(0, total - stored)
    return {'limits': limits, 'pending': pending}


async def official_build(user, dataset, sessions, limits=None):
    from cognee.infrastructure.session.get_session_manager import get_session_manager
    from cognee.infrastructure.session.session_persist_watermark import TRACE_PERSIST_WATERMARK
    from cognee.memify_pipelines.persist_agent_trace_feedbacks_in_knowledge_graph import persist_agent_trace_feedbacks_in_knowledge_graph_pipeline
    from cognee.modules.pipelines.models import PipelineRunErrored
    manager = get_session_manager()
    upper, pending = {}, 0
    for session in sessions:
        total = await manager.get_agent_trace_count(user_id=str(user.id), session_id=session)
        limit = limits[session] if limits is not None else total
        if limit > total:
            raise PolicyError('tool_graph_source_changed', 409)
        stored = await TRACE_PERSIST_WATERMARK.read_count(manager, str(user.id), session)
        if stored > total:
            stored = 0  # Same stale-count rule as the official persister.
        if limit > stored:
            upper[session] = limit
            pending += limit - stored
    if not upper:
        return {'new_steps': 0, 'pipeline_run_ids': []}
    result = await persist_agent_trace_feedbacks_in_knowledge_graph_pipeline(
        user=user, dataset=uuid.UUID(str(dataset)), session_ids=list(upper), persist_up_to=upper, run_in_background=False)
    runs = list(result.values()) if isinstance(result, dict) else [result]
    if any(isinstance(run, PipelineRunErrored) for run in runs):
        raise PolicyError('official_tool_graph_failed', 503)
    return {'new_steps': pending, 'pipeline_run_ids': [str(run.pipeline_run_id) for run in runs if hasattr(run, 'pipeline_run_id')]}


def install_tool_graph_routes(app, config):
    from fastapi import APIRouter, Depends, HTTPException, Request
    from pydantic import BaseModel, ConfigDict, Field
    from cognee.modules.users.methods import get_authenticated_user
    jobs = ToolGraphJobs(Path(config['project_control']['registry_file']).parent / 'tool-graph-service-jobs.json',
                         official_build, official_authorize)
    secret = read_secret(config['project_control']['management_token_file'])

    @asynccontextmanager
    async def lifespan(_app):
        try:
            yield
        finally:
            await jobs.close()

    router = APIRouter(prefix='/api/v1/memlane/tool-graphs', lifespan=lifespan)

    class Preview(BaseModel):
        model_config = ConfigDict(extra='forbid')
        dataset_id: uuid.UUID
        session_ids: list[str] = Field(min_length=1, max_length=128)

    class Build(Preview):
        operation_id: uuid.UUID
        limits: dict[str, int] | None = None

    async def manager(request: Request):
        if not hmac.compare_digest(request.headers.get('x-memlane-management', ''), secret):
            raise HTTPException(403, 'management_required')

    async def respond(call):
        try:
            return await call
        except PolicyError as error:
            raise HTTPException(error.status, error.code) from None

    @router.post('', dependencies=[Depends(manager)])
    async def start(payload: Build, user=Depends(get_authenticated_user)):
        return await respond(jobs.start(user, payload.dataset_id, payload.session_ids, payload.operation_id, payload.limits))

    @router.post('/preview', dependencies=[Depends(manager)])
    async def preview(payload: Preview, user=Depends(get_authenticated_user)):
        async def inspect():
            await jobs.authorize(user, payload.dataset_id)
            return await official_preview(user, payload.session_ids)
        return await respond(inspect())

    @router.get('/capacity', dependencies=[Depends(manager)])
    async def capacity(user=Depends(get_authenticated_user)):
        # Public registry covers queued/background improve between pipeline
        # events too. Only service-wide counts leave this management boundary.
        from cognee.infrastructure.background_tasks import pending_background_tasks
        return {'pending_background_tasks': pending_background_tasks(),
                'tool_graph_jobs_running': sum(not task.done() for task in jobs.tasks.values())}

    @router.get('/{operation}', dependencies=[Depends(manager)])
    async def get(operation: uuid.UUID, user=Depends(get_authenticated_user)):
        try:
            return jobs.get(user, operation)
        except PolicyError as error:
            raise HTTPException(error.status, error.code) from None

    @router.delete('/{operation}', dependencies=[Depends(manager)])
    async def cancel(operation: uuid.UUID, user=Depends(get_authenticated_user)):
        return await respond(jobs.cancel(user, operation))

    app.include_router(router)
    return jobs
