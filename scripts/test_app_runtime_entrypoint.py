"""Installed resources must not execute the checkout disk cleaner."""
import importlib.util
import json
import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('app_runtime_dev', Path(__file__).with_name('dev.py'))
dev = importlib.util.module_from_spec(spec)
spec.loader.exec_module(dev)


class AppRuntimeEntrypointTests(unittest.TestCase):
    def test_installed_resource_start_and_stop_never_enter_checkout_wrapper(self):
        with tempfile.TemporaryDirectory() as temporary:
            digest = 'a' * 64
            root = Path(temporary) / ('runtime-' + digest[:16])
            root.mkdir()
            marker = root / '.memlane-runtime.json'
            marker.write_text(json.dumps({'sha256': digest}))
            marker.chmod(0o600)
            for action in ('start', 'stop', 'status'):
                with self.subTest(action=action), patch.object(dev, 'ROOT', root), \
                     patch.object(dev, 'main', return_value=0) as main, \
                     patch.object(dev.subprocess, 'call') as checkout, \
                     patch.dict(os.environ, {'MEMLANE_DISK_TASK': ''}), \
                     patch.object(dev.sys, 'argv', ['dev.py', action]):
                    self.assertEqual(dev.run_entrypoint(), 0)
                    main.assert_called_once_with()
                    checkout.assert_not_called()

    def test_checkout_still_uses_development_guard(self):
        with tempfile.TemporaryDirectory() as temporary, \
             patch.object(dev, 'ROOT', Path(temporary)), \
             patch.object(dev, 'main') as main, \
             patch.object(dev.subprocess, 'call', return_value=7) as checkout, \
             patch.dict(os.environ, {'MEMLANE_DISK_TASK': ''}):
            self.assertEqual(dev.run_entrypoint(), 7)
            self.assertIn(str(Path(temporary) / 'scripts/task.py'), checkout.call_args.args[0])
            main.assert_not_called()

    def test_foreign_marker_does_not_reclassify_checkout_as_app(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            marker = root / '.memlane-runtime.json'
            marker.write_text(json.dumps({'sha256': 'a' * 64}))
            marker.chmod(0o600)
            with patch.object(dev, 'ROOT', root), self.assertRaises(RuntimeError):
                dev.run_entrypoint()
