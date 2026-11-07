#!/usr/bin/env python3
"""Cross-process raw-text retrieval against real Cognee-RS ONNX and LanceDB."""
import json
import argparse
from pathlib import Path
import resource
import shutil
import subprocess
import tempfile
import time
import uuid

ROOT = Path(__file__).resolve().parents[1]
MODEL = ROOT / ".local/models/multilingual-e5-base-int8"
LIBRARY = ROOT / ".local/runtimes/onnxruntime-osx-arm64-1.28.0/lib/libonnxruntime.1.28.0.dylib"
DATASET = str(uuid.uuid5(uuid.NAMESPACE_URL, "memlane-p0-alpha"))
CORPUS = [
    ("deploy", "项目部署失败时回滚到最近一次成功版本。"),
    ("http", "Use exponential backoff with jitter when retrying failed HTTP requests. Stop after five attempts."),
    ("identity", "getUserInfo caches the supplierId in sessionStorage. Refresh identity after logout."),
    ("style", "个人偏好：代码缩进使用两个空格，不使用 Tab。"),
    ("queue", "写入任务必须先持久化，再返回 accepted；恢复后按 operation_id 去重补发。"),
    ("ttl", "Cached pricing expires after 120 seconds. Use priceCacheTTL to set the expiration window."),
    ("route", "Supplier price changes use /price-change/edit routes, owned by PriceChangeEditor."),
    ("database", "SQLite backups must include the WAL or use the online backup API to preserve committed data."),
    ("long-cn", "天地玄黄宇宙洪荒日月盈昃辰宿列张" * 160 + "冬季机房温度过低时启用机柜加热设备，函数 enableRackHeating 负责加热控制。"),
]
QUERIES = [
    ("发布出错怎么恢复？", "deploy"),
    ("HTTP retry strategy", "http"),
    ("getUserInfo supplierId", "identity"),
    ("indentation preference", "style"),
    ("故障恢复后避免重复提交写入任务", "queue"),
    ("priceCacheTTL expiration", "ttl"),
    ("PriceChangeEditor /price-change/edit", "route"),
    ("SQLite consistent backup WAL", "database"),
    ("enableRackHeating 冬季机房加热", "long-cn"),
]


class Probe:
    def __init__(self, directory, model=MODEL):
        self.root = Path(directory)
        self.binary = self.root / "index-probe"
        shutil.copy2(ROOT / ".cache/cognee-target/release/memlane-index-probe", self.binary)
        shutil.copy2(LIBRARY, self.root / LIBRARY.name)
        shutil.copytree(model, self.root / "model")
        self.env = {"PATH": "/usr/bin:/bin", "RUST_LOG": "error", "TELEMETRY_DISABLED": "true",
                    "ORT_DYLIB_PATH": str(self.root / LIBRARY.name)}
        self.commands = []

    def call(self, request):
        started = time.monotonic()
        p = subprocess.run([str(self.binary), str(self.root / "data"), str(self.root / "model")],
                           input=json.dumps(request), text=True, capture_output=True,
                           cwd=self.root, env=self.env, timeout=120)
        entry = {"op": request["op"], "exit_code": p.returncode,
                 "wall_ms": (time.monotonic()-started)*1000,
                 "stderr": p.stderr.replace(str(self.root), "<temporary>")}
        self.commands.append(entry)
        if p.returncode:
            raise RuntimeError(entry)
        result = json.loads(p.stdout)
        entry["model_load_ms"] = result["model_load_ms"]
        return result["result"]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--model", type=Path, default=MODEL)
    args = parser.parse_args()
    report = {"status": "failed", "engine": "Cognee-RS ONNX + LanceDB", "llm_used": False,
              "model": args.model.name, "queries": []}
    error = None
    try:
        with tempfile.TemporaryDirectory(prefix="memlane-index-p0-") as name:
            probe = Probe(name, args.model)
            original = probe.root / "saved-originals"
            original.mkdir()
            records = []
            by_memory = {}
            for key, text in CORPUS:
                memory = str(uuid.uuid5(uuid.NAMESPACE_URL, "memlane-p0-"+key))
                (original / (memory+".txt")).write_text(text)
                records.append({"dataset": DATASET, "memory": memory, "revision": 1, "text": text})
                by_memory[memory] = (key, text)
            receipt = probe.call({"op": "index", "records": records})
            report["commands"] = probe.commands
            report["index_receipts"] = receipt["receipts"]
            report["index_total_ms"] = sum(r["index_ms"] for r in receipt["receipts"])
            assert receipt["receipts"][-1]["chunks"] > 1, "long Chinese text was not split"
            top1 = top3 = 0
            failures = []
            for query, expected in QUERIES:
                results = probe.call({"op": "search", "dataset": DATASET, "query": query, "top_k": 3})["candidates"]
                for hit in results:
                    key, text = by_memory[hit["memory"]]
                    assert hit["dataset"] == DATASET and hit["revision"] == 1
                    assert text.encode()[hit["start_byte"]:hit["end_byte"]].decode() == hit["text"], "chunk provenance mismatch"
                keys = [by_memory[h["memory"]][0] for h in results]
                top1 += bool(keys and keys[0] == expected)
                top3 += expected in keys
                report["queries"].append({"query": query, "expected": expected, "results": keys,
                                           "scores": [h["score"] for h in results]})
                if expected not in keys:
                    failures.append((query, expected, keys))
                if expected in ("identity", "ttl", "route", "long-cn"):
                    if not keys or keys[0] != expected:
                        failures.append(("identifier Top-1", query, keys))
            report.update({"status": "failed" if failures else "passed", "records": len(records), "query_count": len(QUERIES),
                           "top1_hits": top1, "top3_hits": top3, "cross_process_persistence": True,
                           "source_byte_ranges_verified": True, "long_chinese_tail_retrieved": True,
                           "scope": "raw-text retrieval; version validity requires business-store checks"})
            if failures:
                raise RuntimeError(failures)
    except Exception as exc:
        error = str(exc)
        report["error"] = error
    report["peak_child_rss_bytes"] = resource.getrusage(resource.RUSAGE_CHILDREN).ru_maxrss
    out = ROOT / "build/p0-engine/raw-index-verification.json"
    out.parent.mkdir(parents=True, exist_ok=True)
    out.write_text(json.dumps(report, ensure_ascii=False, indent=2)+"\n")
    print(json.dumps(report, ensure_ascii=False, indent=2))
    if error:
        raise SystemExit(1)


if __name__ == "__main__":
    main()
