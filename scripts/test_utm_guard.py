from __future__ import annotations

import base64
import json
import os
from pathlib import Path
import socket
import sys
import tempfile
import time
from types import SimpleNamespace
import unittest

sys.path.insert(0, str(Path(__file__).resolve().parent))
import utm_guard


def _make_jwt(claims: dict) -> str:
    def enc(obj: dict) -> str:
        raw = json.dumps(obj, separators=(',', ':')).encode('utf-8')
        return base64.urlsafe_b64encode(raw).decode('ascii').rstrip('=')

    return f'{enc({"alg": "HS256", "typ": "JWT"})}.{enc(claims)}.signature'


class UtmGuardTests(unittest.TestCase):
    def setUp(self):
        self._tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self._tmp.cleanup)
        self.root = Path(self._tmp.name).resolve()
        self.home = self.root / 'home'
        self.home.mkdir()

    def test_protected_paths_block_descendants_exact_and_ancestors(self):
        trae_support = self.home / 'Library' / 'Application Support' / 'Trae CN'
        trae_support.mkdir(parents=True)
        jwt_file = self.home / '.trae-cn' / 'trae-jwt-token'
        jwt_file.parent.mkdir(parents=True)
        jwt_file.write_text('secret-value', encoding='utf-8')
        codex_state = self.home / '.codex' / 'state_1.sqlite'
        codex_state.parent.mkdir(parents=True)
        codex_state.write_text('db', encoding='utf-8')

        for blocked in (
            trae_support,
            trae_support / 'User' / 'globalStorage',
            self.home / 'Library' / 'Application Support',
            self.home,
            jwt_file,
            self.home / '.trae-cn',
            self.home / '.codex' / 'auth.json',
            codex_state,
            self.home / 'Library' / 'Application Support' / 'Memlane',
            Path('/Applications/Trae CN.app'),
        ):
            with self.subTest(blocked=str(blocked)):
                self.assertIsNotNone(utm_guard.is_protected_overlap(blocked, self.home))

        safe_target = self.root / 'isolated-run' / 'library'
        self.assertIsNone(utm_guard.is_protected_overlap(safe_target, self.home))

    def test_protected_paths_block_case_insensitive_variants_on_apfs(self):
        for variant in (
            self.home / 'library' / 'application support' / 'trae cn',
            self.home / 'Library' / 'Application Support' / 'memlane',
            self.home / 'LIBRARY' / 'APPLICATION SUPPORT' / 'TRAE CN' / 'User',
            self.home / '.TRAE-CN' / 'TRAE-JWT-TOKEN',
            self.home / '.CODEX' / 'AUTH.JSON',
            self.home / '.CODEX' / 'STATE_1.SQLITE',
            Path('/applications/trae cn.app'),
        ):
            with self.subTest(variant=str(variant)):
                self.assertIsNotNone(utm_guard.is_protected_overlap(variant, self.home, case_insensitive=True))

    def test_safe_remove_enforces_manifest_containment_symlinks_and_whitelist(self):
        isolated = self.root / 'isolated-run'
        isolated.mkdir()
        child = isolated / 'subdir'
        child.mkdir()
        (child / 'data.txt').write_text('ok', encoding='utf-8')

        # Missing manifest must refuse.
        with self.assertRaisesRegex(utm_guard.ProtectedPathError, 'manifest'):
            utm_guard.safe_remove(child, isolated, home=self.home)
        self.assertTrue(child.exists())

        utm_guard.write_isolated_manifest(isolated, run_id='run-1')

        # Deleting isolated root itself without allow_root_self must refuse.
        with self.assertRaisesRegex(utm_guard.ProtectedPathError, 'allow_root_self'):
            utm_guard.safe_remove(isolated, isolated, home=self.home)

        # Symlink escape into protected host directory must refuse.
        trae_dir = self.home / 'Library' / 'Application Support' / 'Trae CN'
        trae_dir.mkdir(parents=True)
        escape = isolated / 'escape-link'
        escape.symlink_to(trae_dir)
        with self.assertRaises(utm_guard.ProtectedPathError):
            utm_guard.safe_remove(escape, isolated, home=self.home)
        self.assertTrue(trae_dir.is_dir())

        # Outside allowed_root must refuse.
        outside = self.root / 'outside'
        outside.mkdir()
        with self.assertRaises(utm_guard.ProtectedPathError):
            utm_guard.safe_remove(outside, isolated, home=self.home)
        self.assertTrue(outside.is_dir())

        # Valid removal inside isolated root succeeds.
        self.assertTrue(utm_guard.safe_remove(child, isolated, home=self.home))
        self.assertFalse(child.exists())

        # Teardown of isolated root itself succeeds with allow_root_self=True.
        self.assertTrue(utm_guard.safe_remove(isolated, isolated, home=self.home, allow_root_self=True))
        self.assertFalse(isolated.exists())

    def test_sentinel_detects_deletion_truncation_and_emptying(self):
        trae_support = self.home / 'Library' / 'Application Support' / 'Trae CN'
        trae_support.mkdir(parents=True)
        (trae_support / 'Cookies').write_text('cookie', encoding='utf-8')
        jwt_file = self.home / '.trae-cn' / 'trae-jwt-token'
        jwt_file.parent.mkdir(parents=True)
        jwt_file.write_text('non-empty-jwt', encoding='utf-8')
        codex_auth = self.home / '.codex' / 'auth.json'
        codex_auth.parent.mkdir(parents=True)
        codex_auth.write_text('{"token":"x"}', encoding='utf-8')

        sentinel = utm_guard.HostStateSentinel(self.home)
        self.assertTrue(sentinel.verify()['ok'])

        # Truncate JWT, empty Trae CN app support, delete codex auth.
        jwt_file.write_text('', encoding='utf-8')
        (trae_support / 'Cookies').unlink()
        codex_auth.unlink()

        report = sentinel.verify()
        self.assertFalse(report['ok'])
        reasons = {(v['label'], v['reason']) for v in report['violations']}
        self.assertIn(('trae_cn_jwt', 'truncated'), reasons)
        self.assertIn(('trae_cn_app_support', 'emptied'), reasons)
        self.assertIn(('codex_auth', 'deleted'), reasons)

    def test_probe_dns_success_and_failure(self):
        ok = utm_guard.probe_dns('china', resolver=lambda host: [('1.2.3.4', 443)])
        self.assertTrue(ok['ok'])
        self.assertEqual(ok['failed_hosts'], [])

        def fail_one(host):
            if host == 'modelscope.cn':
                raise socket.gaierror('NXDOMAIN')
            return [('1.2.3.4', 443)]

        bad = utm_guard.probe_dns('china', resolver=fail_one)
        self.assertFalse(bad['ok'])
        self.assertEqual(bad['code'], 'PREFLIGHT_DNS_FAILED')
        self.assertEqual(bad['failed_hosts'], ['modelscope.cn'])

    def test_probe_proxy_detects_dead_scutil_proxy_and_http_502(self):
        scutil_out = (
            '<dictionary> {\n'
            '  HTTPSEnable : 1\n'
            '  HTTPSProxy : 127.0.0.1\n'
            '  HTTPSPort : 7890\n'
            '  ExceptionsList : <array> {\n'
            '    0 : *.local\n'
            '  }\n'
            '}\n'
        )
        dead = utm_guard.probe_proxy(
            'official',
            env={},
            scutil_fn=lambda: scutil_out,
            tcp_connect_fn=lambda addr, t: (_ for _ in ()).throw(ConnectionRefusedError()),
        )
        self.assertFalse(dead['ok'])
        self.assertEqual(dead['code'], 'PREFLIGHT_PROXY_BLOCKED')
        self.assertEqual(dead['proxy_source'], 'scutil:https')
        self.assertEqual(dead['proxy_endpoint'], '127.0.0.1:7890')
        self.assertEqual(dead['detail'], 'tcp_refused')

        bad_gateway = utm_guard.probe_proxy(
            'official',
            env={'HTTPS_PROXY': 'http://user:secretpass@127.0.0.1:8080'},
            scutil_fn=lambda: '',
            tcp_connect_fn=lambda addr, t: SimpleNamespace(close=lambda: None),
            head_fn=lambda url, proxy, timeout: 502,
        )
        self.assertFalse(bad_gateway['ok'])
        self.assertEqual(bad_gateway['detail'], 'http_502')
        self.assertEqual(bad_gateway['proxy_endpoint'], '127.0.0.1:8080')
        self.assertNotIn('secretpass', json.dumps(bad_gateway))

        healthy = utm_guard.probe_proxy(
            'china',
            env={},
            scutil_fn=lambda: '',
            head_fn=lambda url, proxy, timeout: 200,
        )
        self.assertTrue(healthy['ok'])
        self.assertEqual(healthy['http_status'], 200)

    def test_probe_disk_thresholds(self):
        low = utm_guard.probe_disk(
            (self.home,),
            min_bytes=2 * 1024**3,
            warn_bytes=5 * 1024**3,
            disk_usage_fn=lambda _: SimpleNamespace(free=1 * 1024**3),
        )
        self.assertFalse(low['ok'])
        self.assertEqual(low['code'], 'PREFLIGHT_DISK_LOW')

        warn = utm_guard.probe_disk(
            (self.home,),
            min_bytes=2 * 1024**3,
            warn_bytes=5 * 1024**3,
            disk_usage_fn=lambda _: SimpleNamespace(free=3 * 1024**3),
        )
        self.assertTrue(warn['ok'])
        self.assertTrue(warn['warning'])

    def test_probe_host_login_validates_jwt_exp_without_leaking_token(self):
        apps = self.root / 'Applications'
        (apps / 'Trae CN.app').mkdir(parents=True)
        support = self.home / 'Library' / 'Application Support' / 'Trae CN'
        support.mkdir(parents=True)
        (support / 'state.db').write_text('ok', encoding='utf-8')
        jwt_path = self.home / '.trae-cn' / 'trae-jwt-token'
        jwt_path.parent.mkdir(parents=True)

        now = 1_800_000_000.0
        expired_token = _make_jwt({'sub': 'secret-user', 'exp': int(now - 60)})
        jwt_path.write_text(expired_token, encoding='utf-8')
        expired = utm_guard.probe_host_login(
            self.home,
            require_login_agents=('trae_ide',),
            now=now,
            app_dirs=(apps,),
        )
        self.assertFalse(expired['ok'])
        self.assertEqual(expired['code'], 'PREFLIGHT_HOST_LOGIN_MISSING_OR_EXPIRED')
        self.assertEqual(expired['reason'], 'jwt_expired')
        self.assertLess(expired['expires_in_s'], 0)
        dumped = json.dumps(expired)
        self.assertNotIn(expired_token, dumped)
        self.assertNotIn('secret-user', dumped)

        fresh_token = _make_jwt({'sub': 'secret-user', 'exp': int(now + 3600)})
        jwt_path.write_text(fresh_token, encoding='utf-8')
        valid = utm_guard.probe_host_login(
            self.home,
            require_login_agents=('trae_ide',),
            now=now,
            app_dirs=(apps,),
        )
        self.assertTrue(valid['ok'])
        self.assertNotIn(fresh_token, json.dumps(valid))

    def test_probe_host_login_codex_auth_permissions(self):
        bin_dir = self.home / '.local' / 'bin'
        bin_dir.mkdir(parents=True)
        codex_bin = bin_dir / 'codex'
        codex_bin.write_text('#!/bin/sh\nexit 0\n', encoding='utf-8')
        codex_bin.chmod(0o700)
        auth = self.home / '.codex' / 'auth.json'
        auth.parent.mkdir(parents=True)
        auth.write_text('{"access_token": "secret-codex-token"}', encoding='utf-8')
        auth.chmod(0o644)

        bad_perm = utm_guard.probe_host_login(
            self.home,
            require_login_agents=('codex',),
            app_dirs=(),
            which_fn=lambda _: None,
        )
        self.assertFalse(bad_perm['ok'])
        self.assertEqual(bad_perm['reason'], 'insecure_permissions')

        auth.chmod(0o600)
        ok = utm_guard.probe_host_login(
            self.home,
            require_login_agents=('codex',),
            app_dirs=(),
            which_fn=lambda _: None,
        )
        self.assertTrue(ok['ok'])
        self.assertNotIn('secret-codex-token', json.dumps(ok))

    def test_privacy_check_catches_sk_prefix_without_false_positive_on_task_paths(self):
        benign = {'path': '/private/var/tmp/memlane-task-abc123/runtime.json', 'dir': 'disk-cleanup'}
        utm_guard.check_output_privacy(json.dumps(benign), benign)
        for bad in (
            {'leak': 'sk-secret'},
            {'leak': 'prefix sk-12345678'},
            {'leak': 'SK-UPPER'},
        ):
            with self.subTest(bad=bad):
                with self.assertRaises(utm_guard.GuardError):
                    utm_guard.check_output_privacy(json.dumps(bad), bad)


if __name__ == '__main__':
    unittest.main()
