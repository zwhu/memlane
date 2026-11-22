"""Phase 0 capability negotiation and O(1) / O(log N) read-only /v2 lookup endpoints.

Serves ``/v1/capabilities``, ``/v2/datasets/{ds}/documents/{doc}``,
``/v2/datasets/{ds}/sessions/{sid}``, and ``/v2/projects/{id}/code-activity``
using ``sqlite_ro.open_official_sqlite_ro`` with Fail-Closed ACL enforcement.
"""
import asyncio
from pathlib import Path
import sqlite3

from aiohttp import web

from local_body_reader import json_utf8_response, verify_official_read_access
from project_policy import PolicyError
from provenance_store import _calendar_date, _is_lesson_metadata, _local_calendar_date
from sqlite_ro import (
    common_category_from_meta,
    extract_trusted_metadata,
    id_variants,
    is_valid_uuid,
    iso_text,
    knowledge_category_label,
    official_sqlite_ro,
    parse_official_timestamp,
    ro_snapshot,
    unique_learning_session,
    uuid_text,
)

WAVE1_API_VERSIONS = ('v1', 'v2')
WAVE1_FEATURES = (
    'document_lookup_v2',
    'session_lookup_v2',
    'code_activity_v2',
    'tool_graph_batched_v2',
    'catalog_index_v2',
    'reading_state_v2',
    'local_body_fastpath_v2',
    'overview_v2',
    'knowledge_cursor_v2',
    'common_knowledge_cursor_v2',
    'raw_records_v2',
    'search_v2',
    'message_summary_v2',
    'provenance_backfill_v2',
)

_ACTIVE_PIPELINE_STATUSES = frozenset({'DATASET_PROCESSING_STARTED', 'DATASET_PROCESSING_INITIATED'})
_CODE_ACTIVITY_SCAN_LIMIT = 200


def _safe_session_id(value) -> bool:
    if not isinstance(value, str) or not 1 <= len(value) <= 256:
        return False
    if '/' in value or '\\' in value or '..' in value:
        return False
    return not any(ord(ch) < 32 or ord(ch) == 127 for ch in value)


def _table_columns(con, table: str) -> set[str]:
    return {str(row[1]) for row in con.execute(f'PRAGMA table_info({table})').fetchall()}


def _require_verified_managed_dataset(registry, raw_dataset, *, allow_code: bool = True) -> str:
    """Verify dataset belongs to a managed group and Fail-Closed reject ``unconfirmed`` groups."""
    if not is_valid_uuid(raw_dataset):
        raise PolicyError('dataset_access_denied', 403)
    ds_id = uuid_text(raw_dataset)
    matched_group = None
    for group in registry.managed_groups():
        if group.get('dataset_id') == ds_id:
            matched_group = group
            break
        if allow_code and group.get('code_enabled') and group.get('code_dataset_id') == ds_id:
            matched_group = group
            break
    if matched_group is None:
        raise PolicyError('dataset_access_denied', 403)
    if matched_group.get('status') != 'verified':
        raise PolicyError('project_unconfirmed', 403)
    return ds_id


def _lookup_provenance_date(provenance_path: Path, dataset_id: str, doc_id: str, meta: dict) -> str:
    if not provenance_path or not provenance_path.is_file() or provenance_path.is_symlink():
        return ''
    lt = str(meta.get('lesson_type') or '').strip()
    sk = str(meta.get('state_key') or '').strip()
    dates = []
    try:
        with official_sqlite_ro(provenance_path) as con:
            with ro_snapshot(con):
                doc_row = con.execute(
                    '''SELECT lesson_type, state_key, first_source_date
                       FROM document_provenance
                       WHERE dataset_id = ? AND document_id = ?''',
                    (dataset_id, doc_id),
                ).fetchone()
                if doc_row is not None:
                    if not lt and doc_row[0]:
                        lt = str(doc_row[0]).strip()
                    if not sk and doc_row[1]:
                        sk = str(doc_row[1]).strip()
                    if _calendar_date(doc_row[2]):
                        dates.append(doc_row[2])
                if lt and sk:
                    key_row = con.execute(
                        '''SELECT first_source_date
                           FROM keyed_provenance
                           WHERE dataset_id = ? AND lesson_type = ? AND state_key = ?''',
                        (dataset_id, lt, sk),
                    ).fetchone()
                    if key_row is not None and _calendar_date(key_row[0]):
                        dates.append(key_row[0])
    except (PolicyError, sqlite3.Error):
        return ''
    return min(dates) if dates else ''


def _effective_date(meta: dict, created_at: str, *, prov_date: str = '') -> str:
    """Exact 1:1 precedence with ``catalog_index._project_cognee_row``:
    provenance > meta.first_source_date > meta.session_date > local(created_at).
    """
    if _calendar_date(prov_date):
        return prov_date
    for key in ('first_source_date', 'session_date'):
        day = _calendar_date(meta.get(key))
        if day:
            return day
    return _local_calendar_date(created_at) or ''


def _read_single_document(official_data, provenance_path, dataset_id: str, doc_id: str) -> dict:
    db_path = Path(official_data) / 'system' / 'databases' / 'cognee_db'
    doc_variants = id_variants(doc_id)
    with official_sqlite_ro(db_path) as con:
        with ro_snapshot(con):
            cols = _table_columns(con, 'data')
            has_sys = 'system_metadata' in cols
            has_legacy = 'legacy_id' in cols
            sys_col = 'system_metadata' if has_sys else 'NULL'
            select_sql = (
                f'SELECT id, dataset_id, name, label, mime_type, external_metadata, '
                f'{sys_col}, node_set, updated_at, created_at, content_hash '
                f'FROM data WHERE '
            )
            row = None
            for variant in doc_variants:
                row = con.execute(select_sql + 'id = ? LIMIT 1', (variant,)).fetchone()
                if row is not None:
                    break
            if row is None and has_legacy:
                for variant in doc_variants:
                    row = con.execute(select_sql + 'legacy_id = ? LIMIT 1', (variant,)).fetchone()
                    if row is not None:
                        break

    if row is None:
        raise PolicyError('document_not_found', 404)
    row_ds = uuid_text(row[1])
    if row_ds != dataset_id:
        raise PolicyError('document_not_found', 404)

    canonical_id = uuid_text(row[0])
    if not canonical_id:
        raise PolicyError('official_store_unavailable', 503)

    meta = extract_trusted_metadata(row[6], row[5], row[7])
    updated_at = iso_text(row[8])
    created_at = iso_text(row[9])

    prov_date = ''
    if _is_lesson_metadata(meta):
        prov_date = _lookup_provenance_date(provenance_path, dataset_id, canonical_id, meta)
        eff_day = _effective_date(meta, created_at, prov_date=prov_date)
        if eff_day:
            meta['first_source_date'] = eff_day
    else:
        eff_day = _effective_date(meta, created_at)

    return {
        'id': canonical_id,
        'datasetId': dataset_id,
        'name': row[2] or '',
        'label': row[3],
        'mimeType': row[4] or '',
        'externalMetadata': meta,
        'updatedAt': updated_at,
        'createdAt': created_at,
        'contentHash': row[10] or '',
        'effective_date': eff_day,
        'category': knowledge_category_label(meta),
        'common_category': common_category_from_meta(meta),
        'session_id': unique_learning_session(meta),
        'recall_count': 0,
    }


def _read_single_session(official_data, dataset_id: str, session_id: str, *, user_id: str = '') -> dict:
    db_path = Path(official_data) / 'system' / 'databases' / 'cognee_db'
    ds_variants = id_variants(dataset_id)
    user_variants = id_variants(user_id) if user_id else ()

    with official_sqlite_ro(db_path) as con:
        with ro_snapshot(con):
            cols = _table_columns(con, 'session_records')
            status_col = 'status' if 'status' in cols else "''"
            row = None
            if user_variants:
                for u_var in user_variants:
                    for ds_var in ds_variants:
                        row = con.execute(
                            f'''SELECT session_id, user_id, dataset_id, agent_id,
                                       last_activity_at, started_at, {status_col}
                                FROM session_records
                                WHERE session_id = ? AND user_id = ? AND dataset_id = ?
                                LIMIT 1''',
                            (session_id, u_var, ds_var),
                        ).fetchone()
                        if row is not None:
                            break
                    if row is not None:
                        break
            else:
                candidates = []
                for ds_var in ds_variants:
                    found = con.execute(
                        f'''SELECT session_id, user_id, dataset_id, agent_id,
                                   last_activity_at, started_at, {status_col}
                            FROM session_records
                            WHERE session_id = ? AND dataset_id = ?
                            ORDER BY last_activity_at DESC
                            LIMIT 1''',
                        (session_id, ds_var),
                    ).fetchone()
                    if found is not None:
                        candidates.append(found)
                if candidates:
                    candidates.sort(key=lambda r: str(r[4] or ''), reverse=True)
                    row = candidates[0]

    if row is None:
        raise PolicyError('session_not_found', 404)
    return {
        'session_id': str(row[0] or ''),
        'dataset_id': dataset_id,
        'user_id': uuid_text(row[1]),
        'agent_id': str(row[3] or ''),
        'last_activity_at': iso_text(row[4]),
        'started_at': iso_text(row[5]),
        'status': str(row[6] or ''),
    }


def _read_code_activity(official_data, project_id: str, code_dataset_id: str) -> dict:
    """Bounded O(log N + K) code-activity check using upstream composite index
    ``ix_pipeline_runs_dataset_pipeline_created_at(dataset_id, pipeline_name, created_at)``.
    """
    db_path = Path(official_data) / 'system' / 'databases' / 'cognee_db'
    ds_variants = id_variants(code_dataset_id)

    with official_sqlite_ro(db_path) as con:
        with ro_snapshot(con):
            ds_row = None
            for variant in ds_variants:
                ds_row = con.execute(
                    'SELECT id, created_at FROM datasets WHERE id = ? LIMIT 1',
                    (variant,),
                ).fetchone()
                if ds_row is not None:
                    break
            if ds_row is None:
                raise PolicyError('code_dataset_not_found', 404)

            dataset_created_at = iso_text(ds_row[1])
            generation_dt = parse_official_timestamp(dataset_created_at)
            if generation_dt is None:
                raise PolicyError('code_dataset_generation_invalid', 503)

            raw_rows = []
            seen_raw_ids = set()

            def _add_rows(rows):
                for row in rows:
                    row_id = uuid_text(row[0])
                    if row_id in seen_raw_ids:
                        continue
                    seen_raw_ids.add(row_id)
                    raw_rows.append(row)

            for variant in ds_variants:
                # 1. Bounded recent window per indexed branch (hits ix_pipeline_runs_dataset_pipeline_created_at, 0 TEMP B-TREE)
                pipe_rows = con.execute(
                    '''SELECT id, pipeline_run_id, dataset_id, pipeline_name, operation_name,
                              status, outcome, created_at, started_at, ended_at
                       FROM pipeline_runs
                       WHERE dataset_id = ? AND pipeline_name = 'code_graph_pipeline'
                       ORDER BY created_at DESC
                       LIMIT ?''',
                    (variant, _CODE_ACTIVITY_SCAN_LIMIT),
                ).fetchall()
                _add_rows(pipe_rows)
                if len(pipe_rows) == _CODE_ACTIVITY_SCAN_LIMIT:
                    oldest_cut = pipe_rows[-1][7]
                    older_active = con.execute(
                        '''SELECT id, pipeline_run_id, dataset_id, pipeline_name, operation_name,
                                  status, outcome, created_at, started_at, ended_at
                           FROM pipeline_runs r1
                           WHERE r1.dataset_id = ? AND r1.pipeline_name = 'code_graph_pipeline'
                             AND r1.created_at <= ?
                             AND r1.status IN ('DATASET_PROCESSING_STARTED', 'DATASET_PROCESSING_INITIATED')
                             AND NOT EXISTS (
                               SELECT 1 FROM pipeline_runs r2
                               WHERE r2.dataset_id = r1.dataset_id
                                 AND r2.pipeline_name = 'code_graph_pipeline'
                                 AND r2.pipeline_run_id = r1.pipeline_run_id
                                 AND r2.status NOT IN ('DATASET_PROCESSING_STARTED', 'DATASET_PROCESSING_INITIATED')
                             )
                           ORDER BY r1.created_at DESC
                           LIMIT 16''',
                        (variant, oldest_cut),
                    ).fetchall()
                    _add_rows(older_active)

                for op_sql in (
                    '''SELECT id, pipeline_run_id, dataset_id, pipeline_name, operation_name,
                              status, outcome, created_at, started_at, ended_at
                       FROM pipeline_runs
                       WHERE dataset_id = ? AND pipeline_name IS NULL AND operation_name = 'remember'
                       ORDER BY created_at DESC
                       LIMIT ?''',
                    '''SELECT id, pipeline_run_id, dataset_id, pipeline_name, operation_name,
                              status, outcome, created_at, started_at, ended_at
                       FROM pipeline_runs
                       WHERE dataset_id = ? AND pipeline_name = '' AND operation_name = 'remember'
                       ORDER BY created_at DESC
                       LIMIT ?''',
                ):
                    op_rows = con.execute(op_sql, (variant, _CODE_ACTIVITY_SCAN_LIMIT)).fetchall()
                    _add_rows(op_rows)

    parsed_rows = []
    for row in raw_rows:
        row_ds = uuid_text(row[2])
        if row_ds != code_dataset_id:
            raise PolicyError('code_activity_dataset_mismatch', 503)
        created_iso = iso_text(row[7])
        started_iso = iso_text(row[8])
        created_dt = parse_official_timestamp(created_iso)
        if created_dt is None:
            raise PolicyError('code_activity_timestamp_invalid', 503)
        started_dt = parse_official_timestamp(started_iso) if started_iso else None
        if started_iso and started_dt is None:
            raise PolicyError('code_activity_timestamp_invalid', 503)
        # Exact equivalence to graph.go:codeRunPredatesGeneration
        if started_dt is None:
            predates = created_dt < generation_dt
        else:
            predates = created_dt < generation_dt and started_dt < generation_dt
        if predates:
            continue
        parsed_rows.append({
            'id': uuid_text(row[0]),
            'run_id': uuid_text(row[1]),
            'kind': 'pipeline' if row[3] else 'operation',
            'pipeline_name': str(row[3] or ''),
            'operation_name': str(row[4] or ''),
            'status': str(row[5] or ''),
            'outcome': str(row[6] or ''),
            'created_at': created_iso,
            'started_at': started_iso,
            'ended_at': iso_text(row[9]),
            '_created_dt': created_dt,
        })

    parsed_rows.sort(key=lambda item: (item['_created_dt'], item['id']), reverse=True)

    active = False
    status = ''
    run_id = ''
    latest_created_at = ''
    latest_started_at = ''
    latest_ended_at = ''
    seen_runs = set()

    for item in parsed_rows:
        key = item['run_id'] or item['id']
        if key in seen_runs:
            continue
        seen_runs.add(key)
        if not run_id and not status:
            run_id = item['run_id']
            status = item['status'] or item['outcome']
            latest_created_at = item['created_at']
            latest_started_at = item['started_at']
            latest_ended_at = item['ended_at']
        is_active = (
            item['status'] in _ACTIVE_PIPELINE_STATUSES
            or (item['kind'] == 'operation' and not item['outcome'] and not item['ended_at'])
        )
        if is_active:
            active = True
            run_id = item['run_id']
            status = 'running'
            latest_created_at = item['created_at']
            latest_started_at = item['started_at']
            latest_ended_at = item['ended_at']

    return {
        'project_id': project_id,
        'code_dataset_id': code_dataset_id,
        'dataset_created_at': dataset_created_at,
        'active': active,
        'status': status,
        'run_id': run_id,
        'latest_created_at': latest_created_at,
        'latest_started_at': latest_started_at,
        'latest_ended_at': latest_ended_at,
    }


def attach_v2_lookups(
    app,
    registry,
    *,
    recall_store=None,
    codex_config=None,
    features=WAVE1_FEATURES,
):
    official_data = (codex_config or {}).get('official_data')
    provenance_path = registry.path.parent / 'lesson-provenance.sqlite3'
    feature_list = list(features)

    async def capabilities(request):
        if not request['manager']:
            raise PolicyError('management_required', 403)
        if request.query:
            raise PolicyError('invalid_request_fields')
        return json_utf8_response({
            'api_versions': list(WAVE1_API_VERSIONS),
            'features': feature_list,
        })

    async def document_lookup(request):
        if not request['manager']:
            raise PolicyError('management_required', 403)
        if request.query:
            raise PolicyError('invalid_request_fields')
        ds_id = _require_verified_managed_dataset(registry, request.match_info['ds'], allow_code=True)
        raw_doc = request.match_info['doc']
        if not is_valid_uuid(raw_doc):
            raise PolicyError('invalid_document_id')
        if not official_data:
            raise PolicyError('official_store_unavailable', 503)
        await verify_official_read_access(getattr(registry, 'official', None), ds_id)

        doc = await asyncio.to_thread(
            _read_single_document,
            official_data,
            provenance_path,
            ds_id,
            raw_doc,
        )
        if recall_store is not None:
            try:
                stats = await asyncio.to_thread(recall_store.stats, ds_id, doc['id'])
                doc['recall_count'] = int(stats.get('count') or 0)
            except Exception:
                doc['recall_count'] = 0
        return json_utf8_response(doc)

    async def session_lookup(request):
        if not request['manager']:
            raise PolicyError('management_required', 403)
        if set(request.query) - {'user_id'}:
            raise PolicyError('invalid_request_fields')
        ds_id = _require_verified_managed_dataset(registry, request.match_info['ds'], allow_code=False)
        session_id = request.match_info['sid']
        if not _safe_session_id(session_id):
            raise PolicyError('invalid_session_id')
        user_id = (request.query.get('user_id') or '').strip()
        if user_id and not is_valid_uuid(user_id):
            raise PolicyError('invalid_user_id')
        if not official_data:
            raise PolicyError('official_store_unavailable', 503)
        await verify_official_read_access(getattr(registry, 'official', None), ds_id)

        session_obj = await asyncio.to_thread(
            _read_single_session,
            official_data,
            ds_id,
            session_id,
            user_id=user_id,
        )
        return json_utf8_response(session_obj)

    async def code_activity(request):
        if not request['manager']:
            raise PolicyError('management_required', 403)
        if request.query:
            raise PolicyError('invalid_request_fields')
        project_id = request.match_info['id']
        project = registry.state['projects'].get(project_id)
        if project is None:
            raise PolicyError('project_not_found', 404)
        if project.get('status') != 'verified':
            raise PolicyError('project_unconfirmed', 403)
        code_ds = project.get('code_dataset_id')
        if not project.get('code_enabled') or not code_ds or not is_valid_uuid(str(code_ds)):
            raise PolicyError('code_dataset_not_configured', 409)
        if not official_data:
            raise PolicyError('official_store_unavailable', 503)
        code_ds_canon = uuid_text(code_ds)
        await verify_official_read_access(getattr(registry, 'official', None), code_ds_canon)

        result = await asyncio.to_thread(
            _read_code_activity,
            official_data,
            project_id,
            code_ds_canon,
        )
        return json_utf8_response(result)

    app.add_routes([
        web.get('/v1/capabilities', capabilities),
        web.get('/v2/datasets/{ds}/documents/{doc}', document_lookup),
        web.get('/v2/datasets/{ds}/sessions/{sid}', session_lookup),
        web.get('/v2/projects/{id}/code-activity', code_activity),
    ])
