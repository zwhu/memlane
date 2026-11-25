import unittest

from engines.cognee.llm_config import compute_model_digest, environment_from_generation_config, native_llm_kwargs
from engines.cognee.ollama_schema import receipt_for


DEFAULTS = {'STRUCTURED_OUTPUT_FRAMEWORK': 'litellm_native', 'LITELLM_LOCAL_MODEL_COST_MAP': 'true'}


class LLMConfigTests(unittest.TestCase):
    def test_absent_and_null_values_are_omitted(self):
        self.assertEqual(native_llm_kwargs({'LLM_TEMPERATURE': None, 'LLM_ARGS': None}), {
            '_env_file': None, '_env_prefix': 'MEMLANE_HANDOFF_ISOLATED_'})
        for config in ({}, {'provider': None, 'temperature': None, 'max_output_tokens': None, 'llm_args': None,
                            'structured_output_framework': None, 'chunks_per_batch': None}):
            self.assertEqual(environment_from_generation_config(config), DEFAULTS)

    def test_explicit_zero_and_args(self):
        for value in (0, 0.0, '0'):
            with self.subTest(value=value):
                for args in ('{"top_p":0}', {'top_p': 0}):
                    kwargs = native_llm_kwargs({'LLM_TEMPERATURE': value,
                                               'LLM_MAX_COMPLETION_TOKENS': value, 'LLM_ARGS': args})
                    self.assertIs(kwargs['llm_temperature'], value)
                    self.assertIs(kwargs['llm_max_completion_tokens'], value)
                    self.assertEqual(kwargs['llm_args'], {'top_p': 0})
                env = environment_from_generation_config({'temperature': value, 'max_output_tokens': value})
                self.assertIs(env['LLM_TEMPERATURE'], value)
                self.assertIs(env['LLM_MAX_COMPLETION_TOKENS'], value)
                self.assertEqual(env['LLM_ARGS'], {'max_tokens': 0})
        for args in ('{"top_p":0}', {'top_p': 0}):
            env = environment_from_generation_config({'llm_args': args, 'max_output_tokens': 0})
            self.assertEqual(env['LLM_ARGS'], {'top_p': 0, 'max_tokens': 0})
            self.assertNotIn('LLM_TEMPERATURE', env)

    def test_environment_override_and_canonical_digest(self):
        explicit = {'LLM_MODEL': 'explicit', 'LLM_TEMPERATURE': 0.0, 'LLM_ARGS': '{"top_p":0}'}
        config = {'model': 'unused', 'environment': explicit}
        self.assertEqual(environment_from_generation_config(config), {**DEFAULTS, **explicit})
        self.assertNotIn('STRUCTURED_OUTPUT_FRAMEWORK', explicit)
        self.assertNotIn('LITELLM_LOCAL_MODEL_COST_MAP', explicit)
        self.assertEqual(compute_model_digest(config), compute_model_digest(dict(reversed(list(config.items())))))
        self.assertNotEqual(compute_model_digest(config), compute_model_digest({'model': 'unused'}))

    def test_local_ollama_without_api_key(self):
        endpoint = 'http://127.0.0.1:11434'
        model = 'ollama_chat/qwen3:8b'
        evidence = receipt_for(model, endpoint)
        self.assertEqual(environment_from_generation_config({
            'provider': 'ollama', 'model': 'qwen3:8b', 'endpoint': endpoint, 'ollama_schema_evidence': evidence,
        }), {**DEFAULTS, 'LLM_PROVIDER': 'ollama', 'LLM_MODEL': model, 'LLM_ENDPOINT': endpoint,
             'LLM_API_KEY': 'ollama', 'OLLAMA_API_BASE': endpoint, 'MEMLANE_OLLAMA_SCHEMA_EVIDENCE': evidence})

    def test_flat_provider_prefixes(self):
        for provider, model, qualified in (
            ('custom', 'org/my-model', 'openai/org/my-model'),
            ('openai', 'openai/gpt-5', 'openai/gpt-5'),
            ('anthropic', 'claude-sonnet', 'anthropic/claude-sonnet'),
            ('gemini', 'gemini-pro', 'gemini/gemini-pro'),
        ):
            with self.subTest(provider=provider):
                self.assertEqual(environment_from_generation_config({'provider': provider, 'model': model}), {
                    **DEFAULTS, 'LLM_PROVIDER': provider, 'LLM_MODEL': qualified})
        with self.assertRaisesRegex(ValueError, '^unsupported_generation_provider$'):
            environment_from_generation_config({'provider': 'unsupported', 'model': 'example'})

    def test_flat_agent_bridge_defaults_and_explicit_overrides(self):
        config = {'mode': 'agent_bridge', 'bridge_kind': 'codex', 'model': 'gpt-5'}
        expected = {**DEFAULTS, 'LLM_PROVIDER': 'openai', 'LLM_MODEL': 'openai/gpt-5', 'CHUNKS_PER_BATCH': '2'}
        self.assertEqual(environment_from_generation_config(config), expected)
        self.assertEqual(environment_from_generation_config({
            **config, 'provider': 'custom', 'chunks_per_batch': 3, 'structured_output_framework': 'instructor',
        }), {**expected, 'CHUNKS_PER_BATCH': 3, 'STRUCTURED_OUTPUT_FRAMEWORK': 'instructor'})

    def test_ollama_keeps_explicit_key_and_base(self):
        env = {'LLM_PROVIDER': 'ollama', 'LLM_MODEL': 'local', 'LLM_ENDPOINT': 'http://127.0.0.1:11434'}
        self.assertEqual(environment_from_generation_config({'environment': env}), {
            **DEFAULTS, **env, 'LLM_API_KEY': 'ollama', 'OLLAMA_API_BASE': env['LLM_ENDPOINT']})
        explicit = {**env, 'LLM_API_KEY': 'explicit', 'OLLAMA_API_BASE': 'http://localhost:11434',
                    'STRUCTURED_OUTPUT_FRAMEWORK': 'instructor', 'LITELLM_LOCAL_MODEL_COST_MAP': 'false'}
        self.assertEqual(environment_from_generation_config({'environment': explicit}), explicit)
