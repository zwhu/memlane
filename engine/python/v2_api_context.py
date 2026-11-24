"""Runtime dependencies and shared authorization for the v1 API."""
import asyncio
import base64
from collections.abc import Callable
from concurrent.futures import ThreadPoolExecutor
import functools
import hashlib
import hmac
import inspect
import json
from pathlib import Path
import re
import secrets
import threading

from cache_read_v2 import CacheSessionHydrator
from catalog_index import CatalogIndex, _file_stat_sig
from local_body_reader import (
    fetch_authorized_official_datasets,
    read_local_preview_snippet,
    verify_official_read_access,
)
from project_policy import PolicyError
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
from v2_lookups import (
    READING_CONTRACT,
    READING_CONTRACT_HEADER,
    _require_verified_managed_dataset,
)


PayloadBuilder = Callable[[list[dict], int, int], dict]
PayloadTrimmer = Callable[[dict], bool]

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
    automatic_save = bool(result['automatic_save']) if 'automatic_save' in result else bool(
        result.get('automatic_enabled', False)
    )
    return (
        job, automatic_save,
        *(result[key] if isinstance(result.get(key), list) else []
          for key in ('buffer_reviews', 'checks', 'processing_issues')),
    )


class V2APIHandles:
    """Lifecycle handles for catalog index, reading state store, and late-bound backlog."""

    def __init__(self, catalog_index: CatalogIndex, reading_state: ReadingStateStore):
        self.catalog_index = catalog_index
        self.reading_state = reading_state
        self.backlog = None

    def set_backlog(self, backlog):
        self.backlog = backlog


def _resolve_common_dataset(registry, raw_ds: str) -> str:
    if raw_ds:
        return _require_verified_managed_dataset(registry, raw_ds, allow_code=False)
    common = registry.state.get('common')
    if not isinstance(common, dict) or not common.get('dataset_id'):
        raise PolicyError('common_dataset_not_configured', 404)
    if common.get('status') != 'verified':
        raise PolicyError('project_unconfirmed', 403)
    return common['dataset_id']


class V2ApiContext:
    """Runtime dependencies and shared authorization for the v1 handlers."""

    def __init__(
        self,
        registry,
        *,
        manager_token: str = '',
        recall_store=None,
        codex_config=None,
        backlog=None,
        catalog_index: CatalogIndex | None = None,
        reading_state: ReadingStateStore | None = None,
    ):
        self.state_dir = registry.path.parent
        self.official_data = (codex_config or {}).get('official_data')
        self.registry = registry
        self.manager_token = manager_token
        self.recall_store = recall_store
        self.codex_config = codex_config
        self.cat = catalog_index or CatalogIndex(
            self.state_dir / 'catalog-index.sqlite3',
            official_data=self.official_data,
            provenance_path=self.state_dir / 'lesson-provenance.sqlite3',
            recall_path=self.state_dir / 'knowledge-recall.sqlite3',
        )
        self.read_store = reading_state or ReadingStateStore(
            self.state_dir / 'ui-reading-state.sqlite3',
            state_dir=self.state_dir,
        )
        self.hydrator = CacheSessionHydrator()
        self.rel_resolver = _SessionRelationsResolver(registry, codex_config)
        self.pipeline_runs_cache: dict = {}
        self.pipeline_runs_lock = threading.Lock()
        cursor_secret = (
            hashlib.sha256(('memlane-v2-cursor:' + manager_token).encode('utf-8')).digest()
            if manager_token
            else secrets.token_bytes(32)
        )
        self.cursors = CursorCodec(cursor_secret)
        self.handles = V2APIHandles(self.cat, self.read_store)
        self.handles.backlog = backlog
        self.handles.relations_resolver = self.rel_resolver
        self.db_executor = ThreadPoolExecutor(max_workers=4, thread_name_prefix='v2-api-read')
        self.owns_catalog = catalog_index is None
        self.owns_reading_state = reading_state is None

    async def run_db(self, fn, *args, **kwargs):
        loop = asyncio.get_running_loop()
        if kwargs:
            return await loop.run_in_executor(self.db_executor, functools.partial(fn, *args, **kwargs))
        return await loop.run_in_executor(self.db_executor, fn, *args)

    def query_catalog_snapshot(self, query):
        gen = self.cat.generation()
        try:
            result = query(gen)
        except PolicyError:
            if self.cat.generation() == gen:
                raise
            raise PolicyError('catalog_generation_conflict', 409)
        if self.cat.generation() == gen:
            return result
        raise PolicyError('catalog_generation_conflict', 409)

    @staticmethod
    def require_manager(request):
        if not request['manager']:
            raise PolicyError('management_required', 403)

    @staticmethod
    def require_reading_contract(request):
        if request.headers.get(READING_CONTRACT_HEADER) != READING_CONTRACT:
            raise PolicyError('reading_contract_mismatch', 409)

    @staticmethod
    def reading_identity(request) -> str:
        raw = request.headers.get('X-Memlane-Reading-Identity', '')
        return raw if isinstance(raw, str) else ''

    @staticmethod
    async def json_body(request):
        if request.content_type != 'application/json':
            raise PolicyError('json_required', 415)
        data = await request.json()
        if not isinstance(data, dict):
            raise PolicyError('invalid_request_fields', 400)
        return data

    async def ensure_synced(self, *, fresh: bool = False):
        if not self.official_data:
            raise PolicyError('official_store_unavailable', 503)
        projects, common = _verified_groups(self.registry)
        managed_ds = [p['dataset_id'] for p in projects]
        if common is not None:
            managed_ds.append(common['dataset_id'])
        if not fresh and hasattr(self.cat, 'sync_for_read_async'):
            await self.cat.sync_for_read_async(managed_ds, relations_resolver=self.rel_resolver)
        elif hasattr(self.cat, 'sync_async'):
            await self.cat.sync_async(managed_ds, relations_resolver=self.rel_resolver)
        else:
            await self.run_db(self.cat.sync, managed_ds, relations_resolver=self.rel_resolver)
        return _verified_groups(self.registry)

    async def run_read_query(self, query, *args, missing_code=None, revalidate=None):
        check_local = getattr(revalidate, 'check_local', None)
        if check_local is not None:
            check_local()
        conflict_retries = 0
        sync_retried = False
        while True:
            try:
                return await self.run_db(query, *args)
            except PolicyError as exc:
                is_conflict = exc.code == 'catalog_generation_conflict' and exc.status == 409
                if is_conflict and conflict_retries < 2:
                    conflict_retries += 1
                    if revalidate is not None:
                        await revalidate()
                    continue
                is_rebuilding = exc.code == 'catalog_index_rebuilding' and exc.status == 503
                is_missing = bool(missing_code and exc.code == missing_code and exc.status == 404)
                if (is_rebuilding or is_missing) and not sync_retried:
                    sync_retried = True
                    if revalidate is not None:
                        await revalidate()
                    await self.ensure_synced(fresh=True)
                    if revalidate is not None:
                        await revalidate()
                    continue
                raise

    def project_revalidator(self, project_id: str, expected_dataset_id: str):
        def _check_local():
            proj = _require_verified_project(self.registry, project_id)
            if proj['dataset_id'] != expected_dataset_id:
                raise PolicyError('dataset_access_denied', 403)

        async def _revalidate():
            _check_local()
            await verify_official_read_access(getattr(self.registry, 'official', None), expected_dataset_id)
            _check_local()

        _revalidate.check_local = _check_local
        return _revalidate

    def common_revalidator(self, raw_ds: str, expected_dataset_id: str):
        def _check_local():
            cur_ds = _resolve_common_dataset(self.registry, raw_ds)
            if cur_ds != expected_dataset_id:
                raise PolicyError('dataset_access_denied', 403)

        async def _revalidate():
            _check_local()
            await verify_official_read_access(getattr(self.registry, 'official', None), expected_dataset_id)
            _check_local()

        _revalidate.check_local = _check_local
        return _revalidate

    def managed_dataset_revalidator(self, raw_ds: str, expected_dataset_id: str, ds_to_proj_out: dict | None = None):
        def _check_local():
            cur_ds = _require_verified_managed_dataset(self.registry, raw_ds, allow_code=False)
            if cur_ds != expected_dataset_id:
                raise PolicyError('dataset_access_denied', 403)

        async def _revalidate():
            _check_local()
            await verify_official_read_access(getattr(self.registry, 'official', None), expected_dataset_id)
            _check_local()
            if ds_to_proj_out is not None:
                projs_v, common_v = _verified_groups(self.registry)
                ds_to_proj_out.clear()
                for p in projs_v:
                    ds_to_proj_out[p['dataset_id']] = p['id']
                if common_v is not None:
                    ds_to_proj_out[common_v['dataset_id']] = ''

        _revalidate.check_local = _check_local
        return _revalidate

    def populate_authorized_scope(self, scope: dict, authorized_ds: set[str], projects_v: list[dict], common_v: dict | None, *, include_runs: bool = False):
        projs = [p for p in projects_v if p['dataset_id'] in authorized_ds]
        com = common_v if (common_v is not None and common_v['dataset_id'] in authorized_ds) else None
        allowed = [p['dataset_id'] for p in projs]
        ds_to_proj = {p['dataset_id']: p['id'] for p in projs}
        allowed_run = set(allowed) if include_runs else set()
        if include_runs:
            for proj in projs:
                code_ds = proj.get('code_dataset_id')
                if proj.get('code_enabled') and code_ds and is_valid_uuid(str(code_ds)):
                    code_canon = uuid_text(code_ds)
                    if code_canon in authorized_ds:
                        allowed_run.add(code_canon)
        if com is not None:
            allowed.append(com['dataset_id'])
            ds_to_proj[com['dataset_id']] = ''
            if include_runs:
                allowed_run.add(com['dataset_id'])
        scope['projects'] = projs
        scope['common'] = com
        scope['allowed_ds'] = allowed
        scope['ds_to_proj'] = ds_to_proj
        scope['allowed_ds_set'] = set(allowed)
        if include_runs:
            scope['allowed_run_ds'] = allowed_run
            scope['agents_snapshot'] = {k: dict(v) for k, v in self.registry.state.get('agents', {}).items()}
        return scope

    def multi_scope_revalidator(self, scope: dict, *, include_runs: bool = False):
        async def _revalidate():
            authorized_ds = await fetch_authorized_official_datasets(getattr(self.registry, 'official', None))
            projects_v, common_v = _verified_groups(self.registry)
            self.populate_authorized_scope(scope, authorized_ds, projects_v, common_v, include_runs=include_runs)

        return _revalidate

    async def resolve_search_scope(self, project_id_filter: str, dataset_id_filter: str):
        """Resolve the initial search scope and its matching retry authorization."""
        if project_id_filter and dataset_id_filter:
            raise PolicyError('invalid_search_query', 400)
        scope: dict = {}
        if project_id_filter:
            proj = _require_verified_project(self.registry, project_id_filter)
            await verify_official_read_access(getattr(self.registry, 'official', None), proj['dataset_id'])
            await self.ensure_synced()
            scope['allowed_ds'] = [proj['dataset_id']]
            scope['ds_to_proj'] = {proj['dataset_id']: proj['id']}
            revalidate_fn = self.project_revalidator(project_id_filter, proj['dataset_id'])
        elif dataset_id_filter:
            target_ds = _require_verified_managed_dataset(self.registry, dataset_id_filter, allow_code=False)
            await verify_official_read_access(getattr(self.registry, 'official', None), target_ds)
            projects, common = await self.ensure_synced()
            ds_to_proj = {p['dataset_id']: p['id'] for p in projects}
            if common is not None:
                ds_to_proj[common['dataset_id']] = ''
            scope['allowed_ds'] = [target_ds]
            scope['ds_to_proj'] = ds_to_proj
            revalidate_fn = self.managed_dataset_revalidator(dataset_id_filter, target_ds, scope['ds_to_proj'])
        else:
            authorized_ds = await fetch_authorized_official_datasets(getattr(self.registry, 'official', None))
            projects, common = await self.ensure_synced()
            self.populate_authorized_scope(scope, authorized_ds, projects, common)
            revalidate_fn = self.multi_scope_revalidator(scope)
        return scope, revalidate_fn

    def page_unread_flags_by_dataset(self, 
        identity: str, rows: list[dict], ds_to_proj: dict[str, str], *, knowledge_only: bool = False,
    ) -> dict[tuple[str, str], bool]:
        unread_flags = {}
        if identity and rows:
            by_ds: dict[str, list[dict]] = {}
            for doc in rows:
                if not knowledge_only or doc['is_knowledge']:
                    by_ds.setdefault(doc['dataset_id'], []).append(doc)
            for ds, docs_in_ds in by_ds.items():
                pid = ds_to_proj.get(ds, '')
                if pid:
                    for did, flag in self.read_store.page_unread_flags(identity, pid, ds, docs_in_ds, catalog_index=self.cat).items():
                        unread_flags[(ds, did)] = flag
        return unread_flags

    async def cleanup(self, _app):
        self.db_executor.shutdown(wait=True)
        self.hydrator.close()
        close_pooled_official_sqlite_ro()
        if self.owns_catalog:
            self.cat.close()
        if self.owns_reading_state:
            self.read_store.close()
