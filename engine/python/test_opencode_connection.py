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
from _runtime_common import file_sha256
from opencode_connection import OpenCodeConnection, _stream_fd_sha256, clear_node_hash_cache, jsonc, sha
from opencode_installation import loader
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
        binding = json.loads(Path(self.adapter.record()['installation']['binding_file']).read_bytes())
        self.assertEqual(binding['cognee']['baseUrl'], 'http://127.0.0.1:19992/cognee')
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
        self.assertEqual(json.loads(original_binding)['cognee']['baseUrl'], 'http://127.0.0.1:19992/cognee')
        self.config['project_control']['url'] = 'http://127.0.0.1:19993'
        second = await self.prepare('prepare-next-generation')
        next_installation = self.adapter.record()['installation']
        self.assertNotEqual(next_installation['binding_file'], original['binding_file'])
        self.assertEqual(json.loads(Path(next_installation['binding_file']).read_bytes())['cognee']['baseUrl'], 'http://127.0.0.1:19993/cognee')
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


class OpenCodeVerifyInstallationHashTests(unittest.IsolatedAsyncioTestCase):
    def setUp(self):
        self.root = Path(self.enterContext(tempfile.TemporaryDirectory())).resolve()
        clear_node_hash_cache()
        self.addCleanup(clear_node_hash_cache)
        self.registry = Registry(self.root / 'private/registry.json', EnrollmentFixture())
        self.addCleanup(self.registry.close)
        profile = self.root / 'profile'
        profile.mkdir()
        self.adapter = OpenCodeConnection(self.registry, {}, profile, sys.executable)
        self.node = self.root / 'node'
        self.node.write_bytes(b'x' * (1024 * 1024 + 1))
        self.node.chmod(0o700)
        artifact = self.root / 'artifact'
        helper = artifact / 'dist/src/managed-pending-cli.js'
        helper.parent.mkdir(parents=True)
        helper.write_bytes(b'export {};')
        helper_sha = file_sha256(helper)
        manifest = json.dumps({'protocol': 1, 'files': {str(helper.relative_to(artifact)): helper_sha}}).encode()
        (artifact / '.memlane-opencode-artifact.json').write_bytes(manifest)
        secret = self.root / 'secret'
        private_write(secret, b'synthetic secret')
        binding_file = self.root / 'binding.json'
        binding = {'instance_id': self.adapter.id, 'profile': str(profile),
                   'secrets': [{'path': str(secret), 'sha256': file_sha256(secret)}]}
        raw_binding = json.dumps(binding).encode()
        private_write(binding_file, raw_binding)
        self.installation = {'instance_id': self.adapter.id, 'profile': str(profile), 'plugin_dir': str(artifact),
                             'manifest_sha256': sha(manifest), 'node_executable': str(self.node),
                             'node_sha256': file_sha256(self.node), 'helper_sha256': helper_sha,
                             'binding_file': str(binding_file), 'binding_sha256': sha(raw_binding)}
        self.installation['loader_sha256'] = sha(loader(self.installation))

    def assert_requires_review(self, installation):
        with self.assertRaises(PolicyError) as raised:
            self.adapter.verify_installation(installation)
        self.assertEqual((raised.exception.code, raised.exception.status), ('opencode_installation_requires_review', 409))

    async def test_streams_hash_caches_unchanged_node_and_rehashes_on_force_review_or_change(self):
        verify = self.adapter.verify_installation
        read_bytes = Path.read_bytes

        def forbid_node_read_bytes(path):
            self.assertNotEqual(path, self.node)
            return read_bytes(path)

        with (patch.object(Path, 'read_bytes', forbid_node_read_bytes),
              patch('opencode_connection._stream_fd_sha256', wraps=_stream_fd_sha256) as hashed):
            verify(self.installation)
            verify(self.installation)
            self.assertEqual(hashed.call_count, 1)
            verify(self.installation, force=True)
            self.assertEqual(hashed.call_count, 2)

            # Explicit review on a configured connection must force a fresh hash even with a warm cache.
            stub = loader(self.installation)
            private_write(self.adapter.target, stub)
            self.registry.state.setdefault('connections', {})[self.adapter.id] = {
                'status': 'configured_pending_new_session',
                'activated': True,
                'active_installation': dict(self.installation),
                'active_loader_sha256': sha(stub),
            }
            reviewed = await self.adapter.review(self.registry.state['revision'])
            self.assertEqual(reviewed['status'], 'configured_pending_new_session')
            self.assertEqual(hashed.call_count, 3)

            # Same-length in-place edit preserving mtime must still invalidate cache via ctime_ns.
            st_before = os.lstat(self.node)
            with self.node.open('r+b') as handle:
                handle.write(b'y')
            os.utime(self.node, ns=(st_before.st_atime_ns, st_before.st_mtime_ns))
            self.assert_requires_review(self.installation)
            self.assertEqual(hashed.call_count, 4)

    def test_rejects_unsafe_node_before_hashing_even_with_a_cached_digest(self):
        self.adapter.verify_installation(self.installation)
        link = self.node.with_name('node-link')
        link.symlink_to(self.node)
        loop = self.node.with_name('node-loop')
        loop.symlink_to(loop)
        parent_link = self.root / 'dir-link'
        parent_link.symlink_to(self.root)
        node_dir = self.root / 'node-dir'
        node_dir.mkdir()
        with patch('opencode_connection._stream_fd_sha256', wraps=_stream_fd_sha256) as hashed:
            self.assert_requires_review({**self.installation, 'node_executable': str(link)})
            self.assert_requires_review({**self.installation, 'node_executable': str(loop)})
            self.assert_requires_review({**self.installation, 'node_executable': str(parent_link / 'node')})
            self.assert_requires_review({**self.installation, 'node_executable': str(node_dir)})
            self.node.chmod(0o600)
            self.assert_requires_review(self.installation)
            self.node.chmod(0o700)
            with self.node.open('r+b') as handle:
                handle.truncate(512 * 1024 * 1024 + 1)
            self.assert_requires_review(self.installation)
            hashed.assert_not_called()


class OpenCodeJSONCTests(unittest.TestCase):
    def test_jsonc_strings_comments_trailing_commas_and_ambiguity(self):
        value = jsonc(b'{/* block */ "url":"http://example/a,]", "plugin":["other",], // line\n}')
        self.assertEqual(value, {'url': 'http://example/a,]', 'plugin': ['other']})
        for raw in (b'{"plugin":[],"plugin":[]}', b'{/*missing}', b'{"x":NaN}'):
            with self.assertRaises(ValueError):
                jsonc(raw)
