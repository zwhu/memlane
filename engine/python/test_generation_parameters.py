import json
import tempfile
import unittest
from pathlib import Path

from generation_config import GenerationStore, compile_environment, fingerprint, validate_profile
from project_policy import PolicyError


LOCAL = {'mode': 'provider', 'provider': 'ollama', 'model': 'synthetic', 'endpoint': 'http://127.0.0.1:11434'}


class GenerationParameterTests(unittest.TestCase):
    def test_endpoint_capacity_is_model_and_agent_independent(self):
        profiles = [LOCAL, {'mode': 'agent_bridge', 'bridge_kind': 'codex', 'model': 'synthetic'}]
        for profile in profiles:
            self.assertEqual(validate_profile({**profile, 'max_concurrent_requests': 4})['max_concurrent_requests'], 4)
            for invalid in (None, True, 0, -1, 257, 1.5, '2'):
                with self.assertRaisesRegex(PolicyError, 'invalid_generation_capacity'):
                    validate_profile({**profile, 'max_concurrent_requests': invalid})
        self.assertNotEqual(fingerprint(validate_profile(LOCAL)),
                            fingerprint(validate_profile({**LOCAL, 'max_concurrent_requests': 4})))

    def test_bridge_uses_official_chunk_batch_without_timed_pacing(self):
        with tempfile.TemporaryDirectory() as temporary:
            token = Path(temporary) / 'bridge.key'
            token.write_text('synthetic-private-key'); token.chmod(0o600)
            profile = {'mode':'agent_bridge', 'bridge_kind':'codex', 'model':'synthetic'}
            env = compile_environment(profile, {'completion_url':'http://127.0.0.1:18173', 'completion_token_file':str(token)})
            self.assertEqual(env['CHUNKS_PER_BATCH'], '2')
            for key in ('LLM_RATE_LIMIT_ENABLED','LLM_RATE_LIMIT_REQUESTS','LLM_RATE_LIMIT_INTERVAL'):
                self.assertNotIn(key, env)
        self.assertNotIn('LLM_RATE_LIMIT_ENABLED', compile_environment(LOCAL, {}))
        self.assertNotIn('CHUNKS_PER_BATCH', compile_environment(LOCAL, {}))

    def test_rejects_invalid_or_unbounded_values_and_bridge_overrides(self):
        for value in (True, None, '0.6', float('nan'), float('inf'), -0.1, 2.1, 10**1000):
            with self.subTest(temperature=str(value)), self.assertRaisesRegex(PolicyError, 'invalid_generation_temperature'):
                validate_profile({**LOCAL, 'temperature': value})
        for value in (True, None, '8192', 8192.0, 0, -1, 131073, 10**1000):
            with self.subTest(tokens=str(value)), self.assertRaisesRegex(PolicyError, 'invalid_generation_output_limit'):
                validate_profile({**LOCAL, 'max_output_tokens': value})
        for key in ('temperature', 'max_output_tokens'):
            with self.assertRaisesRegex(PolicyError, 'unsupported_generation_bridge'):
                validate_profile({'mode': 'agent_bridge', 'bridge_kind': 'codex', 'model': 'old', key: 1})

    def test_explicit_zero_and_limits_compile_for_each_official_protocol(self):
        for provider in ('openai', 'anthropic', 'gemini', 'custom', 'ollama'):
            profile = {**LOCAL, 'provider': provider, 'temperature': 0, 'max_output_tokens': 8192}
            if provider != 'ollama':
                profile.update(credential_ref='key', credential_version='v1')
            with self.subTest(provider=provider):
                env = compile_environment(profile, {}, 'synthetic-only-key')
                self.assertEqual(env['LLM_TEMPERATURE'], '0.0')
                self.assertEqual(env['LLM_MAX_COMPLETION_TOKENS'], '8192')
                self.assertEqual(json.loads(env['LLM_ARGS']), {'max_tokens': 8192})
        legacy = compile_environment(LOCAL, {})
        self.assertNotIn('LLM_TEMPERATURE', legacy)
        self.assertNotIn('LLM_ARGS', legacy)

    def test_parameter_change_invalidates_validation_and_legacy_digest_is_preserved(self):
        with tempfile.TemporaryDirectory() as temporary:
            store = GenerationStore(Path(temporary) / 'generation.json', 'fixed', legacy_model='original')
            try:
                legacy = store.record(LOCAL, 1)
                self.assertEqual(legacy['digest'], fingerprint({'profile': LOCAL, 'service_commit': 'fixed', 'credential_digest': None}))
                base = {'mode': 'provider', 'provider': 'custom', 'model': 'synthetic', 'endpoint': 'https://example.test/v1',
                        'temperature': .6, 'max_output_tokens': 8192}
                first = store.put('api', base, store.state['revision'], 'first', api_key='synthetic-only-key')
                store.state['validations']['passed'] = {'digest': first['digest'], 'status': 'passed', 'plain_passed': True,
                    'schema_passed': True, 'quality_passed': True, 'registered_revision': 1}
                store.select_next(first, store.state['revision'], 'select-first')
                original = store.resolve(first)
                for index, change in enumerate(({'temperature': 0}, {'max_output_tokens': 4096})):
                    changed = store.put('api', {**original['profile'], **change}, store.state['revision'], 'change-' + str(index))
                    self.assertNotEqual(first['digest'], changed['digest'])
                    with self.assertRaisesRegex(PolicyError, 'generation_validation_required'):
                        store.select_next(changed, store.state['revision'], 'not-verified-' + str(index))
                    self.assertEqual(store.resolve(first), original)
                zero = store.resolve(changed)['profile']
                self.assertEqual(zero['temperature'], .6)
                self.assertEqual(store.state['active']['id'], 'existing-codex')
            finally:
                store.close()
