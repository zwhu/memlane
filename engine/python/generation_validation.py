"""One owned, isolated validation subprocess at a time; reads never call models."""
import asyncio
import copy
import json
import os
from pathlib import Path
import signal
import tempfile
import shutil
import subprocess

from project_policy import PolicyError
from process_groups import group_exists

ROOT = Path(__file__).resolve().parents[2]
PROBE_TIMEOUT_SECONDS = 120

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
                row.update(status='result_unknown', error_code='validation_owner_interrupted')
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
            check_bridge_runtime(self.runtime)
            environment = None
        else:
            # Check the private key before registering a model operation.
            environment = s.environment(reference, self.runtime)
        s.state['validations'][operation_id] = {'status': 'queued', 'digest': row['digest'],
                                              'reference': copy.deepcopy(reference), 'structured_mode': 'unknown',
                                              'registered_revision': s.state['revision'] + 1}
        result = s.commit(operation_id, request, {'operation_id': operation_id, 'status': 'queued'})
        self.operation_id = operation_id
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
            row.update(status='result_unknown', error_code='validation_interrupted')
        return self.store.commit(operation_id, request, {'operation_id': target, 'status': row['status'],
                                                         'local_process_exited': self.process is None and self.bridge is None and receipt_groups_exited(row)})

    async def run(self, operation_id, environment, profile):
        row = self.store.state['validations'][operation_id]
        row['status'] = 'running'
        self.store.save()
        # No inherited provider/auth/login environment. stdin carries the
        # credential; no key appears in process arguments or validation logs.
        env = {key: value for key, value in os.environ.items() if key in ('PATH', 'LANG', 'LC_ALL', 'TMPDIR')}
        temporary = tempfile.mkdtemp(prefix='memlane-generation-validation-')
        try:
            if profile['mode'] == 'agent_bridge':
                from generation_bridge import ValidationBridge
                def changed():
                    row['bridge_process_groups'] = sorted(self.bridge.processes)
                    self.store.save()
                self.bridge = ValidationBridge(self.runtime, profile, temporary, changed)
                environment = await self.bridge.start()
            env.update(HOME=temporary, XDG_CACHE_HOME=str(Path(temporary) / 'cache'))
            command = [self.runtime['python'], str(ROOT / 'engine/python/generation_probe.py')]
            launch = asyncio.create_task(asyncio.create_subprocess_exec(*command, env=env, cwd=temporary,
                stdin=asyncio.subprocess.PIPE, stdout=asyncio.subprocess.PIPE, stderr=asyncio.subprocess.DEVNULL,
                start_new_session=True, limit=64 * 1024))
            try:
                self.process = await asyncio.shield(launch)
            except asyncio.CancelledError:
                self.process = await launch
                raise
            row['process_group'] = self.process.pid
            self.store.save()
            payload = {'environment': environment, 'runtime': {
                **{key: self.runtime[key] for key in ('official_source', 'official_archive')},
                'generation_mode': profile['mode']}}
            stdout, _ = await asyncio.wait_for(self.process.communicate(json.dumps(payload).encode()), PROBE_TIMEOUT_SECONDS)
            if self.process.returncode != 0 or len(stdout) > 16384:
                raise ValueError('probe_result_unconfirmed')
            result = json.loads(stdout)
            allowed = {'status', 'plain_passed', 'schema_passed', 'quality_passed', 'structured_mode',
                       'sample_version', 'boundary', 'error_code', 'structured_mode_evidence', 'adapter_calls',
                       'schema_errors', 'schema_response_shapes', 'capability_calls', 'native_schema_evidence', 'quality_fields'}
            if not isinstance(result, dict) or set(result) - allowed or result.get('status') not in ('passed', 'failed', 'result_unknown'):
                raise ValueError('probe_result_unconfirmed')
            if profile.get('provider') == 'ollama':
                from generation_ollama import evidence_matches
                evidence = result.get('native_schema_evidence')
                if ((evidence is not None or result.get('status') == 'passed')
                        and not evidence_matches(evidence, environment['LLM_MODEL'], environment['LLM_ENDPOINT'])):
                    raise ValueError('probe_result_unconfirmed')
            row.update(result)
        except asyncio.TimeoutError:
            row.update(status='result_unknown', error_code='validation_timeout')
        except asyncio.CancelledError:
            row.update(status='result_unknown', error_code='validation_interrupted')
        except Exception:
            row.update(status='result_unknown', error_code='probe_result_unconfirmed')
        finally:
            try:
                await self.stop_process()
            except Exception:
                row.update(status='result_unknown', error_code='validation_cleanup_unconfirmed')
            try:
                if self.bridge:
                    await self.bridge.close()
                    self.bridge = None
            except Exception:
                row.update(status='result_unknown', error_code='validation_cleanup_unconfirmed')
            if self.process is None and self.bridge is None:
                shutil.rmtree(temporary)
            else:
                row.update(status='result_unknown', error_code='validation_cleanup_unconfirmed',
                           resources_retained=True, temporary_root=temporary)
            self.store.save()

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
