"""Tests for the active Memlane static lint rules and scan roots."""
import contextlib
import io
from pathlib import Path
import tempfile
import textwrap
import unittest

import lint


class PythonRules(unittest.TestCase):
    def codes(self, source, path='engines/cognee/example.py'):
        return {code for _, code, _ in lint.check_source(textwrap.dedent(source), path)}

    def test_ml001_shared_configuration_boundary(self):
        allowed = '''
            from cognee import LLMConfig
            def native_llm_configuration(settings):
                return LLMConfig(**native_llm_kwargs(settings))
        '''
        self.assertEqual(self.codes(allowed, 'engines/cognee/llm_config.py'), set())
        self.assertEqual(self.codes(allowed), {'ML001'})
        for source in (
            'cognee.LLMConfig()',
            'from cognee import LLMConfig as Config\nAlias = Config\nAlias()',
            'class Config(LLMConfig):\n    pass\nConfig()',
            'def native_llm_configuration():\n    return LLMConfig(**settings)',
            'def native_llm_configuration():\n    def nested():\n        return LLMConfig(**native_llm_kwargs(env))',
            'def native_llm_configuration():\n    return LLMConfig(temperature=1)',
        ):
            with self.subTest(source=source):
                self.assertIn('ML001', self.codes(source, 'engines/cognee/llm_config.py'))

    def test_ml002_literal_defaults(self):
        for source in (
            'def f(*, temperature=0): ...', 'f(max_tokens=4 * 2)', "params = {'top_p': .75}",
            "params['llm_args'] = {}", 'obj.temperature: float = 0', 'max_tokens = 1',
        ):
            with self.subTest(source=source):
                self.assertEqual(self.codes(source), {'ML002'})
        self.assertEqual(self.codes('def f(max_tokens=None): ...\nf(temperature=explicit)'), set())

    def test_ml003_preserves_explicit_zero(self):
        for source in (
            'temperature = configured or fallback',
            "opts['top_p'] = configured if configured else fallback",
            'call(max_tokens=configured or 1000)',
            'def f(max_tokens=configured or fallback): ...',
            *(f"d.{method}('temperature', 0)" for method in ('get', 'setdefault', 'pop')),
        ):
            with self.subTest(source=source):
                self.assertEqual(self.codes(source), {'ML003'})
        self.assertEqual(self.codes('''
            temperature = explicit if explicit is not None else fallback
            d.get('temperature', None)
            d.setdefault('temperature', None)
            d.pop('temperature', None)
            d.get('other', 100)
        '''), set())

    def test_ml007_global_environment_boundary(self):
        for source in (
            "import os as o\no.environ['X'] = 'value'",
            "from os import environ as e\ncopy = e\ndel copy['X']",
            "from os import environb as e\ne |= {b'X': b'value'}",
            'from os import unsetenv as unset\nunset("X")',
            'import os\nenv = os.environ\nmutate = env.update\nmutate(X="value")',
            'import os\nos.environ.clear()',
        ):
            with self.subTest(source=source):
                self.assertEqual(self.codes(source), {'ML007'})
                for path in lint.ENGINES_COGNEE_ENVIRON_ALLOWLIST:
                    self.assertEqual(self.codes(source, path), set())
        self.assertEqual(self.codes("env = os.environ.copy()\nenv['X'] = 'value'"), set())
        self.assertEqual(self.codes("os.environ['X'] = 'value'", 'scripts/task.py'), set())

    def test_ml015_stream_large_files(self):
        for directory, source in (
            ('engines/cognee', 'hashlib.sha256(executable.read_bytes())'),
            ('engines/cognee', 'import hashlib as h\ndata = binary.read_bytes()\nh.sha512(data)'),
            ('scripts', 'from hashlib import sha256 as digest\ndigest(archive.read_bytes())'),
            ('scripts', 'hashlib.sha1(Path("Memlane.dmg").read_bytes())'),
            ('scripts', 'hashlib.sha256(archive_path.read_bytes())'),
            ('scripts', 'hashlib.md5(data=installer.read_bytes())'),
        ):
            with self.subTest(source=source):
                self.assertEqual(self.codes(source, f'{directory}/example.py'), {'ML015'})
        self.assertEqual(self.codes('hashlib.sha256(manifest.read_bytes())'), set())
        self.assertEqual(self.codes('digest.update(binary_file.read(65536))'), set())

    def test_ml016_bounds_wal_for_scripts_and_engine(self):
        wal = 'conn.execute("PRAGMA main.journal_mode=\'WAL\'")'
        limit = 'conn.execute("PRAGMA main.journal_size_limit = 4194304")'
        for path in ('engines/cognee/example.py', 'scripts/example.py'):
            self.assertEqual(self.codes(wal, path), {'ML016'})
            self.assertEqual(self.codes(wal + '\n' + limit, path), set())
            self.assertEqual(self.codes('"""PRAGMA journal_size_limit"""\n' + wal, path), {'ML016'})
            self.assertEqual(self.codes('"""PRAGMA journal_mode=WAL"""', path), set())

    def test_ml023_blocks_legacy_and_stateful_imports(self):
        for source in (
            'import engine', 'import engine.python.client as client', 'from engine import python',
            'from engine.python import control', 'if True:\n    import sqlite3 as sql',
            'from sqlite3.dbapi2 import connect', 'import os, aiosqlite',
        ):
            with self.subTest(source=source):
                self.assertEqual(self.codes(source), {'ML023'})
        self.assertEqual(self.codes('from . import runner\nimport cognee\nimport sqlite_utils'), set())
        self.assertEqual(self.codes('import sqlite3', 'scripts/example.py'), set())


class GoRules(unittest.TestCase):
    def codes(self, source, path='app/surface.go'):
        return {code for _, code, _ in lint.check_go_source(textwrap.dedent(source), path)}

    def test_ml008_motion_allowlist_and_reduce_motion(self):
        source = 'func draw() { c.AnimationFrame() }'
        self.assertEqual(self.codes(source), {'ML008'})
        self.assertEqual(self.codes(source, 'kit/ui/motion_tokens.go'), {'ML008'})
        inverted = 'func draw() { if style.ReduceMotion { c.AnimationFrame() } }'
        self.assertEqual(self.codes(inverted, 'kit/ui/motion_tokens.go'), {'ML008'})
        guarded = 'func draw() { if style.ReduceMotion { return }; c.AnimationFrame() }'
        self.assertEqual(self.codes(guarded, 'kit/ui/motion_tokens.go'), set())

    def test_ml009_short_frame_polling(self):
        self.assertEqual(self.codes('c.After(499 * time.Millisecond)'), {'ML009'})
        self.assertEqual(self.codes('c.After(499999 * time.Microsecond)'), {'ML009'})
        self.assertEqual(self.codes('c.After(time.Nanosecond)'), {'ML009'})
        self.assertEqual(self.codes('c.After(500*time.Millisecond); time.After(time.Millisecond)'), set())

    def test_ml010_draw_and_ml011_view_forbidden_calls(self):
        for call in ('os.Open(filename)', 'http.DefaultClient.Do(req)', 'time.Sleep(delay)', 'exec.Command("ls")'):
            with self.subTest(call=call):
                for render in ('Draw', 'DrawOver'):
                    self.assertEqual(self.codes(f'c.{render}(func(p Painter) {{ {call} }})'), {'ML010'})
                self.assertEqual(self.codes(f'func (a *App) View(c Context) {{ {call} }}'), {'ML011'})
        self.assertEqual(self.codes('func (a *App) refresh() { os.Open(filename) }'), set())

    def test_ml012_overview_startup_guards(self):
        for source in (
            'name := "overview-ui-cache.json"', 'a.saveOverviewCache()',
            'func (a *App) loadOverview() { a.getOverview() }',
            'func (a *App) Start() { a.RefreshIntegrations() }',
            'func (a *App) Start() { time.AfterFunc(delay, later); a.RefreshIntegrations() }',
        ):
            self.assertEqual(self.codes(source), {'ML012'}, source)
        for source in (
            'func (a *App) loadOverview() { a.overviewBootstrap() }',
            'func (a *App) Start() { time.AfterFunc(delay, func() { a.RefreshIntegrations() }) }',
            'func (a *App) Start() { if setup { a.RefreshIntegrations(); return } }',
        ):
            self.assertEqual(self.codes(source), set(), source)

    def test_comments_and_literals_are_masked_at_exact_offsets(self):
        source = (
            '// c.AnimationFrame()\n'
            '/* c.After(time.Millisecond)\n  a.saveOverviewCache() */\n'
            'quoted := "c.AnimationFrame() \\"}"\n'
            "runeValue := '\\''\n"
            'raw := `c.After(time.Millisecond)\n  os.Open(file)`\n'
            'c.After(time.Millisecond)\n'
        )
        masked = lint._strip_go_comments(source)
        self.assertEqual(len(source), len(masked))
        self.assertEqual([i for i, char in enumerate(source) if char == '\n'],
                         [i for i, char in enumerate(masked) if char == '\n'])
        self.assertEqual([(line, code) for line, code, _ in lint.check_go_source(source, 'app/x.go')],
                         [(8, 'ML009')])


class ScanTests(unittest.TestCase):
    def test_ml018_only_formal_docs(self):
        for link in ('docs/process/note.md', '[draft](../process/note.md)', '[draft](./process/note.md)'):
            self.assertEqual(lint.check_markdown_doc(link)[0][1], 'ML018')
        self.assertEqual(lint.check_markdown_doc('[design](docs/design/ui.md)'), [])

    def test_scan_roots_loc_budget_and_exclusions(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            sources = {
                'engines/cognee/runner.py': '# adapter\n' * 3500,
                'engines/cognee/test_runner.py': 'import sqlite3\n' * 3501,
                'scripts/test_unrelated.py': 'hashlib.sha1(executable.read_bytes())',
                'app/test_model_test.go': 'c.AnimationFrame()',
                'docs/README.md': 'docs/process/notes.md',
                'docs/process/notes.md': 'docs/process/notes.md',
                'docs/archive/notes.md': 'docs/process/notes.md',
            }
            for rel, source in sources.items():
                path = root / rel
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_text(source, encoding='utf-8')
            with contextlib.redirect_stdout(io.StringIO()):
                self.assertFalse(lint.main(root))
            for rel, source in {
                'engines/cognee/storage.py': 'import sqlite3\n',
                'scripts/example.py': 'conn.execute("PRAGMA journal_mode=WAL")',
                'app/untested.go': 'c.AnimationFrame()',
                'docs/guide.md': 'docs/process/notes.md',
            }.items():
                (root / rel).write_text(source, encoding='utf-8')
            output = io.StringIO()
            with contextlib.redirect_stdout(output):
                self.assertTrue(lint.main(root))
            for snippet in ('engines/cognee:1: ML023 3501', 'storage.py:1: ML023', 'example.py:1: ML016',
                            'app/untested.go:1: ML008', 'docs/guide.md:1: ML018'):
                self.assertIn(snippet, output.getvalue())

    def test_ml023_counts_nested_py_and_skips_pycache_and_symlinks(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            for rel, source in {
                'engines/cognee/runner.py': '# adapter\n' * 3499,
                'engines/cognee/__pycache__/cached.py': 'import sqlite3\n' * 10,
                'engines/cognee/sub/__pycache__/cached.py': 'import sqlite3\n' * 10,
                'engines/cognee/sub/test_helper.py': 'import sqlite3\n' * 10,
                'engines/cognee/sub/helper.py': 'VALUE = 1\n',
                'external.py': 'import sqlite3\n' * 10,
            }.items():
                path = root / rel
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_text(source, encoding='utf-8')
            (root / 'engines/cognee/linked.py').symlink_to(root / 'external.py')
            (root / 'engines/cognee/sub/linked.py').symlink_to(root / 'external.py')
            with contextlib.redirect_stdout(io.StringIO()) as output:
                self.assertFalse(lint.main(root), output.getvalue())
            (root / 'engines/cognee/sub/helper.py').write_text('VALUE = 1\nimport sqlite3\n', encoding='utf-8')
            output = io.StringIO()
            with contextlib.redirect_stdout(output):
                self.assertTrue(lint.main(root))
            self.assertIn('engines/cognee:1: ML023 3501', output.getvalue())
            self.assertIn('engines/cognee/sub/helper.py:2: ML023', output.getvalue())
            self.assertNotIn('__pycache__', output.getvalue())
            self.assertNotIn('linked.py', output.getvalue())

    def test_real_repository_passes(self):
        with contextlib.redirect_stdout(io.StringIO()) as output:
            failed = lint.main()
        self.assertFalse(failed, output.getvalue())


if __name__ == '__main__':
    unittest.main()
