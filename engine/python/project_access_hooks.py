"""Managed official-plugin access and tool-policy hooks.

Installed beside the fixed official scripts as _memlane_access.py. All calls to
_memlane_policy go through the module attribute dynamically so host overrides
and unit-test monkeypatches remain authoritative.
"""
import importlib
import json


def _policy(policy_mod=None):
    return policy_mod if policy_mod is not None else importlib.import_module('_memlane_policy')


def is_managed(*, policy_mod=None):
    return bool(_policy(policy_mod).managed())


def code_dataset_field(*, policy_mod=None):
    return 'datasetId' if _policy(policy_mod).managed() else 'datasetName'


def gate_code_index(load_env_file, dataset, workspace, *, error_fn, policy_mod=None):
    load_env_file()
    policy = _policy(policy_mod)
    if not policy.managed():
        return dataset, None
    try:
        return policy.code_target('write', dataset, workspace=workspace), None
    except policy.NotReady as error:
        return None, error_fn(403, str(error))


def gate_code_do_index(load_env_file, dataset, repo_spec, service_url, api_key, environ, *, error_fn, policy_mod=None):
    load_env_file()
    policy = _policy(policy_mod)
    if not policy.managed():
        return dataset, service_url, api_key, None
    try:
        target = policy.code_target('write', dataset, workspace=repo_spec)
    except policy.NotReady as error:
        return None, service_url, api_key, error_fn(403, str(error))
    return target, environ['COGNEE_BASE_URL'], environ['COGNEE_API_KEY'], None


def gate_code_auto(load_env_file, operation, target=None, *, automatic=False, policy_mod=None):
    load_env_file()
    policy = _policy(policy_mod)
    if not policy.managed():
        return True
    try:
        policy.code_target(operation, target, automatic=automatic)
    except policy.NotReady:
        return False
    return True


def gate_code_lane_dataset(target, *, policy_mod=None):
    policy = _policy(policy_mod)
    if not policy.managed():
        return True
    try:
        policy.code_target('read', target)
    except policy.NotReady:
        return False
    return True


def configure_managed_config(config, environ, *, policy_mod=None):
    policy = _policy(policy_mod)
    if not policy.managed():
        return config
    if not config.get('base_url') or not config.get('api_key'):
        raise RuntimeError('managed_official_connection_required')
    config['plugin_identity'] = 'false'
    config['shared_agent_memory'] = False
    config['managed_endpoint'] = True
    environ['COGNEE_PLUGIN_IDENTITY'] = 'false'
    environ['COGNEE_SHARED_AGENT_MEMORY'] = 'false'
    environ['COGNEE_MANAGED_ENDPOINT'] = 'true'
    environ['COGNEE_SESSION_COMPANION_DATASET'] = 'false'
    return config


def bind_session_start(config, cwd, session_key, hook_log, *, policy_mod=None):
    policy = _policy(policy_mod)
    if not policy.managed():
        return None
    try:
        config['dataset'] = policy.bind(cwd, session_key)
    except policy.NotReady:
        hook_log('managed_project_not_ready', {'code': 'project_binding_unconfirmed'})
        return {'hookSpecificOutput': {'hookEventName': 'SessionStart'}}
    return None


def iter_jsonl_lines(path, start_offset=0):
    with open(path, 'rb') as handle:
        if start_offset:
            handle.seek(start_offset)
            # A stat-only boundary cannot know whether it split a JSONL
            # record. Drop the first suffix, never read back its prefix.
            handle.readline()
        for raw in handle:
            yield raw.decode('utf-8', errors='replace')


def skip_pre_boundary_codex_record(start_offset, units, payload):
    if not start_offset or units:
        return False
    # Ignore a tool/answer whose request began before the allowed
    # boundary, including a partially recorded pre-resume turn.
    item = payload.get('item') or {}
    return payload.get('type') != 'item_completed' or item.get('type') != 'UserMessage'


def resolve_capture_offset(payload, enabled, *, policy_mod=None):
    policy = _policy(policy_mod)
    if not policy.managed():
        return 0
    try:
        return policy.capture_boundary(str(payload.get('transcript_path') or ''), enabled=enabled)
    except (policy.NotReady, OSError, ValueError):
        return None


def stage_chosen_recall(dataset_id, chosen, line_for, *, policy_mod=None):
    policy = _policy(policy_mod)
    if policy.managed():
        policy.stage_recall(dataset_id, [(item['data_id'], line_for(item)) for item in chosen])


def managed_api_key_with_source(environ, *, policy_mod=None):
    policy = _policy(policy_mod)
    if not policy.managed():
        return None
    key = str(environ.get('COGNEE_API_KEY') or '').strip()
    if not key:
        raise policy.NotReady('managed_agent_credential_required')
    return key, 'managed_agent_key'


def managed_drain_precheck(dataset, session_id, load_json_file, bridge_file, bridge_cache_key, *, policy_mod=None):
    policy = _policy(policy_mod)
    if not policy.managed() or policy.permit('drain', dataset):
        return None
    snapshot = (load_json_file(bridge_file(session_id)).get(bridge_cache_key(dataset, session_id)) or {}).get('pending_entries') or []
    return 0, len(snapshot)


def handle_drain_http_error(dataset, session_id, entry, code, *, policy_mod=None):
    policy = _policy(policy_mod)
    policy.block(code)
    if policy.managed() and code in (401, 403):
        policy.stamp_pending_entry(dataset, session_id, entry, False)


def stamp_before_send(dataset, session_id, entry, *, policy_mod=None):
    policy = _policy(policy_mod)
    if policy.managed():
        policy.stamp_pending_entry(dataset, session_id, entry, True)


def record_managed_http_denial(method, path, code, *, policy_mod=None):
    # Permission denial is sticky even if another entry point catches it.
    if method not in ('GET', 'HEAD') and path.split('?')[0] in ('/api/v1/remember/entry', '/api/v1/improve'):
        _policy(policy_mod).block(code)


def prepare_managed_remember(load_env_file, service_url, api_key, target, environ, *, error_fn, policy_mod=None):
    load_env_file()
    policy = _policy(policy_mod)
    if not policy.managed():
        return None, api_key
    if not policy.permit('write', target):
        return error_fn(403, '当前 Agent 没有此项目的写入权限'), api_key
    if service_url.rstrip('/') != environ.get('COGNEE_BASE_URL', '').rstrip('/'):
        return error_fn(403, 'managed_service_mismatch'), api_key
    return None, environ['COGNEE_API_KEY']


def prepare_managed_remember_strict(load_env_file, service_url, api_key, target, environ, *, error_fn, policy_mod=None):
    """Distinguish confirmed local denial from unavailable live policy."""
    load_env_file()
    policy = _policy(policy_mod)
    if not policy.managed():
        return None, api_key
    try:
        decision = policy.current()
    except (policy.NotReady, OSError, ValueError, KeyError, TypeError):
        return error_fn(0, 'project_policy_unconfirmed'), api_key
    if target != decision['dataset_id']:
        return error_fn(403, 'managed_dataset_mismatch'), api_key
    if 'write' not in decision['policy']['permissions']:
        return error_fn(403, '当前 Agent 没有此项目的写入权限'), api_key
    if service_url.rstrip('/') != environ.get('COGNEE_BASE_URL', '').rstrip('/'):
        return error_fn(403, 'managed_service_mismatch'), api_key
    return None, environ['COGNEE_API_KEY']


def prepare_managed_recall(load_env_file, service_url, api_key, dataset, dataset_ids, session_id, scope, environ, *, error_fn, policy_mod=None):
    load_env_file()
    policy = _policy(policy_mod)
    if not policy.managed():
        return None, api_key, dataset, dataset_ids, session_id, scope
    if service_url.rstrip('/') != environ.get('COGNEE_BASE_URL', '').rstrip('/'):
        return error_fn(403, 'managed_service_mismatch'), api_key, dataset, dataset_ids, session_id, scope
    try:
        targets, project_id = policy.recall_targets(dataset, dataset_ids)
    except (policy.NotReady, OSError, ValueError, KeyError, TypeError) as error:
        denied = isinstance(error, policy.NotReady) and str(error) == 'project_read_denied'
        return error_fn(403 if denied else 0, 'project_read_denied' if denied else 'project_policy_unconfirmed'), api_key, dataset, dataset_ids, session_id, scope
    if any(target != project_id for target in targets):
        session_id, scope = '', ['graph']
    return None, environ['COGNEE_API_KEY'], '', targets, session_id, scope


def report_managed_explicit_recall(data, *, policy_mod=None):
    policy = _policy(policy_mod)
    if policy.managed():
        policy.report_explicit_recall(data)


def check_forget_access(command, target, *, policy_mod=None):
    policy = _policy(policy_mod)
    if command in ('data', 'raw'):
        policy.recall_targets(target, [target])
    elif command == 'forget':
        policy.require('delete', target)


def check_forget_tool_policy(command, target, *, policy_mod=None):
    """Return a local policy JSON string when blocked/unconfirmed, or None if allowed."""
    if command not in ('data', 'raw', 'forget'):
        return None
    policy = _policy(policy_mod)
    try:
        decision = policy.current()
        common = decision.get('common') or {}
        if target == decision['dataset_id']:
            permissions = decision['policy']['permissions']
        elif target == common.get('dataset_id'):
            if not common.get('ready'):
                raise policy.NotReady('common_policy_unconfirmed')
            permissions = ['read'] if common.get('readable') else []
        else:
            permissions = []
    except (policy.NotReady, OSError, ValueError, KeyError, TypeError):
        return json.dumps({'error': 'project_policy_unconfirmed', 'status': 0, 'memlane_local_result': True})
    permission = 'delete' if command == 'forget' else 'read'
    if permission not in permissions:
        return json.dumps({'error': 'project_' + permission + '_denied', 'status': 403,
                           'local_denial': True, 'memlane_local_result': True})
    return None
