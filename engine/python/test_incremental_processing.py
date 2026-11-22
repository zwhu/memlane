import asyncio
import copy
import json
from pathlib import Path
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import AsyncMock

from incremental_processing import IncrementalProcessing


class IncrementalProcessingTests(unittest.IsolatedAsyncioTestCase):
    async def asyncSetUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.now = 1000.0
        self.allowed = True
        self.calls = []
        self.detail = {'session_id': 'source', 'dataset_id': 'ds', 'user_id': 'actor',
            'msg_count': 1, 'tool_calls': 3, 'last_activity_at': '1970-01-01T00:16:41Z',
            'qas': [{'time': '1970-01-01T00:16:41Z'}], 'traces': []}
        self.snapshot = {'session_id': 'source', 'tokens': [], 'unknown': 20}
        self.result = {'status': 'submitted'}
        self.registry = SimpleNamespace(path=Path(self.temp.name) / 'registry.json', state={
            'sessions': {json.dumps(['a', 'host']): 'p'}, 'projects': {'p': {'dataset_id': 'ds'}}},
            official=SimpleNamespace(request=AsyncMock(side_effect=lambda *a, **k: copy.deepcopy(self.detail))))
        async def execute(row, target, action):
            self.calls.append((action, copy.deepcopy(target)))
            return copy.deepcopy(self.snapshot if action == 'review' else self.result)
        self.backlog = SimpleNamespace(registry=self.registry, jobs={}, runtime={}, execute=execute,
            checked_target=lambda *a, **k: ({'installation': {}, 'principal_id': 'actor', 'key_file': '/original'},
                {'policy': {'permissions': ['read', 'write'], 'can_auto_process': self.allowed}}))
        # Installations are verified by the existing executor in production.
        self.backlog.checked_target = lambda *a, **k: ({'installation': {'verified': True}, 'principal_id': 'actor', 'key_file': '/original'},
                {'policy': {'permissions': ['read', 'write'], 'can_auto_process': self.allowed}})
        self.available = AsyncMock(return_value=True)
        self.processing = IncrementalProcessing(self.backlog, clock=lambda: self.now, available=self.available)

    async def asyncTearDown(self):
        await self.processing.close()
        self.temp.cleanup()

    def submissions(self):
        return [target for action, target in self.calls if action == 'process']

    async def test_confirmed_handoff_respects_original_stop_scope(self):
        import hashlib
        key=hashlib.sha256(json.dumps(['a','host']).encode()).hexdigest()
        cases=[('unrelated',{'targets':[{'agent_id':'a','host':'older'}]},True),
               ('same_source',{'targets':[{'agent_id':'a','host':'host'}]},False),
               ('scope_unknown',{},False),
               ('explicit_pause',{'targets':[{'agent_id':'a','host':'older'}],'automatic_user_paused':True},False),
               ('unknown_submit',{'targets':[{'agent_id':'a','host':'older'}],
                   'pending_processing':{'original':{'agent_id':'a','host':'host','processing':'unknown'}}},False)]
        for name,fields,allowed in cases:
            with self.subTest(name=name):
                self.calls.clear()
                job={'project_id':'p','status':'stopped',**fields}
                self.backlog.jobs={'old':copy.deepcopy(job)}
                self.processing.state['sessions']={key:{'session_id':'source','status':'watching',
                    'counts':[1,3],'changed_at':900,'pending_since':900,'handoff_operation':'confirmed-switch'}}
                await self.processing.tick()
                self.assertEqual(len(self.submissions()),int(allowed))
                self.assertEqual(self.backlog.jobs['old'],job,'original stopped scope or pause was changed')
                if allowed:self.assertEqual(self.submissions()[0]['handoff_operation'],'confirmed-switch')

    async def test_saved_units_start_after_quiet_without_session_end_or_ui(self):
        await self.processing.tick()
        self.assertEqual(self.submissions(), [])
        self.now += 31
        await self.processing.tick()
        self.assertEqual(len(self.submissions()), 1)
        self.assertEqual(self.submissions()[0]['incremental_cooldown_seconds'], 300)
        self.assertEqual(self.submissions()[0]['session_id'], 'source')
        self.now += 600
        await self.processing.tick()
        self.assertEqual(len(self.submissions()), 1, 'unchanged source was resubmitted')
        self.detail['msg_count'] += 1
        await self.processing.tick()
        self.now += 31
        await self.processing.tick()
        self.assertEqual(len(self.submissions()), 2, 'new units never reached next increment')

    async def test_continuous_arrivals_do_not_starve_and_busy_never_forces_admission(self):
        await self.processing.tick()
        self.available.return_value = False
        for _ in range(32):
            self.now += 10
            self.detail['msg_count'] += 1
            await self.processing.tick()
        self.assertEqual(self.submissions(), [])
        self.available.return_value = True
        await self.processing.tick()
        self.assertEqual(len(self.submissions()), 1)

    async def test_multiple_hosts_rotate_and_only_one_job_is_admitted_per_pass(self):
        self.registry.state['sessions'] = {json.dumps(['a', host]): 'p' for host in ('codex', 'pi', 'traex')}
        await self.processing.tick()
        self.now += 31
        await self.processing.tick()
        self.assertEqual(len(self.submissions()), 1)
        await self.processing.tick()
        self.assertEqual(len(self.submissions()), 2)
        await self.processing.tick()
        self.assertEqual({t['host'] for t in self.submissions()}, {'codex', 'pi', 'traex'})
        await self.processing.tick()
        self.assertEqual(len(self.submissions()), 3, 'unchanged units were processed twice')

    async def test_unknown_is_durable_and_new_arrivals_cannot_clear_it(self):
        self.result = {'status': 'unknown'}
        await self.processing.tick(); self.now += 31; await self.processing.tick()
        self.detail['msg_count'] += 1
        self.now += 600
        self.processing = IncrementalProcessing(self.backlog, clock=lambda: self.now, available=self.available)
        await self.processing.tick()
        self.assertEqual(len(self.submissions()), 1)
        self.assertNotIn('question', self.processing.path.read_text())

    async def test_permissions_pause_unsaved_and_wrong_actor_block_dispatch(self):
        await self.processing.tick(); self.now += 31
        self.allowed = False
        await self.processing.tick()
        self.allowed = True
        await self.processing.tick()
        self.assertEqual(self.submissions(), [], 'disabled-period source replayed')
        self.detail['msg_count'] += 1
        self.snapshot['tokens'] = ['unsent']
        await self.processing.tick(); self.now += 31; await self.processing.tick()
        self.assertEqual(self.submissions(), [])
        self.snapshot['tokens'] = []
        self.detail['user_id'] = 'another-actor'
        with self.assertRaises(Exception):
            await self.processing.tick()
        self.assertEqual(self.submissions(), [])

    async def test_installation_does_not_replay_old_saved_history(self):
        self.detail['last_activity_at'] = '1970-01-01T00:01:00Z'
        await self.processing.tick(); self.now += 600; await self.processing.tick()
        self.assertEqual(self.submissions(), [])
        self.detail['msg_count'] += 1
        await self.processing.tick(); self.now += 31; await self.processing.tick()
        self.assertEqual(len(self.submissions()), 1)

    async def test_cancelled_submit_stays_unknown_on_restart(self):
        execute = self.backlog.execute
        async def cancel(row, target, action):
            if action == 'process':
                raise asyncio.CancelledError
            return await execute(row, target, action)
        self.backlog.execute = cancel
        await self.processing.tick(); self.now += 31
        with self.assertRaises(asyncio.CancelledError):
            await self.processing.tick()
        self.assertEqual(next(iter(self.processing.state['sessions'].values()))['status'], 'unknown')

    async def test_partial_tool_turn_never_dispatches_even_after_maximum_wait(self):
        await self.processing.tick()
        self.detail['tool_calls'] += 1
        self.detail['traces'] = [{'time': '1970-01-01T00:17:00Z'}]
        self.now += 600
        await self.processing.tick()
        self.assertEqual(self.submissions(), [])
        self.detail['msg_count'] += 1
        self.detail['qas'] = [{'time': '1970-01-01T00:27:00Z'}]
        await self.processing.tick()
        self.assertEqual(len(self.submissions()), 1)

    async def test_official_cooldown_does_not_hot_loop_submissions(self):
        self.result = {'status': 'deferred', 'reason': 'cooldown'}
        await self.processing.tick(); self.now += 31; await self.processing.tick()
        for _ in range(5):
            self.now += 5
            await self.processing.tick()
        self.assertEqual(len(self.submissions()), 1)
        self.now += 300
        await self.processing.tick()
        self.assertEqual(len(self.submissions()), 2)

    async def test_capacity_checks_original_child_identity_not_only_manager(self):
        self.registry.state['agents'] = {'a': {'key_file': '/original', 'installation': {'verified': True}}}
        async def activity(*args, **kwargs):
            if kwargs.get('token_file'):
                return [{'kind': 'pipeline', 'status': 'DATASET_PROCESSING_STARTED', 'pipeline_run_id': 'child-run'}]
            return []
        self.registry.official.request = AsyncMock(side_effect=activity)
        self.assertFalse(await self.processing.capacity_available())
        self.assertEqual(self.registry.official.request.await_count, 2)

    async def test_pre_submit_policy_unconfirmed_defers_and_restart_records_owner_interrupted(self):
        self.result = {'status': 'paused', 'reason': 'policy_unconfirmed'}
        await self.processing.tick()
        self.now += 31
        await self.processing.tick()
        self.assertEqual(len(self.submissions()), 1)
        entry = next(iter(self.processing.state['sessions'].values()))
        self.assertEqual((entry['status'], entry['reason']), ('deferred', 'policy_unconfirmed'))

        # Backoff delays immediate retry, then allows retry after 30s.
        await self.processing.tick()
        self.assertEqual(len(self.submissions()), 1)
        self.now += 31
        self.result = {'status': 'submitted'}
        await self.processing.tick()
        self.assertEqual(len(self.submissions()), 2)
        self.assertEqual(next(iter(self.processing.state['sessions'].values()))['status'], 'submitted')

        # Restart with in-flight submitting converges to unknown + owner_interrupted.
        entry['status'] = 'submitting'
        entry['reason'] = ''
        self.processing.save()
        reopened = IncrementalProcessing(self.backlog, clock=lambda: self.now, available=self.available)
        reopened_entry = next(iter(reopened.state['sessions'].values()))
        self.assertEqual((reopened_entry['status'], reopened_entry['reason']), ('unknown', 'owner_interrupted'))

    async def test_tick_isolates_per_session_review_and_fetch_errors(self):
        from project_policy import PolicyError
        self.registry.state['sessions'] = {
            json.dumps(['a', 'broken-review']): 'p',
            json.dumps(['a', 'missing-404']): 'p',
            json.dumps(['a', 'healthy']): 'p',
        }
        orig_execute = self.backlog.execute

        async def faulty_execute(row, target, action):
            if target['host'] == 'broken-review' and action == 'review':
                raise PolicyError('review_unavailable', 503)
            snap = await orig_execute(row, target, action)
            if action == 'review':
                snap['session_id'] = 'sid-' + target['host']
            return snap

        async def faulty_official(method, path, **kwargs):
            if 'sid-missing-404' in path:
                raise PolicyError('official_http_404', 503)
            return {**copy.deepcopy(self.detail), 'session_id': 'sid-healthy'}

        self.backlog.execute = faulty_execute
        self.registry.official.request = AsyncMock(side_effect=faulty_official)
        await self.processing.tick()
        self.now += 31
        await self.processing.tick()
        self.assertEqual([t['host'] for t in self.submissions()], ['healthy'])
    async def test_sqlite_capacity_detects_old_active_run_beyond_500_newer_runs(self):
        import sqlite3
        import uuid
        from incremental_processing import _sqlite_capacity_available

        official_data = Path(self.temp.name) / 'official-cap'
        db_dir = official_data / 'system' / 'databases'
        db_dir.mkdir(parents=True)
        cognee_db = db_dir / 'cognee_db'

        con = sqlite3.connect(cognee_db)
        con.execute('''
            CREATE TABLE pipeline_runs (
                id CHAR(32) PRIMARY KEY,
                created_at DATETIME,
                status VARCHAR,
                pipeline_run_id CHAR(32),
                pipeline_name VARCHAR,
                ended_at DATETIME
            )
        ''')
        con.execute('CREATE INDEX ix_pipeline_runs_pipeline_run_id ON pipeline_runs(pipeline_run_id)')
        con.execute('CREATE INDEX ix_pipeline_runs_created_at_id ON pipeline_runs(created_at, id)')

        old_run_id = uuid.uuid4().hex
        con.execute(
            'INSERT INTO pipeline_runs VALUES (?,?,?,?,?,?)',
            (uuid.uuid4().hex, '2026-11-20 00:00:00', 'DATASET_PROCESSING_STARTED', old_run_id, 'cognify_pipeline', None),
        )
        # Insert 600 newer completed runs (1,200 rows) pushing old_run_id well beyond any 500-row window.
        rows = []
        for i in range(600):
            rid = uuid.uuid4().hex
            ts1 = f'2026-11-21 {i // 3600:02d}:{(i // 60) % 60:02d}:{i % 60:02d}.000000'
            ts2 = f'2026-11-21 {i // 3600:02d}:{(i // 60) % 60:02d}:{i % 60:02d}.500000'
            rows.append((uuid.uuid4().hex, ts1, 'DATASET_PROCESSING_STARTED', rid, 'cognify_pipeline', None))
            rows.append((uuid.uuid4().hex, ts2, 'DATASET_PROCESSING_COMPLETED', rid, 'cognify_pipeline', None))
        con.executemany('INSERT INTO pipeline_runs VALUES (?,?,?,?,?,?)', rows)
        con.commit()
        con.close()

        self.assertFalse(_sqlite_capacity_available(official_data))

        # Completing the old run frees capacity.
        con = sqlite3.connect(cognee_db)
        con.execute(
            'INSERT INTO pipeline_runs VALUES (?,?,?,?,?,?)',
            (uuid.uuid4().hex, '2026-11-22 00:00:00', 'DATASET_PROCESSING_COMPLETED', old_run_id, 'cognify_pipeline', None),
        )
        con.commit()
        con.close()
        self.assertTrue(_sqlite_capacity_available(official_data))

        # An active operation row (empty pipeline_name, NULL ended_at) blocks capacity.
        con = sqlite3.connect(cognee_db)
        con.execute(
            'INSERT INTO pipeline_runs VALUES (?,?,?,?,?,?)',
            (uuid.uuid4().hex, '2026-11-22 00:01:00', None, uuid.uuid4().hex, None, None),
        )
        con.commit()
        con.close()
        self.assertFalse(_sqlite_capacity_available(official_data))

    async def test_sqlite_summary_handles_iso_t_expiry_out_of_order_times_and_large_2mb_session(self):
        from datetime import datetime, timedelta, timezone
        import sqlite3
        import uuid
        from incremental_processing import _read_session_incremental_summary_ro, timestamp

        official_data = Path(self.temp.name) / 'official-summary'
        db_dir = official_data / 'system' / 'databases'
        db_dir.mkdir(parents=True)
        cognee_db = db_dir / 'cognee_db'
        cache_db = db_dir / 'cache.db'

        user_uuid = str(uuid.uuid4())
        ds_uuid = str(uuid.uuid4())
        session_id = 'sess-large-oob'

        c1 = sqlite3.connect(cognee_db)
        c1.execute('''
            CREATE TABLE session_records (
                session_id VARCHAR NOT NULL, user_id CHAR(32) NOT NULL, dataset_id CHAR(32),
                status VARCHAR, started_at DATETIME, last_activity_at DATETIME, agent_id VARCHAR,
                PRIMARY KEY (session_id, user_id)
            )
        ''')
        c1.execute(
            'INSERT INTO session_records VALUES (?,?,?,?,?,?,?)',
            (session_id, uuid.UUID(user_uuid).hex, uuid.UUID(ds_uuid).hex, 'active',
             '2026-11-21 08:00:00', '2026-11-21 10:05:00', 'codex'),
        )
        c1.commit()
        c1.close()

        now_utc = datetime.now(timezone.utc)
        # Same-day ISO 'T' timestamp in the past (would falsely compare > space-separated now!).
        expired_t_same_day = (now_utc - timedelta(seconds=5)).strftime('%Y-%m-%dT%H:%M:%S.%f')
        expired_tz_same_day = (now_utc - timedelta(seconds=5)).strftime('%Y-%m-%dT%H:%M:%SZ')
        future_t = (now_utc + timedelta(hours=2)).strftime('%Y-%m-%dT%H:%M:%S.%f')

        c2 = sqlite3.connect(cache_db)
        c2.execute('''
            CREATE TABLE cache_qa_entries (
                seq INTEGER PRIMARY KEY, user_id TEXT NOT NULL, session_id TEXT NOT NULL,
                qa_id TEXT NOT NULL, payload JSON NOT NULL, expires_at DATETIME
            )
        ''')
        c2.execute('CREATE INDEX idx_cache_qa_session ON cache_qa_entries(user_id, session_id, seq)')
        c2.execute('''
            CREATE TABLE cache_trace_entries (
                seq INTEGER PRIMARY KEY, user_id TEXT NOT NULL, session_id TEXT NOT NULL,
                payload JSON NOT NULL, expires_at DATETIME
            )
        ''')
        c2.execute('CREATE INDEX idx_cache_trace_session ON cache_trace_entries(user_id, session_id, seq)')

        # Active QAs with >2 MiB total payload AND out-of-order event timestamps (seq=1 newer than seq=30).
        big_pad = 'x' * 80000
        c2.execute(
            'INSERT INTO cache_qa_entries VALUES (?,?,?,?,?,?)',
            (1, user_uuid, session_id, 'qa-newest-time',
             json.dumps({'time': '2026-11-21T10:15:00Z', 'answer': big_pad}), future_t),
        )
        for idx in range(2, 31):
            c2.execute(
                'INSERT INTO cache_qa_entries VALUES (?,?,?,?,?,?)',
                (idx, user_uuid, session_id, f'qa-{idx}',
                 json.dumps({'time': '2026-11-21T10:01:00Z', 'answer': big_pad}), None),
            )
        # Same-day expired ISO 'T' QA rows must NOT be counted or influence qa_at.
        c2.execute(
            'INSERT INTO cache_qa_entries VALUES (?,?,?,?,?,?)',
            (31, user_uuid, session_id, 'qa-expired-t',
             json.dumps({'time': '2026-11-21T23:59:59Z', 'answer': 'expired'}), expired_t_same_day),
        )
        c2.execute(
            'INSERT INTO cache_qa_entries VALUES (?,?,?,?,?,?)',
            (32, user_uuid, session_id, 'qa-expired-tz',
             json.dumps({'time': '2026-11-21T23:59:59Z', 'answer': 'expired'}), expired_tz_same_day),
        )

        # Traces: seq=1 has newer time than seq=2, plus same-day expired 'T' trace at seq=3.
        c2.execute(
            'INSERT INTO cache_trace_entries VALUES (?,?,?,?,?)',
            (1, user_uuid, session_id, json.dumps({'time': '2026-11-21T10:14:00Z'}), future_t),
        )
        c2.execute(
            'INSERT INTO cache_trace_entries VALUES (?,?,?,?,?)',
            (2, user_uuid, session_id, json.dumps({'time': '2026-11-21T10:02:00Z'}), None),
        )
        c2.execute(
            'INSERT INTO cache_trace_entries VALUES (?,?,?,?,?)',
            (3, user_uuid, session_id, json.dumps({'time': '2026-11-21T23:59:59Z'}), expired_t_same_day),
        )
        c2.commit()

        # Verify single-format equality query has zero TEMP B-TREE.
        eqp = c2.execute(
            """EXPLAIN QUERY PLAN
               SELECT json_extract(payload, '$.time')
               FROM cache_qa_entries
               WHERE user_id = ? AND session_id = ?
                 AND (expires_at IS NULL OR RTRIM(REPLACE(substr(expires_at, 1, 26), 'T', ' '), 'Zz') > ?)""",
            (user_uuid, session_id, now_utc.strftime('%Y-%m-%d %H:%M:%S.%f')),
        ).fetchall()
        plan_text = ' '.join(str(r[-1]) for r in eqp)
        self.assertNotIn('TEMP B-TREE', plan_text)
        c2.close()

        summary = _read_session_incremental_summary_ro(official_data, session_id, user_uuid, ds_uuid)
        self.assertIsNotNone(summary)
        self.assertEqual(summary['msg_count'], 30)
        self.assertEqual(summary['tool_calls'], 2)
        self.assertEqual(summary['qa_at'], timestamp('2026-11-21T10:15:00Z'))
        self.assertEqual(summary['trace_at'], timestamp('2026-11-21T10:14:00Z'))
