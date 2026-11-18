"""Small capture receipts committed in Cognee's existing SQL cache transaction.

No message bodies, second queue, process mutex or separate database. An entry
and its receipt either commit together or both roll back. Unmanaged writes keep
their native behavior. This protocol deliberately cannot acknowledge legacy IDs.
"""
import contextvars
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

    def receipt(self):
        return dict(protocol=1, message_id=self.message, payload_sha256=self.digest,
                    entry_id=self.entry_id, entry_type=self.kind, session_id=self.session,
                    dataset_id=self.dataset, status='saved')


current_capture = contextvars.ContextVar('memlane_capture', default=None)


def entry_id():
    capture = current_capture.get()
    return capture.entry_id if capture else str(uuid.uuid4())


async def reserve(session, user, session_id, identifier):
    """Called inside the native QA/trace insert transaction, never after commit."""
    capture = current_capture.get()
    if capture is None:
        return
    if (str(user), session_id, identifier) != (capture.user, capture.session, capture.entry_id):
        raise CaptureConflict('capture scope changed')
    from cognee.infrastructure.databases.cache.sql.tables import cache_kv
    from sqlalchemy import select
    from sqlalchemy.dialects.sqlite import insert
    # Current product is local SQLite only. Fail closed for an unvalidated DB.
    if session.bind.dialect.name != 'sqlite':
        raise RuntimeError('capture receipts require the local SQL cache')
    value = json.dumps(capture.receipt(), sort_keys=True)
    result = await session.execute(insert(cache_kv).values(
        key=capture.key, value=value, expires_at=None).on_conflict_do_nothing(index_elements=['key']))
    if result.rowcount == 1:
        return
    existing = (await session.execute(select(cache_kv.c.value).where(cache_kv.c.key == capture.key))).scalar_one()
    if existing != value:
        raise CaptureConflict('message ID already used for another payload')
    raise CaptureDuplicate()


async def lookup(adapter, capture):
    from cognee.infrastructure.databases.cache.sql.tables import cache_kv
    from sqlalchemy import select
    await adapter._ensure_initialized()
    async with adapter.sessionmaker() as session:
        value = (await session.execute(select(cache_kv.c.value).where(cache_kv.c.key == capture.key))).scalar_one_or_none()
    return json.loads(value) if value else None


def install_capture_routes(app, *, bindings=None):
    from fastapi import Depends, HTTPException, Query
    from pydantic import BaseModel, Field, TypeAdapter
    from cognee.memory import QAEntry, TraceEntry
    from cognee.modules.users.methods import get_authenticated_user
    from cognee.modules.users.permissions.methods import get_permitted_dataset_ids
    from cognee.infrastructure.session.get_session_manager import get_session_manager
    from cognee.modules.session_lifecycle.metrics import get_session_row
    from cognee.api.v1.remember.remember import _resolve_entry_dataset
    from cognee.api.v1.remember import remember
    if bindings is not None:
        get_authenticated_user = bindings.get_authenticated_user
        get_permitted_dataset_ids = bindings.get_permitted_dataset_ids
        get_session_manager = bindings.get_session_manager
        get_session_row = bindings.get_session_row
        _resolve_entry_dataset = bindings.resolve_entry_dataset
        remember = bindings.remember

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

    def cache():
        from cognee.infrastructure.databases.cache.sql.SqlCacheAdapter import SqlCacheAdapter
        adapter = get_session_manager()._cache
        if (not isinstance(adapter, SqlCacheAdapter) or adapter._is_postgres
                or getattr(adapter, '__memlane_capture_protocol__', None) != 1):
            raise HTTPException(503, 'local capture cache unavailable')
        return adapter

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
        capture = Capture(str(user.id), str(dataset_id), session_id, message, '', '')
        result = await lookup(cache(), capture)
        if result is None:
            # Absence does not authorize replay of an old, unnumbered request.
            raise HTTPException(404, 'capture not found')
        return result

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
        adapter = cache()
        old = await lookup(adapter, capture)
        if old:
            if old != capture.receipt():
                raise HTTPException(409, 'message ID already used for another payload')
            return old
        token = current_capture.set(capture)
        try:
            try:
                await remember(entry, dataset_id=payload.dataset_id, session_id=payload.session_id, user=user)
            except CaptureConflict:
                raise HTTPException(409, 'message ID already used for another payload') from None
            except Exception:
                # A native post-save index/activity failure cannot undo committed
                # raw content. Only the atomic receipt proves that it committed.
                result = await lookup(adapter, capture)
                if result == capture.receipt():
                    return result
                raise
            result = await lookup(adapter, capture)
            if result != capture.receipt():
                raise HTTPException(503, 'capture commit not confirmed')
            return result
        finally:
            current_capture.reset(token)
