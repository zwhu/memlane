"""Bounded read-only HTTP projection of the pinned official SessionManager.

No copied content store, cache mutation, inference or changes to upstream files.
"""
import json
from datetime import datetime, timezone
from types import SimpleNamespace

from fastapi import Depends, HTTPException, Query
from fastapi.encoders import jsonable_encoder


def install_session_history(app, *, bindings=None):
    if bindings is None:
        from cognee.modules.users.methods import get_authenticated_user, get_visible_user_ids
        from cognee.modules.users.permissions.methods import get_permitted_dataset_ids
        from cognee.modules.session_lifecycle.metrics import get_session_row
        from cognee.infrastructure.session.get_session_manager import get_session_manager
        bindings = SimpleNamespace(get_authenticated_user=get_authenticated_user,
            get_visible_user_ids=get_visible_user_ids,
            get_permitted_dataset_ids=get_permitted_dataset_ids,
            get_session_row=get_session_row, get_session_manager=get_session_manager)

    @app.get('/api/v1/sessions/{session_id}/capture-status')
    async def capture_status(session_id: str, user=Depends(bindings.get_authenticated_user)):
        permitted = await bindings.get_permitted_dataset_ids(user.id)
        visible = await bindings.get_visible_user_ids(user.id)
        row = await bindings.get_session_row(session_id=session_id, user_id=user.id,
            user_ids=visible, permitted_dataset_ids=permitted)
        if row is None:
            raise HTTPException(404, 'session not found')
        owner = str(getattr(row, 'user_id', '') or '')
        sm = bindings.get_session_manager()
        if not sm.is_available or not owner:
            raise HTTPException(503, 'capture status unavailable')
        try:
            qas = await sm.get_session(user_id=owner, session_id=session_id, formatted=False)
            traces = await sm.get_agent_trace_session(user_id=owner, session_id=session_id)
        except Exception:
            raise HTTPException(503, 'capture status unavailable') from None
        if not sm.is_available or not isinstance(qas, list) or not isinstance(traces, list):
            raise HTTPException(503, 'capture status unavailable')
        def latest(entries, identifier):
            times = []
            for entry in entries:
                def field(name):
                    return entry.get(name) if isinstance(entry, dict) else getattr(entry, name, None)
                if not field(identifier):
                    continue
                try:
                    value = datetime.fromisoformat(str(field('time') or '').replace('Z', '+00:00'))
                    value = value.replace(tzinfo=timezone.utc) if value.tzinfo is None else value
                    instant = value.timestamp()
                    if 0 < instant <= datetime.now(timezone.utc).timestamp() + 60:
                        times.append(instant)
                except (ValueError, TypeError, OverflowError):
                    continue
            return max(times) if times else None
        metadata = row.to_dict()
        return {'session_id': session_id, 'user_id': owner, 'dataset_id': metadata.get('dataset_id'),
                'qa_count': len(qas), 'trace_count': len(traces),
                'last_qa_at': latest(qas, 'qa_id'), 'last_trace_at': latest(traces, 'trace_id')}

    @app.get('/api/v1/sessions/{session_id}/history')
    async def history(session_id: str, qa_offset: int = Query(0, ge=0),
                      trace_offset: int = Query(0, ge=0), limit: int = Query(20, ge=1, le=20),
                      user=Depends(bindings.get_authenticated_user)):
        permitted = await bindings.get_permitted_dataset_ids(user.id)
        visible = await bindings.get_visible_user_ids(user.id)
        row = await bindings.get_session_row(session_id=session_id, user_id=user.id,
            user_ids=visible, permitted_dataset_ids=permitted)
        if row is None:
            raise HTTPException(404, 'session not found')
        owner = str(getattr(row, 'user_id', '') or '')
        sm = bindings.get_session_manager()
        if not sm.is_available or not owner:
            raise HTTPException(503, 'session history unavailable')
        try:
            qas = await sm.get_session(user_id=owner, session_id=session_id, formatted=False)
            traces = await sm.get_agent_trace_session(user_id=owner, session_id=session_id)
        except Exception:
            raise HTTPException(503, 'session history unavailable') from None
        if not sm.is_available or not isinstance(qas, list) or not isinstance(traces, list):
            raise HTTPException(503, 'session history unavailable')
        if qa_offset > len(qas) or trace_offset > len(traces):
            raise HTTPException(409, 'session history changed')
        def question(entry):
            return entry.get('question') if isinstance(entry, dict) else getattr(entry, 'question', None)
        record = row.to_dict()
        record.update(label=next((str(question(q))[:120] for q in qas if question(q)), None),
            msg_count=len(qas), tool_calls=len(traces), history_paging=True,
            qa_offset=qa_offset, trace_offset=trace_offset, page_limit=limit,
            qas=qas[qa_offset:qa_offset+limit], traces=traces[trace_offset:trace_offset+limit],
            qa_more=qa_offset+limit < len(qas), trace_more=trace_offset+limit < len(traces))
        result = jsonable_encoder(record)
        # Do not silently truncate a source record or mark an unread page complete.
        if len(json.dumps(result, ensure_ascii=False).encode()) > 8 * 1024 * 1024:
            raise HTTPException(413, 'session history page exceeds reading limit')
        return result
