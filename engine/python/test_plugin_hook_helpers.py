"""Direct unit tests for extracted standalone plugin helper modules."""
import contextlib
import json
from pathlib import Path
import tempfile
import types
import unittest
import uuid

import project_access_hooks as access
import project_host_hooks as host
from project_tool_patch import forget_response, remember_response


class FakeNotReady(RuntimeError):
    pass


def _make_policy(**overrides):
    defaults = {
        'NotReady': FakeNotReady,
        'managed': lambda: True,
        'permit': lambda *a, **k: True,
        'require': lambda *a, **k: None,
        'code_target': lambda *a, **k: 'code-uuid',
        'bind': lambda *a, **k: 'bound-uuid',
        'dataset': lambda: 'dataset-uuid',
        'current': lambda *a, **k: {
            'revision': 1,
            'dataset_id': 'proj-uuid',
            'policy': {'permissions': ['read', 'write', 'delete'], 'can_capture': True},
            'common': {'ready': True, 'readable': True, 'dataset_id': 'common-uuid'},
        },
        'recall_targets': lambda ds, ids: (['proj-uuid'], 'proj-uuid'),
        'capture_boundary': lambda *a, **k: 0,
        'stage_recall': lambda *a, **k: None,
        'report_explicit_recall': lambda *a, **k: None,
        'block': lambda *a, **k: None,
        'stamp_pending_entry': lambda *a, **k: None,
        'begin_capture_boundary': lambda *a, **k: None,
    }
    defaults.update(overrides)
    return types.SimpleNamespace(**defaults)


class ProjectAccessHooksTests(unittest.TestCase):
    def test_code_graph_gates_enforce_policy_and_override_credentials(self):
        err = lambda code, msg: {'status': code, 'error': msg}
        loaded = []
        policy = _make_policy(code_target=lambda op, target=None, **kw: 'code-ds')
        ds, res = access.gate_code_index(lambda: loaded.append(1), 'slug', '/repo', error_fn=err, policy_mod=policy)
        self.assertEqual((ds, res, len(loaded)), ('code-ds', None, 1))

        env = {'COGNEE_BASE_URL': 'http://127.0.0.1:18999', 'COGNEE_API_KEY': 'child-key'}
        ds, url, key, res = access.gate_code_do_index(
            lambda: None, 'slug', '/repo', 'http://wrong', 'owner', env, error_fn=err, policy_mod=policy)
        self.assertEqual((ds, url, key, res), ('code-ds', 'http://127.0.0.1:18999', 'child-key', None))
        self.assertEqual(access.code_dataset_field(policy_mod=policy), 'datasetId')
        self.assertEqual(access.code_dataset_field(policy_mod=_make_policy(managed=lambda: False)), 'datasetName')

        denied = _make_policy(code_target=lambda *a, **k: (_ for _ in ()).throw(FakeNotReady('code_off')))
        _, res = access.gate_code_index(lambda: None, 'slug', '/repo', error_fn=err, policy_mod=denied)
        self.assertEqual(res, {'status': 403, 'error': 'code_off'})
        self.assertFalse(access.gate_code_auto(lambda: None, 'write', automatic=True, policy_mod=denied))
        self.assertFalse(access.gate_code_lane_dataset('slug', policy_mod=denied))

    def test_configure_managed_config_updates_passed_env_only_and_requires_creds(self):
        policy = _make_policy()
        with self.assertRaisesRegex(RuntimeError, 'managed_official_connection_required'):
            access.configure_managed_config({'base_url': 'http://127.0.0.1:18999'}, {}, policy_mod=policy)
        cfg = {'base_url': 'http://127.0.0.1:18999', 'api_key': 'child-key'}
        env = {}
        out = access.configure_managed_config(cfg, env, policy_mod=policy)
        self.assertIs(out, cfg)
        self.assertEqual(cfg['plugin_identity'], 'false')
        self.assertIs(cfg['shared_agent_memory'], False)
        self.assertIs(cfg['managed_endpoint'], True)
        self.assertEqual(env['COGNEE_SESSION_COMPANION_DATASET'], 'false')
        self.assertEqual(env['COGNEE_MANAGED_ENDPOINT'], 'true')

    def test_jsonl_offset_and_pre_boundary_codex_record_skip(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / 'rollout.jsonl'
            first = '{"type":"a"}\n'
            second = '{"type":"b"}\n'
            path.write_text(first + second, encoding='utf-8')
            self.assertEqual(list(access.iter_jsonl_lines(path, 0)), [first, second])
            self.assertEqual(list(access.iter_jsonl_lines(path, 3)), [second])
        self.assertTrue(access.skip_pre_boundary_codex_record(10, [], {'type': 'item_completed', 'item': {'type': 'AgentMessage'}}))
        self.assertFalse(access.skip_pre_boundary_codex_record(10, [], {'type': 'item_completed', 'item': {'type': 'UserMessage'}}))
        self.assertFalse(access.skip_pre_boundary_codex_record(0, [], {'type': 'item_completed', 'item': {'type': 'AgentMessage'}}))

    def test_http_denial_blocks_only_mutating_capture_and_improve_routes(self):
        blocked = []
        policy = _make_policy(block=blocked.append)
        access.record_managed_http_denial('GET', '/api/v1/remember/entry', 403, policy_mod=policy)
        access.record_managed_http_denial('POST', '/api/v1/search', 403, policy_mod=policy)
        self.assertEqual(blocked, [])
        access.record_managed_http_denial('POST', '/api/v1/remember/entry?x=1', 403, policy_mod=policy)
        access.record_managed_http_denial('POST', '/api/v1/improve', 401, policy_mod=policy)
        self.assertEqual(blocked, [403, 401])

    def test_remember_strict_and_recall_and_forget_policy_distinguish_denied_vs_unconfirmed(self):
        err = lambda code, msg: {'status': code, 'error': msg}
        env = {'COGNEE_BASE_URL': 'http://127.0.0.1:18999', 'COGNEE_API_KEY': 'child-key'}

        unavail = _make_policy(current=lambda *a, **k: (_ for _ in ()).throw(FakeNotReady('offline')))
        res, _ = access.prepare_managed_remember_strict(
            lambda: None, 'http://127.0.0.1:18999', 'old', 'proj-uuid', env, error_fn=err, policy_mod=unavail)
        self.assertEqual(res, {'status': 0, 'error': 'project_policy_unconfirmed'})

        readonly = _make_policy(current=lambda *a, **k: {
            'revision': 1, 'dataset_id': 'proj-uuid', 'policy': {'permissions': ['read']},
            'common': {'ready': True, 'readable': True, 'dataset_id': 'common-uuid'},
        })
        res, _ = access.prepare_managed_remember_strict(
            lambda: None, 'http://127.0.0.1:18999', 'old', 'other-uuid', env, error_fn=err, policy_mod=readonly)
        self.assertEqual(res, {'status': 403, 'error': 'managed_dataset_mismatch'})
        res, _ = access.prepare_managed_remember_strict(
            lambda: None, 'http://127.0.0.1:18999', 'old', 'proj-uuid', env, error_fn=err, policy_mod=readonly)
        self.assertEqual(res['status'], 403)
        self.assertIn('写入权限', res['error'])

        # Recall cross-project switches scope to graph and clears session_id.
        cross = _make_policy(recall_targets=lambda ds, ids: (['proj-uuid', 'common-uuid'], 'proj-uuid'))
        res, api_key, ds, ids, sid, scope = access.prepare_managed_recall(
            lambda: None, 'http://127.0.0.1:18999', 'old', 'proj-uuid', '', 'sess', ['session'], env, error_fn=err, policy_mod=cross)
        self.assertIsNone(res)
        self.assertEqual((api_key, ds, ids, sid, scope), ('child-key', '', ['proj-uuid', 'common-uuid'], '', ['graph']))

        # Forget tool policy: common read allowed, common delete denied, unavailable status 0.
        self.assertIsNone(access.check_forget_tool_policy('raw', 'common-uuid', policy_mod=readonly))
        denied_common_del = json.loads(access.check_forget_tool_policy('forget', 'common-uuid', policy_mod=readonly))
        self.assertEqual((denied_common_del['status'], denied_common_del['error'], denied_common_del['local_denial']),
                         (403, 'project_delete_denied', True))
        unconfirmed = json.loads(access.check_forget_tool_policy('forget', 'proj-uuid', policy_mod=unavail))
        self.assertEqual((unconfirmed['status'], unconfirmed['error']), (0, 'project_policy_unconfirmed'))

    def test_tool_patch_overlays_preserve_legacy_anchor_compatibility(self):
        legacy_remember = (
            '    from _memlane_policy import managed, permit\n'
            '    if managed() and not permit("write", dataset_id or dataset):\n'
            '        return _error(403, "当前 Agent 没有此项目的写入权限")\n'
        )
        patched_rem = remember_response(legacy_remember)
        self.assertIn('project_policy_unconfirmed', patched_rem)
        with self.assertRaises(ValueError):
            remember_response(patched_rem)

        legacy_forget = (
            '    load_env_file()\n'
            '    from _memlane_policy import require, recall_targets\n'
            '    command, target = sys.argv[3], sys.argv[4]\n'
            '    if command in ("data", "raw"):\n'
            '        recall_targets(target, [target])\n'
            '    elif command == "forget":\n'
            '        require("delete", target)\n'
            'SERVICE_URL="$(python3 - <<\'PY\' "${runtime_json}" 2>/dev/null || true\n'
        )
        patched_fgt = forget_response(legacy_forget)
        self.assertIn('memlane_local_result', patched_fgt)
        with self.assertRaises(ValueError):
            forget_response(patched_fgt)


class ProjectHostHooksTests(unittest.TestCase):
    def test_begin_capture_boundary_and_claude_fresh_transcript_lifecycle(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp).resolve()
            profile = root / 'claude-profile'
            project_dir = profile / 'projects' / 'repo'
            project_dir.mkdir(parents=True)
            state_box = {}

            @contextlib.contextmanager
            def _state():
                yield state_box

            def _position(path):
                st = Path(path).stat()
                return {'path': str(Path(path).absolute()), 'inode': st.st_ino, 'device': st.st_dev, 'size': st.st_size}

            session = str(uuid.uuid4())
            transcript = project_dir / (session + '.jsonl')
            decision = {'revision': 5, 'policy': {'permissions': ['read', 'write'], 'can_capture': True}}
            policy = _make_policy(_state=_state, _position=_position, current=lambda *a, **k: decision)
            common = types.SimpleNamespace(get_session_key=lambda: session)

            host.claude_begin_capture_boundary(
                policy, str(transcript), source='startup', session_key=session,
                environ={'CLAUDE_CONFIG_DIR': str(profile)}, common_mod=common)
            self.assertTrue(state_box.get('paused'))
            self.assertIn('fresh_host_transcript', state_box)
            self.assertIsNone(host.claude_capture_boundary(policy, lambda *a, **k: 99, str(transcript)))

            transcript.write_text('{"type":"user"}\n', encoding='utf-8')
            self.assertEqual(host.claude_capture_boundary(policy, lambda *a, **k: 99, str(transcript)), 0)
            self.assertNotIn('fresh_host_transcript', state_box)
            self.assertNotIn('paused', state_box)

    def test_managed_python_tool_runtime_and_cursor_helpers(self):
        env = {'COGNEE_BASE_URL': 'http://127.0.0.1:18999', 'COGNEE_API_KEY': 'child-key'}
        policy = _make_policy(dataset=lambda: 'ds-uuid')
        seen = {}
        common = types.SimpleNamespace(
            shell_runtime_overrides=lambda url: {'host_key': 'hk-1'},
            set_session_key=lambda k: seen.setdefault('key', k),
            get_session_key=lambda: seen.get('key', ''),
            resolve_cognee_session_id=lambda k: 'sid:' + k,
        )
        rt = host.managed_python_tool_runtime(lambda: None, env, policy_mod=policy, common_mod=common)
        self.assertEqual(rt['dataset_id'], 'ds-uuid')
        self.assertEqual(rt['session_id'], 'sid:hk-1')
        self.assertEqual(json.loads(host.managed_cli_recall_fallback(policy_mod=policy)),
                         {'error': 'managed_server_unavailable', 'authoritative': False})

        coding = types.SimpleNamespace(build_block=lambda q, c: f'ctx:{q}@{c}')
        rec = host.cursor_hold_record('t1', 'x' * 20000, '/repo', lambda s: s)
        self.assertEqual(len(rec['query']), 16000)
        self.assertEqual(
            host.cursor_rebuild_held_context(lambda: None, 'sess-1', {'query': 'hello', 'cwd': '/repo'},
                                             common_mod=common, coding_mod=coding),
            'ctx:hello@/repo',
        )
        self.assertEqual(
            host.cursor_rebuild_held_context(lambda: None, 'sess-1', {'context': 'legacy'},
                                             common_mod=common, coding_mod=coding),
            '',
        )


if __name__ == '__main__':
    unittest.main()
