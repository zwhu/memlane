import json
import fcntl
from pathlib import Path
import subprocess
import unittest
from unittest.mock import patch

from test_first_install_runtime import FirstRuntimeFixture
from first_install_store import read_private, write_private
from first_install_runtime import activation_scope, prepare_activation, finish_activation
from generation_config import GenerationStore
from generation_startup import start_generation


class FirstActivationTests(FirstRuntimeFixture, unittest.TestCase):
    def test_overlapping_activation_cannot_issue_a_second_shutdown(self):
        config, request, path = self.prepare_candidate()
        with (self.root / 'first-install-activation.lock').open('w') as lock:
            (self.root / 'first-install-activation.lock').chmod(0o600)
            fcntl.flock(lock.fileno(), fcntl.LOCK_EX | fcntl.LOCK_NB)
            with patch('subprocess.run') as stop:
                with self.assertRaises(BlockingIOError):
                    prepare_activation(config, self.path, path)
                stop.assert_not_called()
        self.assertEqual(json.loads(read_private(path)), request)
        self.assertTrue(json.loads(read_private(self.path))['first_install']['setup'])

    def prepare_candidate(self):
        config = self.prepare()
        generation = GenerationStore(config['generation_file'], config['service_commit'])
        try:
            row = generation.put('candidate', {'mode': 'agent_bridge', 'bridge_kind': 'codex',
                                              'model': 'synthetic-model'}, 0, 'save')
            reference = {'id': row['id'], 'revision': row['revision']}
            generation.state['validations']['synthetic-validation'] = {
                'status': 'passed', 'digest': row['digest'], 'plain_passed': True,
                'schema_passed': True, 'quality_passed': True, 'registered_revision': 1}
            generation.select_next(reference, generation.state['revision'], 'select')
            request = {'schema': 1, 'store_id': self.spec['store_id'], 'operation_id': 'first-synthetic-operation',
                       'reference': reference, 'digest': row['digest'], 'revision': generation.state['revision'],
                       'status': 'approved'}
        finally:
            generation.close()
        path = self.root / 'first-install-activation.json'
        write_private(path, json.dumps(request))
        return config, request, path

    def apply_candidate_fixture(self, config, request):
        # Protocol fixture only: launch callback supplies a synthetic health
        # result. No Cognee service, Agent or generation model is started.
        generation = GenerationStore(config['generation_file'], config['service_commit'])
        try:
            start_generation(generation, lambda *args: 'synthetic-owned-health', lambda: True)
        finally:
            generation.close()
        effective = {**config, 'first_install': {**config['first_install'], 'setup': False},
                     'generation_reference': request['reference'], 'generation_mode': 'agent_bridge',
                     'model': 'synthetic-model'}
        return effective

    def test_activation_intent_does_not_itself_complete_setup(self):
        config, request, path = self.prepare_candidate()
        self.assertTrue(prepare_activation(config, self.path, path))
        self.assertFalse(json.loads(read_private(self.path))['first_install']['setup'])
        self.assertEqual(json.loads(read_private(path))['status'], 'starting')
        self.assertIsNone(json.loads(read_private(config['generation_file']))['active'])
        self.assertFalse((self.root / 'client-state.json').exists())

    def test_changed_selection_is_rejected_before_stopping_any_owner(self):
        config, request, path = self.prepare_candidate()
        state = json.loads(read_private(config['generation_file']))
        state['next'] = None
        write_private(config['generation_file'], json.dumps(state))
        with patch('subprocess.run') as stop:
            with self.assertRaises(ValueError):
                prepare_activation(config, self.path, path)
            stop.assert_not_called()
        self.assertTrue(json.loads(read_private(self.path))['first_install']['setup'])

    def test_shutdown_unknown_keeps_original_configuration_and_operation(self):
        config, request, path = self.prepare_candidate()
        write_private(self.root / '.memlane-dev-owner.json', '{"phase":"first_install_setup"}')
        with patch('subprocess.run', side_effect=subprocess.CalledProcessError(42, ['synthetic-stop'])):
            with self.assertRaises(ValueError):
                prepare_activation(config, self.path, path)
        receipt = json.loads(read_private(path))
        self.assertEqual(receipt['status'], 'result_unknown')
        self.assertEqual(receipt['exit_code'], 42)
        self.assertEqual(receipt['operation_id'], request['operation_id'])
        self.assertTrue(json.loads(read_private(self.path))['first_install']['setup'])
        with patch('subprocess.run') as stop:
            with self.assertRaises(ValueError):
                prepare_activation(config, self.path, path)
            stop.assert_not_called()

    def test_complete_requires_applied_reference_and_client_receipt(self):
        config, request, path = self.prepare_candidate()
        prepare_activation(config, self.path, path)
        effective = self.apply_candidate_fixture(config, request)
        with self.assertRaises(FileNotFoundError):
            finish_activation(effective, self.path)
        self.assertEqual(json.loads(read_private(path))['status'], 'starting')
        write_private(self.root / 'client-state.json', '{"schema_version":1,"initialization_completed_at":"2026-11-18T00:00:00+00:00","first_used_date":"2026-11-18","first_used_timezone":"UTC"}')
        finish_activation(effective, self.path)
        self.assertEqual(json.loads(read_private(path))['status'], 'completed')
        self.assertEqual(json.loads(read_private(self.path))['model'], 'synthetic-model')
        self.assertFalse(prepare_activation(json.loads(read_private(self.path)), self.path, path))

    def test_finished_initialization_never_blocks_a_later_normal_profile(self):
        config, request, path = self.prepare_candidate()
        request['status'] = 'completed'
        write_private(path, json.dumps(request))
        original = read_private(path)
        finish_activation({**config, 'generation_reference': {'id': 'later-normal-profile', 'revision': 99}}, self.path)
        self.assertEqual(read_private(path), original)

    def test_validation_must_be_current_and_fully_passed(self):
        config, request, path = self.prepare_candidate()
        state = json.loads(read_private(config['generation_file']))
        state['validations']['later-failure'] = {'digest': request['digest'], 'status': 'failed', 'registered_revision': 999}
        write_private(config['generation_file'], json.dumps(state))
        with self.assertRaises(ValueError):
            activation_scope(config, request)

    def test_setup_stop_detaches_stdin_to_devnull(self):
        config, request, path = self.prepare_candidate()
        owner_path = self.root / '.memlane-dev-owner.json'
        write_private(owner_path, '{"phase":"first_install_setup"}')

        def fake_stop(*args, **kwargs):
            owner_path.unlink(missing_ok=True)
            return subprocess.CompletedProcess(args[0], 0)

        with patch('subprocess.run', side_effect=fake_stop) as stop:
            self.assertTrue(prepare_activation(config, self.path, path))
        stop.assert_called_once()
        self.assertEqual(stop.call_args.kwargs['stdin'], subprocess.DEVNULL)
