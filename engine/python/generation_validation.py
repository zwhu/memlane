"""One owned, isolated validation subprocess at a time; reads never call models."""
import asyncio
import copy
from datetime import datetime
import json
import os
from pathlib import Path
import re
import signal
import socket
import tempfile
import shutil
import subprocess
import time

from project_policy import PolicyError
from process_groups import group_exists
from performance_trace import BRIDGE_ERROR_CODES, RELAY_COMPONENTS, RequestTrace, _RELAY_OUTCOMES, relay_trace_line

ROOT = Path(__file__).resolve().parents[2]
PROBE_TIMEOUT_SECONDS = 120

_TIMEOUT_OUTCOMES = frozenset({
    'ConnectTimeout', 'ReadTimeout', 'WriteTimeout', 'PoolTimeout', 'timeout',
    'pi_timeout', 'codex_timeout', 'antigravity_timeout', 'traex_timeout',
})
_CONNECTION_OUTCOMES = frozenset({
    'ConnectError', 'ReadError', 'WriteError', 'ProxyError', 'RemoteProtocolError', 'OSError',
    'codex_connection_failed', 'traex_connection_failed',
})
_NETWORK_ERRORS = frozenset({
    'gaierror', 'ConnectionRefusedError', 'ConnectionError', 'ConnectionResetError',
    'ConnectionAbortedError', 'BrokenPipeError', 'TimeoutError',
}) | frozenset(name for name in _TIMEOUT_OUTCOMES | _CONNECTION_OUTCOMES if name[0].isupper())
_PROBE_ERROR_CODES = frozenset({
    'authentication_failed', 'permission_denied', 'payment_required', 'rate_or_quota_limit',
    'provider_endpoint_or_model_not_found', 'provider_unavailable', 'provider_timeout',
    'provider_connection_unconfirmed', 'provider_redirect_rejected', 'provider_bad_request',
    'quota_exhausted', 'provider_refusal', 'structured_output_unsupported', 'unsupported_model_parameters',
    'native_schema_verification_failed', 'schema_validation_failed', 'plain_output_mismatch',
    'extraction_quality_failed', 'unexpected_model_destination', 'completion_result_unconfirmed',
    'completion_truncated', 'fallback_configuration_rejected', 'probe_result_unconfirmed',
})



class _ProbeFailure(Exception):
    def __init__(self, code, exit_code=None):
        self.code, self.exit_code = code, exit_code


def _capture_trace_line(line, request_id, diagnostics):
    try:
        relay_trace_line(line, request_id)
        if not line.startswith(b'{'):
            return
        row = json.loads(line)
        if (row.get('schema_version') != 1 or row.get('component') not in RELAY_COMPONENTS
                or row.get('event') != 'request_finished' or not re.fullmatch(r'[0-9a-f]{32}', row['request_id'])
                or not isinstance(row['durations_ms'], dict)):
            return
        datetime.fromisoformat(row['at'])
        duration = row['elapsed_ms']
        if type(duration) not in (int, float) or not 0 <= duration <= 1e12:
            return
        status = row.get('status')
        outcome = row.get('outcome')
        bridge_code = row.get('bridge_error_code')
        if type(status) is int and 100 <= status <= 599:
            # Later successful transport calls (such as adapter cleanup) must
            # not obscure an earlier failed request.
            if status >= 400 or diagnostics.get('http_status', 0) < 400:
                diagnostics['http_status'] = status
        if isinstance(outcome, str) and outcome in _RELAY_OUTCOMES:
            if outcome not in {'succeeded', 'failed', 'cancelled', 'closed_early'} or 'relay_outcome' not in diagnostics:
                diagnostics['relay_outcome'] = outcome
        if isinstance(bridge_code, str) and bridge_code in BRIDGE_ERROR_CODES:
            diagnostics['bridge_error_code'] = bridge_code
        elif isinstance(outcome, str) and outcome in BRIDGE_ERROR_CODES:
            diagnostics['bridge_error_code'] = outcome
        diagnostics['relay_component'] = row['component']
    except (AttributeError, KeyError, TypeError, ValueError, RecursionError):
        pass


async def _drain_validation_trace(stream, request_id, diagnostics):
    pending, dropping = bytearray(), False
    while chunk := await stream.read(4096):
        for index, part in enumerate(chunk.split(b'\n')):
            if index:
                if not dropping:
                    _capture_trace_line(bytes(pending), request_id, diagnostics)
                pending.clear()
                dropping = False
            if not dropping and len(pending) + len(part) <= 8192:
                pending.extend(part)
            else:
                pending.clear()
                dropping = True
    if pending and not dropping:
        _capture_trace_line(bytes(pending), request_id, diagnostics)


def _http_status(value):
    return value if type(value) is int and 100 <= value <= 599 else None


def _http_failure(status):
    if status is None:
        return None
    if 500 <= status <= 599:
        return 'provider_unavailable'
    return {401: 'authentication_failed', 403: 'permission_denied', 402: 'payment_required',
            429: 'rate_or_quota_limit', 404: 'provider_endpoint_or_model_not_found'}.get(status)


def _relay_failure(diagnostics):
    outcome = diagnostics.get('relay_outcome')
    if outcome in _TIMEOUT_OUTCOMES:
        return 'provider_timeout', outcome
    if outcome in _CONNECTION_OUTCOMES:
        return 'provider_connection_unconfirmed', outcome
    bridge_code = diagnostics.get('bridge_error_code')
    if isinstance(bridge_code, str) and bridge_code in BRIDGE_ERROR_CODES:
        return bridge_code, bridge_code
    code = _http_failure(_http_status(diagnostics.get('http_status')))
    return code, code


def _failure_fields(phase, diagnostics):
    fields = {'failed_phase': phase, 'diagnostic_stage': phase}
    if status := _http_status(diagnostics.get('http_status')):
        fields['http_status'] = status
    bridge_code = diagnostics.get('bridge_error_code')
    if isinstance(bridge_code, str) and bridge_code in BRIDGE_ERROR_CODES:
        fields['bridge_error_code'] = bridge_code
    network = diagnostics.get('network_error') or diagnostics.get('relay_outcome')
    if isinstance(network, str) and network in _NETWORK_ERRORS:
        fields['network_error'] = network
    return fields


def _classify_validation_exception(exc, phase, diagnostics):
    fields = _failure_fields(phase, diagnostics)
    code, detail, status = 'probe_result_unconfirmed', 'probe_result_unconfirmed', 'result_unknown'
    if isinstance(exc, asyncio.CancelledError):
        code = detail = 'validation_interrupted'
    elif isinstance(exc, _ProbeFailure):
        detail = exc.code
        if exc.exit_code:
            fields['process_exit_code'] = exc.exit_code
            code, detail = _relay_failure(diagnostics)
            code = code or 'probe_process_failed'
            detail = detail or code
    else:
        pending, seen = [exc], set()
        network = None
        while pending and len(seen) < 16:
            current = pending.pop()
            if current is None or id(current) in seen:
                continue
            seen.add(id(current))
            name = type(current).__name__
            http_status = _http_status(getattr(current, 'status_code', None))
            if http_status is None:
                http_status = _http_status(getattr(getattr(current, 'response', None), 'status_code', None))
            if http_status is not None:
                fields.setdefault('http_status', http_status)
            if isinstance(current, PolicyError) and isinstance(current.code, str) and re.fullmatch(r'[a-z0-9_]{1,64}', current.code):
                code = detail = current.code
                status = 'failed'
                break
            if isinstance(current, socket.gaierror):
                network = ('dns_resolution_failed', 'dns_resolution_failed', 'gaierror')
            elif network is None:
                if isinstance(current, asyncio.TimeoutError):
                    network = ('validation_timeout', 'validation_timeout', 'TimeoutError')
                elif name in _TIMEOUT_OUTCOMES:
                    network = ('provider_timeout', name, name)
                elif isinstance(current, ConnectionError) or name in _CONNECTION_OUTCOMES:
                    detail = 'connection_refused' if isinstance(current, ConnectionRefusedError) else 'connection_error'
                    network = ('provider_connection_unconfirmed', detail, name if name in _NETWORK_ERRORS else 'ConnectionError')
                elif isinstance(current, OSError):
                    network = ('provider_connection_unconfirmed', 'connection_error', 'OSError')
            pending.extend((current.__context__, current.__cause__))
        if network and status != 'failed':
            code, detail, name = network
            fields.setdefault('network_error', name)
        elif status != 'failed' and (http_code := _http_failure(fields.get('http_status'))):
            code = detail = http_code
        if isinstance(exc, asyncio.TimeoutError):
            code = detail = 'validation_timeout'
            status = 'result_unknown'
    return dict(fields, status=status, error_code=code, diagnostic_code=detail)


def receipt_groups_exited(receipt):
    groups = [receipt.get('process_group'), *receipt.get('bridge_process_groups', [])]
    try:
        return not any(pid and group_exists(pid) for pid in groups)
    except (OSError, subprocess.SubprocessError):
        return False


class GenerationValidation:
    def __init__(self, store, runtime):
        self.store, self.runtime = store, runtime
        self.task, self.process, self.bridge = None, None, None
        self.operation_id = None
        # A previous process's queued/running request is not retried on restart.
        changed = False
        for row in store.state['validations'].values():
            if row['status'] in ('queued', 'running'):
                phase = row.get('phase', row['status'])
                row.update(status='result_unknown', error_code='validation_owner_interrupted',
                           diagnostic_code='validation_owner_interrupted', diagnostic_stage=phase, failed_phase=phase,
                           elapsed_ms=row.get('elapsed_ms', 0.0))
                changed = True
        if changed:
            store.save()

    def start(self, reference, expected, operation_id):
        s = self.store
        request = {'action': 'validate', 'reference': reference}
        old = s.checked(expected, operation_id, request)
        if old is not None:
            return old
        if self.process is not None or self.bridge is not None or (self.task and not self.task.done()):
            raise PolicyError('generation_validation_busy', 409)
        # After an interrupted manager, never infer process exit from a stale
        # operation file. A surviving group (or reused PID) needs review.
        for receipt in s.state['validations'].values():
            if receipt.get('status') != 'result_unknown':
                continue
            if not receipt_groups_exited(receipt):
                raise PolicyError('generation_validation_owner_requires_review', 409)
        row = s.resolve(reference)
        if row['profile']['mode'] == 'agent_bridge':
            from generation_bridge import check_bridge_runtime
            from agent_bridge_runtime import read_agent_defaults
            selected = check_bridge_runtime(self.runtime, row['profile'])
            defaults = read_agent_defaults(selected['kind'], selected['home'])
            environment = None
        else:
            # Check the private key before registering a model operation.
            environment = s.environment(reference, self.runtime)
        s.state['validations'][operation_id] = {'status': 'queued', 'digest': row['digest'],
                                              'reference': copy.deepcopy(reference), 'structured_mode': 'unknown',
                                              'registered_revision': s.state['revision'] + 1,
                                              'phase': 'queued', 'started_at': time.time(),
                                              'backend_kind': row['profile'].get('bridge_kind') or row['profile'].get('provider') or 'unknown'}
        if row['profile']['mode'] == 'agent_bridge':
            s.state['validations'][operation_id]['agent_defaults'] = defaults
        started_monotonic = time.monotonic()
        result = s.commit(operation_id, request, {'operation_id': operation_id, 'status': 'queued'})
        self.operation_id = operation_id
        self.started_monotonic = started_monotonic
        self.task = asyncio.create_task(self.run(operation_id, environment, row['profile']))
        return result

    def get(self, operation_id):
        if operation_id not in self.store.state['validations']:
            raise PolicyError('generation_validation_not_found', 404)
        return copy.deepcopy(self.store.state['validations'][operation_id])

    async def cancel(self, target, expected, operation_id):
        request = {'action': 'cancel_validation', 'target': target}
        old = self.store.checked(expected, operation_id, request)
        if old is not None:
            return old
        self.get(target)
        if target == self.operation_id and self.task and not self.task.done():
            self.task.cancel()
            try:
                await self.task
            except asyncio.CancelledError:
                pass
        row = self.store.state['validations'][target]
        if row['status'] in ('queued', 'running'):
            row.update(_classify_validation_exception(asyncio.CancelledError(), row.get('phase', row['status']), {}),
                       elapsed_ms=round((time.monotonic() - self.started_monotonic) * 1000, 1))
        return self.store.commit(operation_id, request, {'operation_id': target, 'status': row['status'],
                                                         'local_process_exited': self.process is None and self.bridge is None and receipt_groups_exited(row)})

    async def run(self, operation_id, environment, profile):
        trace = RequestTrace('generation_validation', backend_kind=profile.get('bridge_kind') or profile.get('provider'))
        diagnostics = temporary = None
        relay_diag = {}
        failure = None
        result = {}
        started_monotonic = self.started_monotonic
        current_phase = 'prepare'
        row = self.store.state['validations'][operation_id]
        row['status'] = 'running'
        # A cold local reasoning model may spend most of the remote budget on
        # plain output alone. Keep all three checks and allow a finite total
        # budget three times as long; cancellation/unknown rules stay shared.
        deadline = PROBE_TIMEOUT_SECONDS * (3 if profile.get('provider') == 'ollama' else 1)
        row['deadline_seconds'] = deadline
        def transition(phase):
            nonlocal current_phase
            current_phase = row['phase'] = phase
            trace.transition(phase)
            self.store.save()

        # No inherited provider/auth/login environment. stdin carries the
        # credential; no key appears in process arguments or validation logs.
        env = {key: value for key, value in os.environ.items() if key in ('PATH', 'LANG', 'LC_ALL', 'TMPDIR')}
        try:
            transition('prepare')
            temporary = tempfile.mkdtemp(prefix='memlane-generation-validation-')
            if profile['mode'] == 'agent_bridge':
                transition('bridge_start')
                from generation_bridge import ValidationBridge, bridge_runtime_fingerprint
                row['bridge_runtime_fingerprint'] = bridge_runtime_fingerprint(self.runtime, profile)
                def changed():
                    row['bridge_process_groups'] = sorted(self.bridge.processes)
                    self.store.save()
                self.bridge = ValidationBridge(self.runtime, profile, temporary, changed)
                environment = await self.bridge.start()
            env.update(HOME=temporary, XDG_CACHE_HOME=str(Path(temporary) / 'cache'))
            command = [self.runtime['python'], '-B', str(ROOT / 'engine/python/generation_probe.py')]
            transition('process_spawn')
            launch = asyncio.create_task(asyncio.create_subprocess_exec(*command, env=env, cwd=temporary,
                stdin=asyncio.subprocess.PIPE, stdout=asyncio.subprocess.PIPE, stderr=asyncio.subprocess.PIPE,
                start_new_session=True, limit=64 * 1024))
            try:
                self.process = await asyncio.shield(launch)
            except asyncio.CancelledError:
                self.process = await launch
                raise
            row['process_group'] = self.process.pid
            self.store.save()
            diagnostics = asyncio.create_task(_drain_validation_trace(self.process.stderr, trace.request_id, relay_diag))
            payload = {'environment': environment, 'runtime': {
                **{key: self.runtime[key] for key in ('official_source', 'official_archive')},
                'generation_mode': profile['mode'],
                'generation_backend_kind': profile.get('bridge_kind') or profile.get('provider'),
                'generation_max_concurrent_requests': profile.get('max_concurrent_requests', 2)}}
            transition('probe')
            async def communicate():
                try:
                    self.process.stdin.write(json.dumps(payload).encode())
                    await self.process.stdin.drain()
                except (BrokenPipeError, ConnectionResetError):
                    # An early worker exit can close stdin before reading the
                    # request. Collect stdout, stderr and the exit code anyway.
                    pass
                finally:
                    self.process.stdin.close()
                stdout = bytearray()
                while chunk := await self.process.stdout.read(4096):
                    stdout.extend(chunk)
                    if len(stdout) > 16384:
                        raise _ProbeFailure('probe_output_too_large')
                await self.process.wait()
                return bytes(stdout)
            stdout = await asyncio.wait_for(communicate(), deadline)
            try:
                await asyncio.wait_for(asyncio.shield(diagnostics), .25)
            except (asyncio.TimeoutError, OSError, ValueError, RecursionError):
                pass
            if self.process.returncode != 0:
                raise _ProbeFailure('probe_process_failed', self.process.returncode)
            try:
                decoded = json.loads(stdout)
            except (ValueError, RecursionError):
                raise _ProbeFailure('probe_output_invalid_json') from None
            allowed = {'status', 'plain_passed', 'schema_passed', 'quality_passed', 'structured_mode',
                       'sample_version', 'boundary', 'error_code', 'structured_mode_evidence', 'adapter_calls',
                       'schema_errors', 'schema_response_shapes', 'capability_calls', 'native_schema_evidence', 'quality_fields'}
            if not isinstance(decoded, dict) or set(decoded) - allowed or decoded.get('status') not in ('passed', 'failed', 'result_unknown'):
                raise _ProbeFailure('probe_output_invalid_schema')
            if ('error_code' in decoded and (not isinstance(decoded['error_code'], str)
                                             or decoded['error_code'] not in _PROBE_ERROR_CODES)
                    or any(field in decoded and type(decoded[field]) is not bool
                           for field in ('plain_passed', 'schema_passed', 'quality_passed'))):
                raise _ProbeFailure('probe_output_invalid_schema')
            if profile.get('provider') == 'ollama':
                from generation_ollama import evidence_matches
                evidence = decoded.get('native_schema_evidence')
                if ((evidence is not None or decoded.get('status') == 'passed')
                        and not evidence_matches(evidence, environment['LLM_MODEL'], environment['LLM_ENDPOINT'])):
                    raise _ProbeFailure('ollama_native_schema_unverified')
            if profile['mode'] == 'agent_bridge':
                from generation_bridge import bridge_runtime_fingerprint
                from agent_bridge_runtime import read_agent_defaults, check_bridge_runtime
                selected = check_bridge_runtime(self.runtime, profile)
                if (row['bridge_runtime_fingerprint'] != bridge_runtime_fingerprint(self.runtime, profile)
                        or row['agent_defaults'] != read_agent_defaults(selected['kind'], selected['home'])):
                    decoded = {'status':'failed', 'error_code':'generation_bridge_changed_revalidate',
                              'plain_passed':False, 'schema_passed':False, 'quality_passed':False}
            result = decoded
        except (Exception, asyncio.CancelledError) as exc:
            failure = exc
        finally:
            failed_phase = current_phase
            try:
                transition('cleanup')
            except OSError:
                # A failed progress write must still release owned resources.
                pass
            cleanup = asyncio.create_task(self._cleanup(row, temporary, diagnostics))
            while not cleanup.done():
                try:
                    await asyncio.shield(cleanup)
                except asyncio.CancelledError as exc:
                    if failure is None:
                        failure, failed_phase = exc, 'cleanup'
            cleanup_unconfirmed = cleanup.result()
            if failure is not None:
                result = _classify_validation_exception(failure, failed_phase, relay_diag)
            elif result['status'] != 'passed':
                result.update(_failure_fields('probe', relay_diag))
                _, detail = _relay_failure(relay_diag)
                if detail:
                    result['diagnostic_code'] = detail
            if cleanup_unconfirmed:
                result.update(status='result_unknown', error_code='validation_cleanup_unconfirmed',
                              failed_phase='cleanup', diagnostic_stage='cleanup', diagnostic_code='validation_cleanup_unconfirmed')
            row.update(result, elapsed_ms=round((time.monotonic() - started_monotonic) * 1000, 1))
            self.store.save()
            measurements = {key: row[key] for key in ('failed_phase', 'diagnostic_code') if key in row}
            code = row.get('error_code')
            if isinstance(code, str) and re.fullmatch(r'[a-z0-9_]{1,64}', code):
                measurements['error_code'] = code
            trace.finish(row['status'], **measurements)

    async def _cleanup(self, row, temporary, diagnostics):
        cleanup_unconfirmed = False
        try:
            await self.stop_process()
        except Exception:
            cleanup_unconfirmed = True
        try:
            if self.bridge:
                await self.bridge.close()
                self.bridge = None
        except Exception:
            cleanup_unconfirmed = True
        if temporary:
            if self.process is None and self.bridge is None:
                try:
                    shutil.rmtree(temporary)
                except OSError:
                    cleanup_unconfirmed = True
            else:
                cleanup_unconfirmed = True
                row.update(resources_retained=True, temporary_root=temporary)
        if diagnostics is not None:
            # Descendants can keep a pipe open even after the parent exits.
            # Do not strand shutdown or retain arbitrary stderr in memory.
            try:
                await asyncio.wait_for(asyncio.shield(diagnostics), 1)
            except (asyncio.TimeoutError, asyncio.CancelledError):
                diagnostics.cancel()
                await asyncio.gather(diagnostics, return_exceptions=True)
            except Exception:
                pass
        return cleanup_unconfirmed

    async def stop_process(self):
        process = self.process
        if process is None:
            return
        if not group_exists(process.pid):
            await process.wait()
            self.process = None
            return
        try:
            os.killpg(process.pid, signal.SIGTERM)
        except ProcessLookupError:
            pass
        except PermissionError:
            if group_exists(process.pid):
                raise
        try:
            await asyncio.wait_for(process.wait(), 5)
        except asyncio.TimeoutError:
            os.killpg(process.pid, signal.SIGKILL)
            await asyncio.wait_for(process.wait(), 5)
        # Confirm the entire owned group, including descendants, has exited.
        for _ in range(100):
            if not group_exists(process.pid):
                self.process = None
                return
            await asyncio.sleep(.05)
        raise RuntimeError('validation_group_exit_unconfirmed')

    async def close(self):
        if self.task and not self.task.done():
            self.task.cancel()
            try:
                await self.task
            except asyncio.CancelledError:
                pass
