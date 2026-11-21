"""Tests for External ProvenanceStore index and FastAPI/ASGI middleware."""
import json
import os
from pathlib import Path
import stat
import sys
import tempfile
from types import SimpleNamespace
import unittest
import uuid

from fastapi import FastAPI
from httpx import ASGITransport, AsyncClient

sys.path.insert(0, str(Path(__file__).resolve().parent))
from provenance_store import ProvenanceStore, install_provenance_middleware


class ProvenanceStoreTests(unittest.IsolatedAsyncioTestCase):
    def test_permissions_symlink_and_zero_text_schema(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            db_path = root / 'provenance.sqlite3'
            store = ProvenanceStore(db_path)
            self.assertEqual(stat.S_IMODE(os.stat(db_path).st_mode), 0o600)
            cols = {
                row[1]
                for table in ('keyed_provenance', 'document_provenance')
                for row in store.db.execute(f'PRAGMA table_info({table})').fetchall()
            }
            self.assertFalse(cols & {'statement', 'text', 'raw_data', 'question', 'answer'})
            store.close()

            link = root / 'link.sqlite3'
            link.symlink_to(db_path)
            with self.assertRaises(ValueError):
                ProvenanceStore(link)

    def test_sqlite_wal_pragmas_and_checkpoint(self):
        with tempfile.TemporaryDirectory() as temporary:
            db_path = Path(temporary) / 'lesson-provenance.sqlite3'
            store = ProvenanceStore(db_path)
            self.assertEqual(store.db.execute('PRAGMA journal_mode').fetchone()[0].lower(), 'wal')
            self.assertEqual(store.db.execute('PRAGMA journal_size_limit').fetchone()[0], 4194304)
            self.assertEqual(store.db.execute('PRAGMA wal_autocheckpoint').fetchone()[0], 1000)
            self.assertTrue(store.checkpoint('TRUNCATE'))
            store.close()

    def test_state_key_min_date_preservation_and_option_b_fallbacks(self):
        from datetime import datetime
        with tempfile.TemporaryDirectory() as temporary:
            store = ProvenanceStore(Path(temporary) / 'provenance.sqlite3')
            ds = str(uuid.uuid4())
            doc1 = str(uuid.uuid4())
            doc2 = str(uuid.uuid4())
            doc_session = str(uuid.uuid4())
            doc_created = str(uuid.uuid4())

            first = store.record_document(
                ds, doc1, lesson_type='state', state_key='test-baseline',
                candidate_dates=['2026-11-14', '2026-11-15'], source_qa_ids=['qa-b', 'qa-a'],
            )
            self.assertEqual(first['first_source_date'], '2026-11-14')
            self.assertEqual(first['source_date_status'], 'linked_qa')
            self.assertEqual(first['source_qa_ids'], ['qa-a', 'qa-b'])

            # Replacing a state-keyed lesson in a newer session retains earliest source date.
            second = store.record_document(
                ds, doc2, lesson_type='state', state_key='test-baseline',
                candidate_dates=['2026-11-16'], source_qa_ids=['qa-c'],
                explicit_status='session_date',
            )
            self.assertEqual(second['first_source_date'], '2026-11-14')
            self.assertEqual(second['source_date_status'], 'linked_qa')

            # Simulate legacy sticky ('', 'unconfirmed') rows upgrading under Option B.
            store.record_document(ds, doc_session, explicit_status='unconfirmed')
            store.record_document(ds, doc_created, explicit_status='unconfirmed')

            created_iso = '2026-11-20T05:10:00+00:00'
            expected_created_day = datetime.fromisoformat(created_iso).astimezone().strftime('%Y-%m-%d')

            enriched = store.enrich_data_items(ds, [
                {
                    'id': doc_session,
                    'externalMetadata': {
                        'node_set': ['session_learnings'],
                        'session_date': '2026-11-19',
                        'source_date_status': 'unconfirmed',
                    },
                },
                {
                    'id': doc_created,
                    'createdAt': created_iso,
                    'externalMetadata': {
                        'node_set': ['session_learnings'],
                    },
                },
                {
                    'id': doc2,
                    'external_metadata': {
                        'lesson_type': 'state',
                        'state_key': 'test-baseline',
                        'session_date': '2026-11-16',
                    },
                },
            ])
            self.assertEqual(enriched[0]['externalMetadata']['first_source_date'], '2026-11-19')
            self.assertEqual(enriched[0]['externalMetadata']['source_date_status'], 'session_date')
            self.assertEqual(enriched[1]['externalMetadata']['first_source_date'], expected_created_day)
            self.assertEqual(enriched[1]['externalMetadata']['source_date_status'], 'created_at')
            self.assertEqual(enriched[2]['external_metadata']['first_source_date'], '2026-11-14')
            self.assertEqual(enriched[2]['external_metadata']['source_date_status'], 'linked_qa')
            store.close()

    async def test_sync_dataset_prefers_system_metadata_and_middleware_enrich(self):
        with tempfile.TemporaryDirectory() as temporary:
            store = ProvenanceStore(Path(temporary) / 'provenance.sqlite3')
            ds = str(uuid.uuid4())
            doc1 = str(uuid.uuid4())
            doc2 = str(uuid.uuid4())

            async def fake_fetch_rows(dataset_id):
                return [
                    SimpleNamespace(
                        id=doc1,
                        system_metadata={
                            'lesson_type': 'state',
                            'state_key': 'footprint:auth',
                            'session_date': '2026-11-14',
                            'source_qa_ids': ['qa-1'],
                        },
                        # Planted external_metadata date must be ignored when system_metadata exists.
                        external_metadata={'first_source_date': '1999-01-01', 'session_date': '1999-01-01'},
                    ),
                    SimpleNamespace(
                        id=doc2,
                        system_metadata={
                            'lesson_type': 'state',
                            'state_key': 'footprint:auth',
                            'first_source_date': '2026-11-16',
                        },
                        external_metadata={},
                    ),
                ]

            def fake_qa_dates(qa_ids=None, session_id=None, trusted_metadata=None):
                return ['2026-11-14T10:15:00+00:00'] if 'qa-1' in (qa_ids or []) else []

            app = FastAPI()
            sync_calls = []

            async def custom_sync(dataset_id, session_ids=None):
                sync_calls.append((dataset_id, session_ids))
                await store.sync_dataset(dataset_id, fetch_rows=fake_fetch_rows, fetch_session_qa_dates=fake_qa_dates, session_ids=session_ids)

            install_provenance_middleware(app, store, sync_hook=custom_sync)

            @app.post('/api/v1/improve')
            async def improve(body: dict):
                return {'status': 'completed', 'dataset_id': body.get('dataset_id')}

            @app.get('/api/v1/datasets/{dataset_id}/data')
            async def list_data(dataset_id: str):
                return [
                    {
                        'id': doc2,
                        'name': 'replacement lesson',
                        'externalMetadata': {'lesson_type': 'state', 'state_key': 'footprint:auth'},
                    },
                    {
                        'id': str(uuid.uuid4()),
                        'name': 'plain manual note',
                        'externalMetadata': {'source': 'manual'},
                    },
                ]

            async with AsyncClient(transport=ASGITransport(app=app), base_url='http://127.0.0.1:18999') as client:
                r_imp = await client.post('/api/v1/improve', json={'dataset_id': ds, 'session_ids': ['s1']})
                self.assertEqual(r_imp.status_code, 200)
                self.assertEqual(sync_calls, [(ds, ['s1'])])

                r_data = await client.get(f'/api/v1/datasets/{ds}/data')
                self.assertEqual(r_data.status_code, 200)
                items = r_data.json()
                self.assertEqual(items[0]['externalMetadata']['first_source_date'], '2026-11-14')
                self.assertEqual(items[0]['externalMetadata']['source_date_status'], 'linked_qa')
                self.assertNotIn('first_source_date', items[1]['externalMetadata'])
            store.close()


if __name__ == '__main__':
    unittest.main()
