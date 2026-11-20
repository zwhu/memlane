"""Managed Claude Code and Cursor host-plugin hooks.

Installed beside the fixed host scripts as _memlane_host.py. Keeps capture
boundaries, fallback guards, and deferred recall rebuilds directly lintable
and unit-testable without inline code generation.
"""
import importlib
import json
import os
from pathlib import Path
import time
import uuid


def _policy(policy_mod=None):
    return policy_mod if policy_mod is not None else importlib.import_module('_memlane_policy')


def _plugin_common(common_mod=None):
    return common_mod if common_mod is not None else importlib.import_module('_plugin_common')


def _coding_memory(coding_mod=None):
    return coding_mod if coding_mod is not None else importlib.import_module('_coding_memory')


def begin_capture_boundary(policy_mod, transcript):
    """Record only a stat at installation/session activation; never read history."""
    with policy_mod._state() as state:
        if state.get('host_boundary_established'):
            return
        if not transcript:
            raise policy_mod.NotReady('host_transcript_required')
        try:
            state['cursor'] = policy_mod._position(transcript)
        except FileNotFoundError:
            # Unknown material arriving later cannot be assumed to be new.
            # The first live capture establishes its stat and skips that turn.
            state['paused'] = True
        state['host_boundary_established'] = True


def claude_begin_capture_boundary(policy_mod, transcript, *, source=None, session_key=None, environ=None, common_mod=None):
    """A native startup may precede creation of its own new transcript."""
    env = os.environ if environ is None else environ
    common = _plugin_common(common_mod)
    path = Path(transcript).absolute() if transcript else None
    fresh = None
    if path and source == 'startup' and session_key == common.get_session_key():
        try:
            if str(uuid.UUID(session_key)) != session_key:
                raise ValueError()
            profile = Path(env.get('CLAUDE_CONFIG_DIR') or Path.home() / '.claude').absolute()
            relative = path.relative_to(profile / 'projects')
            if len(relative.parts) != 2 or path.name != session_key + '.jsonl':
                raise ValueError()
            if any(parent.is_symlink() for parent in (path, *path.parents)):
                raise ValueError()
            parent = path.parent.stat()
            decision = policy_mod.current()
            rule = decision['policy']
            if 'write' in rule['permissions'] and rule['can_capture']:
                fresh = {
                    'path': str(path),
                    'parent_inode': parent.st_ino,
                    'parent_device': parent.st_dev,
                    'revision': decision['revision'],
                    'observed_absent_at': time.time(),
                }
        except (ValueError, OSError, KeyError, policy_mod.NotReady):
            pass
    with policy_mod._state() as state:
        if state.get('host_boundary_established'):
            return
        if not path:
            raise policy_mod.NotReady('host_transcript_required')
        try:
            state['cursor'] = policy_mod._position(path)
        except FileNotFoundError:
            state['paused'] = True
            if fresh:
                state['fresh_host_transcript'] = fresh
        state['host_boundary_established'] = True


def claude_capture_boundary(policy_mod, fallback_boundary, transcript, *, enabled=True):
    with policy_mod._state() as state:
        fresh = state.get('fresh_host_transcript')
    if fresh:
        path = Path(transcript).absolute()
        try:
            decision = policy_mod.current()
            parent = path.parent.stat()
            stat = path.stat()
            valid = (
                enabled
                and decision['revision'] == fresh['revision']
                and 'write' in decision['policy']['permissions']
                and decision['policy']['can_capture']
                and str(path) == fresh['path']
                and parent.st_ino == fresh['parent_inode']
                and parent.st_dev == fresh['parent_device']
                and path.is_file()
                and not any(p.is_symlink() for p in (path, *path.parents))
                and getattr(stat, 'st_birthtime', stat.st_ctime) >= fresh['observed_absent_at']
            )
        except FileNotFoundError:
            # SessionStart also schedules an early flush. Keep the absence
            # receipt until this new native transcript actually exists.
            return None
        except (OSError, KeyError, policy_mod.NotReady):
            valid = False
        with policy_mod._state() as state:
            if state.get('fresh_host_transcript') == fresh:
                state.pop('fresh_host_transcript', None)
                if valid and not state.get('cursor_unconfirmed'):
                    state.pop('paused', None)
                    state['cursor'] = {**policy_mod._position(path), 'size': 0}
                    return 0
    return fallback_boundary(transcript, enabled=enabled)


def claude_transcript_files(transcript, fallback_files, *, policy_mod=None):
    # Sidechain files have independent histories. Managed capture only reads
    # this host session's bound transcript; subagents must bind independently.
    return [transcript] if _policy(policy_mod).managed() else fallback_files(transcript)


def claude_session_start_bind(config, payload, cwd, session_key, *, policy_mod=None):
    policy = _policy(policy_mod)
    try:
        config['dataset'] = policy.bind(cwd, session_key)
        policy.begin_capture_boundary(
            str(payload.get('transcript_path') or ''),
            source=payload.get('source'),
            session_key=session_key,
        )
    except (policy.NotReady, OSError, ValueError):
        return {'hookSpecificOutput': {'hookEventName': 'SessionStart'}}
    return None


def managed_python_tool_runtime(load_env_file, environ, *, policy_mod=None, common_mod=None):
    load_env_file()
    policy = _policy(policy_mod)
    if not policy.managed():
        return None
    common = _plugin_common(common_mod)
    runtime = common.shell_runtime_overrides(environ['COGNEE_BASE_URL'])
    if not runtime.get('host_key'):
        raise RuntimeError('managed_host_session_required')
    common.set_session_key(runtime['host_key'])
    target = policy.dataset()
    return {
        'service_url': environ['COGNEE_BASE_URL'],
        'api_key': environ['COGNEE_API_KEY'],
        'dataset': target,
        'dataset_id': target,
        'dataset_ids': target,
        'session_id': common.resolve_cognee_session_id(common.get_session_key()),
        'recall_auto': '',
    }


def managed_cli_recall_fallback(*, policy_mod=None):
    if _policy(policy_mod).managed():
        return json.dumps({'error': 'managed_server_unavailable', 'authoritative': False})
    return None


def cursor_context_lookup(prompt, cwd, *, policy_mod=None, coding_mod=None):
    if not _policy(policy_mod).managed():
        return None
    context = _coding_memory(coding_mod).build_block(prompt, cwd)
    return {'hookSpecificOutput': {'hookEventName': 'UserPromptSubmit', 'additionalContext': context}}


def cursor_store_allowed(capture_enabled, *, policy_mod=None):
    return bool(capture_enabled() and _policy(policy_mod).permit('capture'))


def cursor_hold_record(turn, query, cwd, sanitize):
    return {
        'turn': str(turn or ''),
        'context': 'managed_deferred_recall',
        'query': sanitize(query)[:16000],
        'cwd': cwd,
    }


def cursor_rebuild_held_context(load_env_file, session, held, *, common_mod=None, coding_mod=None):
    load_env_file()
    _plugin_common(common_mod).set_session_key(session)
    if 'query' not in held:
        return ''
    return _coding_memory(coding_mod).build_block(str(held.get('query') or ''), str(held.get('cwd') or ''))
