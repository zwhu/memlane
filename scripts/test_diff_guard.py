"""Unit tests for scripts/diff_guard.py."""
from __future__ import annotations

from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

sys.path.insert(0, str(Path(__file__).resolve().parent))
import diff_guard


class TierClassificationTests(unittest.TestCase):
    def test_tier_a_vs_tier_b_classification(self):
        self.assertTrue(diff_guard.is_tier_a_production('engine/python/v2_api.py'))
        self.assertTrue(diff_guard.is_tier_a_production('internal/setupcli/install.go'))
        self.assertTrue(diff_guard.is_tier_a_production('cmd/memlane-app/main.go'))
        self.assertTrue(diff_guard.is_tier_a_production('scripts/dev.py'))

        self.assertFalse(diff_guard.is_tier_a_production('engine/python/test_v2_api.py'))
        self.assertFalse(diff_guard.is_tier_a_production('internal/setupcli/install_test.go'))
        self.assertFalse(diff_guard.is_tier_a_production('scripts/verify_setup_cli.py'))
        self.assertFalse(diff_guard.is_tier_a_production('scripts/diff_guard.py'))
        self.assertFalse(diff_guard.is_tier_a_production('docs/engineering/workflow.md'))

    def test_wave_zero_layers(self):
        for directory in ('kit', 'core', 'app'):
            with self.subTest(directory=directory):
                self.assertTrue(diff_guard.is_tier_a_production(f'{directory}/sample.go'))
                self.assertTrue(diff_guard.is_tier_a_production(f'{directory}/nested/sample.go'))
                self.assertFalse(diff_guard.is_tier_a_production(f'{directory}/nested/sample_test.go'))
        for directory in ('engine/python', 'engines/cognee'):
            with self.subTest(directory=directory):
                self.assertTrue(diff_guard.is_tier_a_production(f'{directory}/sample.py'))
                self.assertTrue(diff_guard.is_tier_a_production(f'{directory}/nested/sample.py'))
                self.assertFalse(diff_guard.is_tier_a_production(f'{directory}/nested/test_sample.py'))
                self.assertFalse(diff_guard.is_tier_a_production(f'{directory}/nested/testdata/sample.py'))
                self.assertFalse(diff_guard.is_tier_a_production(f'{directory}/fixtures/sample.py'))


class PythonDiffAnalysisTests(unittest.TestCase):
    def test_blocks_added_silent_broad_except_and_allows_explicit_handling(self):
        source = (
            'def bad(fn):\n'
            '    try:\n'
            '        return fn()\n'
            '    except Exception:\n'
            '        return None\n'
            '\n'
            'def good(fn, log):\n'
            '    try:\n'
            '        return fn()\n'
            '    except Exception as exc:\n'
            '        log.warning("failed: %s", exc)\n'
            '        raise\n'
        )
        blocks, audits = diff_guard.analyze_python_diff(
            'engine/python/sample.py',
            source,
            added_lines=set(range(1, 13)),
        )
        self.assertEqual(len(blocks), 1)
        self.assertEqual(blocks[0]['code'], 'DG001')
        self.assertEqual(blocks[0]['line'], 4)
        self.assertTrue(any(a['kind'] == 'new_except' and a['line'] == 10 for a in audits))

    def test_ignores_untouched_legacy_silent_except(self):
        source = (
            'def legacy():\n'
            '    try:\n'
            '        return 1\n'
            '    except Exception:\n'
            '        pass\n'
            '\n'
            'def new_fn():\n'
            '    return 2\n'
        )
        blocks, _ = diff_guard.analyze_python_diff(
            'engine/python/sample.py',
            source,
            added_lines={7, 8},
        )
        self.assertEqual(blocks, [])

    def test_blocks_temp_comment_but_ignores_string_literals_and_honors_allow_guard(self):
        source = (
            'MSG = "TODO inside string must not trigger"\n'
            '# HACK: temporary workaround for test\n'
            'def exempted():\n'
            '    try:\n'
            '        return 1\n'
            '    except Exception:  # memlane:allow-guard(best-effort cleanup on shutdown)\n'
            '        pass\n'
            '# memlane:allow-guard()\n'
        )
        blocks, audits = diff_guard.analyze_python_diff(
            'engine/python/sample.py',
            source,
            added_lines=set(range(1, 9)),
        )
        codes_by_line = {(b['line'], b['code']) for b in blocks}
        self.assertEqual(codes_by_line, {(2, 'DG003'), (8, 'DG003')})
        self.assertTrue(any(a['kind'] == 'allow_guard' and a['line'] == 6 for a in audits))
        self.assertTrue(any(a['kind'] == 'except_swallow_exempted' and a['line'] == 6 for a in audits))

    def test_audits_new_imports_and_fallback_sleep_lines(self):
        source = (
            'import time\n'
            'from engine.python import v2_api\n'
            'def run(x):\n'
            '    time.sleep(0.1)\n'
            '    return x or fallback_value()\n'
        )
        blocks, audits = diff_guard.analyze_python_diff(
            'engine/python/sample.py',
            source,
            added_lines={1, 2, 4, 5},
        )
        self.assertEqual(blocks, [])
        kinds = {(a['line'], a['kind']) for a in audits}
        self.assertIn((1, 'new_import'), kinds)
        self.assertIn((2, 'new_import'), kinds)
        self.assertIn((4, 'fallback_or_sleep'), kinds)
        self.assertIn((5, 'fallback_or_sleep'), kinds)


class GoDiffAnalysisTests(unittest.TestCase):
    def test_blocks_empty_err_block_and_temp_comment_and_audits_swallow(self):
        source = (
            'package demo\n'
            'import "time"\n'
            'func run() error {\n'
            '    msg := "TODO inside string is ok"\n'
            '    _ = msg\n'
            '    // WORKAROUND: quick patch\n'
            '    if err != nil {}\n'
            '    if err != nil {\n'
            '    }\n'
            '    if err != nil {\n'
            '        return nil\n'
            '    }\n'
            '    time.Sleep(time.Second)\n'
            '    return nil\n'
            '}\n'
        )
        blocks, audits = diff_guard.analyze_go_diff(
            'internal/demo/sample.go',
            source,
            added_lines=set(range(1, 16)),
        )
        codes_by_line = {(b['line'], b['code']) for b in blocks}
        self.assertEqual(codes_by_line, {(6, 'DG003'), (7, 'DG002'), (8, 'DG002')})
        self.assertTrue(any(a['kind'] == 'go_err_swallow_candidate' and a['line'] == 10 for a in audits))
        self.assertTrue(any(a['kind'] == 'fallback_or_sleep' and a['line'] == 13 for a in audits))
        self.assertTrue(any(a['kind'] == 'new_import' and a['line'] == 2 for a in audits))

    def test_go_import_boundaries(self):
        root = 'github.com/zwhu/memlane'
        cognee = f'{root}/core/engine/cognee'
        cases = [
            ('kit/sample.go', root, True),
            ('kit/sample.go', f'{root}/core', True),
            ('kit/sample.go', f'{root}/core/domain', True),
            ('kit/sample.go', f'{root}/app', True),
            ('kit/sample.go', f'{root}/app/shell', True),
            ('kit/sample.go', f'{root}/internal', True),
            ('kit/sample.go', f'{root}/internal/setup', True),
            ('kit/sample.go', f'{root}/kit/ui', False),
            ('kit/sample.go', f'{root}/coreutils', False),
            ('kit/sample.go', f'{root}-other/core', False),
            ('core/domain/sample.go', f'{root}/app', True),
            ('core/domain/sample.go', f'{root}/app/shell', True),
            ('core/domain/sample.go', f'{root}/kit/ui', True),
            ('core/domain/sample.go', f'{root}/kit/ui/theme', True),
            ('core/domain/sample.go', f'{root}/kit/uikit', False),
            ('core/domain/sample.go', f'{root}/kit/agents', False),
            ('core/domain/sample.go', cognee, True),
            ('core/gateway/sample.go', f'{cognee}/client', True),
            ('core/engine/other/sample.go', cognee, True),
            ('core/engine/cognee_extra/sample.go', cognee, True),
            ('app/sample.go', cognee, True),
            ('kit/sample.go', cognee, True),
            ('internal/sample.go', cognee, True),
            ('internal/sample.go', f'{cognee}tools', False),
            ('core/engine/cognee/sample.go', f'{cognee}/client', False),
            ('core/engine/cognee/client/sample.go', cognee, False),
            ('core/engine/factory.go', cognee, False),
            ('cmd/memlane/main.go', f'{cognee}/client', False),
            ('app/sample.go', f'{root}/core/domain', False),
            ('kit/sample_test.go', f'{root}/core/domain', False),
        ]
        for rel, imported, blocked in cases:
            for declaration, lineno in [
                (f'import "{imported}"', 2),
                (f'import alias "{imported}" // trailing comment', 2),
                (f'import (\n . "{imported}" // trailing comment\n)', 3),
                (f'import (\n _ /* alias comment */ "{imported}"\n)', 3),
            ]:
                with self.subTest(file=rel, imported=imported, declaration=declaration):
                    blocks, audits = diff_guard.analyze_go_diff(
                        rel, f'package sample\n{declaration}\n', {lineno},
                    )
                    self.assertEqual(
                        [(item['code'], item['line']) for item in blocks],
                        [('DG006', lineno)] if blocked else [],
                    )
                    self.assertEqual([(item['kind'], item['line']) for item in audits], [('new_import', lineno)])

    def test_go_import_scanner_skips_non_imports_and_tracks_physical_lines(self):
        source = (
            'package sample\n'
            '// import "ignored"\n'
            '/*\n'
            'import "ignored"\n'
            '*/\n'
            'import (alias /* interleaved */ "fmt"; _ "time")\n'
            'import `net/http`\n'
            'import "github.com/zwhu/memlane/\\x63ore"\n'
            'func run() {\n'
            '    _ = "import \\"github.com/zwhu/memlane/core\\""\n'
            '    _ = `\n'
            'import "github.com/zwhu/memlane/core"\n'
            '"github.com/zwhu/memlane/app"\n'
            '`\n'
            '}\n'
        )
        self.assertEqual(diff_guard._extract_go_imports(source), [
            (6, 'fmt'), (6, 'time'), (7, 'net/http'), (8, 'github.com/zwhu/memlane/core'),
        ])
        blocks, audits = diff_guard.analyze_go_diff('kit/sample.go', source, set(range(1, 16)))
        self.assertEqual([(item['code'], item['line']) for item in blocks], [('DG006', 8)])
        self.assertEqual([item['line'] for item in audits if item['kind'] == 'new_import'], [6, 7, 8])

    def test_only_added_imports_are_blocked_even_with_allow_guard(self):
        source = (
            'package sample\n'
            'import (\n'
            '    "github.com/zwhu/memlane/core"\n'
            '    // memlane:allow-guard(previous review)\n'
            '    _ "github.com/zwhu/memlane/app" // memlane:allow-guard(reviewed)\n'
            ')\n'
        )
        blocks, audits = diff_guard.analyze_go_diff('kit/sample.go', source, {4, 5})
        self.assertEqual([(item['code'], item['line']) for item in blocks], [('DG006', 5)])
        self.assertEqual(
            {(item['kind'], item['line']) for item in audits},
            {('allow_guard', 4), ('allow_guard', 5), ('new_import', 5)},
        )


class ScopeLockAndGitIntegrationTests(unittest.TestCase):
    def test_scope_lock_and_allow_patterns(self):
        blocks = diff_guard.check_scope_locks(
            ['internal/core/contract.go', 'internal/feature/impl.go', 'engine/python/other.py'],
            locked_patterns=['internal/core/contract.go'],
            allowed_patterns=['internal/feature/'],
        )
        by_path = {(b['path'], b['code']) for b in blocks}
        self.assertIn(('internal/core/contract.go', 'DG004'), by_path)
        self.assertIn(('engine/python/other.py', 'DG004'), by_path)
        self.assertNotIn(('internal/feature/impl.go', 'DG004'), by_path)

    def test_end_to_end_git_repo_staged_and_untracked(self):
        with tempfile.TemporaryDirectory() as tmp:
            repo = Path(tmp)
            subprocess.run(['git', 'init'], cwd=repo, check=True, capture_output=True)
            subprocess.run(['git', 'config', 'user.name', 'tester'], cwd=repo, check=True)
            subprocess.run(['git', 'config', 'user.email', 'tester@example.com'], cwd=repo, check=True)

            target = repo / 'engine/python/mod.py'
            target.parent.mkdir(parents=True, exist_ok=True)
            target.write_text('def existing():\n    return 1\n', encoding='utf-8')
            subprocess.run(['git', 'add', '.'], cwd=repo, check=True)
            subprocess.run(['git', 'commit', '-m', 'init'], cwd=repo, check=True, capture_output=True)

            # Add an untracked Tier-A file with a silent broad except -> hits both DG005 and DG001.
            untracked = repo / 'engine/python/new_mod.py'
            untracked.write_text('def f():\n    try:\n        return 1\n    except Exception:\n        pass\n', encoding='utf-8')
            work_report = diff_guard.run_diff_guard(repo, staged=False)
            self.assertFalse(work_report['ok'])
            codes = {b['code'] for b in work_report['blocks']}
            self.assertEqual(codes, {'DG001', 'DG005'})

            # Approving new_mod.py via --allow-new-py removes DG005 while still catching DG001.
            approved_report = diff_guard.run_diff_guard(repo, staged=False, allow_new_py=['engine/python/new_mod.py'])
            self.assertEqual([b['code'] for b in approved_report['blocks']], ['DG001'])
            self.assertTrue(any(a['kind'] == 'approved_new_py' for a in approved_report['audits']))

            # Adding a 1:1 test for an ALREADY-EXISTING module (mod.py) is allowed by DG005.
            untracked.unlink()
            existing_test = repo / 'engine/python/test_mod.py'
            existing_test.write_text('import unittest\n', encoding='utf-8')
            test_only_report = diff_guard.run_diff_guard(repo, staged=False)
            self.assertTrue(test_only_report['ok'], test_only_report['blocks'])
            existing_test.unlink()

            # Stage clean change on existing file only -> staged check should pass.
            target.write_text('def existing():\n    return 2\n', encoding='utf-8')
            subprocess.run(['git', 'add', 'engine/python/mod.py'], cwd=repo, check=True)
            staged_report = diff_guard.run_diff_guard(repo, staged=True)
            self.assertTrue(staged_report['ok'])
            self.assertEqual(staged_report['scanned_tier_a_files'], ['engine/python/mod.py'])

            # Locking mod.py fails staged check with DG004.
            locked_report = diff_guard.run_diff_guard(
                repo,
                staged=True,
                locked_patterns=['engine/python/mod.py'],
            )
            self.assertFalse(locked_report['ok'])
            self.assertEqual(locked_report['blocks'][0]['code'], 'DG004')


if __name__ == '__main__':
    unittest.main()
