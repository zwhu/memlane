"""Tests for cross-project session drift detection, storage, and HTTP API."""
import json
import os
from pathlib import Path
import sqlite3
import sys
import tempfile
import unittest
import uuid

from aiohttp import web
from aiohttp.test_utils import TestClient, TestServer

sys.path.insert(0, str(Path(__file__).resolve().parent))
from _runtime_common import private_write
from capture_receipts import install_capture_routes
from external_capture_store import ExternalCaptureStore
from project_control import Official, Registry
from project_control_server import create_app
from session_drift import (
    RegistryProjectIndex,
    SessionDriftStore,
    build_project_alias_index,
    extract_trace_project_hits,
    match_project_for_path,
)


def _trace(origin_function, params):
    return {
        'type': 'trace',
        'origin_function': origin_function,
        'method_params': params,
    }


class SessionDriftUnitTests(unittest.TestCase):
    def setUp(self):
        self.tempdir = tempfile.TemporaryDirectory()
        self.root = Path(self.tempdir.name)
        self.item_fe = self.root / 'prod/item-fe'
        self.memlane = self.root / 'AI/memlane'
        self.memlane_sub = self.memlane / 'subrepo'
        self.memlane_settings = self.root / 'AI/memlane-settings'
        for d in (self.item_fe, self.memlane, self.memlane_sub, self.memlane_settings):
            d.mkdir(parents=True)
        self.ds_item = str(uuid.uuid4())
        self.ds_memlane = str(uuid.uuid4())
        self.ds_sub = str(uuid.uuid4())
        self.projects = {
            'proj-item': {
                'id': 'proj-item',
                'name': 'item-fe',
                'dataset_id': self.ds_item,
                'aliases': [str(self.item_fe) + '/'],
            },
            'proj-memlane': {
                'id': 'proj-memlane',
                'name': 'memlane',
                'dataset_id': self.ds_memlane,
                'aliases': [str(self.memlane)],
            },
            'proj-sub': {
                'id': 'proj-sub',
                'name': 'memlane-sub',
                'dataset_id': self.ds_sub,
                'aliases': [str(self.memlane_sub)],
            },
        }
        self.alias_index = build_project_alias_index(self.projects)

    def tearDown(self):
        self.tempdir.cleanup()

    def test_sqlite_wal_pragmas_and_checkpoint(self):
        db_path = self.root / 'session-drift.sqlite3'
        store = SessionDriftStore(db_path)
        self.assertEqual(store.db.execute('PRAGMA journal_mode').fetchone()[0].lower(), 'wal')
        self.assertEqual(store.db.execute('PRAGMA journal_size_limit').fetchone()[0], 4194304)
        self.assertEqual(store.db.execute('PRAGMA wal_autocheckpoint').fetchone()[0], 1000)
        self.assertTrue(store.checkpoint('TRUNCATE'))
        store.close()

    def test_longest_prefix_and_strict_boundary_matching(self):
        self.assertEqual(match_project_for_path(str(self.memlane / 'scripts/dev.py'), self.alias_index), 'proj-memlane')
        self.assertEqual(match_project_for_path(str(self.memlane_sub / 'pkg/main.go'), self.alias_index), 'proj-sub')
        self.assertEqual(
            match_project_for_path(str(self.memlane / 'scripts/../scripts/dev.py'), self.alias_index),
            'proj-memlane',
        )
        # Sibling prefix without directory separator must never match memlane.
        self.assertIsNone(match_project_for_path(str(self.memlane_settings / 'config.json'), self.alias_index))
        self.assertIsNone(match_project_for_path('relative/path.py', self.alias_index))
        self.assertIsNone(match_project_for_path('', self.alias_index))

    def test_multi_host_tool_path_extraction_and_deduplication(self):
        self.assertEqual(
            extract_trace_project_hits(_trace('read_file', {'Path': str(self.memlane / 'a.py')}), self.alias_index),
            {('proj-memlane', 'read')},
        )
        self.assertEqual(
            extract_trace_project_hits(
                _trace('edit_file', {'Path': str(self.memlane / 'a.py'), 'file_path': str(self.memlane / 'b.py')}),
                self.alias_index,
            ),
            {('proj-memlane', 'write')},
        )
        self.assertEqual(
            extract_trace_project_hits(_trace('Edit', {'file_path': str(self.memlane / 'a.py')}), self.alias_index),
            {('proj-memlane', 'write')},
        )
        self.assertEqual(
            extract_trace_project_hits(_trace('write', {'path': str(self.memlane / 'c.py')}), self.alias_index),
            {('proj-memlane', 'write')},
        )
        patch_cmd = (
            f'*** Begin Patch\n'
            f'*** Update File: {self.memlane / "scripts/dev.py"}\n'
            f'*** Add File: {self.memlane / "scripts/new.py"}\n'
            f'*** End Patch'
        )
        self.assertEqual(
            extract_trace_project_hits(_trace('apply_patch', {'command': patch_cmd}), self.alias_index),
            {('proj-memlane', 'write')},
        )
        self.assertEqual(
            extract_trace_project_hits(
                _trace('run_command', {'CommandLine': f'python3 {self.memlane / "scripts/lint.py"} && ls /tmp'}),
                self.alias_index,
            ),
            {('proj-memlane', 'command')},
        )
        self.assertEqual(
            extract_trace_project_hits(
                _trace('Bash', {'command': f'cat "{self.memlane / "README.md"}"'}),
                self.alias_index,
            ),
            {('proj-memlane', 'command')},
        )
        self.assertEqual(
            extract_trace_project_hits({'type': 'qa', 'question': str(self.memlane / 'a.py')}, self.alias_index),
            set(),
        )

    def test_store_permissions_symlink_rejection_idempotency_and_rules(self):
        db_path = self.root / 'session-drift.sqlite3'
        link_path = self.root / 'link-drift.sqlite3'
        link_path.symlink_to(db_path)
        with self.assertRaisesRegex(ValueError, 'symlink'):
            SessionDriftStore(link_path)

        store = SessionDriftStore(db_path)
        try:
            self.assertEqual(os.stat(db_path).st_mode & 0o777, 0o600)

            # Duplicate message_id across gateway + capture_receipts counts only once.
            secret_path = str(self.memlane / 'secret-file-name.py')
            self.assertTrue(store.observe_trace(
                self.ds_item, 'sess-write', _trace('edit_file', {'Path': secret_path}),
                self.alias_index, message_id='msg-1',
            ))
            self.assertFalse(store.observe_trace(
                self.ds_item, 'sess-write', _trace('edit_file', {'Path': secret_path}),
                self.alias_index, message_id='msg-1',
            ))
            # Verify raw file paths or message bodies are not persisted in SQLite.
            with sqlite3.connect(db_path) as raw_conn:
                dump = json.dumps(raw_conn.execute('SELECT * FROM session_project_hits').fetchall())
            self.assertNotIn('secret-file-name.py', dump)

            # Only 1 write is below write_drift threshold (target_total >= 2).
            self.assertEqual(store.evaluate_project_drifts(self.projects)['items'], [])
            self.assertTrue(store.observe_trace(
                self.ds_item, 'sess-write', _trace('read_file', {'Path': str(self.memlane / 'b.py')}),
                self.alias_index, message_id='msg-2',
            ))
            drifts = store.evaluate_project_drifts(self.projects)['items']
            self.assertEqual(len(drifts), 1)
            self.assertEqual(drifts[0]['reason'], 'write_drift')
            self.assertEqual(drifts[0]['target_project_id'], 'proj-memlane')
            self.assertEqual(drifts[0]['target_write_count'], 1)
            self.assertEqual(drifts[0]['target_read_count'], 1)

            # Cross-repo read-only reference must not false-positive when bound project is active.
            for idx in range(3):
                store.observe_trace(
                    self.ds_item, 'sess-ref', _trace('read_file', {'Path': str(self.memlane / f'r{idx}.py')}),
                    self.alias_index, message_id=f'ref-m-{idx}',
                )
            for idx in range(2):
                store.observe_trace(
                    self.ds_item, 'sess-ref', _trace('edit_file', {'Path': str(self.item_fe / f'i{idx}.ts')}),
                    self.alias_index, message_id=f'ref-i-{idx}',
                )

            # Rule 2: exclusive_other_project (4+ hits on other, 0 on bound, read-only).
            for idx in range(4):
                store.observe_trace(
                    self.ds_item, 'sess-excl', _trace('read_file', {'Path': str(self.memlane / f'e{idx}.py')}),
                    self.alias_index, message_id=f'excl-{idx}',
                )

            # Rule 3: dominant_other_project (6+ hits, >=85% share, bound_write == 0).
            store.observe_trace(
                self.ds_item, 'sess-dom', _trace('read_file', {'Path': str(self.item_fe / 'x.ts')}),
                self.alias_index, message_id='dom-bound',
            )
            for idx in range(6):
                store.observe_trace(
                    self.ds_item, 'sess-dom', _trace('run_command', {'CommandLine': f'cat {self.memlane / "d.py"}'}),
                    self.alias_index, message_id=f'dom-other-{idx}',
                )

            reasons = {row['session_id']: row['reason'] for row in store.evaluate_project_drifts(self.projects)['items']}
            self.assertEqual(reasons, {
                'sess-write': 'write_drift',
                'sess-excl': 'exclusive_other_project',
                'sess-dom': 'dominant_other_project',
            })
        finally:
            store.close()

    def test_registry_project_index_refreshes_on_change(self):
        reg_file = self.root / 'project-registry.json'
        reg_file.write_text(json.dumps({'projects': {}}), encoding='utf-8')
        idx = RegistryProjectIndex(reg_file)
        self.assertEqual(idx.alias_index(), [])
        reg_file.write_text(json.dumps({'projects': self.projects}), encoding='utf-8')
        loaded = idx.alias_index()
        self.assertEqual(match_project_for_path(str(self.memlane / 'x.py'), loaded), 'proj-memlane')


class _FakeRegistry(Registry):
    def __init__(self, path, official):
        super().__init__(path, official)


class SessionDriftIntegrationTests(unittest.IsolatedAsyncioTestCase):
    async def asyncSetUp(self):
        self.tempdir = tempfile.TemporaryDirectory()
        self.root = Path(self.tempdir.name)
        self.item_dir = self.root / 'prod/item-fe'
        self.memlane_dir = self.root / 'AI/memlane'
        self.item_dir.mkdir(parents=True)
        self.memlane_dir.mkdir(parents=True)

        self.owner_key = 'owner-secret-key'
        self.agent_key = 'agent-a-secret-key'
        self.manager_token = 'm' * 40
        private_write(self.root / 'owner.key', self.owner_key.encode())
        private_write(self.root / 'agent_a.key', self.agent_key.encode())
        private_write(self.root / 'control_a.tok', b'c' * 40)

        self.ds_item = str(uuid.uuid4())
        self.ds_memlane = str(uuid.uuid4())

        async def upstream_handler(request):
            if request.path == '/api/v1/datasets':
                return web.json_response([
                    {'id': self.ds_item, 'name': 'item-fe'},
                    {'id': self.ds_memlane, 'name': 'memlane'},
                ])
            return web.json_response({'ok': True, 'entry_id': str(uuid.uuid4()), 'status': 'saved'})

        upstream_app = web.Application()
        upstream_app.router.add_route('*', '/{tail:.*}', upstream_handler)
        self.upstream = TestClient(TestServer(upstream_app))
        await self.upstream.start_server()
        upstream_url = f'http://127.0.0.1:{self.upstream.server.port}'

        self.registry = _FakeRegistry(
            self.root / 'registry.json',
            Official(self.upstream.session, upstream_url, str(self.root / 'owner.key')),
        )
        self.registry.state['agents'] = {
            'agent-a': {
                'name': 'Agent A',
                'principal_id': str(uuid.uuid4()),
                'key_file': str(self.root / 'agent_a.key'),
                'control_token_file': str(self.root / 'control_a.tok'),
            },
        }
        self.registry.state['projects'] = {
            'proj-item': {
                'id': 'proj-item',
                'name': 'item-fe',
                'dataset_id': self.ds_item,
                'dataset_name': 'item-ds',
                'aliases': [str(self.item_dir)],
                'overrides': {},
                'desired_revision': 1,
                'status': 'verified',
                'applied': {'agent-a': {'revision': 1, 'status': 'verified'}},
            },
            'proj-memlane': {
                'id': 'proj-memlane',
                'name': 'memlane',
                'dataset_id': self.ds_memlane,
                'dataset_name': 'memlane-ds',
                'aliases': [str(self.memlane_dir)],
                'overrides': {},
                'desired_revision': 1,
                'status': 'verified',
                'applied': {'agent-a': {'revision': 1, 'status': 'verified'}},
            },
        }
        self.registry.save()

        self.app = create_app(self.registry, self.manager_token, {})
        self.client = TestClient(TestServer(self.app))
        await self.client.start_server()

    async def asyncTearDown(self):
        await self.client.close()
        self.registry.close()
        await self.upstream.close()
        self.tempdir.cleanup()

    def _manager_headers(self):
        return {'Authorization': 'Bearer ' + self.manager_token}

    def _agent_control_headers(self):
        return {'Authorization': 'Bearer ' + ('c' * 40)}

    async def test_gateway_capture_endpoints_auth_and_dismiss_flow(self):
        # Auth enforcement: 401 unauthenticated, 403 agent token, 404 unknown project.
        r = await self.client.get('/v1/projects/proj-item/session-drift')
        self.assertEqual(r.status, 401)
        r = await self.client.get('/v1/projects/proj-item/session-drift', headers=self._agent_control_headers())
        self.assertEqual(r.status, 403)
        r = await self.client.get('/v1/projects/missing/session-drift', headers=self._manager_headers())
        self.assertEqual(r.status, 404)

        # Capture trace entries targeting memlane while bound to item-fe.
        msg1 = str(uuid.uuid4())
        r1 = await self.client.put(
            f'/cognee/api/v1/capture/entries/{msg1}',
            headers={'X-Api-Key': self.agent_key},
            json={
                'dataset_id': self.ds_item,
                'session_id': 'sess-cross-1',
                'entry': _trace('edit_file', {'Path': str(self.memlane_dir / 'scripts/dev.py')}),
            },
        )
        self.assertEqual(r1.status, 200)

        r2 = await self.client.post(
            '/cognee/api/v1/remember/entry',
            headers={'X-Api-Key': self.agent_key},
            json={
                'dataset_id': self.ds_item,
                'session_id': 'sess-cross-1',
                'entry': _trace('read_file', {'Path': str(self.memlane_dir / 'engine/python/session_drift.py')}),
            },
        )
        self.assertEqual(r2.status, 200)

        # Query project-scoped and global session-drift endpoints.
        proj_resp = await self.client.get('/v1/projects/proj-item/session-drift', headers=self._manager_headers())
        self.assertEqual(proj_resp.status, 200)
        proj_body = await proj_resp.json()
        self.assertEqual(len(proj_body['items']), 1)
        item = proj_body['items'][0]
        self.assertEqual(item['session_id'], 'sess-cross-1')
        self.assertEqual(item['target_project_id'], 'proj-memlane')
        self.assertEqual(item['reason'], 'write_drift')

        all_resp = await self.client.get('/v1/session-drift', headers=self._manager_headers())
        self.assertEqual(all_resp.status, 200)
        self.assertEqual(len((await all_resp.json())['items']), 1)

        # Invalid dismiss payload rejected with 400.
        bad = await self.client.post(
            '/v1/projects/proj-item/session-drift/dismiss',
            headers=self._manager_headers(),
            json={'session_id': 'sess-cross-1'},
        )
        self.assertEqual(bad.status, 400)

        # Valid dismiss persists and clears item.
        dismiss_resp = await self.client.post(
            '/v1/projects/proj-item/session-drift/dismiss',
            headers=self._manager_headers(),
            json={'session_id': 'sess-cross-1', 'target_project_id': 'proj-memlane'},
        )
        self.assertEqual(dismiss_resp.status, 200)
        self.assertEqual((await dismiss_resp.json())['items'], [])

        proj_after = await self.client.get('/v1/projects/proj-item/session-drift', headers=self._manager_headers())
        self.assertEqual((await proj_after.json())['items'], [])

    async def test_capture_receipts_route_hook_invokes_drift_observer_without_dup(self):
        from types import SimpleNamespace
        from fastapi import FastAPI
        from fastapi.testclient import TestClient as FastAPITestClient

        drift_store = SessionDriftStore(self.root / 'session-drift.sqlite3')
        capture_store = ExternalCaptureStore(self.root / 'capture-receipts.sqlite3')
        try:
            user_id = uuid.uuid4()
            dataset_uuid = uuid.UUID(self.ds_item)

            class FakeSessionManager:
                is_available = True

            class FakeBindings:
                async def get_authenticated_user(self):
                    return SimpleNamespace(id=user_id)

                async def get_permitted_dataset_ids(self, _uid):
                    return [dataset_uuid]

                def get_session_manager(self):
                    return FakeSessionManager()

                async def get_session_row(self, **_kwargs):
                    return None

                async def resolve_entry_dataset(self, **_kwargs):
                    return None

                async def remember(self, entry, **_kwargs):
                    return {'entry_id': 'entry-123', 'status': 'saved'}

            alias_index = build_project_alias_index(self.registry.state['projects'])
            fapp = FastAPI()
            install_capture_routes(
                fapp,
                bindings=FakeBindings(),
                receipt_store=capture_store,
                drift_observer=lambda bds, sid, ent, msg: drift_store.observe_trace(
                    bds, sid, ent, alias_index, message_id=msg,
                ),
            )
            client = FastAPITestClient(fapp)
            msg_a = str(uuid.uuid4())
            msg_b = str(uuid.uuid4())
            resp_a = client.put(
                f'/api/v1/capture/entries/{msg_a}',
                json={
                    'dataset_id': self.ds_item,
                    'session_id': 'sess-direct',
                    'entry': _trace('edit_file', {'Path': str(self.memlane_dir / 'scripts/dev.py')}),
                },
            )
            self.assertEqual(resp_a.status_code, 200)
            # Replaying same message_id must remain idempotent in drift store.
            resp_dup = client.put(
                f'/api/v1/capture/entries/{msg_a}',
                json={
                    'dataset_id': self.ds_item,
                    'session_id': 'sess-direct',
                    'entry': _trace('edit_file', {'Path': str(self.memlane_dir / 'scripts/dev.py')}),
                },
            )
            self.assertEqual(resp_dup.status_code, 200)
            resp_b = client.put(
                f'/api/v1/capture/entries/{msg_b}',
                json={
                    'dataset_id': self.ds_item,
                    'session_id': 'sess-direct',
                    'entry': _trace('read_file', {'Path': str(self.memlane_dir / 'scripts/lint.py')}),
                },
            )
            self.assertEqual(resp_b.status_code, 200)
            drifts = drift_store.evaluate_project_drifts(self.registry.state['projects'])['items']
            self.assertEqual(len(drifts), 1)
            self.assertEqual(drifts[0]['session_id'], 'sess-direct')
            self.assertEqual(drifts[0]['target_write_count'], 1)
            self.assertEqual(drifts[0]['target_read_count'], 1)
        finally:
            capture_store.close()
            drift_store.close()
