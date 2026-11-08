"""Memlane 的只读 Cognee 1.6.7 材料图接口。

官方 graph DTO 丢弃边属性，未返回 edge→chunk 依据。本扩展复用官方
授权、dataset 图和 active edge evidence SDK，不抽取、不写图、不运行模型。
它必须加载在同一个 Cognee 服务进程中，不能另开数据库拥有者。
"""
import importlib.metadata
import json
import os
from pathlib import Path
from uuid import UUID, uuid4


def install(app):
    if importlib.metadata.version("cognee") != "1.6.7":
        raise RuntimeError("Memlane 材料图仅适配 Cognee 1.6.7")
    from fastapi import Depends, HTTPException
    from cognee.modules.users.methods import get_authenticated_user
    from cognee.modules.data.methods import get_authorized_dataset
    from cognee.context_global_variables import set_database_global_context_variables
    from cognee.infrastructure.databases.graph import get_graph_engine
    from cognee.modules.provenance.edge_evidence.lookup import get_edge_evidence_records

    # Stable service identity belongs to this data root, not a port or a model.
    root = Path(os.environ["SYSTEM_ROOT_DIRECTORY"])
    root.mkdir(parents=True, exist_ok=True)
    path = root / "memlane-service-identity.json"
    try:
        fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    except FileExistsError:
        instance = str(UUID(json.loads(path.read_text())["instance"]))
    else:
        instance = str(uuid4())
        with os.fdopen(fd, "w") as file:
            json.dump({"instance": instance, "adapter": "memlane-material-graph-v1"}, file)
            file.flush()
            os.fsync(file.fileno())

    @app.get("/api/v1/memlane/identity")
    async def identity(user=Depends(get_authenticated_user)):
        return {"instance": instance, "cognee_version": "1.6.7", "adapter": "memlane-material-graph-v1"}

    @app.post("/api/v1/memlane/verify-environment")
    async def verify_environment(user=Depends(get_authenticated_user)):
        # Explicitly approved readiness probe, without adding knowledge or
        # starting a session. The same embedding/LLM gateways serve cognify.
        import asyncio
        from pydantic import BaseModel
        from cognee.infrastructure.llm.LLMGateway import LLMGateway
        from cognee.infrastructure.databases.vector.embeddings.get_embedding_engine import get_embedding_engine

        class ReadyProbe(BaseModel):
            ready: bool

        try:
            vectors = await asyncio.wait_for(get_embedding_engine().embed_text(["Memlane environment verification"]), timeout=60)
            response = await asyncio.wait_for(LLMGateway.acreate_structured_output(
                text_input="Return a JSON object with ready set to true.",
                system_prompt="This is an environment readiness probe. Return only the requested object.",
                response_model=ReadyProbe), timeout=60)
            if not vectors or not vectors[0] or not response.ready:
                raise ValueError("Readiness probe did not pass")
        except Exception as error:
            # Provider exceptions can contain credentials/headers. Only the
            # error type leaves this private service, never repr(error).
            raise HTTPException(503, "Model verification failed: " + type(error).__name__) from None
        return {"instance": instance, "state": "verified", "embedding_dimensions": len(vectors[0]),
                "model": os.environ.get("LLM_MODEL", ""), "memory_written": False}

    @app.get("/api/v1/memlane/datasets/{dataset_id}/sessions/{session_id}")
    async def complete_session(dataset_id: UUID, session_id: str, expected_instance: UUID, user=Depends(get_authenticated_user)):
        if str(expected_instance) != instance:
            raise HTTPException(409, "Cognee service identity changed")
        if not session_id or len(session_id) > 128 or any(char.isspace() for char in session_id):
            raise HTTPException(422, "Bounded session identity required")
        dataset = await get_authorized_dataset(user, dataset_id)
        if not dataset:
            raise HTTPException(404, "Dataset not found or not authorized")
        from fastapi.encoders import jsonable_encoder
        from cognee.modules.users.methods import get_visible_user_ids
        from cognee.modules.users.permissions.methods import get_permitted_dataset_ids
        from cognee.modules.session_lifecycle.metrics import get_session_row
        from cognee.infrastructure.session.get_session_manager import get_session_manager
        from cognee.infrastructure.session.session_persist_watermark import get_persisted_qa_count, SESSION_PERSIST_WATERMARK, TRACE_PERSIST_WATERMARK
        from cognee.modules.data.methods import get_dataset_data
        from cognee.modules.pipelines.models.DataItemStatus import is_data_item_completed
        from cognee.infrastructure.files.utils.open_data_file import open_data_file

        row = await get_session_row(session_id=session_id, user_id=user.id,
                                    user_ids=await get_visible_user_ids(user.id),
                                    permitted_dataset_ids=await get_permitted_dataset_ids(user.id))
        if row is None or str(row.dataset_id) != str(dataset_id):
            raise HTTPException(404, "Session is outside the authorized dataset")
        sm = get_session_manager()
        if not sm.is_available:
            raise HTTPException(503, "Official session material is unavailable")
        owner = str(row.user_id)
        qas = await sm.get_session(user_id=owner, session_id=session_id, formatted=False)
        traces = await sm.get_agent_trace_session(user_id=owner, session_id=session_id)
        qas = qas if isinstance(qas, list) else []
        if len(qas) + len(traces) > 256:
            raise HTTPException(413, "Session exceeds the current complete-material budget")
        persisted_qa = await get_persisted_qa_count(sm, owner, session_id)
        persisted_trace = await TRACE_PERSIST_WATERMARK.read_count(sm, owner, session_id)
        persisted_qa = SESSION_PERSIST_WATERMARK.resolve_effective(persisted_qa, len(qas), session_id=session_id)
        persisted_trace = TRACE_PERSIST_WATERMARK.resolve_effective(persisted_trace, len(traces), session_id=session_id)
        raw = []
        for index, qa in enumerate(qas):
            if not qa.qa_id:
                raise HTTPException(409, "Official QA has no stable identity")
            text = "Question: " + qa.question + "\n\nAnswer: " + qa.answer + "\n\nOfficial QA metadata:\n" + json.dumps(
                qa.model_dump(mode="json", exclude={"question", "answer"}), ensure_ascii=False, indent=2)
            raw.append({"id": qa.qa_id, "kind": "qa", "text": text, "sequence": index,
                        "captured_at": qa.time, "persisted": index < persisted_qa})
        for index, trace in enumerate(traces):
            # SDK order is authoritative; the official trace type has no
            # timestamp, so we retain sequence instead of inventing a clock.
            raw.append({"id": trace.trace_id, "kind": "trace",
                        "text": json.dumps(jsonable_encoder(trace), ensure_ascii=False, indent=2),
                        "sequence": index, "captured_at": "", "persisted": index < persisted_trace})
        if any(len(record["text"].encode()) > 256 * 1024 for record in raw):
            raise HTTPException(413, "Original record exceeds the supported read budget")
        records = await get_dataset_data(dataset_id, order_by="created_at")
        if len(records) > 4096:
            raise HTTPException(413, "Dataset document budget exceeded")
        documents = []
        header = "# Session learning (session " + session_id + ")\n\n"
        for record in records:
            if record.mime_type != "text/plain":
                continue
            # The fixed official rendering template carries the session id;
            # a filename or model-invented label is never used to guess it.
            async with open_data_file(record.raw_data_location, mode="r", encoding="utf-8") as file:
                prefix = file.read(len(header))
                if prefix != header:
                    continue
                text = prefix + file.read(16 * 1024 + 1)
            if len(text.encode()) > 16 * 1024:
                raise HTTPException(413, "Official learning exceeds the current knowledge budget")
            status = ((record.pipeline_status or {}).get("cognify_pipeline") or {}).get(str(dataset_id))
            documents.append({"id": str(record.id), "text": text, "created_at": str(record.created_at),
                              "processed": is_data_item_completed(status)})
        return {"instance": instance, "dataset": str(dataset_id), "session": session_id,
                "cognee_version": "1.6.7", "raw": raw, "documents": documents,
                "persisted_qa": persisted_qa, "persisted_trace": persisted_trace,
                "state": "persisted" if raw and all(record["persisted"] for record in raw) else "awaiting_official_sync"}

    @app.get("/api/v1/memlane/datasets/{dataset_id}/graph")
    async def material_graph(dataset_id: UUID, expected_instance: UUID, user=Depends(get_authenticated_user)):
        if str(expected_instance) != instance:
            raise HTTPException(409, "Cognee service identity changed")
        dataset = await get_authorized_dataset(user, dataset_id)
        if not dataset:
            raise HTTPException(404, "Dataset not found or not authorized")
        async with set_database_global_context_variables(dataset_id, dataset.owner_id):
            graph = await get_graph_engine()
            nodes, edges = await graph.get_graph_data()
            if len(nodes) > 4000 or len(edges) > 16000:
                raise HTTPException(413, "Material graph exceeds this adapter's bounded scope")
            by_id = {str(node[0]): node[1] for node in nodes}
            chunks = {key: value for key, value in by_id.items() if value.get("type") == "DocumentChunk"}
            entity_ids = {key for key, value in by_id.items() if value.get("type") == "Entity"}
            relations = []
            missing_identity = 0
            for edge in edges:
                source, target, label = str(edge[0]), str(edge[1]), str(edge[2])
                info = edge[3] if len(edge) > 3 and isinstance(edge[3], dict) else {}
                if source in entity_ids and target in entity_ids:
                    edge_id = info.get("edge_object_id")
                    if edge_id:
                        relations.append((source, target, label, str(UUID(str(edge_id)))))
                    else:
                        missing_identity += 1
            # This SDK excludes uncompleted/failed pipeline observations.
            evidence = await get_edge_evidence_records(
                [UUID(edge[3]) for edge in relations], dataset_id,
                per_edge_limit=16, total_limit=16000,
            )
            evidence_by_edge = {}
            for item in evidence:
                chunk = chunks.get(str(item.chunk_id))
                if not chunk or not chunk.get("text"):
                    continue
                evidence_by_edge.setdefault(str(item.edge_id), []).append({
                    "document": str(item.data_id), "chunk": str(item.chunk_id),
                    "chunk_index": item.chunk_index, "quote": chunk["text"],
                })
            mentions = []
            for edge in edges:
                source, target, label = str(edge[0]), str(edge[1]), str(edge[2])
                if label != "contains" or source not in chunks or target not in entity_ids:
                    continue
                chunk = chunks[source]
                document = chunk.get("document_id")
                if not document:
                    document = next((str(e[1]) for e in edges if str(e[0]) == source and str(e[2]) == "is_part_of"), None)
                if document and chunk.get("text"):
                    mentions.append({"entity": target, "document": str(document), "chunk": source,
                                     "chunk_index": chunk.get("chunk_index"), "quote": chunk["text"]})
            supported = [{"id": edge[3], "source": edge[0], "target": edge[1], "label": edge[2],
                          "sources": evidence_by_edge[edge[3]]}
                         for edge in relations if edge[3] in evidence_by_edge]
            return {"instance": instance, "dataset": str(dataset_id), "cognee_version": "1.6.7",
                    "entities": [{"id": key, "name": by_id[key].get("name", ""),
                                  "description": by_id[key].get("description", "")}
                                 for key in sorted(entity_ids)],
                    "mentions": mentions, "relations": supported,
                    "unsupported_relations": missing_identity+len(relations)-len(supported)}
