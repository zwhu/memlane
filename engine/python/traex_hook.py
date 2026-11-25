"""TraeX command hooks using Cognee's Codex rollout reader for questions."""
import json
import os
import sys
from datetime import datetime, timezone
from pathlib import Path

import agent_memory_adapter as memory

EVENTS = {'SessionStart', 'UserPromptSubmit', 'PostToolUse', 'PostToolUseFailure', 'Stop', 'SessionEnd'}


def transcript_path(value, session_id):
    """Accept only the current session's rollout in the TraeX profile."""
    if not isinstance(value, str) or not value:
        return None
    path = Path(value)
    if (not path.is_absolute() or not path.name.startswith('rollout-')
            or not path.name.endswith(f'-{session_id}.jsonl')):
        return None
    try:
        resolved = path.resolve(strict=True)
        if resolved != path or not resolved.is_file():
            return None
        profile = Path(os.environ.get('TRAECLI_HOME') or os.environ.get('TRAE_HOME') or Path.home() / '.trae').resolve()
        if any(resolved.is_relative_to(root) for root in (profile / 'sessions', profile / 'cli/sessions')):
            return resolved
    except (OSError, RuntimeError, ValueError):
        pass
    return None


def capture_questions(event, payload, request, key, session, dataset):
    from _capture_policy import capture_enabled, sanitize
    from _coding_memory import _SessionState, _deliver, _qa_entries, parse_codex
    from _memlane_policy import capture_boundary, capture_decision

    with _SessionState(key) as state:
        if not state.acquired:
            return {'status': 'capture_state_locked'}
        data = state.data
        previous_path = data.get('traex_transcript')
        existing_history = event == 'SessionStart' and payload.get('source') in ('resume', 'compact', 'fork')
        if not previous_path and (existing_history or 'traex_exclude_history' not in data):
            data['traex_exclude_history'] = bool(existing_history or data.get('host_events'))
        path = transcript_path(payload.get('transcript_path') or previous_path, request['session_id'])
        decision = capture_decision(dataset) if capture_enabled() else ('disabled', None)
        status, version = decision
        if event in ('SessionStart', 'UserPromptSubmit'):
            data.pop('traex_active_turn', None)
        if status == 'unknown':
            state.save()
            return {'status': 'capture_policy_unconfirmed'}
        if path is None or (previous_path and str(path) != previous_path):
            if status != 'enabled':
                data['traex_exclude_history'] = True
                data.pop('traex_active_turn', None)
            state.save()
            return {'status': 'transcript_unavailable'}

        if data.pop('traex_exclude_history', False):
            # Baseline existing and unauthorized history without reading it.
            capture_boundary(path, enabled=False, decision=decision)
        data['traex_transcript'] = str(path)
        new_turn = event == 'UserPromptSubmit'
        offset = capture_boundary(path, decision=decision, new_turn=new_turn)
        turn_id = str(payload.get('turn_id') or '')
        if new_turn and offset is not None:
            data['traex_active_turn'] = {'version': version, 'turn_id': turn_id}
        elif offset is None or event in ('Stop', 'SessionEnd'):
            data.pop('traex_active_turn', None)
        turn = data.get('traex_active_turn')
        state.save()
        if event.startswith('PostToolUse'):
            if turn and turn['version'] == version and (not turn_id or turn_id == turn['turn_id']):
                return {'status': 'tool_allowed', 'decision': decision}
            return {'status': 'skipped_policy'}
        if offset is None or event not in ('Stop', 'SessionEnd'):
            return {'status': 'capture_boundary_observed'}

        parsed = parse_codex(path, offset)
        entries = _qa_entries(
            {**payload, 'transcript_path': str(path)}, parsed, data, request['cwd'], sanitize, event == 'SessionEnd',
        )
        if entries:
            _deliver(dataset, session, entries)
            data['host_capture_generation'] = data.get('host_capture_generation', 0) + len(entries)
            state.save()
        return {'status': 'captured', 'qa': len(entries)}


def handle(event, payload):
    if os.environ.get('MEMLANE_PROCESSING_SESSION') == '1':
        return {}
    if event not in EVENTS or not isinstance(payload, dict):
        raise ValueError('unsupported_traex_event')
    request = {'operation': 'resolve', 'session_id': payload.get('session_id'), 'cwd': payload.get('cwd')}
    memory.request_fields(request)
    key, session, dataset = memory.setup(request)
    if event == 'SessionStart':
        capture_questions(event, payload, request, key, session, dataset)
        return {}
    if event == 'UserPromptSubmit':
        from _coding_memory import begin_turn
        prompt = payload.get('prompt')
        if not isinstance(prompt, str):
            raise ValueError('traex_prompt_required')
        capture_questions(event, payload, request, key, session, dataset)
        begin_turn(key, prompt, request['cwd'])
        result = memory.handle({**request, 'operation': 'recall', 'query': prompt[:16000]})
        return {'hookSpecificOutput': {'hookEventName': 'UserPromptSubmit',
                                       'additionalContext': result.get('context', '')}}
    if event.startswith('PostToolUse'):
        tool_id, name = payload.get('tool_use_id'), payload.get('tool_name')
        if not isinstance(tool_id, str) or not isinstance(name, str):
            raise ValueError('traex_tool_identity_required')
        boundary = capture_questions(event, payload, request, key, session, dataset)
        if boundary['status'] != 'tool_allowed':
            return boundary
        return memory.capture({**request, 'operation': 'capture', 'event_id': 'tool:' + tool_id, 'entry': {
            'type': 'trace', 'origin_function': name, 'method_params': payload.get('tool_input', {}),
            'method_return_value': payload.get('tool_response', ''),
            'status': 'error' if event == 'PostToolUseFailure' else 'success',
            'time': datetime.now(timezone.utc).isoformat(),
        }}, key, session, dataset, expected_decision=boundary['decision'])
    result = capture_questions(event, payload, request, key, session, dataset)
    flushed = memory.handle({**request, 'operation': 'flush', 'final': event == 'SessionEnd'})
    return result if event == 'Stop' else flushed


def main():
    try:
        raw = sys.stdin.buffer.read(memory.MAX_INPUT + 1)
        if len(raw) > memory.MAX_INPUT:
            raise ValueError('request_limit')
        event = sys.argv[1]
        result = handle(event, json.loads(raw))
        # Only prompt hooks may inject context. Other command hooks must stay
        # neutral: no decisions, continuation requests or status-as-instruction.
        output = result if event == 'UserPromptSubmit' else {}
    except Exception:
        try:
            from _plugin_common import hook_log
            hook_log('memlane_host_operation_unconfirmed', {'host': 'traex'})
        except Exception:
            pass
        output = {}
    print(json.dumps(output, ensure_ascii=False))


if __name__ == '__main__':
    main()
