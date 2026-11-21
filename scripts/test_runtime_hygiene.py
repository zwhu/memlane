"""Unit tests for daemon polling hygiene, non-watch UI launch, and disk-space cleanup hygiene."""
from collections import namedtuple
import importlib.util
import json
import os
from pathlib import Path
import sys
import tempfile
import time
import unittest
from unittest.mock import Mock, patch


SCRIPTS_DIR = Path(__file__).resolve().parent


def _load_module(name, filename):
    spec = importlib.util.spec_from_file_location(name, SCRIPTS_DIR / filename)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


dev = _load_module('runtime_hygiene_dev', 'dev.py')
disk = _load_module('runtime_hygiene_disk', 'disk-space.py')


class DevMonitorHygieneTests(unittest.TestCase):
    def test_monitor_loop_constants_and_housekeeping_throttling(self):
        self.assertEqual(dev.MONITOR_POLL_INTERVAL, 0.5)
        self.assertEqual(dev.HOUSEKEEPING_INTERVAL, 10.0)
        with tempfile.TemporaryDirectory() as tmp:
            log_dir = Path(tmp) / 'runtime-logs'
            log_dir.mkdir()
            log_file = log_dir / 'official.log'
            log_file.write_bytes(b'hello')
            processes = [Mock(pid=1001)]
            descendants = {}
            save_owner = Mock()

            def fake_track(roots, known):
                known[2002] = 'start-a'

            with patch.object(dev, 'trim_service_log') as trim, \
                 patch.object(dev, 'track_descendants', side_effect=fake_track) as track:
                # Startup pass (last_run=None) runs immediately.
                t0 = dev.run_monitor_housekeeping(
                    log_dir, processes, descendants, save_owner, last_run=None, now=100.0,
                )
                self.assertEqual(t0, 100.0)
                self.assertEqual(trim.call_count, 1)
                self.assertEqual(track.call_count, 1)
                self.assertEqual(save_owner.call_count, 1)

                # Subsequent ticks within < 10s do not fork ps or glob/trim logs.
                for tick in (100.5, 105.0, 109.9):
                    t_next = dev.run_monitor_housekeeping(
                        log_dir, processes, descendants, save_owner, last_run=t0, now=tick,
                    )
                    self.assertEqual(t_next, 100.0)
                self.assertEqual(trim.call_count, 1)
                self.assertEqual(track.call_count, 1)
                self.assertEqual(save_owner.call_count, 1)

                # Once 10s elapsed, housekeeping runs again; unchanged descendants skip save_owner.
                t1 = dev.run_monitor_housekeeping(
                    log_dir, processes, descendants, save_owner, last_run=t0, now=110.0,
                )
                self.assertEqual(t1, 110.0)
                self.assertEqual(trim.call_count, 2)
                self.assertEqual(track.call_count, 2)
                self.assertEqual(save_owner.call_count, 1)

    def test_housekeeping_safely_skips_symlink_logs_without_crashing_monitor(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            log_dir = root / 'runtime-logs'
            log_dir.mkdir()
            outside = root / 'outside.log'
            outside.write_bytes(b'X' * 500)
            (log_dir / 'linked.log').symlink_to(outside)
            healthy = log_dir / 'official.log'
            healthy.write_bytes(b'ok')
            descendants = {}
            save_owner = Mock()

            with patch.object(dev, 'track_descendants'):
                ts = dev.run_monitor_housekeeping(
                    log_dir, [Mock(pid=1001)], descendants, save_owner, last_run=None, now=50.0,
                )
            self.assertEqual(ts, 50.0)
            self.assertEqual(outside.read_bytes(), b'X' * 500)
            self.assertFalse(Path(str(outside) + '.previous').exists())


class DevUILaunchTests(unittest.TestCase):
    def test_reopen_ui_default_launches_native_binary_without_mygo_dev(self):
        with tempfile.TemporaryDirectory() as tmp:
            config_path = Path(tmp) / 'runtime.json'
            binary = Path(tmp) / 'Memlane Dev.app/Contents/MacOS/Memlane Dev'
            launch = Mock(return_value=Mock(pid=555))
            with patch.object(dev, 'ensure_ui_binary', return_value=binary) as ensure:
                proc = dev.reopen_ui(None, [], launch, config_path)
            self.assertEqual(proc.pid, 555)
            ensure.assert_called_once_with(config_path)
            launch.assert_called_once()
            cmd = launch.call_args.args[0]
            env = launch.call_args.kwargs['env']
            self.assertEqual(cmd, [str(binary)])
            self.assertNotIn('mygo', cmd)
            self.assertEqual(env['MEMLANE_DEV_RUNTIME_CONFIG'], str(config_path))

    def test_reopen_ui_watch_mode_opt_in_invokes_mygo_dev(self):
        with tempfile.TemporaryDirectory() as tmp:
            config_path = Path(tmp) / 'runtime.json'
            launch = Mock(return_value=Mock(pid=777))
            fake_env = {'MEMLANE_DEV_RUNTIME_CONFIG': str(config_path), 'GOFLAGS': '-modfile=x'}
            with patch.object(dev, 'ui_env', return_value=fake_env) as build_env, \
                 patch.object(dev, 'ensure_ui_binary') as ensure:
                proc = dev.reopen_ui(None, [], launch, config_path, watch_ui=True)
            self.assertEqual(proc.pid, 777)
            ensure.assert_not_called()
            build_env.assert_called_once_with(config_path)
            self.assertEqual(launch.call_args.args[0], [str(dev.GO), 'tool', 'mygo', 'dev'])
            self.assertEqual(launch.call_args.kwargs['env'], fake_env)

    def test_ui_build_staleness_tracks_go_and_assets_and_ignores_python_and_tests(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            (root / 'cmd/memlane-app').mkdir(parents=True)
            (root / 'internal/simpledesktop').mkdir(parents=True)
            (root / 'assets').mkdir(parents=True)
            (root / 'engine/python').mkdir(parents=True)
            go_main = root / 'cmd/memlane-app/main.go'
            go_test = root / 'internal/simpledesktop/app_test.go'
            py_file = root / 'engine/python/server.py'
            asset = root / 'assets/icon.png'
            for path in (go_main, go_test, py_file, asset, root / 'go.mod'):
                path.write_text('x')
                os.utime(path, (1000, 1000))

            binary = dev.ui_binary_path(root)
            self.assertTrue(dev.ui_build_stale(binary, root))

            binary.parent.mkdir(parents=True)
            binary.write_bytes(b'binary')
            binary.chmod(0o755)
            os.utime(binary, (2000, 2000))
            self.assertFalse(dev.ui_build_stale(binary, root))

            # Modifying Python files or Go *_test.go files does NOT make UI binary stale.
            os.utime(py_file, (3000, 3000))
            os.utime(go_test, (3000, 3000))
            self.assertFalse(dev.ui_build_stale(binary, root))

            # Modifying production Go code makes binary stale.
            os.utime(go_main, (3000, 3000))
            self.assertTrue(dev.ui_build_stale(binary, root))

            # Reset Go mtime; modifying asset makes binary stale.
            os.utime(go_main, (1000, 1000))
            self.assertFalse(dev.ui_build_stale(binary, root))
            os.utime(asset, (3000, 3000))
            self.assertTrue(dev.ui_build_stale(binary, root))

    def test_ensure_ui_binary_skips_build_when_fresh_and_builds_when_missing(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            config_path = root / 'runtime.json'
            binary = dev.ui_binary_path(root)
            with patch.object(dev.subprocess, 'run') as run:
                def fake_build(cmd, **kwargs):
                    out = Path(cmd[cmd.index('-o') + 1])
                    out.write_bytes(b'built')
                    out.chmod(0o755)
                run.side_effect = fake_build
                built = dev.ensure_ui_binary(config_path, root=root, env={'PATH': '/usr/bin'})
                self.assertEqual(built, binary)
                self.assertEqual(run.call_count, 1)
                self.assertEqual(run.call_args.args[0], [str(dev.GO), 'build', '-o', str(binary), './cmd/memlane-app'])
                self.assertTrue((binary.parent.parent / 'Info.plist').is_file())
                self.assertTrue((binary.parent.parent / 'PkgInfo').is_file())

                # Second call finds fresh binary and does not invoke go build.
                again = dev.ensure_ui_binary(config_path, root=root, env={'PATH': '/usr/bin'})
                self.assertEqual(again, binary)
                self.assertEqual(run.call_count, 1)

    def test_cli_watch_ui_validation(self):
        with patch.object(sys, 'argv', ['dev.py', 'status', '--watch-ui']):
            with self.assertRaises(SystemExit) as ctx:
                dev.main()
            self.assertEqual(ctx.exception.code, 2)
        with patch.object(sys, 'argv', ['dev.py', 'start', '--no-ui', '--watch-ui']):
            with self.assertRaises(SystemExit) as ctx:
                dev.main()
            self.assertEqual(ctx.exception.code, 2)


class StalePidReceiptCleanupTests(unittest.TestCase):
    def test_cleanup_removes_only_dead_pid_receipts_and_keeps_live_and_current(self):
        with tempfile.TemporaryDirectory() as tmp:
            daily = Path(tmp)
            dead_runtime = daily / '.runtime-active-999991.json'
            dead_applied = daily / '.generation-applied-999992.json'
            live_runtime = daily / '.runtime-active-4242.json'
            perm_applied = daily / '.generation-applied-4243.json'
            self_runtime = daily / '.runtime-active-7777.json'
            unrelated = daily / 'runtime.json'
            symlink_target = daily / 'target.json'
            symlink_receipt = daily / '.runtime-active-999993.json'

            for path in (dead_runtime, dead_applied, live_runtime, perm_applied,
                         self_runtime, unrelated, symlink_target):
                path.write_text('{}')
            symlink_receipt.symlink_to(symlink_target)

            def fake_kill(pid, sig):
                self.assertEqual(sig, 0)
                if pid in (999991, 999992, 999993):
                    raise ProcessLookupError()
                if pid == 4243:
                    raise PermissionError()
                return None

            with patch.object(dev.os, 'kill', side_effect=fake_kill):
                removed = dev.cleanup_stale_pid_receipts(daily, current_pid=7777)

            self.assertEqual(set(removed), {str(dead_runtime), str(dead_applied)})
            self.assertFalse(dead_runtime.exists())
            self.assertFalse(dead_applied.exists())
            self.assertTrue(live_runtime.exists())
            self.assertTrue(perm_applied.exists())
            self.assertTrue(self_runtime.exists())
            self.assertTrue(unrelated.exists())
            self.assertTrue(symlink_receipt.is_symlink())
            self.assertTrue(symlink_target.exists())


class DiskSpaceHygieneTests(unittest.TestCase):
    def test_prune_cleanup_receipts_keeps_latest_n_and_preserves_metadata_files(self):
        with tempfile.TemporaryDirectory() as tmp:
            log_dir = Path(tmp) / 'disk-cleanup'
            log_dir.mkdir()
            retained_pkg = log_dir / 'retained-package.txt'
            retained_deps = log_dir / 'retained-dependencies.txt'
            custom_json = log_dir / 'manual-audit.json'
            subdir = log_dir / '20261101-000000.json'
            subdir.mkdir()
            retained_pkg.write_text('build/keep\n')
            retained_deps.write_text('/abs/dep\n')
            custom_json.write_text('{"note": true}\n')

            created = []
            for idx in range(125):
                name = f'202611{idx // 60 + 10:02d}-12{idx % 60:02d}00.json'
                path = log_dir / name
                path.write_text('{}')
                created.append(name)
            expected_kept = set(sorted(created)[-100:])

            removed = disk.prune_cleanup_receipts(log_dir, keep=100)
            self.assertEqual(len(removed), 25)
            remaining_receipts = {
                p.name for p in log_dir.iterdir()
                if p.is_file() and disk.RECEIPT_NAME_RE.match(p.name)
            }
            self.assertEqual(remaining_receipts, expected_kept)
            self.assertTrue(retained_pkg.is_file())
            self.assertTrue(retained_deps.is_file())
            self.assertTrue(custom_json.is_file())
            self.assertTrue(subdir.is_dir())

    def test_finish_deferred_noop_skips_new_receipt_and_prunes_existing_receipts(self):
        usage = namedtuple('Usage', 'total used free')(500 * disk.GIB, 350 * disk.GIB, 150 * disk.GIB)
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            log_dir = root / 'build/disk-cleanup'
            log_dir.mkdir(parents=True)
            retained = log_dir / 'retained-package.txt'
            retained.write_text('keep\n')
            for idx in range(105):
                (log_dir / f'20261110-00{idx // 60:02d}{idx % 60:02d}.json').write_text('{}')

            with patch('sys.argv', ['disk-space.py', '--finish']), \
                 patch.object(disk, 'ROOT', root), \
                 patch.object(disk.shutil, 'disk_usage', return_value=usage), \
                 patch.object(disk, 'other_tasks', return_value=True), \
                 patch.object(disk, 'active_arguments', return_value=[]):
                disk.main()

            receipts = [p for p in log_dir.iterdir() if p.is_file() and disk.RECEIPT_NAME_RE.match(p.name)]
            self.assertEqual(len(receipts), 100)
            self.assertTrue(retained.is_file())

    def test_other_tasks_ignores_long_lived_dev_start_but_detects_competing_tasks_and_compilers(self):
        task_abs = str(disk.ROOT / 'scripts/task.py')
        dev_abs = str(disk.ROOT / 'scripts/dev.py')
        dev_start_lines = [
            f'100 /usr/bin/python3 {task_abs} -- /usr/bin/python3 {dev_abs} start --config /tmp/runtime.json',
            '101 python3 scripts/task.py -- python3 scripts/dev.py start --no-ui',
            f'102 python3 {task_abs} -- python3 {dev_abs} --config /tmp/runtime.json',
            '103 python3 scripts/task.py -- python3 scripts/dev.py setup --config /tmp/runtime.json',
        ]
        competing_lines = [
            f'200 /usr/bin/python3 {task_abs} -- sh scripts/go.sh test ./...',
            '201 python3 scripts/task.py -- python3 -m unittest',
            f'202 python3 {task_abs} -- python3 {dev_abs} prepare --component-root /tmp/c',
        ]
        with patch.dict(disk.os.environ, {'MEMLANE_TASK_OWNER_PID': '999'}):
            self.assertFalse(disk.other_tasks(dev_start_lines))
            for line in competing_lines:
                with self.subTest(line=line):
                    self.assertTrue(disk.other_tasks([*dev_start_lines, line]))

        # Active compiler under ROOT still defers cache trim.
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp).resolve()
            go_bin = str(root / '.local/toolchains/go-1.27.3/go/bin/go')
            with patch.object(disk, 'ROOT', root), \
                 patch.object(disk, 'DEPENDENCIES', root / 'absent'), \
                 patch.object(disk, 'active_arguments', return_value=[f'300 {go_bin} test ./...']):
                result = disk.trim_caches(0)
            self.assertEqual(result, ['cache cleanup deferred: compiler is active'])

    def test_retained_dependencies_protects_project_control_and_opencode_paths(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp).resolve()
            daily = root / '.local/daily'
            daily.mkdir(parents=True)
            plugin_dir = root / 'plugins/project'
            opencode_dir = root / 'plugins/opencode'
            registry = daily / 'projects.json'
            mgmt_token = daily / 'mgmt.token'
            agent_a = daily / 'agent-a.token'
            agent_b = daily / 'agent-b.token'
            for directory in (plugin_dir, opencode_dir):
                directory.mkdir(parents=True)
            for file in (registry, mgmt_token, agent_a, agent_b):
                file.write_text('synthetic')
            config = daily / 'runtime.json'
            config.write_text(json.dumps({
                'opencode_plugin_dir': str(opencode_dir),
                'project_control': {
                    'url': 'http://127.0.0.1:18777',
                    'plugin_dir': str(plugin_dir),
                    'registry_file': str(registry),
                    'management_token_file': str(mgmt_token),
                    'agent_control_token_files': {
                        'codex': str(agent_a),
                        'opencode': str(agent_b),
                    },
                },
            }))

            with patch.object(disk, 'ROOT', root), patch.object(disk, 'DEPENDENCIES', root / 'absent'):
                deps = disk.retained_dependencies()

            for expected in (plugin_dir, opencode_dir, registry, mgmt_token, agent_a, agent_b):
                self.assertTrue(disk.protected_path(expected, deps), f'expected {expected} to be protected')

            # Relative path in project_control fails closed.
            config.write_text(json.dumps({
                'project_control': {'plugin_dir': 'relative/plugin'},
            }))
            with patch.object(disk, 'ROOT', root), patch.object(disk, 'DEPENDENCIES', root / 'absent'):
                with self.assertRaisesRegex(RuntimeError, 'cleanup refused'):
                    disk.retained_dependencies()


if __name__ == '__main__':
    unittest.main()
