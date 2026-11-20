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


    def test_configuring_a_source_cannot_enable_it(self):
        from lint import check_go_source
        source='func (a *App) chooseGenerationSource(mode string) { a.openGenerationHandoff(nil) }'
        self.assertEqual({row[1] for row in check_go_source(source,'generation_workspace.go')},{'ML005'})
        allowed='func (a *App) chooseGenerationSource(mode string) { a.selectGenerationProfile(id,row) }'
        self.assertEqual(check_go_source(allowed,'generation_workspace.go'),[])

    def test_management_page_cannot_enable_a_profile(self):
        from lint import check_go_source
        source='func (a *App) generationManagement(c *ui.Context) { a.startGenerationHandoff() }'
        self.assertEqual({row[1] for row in check_go_source(source,'generation_management.go')},{'ML005'})

    def test_profile_generation_cannot_restore_total_deadline(self):
        source='async def generate(adapter,row):\n return await asyncio.wait_for(collect(),180)'
        self.assertEqual({row[1] for row in check_source(source,'user_profiles.py')},{'ML006'})
        self.assertEqual({row[1] for row in check_source(source,'engine/python/user_profiles/runner.py')},{'ML006'})

    def test_missing_protected_go_targets_fail_instead_of_silently_passing(self):
        from lint import check_go_source
        renamed = 'func (a *App) renamedCards(c *ui.Context) {}'
        self.assertEqual({row[1] for row in check_go_source(renamed, 'settings_model_cards.go')}, {'ML004'})
        partial = 'func (a *App) chooseGenerationSource(mode string) {}'
        self.assertEqual(check_go_source(partial, 'generation_workspace.go'), [])
        self.assertEqual({row[1] for row in check_go_source(partial, 'generation_workspace.go', require_all_targets=True)}, {'ML005'})

    def test_scripts_directory_is_scanned_and_lint_or_test_scripts_skipped(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / 'engine/python').mkdir(parents=True)
            scripts_dir = root / 'scripts'
            scripts_dir.mkdir(parents=True)
            bad = scripts_dir / 'helper.py'
            bad.write_text('LLMConfig(llm_temperature=0)')
            (scripts_dir / 'lint.py').write_text('LLMConfig(llm_temperature=0)')
            (scripts_dir / 'test_helper.py').write_text('LLMConfig(llm_temperature=0)')
            output = io.StringIO()
            with patch.object(lint, 'ROOT', root), contextlib.redirect_stdout(output):
                self.assertTrue(lint.main())
                self.assertIn('scripts/helper.py:1: ML001', output.getvalue())
                self.assertNotIn('scripts/lint.py', output.getvalue())
                self.assertNotIn('scripts/test_helper.py', output.getvalue())
                bad.write_text('native_llm_configuration(environment)')
                self.assertFalse(lint.main())


class GlobalEnvironLintTests(unittest.TestCase):
    def codes(self, source, path='project_control_server.py'):
        return {code for _, code, _ in check_source(source, path)}

    def test_unallowlisted_environ_mutations_are_rejected(self):
        cases = [
            "import os\nos.environ['HOME'] = '/tmp'",
            "import os\nos.environ['PATH'] += ':/bin'",
            "import os\nos.environ |= {'FOO': '1'}",
            "import os\nos.environ.update({'FOO': '1'})",
            "import os\nos.environ.pop('FOO', None)",
            "import os\nos.environ.popitem()",
            "import os\nos.environ.clear()",
            "import os\nos.environ.setdefault('FOO', '1')",
            "import os\ndel os.environ['FOO']",
            "import os\nos.environ['A'], other = '1', '2'",
            "import os as operating_system\noperating_system.environ['FOO'] = '1'",
            "from os import environ as env\nenv['FOO'] = '1'",
            "from os import environ\nenviron.update(FOO='1')",
            "import os\nenv = os.environ\nenv['FOO'] = '1'",
            "import os\nos.environb[b'FOO'] = b'1'",
            "import os\nos.putenv('FOO', '1')",
            "import os\nos.unsetenv('FOO')",
            "from os import putenv\nputenv('FOO', '1')",
        ]
        for source in cases:
            with self.subTest(source=source):
                self.assertIn('ML007', self.codes(source, 'engine/python/first_install_runtime.py'))

    def test_reads_and_explicit_env_copies_are_allowed(self):
        cases = [
            "import os\nvalue = os.environ.get('HOME')\nother = os.environ['PATH']",
            "import os\nenv = dict(os.environ)\nenv['HOME'] = '/tmp'\nenv.pop('X', None)",
            "import os\nenv = {**os.environ, 'HOME': '/tmp'}\nenv.update({'Y': '2'})",
            "import os\nenv = os.environ.copy()\ndel env['X']",
            "import os\nenv = {k: v for k, v in os.environ.items()}\nenv['HOME'] = '/tmp'",
        ]
        for source in cases:
            with self.subTest(source=source):
                self.assertEqual(self.codes(source, 'engine/python/first_install_runtime.py'), set())

    def test_allowlisted_entrypoints_and_scoped_worker_helper(self):
        mutation = "import os\nos.environ['COGNEE_API_KEY'] = 'value'"
        for allowed in (
            'official_service_dev.py',
            'generation_handoff_runtime.py',
            'generation_probe.py',
            'first_install_store.py',
            'agent_memory_adapter.py',
            'antigravity_hook_policy.py',
            'project_plugin_policy.py',
        ):
            with self.subTest(allowed=allowed):
                self.assertEqual(self.codes(mutation, 'engine/python/' + allowed), set())

        self.assertIn('ML007', self.codes(mutation, 'engine/python/first_install_runtime.py'))
        self.assertIn('ML007', self.codes(mutation, 'engine/python/project_control_server.py'))

        scoped_ok = """import contextlib, os
@contextlib.contextmanager
def _scoped_improve_cooldown(value):
    prev = os.environ.get('COGNEE_IMPROVE_COOLDOWN')
    os.environ['COGNEE_IMPROVE_COOLDOWN'] = str(value)
    try:
        yield
    finally:
        os.environ.pop('COGNEE_IMPROVE_COOLDOWN', None)
"""
        self.assertEqual(self.codes(scoped_ok, 'engine/python/project_backlog_worker.py'), set())

        scoped_without_finally = """import os
def _scoped_improve_cooldown(value):
    os.environ['COGNEE_IMPROVE_COOLDOWN'] = str(value)
"""
        self.assertIn('ML007', self.codes(scoped_without_finally, 'engine/python/project_backlog_worker.py'))

        unscoped_in_worker = """import os
def process():
    os.environ['COGNEE_IMPROVE_COOLDOWN'] = '300'
"""
        self.assertIn('ML007', self.codes(unscoped_in_worker, 'engine/python/project_backlog_worker.py'))

    def test_lint_main_enforces_ml007_in_engine_python_only(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            engine_dir = root / 'engine/python'
            engine_dir.mkdir(parents=True)
            scripts_dir = root / 'scripts'
            scripts_dir.mkdir(parents=True)
            bad = engine_dir / 'control_worker.py'
            bad.write_text("import os\nos.environ['HOME'] = '/tmp'\n")
            (engine_dir / 'test_control_worker.py').write_text("import os\nos.environ['HOME'] = '/tmp'\n")
            (scripts_dir / 'task.py').write_text("import os\nos.environ['MEMLANE_TASK_OWNER_PID'] = '1'\n")
            output = io.StringIO()
            with patch.object(lint, 'ROOT', root), contextlib.redirect_stdout(output):
                self.assertTrue(lint.main())
                self.assertIn('engine/python/control_worker.py:2: ML007', output.getvalue())
                self.assertNotIn('test_control_worker.py', output.getvalue())
                self.assertNotIn('scripts/task.py', output.getvalue())
                bad.write_text("import os\nenv = dict(os.environ, HOME='/tmp')\n")
                self.assertFalse(lint.main())

    def test_lint_main_recursively_scans_user_profile_providers(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            providers_dir = root / 'engine/python/user_profiles/providers'
            providers_dir.mkdir(parents=True)
            bad = providers_dir / 'sample.py'
            bad.write_text("import os\nos.environ['HOME'] = '/tmp'\n")
            output = io.StringIO()
            with patch.object(lint, 'ROOT', root), contextlib.redirect_stdout(output):
                self.assertTrue(lint.main())
                self.assertIn('engine/python/user_profiles/providers/sample.py:2: ML007', output.getvalue())
                bad.write_text("import os\nenv = dict(os.environ, HOME='/tmp')\n")
                self.assertFalse(lint.main())


if __name__ == '__main__':
    unittest.main()
