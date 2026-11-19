import json
import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import Mock

import dev
from dev_recovery import recover


class RecoveryTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name).resolve()
        self.config_path = self.root / 'runtime.json'
        self.config_path.write_text('{}')
        self.config = {'official_data': str(self.root), 'service_url': 'http://127.0.0.1:19998'}
        self.receipt = self.root / '.memlane-dev-owner.json'
        self.state = {'pid': 987654, 'children': [987655], 'started': 'original',
            'config': str(self.config_path), 'official_data': str(self.root)}
        self.receipt.write_text(json.dumps(self.state))
        self.receipt.chmod(0o600)
        self.ports = Mock()

    def invoke(self, table=None):
        return recover(self.config, self.config_path, acquire=dev.acquire,
                       process_tree=lambda: table or {}, port_free=self.ports)

    def test_recover_requires_released_identities_ports_and_locks(self):
        original = self.receipt.read_bytes()
        for table in ({987654: (1, 987654, 'reused')}, {987655: (1, 987655, 'original')},
                      {123456: (1, 987655, 'descendant')}):
            with self.subTest(table=table), self.assertRaisesRegex(RuntimeError, 'remains'):
                self.invoke(table)
            self.assertEqual(self.receipt.read_bytes(), original)
        self.ports.side_effect = OSError('port occupied')
        with self.assertRaises(OSError):
            self.invoke()
        self.assertEqual(self.receipt.read_bytes(), original)
        self.ports.side_effect = None
        with dev.acquire(self.root):
            with self.assertRaises(BlockingIOError):
                self.invoke()
        result = self.invoke()
        self.assertEqual(result['status'], 'released')
        self.assertEqual(Path(result['archive']).read_bytes(), original)
        self.assertFalse(self.receipt.exists())
        self.assertEqual(self.invoke()['status'], 'already_released')

    def test_recover_rejects_foreign_or_public_receipt(self):
        self.state['official_data'] = str(self.root / 'other')
        self.receipt.write_text(json.dumps(self.state))
        with self.assertRaisesRegex(RuntimeError, 'configuration mismatch'):
            self.invoke()
        self.receipt.chmod(0o644)
        with self.assertRaisesRegex(RuntimeError, 'private owner'):
            self.invoke()

    def test_recover_preserves_active_generation_writer(self):
        import fcntl
        path = self.root / 'generation.json'
        self.config['generation_file'] = str(path)
        with Path(str(path) + '.lock').open('a+') as lock:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
            with self.assertRaises(BlockingIOError):
                self.invoke()
        self.assertTrue(self.receipt.exists())
