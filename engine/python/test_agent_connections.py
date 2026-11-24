import asyncio
import copy
import importlib.util
import json
from pathlib import Path
import shutil
import sys
import subprocess
import os
import tempfile
import threading
from types import SimpleNamespace
import unittest
from unittest.mock import AsyncMock, patch

from agent_connections import NativeConnection, Connections, sha, verify_artifact
from project_control import Registry, private_write
from project_codex_connection import CodexConnection
from project_policy import PolicyError

ROOT = Path(__file__).resolve().parents[2]
NATIVE_KINDS = ('pi', 'traex', 'cursor', 'claude_code', 'trae_ide')


class CheckedConnectionViewTests(unittest.IsolatedAsyncioTestCase):
    async def asyncSetUp(self):
        self.root = Path(self.enterContext(tempfile.TemporaryDirectory())).resolve()
        self.profile = self.root / 'codex'
        self.profile.mkdir()
        self.registry = Registry(self.root / 'private/registry.json', None)
        self.addCleanup(self.registry.close)
        self.config = {'agent_hosts': {'codex': {'profile': str(self.profile), 'executable': sys.executable}}}
        self.connections = Connections(self.registry, self.config)
        self.addAsyncCleanup(self.connections.capture.close)

    async def test_checked_view_discovers_once_and_attaches_refreshed_capture(self):
        loop = asyncio.get_running_loop()
        loop_thread = threading.get_ident()
        started, release = asyncio.Event(), threading.Event()
        viewed = []

        def paused_view(adapter):
            self.assertNotEqual(threading.get_ident(), loop_thread)
            self.assertIsNot(adapter.registry, self.registry)
            viewed.append(adapter)
            loop.call_soon_threadsafe(started.set)
            if not release.wait(3):
                raise TimeoutError('test did not release Codex view')
            return {'instance_id': adapter.id, 'status': 'configured_pending_new_session'}

        inspect = AsyncMock(return_value={'state': 'healthy', 'permission': 'granted'})
        with (patch.object(CodexConnection, 'view', paused_view),
              patch.object(Connections, '_discover', wraps=Connections._discover) as discover,
              patch.object(self.connections.capture, 'inspect', inspect)):
            request = asyncio.create_task(self.connections.checked_view(force=True))
            try:
                await asyncio.wait_for(started.wait(), 2)
                await asyncio.sleep(0)
                self.assertFalse(request.done())
            finally:
                release.set()
                result = await request
        discover.assert_called_once()
        self.assertEqual(len(viewed), 1)
        self.assertIs(viewed[0].registry, self.registry)
        self.assertIs(self.connections.adapters[viewed[0].id], viewed[0])
        inspect.assert_awaited_once()
        row = next(row for row in result['connections'] if row['kind'] == 'codex')
        self.assertEqual(row['capture']['state'], 'healthy')
        self.assertTrue(row['capabilities']['auto_capture']['verified'])

    async def test_checked_view_does_not_replace_newly_selected_environment(self):
        loop = asyncio.get_running_loop()
        started, release = asyncio.Event(), threading.Event()
        replacement = self.root / 'other-codex'
        replacement.mkdir()

        def paused_view(adapter):
            if adapter.registry is not self.registry:
                loop.call_soon_threadsafe(started.set)
                if not release.wait(3):
                    raise TimeoutError('test did not release Codex view')
            return {'instance_id': adapter.id, 'status': 'not_connected'}

        revision = self.registry.state['revision']
        with patch.object(CodexConnection, 'view', paused_view):
            request = asyncio.create_task(self.connections.checked_view())
            try:
                await asyncio.wait_for(started.wait(), 2)
                selected = await self.connections.select_environment('codex', str(replacement), revision)
                selected_adapters = self.connections.adapters
            finally:
                release.set()
                original = await request
        self.assertEqual(original['revision'], revision)
        self.assertEqual(selected['revision'], revision + 1)
        self.assertEqual(next(row['profile'] for row in original['connections'] if row['kind'] == 'codex'), str(self.profile))
        self.assertIs(self.connections.adapters, selected_adapters)
        self.assertEqual(self.connections.environments['codex']['profile'], str(replacement))
        self.assertEqual(next(adapter.home for adapter in selected_adapters.values() if isinstance(adapter, CodexConnection)), replacement)


class ConnectionTests(unittest.IsolatedAsyncioTestCase):
    @classmethod
    def setUpClass(cls):
        cls.temp = tempfile.TemporaryDirectory()
        cls.root = Path(cls.temp.name).resolve()
        spec = importlib.util.spec_from_file_location('managed_builder', ROOT / 'scripts/build_project_plugin.py')
        builder = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(builder)
        cls.plugin = cls.root / 'fixed-plugin'
        builder.build(ROOT / '.cache/coding-memory-components/plugin-a176248914fdb32a78354854b300174203b15a5c/integrations/codex/plugins/cognee', cls.plugin)

    @classmethod
    def tearDownClass(cls):
        cls.temp.cleanup()

    async def asyncSetUp(self):
        self.workspace = Path(tempfile.mkdtemp(dir=self.root))
        official = type('OfficialFixture', (), {'url': 'http://127.0.0.1:18999'})()
        self.registry = Registry(self.workspace / 'private/registry.json', official)
        self.config = {'python': sys.executable, 'agent_host_home': str(self.workspace),
                       'project_control': {'plugin_dir': str(self.plugin), 'url': 'http://127.0.0.1:18998'}}
        self.profile = self.workspace / 'pi-profile'
        self.profile.mkdir()
        self.adapter = NativeConnection(self.registry, self.config, 'pi', self.profile, sys.executable)
        for suffix in ('key', 'control'):
            private_write(self.workspace / suffix, ('synthetic-' + suffix + '-credential').encode())
        self.registry.state['agents'][self.adapter.id] = {
            'name': 'pi', 'key_file': str(self.workspace / 'key'), 'control_token_file': str(self.workspace / 'control')}
        self.registry.save()

    async def asyncTearDown(self):
        self.registry.close()
        shutil.rmtree(self.workspace)

    def native_adapter(self, kind, label):
        config = {**self.config, 'agent_hosts': {'claude_code': {'profile': str(self.workspace / 'isolated-claude')}}}
        adapter = NativeConnection(self.registry, config, kind, self.workspace / f'{label}-{kind}', sys.executable)
        self.registry.state['agents'][adapter.id] = {
            'name': kind, 'key_file': str(self.workspace / 'key'), 'control_token_file': str(self.workspace / 'control')}
        return adapter

    async def activate_native_fixture(self, adapter, prepared):
        def native_install(*args, **kwargs):
            installation = self.registry.state['agents'][adapter.id]['installation']
            source = Path(installation['plugin_dir'])
            marketplace = installation['marketplace_name']
            cache = adapter.profile / 'plugins/cache' / marketplace
            if adapter.kind == 'traex' and args[0][1:3] == ['plugin', 'install']:
                version = json.loads((source / '.codex-plugin/plugin.json').read_text())['version']
                shutil.copytree(source, cache / 'memlane-cognee' / version)
                receipt = adapter.profile / '.tmp/marketplaces' / marketplace / '.agents/plugins/trae-direct-install.json'
                private_write(receipt, json.dumps({
                    'type': 'trae-direct-plugin-install', 'marketplace': marketplace,
                    'plugins': {'memlane-cognee': {'source_path': str(source), 'source_kind': 'native'}},
                }).encode())
                private_write(adapter.target, f'[plugins."memlane-cognee@{marketplace}"]\nenabled = true\n'.encode())
            elif adapter.kind == 'claude_code' and args[0][1:3] == ['plugin', 'install']:
                cache = cache / 'cognee'
                shutil.copytree(source, cache)
                name = 'cognee@' + marketplace
                private_write(adapter.profile / 'plugins/installed_plugins.json', json.dumps({
                    'plugins': {name: [{'scope': 'user', 'installPath': str(cache)}]},
                }).encode())
                private_write(adapter.target, json.dumps({'enabledPlugins': {name: True}}).encode())
            return SimpleNamespace(returncode=0)

        with patch('agent_connections.subprocess.run', side_effect=native_install):
            return await adapter.activate(self.registry.state['revision'], prepared['preview']['proposed_sha256'])

    def pending_operation(self, adapter, action):
        operations = self.registry.state.setdefault('connection_operations', {})
        operation = {'instance_id': adapter.id, 'action': action, 'status': 'unknown'}
        operations[f'{adapter.id}-{action}'] = operation
        return operation

    async def review_without_host_writes(self, adapter, operation=None):
        baseline = adapter.target.read_bytes() if adapter.target.exists() else None
        revision = self.registry.state['revision']
        with patch('agent_connections.private_write') as write, patch('agent_connections.subprocess.run') as installer:
            row = await adapter.review(revision)
        write.assert_not_called()
        installer.assert_not_called()
        self.assertEqual(adapter.target.read_bytes() if adapter.target.exists() else None, baseline)
        self.assertEqual(self.registry.state['revision'], revision + 1)
        if operation is not None:
            self.assertEqual(operation['status'], 'complete')
            self.assertEqual(operation['outcome'], 'rejected')
            self.assertEqual(operation['error_code'], f'{adapter.kind}_preparation_did_not_install')
            self.assertEqual(operation['result'], {'revision': revision + 1, 'connection': row})
        return row

    async def assert_review_rejected(self, adapter, code):
        before = copy.deepcopy(self.registry.state)
        saved = self.registry.path.read_bytes()
        with self.assertRaisesRegex(PolicyError, code) as error:
            await adapter.review(self.registry.state['revision'])
        self.assertEqual(error.exception.status, 409)
        self.assertEqual(self.registry.state, before)
        self.assertEqual(self.registry.path.read_bytes(), saved)

    async def test_first_preparation_unknown_recovers_after_checking_staged_artifacts(self):
        from project_installation import prepare

        for kind in NATIVE_KINDS:
            for stages_artifact in (False, True):
                with self.subTest(kind=kind, stages_artifact=stages_artifact):
                    adapter = self.native_adapter(kind, f'first-{stages_artifact}')
                    operation = self.pending_operation(adapter, 'prepare')
                    agent = self.registry.state['agents'][adapter.id]

                    def interrupt(*args, **kwargs):
                        if stages_artifact:
                            agent['installation'] = prepare(*args, **kwargs)
                        raise RuntimeError('interrupted prepare')

                    with patch('project_installation.prepare', side_effect=interrupt):
                        with self.assertRaisesRegex(RuntimeError, 'interrupted prepare'):
                            await adapter.prepare(self.registry.state['revision'])
                    self.assertEqual(adapter.view()['status'], 'preparation_unknown')
                    journal = self.registry.state['connections'][adapter.id]
                    baseline_sha = journal['preview'].pop('baseline_sha256')
                    await self.assert_review_rejected(adapter, 'host_configuration_changed')
                    journal['preview']['baseline_sha256'] = None
                    await self.assert_review_rejected(adapter, 'host_configuration_changed')
                    journal['preview']['baseline_sha256'] = baseline_sha
                    artifacts = self.registry.path.parent / 'managed-marketplaces' / adapter.id
                    half = artifacts / 'half-applied'
                    half.mkdir(parents=True)
                    await self.assert_review_rejected(adapter, 'connection_artifact_changed')
                    half.rmdir()
                    if not stages_artifact:
                        artifacts.rmdir()
                    else:
                        plugin = Path(agent['installation']['plugin_dir'])
                        marker = plugin / '.memlane-installation.json'
                        marker.chmod(0o644)
                        await self.assert_review_rejected(adapter, 'connection_artifact_changed')
                        marker.chmod(0o600)
                        binding = plugin / 'host-binding.json'
                        contents = binding.read_bytes()
                        binding.unlink()
                        await self.assert_review_rejected(adapter, 'connection_artifact_changed')
                        private_write(binding, contents)

                    private_write(adapter.target, b'host edited after preview')
                    await self.assert_review_rejected(adapter, 'host_configuration_changed')
                    adapter.target.unlink()
                    row = await self.review_without_host_writes(adapter, operation)
                    self.assertEqual(row['status'], 'not_connected')
                    self.assertNotIn('installation', agent)
                    self.assertEqual((await adapter.prepare(self.registry.state['revision']))['status'], 'prepared')
                    self.assertEqual((await adapter.review(self.registry.state['revision']))['status'], 'prepared')

    async def test_upgrade_preparation_unknown_recovers_new_and_legacy_journals(self):
        for kind in NATIVE_KINDS:
            with self.subTest(kind=kind):
                adapter = self.native_adapter(kind, 'upgrade')
                prepared = await adapter.prepare(self.registry.state['revision'])
                await self.activate_native_fixture(adapter, prepared)
                agent = self.registry.state['agents'][adapter.id]
                installation = copy.deepcopy(agent['installation'])
                active = self.registry.state['connections'][adapter.id]
                active.update(activated_at=42, upgraded_at=43, environment_receipt_required=True,
                              executable_fingerprint=[1, 2, 3])
                original = copy.deepcopy(active)
                baseline = adapter.target.read_bytes()
                manifest = Path(installation['plugin_dir']) / '.memlane-project-access.json'
                manifest_bytes = manifest.read_bytes()
                self.registry.save()
                manifest.write_bytes(b'{}')
                before = copy.deepcopy(self.registry.state)
                with self.assertRaisesRegex(PolicyError, 'connection_artifact_changed'):
                    await adapter.prepare(self.registry.state['revision'])
                self.assertEqual(self.registry.state, before)
                manifest.write_bytes(manifest_bytes)

                operation = self.pending_operation(adapter, 'prepare')
                with patch('project_installation.prepare', side_effect=RuntimeError('interrupted prepare')):
                    with self.assertRaisesRegex(RuntimeError, 'interrupted prepare'):
                        await adapter.prepare(self.registry.state['revision'])
                journal = self.registry.state['connections'][adapter.id]
                persisted = json.loads(self.registry.path.read_bytes())
                self.assertEqual(persisted['connections'][adapter.id], journal)
                self.assertTrue(journal['activated'])
                half = manifest.parents[3] / 'half-applied' / 'plugins/cognee'
                half.mkdir(parents=True)
                shutil.copytree(manifest.parent, half, dirs_exist_ok=True, ignore=shutil.ignore_patterns('hooks.json'))
                await self.assert_review_rejected(adapter, 'connection_artifact_changed')
                shutil.rmtree(half.parents[1])
                if kind != 'cursor':
                    catalog = Path(installation['marketplace_root']) / (
                        '.claude-plugin/marketplace.json' if kind == 'claude_code' else '.agents/plugins/marketplace.json')
                    catalog_bytes = catalog.read_bytes()
                    catalog.unlink()
                    await self.assert_review_rejected(adapter, 'connection_artifact_changed')
                    private_write(catalog, catalog_bytes)
                wrong_manifest = json.loads(manifest_bytes)
                wrong_manifest['host_kind'] = 'other-host'
                private_write(manifest, json.dumps(wrong_manifest).encode())
                await self.assert_review_rejected(adapter, 'connection_artifact_changed')
                private_write(manifest, manifest_bytes)
                for key in ('activated_at', 'upgraded_at', 'environment_receipt_required',
                            'executable_fingerprint', 'sessions_before_activation'):
                    self.assertEqual(journal[key], original[key])
                legacy = {key: copy.deepcopy(journal[key]) for key in
                          ('status', 'preview', 'previous_plugin', 'cursor_hook', 'cursor_active_installation',
                           'trae_ide_hooks') if key in journal}
                for format_name in ('current', 'legacy'):
                    with self.subTest(journal=format_name):
                        if format_name == 'legacy':
                            self.registry.state['connections'][adapter.id] = legacy
                            operation = self.pending_operation(adapter, 'prepare')
                            self.registry.save()
                        manifest.write_bytes(b'{}')
                        await self.assert_review_rejected(adapter, 'connection_artifact_changed')
                        manifest.write_bytes(manifest_bytes)
                        adapter.target.write_bytes(b'edited host config')
                        await self.assert_review_rejected(adapter, 'host_configuration_changed')
                        adapter.target.write_bytes(baseline)

                        row = await self.review_without_host_writes(adapter, operation)
                        self.assertEqual(row['status'], 'configured_pending_new_session')
                        self.assertEqual(agent['installation'], installation)
                        restored = self.registry.state['connections'][adapter.id]
                        if format_name == 'current':
                            self.assertEqual(restored, original)
                        self.assertFalse({'prepared_installation', 'active_installation', 'active_record'} & restored.keys())

    async def test_trae_ide_dispatch_uses_active_generation_until_upgrade_activation(self):
        from trae_ide_connection import EVENTS, LEGACY_EVENTS, HookDispatch, loader

        adapter = self.native_adapter('trae_ide', 'dispatch')
        adapter.profile.mkdir()
        dispatch = HookDispatch(self.registry, sys.executable)
        event = 'Stop'
        raw = json.dumps({'hook_event_name': event, 'cwd': str(adapter.profile),
                          'workspace_roots': [str(adapter.profile)]}).encode()
        worker = SimpleNamespace(returncode=0, communicate=AsyncMock(return_value=(b'{}', b'')))

        async def assert_dispatch(installation):
            with patch('asyncio.create_subprocess_exec', new_callable=AsyncMock, return_value=worker) as execute:
                self.assertEqual(await dispatch.run(adapter.id, event, raw), {})
            execute.assert_awaited_once()
            self.assertEqual(execute.await_args.args[2], str(Path(installation['plugin_dir']) / 'scripts/trae_ide_hook.py'))

        with patch('trae_ide_connection.loader', side_effect=lambda installation, python: {
            event: group for event, group in loader(installation, python).items() if event in LEGACY_EVENTS
        }):
            prepared = await adapter.prepare(self.registry.state['revision'])
        record = self.registry.state['connections'][adapter.id]
        for status in ('prepared', 'preparation_unknown'):
            record['status'] = status
            with self.assertRaisesRegex(PolicyError, 'trae_ide_hook_not_authorized') as error:
                await dispatch.run(adapter.id, event, raw)
            self.assertEqual(error.exception.status, 403)
        record['status'] = 'prepared'
        await adapter.activate(self.registry.state['revision'], prepared['preview']['proposed_sha256'])
        agent = self.registry.state['agents'][adapter.id]
        v1 = copy.deepcopy(agent['installation'])
        baseline = adapter.target.read_bytes()
        self.assertEqual(set(json.loads(baseline)['hooks']), set(LEGACY_EVENTS))
        self.assertEqual(adapter.view()['status'], 'configured_pending_new_session')
        await assert_dispatch(v1)

        adapter.config = {**adapter.config, 'python': '/usr/bin/python3'}
        with patch('project_installation.prepare', side_effect=RuntimeError('interrupted prepare')):
            with self.assertRaisesRegex(RuntimeError, 'interrupted prepare'):
                await adapter.prepare(self.registry.state['revision'])
        self.assertEqual(adapter.view()['status'], 'preparation_unknown')
        await assert_dispatch(v1)
        await self.review_without_host_writes(adapter)

        self.assertEqual((await adapter.prepare(self.registry.state['revision']))['status'], 'prepared')
        record = self.registry.state['connections'][adapter.id]
        v2 = record['prepared_installation']
        self.assertEqual(set(record['trae_ide_next_hooks']), set(EVENTS))
        self.assertNotEqual(v1['plugin_dir'], v2['plugin_dir'])
        self.assertTrue(record['activated'])
        self.assertEqual(agent['installation'], v1)
        self.assertEqual(adapter.target.read_bytes(), baseline)
        await assert_dispatch(v1)
        private_write(adapter.target, b'host edited after preview')
        await self.assert_review_rejected(adapter, 'host_configuration_changed')
        private_write(adapter.target, baseline)

        self.assertEqual((await self.review_without_host_writes(adapter))['status'], 'configured_pending_new_session')
        self.assertFalse({'prepared_installation', 'active_installation', 'active_record'}
                         & self.registry.state['connections'][adapter.id].keys())
        self.assertEqual(agent['installation'], v1)
        self.assertEqual(adapter.target.read_bytes(), baseline)
        await assert_dispatch(v1)

        prepared = await adapter.prepare(self.registry.state['revision'])
        row = await adapter.activate(self.registry.state['revision'], prepared['preview']['proposed_sha256'])
        self.assertEqual(row['status'], 'configured_pending_new_session')
        self.assertFalse({'prepared_installation', 'active_installation', 'active_record'}
                         & self.registry.state['connections'][adapter.id].keys())
        self.assertEqual(agent['installation'], v2)
        self.assertIn(v2['plugin_dir'].encode(), adapter.target.read_bytes())
        self.assertNotIn(v1['plugin_dir'].encode(), adapter.target.read_bytes())
        await assert_dispatch(v2)

    async def test_pi_reprepares_upgrade_and_recovers_completed_activation(self):
        adapter = self.adapter
        state = self.registry.state
        prepared = await adapter.prepare(state['revision'])
        await adapter.activate(state['revision'], prepared['preview']['proposed_sha256'])
        active = copy.deepcopy(state['agents'][adapter.id]['installation'])
        baseline = adapter.target.read_bytes()
        adapter.config = {**adapter.config, 'python': '/usr/bin/python3'}
        upgraded = await adapter.prepare(state['revision'])
        self.assertNotEqual(prepared['preview']['proposed_sha256'], upgraded['preview']['proposed_sha256'])
        self.assertEqual(await adapter.prepare(state['revision']), upgraded)
        self.assertEqual(adapter.target.read_bytes(), baseline)
        self.assertEqual(state['agents'][adapter.id]['installation'], active)

        def interrupt_after_write(path, data):
            private_write(path, data)
            raise RuntimeError('interrupted activation')

        with patch('agent_connections.private_write', side_effect=interrupt_after_write):
            with self.assertRaisesRegex(RuntimeError, 'interrupted activation'):
                await adapter.activate(state['revision'], upgraded['preview']['proposed_sha256'])
        journal = state['connections'][adapter.id]
        self.assertEqual(journal['status'], 'activation_unknown')
        self.assertEqual(state['agents'][adapter.id]['installation'], journal['prepared_installation'])
        row = await self.review_without_host_writes(adapter)
        self.assertEqual(row['status'], 'configured_pending_new_session')
        self.assertNotEqual(state['agents'][adapter.id]['installation'], active)
        self.assertFalse({'prepared_installation', 'active_installation', 'active_record'}
                         & state['connections'][adapter.id].keys())

    async def test_trae_ide_dispatch_checks_bindings_even_without_workspace(self):
        from trae_ide_connection import HookDispatch

        adapter = self.native_adapter('trae_ide', 'invalid-workspace')
        state = self.registry.state
        prepared = await adapter.prepare(state['revision'])
        await self.activate_native_fixture(adapter, prepared)
        agent = state['agents'][adapter.id]
        marker = Path(agent['installation']['plugin_dir']) / '.memlane-installation.json'
        marker_bytes = marker.read_bytes()
        hooks_bytes = adapter.target.read_bytes()
        dispatch = HookDispatch(self.registry, sys.executable)
        invalid = str(adapter.profile / 'absent')
        for workspace in ({}, {'cwd': invalid, 'workspace_roots': [invalid]}):
            raw = json.dumps({'hook_event_name': 'Stop', **workspace}).encode()
            for field in ('host_kind', 'instance_id', 'runtime_profile', 'hook_origin', 'hook_token_file'):
                with self.subTest(workspace=workspace, field=field):
                    binding = json.loads(marker_bytes)
                    binding.pop(field)
                    private_write(marker, json.dumps(binding).encode())
                    with self.assertRaisesRegex(PolicyError, 'connection_binding_changed') as error:
                        await dispatch.run(adapter.id, 'Stop', raw)
                    self.assertEqual(error.exception.status, 409)
            private_write(marker, marker_bytes)
            profile = agent.pop('host_profile')
            with self.assertRaisesRegex(PolicyError, 'connection_binding_changed'):
                await dispatch.run(adapter.id, 'Stop', raw)
            agent['host_profile'] = profile
            hooks = json.loads(hooks_bytes)
            hooks['hooks']['Stop'].clear()
            private_write(adapter.target, json.dumps(hooks).encode())
            with self.assertRaisesRegex(PolicyError, 'trae_ide_hook_changed_requires_review'):
                await dispatch.run(adapter.id, 'Stop', raw)
            hooks = json.loads(hooks_bytes)
            hooks['hooks'].setdefault('Stop', []).append({'hooks': [{'type': 'command', 'command': 'echo drift'}]})
            private_write(adapter.target, json.dumps(hooks).encode())
            with self.assertRaisesRegex(PolicyError, 'trae_ide_hook_changed_requires_review'):
                await dispatch.run(adapter.id, 'Stop', raw)
            private_write(adapter.target, hooks_bytes)
            self.assertEqual(await dispatch.run(adapter.id, 'Stop', raw), {})

    async def test_hook_activate_verifies_candidate_marker_before_host_write_and_legacy_proposal(self):
        state = self.registry.state
        for kind in ('cursor', 'trae_ide'):
            with self.subTest(kind=kind):
                adapter = self.native_adapter(kind, 'prewrite-marker')
                prepared = await adapter.prepare(state['revision'])
                installation = state['agents'][adapter.id]['installation']
                marker = Path(installation['plugin_dir']) / '.memlane-installation.json'
                original_marker = marker.read_bytes()
                stripped = json.loads(original_marker)
                stripped.pop('runtime_profile')
                private_write(marker, json.dumps(stripped).encode())
                with self.assertRaisesRegex(PolicyError, 'connection_binding_changed'):
                    await adapter.activate(state['revision'], prepared['preview']['proposed_sha256'])
                self.assertFalse(adapter.target.exists())
                self.assertEqual(state['connections'][adapter.id]['status'], 'prepared')
                private_write(marker, original_marker)
                await adapter.activate(state['revision'], prepared['preview']['proposed_sha256'])
                if kind == 'trae_ide':
                    record = state['connections'][adapter.id]
                    record.pop('trae_ide_proposal_sha256', None)
                    record['status'] = 'activation_unknown'
                    hooks_bytes = adapter.target.read_bytes()
                    drifted = json.loads(hooks_bytes)
                    drifted['hooks'].setdefault('Stop', []).append({'hooks': [{'type': 'command', 'command': 'echo extra'}]})
                    private_write(adapter.target, json.dumps(drifted).encode())
                    await self.assert_review_rejected(adapter, 'trae_ide_hook_changed_requires_review')
                    private_write(adapter.target, hooks_bytes)
                    self.assertEqual((await self.review_without_host_writes(adapter))['status'],
                                     'configured_pending_new_session')

    async def test_hook_reprepare_restores_same_generation_marker_on_failure(self):
        state = self.registry.state
        for kind in ('trae_ide', 'cursor'):
            with self.subTest(kind=kind):
                adapter = self.native_adapter(kind, 'same-generation')
                prepared = await adapter.prepare(state['revision'])
                await self.activate_native_fixture(adapter, prepared)
                installation = state['agents'][adapter.id]['installation']
                marker = Path(installation['plugin_dir']) / '.memlane-installation.json'
                original = marker.read_bytes()
                interrupted = False

                def interrupt_marker_write(path, data):
                    nonlocal interrupted
                    self.assertEqual(path, marker)
                    self.assertNotIn('runtime_profile', json.loads(marker.read_bytes()))
                    if not interrupted:
                        interrupted = True
                        raise RuntimeError('interrupted marker write')
                    private_write(path, data)

                with patch('agent_connections.private_write', side_effect=interrupt_marker_write):
                    with self.assertRaisesRegex(RuntimeError, 'interrupted marker write'):
                        await adapter.prepare(state['revision'])
                self.assertEqual(marker.read_bytes(), original)
                self.assertEqual(state['connections'][adapter.id]['status'], 'preparation_unknown')
                self.assertEqual((await self.review_without_host_writes(adapter))['status'],
                                 'configured_pending_new_session')
                changed = json.loads(original)
                changed.pop('runtime_profile')
                private_write(marker, json.dumps(changed).encode())
                with self.assertRaisesRegex(PolicyError, 'connection_binding_changed'):
                    await adapter.prepare(state['revision'])

    async def test_prepared_cancel_is_transactional_and_settles_only_pending_prepare(self):
        adapter = self.native_adapter('trae_ide', 'cancel')
        state = self.registry.state
        prepared = await adapter.prepare(state['revision'])
        await self.activate_native_fixture(adapter, prepared)
        state['connections'][adapter.id].update(activated_at=42, executable_fingerprint=[0, 0, 0])
        active = copy.deepcopy(state['connections'][adapter.id])
        adapter.config = {**adapter.config, 'python': '/usr/bin/python3'}
        await adapter.prepare(state['revision'])
        pending = self.pending_operation(adapter, 'prepare')
        completed = {**pending, 'status': 'complete', 'result': {'connection': {'status': 'prepared'}}}
        state['connection_operations']['completed-prepare'] = copy.deepcopy(completed)
        journal = state['connections'][adapter.id]
        digest = journal['preview'].pop('baseline_sha256')
        await self.assert_review_rejected(adapter, 'host_configuration_changed')
        journal['preview']['baseline_sha256'] = digest
        self.registry.save()
        original_hooks = adapter.target.read_bytes()
        verify = adapter._verify_active_installation

        def edit_after_verification(*args):
            verify(*args)
            private_write(adapter.target, original_hooks + b'\n')

        with patch.object(adapter, '_verify_active_installation', side_effect=edit_after_verification):
            await self.assert_review_rejected(adapter, 'trae_ide_hook_changed_requires_review')
        private_write(adapter.target, original_hooks)
        connections = Connections(self.registry, {**adapter.config, 'agent_hosts': {
            'trae_ide': {'profile': str(adapter.profile), 'executable': sys.executable},
        }})
        reviewed = await connections.perform(adapter.id, 'review', state['revision'], 'cancel-prepare')
        row = reviewed['connection']
        self.assertEqual(state['connections'][adapter.id], active)
        self.assertEqual(pending['status'], 'complete')
        self.assertEqual(pending['outcome'], 'rejected')
        self.assertEqual(pending['error_code'], 'trae_ide_preparation_cancelled')
        self.assertEqual(pending['result'], {'revision': state['revision'], 'connection': row})
        self.assertEqual(state['connection_operations']['completed-prepare'], completed)

    async def test_activation_review_restores_or_commits_installed_generation(self):
        from aiohttp.test_utils import make_mocked_request
        from project_control_server import create_app

        state = self.registry.state
        for kind in NATIVE_KINDS:
            for after_write in (False, True):
                with self.subTest(kind=kind, after_write=after_write):
                    adapter = self.native_adapter(kind, f'activation-{after_write}')
                    prepared = await adapter.prepare(state['revision'])
                    await self.activate_native_fixture(adapter, prepared)
                    state['connections'][adapter.id].update(activated_at=42, upgraded_at=43)
                    v1 = copy.deepcopy(state['connections'][adapter.id])
                    agent = state['agents'][adapter.id]
                    old_installation = copy.deepcopy(agent['installation'])
                    baseline = adapter.target.read_bytes()

                    adapter.config = {**adapter.config, 'python': '/usr/bin/python3'}
                    prepared = await adapter.prepare(state['revision'])
                    candidate = state['connections'][adapter.id]['prepared_installation']
                    self.assertNotEqual(candidate['plugin_dir'], old_installation['plugin_dir'])
                    pending = self.pending_operation(adapter, 'activate')
                    pending['error_code'] = 'interrupted'
                    save = self.registry.save

                    def interrupt_before_write():
                        save()
                        if state['connections'][adapter.id]['status'] == 'activation_unknown':
                            raise RuntimeError('interrupted activation')

                    def interrupt_after_pi_write(path, data):
                        private_write(path, data)
                        raise RuntimeError('interrupted activation')

                    interruption = RuntimeError('interrupted activation')
                    if not after_write:
                        interrupt = patch.object(self.registry, 'save', side_effect=interrupt_before_write)
                    elif kind == 'pi':
                        interrupt = patch('agent_connections.private_write', side_effect=interrupt_after_pi_write)
                    else:
                        verifier = 'claude_installed' if kind == 'claude_code' else f'{kind}_installed'
                        interrupt = patch.object(adapter, verifier, side_effect=interruption)
                    with interrupt, self.assertRaisesRegex(RuntimeError, 'interrupted activation'):
                        await self.activate_native_fixture(adapter, prepared)
                    self.assertEqual(state['connections'][adapter.id]['status'], 'activation_unknown')
                    self.assertEqual(agent['installation'], candidate)
                    if not after_write:
                        self.assertEqual(adapter.target.read_bytes(), baseline)
                        if kind == 'claude_code':
                            known_marketplaces = adapter.profile / 'plugins/known_marketplaces.json'
                            private_write(known_marketplaces, json.dumps({candidate['marketplace_name']: {}}).encode())
                            await self.assert_review_rejected(adapter, 'claude_installation_requires_review')
                            known_marketplaces.unlink()
                        elif kind == 'traex':
                            cache = adapter.profile / 'plugins/cache' / candidate['marketplace_name']
                            cache.mkdir()
                            await self.assert_review_rejected(adapter, 'traex_installation_requires_review')
                            cache.rmdir()
                    elif kind in ('trae_ide', 'traex', 'claude_code'):
                        changed_path = (adapter.target if kind == 'trae_ide'
                                        else Path(candidate['plugin_dir']) / '.memlane-project-access.json')
                        original = changed_path.read_bytes()
                        private_write(changed_path, original + b'\n')
                        code = ('trae_ide_hook_changed_requires_review' if kind == 'trae_ide'
                                else 'connection_artifact_changed')
                        await self.assert_review_rejected(adapter, code)
                        private_write(changed_path, original)

                    with patch('agent_connections.time.time', return_value=123):
                        row = await self.review_without_host_writes(adapter)
                    restored = state['connections'][adapter.id]
                    self.assertEqual(row['status'], 'configured_pending_new_session')
                    self.assertEqual(pending['status'], 'complete')
                    self.assertEqual(pending['result'], {'revision': state['revision'], 'connection': row})
                    _, persisted_operations, _ = self.registry.sessions_store.load_all()
                    self.assertEqual(persisted_operations[f'{adapter.id}-activate'], pending)
                    if after_write:
                        self.assertEqual(restored['activated_at'], 123)
                        self.assertNotIn('upgraded_at', restored)
                        self.assertTrue(restored['environment_receipt_required'])
                        self.assertEqual(agent['installation'], candidate)
                        self.assertNotIn('error_code', pending)
                        self.assertNotIn('outcome', pending)
                    else:
                        self.assertEqual(restored, v1)
                        self.assertEqual(agent['installation'], old_installation)
                        self.assertEqual(pending['outcome'], 'rejected')
                        self.assertEqual(pending['error_code'], f'{kind}_activation_did_not_install')
                    if kind == 'trae_ide':
                        raw = json.dumps({'hook_event_name': 'Stop', 'cwd': str(adapter.profile),
                                          'workspace_roots': [str(adapter.profile)]}).encode()
                        app = create_app(self.registry, 'm' * 32, {}, codex_config=adapter.config)
                        request = make_mocked_request('POST', '/v1/trae-ide/hooks/Stop',
                                                      headers={'Content-Type': 'application/json'},
                                                      match_info={'event': 'Stop'}, app=app)
                        request['agent'] = adapter.id
                        route = await app.router.resolve(request)
                        worker = SimpleNamespace(returncode=0, communicate=AsyncMock(return_value=(b'{}', b'')))
                        with (patch.object(request, 'read', new_callable=AsyncMock, return_value=raw),
                              patch('asyncio.create_subprocess_exec', new_callable=AsyncMock, return_value=worker) as run):
                            response = await route.handler(request)
                        self.assertEqual(response.status, 200)
                        self.assertEqual(json.loads(response.body), {})
                        self.assertEqual(run.await_args.args[2],
                                         str(Path(agent['installation']['plugin_dir']) / 'scripts/trae_ide_hook.py'))

    async def test_pi_traex_missing_component_refuses_before_provision_or_state_write(self):
        from unittest.mock import AsyncMock, patch
        empty = self.workspace / 'empty-component'
        empty.mkdir()
        for kind in ('pi', 'traex'):
            for source in (None, str(self.workspace / 'absent'), str(empty), 'relative-component'):
                with self.subTest(kind=kind, source=source):
                    control = {'url': 'http://127.0.0.1:18998'}
                    if source is not None:
                        control['plugin_dir'] = source
                    config = {**self.config, 'project_control': control}
                    adapter = NativeConnection(self.registry, config, kind, self.workspace / ('new-' + kind), sys.executable)
                    before = self.registry.path.read_bytes()
                    provision = AsyncMock(side_effect=AssertionError('must not provision without component'))
                    with patch.object(self.registry, 'provision_agent', provision):
                        self.assertEqual(adapter.view()['status'], 'component_unavailable')
                        with self.assertRaises(PolicyError) as error:
                            await adapter.prepare(self.registry.state['revision'])
                        self.assertEqual(error.exception.code, 'managed_plugin_input_mismatch')
                        provision.assert_not_awaited()
                    self.assertEqual(self.registry.path.read_bytes(), before)
                    self.assertFalse(adapter.profile.exists())

    async def test_missing_source_does_not_hide_previously_prepared_installation(self):
        self.registry.state['connections'] = {self.adapter.id: {'status': 'prepared', 'preview': {'proposed_sha256': 'fixed'}}}
        self.adapter.config = {**self.config, 'project_control': {}}
        self.assertEqual(self.adapter.view()['status'], 'prepared')
        self.assertEqual(self.adapter.view()['preview']['proposed_sha256'], 'fixed')

    async def test_pi_reprepare_preserves_existing_buffer_home_without_runtime_override(self):
        await self.adapter.prepare(self.registry.state['revision'])
        original_home = self.registry.state['agents'][self.adapter.id]['installation']['home']
        pending = Path(original_home) / '.cognee-plugin' / ('pi-' + self.adapter.id) / 'pending.json'
        private_write(pending, b'{"pending":"synthetic-original-record"}')
        self.adapter.config = {key: value for key, value in self.config.items() if key != 'agent_host_home'}
        await self.adapter.prepare(self.registry.state['revision'])
        installation = self.registry.state['agents'][self.adapter.id]['installation']
        self.assertEqual(installation['home'], original_home)
        self.assertEqual(pending.read_bytes(), b'{"pending":"synthetic-original-record"}')

    async def test_pi_reprepare_honors_explicit_buffer_home_override(self):
        await self.adapter.prepare(self.registry.state['revision'])
        replacement = self.workspace / 'explicit-home'
        replacement.mkdir()
        self.adapter.config = {**self.config, 'agent_host_home': str(replacement)}
        await self.adapter.prepare(self.registry.state['revision'])
        self.assertEqual(self.registry.state['agents'][self.adapter.id]['installation']['home'], str(replacement))

    async def test_pi_prepare_activate_preserves_profile_and_waits_for_real_receipt(self):
        settings = self.profile / 'settings.json'
        settings.write_text('{"model":"unchanged","extensions":["other.ts"]}')
        before = settings.read_bytes()
        preview = self.adapter.preview()
        self.assertFalse(self.adapter.target.exists())
        prepared = await self.adapter.prepare(self.registry.state['revision'])
        self.assertFalse(self.adapter.target.exists())
        self.assertEqual(prepared['status'], 'prepared')
        self.assertEqual(prepared['preview']['baseline_sha256'], preview['baseline_sha256'])
        installation = self.registry.state['agents'][self.adapter.id]['installation']
        verify_artifact(installation)
        root = Path(installation['plugin_dir'])
        common = (root / 'scripts/_plugin_common.py').read_text()
        self.assertIn('pi-' + self.adapter.id, common)
        self.assertNotIn('".cognee-plugin" / "codex"', common)
        self.assertEqual((self.plugin / 'scripts/_plugin_common.py').read_bytes().find(b'".cognee-plugin" / "codex"') >= 0, True)
        result = await self.adapter.activate(self.registry.state['revision'], prepared['preview']['proposed_sha256'])
        self.assertEqual(settings.read_bytes(), before)
        self.assertEqual(result['status'], 'configured_pending_new_session')
        self.assertEqual(result['new_sessions'], 0)
        self.assertIn(str(root / 'extension/index.ts'), self.adapter.target.read_text())
        self.registry.state['sessions'][json.dumps([self.adapter.id, 'real-host-key'])] = 'project'
        self.assertEqual(self.adapter.view()['new_sessions'], 1)
        with self.assertRaises(PolicyError):
            await self.adapter.activate(self.registry.state['revision'], prepared['preview']['proposed_sha256'])

    async def test_tampered_artifact_and_concurrent_config_edit_never_activate(self):
        prepared = await self.adapter.prepare(0)
        self.adapter.target.parent.mkdir(parents=True)
        self.adapter.target.write_text('unrelated-user-extension')
        with self.assertRaisesRegex(PolicyError, 'host_configuration_changed'):
            await self.adapter.activate(self.registry.state['revision'], prepared['preview']['proposed_sha256'])
        self.assertEqual(self.adapter.target.read_text(), 'unrelated-user-extension')
        self.adapter.target.unlink()
        installation = self.registry.state['agents'][self.adapter.id]['installation']
        (Path(installation['plugin_dir']) / 'scripts/agent_memory_adapter.py').write_text('tampered')
        with self.assertRaisesRegex(PolicyError, 'connection_artifact_changed'):
            await self.adapter.activate(self.registry.state['revision'], prepared['preview']['proposed_sha256'])
        # Updating the artifact's self-reported receipt cannot repair it.
        receipt = Path(installation['plugin_dir']) / '.memlane-project-access.json'
        data = json.loads(receipt.read_text())
        data['files']['scripts/agent_memory_adapter.py'] = sha(b'tampered')
        receipt.write_text(json.dumps(data))
        with self.assertRaisesRegex(PolicyError, 'connection_artifact_changed'):
            verify_artifact(installation)

    async def test_idempotent_management_operation_and_unknown_never_reexecutes(self):
        config = {**self.config, 'agent_hosts': {'pi': {'profile': str(self.profile), 'executable': sys.executable}}}
        connections = Connections(self.registry, config)
        first = await connections.perform(self.adapter.id, 'prepare', 0, 'prepare-one')
        second = await connections.perform(self.adapter.id, 'prepare', 0, 'prepare-one')
        self.assertEqual(first, second)
        self.assertEqual(self.registry.state['revision'], first['revision'])
        before = self.registry.path.read_bytes()
        self.assertEqual(connections.operation('prepare-one'), {'recorded': True, 'status': 'complete', 'result': first, 'error_code': None})
        self.assertEqual(connections.operation('not-registered'), {'recorded': False})
        self.assertEqual(self.registry.path.read_bytes(), before, 'reading original receipt changed registry')
        receipt = connections.operation('prepare-one')
        receipt['result']['connection']['status'] = 'tampered-copy'
        self.assertEqual(connections.operation('prepare-one')['result'], first)
        with self.assertRaisesRegex(PolicyError, 'operation_id_conflict'):
            await connections.perform(self.adapter.id, 'activate', first['revision'], 'prepare-one', 'wrong')
        self.registry.state['connection_operations']['unknown'] = {
            'signature': sha(json.dumps([self.adapter.id, 'activate', 'digest']).encode()), 'status': 'unknown'}
        with self.assertRaisesRegex(PolicyError, 'connection_operation_requires_review'):
            await connections.perform(self.adapter.id, 'activate', first['revision'], 'unknown', 'digest')
        self.assertEqual(connections.operation('unknown')['status'], 'unknown')

    async def test_original_connection_receipt_http_is_management_only_and_read_only(self):
        from aiohttp.test_utils import TestClient, TestServer
        from project_control_server import create_app
        config = {**self.config, 'agent_hosts': {'pi': {'profile': str(self.profile), 'executable': sys.executable}}}
        self.registry.state['connection_operations'] = {'prior-operation': {'status': 'unknown', 'signature': 'private-internal'}}
        self.registry.save()
        before = self.registry.path.read_bytes()
        client = TestClient(TestServer(create_app(self.registry, 'm'*40, {'a': 'a'*40}, codex_config=config)))
        await client.start_server()
        try:
            path = '/v1/connections/operations/prior-operation'
            self.assertEqual((await client.get(path)).status, 401)
            self.assertEqual((await client.get(path, headers={'Authorization': 'Bearer '+'a'*40})).status, 403)
            response = await client.get(path, headers={'Authorization': 'Bearer '+'m'*40})
            self.assertEqual(response.status, 200)
            data = await response.json()
            self.assertTrue(data['recorded'])
            self.assertEqual(data['status'], 'unknown')
            self.assertNotIn('private-internal', json.dumps(data))
            response = await client.get('/v1/connections/operations/absent', headers={'Authorization': 'Bearer '+'m'*40})
            self.assertEqual(await response.json(), {'recorded': False})
            self.assertEqual(self.registry.path.read_bytes(), before)
        finally:
            await client.close()

    async def test_codex_discovery_is_independent_of_provider_bridge_login_and_does_not_write_profile(self):
        from project_codex_connection import CodexConnection
        profile = self.workspace / 'not-yet-created-codex'
        config = {**self.config, 'generation_mode': 'provider', 'agent_hosts': {
            'codex': {'profile': str(profile), 'executable': sys.executable}}}
        before = json.loads(self.registry.path.read_text())
        connections = Connections(self.registry, config)
        row = next(r for r in connections.view()['connections'] if r['kind'] == 'codex')
        self.assertTrue(row['detected'])
        self.assertEqual(row['status'], 'environment_missing')
        self.assertEqual(row['codex_home'], str(profile.resolve()))
        self.assertFalse(profile.exists(), 'discovery created host configuration')
        self.assertNotIn('auth_home', config, 'Agent detection mutated bridge authentication config')
        self.assertNotIn('codex_executable', config)
        self.assertEqual(json.loads(self.registry.path.read_text()), before)
        legacy = CodexConnection(self.registry, {**self.config, 'auth_home': str(profile), 'codex_executable': sys.executable})
        self.assertEqual(row['instance_id'], legacy.id, 'same home changed the legacy Codex identity')
        missing = Connections(self.registry, {**config, 'agent_hosts': {'codex': {
            'profile': str(profile), 'executable': '/synthetic/unavailable-codex'}}})
        self.assertFalse(next(r for r in missing.view()['connections'] if r['kind']=='codex')['detected'])
        profile.write_text('not a profile directory')
        broken = Connections(self.registry, config).view()['connections']
        self.assertEqual(next(r for r in broken if r['kind']=='codex')['status'], 'environment_unavailable')
        self.assertTrue(any(r['kind']=='pi' for r in broken), 'bad Codex target hid independent Agent connections')

    async def test_select_environment_persists_without_installing_or_retaining_wrong_adapter(self):
        target = self.workspace / 'daily traex'
        target.mkdir()
        original = target / 'traecli.toml'
        original.write_text('[plugins.other]\nenabled = true\n')
        config = {**self.config, 'agent_hosts': {'traex': {
            'profile': str(self.workspace / 'missing verification'), 'executable': sys.executable}}}
        connections = Connections(self.registry, config)
        old = next(r for r in connections.view()['connections'] if r['kind'] == 'traex')
        result = await connections.select_environment('traex', str(target), self.registry.state['revision'])
        row = next(r for r in result['connections'] if r['kind'] == 'traex')
        self.assertNotEqual(row['instance_id'], old['instance_id'])
        self.assertEqual(row['profile'], str(target))
        self.assertEqual(row['status'], 'not_connected')
        self.assertEqual(original.read_text(), '[plugins.other]\nenabled = true\n')
        self.assertNotIn(row['instance_id'], self.registry.state['agents'])
        self.assertNotIn(old['instance_id'], connections.adapters)
        reopened = Connections(self.registry, config)
        row2 = next(r for r in reopened.view()['connections'] if r['kind'] == 'traex')
        self.assertEqual(row2['instance_id'], row['instance_id'])
        before = self.registry.path.read_bytes()
        with self.assertRaises(PolicyError):
            await connections.select_environment('traex', str(target), result['revision'] - 1)
        self.assertEqual(self.registry.path.read_bytes(), before)

    async def test_environment_selection_http_requires_manager_and_does_not_install(self):
        from aiohttp.test_utils import TestClient, TestServer
        from project_control_server import create_app
        target = self.workspace / 'real-profile'
        target.mkdir()
        client = TestClient(TestServer(create_app(self.registry, 'm'*40, {'a': 'a'*40}, codex_config=self.config)))
        await client.start_server()
        try:
            body = {'expected_revision': self.registry.state['revision'], 'kind': 'traex', 'profile': str(target)}
            denied = await client.post('/v1/connections/environment', json=body, headers={'Authorization': 'Bearer '+'a'*40})
            self.assertEqual(denied.status, 403)
            self.assertNotIn('host_targets', self.registry.state)
            response = await client.post('/v1/connections/environment', json=body, headers={'Authorization': 'Bearer '+'m'*40})
            self.assertEqual(response.status, 200)
            self.assertEqual(self.registry.state['host_targets']['traex'], str(target))
            self.assertFalse((target / 'traecli.toml').exists())
        finally:
            await client.close()

    async def test_waiting_receipt_explains_timeout_without_inventing_connection(self):
        connections = Connections(self.registry, {**self.config, 'agent_hosts': {'pi': {
            'profile': str(self.profile), 'executable': sys.executable}}})
        self.registry.state['connections'][self.adapter.id] = {
            'status': 'configured_pending_new_session', 'activated': True,
            'proposed_sha256': sha(b''), 'sessions_before_activation': [], 'activated_at': 1}
        row = next(r for r in connections.view()['connections'] if r['instance_id'] == self.adapter.id)
        self.assertEqual(row['new_sessions'], 0)
        self.assertTrue(row['receipt_overdue'])
        self.assertEqual(row['last_receipt_at'], 0)

    async def test_host_upgrade_review_requires_fresh_receipt_without_reinstalling(self):
        from unittest.mock import patch
        connections = Connections(self.registry, {**self.config, 'agent_hosts': {'pi': {
            'profile': str(self.profile), 'executable': sys.executable}}})
        prepared = await connections.perform(self.adapter.id, 'prepare', 0, 'upgrade-prepare')
        await connections.perform(self.adapter.id, 'activate', self.registry.state['revision'],
            'upgrade-activate', prepared['connection']['preview']['proposed_sha256'])
        record = self.registry.state['connections'][self.adapter.id]
        record['executable_fingerprint'] = [0, 0, 0]
        record['activated_at'] = 1
        self.registry.state['sessions'][json.dumps([self.adapter.id, 'old-session'])] = 'project'
        self.registry.state['connection_receipts'] = {self.adapter.id: {
            'received_at': 2, 'environment_verified': True}}
        row = next(r for r in connections.view()['connections'] if r['instance_id'] == self.adapter.id)
        self.assertEqual(row['error_code'], 'host_executable_changed')
        original = self.adapter.target.read_bytes()
        with patch.object(NativeConnection, 'prepare', side_effect=AssertionError('must not install')):
            await connections.perform(self.adapter.id, 'review', self.registry.state['revision'], 'upgrade-review')
        self.assertEqual(self.adapter.target.read_bytes(), original)
        row = next(r for r in connections.view()['connections'] if r['instance_id'] == self.adapter.id)
        self.assertFalse(row['executable_changed'])
        self.assertEqual(row['status'], 'environment_receipt_unconfirmed')
        self.assertTrue(row['awaiting_post_upgrade_receipt'])

    async def test_host_upgrade_review_preserves_verified_post_upgrade_receipt(self):
        import time
        from unittest.mock import patch
        executable = self.workspace / 'pi-upgraded'
        executable.write_text('#!/bin/sh\n')
        executable.chmod(0o700)
        past_ns = int((time.time() - 120) * 1e9)
        os.utime(executable, ns=(past_ns, past_ns))
        binary_mtime = executable.stat().st_mtime_ns / 1e9

        connections = Connections(self.registry, {**self.config, 'agent_hosts': {'pi': {
            'profile': str(self.profile), 'executable': str(executable)}}})
        prepared = await connections.perform(self.adapter.id, 'prepare', 0, 'up2-prepare')
        await connections.perform(self.adapter.id, 'activate', self.registry.state['revision'],
            'up2-activate', prepared['connection']['preview']['proposed_sha256'])
        record = self.registry.state['connections'][self.adapter.id]
        record['executable_fingerprint'] = [0, 0, 0]
        record['activated_at'] = binary_mtime - 60
        self.registry.state['sessions'][json.dumps([self.adapter.id, 'post-upgrade-session'])] = 'project'
        self.registry.state['connection_receipts'] = {self.adapter.id: {
            'received_at': binary_mtime + 10,
            'environment_verified': True,
            'profile': str(self.profile),
        }}
        with patch.object(NativeConnection, 'prepare', side_effect=AssertionError('must not install')):
            await connections.perform(self.adapter.id, 'review', self.registry.state['revision'], 'up2-review')
        row = next(r for r in connections.view()['connections'] if r['instance_id'] == self.adapter.id)
        self.assertEqual(record['activated_at'], binary_mtime)
        self.assertTrue(row['environment_verified'])
        self.assertEqual(row['status'], 'configured_pending_new_session')
        self.assertFalse(row['awaiting_post_upgrade_receipt'])

    async def test_helper_dedup_denied_watermark_and_unknown_enqueue_do_not_replay(self):
        await self.adapter.prepare(0)
        installation = self.registry.state['agents'][self.adapter.id]['installation']
        script = r'''
import json, os, pathlib, sys, subprocess
sys.path.insert(0, sys.argv[1])
import agent_memory_adapter as memory
import _memlane_policy as policy
import _coding_memory as coding
import _project_memory as projects
projects.begin = lambda *args: None
projects.prepare = lambda *args, **kwargs: {}
policy.bind = lambda *args: "00000000-0000-0000-0000-000000000001"
policy.current = lambda *args: {"ready": True, "policy":{"permissions":["read","write","delete"]}}
allowed = [True]
policy.permit = lambda *args, **kwargs: allowed[0]
coding._drain = lambda *args: None
delivered = []
coding._deliver = lambda *args: delivered.extend(args[-1])
request = {"operation":"capture", "session_id":"synthetic", "cwd":os.environ["HOME"],
           "event_id":"first", "entry":{"type":"qa","question":"synthetic prompt","answer":"synthetic answer"}}
os.environ["MEMLANE_PROCESSING_SESSION"] = "1"
assert memory.handle(request)["status"] == "skipped_processing_session"
os.environ.pop("MEMLANE_PROCESSING_SESSION")
def git(*args):
    return subprocess.check_output(["git", "-c", "user.name=Synthetic", "-c", "user.email=synthetic@example.com", *args], cwd=os.environ["HOME"], stderr=subprocess.DEVNULL, text=True).strip()
git("init", "-q")
git("commit", "--allow-empty", "-qm", "before")
before = git("rev-parse", "HEAD")
memory.handle({**request,"operation":"resolve","event_id":"turn-one"})
git("commit", "--allow-empty", "-qm", "after")
after = git("rev-parse", "HEAD")
assert memory.handle(request)["status"] == "buffered"
context = json.loads(delivered[0]["context"])
assert context["start_commit"] == before and context["end_commit"] == after and before != after
assert context["cwd"] == os.environ["HOME"] and context["session_cwd"] == os.environ["HOME"]
assert "transcript_path" not in context
assert memory.handle(request)["duplicate"]
assert len(delivered) == 1
assert os.environ["COGNEE_API_KEY"] == "synthetic-key-credential"
original_policy = policy.current
policy.current = lambda *args, **kwargs: {"ready": True, "policy":{"permissions":["read"]}}
for operation, extra, permission in [("remember", {"content":"denied synthetic"}, "write"), ("forget", {"data_id":"00000000-0000-0000-0000-000000000002"}, "delete")]:
    assert memory.execute({**request, "operation":operation, **extra}) == {"status":"denied", "http_status":403, "error":"project_"+permission+"_denied"}
def unavailable(*args, **kwargs): raise policy.NotReady("synthetic policy unavailable")
policy.current = unavailable
assert memory.execute({**request,"operation":"remember","content":"must remain unknown"}) == {"status":"unavailable","error":"memory_operation_unconfirmed"}
policy.current = original_policy
trace = {**request, "event_id":"tool-one", "entry":{"type":"trace", "origin_function":"bash",
         "method_params":{"command":"printf synthetic"}, "method_return_value":{"text":"synthetic"},
         "status":"success", "time":"2026-11-14T00:00:00Z"}}
assert memory.handle(trace)["status"] == "buffered"
assert delivered[-1]["method_params"] == {"command":"printf synthetic"}
assert delivered[-1]["generate_feedback_with_llm"] is False
assert isinstance(delivered[-1]["method_return_value"], str)
delivered.pop()
allowed[0] = False
memory.handle({**request,"operation":"resolve","event_id":"turn-denied"})
assert "host_turn_context" not in coding.read_state(memory.setup(request)[0])
request["event_id"] = "denied"
assert memory.handle(request)["status"] == "skipped_policy"
allowed[0] = True
assert memory.handle(request)["status"] == "skipped_policy"
assert len(delivered) == 1
def unknown(*args): raise RuntimeError("synthetic enqueue outcome unknown")
coding._deliver = unknown
request["event_id"] = "unknown"
try: memory.handle(request)
except RuntimeError: pass
else: raise AssertionError("enqueue should be unknown")
assert memory.handle(request)["status"] == "enqueue_unknown"
key, _, _ = memory.setup(request)
state = coding.read_state(key)
assert "synthetic answer" not in json.dumps(state)
assert "synthetic prompt" not in json.dumps(state)
assert len(state["host_events"]) == 4
import _plugin_common as common
submitted = []
common.run_session_improve_detailed = lambda *args, **kwargs: submitted.append(kwargs["trigger"]) or {"ok":True}
flush = {"operation":"flush", "session_id":"synthetic", "cwd":os.environ["HOME"], "final":True}
assert memory.handle(flush)["status"] == "processing_submitted"
assert memory.handle(flush)["status"] == "no_new_final_material"
assert submitted == ["final"]
print("helper metadata watermark and private binding passed")
'''
        environment = {key: value for key, value in __import__('os').environ.items()
                       if not key.startswith(('COGNEE_', 'MEMLANE_', 'LLM_', 'EMBEDDING_'))}
        environment.update(HOME=str(self.workspace), COGNEE_API_KEY='must-not-use-ambient-owner')
        process = await asyncio.to_thread(subprocess.run, [sys.executable, '-c', script,
                                             str(Path(installation['plugin_dir']) / 'scripts')],
                                          env=environment, cwd=self.workspace, capture_output=True, text=True, timeout=20)
        self.assertEqual(process.returncode, 0, process.stderr[-3000:])
        self.assertNotIn('must-not-use-ambient-owner', process.stdout)

    async def test_real_traex_installer_uses_isolated_user_profile(self):
        executable = shutil.which('traecli')
        if not executable:
            self.skipTest('TraeX host not installed')
        from unittest.mock import patch
        profile = self.workspace / 'traex-profile'
        profile.mkdir()
        adapter = NativeConnection(self.registry, self.config, 'traex', profile, executable)
        self.registry.state['agents'][adapter.id] = {
            'name': 'TraeX', 'key_file': str(self.workspace / 'key'), 'control_token_file': str(self.workspace / 'control')}
        prepared = await adapter.prepare(0)
        with patch.dict(os.environ, {'HOME': str(self.workspace)}):
            result = await adapter.activate(self.registry.state['revision'], prepared['preview']['proposed_sha256'])
        self.assertEqual(result['status'], 'configured_pending_new_session')
        self.assertTrue((profile / 'traecli.toml').is_file(), 'host ignored configured profile')
        text = (profile / 'traecli.toml').read_text()
        self.assertIn('memlane-cognee', text)
        self.assertNotIn('bypass', text)
        self.assertFalse((self.workspace / '.trae/traecli.toml').exists(), 'host wrote to an unexpected default profile')
        installation = self.registry.state['agents'][adapter.id]['installation']
        cache = adapter.traex_installed(installation)
        verify_artifact({**installation, 'plugin_dir': str(cache)})
        self.assertEqual(json.loads((cache / '.codex-plugin/plugin.json').read_text())['name'], 'memlane-cognee')
        self.assertIn('+memlane.', cache.name)
        # An interrupted manager can establish the original result by reading
        # the complete native receipt; review must never run the installer.
        self.registry.state['connections'][adapter.id]['status'] = 'activation_unknown'
        with patch('agent_connections.subprocess.run', side_effect=AssertionError('review replayed installation')):
            reviewed = await adapter.review(self.registry.state['revision'])
        self.assertEqual(reviewed['status'], 'configured_pending_new_session')
        original = (cache / 'hooks.json').read_bytes()
        (cache / 'hooks.json').write_bytes(b'{}')
        with self.assertRaisesRegex(PolicyError, 'connection_artifact_changed'):
            adapter.view()
        (cache / 'hooks.json').write_bytes(original)
        # Same instance, explicitly activated new generation: native versioned
        # cache must preserve the old files for an already-running host.
        adapter.config = {**self.config, 'python': '/usr/bin/python3'}
        second = await adapter.prepare(self.registry.state['revision'])
        with patch.dict(os.environ, {'HOME': str(self.workspace)}):
            await adapter.activate(self.registry.state['revision'], second['preview']['proposed_sha256'])
        newer = self.registry.state['agents'][adapter.id]['installation']
        self.assertNotEqual(adapter.traex_installed(newer), cache)
        verify_artifact({**installation, 'plugin_dir': str(cache)})
        adapter.target.write_text(text.replace('enabled = true', 'enabled = false'))
        with self.assertRaisesRegex(PolicyError, 'traex_installation_requires_review'):
            adapter.traex_installed(installation)

    async def test_real_import_order_initializes_matching_official_write_and_recall_scope(self):
        await self.adapter.prepare(0)
        installation = self.registry.state['agents'][self.adapter.id]['installation']
        script = r'''
import json, os, sys
sys.path.insert(0, sys.argv[1])
import agent_memory_adapter as memory
import _memlane_policy as policy
import _project_memory as projects
policy.bind = lambda *args: "00000000-0000-0000-0000-000000000001"
policy.permit = lambda *args, **kwargs: True
# Only the server capability answer is synthetic; official begin/route and
# production import order stay intact, unlike prematurely importing coding.
def prepare(dataset, session, **kwargs):
    from _plugin_common import _load_json_file, _write_json_file
    path = projects._path(dataset, session)
    state = _load_json_file(path)
    assert state["node_set"]
    state.update(pending=False, identity=projects._identity())
    _write_json_file(path, state)
    return state
projects.prepare = prepare
key, session, dataset = memory.setup({"operation":"status", "session_id":"scope-fixture", "cwd":os.environ["HOME"]})
assert key.startswith("pi-") and session == key, (key, session)
import _coding_memory as coding
import _plugin_common as common
expected = [projects.project_tag(os.environ["HOME"], "auto")]
assert coding.project_scope(dataset, session, os.environ["HOME"]) == expected
requests = []
common.require_typed_dataset_id_support = lambda: None
common._json_http_request = lambda path, payload, **kwargs: requests.append(payload) or {}
common.remember_entry_via_http(dataset, session, {"type":"qa", "question":"synthetic", "answer":"synthetic"})
assert requests[-1]["entry"]["node_set"] == expected
assert coding._project_ok({"project_node_sets":expected}, expected)
assert not coding._project_ok({"project_node_sets":[]}, expected)
print("official capture/recall project scope matches; old unscoped rows unchanged")
'''
        environment = {key: value for key, value in os.environ.items() if not key.startswith(('COGNEE_', 'MEMLANE_', 'LLM_', 'EMBEDDING_'))}
        environment['HOME'] = str(self.workspace)
        process = await asyncio.to_thread(subprocess.run, [sys.executable, '-c', script,
                                          str(Path(installation['plugin_dir']) / 'scripts')],
                                          env=environment, cwd=self.workspace, capture_output=True, text=True, timeout=20)
        self.assertEqual(process.returncode, 0, process.stderr[-3000:])

    async def test_traex_explicit_skill_resolves_hook_binding_with_shared_cwd(self):
        private_write(self.workspace / 'control', b'fixture-control-token-with-valid-length-0001')
        from project_installation import prepare
        installation = prepare(self.plugin, self.registry.path.parent, 'synthetic-traex',
            self.registry.state['agents'][self.adapter.id], 'http://127.0.0.1:18999', 'http://127.0.0.1:18998',
            str(self.workspace), host_kind='traex', python=sys.executable)
        script = r'''
import os, sys
sys.path.insert(0, sys.argv[1])
import agent_memory_adapter as memory
import _memlane_policy as policy
import _project_memory as projects
dataset = "00000000-0000-0000-0000-000000000001"
decision = {"ready":True, "revision":1, "project_id":"fixture-project", "dataset_id":dataset,
            "policy":{"permissions":["read","write","delete"], "can_capture":True, "can_auto_process":False}}
policy._request = lambda *a, **kw: decision
projects.prepare = lambda *a, **kw: {}
key, session, _ = memory.setup({"operation":"status", "session_id":"11111111-1111-4111-8111-111111111111", "cwd":os.environ["HOME"]})
assert key.startswith("traex-") and session == key, (key, session)
import _plugin_common as common
# Several hook launches at one cwd must never make a shell skill guess a session.
common.ensure_launch_record("other-host", os.environ["HOME"], dataset=dataset)
os.environ.pop("COGNEE_SESSION_KEY", None)
os.environ["TRAECLI_THREAD_ID"] = "11111111-1111-4111-8111-111111111111"
import _env_file
_env_file._loaded = False
_env_file.load_env_file()
resolved, reason = common.resolve_host_key_outside_hook()
assert resolved == key, (resolved, reason)
assert policy.dataset() == dataset
assert policy.permit("read")
print("explicit skill and hooks resolve one binding without cwd fallback")
'''
        environment = {key: value for key, value in os.environ.items() if not key.startswith(
            ('COGNEE_', 'MEMLANE_', 'LLM_', 'EMBEDDING_', 'TRAECLI_', 'CODEX_'))}
        environment['HOME'] = str(self.workspace)
        process = await asyncio.to_thread(subprocess.run, [sys.executable, '-c', script,
                                          str(Path(installation['plugin_dir']) / 'scripts')],
                                          env=environment, cwd=self.workspace, capture_output=True, text=True, timeout=20)
        self.assertEqual(process.returncode, 0, process.stderr[-3000:])

    async def test_trae_ide_installs_live_hooks_and_captures_only_observed_turns(self):
        profile = self.workspace / 'trae-ide-profile'
        profile.mkdir()
        adapter = NativeConnection(self.registry, self.config, 'trae_ide', profile, sys.executable)
        other = {'hooks': [{'command': 'echo existing'}]}
        baseline = {'version': 1, 'hooks': {'Stop': [other]}}
        adapter.target.write_text(json.dumps(baseline))
        self.registry.state['agents'][adapter.id] = dict(self.registry.state['agents'][self.adapter.id])
        self.registry.state['connections'] = {
            adapter.id: {'status': 'preparation_unknown', 'preview': adapter.preview(), 'trae_ide_hooks': None},
        }
        original = {'instance_id': adapter.id, 'action': 'prepare', 'status': 'unknown'}
        self.registry.state['connection_operations'] = {'original-prepare': original}
        self.assertEqual((await adapter.review(self.registry.state['revision']))['status'], 'not_connected')
        self.assertEqual(original['outcome'], 'rejected')
        self.assertEqual(original['status'], 'complete')
        self.assertEqual(json.loads(adapter.target.read_text()), baseline)
        prepared = await adapter.prepare(self.registry.state['revision'])
        self.assertEqual(json.loads(adapter.target.read_text()), baseline)
        receipt = await adapter.activate(self.registry.state['revision'], prepared['preview']['proposed_sha256'])
        self.assertEqual(receipt['entrypoints'], ['ide'])
        self.assertEqual(receipt['status'], 'configured_pending_new_session')
        config = json.loads(adapter.target.read_text())
        self.assertEqual(config['hooks']['Stop'][0], other)
        installation = self.registry.state['agents'][adapter.id]['installation']
        verify_artifact(installation)
        binding = json.loads((Path(installation['plugin_dir']) / '.memlane-installation.json').read_text())
        self.assertEqual(binding['runtime_profile'], str(profile))
        self.assertEqual(binding['hook_origin'], self.config['project_control']['url'])
        self.assertEqual(binding['hook_token_file'], self.registry.state['agents'][adapter.id]['control_token_file'])
        environment_receipt = self.registry.check_host_environment(adapter.id, {
            'instance_id': adapter.id, 'plugin_path': installation['plugin_dir'],
        })
        self.assertTrue(environment_receipt['environment_verified'])

        from aiohttp.test_utils import make_mocked_request
        from project_control_server import create_app

        manager_token = 'm' * 32
        ide_token = 't' * 32
        unrelated_token = 'u' * 32
        private_write(self.workspace / 'ide-token', ide_token.encode())
        self.registry.state['agents'][adapter.id]['control_token_file'] = str(self.workspace / 'ide-token')
        app = create_app(self.registry, manager_token, {'unrelated': unrelated_token}, codex_config=self.config)
        app.freeze()
        route = '/v1/trae-ide/hooks/UserPromptSubmit'
        payload = {'session_id': 'synthetic-session', 'cwd': str(profile), 'workspace_roots': [str(profile)],
                   'hook_event_name': 'UserPromptSubmit', 'prompt': 'synthetic prompt'}

        worker = type('Worker', (), {'returncode': 0, 'communicate': AsyncMock(return_value=(b'{}', b''))})()
        with patch('asyncio.create_subprocess_exec', new_callable=AsyncMock, return_value=worker) as execute:
            for token, status in ((None, 401), (manager_token, 403), (unrelated_token, 403), (ide_token, 200)):
                headers = {'Content-Type': 'application/json'}
                if token:
                    headers['Authorization'] = 'Bearer ' + token
                request = make_mocked_request('POST', route, app=app, headers=headers)
                request.read = AsyncMock(return_value=json.dumps(payload).encode())
                response = await app._handle(request)
                self.assertEqual(response.status, status, response.text)
            execute.assert_awaited_once()
            launch = execute.await_args
            self.assertEqual(launch.args[-2:], ('UserPromptSubmit', '--dispatch'))
            self.assertEqual(launch.kwargs['umask'], 0o077)

        script = r'''
import os, sys
sys.path.insert(0, sys.argv[1])
import trae_ide_hook as hook
import agent_memory_adapter as memory
import _coding_memory as coding
import _memlane_policy as policy
import _project_memory as projects
projects.begin = lambda *args: None
projects.prepare = lambda *args, **kwargs: {}
policy.bind = lambda *args: "00000000-0000-0000-0000-000000000001"
allowed = True
policy.permit = lambda *args, **kwargs: allowed
saved = []
coding._deliver = lambda dataset, session, entries: saved.extend(entries)
coding._drain = lambda *args: None
memory.handle = lambda request: {"context": "authorized synthetic context"}
base = {"session_id": "synthetic-ide-session", "cwd": os.environ["HOME"],
        "workspace_roots": [os.environ["HOME"]]}

def run(event, **fields):
    return hook.handle(event, {**base, "hook_event_name": event, **fields})

run("SessionStart")
run("Stop", last_assistant_message="reply from before connection")
assert saved == []
result = run("UserPromptSubmit", prompt="IDE question")
assert result["hookSpecificOutput"]["additionalContext"] == "authorized synthetic context"
run("PostToolUse", tool_use_id="tool-1", tool_name="Read", tool_response={"result":"synthetic"})
run("Stop", last_assistant_message="IDE answer")
run("Stop", last_assistant_message="duplicate answer")
assert [entry["type"] for entry in saved] == ["trace", "qa"]
assert saved[-1]["question"] == "IDE question" and saved[-1]["answer"] == "IDE answer"

run("UserPromptSubmit", prompt="revoked question")
allowed = False
run("Stop", last_assistant_message="revoked answer")
run("UserPromptSubmit", prompt="paused question")
allowed = True
run("Stop", last_assistant_message="paused answer")
assert run("UserPromptSubmit", prompt="ambiguous", workspace_roots=[base["cwd"], "/tmp"]) == {}
run("Stop", last_assistant_message="ambiguous answer")
assert len(saved) == 2
'''
        environment = {key: value for key, value in os.environ.items() if not key.startswith(('COGNEE_', 'MEMLANE_'))}
        environment['HOME'] = str(self.workspace)
        process = await asyncio.to_thread(subprocess.run, [sys.executable, '-B', '-c', script,
                                          str(Path(installation['plugin_dir']) / 'scripts')],
                                          env=environment, cwd=self.workspace, capture_output=True, text=True, timeout=20)
        self.assertEqual(process.returncode, 0, process.stderr[-3000:])

    async def test_traex_capture_recovers_interrupted_rollout_and_respects_boundary(self):
        from project_installation import prepare
        installation = prepare(self.plugin, self.registry.path.parent, 'synthetic-traex',
            self.registry.state['agents'][self.adapter.id], 'http://127.0.0.1:18999', 'http://127.0.0.1:18998',
            str(self.workspace), host_kind='traex', python=sys.executable)
        script = r'''
import json, os, sys
from pathlib import Path
sys.path.insert(0, sys.argv[1])
import traex_hook as hook
import agent_memory_adapter as memory
import _coding_memory as coding
import _memlane_policy as policy
import _project_memory as projects
projects.begin = lambda *args: None
projects.prepare = lambda *args, **kwargs: {}
os.environ["MEMLANE_PROCESSING_SESSION"] = "1"
assert hook.handle("SessionStart", {}) == {}
os.environ.pop("MEMLANE_PROCESSING_SESSION")
policy.bind = lambda *args: "00000000-0000-0000-0000-000000000001"
policy._connection = lambda: ("http://127.0.0.1:18998", "synthetic-control-token")
allowed = True
policy.permit = lambda *args, **kwargs: allowed
calls, saved = [], []
coding._deliver = lambda dataset, session, entries: saved.extend(entries)

def operation(request):
    calls.append(request)
    return {"context":"synthetic authorized context", "status":"buffered"}
memory.handle = operation
root = Path.home() / ".trae" / "cli" / "sessions"
root.mkdir(parents=True)
rollout = root / "rollout-synthetic-host-session.jsonl"
rollout.touch()
payload = {"session_id":"host-session", "cwd":os.environ["HOME"],
           "source":"startup", "transcript_path":str(rollout)}
assert hook.transcript_path(str(rollout), "other-session") is None
elsewhere = Path.home() / rollout.name
elsewhere.touch()
assert hook.transcript_path(str(elsewhere), "host-session") is None
symlink = root / "rollout-symlink-host-session.jsonl"
symlink.symlink_to(elsewhere)
assert hook.transcript_path(str(symlink), "host-session") is None

seq = 0
def event(kind, **fields):
    global seq
    seq += 1
    stamp = f"2026-11-23T02:03:{seq:02d}Z"
    with rollout.open("a") as f:
        f.write(json.dumps({"type":"event_msg","timestamp":stamp,"payload":{"type":kind,**fields}}) + "\n")
def item(kind, text):
    event("item_completed", item={"type":kind,"content":[{"type":"Text","text":text}]})
def prompt(text):
    result = hook.handle("UserPromptSubmit", {**payload,"prompt":text})
    assert result["hookSpecificOutput"]["additionalContext"] == "synthetic authorized context"
    item("UserMessage", text)

# Startup may precede creation of the native rollout.
hook.handle("SessionStart", {**payload,"transcript_path":None})
prompt("interrupted prompt")
item("AgentMessage", "progress reply")
event("turn_aborted", reason="interrupted")
assert saved == []
prompt("continue")
item("AgentMessage", "final reply")
assert hook.handle("Stop", {**payload,"last_assistant_message":"final reply"})["qa"] == 2
assert [row["question"] for row in saved] == ["interrupted prompt", "continue"]
assert "progress reply" in saved[0]["answer"]
assert json.loads(saved[0]["context"])["interrupted"] is True
assert calls[-1]["operation"] == "flush"
assert hook.handle("Stop", payload)["qa"] == 0
hook.handle("PostToolUseFailure", {**payload,"tool_use_id":"native-tool-id","tool_name":"bash",
            "tool_input":{"command":"synthetic"},"tool_response":{"exit_code":1}})
assert calls[-1]["entry"]["status"] == "error"
assert calls[-1]["event_id"] == "tool:native-tool-id"

prompt("request with a reply")
item("AgentMessage", "response before steering")
prompt("steering with no reply")
prompt("last steering request")
item("AgentMessage", "response after steering")
assert hook.handle("Stop", payload)["qa"] == 2
assert [row["question"] for row in saved[-2:]] == ["request with a reply", "last steering request"]

allowed = False
prompt("private question")
item("AgentMessage", "private reply")
hook.handle("Stop", payload)
allowed = True
prompt("resume boundary")
item("AgentMessage", "allowed reply")
hook.handle("Stop", payload)
assert "private question" not in [row["question"] for row in saved]

# Final capture still works when a shutdown hook omits the transcript path.
prompt("last question")
item("AgentMessage", "last reply")
hook.handle("SessionEnd", {**payload,"transcript_path":None})
assert saved[-1]["question"] == "last question"
assert calls[-1]["final"] is True

# Resuming a session first seen by this generation never replays its old turns.
rollout = root / "rollout-synthetic-old-session.jsonl"
payload.update(session_id="old-session", source="resume", transcript_path=str(rollout))
item("UserMessage", "already saved")
item("AgentMessage", "old reply")
hook.handle("SessionStart", payload)
event("task_started")
prompt("after upgrade")
item("AgentMessage", "fresh reply")
hook.handle("Stop", payload)
assert saved[-1]["question"] == "after upgrade"
assert "already saved" not in [row["question"] for row in saved]
'''
        environment = {key: value for key, value in os.environ.items() if not key.startswith(('COGNEE_', 'MEMLANE_', 'LLM_', 'EMBEDDING_'))}
        environment['HOME'] = str(self.workspace)
        process = await asyncio.to_thread(subprocess.run, [sys.executable, '-c', script,
                                          str(Path(installation['plugin_dir']) / 'scripts')],
                                          env=environment, cwd=self.workspace, capture_output=True, text=True, timeout=20)
        self.assertEqual(process.returncode, 0, process.stderr[-3000:])

    async def test_prepare_routes_host_traffic_to_control_gateway_and_proxies_with_child_key(self):
        from aiohttp import web
        from aiohttp.test_utils import TestClient, TestServer
        from project_control_server import create_app
        import uuid

        owner_ds = str(uuid.uuid4())
        allowed_ds = str(uuid.uuid4())
        denied_ds = str(uuid.uuid4())
        seen_headers = []

        async def handle_health(request):
            seen_headers.append(('health', request.headers.get('X-Api-Key')))
            return web.json_response({'status': 'ready'})

        async def handle_datasets(request):
            seen_headers.append(('datasets', request.headers.get('X-Api-Key')))
            return web.json_response([
                {'id': allowed_ds, 'name': 'allowed'},
                {'id': denied_ds, 'name': 'denied'},
                {'id': owner_ds, 'name': 'other'},
            ])

        official_app = web.Application()
        official_app.router.add_get('/health', handle_health)
        official_app.router.add_get('/api/v1/datasets', handle_datasets)
        official_server = TestServer(official_app)
        await official_server.start_server()
        try:
            official_url = str(official_server.make_url('')).rstrip('/')
            self.registry.official = type('OfficialFixture', (), {'url': official_url})()
            # Pre-seed valid project records on the registry.
            self.registry.state['projects']['proj-allowed'] = {
                'id': 'proj-allowed', 'name': 'Allowed', 'dataset_id': allowed_ds,
                'dataset_name': 'ml-project-allowed', 'status': 'verified',
                'desired_revision': 1, 'applied': {self.adapter.id: {'status': 'verified', 'revision': 1}},
                'overrides': {self.adapter.id: {'access': 'none'}},
                'aliases': [], 'code_enabled': False, 'code_configured': False,
            }
            self.registry.state['projects']['proj-denied'] = {
                'id': 'proj-denied', 'name': 'Denied', 'dataset_id': denied_ds,
                'dataset_name': 'ml-project-denied', 'status': 'verified',
                'desired_revision': 1, 'applied': {self.adapter.id: {'status': 'verified', 'revision': 1}},
                'overrides': {self.adapter.id: {'access': 'none'}},
                'aliases': [], 'code_enabled': False, 'code_configured': False,
            }
            self.registry.save()
            client = TestClient(TestServer(create_app(self.registry, 'm' * 40, {}, codex_config=self.config)))
            await client.start_server()
            try:
                control_url = str(client.make_url('')).rstrip('/')
                self.adapter.config = {
                    **self.config,
                    'project_control': {'plugin_dir': str(self.plugin), 'url': control_url},
                }
                await self.adapter.prepare(self.registry.state['revision'])
                env_file = Path(self.registry.state['agents'][self.adapter.id]['installation']['env_file'])
                env_text = env_file.read_text()
                self.assertIn(f'COGNEE_BASE_URL="{control_url}/cognee"', env_text)
                self.assertNotIn(f'COGNEE_BASE_URL="{official_url}"', env_text)
                child_key = (self.workspace / 'key').read_text().strip()
                # /cognee/health forwards child key and responds 200.
                resp = await client.get('/cognee/health', headers={'X-Api-Key': child_key})
                self.assertEqual(resp.status, 200, await resp.text())
                # Before project grant, /cognee/api/v1/datasets filters out unauthorized datasets.
                resp = await client.get('/cognee/api/v1/datasets', headers={'X-Api-Key': child_key})
                self.assertEqual(resp.status, 200, await resp.text())
                self.assertEqual(await resp.json(), [])
                # Grant explicit read on proj-allowed; only allowed_ds is visible through gateway.
                self.registry.state['projects']['proj-allowed']['overrides'][self.adapter.id] = {'access': 'read'}
                self.registry.save()
                resp = await client.get('/cognee/api/v1/datasets', headers={'X-Api-Key': child_key})
                self.assertEqual(resp.status, 200, await resp.text())
                rows = await resp.json()
                self.assertEqual([r['id'] for r in rows], [allowed_ds])
                self.assertTrue(any(key == child_key for _, key in seen_headers))
            finally:
                await client.close()
        finally:
            await official_server.close()

    async def test_component_source_available_checks_archive_and_plugin_dir_sibling(self):
        from unittest.mock import patch
        from antigravity_connection import AntigravityConnection
        import project_host_plugin

        manifest = json.loads((ROOT / 'engine/coding-memory-components.json').read_text())['plugin']
        real_archive = ROOT / '.cache/coding-memory-components' / (manifest['commit'] + '.tar.gz')
        if not real_archive.is_file():
            self.skipTest('pinned integration archive not cached locally')

        empty_repo = self.workspace / 'empty-repo'
        empty_repo.mkdir()
        bundle_root = self.workspace / 'packaged-runtime'
        plugin_dir = bundle_root / 'managed-plugin'
        plugin_dir.mkdir(parents=True)

        def make_adapter(kind, cfg):
            profile = self.workspace / ('p-' + kind)
            if kind == 'antigravity':
                return AntigravityConnection(self.registry, cfg, profile, sys.executable)
            return NativeConnection(self.registry, cfg, kind, profile, sys.executable)

        with patch.object(project_host_plugin, 'ROOT', empty_repo):
            project_host_plugin._ARCHIVE_DIGEST_CACHE.clear()
            for kind in ('claude_code', 'cursor', 'antigravity'):
                cfg = {**self.config, 'project_control': {'url': 'http://127.0.0.1:18998', 'plugin_dir': str(plugin_dir)}}
                adapter = make_adapter(kind, cfg)
                self.assertFalse(adapter.component_source_available(), f'{kind} must report unavailable without archive')
                self.assertEqual(adapter.view()['status'], 'component_unavailable')

            sibling = bundle_root / 'integrations-upstream' / 'service.tar.gz'
            sibling.parent.mkdir(parents=True)
            shutil.copyfile(real_archive, sibling)
            project_host_plugin._ARCHIVE_DIGEST_CACHE.clear()
            for kind in ('claude_code', 'cursor', 'antigravity'):
                cfg = {**self.config, 'project_control': {'url': 'http://127.0.0.1:18998', 'plugin_dir': str(plugin_dir)}}
                adapter = make_adapter(kind, cfg)
                self.assertTrue(adapter.component_source_available(), f'{kind} must resolve plugin_dir sibling archive')


if __name__ == '__main__':
    unittest.main()
