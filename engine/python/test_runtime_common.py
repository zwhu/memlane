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
    decode_node_set,
    file_sha256,
    id_variants,
    is_derived_material,
    is_knowledge_doc,
    iso_text,
    match_managed_plugin_manifest,
    parse_json,
    private_write,
    read_secret,
    replace_exact,
    sql_in_clause,
    uuid_hex,
    uuid_text,
    verify_sqlite_schema,
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
        self.assertEqual(LEGACY_MANAGED_PLUGIN_MANIFESTS, ('managed-project-plugin-pre-cursor-policy.json',))
        for name in LEGACY_MANAGED_PLUGIN_MANIFESTS:
            path = engine_dir / name
            self.assertTrue(path.is_file(), f'Missing legacy manifest: {path}')
            legacy = json.loads(path.read_text())
            self.assertEqual(match_managed_plugin_manifest(engine_dir, legacy), (legacy, False))
        with self.assertRaises(PolicyError):
            match_managed_plugin_manifest(engine_dir, {'files': {'unexpected': 'deadbeef'}})

    def test_shared_uuid_sqlite_and_doc_classification_helpers(self):
        import sqlite3
        import uuid

        u = str(uuid.uuid4())
        h = uuid.UUID(u).hex
        self.assertEqual(uuid_text(h), u)
        self.assertEqual(uuid_hex(u), h)
        self.assertEqual(id_variants(u), (u, h))
        self.assertEqual(sql_in_clause((u,)), ('= ?', (u,)))
        self.assertEqual(sql_in_clause((u, h)), ('IN (?,?)', (u, h)))
        self.assertEqual(iso_text('2026-11-21 10:00:00'), '2026-11-21T10:00:00')
        self.assertEqual(decode_node_set(json.dumps(json.dumps(['session_learnings']))), ['session_learnings'])
        self.assertTrue(is_knowledge_doc({'node_set': ['session_learnings']}))
        self.assertTrue(is_knowledge_doc({'externalMetadata': {'common_category': 'prefs'}}))
        self.assertFalse(is_knowledge_doc({'externalMetadata': {'node_set': ['project_docs']}}))
        self.assertTrue(is_derived_material({'node_set': ['agent_trace_feedbacks']}))
        self.assertTrue(is_derived_material({'externalMetadata': {'node_set': ['user_sessions_from_cache']}}))
        self.assertFalse(is_derived_material({'externalMetadata': {}}))

        con = sqlite3.connect(':memory:')
        con.execute('CREATE TABLE sample (a TEXT, b INTEGER)')
        verify_sqlite_schema(con, {'sample': {'a', 'b'}})
        with self.assertRaises(sqlite3.OperationalError):
            verify_sqlite_schema(con, {'sample': {'a', 'missing_col'}})
        with self.assertRaises(sqlite3.OperationalError):
            verify_sqlite_schema(con, {'missing_table': {'a'}})
        con.close()


if __name__ == '__main__':
    unittest.main()
