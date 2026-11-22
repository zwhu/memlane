"""Read-only projection of bounded, content-free model request diagnostics."""
import asyncio
import json
from datetime import datetime, timezone
from pathlib import Path
import re
import sqlite3
from urllib.parse import quote

from aiohttp import web
from _runtime_common import id_variants, sql_in_clause, uuid_hex, uuid_text, verify_sqlite_schema
from performance_trace import BRIDGE_BACKENDS, BRIDGE_COMPONENTS, BRIDGE_ERROR_CODES
from project_policy import PolicyError

TAIL_BYTES = 1024 * 1024
ACTIVITY_PAGE_SIZE = 100
MAX_ACTIVITY_PAGES = 5
MAX_RESOLVED_NOTICES = 20
_RECEIPT_COLUMNS = ('pipeline_run_id', 'dataset_id', 'pipeline_name', 'parent_operation_id',
                    'operation_name', 'outcome', 'ended_at')


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
        if not 400 <= status <= 599:
            continue
        operation = row.get('operation_id') or row['request_id']
        if not isinstance(operation, str) or not re.fullmatch(r'[0-9a-f-]{32,36}', operation):
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
        key = uuid_text(operation)
        if key not in failures or at > failures[key][0]:
            failures[key] = (at, item)
    return [item for _, item in sorted(failures.values(), key=lambda pair: pair[0], reverse=True)]


def _sqlite_operation_rows(official_data, operations):
    db_path = Path(official_data).resolve() / 'system' / 'databases' / 'cognee_db'
    if db_path.is_symlink() or not db_path.is_file():
        return None
    ids = tuple(variant for operation in sorted(operations) for variant in id_variants(operation))
    rows = []
    try:
        con = sqlite3.connect(db_path.as_uri() + '?mode=ro', uri=True, timeout=1.0)
        try:
            verify_sqlite_schema(con, {'pipeline_runs': frozenset(_RECEIPT_COLUMNS)})
            # Only constrain the indexed run ID in SQL. Verify dataset and
            # operation kind in the shared SQLite/HTTP receipt check below.
            for start in range(0, len(ids), 500):
                clause, params = sql_in_clause(ids[start:start + 500])
                for values in con.execute(
                    f"SELECT {', '.join(_RECEIPT_COLUMNS)} FROM pipeline_runs WHERE pipeline_run_id {clause}",
                    params,
                ):
                    row = dict(zip(_RECEIPT_COLUMNS, values))
                    row['kind'] = 'pipeline' if row['pipeline_name'] else 'operation'
                    rows.append(row)
        finally:
            con.close()
    except sqlite3.Error:
        return None
    return rows


def _improve_operation_id(row, dataset):
    if (isinstance(row, dict) and row.get('kind') == 'operation'
            and row.get('operation_name') == 'improve' and not row.get('parent_operation_id')
            and uuid_text(row.get('dataset_id')) == dataset):
        return uuid_text(row.get('pipeline_run_id'))
    return ''


async def _http_operation_rows(official, dataset, operations):
    # Cognee offers a dataset-scoped paginated feed. Keep the fallback within
    # the desktop request budget and leave any unconfirmed operation visible.
    rows = []
    remaining = set(operations)
    try:
        async with asyncio.timeout(2.0):
            for page in range(MAX_ACTIVITY_PAGES):
                path = f'/api/v1/activity/pipeline-runs?dataset_id={quote(dataset, safe="")}&limit={ACTIVITY_PAGE_SIZE}'
                if page:
                    path += f'&offset={page * ACTIVITY_PAGE_SIZE}'
                batch = await official.request('GET', path)
                if not isinstance(batch, list):
                    break
                for row in batch:
                    operation = _improve_operation_id(row, dataset)
                    if operation in operations:
                        rows.append(row)
                        remaining.discard(operation)
                if not remaining or len(batch) < ACTIVITY_PAGE_SIZE:
                    break
    except (PolicyError, OSError, TimeoutError):
        pass
    return rows


async def read_processing_notices(registry, dataset, *, official_data=None):
    try:
        items = await asyncio.to_thread(processing_notices, registry.path.parent / 'runtime-logs', dataset)
    except OSError:
        raise PolicyError('processing_diagnostics_unavailable', 503) from None
    # A request without an operation ID is diagnostic-only evidence.
    operations = {uuid_text(row['operation_id']): row for row in items
                  if row['operation_id'] != row['request_id'] and uuid_hex(row['operation_id'])}
    expected_dataset = uuid_text(dataset)
    rows = []
    if operations:
        rows = await asyncio.to_thread(_sqlite_operation_rows, official_data, operations) if official_data else None
        if rows is None:
            official = getattr(registry, 'official', None)
            rows = await _http_operation_rows(official, expected_dataset, operations) if official is not None else []
    receipts = {}
    for row in rows:
        operation = _improve_operation_id(row, expected_dataset)
        if operation in operations:
            receipts.setdefault(operation, []).append(row)
    for operation, matches in receipts.items():
        if len(matches) != 1:
            continue
        receipt = matches[0]
        outcome = receipt.get('outcome')
        if outcome not in ('succeeded', 'failed'):
            continue
        try:
            ended = datetime.fromisoformat(receipt.get('ended_at'))
        except (TypeError, ValueError):
            continue
        # Pinned Cognee's improve writer closes the operation row after all
        # stages, including detached runs. SQLite stores naive UTC.
        if ended.tzinfo is None:
            ended = ended.replace(tzinfo=timezone.utc)
        item = operations[operation]
        if ended >= datetime.fromisoformat(item['at']):
            field = 'resolved_at' if outcome == 'succeeded' else 'failed_at'
            item[field] = ended.astimezone(timezone.utc).isoformat()
    unresolved = [item for item in items if 'resolved_at' not in item]
    resolved = sorted((item for item in items if 'resolved_at' in item),
                      key=lambda item: item['resolved_at'], reverse=True)
    return unresolved[:1], resolved[:MAX_RESOLVED_NOTICES]


def attach_processing_notices(app, registry, *, official_data=None):
    async def status(request):
        if not request['manager']:
            raise PolicyError('management_required', 403)
        project = registry.state['projects'].get(request.match_info['id'])
        if project is None:
            raise PolicyError('project_not_found', 404)
        items, resolved_items = await read_processing_notices(registry, project['dataset_id'], official_data=official_data)
        return web.json_response({'items': items, 'resolved_items': resolved_items})
    app.router.add_get('/v1/projects/{id}/processing-notices', status)
