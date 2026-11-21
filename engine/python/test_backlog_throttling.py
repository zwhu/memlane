"""Regression tests for backlog/incremental loop throttling and verification/review caches."""
import ast
import asyncio
import copy
import hashlib
import json
import os
from pathlib import Path
import tempfile
import types
import unittest
from unittest.mock import AsyncMock, patch

from incremental_processing import IncrementalProcessing
import project_backlog
from project_backlog import Backlog, clear_installation_verify_cache, verify_installation
from project_policy import PolicyError


ENGINE_DIR = Path(__file__).resolve().parent


class InstallationVerifyCacheTests(unittest.TestCase):
    def setUp(self):
        clear_installation_verify_cache()
        self.tmp = tempfile.TemporaryDirectory()
        self.root = Path(self.tmp.name).resolve()
        self.plugin_dir = self.root / 'plugin'
        (self.plugin_dir / 'scripts').mkdir(parents=True)
        self.policy_py = self.plugin_dir / 'scripts/_memlane_policy.py'
        self.common_py = self.plugin_dir / 'scripts/_plugin_common.py'
        self.policy_py.write_bytes(b'POLICY = 1\n')
        self.common_py.write_bytes(b'COMMON = 1\n')
        files = {
            'scripts/_memlane_policy.py': hashlib.sha256(self.policy_py.read_bytes()).hexdigest(),
            'scripts/_plugin_common.py': hashlib.sha256(self.common_py.read_bytes()).hexdigest(),
        }
        manifest_raw = json.dumps({'files': files}, sort_keys=True).encode()
        self.manifest_path = self.plugin_dir / '.memlane-project-access.json'
        self.manifest_path.write_bytes(manifest_raw)
        self.installation = {
            'plugin_dir': str(self.plugin_dir),
            'env_file': str(self.root / 'env'),
            'home': str(self.root / 'home'),
            'manifest_sha256': hashlib.sha256(manifest_raw).hexdigest(),
        }

    def tearDown(self):
        clear_installation_verify_cache()
        self.tmp.cleanup()

    def test_verify_installation_caches_by_stat_and_invalidates_immediately_on_change(self):
        real_sha = project_backlog._sha256_file
        hashed = []

        def counting_sha(path, **kwargs):
            hashed.append(Path(path).name)
            return real_sha(path, **kwargs)

        now = [100.0]
        with patch('project_backlog._sha256_file', side_effect=counting_sha):
            verify_installation(self.installation, clock=lambda: now[0])
            self.assertEqual(len(hashed), 2)
            # Second call with unchanged stats hits cache without re-hashing any file.
            verify_installation(self.installation, clock=lambda: now[0] + 10.0)
            self.assertEqual(len(hashed), 2, 'unchanged plugin files were re-hashed on cache hit')

            # Changing expected manifest_sha256 rejects immediately.
            with self.assertRaises(PolicyError) as err:
                verify_installation({**self.installation, 'manifest_sha256': '0' * 64}, clock=lambda: now[0] + 11.0)
            self.assertEqual(err.exception.code, 'backlog_installation_changed')

            # Modifying a plugin file on disk invalidates stat check immediately.
            self.policy_py.write_bytes(b'POLICY = 2  # tampered\n')
            with self.assertRaises(PolicyError) as err:
                verify_installation(self.installation, clock=lambda: now[0] + 12.0)
            self.assertEqual(err.exception.code, 'backlog_installation_changed')

            # Restore file, verify cache warms again, then verify TTL expiry re-hashes.
            self.policy_py.write_bytes(b'POLICY = 1\n')
            before = len(hashed)
            verify_installation(self.installation, clock=lambda: now[0] + 20.0)
            self.assertGreater(len(hashed), before)
            warm = len(hashed)
            verify_installation(self.installation, clock=lambda: now[0] + 25.0)
            self.assertEqual(len(hashed), warm)
            verify_installation(self.installation, clock=lambda: now[0] + 400.0)
            self.assertEqual(len(hashed), warm + 2)

            # Replacing a plugin file with a symlink fails immediately.
            target = self.root / 'other.py'
            target.write_bytes(b'POLICY = 1\n')
            self.policy_py.unlink()
            os.symlink(target, self.policy_py)
            with self.assertRaises(PolicyError) as err:
                verify_installation(self.installation, clock=lambda: now[0] + 405.0)
            self.assertEqual(err.exception.code, 'backlog_installation_changed')


class SharedReviewSnapshotCacheTests(unittest.IsolatedAsyncioTestCase):
    async def asyncSetUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.root = Path(self.tmp.name)
        self.now = 500.0
        self.policy = {
            'ready': True,
            'policy': {'permissions': ['read', 'write'], 'can_capture': True, 'can_auto_process': True},
        }
        self.detail = {
            'session_id': 'native-session',
            'dataset_id': 'ds',
            'user_id': 'actor',
            'msg_count': 1,
            'tool_calls': 0,
            'last_activity_at': '1970-01-01T00:00:01Z',
            'qas': [{'time': '1970-01-01T00:00:01Z'}],
            'traces': [],
        }
        self.registry = types.SimpleNamespace(
            path=self.root / 'registry.json',
            state={
                'agents': {
                    'a': {
                        'installation': {'plugin_dir': '/synthetic', 'env_file': '/synthetic/env', 'home': '/synthetic'},
                        'principal_id': 'actor',
                        'key_file': '/synthetic/key',
                    },
                },
                'projects': {'p': {'id': 'p', 'dataset_id': 'ds'}},
                'sessions': {json.dumps(['a', 'host']): 'p'},
            },
            official=types.SimpleNamespace(request=AsyncMock(side_effect=lambda *a, **k: copy.deepcopy(self.detail))),
            policy=lambda *args: copy.deepcopy(self.policy),
        )
        self.verify_patch = patch('project_backlog.verify_installation')
        self.verify_patch.start()
        self.worker_calls = []
        self.review_gate = None

        async def worker(row, target, action, update):
            self.worker_calls.append((target['host'], action))
            if action == 'review':
                if self.review_gate is not None:
                    await self.review_gate.wait()
                return {'session_id': 'native-session', 'tokens': [], 'unknown': 0, 'signature': 'sig', 'filtered': 0}
            if action == 'check':
                return {'status': 'confirmed', 'unknown': 0, 'matched': 0, 'reason': ''}
            if action == 'process':
                return {'status': 'submitted'}
            result = {'status': 'completed', 'saved': 0, 'handled': 0, 'isolated': 0, 'remaining': 0}
            if update:
                update(result)
            return result

        self.backlog = Backlog(self.registry, {}, worker=worker, review_cache_ttl=25.0, clock=lambda: self.now)
        self.processing = IncrementalProcessing(
            self.backlog, clock=lambda: self.now, available=AsyncMock(return_value=True))

    async def asyncTearDown(self):
        await self.processing.close()
        await self.backlog.close()
        self.verify_patch.stop()
        self.tmp.cleanup()

    async def test_three_loops_share_review_snapshot_cache_until_ttl_or_mutation(self):
        # 1. automatic_check_tick populates cache.
        await self.backlog.automatic_check_tick()
        self.assertEqual(self.worker_calls, [('host', 'review')])

        # 2. IncrementalProcessing.tick and automatic_tick within TTL reuse cached review snapshot.
        self.now += 5.0
        await self.processing.tick()
        await self.backlog.automatic_tick()
        self.assertEqual(self.worker_calls, [('host', 'review')], 'duplicate review subprocesses spawned across loops')

        # 3. force=True bypasses cache immediately.
        row = {'project_id': 'p', 'operation_id': 'op-1'}
        target = {'agent_id': 'a', 'host': 'host', 'dataset': 'ds', 'session_id': 'native-session', 'tokens': []}
        await self.backlog.execute(row, target, 'review', force=True)
        self.assertEqual(self.worker_calls, [('host', 'review'), ('host', 'review')])

        # 4. Invalidation on save, process, and check forces fresh review next time.
        for mutating_action in ('save', 'process', 'check'):
            with self.subTest(action=mutating_action):
                self.worker_calls.clear()
                await self.backlog.execute(row, target, 'review')  # cache hit
                self.assertEqual(self.worker_calls, [])
                await self.backlog.execute(row, target, mutating_action)
                await self.backlog.execute(row, target, 'review')  # cache invalidated -> miss
                self.assertEqual(self.worker_calls, [('host', mutating_action), ('host', 'review')])

        # 5. Advancing clock past TTL expires cached snapshot.
        self.worker_calls.clear()
        self.now += 26.0
        await self.backlog.execute(row, target, 'review')
        self.assertEqual(self.worker_calls, [('host', 'review')])

    async def test_concurrent_review_calls_coalesce_into_single_worker_execution(self):
        self.review_gate = asyncio.Event()
        row = {'project_id': 'p', 'operation_id': 'op-concurrent'}
        target = {'agent_id': 'a', 'host': 'host', 'dataset': 'ds'}
        first = asyncio.create_task(self.backlog.execute(row, target, 'review'))
        second = asyncio.create_task(self.backlog.execute(row, target, 'review'))
        await asyncio.sleep(0)
        self.review_gate.set()
        results = await asyncio.gather(first, second)
        self.assertEqual(results[0], results[1])
        self.assertEqual(self.worker_calls, [('host', 'review')])

    async def test_revoked_policy_is_enforced_even_with_warm_review_cache(self):
        row = {'project_id': 'p', 'operation_id': 'op-policy'}
        target = {'agent_id': 'a', 'host': 'host', 'dataset': 'ds'}
        await self.backlog.execute(row, target, 'review')
        self.policy['ready'] = False
        with self.assertRaises(PolicyError) as err:
            await self.backlog.execute(row, target, 'review')
        self.assertEqual(err.exception.code, 'backlog_write_denied')


class LoopIntervalAndAsyncSubprocessGuardTests(unittest.TestCase):
    def test_background_loops_sleep_at_least_15s_and_no_sync_subprocess_in_async_def(self):
        expected_sleeps = {
            ('project_backlog.py', 'automatic_check_loop'): 20,
            ('project_backlog.py', 'automatic_loop'): 30,
            ('incremental_processing.py', 'loop'): 15,
        }
        seen_sleeps = {}
        for filename in ('project_backlog.py', 'incremental_processing.py', 'opencode_backlog.py'):
            tree = ast.parse((ENGINE_DIR / filename).read_text(encoding='utf-8'), filename=filename)
            for node in ast.walk(tree):
                if isinstance(node, ast.AsyncFunctionDef):
                    for child in ast.walk(node):
                        if isinstance(child, ast.Call) and isinstance(child.func, ast.Attribute):
                            if (isinstance(child.func.value, ast.Name)
                                    and child.func.value.id == 'subprocess'
                                    and child.func.attr in ('run', 'check_output', 'check_call', 'call', 'Popen')):
                                self.fail(f'{filename}:{child.lineno} calls subprocess.{child.func.attr} directly in async def {node.name}')
                    if (filename, node.name) in expected_sleeps:
                        sleeps = []
                        for child in ast.walk(node):
                            if (isinstance(child, ast.Call) and isinstance(child.func, ast.Attribute)
                                    and isinstance(child.func.value, ast.Name)
                                    and child.func.value.id == 'asyncio' and child.func.attr == 'sleep'
                                    and child.args and isinstance(child.args[0], ast.Constant)
                                    and isinstance(child.args[0].value, (int, float))):
                                sleeps.append(child.args[0].value)
                        self.assertTrue(sleeps, f'no asyncio.sleep literal found in {filename}:{node.name}')
                        self.assertGreaterEqual(min(sleeps), 15)
                        seen_sleeps[(filename, node.name)] = min(sleeps)
        self.assertEqual(seen_sleeps, expected_sleeps)


if __name__ == '__main__':
    unittest.main()
