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
from provenance_store import ProvenanceStore, install_provenance_middleware, resolve_lesson_date


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

    async def test_get_enrichment_is_strictly_read_only_and_blocks_external_metadata_poisoning(self):
        with tempfile.TemporaryDirectory() as temporary:
            store = ProvenanceStore(Path(temporary) / 'provenance.sqlite3')
            ds = str(uuid.uuid4())
            doc_existing = str(uuid.uuid4())
            doc_untracked = str(uuid.uuid4())
            doc_no_qa = str(uuid.uuid4())

            # Seed legitimate provenance for state_key='auth-policy'.
            store.record_document(
                ds,
                doc_existing,
                lesson_type='state',
                state_key='auth-policy',
                candidate_dates=['2026-11-15'],
                source_qa_ids=['qa-real'],
            )
            doc_count_before = store.db.execute('SELECT COUNT(*) FROM document_provenance').fetchone()[0]
            key_count_before = store.db.execute('SELECT COUNT(*) FROM keyed_provenance').fetchone()[0]

            # Call enrich_data_items with attacker-planted externalMetadata and empty source_qa_ids.
            enriched = store.enrich_data_items(ds, [
                {
                    'id': doc_existing,
                    'externalMetadata': {
                        'lesson_type': 'state',
                        'state_key': 'auth-policy',
                        'first_source_date': '1999-01-01',
                    },
                },
                {
                    'id': doc_untracked,
                    'externalMetadata': {
                        'lesson_type': 'state',
                        'state_key': 'auth-policy',
                        'first_source_date': '1999-01-01',
                    },
                },
                {
                    'id': doc_no_qa,
                    'externalMetadata': {
                        'lesson_type': 'state',
                        'state_key': 'brand-new-key',
                        'first_source_date': '2020-05-05',
                    },
                },
                {
                    'id': str(uuid.uuid4()),
                    'systemMetadata': {
                        'lesson_type': 'state',
                        'state_key': 'sys-wins',
                        'session_date': '2026-11-18',
                    },
                    'externalMetadata': {
                        'lesson_type': 'state',
                        'state_key': 'sys-wins',
                        'first_source_date': '1999-01-01',
                        'session_date': '1999-01-01',
                    },
                },
            ])

            # 1. Existing state_key retains true locked date '2026-11-15', ignoring planted '1999-01-01'.
            self.assertEqual(enriched[0]['externalMetadata']['first_source_date'], '2026-11-15')
            self.assertEqual(enriched[0]['externalMetadata']['source_date_status'], 'linked_qa')
            self.assertEqual(enriched[1]['externalMetadata']['first_source_date'], '2026-11-15')

            # 2. Empty source_qa_ids must never be mislabeled as linked_qa.
            self.assertNotEqual(enriched[2]['externalMetadata']['source_date_status'], 'linked_qa')

            # 3. systemMetadata wins over user-editable externalMetadata.
            self.assertEqual(enriched[3]['externalMetadata']['first_source_date'], '2026-11-18')
            self.assertEqual(enriched[3]['externalMetadata']['source_date_status'], 'session_date')

            # 4. Zero writes occurred in SQLite during enrich_data_items.
            self.assertEqual(store.db.execute('SELECT COUNT(*) FROM document_provenance').fetchone()[0], doc_count_before)
            self.assertEqual(store.db.execute('SELECT COUNT(*) FROM keyed_provenance').fetchone()[0], key_count_before)
            self.assertEqual(
                store.db.execute(
                    'SELECT first_source_date FROM keyed_provenance WHERE dataset_id=? AND lesson_type=? AND state_key=?',
                    (ds, 'state', 'auth-policy'),
                ).fetchone()[0],
                '2026-11-15',
            )
            self.assertIsNone(store.lookup(ds, doc_no_qa, lesson_type='state', state_key='brand-new-key'))
            # Non-hyphenated UUID lookup matches canonical stored row.
            self.assertEqual(
                store.lookup(uuid.UUID(ds).hex, uuid.UUID(doc_existing).hex)['first_source_date'],
                '2026-11-15',
            )
            store.close()

    def test_resolve_lesson_date_and_enrich_data_items_agree_on_unlocked_historical_docs(self):
        from datetime import datetime
        with tempfile.TemporaryDirectory() as temporary:
            store = ProvenanceStore(Path(temporary) / 'provenance.sqlite3')
            ds = str(uuid.uuid4())
            created_iso = '2026-11-20T05:10:00+00:00'
            expected_created_day = datetime.fromisoformat(created_iso).astimezone().strftime('%Y-%m-%d')

            cases = [
                # 1. Historical QA-less doc (externalMetadata only) with createdAt -> created_at wins over unconfirmed first_source_date.
                (
                    None,
                    {'lesson_type': 'state', 'state_key': 'hist-ext', 'first_source_date': '2020-05-05'},
                    created_iso,
                    expected_created_day,
                    'created_at',
                    '',
                ),
                # 2. Trusted systemMetadata.first_source_date (even without QA IDs) keeps priority over created_at,
                #    never mislabeled as linked_qa.
                (
                    {'lesson_type': 'state', 'state_key': 'hist-sys', 'first_source_date': '2020-05-05'},
                    {'lesson_type': 'state', 'state_key': 'hist-sys', 'first_source_date': '1999-01-01'},
                    created_iso,
                    '2020-05-05',
                    'unconfirmed',
                    '2020-05-05',
                ),
                # 3. Historical QA-less doc without createdAt (even with forged declared status) -> first_source_date + unconfirmed.
                (
                    None,
                    {
                        'lesson_type': 'state',
                        'state_key': 'hist-no-created',
                        'first_source_date': '2020-05-05',
                        'source_date_status': 'session_date',
                    },
                    None,
                    '2020-05-05',
                    'unconfirmed',
                    '2020-05-05',
                ),
                # 4. session_date wins over QA-less external first_source_date and createdAt.
                (
                    None,
                    {
                        'lesson_type': 'state',
                        'state_key': 'hist-sess',
                        'first_source_date': '1999-01-01',
                        'session_date': '2026-11-18',
                    },
                    created_iso,
                    '2026-11-18',
                    'session_date',
                    '2026-11-18',
                ),
                # 5. Trusted systemMetadata QA-backed first_source_date -> linked_qa.
                (
                    {
                        'lesson_type': 'state',
                        'state_key': 'hist-sys-qa',
                        'first_source_date': '2026-11-12',
                        'session_date': '2026-11-18',
                        'source_qa_ids': ['qa-1'],
                    },
                    {
                        'lesson_type': 'state',
                        'state_key': 'hist-sys-qa',
                        'first_source_date': '1999-01-01',
                    },
                    created_iso,
                    '2026-11-12',
                    'linked_qa',
                    '2026-11-12',
                ),
                # 6. Untrusted externalMetadata self-asserted source_qa_ids NEVER yields linked_qa
                #    and NEVER overrides created_at.
                (
                    None,
                    {
                        'lesson_type': 'state',
                        'state_key': 'hist-ext-spoof-qa',
                        'first_source_date': '1999-01-01',
                        'source_qa_ids': ['qa-fake'],
                    },
                    created_iso,
                    expected_created_day,
                    'created_at',
                    '',
                ),
                # 7. Untrusted externalMetadata with self-asserted source_qa_ids and no createdAt -> unconfirmed, NEVER linked_qa.
                (
                    None,
                    {
                        'lesson_type': 'state',
                        'state_key': 'hist-ext-spoof-qa-no-created',
                        'first_source_date': '1999-01-01',
                        'source_qa_ids': ['qa-fake'],
                    },
                    None,
                    '1999-01-01',
                    'unconfirmed',
                    '1999-01-01',
                ),
            ]

            for sys_meta, ext_meta, created_at, expected_day, expected_status, expected_no_created_fallback in cases:
                doc_id = str(uuid.uuid4())
                item = {'id': doc_id, 'externalMetadata': dict(ext_meta)}
                if sys_meta is not None:
                    item['systemMetadata'] = dict(sys_meta)
                if created_at is not None:
                    item['createdAt'] = created_at

                enriched = store.enrich_data_items(ds, [item])[0]['externalMetadata']
                fast_day = resolve_lesson_date(
                    ds,
                    doc_id,
                    system_metadata=sys_meta,
                    external_metadata=ext_meta,
                    created_at=created_at,
                    include_created_fallback=True,
                )
                catalog_day_sqlite = resolve_lesson_date(
                    ds,
                    doc_id,
                    system_metadata=sys_meta,
                    external_metadata=ext_meta,
                    created_at=created_at,
                    include_created_fallback=False,
                )
                catalog_day_http = resolve_lesson_date(
                    ds,
                    doc_id,
                    system_metadata=sys_meta,
                    external_metadata=enriched,
                    created_at=created_at,
                    include_created_fallback=False,
                )
                self.assertEqual(enriched['first_source_date'], expected_day)
                self.assertEqual(fast_day, expected_day)
                self.assertEqual(enriched['source_date_status'], expected_status)
                self.assertEqual(catalog_day_sqlite, expected_no_created_fallback)
                self.assertEqual(catalog_day_http, expected_no_created_fallback)

            # Strictly zero SQLite writes during GET enrichment.
            self.assertEqual(store.db.execute('SELECT COUNT(*) FROM document_provenance').fetchone()[0], 0)
            self.assertEqual(store.db.execute('SELECT COUNT(*) FROM keyed_provenance').fetchone()[0], 0)
            store.close()


if __name__ == '__main__':
    unittest.main()
