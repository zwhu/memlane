"""Isolated TRAE IDE event and diagnostics tests."""
from contextlib import redirect_stdout
from copy import deepcopy
import errno
import io
import json
from pathlib import Path
import sys
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import Mock, patch

import trae_ide_hook as hook


class TurnTests(unittest.TestCase):
    def setUp(self):
        self.state = {}
        self.entries = []
        self.allowed = self.enabled = True
        test = self

        class SessionState:
            acquired = True

            def __init__(self, key):
                self.data = deepcopy(test.state)

            def __enter__(self):
                return self

            def __exit__(self, *error):
                pass

            def save(self):
                test.state = deepcopy(self.data)

        modules = {
            '_capture_policy': SimpleNamespace(capture_enabled=lambda: self.enabled, sanitize=lambda value: value),
            '_coding_memory': SimpleNamespace(_SessionState=SessionState, _truncate=lambda value, size: value[:size]),
            '_memlane_policy': SimpleNamespace(permit=lambda permission: self.allowed),
        }
        self.enterContext(patch.dict(sys.modules, modules))
        self.enterContext(patch.object(hook.memory, 'setup', return_value=('key', 'session', 'dataset')))
        self.begin = self.enterContext(patch.object(hook.memory, 'begin_turn'))
        self.handle = self.enterContext(patch.object(hook.memory, 'handle', return_value={'context': 'recalled'}))
        self.enterContext(patch.object(hook.memory, 'capture',
                                      side_effect=lambda request, *_: self.entries.append(request)))

    def run_hook(self, event, **fields):
        return hook.handle(event, {'hook_event_name': event, 'cwd': '/tmp', 'workspace_roots': ['/tmp'],
                                   'session_id': 'session', **fields})

    def qa(self):
        return [(request['entry']['question'], request['entry']['answer']) for request in self.entries
                if request['entry']['type'] == 'qa']

    def test_prompts_and_replies_use_fifo(self):
        for question in ('Q1', 'Q2'):
            self.assertEqual(self.run_hook('UserPromptSubmit', prompt=question),
                             {'hookSpecificOutput': {'hookEventName': 'UserPromptSubmit', 'additionalContext': 'recalled'}})
        turns = self.state['trae_ide_turns']
        for answer in ('A1', 'A2'):
            self.run_hook('Stop', last_assistant_message=answer)
        self.assertEqual(self.qa(), [('Q1', 'A1'), ('Q2', 'A2')])
        self.assertEqual([call.args[0]['event_id'] for call in self.begin.call_args_list], [turn['id'] for turn in turns])
        self.assertEqual([request['event_id'] for request in self.entries], ['qa:' + turn['id'] for turn in turns])
        self.assertEqual(self.state['trae_ide_turns'], [])

    def test_legacy_slot_migrates_and_session_start_clears_both_formats(self):
        self.state['trae_ide_turn'] = {'id': 'legacy', 'time': 'then', 'question': 'old'}
        self.run_hook('UserPromptSubmit', prompt='new')
        self.assertNotIn('trae_ide_turn', self.state)
        self.run_hook('Stop', last_assistant_message='old answer')
        self.assertEqual(self.qa(), [('old', 'old answer')])
        self.state['trae_ide_turn'] = {'id': 'stale', 'time': 'then', 'question': 'stale'}
        self.run_hook('SessionStart')
        self.assertNotIn('trae_ide_turn', self.state)
        self.assertNotIn('trae_ide_turns', self.state)
        self.run_hook('Stop', last_assistant_message='ignored')
        self.assertEqual(len(self.qa()), 1)
        self.assertEqual(self.handle.call_args.args[0]['operation'], 'flush')

    def test_queue_evicts_oldest_at_capacity(self):
        for number in range(hook.MAX_PENDING_TURNS + 2):
            self.run_hook('UserPromptSubmit', prompt=f'Q{number}')
        self.assertEqual([turn['question'] for turn in self.state['trae_ide_turns']],
                         [f'Q{number}' for number in range(2, hook.MAX_PENDING_TURNS + 2)])

    def test_tool_failure_traced_and_interrupt_drains_pending_turns(self):
        self.run_hook('UserPromptSubmit', prompt='earlier')
        self.run_hook('UserPromptSubmit', prompt='current')
        for number in range(10):
            self.run_hook('PostToolUseFailure', tool_use_id=str(number), tool_name=f'Tool{number}', error='tool error')
        self.assertEqual(self.state['trae_ide_turns'][-1]['tool_failures'], [f'Tool{number}' for number in range(2, 10)])
        self.assertEqual(self.handle.call_args.args[0]['operation'], 'flush')
        self.run_hook('PostToolUseFailure', tool_use_id='last', tool_name='LastTool', tool_input={'sample': True},
                      error='interrupted', is_interrupt=True)
        request = self.entries[-3]
        self.assertEqual(request['event_id'], 'tool:last')
        entry = request['entry']
        self.assertEqual((entry['status'], entry['error_message'], entry['method_return_value']),
                         ('error', 'interrupted', 'interrupted'))
        self.assertEqual(entry['method_params'], {'sample': True})
        self.assertEqual(self.qa(), [
            ('earlier', '[Turn interrupted before assistant completion]'),
            ('current', '[Turn interrupted after tool failure: Tool3, Tool4, Tool5, Tool6, Tool7, Tool8, Tool9, LastTool]'),
        ])
        self.assertEqual(self.state['trae_ide_turns'], [])
        self.assertEqual(self.handle.call_args.args[0]['operation'], 'flush')

    def test_session_end_drains_in_order_and_only_last_can_have_answer(self):
        marker = '[Turn interrupted before assistant completion]'
        for fields, expected in (({}, marker), ({'last_assistant_message': 'final answer'}, 'final answer')):
            with self.subTest(fields=fields):
                self.entries.clear()
                self.run_hook('UserPromptSubmit', prompt='Q1')
                self.run_hook('UserPromptSubmit', prompt='Q2')
                self.run_hook('SessionEnd', **fields)
                self.assertEqual(self.qa(), [('Q1', marker), ('Q2', expected)])
                self.assertEqual(self.state['trae_ide_turns'], [])
                self.assertTrue(self.handle.call_args.args[0]['final'])
                self.assertEqual(self.handle.call_args.args[0]['operation'], 'flush')

    def test_stop_without_nonempty_answer_captures_interruption(self):
        for fields in ({}, {'last_assistant_message': '   '}):
            self.run_hook('UserPromptSubmit', prompt='Q')
            self.run_hook('Stop', **fields)
        self.assertEqual(self.qa(), [('Q', '[Turn interrupted before assistant completion]')] * 2)

    def test_policy_tombstone_keeps_paused_prompt_and_answer_out_of_capture(self):
        self.run_hook('UserPromptSubmit', prompt='allowed')
        self.enabled = False
        self.run_hook('UserPromptSubmit', prompt='private paused prompt')
        self.enabled = True
        self.allowed = False
        self.run_hook('UserPromptSubmit', prompt='private denied prompt')
        self.allowed = True
        self.assertEqual(len(self.state['trae_ide_turns']), 3)
        for tombstone in self.state['trae_ide_turns'][1:]:
            self.assertEqual(set(tombstone), {'id', 'time', 'skipped_policy'})
            self.assertTrue(tombstone['skipped_policy'])
        self.begin.assert_called_once()
        self.run_hook('Stop', last_assistant_message='allowed answer')
        self.run_hook('Stop', last_assistant_message='private paused answer')
        self.run_hook('SessionEnd', last_assistant_message='private denied answer')
        self.assertEqual(self.qa(), [('allowed', 'allowed answer')])


class ForwardTests(unittest.TestCase):
    def setUp(self):
        self.root = Path(self.enterContext(tempfile.TemporaryDirectory()))
        self.directory = self.root / '.cognee-plugin' / 'trae_ide-fixture'
        self.diagnostic = self.directory / hook.HOOK_ERROR_FILE
        self.token = self.root / 'token'
        self.token.write_text('synthetic-local-credential')
        self.token.chmod(0o600)
        self.marker = self.root / 'binding.json'
        self.binding = {'instance_id': 'fixture', 'host_kind': 'trae_ide', 'hook_origin': 'http://127.0.0.1:8080',
                        'hook_token_file': str(self.token)}
        self.write_binding()
        self.enterContext(patch.object(hook, 'binding_path', return_value=self.marker))
        self.enterContext(patch.object(Path, 'home', return_value=self.root))
        self.enterContext(patch.dict(sys.modules, {'_plugin_common': None}))
        self.connection = self.enterContext(patch.object(hook.http.client, 'HTTPConnection')).return_value
        self.response = self.connection.getresponse.return_value
        self.response.status = 503
        self.raw = json.dumps({'prompt': 'private prompt', 'tool_input': 'private tool data',
                               'sk-secret-as-key': 'private value'}).encode()

    def write_binding(self):
        self.marker.write_text(json.dumps(self.binding))
        self.marker.chmod(0o600)

    def forward(self):
        self.assertEqual(hook.forward('UserPromptSubmit', self.raw), {})

    def entries(self):
        return json.loads(self.diagnostic.read_text())['entries']

    def test_forward_failure_codes_are_content_free_and_bounded(self):
        self.forward()
        self.assertEqual(self.entries()[-1]['status'], 503)
        self.assertEqual(self.entries()[-1]['error_code'], 'http_503')
        self.connection.request.side_effect = ConnectionRefusedError('private network exception')
        self.forward()
        self.assertEqual(self.entries()[-1]['error_type'], 'ConnectionRefusedError')
        self.assertEqual(self.entries()[-1]['error_code'], 'network_error')
        self.binding['host_kind'] = 'wrong'
        self.write_binding()
        for _ in range(6):
            self.forward()
        entries = self.entries()
        self.assertEqual(len(entries), 5)
        self.assertEqual(entries[-1]['error_code'], 'invalid_binding')
        self.assertEqual(entries[-1]['payload_keys'], ['prompt', 'tool_input'])
        self.assertLessEqual(self.diagnostic.stat().st_size, 16384)
        self.assertEqual(self.directory.stat().st_mode & 0o777, 0o700)
        self.assertEqual(self.diagnostic.stat().st_mode & 0o777, 0o600)
        for private in ('private', 'credential', 'sk-secret'):
            self.assertNotIn(private, self.diagnostic.read_text())

    def test_invalid_origin_token_and_response(self):
        self.binding['hook_origin'] = 'https://example.test'
        self.write_binding()
        self.forward()
        self.binding['hook_origin'] = 'http://127.0.0.1:8080'
        self.write_binding()
        self.token.chmod(0o644)
        self.forward()
        self.token.chmod(0o600)
        self.response.status = 200
        self.response.read.return_value = b'x' * (hook.memory.MAX_INPUT + 1)
        self.forward()
        self.response.read.return_value = b'private server response'
        self.forward()
        self.assertEqual([item['error_code'] for item in self.entries()],
                         ['invalid_origin', 'invalid_token_file', 'response_too_large', 'invalid_response_json'])

    def test_symlinks_and_denied_sandbox_writes_are_ignored(self):
        with patch.object(Path, 'mkdir', side_effect=PermissionError(errno.EPERM, 'private sandbox path')):
            self.forward()
        self.assertFalse(self.diagnostic.exists())
        outside = self.root / 'outside'
        outside.mkdir(mode=0o700)
        self.directory.parent.mkdir(mode=0o700)
        self.directory.symlink_to(outside, target_is_directory=True)
        self.forward()
        self.assertFalse((outside / hook.HOOK_ERROR_FILE).exists())
        self.directory.unlink()
        self.directory.mkdir(mode=0o700)
        untouched = outside / hook.HOOK_ERROR_FILE
        untouched.write_text('untouched')
        self.diagnostic.symlink_to(untouched)
        self.forward()
        self.assertEqual(untouched.read_text(), 'untouched')
        self.assertTrue(self.diagnostic.is_symlink())

    def test_main_reports_dispatch_exception_and_oversized_input_and_prints_neutral_output(self):
        log = Mock()
        with patch.dict(sys.modules, {'_plugin_common': SimpleNamespace(_PLUGIN_DIR=self.directory, hook_log=log)}):
            for raw in (self.raw, b'a' * (hook.memory.MAX_INPUT + 1)):
                output = io.StringIO()
                with (patch.object(sys, 'stdin', SimpleNamespace(buffer=io.BytesIO(raw))),
                      patch.object(sys, 'argv', ['trae_ide_hook.py', 'UserPromptSubmit', '--dispatch']),
                      patch.object(hook, 'handle', side_effect=ValueError('private prompt')),
                      redirect_stdout(output)):
                    hook.main()
                self.assertEqual(json.loads(output.getvalue()), {})
        log.assert_called_once_with('memlane_host_operation_unconfirmed', {'host': 'trae_ide'})
        self.assertEqual([item['error_code'] for item in self.entries()], ['hook_exception', 'input_too_large'])
        self.assertNotIn('private prompt', self.diagnostic.read_text())
