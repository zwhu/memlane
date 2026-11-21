"""Read-only projection of bounded, content-free model request diagnostics."""
import json
from datetime import datetime, timezone
from pathlib import Path
import re

from aiohttp import web
from performance_trace import BRIDGE_BACKENDS, BRIDGE_COMPONENTS, BRIDGE_ERROR_CODES
from project_policy import PolicyError

TAIL_BYTES = 1024 * 1024


def trace_rows(path):
    path = Path(path)
    if path.is_symlink():
        raise OSError('diagnostic_log_symlink')
    if not path.is_file():
        return []
    with path.open('rb') as handle:
        handle.seek(0, 2)
        size = handle.tell()
        handle.seek(max(0, size - TAIL_BYTES))
        data = handle.read(TAIL_BYTES)
    lines = data.splitlines()
    if size > TAIL_BYTES:
        lines = lines[1:]
    rows = []
    for line in lines:
        if len(line) > 8192 or not line.startswith(b'{'):
            continue
        try:
            row = json.loads(line)
            if (isinstance(row, dict) and row.get('schema_version') == 1
                    and row.get('event') == 'request_finished'
                    and re.fullmatch(r'[0-9a-f]{32}', str(row.get('request_id', '')))):
                rows.append(row)
        except (ValueError, TypeError):
            continue
    return rows


def processing_notices(root, dataset, *, now=None):
    now = now or datetime.now(timezone.utc)
    bridge = {}
    for log_name in ('codex_completion_endpoint.log', 'agent_completion_endpoint.log'):
        for r in trace_rows(Path(root) / log_name):
            comp = r.get('component')
            outcome = r.get('outcome')
            if comp in BRIDGE_COMPONENTS and outcome in BRIDGE_ERROR_CODES:
                bridge[r['request_id']] = (outcome, comp[:-len('_bridge')])
    failures = {}
    latest_success_dt = None
    latest_success_iso = ''
    for row in trace_rows(Path(root) / 'official_service_dev.log'):
        status = row.get('status')
        if (row.get('component') != 'generation_http' or row.get('dataset_id') != dataset
                or type(status) is not int):
            continue
        try:
            at = datetime.fromisoformat(row['at']).astimezone(timezone.utc)
            if not 0 <= (now - at).total_seconds() <= 86400:
                continue
        except (ValueError, TypeError, KeyError):
            continue
        if 200 <= status < 300:
            if latest_success_dt is None or at > latest_success_dt:
                latest_success_dt = at
                latest_success_iso = at.isoformat()
            continue
        if not 400 <= status <= 599:
            continue
        operation = row.get('operation_id') or row['request_id']
        if not re.fullmatch(r'[0-9a-f-]{32,36}', str(operation)):
            continue
        matched = bridge.get(row['request_id'])
        code = matched[0] if matched else 'model_request_failed'
        row_backend = row.get('backend_kind')
        if row_backend in BRIDGE_BACKENDS:
            backend = row_backend
        elif matched and row_backend in (None, '', 'unknown'):
            backend = matched[1]
        else:
            backend = 'provider'
        item = {'operation_id': operation, 'request_id': row['request_id'],
                'at': at.isoformat(), 'http_status': status,
                'code': code,
                'backend': backend}
        if operation not in failures or at > failures[operation][0]:
            failures[operation] = (at, item)
    # Historical request failures, not inferred task outcomes or write receipts.
    ordered = sorted(failures.values(), key=lambda pair: pair[0], reverse=True)[:1]
    if not ordered:
        return []
    latest_dt, latest_item = ordered[0]
    if latest_success_dt is not None and latest_success_dt > latest_dt:
        latest_item['recovered_at'] = latest_success_iso
    return [latest_item]


def attach_processing_notices(app, registry):
    async def status(request):
        if not request['manager']:
            raise PolicyError('management_required', 403)
        project = registry.state['projects'].get(request.match_info['id'])
        if project is None:
            raise PolicyError('project_not_found', 404)
        try:
            items = processing_notices(registry.path.parent / 'runtime-logs', project['dataset_id'])
        except OSError:
            raise PolicyError('processing_diagnostics_unavailable', 503) from None
        return web.json_response({'items': items})
    app.router.add_get('/v1/projects/{id}/processing-notices', status)
