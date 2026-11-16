import copy
import json
import asyncio
import hashlib
import os
from pathlib import Path
import subprocess
import tempfile
import types
import unittest
import uuid
from unittest.mock import AsyncMock, patch

from project_backlog import Backlog
from project_policy import PolicyError


class OpenCodeBacklogDispatchTests(unittest.IsolatedAsyncioTestCase):
    async def asyncSetUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.root = Path(self.tmp.name)
        self.runtime = {'service_url': 'http://127.0.0.1:19000'}
        self.policy = {'ready': True, 'policy': {'permissions': ['read', 'write', 'delete'], 'can_auto_process': True}}
        self.installation = {'host_kind': 'opencode', 'instance_id': 'oc', 'binding_file': '/private/binding'}
        self.registry = types.SimpleNamespace(path=self.root / 'registry.json', state={
            'agents': {'oc': {'installation': self.installation}},
            'projects': {'p': {'id': 'p', 'dataset_id': 'ds'}},
            'sessions': {json.dumps(['oc', 'host']): 'p'}},
            official=types.SimpleNamespace(request=AsyncMock()), policy=lambda *args: copy.deepcopy(self.policy))
        self.backlog = Backlog(self.registry, self.runtime)
        self.row = {'operation_id': 'fixture', 'project_id': 'p', 'targets': []}
        self.target = {'agent_id': 'oc', 'host': 'host', 'dataset': 'ds', 'session_id': 'official',
                       'native': 'native', 'tokens': ['a' * 64], 'saved': 1}

    async def asyncTearDown(self):
        await self.backlog.close()
        self.tmp.cleanup()

    async def test_native_dispatch_precedes_python_only_installation_validation(self):
        snapshot = {'session_id': 'official', 'native': 'native', 'tokens': ['a' * 64], 'unknown': 2}
        execute = AsyncMock(return_value=snapshot)
        with patch('opencode_backlog.execute', execute), patch('project_backlog.verify_installation') as python_verify:
            result = await self.backlog.execute(self.row, self.target, 'review')
        self.assertEqual(result, snapshot)
        python_verify.assert_not_called()
        execute.assert_awaited_once_with(self.runtime, self.installation, 'p', self.target, 'review',
                                         self.backlog.cancel_file(self.row), None)

    async def test_processing_uses_original_node_action_receipt_instead_of_duplicate_official_post(self):
        for outcome in ('submitted', 'unknown', 'disabled', 'deferred'):
            with self.subTest(outcome=outcome):
                target = copy.deepcopy(self.target)
                execute = AsyncMock(return_value={'status': outcome})
                with patch('opencode_backlog.execute', execute):
                    await self.backlog.process_saved(self.row, target)
                self.assertEqual(target['processing'], outcome)
                self.assertEqual(execute.await_args.args[4], 'process')
                self.registry.official.request.assert_not_awaited()

    async def test_disabled_processing_never_launches_a_node_worker(self):
        self.policy['policy']['can_auto_process'] = False
        target = copy.deepcopy(self.target)
        with patch('opencode_backlog.execute', AsyncMock()) as execute:
            await self.backlog.process_saved(self.row, target)
        execute.assert_not_awaited()
        self.assertEqual(target['processing'], 'disabled')

    async def test_node_processing_failure_retains_unknown_without_falling_back_to_another_post(self):
        target = copy.deepcopy(self.target)
        with patch('opencode_backlog.execute', AsyncMock(side_effect=RuntimeError('lost outcome'))):
            await self.backlog.process_saved(self.row, target)
        self.assertEqual(target['processing'], 'unknown')
        self.registry.official.request.assert_not_awaited()


class OpenCodeBacklogWorkerTests(unittest.IsolatedAsyncioTestCase):
    async def test_actual_check_acknowledges_exact_marker_without_a_send(self):
        from opencode_backlog import execute
        before=await execute(self.runtime,self.installation,self.project_id,self.target,'review',self.cancel)
        self.assertEqual(before['unknown'],1)
        self.session_detail={'session_id':before['session_id'],'dataset_id':self.target['dataset'],
                             'qas':[{'context':'unknown'}],'traces':[]}
        result=await execute(self.runtime,self.installation,self.project_id,self.target,'check',self.cancel)
        self.assertEqual((result['status'],result['unknown'],result['matched']),('confirmed',0,1))
        after=await execute(self.runtime,self.installation,self.project_id,self.target,'review',self.cancel)
        self.assertEqual(after['tokens'],before['tokens'])
        self.assertEqual(self.writes,[])

    async def asyncSetUp(self):
        artifact = os.environ.get('MEMLANE_OPENCODE_BACKLOG_TEST_ARTIFACT')
        if not artifact:
            self.skipTest('set MEMLANE_OPENCODE_BACKLOG_TEST_ARTIFACT to the built pinned dev component')
        from aiohttp import web
        from aiohttp.test_utils import TestServer
        from opencode_installation import prepare, digest
        from project_control import Registry, private_write
        from project_control_server import create_app
        from test_project_control import OfficialFixture
        self.tmp = tempfile.TemporaryDirectory()
        self.root = Path(self.tmp.name).resolve()
        self.artifact = Path(artifact).resolve()
        self.writes = []
        self.inflight = asyncio.Event()
        self.response = asyncio.Event()
        self.hold_response = False
        self.session_detail = None

        async def entry(request):
            self.assertEqual(request.headers.get('X-Api-Key'), 'c' * 40)
            self.writes.append(await request.json())
            self.inflight.set()
            if self.hold_response:
                await self.response.wait()
            return web.json_response({'ok': True})
        app = web.Application()
        app.router.add_post('/api/v1/remember/entry', entry)
        async def detail(request):
            return web.json_response(self.session_detail) if self.session_detail else web.json_response({}, status=404)
        app.router.add_get('/api/v1/sessions/{id}', detail)
        self.official_server = TestServer(app)
        await self.official_server.start_server()
        official = OfficialFixture()
        official.url = str(self.official_server.make_url('')).rstrip('/')
        self.registry = Registry(self.root / 'registry.json', official)
        self.registry.state['agents'] = {'a': {'name': 'A', 'principal_id': str(uuid.uuid4())}}
        self.registry.save()
        await self.registry.ensure_common(self.registry.state['revision'])
        for name, value in [('manager', 'm' * 40), ('control', 'a' * 40), ('official', 'c' * 40)]:
            private_write(self.root / name, value.encode())
        self.server = TestServer(create_app(self.registry, 'm' * 40, {'a': 'a' * 40}))
        await self.server.start_server()
        self.profile = self.root / 'profile'
        self.profile.mkdir()
        self.repo = self.root / 'repo'
        self.repo.mkdir()
        subprocess.run(['git', 'init', '-q', str(self.repo)], check=True, capture_output=True)
        self.runtime = {'service_url': official.url, 'project_control': {
            'url': str(self.server.make_url('')).rstrip('/'), 'management_token_file': str(self.root / 'manager')}}
        self.installation = await asyncio.to_thread(prepare, self.artifact, self.root, 'a',
            {'key_file': str(self.root / 'official'), 'control_token_file': str(self.root / 'control')},
            official.url, self.runtime['project_control']['url'], self.profile,
            artifact_sha256=digest((self.artifact / '.memlane-opencode-artifact.json').read_bytes()))
        self.setup_script = '''
import {readFileSync} from 'node:fs'; import {pathToFileURL} from 'node:url';
const cfg=JSON.parse(readFileSync(process.argv[1])), b=JSON.parse(readFileSync(cfg.installation.binding_file));
const load=n=>import(pathToFileURL(b.plugin_root+'/dist/src/'+n+'.js'));
const [{ManagedProjectControl},{resolveManagedConfig},{Outbox,hash}]=await Promise.all(['managed-control','managed-config','runtime'].map(load));
const c=new ManagedProjectControl({...b.memlane,directory:cfg.repo}), native='actual-worker-fixture';
const p=await c.current(native), session=`opencode_${hash('a').slice(0,16)}_${hash(native).slice(0,24)}`;
const target={native,project_id:p.project_id,dataset_id:p.dataset_id}, resolved=resolveManagedConfig(b.cognee);
const q=new Outbox([resolved.baseUrl,b.memlane.instanceId].join('|'),resolved.stateDir);
if(process.argv[2]==='seed') {
 q.enqueue(hash('unknown'),session,{type:'qa',context:'unknown',answer:'PRIVATE_UNKNOWN'},target);
 await q.flush(async()=>{throw new Error('controlled unknown');},async()=>false).catch(()=>{});
 for(const name of ['first','second']) q.enqueue(hash(name),session,{type:'qa',context:name,answer:'PRIVATE_KNOWN'},target);
} else if(process.argv[2]==='append') q.enqueue(hash('new'),session,{type:'qa',context:'new'},target);
console.log(JSON.stringify({project_id:p.project_id,host:c.hostKey(native),dataset:p.dataset_id,...q.reviewBacklog(target,session)}));
'''
        self.descriptor = self.root / 'input.json'
        private_write(self.descriptor, json.dumps({'repo': str(self.repo), 'installation': self.installation}).encode())
        seeded = await self.node('seed')
        self.project_id = seeded['project_id']
        self.target = {'agent_id': 'a', 'host': seeded['host'], 'dataset': seeded['dataset']}
        self.cancel = self.root / 'fixture.stop'

    async def node(self, action):
        env = {k: v for k, v in os.environ.items() if not k.startswith('NODE_')}
        p = await asyncio.create_subprocess_exec(self.installation['node_executable'], '--input-type=module', '-e',
            self.setup_script, str(self.descriptor), action, env=env,
            stdout=asyncio.subprocess.PIPE, stderr=asyncio.subprocess.PIPE)
        stdout, stderr = await asyncio.wait_for(p.communicate(), 20)
        self.assertEqual(p.returncode, 0, stderr.decode())
        return json.loads(stdout)

    async def asyncTearDown(self):
        if hasattr(self, 'tmp'):
            self.response.set()
            await self.server.close()
            await self.official_server.close()
            self.registry.close()
            self.tmp.cleanup()

    async def test_actual_verified_worker_streams_frozen_ids_and_rejects_agent_auth(self):
        from opencode_backlog import execute
        snapshot = await execute(self.runtime, self.installation, self.project_id, self.target, 'review', self.cancel)
        self.assertEqual(len(snapshot['tokens']), 2)
        self.assertEqual(snapshot['unknown'], 1)
        self.assertNotIn('PRIVATE_', json.dumps(snapshot))
        await self.node('append')
        target = {**self.target, **snapshot}
        bad_runtime = copy.deepcopy(self.runtime)
        bad_runtime['project_control']['management_token_file'] = str(self.root / 'control')
        with self.assertRaises(PolicyError) as error:
            await execute(bad_runtime, self.installation, self.project_id, target, 'save', self.root / 'denied.stop')
        self.assertEqual(error.exception.code, 'pending_manager_auth_required')
        self.assertEqual(self.writes, [])
        progress = []
        with patch.dict(os.environ, {'NODE_OPTIONS': '--require /must-not-run'}):
            result = await execute(self.runtime, self.installation, self.project_id, target, 'save', self.cancel, progress.append)
        self.assertEqual(result['status'], 'completed')
        self.assertEqual(result['saved'], 2)
        self.assertEqual(len(self.writes), 2)
        self.assertEqual({w['entry']['context'] for w in self.writes}, {'first', 'second'})
        current = await self.node('status')
        self.assertEqual(len(current['tokens']), 1)
        self.assertEqual(current['unknown'], 1)
        self.assertTrue(all(p['saved'] + p['handled'] + p['isolated'] + p['remaining'] == 2 for p in progress))
        owner = json.loads(Path(str(self.cancel) + '.opencode-worker.json').read_text())
        self.assertEqual(owner['phase'], 'confirmed_exited')
        self.assertEqual(owner['exit_code'], 0)

    async def test_adapter_cancellation_waits_for_inflight_receipt_and_stops_next_id(self):
        from opencode_backlog import execute
        snapshot = await execute(self.runtime, self.installation, self.project_id, self.target, 'review', self.cancel)
        self.hold_response = True
        task = asyncio.create_task(execute(self.runtime, self.installation, self.project_id,
            {**self.target, **snapshot}, 'save', self.cancel))
        await asyncio.wait_for(self.inflight.wait(), 15)
        task.cancel()
        await asyncio.sleep(.05)
        self.response.set()
        with self.assertRaises(asyncio.CancelledError):
            await task
        self.assertEqual(len(self.writes), 1)
        current = await self.node('status')
        self.assertEqual(len(current['tokens']), 1)
        self.assertEqual(current['unknown'], 1)
        owner = json.loads(Path(str(self.cancel) + '.opencode-worker.json').read_text())
        self.assertEqual(owner['phase'], 'confirmed_exited')

    async def test_changed_binding_or_node_digest_refuses_before_worker_launch(self):
        from opencode_backlog import execute
        for key in ('binding_sha256', 'node_sha256'):
            with self.subTest(key=key):
                installation = {**self.installation, key: '0' * 64}
                cancel = self.root / (key + '.stop')
                with self.assertRaises(PolicyError) as error:
                    await execute(self.runtime, installation, self.project_id, self.target, 'review', cancel)
                self.assertEqual(error.exception.code, 'backlog_installation_changed')
                self.assertFalse(Path(str(cancel) + '.opencode-worker.json').exists())
                self.assertEqual(self.writes, [])


if __name__ == '__main__':
    unittest.main()
