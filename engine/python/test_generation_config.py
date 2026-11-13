import json
from pathlib import Path
import tempfile
import unittest

from generation_config import GenerationStore, compile_environment, validate_profile, runtime_generation
from project_control import Registry, private_write
from project_policy import PolicyError


class GenerationTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.root = Path(self.temp.name)
        self.path = self.root / 'generation.json'
        self.store = GenerationStore(self.path, 'fixed-commit', legacy_model='original-model')

    def tearDown(self):
        self.store.close()
        self.temp.cleanup()

    def put(self, operation='save-one', model='deepseek-test', key='synthetic-secret'):
        return self.store.put('api', {'mode': 'provider', 'provider': 'custom', 'model': model,
                                     'endpoint': 'https://example.test/v1'}, self.store.state['revision'], operation, api_key=key)

    def test_save_is_private_immutable_and_idempotent_without_touching_active(self):
        active = self.store.state['active'].copy()
        first = self.put()
        self.assertEqual(self.put(), first)
        self.assertEqual(self.store.state['revision'], 1)
        self.assertNotIn('synthetic-secret', json.dumps(self.store.view()))
        self.assertNotIn('credential_ref', json.dumps(self.store.view()))
        row = self.store.resolve(first)
        credential = self.store.credential_path(row['profile']['credential_ref'], row['profile']['credential_version'])
        self.assertEqual(credential.stat().st_mode & 0o777, 0o600)
        self.assertEqual(credential.parent.stat().st_mode & 0o777, 0o700)
        self.assertEqual(self.path.stat().st_mode & 0o777, 0o600)
        second = self.put('save-two', 'different-model')
        self.assertEqual(first['revision'], 1)
        self.assertEqual(second['revision'], 2)
        self.assertEqual(self.store.resolve(first), row)
        self.assertEqual(self.store.state['active'], active)
        with self.assertRaisesRegex(PolicyError, 'operation_id_conflict'):
            self.put(model='changed')

    def test_validation_must_match_exact_revision_schema_and_quality(self):
        first = self.put()
        ref = {k: first[k] for k in ('id', 'revision')}
        with self.assertRaisesRegex(PolicyError, 'generation_validation_required'):
            self.store.select_next(ref, 1, 'select')
        self.store.state['validations']['v'] = {'status': 'passed', 'digest': first['digest'],
                                                 'plain_passed': True, 'schema_passed': True, 'quality_passed': True}
        self.store.select_next(ref, 1, 'select')
        self.assertEqual(self.store.state['next'], ref)
        self.assertEqual(self.store.state['active']['id'], 'existing-codex')
        changed = self.put('change-key', key='new-synthetic-key')
        with self.assertRaisesRegex(PolicyError, 'generation_validation_required'):
            self.store.select_next(changed, self.store.state['revision'], 'new-select')

    def test_protocol_mapping_and_rejects_unsafe_endpoints_or_arbitrary_fields(self):
        for provider, prefix in [('openai', 'openai'), ('anthropic', 'anthropic'), ('gemini', 'gemini'), ('custom', 'openai')]:
            p = {'mode': 'provider', 'provider': provider, 'model': 'org/model',
                 'endpoint': 'https://example.test/v1', 'credential_ref': 'api', 'credential_version': 'v1'}
            result = compile_environment(p, {}, 'key')
            self.assertEqual(result['LLM_MODEL'], prefix + '/org/model')
            self.assertEqual(result['LLM_PROVIDER'], provider)
            self.assertEqual(compile_environment({**p, 'model': prefix + '/model'}, {}, 'key')['LLM_MODEL'], prefix + '/model')
            for endpoint in ['http://example.test', 'https://key@example.test', 'https://example.test?key=secret', 'file:///tmp/model']:
                with self.assertRaises(PolicyError):
                    validate_profile({**p, 'endpoint': endpoint})
            with self.assertRaises(PolicyError):
                validate_profile({**p, 'fallback_model': 'unrequested'})

    def test_latest_failed_or_unknown_validation_supersedes_old_success_at_select_and_startup(self):
        from generation_startup import candidate
        first = self.put()
        ref = {k: first[k] for k in ('id', 'revision')}
        passed = {'status': 'passed', 'digest': first['digest'], 'plain_passed': True,
                  'schema_passed': True, 'quality_passed': True, 'registered_revision': 2}
        self.store.state['validations']['z-old'] = passed
        self.store.select_next(ref, 1, 'select-first')
        for state in ('queued', 'running', 'failed', 'result_unknown'):
            with self.subTest(state=state):
                self.store.state['validations']['a-new'] = {**passed, 'status': state, 'registered_revision': 3}
                with self.assertRaisesRegex(PolicyError, 'generation_validation_required'):
                    self.store.select_next(ref, self.store.state['revision'], 'blocked-' + state)
                with self.assertRaisesRegex(PolicyError, 'generation_validation_required'):
                    candidate(self.store)
                self.assertEqual(self.store.state['active']['id'], 'existing-codex')
                self.assertEqual(self.store.state['next'], ref)
        self.store.save()
        self.store.close()
        self.store = GenerationStore(self.path, 'fixed-commit')
        with self.assertRaisesRegex(PolicyError, 'generation_validation_required'):
            candidate(self.store)
        self.store.state['validations']['a-new']['status'] = 'passed'
        self.assertEqual(candidate(self.store), ref)
        # JSON key order and lexical operation IDs do not establish chronology.
        for v in self.store.state['validations'].values():
            v.pop('registered_revision')
        self.store.state['validations']['a-new']['status'] = 'failed'
        with self.assertRaisesRegex(PolicyError, 'generation_validation_required'):
            candidate(self.store)

    def test_second_writer_is_denied_and_service_commit_invalidates_digest(self):
        with self.assertRaises(BlockingIOError):
            GenerationStore(self.path, 'fixed-commit')
        first = self.put()
        self.store.service_commit = 'new-commit'
        with self.assertRaisesRegex(PolicyError, 'generation_validation_stale'):
            self.store.resolve(first)

    def test_service_reads_fixed_revision_without_taking_controller_lock_or_using_next(self):
        first = self.put()
        runtime = {'generation_file': str(self.path), 'generation_reference': first, 'service_commit': 'fixed-commit'}
        profile, env, digest = runtime_generation(runtime)
        self.put('edit', 'changed-model', 'changed-key')
        again, again_env, again_digest = runtime_generation(runtime)
        self.assertEqual((profile, env, digest), (again, again_env, again_digest))
        self.assertEqual(env['LLM_MODEL'], 'openai/deepseek-test')
        self.assertEqual(env['LLM_API_KEY'], 'synthetic-secret')
        self.assertEqual(self.store.state['active']['id'], 'existing-codex')

    def test_changed_key_file_invalidates_validation_even_with_same_version(self):
        first = self.put()
        profile = self.store.resolve(first)['profile']
        path = self.store.credential_path(profile['credential_ref'], profile['credential_version'])
        path.write_text('externally-changed-key')
        with self.assertRaisesRegex(PolicyError, 'generation_validation_stale'):
            self.store.resolve(first)

    def test_schema1_migration_preserves_identity_acl_and_material_references(self):
        path = self.root / 'registry.json'
        before = {'schema_version': 1, 'revision': 9, 'agents': {'codex-one': {'name': 'original', 'principal_id': 'unchanged'}},
                  'defaults': {'access': 'read'}, 'projects': {'p': {'overrides': {'codex-one': {'access': 'maintain'}}}},
                  'sessions': {'["codex-one", "session"]': 'p'}, 'codex_connections': {'codex-one': {'status': 'prepared'}}}
        private_write(path, json.dumps(before).encode())
        registry = Registry(path, None)
        try:
            after = registry.state
            self.assertEqual(after['schema_version'], 2)
            for field in ('revision', 'defaults', 'projects', 'sessions', 'codex_connections'):
                self.assertEqual(after[field], before[field])
            self.assertEqual(after['connections'], before['codex_connections'])
            self.assertEqual(after['agents']['codex-one']['principal_id'], 'unchanged')
            self.assertEqual(json.loads(Path(str(path) + '.schema1-backup').read_text()), before)
        finally:
            registry.close()


if __name__ == '__main__':
    unittest.main()
