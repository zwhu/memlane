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


class RequestTrace:
    def __init__(self, component, request_id=None, *, backend_kind='unknown'):
        self.component = component
        self.backend_kind = backend_kind if backend_kind in {'codex', 'openai', 'anthropic', 'gemini', 'custom', 'ollama'} else 'unknown'
        if component == 'codex_bridge':
            self.backend_kind = 'codex'
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
        if row.get('schema_version') != 1 or row.get('component') not in {'generation_http', 'codex_bridge'}:
            return
        if row.get('event') not in {'request_finished', 'bridge_admitted', 'first_output_event'}:
            return
        if not re.fullmatch(r'[0-9a-f]{32}', row['request_id']):
            return
        clean = {k: row[k] for k in ('schema_version', 'component', 'event', 'request_id')}
        clean['at'] = datetime.fromisoformat(row['at']).astimezone(timezone.utc).isoformat()
        clean['validation_request_id'] = uuid.UUID(hex=validation_request_id).hex
        clean['backend_kind'] = row.get('backend_kind') if row.get('backend_kind') in {'codex', 'openai', 'anthropic', 'gemini', 'custom', 'ollama'} else 'unknown'
        phases = {'capacity_queue', 'dispatch_to_headers', 'response_body', 'request_validation', 'bridge_queue',
                  'execute', 'workspace_prepare', 'process_spawn', 'rpc_initialize', 'thread_start',
                  'model_turn', 'schema_validation', 'process_cleanup'}
        def number(value):
            return type(value) in (int, float) and math.isfinite(value) and 0 <= value <= 1e12
        if not number(row['elapsed_ms']) or not isinstance(row['durations_ms'], dict):
            return
        clean['elapsed_ms'] = row['elapsed_ms']
        clean['durations_ms'] = {k: v for k, v in row['durations_ms'].items() if k in phases and number(v)}
        for key in ('input_bytes', 'output_bytes', 'status', 'active_or_waiting', 'max_concurrency'):
            if number(row.get(key)):
                clean[key] = row[key]
        if row.get('outcome') is not None:
            outcomes = {'succeeded', 'failed', 'cancelled', 'closed_early', 'CancelledError', 'PoolTimeout',
                        'ConnectTimeout', 'ReadTimeout', 'WriteTimeout', 'ConnectError', 'ReadError', 'WriteError',
                        'RemoteProtocolError', 'LocalProtocolError', 'ProxyError', 'OSError'}
            # Error prose and arbitrary provider strings never cross this pipe.
            clean['outcome'] = row['outcome'] if row['outcome'] in outcomes else 'other_error'
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
