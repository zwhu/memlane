#!/usr/bin/env python3
"""Run the beta.3 Level 1 release gate from a prepared macOS UTM snapshot."""
from __future__ import annotations

import argparse
from dataclasses import dataclass
import fcntl
import hashlib
import json
import math
import os
from pathlib import Path
import re
import shlex
import shutil
import signal
import stat
import subprocess
import sys
import time

if __package__:
    from . import utm_guard, utm_isolated_env
else:
    import utm_guard
    import utm_isolated_env

EXIT_PREFLIGHT_BLOCKED = utm_guard.EXIT_PREFLIGHT_BLOCKED
EXIT_PROTECTED_PATH_VIOLATED = utm_guard.EXIT_PROTECTED_PATH_VIOLATED
EXIT_ISOLATED_VERSION_MISMATCH = utm_guard.EXIT_ISOLATED_VERSION_MISMATCH

SCENARIOS = {
    'clean-install': ('clean-codex-traecn', 'dirty-hooks-preexisting'),
    'half-installed': ('clean-codex-traecn',),
    'beta2-upgrade': ('stuck-beta2-upgrade',),
}
AGENTS = ('codex', 'trae_ide')
EVENTS = {'SessionStart', 'UserPromptSubmit', 'PostToolUse', 'Stop'}
FORBIDDEN_EVENTS = {'SessionEnd', 'PostToolUseFailure'}
SECRET_MARKERS = utm_guard.SECRET_MARKERS
GUIDANCE = utm_guard.GUIDANCE
DEFAULT_MAX_POLLS = 120
DEFAULT_POLL_TIMEOUT = 1500.0
MAX_POLLS = DEFAULT_MAX_POLLS
MAX_APPLY_RETRIES = 3
POLL_TIMEOUT = DEFAULT_POLL_TIMEOUT
WORKER_START_TIMEOUT = 10
RETRYABLE_WORKER_ERRORS = ('upgrade_locked', 'owner_busy', 'install_worker_active')


class VerificationError(RuntimeError):
    pass


@dataclass(frozen=True)
class CLIResult:
    returncode: int
    payload: dict


def require(condition, message):
    if not condition:
        raise VerificationError(message)


def json_object(raw):
    return utm_guard.json_object(raw, error_cls=VerificationError)


def check_output_privacy(raw, payload, extra_markers=()):
    utm_guard.check_output_privacy(raw, payload, tuple(extra_markers), guidance=GUIDANCE, error_cls=VerificationError)


def clean_env():
    allowed = ('HOME', 'TMPDIR', 'PATH', 'LANG', 'LC_ALL')
    return {key: os.environ[key] for key in allowed if key in os.environ}


def run_cli(executable, args, *, env=None, timeout=65) -> CLIResult:
    child_env = clean_env() if env is None else dict(env)
    child_env.pop('MEMLANE_DEV_RUNTIME_CONFIG', None)
    try:
        result = subprocess.run([str(executable), *args], env=child_env, capture_output=True,
                                encoding='utf-8', timeout=timeout, check=False)
    except subprocess.TimeoutExpired:
        raise VerificationError('CLI timed out') from None
    except (OSError, UnicodeError):
        raise VerificationError('could not execute CLI or decode its output') from None
    if '--json' not in args and '--json=true' not in args:
        return CLIResult(result.returncode, {})
    require(result.stderr == '', 'CLI stderr was non-empty')
    payload = json_object(result.stdout)
    require(type(payload.get('schema_version')) is int and payload['schema_version'] == 1,
            'CLI schema_version must be 1')
    require(type(payload.get('exit_code')) is int and payload['exit_code'] == result.returncode,
            'CLI exit_code differs from process return code')
    check_output_privacy(result.stdout, payload)
    return CLIResult(result.returncode, payload)


def read_json(path):
    try:
        return json_object(path.read_text(encoding='utf-8'))
    except (OSError, UnicodeError):
        raise VerificationError('could not read verification JSON file') from None


def file_sha(path):
    try:
        with path.open('rb') as handle:
            digest = hashlib.sha256()
            for chunk in iter(lambda: handle.read(65536), b''):
                digest.update(chunk)
            return digest.hexdigest()
    except FileNotFoundError:
        return None


def hooks_document(path):
    payload = read_json(path)
    require(type(payload.get('version')) is int and payload['version'] == 1, 'hooks.json version must be 1')
    hooks = payload.get('hooks')
    require(isinstance(hooks, dict), 'hooks.json must contain a hooks object')
    return hooks


def memlane_hook_group(group):
    for hook in group.get('hooks', []):
        if not isinstance(hook, dict) or not isinstance(hook.get('command'), str):
            continue
        try:
            args = shlex.split(hook['command'])
        except ValueError:
            continue
        if len(args) >= 4 and args[1] == '-B' and re.search(
            r'(?:^|/)managed-marketplaces/trae_ide-[a-f0-9]{24}/[a-f0-9]{20}/'
            r'plugins/cognee/scripts/trae_ide_hook\.py$', args[2],
        ):
            return True
    return False


def check_hook_groups(groups):
    require(isinstance(groups, list) and all(
        isinstance(group, dict) and isinstance(group.get('hooks'), list) and group['hooks']
        for group in groups
    ), 'invalid hooks.json event groups')


def third_party_groups(path):
    if not path.exists():
        return {}
    result = {}
    for event, groups in hooks_document(path).items():
        check_hook_groups(groups)
        result[event] = [group for group in groups if not memlane_hook_group(group)]
    return result


def assert_trae_hooks_4_event_schema(hooks_path, expected_third_party_groups=None):
    hooks = hooks_document(hooks_path)
    require(EVENTS <= hooks.keys(), 'hooks.json is missing a required Memlane event')
    require(not FORBIDDEN_EVENTS.intersection(hooks), 'hooks.json contains an unsupported beta.2 event')
    for event, groups in hooks.items():
        require(re.fullmatch(r'[A-Za-z][A-Za-z0-9_]*', event) is not None, 'invalid hooks.json event name')
        require(bool(groups), 'hooks.json contains an empty event key')
        check_hook_groups(groups)
        if event in EVENTS:
            require(any(map(memlane_hook_group, groups)), 'required event has no Memlane hook group')
        else:
            require(not any(map(memlane_hook_group, groups)), 'Memlane hook under an unsupported event')
    for event, expected_groups in (expected_third_party_groups or {}).items():
        remaining = list(hooks.get(event, []))
        for group in expected_groups:
            require(group in remaining, 'a preexisting third-party hook group changed or disappeared')
            remaining.remove(group)


def flock_held(path):
    try:
        fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
    except FileNotFoundError:
        return False
    with os.fdopen(fd, 'rb') as handle:
        require(stat.S_ISREG(os.fstat(handle.fileno()).st_mode), 'lock path is not a regular file')
        try:
            fcntl.flock(handle, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            return True
        fcntl.flock(handle, fcntl.LOCK_UN)
    return False


def artifact(result, key):
    value = result.payload.get('artifacts', {}).get(key)
    patterns = {
        'plan_digest': r'[a-fA-F0-9]{64}',
        'reviewed_sha': r'[a-fA-F0-9]{64}',
        'owner_fingerprint': r'[a-fA-F0-9]{64}',
    }
    require(isinstance(value, str) and re.fullmatch(patterns[key], value), f'missing or invalid {key} receipt')
    return value


def hosts(result):
    return result.payload.get('connections', {}).get('hosts', [])


def pending(result):
    return {row['operation_id']: row for row in result.payload.get('connections', {}).get('pending_ui_operations', [])}


def valid_operation_id(value):
    return isinstance(value, str) and re.fullmatch(r'[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}', value) is not None


def worker_process_alive(state):
    pid = state.get('pid')
    if type(pid) is not int or pid <= 1:
        return False

    def process_start(**kwargs):
        result = subprocess.run(['/bin/ps', '-o', 'lstart=,stat=', '-p', str(pid)],
                                capture_output=True, encoding='utf-8', timeout=5, check=False, **kwargs)
        if result.returncode != 0:
            return None
        lines = result.stdout.strip().splitlines()
        if len(lines) != 1:
            return None
        parts = lines[0].rsplit(None, 1)
        if len(parts) != 2 or 'Z' in parts[1].strip():
            return None
        return parts[0].strip()

    started = process_start()
    if not started:
        return False
    if 'started' in state:
        expected = state['started']
        if not isinstance(expected, str) or not expected.strip():
            return False
        if started != expected.strip():
            return process_start(env={**os.environ, 'LC_ALL': 'C'}) == expected.strip()
    return True


class Gate:
    def __init__(self, options):
        self.options = options
        self.env = clean_env()
        self.env['HOME'] = str(options.home)
        if options.home != Path.home():
            self.env['TMPDIR'] = str(options.home)
        self.runtime = options.config.parent
        self.hooks_path = options.hooks_path
        self.receipts = []
        self.app_meta = {}

    def check(self, label, action):
        receipt = {'step': label, 'status': 'failed'}
        self.receipts.append(receipt)
        try:
            result = action(receipt)
        except VerificationError as exc:
            receipt['error'] = str(exc)
            raise
        except (OSError, UnicodeError, KeyError, ValueError, TypeError, AttributeError, subprocess.TimeoutExpired):
            receipt['error'] = 'invalid verification files, operating system state, or JSON receipt'
            raise VerificationError(receipt['error']) from None
        receipt['status'] = 'passed'
        return result

    def command(self, *args):
        scope = []
        for flag in ('config', 'component_root'):
            value = getattr(self.options, flag)
            scope.extend(['--' + flag.replace('_', '-'), str(value)])
        if self.options.mirror and args[0] in ('doctor', 'install'):
            scope.extend(['--mirror', self.options.mirror])
        return [*args, *scope, '--json']

    def cli(self, label, *args, codes=(0,), validate=None, timeout=65):
        def execute(receipt):
            command = self.command(*args)
            limit = timeout() if callable(timeout) else timeout
            result = run_cli(self.options.executable, command, env=self.env, timeout=limit)
            receipt['exit_code'] = result.returncode
            if isinstance(result.payload.get('app'), dict):
                self.app_meta.update(result.payload['app'])
            if validate:
                validate(result)
            require(result.returncode in codes, f'expected CLI exit code in {codes}')
            return result
        return self.check(label, execute)

    def plan(self, codes=(10, 20)):
        result = self.cli('review installation plan', 'install', '--plan', codes=codes,
                          validate=lambda result: artifact(result, 'plan_digest'))
        return artifact(result, 'plan_digest')

    def poll(self, result, operation_id=None):
        def daemon_ready(res):
            return res.returncode != 20 and res.payload.get('daemon', {}).get('state') == 'ready'

        def has_contention(res):
            blockers = {item.get('code') for item in res.payload.get('blockers', [])}
            progress = res.payload.get('components', {}).get('progress') or {}
            return (res.returncode == 20 or bool(blockers.intersection(RETRYABLE_WORKER_ERRORS))
                    or progress.get('error_code') in RETRYABLE_WORKER_ERRORS)

        def find_install_action(actions, flag):
            return next((action['args'] for action in actions
                         if action.get('args', [])[:1] == ['install'] and flag in action['args']), None)

        def built_flag_values(args, flag):
            return [value for token, value in zip(args[2::2], args[3::2]) if token == flag]

        def install_args(action, mode):
            args = ['install', mode]
            flags = iter(action[1:])
            for flag in flags:
                if flag in ('--plan-digest', '--component', '--operation-id'):
                    args.extend((flag, next(flags, '')))
                elif flag in ('--config', '--component-root', '--mirror', '--wait'):
                    next(flags, None)
            if mode == '--apply':
                args.extend(('--wait', '45'))
            return args

        def worker_status(res, *, allow_plan_contention=False):
            components = res.payload.get('components', {})
            worker_active = components.get('worker_active')
            progress = components.get('progress') or {}
            state = progress.get('state')
            lock_error = state == 'failed' and progress.get('error_code') in RETRYABLE_WORKER_ERRORS
            contention = lock_error and res.returncode == 20
            plan_contention = allow_plan_contention and lock_error and worker_active is False and res.returncode == 10
            historical_contention = lock_error and worker_active is False and daemon_ready(res)
            require(contention or plan_contention or historical_contention
                    or state not in ('failed', 'cancelled', 'needs_review'),
                    f'install worker state is {state}')
            return worker_active, state, worker_active is False and contention

        def completed():
            progress = result.payload.get('components', {}).get('progress') or {}
            return worker_active is False and daemon_ready(result) and (
                state in (None, 'completed')
                or (state == 'failed' and progress.get('error_code') in RETRYABLE_WORKER_ERRORS)
            )

        def validate_poll(res):
            worker_status(res)
            check_no_orphans(res)

        def check_doctor_operation_id(res, expected):
            if expected is None:
                return
            components = res.payload.get('components') or {}
            has_worker = bool(components.get('worker_active')) or components.get('progress') is not None
            reported = res.payload.get('operation_id')
            # Go emits an empty ID only on the empty-receipt daemon-only path.
            if not has_worker and reported in (None, ''):
                return
            require(reported == expected, 'install poll changed operation ID')

        def inspect_initial_worker_state(_):
            if operation_id is not None:
                check_doctor_operation_id(result, operation_id)
            return worker_status(result)

        def validate_plan(res):
            worker_status(res, allow_plan_contention=True)
            check_no_orphans(res)
            if res.returncode == 20 or res.payload.get('components', {}).get('worker_active') is True:
                if 'operation_id' in res.payload:
                    require(valid_operation_id(res.payload['operation_id']),
                            'install poll requires a valid operation ID')

        def cli_timeout():
            remaining = deadline - time.monotonic()
            require(remaining > 0, 'install polling deadline exceeded')
            return min(65.0, remaining)

        max_polls = getattr(self.options, 'max_polls', MAX_POLLS)
        deadline = time.monotonic() + getattr(self.options, 'poll_timeout', POLL_TIMEOUT)
        worker_active, state, retryable_contention = self.check(
            'inspect initial install worker state', inspect_initial_worker_state)
        saw_contention = has_contention(result)
        worker_poll = (operation_id is not None or result.returncode == 20
                       or worker_active is True or state not in (None, 'completed'))
        poll_rounds = 0
        apply_retries = 0
        force_doctor_poll = False
        while worker_poll and not completed():
            forced_doctor = force_doctor_poll
            force_doctor_poll = False
            apply = None
            plan = None
            can_recover = (not forced_doctor and worker_active is False and saw_contention and result.returncode in (10, 20)
                           and (state in (None, 'completed') or retryable_contention))
            if can_recover:
                actions = result.payload.get('next_actions', [])
                apply = find_install_action(actions, '--apply')
                if apply is None:
                    plan = find_install_action(actions, '--plan')
            recovering = apply is not None or plan is not None
            if recovering:
                self.check(f'check install retry limit {apply_retries + 1}', lambda _: require(
                    apply_retries < MAX_APPLY_RETRIES, 'install lock contention retry limit exceeded'))
                apply_retries += 1
            else:
                if poll_rounds >= max_polls:
                    break
                poll_rounds += 1
            round_number = poll_rounds + apply_retries

            def poll_args(_):
                require((recovering and result.returncode in (10, 20))
                        or worker_active is not False or result.returncode == 20,
                        'installation completion unconfirmed after polling')
                reported = result.payload.get('operation_id')
                op = operation_id or reported
                if not recovering or op is not None:
                    require(valid_operation_id(op), 'install poll requires a valid operation ID')
                require(not reported or reported == op, 'install poll changed operation ID')
                retry = result.payload.get('retry_after_ms', 0 if result.returncode == 10 else None)
                require(type(retry) is int and retry >= 0, 'invalid install retry_after_ms')
                remaining = deadline - time.monotonic()
                delay = retry / 1000
                require(delay < remaining, 'install polling deadline exceeded')
                return op, delay
            operation_id, delay = self.check(f'review install retry {round_number}', poll_args)
            if not recovering or result.returncode == 20 or delay > 0:
                time.sleep(delay)

            apply_args = install_args(apply, '--apply') if apply is not None else None
            if plan is not None:
                plan_args = install_args(plan, '--plan')
                result = self.cli(f'refresh installation plan {round_number}', *plan_args,
                                  codes=(0, 10, 20), validate=validate_plan, timeout=cli_timeout)
                worker_active, state, retryable_contention = worker_status(result, allow_plan_contention=True)
                saw_contention = saw_contention or has_contention(result)
                if result.returncode == 0 and completed():
                    continue
                if worker_active is True or result.returncode == 20:
                    if 'operation_id' in result.payload:
                        operation_id = result.payload['operation_id']
                    force_doctor_poll = True
                    continue
                if result.returncode != 10:
                    break

                def review_refreshed_plan(_):
                    digest = artifact(result, 'plan_digest')
                    action = find_install_action(result.payload.get('next_actions', []), '--apply')
                    require(action is not None, 'refreshed installation plan did not provide an install apply action')
                    args = install_args(action, '--apply')
                    require(built_flag_values(args, '--plan-digest') == [digest],
                            'refreshed install apply action plan_digest mismatch')
                    require(built_flag_values(args, '--component') == built_flag_values(plan_args, '--component'),
                            'refreshed install apply action changed component scope')
                    return args

                apply_args = self.check(f'review refreshed installation plan {round_number}', review_refreshed_plan)

            args = ['doctor', '--operation-id', operation_id]
            codes = (0, 10, 20)
            if apply_args is not None:
                args = apply_args
                codes = (0, 20)

            def validate_operation(res):
                validate_poll(res)
                if apply_args is None:
                    check_doctor_operation_id(res, operation_id)
                elif 'operation_id' in res.payload and res.payload['operation_id'] is not None:
                    require(valid_operation_id(res.payload['operation_id']),
                            'install poll requires a valid operation ID')

            result = self.cli(f'poll install operation {round_number}', *args, codes=codes, validate=validate_operation,
                              timeout=cli_timeout)
            if apply_args is not None and 'operation_id' in result.payload and result.payload['operation_id'] is not None:
                operation_id = result.payload['operation_id']
            worker_active, state, retryable_contention = worker_status(result)
            saw_contention = saw_contention or has_contention(result)
        self.check('confirm installation completed', lambda _: require(completed(),
                                                                      'installation completion unconfirmed after polling'))
        return result

    def setup_verified(self, *flags):
        self.cli('verify setup', 'verify', '--stage', 'setup', *flags)

    def apply_connection(self, kind, preview, read_only):
        args = ('connect', '--agent', kind)
        if not preview.payload.get('artifacts', {}).get('reviewed_sha'):
            # Unprepared hosts expose a read-only preview before staging. Their
            # activation SHA is available only after the explicit prepare phase.
            self.check(f'confirm {kind} prepare action', lambda _: require(any(
                action.get('requires') == 'explicit_confirmation'
                and action.get('args', [])[:1] == ['connect']
                and '--phase' in action['args'] and 'prepare' in action['args']
                and '--apply' in action['args']
                for action in preview.payload.get('next_actions', [])
            ), 'connection preview did not provide a SHA or a prepare action'))
            self.cli(f'prepare {kind} connection', *args, '--phase', 'prepare', '--apply')
            preview = self.cli(f'review prepared {kind} activation', *args, '--phase', 'activate',
                               '--dry-run', codes=(10,), validate=read_only)
        sha = self.check(f'confirm {kind} reviewed SHA', lambda _: artifact(preview, 'reviewed_sha'))
        self.cli(f'apply {kind} connection', *args, '--apply', '--reviewed-sha', sha, codes=(0, 11))

    def repair_hooks(self, host, baseline):
        def unchanged(result):
            require(file_sha(self.hooks_path) == baseline, 'TRAE hooks repair dry-run changed hooks.json')
            artifact(result, 'reviewed_sha')
        def target_flags(_):
            target = host.get('instance_id')
            require(isinstance(target, str) and re.fullmatch(r'[a-z][a-z0-9_-]{0,63}', target),
                    'missing TRAE connection instance ID')
            return ('--instance-id', target)
        target = self.check('identify TRAE repair instance', target_flags)
        preview = self.cli('review TRAE hooks repair', 'repair', 'trae-hooks', *target, '--dry-run',
                           codes=(10,), validate=unchanged)
        self.cli('apply TRAE hooks repair', 'repair', 'trae-hooks', '--apply', *target,
                 '--reviewed-sha', artifact(preview, 'reviewed_sha'))

    def clean_install(self):
        def dns(result):
            ok = result.payload.get('environment', {}).get('dns_ok')
            require(ok is not False and result.returncode != 30, 'I-06 DNS preflight failed')
            require(ok is True, 'I-06 DNS preflight did not confirm DNS')
        doctor = self.cli('check network and missing components', 'doctor', '--check-network', codes=(10,), validate=dns)
        digest = self.plan()
        apply = self.cli('apply installation plan', 'install', '--apply', '--plan-digest', digest, '--wait', '45',
                         codes=(0, 20), validate=check_no_orphans)
        self.poll(apply)
        self.cli('verify components', 'verify', '--stage', 'components')
        self.setup_verified()

        detected = {host['kind'] for host in hosts(doctor) if host.get('detected') is True}
        agents = [agent for agent in AGENTS if agent in detected or agent in self.options.agents]
        self.check('identify connection targets', lambda _: require(agents, 'no Codex or TRAE CN connection targets'))
        for kind in agents:
            is_trae = kind == 'trae_ide'
            baseline = self.check('snapshot TRAE hooks', lambda _: file_sha(self.hooks_path)) if is_trae else None
            groups = self.check('record third-party hook groups', lambda _: third_party_groups(self.hooks_path)) if is_trae else None
            def read_only(_):
                if is_trae:
                    require(file_sha(self.hooks_path) == baseline, 'connect dry-run changed hooks.json')
            preview = self.cli(f'review {kind} connection', 'connect', '--agent', kind, '--dry-run',
                               codes=(0, 10), validate=read_only)
            host = next((host for host in hosts(preview) if host.get('kind') == kind), {})
            requires_repair = is_trae and (host.get('needs_hook_repair') is True or any(
                item.get('code') == 'trae_hooks_repair_required' for item in preview.payload.get('blockers', [])
            ))
            if requires_repair:
                self.repair_hooks(host, baseline)
                # Orphan cleanup can leave the host unactivated. Re-review before
                # installing replacement hooks if the repair did not restore them.
                repaired = self.check('inspect repaired TRAE hooks', lambda _: hooks_document(self.hooks_path))
                if all(event in repaired and any(map(memlane_hook_group, repaired[event])) for event in EVENTS):
                    preview = None
                else:
                    baseline = self.check('snapshot repaired TRAE hooks', lambda _: file_sha(self.hooks_path))
                    preview = self.cli('review TRAE connection after repair', 'connect', '--agent', kind,
                                       '--dry-run', codes=(0, 10), validate=read_only)
            if preview is not None:
                self.apply_connection(kind, preview, read_only)
            if is_trae:
                self.check('verify four-event TRAE hooks and third-party groups',
                           lambda _: assert_trae_hooks_4_event_schema(self.hooks_path, groups))
            self.cli(f'verify {kind} needs a real host session', 'verify', '--stage', 'connection',
                     '--agent', kind, codes=(11,), validate=check_human_required)

    def half_installed(self):
        digest = self.plan()
        install_dir = self.options.component_root / 'installation'
        state_path = install_dir / 'worker-state.json'
        lock_path = install_dir / 'installation.lock'

        def interrupt(receipt):
            previous = file_sha(state_path)
            args = self.command('install', '--apply', '--plan-digest', digest, '--wait', '45')
            with subprocess.Popen([str(self.options.executable), *args], env=self.env,
                                  stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL) as parent:
                try:
                    deadline = time.monotonic() + WORKER_START_TIMEOUT
                    while time.monotonic() < deadline and parent.poll() is None:
                        if (file_sha(state_path) not in (None, previous)) or flock_held(lock_path):
                            parent.kill()
                            receipt['exit_code'] = parent.wait(timeout=5)
                            require(receipt['exit_code'] == -signal.SIGKILL, 'install parent exited before SIGKILL')
                            return previous
                        time.sleep(0.05)
                    raise VerificationError('install parent exited or timed out before its worker started')
                finally:
                    if parent.poll() is None:
                        parent.kill()
                        parent.wait(timeout=5)
        previous = self.check('SIGKILL install parent after worker starts', interrupt)

        def surviving_worker(_):
            deadline = time.monotonic() + WORKER_START_TIMEOUT
            while time.monotonic() < deadline:
                if file_sha(state_path) not in (None, previous):
                    state = read_json(state_path)
                    state_name = state.get('state')
                    locked = flock_held(lock_path)
                    alive_runtime_start = (not locked and state_name in ('starting', 'running')
                                           and state.get('stage') == 'starting_runtime'
                                           and worker_process_alive(state))
                    if locked or alive_runtime_start or state_name in ('completed', 'needs_review', 'failed', 'cancelled'):
                        operation_id = state.get('operation_id')
                        require(valid_operation_id(operation_id), 'detached worker did not retain a valid operation ID')
                        return operation_id, locked or alive_runtime_start or state_name == 'completed'
                time.sleep(0.05)
            raise VerificationError('worker did not retain the install lock, start the runtime, or complete after parent SIGKILL')
        operation, survived = self.check('read detached worker state after parent SIGKILL', surviving_worker)
        result = self.cli('inspect surviving install worker', 'doctor', '--operation-id', operation,
                          codes=(0, 10, 20), validate=check_no_orphans)
        self.check('confirm detached worker survived or completed', lambda _: require(
            survived, 'worker terminated without completing after parent SIGKILL'))
        self.poll(result, operation)
        self.setup_verified()

    def beta2_upgrade(self):
        def stale_snapshot(result):
            require(result.payload.get('daemon', {}).get('stale_version_detected') is True,
                    'snapshot did not contain a stale beta.2 daemon')
            require(result.payload.get('components', {}).get('complete') is True,
                    'upgrade snapshot components are incomplete')
        initial = self.cli('inspect stale beta.2 snapshot', 'doctor', codes=(10, 40), validate=stale_snapshot)

        def ownership_snapshot(_):
            config = read_json(self.options.config)
            root = Path(config['official_data'])
            require(root.is_absolute(), 'invalid upgrade store path')
            return root, file_sha(self.options.config), file_sha(root / '.memlane-dev-owner.json'), flock_held(root / '.service-owner')
        owner_before = self.check('snapshot stale daemon ownership', ownership_snapshot)
        def unchanged_owner(result):
            require(ownership_snapshot(None) == owner_before, 'stale-daemon dry-run changed ownership or runtime config')
            require(artifact(result, 'owner_fingerprint') == initial.payload['daemon']['owner_fingerprint'],
                    'stale-daemon preview did not match the original owner')
        preview = self.cli('review stale daemon repair', 'repair', 'stale-daemon', '--dry-run',
                           codes=(10,), validate=unchanged_owner)
        self.cli('apply stale daemon repair', 'repair', 'stale-daemon', '--apply',
                 '--owner-fingerprint', artifact(preview, 'owner_fingerprint'))
        post_upgrade = self.cli('inspect upgraded beta.3 daemon', 'doctor', codes=(0, 10, 11, 40))

        before = pending(initial)
        self.cli('review pending operations', 'repair', 'pending-ops', '--dry-run', codes=(0, 10, 40),
                 validate=lambda result: require(pending(result) == before, 'pending operations changed during daemon repair'))
        expected = before.copy()
        for index, (operation_id, row) in enumerate(before.items(), 1):
            if not row.get('kind', '').startswith('connection_'):
                continue
            def classify(result):
                evidence = result.payload.get('artifacts', {}).get('connection_operation') or {}
                recorded = evidence.get('recorded') is True
                return (
                    ((recorded or evidence.get('status') == 'unrecorded')
                     and evidence.get('reconcilable') is True and evidence.get('phase') != 'post_mutation')
                    or (recorded and (evidence.get('safe_to_unlock') is True or evidence.get('already_complete') is True))
                )
            detail = self.cli(f'review pending operation {index}', 'repair', 'pending-ops', '--operation-id',
                              operation_id, '--dry-run', codes=(10, 40))
            safe = classify(detail)
            self.check(f'confirm pending operation {index} classification', lambda _: require(
                detail.returncode == (10 if safe else 40), 'pending operation classification disagrees with preview'))
            self.cli(f'apply pending operation {index}', 'repair', 'pending-ops', '--apply', '--operation-id',
                     operation_id, codes=(0,) if safe else (40,))
            if safe:
                expected.pop(operation_id)
            self.cli(f'check pending operation {index} retention', 'doctor', codes=(0, 10, 11, 40),
                     validate=lambda result: require(pending(result) == expected, 'pending journal retention differs from reviewed outcome'))

        def find_trae(_):
            candidates = [host for host in hosts(post_upgrade) if host.get('kind') == 'trae_ide' and host.get('needs_hook_repair') is True]
            require(len(candidates) == 1, 'upgrade snapshot requires one TRAE instance needing hook repair')
            return candidates[0]
        host = self.check('identify TRAE hook repair after upgrade', find_trae)
        baseline, groups = self.check('snapshot legacy TRAE hooks',
                                      lambda _: (file_sha(self.hooks_path), third_party_groups(self.hooks_path)))
        self.repair_hooks(host, baseline)
        self.check('verify migrated TRAE hooks and third-party groups',
                   lambda _: assert_trae_hooks_4_event_schema(self.hooks_path, groups))
        self.setup_verified('--expect-zero-downloads', '--expect-runtime-current')

        def stopped(result):
            require(result.payload.get('daemon', {}).get('owner_locked') is False
                    and not flock_held(owner_before[0] / '.service-owner'), 'daemon retained the service owner lock')
        stop = self.cli('review stop with current owner', 'stop', '--dry-run', codes=(10,),
                        validate=lambda result: artifact(result, 'owner_fingerprint'))
        self.cli('stop current daemon', 'stop', '--apply', '--owner-fingerprint',
                 artifact(stop, 'owner_fingerprint'), validate=stopped)
        self.check('verify uninstall sees no active owner', self.uninstall)

    def uninstall(self, receipt):
        args = [sys.executable, str(self.options.uninstall_script), '--json', '--home', str(self.options.home),
                '--runtime-dir', str(self.runtime), '--components-dir', str(self.options.component_root)]
        apply = self.options.run_uninstall_apply
        if apply:
            args.append('--apply')
        try:
            result = subprocess.run(args, env=self.env, capture_output=True, encoding='utf-8', timeout=65, check=False)
        except subprocess.TimeoutExpired:
            raise VerificationError('uninstall timed out') from None
        receipt['exit_code'] = result.returncode
        require(result.stderr == '', 'uninstall stderr was non-empty')
        payload = json_object(result.stdout)
        require(payload.get('locked_owners') == [], 'uninstall found an active .service-owner lock or receipt')
        require(result.returncode == 0 and payload.get('errors') == []
                and payload.get('dry_run') is (not apply)
                and payload.get('status') == ('completed' if apply else 'dry_run'), 'uninstall failed')


def check_no_orphans(result):
    require(result.returncode != 40 and not any(
        item.get('code') == 'orphan_install_process' for item in result.payload.get('blockers', [])
    ), 'installer needs review (orphan_install_process or unverifiable worker)')


def check_human_required(result):
    require(result.returncode == 11, 'connection verification must return 11 before a real host session')
    steps = result.payload.get('human_steps')
    require(result.payload.get('ok') is False and result.payload.get('status') == 'human_required'
            and isinstance(steps, list) and any(isinstance(step, str) and step.strip() for step in steps),
            'connection verification did not provide human steps')


def absolute_path(value):
    path = Path(value)
    if not path.is_absolute():
        raise argparse.ArgumentTypeError('expected an absolute path')
    return path


def positive_int(value):
    try:
        number = int(value)
    except ValueError:
        raise argparse.ArgumentTypeError('expected an integer greater than or equal to 1') from None
    if number < 1:
        raise argparse.ArgumentTypeError('expected an integer greater than or equal to 1')
    return number


def positive_float(value):
    try:
        number = float(value)
    except ValueError:
        raise argparse.ArgumentTypeError('expected a positive finite number') from None
    if not math.isfinite(number) or number <= 0:
        raise argparse.ArgumentTypeError('expected a positive finite number')
    return number


def parse_args(argv):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--executable', type=absolute_path)
    parser.add_argument('--scenario', choices=(*SCENARIOS, 'all'))
    parser.add_argument('--level', choices=('1', '2a-check', '2b-check'), default='1',
                        help='verification level: 1 (CLI gate), 2a-check (empty-store 3-step onboarding), 2b-check (warm daemon + real hosts)')
    for name in ('home', 'config', 'component-root', 'uninstall-script', 'hooks-path',
                 'isolated-root', 'warm-components-from', 'evidence-dir'):
        parser.add_argument('--' + name, type=absolute_path)
    parser.add_argument('--mirror', choices=('china', 'official'))
    parser.add_argument('--max-polls', type=positive_int, default=DEFAULT_MAX_POLLS,
                        help='maximum install polls (default: %(default)s)')
    parser.add_argument('--poll-timeout', type=positive_float, default=DEFAULT_POLL_TIMEOUT,
                        help='install polling deadline in seconds (default: %(default)s)')
    parser.add_argument('--agents', choices=AGENTS, nargs='+', default=[])
    parser.add_argument('--require-host-login', choices=AGENTS, nargs='+', default=[],
                        help='fail-fast if required host login state/JWT is missing or expired')
    parser.add_argument('--probe-host', nargs='+', default=[],
                        help='additional hostnames to resolve during fail-fast DNS preflight')
    parser.add_argument('--preflight-only', action='store_true',
                        help='run only the 3s fail-fast environment preflight probes and exit 0 or 31')
    parser.add_argument('--skip-preflight', action='store_true',
                        help='skip fail-fast preflight probes (recorded as skipped; rejected by release gate)')
    parser.add_argument('--keep-isolated', action='store_true',
                        help='keep isolated daemon and workspace alive with .memlane-utm-isolated.json for chained Level 2b reuse')
    parser.add_argument('--teardown-isolated', action='store_true',
                        help='stop isolated daemon, restore backed-up host configs, and safely remove isolated workspace')
    parser.add_argument('--keep-host-config', action='store_true',
                        help='skip restoring backed-up host config files during --teardown-isolated')
    parser.add_argument('--run-uninstall-apply', action='store_true')
    parser.add_argument('--json', action='store_true')
    parser.add_argument('--dry-run', action='store_true')
    options = parser.parse_args(argv)
    if options.keep_isolated and options.teardown_isolated:
        parser.error('--keep-isolated and --teardown-isolated are mutually exclusive')
    if options.teardown_isolated:
        if options.level != '1':
            parser.error('--teardown-isolated cannot be combined with --level 2a-check/2b-check')
        if options.isolated_root is None and options.config is None:
            parser.error('--teardown-isolated requires --isolated-root or explicit --config')
    elif not options.preflight_only:
        if options.executable is None:
            parser.error('the following arguments are required: --executable')
        if options.level == '1' and options.scenario is None:
            parser.error('the following arguments are required: --scenario')
    if options.scenario == 'all' and not options.dry_run and not options.preflight_only:
        parser.error('--scenario all requires --dry-run. The three UTM snapshots are mutually exclusive. Run one --scenario per snapshot.')
    options.home = options.home or Path.home()
    default_runtime = options.home / 'Library' / 'Application Support' / 'Memlane'
    runtime_base = options.isolated_root or default_runtime
    options.config = options.config or runtime_base / 'runtime.json'
    options.component_root = options.component_root or runtime_base / 'components'
    if (options.keep_isolated or options.teardown_isolated) and options.isolated_root is None:
        options.isolated_root = options.config.parent
    options.hooks_path = options.hooks_path or options.home / '.trae-cn' / 'hooks.json'
    options.uninstall_script = options.uninstall_script or Path(__file__).resolve().with_name('uninstall.py')
    return options


def plan_steps(scenario, uninstall_apply):
    steps = {
        'clean-install': [
            'doctor --check-network → DNS true (I-06), exit 10',
            'install --plan → reviewed plan digest',
            'install --apply --wait 45 → poll doctor on exit 20, then verify components and setup (exit 0)',
            'connect Codex and TRAE CN → dry-run before reviewed apply; repair legacy TRAE hooks when requested',
            'TRAE hooks.json → four supported events, no empty keys, third-party groups retained',
            'verify --stage connection → exit 11 with human steps before a real session',
        ],
        'half-installed': [
            'install --plan → reviewed plan digest',
            'install --apply --wait 45 → SIGKILL parent after worker state or lock appears',
            'doctor --operation-id → detached worker retains lock or completes, no orphan install process',
            'verify --stage setup → exit 0',
        ],
        'beta2-upgrade': [
            'doctor → stale daemon, complete components and pending operations',
            'repair stale-daemon → no change on dry-run; apply with reviewed owner fingerprint',
            'doctor → inspect TRAE hook state on the upgraded daemon',
            'repair pending-ops → safe operations unlocked, ambiguous operations retained',
            'repair trae-hooks → unchanged SHA on dry-run; four events and third-party groups after apply',
            'verify --stage setup --expect-zero-downloads --expect-runtime-current → exit 0',
            'stop → apply with current owner fingerprint',
            f'uninstall.py --json{" --apply" if uninstall_apply else ""} → no active owner lock',
        ],
    }
    return steps[scenario]


def _execute_preflight(options, *, require_login_override=None):
    login_agents = tuple(getattr(options, 'require_host_login', ())) if require_login_override is None else tuple(require_login_override)
    return utm_guard.run_preflight(
        home=options.home,
        component_root=options.component_root,
        mirror=options.mirror or 'china',
        scenario=options.scenario or '',
        check_network=True,
        extra_probe_hosts=tuple(getattr(options, 'probe_host', ())),
        require_login_agents=login_agents,
    )


def _check_evidence_dir_safe(evidence_dir, home):
    if evidence_dir is not None and utm_guard.is_protected_overlap(evidence_dir, home) is not None:
        raise utm_isolated_env.IsolatedEnvError(
            'refused writing evidence inside a protected path', returncode=EXIT_PROTECTED_PATH_VIOLATED,
        )


def _write_evidence_json(evidence_dir, filename, payload, *, home):
    if evidence_dir is None:
        return None
    _check_evidence_dir_safe(evidence_dir, home)
    raw = json.dumps(payload, ensure_ascii=False, indent=2) + '\n'
    check_output_privacy(raw, payload)
    evidence_dir.mkdir(parents=True, exist_ok=True)
    dest = evidence_dir / filename
    tmp = evidence_dir / f'.{filename}.{os.getpid()}.tmp'
    tmp.write_text(raw, encoding='utf-8')
    os.replace(tmp, dest)
    return str(dest)


def _check_private_token_file(path_str):
    if not path_str:
        return False, False
    path = Path(path_str)
    if path.is_symlink():
        return True, False
    try:
        st = os.lstat(path)
    except OSError:
        return False, False
    return True, (stat.S_ISREG(st.st_mode) and st.st_size > 0 and (st.st_mode & 0o077) == 0)


def _sanitized_bridge_summary(isolated_root, config_path):
    params = utm_isolated_env.resolve_host_bridge_params(isolated_root, runtime_config=config_path)
    tok_exists, tok_private = _check_private_token_file(params['token_file'])
    mgmt_exists, mgmt_private = _check_private_token_file(params['management_token_file'])
    return {
        'store_id': params['store_id'],
        'service_url': params['service_url'],
        'control_url': params['control_url'],
        'instances': params['instances'],
        'token_file_present': tok_exists,
        'token_file_private_0600': tok_private,
        'management_token_file_present': mgmt_exists,
        'management_token_file_private_0600': mgmt_private,
    }


def _backup_host_hooks(isolated_root, hooks_path):
    if isolated_root is None or not hooks_path.is_file() or hooks_path.is_symlink():
        return {}
    backup_dir = isolated_root / 'backups'
    backup_dir.mkdir(parents=True, exist_ok=True, mode=0o700)
    backup_path = backup_dir / 'hooks.json'
    shutil.copyfile(hooks_path, backup_path)
    return {str(hooks_path): str(backup_path)}


def _error_status(returncode):
    if returncode == EXIT_ISOLATED_VERSION_MISMATCH:
        return 'isolated_version_mismatch'
    if returncode == EXIT_PROTECTED_PATH_VIOLATED:
        return 'protected_path_violated'
    if returncode == EXIT_PREFLIGHT_BLOCKED:
        return 'preflight_env_blocked'
    return 'failed'


def _run_teardown(options):
    isolated_root = options.isolated_root
    if options.dry_run:
        return 0, {
            'status': 'planned', 'returncode': 0, 'action': 'teardown_isolated', 'scenarios': [],
            'steps': [
                'stop --dry-run / --apply --owner-fingerprint (if active)',
                'verify .service-owner flock released',
                'restore backed-up host configs' if not options.keep_host_config else 'keep host configs (--keep-host-config)',
                'utm_guard.safe_remove isolated root with .memlane-utm-isolated.json verification',
                'HostStateSentinel verify protected login paths intact',
            ],
        }

    def stop_daemon(_state):
        if options.executable is None or not options.config.is_file():
            return
        gate = Gate(options)
        preview = gate.cli('review isolated daemon stop', 'stop', '--dry-run', codes=(0, 10, 40))
        if preview.returncode == 10:
            fp = artifact(preview, 'owner_fingerprint')
            gate.cli('stop isolated daemon', 'stop', '--apply', '--owner-fingerprint', fp)

    try:
        _check_evidence_dir_safe(getattr(options, 'evidence_dir', None), options.home)
        result = utm_isolated_env.teardown_isolated_env(
            isolated_root,
            home=options.home,
            keep_host_config=getattr(options, 'keep_host_config', False),
            stop_fn=stop_daemon,
            flock_check_fn=flock_held,
        )
        report = {'status': result['status'], 'returncode': 0, 'teardown': result, 'sentinel': result['sentinel'],
                  'scenarios': [], 'summary': {'passed': 1, 'failed': 0}}
        if options.evidence_dir is not None:
            _write_evidence_json(options.evidence_dir, 'teardown-report.json', report, home=options.home)
    except (utm_isolated_env.IsolatedEnvError, VerificationError) as exc:
        code = exc.returncode if isinstance(exc, utm_isolated_env.IsolatedEnvError) else 1
        report = {'status': _error_status(code), 'returncode': code, 'error': str(exc), 'scenarios': [],
                  'summary': {'passed': 0, 'failed': 1}}
        return code, report
    return 0, report


def _run_level_2a_check(options):
    env_mode = 'apfs_clone_pool' if options.warm_components_from else 'zero_cache_download'
    if options.dry_run:
        return 0, {
            'status': 'planned', 'returncode': 0, 'level': '2a', 'environment_mode': env_mode, 'scenarios': [],
            'steps': [
                'optional APFS CoW clone from --warm-components-from into empty-store sandbox (forbids initialized hot store)',
                'verify runtime.json, store-identity.json store_id continuity across restart, and 0600 owner token permissions',
                'verify doctor daemon ready and verify --stage setup exit 0',
                'emit sanitized level2a-manifest.json if --evidence-dir is set',
            ],
        }
    if getattr(options, 'skip_preflight', False):
        preflight = {'status': 'skipped', 'ok': True, 'returncode': 0, 'checks': []}
    else:
        preflight_code, preflight = _execute_preflight(options)
        if preflight_code != 0:
            return EXIT_PREFLIGHT_BLOCKED, {
                'status': 'preflight_env_blocked', 'returncode': EXIT_PREFLIGHT_BLOCKED, 'level': '2a',
                'preflight': preflight, 'scenarios': [], 'summary': {'passed': 0, 'failed': 0},
            }

    default_runtime = (options.home / 'Library' / 'Application Support' / 'Memlane').resolve(strict=False)
    actual_runtime = options.config.parent.resolve(strict=False)
    sentinel = utm_guard.HostStateSentinel(options.home, protect_memlane_dir=(actual_runtime != default_runtime))
    isolated_root = options.isolated_root or options.config.parent
    gate = Gate(options)
    clone_info = None
    try:
        _check_evidence_dir_safe(getattr(options, 'evidence_dir', None), options.home)
        candidate = utm_isolated_env.compute_candidate_fingerprint(options.executable)
        existing_manifest = utm_isolated_env.read_isolated_manifest(isolated_root)
        if existing_manifest is not None:
            utm_isolated_env.verify_fingerprint_match(existing_manifest, candidate)
            if 'clean-install' in (existing_manifest.get('executed_scenarios') or []):
                raise VerificationError('Level 2a forbids reusing a Level 1 pre-initialized clean-install hot store')

        # Prep mode: empty store + warm component pool before launching GUI 3-step onboarding.
        if options.warm_components_from is not None and not options.config.is_file():
            library_dir = isolated_root / 'library'
            require(not (library_dir.is_dir() and any(library_dir.iterdir())),
                    'Level 2a prep requires an empty library directory (cannot skip 3-step onboarding)')
            utm_isolated_env.prepare_isolated_env(
                isolated_root, options.executable, scenario='level-2a', home=options.home,
                components_source='apfs_clone_pool', allow_reuse=True,
            )
            clone_info = utm_isolated_env.clone_components_apfs(
                options.warm_components_from, options.component_root, home=options.home, allowed_root=isolated_root,
            )
            gate.cli('confirm empty store before 2a onboarding', 'doctor', codes=(10, 40),
                     validate=lambda res: require(res.payload.get('daemon', {}).get('state') != 'ready',
                                                  'Level 2a prep expected an uninitialized daemon before onboarding'))
            sentinel_report = sentinel.verify()
            if not sentinel_report['ok']:
                return EXIT_PROTECTED_PATH_VIOLATED, {'status': 'protected_path_violated', 'returncode': EXIT_PROTECTED_PATH_VIOLATED,
                                                      'level': '2a', 'preflight': preflight, 'sentinel': sentinel_report,
                                                      'scenarios': [], 'summary': {'passed': 0, 'failed': 1}}
            report = {
                'status': 'ready_for_2a_onboarding', 'returncode': 0, 'level': '2a',
                'environment_mode': 'apfs_clone_pool', 'clone': clone_info, 'candidate': candidate,
                'preflight': preflight, 'sentinel': sentinel_report,
                'scenarios': [{'scenario': 'level-2a-prep', 'status': 'passed', 'steps': gate.receipts}],
                'summary': {'passed': 1, 'failed': 0},
            }
            if options.evidence_dir is not None:
                _write_evidence_json(options.evidence_dir, 'level2a-manifest.json', report, home=options.home)
            return 0, report

        # Post-onboarding verify mode: confirm store_id, 0600 owner token, setup ready, and restart continuity.
        summary = gate.check('inspect Level 2a runtime, store_id, and 0600 credentials',
                             lambda _: _sanitized_bridge_summary(isolated_root, options.config))
        gate.check('confirm Level 2a store_id and 0600 owner token', lambda _: (
            require(bool(summary['store_id']), 'missing store_id in store-identity.json'),
            require(summary['token_file_present'] and summary['token_file_private_0600'],
                    'owner token file missing or not strictly 0600 private'),
        ))
        prev_store_id = (existing_manifest or {}).get('store_id') or ''
        restart_reused = False
        if prev_store_id:
            gate.check('confirm store_id unchanged after restart',
                       lambda _: require(summary['store_id'] == prev_store_id, 'store_id changed across app restart'))
            restart_reused = True
        gate.cli('verify Level 2a daemon ready', 'doctor', codes=(0, 10, 11),
                 validate=lambda res: require(res.payload.get('daemon', {}).get('state') == 'ready',
                                              'Level 2a daemon is not ready'))
        if existing_manifest is not None and gate.app_meta.get('bundled_runtime_hash'):
            utm_isolated_env.verify_fingerprint_match(
                existing_manifest,
                utm_isolated_env.compute_candidate_fingerprint(
                    options.executable, bundled_runtime_hash=str(gate.app_meta.get('bundled_runtime_hash') or ''),
                ),
            )
        gate.setup_verified()
        if existing_manifest is not None:
            executed = list(dict.fromkeys([*(existing_manifest.get('executed_scenarios') or []), 'level-2a']))
            utm_isolated_env.update_isolated_manifest(
                isolated_root, store_id=summary['store_id'],
                bundled_runtime_hash=str(gate.app_meta.get('bundled_runtime_hash') or existing_manifest.get('bundled_runtime_hash') or ''),
                executed_scenarios=executed,
            )
            env_mode = existing_manifest.get('components_source') or env_mode
        status = 'passed'
    except utm_isolated_env.IsolatedEnvError as exc:
        sentinel_report = sentinel.verify()
        return exc.returncode, {
            'status': _error_status(exc.returncode), 'returncode': exc.returncode, 'level': '2a',
            'error': str(exc), 'preflight': preflight, 'sentinel': sentinel_report,
            'scenarios': [{'scenario': 'level-2a-check', 'status': 'failed', 'steps': gate.receipts}],
            'summary': {'passed': 0, 'failed': 1},
        }
    except VerificationError:
        summary = {}
        restart_reused = False
        status = 'failed'

    sentinel_report = sentinel.verify()
    if not sentinel_report['ok']:
        return EXIT_PROTECTED_PATH_VIOLATED, {
            'status': 'protected_path_violated', 'returncode': EXIT_PROTECTED_PATH_VIOLATED, 'level': '2a',
            'preflight': preflight, 'sentinel': sentinel_report,
            'scenarios': [{'scenario': 'level-2a-check', 'status': status, 'steps': gate.receipts}],
            'summary': {'passed': int(status == 'passed'), 'failed': int(status != 'passed')},
        }
    code = int(status != 'passed')
    report = {
        'status': status, 'returncode': code, 'level': '2a', 'environment_mode': env_mode,
        'candidate': utm_isolated_env.compute_candidate_fingerprint(
            options.executable, bundled_runtime_hash=str(gate.app_meta.get('bundled_runtime_hash') or ''),
        ),
        'level2a': {'store_id': summary.get('store_id', ''), 'token_file_private_0600': summary.get('token_file_private_0600', False),
                    'restart_store_id_reused': restart_reused},
        'preflight': preflight, 'sentinel': sentinel_report,
        'scenarios': [{'scenario': 'level-2a-check', 'status': status, 'steps': gate.receipts}],
        'summary': {'passed': int(code == 0), 'failed': int(code != 0)},
    }
    if options.evidence_dir is not None and code == 0:
        _write_evidence_json(options.evidence_dir, 'level2a-manifest.json', report, home=options.home)
    return code, report


def _run_level_2b_check(options):
    target_agents = tuple(options.agents or AGENTS)
    if options.dry_run:
        return 0, {
            'status': 'planned', 'returncode': 0, 'level': '2b', 'agents': list(target_agents), 'scenarios': [],
            'steps': [
                'preflight P1-P4 including host login / JWT check for target agents',
                'verify isolated candidate fingerprint match (allows reusing Level 2a or Level 1 --keep-isolated hot daemon)',
                'verify doctor daemon ready and verify --stage setup exit 0',
                'verify --stage connection --agent <kind> returns exit 0 (real host session verified) + TRAE 4-event hooks',
                'emit sanitized level2b-manifest.json and host-session-<agent>.json if --evidence-dir is set',
            ],
        }
    if getattr(options, 'skip_preflight', False):
        preflight = {'status': 'skipped', 'ok': True, 'returncode': 0, 'checks': []}
    else:
        login_req = tuple(options.require_host_login) if options.require_host_login else target_agents
        preflight_code, preflight = _execute_preflight(options, require_login_override=login_req)
        if preflight_code != 0:
            return EXIT_PREFLIGHT_BLOCKED, {
                'status': 'preflight_env_blocked', 'returncode': EXIT_PREFLIGHT_BLOCKED, 'level': '2b',
                'preflight': preflight, 'scenarios': [], 'summary': {'passed': 0, 'failed': 0},
            }

    default_runtime = (options.home / 'Library' / 'Application Support' / 'Memlane').resolve(strict=False)
    actual_runtime = options.config.parent.resolve(strict=False)
    sentinel = utm_guard.HostStateSentinel(options.home, protect_memlane_dir=(actual_runtime != default_runtime))
    isolated_root = options.isolated_root or options.config.parent
    gate = Gate(options)
    host_sessions = {}
    reused_from = 'external'
    bridge_summary = {}
    try:
        _check_evidence_dir_safe(getattr(options, 'evidence_dir', None), options.home)
        candidate = utm_isolated_env.compute_candidate_fingerprint(options.executable)
        existing_manifest = utm_isolated_env.read_isolated_manifest(isolated_root)
        if existing_manifest is not None:
            utm_isolated_env.verify_fingerprint_match(existing_manifest, candidate)
            prev_scenarios = set(existing_manifest.get('executed_scenarios') or [])
            if 'level-2a' in prev_scenarios:
                reused_from = 'level_2a'
            elif 'clean-install' in prev_scenarios:
                reused_from = 'level_1_hot'
            else:
                reused_from = 'isolated'
        gate.cli('verify Level 2b warm daemon ready', 'doctor', codes=(0, 10, 11),
                 validate=lambda res: require(res.payload.get('daemon', {}).get('state') == 'ready',
                                              'Level 2b warm daemon is not ready'))
        if existing_manifest is not None and gate.app_meta.get('bundled_runtime_hash'):
            utm_isolated_env.verify_fingerprint_match(
                existing_manifest,
                utm_isolated_env.compute_candidate_fingerprint(
                    options.executable, bundled_runtime_hash=str(gate.app_meta.get('bundled_runtime_hash') or ''),
                ),
            )
        gate.setup_verified()
        if options.config.is_file():
            bridge_summary = gate.check('resolve sanitized isolated host bridge parameters',
                                        lambda _: _sanitized_bridge_summary(isolated_root, options.config))
        for kind in target_agents:
            res = gate.cli(f'verify {kind} real host session adopted', 'verify', '--stage', 'connection', '--agent', kind, codes=(0,))
            if kind == 'trae_ide':
                gate.check('verify four-event TRAE hooks schema',
                           lambda _: assert_trae_hooks_4_event_schema(options.hooks_path))
            host_sessions[kind] = {
                'agent': kind, 'status': 'passed', 'exit_code': res.returncode, 'connection_verified': True,
                'instance_id': bridge_summary.get('instances', {}).get(kind, ''),
            }
        if existing_manifest is not None:
            executed = list(dict.fromkeys([*(existing_manifest.get('executed_scenarios') or []), 'level-2b']))
            utm_isolated_env.update_isolated_manifest(isolated_root, executed_scenarios=executed)
        status = 'passed'
    except utm_isolated_env.IsolatedEnvError as exc:
        sentinel_report = sentinel.verify()
        return exc.returncode, {
            'status': _error_status(exc.returncode), 'returncode': exc.returncode, 'level': '2b',
            'error': str(exc), 'preflight': preflight, 'sentinel': sentinel_report,
            'scenarios': [{'scenario': 'level-2b-check', 'status': 'failed', 'steps': gate.receipts}],
            'summary': {'passed': 0, 'failed': 1},
        }
    except VerificationError:
        status = 'failed'

    sentinel_report = sentinel.verify()
    if not sentinel_report['ok']:
        return EXIT_PROTECTED_PATH_VIOLATED, {
            'status': 'protected_path_violated', 'returncode': EXIT_PROTECTED_PATH_VIOLATED, 'level': '2b',
            'preflight': preflight, 'sentinel': sentinel_report,
            'scenarios': [{'scenario': 'level-2b-check', 'status': status, 'steps': gate.receipts}],
            'summary': {'passed': int(status == 'passed'), 'failed': int(status != 'passed')},
        }
    code = int(status != 'passed')
    report = {
        'status': status, 'returncode': code, 'level': '2b', 'reused_from': reused_from,
        'candidate': utm_isolated_env.compute_candidate_fingerprint(
            options.executable, bundled_runtime_hash=str(gate.app_meta.get('bundled_runtime_hash') or ''),
        ),
        'bridge_summary': bridge_summary, 'host_sessions': host_sessions,
        'preflight': preflight, 'sentinel': sentinel_report,
        'scenarios': [{'scenario': 'level-2b-check', 'status': status, 'steps': gate.receipts}],
        'summary': {'passed': int(code == 0), 'failed': int(code != 0)},
    }
    if options.evidence_dir is not None and code == 0:
        _write_evidence_json(options.evidence_dir, 'level2b-manifest.json', report, home=options.home)
        for kind, session_receipt in host_sessions.items():
            _write_evidence_json(options.evidence_dir, f'host-session-{kind}.json', session_receipt, home=options.home)
    return code, report


def run_verification(options):
    if getattr(options, 'preflight_only', False):
        code, preflight = _execute_preflight(options)
        return code, {
            'status': preflight['status'],
            'returncode': code,
            'preflight': preflight,
            'scenarios': [],
            'summary': {'passed': 0, 'failed': 0},
        }
    if getattr(options, 'teardown_isolated', False):
        return _run_teardown(options)
    if getattr(options, 'level', '1') == '2a-check':
        return _run_level_2a_check(options)
    if getattr(options, 'level', '1') == '2b-check':
        return _run_level_2b_check(options)

    scenarios = tuple(SCENARIOS) if options.scenario == 'all' else (options.scenario,)
    env_mode = 'apfs_clone_pool' if getattr(options, 'warm_components_from', None) else 'zero_cache_download'
    if options.dry_run:
        return 0, {'status': 'planned', 'returncode': 0, 'level': '1', 'environment_mode': env_mode, 'scenarios': [
            {'scenario': name, 'snapshots': SCENARIOS[name], 'steps': plan_steps(name, options.run_uninstall_apply)}
            for name in scenarios
        ]}
    if getattr(options, 'skip_preflight', False):
        preflight = {'status': 'skipped', 'ok': True, 'returncode': 0, 'checks': []}
    else:
        preflight_code, preflight = _execute_preflight(options)
        if preflight_code != 0:
            return EXIT_PREFLIGHT_BLOCKED, {
                'status': 'preflight_env_blocked',
                'returncode': EXIT_PREFLIGHT_BLOCKED,
                'preflight': preflight,
                'scenarios': [],
                'summary': {'passed': 0, 'failed': 0},
            }

    isolated_root = getattr(options, 'isolated_root', None)
    keep_isolated = getattr(options, 'keep_isolated', False)
    warm_pool = getattr(options, 'warm_components_from', None)
    clone_info = None
    host_backups = {}
    try:
        _check_evidence_dir_safe(getattr(options, 'evidence_dir', None), options.home)
        if keep_isolated or warm_pool is not None or (isolated_root is not None and (isolated_root / utm_isolated_env.MANIFEST_NAME).exists()):
            target_isolated = isolated_root or options.config.parent
            utm_isolated_env.prepare_isolated_env(
                target_isolated, options.executable, scenario=scenarios[0], home=options.home,
                components_source=env_mode, allow_reuse=False,
            )
            if warm_pool is not None:
                clone_info = utm_isolated_env.clone_components_apfs(
                    warm_pool, options.component_root, home=options.home, allowed_root=target_isolated,
                )
            if keep_isolated:
                host_backups = _backup_host_hooks(target_isolated, options.hooks_path)
    except utm_isolated_env.IsolatedEnvError as exc:
        return exc.returncode, {
            'status': _error_status(exc.returncode),
            'returncode': exc.returncode,
            'error': str(exc),
            'preflight': preflight,
            'scenarios': [],
            'summary': {'passed': 0, 'failed': 1},
        }

    default_runtime = (options.home / 'Library' / 'Application Support' / 'Memlane').resolve(strict=False)
    actual_runtime = options.config.parent.resolve(strict=False)
    sentinel = utm_guard.HostStateSentinel(options.home, protect_memlane_dir=(actual_runtime != default_runtime))
    results = []
    app_meta = {}
    for name in scenarios:
        gate = Gate(options)
        try:
            getattr(gate, name.replace('-', '_'))()
        except VerificationError:
            status = 'failed'
        else:
            status = 'passed'
        app_meta.update(gate.app_meta)
        results.append({'scenario': name, 'status': status, 'steps': gate.receipts})
    sentinel_report = sentinel.verify()
    passed = sum(item['status'] == 'passed' for item in results)
    if not sentinel_report['ok']:
        return EXIT_PROTECTED_PATH_VIOLATED, {
            'status': 'protected_path_violated',
            'returncode': EXIT_PROTECTED_PATH_VIOLATED,
            'preflight': preflight,
            'sentinel': sentinel_report,
            'scenarios': results,
            'summary': {'passed': passed, 'failed': len(results) - passed},
        }
    code = int(passed != len(results))
    isolated_info = None
    if keep_isolated and code == 0:
        target_isolated = isolated_root or options.config.parent
        bridge_summary = _sanitized_bridge_summary(target_isolated, options.config) if options.config.is_file() else {}
        existing = utm_isolated_env.read_isolated_manifest(target_isolated) or {}
        executed = list(dict.fromkeys([*(existing.get('executed_scenarios') or []), *scenarios]))
        merged_backups = {**(existing.get('host_config_backups') or {}), **host_backups}
        utm_isolated_env.update_isolated_manifest(
            target_isolated,
            bundled_runtime_hash=str(app_meta.get('bundled_runtime_hash') or existing.get('bundled_runtime_hash') or ''),
            store_id=str(bridge_summary.get('store_id') or existing.get('store_id') or ''),
            components_source=env_mode,
            executed_scenarios=executed,
            host_config_backups=merged_backups,
        )
        isolated_info = {'kept': True, 'environment_mode': env_mode, 'store_id': bridge_summary.get('store_id', ''),
                         'bridge_summary': bridge_summary, 'clone': clone_info}
    report = {
        'status': 'failed' if code else 'passed',
        'level': '1',
        'environment_mode': env_mode,
        'preflight': preflight,
        'sentinel': sentinel_report,
        'scenarios': results,
        'summary': {'passed': passed, 'failed': len(results) - passed},
        'returncode': code,
    }
    if isolated_info is not None:
        report['isolated_env'] = isolated_info
    elif clone_info is not None:
        report['clone'] = clone_info
    evidence_dir = getattr(options, 'evidence_dir', None)
    if evidence_dir is not None and code == 0:
        _write_evidence_json(evidence_dir, 'level1-report.json', report, home=options.home)
    return code, report


def main(argv=None):
    options = parse_args(argv)
    code, report = run_verification(options)
    if options.json or options.dry_run:
        print(json.dumps(report, ensure_ascii=False, indent=2))
    else:
        if report.get('error'):
            print(f"{report['status']} (exit {code}): {report['error']}")
        if report.get('preflight') and not report['preflight'].get('ok', True):
            print(f"preflight [{report['status']}] (exit {code})")
            for check in report['preflight'].get('checks', []):
                if not check.get('ok'):
                    print(f"  failed: {check['check']} — {check.get('code')} ({check.get('remediation', '')})")
        for scenario in report['scenarios']:
            print(f"{scenario['scenario']} [{scenario['status']}]")
            for step in scenario['steps']:
                suffix = f" — {step['error']}" if 'error' in step else ''
                print(f"  {step['status']}: {step['step']}{suffix}")
        if report.get('sentinel') and not report['sentinel'].get('ok', True):
            print(f"sentinel [{report['status']}] (exit {code}): {report['sentinel'].get('violations')}")
    return code


if __name__ == '__main__':
    sys.exit(main())
