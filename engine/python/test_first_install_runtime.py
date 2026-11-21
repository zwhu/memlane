import io
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

import first_install_runtime as runtime
from first_install_store import write_private


class FirstRuntimeFixture:
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.parent = Path(self.temporary.name).resolve()
        self.root = self.parent / 'library'
        self.root.mkdir(mode=0o700)
        commit = json.loads((runtime.ROOT / 'engine/coding-memory-components.json').read_text())['service']['commit']
        self.spec = {'schema': 1, 'store_id': 'a' * 32, 'source_commit': commit, 'model_digest': 'b' * 64,
                     'root': str(self.root), 'source': '/synthetic/source', 'archive': '/synthetic/archive',
                     'model_cache': '/synthetic/model'}
        write_private(self.root / '.memlane-first-store.json', json.dumps({key: self.spec[key] for key in
                      ('schema', 'store_id', 'source_commit', 'model_digest')}))
        write_private(self.root / 'first-install-input.json', json.dumps(self.spec))
        self.receipt = {'schema': 1, 'store_id': self.spec['store_id'], 'source_commit': commit,
                        'storage_initialized': True, 'owner_id': 'synthetic-owner', 'key_id': 'synthetic-key'}
        write_private(self.root / 'store-identity.json', json.dumps(self.receipt))
        write_private(self.root / 'owner.token', 'synthetic-owner-key-only-for-protocol-fixture')
        self.path = self.parent / 'runtime.json'

    def prepare(self, *, graph_assets=None):
        with patch('dev.validate_project_control'), patch('agent_connections.detect_executable', return_value=None):
            return runtime.prepare_runtime(self.root, self.path, '/synthetic/plugin', python='/synthetic/python', graph_assets=graph_assets)


class FirstRuntimeTests(FirstRuntimeFixture, unittest.TestCase):

    def test_fresh_configuration_has_no_default_password_or_generation_profile(self):
        config = self.prepare()
        self.assertEqual(config['generation_mode'], 'unconfigured')
        self.assertEqual(config['model'], '')
        self.assertTrue(config['first_install']['setup'])
        self.assertFalse((self.root / 'generation.json').exists())
        self.assertFalse((self.root / 'client-state.json').exists())
        environment = json.loads(runtime.read_private(config['service_environment_file']))
        self.assertNotIn('DEFAULT_USER_PASSWORD', environment)
        self.assertFalse(any(key.startswith('LLM_') for key in environment))
        self.assertEqual(len({config['service_url'], config['completion_url'], config['project_control']['url']}), 3)
        for path in (self.path, Path(config['service_environment_file']),
                     Path(config['completion_token_file']), Path(config['project_control']['management_token_file'])):
            self.assertEqual(path.stat().st_mode & 0o077, 0)

    def test_reentry_keeps_original_ports_secrets_and_generation(self):
        first = self.prepare()
        original = self.path.read_bytes()
        token = runtime.read_private(first['project_control']['management_token_file'])
        with patch('dev.load_config', side_effect=lambda path: json.loads(runtime.read_private(path))):
            second = runtime.prepare_runtime(self.root, self.path, '/different/plugin')
        self.assertEqual(first, second)
        self.assertEqual(self.path.read_bytes(), original)
        self.assertEqual(runtime.read_private(second['project_control']['management_token_file']), token)

    def test_existing_daily_configuration_is_preserved(self):
        original = '{"official_data":"/synthetic/existing-daily"}'
        write_private(self.path, original)
        with self.assertRaises(ValueError):
            self.prepare()
        self.assertEqual(self.path.read_text(), original)
        self.assertFalse((self.root / 'management.token').exists())

    def test_incompatible_partial_environment_is_not_reset(self):
        original = '{"EMBEDDING_MODEL":"different-existing-model"}'
        write_private(self.root / 'service-environment.json', original)
        with self.assertRaises(ValueError):
            self.prepare()
        self.assertEqual((self.root / 'service-environment.json').read_text(), original)
        self.assertFalse(self.path.exists())

    def test_public_health_is_insufficient_without_expected_official_owner(self):
        config = self.prepare()
        with patch('urllib.request.urlopen', return_value=io.BytesIO(b'{"id":"another-owner"}')) as open_request:
            open_request.return_value.status = 200
            with self.assertRaises(ValueError):
                runtime.confirm_service_owner(config)
        with patch('urllib.request.urlopen', return_value=io.BytesIO(b'{"id":"synthetic-owner"}')) as open_request:
            open_request.return_value.status = 200
            runtime.confirm_service_owner(config)

    def test_service_boot_stops_at_owner_mismatch(self):
        config = self.prepare()
        from dev import first_install_runtime
        launched = []
        with patch('first_install_runtime.confirm_service_owner', side_effect=ValueError('wrong owner')):
            with self.assertRaises(ValueError):
                first_install_runtime(config, self.path, lambda args: launched.append(args), health=lambda *args: None)
        self.assertEqual(len(launched), 1)
        self.assertFalse((self.root / 'client-state.json').exists())

    def test_host_home_is_passed_explicitly_without_mutating_process_environ(self):
        import os
        import sys
        fake_home = self.parent / 'host-home'
        codex_bin = fake_home / '.local/bin/codex'
        codex_bin.parent.mkdir(parents=True)
        codex_bin.write_text('#!/bin/sh\nexit 0\n', encoding='utf-8')
        codex_bin.chmod(0o700)
        sandbox_home = self.parent / 'sandbox-home'
        sandbox_home.mkdir()
        python_bin = self.parent / 'fake-python'
        python_bin.write_text('#!/bin/sh\nexit 0\n', encoding='utf-8')
        python_bin.chmod(0o700)
        exec_calls = []

        def fake_detect(kind, settings, legacy=None):
            return settings.get('executable')

        with patch.dict(os.environ, {'HOME': str(sandbox_home)}, clear=False), \
                patch('dev.validate_project_control'), \
                patch('agent_connections.detect_executable', side_effect=fake_detect), \
                patch('os.execve', side_effect=lambda *args: exec_calls.append(args)), \
                patch.object(sys, 'argv', [
                    'first_install_runtime.py',
                    '--store-root', str(self.root),
                    '--runtime-config', str(self.path),
                    '--plugin', '/synthetic/plugin',
                    '--host-home', str(fake_home),
                    '--python-executable', str(python_bin),
                ]):
            runtime.main()
            self.assertEqual(os.environ['HOME'], str(sandbox_home))

        config = json.loads(runtime.read_private(self.path))
        self.assertEqual(config['codex_executable'], str(codex_bin))
        self.assertEqual(config['auth_home'], str(fake_home / '.codex'))
        self.assertEqual(len(exec_calls), 1)
        self.assertEqual(exec_calls[0][2]['HOME'], str(fake_home))
        self.assertEqual(exec_calls[0][2]['PYTHONDONTWRITEBYTECODE'], '1')

    def test_graph_assets_defaults_to_repo_bundle_and_accepts_explicit_cli_flag(self):
        import os
        import sys
        auto_cfg = self.prepare()
        expected_repo_dist = str(runtime.ROOT / 'web/official-graph/dist')
        if (runtime.ROOT / 'web/official-graph/dist/assets.json').is_file():
            self.assertEqual(auto_cfg['graph_assets'], expected_repo_dist)
        else:
            self.assertEqual(auto_cfg['graph_assets'], '')

        # Fresh config with explicit --graph-assets CLI flag writes the path and reentry preserves it.
        cfg_path = self.parent / 'explicit-runtime.json'
        fake_home = self.parent / 'host-home-2'
        fake_home.mkdir()
        explicit_assets = self.parent / 'embedded-graph-dist'
        explicit_assets.mkdir()
        python_bin = self.parent / 'fake-python-2'
        python_bin.write_text('#!/bin/sh\nexit 0\n', encoding='utf-8')
        python_bin.chmod(0o700)
        with patch.dict(os.environ, {'HOME': str(self.parent)}, clear=False), \
                patch('dev.validate_project_control'), \
                patch('agent_connections.detect_executable', return_value=None), \
                patch('os.execve'), \
                patch.object(sys, 'argv', [
                    'first_install_runtime.py',
                    '--store-root', str(self.root),
                    '--runtime-config', str(cfg_path),
                    '--plugin', '/synthetic/plugin',
                    '--host-home', str(fake_home),
                    '--python-executable', str(python_bin),
                    '--graph-assets', str(explicit_assets),
                ]):
            runtime.main()
        written = cfg_path.read_bytes()
        parsed = json.loads(written)
        self.assertEqual(parsed['graph_assets'], str(explicit_assets))
        with patch('dev.load_config', side_effect=lambda path: json.loads(runtime.read_private(path))):
            reentered = runtime.prepare_runtime(self.root, cfg_path, '/synthetic/plugin', graph_assets=self.parent / 'other-assets')
        self.assertEqual(reentered['graph_assets'], str(explicit_assets))
        self.assertEqual(cfg_path.read_bytes(), written)
