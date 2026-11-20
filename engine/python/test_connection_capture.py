import asyncio
import copy
import json
from pathlib import Path
import tempfile
import time
from types import SimpleNamespace
import unittest
from unittest.mock import AsyncMock, patch

from connection_capture import ConnectionCapture
from codex_hook_access import CodexHookAccess
from project_policy import PolicyError


class CaptureTests(unittest.IsolatedAsyncioTestCase):
    async def asyncSetUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.root = Path(self.temp.name)
        self.now = time.time()
        self.identifier, self.host = 'installed-agent', 'host-session'
        self.adapter = SimpleNamespace(id=self.identifier, home=self.root, executable=__file__)
        self.row = {'instance_id': self.identifier, 'kind': 'codex', 'profile': str(self.root),
                    'status': 'configured_pending_new_session', 'new_sessions': 30}
        self.record = {'activated_at': self.now - 100}
        self.registry = SimpleNamespace(state={
            'agents': {self.identifier: {'principal_id': 'subject', 'key_file': 'agent-key',
                'host_profile': str(self.root), 'installation': {'manifest_sha256': 'fixed', 'marketplace_name': 'fixed'}}},
            'codex_connections': {self.identifier: self.record},
            'connections': {self.identifier: self.record},
            'connection_receipts': {self.identifier: {'environment_verified': True,
                'received_at': self.now, 'host_session_key': self.host, 'project_id': 'project'}},
            'sessions': {json.dumps([self.identifier, self.host]): 'project'},
            'projects': {'project': {'dataset_id': 'dataset', 'status': 'verified', 'desired_revision': 1,
                'applied': {self.identifier: {'status': 'verified', 'revision': 1}}}}}, official=SimpleNamespace(request=AsyncMock()))
        self.saved = {'session_id': 'codex_host-session', 'user_id': 'subject', 'dataset_id': 'dataset',
                      'qa_count': 1, 'trace_count': 2, 'last_qa_at': self.now - 10}
        self.registry.official.request.return_value = self.saved
        self.access = {'permission': 'granted', 'review_sha': 'review'}
        self.host_calls = 0
        owner = self
        class Host:
            async def __aenter__(self):
                owner.host_calls += 1
                return self
            async def __aexit__(self, *_):
                pass
            async def inspect(self, _):
                await asyncio.sleep(0)
                return copy.deepcopy(owner.access)
            async def allow(self, installation, reviewed):
                if reviewed != owner.access['review_sha']:
                    raise PolicyError('capture_review_changed', 409)
                owner.access['permission'] = 'granted'
        self.factory = lambda *_: Host()
        self.capture = ConnectionCapture(self.registry, {}, host_factory=self.factory)

    async def asyncTearDown(self):
        await self.capture.close()
        self.temp.cleanup()

    async def refresh(self):
        await self.capture.refresh([self.row], {self.identifier: self.adapter})
        return self.capture.view(self.row, self.adapter)

    async def test_installed_bound_and_trusted_still_need_real_saved_message_for_all_three_hosts(self):
        for kind in ('codex', 'pi', 'traex'):
            with self.subTest(kind=kind):
                self.row['kind'] = kind
                self.capture.cache.clear()
                self.registry.official.request.return_value = {**self.saved, 'qa_count': 0, 'last_qa_at': None}
                self.assertEqual((await self.refresh())['state'], 'waiting_message')
                self.capture.cache.clear()
                self.registry.official.request.return_value = self.saved
                self.assertEqual((await self.refresh())['state'], 'healthy')
                self.registry.official.request.assert_awaited_with('GET',
                    '/api/v1/sessions/codex_host-session/capture-status', token_file='agent-key')
        self.assertEqual(self.host_calls, 2, 'pi/TraeX must not use Codex trust inspection')

    async def test_codex_untrusted_or_explicitly_deferred_overrides_old_success(self):
        self.access['permission'] = 'required'
        self.assertEqual((await self.refresh())['state'], 'needs_permission')
        self.registry.official.request.assert_not_awaited()
        self.record['capture_deferred'] = True
        restarted = ConnectionCapture(self.registry, {}, host_factory=self.factory)
        await restarted.refresh([self.row], {self.identifier: self.adapter})
        self.assertEqual(restarted.view(self.row, self.adapter)['state'], 'deferred')
        self.access['permission'] = 'granted'
        restarted.cache.clear()
        await restarted.refresh([self.row], {self.identifier: self.adapter})
        self.assertEqual(restarted.view(self.row, self.adapter)['state'], 'healthy', 'external approval supersedes deferred guide')

    async def test_unverified_or_pre_activation_receipt_never_proves_execution(self):
        receipt = self.registry.state['connection_receipts'][self.identifier]
        for update in ({'environment_verified': False}, {'environment_verified': True, 'received_at': self.now - 200}):
            receipt.update(update)
            self.capture.cache.clear()
            self.assertEqual((await self.refresh())['state'], 'waiting_session')
        self.registry.official.request.assert_not_awaited()

    async def test_old_wrong_identity_wrong_project_and_future_records_never_turn_green(self):
        for update, expected in (({'last_qa_at': self.now - 200}, 'waiting_message'),
                                 ({'last_qa_at': self.now + 3600}, 'waiting_message'),
                                 ({'user_id': 'other'}, 'check_failed'),
                                 ({'dataset_id': 'other'}, 'check_failed'),
                                 ({'session_id': 'other'}, 'check_failed')):
            with self.subTest(update=update):
                self.capture.cache.clear()
                self.registry.official.request.return_value = {**self.saved, **update}
                self.assertEqual((await self.refresh())['state'], expected)

    async def test_read_failure_is_unknown_retains_last_save_and_does_not_replay(self):
        healthy = await self.refresh()
        self.capture.cache[self.identifier]['value']['checked_at'] = 0
        self.registry.official.request.side_effect = PolicyError('official_http_503', 503)
        failed = await self.refresh()
        self.assertEqual(failed['state'], 'check_failed')
        self.assertEqual(failed['last_saved_at'], healthy['last_saved_at'])
        self.assertTrue(all(call.args[0] == 'GET' for call in self.registry.official.request.await_args_list))

    async def test_empty_404_waits_but_permission_error_does_not_claim_no_messages(self):
        for code, expected in (('official_http_404', 'waiting_message'), ('official_http_403', 'check_failed')):
            self.capture.cache.clear()
            self.registry.official.request.side_effect = PolicyError(code, 503)
            self.assertEqual((await self.refresh())['state'], expected)

    async def test_refresh_coalesces_and_generation_change_withdraws_cached_success(self):
        await asyncio.gather(self.refresh(), self.refresh())
        self.assertEqual(self.host_calls, 1)
        self.assertEqual(self.registry.official.request.await_count, 1)
        self.registry.state['agents'][self.identifier]['installation']['manifest_sha256'] = 'changed'
        self.assertEqual(self.capture.view(self.row, self.adapter)['state'], 'checking')

    async def test_profile_change_while_reading_discards_late_result(self):
        async def change(*_, **__):
            self.registry.state['agents'][self.identifier]['host_profile'] = 'other-profile'
            return self.saved
        self.registry.official.request.side_effect = change
        self.assertEqual((await self.refresh())['state'], 'checking')

    async def test_project_capture_revocation_withdraws_old_green_status(self):
        self.assertEqual((await self.refresh())['state'], 'healthy')
        self.registry.state['projects']['project']['overrides'] = {self.identifier: {'capture': False}}
        self.assertEqual(self.capture.view(self.row, self.adapter)['state'], 'checking')
        self.assertEqual((await self.refresh())['state'], 'project_disabled')

    async def test_force_refresh_bypasses_15s_cache(self):
        self.registry.official.request.return_value = {**self.saved, 'qa_count': 0, 'last_qa_at': None}
        self.assertEqual((await self.refresh())['state'], 'waiting_message')
        self.registry.official.request.return_value = self.saved
        await self.capture.refresh([self.row], {self.identifier: self.adapter})
        self.assertEqual(self.capture.view(self.row, self.adapter)['state'], 'waiting_message')
        await self.capture.refresh([self.row], {self.identifier: self.adapter}, force=True)
        self.assertEqual(self.capture.view(self.row, self.adapter)['state'], 'healthy')

    async def test_allow_requires_exact_review_and_clears_deferral_only_after_host_success(self):
        self.access['permission'] = 'required'
        self.record['capture_deferred'] = True
        with self.assertRaises(PolicyError):
            await self.capture.allow(self.adapter, 'obsolete')
        self.assertTrue(self.record['capture_deferred'])
        await self.capture.allow(self.adapter, 'review')
        self.assertFalse(self.record['capture_deferred'])


class HostTrustTests(unittest.IsolatedAsyncioTestCase):
    async def test_allow_uses_host_hashes_scoped_edits_and_version_guard(self):
        host = CodexHookAccess('/unused', '/synthetic-profile')
        hooks = [{'key': 'cognee@fixed:hooks.json:stop:0:0', 'currentHash': 'sha256:'+'a'*64+'/sha256:'+'b'*64}]
        before = {'permission': 'required', 'review_sha': 'review', 'hooks': hooks}
        host.inspect = AsyncMock(side_effect=[before, {**before, 'permission': 'granted'}])
        host.request = AsyncMock(side_effect=[{'layers': [{'name': {'type': 'user'}, 'version': 'exact-config'}]}, {}])
        await host.allow({}, 'review')
        method, payload = host.request.await_args_list[1].args
        self.assertEqual(method, 'config/batchWrite')
        self.assertEqual(payload['expectedVersion'], 'exact-config')
        self.assertEqual(payload['filePath'], '/synthetic-profile/config.toml')
        self.assertEqual(len(payload['edits']), 1)
        self.assertEqual(payload['edits'][0]['value'], {'trusted_hash': 'sha256:'+'a'*64,
                          'trusted_content_hash': 'sha256:'+'b'*64, 'enabled': True})
        self.assertNotIn('bypass', json.dumps(payload))

    async def test_changed_hook_review_never_writes_config(self):
        host = CodexHookAccess('/unused', '/synthetic-profile')
        host.inspect = AsyncMock(return_value={'review_sha': 'new'})
        host.request = AsyncMock(return_value={'layers': []})
        with self.assertRaises(PolicyError):
            await host.allow({}, 'old')
        self.assertEqual(host.request.await_count, 1)

    async def test_hook_source_integrity_is_verified_before_any_trust(self):
        with tempfile.TemporaryDirectory() as directory:
            host = CodexHookAccess('/unused', directory)
            self.assertEqual(host.profile, str(Path(directory).resolve()))
            metadata = {'key': 'cognee@fixed:key', 'currentHash': 'sha256:'+'a'*64+'/sha256:'+'b'*64,
                'enabled': True, 'trustStatus': 'untrusted', 'pluginId': 'cognee@fixed',
                'isManaged': False, 'source': 'plugin',
                'sourcePath': str(Path(directory)/'plugins/cache/fixed/cognee/1.8.0/hooks.json')}
            host.request = AsyncMock(return_value={'data': [{'errors': [], 'hooks': [metadata]}]})
            with patch('project_codex_connection.verify_cache') as verify:
                result = await host.inspect({'marketplace_name': 'fixed'})
                self.assertEqual(result['permission'], 'required')
                verify.assert_called_once()
            metadata['sourcePath'] = '/other/hooks.json'
            with self.assertRaises(PolicyError):
                await host.inspect({'marketplace_name': 'fixed'})
