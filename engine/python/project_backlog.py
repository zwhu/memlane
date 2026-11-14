"""Manager-owned backlog jobs. Journals contain counts and hashes, never bodies."""
import asyncio
import copy
import hashlib
import json
import os
from pathlib import Path
import re
import sys
import time
from urllib.parse import quote
import uuid

from aiohttp import web
from project_control import private_write
from project_policy import PolicyError

ACTIVE = {'queued', 'reviewing', 'running', 'waiting', 'stopping', 'processing'}
WORKER = Path(__file__).with_name('project_backlog_worker.py')


def verify_installation(installation):
    root = Path(installation['plugin_dir'])
    if not all(Path(installation[k]).is_absolute() for k in ('plugin_dir', 'env_file', 'home')):
        raise PolicyError('backlog_installation_unconfirmed', 409)
    manifest = root / '.memlane-project-access.json'
    if manifest.is_symlink() or hashlib.sha256(manifest.read_bytes()).hexdigest() != installation['manifest_sha256']:
        raise PolicyError('backlog_installation_changed', 409)
    files = json.loads(manifest.read_bytes())['files']
    if 'scripts/_memlane_policy.py' not in files or 'scripts/_plugin_common.py' not in files:
        raise PolicyError('backlog_host_unsupported', 409)
    for name, expected in files.items():
        relative = Path(name)
        path = root / relative
        if (relative.is_absolute() or '..' in relative.parts or path.is_symlink() or not path.is_file()
                or hashlib.sha256(path.read_bytes()).hexdigest() != expected):
            raise PolicyError('backlog_installation_changed', 409)


class Backlog:
    def __init__(self, registry, runtime, *, worker=None):
        self.registry, self.runtime, self.worker = registry, runtime, worker
        self.root = registry.path.parent / 'backlog'
        self.path = self.root / 'jobs.json'
        self.root.mkdir(parents=True, exist_ok=True, mode=0o700)
        if self.path.is_symlink():
            raise PolicyError('backlog_journal_unconfirmed', 503)
        self.jobs = json.loads(self.path.read_bytes()) if self.path.exists() else {}
        self.task = None
        for row in self.jobs.values():
            if row['status'] in ACTIVE:
                private_write(self.cancel_file(row), b'stop')
                row.update(status='interrupted', reason='owner_interrupted')
        self.persist()

    def persist(self):
        private_write(self.path, json.dumps(self.jobs, sort_keys=True).encode())

    def cancel_file(self, row):
        return self.root / (row['operation_id'] + '.stop')

    def public(self, row):
        result = {k: v for k, v in row.items() if k not in ('targets',)}
        targets = row['targets']
        result.update(saved=sum(t.get('saved', 0) for t in targets),
                      handled=sum(t.get('handled', 0) for t in targets),
                      isolated=sum(t.get('unknown', 0) + t.get('isolated', 0) for t in targets),
                      remaining=sum(t.get('remaining', len(t.get('tokens', []))) for t in targets),
                      total=sum(len(t.get('tokens', [])) for t in targets),
                      sessions=len(targets),
                      processing_submitted=sum(t.get('processing') == 'submitted' for t in targets),
                      processing_failed=sum(t.get('processing') in ('unknown', 'failed') for t in targets),
                      processing_disabled=sum(t.get('processing') == 'disabled' for t in targets))
        return result

    def get(self, project):
        rows = [r for r in self.jobs.values() if r['project_id'] == project]
        return {'job': self.public(max(rows, key=lambda r: r['created_at'])) if rows else None}

    def checked_target(self, project_id, target, *, write=True):
        project = self.registry.state['projects'].get(project_id)
        agent = self.registry.state['agents'].get(target['agent_id'])
        binding = json.dumps([target['agent_id'], target['host']], ensure_ascii=True)
        if (not project or not agent or self.registry.state['sessions'].get(binding) != project_id
                or project.get('dataset_id') != target['dataset']):
            raise PolicyError('backlog_binding_changed', 409)
        view = self.registry.policy(project, target['agent_id'])
        if not view['ready'] or (write and 'write' not in view['policy']['permissions']):
            raise PolicyError('backlog_write_denied', 403)
        return agent, view

    def start(self, project_id, operation_id):
        try:
            if str(uuid.UUID(operation_id)) != operation_id:
                raise ValueError()
        except (ValueError, TypeError, AttributeError):
            raise PolicyError('backlog_operation_id_required') from None
        old = self.jobs.get(operation_id)
        if old is not None:
            if old['project_id'] != project_id:
                raise PolicyError('backlog_operation_conflict', 409)
            return self.public(old)
        # Only one sender across this manager; the installed shared locks also
        # exclude independent normal hooks and other manager instances.
        if self.task and not self.task.done():
            active = next((r for r in self.jobs.values() if r['status'] in ACTIVE), None)
            if active and active['project_id'] == project_id:
                return self.public(active)
            raise PolicyError('backlog_sender_busy', 409)
        if project_id not in self.registry.state['projects']:
            raise PolicyError('project_not_found', 404)
        if len(self.jobs) >= 100:
            raise PolicyError('backlog_journal_limit', 409)
        project = self.registry.state['projects'][project_id]
        targets = []
        for key, bound in self.registry.state['sessions'].items():
            if bound != project_id:
                continue
            agent_id, host = json.loads(key)
            agent = self.registry.state['agents'].get(agent_id, {})
            if not agent.get('installation'):
                continue
            target = {'agent_id': agent_id, 'host': host, 'dataset': project['dataset_id']}
            self.checked_target(project_id, target)
            targets.append(target)
        row = {'operation_id': operation_id, 'project_id': project_id, 'status': 'queued',
               'reason': '', 'created_at': time.time(), 'targets': targets}
        self.jobs[operation_id] = row
        self.persist()
        self.task = asyncio.create_task(self.run(row))
        return self.public(row)

    def stop(self, project_id, operation_id):
        row = self.jobs.get(operation_id)
        if row is None or row['project_id'] != project_id:
            raise PolicyError('backlog_job_not_found', 404)
        if row['status'] in ACTIVE:
            private_write(self.cancel_file(row), b'stop')
            row['status'] = 'stopping'
            self.persist()
        return self.public(row)

    async def execute(self, row, target, action, update=None):
        agent, _ = self.checked_target(row['project_id'], target)
        installation = copy.deepcopy(agent['installation'])
        if installation.get('host_kind') == 'opencode':
            from opencode_backlog import execute
            return await execute(self.runtime, installation, row['project_id'], target, action, self.cancel_file(row), update)
        verify_installation(installation)
        if self.worker:
            return await self.worker(row, target, action, update)
        env = {k: v for k, v in os.environ.items()
               if not k.startswith(('COGNEE_', 'MEMLANE_', 'LLM_', 'EMBEDDING_'))}
        env.update(HOME=installation['home'], COGNEE_ENV_FILE=installation['env_file'],
                   COGNEE_SESSION_KEY=target['host'], COGNEE_PLUGIN_IN_VENV='1')
        payload = {**target, 'action': action, 'scripts': str(Path(installation['plugin_dir']) / 'scripts'),
                   'cancel_file': str(self.cancel_file(row))}
        process = await asyncio.create_subprocess_exec(
            sys.executable, str(WORKER), cwd=installation['home'], env=env,
            stdin=asyncio.subprocess.PIPE, stdout=asyncio.subprocess.PIPE, stderr=asyncio.subprocess.DEVNULL,
            limit=1024 * 1024)
        process.stdin.write(json.dumps(payload).encode())
        await process.stdin.drain()
        process.stdin.close()
        last = None
        try:
            while True:
                # No total task deadline: progress can span many old 20s batches.
                line = await asyncio.wait_for(process.stdout.readline(), 45)
                if not line:
                    break
                value = json.loads(line)
                if update:
                    update(value)
                last = value
            await asyncio.wait_for(process.wait(), 10)
            if process.returncode != 0 or last is None:
                raise PolicyError('backlog_worker_unconfirmed', 503)
            return last
        except BaseException:
            # Never abandon a sender then blindly retry it. Signal cooperative
            # stop; its HTTP result/unknown journal remains the original truth.
            private_write(self.cancel_file(row), b'stop')
            try:
                process.terminate()
            except ProcessLookupError:
                pass
            try:
                await asyncio.wait_for(process.wait(), 35)
            except asyncio.TimeoutError:
                raise PolicyError('backlog_sender_exit_unconfirmed', 503) from None
            raise

    async def process_saved(self, row, target):
        if self.cancel_file(row).exists() or not (target.get('saved') or target.get('handled')):
            return
        agent, policy = self.checked_target(row['project_id'], target)
        if not policy['policy']['can_auto_process']:
            target['processing'] = 'disabled'
            return
        # Journal before POST. An unknown submit is never automatically repeated.
        target['processing'] = 'submitting'
        self.persist()
        try:
            if agent['installation'].get('host_kind') == 'opencode':
                result = await self.execute(row, target, 'process')
                status = result.get('status')
                target['processing'] = status if status in ('submitted', 'disabled', 'deferred', 'failed', 'unknown') else 'unknown'
                self.persist()
                return
            result = await self.registry.official.request('POST', '/api/v1/improve',
                {'dataset_id': target['dataset'], 'session_ids': [target['session_id']], 'run_in_background': True},
                token_file=agent['key_file'])
            status = result.get('status') if isinstance(result, dict) else None
            target['processing'] = ('submitted' if status in ('running', 'completed', 'skipped')
                                    else 'failed' if status == 'errored' else 'unknown')
        except Exception:
            target['processing'] = 'unknown'
        self.persist()

    async def run(self, row):
        try:
            row['status'] = 'reviewing'
            self.persist()
            # Freeze every original buffer before any POST. New arrivals remain
            # with the normal capture flow and are not silently added to consent.
            for target in row['targets']:
                if self.cancel_file(row).exists():
                    row.update(status='stopped', reason='user_stopped')
                    return
                snapshot = await self.execute(row, target, 'review')
                if ('session_id' not in snapshot or not isinstance(snapshot.get('tokens'), list)
                        or len(snapshot['tokens']) > 200000 or any(not re.fullmatch('[0-9a-f]{64}', t) for t in snapshot['tokens'])
                        or type(snapshot.get('unknown')) is not int or snapshot['unknown'] < 0):
                    raise PolicyError('backlog_snapshot_unconfirmed', 503)
                target.update(snapshot)
                target.update(saved=0, handled=0, isolated=0, remaining=len(target['tokens']))
                self.persist()
            for target in row['targets']:
                if self.cancel_file(row).exists():
                    row.update(status='stopped', reason='user_stopped')
                    return
                if not target['tokens']:
                    continue
                def progress(value):
                    if value.get('status') not in ('running', 'waiting', 'completed', 'paused', 'stopped'):
                        raise PolicyError('backlog_progress_unconfirmed', 503)
                    for key in ('saved', 'handled', 'isolated', 'remaining'):
                        if type(value.get(key)) is not int or value[key] < 0:
                            raise PolicyError('backlog_progress_unconfirmed', 503)
                    if sum(value[k] for k in ('saved', 'handled', 'isolated', 'remaining')) != len(target['tokens']):
                        raise PolicyError('backlog_progress_unconfirmed', 503)
                    target.update({k: value[k] for k in ('saved', 'handled', 'isolated', 'remaining')})
                    row.update(status='stopping' if self.cancel_file(row).exists() else 'waiting' if value['status'] == 'waiting' else 'running',
                               reason=value.get('reason', ''))
                    self.persist()
                result = await self.execute(row, target, 'save', progress)
                if result['status'] != 'completed':
                    row.update(status=result['status'], reason=result.get('reason', 'worker_state_unconfirmed'))
                    return
            row['status'] = 'processing'
            self.persist()
            for target in row['targets']:
                await self.process_saved(row, target)
            row.update(status='stopped' if self.cancel_file(row).exists() else 'completed', reason='')
        except PolicyError as error:
            row.update(status='paused', reason=error.code)
        except Exception:
            row.update(status='paused', reason='backlog_result_unconfirmed')
        finally:
            row['updated_at'] = time.time()
            self.persist()

    async def close(self):
        for row in self.jobs.values():
            if row['status'] in ACTIVE:
                self.stop(row['project_id'], row['operation_id'])
        if self.task and not self.task.done():
            await asyncio.wait_for(asyncio.shield(self.task), 40)


def attach_backlog(app, registry, runtime):
    backlog = Backlog(registry, runtime)
    async def endpoint(request):
        if not request['manager']:
            raise PolicyError('management_required', 403)
        project = request.match_info['id']
        if request.method == 'GET':
            return web.json_response(backlog.get(project))
        if request.content_type != 'application/json':
            raise PolicyError('json_required', 415)
        payload = await request.json()
        if not isinstance(payload, dict) or set(payload) != {'operation_id'}:
            raise PolicyError('invalid_request_fields')
        result = (backlog.stop(project, payload['operation_id']) if request.path.endswith('/stop')
                  else backlog.start(project, payload['operation_id']))
        return web.json_response({'job': result})
    app.add_routes([web.get('/v1/projects/{id}/backlog', endpoint),
                    web.post('/v1/projects/{id}/backlog', endpoint),
                    web.post('/v1/projects/{id}/backlog/stop', endpoint)])
    async def cleanup(_):
        await backlog.close()
    app.on_cleanup.append(cleanup)
    return backlog
