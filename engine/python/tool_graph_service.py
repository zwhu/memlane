"""Thin, authenticated SDK adapter for the official tool-trace graph pipeline.

The pinned HTTP improve API cannot scope this optional stage per project.
No extraction, chunking, vector storage, graph algorithm or watermark is
implemented here. Only bounded job identities and outcomes are retained.
"""
import asyncio
from collections import OrderedDict
from contextlib import asynccontextmanager
from datetime import datetime, timezone
import hashlib
import hmac
import json
from pathlib import Path
import sqlite3
import uuid

from performance_trace import RequestTrace
from _runtime_common import private_write, read_secret
from project_policy import PolicyError
from sqlite_ro import id_variants, official_sqlite_ro, uuid_text


MAX_TOOL_GRAPH_SESSIONS = 10000
MAX_RETAINED_TERMINAL_JOBS = 200
_MAX_WATERMARK_ENTRIES = 65536
_TERMINAL_JOB_STATUSES = frozenset({'completed', 'cancelled'})
_CACHE_EXPIRY_NORM_EXPR = "RTRIM(REPLACE(substr(expires_at, 1, 26), 'T', ' '), 'Zz')"


def now():
    return datetime.now(timezone.utc).isoformat()


def _cache_now_space() -> str:
    return datetime.now(timezone.utc).strftime('%Y-%m-%d %H:%M:%S.%f')


def _validate_trace_signature(raw) -> dict[str, int] | None:
    if not isinstance(raw, dict):
        return None
    cnt = raw.get('count')
    mn = raw.get('min_seq')
    mx = raw.get('max_seq')
    if (
        type(cnt) is not int
        or type(mn) is not int
        or type(mx) is not int
        or cnt < 0
        or mn < 0
        or mx < mn
    ):
        return None
    return {'count': cnt, 'min_seq': mn, 'max_seq': mx}


class ToolGraphWatermarkStore:
    """Private 0600 sidecar tracking persisted trace seq signatures (zero upstream Cognee edits).

    Detects equal-count replacement (old traces expire while an equal number of new traces
    arrive) where upstream count-only ``TRACE_PERSIST_WATERMARK`` stays equal to ``total``.
    """

    def __init__(self, path):
        self.path = Path(path)
        self._rows: OrderedDict[str, dict[str, int]] = OrderedDict()
        self._dirty = False
        if self.path.exists():
            if self.path.is_symlink() or self.path.stat().st_mode & 0o077:
                raise ValueError('private_tool_graph_watermarks_required')
            payload = json.loads(self.path.read_text())
            if payload.get('schema_version') != 1 or not isinstance(payload.get('entries'), dict):
                raise ValueError('unsupported_tool_graph_watermarks')
            for key, value in payload['entries'].items():
                if not isinstance(key, str) or not isinstance(value, dict):
                    continue
                sig = _validate_trace_signature(value)
                persisted_max = value.get('persisted_max_seq')
                if sig is not None and type(persisted_max) is int and persisted_max >= 0:
                    self._rows[key] = {**sig, 'persisted_max_seq': persisted_max}
            while len(self._rows) > _MAX_WATERMARK_ENTRIES:
                self._rows.popitem(last=False)

    @staticmethod
    def _key(user_id: str, session_id: str) -> str:
        return json.dumps([str(user_id), str(session_id)], separators=(',', ':'))

    def get(self, user_id: str, session_id: str) -> dict[str, int] | None:
        key = self._key(user_id, session_id)
        entry = self._rows.get(key)
        if entry is not None:
            self._rows.move_to_end(key)
            return dict(entry)
        return None

    def record(self, user_id: str, session_id: str, *, count: int, min_seq: int, max_seq: int, persisted_max_seq: int) -> None:
        key = self._key(user_id, session_id)
        value = {
            'count': int(count),
            'min_seq': int(min_seq),
            'max_seq': int(max_seq),
            'persisted_max_seq': int(persisted_max_seq),
        }
        if self._rows.get(key) == value:
            self._rows.move_to_end(key)
            return
        self._rows[key] = value
        self._rows.move_to_end(key)
        while len(self._rows) > _MAX_WATERMARK_ENTRIES:
            self._rows.popitem(last=False)
        self._dirty = True

    def save(self) -> None:
        if not self._dirty:
            return
        private_write(
            self.path,
            json.dumps({'schema_version': 1, 'entries': dict(self._rows)}, sort_keys=True).encode(),
        )
        self._dirty = False


def _read_session_trace_window_ro(
    cache_db,
    user_id: str,
    session_id: str,
    *,
    upto_count: int | None = None,
    persisted_max_seq: int | None = None,
) -> dict[str, int | None] | None:
    """Inspect active trace (count, min_seq, max_seq, limit_th_seq, already_persisted) in one read snapshot."""
    if not cache_db:
        return None
    target = Path(cache_db)
    if not target.is_file() or target.is_symlink():
        return None
    primary_user = uuid_text(user_id) or str(user_id)
    user_candidates = [primary_user] + [v for v in id_variants(user_id) if v != primary_user]
    now_space = _cache_now_space()
    try:
        with official_sqlite_ro(target) as con:
            con.execute('BEGIN DEFERRED')
            try:
                matched_uid = None
                count = 0
                min_seq = None
                max_seq = None
                for uid in user_candidates:
                    row = con.execute(
                        f'''SELECT COUNT(*), MIN(seq), MAX(seq)
                            FROM cache_trace_entries
                            WHERE user_id = ? AND session_id = ?
                              AND (expires_at IS NULL OR {_CACHE_EXPIRY_NORM_EXPR} > ?)''',
                        (uid, session_id, now_space),
                    ).fetchone()
                    cnt = int(row[0] or 0) if row else 0
                    if cnt > 0:
                        matched_uid = uid
                        count = cnt
                        min_seq = int(row[1])
                        max_seq = int(row[2])
                        break
                if matched_uid is None:
                    return {
                        'count': 0,
                        'min_seq': None,
                        'max_seq': None,
                        'already_persisted': 0,
                        'limit_th_seq': None,
                    }
                already_persisted = None
                if persisted_max_seq is not None:
                    row_p = con.execute(
                        f'''SELECT COUNT(*)
                            FROM cache_trace_entries
                            WHERE user_id = ? AND session_id = ? AND seq <= ?
                              AND (expires_at IS NULL OR {_CACHE_EXPIRY_NORM_EXPR} > ?)''',
                        (matched_uid, session_id, int(persisted_max_seq), now_space),
                    ).fetchone()
                    already_persisted = int(row_p[0] or 0) if row_p else 0
                limit_th_seq = None
                if upto_count is not None and upto_count > 0 and count >= upto_count:
                    row_l = con.execute(
                        f'''SELECT seq
                            FROM cache_trace_entries
                            WHERE user_id = ? AND session_id = ?
                              AND (expires_at IS NULL OR {_CACHE_EXPIRY_NORM_EXPR} > ?)
                            ORDER BY seq ASC
                            LIMIT 1 OFFSET ?''',
                        (matched_uid, session_id, now_space, int(upto_count) - 1),
                    ).fetchone()
                    if row_l is not None:
                        limit_th_seq = int(row_l[0])
                return {
                    'count': count,
                    'min_seq': min_seq,
                    'max_seq': max_seq,
                    'already_persisted': already_persisted,
                    'limit_th_seq': limit_th_seq,
                }
            finally:
                if con.in_transaction:
                    con.execute('ROLLBACK')
    except (PolicyError, sqlite3.Error):
        return None


class ToolGraphJobs:
    def __init__(self, path, build, authorize):
        self.path, self.build, self.authorize = Path(path), build, authorize
        self.tasks = {}
        self.rows = {}
        if self.path.exists():
            if self.path.is_symlink() or self.path.stat().st_mode & 0o077:
                raise ValueError('private_tool_graph_jobs_required')
            payload = json.loads(self.path.read_text())
            if payload.get('schema_version') != 1:
                raise ValueError('unsupported_tool_graph_jobs')
            self.rows = payload['jobs']
            for row in self.rows.values():
                if row['status'] in ('queued', 'running'):
                    row.update(status='result_unknown', error_code='tool_graph_owner_restarted')
            self.save()

    def _prune_terminal_jobs(self):
        terminal = [
            (str(row.get('ended_at') or row.get('created_at') or ''), op_id)
            for op_id, row in self.rows.items()
            if row.get('status') in _TERMINAL_JOB_STATUSES
        ]
        if len(terminal) <= MAX_RETAINED_TERMINAL_JOBS:
            return
        terminal.sort()
        excess = len(terminal) - MAX_RETAINED_TERMINAL_JOBS
        for _, op_id in terminal[:excess]:
            self.rows.pop(op_id, None)

    def save(self):
        self._prune_terminal_jobs()
        private_write(self.path, json.dumps({'schema_version': 1, 'jobs': self.rows}, sort_keys=True).encode())

    async def start(self, user, dataset, sessions, operation, limits=None, signatures=None):
        await self.authorize(user, dataset)
        uid, dataset, operation = str(user.id), str(uuid.UUID(str(dataset))), str(uuid.UUID(str(operation)))
        sessions = sorted(set(sessions))
        if not sessions or len(sessions) > MAX_TOOL_GRAPH_SESSIONS or any(not isinstance(s, str) or not s or len(s) > 256 for s in sessions):
            raise PolicyError('invalid_tool_graph_sessions')
        if limits is not None and (set(limits) != set(sessions) or any(type(n) is not int or n < 0 for n in limits.values())):
            raise PolicyError('invalid_tool_graph_snapshot')
        clean_sigs = None
        if signatures is not None:
            if not isinstance(signatures, dict) or not set(signatures) <= set(sessions):
                raise PolicyError('invalid_tool_graph_snapshot')
            clean_sigs = {}
            for s, raw_sig in signatures.items():
                parsed = _validate_trace_signature(raw_sig)
                if parsed is None:
                    raise PolicyError('invalid_tool_graph_snapshot')
                clean_sigs[s] = parsed
        sig_payload = [uid, dataset, sessions, limits] if clean_sigs is None else [uid, dataset, sessions, limits, clean_sigs]
        signature = hashlib.sha256(json.dumps(sig_payload, sort_keys=True).encode()).hexdigest()
        if operation in self.rows:
            row = self.get(user, operation)
            if row['signature'] != signature:
                raise PolicyError('tool_graph_operation_conflict', 409)
            return row
        if any(row['dataset_id'] == dataset and row['status'] in ('queued', 'running', 'result_unknown') for row in self.rows.values()):
            raise PolicyError('tool_graph_existing_operation_requires_review', 409)
        row = {'operation_id': operation, 'user_id': uid, 'dataset_id': dataset, 'session_ids': sessions,
               'signature': signature, 'limits': limits, 'status': 'queued', 'created_at': now()}
        if clean_sigs is not None:
            row['signatures'] = clean_sigs
        self.rows[operation] = row
        self.save()  # Unknown writes are never repeated under a new operation.
        self.tasks[operation] = asyncio.create_task(self.run(user, row))
        self.tasks[operation].add_done_callback(lambda _task: self.tasks.pop(operation, None))
        return dict(row)

    def get(self, user, operation):
        row = self.rows.get(str(operation))
        if row is None or row['user_id'] != str(user.id):
            raise PolicyError('tool_graph_operation_not_found', 404)
        return dict(row)

    async def run(self, user, row):
        trace = RequestTrace('tool_graph')
        trace.transition('official_pipeline')
        row.update(status='running', started_at=now())
        self.save()
        try:
            if row.get('signatures') is not None:
                result = await self.build(
                    user,
                    row['dataset_id'],
                    row['session_ids'],
                    row.get('limits'),
                    signatures=row.get('signatures'),
                )
            else:
                result = await self.build(user, row['dataset_id'], row['session_ids'], row.get('limits'))
            row.update(status='completed', result=result)
        except asyncio.CancelledError:
            row.update(status='cancelled', error_code='tool_graph_cancelled')
        except Exception as error:
            trace.emit("pipeline_failed", error_class=type(error).__name__)
            # A failed build can have written partial data. Its original
            # operation stays reviewable; this adapter does not retry it.
            row.update(status='result_unknown', error_code='tool_graph_build_unconfirmed')
        finally:
            row['ended_at'] = now()
            self.save()
            trace.finish(row['status'])

    async def cancel(self, user, operation):
        self.get(user, operation)
        task = self.tasks.get(str(operation))
        if task is not None and not task.done():
            task.cancel()
            try:
                await task
            except asyncio.CancelledError:
                self.rows[str(operation)].update(status='cancelled', error_code='tool_graph_cancelled', ended_at=now())
                self.save()
        return self.get(user, operation)

    async def close(self):
        tasks = [task for task in self.tasks.values() if not task.done()]
        for task in tasks:
            task.cancel()
        if tasks:
            await asyncio.gather(*tasks, return_exceptions=True)
        for row in self.rows.values():
            if row['status'] in ('queued', 'running'):
                row.update(status='result_unknown', error_code='tool_graph_owner_stopped', ended_at=now())
        self.save()


async def official_authorize(user, dataset):
    from cognee.modules.data.methods import get_authorized_existing_datasets
    result = await get_authorized_existing_datasets(user=user, datasets=[dataset], permission_type='write')
    if not result:
        raise PolicyError('tool_graph_write_required', 403)


async def official_preview(user, sessions, *, cache_db=None, watermark_store=None):
    from cognee.infrastructure.session.get_session_manager import get_session_manager
    from cognee.infrastructure.session.session_persist_watermark import TRACE_PERSIST_WATERMARK
    manager = get_session_manager()
    if not manager.is_available:
        raise PolicyError('tool_graph_source_unavailable', 503)
    uid = str(user.id)
    limits, pending, signatures = {}, {}, {}
    for session in sessions:
        total = await manager.get_agent_trace_count(user_id=uid, session_id=session)
        stored = await TRACE_PERSIST_WATERMARK.read_count(manager, uid, session)
        effective_stored = 0 if stored > total else stored
        saved = watermark_store.get(uid, session) if watermark_store is not None else None
        win = (
            await asyncio.to_thread(
                _read_session_trace_window_ro,
                cache_db,
                uid,
                session,
                persisted_max_seq=saved['persisted_max_seq'] if saved is not None else None,
            )
            if cache_db is not None
            else None
        )
        if win is not None and win['count'] == total and win['count'] > 0 and win['max_seq'] is not None and win['min_seq'] is not None:
            signatures[session] = {
                'count': win['count'],
                'min_seq': win['min_seq'],
                'max_seq': win['max_seq'],
            }
            if saved is not None:
                if win['max_seq'] != saved['persisted_max_seq'] or win['min_seq'] != saved['min_seq']:
                    already = int(win['already_persisted'] or 0)
                    effective_stored = min(effective_stored, already)
            elif effective_stored == total and watermark_store is not None:
                # Seed baseline signature for already-persisted sessions so future equal-count
                # replacements (delete 1 + insert 1 with identical count) are detected.
                watermark_store.record(
                    uid,
                    session,
                    count=win['count'],
                    min_seq=win['min_seq'],
                    max_seq=win['max_seq'],
                    persisted_max_seq=win['max_seq'],
                )
        limits[session], pending[session] = total, max(0, total - effective_stored)
    if watermark_store is not None:
        watermark_store.save()
    result = {'limits': limits, 'pending': pending}
    if signatures:
        result['signatures'] = signatures
    return result


async def official_build(user, dataset, sessions, limits=None, *, signatures=None, cache_db=None, watermark_store=None):
    from cognee.infrastructure.session.get_session_manager import get_session_manager
    from cognee.infrastructure.session.session_persist_watermark import TRACE_PERSIST_WATERMARK
    from cognee.memify_pipelines.persist_agent_trace_feedbacks_in_knowledge_graph import persist_agent_trace_feedbacks_in_knowledge_graph_pipeline
    from cognee.modules.pipelines.models import PipelineRunErrored
    manager = get_session_manager()
    uid = str(user.id)
    upper, pending = {}, 0
    watermark_resets: dict[str, int] = {}
    pending_sigs: dict[str, tuple[int, int, int]] = {}
    for session in sessions:
        total = await manager.get_agent_trace_count(user_id=uid, session_id=session)
        limit = limits[session] if limits is not None else total
        if limit > total:
            raise PolicyError('tool_graph_source_changed', 409)
        stored = await TRACE_PERSIST_WATERMARK.read_count(manager, uid, session)
        effective_stored = 0 if stored > total else stored
        saved = watermark_store.get(uid, session) if watermark_store is not None else None
        expected_sig = signatures.get(session) if isinstance(signatures, dict) else None
        win = (
            await asyncio.to_thread(
                _read_session_trace_window_ro,
                cache_db,
                uid,
                session,
                upto_count=limit if limit > 0 else None,
                persisted_max_seq=saved['persisted_max_seq'] if saved is not None else None,
            )
            if cache_db is not None
            else None
        )
        target_sig = None
        if win is not None:
            if expected_sig is not None and limit > 0:
                if (
                    win['count'] < limit
                    or win['min_seq'] != expected_sig['min_seq']
                    or win['limit_th_seq'] != expected_sig['max_seq']
                ):
                    raise PolicyError('tool_graph_source_changed', 409)
            if saved is not None and limit > 0 and win['limit_th_seq'] is not None:
                if win['limit_th_seq'] != saved['persisted_max_seq'] or win['min_seq'] != saved['min_seq']:
                    already = int(win['already_persisted'] or 0)
                    effective_stored = min(effective_stored, already)
            if limit > 0 and win['min_seq'] is not None and win['limit_th_seq'] is not None:
                target_sig = (limit, win['min_seq'], win['limit_th_seq'])
        if limit > effective_stored:
            # When equal-count replacement kept stored <= total, upstream resolve_effective
            # would not reset TRACE_PERSIST_WATERMARK on its own. Reconcile via Cognee's
            # public watermark writer before running the official pipeline (zero upstream patch).
            if stored <= total and effective_stored < stored:
                watermark_resets[session] = effective_stored
            upper[session] = limit
            pending += limit - effective_stored
            if target_sig is not None:
                pending_sigs[session] = target_sig
    if not upper:
        return {'new_steps': 0, 'pipeline_run_ids': []}
    for session, new_wm in watermark_resets.items():
        await TRACE_PERSIST_WATERMARK.write_count(manager, uid, session, new_wm)
    result = await persist_agent_trace_feedbacks_in_knowledge_graph_pipeline(
        user=user, dataset=uuid.UUID(str(dataset)), session_ids=list(upper), persist_up_to=upper, run_in_background=False)
    runs = list(result.values()) if isinstance(result, dict) else [result]
    if any(isinstance(run, PipelineRunErrored) for run in runs):
        raise PolicyError('official_tool_graph_failed', 503)
    if watermark_store is not None and pending_sigs:
        for session, (cnt, mn, mx) in pending_sigs.items():
            watermark_store.record(uid, session, count=cnt, min_seq=mn, max_seq=mx, persisted_max_seq=mx)
        watermark_store.save()
    return {'new_steps': pending, 'pipeline_run_ids': [str(run.pipeline_run_id) for run in runs if hasattr(run, 'pipeline_run_id')]}


def install_tool_graph_routes(app, config):
    from fastapi import APIRouter, Depends, HTTPException, Request
    from pydantic import BaseModel, ConfigDict, Field
    from cognee.modules.users.methods import get_authenticated_user
    state_dir = Path(config['project_control']['registry_file']).parent
    official_data = config.get('official_data')
    cache_db = (Path(official_data) / 'system' / 'databases' / 'cache.db') if official_data else None
    watermark_store = ToolGraphWatermarkStore(state_dir / 'tool-graph-trace-watermarks.json')

    async def build_with_watermarks(user, dataset, sessions, limits=None, *, signatures=None):
        return await official_build(
            user,
            dataset,
            sessions,
            limits,
            signatures=signatures,
            cache_db=cache_db,
            watermark_store=watermark_store,
        )

    jobs = ToolGraphJobs(state_dir / 'tool-graph-service-jobs.json',
                         build_with_watermarks, official_authorize)
    secret = read_secret(config['project_control']['management_token_file'])

    @asynccontextmanager
    async def lifespan(_app):
        try:
            yield
        finally:
            await jobs.close()

    router = APIRouter(prefix='/api/v1/memlane/tool-graphs', lifespan=lifespan)

    class Preview(BaseModel):
        model_config = ConfigDict(extra='forbid')
        dataset_id: uuid.UUID
        session_ids: list[str] = Field(min_length=1, max_length=MAX_TOOL_GRAPH_SESSIONS)

    class Build(Preview):
        operation_id: uuid.UUID
        limits: dict[str, int] | None = None
        signatures: dict[str, dict[str, int]] | None = None

    async def manager(request: Request):
        if not hmac.compare_digest(request.headers.get('x-memlane-management', ''), secret):
            raise HTTPException(403, 'management_required')

    async def respond(call):
        try:
            return await call
        except PolicyError as error:
            raise HTTPException(error.status, error.code) from None

    @router.post('', dependencies=[Depends(manager)])
    async def start(payload: Build, user=Depends(get_authenticated_user)):
        return await respond(
            jobs.start(
                user,
                payload.dataset_id,
                payload.session_ids,
                payload.operation_id,
                payload.limits,
                payload.signatures,
            )
        )

    @router.post('/preview', dependencies=[Depends(manager)])
    async def preview(payload: Preview, user=Depends(get_authenticated_user)):
        async def inspect():
            await jobs.authorize(user, payload.dataset_id)
            return await official_preview(
                user,
                payload.session_ids,
                cache_db=cache_db,
                watermark_store=watermark_store,
            )
        return await respond(inspect())

    @router.get('/capacity', dependencies=[Depends(manager)])
    async def capacity(user=Depends(get_authenticated_user)):
        # Public registry covers queued/background improve between pipeline
        # events too. Only service-wide counts leave this management boundary.
        from cognee.infrastructure.background_tasks import pending_background_tasks
        return {'pending_background_tasks': pending_background_tasks(),
                'tool_graph_jobs_running': sum(not task.done() for task in jobs.tasks.values())}

    @router.get('/{operation}', dependencies=[Depends(manager)])
    async def get(operation: uuid.UUID, user=Depends(get_authenticated_user)):
        try:
            return jobs.get(user, operation)
        except PolicyError as error:
            raise HTTPException(error.status, error.code) from None

    @router.delete('/{operation}', dependencies=[Depends(manager)])
    async def cancel(operation: uuid.UUID, user=Depends(get_authenticated_user)):
        return await respond(jobs.cancel(user, operation))

    app.include_router(router)
    return jobs
