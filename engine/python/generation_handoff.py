"""Management-owned, durable handoff of remaining processing to one profile."""
import asyncio
import copy
import hashlib
import json
import time
from urllib.parse import quote
from generation_config import validation_passed
from project_control import read_secret
from project_policy import PolicyError

ACTIVE = {'queued', 'preparing', 'stopping', 'applying'}


class GenerationHandoff:
    def __init__(self, store, registry, runtime, *, incremental=None, backlog=None):
        self.store, self.registry, self.runtime = store, registry, runtime
        self.incremental, self.backlog, self.task = incremental, backlog, None
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
        if candidate['profile']['mode'] == 'agent_bridge':
            from agent_bridge_runtime import require_connected_bridge
            require_connected_bridge(self.registry, self.runtime, candidate['profile'])
        else:
            if not validation_passed(self.store.state['validations'], candidate['digest']):
                raise PolicyError('generation_validation_required', 409)
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
            await self.apply_receipt(row, result)
        except BaseException:
            row.update(status='unknown', reason='generation_handoff_result_unknown')
        finally:
            self.store.save()

    def source_credentials(self):
        return {identifier:hashlib.sha256(read_secret(actor['key_file']).encode()).hexdigest()
                for identifier, actor in self.registry.state.get('agents', {}).items()
                if actor.get('installation') and identifier not in self.registry.state.get('agent_retirements', {})}

    async def apply_receipt(self, row, result):
        credentials = self.source_credentials()
        cancelled = {(source['session_id'], source['credential_sha256'])
                     for source in result.get('cancelled_sources', []) + row.get('carried_sources', [])}
        bindings = {hashlib.sha256(raw.encode()).hexdigest():(json.loads(raw), project)
                    for raw,project in self.registry.state.get('sessions', {}).items()}
        if self.incremental:
            for key,session in self.incremental.state['sessions'].items():
                # Only a confirmed-cancelled original improve can be resumed.
                # Unrelated/unknown submissions retain their original barrier.
                binding, _ = bindings.get(key, ([None,None],None))
                if ((session.get('session_id'), credentials.get(binding[0])) in cancelled
                        and session.get('status') not in ('unknown','submitting','disabled')):
                    session.update(status='watching', pending_since=time.time(), waiting_reason='', next_attempt_at=0)
                    session['handoff_operation'] = row['operation_id']
                    session.pop('submitted_counts', None)
            # Hooks can submit improve before the periodic scheduler has ever
            # observed that host. Resolve its original binding, not a new
            # owner/session or a guessed session prefix, before scheduling it.
            if self.backlog:
                for key,(binding,project) in bindings.items():
                    if key in self.incremental.state['sessions']:
                        continue
                    agent_id, host = binding
                    sources = {session for session, credential in cancelled
                               if credential == credentials.get(agent_id)}
                    if not sources:
                        continue
                    target = {'agent_id':agent_id, 'host':host,
                              'dataset':self.registry.state['projects'][project]['dataset_id']}
                    self.backlog.checked_target(project, target)
                    snapshot = await self.backlog.execute({'project_id':project,'operation_id':row['operation_id']},target,'review')
                    if snapshot.get('session_id') not in sources:
                        continue
                    self.incremental.state['sessions'][key] = {'session_id':snapshot['session_id'],
                        'status':'watching', 'counts':[0,0], 'pending_since':time.time(),
                        'changed_at':time.time(), 'handoff_operation':row['operation_id']}
            self.incremental.save()
        if self.backlog:
            for job in self.backlog.jobs.values():
                for target in job.get('pending_processing', {}).values():
                    if (target.get('session_id'), credentials.get(target.get('agent_id'))) in cancelled and target.get('processing') == 'submitted':
                        target['processing'] = 'queued'
                        target['handoff_operation'] = row['operation_id']
            self.backlog.persist()
        self.store.state['active'] = copy.deepcopy(row['reference'])
        for identifier in row.get('carried_operations', []):
            self.store.state['handoffs'][identifier]['superseded_by'] = row['operation_id']
        if self.store.state.get('next') in (row.get('previous_next'), row['reference']):
            self.store.state['next'] = None
        self.store.state.pop('next_error', None)
        row.update(status='applied', cancelled_sessions=len(cancelled), ended_at=time.time(), reason='')
        self.store.state['revision'] += 1

    async def review(self, operation):
        row = self.store.state['handoffs'].get(operation)
        if row is None:
            raise PolicyError('generation_handoff_not_found', 404)
        if row['status'] == 'unknown':
            result = await self.service('GET', operation)
            if result.get('status') == 'applied' and result.get('digest') == row['digest'] and result.get('reference') == row['reference']:
                await self.apply_receipt(row, result)
                self.store.save()
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
