import json
from pathlib import Path
import shlex
import subprocess
import sys
import tempfile
import unittest

from cursor_connection import loader, proposal
from project_policy import PolicyError


class CursorLoaderTests(unittest.TestCase):
    def test_command_preserves_quoted_generation_path_and_emits_only_one_path(self):
        with tempfile.TemporaryDirectory() as temporary:
            path = str(Path(temporary) / 'space \' quote $(touch injected) `touch injected`/cognee')
            command = loader({'plugin_dir': path}, sys.executable)['command']
            result = subprocess.run(command, shell=True, cwd=temporary, capture_output=True, text=True, check=True)
            self.assertEqual(json.loads(result.stdout), {'pluginPaths': [path]})
            self.assertFalse((Path(temporary) / 'injected').exists())
            self.assertEqual(shlex.split(command)[0], sys.executable)

    def test_append_and_replace_preserves_other_hooks_and_config_fields(self):
        raw = json.dumps({'version': 1, 'hooks': {'stop': [{'command': 'echo unrelated'}]}, 'x': ['中文']}).encode()
        old = {'command': 'echo cognee-old'}
        new = {'command': 'echo cognee-new'}
        first = proposal(raw, None, old)
        second = json.loads(proposal(first, old, new))
        self.assertEqual(second['hooks']['workspaceOpen'], [new])
        self.assertEqual(second['hooks']['stop'], [{'command': 'echo unrelated'}])
        self.assertEqual(second['x'], ['中文'])

    def test_missing_or_duplicated_owned_entry_requires_review(self):
        old = {'command': 'echo cognee-old'}
        for entries in ([], [old, old], [{'command': 'echo changed'}]):
            with self.subTest(entries=entries), self.assertRaisesRegex(PolicyError, 'cursor_loader_changed_requires_review'):
                proposal(json.dumps({'version': 1, 'hooks': {'workspaceOpen': entries}}).encode(), old, None)

    def test_unmanaged_cognee_hook_is_preserved_and_rejected(self):
        for event in ('workspaceOpen', 'stop'):
            raw = json.dumps({'version': 1, 'hooks': {event: [{'command': 'cognee-existing'}]}}).encode()
            with self.subTest(event=event), self.assertRaisesRegex(PolicyError, 'duplicate_cursor_memory_hook_requires_review'):
                proposal(raw, None, {'command': 'new'})
        old = {'command': 'cognee-existing'}
        raw = json.dumps({'version': 1, 'hooks': {'workspaceOpen': [old], 'stop': [old]}}).encode()
        with self.assertRaisesRegex(PolicyError, 'duplicate_cursor_memory_hook_requires_review'):
            proposal(raw, old, None)

    def test_invalid_configuration_never_becomes_an_empty_replacement(self):
        for config in ('invalid', '[]', 'null', '{}', '{"version":true,"hooks":{}}',
                       '{"version":1,"hooks":[]}', '{"version":1,"hooks":{"stop":{}}}'):
            with self.subTest(config=config), self.assertRaisesRegex(PolicyError, 'cursor_configuration_requires_review'):
                proposal(config.encode(), None, {'command': 'new'})


if __name__ == '__main__':
    unittest.main()
