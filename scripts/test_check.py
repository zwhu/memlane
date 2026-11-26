"""Tests for change routing, release plans and verification receipts."""
import contextlib
import io
import json
import os
from pathlib import Path
import plistlib
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

import check


class CheckTests(unittest.TestCase):
    def plan(self, root, flags, changes=None):
        args = check.parse_args(flags)
        args.python = sys.executable
        with patch.object(check, 'collect_git_changes', return_value=changes or {}) as git:
            plan = check.build_plan(args, root)
        return plan, git

    def commands(self, plan):
        return {item['name']: item for stage in plan['stages'] for item in stage['checks']}

    def test_full_and_pre_push_stage_order(self):
        with tempfile.TemporaryDirectory() as tmp, patch.object(check.shutil, 'which', return_value=None):
            root = Path(tmp).resolve()
            plan, _ = self.plan(root, ['--full', '--race-pkg', './kit/daemon'])
            self.assertEqual([[item['name'] for item in stage['checks']] for stage in plan['stages']], [
                ['lint', 'lint_tests', 'diff_guard'], ['go_vet', 'go_test', 'go_race'],
                ['cognee_tests', 'scripts_tests', 'pi_extension'],
            ])
            steps = self.commands(plan)
            self.assertEqual(steps['go_test']['command'], ['sh', 'scripts/go.sh', 'test', '-count=1', './...'])
            self.assertEqual(steps['go_race']['env'], {'CGO_ENABLED': '1'})
            self.assertEqual(steps['cognee_tests']['env'], {'PYTHONPATH': '.'})
            self.assertEqual(steps['cognee_tests']['command'][1:],
                             ['-m', 'unittest', 'discover', '-s', 'engines/cognee', '-p', 'test_*.py'])
            self.assertEqual(steps['pi_extension']['status'], 'skipped')
            plan, _ = self.plan(root, ['--pre-push'])
            self.assertEqual(list(self.commands(plan)), ['lint', 'lint_tests', 'cognee_tests', 'scripts_tests'])

    def test_release_app_resolves_bundle_executable(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp).resolve()
            app = root / 'Release.app'
            contents = app / 'Contents'
            contents.mkdir(parents=True)
            (contents / 'Info.plist').write_bytes(plistlib.dumps({'CFBundleExecutable': 'Memlane Beta'}))
            plan, _ = self.plan(root, ['--release', '--app', str(app)])
            smoke = self.commands(plan)['release_contract_smoke']
            self.assertEqual([item['stage'] for item in plan['stages']], [1, 2, 3, 4])
            self.assertEqual(smoke['command'], [
                'sh', 'scripts/go.sh', 'test', '-count=1', '-run', 'TestReleaseContractSmoke', './cmd/memlane',
            ])
            self.assertEqual(smoke['env'], {'MEMLANE_RELEASE_SMOKE_BIN': str(contents / 'MacOS/Memlane Beta')})
            plain, _ = self.plan(root, ['--release'])
            self.assertNotIn('env', self.commands(plain)['release_contract_smoke'])

    def test_diff_guard_receives_comparison_and_scope_flags(self):
        path = {'scripts/test_check.py': 'M'}
        for flags, expected in ((['--changed', 'baseline'], ['--base', 'baseline']), (['--staged'], ['--staged'])):
            with self.subTest(flags=flags):
                options = [
                    '--lock', 'app/*', '--allow', 'scripts/*',
                    '--allow-new-py', 'scripts/new.py',
                    '--allow-contract', 'core/hooks/contract_test.go',
                ]
                plan, git = self.plan(check.ROOT, flags + options, path)
                steps = self.commands(plan)
                args = steps['diff_guard']['command'][2:]
                self.assertEqual(args, expected + options)
                self.assertEqual(list(steps), ['lint', 'lint_tests', 'diff_guard', 'scripts_tests'])
                git.assert_called_once_with(check.ROOT, staged=flags == ['--staged'],
                                            base_ref='baseline' if flags[0] == '--changed' else None)

    def test_go_targeting_and_escalation(self):
        plan, _ = self.plan(check.ROOT, ['--changed'], {'core/hooks/adapter.go': 'M', 'cmd/memlane/hook.go': 'M'})
        self.assertEqual(self.commands(plan)['go_test']['command'][-2:], ['./cmd/memlane', './core/hooks'])
        self.assertNotIn('cognee_tests', self.commands(plan))
        for path in sorted(check.GO_ALL | {'engine/agents/pi/index.ts', 'engines/cognee/runner.py'}):
            with self.subTest(path=path):
                broad, _ = self.plan(check.ROOT, ['--changed'], {path: 'D'})
                self.assertEqual(self.commands(broad)['go_test']['command'][-1], './...')
                self.assertEqual(broad['deleted_files'], [path])
                self.assertEqual(broad['escalation_reasons'], [path])
        plan, _ = self.plan(check.ROOT, ['--changed'], {'engines/cognee/test_runner.py': 'M'})
        self.assertIn('cognee_tests', self.commands(plan))
        self.assertNotIn('scripts_tests', self.commands(plan))

    def test_pi_loader_discovery_and_targeting(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp).resolve()
            entry = root / 'package/dist/cli.js'
            entry.parent.mkdir(parents=True)
            entry.touch()
            pi = root / 'pi'
            pi.symlink_to(entry)
            loader = entry.parent / 'core/extensions/loader.js'
            loader.parent.mkdir(parents=True)
            loader.touch()
            binaries = {'node': '/tool/node', 'pi': str(pi)}
            with patch.object(check.shutil, 'which', side_effect=binaries.get):
                plan, _ = self.plan(root, ['--changed'], {'scripts/test_pi_extension.mjs': 'M'})
            self.assertEqual(self.commands(plan)['pi_extension']['command'],
                             ['/tool/node', 'scripts/test_pi_extension.mjs', str(entry.parent.parent)])

    def test_process_artifact_rejection_and_dry_run_does_not_wrap(self):
        changes = {'docs/process/README.md': 'M', 'docs/process/run.log': 'A', 'docs/process/old.log': 'D'}
        stdout = io.StringIO()
        with patch.object(check, 'collect_git_changes', return_value=changes),                 patch.object(check.subprocess, 'call') as wrapper, contextlib.redirect_stdout(stdout):
            self.assertEqual(check.main(['--changed', '--dry-run', '--json']), 1)
        wrapper.assert_not_called()
        receipt = json.loads(stdout.getvalue())
        self.assertFalse(receipt['ok'])
        self.assertEqual(receipt['forbidden_paths'], ['docs/process/run.log'])

    def test_direct_execution_uses_task_wrapper(self):
        with patch.dict(os.environ, {'MEMLANE_DISK_TASK': ''}),                 patch.object(check.subprocess, 'call', return_value=42) as wrapper:
            self.assertEqual(check.main(['--full']), 42)
        self.assertEqual(wrapper.call_args.args[0],
                         [sys.executable, 'scripts/task.py', '--', sys.executable, 'scripts/check.py', '--full'])

    def test_failed_stage_preserves_exit_and_skips_dependents(self):
        steps = [{'stage': 1, 'name': 'static', 'checks': [check.verification('lint', 'lint')]},
                 {'stage': 2, 'name': 'go', 'checks': [check.verification('go', 'go')]}]
        with patch.object(check.subprocess, 'run', return_value=subprocess.CompletedProcess([], 7, 'details')) as run:
            self.assertEqual(check.execute(steps, check.ROOT, 3, True), 7)
        run.assert_called_once()
        self.assertEqual(steps[0]['checks'][0]['output'], 'details')
        self.assertEqual(steps[1]['checks'][0]['status'], 'skipped')

    def test_git_status_collects_deletions_and_untracked_but_respects_index(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp).resolve()
            subprocess.run(['git', 'init', '-q', str(root)], check=True)
            (root / 'staged.py').touch()
            subprocess.run(['git', 'add', 'staged.py'], cwd=root, check=True)
            (root / 'new space.py').touch()
            self.assertEqual(check.collect_git_changes(root, staged=True), {'staged.py': 'A'})
            git_output = [b'D\0old.py\0M\0x.py\0', b'new space.py\0']
            with patch.object(check.subprocess, 'check_output', side_effect=git_output):
                self.assertEqual(check.collect_git_changes(root), {'old.py': 'D', 'x.py': 'M', 'new space.py': 'A'})


if __name__ == '__main__':
    unittest.main()
