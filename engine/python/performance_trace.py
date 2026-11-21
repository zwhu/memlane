"""Content-free request timings. Failures in diagnostics never fail inference."""
from contextvars import ContextVar
from datetime import datetime, timezone
import json
import math
import re
import sys
import threading
import time
import uuid

current_trace = ContextVar('memlane_performance_trace', default=None)
HEADER = 'x-memlane-request-id'
_write_lock = threading.Lock()

BRIDGE_BACKENDS = frozenset({'codex', 'pi', 'traex', 'antigravity'})
BACKEND_KINDS = BRIDGE_BACKENDS | frozenset({'openai', 'anthropic', 'gemini', 'custom', 'ollama'})
BRIDGE_COMPONENTS = frozenset(f'{kind}_bridge' for kind in BRIDGE_BACKENDS)
RELAY_COMPONENTS = frozenset({'generation_http', 'cognee_gateway'}) | BRIDGE_COMPONENTS
GATEWAY_ROUTES = frozenset({
    'GET /openapi.json',
    'GET /health',
    'GET /api/v1/health',
    'GET /api/v1/users/me',
    'GET /api/v1/datasets',
    'GET /api/v1/datasets/status',
    'GET /api/v1/sessions',
    'GET /api/v1/activity/pipeline-runs',
    'GET /api/v1/datasets/{id}/data',
    'GET /api/v1/datasets/{id}/graph',
    'GET /api/v1/datasets/{id}/data/{id}',
    'GET /api/v1/datasets/{id}/data/{id}/raw',
    'DELETE /api/v1/datasets/{id}/data/{id}',
    'DELETE /api/v1/datasets/{id}/data/{id}/raw',
    'GET /api/v1/sessions/{id}',
    'GET /api/v1/capture/entries/{id}',
    'PUT /api/v1/capture/entries/{id}',
    'POST /api/v1/recall',
    'POST /api/v1/search',
    'POST /api/v1/remember/entry',
    'POST /api/v1/remember',
    'POST /api/v1/improve',
    'POST /api/v1/cognify',
    'POST /api/v1/memify',
    'POST /api/v1/forget',
    'other',
})
BRIDGE_ERROR_CODES = frozenset({
    # Codex bridge
    'codex_authentication_failed', 'codex_authentication_required', 'codex_busy',
    'codex_cancelled', 'codex_connection_failed', 'codex_context_exceeded',
    'codex_diagnostic_directory_invalid', 'codex_event_identity_invalid', 'codex_isolation_failed',
    'codex_messages_invalid', 'codex_model_failed', 'codex_model_mismatch',
    'codex_model_or_permissions_changed', 'codex_model_unavailable', 'codex_multiple_final_answers',
    'codex_output_limit', 'codex_process_cleanup_unknown', 'codex_process_unavailable',
    'codex_quota_exceeded', 'codex_rate_limited', 'codex_request_invalid',
    'codex_request_unsupported', 'codex_response_format_unsupported', 'codex_rpc_rejected',
    'codex_schema_failed', 'codex_schema_invalid', 'codex_schema_unsupported',
    'codex_stream_disconnected', 'codex_timeout', 'codex_tool_or_rpc_rejected',
    'codex_transport_closed', 'codex_transport_invalid', 'codex_unexpected_tool_execution',
    'codex_upstream_cancelled', 'codex_upstream_error',
    # Pi bridge
    'pi_authentication_required', 'pi_command_credential_unsupported', 'pi_event_unrecognized',
    'pi_interactive_request_rejected', 'pi_model_failed', 'pi_model_or_state_changed',
    'pi_native_schema_unconfirmed', 'pi_output_limit', 'pi_prompt_rejected',
    'pi_provider_configuration_invalid', 'pi_provider_schema_unsupported',
    'pi_retry_or_terminal_invalid', 'pi_rpc_rejected', 'pi_schema_failed',
    'pi_terminal_incomplete', 'pi_timeout', 'pi_tool_or_retry_rejected',
    'pi_tool_rejected', 'pi_transport_closed_or_limit', 'pi_transport_invalid',
    'pi_unexpected_startup_event',
    # Antigravity bridge
    'antigravity_account_location_unavailable', 'antigravity_auth_home_layout_unsupported',
    'antigravity_authentication_required', 'antigravity_cancelled',
    'antigravity_conversation_changed', 'antigravity_conversation_unconfirmed',
    'antigravity_duplicate_initialization', 'antigravity_initialization_unconfirmed',
    'antigravity_model_failed', 'antigravity_model_or_context_changed',
    'antigravity_process_failed', 'antigravity_schema_failed', 'antigravity_schema_not_bound',
    'antigravity_structured_output_missing', 'antigravity_terminal_invalid',
    'antigravity_timeout', 'antigravity_tool_rejected', 'antigravity_tools_not_isolated',
    'antigravity_transport_closed_or_limit', 'antigravity_transport_invalid',
    # TraeX bridge
    'traex_authentication_failed', 'traex_authentication_required', 'traex_busy',
    'traex_connection_failed', 'traex_context_exceeded', 'traex_model_failed',
    'traex_quota_exceeded', 'traex_rate_limited', 'traex_request_invalid',
    'traex_schema_failed', 'traex_schema_unsupported', 'traex_stream_disconnected',
    'traex_timeout', 'traex_upstream_cancelled', 'traex_upstream_error',
    # Generic bridge error
    'unsupported_generation_bridge',
})
_RELAY_OUTCOMES = frozenset({
    'succeeded', 'failed', 'cancelled', 'closed_early', 'CancelledError', 'PoolTimeout',
    'ConnectTimeout', 'ReadTimeout', 'WriteTimeout', 'ConnectError', 'ReadError', 'WriteError',
    'RemoteProtocolError', 'LocalProtocolError', 'ProxyError', 'OSError',
    'timeout', 'upstream_error', 'response_limit',
}) | BRIDGE_ERROR_CODES


class RequestTrace:
    def __init__(self, component, request_id=None, *, backend_kind='unknown'):
        self.component = component
        self.backend_kind = backend_kind if backend_kind in BACKEND_KINDS else 'unknown'
        if component in BRIDGE_COMPONENTS:
            self.backend_kind = component[:-len('_bridge')]
        self.request_id = request_id if isinstance(request_id, str) and re.fullmatch(r'[0-9a-f]{32}', request_id) else uuid.uuid4().hex
        self.started = self.changed = time.monotonic()
        self.phase = None
        self.durations = {}
        self.finished = False
        self.operation = {}
        # Read the upstream public context only if the engine already loaded
        # it. Diagnostics must not import/start an engine in the bridge.
        if component == 'generation_http':
            module = sys.modules.get('cognee.modules.operations')
            getter = getattr(module, 'get_current_operation', None)
            if getter:
                try:
                    operation = getter()
                    for field in ('operation_id', 'parent_operation_id', 'dataset_id'):
                        value = getattr(operation, field, None)
                        if value is not None:
                            self.operation[field] = str(uuid.UUID(str(value)))
                except Exception:
                    pass

    def transition(self, phase):
        now = time.monotonic()
        if self.phase:
            self.durations[self.phase] = self.durations.get(self.phase, 0) + (now - self.changed) * 1000
        self.phase, self.changed = phase, now

    def emit(self, event, **measurements):
        # Names are code-owned; never pass prompts, headers, exception text,
        # URLs, paths, model output, credentials or request bodies here.
        row = {'schema_version': 1, 'event': event, 'component': self.component, 'request_id': self.request_id,
               'backend_kind': self.backend_kind,
               'at': datetime.now(timezone.utc).isoformat(),
               'elapsed_ms': round((time.monotonic() - self.started) * 1000, 3),
               'durations_ms': {k: round(v, 3) for k, v in self.durations.items()}, **self.operation, **measurements}
        try:
            with _write_lock:
                print(json.dumps(row, separators=(',', ':')), file=sys.stderr, flush=True)
        except (OSError, ValueError):
            pass

    def finish(self, outcome, **measurements):
        if self.finished:
            return
        self.transition(None)
        self.finished = True
        self.emit('request_finished', outcome=outcome, **measurements)


def phase(name):
    trace = current_trace.get()
    if trace is not None:
        trace.transition(name)


def relay_trace_line(line, validation_request_id):
    """Forward only our bounded, typed metrics from a noisy SDK subprocess."""
    if len(line) > 8192 or not line.startswith(b'{'):
        return
    try:
        row = json.loads(line)
        if row.get('schema_version') != 1 or row.get('component') not in RELAY_COMPONENTS:
            return
        if row.get('event') not in {'request_finished', 'bridge_admitted', 'first_output_event'}:
            return
        if not re.fullmatch(r'[0-9a-f]{32}', row['request_id']):
            return
        clean = {k: row[k] for k in ('schema_version', 'component', 'event', 'request_id')}
        clean['at'] = datetime.fromisoformat(row['at']).astimezone(timezone.utc).isoformat()
        clean['validation_request_id'] = uuid.UUID(hex=validation_request_id).hex
        clean['backend_kind'] = row.get('backend_kind') if row.get('backend_kind') in BACKEND_KINDS else 'unknown'
        phases = {'capacity_queue', 'dispatch_to_headers', 'response_body', 'request_validation', 'bridge_queue',
                  'execute', 'workspace_prepare', 'process_spawn', 'rpc_initialize', 'thread_start',
                  'model_turn', 'schema_validation', 'process_cleanup', 'upstream_request'}
        def number(value):
            return type(value) in (int, float) and math.isfinite(value) and 0 <= value <= 1e12
        if not number(row['elapsed_ms']) or not isinstance(row['durations_ms'], dict):
            return
        clean['elapsed_ms'] = row['elapsed_ms']
        clean['durations_ms'] = {k: v for k, v in row['durations_ms'].items() if k in phases and number(v)}
        for key in ('input_bytes', 'output_bytes', 'status', 'active_or_waiting', 'max_concurrency'):
            if number(row.get(key)):
                clean[key] = row[key]
        if type(row.get('hit_count')) is int and 0 <= row['hit_count'] <= 100000:
            clean['hit_count'] = row['hit_count']
        if row.get('route') in GATEWAY_ROUTES:
            clean['route'] = row['route']
        if row.get('outcome') is not None:
            # Error prose and arbitrary provider strings never cross this pipe.
            clean['outcome'] = row['outcome'] if row['outcome'] in _RELAY_OUTCOMES else 'other_error'
        with _write_lock:
            print(json.dumps(clean, separators=(',', ':')), file=sys.stderr, flush=True)
    except (ValueError, TypeError, KeyError, OSError, AttributeError):
        return


async def drain_trace_stream(stream, validation_request_id):
    # Drain everything to prevent pipe backpressure. Keep at most one 8KB
    # candidate line, even if SDK stderr contains a huge line or secret body.
    pending, dropping = bytearray(), False
    while True:
        chunk = await stream.read(4096)
        if not chunk:
            if pending and not dropping:
                relay_trace_line(bytes(pending), validation_request_id)
            return
        for index, part in enumerate(chunk.split(b'\n')):
            if index:
                if not dropping:
                    relay_trace_line(bytes(pending), validation_request_id)
                pending.clear()
                dropping = False
            if not dropping and len(pending) + len(part) <= 8192:
                pending.extend(part)
            else:
                pending.clear()
                dropping = True
