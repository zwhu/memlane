"""JSON stdin/stdout bridge, installed beside the fixed official hook scripts.

All content transport, sanitization, buffer and processing reuse those scripts.
No listener, local SDK, transcript reader or content store is introduced here.
"""
import hashlib
import json
import os
from pathlib import Path
import sys
import uuid
import urllib.request
import urllib.error
import time

MAX_INPUT = 128 * 1024
OPERATIONS = {'resolve', 'status', 'recall', 'remember', 'forget', 'capture', 'flush'}


class AccessDenied(Exception):
    def __init__(self, permission):
        self.permission = permission


def explicit_access(permission, dataset):
    # A failed policy fetch remains unconfirmed, rather than being presented
    # as an authoritative denial. current() checks the live official ACL.
    from _memlane_policy import current
    if permission not in current(dataset)['policy']['permissions']:
        raise AccessDenied(permission)


def request_fields(request):
    if not isinstance(request, dict) or set(request) - {'operation', 'session_id', 'cwd', 'event_id', 'entry', 'query', 'content', 'data_id', 'final'}:
        raise ValueError('invalid_request_fields')
    if request.get('operation') not in OPERATIONS:
        raise ValueError('unsupported_operation')
    for field in ('session_id', 'cwd'):
        if not isinstance(request.get(field), str) or not request[field] or len(request[field]) > 4096:
            raise ValueError('host_session_and_workspace_required')
    if not Path(request['cwd']).is_absolute():
        raise ValueError('absolute_workspace_required')
    return request


def setup(request):
    marker = Path(__file__).resolve().parents[1] / '.memlane-installation.json'
    if marker.is_symlink() or marker.stat().st_mode & 0o077:
        raise ValueError('private_installation_binding_required')
    binding = json.loads(marker.read_text())
    if binding.get('host_kind') not in ('pi', 'traex'):
        raise ValueError('unsupported_installation_host')
    # Ambient exports cannot nominate another session or credential. The fixed
    # environment loader pins the child key from this installation's marker.
    for key in list(os.environ):
        if key.startswith(('COGNEE_', 'MEMLANE_')):
            del os.environ[key]
    from _env_file import load_env_file, parse_env_file
    from _memlane_policy import prepare_environment
    # TraeX may dispatch several operations within one hook process. The
    # upstream loader is cached; always restore the immutable binding first.
    prepare_environment(parse_env_file)
    load_env_file()
    from _plugin_common import set_session_key, ensure_launch_record
    from _memlane_policy import bind
    key = binding['host_kind'] + '-' + hashlib.sha256(
        (binding['instance_id'] + '\0' + request['session_id']).encode()).hexdigest()
    os.environ['COGNEE_SESSION_PREFIX'] = binding['host_kind']
    set_session_key(key)
    dataset = bind(request['cwd'], key)
    session, _ = ensure_launch_record(key, request['cwd'], dataset=dataset)
    from _project_memory import begin, prepare
    begin(dataset, session, request['cwd'])
    prepare(dataset, session)
    from _coding_memory import _SessionState
    with _SessionState(key) as state:
        if state.acquired and 'started_at' not in state.data:
            state.data['started_at'] = time.time()
            state.save()
    return key, session, dataset


def begin_turn(request, key):
    """Capture only source metadata at the prompt boundary, never a transcript."""
    from _memlane_policy import permit
    from _coding_memory import _SessionState, git_root, git_head, git_branch, now_iso
    event = request.get('event_id')
    if not isinstance(event, str) or not event or len(event) > 512:
        raise ValueError('turn_event_id_required')
    with _SessionState(key) as state:
        if not state.acquired:
            raise ValueError('capture_state_locked')
        if not permit('capture'):
            state.data.pop('host_turn_context', None)
            state.save()
            return
        identifier = hashlib.sha256(event.encode()).hexdigest()
        if state.data.get('host_turn_context', {}).get('event_id') == identifier:
            return
        cwd = request['cwd']
        state.data['host_turn_context'] = {'event_id': identifier, 'cwd': git_root(cwd) or cwd,
                                         'session_cwd': cwd, 'start_commit': git_head(cwd),
                                         'branch': git_branch(cwd), 'date': now_iso()[:10]}
        state.save()


def capture(request, key, session, dataset):
    from _capture_policy import capture_enabled, sanitize
    from _memlane_policy import permit
    from _coding_memory import _SessionState, _deliver, _drain, _truncate, trace_entry
    event_id = request.get('event_id')
    if not isinstance(event_id, str) or not event_id or len(event_id) > 512:
        raise ValueError('capture_event_id_required')
    event_id = hashlib.sha256(event_id.encode()).hexdigest()
    with _SessionState(key) as state:
        if not state.acquired:
            raise ValueError('capture_state_locked')
        seen = state.data.setdefault('host_events', {})
        if event_id in seen:
            return {'status': seen[event_id], 'duplicate': True}
        # Store a metadata-only watermark before inspecting any content. An
        # event denied now is never re-read when permission later returns.
        if not capture_enabled() or not permit('capture'):
            seen[event_id] = 'skipped_policy'
            state.save()
            return {'status': 'skipped_policy'}
        entry = request.get('entry')
        if not isinstance(entry, dict):
            raise ValueError('invalid_capture_entry')
        if entry.get('type') == 'qa':
            if set(entry) - {'type', 'question', 'answer', 'time', 'prompt_time', 'context'}:
                raise ValueError('invalid_capture_entry')
            if any(not isinstance(entry.get(field), str) for field in ('question', 'answer')):
                raise ValueError('invalid_capture_entry')
            if not entry['answer'].strip():
                return {'status': 'missing_answer'}
        elif entry.get('type') == 'trace':
            if set(entry) - {'type', 'origin_function', 'method_params', 'method_return_value', 'status', 'error_message', 'time'}:
                raise ValueError('invalid_capture_entry')
            if not isinstance(entry.get('origin_function'), str) or entry.get('status') not in ('success', 'error'):
                raise ValueError('invalid_capture_entry')
        else:
            raise ValueError('invalid_capture_entry')
        clean = sanitize(entry)
        if clean['type'] == 'qa':
            clean['question'] = _truncate(clean['question'], 8000)
            clean['answer'] = _truncate(clean['answer'], 8000)
            from _coding_memory import git_root, git_head, git_branch, now_iso
            context = dict(state.data.get('host_turn_context') or {})
            context.pop('event_id', None)
            cwd = request['cwd']
            if not context:
                context = {'cwd': git_root(cwd) or cwd, 'session_cwd': cwd,
                           'start_commit': '', 'branch': git_branch(cwd), 'date': now_iso()[:10]}
            context['end_commit'] = git_head(cwd)
            clean['context'] = json.dumps(context, ensure_ascii=False)
        else:
            clean = trace_entry({'name': clean['origin_function'],
                                 'input': clean.get('method_params', {}),
                                 'output': clean.get('method_return_value'),
                                 'is_error': clean['status'] == 'error',
                                 'error': clean.get('error_message', ''),
                                 'time': clean.get('time')})
        # Unknown enqueue outcome cannot be repeated implicitly. The official
        # buffer retains the body and receipt; this metadata contains only IDs.
        seen[event_id] = 'enqueue_unknown'
        state.save()
        _deliver(dataset, session, [clean])
        seen[event_id] = 'buffered'
        state.data['host_capture_generation'] = state.data.get('host_capture_generation', 0) + 1
        # Bound metadata by retaining recent IDs; no transcript backfill exists.
        if len(seen) > 20000:
            for old in list(seen)[:len(seen) - 20000]:
                del seen[old]
        state.save()
    _drain(dataset, session)
    return {'status': 'buffered'}


def handle(request):
    if os.environ.get('MEMLANE_PROCESSING_SESSION') == '1':
        return {'status': 'skipped_processing_session'}
    request_fields(request)
    key, session, dataset = setup(request)
    from _memlane_policy import current, require
    operation = request['operation']
    if operation in ('resolve', 'status'):
        if operation == 'resolve' and request.get('event_id'):
            begin_turn(request, key)
        policy = current()
        return {'status': 'ready', 'session_id': session, 'dataset_id': dataset,
                'permissions': policy['policy']['permissions']}
    if operation == 'recall':
        from _coding_memory import deliver
        query = request.get('query', '')
        if not isinstance(query, str) or len(query) > 16000:
            raise ValueError('invalid_recall_query')
        return {'status': 'complete', 'context': deliver(key, query, request['cwd'], force=True)}
    if operation == 'capture':
        return capture(request, key, session, dataset)
    if operation == 'flush':
        from _coding_memory import _drain, _maybe_cooldown_improve, _SessionState
        require('capture', dataset)
        if request.get('final') is True:
            with _SessionState(key) as state:
                if not state.acquired:
                    return {'status': 'flush_result_unconfirmed'}
                generation = state.data.get('host_capture_generation', 0)
                if not generation or state.data.get('host_final_requested') == generation:
                    return {'status': 'no_new_final_material'}
                require('process', dataset, automatic=True)
                # Persist the attempt first. A timeout after submission is
                # unknown; another shutdown event cannot silently resubmit it.
                state.data['host_final_requested'] = generation
                state.save()
            from _plugin_common import run_session_improve_detailed
            outcome = run_session_improve_detailed(dataset, session, trigger='final')
            return {'status': 'processing_submitted' if outcome.get('ok') else 'flush_result_unconfirmed'}
        else:
            _drain(dataset, session)
            _maybe_cooldown_improve(dataset, session, key)
        return {'status': 'flush_requested'}
    if operation == 'remember':
        explicit_access('write', dataset)
        require('write', dataset)
        content = request.get('content')
        if not isinstance(content, str) or not content.strip() or len(content) > 32000:
            raise ValueError('invalid_memory_content')
        from _capture_policy import sanitize
        from _remember_http import do_remember
        result = do_remember(os.environ['COGNEE_BASE_URL'], os.environ['COGNEE_API_KEY'],
                             sanitize(content), dataset, 'memory', dataset_id=dataset)
        if isinstance(result, dict) and result.get('status') in (401, 403):
            return {'status': 'denied', 'http_status': result['status'], 'error': 'official_access_denied'}
        if not isinstance(result, dict) or not result.get('ok'):
            return {'status': 'result_unconfirmed', 'error': 'official_remember_unconfirmed'}
        return {'status': 'submitted'}
    explicit_access('delete', dataset)
    require('delete', dataset)
    data_id = str(uuid.UUID(request.get('data_id', '')))
    # Only one explicit DataID in the bound dataset; never a dataset deletion.
    class NoRedirect(urllib.request.HTTPRedirectHandler):
        def redirect_request(self, *args, **kwargs):
            return None
    payload = json.dumps({'datasetId': dataset, 'dataId': data_id}).encode()
    req = urllib.request.Request(os.environ['COGNEE_BASE_URL'].rstrip('/') + '/api/v1/forget',
                                 data=payload, method='POST', headers={
                                     'X-Api-Key': os.environ['COGNEE_API_KEY'], 'Content-Type': 'application/json'})
    with urllib.request.build_opener(NoRedirect).open(req, timeout=15) as response:
        if not 200 <= response.status < 300:
            raise ValueError('official_forget_unconfirmed')
    return {'status': 'submitted'}


def execute(request):
    try:
        return handle(request)
    except AccessDenied as error:
        return {'status': 'denied', 'http_status': 403, 'error': 'project_' + error.permission + '_denied'}
    except urllib.error.HTTPError as error:
        if error.code in (401, 403):
            return {'status': 'denied', 'http_status': error.code, 'error': 'official_access_denied'}
    except Exception:
        pass
    # No provider body, credentials or exception text leaves the helper.
    return {'status': 'unavailable', 'error': 'memory_operation_unconfirmed'}


def main():
    try:
        raw = sys.stdin.buffer.read(MAX_INPUT + 1)
        if len(raw) > MAX_INPUT:
            raise ValueError('request_limit')
        result = execute(json.loads(raw))
    except Exception:
        # Upstream exception text may contain content, provider bodies or keys.
        result = {'status': 'unavailable', 'error': 'memory_operation_unconfirmed'}
    sys.stdout.write(json.dumps(result, ensure_ascii=False) + '\n')


if __name__ == '__main__':
    main()
