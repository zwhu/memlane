import asyncio
from contextvars import ContextVar
from datetime import date
import hashlib
import os
from types import SimpleNamespace
import unittest
from unittest.mock import AsyncMock, Mock, patch
from uuid import uuid4

from fastapi import FastAPI
import httpx
from pydantic import BaseModel

from engines.cognee import PolicyError
from engines.cognee.engine_api import CogneeBackend, Extract, Ingest, Scope, Search, SearchFilters, install_engine_api
from engines.cognee.handoff import ModelHandoffController
from engines.cognee.language_egress import current_output_language, set_default_language
from engines.cognee.runner import SetupOnlyMiddleware


class Backend:
    def __init__(self):
        self.received = []
        self.items = [{'id': 'one'}, {'id': 'two'}, {'id': 'three'}]

    async def ingest(self, payload, content):
        await asyncio.sleep(0)
        self.received.append((payload.kind, content))
        return {'entry_id': 'entry', 'document_id': 'one' if payload.kind == 'document' else None}

    async def extract(self, payload):
        return {'touched_doc_ids': payload.doc_ids,
                'pipeline_run_id': f'{payload.mode}-{current_output_language()}'}

    async def search(self, payload, limit):
        hits = [{'dataset_id': scope.dataset_id, 'document_id': 'one', 'title': 'title',
                 'snippet': payload.query, 'score': .1, 'metadata': {'limit': limit, 'kind': payload.filters.kind}}
                for scope in payload.scopes]
        if payload.mode == 'hybrid':
            return {
                'hits': hits,
                'graph_context': {
                    'facts': [{'dataset_id': payload.scopes[0].dataset_id, 'text': 'fact-1'}],
                    'session_guidance': f'session:{payload.filters.session_id}' if payload.filters.include_session_context else '',
                },
            }
        return hits

    async def list_items(self, scope, limit, offset):
        return self.items[offset:offset + limit]

    async def get_item(self, scope, item_id):
        for item in self.items:
            if item['id'] == item_id:
                return item
        raise PolicyError('item_not_found', 404)

    async def delete_item(self, scope, item_id):
        self.items.remove(await self.get_item(scope, item_id))
        return {'deleted': True}

    async def graph(self, dataset_id, filter_text):
        return {'nodes': [{'id': dataset_id, 'label': filter_text}], 'edges': []}

    async def ensure_dataset(self, name):
        return {'id': 'ds-ensured', 'name': name, 'created_at': '2026-11-27T00:00:00Z'}

    async def index_code(self, payload):
        return {'status': 'completed', 'dataset_id': payload.dataset_id, 'pipeline_run_id': 'run-1', 'items_processed': 3}


class CogneeBackendTests(unittest.IsolatedAsyncioTestCase):
    def setUp(self):
        self.enterContext(patch.dict(os.environ, {'LITELLM_LOCAL_MODEL_COST_MAP': 'true'}))
        self.backend = CogneeBackend()
        self.dataset = uuid4()
        self.scope = Scope(dataset_id=str(self.dataset))
        self.user = object()
        self.enterContext(patch.object(self.backend, '_dataset', return_value=(self.user, self.dataset)))

    def dataset_access(self, *, existing=None, authorization=None):
        owner = SimpleNamespace(id=uuid4(), tenant_id=uuid4())
        default = self.enterContext(patch(
            'cognee.modules.users.methods.get_default_user.get_default_user', return_value=owner))
        authorize = self.enterContext(patch(
            'cognee.modules.data.methods.get_authorized_existing_datasets', new_callable=AsyncMock))
        authorize.return_value = []
        if authorization is not None:
            authorize.side_effect = authorization
        relational = self.enterContext(patch('cognee.infrastructure.databases.relational.get_relational_engine'))
        session = relational.return_value.get_async_session.return_value.__aenter__.return_value
        session.get = AsyncMock(return_value=existing)
        session.add = Mock()
        session.commit = AsyncMock()
        session.rollback = AsyncMock()
        grant = self.enterContext(patch('cognee.modules.users.permissions.methods.give_permission_on_dataset'))
        return CogneeBackend(), owner, default, authorize, session, grant

    async def test_owner_ingest_provisions_a_fresh_dataset(self):
        from cognee.modules.data.models import Dataset
        from cognee.modules.users.exceptions import PermissionDeniedError

        backend, owner, default, authorize, session, grant = self.dataset_access()
        with (patch('cognee.remember', return_value=SimpleNamespace(status='session_stored', entry_id='qa')) as remember,
              patch('cognee.add', return_value=SimpleNamespace(data_ids=['doc'])) as add):
            for user_id, kind in ((None, 'capture_qa'), ('', 'document'), ('  ', 'document')):
                with self.subTest(user_id=user_id):
                    default.reset_mock()
                    authorize.reset_mock()
                    session.add.reset_mock()
                    session.commit.reset_mock()
                    grant.reset_mock()
                    def check_acl(**_):
                        if not grant.await_count:
                            raise PermissionDeniedError()
                        self.assertEqual(grant.await_count, 4)
                        return [self.dataset]

                    authorize.side_effect = check_acl
                    content = {'question': 'Q', 'answer': 'A', 'context': {}} if kind == 'capture_qa' else 'note'
                    payload = Ingest(scope=Scope(dataset_id=str(self.dataset), user_id=user_id),
                                     kind=kind, content=content, source_meta={'session_id': 'session'})
                    result = await backend.ingest(payload, content)
                    self.assertEqual(result['entry_id'], 'qa' if kind == 'capture_qa' else 'doc')
                    default.assert_awaited_once()
                    row = session.add.call_args.args[0]
                    self.assertIsInstance(row, Dataset)
                    self.assertEqual((row.id, row.name, row.owner_id, row.tenant_id),
                                     (self.dataset, str(self.dataset), owner.id, owner.tenant_id))
                    session.commit.assert_awaited_once()
                    self.assertEqual([c.args for c in grant.await_args_list],
                                     [(owner, self.dataset, p) for p in ('read', 'write', 'delete', 'share')])
                    self.assertEqual(authorize.await_count, 2)
                    downstream = remember if kind == 'capture_qa' else add
                    self.assertEqual(downstream.call_args.kwargs['dataset_id'], self.dataset)
                    self.assertIs(downstream.call_args.kwargs['user'], owner)

    async def test_existing_unauthorized_dataset_is_never_granted(self):
        backend, _, _, _, session, grant = self.dataset_access(existing=object())
        with self.assertRaises(PolicyError) as raised:
            await backend._dataset(self.scope, 'write', allow_provision=True)
        self.assertEqual((raised.exception.code, raised.exception.status), ('dataset_access_required', 403))
        session.get.assert_awaited_once()
        session.add.assert_not_called()
        session.commit.assert_not_awaited()
        grant.assert_not_awaited()

    async def test_non_owner_cannot_provision(self):
        backend, _, _, _, session, grant = self.dataset_access()
        other = SimpleNamespace(id=uuid4())
        scope = Scope(dataset_id=str(self.dataset), user_id=str(other.id))
        with patch('cognee.modules.users.methods.get_user.get_user', return_value=other) as get_user:
            with self.assertRaises(PolicyError) as raised:
                await backend._dataset(scope, 'write', allow_provision=True)
            get_user.assert_awaited_once_with(other.id)
        self.assertEqual((raised.exception.code, raised.exception.status), ('dataset_access_required', 403))
        session.get.assert_not_awaited()
        grant.assert_not_awaited()

    async def test_missing_dataset_is_denied_for_read_and_delete(self):
        backend, _, _, _, session, grant = self.dataset_access()
        for operation, args in (
            (backend.search, (Search(scopes=[self.scope], query='test'), 1)),
            (backend.list_items, (self.scope, 1, 0)),
            (backend.get_item, (self.scope, str(uuid4()))),
            (backend.delete_item, (self.scope, str(uuid4()))),
            (backend.graph, (str(self.dataset), None)),
        ):
            with self.subTest(operation=operation.__name__):
                with self.assertRaises(PolicyError) as raised:
                    await operation(*args)
                self.assertEqual((raised.exception.code, raised.exception.status), ('dataset_not_provisioned', 404))
        self.assertEqual(session.get.await_count, 5)
        grant.assert_not_awaited()

    async def test_existing_dataset_without_permission_returns_403(self):
        backend, _, _, _, session, grant = self.dataset_access(existing=object())
        with self.assertRaises(PolicyError) as raised:
            await backend.search(Search(scopes=[self.scope], query='test'), 1)
        self.assertEqual((raised.exception.code, raised.exception.status), ('dataset_access_required', 403))
        session.get.assert_awaited_once()
        grant.assert_not_awaited()

    async def test_database_not_created_on_read_returns_dataset_not_provisioned(self):
        from cognee.infrastructure.databases.exceptions import DatabaseNotCreatedError

        backend, _, default, _, _, _ = self.dataset_access()
        default.side_effect = DatabaseNotCreatedError()
        with self.assertRaises(PolicyError) as raised:
            await backend.search(Search(scopes=[self.scope], query='test'), 1)
        self.assertEqual((raised.exception.code, raised.exception.status), ('dataset_not_provisioned', 404))

    async def test_ensure_dataset_and_index_code(self):
        import tempfile
        from engines.cognee.engine_api import CodeIndex

        backend, owner, _, _, _, _ = self.dataset_access()
        created_ds = SimpleNamespace(id=self.dataset, name='proj-code', created_at='2026-11-27T00:00:00Z')
        with patch('cognee.modules.data.methods.create_authorized_dataset.create_authorized_dataset',
                   new_callable=AsyncMock, return_value=created_ds) as create_ds:
            res_ds = await backend.ensure_dataset('proj-code')
        self.assertEqual(res_ds, {'id': str(self.dataset), 'name': 'proj-code', 'created_at': '2026-11-27T00:00:00Z'})
        create_ds.assert_awaited_once_with('proj-code', owner)

        for bad_path in ('https://example.com/repo.git', 'git@github.com:org/repo.git', 'relative/path', '/nonexistent/path/xyz'):
            with self.assertRaises(PolicyError) as raised:
                await backend.index_code(CodeIndex(dataset_id=str(self.dataset), repo_path=bad_path))
            self.assertEqual((raised.exception.code, raised.exception.status), ('invalid_repo_path', 422))

        with tempfile.TemporaryDirectory() as tmpdir:
            link_path = os.path.join(tmpdir, 'sym')
            os.symlink(tmpdir, link_path)
            with self.assertRaises(PolicyError) as raised:
                await backend.index_code(CodeIndex(dataset_id=str(self.dataset), repo_path=link_path))
            self.assertEqual((raised.exception.code, raised.exception.status), ('invalid_repo_path', 422))

            with (patch.object(backend, '_dataset', return_value=(owner, self.dataset)),
                  patch('cognee.remember', new_callable=AsyncMock,
                        return_value=SimpleNamespace(status='completed', pipeline_run_id='run-42', items_processed=7)) as remember):
                res = await backend.index_code(CodeIndex(dataset_id=str(self.dataset), repo_path=tmpdir))
            self.assertEqual(res, {'status': 'completed', 'dataset_id': str(self.dataset), 'pipeline_run_id': 'run-42', 'items_processed': 7})
            remember.assert_awaited_once_with(
                [tmpdir], dataset_id=self.dataset, user=owner, content_type='code',
                run_in_background=False, index_vectors=False, self_improvement=False, raise_on_error=False,
            )

    async def test_insert_race_rolls_back_and_rechecks_acl(self):
        from sqlalchemy.exc import IntegrityError

        backend, owner, _, _, session, grant = self.dataset_access(authorization=[[], [self.dataset]])
        session.commit.side_effect = IntegrityError('insert dataset', {}, ValueError('duplicate id'))
        self.assertEqual(await backend._dataset(self.scope, 'write', allow_provision=True), (owner, self.dataset))
        session.rollback.assert_awaited_once()
        grant.assert_not_awaited()

    async def test_search_unwraps_sdk_results_and_resolves_document_ids(self):
        from cognee import SearchType
        from cognee.modules.search.types.SearchResult import SearchResult

        class Chunk(BaseModel):
            is_part_of: dict[str, str] | None = None
            id: str

        parent = uuid4()
        sdk_dataset = uuid4()
        results = [
            SearchResult(dataset_id=self.dataset, dataset_name='project', search_result=[
                {'data_id': 'data', 'document_id': 'other', 'is_part_of': 'parent', 'id': 'chunk'},
                {'document_id': 'doc', 'is_part_of': 'parent', 'id': 'chunk'},
                Chunk(is_part_of={'id': 'nested-parent'}, id='chunk'),
                {'is_part_of': parent, 'id': 'chunk'},
                {'is_part_of': 'string-parent', 'id': 'chunk'},
                {'is_part_of': '', 'id': 'id-only'},
                {'is_part_of': ['invalid-parent']},
                'ignored',
            ]),
            SearchResult(dataset_id=sdk_dataset, dataset_name='project', search_result={'id': 'single-dict'}),
            SearchResult(dataset_id=None, dataset_name=None, search_result=Chunk(id='single-model')),
            SearchResult(dataset_id=None, dataset_name=None, search_result='ignored'),
            {'dataset_id': sdk_dataset, 'search_result': [{'id': 'wrapped-dict'}]},
            {'id': 'plain-dict'},
        ]
        with patch('cognee.search', return_value=results) as search:
            reply = await self.backend.search(Search(scopes=[self.scope], query='match'), limit=20)
        search.assert_awaited_once_with(query_text='match', query_type=SearchType.CHUNKS,
                                        user=self.user, dataset_ids=[self.dataset], top_k=20)
        expected = [
            ('data', self.dataset), ('doc', self.dataset), ('nested-parent', self.dataset),
            (str(parent), self.dataset), ('string-parent', self.dataset), ('id-only', self.dataset),
            ('single-dict', sdk_dataset), ('single-model', self.dataset),
            ('wrapped-dict', sdk_dataset), ('plain-dict', self.dataset),
        ]
        self.assertIsNone(reply['graph_context'])
        self.assertEqual([(hit['document_id'], hit['dataset_id']) for hit in reply['hits']],
                         [(doc, str(dataset)) for doc, dataset in expected])

    async def test_search_filters_sdk_chunks_by_kind(self):
        from cognee.modules.search.types.SearchResult import SearchResult

        result = SearchResult(dataset_id=None, dataset_name=None, search_result=[
            {'data_id': 'source', 'external_metadata': '{"kind":"capture_qa"}', 'text': 'snippet', 'score': 0.3},
            {'data_id': 'document'},
            {'data_id': 'trace', 'kind': 'capture_trace', 'metadata': {'kind': 'capture_qa'}},
        ])
        with patch('cognee.search', return_value=[result]):
            reply = await self.backend.search(
                Search(scopes=[self.scope], query='match', filters=SearchFilters(kind='capture_qa')), limit=10)
        self.assertIsNone(reply['graph_context'])
        self.assertEqual(reply['hits'], [{'dataset_id': str(self.dataset), 'document_id': 'source', 'title': '',
                                          'snippet': 'snippet', 'score': 0.3, 'metadata': {'kind': 'capture_qa'}}])

    async def test_search_chunks_passes_node_sets_and_collects_conflicts(self):
        from cognee import SearchType

        results = [{
            'dataset_id': self.dataset,
            'search_result': [
                {'data_id': 'doc-1', 'text': 'passage', 'score': 0.2,
                 'conflicts': ['Port changed from 8080 to 9090', 'Port changed from 8080 to 9090']},
            ],
        }]
        with patch('cognee.search', return_value=results) as search:
            reply = await self.backend.search(
                Search(scopes=[self.scope], query='port', mode='chunks',
                       filters=SearchFilters(node_sets=['infra', '  '])),
                limit=5,
            )
        search.assert_awaited_once_with(
            query_text='port', query_type=SearchType.CHUNKS,
            user=self.user, dataset_ids=[self.dataset], top_k=5, node_name=['infra'],
        )
        self.assertEqual(len(reply['hits']), 1)
        self.assertEqual(reply['graph_context'], {
            'conflicts': [{'dataset_id': str(self.dataset), 'text': 'Port changed from 8080 to 9090'}],
        })

    async def test_search_hybrid_parses_scored_results_entities_facts_conflicts_and_zero_llm(self):
        from cognee import SearchType
        from cognee.infrastructure.databases.vector.models.ScoredResult import ScoredResult
        from cognee.modules.retrieval.hybrid.external_metadata import project_external_metadata

        chunk_id = uuid4()

        async def fake_hybrid_search(**kwargs):
            chunks = [
                ScoredResult(
                    id=chunk_id,
                    score=0.11,
                    payload={
                        'id': str(chunk_id),
                        'document_id': 'doc-auth',
                        'document_name': 'auth.md',
                        'text': 'JWT verification uses RS256',
                        'external_metadata': '{"kind":"document","lesson_type":"architecture","session_id":"s-1","unlisted":"drop-me"}',
                        'belongs_to_set': ['auth', 'security'],
                        'conflicts': ['Shared conflict', 'Chunk-only conflict'],
                    },
                ),
                {
                    'data_id': 'doc-skipped',
                    'text': 'filtered out',
                    'external_metadata': '{"kind":"capture_trace"}',
                    'score': 0.05,
                    'conflicts': ['Filtered conflict'],
                },
                {
                    'id': 'chunk-ev',
                    'text': 'Matched via evidence doc id',
                    'score': 0.25,
                },
            ]
            cfg = kwargs.get('retriever_specific_config') or {}
            # Run real Cognee 1.6.7 project_external_metadata so tests never mask stripping.
            project_external_metadata(
                chunks,
                bool(cfg.get('include_external_metadata', False)),
                cfg.get('external_metadata_keys'),
            )
            return [{
                'dataset_id': self.dataset,
                'objects_result': {
                    'chunks': chunks,
                    'entities': [
                        {
                            'id': 'ent-1',
                            'name': 'AuthService',
                            'type': 'Service',
                            'edges': [
                                {
                                    'text': 'AuthService validates JWT (authoritative source)',
                                    'source': 'AuthService',
                                    'target': 'JWT',
                                    'relationship': 'validates',
                                    'authoritative': True,
                                },
                                {
                                    'text': 'AuthService uses HS256 [superseded; as of 2025-01-01]',
                                    'source': 'AuthService',
                                    'target': 'HS256',
                                    'relationship': 'uses',
                                    'authoritative': False,
                                },
                            ],
                            'conflicts': ['Shared conflict', 'Entity-only conflict'],
                        }
                    ],
                    'facts': [
                        {'id': 'f-1', 'text': 'Refresh tokens rotate on every use'},
                    ],
                },
                'evidence': [
                    {
                        'kind': 'segment',
                        'artifact_id': 'chunk-ev',
                        'chunk_id': 'chunk-ev',
                        'data_id': 'doc-from-evidence',
                        'document_name': 'ev.md',
                    }
                ],
            }]

        with patch('cognee.search', side_effect=fake_hybrid_search) as search:
            reply = await self.backend.search(
                Search(
                    scopes=[self.scope],
                    query='jwt verification',
                    mode='hybrid',
                    filters=SearchFilters(kind='document', node_sets=['auth'], session_id='sess-1'),
                ),
                limit=8,
            )
        search.assert_awaited_once_with(
            query_text='jwt verification',
            query_type=SearchType.HYBRID_COMPLETION,
            user=self.user,
            dataset_ids=[self.dataset],
            top_k=8,
            node_name=['auth'],
            only_context=True,
            verbose=True,
            include_references=True,
            retriever_specific_config={
                'include_external_metadata': True,
                'external_metadata_keys': [
                    'kind', 'memlane_kind', 'lesson_type', 'state_key',
                    'category', 'common_category', 'session_id', 'session_date', 'node_set',
                ],
            },
        )
        self.assertNotIn('session_id', search.call_args.kwargs)
        self.assertEqual(reply['hits'], [
            {
                'dataset_id': str(self.dataset),
                'document_id': 'doc-auth',
                'title': 'auth.md',
                'snippet': 'JWT verification uses RS256',
                'score': 0.11,
                'metadata': {
                    'kind': 'document',
                    'lesson_type': 'architecture',
                    'session_id': 's-1',
                    'node_set': ['auth', 'security'],
                },
            },
            {
                'dataset_id': str(self.dataset),
                'document_id': 'doc-from-evidence',
                'title': 'ev.md',
                'snippet': 'Matched via evidence doc id',
                'score': 0.25,
                'metadata': {},
            },
        ])
        self.assertEqual(reply['graph_context'], {
            'entities': [{
                'dataset_id': str(self.dataset),
                'name': 'AuthService',
                'type': 'Service',
                'edges': [
                    {
                        'text': 'AuthService validates JWT (authoritative source)',
                        'source': 'AuthService',
                        'target': 'JWT',
                        'relationship': 'validates',
                        'superseded': False,
                        'authoritative': True,
                    },
                    {
                        'text': 'AuthService uses HS256 [superseded; as of 2025-01-01]',
                        'source': 'AuthService',
                        'target': 'HS256',
                        'relationship': 'uses',
                        'superseded': True,
                        'authoritative': False,
                    },
                ],
                'conflicts': ['Shared conflict', 'Entity-only conflict'],
            }],
            'facts': [{'dataset_id': str(self.dataset), 'text': 'Refresh tokens rotate on every use'}],
            'conflicts': [
                {'dataset_id': str(self.dataset), 'text': 'Shared conflict'},
                {'dataset_id': str(self.dataset), 'text': 'Chunk-only conflict'},
                {'dataset_id': str(self.dataset), 'text': 'Entity-only conflict'},
            ],
        })

    async def test_search_hybrid_degrades_to_chunks_on_no_data_error(self):
        from cognee import SearchType
        from cognee.modules.retrieval.exceptions.exceptions import NoDataError

        with patch('cognee.search', side_effect=[
            NoDataError('empty graph'),
            [{'dataset_id': self.dataset, 'search_result': [{'data_id': 'doc-1', 'text': 'chunk fallback', 'score': 0.2}]}],
        ]) as search:
            reply = await self.backend.search(Search(scopes=[self.scope], query='fresh', mode='hybrid'), limit=5)
        self.assertEqual(search.await_count, 2)
        self.assertEqual(search.await_args_list[0].kwargs['query_type'], SearchType.HYBRID_COMPLETION)
        self.assertEqual(search.await_args_list[1].kwargs['query_type'], SearchType.CHUNKS)
        self.assertEqual(reply['hits'][0]['document_id'], 'doc-1')
        self.assertEqual(reply['graph_context'], {'degraded_to_chunks': True})

        with patch('cognee.search', side_effect=[NoDataError('empty graph'), NoDataError('empty chunks')]):
            empty = await self.backend.search(Search(scopes=[self.scope], query='fresh', mode='hybrid'), limit=5)
        self.assertEqual(empty, {'hits': [], 'graph_context': None})

    async def test_search_runs_multiple_scopes_concurrently(self):
        ds1, ds2 = uuid4(), uuid4()
        scope1, scope2 = Scope(dataset_id=str(ds1)), Scope(dataset_id=str(ds2))
        both_started = asyncio.Event()
        in_flight = 0

        async def resolve_dataset(scope, *_args, **_kwargs):
            return self.user, ds1 if scope.dataset_id == str(ds1) else ds2

        async def fake_search(*, dataset_ids, **_kwargs):
            nonlocal in_flight
            in_flight += 1
            if in_flight == 2:
                both_started.set()
            await asyncio.wait_for(both_started.wait(), timeout=1.0)
            ds = dataset_ids[0]
            score = 0.3 if ds == ds1 else 0.1
            return [{
                'dataset_id': ds,
                'objects_result': {
                    'chunks': [{'data_id': f'doc-{ds}', 'text': 'hit', 'score': score, 'conflicts': [f'conflict-{ds}']}],
                    'entities': [],
                    'facts': [{'id': f'f-{ds}', 'text': f'fact-{ds}'}],
                },
                'evidence': [],
            }]

        with (patch.object(self.backend, '_dataset', side_effect=resolve_dataset),
              patch('cognee.search', side_effect=fake_search)):
            reply = await self.backend.search(
                Search(scopes=[scope1, scope2], query='concurrent', mode='hybrid'), limit=5)
        self.assertEqual([hit['dataset_id'] for hit in reply['hits']], [str(ds2), str(ds1)])
        self.assertEqual(reply['graph_context']['facts'], [
            {'dataset_id': str(ds1), 'text': f'fact-{ds1}'},
            {'dataset_id': str(ds2), 'text': f'fact-{ds2}'},
        ])
        self.assertEqual(reply['graph_context']['conflicts'], [
            {'dataset_id': str(ds1), 'text': f'conflict-{ds1}'},
            {'dataset_id': str(ds2), 'text': f'conflict-{ds2}'},
        ])

    async def test_search_graph_mode_filters_document_chunks_reads_conflict_marks_json_and_degrades(self):
        from cognee import SearchType
        from cognee.modules.graph.cognee_graph.CogneeGraphElements import Edge, Node
        from cognee.modules.retrieval.exceptions.exceptions import NoDataError

        chunk_node = Node('chunk-1', {'type': 'DocumentChunk', 'text': 'Gateway uses SQLite WAL.'})
        svc_node = Node('n-svc', {'name': 'Gateway', 'type': 'Service'})
        db_node = Node('n-db', {'name': 'SQLite', 'type': 'Database'})
        edge_entity = Edge(svc_node, db_node, {
            'relationship_name': 'uses',
            'edge_text': 'Gateway uses SQLite',
            'authoritative': True,
            'conflict_marks_json': '[{"status":"superseded"}]',
        })
        edge_chunk = Edge(chunk_node, svc_node, {'relationship_name': 'contains'})
        edge_untyped_contains = Edge(Node('raw-chunk-uuid', {}), svc_node, {'relationship_name': 'contains'})
        verbose_results = [{
            'dataset_id': self.dataset,
            'objects_result': [edge_entity, edge_chunk, edge_untyped_contains],
            'context_result': 'Connections:\n...\n\n## Fact conflicts\n- Older graph claimed Postgres\n',
            'evidence': [
                {'kind': 'graph_edge', 'artifact_id': 'e1'},
                {
                    'kind': 'segment',
                    'artifact_id': 'chunk-1',
                    'chunk_id': 'chunk-1',
                    'data_id': 'doc-gw',
                    'document_name': 'gateway.md',
                    'role': 'supports_assertion',
                },
            ],
        }]
        with patch('cognee.search', return_value=verbose_results) as search:
            reply = await self.backend.search(
                Search(scopes=[self.scope], query='storage', mode='graph',
                       filters=SearchFilters(session_id='sess-ignored')),
                limit=5,
            )
        search.assert_awaited_once_with(
            query_text='storage',
            query_type=SearchType.GRAPH_COMPLETION,
            user=self.user,
            dataset_ids=[self.dataset],
            top_k=5,
            node_name=None,
            only_context=True,
            verbose=True,
            include_references=True,
        )
        self.assertNotIn('session_id', search.call_args.kwargs)
        self.assertEqual(reply['hits'], [{
            'dataset_id': str(self.dataset),
            'document_id': 'doc-gw',
            'title': 'gateway.md',
            'snippet': 'Gateway uses SQLite WAL.',
            'score': None,
            'metadata': {'chunk_id': 'chunk-1', 'role': 'supports_assertion'},
        }])
        self.assertEqual(reply['graph_context']['entities'], [{
            'dataset_id': str(self.dataset),
            'name': 'Gateway',
            'type': 'Service',
            'edges': [{
                'text': 'Gateway uses SQLite',
                'source': 'Gateway',
                'target': 'SQLite',
                'relationship': 'uses',
                'superseded': True,
                'authoritative': True,
            }],
        }])
        self.assertEqual(reply['graph_context']['facts'], [
            {'dataset_id': str(self.dataset), 'text': 'Gateway uses SQLite'},
        ])
        self.assertEqual(reply['graph_context']['conflicts'], [
            {'dataset_id': str(self.dataset), 'text': 'Older graph claimed Postgres'},
        ])

        with patch('cognee.search', side_effect=[
            NoDataError('empty graph'),
            [{'dataset_id': self.dataset, 'search_result': [{'data_id': 'fallback-doc', 'text': 'chunk', 'score': 0.4}]}],
        ]):
            degraded = await self.backend.search(Search(scopes=[self.scope], query='storage', mode='graph'), limit=5)
        self.assertEqual(degraded['hits'][0]['document_id'], 'fallback-doc')
        self.assertEqual(degraded['graph_context'], {'degraded_to_chunks': True})

    async def test_search_code_mode_uses_ask_parses_neighborhood_and_handles_missing_graph(self):
        from cognee import SearchType
        from cognee.modules.retrieval.code_retriever import CodeGraphUnavailableError, CodeSeedNotFoundError
        from cognee.modules.retrieval.exceptions.exceptions import NoDataError

        ask_results = [{
            'dataset_id': self.dataset,
            'search_result': {
                'operation': 'ask',
                'question': 'where are tokens validated?',
                'seeds': [
                    {'id': 'sym-1', 'name': 'AuthService.Verify', 'kind': 'symbol', 'file': 'auth/service.go', 'line': 42},
                ],
                'neighborhood': {
                    'nodes': [
                        {'id': 'sym-1', 'name': 'AuthService.Verify', 'kind': 'symbol', 'file': 'auth/service.go', 'line': 42, 'role': 'seed'},
                        {'id': 'sym-2', 'name': 'jwt.Parse', 'kind': 'symbol', 'file': 'jwt/parse.go', 'line': 10, 'role': 'callee'},
                    ],
                    'edges': [
                        {'source_id': 'sym-1', 'source': 'AuthService.Verify', 'target': 'jwt.Parse', 'type': 'calls'},
                        {'source_id': 'sym-2', 'source': 'jwt.Parse', 'target': 'crypto.Verify', 'type': 'calls'},
                    ],
                },
            },
        }]
        with patch('cognee.search', return_value=ask_results) as search:
            reply = await self.backend.search(
                Search(scopes=[self.scope], query='where are tokens validated?', mode='code'), limit=5)
        search.assert_awaited_once_with(
            query_text='where are tokens validated?',
            query_type=SearchType.CODE,
            user=self.user,
            dataset_ids=[self.dataset],
            top_k=5,
            retriever_specific_config={
                'operation': 'ask',
                'question': 'where are tokens validated?',
                'max_seeds': 5,
                'max_nodes': 20,
            },
        )
        self.assertEqual(reply['hits'], [])
        self.assertEqual(reply['graph_context'], {
            'code_facts': [
                {
                    'dataset_id': str(self.dataset),
                    'name': 'AuthService.Verify',
                    'kind': 'symbol',
                    'file': 'auth/service.go',
                    'line': 42,
                    'relations': [{'type': 'calls', 'target': 'jwt.Parse'}],
                },
                {
                    'dataset_id': str(self.dataset),
                    'name': 'jwt.Parse',
                    'kind': 'symbol',
                    'file': 'jwt/parse.go',
                    'line': 10,
                    'relations': [{'type': 'calls', 'target': 'crypto.Verify'}],
                },
            ],
        })

        for err_or_result in (
            CodeSeedNotFoundError('not found'),
            CodeGraphUnavailableError('no code graph'),
            NoDataError('empty'),
        ):
            with patch('cognee.search', side_effect=err_or_result):
                empty = await self.backend.search(Search(scopes=[self.scope], query='missing', mode='code'), limit=5)
            self.assertEqual(empty, {'hits': [], 'graph_context': None})

    async def test_search_session_context_requires_authorized_scope_read_only_and_fail_open(self):
        user_with_id = SimpleNamespace(id=uuid4())
        sm_available = SimpleNamespace(is_available=True)
        with (patch.object(self.backend, '_dataset', return_value=(user_with_id, self.dataset)),
              patch('cognee.search', return_value=[]) as search,
              patch('cognee.infrastructure.session.get_session_manager.get_session_manager', return_value=sm_available),
              patch('cognee.infrastructure.session.session_context_builder.build_active_context_block',
                    new_callable=AsyncMock, return_value=('## Active session guidance\n- Rule 1', ['e1'])) as build_block):
            reply = await self.backend.search(
                Search(
                    scopes=[self.scope],
                    query='rules',
                    mode='hybrid',
                    filters=SearchFilters(session_id='sess-9', include_session_context=True),
                ),
                limit=5,
            )
        self.assertNotIn('session_id', search.call_args.kwargs)
        build_block.assert_awaited_once_with(
            session_manager=sm_available,
            user_id=str(user_with_id.id),
            session_id='sess-9',
            query='rules',
            stamp_served=False,
        )
        self.assertEqual(reply['graph_context'], {'session_guidance': '## Active session guidance\n- Rule 1'})

        # Empty scopes must NEVER fall back to default user or read session context.
        with (patch('cognee.infrastructure.session.get_session_manager.get_session_manager', return_value=sm_available),
              patch('cognee.infrastructure.session.session_context_builder.build_active_context_block',
                    new_callable=AsyncMock) as build_block,
              patch('cognee.modules.users.methods.get_default_user.get_default_user',
                    new_callable=AsyncMock) as default_user):
            empty_scope_reply = await self.backend.search(
                Search(
                    scopes=[],
                    query='rules',
                    mode='hybrid',
                    filters=SearchFilters(session_id='sess-other', include_session_context=True),
                ),
                limit=5,
            )
        build_block.assert_not_awaited()
        default_user.assert_not_awaited()
        self.assertEqual(empty_scope_reply, {'hits': [], 'graph_context': None})

        # Authorized user lacking id must NOT fall back to default user either.
        with (patch.object(self.backend, '_dataset', return_value=(object(), self.dataset)),
              patch('cognee.search', return_value=[]),
              patch('cognee.infrastructure.session.get_session_manager.get_session_manager', return_value=sm_available),
              patch('cognee.infrastructure.session.session_context_builder.build_active_context_block',
                    new_callable=AsyncMock) as build_block,
              patch('cognee.modules.users.methods.get_default_user.get_default_user',
                    new_callable=AsyncMock) as default_user):
            no_id_reply = await self.backend.search(
                Search(
                    scopes=[self.scope],
                    query='rules',
                    mode='hybrid',
                    filters=SearchFilters(session_id='sess-9', include_session_context=True),
                ),
                limit=5,
            )
        build_block.assert_not_awaited()
        default_user.assert_not_awaited()
        self.assertIsNone(no_id_reply['graph_context'])

        sm_unavailable = SimpleNamespace(is_available=False)
        with (patch.object(self.backend, '_dataset', return_value=(user_with_id, self.dataset)),
              patch('cognee.search', return_value=[]),
              patch('cognee.infrastructure.session.get_session_manager.get_session_manager', return_value=sm_unavailable),
              patch('cognee.infrastructure.session.session_context_builder.build_active_context_block',
                    new_callable=AsyncMock) as build_block):
            reply_unavail = await self.backend.search(
                Search(
                    scopes=[self.scope],
                    query='rules',
                    mode='hybrid',
                    filters=SearchFilters(session_id='sess-9', include_session_context=True),
                ),
                limit=5,
            )
        build_block.assert_not_awaited()
        self.assertIsNone(reply_unavail['graph_context'])

        with (patch.object(self.backend, '_dataset', return_value=(user_with_id, self.dataset)),
              patch('cognee.search', return_value=[]),
              patch('cognee.infrastructure.session.get_session_manager.get_session_manager', return_value=sm_available),
              patch('cognee.infrastructure.session.session_context_builder.build_active_context_block',
                    new_callable=AsyncMock, side_effect=RuntimeError('cache down')),
              self.assertLogs('engines.cognee.engine_api', level='WARNING') as logs):
            reply_err = await self.backend.search(
                Search(
                    scopes=[self.scope],
                    query='rules',
                    mode='hybrid',
                    filters=SearchFilters(session_id='sess-9', include_session_context=True),
                ),
                limit=5,
            )
        self.assertIsNone(reply_err['graph_context'])
        self.assertTrue(any('cache down' in line for line in logs.output))

    async def test_extract_includes_unique_improve_stage_data_ids(self):
        doc, lesson, session = (str(uuid4()) for _ in range(3))
        document_run = SimpleNamespace(data_ids=[doc], pipeline_run_id='cognify')
        stages = [
            SimpleNamespace(run=None),
            SimpleNamespace(run=SimpleNamespace(data_ids=[doc, lesson, lesson], pipeline_run_id='improve')),
            SimpleNamespace(run=SimpleNamespace(pipeline_run_id='finalize')),
        ]
        with (patch.object(self.backend, '_find_data', new_callable=AsyncMock, side_effect=[True, None]),
              patch('cognee.cognify', return_value=document_run),
              patch('cognee.improve', return_value=SimpleNamespace(status='completed', stages=stages))):
            result = await self.backend.extract(Extract(scope=self.scope, doc_ids=[doc, session]))
        self.assertEqual(result, {'touched_doc_ids': [doc, session, lesson], 'pipeline_run_id': 'cognify'})


class EngineAPITests(unittest.IsolatedAsyncioTestCase):
    prefix = '/internal/v1'
    scope = {'dataset_id': 'dataset', 'user_id': 'owner'}
    profile = {'provider': 'openai', 'model': 'openai/example', 'endpoint': 'http://localhost:44321/v1'}

    async def asyncSetUp(self):
        self.enterContext(patch.dict(os.environ))
        self.addCleanup(set_default_language, 'zh-CN')
        self.handoff = ModelHandoffController(factory=dict, background=lambda: ())
        self.backend = Backend()
        app = FastAPI()
        install_engine_api(app, handoff=self.handoff, store_path='/isolated/store', token='local-secret',
                           backend=self.backend)
        self.handoff.install_context_middleware(app, config_context=ContextVar('test_llm_config', default=None))
        app.add_middleware(SetupOnlyMiddleware, is_setup_only=lambda: self.handoff.mode == 'setup_ready')
        self.client = await self.enterAsyncContext(httpx.AsyncClient(
            transport=httpx.ASGITransport(app), base_url=f'http://runner{self.prefix}/',
            headers={'Authorization': 'Bearer local-secret'}))

    async def apply(self, operation_id='activate'):
        response = await self.client.post('model/apply', json={
            'operation_id': operation_id, 'generation_config': self.profile, 'output_language': 'es'})
        self.assertEqual(response.status_code, 200, response.text)
        return response.json()

    async def test_health_auth_and_model_transition(self):
        for authorization, code in (('', 401), ('Bearer', 401), ('Basic local-secret', 401),
                                    ('Bearer wrong', 403)):
            response = await self.client.get('health', headers={'Authorization': authorization})
            self.assertEqual(response.status_code, code)
        response = await self.client.get('health')
        self.assertEqual(response.json(), {
            'ok': True, 'engine': 'cognee', 'version': '1.6.7', 'mode': 'setup_ready',
            'model_digest': '', 'store_path': '/isolated/store',
            'capabilities': {
                'tool_graph': True, 'language_egress': True, 'model_handoff': True,
                'hybrid_recall': True, 'session_context': True, 'code_search': True,
                'search_modes': ['hybrid', 'graph', 'chunks', 'code'],
            }})
        self.assertEqual((await self.client.get('items', params=self.scope)).status_code, 503)
        self.assertEqual((await self.client.get('model/status')).json()['mode'], 'setup_ready')
        applied = await self.apply()
        self.assertEqual(set(applied), {'ok', 'applied', 'model_digest', 'operation_id'})
        self.assertEqual(await self.apply(), applied)
        self.assertEqual((await self.client.get('model/status')).json(), {
            'ok': True, 'mode': 'ready', 'model_digest': applied['model_digest'],
            'output_language': 'es', 'active_tasks': 0})
        self.assertEqual((await self.client.get('health')).json()['mode'], 'ready')
        self.assertEqual((await self.client.get('items', params=self.scope)).status_code, 200)

    async def test_ingest_concurrent_dedup_conflict_and_capture(self):
        await self.apply()
        document = {'kind': 'document', 'content': 'hello', 'scope': self.scope, 'idempotency_key': 'abc'}
        first, retry = await asyncio.gather(*(self.client.post('ingest', json=document) for _ in range(2)))
        for response in (first, retry):
            self.assertEqual(response.status_code, 200)
            self.assertEqual(response.json()['content_hash'], hashlib.sha256(b'hello').hexdigest())
            self.assertEqual(response.json()['document_id'], 'one')
        self.assertEqual(sorted(response.json()['deduplicated'] for response in (first, retry)), [False, True])
        self.assertEqual(len(self.backend.received), 1)
        conflict = await self.client.post('ingest', json={**document, 'content': 'changed'})
        self.assertEqual((conflict.status_code, conflict.json()['detail']), (409, 'idempotency_key_conflict'))

        qa = {'scope': self.scope, 'kind': 'capture_qa', 'content': {'question': 'Q', 'answer': 'A', 'context': 'note'}}
        for source, day in (({'context_date': '2026-11-26'}, '2026-11-26'), ({}, date.today().isoformat())):
            self.assertEqual((await self.client.post('ingest', json={**qa, 'source_meta': source})).status_code, 200)
            self.assertEqual(self.backend.received[-1][1]['context'], {'date': day, 'text': 'note'})

        trace = {'scope': self.scope, 'kind': 'capture_trace', 'idempotency_key': 'trace',
                 'content': {'origin_function': 'check', 'generate_feedback_with_llm': True}}
        self.assertEqual((await self.client.post('ingest', json=trace)).status_code, 422)
        trace['content']['generate_feedback_with_llm'] = False
        self.assertEqual((await self.client.post('ingest', json=trace)).status_code, 200)
        self.assertIn('_memlane_capture', self.backend.received[-1][1]['method_params'])

    async def test_capture_qa_context_date_precedence(self):
        await self.apply()
        qa = {'scope': self.scope, 'kind': 'capture_qa', 'content': {
            'question': 'Q', 'answer': 'A', 'context': {'date': '2026-11-10', 'text': 'note'}}}
        for extra, expected in (({}, '2026-11-10'), ({'source_meta': {'context_date': '2026-11-26'}}, '2026-11-26')):
            with self.subTest(extra=extra):
                response = await self.client.post('ingest', json={**qa, **extra})
                self.assertEqual(response.status_code, 200)
                self.assertEqual(self.backend.received[-1][1]['context'], {'date': expected, 'text': 'note'})

    async def test_digest_and_extract_scoped_language(self):
        applied = await self.apply()
        base = {'scope': self.scope, 'doc_ids': ['one'], 'output_language': 'de'}
        for wrong in ({}, {'expected_model_digest': 'wrong'}, {'expected_model_digest': ''}):
            response = await self.client.post('extract', json={**base, **wrong})
            self.assertEqual((response.status_code, response.json()), (409, {'detail': 'model_digest_mismatch'}))
        for mode in ('default', 'tool_graph'):
            response = await self.client.post('extract', json={**base, 'mode': mode,
                'expected_model_digest': applied['model_digest']})
            self.assertEqual(response.json(), {'ok': True, 'touched_doc_ids': ['one'],
                              'pipeline_run_id': f'{mode}-de', 'model_digest': applied['model_digest']})
        self.assertEqual(current_output_language(), 'es')

    async def test_search_items_pagination_delete_and_graph(self):
        await self.apply()
        response = await self.client.post('search', json={'query': 'needle', 'scopes': [self.scope],
                                          'filters': {'kind': 'document', 'limit': 2}})
        self.assertEqual(response.json(), {'ok': True, 'hits': [{
            'dataset_id': 'dataset', 'document_id': 'one', 'title': 'title', 'snippet': 'needle',
            'score': .1, 'metadata': {'limit': 2, 'kind': 'document'}}]})
        first = (await self.client.get('items', params={**self.scope, 'limit': 2})).json()
        self.assertEqual(first['items'], [{'id': 'one'}, {'id': 'two'}])
        next_page = (await self.client.get('items', params={**self.scope, 'cursor': first['next_cursor']})).json()
        self.assertEqual(next_page, {'ok': True, 'items': [{'id': 'three'}], 'next_cursor': None})
        for params in ({'limit': 101}, {'cursor': 'nope'}):
            self.assertEqual((await self.client.get('items', params={**self.scope, **params})).status_code, 422)
        self.assertEqual((await self.client.get('items/one', params=self.scope)).json(),
                         {'ok': True, 'item': {'id': 'one'}})
        self.assertEqual((await self.client.delete('items/one', params=self.scope)).json(),
                         {'ok': True, 'deleted': True, 'item_id': 'one'})
        self.assertEqual((await self.client.get('items/one', params=self.scope)).status_code, 404)
        self.assertEqual((await self.client.get('graph', params={'dataset_id': 'dataset', 'filter': 'n'})).json(),
                         {'ok': True, 'nodes': [{'id': 'dataset', 'label': 'n'}], 'edges': []})

    async def test_search_route_hybrid_graph_context_and_mode_validation(self):
        await self.apply()
        hybrid_resp = await self.client.post('search', json={
            'query': 'auth',
            'mode': 'hybrid',
            'scopes': [self.scope],
            'filters': {'kind': 'document', 'node_sets': ['auth'], 'session_id': 'sess-7', 'include_session_context': True},
            'limit': 4,
        })
        self.assertEqual(hybrid_resp.status_code, 200)
        self.assertEqual(hybrid_resp.json(), {
            'ok': True,
            'hits': [{
                'dataset_id': 'dataset', 'document_id': 'one', 'title': 'title', 'snippet': 'auth',
                'score': .1, 'metadata': {'limit': 4, 'kind': 'document'},
            }],
            'graph_context': {
                'facts': [{'dataset_id': 'dataset', 'text': 'fact-1'}],
                'session_guidance': 'session:sess-7',
            },
        })
        invalid_mode = await self.client.post('search', json={'query': 'auth', 'mode': 'bogus', 'scopes': [self.scope]})
        self.assertEqual(invalid_mode.status_code, 422)

    async def test_datasets_and_code_index_routes(self):
        await self.apply()
        ds_resp = await self.client.post('datasets', json={'name': 'demo-code'})
        self.assertEqual(ds_resp.status_code, 200)
        self.assertEqual(ds_resp.json(), {'ok': True, 'id': 'ds-ensured', 'name': 'demo-code', 'created_at': '2026-11-27T00:00:00Z'})
        idx_resp = await self.client.post('code-index', json={'dataset_id': 'ds-ensured', 'repo_path': '/tmp/demo'})
        self.assertEqual(idx_resp.status_code, 200)
        self.assertEqual(idx_resp.json(), {'ok': True, 'status': 'completed', 'dataset_id': 'ds-ensured', 'pipeline_run_id': 'run-1', 'items_processed': 3})

    async def test_internal_route_classifies_unhandled_500_without_leaking_secrets(self):
        applied = await self.apply()
        cases = [
            (RuntimeError('Runtime exception: Corrupted wal file. Read out invalid WAL record type. /Users/alice/sk-secret-123'), 'graph_wal_corrupted'),
            (RuntimeError('Buffer manager exception: Maximum database size of 34359738368 bytes has been reached. Reopen with larger max_db_size /Users/alice'), 'graph_db_size_limit'),
            (OSError(24, 'Too many open files: /Users/alice/secret.key'), 'fd_limit_exceeded'),
            (RuntimeError('unexpected boom sk-secret-123 /Users/alice/private'), None),
        ]
        for exc, expected_category in cases:
            with self.subTest(expected_category=expected_category):
                with patch.object(self.backend, 'extract', new_callable=AsyncMock, side_effect=exc):
                    response = await self.client.post('extract', json={
                        'scope': self.scope, 'doc_ids': ['one'], 'expected_model_digest': applied['model_digest'],
                    })
                self.assertEqual(response.status_code, 500)
                body = response.json()
                self.assertEqual(body['detail'], 'engine_internal_error')
                self.assertEqual(body.get('category'), expected_category)
                raw = response.text
                for secret in ('/Users/alice', 'sk-secret-123', 'unexpected boom'):
                    self.assertNotIn(secret, raw)
