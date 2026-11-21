"""End-to-end HTTP integration tests for all Phase 1 /v2/* endpoints."""
import hashlib
import json
from pathlib import Path
import sqlite3
import tempfile
import unittest
import uuid

from aiohttp.test_utils import AioHTTPTestCase

from knowledge_recall import RecallStore
from project_control import Registry
from project_control_server import create_app
from reading_state_store import knowledge_read_scope_hash


class _DummyOfficial:
    pass


class V2APITests(AioHTTPTestCase):
    async def get_application(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.root = Path(self.tmp.name)
        self.official_data = self.root / 'official-store'
        self.db_dir = self.official_data / 'system' / 'databases'
        self.data_dir = self.official_data / 'data'
        self.db_dir.mkdir(parents=True)
        self.data_dir.mkdir(parents=True)
        self.cognee_db = self.db_dir / 'cognee_db'
        self.cache_db = self.db_dir / 'cache.db'

        self.ds_proj = str(uuid.uuid4())
        self.ds_common = str(uuid.uuid4())
        self.ds_unconf = str(uuid.uuid4())
        self.user_a = str(uuid.uuid4())
        self.user_b = str(uuid.uuid4())

        self.identity = f"http://127.0.0.1:61255/{hashlib.sha256(b'tok').hexdigest()}/"
        self.manager_headers = {
            'Authorization': 'Bearer ' + 'm' * 40,
            'X-Memlane-Reading-Identity': self.identity,
        }
        self.agent_headers = {'Authorization': 'Bearer ' + 'a' * 40}

        # Populate cognee_db
        c1 = sqlite3.connect(self.cognee_db)
        c1.executescript('''
            CREATE TABLE data (
                id CHAR(32) PRIMARY KEY, dataset_id CHAR(32), legacy_id CHAR(32),
                name VARCHAR, label VARCHAR, mime_type VARCHAR, raw_data_location VARCHAR,
                external_metadata JSON, system_metadata JSON, node_set JSON,
                pipeline_status JSON, updated_at DATETIME, created_at DATETIME, content_hash VARCHAR
            );
            CREATE TABLE session_records (
                session_id VARCHAR NOT NULL, user_id CHAR(32) NOT NULL, dataset_id CHAR(32),
                status VARCHAR, started_at DATETIME, last_activity_at DATETIME, agent_id VARCHAR,
                PRIMARY KEY (session_id, user_id)
            );
            CREATE TABLE pipeline_runs (
                id CHAR(32) PRIMARY KEY, pipeline_run_id CHAR(32), parent_operation_id CHAR(32),
                dataset_id CHAR(32), pipeline_name VARCHAR, operation_name VARCHAR,
                status VARCHAR, outcome VARCHAR, background BOOLEAN, error_class VARCHAR,
                origin VARCHAR, created_at DATETIME, started_at DATETIME, ended_at DATETIME
            );
        ''')

        # 15 project knowledge docs (10 state, 5 pitfall) + 1 embedded common + 3 raw docs
        self.k_docs = []
        for i in range(15):
            did = str(uuid.uuid4())
            self.k_docs.append(did)
            fp = self.data_dir / f'k-{i}.md'
            fp.write_text(f'# Session learning (session s-{i})\n\nKnowledge body number {i}. ' + ('说明文字' * 20), encoding='utf-8')
            lt = 'state' if i < 10 else 'pitfall'
            day = '2026-11-21' if i < 8 else '2026-11-20'
            c1.execute(
                'INSERT INTO data VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)',
                (
                    uuid.UUID(did).hex, uuid.UUID(self.ds_proj).hex, None,
                    f'text_{i:032x}.md', f'Topic {i}' if i == 0 else None, 'text/markdown',
                    f'file://{fp}',
                    json.dumps({'why_learned': 'context'}),
                    json.dumps({'lesson_type': lt, 'state_key': f'sk:{i}', 'session_date': day}),
                    json.dumps(json.dumps(['session_learnings', f'session_learnings:s-{i}'])),
                    json.dumps({'cognify_pipeline': {self.ds_proj: 'DATA_ITEM_PROCESSING_COMPLETED'}}),
                    f'2026-11-21 10:00:{i:02d}', f'2026-11-21 10:00:{i:02d}', f'hk-{i}',
                ),
            )

        # Embedded common doc inside project dataset (must be excluded from category_counts!)
        self.embedded_common = str(uuid.uuid4())
        embed_fp = self.data_dir / 'embed-common.md'
        embed_fp.write_text('# Embedded Common\n\nCommon preference note stored in project dataset.', encoding='utf-8')
        c1.execute(
            'INSERT INTO data VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)',
            (
                uuid.UUID(self.embedded_common).hex, uuid.UUID(self.ds_proj).hex, None,
                'embed-common.md', 'Embedded Common', 'text/markdown', f'file://{embed_fp}',
                json.dumps({'memlane_kind': 'common_knowledge', 'category': 'preference'}), None, None,
                '{}', '2026-11-21 09:00:00', '2026-11-21 09:00:00', 'he-c',
            ),
        )

        # Common dataset docs (2 preference, 1 experience)
        self.common_docs = [str(uuid.uuid4()) for _ in range(3)]
        for idx, (did, cat_val, upd) in enumerate((
            (self.common_docs[0], 'preference', '2026-11-21 12:00:00'),
            (self.common_docs[1], 'experience', '2026-11-21 11:00:00'),
            (self.common_docs[2], 'preference', '2026-11-21 10:00:00'),
        )):
            c1.execute(
                'INSERT INTO data VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)',
                (
                    uuid.UUID(did).hex, uuid.UUID(self.ds_common).hex, None,
                    f'common-{idx}.md', f'Common {idx}', 'text/markdown', '',
                    json.dumps({'memlane_kind': 'common_knowledge', 'category': cat_val}), None, None,
                    '{}', upd, '2026-11-20 08:00:00', f'hc-{idx}',
                ),
            )

        # 3 raw docs (1 pending, 2 completed) interleaved with session timestamps
        self.raw_docs = [str(uuid.uuid4()) for _ in range(3)]
        for idx, (did, ts, status_obj) in enumerate((
            (self.raw_docs[0], '2026-11-21 10:00:00', {}),
            (self.raw_docs[1], '2026-11-21 09:30:00', {'cognify_pipeline': {self.ds_proj: 'DATA_ITEM_PROCESSING_COMPLETED'}}),
            (self.raw_docs[2], '2026-11-21 09:00:00', {'cognify_pipeline': {self.ds_proj: 'DATA_ITEM_PROCESSING_COMPLETED'}}),
        )):
            c1.execute(
                'INSERT INTO data VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)',
                (
                    uuid.UUID(did).hex, uuid.UUID(self.ds_proj).hex, None,
                    f'raw-{idx}.md', f'Raw Doc {idx}', 'text/markdown', '',
                    json.dumps({'node_set': ['project_docs']}), None, None,
                    json.dumps(status_obj), ts, ts, f'hr-{idx}',
                ),
            )

        # Unconfirmed project doc
        self.unconf_doc = str(uuid.uuid4())
        c1.execute(
            'INSERT INTO data VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)',
            (
                uuid.UUID(self.unconf_doc).hex, uuid.UUID(self.ds_unconf).hex, None,
                'secret.md', 'Secret', 'text/markdown', '',
                json.dumps({'node_set': ['session_learnings']}), None, None,
                '{}', '2026-11-21 08:00:00', '2026-11-21 08:00:00', 'hu',
            ),
        )

        # Sessions: 2 users sharing IDENTICAL session_id + timestamp as raw_docs[0], plus 1 parent with 8 children!
        c1.executemany(
            'INSERT INTO session_records VALUES (?,?,?,?,?,?,?)',
            [
                ('sess-tie', uuid.UUID(self.user_a).hex, uuid.UUID(self.ds_proj).hex, 'active', '2026-11-21 09:00:00', '2026-11-21 10:00:00', 'codex'),
                ('sess-tie', uuid.UUID(self.user_b).hex, uuid.UUID(self.ds_proj).hex, 'active', '2026-11-21 09:00:00', '2026-11-21 10:00:00', 'claude'),
                ('sess-parent', uuid.UUID(self.user_a).hex, uuid.UUID(self.ds_proj).hex, 'active', '2026-11-21 08:00:00', '2026-11-21 09:45:00', 'codex'),
                *[
                    (f'sess-child-{i}', uuid.UUID(self.user_a).hex, uuid.UUID(self.ds_proj).hex, 'active', '2026-11-21 08:05:00', f'2026-11-21 09:40:{i:02d}', 'codex')
                    for i in range(8)
                ],
            ],
        )
        c1.commit()
        c1.close()

        # Populate cache.db with 1 expired row on sess-tie/user_a + valid QAs on sess-parent & children
        c2 = sqlite3.connect(self.cache_db)
        c2.executescript('''
            CREATE TABLE cache_qa_entries (
                seq INTEGER PRIMARY KEY, user_id TEXT NOT NULL, session_id TEXT NOT NULL,
                qa_id TEXT NOT NULL, payload JSON NOT NULL,
                created_at DATETIME DEFAULT CURRENT_TIMESTAMP NOT NULL, expires_at DATETIME
            );
            CREATE INDEX idx_cache_qa_session ON cache_qa_entries (user_id, session_id, seq);
            CREATE TABLE cache_trace_entries (
                seq INTEGER PRIMARY KEY, user_id TEXT NOT NULL, session_id TEXT NOT NULL,
                payload JSON NOT NULL,
                created_at DATETIME DEFAULT CURRENT_TIMESTAMP NOT NULL, expires_at DATETIME
            );
            CREATE INDEX idx_cache_trace_session ON cache_trace_entries (user_id, session_id, seq);
        ''')
        c2.executemany(
            'INSERT INTO cache_qa_entries (seq, user_id, session_id, qa_id, payload, expires_at) VALUES (?,?,?,?,?,?)',
            [
                (1, self.user_a, 'sess-tie', 'q-exp', json.dumps({'question': 'EXPIRED'}), '2020-01-01 00:00:00'),
                (2, self.user_a, 'sess-tie', 'q-empty', json.dumps({'question': ''}), '2099-01-01 00:00:00'),
                (3, self.user_a, 'sess-tie', 'q-ok', json.dumps({'question': 'Tie session question A'}), '2099-01-01 00:00:00'),
                (4, self.user_b, 'sess-tie', 'q-b', json.dumps({'question': 'Tie session question B'}), '2099-01-01 00:00:00'),
                (5, self.user_a, 'sess-parent', 'q-p', json.dumps({'question': 'Parent task question'}), '2099-01-01 00:00:00'),
            ],
        )
        c2.execute(
            'INSERT INTO cache_trace_entries (seq, user_id, session_id, payload, expires_at) VALUES (?,?,?,?,?)',
            (1, self.user_a, 'sess-parent', json.dumps({'origin_function': 'Read'}), '2099-01-01 00:00:00'),
        )
        c2.commit()
        c2.close()

        # Seed recall events for composite (agent_id, event_id) filter testing
        self.recall_event_a = str(uuid.uuid4())
        rec = RecallStore(self.root / 'knowledge-recall.sqlite3')
        rec.record('agent-1', {
            'event_id': self.recall_event_a,
            'channel': 'automatic',
            'hits': [
                {'dataset_id': self.ds_proj, 'document_id': self.k_docs[0]},
                {'dataset_id': self.ds_proj, 'document_id': self.k_docs[10]},
            ],
        })
        rec.record('agent-2', {
            'event_id': self.recall_event_a,  # SAME event_id, DIFFERENT agent_id!
            'channel': 'explicit',
            'hits': [{'dataset_id': self.ds_proj, 'document_id': self.k_docs[5]}],
        })
        rec.close()

        # Pre-seed a legacy knowledge-read-<hash>.json so all 15 k_docs are unread (> enabled_at)
        sh = knowledge_read_scope_hash(self.identity, 'p1', self.ds_proj)
        (self.root / f'knowledge-read-{sh}.json').write_text(
            json.dumps({
                'version': 1,
                'initialized': True,
                'enabled_at': '2026-11-21T09:59:00.000000000Z',
                'knowledge_ids': {},
            }),
            encoding='utf-8',
        )

        self.registry = Registry(self.root / 'registry.json', _DummyOfficial())
        self.registry.state['projects'] = {
            'p1': {'id': 'p1', 'name': 'Alpha', 'dataset_id': self.ds_proj, 'status': 'verified'},
            'p-unconf': {'id': 'p-unconf', 'name': 'Unconfirmed', 'dataset_id': self.ds_unconf, 'status': 'unconfirmed'},
        }
        self.registry.state['common'] = {
            'id': 'common', 'kind': 'common', 'dataset_id': self.ds_common, 'status': 'verified',
        }
        self.registry.state['session_relations'] = {
            f'sess-child-{i}': {'parent_session_id': 'sess-parent', 'subagent_role': 'worker', 'nesting_depth': 1}
            for i in range(8)
        }
        return create_app(
            self.registry,
            'm' * 40,
            {'agent-1': 'a' * 40},
            codex_config={'official_data': str(self.official_data)},
        )

    async def tearDownAsync(self):
        await super().tearDownAsync()
        self.registry.close()
        self.tmp.cleanup()

    async def test_overview_body_knowledge_common_search_and_reading_state_flow(self):
        # 1. Migrate local reading state via POST /v2/reading-state/migrate-local
        mig = await self.client.post(
            '/v2/reading-state/migrate-local',
            headers=self.manager_headers,
            json={'reading_identity': self.identity},
        )
        self.assertEqual(mig.status, 200)
        mig_body = await mig.json()
        self.assertEqual(mig_body['migrated'][0]['status'], 'migrated')
        self.assertEqual(mig_body['migrated'][0]['unread_count'], 15)

        # 2. GET /v2/overview/bootstrap (<= 128 KiB, excludes unconfirmed project)
        ov_resp = await self.client.get('/v2/overview/bootstrap', headers=self.manager_headers)
        self.assertEqual(ov_resp.status, 200)
        raw_ov = await ov_resp.read()
        self.assertLessEqual(len(raw_ov), 128 * 1024)
        ov = json.loads(raw_ov)
        self.assertEqual([p['project_id'] for p in ov['projects']], ['p1'])
        self.assertEqual(ov['projects'][0]['unread_count'], 15)
        self.assertEqual(ov['common']['knowledge_count'], 3)
        self.assertGreater(len(ov['day_buckets']), 0)
        self.assertTrue(ov['recent_items'][0]['preview_text'])

        # 3. GET /v2/overview/day cursor pagination + tamper rejection
        day1 = await (await self.client.get('/v2/overview/day?day=2026-11-21&limit=4', headers=self.manager_headers)).json()
        self.assertEqual(len(day1['items']), 4)
        self.assertTrue(day1['has_more'])
        day2 = await (await self.client.get(f"/v2/overview/day?day=2026-11-21&limit=4&cursor={day1['next_cursor']}", headers=self.manager_headers)).json()
        self.assertEqual(len(day2['items']), 4)
        self.assertFalse(set(i['doc_id'] for i in day1['items']) & set(i['doc_id'] for i in day2['items']))
        bad_cur = await self.client.get(f"/v2/overview/day?day=2026-11-20&cursor={day1['next_cursor']}", headers=self.manager_headers)
        self.assertEqual(bad_cur.status, 400)
        self.assertEqual((await bad_cur.json())['error'], 'invalid_cursor')

        # 4. GET /v2/projects/p1/knowledge: unconfirmed rejected, category_counts semantics, composite recall key
        self.assertEqual((await self.client.get('/v2/projects/p-unconf/knowledge', headers=self.manager_headers)).status, 403)
        self.assertEqual((await self.client.get(f'/v2/projects/p1/knowledge?recall_event_id={self.recall_event_a}', headers=self.manager_headers)).status, 400)

        # Composite (agent-1, event) returns 2 hits (1 项目状态 + 1 踩坑经验); filtering category keeps BOTH in category_counts!
        rec_filtered = await (await self.client.get(
            f'/v2/projects/p1/knowledge?recall_agent_id=agent-1&recall_event_id={self.recall_event_a}&category=项目状态',
            headers=self.manager_headers,
        )).json()
        self.assertEqual(rec_filtered['category_counts'], {'项目状态': 1, '踩坑经验': 1})
        self.assertEqual([i['doc_id'] for i in rec_filtered['items']], [self.k_docs[0]])

        # Different agent_id with SAME event_id gets its own distinct hit!
        rec_a2 = await (await self.client.get(
            f'/v2/projects/p1/knowledge?recall_agent_id=agent-2&recall_event_id={self.recall_event_a}',
            headers=self.manager_headers,
        )).json()
        self.assertEqual([i['doc_id'] for i in rec_a2['items']], [self.k_docs[5]])

        # Full pagination through all 16 project knowledge items (15 lessons + 1 embedded common, 0 dupes)
        seen_k = []
        cursor = ''
        while True:
            q = f'/v2/projects/p1/knowledge?limit=5' + (f'&cursor={cursor}' if cursor else '')
            page = await (await self.client.get(q, headers=self.manager_headers)).json()
            seen_k.extend(i['doc_id'] for i in page['items'])
            if not page['has_more']:
                break
            cursor = page['next_cursor']
        self.assertEqual(len(seen_k), 16)
        self.assertEqual(len(set(seen_k)), 16)

        # 5. GET /v2/common/knowledge ordering + filter
        com_all = await (await self.client.get('/v2/common/knowledge?limit=10', headers=self.manager_headers)).json()
        self.assertEqual([i['doc_id'] for i in com_all['items']], self.common_docs)
        com_pref = await (await self.client.get('/v2/common/knowledge?common_category=preference', headers=self.manager_headers)).json()
        self.assertEqual([i['doc_id'] for i in com_pref['items']], [self.common_docs[0], self.common_docs[2]])

        # 6. GET /v2/datasets/{ds}/documents/{doc}/body fast-path
        body_resp = await self.client.get(f'/v2/datasets/{self.ds_proj}/documents/{self.k_docs[0]}/body', headers=self.manager_headers)
        self.assertEqual(body_resp.status, 200)
        b_json = await body_resp.json()
        self.assertEqual(b_json['source'], 'local_fastpath')
        self.assertIn('Knowledge body number 0', b_json['body'])

        # 7. GET /v2/search/recent & /v2/search/metadata
        recent = await (await self.client.get('/v2/search/recent?limit=5', headers=self.manager_headers)).json()
        self.assertEqual(len(recent['knowledge_items']), 5)
        self.assertGreaterEqual(len(recent['session_items']), 1)

        meta_s = await (await self.client.get('/v2/search/metadata?q=Topic%200&filter=knowledge', headers=self.manager_headers)).json()
        self.assertEqual([i['doc_id'] for i in meta_s['items']], [self.k_docs[0]])
        self.assertEqual(meta_s['items'][0]['match_field'], 'doc_label')

        # 8. GET /v2/projects/p1/message-summary (slim < 16 KiB, unread_count=15, preview=10, unread_has_more=True)
        ms_resp = await self.client.get('/v2/projects/p1/message-summary', headers=self.manager_headers)
        self.assertEqual(ms_resp.status, 200)
        ms_raw = await ms_resp.read()
        self.assertLessEqual(len(ms_raw), 16 * 1024)
        ms = json.loads(ms_raw)
        self.assertNotIn('knowledge_catalog', ms)
        self.assertEqual(ms['unread_count'], 15)
        self.assertEqual(len(ms['unread_preview']), 10)
        self.assertTrue(ms['unread_has_more'])
        self.assertEqual(ms['pending_documents_count'], 1)

        # 9. POST /v2/reading-state/mark-read & mark-all-read
        mr = await (await self.client.post(
            '/v2/reading-state/mark-read',
            headers=self.manager_headers,
            json={'reading_identity': self.identity, 'project_id': 'p1', 'dataset_id': self.ds_proj, 'doc_ids': [self.k_docs[0]]},
        )).json()
        self.assertEqual(mr['unread_count'], 14)

        mar = await (await self.client.post(
            '/v2/reading-state/mark-all-read',
            headers=self.manager_headers,
            json={'reading_identity': self.identity, 'project_id': 'p1', 'dataset_id': self.ds_proj},
        )).json()
        self.assertEqual(mar['unread_count'], 0)

    async def test_raw_records_v2_dual_branch_merge_tie_breaker_and_children_paging(self):
        # Traverse ALL top-level raw records with page size 2 -> crosses exact timestamp tie between
        # (sess-tie, user_a), (sess-tie, user_b), and raw_docs[0] with ZERO dropped or duplicated rows!
        collected = []
        cursor = ''
        while True:
            q = '/v2/projects/p1/raw-records?kind=all&limit=2' + (f'&cursor={cursor}' if cursor else '')
            resp = await self.client.get(q, headers=self.manager_headers)
            self.assertEqual(resp.status, 200)
            page = await resp.json()
            for entry in page['items']:
                if entry['item_kind'] == 'session':
                    s = entry['session']
                    collected.append(('session', s['user_id'], s['session_id'], s))
                else:
                    d = entry['document']
                    collected.append(('document', '', d['id'], d))
            if not page['has_more']:
                break
            cursor = page['next_cursor']

        # Expect exactly 3 top-level sessions (2 sess-tie users + 1 sess-parent) + 3 raw docs = 6 items
        identities = [(k, u, i) for k, u, i, _ in collected]
        self.assertEqual(len(identities), 6)
        self.assertEqual(len(set(identities)), 6)

        # Verify expired row excluded & empty question fallback worked on sess-tie/user_a
        by_key = {(k, u, i): obj for k, u, i, obj in collected}
        s_a = by_key[('session', self.user_a, 'sess-tie')]
        self.assertEqual(s_a['msg_count'], 2)
        self.assertEqual(s_a['label'], 'Tie session question A')
        s_b = by_key[('session', self.user_b, 'sess-tie')]
        self.assertEqual(s_b['msg_count'], 1)
        self.assertEqual(s_b['label'], 'Tie session question B')

        # Parent session shows child_count=8, 5 preview children, children_has_more=True
        s_parent = by_key[('session', self.user_a, 'sess-parent')]
        self.assertEqual(s_parent['child_count'], 8)
        self.assertEqual(len(s_parent['children_preview']), 5)
        self.assertTrue(s_parent['children_has_more'])
        self.assertEqual(s_parent['tool_calls'], 1)

        # Paginate /children with limit=3 -> reaches all 8 subagent sessions with 0 dupes!
        children_seen = []
        ccursor = ''
        while True:
            q = '/v2/projects/p1/raw-records/sessions/sess-parent/children?limit=3' + (f'&cursor={ccursor}' if ccursor else '')
            cpage = await (await self.client.get(q, headers=self.manager_headers)).json()
            self.assertEqual(cpage['total_count'], 8)
            children_seen.extend(row['session_id'] for row in cpage['items'])
            if not cpage['has_more']:
                break
            ccursor = cpage['next_cursor']
        self.assertEqual(len(children_seen), 8)
        self.assertEqual(len(set(children_seen)), 8)


if __name__ == '__main__':
    unittest.main()
