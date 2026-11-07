#!/usr/bin/env python3
"""Real JSONL process contract from the independent worker archive and local model."""
import hashlib
import argparse
import importlib.util
import json
from pathlib import Path
import selectors
import shutil
import subprocess
import sys
import tarfile
import tempfile
import time
import uuid

ROOT = Path(__file__).resolve().parents[1]
_link_spec = importlib.util.spec_from_file_location("archive_links", ROOT / "scripts/archive-links.py")
_link_module = importlib.util.module_from_spec(_link_spec)
_link_spec.loader.exec_module(_link_module)
restore_links = _link_module.restore_links
ARCHIVE = ROOT / "build/p0-engine/memlane-cognee-rust-0.0.1-darwin-arm64.tar.gz"
MODEL = ROOT / ".local/models/multilingual-e5-base-int8"


class Session:
    source_python = False
    deny_network = False
    def __init__(self, root, data="data", model="model", ready=True):
        self.root = Path(root)
        self.stderr = tempfile.TemporaryFile()
        command = ([str(ROOT / ".local/cognee-python/bin/python"), str(ROOT / "engine/python/worker.py")]
                   if self.source_python else [str(self.root / "worker/memlane-cognee-worker")])
        command += [str(self.root / data), str(self.root / model)]
        if self.deny_network:
            command = ["/usr/bin/sandbox-exec", "-p", "(version 1)(allow default)(deny network*)"]+command
        self.process = subprocess.Popen(command,
                                        cwd=self.root, env={"PATH": "/usr/bin:/bin", "RUST_LOG": "error",
                                            "TELEMETRY_DISABLED": "true", "HOME": str(self.root / "private-home"),
                                            "ORT_DYLIB_PATH": str(self.root / "worker/libonnxruntime.1.28.0.dylib")},
                                        stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=self.stderr,
                                        bufsize=0)
        self.count = 0
        self.trace = []
        try:
            self.hello = self.read()
            assert self.hello["event"] == "hello" and self.hello["protocol"] == 1
            self.startup = self.read()
            if ready:
                assert self.startup["event"] == "ready" and self.startup["ready"], self.startup
            else:
                assert self.startup.get("ok") is False, self.startup
        except Exception as exc:
            self.stderr.seek(0)
            diagnostic = self.stderr.read().decode(errors="replace")[-5000:]
            trace = self.trace
            self.close()
            raise RuntimeError({"startup_error": repr(exc), "trace":trace, "stderr_tail":diagnostic}) from exc

    def read(self, timeout=60):
        with selectors.DefaultSelector() as selector:
            selector.register(self.process.stdout, selectors.EVENT_READ)
            if not selector.select(timeout):
                raise TimeoutError("worker response timeout")
        line = self.process.stdout.readline()
        if not line:
            self.stderr.seek(0)
            raise RuntimeError("worker exited: " + self.stderr.read().decode()[-2500:])
        value = json.loads(line)
        assert value["protocol"] == 1
        self.trace.append({"id": value.get("id"), "event": value.get("event"),
                           "ok": value.get("ok"), "error_code": value.get("error", {}).get("code")})
        return value

    def send(self, op, **fields):
        self.count += 1
        request = {"protocol": 1, "id": "request-" + str(self.count), "op": op, **fields}
        self.process.stdin.write((json.dumps(request)+"\n").encode())
        self.process.stdin.flush()
        return request["id"]

    def call(self, op, **fields):
        request_id = self.send(op, **fields)
        response = self.read()
        assert response["id"] == request_id, response
        return response

    def close(self):
        if self.process.poll() is None:
            self.process.stdin.close()
            try:
                self.process.wait(timeout=5)
            except subprocess.TimeoutExpired:
                self.process.kill()
                self.process.wait()
        self.stderr.close()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--from-step", choices=["c1", "c2", "c3"], default="c1")
    parser.add_argument("--python-source", action="store_true")
    parser.add_argument("--deny-network", action="store_true")
    parser.add_argument("--archive", type=Path, default=ARCHIVE)
    parser.add_argument("--report", type=Path)
    args = parser.parse_args()
    Session.source_python = args.python_source
    Session.deny_network = args.deny_network
    archive = args.archive.resolve()
    report_path = args.report or ROOT / ("build/p0-engine/python-source-verification.json" if args.python_source else "build/p0-engine/worker-verification.json")
    report = {"status": "failed", "steps": [], "archive": None if args.python_source else str(archive.relative_to(ROOT)), "source_worker": args.python_source, "os_network_denied":args.deny_network, "synthetic_only": True}
    if args.from_step != "c1":
        prior = json.loads(report_path.read_text())
        prefixes = ["c1-"] if args.from_step == "c2" else ["c1-", "c2-"]
        for previous in prior["steps"]:
            if any(previous["step"].startswith(p) for p in prefixes) and previous["status"] == "passed":
                report["steps"].append(previous)
        report["reused_prior_passed_steps"] = [p["step"] for p in report["steps"]]
    error = None
    sessions = []
    with tempfile.TemporaryDirectory(prefix="memlane-worker-p0-") as name:
        root = Path(name)
        worker = root / "worker"
        worker.mkdir()
        try:
            with tarfile.open(archive) as source:
                manifest = json.load(source.extractfile("component.json"))
                for entry in manifest["files"]:
                    assert not Path(entry["path"]).is_absolute() and ".." not in Path(entry["path"]).parts
                    member = source.getmember(entry["path"])
                    assert member.isfile()
                    (worker / entry["path"]).parent.mkdir(parents=True, exist_ok=True)
                    with source.extractfile(member) as data, (worker / entry["path"]).open("wb") as dest:
                        shutil.copyfileobj(data, dest)
                    path = worker / entry["path"]
                    assert path.stat().st_size == entry["bytes"]
                    assert hashlib.sha256(path.read_bytes()).hexdigest() == entry["sha256"]
                    path.chmod(0o755 if entry["path"] == manifest["executable"] or entry.get("executable") else 0o644)
            restore_links(worker, manifest)
            shutil.copytree(MODEL, root / "model")
            session = Session(root)
            sessions.append(session)
            report["hello"] = session.hello
            report["ready"] = session.startup
            dataset = str(uuid.uuid5(uuid.NAMESPACE_URL, "worker-p0-project-a"))
            memory = str(uuid.uuid5(uuid.NAMESPACE_URL, "worker-p0-memory"))
            text = "workerRawTextToken：手动原文直接建立本地索引，不依赖AI整理。"

            def step(slug, focus, action):
                subprocess.run([sys.executable, str(ROOT / "scripts/p0-handoff.py"), ("python-" if args.python_source or "python" in archive.name else "")+slug, focus], check=True)
                entry = {"step": slug, "status": "running"}
                report["steps"].append(entry)
                started = time.monotonic()
                try:
                    entry["evidence"] = action()
                    entry.update(status="passed", wall_ms=(time.monotonic()-started)*1000)
                    print(json.dumps(entry, ensure_ascii=False), flush=True)
                    report_path.write_text(json.dumps(report, ensure_ascii=False, indent=2)+"\n")
                    subprocess.run([sys.executable, str(ROOT / "scripts/p0-handoff.py"), ("python-" if args.python_source or "python" in archive.name else "")+slug+"-done",
                                    "本步完成，按依赖继续下一项。", "--result", json.dumps(entry["evidence"], ensure_ascii=False)], check=True)
                except Exception as exc:
                    entry.update(status="failed", error=repr(exc))
                    raise

            def protocol():
                response = session.call("handshake")
                assert response["ok"] and response["result"]["worker_version"] == "0.0.1"
                response = session.call("index", protocol=99, record={"dataset": dataset, "memory": memory, "revision": 1, "text": text})
                assert not response["ok"] and response["error"]["code"] == "protocol_mismatch"
                response = session.call("index", record={"dataset": dataset, "memory": memory, "revision": 1, "text": text})
                assert response["ok"] and response["result"]["chunk_ids"]
                receipt = response["result"]
                response = session.call("search", dataset=dataset, query="workerRawTextToken", top_k=3)
                assert response["ok"]
                hit = response["result"]["candidates"][0]
                assert hit["memory"] == memory and hit["revision"] == 1
                assert text.encode()[hit["start_byte"]:hit["end_byte"]].decode() == hit["text"]
                assert hit["chunk_id"] in receipt["chunk_ids"] and hit["generation"] == receipt["generation"]
                response = session.call("search", query="missing scope", top_k=3)
                assert not response["ok"] and response["error"]["code"] == "engine_error"
                response = session.call("code_graph")
                assert not response["ok"] and response["error"]["code"] == "unsupported_operation"
                assert "code_graph" not in session.startup["capabilities"] and not session.startup["graph_ready"]
                session.process.stdin.write(b"{malformed-json}\n")
                session.process.stdin.flush()
                assert session.read()["error"]["code"] == "bad_request"
                response = session.call("health", id="request-1")
                assert not response["ok"] and response["error"]["code"] == "duplicate_id"
                return {"handshake_and_ready": True, "wrong_protocol_blocks_index": True,
                        "real_index_search_traceable": True, "missing_scope_and_malformed_rejected": True,
                        "unsupported_graph_explicit": True, "duplicate_request_id_rejected": True}
            if args.from_step == "c1":
                step("c1-protocol", "从归档运行真实worker，验证握手、就绪、索引检索及错误契约。", protocol)
            else:
                # Recreate only the fixture needed by remaining checks.
                assert session.call("index", record={"dataset": dataset, "memory": memory, "revision": 1, "text": text})["ok"]

            def timeout_cancel():
                long_text = "天地玄黄宇宙洪荒日月盈昃辰宿列张" * 1500 + "cancelledJobToken"
                record = {"dataset": dataset, "memory": str(uuid.uuid4()), "revision": 1, "text": long_text}
                start = time.monotonic()
                response = session.call("index", record=record, timeout_ms=1)
                assert not response["ok"] and response["error"]["code"] == "timeout", response
                elapsed = (time.monotonic()-start)*1000
                assert elapsed < 2000, elapsed
                job = session.send("index", record=record)
                cancel = session.send("cancel", target=job)
                responses = [session.read(), session.read()]
                by_id = {r["id"]: r for r in responses}
                assert by_id[cancel]["ok"] and by_id[cancel]["result"]["cancel_requested"]
                assert not by_id[job]["ok"] and by_id[job]["error"]["code"] == "cancelled"
                assert session.call("health")["ok"]
                # A completed operation arriving while the next JSONL line is
                # incomplete must not discard the already consumed prefix.
                job = session.send("search", dataset=dataset, query="workerRawTextToken", top_k=1)
                session.count += 1
                following = json.dumps({"protocol": 1, "id": f"request-{session.count}", "op": "health"}).encode()
                half = len(following)//2
                session.process.stdin.write(following[:half]);session.process.stdin.flush()
                completed = session.read()
                assert completed["id"] == job and completed["ok"], completed
                session.process.stdin.write(following[half:]+b"\n");session.process.stdin.flush()
                assert session.read()["ok"]
                return {"real_work_timeout_ms": elapsed, "cancel_no_completed_receipt": True,
                        "health_after_cancel": True, "split_jsonl_during_completion": True,
                        "limit": "started local inference may finish briefly; supervisor must stop the process tree when required"}
            if args.from_step in ("c1", "c2"):
                step("c2-timeout-cancel", "对真实长原文索引触发超时及取消，确认没有完成索引回执并验证JSONL分片。", timeout_cancel)

            def ownership_restart():
                rejected = Session(root, ready=False)
                sessions.append(rejected)
                assert rejected.startup["error"]["code"] == "owner_conflict"
                assert rejected.process.wait(timeout=5) != 0
                session.process.kill();session.process.wait(timeout=5)
                restarted = Session(root)
                sessions.append(restarted)
                hits = restarted.call("search", dataset=dataset, query="workerRawTextToken", top_k=1)
                assert hits["ok"] and hits["result"]["candidates"][0]["memory"] == memory
                restarted.call("remove", dataset=dataset, chunk_ids=[hits["result"]["candidates"][0]["chunk_id"]])
                assert not restarted.call("search", dataset=dataset, query="workerRawTextToken", top_k=1)["result"]["candidates"]
                assert restarted.call("shutdown")["ok"]
                exit_code = restarted.process.wait(timeout=5)
                assert exit_code == 0, f"shutdown exit code {exit_code}"
                unavailable = Session(root, data="missing-model-data", model="missing-model", ready=False)
                sessions.append(unavailable)
                assert unavailable.startup["error"]["code"] == "startup_failed"
                assert unavailable.process.wait(timeout=5) != 0
                return {"second_owner_rejected": True, "crash_releases_lock": True,
                        "restart_persists_index": True, "real_remove": True,
                        "graceful_shutdown": True, "missing_model_never_ready": True}
            step("c3-owner-restart", "真实双进程独占、终止后重启持久化、派生删除和缺少模型的启动失败。", ownership_restart)
            links = subprocess.run(["/usr/bin/otool", "-L", str(worker / "memlane-cognee-worker")], capture_output=True, text=True, check=True).stdout
            assert "/Users/" not in "\n".join(links.splitlines()[1:]), links
            report.update(status="passed", relocated_archive=not args.python_source, no_development_link_dependency=not args.python_source)
        except Exception as exc:
            error = repr(exc);report["error"] = error
        finally:
            report["process_traces"] = [s.trace for s in sessions]
            for session in sessions:
                session.close()
    report_path.write_text(json.dumps(report, ensure_ascii=False, indent=2)+"\n")
    print(json.dumps(report, ensure_ascii=False, indent=2))
    if error:
        raise SystemExit(1)


if __name__ == "__main__":
    main()
