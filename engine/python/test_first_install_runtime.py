from http.client import HTTPMessage
import io
import json
import os
from pathlib import Path
import sys
import tempfile
import unittest
import urllib.error
import urllib.response
from unittest.mock import patch

import first_install_runtime as runtime
from first_install_store import write_private


FIXED_ORIGINS = ['http://127.0.0.1:41001', 'http://127.0.0.1:41002', 'http://127.0.0.1:41003']


class FirstRuntimeFixture:
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.enterContext(patch.object(runtime, 'unused_origins', return_value=FIXED_ORIGINS))
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

    def test_existing_only_cli_requires_runtime_and_validates_existing_files_without_writes(self):
        argv = ['first_install_runtime.py', '--store-root', str(self.root), '--runtime-config', str(self.path),
                '--plugin', '/synthetic/plugin', '--prepare-only', '--skip-graph-refresh', '--existing-only']
        environment = self.root / 'service-environment.json'
        before = set(self.root.iterdir())
        with patch.object(sys, 'argv', argv):
            with self.assertRaisesRegex(ValueError, 'existing first runtime configuration required'):
                runtime.main()
        self.assertFalse(self.path.exists())
        self.assertFalse(environment.exists())
        self.assertEqual(set(self.root.iterdir()), before)

        initial = self.prepare()
        files = {path: (path.read_bytes(), path.stat().st_mtime_ns) for path in (self.path, *self.root.iterdir())}
        with (patch.object(sys, 'argv', argv),
              patch('dev.load_config', side_effect=lambda path: json.loads(runtime.read_private(path))) as load):
            runtime.main()
            load.assert_called_once_with(self.path)
        self.assertEqual(json.loads(self.path.read_bytes()), initial)
        self.assertEqual(set(self.root.iterdir()), set(files) - {self.path})
        for path, snapshot in files.items():
            self.assertEqual((path.read_bytes(), path.stat().st_mtime_ns), snapshot)

    def test_reentry_refreshes_managed_graph_assets_and_preserves_concurrent_settings(self):
        old = self.parent / ('runtime-' + 'a' * 16) / 'web/official-graph/dist'
        new = self.parent / ('runtime-' + 'b' * 16) / 'web/official-graph/dist'
        new.mkdir(parents=True)
        (new / 'assets.json').write_text('{}')
        initial = self.prepare(graph_assets=old)
        for previous in (str(old), ''):
            with self.subTest(previous=previous):
                config = {**initial, 'graph_assets': previous}
                write_private(self.path, json.dumps(config))
                read_private = runtime.read_private
                compared = False

                def update_before_compare(path):
                    nonlocal compared
                    if path == self.path and not compared:
                        compared = True
                        original = read_private(path)
                        write_private(path, json.dumps({**json.loads(original), 'user_setting': 'concurrent'}))
                        return original
                    return read_private(path)

                with patch('dev.load_config', side_effect=lambda path: json.loads(read_private(path))), \
                     patch.object(runtime, 'read_private', side_effect=update_before_compare):
                    result = runtime.prepare_runtime(self.root, self.path, '/synthetic/plugin', graph_assets=new)
                self.assertEqual(result, {**config, 'graph_assets': str(new), 'user_setting': 'concurrent'})

    def test_skip_graph_refresh_preserves_existing_bytes_and_checks_store_identity(self):
        old = self.parent / ('runtime-' + 'a' * 16) / 'web/official-graph/dist'
        new = self.parent / ('runtime-' + 'b' * 16) / 'web/official-graph/dist'
        new.mkdir(parents=True)
        (new / 'assets.json').write_text('{}')
        initial = self.prepare(graph_assets=old)
        before = self.path.read_bytes(), self.path.stat().st_mtime_ns
        argv = ['first_install_runtime.py', '--store-root', str(self.root), '--runtime-config', str(self.path),
                '--plugin', '/synthetic/plugin', '--graph-assets', str(new), '--prepare-only', '--skip-graph-refresh']

        def prepare_without_refresh():
            return runtime.prepare_runtime(self.root, self.path, '/synthetic/plugin', graph_assets=new,
                                           skip_graph_refresh=True)

        with (patch('dev.load_config', side_effect=lambda path: json.loads(runtime.read_private(path))) as load,
              patch.object(sys, 'argv', argv)):
            self.assertEqual(prepare_without_refresh(), initial)
            self.assertEqual((self.path.read_bytes(), self.path.stat().st_mtime_ns), before)
            runtime.main()
            self.assertEqual((self.path.read_bytes(), self.path.stat().st_mtime_ns), before)
            self.assertEqual(load.call_count, 2)
            for change in ({'first_install': {'store_id': 'different'}}, {'official_data': '/wrong/store'},
                           {'service_commit': 'different'}, {'token_file': '/wrong/token'}):
                with self.subTest(change=change):
                    altered = {**initial, **change}
                    write_private(self.path, json.dumps(altered))
                    before = self.path.read_bytes(), self.path.stat().st_mtime_ns
                    for entrypoint in (prepare_without_refresh, runtime.main):
                        with self.assertRaisesRegex(ValueError, 'existing configuration belongs to another installation'):
                            entrypoint()
                        self.assertEqual((self.path.read_bytes(), self.path.stat().st_mtime_ns), before)
            self.assertEqual(load.call_count, 2)

    def test_reentry_preserves_custom_path_bytes_and_rejects_linked_runtime(self):
        custom = self.parent / 'embedded-graph-dist'
        config = self.prepare(graph_assets=custom)
        before = self.path.read_bytes()
        new = self.parent / ('runtime-' + 'b' * 16) / 'web/official-graph/dist'
        new.mkdir(parents=True)
        (new / 'assets.json').write_text('{}')
        with patch('dev.load_config', side_effect=lambda path: json.loads(path.read_text())):
            self.assertEqual(runtime.prepare_runtime(self.root, self.path, '/synthetic/plugin', graph_assets=new), config)
        self.assertEqual(self.path.read_bytes(), before)

        linked = self.parent / ('runtime-' + 'a' * 16)
        linked.symlink_to(new.parents[2])
        self.assertFalse(runtime._is_managed_runtime_graph_assets(linked / 'web/official-graph/dist', self.parent))
        for invalid in (custom, new / '..' / 'dist', self.parent / 'other' / linked.name / 'graph'):
            self.assertFalse(runtime._is_managed_runtime_graph_assets(invalid, self.parent))

    def test_reentry_refreshes_nested_and_external_marked_app_generations(self):
        for index, install_root in enumerate((self.parent / 'components/installation', self.parent / 'external')):
            with self.subTest(root=install_root):
                generations = []
                for digest in ('a' * 64, 'b' * 64):
                    directory = install_root / ('runtime-' + digest[:16])
                    graph = directory / 'web/official-graph/dist'
                    graph.mkdir(parents=True)
                    (graph / 'assets.json').write_text('{}')
                    write_private(directory / '.memlane-runtime.json', json.dumps({'sha256': digest}))
                    generations.append(graph)
                old, new = generations
                config_path = self.parent / f'runtime-{index}.json'
                with patch('dev.validate_project_control'), patch('agent_connections.detect_executable', return_value=None):
                    config = runtime.prepare_runtime(self.root, config_path, '/synthetic/plugin',
                                                     python='/synthetic/python', graph_assets=old)
                with patch('dev.load_config', side_effect=lambda path: json.loads(path.read_text())):
                    result = runtime.prepare_runtime(self.root, config_path, '/synthetic/plugin', graph_assets=new)
                self.assertEqual(result, {**config, 'graph_assets': str(new)})

    def test_reentry_rejects_missing_assets_and_tampered_markers(self):
        old = self.parent / ('runtime-' + 'a' * 16) / 'web/official-graph/dist'
        config = self.prepare(graph_assets=old)
        new = self.parent / ('runtime-' + 'b' * 16) / 'web/official-graph/dist'
        new.mkdir(parents=True)
        marker = new.parents[2] / '.memlane-runtime.json'
        with patch('dev.load_config', side_effect=lambda path: json.loads(path.read_text())):
            for case in ('missing assets', 'mismatched marker', 'public marker', 'linked marker', 'tampered old marker'):
                with self.subTest(case=case):
                    if case == 'mismatched marker':
                        (new / 'assets.json').write_text('{}')
                        write_private(marker, json.dumps({'sha256': 'c' * 64}))
                    elif case == 'public marker':
                        write_private(marker, json.dumps({'sha256': 'b' * 64}))
                        marker.chmod(0o644)
                    elif case == 'linked marker':
                        marker.rename(marker.with_name('other-marker'))
                        marker.symlink_to(marker.with_name('other-marker'))
                    elif case == 'tampered old marker':
                        marker.unlink()
                        old.mkdir(parents=True)
                        write_private(old.parents[2] / marker.name, '{}')
                    self.assertEqual(runtime.prepare_runtime(self.root, self.path, '/synthetic/plugin',
                                                             graph_assets=new), config)

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
        with patch('urllib.request.build_opener') as opener:
            for owner in ('another-owner', 'synthetic-owner'):
                response = io.BytesIO(json.dumps({'id': owner}).encode())
                response.status = 200
                opener.return_value.open.return_value = response
                if owner == 'another-owner':
                    with self.assertRaisesRegex(ValueError, 'authenticated first service owner differs'):
                        runtime.confirm_service_owner(config)
                else:
                    runtime.confirm_service_owner(config)

    def test_owner_confirmation_ignores_proxy_environment_and_refuses_redirect(self):
        config = self.prepare()
        headers = HTTPMessage()
        headers['Location'] = 'http://outside.invalid/collect'
        response = urllib.response.addinfourl(io.BytesIO(b''), headers, config['service_url'], 302)
        response.msg = 'Found'
        proxy = 'http://proxy.invalid:8080'
        environment = {'HTTP_PROXY': proxy, 'HTTPS_PROXY': proxy}
        build_opener = runtime.urllib.request.build_opener
        with (patch.dict(os.environ, environment, clear=True),
              patch('urllib.request.build_opener', wraps=build_opener) as opener,
              patch('urllib.request.HTTPHandler.http_open', return_value=response) as http_open):
            with self.assertRaises(urllib.error.HTTPError) as error:
                runtime.confirm_service_owner(config)
        self.assertEqual(error.exception.code, 302)
        proxy_handler = opener.call_args.args[0]
        self.assertIsInstance(proxy_handler, runtime.urllib.request.ProxyHandler)
        self.assertEqual(proxy_handler.proxies, {})
        http_open.assert_called_once()
        request = http_open.call_args.args[0]
        self.assertEqual(request.full_url, config['service_url'] + '/api/v1/users/me')
        self.assertEqual(request.host, '127.0.0.1:41001')
        self.assertEqual(request.get_header('X-api-key'), runtime.read_private(config['token_file']).strip())

    def test_service_boot_stops_at_owner_mismatch(self):
        config = self.prepare()
        from dev import first_install_runtime
        launched = []
        with (patch('dev.port_free'),
              patch('first_install_runtime.confirm_service_owner', side_effect=ValueError('wrong owner'))):
            with self.assertRaisesRegex(ValueError, 'wrong owner'):
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
