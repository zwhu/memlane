"""Python comparison worker: real Cognee index task and LanceDB, local FastEmbed.

Only protocol bytes go to stdout. It never invokes remember/cognify/improve or a
cloud embedding provider. Original/version truth stays in the owning Go service.
"""
import asyncio
from contextlib import suppress
import fcntl
import faulthandler
import hashlib
import json
import math
import os
from pathlib import Path
import sys
import tempfile
import threading
import time
import uuid

PROTOCOL_OUT = sys.stdout.buffer
sys.stdout = sys.stderr
PROTOCOL = 1
MAX_LINE = 1024 * 1024


def emit(value):
    PROTOCOL_OUT.write((json.dumps({"protocol": PROTOCOL, **value}, ensure_ascii=False)+"\n").encode())
    PROTOCOL_OUT.flush()


def failure(request_id, code, message):
    return {"id": request_id, "ok": False, "error": {"code": code, "message": message}}


def success(request_id, result):
    return {"id": request_id, "ok": True, "result": result}


class Backend:
    def __init__(self, root, model):
        # Imports are deliberately delayed until paths/cache/privacy flags exist.
        from fastembed import TextEmbedding
        from fastembed.common.model_description import PoolingType, ModelSource
        from tokenizers import Tokenizer
        from cognee.infrastructure.engine import DataPoint
        from cognee.infrastructure.databases.vector.embeddings.EmbeddingGateway import EmbeddingGateway
        from cognee.infrastructure.databases.vector.embeddings.EmbeddingEngine import EmbeddingResult
        from cognee.infrastructure.databases.vector.lancedb.LanceDBAdapter import LanceDBAdapter
        from cognee.tasks.storage.index_data_points import index_data_points

        self.root = root
        self.recipe = json.loads((model / "recipe.json").read_text())
        self.counter = Tokenizer.from_file(str(model / "tokenizer.json"))
        self.counter.no_padding()
        self.counter.no_truncation()
        alias = "Memlane/multilingual-e5-base-int8"
        TextEmbedding.add_custom_model(model=alias, pooling=PoolingType.MEAN, normalization=True,
                                      sources=ModelSource(hf=self.recipe["repository"]),
                                      dim=self.recipe["dimension"], model_file="model.onnx",
                                      license="mit", description="Pinned local Memlane P0 model")
        self.model = TextEmbedding(model_name=alias, specific_model_path=str(model),
                                   local_files_only=True, cache_dir=str(root / "cache/fastembed"),
                                   providers=["CPUExecutionProvider"], threads=4)
        tokenizer = self.model.model.tokenizer
        padding = tokenizer.padding
        tokenizer.enable_padding(length=self.recipe["max_sequence_length"],
                                 direction=padding["direction"], pad_id=padding["pad_id"],
                                 pad_type_id=padding["pad_type_id"], pad_token=padding["pad_token"])
        self.model_lock = threading.Lock()
        backend = self

        class LocalEmbedding:
            model = alias
            def __init__(self, prefix):
                self.prefix = prefix
                self.dimensions = backend.recipe["dimension"]
            def get_vector_size(self):
                return self.dimensions
            def get_batch_size(self):
                return 1
            async def input_limit(self):
                return backend.recipe["max_sequence_length"]-2
            async def embed_text(self, texts):
                prefixed = [self.prefix+text for text in texts]
                for text in prefixed:
                    if backend.count(text)+2 > backend.recipe["max_sequence_length"]:
                        raise ValueError("embedding input exceeds fixed token limit")
                def infer():
                    with backend.model_lock:
                        return [v.tolist() for v in backend.model.embed(prefixed, batch_size=1, parallel=None)]
                vectors = await asyncio.to_thread(infer)
                if any(len(v) != self.dimensions or not all(math.isfinite(x) for x in v) for v in vectors):
                    raise ValueError("invalid real embedding output")
                return EmbeddingResult(vectors, tokens=sum(backend.count(t) for t in prefixed))

        class MemlaneRawChunk(DataPoint):
            text: str
            external_metadata: str
            metadata: dict = {"index_fields": ["text"]}

        self.Chunk = MemlaneRawChunk
        self.Adapter = LanceDBAdapter
        self.index_data_points = index_data_points
        self.passage = EmbeddingGateway(LocalEmbedding(self.recipe.get("passage_instruction", "")))
        self.query = EmbeddingGateway(LocalEmbedding(self.recipe["query_instruction"]))
        self.adapters = {}
        recipe = {k: self.recipe[k] for k in ["id", "revision", "dimension", "max_sequence_length",
                                             "pooling", "query_instruction", "passage_instruction",
                                             "chunk_tokens", "overlap_tokens", "files"]}
        generation = "python-cognee-1.6.7-fastembed-0.9.1:"+json.dumps(recipe, sort_keys=True, separators=(",", ":"))
        self.generation = str(uuid.uuid5(uuid.NAMESPACE_OID, generation))

    def count(self, text):
        return len(self.counter.encode(text, add_special_tokens=False).ids)

    def spans(self, text):
        if not text.strip():
            raise ValueError("empty record")
        spans = []
        start = 0
        while start < len(text):
            low, high = start+1, len(text)
            while low < high:
                mid = (low+high+1)//2
                if self.count(text[start:mid]) <= self.recipe["chunk_tokens"]:
                    low = mid
                else:
                    high = mid-1
            end = low
            if self.count(text[start:end]) > self.recipe["chunk_tokens"]:
                raise ValueError("character exceeds token budget")
            spans.append((start, end))
            if end == len(text):
                break
            overlap = end
            while overlap > start+1 and self.count(text[overlap-1:end]) <= self.recipe["overlap_tokens"]:
                overlap -= 1
            start = max(overlap, start+1)
        return spans

    def adapter(self, dataset):
        dataset = str(uuid.UUID(dataset))
        if dataset not in self.adapters:
            directory = self.root / "datasets" / dataset / self.generation
            self.adapters[dataset] = self.Adapter(url=str(directory), api_key=None, embedding_engine=self.passage)
        return self.adapters[dataset]

    async def index(self, record):
        if set(record) != {"dataset", "memory", "revision", "text"}:
            raise ValueError("invalid record fields")
        dataset, memory = str(uuid.UUID(record["dataset"])), uuid.UUID(record["memory"])
        revision = record["revision"]
        if type(revision) is not int or revision < 1:
            raise ValueError("invalid revision")
        text = record["text"]
        spans = await asyncio.to_thread(self.spans, text)
        adapter = self.adapter(dataset)
        document = uuid.uuid5(memory, f"{dataset}:{revision}:{self.generation}")
        ids = []
        for number, (start, end) in enumerate(spans):
            chunk_id = uuid.uuid5(document, str(number))
            meta = {"dataset": dataset, "memory": str(memory), "revision": revision,
                    "generation": self.generation, "start_byte": len(text[:start].encode()),
                    "end_byte": len(text[:end].encode())}
            chunk = self.Chunk(id=chunk_id, text=text[start:end], external_metadata=json.dumps(meta),
                               belongs_to_set=[dataset])
            await self.index_data_points([chunk], vector_engine=adapter)
            ids.append(str(chunk_id))
        return {"chunk_ids": ids, "generation": self.generation, "chunks": len(ids)}

    async def search(self, request):
        dataset = str(uuid.UUID(request["dataset"]))
        query, k = request["query"], request["top_k"]
        if not isinstance(query, str) or not query.strip() or type(k) is not int or not 1 <= k <= 1000:
            raise ValueError("invalid query or top_k")
        adapter = self.adapter(dataset)
        collection = self.Chunk.vector_collection()
        if not await adapter.has_collection(collection):
            return {"candidates": []}
        vector = (await self.query.embed_text([query]))[0]
        results = await adapter.search(collection_name=collection, query_vector=vector, limit=k, include_payload=True)
        candidates = []
        for result in results:
            meta = json.loads(result.payload["external_metadata"])
            if meta["dataset"] != dataset:
                raise ValueError("candidate dataset mismatch")
            candidates.append({"chunk_id": str(result.id), **meta, "text": result.payload["text"],
                               "score": 1.0-result.score})
        return {"candidates": candidates}

    async def perform(self, request):
        if request["op"] == "index":
            return await self.index(request["record"])
        if request["op"] == "search":
            return await self.search(request)
        if request["op"] == "remove":
            adapter = self.adapter(request["dataset"])
            await adapter.delete_data_points(self.Chunk.vector_collection(), [uuid.UUID(i) for i in request["chunk_ids"]])
            return {"removed": len(request["chunk_ids"])}
        raise ValueError("unsupported operation")


async def serve(backend):
    loop = asyncio.get_running_loop()
    queue = asyncio.Queue(maxsize=16)
    def reader():
        pending = bytearray()
        while True:
            # Raw reads avoid a daemon thread retaining Python's stdin buffer
            # lock when the interpreter shuts down after a protocol shutdown.
            chunk = os.read(0, 65536)
            pending.extend(chunk)
            while b"\n" in pending or len(pending) > MAX_LINE or not chunk:
                boundary = pending.find(b"\n")
                length = boundary+1 if boundary >= 0 else len(pending)
                line = bytes(pending[:length])
                del pending[:length]
                try:
                    asyncio.run_coroutine_threadsafe(queue.put(line), loop).result()
                except Exception:
                    return
                if not line or len(line) > MAX_LINE:
                    return
                if not pending:
                    break
            if not chunk:
                return
    threading.Thread(target=reader, daemon=True).start()
    incoming = asyncio.create_task(queue.get())
    active = None
    active_id = None
    used = set()
    while True:
        waiting = [incoming]+([active] if active else [])
        completed, _ = await asyncio.wait(waiting, return_when=asyncio.FIRST_COMPLETED)
        if active and active in completed:
            emit(failure(active_id, "cancelled", "operation cancelled; no completed index receipt") if active.cancelled() else active.result())
            active = active_id = None
        if incoming not in completed:
            continue
        line = incoming.result()
        if not line:
            break
        incoming = asyncio.create_task(queue.get())
        if len(line) > MAX_LINE:
            emit(failure("", "message_too_large", "message exceeds 1 MiB"));break
        try:
            request = json.loads(line)
            allowed = {"protocol", "id", "op", "record", "dataset", "query", "top_k", "chunk_ids", "target", "timeout_ms"}
            if not isinstance(request, dict) or set(request)-allowed:
                raise ValueError("request fields")
            request_id = request["id"]
            if type(request["protocol"]) is not int:
                raise ValueError("protocol type")
            if not isinstance(request_id, str) or not 1 <= len(request_id) <= 128:
                raise ValueError("request ID")
        except Exception:
            emit(failure("", "bad_request", "invalid JSON or request fields"));continue
        if request.get("protocol") != PROTOCOL:
            emit(failure(request_id, "protocol_mismatch", "requires protocol 1"));continue
        if request_id in used or len(used) >= 10000:
            emit(failure(request_id, "duplicate_id", "unique IDs required; reconnect after 10000 requests"));continue
        used.add(request_id)
        op = request.get("op")
        if op in ("health", "handshake"):
            emit(success(request_id, {"worker_version": "0.0.1", "ready": True,
                                      "busy": active is not None, "generation": backend.generation}))
        elif op == "shutdown":
            if active:
                active.cancel()
            emit(success(request_id, {"stopping": True}));break
        elif op == "cancel":
            matched = active is not None and active_id == request.get("target")
            if matched:
                active.cancel()
            emit(success(request_id, {"cancel_requested": matched}))
        elif op in ("index", "search", "remove"):
            if active:
                emit(failure(request_id, "busy", "one operation at a time"));continue
            timeout = request.get("timeout_ms", 120000)
            if type(timeout) is not int or not 1 <= timeout <= 120000:
                emit(failure(request_id, "bad_request", "invalid timeout"));continue
            async def job(req=request):
                try:
                    result = await asyncio.wait_for(backend.perform(req), req.get("timeout_ms", 120000)/1000)
                    return success(req["id"], result)
                except asyncio.TimeoutError:
                    return failure(req["id"], "timeout", "operation exceeded timeout; no completed index receipt")
                except asyncio.CancelledError:
                    return failure(req["id"], "cancelled", "operation cancelled; no completed index receipt")
                except Exception as exc:
                    # Never stringify record-bearing validation exceptions.
                    return failure(req["id"], "engine_error", type(exc).__name__+": local operation failed")
            active_id = request_id
            active = asyncio.create_task(job())
        else:
            emit(failure(request_id, "unsupported_operation", "operation not supported by this worker"))
    incoming.cancel()
    if active:
        active.cancel()
        with suppress(asyncio.CancelledError):
            await active


def main():
    if len(sys.argv) != 3 or not all(Path(p).is_absolute() for p in sys.argv[1:]):
        raise SystemExit("expected absolute data and model directories")
    root, model = map(Path, sys.argv[1:])
    root.mkdir(parents=True, exist_ok=True, mode=0o700)
    os.chdir(root)
    env_fd, env_path = tempfile.mkstemp(prefix=".isolated-env-", dir=root)
    os.close(env_fd)
    os.environ.update({"COGNEE_ENV_FILE": env_path, "TELEMETRY_DISABLED": "true", "COGNEE_TELEMETRY_DISABLED": "true",
                       "PYTHON_DOTENV_DISABLED": "1", "COGNEE_TRACING_ENABLED": "false",
                       "LITELLM_LOCAL_MODEL_COST_MAP": "true",
                       "COGNEE_LOG_FILE": "false", "COGNEE_LOGS_DIR": str(root / "logs"),
                       "SYSTEM_ROOT_DIRECTORY": str(root / "system"), "DATA_ROOT_DIRECTORY": str(root / "raw"),
                       "CACHE_ROOT_DIRECTORY": str(root / "cache"), "LOGS_ROOT_DIRECTORY": str(root / "logs"),
                       "COGNEE_REPOS_DIR": str(root / "repos"), "DB_PATH": str(root / "system/databases"),
                       "DB_PROVIDER": "sqlite",
                       "COGNEE_SYSTEM_ROOT_DIRECTORY": str(root / "system"),
                       "COGNEE_DATA_ROOT_DIRECTORY": str(root / "raw"),
                       "FASTEMBED_CACHE_PATH": str(root / "cache/fastembed"), "HF_HOME": str(root / "cache/hf"),
                       "HF_HUB_CACHE": str(root / "cache/hf/hub"), "HF_HUB_OFFLINE": "1",
                       "EMBEDDING_PROVIDER": "fastembed", "EMBEDDING_DIMENSIONS": "768",
                       "EMBEDDING_MAX_CONCURRENT_DATA_POINTS": "1", "MOCK_EMBEDDING": "false"})
    emit({"event": "hello", "worker_version": "0.0.1", "ready": False,
          "engine": "cognee-python", "cognee_version": "1.6.7", "fastembed_version": "0.9.1"})
    with (root / ".worker.lock").open("a+b") as ownership:
        try:
            fcntl.flock(ownership, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except OSError:
            emit(failure("", "owner_conflict", "worker data already owned"));raise SystemExit(1)
        try:
            started = time.monotonic()
            faulthandler.dump_traceback_later(30, repeat=True, file=sys.stderr)
            backend = Backend(root, model)
            faulthandler.cancel_dump_traceback_later()
            Path(env_path).unlink()
        except Exception as exc:
            emit(failure("", "startup_failed", type(exc).__name__+": "+str(exc)[:2000]));raise SystemExit(1)
        emit({"event": "ready", "worker_version": "0.0.1", "ready": True,
              "generation": backend.generation, "model": backend.recipe["id"],
              "model_revision": backend.recipe["revision"], "model_load_ms": (time.monotonic()-started)*1000,
              "capabilities": ["raw_index", "raw_search", "remove", "cancel", "timeout"], "graph_ready": False})
        asyncio.run(serve(backend))


if __name__ == "__main__":
    main()
