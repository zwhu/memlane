import json
import tempfile
import unittest
from pathlib import Path

from generation_config import GenerationStore, compile_environment, fingerprint, validate_profile, native_llm_kwargs
from project_policy import PolicyError


LOCAL = {'mode': 'provider', 'provider': 'ollama', 'model': 'synthetic', 'endpoint': 'http://127.0.0.1:11434'}


class GenerationParameterTests(unittest.TestCase):
    def test_endpoint_capacity_is_model_and_agent_independent(self):
        profiles = [LOCAL, {'mode': 'agent_bridge', 'bridge_kind': 'codex', 'model': 'synthetic'},
                    {'mode': 'agent_bridge', 'bridge_kind': 'pi', 'bridge_provider': 'deepseek', 'model': 'synthetic'},
                    {'mode': 'agent_bridge', 'bridge_kind': 'traex', 'model': 'synthetic'}]
        for profile in profiles:
            self.assertEqual(validate_profile({**profile, 'max_concurrent_requests': 4})['max_concurrent_requests'], 4)
            limit = 32 if profile['mode'] == 'agent_bridge' else 256
            self.assertEqual(validate_profile({**profile, 'max_concurrent_requests': limit})['max_concurrent_requests'], limit)
            for invalid in (None, True, 0, -1, limit + 1, 257, 1.5, '2'):
                with self.assertRaisesRegex(PolicyError, 'invalid_generation_capacity'):
                    validate_profile({**profile, 'max_concurrent_requests': invalid})
        self.assertEqual(validate_profile({**LOCAL, 'max_concurrent_requests': 33})['max_concurrent_requests'], 33)
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


class NativeParameterBoundaryTests(unittest.TestCase):
    def test_unset_sampling_stays_unset_for_bridge_and_every_direct_protocol(self):
        with tempfile.TemporaryDirectory() as temporary:
            token=Path(temporary)/'completion-key';token.write_text('synthetic-only-key');token.chmod(0o600)
            runtime={'completion_url':'http://127.0.0.1:18173','completion_token_file':str(token)}
            profiles=[{'mode':'agent_bridge','bridge_kind':'codex','model':'gpt-6.1-sol'}]
            profiles.extend({**LOCAL,'provider':provider,**({} if provider=='ollama' else
                {'credential_ref':'fixture','credential_version':'one'})}
                for provider in ('openai','anthropic','gemini','custom','ollama'))
            for profile in profiles:
                with self.subTest(mode=profile['mode'],provider=profile.get('provider')):
                    values=native_llm_kwargs(compile_environment(profile,runtime,'synthetic-only-key'))
                    for field in ('llm_temperature','llm_max_completion_tokens','llm_args'):
                        self.assertNotIn(field,values)
                    self.assertIsNone(values['_env_file'])
                    self.assertTrue(values['_env_prefix'])

    def test_explicit_zero_and_output_limit_are_preserved_without_default_injection(self):
        for provider in ('openai','anthropic','gemini','custom','ollama'):
            profile={**LOCAL,'provider':provider,'temperature':0,'max_output_tokens':4096,
                **({} if provider=='ollama' else {'credential_ref':'fixture','credential_version':'one'})}
            values=native_llm_kwargs(compile_environment(profile,{},'synthetic-only-key'))
            self.assertEqual(float(values['llm_temperature']),0)
            self.assertEqual(int(values['llm_max_completion_tokens']),4096)
            self.assertEqual(values['llm_args'],{'max_tokens':4096})

    def test_production_consumers_cannot_bypass_shared_native_configuration_factory(self):
        import ast
        root=Path(__file__).resolve().parent
        for path in root.glob('generation_*.py'):
            if path.name=='generation_config.py':continue
            tree=ast.parse(path.read_text())
            aliases={'LLMConfig'}
            for node in ast.walk(tree):
                if isinstance(node,ast.ImportFrom):
                    aliases.update(item.asname or item.name for item in node.names if item.name=='LLMConfig')
            calls=[node for node in ast.walk(tree) if isinstance(node,ast.Call) and
                ((isinstance(node.func,ast.Name) and node.func.id in aliases) or
                 (isinstance(node.func,ast.Attribute) and node.func.attr=='LLMConfig'))]
            self.assertEqual(calls,[],path.name+' constructs native LLM config outside the shared boundary')
