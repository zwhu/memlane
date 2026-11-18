import unittest
import contextlib
import io
import tempfile
from pathlib import Path
from unittest.mock import patch

import lint
from lint import check_source


class ModelParameterLintTests(unittest.TestCase):
    def codes(self, source, name='generation_handoff_runtime.py'):
        return {code for _, code, _ in check_source(source, name)}

    def test_original_regression_and_literal_injection_are_rejected(self):
        cases = ["fields.setdefault('llm_temperature', 0.0)",
                 "fields.get('LLM_TEMPERATURE', previous)",
                 "fields['llm_temperature'] = 0.0",
                 "fields.update({'llm_max_completion_tokens': 16384})",
                 "config.llm_temperature = 0",
                 "fields['temperature'] = configured or 0.7",
                 "request(temperature=0)",
                 "request(**{'temperature': 0})"]
        for source in cases:
            with self.subTest(source=source):
                self.assertTrue(self.codes(source))

    def test_constructor_alias_and_qualified_constructor_are_rejected(self):
        for source in ("LLMConfig(**fields)",
                       "from cognee.infrastructure.llm.config import LLMConfig as Config\nConfig(**fields)",
                       "Config = native.LLMConfig\nOther = Config\nOther(**fields)",
                       "config.LLMConfig(**fields)"):
            with self.subTest(source=source):
                self.assertIn('ML001', self.codes(source))

    def test_shared_boundary_also_rejects_defaults_or_second_constructor(self):
        source = """def native_llm_configuration(environment):
    return LLMConfig(**native_llm_kwargs(environment))
def other(environment):
    return LLMConfig(**native_llm_kwargs(environment))
"""
        self.assertIn('ML001', self.codes(source, 'generation_config.py'))
        self.assertIn('ML003', self.codes("x.setdefault('llm_args', {})", 'generation_config.py'))

    def test_explicit_user_values_and_shared_constructor_are_allowed(self):
        source = """def native_llm_configuration(environment):
    return LLMConfig(**native_llm_kwargs(environment))
def parameters(profile):
    result = {}
    if 'temperature' in profile:
        result['LLM_TEMPERATURE'] = str(profile['temperature'])
    return result
"""
        self.assertEqual(self.codes(source, 'generation_config.py'), set())
        self.assertEqual(self.codes("native_llm_configuration(environment)"), set())

    def test_diagnostics_include_location_and_syntax_errors_fail(self):
        self.assertEqual(check_source("\nx.setdefault('llm_temperature', 0)", 'x.py')[0][:2], (2, 'ML003'))
        self.assertIn('ML000', self.codes('def broken('))

    def test_lint_entry_fails_for_production_violation_and_skips_test_fixture(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            sources = root / 'engine/python'
            sources.mkdir(parents=True)
            production = sources / 'new_consumer.py'
            production.write_text("LLMConfig(llm_temperature=0)")
            (sources / 'test_fixture.py').write_text("LLMConfig(llm_temperature=0)")
            output = io.StringIO()
            with patch.object(lint, 'ROOT', root), contextlib.redirect_stdout(output):
                self.assertTrue(lint.main())
                self.assertIn('engine/python/new_consumer.py:1: ML001', output.getvalue())
                self.assertNotIn('test_fixture.py', output.getvalue())
                production.write_text('native_llm_configuration(environment)')
                self.assertFalse(lint.main())



class GenerationPresentationLintTests(unittest.TestCase):
    def test_flat_model_and_provider_rendering_is_rejected(self):
        from lint import check_go_source
        source = 'func (a *App) generationProfileCards(c *ui.Context) {\n model := row.Profile.Model\n provider := row.Profile.Provider\n}'
        self.assertTrue(all(row[1] == 'ML004' for row in check_go_source(source, 'settings_model_cards.go')))
        self.assertEqual(len(check_go_source(source, 'settings_model_cards.go')), 3)

    def test_shared_projection_is_allowed(self):
        from lint import check_go_source
        self.assertEqual(check_go_source('func (a *App) generationProfileCards(c *ui.Context) { groups := generationProfileGroups(s.Generation) }', 'settings_model_cards.go'), [])


if __name__ == '__main__':
    unittest.main()
