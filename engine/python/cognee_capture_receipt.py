"""Read-only receipt lookup in the authenticated user's own official cache.

No transcript mirror, ingestion, retry, graph processing or model invocation.
An empty/expired cache is unknown, never proof that a write did not arrive.
"""
import json
import re
from uuid import UUID


def validate_identity(session, kind, operation):
    if not isinstance(session, str) or not re.fullmatch(r"pi-[0-9a-f]{48}", session):
        raise ValueError("bounded_capture_session_required")
    if kind not in ("qa", "trace") or not isinstance(operation, str) or not re.fullmatch(r"[0-9a-f]{64}", operation):
        raise ValueError("bounded_capture_operation_required")


def select_receipt(entries, kind, operation):
    if not isinstance(entries, list) or len(entries) > 256:
        raise ValueError("capture_receipt_budget")
    selected = None
    for item in entries:
        row = item if isinstance(item, dict) else item.model_dump(mode="json")
        context_key = "context" if kind == "qa" else "memory_context"
        text = row.get(context_key)
        if not isinstance(text, str) or len(text.encode()) > 16384:
            continue
        try:
            marker = json.loads(text)
        except (ValueError, TypeError):
            continue
        if not isinstance(marker, dict) or marker.get("origin") != "memlane_pi_capture_v1" or marker.get("operation") != operation:
            continue
        identity_key = "qa_id" if kind == "qa" else "trace_id"
        identity = row.get(identity_key)
        if not isinstance(identity, str) or str(UUID(identity)) != identity:
            raise ValueError("capture_receipt_identity")
        if selected is not None:
            raise ValueError("capture_receipt_ambiguous")
        keys = ("question", "answer", "context") if kind == "qa" else (
            "origin_function", "status", "method_params", "method_return_value", "error_message", "memory_context")
        entry = {key: row[key] for key in keys}
        entry["type"] = kind
        if kind == "trace":
            # This flag controls work at ingestion; it is not a persisted SDK
            # trace attribute. Lookup cannot initiate feedback/model work.
            entry["generate_feedback_with_llm"] = False
        if len(json.dumps(entry, ensure_ascii=False).encode()) > 256 * 1024:
            raise ValueError("capture_receipt_budget")
        selected = {"state": "saved", "entry_id": identity, "entry": entry}
    return selected or {"state": "unknown"}


def install(app, instance, authenticated_user):
    from fastapi import Depends, HTTPException

    @app.get("/api/v1/memlane/datasets/{dataset_id}/capture-receipts/{session_id}/{kind}/{operation}")
    async def receipt(dataset_id: UUID, session_id: str, kind: str, operation: str,
                      expected_instance: UUID, user=Depends(authenticated_user)):
        if str(expected_instance) != instance:
            raise HTTPException(409, "capture_receipt_instance_changed")
        try:
            validate_identity(session_id, kind, operation)
        except ValueError:
            raise HTTPException(422, "invalid_capture_receipt_query") from None
        from cognee.modules.data.methods import get_authorized_dataset
        from cognee.modules.session_lifecycle.metrics import get_session_row
        from cognee.infrastructure.session.get_session_manager import get_session_manager

        dataset = await get_authorized_dataset(user, dataset_id)
        if dataset is None:
            raise HTTPException(404, "capture_receipt_scope_unavailable")
        # Deliberately no visible-user expansion: capture uses this principal's
        # own session cache, never another visible owner's same-named session.
        row = await get_session_row(session_id=session_id, user_id=user.id)
        base = {"instance": instance, "dataset": str(dataset_id), "session": session_id,
                "user": str(user.id), "operation": operation, "kind": kind}
        if row is None:
            return {**base, "state": "unknown"}
        if row.user_id != user.id or row.dataset_id != dataset_id:
            raise HTTPException(404, "capture_receipt_scope_unavailable")
        manager = get_session_manager()
        if not manager.is_available:
            raise HTTPException(503, "capture_receipt_cache_unavailable")
        entries = (await manager.get_session(user_id=str(user.id), session_id=session_id, formatted=False, last_n=257)
                   if kind == "qa" else await manager.get_agent_trace_session(user_id=str(user.id), session_id=session_id, last_n=257))
        try:
            result = select_receipt(entries, kind, operation)
        except Exception:
            raise HTTPException(409, "capture_receipt_unverified") from None
        return {**base, **result}
