import asyncio
from pathlib import Path
import tempfile
import types
import unittest
import uuid
from unittest.mock import patch

from aiohttp.test_utils import TestClient, TestServer
from knowledge_recall import RecallStore
from project_control_server import create_app
from project_policy import PolicyError
import project_plugin_policy as plugin

D, K, K2 = [str(uuid.uuid4()) for _ in range(3)]


def event(*documents, event_id=None):
    return {'event_id': event_id or str(uuid.uuid4()), 'channel': 'automatic',
            'hits': [{'dataset_id': D, 'document_id': key} for key in documents]}


class RecallTests(unittest.TestCase):
    def test_event_identity_pagination_and_dataset_counts_share_original_receipts(self):
        with tempfile.TemporaryDirectory() as folder:
            store = RecallStore(Path(folder)/'recalls.db')
            first, second = event(K), event(K, K2)
            store.record('agent', first); store.record('agent', second)
            rows = store.events({D}, 1, 0)
            self.assertTrue(rows['more'])
            self.assertEqual(rows['items'][0]['event_id'], second['event_id'])
            self.assertEqual(len(rows['items'][0]['hits']), 2)
            self.assertEqual(store.events({D}, 1, 1)['items'][0]['event_id'], first['event_id'])
            self.assertEqual(store.events({str(uuid.uuid4())}, 10, 0)['items'], [])
            self.assertEqual({r['document_id']:r['count'] for r in store.counts(D)['items']}, {K:2,K2:1})
            store.close()
    def test_transaction_dedup_reopen_and_conflict(self):
        with tempfile.TemporaryDirectory() as folder:
            path = Path(folder) / 'knowledge-recall.sqlite3'
            store = RecallStore(path)
            start = store.stats(D, K)
            self.assertEqual(start['count'], 0)
            payload = event(K, K, K2)
            self.assertTrue(store.record('agent', payload)['recorded'])
            self.assertFalse(store.record('agent', event(K2, K, event_id=payload['event_id']))['recorded'])
            with self.assertRaisesRegex(PolicyError, 'recall_event_conflict'):
                store.record('agent', event(K, event_id=payload['event_id']))
            store.record('other', payload)
            self.assertEqual(store.stats(D, K)['count'], 2)
            store.close()
            store = RecallStore(path)
            self.assertEqual(store.stats(D, K)['count'], 2)
            self.assertEqual(store.stats(D, K)['started_at'], start['started_at'])
            self.assertIsNotNone(store.stats(D, K)['last_recalled_at'])
            self.assertEqual(path.stat().st_mode & 0o777, 0o600)
            store.close()

    def test_bad_payload_never_partially_writes(self):
        with tempfile.TemporaryDirectory() as folder:
            store = RecallStore(Path(folder) / 'stats.db')
            bad = event(K)
            bad['hits'].append({'dataset_id': D, 'document_id': 'invalid'})
            with self.assertRaises(PolicyError):
                store.record('agent', bad)
            self.assertEqual(store.db.execute('SELECT COUNT(*) FROM recall_event').fetchone()[0], 0)
            bad = event(K)
            bad['query'] = 'do not store me'
            with self.assertRaises(PolicyError):
                store.record('agent', bad)
            store.close()

    def test_final_delivery_only_and_no_receipt_failure_regression(self):
        def builder(query, cwd, callback):
            plugin.stage_recall(D, [(K, 'shown lesson'), (K2, 'truncated lesson')])
            return 'shown lesson'
        with patch.object(plugin, '_merge_block', builder), patch.object(plugin, 'report_recall') as report:
            self.assertEqual(plugin.merge_block('private query', '.', None), 'shown lesson')
            report.assert_called_once_with([(D, K)], 'automatic')
        with patch.object(plugin, 'managed', return_value=True), patch.object(plugin, '_request', side_effect=OSError('offline')):
            plugin.report_recall([(D, K)], 'automatic')

    def test_explicit_requires_document_reference_not_text_or_graph_id(self):
        rows = [{'dataset_id': D, 'search_result': [{'document_id': K}, {'document_id': K}, {'id': K2}]},
                {'source': 'graph', 'context': 'answer text'}]
        with patch.object(plugin, 'report_recall') as report:
            plugin.report_explicit_recall(rows)
            report.assert_called_once_with({(D, K)}, 'explicit')


class RecallAPITests(unittest.IsolatedAsyncioTestCase):
    async def test_manager_counts_and_events_do_not_recall_or_accept_agent_reads(self):
        manager = {'Authorization': 'Bearer '+'m'*40}
        agent = {'Authorization': 'Bearer '+'a'*40}
        await self.client.post('/v1/knowledge/recalls', json=event(K,K2), headers=agent)
        for path in ('/v1/knowledge/recall-events?limit=1', '/v1/knowledge/recall-counts?dataset_id='+D):
            self.assertEqual((await self.client.get(path, headers=agent)).status, 403)
            response = await self.client.get(path, headers=manager)
            self.assertEqual(response.status, 200)
            self.assertTrue((await response.json())['items'])
        for query in ('limit=0','limit=101','offset=-1','limit=oops','unexpected=1'):
            self.assertEqual((await self.client.get('/v1/knowledge/recall-events?'+query, headers=manager)).status, 400)
    async def asyncSetUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.readable = True
        def policy(group, agent):
            return {'ready': True, 'policy': {'permissions': ['read'] if self.readable else []}}
        self.registry = types.SimpleNamespace(path=Path(self.temporary.name) / 'registry.json',
            state={'agents': {}}, mutex=asyncio.Lock(), managed_groups=lambda: [{'dataset_id': D}], policy=policy)
        self.client = TestClient(TestServer(create_app(self.registry, 'm' * 40, {'agent': 'a' * 40})))
        await self.client.start_server()

    async def asyncTearDown(self):
        await self.client.close()
        self.temporary.cleanup()

    async def test_agent_write_manager_read_and_denials(self):
        url = '/v1/knowledge/recalls'
        headers = {'Authorization': 'Bearer ' + 'a' * 40}
        response = await self.client.post(url, json=event(K, K), headers=headers)
        self.assertEqual(response.status, 200, await response.text())
        query = url + '?dataset_id=' + D + '&document_id=' + K
        response = await self.client.get(query, headers={'Authorization': 'Bearer ' + 'm' * 40})
        self.assertEqual((await response.json())['count'], 1)
        self.assertEqual((await self.client.get(query, headers=headers)).status, 403)
        self.assertEqual((await self.client.post(url, json=event(K))).status, 401)
        self.readable = False
        self.assertEqual((await self.client.post(url, json=event(K), headers=headers)).status, 403)
        self.assertEqual((await self.client.post(url, json=event(K), headers={'Authorization': 'Bearer ' + 'm' * 40})).status, 403)


    async def test_real_hook_http_receipt_to_stats(self):
        token = Path(self.temporary.name) / 'agent.token'
        token.write_text('a' * 40)
        token.chmod(0o600)
        with patch.dict('os.environ', {'MEMLANE_PROJECT_CONTROL_URL': str(self.client.make_url('')).rstrip('/'),
                                     'MEMLANE_PROJECT_CONTROL_TOKEN_FILE': str(token)}):
            await asyncio.to_thread(plugin.report_recall, [(D, K), (D, K)], 'automatic')
        response = await self.client.get('/v1/knowledge/recalls?dataset_id=' + D + '&document_id=' + K,
                                         headers={'Authorization': 'Bearer ' + 'm' * 40})
        self.assertEqual((await response.json())['count'], 1)
