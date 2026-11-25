from __future__ import annotations

import fcntl
import json
import os
from pathlib import Path
import sys
import tempfile
import unittest

sys.path.insert(0, str(Path(__file__).resolve().parent))
import utm_guard
import utm_isolated_env as iso


class IsolatedEnvTests(unittest.TestCase):
    def setUp(self):
        tmp = tempfile.TemporaryDirectory()
        self.addCleanup(tmp.cleanup)
        self.root = Path(tmp.name).resolve()
        self.home = self.root / 'home'
        self.home.mkdir(mode=0o700)
        self.exe = self.root / 'Memlane'
        self.exe.write_bytes(b'#!/bin/sh\necho ok\n')
        self.exe.chmod(0o700)
        self.isolated = self.root / 'isolated-run'

    def _make_pool(self) -> Path:
        pool = self.root / 'pool' / 'components'
        venv_bin = pool / 'runtime-abc123' / 'venv' / 'bin'
        venv_bin.mkdir(parents=True)
        (pool / 'runtime-abc123' / 'venv' / 'pyvenv.cfg').write_text(
            f'home = {pool}/python-3.12/bin\nexecutable = {pool}/runtime-abc123/venv/bin/python\n',
            encoding='utf-8',
        )
        script = venv_bin / 'cognee-cli'
        script.write_text(f'#!{pool}/runtime-abc123/venv/bin/python\nprint("hi")\n', encoding='utf-8')
        script.chmod(0o755)
        binary = venv_bin / 'native-bin'
        binary.write_bytes(b'\xcf\xfa\xed\xfe\x00\x01binary-payload')
        binary.chmod(0o755)
        os.symlink(str(pool / 'python-3.12' / 'bin' / 'python3.12'), venv_bin / 'python')
        return pool

    def test_clone_components_apfs_and_relocates_venv_without_mutating_pool(self):
        pool = self._make_pool()
        iso.prepare_isolated_env(self.isolated, self.exe, scenario='clean-install', home=self.home, components_source='apfs_clone_pool')
        target = self.isolated / 'components'
        result = iso.clone_components_apfs(pool, target, home=self.home, allowed_root=self.isolated)
        self.assertEqual(result['clone_mode'], 'apfs_cow')
        self.assertEqual(result['relocated_files'], 3)
        # Cloned venv cfg, script shebang, and symlink must point to target; binary untouched.
        cfg_text = (target / 'runtime-abc123' / 'venv' / 'pyvenv.cfg').read_text(encoding='utf-8')
        self.assertIn(str(target), cfg_text)
        self.assertNotIn(str(pool), cfg_text)
        shebang = (target / 'runtime-abc123' / 'venv' / 'bin' / 'cognee-cli').read_text(encoding='utf-8')
        self.assertTrue(shebang.startswith(f'#!{target}/runtime-abc123/venv/bin/python\n'))
        self.assertEqual(os.readlink(target / 'runtime-abc123' / 'venv' / 'bin' / 'python'),
                         f'{target}/python-3.12/bin/python3.12')
        self.assertEqual((target / 'runtime-abc123' / 'venv' / 'bin' / 'native-bin').read_bytes(),
                         b'\xcf\xfa\xed\xfe\x00\x01binary-payload')
        # Read-only pool remains untouched.
        self.assertIn(str(pool), (pool / 'runtime-abc123' / 'venv' / 'pyvenv.cfg').read_text(encoding='utf-8'))

    def test_clone_components_apfs_failure_and_explicit_fallback(self):
        pool = self._make_pool()
        iso.prepare_isolated_env(self.isolated, self.exe, scenario='clean-install', home=self.home)
        target = self.isolated / 'components'
        with self.assertRaises(iso.IsolatedEnvError) as ctx:
            iso.clone_components_apfs(pool, target, home=self.home, allowed_root=self.isolated, cp_cmd=('/usr/bin/false',))
        self.assertIn('APFS CoW clone failed', str(ctx.exception))
        self.assertFalse(target.exists())

        fallback = iso.clone_components_apfs(
            pool, target, home=self.home, allowed_root=self.isolated,
            cp_cmd=('/usr/bin/false',), allow_copy_fallback=True,
        )
        self.assertEqual(fallback['clone_mode'], 'copy_fallback')
        self.assertEqual(fallback['relocated_files'], 3)
        self.assertIn(str(target), (target / 'runtime-abc123' / 'venv' / 'pyvenv.cfg').read_text(encoding='utf-8'))

    def test_fingerprint_drift_returns_exit_33(self):
        iso.prepare_isolated_env(
            self.isolated, self.exe, scenario='clean-install', home=self.home,
            bundled_runtime_hash='1111222233334444', component_versions={'cognee': '1.6.7'},
        )
        # Executable SHA drift -> exit 33
        other_exe = self.root / 'Memlane-v2'
        other_exe.write_bytes(b'#!/bin/sh\necho v2\n')
        other_exe.chmod(0o700)
        with self.assertRaises(iso.IsolatedEnvError) as ctx:
            iso.prepare_isolated_env(self.isolated, other_exe, scenario='level-2b', home=self.home, allow_reuse=True)
        self.assertEqual(ctx.exception.returncode, iso.EXIT_ISOLATED_VERSION_MISMATCH)

        # bundled_runtime_hash drift -> exit 33
        with self.assertRaises(iso.IsolatedEnvError) as ctx2:
            iso.prepare_isolated_env(
                self.isolated, self.exe, scenario='level-2b', home=self.home,
                bundled_runtime_hash='9999888877776666', allow_reuse=True,
            )
        self.assertEqual(ctx2.exception.returncode, iso.EXIT_ISOLATED_VERSION_MISMATCH)

        # component_versions drift -> exit 33
        with self.assertRaises(iso.IsolatedEnvError) as ctx3:
            iso.prepare_isolated_env(
                self.isolated, self.exe, scenario='level-2b', home=self.home,
                bundled_runtime_hash='1111222233334444', component_versions={'cognee': '1.6.8'}, allow_reuse=True,
            )
        self.assertEqual(ctx3.exception.returncode, iso.EXIT_ISOLATED_VERSION_MISMATCH)

    def test_mutual_exclusion_matrix_forbids_unsafe_hot_reuse(self):
        iso.prepare_isolated_env(self.isolated, self.exe, scenario='clean-install', home=self.home)
        iso.update_isolated_manifest(self.isolated, store_id='store-001', executed_scenarios=['clean-install'])
        for scenario in ('clean-install', 'half-installed', 'beta2-upgrade'):
            with self.subTest(scenario=scenario):
                with self.assertRaises(iso.IsolatedEnvError):
                    iso.prepare_isolated_env(self.isolated, self.exe, scenario=scenario, home=self.home, allow_reuse=False)
        # Same candidate with allow_reuse=True (e.g. Level 2b) succeeds.
        reused = iso.prepare_isolated_env(self.isolated, self.exe, scenario='level-2b', home=self.home, allow_reuse=True)
        self.assertEqual(reused['store_id'], 'store-001')

    def test_resolve_host_bridge_params_reads_store_and_instances(self):
        lib = self.isolated / 'library'
        lib.mkdir(parents=True)
        (lib / 'store-identity.json').write_text(json.dumps({'schema': 1, 'store_id': 'st-abc'}), encoding='utf-8')
        reg = self.isolated / 'projects.json'
        # Real Memlane projects.json stores agents as a dict keyed by instance_id.
        reg.write_text(json.dumps({
            'schema_version': 2,
            'agents': {
                'codex-111': {'kind': 'codex', 'name': 'Codex', 'principal_id': 'p-1'},
                'trae_ide-222': {'kind': 'trae_ide', 'name': 'TRAE CN', 'principal_id': 'p-2'},
            },
        }), encoding='utf-8')
        (self.isolated / 'runtime.json').write_text(json.dumps({
            'official_data': str(lib),
            'service_url': 'http://127.0.0.1:18000',
            'token_file': str(self.isolated / 'owner.token'),
            'project_control': {
                'url': 'http://127.0.0.1:18080',
                'management_token_file': str(self.isolated / 'management.token'),
                'registry_file': str(reg),
            },
        }), encoding='utf-8')
        params = iso.resolve_host_bridge_params(self.isolated)
        self.assertEqual(params['store_id'], 'st-abc')
        self.assertEqual(params['service_url'], 'http://127.0.0.1:18000')
        self.assertEqual(params['control_url'], 'http://127.0.0.1:18080')
        self.assertEqual(params['instances'], {'codex': 'codex-111', 'trae_ide': 'trae_ide-222'})

        # List fallback remains supported for legacy/synthetic fixtures.
        reg.write_text(json.dumps({
            'agents': [{'kind': 'codex', 'instance_id': 'codex-list'}],
        }), encoding='utf-8')
        self.assertEqual(iso.resolve_host_bridge_params(self.isolated)['instances'], {'codex': 'codex-list'})

    def test_teardown_stops_checks_lock_restores_hooks_and_is_idempotent(self):
        iso.prepare_isolated_env(self.isolated, self.exe, scenario='clean-install', home=self.home)
        lib = self.isolated / 'library'
        lib.mkdir()
        (self.isolated / 'runtime.json').write_text(json.dumps({'official_data': str(lib)}), encoding='utf-8')
        # Set up protected Trae CN login state + preexisting hooks.json + backup inside isolated_root.
        trae_dir = self.home / '.trae-cn'
        trae_dir.mkdir()
        jwt = trae_dir / 'trae-jwt-token'
        jwt.write_text('opaque-jwt-sentinel', encoding='utf-8')
        hooks = trae_dir / 'hooks.json'
        hooks.write_text('{"version":1,"hooks":{"Stop":[{"hooks":[{"command":"mutated"}]}]}}', encoding='utf-8')
        backup = self.isolated / 'backups' / 'hooks.json'
        backup.parent.mkdir()
        original_hooks = '{"version":1,"hooks":{"PreToolUse":[{"hooks":[{"command":"orig"}]}]}}'
        backup.write_text(original_hooks, encoding='utf-8')
        iso.update_isolated_manifest(self.isolated, host_config_backups={str(hooks): str(backup)})

        # Active flock must abort teardown before deleting anything.
        lock_file = lib / '.service-owner'
        with lock_file.open('w') as handle:
            fcntl.flock(handle, fcntl.LOCK_EX | fcntl.LOCK_NB)
            with self.assertRaisesRegex(iso.IsolatedEnvError, 'service owner lock is still held'):
                iso.teardown_isolated_env(self.isolated, home=self.home)
            self.assertTrue(self.isolated.exists())

        stopped = []
        report = iso.teardown_isolated_env(self.isolated, home=self.home, stop_fn=lambda st: stopped.append(st['run_id']))
        self.assertEqual(report['status'], 'torn_down')
        self.assertTrue(report['removed'])
        self.assertEqual(report['restored_configs'], ['hooks.json'])
        self.assertEqual(len(stopped), 1)
        self.assertFalse(self.isolated.exists())
        self.assertEqual(hooks.read_text(encoding='utf-8'), original_hooks)
        self.assertEqual(jwt.read_text(encoding='utf-8'), 'opaque-jwt-sentinel')

        # Second call is cleanly idempotent.
        again = iso.teardown_isolated_env(self.isolated, home=self.home)
        self.assertEqual(again['status'], 'already_absent')
        self.assertFalse(again['removed'])

    def test_protected_paths_and_unowned_dirs_rejected_with_exit_32(self):
        memlane_dir = self.home / 'Library' / 'Application Support' / 'Memlane'
        memlane_dir.mkdir(parents=True)
        with self.assertRaises(iso.IsolatedEnvError) as ctx:
            iso.prepare_isolated_env(memlane_dir, self.exe, scenario='clean-install', home=self.home)
        self.assertEqual(ctx.exception.returncode, utm_guard.EXIT_PROTECTED_PATH_VIOLATED)

        trae_dir = self.home / '.trae-cn'
        trae_dir.mkdir(parents=True)
        with self.assertRaises(iso.IsolatedEnvError) as ctx2:
            iso.teardown_isolated_env(trae_dir, home=self.home)
        self.assertEqual(ctx2.exception.returncode, utm_guard.EXIT_PROTECTED_PATH_VIOLATED)

        # Unowned directory lacking .memlane-utm-isolated.json cannot be torn down.
        unowned = self.root / 'random-user-dir'
        unowned.mkdir()
        (unowned / 'keep.txt').write_text('keep', encoding='utf-8')
        with self.assertRaises(iso.IsolatedEnvError) as ctx3:
            iso.teardown_isolated_env(unowned, home=self.home)
        self.assertEqual(ctx3.exception.returncode, utm_guard.EXIT_PROTECTED_PATH_VIOLATED)
        self.assertTrue((unowned / 'keep.txt').exists())


if __name__ == '__main__':
    unittest.main()
