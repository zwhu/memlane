import json
from pathlib import Path
import sqlite3
import tempfile
import unittest
import uuid

from aiohttp.test_utils import AioHTTPTestCase

from project_control import Registry
from project_control_server import create_app


class _DummyOfficial:
    pass


def _create_mock_dbs(official_data: Path):
    db_dir = official_data / 'system' / 'databases'
    db_dir.mkdir(parents=True, exist_ok=True)
    cognee_db = db_dir / 'cognee_db'
    cache_db = db_dir / 'cache.db'

    c1 = sqlite3.connect(cognee_db)
    c1.executescript('''
        CREATE TABLE data (
            id CHAR(32) PRIMARY KEY, dataset_id CHAR(32), name VARCHAR, label VARCHAR,
            mime_type VARCHAR, raw_data_location VARCHAR, external_metadata JSON,
            node_set JSON, updated_at DATETIME, created_at DATETIME, content_hash VARCHAR
        );
        CREATE TABLE session_records (
            session_id VARCHAR, user_id CHAR(32), dataset_id CHAR(32),
            agent_id VARCHAR, last_activity_at DATETIME, started_at DATETIME
        );
    ''')
    c1.commit()
    c1.close()

    c2 = sqlite3.connect(cache_db)
    c2.executescript('''
        CREATE TABLE cache_qa_entries (
            seq INTEGER PRIMARY KEY, user_id TEXT NOT NULL, session_id TEXT NOT NULL,
            qa_id TEXT NOT NULL, payload JSON NOT NULL,
            created_at DATETIME DEFAULT CURRENT_TIMESTAMP NOT NULL, expires_at DATETIME
        );
        CREATE INDEX idx_cache_qa_expires ON cache_qa_entries (expires_at) WHERE expires_at IS NOT NULL;
        CREATE INDEX idx_cache_qa_session ON cache_qa_entries (user_id, session_id, seq);
        CREATE TABLE cache_trace_entries (
            seq INTEGER PRIMARY KEY, user_id TEXT NOT NULL, session_id TEXT NOT NULL,
            payload JSON NOT NULL,
            created_at DATETIME DEFAULT CURRENT_TIMESTAMP NOT NULL, expires_at DATETIME
        );
        CREATE INDEX idx_cache_trace_expires ON cache_trace_entries (expires_at) WHERE expires_at IS NOT NULL;
        CREATE INDEX idx_cache_trace_session ON cache_trace_entries (user_id, session_id, seq);
    ''')
    c2.commit()
    c2.close()
    return cognee_db, cache_db


class RawRecordsReadTest(AioHTTPTestCase):
    async def get_application(self):
        self.tmp = tempfile.TemporaryDirectory()
        root = Path(self.tmp.name)
        self.official_data = root / 'official-store'
        self.cognee_db, self.cache_db = _create_mock_dbs(self.official_data)

        self.ds_managed = str(uuid.uuid4())
        self.ds_other = str(uuid.uuid4())
        self.user_uuid = str(uuid.uuid4())
        self.user_hex = uuid.UUID(self.user_uuid).hex
        self.raw_doc_id = str(uuid.uuid4())
        self.knowledge_doc_id = str(uuid.uuid4())
        self.derived_doc_id = str(uuid.uuid4())
        self.other_doc_id = str(uuid.uuid4())

        con = sqlite3.connect(self.cognee_db)
        # 1. Valid raw document
        con.execute(
            'INSERT INTO data VALUES(?,?,?,?,?,?,?,?,?,?,?)',
            (
                uuid.UUID(self.raw_doc_id).hex,
                uuid.UUID(self.ds_managed).hex,
                'architecture.md',
                None,
                'text/markdown',
                'file:///tmp/arch.md',
                json.dumps({'node_set': ['project_docs']}),
                json.dumps(['project_docs']),
                '2026-11-20 12:00:00',
                '2026-11-20 11:00:00',
                'raw-hash',
            ),
        )
        # 2. Knowledge document (must be excluded)
        con.execute(
            'INSERT INTO data VALUES(?,?,?,?,?,?,?,?,?,?,?)',
            (
                uuid.UUID(self.knowledge_doc_id).hex,
                uuid.UUID(self.ds_managed).hex,
                'lesson.txt',
                None,
                'text/plain',
                'file:///tmp/lesson.txt',
                json.dumps({'node_set': ['session_learnings']}),
                json.dumps(['session_learnings']),
                '2026-11-20 12:00:00',
                '2026-11-20 11:00:00',
                'k-hash',
            ),
        )
        # 3. Derived material document (must be excluded)
        con.execute(
            'INSERT INTO data VALUES(?,?,?,?,?,?,?,?,?,?,?)',
            (
                uuid.UUID(self.derived_doc_id).hex,
                uuid.UUID(self.ds_managed).hex,
                'cache-export.txt',
                None,
                'text/plain',
                'file:///tmp/derived.txt',
                json.dumps({'node_set': ['agent_trace_feedbacks']}),
                json.dumps(['agent_trace_feedbacks']),
                '2026-11-20 12:00:00',
                '2026-11-20 11:00:00',
                'd-hash',
            ),
        )
        # 4. Unmanaged dataset doc (must be isolated)
        con.execute(
            'INSERT INTO data VALUES(?,?,?,?,?,?,?,?,?,?,?)',
            (
                uuid.UUID(self.other_doc_id).hex,
                uuid.UUID(self.ds_other).hex,
                'secret.md',
                None,
                'text/markdown',
                'file:///tmp/secret.md',
                json.dumps({}),
                json.dumps([]),
                '2026-11-20 12:00:00',
                '2026-11-20 11:00:00',
                'o-hash',
            ),
        )
        # Managed sessions + foreign dataset session
        con.execute(
            'INSERT INTO session_records VALUES(?,?,?,?,?,?)',
            ('codex_s1', self.user_hex, uuid.UUID(self.ds_managed).hex, 'codex', '2026-11-21 10:00:00', '2026-11-21 09:00:00'),
        )
        con.execute(
            'INSERT INTO session_records VALUES(?,?,?,?,?,?)',
            ('codex_s2', self.user_hex, uuid.UUID(self.ds_managed).hex, 'codex', '2026-11-20 10:00:00', '2026-11-20 09:00:00'),
        )
        con.execute(
            'INSERT INTO session_records VALUES(?,?,?,?,?,?)',
            ('foreign_s', self.user_hex, uuid.UUID(self.ds_other).hex, 'codex', '2026-11-21 10:00:00', '2026-11-21 09:00:00'),
        )
        con.commit()
        con.close()

        c2 = sqlite3.connect(self.cache_db)
        # Expired QA entry on codex_s1 (space format & T format must both be excluded)
        c2.execute(
            'INSERT INTO cache_qa_entries (seq, user_id, session_id, qa_id, payload, expires_at) VALUES (?,?,?,?,?,?)',
            (1, self.user_uuid, 'codex_s1', 'expired-qa', json.dumps({'qa_id': 'expired-qa', 'question': 'stale expired', 'answer': 'old'}), '2020-01-01 00:00:00'),
        )
        # First active QA on codex_s1 has empty question -> label must fall back to seq=3!
        c2.execute(
            'INSERT INTO cache_qa_entries (seq, user_id, session_id, qa_id, payload, expires_at) VALUES (?,?,?,?,?,?)',
            (2, self.user_uuid, 'codex_s1', 'qa-empty', json.dumps({'qa_id': 'qa-empty', 'question': '', 'answer': 'ok'}), '2099-01-01 00:00:00'),
        )
        c2.execute(
            'INSERT INTO cache_qa_entries (seq, user_id, session_id, qa_id, payload, expires_at) VALUES (?,?,?,?,?,?)',
            (3, self.user_uuid, 'codex_s1', 'qa-real', json.dumps({'qa_id': 'qa-real', 'question': '如何优化原始记录加载性能？', 'answer': 'SQLite直读'}), None),
        )
        # 25 active traces on codex_s1 + 1 expired trace
        c2.execute(
            'INSERT INTO cache_trace_entries (seq, user_id, session_id, payload, expires_at) VALUES (?,?,?,?,?)',
            (1, self.user_uuid, 'codex_s1', json.dumps({'trace_id': 'expired-tr', 'origin_function': 'Bash'}), '2020-01-01T00:00:00'),
        )
        for idx in range(25):
            c2.execute(
                'INSERT INTO cache_trace_entries (seq, user_id, session_id, payload, expires_at) VALUES (?,?,?,?,?)',
                (idx + 2, self.user_uuid, 'codex_s1', json.dumps({'trace_id': f'tr-{idx}', 'origin_function': 'Read'}), None),
            )
        c2.commit()
        c2.close()

        reg_path = root / 'registry.json'
        self.registry = Registry(reg_path, _DummyOfficial())
        self.registry.state['projects'] = {
            'p1': {'id': 'p1', 'name': 'memlane', 'dataset_id': self.ds_managed, 'status': 'verified'},
        }
        self.manager_token = 'm' * 40
        self.agent_token = 'a' * 40
        return create_app(
            self.registry,
            self.manager_token,
            {'agent-1': self.agent_token},
            codex_config={'official_data': str(self.official_data)},
        )

    async def tearDownAsync(self):
        await super().tearDownAsync()
        self.registry.close()
        self.tmp.cleanup()

    async def test_auth_validation_and_dataset_isolation(self):
        mgr = {'Authorization': 'Bearer ' + self.manager_token}
        agt = {'Authorization': 'Bearer ' + self.agent_token}

        self.assertEqual((await self.client.get(f'/v1/raw-records?dataset_id={self.ds_managed}', headers=agt)).status, 403)
        self.assertEqual((await self.client.get(f'/v1/raw-records?dataset_id={self.ds_other}', headers=mgr)).status, 403)
        self.assertEqual((await self.client.get(f'/v1/raw-records?dataset_id={self.ds_managed}&kind=invalid', headers=mgr)).status, 400)
        self.assertEqual((await self.client.get(f'/v1/raw-records?dataset_id={self.ds_managed}&extra=1', headers=mgr)).status, 400)
        self.assertEqual((await self.client.get(f'/v1/raw-records/session-history?dataset_id={self.ds_managed}&session_id=../bad', headers=mgr)).status, 400)
        self.assertEqual((await self.client.get(f'/v1/raw-records/session-history?dataset_id={self.ds_managed}&session_id=codex_s1&limit=99', headers=mgr)).status, 400)

    async def test_raw_records_catalog_hydrates_titles_and_counts_and_respects_kinds(self):
        mgr = {'Authorization': 'Bearer ' + self.manager_token}
        resp = await self.client.get(f'/v1/raw-records?dataset_id={self.ds_managed}&kind=all', headers=mgr)
        self.assertEqual(resp.status, 200)
        data = await resp.json()
        self.assertEqual(data['dataset_id'], self.ds_managed)
        self.assertTrue(data['cache_ready'])
        self.assertEqual([d['id'] for d in data['documents']], [self.raw_doc_id])
        self.assertEqual([s['session_id'] for s in data['sessions']], ['codex_s1', 'codex_s2'])
        s1 = data['sessions'][0]
        self.assertEqual(s1['label'], '如何优化原始记录加载性能？')
        self.assertEqual(s1['msg_count'], 2)
        self.assertEqual(s1['tool_calls'], 25)
        self.assertEqual(s1['user_id'], self.user_uuid)
        s2 = data['sessions'][1]
        self.assertEqual((s2['label'], s2['msg_count'], s2['tool_calls']), ('', 0, 0))

        sess_only = await (await self.client.get(f'/v1/raw-records?dataset_id={self.ds_managed}&kind=session', headers=mgr)).json()
        self.assertEqual(sess_only['documents'], [])
        self.assertEqual(len(sess_only['sessions']), 2)

        doc_only = await (await self.client.get(f'/v1/raw-records?dataset_id={self.ds_managed}&kind=document', headers=mgr)).json()
        self.assertEqual(len(doc_only['documents']), 1)
        self.assertEqual(doc_only['sessions'], [])

    async def test_session_history_pagination_and_edge_errors(self):
        mgr = {'Authorization': 'Bearer ' + self.manager_token}
        page0 = await self.client.get(
            f'/v1/raw-records/session-history?dataset_id={self.ds_managed}&session_id=codex_s1&qa_offset=0&trace_offset=0&limit=20',
            headers=mgr,
        )
        self.assertEqual(page0.status, 200)
        p0 = await page0.json()
        self.assertTrue(p0['history_paging'])
        self.assertEqual((p0['msg_count'], p0['tool_calls']), (2, 25))
        self.assertEqual(p0['label'], '如何优化原始记录加载性能？')
        self.assertEqual([q['qa_id'] for q in p0['qas']], ['qa-empty', 'qa-real'])
        self.assertEqual(len(p0['traces']), 20)
        self.assertFalse(p0['qa_more'])
        self.assertTrue(p0['trace_more'])

        # Page 1 trace slice still resolves session label cleanly
        page1 = await self.client.get(
            f'/v1/raw-records/session-history?dataset_id={self.ds_managed}&session_id=codex_s1&qa_offset=2&trace_offset=20&limit=20',
            headers=mgr,
        )
        self.assertEqual(page1.status, 200)
        p1 = await page1.json()
        self.assertEqual(p1['qas'], [])
        self.assertEqual([t['trace_id'] for t in p1['traces']], [f'tr-{i}' for i in range(20, 25)])
        self.assertFalse(p1['trace_more'])
        self.assertEqual(p1['label'], '如何优化原始记录加载性能？')

        # 404 for foreign or missing session
        self.assertEqual((await self.client.get(
            f'/v1/raw-records/session-history?dataset_id={self.ds_managed}&session_id=foreign_s',
            headers=mgr,
        )).status, 404)
        # 409 for offset past end
        self.assertEqual((await self.client.get(
            f'/v1/raw-records/session-history?dataset_id={self.ds_managed}&session_id=codex_s1&qa_offset=5',
            headers=mgr,
        )).status, 409)

        # Missing cache.db degrades raw-records to cache_ready=False and returns 503 for history
        self.cache_db.unlink()
        degraded = await (await self.client.get(f'/v1/raw-records?dataset_id={self.ds_managed}', headers=mgr)).json()
        self.assertFalse(degraded['cache_ready'])
        self.assertEqual(len(degraded['sessions']), 2)
        self.assertNotIn('label', degraded['sessions'][0])
        self.assertEqual((await self.client.get(
            f'/v1/raw-records/session-history?dataset_id={self.ds_managed}&session_id=codex_s1',
            headers=mgr,
        )).status, 503)

    async def test_raw_records_and_history_include_subagent_relations(self):
        mgr = {'Authorization': 'Bearer ' + self.manager_token}
        root = Path(self.tmp.name)
        codex_dir = root / '.codex'
        codex_dir.mkdir(parents=True)
        cdb = codex_dir / 'state_5.sqlite'
        cc = sqlite3.connect(cdb)
        cc.executescript('''
            CREATE TABLE thread_spawn_edges (parent_thread_id TEXT, child_thread_id TEXT, status TEXT);
            CREATE TABLE threads (id TEXT PRIMARY KEY, source TEXT, agent_nickname TEXT, agent_role TEXT, attached_thread_parent_id TEXT);
        ''')
        cc.execute('INSERT INTO thread_spawn_edges VALUES (?,?,?)', ('s1', 's2', 'closed'))
        cc.execute(
            'INSERT INTO threads VALUES (?,?,?,?,?)',
            ('s2', json.dumps({'subagent': {'thread_spawn': {'parent_thread_id': 's1', 'depth': 1, 'agent_role': 'research'}}}), 'Researcher', 'research', None),
        )
        # Child in foreign dataset must remain invisible/isolated from parent
        cc.execute('INSERT INTO thread_spawn_edges VALUES (?,?,?)', ('s_foreign', 'orphan_sub', 'closed'))
        cc.execute(
            'INSERT INTO threads VALUES (?,?,?,?,?)',
            ('orphan_sub', json.dumps({'subagent': {'other': 'guardian'}}), None, None, None),
        )
        cc.commit()
        cc.close()

        c1 = sqlite3.connect(self.cognee_db)
        c1.execute(
            'INSERT INTO session_records VALUES(?,?,?,?,?,?)',
            ('codex_orphan_sub', self.user_hex, uuid.UUID(self.ds_managed).hex, 'codex', '2026-11-20 09:00:00', '2026-11-20 08:30:00'),
        )
        c1.commit()
        c1.close()

        c2 = sqlite3.connect(self.cache_db)
        # s2 is a subagent WITH both agent-to-agent QA and a tool trace!
        c2.execute(
            'INSERT INTO cache_qa_entries (seq, user_id, session_id, qa_id, payload, expires_at) VALUES (?,?,?,?,?,?)',
            (10, self.user_uuid, 'codex_s2', 'sub-qa-1', json.dumps({'qa_id': 'sub-qa-1', 'question': 'Subagent research prompt', 'answer': 'Research findings'}), None),
        )
        c2.execute(
            'INSERT INTO cache_trace_entries (seq, user_id, session_id, payload, expires_at) VALUES (?,?,?,?,?)',
            (30, self.user_uuid, 'codex_s2', json.dumps({'trace_id': 'sub-tr-1', 'origin_function': 'Grep'}), None),
        )
        c2.commit()
        c2.close()

        self.registry.state['agents'] = {
            'codex-1': {'kind': 'codex', 'host_profile': str(codex_dir)},
        }

        catalog = await (await self.client.get(f'/v1/raw-records?dataset_id={self.ds_managed}&kind=session', headers=mgr)).json()
        by_id = {s['session_id']: s for s in catalog['sessions']}
        self.assertNotIn('is_subagent', by_id['codex_s1'])
        self.assertTrue(by_id['codex_s2']['is_subagent'])
        self.assertEqual(by_id['codex_s2']['parent_session_id'], 'codex_s1')
        self.assertEqual(by_id['codex_s2']['subagent_role'], 'research')
        self.assertTrue(by_id['codex_orphan_sub']['is_subagent'])
        self.assertNotIn('parent_session_id', by_id['codex_orphan_sub'])

        parent_hist = await (await self.client.get(
            f'/v1/raw-records/session-history?dataset_id={self.ds_managed}&session_id=codex_s1',
            headers=mgr,
        )).json()
        self.assertEqual(len(parent_hist['subagents']), 1)
        self.assertEqual(parent_hist['subagents'][0]['session_id'], 'codex_s2')
        self.assertEqual(parent_hist['subagents'][0]['label'], 'Subagent research prompt')
        self.assertEqual((parent_hist['subagents'][0]['msg_count'], parent_hist['subagents'][0]['tool_calls']), (1, 1))

        child_hist = await (await self.client.get(
            f'/v1/raw-records/session-history?dataset_id={self.ds_managed}&session_id=codex_s2',
            headers=mgr,
        )).json()
        self.assertTrue(child_hist['is_subagent'])
        self.assertEqual(child_hist['parent_session_id'], 'codex_s1')
        self.assertEqual(child_hist['parent_label'], '如何优化原始记录加载性能？')
        self.assertEqual(child_hist['subagent_role'], 'research')

    async def test_schema_guards_fail_closed_with_503_and_never_return_silent_empty_page(self):
        mgr = {'Authorization': 'Bearer ' + self.manager_token}

        # Existing session with 0 rows in cache.db must return 503 (never silent 200 empty page).
        empty_hist = await self.client.get(
            f'/v1/raw-records/session-history?dataset_id={self.ds_managed}&session_id=codex_s2',
            headers=mgr,
        )
        self.assertEqual(empty_hist.status, 503)

        # Dropping a required cache table degrades /v1/raw-records to cache_ready=False and fails history with 503.
        c2 = sqlite3.connect(self.cache_db)
        c2.execute('DROP TABLE cache_trace_entries')
        c2.commit()
        c2.close()
        cat = await (await self.client.get(f'/v1/raw-records?dataset_id={self.ds_managed}', headers=mgr)).json()
        self.assertFalse(cat['cache_ready'])
        self.assertNotIn('label', cat['sessions'][0])
        hist_broken_cache = await self.client.get(
            f'/v1/raw-records/session-history?dataset_id={self.ds_managed}&session_id=codex_s1',
            headers=mgr,
        )
        self.assertEqual(hist_broken_cache.status, 503)

        # Dropping a required cognee_db table fails both endpoints closed with 503.
        c1 = sqlite3.connect(self.cognee_db)
        c1.execute('DROP TABLE session_records')
        c1.commit()
        c1.close()
        self.assertEqual(
            (await self.client.get(f'/v1/raw-records?dataset_id={self.ds_managed}&kind=session', headers=mgr)).status,
            503,
        )
        self.assertEqual(
            (await self.client.get(
                f'/v1/raw-records/session-history?dataset_id={self.ds_managed}&session_id=codex_s1',
                headers=mgr,
            )).status,
            503,
        )


if __name__ == '__main__':
    unittest.main()
