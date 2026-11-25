"""Loopback-only internal v1 transport and pinned Cognee SDK adapter."""
import asyncio
from collections import OrderedDict
from datetime import date
import hashlib
import hmac
import json
import re
from typing import Any, Literal, Protocol
from uuid import UUID

from fastapi import APIRouter, Depends, Header, HTTPException, Query
from fastapi.responses import JSONResponse
from pydantic import BaseModel, Field, ValidationError

from . import ENGINE_NAME, ENGINE_VERSION, PolicyError, read_secret
from .language_egress import scoped_output_language
from .tool_graph import run_tool_graph_pipeline


class Scope(BaseModel):
    dataset_id: str
    user_id: str | None = None

class SourceMeta(BaseModel):
    session_id: str | None = None
    context_date: date | None = None
    external_metadata: dict | None = None
    node_set: list[str] | None = None

class Ingest(BaseModel):
    scope: Scope
    kind: Literal['capture_qa', 'capture_trace', 'document']
    idempotency_key: str | None = Field(default=None, max_length=256)
    content: str | dict
    source_meta: SourceMeta = Field(default_factory=SourceMeta)

class Extract(BaseModel):
    scope: Scope
    doc_ids: list[str]
    expected_model_digest: str | None = None
    output_language: str | None = None
    mode: Literal['default', 'tool_graph'] = 'default'

class SearchFilters(BaseModel):
    kind: str | None = None
    limit: int | None = Field(default=None, ge=1, le=100)

class Search(BaseModel):
    scopes: list[Scope]
    query: str
    filters: SearchFilters = Field(default_factory=SearchFilters)
    limit: int | None = Field(default=None, ge=1, le=100)

class ModelApply(BaseModel):
    generation_config: dict
    output_language: str | None = None
    operation_id: str


class EngineBackend(Protocol):
    async def ingest(self, payload: Ingest, content: str | dict) -> dict: ...
    async def extract(self, payload: Extract) -> dict: ...
    async def search(self, payload: Search, limit: int) -> list[dict]: ...
    async def list_items(self, scope: Scope, limit: int, offset: int) -> list[dict]: ...
    async def get_item(self, scope: Scope, item_id: str) -> dict: ...
    async def delete_item(self, scope: Scope, item_id: str) -> dict: ...
    async def graph(self, dataset_id: str, filter_text: str | None) -> dict: ...


def _uuid(value):
    try:
        return UUID(value)
    except ValueError:
        raise PolicyError('invalid_identifier', 422) from None


class CogneeBackend:
    def __init__(self):
        self._provision_lock = asyncio.Lock()

    async def _dataset(self, scope, permission='read', *, allow_provision=False):
        from cognee.infrastructure.databases.exceptions import DatabaseNotCreatedError, EntityNotFoundError
        from cognee.modules.data.methods import get_authorized_existing_datasets
        from cognee.modules.users.exceptions import PermissionDeniedError
        from cognee.modules.users.methods.get_user import get_user
        from cognee.modules.users.methods.get_default_user import get_default_user

        dataset = _uuid(scope.dataset_id)
        user_id = (scope.user_id or '').strip()
        try:
            user = await get_user(_uuid(user_id)) if user_id else await get_default_user()
        except DatabaseNotCreatedError:
            if user_id or not allow_provision:
                raise PolicyError('dataset_access_required', 403) from None
            from cognee.modules.engine.operations.setup import setup
            async with self._provision_lock:
                await setup()
                user = await get_default_user()
        except EntityNotFoundError:
            raise PolicyError('user_not_found', 404) from None

        async def authorized():
            try:
                return await get_authorized_existing_datasets(
                    user=user, datasets=[dataset], permission_type=permission)
            except PermissionDeniedError:
                return []

        if await authorized():
            return user, dataset
        if allow_provision and (not user_id or user.id == (await get_default_user()).id):
            # Serialize new owner writes until the dataset and its ACLs are visible.
            async with self._provision_lock:
                await self._provision_owner_dataset(user, dataset)
                if await authorized():
                    return user, dataset
        raise PolicyError('dataset_access_required', 403)

    @staticmethod
    async def _provision_owner_dataset(user, dataset):
        from cognee.infrastructure.databases.relational import get_relational_engine
        from cognee.modules.data.models import Dataset
        from cognee.modules.users.permissions.methods import give_permission_on_dataset
        from sqlalchemy.exc import IntegrityError

        async with get_relational_engine().get_async_session() as session:
            if await session.get(Dataset, dataset) is not None:
                return False
            session.add(Dataset(id=dataset, name=str(dataset), data=[], owner_id=user.id,
                                tenant_id=getattr(user, 'tenant_id', None)))
            try:
                await session.commit()
            except IntegrityError:
                await session.rollback()
                return False  # Another writer may have created the dataset. Recheck its ACL.

        for permission in ('read', 'write', 'delete', 'share'):
            await give_permission_on_dataset(user, dataset, permission)
        return True

    async def ingest(self, payload, content):
        from cognee import add, remember
        from cognee.memory import QAEntry, TraceEntry
        from cognee.modules.pipelines.models import PipelineRunErrored
        from cognee.tasks.ingestion.data_item import DataItem

        user, dataset = await self._dataset(payload.scope, 'write', allow_provision=True)
        meta = payload.source_meta
        if payload.kind == 'document':
            body = content if isinstance(content, str) else _json(content)
            item = DataItem(body, external_metadata=meta.external_metadata, literal_text=True, node_set=meta.node_set)
            run = await add(item, dataset_id=dataset, user=user, run_in_background=False)
            if isinstance(run, PipelineRunErrored) or not getattr(run, 'data_ids', None):
                raise PolicyError('official_document_ingest_unconfirmed', 503)
            return {'entry_id': str(run.data_ids[0]), 'document_id': str(run.data_ids[0])}

        entry_model = QAEntry if payload.kind == 'capture_qa' else TraceEntry
        session = meta.session_id
        if not session:
            raise PolicyError('capture_session_required', 422)
        if payload.kind == 'capture_qa':
            content = {**content, 'context': _json(content['context'])}
        try:
            entry = entry_model.model_validate(content)
        except ValidationError:
            raise PolicyError('invalid_capture_entry', 422) from None
        result = await remember(entry, dataset_id=dataset, user=user, session_id=session, node_set=meta.node_set)
        if result.status != 'session_stored' or result.entry_id is None:
            raise PolicyError('official_capture_ingest_unconfirmed', 503)
        return {'entry_id': str(result.entry_id), 'document_id': None}

    async def extract(self, payload):
        from cognee import cognify, improve
        from cognee.modules.pipelines.models import PipelineRunErrored

        user, dataset = await self._dataset(payload.scope, 'write', allow_provision=True)
        if payload.mode == 'tool_graph':
            result = await run_tool_graph_pipeline(user, dataset, payload.doc_ids)
            runs = result['pipeline_run_ids']
            return {'touched_doc_ids': payload.doc_ids if result['new_steps'] else [],
                    'pipeline_run_id': runs[0] if runs else None}
        if not payload.doc_ids:
            return {'touched_doc_ids': [], 'pipeline_run_id': None}
        documents, sessions = [], []
        for value in dict.fromkeys(payload.doc_ids):
            if await self._find_data(user, dataset, value):
                documents.append(value)
            else:
                sessions.append(value)
        runs = []
        touched = []
        if documents:
            # The pinned public cognify entry point processes the dataset's
            # incremental backlog. The returned data IDs are the actual writes.
            result = await cognify(datasets=[dataset], user=user, run_in_background=False)
            completed = list(result.values()) if isinstance(result, dict) else [result]
            if any(isinstance(run, PipelineRunErrored) for run in completed):
                raise PolicyError('official_extract_failed', 503)
            touched.extend(str(doc) for run in completed for doc in (run.data_ids or []))
            runs.extend(str(run.pipeline_run_id) for run in completed)
        if sessions:
            result = await improve(dataset=dataset, user=user, session_ids=sessions, run_in_background=False)
            if result.status == 'errored':
                raise PolicyError('official_extract_failed', 503)
            if result.status == 'completed':
                touched.extend(sessions)
            for stage in result.stages:
                if stage.run is not None:
                    runs.append(str(stage.run.pipeline_run_id))
                    touched.extend(str(doc) for doc in (getattr(stage.run, 'data_ids', None) or []))
        return {'touched_doc_ids': list(dict.fromkeys(touched)), 'pipeline_run_id': runs[0] if runs else None}

    async def search(self, payload, limit):
        from cognee import SearchType, search
        from cognee.modules.retrieval.exceptions.exceptions import NoDataError

        hits = []
        for scope in payload.scopes:
            user, dataset = await self._dataset(scope)
            try:
                result = await search(query_text=payload.query, query_type=SearchType.CHUNKS,
                                      user=user, dataset_ids=[dataset], top_k=limit)
            except NoDataError:
                continue
            for value in result or []:
                if isinstance(value, dict):
                    chunks = value.get('search_result', [value])
                    result_dataset = value.get('dataset_id')
                else:
                    chunks = getattr(value, 'search_result', value)
                    result_dataset = getattr(value, 'dataset_id', None)
                if isinstance(chunks, (dict, BaseModel)):
                    chunks = [chunks]
                if not isinstance(chunks, list):
                    continue
                for chunk in chunks:
                    if isinstance(chunk, BaseModel):
                        chunk = chunk.model_dump()
                    if not isinstance(chunk, dict):
                        continue
                    metadata = chunk.get('external_metadata')
                    if isinstance(metadata, str):
                        try:
                            metadata = json.loads(metadata)
                        except ValueError:
                            metadata = None
                    if not isinstance(metadata, dict):
                        metadata = chunk.get('metadata') or {}
                    kind = chunk.get('kind', metadata.get('kind', 'document'))
                    if payload.filters.kind is not None and kind != payload.filters.kind:
                        continue
                    parent = chunk.get('is_part_of')
                    if isinstance(parent, dict):
                        parent = parent.get('id')
                    elif not isinstance(parent, (str, UUID)):
                        parent = None
                    document = chunk.get('data_id') or chunk.get('document_id') or parent or chunk.get('id')
                    if document is not None:
                        hits.append({'dataset_id': str(result_dataset or dataset), 'document_id': str(document),
                                     'title': chunk.get('document_name') or chunk.get('title') or '',
                                     'snippet': chunk.get('text') or '', 'score': chunk.get('score'),
                                     'metadata': metadata})
        hits.sort(key=lambda hit: hit['score'] if hit['score'] is not None else float('inf'))
        return hits[:limit]

    async def list_items(self, scope, limit, offset):
        from cognee.modules.data.methods import get_dataset_data
        _, dataset = await self._dataset(scope)
        return [row.to_json() for row in await get_dataset_data(
            dataset, limit=limit, offset=offset, order_by='created_at')]

    @staticmethod
    async def _find_data(user, dataset, item_id):
        from cognee.modules.data.methods import get_data
        try:
            identifier = UUID(item_id)
        except ValueError:
            return None
        return await get_data(user.id, identifier, dataset_id=dataset, verify_owner=False)

    async def get_item(self, scope, item_id):
        user, dataset = await self._dataset(scope)
        row = await self._find_data(user, dataset, item_id)
        if row is None:
            raise PolicyError('item_not_found', 404)
        return row.to_json()

    async def delete_item(self, scope, item_id):
        from cognee import datasets
        user, dataset = await self._dataset(scope, 'delete')
        receipt = await datasets.delete_data(dataset, _uuid(item_id), user=user)
        if receipt['status'] == 'not_found':
            raise PolicyError('item_not_found', 404)
        if receipt['status'] != 'success':
            raise PolicyError('official_delete_unconfirmed', 503)
        return {'deleted': True}

    async def graph(self, dataset_id, filter_text):
        from cognee.modules.graph.methods.get_formatted_graph_data import get_formatted_graph_data
        user, dataset = await self._dataset(Scope(dataset_id=dataset_id))
        graph = await get_formatted_graph_data(dataset, user)
        if filter_text:
            nodes = [node for node in graph['nodes'] if filter_text.casefold() in
                     (str(node['label']) + ' ' + str(node['type'])).casefold()]
            ids = {node['id'] for node in nodes}
            return {'nodes': nodes, 'edges': [edge for edge in graph['edges']
                                              if edge['source'] in ids and edge['target'] in ids]}
        return graph


def _json(value):
    return json.dumps(value, sort_keys=True, ensure_ascii=False, separators=(',', ':'), allow_nan=False)


def _capture_content(payload, content_hash):
    if payload.kind == 'document':
        return payload.content
    if not isinstance(payload.content, dict):
        raise PolicyError('invalid_capture_entry', 422)
    content = dict(payload.content)
    if payload.kind == 'capture_qa':
        context = content.get('context')
        if isinstance(context, str):
            try:
                context = json.loads(context)
            except ValueError:
                context = {'text': context}
        if not isinstance(context, dict):
            context = {} if context is None else {'text': context}
        context_date = payload.source_meta.context_date
        existing_date = context.get('date')
        if context_date is not None:
            existing_date = context_date.isoformat()
        elif not isinstance(existing_date, str) or not existing_date:
            existing_date = date.today().isoformat()
        content['context'] = {**context, 'date': existing_date}
    else:
        if content.get('generate_feedback_with_llm') not in (False, None):
            raise PolicyError('capture_must_not_invoke_model', 422)
        content['generate_feedback_with_llm'] = False
        if payload.idempotency_key:
            params = content.get('method_params')
            if params is not None and not isinstance(params, dict):
                raise PolicyError('invalid_capture_entry', 422)
            marker = [payload.scope.model_dump(), payload.source_meta.session_id, payload.idempotency_key, content_hash]
            content['method_params'] = {**(params or {}), '_memlane_capture': {
                'key': hashlib.sha256(_json(marker).encode()).hexdigest()}}
    return content


def install_engine_api(app, *, handoff, store_path, token=None, token_file=None, backend: EngineBackend | None = None):
    if token is None and token_file is not None:
        token = read_secret(token_file)
    if not isinstance(token, str) or not re.fullmatch(r'[A-Za-z0-9._~+/-]+=*', token):
        raise ValueError('valid internal bearer token required')
    secret = token.encode()
    backend = CogneeBackend() if backend is None else backend
    receipts = OrderedDict()
    locks = tuple(asyncio.Lock() for _ in range(64))

    async def authorize(authorization: str | None = Header(None)):
        match = re.fullmatch(r'Bearer ([A-Za-z0-9._~+/-]+=*)', authorization or '', flags=re.IGNORECASE | re.ASCII)
        if match is None:
            raise HTTPException(401, 'internal_bearer_required', headers={'WWW-Authenticate': 'Bearer'})
        if not hmac.compare_digest(match[1].encode(), secret):
            raise HTTPException(403, 'internal_bearer_invalid')

    @app.exception_handler(PolicyError)
    async def policy_error(_request, error):
        return JSONResponse({'detail': error.code}, status_code=error.status)

    router = APIRouter(prefix='/internal/v1', dependencies=[Depends(authorize)])

    @router.get('/health')
    async def health():
        return {'ok': True, 'engine': ENGINE_NAME, 'version': ENGINE_VERSION, 'mode': handoff.mode,
                'model_digest': handoff.active_digest, 'store_path': str(store_path),
                'capabilities': {'tool_graph': True, 'language_egress': True, 'model_handoff': True}}

    @router.post('/ingest')
    async def ingest(payload: Ingest):
        try:
            raw = payload.content if isinstance(payload.content, str) else _json(payload.content)
            content_hash = hashlib.sha256(raw.encode()).hexdigest()
        except (ValueError, UnicodeError):
            raise PolicyError('invalid_ingest_content', 422) from None
        content = _capture_content(payload, content_hash)

        async def save():
            receipt = await backend.ingest(payload, content)
            return {**receipt, 'ok': True, 'content_hash': content_hash, 'deduplicated': False}

        if not payload.idempotency_key:
            return await save()
        key = (payload.scope.dataset_id, payload.scope.user_id, payload.kind, payload.idempotency_key)
        async with locks[hash(key) % len(locks)]:
            if key in receipts:
                receipt = receipts[key]
                if receipt['content_hash'] != content_hash:
                    raise PolicyError('idempotency_key_conflict', 409)
                receipts.move_to_end(key)
                return {**receipt, 'deduplicated': True}
            receipt = await save()
            receipts[key] = receipt
            if len(receipts) > 4096:
                receipts.popitem(last=False)
            return receipt

    @router.post('/extract')
    async def extract(payload: Extract):
        handoff.verify_expected_digest(payload.expected_model_digest)
        with scoped_output_language(payload.output_language):
            result = await backend.extract(payload)
        return {**result, 'ok': True, 'model_digest': payload.expected_model_digest}

    @router.post('/search')
    async def search(payload: Search):
        limit = payload.limit or payload.filters.limit or 50
        return {'ok': True, 'hits': await backend.search(payload, limit)}

    @router.get('/items')
    async def list_items(dataset_id: str, user_id: str | None = None,
                         limit: int = Query(50, ge=1, le=100), cursor: str | None = None):
        if cursor is not None and not re.fullmatch(r'[0-9]{1,10}', cursor):
            raise PolicyError('invalid_cursor', 422)
        offset = int(cursor) if cursor else 0
        items = await backend.list_items(Scope(dataset_id=dataset_id, user_id=user_id), limit + 1, offset)
        return {'ok': True, 'items': items[:limit],
                'next_cursor': str(offset + limit) if len(items) > limit else None}

    @router.get('/items/{item_id}')
    async def get_item(item_id: str, dataset_id: str, user_id: str | None = None):
        item = await backend.get_item(Scope(dataset_id=dataset_id, user_id=user_id), item_id)
        return {'ok': True, 'item': item}

    @router.delete('/items/{item_id}')
    async def delete_item(item_id: str, dataset_id: str, user_id: str | None = None):
        result = await backend.delete_item(Scope(dataset_id=dataset_id, user_id=user_id), item_id)
        return {**result, 'ok': True, 'item_id': item_id}

    @router.get('/graph')
    async def graph(dataset_id: str, filter: str | None = None):
        return {'ok': True, **await backend.graph(dataset_id, filter)}

    @router.post('/model/apply')
    async def apply_model(payload: ModelApply):
        return await handoff.apply(payload.operation_id, payload.generation_config,
                                   output_language=payload.output_language)

    @router.get('/model/status')
    async def model_status():
        return handoff.status()

    app.include_router(router)
