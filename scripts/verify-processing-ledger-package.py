#!/usr/bin/env python3
"""新合成根验证同包daemon配置账本与schema16迁移；无模型、不改用户配置。"""
import argparse
import contextlib
import hashlib
import json
import os
from pathlib import Path
import socket
import sqlite3
import subprocess
import time
import uuid


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--app", type=Path, required=True)
    parser.add_argument("--legacy-daemon", type=Path, required=True, help="已验证schema16旧包的真实daemon")
    parser.add_argument("--out", type=Path, required=True)
    args = parser.parse_args()
    out = args.out.resolve()
    out.mkdir(mode=0o700, parents=True, exist_ok=False)
    (out / "synthetic-only.json").write_text(json.dumps({"synthetic_only": True, "llm_invoked": False, "input_is_explicit_migration_fixture": True}))
    root = out / "data"
    root.mkdir()
    config = out / "service-config.json"
    config.write_text(json.dumps({"root": str(root)}))
    ipc = Path("/tmp") / ("memlane-" + str(os.getuid()) + "-" + hashlib.sha256(str(root).encode()).hexdigest()[:12]) / "service.sock"
    daemon = args.app.resolve() / "Contents/Resources/bin/memlaned"
    report = {"status": "failed", "actual_packaged_daemon": True, "actual_app_window": False,
              "synthetic_only": True, "llm_invoked": False, "cleanup_errors": [], "processes": []}
    process = None

    def call(op, **fields):
        with socket.socket(socket.AF_UNIX) as channel:
            channel.settimeout(15)
            channel.connect(str(ipc))
            channel.sendall((json.dumps({"protocol": 1, "id": str(uuid.uuid4()), "op": op, **fields}) + "\n").encode())
            value = json.loads(channel.makefile("rb").readline())
            assert value["ok"], value
            return value.get("result")

    def launch(executable, name, *, resume=False, expect_stopped=False):
        nonlocal process
        assert not ipc.exists(), "private socket remained before restart"
        with (out / (name + ".log")).open("w") as log:
            command = [str(executable.resolve()), "--config", str(config)]
            if resume:
                command.append("--resume")
            process = subprocess.Popen(command, stdout=log, stderr=log, start_new_session=True)
        report["processes"].append({"pid": process.pid, "executable": str(executable.resolve()),
                                    "config": str(config), "explicit_synthetic_resume": resume})
        if expect_stopped:
            process.wait(timeout=15)
            assert process.returncode == 0 and not ipc.exists(), "stopped daemon resumed implicitly"
            return
        deadline = time.monotonic() + 20
        while not ipc.exists():
            if process.poll() is not None or time.monotonic() > deadline:
                raise RuntimeError("owned daemon did not publish its private socket")
            time.sleep(.05)

    def stop():
        call("stop")
        process.wait(timeout=15)
        assert process.returncode == 0 and not ipc.exists()

    def database_rows(database=None):
        assert process is None or process.poll() is not None, "snapshot requires the owned daemon to be stopped"
        database = database or root / "memlane.sqlite"
        assert database.is_file()
        for suffix in ["-wal", "-journal"]:
            sidecar = Path(str(database) + suffix)
            assert not sidecar.exists() or sidecar.stat().st_size == 0, "uncheckpointed data prohibits immutable read"
        report.setdefault("database_reads", []).append({
            "path": str(database), "exists": database.is_file(),
            "files": [{"name": item.name, "bytes": item.stat().st_size}
                      for item in sorted(root.iterdir()) if item.is_file()],
            "sqlite_version": sqlite3.sqlite_version})
        # macOS SQLite cannot initialize read-only WAL sidecars on this host.
        # Immutable reads are restricted to fully stopped, checkpointed files.
        with contextlib.closing(sqlite3.connect(database.as_uri() + "?mode=ro&immutable=1", uri=True)) as db:
            version = db.execute("PRAGMA user_version").fetchone()[0]
            names = [row[0] for row in db.execute("SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name")]
            rows = {name: db.execute('SELECT * FROM "' + name.replace('"', '""') + '"').fetchall() for name in names}
            return version, rows

    try:
        assert daemon.is_file() and (args.app / "Contents/Resources/bin/memlane").is_file()
        assert list((args.app / "Contents/Library/LaunchAgents").glob("*.plist"))
        report["daemon_sha256"] = hashlib.sha256(daemon.read_bytes()).hexdigest()
        launch(args.legacy_daemon, "legacy-daemon")
        scopes = call("scopes")
        scope = next(scope for scope in scopes if scope["kind"] == "preference")
        memory = str(uuid.uuid4())
        for revision, kind, text in [(0, "save", "explicit schema16 fixture v1"), (1, "edit", "explicit schema16 user version v2")]:
            call("apply", user_confirmed=True, mutation={"operation_id": str(uuid.uuid4()), "kind": kind,
                "memory": memory, "dataset": scope["id"], "expected_revision": revision, "text": text})
        stop()
        version, before = database_rows()
        assert version == 16
        stop_bytes = (root / "service-state.json").read_bytes()
        launch(daemon, "new-packaged-daemon-stopped", expect_stopped=True)
        assert (root / "service-state.json").read_bytes() == stop_bytes
        assert database_rows() == (16, before), "stopped startup modified the legacy database"
        report["stopped_startup_preserved_legacy_database"] = True
        launch(daemon, "new-packaged-daemon", resume=True)
        current = call("get", memory=memory)
        assert current["revision"] == 2 and current["text"] == "explicit schema16 user version v2"
        empty = call("processing_get")
        assert not empty["saved"] and not empty["current"]["active"]
        stop()
        version, after = database_rows()
        assert version == 17 and all(sorted(after[name]) == sorted(rows) for name, rows in before.items())
        backups = list((root / "backups").glob("schema-v16-*.sqlite"))
        assert len(backups) == 1 and database_rows(backups[0]) == (16, before)
        launch(daemon, "new-packaged-daemon-save", resume=True)
        draft = {"operation_id": str(uuid.uuid4()), "spec": {"mode": "provider", "provider": "ledger-only fixture",
            "model": "unverified-fixture", "protocol": "openai-responses", "endpoint": "https://provider.invalid/v1"}}
        saved = call("processing_save", user_confirmed=True, processing_save=draft)
        assert call("processing_save", user_confirmed=True, processing_save=draft)["revision"] == saved["revision"]
        assert not call("processing_get")["current"]["active"]
        stop()
        stop_bytes = (root / "service-state.json").read_bytes()
        launch(daemon, "new-packaged-daemon-restart-stopped", expect_stopped=True)
        assert (root / "service-state.json").read_bytes() == stop_bytes
        launch(daemon, "new-packaged-daemon-restart", resume=True)
        retained = call("processing_get")
        assert retained["saved"]["provider"]["revision"] == saved["revision"] and not retained["current"]["active"]
        stop()
        assert json.loads((root / "service-state.json").read_text())["stopped"] is True
        report.update(status="passed", old_tables_preserved=len(before), schema_before=16, schema_after=17,
            user_version_retained=2, model_not_activated=True, candidate_retained_after_restart=True,
            stop_intent_retained=True, migration_required_explicit_synthetic_resume=True,
            scope="完整包daemon的真实账本/迁移/重启；不代表新A界面、后端验证或普通环境准备通过")
    except Exception as error:
        report["failure"] = repr(error)
    finally:
        if process is not None and process.poll() is None:
            process.terminate()
            try:
                process.wait(timeout=10)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait(timeout=5)
        report["socket_absent"] = not ipc.exists()
        report["all_owned_processes_absent"] = all(not subprocess.run(["ps", "-p", str(entry["pid"]), "-o", "stat="], capture_output=True, text=True).stdout.strip() for entry in report["processes"])
        if not report["all_owned_processes_absent"] or not report["socket_absent"]:
            report["status"] = "failed"
            report["cleanup_errors"].append("owned daemon/socket remained")
        (out / "report.json").write_text(json.dumps(report, ensure_ascii=False, indent=2))
        print(json.dumps({"status": report["status"], "report": str(out / "report.json"), "failure": report.get("failure")}), flush=True)
    return 0 if report["status"] == "passed" else 1


if __name__ == "__main__":
    raise SystemExit(main())
