"""TRAE repair and phased operations through the management HTTP boundary."""
import copy
import json
import os
import stat
from pathlib import Path
import sys
import tempfile
import unittest
from unittest.mock import AsyncMock, patch

from aiohttp.test_utils import make_mocked_request

from agent_connections import Connections, sha
from project_control import Registry, private_write
from project_control_server import create_app
from project_policy import PolicyError
from test_trae_ide_connection import beta2_hooks
from trae_hooks_migration import apply_trae_hooks_repair, inspect_trae_hooks_repair
from trae_ide_connection import EVENTS, UNSUPPORTED_BETA2_EVENTS, proposal


class ConnectionReviewTests(unittest.IsolatedAsyncioTestCase):
    async def asyncSetUp(self):
        self.root = Path(self.enterContext(tempfile.TemporaryDirectory())).resolve()
        self.registry = Registry(self.root / 'projects.json', None)
        self.addCleanup(lambda: self.registry.close())
        self.profile = self.root / 'trae-cn'
        self.profile.mkdir()
        (self.root / 'pi').mkdir()
        (self.root / 'codex').mkdir()
        (self.root / 'opencode').mkdir()
        self.pi_executable = self.root / 'pi-executable'
        self.pi_executable.touch(mode=0o700)
        self.config = {'python': sys.executable, 'agent_hosts': {
            'trae_ide': {'profile': str(self.profile), 'executable': sys.executable},
            'pi': {'profile': str(self.root / 'pi'), 'executable': str(self.pi_executable)},
            'opencode': {'profile': str(self.root / 'opencode'), 'executable': sys.executable},
            'codex': {'profile': str(self.root / 'codex'), 'executable': sys.executable},
        }}
        self.connections = Connections(self.registry, self.config)
        self.adapter = next(adapter for adapter in self.connections.adapters.values()
                            if getattr(adapter, 'kind', None) == 'trae_ide')
        self.identity = self.adapter.id
        self.target = self.adapter.target
        self.app = create_app(self.registry, 'm' * 40, {'agent': 'a' * 40}, codex_config=self.config)
        self.app.freeze()
        self.addAsyncCleanup(self.app.cleanup)

    async def request(self, method, path, payload=None, *, agent=False):
        headers = {'Authorization': 'Bearer ' + ('a' if agent else 'm') * 40}
        if payload is not None:
            headers['Content-Type'] = 'application/json'
        request = make_mocked_request(method, path, headers=headers, app=self.app)
        with patch.object(type(request), 'json', new=AsyncMock(return_value=payload)):
            response = await self.app._handle(request)
        return response.status, json.loads(response.body)

    def paths(self):
        return {path.relative_to(self.root): (path.read_bytes(), path.stat().st_mtime_ns)
                for path in self.root.rglob('*') if path.is_file()}

    def orphan(self):
        plugin = (self.root / 'managed-marketplaces' / self.identity / ('a' * 20) / 'plugins/cognee')
        old = beta2_hooks({'plugin_dir': str(plugin)}, sys.executable)
        user = {'hooks': [{'command': 'echo trae_ide_hook.py'}]}
        raw = proposal(json.dumps({'hooks': {'Stop': [user], 'Custom': [user]}}), None, old)
        self.target.write_bytes(raw)
        return raw, user

    def active_beta2(self):
        plugin = self.root / 'legacy-plugin'
        script = b'# Synthetic TRAE hook'
        private_write(plugin / 'scripts/trae_ide_hook.py', script)
        manifest = json.dumps({'files': {'scripts/trae_ide_hook.py': sha(script)}}).encode()
        private_write(plugin / '.memlane-project-access.json', manifest)
        token = self.root / 'control.token'
        installation = {'plugin_dir': str(plugin), 'home': str(self.root),
                        'env_file': str(self.root / 'agent.env'), 'manifest_sha256': sha(manifest)}
        private_write(plugin / '.memlane-installation.json', json.dumps({
            'instance_id': self.identity, 'host_kind': 'trae_ide', 'env_file': installation['env_file'],
            'runtime_profile': str(self.profile), 'hook_origin': 'http://127.0.0.1:12345',
            'hook_token_file': str(token)}))
        self.registry.state['agents'][self.identity] = {'installation': installation, 'control_token_file': str(token)}
        hooks = beta2_hooks(installation, sys.executable)
        raw = proposal(b'', None, hooks)
        self.target.write_bytes(raw)
        self.registry.state['connections'][self.identity] = dict(
            status='configured_pending_new_session', activated=True, trae_ide_hooks=hooks, trae_ide_next_hooks=hooks,
            trae_ide_proposal_sha256=sha(raw))
        self.registry.save()
        return raw, installation

    async def submit_repair(self, operation):
        endpoint = f'/v1/connections/{self.identity}/repair-trae-hooks'
        status, plan = await self.request('POST', endpoint, {'dry_run': True})
        self.assertEqual(status, 200)
        self.assertEqual(plan['revision'], self.registry.state['revision'])
        self.assertTrue(plan['repairable'], plan)
        return await self.request('POST', endpoint, {'expected_revision': self.registry.state['revision'],
                                                     'operation_id': operation, 'reviewed_sha': plan['reviewed_sha']})

    async def review_repair(self, operation):
        return await self.request('POST', f'/v1/connections/{self.identity}/review', {
            'expected_revision': self.registry.state['revision'], 'operation_id': 'review-' + operation})

    def disk_connection(self):
        return json.loads(self.registry.path.read_bytes())['connections'].get(self.identity, {})

    async def test_live_change_after_planning_is_rejected_before_journaling(self):
        self.orphan()
        _, raw_plan = await self.request('POST', f'/v1/connections/{self.identity}/repair-trae-hooks', {'dry_run': True})
        from trae_hooks_migration import _plan

        def plan_then_edit(*args):
            plan = _plan(*args)
            self.target.write_bytes(b'{"hooks":{}}')
            return plan

        with (patch('trae_hooks_migration._plan', side_effect=plan_then_edit),
              patch('trae_hooks_migration._backup') as backup):
            result = await self.request('POST', f'/v1/connections/{self.identity}/repair-trae-hooks', {
                'expected_revision': 0, 'operation_id': 'changed-baseline', 'reviewed_sha': raw_plan['reviewed_sha']})
        self.assertEqual(result, (409, {'error': 'trae_ide_repair_preview_changed', 'phase': 'pre_mutation'}))
        backup.assert_not_called()
        self.assertNotIn('trae_ide_repair_journal', self.disk_connection())
        self.assertEqual(self.target.read_bytes(), b'{"hooks":{}}')

    async def test_backup_and_roll_forward_failures_leave_durable_reviewable_journal(self):
        raw, old = self.active_beta2()
        for step in ('_backup', 'prepare'):
            operation = 'failed-' + step
            with self.subTest(step=step):
                if step == 'prepare':
                    source = self.root / 'managed'
                    private_write(source / '.memlane-project-access.json', '{}')
                    self.config.update(plugin_dir=str(source), project_control_url='http://127.0.0.1:12345')
                    planned = {**old, 'plugin_dir': str(self.root / 'next-plugin'), 'manifest_sha256': 'next-manifest'}
                else:
                    planned = None

                def fail(*args, **kwargs):
                    journal = self.disk_connection()['trae_ide_repair_journal']
                    sqlite_ops = self.registry.sessions_store.load_all()[1]
                    self.assertEqual(sqlite_ops[operation]['phase'], 'post_mutation')
                    self.assertEqual(journal, sqlite_ops[operation]['repair_journal'])
                    self.assertEqual(journal['target_installation'], old)
                    self.assertEqual(journal['backup_path'], '')
                    self.assertEqual(journal['target_proposed_sha256'],
                                     sha(((planned or old)['manifest_sha256'] + journal['target_proposal_sha256']).encode()))
                    raise OSError('synthetic stage failure')

                with (patch('trae_hooks_migration.plan_trae_ide_installation', return_value=planned),
                      patch('trae_hooks_migration.' + step, side_effect=fail) as failed):
                    result = await self.submit_repair(operation)
                failed.assert_called_once()
                self.assertEqual(result, (503, {'error': 'trae_ide_hook_repair_result_unknown', 'phase': 'post_mutation'}))
                self.assertEqual(self.target.read_bytes(), raw)
                self.assertEqual(self.adapter.view()['status'], 'activation_unknown')
                self.assertEqual((await self.review_repair(operation))[0], 200)
                self.assertEqual(self.target.read_bytes(), raw)
                self.assertNotIn('trae_ide_repair_journal', self.disk_connection())
                _, receipt = await self.request('GET', f'/v1/connections/operations/{operation}')
                self.assertEqual((receipt['status'], receipt['outcome'], receipt['phase'], receipt['error_code']),
                                 ('complete', 'rejected', 'pre_mutation', 'trae_ide_repair_did_not_apply'))

    async def test_review_recovers_journal_written_before_sqlite_receipt(self):
        class SimulatedCrash(BaseException):
            pass

        raw, _ = self.orphan()
        operation = 'journal-before-receipt'
        plan = inspect_trae_hooks_repair(self.registry, self.adapter, self.config)
        with patch('trae_hooks_migration.persist_operation', side_effect=SimulatedCrash):
            with self.assertRaises(SimulatedCrash):
                apply_trae_hooks_repair(self.registry, self.adapter, self.config, expected_revision=0,
                                        operation_id=operation, reviewed_sha=plan['reviewed_sha'])
        self.assertEqual(self.disk_connection()['trae_ide_repair_journal']['operation_id'], operation)
        self.assertNotIn(operation, self.registry.sessions_store.load_all()[1])
        self.assertEqual(await self.request('GET', f'/v1/connections/operations/{operation}'),
                         (200, dict(recorded=True, operation_id=operation, instance_id=self.identity,
                                    action='repair-trae-hooks', status='unknown', phase='post_mutation',
                                    result=None, error_code=None)))
        self.assertEqual(await self.request('POST', f'/v1/connections/operations/{operation}/reconcile',
                                            {'dry_run': True, 'action': 'activate'}),
                         (409, {'error': 'operation_id_conflict', 'phase': 'post_mutation'}))
        with self.assertRaisesRegex(PolicyError, 'connection_operation_requires_review'):
            apply_trae_hooks_repair(self.registry, self.adapter, self.config, expected_revision=0,
                                    operation_id=operation, reviewed_sha=plan['reviewed_sha'])
        self.assertEqual((await self.review_repair(operation))[0], 200)
        self.assertEqual(self.target.read_bytes(), raw)
        self.assertNotIn('trae_ide_repair_journal', self.disk_connection())
        self.assertEqual(self.registry.sessions_store.load_all()[1][operation]['error_code'],
                         'trae_ide_repair_did_not_apply')

    async def test_active_four_event_hooks_are_unknown_until_review_completes(self):
        self.active_beta2()
        operation = 'active-interrupted'
        with patch('trae_hooks_migration._finish_repair', side_effect=OSError('synthetic interruption')):
            self.assertEqual((await self.submit_repair(operation))[0], 503)
        self.assertEqual(set(json.loads(self.target.read_bytes())['hooks']), set(EVENTS))
        self.assertEqual(self.adapter.view()['status'], 'activation_unknown')
        with patch('project_codex_connection.legacy_consumers', return_value=[]):
            status, response = await self.request('GET', '/v1/connections')
        self.assertEqual(status, 200)
        row = next(row for row in response['connections'] if row['instance_id'] == self.identity)
        self.assertEqual(row['status'], 'activation_unknown')
        status, preview = await self.request('GET', f'/v1/connections/{self.identity}/preview')
        self.assertEqual((status, preview['status'], preview['ok']), (200, 'activation_unknown', False))
        self.assertIn('connection_result_requires_review', preview['blockers'])
        with self.assertRaisesRegex(PolicyError, 'connection_result_requires_review'):
            await self.adapter.prepare(self.registry.state['revision'])
        self.assertEqual((await self.review_repair(operation))[0], 200)
        self.assertEqual(self.adapter.view()['status'], 'configured_pending_new_session')
        self.assertNotIn('trae_ide_repair_journal', self.disk_connection())
        self.assertEqual(self.registry.sessions_store.load_all()[1][operation]['status'], 'complete')

    async def test_post_rename_directory_fsync_failure_preserves_complete_repair(self):
        self.active_beta2()
        operation = 'committed-before-fsync-error'
        original_fsync = os.fsync
        revision = self.registry.state['revision']
        failed = False

        def fail_after_registry_replace(fd):
            nonlocal failed
            if (not failed and stat.S_ISDIR(os.fstat(fd).st_mode)
                    and json.loads(self.registry.path.read_bytes())['revision'] == revision + 1):
                self.assertNotIn('trae_ide_repair_journal', self.disk_connection())
                failed = True
                raise OSError('synthetic directory fsync failure')
            return original_fsync(fd)

        with patch('_runtime_common.os.fsync', side_effect=fail_after_registry_replace):
            result = await self.submit_repair(operation)
        self.assertTrue(failed)
        self.assertEqual(result, (503, {'error': 'trae_ide_hook_repair_result_unknown', 'phase': 'post_mutation'}))
        self.assertEqual(self.registry.state['revision'], revision + 1)
        self.assertNotIn('trae_ide_repair_journal', self.registry.state['connections'][self.identity])
        self.assertEqual(self.registry.state['connections'][self.identity], self.disk_connection())
        self.assertEqual(self.adapter.view()['status'], 'configured_pending_new_session')
        self.assertEqual(self.registry.sessions_store.load_all()[1][operation]['status'], 'complete')
        status, receipt = await self.request('GET', f'/v1/connections/operations/{operation}')
        self.assertEqual((status, receipt['status'], receipt['result']['repaired']), (200, 'complete', True))

    async def test_complete_operation_conflict_preserves_explicit_phase(self):
        operations = self.registry.state.setdefault('connection_operations', {})
        for index, fields in enumerate(({'phase': 'post_mutation'}, {})):
            operation = f'completed-{index}'
            operations[operation] = {'status': 'complete', 'signature': 'different-signature', **fields}
            response = await self.request('POST', f'/v1/connections/{self.identity}/repair-trae-hooks', {
                'expected_revision': 0, 'operation_id': operation, 'reviewed_sha': 'new-review'})
            self.assertEqual(response, (409, {'error': 'operation_id_conflict',
                                               'phase': fields.get('phase', 'pre_mutation')}))

    async def test_preview_reports_exact_prepare_preflight_blockers(self):
        identifiers = {getattr(adapter, 'kind', 'codex'): adapter.id
                       for adapter in self.connections.adapters.values()}

        async def expect(kind, code, error_status):
            state, before = copy.deepcopy(self.registry.state), self.paths()
            status, preview = await self.request('GET', f'/v1/connections/{identifiers[kind]}/preview')
            self.assertEqual(status, 200)
            self.assertFalse(preview['ok'])
            self.assertEqual(preview['blockers'], [code])
            self.assertEqual(preview['blocker_details'], [
                {'code': code, 'status': error_status, 'phase': 'pre_mutation'}])
            self.assertEqual(preview['proposed_sha256'], '')
            self.assertEqual(self.paths(), before)
            self.assertEqual(self.registry.state, state)

        await expect('pi', 'managed_plugin_input_mismatch', 503)
        await expect('codex', 'managed_plugin_input_mismatch', 503)
        await expect('opencode', 'opencode_component_required', 503)

        managed = self.root / 'managed-source'
        private_write(managed / '.memlane-project-access.json', '{}')
        self.config.update(plugin_dir=str(managed), python='relative-python')
        await expect('pi', 'host_helper_python_required', 400)

        self.config.update(python=sys.executable, agent_host_home='relative-home')
        await expect('pi', 'absolute_host_home_required', 400)
        await expect('codex', 'absolute_host_home_required', 400)

        self.pi_executable.unlink()
        await expect('pi', 'host_executable_unavailable', 503)
        for kind, table, state, code in (
                ('codex', 'codex_connections', 'staging_unknown', 'codex_connection_result_requires_review'),
                ('opencode', 'connections', 'preparation_unknown', 'connection_result_requires_review'),
                ('pi', 'connections', 'activation_unknown', 'connection_result_requires_review')):
            with self.subTest(kind=kind):
                self.registry.state.setdefault(table, {})[identifiers[kind]] = {'status': state}
                await expect(kind, code, 409)
                self.registry.state[table].pop(identifiers[kind])

        for kind in ('codex', 'opencode'):
            with self.subTest(kind=kind):
                self.registry.state.setdefault('enrollments', {})[identifiers[kind]] = {'status': 'unconfirmed'}
                await expect(kind, 'agent_creation_requires_review', 409)

    async def test_readonly_preview_covers_all_adapter_kinds(self):
        before = self.paths()
        state = copy.deepcopy(self.registry.state)
        previews = []
        for identifier in self.connections.adapters:
            status, preview = await self.request('GET', f'/v1/connections/{identifier}/preview')
            self.assertEqual(status, 200)
            self.assertEqual(preview['instance_id'], identifier)
            self.assertEqual(preview['revision'], state['revision'])
            self.assertIsInstance(preview['diff_summary'], str)
            self.assertIsInstance(preview['summary'], str)
            self.assertIsInstance(preview['backup_plan'], str)
            self.assertIsInstance(preview['blockers'], list)
            self.assertTrue(all(isinstance(code, str) for code in preview['blockers']))
            self.assertEqual(preview['connection']['instance_id'], identifier)
            self.assertIn('reviewed_sha', preview)
            previews.append(preview['kind'])
        self.assertCountEqual(previews, ('codex', 'pi', 'traex', 'trae_ide', 'claude_code',
                                         'cursor', 'antigravity', 'opencode'))
        self.assertEqual(self.paths(), before)
        self.assertEqual(self.registry.state, state)

    async def test_orphan_preview_is_read_only_and_confirmation_is_idempotent(self):
        raw, user = self.orphan()
        before = self.paths()
        state = copy.deepcopy(self.registry.state)
        endpoint = f'/v1/connections/{self.identity}'
        self.assertEqual((await self.request('GET', endpoint + '/preview', agent=True))[0], 403)
        with patch('project_control_server.CachedSecretReader.read') as secrets:
            status, preview = await self.request('GET', endpoint + '/preview')
            secrets.assert_not_called()
        self.assertEqual(status, 200)
        self.assertTrue(preview['ok'])
        self.assertTrue(preview['needs_hook_repair'])
        self.assertNotIn('command', json.dumps(preview))
        repair = endpoint + '/repair-trae-hooks'
        status, dry = await self.request('POST', repair, {'dry_run': True, 'expected_revision': preview['revision'], 'operation_id': 'repair-orphan'})
        self.assertEqual(status, 200)
        self.assertEqual(preview['reviewed_sha'], dry['reviewed_sha'])
        self.assertEqual(dry['preserved_third_party_groups'], 2)
        self.assertEqual(before, self.paths())
        self.assertEqual(state, self.registry.state)
        payload = {'expected_revision': preview['revision'], 'operation_id': 'repair-orphan',
                   'proposed_sha256': preview['proposed_sha256']}
        status, result = await self.request('POST', repair, payload)
        self.assertEqual(status, 200)
        self.assertEqual(result['mode'], 'orphan_cleanup')
        self.assertEqual(result['preserved_third_party_groups'], 2)
        self.assertEqual(result['operation_id'], payload['operation_id'])
        self.assertEqual(result['reviewed_sha'], preview['reviewed_sha'])
        self.assertEqual(result['proposed_sha256'], preview['proposed_sha256'])
        self.assertEqual(result['summary'], preview['repair_summary'])
        self.assertEqual(result['removed_events'], list(UNSUPPORTED_BETA2_EVENTS))
        self.assertEqual(Path(result['backup_path']).read_bytes(), raw)
        self.assertEqual(json.loads(self.target.read_bytes())['hooks'], {'Stop': [user], 'Custom': [user]})
        self.assertEqual(await self.request('POST', repair, payload), (200, result))
        payload['proposed_sha256'] = 'different'
        self.assertEqual(await self.request('POST', repair, payload), (409, {'error': 'operation_id_conflict', 'phase': 'pre_mutation'}))

    async def test_recover_legacy_repair_journal_without_preserved_group_count(self):
        self.orphan()
        operation = 'legacy-repair'
        repair = f'/v1/connections/{self.identity}/repair-trae-hooks'
        status, plan = await self.request('POST', repair, {'dry_run': True})
        self.assertEqual(status, 200)
        payload = {'expected_revision': plan['revision'], 'operation_id': operation, 'reviewed_sha': plan['reviewed_sha']}
        with patch('trae_hooks_migration._finish_repair', side_effect=OSError('synthetic interruption')):
            self.assertEqual((await self.request('POST', repair, payload))[0], 503)
        journal = self.registry.state['connections'][self.identity]['trae_ide_repair_journal']
        self.assertEqual(journal.pop('preserved_third_party_groups'), 2)
        self.registry.save()
        self.assertEqual((await self.review_repair(operation))[0], 200)
        status, result = await self.request('POST', repair, payload)
        self.assertEqual(status, 200)
        self.assertEqual(result['preserved_third_party_groups'], 0)

    async def test_empty_legacy_keys_require_current_revision_and_reviewed_hash(self):
        repair = f'/v1/connections/{self.identity}/repair-trae-hooks'
        self.target.write_text('{"hooks":{"PostToolUseFailure":[],"SessionEnd":[]}}')
        _, preview = await self.request('POST', repair, {'dry_run': True})
        self.assertTrue(preview['repairable'])
        self.assertEqual(preview['removed_events'], list(UNSUPPORTED_BETA2_EVENTS))
        raw = self.target.read_bytes()
        for op, revision, code in (('stale', 1, 'revision_conflict'), ('changed', 0, 'trae_ide_repair_preview_changed')):
            status, response = await self.request('POST', repair,
                                                  {'expected_revision': revision, 'operation_id': op, 'reviewed_sha': 'wrong'})
            self.assertEqual((status, response), (409, {'error': code, 'phase': 'pre_mutation'}))
            _, receipt = await self.request('GET', f'/v1/connections/operations/{op}')
            self.assertEqual((receipt['status'], receipt['outcome']), ('complete', 'rejected'))
            self.assertEqual(self.target.read_bytes(), raw)
        payload = {'expected_revision': 0, 'operation_id': 'correct', 'reviewed_sha': preview['reviewed_sha']}
        self.assertEqual((await self.request('POST', repair, payload))[0], 200)
        self.assertEqual(json.loads(self.target.read_bytes())['hooks'], {})
        payload.update(expected_revision=1, operation_id='unnecessary')
        self.assertEqual(await self.request('POST', repair, payload),
                         (409, {'error': 'trae_ide_hook_repair_not_needed', 'phase': 'pre_mutation'}))

    async def test_repair_refuses_third_party_unsupported_and_ambiguous_states(self):
        raw, user = self.orphan()
        for scenario, expected in (
            ('third_party', 'trae_ide_third_party_unsupported_hook_requires_review'),
            ('tampered', 'trae_ide_hook_changed_requires_review'),
            ('activation_unknown', 'connection_result_requires_review'),
            ('wrong_version', 'trae_ide_configuration_requires_review'),
            ('symlink', 'host_configuration_requires_review'),
        ):
            with self.subTest(scenario=scenario):
                self.registry.state['connections'].clear()
                config = json.loads(raw)
                if scenario == 'third_party':
                    config['hooks']['SessionEnd'].append(user)
                elif scenario == 'tampered':
                    config['hooks']['Stop'][-1]['hooks'][0]['timeout'] = 10
                elif scenario == 'activation_unknown':
                    self.registry.state['connections'][self.identity] = {'status': scenario}
                elif scenario == 'wrong_version':
                    config['version'] = True
                else:
                    self.registry.state['connections'][self.identity] = {
                        'trae_ide_hooks': {event: groups[-1] for event, groups in config['hooks'].items()}}
                    self.target.unlink()
                    source = self.target.with_name('source.json')
                    source.write_bytes(raw)
                    self.target.symlink_to(source)
                if scenario != 'symlink':
                    self.target.write_text(json.dumps(config))
                before = self.target.read_bytes()
                plan = inspect_trae_hooks_repair(self.registry, self.adapter, self.config)
                self.assertTrue(plan['needs_hook_repair'])
                self.assertFalse(plan['repairable'])
                status, error = await self.request('POST', f'/v1/connections/{self.identity}/repair-trae-hooks',
                                                   {'expected_revision': 0, 'operation_id': scenario, 'reviewed_sha': ''})
                self.assertEqual((status, error), (409, {'error': expected, 'phase': 'pre_mutation'}))
                self.assertEqual(self.target.read_bytes(), before)
        self.assertFalse(list(self.profile.glob('hooks.json.memlane-repair-backup-*')))

    async def test_prepared_preview_and_phased_receipts(self):
        endpoint = f'/v1/connections/{self.identity}'
        before = self.registry.path.read_bytes()
        for action, revision, code in (
            ('activate', 7, 'revision_conflict'), ('prepare', 0, 'managed_plugin_input_mismatch'),
            ('activate', 0, 'reviewed_prepared_connection_required'),
        ):
            with self.subTest(code=code), patch.object(self.registry, 'provision_agent') as provision:
                status, payload = await self.request('POST', endpoint + '/' + action,
                                                     {'expected_revision': revision, 'operation_id': code})
                provision.assert_not_called()
                self.assertIn(status, (409, 503))
                self.assertEqual(payload, {'error': code, 'phase': 'pre_mutation'})
                _, receipt = await self.request('GET', '/v1/connections/operations/' + code)
                self.assertEqual((receipt['status'], receipt['outcome'], receipt['phase']),
                                 ('complete', 'rejected', 'pre_mutation'))
        self.assertEqual(self.registry.state['revision'], 0)
        self.assertEqual(self.registry.path.read_bytes(), before)

        # Drive a journal race through the pi adapter, which writes the host stub.
        pi = next(adapter for adapter in self.connections.adapters.values() if getattr(adapter, 'kind', None) == 'pi')
        pi.target.parent.mkdir(parents=True)
        pi.root.mkdir(parents=True)
        stub = b'prepared extension'
        (pi.root / 'extension-proposed.ts').write_bytes(stub)
        self.registry.state['agents'][pi.id] = {'installation': {}}
        self.registry.state['connections'][pi.id] = {
            'status': 'prepared', 'proposed_sha256': sha(stub), 'preview': {'baseline_sha256': sha(b'')}}
        pi_endpoint = f'/v1/connections/{pi.id}'
        status, preview = await self.request('GET', pi_endpoint + '/preview')
        self.assertEqual((status, preview['reviewed_sha']), (200, sha(stub)))
        original_save = self.registry.save

        def competing_edit():
            original_save()
            pi.target.write_bytes(b'other extension')

        with (patch('agent_connections.verify_artifact'),
              patch.object(self.registry, 'save', side_effect=competing_edit)):
            status, payload = await self.request('POST', pi_endpoint + '/activate',
                                                 {'expected_revision': 0, 'operation_id': 'raced',
                                                  'proposed_sha256': sha(stub)})
        self.assertEqual((status, payload), (409, {'error': 'host_configuration_changed', 'phase': 'post_mutation'}))
        _, receipt = await self.request('GET', '/v1/connections/operations/raced')
        self.assertEqual((receipt['status'], receipt['phase']), ('unknown', 'post_mutation'))
        self.assertNotIn('outcome', receipt)
        self.assertEqual(pi.target.read_bytes(), b'other extension')

    async def test_reconcile_apply_requires_current_revision_including_zero(self):
        operations = self.registry.state.setdefault('connection_operations', {})
        for operation in ('at-zero', 'stale-zero'):
            operations[operation] = dict(status='unknown', phase='pre_mutation', instance_id=self.identity,
                                         action='prepare', error_code='revision_conflict')
        endpoint = '/v1/connections/operations/at-zero/reconcile'
        for payload in ({}, {'dry_run': False}, {'expected_revision': None}):
            with self.subTest(payload=payload):
                self.assertEqual(await self.request('POST', endpoint, payload),
                                 (400, {'error': 'invalid_request_fields'}))
        with self.assertRaisesRegex(PolicyError, 'invalid_request_fields'):
            await self.connections.reconcile_operation('at-zero')
        self.assertEqual(self.registry.state['revision'], 0)
        self.assertEqual(operations['at-zero']['status'], 'unknown')
        status, result = await self.request('POST', endpoint, {'expected_revision': 0})
        self.assertEqual((status, result['revision'], result['instance_id']), (200, 1, self.identity))
        self.assertEqual(await self.request('POST', '/v1/connections/operations/stale-zero/reconcile',
                                            {'dry_run': False, 'expected_revision': 0}),
                         (409, {'error': 'revision_conflict'}))
        self.assertEqual(operations['stale-zero']['status'], 'unknown')
        self.assertEqual(self.registry.state['revision'], 1)

    async def test_reconcile_validates_existing_receipt_scope_before_returning_or_applying(self):
        operations = self.registry.state.setdefault('connection_operations', {})
        for state, phase, expected_phase in (
                ('complete', None, 'pre_mutation'), ('unknown', None, 'post_mutation'),
                ('unknown', 'pre_mutation', 'pre_mutation')):
            operation = f'{state}-{phase}'
            receipt = dict(status=state, phase=phase, instance_id=self.identity, action='prepare')
            operations[operation] = receipt.copy()
            endpoint = f'/v1/connections/operations/{operation}/reconcile'
            for dry_run in (True, False):
                for field, value in (('instance_id', 'other'), ('action', 'activate'),
                                     ('instance_id', 0), ('action', [])):
                    with self.subTest(state=state, phase=phase, dry_run=dry_run, field=field, value=value):
                        self.assertEqual(await self.request('POST', endpoint, {
                            'dry_run': dry_run, 'expected_revision': 0, field: value}),
                            (409, {'error': 'operation_id_conflict', 'phase': expected_phase}))
                if state == 'complete':
                    status, result = await self.request('POST', endpoint, {
                        'dry_run': dry_run, 'expected_revision': 0, 'instance_id': self.identity, 'action': 'prepare'})
                    self.assertEqual((status, result['already_complete'], result['instance_id']),
                                     (200, True, self.identity))
            self.assertEqual(operations[operation], receipt)
        self.assertEqual(self.registry.state['revision'], 0)

    async def test_reconcile_only_untouched_allowlisted_receipts(self):
        operations = self.registry.state.setdefault('connection_operations', {})
        for label, code, phase, status, files_changed, allowed in (
            ('safe', 'revision_conflict', None, 'prepared', False, True),
            ('ambiguous', 'host_configuration_changed', None, 'prepared', False, False),
            ('missing', None, None, 'prepared', False, False),
            ('unknown', 'revision_conflict', None, 'activation_unknown', False, False),
            ('post', 'revision_conflict', 'post_mutation', 'prepared', False, False),
            ('modified', 'revision_conflict', None, 'prepared', True, False),
        ):
            with self.subTest(label=label):
                self.registry.state['connections'][self.identity] = {
                    'status': status, 'preview': {'baseline_sha256': sha(b'')}}
                if files_changed:
                    self.target.write_text('{"hooks":{}}')
                operations[label] = {'status': 'unknown', 'instance_id': self.identity, 'action': 'activate',
                                     'error_code': code, 'phase': phase}
                endpoint = f'/v1/connections/operations/{label}/reconcile'
                expected = self.registry.state['revision']
                before = self.paths()
                self.assertEqual((await self.request('POST', endpoint, {'dry_run': True}))[1]['reconcilable'], allowed)
                self.assertEqual(self.paths(), before)
                response_status, result = await self.request('POST', endpoint, {'expected_revision': expected})
                if allowed:
                    self.assertEqual(response_status, 200)
                    self.assertEqual(result['revision'], expected + 1)
                    self.assertEqual(result['operation']['outcome'], 'rejected')
                    self.assertEqual(result['result'], result['operation']['result'])
                    self.assertEqual({key: result[key] for key in (
                        'recorded', 'safe_to_unlock', 'reconciled', 'status', 'outcome', 'phase',
                        'operation_id', 'action', 'error_code', 'reason', 'reconcilable',
                    )}, dict(recorded=True, safe_to_unlock=True, reconciled=True, status='complete',
                             outcome='rejected', phase='pre_mutation', operation_id=label, action='activate',
                             error_code=code, reason='', reconcilable=True))
                else:
                    self.assertEqual((response_status, result['phase']), (409, 'post_mutation'))
                    self.assertEqual(operations[label]['status'], 'unknown')
                    self.assertEqual(self.paths(), before)

    async def test_unknown_replays_preserve_post_mutation_for_matching_and_conflicting_signatures(self):
        for phase in ({'phase': 'post_mutation'}, {}):
            for action in ('prepare', 'repair-trae-hooks'):
                operation = 'replay-' + action
                self.registry.state.setdefault('connection_operations', {})[operation] = {
                    'signature': sha(json.dumps([self.identity, action, None]).encode()),
                    'status': 'unknown', **phase}
                for digest, code in ((None, 'connection_operation_requires_review'), ('different', 'operation_id_conflict')):
                    with self.subTest(phase=phase, action=action, digest=digest):
                        with self.assertRaisesRegex(PolicyError, code) as error:
                            if action == 'prepare':
                                await self.connections.perform(self.identity, action, 0, operation, digest)
                            else:
                                apply_trae_hooks_repair(self.registry, self.adapter, self.config, expected_revision=0,
                                                        operation_id=operation, reviewed_sha=digest)
                        self.assertEqual((error.exception.status, error.exception.phase), (409, 'post_mutation'))
                        if action == 'repair-trae-hooks' or digest is None:
                            status, response = await self.request('POST', f'/v1/connections/{self.identity}/{action}',
                                                                  {'expected_revision': 0, 'operation_id': operation,
                                                                   **({'proposed_sha256': digest} if digest else {})})
                            self.assertEqual((status, response), (409, {'error': code, 'phase': 'post_mutation'}))

    async def test_historical_prepare_revision_conflict_with_existing_agent_requires_review(self):
        self.registry.state['agents'][self.identity] = {'name': 'provisioned before revision check'}
        self.registry.state.setdefault('connection_operations', {})['legacy-prepare'] = {
            'status': 'unknown', 'instance_id': self.identity, 'action': 'prepare', 'error_code': 'revision_conflict'}
        preview = await self.connections.reconcile_operation('legacy-prepare', dry_run=True)
        self.assertFalse(preview['reconcilable'])
        endpoint = '/v1/connections/operations/legacy-prepare/reconcile'
        self.assertEqual(await self.request('POST', endpoint, {'dry_run': True}), (200, preview))
        self.assertEqual(await self.request('POST', endpoint, {'expected_revision': 0}),
                         (409, {'error': 'connection_operation_ambiguous_requires_review', 'phase': 'post_mutation'}))

    async def test_historical_config_rejection_allows_untouched_user_files(self):
        codex = next(adapter for adapter in self.connections.adapters.values() if getattr(adapter, 'kind', 'codex') == 'codex')
        for adapter, code, content in (
                (self.adapter, 'trae_ide_configuration_requires_review', b'{"version":0,"hooks":{"Stop":[{"hooks":[{"command":"echo user"}]}]}}'),
                (codex, 'codex_config_requires_review', b'[an unrelated invalid toml section')):
            with self.subTest(code=code):
                adapter.target.write_bytes(content)
                with self.assertRaisesRegex(PolicyError, code):
                    adapter.preview()
                self.registry.state.setdefault('connection_operations', {})[code] = {
                    'status': 'unknown', 'instance_id': adapter.id, 'action': 'prepare', 'error_code': code}
                before = self.paths()
                endpoint = f'/v1/connections/operations/{code}/reconcile'
                preview = await self.connections.reconcile_operation(code, dry_run=True)
                self.assertTrue(preview['reconcilable'])
                self.assertEqual(await self.request('POST', endpoint, {'dry_run': True}), (200, preview))
                self.assertEqual(self.paths(), before)
                status, result = await self.request('POST', endpoint, {'expected_revision': preview['revision']})
                self.assertEqual((status, result['outcome'], result['error_code']), (200, 'rejected', code))
                self.assertEqual(adapter.target.read_bytes(), content)

    async def test_explicit_unrecorded_reconciliation_requires_scope_and_untouched_host(self):
        endpoint = '/v1/connections/operations/old-conflict/reconcile'
        self.assertEqual((await self.request('POST', endpoint, {'dry_run': True}))[0], 404)
        scope = {'instance_id': self.identity, 'action': 'prepare'}
        self.target.write_text('{"version":0,"unrelated":true}')
        before = self.paths()
        preview = await self.connections.reconcile_operation('old-conflict', dry_run=True, **scope)
        self.assertEqual(preview, {'recorded': False, 'operation_id': 'old-conflict', 'revision': 0, **scope,
                                  'status': 'unrecorded', 'outcome': '', 'phase': '', 'error_code': 'revision_conflict',
                                  'reconciled': False, 'reconcilable': True, 'safe_to_unlock': False,
                                  'reason': '', 'result': None})
        self.assertEqual(await self.request('POST', endpoint, {'dry_run': True, **scope}), (200, preview))
        self.assertEqual(self.paths(), before)
        self.registry.state['agents'][self.identity] = {'name': 'ambiguous'}
        denied = await self.connections.reconcile_operation('old-conflict', dry_run=True, **scope)
        self.assertFalse(denied['reconcilable'])
        self.assertEqual(await self.request('POST', endpoint, {'dry_run': True, **scope}), (200, denied))
        self.assertEqual(await self.request('POST', endpoint, {'expected_revision': 0, **scope}),
                         (409, {'error': 'connection_operation_ambiguous_requires_review', 'phase': 'post_mutation'}))
        self.registry.state['agents'].pop(self.identity)
        self.target.write_text(f'managed-marketplaces/{self.identity}/')
        self.assertFalse((await self.connections.reconcile_operation('old-conflict', dry_run=True, **scope))['reconcilable'])
        self.target.write_text('{"version":0,"unrelated":true}')
        status, result = await self.request('POST', endpoint, {'expected_revision': 0, **scope})
        self.assertEqual((status, result['revision'], result['outcome'], result['phase'], result['safe_to_unlock']),
                         (200, 1, 'rejected', 'pre_mutation', True))
        with self.assertRaisesRegex(PolicyError, 'revision_conflict') as replay:
            await self.connections.perform(self.identity, 'prepare', 1, 'old-conflict')
        self.assertEqual(replay.exception.phase, 'pre_mutation')

    async def test_orphan_recovery_waits_for_json_commit_and_refuses_unreviewed_live_changes(self):
        class SimulatedCrash(BaseException):
            pass

        baseline, user = self.orphan()
        self.registry.state['connections'][self.identity] = {
            'status': 'prepared', 'trae_ide_next_hooks': {event: groups[-1] for event, groups in json.loads(baseline)['hooks'].items()
                                    if event != 'Custom'},
            'preview': {'baseline_sha256': sha(baseline)}}
        self.registry.save()
        revision = self.registry.state['revision']
        operation = 'interrupted-orphan'
        plan = inspect_trae_hooks_repair(self.registry, self.adapter, self.config)
        save_json = self.registry._save_slim_json

        def crash_after_sqlite(**kwargs):
            if sha(self.target.read_bytes()) == plan['proposed_sha256']:
                raise SimulatedCrash
            return save_json(**kwargs)

        with patch.object(self.registry, '_save_slim_json', side_effect=crash_after_sqlite):
            with self.assertRaises(SimulatedCrash):
                apply_trae_hooks_repair(self.registry, self.adapter, self.config, expected_revision=revision,
                                        operation_id=operation, reviewed_sha=plan['reviewed_sha'])

        registry_path = self.registry.path
        self.registry.close()
        self.registry = Registry(registry_path, None)
        self.connections = Connections(self.registry, self.config)
        self.adapter = self.connections.adapters[self.identity]
        self.assertEqual(self.registry.state['connection_operations'][operation]['status'], 'complete')
        self.assertEqual(self.connections.operation(operation)['status'], 'unknown')
        self.assertFalse((await self.connections.reconcile_operation(operation, dry_run=True))['safe_to_unlock'])
        with self.assertRaisesRegex(PolicyError, 'connection_operation_requires_review'):
            apply_trae_hooks_repair(self.registry, self.adapter, self.config, expected_revision=revision,
                                    operation_id=operation, reviewed_sha=plan['reviewed_sha'])
        live = self.target.read_bytes()
        self.target.write_bytes(live + b' ')
        with self.assertRaisesRegex(PolicyError, 'trae_ide_hook_changed_requires_review'):
            await self.adapter.review(revision)
        self.assertEqual(self.registry.state['revision'], revision)
        self.target.write_bytes(live)
        await self.adapter.review(revision)
        record = self.registry.state['connections'][self.identity]
        self.assertEqual(record, {'status': 'not_connected'})
        self.assertEqual(self.registry.state['revision'], revision + 1)
        receipt = self.connections.operation(operation)
        self.assertEqual(receipt['status'], 'complete')
        self.assertEqual(receipt['result']['preserved_third_party_groups'], 2)
        result = apply_trae_hooks_repair(self.registry, self.adapter, self.config, expected_revision=revision,
                                         operation_id=operation, reviewed_sha=plan['reviewed_sha'])
        self.assertEqual(result['preserved_third_party_groups'], 2)
        self.assertEqual(json.loads(live)['hooks'], {'Stop': [user], 'Custom': [user]})
