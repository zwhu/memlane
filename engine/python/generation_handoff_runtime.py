"""Single-service model handoff using Cognee's native task-local LLM config.

No content replay. Only marked improve tasks may be cancelled; unknown work
blocks application. Journals contain references/digests, never model credentials.
"""
import asyncio
import contextvars
import copy
import hmac
import hashlib
import json
import os
from pathlib import Path
import time
import uuid
from types import SimpleNamespace

from project_control import private_write, read_secret
from project_policy import PolicyError

scope = contextvars.ContextVar('memlane_generation_scope', default=None)


def improve_task(task):
    name = getattr(task.get_coro(), '__qualname__', '')
    return name == '_start_background_run.<locals>.run'


def task_operation(task):
    # Pinned SDK's launcher closes this exact root operation in its finally.
    # The task-local current operation may temporarily be a nested stage, whose
    # successful close cannot prove that the root cancellation was persisted.
    from cognee.modules.operations import OperationContext
    frame = task.get_coro().cr_frame
    operation = frame.f_locals.get('operation') if frame is not None else None
    return operation if isinstance(operation, OperationContext) and operation.operation_name == 'improve' else None


async def terminal_receipts(operations):
    # finish_operation intentionally swallows SQL failures. A completed Python
    # task alone is therefore not evidence that its terminal receipt persisted.
    from cognee.infrastructure.databases.relational import get_relational_engine
    from cognee.modules.pipelines.models import PipelineRun
    from sqlalchemy import select
    expected = {op.operation_id:op for op in operations}
    if not expected:
        return True
    async with get_relational_engine().get_async_session() as session:
        rows = (await session.execute(select(PipelineRun).where(
            PipelineRun.pipeline_run_id.in_(list(expected))))).scalars().all()
    return len(rows) == len(expected) and all(row.ended_at is not None
        and row.outcome == 'failed' and row.error_class == 'CancelledError'
        and row.user_id == expected[row.pipeline_run_id].user_id for row in rows)


class GenerationHandoffRuntime:
    def __init__(self, runtime, profile, environment, gate, *, factory=None, background=None, stop_timeout=30,
                 operation_for=task_operation, verify_terminal=terminal_receipts, initial_configuration=None):
        self.runtime, self.profile, self.environment, self.gate = runtime, profile, environment, gate
        self.path = Path(runtime['generation_file']).with_name('generation-handoff-runtime.json')
        if self.path.exists() and (self.path.is_symlink() or not self.path.is_file()
                or self.path.stat().st_mode & 0o077 or self.path.stat().st_size > 8 * 1024 * 1024):
            raise ValueError('private_generation_handoff_state_required')
        self.rows = json.loads(self.path.read_text()) if self.path.exists() else {}
        from process_groups import group_exists
        if any(group_exists(pid) for row in self.rows.values() for pid in row.get('bridge_process_groups', [])):
            raise PolicyError('generation_bridge_cleanup_unconfirmed', 503)
        self.active = {'reference': runtime.get('generation_reference'), 'digest': environment.get('_digest')}
        self.config = initial_configuration
        self.factory, self.background = factory, background
        self.stop_timeout = stop_timeout
        self.operation_for, self.verify_terminal = operation_for, verify_terminal
        self.requests = set()
        self.switching = False
        self.bridge = None
        self.task = None
        for row in self.rows.values():
            if row['status'] in ('preparing', 'stopping', 'applying'):
                row.update(status='unknown', reason='service_interrupted')
        self.save()

    def save(self):
        private_write(self.path, json.dumps(self.rows, sort_keys=True).encode())

    def tasks(self):
        if self.background:
            return set(self.background())
        from cognee.infrastructure.background_tasks import _pending_on_current_loop
        return _pending_on_current_loop()

    def get(self, operation):
        return copy.deepcopy(self.rows.get(operation, {'status': 'not_found'}))

    async def inspect(self, operation):
        row = self.rows.get(operation)
        if row and row['status'] in ('blocked','unknown') and row.get('previous') == self.active:
            operations = [SimpleNamespace(operation_id=uuid.UUID(value['id']),
                user_id=uuid.UUID(value['user_id']) if value['user_id'] else None)
                for value in row.get('cancelled_operations', [])]
            if (not self.tasks() and not self.requests and not self.gate.active
                    and 'cancelled_operations' in row and await self.verify_terminal(operations)):
                # Read-only reconciliation: no cancellation, model call, profile
                # application or content submission occurs in this GET path.
                row.update(status='not_applied')
                self.save()
        return self.get(operation)

    async def apply(self, operation, reference, digest, profile, environment, source_credentials=()):
        prior = self.rows.get(operation)
        if prior:
            if prior['reference'] != reference or prior['digest'] != digest:
                raise PolicyError('operation_id_conflict', 409)
            return self.get(operation)
        if self.switching:
            raise PolicyError('generation_handoff_busy', 409)
        self.switching = True
        row = self.rows[operation] = {'status': 'preparing', 'reference': reference, 'digest': digest,
                                      'previous':copy.deepcopy(self.active),
                                      'cancelled_operations':[], 'cancelled_sources':[],
                                      'cancelled': 0, 'reason': '', 'started_at': time.time()}
        self.save()
        new_bridge = None
        try:
            if self.factory:
                candidate, new_bridge = await self.factory(profile, environment)
            else:
                from generation_config import native_llm_configuration
                if profile['mode'] == 'agent_bridge':
                    from generation_bridge import ValidationBridge
                    bridge_owner = {}
                    def bridge_changed():
                        row['bridge_process_groups'] = sorted(bridge_owner['bridge'].processes)
                        self.save()
                    new_bridge = ValidationBridge(self.runtime, profile,
                        self.path.parent / 'generation-handoff-bridges' / operation, bridge_changed,
                        concurrency=profile.get('max_concurrent_requests', 2))
                    bridge_owner['bridge'] = new_bridge
                    new_bridge.root.mkdir(parents=True, mode=0o700, exist_ok=True)
                    environment = await new_bridge.start()
                elif profile.get('provider') == 'ollama':
                    import aiohttp
                    try:
                        async with aiohttp.ClientSession() as client:
                            async with client.post(profile['endpoint'].rstrip('/') + '/api/show',
                                    json={'model':profile['model']}, allow_redirects=False,
                                    timeout=aiohttp.ClientTimeout(total=5)) as response:
                                if response.status != 200:
                                    raise PolicyError('generation_provider_unavailable', 409)
                    except (aiohttp.ClientError, asyncio.TimeoutError):
                        raise PolicyError('generation_provider_unavailable', 409) from None
                candidate = native_llm_configuration(environment)
                if profile.get('provider') == 'ollama':
                    from generation_ollama import register_verified_schema
                    register_verified_schema(json.loads(environment['MEMLANE_OLLAMA_SCHEMA_EVIDENCE']),
                                             candidate.llm_model, candidate.llm_endpoint)
                if profile['mode'] == 'agent_bridge':
                    import litellm
                    litellm.register_model({candidate.llm_model: {'supports_response_schema': True,
                        'supports_function_calling': False, 'litellm_provider': 'openai', 'mode': 'chat'}})
            row['status'] = 'stopping'; self.save()
            tasks = {t for t in self.tasks() if not t.done()}
            operations = {t:self.operation_for(t) for t in tasks if improve_task(t)}
            cancellable = {t for t in tasks if improve_task(t)
                and (t.get_context().get(scope) or {}).get('sessions')
                and (t.get_context().get(scope) or {}).get('credential_sha256') in source_credentials
                and operations.get(t) is not None}
            row['cancelled_operations'] = [{'id':str(operations[t].operation_id),
                'user_id':str(operations[t].user_id) if operations[t].user_id else None} for t in cancellable]
            row['cancelled_sources'] = [{'session_id':session,
                'credential_sha256':task.get_context().get(scope)['credential_sha256']}
                for task in cancellable for session in task.get_context().get(scope)['sessions']]
            self.save()
            for task in cancellable:
                task.cancel()
            # asyncio.wait does not cancel tasks whose cleanup is still writing.
            if cancellable:
                _, pending = await asyncio.wait(cancellable, timeout=self.stop_timeout)
                if pending:
                    raise PolicyError('generation_stop_unconfirmed', 409)
            row['cancelled'] = len(cancellable)
            row['cancelled_sessions'] = sorted({session for task in cancellable
                for session in (task.get_context().get(scope) or {}).get('sessions', [])})
            row['cancelled_sources'] = [{'session_id':session,
                'credential_sha256':task.get_context().get(scope)['credential_sha256']}
                for task in cancellable for session in task.get_context().get(scope)['sessions']]
            # Finalizers can register further SQL writes. Do not assume root
            # cancellation means every child/HTTP/model process has exited.
            deadline = time.monotonic() + self.stop_timeout
            while self.tasks() or self.requests or self.gate.active:
                if time.monotonic() >= deadline:
                    raise PolicyError('generation_stop_unconfirmed', 409)
                await asyncio.sleep(.05)
            if not await self.verify_terminal([operations[t] for t in cancellable]):
                raise PolicyError('generation_stop_unconfirmed', 409)
            if self.bridge:
                await self.bridge.close()
            elif self.profile['mode'] == 'agent_bridge':
                import aiohttp
                async with aiohttp.ClientSession() as client:
                    async with client.get(self.runtime['completion_url'].rstrip('/') + '/health',
                                          timeout=aiohttp.ClientTimeout(total=5)) as response:
                        if response.status != 200 or (await response.json()).get('active_or_waiting') != 0:
                            raise PolicyError('generation_stop_unconfirmed', 409)
            row['status'] = 'applying'; self.save()
            if profile.get('provider') == 'ollama':
                os.environ['OLLAMA_API_BASE'] = environment['LLM_ENDPOINT']
            self.gate.reconfigure(environment['LLM_ENDPOINT'], profile.get('max_concurrent_requests', 2),
                                  profile.get('bridge_kind') or profile.get('provider'))
            self.config, self.bridge = candidate, new_bridge
            self.profile, self.environment = copy.deepcopy(profile), environment
            self.active = {'reference': copy.deepcopy(reference), 'digest': digest}
            new_bridge = None
            row.update(status='applied', ended_at=time.time())
            self.save()
            return self.get(operation)
        except BaseException as error:
            row.update(status='unknown' if isinstance(error, (asyncio.CancelledError, TimeoutError)) else 'blocked',
                       reason=error.code if isinstance(error, PolicyError) else 'generation_handoff_unconfirmed')
            self.save()
            if new_bridge:
                await new_bridge.close()
            return self.get(operation)
        finally:
            self.switching = False

    async def close(self):
        if self.task and not self.task.done():
            self.task.cancel()
            await asyncio.gather(self.task, return_exceptions=True)
        if self.bridge:
            await self.bridge.close()


def install_generation_handoff(app, runtime, profile, environment, gate):
    from fastapi import Depends, HTTPException, Request
    from pydantic import BaseModel
    from generation_config import native_llm_configuration
    controller = GenerationHandoffRuntime(runtime, profile, environment, gate,
        initial_configuration=native_llm_configuration(environment))
    secret = read_secret(runtime['project_control']['management_token_file'])

    async def manager(request: Request):
        if not hmac.compare_digest(request.headers.get('x-memlane-management', ''), secret):
            raise HTTPException(403, 'management_required')

    class Selection(BaseModel):
        reference: dict
        digest: str
        profile: dict
        environment: dict
        source_credentials: list[str] = []

    @app.middleware('http')
    async def generation_context(request, call_next):
        # Raw save/receipt reads do not depend on a model and remain available.
        content = request.url.path.startswith('/api/v1/') and not request.url.path.startswith('/api/v1/memlane/generation/')
        if not content or '/capture/' in request.url.path:
            return await call_next(request)
        if controller.switching and request.method not in ('GET', 'HEAD'):
            from starlette.responses import JSONResponse
            return JSONResponse({'detail':'generation_handoff_in_progress'}, status_code=409)
        from cognee.context_global_variables import llm_config
        token = llm_config.set(controller.config) if controller.config is not None else None
        sessions = []
        if request.url.path.rstrip('/').endswith('/improve') and request.method == 'POST':
            try:
                value = await request.json()
                sessions = value.get('sessionIds', [])
                if not isinstance(sessions, list) or not all(isinstance(s, str) for s in sessions):
                    sessions = []
            except ValueError:
                pass
        credential = request.headers.get('x-api-key', '')
        marker = scope.set({'digest': controller.active['digest'] or 'startup', 'sessions': sessions,
            'credential_sha256': hashlib.sha256(credential.encode()).hexdigest() if credential else ''})
        task = asyncio.current_task(); controller.requests.add(task)
        try:
            return await call_next(request)
        finally:
            controller.requests.discard(task)
            scope.reset(marker)
            if token is not None:
                llm_config.reset(token)

    @app.put('/api/v1/memlane/generation/handoffs/{operation}', dependencies=[Depends(manager)])
    async def select(operation: str, payload: Selection):
        from generation_config import IDENTIFIER, validate_profile
        if not IDENTIFIER.fullmatch(operation):
            raise HTTPException(400, 'invalid_operation_id')
        try:
            chosen = validate_profile(payload.profile)
            old = controller.rows.get(operation)
            if old:
                if old['reference'] != payload.reference or old['digest'] != payload.digest:
                    raise PolicyError('operation_id_conflict', 409)
                return controller.get(operation)
            if controller.switching or (controller.task and not controller.task.done()):
                raise PolicyError('generation_handoff_busy', 409)
            controller.task = asyncio.create_task(controller.apply(operation, payload.reference,
                payload.digest, chosen, payload.environment, payload.source_credentials))
            await asyncio.sleep(0)
            return controller.get(operation)
        except PolicyError as error:
            raise HTTPException(error.status, error.code) from None

    @app.get('/api/v1/memlane/generation/handoffs/{operation}', dependencies=[Depends(manager)])
    async def receipt(operation: str):
        return await controller.inspect(operation)

    @app.get('/api/v1/memlane/generation/status', dependencies=[Depends(manager)])
    async def status():
        running = {session for task in controller.tasks() if improve_task(task)
            for session in (task.get_context().get(scope) or {}).get('sessions', [])}
        return {'running_sessions': len(running), 'active': controller.active}
    return controller
