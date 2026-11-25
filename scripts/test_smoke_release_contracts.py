from __future__ import annotations

import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).resolve().parent))
import smoke_release_contracts as smoke


class SmokeReleaseContractsTests(unittest.TestCase):
    def setUp(self):
        self._tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self._tmp.cleanup)
        self.root = Path(self._tmp.name).resolve()

    def test_missing_executable_returns_exit_2_env_unavailable(self):
        code, report = smoke.run_local_contract_smoke(self.root / 'does-not-exist')
        self.assertEqual(code, smoke.EXIT_ENV_UNAVAILABLE)
        self.assertEqual(report['status'], 'env_unavailable')

    def test_missing_python_dependencies_returns_exit_2(self):
        fake_bin = self.root / 'Memlane'
        fake_bin.write_text('#!/bin/sh\nexit 0\n', encoding='utf-8')
        fake_bin.chmod(0o700)
        with patch.object(smoke, 'resolve_smoke_python', side_effect=smoke.EnvironmentUnavailable('no aiohttp')):
            code, report = smoke.run_local_contract_smoke(fake_bin)
        self.assertEqual(code, smoke.EXIT_ENV_UNAVAILABLE)
        self.assertEqual(report['status'], 'env_unavailable')

    def test_python_probe_uses_E_s_B_never_I(self):
        fake_py = self.root / 'python3.12'
        fake_py.write_text('#!/bin/sh\nexit 0\n', encoding='utf-8')
        fake_py.chmod(0o700)
        with patch.object(smoke.subprocess, 'run', return_value=subprocess.CompletedProcess([], 0, '', '')) as run:
            self.assertTrue(smoke._python_supports_control_server(fake_py))
        cmd = run.call_args.args[0]
        self.assertEqual(cmd[:4], [str(fake_py), '-E', '-s', '-B'])
        self.assertNotIn('-I', cmd)
        self.assertIn('-E', smoke.OWNER_HOLDER_SCRIPT)
        self.assertIn('-s', smoke.OWNER_HOLDER_SCRIPT)
        self.assertIn('-B', smoke.OWNER_HOLDER_SCRIPT)
        self.assertNotIn("'-I'", smoke.OWNER_HOLDER_SCRIPT)

    def test_config_and_env_isolation_guards_reject_escapes(self):
        good_cfg = {
            'official_data': str(self.root / 'live' / 'library'),
            'token_file': str(self.root / 'live' / 'library' / 'owner.token'),
            'project_control': {
                'registry_file': str(self.root / 'live' / 'library' / 'projects.json'),
                'management_token_file': str(self.root / 'live' / 'library' / 'management.token'),
            },
        }
        smoke.assert_config_isolated(good_cfg, self.root, real_home=Path('/Users/tester'))

        for bad_cfg in (
            {'official_data': '/repo/.local/daily/official-store'},
            {'official_data': '/Users/tester/Library/Application Support/Memlane/library'},
            {'token_file': '/tmp/outside-smoke-root/owner.token'},
        ):
            with self.subTest(bad_cfg=bad_cfg):
                with self.assertRaises(smoke.IsolationViolation):
                    smoke.assert_config_isolated(bad_cfg, self.root, real_home=Path('/Users/tester'))

        clean = smoke._clean_smoke_env(self.root)
        smoke.assert_env_isolated(clean, self.root)
        with self.assertRaises(smoke.IsolationViolation):
            smoke.assert_env_isolated({**clean, 'MEMLANE_DEV_RUNTIME_CONFIG': '/private/daily/runtime.json'}, self.root)

    def test_lsof_isolation_guard_catches_daily_store_and_host_login_fds(self):
        fake_home = self.root / 'real-home'
        fake_repo = self.root / 'repo'
        (fake_repo / '.local' / 'daily').mkdir(parents=True)
        (fake_home / '.trae-cn').mkdir(parents=True)

        safe_lsof = f'p123\nn{self.root}/live/library/projects.json\nn/usr/lib/libSystem.B.dylib\n'
        res = smoke.assert_lsof_isolated(
            [123], repo_root=fake_repo, real_home=fake_home, lsof_runner=lambda _: safe_lsof,
        )
        self.assertTrue(res['checked'])
        self.assertEqual(res['violations'], 0)

        for bad_fd in (
            str(fake_repo / '.local' / 'daily' / 'official-store' / 'cognee.db'),
            str(fake_home / '.trae-cn' / 'trae-jwt-token'),
        ):
            with self.subTest(bad_fd=bad_fd):
                bad_lsof = f'p123\nn{bad_fd}\n'
                with self.assertRaises(smoke.IsolationViolation):
                    smoke.assert_lsof_isolated(
                        [123], repo_root=fake_repo, real_home=fake_home, lsof_runner=lambda _: bad_lsof,
                    )

    def test_no_pycache_guard_detects_bytecode(self):
        smoke.assert_no_pycache(self.root)
        pc = self.root / 'engine' / '__pycache__'
        pc.mkdir(parents=True)
        with self.assertRaises(smoke.IsolationViolation):
            smoke.assert_no_pycache(self.root)

    def test_contract_failure_and_privacy_violation_return_expected_exit_codes(self):
        fake_py = self.root / 'python3.12'
        fake_py.write_text('#!/bin/sh\nexit 0\n', encoding='utf-8')
        fake_py.chmod(0o700)

        # Contract failure (exit 1): version command returns unexpected exit code.
        bad_contract_bin = self.root / 'Memlane-bad'
        bad_contract_bin.write_text(
            '#!/bin/sh\n'
            'printf \'%s\\n\' \'{"schema_version":1,"command":"version","exit_code":50,"status":"fatal","ok":false}\'\n'
            'exit 50\n',
            encoding='utf-8',
        )
        bad_contract_bin.chmod(0o700)
        with patch.object(smoke, 'resolve_smoke_python', return_value=fake_py):
            code, report = smoke.run_local_contract_smoke(bad_contract_bin)
        self.assertEqual(code, smoke.EXIT_CONTRACT_FAILED)
        self.assertEqual(report['status'], 'failed')

        # Privacy leak (exit 3): CLI echoes a secret token marker.
        leak_bin = self.root / 'Memlane-leak'
        leak_bin.write_text(
            '#!/bin/sh\n'
            'printf \'%s\\n\' \'{"schema_version":1,"command":"version","exit_code":0,"leak":"Bearer secret-token","capabilities":{}}\'\n'
            'exit 0\n',
            encoding='utf-8',
        )
        leak_bin.chmod(0o700)
        with patch.object(smoke, 'resolve_smoke_python', return_value=fake_py):
            code, report = smoke.run_local_contract_smoke(leak_bin)
        self.assertEqual(code, smoke.EXIT_ISOLATION_VIOLATION)
        self.assertEqual(report['status'], 'isolation_violation')


if __name__ == '__main__':
    unittest.main()
