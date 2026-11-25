"""Management-owned, durable handoff of remaining processing to one profile."""
import asyncio
import copy
import hashlib
import json
import time
from urllib.parse import quote
from generation_config import validation_passed
from _runtime_common import read_secret
from project_policy import PolicyError

ACTIVE = {'queued', 'preparing', 'stopping', 'applying', 'reconciling'}


class GenerationHandoff:
    def __init__(self, store, registry, runtime, *, incremental=None, backlog=None):
        self.store, self.registry, self.runtime = store, registry, runtime
        self.incremental, self.backlog, self.task = incremental, backlog, None
        self._lock = asyncio.Lock()
        for row in self.store.state.setdefault('handoffs', {}).values():
            if row['status'] in ACTIVE:
                row.update(status='unknown', reason='controller_interrupted')
        self.store.save()

    def pending(self):
        rows = list(self.store.state.get('handoffs', {}).values())
        return next((r for r in rows if r['status'] in ACTIVE or r['status'] == 'unknown'), None)

    def counts(self):
        sessions = self.incremental.state['sessions'].values() if self.incremental else []
        return {'pending_sessions': sum(bool(r.get('pending_since')) and r.get('status') != 'unknown' for r in sessions),
                'unconfirmed_sessions': sum(r.get('status') == 'unknown' for r in sessions)}

    def view(self, operation=None):
        rows = self.store.state.get('handoffs', {})
        row = rows.get(operation) if operation else max(rows.values(), key=lambda r:r['started_at'], default=None)
        return {'handoff': copy.deepcopy(row), **self.counts()}

    async def preview(self):
        result = await self.registry.official.request('GET', '/api/v1/memlane/generation/status',
            adapter_token=read_secret(self.runtime['project_control']['management_token_file']))
        if type(result.get('running_sessions')) is not int or result['running_sessions'] < 0:
            raise PolicyError('generation_processing_state_unconfirmed', 503)
        return {**self.view(), 'running_sessions': result['running_sessions']}

    def start(self, reference, expected, operation):
        request = {'action':'handoff', 'reference':reference}
        old = self.store.checked(expected, operation, request)
        if old is not None:
            return self.view(operation)
        if self.pending():
            raise PolicyError('generation_handoff_requires_review', 409)
        candidate = self.store.resolve(reference)
        validations = self.store.state['validations']
        if not validation_passed(validations, candidate['digest']):
            raise PolicyError('generation_validation_required', 409)
        if candidate['profile']['mode'] == 'agent_bridge':
            if self.runtime or any(r.get('digest') == candidate['digest'] and r.get('bridge_runtime_fingerprint')
                                   for r in validations.values()):
                from agent_bridge_runtime import check_bridge_runtime, require_bridge_validation
                require_bridge_validation(self.store, self.runtime, candidate, allow_legacy=False)
                check_bridge_runtime(self.runtime, candidate['profile'])
        else:
            self.store.native_schema_evidence(candidate)
        if reference == self.store.state.get('active'):
            raise PolicyError('generation_profile_already_active', 409)
        row = {'operation_id':operation, 'reference':copy.deepcopy(reference), 'digest':candidate['digest'],
               'previous':copy.deepcopy(self.store.state['active']), 'status':'queued', 'reason':'',
               'previous_next':copy.deepcopy(self.store.state.get('next')),
               'started_at':time.time(), **self.counts()}
        row['carried_operations'] = [identifier for identifier, prior in self.store.state['handoffs'].items()
            if prior.get('status') == 'not_applied' and not prior.get('superseded_by')]
        row['carried_sources'] = [source for identifier in row['carried_operations']
            for source in self.store.state['handoffs'][identifier].get('cancelled_sources', [])]
        self.store.state['handoffs'][operation] = row
        self.store.commit(operation, request, {'operation_id':operation})
        self.task = asyncio.create_task(self.run(row, candidate))
        return self.view(operation)

    async def service(self, method, operation, body=None):
        return await self.registry.official.request(method,
            '/api/v1/memlane/generation/handoffs/' + quote(operation, safe=''), body,
            adapter_token=read_secret(self.runtime['project_control']['management_token_file']))

    async def run(self, row, candidate):
        try:
            row['status'] = 'stopping'; self.store.save()
            environment = self.store.environment(row['reference'], self.runtime)
            evidence = self.store.native_schema_evidence(candidate)
            if evidence:
                environment['MEMLANE_OLLAMA_SCHEMA_EVIDENCE'] = json.dumps(evidence)
            result = await self.service('PUT', row['operation_id'], {'reference':row['reference'],
                'digest':row['digest'], 'profile':candidate['profile'],
                'environment':environment, 'source_credentials':list(self.source_credentials().values())})
            for _ in range(120):
                if result.get('status') not in ('preparing','stopping','applying'):
                    break
                row['status'] = result['status']; self.store.save()
                await asyncio.sleep(1)
                result = await self.service('GET', row['operation_id'])
            if result.get('reference') != row['reference'] or result.get('digest') != row['digest']:
                raise PolicyError('generation_handoff_receipt_mismatch', 409)
            if result.get('status') == 'not_applied':
                row.update(status='not_applied', reason=result.get('reason',''), cancelled_sources=result.get('cancelled_sources', []))
                return
            if result.get('status') != 'applied':
                row.update(status='unknown', reason=result.get('reason') or 'generation_stop_unconfirmed')
                return
            if not self.current_receipt(row, result):
                row.update(status='unknown', reason='generation_current_configuration_mismatch')
                return
            await self.apply_receipt(row, result)
        except BaseException:
            row.update(status='unknown', reason='generation_handoff_result_unknown')
        finally:
            self.store.save()

    def source_credentials(self, *, warnings=None):
        result = {}
        for identifier, actor in self.registry.state.get('agents', {}).items():
            if not actor.get('installation') or identifier in self.registry.state.get('agent_retirements', {}):
                continue
            if warnings is None:
                result[identifier] = hashlib.sha256(read_secret(actor['key_file']).encode()).hexdigest()
                continue
            try:
                key_file = actor.get('key_file')
                if not key_file:
                    raise PolicyError('credential_unavailable', 503)
                result[identifier] = hashlib.sha256(read_secret(key_file).encode()).hexdigest()
            except Exception as error:
                warnings.append({
                    'scope': 'agent_credential',
                    'agent_id': identifier,
                    'error_code': error.code if isinstance(error, PolicyError) else 'credential_unavailable',
                })
        return result

    @staticmethod
    def current_receipt(row, result):
        return result.get('runtime_active') == {'reference': row['reference'], 'digest': row['digest']}

    async def apply_receipt(self, row, result):
        async with self._lock:
            await self._apply_receipt_locked(row, result)

    async def _apply_receipt_locked(self, row, result):
        if row.get('status') == 'applied':
            return
        cancelled = {(source['session_id'], source['credential_sha256'])
                     for source in result.get('cancelled_sources', []) + row.get('carried_sources', [])}
        # Phase 1: Atomically persist the confirmed engine active state with an
        # idempotent commit marker and intermediate 'reconciling' status BEFORE
        # reading agent credentials or running per-session reconciliation. This
        # prevents control-plane/engine active split-brain (even if an unrelated
        # agent key file is missing) while ensuring crashes mid-reconciliation
        # remain resumable via review() without double-incrementing revision.
        if not row.get('active_committed'):
            self.store.resolve(row['reference'])
            self.store.state['active'] = copy.deepcopy(row['reference'])
            for identifier in row.get('carried_operations', []):
                if identifier in self.store.state.get('handoffs', {}):
                    self.store.state['handoffs'][identifier]['superseded_by'] = row['operation_id']
            if self.store.state.get('next') in (row.get('previous_next'), row['reference']):
                self.store.state['next'] = None
            self.store.state.pop('next_error', None)
            self.store.state['revision'] += 1
            row['active_committed'] = True
        row.update(status='reconciling', cancelled_sessions=len(cancelled), reason='')
        self.store.save()

        # Phase 2: Best-effort, per-agent/per-session isolated reconciliation of
        # confirmed cancelled sessions. Only advance to terminal 'applied' after
        # incremental/backlog state persistence succeeds; otherwise keep the
        # operation resumable as 'unknown' (with active_committed=True).
        warnings = []
        persist_failed = False
        credentials = self.source_credentials(warnings=warnings)
        bindings = {}
        for raw, project in self.registry.state.get('sessions', {}).items():
            try:
                bindings[hashlib.sha256(raw.encode()).hexdigest()] = (json.loads(raw), project)
            except (TypeError, ValueError):
                continue
        if self.incremental:
            for key, session in self.incremental.state['sessions'].items():
                # Only a confirmed-cancelled original improve can be resumed.
                # Unrelated/unknown submissions retain their original barrier.
                binding, _ = bindings.get(key, ([None, None], None))
                if ((session.get('session_id'), credentials.get(binding[0])) in cancelled
                        and session.get('status') not in ('unknown', 'submitting', 'disabled')):
                    session.update(status='watching', pending_since=time.time(), waiting_reason='', next_attempt_at=0)
                    session['handoff_operation'] = row['operation_id']
                    session.pop('submitted_counts', None)
            # Hooks can submit improve before the periodic scheduler has ever
            # observed that host. Resolve its original binding, not a new
            # owner/session or a guessed session prefix, before scheduling it.
            if self.backlog:
                for key, (binding, project) in bindings.items():
                    if key in self.incremental.state['sessions']:
                        continue
                    agent_id, host = binding
                    sources = {session for session, credential in cancelled
                               if credential == credentials.get(agent_id)}
                    if not sources:
                        continue
                    try:
                        project_row = self.registry.state.get('projects', {}).get(project)
                        if not project_row or not project_row.get('dataset_id'):
                            raise PolicyError('backlog_binding_changed', 409)
                        target = {'agent_id': agent_id, 'host': host, 'dataset': project_row['dataset_id']}
                        checked = self.backlog.checked_target(project, target, write=False)
                        if isinstance(checked, tuple) and len(checked) == 2 and isinstance(checked[1], dict):
                            permissions = checked[1].get('policy', {}).get('permissions')
                            if permissions is not None and 'read' not in permissions:
                                raise PolicyError('backlog_read_denied', 403)
                        snapshot = await self.backlog.execute(
                            {'project_id': project, 'operation_id': row['operation_id']}, target, 'review')
                        if snapshot.get('session_id') not in sources:
                            continue
                        self.incremental.state['sessions'][key] = {'session_id': snapshot['session_id'],
                            'status': 'watching', 'counts': [0, 0], 'pending_since': time.time(),
                            'changed_at': time.time(), 'handoff_operation': row['operation_id']}
                    except Exception as error:
                        warnings.append({
                            'project_id': project,
                            'agent_id': agent_id,
                            'error_code': error.code if isinstance(error, PolicyError) else 'handoff_session_reconcile_failed',
                        })
            try:
                self.incremental.save()
            except Exception as error:
                persist_failed = True
                warnings.append({
                    'scope': 'incremental',
                    'error_code': error.code if isinstance(error, PolicyError) else 'incremental_save_failed',
                })
        if self.backlog:
            try:
                for job in self.backlog.jobs.values():
                    for target in job.get('pending_processing', {}).values():
                        if (target.get('session_id'), credentials.get(target.get('agent_id'))) in cancelled and target.get('processing') == 'submitted':
                            target['processing'] = 'queued'
                            target['handoff_operation'] = row['operation_id']
                self.backlog.persist()
            except Exception as error:
                persist_failed = True
                warnings.append({
                    'scope': 'backlog',
                    'error_code': error.code if isinstance(error, PolicyError) else 'backlog_save_failed',
                })
        if warnings:
            row['reconcile_warnings'] = warnings
        else:
            row.pop('reconcile_warnings', None)
        if persist_failed:
            row.update(status='unknown', cancelled_sessions=len(cancelled),
                       reason='handoff_reconcile_persist_failed')
            self.store.save()
            return
        row.update(status='applied', cancelled_sessions=len(cancelled), ended_at=time.time(), reason='')
        self.store.save()

    async def review(self, operation):
        async with self._lock:
            row = self.store.state['handoffs'].get(operation)
            if row is None:
                raise PolicyError('generation_handoff_not_found', 404)
            if row['status'] in ('unknown', 'reconciling'):
                result = await self.service('GET', operation)
                if result.get('status') == 'applied' and result.get('digest') == row['digest'] and result.get('reference') == row['reference']:
                    if not self.current_receipt(row, result):
                        row['reason'] = 'generation_current_configuration_mismatch'
                        self.store.save()
                        return self.view(operation)
                    await self._apply_receipt_locked(row, result)
                elif result.get('status') == 'not_applied' and result.get('digest') == row['digest'] and result.get('reference') == row['reference']:
                    row.update(status='not_applied', reason=result.get('reason',''), cancelled_sources=result.get('cancelled_sources', []))
                    self.store.save()
            return self.view(operation)

    async def close(self):
        if self.task and not self.task.done():
            self.task.cancel()
            await asyncio.gather(self.task, return_exceptions=True)


def attach_generation_handoff(app, store, registry, runtime, incremental, backlog):
    from aiohttp import web
    from project_control_server import fields
    control = GenerationHandoff(store, registry, runtime, incremental=incremental, backlog=backlog)
    store.runtime_handoff = True
    incremental.handoff = control
    backlog.handoff = control
    async def route(request):
        if not request['manager']:
            raise PolicyError('management_required', 403)
        if request.method == 'GET':
            operation = request.match_info.get('operation')
            return web.json_response(await control.review(operation) if operation else await control.preview())
        if request.content_type != 'application/json':
            raise PolicyError('json_required', 415)
        payload = fields(await request.json(), ('profile','expected_revision','operation_id'))
        return web.json_response(control.start(payload['profile'],payload['expected_revision'],payload['operation_id']))
    app.router.add_get('/v1/generation/handoffs', route)
    app.router.add_get('/v1/generation/handoffs/{operation}', route)
    app.router.add_post('/v1/generation/handoffs', route)
    async def cleanup(_):
        await control.close()
    app.on_cleanup.insert(0, cleanup)
    return control
