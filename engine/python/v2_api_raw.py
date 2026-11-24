"""Raw records and session-child pages."""
import heapq
from pathlib import Path

from cache_read_v2 import CacheSessionHydrator
from catalog_index import CatalogIndex
from local_body_reader import verify_official_read_access
from project_policy import PolicyError
from sqlite_ro import uuid_text
from v2_api_context import (
    PayloadBuilder,
    V2ApiContext,
    _filter_digest,
    _require_verified_project,
)


def _query_raw_records_v2(
    official_data,
    catalog_index: CatalogIndex,
    hydrator: CacheSessionHydrator,
    dataset_id: str,
    *,
    kind: str,
    limit: int,
    after_order_key: tuple | None,
    snapshot_runner=None,
) -> dict:
    """Dual-branch indexed Keyset query + Python ``heapq.merge`` (zero full scan, zero TEMP B-TREE)."""
    cache_db = Path(official_data) / 'system' / 'databases' / 'cache.db'

    def _query_catalog(gen: int):
        session_branch = []
        doc_branch = []

        if kind != 'document':
            sess_after = None
            if after_order_key is not None:
                last_neg_ts, last_kind_rank, last_uid, last_sid = after_order_key
                if last_kind_rank == 0:
                    sess_after = (int(last_neg_ts), str(last_uid), str(last_sid))
                else:
                    # Document came after sessions at the same timestamp -> advance past that timestamp
                    sess_after = (int(last_neg_ts) + 1, '', '')
            top_sessions = catalog_index.query_top_level_sessions_page(
                dataset_id,
                limit=limit + 1,
                after_key=sess_after,
            )
            for s in top_sessions:
                order_key = (int(s['sort_ts_desc_key']), 0, s['user_id'], s['session_id'])
                if after_order_key is not None and order_key <= after_order_key:
                    continue
                session_branch.append({
                    'item_kind': 'session',
                    'sort_ts_ns': -int(s['sort_ts_desc_key']),
                    '_order_key': order_key,
                    '_raw_session': s,
                })

        if kind != 'session':
            doc_after = None
            if after_order_key is not None:
                last_neg_ts, last_kind_rank, _, last_item_id = after_order_key
                if last_kind_rank == 1:
                    doc_after = (int(last_neg_ts), str(last_item_id))
                else:
                    doc_after = (int(last_neg_ts), '')
            raw_docs = catalog_index.query_raw_docs_page(dataset_id, limit=limit + 1, after_key=doc_after)
            for d in raw_docs:
                sort_ts_ns = -int(d['sort_ts_desc_key'])
                order_key = (int(d['sort_ts_desc_key']), 1, '', d['doc_id'])
                if after_order_key is not None and order_key <= after_order_key:
                    continue
                doc_branch.append({
                    'item_kind': 'document',
                    'sort_ts_ns': sort_ts_ns,
                    '_order_key': order_key,
                    'document': {
                        'id': d['doc_id'],
                        'datasetId': d['dataset_id'],
                        'name': d['doc_name'],
                        'label': d['doc_label'],
                        'createdAt': d['created_at'],
                        'updatedAt': d['updated_at'],
                        'contentHash': d['content_hash'],
                        'pipeline_status': d['pipeline_status'],
                    },
                })

        merged = list(heapq.merge(session_branch, doc_branch, key=lambda item: item['_order_key']))
        has_more_l = len(merged) > limit
        page_l = merged[:limit]

        parent_preview_specs = [
            (entry['_raw_session']['session_id'], entry['_raw_session']['user_id'])
            for entry in page_l
            if entry['item_kind'] == 'session' and int(entry['_raw_session'].get('child_count') or 0) > 0
        ]
        previews_l = (
            catalog_index.query_top_level_children_previews(
                dataset_id, parent_preview_specs, limit=5, expected_gen=gen,
            )
            if parent_preview_specs
            else {}
        )
        return gen, page_l, previews_l, has_more_l

    if snapshot_runner is not None:
        gen, page, previews_by_parent, has_more = snapshot_runner(_query_catalog)
    else:
        gen, page, previews_by_parent, has_more = _query_catalog(catalog_index.generation())

    # Hydrate ONLY the <= limit sessions that landed on this page (plus <=5 indexed preview children each)
    page_sessions_to_hydrate = []
    for entry in page:
        if entry['item_kind'] != 'session':
            continue
        raw_s = entry['_raw_session']
        s_out = {
            'session_id': raw_s['session_id'],
            'dataset_id': raw_s['dataset_id'],
            'user_id': raw_s['user_id'],
            'agent_id': raw_s['agent_id'],
            'last_activity_at': raw_s['last_activity_at'],
            'started_at': raw_s['started_at'],
        }
        entry['session'] = s_out
        page_sessions_to_hydrate.append(s_out)

        child_cnt = int(raw_s.get('child_count') or 0)
        preview_children = []
        if child_cnt > 0:
            p_items, children_has_more = previews_by_parent.get(
                (raw_s['session_id'], raw_s['user_id']),
                ([], False),
            )
            for child_row in p_items:
                c_out = {
                    'session_id': child_row['session_id'],
                    'dataset_id': child_row['dataset_id'],
                    'user_id': child_row['user_id'],
                    'agent_id': child_row['agent_id'],
                    'last_activity_at': child_row['last_activity_at'],
                    'started_at': child_row['started_at'],
                    'is_subagent': True,
                    'parent_session_id': child_row['parent_session_id'],
                    'subagent_role': child_row['subagent_role'],
                    'nesting_depth': int(child_row['nesting_depth'] or 1),
                }
                preview_children.append(c_out)
                page_sessions_to_hydrate.append(c_out)
        else:
            children_has_more = False

        s_out['child_count'] = child_cnt
        s_out['children_preview'] = preview_children
        s_out['children_has_more'] = children_has_more

    cache_ready = hydrator.hydrate_sessions(cache_db, page_sessions_to_hydrate) if page_sessions_to_hydrate else (
        cache_db.is_file() and not cache_db.is_symlink()
    )

    clean_items = []
    order_keys = []
    for entry in page:
        order_keys.append(entry['_order_key'])
        if entry['item_kind'] == 'session':
            clean_items.append({
                'item_kind': 'session',
                'sort_ts_ns': entry['sort_ts_ns'],
                'session': entry['session'],
            })
        else:
            clean_items.append({
                'item_kind': 'document',
                'sort_ts_ns': entry['sort_ts_ns'],
                'document': entry['document'],
            })

    return {
        'generation': gen,
        'cache_ready': cache_ready,
        'items': clean_items,
        'order_keys': order_keys,
        'has_more': has_more,
    }


def _query_session_children_v2(
    official_data,
    catalog_index: CatalogIndex,
    hydrator: CacheSessionHydrator,
    dataset_id: str,
    parent_session_id: str,
    *,
    user_id: str = '',
    limit: int,
    after_key: tuple | None,
) -> dict:
    cache_db = Path(official_data) / 'system' / 'databases' / 'cache.db'
    c_page = catalog_index.query_session_children_page(
        dataset_id,
        parent_session_id,
        user_id=user_id,
        limit=limit,
        after_key=after_key,
    )
    page_items = []
    order_keys = []
    for child_row in c_page['items']:
        order_keys.append((int(child_row['sort_ts_desc_key']), child_row['user_id'], child_row['session_id']))
        page_items.append({
            'session_id': child_row['session_id'],
            'dataset_id': child_row['dataset_id'],
            'user_id': child_row['user_id'],
            'agent_id': child_row['agent_id'],
            'last_activity_at': child_row['last_activity_at'],
            'started_at': child_row['started_at'],
            'is_subagent': True,
            'parent_session_id': child_row['parent_session_id'],
            'subagent_role': child_row['subagent_role'],
            'nesting_depth': int(child_row['nesting_depth'] or 1),
        })
    if page_items:
        hydrator.hydrate_sessions(cache_db, page_items)
    return {
        'generation': int(c_page.get('generation', 0)),
        'total_count': int(c_page['total_count']),
        'items': page_items,
        'order_keys': order_keys,
        'has_more': bool(c_page['has_more']),
    }


async def execute_raw_records(
    ctx: V2ApiContext,
    *,
    project_id: str,
    kind: str,
    limit: int,
    raw_cursor: str,
    dataset_id: str | None = None,
) -> tuple[PayloadBuilder, list[dict]]:
    official_data = ctx.official_data
    registry = ctx.registry
    cat = ctx.cat
    hydrator = ctx.hydrator
    cursors = ctx.cursors

    if dataset_id is None:
        dataset_id = _require_verified_project(registry, project_id)['dataset_id']

    f_digest = _filter_digest(kind=kind)
    after_key = None
    if raw_cursor:
        keys = cursors.decode(
            raw_cursor,
            endpoint='raw_records',
            dataset_id=dataset_id,
            sort_mode='ts_merge',
            filter_digest=f_digest,
        )
        if len(keys) != 4:
            raise PolicyError('invalid_cursor', 400)
        after_key = (int(keys[0]), int(keys[1]), str(keys[2]), str(keys[3]))

    await verify_official_read_access(getattr(registry, 'official', None), dataset_id)
    await ctx.ensure_synced()

    def _query():
        res_l = _query_raw_records_v2(
            official_data,
            cat,
            hydrator,
            dataset_id,
            kind=kind,
            limit=limit,
            after_order_key=after_key,
            snapshot_runner=ctx.query_catalog_snapshot,
        )
        return res_l['generation'], res_l

    gen, res = await ctx.run_read_query(
        _query,
        revalidate=ctx.project_revalidator(project_id, dataset_id),
    )
    order_keys = res['order_keys']

    def _make_payload(subset, _lo, hi):
        eff_more = bool(res['has_more'] or hi < len(res['items']))
        nxt = ''
        if eff_more and subset:
            lk = order_keys[hi - 1]
            nxt = cursors.encode(
                endpoint='raw_records',
                dataset_id=dataset_id,
                sort_mode='ts_merge',
                filter_digest=f_digest,
                generation_hint=gen,
                keys=[lk[0], lk[1], lk[2], lk[3]],
            )
        return {
            'project_id': project_id,
            'dataset_id': dataset_id,
            'kind': kind,
            'cache_ready': res['cache_ready'],
            'items': subset,
            'next_cursor': nxt,
            'has_more': eff_more,
        }

    return _make_payload, res['items']


async def execute_raw_records_session_children(
    ctx: V2ApiContext,
    *,
    project_id: str,
    parent_sid: str,
    user_id: str,
    limit: int,
    raw_cursor: str,
    dataset_id: str | None = None,
) -> tuple[PayloadBuilder, list[dict]]:
    official_data = ctx.official_data
    registry = ctx.registry
    cat = ctx.cat
    hydrator = ctx.hydrator
    cursors = ctx.cursors

    if dataset_id is None:
        dataset_id = _require_verified_project(registry, project_id)['dataset_id']

    f_digest = _filter_digest(parent_session_id=parent_sid, user_id=uuid_text(user_id) if user_id else '')
    after_key = None
    if raw_cursor:
        keys = cursors.decode(
            raw_cursor,
            endpoint='raw_records_children',
            dataset_id=dataset_id,
            sort_mode='child_ts',
            filter_digest=f_digest,
        )
        if len(keys) != 3:
            raise PolicyError('invalid_cursor', 400)
        after_key = (int(keys[0]), str(keys[1]), str(keys[2]))

    await verify_official_read_access(getattr(registry, 'official', None), dataset_id)
    await ctx.ensure_synced()

    def _query():
        res_l = _query_session_children_v2(
            official_data,
            cat,
            hydrator,
            dataset_id,
            parent_sid,
            user_id=user_id,
            limit=limit,
            after_key=after_key,
        )
        return res_l['generation'], res_l

    gen, res = await ctx.run_read_query(
        _query,
        missing_code='session_not_found',
        revalidate=ctx.project_revalidator(project_id, dataset_id),
    )
    order_keys = res['order_keys']

    def _make_payload(subset, _lo, hi):
        eff_more = bool(res['has_more'] or hi < len(res['items']))
        nxt = ''
        if eff_more and subset:
            lk = order_keys[hi - 1]
            nxt = cursors.encode(
                endpoint='raw_records_children',
                dataset_id=dataset_id,
                sort_mode='child_ts',
                filter_digest=f_digest,
                generation_hint=gen,
                keys=[lk[0], lk[1], lk[2]],
            )
        return {
            'project_id': project_id,
            'dataset_id': dataset_id,
            'parent_session_id': parent_sid,
            'total_count': res['total_count'],
            'items': subset,
            'next_cursor': nxt,
            'has_more': eff_more,
        }

    return _make_payload, res['items']
