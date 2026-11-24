"""Phase 1 /v1/* cursor-paginated read APIs, local body fast-path, and reading-state endpoints.

Implements every Phase 1 control-plane endpoint specified in proposal.md §3.5:
- GET  /v1/datasets/{ds}/documents/{doc}/body
- GET  /v1/overview/bootstrap
- GET  /v1/overview/day
- GET  /v1/projects/{id}/knowledge
- GET  /v1/common/knowledge
- GET  /v1/search/recent
- GET  /v1/search/metadata
- GET  /v1/projects/{id}/raw-records
- GET  /v1/projects/{id}/raw-records/sessions/{sid}/children
- GET  /v1/projects/{id}/message-summary
- POST /v1/reading-state/migrate-local
- POST /v1/reading-state/import/begin
- POST /v1/reading-state/import/chunk
- POST /v1/reading-state/import/commit
- POST /v1/reading-state/mark-read
- POST /v1/reading-state/mark-all-read
- POST /v1/provenance/backfill-legacy-dates
"""
from aiohttp import web
from catalog_index import CatalogIndex
from local_body_reader import (
    DEFAULT_LIST_JSON_BUDGET_BYTES,
    DEFAULT_OVERVIEW_JSON_BUDGET_BYTES,
    MAX_BODY_BYTES,
    finalize_budgeted_payload,
    json_utf8_response,
    read_document_body_with_fallback,
)
from project_policy import PolicyError
from provenance_store import _calendar_date
from reading_state_store import ReadingStateStore
from sqlite_ro import is_valid_uuid
from v2_lookups import _require_verified_managed_dataset, _safe_session_id
from v2_api_context import (
    CursorCodec,
    V2APIHandles,
    V2ApiContext,
    _IMAGE_ONLY_RE,
    _MACHINE_NAME_RE,
    _PIPELINE_RUN_SCAN_CHUNK,
    _PIPELINE_RUN_SCAN_MAX_CHUNKS,
    _SessionRelationsResolver,
    _b64u_decode,
    _b64u_encode,
    _filter_digest,
    _first_preview_sentence,
    _format_knowledge_item,
    _format_pipeline_run_row,
    _has_unconfirmed_groups,
    _knowledge_title,
    _read_backlog_slim,
    _read_recall_events_summary,
    _read_recent_pipeline_runs,
    _readable_name,
    _recall_event_doc_ids,
    _require_verified_project,
    _resolve_common_dataset,
    _verified_groups,
)
from v2_api_raw import (
    _query_raw_records_v2,
    _query_session_children_v2,
    execute_raw_records,
    execute_raw_records_session_children,
)
from v2_api_mutations import (
    _archive_legacy_dates_if_unchanged,
    execute_provenance_backfill_legacy_dates,
    execute_reading_import_begin,
    execute_reading_import_chunk,
    execute_reading_import_commit,
    execute_reading_mark_all_read,
    execute_reading_mark_read,
    execute_reading_migrate_local,
)
from v2_api_overview import (
    execute_message_summary,
    execute_overview_bootstrap,
    execute_overview_day,
)
from v2_api_knowledge import (
    execute_common_knowledge,
    execute_project_knowledge,
    execute_search_metadata,
    execute_search_recent,
)


_MAX_PAGE_LIMIT = 50
_DEFAULT_PAGE_LIMIT = 30
_MAX_MESSAGE_SUMMARY_BYTES = 16 * 1024


def _parse_limit(raw_val, *, default: int = _DEFAULT_PAGE_LIMIT, max_val: int = _MAX_PAGE_LIMIT) -> int:
    if raw_val is None or raw_val == '':
        return default
    try:
        val = int(raw_val)
    except ValueError as exc:
        raise PolicyError('invalid_limit', 400) from exc
    if not 1 <= val <= max_val:
        raise PolicyError('invalid_limit', 400)
    return val


def attach_v2_api(
    app,
    registry,
    *,
    manager_token: str = '',
    recall_store=None,
    codex_config=None,
    backlog=None,
    catalog_index: CatalogIndex | None = None,
    reading_state: ReadingStateStore | None = None,
) -> V2APIHandles:
    ctx = V2ApiContext(
        registry,
        manager_token=manager_token,
        recall_store=recall_store,
        codex_config=codex_config,
        backlog=backlog,
        catalog_index=catalog_index,
        reading_state=reading_state,
    )

    async def document_body(request):
        ctx.require_manager(request)
        if request.query:
            raise PolicyError('invalid_request_fields', 400)
        ds_id = _require_verified_managed_dataset(registry, request.match_info['ds'], allow_code=True)
        raw_doc = request.match_info['doc']
        if not is_valid_uuid(raw_doc):
            raise PolicyError('invalid_document_id', 400)
        if not ctx.official_data:
            raise PolicyError('official_store_unavailable', 503)
        result = await read_document_body_with_fallback(
            ctx.official_data,
            ds_id,
            raw_doc,
            official=getattr(registry, 'official', None),
            max_bytes=MAX_BODY_BYTES,
        )
        return json_utf8_response(result)

    async def overview_bootstrap_v2(request):
        ctx.require_manager(request)
        ctx.require_reading_contract(request)
        if set(request.query) - {'days', 'recent_limit', 'activity_limit', 'recall_limit'}:
            raise PolicyError('invalid_overview_query', 400)
        days = _parse_limit(request.query.get('days'), default=365, max_val=3650)
        recent_limit = _parse_limit(request.query.get('recent_limit'), default=20, max_val=20)
        activity_limit = _parse_limit(request.query.get('activity_limit'), default=20, max_val=50)
        recall_limit = _parse_limit(request.query.get('recall_limit'), default=20, max_val=50)
        identity = ctx.reading_identity(request)

        make_payload, items, trim_payload = await execute_overview_bootstrap(
            ctx, days=days, recent_limit=recent_limit, activity_limit=activity_limit,
            recall_limit=recall_limit, identity=identity,
        )
        payload, raw_bytes = finalize_budgeted_payload(
            make_payload, items, max_json_bytes=DEFAULT_OVERVIEW_JSON_BUDGET_BYTES,
            fixed_trimmer=trim_payload,
        )
        return json_utf8_response(payload, raw_bytes=raw_bytes)

    async def overview_day_v2(request):
        ctx.require_manager(request)
        if set(request.query) - {'day', 'limit', 'cursor'}:
            raise PolicyError('invalid_overview_query', 400)
        day = _calendar_date(request.query.get('day', ''))
        if not day:
            raise PolicyError('invalid_day_parameter', 400)
        limit = _parse_limit(request.query.get('limit'))
        raw_cursor = (request.query.get('cursor') or '').strip()
        identity = ctx.reading_identity(request)

        make_payload, items = await execute_overview_day(
            ctx, day=day, limit=limit, raw_cursor=raw_cursor, identity=identity,
        )
        payload, raw_bytes = finalize_budgeted_payload(
            make_payload, items, max_json_bytes=DEFAULT_LIST_JSON_BUDGET_BYTES,
        )
        return json_utf8_response(payload, raw_bytes=raw_bytes)

    async def project_knowledge_v2(request):
        ctx.require_manager(request)
        allowed_q = {
            'sort', 'category', 'unread_only', 'recall_agent_id',
            'recall_event_id', 'limit', 'cursor', 'direction', 'anchor_doc_id',
        }
        if set(request.query) - allowed_q:
            raise PolicyError('invalid_knowledge_query', 400)

        project_id = request.match_info['id']
        dataset_id = _require_verified_project(registry, project_id)['dataset_id']

        sort_mode = (request.query.get('sort') or 'date').strip()
        if sort_mode not in ('date', 'recall'):
            raise PolicyError('invalid_sort_mode', 400)
        category = (request.query.get('category') or '').strip()
        unread_only_raw = (request.query.get('unread_only') or '0').strip().lower()
        if unread_only_raw not in ('0', '1', 'true', 'false'):
            raise PolicyError('invalid_unread_only', 400)
        unread_only = unread_only_raw in ('1', 'true')

        recall_agent = (request.query.get('recall_agent_id') or '').strip()
        recall_event = (request.query.get('recall_event_id') or '').strip()
        if bool(recall_agent) != bool(recall_event):
            raise PolicyError('invalid_recall_filter', 400)
        if recall_event and not is_valid_uuid(recall_event):
            raise PolicyError('invalid_recall_filter', 400)

        direction = (request.query.get('direction') or 'next').strip()
        if direction not in ('next', 'prev'):
            raise PolicyError('invalid_knowledge_query', 400)
        anchor_doc_id = (request.query.get('anchor_doc_id') or '').strip()
        if anchor_doc_id and not is_valid_uuid(anchor_doc_id):
            raise PolicyError('invalid_document_id', 400)

        limit = _parse_limit(request.query.get('limit'))
        raw_cursor = (request.query.get('cursor') or '').strip()
        if anchor_doc_id and raw_cursor:
            raise PolicyError('invalid_knowledge_query', 400)
        if direction == 'prev' and not raw_cursor and not anchor_doc_id:
            raise PolicyError('invalid_cursor', 400)

        identity = ctx.reading_identity(request)
        if unread_only and not identity:
            raise PolicyError('reading_identity_required', 400)

        make_payload, items = await execute_project_knowledge(
            ctx, project_id=project_id, sort_mode=sort_mode, category=category,
            unread_only=unread_only, recall_agent=recall_agent, recall_event=recall_event,
            direction=direction, anchor_doc_id=anchor_doc_id, limit=limit, raw_cursor=raw_cursor,
            identity=identity, dataset_id=dataset_id,
        )
        payload, raw_bytes = finalize_budgeted_payload(
            make_payload, items, max_json_bytes=DEFAULT_LIST_JSON_BUDGET_BYTES,
            anchor_doc_id=anchor_doc_id if direction != 'prev' else '',
            trim_from_left=direction == 'prev',
        )
        return json_utf8_response(payload, raw_bytes=raw_bytes)

    async def common_knowledge_v2(request):
        ctx.require_manager(request)
        if set(request.query) - {
            'dataset_id', 'common_category', 'limit', 'cursor', 'direction', 'anchor_doc_id',
            'recall_agent_id', 'recall_event_id',
        }:
            raise PolicyError('invalid_common_query', 400)
        raw_ds = (request.query.get('dataset_id') or '').strip()
        dataset_id = _resolve_common_dataset(registry, raw_ds)

        common_cat = (request.query.get('common_category') or 'all').strip()
        if common_cat not in ('all', 'preference', 'experience'):
            raise PolicyError('invalid_common_category', 400)
        recall_agent = (request.query.get('recall_agent_id') or '').strip()
        recall_event = (request.query.get('recall_event_id') or '').strip()
        if bool(recall_agent) != bool(recall_event) or (recall_event and not is_valid_uuid(recall_event)):
            raise PolicyError('invalid_recall_filter', 400)
        direction = (request.query.get('direction') or 'next').strip()
        if direction not in ('next', 'prev'):
            raise PolicyError('invalid_common_query', 400)
        anchor_doc_id = (request.query.get('anchor_doc_id') or '').strip()
        if anchor_doc_id and not is_valid_uuid(anchor_doc_id):
            raise PolicyError('invalid_document_id', 400)

        limit = _parse_limit(request.query.get('limit'))
        raw_cursor = (request.query.get('cursor') or '').strip()
        if anchor_doc_id and raw_cursor:
            raise PolicyError('invalid_common_query', 400)
        if direction == 'prev' and not raw_cursor and not anchor_doc_id:
            raise PolicyError('invalid_cursor', 400)

        make_payload, items = await execute_common_knowledge(
            ctx, raw_ds=raw_ds, common_cat=common_cat, recall_agent=recall_agent,
            recall_event=recall_event, direction=direction, anchor_doc_id=anchor_doc_id, limit=limit,
            raw_cursor=raw_cursor, dataset_id=dataset_id,
        )
        payload, raw_bytes = finalize_budgeted_payload(
            make_payload, items, max_json_bytes=DEFAULT_LIST_JSON_BUDGET_BYTES,
            anchor_doc_id=anchor_doc_id if direction != 'prev' else '',
            trim_from_left=direction == 'prev',
        )
        return json_utf8_response(payload, raw_bytes=raw_bytes)

    async def search_recent_v2(request):
        ctx.require_manager(request)
        if set(request.query) - {'limit', 'project_id', 'dataset_id'}:
            raise PolicyError('invalid_search_query', 400)
        limit = _parse_limit(request.query.get('limit'), default=20, max_val=_MAX_PAGE_LIMIT)
        project_id_filter = (request.query.get('project_id') or '').strip()
        dataset_id_filter = (request.query.get('dataset_id') or '').strip()
        if project_id_filter and dataset_id_filter:
            raise PolicyError('invalid_search_query', 400)
        identity = ctx.reading_identity(request)

        make_payload, items, trim_payload = await execute_search_recent(
            ctx, limit=limit, project_id_filter=project_id_filter, dataset_id_filter=dataset_id_filter,
            identity=identity,
        )
        payload, raw_bytes = finalize_budgeted_payload(
            make_payload, items, max_json_bytes=DEFAULT_OVERVIEW_JSON_BUDGET_BYTES,
            fixed_trimmer=trim_payload,
        )
        return json_utf8_response(payload, raw_bytes=raw_bytes)

    async def search_metadata_v2(request):
        ctx.require_manager(request)
        if set(request.query) - {'q', 'filter', 'project_id', 'dataset_id', 'limit'}:
            raise PolicyError('invalid_search_query', 400)
        query_text = (request.query.get('q') or '').strip()
        if not query_text or len(query_text) > 256:
            raise PolicyError('invalid_search_query', 400)
        filter_kind = (request.query.get('filter') or 'all').strip()
        if filter_kind not in ('all', 'knowledge', 'raw'):
            raise PolicyError('invalid_search_query', 400)
        limit = _parse_limit(request.query.get('limit'), default=30, max_val=_MAX_PAGE_LIMIT)
        project_id_filter = (request.query.get('project_id') or '').strip()
        dataset_id_filter = (request.query.get('dataset_id') or '').strip()
        if project_id_filter and dataset_id_filter:
            raise PolicyError('invalid_search_query', 400)
        identity = ctx.reading_identity(request)

        make_payload, items = await execute_search_metadata(
            ctx, query_text=query_text, filter_kind=filter_kind, limit=limit,
            project_id_filter=project_id_filter, dataset_id_filter=dataset_id_filter,
            identity=identity,
        )
        payload, raw_bytes = finalize_budgeted_payload(
            make_payload, items, max_json_bytes=DEFAULT_LIST_JSON_BUDGET_BYTES,
        )
        return json_utf8_response(payload, raw_bytes=raw_bytes)

    async def raw_records_v2(request):
        ctx.require_manager(request)
        if set(request.query) - {'kind', 'limit', 'cursor'}:
            raise PolicyError('invalid_raw_records_query', 400)
        project_id = request.match_info['id']
        dataset_id = _require_verified_project(registry, project_id)['dataset_id']
        kind = (request.query.get('kind') or 'all').strip()
        if kind not in ('all', 'session', 'document'):
            raise PolicyError('invalid_raw_records_query', 400)
        limit = _parse_limit(request.query.get('limit'))
        raw_cursor = (request.query.get('cursor') or '').strip()

        make_payload, items = await execute_raw_records(
            ctx, project_id=project_id, kind=kind, limit=limit, raw_cursor=raw_cursor,
            dataset_id=dataset_id,
        )
        payload, raw_bytes = finalize_budgeted_payload(
            make_payload, items, max_json_bytes=DEFAULT_LIST_JSON_BUDGET_BYTES,
        )
        return json_utf8_response(payload, raw_bytes=raw_bytes)

    async def raw_records_session_children_v2(request):
        ctx.require_manager(request)
        if set(request.query) - {'limit', 'cursor', 'user_id'}:
            raise PolicyError('invalid_raw_records_query', 400)
        project_id = request.match_info['id']
        dataset_id = _require_verified_project(registry, project_id)['dataset_id']
        parent_sid = request.match_info['sid']
        if not _safe_session_id(parent_sid):
            raise PolicyError('invalid_session_id', 400)
        user_id = (request.query.get('user_id') or '').strip()
        if user_id and not is_valid_uuid(user_id):
            raise PolicyError('invalid_user_id', 400)
        limit = _parse_limit(request.query.get('limit'))
        raw_cursor = (request.query.get('cursor') or '').strip()

        make_payload, items = await execute_raw_records_session_children(
            ctx, project_id=project_id, parent_sid=parent_sid, user_id=user_id, limit=limit,
            raw_cursor=raw_cursor, dataset_id=dataset_id,
        )
        payload, raw_bytes = finalize_budgeted_payload(
            make_payload, items, max_json_bytes=DEFAULT_LIST_JSON_BUDGET_BYTES,
        )
        return json_utf8_response(payload, raw_bytes=raw_bytes)

    async def message_summary_v2(request):
        ctx.require_manager(request)
        ctx.require_reading_contract(request)
        if request.query:
            raise PolicyError('invalid_summary_query', 400)
        project_id = request.match_info['id']
        identity = ctx.reading_identity(request)

        make_payload, items, trim_payload = await execute_message_summary(
            ctx, project_id=project_id, identity=identity,
        )
        payload, raw_bytes = finalize_budgeted_payload(
            make_payload, items, max_json_bytes=_MAX_MESSAGE_SUMMARY_BYTES,
            fixed_trimmer=trim_payload,
        )
        return json_utf8_response(payload, raw_bytes=raw_bytes)

    async def reading_migrate_local(request):
        ctx.require_manager(request)
        return json_utf8_response(await execute_reading_migrate_local(ctx, await ctx.json_body(request)))

    async def reading_import_begin(request):
        ctx.require_manager(request)
        return json_utf8_response(await execute_reading_import_begin(ctx, await ctx.json_body(request)))

    async def reading_import_chunk(request):
        ctx.require_manager(request)
        return json_utf8_response(await execute_reading_import_chunk(ctx, await ctx.json_body(request)))

    async def reading_import_commit(request):
        ctx.require_manager(request)
        return json_utf8_response(await execute_reading_import_commit(ctx, await ctx.json_body(request)))

    async def reading_mark_read(request):
        ctx.require_manager(request)
        return json_utf8_response(await execute_reading_mark_read(ctx, await ctx.json_body(request)))

    async def reading_mark_all_read(request):
        ctx.require_manager(request)
        return json_utf8_response(await execute_reading_mark_all_read(ctx, await ctx.json_body(request)))

    async def provenance_backfill_legacy_dates(request):
        ctx.require_manager(request)
        return json_utf8_response(await execute_provenance_backfill_legacy_dates(ctx, await ctx.json_body(request)))

    app.add_routes([
        web.get('/v1/datasets/{ds}/documents/{doc}/body', document_body),
        web.get('/v1/overview/bootstrap', overview_bootstrap_v2),
        web.get('/v1/overview/day', overview_day_v2),
        web.get('/v1/projects/{id}/knowledge', project_knowledge_v2),
        web.get('/v1/common/knowledge', common_knowledge_v2),
        web.get('/v1/search/recent', search_recent_v2),
        web.get('/v1/search/metadata', search_metadata_v2),
        web.get('/v1/projects/{id}/raw-records', raw_records_v2),
        web.get('/v1/projects/{id}/raw-records/sessions/{sid}/children', raw_records_session_children_v2),
        web.get('/v1/projects/{id}/message-summary', message_summary_v2),
        web.post('/v1/reading-state/migrate-local', reading_migrate_local),
        web.post('/v1/reading-state/import/begin', reading_import_begin),
        web.post('/v1/reading-state/import/chunk', reading_import_chunk),
        web.post('/v1/reading-state/import/commit', reading_import_commit),
        web.post('/v1/reading-state/mark-read', reading_mark_read),
        web.post('/v1/reading-state/mark-all-read', reading_mark_all_read),
        web.post('/v1/provenance/backfill-legacy-dates', provenance_backfill_legacy_dates),
    ])

    app.on_cleanup.append(ctx.cleanup)
    return ctx.handles
