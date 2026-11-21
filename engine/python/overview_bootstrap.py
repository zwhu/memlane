"""Read-only overview bootstrap aggregation over the official local store.

Serves the desktop Overview in a single authenticated loopback call (~30-50ms)
without modifying upstream Cognee source or keeping a second cache on disk.
"""
import asyncio
from pathlib import Path
import sqlite3
from urllib.parse import unquote, urlparse

from aiohttp import web

from _runtime_common import (
    decode_node_set,
    id_variants,
    is_knowledge_doc,
    iso_text,
    parse_json,
    sql_in_clause,
    sqlite_table_columns,
    uuid_text,
    verify_sqlite_schema,
)
from project_policy import PolicyError
from sqlite_ro import open_official_sqlite_ro
from provenance_store import (
    calendar_date,
    is_lesson_metadata,
    load_provenance_maps,
    local_calendar_date,
    resolve_lesson_date,
)

_MAX_PREVIEW_DOCS = 30
_MAX_PREVIEW_BYTES = 4096
# Compatibility alias for sibling modules outside this track (session_relations.py).
_parse_json = parse_json
_OVERVIEW_REQUIRED_SCHEMA = {
    'data': frozenset({
        'id', 'dataset_id', 'name', 'label', 'mime_type', 'raw_data_location',
        'external_metadata', 'node_set', 'updated_at', 'created_at', 'content_hash',
    }),
    'session_records': frozenset({
        'session_id', 'user_id', 'dataset_id', 'agent_id', 'last_activity_at', 'started_at',
    }),
    'pipeline_runs': frozenset({
        'id', 'pipeline_run_id', 'parent_operation_id', 'dataset_id',
        'pipeline_name', 'operation_name', 'status', 'outcome', 'background',
        'error_class', 'origin', 'created_at', 'started_at', 'ended_at',
    }),
}


def _read_local_preview(raw_location, official_root):
    if not raw_location or not official_root:
        return ''
    text = str(raw_location).strip()
    if text.startswith('file://'):
        parsed = urlparse(text)
        if parsed.netloc not in ('', 'localhost'):
            return ''
        path = Path(unquote(parsed.path))
    else:
        path = Path(text)
    try:
        if path.is_symlink():
            return ''
        resolved = path.resolve(strict=True)
        resolved.relative_to(official_root)
        if not resolved.is_file():
            return ''
        with resolved.open('rb') as handle:
            chunk = handle.read(_MAX_PREVIEW_BYTES)
        return chunk.decode('utf-8', errors='replace')
    except (OSError, ValueError):
        return ''


def _effective_knowledge_day(meta, created_at):
    for key in ('first_source_date', 'session_date'):
        day = calendar_date(meta.get(key))
        if day:
            return day
    return local_calendar_date(created_at) or ''


def _read_overview_snapshot(official_data, provenance_path, allowed_datasets, activity_limit):
    official_root = Path(official_data).resolve()
    db_path = official_root / 'system' / 'databases' / 'cognee_db'
    if db_path.is_symlink() or not db_path.is_file():
        raise PolicyError('overview_store_unavailable', 503)

    ds_variant_map = {}
    canonical_datasets = set()
    for ds in allowed_datasets:
        canon = uuid_text(ds)
        if not canon:
            continue
        canonical_datasets.add(canon)
        for variant in id_variants(canon):
            ds_variant_map[variant] = canon
    doc_prov, key_prov = load_provenance_maps(provenance_path, canonical_datasets)

    try:
        con = open_official_sqlite_ro(db_path)
    except sqlite3.Error as err:
        raise PolicyError('overview_store_unavailable', 503) from err

    documents = []
    sessions = []
    pipeline_runs = []
    pipeline_runs_more = False
    knowledge_candidates = []
    try:
        verify_sqlite_schema(con, _OVERVIEW_REQUIRED_SCHEMA)
        has_system_metadata = 'system_metadata' in sqlite_table_columns(con, 'data')
        if ds_variant_map:
            variant_list = tuple(sorted(ds_variant_map.keys()))
            ds_sql, ds_params = sql_in_clause(variant_list)
            sys_col_sql = ', system_metadata' if has_system_metadata else ''
            for row in con.execute(
                f'''SELECT id, dataset_id, name, label, mime_type, raw_data_location,
                           external_metadata, node_set, updated_at, created_at, content_hash{sys_col_sql}
                    FROM data WHERE dataset_id {ds_sql}''',
                ds_params,
            ):
                doc_id = uuid_text(row[0])
                ds_id = ds_variant_map.get(str(row[1]), uuid_text(row[1]))
                if not doc_id or not ds_id:
                    continue
                raw_ext = parse_json(row[6])
                ext_meta = dict(raw_ext) if isinstance(raw_ext, dict) else {}
                raw_sys = parse_json(row[11]) if has_system_metadata else None
                sys_meta = dict(raw_sys) if isinstance(raw_sys, dict) and raw_sys else None
                ns = decode_node_set(row[7])
                if ns and 'node_set' not in ext_meta:
                    ext_meta['node_set'] = ns
                if sys_meta is not None and ns and 'node_set' not in sys_meta:
                    sys_meta['node_set'] = ns
                trusted = sys_meta if sys_meta is not None else ext_meta
                meta = dict(ext_meta)
                created_at = iso_text(row[9])
                updated_at = iso_text(row[8])
                if is_lesson_metadata(trusted):
                    if sys_meta is not None:
                        for key in ('lesson_type', 'state_key', 'session_date', 'source_qa_ids'):
                            if sys_meta.get(key) is not None:
                                meta[key] = sys_meta[key]
                    resolved_day = resolve_lesson_date(
                        ds_id,
                        doc_id,
                        system_metadata=sys_meta,
                        external_metadata=ext_meta,
                        created_at=created_at,
                        doc_prov=doc_prov,
                        key_prov=key_prov,
                    )
                    if resolved_day:
                        meta['first_source_date'] = resolved_day
                doc_obj = {
                    'id': doc_id,
                    'datasetId': ds_id,
                    'name': row[2] or '',
                    'label': row[3],
                    'mimeType': row[4] or '',
                    'externalMetadata': meta,
                    'updatedAt': updated_at,
                    'createdAt': created_at,
                    'contentHash': row[10] or '',
                }
                documents.append(doc_obj)
                if is_knowledge_doc(meta) and row[5]:
                    day = _effective_knowledge_day(meta, created_at)
                    knowledge_candidates.append((day, f'{ds_id}/{doc_id}', row[5]))

            for srow in con.execute(
                f'''SELECT session_id, user_id, dataset_id, agent_id, last_activity_at, started_at
                    FROM session_records WHERE dataset_id {ds_sql}''',
                ds_params,
            ):
                sid = str(srow[0] or '')
                if not sid:
                    continue
                sessions.append({
                    'session_id': sid,
                    'user_id': uuid_text(srow[1]),
                    'dataset_id': ds_variant_map.get(str(srow[2]), uuid_text(srow[2])),
                    'agent_id': str(srow[3] or ''),
                    'last_activity_at': iso_text(srow[4]),
                    'started_at': iso_text(srow[5]),
                })

        pipe_rows = con.execute(
            '''SELECT id, pipeline_run_id, parent_operation_id, dataset_id,
                      pipeline_name, operation_name, status, outcome, background,
                      error_class, origin, created_at, started_at, ended_at
               FROM pipeline_runs ORDER BY created_at DESC, id DESC LIMIT ?''',
            (activity_limit + 1,),
        ).fetchall()
        pipeline_runs_more = len(pipe_rows) > activity_limit
        for pr in pipe_rows[:activity_limit]:
            pipeline_runs.append({
                'id': uuid_text(pr[0]),
                'pipeline_run_id': uuid_text(pr[1]),
                'parent_operation_id': uuid_text(pr[2]),
                'dataset_id': uuid_text(pr[3]),
                'kind': 'pipeline' if pr[4] else 'operation',
                'pipeline_name': pr[4] or '',
                'operation_name': pr[5] or '',
                'status': pr[6] or '',
                'outcome': pr[7] or '',
                'background': bool(pr[8]),
                'error_class': pr[9] or '',
                'origin': pr[10] or '',
                'created_at': iso_text(pr[11]),
                'started_at': iso_text(pr[12]),
                'ended_at': iso_text(pr[13]),
            })
    except sqlite3.Error as err:
        raise PolicyError('overview_store_unavailable', 503) from err
    finally:
        con.close()

    # Match Go aggregateOverview sort order (date DESC, key ASC) so top-N previews
    # cover the exact rows visible on the initial Overview screen.
    knowledge_candidates.sort(key=lambda item: item[1])
    knowledge_candidates.sort(key=lambda item: item[0], reverse=True)
    previews = {}
    for _, key, raw_loc in knowledge_candidates[:_MAX_PREVIEW_DOCS]:
        text = _read_local_preview(raw_loc, official_root)
        if text:
            previews[key] = text

    return {
        'documents': documents,
        'sessions': sessions,
        'previews': previews,
        'pipeline_runs': pipeline_runs,
        'pipeline_runs_more': pipeline_runs_more,
    }


def _read_recall_summary(recall_store, allowed_datasets, agents_state, recall_limit):
    if recall_store is None or not allowed_datasets:
        return {'recall_events': [], 'recall_events_more': False, 'recall_started_at': '', 'recall_counts': {}}
    events_page = recall_store.events(allowed_datasets, recall_limit, 0)
    for item in events_page['items']:
        agent = agents_state.get(item['agent_id'], {})
        item['agent_name'] = agent.get('name') or 'Agent 未记录'
        item['agent_kind'] = agent.get('kind') or ''
    counts_summary = recall_store.counts_for_datasets(allowed_datasets)
    return {
        'recall_events': events_page['items'],
        'recall_events_more': bool(events_page['more']),
        'recall_started_at': counts_summary['started_at'],
        'recall_counts': counts_summary['counts'],
    }


def attach_overview_bootstrap(app, registry, *, recall_store=None, codex_config=None):
    official_data = (codex_config or {}).get('official_data')
    provenance_path = registry.path.parent / 'lesson-provenance.sqlite3'

    async def overview_bootstrap(request):
        if not request['manager']:
            raise PolicyError('management_required', 403)
        if set(request.query) - {'activity_limit', 'recall_limit'}:
            raise PolicyError('invalid_overview_query')
        try:
            activity_limit = int(request.query.get('activity_limit', '250'))
            recall_limit = int(request.query.get('recall_limit', '50'))
        except ValueError:
            raise PolicyError('invalid_overview_query') from None
        if not 1 <= activity_limit <= 500 or not 1 <= recall_limit <= 100:
            raise PolicyError('invalid_overview_query')
        if not official_data:
            raise PolicyError('overview_store_unavailable', 503)

        allowed = {g['dataset_id'] for g in registry.managed_groups() if g.get('dataset_id')}
        agents_snapshot = {k: dict(v) for k, v in registry.state.get('agents', {}).items()}

        snapshot = await asyncio.to_thread(
            _read_overview_snapshot,
            official_data,
            provenance_path,
            allowed,
            activity_limit,
        )
        recalls = _read_recall_summary(recall_store, allowed, agents_snapshot, recall_limit)
        snapshot.update(recalls)
        return web.json_response(snapshot)

    app.add_routes([web.get('/v1/overview/bootstrap', overview_bootstrap)])
