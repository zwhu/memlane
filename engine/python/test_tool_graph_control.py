import asyncio
from pathlib import Path
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import AsyncMock, patch
import uuid

from project_policy import PolicyError
from tool_graph_control import ToolGraphControl


class ToolGraphControlTests(unittest.IsolatedAsyncioTestCase):
    async def asyncSetUp(self):
        self.directory = tempfile.TemporaryDirectory()
        root = Path(self.directory.name)
        token = root / 'manager-token'
        token.write_text('synthetic-management-secret')
        token.chmod(0o600)
        self.actor = {'key_file': '/original/agent.key', 'principal_id': 'original-user'}
        self.official = SimpleNamespace(request=AsyncMock(side_effect=self.request))
        self.registry = SimpleNamespace(state={'revision': 0, 'service_namespace': str(uuid.uuid4()),
            'projects': {'p': {'id': 'p', 'status': 'verified', 'dataset_id': 'project-dataset'}},
            'agents': {'a': self.actor}}, official=self.official,
            checked_policy=AsyncMock(return_value={'ready': True, 'policy': {'permissions': ['read', 'write', 'delete']}}))
        self.control = ToolGraphControl(self.registry, {'project_control': {
            'registry_file': str(root / 'registry.json'), 'management_token_file': str(token)}})

    async def asyncTearDown(self):
        await self.control.close()
        self.directory.cleanup()

    async def request(self, method, path, body=None, **kwargs):
        if path.startswith('/api/v1/sessions'):
            return {'sessions': [
                {'session_id': 'selected', 'dataset_id': 'project-dataset', 'user_id': 'original-user', 'tool_calls': 4},
                {'session_id': 'other-project', 'dataset_id': 'other', 'user_id': 'original-user', 'tool_calls': 100},
                {'session_id': 'other-user', 'dataset_id': 'project-dataset', 'user_id': 'other-user', 'tool_calls': 100}], 'has_more': False}
        self.assertEqual(kwargs['token_file'], '/original/agent.key')
        self.assertEqual(kwargs['adapter_token'], 'synthetic-management-secret')
        if path.endswith('/preview'):
            return {'limits': {'selected': 7}, 'pending': {'selected': 4}}
        return {'status': 'completed'}

    async def test_preview_and_manual_start_are_bound_to_actor_project_and_reviewed_count(self):
        preview = await self.control.preview('p')
        self.assertEqual(preview['session_count'], 1)
        self.assertEqual(preview['tool_steps'], 4)
        operation = str(uuid.uuid4())
        await self.control.start('p', operation, preview['review_token'])
        await self.control.tasks[operation]
        self.assertEqual(self.control.latest('p')['status'], 'completed')
        calls = [call for call in self.official.request.call_args_list if call.args[0] == 'POST' and call.args[1].endswith('/tool-graphs')]
        self.assertEqual(len(calls), 1)
        self.assertEqual(calls[0].args[2]['limits'], {'selected': 7})
        await self.control.start('p', operation, preview['review_token'])
        self.assertEqual(len([call for call in self.official.request.call_args_list if call.args[0] == 'POST' and call.args[1].endswith('/tool-graphs')]), 1)

    async def test_changed_review_or_revoked_write_does_not_dispatch(self):
        preview = await self.control.preview('p')
        self.registry.state['revision'] += 1
        with self.assertRaises(PolicyError):
            await self.control.start('p', str(uuid.uuid4()), preview['review_token'])
        self.registry.checked_policy.return_value = {'ready': True, 'policy': {'permissions': ['read']}}
        with self.assertRaises(PolicyError):
            await self.control.actor('p', 'a')
        self.assertFalse(any(call.args[0] == 'POST' and call.args[1].endswith('/tool-graphs') for call in self.official.request.call_args_list))

    async def test_uncertain_submission_is_not_repeated_under_a_new_id(self):
        preview = await self.control.preview('p')
        original = self.official.request.side_effect
        async def request(method, path, *args, **kwargs):
            if method == 'POST' and path.endswith('/tool-graphs'):
                raise PolicyError('official_result_unknown', 503)
            return await original(method, path, *args, **kwargs)
        self.official.request.side_effect = request
        operation = str(uuid.uuid4())
        await self.control.start('p', operation, preview['review_token'])
        await self.control.tasks[operation]
        self.assertEqual(self.control.latest('p')['status'], 'result_unknown')
        with self.assertRaises(PolicyError):
            await self.control.start('p', str(uuid.uuid4()), preview['review_token'])

    async def test_automatic_graph_respects_project_date_and_auto_processing_permission(self):
        self.registry.state['tool_graph'] = {'enabled': True, 'changed_at': '2026-11-16T00:00:00+00:00'}
        activity = [
            {'id': 'old', 'kind': 'operation', 'operation_name': 'improve', 'outcome': 'succeeded',
             'dataset_id': 'project-dataset', 'started_at': '2026-11-15T23:00:00+00:00',
             'user_id': 'original-user', 'session_ids': ['selected']},
            {'id': 'new', 'kind': 'operation', 'operation_name': 'improve', 'outcome': 'succeeded',
             'dataset_id': 'project-dataset', 'started_at': '2026-11-16T01:00:00+00:00',
             'user_id': 'original-user', 'session_ids': ['selected']}]
        original = self.official.request.side_effect
        async def request(method, path, *args, **kwargs):
            if '/activity/' in path:
                return activity
            return await original(method, path, *args, **kwargs)
        self.official.request.side_effect = request
        self.control.start = AsyncMock()
        for enabled in (False, True):
            self.registry.checked_policy.return_value = {'ready': True, 'policy': {
                'permissions': ['read', 'write'], 'can_auto_process': enabled}}
            with patch('tool_graph_control.asyncio.sleep', AsyncMock(side_effect=asyncio.CancelledError)):
                with self.assertRaises(asyncio.CancelledError):
                    await self.control.automatic_loop()
            self.assertEqual(self.control.start.await_count, int(enabled))
        call = self.control.start.await_args
        self.assertTrue(call.kwargs['automatic'])
        self.assertEqual(call.kwargs['plan']['plan'][0]['session_id'], 'selected')

    async def test_h6_uncapped_200_sessions_sqlite_prefilter_cold_cache_and_batched_build(self):
        import json
        import sqlite3

        root = Path(self.directory.name)
        official_data = root / 'official-store'
        db_dir = official_data / 'system' / 'databases'
        db_dir.mkdir(parents=True)
        ds_id = str(uuid.uuid4())
        user_id = str(uuid.uuid4())

        cognee_db = db_dir / 'cognee_db'
        cache_db = db_dir / 'cache.db'
        c1 = sqlite3.connect(cognee_db)
        c1.execute('''
            CREATE TABLE session_records (
                session_id VARCHAR NOT NULL, user_id CHAR(32) NOT NULL, dataset_id CHAR(32),
                status VARCHAR, started_at DATETIME, last_activity_at DATETIME, agent_id VARCHAR,
                PRIMARY KEY (session_id, user_id)
            )
        ''')
        # 200 sessions for this dataset & actor (> 128 hard cap!)
        all_sessions = [f'sess-{i:03d}' for i in range(200)]
        c1.executemany(
            'INSERT INTO session_records VALUES (?,?,?,?,?,?,?)',
            [(sid, uuid.UUID(user_id).hex, uuid.UUID(ds_id).hex, 'active', '2026-11-21 08:00:00', '2026-11-21 09:00:00', 'codex') for sid in all_sessions],
        )
        c1.commit()
        c1.close()

        c2 = sqlite3.connect(cache_db)
        c2.execute('''
            CREATE TABLE cache_trace_entries (
                seq INTEGER PRIMARY KEY, user_id TEXT NOT NULL, session_id TEXT NOT NULL,
                payload JSON NOT NULL, expires_at DATETIME
            )
        ''')
        # sess-000..sess-049: 0 traces (skipped by SQLite pre-filter)
        # sess-050..sess-059: 3 traces each, official watermark already at 3 (cold pending=0)
        # sess-060..sess-199 (140 sessions): 4 traces each, official watermark at 1 -> pending=3
        seq = 1
        for i in range(50, 60):
            for _ in range(3):
                c2.execute('INSERT INTO cache_trace_entries VALUES (?,?,?,?,?)', (seq, user_id, f'sess-{i:03d}', '{}', None))
                seq += 1
        for i in range(60, 200):
            for _ in range(4):
                c2.execute('INSERT INTO cache_trace_entries VALUES (?,?,?,?,?)', (seq, user_id, f'sess-{i:03d}', '{}', None))
                seq += 1
        c2.commit()
        c2.close()

        watermarks = {f'sess-{i:03d}': 3 for i in range(50, 60)}
        watermarks.update({f'sess-{i:03d}': 1 for i in range(60, 200)})
        totals = {f'sess-{i:03d}': 3 for i in range(50, 60)}
        totals.update({f'sess-{i:03d}': 4 for i in range(60, 200)})

        preview_batches = []
        build_batches = []

        async def mock_request(method, path, body=None, **kwargs):
            if path.startswith('/api/v1/sessions'):
                raise AssertionError('should pre-filter via read-only SQLite, not HTTP /api/v1/sessions')
            if path.endswith('/preview'):
                sids = list(body['session_ids'])
                self.assertLessEqual(len(sids), 128)
                preview_batches.append(sids)
                limits = {s: totals[s] for s in sids}
                pending = {s: max(0, totals[s] - (0 if watermarks[s] > totals[s] else watermarks[s])) for s in sids}
                return {'limits': limits, 'pending': pending}
            if method == 'POST' and path.endswith('/tool-graphs'):
                sids = list(body['session_ids'])
                self.assertLessEqual(len(sids), 64)
                build_batches.append(sids)
                for s in sids:
                    watermarks[s] = body['limits'][s]
                return {'status': 'completed'}
            return {'status': 'completed'}

        token = root / 'manager-token'
        actor = {'key_file': '/original/agent.key', 'principal_id': user_id}
        official = SimpleNamespace(request=AsyncMock(side_effect=mock_request))
        registry = SimpleNamespace(
            state={
                'revision': 0,
                'service_namespace': str(uuid.uuid4()),
                'projects': {'p-big': {'id': 'p-big', 'status': 'verified', 'dataset_id': ds_id}},
                'agents': {'a': actor},
            },
            official=official,
            checked_policy=AsyncMock(return_value={'ready': True, 'policy': {'permissions': ['read', 'write']}}),
        )
        control = ToolGraphControl(registry, {
            'official_data': str(official_data),
            'project_control': {'registry_file': str(root / 'reg-big.json'), 'management_token_file': str(token)},
        })
        try:
            # First preview: 150 active sessions -> 2 batches (128 + 22); 50 empty sessions never queried
            prev1 = await control.preview('p-big')
            self.assertEqual([len(b) for b in preview_batches], [128, 22])
            self.assertEqual(prev1['session_count'], 140)
            self.assertEqual(prev1['tool_steps'], 140 * 3)

            # Second preview: 10 cold sessions skipped by cold cache -> only 140 queried (128 + 12)
            preview_batches.clear()
            prev2 = await control.preview('p-big')
            self.assertEqual([len(b) for b in preview_batches], [128, 12])
            queried_2 = {s for b in preview_batches for s in b}
            self.assertNotIn('sess-050', queried_2)
            self.assertEqual(prev2['session_count'], 140)

            # Mutate cold session sess-050 (+1 new trace) and sess-051 (-1 trace -> watermark reset!)
            c2 = sqlite3.connect(cache_db)
            c2.execute('INSERT INTO cache_trace_entries VALUES (?,?,?,?,?)', (seq, user_id, 'sess-050', '{}', None))
            c2.execute("DELETE FROM cache_trace_entries WHERE seq = (SELECT MAX(seq) FROM cache_trace_entries WHERE session_id = 'sess-051')")
            c2.commit()
            c2.close()
            totals['sess-050'] = 4
            totals['sess-051'] = 2  # stored(3) > total(2) -> resets watermark to 0 -> pending=2!

            preview_batches.clear()
            prev3 = await control.preview('p-big')
            queried_3 = {s for b in preview_batches for s in b}
            self.assertIn('sess-050', queried_3)
            self.assertIn('sess-051', queried_3)
            self.assertNotIn('sess-052', queried_3)
            self.assertEqual(prev3['session_count'], 142)

            # Start build on 142 sessions -> split into 3 batches (64 + 64 + 14)
            op = str(uuid.uuid4())
            await control.start('p-big', op, prev3['review_token'])
            await control.tasks[op]
            self.assertEqual(control.latest('p-big')['status'], 'completed')
            self.assertEqual([len(b) for b in build_batches], [64, 64, 14])
        finally:
            await control.close()

    async def test_automatic_loop_does_not_rerun_pruned_operations_beyond_200_limit(self):
        self.registry.state['tool_graph'] = {'enabled': True, 'changed_at': '2026-11-16T00:00:00+00:00'}
        ns = uuid.UUID(self.registry.state['service_namespace'])
        # Create 240 completed operations (> _MAX_RETAINED_TERMINAL_JOBS=200).
        activity = []
        for i in range(240):
            act_id = f'improve-{i:04d}'
            op_id = str(uuid.uuid5(ns, 'tool-graph/' + act_id))
            self.control.jobs[op_id] = {
                'operation_id': op_id,
                'project_id': 'p',
                'dataset_id': 'project-dataset',
                'review_token': f'tok-{i}',
                'status': 'completed',
                'automatic': True,
                'created_at': f'2026-11-17T{i // 60:02d}:{i % 60:02d}:00+00:00',
                'session_count': 1,
                'tool_steps': 1,
                'children': [],
            }
            activity.append({
                'id': act_id,
                'kind': 'operation',
                'operation_name': 'improve',
                'outcome': 'succeeded',
                'dataset_id': 'project-dataset',
                'started_at': f'2026-11-17T{i // 60:02d}:{i % 60:02d}:00+00:00',
                'user_id': 'original-user',
                'session_ids': ['selected'],
            })
        self.control.save()
        self.assertEqual(len(self.control.jobs), 200)
        self.assertEqual(len(self.control._seen_operations), 240)

        # Reopen fresh ToolGraphControl from disk to verify seen IDs survive restart.
        reopened = ToolGraphControl(self.registry, self.control.runtime)
        try:
            self.assertEqual(len(reopened.jobs), 200)
            self.assertEqual(len(reopened._seen_operations), 240)
            original = self.official.request.side_effect
            async def request(method, path, *args, **kwargs):
                if '/activity/' in path:
                    return activity
                return await original(method, path, *args, **kwargs)
            self.official.request.side_effect = request
            self.registry.checked_policy.return_value = {
                'ready': True,
                'policy': {'permissions': ['read', 'write'], 'can_auto_process': True},
            }
            reopened.start = AsyncMock()
            with patch('tool_graph_control.asyncio.sleep', AsyncMock(side_effect=asyncio.CancelledError)):
                with self.assertRaises(asyncio.CancelledError):
                    await reopened.automatic_loop()
            self.assertEqual(reopened.start.await_count, 0, 'pruned historical operation re-triggered in automatic_loop')

            # Adding one genuinely new operation #240 triggers exactly once.
            activity.append({
                'id': 'improve-new-241',
                'kind': 'operation',
                'operation_name': 'improve',
                'outcome': 'succeeded',
                'dataset_id': 'project-dataset',
                'started_at': '2026-11-18T00:00:00+00:00',
                'user_id': 'original-user',
                'session_ids': ['selected'],
            })
            with patch('tool_graph_control.asyncio.sleep', AsyncMock(side_effect=asyncio.CancelledError)):
                with self.assertRaises(asyncio.CancelledError):
                    await reopened.automatic_loop()
            self.assertEqual(reopened.start.await_count, 1)
        finally:
            await reopened.close()

    async def test_equal_count_replacement_invalidates_cold_cache_and_second_batch_failure_recovers(self):
        import sqlite3

        root = Path(self.directory.name)
        official_data = root / 'official-equal-replace'
        db_dir = official_data / 'system' / 'databases'
        db_dir.mkdir(parents=True)
        ds_id = str(uuid.uuid4())
        user_id = str(uuid.uuid4())

        cognee_db = db_dir / 'cognee_db'
        cache_db = db_dir / 'cache.db'
        c1 = sqlite3.connect(cognee_db)
        c1.execute('''
            CREATE TABLE session_records (
                session_id VARCHAR NOT NULL, user_id CHAR(32) NOT NULL, dataset_id CHAR(32),
                status VARCHAR, started_at DATETIME, last_activity_at DATETIME, agent_id VARCHAR,
                PRIMARY KEY (session_id, user_id)
            )
        ''')
        all_sessions = ['sess-cold'] + [f'sess-b-{i:03d}' for i in range(70)]
        c1.executemany(
            'INSERT INTO session_records VALUES (?,?,?,?,?,?,?)',
            [(sid, uuid.UUID(user_id).hex, uuid.UUID(ds_id).hex, 'active', '2026-11-21 08:00:00', '2026-11-21 09:00:00', 'codex') for sid in all_sessions],
        )
        c1.commit()
        c1.close()

        c2 = sqlite3.connect(cache_db)
        c2.execute('''
            CREATE TABLE cache_trace_entries (
                seq INTEGER PRIMARY KEY, user_id TEXT NOT NULL, session_id TEXT NOT NULL,
                payload JSON NOT NULL, expires_at DATETIME
            )
        ''')
        c2.execute('CREATE INDEX idx_cache_trace_session ON cache_trace_entries(user_id, session_id, seq)')
        # sess-cold starts with seq 1, 2, 3 (count=3, min=1, max=3) already persisted (pending=0)
        c2.executemany(
            'INSERT INTO cache_trace_entries VALUES (?,?,?,?,?)',
            [(1, user_id, 'sess-cold', '{}', None),
             (2, user_id, 'sess-cold', '{}', None),
             (3, user_id, 'sess-cold', '{}', None)],
        )
        seq = 10
        for i in range(70):
            c2.execute('INSERT INTO cache_trace_entries VALUES (?,?,?,?,?)', (seq, user_id, f'sess-b-{i:03d}', '{}', None))
            seq += 1
        c2.commit()
        c2.close()

        persisted_max = {'sess-cold': 3}
        preview_calls = []
        build_calls = []
        fail_second_batch = [True]

        async def mock_request(method, path, body=None, **kwargs):
            if path.endswith('/preview'):
                sids = list(body['session_ids'])
                preview_calls.append(sids)
                # Read real SQLite window to emulate watermark-aware service
                c = sqlite3.connect(cache_db)
                limits, pending, sigs = {}, {}, {}
                for s in sids:
                    cnt, mn, mx = c.execute(
                        'SELECT COUNT(*), MIN(seq), MAX(seq) FROM cache_trace_entries WHERE user_id = ? AND session_id = ?',
                        (user_id, s),
                    ).fetchone()
                    cnt = int(cnt or 0)
                    already = 0
                    if s in persisted_max:
                        already = int(c.execute(
                            'SELECT COUNT(*) FROM cache_trace_entries WHERE user_id = ? AND session_id = ? AND seq <= ?',
                            (user_id, s, persisted_max[s]),
                        ).fetchone()[0])
                    limits[s] = cnt
                    pending[s] = max(0, cnt - already)
                    if cnt > 0:
                        sigs[s] = {'count': cnt, 'min_seq': int(mn), 'max_seq': int(mx)}
                c.close()
                return {'limits': limits, 'pending': pending, 'signatures': sigs}
            if method == 'POST' and path.endswith('/tool-graphs'):
                build_calls.append(body)
                if len(build_calls) == 2 and fail_second_batch[0]:
                    raise PolicyError('official_tool_graph_failed', 503)
                for s in body['session_ids']:
                    persisted_max[s] = body['signatures'][s]['max_seq']
                return {'status': 'completed'}
            return {'status': 'completed'}

        token = root / 'manager-token'
        actor = {'key_file': '/original/agent.key', 'principal_id': user_id}
        official = SimpleNamespace(request=AsyncMock(side_effect=mock_request))
        registry = SimpleNamespace(
            state={
                'revision': 0,
                'service_namespace': str(uuid.uuid4()),
                'projects': {'p-eq': {'id': 'p-eq', 'status': 'verified', 'dataset_id': ds_id}},
                'agents': {'a': actor},
            },
            official=official,
            checked_policy=AsyncMock(return_value={'ready': True, 'policy': {'permissions': ['read', 'write']}}),
        )
        control = ToolGraphControl(registry, {
            'official_data': str(official_data),
            'project_control': {'registry_file': str(root / 'reg-eq.json'), 'management_token_file': str(token)},
        })
        try:
            prev1 = await control.preview('p-eq')
            self.assertEqual(prev1['session_count'], 70)

            # Second preview skips sess-cold (unchanged signature (3, 3, 1), pending=0).
            preview_calls.clear()
            await control.preview('p-eq')
            self.assertNotIn('sess-cold', {s for b in preview_calls for s in b})

            # Equal-count replacement on sess-cold: delete seq=1, insert seq=4 (count STILL 3, max_seq 3->4!).
            c2 = sqlite3.connect(cache_db)
            c2.execute("DELETE FROM cache_trace_entries WHERE session_id = 'sess-cold' AND seq = 1")
            c2.execute('INSERT INTO cache_trace_entries VALUES (?,?,?,?,?)', (4, user_id, 'sess-cold', '{}', None))
            c2.commit()
            c2.close()

            preview_calls.clear()
            prev3 = await control.preview('p-eq')
            self.assertIn('sess-cold', {s for b in preview_calls for s in b})
            self.assertEqual(prev3['session_count'], 71)
            cold_row = next(r for r in prev3['plan'] if r['session_id'] == 'sess-cold')
            self.assertEqual(cold_row['pending_steps'], 1)
            self.assertEqual(cold_row['signature'], {'count': 3, 'min_seq': 2, 'max_seq': 4})

            # Start build (71 sessions -> batches 64 + 7); second batch fails -> result_unknown.
            op1 = str(uuid.uuid4())
            await control.start('p-eq', op1, prev3['review_token'])
            await control.tasks[op1]
            self.assertEqual(control.latest('p-eq')['status'], 'result_unknown')
            self.assertEqual(len(build_calls), 2)
            # First batch persisted 64 sessions; second confirmation preview shows remaining 7.
            prev4 = await control.preview('p-eq')
            self.assertEqual(prev4['session_count'], 7)
        finally:
            await control.close()
