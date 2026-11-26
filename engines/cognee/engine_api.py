"""Loopback-only internal v1 transport and pinned Cognee SDK adapter."""
import asyncio
from collections import OrderedDict
from datetime import date
import hashlib
import hmac
import json
import logging
import re
from typing import Any, Literal, Protocol
from uuid import UUID

from fastapi import APIRouter, Depends, Header, HTTPException, Query
from fastapi.responses import JSONResponse
from pydantic import BaseModel, Field, ValidationError

from . import ENGINE_NAME, ENGINE_VERSION, PolicyError, read_secret
from .language_egress import scoped_output_language
from .tool_graph import run_tool_graph_pipeline

logger = logging.getLogger(__name__)
_SUPERSEDED_TAG_RE = re.compile(r'\[superseded(?:;|\])', re.IGNORECASE)
_SEARCH_MODES = ['hybrid', 'graph', 'chunks', 'code']


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
    node_sets: list[str] | None = None
    session_id: str | None = None
    include_session_context: bool = False

class Search(BaseModel):
    scopes: list[Scope]
    query: str
    mode: Literal['chunks', 'hybrid', 'graph', 'code'] = 'chunks'
    filters: SearchFilters = Field(default_factory=SearchFilters)
    limit: int | None = Field(default=None, ge=1, le=100)

class ModelApply(BaseModel):
    generation_config: dict
    output_language: str | None = None
    operation_id: str


class EngineBackend(Protocol):
    async def ingest(self, payload: Ingest, content: str | dict) -> dict: ...
    async def extract(self, payload: Extract) -> dict: ...
    async def search(self, payload: Search, limit: int) -> list[dict] | dict: ...
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

        node_sets = _clean_strings(payload.filters.node_sets) or None
        authorized = await asyncio.gather(*(self._dataset(scope) for scope in payload.scopes))

        async def search_chunks(user, dataset):
            kwargs = {
                'query_text': payload.query,
                'query_type': SearchType.CHUNKS,
                'user': user,
                'dataset_ids': [dataset],
                'top_k': limit,
            }
            if node_sets:
                kwargs['node_name'] = node_sets
            try:
                result = await search(**kwargs)
            except NoDataError:
                return None
            return _parse_chunk_results(result, dataset, payload.filters.kind)

        async def search_scope(user, dataset):
            if payload.mode == 'chunks':
                parsed = await search_chunks(user, dataset)
                if parsed is None:
                    return _empty_scope_result()
                return {'hits': parsed[0], 'entities': [], 'facts': [], 'conflicts': parsed[1],
                        'code_facts': [], 'degraded_to_chunks': False}

            if payload.mode in ('hybrid', 'graph'):
                query_type = SearchType.HYBRID_COMPLETION if payload.mode == 'hybrid' else SearchType.GRAPH_COMPLETION
                parser = _parse_hybrid_results if payload.mode == 'hybrid' else _parse_graph_results
                try:
                    result = await search(
                        query_text=payload.query,
                        query_type=query_type,
                        user=user,
                        dataset_ids=[dataset],
                        top_k=limit,
                        node_name=node_sets,
                        only_context=True,
                        verbose=True,
                        include_references=True,
                    )
                except NoDataError:
                    degraded = await search_chunks(user, dataset)
                    if degraded is None:
                        return _empty_scope_result()
                    return {'hits': degraded[0], 'entities': [], 'facts': [], 'conflicts': degraded[1],
                            'code_facts': [], 'degraded_to_chunks': True}
                hits, entities, facts, conflicts = parser(result, dataset, payload.filters.kind)
                return {'hits': hits, 'entities': entities, 'facts': facts, 'conflicts': conflicts,
                        'code_facts': [], 'degraded_to_chunks': False}

            from cognee.modules.retrieval.code_retriever import CodeSeedNotFoundError
            try:
                result = await search(
                    query_text=payload.query,
                    query_type=SearchType.CODE,
                    user=user,
                    dataset_ids=[dataset],
                    top_k=limit,
                )
            except (NoDataError, CodeSeedNotFoundError):
                return _empty_scope_result()
            return {'hits': [], 'entities': [], 'facts': [], 'conflicts': [],
                    'code_facts': _parse_code_results(result, dataset, limit), 'degraded_to_chunks': False}

        session_id = (payload.filters.session_id or '').strip()
        scope_tasks = [search_scope(user, dataset) for user, dataset in authorized]
        if payload.filters.include_session_context and session_id:
            session_user = authorized[0][0] if authorized else None
            gathered = await asyncio.gather(*scope_tasks, self._read_session_context(session_user, session_id, payload.query))
            scope_results, session_guidance = gathered[:-1], gathered[-1]
        else:
            scope_results = await asyncio.gather(*scope_tasks)
            session_guidance = ''

        hits, entities, facts, conflicts, code_facts = [], [], [], [], []
        seen_facts, seen_code_facts = set(), set()
        degraded_to_chunks = False
        for part in scope_results:
            hits.extend(part['hits'])
            entities.extend(part['entities'])
            for fact in part['facts']:
                key = (fact['dataset_id'], fact['text'])
                if key not in seen_facts:
                    seen_facts.add(key)
                    facts.append(fact)
            conflicts.extend(part['conflicts'])
            for code_fact in part['code_facts']:
                key = (code_fact['dataset_id'], code_fact['name'], code_fact.get('file', ''), code_fact.get('line', 0))
                if key not in seen_code_facts:
                    seen_code_facts.add(key)
                    code_facts.append(code_fact)
            degraded_to_chunks = degraded_to_chunks or part['degraded_to_chunks']

        hits.sort(key=lambda hit: hit['score'] if hit['score'] is not None else float('inf'))
        unique_conflicts = list(dict.fromkeys(c for c in conflicts if c))
        graph_context = {}
        if entities:
            graph_context['entities'] = entities
        if facts:
            graph_context['facts'] = facts
        if unique_conflicts:
            graph_context['conflicts'] = unique_conflicts
        if session_guidance:
            graph_context['session_guidance'] = session_guidance
        if code_facts:
            graph_context['code_facts'] = code_facts[:limit]
        if degraded_to_chunks:
            graph_context['degraded_to_chunks'] = True
        return {'hits': hits[:limit], 'graph_context': graph_context or None}

    @staticmethod
    async def _read_session_context(user, session_id: str, query_text: str) -> str:
        try:
            from cognee.infrastructure.session.get_session_manager import get_session_manager
            from cognee.infrastructure.session.session_context_builder import build_active_context_block

            sm = get_session_manager()
            if not getattr(sm, 'is_available', False):
                return ''
            user_id = getattr(user, 'id', None)
            if user_id is None:
                from cognee.modules.users.methods.get_default_user import get_default_user
                user_id = getattr(await get_default_user(), 'id', None)
            if user_id is None:
                return ''
            block, _served = await build_active_context_block(
                session_manager=sm,
                user_id=str(user_id),
                session_id=session_id,
                query=query_text,
                stamp_served=False,
            )
            return block.strip() if isinstance(block, str) else ''
        except Exception as error:
            logger.warning('Failed to load read-only session context for %s: %s', session_id, error)
            return ''

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


def _empty_scope_result() -> dict:
    return {'hits': [], 'entities': [], 'facts': [], 'conflicts': [], 'code_facts': [], 'degraded_to_chunks': False}


def _text(value: Any) -> str:
    if value is None or isinstance(value, bool):
        return ''
    if isinstance(value, (str, int, float, UUID)):
        return str(value).strip()
    return ''


def _clean_strings(values: Any) -> list[str]:
    if not isinstance(values, (list, tuple, set)):
        return []
    return list(dict.fromkeys(text for value in values if (text := _text(value))))


def _as_dict(value: Any) -> dict | None:
    if isinstance(value, BaseModel):
        return value.model_dump()
    if isinstance(value, dict):
        return value
    return None


def _chunk_dict(chunk: Any) -> dict | None:
    raw = _as_dict(chunk)
    if raw is None:
        nested = getattr(chunk, 'payload', None)
        if isinstance(nested, dict):
            raw = {'id': getattr(chunk, 'id', None), 'score': getattr(chunk, 'score', None), 'payload': nested}
        else:
            return None
    nested = raw.get('payload')
    if isinstance(nested, dict):
        merged = dict(nested)
        if raw.get('score') is not None:
            merged['score'] = raw['score']
        if raw.get('id') is not None and not merged.get('id'):
            merged['id'] = raw['id']
        return merged
    return raw


def _chunk_to_hit(chunk: Any, dataset_str: str, kind_filter: str | None,
                  evidence_by_chunk: dict[str, dict] | None = None) -> tuple[dict | None, list[str]]:
    item = _chunk_dict(chunk)
    if item is None:
        return None, []
    metadata = item.get('external_metadata')
    if isinstance(metadata, str):
        try:
            metadata = json.loads(metadata)
        except ValueError:
            metadata = None
    if not isinstance(metadata, dict):
        metadata = item.get('metadata') or {}
    kind = item.get('kind', metadata.get('kind', 'document'))
    if kind_filter is not None and kind != kind_filter:
        return None, []
    parent = item.get('is_part_of')
    if isinstance(parent, dict):
        parent = parent.get('id')
    elif not isinstance(parent, (str, UUID)):
        parent = None
    chunk_id = _text(item.get('id'))
    ev = (evidence_by_chunk or {}).get(chunk_id) if chunk_id else None
    document = (item.get('data_id') or item.get('document_id') or parent
                or (ev.get('data_id') if ev else None) or item.get('id'))
    chunk_conflicts = _clean_strings(item.get('conflicts'))
    if not document:
        return None, chunk_conflicts
    title = item.get('document_name') or item.get('title') or (ev.get('document_name') if ev else '') or ''
    return ({
        'dataset_id': dataset_str,
        'document_id': str(document),
        'title': title,
        'snippet': item.get('text') or '',
        'score': item.get('score'),
        'metadata': metadata,
    }, chunk_conflicts)


def _parse_chunk_results(result: Any, dataset: UUID, kind_filter: str | None) -> tuple[list[dict], list[str]]:
    hits, conflicts = [], []
    for value in result or []:
        if isinstance(value, dict):
            chunks = value.get('search_result', [value])
            result_dataset = value.get('dataset_id')
        else:
            chunks = getattr(value, 'search_result', value)
            result_dataset = getattr(value, 'dataset_id', None)
        if isinstance(chunks, (dict, BaseModel)) or isinstance(getattr(chunks, 'payload', None), dict):
            chunks = [chunks]
        if not isinstance(chunks, list):
            continue
        dataset_str = str(result_dataset or dataset)
        for chunk in chunks:
            hit, chunk_conflicts = _chunk_to_hit(chunk, dataset_str, kind_filter)
            if hit is not None:
                hits.append(hit)
            conflicts.extend(chunk_conflicts)
    return hits, conflicts


def _edge_is_superseded(attrs: dict, text: str) -> bool:
    raw = attrs.get('superseded')
    if raw is not None:
        return bool(raw)
    marks = attrs.get('conflict_marks')
    if isinstance(marks, list):
        if any(isinstance(mark, dict) and mark.get('resolution_source') == 'user'
               and mark.get('status') == 'current' for mark in marks):
            return False
        if any(isinstance(mark, dict) and mark.get('status') == 'superseded' for mark in marks):
            return True
    return bool(_SUPERSEDED_TAG_RE.search(text))


def _normalize_evidence(raw_evidence: Any) -> tuple[list[dict], dict[str, dict]]:
    items, by_chunk = [], {}
    for entry in raw_evidence or []:
        ev = entry.model_dump(mode='json') if isinstance(entry, BaseModel) else (entry if isinstance(entry, dict) else None)
        if ev is None:
            continue
        items.append(ev)
        if ev.get('kind') == 'segment':
            chunk_id = _text(ev.get('chunk_id') or ev.get('artifact_id'))
            if chunk_id and chunk_id not in by_chunk:
                by_chunk[chunk_id] = ev
    return items, by_chunk


def _evidence_to_hits(evidence_list: list[dict], dataset_str: str, kind_filter: str | None,
                      chunk_text_by_id: dict[str, str] | None = None) -> list[dict]:
    hits, seen = [], set()
    for ev in evidence_list:
        if ev.get('kind') != 'segment' and not (ev.get('data_id') or ev.get('document_id')):
            continue
        metadata = ev.get('metadata') if isinstance(ev.get('metadata'), dict) else {}
        if kind_filter is not None and metadata.get('kind', 'document') != kind_filter:
            continue
        ev_dataset = _text(ev.get('dataset_id')) or dataset_str
        chunk_id = _text(ev.get('chunk_id') or ev.get('artifact_id'))
        doc_id = _text(ev.get('data_id') or ev.get('document_id') or chunk_id)
        if not doc_id:
            continue
        key = (ev_dataset, doc_id, chunk_id)
        if key in seen:
            continue
        seen.add(key)
        hit_meta = dict(metadata)
        for field in ('chunk_id', 'chunk_index', 'role'):
            if ev.get(field) is not None and field not in hit_meta:
                hit_meta[field] = ev[field]
        hits.append({
            'dataset_id': ev_dataset,
            'document_id': doc_id,
            'title': _text(ev.get('document_name') or ev.get('title')),
            'snippet': _text(ev.get('text')) or (chunk_text_by_id or {}).get(chunk_id, ''),
            'score': ev.get('score') if isinstance(ev.get('score'), (int, float)) and not isinstance(ev.get('score'), bool) else None,
            'metadata': hit_meta,
        })
    return hits


def _parse_context_conflicts(context_result: Any) -> list[str]:
    if not isinstance(context_result, str) or '## Fact conflicts' not in context_result:
        return []
    section = context_result.split('## Fact conflicts', 1)[1]
    conflicts = []
    for raw_line in section.splitlines()[1:]:
        line = raw_line.strip()
        if line.startswith('## '):
            break
        if line.startswith('- ') and (item := line[2:].strip()):
            conflicts.append(item)
    return conflicts


def _verbose_entry(value: Any, dataset: UUID) -> tuple[str, Any, list[dict], dict[str, dict], Any]:
    if isinstance(value, dict):
        result_dataset = value.get('dataset_id')
        objects_result = value.get('objects_result')
        if objects_result is None and 'search_result' in value:
            objects_result = value.get('search_result')
        raw_evidence = value.get('evidence')
        context_result = value.get('context_result')
    else:
        result_dataset = getattr(value, 'dataset_id', None)
        objects_result = getattr(value, 'objects_result', None)
        if objects_result is None:
            objects_result = getattr(value, 'result_object', None)
        raw_evidence = getattr(value, 'evidence', None)
        context_result = getattr(value, 'context_result', getattr(value, 'context', None))
    evidence_list, evidence_by_chunk = _normalize_evidence(raw_evidence)
    return str(result_dataset or dataset), objects_result, evidence_list, evidence_by_chunk, context_result


def _parse_hybrid_results(result: Any, dataset: UUID, kind_filter: str | None) -> tuple[list[dict], list[dict], list[dict], list[str]]:
    hits, entities, facts, conflicts = [], [], [], []
    for value in result or []:
        dataset_str, objects_result, evidence_list, evidence_by_chunk, context_result = _verbose_entry(value, dataset)
        if isinstance(objects_result, list):
            g_hits, g_entities, g_facts, g_conflicts = _parse_graph_objects(
                objects_result, evidence_list, context_result, dataset_str, kind_filter)
            hits.extend(g_hits)
            entities.extend(g_entities)
            facts.extend(g_facts)
            conflicts.extend(g_conflicts)
            continue
        if not isinstance(objects_result, dict):
            hits.extend(_evidence_to_hits(evidence_list, dataset_str, kind_filter))
            conflicts.extend(_parse_context_conflicts(context_result))
            continue

        raw_chunks = objects_result.get('chunks') or []
        for chunk in raw_chunks:
            hit, chunk_conflicts = _chunk_to_hit(chunk, dataset_str, kind_filter, evidence_by_chunk)
            if hit is not None:
                hits.append(hit)
            conflicts.extend(chunk_conflicts)
        if not raw_chunks and evidence_list:
            hits.extend(_evidence_to_hits(evidence_list, dataset_str, kind_filter))

        for raw_entity in objects_result.get('entities') or []:
            entity = _as_dict(raw_entity)
            if entity is None:
                continue
            name = _text(entity.get('name'))
            if not name:
                continue
            entity_edges = []
            for raw_edge in entity.get('edges') or []:
                edge = _as_dict(raw_edge)
                if edge is None:
                    continue
                source, target = _text(edge.get('source')), _text(edge.get('target'))
                relationship = _text(edge.get('relationship'))
                edge_text = _text(edge.get('text')) or (
                    f'{source} -- {relationship} -- {target}' if source and relationship and target else '')
                if not edge_text:
                    continue
                entity_edges.append({
                    'text': edge_text,
                    'source': source,
                    'target': target,
                    'relationship': relationship,
                    'superseded': _edge_is_superseded(edge, edge_text),
                    'authoritative': bool(edge.get('authoritative')),
                })
            entity_conflicts = _clean_strings(entity.get('conflicts'))
            conflicts.extend(entity_conflicts)
            entity_item = {'dataset_id': dataset_str, 'name': name, 'type': _text(entity.get('type')), 'edges': entity_edges}
            if entity_conflicts:
                entity_item['conflicts'] = entity_conflicts
            entities.append(entity_item)

        for raw_fact in objects_result.get('facts') or []:
            if isinstance(raw_fact, str):
                fact_text = raw_fact.strip()
            else:
                fact_dict = _as_dict(raw_fact)
                fact_text = _text(fact_dict.get('text') if fact_dict is not None else getattr(raw_fact, 'text', None))
            if fact_text:
                facts.append({'dataset_id': dataset_str, 'text': fact_text})
        conflicts.extend(_parse_context_conflicts(context_result))
    return hits, entities, facts, conflicts


def _parse_graph_objects(objects_result: Any, evidence_list: list[dict], context_result: Any,
                         dataset_str: str, kind_filter: str | None) -> tuple[list[dict], list[dict], list[dict], list[str]]:
    triplets = objects_result.get('triplets') if isinstance(objects_result, dict) else objects_result
    if not isinstance(triplets, (list, tuple)):
        triplets = []
    chunk_text_by_id: dict[str, str] = {}
    entities_by_name: OrderedDict[str, dict] = OrderedDict()
    facts: list[dict] = []

    for triplet in triplets:
        source = source_type = target = relationship = edge_text = ''
        superseded = authoritative = False
        if isinstance(triplet, str):
            edge_text = triplet.strip()
        elif hasattr(triplet, 'node1') and hasattr(triplet, 'node2'):
            n1, n2 = triplet.node1, triplet.node2
            n1_attrs = getattr(n1, 'attributes', None) or {}
            n2_attrs = getattr(n2, 'attributes', None) or {}
            attrs = getattr(triplet, 'attributes', None) or {}
            for node, node_attrs in ((n1, n1_attrs), (n2, n2_attrs)):
                if node_attrs.get('type') == 'DocumentChunk' and (chunk_text := _text(node_attrs.get('text'))):
                    chunk_text_by_id[_text(getattr(node, 'id', None))] = chunk_text
            source = _text(n1_attrs.get('name')) or _text(getattr(n1, 'id', None))
            source_type = _text(n1_attrs.get('type'))
            target = _text(n2_attrs.get('name')) or _text(getattr(n2, 'id', None))
            relationship = _text(attrs.get('relationship_name') or attrs.get('relationship_type'))
            edge_text = _text(attrs.get('edge_text'))
            authoritative = bool(attrs.get('authoritative'))
            superseded = _edge_is_superseded(attrs, edge_text)
        elif (item := _as_dict(triplet)) is not None:
            n1_attrs = item.get('source_node_attributes') or (item.get('node1') or {}).get('node_attributes') or {}
            n2_attrs = item.get('target_node_attributes') or (item.get('node2') or {}).get('node_attributes') or {}
            attrs = item.get('edge_attributes') or item.get('attributes') or item
            for node_id, node_attrs in ((item.get('source_node_id'), n1_attrs), (item.get('target_node_id'), n2_attrs)):
                if isinstance(node_attrs, dict) and node_attrs.get('type') == 'DocumentChunk' and (chunk_text := _text(node_attrs.get('text'))):
                    chunk_text_by_id[_text(node_id)] = chunk_text
            source = _text(item.get('source') or (n1_attrs.get('name') if isinstance(n1_attrs, dict) else None) or item.get('source_node_id'))
            source_type = _text(n1_attrs.get('type') if isinstance(n1_attrs, dict) else item.get('source_type'))
            target = _text(item.get('target') or (n2_attrs.get('name') if isinstance(n2_attrs, dict) else None) or item.get('target_node_id'))
            relationship = _text((attrs.get('relationship_name') or attrs.get('relationship_type')) if isinstance(attrs, dict) else None) or _text(item.get('relationship'))
            edge_text = _text((attrs.get('edge_text') if isinstance(attrs, dict) else None) or item.get('text'))
            authoritative = bool(attrs.get('authoritative') if isinstance(attrs, dict) else item.get('authoritative'))
            superseded = _edge_is_superseded(attrs if isinstance(attrs, dict) else item, edge_text)
        if not edge_text and source and relationship and target:
            edge_text = f'{source} -- {relationship} -- {target}'
        if not edge_text:
            continue
        facts.append({'dataset_id': dataset_str, 'text': edge_text})
        if source and source_type != 'DocumentChunk':
            entity = entities_by_name.setdefault(source, {
                'dataset_id': dataset_str, 'name': source, 'type': source_type, 'edges': [],
            })
            entity['edges'].append({
                'text': edge_text, 'source': source, 'target': target, 'relationship': relationship,
                'superseded': superseded, 'authoritative': authoritative,
            })

    hits = _evidence_to_hits(evidence_list, dataset_str, kind_filter, chunk_text_by_id)
    conflicts = _parse_context_conflicts(context_result)
    return hits, list(entities_by_name.values()), facts, conflicts


def _parse_graph_results(result: Any, dataset: UUID, kind_filter: str | None) -> tuple[list[dict], list[dict], list[dict], list[str]]:
    hits, entities, facts, conflicts = [], [], [], []
    for value in result or []:
        dataset_str, objects_result, evidence_list, _, context_result = _verbose_entry(value, dataset)
        p_hits, p_entities, p_facts, p_conflicts = _parse_graph_objects(
            objects_result, evidence_list, context_result, dataset_str, kind_filter)
        hits.extend(p_hits)
        entities.extend(p_entities)
        facts.extend(p_facts)
        conflicts.extend(p_conflicts)
    return hits, entities, facts, conflicts


def _parse_code_results(result: Any, dataset: UUID, limit: int) -> list[dict]:
    code_facts, seen = [], set()
    for value in result or []:
        if isinstance(value, dict):
            result_dataset = value.get('dataset_id')
            body = value.get('search_result')
            if body is None:
                body = value.get('objects_result', value)
        else:
            result_dataset = getattr(value, 'dataset_id', None)
            body = getattr(value, 'search_result', getattr(value, 'objects_result', value))
        dataset_str = str(result_dataset or dataset)
        if isinstance(body, BaseModel):
            body = body.model_dump()
        raw_facts, edges_by_source = [], {}
        if isinstance(body, dict):
            if body.get('seed_not_found'):
                continue
            for edge in body.get('edges') or []:
                edge_dict = _as_dict(edge)
                if edge_dict is None:
                    continue
                rel_type, target_name = _text(edge_dict.get('type')), _text(edge_dict.get('target'))
                if not rel_type or not target_name:
                    continue
                for src_key in (_text(edge_dict.get('source_id')), _text(edge_dict.get('source'))):
                    if src_key:
                        edges_by_source.setdefault(src_key, []).append({'type': rel_type, 'target': target_name})
            if isinstance(body.get('focus'), dict):
                raw_facts.append(body['focus'])
            for list_key in ('facts', 'nodes', 'hits', 'seeds'):
                if isinstance(body.get(list_key), list):
                    raw_facts.extend(body[list_key])
        elif isinstance(body, list):
            raw_facts.extend(body)

        for raw in raw_facts:
            item = _as_dict(raw)
            if item is None:
                continue
            name = _text(item.get('name'))
            if not name:
                continue
            file_path = _text(item.get('file') or item.get('file_path'))
            raw_line = item.get('line')
            line = raw_line if isinstance(raw_line, int) and not isinstance(raw_line, bool) else 0
            key = (dataset_str, name, file_path, line)
            if key in seen:
                continue
            seen.add(key)
            relations = []
            raw_relations = item.get('relations')
            if not raw_relations:
                raw_relations = edges_by_source.get(_text(item.get('id'))) or edges_by_source.get(name) or []
            for rel in raw_relations or []:
                rel_dict = _as_dict(rel)
                if rel_dict is None:
                    continue
                rel_type, target_name = _text(rel_dict.get('type')), _text(rel_dict.get('target'))
                if rel_type and target_name:
                    relations.append({'type': rel_type, 'target': target_name})
            fact = {'dataset_id': dataset_str, 'name': name}
            if kind := _text(item.get('kind') or item.get('type')):
                fact['kind'] = kind
            if file_path:
                fact['file'] = file_path
            if line:
                fact['line'] = line
            if relations:
                fact['relations'] = relations
            code_facts.append(fact)
            if len(code_facts) >= limit:
                return code_facts
    return code_facts


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
                'capabilities': {
                    'tool_graph': True, 'language_egress': True, 'model_handoff': True,
                    'hybrid_recall': True, 'session_context': True, 'code_search': True,
                    'search_modes': list(_SEARCH_MODES),
                }}

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
        result = await backend.search(payload, limit)
        if isinstance(result, dict):
            hits = result.get('hits', [])
            graph_context = result.get('graph_context')
        else:
            hits = result
            graph_context = None
        response = {'ok': True, 'hits': hits}
        if graph_context:
            response['graph_context'] = graph_context
        return response

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
