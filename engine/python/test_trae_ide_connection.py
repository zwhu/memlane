"""Isolated registration, dispatch, and diagnostic checks for TRAE IDE."""
import asyncio
import json
from pathlib import Path
import sys
import tempfile
import time
from types import SimpleNamespace
import unittest
from unittest.mock import AsyncMock, patch

from agent_connections import NativeConnection
from agent_memory_adapter import MAX_INPUT
from project_policy import PolicyError
import trae_ide_connection as connection


class RegistrationTests(unittest.TestCase):
    def test_legacy_upgrade_preserves_other_hooks_and_verifies_both_generations(self):
        full = connection.loader({'plugin_dir': '/example/new'}, '/example/python')
        self.assertEqual(tuple(full), ('SessionStart', 'UserPromptSubmit', 'PostToolUse',
                                       'PostToolUseFailure', 'Stop', 'SessionEnd'))
        legacy = {event: group for event, group in
                  connection.loader({'plugin_dir': '/example/old'}, '/example/python').items()
                  if event in connection.LEGACY_EVENTS}
        user = {'matcher': '*', 'hooks': [{'command': 'echo test'}]}
        raw = connection.proposal(json.dumps({'version': 1, 'hooks': {'Stop': [user], 'Custom': [user]}}), None, legacy)
        connection.verify_hooks(raw, legacy, Path('/example/old/scripts/trae_ide_hook.py'))
        upgraded = connection.proposal(raw, legacy, full)
        connection.verify_hooks(upgraded, full, Path('/example/new/scripts/trae_ide_hook.py'))
        self.assertEqual(json.loads(upgraded)['hooks'],
                         {**{event: [group] for event, group in full.items()}, 'Stop': [user, full['Stop']],
                          'Custom': [user]})
        for event in full:
            with self.subTest(event=event):
                tampered = json.loads(upgraded)
                tampered['hooks'][event][-1]['hooks'][0]['timeout'] = 10
                with self.assertRaisesRegex(PolicyError, 'trae_ide_hook_changed_requires_review') as caught:
                    connection.verify_hooks(json.dumps(tampered), full, Path('/example/new/scripts/trae_ide_hook.py'))
                self.assertEqual(caught.exception.status, 409)


class DispatchTests(unittest.IsolatedAsyncioTestCase):
    async def asyncSetUp(self):
        self.home = Path(self.enterContext(tempfile.TemporaryDirectory())).resolve()
        self.profile = self.home / 'profile'
        self.profile.mkdir()
        self.registry = SimpleNamespace(path=self.home / 'registry.json',
                                        state={'agents': {}, 'connections': {}, 'sessions': {}})
        self.native = NativeConnection(self.registry, {}, 'trae_ide', self.profile, sys.executable)
        self.identity = self.native.id
        self.installation = {'plugin_dir': str(self.home / 'plugin'), 'home': str(self.home)}
        self.full = connection.loader(self.installation, sys.executable)
        self.registry.state['agents'][self.identity] = {
            'kind': 'trae_ide', 'host_profile': str(self.profile), 'installation': self.installation,
        }
        self.record = self.registry.state['connections'][self.identity] = {
            'status': 'configured_pending_new_session', 'activated': True, 'activated_at': time.time() - 5,
        }
        self.set_hooks(self.full)
        self.dispatch = connection.HookDispatch(self.registry, sys.executable)
        self.diagnostic = self.home / '.cognee-plugin' / ('trae_ide-' + self.identity) / 'last-hook-error.json'
        self.enterContext(patch('agent_connections.verify_hook_artifact'))
        self.stderr = (b'random private traceback locals\n' +
                       b'RuntimeError: worker failed during startup private answer and tool payload\n' +
                       b'Bearer abc-credential\nAuthorization: another-value\nX-Api-Key: "api value"\n' +
                       b'api_key=raw-key\npassword=raw-password\n' +
                       b'token="raw token"\nsecret=raw-secret\n' +
                       b'sk-credential ghp_fakeCredential eyJabc.eyJpayload.signature\n')

    def set_hooks(self, hooks):
        self.record.update(trae_ide_hooks=hooks, trae_ide_next_hooks=hooks)
        self.native.target.write_bytes(connection.proposal(b'', None, hooks))

    def raw(self, event):
        return json.dumps({'hook_event_name': event, 'cwd': str(self.profile), 'workspace_roots': [str(self.profile)],
                           'prompt': 'private prompt'}).encode()

    def assert_diagnostic(self, code):
        entry = self.native.last_hook_error()
        self.assertEqual(entry, self.native.view()['last_hook_error'])
        self.assertEqual(entry['error_code'], code)
        self.assertEqual(entry['event'], 'Stop')
        self.assertEqual(entry['payload_keys'], ['cwd', 'hook_event_name', 'prompt', 'workspace_roots'])
        self.assertIn('worker failed', entry['stderr_snippet'])
        self.assertIn('[redacted]', entry['stderr_snippet'])
        self.assertLessEqual(len(entry['stderr_snippet']), 240)
        for field in ('private', 'credential', 'another-value', 'api value', 'raw-', 'raw token', 'ghp_', 'eyJ'):
            self.assertNotIn(field, self.diagnostic.read_text())
        return entry

    async def test_legacy_generation_dispatches_only_its_registered_events(self):
        legacy = {event: self.full[event] for event in connection.LEGACY_EVENTS}
        self.set_hooks(legacy)
        self.assertEqual(self.native.view()['status'], 'configured_pending_new_session')
        worker = SimpleNamespace(returncode=0, communicate=AsyncMock(return_value=(b'{}', b'')))
        with patch('asyncio.create_subprocess_exec', new_callable=AsyncMock, return_value=worker) as spawn:
            self.assertEqual(await self.dispatch.run(self.identity, 'Stop', self.raw('Stop')), {})
            with self.assertRaisesRegex(PolicyError, 'invalid_trae_ide_hook'):
                await self.dispatch.run(self.identity, 'SessionEnd', self.raw('SessionEnd'))
            spawn.assert_awaited_once()
            self.assertEqual(spawn.await_args.kwargs['stderr'], asyncio.subprocess.PIPE)

    async def test_failed_exit_redacts_diagnostic_with_activation_and_receipt_cutoffs(self):
        worker = SimpleNamespace(returncode=2, communicate=AsyncMock(return_value=(b'', self.stderr)))
        with patch('asyncio.create_subprocess_exec', new_callable=AsyncMock, return_value=worker):
            with self.assertRaisesRegex(PolicyError, 'trae_ide_hook_result_unconfirmed') as caught:
                await self.dispatch.run(self.identity, 'Stop', self.raw('Stop'))
        self.assertEqual(caught.exception.status, 503)
        entry = self.assert_diagnostic('dispatch_exit_nonzero')
        self.assertEqual((entry['status'], entry['error_type']), (2, 'SubprocessError'))
        self.record['activated_at'] = entry['at'] + 1
        self.assertNotIn('last_hook_error', self.native.view())
        self.record['activated_at'] = entry['at'] - 1
        receipt = {'received_at': entry['at'] + 1, 'artifact_verified': True, 'environment_verified': False}
        self.registry.state['connection_receipts'] = {self.identity: receipt}
        self.assertEqual(self.native.last_hook_error(), entry)
        receipt['environment_verified'] = True
        self.assertIsNone(self.native.last_hook_error())
        receipt['received_at'] = entry['at']
        self.assertEqual(self.native.last_hook_error(), entry)

        old_path = self.diagnostic.with_name('saved.json')
        self.diagnostic.rename(old_path)
        self.diagnostic.symlink_to(old_path)
        self.assertIsNone(self.native.last_hook_error())
        self.diagnostic.unlink()
        old_path.rename(self.diagnostic)
        old_directory = self.diagnostic.parent.with_name('saved-directory')
        self.diagnostic.parent.rename(old_directory)
        self.diagnostic.parent.symlink_to(old_directory, target_is_directory=True)
        self.assertIsNone(self.native.last_hook_error())

    async def test_timeout_kills_and_drains_original_communication(self):
        killed = asyncio.Event()

        async def communicate(raw):
            await killed.wait()
            return b'', self.stderr

        def kill():
            worker.returncode = -9
            killed.set()

        worker = SimpleNamespace(returncode=None, communicate=AsyncMock(side_effect=communicate), kill=kill)
        original_wait = asyncio.wait_for

        async def deadline(awaitable, timeout):
            self.assertEqual(timeout, 20)
            return await original_wait(awaitable, timeout=.01)

        with (patch('asyncio.create_subprocess_exec', new_callable=AsyncMock, return_value=worker),
              patch('asyncio.wait_for', side_effect=deadline)):
            with self.assertRaises(asyncio.TimeoutError):
                await self.dispatch.run(self.identity, 'Stop', self.raw('Stop'))
        worker.communicate.assert_awaited_once_with(self.raw('Stop'))
        self.assertTrue(killed.is_set())
        self.assertEqual(self.dispatch.running, 0)
        self.assertEqual(self.assert_diagnostic('dispatch_timeout')['status'], -9)

    async def test_invalid_output_is_reported(self):
        for output in (b'broken', b'[]', b'x' * (MAX_INPUT + 1)):
            with self.subTest(size=len(output)):
                worker = SimpleNamespace(returncode=0, communicate=AsyncMock(return_value=(output, self.stderr)))
                with patch('asyncio.create_subprocess_exec', new_callable=AsyncMock, return_value=worker):
                    with self.assertRaisesRegex(PolicyError, 'trae_ide_hook_result_unconfirmed'):
                        await self.dispatch.run(self.identity, 'Stop', self.raw('Stop'))
                self.assertEqual(self.assert_diagnostic('dispatch_invalid_output')['error_type'], 'ValueError')
