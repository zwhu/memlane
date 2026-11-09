#!/usr/bin/env python3
"""新合成根跨越旧10000请求上限；真实worker/Go索引、检索、活动取消与停止。"""
import argparse
import hashlib
from importlib.machinery import SourceFileLoader
import json
import os
from pathlib import Path
import subprocess
import tarfile
import time
import uuid

ROOT = Path(__file__).resolve().parents[1]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--archive", type=Path, required=True)
    parser.add_argument("--out", type=Path, required=True)
    parser.add_argument("--go-checker", type=Path, required=True)
    parser.add_argument("--python-source", action="store_true")
    parser.add_argument("--checker-timeout", type=int, default=90, help="明确Go进程请求预算；短预算是故障验收")
    args = parser.parse_args()
    if not 1 <= args.checker_timeout <= 90:
        parser.error("checker timeout must be between 1 and 90 seconds")
    out = args.out.resolve()
    out.mkdir(parents=True, mode=0o700, exist_ok=False)
    (out / "synthetic-only.json").write_text(json.dumps({"synthetic_only": True, "no_history": True, "llm_invoked": False}))
    report = {"status": "failed", "actual_worker": True, "python_source": args.python_source,
              "actual_llm": False, "synthetic_only": True, "cleanup_errors": []}
    session = None
    try:
        report["component_sha256"] = hashlib.sha256(args.archive.read_bytes()).hexdigest()
        worker = out / "worker"
        worker.mkdir()
        with tarfile.open(args.archive) as archive:
            for member in archive.getmembers():
                assert member.isfile() and Path(member.name).name == member.name, "only the flat owned worker component is accepted"
                target = worker / member.name
                target.write_bytes(archive.extractfile(member).read())
                target.chmod(member.mode)
        metadata = json.loads((worker / "component.json").read_text())
        for asset in metadata["files"]:
            path = worker / asset["path"]
            assert path.stat().st_size == asset["bytes"] and hashlib.sha256(path.read_bytes()).hexdigest() == asset["sha256"]
        (out / "model").symlink_to(ROOT / ".local/models/multilingual-e5-base-int8", target_is_directory=True)
        U = SourceFileLoader("lifetime_worker", str(ROOT / "scripts/verify-worker.py")).load_module()

        class SequencedSession(U.Session):
            def send(self, op, **fields):
                self.count += 1
                request_id = "seq:" + str(self.count) + ":" + str(uuid.uuid4())
                request = {"protocol": 1, "id": request_id, "op": op, **fields}
                self.process.stdin.write((json.dumps(request) + "\n").encode())
                self.process.stdin.flush()
                return request_id

        SequencedSession.source_python = args.python_source
        SequencedSession.deny_network = True
        session = SequencedSession(out)
        report["ready"] = session.startup
        report["pid"] = session.process.pid
        assert session.startup["request_id_scheme"] == "sequence-uuid-v1"
        first_id = session.send("health")
        assert session.read()["ok"]
        for _ in range(10004):
            response = session.call("health")
            assert response["ok"] and response["result"]["ready"]
        report["successful_health_requests"] = session.count
        # Replaying even the oldest sequence remains rejected after crossing
        # the former capacity; this is not a recent-ID eviction window.
        session.process.stdin.write((json.dumps({"protocol": 1, "id": first_id, "op": "health"}) + "\n").encode())
        replay = session.read()
        assert not replay["ok"] and replay["error"]["code"] == "duplicate_id"
        report["oldest_replay_rejected"] = True
        target = session.send("index", record={"dataset": str(uuid.uuid4()), "memory": str(uuid.uuid4()),
            "revision": 1, "text": "syntheticLifetimeCancel " + "天地玄黄宇宙洪荒日月盈昃" * 2500})
        health_id = session.send("health")
        active = session.read()
        assert active["id"] == health_id and active["result"]["busy"], "index must be active at cancel"
        cancel_id = session.send("cancel", target=target.split(":")[-1])
        responses = [session.read(), session.read()]
        ack = next(row for row in responses if row["id"] == cancel_id)
        terminal = next(row for row in responses if row["id"] == target)
        assert ack["ok"] and ack["result"]["cancel_requested"]
        assert not terminal["ok"] and terminal["error"]["code"] == "cancelled"
        report["active_cancel_ack_and_terminal"] = {"ack": ack, "terminal": terminal}
        assert session.call("health")["result"]["ready"]
        assert session.call("shutdown")["result"]["stopping"]
        session.process.wait(timeout=8)
        report["shutdown_after_limit"] = session.process.returncode == 0
        assert report["shutdown_after_limit"]
        if not args.python_source:
            data = out / "go-client"
            data.mkdir()
            command = [str(args.go_checker.resolve()), "--worker", str(worker / metadata["executable"]),
                       "--library", str(worker / metadata["library"]), "--model", str((out / "model").resolve()),
                       "--data", str(data), "--worker-version", metadata["version"],
                       "--health-requests", "10005", "--deny-network"]
            groups = set()
            checker = None
            try:
                with (out / "go-client.log").open("w") as output, (out / "go-client.stderr").open("w") as diagnostic:
                    checker = subprocess.Popen(command, stdout=output, stderr=diagnostic, start_new_session=True)
                    deadline = time.monotonic() + args.checker_timeout
                    while checker.poll() is None:
                        for line in subprocess.check_output(["ps", "-axo", "pid=,ppid=,pgid=,comm="], text=True).splitlines():
                            fields = line.strip().split(maxsplit=3)
                            if len(fields) == 4 and fields[1] == str(checker.pid) and fields[0] == fields[2] and fields[3] == str(worker / metadata["executable"]):
                                group = int(fields[0])
                                assert group > 1 and group != os.getpgrp()
                                groups.add(group)
                        if time.monotonic() >= deadline:
                            raise subprocess.TimeoutExpired(command, args.checker_timeout)
                        time.sleep(.05)
                report["actual_go_client"] = json.loads((out / "go-client.log").read_text())
                assert checker.returncode == 0 and report["actual_go_client"]["status"] == "passed"
            except Exception as error:
                report["failure"] = repr(error)
                raise
            finally:
                try:
                    if checker is not None and checker.poll() is None:
                        checker.terminate()
                        try:
                            checker.wait(timeout=3)
                        except subprocess.TimeoutExpired:
                            checker.kill()
                            checker.wait(timeout=3)
                except Exception as error:
                    report["cleanup_errors"].append("checker: " + repr(error))
                report["captured_checker_worker_groups"] = sorted(groups)
                for group in groups:
                    try:
                        def members():
                            return [line for line in subprocess.check_output(["ps", "-axo", "pid=,pgid="], text=True).splitlines()
                                    if len(line.split()) == 2 and line.split()[1] == str(group)]
                        if members():
                            import signal
                            try:
                                os.killpg(group, signal.SIGTERM)
                            except ProcessLookupError:
                                pass
                            deadline = time.monotonic() + 3
                            while members() and time.monotonic() < deadline:
                                time.sleep(.05)
                            if members():
                                try:
                                    os.killpg(group, signal.SIGKILL)
                                except ProcessLookupError:
                                    pass
                                deadline = time.monotonic() + 3
                                while members() and time.monotonic() < deadline:
                                    time.sleep(.05)
                            if members():
                                report["cleanup_errors"].append("owned checker worker group still exists")
                    except Exception as error:
                        report["cleanup_errors"].append("worker group: " + repr(error))
                report["checker_worker_groups_gone"] = not report["cleanup_errors"]
        report["status"] = "passed"
    except Exception as error:
        report.setdefault("failure", repr(error))
    finally:
        if session is not None:
            try:
                session.close()
            except Exception as error:
                report["cleanup_errors"].append("direct session: " + repr(error))
            report["process_reaped"] = session.process.poll() is not None
        if report["cleanup_errors"] or (session is not None and not report.get("process_reaped")):
            report["status"] = "failed"
        (out / "report.json").write_text(json.dumps(report, ensure_ascii=False, indent=2))
        print(json.dumps({"status": report["status"], "report": str(out / "report.json"), "failure": report.get("failure")}), flush=True)
    return 0 if report["status"] == "passed" else 1


if __name__ == "__main__":
    raise SystemExit(main())
