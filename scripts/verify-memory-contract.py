#!/usr/bin/env python3
"""Verify real project/version rules against Go SQLite and Rust ONNX/LanceDB."""
import argparse
import importlib.util
import shlex
import tarfile
import hashlib
import copy
import json
from pathlib import Path
import selectors
import shutil
import sqlite3
import subprocess
import sys
import tempfile
import time
import uuid

ROOT = Path(__file__).resolve().parents[1]
_link_spec = importlib.util.spec_from_file_location("archive_links", ROOT / "scripts/archive-links.py")
_link_module = importlib.util.module_from_spec(_link_spec)
_link_spec.loader.exec_module(_link_module)
restore_links = _link_module.restore_links
MODEL = ROOT / ".local/models/multilingual-e5-base-int8"
LIBRARY = ROOT / ".local/runtimes/onnxruntime-osx-arm64-1.28.0/lib/libonnxruntime.1.28.0.dylib"


def identity(key):
    return str(uuid.uuid5(uuid.NAMESPACE_URL, "memlane-p0-contract-" + key))


class Harness:
    def __init__(self, directory, archive=None, source_worker=False):
        self.archive = archive
        self.worker_mode = bool(archive or source_worker)
        self.root = Path(directory)
        for source, name in [(ROOT / "build/p0-engine/memory-probe", "memory-probe"),
                             (ROOT / ".cache/cognee-target/release/memlane-index-probe", "index-probe"),
                             (LIBRARY, LIBRARY.name)]:
            shutil.copy2(source, self.root / name)
        shutil.copytree(MODEL, self.root / "model")
        if archive:
            worker = self.root / "worker"
            worker.mkdir()
            with tarfile.open(archive) as source:
                metadata = json.load(source.extractfile("component.json"))
                for entry in metadata["files"]:
                    rel = Path(entry["path"])
                    assert not rel.is_absolute() and ".." not in rel.parts
                    path = worker / rel
                    path.parent.mkdir(parents=True, exist_ok=True)
                    member = source.getmember(entry["path"])
                    assert member.isfile()
                    with source.extractfile(member) as src, path.open("wb") as dest:
                        shutil.copyfileobj(src,dest)
                    assert path.stat().st_size == entry["bytes"]
                    assert hashlib.sha256(path.read_bytes()).hexdigest() == entry["sha256"]
                    path.chmod(0o755 if entry["path"] == metadata["executable"] or entry.get("executable") else 0o644)
            restore_links(worker, metadata)
            self.worker_binary = worker / metadata["executable"]
        if source_worker:
            directory = self.root / "worker"
            directory.mkdir()
            self.worker_binary = directory / "memlane-cognee-worker"
            command = "exec "+shlex.quote(str(ROOT / ".local/cognee-python/bin/python"))+" "+shlex.quote(str(ROOT / "engine/python/worker.py"))+' "$@"'
            self.worker_binary.write_text("#!/bin/sh\n"+command+"\n")
            self.worker_binary.chmod(0o755)
        self.commands = []
        self.processes = []
        self.process = self.start()

    def start(self):
        cmd = [str(self.root / "memory-probe"), "--data", str(self.root / "data"),
               "--model", str(self.root / "model"), "--engine", str(self.root / "index-probe"),
               "--library", str(self.root / LIBRARY.name)]
        if self.worker_mode:
            cmd[cmd.index("--engine")+1] = str(self.worker_binary)
            cmd.append("--worker-mode")
        process = subprocess.Popen(cmd, cwd=self.root, env={"PATH": "/usr/bin:/bin"},
                                   stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        self.processes.append(process)
        return process

    def send(self, request, process=None):
        p = process or self.process
        p.stdin.write((json.dumps(request) + "\n").encode())
        p.stdin.flush()

    def receive(self, process=None):
        p = process or self.process
        with selectors.DefaultSelector() as selector:
            selector.register(p.stdout, selectors.EVENT_READ)
            if not selector.select(125):
                raise TimeoutError("P0 memory probe did not respond")
        line = p.stdout.readline()
        if not line:
            raise RuntimeError("memory probe exited: " + p.stderr.read().decode()[:1000])
        return json.loads(line)

    def call(self, request, success=True, process=None):
        start = time.monotonic()
        self.send(request, process)
        response = self.receive(process)
        self.commands.append({"op": request["op"], "ok": response["ok"],
                              "wall_ms": (time.monotonic()-start)*1000,
                              "error": response.get("error")})
        assert response["ok"] == success, response
        return response.get("result") if success else response

    def mutate(self, key, scope, text, revision=0, kind="save", restore=0, op_id=None):
        mutation = {"operation_id": op_id or f"{key}-{kind}-{revision}", "kind": kind,
                    "dataset": identity(scope), "memory": identity(key), "expected_revision": revision,
                    "restore_revision": restore, "text": text}
        return self.call({"op": "apply", "mutation": mutation})

    def index(self, key):
        return self.call({"op": "index", "memory": identity(key)})

    def search(self, scope, query="alphaPrivateToken", raw=False):
        if raw:
            request = {"op": "raw_search", "dataset": identity(scope), "query": query, "limit": 100}
        else:
            request = {"op": "search", "datasets": [identity(scope)], "query": query, "limit": 20}
        return self.call(request)

    def close(self):
        for p in self.processes:
            if p.poll() is None:
                p.stdin.close()
                try:
                    p.wait(timeout=5)
                except subprocess.TimeoutExpired:
                    p.kill()
                    p.wait()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--archive", type=Path)
    parser.add_argument("--python-source", action="store_true")
    args = parser.parse_args()
    args.worker_mode = bool(args.archive or args.python_source)
    report = {"source_worker":args.python_source,"relocated_archive":bool(args.archive),"status": "failed", "steps": [], "commands": [], "synthetic_only": True}
    out = ROOT / ("build/p0-engine/python-source-memory-contract.json" if args.python_source else "build/p0-engine/python-memory-contract-verification.json" if args.archive else "build/p0-engine/memory-contract-verification.json")
    error = None
    with tempfile.TemporaryDirectory(prefix="memlane-memory-contract-") as name:
        try:
            harness = Harness(name, args.archive, args.python_source)
        except Exception as exc:
            report["error"] = repr(exc)
            report["steps"] = [{"step":"fixture-startup","status":"failed","error":repr(exc)}]
            out.write_text(json.dumps(report,ensure_ascii=False,indent=2)+"\n")
            print(json.dumps(report,ensure_ascii=False,indent=2))
            raise SystemExit(1)

        def step(slug, focus, action):
            subprocess.run([sys.executable, str(ROOT / "scripts/p0-handoff.py"), ("python-" if args.worker_mode else "")+slug, focus], check=True)
            entry = {"step": slug, "status": "running"}
            report["steps"].append(entry)
            start = time.monotonic()
            try:
                entry["evidence"] = action()
                entry["status"] = "passed"
                entry["wall_ms"] = (time.monotonic()-start)*1000
                report["commands"] = harness.commands
                out.write_text(json.dumps(report, ensure_ascii=False, indent=2)+"\n")
                print(json.dumps(entry, ensure_ascii=False), flush=True)
                subprocess.run([sys.executable, str(ROOT / "scripts/p0-handoff.py"), ("python-" if args.worker_mode else "")+slug+"-done",
                                "本步结果已记录，按依赖继续后续验收。", "--result",
                                json.dumps(entry["evidence"], ensure_ascii=False)], check=True)
            except Exception as exc:
                entry.update(status="failed", error=repr(exc))
                raise

        try:
            def isolation():
                for key, scope, text in [
                    ("private-a", "project-a", "alphaPrivateToken: project A retries use request_id with a 10 second timeout."),
                    ("private-b", "project-b", "betaPrivateToken: project B deployment uses deployBetaPipeline."),
                    ("shared-a", "project-a", "同一段合成正文 sharedOriginalToken，但仍各自属于不同项目。"),
                    ("shared-b", "project-b", "同一段合成正文 sharedOriginalToken，但仍各自属于不同项目。"),
                    ("global", "global", "全局个人偏好 globalPreferenceToken：输出简明，并保留出处。")]:
                    saved = harness.mutate(key, scope, text)
                    assert saved["revision"] == 1 and saved["state"] == "active"
                    harness.index(key)
                for scope in ["project-a", "project-b"]:
                    hits = harness.search(scope)
                    assert hits and all(h["version"]["dataset"] == identity(scope) for h in hits)
                    raw = harness.search(scope, "sharedOriginalToken", raw=True)
                    assert all(h["dataset"] == identity(scope) for h in raw)
                    expected = identity("shared-a" if scope == "project-a" else "shared-b")
                    assert any(h["memory"] == expected for h in raw)
                hits = harness.call({"op": "search", "datasets": [identity("project-a"), identity("global")],
                                     "query": "globalPreferenceToken", "limit": 20})
                assert any(h["version"]["memory"] == identity("global") for h in hits)
                assert all(h["version"]["dataset"] != identity("project-b") for h in hits)
                harness.call({"op": "search", "datasets": [], "query": "all", "limit": 10}, success=False)
                return {"ab_scoped_search": True, "identical_text_separate_datasets": True,
                        "explicit_global_scope": True, "empty_scope_rejected": True}
            step("b1-isolation", "实际检索项目A/B与相同正文，核对显式全局范围，空范围不能搜索全部。", isolation)

            def versions():
                original = harness.call({"op": "get", "memory": identity("private-a")})["text"]
                old = next(c for c in harness.search("project-a", raw=True) if c["memory"] == identity("private-a"))
                changed = harness.mutate("private-a", "project-a", "alphaPrivateToken now uses a 30 second timeout and request_id.", 1, "edit")
                assert changed["revision"] == 2
                raw = harness.search("project-a", raw=True)
                assert any(c["revision"] == 1 and c["memory"] == identity("private-a") for c in raw)
                assert all(h["version"]["memory"] != identity("private-a") for h in harness.search("project-a"))
                harness.index("private-a")
                current = [h for h in harness.search("project-a") if h["version"]["memory"] == identity("private-a")]
                assert len(current) == 1 and current[0]["version"]["revision"] == 2
                assert "30 second" in current[0]["version"]["text"]
                deleted = harness.mutate("private-a", "project-a", "", 2, "delete")
                assert deleted["revision"] == 3 and deleted["state"] == "deleted"
                assert any(c["memory"] == identity("private-a") for c in harness.search("project-a", raw=True))
                assert all(h["version"]["memory"] != identity("private-a") for h in harness.search("project-a"))
                restored = harness.mutate("private-a", "project-a", "", 3, "restore", restore=1)
                assert restored["revision"] == 4 and restored["text"] == original
                harness.index("private-a")
                hits = [h for h in harness.search("project-a") if h["version"]["memory"] == identity("private-a")]
                assert len(hits) == 1 and hits[0]["version"]["revision"] == 4
                assert hits[0]["candidate"]["chunk_id"] != old["chunk_id"]
                assert not harness.call({"op": "resolve", "candidate": old})["valid"]
                forged = copy.deepcopy(hits[0]["candidate"])
                forged["chunk_id"] = identity("never-indexed")
                assert not harness.call({"op": "resolve", "candidate": forged})["valid"]
                forged = copy.deepcopy(hits[0]["candidate"])
                forged["text"] = "伪造片段"
                assert not harness.call({"op": "resolve", "candidate": forged})["valid"]
                with sqlite3.connect(harness.root / "data/memlane.sqlite") as db:
                    rows = db.execute("SELECT revision,text,operation FROM versions WHERE memory=? ORDER BY revision", (identity("private-a"),)).fetchall()
                assert [r[0] for r in rows] == [1, 2, 3, 4]
                assert rows[0][1] == rows[3][1] == original and rows[-1][2] == "restore"
                harness.call({"op": "raw_remove", "dataset": identity("project-a"), "chunk_ids": [old["chunk_id"]]})
                assert all(c["chunk_id"] != old["chunk_id"] for c in harness.search("project-a", raw=True))
                return {"edit_invalidates_before_reindex": True, "delete_invalidates_before_cleanup": True,
                        "restore_appends_revision_4": True, "old_equal_body_not_valid": True,
                        "missing_binding_and_forged_text_rejected": True, "physical_chunk_cleanup": True}
            step("b2-versions", "实际编辑、删除及恢复；故意保留旧chunk验证有效知识过滤，恢复产生新版本。", versions)

            def failure_retry():
                saved = harness.mutate("retry", "project-a", "retryOriginalToken：索引失败不能丢失已保存原文。", op_id="stable-operation")
                binary = harness.root / "index-probe"
                hidden = harness.root / "index-probe.offline"
                if args.worker_mode:
                    harness.call({"op":"engine_stop"})
                else:
                    binary.rename(hidden)
                try:
                    response = harness.call({"op": "index", "memory": identity("retry")}, success=False)
                    assert "worker" in response["error"] if args.worker_mode else "engine process" in response["error"]
                    harness.call({"op": "search", "datasets": [identity("project-a")], "query": "retryOriginalToken", "limit": 10}, success=False)
                    current = harness.call({"op": "get", "memory": identity("retry")})
                    assert current["text"] == saved["text"] and current["revision"] == 1
                    with sqlite3.connect(harness.root / "data/memlane.sqlite") as db:
                        assert db.execute("SELECT state FROM jobs WHERE memory=?", (identity("retry"),)).fetchone()[0] == "failed"
                finally:
                    if args.worker_mode:
                        harness.call({"op":"engine_start"})
                    else:
                        hidden.rename(binary)
                repeated = harness.mutate("retry", "project-a", saved["text"], op_id="stable-operation")
                assert repeated["revision"] == 1
                harness.call({"op": "apply", "mutation": {"operation_id": "stable-operation", "kind": "save", "dataset": identity("project-a"), "memory": identity("retry"), "expected_revision": 0, "text": "different content"}}, success=False)
                harness.index("retry")
                assert any(h["version"]["memory"] == identity("retry") for h in harness.search("project-a", "retryOriginalToken"))
                with sqlite3.connect(harness.root / "data/memlane.sqlite") as db:
                    assert db.execute("SELECT COUNT(*) FROM versions WHERE memory=?", (identity("retry"),)).fetchone()[0] == 1
                return {"real_missing_engine_error": True, "original_and_failed_job_persisted": True,
                        "read_error_not_empty_results": True, "idempotent_retry_one_version": True,
                        "different_content_same_key_conflict": True, "retry_searchable": True}
            step("b3-failure-retry", "实际移走隔离引擎触发故障，验证原文保留、持久重试、检索错误与幂等。", failure_retry)

            def late_result():
                harness.mutate("race", "project-a", "raceOriginalToken initial version" + ("天地玄黄宇宙洪荒日月盈昃辰宿列张"*160 if args.worker_mode else ""))
                second = harness.start()
                harness.send({"op": "index", "memory": identity("race")})
                # A second fixture process edits during the real Rust model load.
                harness.call({"op": "apply", "mutation": {"operation_id": "race-edit", "kind": "edit", "dataset": identity("project-a"), "memory": identity("race"), "expected_revision": 1, "text": "raceOriginalToken newer version"}}, process=second)
                response = harness.receive()
                assert not response["ok"] and "conflict" in response["error"], response
                assert all(h["version"]["memory"] != identity("race") for h in harness.search("project-a", "raceOriginalToken"))
                harness.index("race")
                hits = [h for h in harness.search("project-a", "raceOriginalToken") if h["version"]["memory"] == identity("race")]
                assert len(hits) == 1 and hits[0]["version"]["revision"] == 2
                return {"real_index_edit_race": True, "late_receipt_rejected": True, "new_revision_retry_correct": True}
            step("b4-late-result", "实际索引期间并发编辑，验证迟到结果不能把旧版本标记为可检索。", late_result)
            def restart():
                harness.close()
                harness.process = harness.start()
                current = harness.call({"op": "get", "memory": identity("private-a")})
                assert current["revision"] == 4
                hits = harness.search("project-a")
                assert any(h["version"]["revision"] == 4 and h["version"]["memory"] == identity("private-a") for h in hits)
                return {"current_revision_and_search_survive_restart": True}
            step("b5-restart", "实际重启Go探针，核对恢复后的原文和当前索引持久化。", restart)
            report.update(status="passed", restart_persistence=True)
        except Exception as exc:
            error = repr(exc)
            report["error"] = error
        finally:
            harness.close()
            report["commands"] = harness.commands
    out.write_text(json.dumps(report, ensure_ascii=False, indent=2)+"\n")
    print(json.dumps(report, ensure_ascii=False, indent=2))
    if error:
        raise SystemExit(1)


if __name__ == "__main__":
    main()
