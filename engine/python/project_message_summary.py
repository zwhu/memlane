"""Read-only project message summary aggregation for desktop queue and notices."""
import asyncio
import hashlib
import inspect
from pathlib import Path
import re
import sqlite3
from urllib.parse import quote

from aiohttp import web

from _runtime_common import (
    decode_node_set,
    id_variants,
    is_derived_material,
    is_knowledge_doc,
    iso_text,
    parse_json,
    sql_in_clause,
    sqlite_table_columns,
    uuid_text,
    verify_sqlite_schema,
)
from processing_notices import read_processing_notices
from project_policy import PolicyError
from provenance_store import (
    calendar_date,
    is_lesson_metadata,
    load_provenance_maps,
    resolve_lesson_date,
)

from sqlite_ro import open_official_sqlite_ro

_MACHINE_NAME_RE = re.compile(
    r'^(?:text_[0-9a-fA-F]{32}(?:\.(?:md|txt))?'
    r'|(?:user_context|memory)-[0-9a-fA-F]{32}(?:\.txt)?'
    r'|[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})$'
)
_SUMMARY_REQUIRED_SCHEMA = {
    'data': frozenset({
        'id', 'dataset_id', 'name', 'label', 'external_metadata',
        'node_set', 'pipeline_status', 'created_at', 'updated_at',
    }),
    'pipeline_runs': frozenset({
        'id', 'pipeline_run_id', 'parent_operation_id', 'dataset_id',
        'pipeline_name', 'operation_name', 'status', 'outcome', 'background',
        'error_class', 'origin', 'created_at', 'started_at', 'ended_at',
    }),
}


def _readable_name(value):
    if not isinstance(value, str):
        return ''
    text = value.strip()
    if not text or _MACHINE_NAME_RE.match(text):
        return ''
    return text


def _document_title(doc):
    label = _readable_name(doc.get('label'))
    if label:
        return label
    name = doc.get('name') or ''
    readable = _readable_name(name)
    if readable:
        return readable
    return str(name)


def _document_date(doc):
    meta = doc.get('externalMetadata')
    if isinstance(meta, dict) and is_lesson_metadata(meta):
        for key in ('first_source_date', 'session_date'):
            day = calendar_date(meta.get(key))
            if day:
                return day
    updated = doc.get('updatedAt')
    if isinstance(updated, str) and updated:
        return updated
    created = doc.get('createdAt')
    if isinstance(created, str) and created:
        return created
    return ''


def _error_code(exc, default_code):
    if isinstance(exc, PolicyError) and exc.code:
        return exc.code
    return default_code


async def _read_backlog(backlog, project_id):
    if backlog is None:
        return None, False, [], [], []
    reader = getattr(backlog, 'get', None) or getattr(backlog, 'status', None)
    if reader is None:
        raise PolicyError('backlog_unavailable', 503)
    result = reader(project_id)
    if inspect.isawaitable(result):
        result = await result
    if not isinstance(result, dict):
        raise PolicyError('backlog_unavailable', 503)
    job = result.get('job')
    if 'automatic_save' in result:
        automatic_save = bool(result['automatic_save'])
    else:
        automatic_save = bool(result.get('automatic_enabled', False))
    buffer_reviews = result.get('buffer_reviews')
    checks = result.get('checks')
    processing_issues = result.get('processing_issues')
    return (
        job,
        automatic_save,
        buffer_reviews if isinstance(buffer_reviews, list) else [],
        checks if isinstance(checks, list) else [],
        processing_issues if isinstance(processing_issues, list) else [],
    )


_DATA_HTTP_PAGE_SIZE = 500
_MAX_DATA_HTTP_OFFSET = 1_000_000


def _is_data_item_completed(status_value):
    if isinstance(status_value, dict):
        status_value = status_value.get('status')
    return status_value == 'DATA_ITEM_PROCESSING_COMPLETED'


def _is_refinement_material(doc):
    meta = doc.get('externalMetadata')
    # User profiles are saved and read as raw documents without refinement.
    if isinstance(meta, dict) and meta.get('memlane_kind') == 'user_profile':
        return False
    return not is_derived_material(doc)


def _knowledge_catalog_version(items):
    digest = hashlib.sha256()
    for item in sorted(items, key=lambda row: (row['id'], row['created_at'])):
        prov_day = item.get('first_source_date') or ''
        suffix = f"\x1f{prov_day}" if prov_day else ''
        digest.update(f"{item['id']}\x1f{item['created_at']}{suffix}\n".encode('utf-8'))
    return digest.hexdigest()[:24]


def _knowledge_catalog_entry(doc_id, created_at, *, dataset_id, system_metadata=None,
                             external_metadata=None, doc_prov=None, key_prov=None):
    entry = {'id': doc_id, 'created_at': created_at}
    resolved_day = resolve_lesson_date(
        dataset_id,
        doc_id,
        system_metadata=system_metadata,
        external_metadata=external_metadata,
        created_at=created_at,
        doc_prov=doc_prov,
        key_prov=key_prov,
        include_created_fallback=False,
    )
    if resolved_day:
        entry['first_source_date'] = resolved_day
    return entry


def _read_sqlite_documents_and_activity(official_data, dataset_id, *, provenance_path=None):
    if not official_data:
        return None, False
    db_path = Path(official_data).resolve() / 'system' / 'databases' / 'cognee_db'
    if db_path.is_symlink() or not db_path.is_file():
        return None, True
    variants = id_variants(dataset_id)
    if not variants:
        return None, True
    ds_sql, ds_params = sql_in_clause(variants)
    expected_ds = uuid_text(dataset_id)
    doc_prov, key_prov = load_provenance_maps(provenance_path, {expected_ds})

    try:
        con = open_official_sqlite_ro(db_path)
    except (PolicyError, sqlite3.Error):
        return None, True

    pending = []
    knowledge_items = []
    activity = []
    try:
        verify_sqlite_schema(con, _SUMMARY_REQUIRED_SCHEMA)
        has_system_metadata = 'system_metadata' in sqlite_table_columns(con, 'data')
        sys_col_sql = ', system_metadata' if has_system_metadata else ''
        for row in con.execute(
            f'''SELECT id, dataset_id, name, label, external_metadata, node_set,
                       pipeline_status, created_at, updated_at{sys_col_sql}
                FROM data
                WHERE dataset_id {ds_sql}
                ORDER BY created_at DESC, id ASC''',
            ds_params,
        ):
            doc_id = uuid_text(row[0])
            doc_ds = uuid_text(row[1])
            if not doc_id:
                raise PolicyError('documents_unavailable', 503)
            if doc_ds != expected_ds:
                raise PolicyError('document_identity_mismatch', 503)
            raw_meta = parse_json(row[4])
            meta = dict(raw_meta) if isinstance(raw_meta, dict) else {}
            raw_sys = parse_json(row[9]) if has_system_metadata else None
            sys_meta = dict(raw_sys) if isinstance(raw_sys, dict) and raw_sys else None
            ns = decode_node_set(row[5])
            if ns and not meta.get('node_set'):
                meta['node_set'] = ns
            if sys_meta is not None and ns and not sys_meta.get('node_set'):
                sys_meta['node_set'] = ns
            created_at = iso_text(row[7])
            updated_at = iso_text(row[8])
            trusted = sys_meta if sys_meta is not None else meta
            if is_lesson_metadata(trusted):
                resolved_day = resolve_lesson_date(
                    expected_ds,
                    doc_id,
                    system_metadata=sys_meta,
                    external_metadata=meta,
                    created_at=created_at,
                    doc_prov=doc_prov,
                    key_prov=key_prov,
                    include_created_fallback=False,
                )
                if resolved_day:
                    meta['first_source_date'] = resolved_day
            doc_obj = {
                'id': doc_id,
                'datasetId': dataset_id,
                'name': row[2] or '',
                'label': row[3],
                'createdAt': created_at,
                'updatedAt': updated_at,
                'externalMetadata': meta,
            }
            classified_doc = {**doc_obj, "externalMetadata": trusted}
            if is_knowledge_doc(classified_doc):
                knowledge_items.append(_knowledge_catalog_entry(
                    doc_id,
                    created_at,
                    dataset_id=expected_ds,
                    system_metadata=sys_meta,
                    external_metadata=meta,
                    doc_prov=doc_prov,
                    key_prov=key_prov,
                ))
                continue
            if meta.get('memlane_kind') == 'user_profile' or not _is_refinement_material(classified_doc):
                continue
            ps = parse_json(row[6])
            slots = ps.get('cognify_pipeline') if isinstance(ps, dict) else None
            slot_val = None
            if isinstance(slots, dict):
                slot_val = slots[dataset_id] if dataset_id in slots else slots.get(expected_ds)
            if not _is_data_item_completed(slot_val):
                pending.append({
                    'id': doc_id,
                    'title': _document_title(doc_obj),
                    'date': _document_date(doc_obj),
                    'kind': '文档',
                    'document': doc_obj,
                })

        for pr in con.execute(
            f'''SELECT id, pipeline_run_id, parent_operation_id, dataset_id,
                       pipeline_name, operation_name, status, outcome, background,
                       error_class, origin, created_at, started_at, ended_at
                FROM pipeline_runs
                WHERE dataset_id {ds_sql}
                ORDER BY created_at DESC, id DESC
                LIMIT 20''',
            ds_params,
        ):
            activity.append({
                'id': uuid_text(pr[0]),
                'pipeline_run_id': uuid_text(pr[1]),
                'parent_operation_id': uuid_text(pr[2]),
                'dataset_id': uuid_text(pr[3]) or dataset_id,
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
    except sqlite3.Error:
        return None, True
    finally:
        con.close()

    return (pending, knowledge_items, activity), False


async def _read_pending_documents(official, dataset_id, *, provenance_path=None):
    if official is None:
        raise PolicyError('documents_unavailable', 503)
    encoded = quote(dataset_id, safe='')
    status, first_page = await asyncio.gather(
        official.request('GET', f'/api/v1/datasets/{encoded}/processing-status?pipeline=cognify_pipeline'),
        official.request('GET', f'/api/v1/datasets/{encoded}/data?limit={_DATA_HTTP_PAGE_SIZE}'),
    )
    if not isinstance(status, dict) or not isinstance(status.get('items'), list):
        raise PolicyError('incomplete_processing_status', 503)
    if not isinstance(first_page, list):
        raise PolicyError('documents_unavailable', 503)

    all_docs = list(first_page)
    offset = _DATA_HTTP_PAGE_SIZE
    page = first_page
    while len(page) == _DATA_HTTP_PAGE_SIZE:
        if offset > _MAX_DATA_HTTP_OFFSET:
            raise PolicyError('documents_unavailable', 503)
        page = await official.request(
            'GET',
            f'/api/v1/datasets/{encoded}/data?limit={_DATA_HTTP_PAGE_SIZE}&offset={offset}',
        )
        if not isinstance(page, list):
            raise PolicyError('documents_unavailable', 503)
        all_docs.extend(page)
        offset += _DATA_HTTP_PAGE_SIZE

    expected_ds = uuid_text(dataset_id)
    doc_prov, key_prov = load_provenance_maps(provenance_path, {expected_ds})
    metadata = {}
    for doc in all_docs:
        if not isinstance(doc, dict):
            raise PolicyError('documents_unavailable', 503)
        doc_id = doc.get('id')
        doc_dataset = doc.get('datasetId')
        if not isinstance(doc_id, str) or not doc_id:
            raise PolicyError('documents_unavailable', 503)
        if doc_dataset is not None and doc_dataset != dataset_id:
            raise PolicyError('document_identity_mismatch', 503)
        if doc_id not in metadata:
            metadata[doc_id] = doc

    pending = []
    for row in status['items']:
        if not isinstance(row, dict):
            raise PolicyError('incomplete_processing_status', 503)
        completed = row.get('completed')
        if type(completed) is not bool:
            raise PolicyError('incomplete_processing_status', 503)
        if completed:
            continue
        doc = metadata.get(row.get('id'))
        if doc is None:
            raise PolicyError('document_catalog_mismatch', 503)
        if is_knowledge_doc(doc) or not _is_refinement_material(doc):
            continue
        pending.append({
            'id': doc['id'],
            'title': _document_title(doc),
            'date': _document_date(doc),
            'kind': '文档',
            'document': doc,
        })

    knowledge_items = [
        _knowledge_catalog_entry(
            doc_id,
            iso_text(doc.get('createdAt')),
            dataset_id=expected_ds,
            system_metadata=doc.get('systemMetadata') if isinstance(doc.get('systemMetadata'), dict) else None,
            external_metadata=doc.get('externalMetadata') if isinstance(doc.get('externalMetadata'), dict) else None,
            doc_prov=doc_prov,
            key_prov=key_prov,
        )
        for doc_id, doc in metadata.items()
        if is_knowledge_doc(doc)
    ]
    return pending, knowledge_items


async def _read_activity(official, dataset_id):
    if official is None:
        raise PolicyError('activity_unavailable', 503)
    encoded = quote(dataset_id, safe='')
    rows = await official.request('GET', f'/api/v1/activity/pipeline-runs?dataset_id={encoded}&limit=20')
    if not isinstance(rows, list):
        raise PolicyError('activity_unavailable', 503)
    result = []
    for row in rows:
        if not isinstance(row, dict):
            continue
        row_dataset = row.get('dataset_id')
        if row_dataset and row_dataset != dataset_id:
            continue
        result.append(row)
    return result


async def _read_documents_and_activity(official, official_data, dataset_id, *, provenance_path=None):
    sqlite_degraded = False
    if official_data:
        try:
            fast, sqlite_degraded = await asyncio.to_thread(
                _read_sqlite_documents_and_activity,
                official_data,
                dataset_id,
                provenance_path=provenance_path,
            )
        except PolicyError as exc:
            return exc, exc, False
        if fast is not None:
            pending_documents, knowledge_items, activity = fast
            return (pending_documents, knowledge_items), activity, False
    docs_res, activity_res = await asyncio.gather(
        _read_pending_documents(official, dataset_id, provenance_path=provenance_path),
        _read_activity(official, dataset_id),
        return_exceptions=True,
    )
    return docs_res, activity_res, sqlite_degraded


async def build_project_message_summary(
    registry,
    project_id,
    *,
    backlog=None,
    official_data=None,
    known_knowledge_version='',
):
    project = registry.state['projects'].get(project_id)
    if project is None:
        raise PolicyError('project_not_found', 404)
    dataset_id = project['dataset_id']
    official = getattr(registry, 'official', None)
    provenance_path = registry.path.parent / 'lesson-provenance.sqlite3' if getattr(registry, 'path', None) else None

    backlog_res, notices_res, docs_and_activity = await asyncio.gather(
        _read_backlog(backlog, project_id),
        read_processing_notices(registry, dataset_id, official_data=official_data),
        _read_documents_and_activity(official, official_data, dataset_id, provenance_path=provenance_path),
        return_exceptions=True,
    )

    if isinstance(docs_and_activity, asyncio.CancelledError):
        raise docs_and_activity
    sqlite_degraded = False
    if isinstance(docs_and_activity, BaseException):
        docs_res = activity_res = docs_and_activity
    else:
        docs_res, activity_res, sqlite_degraded = docs_and_activity

    # Fail closed with 503 when configured SQLite projection is missing/broken
    # and the control-plane official HTTP fallback is also unavailable.
    if sqlite_degraded and isinstance(docs_res, BaseException) and isinstance(activity_res, BaseException):
        raise PolicyError('message_summary_store_unavailable', 503)

    errors = {}
    if isinstance(backlog_res, BaseException):
        if isinstance(backlog_res, asyncio.CancelledError):
            raise backlog_res
        backlog_job, automatic_save, buffer_reviews, checks, processing_issues = None, False, [], [], []
        errors['backlog'] = _error_code(backlog_res, 'backlog_unavailable')
    else:
        backlog_job, automatic_save, buffer_reviews, checks, processing_issues = backlog_res

    if isinstance(notices_res, BaseException):
        if isinstance(notices_res, asyncio.CancelledError):
            raise notices_res
        notices, resolved_notices = [], []
        errors['processing_notices'] = _error_code(notices_res, 'processing_diagnostics_unavailable')
    else:
        notices, resolved_notices = notices_res

    if isinstance(docs_res, BaseException):
        if isinstance(docs_res, asyncio.CancelledError):
            raise docs_res
        pending_documents = []
        knowledge_version = ''
        knowledge_unchanged = False
        knowledge_catalog = None
        errors['documents'] = _error_code(docs_res, 'documents_unavailable')
    else:
        pending_documents, knowledge_items = docs_res
        knowledge_version = _knowledge_catalog_version(knowledge_items)
        knowledge_unchanged = bool(known_knowledge_version) and known_knowledge_version == knowledge_version
        knowledge_catalog = None if knowledge_unchanged else knowledge_items

    if isinstance(activity_res, BaseException):
        if isinstance(activity_res, asyncio.CancelledError):
            raise activity_res
        activity = []
        errors['activity'] = _error_code(activity_res, 'activity_unavailable')
    else:
        activity = activity_res

    return {
        'project_id': project_id,
        'dataset_id': dataset_id,
        'automatic_save': automatic_save,
        'backlog': backlog_job,
        'buffer_reviews': buffer_reviews,
        'checks': checks,
        'processing_issues': processing_issues,
        'processing_notices': notices,
        'resolved_processing_notices': resolved_notices,
        'pending_documents': pending_documents,
        'knowledge_catalog': knowledge_catalog,
        'knowledge_catalog_version': knowledge_version,
        'knowledge_catalog_unchanged': knowledge_unchanged,
        'activity': activity,
        'errors': errors,
    }


def attach_project_message_summary(app, registry, *, backlog=None, codex_config=None):
    official_data = (codex_config or {}).get('official_data')

    async def message_summary(request):
        if not request['manager']:
            raise PolicyError('management_required', 403)
        if set(request.query) - {'knowledge_version'}:
            raise PolicyError('invalid_summary_query', 400)
        known_version = (request.query.get('knowledge_version') or '').strip()
        project_id = request.match_info['id']
        summary = await build_project_message_summary(
            registry,
            project_id,
            backlog=backlog,
            official_data=official_data,
            known_knowledge_version=known_version,
        )
        return web.json_response(summary)

    app.add_routes([web.get('/v1/projects/{id}/message-summary', message_summary)])
