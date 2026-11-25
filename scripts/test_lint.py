import unittest
import contextlib
import io
import tempfile
from pathlib import Path
from unittest.mock import patch

import lint
from lint import check_source


class CogneeRunnerLintTests(unittest.TestCase):
    def test_factory_and_parameter_rules_are_path_scoped(self):
        factory = """from cognee.infrastructure.llm.config import LLMConfig
def native_llm_configuration(environment):
    return LLMConfig(**native_llm_kwargs(environment))
"""
        actual = '/outside/repo/engines/cognee/llm_config.py'
        self.assertEqual(check_source(factory, actual), [])
        for path in ('engine/python/llm_config.py', 'engines/other/llm_config.py'):
            self.assertIn('ML001', {code for _, code, _ in check_source(factory, path)})
        defaults = "options['temperature'] = 0\noptions['llm_args'] = configured or {}"
        self.assertEqual({code for _, code, _ in check_source(defaults, actual)}, {'ML002', 'ML003'})

    def test_environment_allowlist_does_not_leak_to_other_runner_paths(self):
        source = "import os\nos.environ['LLM_MODEL'] = 'example'"
        for path in lint.ENGINES_COGNEE_ENVIRON_ALLOWLIST:
            self.assertEqual(check_source(source, '/outside/repo/' + path), [])
        for path in ('engine/python/user_profiles/runner.py', 'engines/other/runner.py',
                     'engines/cognee/not_allowed.py'):
            self.assertEqual({code for _, code, _ in check_source(source, path)}, {'ML007'})

    def test_main_scans_runner_production_files(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory).resolve()
            runner = root / 'engines/cognee'
            runner.mkdir(parents=True)
            source = ("import os\nos.environ['LLM_MODEL'] = 'example'\nLLMConfig()\n"
                      "settings['temperature'] = 0\nsettings['llm_args'] = current or {}\n")
            (runner / 'worker.py').write_text(source)
            (runner / 'test_worker.py').write_text(source)
            (runner / '__pycache__').mkdir()
            (runner / '__pycache__/worker.py').write_text(source)
            console = io.StringIO()
            with patch.object(lint, 'ROOT', root), contextlib.redirect_stdout(console):
                self.assertTrue(lint.main())
            matches = [line for line in console.getvalue().splitlines() if line.startswith('engines/cognee/')]
            self.assertEqual(len(matches), 4)
            for line, code in enumerate(('ML007', 'ML001', 'ML002', 'ML003'), start=2):
                self.assertTrue(any(match.startswith(f'engines/cognee/worker.py:{line}: {code} ')
                                    for match in matches))


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


class PerformanceLintTests(unittest.TestCase):
    def codes(self, source, name='overview_ui.go'):
        return {code for _, code, _ in lint.check_go_source(source, name)}

    def test_animation_frame_restricted_to_motion_tokens_and_reduce_motion(self):
        self.assertIn('ML008', self.codes('func foo(c *ui.Context) { c.AnimationFrame() }', 'overview_ui.go'))
        self.assertIn('ML008', self.codes('func spin(c *ui.Context) { c.AnimationFrame() }', 'motion_tokens.go'))
        ok = 'func spin(c *ui.Context) { if !c.Preferences().ReduceMotion { c.AnimationFrame() } }'
        self.assertEqual(self.codes(ok, 'motion_tokens.go'), set())

    def test_high_frequency_polling_rejected(self):
        self.assertIn('ML009', self.codes('func view(c *ui.Context) { c.After(100 * time.Millisecond) }', 'app.go'))
        self.assertEqual(self.codes('func view(c *ui.Context) { c.After(2 * time.Second) }', 'app.go'), set())

    def test_blocking_io_in_draw_callbacks_and_view_hot_path_rejected(self):
        bad_draw = 'func paint(el ui.Element) { el.Draw(func(p *ui.Painter, r ui.Rect) { os.ReadFile("x") }) }'
        self.assertIn('ML010', self.codes(bad_draw, 'overview_ui.go'))
        bad_view = 'func (a *App) View(c *ui.Context) { time.Sleep(time.Millisecond) }'
        self.assertIn('ML011', self.codes(bad_view, 'app.go'))
        good_draw = 'func paint(el ui.Element) { el.Draw(func(p *ui.Painter, r ui.Rect) { p.Fill(r, tok.Bg, 0) }) }'
        self.assertEqual(self.codes(good_draw, 'overview_ui.go'), set())

    def test_cold_start_overview_guardrails_ml012(self):
        self.assertIn('ML012', self.codes('func x() { a.hydrateOverviewFromCache("s") }', 'overview_ui.go'))
        bad_load = 'func (a *App) loadOverview() { client.overview(ctx) }\n'
        self.assertIn('ML012', self.codes(bad_load, 'overview_ui.go'))
        good_load = 'func (a *App) loadOverview() { control.overviewBootstrap(ctx, 250, 50) }\n'
        self.assertEqual(self.codes(good_load, 'overview_ui.go'), set())
        bad_start = 'func (a *App) Start() { a.RefreshProjectDirectory()\na.RefreshIntegrations() }\n'
        self.assertIn('ML012', self.codes(bad_start, 'app.go'))
        good_start = 'func (a *App) Start() { a.RefreshProjectDirectory()\ntime.AfterFunc(700*time.Millisecond, func() { a.RefreshIntegrations() }) }\n'
        self.assertEqual(self.codes(good_start, 'app.go'), set())


class RuntimeHygieneLintTests(unittest.TestCase):
    def codes(self, source, path='engine/python/project_backlog.py'):
        return {code for _, code, _ in check_source(source, path)}

    def test_ml013_async_background_loop_polling_and_cooperative_yield(self):
        bad_2s = """import asyncio
async def automatic_check_loop():
    while True:
        await asyncio.sleep(2)
"""
        bad_5s = """import asyncio
async def loop():
    while True:
        await asyncio.sleep(5)
"""
        bad_from_import = """from asyncio import sleep
async def worker_loop():
    while True:
        await sleep(0.5)
"""
        for src in (bad_2s, bad_5s, bad_from_import):
            with self.subTest(source=src):
                self.assertIn('ML013', self.codes(src))

        good_yield_and_long_sleep = """import asyncio
async def automatic_tick():
    await asyncio.sleep(0)

async def automatic_check_loop():
    while True:
        await asyncio.sleep(0)
        await asyncio.sleep(15)

async def terminate(deadline):
    while asyncio.get_running_loop().time() < deadline:
        await asyncio.sleep(0.05)
"""
        self.assertEqual(self.codes(good_yield_and_long_sleep), set())

    def test_ml013_dev_main_loop_track_descendants_throttling(self):
        bad_unthrottled = """import time
def main():
    while True:
        track_descendants(roots, descendants)
        time.sleep(0.25)
"""
        self.assertIn('ML013', self.codes(bad_unthrottled, 'scripts/dev.py'))

        bad_if_without_monotonic = """import time
def main(flag):
    while True:
        if flag:
            track_descendants(roots, descendants)
        time.sleep(0.5)
"""
        self.assertIn('ML013', self.codes(bad_if_without_monotonic, 'scripts/dev.py'))

        good_throttled = """import time
def main():
    last_track = 0.0
    while True:
        now = time.monotonic()
        if now - last_track >= 10.0:
            track_descendants(roots, descendants)
            last_track = now
        time.sleep(0.5)
"""
        self.assertEqual(self.codes(good_throttled, 'scripts/dev.py'), set())

    def test_ml014_rejects_sync_subprocess_in_async_def_and_allows_to_thread_or_nested_def(self):
        bad_cases = [
            "import subprocess\nasync def f():\n    subprocess.run(['ps'])",
            "import subprocess\nasync def f():\n    subprocess.check_output(['ps'])",
            "import subprocess\nasync def f():\n    subprocess.call(['ps'])",
            "import subprocess\nasync def f():\n    subprocess.check_call(['ps'])",
            "import subprocess\nasync def f():\n    subprocess.Popen(['ps'])",
            "from subprocess import check_output\nasync def f():\n    check_output(['ps'])",
            "async def snapshot(self, pid):\n    return self.sample_process(pid)",
            "async def snapshot(pid):\n    return default_sample_process(pid)",
        ]
        for src in bad_cases:
            with self.subTest(source=src):
                self.assertIn('ML014', self.codes(src, 'engine/python/service_monitor.py'))

        good_cases = [
            """import asyncio, subprocess
async def execute(pid):
    started = await asyncio.to_thread(subprocess.check_output, ['ps', '-p', str(pid)], text=True)
    proc = await asyncio.create_subprocess_exec('ps', '-p', str(pid))
    return started, proc
""",
            """import asyncio, subprocess
async def snapshot(self, pid):
    def _helper():
        return subprocess.run(['ps', '-p', str(pid)], capture_output=True)
    res1 = await asyncio.to_thread(self.sample_process, pid)
    res2 = await asyncio.to_thread(lambda: self.sample_process(pid))
    res3 = await asyncio.to_thread(_helper)
    return res1, res2, res3
""",
        ]
        for src in good_cases:
            with self.subTest(source=src):
                self.assertEqual(self.codes(src, 'engine/python/service_monitor.py'), set())

    def test_ml015_rejects_binary_read_bytes_hash_and_missing_owner_path_unlink(self):
        bad_node_hash = """import hashlib
def verify(node):
    return hashlib.sha256(node.read_bytes()).hexdigest()
"""
        self.assertIn('ML015', self.codes(bad_node_hash, 'engine/python/opencode_backlog.py'))

        bad_exec_hash = """from hashlib import sha256
def verify(executable):
    return sha256(executable.read_bytes()).hexdigest()
"""
        self.assertIn('ML015', self.codes(bad_exec_hash, 'engine/python/worker.py'))

        bad_execute_without_unlink = """async def execute(cancel_file, process):
    owner_path = cancel_file
    try:
        await process.wait()
    finally:
        private_write(owner_path, b'{}')
"""
        self.assertIn('ML015', self.codes(bad_execute_without_unlink, 'engine/python/opencode_backlog.py'))

        good_opencode_backlog = """import hashlib
def verify(manifest_path):
    return hashlib.sha256(manifest_path.read_bytes()).hexdigest()

async def execute(cancel_file, process, action):
    owner_path = cancel_file
    try:
        await process.wait()
    finally:
        if action in ('review', 'check') and process.returncode is not None:
            owner_path.unlink(missing_ok=True)
"""
        self.assertEqual(self.codes(good_opencode_backlog, 'engine/python/opencode_backlog.py'), set())

    def test_ml016_requires_journal_size_limit_with_wal_mode(self):
        bad_wal = """import sqlite3
def open_db(path):
    db = sqlite3.connect(path)
    db.execute('PRAGMA journal_mode=WAL')
    db.execute('PRAGMA synchronous=FULL')
    return db
"""
        self.assertIn('ML016', self.codes(bad_wal, 'engine/python/external_capture_store.py'))

        good_wal = """import sqlite3
def open_db(path):
    db = sqlite3.connect(path)
    db.execute('PRAGMA journal_mode=WAL')
    db.execute('PRAGMA journal_size_limit=4194304')
    db.execute('PRAGMA synchronous=FULL')
    return db
"""
        self.assertEqual(self.codes(good_wal, 'engine/python/external_capture_store.py'), set())

    def test_ml017_dev_reopen_ui_watch_guard_and_disk_cleanup_receipt_rotation(self):
        bad_reopen_ui = """def reopen_ui(current, processes, launch, config_path):
    return launch([str(GO), 'tool', 'mygo', 'dev'], env=ui_env(config_path))
"""
        self.assertIn('ML017', self.codes(bad_reopen_ui, 'scripts/dev.py'))

        good_reopen_ui_guarded = """def reopen_ui(current, processes, launch, config_path, *, watch_ui=False):
    if watch_ui:
        return launch([str(GO), 'tool', 'mygo', 'dev'], env=ui_env(config_path))
    return launch([str(BINARY)], env=ui_env(config_path))
"""
        self.assertEqual(self.codes(good_reopen_ui_guarded, 'scripts/dev.py'), set())

        good_reopen_ui_direct_binary = """def reopen_ui(current, processes, launch, config_path):
    return launch([str(BINARY)], env=ui_env(config_path))
"""
        self.assertEqual(self.codes(good_reopen_ui_direct_binary, 'scripts/dev.py'), set())

        bad_disk_space = """def prune_history(p):
    p.unlink()

def main(ROOT, report):
    log = ROOT / 'build/disk-cleanup'
    (log / '20261121-120000.json').write_text('{}')
"""
        self.assertIn('ML017', self.codes(bad_disk_space, 'scripts/disk-space.py'))

        good_disk_space = """def prune_cleanup_receipts(log_dir, keep=100):
    for old in sorted(log_dir.glob('*.json'))[:-keep]:
        old.unlink(missing_ok=True)

def main(ROOT, report):
    log = ROOT / 'build/disk-cleanup'
    (log / '20261121-120000.json').write_text('{}')
    prune_cleanup_receipts(log)
"""
        self.assertEqual(self.codes(good_disk_space, 'scripts/disk-space.py'), set())

    def test_hardened_ml001_to_ml003_ml007_coverage(self):
        bad_param_cases = [
            "request(temperature=configured or 0.7)",
            "fields['temperature'] = x if x else 0.7",
            "request(temperature=x if x else 0.7)",
            "fields.pop('LLM_TEMPERATURE', 0.7)",
            "temperature = 0.7",
            "class C:\n    temperature: float = 0.7",
            "def f(temperature=0.7):\n    pass",
            "def f(*, max_tokens=100):\n    pass",
            "fields = {'temperature': 0.5 + 0.2}",
            "class Sub(LLMConfig):\n    pass\nSub()",
            "Cfg: type = LLMConfig\nCfg()",
        ]
        for src in bad_param_cases:
            with self.subTest(bad_param=src):
                codes = {c for _, c, _ in check_source(src, 'engine/python/generation_handoff_runtime.py')}
                self.assertTrue(codes & {'ML001', 'ML002', 'ML003'}, (src, codes))

        good_param_cases = [
            "temperature = None",
            "def f(temperature=None):\n    pass",
            "fields.pop('LLM_TEMPERATURE', None)",
            "request(temperature=None)",
            "from x import LLMConfig as Cfg\ndef f(Cfg):\n    return Cfg()",
        ]
        for src in good_param_cases:
            with self.subTest(good_param=src):
                self.assertEqual(check_source(src, 'engine/python/generation_handoff_runtime.py'), [])

        for src in (
            "import os\nos.environ.__setitem__('FOO', '1')",
            "import os\nos.environ.__delitem__('FOO')",
        ):
            self.assertIn('ML007', {c for _, c, _ in check_source(src, 'engine/python/project_control_server.py')})

        dummy_try_bypass = """import os
def _scoped_improve_cooldown(value):
    os.environ['HOME'] = '/evil'
    try:
        pass
    finally:
        pass
"""
        self.assertIn('ML007', {c for _, c, _ in check_source(dummy_try_bypass, 'engine/python/project_backlog_worker.py')})

        wrong_key_in_try = """import os
def _scoped_improve_cooldown(value):
    os.environ['HOME'] = '/evil'
    try:
        yield
    finally:
        os.environ.pop('COGNEE_IMPROVE_COOLDOWN', None)
"""
        self.assertIn('ML007', {c for _, c, _ in check_source(wrong_key_in_try, 'engine/python/project_backlog_worker.py')})

        shadowed_env = """from os import environ as env
def unrelated(env):
    env['HOME'] = '/tmp'
"""
        self.assertEqual(check_source(shadowed_env, 'engine/python/project_control_server.py'), [])

    def test_hardened_go_lexer_and_real_api_coverage(self):
        self.assertIn('ML008', {c for _, c, _ in lint.check_go_source('func f(c *ui.Context) { c.AnimationFrame /* bypass */ () }', 'overview_ui.go')})
        self.assertIn('ML008', {c for _, c, _ in lint.check_go_source('func spin(c *ui.Context) { s := "ReduceMotion"; _ = s; c.AnimationFrame() }', 'motion_tokens.go')})
        self.assertIn('ML008', {c for _, c, _ in lint.check_go_source('func spin(c *ui.Context) { if c.Preferences().ReduceMotion { c.AnimationFrame() } }', 'motion_tokens.go')})

        self.assertEqual(lint.check_go_source('func f(c *ui.Context) { <-time.After(100 * time.Millisecond) }', 'app.go'), [])
        self.assertIn('ML009', {c for _, c, _ in lint.check_go_source('func f(c *ui.Context) { c.After(time.Millisecond) }', 'app.go')})

        rune_escape = """func f(el ui.Element, r rune) {
    el.Draw(func(p *ui.Painter, rc ui.Rect) {
        if r == '}' {}
        os.ReadFile("secret")
    })
}"""
        self.assertIn('ML010', {c for _, c, _ in lint.check_go_source(rune_escape, 'overview_ui.go')})

        raw_and_str_ok = 'func f() { var m = "overview-ui-cache"; var r = `c.AnimationFrame()\nc.After(10*time.Millisecond)`; _, _ = m, r }'
        self.assertEqual(lint.check_go_source(raw_and_str_ok, 'app.go'), [])

        self.assertIn('ML010', {c for _, c, _ in lint.check_go_source('func f(el ui.Element) { el.Draw(func(p *ui.Painter, r ui.Rect) { hc.Do(req) }) }', 'overview_ui.go')})
        self.assertIn('ML010', {c for _, c, _ in lint.check_go_source('func f(el ui.Element) { el.DrawOver(func(p *ui.Painter, r ui.Rect) { os.OpenFile("x", 0, 0600) }) }', 'overview_ui.go')})
        self.assertIn('ML011', {c for _, c, _ in lint.check_go_source('func (app *App) View(c *ui.Context) { http.NewRequestWithContext(ctx, "GET", "/", nil) }\nvar extra = 1', 'app.go')})
        self.assertEqual(lint.check_go_source('func (a *App) View(c *ui.Context) {}\nvar helper = func() { os.ReadFile("x") }', 'app.go'), [])

        bad_early_start = 'func (a *App) Start() { a.RefreshIntegrations()\na.RefreshProjectDirectory() }'
        self.assertIn('ML012', {c for _, c, _ in lint.check_go_source(bad_early_start, 'app.go')})
        good_early_return_start = 'func (a *App) Start() { if a.Config.GenerationSetupOnly { a.RefreshIntegrations(); return }\na.RefreshProjectDirectory() }'
        self.assertEqual(lint.check_go_source(good_early_return_start, 'app.go'), [])

        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / 'engine/python').mkdir(parents=True)
            go_dir = root / 'internal/simpledesktop'
            go_dir.mkdir(parents=True)
            bad_go = go_dir / 'overview_ui.go'
            bad_go.write_text('package simpledesktop\nfunc f(c *ui.Context) { c.AnimationFrame() }\n')
            (go_dir / 'overview_ui_test.go').write_text('package simpledesktop\nfunc TestX(c *ui.Context) { c.AnimationFrame() }\n')
            output = io.StringIO()
            with patch.object(lint, 'ROOT', root), contextlib.redirect_stdout(output):
                self.assertTrue(lint.main())
                self.assertIn('internal/simpledesktop/overview_ui.go:2: ML008', output.getvalue())
                self.assertNotIn('overview_ui_test.go', output.getvalue())
                bad_go.write_text('package simpledesktop\n')
                self.assertFalse(lint.main())

    def test_main_checks_kit_ui_performance(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / 'engine/python').mkdir(parents=True)
            ui_dir = root / 'kit/ui'
            nested_dir = ui_dir / 'nested'
            nested_dir.mkdir(parents=True)
            bad_sources = {
                ui_dir / 'controls.go': (
                    'func f(c *mygoui.Context) { c.AnimationFrame() }\n'
                    'func poll(c *mygoui.Context) { c.After(time.Millisecond, callback) }\n'
                ),
                ui_dir / 'motion_tokens.go': 'func spin(c *mygoui.Context) { c.AnimationFrame() }\n',
                nested_dir / 'draw.go': (
                    'func f(el mygoui.Element) { el.Draw(func(p *mygoui.Painter, r mygoui.Rect) { time.Sleep(1) }) }\n'
                ),
            }
            for path, source in bad_sources.items():
                path.write_text('package ui\n' + source)
            (ui_dir / 'ui_test.go').write_text('package ui\n' + bad_sources[ui_dir / 'controls.go'])
            output = io.StringIO()
            with patch.object(lint, 'ROOT', root), contextlib.redirect_stdout(output):
                self.assertTrue(lint.main())
            for diagnostic in (
                'kit/ui/controls.go:2: ML008',
                'kit/ui/controls.go:3: ML009',
                'kit/ui/motion_tokens.go:2: ML008',
                'kit/ui/nested/draw.go:2: ML010',
            ):
                self.assertIn(diagnostic, output.getvalue())
            self.assertNotIn('kit/ui/ui_test.go', output.getvalue())

    def test_hardened_ml013_to_ml017_coverage(self):
        self.assertIn('ML013', self.codes("import asyncio\nasync def loop():\n    while 1:\n        await asyncio.sleep(delay=1)"))
        self.assertIn('ML013', self.codes("import asyncio\nPOLL = 0.5\nasync def loop():\n    while True:\n        await asyncio.sleep(POLL)"))
        self.assertEqual(self.codes("from asyncio import sleep\nasync def ok(sleep):\n    while True:\n        await sleep(1)"), set())

        dev_unthrottled_helper = """import time
MONITOR_POLL_INTERVAL = 0.5
def run_monitor_housekeeping(roots, descendants):
    track_descendants(roots, descendants)
def main(roots, descendants):
    while True:
        run_monitor_housekeeping(roots, descendants)
        time.sleep(MONITOR_POLL_INTERVAL)
"""
        self.assertIn('ML013', self.codes(dev_unthrottled_helper, 'scripts/dev.py'))

        dev_throttled_helper = """import time
MONITOR_POLL_INTERVAL = 0.5
def run_monitor_housekeeping(roots, descendants, *, last_run=None, interval=10.0):
    now = time.monotonic()
    if last_run is not None and (now - last_run) < interval:
        return last_run
    track_descendants(roots, descendants)
    return now
def main(roots, descendants):
    while True:
        run_monitor_housekeeping(roots, descendants)
        time.sleep(MONITOR_POLL_INTERVAL)
"""
        self.assertEqual(self.codes(dev_throttled_helper, 'scripts/dev.py'), set())

        dev_else_branch = """import time
def main(roots, descendants):
    last = 0.0
    while True:
        if time.monotonic() - last < 10:
            pass
        else:
            track_descendants(roots, descendants)
        time.sleep(0.5)
"""
        self.assertEqual(self.codes(dev_else_branch, 'scripts/dev.py'), set())

        executor_and_wait_ok = """import asyncio, subprocess
async def ok(self, pid):
    loop = asyncio.get_running_loop()
    a = await loop.run_in_executor(None, lambda: subprocess.run(['ps']))
    b = await asyncio.wait_for(self.sample_process(pid), 2)
    return a, b
"""
        self.assertEqual(self.codes(executor_and_wait_ok, 'engine/python/service_monitor.py'), set())
        self.assertIn('ML014', self.codes("import os\nasync def f():\n    os.system('ps')"))
        self.assertIn('ML014', self.codes("import subprocess\ndef _ps():\n    return subprocess.run(['ps'])\nasync def f():\n    return _ps()"))

        split_read_bytes = """import hashlib
def verify(executable):
    raw = executable.read_bytes()
    return hashlib.sha256(raw).hexdigest()
"""
        self.assertIn('ML015', self.codes(split_read_bytes, 'engine/python/worker.py'))
        self.assertIn('ML015', self.codes("import hashlib\ndef verify(node):\n    return hashlib.sha512(data=node.read_bytes()).hexdigest()", 'engine/python/worker.py'))

        dead_unlink = """async def execute(cancel_file, process):
    owner_path = cancel_file
    try:
        await process.wait()
    finally:
        if False:
            owner_path.unlink()
"""
        self.assertIn('ML015', self.codes(dead_unlink, 'engine/python/opencode_backlog.py'))

        helper_unlink = """def _cleanup_owner(p):
    p.unlink(missing_ok=True)
async def execute(cancel_file, process):
    owner_path = cancel_file
    try:
        await process.wait()
    finally:
        _cleanup_owner(owner_path)
"""
        self.assertEqual(self.codes(helper_unlink, 'engine/python/opencode_backlog.py'), set())

        doc_only_wal = '''"""Example note: PRAGMA journal_mode=WAL."""
def open_db():
    return None
'''
        self.assertEqual(self.codes(doc_only_wal, 'engine/python/external_capture_store.py'), set())
        self.assertIn('ML016', self.codes("def open_db(db):\n    db.execute(\"PRAGMA main.journal_mode = 'WAL'\")", 'engine/python/external_capture_store.py'))
        self.assertIn('ML016', self.codes("def open_db(db):\n    db.execute('PRAGMA ' + 'journal_mode=WAL')", 'engine/python/external_capture_store.py'))

        watch_bypass = """def reopen_ui(current, processes, launch, config_path, *, watch_ui=False):
    if watch_ui:
        pass
    return launch([str(GO), 'tool', 'mygo', 'dev'], env=ui_env(config_path))
"""
        self.assertIn('ML017', self.codes(watch_bypass, 'scripts/dev.py'))

        watch_not_else = """def reopen_ui(current, processes, launch, config_path, *, watch_ui=False):
    if not watch_ui:
        return launch([str(BINARY)], env=ui_env(config_path))
    else:
        return launch((str(GO), 'tool', 'mygo', 'dev'), env=ui_env(config_path))
"""
        self.assertEqual(self.codes(watch_not_else, 'scripts/dev.py'), set())

        fake_unlink_disk = """def drop_tmp(p):
    p.unlink()
def main(ROOT):
    log = ROOT / 'build/disk-cleanup'
    (log / '20261121-120000.json').write_text('{}')
    drop_tmp(ROOT / 'x')
"""
        self.assertIn('ML017', self.codes(fake_unlink_disk, 'scripts/disk-space.py'))

        two_hop_disk = """def _unlink_one(p):
    p.unlink(missing_ok=True)
def prune_cleanup_receipts(log_dir):
    for p in log_dir.glob('*.json'):
        _unlink_one(p)
def note(p):
    p.write_text('ok')
def main(ROOT):
    log = ROOT / 'build/disk-cleanup'
    with open(log / 'r.json', 'w') as fh:
        fh.write('{}')
    prune_cleanup_receipts(log)
"""
        self.assertEqual(self.codes(two_hop_disk, 'scripts/disk-space.py'), set())

    def test_ml006_and_ml015_scripts_coverage(self):
        bad_profile_wait_for = """import asyncio
async def generate(adapter, row):
    return await asyncio.wait_for(collect(), timeout=60)
"""
        self.assertIn('ML006', self.codes(bad_profile_wait_for, 'engine/python/user_profiles/runner.py'))

        bad_profile_timeout = """import asyncio
async def generate(adapter, row):
    async with asyncio.timeout(60):
        return await collect()
"""
        self.assertIn('ML006', self.codes(bad_profile_timeout, 'engine/python/user_profiles/runner.py'))

        good_profile_heartbeat = """import asyncio
async def generate(adapter, row):
    while not collection.done():
        await asyncio.wait({collection}, timeout=1)
    return await collection
"""
        self.assertEqual(self.codes(good_profile_heartbeat, 'engine/python/user_profiles/runner.py'), set())

        bad_script_archive_hash = """import hashlib
def main(archive):
    return hashlib.sha256(archive.read_bytes()).hexdigest()
"""
        self.assertIn('ML015', self.codes(bad_script_archive_hash, 'scripts/bootstrap.py'))

        good_script_chunked_hash = """import hashlib
def main(archive):
    h = hashlib.sha256()
    with archive.open('rb') as stream:
        while chunk := stream.read(1024 * 1024):
            h.update(chunk)
    return h.hexdigest()
"""
        self.assertEqual(self.codes(good_script_chunked_hash, 'scripts/bootstrap.py'), set())


class DocsProcessBoundaryLintTests(unittest.TestCase):
    def test_ml018_rejects_process_dir_references_and_allows_technical_prose(self):
        bad_cases = [
            '实测脚本保存在 `docs/process/native-perf-20261122/` 目录。',
            '参考 `docs/process` 下的截图。',
            '复验截图见 [verify](../process/visual-v4-prototype-shots/verify/)。',
            '深层引用 [log](../../process/run1.log)。',
            '同级引用 [report](process/report.md) 或 [report](./process/report.md)。',
            '绝对链接 [shot](file:///Users/dev/memlane/docs/process/a.png)。',
        ]
        for line in bad_cases:
            with self.subTest(line=line):
                issues = lint.check_markdown_doc('# Title\n' + line + '\n', 'docs/research/sample.md')
                self.assertEqual([(no, code) for no, code, _ in issues], [(2, 'ML018')])

        good_cases = [
            '# 架构说明\n采用 in-process/out-of-process 隔离与多 process/worker 调度。',
            '详见 [通知处理](../../engine/python/processing_notices.py) 与 `../processing_notices.py`。',
            '正式截图位于 [overview](../images/visual-v4-20261120/overview-light.png)。',
        ]
        for text in good_cases:
            with self.subTest(text=text):
                self.assertEqual(lint.check_markdown_doc(text, 'docs/architecture/overview.md'), [])

    def test_lint_main_scans_formal_docs_and_skips_index_process_and_archive(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / 'engine/python').mkdir(parents=True)
            for rel in ('docs/research', 'docs/design', 'docs/process/demo', 'docs/archive'):
                (root / rel).mkdir(parents=True)

            # Allowed routing indexes and ignored/archived trees:
            (root / 'docs/README.md').write_text('所有临时产物放 [process/](process/README.md) 即 `docs/process/`。\n')
            (root / 'docs/process/README.md').write_text('本目录 `docs/process/` 不入库。\n')
            (root / 'docs/process/demo/note.md').write_text('本地草稿 `docs/process/demo/run.log`。\n')
            (root / 'docs/archive/old.md').write_text('历史归档 `docs/process/old/`。\n')

            bad_research = root / 'docs/research/perf.md'
            bad_research.write_text('# Perf\n详见 `docs/process/perf/timeline.txt`。\n')
            bad_design = root / 'docs/design/spec.md'
            bad_design.write_text('# Spec\n截图 [shots](../process/shots/)。\n')

            output = io.StringIO()
            with patch.object(lint, 'ROOT', root), contextlib.redirect_stdout(output):
                self.assertTrue(lint.main())
                rendered = output.getvalue()
                self.assertIn('docs/research/perf.md:2: ML018', rendered)
                self.assertIn('docs/design/spec.md:2: ML018', rendered)
                self.assertNotIn('docs/README.md', rendered)
                self.assertNotIn('docs/process/', rendered.split('ML018')[0])
                self.assertNotIn('docs/archive/old.md', rendered)

                bad_research.write_text('# Perf\n所有结论已内聚于本文正文。\n')
                bad_design.write_text('# Spec\n正式截图见 [shots](../images/shots.png)。\n')
                self.assertFalse(lint.main())


class ScalingGuardrailLintTests(unittest.TestCase):
    def py_codes(self, source, path):
        return {code for _, code, _ in check_source(source, path)}

    def go_codes(self, source, name):
        return {code for _, code, _ in lint.check_go_source(source, name)}

    def test_ml020_enforces_v2_list_limit_byte_budget_and_utf8_exit(self):
        bad_missing_budget = """_MAX_PAGE_LIMIT = 50
def attach(app):
    async def list_items(request):
        limit = _parse_limit(request.query.get('limit'))
        return web.json_response({'items': []})
    app.add_routes([web.get('/v1/projects/{id}/knowledge', list_items)])
"""
        self.assertIn('ML020', self.py_codes(bad_missing_budget, 'engine/python/v2_api.py'))

        bad_missing_max_const = """def attach(app):
    async def list_items(request):
        limit = _parse_limit(request.query.get('limit'))
        payload, _, _ = fit_items_to_json_budget(fn, [])
        return web.json_response(payload)
    app.add_routes([web.get('/v1/projects/{id}/knowledge', list_items)])
"""
        self.assertIn('ML020', self.py_codes(bad_missing_max_const, 'engine/python/v2_api.py'))

        bad_high_limit = """_MAX_PAGE_LIMIT = 200
def attach(app):
    async def list_items(request):
        limit = _parse_limit(request.query.get('limit'))
        payload, _, _ = fit_items_to_json_budget(fn, [])
        return web.json_response(payload)
    app.add_routes([web.get('/v1/projects/{id}/knowledge', list_items)])
"""
        self.assertIn('ML020', self.py_codes(bad_high_limit, 'engine/python/v2_api.py'))

        bad_positional_max_limit = """_MAX_PAGE_LIMIT = 50
def attach(app):
    async def list_items(request):
        limit = _parse_limit(request.query.get('limit'), 30, 100)
        payload, _, _ = fit_items_to_json_budget(fn, [])
        return web.json_response(payload)
    app.router.add_get('/v1/projects/{id}/knowledge', list_items)
"""
        self.assertIn('ML020', self.py_codes(bad_positional_max_limit, 'engine/python/v2_api.py'))

        bad_dead_budget = """_MAX_PAGE_LIMIT = 50
def attach(app):
    async def list_items(request):
        limit = _parse_limit(request.query.get('limit'))
        if False:
            fit_items_to_json_budget(fn, [])
        return web.json_response({'items': []})
    app.router.add_route('GET', '/v1/projects/{id}/knowledge', list_items)
"""
        self.assertIn('ML020', self.py_codes(bad_dead_budget, 'engine/python/v2_api.py'))

        # Once _json_utf8_response or json_utf8_response is present, bare web.json_response on list routes fails ML020.
        bad_bare_json_response_with_helper = """_MAX_PAGE_LIMIT = 50
def _json_utf8_response(raw_bytes):
    return web.Response(body=raw_bytes, content_type='application/json', charset='utf-8')
def attach(app):
    async def list_items(request):
        limit = _parse_limit(request.query.get('limit'))
        payload, _, _ = fit_items_to_json_budget(fn, [])
        return web.json_response(payload)
    app.add_routes([web.get('/v1/projects/{id}/knowledge', list_items)])
"""
        self.assertIn('ML020', self.py_codes(bad_bare_json_response_with_helper, 'engine/python/v2_api.py'))

        bad_ascii_dumps_with_imported_helper = """import json
from local_body_reader import json_utf8_response
_MAX_PAGE_LIMIT = 50
def attach(app):
    async def list_items(request):
        limit = _parse_limit(request.query.get('limit'))
        payload, _, _ = fit_items_to_json_budget(fn, [])
        return web.json_response(payload, dumps=json.dumps)
    app.add_routes([web.get('/v1/projects/{id}/knowledge', list_items)])
"""
        self.assertIn('ML020', self.py_codes(bad_ascii_dumps_with_imported_helper, 'engine/python/v2_api.py'))

        bad_indirect_helper_and_partial_without_utf8 = """import functools, json
_MAX_PAGE_LIMIT = 50
_BAD_DUMPS = functools.partial(json.dumps, separators=(',', ':'))
def _json_utf8_response(raw_bytes):
    return web.Response(body=raw_bytes, content_type='application/json', charset='utf-8')
def _send(payload):
    return web.json_response(payload, dumps=_BAD_DUMPS)
def attach(app):
    async def list_items(request):
        limit = _parse_limit(request.query.get('limit'))
        payload, _, _ = fit_items_to_json_budget(fn, [])
        return _send(payload)
    app.add_routes([web.get('/v1/projects/{id}/knowledge', list_items)])
"""
        self.assertIn('ML020', self.py_codes(bad_indirect_helper_and_partial_without_utf8, 'engine/python/v2_api.py'))

        # Direct check_v2_utf8_response(require_unconditional=True) catches bare web.json_response even without helper.
        legacy_bare = """_MAX_PAGE_LIMIT = 50
def attach(app):
    async def list_items(request):
        limit = _parse_limit(request.query.get('limit'))
        payload, _, _ = fit_items_to_json_budget(fn, [])
        return web.json_response(payload)
    app.add_routes([web.get('/v1/projects/{id}/knowledge', list_items)])
"""
        uncond_issues = lint.check_v2_utf8_response(legacy_bare, require_unconditional=True)
        self.assertTrue(any(code == 'ML020' for _, code, _ in uncond_issues))

        # Unified UTF-8 response paths pass cleanly.
        good_utf8 = """import functools, json
_MAX_PAGE_LIMIT = 50
def _json_utf8_response(raw_bytes):
    return web.Response(body=raw_bytes, content_type='application/json', charset='utf-8')
def attach(app):
    async def list_items(request):
        limit = _parse_limit(request.query.get('limit'))
        payload, _, _ = fit_items_to_json_budget(fn, [])
        return _json_utf8_response(payload)
    async def list_items_partial(request):
        limit = _parse_limit(request.query.get('limit'))
        payload, _, _ = fit_items_to_json_budget(fn, [])
        return web.json_response(payload, dumps=functools.partial(json.dumps, ensure_ascii=False))
    async def doc_body(request):
        return web.json_response({})
    app.add_routes([
        web.get('/v1/projects/{id}/knowledge', list_items),
        web.get('/v1/common/knowledge', list_items_partial),
        web.get('/v1/datasets/{ds}/documents/{doc}/body', doc_body),
    ])
"""
        self.assertEqual(self.py_codes(good_utf8, 'engine/python/v2_api.py'), set())
        self.assertEqual(lint.check_v2_utf8_response(good_utf8, require_unconditional=True), [])

        # Extracted engine/python/v2_api_*.py domain modules are checked directly by ML020 UTF-8 guardrail.
        bad_domain_resp = """from aiohttp import web
def handle(payload):
    return web.json_response(payload)
"""
        self.assertIn('ML020', self.py_codes(bad_domain_resp, 'engine/python/v2_api_knowledge.py'))
        good_domain_resp = """from local_body_reader import json_utf8_response
def handle(payload, raw_bytes):
    return json_utf8_response(payload, raw_bytes=raw_bytes)
"""
        self.assertEqual(self.py_codes(good_domain_resp, 'engine/python/v2_api_knowledge.py'), set())

    def test_ml021_sqlite_connect_allowlist_aliases_importlib_and_immutable_ban(self):
        # Direct calls & basename spoofing rejected outside exact allowlisted relative paths.
        bad_cases = [
            ("import sqlite3\ndef f(p):\n    return sqlite3.connect(p)\n", 'engine/python/new_reader.py'),
            ("from sqlite3 import connect\ndef f(p):\n    return connect(p)\n", 'engine/python/new_reader.py'),
            ("import sqlite3\ndef f(p):\n    return sqlite3.connect(p)\n", 'engine/python/sub/sqlite_ro.py'),
            ("import sqlite3\ndef f(p):\n    return sqlite3.connect(p)\n", 'scripts/sqlite_ro.py'),
            ("import sqlite3\ndef f(p):\n    s = sqlite3\n    return s.connect(p)\n", 'engine/python/new_reader.py'),
            ("import sqlite3\ndef f(p):\n    fn = sqlite3.connect\n    return fn(p)\n", 'engine/python/new_reader.py'),
            ("import sqlite3\ndef f(p):\n    s = sqlite3\n    fn = s.connect\n    return fn(p)\n", 'engine/python/new_reader.py'),
            ("import sqlite3\ndef f(p):\n    return getattr(sqlite3, 'connect')(p)\n", 'engine/python/new_reader.py'),
            ("import sqlite3\ndef f(p):\n    fn = getattr(sqlite3, 'connect')\n    return fn(p)\n", 'engine/python/new_reader.py'),
            ("import importlib\ndef f(p):\n    return importlib.import_module('sqlite3').connect(p)\n", 'engine/python/new_reader.py'),
            ("from importlib import import_module as im\ndef f(p):\n    s = im('sqlite3')\n    return s.connect(p)\n", 'engine/python/new_reader.py'),
            ("def f(p):\n    return __import__('sqlite3').connect(p)\n", 'engine/python/new_reader.py'),
            ("def f(p):\n    s = __import__('sqlite3')\n    return getattr(s, 'connect')(p)\n", 'engine/python/new_reader.py'),
            ("import asyncio, sqlite3\nasync def f(p):\n    return await asyncio.to_thread(sqlite3.connect, p)\n", 'engine/python/new_reader.py'),
            ("import sys\ndef f(p):\n    return sys.modules['sqlite3'].connect(p)\n", 'engine/python/new_reader.py'),
        ]
        for src, path in bad_cases:
            with self.subTest(path=path, src=src):
                self.assertIn('ML021', self.py_codes(src, path))

        # Exact allowlisted paths and benign sqlite3 exception handling / parameter shadowing pass.
        self.assertEqual(self.py_codes("import sqlite3\ndef f(p):\n    return sqlite3.connect(p)\n", 'engine/python/sqlite_ro.py'), set())
        self.assertEqual(self.py_codes("import sqlite3\ndef f(p):\n    return sqlite3.connect(p)\n", 'scripts/bench_scaling.py'), set())
        self.assertEqual(self.py_codes("import sqlite3\ndef f():\n    return sqlite3.Error\n", 'engine/python/new_reader.py'), set())
        self.assertEqual(self.py_codes("import sqlite3\ndef f(sqlite3):\n    return sqlite3.connect('x')\n", 'engine/python/new_reader.py'), set())

        # immutable=1 banned everywhere outside docstrings (including constant folding).
        self.assertIn('ML021', self.py_codes("def f(p):\n    return f'file:{p}?mode=ro&immutable=1'\n", 'engine/python/sqlite_ro.py'))
        self.assertIn('ML021', self.py_codes("FLAG = 'immutable'\ndef f(p):\n    return f'file:{p}?{FLAG}=1'\n", 'engine/python/sqlite_ro.py'))
        self.assertEqual(self.py_codes('"""Never use immutable=1 online."""\ndef f():\n    return 1\n', 'engine/python/sqlite_ro.py'), set())

    def test_ml022_catalog_index_balanced_ddl_alter_and_table_xinfo_whitelist(self):
        # Injecting forbidden columns AFTER CHECK (kind IN (...)) must be caught 100%.
        for bad_col in ('body', 'why_learned', 'system_metadata', 'raw_text', 'content', 'external_metadata'):
            src = f"""SQL = '''
                CREATE TABLE IF NOT EXISTS catalog_docs (
                    dataset_id TEXT NOT NULL,
                    doc_id TEXT NOT NULL,
                    kind TEXT NOT NULL CHECK (kind IN ('knowledge', 'common', 'raw_doc')),
                    common_category TEXT NOT NULL DEFAULT '' CHECK (common_category IN ('', 'preference', 'experience')),
                    {bad_col} TEXT NOT NULL DEFAULT '',
                    PRIMARY KEY (dataset_id, doc_id)
                );
            '''\n"""
            with self.subTest(after_check_col=bad_col):
                self.assertIn('ML022', self.py_codes(src, 'engine/python/catalog_index.py'))

        # ALTER TABLE ADD COLUMN injecting forbidden or unlisted columns must be caught.
        for alter_sql in (
            "con.execute('ALTER TABLE catalog_docs ADD COLUMN raw_text TEXT')",
            "con.execute('ALTER TABLE catalog_docs ADD COLUMN body TEXT')",
            "con.execute('ALTER TABLE catalog_sessions ADD COLUMN qa_payload TEXT')",
            "con.execute('ALTER TABLE dataset_sync_state ADD COLUMN unknown_col TEXT')",
            "con.execute('ALTER TABLE rogue_table ADD COLUMN id TEXT')",
        ):
            with self.subTest(alter=alter_sql):
                self.assertIn('ML022', self.py_codes(alter_sql + '\n', 'engine/python/catalog_index.py'))

        # catalog_sessions with QA/trace/body columns must be rejected.
        for bad_sess_col in ('qa_payload', 'trace_payload', 'question', 'answer', 'body', 'unlisted_col'):
            sess_bad = f"""SQL = '''
                CREATE TABLE IF NOT EXISTS catalog_sessions (
                    dataset_id TEXT NOT NULL,
                    session_id TEXT NOT NULL,
                    {bad_sess_col} TEXT NOT NULL DEFAULT '',
                    PRIMARY KEY (dataset_id, session_id)
                );
            '''\n"""
            with self.subTest(bad_sess_col=bad_sess_col):
                self.assertIn('ML022', self.py_codes(sess_bad, 'engine/python/catalog_index.py'))

        # Any 6th table outside the 5-table whitelist or virtual table / CTAS must be rejected.
        self.assertIn('ML022', self.py_codes("SQL = 'CREATE TABLE IF NOT EXISTS catalog_extra (id TEXT)'\n", 'engine/python/catalog_index.py'))
        self.assertIn('ML022', self.py_codes("SQL = 'CREATE VIRTUAL TABLE catalog_fts USING fts5(doc_label)'\n", 'engine/python/catalog_index.py'))
        self.assertIn('ML022', self.py_codes("SQL = 'CREATE TABLE catalog_docs AS SELECT 1 AS doc_id'\n", 'engine/python/catalog_index.py'))

        # Generated column caught by PRAGMA table_xinfo, second CREATE TABLE IF NOT EXISTS not shadowed, split concat caught.
        gen_col = "SQL = '''CREATE TABLE catalog_docs (doc_id TEXT, body TEXT GENERATED ALWAYS AS (doc_id) VIRTUAL)'''\n"
        self.assertIn('ML022', self.py_codes(gen_col, 'engine/python/catalog_index.py'))
        shadowed_second = """SQL1 = 'CREATE TABLE IF NOT EXISTS catalog_docs (dataset_id TEXT, doc_id TEXT)'
SQL2 = 'CREATE TABLE IF NOT EXISTS catalog_docs (dataset_id TEXT, doc_id TEXT, body TEXT)'
"""
        self.assertIn('ML022', self.py_codes(shadowed_second, 'engine/python/catalog_index.py'))
        split_concat = """A = 'CREATE TABLE IF NOT EXISTS catalog_docs (dataset_id TEXT, '
B = 'why_learned TEXT)'
SQL = A + B
"""
        self.assertIn('ML022', self.py_codes(split_concat, 'engine/python/catalog_index.py'))
        join_concat = """SQL = '\\n'.join(['CREATE TABLE IF NOT EXISTS catalog_docs (dataset_id TEXT,', 'body TEXT)'])
"""
        self.assertIn('ML022', self.py_codes(join_concat, 'engine/python/catalog_index.py'))

        # All 5 allowed metadata tables + allowed ALTER migrations pass cleanly.
        good_five_tables = """SQL = '''
            CREATE TABLE IF NOT EXISTS catalog_meta (
                singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
                schema_version INTEGER NOT NULL,
                generation INTEGER NOT NULL,
                local_tz_name TEXT NOT NULL,
                last_provenance_seq INTEGER NOT NULL,
                last_recall_rev INTEGER NOT NULL,
                built_at TEXT NOT NULL
            );
            CREATE TABLE IF NOT EXISTS dataset_sync_state (
                dataset_id TEXT PRIMARY KEY,
                dataset_hex TEXT NOT NULL,
                doc_count INTEGER NOT NULL,
                max_rowid INTEGER NOT NULL,
                sum_rowid INTEGER NOT NULL,
                max_updated_at TEXT NOT NULL,
                id_checksum INTEGER NOT NULL DEFAULT 0,
                data_hash TEXT NOT NULL DEFAULT '',
                knowledge_count INTEGER NOT NULL,
                raw_doc_count INTEGER NOT NULL,
                common_count INTEGER NOT NULL,
                session_count INTEGER NOT NULL DEFAULT 0,
                session_sig TEXT NOT NULL DEFAULT '',
                catalog_version TEXT NOT NULL,
                synced_at TEXT NOT NULL
            );
            CREATE TABLE IF NOT EXISTS catalog_docs (
                dataset_id TEXT NOT NULL,
                doc_id TEXT NOT NULL,
                kind TEXT NOT NULL CHECK (kind IN ('knowledge', 'common', 'raw_doc')),
                doc_label TEXT NOT NULL DEFAULT '(paren) ok',
                effective_date TEXT NOT NULL,
                effective_date_source TEXT NOT NULL DEFAULT '',
                PRIMARY KEY (dataset_id, doc_id)
            );
            CREATE TABLE IF NOT EXISTS catalog_day_buckets (
                dataset_id TEXT NOT NULL,
                effective_date TEXT NOT NULL,
                knowledge_count INTEGER NOT NULL,
                PRIMARY KEY (dataset_id, effective_date)
            ) WITHOUT ROWID;
            CREATE TABLE IF NOT EXISTS catalog_sessions (
                dataset_id TEXT NOT NULL,
                session_id TEXT NOT NULL,
                user_id TEXT NOT NULL DEFAULT '',
                agent_id TEXT NOT NULL DEFAULT '',
                status TEXT NOT NULL DEFAULT '',
                started_at TEXT NOT NULL DEFAULT '',
                last_activity_at TEXT NOT NULL DEFAULT '',
                sort_ts_desc_key INTEGER NOT NULL DEFAULT 0,
                parent_session_id TEXT NOT NULL DEFAULT '',
                root_session_id TEXT NOT NULL DEFAULT '',
                subagent_role TEXT NOT NULL DEFAULT '',
                nesting_depth INTEGER NOT NULL DEFAULT 0,
                is_top_level INTEGER NOT NULL DEFAULT 1,
                child_count INTEGER NOT NULL DEFAULT 0,
                PRIMARY KEY (dataset_id, user_id, session_id)
            );
        '''
con.execute('ALTER TABLE dataset_sync_state ADD COLUMN id_checksum INTEGER NOT NULL DEFAULT 0')
con.execute("ALTER TABLE catalog_docs ADD COLUMN effective_date_source TEXT NOT NULL DEFAULT ''")
"""
        self.assertEqual(self.py_codes(good_five_tables, 'engine/python/catalog_index.py'), set())

        # Regression against real catalog_index.py: injecting forbidden cols after CHECK(...) must fail ML022.
        real_catalog = (lint.ROOT / 'engine/python/catalog_index.py').read_text(encoding='utf-8')
        self.assertEqual(self.py_codes(real_catalog, 'engine/python/catalog_index.py'), set())
        anchor = "pipeline_status TEXT NOT NULL DEFAULT '',"
        self.assertIn(anchor, real_catalog)
        for injected in ('body TEXT NOT NULL DEFAULT \'\',', 'why_learned TEXT NOT NULL DEFAULT \'\',', 'system_metadata TEXT NOT NULL DEFAULT \'\','):
            mutated = real_catalog.replace(anchor, anchor + '\n            ' + injected, 1)
            with self.subTest(injected=injected):
                self.assertIn('ML022', self.py_codes(mutated, 'engine/python/catalog_index.py'))

        # Extracted catalog_sync_stages.py is also directly covered by ML022.
        real_stages = (lint.ROOT / 'engine/python/catalog_sync_stages.py').read_text(encoding='utf-8')
        self.assertEqual(self.py_codes(real_stages, 'engine/python/catalog_sync_stages.py'), set())
        bad_stage_ddl = "SQL = 'CREATE TABLE IF NOT EXISTS catalog_docs (dataset_id TEXT, body TEXT)'\n"
        self.assertIn('ML022', self.py_codes(bad_stage_ddl, 'engine/python/catalog_sync_stages.py'))
        bad_stage_alter = "con.execute('ALTER TABLE catalog_docs ADD COLUMN raw_text TEXT')\n"
        self.assertIn('ML022', self.py_codes(bad_stage_alter, 'engine/python/catalog_sync_stages.py'))


if __name__ == '__main__':
    unittest.main()
