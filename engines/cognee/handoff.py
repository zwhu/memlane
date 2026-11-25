"""Switch models in memory after the pinned SDK confirms old work is drained."""
import asyncio
import contextvars
import copy
import json
import os
import re
from pathlib import Path
import sys
import time


from . import PolicyError
from .capacity import EndpointCapacity
from .language_egress import DEFAULT_LANGUAGE, set_default_language
from .llm_config import compute_model_digest, environment_from_generation_config, native_llm_configuration
from .ollama_schema import evidence_matches, register_verified_schema

scope = contextvars.ContextVar('memlane_generation_scope', default=None)


def _clear_cognify_config_cache():
    module = sys.modules.get('cognee.modules.cognify.config')
    if module is None:
        return
    get_cognify_config = getattr(module, 'get_cognify_config', None)
    clear = getattr(get_cognify_config, 'cache_clear', None)
    if callable(clear):
        clear()


def _local_litellm_baseline():
    import litellm
    backup = Path(litellm.__file__).with_name('model_prices_and_context_window_backup.json')
    if backup.is_file():
        try:
            data = json.loads(backup.read_text(encoding='utf-8'))
            if isinstance(data, dict):
                return data
        except (OSError, ValueError):
            pass
    return {}


def _register_bridge_model(model):
    import litellm
    bare = model.split('/', 1)[1] if model.startswith('openai/') else model
    aliases = {model, bare, 'openai/' + bare}
    previous = {key: copy.deepcopy(litellm.model_cost[key]) for key in aliases if key in litellm.model_cost}
    def restore():
        for key in aliases:
            if key in previous:
                litellm.model_cost[key] = copy.deepcopy(previous[key])
            else:
                litellm.model_cost.pop(key, None)
        from litellm.utils import _invalidate_model_cost_lowercase_map
        _invalidate_model_cost_lowercase_map()

    try:
        litellm.register_model({model: {'supports_response_schema': True, 'supports_function_calling': False,
            'litellm_provider': 'openai', 'mode': 'chat'}}, persist_across_reloads=False)
    except BaseException:
        restore()
        raise
    return restore


def _initial_model_restore(profile, environment):
    mode = (profile or {}).get('mode')
    provider = (profile or {}).get('provider')
    model = (environment or {}).get('LLM_MODEL')
    if not isinstance(model, str) or not model:
        return None
    if mode == 'agent_bridge':
        bare = model.split('/', 1)[1] if model.startswith('openai/') else model
        aliases = {model, bare, 'openai/' + bare}
    elif provider == 'ollama':
        bare = model.split('/', 1)[1] if model.startswith(('ollama/', 'ollama_chat/')) else model
        aliases = {model, bare, 'ollama/' + bare, 'ollama_chat/' + bare}
    else:
        return None

    def restore():
        try:
            import litellm
        except ImportError:
            return
        baseline = _local_litellm_baseline()
        for key in aliases:
            if key in baseline:
                litellm.model_cost[key] = copy.deepcopy(baseline[key])
            else:
                litellm.model_cost.pop(key, None)
        from litellm.utils import _invalidate_model_cost_lowercase_map
        _invalidate_model_cost_lowercase_map()

    return restore


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
    if not operations:
        return True
    from cognee.infrastructure.databases.relational import get_relational_engine
    from cognee.modules.pipelines.models import PipelineRun
    from sqlalchemy import select
    expected = {op.operation_id: op for op in operations}
    async with get_relational_engine().get_async_session() as session:
        rows = (await session.execute(select(PipelineRun).where(
            PipelineRun.pipeline_run_id.in_(list(expected))))).scalars().all()
    return len(rows) == len(expected) and all(row.ended_at is not None
        and row.outcome == 'failed' and row.error_class == 'CancelledError'
        and row.user_id == expected[row.pipeline_run_id].user_id for row in rows)


_ENV_KEYS = ('OLLAMA_API_BASE', 'CHUNKS_PER_BATCH')


def _sync_environment(values):
    for key in _ENV_KEYS:
        value = values.get(key)
        if value is None:
            os.environ.pop(key, None)
        else:
            os.environ[key] = str(value)
    _clear_cognify_config_cache()


def _register_model(profile, env):
    if env.get('LLM_PROVIDER') == 'ollama':
        evidence = env.get('MEMLANE_OLLAMA_SCHEMA_EVIDENCE')
        if isinstance(evidence, str):
            evidence = json.loads(evidence)
        return register_verified_schema(evidence, env['LLM_MODEL'], env['LLM_ENDPOINT'])
    if profile.get('mode') == 'agent_bridge':
        return _register_bridge_model(env['LLM_MODEL'])
    return None


class ModelHandoffController:
    def __init__(self, *, gate=None, factory=native_llm_configuration, background=None, stop_timeout=30,
                 operation_for=task_operation, verify_terminal=terminal_receipts):
        self.mode, self.active_digest, self.output_language = 'setup_ready', '', DEFAULT_LANGUAGE
        self.config, self.profile, self.environment = None, {}, {}
        self.gate, self.factory, self.background = gate, factory, background
        self.stop_timeout, self.operation_for, self.verify_terminal = stop_timeout, operation_for, verify_terminal
        self.switching, self.requests, self._model_restore = False, set(), None
        self.operations = {}
        self._cancelled = {}

    def tasks(self):
        if self.background is not None:
            return {task for task in self.background() if not task.done()}
        from cognee.infrastructure.background_tasks import _pending_on_current_loop
        return _pending_on_current_loop()

    def verify_expected_digest(self, expected_model_digest):
        if not expected_model_digest or expected_model_digest != (self.active_digest or ''):
            raise PolicyError('model_digest_mismatch', 409)

    async def _drain(self):
        tasks = self.tasks()
        for task in tasks:
            if improve_task(task):
                operation = self.operation_for(task)
                if operation is not None and operation.operation_id not in self._cancelled:
                    self._cancelled[operation.operation_id] = operation
                    task.cancel()
        deadline = time.monotonic() + self.stop_timeout
        loop = asyncio.get_running_loop()
        while time.monotonic() < deadline:
            if not self.tasks() and not self.requests and not (self.gate and self.gate.active):
                try:
                    async with asyncio.timeout_at(loop.time() + max(0, deadline - time.monotonic())):
                        confirmed = await self.verify_terminal(list(self._cancelled.values()))
                except TimeoutError:
                    confirmed = False
                if confirmed:
                    self._cancelled.clear()
                    return
                break
            await asyncio.sleep(min(.05, max(0, deadline - time.monotonic())))
        raise PolicyError('generation_stop_unconfirmed', 409)

    def _swap(self, profile, env, limit, kind):
        previous_env = {key: os.environ.get(key) for key in _ENV_KEYS}
        previous_gate = ((self.gate.origin, self.gate.limit, self.gate.backend_kind) if self.gate else None)
        next_restore = None
        try:
            if self._model_restore is not None:
                self._model_restore()
            next_restore = _register_model(profile, env)
            _sync_environment({'OLLAMA_API_BASE': env['LLM_ENDPOINT'] if env['LLM_PROVIDER'] == 'ollama' else None,
                               'CHUNKS_PER_BATCH': env.get('CHUNKS_PER_BATCH')})
            if self.gate:
                self.gate.reconfigure(env['LLM_ENDPOINT'], limit, kind)
        except BaseException:
            try:
                if next_restore is not None:
                    next_restore()
            finally:
                try:
                    self._model_restore = _register_model(self.profile, self.environment)
                finally:
                    try:
                        _sync_environment(previous_env)
                    finally:
                        if self.gate and previous_gate != (self.gate.origin, self.gate.limit, self.gate.backend_kind):
                            self.gate.reconfigure(*previous_gate)
            raise
        self._model_restore = next_restore

    async def apply(self, operation_id, generation_config, *, output_language=None):
        if not isinstance(operation_id, str) or not re.fullmatch(r'[a-zA-Z0-9_-]{1,64}', operation_id):
            raise PolicyError('invalid_operation_id', 422)
        try:
            profile = copy.deepcopy(generation_config)
            digest = compute_model_digest(profile)
            fingerprint = compute_model_digest({'generation_config': profile, 'output_language': output_language})
        except (TypeError, ValueError):
            raise PolicyError('invalid_generation_config', 422) from None
        previous = self.operations.get(operation_id)
        if previous is not None:
            if previous[0] != fingerprint:
                raise PolicyError('operation_id_conflict', 409)
            if previous[1] is not None:
                return dict(previous[1])
        if self.switching:
            raise PolicyError('generation_handoff_busy', 409)
        self.switching = True
        self.operations[operation_id] = (fingerprint, None)
        try:
            if not isinstance(profile, dict):
                raise PolicyError('invalid_generation_config', 422)
            try:
                env = environment_from_generation_config(profile)
                if not all(isinstance(env.get(key), str) and env[key].strip()
                           for key in ('LLM_PROVIDER', 'LLM_MODEL', 'LLM_ENDPOINT')):
                    raise ValueError('incomplete LLM configuration')
                limit = profile.get('max_concurrent_requests', 2)
                kind = profile.get('bridge_kind') or env['LLM_PROVIDER']
                capacity = EndpointCapacity(env['LLM_ENDPOINT'], limit, backend_kind=kind)
                if capacity.origin.scheme not in ('http', 'https') or not capacity.origin.host:
                    raise ValueError('invalid LLM endpoint')
                candidate = self.factory(env)
                if env['LLM_PROVIDER'] == 'ollama':
                    evidence = env.get('MEMLANE_OLLAMA_SCHEMA_EVIDENCE')
                    if isinstance(evidence, str):
                        evidence = json.loads(evidence)
                    if not evidence_matches(evidence, env['LLM_MODEL'], env['LLM_ENDPOINT']):
                        raise PolicyError('ollama_native_schema_unconfirmed', 422)
            except (TypeError, ValueError):
                raise PolicyError('invalid_generation_config', 422) from None
            await self._drain()
            self._swap(profile, env, limit, kind)
            self.config, self.profile, self.environment, self.active_digest = candidate, profile, env, digest
            if output_language is not None:
                self.output_language = output_language if output_language else DEFAULT_LANGUAGE
            set_default_language(self.output_language)
            self.mode = 'ready'
            result = {'ok': True, 'applied': True, 'model_digest': digest, 'operation_id': operation_id}
            self.operations[operation_id] = (fingerprint, result)
            return dict(result)
        finally:
            self.switching = False

    def status(self):
        return {'ok': True, 'mode': self.mode, 'model_digest': self.active_digest,
                'output_language': self.output_language, 'active_tasks': len(self.tasks()) + len(self.requests)}

    def install_context_middleware(self, app, *, config_context=None):
        if config_context is None:
            from cognee.context_global_variables import llm_config
            config_context = llm_config
        app.add_middleware(_ModelContextMiddleware, controller=self, config_context=config_context)

    async def close(self):
        if self._model_restore is not None:
            self._model_restore()
            self._model_restore = None


class _ModelContextMiddleware:
    def __init__(self, app, controller, config_context):
        self.app, self.controller, self.config_context = app, controller, config_context

    async def __call__(self, request, receive, send):
        if request['type'] not in ('http', 'websocket'):
            await self.app(request, receive, send)
            return
        controller = self.controller
        path, method = request['path'], request.get('method')
        if (method, path) in {('POST', '/internal/v1/model/apply'), ('GET', '/internal/v1/model/status'),
                              ('GET', '/internal/v1/health')}:
            await self.app(request, receive, send)
            return
        if controller.switching:
            if request['type'] == 'websocket':
                await send({'type': 'websocket.close', 'code': 1008})
            else:
                from starlette.responses import JSONResponse
                await JSONResponse({'detail': 'generation_handoff_in_progress'}, status_code=409)(request, receive, send)
            return
        ticket = object()
        controller.requests.add(ticket)
        token = self.config_context.set(controller.config)
        marker = scope.set(path)
        try:
            await self.app(request, receive, send)
        finally:
            scope.reset(marker)
            self.config_context.reset(token)
            controller.requests.discard(ticket)
