"""Isolated synthetic probe against the pinned official native LLM adapter."""
import asyncio
import contextlib
import io
import json
import os
from pathlib import Path
import sys
import signal
import threading
import time

from official_service_dev import verify_source


class ProbeFailure(Exception):
    def __init__(self, reason, *, unknown=False):
        self.reason, self.unknown = reason, unknown


def schema_error_summary(error):
    """Bounded field/type diagnostics for this synthetic schema, never values."""
    from pydantic import ValidationError
    seen = set()
    while error is not None and id(error) not in seen and len(seen) < 16:
        seen.add(id(error))
        if isinstance(error, ValidationError):
            fields = {'project', 'name', 'rules', 'contact', 'ports', 'source'}
            kinds = {'missing', 'extra_forbidden', 'string_type', 'list_type', 'int_type',
                     'int_parsing', 'model_type', 'json_invalid', 'none_required'}
            result = []
            for issue in error.errors(include_input=False, include_context=False, include_url=False)[:8]:
                path = [str(part) if type(part) is int and 0 <= part <= 999 else
                        part if isinstance(part, str) and part in fields else 'other'
                        for part in issue.get('loc', ())[:6]]
                result.append({'field': '.'.join(path) or 'root',
                               'kind': issue['type'] if issue.get('type') in kinds else 'validation_error'})
            return result
        error = error.__cause__ or error.__context__
    return []


def synthetic_response_shape(content):
    try:
        value = json.loads(content)
    except (ValueError, TypeError):
        return 'not_json'
    if not isinstance(value, dict):
        return 'non_object_json'
    if value.get('type') == 'object' and isinstance(value.get('properties'), dict):
        return 'schema_definition'
    if any(key in value for key in ('project', 'ports', 'source')):
        return 'data_object'
    return 'other_object'


def quality_mismatch_fields(actual, expected):
    paths = []

    def compare(a, e, path):
        if len(paths) >= 8:
            return
        if isinstance(e, dict) and isinstance(a, dict):
            for key in e:
                compare(a.get(key), e[key], [*path, key])
        elif isinstance(e, list) and isinstance(a, list):
            if len(a) != len(e):
                paths.append('.'.join([*path, 'length']))
            for index, (left, right) in enumerate(zip(a, e)):
                compare(left, right, [*path, str(index)])
        elif a != e:
            paths.append('.'.join(path))

    compare(actual, expected, [])
    return paths[:8]


def failure(error, *, structured=False):
    """Return fixed codes only; never return exception text/provider bodies."""
    if isinstance(error, ProbeFailure):
        return ('result_unknown' if error.unknown else 'failed'), error.reason
    # Some native SDK adapters wrap HTTP 3xx as connection errors, keeping the
    # HTTPStatusError only in __context__. Prefer that concrete response status
    # over the wrapper's generic connection/500 classification.
    pending, inspected = [error], set()
    while pending and len(inspected) < 32:
        current = pending.pop()
        if current is None or id(current) in inspected:
            continue
        inspected.add(id(current))
        response = getattr(current, 'response', None)
        code = getattr(current, 'status_code', None) or getattr(response, 'status_code', None)
        if isinstance(code, int) and 300 <= code < 400:
            return 'failed', 'provider_redirect_rejected'
        pending.extend((current.__cause__, current.__context__))
    seen = set()
    link = error
    while link is not None and id(link) not in seen and len(seen) < 16:
        seen.add(id(link))
        name = type(link).__name__
        if name == 'NativeSchemaUnconfirmed':
            return 'failed', 'native_schema_verification_failed'
        code = getattr(link, 'status_code', None)
        if isinstance(code, int) and 300 <= code < 400:
            return 'failed', 'provider_redirect_rejected'
        if code in (401, 403, 402, 429):
            return 'failed', {401: 'authentication_failed', 403: 'permission_denied',
                              402: 'payment_required', 429: 'rate_or_quota_limit'}[code]
        if name == 'LLMQuotaExceededError':
            return 'failed', 'quota_exhausted'
        if name in ('ContentPolicyViolationError', 'ContentPolicyFilterError'):
            return 'failed', 'provider_refusal'
        if code in (408, 504) or isinstance(link, (TimeoutError, asyncio.TimeoutError)) or name in ('Timeout', 'ReadTimeout', 'APITimeoutError'):
            return 'result_unknown', 'provider_timeout'
        if name in ('APIConnectionError', 'ConnectError'):
            return 'result_unknown', 'provider_connection_unconfirmed'
        if structured and name in ('ValidationError', 'JSONDecodeError'):
            return 'failed', 'schema_validation_failed'
        if name == 'UnsupportedParamsError':
            return 'failed', 'unsupported_model_parameters'
        if code == 400:
            # The fixed adapter itself uses this narrow text signal to choose
            # its same-model schema fallback. Text is inspected, never emitted.
            if structured and any(term in str(link).lower() for term in ('response_format', 'json_schema', 'schema')):
                return 'failed', 'structured_output_unsupported'
            return 'failed', 'provider_bad_request'
        if code == 404:
            return 'failed', 'provider_endpoint_or_model_not_found'
        if isinstance(code, int) and code >= 500:
            return 'result_unknown', 'provider_unavailable'
        link = link.__cause__
    return 'result_unknown', 'probe_result_unconfirmed'


class CompletionObservation:
    """Observe only the isolated probe's calls to the fixed official adapter."""
    def __init__(self, client, complete):
        self.client, self.complete = client, complete
        self.mode, self.calls = 'unknown', 0
        self.structured_shapes = []

    async def __call__(self, *args, **kwargs):
        # Refuse a changed destination before issuing a request. This does not
        # provide a gateway or redirect any production model traffic.
        if (kwargs.get('model') != self.client.model or kwargs.get('api_base') != self.client.endpoint
                or kwargs.get('api_key') != self.client.api_key):
            raise ProbeFailure('unexpected_model_destination')
        shape = kwargs.get('response_format')
        if isinstance(shape, type):
            self.mode = 'json_schema'
        elif isinstance(shape, dict):
            self.mode = {'json_schema': 'json_schema_non_strict', 'json_object': 'json_object'}.get(shape.get('type'), 'unknown')
        self.calls += 1
        response = await self.complete(*args, **kwargs)
        choices = getattr(response, 'choices', None)
        if not choices:
            raise ProbeFailure('completion_result_unconfirmed', unknown=True)
        choice = choices[0]
        reason = getattr(choice, 'finish_reason', None)
        if reason == 'length':
            raise ProbeFailure('completion_truncated')
        if reason == 'content_filter' or getattr(getattr(choice, 'message', None), 'refusal', None):
            raise ProbeFailure('provider_refusal')
        if reason != 'stop':
            raise ProbeFailure('completion_result_unconfirmed', unknown=True)
        if shape and len(self.structured_shapes) < 8:
            self.structured_shapes.append(synthetic_response_shape(getattr(choice.message, 'content', None)))
        return response


async def probe(config):
    from generation_http import generation_http_policy
    async with generation_http_policy(endpoint=os.environ.get('LLM_ENDPOINT'),
            max_concurrent_requests=config.get('generation_max_concurrent_requests', 2),
            backend_kind=config.get('generation_backend_kind', 'unknown')):
        return await _probe(config)


async def _probe(config):
    source = verify_source(config)
    sys.path.insert(0, str(source))
    if config.get('generation_mode') == 'agent_bridge':
        # Only the local bridge enforces this contract itself. Never advertise
        # native schema capability for an arbitrary API provider/model.
        import litellm
        litellm.register_model({os.environ['LLM_MODEL']: {'supports_response_schema': True,
                              'supports_function_calling': False, 'litellm_provider': 'openai', 'mode': 'chat'}})
    from pydantic import BaseModel, ConfigDict
    import litellm
    from tenacity import stop_after_attempt, wait_none
    from cognee.infrastructure.llm.structured_output_framework.litellm_native.get_native_client import get_native_client

    class Project(BaseModel):
        model_config = ConfigDict(extra='forbid')
        name: str
        rules: list[str]
        contact: str | None

    class Extraction(BaseModel):
        model_config = ConfigDict(extra='forbid')
        project: Project
        ports: list[int]
        source: str

    receipt = {'status': 'failed', 'plain_passed': False, 'schema_passed': False, 'quality_passed': False,
               'structured_mode': 'unknown', 'structured_mode_evidence': 'official_adapter_request',
               'sample_version': 'nested-unicode-null-3', 'boundary': 'official_adapter_synthetic_extraction'}
    client = get_native_client()
    observation = CompletionObservation(client, litellm.acompletion)
    structured = False
    restore_schema = None
    try:
        if client.fallback_model or client.fallback_api_key or client.fallback_endpoint:
            raise ProbeFailure('fallback_configuration_rejected')
        # Keep the fixed adapter's schema routing, validation/self-correction
        # and SDK path. Bound only the probe's outer tenacity policy: a failed
        # sample must not silently spend the production 240s retry floor.
        complete = client.acreate_structured_output.retry_with(stop=stop_after_attempt(1), wait=wait_none())
        litellm.acompletion = observation
        plain = await complete(client, 'Return exactly the text after the colon, with no quotes, punctuation, or extra characters: MEMLANE-PROBE-OK', 'Follow the instruction exactly.', str, num_retries=0)
        receipt['plain_passed'] = isinstance(plain, str) and plain.strip() == 'MEMLANE-PROBE-OK'
        if not receipt['plain_passed']:
            raise ProbeFailure('plain_output_mismatch')
        structured = True
        if client.model.startswith('ollama_chat/'):
            from generation_ollama import verify_native_schema, register_verified_schema
            receipt['capability_calls'] = 1
            evidence = await verify_native_schema(client, observation.complete)
            receipt['native_schema_evidence'] = evidence
            restore_schema = register_verified_schema(evidence, client.model, client.endpoint)
        result = await complete(client,
            'Synthetic facts: project name is 样例项目. Rules in order: pnpm, TypeScript. '
            'No contact is given. Ports in order: 43127 and 43128. Source is synthetic.',
            'Extract only the supplied facts. Missing contact must be null. Preserve order and Unicode. Do not invent facts.', Extraction, num_retries=0)
        expected = {'project': {'name': '样例项目', 'rules': ['pnpm', 'TypeScript'], 'contact': None},
                    'ports': [43127, 43128], 'source': 'synthetic'}
        receipt['schema_passed'] = isinstance(result, Extraction)
        receipt['quality_passed'] = receipt['schema_passed'] and result.model_dump() == expected
        receipt['status'] = 'passed' if receipt['quality_passed'] else 'failed'
        if not receipt['quality_passed']:
            receipt['error_code'] = 'extraction_quality_failed'
            receipt['quality_fields'] = quality_mismatch_fields(result.model_dump(), expected)
    except Exception as error:
        receipt['status'], receipt['error_code'] = failure(error, structured=structured)
        if receipt['error_code'] == 'schema_validation_failed':
            receipt['schema_errors'] = schema_error_summary(error)
            receipt['schema_response_shapes'] = observation.structured_shapes
    finally:
        litellm.acompletion = observation.complete
        if restore_schema is not None:
            # The isolated probe owns this SDK worker. Success logging can
            # perform model lookups after acompletion returns; drain it while
            # the endpoint, HTTP policy and temporary proof are still in scope.
            from litellm.litellm_core_utils.logging_worker import GLOBAL_LOGGING_WORKER
            try:
                try:
                    # Completion schedules the enqueue helper as a task; it
                    # may not have run when the last response returns. Join
                    # those helpers before flush inspects the worker queue.
                    helpers = [task for task in asyncio.all_tasks()
                               if getattr(task.get_coro(), '__qualname__', '') == '_client_async_logging_helper']
                    if helpers:
                        await asyncio.wait_for(asyncio.gather(*helpers), 5)
                    await asyncio.wait_for(GLOBAL_LOGGING_WORKER.flush(), 5)
                except asyncio.TimeoutError:
                    await GLOBAL_LOGGING_WORKER.stop()
            finally:
                restore_schema()
        receipt.update(structured_mode=observation.mode, adapter_calls=observation.calls)
    return receipt


def main():
    parent = os.getppid()
    def watch_owner():
        while os.getppid() == parent:
            time.sleep(.5)
        os.killpg(os.getpgrp(), signal.SIGTERM)
    threading.Thread(target=watch_owner, daemon=True).start()
    try:
        request = json.load(sys.stdin)
        # The manager supplies a fresh synthetic HOME. Every SDK path points
        # inside it before importing Cognee; no daily database is opened.
        root = Path(os.environ['HOME'])
        for key in list(os.environ):
            if key.startswith(('LLM_', 'FALLBACK_', 'COGNEE_', 'EMBEDDING_', 'MEMLANE_', 'OPENAI_', 'ANTHROPIC_', 'GEMINI_', 'OLLAMA_')):
                del os.environ[key]
        os.environ.update(request['environment'])
        os.environ.update(SYSTEM_ROOT_DIRECTORY=str(root / 'system'), DATA_ROOT_DIRECTORY=str(root / 'data'),
                          CACHE_ROOT_DIRECTORY=str(root / 'cache'), DB_PATH=str(root / 'system/databases'),
                          LOGS_ROOT_DIRECTORY=str(root / 'logs'), COGNEE_LOGS_DIR=str(root / 'logs'),
                          TELEMETRY_DISABLED='true', COGNEE_TRACING_ENABLED='false', PYTHON_DOTENV_DISABLED='1')
        with contextlib.redirect_stdout(io.StringIO()):
            result = asyncio.run(probe(request['runtime']))
    except Exception as error:
        status, reason = failure(error)
        result = {'status': status, 'error_code': reason, 'structured_mode': 'unknown'}
    print(json.dumps(result))


if __name__ == '__main__':
    main()
