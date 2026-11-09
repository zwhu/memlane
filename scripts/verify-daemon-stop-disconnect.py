#!/usr/bin/env python3
"""真实 daemon 在 stop 调用者断线后仍须完成停止；只创建新合成根。"""
import argparse
import fcntl
import hashlib
import json
import os
from pathlib import Path
import socket
import subprocess
import tempfile
import time
import uuid


def call(path, operation):
    with socket.socket(socket.AF_UNIX) as connection:
        connection.settimeout(2)
        connection.connect(str(path))
        connection.sendall(json.dumps({"protocol": 1, "id": str(uuid.uuid4()), "op": operation}).encode() + b"\n")
        return json.loads(connection.makefile("rb").readline())


def lock_owned(root):
    with (root / ".service.lock").open("r+") as handle:
        try:
            fcntl.flock(handle, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            return True
        fcntl.flock(handle, fcntl.LOCK_UN)
        return False


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--daemon", type=Path, required=True)
    parser.add_argument("--out", type=Path, required=True)
    parser.add_argument("--worker", type=Path)
    parser.add_argument("--model", type=Path)
    parser.add_argument("--library", type=Path)
    args = parser.parse_args()
    if any((args.worker, args.model, args.library)) and not all((args.worker, args.model, args.library)):
        parser.error("worker, model and library must be supplied together")
    out = args.out.resolve()
    out.mkdir(mode=0o700, parents=True, exist_ok=False)
    daemon = args.daemon.resolve()
    report = {"status": "failed", "actual_daemon": True, "actual_worker": bool(args.worker),
              "actual_app": False, "llm_invoked": False, "daemon_sha256": hashlib.sha256(daemon.read_bytes()).hexdigest(), "trials": []}
    for number in range(3):
        root = Path(tempfile.mkdtemp(prefix="stop-", dir=out))
        (root / "synthetic-only.json").write_text(json.dumps({"synthetic_only": True, "no_business_history": True}))
        worker = {"Executable": str(args.worker.resolve() if args.worker else root / "missing-worker"),
                  "DataDir": str(root / "index"), "ModelDir": str(args.model.resolve() if args.model else root / "missing-model"),
                  "Library": str(args.library.resolve() if args.library else root / "missing-library"),
                  "ExpectedVersion": "0.0.3", "DenyNetwork": bool(args.worker)}
        config = root / "service-config.json"
        config.write_text(json.dumps({"root": str(root), "worker": worker}))
        ipc = Path("/tmp") / ("memlane-" + str(os.getuid()) + "-" + hashlib.sha256(str(root).encode()).hexdigest()[:12]) / "service.sock"
        trial = {"number": number + 1, "root": str(root), "fixture": "real Rust" if args.worker else "worker deliberately absent"}
        with (root / "daemon.log").open("w") as log:
            process = subprocess.Popen([str(daemon), "--config", str(config)], stdout=log, stderr=log, start_new_session=True)
            trial["daemon_pid"] = process.pid
            try:
                deadline = time.monotonic() + 35
                while not ipc.exists():
                    if process.poll() is not None or time.monotonic() > deadline:
                        raise RuntimeError("daemon socket did not become available")
                    time.sleep(.02)
                health = call(ipc, "health")
                while args.worker and health.get("result", {}).get("state") != "ready":
                    if time.monotonic() > deadline:
                        raise RuntimeError("real worker did not become ready")
                    time.sleep(.05)
                    health = call(ipc, "health")
                trial["health_before"] = health
                worker_pid = health.get("result", {}).get("worker_pid", 0)
                if args.worker:
                    parent = subprocess.check_output(["ps", "-p", str(worker_pid), "-o", "ppid="], text=True).strip()
                    assert worker_pid > 1 and parent == str(process.pid), "worker must belong to this trial's daemon"
                    trial["worker_pid"] = worker_pid
                with socket.socket(socket.AF_UNIX) as connection:
                    connection.connect(str(ipc))
                    connection.sendall(json.dumps({"protocol": 1, "id": str(uuid.uuid4()), "op": "stop"}).encode() + b"\n")
                    connection.shutdown(socket.SHUT_RDWR)
                deadline = time.monotonic() + 5
                while time.monotonic() < deadline:
                    if process.poll() is not None:
                        break
                    time.sleep(.02)
                state = root / "service-state.json"
                trial["stop_persisted"] = state.exists() and json.loads(state.read_text()).get("stopped") is True
                trial["daemon_exited_after_disconnect"] = process.poll() is not None
                trial["socket_removed_after_disconnect"] = not ipc.exists()
                trial["lock_released_after_disconnect"] = not lock_owned(root)
                trial["passed"] = all(trial[key] for key in ("stop_persisted", "daemon_exited_after_disconnect", "socket_removed_after_disconnect", "lock_released_after_disconnect"))
                if args.worker:
                    trial["worker_exited_after_disconnect"] = not subprocess.run(["ps", "-p", str(worker_pid), "-o", "stat="], capture_output=True, text=True).stdout.strip()
                    trial["passed"] = trial["passed"] and trial["worker_exited_after_disconnect"]
            except Exception as error:
                trial["error"] = type(error).__name__ + ": " + str(error)
                trial["passed"] = False
            finally:
                if process.poll() is None:
                    process.terminate()
                    try:
                        process.wait(timeout=12)
                    except subprocess.TimeoutExpired:
                        process.kill()
                        process.wait(timeout=4)
                trial["cleanup_exit_code"] = process.returncode
                trial["cleanup_socket_absent"] = not ipc.exists()
                if args.worker and trial.get("worker_pid"):
                    trial["cleanup_worker_absent"] = not subprocess.run(["ps", "-p", str(trial["worker_pid"]), "-o", "stat="], capture_output=True, text=True).stdout.strip()
        report["trials"].append(trial)
    if all(trial["passed"] for trial in report["trials"]):
        report["status"] = "passed"
    (out / "report.json").write_text(json.dumps(report, ensure_ascii=False, indent=2))
    print(json.dumps({"status": report["status"], "report": str(out / "report.json"),
                      "passed_trials": sum(trial["passed"] for trial in report["trials"]), "actual_worker": bool(args.worker)}), flush=True)
    return 0 if report["status"] == "passed" else 1


if __name__ == "__main__":
    raise SystemExit(main())
