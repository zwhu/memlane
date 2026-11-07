#!/usr/bin/env python3
"""Run the real Cognee-RS ONNX provider with isolated synthetic inputs."""
import hashlib
import argparse
import json
import math
from pathlib import Path
import resource
import shutil
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parents[1]
MANIFEST = ROOT / "deps/models/bge-small-zh-v1.5-int8.json"
LIBRARY = ROOT / ".local/runtimes/onnxruntime-osx-arm64-1.28.0/lib/libonnxruntime.1.28.0.dylib"


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--manifest", type=Path, default=MANIFEST)
    args = parser.parse_args()
    manifest = json.loads(args.manifest.read_text())
    model_dir = ROOT / ".local/models" / manifest["id"]
    for entry in manifest["files"]:
        data = (model_dir / entry["name"]).read_bytes()
        assert len(data) == entry["bytes"]
        assert hashlib.sha256(data).hexdigest() == entry["sha256"]
    payload = {"passages": ["项目部署失败时回滚到最近一次成功版本。",
                            "Use exponential backoff when retrying failed HTTP requests.",
                            "getUserInfo caches the supplierId in sessionStorage."],
               "queries": ["发布出错怎么恢复？", "HTTP retry strategy", "getUserInfo supplierId"]}
    with tempfile.TemporaryDirectory(prefix="memlane-embedding-p0-") as name:
        temporary = Path(name)
        executable = temporary / "embedding-probe"
        shutil.copy2(ROOT / ".cache/cognee-target/release/memlane-embedding-probe", executable)
        shutil.copy2(LIBRARY, temporary / LIBRARY.name)
        shutil.copytree(model_dir, temporary / "model")
        result = subprocess.run([str(executable), str(temporary / "model")],
                                input=json.dumps(payload), text=True, capture_output=True,
                                cwd=temporary, timeout=120,
                                env={"PATH": "/usr/bin:/bin", "RUST_LOG": "error",
                                     "TELEMETRY_DISABLED": "true",
                                     "ORT_DYLIB_PATH": str(temporary / LIBRARY.name)})
        assert result.returncode == 0, result.stderr
        report = json.loads(result.stdout)
        vectors = report.pop("passage_vectors")
        queries = report.pop("query_vectors")
        assert len(vectors) == len(payload["passages"])
        assert len(queries) == len(payload["queries"])
        for vector in vectors + queries:
            assert len(vector) == manifest["dimension"]
            assert all(math.isfinite(x) for x in vector)
            assert abs(sum(x*x for x in vector) - 1) < 0.0001
        scores = [[sum(x*y for x, y in zip(q, v)) for v in vectors] for q in queries]
        ranks = [sorted(range(len(vectors)), key=lambda i: row[i], reverse=True) for row in scores]
        passed = [r[0] for r in ranks] == [0, 1, 2]
        report.update({"status": "passed" if passed else "failed", "synthetic_passages": payload["passages"],
                       "queries": payload["queries"], "scores": scores, "ranks": ranks,
                       "finite_normalized_vectors": True, "relocated": True,
                       "peak_child_rss_bytes": resource.getrusage(resource.RUSAGE_CHILDREN).ru_maxrss,
                       "model_revision": manifest["revision"],
                       "model_file_bytes": sum(e["bytes"] for e in manifest["files"]),
                       "scope": "embedding smoke only; durable index and project isolation not verified"})
    output = ROOT / "build/p0-engine" / (manifest["id"] + "-embedding-verification.json")
    output.parent.mkdir(parents=True, exist_ok=True)
    output.write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n")
    print(json.dumps(report, ensure_ascii=False, indent=2))
    if not passed:
        raise SystemExit("Top-1 retrieval smoke failed; report saved")


if __name__ == "__main__":
    main()
