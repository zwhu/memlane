"""Bounded, explicit Memlane material ingestion through Cognee 1.6.7 pipelines.

No global model/environment mutation. The caller supplies one ephemeral owner
schema gateway and a frozen exact version. This module never discovers files,
URLs, conversations or other pending dataset items.
"""
import asyncio
import hashlib
import os
from urllib.parse import urlsplit
from uuid import UUID, uuid5, NAMESPACE_OID


def validate(payload, instance):
    material = payload.get('material', {})
    for key in ('library', 'operation', 'instance', 'dataset', 'project', 'memory'):
        if str(UUID(material[key])) != material[key]:
            raise ValueError('canonical_material_identity_required')
    processing = material.get('processing', {})
    if not processing.get('revision', {}).get('config_digest') or len(processing.get('dependency_digest', '')) != 64:
        raise ValueError('frozen_processing_identity_required')
    text = material.get('text', '')
    if material['instance'] != instance or material.get('origin') not in ('knowledge', 'raw') or type(material.get('revision')) is not int or material['revision'] < 1:
        raise ValueError('material_identity_changed')
    if not text or len(text.encode()) > 256 * 1024 or hashlib.sha256(text.encode()).hexdigest() != material.get('digest'):
        raise ValueError('material_body_digest_changed')
    gateway = payload.get('gateway', {})
    endpoint = urlsplit(gateway.get('endpoint', ''))
    if endpoint.scheme != 'http' or endpoint.hostname != '127.0.0.1' or not endpoint.port or endpoint.path != '/v1' or endpoint.query or endpoint.fragment or endpoint.username:
        raise ValueError('private_owner_gateway_required')
    token = gateway.get('token', '')
    if not token or len(token) > 256 or any(c.isspace() for c in token):
        raise ValueError('private_owner_gateway_token_required')
    return material, gateway


def document_id(material):
    # Distinct exact versions and libraries never alias each other's provenance.
    return uuid5(NAMESPACE_OID, '\0'.join(str(material[k]) for k in ('library', 'instance', 'dataset', 'project', 'memory', 'revision', 'digest')) + '\0' + material['processing']['revision']['config_digest'] + '\0' + material['processing']['dependency_digest'])


def material_data_item(material, identity, metadata):
    from cognee.tasks.ingestion.data_item import DataItem
    return DataItem(data=material['text'], literal_text=True, data_id=identity, external_metadata=metadata)


def require_completed_material_run(result, dataset_id, identity):
    from cognee.modules.pipelines.models.PipelineRunInfo import PipelineRunCompleted
    if isinstance(result, dict) and list(result) != [dataset_id]:
        raise ValueError('official_material_not_processed')
    runs = list(result.values()) if isinstance(result, dict) else [result]
    if len(runs) != 1:
        raise ValueError('official_material_not_processed')
    run = runs[0]
    if not isinstance(run, PipelineRunCompleted) or run.status != 'PipelineRunCompleted' or run.dataset_id != dataset_id or run.after_run_error or run.data_ids != [identity]:
        raise ValueError('official_material_not_processed')
    entries = run.data_ingestion_info
    if not isinstance(entries, list) or len(entries) != 1:
        raise ValueError('official_material_not_processed')
    entry = entries[0]
    item = entry.get('run_info') if isinstance(entry, dict) else None
    if not isinstance(item, PipelineRunCompleted) or item.status != 'PipelineRunCompleted' or item.dataset_id != dataset_id or item.after_run_error or entry.get('data_id') != identity or entry.get('data_ids', [identity]) != [identity]:
        raise ValueError('official_material_not_processed')

    return [str(r.pipeline_run_id) for r in (run, item) if getattr(r, 'pipeline_run_id', None) is not None]


async def submit_official(material, gateway, user):
    from cognee.modules.data.methods import get_dataset_data
    from cognee.modules.pipelines import Task, run_pipeline
    from cognee.modules.pipelines.layers.pipeline_execution_mode import run_pipeline_blocking
    from cognee.modules.migrations.startup import run_migrations_and_block
    from cognee.modules.pipelines.layers.resolve_authorized_user_dataset import resolve_authorized_user_dataset
    from cognee.tasks.ingestion import resolve_data_directories, ingest_data
    from cognee.infrastructure.llm.config import LLMConfig
    from cognee.api.v1.cognify.cognify import get_default_tasks, raise_if_cognify_errored
    from cognee.infrastructure.files.utils.open_data_file import open_data_file
    from cognee.modules.cognify.rollback import cognify_rollback_handler
    from cognee.tasks.ingestion.document_structure import reconcile_document_structure
    from cognee.infrastructure.databases.vector.compact_vector_store import compact_vector_store

    dataset_id = UUID(material['dataset'])
    # Explicit write permission, never merely read-authorized dataset lookup.
    _, dataset = await resolve_authorized_user_dataset(dataset_name="", dataset_id=dataset_id, user=user)
    await run_migrations_and_block([dataset_id], user)
    alias = 'openai/memlane-processing'
    config = LLMConfig(_env_prefix='MEMLANE_SUBMISSION_ISOLATED_', _env_file=None, llm_provider='custom', llm_model=alias, llm_endpoint=gateway['endpoint'],
                       llm_api_key=gateway['token'], llm_max_completion_tokens=8192, llm_streaming=False, llm_args={},
                       **{f'llm_{stage}_{field}': value for stage in ('extraction', 'summarization', 'query') for field, value in dict(model=alias, provider='custom', endpoint=gateway['endpoint'], api_key=gateway['token']).items()})
    from cognee_processing_adapter import install as install_adapter
    install_adapter()
    identity = document_id(material)
    metadata = {'memlane_material_v1': {k: material[k] for k in ('library', 'instance', 'dataset', 'project', 'memory', 'revision', 'origin', 'digest')} | {'processing_revision_digest': material['processing']['revision']['config_digest'], 'processing_dependency_digest': material['processing']['dependency_digest']}}
    records = await get_dataset_data(dataset_id, limit=4097, order_by='created_at')
    if len(records) > 4096:
        raise ValueError('dataset_document_budget_exceeded')
    record = next((r for r in records if r.id == identity), None)
    if record is None:
        # The SDK literal flag prevents URL/path interpretation without unsupported streams.
        item = material_data_item(material, identity, metadata)
        tasks = [Task(resolve_data_directories, include_subdirectories=False),
                 Task(ingest_data, dataset.name, user, None, dataset_id, None, 0.5)]
        result = await run_pipeline_blocking(pipeline=run_pipeline, tasks=tasks, data=[item], datasets=[dataset_id],
                                            user=user, pipeline_name='add_pipeline', llm_config=config,
                                            skip_connection_test=True, incremental_loading=True, data_cache=False)
        raise_if_cognify_errored(result)
        records = await get_dataset_data(dataset_id, limit=4097, order_by='created_at')
        record = next((r for r in records if r.id == identity), None)
    if record is None or (record.external_metadata or {}).get('memlane_material_v1') != metadata['memlane_material_v1'] or record.mime_type != 'text/plain':
        raise ValueError('official_material_lineage_mismatch')
    async with open_data_file(record.raw_data_location, mode='r', encoding='utf-8') as file:
        stored = file.read(256 * 1024 + 1)
    if stored != material['text']:
        raise ValueError('official_material_body_mismatch')
    # Nonempty exact Data list is critical: no fallback to whole pending dataset.
    from cognee_grounded_graph import MaterialGroundedExtractor
    # Trusted process configuration, never supplied by HTTP material.
    extractor = MaterialGroundedExtractor(material, identity, os.path.realpath(os.environ['SYSTEM_ROOT_DIRECTORY']))
    try:
        # Exactly extract + independent batch judge per chunk. Summaries use
        # accepted extraction, not an extra model call. No automatic retries.
        tasks = await get_default_tasks(user=user, chunk_size=1024, chunks_per_batch=1, early_chunk_indexing=False,
                                        summary_method='from_extraction', calculate_chunk_graphs=extractor)
        result = await run_pipeline_blocking(pipeline=run_pipeline, tasks=tasks, data=[record], datasets=[dataset_id],
                                            user=user, pipeline_name='cognify_pipeline', llm_config=config,
                                            skip_connection_test=True, incremental_loading=False, data_cache=False, rollback_handler=cognify_rollback_handler,
                                            after_items_ran=reconcile_document_structure, after_run_completed=compact_vector_store)
        raise_if_cognify_errored(result)
        records = await get_dataset_data(dataset_id, limit=4097, order_by='created_at')
        record = next((r for r in records if r.id == identity), None)
        completed_runs = require_completed_material_run(result, dataset_id, identity)
        if record is None or (record.external_metadata or {}).get('memlane_material_v1') != metadata['memlane_material_v1'] or record.mime_type != 'text/plain':
            raise ValueError('official_material_lineage_mismatch')
        async with open_data_file(record.raw_data_location, mode='r', encoding='utf-8') as file:
            stored = file.read(256 * 1024 + 1)
        if stored != material['text']:
            raise ValueError('official_material_body_mismatch')
        await extractor.publish_completed(completed_runs)
    except BaseException:
        try:
            extractor.mark_failed('material_pipeline_failed')
        except Exception:
            # Preserve the primary failure. Unpublished audit remains unknown;
            # a cleanup failure never turns the material task into success.
            pass
        raise

    return dict({k: material[k] for k in ('library', 'operation', 'instance', 'dataset', 'project', 'memory', 'revision', 'digest')},
                document=str(identity), state='processed', cognee_version='1.6.7',processing_revision_digest=material['processing']['revision']['config_digest'],processing_dependency_digest=material['processing']['dependency_digest'])


def install(app, instance, authenticated_user):
    from fastapi import Depends, HTTPException, Request
    # Annotations must use the module globals for FastAPI forward resolution.
    globals()['Request'] = Request

    active = {}
    cancelled = set()

    @app.post('/api/v1/memlane/material-submission/cancel')
    async def cancel_submission(request: Request, user=Depends(authenticated_user)):
        raw = bytearray()
        async for chunk in request.stream():
            raw.extend(chunk)
            if len(raw) > 2048:
                raise HTTPException(413, 'material_cancel_unbounded')
        try:
            import json
            payload = json.loads(raw)
            if payload['instance'] != instance:
                raise ValueError()
            library, operation = (str(UUID(payload[k])) for k in ('library', 'operation'))
        except Exception:
            raise HTTPException(422, 'invalid_material_cancellation') from None
        key = (str(user.id), library, operation)
        # Bounded cancellation tombstones also cover a POST whose body was
        # still arriving when cancellation won; never resume an old operation.
        if len(cancelled) >= 1024:
            raise HTTPException(429, 'material_cancel_budget')
        cancelled.add(key)
        pending = list(active.get(key, set()))
        for task in pending:
            task.cancel()
        if pending:
            await asyncio.gather(*pending, return_exceptions=True)
        return {'instance': instance, 'library': library, 'operation': operation, 'state': 'stopped'}

    @app.post('/api/v1/memlane/material-submission')
    async def submit(request: Request, user=Depends(authenticated_user)):
        raw = bytearray()
        async for chunk in request.stream():
            raw.extend(chunk)
            if len(raw) > 512 * 1024:
                raise HTTPException(413, 'material_request_unbounded')
        try:
            import json
            material, gateway = validate(json.loads(raw), instance)
        except Exception:
            raise HTTPException(422, 'invalid_material_submission') from None
        key = (str(user.id), material['library'], material['operation'])
        if key in cancelled:
            raise HTTPException(409, 'material_operation_cancelled')
        if active.get(key):
            raise HTTPException(409, 'material_operation_busy')
        task = asyncio.create_task(submit_official(material, gateway, user))
        active.setdefault(key, set()).add(task)
        async def disconnected():
            while not task.done():
                if await request.is_disconnected():
                    task.cancel()
                    return
                await asyncio.sleep(0.1)
        watch = asyncio.create_task(disconnected())
        try:
            return await asyncio.wait_for(task, timeout=300)
        except asyncio.CancelledError:
            task.cancel()
            await asyncio.gather(task, return_exceptions=True)
            raise
        except Exception:
            # No provider exception/body/token is exposed to UI or logs here.
            raise HTTPException(503, 'material_processing_failed') from None
        finally:
            watch.cancel()
            if not task.done(): task.cancel()
            await asyncio.gather(watch, task, return_exceptions=True)
            active.get(key, set()).discard(task)
            if not active.get(key): active.pop(key, None)
