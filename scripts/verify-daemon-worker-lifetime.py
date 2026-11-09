#!/usr/bin/env python3
"""新合成根真实daemon/worker跨限及旧worker健康错误传播，不调用LLM。"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import socket
import subprocess
import time
import uuid


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--daemon", type=Path, required=True)
    parser.add_argument("--component", type=Path, required=True)
    parser.add_argument("--model", type=Path, required=True)
    parser.add_argument("--out", type=Path, required=True)
    parser.add_argument("--expect-budget-failure", action="store_true", help="旧0.0.3真实跨限故障须显示不可用")
    args = parser.parse_args()
    out = args.out.resolve()
    out.mkdir(mode=0o700, parents=True, exist_ok=False)
    (out / "synthetic-only.json").write_text(json.dumps({"synthetic_only": True, "llm_invoked": False}))
    metadata = json.loads((args.component / "component.json").read_text())
    config = {"root": str(out), "worker": {"Executable": str((args.component / metadata["executable"]).resolve()),
        "Library": str((args.component / metadata["library"]).resolve()), "DataDir": str(out / "index"),
        "ModelDir": str(args.model.resolve()), "ExpectedVersion": metadata["version"], "DenyNetwork": True}}
    path = out / "service-config.json"
    path.write_text(json.dumps(config))
    ipc = Path("/tmp") / ("memlane-" + str(os.getuid()) + "-" + hashlib.sha256(str(out).encode()).hexdigest()[:12]) / "service.sock"

    def call(op, **fields):
        with socket.socket(socket.AF_UNIX) as connection:
            connection.settimeout(10)
            connection.connect(str(ipc))
            connection.sendall((json.dumps({"protocol": 1, "id": str(uuid.uuid4()), "op": op, **fields}) + "\n").encode())
            response = json.loads(connection.makefile("rb").readline())
            assert response["ok"], response
            return response.get("result")

    def wait(predicate, seconds=30):
        deadline = time.monotonic() + seconds
        while time.monotonic() < deadline:
            value = predicate()
            if value:
                return value
            time.sleep(.05)
        raise TimeoutError("actual service state did not reach the required result")

    report = {"status": "failed", "actual_daemon": True, "actual_worker": True, "actual_app": False,
              "llm_invoked": False, "expected_legacy_failure": args.expect_budget_failure, "worker_version": metadata["version"]}
    with (out / "daemon.log").open("w") as log:
        process = subprocess.Popen([str(args.daemon.resolve()), "--config", str(path)], stdout=log, stderr=log, start_new_session=True)
        try:
            wait(lambda: ipc.exists())
            initial = wait(lambda: (health if (health := call("health"))["state"] == "ready" else None))
            report["worker_pid"] = initial["worker_pid"]
            last = None
            for _ in range(10005):
                last = call("health")
                if not args.expect_budget_failure:
                    assert last["state"] == "ready" and not last["worker_error"], last
            report["requests_after_ready"] = 10005
            report["health_after_limit"] = last
            assert last["worker_pid"] == initial["worker_pid"], "crossing the limit must not silently replace the worker"
            if args.expect_budget_failure:
                assert last["state"] == "unavailable" and "duplicate_id" in last["worker_error"]
            else:
                scope = next(scope for scope in call("scopes") if scope["kind"] == "preference")
                memory = str(uuid.uuid4())
                call("apply", user_confirmed=True, mutation={"operation_id": str(uuid.uuid4()), "kind": "save", "memory": memory,
                    "dataset": scope["id"], "expected_revision": 0, "text": "daemonLifetimeToken 合成跨限原文", "title": "跨限验证"})
                current = wait(lambda: (value if (value := call("get", memory=memory))["index_state"] == "indexed" else None))
                hits = call("search", datasets=[scope["id"]], query="daemonLifetimeToken", limit=3)
                assert any(hit["version"]["memory"] == memory and hit["version"]["revision"] == current["revision"] for hit in hits)
                report["actual_save_index_current_search_after_limit"] = True
            call("stop")
            process.wait(timeout=20)
            report["explicit_stop_persisted"] = json.loads((out / "service-state.json").read_text())["stopped"] is True
            assert report["explicit_stop_persisted"] and not ipc.exists()
            report["status"] = "passed"
        except Exception as error:
            report["failure"] = repr(error)
        finally:
            if process.poll() is None:
                process.terminate()
                try:
                    process.wait(timeout=15)
                except subprocess.TimeoutExpired:
                    process.kill()
                    process.wait(timeout=5)
            report["daemon_reaped"] = process.poll() is not None
            report["daemon_exit_code"] = process.returncode
            report["socket_absent"] = not ipc.exists()
            if report.get("worker_pid"):
                report["worker_absent"] = not subprocess.run(["ps", "-p", str(report["worker_pid"]), "-o", "stat="], capture_output=True, text=True).stdout.strip()
            if not (report["daemon_reaped"] and report["socket_absent"] and report.get("worker_absent", False) and process.returncode == 0):
                report["status"] = "failed"
                report["cleanup_failure"] = "daemon, socket or owned worker did not reach a clean exit"
            (out / "report.json").write_text(json.dumps(report, ensure_ascii=False, indent=2))
            print(json.dumps({"status": report["status"], "report": str(out / "report.json"), "failure": report.get("failure")}), flush=True)
    return 0 if report["status"] == "passed" else 1


if __name__ == "__main__":
    raise SystemExit(main())
