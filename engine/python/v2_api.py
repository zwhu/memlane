"""Phase 1 /v2/* cursor-paginated read APIs, local body fast-path, and reading-state endpoints.

Implements every Phase 1 control-plane endpoint specified in proposal.md §3.5:
- GET  /v2/datasets/{ds}/documents/{doc}/body
- GET  /v2/overview/bootstrap
- GET  /v2/overview/day
- GET  /v2/projects/{id}/knowledge
- GET  /v2/common/knowledge
- GET  /v2/search/recent
- GET  /v2/search/metadata
- GET  /v2/projects/{id}/raw-records
- GET  /v2/projects/{id}/raw-records/sessions/{sid}/children
- GET  /v2/projects/{id}/message-summary
- POST /v2/reading-state/migrate-local
- POST /v2/reading-state/import/begin
- POST /v2/reading-state/import/chunk
- POST /v2/reading-state/import/commit
- POST /v2/reading-state/mark-read
- POST /v2/reading-state/mark-all-read
- POST /v2/provenance/backfill-legacy-dates
"""
import asyncio
import base64
from concurrent.futures import ThreadPoolExecutor
import functools
import hashlib
import heapq
import hmac
import inspect
import json
import os
from pathlib import Path
import re
import secrets
import sqlite3
import threading

from aiohttp import web

from cache_read_v2 import CacheSessionHydrator
from catalog_index import CatalogIndex, _file_stat_sig
from local_body_reader import (
    DEFAULT_LIST_JSON_BUDGET_BYTES,
    DEFAULT_OVERVIEW_JSON_BUDGET_BYTES,
    MAX_BODY_BYTES,
    fetch_authorized_official_datasets,
    finalize_budgeted_payload,
    json_utf8_response,
    read_document_body_with_fallback,
    read_local_preview_snippet,
    verify_official_read_access,
)
from processing_notices import processing_notices
from project_policy import PolicyError
from provenance_store import _calendar_date
from reading_state_store import ReadingStateStore
from session_relations import TrackedRelationsDict, resolve_session_relations
from sqlite_ro import (
    close_pooled_official_sqlite_ro,
    id_variants,
    is_valid_uuid,
    iso_text,
    pooled_official_sqlite_ro,
    ro_snapshot,
    uuid_text,
)
from v2_lookups import _require_verified_managed_dataset, _safe_session_id

_MAX_PAGE_LIMIT = 50
_DEFAULT_PAGE_LIMIT = 30
_MAX_MESSAGE_SUMMARY_BYTES = 16 * 1024
_PIPELINE_RUN_SCAN_CHUNK = 128
_PIPELINE_RUN_SCAN_MAX_CHUNKS = 2
_MACHINE_NAME_RE = re.compile(
    r'^(?:text_[0-9a-fA-F]{32}(?:\.(?:md|txt))?'
    r'|(?:user_context|memory)-[0-9a-fA-F]{32}(?:\.txt)?'
    r'|[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})$'
)
_IMAGE_ONLY_RE = re.compile(r'^!\[[^\]]*\]\([^)]*\)$')


def _b64u_encode(raw: bytes) -> str:
    return base64.urlsafe_b64encode(raw).decode('ascii').rstrip('=')


def _b64u_decode(token: str) -> bytes:
    pad = '=' * (-len(token) % 4)
    return base64.urlsafe_b64decode(token + pad)


class CursorCodec:
    """HMAC-SHA256 signed opaque cursor tokens bound to endpoint, dataset, sort, and filters."""

    def __init__(self, secret_key: bytes):
        self._key = bytes(secret_key)

    def encode(
        self,
        *,
        endpoint: str,
        dataset_id: str,
        sort_mode: str,
        filter_digest: str,
        generation_hint: int,
        keys: list,
    ) -> str:
        payload = json.dumps(
            {
                'e': endpoint,
                'd': dataset_id,
                's': sort_mode,
                'f': filter_digest,
                'g': int(generation_hint),
                'k': keys,
            },
            separators=(',', ':'),
            ensure_ascii=False,
        ).encode('utf-8')
        body_b64 = _b64u_encode(payload)
        sig = hmac.new(self._key, body_b64.encode('ascii'), hashlib.sha256).digest()
        return f'{body_b64}.{_b64u_encode(sig)}'

    def decode(
        self,
        token: str,
        *,
        endpoint: str,
        dataset_id: str,
        sort_mode: str,
        filter_digest: str,
    ) -> list:
        if not isinstance(token, str) or '.' not in token or len(token) > 2048:
            raise PolicyError('invalid_cursor', 400)
        parts = token.split('.')
        if len(parts) != 2:
            raise PolicyError('invalid_cursor', 400)
        body_b64, sig_b64 = parts
        expected_sig = _b64u_encode(hmac.new(self._key, body_b64.encode('ascii'), hashlib.sha256).digest())
        if not hmac.compare_digest(sig_b64, expected_sig):
            raise PolicyError('invalid_cursor', 400)
        try:
            data = json.loads(_b64u_decode(body_b64).decode('utf-8'))
        except Exception as exc:
            raise PolicyError('invalid_cursor', 400) from exc
        if (
            not isinstance(data, dict)
            or data.get('e') != endpoint
            or data.get('d') != dataset_id
            or data.get('s') != sort_mode
            or data.get('f') != filter_digest
            or not isinstance(data.get('k'), list)
        ):
            raise PolicyError('invalid_cursor', 400)
        return data['k']


def _filter_digest(**kwargs) -> str:
    encoded = json.dumps(kwargs, sort_keys=True, separators=(',', ':'), ensure_ascii=False).encode('utf-8')
    return hashlib.sha256(encoded).hexdigest()[:16]


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


def _readable_name(value) -> str:
    if not isinstance(value, str):
        return ''
    text = value.strip()
    if not text or _MACHINE_NAME_RE.match(text):
        return ''
    return text


def _first_preview_sentence(preview: str, limit: int = 80) -> str:
    if not preview:
        return ''
    lines = []
    fence = ''
    for line in preview.splitlines():
        s = line.strip()
        if s.startswith('```') or s.startswith('~~~'):
            mark = s[:3]
            fence = '' if fence == mark else (mark if not fence else fence)
            continue
        if fence or line.startswith('    ') or line.startswith('\t'):
            continue
        if not s:
            if lines:
                break
            continue
        if _IMAGE_ONLY_RE.match(s):
            continue
        s = s.lstrip('#>*-+ ').strip()
        if s:
            lines.append(s)
    joined = ' '.join(lines).strip()
    if not joined:
        return ''
    return joined[:limit]


def _knowledge_title(doc: dict, preview_text: str) -> str:
    label = _readable_name(doc.get('doc_label'))
    if label:
        return label
    name = _readable_name(doc.get('doc_name'))
    if name:
        return name
    sentence = _first_preview_sentence(preview_text)
    if sentence:
        return sentence
    day = doc.get('effective_date') or ''
    return f'记忆 · {day}' if day else '记忆'


def _format_knowledge_item(
    doc: dict,
    *,
    project_id: str,
    is_unread: bool,
    official_data,
    include_preview: bool = True,
) -> dict:
    preview_text = ''
    preview_truncated = False
    if include_preview and official_data and doc.get('raw_data_location'):
        preview_text, preview_truncated = read_local_preview_snippet(
            doc['raw_data_location'],
            official_data,
        )
    out = {
        'doc_id': doc['doc_id'],
        'dataset_id': doc['dataset_id'],
        'project_id': project_id,
        'title': _knowledge_title(doc, preview_text),
        'doc_name': doc['doc_name'],
        'doc_label': doc['doc_label'],
        'effective_date': doc['effective_date'],
        'created_at': doc['created_at'],
        'updated_at': doc['updated_at'],
        'content_hash': doc['content_hash'],
        'kind': doc['kind'],
        'category': doc['category'],
        'common_category': doc['common_category'],
        'lesson_type': doc['lesson_type'],
        'state_key': doc['state_key'],
        'session_id': doc['session_id'],
        'recall_count': doc['recall_count'],
        'is_unread': bool(is_unread),
        'preview_text': preview_text,
        'preview_truncated': bool(preview_truncated),
    }
    if 'match_field' in doc:
        out['match_field'] = doc['match_field']
    return out


def _verified_groups(registry):
    projects = []
    for proj in registry.state.get('projects', {}).values():
        if proj.get('status') == 'verified' and proj.get('dataset_id'):
            projects.append(proj)
    common = registry.state.get('common')
    if not (isinstance(common, dict) and common.get('status') == 'verified' and common.get('dataset_id')):
        common = None
    return projects, common


def _has_unconfirmed_groups(registry) -> bool:
    for proj in registry.state.get('projects', {}).values():
        if isinstance(proj, dict) and proj.get('status') != 'verified':
            return True
    common = registry.state.get('common')
    if isinstance(common, dict) and common.get('dataset_id') and common.get('status') != 'verified':
        return True
    return False


def _require_verified_project(registry, project_id: str) -> dict:
    project = registry.state.get('projects', {}).get(project_id)
    if project is None:
        raise PolicyError('project_not_found', 404)
    if project.get('status') != 'verified' or not project.get('dataset_id'):
        raise PolicyError('project_unconfirmed', 403)
    return project


def _format_pipeline_run_row(pr, fallback_ds: str = '') -> dict:
    return {
        'id': uuid_text(pr[0]),
        'pipeline_run_id': uuid_text(pr[1]),
        'parent_operation_id': uuid_text(pr[2]),
        'dataset_id': uuid_text(pr[3]) or fallback_ds,
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
    }


def _read_recent_pipeline_runs(
    official_data,
    *,
    allowed_datasets: set[str] | list[str] | None = None,
    dataset_id: str | None = None,
    limit: int = 20,
    cache: dict | None = None,
    cache_lock=None,
) -> tuple[list[dict], bool]:
    """Read recent pipeline_runs strictly SQL-scoped to authorized datasets with ZERO TEMP B-TREE.

    Always scopes SQL via ``WHERE dataset_id = ?`` using Cognee 1.6.7's composite index
    ``ix_pipeline_runs_dataset_pipeline_created_at(dataset_id, pipeline_name, created_at)``
    loose index scan — never reads unauthorized/unconfirmed dataset rows in SQL and never
    relies on global table probes.
    """
    if not official_data:
        return [], False
    db_path = Path(official_data) / 'system' / 'databases' / 'cognee_db'
    if not db_path.is_file() or db_path.is_symlink():
        return [], False

    target_set: set[str] = set()
    if dataset_id:
        c = uuid_text(dataset_id)
        if c:
            target_set.add(c)
    if allowed_datasets is not None:
        for ds in allowed_datasets:
            c = uuid_text(ds)
            if c:
                target_set.add(c)
    if not target_set:
        return [], False

    target_sorted = tuple(sorted(target_set))
    stat_sig = _file_stat_sig(db_path)
    cache_key = (stat_sig, target_sorted, int(limit))
    if cache is not None and cache_lock is not None:
        with cache_lock:
            cached = cache.get(cache_key)
            if cached is not None:
                return [dict(item) for item in cached[0]], cached[1]

    candidates: dict[str, tuple] = {}
    with pooled_official_sqlite_ro(db_path) as con:
        with ro_snapshot(con):
            for ds in target_sorted:
                for ds_var in id_variants(ds):
                    pnames = [
                        r[0]
                        for r in con.execute(
                            '''WITH RECURSIVE pnames(p) AS (
                                   SELECT MIN(pipeline_name)
                                   FROM pipeline_runs
                                   WHERE dataset_id = ? AND pipeline_name IS NOT NULL
                                   UNION ALL
                                   SELECT (
                                       SELECT MIN(pipeline_name)
                                       FROM pipeline_runs
                                       WHERE dataset_id = ? AND pipeline_name > pnames.p
                                   )
                                   FROM pnames
                                   WHERE pnames.p IS NOT NULL
                               )
                               SELECT p FROM pnames WHERE p IS NOT NULL''',
                            (ds_var, ds_var),
                        ).fetchall()
                    ]
                    has_null_pname = con.execute(
                        'SELECT 1 FROM pipeline_runs WHERE dataset_id = ? AND pipeline_name IS NULL LIMIT 1',
                        (ds_var,),
                    ).fetchone() is not None
                    if has_null_pname:
                        pnames.append(None)
                    for pname in pnames:
                        if pname is None:
                            sub = con.execute(
                                '''SELECT id, pipeline_run_id, parent_operation_id, dataset_id,
                                          pipeline_name, operation_name, status, outcome, background,
                                          error_class, origin, created_at, started_at, ended_at
                                   FROM pipeline_runs
                                   WHERE dataset_id = ? AND pipeline_name IS NULL
                                   ORDER BY created_at DESC
                                   LIMIT ?''',
                                (ds_var, limit + 1),
                            ).fetchall()
                        else:
                            sub = con.execute(
                                '''SELECT id, pipeline_run_id, parent_operation_id, dataset_id,
                                          pipeline_name, operation_name, status, outcome, background,
                                          error_class, origin, created_at, started_at, ended_at
                                   FROM pipeline_runs
                                   WHERE dataset_id = ? AND pipeline_name = ?
                                   ORDER BY created_at DESC
                                   LIMIT ?''',
                                (ds_var, pname, limit + 1),
                            ).fetchall()
                        for pr in sub:
                            candidates[str(pr[0])] = pr

    matched_rows = sorted(
        candidates.values(),
        key=lambda r: (str(r[11] or ''), str(r[0] or '')),
        reverse=True,
    )
    more = len(matched_rows) > limit
    fallback_ds = dataset_id or ''
    runs = [_format_pipeline_run_row(pr, fallback_ds) for pr in matched_rows[:limit]]
    if cache is not None and cache_lock is not None:
        with cache_lock:
            if len(cache) >= 32:
                cache.clear()
            cache[cache_key] = ([dict(item) for item in runs], more)
    return runs, more


class _SessionRelationsResolver:
    """Callable relation resolver with exact full-map Blake2b signature for ``CatalogIndex.sync``."""

    global_relation_sig = True

    def __init__(self, registry, codex_config):
        self._registry = registry
        self._codex_config = codex_config
        self._tracked_sig_cache: tuple[int, int, str] | None = None

    @staticmethod
    def _full_ordered_digest(persisted: dict) -> str:
        n = len(persisted)
        if n == 0:
            return '0'
        h = hashlib.blake2b(digest_size=16)
        for k in sorted(persisted):
            v = persisted[k]
            if isinstance(v, dict):
                h.update(
                    f"{k}|{v.get('parent_session_id','')}|{v.get('subagent_role','')}|{v.get('nesting_depth',0)}\n".encode('utf-8')
                )
            else:
                h.update(f'{k}|\n'.encode('utf-8'))
        return f'{n}:{h.hexdigest()}'

    def relation_sig(self, _dataset_id: str = '') -> str:
        state = getattr(self._registry, 'state', None)
        persisted = state.get('session_relations') if isinstance(state, dict) else None
        if isinstance(persisted, TrackedRelationsDict):
            pid = id(persisted)
            rev = persisted.revision
            cached = self._tracked_sig_cache
            if cached is not None and cached[0] == pid and cached[1] == rev:
                p_sig = cached[2]
            else:
                p_sig = self._full_ordered_digest(persisted)
                self._tracked_sig_cache = (pid, rev, p_sig)
        elif isinstance(persisted, dict):
            self._tracked_sig_cache = None
            p_sig = self._full_ordered_digest(persisted)
        else:
            self._tracked_sig_cache = None
            p_sig = '0'

        cfg = self._codex_config if isinstance(self._codex_config, dict) else {}
        host_parts = []
        for key in ('antigravity_db', 'codex_state_db', 'opencode_db', 'pi_sessions_dir'):
            val = cfg.get(key)
            if isinstance(val, (str, Path)) and str(val).strip():
                p = Path(str(val)).expanduser()
                try:
                    st = p.stat()
                    host_parts.append(f'{key}:{st.st_mtime_ns}:{st.st_size}')
                except OSError:
                    host_parts.append(f'{key}:0')
        return p_sig + '|' + ','.join(host_parts)

    def __call__(self, _dataset_id: str, session_ids: list[str]) -> dict:
        return resolve_session_relations(
            session_ids,
            registry=self._registry,
            codex_config=self._codex_config,
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
) -> dict:
    """Dual-branch indexed Keyset query + Python ``heapq.merge`` (zero full scan, zero TEMP B-TREE)."""
    cache_db = Path(official_data) / 'system' / 'databases' / 'cache.db'
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
    has_more = len(merged) > limit
    page = merged[:limit]

    # Hydrate ONLY the <= limit sessions that landed on this page (plus <=5 indexed preview children each)
    parent_preview_specs = [
        (entry['_raw_session']['session_id'], entry['_raw_session']['user_id'])
        for entry in page
        if entry['item_kind'] == 'session' and int(entry['_raw_session'].get('child_count') or 0) > 0
    ]
    previews_by_parent = (
        catalog_index.query_top_level_children_previews(dataset_id, parent_preview_specs, limit=5)
        if parent_preview_specs
        else {}
    )
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
        'total_count': int(c_page['total_count']),
        'items': page_items,
        'order_keys': order_keys,
        'has_more': bool(c_page['has_more']),
    }


def _recall_event_doc_ids(recall_store, dataset_id: str, agent_id: str, event_id: str) -> set[str]:
    if recall_store is None:
        return set()
    if hasattr(recall_store, 'event_doc_ids'):
        return recall_store.event_doc_ids(dataset_id, agent_id, event_id)
    event_uuid = recall_store.identifier(event_id)
    ds_uuid = recall_store.identifier(dataset_id)
    rows = recall_store.db.execute(
        '''SELECT document_id FROM recall_hit
           WHERE agent_id = ? AND event_id = ? AND dataset_id = ?''',
        (agent_id, event_uuid, ds_uuid),
    ).fetchall()
    return {str(r[0]) for r in rows}


def _read_recall_events_summary(recall_store, allowed_datasets: set[str], agents_state: dict, limit: int):
    if recall_store is None or not allowed_datasets:
        return [], False, ''
    page = recall_store.events(allowed_datasets, limit, 0)
    for item in page['items']:
        agent = agents_state.get(item['agent_id'], {})
        item['agent_name'] = agent.get('name') or 'Agent 未记录'
        item['agent_kind'] = agent.get('kind') or ''
    if hasattr(recall_store, 'started_at'):
        started_at = recall_store.started_at()
    else:
        started_row = recall_store.db.execute('SELECT started_at FROM recall_meta WHERE singleton=1').fetchone()
        started_at = str(started_row[0]) if started_row else ''
    return page['items'], bool(page['more']), started_at


async def _read_backlog_slim(backlog, project_id: str):
    if backlog is None:
        return None, False
    reader = getattr(backlog, 'get', None) or getattr(backlog, 'status', None)
    if reader is None:
        raise PolicyError('backlog_unavailable', 503)
    result = reader(project_id)
    if inspect.isawaitable(result):
        result = await result
    if not isinstance(result, dict):
        raise PolicyError('backlog_unavailable', 503)
    job = result.get('job')
    automatic_save = bool(result['automatic_save']) if 'automatic_save' in result else bool(
        result.get('automatic_enabled', False)
    )
    return job, automatic_save


class V2APIHandles:
    """Lifecycle handles for catalog index, reading state store, and late-bound backlog."""

    def __init__(self, catalog_index: CatalogIndex, reading_state: ReadingStateStore):
        self.catalog_index = catalog_index
        self.reading_state = reading_state
        self.backlog = None

    def set_backlog(self, backlog):
        self.backlog = backlog


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
    state_dir = registry.path.parent
    official_data = (codex_config or {}).get('official_data')
    provenance_path = state_dir / 'lesson-provenance.sqlite3'
    recall_path = state_dir / 'knowledge-recall.sqlite3'

    cat = catalog_index or CatalogIndex(
        state_dir / 'catalog-index.sqlite3',
        official_data=official_data,
        provenance_path=provenance_path,
        recall_path=recall_path,
    )
    read_store = reading_state or ReadingStateStore(
        state_dir / 'ui-reading-state.sqlite3',
        state_dir=state_dir,
    )
    hydrator = CacheSessionHydrator()
    rel_resolver = _SessionRelationsResolver(registry, codex_config)
    pipeline_runs_cache: dict = {}
    pipeline_runs_lock = threading.Lock()
    cursor_secret = (
        hashlib.sha256(('memlane-v2-cursor:' + manager_token).encode('utf-8')).digest()
        if manager_token
        else secrets.token_bytes(32)
    )
    cursors = CursorCodec(cursor_secret)
    handles = V2APIHandles(cat, read_store)
    handles.backlog = backlog
    handles.relations_resolver = rel_resolver
    db_executor = ThreadPoolExecutor(max_workers=4, thread_name_prefix='v2-api-read')

    async def _run_db(fn, *args, **kwargs):
        loop = asyncio.get_running_loop()
        if kwargs:
            return await loop.run_in_executor(db_executor, functools.partial(fn, *args, **kwargs))
        return await loop.run_in_executor(db_executor, fn, *args)

    def _require_manager(request):
        if not request['manager']:
            raise PolicyError('management_required', 403)

    def _reading_identity(request) -> str:
        raw = request.headers.get('X-Memlane-Reading-Identity', '')
        return raw if isinstance(raw, str) else ''

    async def _ensure_synced():
        if not official_data:
            raise PolicyError('official_store_unavailable', 503)
        projects, common = _verified_groups(registry)
        managed_ds = [p['dataset_id'] for p in projects]
        if common is not None:
            managed_ds.append(common['dataset_id'])
        if hasattr(cat, 'sync_async'):
            await cat.sync_async(managed_ds, relations_resolver=rel_resolver)
        else:
            await _run_db(cat.sync, managed_ds, relations_resolver=rel_resolver)
        return projects, common

    # 1. GET /v2/datasets/{ds}/documents/{doc}/body
    async def document_body(request):
        _require_manager(request)
        if request.query:
            raise PolicyError('invalid_request_fields', 400)
        ds_id = _require_verified_managed_dataset(registry, request.match_info['ds'], allow_code=True)
        raw_doc = request.match_info['doc']
        if not is_valid_uuid(raw_doc):
            raise PolicyError('invalid_document_id', 400)
        if not official_data:
            raise PolicyError('official_store_unavailable', 503)
        result = await read_document_body_with_fallback(
            official_data,
            ds_id,
            raw_doc,
            official=getattr(registry, 'official', None),
            max_bytes=MAX_BODY_BYTES,
        )
        return json_utf8_response(result)

    # 2. GET /v2/overview/bootstrap (<= 128 KiB serialized UTF-8 JSON budget)
    async def overview_bootstrap_v2(request):
        _require_manager(request)
        if set(request.query) - {'days', 'recent_limit', 'activity_limit', 'recall_limit'}:
            raise PolicyError('invalid_overview_query', 400)
        days = _parse_limit(request.query.get('days'), default=365, max_val=3650)
        recent_limit = _parse_limit(request.query.get('recent_limit'), default=20, max_val=20)
        activity_limit = _parse_limit(request.query.get('activity_limit'), default=20, max_val=50)
        recall_limit = _parse_limit(request.query.get('recall_limit'), default=20, max_val=50)
        identity = _reading_identity(request)

        authorized_ds = await fetch_authorized_official_datasets(getattr(registry, 'official', None))
        projects, common = await _ensure_synced()
        projects = [p for p in projects if p['dataset_id'] in authorized_ds]
        if common is not None and common['dataset_id'] not in authorized_ds:
            common = None

        allowed_ds = [p['dataset_id'] for p in projects]
        ds_to_proj = {p['dataset_id']: p['id'] for p in projects}
        allowed_run_ds = set(allowed_ds)
        for proj in projects:
            code_ds = proj.get('code_dataset_id')
            if proj.get('code_enabled') and code_ds and is_valid_uuid(str(code_ds)):
                code_canon = uuid_text(code_ds)
                if code_canon in authorized_ds:
                    allowed_run_ds.add(code_canon)
        if common is not None:
            allowed_ds.append(common['dataset_id'])
            ds_to_proj[common['dataset_id']] = ''
            allowed_run_ds.add(common['dataset_id'])

        agents_snapshot = {k: dict(v) for k, v in registry.state.get('agents', {}).items()}
        allowed_ds_set = set(allowed_ds)

        def _build_sync():
            meta = cat.meta()
            sync_map = cat.all_dataset_sync_info(allowed_ds)
            day_buckets = cat.overview_day_buckets(allowed_ds, days=days)
            recent_raw = cat.query_overview_recent(allowed_ds, limit=recent_limit)

            proj_summaries = []
            total_knowledge = 0
            total_raw_count = 0
            for proj in projects:
                ds = proj['dataset_id']
                sinfo = sync_map.get(ds) or {}
                k_cnt = int(sinfo.get('knowledge_count', 0))
                raw_cnt = int(sinfo.get('raw_doc_count', 0))
                sess_cnt = int(sinfo.get('session_count', 0))
                total_knowledge += k_cnt
                total_raw_count += sess_cnt + raw_cnt
                if identity:
                    u_ids, u_rev = read_store.compute_unread_snapshot(identity, proj['id'], ds, cat)
                    unread_cnt = len(u_ids)
                else:
                    unread_cnt = 0
                    u_rev = 0
                proj_summaries.append({
                    'project_id': proj['id'],
                    'dataset_id': ds,
                    'knowledge_count': k_cnt,
                    'raw_doc_count': raw_cnt,
                    'session_count': sess_cnt,
                    'unread_count': unread_cnt,
                    'unread_state_rev': u_rev,
                    'catalog_version': str(sinfo.get('catalog_version', '')),
                })

            common_summary = None
            if common is not None:
                cds = common['dataset_id']
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

            unread_flags = {}
            if identity and recent_raw:
                by_ds: dict[str, list[dict]] = {}
                for d in recent_raw:
                    by_ds.setdefault(d['dataset_id'], []).append(d)
                for ds, docs_in_ds in by_ds.items():
                    pid = ds_to_proj.get(ds, '')
                    if pid:
                        flags = read_store.page_unread_flags(identity, pid, ds, docs_in_ds, catalog_index=cat)
                        for did, flag in flags.items():
                            unread_flags[(ds, did)] = flag

            recent_items = [
                _format_knowledge_item(
                    d,
                    project_id=ds_to_proj.get(d['dataset_id'], ''),
                    is_unread=unread_flags.get((d['dataset_id'], d['doc_id']), False),
                    official_data=official_data,
                )
                for d in recent_raw
            ]
            try:
                runs, runs_more = _read_recent_pipeline_runs(
                    official_data,
                    allowed_datasets=allowed_run_ds,
                    limit=activity_limit,
                    cache=pipeline_runs_cache,
                    cache_lock=pipeline_runs_lock,
                )
            except (PolicyError, sqlite3.Error):
                runs, runs_more = [], False
            rec_events, rec_more, rec_started = _read_recall_events_summary(
                recall_store,
                allowed_ds_set,
                agents_snapshot,
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
        ) = await _run_db(_build_sync)
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

        payload, raw_bytes = finalize_budgeted_payload(
            _make_payload,
            recent_items,
            max_json_bytes=DEFAULT_OVERVIEW_JSON_BUDGET_BYTES,
            fixed_trimmer=_trim_overview,
        )
        return json_utf8_response(payload, raw_bytes=raw_bytes)

    # 3. GET /v2/overview/day
    async def overview_day_v2(request):
        _require_manager(request)
        if set(request.query) - {'day', 'limit', 'cursor'}:
            raise PolicyError('invalid_overview_query', 400)
        day = _calendar_date(request.query.get('day', ''))
        if not day:
            raise PolicyError('invalid_day_parameter', 400)
        limit = _parse_limit(request.query.get('limit'))
        raw_cursor = (request.query.get('cursor') or '').strip()
        identity = _reading_identity(request)

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
        projects, common = await _ensure_synced()
        projects = [p for p in projects if p['dataset_id'] in authorized_ds]
        if common is not None and common['dataset_id'] not in authorized_ds:
            common = None
        allowed_ds = [p['dataset_id'] for p in projects]
        ds_to_proj = {p['dataset_id']: p['id'] for p in projects}
        if common is not None:
            allowed_ds.append(common['dataset_id'])
            ds_to_proj[common['dataset_id']] = ''

        def _query():
            gen = cat.generation()
            rows, has_more, total_count = cat.query_overview_day(allowed_ds, day, limit=limit, after_key=after_key)
            unread_flags = {}
            if identity and rows:
                by_ds: dict[str, list[dict]] = {}
                for d in rows:
                    by_ds.setdefault(d['dataset_id'], []).append(d)
                for ds, docs_in_ds in by_ds.items():
                    pid = ds_to_proj.get(ds, '')
                    if pid:
                        for did, flag in read_store.page_unread_flags(identity, pid, ds, docs_in_ds, catalog_index=cat).items():
                            unread_flags[(ds, did)] = flag
            formatted = [
                _format_knowledge_item(
                    d,
                    project_id=ds_to_proj.get(d['dataset_id'], ''),
                    is_unread=unread_flags.get((d['dataset_id'], d['doc_id']), False),
                    official_data=official_data,
                )
                for d in rows
            ]
            return gen, formatted, has_more, total_count

        gen, formatted, base_has_more, total_count = await _run_db(_query)

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

        payload, raw_bytes = finalize_budgeted_payload(
            _make_payload,
            formatted,
            max_json_bytes=DEFAULT_LIST_JSON_BUDGET_BYTES,
        )
        return json_utf8_response(payload, raw_bytes=raw_bytes)

    # 4. GET /v2/projects/{id}/knowledge
    async def project_knowledge_v2(request):
        _require_manager(request)
        allowed_q = {
            'sort', 'category', 'unread_only', 'recall_agent_id',
            'recall_event_id', 'limit', 'cursor', 'direction', 'anchor_doc_id',
        }
        if set(request.query) - allowed_q:
            raise PolicyError('invalid_knowledge_query', 400)

        project_id = request.match_info['id']
        project = _require_verified_project(registry, project_id)
        dataset_id = project['dataset_id']

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

        identity = _reading_identity(request)
        if unread_only and not identity:
            raise PolicyError('reading_identity_required', 400)

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
        await _ensure_synced()

        def _query():
            recall_ids = None
            if recall_agent and recall_event:
                recall_ids = _recall_event_doc_ids(recall_store, dataset_id, recall_agent, recall_event)

            gen = cat.generation()
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
                    read_store.page_unread_flags(identity, project_id, dataset_id, page_rows, catalog_index=cat)
                    if identity
                    else {}
                )
            )
            formatted = [
                _format_knowledge_item(
                    d,
                    project_id=project_id,
                    is_unread=page_unread.get(d['doc_id'], False),
                    official_data=official_data,
                )
                for d in page_rows
            ]
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
                formatted,
                cursor_keys,
                bool(res['has_more']),
                bool(res['has_prev']),
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
        ) = await _run_db(_query)

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

        payload, raw_bytes = finalize_budgeted_payload(
            _make_payload,
            formatted,
            max_json_bytes=DEFAULT_LIST_JSON_BUDGET_BYTES,
            anchor_doc_id=(anchor_doc_id if direction != 'prev' else ''),
            trim_from_left=(direction == 'prev'),
        )
        return json_utf8_response(payload, raw_bytes=raw_bytes)

    # 5. GET /v2/common/knowledge
    async def common_knowledge_v2(request):
        _require_manager(request)
        if set(request.query) - {'dataset_id', 'common_category', 'limit', 'cursor', 'direction', 'anchor_doc_id'}:
            raise PolicyError('invalid_common_query', 400)
        raw_ds = (request.query.get('dataset_id') or '').strip()
        if raw_ds:
            dataset_id = _require_verified_managed_dataset(registry, raw_ds, allow_code=False)
        else:
            common = registry.state.get('common')
            if not isinstance(common, dict) or not common.get('dataset_id'):
                raise PolicyError('common_dataset_not_configured', 404)
            if common.get('status') != 'verified':
                raise PolicyError('project_unconfirmed', 403)
            dataset_id = common['dataset_id']

        common_cat = (request.query.get('common_category') or 'all').strip()
        if common_cat not in ('all', 'preference', 'experience'):
            raise PolicyError('invalid_common_category', 400)
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

        f_digest = _filter_digest(common_category=common_cat)
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
        await _ensure_synced()

        def _query():
            gen = cat.generation()
            sinfo = cat.dataset_sync_info(dataset_id) or {}
            cat_ver = str(sinfo.get('catalog_version', ''))
            res = cat.query_common_knowledge(
                dataset_id,
                common_category=common_cat,
                limit=limit,
                after_key=after_key,
                before_key=before_key,
                anchor_doc_id=anchor_doc_id,
                anchor_direction=direction,
            )
            if res is None:
                raise PolicyError('document_not_found', 404)
            rows = res['items']
            formatted = [
                _format_knowledge_item(
                    d,
                    project_id='',
                    is_unread=False,
                    official_data=official_data,
                )
                for d in rows
            ]
            ckeys = [[d['sort_ts_desc_key'], d['doc_id']] for d in rows]
            return (
                gen,
                cat_ver,
                res['total_filtered'],
                res['category_counts'],
                formatted,
                ckeys,
                bool(res['has_more']),
                bool(res['has_prev']),
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
        ) = await _run_db(_query)

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

        payload, raw_bytes = finalize_budgeted_payload(
            _make_payload,
            formatted,
            max_json_bytes=DEFAULT_LIST_JSON_BUDGET_BYTES,
            anchor_doc_id=(anchor_doc_id if direction != 'prev' else ''),
            trim_from_left=(direction == 'prev'),
        )
        return json_utf8_response(payload, raw_bytes=raw_bytes)

    # 6. GET /v2/search/recent
    async def search_recent_v2(request):
        _require_manager(request)
        if set(request.query) - {'limit', 'project_id', 'dataset_id'}:
            raise PolicyError('invalid_search_query', 400)
        limit = _parse_limit(request.query.get('limit'), default=20, max_val=_MAX_PAGE_LIMIT)
        project_id_filter = (request.query.get('project_id') or '').strip()
        dataset_id_filter = (request.query.get('dataset_id') or '').strip()
        if project_id_filter and dataset_id_filter:
            raise PolicyError('invalid_search_query', 400)
        identity = _reading_identity(request)

        if project_id_filter:
            proj = _require_verified_project(registry, project_id_filter)
            await verify_official_read_access(getattr(registry, 'official', None), proj['dataset_id'])
            await _ensure_synced()
            allowed_ds = [proj['dataset_id']]
            ds_to_proj = {proj['dataset_id']: proj['id']}
        elif dataset_id_filter:
            target_ds = _require_verified_managed_dataset(registry, dataset_id_filter, allow_code=False)
            await verify_official_read_access(getattr(registry, 'official', None), target_ds)
            projects, common = await _ensure_synced()
            ds_to_proj = {p['dataset_id']: p['id'] for p in projects}
            if common is not None:
                ds_to_proj[common['dataset_id']] = ''
            allowed_ds = [target_ds]
        else:
            authorized_ds = await fetch_authorized_official_datasets(getattr(registry, 'official', None))
            projects, common = await _ensure_synced()
            projects = [p for p in projects if p['dataset_id'] in authorized_ds]
            if common is not None and common['dataset_id'] not in authorized_ds:
                common = None
            allowed_ds = [p['dataset_id'] for p in projects]
            ds_to_proj = {p['dataset_id']: p['id'] for p in projects}
            if common is not None:
                allowed_ds.append(common['dataset_id'])
                ds_to_proj[common['dataset_id']] = ''

        def _query():
            gen = cat.generation()
            k_rows = cat.query_overview_recent(allowed_ds, limit=limit)
            unread_flags = {}
            if identity and k_rows:
                by_ds: dict[str, list[dict]] = {}
                for d in k_rows:
                    by_ds.setdefault(d['dataset_id'], []).append(d)
                for ds, docs_in_ds in by_ds.items():
                    pid = ds_to_proj.get(ds, '')
                    if pid:
                        for did, flag in read_store.page_unread_flags(identity, pid, ds, docs_in_ds, catalog_index=cat).items():
                            unread_flags[(ds, did)] = flag
            k_items = [
                _format_knowledge_item(
                    d,
                    project_id=ds_to_proj.get(d['dataset_id'], ''),
                    is_unread=unread_flags.get((d['dataset_id'], d['doc_id']), False),
                    official_data=official_data,
                )
                for d in k_rows
            ]
            # Indexed query on catalog_sessions (zero TEMP B-TREE) + cache.db hydration
            s_rows = cat.query_recent_sessions(allowed_ds, limit=limit)
            s_items = [
                {
                    'session_id': sr['session_id'],
                    'dataset_id': sr['dataset_id'],
                    'project_id': ds_to_proj.get(sr['dataset_id'], ''),
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

        gen, k_items, s_items = await _run_db(_query)

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

        payload, raw_bytes = finalize_budgeted_payload(
            _make_payload,
            k_items,
            max_json_bytes=DEFAULT_OVERVIEW_JSON_BUDGET_BYTES,
            fixed_trimmer=_trim_recent,
        )
        return json_utf8_response(payload, raw_bytes=raw_bytes)

    # 7. GET /v2/search/metadata (Channel 1 instant metadata search)
    async def search_metadata_v2(request):
        _require_manager(request)
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
        identity = _reading_identity(request)

        if project_id_filter:
            proj = _require_verified_project(registry, project_id_filter)
            await verify_official_read_access(getattr(registry, 'official', None), proj['dataset_id'])
            await _ensure_synced()
            allowed_ds = [proj['dataset_id']]
            ds_to_proj = {proj['dataset_id']: proj['id']}
        elif dataset_id_filter:
            target_ds = _require_verified_managed_dataset(registry, dataset_id_filter, allow_code=False)
            await verify_official_read_access(getattr(registry, 'official', None), target_ds)
            projects, common = await _ensure_synced()
            ds_to_proj = {p['dataset_id']: p['id'] for p in projects}
            if common is not None:
                ds_to_proj[common['dataset_id']] = ''
            allowed_ds = [target_ds]
        else:
            authorized_ds = await fetch_authorized_official_datasets(getattr(registry, 'official', None))
            projects, common = await _ensure_synced()
            projects = [p for p in projects if p['dataset_id'] in authorized_ds]
            if common is not None and common['dataset_id'] not in authorized_ds:
                common = None
            allowed_ds = [p['dataset_id'] for p in projects]
            ds_to_proj = {p['dataset_id']: p['id'] for p in projects}
            if common is not None:
                allowed_ds.append(common['dataset_id'])
                ds_to_proj[common['dataset_id']] = ''

        def _query():
            gen = cat.generation()
            rows, has_more = cat.query_search_metadata(
                allowed_ds,
                query_text,
                filter_kind=filter_kind,
                limit=limit,
            )
            unread_flags = {}
            if identity and rows:
                by_ds: dict[str, list[dict]] = {}
                for d in rows:
                    if d['is_knowledge']:
                        by_ds.setdefault(d['dataset_id'], []).append(d)
                for ds, docs_in_ds in by_ds.items():
                    pid = ds_to_proj.get(ds, '')
                    if pid:
                        for did, flag in read_store.page_unread_flags(identity, pid, ds, docs_in_ds, catalog_index=cat).items():
                            unread_flags[(ds, did)] = flag
            formatted = [
                _format_knowledge_item(
                    d,
                    project_id=ds_to_proj.get(d['dataset_id'], ''),
                    is_unread=unread_flags.get((d['dataset_id'], d['doc_id']), False),
                    official_data=official_data,
                )
                for d in rows
            ]
            return gen, formatted, has_more

        gen, formatted, base_has_more = await _run_db(_query)

        def _make_payload(subset, _lo, hi):
            return {
                'query': query_text,
                'generation': gen,
                'items': subset,
                'has_more': bool(base_has_more or hi < len(formatted)),
            }

        payload, raw_bytes = finalize_budgeted_payload(
            _make_payload,
            formatted,
            max_json_bytes=DEFAULT_LIST_JSON_BUDGET_BYTES,
        )
        return json_utf8_response(payload, raw_bytes=raw_bytes)

    # 8. GET /v2/projects/{id}/raw-records
    async def raw_records_v2(request):
        _require_manager(request)
        if set(request.query) - {'kind', 'limit', 'cursor'}:
            raise PolicyError('invalid_raw_records_query', 400)
        project_id = request.match_info['id']
        project = _require_verified_project(registry, project_id)
        dataset_id = project['dataset_id']
        kind = (request.query.get('kind') or 'all').strip()
        if kind not in ('all', 'session', 'document'):
            raise PolicyError('invalid_raw_records_query', 400)
        limit = _parse_limit(request.query.get('limit'))
        raw_cursor = (request.query.get('cursor') or '').strip()

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
        await _ensure_synced()

        def _query():
            gen = cat.generation()
            res = _query_raw_records_v2(
                official_data,
                cat,
                hydrator,
                dataset_id,
                kind=kind,
                limit=limit,
                after_order_key=after_key,
            )
            return gen, res

        gen, res = await _run_db(_query)
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

        payload, raw_bytes = finalize_budgeted_payload(
            _make_payload,
            res['items'],
            max_json_bytes=DEFAULT_LIST_JSON_BUDGET_BYTES,
        )
        return json_utf8_response(payload, raw_bytes=raw_bytes)

    # 9. GET /v2/projects/{id}/raw-records/sessions/{sid}/children
    async def raw_records_session_children_v2(request):
        _require_manager(request)
        if set(request.query) - {'limit', 'cursor', 'user_id'}:
            raise PolicyError('invalid_raw_records_query', 400)
        project_id = request.match_info['id']
        project = _require_verified_project(registry, project_id)
        dataset_id = project['dataset_id']
        parent_sid = request.match_info['sid']
        if not _safe_session_id(parent_sid):
            raise PolicyError('invalid_session_id', 400)
        user_id = (request.query.get('user_id') or '').strip()
        if user_id and not is_valid_uuid(user_id):
            raise PolicyError('invalid_user_id', 400)
        limit = _parse_limit(request.query.get('limit'))
        raw_cursor = (request.query.get('cursor') or '').strip()

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
        await _ensure_synced()

        def _query():
            gen = cat.generation()
            res = _query_session_children_v2(
                official_data,
                cat,
                hydrator,
                dataset_id,
                parent_sid,
                user_id=user_id,
                limit=limit,
                after_key=after_key,
            )
            return gen, res

        gen, res = await _run_db(_query)
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

        payload, raw_bytes = finalize_budgeted_payload(
            _make_payload,
            res['items'],
            max_json_bytes=DEFAULT_LIST_JSON_BUDGET_BYTES,
        )
        return json_utf8_response(payload, raw_bytes=raw_bytes)

    # 10. GET /v2/projects/{id}/message-summary (slim < 16 KiB, zero full knowledge_catalog)
    async def message_summary_v2(request):
        _require_manager(request)
        if request.query:
            raise PolicyError('invalid_summary_query', 400)
        project_id = request.match_info['id']
        project = _require_verified_project(registry, project_id)
        dataset_id = project['dataset_id']
        identity = _reading_identity(request)
        await verify_official_read_access(getattr(registry, 'official', None), dataset_id)

        errors = {}
        backlog_job = None
        automatic_save = False
        notices = []

        async def _load_backlog():
            return await _read_backlog_slim(handles.backlog, project_id)

        async def _load_db_sections():
            sync_err = None
            try:
                await _ensure_synced()
            except BaseException as exc:
                if isinstance(exc, asyncio.CancelledError):
                    raise
                sync_err = exc

            def _run_all():
                n_val = []
                n_err = None
                try:
                    n_val = processing_notices(state_dir / 'runtime-logs', dataset_id)
                except Exception as exc:
                    n_err = exc

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
                    return n_val, n_err, a_val, a_err, None, sync_err

                try:
                    sinfo = cat.dataset_sync_info(dataset_id) or {}
                    cat_ver_l = str(sinfo.get('catalog_version', ''))
                    pending_rows, pending_total_l = cat.query_pending_raw_docs(dataset_id, limit=20)
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
                except Exception as exc:
                    return n_val, n_err, a_val, a_err, None, exc

                reading_err_l = None
                unread_count_l = 0
                unread_state_rev_l = 0
                unread_preview_l = []
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
                            for d in top_docs[:10]:
                                preview_snip = ''
                                if official_data and d.get('raw_data_location'):
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

                return (
                    n_val,
                    n_err,
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

            return await _run_db(_run_all)

        b_res, db_res = await asyncio.gather(
            _load_backlog(),
            _load_db_sections(),
            return_exceptions=True,
        )

        if isinstance(b_res, asyncio.CancelledError):
            raise b_res
        if isinstance(b_res, BaseException):
            errors['backlog'] = b_res.code if isinstance(b_res, PolicyError) else 'backlog_unavailable'
        else:
            backlog_job, automatic_save = b_res

        if isinstance(db_res, asyncio.CancelledError):
            raise db_res
        if isinstance(db_res, BaseException):
            n_res = db_res
            a_res = db_res
            d_res = db_res
        else:
            n_val, n_err, a_val, a_err, d_val, d_err = db_res
            n_res = n_err if n_err is not None else n_val
            a_res = a_err if a_err is not None else a_val
            d_res = d_err if d_err is not None else d_val

        if isinstance(n_res, asyncio.CancelledError):
            raise n_res
        if isinstance(n_res, BaseException):
            errors['processing_notices'] = (
                n_res.code if isinstance(n_res, PolicyError) else 'processing_diagnostics_unavailable'
            )
        else:
            notices = n_res

        cat_ver = ''
        unread_count = 0
        unread_state_rev = 0
        unread_preview = []
        pending_total = 0
        pending_docs = []
        if isinstance(d_res, asyncio.CancelledError):
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
            return {
                'project_id': project_id,
                'dataset_id': dataset_id,
                'automatic_save': automatic_save,
                'backlog': dict(backlog_job) if isinstance(backlog_job, dict) else backlog_job,
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
            if isinstance(p.get('backlog'), dict):
                b = p['backlog']
                for k in ('last_error', 'message', 'detail'):
                    if isinstance(b.get(k), str) and len(b[k]) > 64:
                        b[k] = b[k][:64]
                        return True
            return False

        payload, raw_bytes = finalize_budgeted_payload(
            _make_summary_payload,
            [],
            max_json_bytes=_MAX_MESSAGE_SUMMARY_BYTES,
            fixed_trimmer=_trim_summary,
        )
        return json_utf8_response(payload, raw_bytes=raw_bytes)

    # 11. Reading State Write & Migration Routes
    async def _json_body(request):
        if request.content_type != 'application/json':
            raise PolicyError('json_required', 415)
        data = await request.json()
        if not isinstance(data, dict):
            raise PolicyError('invalid_request_fields', 400)
        return data

    async def reading_migrate_local(request):
        _require_manager(request)
        body = await _json_body(request)
        if set(body) - {'reading_identity', 'projects'} or 'reading_identity' not in body:
            raise PolicyError('invalid_request_fields', 400)
        identity = body['reading_identity']
        if not isinstance(identity, str) or not identity:
            raise PolicyError('invalid_reading_identity', 400)

        req_projects = body.get('projects')
        targets = []
        if req_projects is None:
            verified_projs, _ = _verified_groups(registry)
            targets = [(p['id'], p['dataset_id']) for p in verified_projs]
        else:
            if not isinstance(req_projects, list):
                raise PolicyError('invalid_request_fields', 400)
            for item in req_projects:
                if not isinstance(item, dict) or set(item) != {'project_id', 'dataset_id'}:
                    raise PolicyError('invalid_request_fields', 400)
                proj = _require_verified_project(registry, str(item['project_id']))
                if proj['dataset_id'] != uuid_text(item['dataset_id']):
                    raise PolicyError('dataset_access_denied', 403)
                targets.append((proj['id'], proj['dataset_id']))

        synced = False
        if official_data:
            try:
                await _ensure_synced()
                synced = True
            except PolicyError:
                synced = False

        def _migrate():
            migrated = []
            for pid, ds_id in targets:
                res = read_store.migrate_local_file(
                    state_dir,
                    identity,
                    pid,
                    ds_id,
                    catalog_index=cat if synced else None,
                )
                # CRITICAL (PY-P0-04): NEVER call compute_unread_ids when status == 'no_legacy_file',
                # as ensure_scope would pre-create an empty scope before custom-dir import!
                if synced and res.get('status') in ('migrated', 'already_migrated'):
                    unread_cnt = len(read_store.compute_unread_ids(identity, pid, ds_id, cat))
                else:
                    unread_cnt = 0
                res['unread_count'] = unread_cnt
                migrated.append(res)
            return migrated

        return json_utf8_response({'migrated': await _run_db(_migrate)})

    async def reading_import_begin(request):
        _require_manager(request)
        body = await _json_body(request)
        if set(body) != {'reading_identity', 'project_id', 'dataset_id', 'initialized', 'enabled_at'}:
            raise PolicyError('invalid_request_fields', 400)
        identity = body['reading_identity']
        if not isinstance(identity, str) or not identity:
            raise PolicyError('invalid_reading_identity', 400)
        if type(body['initialized']) is not bool:
            raise PolicyError('invalid_request_fields', 400)
        proj = _require_verified_project(registry, str(body['project_id']))
        if proj['dataset_id'] != uuid_text(body['dataset_id']):
            raise PolicyError('dataset_access_denied', 403)
        res = await _run_db(
            read_store.begin_import,
            identity,
            proj['id'],
            proj['dataset_id'],
            initialized=body['initialized'],
            enabled_at=str(body['enabled_at'] or ''),
        )
        return json_utf8_response(res)

    async def reading_import_chunk(request):
        _require_manager(request)
        body = await _json_body(request)
        if set(body) != {'batch_id', 'items'}:
            raise PolicyError('invalid_request_fields', 400)
        res = await _run_db(read_store.stage_import_chunk, str(body['batch_id']), body['items'])
        return json_utf8_response(res)

    async def reading_import_commit(request):
        _require_manager(request)
        body = await _json_body(request)
        if set(body) - {'batch_id', 'expected_items'} or 'batch_id' not in body:
            raise PolicyError('invalid_request_fields', 400)
        expected_items = None
        if 'expected_items' in body and body['expected_items'] is not None:
            if type(body['expected_items']) is not int or body['expected_items'] < 0:
                raise PolicyError('invalid_request_fields', 400)
            expected_items = body['expected_items']

        def _verify_project(pid: str, ds_id: str):
            proj = _require_verified_project(registry, pid)
            if proj['dataset_id'] != uuid_text(ds_id):
                raise PolicyError('dataset_access_denied', 403)

        synced = False
        if official_data:
            try:
                await _ensure_synced()
                synced = True
            except PolicyError:
                synced = False
        res = await _run_db(
            read_store.commit_import,
            str(body['batch_id']),
            catalog_index=cat if synced else None,
            expected_items=expected_items,
            verify_project=_verify_project,
        )
        return json_utf8_response({
            'scope_hash': res['scope_hash'],
            'committed': res['committed'],
            'unread_count': int(res.get('unread_count', 0)),
            'state_rev': int(res.get('state_rev', 0)),
        })

    async def reading_mark_read(request):
        _require_manager(request)
        body = await _json_body(request)
        if set(body) != {'reading_identity', 'project_id', 'dataset_id', 'doc_ids'}:
            raise PolicyError('invalid_request_fields', 400)
        identity = body['reading_identity']
        if not isinstance(identity, str) or not identity:
            raise PolicyError('invalid_reading_identity', 400)
        doc_ids = body['doc_ids']
        if not isinstance(doc_ids, list) or not 1 <= len(doc_ids) <= 200:
            raise PolicyError('invalid_doc_ids', 400)
        proj = _require_verified_project(registry, str(body['project_id']))
        if proj['dataset_id'] != uuid_text(body['dataset_id']):
            raise PolicyError('dataset_access_denied', 403)
        synced = False
        if official_data:
            try:
                await _ensure_synced()
                synced = True
            except PolicyError:
                synced = False
        res = await _run_db(
            read_store.mark_read,
            identity,
            proj['id'],
            proj['dataset_id'],
            doc_ids,
            catalog_index=cat if synced else None,
        )
        return json_utf8_response(res)

    async def reading_mark_all_read(request):
        _require_manager(request)
        body = await _json_body(request)
        if set(body) != {'reading_identity', 'project_id', 'dataset_id'}:
            raise PolicyError('invalid_request_fields', 400)
        identity = body['reading_identity']
        if not isinstance(identity, str) or not identity:
            raise PolicyError('invalid_reading_identity', 400)
        proj = _require_verified_project(registry, str(body['project_id']))
        if proj['dataset_id'] != uuid_text(body['dataset_id']):
            raise PolicyError('dataset_access_denied', 403)
        synced = False
        if official_data:
            try:
                await _ensure_synced()
                synced = True
            except PolicyError:
                synced = False
        res = await _run_db(
            read_store.mark_all_read,
            identity,
            proj['id'],
            proj['dataset_id'],
            catalog_index=cat if synced else None,
        )
        return json_utf8_response(res)

    # 12. POST /v2/provenance/backfill-legacy-dates (single-writer via official_service_dev)
    _LEGACY_KEY_RE = re.compile(r'^[0-9a-f]{64}$')

    async def provenance_backfill_legacy_dates(request):
        _require_manager(request)
        body = await _json_body(request)
        if set(body) - {'reading_identity', 'days'} or 'reading_identity' not in body:
            raise PolicyError('invalid_request_fields', 400)
        identity = body['reading_identity']
        if not isinstance(identity, str) or not identity:
            raise PolicyError('invalid_reading_identity', 400)

        has_unconfirmed = _has_unconfirmed_groups(registry)
        legacy_path = None
        if 'days' in body:
            raw_days = body['days']
            if not isinstance(raw_days, dict) or len(raw_days) > 200000:
                raise PolicyError('invalid_legacy_dates', 400)
            days = {}
            for k, v in raw_days.items():
                if not isinstance(k, str) or not _LEGACY_KEY_RE.match(k):
                    raise PolicyError('invalid_legacy_dates', 400)
                day = _calendar_date(v)
                if not day:
                    raise PolicyError('invalid_legacy_dates', 400)
                days[k] = day
        else:
            digest = hashlib.sha256(identity.encode('utf-8')).hexdigest()
            legacy_path = state_dir / f'knowledge-source-dates-{digest}.json'
            if not legacy_path.exists():
                return json_utf8_response({
                    'status': 'absent',
                    'matched': 0,
                    'imported': 0,
                    'skipped': 0,
                    'unmapped': 0,
                    'has_unconfirmed_projects': has_unconfirmed,
                    'backup_path': '',
                })
            try:
                st = os.lstat(legacy_path)
            except OSError as exc:
                raise PolicyError('invalid_legacy_dates', 400) from exc
            if legacy_path.is_symlink() or (st.st_mode & 0o077) != 0 or st.st_size > 8 * 1024 * 1024:
                raise PolicyError('invalid_legacy_dates', 400)
            try:
                parsed = json.loads(legacy_path.read_text(encoding='utf-8'))
            except (OSError, ValueError) as exc:
                raise PolicyError('invalid_legacy_dates', 400) from exc
            if (
                not isinstance(parsed, dict)
                or parsed.get('schema_version') != 1
                or not isinstance(parsed.get('days'), dict)
                or len(parsed['days']) > 200000
            ):
                raise PolicyError('invalid_legacy_dates', 400)
            days = {}
            for k, v in parsed['days'].items():
                if not isinstance(k, str) or not _LEGACY_KEY_RE.match(k):
                    raise PolicyError('invalid_legacy_dates', 400)
                day = _calendar_date(v)
                if not day:
                    raise PolicyError('invalid_legacy_dates', 400)
                days[k] = day

        if not days:
            backup_path = ''
            if legacy_path is not None and not has_unconfirmed:
                bak = legacy_path.with_name(legacy_path.name + '.migrated.bak')
                os.replace(legacy_path, bak)
                backup_path = str(bak)
            return json_utf8_response({
                'status': 'empty',
                'matched': 0,
                'imported': 0,
                'skipped': 0,
                'unmapped': 0,
                'has_unconfirmed_projects': has_unconfirmed,
                'backup_path': backup_path,
            })

        official_client = getattr(registry, 'official', None)
        if official_client is None:
            raise PolicyError('provenance_backfill_unavailable', 503)

        projects, common = await _ensure_synced()
        allowed_ds = [p['dataset_id'] for p in projects]
        if common is not None:
            allowed_ds.append(common['dataset_id'])

        def _resolve_entries():
            entries = []
            matched_keys = set()
            if not allowed_ds:
                return entries, len(days)
            for ds_s, doc_s, lt_s, sk_s in cat.iter_knowledge_provenance_keys(allowed_ds):
                stable = f'{lt_s}/{sk_s}' if sk_s else doc_s
                primary_key = hashlib.sha256(f'{ds_s}/{stable}'.encode('utf-8')).hexdigest()
                found = []
                if primary_key in days:
                    found.append(days[primary_key])
                    matched_keys.add(primary_key)
                if sk_s:
                    fallback_key = hashlib.sha256(f'{ds_s}/{doc_s}'.encode('utf-8')).hexdigest()
                    if fallback_key != primary_key and fallback_key in days:
                        found.append(days[fallback_key])
                        matched_keys.add(fallback_key)
                if found:
                    entries.append({
                        'dataset_id': ds_s,
                        'document_id': doc_s,
                        'lesson_type': lt_s,
                        'state_key': sk_s,
                        'first_source_date': min(found),
                    })
            return entries, len(days) - len(matched_keys)

        entries, unmapped_count = await _run_db(_resolve_entries)

        total_imported = 0
        total_server_skipped = 0
        chunk_size = 500
        for start in range(0, len(entries), chunk_size):
            chunk = entries[start : start + chunk_size]
            try:
                resp = await official_client.request(
                    'POST',
                    '/api/v1/memlane/provenance/backfill-legacy-dates',
                    {'entries': chunk},
                    adapter_token=manager_token,
                )
            except PolicyError as exc:
                raise PolicyError('provenance_backfill_unavailable', 503) from exc
            if not isinstance(resp, dict):
                raise PolicyError('provenance_backfill_unavailable', 503)
            total_imported += int(resp.get('imported') or 0)
            total_server_skipped += int(resp.get('skipped') or 0)

        # Contract #6 (GO-P1-07 / CC-P1-04): NEVER rename legacy .json to .migrated.bak while
        # unmapped entries remain or any project/common group is still unconfirmed!
        backup_path = ''
        if legacy_path is not None and unmapped_count == 0 and not has_unconfirmed:
            bak = legacy_path.with_name(legacy_path.name + '.migrated.bak')
            os.replace(legacy_path, bak)
            backup_path = str(bak)

        return json_utf8_response({
            'status': 'migrated' if (unmapped_count == 0 and not has_unconfirmed) else 'partial',
            'matched': len(entries),
            'imported': total_imported,
            'skipped': unmapped_count + total_server_skipped,
            'unmapped': unmapped_count,
            'has_unconfirmed_projects': has_unconfirmed,
            'backup_path': backup_path,
        })

    app.add_routes([
        web.get('/v2/datasets/{ds}/documents/{doc}/body', document_body),
        web.get('/v2/overview/bootstrap', overview_bootstrap_v2),
        web.get('/v2/overview/day', overview_day_v2),
        web.get('/v2/projects/{id}/knowledge', project_knowledge_v2),
        web.get('/v2/common/knowledge', common_knowledge_v2),
        web.get('/v2/search/recent', search_recent_v2),
        web.get('/v2/search/metadata', search_metadata_v2),
        web.get('/v2/projects/{id}/raw-records', raw_records_v2),
        web.get('/v2/projects/{id}/raw-records/sessions/{sid}/children', raw_records_session_children_v2),
        web.get('/v2/projects/{id}/message-summary', message_summary_v2),
        web.post('/v2/reading-state/migrate-local', reading_migrate_local),
        web.post('/v2/reading-state/import/begin', reading_import_begin),
        web.post('/v2/reading-state/import/chunk', reading_import_chunk),
        web.post('/v2/reading-state/import/commit', reading_import_commit),
        web.post('/v2/reading-state/mark-read', reading_mark_read),
        web.post('/v2/reading-state/mark-all-read', reading_mark_all_read),
        web.post('/v2/provenance/backfill-legacy-dates', provenance_backfill_legacy_dates),
    ])

    async def cleanup(_app):
        db_executor.shutdown(wait=True)
        hydrator.close()
        close_pooled_official_sqlite_ro()
        if catalog_index is None:
            cat.close()
        if reading_state is None:
            read_store.close()

    app.on_cleanup.append(cleanup)
    return handles
