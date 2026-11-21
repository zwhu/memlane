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


class OpenCodeBacklogHygieneAndCacheTests(unittest.IsolatedAsyncioTestCase):
    def setUp(self):
        import opencode_backlog
        opencode_backlog.clear_verification_caches()
        self.tmp = tempfile.TemporaryDirectory()
        self.root = Path(self.tmp.name).resolve()
        self.plugin_dir = self.root / 'plugin'
        (self.plugin_dir / 'dist/src').mkdir(parents=True)
        backlog_js = self.plugin_dir / 'dist/src/managed-backlog.js'
        helper_js = self.plugin_dir / 'dist/src/managed-pending-cli.js'
        backlog_js.write_bytes(b'export const backlog = 1;\n')
        helper_js.write_bytes(b'export const helper = 1;\n')
        files = {
            'dist/src/managed-backlog.js': hashlib.sha256(backlog_js.read_bytes()).hexdigest(),
            'dist/src/managed-pending-cli.js': hashlib.sha256(helper_js.read_bytes()).hexdigest(),
        }
        manifest_raw = json.dumps({'protocol': 1, 'files': files}, sort_keys=True).encode()
        (self.plugin_dir / '.memlane-opencode-artifact.json').write_bytes(manifest_raw)

        self.profile = self.root / 'profile'
        self.profile.mkdir()

        def write_private(path, data):
            path.write_bytes(data)
            os.chmod(path, 0o600)
            return hashlib.sha256(data).hexdigest()

        self.manager_token = self.root / 'manager.token'
        write_private(self.manager_token, b'm' * 40)
        self.secret_a = self.root / 'secret-a.token'
        secret_a_sha = write_private(self.secret_a, b'a' * 40)
        self.secret_b = self.root / 'secret-b.token'
        secret_b_sha = write_private(self.secret_b, b'b' * 40)

        self.node = self.root / 'fake-node'
        self.node.write_bytes(b'#!/bin/sh\nexit 0\n' + b'x' * 4096)
        os.chmod(self.node, 0o755)
        self.node_sha = hashlib.sha256(self.node.read_bytes()).hexdigest()

        binding = {
            'version': 1,
            'instance_id': 'oc',
            'plugin_root': str(self.plugin_dir),
            'profile': str(self.profile),
            'memlane': {'origin': 'http://127.0.0.1:19100'},
            'cognee': {'baseUrl': 'http://127.0.0.1:19000'},
            'secrets': [
                {'path': str(self.secret_a), 'sha256': secret_a_sha},
                {'path': str(self.secret_b), 'sha256': secret_b_sha},
            ],
        }
        self.binding_file = self.root / 'binding.json'
        binding_sha = write_private(self.binding_file, json.dumps(binding).encode())

        self.runtime = {
            'service_url': 'http://127.0.0.1:19000',
            'project_control': {
                'url': 'http://127.0.0.1:19100',
                'management_token_file': str(self.manager_token),
            },
        }
        self.installation = {
            'host_kind': 'opencode',
            'instance_id': 'oc',
            'plugin_dir': str(self.plugin_dir),
            'manifest_sha256': hashlib.sha256(manifest_raw).hexdigest(),
            'helper_sha256': files['dist/src/managed-pending-cli.js'],
            'binding_file': str(self.binding_file),
            'binding_sha256': binding_sha,
            'node_executable': str(self.node),
            'node_sha256': self.node_sha,
            'profile': str(self.profile),
        }
        self.target = {
            'agent_id': 'oc',
            'host': 'host',
            'dataset': 'ds',
            'session_id': 'official',
            'native': 'native',
            'tokens': ['a' * 64],
        }

    def tearDown(self):
        import opencode_backlog
        opencode_backlog.clear_verification_caches()
        self.tmp.cleanup()

    def _fake_process(self, lines, *, returncode=0, hang_on_wait=False):
        class FakeProcess:
            def __init__(self):
                self.pid = 424242
                self.returncode = None
                self._lines = list(lines)
                self.terminated = False
                self.stdin = types.SimpleNamespace(
                    write=lambda data: None,
                    drain=AsyncMock(return_value=None),
                    close=lambda: None,
                )
                self.stdout = types.SimpleNamespace(readline=self._readline)

            async def _readline(self):
                if not self._lines:
                    return b''
                item = self._lines.pop(0)
                if isinstance(item, BaseException):
                    raise item
                return item

            async def wait(self):
                if hang_on_wait:
                    raise asyncio.TimeoutError()
                self.returncode = returncode
                return returncode

            def terminate(self):
                self.terminated = True

        return FakeProcess()

    async def test_review_and_check_unlink_worker_file_and_skip_ps_while_save_retains_it(self):
        import opencode_backlog
        for action, payload in (
            ('review', {'session_id': 'official', 'tokens': [], 'unknown': 0}),
            ('check', {'checked': True, 'unknown_outcomes': 0, 'matched_unknown': 0}),
        ):
            with self.subTest(action=action):
                cancel = self.root / f'{action}.stop'
                owner_path = Path(str(cancel) + '.opencode-worker.json')
                owner_path.write_text('{"stale":true}')
                proc = self._fake_process([json.dumps(payload).encode() + b'\n', b''])
                with patch('opencode_backlog.asyncio.create_subprocess_exec', AsyncMock(return_value=proc)), \
                     patch('opencode_backlog._process_start_time', side_effect=AssertionError('ps must not run')):
                    result = await opencode_backlog.execute(
                        self.runtime, self.installation, 'p', self.target, action, cancel)
                self.assertFalse(owner_path.exists(), f'{action} leaked .opencode-worker.json')
                self.assertEqual(result['status'] if action == 'check' else result['unknown'],
                                 'confirmed' if action == 'check' else 0)

        # Mutating save retains durable owner_path and records ps start time.
        cancel = self.root / 'save.stop'
        owner_path = Path(str(cancel) + '.opencode-worker.json')
        proc = self._fake_process([
            json.dumps({'status': 'completed', 'saved': 1, 'handled': 0, 'isolated': 0, 'remaining': 0}).encode() + b'\n',
            b'',
        ])
        with patch('opencode_backlog.asyncio.create_subprocess_exec', AsyncMock(return_value=proc)), \
             patch('opencode_backlog._process_start_time', return_value='Fri Nov 21 06:00:00 2026') as ps_mock:
            result = await opencode_backlog.execute(
                self.runtime, self.installation, 'p', self.target, 'save', cancel)
        self.assertEqual(result['status'], 'completed')
        ps_mock.assert_called_once_with(proc.pid)
        self.assertTrue(owner_path.exists(), 'mutating save must retain .opencode-worker.json')
        owner = json.loads(owner_path.read_text())
        self.assertEqual(owner['phase'], 'confirmed_exited')
        self.assertEqual(owner['exit_code'], 0)
        self.assertEqual(owner['started'], 'Fri Nov 21 06:00:00 2026')

    async def test_read_only_exit_unconfirmed_retains_owner_file(self):
        import opencode_backlog
        cancel = self.root / 'hung-review.stop'
        owner_path = Path(str(cancel) + '.opencode-worker.json')
        proc = self._fake_process([asyncio.TimeoutError()], hang_on_wait=True)
        with patch('opencode_backlog.asyncio.create_subprocess_exec', AsyncMock(return_value=proc)):
            with self.assertRaises(PolicyError) as err:
                await opencode_backlog.execute(
                    self.runtime, self.installation, 'p', self.target, 'review', cancel)
        self.assertEqual(err.exception.code, 'backlog_sender_exit_unconfirmed')
        self.assertTrue(owner_path.exists())
        self.assertEqual(json.loads(owner_path.read_text())['phase'], 'exit_unconfirmed')

    def test_node_and_manifest_digest_cache_avoids_reread_and_rejects_modifications(self):
        import opencode_backlog
        import opencode_installation

        real_sha = opencode_backlog._sha256_file
        real_verify_artifact = opencode_installation.verify
        sha_calls = []
        artifact_calls = []

        def counting_sha(path, **kwargs):
            sha_calls.append(Path(path))
            return real_sha(path, **kwargs)

        def counting_artifact(root, expected=None):
            artifact_calls.append((Path(root), expected))
            return real_verify_artifact(root, expected)

        with patch('opencode_backlog._sha256_file', side_effect=counting_sha), \
             patch('opencode_installation.verify', side_effect=counting_artifact):
            opencode_backlog.verify(self.installation, self.runtime, 'oc')
            opencode_backlog.verify(self.installation, self.runtime, 'oc')
            self.assertEqual(len(sha_calls), 1, 'unchanged node binary was hashed more than once')
            self.assertEqual(len(artifact_calls), 1, 'unchanged artifact tree was hashed more than once')

            # Force re-verification (mutating path) bypasses cache and streams digest.
            opencode_backlog.verify(self.installation, self.runtime, 'oc', force=True)
            self.assertEqual(len(sha_calls), 2)
            self.assertEqual(len(artifact_calls), 2)

            # Modified expected node_sha256 fails immediately even while cache is warm.
            bad_digest_install = {**self.installation, 'node_sha256': '0' * 64}
            with self.assertRaises(PolicyError) as err:
                opencode_backlog.verify(bad_digest_install, self.runtime, 'oc')
            self.assertEqual(err.exception.code, 'backlog_installation_changed')

            # Modified node binary on disk invalidates stat key immediately.
            self.node.write_bytes(b'#!/bin/sh\nexit 1\n' + b'y' * 4097)
            os.chmod(self.node, 0o755)
            with self.assertRaises(PolicyError) as err:
                opencode_backlog.verify(self.installation, self.runtime, 'oc')
            self.assertEqual(err.exception.code, 'backlog_installation_changed')

            # Restore node binary, then tamper with plugin artifact tree.
            self.node.write_bytes(b'#!/bin/sh\nexit 0\n' + b'x' * 4096)
            os.chmod(self.node, 0o755)
            opencode_backlog.verify(self.installation, self.runtime, 'oc')
            (self.plugin_dir / 'dist/src/extra.js').write_bytes(b'evil();\n')
            with self.assertRaises(PolicyError) as err:
                opencode_backlog.verify(self.installation, self.runtime, 'oc')
            self.assertEqual(err.exception.code, 'backlog_installation_changed')

    async def test_gateway_cognee_base_url_verified_and_passed_to_worker(self):
        import opencode_backlog
        binding = json.loads(self.binding_file.read_bytes())
        binding['cognee']['baseUrl'] = 'http://127.0.0.1:19100/cognee'
        raw = json.dumps(binding).encode()
        self.binding_file.write_bytes(raw)
        os.chmod(self.binding_file, 0o600)
        installation = {**self.installation, 'binding_sha256': hashlib.sha256(raw).hexdigest()}
        _, _, _, base_url = opencode_backlog.verify(installation, self.runtime, 'oc')
        self.assertEqual(base_url, 'http://127.0.0.1:19100/cognee')

        sent_stdin = []
        proc = self._fake_process([json.dumps({'session_id': 'official', 'tokens': [], 'unknown': 0}).encode() + b'\n', b''])
        proc.stdin.write = lambda data: sent_stdin.append(data)
        with patch('opencode_backlog.asyncio.create_subprocess_exec', AsyncMock(return_value=proc)):
            await opencode_backlog.execute(self.runtime, installation, 'p', self.target, 'review', self.root / 'gw.stop')
        payload = json.loads(b''.join(sent_stdin).decode())
        self.assertEqual(payload['operator']['service_url'], 'http://127.0.0.1:19100/cognee')

        binding['cognee']['baseUrl'] = 'http://127.0.0.1:19100/other'
        bad_raw = json.dumps(binding).encode()
        self.binding_file.write_bytes(bad_raw)
        os.chmod(self.binding_file, 0o600)
        bad_install = {**self.installation, 'binding_sha256': hashlib.sha256(bad_raw).hexdigest()}
        with self.assertRaises(PolicyError) as err:
            opencode_backlog.verify(bad_install, self.runtime, 'oc')
        self.assertEqual(err.exception.code, 'backlog_installation_changed')


if __name__ == '__main__':
    unittest.main()
