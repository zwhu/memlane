import asyncio
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest
import uuid

from aiohttp import ClientSession, web
from aiohttp.test_utils import TestClient, TestServer

from project_control import Official, Registry, private_write
from project_control_server import create_app
from project_policy import PolicyError


class OfficialFixture:
    def __init__(self):
        self.datasets = {}
        self.acls = {}
        self.creates = 0
        self.fail = False

    async def owned_dataset(self, name, dataset_id=None):
        if self.fail:
            raise PolicyError('official_result_unknown', 503)
        if dataset_id:
            return next(row for row in self.datasets.values() if row['id'] == dataset_id)
        if name not in self.datasets:
            self.creates += 1
            self.datasets[name] = {'name': name, 'id': str(uuid.uuid4())}
        await asyncio.sleep(0)
        return self.datasets[name]

    async def sync_acl(self, agent, dataset, permissions):
        if self.fail:
            raise PolicyError('effective_acl_mismatch', 503)
        self.acls[(agent['principal_id'], dataset)] = set(permissions)

    async def effective_permissions(self, agent, dataset):
        return self.acls.get((agent['principal_id'], dataset), set())


class RegistryTests(unittest.IsolatedAsyncioTestCase):
    async def asyncSetUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.root = Path(self.temporary.name).resolve()
        self.repo = self.root / 'repo'
        self.repo.mkdir()
        subprocess.run(['git', 'init', '-q', str(self.repo)], check=True)
        self.official = OfficialFixture()
        self.path = self.root / 'config/registry.json'
        self.registry = Registry(self.path, self.official)
        self.registry.state['agents'] = {'a': {'name': 'A', 'principal_id': str(uuid.uuid4())},
                                         'b': {'name': 'B', 'principal_id': str(uuid.uuid4())}}
        self.registry.save()

    async def asyncTearDown(self):
        self.registry.close()
        self.temporary.cleanup()

    async def test_concurrent_discovery_subdirectory_and_symlink(self):
        nested = self.repo / 'nested'
        nested.mkdir()
        alias = self.root / 'alias'
        alias.symlink_to(self.repo, target_is_directory=True)
        results = await asyncio.gather(self.registry.resolve('a', str(self.repo), 'one'),
                                       self.registry.resolve('b', str(nested), 'two'),
                                       self.registry.resolve('a', str(alias), 'three'))
        self.assertEqual(len({r['project_id'] for r in results}), 1)
        self.assertEqual(self.official.creates, 1)
        self.assertTrue(all(r['ready'] and not r['code_enabled'] for r in results))
        self.assertEqual(self.official.acls[(''+self.registry.state['agents']['a']['principal_id'], results[0]['dataset_id'])],
                         {'read', 'write', 'delete'})

    async def test_same_name_distinct_paths_and_pinned_session(self):
        another = self.root / 'other/repo'
        another.mkdir(parents=True)
        first = await self.registry.resolve('a', str(self.repo), 'one')
        second = await self.registry.resolve('a', str(another), 'two', explicit=True)
        pinned = await self.registry.resolve('a', str(another), 'one', explicit=True)
        self.assertNotEqual(first['project_id'], second['project_id'])
        self.assertEqual(first['project_id'], pinned['project_id'])

    async def test_defaults_inherit_false_and_reset(self):
        result = await self.registry.resolve('a', str(self.repo), 'one')
        identifier = result['project_id']
        await self.registry.set_policy(self.registry.state['revision'], {'access': 'none', 'capture': False},
                                       project_id=identifier, agent_id='a')
        await self.registry.set_policy(self.registry.state['revision'], {'access': 'read', 'auto_process': False})
        project = self.registry.state['projects'][identifier]
        a = self.registry.policy(project, 'a')
        b = self.registry.policy(project, 'b')
        self.assertEqual(a['policy']['access'], 'none')
        self.assertFalse(a['policy']['capture'])
        self.assertEqual(b['policy']['access'], 'read')
        self.assertFalse(b['policy']['can_capture'])
        await self.registry.set_policy(self.registry.state['revision'], {'access': None, 'capture': None},
                                       project_id=identifier, agent_id='a')
        self.assertEqual(self.registry.policy(project, 'a')['policy'], self.registry.policy(project, 'b')['policy'])

    async def test_unknown_not_retried_by_hooks_and_explicit_reconcile(self):
        self.official.fail = True
        result = await self.registry.resolve('a', str(self.repo), 'one')
        self.assertFalse(result['ready'])
        self.official.fail = False
        again = await self.registry.resolve('a', str(self.repo), 'two')
        self.assertFalse(again['ready'])
        self.assertEqual(self.official.creates, 0)
        await self.registry.reconcile(self.registry.state['revision'], result['project_id'])
        final = await self.registry.resolve('a', str(self.repo), 'one')
        self.assertTrue(final['ready'])
        self.assertEqual(self.official.creates, 1)

    async def test_revision_conflict_and_single_writer_durable_restart(self):
        result = await self.registry.resolve('a', str(self.repo), 'one')
        with self.assertRaises(PolicyError) as error:
            await self.registry.set_policy(-1, {'capture': False})
        self.assertEqual(error.exception.status, 409)
        with self.assertRaises(BlockingIOError):
            Registry(self.path, self.official)
        self.assertEqual(os.stat(self.path).st_mode & 0o777, 0o600)
        self.registry.close()
        self.registry = Registry(self.path, self.official)
        self.assertEqual((await self.registry.resolve('a', str(self.repo), 'one'))['project_id'], result['project_id'])
        self.assertEqual(self.official.creates, 1)

    async def test_manual_and_discovery_share_registry(self):
        project = await self.registry.create_project(self.registry.state['revision'], str(self.repo))
        result = await self.registry.resolve('a', str(self.repo), 'one')
        self.assertEqual(result['project_id'], project['id'])
        self.assertEqual(self.official.creates, 1)
        alias = self.root / 'moved'
        alias.mkdir()
        await self.registry.add_alias(self.registry.state['revision'], project['id'], str(alias))
        self.assertEqual((await self.registry.resolve('a', str(alias), 'two', explicit=True))['project_id'], project['id'])

    async def test_association_preserves_existing_acl(self):
        row = await self.official.owned_dataset('existing')
        self.official.acls[(self.registry.state['agents']['a']['principal_id'], row['id'])] = {'read'}
        project = await self.registry.create_project(self.registry.state['revision'], str(self.repo), dataset_id=row['id'])
        self.assertEqual(self.registry.policy(project, 'a')['policy']['access'], 'read')
        self.assertEqual(self.registry.policy(project, 'b')['policy']['access'], 'none')
        self.assertEqual(self.official.acls[(self.registry.state['agents']['a']['principal_id'], row['id'])], {'read'})

    async def test_http_separates_principals_and_rejects_content(self):
        client = TestClient(TestServer(create_app(self.registry, 'm' * 40, {'a': 'a' * 40, 'b': 'b' * 40})))
        await client.start_server()
        try:
            agent = {'Authorization': 'Bearer ' + 'a' * 40}
            manager = {'Authorization': 'Bearer ' + 'm' * 40}
            self.assertEqual((await client.get('/v1/projects')).status, 401)
            self.assertEqual((await client.get('/v1/projects', headers=agent)).status, 403)
            payload = {'workspace': str(self.repo), 'host_session_key': 'one', 'agent_id': 'b'}
            self.assertEqual((await client.post('/v1/project/resolve', json=payload, headers=agent)).status, 400)
            payload.pop('agent_id')
            response = await client.post('/v1/project/resolve', json=payload, headers=agent)
            self.assertTrue((await response.json())['ready'])
            self.assertEqual((await client.put('/v1/defaults', json={'expected_revision': 1, 'fields': {'capture': False}}, headers=agent)).status, 403)
            self.assertEqual((await client.get('/v1/projects', headers={**manager, 'Origin': 'https://example.com'})).status, 403)
            response = await client.get('/v1/projects', headers=manager)
            self.assertEqual(response.status, 200)
            self.assertNotIn('key_file', json.dumps(await response.json()))
        finally:
            await client.close()


class OfficialContractTests(unittest.IsolatedAsyncioTestCase):
    async def test_effective_role_union_and_exact_acl_http_contract(self):
        owner, child, tenant, role, dataset = [str(uuid.uuid4()) for _ in range(5)]
        grants = {'read', 'write', 'delete', 'share'}
        requests = []
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            private_write(root / 'owner', b'owner-key')
            private_write(root / 'child', b'child-key')

            async def handle(request):
                requests.append((request.method, request.path, request.query_string))
                key = request.headers.get('X-Api-Key')
                if request.path == '/api/v1/users/me':
                    self.assertIn(key, ('owner-key', 'child-key'))
                    return web.json_response({'id': owner if key == 'owner-key' else child,
                                              'parent_user_id': None if key == 'owner-key' else owner})
                if request.path == '/api/v1/permissions/tenants/me':
                    self.assertEqual(key, 'child-key')
                    return web.json_response([{'id': tenant}])
                if request.path == f'/api/v1/permissions/tenants/{tenant}/roles/users/{child}':
                    self.assertEqual(key, 'owner-key')
                    return web.json_response([{'id': role}])
                permission = request.query.get('permission_name')
                if request.path == f'/api/v1/permissions/datasets/{child}':
                    self.assertEqual(key, 'owner-key')
                    self.assertEqual(await request.json(), [dataset])
                    if request.method == 'POST':
                        grants.add(permission)
                    else:
                        self.assertEqual(request.method, 'DELETE')
                        grants.discard(permission)
                    return web.json_response({'message': 'ok'})
                if request.path.startswith('/api/v1/permissions/principals/'):
                    self.assertEqual(key, 'owner-key')
                    principal = request.path.split('/')[-2]
                    actual = (principal == child and permission in grants) or (principal == role and permission == 'write')
                    return web.json_response([{'id': dataset}] if actual else [])
                raise AssertionError(request.path)

            app = web.Application()
            app.router.add_route('*', '/{tail:.*}', handle)
            server = TestServer(app)
            await server.start_server()
            try:
                async with ClientSession() as session:
                    official = Official(session, str(server.make_url('')).rstrip('/'), root / 'owner')
                    agent = {'principal_id': child, 'key_file': str(root / 'child')}
                    await official.sync_acl(agent, dataset, {'read', 'write', 'delete'})
                    self.assertNotIn('share', grants)
                    with self.assertRaises(PolicyError) as error:
                        await official.sync_acl(agent, dataset, set())
                    self.assertEqual(error.exception.code, 'effective_acl_mismatch')
                    self.assertEqual(grants, set())
                    self.assertTrue(any(role in path for _, path, _ in requests))
            finally:
                await server.close()

    async def test_unknown_agent_creation_not_automatically_repeated(self):
        fixture = OfficialFixture()
        attempts = []

        async def unknown(method, path, *args, **kwargs):
            attempts.append((method, path))
            raise PolicyError('official_result_unknown', 503)
        fixture.request = unknown
        with tempfile.TemporaryDirectory() as temporary:
            registry = Registry(Path(temporary) / 'registry.json', fixture)
            try:
                result = await registry.provision_agent(0, 'codex-test', 'Codex Test')
                self.assertEqual(result['enrollments']['codex-test']['status'], 'unconfirmed')
                self.assertFalse(result['agents'])
                with self.assertRaises(PolicyError):
                    await registry.provision_agent(result['revision'], 'codex-test', 'Codex Test')
                self.assertEqual(len(attempts), 1)
                self.assertEqual(attempts[0][0], 'POST')
                self.assertIn('/api/v1/agents/create?name=', attempts[0][1])
            finally:
                registry.close()

    async def test_agent_provision_persists_private_identity_and_hides_credentials(self):
        fixture = OfficialFixture()
        owner, child = str(uuid.uuid4()), str(uuid.uuid4())
        calls = []

        async def official(method, path, body=None, token_file=None):
            calls.append((method, path, body))
            if path.startswith('/api/v1/agents/create?name='):
                self.assertIsNone(body)
                return {'agentId': child, 'agentApiKey': 'child-secret'}
            self.assertEqual(path, '/api/v1/users/me')
            return {'id': child if token_file else owner, 'parent_user_id': owner if token_file else None}
        fixture.request = official
        with tempfile.TemporaryDirectory() as temporary:
            registry = Registry(Path(temporary) / 'registry.json', fixture)
            try:
                directory = await registry.provision_agent(0, 'codex-test', 'Codex Test')
                self.assertEqual(directory['enrollments']['codex-test']['status'], 'verified')
                agent = registry.state['agents']['codex-test']
                self.assertEqual(Path(agent['key_file']).read_text(), 'child-secret')
                self.assertEqual(os.stat(agent['key_file']).st_mode & 0o777, 0o600)
                self.assertEqual(os.stat(agent['control_token_file']).st_mode & 0o777, 0o600)
                self.assertNotIn('secret', json.dumps(directory))
                self.assertNotIn('key_file', json.dumps(directory))
                self.assertNotIn('control_token_file', json.dumps(directory))
                registry.state['agents'].pop('codex-test')
                registry.state['enrollments']['codex-test']['status'] = 'unconfirmed'
                registry.save()
                recovered = await registry.recover_agent(registry.state['revision'], 'codex-test')
                self.assertEqual(recovered['enrollments']['codex-test']['status'], 'verified')
                self.assertEqual(registry.state['agents']['codex-test']['principal_id'], child)
                self.assertEqual(len([path for method, path, _ in calls if method == 'POST']), 1)
            finally:
                registry.close()


if __name__ == '__main__':
    unittest.main()
