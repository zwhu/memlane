"""Idempotent capture receipt protocol backed by ExternalCaptureStore + read-only Cognee lookup.

No Cognee source patching is required. Reads legacy ``cache_kv`` receipts when
present and reconciles pending interrupted requests read-only against
``SessionManager``.
"""
import asyncio
import hashlib
import json
import uuid
from dataclasses import dataclass


class CaptureDuplicate(Exception):
    pass


class CaptureConflict(Exception):
    pass


@dataclass(frozen=True)
class Capture:
    user: str
    dataset: str
    session: str
    message: str
    digest: str
    kind: str

    @property
    def key(self):
        scope = json.dumps([self.user, self.dataset, self.session, self.message])
        return 'memlane.capture.v1:' + hashlib.sha256(scope.encode()).hexdigest()

    @property
    def entry_id(self):
        return str(uuid.uuid5(uuid.NAMESPACE_URL, self.key))

    def receipt(self, *, entry_id=None):
        return dict(
            protocol=1,
            message_id=self.message,
            payload_sha256=self.digest,
            entry_id=entry_id or self.entry_id,
            entry_type=self.kind,
            session_id=self.session,
            dataset_id=self.dataset,
            status='saved',
        )


async def lookup_legacy_cache_kv(adapter, capture):
    """Read-only lookup for legacy capture receipts written to stock ``cache_kv``."""
    from cognee.infrastructure.databases.cache.sql.SqlCacheAdapter import SqlCacheAdapter
    from cognee.infrastructure.databases.cache.sql.tables import cache_kv
    from sqlalchemy import select

    if not isinstance(adapter, SqlCacheAdapter) or adapter._is_postgres:
        return None
    await adapter._ensure_initialized()
    async with adapter.sessionmaker() as session:
        value = (await session.execute(select(cache_kv.c.value).where(cache_kv.c.key == capture.key))).scalar_one_or_none()
    return json.loads(value) if value else None


def _legacy_matches(old, capture):
    return (
        isinstance(old, dict)
        and old.get('protocol') == 1
        and old.get('status') == 'saved'
        and old.get('message_id') == capture.message
        and old.get('payload_sha256') == capture.digest
        and old.get('entry_type') == capture.kind
        and old.get('session_id') == capture.session
        and old.get('dataset_id') == capture.dataset
    )


def install_capture_routes(app, *, bindings=None, receipt_store=None, drift_observer=None):
    from fastapi import Depends, HTTPException, Query
    from pydantic import BaseModel, Field, TypeAdapter
    from cognee.memory import QAEntry, TraceEntry
    from cognee.modules.users.methods import get_authenticated_user
    from cognee.modules.users.permissions.methods import get_permitted_dataset_ids
    from cognee.infrastructure.session.get_session_manager import get_session_manager
    from cognee.modules.session_lifecycle.metrics import get_session_row
    from cognee.api.v1.remember.remember import _resolve_entry_dataset
    from cognee.api.v1.remember import remember
    from external_capture_store import attach_capture_marker, ensure_qa_context_date, find_committed_capture

    if bindings is not None:
        get_authenticated_user = bindings.get_authenticated_user
        get_permitted_dataset_ids = bindings.get_permitted_dataset_ids
        get_session_manager = bindings.get_session_manager
        get_session_row = bindings.get_session_row
        _resolve_entry_dataset = bindings.resolve_entry_dataset
        remember = bindings.remember

    key_locks = {}

    class Body(BaseModel):
        dataset_id: uuid.UUID
        session_id: str = Field(min_length=1, max_length=512)
        entry: dict

    def message_id(value):
        try:
            if str(uuid.UUID(value)) != value:
                raise ValueError()
        except ValueError:
            raise HTTPException(422, 'invalid message ID') from None
        return value

    def require_ready():
        if receipt_store is None:
            raise HTTPException(503, 'local capture store unavailable')
        sm = get_session_manager()
        if not getattr(sm, 'is_available', False):
            raise HTTPException(503, 'local capture cache unavailable')
        return sm

    async def legacy_lookup(sm, capture):
        adapter = getattr(sm, '_cache', None)
        if adapter is None:
            return None
        try:
            return await lookup_legacy_cache_kv(adapter, capture)
        except Exception:
            return None

    @app.get('/api/v1/capture/entries/{message}')
    async def status(message: str, dataset_id: uuid.UUID, session_id: str = Query(min_length=1, max_length=512),
                     user=Depends(get_authenticated_user)):
        message_id(message)
        permitted = await get_permitted_dataset_ids(user.id)
        if dataset_id not in permitted and str(dataset_id) not in {str(v) for v in permitted}:
            raise HTTPException(404, 'capture not found')
        row = await get_session_row(session_id=session_id, user_id=user.id)
        if row is None or str(row.dataset_id) != str(dataset_id):
            raise HTTPException(404, 'capture not found')
        sm = require_ready()
        capture = Capture(str(user.id), str(dataset_id), session_id, message, '', '')
        found = receipt_store.get(capture.key)
        if found and found['status'] == 'saved' and found.get('receipt'):
            return found['receipt']
        legacy = await legacy_lookup(sm, capture)
        if legacy is not None:
            return legacy
        # Absence does not authorize replay of an old, unnumbered request.
        raise HTTPException(404, 'capture not found')

    @app.put('/api/v1/capture/entries/{message}')
    async def store(message: str, payload: Body, user=Depends(get_authenticated_user)):
        message_id(message)
        try:
            entry = TypeAdapter(QAEntry | TraceEntry).validate_python(payload.entry)
        except ValueError:
            raise HTTPException(422, 'invalid capture entry') from None
        if entry.type == 'trace' and entry.generate_feedback_with_llm:
            raise HTTPException(422, 'capture must not invoke a model')
        encoded = json.dumps(payload.entry, sort_keys=True, ensure_ascii=False, allow_nan=False).encode()
        if len(encoded) > 2 * 1024 * 1024:
            raise HTTPException(413, 'capture entry too large')
        # Original official write ACL and session/dataset binding checks still run
        # on duplicate requests, so a receipt never grants a revoked writer access.
        await _resolve_entry_dataset(session_id=payload.session_id, dataset_name='main_dataset',
            dataset_id=payload.dataset_id, user=user)
        capture = Capture(str(user.id), str(payload.dataset_id), payload.session_id,
                          message, hashlib.sha256(encoded).hexdigest(), entry.type)
        sm = require_ready()
        lock = key_locks.setdefault(capture.key, asyncio.Lock())
        try:
            async with lock:
                legacy = await legacy_lookup(sm, capture)
                if legacy is not None:
                    if not _legacy_matches(legacy, capture):
                        raise HTTPException(409, 'message ID already used for another payload')
                    return legacy

                baseline_count = None
                if entry.type == 'qa' and receipt_store.get(capture.key) is None:
                    try:
                        existing_qas = await sm.get_session(
                            user_id=capture.user, session_id=capture.session, formatted=False,
                        )
                        if isinstance(existing_qas, list):
                            baseline_count = len(existing_qas)
                    except Exception:
                        baseline_count = None

                try:
                    state, record = receipt_store.reserve(capture, baseline_count=baseline_count)
                except CaptureConflict:
                    raise HTTPException(409, 'message ID already used for another payload') from None
                def _observe_drift():
                    if drift_observer is not None and entry.type == 'trace':
                        try:
                            drift_observer(str(payload.dataset_id), payload.session_id, payload.entry, message)
                        except Exception:
                            pass

                def _finish_saved(saved_entry_id):
                    receipt = receipt_store.mark_saved(capture, str(saved_entry_id))
                    _observe_drift()
                    return receipt

                if state == 'saved' and record.get('receipt'):
                    _observe_drift()
                    return record['receipt']
                marked_entry = ensure_qa_context_date(attach_capture_marker(entry, capture))
                alt_contexts = (entry.context,) if getattr(entry, 'type', None) == 'qa' else ()
                if state == 'pending':
                    committed_id = await find_committed_capture(
                        sm, capture, marked_entry, baseline_count=record.get('baseline_count'),
                        alt_contexts=alt_contexts,
                    )
                    if committed_id:
                        return _finish_saved(committed_id)

                try:
                    result = await remember(
                        marked_entry, dataset_id=payload.dataset_id, session_id=payload.session_id, user=user,
                    )
                except Exception:
                    committed_id = await find_committed_capture(
                        sm, capture, marked_entry, baseline_count=record.get('baseline_count'),
                        alt_contexts=alt_contexts,
                    )
                    if committed_id:
                        return _finish_saved(committed_id)
                    receipt_store.abort_pending(capture)
                    raise

                committed_id = result.get('entry_id') if isinstance(result, dict) else getattr(result, 'entry_id', None)
                status_val = result.get('status') if isinstance(result, dict) else getattr(result, 'status', None)
                if status_val == 'errored' or not committed_id:
                    committed_id = await find_committed_capture(
                        sm, capture, marked_entry, baseline_count=record.get('baseline_count'),
                        alt_contexts=alt_contexts,
                    )
                    if not committed_id:
                        receipt_store.abort_pending(capture)
                        raise HTTPException(503, 'capture commit not confirmed')
                return _finish_saved(committed_id)
        finally:
            if key_locks.get(capture.key) is lock and not lock.locked() and not getattr(lock, '_waiters', None):
                key_locks.pop(capture.key, None)
