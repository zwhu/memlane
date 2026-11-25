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
        return [{'dataset_id': scope.dataset_id, 'document_id': 'one', 'title': 'title',
                 'snippet': payload.query, 'score': .1, 'metadata': {'limit': limit, 'kind': payload.filters.kind}}
                for scope in payload.scopes]

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
                self.assertEqual((raised.exception.code, raised.exception.status), ('dataset_access_required', 403))
        session.get.assert_not_awaited()
        grant.assert_not_awaited()

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
            hits = await self.backend.search(Search(scopes=[self.scope], query='match'), limit=20)
        search.assert_awaited_once_with(query_text='match', query_type=SearchType.CHUNKS,
                                        user=self.user, dataset_ids=[self.dataset], top_k=20)
        expected = [
            ('data', self.dataset), ('doc', self.dataset), ('nested-parent', self.dataset),
            (str(parent), self.dataset), ('string-parent', self.dataset), ('id-only', self.dataset),
            ('single-dict', sdk_dataset), ('single-model', self.dataset),
            ('wrapped-dict', sdk_dataset), ('plain-dict', self.dataset),
        ]
        self.assertEqual([(hit['document_id'], hit['dataset_id']) for hit in hits],
                         [(doc, str(dataset)) for doc, dataset in expected])

    async def test_search_filters_sdk_chunks_by_kind(self):
        from cognee.modules.search.types.SearchResult import SearchResult

        result = SearchResult(dataset_id=None, dataset_name=None, search_result=[
            {'data_id': 'source', 'external_metadata': '{"kind":"capture_qa"}', 'text': 'snippet', 'score': 0.3},
            {'data_id': 'document'},
            {'data_id': 'trace', 'kind': 'capture_trace', 'metadata': {'kind': 'capture_qa'}},
        ])
        with patch('cognee.search', return_value=[result]):
            hits = await self.backend.search(
                Search(scopes=[self.scope], query='match', filters=SearchFilters(kind='capture_qa')), limit=10)
        self.assertEqual(hits, [{'dataset_id': str(self.dataset), 'document_id': 'source', 'title': '',
                                 'snippet': 'snippet', 'score': 0.3, 'metadata': {'kind': 'capture_qa'}}])

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
            'capabilities': {'tool_graph': True, 'language_egress': True, 'model_handoff': True}})
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
