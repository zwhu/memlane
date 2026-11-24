"""Project and common knowledge pages, plus metadata search."""
from pathlib import Path

from local_body_reader import verify_official_read_access
from project_policy import PolicyError
from v2_api_context import (
    PayloadBuilder,
    PayloadTrimmer,
    V2ApiContext,
    _filter_digest,
    _format_knowledge_item,
    _recall_event_doc_ids,
    _require_verified_project,
    _resolve_common_dataset,
)


async def execute_project_knowledge(
    ctx: V2ApiContext,
    *,
    project_id: str,
    sort_mode: str,
    category: str,
    unread_only: bool,
    recall_agent: str,
    recall_event: str,
    direction: str,
    anchor_doc_id: str,
    limit: int,
    raw_cursor: str,
    identity: str,
    dataset_id: str | None = None,
) -> tuple[PayloadBuilder, list[dict]]:
    official_data = ctx.official_data
    registry = ctx.registry
    recall_store = ctx.recall_store
    cat = ctx.cat
    read_store = ctx.read_store
    cursors = ctx.cursors

    if dataset_id is None:
        dataset_id = _require_verified_project(registry, project_id)['dataset_id']

    f_digest = _filter_digest(
        category=category,
        unread_only=unread_only,
        recall_agent=recall_agent,
        recall_event=recall_event,
    )
    after_key = None
    before_key = None
    if raw_cursor:
        keys = cursors.decode(
            raw_cursor,
            endpoint='project_knowledge',
            dataset_id=dataset_id,
            sort_mode=sort_mode,
            filter_digest=f_digest,
        )
        if sort_mode == 'recall':
            if len(keys) != 3:
                raise PolicyError('invalid_cursor', 400)
            parsed_key = (int(keys[0]), int(keys[1]), str(keys[2]))
        else:
            if len(keys) != 2:
                raise PolicyError('invalid_cursor', 400)
            parsed_key = (int(keys[0]), str(keys[1]))
        if direction == 'prev':
            before_key = parsed_key
        else:
            after_key = parsed_key

    await verify_official_read_access(getattr(registry, 'official', None), dataset_id)
    await ctx.ensure_synced()

    def _query(gen):
        recall_ids = None
        if recall_agent and recall_event:
            recall_ids = _recall_event_doc_ids(recall_store, dataset_id, recall_agent, recall_event)

        sinfo = cat.dataset_sync_info(dataset_id) or {}
        cat_ver = str(sinfo.get('catalog_version', ''))

        unread_ids_set = None
        unread_count = 0
        unread_state_rev = 0
        if identity:
            unread_ids_set, unread_state_rev = read_store.compute_unread_snapshot(
                identity, project_id, dataset_id, cat
            )
            unread_count = len(unread_ids_set)

        allowed_doc_ids = None
        if unread_only and recall_ids is not None:
            allowed_doc_ids = (unread_ids_set or set()) & recall_ids
        elif unread_only:
            allowed_doc_ids = unread_ids_set or set()
        elif recall_ids is not None:
            allowed_doc_ids = recall_ids

        res = cat.query_project_knowledge(
            dataset_id,
            sort=sort_mode,
            category=category,
            allowed_doc_ids=allowed_doc_ids,
            limit=limit,
            after_key=after_key,
            before_key=before_key,
            anchor_doc_id=anchor_doc_id,
            anchor_direction=direction,
        )
        if res is None:
            raise PolicyError('document_not_found', 404)
        page_rows = res['items']
        page_unread = (
            {d['doc_id']: True for d in page_rows}
            if unread_only
            else (
                {d['doc_id']: (d['doc_id'] in unread_ids_set) for d in page_rows}
                if identity and unread_ids_set is not None
                else {}
            )
        )
        cursor_keys = [
            (
                [d['recall_desc_key'], d['day_desc_key'], d['doc_id']]
                if sort_mode == 'recall'
                else [d['day_desc_key'], d['doc_id']]
            )
            for d in page_rows
        ]
        return (
            gen,
            cat_ver,
            unread_count,
            unread_state_rev,
            res['total_filtered'],
            res['category_counts'],
            page_rows,
            page_unread,
            cursor_keys,
            bool(res['has_more']),
            bool(res['has_prev']),
        )

    def _query_and_format():
        (
            gen_r,
            cat_ver_r,
            unread_count_r,
            unread_state_rev_r,
            total_filtered_r,
            category_counts_r,
            page_rows_r,
            page_unread_r,
            cursor_keys_r,
            has_more_r,
            has_prev_r,
        ) = ctx.query_catalog_snapshot(_query)
        formatted_r = [
            _format_knowledge_item(
                d,
                project_id=project_id,
                is_unread=page_unread_r.get(d['doc_id'], False),
                official_data=official_data,
            )
            for d in page_rows_r
        ]
        return (
            gen_r,
            cat_ver_r,
            unread_count_r,
            unread_state_rev_r,
            total_filtered_r,
            category_counts_r,
            formatted_r,
            cursor_keys_r,
            has_more_r,
            has_prev_r,
        )

    (
        gen,
        cat_ver,
        unread_count,
        unread_state_rev,
        total_filtered,
        category_counts,
        formatted,
        cursor_keys,
        base_has_more,
        base_has_prev,
    ) = await ctx.run_read_query(
        _query_and_format,
        missing_code='document_not_found' if anchor_doc_id else None,
        revalidate=ctx.project_revalidator(project_id, dataset_id),
    )

    def _make_payload(subset, lo, hi):
        eff_more = bool(base_has_more or hi < len(formatted))
        eff_prev = bool(base_has_prev or lo > 0)
        nxt = ''
        prv = ''
        if eff_more and subset:
            nxt = cursors.encode(
                endpoint='project_knowledge',
                dataset_id=dataset_id,
                sort_mode=sort_mode,
                filter_digest=f_digest,
                generation_hint=gen,
                keys=cursor_keys[hi - 1],
            )
        if eff_prev and subset:
            prv = cursors.encode(
                endpoint='project_knowledge',
                dataset_id=dataset_id,
                sort_mode=sort_mode,
                filter_digest=f_digest,
                generation_hint=gen,
                keys=cursor_keys[lo],
            )
        return {
            'project_id': project_id,
            'dataset_id': dataset_id,
            'generation': gen,
            'catalog_version': cat_ver,
            'sort': sort_mode,
            'total_filtered': total_filtered,
            'unread_count': unread_count,
            'unread_state_rev': unread_state_rev,
            'category_counts': category_counts,
            'items': subset,
            'next_cursor': nxt,
            'has_more': eff_more,
            'prev_cursor': prv,
            'has_prev': eff_prev,
        }

    return _make_payload, formatted


async def execute_common_knowledge(
    ctx: V2ApiContext,
    *,
    raw_ds: str,
    common_cat: str,
    recall_agent: str,
    recall_event: str,
    direction: str,
    anchor_doc_id: str,
    limit: int,
    raw_cursor: str,
    dataset_id: str | None = None,
) -> tuple[PayloadBuilder, list[dict]]:
    official_data = ctx.official_data
    registry = ctx.registry
    recall_store = ctx.recall_store
    cat = ctx.cat
    cursors = ctx.cursors

    if dataset_id is None:
        dataset_id = _resolve_common_dataset(registry, raw_ds)

    f_digest = _filter_digest(common_category=common_cat, recall_agent=recall_agent, recall_event=recall_event)
    after_key = None
    before_key = None
    if raw_cursor:
        keys = cursors.decode(
            raw_cursor,
            endpoint='common_knowledge',
            dataset_id=dataset_id,
            sort_mode='updated_ts',
            filter_digest=f_digest,
        )
        if len(keys) != 2:
            raise PolicyError('invalid_cursor', 400)
        parsed_key = (int(keys[0]), str(keys[1]))
        if direction == 'prev':
            before_key = parsed_key
        else:
            after_key = parsed_key

    await verify_official_read_access(getattr(registry, 'official', None), dataset_id)
    await ctx.ensure_synced()

    def _query_snap(gen):
        sinfo = cat.dataset_sync_info(dataset_id) or {}
        cat_ver = str(sinfo.get('catalog_version', ''))
        recall_ids = _recall_event_doc_ids(recall_store, dataset_id, recall_agent, recall_event) if recall_event else None
        res = cat.query_common_knowledge(
            dataset_id,
            common_category=common_cat,
            recall_ids=recall_ids,
            limit=limit,
            after_key=after_key,
            before_key=before_key,
            anchor_doc_id=anchor_doc_id,
            anchor_direction=direction,
        )
        if res is None:
            raise PolicyError('document_not_found', 404)
        rows = res['items']
        ckeys = [[d['sort_ts_desc_key'], d['doc_id']] for d in rows]
        return (
            gen,
            cat_ver,
            res['total_filtered'],
            res['category_counts'],
            rows,
            ckeys,
            bool(res['has_more']),
            bool(res['has_prev']),
        )

    def _query_and_format_common():
        (
            gen_r,
            cat_ver_r,
            total_filtered_r,
            category_counts_r,
            rows_r,
            ckeys_r,
            has_more_r,
            has_prev_r,
        ) = ctx.query_catalog_snapshot(_query_snap)
        formatted_r = [
            _format_knowledge_item(
                d,
                project_id='',
                is_unread=False,
                official_data=official_data,
            )
            for d in rows_r
        ]
        return (
            gen_r,
            cat_ver_r,
            total_filtered_r,
            category_counts_r,
            formatted_r,
            ckeys_r,
            has_more_r,
            has_prev_r,
        )

    (
        gen,
        cat_ver,
        total_filtered,
        category_counts,
        formatted,
        ckeys,
        base_has_more,
        base_has_prev,
    ) = await ctx.run_read_query(
        _query_and_format_common,
        missing_code='document_not_found' if anchor_doc_id else None,
        revalidate=ctx.common_revalidator(raw_ds, dataset_id),
    )

    def _make_payload(subset, lo, hi):
        eff_more = bool(base_has_more or hi < len(formatted))
        eff_prev = bool(base_has_prev or lo > 0)
        nxt = ''
        prv = ''
        if eff_more and subset:
            nxt = cursors.encode(
                endpoint='common_knowledge',
                dataset_id=dataset_id,
                sort_mode='updated_ts',
                filter_digest=f_digest,
                generation_hint=gen,
                keys=ckeys[hi - 1],
            )
        if eff_prev and subset:
            prv = cursors.encode(
                endpoint='common_knowledge',
                dataset_id=dataset_id,
                sort_mode='updated_ts',
                filter_digest=f_digest,
                generation_hint=gen,
                keys=ckeys[lo],
            )
        return {
            'dataset_id': dataset_id,
            'generation': gen,
            'catalog_version': cat_ver,
            'common_category': common_cat,
            'total_filtered': total_filtered,
            'category_counts': category_counts,
            'items': subset,
            'next_cursor': nxt,
            'has_more': eff_more,
            'prev_cursor': prv,
            'has_prev': eff_prev,
        }

    return _make_payload, formatted


async def execute_search_recent(
    ctx: V2ApiContext,
    *,
    limit: int,
    project_id_filter: str,
    dataset_id_filter: str,
    identity: str,
) -> tuple[PayloadBuilder, list[dict], PayloadTrimmer]:
    official_data = ctx.official_data
    cat = ctx.cat
    hydrator = ctx.hydrator

    scope, revalidate_fn = await ctx.resolve_search_scope(project_id_filter, dataset_id_filter)

    def _query_recent_snap(gen):
        cur_allowed_ds = scope['allowed_ds']
        cur_ds_to_proj = scope['ds_to_proj']
        k_rows = cat.query_overview_recent(cur_allowed_ds, limit=limit)
        unread_flags = ctx.page_unread_flags_by_dataset(identity, k_rows, cur_ds_to_proj)
        s_rows = cat.query_recent_sessions(cur_allowed_ds, limit=limit)
        return gen, k_rows, unread_flags, s_rows

    def _query():
        cur_ds_to_proj = scope['ds_to_proj']
        gen, k_rows, unread_flags, s_rows = ctx.query_catalog_snapshot(_query_recent_snap)
        k_items = [
            _format_knowledge_item(
                d,
                project_id=cur_ds_to_proj.get(d['dataset_id'], ''),
                is_unread=unread_flags.get((d['dataset_id'], d['doc_id']), False),
                official_data=official_data,
            )
            for d in k_rows
        ]
        # Indexed query on catalog_sessions (zero TEMP B-TREE) + cache.db hydration
        s_items = [
            {
                'session_id': sr['session_id'],
                'dataset_id': sr['dataset_id'],
                'project_id': cur_ds_to_proj.get(sr['dataset_id'], ''),
                'user_id': sr['user_id'],
                'agent_id': sr['agent_id'],
                'last_activity_at': sr['last_activity_at'],
                'started_at': sr['started_at'],
            }
            for sr in s_rows
        ]
        if s_items and official_data:
            cache_db = Path(official_data) / 'system' / 'databases' / 'cache.db'
            hydrator.hydrate_sessions(cache_db, s_items)
        return gen, k_items, s_items

    gen, k_items, s_items = await ctx.run_read_query(_query, revalidate=revalidate_fn)

    def _make_payload(subset, _lo, _hi):
        return {
            'generation': gen,
            'knowledge_items': subset,
            'session_items': list(s_items),
        }

    def _trim_recent(p: dict) -> bool:
        if p['session_items']:
            p['session_items'].pop()
            return True
        if p['knowledge_items']:
            p['knowledge_items'].pop()
            return True
        return False

    return _make_payload, k_items, _trim_recent


async def execute_search_metadata(
    ctx: V2ApiContext,
    *,
    query_text: str,
    filter_kind: str,
    limit: int,
    project_id_filter: str,
    dataset_id_filter: str,
    identity: str,
) -> tuple[PayloadBuilder, list[dict]]:
    official_data = ctx.official_data
    cat = ctx.cat

    scope, revalidate_fn = await ctx.resolve_search_scope(project_id_filter, dataset_id_filter)

    def _query_meta_snap(gen):
        cur_allowed_ds = scope['allowed_ds']
        cur_ds_to_proj = scope['ds_to_proj']
        rows, has_more = cat.query_search_metadata(
            cur_allowed_ds,
            query_text,
            filter_kind=filter_kind,
            limit=limit,
        )
        unread_flags = ctx.page_unread_flags_by_dataset(identity, rows, cur_ds_to_proj, knowledge_only=True)
        return gen, rows, unread_flags, has_more

    def _query():
        cur_ds_to_proj = scope['ds_to_proj']
        gen, rows, unread_flags, has_more = ctx.query_catalog_snapshot(_query_meta_snap)
        formatted = [
            _format_knowledge_item(
                d,
                project_id=cur_ds_to_proj.get(d['dataset_id'], ''),
                is_unread=unread_flags.get((d['dataset_id'], d['doc_id']), False),
                official_data=official_data,
            )
            for d in rows
        ]
        return gen, formatted, has_more

    gen, formatted, base_has_more = await ctx.run_read_query(_query, revalidate=revalidate_fn)

    def _make_payload(subset, _lo, hi):
        return {
            'query': query_text,
            'generation': gen,
            'items': subset,
            'has_more': bool(base_has_more or hi < len(formatted)),
        }

    return _make_payload, formatted
