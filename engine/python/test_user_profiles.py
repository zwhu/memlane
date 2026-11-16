import asyncio
import json
from pathlib import Path
import tempfile
from types import SimpleNamespace
import unittest

from aiohttp import web
from aiohttp.test_utils import TestClient, TestServer

from project_policy import PolicyError
from user_profiles import UserProfiles, ProfileDocuments, PROMPT, attach_user_profiles, command_for


class Documents:
    def __init__(self):
        self.rows = {}
        self.writes = 0
        self.unknown = False
        self.readable = True

    async def dataset(self):
        return 'daily-official-dataset'

    async def save(self, dataset, body, row):
        self.writes += 1
        identifier = str(self.writes)
        self.rows[row['operation_id']] = (identifier, body)
        if self.unknown:
            raise PolicyError('profile_save_unconfirmed', 503)
        return identifier

    async def find(self, dataset, digest, operation):
        return self.rows[operation][0]

    async def read(self, dataset, document):
        if not self.readable:
            raise PolicyError('profile_document_unavailable', 503)
        return next(body.split('-->\n\n', 1)[1] for identifier, body in self.rows.values() if identifier == document)


class Connections:
    def __init__(self):
        self.rows = [{'instance_id': 'codex-test', 'kind': 'codex', 'status': 'configured_pending_new_session',
            'new_sessions': 1, 'environment_verified': True, 'profile': '/synthetic/codex'}]
        self.adapters = {'codex-test': object()}

    def view(self):
        return {'connections': self.rows}


class UserProfilesTests(unittest.IsolatedAsyncioTestCase):
    async def asyncSetUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.registry = SimpleNamespace(path=Path(self.tmp.name) / 'registry.json', state={}, official=None)
        self.connections, self.documents, self.calls = Connections(), Documents(), 0
        async def runner(adapter, source):
            self.calls += 1
            return '# 用户画像\n\n用户明确要求简洁。未知：职业。'
        self.service = UserProfiles(self.registry, self.connections, self.documents, runner)

    async def asyncTearDown(self):
        await self.service.close()
        self.tmp.cleanup()

    async def finish(self):
        await asyncio.gather(*list(self.service.tasks.values()))

    async def test_auto_once_then_reopen_load_without_inference_or_content_copy(self):
        await self.service.scan()
        await self.finish()
        await self.service.scan()
        self.assertEqual(self.calls, 1)
        first = await self.service.read('codex-test')
        self.assertIn('未知：职业', first['body'])
        self.assertNotIn('用户明确要求简洁', self.service.path.read_text())
        self.assertFalse(list((self.service.path.parent / 'profile-pending').iterdir()))
        reopened = UserProfiles(self.registry, self.connections, self.documents, self.service.runner)
        await reopened.scan()
        self.assertEqual((await reopened.read('codex-test')), first)
        self.assertEqual(self.calls, 1)
        await reopened.close()

    async def test_unverified_connection_does_not_generate(self):
        self.connections.rows[0]['environment_verified'] = False
        await self.service.scan()
        self.assertFalse(self.service.tasks)
        self.assertEqual(self.calls, 0)

    async def test_manual_source_and_same_text_keep_distinct_source_documents(self):
        for name in ('manual:grok', 'manual:chatgpt'):
            await self.service.start(name, name + '-operation', body='# 相同文档')
        await self.finish()
        self.assertEqual(self.calls, 0)
        grok = await self.service.read('manual:grok')
        chatgpt = await self.service.read('manual:chatgpt')
        self.assertEqual(grok['body'], chatgpt['body'])
        self.assertNotEqual(grok['document'], chatgpt['document'])
        bodies = [body for _, body in self.documents.rows.values()]
        self.assertNotEqual(bodies[0], bodies[1])

    async def test_failure_falls_back_to_manual_without_automatic_retry(self):
        async def fail(*args):
            self.calls += 1
            raise PolicyError('profile_agent_failed', 503)
        self.service.runner = fail
        await self.service.scan()
        await self.finish()
        await self.service.scan()
        self.assertEqual(self.calls, 1)
        self.assertEqual(self.service.state['profiles']['codex-test']['status'], 'failed')
        await self.service.start('codex-test', 'manual-result', body='# 手动画像')
        await self.finish()
        self.assertEqual(self.calls, 1)
        self.assertEqual((await self.service.read('codex-test'))['body'], '# 手动画像')

    async def test_unknown_save_reconciles_original_without_replay(self):
        self.documents.unknown = True
        await self.service.start('manual:grok', 'unknown-save', body='# 已保存但回执丢失')
        await self.finish()
        row = self.service.state['profiles']['manual:grok']
        self.assertEqual(row['status'], 'unknown')
        self.assertTrue(Path(row['pending_file']).is_file())
        with self.assertRaises(PolicyError):
            await self.service.start('manual:grok', 'different-operation', body='# 重复')
        await self.service.reconcile('manual:grok')
        self.assertEqual(row['status'], 'ready')
        self.assertEqual(self.documents.writes, 1)

    async def test_same_operation_is_idempotent_and_refresh_keeps_old_document(self):
        await self.service.start('manual:grok', 'first-operation', body='# 原文档')
        await self.finish()
        await self.service.start('manual:grok', 'first-operation', body='# 原文档')
        self.assertEqual(self.documents.writes, 1)
        old = await self.service.read('manual:grok')
        await self.service.start('manual:grok', 'next-operation', body='# 更新文档')
        self.assertEqual(await self.service.read('manual:grok'), old)
        await self.finish()
        self.assertEqual((await self.service.read('manual:grok'))['body'], '# 更新文档')

    async def test_restarts_mark_inflight_unknown_without_retry(self):
        self.service.state['profiles']['codex-test'] = {'status': 'generating', 'operation_id': 'interrupted'}
        self.service.persist()
        reopened = UserProfiles(self.registry, self.connections, self.documents, self.service.runner)
        await reopened.scan()
        self.assertEqual(reopened.state['profiles']['codex-test']['status'], 'unknown')
        self.assertEqual(self.calls, 0)
        await reopened.close()

    async def test_retires_source_withdraws_document(self):
        await self.service.scan()
        await self.finish()
        self.registry.state['agent_retirements'] = {'codex-test': {}}
        with self.assertRaises(PolicyError):
            await self.service.read('codex-test')

    async def test_fixed_cli_uses_original_profile_readonly_and_no_shared_tools(self):
        root = Path(self.tmp.name)
        (root / 'config.toml').write_text('[mcp_servers.shared]\ncommand="must-not-run"\n')
        adapter = SimpleNamespace(executable='/bin/echo')
        args, env, output = command_for(adapter, {'kind': 'codex', 'profile': str(root)}, root)
        self.assertIn('read-only', args)
        self.assertIn('features.hooks=false', args)
        self.assertIn('mcp_servers."shared".enabled=false', args)
        self.assertEqual(env['CODEX_HOME'], str(root))
        self.assertEqual(env['MEMLANE_PROCESSING_SESSION'], '1')
        self.assertEqual(args[-1], PROMPT)
        self.assertIn('Only include rules from stored memories, not from conversations.', PROMPT)
        self.assertIn('Wrap the entire export in a single code block', PROMPT)

    async def test_official_document_identity_uses_actual_camelcase_dto_and_scope(self):
        class Official:
            async def request(self, method, path):
                self.path = path
                return [{'id': '60729eae-264c-46f9-88ce-02680140b9c3', 'datasetId': 'daily',
                         'contentHash': 'hash', 'externalMetadata': {'profile_operation': 'original'}}]
        official = Official()
        documents = ProfileDocuments(official)
        self.assertEqual(await documents.find('daily', 'hash', 'original'), '60729eae-264c-46f9-88ce-02680140b9c3')
        with self.assertRaises(PolicyError):
            await documents.find('other-dataset', 'hash', 'original')

    async def test_http_routes_manager_only_and_large_manual_document(self):
        @web.middleware
        async def auth(request, handler):
            request['manager'] = request.headers.get('Authorization') == 'Bearer manager'
            try:
                return await handler(request)
            except PolicyError as error:
                return web.json_response({'error': error.code}, status=error.status)
        app = web.Application(client_max_size=320 * 1024, middlewares=[auth])
        # Start with no configured adapter, so this tests only the import path.
        service = attach_user_profiles(app, self.registry, None)
        service.documents = self.documents
        client = TestClient(TestServer(app))
        await client.start_server()
        try:
            denied = await client.get('/v1/user-profiles')
            self.assertEqual(denied.status, 403)
            headers = {'Authorization': 'Bearer manager'}
            invalid = await client.post('/v1/user-profiles/manual:grok/import', json={'operation_id': 'test-operation', 'body': None}, headers=headers)
            self.assertEqual(invalid.status, 400)
            result = await client.post('/v1/user-profiles/manual:grok/import', json={'operation_id': 'test-operation', 'body': '# 用户画像\n' + '文' * 12000}, headers=headers)
            self.assertEqual(result.status, 200)
            await asyncio.gather(*list(service.tasks.values()))
            document = await client.get('/v1/user-profiles/manual:grok/document', headers=headers)
            self.assertEqual(document.status, 200)
            self.assertIn('文' * 12000, (await document.json())['body'])
        finally:
            await client.close()


if __name__ == '__main__':
    unittest.main()
