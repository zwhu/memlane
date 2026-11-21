"""Unit tests for sqlite_ro read-only foundation and trusted metadata extraction."""
import json
from pathlib import Path
import sqlite3
import tempfile
import unittest
from unittest.mock import patch

from project_policy import PolicyError
from sqlite_ro import (
    common_category_from_meta,
    decode_node_set,
    extract_trusted_metadata,
    knowledge_category_label,
    official_sqlite_ro,
    open_official_sqlite_ro,
    unique_learning_session,
)


class SqliteRoTests(unittest.TestCase):
    def _make_wal_db(self, root: Path) -> Path:
        db_path = root / 'cognee_db'
        con = sqlite3.connect(db_path)
        con.execute('PRAGMA journal_mode=WAL')
        con.execute('CREATE TABLE sample (id TEXT PRIMARY KEY, val TEXT)')
        con.execute("INSERT INTO sample VALUES ('1', 'hello')")
        con.commit()
        con.close()
        return db_path

    def test_authorizer_blocks_all_writes_ddl_attach_and_unsafe_pragmas(self):
        with tempfile.TemporaryDirectory() as tmp:
            db_path = self._make_wal_db(Path(tmp))
            with official_sqlite_ro(db_path) as con:
                row = con.execute("SELECT val FROM sample WHERE id = '1'").fetchone()
                self.assertEqual(row, ('hello',))
                # Read-only metadata pragma works
                cols = [r[1] for r in con.execute('PRAGMA table_info(sample)').fetchall()]
                self.assertEqual(cols, ['id', 'val'])

                forbidden_statements = [
                    "INSERT INTO sample VALUES ('2', 'bad')",
                    "UPDATE sample SET val = 'bad' WHERE id = '1'",
                    "DELETE FROM sample WHERE id = '1'",
                    'CREATE TABLE hacked (x INT)',
                    'CREATE INDEX idx_bad ON sample(val)',
                    'DROP TABLE sample',
                    'ALTER TABLE sample ADD COLUMN extra TEXT',
                    "ATTACH DATABASE ':memory:' AS extra_db",
                    'PRAGMA query_only = OFF',
                    'PRAGMA query_only = 0',
                    'PRAGMA journal_mode = DELETE',
                    'PRAGMA wal_checkpoint(TRUNCATE)',
                    'PRAGMA writable_schema = ON',
                ]
                for stmt in forbidden_statements:
                    with self.subTest(stmt=stmt):
                        with self.assertRaises(sqlite3.DatabaseError):
                            con.execute(stmt)
                # Read-only deferred WAL snapshot succeeds, while write transaction & DML inside snapshot remain denied
                con.execute('BEGIN DEFERRED')
                try:
                    self.assertEqual(con.execute("SELECT val FROM sample WHERE id = '1'").fetchone(), ('hello',))
                    with self.assertRaises(sqlite3.DatabaseError):
                        con.execute("INSERT INTO sample VALUES ('2', 'bad')")
                finally:
                    con.execute('COMMIT')
                with self.assertRaises(sqlite3.DatabaseError):
                    con.execute('BEGIN IMMEDIATE')

    def test_rejects_missing_and_symlink_db(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            with self.assertRaises(PolicyError) as missing_err:
                open_official_sqlite_ro(root / 'missing.db')
            self.assertEqual((missing_err.exception.code, missing_err.exception.status), ('official_store_unavailable', 503))

            real_db = self._make_wal_db(root)
            link_db = root / 'link.db'
            link_db.symlink_to(real_db)
            with self.assertRaises(PolicyError) as sym_err:
                open_official_sqlite_ro(link_db)
            self.assertEqual((sym_err.exception.code, sym_err.exception.status), ('official_store_unavailable', 503))

    def test_transient_wal_operational_error_retries_three_times_without_immutable(self):
        with tempfile.TemporaryDirectory() as tmp:
            db_path = self._make_wal_db(Path(tmp))
            seen_uris = []
            sleeps = []
            real_connect = sqlite3.connect

            attempts = {'count': 0}

            def flaky_connect(database, *args, **kwargs):
                seen_uris.append(database)
                attempts['count'] += 1
                if attempts['count'] <= 3:
                    raise sqlite3.OperationalError('unable to open database file (WAL shm missing)')
                return real_connect(database, *args, **kwargs)

            with patch('sqlite_ro.sqlite3.connect', side_effect=flaky_connect):
                con = open_official_sqlite_ro(db_path, sleep_fn=sleeps.append)
                try:
                    self.assertEqual(con.execute('SELECT count(*) FROM sample').fetchone()[0], 1)
                finally:
                    con.close()

            self.assertEqual(attempts['count'], 4)
            self.assertEqual(sleeps, [0.05, 0.15, 0.30])
            self.assertTrue(all('mode=ro' in u and 'immutable' not in u for u in seen_uris))

            # All 4 attempts failing -> Fail-Closed 503 PolicyError
            sleeps_fail = []
            with patch('sqlite_ro.sqlite3.connect', side_effect=sqlite3.OperationalError('database is locked')):
                with self.assertRaises(PolicyError) as err:
                    open_official_sqlite_ro(db_path, sleep_fn=sleeps_fail.append)
            self.assertEqual((err.exception.code, err.exception.status), ('official_store_unavailable', 503))
            self.assertEqual(sleeps_fail, [0.05, 0.15, 0.30])

    def test_trusted_metadata_precedence_and_double_encoded_node_set(self):
        # Double-encoded JSON node_set (matches Cognee SQLite column storage)
        double_ns = json.dumps(json.dumps(['session_learnings', 'session_learnings:sess-42']))
        self.assertEqual(decode_node_set(double_ns), ['session_learnings', 'session_learnings:sess-42'])

        # system_metadata unconditionally overrides external_metadata and blocks planted keys
        trusted = extract_trusted_metadata(
            json.dumps({
                'lesson_type': 'state',
                'state_key': 'footprint:auth',
                'session_date': '2026-11-14',
                'source_qa_ids': ['qa-1'],
            }),
            json.dumps({
                'lesson_type': 'pitfall',
                'first_source_date': '1999-01-01',
                'session_date': '1999-01-01',
                'memlane_kind': 'common_knowledge',
                'category': 'preference',
                'why_learned': 'observed in CI',
            }),
            double_ns,
        )
        self.assertEqual(trusted['lesson_type'], 'state')
        self.assertEqual(trusted['state_key'], 'footprint:auth')
        self.assertEqual(trusted['session_date'], '2026-11-14')
        self.assertNotIn('first_source_date', trusted)
        self.assertNotIn('memlane_kind', trusted)
        self.assertEqual(trusted['why_learned'], 'observed in CI')
        self.assertEqual(trusted['node_set'], ['session_learnings', 'session_learnings:sess-42'])
        self.assertEqual(unique_learning_session(trusted), 'sess-42')

        # Common knowledge (no system_metadata) keeps memlane_kind + category
        common_meta = extract_trusted_metadata(
            None,
            json.dumps({'memlane_kind': 'common_knowledge', 'category': 'preference'}),
            None,
        )
        self.assertEqual(common_category_from_meta(common_meta), 'preference')
        self.assertEqual(knowledge_category_label(common_meta), '')

    def test_category_labels_match_knowledge_basis_contract(self):
        self.assertEqual(
            knowledge_category_label({'node_set': ['user_context']}),
            '直接保存',
        )
        self.assertEqual(
            knowledge_category_label({'node_set': ['memory']}),
            '直接保存',
        )
        self.assertEqual(
            knowledge_category_label({'node_set': ['session_learnings', 'user_context'], 'lesson_type': 'pitfall'}),
            '踩坑经验',
        )
        for lesson_type, expected in (
            ('state', '项目状态'),
            ('pitfall', '踩坑经验'),
            ('convention', '项目约定'),
            ('location', '代码位置与关联'),
            ('change', '代码变更'),
            ('constraint', '约束条件'),
            ('other', '其他'),
            ('', '未记录'),
            ('custom_kind', '未识别分类（custom_kind）'),
        ):
            self.assertEqual(
                knowledge_category_label({'node_set': ['session_learnings'], 'lesson_type': lesson_type}),
                expected,
            )
        # Non-knowledge raw document -> empty category
        self.assertEqual(knowledge_category_label({'node_set': ['project_docs']}), '')
        # Embedded common knowledge -> empty category
        self.assertEqual(
            knowledge_category_label({
                'memlane_kind': 'common_knowledge',
                'category': 'experience',
                'node_set': ['session_learnings'],
                'lesson_type': 'pitfall',
            }),
            '',
        )


if __name__ == '__main__':
    unittest.main()
