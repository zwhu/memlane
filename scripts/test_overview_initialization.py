import importlib.util
import json
from pathlib import Path
import tempfile
import unittest
import sys
from unittest.mock import patch, MagicMock
from datetime import datetime, timezone, timedelta

spec = importlib.util.spec_from_file_location('dev', Path(__file__).with_name('dev.py'))
dev = importlib.util.module_from_spec(spec)
spec.loader.exec_module(dev)


class InitializationTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name).resolve()
        token = self.root / 'token'
        token.write_text('fixture-key')
        self.config = {'token_file': str(token), 'service_url': 'http://127.0.0.1:61255',
                       'completion_url': 'http://127.0.0.1:18173'}
        self.path = self.root / 'runtime.json'
        self.state = self.root / 'client-state.json'
        self.response = MagicMock()
        self.response.__enter__.return_value.status = 200

    def test_local_completion_date_is_saved_once_and_reconfiguration_preserves_it(self):
        now = datetime(2026, 11, 13, 0, 5, tzinfo=timezone(timedelta(hours=8)))
        with patch.object(dev.urllib.request, 'urlopen', return_value=self.response) as request:
            first = dev.complete_initialization(self.config, self.path, now)
            self.assertEqual(first['first_used_date'], '2026-11-13')
            self.assertEqual(first['initialization_completed_at'], '2026-11-12T16:05:00+00:00')
            original = self.state.read_bytes()
            self.config['model'] = 'changed'
            self.assertEqual(dev.complete_initialization(self.config, self.path, now + timedelta(days=3)), first)
            self.assertEqual(self.state.read_bytes(), original)
            self.assertEqual(request.call_args_list[0].args[0].get_header('X-api-key'), 'fixture-key')
        self.assertEqual(self.state.stat().st_mode & 0o777, 0o600)

    def test_failed_readiness_never_writes_first_day(self):
        with patch.object(dev.urllib.request, 'urlopen', side_effect=RuntimeError('not ready')):
            with self.assertRaises(RuntimeError):
                dev.complete_initialization(self.config, self.path)
        self.assertFalse(self.state.exists())

    def test_corrupt_state_and_failed_save_are_retained_for_retry(self):
        self.state.write_text('{invalid')
        with patch.object(dev.urllib.request, 'urlopen', return_value=self.response):
            with self.assertRaises(json.JSONDecodeError):
                dev.complete_initialization(self.config, self.path)
        self.assertEqual(self.state.read_text(), '{invalid')
        self.state.unlink()
        with patch.object(dev.urllib.request, 'urlopen', return_value=self.response), patch.object(dev.os, 'replace', side_effect=OSError('disk failure')):
            with self.assertRaises(OSError):
                dev.complete_initialization(self.config, self.path)
        self.assertFalse(self.state.exists())
        self.assertEqual(list(self.root.glob('.client-state-*')), [])

    def test_foreground_ui_attaches_without_signalling_services(self):
        self.path.write_text('{}')
        owner = {'pid': 123, 'started': 'fixture', 'config': str(self.path), 'children': [10, 11, 12]}
        (self.root / '.memlane-dev-owner.json').write_text(json.dumps(owner))
        config = dict(self.config, official_data=str(self.root))
        watcher = MagicMock(pid=13)
        with patch.object(sys, 'argv', ['dev.py', 'open', '--watch-ui', '--config', str(self.path)]), \
             patch.object(dev, 'load_config', return_value=config), \
             patch.object(dev, 'acquire', side_effect=BlockingIOError), \
             patch.object(dev, 'complete_initialization') as complete, \
             patch.object(dev.os, 'kill') as send_signal, \
             patch.object(dev.subprocess, 'check_output', return_value='fixture\n'), \
             patch.object(dev.subprocess, 'run', return_value=MagicMock(returncode=0, stdout='Z\n')), \
             patch.object(dev.subprocess, 'Popen', return_value=watcher) as launch, \
             patch.object(dev.signal, 'signal'), \
             patch.object(dev, 'stop_group') as stop:
            dev.main()
        complete.assert_called_once()
        send_signal.assert_called_once_with(123, 0)
        self.assertEqual(launch.call_args.args[0], [str(dev.GO), 'tool', 'mygo', 'dev'])
        stop.assert_called_once_with(watcher)

    def test_foreground_ui_refuses_a_live_existing_watcher(self):
        self.path.write_text('{}')
        owner = {'pid': 123, 'started': 'fixture', 'config': str(self.path), 'children': [10, 11, 12]}
        (self.root / '.memlane-dev-owner.json').write_text(json.dumps(owner))
        with patch.object(sys, 'argv', ['dev.py', 'open', '--watch-ui', '--config', str(self.path)]), \
             patch.object(dev, 'load_config', return_value=dict(self.config, official_data=str(self.root))), \
             patch.object(dev, 'acquire', side_effect=BlockingIOError), \
             patch.object(dev, 'complete_initialization'), \
             patch.object(dev.os, 'kill'), \
             patch.object(dev.subprocess, 'check_output', return_value='fixture\n'), \
             patch.object(dev.subprocess, 'run', return_value=MagicMock(returncode=0, stdout='S\n')), \
             patch.object(dev.subprocess, 'Popen') as launch:
            with self.assertRaisesRegex(RuntimeError, 'watcher is live'):
                dev.main()
        launch.assert_not_called()


if __name__ == '__main__':
    unittest.main()
