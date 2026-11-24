"""Compose TRAE CN user hooks while preserving other configured commands."""
import asyncio
import json
import os
from pathlib import Path
import re
import shlex

from project_policy import PolicyError
from trae_ide_hook import record_hook_error, scrub_diagnostic_secrets

LEGACY_EVENTS = ('SessionStart', 'UserPromptSubmit', 'PostToolUse', 'Stop')
EVENTS = ('SessionStart', 'UserPromptSubmit', 'PostToolUse', 'PostToolUseFailure', 'Stop', 'SessionEnd')

_DIAGNOSTIC_WORDS = frozenset('''traceback most recent call last file line error exception
    runtimeerror valueerror typeerror oserror permissionerror timeouterror subprocesserror
    connectionrefusederror filenotfounderror keyerror attributeerror assertionerror
    worker hook failed failure exited timed out timeout startup dispatch invalid output
    malformed json denied refused not found permission network disconnected unknown
    authorization x-api-key api_key token secret password'''.lower().split())


def redact_diagnostic_text(raw, max_len=240):
    """Keep the stderr tail's diagnostic vocabulary, dropping arbitrary prose."""
    text = raw.decode(errors='replace') if isinstance(raw, bytes) else raw
    lines = []
    for line in text.splitlines()[-12:]:
        line = scrub_diagnostic_secrets(line)
        words = re.findall(r'\[redacted\]|[\w-]+', line)
        clean = ' '.join(word for word in words if word.lower() in _DIAGNOSTIC_WORDS or word == '[redacted]')
        if clean:
            lines.append(clean)
    return ' '.join(lines)[-max_len:].lstrip() if max_len > 0 else ''


def record_dispatch_error(home, instance_id, event, *, status, error_code, error_type, stderr=b'', payload=None):
    try:
        state_dir = Path(home) / '.cognee-plugin' / ('trae_ide-' + instance_id)
        record_hook_error(event, error_code=error_code, error_type=error_type, status=status,
                          payload=payload, state_dir=state_dir, stderr_snippet=redact_diagnostic_text(stderr))
    except Exception:
        pass


def loader(installation, python):
    script = Path(installation['plugin_dir']) / 'scripts/trae_ide_hook.py'
    return {event: {'hooks': [{'type': 'command',
                              'command': shlex.join([python, '-B', str(script), event]),
                              'timeout': 25}]} for event in EVENTS}


def proposal(raw, previous, replacement):
    try:
        config = json.loads(raw) if raw else {'version': 1, 'hooks': {}}
        if not isinstance(config, dict) or type(config.get('version')) is not int or config['version'] != 1:
            raise ValueError()
        hooks = config['hooks']
        if not isinstance(hooks, dict) or any(
                not isinstance(groups, list) or any(not isinstance(group, dict) for group in groups)
                for groups in hooks.values()):
            raise ValueError()
    except (ValueError, KeyError, TypeError):
        raise PolicyError('trae_ide_configuration_requires_review', 409) from None

    previous, replacement = previous or {}, replacement or {}
    for event, groups in hooks.items():
        for group in groups:
            if 'trae_ide_hook.py' in json.dumps(group) and group != previous.get(event):
                raise PolicyError('trae_ide_hook_changed_requires_review', 409)
    for event in dict.fromkeys((*EVENTS, *previous, *replacement)):
        old, new = previous.get(event), replacement.get(event)
        if old is not None:
            groups = hooks.get(event, [])
            if groups.count(old) != 1:
                raise PolicyError('trae_ide_hook_changed_requires_review', 409)
            if new is not None:
                groups[groups.index(old)] = new
        elif new is not None:
            hooks.setdefault(event, []).append(new)
    return (json.dumps(config, ensure_ascii=False, indent=2) + '\n').encode()


def verify_hooks(raw, expected_hooks, script):
    try:
        if not isinstance(expected_hooks, dict) or set(expected_hooks) not in (set(EVENTS), set(LEGACY_EVENTS)):
            raise ValueError()
        for event, group in expected_hooks.items():
            commands = group['hooks']
            if (len(commands) != 1 or commands[0]['type'] != 'command'
                    or shlex.split(commands[0]['command'])[1:] != ['-B', str(script), event]):
                raise ValueError()
    except (KeyError, TypeError, ValueError, AttributeError, IndexError):
        raise PolicyError('trae_ide_hook_changed_requires_review', 409) from None
    proposal(raw, expected_hooks, None)


class HookDispatch:
    """Run an approved IDE adapter with the gateway's runtime privileges."""

    def __init__(self, registry, python):
        self.registry = registry
        self.python = python
        self.running = 0

    async def run(self, identity, event, raw):
        from agent_connections import safe_file, sha, verify_hook_artifact
        from agent_memory_adapter import MAX_INPUT
        from trae_ide_hook import workspace

        agent = self.registry.state['agents'].get(identity, {})
        record = self.registry.state.get('connections', {}).get(identity, {})
        installation = expected_hooks = proposal_sha = None
        if agent.get('kind') == 'trae_ide' and record.get('activated') is True:
            if record.get('status') == 'configured_pending_new_session':
                installation = agent.get('installation')
                expected_hooks = record.get('trae_ide_hooks') or record.get('trae_ide_next_hooks')
                proposal_sha = record.get('trae_ide_proposal_sha256')
            elif record.get('status') in ('prepared', 'preparation_unknown'):
                installation = record.get('active_installation')
                expected_hooks = record.get('trae_ide_hooks')
                active_record = record.get('active_record')
                if isinstance(active_record, dict):
                    proposal_sha = active_record.get('trae_ide_proposal_sha256')
        if not isinstance(installation, dict) or not isinstance(expected_hooks, dict):
            raise PolicyError('trae_ide_hook_not_authorized', 403)
        if event not in EVENTS or event not in expected_hooks or len(raw) > MAX_INPUT:
            raise PolicyError('invalid_trae_ide_hook')
        payload = json.loads(raw)
        if not isinstance(payload, dict) or payload.get('hook_event_name') != event:
            raise PolicyError('invalid_trae_ide_hook')
        profile = agent.get('host_profile')

        def verified_script():
            verify_hook_artifact({**installation, 'host_kind': 'trae_ide', 'instance_id': identity}, profile)
            raw_hooks = safe_file(Path(profile) / 'hooks.json')
            if isinstance(proposal_sha, str) and sha(raw_hooks) != proposal_sha:
                raise PolicyError('trae_ide_hook_changed_requires_review', 409)
            script = Path(installation['plugin_dir']) / 'scripts/trae_ide_hook.py'
            verify_hooks(raw_hooks, expected_hooks, script)
            return script

        if self.running >= 8:
            raise PolicyError('trae_ide_hook_busy', 503)
        self.running += 1
        try:
            script = await asyncio.to_thread(verified_script)
            cwd = workspace(payload)
            if not cwd or not Path(cwd).is_dir():
                return {}
            environment = {key: value for key, value in os.environ.items()
                           if not key.startswith(('COGNEE_', 'MEMLANE_', 'PYTHON'))}
            environment['HOME'] = installation['home']
            process = await asyncio.create_subprocess_exec(
                self.python, '-B', str(script), event, '--dispatch', cwd=cwd, env=environment, umask=0o077,
                stdin=asyncio.subprocess.PIPE, stdout=asyncio.subprocess.PIPE, stderr=asyncio.subprocess.PIPE,
            )
            async def report(code, error_type, stderr):
                await asyncio.to_thread(record_dispatch_error, installation['home'], identity, event,
                                        status=process.returncode, error_code=code, error_type=error_type,
                                        stderr=stderr, payload=payload)

            # Keep draining the original pipes after killing the worker so
            # timeout diagnostics include bytes already read by communicate.
            communication = asyncio.create_task(process.communicate(raw))
            try:
                output, stderr = await asyncio.wait_for(asyncio.shield(communication), timeout=20)
            except (asyncio.TimeoutError, asyncio.CancelledError) as error:
                if process.returncode is None:
                    try:
                        process.kill()
                    except ProcessLookupError:
                        pass
                _, stderr = await asyncio.shield(communication)
                if isinstance(error, asyncio.TimeoutError):
                    await report('dispatch_timeout', 'TimeoutError', stderr)
                raise
            if process.returncode:
                await report('dispatch_exit_nonzero', 'SubprocessError', stderr)
                raise PolicyError('trae_ide_hook_result_unconfirmed', 503)
            try:
                if len(output) > MAX_INPUT:
                    raise ValueError()
                result = json.loads(output)
                if not isinstance(result, dict):
                    raise ValueError()
                return result
            except (ValueError, RecursionError):
                await report('dispatch_invalid_output', 'ValueError', stderr)
                raise PolicyError('trae_ide_hook_result_unconfirmed', 503) from None
        finally:
            self.running -= 1
