import json
import fcntl
import os
from pathlib import Path
import subprocess
import unittest
from unittest.mock import patch

from test_first_install_runtime import FIXED_ORIGINS, FirstRuntimeFixture
from first_install_store import read_private, write_private
from first_install_runtime import (
    _check_activation_scope, _validate_runtime_store, activation_scope, finish_activation, prepare_activation,
)
from generation_config import GenerationStore
from generation_startup import start_generation
from project_policy import PolicyError


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

    @patch('first_install_runtime.unused_origins', return_value=FIXED_ORIGINS)
    def test_changed_selection_is_rejected_before_stopping_any_owner(self, _):
        config, request, path = self.prepare_candidate()
        original = read_private(config['generation_file'])
        for status, field, value, code in (
                ('approved', 'next', None, 'first_activation_precondition_failed'),
                ('approved', 'revision', -1, 'first_activation_precondition_failed'),
                ('approved', 'validations', {}, 'generation_validation_required'),
                ('stopping', 'next', None, 'first_activation_precondition_failed'),
                ('starting', 'validations', {}, '')):
            with self.subTest(status=status, field=field):
                write_private(path, json.dumps({**request, 'status': status}))
                write_private(config['generation_file'], json.dumps({**json.loads(original), field: value}))
                with patch('subprocess.run') as stop:
                    with self.assertRaises((ValueError, PolicyError)):
                        prepare_activation(config, self.path, path)
                    stop.assert_not_called()
                saved = json.loads(read_private(path))
                if status in ('approved', 'stopping'):
                    self.assertEqual(saved, {**request, 'status': 'rejected', 'phase': 'pre_mutation',
                                             'error_code': code})
                else:
                    self.assertEqual(saved, {**request, 'status': status})
                self.assertTrue(json.loads(read_private(self.path))['first_install']['setup'])

    @patch('first_install_runtime.unused_origins', return_value=FIXED_ORIGINS)
    def test_approved_activation_rejects_scope_change_under_owner_lock(self, _):
        config, request, path = self.prepare_candidate()
        with patch('first_install_runtime.activation_scope',
                   side_effect=[False, ValueError('selection changed')]) as scope, patch('subprocess.run') as stop:
            with self.assertRaisesRegex(ValueError, 'selection changed'):
                prepare_activation(config, self.path, path)
            self.assertEqual(scope.call_count, 2)
            stop.assert_not_called()
        self.assertEqual(json.loads(read_private(path)), {
            **request, 'status': 'rejected', 'phase': 'pre_mutation', 'error_code': 'first_activation_precondition_failed'})
        self.assertTrue(json.loads(read_private(self.path))['first_install']['setup'])

    @patch('first_install_runtime.unused_origins', return_value=FIXED_ORIGINS)
    def test_generation_read_oserror_rejects_before_stopping(self, _):
        config, request, path = self.prepare_candidate()

        def read_with_failure(filename):
            if filename == config['generation_file']:
                raise OSError('synthetic generation read failure')
            return read_private(filename)

        before = self.path.read_bytes()
        with (patch('first_install_runtime.read_private', side_effect=read_with_failure),
              patch('subprocess.run') as stop):
            with self.assertRaisesRegex(OSError, 'synthetic generation read failure'):
                prepare_activation(config, self.path, path)
        stop.assert_not_called()
        self.assertEqual(json.loads(read_private(path)), {
            **request, 'status': 'rejected', 'phase': 'pre_mutation', 'error_code': 'first_activation_precondition_failed'})
        self.assertEqual(self.path.read_bytes(), before)

    @patch('first_install_runtime.unused_origins', return_value=FIXED_ORIGINS)
    def test_selection_drift_during_stop_rejects_before_config_mutation(self, _):
        config, request, path = self.prepare_candidate()
        owner = self.root / '.memlane-dev-owner.json'
        write_private(owner, '{"phase":"first_install_setup"}')
        before = self.path.read_bytes()

        def stop_with_drift(cmd, **kwargs):
            self.assertEqual(json.loads(read_private(path))['status'], 'stopping')
            owner.unlink()
            generation = json.loads(read_private(config['generation_file']))
            write_private(config['generation_file'], json.dumps({**generation, 'next': None}))
            return subprocess.CompletedProcess(cmd, 0)

        with patch('subprocess.run', side_effect=stop_with_drift) as stop:
            with self.assertRaisesRegex(ValueError, 'selection changed'):
                prepare_activation(config, self.path, path)
        stop.assert_called_once()
        self.assertEqual(json.loads(read_private(path)), {
            **request, 'status': 'rejected', 'phase': 'pre_mutation', 'error_code': 'first_activation_precondition_failed'})
        self.assertEqual(self.path.read_bytes(), before)
        self.assertTrue(json.loads(before)['first_install']['setup'])

    @patch('first_install_runtime.unused_origins', return_value=FIXED_ORIGINS)
    def test_activation_rereads_concurrently_refreshed_graph_assets_under_flock(self, _):
        config, _, path = self.prepare_candidate()
        owner = self.root / '.memlane-dev-owner.json'
        write_private(owner, '{"phase":"first_install_setup"}')
        refreshed = str(self.parent / ('runtime-' + 'b' * 16) / 'web/official-graph/dist')
        latest = {**config, 'graph_assets': refreshed}
        scope_checks = []

        def stop_and_refresh(cmd, **kwargs):
            owner.unlink()
            write_private(self.path, json.dumps(latest))
            return subprocess.CompletedProcess(cmd, 0)

        def check_scope(snapshot, receipt, receipt_path):
            scope_checks.append(snapshot)
            if len(scope_checks) == 2:
                self.assert_config_parent_locked()
            return _check_activation_scope(snapshot, receipt, receipt_path)

        with (patch('subprocess.run', side_effect=stop_and_refresh) as stop,
              patch('first_install_runtime._check_activation_scope', side_effect=check_scope)):
            self.assertTrue(prepare_activation(config, self.path, path))
        stop.assert_called_once()
        self.assertEqual(scope_checks, [config, latest])
        self.assertEqual(json.loads(read_private(self.path)),
                         {**latest, 'first_install': {**latest['first_install'], 'setup': False}})
        self.assertEqual(json.loads(read_private(path))['status'], 'starting')

    def assert_config_parent_locked(self):
        fd = os.open(self.path.parent, os.O_RDONLY | os.O_DIRECTORY)
        try:
            with self.assertRaises(BlockingIOError):
                fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
        finally:
            os.close(fd)

    @patch('first_install_runtime.unused_origins', return_value=FIXED_ORIGINS)
    def test_activation_rejects_replaced_generation_file_under_config_flock(self, _):
        config, request, path = self.prepare_candidate()
        owner = self.root / '.memlane-dev-owner.json'
        write_private(owner, '{"phase":"first_install_setup"}')
        latest = {**config, 'generation_file': str(self.root / 'replacement-generation.json')}
        write_private(latest['generation_file'], read_private(config['generation_file']))

        def stop_and_replace(cmd, **kwargs):
            owner.unlink()
            write_private(self.path, json.dumps(latest))
            return subprocess.CompletedProcess(cmd, 0)

        def validate(snapshot, root, specification):
            self.assert_config_parent_locked()
            self.assertEqual(snapshot, latest)
            return _validate_runtime_store(snapshot, root, specification)

        with (patch('subprocess.run', side_effect=stop_and_replace),
              patch('first_install_runtime._validate_runtime_store', side_effect=validate) as validate_store,
              patch('first_install_runtime._check_activation_scope', wraps=_check_activation_scope) as check_scope):
            with self.assertRaisesRegex(ValueError, 'existing configuration belongs to another installation'):
                prepare_activation(config, self.path, path)
        validate_store.assert_called_once()
        check_scope.assert_called_once()
        self.assertEqual(json.loads(read_private(path)), {**request, 'status': 'stopping'})
        self.assertEqual(json.loads(read_private(self.path)), latest)

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
        state['connected_agent_selections'] = {request['digest']: True}
        write_private(config['generation_file'], json.dumps(state))
        for allow_applied in (False, True):
            with self.assertRaisesRegex(PolicyError, 'generation_validation_required'):
                activation_scope(config, request, allow_applied=allow_applied)
        state['active'] = request['reference']
        write_private(config['generation_file'], json.dumps(state))
        self.assertTrue(activation_scope(config, request, allow_applied=True))

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
