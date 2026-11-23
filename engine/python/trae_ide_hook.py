"""TRAE CN's live command hooks. Capture only turns observed after binding."""
from datetime import datetime, timezone
import http.client
import json
import os
from pathlib import Path
import sys
import uuid
from urllib.parse import urlsplit

import agent_memory_adapter as memory

EVENTS = {'SessionStart', 'UserPromptSubmit', 'PostToolUse', 'Stop'}


def workspace(payload):
    roots = payload.get('workspace_roots')
    cwd = payload.get('cwd')
    # In a multi-root window, a global hook cwd is always the first root and
    # does not identify the project the turn actually belongs to.
    if not isinstance(cwd, str) or not Path(cwd).is_absolute() or not isinstance(roots, list) or len(roots) != 1:
        return None
    if not isinstance(roots[0], str) or not Path(roots[0]).is_absolute():
        return None
    return cwd if Path(cwd).resolve() == Path(roots[0]).resolve() else None


def now():
    return datetime.now(timezone.utc).isoformat()


def observe_turn(key, payload=None):
    from _capture_policy import capture_enabled, sanitize
    from _coding_memory import _SessionState, _truncate
    from _memlane_policy import permit

    with _SessionState(key) as state:
        if not state.acquired:
            return None
        previous = state.data.pop('trae_ide_turn', None)
        if payload is not None:
            prompt = payload.get('prompt')
            if isinstance(prompt, str) and prompt.strip() and capture_enabled() and permit('capture'):
                identifier = uuid.uuid4().hex
                state.data['trae_ide_turn'] = {'id': identifier, 'time': now(),
                                               'question': _truncate(sanitize(prompt), 8000)}
                state.save()
                return identifier
        state.save()
        return previous if payload is None else None


def handle(event, payload):
    if os.environ.get('MEMLANE_PROCESSING_SESSION') == '1':
        return {}
    if event not in EVENTS or not isinstance(payload, dict) or payload.get('hook_event_name') != event:
        return {}
    cwd = workspace(payload)
    if not cwd:
        return {}
    request = memory.request_fields({'operation': 'resolve', 'session_id': payload.get('session_id'), 'cwd': cwd})
    key, session, dataset = memory.setup(request)

    if event == 'SessionStart':
        observe_turn(key)
    elif event == 'UserPromptSubmit':
        turn_id = observe_turn(key, payload)
        if turn_id:
            memory.begin_turn({**request, 'event_id': turn_id}, key)
        prompt = payload.get('prompt')
        if isinstance(prompt, str) and prompt.strip():
            result = memory.handle({**request, 'operation': 'recall', 'query': prompt[:16000]})
            return {'hookSpecificOutput': {'hookEventName': event, 'additionalContext': result.get('context', '')}}
    elif event == 'PostToolUse':
        tool_id, name = payload.get('tool_use_id'), payload.get('tool_name')
        if isinstance(tool_id, str) and isinstance(name, str):
            output = payload.get('tool_response', {})
            memory.capture({**request, 'event_id': 'tool:' + tool_id, 'entry': {
                'type': 'trace', 'origin_function': name, 'method_params': payload.get('tool_input', {}),
                'method_return_value': output, 'time': now(),
                'status': 'error' if isinstance(output, dict) and output.get('is_error') is True else 'success',
            }}, key, session, dataset)
    else:
        turn = observe_turn(key)
        answer = payload.get('last_assistant_message')
        if turn and isinstance(answer, str) and answer.strip():
            memory.capture({**request, 'event_id': 'qa:' + turn['id'], 'entry': {
                'type': 'qa', 'question': turn['question'], 'answer': answer,
                'prompt_time': turn['time'], 'time': now(),
            }}, key, session, dataset)
        memory.handle({**request, 'operation': 'flush'})
    return {}


def forward(event, raw):
    binding_path = Path(__file__).resolve().parents[1] / '.memlane-installation.json'
    if binding_path.is_symlink() or binding_path.stat().st_mode & 0o077:
        return {}
    binding = json.loads(binding_path.read_text())
    if binding.get('host_kind') != 'trae_ide':
        return {}
    origin = urlsplit(binding['hook_origin'])
    if origin.scheme != 'http' or origin.hostname != '127.0.0.1':
        return {}
    token_path = Path(binding['hook_token_file'])
    if not token_path.is_absolute() or token_path.is_symlink() or token_path.stat().st_mode & 0o077:
        return {}
    connection = http.client.HTTPConnection(origin.hostname, origin.port, timeout=22)
    try:
        connection.request('POST', '/v1/trae-ide/hooks/' + event, raw, {
            'Authorization': 'Bearer ' + token_path.read_text().strip(),
            'Content-Type': 'application/json',
        })
        response = connection.getresponse()
        if response.status != 200:
            return {}
        data = response.read(memory.MAX_INPUT + 1)
        return json.loads(data) if len(data) <= memory.MAX_INPUT else {}
    finally:
        connection.close()


def main():
    dispatch = len(sys.argv) == 3 and sys.argv[2] == '--dispatch'
    try:
        raw = sys.stdin.buffer.read(memory.MAX_INPUT + 1)
        event = sys.argv[1]
        output = {}
        if event in EVENTS and len(raw) <= memory.MAX_INPUT and os.environ.get('MEMLANE_PROCESSING_SESSION') != '1':
            output = handle(event, json.loads(raw)) if dispatch else forward(event, raw)
    except Exception:
        if dispatch:
            try:
                from _plugin_common import hook_log
                hook_log('memlane_host_operation_unconfirmed', {'host': 'trae_ide'})
            except Exception:
                pass
        output = {}
    print(json.dumps(output, ensure_ascii=False))


if __name__ == '__main__':
    main()
