"""TRAE CN's live command hooks. Capture only turns observed after binding."""
from datetime import datetime, timezone
import fcntl
import http.client
import json
import os
from pathlib import Path
import re
import stat
import sys
import time
import uuid
from urllib.parse import urlsplit

import agent_memory_adapter as memory

EVENTS = {'SessionStart', 'UserPromptSubmit', 'PostToolUse', 'PostToolUseFailure', 'Stop', 'SessionEnd'}
MAX_PENDING_TURNS = 16
MAX_HOOK_ERRORS = 5
MAX_HOOK_ERROR_BYTES = 16384
HOOK_ERROR_FILE = 'last-hook-error.json'

# Only fields defined by the host protocol can be reported. Arbitrary JSON
# keys can themselves contain credentials or user content.
PAYLOAD_KEYS = {
    'hook_event_name', 'session_id', 'cwd', 'workspace_roots', 'prompt',
    'tool_use_id', 'tool_name', 'tool_input', 'tool_response', 'error',
    'is_interrupt', 'last_assistant_message', 'transcript_path', 'permission_mode',
    'stop_hook_active', 'source', 'reason',
}
_SECRET_FIELD = re.compile(
    r"""(?i)(?<![a-z0-9])(authorization|x-api-key|api[_-]?key|token|secret|password)['"`]?[ \t]*[:=][ \t]*[^\r\n]*""")
_SECRET_TOKEN = re.compile(r'(?i)\bbearer[ \t]+\S+|\b(?:sk-[\w-]+|gh\w*_[\w-]+|eyJ[\w.-]+)')


def scrub_diagnostic_secrets(text):
    return _SECRET_TOKEN.sub('[redacted]', _SECRET_FIELD.sub(r'\1 [redacted]', text))


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


def observe_turn(event, key, payload):
    from _capture_policy import capture_enabled, sanitize
    from _coding_memory import _SessionState, _truncate
    from _memlane_policy import permit

    with _SessionState(key) as state:
        if not state.acquired:
            raise ValueError('capture_state_locked')
        previous = state.data.pop('trae_ide_turn', None)
        turns = state.data.setdefault('trae_ide_turns', [])
        if isinstance(previous, dict):
            turns.insert(0, previous)
        drained = []
        turn_id = None
        if event == 'SessionStart':
            state.data.pop('trae_ide_turns', None)
        elif event == 'UserPromptSubmit':
            prompt = payload.get('prompt')
            if isinstance(prompt, str) and prompt.strip():
                turn = {'id': uuid.uuid4().hex, 'time': now()}
                if capture_enabled() and permit('capture'):
                    turn['question'] = _truncate(sanitize(prompt), 8000)
                    turn_id = turn['id']
                else:
                    turn['skipped_policy'] = True
                turns.append(turn)
        elif event == 'Stop' and turns:
            drained.append(turns.pop(0))
        elif event == 'PostToolUseFailure':
            name = payload.get('tool_name')
            if turns and not turns[-1].get('skipped_policy') and isinstance(name, str) and name.strip():
                failures = turns[-1].setdefault('tool_failures', [])
                failures.append(_truncate(sanitize(name), 80))
                del failures[:-8]
        if event == 'SessionEnd' or (event == 'PostToolUseFailure' and payload.get('is_interrupt') is True):
            drained = turns[:]
            turns.clear()
        del turns[:-MAX_PENDING_TURNS]
        state.save()
        return turn_id, drained


def interrupted_answer_for(turn):
    tools = turn.get('tool_failures')
    if tools:
        return '[Turn interrupted after tool failure: ' + ', '.join(tools) + ']'
    return '[Turn interrupted before assistant completion]'


def capture_turns(turns, answer, request, key, session, dataset):
    for index, turn in enumerate(turns):
        if turn.get('skipped_policy'):
            continue
        reply = answer if index == len(turns) - 1 and isinstance(answer, str) and answer.strip() else interrupted_answer_for(turn)
        memory.capture({**request, 'event_id': 'qa:' + turn['id'], 'entry': {
            'type': 'qa', 'question': turn['question'], 'answer': reply,
            'prompt_time': turn['time'], 'time': now(),
        }}, key, session, dataset)


def capture_tool(payload, failed, request, key, session, dataset):
    tool_id, name = payload.get('tool_use_id'), payload.get('tool_name')
    if not isinstance(tool_id, str) or not isinstance(name, str):
        return
    output = payload.get('tool_response', payload.get('error', {}) if failed else {})
    entry = {
        'type': 'trace', 'origin_function': name, 'method_params': payload.get('tool_input', {}),
        'method_return_value': output, 'time': now(),
        'status': 'error' if failed or (isinstance(output, dict) and output.get('is_error') is True) else 'success',
    }
    if failed and isinstance(payload.get('error'), str):
        entry['error_message'] = payload['error']
    memory.capture({**request, 'event_id': 'tool:' + tool_id, 'entry': entry}, key, session, dataset)


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
        observe_turn(event, key, payload)
    elif event == 'UserPromptSubmit':
        turn_id, _ = observe_turn(event, key, payload)
        if turn_id:
            memory.begin_turn({**request, 'event_id': turn_id}, key)
        prompt = payload.get('prompt')
        if isinstance(prompt, str) and prompt.strip():
            result = memory.handle({**request, 'operation': 'recall', 'query': prompt[:16000]})
            return {'hookSpecificOutput': {'hookEventName': event, 'additionalContext': result.get('context', '')}}
    elif event == 'PostToolUse':
        capture_tool(payload, False, request, key, session, dataset)
    else:
        flush = {**request, 'operation': 'flush'}
        if event == 'SessionEnd':
            flush['final'] = True
        failed = event == 'PostToolUseFailure'
        try:
            if failed:
                capture_tool(payload, True, request, key, session, dataset)
        finally:
            try:
                _, turns = observe_turn(event, key, payload)
                answer = None if failed else payload.get('last_assistant_message')
                capture_turns(turns, answer, request, key, session, dataset)
            finally:
                memory.handle(flush)
    return {}


def binding_path():
    return Path(__file__).resolve().parents[1] / '.memlane-installation.json'


def hook_error_state_dir():
    try:
        from _plugin_common import _PLUGIN_DIR
        return Path(_PLUGIN_DIR)
    except Exception:
        path = binding_path()
        if path.is_symlink() or not path.is_file() or path.stat().st_mode & 0o077:
            return None
        instance_id = json.loads(path.read_text())['instance_id']
        if isinstance(instance_id, str) and re.fullmatch(r'[a-zA-Z0-9_-]{1,128}', instance_id):
            return Path.home() / '.cognee-plugin' / ('trae_ide-' + instance_id)
    return None


def _read_hook_errors(directory):
    with os.fdopen(os.open(HOOK_ERROR_FILE, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK,
                          dir_fd=directory), 'rb') as stream:
        info = os.fstat(stream.fileno())
        if not stat.S_ISREG(info.st_mode) or stat.S_IMODE(info.st_mode) != 0o600 or info.st_size > MAX_HOOK_ERROR_BYTES:
            return []
        data = stream.read(MAX_HOOK_ERROR_BYTES + 1)
    if len(data) > MAX_HOOK_ERROR_BYTES:
        return []
    value = json.loads(data)
    entries = value.get('entries') if isinstance(value, dict) else None
    return entries if isinstance(entries, list) and len(entries) <= MAX_HOOK_ERRORS else []


def read_hook_errors(state_dir):
    try:
        if state_dir.is_symlink() or state_dir.parent.is_symlink() or (state_dir / HOOK_ERROR_FILE).is_symlink():
            return []
        directory = os.open(state_dir, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
        try:
            if stat.S_IMODE(os.fstat(directory).st_mode) == 0o700:
                return _read_hook_errors(directory)
        finally:
            os.close(directory)
    except (OSError, ValueError, RecursionError):
        pass
    return []


def record_hook_error(event, *, error_code, error_type, status=None, payload=None, state_dir=None, stderr_snippet=''):
    """Record bounded failure metadata without disrupting the host hook."""
    try:
        state_dir = state_dir if state_dir is not None else hook_error_state_dir()
        if state_dir is None or state_dir.is_symlink() or state_dir.parent.is_symlink():
            return
        target = state_dir / HOOK_ERROR_FILE
        if target.is_symlink():
            return
        state_dir.mkdir(mode=0o700, parents=True, exist_ok=True)
        directory = os.open(state_dir, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
        try:
            os.fchmod(directory, 0o700)
            # Forwarders and gateway workers can report failures concurrently.
            fcntl.flock(directory, fcntl.LOCK_EX)
            if target.is_symlink():
                return
            try:
                entries = _read_hook_errors(directory)
            except (OSError, ValueError, RecursionError):
                entries = []
            at = time.time()
            entry = {
                'at': at, 'timestamp': datetime.fromtimestamp(at, timezone.utc).isoformat(),
                'event': event if event in EVENTS else '', 'script': 'trae_ide_hook.py',
                'status': status if type(status) is int else None,
                'error_code': error_code[:80], 'error_type': error_type[:80],
                'reason': error_code[:120],
                'payload_keys': sorted(PAYLOAD_KEYS.intersection(payload)) if isinstance(payload, dict) else [],
            }
            if stderr_snippet:
                entry['stderr_snippet'] = stderr_snippet[:240]
            entries = [item for item in entries if isinstance(item, dict)][1 - MAX_HOOK_ERRORS:] + [entry]
            data = json.dumps({'entries': entries}).encode()
            while len(data) > MAX_HOOK_ERROR_BYTES and len(entries) > 1:
                entries.pop(0)
                data = json.dumps({'entries': entries}).encode()
            temporary = '.hook-error-' + uuid.uuid4().hex
            fd = os.open(temporary, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600, dir_fd=directory)
            try:
                with os.fdopen(fd, 'wb') as stream:
                    os.fchmod(stream.fileno(), 0o600)
                    stream.write(data)
                    stream.flush()
                    os.fsync(stream.fileno())
                if not target.is_symlink():
                    os.replace(temporary, HOOK_ERROR_FILE, src_dir_fd=directory, dst_dir_fd=directory)
            finally:
                try:
                    os.unlink(temporary, dir_fd=directory)
                except FileNotFoundError:
                    pass
        finally:
            os.close(directory)
    except Exception:
        pass


def payload_from(raw):
    try:
        return json.loads(raw)
    except (ValueError, TypeError, RecursionError):
        return None


def forward(event, raw):
    if event not in EVENTS:
        return {}

    def failure(code, error_type='ValueError', status=None):
        record_hook_error(event, error_code=code, error_type=error_type, status=status, payload=payload_from(raw))
        return {}

    path = binding_path()
    try:
        if path.is_symlink() or not path.is_file() or path.stat().st_mode & 0o077:
            return failure('invalid_binding')
        binding = json.loads(path.read_text())
        if not isinstance(binding, dict) or binding.get('host_kind') != 'trae_ide':
            return failure('invalid_binding')
    except (OSError, ValueError, RecursionError) as error:
        return failure('invalid_binding', type(error).__name__)

    try:
        origin = urlsplit(binding['hook_origin'])
        if (origin.scheme != 'http' or origin.hostname != '127.0.0.1' or origin.username is not None
                or origin.path or origin.query or origin.fragment):
            return failure('invalid_origin')
        port = origin.port
        if not port:
            return failure('invalid_origin')
    except (AttributeError, KeyError, ValueError, TypeError) as error:
        return failure('invalid_origin', type(error).__name__)

    try:
        token_path = Path(binding['hook_token_file'])
        if not token_path.is_absolute() or token_path.is_symlink() or not token_path.is_file() or token_path.stat().st_mode & 0o077:
            return failure('invalid_token_file')
        token = token_path.read_text().strip()
        if not token or not token.isascii() or any(character.isspace() for character in token):
            return failure('invalid_token_file')
    except (KeyError, OSError, TypeError, ValueError) as error:
        return failure('invalid_token_file', type(error).__name__)

    try:
        connection = http.client.HTTPConnection(origin.hostname, port, timeout=22)
        try:
            connection.request('POST', '/v1/trae-ide/hooks/' + event, raw, {
                'Authorization': 'Bearer ' + token,
                'Content-Type': 'application/json',
            })
            response = connection.getresponse()
            if response.status != 200:
                return failure(f'http_{response.status}', 'HTTPError', response.status)
            data = response.read(memory.MAX_INPUT + 1)
            if len(data) > memory.MAX_INPUT:
                return failure('response_too_large', status=response.status)
            output = payload_from(data)
            return output if isinstance(output, dict) else failure('invalid_response_json', status=response.status)
        finally:
            connection.close()
    except (OSError, http.client.HTTPException) as error:
        return failure('network_error', type(error).__name__)


def main():
    dispatch = len(sys.argv) == 3 and sys.argv[2] == '--dispatch'
    event = sys.argv[1] if len(sys.argv) > 1 else ''
    raw = payload = None
    output = {}
    try:
        raw = sys.stdin.buffer.read(memory.MAX_INPUT + 1)
        if len(raw) > memory.MAX_INPUT:
            record_hook_error(event, error_code='input_too_large', error_type='ValueError')
        elif event in EVENTS and os.environ.get('MEMLANE_PROCESSING_SESSION') != '1':
            if dispatch:
                payload = json.loads(raw)
                output = handle(event, payload)
            else:
                output = forward(event, raw)
    except Exception as error:
        if not dispatch:
            payload = payload_from(raw)
        record_hook_error(event, error_code='hook_exception', error_type=type(error).__name__, payload=payload)
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
