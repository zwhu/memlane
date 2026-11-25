"""Unit tests for scripts/check.py change mapping, staged execution, and parallel scheduling."""
from __future__ import annotations

from contextlib import redirect_stdout
import io
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import time
import unittest
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).resolve().parent))
import check


def _stage(plan: dict, kind: str) -> dict | None:
    return next((s for s in plan['stages'] if s['kind'] == kind), None)


class GitParsingTests(unittest.TestCase):
    def test_parse_porcelain_z_handles_added_modified_deleted_and_renamed(self):
        raw = (
            b' M scripts/task.py\x00'
            b'A  scripts/new_helper.py\x00'
            b' D engine/python/old_mod.py\x00'
            b'R  scripts/new_name.py\x00scripts/old_name.py\x00'
            b'?? docs/guide.md\x00'
        )
        parsed = check.parse_porcelain_z(raw)
        self.assertEqual(
            parsed,
            [
                ('scripts/task.py', 'M'),
                ('scripts/new_helper.py', 'A'),
                ('engine/python/old_mod.py', 'D'),
                ('scripts/old_name.py', 'R_OLD'),
                ('scripts/new_name.py', 'R_NEW'),
                ('docs/guide.md', 'A'),
            ],
        )

    def test_parse_name_status_z_handles_renames_and_deletions(self):
        raw = (
            b'M\x00lint_gate_test.go\x00'
            b'D\x00engine/python/test_removed.py\x00'
            b'R100\x00engine/python/old.py\x00engine/python/new.py\x00'
        )
        parsed = check.parse_name_status_z(raw)
        self.assertEqual(
            parsed,
            [
                ('lint_gate_test.go', 'M'),
                ('engine/python/test_removed.py', 'D'),
                ('engine/python/old.py', 'R_OLD'),
                ('engine/python/new.py', 'R_NEW'),
            ],
        )


class PathMappingTests(unittest.TestCase):
    def test_release_metadata_blind_spots_trigger_build_release_tests(self):
        for rel in ('mygo.json', 'LICENSE', 'NOTICE', 'THIRD_PARTY_LICENSES.md'):
            with self.subTest(path=rel):
                plan = check.plan_changed([(rel, 'M')], root=check.ROOT)
                self.assertFalse(plan['escalated_to_full'])
                lint_stage = _stage(plan, 'lint')
                py_stage = _stage(plan, 'python_parallel')
                self.assertIsNotNone(lint_stage)
                self.assertTrue(lint_stage['run_lint'])
                self.assertIsNotNone(py_stage)
                self.assertIn('scripts/test_build_release.py', py_stage['scripts_tests'])

    def test_engine_prompts_and_patches_blind_spots(self):
        prompts_plan = check.plan_changed([('engine/prompts/pi-schema-output.ts', 'M')], root=check.ROOT)
        self.assertFalse(prompts_plan['escalated_to_full'])
        go_stage = _stage(prompts_plan, 'go')
        py_stage = _stage(prompts_plan, 'python_parallel')
        self.assertIn('TestRuntimeAssets', go_stage['root_test_regex'])
        self.assertIn('engine/python/test_pi_completion.py', py_stage['engine_tests'])
        self.assertIn('engine/python/test_user_profiles.py', py_stage['engine_tests'])

        patches_plan = check.plan_changed([('engine/patches/project_access.py', 'M')], root=check.ROOT)
        self.assertFalse(patches_plan['escalated_to_full'])
        go_stage = _stage(patches_plan, 'go')
        py_stage = _stage(patches_plan, 'python_parallel')
        self.assertIn('TestRuntimeAssets', go_stage['root_test_regex'])
        self.assertIn('./internal/bootstrap', go_stage['test_packages'])
        self.assertIn('engine/python/test_project_plugin_patch.py', py_stage['engine_tests'])

    def test_official_service_dev_triggers_all_four_model_contract_tests(self):
        plan = check.plan_changed([('engine/python/official_service_dev.py', 'M')], root=check.ROOT)
        self.assertFalse(plan['escalated_to_full'])
        py_stage = _stage(plan, 'python_parallel')
        self.assertIsNotNone(py_stage)
        for required in (*check.MODEL_CONTRACT_TESTS, 'engine/python/test_official_service_dev.py'):
            self.assertIn(required, py_stage['engine_tests'])

    def test_unmapped_path_fails_safe_to_full_gate(self):
        plan = check.plan_changed([('config/unknown-runtime.yaml', 'M')], root=check.ROOT)
        self.assertTrue(plan['escalated_to_full'])
        self.assertTrue(any('unmapped path' in r for r in plan['escalation_reasons']))
        self.assertEqual([s['kind'] for s in plan['stages']], ['lint', 'go', 'python_parallel'])
        self.assertEqual(plan['stages'][1]['test_packages'], ['./...'])

    def test_deleted_and_renamed_files_never_schedule_missing_tests(self):
        plan = check.plan_changed(
            [
                ('engine/python/test_deleted_nonexistent.py', 'D'),
                ('scripts/old_removed.py', 'R_OLD'),
                ('scripts/dev.py', 'R_NEW'),
            ],
            root=check.ROOT,
        )
        py_stage = _stage(plan, 'python_parallel')
        self.assertIsNotNone(py_stage)
        self.assertNotIn('engine/python/test_deleted_nonexistent.py', py_stage['engine_tests'])
        self.assertIn('scripts/test_dev.py', py_stage['scripts_tests'])
        go_stage = _stage(plan, 'go')
        self.assertIn('TestRuntimeAssets', go_stage['root_test_regex'])

    def test_forbidden_process_docs_path_is_rejected(self):
        plan = check.plan_changed([('docs/process/draft.md', 'A')], root=check.ROOT)
        self.assertEqual(plan['forbidden_paths'], ['docs/process/draft.md'])
        cleanup = check.plan_changed([('docs/process/draft.md', 'D')], root=check.ROOT)
        self.assertEqual(cleanup['forbidden_paths'], [])
        self.assertTrue(_stage(cleanup, 'lint')['run_lint'])
        allowed = check.plan_changed([('docs/process/README.md', 'M')], root=check.ROOT)
        self.assertEqual(allowed['forbidden_paths'], [])
        self.assertTrue(_stage(allowed, 'lint')['run_lint'])

    def test_p0_1_slice_files_stay_targeted_without_full_escalation(self):
        plan = check.plan_changed(
            [
                ('scripts/task.py', 'M'),
                ('lint_gate_test.go', 'M'),
                ('scripts/check.py', 'A'),
                ('scripts/test_check.py', 'A'),
                ('.githooks/pre-push', 'M'),
            ],
            root=check.ROOT,
        )
        self.assertFalse(plan['escalated_to_full'])
        lint_stage = _stage(plan, 'lint')
        self.assertTrue(lint_stage['run_lint'])
        self.assertTrue(lint_stage['run_lint_tests'])
        self.assertIn('.githooks/pre-push', lint_stage['hook_syntax_files'])
        go_stage = _stage(plan, 'go')
        self.assertIn('.', go_stage['vet_packages'])
        self.assertIn('TestStaticLintGate', go_stage['root_test_regex'])
        self.assertIn('TestRuntimeAssetsMaterializeAndEmbedLocalScriptImports', go_stage['root_test_regex'])
        py_stage = _stage(plan, 'python_parallel')
        self.assertIn('scripts/test_check.py', py_stage['scripts_tests'])
        self.assertIn('scripts/test_disk_space.py', py_stage['scripts_tests'])

    def test_go_dependency_propagation_for_bootstrap_and_setupcli(self):
        plan = check.plan_changed([('internal/bootstrap/probe.go', 'M')], root=check.ROOT)
        self.assertFalse(plan['escalated_to_full'])
        self.assertTrue(_stage(plan, 'lint')['run_lint'])
        go_stage = _stage(plan, 'go')
        self.assertIn('./internal/bootstrap', go_stage['test_packages'])
        self.assertIn('./internal/setupcli', go_stage['test_packages'])
        self.assertIn('./internal/simpledesktop', go_stage['vet_packages'])

    def test_cli_rejects_removed_fast_flag_and_supports_changed_staged_combo(self):
        with self.assertRaises(SystemExit) as ctx:
            check.build_parser().parse_args(['--fast'])
        self.assertEqual(ctx.exception.code, 2)

        buf = io.StringIO()
        with patch.object(check, 'collect_git_changes', return_value=[('docs/engineering/workflow.md', 'M')]) as collect, \
             redirect_stdout(buf):
            code = check.main(['--changed', '--staged', '--dry-run', '--json'], root=check.ROOT, allow_task_wrap=False)
        self.assertEqual(code, 0)
        collect.assert_called_once_with(check.ROOT, staged=True)

        for bad in (['--staged', '--full'], ['--changed', 'HEAD~1', '--staged']):
            with self.subTest(args=bad), self.assertRaises(SystemExit) as bad_ctx:
                check.main(bad, root=check.ROOT, allow_task_wrap=False)
            self.assertEqual(bad_ctx.exception.code, 2)

    def test_unmatched_engine_module_fails_safe_even_alongside_tested_files(self):
        plan = check.plan_changed(
            [
                ('engine/python/test_v2_api.py', 'M'),
                ('engine/python/brand_new_orphan_module.py', 'A'),
            ],
            root=check.ROOT,
        )
        self.assertTrue(plan['escalated_to_full'])
        self.assertTrue(any('brand_new_orphan_module' in r for r in plan['escalation_reasons']))
        self.assertEqual([s['kind'] for s in plan['stages']], ['lint', 'go', 'python_parallel'])


class FullAndReleaseStageTests(unittest.TestCase):
    def test_full_gate_enforces_three_serialized_stages(self):
        stages = check.build_full_stages(check.ROOT, include_release=False, race_pkgs=['./internal/bootstrap'])
        self.assertEqual([(s['stage'], s['kind']) for s in stages], [(1, 'lint'), (2, 'go'), (3, 'python_parallel')])
        self.assertTrue(stages[0]['run_lint'] and stages[0]['run_lint_tests'])
        self.assertEqual(stages[1]['test_packages'], ['./...'])
        self.assertEqual(stages[1]['race_packages'], ['./internal/bootstrap'])
        self.assertTrue(stages[2]['scripts_tests'])
        self.assertTrue(stages[2]['engine_tests'])

    def test_pre_push_plan_covers_full_lint_and_all_script_tests(self):
        stages = check.build_pre_push_stages(check.ROOT)
        self.assertEqual([(s['stage'], s['kind']) for s in stages], [(1, 'lint'), (2, 'python_parallel')])
        self.assertTrue(stages[0]['run_lint'] and stages[0]['run_lint_tests'])
        self.assertIn('scripts/test_lint.py', stages[1]['scripts_tests'])
        self.assertIn('scripts/test_check.py', stages[1]['scripts_tests'])
        self.assertEqual(stages[1]['engine_tests'], [])

    def test_execute_stages_stops_on_stage1_failure_and_preserves_order(self):
        observed: list[str] = []

        def fake_run_cmd(cmd, *, cwd, env=None, quiet_on_success=False):
            observed.append(' '.join(cmd))
            if 'lint.py' in ' '.join(cmd):
                return {'command': cmd, 'exit_code': 1, 'ok': False, 'elapsed_s': 0.01, 'output': 'ML001'}
            return {'command': cmd, 'exit_code': 0, 'ok': True, 'elapsed_s': 0.01, 'output': ''}

        stages = check.build_full_stages(check.ROOT, include_release=False)
        with patch.object(check, '_run_cmd', side_effect=fake_run_cmd), \
             patch.object(check, 'run_python_suite_parallel') as py_run:
            code, results = check.execute_stages(stages, root=check.ROOT, python_bin=sys.executable, jobs=2, json_mode=True)
        self.assertEqual(code, 1)
        self.assertEqual(len(results), 1)
        self.assertEqual(results[0]['name'], 'lint_and_self_test')
        py_run.assert_not_called()

    def test_release_mode_propagates_smoke_exit_codes_0_1_2_3(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            (root / 'scripts').mkdir()
            smoke = root / 'scripts/smoke_release_contracts.py'
            smoke.write_text('# stub\n', encoding='utf-8')
            fake_app = root / 'Memlane'
            fake_app.write_text('#!/bin/sh\n', encoding='utf-8')
            fake_app.chmod(0o700)
            stage = {
                'stage': 4,
                'name': 'release_contract_smoke',
                'kind': 'release_smoke',
                'app_path': str(fake_app),
            }
            for expected_code in (0, 1, 2, 3):
                with self.subTest(exit_code=expected_code):
                    completed = subprocess.CompletedProcess([], expected_code, '{"status":"stub"}\n', '')
                    with patch.object(check.subprocess, 'run', return_value=completed):
                        code, results = check.execute_stages(
                            [stage], root=root, python_bin=sys.executable, jobs=1, json_mode=True
                        )
                    self.assertEqual(code, expected_code)
                    self.assertEqual(results[0]['exit_code'], expected_code)
                    self.assertEqual(results[0]['ok'], expected_code == 0)

    def test_dry_run_json_output_and_no_task_reexec(self):
        buf = io.StringIO()
        with patch.object(check, 'collect_git_changes', return_value=[('docs/engineering/workflow.md', 'M')]), \
             patch.object(check, '_reexec_under_task') as reexec, \
             redirect_stdout(buf):
            code = check.main(['--changed', '--dry-run', '--json'], root=check.ROOT, allow_task_wrap=True)
        self.assertEqual(code, 0)
        reexec.assert_not_called()
        payload = json.loads(buf.getvalue())
        self.assertEqual(payload['schema_version'], 1)
        self.assertEqual(payload['mode'], 'changed')
        self.assertTrue(payload['dry_run'])
        self.assertEqual(payload['stages'][0]['kind'], 'lint')


class ParallelRunnerAndTaskTests(unittest.TestCase):
    def test_bucket_python_modules_isolates_heavy_modules_and_balances_light(self):
        mods = [
            'engine/python/test_generation_probe.py',
            'engine/python/test_agent_connections.py',
            'engine/python/test_alpha.py',
            'engine/python/test_beta.py',
            'engine/python/test_gamma.py',
            'engine/python/test_delta.py',
        ]
        buckets = check.bucket_python_modules(mods, max_workers=3)
        self.assertIn(['engine/python/test_generation_probe.py'], buckets)
        self.assertIn(['engine/python/test_agent_connections.py'], buckets)
        flattened = sorted(item for b in buckets for item in b)
        self.assertEqual(flattened, sorted(mods))

    def test_parallel_runner_executes_isolated_temp_modules_and_reports_failures(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            scripts_dir = root / 'scripts'
            scripts_dir.mkdir()
            (scripts_dir / 'test_one.py').write_text(
                'import os, unittest\n'
                'class One(unittest.TestCase):\n'
                '    def test_ok(self):\n'
                '        self.assertTrue(os.environ.get("TMPDIR", "").endswith("/"))\n',
                encoding='utf-8',
            )
            (scripts_dir / 'test_two.py').write_text(
                'import unittest\n'
                'class Two(unittest.TestCase):\n'
                '    def test_ok(self):\n'
                '        self.assertEqual(1 + 1, 2)\n',
                encoding='utf-8',
            )
            ok_res = check.run_python_suite_parallel(
                sys.executable, root, ['scripts/test_one.py', 'scripts/test_two.py'], [], jobs=2, json_mode=True
            )
            self.assertTrue(ok_res['ok'])
            self.assertEqual(ok_res['tests_run'], 2)

            (scripts_dir / 'test_fail.py').write_text(
                'import unittest\n'
                'class Fail(unittest.TestCase):\n'
                '    def test_boom(self):\n'
                '        self.fail("intentional")\n',
                encoding='utf-8',
            )
            fail_res = check.run_python_suite_parallel(
                sys.executable, root, ['scripts/test_one.py', 'scripts/test_fail.py'], [], jobs=2, json_mode=True
            )
            self.assertFalse(fail_res['ok'])
            self.assertEqual(fail_res['exit_code'], 1)

    def test_parallel_runner_fails_closed_on_nonzero_worker_exit_after_ok_marker(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            scripts_dir = root / 'scripts'
            scripts_dir.mkdir()
            (scripts_dir / 'test_atexit_crash.py').write_text(
                'import atexit, os, sys, unittest\n'
                'def _crash_on_exit():\n'
                '    sys.stderr.write("simulated interpreter teardown crash\\n")\n'
                '    sys.stderr.flush()\n'
                '    os._exit(2)\n'
                'atexit.register(_crash_on_exit)\n'
                'class ExitCrash(unittest.TestCase):\n'
                '    def test_pass_before_teardown_crash(self):\n'
                '        self.assertTrue(True)\n',
                encoding='utf-8',
            )
            bucket_res = check._run_python_bucket(
                sys.executable, root, 'scripts', ['scripts/test_atexit_crash.py']
            )
            self.assertFalse(bucket_res['ok'])
            self.assertEqual(bucket_res['exit_code'], 2)
            self.assertEqual(bucket_res['tests_run'], 1)
            self.assertIn('simulated interpreter teardown crash', bucket_res['output'])

            suite_res = check.run_python_suite_parallel(
                sys.executable, root, ['scripts/test_atexit_crash.py'], [], jobs=1, json_mode=True
            )
            self.assertFalse(suite_res['ok'])
            self.assertEqual(suite_res['exit_code'], 1)
            self.assertFalse(suite_res['buckets'][0]['ok'])
            self.assertEqual(suite_res['buckets'][0]['exit_code'], 2)

    def test_task_py_wait_timeout_eliminates_two_second_sleep_on_short_commands(self):
        # Run task.py directly in a fresh env without MEMLANE_DISK_TASK to verify fast exit.
        env = {k: v for k, v in os.environ.items() if k != 'MEMLANE_DISK_TASK'}
        started = time.perf_counter()
        proc = subprocess.run(
            [sys.executable, str(check.ROOT / 'scripts/task.py'), '--', sys.executable, '-c', 'pass'],
            cwd=str(check.ROOT),
            env=env,
            capture_output=True,
            text=True,
            timeout=15,
            check=False,
        )
        elapsed = time.perf_counter() - started
        self.assertEqual(proc.returncode, 0, proc.stderr)
        self.assertLess(elapsed, 1.8, f'task.py short command still slept ~2s: {elapsed:.2f}s')


if __name__ == '__main__':
    unittest.main()
