"""Read-only projection of bounded, content-free model request diagnostics."""
import json
from datetime import datetime, timezone
from pathlib import Path
import re

from aiohttp import web
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
    codes = {'codex_schema_unsupported', 'codex_request_invalid', 'codex_model_failed',
             'codex_quota_exceeded', 'codex_rate_limited', 'codex_timeout',
             'codex_authentication_failed', 'codex_context_exceeded'}
    bridge = {r['request_id']: r.get('outcome') for r in
              trace_rows(Path(root) / 'codex_completion_endpoint.log')
              if r.get('component') == 'codex_bridge' and r.get('outcome') in codes}
    failures = {}
    for row in trace_rows(Path(root) / 'official_service_dev.log'):
        status = row.get('status')
        if (row.get('component') != 'generation_http' or row.get('dataset_id') != dataset
                or type(status) is not int or not 400 <= status <= 599):
            continue
        try:
            at = datetime.fromisoformat(row['at']).astimezone(timezone.utc)
            if not 0 <= (now - at).total_seconds() <= 86400:
                continue
        except (ValueError, TypeError, KeyError):
            continue
        operation = row.get('operation_id') or row['request_id']
        if not re.fullmatch(r'[0-9a-f-]{32,36}', str(operation)):
            continue
        item = {'operation_id': operation, 'request_id': row['request_id'],
                'at': at.isoformat(), 'http_status': status,
                'code': bridge.get(row['request_id'], 'model_request_failed'),
                'backend': 'codex' if row.get('backend_kind') == 'codex' else 'provider'}
        if operation not in failures or item['at'] > failures[operation]['at']:
            failures[operation] = item
    # Historical request failures, not inferred task outcomes or write receipts.
    return sorted(failures.values(), key=lambda item: item['at'], reverse=True)[:1]


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
