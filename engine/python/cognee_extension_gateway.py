"""Memlane Cognee HTTP Extension Gateway mounted at ``/cognee``.

Runs strictly outside upstream Cognee source files: authenticates Agents via
``X-Api-Key``, enforces Memlane project/common ACLs plus capture/process gates,
transparently merges readable common knowledge on search/recall, enriches
Codex ``Edit`` unified diffs, and records explicit recall hits.
"""
import asyncio
import email.parser
import email.policy
import hmac
import inspect
import json
import logging
import re
import uuid
from urllib.parse import parse_qs, quote

import aiohttp
from aiohttp import ClientSession, ClientTimeout, web

from _runtime_common import CachedSecretReader
from external_capture_store import ensure_qa_context_date
from performance_trace import HEADER as TRACE_HEADER, RequestTrace
from project_control import loopback_origin
from project_policy import PolicyError
from session_drift import build_project_alias_index


async def _record_recall(store, stage, args, channel, hit_count):
    try:
        async_write = getattr(store, stage + '_async', None)
        if async_write is not None:
            await async_write(*args)
        elif hasattr(store, stage):
            await asyncio.to_thread(getattr(store, stage), *args)
    except Exception as exc:
        warning = {'event': 'recall_record_failed', 'component': 'cognee_gateway',
                   'stage': stage, 'channel': channel, 'hit_count': hit_count,
                   'error_type': type(exc).__name__}
        if isinstance(exc, PolicyError):
            warning['error_code'] = exc.code
        logging.getLogger(__name__).warning('%s', json.dumps(warning))

MAX_GATEWAY_BODY_BYTES = 16 * 1024 * 1024
GATEWAY_TIMEOUT_SECONDS = 600.0
GATEWAY_QUERY_TIMEOUT_SECONDS = 15.0
_HOP_HEADERS = frozenset({
    'host', 'connection', 'keep-alive', 'proxy-authenticate', 'proxy-authorization',
    'te', 'trailer', 'transfer-encoding', 'upgrade', 'content-length', 'origin',
    'authorization', 'x-memlane-management',
})
_DATASET_ID_RE = re.compile(
    r'^/api/v1/datasets/(?P<dataset_id>[^/]+)/(?P<tail>data/count|data/[^/]+/raw|data/[^/]+|data|graph|processing-status)$'
)
_SESSION_ID_RE = re.compile(
    r'^/api/v1/sessions/(?P<session_id>[^/]+)(?:/(?P<subroute>capture-status|history))?$'
)
_RESERVED_SESSION_SEGMENTS = frozenset({
    'stats', 'cost-by-model', 'with-agent-info', 'cost-by-user-agent',
})
_CAPTURE_ENTRY_RE = re.compile(r'^/api/v1/capture/entries/(?P<message>[^/]+)$')
_UNAUTH_READ_PATHS = frozenset({'/health', '/api/v1/health'})
_SIMPLE_READ_PATHS = frozenset({
    '/openapi.json', '/health', '/api/v1/health', '/api/v1/users/me',
    '/api/v1/datasets', '/api/v1/datasets/status', '/api/v1/datasets/status/progress',
    '/api/v1/sessions', '/api/v1/activity/pipeline-runs',
    '/api/v1/agents/connections/me',
})


def enrich_codex_file_change_entry(entry):
    """Mirror Codex ``Edit`` ``method_params.diff`` into ``method_return_value.unified_diff``."""
    if not isinstance(entry, dict):
        return entry
    if entry.get('type') != 'trace' or entry.get('origin_function') != 'Edit':
        return entry
    params = entry.get('method_params')
    if not isinstance(params, dict) or params.get('change') != 'update':
        return entry
    diff = params.get('diff')
    if not isinstance(diff, str) or not diff:
        return entry
    ret = entry.get('method_return_value')
    if isinstance(ret, dict):
        if 'unified_diff' in ret:
            return entry
        updated_ret = {**ret, 'unified_diff': diff}
    else:
        updated_ret = {'status': ret if ret is not None else 'completed', 'unified_diff': diff}
    return {**entry, 'method_return_value': updated_ret}


def _uuid_str(value):
    if not isinstance(value, str):
        return None
    try:
        return str(uuid.UUID(value.strip()))
    except (ValueError, AttributeError):
        return None


def _safe_path_segment(value, *, max_len=256):
    if not isinstance(value, str) or not 1 <= len(value) <= max_len:
        return False
    if '/' in value or '\\' in value or '..' in value:
        return False
    return not any(ord(ch) < 32 or ord(ch) == 127 for ch in value)


def _strip_mount_prefix(path, prefix):
    if prefix and path == prefix:
        return '/'
    if prefix and path.startswith(prefix + '/'):
        path = path[len(prefix):]
    if len(path) > 1 and path.endswith('/') and path.rstrip('/') in {
        '/api/v1/datasets', '/api/v1/datasets/status', '/api/v1/datasets/status/progress',
        '/api/v1/sessions', '/api/v1/activity/pipeline-runs', '/api/v1/agents/connections/me',
        '/api/v1/agents/register', '/api/v1/agents/unregister',
        '/api/v1/recall', '/api/v1/search', '/api/v1/remember',
        '/api/v1/improve', '/api/v1/cognify', '/api/v1/memify', '/api/v1/forget',
    }:
        return path.rstrip('/')
    return path


def _gateway_route_label(method, upstream_path):
    """Map gateway request method+path to a fixed, ID-free allowlisted template."""
    exact = f'{method} {upstream_path}'
    if upstream_path in _SIMPLE_READ_PATHS and method == 'GET':
        return exact
    if upstream_path in (
        '/api/v1/recall', '/api/v1/search', '/api/v1/remember/entry', '/api/v1/remember',
        '/api/v1/improve', '/api/v1/cognify', '/api/v1/memify', '/api/v1/forget',
        '/api/v1/agents/register', '/api/v1/agents/unregister',
    ) and method == 'POST':
        return exact
    m = _DATASET_ID_RE.match(upstream_path)
    if m and method in ('GET', 'DELETE'):
        tail = m.group('tail')
        if tail == 'data':
            return f'{method} /api/v1/datasets/{{id}}/data'
        if tail == 'data/count':
            return f'{method} /api/v1/datasets/{{id}}/data/count'
        if tail == 'graph':
            return f'{method} /api/v1/datasets/{{id}}/graph'
        if tail == 'processing-status':
            return f'{method} /api/v1/datasets/{{id}}/processing-status'
        if tail.startswith('data/') and tail.endswith('/raw'):
            return f'{method} /api/v1/datasets/{{id}}/data/{{id}}/raw'
        if tail.startswith('data/'):
            return f'{method} /api/v1/datasets/{{id}}/data/{{id}}'
    m = _SESSION_ID_RE.match(upstream_path)
    if m and method == 'GET':
        subroute = m.group('subroute')
        return f'GET /api/v1/sessions/{{id}}/{subroute}' if subroute else 'GET /api/v1/sessions/{id}'
    if _CAPTURE_ENTRY_RE.match(upstream_path) and method in ('GET', 'PUT'):
        return f'{method} /api/v1/capture/entries/{{id}}'
    return 'other'


def _extract_recall_hits(payload, allowed_read_ids):
    """Extract unique official (dataset_id, document_id) UUID pairs without reading answer text."""
    hits = set()

    def visit(node, current_dataset=None):
        if len(hits) >= 100:
            return
        if isinstance(node, list):
            for item in node:
                visit(item, current_dataset)
            return
        if not isinstance(node, dict):
            return
        if node.get('error'):
            return
        ds = _uuid_str(node.get('dataset_id')) or current_dataset
        doc = _uuid_str(node.get('document_id')) or _uuid_str(node.get('doc_id'))
        if ds and doc and ds in allowed_read_ids:
            hits.add((ds, doc))
        for key in ('search_result', 'results', 'context', 'metadata', 'raw', 'sources'):
            nested = node.get(key)
            if isinstance(nested, (list, dict)):
                visit(nested, ds)

    visit(payload)
    return [{'dataset_id': d, 'document_id': doc} for d, doc in sorted(hits)[:100]]


class _AgentPolicyContext:
    def __init__(self, agent_id, api_key, entries, common_info):
        self.agent_id = agent_id
        self.api_key = api_key
        self.by_id = {}
        self.by_name = {}
        self.common_id = common_info['dataset_id'] if common_info else None
        self.common_readable = bool(common_info and common_info['ready'] and 'read' in common_info['permissions'])
        self.readable_ids = set()
        self.readable_project_ids = []
        self.readable_code_ids = []
        for entry in entries:
            ds_id = entry.get('dataset_id')
            if not ds_id:
                continue
            self.by_id[ds_id] = entry
            if entry.get('dataset_name'):
                self.by_name[entry['dataset_name']] = entry
            if entry['ready'] and 'read' in entry['permissions']:
                self.readable_ids.add(ds_id)
                if entry['kind'] == 'project':
                    self.readable_project_ids.append(ds_id)
                elif entry['kind'] == 'code':
                    self.readable_code_ids.append(ds_id)


async def _authenticate_and_snapshot(registry, secret_reader, presented_key):
    if not presented_key:
        raise PolicyError('api_key_required', 401)
    async with registry.mutex:
        agents_copy = [(aid, dict(info)) for aid, info in registry.state.get('agents', {}).items()]
        retirements = set(registry.state.get('agent_retirements', {}).keys())
        archived = dict(registry.state.get('archived_agents', {}))

    matched_agent = None
    matched_retired = False
    for aid, info in agents_copy:
        key_file = info.get('key_file')
        if not key_file:
            continue
        try:
            secret = secret_reader.read(key_file)
        except (PolicyError, OSError):
            continue
        if hmac.compare_digest(presented_key, secret):
            if aid in retirements:
                matched_retired = True
                break
            matched_agent = aid
            break
    if matched_retired:
        raise PolicyError('agent_retired', 403)
    if matched_agent is None:
        for info in archived.values():
            key_file = info.get('key_file')
            if not key_file:
                continue
            try:
                secret = secret_reader.read(key_file)
            except (PolicyError, OSError):
                continue
            if hmac.compare_digest(presented_key, secret):
                raise PolicyError('agent_retired', 403)
        raise PolicyError('invalid_api_key', 401)

    async with registry.mutex:
        if matched_agent not in registry.state.get('agents', {}):
            raise PolicyError('agent_not_registered', 403)
        if matched_agent in registry.state.get('agent_retirements', {}):
            raise PolicyError('agent_retired', 403)
        entries = []
        for project in registry.state.get('projects', {}).values():
            view = registry.policy(project, matched_agent)
            perms = set(view['policy']['permissions'])
            if project.get('dataset_id'):
                entries.append({
                    'dataset_id': project['dataset_id'],
                    'dataset_name': project.get('dataset_name'),
                    'kind': 'project',
                    'ready': bool(view['ready']),
                    'permissions': perms,
                    'can_capture': bool(view['policy']['can_capture']),
                    'can_auto_process': bool(view['policy']['can_auto_process']),
                    'code_enabled': bool(view['code_enabled']),
                })
            if project.get('code_dataset_id'):
                entries.append({
                    'dataset_id': project['code_dataset_id'],
                    'dataset_name': None,
                    'kind': 'code',
                    'ready': bool(view['code']['ready']),
                    'permissions': perms if project.get('code_enabled') else set(),
                    'can_capture': False,
                    'can_auto_process': bool(view['policy']['can_auto_process']),
                    'code_enabled': bool(view['code_enabled']),
                })
        common = registry.state.get('common')
        common_info = None
        if common and common.get('dataset_id'):
            cview = registry.common_policy(matched_agent)
            common_info = {
                'dataset_id': common['dataset_id'],
                'dataset_name': common.get('dataset_name'),
                'kind': 'common',
                'ready': bool(cview.get('ready')),
                'permissions': {'read'} if (cview.get('ready') and cview.get('readable')) else set(),
                'can_capture': False,
                'can_auto_process': False,
                'code_enabled': False,
            }
            entries.append(common_info)
        return _AgentPolicyContext(matched_agent, presented_key, entries, common_info)


def _resolve_target(ctx, ref):
    uid = _uuid_str(ref)
    if uid is not None:
        info = ctx.by_id.get(uid)
        if info is None:
            raise PolicyError('dataset_access_denied', 403)
        return info
    if isinstance(ref, str) and ref.strip():
        info = ctx.by_name.get(ref.strip())
        if info is None:
            raise PolicyError('dataset_access_denied', 403)
        return info
    raise PolicyError('dataset_access_denied', 403)


def _authorize_target(info, operation):
    if not info['ready']:
        raise PolicyError('dataset_not_ready', 403)
    if operation == 'read':
        if 'read' not in info['permissions']:
            raise PolicyError('dataset_read_denied', 403)
        return info
    if info['kind'] == 'common':
        raise PolicyError('common_dataset_read_only', 403)
    if operation == 'delete':
        if 'delete' not in info['permissions']:
            raise PolicyError('dataset_delete_denied', 403)
        return info
    if operation == 'code_write':
        if not info['code_enabled'] or 'write' not in info['permissions']:
            raise PolicyError('code_indexing_denied', 403)
        return info
    if operation == 'capture':
        if info['kind'] == 'code':
            raise PolicyError('code_dataset_capture_denied', 403)
        if 'write' not in info['permissions']:
            raise PolicyError('dataset_write_denied', 403)
        if not info['can_capture']:
            raise PolicyError('capture_paused', 403)
        return info
    if operation == 'process':
        if 'write' not in info['permissions']:
            raise PolicyError('dataset_write_denied', 403)
        if not info['can_auto_process']:
            raise PolicyError('auto_process_paused', 403)
        return info
    raise PolicyError('dataset_access_denied', 403)


def _parse_remember_fields(content_type, raw_bytes):
    ctype = (content_type or '').split(';', 1)[0].strip().lower()
    if ctype == 'application/json':
        data = json.loads(raw_bytes)
        if not isinstance(data, dict):
            raise PolicyError('invalid_request', 400)
        return {k: v for k, v in data.items() if k in ('datasetId', 'dataset_id', 'datasetName', 'dataset_name', 'content_type')}
    if ctype == 'application/x-www-form-urlencoded':
        parsed = parse_qs(raw_bytes.decode('utf-8'), keep_blank_values=False)
        return {k: vals[-1] for k, vals in parsed.items() if vals}
    if ctype == 'multipart/form-data':
        header = f'Content-Type: {content_type}\r\nMIME-Version: 1.0\r\n\r\n'.encode('latin-1')
        msg = email.parser.BytesParser(policy=email.policy.default).parsebytes(header + raw_bytes)
        fields = {}
        for part in msg.iter_parts():
            name = part.get_param('name', header='content-disposition')
            if name in ('datasetId', 'dataset_id', 'datasetName', 'dataset_name', 'content_type'):
                raw_val = part.get_payload(decode=True)
                val = raw_val.decode('utf-8', errors='replace') if isinstance(raw_val, bytes) else str(raw_val or '')
                if val.strip():
                    fields[name] = val.strip()
        return fields
    raise PolicyError('unsupported_media_type', 415)


def _append_multipart_field(content_type, raw_bytes, name, value):
    header = f'Content-Type: {content_type}\r\nMIME-Version: 1.0\r\n\r\n'.encode('latin-1')
    msg = email.parser.BytesParser(policy=email.policy.default).parsebytes(header + raw_bytes)
    boundary = msg.get_boundary()
    if not boundary:
        return raw_bytes
    marker = f'--{boundary}--'.encode('latin-1')
    idx = raw_bytes.rfind(marker)
    if idx < 0:
        return raw_bytes
    addition = (
        f'--{boundary}\r\nContent-Disposition: form-data; name="{name}"\r\n\r\n{value}\r\n'.encode('utf-8')
    )
    prefix = raw_bytes[:idx]
    if prefix and not prefix.endswith(b'\r\n'):
        addition = b'\r\n' + addition
    return prefix + addition + raw_bytes[idx:]


async def _read_limited_body(request):
    buf = bytearray()
    async for chunk in request.content.iter_chunked(64 * 1024):
        buf.extend(chunk)
        if len(buf) > MAX_GATEWAY_BODY_BYTES:
            raise PolicyError('payload_too_large', 413)
    return bytes(buf)


def _read_json_bytes(raw_bytes):
    try:
        payload = json.loads(raw_bytes)
    except ValueError:
        raise PolicyError('invalid_json', 400) from None
    if not isinstance(payload, dict):
        raise PolicyError('invalid_json', 400)
    return payload


def _rewrite_query_datasets(ctx, payload):
    raw_ids = payload.get('dataset_ids') if 'dataset_ids' in payload else payload.get('datasetIds')
    raw_names = payload.get('datasets')
    search_type = str(payload.get('search_type') or payload.get('searchType') or '').upper()
    scope_val = payload.get('scope')
    scope_set = ({scope_val.lower()} if isinstance(scope_val, str)
                 else ({str(s).lower() for s in scope_val} if isinstance(scope_val, list) else set()))
    target_ids = []
    if isinstance(raw_ids, list) and raw_ids:
        for item in raw_ids:
            uid = _uuid_str(str(item)) if item is not None else None
            if uid is None:
                raise PolicyError('dataset_access_denied', 403)
            info = _authorize_target(_resolve_target(ctx, uid), 'read')
            if info['dataset_id'] not in target_ids:
                target_ids.append(info['dataset_id'])
    elif isinstance(raw_names, list) and raw_names:
        for name in raw_names:
            info = _authorize_target(_resolve_target(ctx, name), 'read')
            if info['dataset_id'] not in target_ids:
                target_ids.append(info['dataset_id'])
    else:
        target_ids = list(ctx.readable_project_ids)
        if (search_type == 'CODE' or 'code' in scope_set or 'all' in scope_set):
            for cid in ctx.readable_code_ids:
                if cid not in target_ids:
                    target_ids.append(cid)
        if ctx.common_readable and ctx.common_id and ctx.common_id not in target_ids:
            target_ids.append(ctx.common_id)
        if not target_ids:
            raise PolicyError('dataset_read_denied', 403)

    is_code_only = (
        search_type == 'CODE'
        or (bool(scope_set) and scope_set <= {'code'})
        or (bool(target_ids) and all(ctx.by_id[d]['kind'] == 'code' for d in target_ids))
    )
    if (ctx.common_readable and ctx.common_id and not is_code_only
            and ctx.common_id not in target_ids):
        target_ids.append(ctx.common_id)
    updated = dict(payload)
    updated.pop('datasetIds', None)
    updated.pop('datasets', None)
    updated['dataset_ids'] = target_ids
    return updated


def _filter_datasets_list(ctx, data):
    if not isinstance(data, list):
        raise PolicyError('official_result_unknown', 503)
    return [row for row in data if isinstance(row, dict) and _uuid_str(row.get('id')) in ctx.readable_ids]


def _filter_datasets_status(ctx, data):
    if not isinstance(data, dict):
        raise PolicyError('official_result_unknown', 503)
    return {key: val for key, val in data.items() if _uuid_str(key) in ctx.readable_ids}


def _filter_sessions_list(ctx, data):
    if isinstance(data, dict) and isinstance(data.get('sessions'), list):
        orig = data['sessions']
        filtered = [
            row for row in orig
            if isinstance(row, dict) and _uuid_str(row.get('dataset_id')) in ctx.readable_ids
        ]
        removed = len(orig) - len(filtered)
        out = {**data, 'sessions': filtered}
        if isinstance(out.get('total'), int):
            if not out.get('has_more') and out.get('offset', 0) == 0:
                out['total'] = len(filtered)
            else:
                out['total'] = max(len(filtered), out['total'] - removed)
        return out
    if isinstance(data, list):
        return [
            row for row in data
            if isinstance(row, dict) and _uuid_str(row.get('dataset_id')) in ctx.readable_ids
        ]
    raise PolicyError('official_result_unknown', 503)


def _filter_pipeline_runs(ctx, data):
    if not isinstance(data, list):
        raise PolicyError('official_result_unknown', 503)
    return [
        row for row in data
        if isinstance(row, dict) and _uuid_str(row.get('dataset_id')) in ctx.readable_ids
    ]


def _filter_session_detail(ctx, data):
    if not isinstance(data, dict):
        raise PolicyError('dataset_read_denied', 403)
    ds_id = _uuid_str(data.get('dataset_id'))
    if not ds_id or ds_id not in ctx.readable_ids:
        raise PolicyError('dataset_read_denied', 403)
    return data


def _agent_dataset_ref_id(ctx, item):
    if isinstance(item, str):
        return _uuid_str(item)
    if isinstance(item, dict):
        uid = _uuid_str(item.get('id')) or _uuid_str(item.get('dataset_id'))
        if uid:
            return uid
        name = item.get('name')
        if isinstance(name, str) and name.strip():
            info = ctx.by_name.get(name.strip())
            if info:
                return info['dataset_id']
    return None


def _norm_user_id(value):
    if value is None:
        return None
    uid = _uuid_str(value)
    if uid:
        return uid
    text = str(value).strip()
    return text or None


async def _filter_agent_connection_me(ctx, data, *, lookup_session_dataset=None):
    if not isinstance(data, dict):
        raise PolicyError('official_result_unknown', 503)
    out = dict(data)
    if isinstance(out.get('memory_sources'), list):
        out['memory_sources'] = [
            src for src in out['memory_sources']
            if isinstance(src, dict)
            and (_uuid_str(src.get('dataset_id')) or _uuid_str(src.get('id'))) in ctx.readable_ids
        ]

    orig_agent_refs = []
    agent_session_id = ''
    agent_user_id = None
    if isinstance(out.get('agent'), dict):
        agent_obj = dict(out['agent'])
        raw_agent_sid = agent_obj.get('session_id')
        if isinstance(raw_agent_sid, str) and raw_agent_sid.strip():
            agent_session_id = raw_agent_sid.strip()
        agent_user_id = _norm_user_id(agent_obj.get('user_id'))
        for key in ('dataset_ids', 'datasets'):
            if isinstance(agent_obj.get(key), list):
                orig_agent_refs.extend(agent_obj[key])
                agent_obj[key] = [
                    item for item in agent_obj[key]
                    if _agent_dataset_ref_id(ctx, item) in ctx.readable_ids
                ]
        out['agent'] = agent_obj

    agent_all_datasets_readable = (
        bool(orig_agent_refs)
        and all(_agent_dataset_ref_id(ctx, item) in ctx.readable_ids for item in orig_agent_refs)
    )
    has_session_content = bool(out.get('recent_qas')) or bool(out.get('recent_traces'))

    if isinstance(out.get('recent_sessions'), list):
        orig_sess = out['recent_sessions']
        filtered_sess = []
        session_lookup_cache = {}
        for row in orig_sess:
            if not isinstance(row, dict):
                continue
            explicit_ds = row.get('dataset_id')
            if explicit_ds is not None:
                if _uuid_str(explicit_ds) in ctx.readable_ids:
                    filtered_sess.append(row)
                continue
            sid = str(row.get('session_id') or '').strip()
            if not sid or not _safe_path_segment(sid) or sid in _RESERVED_SESSION_SEGMENTS:
                continue
            expected_uid = _norm_user_id(row.get('user_id')) or agent_user_id
            status, resolved_ds, resolved_uid = 'error', None, None
            if lookup_session_dataset is not None:
                if sid not in session_lookup_cache:
                    session_lookup_cache[sid] = await lookup_session_dataset(sid)
                status, resolved_ds, resolved_uid = session_lookup_cache[sid]
            if status == 'found':
                if (
                    resolved_ds in ctx.readable_ids
                    and expected_uid is not None
                    and resolved_uid is not None
                    and resolved_uid == expected_uid
                ):
                    filtered_sess.append(row)
            elif (
                status == 'not_found'
                and agent_all_datasets_readable
                and not has_session_content
                and (not agent_session_id or sid == agent_session_id)
            ):
                filtered_sess.append(row)
        out['recent_sessions'] = filtered_sess
        if not filtered_sess or len(filtered_sess) != len(orig_sess):
            if isinstance(out.get('recent_traces'), list):
                out['recent_traces'] = []
            if isinstance(out.get('recent_qas'), list):
                out['recent_qas'] = []
    else:
        if isinstance(out.get('recent_traces'), list):
            out['recent_traces'] = []
        if isinstance(out.get('recent_qas'), list):
            out['recent_qas'] = []
    return out


def _trace_message_id(entry_obj):
    if not isinstance(entry_obj, dict):
        return None
    params = entry_obj.get('method_params')
    if isinstance(params, dict):
        cap_id = params.get('cognee_capture_id')
        if isinstance(cap_id, str) and cap_id.strip():
            return cap_id.strip()
    trace_id = entry_obj.get('trace_id')
    if isinstance(trace_id, str) and trace_id.strip():
        return trace_id.strip()
    return None


def create_gateway_app(registry, *, recall_store=None, drift_store=None, official_url=None, session=None, prefix='/cognee'):
    """Create the Cognee HTTP Extension Gateway ``aiohttp.web.Application``."""
    secret_cache = CachedSecretReader()
    owned_session = None

    async def _observe_trace_drift(bound_dataset_id, session_id, entry_obj, *, message_id=None):
        if (drift_store is None or not isinstance(entry_obj, dict)
                or entry_obj.get('type') != 'trace'
                or not isinstance(session_id, str) or not session_id):
            return
        try:
            async with registry.mutex:
                alias_index = build_project_alias_index(registry.state.get('projects', {}))
            drift_store.observe_trace(bound_dataset_id, session_id, entry_obj, alias_index, message_id=message_id)
        except Exception:
            pass

    def resolve_upstream():
        url = official_url or getattr(getattr(registry, 'official', None), 'url', None)
        if not url:
            raise PolicyError('official_service_unavailable', 503)
        return loopback_origin(url)

    async def get_session():
        nonlocal owned_session
        if session is not None:
            return session
        existing = getattr(getattr(registry, 'official', None), 'session', None)
        if existing is not None and not existing.closed:
            return existing
        if owned_session is None or owned_session.closed:
            owned_session = ClientSession()
        return owned_session

    async def _lookup_session_dataset(ctx, sid):
        if not sid or not _safe_path_segment(sid) or sid in _RESERVED_SESSION_SEGMENTS:
            return 'error', None, None
        try:
            upstream = resolve_upstream()
            client = await get_session()
            url = f'{upstream}/api/v1/sessions/{quote(sid, safe="")}'
            timeout = ClientTimeout(total=min(5.0, GATEWAY_QUERY_TIMEOUT_SECONDS), sock_connect=5.0)
            async with client.get(
                url,
                headers={'X-Api-Key': ctx.api_key},
                timeout=timeout,
                allow_redirects=False,
            ) as resp:
                if resp.status == 404:
                    return 'not_found', None, None
                if resp.status != 200:
                    return 'error', None, None
                ctype = (resp.headers.get('Content-Type') or '').split(';', 1)[0].strip().lower()
                if ctype != 'application/json':
                    return 'error', None, None
                raw = bytearray()
                async for chunk in resp.content.iter_chunked(64 * 1024):
                    raw.extend(chunk)
                    if len(raw) > MAX_GATEWAY_BODY_BYTES:
                        return 'error', None, None
                parsed = json.loads(bytes(raw))
                if not isinstance(parsed, dict):
                    return 'error', None, None
                return 'found', _uuid_str(parsed.get('dataset_id')), _norm_user_id(parsed.get('user_id'))
        except Exception:
            return 'error', None, None

    async def proxy(request, ctx, upstream_path, *, body_bytes=b'', json_body=None,
                    dataset_filter_fn=None, record_hits=False, recall_channel='explicit',
                    timeout_seconds=None):
        upstream = resolve_upstream()
        client = await get_session()
        query = request.rel_url.query_string
        url = f'{upstream}{upstream_path}' + (f'?{query}' if query else '')
        headers = {}
        for k, v in request.headers.items():
            if k.lower() in _HOP_HEADERS or k.lower() == 'x-api-key':
                continue
            headers[k] = v
        if ctx is not None:
            headers['X-Api-Key'] = ctx.api_key
        if json_body is not None:
            headers['Content-Type'] = 'application/json'
            send_kwargs = {'json': json_body}
        elif request.method in ('POST', 'PUT', 'PATCH'):
            send_kwargs = {'data': body_bytes}
        else:
            send_kwargs = {}

        effective_timeout = GATEWAY_TIMEOUT_SECONDS if timeout_seconds is None else float(timeout_seconds)
        timeout = ClientTimeout(total=effective_timeout, sock_connect=min(10.0, effective_timeout))
        route_label = _gateway_route_label(request.method.upper(), upstream_path)
        trace = RequestTrace('cognee_gateway', request.headers.get(TRACE_HEADER))
        trace.transition('upstream_request')
        try:
            async with client.request(
                request.method, url, headers=headers, timeout=timeout, allow_redirects=False, **send_kwargs
            ) as upstream_resp:
                ctype = (upstream_resp.headers.get('Content-Type') or '').split(';', 1)[0].strip().lower()
                if ctype == 'text/event-stream' and dataset_filter_fn is None:
                    stream = web.StreamResponse(status=upstream_resp.status, headers={'Content-Type': 'text/event-stream'})
                    await stream.prepare(request)
                    stream_outcome = 'succeeded' if upstream_resp.status < 400 else 'upstream_error'
                    try:
                        async for chunk in upstream_resp.content.iter_chunked(16 * 1024):
                            await stream.write(chunk)
                        await stream.write_eof()
                    except Exception as exc:
                        stream_outcome = type(exc).__name__
                        raise
                    finally:
                        trace.finish(stream_outcome, route=route_label, status=upstream_resp.status)
                    return stream
                raw_resp = bytearray()
                async for chunk in upstream_resp.content.iter_chunked(64 * 1024):
                    raw_resp.extend(chunk)
                    if len(raw_resp) > MAX_GATEWAY_BODY_BYTES:
                        trace.finish('response_limit', route=route_label, status=503)
                        raise PolicyError('official_response_limit', 503)
                resp_bytes = bytes(raw_resp)
                resp_headers = {}
                if upstream_resp.headers.get('Content-Type'):
                    resp_headers['Content-Type'] = upstream_resp.headers['Content-Type']
                extra_trace = {}
                if upstream_resp.status == 200 and (dataset_filter_fn or record_hits):
                    if ctype != 'application/json':
                        if dataset_filter_fn is not None:
                            trace.finish('upstream_error', route=route_label, status=503)
                            raise PolicyError('official_result_unknown', 503)
                        parsed = None
                    else:
                        try:
                            parsed = json.loads(resp_bytes)
                        except ValueError:
                            if dataset_filter_fn is not None:
                                trace.finish('upstream_error', route=route_label, status=503)
                                raise PolicyError('official_result_unknown', 503) from None
                            parsed = None
                    if parsed is not None:
                        if dataset_filter_fn is not None:
                            try:
                                parsed = dataset_filter_fn(parsed)
                                if inspect.isawaitable(parsed):
                                    parsed = await parsed
                            except PolicyError as err:
                                trace.finish('denied' if err.status == 403 else 'upstream_error', route=route_label, status=err.status)
                                raise
                            resp_bytes = json.dumps(parsed, ensure_ascii=False).encode('utf-8')
                            resp_headers['Content-Type'] = 'application/json'
                        if record_hits and ctx is not None:
                            hits = _extract_recall_hits(parsed, ctx.readable_ids)
                            extra_trace['hit_count'] = len(hits)
                            if recall_store is not None:
                                await _record_recall(recall_store, 'record_query', (recall_channel, len(hits)),
                                                     recall_channel, len(hits))
                                if hits:
                                    event = {'event_id': str(uuid.uuid4()), 'channel': recall_channel, 'hits': hits}
                                    await _record_recall(recall_store, 'record', (ctx.agent_id, event),
                                                         recall_channel, len(hits))
                outcome = 'succeeded' if upstream_resp.status < 400 else 'upstream_error'
                trace.finish(outcome, route=route_label, status=upstream_resp.status, output_bytes=len(resp_bytes), **extra_trace)
                return web.Response(status=upstream_resp.status, body=resp_bytes, headers=resp_headers)
        except asyncio.TimeoutError:
            trace.finish('timeout', route=route_label, status=503)
            raise PolicyError('official_result_unknown', 503) from None
        except aiohttp.ClientError as exc:
            trace.finish(type(exc).__name__, route=route_label, status=503)
            raise PolicyError('official_result_unknown', 503) from None

    async def handle(request):
        try:
            if request.headers.get('Origin'):
                raise PolicyError('browser_origin_rejected', 403)
            raw_path = request.raw_path.split('?', 1)[0]
            if '..' in raw_path or '\\' in raw_path or '%2e%2e' in raw_path.lower() or '%2f' in raw_path.lower():
                raise PolicyError('gateway_route_not_allowed', 403)
            rel_path = _strip_mount_prefix(request.path, prefix)
            if '..' in rel_path or '\\' in rel_path:
                raise PolicyError('gateway_route_not_allowed', 403)

            method = request.method.upper()
            presented_key = request.headers.get('X-Api-Key', '').strip()
            if method == 'GET' and rel_path in _UNAUTH_READ_PATHS:
                ctx = await _authenticate_and_snapshot(registry, secret_cache, presented_key) if presented_key else None
                return await proxy(request, ctx, rel_path)

            ctx = await _authenticate_and_snapshot(registry, secret_cache, presented_key)

            if method == 'GET' and rel_path in _SIMPLE_READ_PATHS:
                filt = None
                if rel_path == '/api/v1/datasets':
                    filt = lambda rows: _filter_datasets_list(ctx, rows)
                elif rel_path in ('/api/v1/datasets/status', '/api/v1/datasets/status/progress'):
                    for raw_ds in request.query.getall('dataset', []):
                        _authorize_target(_resolve_target(ctx, raw_ds), 'read')
                    filt = lambda data: _filter_datasets_status(ctx, data)
                elif rel_path == '/api/v1/sessions':
                    filt = lambda data: _filter_sessions_list(ctx, data)
                elif rel_path == '/api/v1/activity/pipeline-runs':
                    raw_ds = request.query.get('dataset_id', '').strip()
                    if raw_ds:
                        _authorize_target(_resolve_target(ctx, raw_ds), 'read')
                    filt = lambda rows: _filter_pipeline_runs(ctx, rows)
                elif rel_path == '/api/v1/agents/connections/me':
                    filt = lambda data: _filter_agent_connection_me(
                        ctx,
                        data,
                        lookup_session_dataset=lambda sid: _lookup_session_dataset(ctx, sid),
                    )
                return await proxy(request, ctx, rel_path, dataset_filter_fn=filt)

            m = _DATASET_ID_RE.match(rel_path)
            if m and method in ('GET', 'DELETE'):
                ds_id = _uuid_str(m.group('dataset_id'))
                if not ds_id:
                    raise PolicyError('dataset_access_denied', 403)
                tail = m.group('tail')
                for part in tail.split('/'):
                    if not _safe_path_segment(part):
                        raise PolicyError('gateway_route_not_allowed', 403)
                if method == 'GET':
                    _authorize_target(_resolve_target(ctx, ds_id), 'read')
                    return await proxy(request, ctx, f'/api/v1/datasets/{ds_id}/{tail}')
                if method == 'DELETE' and tail.startswith('data/') and tail != 'data/count' and not tail.endswith('/raw'):
                    _authorize_target(_resolve_target(ctx, ds_id), 'delete')
                    return await proxy(request, ctx, f'/api/v1/datasets/{ds_id}/{tail}')
                raise PolicyError('gateway_route_not_allowed', 403)

            m = _SESSION_ID_RE.match(rel_path)
            if m and method == 'GET':
                sid = m.group('session_id')
                subroute = m.group('subroute')
                if not _safe_path_segment(sid) or (subroute is None and sid in _RESERVED_SESSION_SEGMENTS):
                    raise PolicyError('gateway_route_not_allowed', 403)
                upstream_session_path = f'/api/v1/sessions/{sid}/{subroute}' if subroute else f'/api/v1/sessions/{sid}'
                return await proxy(
                    request,
                    ctx,
                    upstream_session_path,
                    dataset_filter_fn=lambda data: _filter_session_detail(ctx, data),
                )

            m = _CAPTURE_ENTRY_RE.match(rel_path)
            if m and method in ('GET', 'PUT'):
                msg_id = m.group('message')
                if not _safe_path_segment(msg_id):
                    raise PolicyError('gateway_route_not_allowed', 403)
                if method == 'GET':
                    ds_id = _uuid_str(request.query.get('dataset_id', ''))
                    if not ds_id:
                        raise PolicyError('dataset_access_denied', 403)
                    _authorize_target(_resolve_target(ctx, ds_id), 'read')
                    return await proxy(request, ctx, f'/api/v1/capture/entries/{msg_id}')
                body_bytes = await _read_limited_body(request)
                payload = _read_json_bytes(body_bytes)
                ds_id = _uuid_str(payload.get('dataset_id'))
                if not ds_id:
                    raise PolicyError('dataset_access_denied', 403)
                _authorize_target(_resolve_target(ctx, ds_id), 'capture')
                entry_obj = payload.get('entry')
                if isinstance(entry_obj, dict):
                    entry_obj = enrich_codex_file_change_entry(entry_obj)
                    payload = {**payload, 'entry': entry_obj}
                resp = await proxy(request, ctx, f'/api/v1/capture/entries/{msg_id}', json_body=payload)
                if 200 <= resp.status < 300:
                    await _observe_trace_drift(ds_id, payload.get('session_id'), entry_obj, message_id=msg_id)
                return resp

            if method == 'POST' and rel_path in ('/api/v1/recall', '/api/v1/search'):
                body_bytes = await _read_limited_body(request)
                payload = _rewrite_query_datasets(ctx, _read_json_bytes(body_bytes))
                channel = request.headers.get('X-Memlane-Recall-Channel', 'explicit').strip().lower()
                if channel not in ('automatic', 'explicit'):
                    channel = 'explicit'
                return await proxy(
                    request, ctx, rel_path, json_body=payload, record_hits=True,
                    recall_channel=channel, timeout_seconds=GATEWAY_QUERY_TIMEOUT_SECONDS,
                )

            if method == 'POST' and rel_path == '/api/v1/remember/entry':
                body_bytes = await _read_limited_body(request)
                payload = _read_json_bytes(body_bytes)
                ref = payload.get('dataset_id') or payload.get('datasetId') or payload.get('dataset_name') or payload.get('datasetName')
                info = _authorize_target(_resolve_target(ctx, ref), 'capture')
                updated = {**payload, 'dataset_id': info['dataset_id']}
                updated.pop('datasetId', None)
                updated.pop('datasetName', None)
                updated.pop('dataset_name', None)
                entry_obj = updated.get('entry')
                if isinstance(entry_obj, dict):
                    entry_obj = ensure_qa_context_date(enrich_codex_file_change_entry(entry_obj))
                    updated['entry'] = entry_obj
                resp = await proxy(request, ctx, rel_path, json_body=updated)
                if 200 <= resp.status < 300:
                    await _observe_trace_drift(
                        info['dataset_id'],
                        updated.get('session_id') or updated.get('sessionId'),
                        entry_obj,
                        message_id=_trace_message_id(entry_obj),
                    )
                return resp

            if method == 'POST' and rel_path == '/api/v1/remember':
                body_bytes = await _read_limited_body(request)
                fields = _parse_remember_fields(request.headers.get('Content-Type', ''), body_bytes)
                id_ref = fields.get('datasetId') or fields.get('dataset_id')
                name_ref = fields.get('datasetName') or fields.get('dataset_name')
                ref = id_ref or name_ref
                op = 'code_write' if fields.get('content_type') == 'code' else 'capture'
                info = _authorize_target(_resolve_target(ctx, ref), op)
                if id_ref and name_ref:
                    expected_name = info.get('dataset_name')
                    if expected_name and str(name_ref).strip() != expected_name:
                        raise PolicyError('dataset_access_denied', 403)
                ctype = (request.headers.get('Content-Type') or '').split(';', 1)[0].strip().lower()
                if ctype == 'application/json':
                    payload = _read_json_bytes(body_bytes)
                    payload['dataset_id'] = info['dataset_id']
                    payload.pop('datasetId', None)
                    payload.pop('datasetName', None)
                    payload.pop('dataset_name', None)
                    return await proxy(request, ctx, rel_path, json_body=payload)
                if not id_ref:
                    if ctype == 'multipart/form-data':
                        body_bytes = _append_multipart_field(request.headers.get('Content-Type', ''), body_bytes, 'datasetId', info['dataset_id'])
                    elif ctype == 'application/x-www-form-urlencoded':
                        sep = b'&' if body_bytes else b''
                        body_bytes = body_bytes + sep + ('datasetId=' + info['dataset_id']).encode('utf-8')
                return await proxy(request, ctx, rel_path, body_bytes=body_bytes)

            if method == 'POST' and rel_path in ('/api/v1/improve', '/api/v1/cognify', '/api/v1/memify'):
                body_bytes = await _read_limited_body(request)
                payload = _read_json_bytes(body_bytes)
                if rel_path == '/api/v1/improve':
                    ref = payload.get('dataset_id') or payload.get('datasetId') or payload.get('dataset_name') or payload.get('datasetName')
                    info = _authorize_target(_resolve_target(ctx, ref), 'process')
                    updated = {**payload, 'dataset_id': info['dataset_id']}
                    updated.pop('datasetId', None)
                    updated.pop('datasetName', None)
                    updated.pop('dataset_name', None)
                    return await proxy(request, ctx, rel_path, json_body=updated)
                refs = payload.get('dataset_ids') or payload.get('datasetIds') or payload.get('datasets')
                if not isinstance(refs, list) or not refs:
                    single = payload.get('dataset_id') or payload.get('datasetId') or payload.get('dataset_name')
                    refs = [single] if single else []
                if not refs:
                    raise PolicyError('managed_dataset_required', 403)
                target_ids = [_authorize_target(_resolve_target(ctx, ref), 'process')['dataset_id'] for ref in refs]
                updated = {**payload, 'dataset_ids': target_ids}
                updated.pop('datasetIds', None)
                updated.pop('datasets', None)
                return await proxy(request, ctx, rel_path, json_body=updated)

            if method == 'POST' and rel_path == '/api/v1/forget':
                body_bytes = await _read_limited_body(request)
                payload = _read_json_bytes(body_bytes)
                if payload.get('everything'):
                    raise PolicyError('forget_everything_not_allowed', 403)
                ref = payload.get('dataset_id') or payload.get('datasetId') or payload.get('dataset')
                info = _authorize_target(_resolve_target(ctx, ref), 'delete')
                updated = {**payload, 'dataset_id': info['dataset_id']}
                updated.pop('datasetId', None)
                updated.pop('dataset', None)
                return await proxy(request, ctx, rel_path, json_body=updated)

            if method == 'POST' and rel_path == '/api/v1/agents/register':
                body_bytes = await _read_limited_body(request)
                payload = _read_json_bytes(body_bytes)
                raw_ids = payload.get('dataset_ids')
                raw_names = payload.get('dataset_names')
                resolved_ids = []
                if raw_ids is not None:
                    if not isinstance(raw_ids, list):
                        raise PolicyError('invalid_request', 400)
                    for item in raw_ids:
                        uid = _uuid_str(str(item)) if item is not None else None
                        if uid is None:
                            raise PolicyError('dataset_access_denied', 403)
                        info = _authorize_target(_resolve_target(ctx, uid), 'read')
                        if info['dataset_id'] not in resolved_ids:
                            resolved_ids.append(info['dataset_id'])
                if raw_names is not None:
                    if not isinstance(raw_names, list):
                        raise PolicyError('invalid_request', 400)
                    for name in raw_names:
                        info = _authorize_target(_resolve_target(ctx, name), 'read')
                        if info['dataset_id'] not in resolved_ids:
                            resolved_ids.append(info['dataset_id'])
                updated = dict(payload)
                if raw_ids is not None or raw_names is not None:
                    updated['dataset_ids'] = resolved_ids
                    updated.pop('dataset_names', None)
                return await proxy(request, ctx, rel_path, json_body=updated)

            if method == 'POST' and rel_path == '/api/v1/agents/unregister':
                body_bytes = await _read_limited_body(request)
                payload = _read_json_bytes(body_bytes)
                return await proxy(request, ctx, rel_path, json_body=payload)

            raise PolicyError('gateway_route_not_allowed', 403)
        except PolicyError as error:
            return web.json_response({'error': error.code, 'detail': error.code}, status=error.status)

    gw = web.Application(client_max_size=MAX_GATEWAY_BODY_BYTES)
    gw.router.add_route('*', '/{tail:.*}', handle)
    gw.router.add_route('*', '/', handle)

    async def cleanup(_):
        if owned_session is not None and not owned_session.closed:
            await owned_session.close()

    gw.on_cleanup.append(cleanup)
    return gw


def attach_cognee_gateway(app, registry, *, recall_store=None, drift_store=None, official_url=None, session=None):
    """Mount the Cognee HTTP Extension Gateway subapp at ``/cognee``."""
    gw = create_gateway_app(registry, recall_store=recall_store, drift_store=drift_store, official_url=official_url, session=session, prefix='/cognee')
    app.add_subapp('/cognee', gw)
    return gw
