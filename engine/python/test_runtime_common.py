"""Unit tests for shared runtime utilities in engine/python/_runtime_common.py."""
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

from _runtime_common import (
    CachedSecretReader,
    LEGACY_MANAGED_PLUGIN_MANIFESTS,
    bytes_sha256,
    file_sha256,
    match_managed_plugin_manifest,
    private_write,
    read_secret,
    replace_exact,
)
from project_policy import PolicyError


class RuntimeCommonTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.root = Path(self.temp.name).resolve()

    def tearDown(self):
        self.temp.cleanup()

    def test_private_write_and_read_secret_enforce_private_permissions(self):
        target = self.root / 'nested/secret.token'
        private_write(target, b'  my-secret-value \n')
        self.assertEqual(target.stat().st_mode & 0o777, 0o600)
        self.assertEqual(read_secret(target), 'my-secret-value')
        self.assertEqual(bytes_sha256(b'  my-secret-value \n'), file_sha256(target))

        target.chmod(0o644)
        with self.assertRaises(PolicyError):
            read_secret(target)
        target.chmod(0o600)

        link = self.root / 'link.token'
        link.symlink_to(target)
        with self.assertRaises(PolicyError):
            read_secret(link)

    def test_cached_secret_reader_caches_and_revalidates_on_change(self):
        target = self.root / 'agent.token'
        private_write(target, b'token-alpha-' + b'a' * 24)
        reader = CachedSecretReader()
        calls = {'count': 0}
        orig_read_text = Path.read_text

        def counted_read_text(path_self, *args, **kwargs):
            if path_self == target:
                calls['count'] += 1
            return orig_read_text(path_self, *args, **kwargs)

        with patch.object(Path, 'read_text', autospec=True, side_effect=counted_read_text):
            self.assertEqual(reader.read(target), 'token-alpha-' + 'a' * 24)
            self.assertEqual(reader.read(target), 'token-alpha-' + 'a' * 24)
            self.assertEqual(calls['count'], 1)

            private_write(target, b'token-beta-' + b'b' * 30)
            self.assertEqual(reader.read(target), 'token-beta-' + 'b' * 30)
            self.assertEqual(calls['count'], 2)

        target.chmod(0o644)
        with self.assertRaises(PolicyError):
            reader.read(target)
        target.chmod(0o600)

        link = self.root / 'link-agent.token'
        link.symlink_to(target)
        with self.assertRaises(PolicyError):
            reader.read(link)

    def test_replace_exact_and_managed_manifest_matching(self):
        self.assertEqual(replace_exact('a b c', 'b', 'X'), 'a X c')
        with self.assertRaises(ValueError):
            replace_exact('a b c', 'missing', 'X')
        with self.assertRaises(ValueError):
            replace_exact('a b b', 'b', 'X')

        engine_dir = Path(__file__).resolve().parents[1]
        current = json.loads((engine_dir / 'managed-project-plugin.json').read_text())
        self.assertEqual(match_managed_plugin_manifest(engine_dir, current), (current, True))
        legacy = json.loads((engine_dir / LEGACY_MANAGED_PLUGIN_MANIFESTS[0]).read_text())
        self.assertEqual(match_managed_plugin_manifest(engine_dir, legacy), (legacy, False))
        with self.assertRaises(PolicyError):
            match_managed_plugin_manifest(engine_dir, {'files': {'unexpected': 'deadbeef'}})


if __name__ == '__main__':
    unittest.main()
