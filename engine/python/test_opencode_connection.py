import json
import os
from pathlib import Path
import sys
import tempfile
import asyncio
import shutil
import socket
import unittest
import uuid
from unittest.mock import patch

from agent_connections import Connections
from opencode_connection import OpenCodeConnection, jsonc
from project_control import Registry, private_write
from project_policy import PolicyError
from test_project_control import OfficialFixture


class EnrollmentFixture(OfficialFixture):
    url = 'http://127.0.0.1:19991'

    def __init__(self):
        super().__init__()
        self.owner = str(uuid.uuid4())
        self.children = {}
        self.enrolled = 0

    async def request(self, method, path, body=None, token_file=None):
        if method == 'POST' and path.startswith('/api/v1/agents/create?'):
            self.enrolled += 1
            principal, key = str(uuid.uuid4()), 'c' + uuid.uuid4().hex + 'private'
            self.children[key] = principal
            return {'agentId': principal, 'agentApiKey': key}
        if method == 'GET' and path == '/api/v1/users/me':
            if token_file:
                return {'id': self.children[Path(token_file).read_text()], 'parent_user_id': self.owner}
            return {'id': self.owner}
        raise AssertionError('unexpected fixture endpoint')


class OpenCodeConnectionTests(unittest.IsolatedAsyncioTestCase):
    async def asyncSetUp(self):
        artifact = os.environ.get('MEMLANE_OPENCODE_TEST_ARTIFACT')
        expected = os.environ.get('MEMLANE_OPENCODE_TEST_ARTIFACT_SHA256')
        if not artifact or not expected:
            self.skipTest('set fixed managed artifact path and SHA256')
        self.temporary = tempfile.TemporaryDirectory()
        self.root = Path(self.temporary.name).resolve()
        self.profile = self.root / 'profile'
        self.profile.mkdir()
        self.official = EnrollmentFixture()
        self.registry = Registry(self.root / 'private/registry.json', self.official)
        self.config = {'agent_hosts': {'opencode': {'profile': str(self.profile), 'executable': sys.executable}},
                       'opencode_plugin_dir': artifact, 'opencode_manifest_sha256': expected,
                       'project_control': {'url': 'http://127.0.0.1:19992'}}
        self.connections = Connections(self.registry, self.config)
        self.adapter = next(a for a in self.connections.adapters.values() if getattr(a, 'kind', None) == 'opencode')

    async def asyncTearDown(self):
        if hasattr(self, 'registry'):
            self.registry.close()
            self.temporary.cleanup()

    async def prepare(self, op='prepare'):
        return await self.connections.perform(self.adapter.id, 'prepare', self.registry.state['revision'], op)

    async def activate(self, prepared, op='activate'):
        return await self.connections.perform(self.adapter.id, 'activate', self.registry.state['revision'], op,
                                              prepared['connection']['preview']['proposed_sha256'])

    async def test_missing_explicit_management_runtime_fails_before_subject_or_binding_creation(self):
        self.config['opencode_node_executable'] = str(self.root / 'missing-node')
        with self.assertRaisesRegex(PolicyError, 'opencode_pending_runtime_unconfirmed'):
            await self.prepare('missing-runtime')
        self.assertEqual(self.official.enrolled, 0)
        self.assertFalse(self.adapter.target.exists())
        self.assertFalse((self.registry.path.parent / 'opencode-installations').exists())
        # The outer command may journal its rejection, but it never creates an Agent.
        self.assertNotIn(self.adapter.id, self.registry.state['agents'])

    async def test_prepare_activate_reopen_uses_independent_subject_and_keeps_other_files(self):
        plugins = self.profile / 'plugins'
        plugins.mkdir()
        other = plugins / 'other.ts'; other.write_text('export default async () => ({});')
        config = self.profile / 'opencode.jsonc'
        raw = b'{//keep\n"plugin":["other-plugin",],"model":"unchanged",}'
        config.write_bytes(raw)
        prepared = await self.prepare()
        self.assertEqual(prepared['connection']['status'], 'prepared')
        self.assertFalse(self.adapter.target.exists())
        self.assertEqual(self.official.enrolled, 1)
        self.assertNotEqual(self.registry.state['agents'][self.adapter.id]['principal_id'], self.official.owner)
        activated = await self.activate(prepared)
        self.assertEqual(activated['connection']['status'], 'configured_pending_new_session')
        self.assertEqual(activated['connection']['new_sessions'], 0)
        self.assertEqual(config.read_bytes(), raw)
        self.assertEqual(other.read_text(), 'export default async () => ({});')
        loader = self.adapter.target.read_bytes()
        self.assertEqual(await self.connections.perform(self.adapter.id, 'prepare', 0, 'prepare'), prepared)
        self.assertEqual(self.adapter.target.read_bytes(), loader)
        rebuilt = Connections(self.registry, self.config)
        self.assertEqual(next(r for r in rebuilt.view()['connections'] if r['kind'] == 'opencode')['status'], 'configured_pending_new_session')
        self.assertEqual(self.official.creates, 0, 'computer-level connection created a project/dataset')

    async def test_jsonc_conflicts_and_config_changes_preserve_owned_and_unrelated_files(self):
        config = self.profile / 'opencode.jsonc'
        config.write_text('{"plugin":["@cognee/cognee-opencode"],}')
        before = config.read_bytes()
        with self.assertRaisesRegex(PolicyError, 'duplicate_opencode'):
            await self.prepare('conflict')
        self.assertEqual(config.read_bytes(), before)
        self.assertEqual(self.official.enrolled, 0)
        self.assertFalse(self.adapter.target.exists())
        self.assertEqual(self.connections.operation('conflict')['outcome'], 'rejected')
        config.write_text('{"model":"first",}')
        prepared = await self.prepare('prepare-safe')
        config.write_text('{"model":"edited-concurrently",}')
        with self.assertRaisesRegex(PolicyError, 'host_configuration_changed'):
            await self.activate(prepared)
        self.assertFalse(self.adapter.target.exists())
        self.assertEqual(jsonc(config.read_bytes())['model'], 'edited-concurrently')
        snapshot = self.registry.path.read_bytes()
        receipt = self.connections.operation('activate')
        self.assertEqual(receipt['outcome'], 'rejected')
        self.assertEqual(self.registry.path.read_bytes(), snapshot)

    async def test_activation_crash_is_recovered_read_only_by_original_operation_and_explicit_review(self):
        prepared = await self.prepare()
        original_save = self.registry.save
        def interrupted():
            if self.adapter.record().get('status') == 'configured_pending_new_session':
                raise OSError('simulated final receipt failure')
            original_save()
        with patch.object(self.registry, 'save', side_effect=interrupted):
            with self.assertRaises(OSError):
                await self.activate(prepared, 'activate-lost')
        entry = self.adapter.target.read_bytes()
        self.registry.close()
        self.registry = Registry(self.root / 'private/registry.json', self.official)
        self.connections = Connections(self.registry, self.config)
        self.adapter = self.connections.adapters[self.adapter.id]
        snapshot = self.registry.path.read_bytes()
        receipt = self.connections.operation('activate-lost')
        self.assertEqual(receipt['status'], 'complete')
        self.assertTrue(receipt['reconciled'])
        self.assertEqual(self.registry.path.read_bytes(), snapshot)
        self.assertEqual(self.adapter.target.read_bytes(), entry)
        reviewed = await self.connections.perform(self.adapter.id, 'review', self.registry.state['revision'], 'review-exact')
        self.assertEqual(reviewed['connection']['status'], 'configured_pending_new_session')
        self.assertEqual(self.official.enrolled, 1)

    async def test_unknown_partial_write_is_not_confirmed_or_reinstalled_by_lookup(self):
        prepared = await self.prepare()
        with patch('opencode_connection.private_write', side_effect=OSError('no install result')):
            with self.assertRaises(OSError):
                await self.activate(prepared, 'activate-unknown')
        self.assertFalse(self.adapter.target.exists())
        snapshot = self.registry.path.read_bytes()
        self.assertEqual(self.connections.operation('activate-unknown')['status'], 'unknown')
        self.assertEqual(self.registry.path.read_bytes(), snapshot)
        self.assertFalse(self.adapter.target.exists())
        with self.assertRaises(PolicyError):
            await self.connections.perform(self.adapter.id, 'activate', self.registry.state['revision'], 'activate-unknown', prepared['connection']['preview']['proposed_sha256'])

    async def test_upgrade_keeps_old_binding_and_key_tamper_blocks_activation(self):
        first = await self.prepare()
        await self.activate(first)
        original = self.adapter.record()['active_installation']
        original_binding = Path(original['binding_file']).read_bytes()
        self.official.url = 'http://127.0.0.1:19993'
        second = await self.prepare('prepare-next-generation')
        next_installation = self.adapter.record()['installation']
        self.assertNotEqual(next_installation['binding_file'], original['binding_file'])
        self.assertEqual(Path(original['binding_file']).read_bytes(), original_binding)
        active_entry = self.adapter.target.read_bytes()
        key = Path(self.registry.state['agents'][self.adapter.id]['key_file'])
        private_write(key, b'tampered-key' * 4)
        with self.assertRaisesRegex(PolicyError, 'opencode_installation_requires_review'):
            await self.activate(second, 'activate-tamper')
        self.assertEqual(self.adapter.target.read_bytes(), active_entry)
        self.assertEqual(self.official.enrolled, 1)

    async def test_connections_http_requires_management_and_preserves_original_receipt(self):
        from aiohttp.test_utils import TestClient, TestServer
        from project_control_server import create_app
        client = TestClient(TestServer(create_app(self.registry, 'm' * 40, {}, codex_config=self.config)))
        await client.start_server()
        try:
            path = '/v1/connections/' + self.adapter.id + '/prepare'
            self.assertEqual((await client.post(path, json={})).status, 401)
            self.assertFalse(self.registry.state.get('connection_operations'))
            payload = {'expected_revision': self.registry.state['revision'], 'operation_id': 'http-prepare'}
            manager = {'Authorization': 'Bearer ' + 'm' * 40}
            response = await client.post(path, headers=manager, json=payload)
            self.assertEqual(response.status, 200)
            prepared = await response.json()
            child = Path(self.registry.state['agents'][self.adapter.id]['control_token_file']).read_text()
            self.assertEqual((await client.get('/v1/connections', headers={'Authorization': 'Bearer ' + child})).status, 403)
            snapshot = self.registry.path.read_bytes()
            receipt = await client.get('/v1/connections/operations/http-prepare', headers=manager)
            self.assertEqual((await receipt.json())['result'], prepared)
            self.assertEqual(self.registry.path.read_bytes(), snapshot)
            self.assertFalse(self.adapter.target.exists())
        finally:
            await client.close()

    async def test_native_global_entry_loads_and_binds_two_empty_projects_without_prompt(self):
        executable = os.environ.get('MEMLANE_OPENCODE_NATIVE_EXECUTABLE')
        base = os.environ.get('MEMLANE_OPENCODE_NATIVE_PROFILE_BASE')
        if not executable or not base:
            self.skipTest('set installed OpenCode executable and owned prepared native SDK profile')
        from aiohttp import web, ClientSession, ClientTimeout
        from aiohttp.test_utils import TestServer
        from project_control_server import create_app
        config_home = self.root / 'native-config'
        self.profile = config_home / 'opencode'
        self.profile.mkdir(parents=True)
        self.config['agent_hosts']['opencode'].update(profile=str(self.profile), executable=executable)
        self.connections = Connections(self.registry, self.config)
        self.adapter = next(a for a in self.connections.adapters.values() if getattr(a, 'kind', None) == 'opencode')
        for name in ('package.json', 'bun.lock'):
            shutil.copyfile(Path(base) / name, self.profile / name)
        shutil.copytree(Path(base) / 'node_modules', self.profile / 'node_modules', symlinks=True)
        registrations = []
        control_requests = []
        async def register(request):
            registrations.append(await request.json())
            self.assertNotIn('Authorization', request.headers)
            self.assertIn(request.headers.get('X-Api-Key'), self.official.children)
            return web.json_response({'status': 'ok'})
        official_app = web.Application()
        official_app.router.add_post('/api/v1/agents/register', register)
        official_app.router.add_post('/api/v1/agents/unregister', register)
        official_server = TestServer(official_app)
        await official_server.start_server()
        self.official.url = str(official_server.make_url('')).rstrip('/')
        control_app = create_app(self.registry, 'm' * 40, {}, codex_config=self.config)
        @web.middleware
        async def capture_status(request, handler):
            response = await handler(request)
            control_requests.append({'method': request.method, 'path': request.path, 'status': response.status})
            return response
        control_app.middlewares.insert(0, capture_status)
        control_server = TestServer(control_app)
        await control_server.start_server()
        self.config['project_control']['url'] = str(control_server.make_url('')).rstrip('/')
        process, log = None, None
        try:
            prepared = await self.prepare('native-prepare')
            await self.activate(prepared, 'native-activate')
            env = {key: value for key, value in os.environ.items() if not key.startswith(('MEMLANE_', 'OPENCODE_', 'COGNEE_'))}
            env.update(HOME=str(self.root / 'host-home'), XDG_CONFIG_HOME=str(config_home),
                       XDG_DATA_HOME=str(self.root / 'data'), XDG_STATE_HOME=str(self.root / 'state'), XDG_CACHE_HOME=str(self.root / 'cache'))
            workspaces = [self.root / 'native-repo-a', self.root / 'native-repo-b']
            for path in workspaces:
                path.mkdir()
                import subprocess
                subprocess.run(['git', 'init', '-q', str(path)], check=True)
            with socket.socket() as sock:
                sock.bind(('127.0.0.1', 0)); port = sock.getsockname()[1]
            log = open(self.root / 'native.log', 'wb')
            process = await asyncio.create_subprocess_exec(executable, 'serve', '--hostname', '127.0.0.1', '--port', str(port),
                cwd=workspaces[0], env=env, stdin=asyncio.subprocess.DEVNULL, stdout=log, stderr=log)
            async with ClientSession(timeout=ClientTimeout(total=25)) as session:
                origin = 'http://127.0.0.1:' + str(port)
                for attempt in range(100):
                    if process.returncode is not None:
                        self.fail('native host exited before health')
                    try:
                        async with session.get(origin + '/global/health', timeout=ClientTimeout(total=2)) as response:
                            if response.status == 200:
                                break
                    except Exception:
                        pass
                    await asyncio.sleep(0.1)
                else:
                    self.fail('native host health timed out')
                natives = []
                for workspace in workspaces:
                    async with session.post(origin + '/session', params={'directory': str(workspace)}, json={}) as response:
                        self.assertEqual(response.status, 200)
                        natives.append((await response.json())['id'])
                for attempt in range(400):
                    bound = [key for key in self.registry.state['sessions'] if json.loads(key)[0] == self.adapter.id]
                    if len(bound) == 2 and len(registrations) >= 2:
                        break
                    await asyncio.sleep(0.1)
                self.assertEqual(len(bound), 2, Path(self.root / 'native.log').read_text()[-3000:])
                self.assertGreaterEqual(len(registrations), 2)
                projects = {self.registry.state['sessions'][key] for key in bound}
                self.assertEqual(len(projects), 2)
                self.assertTrue(all(len(list(path.iterdir())) == 1 for path in workspaces))
                print(json.dumps({'native_host': 'OpenCode installed executable', 'empty_sessions': len(natives),
                                  'independent_project_bindings': len(projects), 'model_prompts_sent': 0}))
        finally:
            if process is not None and process.returncode is None:
                process.terminate()
                try:
                    await asyncio.wait_for(process.wait(), timeout=10)
                except asyncio.TimeoutError:
                    process.kill(); await process.wait()
            if log is not None:
                log.close()
            diagnostics = os.environ.get('MEMLANE_OPENCODE_NATIVE_DIAGNOSTICS_ROOT')
            if diagnostics:
                destination = Path(diagnostics)
                destination.mkdir(mode=0o700, parents=True, exist_ok=False)
                for path in [self.root / 'native.log', *list((self.root / 'data/opencode/log').glob('*.log'))]:
                    if path.is_file():
                        private_write(destination / path.name, path.read_bytes()[-1024 * 1024:])
                private_write(destination / 'summary.json', json.dumps({'control_requests': control_requests,
                    'registered': len(registrations), 'bindings': len(self.registry.state['sessions']),
                    'native_returncode': process.returncode if process is not None else None,
                    'model_prompts_sent': 0}, indent=2).encode())
            await control_server.close(); await official_server.close()

    async def test_foreign_loader_and_symlinks_are_not_overwritten(self):
        self.adapter.target.parent.mkdir()
        self.adapter.target.write_text('unowned existing loader')
        with self.assertRaisesRegex(PolicyError, 'existing_opencode_loader'):
            await self.prepare('foreign-loader')
        self.assertEqual(self.adapter.target.read_text(), 'unowned existing loader')
        self.adapter.target.write_bytes(b'')
        with self.assertRaisesRegex(PolicyError, 'existing_opencode_loader'):
            await self.prepare('empty-foreign-loader')
        self.assertEqual(self.adapter.target.read_bytes(), b'')
        self.adapter.target.unlink()
        alias = self.profile / 'opencode.json'
        other = self.root / 'other-config'; other.write_text('{}')
        alias.symlink_to(other)
        with self.assertRaises(PolicyError):
            await self.prepare('symlink-config')
        self.assertEqual(other.read_text(), '{}')
        self.assertEqual(self.official.enrolled, 0)


class OpenCodeJSONCTests(unittest.TestCase):
    def test_jsonc_strings_comments_trailing_commas_and_ambiguity(self):
        value = jsonc(b'{/* block */ "url":"http://example/a,]", "plugin":["other",], // line\n}')
        self.assertEqual(value, {'url': 'http://example/a,]', 'plugin': ['other']})
        for raw in (b'{"plugin":[],"plugin":[]}', b'{/*missing}', b'{"x":NaN}'):
            with self.assertRaises(ValueError):
                jsonc(raw)
