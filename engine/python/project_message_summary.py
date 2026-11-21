"""Read-only project message summary aggregation for desktop queue and notices."""
import asyncio
import inspect
import re
from urllib.parse import quote

from aiohttp import web

from processing_notices import processing_notices
from project_policy import PolicyError

_MACHINE_NAME_RE = re.compile(
    r'^(?:text_[0-9a-fA-F]{32}(?:\.(?:md|txt))?'
    r'|(?:user_context|memory)-[0-9a-fA-F]{32}(?:\.txt)?'
    r'|[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})$'
)
_KNOWLEDGE_NODE_SETS = frozenset({'session_learnings', 'user_context', 'memory'})
_DERIVED_NODE_SETS = frozenset({'user_sessions_from_cache', 'agent_trace_feedbacks', 'session_learnings'})


def _node_sets(doc):
    meta = doc.get('externalMetadata')
    if not isinstance(meta, dict):
        return []
    raw = meta.get('node_set')
    if isinstance(raw, str):
        return [raw]
    if isinstance(raw, list):
        return [item for item in raw if isinstance(item, str)]
    return []


def _is_knowledge_doc(doc):
    meta = doc.get('externalMetadata')
    if isinstance(meta, dict) and str(meta.get('common_category') or '').strip():
        return True
    return any(tag in _KNOWLEDGE_NODE_SETS for tag in _node_sets(doc))


def _is_derived_material(doc):
    return any(tag in _DERIVED_NODE_SETS for tag in _node_sets(doc))


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


async def _read_processing_notices(registry, dataset_id):
    try:
        items = await asyncio.to_thread(
            processing_notices,
            registry.path.parent / 'runtime-logs',
            dataset_id,
        )
    except OSError:
        raise PolicyError('processing_diagnostics_unavailable', 503) from None
    if not isinstance(items, list):
        raise PolicyError('processing_diagnostics_unavailable', 503)
    return items


async def _read_pending_documents(official, dataset_id):
    if official is None:
        raise PolicyError('documents_unavailable', 503)
    encoded = quote(dataset_id, safe='')
    status, docs = await asyncio.gather(
        official.request('GET', f'/api/v1/datasets/{encoded}/processing-status?pipeline=cognify_pipeline'),
        official.request('GET', f'/api/v1/datasets/{encoded}/data?limit=500'),
    )
    if not isinstance(status, dict) or not isinstance(status.get('items'), list):
        raise PolicyError('incomplete_processing_status', 503)
    if not isinstance(docs, list):
        raise PolicyError('documents_unavailable', 503)

    metadata = {}
    for doc in docs:
        if not isinstance(doc, dict):
            raise PolicyError('documents_unavailable', 503)
        doc_id = doc.get('id')
        doc_dataset = doc.get('datasetId')
        if not isinstance(doc_id, str) or not doc_id:
            raise PolicyError('documents_unavailable', 503)
        if doc_dataset is not None and doc_dataset != dataset_id:
            raise PolicyError('document_identity_mismatch', 503)
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
        if _is_knowledge_doc(doc) or _is_derived_material(doc):
            continue
        pending.append({
            'id': doc['id'],
            'title': _document_title(doc),
            'date': _document_date(doc),
            'kind': '文档',
            'document': doc,
        })
    return pending


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


async def build_project_message_summary(registry, project_id, *, backlog=None):
    project = registry.state['projects'].get(project_id)
    if project is None:
        raise PolicyError('project_not_found', 404)
    dataset_id = project['dataset_id']
    official = getattr(registry, 'official', None)

    backlog_res, notices_res, docs_res, activity_res = await asyncio.gather(
        _read_backlog(backlog, project_id),
        _read_processing_notices(registry, dataset_id),
        _read_pending_documents(official, dataset_id),
        _read_activity(official, dataset_id),
        return_exceptions=True,
    )

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
        notices = []
        errors['processing_notices'] = _error_code(notices_res, 'processing_diagnostics_unavailable')
    else:
        notices = notices_res

    if isinstance(docs_res, BaseException):
        if isinstance(docs_res, asyncio.CancelledError):
            raise docs_res
        pending_documents = []
        errors['documents'] = _error_code(docs_res, 'documents_unavailable')
    else:
        pending_documents = docs_res

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
        'pending_documents': pending_documents,
        'activity': activity,
        'errors': errors,
    }


def attach_project_message_summary(app, registry, *, backlog=None):
    async def message_summary(request):
        if not request['manager']:
            raise PolicyError('management_required', 403)
        project_id = request.match_info['id']
        summary = await build_project_message_summary(registry, project_id, backlog=backlog)
        return web.json_response(summary)

    app.add_routes([web.get('/v1/projects/{id}/message-summary', message_summary)])
