import json
from pathlib import Path
import sqlite3
import tempfile
import unittest
import uuid

from aiohttp.test_utils import AioHTTPTestCase

from project_control import Registry
from project_control_server import create_app
from provenance_store import ProvenanceStore


class _DummyOfficial:
    pass


class OverviewBootstrapTest(AioHTTPTestCase):
    async def get_application(self):
        self.tmp = tempfile.TemporaryDirectory()
        root = Path(self.tmp.name)
        self.official_data = root / 'official-store'
        db_dir = self.official_data / 'system' / 'databases'
        data_dir = self.official_data / 'data'
        db_dir.mkdir(parents=True)
        data_dir.mkdir(parents=True)

        self.ds_managed = str(uuid.uuid4())
        self.ds_other = str(uuid.uuid4())
        self.doc_id = str(uuid.uuid4())
        self.doc_other = str(uuid.uuid4())
        self.run_id = str(uuid.uuid4())

        raw_file = data_dir / 'lesson.txt'
        raw_file.write_text('Memlane overview bootstrap preview sentence. Second sentence.', encoding='utf-8')
        outside_file = root / 'secret.txt'
        outside_file.write_text('must never be read', encoding='utf-8')

        con = sqlite3.connect(db_dir / 'cognee_db')
        con.executescript('''
            CREATE TABLE data (
                id CHAR(32) PRIMARY KEY, dataset_id CHAR(32), name VARCHAR, label VARCHAR,
                mime_type VARCHAR, raw_data_location VARCHAR, external_metadata JSON,
                node_set JSON, updated_at DATETIME, created_at DATETIME, content_hash VARCHAR
            );
            CREATE TABLE session_records (
                session_id VARCHAR, user_id CHAR(32), dataset_id CHAR(32),
                agent_id VARCHAR, last_activity_at DATETIME, started_at DATETIME
            );
            CREATE TABLE pipeline_runs (
                id CHAR(32) PRIMARY KEY, pipeline_run_id CHAR(32), parent_operation_id CHAR(32),
                dataset_id CHAR(32), pipeline_name VARCHAR, operation_name VARCHAR,
                status VARCHAR, outcome VARCHAR, background BOOLEAN, error_class VARCHAR,
                origin VARCHAR, created_at DATETIME, started_at DATETIME, ended_at DATETIME
            );
        ''')
        con.execute(
            'INSERT INTO data VALUES(?,?,?,?,?,?,?,?,?,?,?)',
            (
                uuid.UUID(self.doc_id).hex,
                uuid.UUID(self.ds_managed).hex,
                'text_123',
                None,
                'text/plain',
                f'file://{raw_file}',
                json.dumps({'lesson_type': 'state', 'state_key': 'k1', 'node_set': ['session_learnings']}),
                json.dumps(['session_learnings']),
                '2026-11-20 12:00:00',
                '2026-11-20 12:00:00',
                'hash1',
            ),
        )
        # Unmanaged dataset doc must be filtered out
        con.execute(
            'INSERT INTO data VALUES(?,?,?,?,?,?,?,?,?,?,?)',
            (
                uuid.UUID(self.doc_other).hex,
                uuid.UUID(self.ds_other).hex,
                'other',
                None,
                'text/plain',
                f'file://{outside_file}',
                json.dumps({'lesson_type': 'state', 'node_set': ['session_learnings']}),
                json.dumps(['session_learnings']),
                '2026-11-20 12:00:00',
                '2026-11-20 12:00:00',
                'hash2',
            ),
        )
        con.execute(
            'INSERT INTO session_records VALUES(?,?,?,?,?,?)',
            ('sess-1', uuid.uuid4().hex, uuid.UUID(self.ds_managed).hex, 'agent-1', '2026-11-20 12:00:00', '2026-11-20 11:00:00'),
        )
        con.execute(
            'INSERT INTO pipeline_runs VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)',
            (
                uuid.uuid4().hex,
                uuid.UUID(self.run_id).hex,
                None,
                uuid.UUID(self.ds_managed).hex,
                None,
                'improve',
                None,
                'succeeded',
                1,
                None,
                'api',
                '2026-11-20 12:05:00',
                '2026-11-20 12:04:00',
                '2026-11-20 12:05:00',
            ),
        )
        con.commit()
        con.close()

        reg_path = root / 'registry.json'
        prov = ProvenanceStore(root / 'lesson-provenance.sqlite3')
        prov.record_document(
            self.ds_managed,
            self.doc_id,
            lesson_type='state',
            state_key='k1',
            candidate_dates=('2026-11-15',),
        )
        prov.close()

        self.registry = Registry(reg_path, _DummyOfficial())
        self.registry.state['projects'] = {
            'p1': {
                'id': 'p1',
                'name': 'memlane',
                'dataset_id': self.ds_managed,
                'status': 'verified',
            }
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

    async def test_overview_bootstrap_returns_enriched_snapshot_and_rejects_agent(self):
        # Agent token must be rejected (manager-only endpoint)
        denied = await self.client.get(
            '/v1/overview/bootstrap',
            headers={'Authorization': 'Bearer ' + self.agent_token},
        )
        self.assertEqual(denied.status, 403)

        resp = await self.client.get(
            '/v1/overview/bootstrap',
            headers={'Authorization': 'Bearer ' + self.manager_token},
        )
        self.assertEqual(resp.status, 200)
        payload = await resp.json()
        self.assertEqual(len(payload['documents']), 1)
        doc = payload['documents'][0]
        self.assertEqual(doc['id'], self.doc_id)
        self.assertEqual(doc['datasetId'], self.ds_managed)
        self.assertEqual(doc['externalMetadata']['first_source_date'], '2026-11-15')
        self.assertEqual(len(payload['sessions']), 1)
        self.assertEqual(payload['sessions'][0]['session_id'], 'sess-1')
        self.assertEqual(len(payload['pipeline_runs']), 1)
        self.assertEqual(payload['pipeline_runs'][0]['pipeline_run_id'], self.run_id)
        self.assertIn(f'{self.ds_managed}/{self.doc_id}', payload['previews'])
        self.assertIn('bootstrap preview sentence', payload['previews'][f'{self.ds_managed}/{self.doc_id}'])


if __name__ == '__main__':
    unittest.main()
