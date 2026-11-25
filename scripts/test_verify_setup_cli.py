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
from types import SimpleNamespace
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
        state = {'operation_id': 'first-install', 'state': 'running'}
        if settings.get('locked_runtime_start'):
            state['stage'] = 'starting_runtime'
        if settings.get('runtime_start'):
            fcntl.flock(handle, fcntl.LOCK_UN)
            pid = os.getpid()
            try:
                started = subprocess.check_output(['/bin/ps', '-p', str(pid), '-o', 'lstart='],
                                                  encoding='utf-8', timeout=5).strip()
            except (OSError, subprocess.SubprocessError):
                started = ''
            state.update(stage='starting_runtime', pid=pid)
            if started:
                state['started'] = started
        temp = worker_state.with_suffix('.tmp')
        temp.write_text(json.dumps(state))
        temp.replace(worker_state)
        deadline = time.monotonic() + 5
        while not worker_finish.exists() and time.monotonic() < deadline:
            time.sleep(.01)
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
          daemon={'state': 'ready'},
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
                if settings.get('runtime_start'):
                    state = json.loads(worker_state.read_text())
                    assert state['stage'] == 'starting_runtime'
                    os.kill(state['pid'], 0)
                    with worker_lock.open('r') as handle:
                        fcntl.flock(handle, fcntl.LOCK_SH | fcntl.LOCK_NB)
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
        preflight_patch = patch.object(
            gate.utm_guard,
            'run_preflight',
            return_value=(0, {'status': 'passed', 'ok': True, 'returncode': 0, 'elapsed_ms': 1, 'checks': []}),
        )
        self._preflight_mock = preflight_patch.start()
        self.addCleanup(preflight_patch.stop)

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

    def poll_runner(self):
        return gate.Gate(SimpleNamespace(
            executable=self.executable, home=self.root, config=self.root / 'runtime.json',
            component_root=self.root / 'components', hooks_path=self.hooks, mirror='official',
        ))

    def test_exit_twenty_guidance_matches_go(self):
        source = (Path(gate.__file__).resolve().parent.parent / 'internal/setupcli/types.go').read_text(encoding='utf-8')
        declaration = next(line for line in source.splitlines() if line.startswith('const agentGuidance = '))
        self.assertEqual(gate.GUIDANCE, json.loads(declaration.split(' = ', 1)[1]))
        self.assertIn('On exit 20 follow next_actions after retry_after_ms', gate.GUIDANCE)

    def test_install_poll_retries_worker_lock_contention(self):
        digest = 'a' * 64
        for error in gate.RETRYABLE_WORKER_ERRORS:
            for use_apply in (True, False):
                with self.subTest(error=error, use_apply=use_apply):
                    runner = self.poll_runner()
                    failed = {'worker_active': False, 'progress': {'state': 'failed', 'error_code': error}}
                    expected = ['doctor', '--operation-id', 'first-install']
                    actions = []
                    if use_apply:
                        actions = [{'args': ['install', '--mirror', 'official', '--apply', '--plan-digest', digest,
                                            '--config', str(runner.options.config), '--component-root',
                                            str(runner.options.component_root), '--json']}]
                        expected = ['install', '--apply', '--plan-digest', digest, '--wait', '45']
                    initial = gate.CLIResult(20, {
                        'components': failed, 'operation_id': 'first-install', 'retry_after_ms': 10000,
                        'next_actions': actions,
                    })
                    final = gate.CLIResult(0, {
                        'components': {'worker_active': False, 'progress': {'state': 'completed'}} if use_apply else failed,
                        'operation_id': 'first-install', 'daemon': {'state': 'ready'},
                    })
                    with patch.object(gate, 'run_cli', return_value=final) as run_cli, patch.object(gate.time, 'sleep') as sleep:
                        self.assertIs(runner.poll(initial), final)
                    run_cli.assert_called_once_with(self.executable, runner.command(*expected),
                                                    env=runner.env, timeout=65.0)
                    sleep.assert_called_once_with(10)

    def test_install_poll_recovers_from_contention_without_worker_receipt(self):
        runner = self.poll_runner()
        runner.options.max_polls = 2
        digest = 'a' * 64
        components = {'worker_active': False, 'progress': None}
        doctor = ['doctor', '--operation-id', 'first-install']
        initial = gate.CLIResult(20, {
            'components': components, 'operation_id': 'first-install', 'retry_after_ms': 1000,
            'blockers': [{'code': 'install_worker_active'}], 'next_actions': [{'args': doctor}],
        })
        stopped = gate.CLIResult(10, {
            'components': components, 'daemon': {'state': 'stopped'},
            'blockers': [{'code': 'daemon_stopped'}],
            'next_actions': [{'args': ['install', '--plan']},
                             {'args': ['install', '--apply', '--plan-digest', digest]}],
        })
        active = gate.CLIResult(20, {
            'components': {'worker_active': True, 'progress': {'state': 'running'}},
            'operation_id': 'second-install', 'retry_after_ms': 0,
        })
        ready = gate.CLIResult(0, {
            'components': {'worker_active': False, 'progress': {'state': 'completed'}},
            'operation_id': 'second-install', 'daemon': {'state': 'ready'},
        })
        with patch.object(gate, 'run_cli', side_effect=[stopped, active, ready]) as run_cli, patch.object(gate.time, 'sleep') as sleep:
            self.assertIs(runner.poll(initial), ready)
        self.assertEqual([call.args[1] for call in run_cli.call_args_list], [
            runner.command(*doctor), runner.command('install', '--apply', '--plan-digest', digest, '--wait', '45'),
            runner.command('doctor', '--operation-id', 'second-install'),
        ])
        self.assertEqual([call.args for call in sleep.call_args_list], [(1,), (0,)])

    def test_install_poll_refreshes_plan_before_retrying_apply(self):
        digest = 'b' * 64
        busy = gate.CLIResult(20, {
            'components': {'worker_active': False, 'progress': {'state': 'failed', 'error_code': 'owner_busy'}},
            'retry_after_ms': 1000,
            'next_actions': [{'args': ['install', '--plan', '--config', '/old/config', '--component', 'runtime',
                                       '--component-root', '/old/components', '--mirror', 'old', '--json']}],
        })
        components = {'worker_active': False, 'progress': None}
        ready = gate.CLIResult(0, {'components': components, 'daemon': {'state': 'ready'}})
        for resolves in (True, False):
            with self.subTest(resolves=resolves):
                runner = self.poll_runner()
                runner.options.max_polls = 1
                apply_scope = ['--component', 'runtime', '--operation-id', 'new-install']
                apply = ['install', '--apply', '--plan-digest', digest, *apply_scope, '--wait', '5', '--json']
                refreshed = gate.CLIResult(10, {
                    'components': busy.payload['components'], 'daemon': {'state': 'stopped'},
                    'artifacts': {'plan_digest': digest},
                    'next_actions': [{'args': apply}],
                })
                retries = 1 if resolves else 3
                replies = [refreshed, ready] if resolves else [refreshed, busy] * retries
                with patch.object(gate, 'run_cli', side_effect=replies) as run_cli, patch.object(gate.time, 'sleep') as sleep:
                    if resolves:
                        self.assertIs(runner.poll(busy), ready)
                    else:
                        with self.assertRaisesRegex(gate.VerificationError, 'install lock contention retry limit exceeded'):
                            runner.poll(busy)
                self.assertEqual([call.args[1] for call in run_cli.call_args_list], [
                    runner.command('install', '--plan', '--component', 'runtime'),
                    runner.command('install', '--apply', '--plan-digest', digest, *apply_scope, '--wait', '45'),
                ] * retries)
                self.assertEqual([call.args for call in sleep.call_args_list], [(1,)] * retries)

    def test_install_poll_rejects_failed_worker_on_plan_or_doctor(self):
        for command, error_code, step in (
            (['install', '--plan'], 'runtime_start_failed', 'refresh installation plan 1'),
            (['doctor', '--operation-id', 'first-install'], 'owner_busy', 'poll install operation 1'),
        ):
            with self.subTest(command=command, error_code=error_code):
                runner = self.poll_runner()
                busy = gate.CLIResult(20, {
                    'components': {'worker_active': False}, 'operation_id': 'first-install', 'retry_after_ms': 0,
                    'next_actions': [{'args': command}],
                })
                digest = 'b' * 64
                failed = gate.CLIResult(10, {
                    'components': {'worker_active': False, 'progress': {'state': 'failed', 'error_code': error_code}},
                    'operation_id': 'first-install', 'daemon': {'state': 'stopped'},
                    'artifacts': {'plan_digest': digest},
                    'next_actions': [{'args': ['install', '--apply', '--plan-digest', digest]}],
                })
                error = 'install worker state is failed'
                with patch.object(gate, 'run_cli', return_value=failed) as run_cli, patch.object(gate.time, 'sleep'):
                    with self.assertRaisesRegex(gate.VerificationError, f'^{error}$'):
                        runner.poll(busy)
                run_cli.assert_called_once_with(self.executable, runner.command(*command), env=runner.env, timeout=65.0)
                self.assertEqual(runner.receipts[-1], {
                    'step': step, 'status': 'failed', 'exit_code': 10, 'error': error,
                })

    def test_install_poll_routes_busy_or_active_refreshed_plan_to_doctor(self):
        idle = {'worker_active': False, 'progress': None}
        ready = gate.CLIResult(0, {'components': idle, 'daemon': {'state': 'ready'}})
        busy = gate.CLIResult(20, {
            'components': idle, 'retry_after_ms': 0, 'operation_id': 'first-install',
            'next_actions': [{'args': ['install', '--plan']}],
        })
        for code, worker_active in ((20, False), (10, True)):
            for operation_id in ('second-install', 'bad/id', '', None):
                with self.subTest(code=code, worker_active=worker_active, operation_id=operation_id):
                    runner = self.poll_runner()
                    refreshed = gate.CLIResult(code, {
                        **busy.payload, 'components': {'worker_active': worker_active},
                        'operation_id': operation_id,
                    })
                    expected = [runner.command('install', '--plan')]
                    with patch.object(gate, 'run_cli', side_effect=[refreshed, ready]) as run_cli, patch.object(gate.time, 'sleep'):
                        if operation_id == 'second-install':
                            self.assertIs(runner.poll(busy), ready)
                            expected.append(runner.command('doctor', '--operation-id', operation_id))
                        else:
                            error = 'install poll requires a valid operation ID'
                            with self.assertRaisesRegex(gate.VerificationError, f'^{error}$'):
                                runner.poll(busy)
                            self.assertEqual(runner.receipts[-1], {
                                'step': 'refresh installation plan 1', 'status': 'failed', 'exit_code': code, 'error': error,
                            })
                    self.assertEqual([call.args[1] for call in run_cli.call_args_list], expected)

        runner = self.poll_runner()
        with patch.object(gate, 'run_cli', return_value=ready) as run_cli, patch.object(gate.time, 'sleep'):
            self.assertIs(runner.poll(busy), ready)
        run_cli.assert_called_once_with(self.executable, runner.command('install', '--plan'),
                                        env=runner.env, timeout=65.0)

    def test_install_poll_uses_tracked_id_when_busy_refreshed_plan_omits_it(self):
        idle = {'worker_active': False, 'progress': None}
        refreshed = gate.CLIResult(20, {
            'components': idle, 'retry_after_ms': 0,
            'next_actions': [{'args': ['install', '--plan']}],
        })
        ready = gate.CLIResult(0, {'components': idle, 'daemon': {'state': 'ready'}})
        for tracked_id in ('first-install', None):
            with self.subTest(tracked_id=tracked_id):
                runner = self.poll_runner()
                initial = (gate.CLIResult(20, {**refreshed.payload, 'operation_id': tracked_id})
                           if tracked_id else refreshed)
                expected = [runner.command('install', '--plan')]
                with patch.object(gate, 'run_cli', side_effect=[refreshed, ready]) as run_cli, patch.object(gate.time, 'sleep'):
                    if tracked_id:
                        self.assertIs(runner.poll(initial), ready)
                        expected.append(runner.command('doctor', '--operation-id', tracked_id))
                    else:
                        error = 'install poll requires a valid operation ID'
                        with self.assertRaisesRegex(gate.VerificationError, f'^{error}$'):
                            runner.poll(initial)
                        self.assertEqual(runner.receipts[-1]['step'], 'review install retry 2')
                self.assertEqual([call.args[1] for call in run_cli.call_args_list], expected)

    def test_install_poll_resumes_recovery_after_forced_doctor(self):
        runner = self.poll_runner()
        idle = {'worker_active': False, 'progress': None}
        busy = gate.CLIResult(20, {
            'components': idle, 'operation_id': 'first-install', 'retry_after_ms': 0,
            'next_actions': [{'args': ['install', '--plan']}],
        })
        refreshed = gate.CLIResult(20, {**busy.payload, 'operation_id': 'second-install'})
        apply = ['install', '--apply', '--plan-digest', 'b' * 64]
        stopped = gate.CLIResult(10, {
            'components': idle, 'operation_id': 'second-install', 'daemon': {'state': 'stopped'},
            'blockers': [{'code': 'daemon_stopped'}], 'next_actions': [{'args': apply}],
        })
        ready = gate.CLIResult(0, {'components': idle, 'daemon': {'state': 'ready'}})
        with patch.object(gate, 'run_cli', side_effect=[refreshed, stopped, ready]) as run_cli, patch.object(gate.time, 'sleep'):
            self.assertIs(runner.poll(busy), ready)
        self.assertEqual([call.args[1] for call in run_cli.call_args_list], [
            runner.command('install', '--plan'), runner.command('doctor', '--operation-id', 'second-install'),
            runner.command(*apply, '--wait', '45'),
        ])

    def test_install_poll_applies_refreshed_plan_when_daemon_is_already_ready(self):
        runner = self.poll_runner()
        idle = {'worker_active': False, 'progress': None}
        busy = gate.CLIResult(20, {
            'components': idle, 'retry_after_ms': 0,
            'next_actions': [{'args': ['install', '--plan']}],
        })
        digest = 'b' * 64
        apply = ['install', '--apply', '--plan-digest', digest]
        ready = gate.CLIResult(0, {'components': idle, 'daemon': {'state': 'ready'}})
        refreshed = gate.CLIResult(10, {
            **ready.payload, 'artifacts': {'plan_digest': digest}, 'next_actions': [{'args': apply}],
        })
        with patch.object(gate, 'run_cli', side_effect=[refreshed, ready]) as run_cli, patch.object(gate.time, 'sleep'):
            self.assertIs(runner.poll(busy), ready)
        self.assertEqual([call.args[1] for call in run_cli.call_args_list], [
            runner.command('install', '--plan'), runner.command(*apply, '--wait', '45'),
        ])

    def test_install_poll_rejects_invalid_refreshed_apply_action(self):
        digest = 'b' * 64
        apply = ['install', '--apply', '--plan-digest', digest, '--component', 'runtime']
        scoped = ['--component', 'runtime']
        scope_error = 'refreshed install apply action changed component scope'
        digest_error = 'refreshed install apply action plan_digest mismatch'
        for plan_scope, plan_digest, actions, error in (
            (scoped, digest, [], 'refreshed installation plan did not provide an install apply action'),
            (scoped, None, [{'args': apply}], 'missing or invalid plan_digest receipt'),
            (scoped, 'invalid', [{'args': apply}], 'missing or invalid plan_digest receipt'),
            (scoped, 'a' * 64, [{'args': apply}], digest_error),
            (scoped, digest, [{'args': apply + ['--plan-digest', digest]}], digest_error),
            (scoped, digest, [{'args': apply[:-2]}], scope_error),
            ([], digest, [{'args': apply}], scope_error),
            (scoped, digest, [{'args': [*apply[:-2], '--wait', *apply[-2:]]}], scope_error),
        ):
            with self.subTest(plan_scope=plan_scope, plan_digest=plan_digest, actions=actions):
                runner = self.poll_runner()
                plan = ['install', '--plan', *plan_scope]
                busy = gate.CLIResult(20, {
                    'components': {'worker_active': False}, 'retry_after_ms': 0,
                    'next_actions': [{'args': plan}],
                })
                refreshed = gate.CLIResult(10, {
                    'components': {'worker_active': False}, 'daemon': {'state': 'ready'},
                    'artifacts': {} if plan_digest is None else {'plan_digest': plan_digest},
                    'next_actions': actions,
                })
                with patch.object(gate, 'run_cli', return_value=refreshed) as run_cli, patch.object(gate.time, 'sleep'):
                    with self.assertRaisesRegex(gate.VerificationError, f'^{error}$'):
                        runner.poll(busy)
                run_cli.assert_called_once_with(self.executable, runner.command(*plan),
                                                env=runner.env, timeout=65.0)
                self.assertEqual(runner.receipts[-1], {
                    'step': 'review refreshed installation plan 1', 'status': 'failed', 'error': error,
                })

    def test_install_poll_rejects_operation_id_change_from_doctor(self):
        components = {'worker_active': True, 'progress': {'state': 'running'}}
        initial = gate.CLIResult(20, {
            'components': components, 'operation_id': 'first-install', 'retry_after_ms': 0,
        })
        for code, status in (
            (20, {'components': components, 'retry_after_ms': 0}),
            (0, {'components': {'worker_active': False}, 'daemon': {'state': 'ready'}}),
        ):
            with self.subTest(code=code):
                runner = self.poll_runner()
                changed = gate.CLIResult(code, {**status, 'operation_id': 'second-install'})
                with patch.object(gate, 'run_cli', return_value=changed) as run_cli, patch.object(gate.time, 'sleep'):
                    with self.assertRaisesRegex(gate.VerificationError, '^install poll changed operation ID$'):
                        runner.poll(initial)
                run_cli.assert_called_once_with(self.executable, runner.command('doctor', '--operation-id', 'first-install'),
                                                env=runner.env, timeout=65.0)
                self.assertEqual(runner.receipts[-1], {
                    'step': 'poll install operation 1', 'status': 'failed', 'exit_code': code,
                    'error': 'install poll changed operation ID',
                })

    def test_install_poll_rejects_operation_id_change_from_initial_doctor(self):
        for code in (0, 10, 20):
            with self.subTest(code=code):
                runner = self.poll_runner()
                initial = gate.CLIResult(code, {
                    'components': {'worker_active': code == 20}, 'daemon': {'state': 'ready'},
                    'operation_id': 'second-install', 'retry_after_ms': 0,
                })
                with patch.object(gate, 'run_cli') as run_cli:
                    with self.assertRaisesRegex(gate.VerificationError, '^install poll changed operation ID$'):
                        runner.poll(initial, 'first-install')
                run_cli.assert_not_called()
                self.assertEqual(runner.receipts, [{
                    'step': 'inspect initial install worker state', 'status': 'failed',
                    'error': 'install poll changed operation ID',
                }])

    def test_install_poll_rejects_empty_doctor_id_with_worker(self):
        operation_id = 'first-install'
        active = gate.CLIResult(20, {
            'components': {'worker_active': True}, 'operation_id': operation_id, 'retry_after_ms': 0,
        })
        for components in (
            {'worker_active': False, 'progress': {'state': 'completed'}},
            {'worker_active': False, 'progress': {}},
            {'worker_active': True, 'progress': None},
        ):
            for id_fields in ({}, {'operation_id': None}, {'operation_id': ''}):
                doctor = gate.CLIResult(0, {'components': components, 'daemon': {'state': 'ready'}, **id_fields})
                for initial in (True, False):
                    with self.subTest(components=components, id_fields=id_fields, initial=initial):
                        runner = self.poll_runner()
                        with patch.object(gate, 'run_cli', return_value=doctor) as run_cli, patch.object(gate.time, 'sleep'):
                            with self.assertRaisesRegex(gate.VerificationError, '^install poll changed operation ID$'):
                                runner.poll(doctor if initial else active, operation_id)
                        if initial:
                            run_cli.assert_not_called()
                            self.assertEqual(runner.receipts[-1]['step'], 'inspect initial install worker state')
                        else:
                            run_cli.assert_called_once_with(self.executable, runner.command('doctor', '--operation-id', operation_id),
                                                            env=runner.env, timeout=65.0)
                            self.assertEqual(runner.receipts[-1]['step'], 'poll install operation 1')

    def test_install_poll_validates_recovery_apply_operation_id(self):
        digest = 'a' * 64
        busy = gate.CLIResult(20, {
            'components': {'worker_active': False}, 'operation_id': 'first-install', 'retry_after_ms': 0,
            'next_actions': [{'args': ['install', '--apply', '--plan-digest', digest]}],
        })
        ready = gate.CLIResult(0, {
            'components': {'worker_active': False}, 'daemon': {'state': 'ready'},
        })
        for operation_id in ('bad/id', '', None):
            with self.subTest(operation_id=operation_id):
                runner = self.poll_runner()
                expected = [runner.command('install', '--apply', '--plan-digest', digest, '--wait', '45')]
                with patch.object(gate, 'run_cli') as run_cli, patch.object(gate.time, 'sleep'):
                    if operation_id is None:
                        active = gate.CLIResult(20, {
                            'components': {'worker_active': True}, 'operation_id': None, 'retry_after_ms': 0,
                        })
                        run_cli.side_effect = [active, ready]
                        self.assertIs(runner.poll(busy), ready)
                        expected.append(runner.command('doctor', '--operation-id', 'first-install'))
                    else:
                        run_cli.return_value = gate.CLIResult(0, {**ready.payload, 'operation_id': operation_id})
                        error = 'install poll requires a valid operation ID'
                        with self.assertRaisesRegex(gate.VerificationError, f'^{error}$'):
                            runner.poll(busy)
                        self.assertEqual(runner.receipts[-1], {
                            'step': 'poll install operation 1', 'status': 'failed', 'exit_code': 0, 'error': error,
                        })
                self.assertEqual([call.args[1] for call in run_cli.call_args_list], expected)

    def test_install_poll_does_not_spawn_cli_if_deadline_expires_during_sleep(self):
        for action, step in (
            (['doctor', '--operation-id', 'first-install'], 'poll install operation 1'),
            (['install', '--plan'], 'refresh installation plan 1'),
            (['install', '--apply', '--plan-digest', 'a' * 64], 'poll install operation 1'),
        ):
            with self.subTest(action=action):
                runner = self.poll_runner()
                runner.options.poll_timeout = 2
                busy = gate.CLIResult(20, {
                    'components': {'worker_active': False}, 'operation_id': 'first-install', 'retry_after_ms': 1000,
                    'next_actions': [{'args': action}],
                })
                with patch.object(gate, 'time') as clock, patch.object(gate, 'run_cli') as run_cli:
                    clock.monotonic.return_value = 100
                    clock.sleep.side_effect = lambda _: setattr(clock.monotonic, 'return_value', 102)
                    with self.assertRaisesRegex(gate.VerificationError, '^install polling deadline exceeded$'):
                        runner.poll(busy)
                clock.sleep.assert_called_once_with(1)
                run_cli.assert_not_called()
                self.assertEqual(runner.receipts[-1], {
                    'step': step, 'status': 'failed', 'error': 'install polling deadline exceeded',
                })

    def test_install_poll_does_not_spawn_apply_if_deadline_expires_during_plan(self):
        runner = self.poll_runner()
        runner.options.poll_timeout = 2
        digest = 'a' * 64
        busy = gate.CLIResult(20, {
            'components': {'worker_active': False}, 'retry_after_ms': 0,
            'next_actions': [{'args': ['install', '--plan']}],
        })
        refreshed = gate.CLIResult(10, {
            'components': {'worker_active': False}, 'daemon': {'state': 'stopped'},
            'artifacts': {'plan_digest': digest},
            'next_actions': [{'args': ['install', '--apply', '--plan-digest', digest]}],
        })
        with patch.object(gate, 'time') as clock, patch.object(gate, 'run_cli') as run_cli:
            clock.monotonic.return_value = 100

            def run_plan(*args, **kwargs):
                clock.monotonic.return_value = 103
                return refreshed

            run_cli.side_effect = run_plan
            with self.assertRaisesRegex(gate.VerificationError, '^install polling deadline exceeded$'):
                runner.poll(busy)
        run_cli.assert_called_once_with(self.executable, runner.command('install', '--plan'),
                                        env=runner.env, timeout=2)
        self.assertEqual(runner.receipts[-1], {
            'step': 'poll install operation 1', 'status': 'failed', 'error': 'install polling deadline exceeded',
        })

    def test_install_poll_rejects_other_failures_even_after_contention(self):
        retryable = {'state': 'failed', 'error_code': 'upgrade_locked'}
        for progress in ({'state': 'failed'}, {'state': 'failed', 'error_code': 'runtime_start_failed'}):
            with self.subTest(progress=progress):
                runner = self.poll_runner()
                initial = gate.CLIResult(20, {
                    'components': {'worker_active': False, 'progress': retryable},
                    'operation_id': 'first-install', 'retry_after_ms': 0,
                })
                final = gate.CLIResult(0, {
                    'components': {'worker_active': False, 'progress': progress},
                    'operation_id': 'first-install', 'daemon': {'state': 'ready'},
                })
                with patch.object(gate, 'run_cli', return_value=final) as run_cli:
                    with self.assertRaisesRegex(gate.VerificationError, 'install worker state is failed'):
                        runner.poll(initial)
                run_cli.assert_called_once()

    def test_install_poll_accepts_historical_contention_without_seeing_it_live(self):
        for exit_code in (0, 10):
            with self.subTest(exit_code=exit_code):
                runner = self.poll_runner()
                final = gate.CLIResult(exit_code, {
                    'components': {'worker_active': False, 'progress': {'state': 'failed', 'error_code': 'owner_busy'}},
                    'operation_id': 'first-install', 'daemon': {'state': 'ready'},
                })
                with patch.object(gate, 'run_cli') as run_cli:
                    self.assertIs(runner.poll(final, 'first-install'), final)
                run_cli.assert_not_called()

    def test_install_poll_accepts_daemon_only_startup(self):
        components = {'worker_active': False, 'progress': None}
        operation_id = 'first-install'
        initial = gate.CLIResult(20, {
            'components': components, 'operation_id': operation_id, 'retry_after_ms': 0,
            'blockers': [{'code': 'daemon_starting'}],
        })
        for id_fields in ({}, {'operation_id': None}, {'operation_id': ''}):
            with self.subTest(id_fields=id_fields):
                runner = self.poll_runner()
                final = gate.CLIResult(10, {
                    'components': components, 'daemon': {'state': 'ready'}, **id_fields,
                    'blockers': [{'code': 'connection_not_activated'}],
                })
                with patch.object(gate, 'run_cli', return_value=final) as run_cli:
                    self.assertIs(runner.poll(final, operation_id), final)
                    run_cli.assert_not_called()
                    self.assertIs(runner.poll(initial), final)
                run_cli.assert_called_once_with(self.executable, runner.command('doctor', '--operation-id', operation_id),
                                                env=runner.env, timeout=65.0)

    def test_install_poll_rejects_inactive_running_worker_with_exit_zero(self):
        self.fixture('clean-install', poll_exit=0, poll_state='running')
        self.assert_gate('clean-install', error='installation completion unconfirmed after polling')
        self.assertEqual(sum(args[:2] == ['doctor', '--operation-id'] for args in self.calls()), 1)
        self.assertFalse(any(args[0] == 'verify' for args in self.calls()))

    def test_install_poll_waits_for_daemon_after_stale_completed_worker(self):
        runner = self.poll_runner()
        components = {'worker_active': False, 'progress': {'state': 'completed'}}
        stale = gate.CLIResult(20, {
            'components': components, 'operation_id': 'first-install', 'retry_after_ms': 0,
            'daemon': {'state': 'ready'}, 'blockers': [{'code': 'daemon_starting'}],
        })
        final = gate.CLIResult(0, {**stale.payload, 'blockers': []})
        with patch.object(gate, 'run_cli', side_effect=[stale, final]) as run_cli:
            self.assertIs(runner.poll(stale), final)
        self.assertEqual([call.args[1][:3] for call in run_cli.call_args_list],
                         [['doctor', '--operation-id', 'first-install']] * 2)
        with self.assertRaisesRegex(gate.VerificationError, 'installation completion unconfirmed after polling'):
            runner.poll(gate.CLIResult(0, {**final.payload, 'daemon': {'state': 'starting'}}))

    def test_install_poll_caps_apply_retries_independently_of_doctor_polls(self):
        busy = gate.CLIResult(20, {
            'components': {'worker_active': False, 'progress': {'state': 'failed', 'error_code': 'owner_busy'}},
            'operation_id': 'first-install', 'retry_after_ms': 10000,
            'next_actions': [{'args': ['install', '--apply', '--plan-digest', 'a' * 64]}],
        })
        active = gate.CLIResult(20, {
            'components': {'worker_active': True, 'progress': {'state': 'running'}}, 'retry_after_ms': 1000,
        })
        final = gate.CLIResult(10, {
            'components': {'worker_active': False, 'progress': {'state': 'completed'}},
            'operation_id': 'first-install', 'daemon': {'state': 'ready'},
        })
        for resolves in (False, True):
            with self.subTest(resolves=resolves):
                runner = self.poll_runner()
                runner.options.max_polls = 1 if resolves else 120
                replies = [busy, busy, active, final] if resolves else [busy] * 3
                with patch.object(gate, 'run_cli', side_effect=replies) as run_cli, patch.object(gate.time, 'sleep') as sleep:
                    if resolves:
                        self.assertIs(runner.poll(busy), final)
                    else:
                        with self.assertRaisesRegex(gate.VerificationError, 'install lock contention retry limit exceeded'):
                            runner.poll(busy)
                commands = [call.args[1][:2] for call in run_cli.call_args_list]
                self.assertEqual(commands, [['install', '--apply']] * 3 + ([['doctor', '--operation-id']] if resolves else []))
                self.assertEqual([call.args[0] for call in sleep.call_args_list], [10] * 3 + ([1] if resolves else []))

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
        self.fixture('half-installed', locked_runtime_start=True)
        self.addCleanup(self.wait_worker_cleanup)
        with patch.object(gate, 'worker_process_alive') as worker_process_alive:
            receipts = self.assert_gate('half-installed')
        worker_process_alive.assert_not_called()
        self.assertIn(-9, [r.get('exit_code') for r in receipts])
        self.assertEqual([r['exit_code'] for r in receipts if r['step'].startswith('poll install operation')][-1], 10)
        self.assertTrue((self.root / 'worker-done').exists())

    def test_half_install_worker_survives_during_runtime_start_without_install_lock(self):
        self.fixture('half-installed', runtime_start=True)
        self.addCleanup(self.wait_worker_cleanup)
        run = subprocess.run

        def run_with_sandbox_fallback(command, *args, **kwargs):
            try:
                return run(command, *args, **kwargs)
            except PermissionError:
                if command[:3] != ['/bin/ps', '-o', 'lstart=,stat=']:
                    raise
                # Some macOS sandboxes block ps. Identity parsing is covered by the ps unit tests.
                os.kill(int(command[-1]), 0)
                return subprocess.CompletedProcess(command, 0, 'Wed Nov 25 14:00:00 2026 S\n')

        with patch.object(gate.subprocess, 'run', side_effect=run_with_sandbox_fallback):
            receipts = self.assert_gate('half-installed')
        self.assertEqual([r['exit_code'] for r in receipts if r['step'] == 'inspect surviving install worker'], [20])
        self.assertTrue((self.root / 'worker-done').exists())

    def test_worker_process_alive_checks_pid_and_start_identity(self):
        for pid in (None, False, True, 1, -1, '42'):
            with self.subTest(pid=pid), patch.object(gate.subprocess, 'run') as run:
                self.assertFalse(gate.worker_process_alive({'pid': pid}))
                run.assert_not_called()
        started = 'Wed Nov 25 14:00:00 2026'
        other = 'Wed Nov 25 14:00:01 2026'
        for expected, outputs, alive in (
            (started, [(0, f'  {started} S+\n')], True),
            (' ' + started + ' ', [(0, f'{other} S'), (0, f'{started} S')], True),
            (started, [(0, f'{other} S'), (0, f'{other} S')], False),
            (started, [(0, f'{started} Z')], False),
            (started, [(0, f'{started} Z+')], False),
            (started, [(0, f'{other} S'), (0, f'{started} Z+')], False),
            (started, [(0, f'{other} S'), (1, '')], False),
            (started, [(1, '')], False),
            (None, [(0, '')], False),
            (None, [(0, f'{started} S\n{other} S\n')], False),
            (started, [(0, 'invalid')], False),
            (42, [(0, f'{started} S')], False),
        ):
            with self.subTest(expected=expected, outputs=outputs), patch.object(gate.subprocess, 'run') as run:
                run.side_effect = [subprocess.CompletedProcess([], code, stdout) for code, stdout in outputs]
                self.assertIs(gate.worker_process_alive({'pid': 42, 'started': expected}), alive)
                self.assertEqual(run.call_count, len(outputs))
                command = ['/bin/ps', '-o', 'lstart=,stat=', '-p', '42']
                kwargs = dict(capture_output=True, encoding='utf-8', timeout=5, check=False)
                self.assertEqual(run.call_args_list[0].args, (command,))
                self.assertEqual(run.call_args_list[0].kwargs, kwargs)
                if len(outputs) == 2:
                    self.assertEqual(run.call_args_list[1].args, (command,))
                    self.assertEqual(run.call_args_list[1].kwargs, {**kwargs, 'env': {**os.environ, 'LC_ALL': 'C'}})

    def test_worker_process_alive_rejects_invalid_explicit_start_identity(self):
        ps = subprocess.CompletedProcess([], 0, 'Wed Nov 25 14:00:00 2026 S\n')
        with patch.object(gate.subprocess, 'run', return_value=ps):
            self.assertTrue(gate.worker_process_alive({'pid': 42}))
            for started in ('', '   ', 0, False, None):
                with self.subTest(started=started):
                    self.assertFalse(gate.worker_process_alive({'pid': 42, 'started': started}))

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

    def test_preflight_failure_returns_31_without_invoking_cli(self):
        self.fixture('clean-install')
        self._preflight_mock.return_value = (
            gate.EXIT_PREFLIGHT_BLOCKED,
            {
                'status': 'preflight_env_blocked',
                'ok': False,
                'returncode': gate.EXIT_PREFLIGHT_BLOCKED,
                'elapsed_ms': 120,
                'checks': [{'check': 'dns', 'ok': False, 'code': 'PREFLIGHT_DNS_FAILED', 'failed_hosts': ['pypi.org']}],
                'failed_codes': ['PREFLIGHT_DNS_FAILED'],
            },
        )
        code, report = self.run_gate('clean-install')
        self.assertEqual(code, gate.EXIT_PREFLIGHT_BLOCKED)
        self.assertEqual(report['status'], 'preflight_env_blocked')
        self.assertEqual(report['scenarios'], [])
        self.assertFalse((self.root / 'calls.jsonl').exists())

    def test_preflight_only_and_skip_preflight_modes(self):
        output = io.StringIO()
        with redirect_stdout(output):
            code = gate.main(['--preflight-only', '--home', str(self.root), '--mirror', 'china', '--json'])
        report = json.loads(output.getvalue())
        self.assertEqual(code, 0)
        self.assertEqual(report['status'], 'passed')
        self.assertEqual(report['scenarios'], [])

        self.fixture('clean-install')
        self._preflight_mock.reset_mock()
        code2, report2 = self.run_gate('clean-install', '--skip-preflight')
        self.assertEqual(code2, 0, report2)
        self.assertEqual(report2['preflight']['status'], 'skipped')
        self._preflight_mock.assert_not_called()

    def test_sentinel_returns_32_when_protected_login_state_is_deleted(self):
        self.fixture('clean-install')
        jwt_path = self.root / '.trae-cn' / 'trae-jwt-token'
        jwt_path.parent.mkdir(parents=True, exist_ok=True)
        jwt_path.write_text('jwt-present', encoding='utf-8')
        # Inject a buggy CLI that deletes the protected Trae CN JWT during execution.
        self.write_executable(
            'from pathlib import Path\n'
            'import os\n'
            'p = Path(os.environ["HOME"]) / ".trae-cn" / "trae-jwt-token"\n'
            'if p.exists(): p.unlink()\n'
            + FAKE_CLI
        )
        code, report = self.run_gate('clean-install')
        self.assertEqual(code, gate.EXIT_PROTECTED_PATH_VIOLATED)
        self.assertEqual(report['status'], 'protected_path_violated')
        self.assertFalse(report['sentinel']['ok'])
        self.assertEqual(report['sentinel']['violations'][0]['label'], 'trae_cn_jwt')


if __name__ == '__main__':
    unittest.main()
