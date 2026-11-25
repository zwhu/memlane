import unittest

from engines.cognee.llm_config import compute_model_digest, environment_from_generation_config, native_llm_kwargs


class LLMConfigTests(unittest.TestCase):
    def test_absent_and_null_values_are_omitted(self):
        self.assertEqual(native_llm_kwargs({'LLM_TEMPERATURE': None, 'LLM_ARGS': None}), {
            '_env_file': None, '_env_prefix': 'MEMLANE_HANDOFF_ISOLATED_'})
        self.assertEqual(environment_from_generation_config({'provider': None, 'temperature': None}), {})

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

    def test_environment_override_and_canonical_digest(self):
        config = {'model': 'unused', 'environment': {'LLM_MODEL': 'explicit'}}
        self.assertEqual(environment_from_generation_config(config), {'LLM_MODEL': 'explicit'})
        self.assertEqual(compute_model_digest(config), compute_model_digest(dict(reversed(list(config.items())))))
        self.assertNotEqual(compute_model_digest(config), compute_model_digest({'model': 'unused'}))

    def test_local_ollama_without_api_key(self):
        endpoint = 'http://127.0.0.1:11434'
        for config in (
            {'provider': 'ollama', 'model': 'local', 'endpoint': endpoint},
            {'environment': {'LLM_PROVIDER': 'ollama', 'LLM_MODEL': 'local', 'LLM_ENDPOINT': endpoint}},
        ):
            with self.subTest(config=config):
                self.assertEqual(environment_from_generation_config(config), {
                    'LLM_PROVIDER': 'ollama', 'LLM_MODEL': 'local', 'LLM_ENDPOINT': endpoint,
                    'LLM_API_KEY': 'ollama', 'OLLAMA_API_BASE': endpoint})

    def test_ollama_keeps_explicit_key_and_base(self):
        env = {'LLM_PROVIDER': 'ollama', 'LLM_ENDPOINT': 'http://127.0.0.1:11434',
               'LLM_API_KEY': 'explicit', 'OLLAMA_API_BASE': 'http://localhost:11434'}
        self.assertEqual(environment_from_generation_config({'environment': env}), env)
