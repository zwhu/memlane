"""Compose TRAE CN user hooks while preserving other configured commands."""
import asyncio
import json
import os
from pathlib import Path
import shlex

from project_policy import PolicyError

EVENTS = ('SessionStart', 'UserPromptSubmit', 'PostToolUse', 'Stop')


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
    for event in EVENTS:
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
        if not isinstance(expected_hooks, dict) or set(expected_hooks) != set(EVENTS):
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
        if event not in EVENTS or len(raw) > MAX_INPUT:
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
                stdin=asyncio.subprocess.PIPE, stdout=asyncio.subprocess.PIPE, stderr=asyncio.subprocess.DEVNULL,
            )
            try:
                output, _ = await asyncio.wait_for(process.communicate(raw), timeout=20)
            except (asyncio.TimeoutError, asyncio.CancelledError):
                if process.returncode is None:
                    try:
                        process.kill()
                    except ProcessLookupError:
                        pass
                await process.communicate()
                raise
            if process.returncode or len(output) > MAX_INPUT:
                raise PolicyError('trae_ide_hook_result_unconfirmed', 503)
            return json.loads(output)
        finally:
            self.running -= 1
