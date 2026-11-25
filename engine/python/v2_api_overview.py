"""Overview, day browsing, and project message summaries."""
import asyncio
import sqlite3

from local_body_reader import (
    fetch_authorized_official_datasets,
    read_local_preview_snippet,
    verify_official_read_access,
)
from processing_notices import read_processing_notices
from project_policy import PolicyError, project_capture_fields
from v2_api_context import (
    PayloadBuilder,
    PayloadTrimmer,
    V2ApiContext,
    _filter_digest,
    _format_knowledge_item,
    _knowledge_title,
    _read_backlog_slim,
    _read_recall_events_summary,
    _read_recent_pipeline_runs,
    _readable_name,
    _require_verified_project,
)


async def execute_overview_bootstrap(
    ctx: V2ApiContext,
    *,
    days: int,
    recent_limit: int,
    activity_limit: int,
    recall_limit: int,
    identity: str,
) -> tuple[PayloadBuilder, list[dict], PayloadTrimmer]:
    official_data = ctx.official_data
    registry = ctx.registry
    recall_store = ctx.recall_store
    cat = ctx.cat
    read_store = ctx.read_store
    pipeline_runs_cache = ctx.pipeline_runs_cache
    pipeline_runs_lock = ctx.pipeline_runs_lock

    authorized_ds = await fetch_authorized_official_datasets(getattr(registry, 'official', None))
    projects, common = await ctx.ensure_synced()
    scope: dict = {}
    ctx.populate_authorized_scope(scope, authorized_ds, projects, common, include_runs=True)

    def _query_overview_snap(_gen):
        cur_projects = scope['projects']
        cur_common = scope['common']
        cur_allowed_ds = scope['allowed_ds']
        meta = cat.meta()
        sync_map = cat.all_dataset_sync_info(cur_allowed_ds)
        day_buckets = cat.overview_day_buckets(cur_allowed_ds, days=days)
        recent_raw = cat.query_overview_recent(cur_allowed_ds, limit=recent_limit)

        proj_summaries = []
        proj_unread_sets: dict[str, set[str]] = {}
        total_knowledge = 0
        total_raw_count = 0
        for proj in cur_projects:
            ds = proj['dataset_id']
            sinfo = sync_map.get(ds) or {}
            k_cnt = int(sinfo.get('knowledge_count', 0))
            raw_cnt = int(sinfo.get('raw_doc_count', 0))
            sess_cnt = int(sinfo.get('session_count', 0))
            total_knowledge += k_cnt
            total_raw_count += sess_cnt + raw_cnt
            if identity:
                u_ids, u_rev = read_store.compute_unread_snapshot(identity, proj['id'], ds, cat)
                proj_unread_sets[ds] = u_ids
                unread_cnt = len(u_ids)
            else:
                unread_cnt = 0
                u_rev = 0
            proj_summaries.append({
                'project_id': proj['id'],
                'dataset_id': ds,
                **project_capture_fields(proj),
                'knowledge_count': k_cnt,
                'raw_doc_count': raw_cnt,
                'session_count': sess_cnt,
                'unread_count': unread_cnt,
                'unread_state_rev': u_rev,
                'catalog_version': str(sinfo.get('catalog_version', '')),
            })

        common_summary = None
        if cur_common is not None:
            cds = cur_common['dataset_id']
            cinfo = sync_map.get(cds) or {}
            ck_cnt = int(cinfo.get('knowledge_count', 0))
            c_raw_cnt = int(cinfo.get('raw_doc_count', 0))
            total_knowledge += ck_cnt
            total_raw_count += c_raw_cnt
            common_summary = {
                'dataset_id': cds,
                'knowledge_count': ck_cnt,
                'raw_doc_count': c_raw_cnt,
                'catalog_version': str(cinfo.get('catalog_version', '')),
            }
        return (
            meta,
            total_knowledge,
            total_raw_count,
            day_buckets,
            proj_summaries,
            common_summary,
            recent_raw,
            proj_unread_sets,
        )

    def _build_sync():
        cur_ds_to_proj = scope['ds_to_proj']
        cur_allowed_run_ds = scope['allowed_run_ds']
        cur_allowed_ds_set = scope['allowed_ds_set']
        cur_agents = scope['agents_snapshot']
        (
            meta,
            total_knowledge,
            total_raw_count,
            day_buckets,
            proj_summaries,
            common_summary,
            recent_raw,
            proj_unread_sets,
        ) = ctx.query_catalog_snapshot(_query_overview_snap)
        recent_items = [
            _format_knowledge_item(
                d,
                project_id=cur_ds_to_proj.get(d['dataset_id'], ''),
                is_unread=(d['doc_id'] in proj_unread_sets.get(d['dataset_id'], ())) if identity else False,
                official_data=official_data,
            )
            for d in recent_raw
        ]
        try:
            runs, runs_more = _read_recent_pipeline_runs(
                official_data,
                allowed_datasets=cur_allowed_run_ds,
                limit=activity_limit,
                cache=pipeline_runs_cache,
                cache_lock=pipeline_runs_lock,
            )
        except (PolicyError, sqlite3.Error):
            runs, runs_more = [], False
        rec_events, rec_more, rec_started = _read_recall_events_summary(
            recall_store,
            cur_allowed_ds_set,
            cur_agents,
            recall_limit,
        )
        return (
            meta,
            total_knowledge,
            total_raw_count,
            day_buckets,
            proj_summaries,
            common_summary,
            recent_items,
            runs,
            runs_more,
            rec_events,
            rec_more,
            rec_started,
        )

    (
        meta,
        total_knowledge,
        total_raw_count,
        day_buckets,
        proj_summaries,
        common_summary,
        recent_items,
        runs,
        runs_more,
        rec_events,
        rec_more,
        rec_started,
    ) = await ctx.run_read_query(
        _build_sync,
        revalidate=ctx.multi_scope_revalidator(scope, include_runs=True),
    )
    total_rec_hits = sum(len(ev.get('hits') or ()) for ev in rec_events)
    if total_rec_hits > 100:
        capped_events = []
        for ev in rec_events:
            ev_copy = dict(ev)
            if isinstance(ev_copy.get('hits'), list) and len(ev_copy['hits']) > 5:
                ev_copy['hits'] = ev_copy['hits'][:5]
                rec_more = True
            capped_events.append(ev_copy)
        rec_events = capped_events

    def _make_payload(items_subset, _lo, _hi):
        return {
            'generation': meta['generation'],
            'local_tz_name': meta['local_tz_name'],
            'total_knowledge_count': total_knowledge,
            'total_raw_count': total_raw_count,
            'day_buckets': list(day_buckets),
            'projects': proj_summaries,
            'common': common_summary,
            'recent_items': items_subset,
            'pipeline_runs': list(runs),
            'pipeline_runs_more': runs_more,
            'recall_events': [dict(ev) for ev in rec_events],
            'recall_events_more': rec_more,
            'recall_started_at': rec_started,
        }

    def _trim_overview(p: dict) -> bool:
        # 1. Trim verbose hit arrays on recall_events first
        shrunk_hits = False
        for idx, ev in enumerate(p['recall_events']):
            if isinstance(ev.get('hits'), list) and len(ev['hits']) > 5:
                ev_copy = dict(ev)
                ev_copy['hits'] = ev_copy['hits'][:5]
                p['recall_events'][idx] = ev_copy
                p['recall_events_more'] = True
                shrunk_hits = True
        if shrunk_hits:
            return True
        if p['recall_events']:
            p['recall_events'].pop()
            p['recall_events_more'] = True
            return True
        if p['pipeline_runs']:
            p['pipeline_runs'].pop()
            p['pipeline_runs_more'] = True
            return True
        if p['day_buckets']:
            p['day_buckets'].pop()
            return True
        if p['recent_items']:
            p['recent_items'].pop()
            return True
        return False

    return _make_payload, recent_items, _trim_overview


async def execute_overview_day(
    ctx: V2ApiContext,
    *,
    day: str,
    limit: int,
    raw_cursor: str,
    identity: str,
) -> tuple[PayloadBuilder, list[dict]]:
    official_data = ctx.official_data
    registry = ctx.registry
    cat = ctx.cat
    cursors = ctx.cursors

    f_digest = _filter_digest(day=day)
    after_key = None
    if raw_cursor:
        keys = cursors.decode(
            raw_cursor,
            endpoint='overview_day',
            dataset_id='*',
            sort_mode='day',
            filter_digest=f_digest,
        )
        if len(keys) != 2:
            raise PolicyError('invalid_cursor', 400)
        after_key = (str(keys[0]), str(keys[1]))

    authorized_ds = await fetch_authorized_official_datasets(getattr(registry, 'official', None))
    projects, common = await ctx.ensure_synced()
    scope: dict = {}
    ctx.populate_authorized_scope(scope, authorized_ds, projects, common)

    def _query_day_snap(gen):
        cur_allowed_ds = scope['allowed_ds']
        cur_ds_to_proj = scope['ds_to_proj']
        rows, has_more, total_count = cat.query_overview_day(cur_allowed_ds, day, limit=limit, after_key=after_key)
        unread_flags = ctx.page_unread_flags_by_dataset(identity, rows, cur_ds_to_proj)
        return gen, rows, unread_flags, has_more, total_count

    def _query():
        cur_ds_to_proj = scope['ds_to_proj']
        gen, rows, unread_flags, has_more, total_count = ctx.query_catalog_snapshot(_query_day_snap)
        formatted = [
            _format_knowledge_item(
                d,
                project_id=cur_ds_to_proj.get(d['dataset_id'], ''),
                is_unread=unread_flags.get((d['dataset_id'], d['doc_id']), False),
                official_data=official_data,
            )
            for d in rows
        ]
        return gen, formatted, has_more, total_count

    gen, formatted, base_has_more, total_count = await ctx.run_read_query(
        _query,
        revalidate=ctx.multi_scope_revalidator(scope),
    )

    def _make_payload(subset, _lo, hi):
        eff_more = bool(base_has_more or hi < len(formatted))
        nxt = ''
        if eff_more and subset:
            last = subset[-1]
            nxt = cursors.encode(
                endpoint='overview_day',
                dataset_id='*',
                sort_mode='day',
                filter_digest=f_digest,
                generation_hint=gen,
                keys=[last['dataset_id'], last['doc_id']],
            )
        return {
            'day': day,
            'generation': gen,
            'total_count': total_count,
            'items': subset,
            'next_cursor': nxt,
            'has_more': eff_more,
        }

    return _make_payload, formatted


async def execute_message_summary(
    ctx: V2ApiContext,
    *,
    project_id: str,
    identity: str,
) -> tuple[PayloadBuilder, list[dict], PayloadTrimmer]:
    official_data = ctx.official_data
    registry = ctx.registry
    cat = ctx.cat
    read_store = ctx.read_store
    pipeline_runs_cache = ctx.pipeline_runs_cache
    pipeline_runs_lock = ctx.pipeline_runs_lock
    handles = ctx.handles

    project = _require_verified_project(registry, project_id)
    dataset_id = project['dataset_id']
    await verify_official_read_access(getattr(registry, 'official', None), dataset_id)

    errors = {}
    backlog_job = None
    automatic_save = False
    notices = []
    buffer_reviews, checks, processing_issues = [], [], []

    async def _load_backlog():
        return await _read_backlog_slim(handles.backlog, project_id, project.get('auto_capture', True))

    async def _load_db_sections():
        sync_err = None
        try:
            await ctx.ensure_synced()
        except BaseException as exc:
            if isinstance(exc, asyncio.CancelledError):
                raise
            sync_err = exc

        def _run_all():
            a_val = ([], False)
            a_err = None
            try:
                a_val = _read_recent_pipeline_runs(
                    official_data,
                    dataset_id=dataset_id,
                    limit=20,
                    cache=pipeline_runs_cache,
                    cache_lock=pipeline_runs_lock,
                )
            except Exception as exc:
                a_err = exc

            if sync_err is not None:
                return a_val, a_err, None, sync_err

            try:
                snap = ctx.query_catalog_snapshot(_query)
            except Exception as exc:
                return a_val, a_err, None, exc
            (
                cat_ver_l,
                pending_total_l,
                pending_docs_l,
                unread_count_l,
                unread_state_rev_l,
                top_docs_l,
                reading_err_l,
            ) = snap
            unread_preview_l = []
            if reading_err_l is None and top_docs_l:
                try:
                    for d in top_docs_l:
                        preview_snip = ''
                        if (
                            official_data
                            and d.get('raw_data_location')
                            and not _readable_name(d.get('doc_label'))
                            and not _readable_name(d.get('doc_name'))
                        ):
                            preview_snip, _ = read_local_preview_snippet(
                                d['raw_data_location'],
                                official_data,
                                max_runes=80,
                                max_utf8_bytes=240,
                            )
                        unread_preview_l.append({
                            'doc_id': d['doc_id'],
                            'title': _knowledge_title(d, preview_snip),
                            'effective_date': d['effective_date'],
                            'created_at': d['created_at'],
                        })
                except Exception as exc:
                    reading_err_l = exc.code if isinstance(exc, PolicyError) else 'reading_state_unavailable'
                    unread_preview_l = []
            return (
                a_val,
                a_err,
                (
                    cat_ver_l,
                    pending_total_l,
                    pending_docs_l,
                    unread_count_l,
                    unread_state_rev_l,
                    unread_preview_l,
                    reading_err_l,
                ),
                None,
            )

        def _query(gen):
            sinfo = cat.dataset_sync_info(dataset_id) or {}
            cat_ver_l = str(sinfo.get('catalog_version', ''))
            pending_rows, pending_total_l = cat.query_pending_raw_docs(dataset_id, limit=20, expected_gen=gen)
            pending_docs_l = [
                {
                    'id': d['doc_id'],
                    'dataset_id': d['dataset_id'],
                    'name': d['doc_name'],
                    'label': d['doc_label'] or '',
                    'title': _readable_name(d['doc_label']) or _readable_name(d['doc_name']) or d['doc_name'],
                    'date': d['updated_at'] or d['created_at'],
                    'kind': '文档',
                    'created_at': d['created_at'],
                }
                for d in pending_rows
            ]
            reading_err_l = None
            unread_count_l = 0
            unread_state_rev_l = 0
            top_docs_l = []
            try:
                if identity:
                    unread_ids, unread_state_rev_l = read_store.compute_unread_snapshot(
                        identity, project_id, dataset_id, cat
                    )
                    unread_count_l = len(unread_ids)
                    if unread_ids:
                        if len(unread_ids) <= 200:
                            docs = cat.fetch_docs_by_ids(dataset_id, list(unread_ids))
                            docs.sort(key=lambda d: (d['created_at_ns'] or '', d['doc_id']), reverse=True)
                            top_docs = docs[:10]
                        else:
                            top_ids = []
                            for batch in cat.iter_knowledge_doc_ids_after_horizon(dataset_id, '', batch_size=64):
                                for did in batch:
                                    if did in unread_ids:
                                        top_ids.append(did)
                                        if len(top_ids) >= 10:
                                            break
                                if len(top_ids) >= 10:
                                    break
                            if len(top_ids) < 10:
                                rem = [d for d in sorted(unread_ids) if d not in set(top_ids)][: 10 - len(top_ids)]
                                top_ids.extend(rem)
                            top_docs = cat.fetch_docs_by_ids(dataset_id, top_ids)
                            top_docs.sort(key=lambda d: (d['created_at_ns'] or '', d['doc_id']), reverse=True)
                        top_docs_l = top_docs[:10]
            except Exception as exc:
                reading_err_l = exc.code if isinstance(exc, PolicyError) else 'reading_state_unavailable'

            return (
                cat_ver_l,
                pending_total_l,
                pending_docs_l,
                unread_count_l,
                unread_state_rev_l,
                top_docs_l,
                reading_err_l,
            )

        revalidate_proj = ctx.project_revalidator(project_id, dataset_id)
        revalidate_proj.check_local()
        conflict_retries = 0
        rebuild_retried = False
        while True:
            result = await ctx.run_db(_run_all)
            a_val, a_err, d_val, d_err = result
            if (
                isinstance(d_err, PolicyError)
                and d_err.code == 'catalog_generation_conflict'
                and d_err.status == 409
                and conflict_retries < 2
            ):
                conflict_retries += 1
                await revalidate_proj()
                continue
            if (
                isinstance(d_err, PolicyError)
                and d_err.code == 'catalog_index_rebuilding'
                and d_err.status == 503
                and not rebuild_retried
            ):
                rebuild_retried = True
                await revalidate_proj()
                try:
                    await ctx.ensure_synced(fresh=True)
                except BaseException as exc:
                    if isinstance(exc, asyncio.CancelledError):
                        raise
                    return a_val, a_err, None, exc
                await revalidate_proj()
                sync_err = None
                continue
            return result

    b_res, n_res, db_res = await asyncio.gather(
        _load_backlog(),
        read_processing_notices(registry, dataset_id, official_data=official_data),
        _load_db_sections(),
        return_exceptions=True,
    )

    if isinstance(b_res, asyncio.CancelledError):
        raise b_res
    if isinstance(b_res, BaseException):
        errors['backlog'] = b_res.code if isinstance(b_res, PolicyError) else 'backlog_unavailable'
    else:
        backlog_job, automatic_save, buffer_reviews, checks, processing_issues = b_res

    if isinstance(db_res, asyncio.CancelledError):
        raise db_res
    if isinstance(db_res, PolicyError):
        raise db_res
    if isinstance(db_res, BaseException):
        a_res = db_res
        d_res = db_res
    else:
        a_val, a_err, d_val, d_err = db_res
        a_res = a_err if a_err is not None else a_val
        d_res = d_err if d_err is not None else d_val

    if isinstance(n_res, asyncio.CancelledError):
        raise n_res
    if isinstance(n_res, BaseException):
        errors['processing_notices'] = (
            n_res.code if isinstance(n_res, PolicyError) else 'processing_diagnostics_unavailable'
        )
    else:
        unresolved, resolved = n_res
        notices = unresolved + resolved

    cat_ver = ''
    unread_count = 0
    unread_state_rev = 0
    unread_preview = []
    pending_total = 0
    pending_docs = []
    if isinstance(d_res, asyncio.CancelledError):
        raise d_res
    if isinstance(d_res, PolicyError) and (
        d_res.code == 'catalog_generation_conflict' or d_res.status in (401, 403, 404)
    ):
        raise d_res
    if isinstance(d_res, BaseException):
        errors['documents'] = d_res.code if isinstance(d_res, PolicyError) else 'documents_unavailable'
    else:
        cat_ver, pending_total, pending_docs, unread_count, unread_state_rev, unread_preview, reading_err = d_res
        if reading_err:
            errors['reading_state'] = reading_err

    activity = []
    if isinstance(a_res, asyncio.CancelledError):
        raise a_res
    if isinstance(a_res, BaseException):
        errors['activity'] = a_res.code if isinstance(a_res, PolicyError) else 'activity_unavailable'
    else:
        activity, _ = a_res

    def _make_summary_payload(_sub, _lo, _hi):
        capture = project_capture_fields(registry.state['projects'][project_id])
        return {
            'project_id': project_id,
            'dataset_id': dataset_id,
            **capture,
            'automatic_save': capture['auto_capture'] and automatic_save,
            'backlog': dict(backlog_job) if isinstance(backlog_job, dict) else backlog_job,
            'buffer_reviews': list(buffer_reviews),
            'checks': list(checks),
            'processing_issues': list(processing_issues),
            'knowledge_catalog_version': cat_ver,
            'unread_count': unread_count,
            'unread_state_rev': unread_state_rev,
            'unread_preview': list(unread_preview),
            'unread_has_more': unread_count > len(unread_preview),
            'pending_documents_count': pending_total,
            'pending_documents': list(pending_docs),
            'notices': list(notices),
            'activity': list(activity),
            'errors': dict(errors),
        }

    def _trim_summary(p: dict) -> bool:
        if p['activity']:
            p['activity'].pop()
            return True
        if p['pending_documents']:
            p['pending_documents'].pop()
            return True
        if p['notices']:
            p['notices'].pop()
            return True
        if p['unread_preview']:
            p['unread_preview'].pop()
            p['unread_has_more'] = unread_count > len(p['unread_preview'])
            return True
        for key in ('buffer_reviews', 'checks', 'processing_issues'):
            if p[key]:
                p[key].pop()
                return True
        if isinstance(p.get('backlog'), dict):
            b = p['backlog']
            for k in ('last_error', 'message', 'detail'):
                if isinstance(b.get(k), str) and len(b[k]) > 64:
                    b[k] = b[k][:64]
                    return True
        return False

    return _make_summary_payload, [], _trim_summary
