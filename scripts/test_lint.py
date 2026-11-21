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


if __name__ == '__main__':
    unittest.main()
