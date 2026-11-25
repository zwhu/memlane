from __future__ import annotations

from contextlib import redirect_stderr, redirect_stdout
import io
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import time
import unittest
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).resolve().parent))
import verify_setup_cli as gate

OWNED = {'hooks': [{'type': 'command', 'command':
                   '/usr/bin/python3 -B /fixture/managed-marketplaces/trae_ide-' + 'a' * 24
                   + '/' + 'b' * 20 + '/plugins/cognee/scripts/trae_ide_hook.py Stop'}]}
THIRD_PARTY = {'matcher': 'user', 'hooks': [{'type': 'command', 'command': '/usr/bin/true'}]}
HOOKS = {event: [OWNED] for event in sorted(gate.EVENTS)}
HOOKS['Stop'] = [OWNED, THIRD_PARTY]
HOOKS['PreToolUse'] = [THIRD_PARTY]
LEGACY_HOOKS = {**HOOKS, 'SessionEnd': [OWNED], 'PostToolUseFailure': [], 'Notification': []}

FAKE_CLI = r'''
import fcntl
import json
import os
from pathlib import Path
import subprocess
import sys
import time

home = Path(os.environ['HOME'])
hooks = home / 'hooks.json'
log = home / 'calls.jsonl'
runtime = home / 'runtime.json'
worker_dir = home / 'components' / 'installation'
worker_state = worker_dir / 'worker-state.json'
worker_lock = worker_dir / 'installation.lock'
worker_finish = home / 'finish-worker'
worker_done = home / 'worker-done'
settings = json.loads((home / 'settings.json').read_text())
scenario = settings['scenario']
args = sys.argv[1:]
assert 'MEMLANE_DEV_RUNTIME_CONFIG' not in os.environ

if args == ['worker']:
    worker_dir.mkdir(parents=True, exist_ok=True)
    with worker_lock.open('w') as handle:
        fcntl.flock(handle, fcntl.LOCK_EX)
        worker_state.write_text(json.dumps({'operation_id': 'first-install', 'state': 'running'}))
        deadline = time.monotonic() + 5
        while not worker_finish.exists() and time.monotonic() < deadline:
            time.sleep(.01)
        temp = worker_state.with_suffix('.tmp')
        temp.write_text(json.dumps({'operation_id': 'first-install', 'state': 'completed'}))
        temp.replace(worker_state)
    worker_done.touch()
    sys.exit()

with log.open('a') as handle:
    handle.write(json.dumps(args) + '\n')
assert '--json' in args
assert args[args.index('--config') + 1] == str(runtime)
assert args[args.index('--component-root') + 1] == str(worker_dir.parent)
digest = 'a' * 64
reviewed = 'b' * 64
fingerprint = 'c' * 64
fresh = 'd' * 64
doctor_hosts = [
    {'kind': 'codex', 'instance_id': 'codex-id', 'detected': True},
    {'kind': 'trae_ide', 'instance_id': 'trae-cn-id', 'detected': True},
]
daemon_updated = (home / 'daemon-updated').exists()
if scenario != 'beta2-upgrade' or daemon_updated:
    doctor_hosts[1]['needs_hook_repair'] = settings.get('dirty_hooks', False) and not (home / 'hooks-repaired').exists()
def final_hooks():
    hooks.write_text(json.dumps({'version': 1, 'hooks': settings['final_hooks']}))

def reply(code, **fields):
    statuses = {0: 'ok', 10: 'action_required', 11: 'human_required', 20: 'in_progress_or_busy',
                30: 'preflight_blocked', 40: 'needs_review'}
    print(json.dumps({'schema_version': 1, 'command': args[0], 'exit_code': code,
                      'ok': code == 0, 'status': statuses[code], **fields}, ensure_ascii=False))
    sys.exit(code)

def flag(name):
    return args[args.index(name) + 1]

def reviewed_apply():
    assert '--apply' in args and flag('--reviewed-sha') == reviewed

pending_path = home / 'pending.json'
legacy_op = home / 'generation.json.ui-operations' / 'ui-beta2-conflict.json'

def pending_rows():
    rows = json.loads(pending_path.read_text())
    if legacy_op.exists():
        op = json.loads(legacy_op.read_text())
        rows.append({'operation_id': op['operation_id'], 'kind': op['kind'],
                     'instance_id': op['profile_id'], 'revision': op['profile_revision']})
    return rows

def completed_worker():
    reply(10, operation_id='first-install',
          components={'worker_active': False, 'progress': {'state': 'completed'}},
          blockers=[{'code': 'connection_not_activated'}])

if args[0] == 'install':
    if '--plan' in args:
        reply(10, artifacts={'plan_digest': digest})
    assert '--apply' in args and flag('--plan-digest') == digest and flag('--wait') == '45'
    if scenario == 'half-installed':
        subprocess.Popen([sys.executable, __file__, 'worker'], start_new_session=True,
                         stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        time.sleep(30)
        raise AssertionError('runner did not kill the CLI')
    reply(20, operation_id='first-install', retry_after_ms=settings.get('initial_retry_ms', 2),
          components={'worker_active': True, 'progress': {'state': settings.get('initial_state', 'running')}})

if args[0] == 'doctor':
    if '--operation-id' in args:
        assert flag('--operation-id') == 'first-install'
        if scenario == 'half-installed':
            if not worker_finish.exists():
                worker_finish.touch()
                if settings.get('orphan'):
                    reply(40, blockers=[{'code': 'orphan_install_process'}])
                if settings.get('completed_on_inspection'):
                    until = time.monotonic() + 2
                    while not worker_done.exists() and time.monotonic() < until:
                        time.sleep(.01)
                    assert worker_done.exists()
                    completed_worker()
                reply(20, operation_id='first-install', retry_after_ms=20,
                      components={'worker_active': True, 'progress': {'state': 'running'}})
            if not worker_done.exists():
                reply(20, operation_id='first-install', retry_after_ms=20,
                      components={'worker_active': True, 'progress': {'state': 'running'}})
        if 'poll_state' in settings:
            reply(settings.get('poll_exit', 20), operation_id='first-install', retry_after_ms=2,
                  components={'worker_active': False, 'progress': {'state': settings['poll_state']}})
        if 'polls_before_complete' in settings:
            poll_count = sum(json.loads(line)[:2] == ['doctor', '--operation-id']
                             for line in log.read_text().splitlines())
            if poll_count < settings['polls_before_complete']:
                reply(20, operation_id='first-install', retry_after_ms=1,
                      components={'worker_active': True, 'progress': {'state': 'running'}})
        completed_worker()
    if scenario == 'clean-install':
        assert '--check-network' in args and flag('--mirror') == 'official'
        ok = settings.get('dns_ok', True)
        reply(settings.get('doctor_exit', 10 if ok else 30), environment={'dns_ok': ok},
              connections={'hosts': doctor_hosts})
    assert scenario == 'beta2-upgrade'
    reply(40, daemon={'stale_version_detected': not daemon_updated,
                      'owner_fingerprint': fresh if daemon_updated else fingerprint},
          components={'complete': True},
          connections={'hosts': doctor_hosts, 'pending_ui_operations': pending_rows()})

if args[0] == 'connect':
    kind = flag('--agent')
    host = next(row for row in doctor_hosts if row['kind'] == kind)
    prepared = home / ('prepared-' + kind)
    if '--dry-run' in args:
        assert '--apply' not in args
        if settings.get('unprepared') and not prepared.exists():
            reply(10, connections={'hosts': [host]}, next_actions=[
                {'args': ['connect', '--agent', kind, '--apply', '--phase', 'prepare'],
                 'requires': 'explicit_confirmation'},
            ])
        reply(10, artifacts={'reviewed_sha': reviewed}, connections={'hosts': [host]})
    if '--phase' in args and flag('--phase') == 'prepare':
        assert '--apply' in args and '--reviewed-sha' not in args
        prepared.touch()
        reply(0)
    reviewed_apply()
    if kind == 'trae_ide':
        final_hooks()
    reply(11)

if args[0] == 'verify':
    stage = flag('--stage')
    if stage == 'connection':
        if settings.get('false_positive'):
            reply(0)
        reply(11, human_steps=['Restart host and run a real session.'])
    if scenario == 'beta2-upgrade':
        assert '--expect-zero-downloads' in args and '--expect-runtime-current' in args
        assert (home / 'hooks-repaired').exists()
    reply(0)

if args[:2] == ['repair', 'stale-daemon']:
    if '--dry-run' in args:
        if settings.get('mutate_owner'):
            (home / '.memlane-dev-owner.json').write_text('changed')
        reply(10, artifacts={'owner_fingerprint': fingerprint})
    assert flag('--owner-fingerprint') == fingerprint
    (home / 'daemon-updated').touch()
    reply(0)

if args[:2] == ['repair', 'pending-ops']:
    assert (home / 'daemon-updated').exists()
    pending = json.loads(pending_path.read_text())
    if '--dry-run' in args:
        if '--operation-id' not in args:
            reply(10, connections={'pending_ui_operations': pending_rows()})
        if flag('--operation-id') == 'ui-beta2-conflict':
            assert legacy_op.is_file()
            reply(10, artifacts={'connection_operation': {
                'recorded': False, 'status': 'unrecorded', 'reconcilable': True,
                'safe_to_unlock': False, 'phase': 'pre_mutation',
            }})
        safe = flag('--operation-id') == 'ui-safe'
        reply(10 if safe else 40, artifacts={'connection_operation': {
            'recorded': True, 'status': 'unknown', 'reconcilable': safe, 'reconciled': False,
            'safe_to_unlock': False, 'phase': 'pre_mutation' if safe else 'post_mutation',
        }})
    assert '--apply' in args
    operation = flag('--operation-id')
    if operation == 'ui-beta2-conflict':
        legacy_op.unlink()
        reply(0, artifacts={'connection_operation': {
            'recorded': True, 'status': 'complete', 'reconciled': True, 'safe_to_unlock': True,
        }})
    if operation == 'ui-ambiguous':
        if settings.get('lose_ambiguous'):
            pending_path.write_text('[]')
        reply(40)
    assert operation == 'ui-safe'
    pending_path.write_text(json.dumps([row for row in pending if row['operation_id'] != operation]))
    reply(0)

if args[:2] == ['repair', 'trae-hooks']:
    assert flag('--instance-id') == 'trae-cn-id'
    if '--dry-run' in args:
        if settings.get('mutate_hooks'):
            final_hooks()
        reply(10, artifacts={'reviewed_sha': reviewed})
    reviewed_apply()
    if settings.get('orphan_cleanup'):
        hooks.write_text(json.dumps({'version': 1, 'hooks': settings['third_party_only']}))
    else:
        final_hooks()
    (home / 'hooks-repaired').touch()
    reply(0)

if args[0] == 'stop':
    if '--dry-run' in args:
        reply(10, artifacts={'owner_fingerprint': fresh})
    assert '--apply' in args and flag('--owner-fingerprint') == fresh
    (home / 'stopped').touch()
    reply(0, daemon={'owner_locked': False})
raise AssertionError(args)
'''

FAKE_UNINSTALL = r'''
import json
import os
from pathlib import Path
import sys

home = Path(os.environ['HOME'])
args = sys.argv[1:]
assert '--json' in args and home == Path(args[args.index('--home') + 1])
assert Path(args[args.index('--runtime-dir') + 1]) == home
assert Path(args[args.index('--components-dir') + 1]) == home / 'components'
assert (home / 'stopped').exists()
assert '--remove-data' not in args
applied = '--apply' in args
(home / 'uninstall-mode').write_text('apply' if applied else 'dry-run')
print(json.dumps({'locked_owners': [], 'errors': [], 'dry_run': not applied,
                  'status': 'completed' if applied else 'dry_run'}))
'''


class RunnerTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name).resolve()
        self.executable = self.root / 'Memlane'
        self.hooks = self.root / 'hooks.json'
        self.uninstall = self.root / 'uninstall.py'

    def write_executable(self, body):
        self.executable.write_text(f'#!{sys.executable}\n' + body, encoding='utf-8')
        self.executable.chmod(0o700)

    def write_hooks(self, hooks):
        self.hooks.write_text(json.dumps({'version': 1, 'hooks': hooks}), encoding='utf-8')

    def fixture(self, scenario, **settings):
        self.write_executable(FAKE_CLI)
        self.uninstall.write_text(FAKE_UNINSTALL, encoding='utf-8')
        (self.root / 'settings.json').write_text(json.dumps({
            'scenario': scenario, 'final_hooks': HOOKS, **settings,
        }), encoding='utf-8')
        (self.root / 'runtime.json').write_text(json.dumps({'official_data': str(self.root)}), encoding='utf-8')
        if settings.get('dirty_hooks'):
            self.write_hooks(LEGACY_HOOKS)
        if scenario == 'beta2-upgrade':
            (self.root / '.memlane-dev-owner.json').write_text('initial receipt', encoding='utf-8')
            (self.root / 'pending.json').write_text(json.dumps([
                {'operation_id': 'ui-safe', 'kind': 'connection_prepare', 'revision': 1},
                {'operation_id': 'ui-ambiguous', 'kind': 'connection_activate', 'revision': 2},
            ]), encoding='utf-8')

    def run_gate(self, scenario, *extra):
        argv = ['--executable', str(self.executable), '--scenario', scenario,
                '--home', str(self.root), '--config', str(self.root / 'runtime.json'),
                '--component-root', str(self.root / 'components'), '--hooks-path', str(self.hooks),
                '--uninstall-script', str(self.uninstall), '--mirror', 'official', '--json', *extra]
        output = io.StringIO()
        with redirect_stdout(output):
            code = gate.main(argv)
        return code, json.loads(output.getvalue())

    def assert_gate(self, scenario, *, error=None, args=()):
        code, report = self.run_gate(scenario, *args)
        self.assertEqual(code, int(error is not None), report)
        self.assertEqual(report['returncode'], code)
        receipts = report['scenarios'][0]['steps']
        if error is not None:
            self.assertEqual(receipts[-1]['status'], 'failed', receipts)
            self.assertIn(error, receipts[-1]['error'])
        else:
            self.assertTrue(receipts and all(item['status'] == 'passed' for item in receipts), receipts)
        return receipts

    def calls(self):
        return [json.loads(line) for line in (self.root / 'calls.jsonl').read_text().splitlines()]

    def test_run_cli_validates_envelope_and_scans_for_secrets(self):
        valid = {'schema_version': 1, 'exit_code': 10}
        for payload, stderr, error in [
            ({**valid, 'agent_guidance': gate.GUIDANCE}, '', None),
            ({**valid, 'schema_version': 2}, '', 'schema_version'),
            ({**valid, 'schema_version': True}, '', 'schema_version'),
            ({**valid, 'exit_code': 0}, '', 'return code'),
            (valid, '\n', 'stderr'),
            ('{} {}', '', 'single valid JSON object'),
            ('{"schema_version":1,"exit_code":10,"value":"\\u0073k-example"}', '', 'private value'),
            ({**valid, 'agent_guidance': gate.GUIDANCE, 'other': '.local/daily'}, '', 'private value'),
            *(({**valid, 'leak': marker + 'example'}, '', 'private value') for marker in gate.SECRET_MARKERS),
        ]:
            with self.subTest(payload=payload, error=error):
                raw = json.dumps(payload) if isinstance(payload, dict) else payload
                self.write_executable(
                    'import os, sys\n'
                    'assert "MEMLANE_DEV_RUNTIME_CONFIG" not in os.environ\n'
                    'assert os.environ["TEST_MARKER"] == "present"\n'
                    f'assert os.environ["HOME"] == {str(self.root)!r}\n'
                    f'print({raw!r})\nsys.stderr.write({stderr!r})\nsys.exit(10)\n'
                )
                env = {'HOME': str(self.root), 'TEST_MARKER': 'present',
                       'MEMLANE_DEV_RUNTIME_CONFIG': '/private/config'}
                if error:
                    with self.assertRaisesRegex(gate.VerificationError, error):
                        gate.run_cli(self.executable, ['doctor', '--json'], env=env)
                else:
                    self.assertEqual(gate.run_cli(self.executable, ['doctor', '--json'], env=env).returncode, 10)
                self.assertEqual(env['MEMLANE_DEV_RUNTIME_CONFIG'], '/private/config')

    def test_hooks_schema_and_third_party_groups(self):
        self.write_hooks(HOOKS)
        gate.assert_trae_hooks_4_event_schema(self.hooks, {'Stop': [THIRD_PARTY], 'PreToolUse': [THIRD_PARTY]})
        for event, groups, error in (
            ('SessionEnd', [OWNED], 'unsupported beta.2'),
            ('PostToolUseFailure', [THIRD_PARTY], 'unsupported beta.2'),
            ('Notification', [], 'empty event'),
            ('OtherEvent', [OWNED], 'unsupported event'),
            ('SessionStart', [THIRD_PARTY], 'required event has no Memlane hook group'),
            ('PreToolUse', [{'hooks': [{'command': '/usr/bin/false'}]}], 'third-party hook group'),
        ):
            with self.subTest(event=event):
                self.write_hooks({**HOOKS, event: groups})
                with self.assertRaisesRegex(gate.VerificationError, error):
                    gate.assert_trae_hooks_4_event_schema(self.hooks, {'PreToolUse': [THIRD_PARTY]})

    def test_dry_run_all_makes_no_changes(self):
        code, report = self.run_gate('all', '--dry-run')
        self.assertEqual(code, 0)
        self.assertEqual(report['status'], 'planned')
        self.assertEqual([item['scenario'] for item in report['scenarios']], list(gate.SCENARIOS))
        self.assertEqual(len({snapshot for item in report['scenarios'] for snapshot in item['snapshots']}), 3)
        self.assertEqual(list(self.root.iterdir()), [])

    def test_all_without_dry_run_is_rejected(self):
        stderr = io.StringIO()
        with redirect_stderr(stderr), self.assertRaises(SystemExit) as error:
            self.run_gate('all')
        self.assertEqual(error.exception.code, 2)
        self.assertIn('--scenario all requires --dry-run', stderr.getvalue())
        self.assertIn('UTM snapshots are mutually exclusive', stderr.getvalue())
        self.assertEqual(list(self.root.iterdir()), [])

    def test_parse_args_poll_limits(self):
        argv = ['--executable', str(self.executable), '--scenario', 'clean-install']
        defaults = gate.parse_args(argv)
        self.assertEqual(defaults.max_polls, gate.DEFAULT_MAX_POLLS)
        self.assertEqual(defaults.max_polls, 120)
        self.assertEqual(defaults.poll_timeout, gate.DEFAULT_POLL_TIMEOUT)
        self.assertEqual(defaults.poll_timeout, 1500.0)

        custom = gate.parse_args([*argv, '--max-polls', '5', '--poll-timeout', '0.05'])
        self.assertEqual(custom.max_polls, 5)
        self.assertEqual(custom.poll_timeout, 0.05)

    def test_parse_args_rejects_invalid_poll_limits_without_changes(self):
        argv = ['--executable', str(self.executable), '--scenario', 'clean-install',
                '--home', str(self.root)]
        for flag, invalid_values in (
            ('--max-polls', ('0', '-1', '1.5', 'abc')),
            ('--poll-timeout', ('0', '-5', 'nan', 'inf', '-inf', 'abc')),
        ):
            for value in invalid_values:
                with self.subTest(flag=flag, value=value):
                    stderr = io.StringIO()
                    with redirect_stderr(stderr), self.assertRaises(SystemExit) as error:
                        gate.parse_args([*argv, f'{flag}={value}'])
                    self.assertEqual(error.exception.code, 2)
                    self.assertIn(f'argument {flag}: expected', stderr.getvalue())
                    self.assertEqual(list(self.root.iterdir()), [])

    def test_clean_install_polls_then_connects_both_hosts(self):
        self.fixture('clean-install')
        receipts = self.assert_gate('clean-install', args=('--agents', 'codex', 'trae_ide'))
        calls = self.calls()
        self.assertTrue(any(args[:3] == ['doctor', '--operation-id', 'first-install'] for args in calls))
        self.assertEqual([r['exit_code'] for r in receipts if r['step'].startswith('poll install operation')], [10])
        for agent in gate.AGENTS:
            connections = [args for args in calls if args[:3] == ['connect', '--agent', agent]]
            self.assertIn('--dry-run', connections[0])
            self.assertIn('--apply', connections[1])
        self.assertEqual([r['exit_code'] for r in receipts if 'real host session' in r['step']], [11, 11])

    def test_clean_install_enforces_max_polls(self):
        for limit, expected_polls, error in (
            ('2', 2, 'installation completion unconfirmed after polling'),
            ('5', 4, None),
        ):
            with self.subTest(limit=limit):
                (self.root / 'calls.jsonl').unlink(missing_ok=True)
                self.fixture('clean-install', polls_before_complete=4, initial_retry_ms=1)
                with patch.object(gate, 'run_cli', wraps=gate.run_cli) as run_cli:
                    self.assert_gate('clean-install', error=error, args=('--max-polls', limit))
                polls = [call for call in run_cli.call_args_list
                         if call.args[1][:2] == ['doctor', '--operation-id']]
                self.assertEqual([call.kwargs['timeout'] for call in polls], [65.0] * expected_polls)

    def test_clean_install_rejects_retry_beyond_poll_deadline_without_sleeping(self):
        self.fixture('clean-install', initial_retry_ms=500)
        with patch.object(gate, 'time', wraps=time) as clock:
            self.assert_gate('clean-install', error='install polling deadline exceeded',
                             args=('--poll-timeout', '0.05'))
        clock.sleep.assert_not_called()
        self.assertFalse(any(args[:2] == ['doctor', '--operation-id'] for args in self.calls()))

    def test_clean_install_repairs_dirty_hooks(self):
        self.fixture('clean-install', dirty_hooks=True)
        self.assert_gate('clean-install')
        repair_calls = [args for args in self.calls() if args[:2] == ['repair', 'trae-hooks']]
        self.assertEqual(len(repair_calls), 2)
        self.assertIn('--dry-run', repair_calls[0])
        self.assertIn('--apply', repair_calls[1])
        gate.assert_trae_hooks_4_event_schema(self.hooks, {'Stop': [THIRD_PARTY]})

    def test_clean_install_reconnects_after_orphan_cleanup_with_all_third_party_events_present(self):
        groups = {event: [THIRD_PARTY] for event in sorted(gate.EVENTS)}
        final = {event: [OWNED, *items] for event, items in groups.items()}
        self.fixture('clean-install', dirty_hooks=True, orphan_cleanup=True, third_party_only=groups, final_hooks=final)
        self.write_hooks({**groups, 'SessionEnd': [OWNED]})
        self.assert_gate('clean-install')
        calls = self.calls()
        repaired = next(i for i, args in enumerate(calls)
                        if args[:2] == ['repair', 'trae-hooks'] and '--apply' in args)
        preview, apply = calls[repaired + 1:repaired + 3]
        self.assertEqual(preview[:3], ['connect', '--agent', 'trae_ide'])
        self.assertIn('--dry-run', preview)
        self.assertEqual(apply[:3], ['connect', '--agent', 'trae_ide'])
        self.assertIn('--apply', apply)
        gate.assert_trae_hooks_4_event_schema(self.hooks, groups)

    def test_install_poll_rejects_terminal_worker_errors_immediately(self):
        for state in ('failed', 'cancelled', 'needs_review'):
            for setting in ('initial_state', 'poll_state'):
                with self.subTest(state=state, setting=setting):
                    (self.root / 'calls.jsonl').unlink(missing_ok=True)
                    self.fixture('clean-install', **{setting: state})
                    self.assert_gate('clean-install', error=f'install worker state is {state}')
                    polls = [args for args in self.calls() if args[:2] == ['doctor', '--operation-id']]
                    self.assertEqual(len(polls), int(setting == 'poll_state'))
                    self.assertFalse(any(args[0] == 'verify' for args in self.calls()))

    def test_install_poll_rejects_inactive_running_worker_with_exit_zero(self):
        self.fixture('clean-install', poll_exit=0, poll_state='running')
        self.assert_gate('clean-install', error='installation completion unconfirmed after polling')
        self.assertEqual(sum(args[:2] == ['doctor', '--operation-id'] for args in self.calls()), 1)
        self.assertFalse(any(args[0] == 'verify' for args in self.calls()))

    def test_install_poll_accepts_inactive_completed_worker_with_exit_twenty(self):
        self.fixture('clean-install', poll_exit=20, poll_state='completed')
        receipts = self.assert_gate('clean-install')
        self.assertEqual([r['exit_code'] for r in receipts if r['step'].startswith('poll install operation')], [20])
        self.assertEqual(sum(args[:2] == ['doctor', '--operation-id'] for args in self.calls()), 1)

    def test_clean_install_stages_unprepared_hosts_and_rechecks_activation(self):
        self.fixture('clean-install', unprepared=True)
        self.assert_gate('clean-install')
        for agent in gate.AGENTS:
            calls = [args for args in self.calls() if args[:3] == ['connect', '--agent', agent]]
            self.assertEqual(len(calls), 4)
            self.assertIn('--dry-run', calls[0])
            self.assertIn('prepare', calls[1])
            self.assertIn('--apply', calls[1])
            self.assertIn('activate', calls[2])
            self.assertIn('--dry-run', calls[2])
            self.assertIn('--reviewed-sha', calls[3])

    def test_clean_install_reports_i06_without_installing(self):
        for dns, code in [(False, 10), (True, 30), (False, 30)]:
            with self.subTest(dns=dns, code=code):
                self.fixture('clean-install', dns_ok=dns, doctor_exit=code)
                self.assert_gate('clean-install', error='I-06 DNS preflight failed')
        self.assertTrue(all(args[0] == 'doctor' for args in self.calls()))

    def test_clean_install_rejects_premature_verification(self):
        self.fixture('clean-install', false_positive=True)
        self.assert_gate('clean-install', error='must return 11')

    def wait_worker_cleanup(self):
        (self.root / 'finish-worker').touch()
        deadline = time.monotonic() + 6
        while time.monotonic() < deadline:
            if (self.root / 'worker-done').exists():
                return
            time.sleep(.02)
        self.fail('mock detached worker failed to exit')

    def test_half_install_worker_survives_parent_kill(self):
        self.fixture('half-installed')
        self.addCleanup(self.wait_worker_cleanup)
        receipts = self.assert_gate('half-installed')
        self.assertIn(-9, [r.get('exit_code') for r in receipts])
        self.assertEqual([r['exit_code'] for r in receipts if r['step'].startswith('poll install operation')][-1], 10)
        self.assertTrue((self.root / 'worker-done').exists())

    def test_half_install_accepts_completed_worker_on_first_inspection(self):
        self.fixture('half-installed', completed_on_inspection=True)
        self.addCleanup(self.wait_worker_cleanup)
        receipts = self.assert_gate('half-installed')
        self.assertEqual([r['exit_code'] for r in receipts if r['step'] == 'inspect surviving install worker'], [10])
        self.assertFalse(any(r['step'].startswith('poll install operation') for r in receipts))
        self.assertTrue((self.root / 'worker-done').exists())

    def test_half_install_rejects_orphaned_uv_receipt(self):
        self.fixture('half-installed', orphan=True)
        self.addCleanup(self.wait_worker_cleanup)
        receipts = self.assert_gate('half-installed', error='orphan_install_process')
        self.assertEqual(receipts[-1]['exit_code'], 40)

    def test_beta2_upgrade_retains_ambiguous_op_and_checks_uninstall(self):
        self.fixture('beta2-upgrade', dirty_hooks=True)
        receipts = self.assert_gate('beta2-upgrade')
        self.assertEqual([(r['step'], r['exit_code']) for r in receipts if r['step'].startswith('apply pending')],
                         [('apply pending operation 1', 0), ('apply pending operation 2', 40)])
        calls = self.calls()
        upgrade = next(i for i, args in enumerate(calls)
                       if args[:2] == ['repair', 'stale-daemon'] and '--apply' in args)
        self.assertEqual(calls[upgrade + 1][0], 'doctor')
        pending = json.loads((self.root / 'pending.json').read_text())
        self.assertEqual([row['operation_id'] for row in pending], ['ui-ambiguous'])
        self.assertEqual((self.root / 'uninstall-mode').read_text(), 'dry-run')
        gate.assert_trae_hooks_4_event_schema(self.hooks, {'Stop': [THIRD_PARTY], 'PreToolUse': [THIRD_PARTY]})

    def test_beta2_upgrade_can_explicitly_apply_uninstall(self):
        self.fixture('beta2-upgrade', dirty_hooks=True)
        self.assert_gate('beta2-upgrade', args=('--run-uninstall-apply',))
        self.assertEqual((self.root / 'uninstall-mode').read_text(), 'apply')

    def test_beta2_upgrade_reconciles_unrecorded_revision_conflict(self):
        self.fixture('beta2-upgrade', dirty_hooks=True)
        path = self.root / 'generation.json.ui-operations' / 'ui-beta2-conflict.json'
        path.parent.mkdir()
        path.write_text(json.dumps({
            'operation_id': path.stem, 'kind': 'connection_activate',
            'profile_id': 'trae-cn-id', 'profile_revision': 7,
        }))
        receipts = self.assert_gate('beta2-upgrade')
        steps = {receipt['step']: receipt.get('exit_code') for receipt in receipts}
        self.assertEqual(steps['review pending operation 3'], 10)
        self.assertEqual(steps['apply pending operation 3'], 0)
        self.assertFalse(path.exists())

    def test_beta2_upgrade_rejects_a_dry_run_that_mutates_hooks(self):
        self.fixture('beta2-upgrade', dirty_hooks=True, mutate_hooks=True)
        self.assert_gate('beta2-upgrade', error='dry-run changed hooks.json')
        self.assertFalse(any(args[0] == 'stop' for args in self.calls()))

    def test_beta2_upgrade_rejects_a_dry_run_that_mutates_owner(self):
        self.fixture('beta2-upgrade', dirty_hooks=True, mutate_owner=True)
        self.assert_gate('beta2-upgrade', error='dry-run changed ownership')
        self.assertFalse(any('--apply' in args for args in self.calls()))

    def test_beta2_upgrade_detects_lost_ambiguous_op(self):
        self.fixture('beta2-upgrade', dirty_hooks=True, lose_ambiguous=True)
        self.assert_gate('beta2-upgrade', error='journal retention differs')


if __name__ == '__main__':
    unittest.main()
