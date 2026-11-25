"""Resource identity in the owned dev launcher."""
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('dev_runtime_identity', Path(__file__).resolve().parents[2] / 'scripts/dev.py')
dev = importlib.util.module_from_spec(spec)
spec.loader.exec_module(dev)


class DevRuntimeIdentityTests(unittest.TestCase):
    def test_materialized_app_and_checkout_identity(self):
        with tempfile.TemporaryDirectory() as temporary:
            checkout = Path(temporary)
            digest = 'ab' * 32
            root = checkout / ('runtime-' + digest[:16])
            root.mkdir()
            marker = root / '.memlane-runtime.json'
            marker.write_text(json.dumps({'sha256': digest}))
            marker.chmod(0o600)
            with patch.object(dev, 'ROOT', checkout):
                self.assertEqual(dev.runtime_resource_identity(), (None, None))
                self.assertFalse(dev.app_runtime_resources())
            with patch.object(dev, 'ROOT', root):
                self.assertEqual(dev.runtime_resource_identity(), (str(root), digest[:16]))
                self.assertTrue(dev.app_runtime_resources())
