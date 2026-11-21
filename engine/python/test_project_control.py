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

    async def retirement_datasets(self, agent):
        return {dataset for (principal, dataset), permissions in self.acls.items()
                if principal == agent['principal_id'] and permissions}


class RegistryTests(unittest.IsolatedAsyncioTestCase):
    async def test_runtime_catalog_is_adopted_once_without_replaying_removed_binding(self):
        dataset = await self.official.owned_dataset('adopted')
        configured = [{'path':str(self.repo), 'dataset_id':dataset['id'], 'name':'Original'}]
        await self.registry.adopt_runtime_projects(configured, ['reserved'])
        self.assertTrue(self.registry.state['runtime_catalog_imported'])
        self.assertEqual(len(self.registry.state['projects']), 1)
        project = next(iter(self.registry.state['projects'].values()))
        await self.registry.rename_project(self.registry.state['revision'], project['id'], 'Renamed')
        await self.registry.adopt_runtime_projects(configured, [])
        self.assertEqual(self.registry.state['projects'][project['id']]['name'], 'Renamed')
        self.assertIn('reserved', self.registry.state['reserved_datasets'])
        self.registry.state['projects'].clear()
        self.registry.save()
        await self.registry.adopt_runtime_projects(configured, [])
        self.assertEqual(self.registry.state['projects'], {})
        self.assertIn('adopted', self.official.datasets)

    async def test_official_client_reads_chunked_json_to_eof(self):
        expected = {'sessions': [{'session_id': str(n), 'padding': 'x' * 1000} for n in range(30)]}
        async def respond(request):
            response = web.StreamResponse(headers={'Content-Type': 'application/json'})
            await response.prepare(request)
            data = json.dumps(expected).encode()
            for offset in range(0, len(data), 4000):
                await response.write(data[offset:offset + 4000])
                await asyncio.sleep(.01)
            await response.write_eof()
            return response
        app = web.Application()
        app.router.add_get('/large', respond)
        server = TestServer(app)
        await server.start_server()
        key = self.root / 'stream-token'
        private_write(key, b'synthetic-stream-token')
        try:
            async with ClientSession() as session:
                official = Official(session, str(server.make_url('/')).rstrip('/'), key)
                self.assertEqual(await official.request('GET', '/large'), expected)
        finally:
            await server.close()

    async def test_retire_agent_revokes_permissions_without_deleting_projects_or_bindings(self):
        bound = await self.registry.resolve('a', str(self.repo), 'keep-old-session')
        await self.registry.ensure_common(self.registry.state['revision'])
        before_sessions = dict(self.registry.state['sessions'])
        datasets = dict(self.official.datasets)
        other_acl = {k: set(v) for k, v in self.official.acls.items()
                     if k[0] == self.registry.state['agents']['b']['principal_id']}
        principal = self.registry.state['agents']['a']['principal_id']
        self.official.acls[(principal, 'legacy-unlinked-dataset')] = {'read', 'write'}
        await self.registry.retire_agent(self.registry.state['revision'], 'a')
        self.assertNotIn('a', {x['id'] for x in self.registry.directory()['agents']})
        self.assertEqual(self.registry.state['archived_agents']['a']['principal_id'], principal)
        self.assertEqual(self.registry.state['sessions'], before_sessions)
        self.assertEqual(self.official.datasets, datasets)
        self.assertTrue(all(not acl for (who, _), acl in self.official.acls.items() if who == principal))
        self.assertEqual(other_acl, {k: v for k, v in self.official.acls.items() if k in other_acl})
        self.assertTrue(self.registry.policy(self.registry.state['projects'][bound['project_id']], 'b')['ready'])
        with self.assertRaisesRegex(PolicyError, 'agent_not_registered'):
            await self.registry.resolve('a', str(self.repo), 'keep-old-session')
        await self.registry.retire_agent(-1, 'a')  # Already complete: no repeated mutation.

    async def test_failed_retirement_denies_host_until_manager_resumes(self):
        await self.registry.resolve('a', str(self.repo), 'old-session')
        self.official.fail = True
        with self.assertRaises(PolicyError):
            await self.registry.retire_agent(self.registry.state['revision'], 'a')
        self.assertIn('a', self.registry.state['agents'])
        self.assertNotIn('a', self.registry.state.get('archived_agents', {}))
        with self.assertRaisesRegex(PolicyError, 'agent_retired'):
            await self.registry.resolve('a', str(self.repo), 'old-session')
        self.official.fail = False
        await self.registry.retire_agent(self.registry.state['revision'], 'a')
        self.assertIn('a', self.registry.state['archived_agents'])

    async def test_retirement_route_is_management_only_and_withdraws_old_control_token(self):
        await self.registry.resolve('a', str(self.repo), 'original')
        client = TestClient(TestServer(create_app(self.registry, 'm' * 40, {'a': 'a' * 40, 'b': 'b' * 40})))
        await client.start_server()
        try:
            path = '/v1/agents/a/retire'
            body = {'expected_revision': self.registry.state['revision']}
            response = await client.post(path, json=body, headers={'Authorization': 'Bearer ' + 'a' * 40})
            self.assertEqual(response.status, 403)
            response = await client.post(path, json=body, headers={'Authorization': 'Bearer ' + 'm' * 40})
            self.assertEqual(response.status, 200)
            response = await client.post('/v1/project/resolve', json={'workspace': str(self.repo), 'host_session_key': 'original'},
                                         headers={'Authorization': 'Bearer ' + 'a' * 40})
            self.assertEqual(response.status, 401)
        finally:
            await client.close()

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

    async def test_authenticated_resolve_records_receipt_without_replaying_session(self):
        self.assertNotIn('connection_receipts', self.registry.state)
        resolved = await self.registry.resolve('a', str(self.repo), 'new-real-host-key')
        receipt = self.registry.state['connection_receipts']['a']
        self.assertEqual(receipt['project_id'], resolved['project_id'])
        self.assertEqual(receipt['evidence'], 'authenticated_host_resolve')
        self.assertGreater(receipt['received_at'], 0)
        before = self.path.read_bytes()
        await self.registry.session_binding('a', 'new-real-host-key')
        self.assertEqual(self.path.read_bytes(), before, 'read-only lookup created a receipt')
        await self.registry.resolve('a', str(self.repo), 'new-real-host-key')
        self.assertEqual(self.official.creates, 1)
        self.assertEqual(len(self.registry.state['sessions']), 1)

    async def test_loaded_environment_receipt_mismatch_never_registers_wrong_project(self):
        profile = self.root / 'real-traex'
        source = self.root / 'fixed-plugin'
        source.mkdir()
        (source / '.memlane-installation.json').write_text(json.dumps({'instance_id': 'a'}))
        (source / '.memlane-project-access.json').write_text(json.dumps({'files': {'fixed': 'digest'}}))
        self.registry.state['agents']['a'].update(host_profile=str(profile), installation={
            'plugin_dir': str(source), 'marketplace_name': 'fixed-marketplace'})
        payload = {'instance_id': 'a', 'plugin_path': str(source), 'plugin_version': 'fixed',
                   'runtime_profile': str(profile)}
        before = len(self.registry.state['projects'])
        with self.assertRaises(PolicyError) as error:
            await self.registry.resolve('a', str(self.repo), 'wrong-profile',
                host_environment={**payload, 'runtime_profile': str(self.root / 'another-profile')})
        self.assertEqual(error.exception.code, 'host_environment_receipt_mismatch')
        self.assertEqual(len(self.registry.state['projects']), before)
        await self.registry.resolve('a', str(self.repo), 'actual-profile', host_environment=payload)
        self.assertTrue(self.registry.state['connection_receipts']['a']['environment_verified'])
        self.assertEqual(self.registry.state['connection_receipts']['a']['plugin_version'], 'fixed')

    async def test_connection_receipt_tracks_recent_sessions_and_bypasses_throttle_after_activation(self):
        self.registry.state['agents']['a']['kind'] = 'antigravity'
        self.registry.state['connections'] = {'a': {'activated_at': 1.0}}
        for idx in range(10):
            self.registry.record_connection_receipt('a', f'proj-{idx % 2}', f'sess-{idx}', {'environment_verified': True})
        receipt = self.registry.state['connection_receipts']['a']
        recent = receipt['recent_sessions']
        self.assertEqual(len(recent), 8)
        self.assertEqual([item['host_session_key'] for item in recent], [f'sess-{i}' for i in range(9, 1, -1)])

        # Stale receipt within 30s (< activated_at) bypasses 30s throttle immediately.
        stale_at = receipt['received_at'] - 5
        receipt['received_at'] = stale_at
        self.registry.state['connections']['a']['activated_at'] = stale_at + 1
        self.registry.record_connection_receipt('a', 'proj-1', 'sess-9', {'environment_verified': True})
        after_receipt = self.registry.state['connection_receipts']['a']
        self.assertGreater(after_receipt['received_at'], stale_at + 1)

        # Repeated receipt >= activated_at within 30s IS throttled.
        throttled_at = after_receipt['received_at']
        self.registry.record_connection_receipt('a', 'proj-1', 'sess-9', {'environment_verified': True})
        self.assertEqual(self.registry.state['connection_receipts']['a']['received_at'], throttled_at)

    async def test_original_session_binding_lookup_never_discovers_and_is_subject_scoped(self):
        before = self.path.read_bytes()
        with self.assertRaises(PolicyError) as error:
            await self.registry.session_binding('a', 'missing')
        self.assertEqual(error.exception.code, 'session_binding_not_found')
        self.assertEqual(self.path.read_bytes(), before)
        self.assertEqual(self.official.creates, 0)
        original = await self.registry.resolve('a', str(self.repo), 'native')
        before = self.path.read_bytes()
        self.assertEqual(await self.registry.session_binding('a', 'native'), original)
        with self.assertRaises(PolicyError):
            await self.registry.session_binding('b', 'native')
        self.assertEqual(self.path.read_bytes(), before)
        key = (self.registry.state['agents']['a']['principal_id'], original['dataset_id'])
        self.official.acls[key] = {'read'}
        checked = await self.registry.session_binding('a', 'native')
        self.assertFalse(checked['ready'])
        self.assertEqual(self.official.acls[key], {'read'}, 'lookup repaired permissions')

    async def test_http_binding_lookup_requires_original_subject_and_exact_query(self):
        original = await self.registry.resolve('a', str(self.repo), 'native')
        client = TestClient(TestServer(create_app(self.registry, 'm' * 40, {'a': 'a' * 40, 'b': 'b' * 40})))
        await client.start_server()
        try:
            path = '/v1/project/binding?host_session_key=native'
            self.assertEqual((await client.get(path)).status, 401)
            self.assertEqual((await client.get(path, headers={'Authorization': 'Bearer ' + 'm' * 40})).status, 403)
            self.assertEqual((await client.get(path, headers={'Authorization': 'Bearer ' + 'b' * 40})).status, 404)
            response = await client.get(path, headers={'Authorization': 'Bearer ' + 'a' * 40})
            self.assertEqual(response.status, 200)
            self.assertEqual((await response.json())['dataset_id'], original['dataset_id'])
            for suffix in ('&agent_id=b', '&host_session_key=other'):
                self.assertEqual((await client.get(path + suffix, headers={'Authorization': 'Bearer ' + 'a' * 40})).status, 400)
        finally:
            await client.close()

    async def test_same_name_distinct_paths_and_pinned_session(self):
        another = self.root / 'other/repo'
        another.mkdir(parents=True)
        first = await self.registry.resolve('a', str(self.repo), 'one')
        second = await self.registry.resolve('a', str(another), 'two', explicit=True)
        pinned = await self.registry.resolve('a', str(another), 'one', explicit=True)
        self.assertNotEqual(first['project_id'], second['project_id'])
        self.assertEqual(first['project_id'], pinned['project_id'])

    async def test_optional_code_group_follows_project_acl_and_defaults_off(self):
        bound = await self.registry.resolve('a', str(self.repo), 'one')
        identifier = bound['project_id']
        self.assertFalse(bound['code']['enabled'])
        code = await self.official.owned_dataset('explicit-code')
        await self.registry.configure_code(self.registry.state['revision'], identifier, code['id'], True, apply=False)
        project = self.registry.state['projects'][identifier]
        self.assertEqual(project['status'], 'unconfirmed')
        self.assertNotIn((self.registry.state['agents']['a']['principal_id'], code['id']), self.official.acls)
        await self.registry.reconcile(self.registry.state['revision'], identifier)
        self.assertTrue(self.registry.policy(project, 'a')['code']['ready'])
        self.assertEqual(self.official.acls[(self.registry.state['agents']['a']['principal_id'], code['id'])], {'read', 'write', 'delete'})
        await self.registry.set_policy(self.registry.state['revision'], {'access': 'read'}, project_id=identifier, agent_id='a')
        self.assertEqual(self.official.acls[(self.registry.state['agents']['a']['principal_id'], code['id'])], {'read'})
        await self.registry.configure_code(self.registry.state['revision'], identifier, code['id'], False)
        self.assertEqual(self.official.acls[(self.registry.state['agents']['a']['principal_id'], code['id'])], set())
        with self.assertRaises(PolicyError):
            await self.registry.configure_code(self.registry.state['revision'], identifier, project['dataset_id'], True)

    async def test_external_acl_change_withdraws_cached_policy_without_regrant(self):
        bound = await self.registry.resolve('a', str(self.repo), 'one')
        await self.registry.ensure_common(self.registry.state['revision'])
        project = self.registry.state['projects'][bound['project_id']]
        agent = self.registry.state['agents']['a']
        before = self.registry.state['revision']
        self.official.acls[(agent['principal_id'], project['dataset_id'])] = set()
        current = await self.registry.checked_policy(project, 'a')
        self.assertFalse(current['ready'])
        self.assertTrue(current['common']['readable'])
        self.assertGreater(current['revision'], before)
        self.assertEqual(self.official.acls[(agent['principal_id'], project['dataset_id'])], set())
        self.official.acls[(agent['principal_id'], project['dataset_id'])] = {'read','write','delete'}
        self.assertFalse((await self.registry.checked_policy(project, 'a'))['ready'])

    async def test_verify_only_recovers_unknown_acl_without_mutating_official_data(self):
        from unittest.mock import AsyncMock, patch
        bound = await self.registry.resolve('a', str(self.repo), 'one')
        project = self.registry.state['projects'][bound['project_id']]
        project['status'] = 'unconfirmed'
        project['error_code'] = 'official_result_unknown'
        project['applied']['a'] = {'status': 'unconfirmed', 'error_code': 'official_result_unknown'}
        self.registry.save()
        before_sessions = dict(self.registry.state['sessions'])
        before_acls = {k: set(v) for k, v in self.official.acls.items()}
        with patch.object(self.official, 'sync_acl', AsyncMock(side_effect=AssertionError('no ACL writes'))), \
                patch.object(self.official, 'owned_dataset', AsyncMock(side_effect=AssertionError('no dataset writes'))):
            client = TestClient(TestServer(create_app(self.registry, 'm' * 40, {'a': 'a' * 40})))
            await client.start_server()
            try:
                payload = {'expected_revision': self.registry.state['revision'], 'verify_only': True}
                route = '/v1/projects/' + project['id'] + '/reconcile'
                denied = await client.post(route, json=payload, headers={'Authorization': 'Bearer ' + 'a' * 40})
                self.assertEqual(denied.status, 403)
                response = await client.post(route, json=payload, headers={'Authorization': 'Bearer ' + 'm' * 40})
                self.assertEqual(response.status, 200, await response.text())
            finally:
                await client.close()
        self.assertTrue(self.registry.policy(project, 'a')['ready'])
        self.assertNotIn('error_code', project)
        self.assertEqual(before_sessions, self.registry.state['sessions'])
        self.assertEqual(before_acls, self.official.acls)

    async def test_verify_only_never_repairs_missing_or_excess_grants(self):
        from unittest.mock import AsyncMock, patch
        bound = await self.registry.resolve('a', str(self.repo), 'one')
        project = self.registry.state['projects'][bound['project_id']]
        agent = self.registry.state['agents']['a']
        for rights in (set(), {'read', 'write', 'delete', 'share'}):
            self.official.acls[(agent['principal_id'], project['dataset_id'])] = rights
            with patch.object(self.official, 'sync_acl', AsyncMock(side_effect=AssertionError('no ACL writes'))):
                await self.registry.reconcile(self.registry.state['revision'], project['id'], verify_only=True)
            self.assertFalse(self.registry.policy(project, 'a')['ready'])
            self.assertEqual(self.official.acls[(agent['principal_id'], project['dataset_id'])], rights)
        with patch.object(self.official, 'effective_permissions', AsyncMock(side_effect=PolicyError('official_result_unknown', 503))), \
                patch.object(self.official, 'sync_acl', AsyncMock(side_effect=AssertionError('no ACL writes'))):
            await self.registry.reconcile(self.registry.state['revision'], project['id'], verify_only=True)
        self.assertFalse(self.registry.policy(project, 'a')['ready'])
        self.assertEqual(project['error_code'], 'official_result_unknown')

    async def test_verify_unconfirmed_groups_recovers_transient_unknown_read_only(self):
        from unittest.mock import AsyncMock, patch
        bound = await self.registry.resolve('a', str(self.repo), 'one')
        await self.registry.ensure_common(self.registry.state['revision'])
        project = self.registry.state['projects'][bound['project_id']]
        common = self.registry.state['common']
        for group in (project, common):
            group['status'] = 'unconfirmed'
            group['error_code'] = 'official_result_unknown'
            group['applied']['a'] = {'status': 'unconfirmed', 'error_code': 'official_result_unknown'}
        unresolved = {
            'id': 'unresolved',
            'name': 'Unresolved',
            'dataset_id': None,
            'status': 'unconfirmed',
            'error_code': 'official_result_unknown',
            'overrides': {},
            'applied': {},
            'desired_revision': 1,
        }
        self.registry.state['projects']['unresolved'] = unresolved
        self.registry.save()

        with patch.object(self.official, 'sync_acl', AsyncMock(side_effect=AssertionError('no ACL writes'))), \
             patch.object(self.official, 'owned_dataset', AsyncMock(side_effect=AssertionError('no dataset writes'))):
            recovered = await self.registry.verify_unconfirmed_groups()
        self.assertEqual(recovered, 2)
        self.assertTrue(self.registry.policy(project, 'a')['ready'])
        self.assertTrue(self.registry.common_policy('a')['ready'])
        self.assertEqual(unresolved['status'], 'unconfirmed')

        # Real ACL drift keeps group unconfirmed without repairing grants.
        self.registry.state['projects'].pop('unresolved', None)
        agent = self.registry.state['agents']['a']
        self.official.acls[(agent['principal_id'], project['dataset_id'])] = {'read'}
        project['status'] = 'unconfirmed'
        project['error_code'] = 'official_result_unknown'
        with patch.object(self.official, 'sync_acl', AsyncMock(side_effect=AssertionError('no ACL writes'))):
            recovered = await self.registry.verify_unconfirmed_groups()
        self.assertEqual(recovered, 0)
        self.assertEqual(project['status'], 'unconfirmed')
        self.assertEqual(project['error_code'], 'effective_acl_changed')

    async def test_manual_origin_is_preserved_when_automatic_hooks_find_it(self):
        created=await self.registry.create_project(self.registry.state['revision'],str(self.repo),name='Manual')
        self.assertEqual(created['source'],'manual')
        bound=await self.registry.resolve('a',str(self.repo),'manual-after')
        self.assertEqual(bound['project_id'],created['id'])
        self.assertEqual(self.registry.state['projects'][created['id']]['source'],'manual')

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
        # Linked projects keep their original source folder; only names may change.
        with self.assertRaises(PolicyError) as error:
            await self.registry.add_alias(self.registry.state['revision'], project['id'], str(alias))
        self.assertEqual(error.exception.code, 'project_binding_locked')
        self.assertEqual(self.registry.state['projects'][project['id']]['aliases'], [str(self.repo)])

    async def test_http_existing_records_link_without_workspace_and_preserve_acl(self):
        import copy
        row = await self.official.owned_dataset('existing-without-folder')
        self.official.acls[(self.registry.state['agents']['a']['principal_id'], row['id'])] = {'read'}
        before_acl = copy.deepcopy(self.official.acls)
        before_creates = self.official.creates
        client = TestClient(TestServer(create_app(self.registry, 'm' * 40, {'a': 'a' * 40})))
        await client.start_server()
        try:
            manager = {'Authorization': 'Bearer ' + 'm' * 40}
            revision = self.registry.state['revision']
            payload = {'expected_revision': revision, 'dataset_id': row['id'], 'name': row['name']}
            response = await client.post('/v1/projects', json=payload, headers=manager)
            self.assertEqual(response.status, 200)
            project = await response.json()
            self.assertEqual(project['aliases'], [])
            self.assertEqual(project['dataset_id'], row['id'])
            self.assertEqual(project['status'], 'verified')
            self.assertEqual(self.registry.policy(project, 'a')['policy']['access'], 'read')
            self.assertEqual(self.registry.policy(project, 'b')['policy']['access'], 'none')
            self.assertEqual(self.official.creates, before_creates)
            for key, value in before_acl.items():
                self.assertEqual(self.official.acls[key], value)
            self.assertFalse(self.official.acls[(self.registry.state['agents']['b']['principal_id'], row['id'])])
            self.assertEqual((await client.post('/v1/projects', json=payload, headers=manager)).status, 409)
            self.assertEqual(len(self.registry.state['projects']), 1)
            no_folder = {'expected_revision': self.registry.state['revision'], 'name': 'new-project'}
            self.assertEqual((await client.post('/v1/projects', json=no_folder, headers=manager)).status, 400)
            self.assertEqual(self.official.creates, before_creates)
            identifier = project['id']
        finally:
            await client.close()
        self.registry.close()
        self.registry = Registry(self.path, self.official)
        self.assertEqual(self.registry.state['projects'][identifier]['aliases'], [])
        self.assertEqual(self.registry.state['projects'][identifier]['dataset_id'], row['id'])

    async def test_association_preserves_existing_acl(self):
        row = await self.official.owned_dataset('existing')
        self.official.acls[(self.registry.state['agents']['a']['principal_id'], row['id'])] = {'read'}
        project = await self.registry.create_project(self.registry.state['revision'], str(self.repo), dataset_id=row['id'])
        self.assertEqual(self.registry.policy(project, 'a')['policy']['access'], 'read')
        self.assertEqual(self.registry.policy(project, 'b')['policy']['access'], 'none')
        self.assertEqual(self.official.acls[(self.registry.state['agents']['a']['principal_id'], row['id'])], {'read'})

    async def test_common_dataset_is_independent_and_read_only_for_agents(self):
        directory = await self.registry.ensure_common(self.registry.state['revision'])
        common = directory['common']
        self.assertEqual(len(directory['projects']), 0)
        self.assertTrue(self.registry.common_policy('a')['readable'])
        self.assertEqual(self.official.acls[(self.registry.state['agents']['a']['principal_id'], common['dataset_id'])], {'read'})
        result = await self.registry.resolve('a', str(self.repo), 'one')
        self.assertNotEqual(result['dataset_id'], common['dataset_id'])
        self.assertEqual(result['common']['dataset_id'], common['dataset_id'])
        again = await self.registry.ensure_common(self.registry.state['revision'])
        self.assertEqual(again['common']['dataset_id'], common['dataset_id'])
        self.assertEqual(self.official.creates, 2)

    async def test_common_and_code_groups_cannot_be_linked_as_projects(self):
        common = (await self.registry.ensure_common(self.registry.state['revision']))['common']
        code = str(uuid.uuid4())
        self.registry.state['reserved_datasets'] = [code]
        for dataset in (common['dataset_id'], code):
            with self.assertRaises(PolicyError) as error:
                await self.registry.create_project(self.registry.state['revision'], str(self.repo), dataset_id=dataset)
            self.assertEqual(error.exception.code, 'non_project_dataset_cannot_be_linked')
        self.assertEqual(self.registry.state['projects'], {})

    async def test_rename_changes_only_display_name_and_survives_reload(self):
        import copy
        project = await self.registry.create_project(self.registry.state['revision'], str(self.repo))
        before = copy.deepcopy(self.registry.state)
        revision = before['revision']
        updated = await self.registry.rename_project(revision, project['id'], '  新名称  ')
        expected = copy.deepcopy(before['projects'][project['id']])
        expected['name'] = '新名称'
        self.assertEqual(updated, expected)
        self.assertEqual(self.registry.state['revision'], revision + 1)
        self.assertEqual(self.registry.state['sessions'], before['sessions'])
        self.assertEqual(self.official.creates, 1)
        self.assertEqual(json.loads(self.path.read_text())['projects'][project['id']], expected)
        with self.assertRaises(PolicyError):
            await self.registry.rename_project(revision, project['id'], 'stale')
        for name in ('', '   ', 'x' * 121, 'a\nb', None):
            with self.assertRaises(PolicyError):
                await self.registry.rename_project(revision + 1, project['id'], name)
        self.assertEqual(self.registry.state['projects'][project['id']], expected)

    async def test_unlinked_record_can_bind_once_only(self):
        project = await self.registry.create_project(self.registry.state['revision'], str(self.repo))
        self.registry.state['projects'][project['id']]['aliases'] = []
        self.registry.save()
        await self.registry.add_alias(self.registry.state['revision'], project['id'], str(self.repo))
        revision = self.registry.state['revision']
        await self.registry.add_alias(revision, project['id'], str(self.repo))
        self.assertEqual(self.registry.state['revision'], revision)
        other = self.root / 'other'
        other.mkdir()
        with self.assertRaises(PolicyError):
            await self.registry.add_alias(revision, project['id'], str(other))

    async def test_http_rename_rejects_bindings_and_agent_identity(self):
        project = await self.registry.create_project(self.registry.state['revision'], str(self.repo))
        client = TestClient(TestServer(create_app(self.registry, 'm' * 40, {'a': 'a' * 40})))
        await client.start_server()
        try:
            route = '/v1/projects/' + project['id'] + '/name'
            payload = {'expected_revision': self.registry.state['revision'], 'name': 'renamed'}
            manager = {'Authorization': 'Bearer ' + 'm' * 40}
            self.assertEqual((await client.put(route, json=payload, headers={'Authorization': 'Bearer ' + 'a' * 40})).status, 403)
            for field in ('workspace', 'dataset_id', 'overrides'):
                self.assertEqual((await client.put(route, json={**payload, field: 'forbidden'}, headers=manager)).status, 400)
            response = await client.put(route, json=payload, headers=manager)
            self.assertEqual(response.status, 200)
            self.assertEqual((await response.json())['name'], 'renamed')
            self.assertEqual((await client.put(route, json=payload, headers=manager)).status, 409)
        finally:
            await client.close()

    async def test_http_separates_principals_and_rejects_content(self):
        client = TestClient(TestServer(create_app(self.registry, 'm' * 40, {'a': 'a' * 40, 'b': 'b' * 40})))
        await client.start_server()
        try:
            agent = {'Authorization': 'Bearer ' + 'a' * 40}
            manager = {'Authorization': 'Bearer ' + 'm' * 40}
            self.assertEqual((await client.get('/v1/projects')).status, 401)
            self.assertEqual((await client.get('/v1/projects', headers=agent)).status, 403)
            for action in ('prepare', 'activate', 'review'):
                response = await client.post('/v1/codex/' + action, json={'expected_revision': 0}, headers=agent)
                self.assertEqual(response.status, 403)
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

    async def test_checked_policy_transient_probe_failure_fails_closed_without_poisoning_state(self):
        from unittest.mock import AsyncMock, patch
        bound = await self.registry.resolve('a', str(self.repo), 'one')
        code = await self.official.owned_dataset('explicit-code')
        await self.registry.configure_code(self.registry.state['revision'], bound['project_id'], code['id'], True)
        await self.registry.ensure_common(self.registry.state['revision'])
        project = self.registry.state['projects'][bound['project_id']]
        common = self.registry.state['common']
        revision_before = self.registry.state['revision']

        for error_code in ('official_result_unknown', 'official_http_503'):
            with patch.object(self.official, 'effective_permissions', AsyncMock(side_effect=PolicyError(error_code, 503))):
                current = await self.registry.checked_policy(project, 'a')
                self.assertFalse(current['ready'])
                self.assertFalse(current['code']['ready'])
                self.assertEqual(current['applied'], {'status': 'unconfirmed', 'error_code': error_code})
                self.assertFalse(current['common']['ready'])
                self.assertFalse(current['common']['readable'])
            self.assertEqual(project['status'], 'verified')
            self.assertNotIn('error_code', project)
            self.assertEqual(project['applied']['a']['status'], 'verified')
            self.assertEqual(common['status'], 'verified')
            self.assertNotIn('error_code', common)
            self.assertEqual(self.registry.state['revision'], revision_before)
            self.assertTrue(self.registry.policy(project, 'b')['ready'])

        recovered = await self.registry.checked_policy(project, 'a')
        self.assertTrue(recovered['ready'])
        self.assertTrue(recovered['code']['ready'])
        self.assertTrue(recovered['common']['ready'])
        self.assertTrue(recovered['common']['readable'])

    async def test_checked_policy_non_transient_probe_error_still_persists_unconfirmed(self):
        from unittest.mock import AsyncMock, patch
        bound = await self.registry.resolve('a', str(self.repo), 'one')
        project = self.registry.state['projects'][bound['project_id']]
        for error_code in ('official_http_401', 'independent_agent_required'):
            revision_before = self.registry.state['revision']
            with patch.object(self.official, 'effective_permissions', AsyncMock(side_effect=PolicyError(error_code, 503))):
                current = await self.registry.checked_policy(project, 'a')
                self.assertFalse(current['ready'])
            self.assertEqual(project['status'], 'unconfirmed')
            self.assertEqual(project['error_code'], error_code)
            self.assertEqual(project['applied']['a'], {'status': 'unconfirmed', 'error_code': error_code})
            self.assertGreater(self.registry.state['revision'], revision_before)
            after = await self.registry.checked_policy(project, 'a')
            self.assertFalse(after['ready'])
            await self.registry.reconcile(self.registry.state['revision'], project['id'])

    async def test_checked_policy_batches_official_effective_permissions_and_handles_transient_503(self):
        owner, child, proj_ds, code_ds, common_ds = [str(uuid.uuid4()) for _ in range(5)]
        grants = {
            proj_ds: {'read', 'write', 'delete'},
            code_ds: {'read', 'write', 'delete'},
            common_ds: {'read'},
        }
        requests = []
        mode = 'ok'
        private_write(self.root / 'owner-key', b'owner-key')
        private_write(self.root / 'child-key', b'child-key')

        async def handle(request):
            requests.append((request.method, request.path, request.query_string))
            if mode == '503':
                return web.Response(status=503, text='busy')
            if mode == 'unknown':
                return web.Response(status=200, text='truncated')
            key = request.headers.get('X-Api-Key')
            if request.path == '/api/v1/users/me':
                return web.json_response({'id': owner if key == 'owner-key' else child,
                                          'parent_user_id': None if key == 'owner-key' else owner})
            if request.path == '/api/v1/permissions/tenants/me':
                return web.json_response([])
            if request.path.startswith('/api/v1/permissions/principals/'):
                perm = request.query.get('permission_name')
                return web.json_response([{'id': ds} for ds, perms in grants.items() if perm in perms])
            raise AssertionError(request.path)

        app = web.Application()
        app.router.add_route('*', '/{tail:.*}', handle)
        server = TestServer(app)
        await server.start_server()
        try:
            async with ClientSession() as session:
                official = Official(session, str(server.make_url('')).rstrip('/'), self.root / 'owner-key')
                registry = Registry(self.root / 'batch-registry.json', official)
                try:
                    registry.state['agents'] = {
                        'a': {'name': 'A', 'principal_id': child, 'key_file': str(self.root / 'child-key')},
                    }
                    registry.state['revision'] = 10
                    registry.state['projects'] = {
                        'p1': {
                            'id': 'p1', 'name': 'repo', 'aliases': [str(self.repo)],
                            'dataset_id': proj_ds, 'dataset_name': 'ml-project-p1',
                            'code_configured': True, 'code_enabled': True, 'code_dataset_id': code_ds,
                            'overrides': {}, 'desired_revision': 10, 'status': 'verified',
                            'applied': {'a': {'status': 'verified', 'revision': 10}},
                        },
                    }
                    registry.state['common'] = {
                        'id': 'c1', 'kind': 'common', 'name': '通用记忆',
                        'dataset_id': common_ds, 'dataset_name': 'ml-common-c1',
                        'overrides': {}, 'desired_revision': 10, 'status': 'verified', 'code_enabled': False,
                        'applied': {'a': {'status': 'verified', 'revision': 10}},
                    }
                    registry.save()
                    project = registry.state['projects']['p1']
                    common = registry.state['common']

                    ok_res = await registry.checked_policy(project, 'a')
                    self.assertEqual(len(requests), 7)
                    self.assertTrue(ok_res['ready'])
                    self.assertTrue(ok_res['code']['ready'])
                    self.assertTrue(ok_res['common']['readable'])

                    for transient_mode, expected_err in (('503', 'official_http_503'), ('unknown', 'official_result_unknown')):
                        mode = transient_mode
                        failed = await registry.checked_policy(project, 'a')
                        self.assertFalse(failed['ready'])
                        self.assertFalse(failed['code']['ready'])
                        self.assertEqual(failed['applied'], {'status': 'unconfirmed', 'error_code': expected_err})
                        self.assertFalse(failed['common']['ready'])
                        self.assertFalse(failed['common']['readable'])
                        self.assertEqual(project['status'], 'verified')
                        self.assertEqual(common['status'], 'verified')
                        self.assertEqual(registry.state['revision'], 10)

                    mode = 'ok'
                    healed = await registry.checked_policy(project, 'a')
                    self.assertTrue(healed['ready'])
                    self.assertTrue(healed['code']['ready'])
                    self.assertTrue(healed['common']['readable'])

                    grants[proj_ds] = {'read'}
                    drifted = await registry.checked_policy(project, 'a')
                    self.assertFalse(drifted['ready'])
                    self.assertEqual(project['status'], 'unconfirmed')
                    self.assertEqual(project['error_code'], 'effective_acl_changed')
                    self.assertEqual(common['status'], 'verified')
                    self.assertTrue(drifted['common']['readable'])
                finally:
                    registry.close()
        finally:
            await server.close()


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
                    # Official self-query avoids requiring the management user
                    # to have a current tenant just to inspect its child.
                    if key != 'child-key':
                        return web.json_response({'detail': 'TenantNotFoundError'}, status=404)
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

    async def test_computer_level_provision_grants_no_project_or_common_acl_and_protects_profiles(self):
        from unittest.mock import AsyncMock, patch
        fixture = OfficialFixture()
        owner, existing_child, new_child = str(uuid.uuid4()), str(uuid.uuid4()), str(uuid.uuid4())
        key_map = {}

        async def official(method, path, body=None, token_file=None):
            if path.startswith('/api/v1/agents/create?name='):
                key = 'new-child-secret-token-' + uuid.uuid4().hex
                key_map[key] = new_child
                return {'agentId': new_child, 'agentApiKey': key}
            if path == '/api/v1/users/me':
                if token_file:
                    secret = Path(token_file).read_text().strip()
                    return {'id': key_map[secret], 'parent_user_id': owner}
                return {'id': owner, 'parent_user_id': None}
            raise AssertionError((method, path))

        fixture.request = official
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary).resolve()
            repo1 = root / 'repo1'
            repo2 = root / 'repo2'
            profiles_dir = root / 'profiles-repo'
            for d in (repo1, repo2, profiles_dir):
                d.mkdir()
                subprocess.run(['git', 'init', '-q', str(d)], check=True)
            registry = Registry(root / 'registry.json', fixture)
            try:
                registry.state['agents']['existing'] = {'name': 'Existing', 'principal_id': existing_child}
                registry.save()
                common_dir = await registry.ensure_common(registry.state['revision'])
                self.assertEqual(common_dir['common']['name'], '通用记忆')
                proj1 = await registry.create_project(registry.state['revision'], str(repo1), name='Repo1')
                profiles_proj = await registry.create_project(registry.state['revision'], str(profiles_dir), name='ml-user-profiles')

                # Protected ml-user-profiles defaults to access='none' for ALL existing hosts too!
                self.assertEqual(registry.policy(registry.state['projects'][profiles_proj['id']], 'existing')['policy']['access'], 'none')
                self.assertEqual(fixture.acls.get((existing_child, profiles_proj['dataset_id']), set()), set())

                # Computer-level provision_agent MUST NOT call sync_acl or grant any project/common ACL!
                with patch.object(fixture, 'sync_acl', AsyncMock(side_effect=AssertionError('provision must not write ACLs'))):
                    await registry.provision_agent(registry.state['revision'], 'pi-new', '本机 pi')

                self.assertEqual(fixture.acls.get((new_child, proj1['dataset_id']), set()), set())
                self.assertEqual(fixture.acls.get((new_child, profiles_proj['dataset_id']), set()), set())
                self.assertEqual(fixture.acls.get((new_child, common_dir['common']['dataset_id']), set()), set())
                self.assertEqual(registry.policy(registry.state['projects'][proj1['id']], 'pi-new')['policy']['access'], 'none')
                self.assertTrue(registry.policy(registry.state['projects'][proj1['id']], 'pi-new')['ready'])
                self.assertEqual(registry.policy(registry.state['projects'][profiles_proj['id']], 'pi-new')['policy']['access'], 'none')
                self.assertFalse(registry.common_policy('pi-new')['readable'])

                # New project created after provision still keeps ungranted agent at access='none'.
                proj2 = await registry.create_project(registry.state['revision'], str(repo2), name='Repo2')
                self.assertEqual(registry.policy(registry.state['projects'][proj2['id']], 'pi-new')['policy']['access'], 'none')
                self.assertEqual(fixture.acls.get((new_child, proj2['dataset_id']), set()), set())

                # Explicit project-level authorization grants ONLY that project.
                await registry.set_policy(registry.state['revision'], {'access': 'maintain'},
                                          project_id=proj1['id'], agent_id='pi-new')
                self.assertEqual(fixture.acls[(new_child, proj1['dataset_id'])], {'read', 'write', 'delete'})
                self.assertEqual(fixture.acls.get((new_child, proj2['dataset_id']), set()), set())
                self.assertEqual(fixture.acls.get((new_child, profiles_proj['dataset_id']), set()), set())
                self.assertFalse(registry.common_policy('pi-new')['readable'])

                # Explicit common authorization grants read-only (rejecting maintain).
                with self.assertRaisesRegex(PolicyError, 'common_read_only'):
                    await registry.set_policy(registry.state['revision'], {'access': 'maintain'},
                                              project_id=common_dir['common']['id'], agent_id='pi-new')
                await registry.set_policy(registry.state['revision'], {'access': 'read'},
                                          project_id=common_dir['common']['id'], agent_id='pi-new')
                self.assertEqual(fixture.acls[(new_child, common_dir['common']['dataset_id'])], {'read'})
                self.assertTrue(registry.common_policy('pi-new')['readable'])
            finally:
                registry.close()


if __name__ == '__main__':
    unittest.main()
