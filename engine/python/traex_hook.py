"""TraeX command-hook payload adaptation; no transcript fallback or trust edits."""
import json
import sys

import agent_memory_adapter as memory

EVENTS = {'SessionStart', 'UserPromptSubmit', 'PostToolUse', 'PostToolUseFailure', 'Stop', 'SessionEnd'}


def handle(event, payload):
    if event not in EVENTS or not isinstance(payload, dict):
        raise ValueError('unsupported_traex_event')
    request = {'operation': 'resolve', 'session_id': payload.get('session_id'), 'cwd': payload.get('cwd')}
    memory.request_fields(request)
    key, session, dataset = memory.setup(request)
    from _memlane_policy import permit
    from _plugin_common import remember_pending_prompt, pop_pending_prompt
    from _capture_policy import capture_enabled, sanitize
    if event == 'SessionStart':
        return {}
    if event == 'UserPromptSubmit':
        prompt = payload.get('prompt')
        if not isinstance(prompt, str):
            raise ValueError('traex_prompt_required')
        if capture_enabled() and permit('capture'):
            remember_pending_prompt(session, sanitize(prompt), turn_id=str(payload.get('turn_id') or ''))
        else:
            # Forget a previously pending prompt if permission was revoked;
            # restoring permission must not pair it with a later response.
            pop_pending_prompt(session, turn_id=str(payload.get('turn_id') or ''))
        result = memory.handle({**request, 'operation': 'recall', 'query': prompt[:16000]})
        return {'hookSpecificOutput': {'hookEventName': 'UserPromptSubmit',
                                       'additionalContext': result.get('context', '')}}
    if event.startswith('PostToolUse'):
        tool_id, name = payload.get('tool_use_id'), payload.get('tool_name')
        if not isinstance(tool_id, str) or not isinstance(name, str):
            raise ValueError('traex_tool_identity_required')
        return memory.handle({**request, 'operation': 'capture', 'event_id': 'tool:' + tool_id, 'entry': {
            'type': 'trace', 'origin_function': name, 'method_params': payload.get('tool_input', {}),
            'method_return_value': payload.get('tool_response', ''),
            'status': 'error' if event == 'PostToolUseFailure' else 'success',
        }})
    if event == 'Stop':
        answer = payload.get('last_assistant_message')
        if not isinstance(answer, str) or not answer.strip():
            return {'status': 'missing_answer'}
        pending = pop_pending_prompt(session, turn_id=str(payload.get('turn_id') or ''))
        if not pending.get('prompt'):
            return {'status': 'missing_prompt'}
        turn = str(payload.get('turn_id') or pending.get('ts') or '')
        result = memory.handle({**request, 'operation': 'capture', 'event_id': 'answer:' + turn, 'entry': {
            'type': 'qa', 'question': pending['prompt'], 'answer': answer,
            'prompt_time': pending.get('ts', ''),
        }})
        memory.handle({**request, 'operation': 'flush'})
        return result
    return memory.handle({**request, 'operation': 'flush', 'final': True})


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
