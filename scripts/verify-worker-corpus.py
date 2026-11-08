#!/usr/bin/env python3
"""Run the unchanged raw retrieval corpus against a relocated JSONL worker."""
import argparse
import importlib.util
import hashlib
import json
from pathlib import Path
import resource
import shutil
import subprocess
import sys
import tarfile
import tempfile
import time
import uuid

from importlib.util import module_from_spec, spec_from_file_location

ROOT = Path(__file__).resolve().parents[1]
_link_spec = importlib.util.spec_from_file_location("archive_links", ROOT / "scripts/archive-links.py")
_link_module = importlib.util.module_from_spec(_link_spec)
_link_spec.loader.exec_module(_link_module)
restore_links = _link_module.restore_links


def load(name, path):
    spec = spec_from_file_location(name, path)
    module = module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--archive", type=Path)
    parser.add_argument("--python-source", action="store_true")
    parser.add_argument("--out", type=Path, help="explicit report path for a new component validation")
    args = parser.parse_args()
    if not args.archive and not args.python_source:parser.error("archive or python source required")
    corpus = load("raw_corpus", ROOT / "scripts/verify-index.py")
    protocol = load("worker_protocol", ROOT / "scripts/verify-worker.py")
    protocol.Session.deny_network=True
    protocol.Session.source_python=args.python_source
    report = {"os_network_denied":True,"status":"failed","queries":[],"synthetic_only":True,"llm_used":False,
              "archive":str(args.archive) if args.archive else None,"relocated_archive":not args.python_source}
    with tempfile.TemporaryDirectory(prefix="memlane-worker-corpus-") as name:
        root = Path(name)
        (root / "worker").mkdir()
        session = None
        try:
            if not args.python_source:
                with tarfile.open(args.archive) as source:
                    manifest = json.load(source.extractfile("component.json"))
                    for entry in manifest["files"]:
                        rel = Path(entry["path"])
                        assert not rel.is_absolute() and ".." not in rel.parts
                        member=source.getmember(entry["path"])
                        assert member.isfile()
                        path=root / "worker" / rel
                        path.parent.mkdir(parents=True,exist_ok=True)
                        with source.extractfile(member) as src, path.open("wb") as out:
                            shutil.copyfileobj(src,out)
                        assert path.stat().st_size==entry["bytes"]
                        assert hashlib.sha256(path.read_bytes()).hexdigest()==entry["sha256"]
                        path.chmod(0o755 if entry["path"]==manifest["executable"] or entry.get("executable") else 0o644)
            if not args.python_source:restore_links(root / "worker", manifest)
            shutil.copytree(corpus.MODEL,root / "model")
            session=protocol.Session(root)
            report["ready"]=session.startup
            by_memory={}
            receipts=[]
            started=time.monotonic()
            for key,text in corpus.CORPUS:
                memory=str(uuid.uuid5(uuid.NAMESPACE_URL,"memlane-p0-"+key))
                by_memory[memory]=(key,text)
                response=session.call("index",record={"dataset":corpus.DATASET,"memory":memory,"revision":1,"text":text})
                assert response["ok"],response
                receipts.append(response["result"])
            report["index_total_ms"]=(time.monotonic()-started)*1000
            assert receipts[-1]["chunks"]>1
            session.close()
            session=protocol.Session(root)
            failures=[];top1=top3=0
            for query,expected in corpus.QUERIES:
                response=session.call("search",dataset=corpus.DATASET,query=query,top_k=3)
                assert response["ok"],response
                results=response["result"]["candidates"]
                for hit in results:
                    key,text=by_memory[hit["memory"]]
                    assert hit["dataset"]==corpus.DATASET and hit["revision"]==1
                    assert text.encode()[hit["start_byte"]:hit["end_byte"]].decode()==hit["text"]
                keys=[by_memory[h["memory"]][0] for h in results]
                top1+=bool(keys and keys[0]==expected);top3+=expected in keys
                report["queries"].append({"query":query,"expected":expected,"results":keys,"scores":[h["score"] for h in results]})
                if expected not in keys or (expected in ("identity","ttl","route","long-cn") and (not keys or keys[0]!=expected)):
                    failures.append((query,expected,keys))
            report.update(top1_hits=top1,top3_hits=top3,query_count=len(corpus.QUERIES),
                          cross_process_persistence=True,source_byte_ranges_verified=True,
                          long_chinese_chunks=receipts[-1]["chunks"])
            assert not failures,failures
            report["status"]="passed"
        except Exception as exc:
            report["error"]=repr(exc)
        finally:
            if session:session.close()
    report["peak_child_rss_bytes"]=resource.getrusage(resource.RUSAGE_CHILDREN).ru_maxrss
    output = args.out or ROOT / ("build/p0-engine/python-source-corpus.json" if args.python_source else "build/p0-engine/python-worker-corpus.json")
    output.parent.mkdir(parents=True, exist_ok=True)
    output.write_text(json.dumps(report,ensure_ascii=False,indent=2)+"\n")
    print(json.dumps(report,ensure_ascii=False,indent=2))
    if report["status"]!="passed":raise SystemExit(1)


if __name__ == "__main__":main()
