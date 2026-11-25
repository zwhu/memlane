"""Run the pinned trace graph pipeline through its public session interfaces."""
from uuid import UUID

from . import PolicyError


async def official_authorize(user, dataset_id):
    from cognee.modules.data.methods import get_authorized_existing_datasets
    from cognee.modules.users.exceptions import PermissionDeniedError
    try:
        authorized = await get_authorized_existing_datasets(user=user, datasets=[UUID(str(dataset_id))],
                                                            permission_type='write')
    except PermissionDeniedError:
        raise PolicyError('tool_graph_write_required', 403) from None
    if not authorized:
        raise PolicyError('tool_graph_write_required', 403)


async def read_trace_watermarks(user, session_ids):
    from cognee.infrastructure.session.get_session_manager import get_session_manager
    from cognee.infrastructure.session.session_persist_watermark import TRACE_PERSIST_WATERMARK
    manager, uid = get_session_manager(), str(user.id)
    if not manager.is_available:
        raise PolicyError('tool_graph_source_unavailable', 503)
    return {session: (await manager.get_agent_trace_count(user_id=uid, session_id=session),
                      await TRACE_PERSIST_WATERMARK.read_count(manager, uid, session))
            for session in dict.fromkeys(session_ids)}


async def run_tool_graph_pipeline(user, dataset_id, session_ids, *, limits=None, watermark_resets=None):
    from cognee.infrastructure.session.get_session_manager import get_session_manager
    from cognee.infrastructure.session.session_persist_watermark import TRACE_PERSIST_WATERMARK
    from cognee.memify_pipelines.persist_agent_trace_feedbacks_in_knowledge_graph import (
        persist_agent_trace_feedbacks_in_knowledge_graph_pipeline,
    )
    from cognee.modules.pipelines.models import PipelineRunErrored

    await official_authorize(user, dataset_id)
    watermarks = await read_trace_watermarks(user, session_ids)
    upper, resets, pending = {}, {}, 0
    for session, (total, stored) in watermarks.items():
        limit = limits.get(session, total) if limits is not None else total
        if type(limit) is not int or not 0 <= limit <= total:
            raise PolicyError('tool_graph_source_changed', 409)
        effective = 0 if stored > total else stored
        if watermark_resets is not None and session in watermark_resets:
            effective = watermark_resets[session]
            if type(effective) is not int or not 0 <= effective <= min(stored, limit):
                raise PolicyError('tool_graph_source_changed', 409)
            resets[session] = effective
        if limit > effective:
            upper[session] = limit
            pending += limit - effective
    manager, uid = get_session_manager(), str(user.id)
    for session, watermark in resets.items():
        await TRACE_PERSIST_WATERMARK.write_count(manager, uid, session, watermark)
    if not upper:
        return {'new_steps': 0, 'pipeline_run_ids': []}
    result = await persist_agent_trace_feedbacks_in_knowledge_graph_pipeline(
        user=user, dataset=UUID(str(dataset_id)), session_ids=list(upper), persist_up_to=upper,
        run_in_background=False)
    runs = list(result.values()) if isinstance(result, dict) else [result]
    if any(isinstance(run, PipelineRunErrored) for run in runs):
        raise PolicyError('official_tool_graph_failed', 503)
    return {'new_steps': pending, 'pipeline_run_ids': [
        str(run.pipeline_run_id) for run in runs if getattr(run, 'pipeline_run_id', None)]}
