"""Unit tests for disk-space cleanup hygiene."""
from collections import namedtuple
import importlib.util
import json
import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch


SCRIPTS_DIR = Path(__file__).resolve().parent


def _load_module(name, filename):
    spec = importlib.util.spec_from_file_location(name, SCRIPTS_DIR / filename)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


disk = _load_module('runtime_hygiene_disk', 'disk-space.py')


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
            go_bin = str(root / '.local/toolchains/go-1.27.4/go/bin/go')
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
