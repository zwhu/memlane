#!/usr/bin/env python3
"""在新空业务根验证完整包 daemon 拥有的真实 pi 配置验证、取消与启用。"""
import argparse
import fcntl
import hashlib
import json
import os
from pathlib import Path
import shutil
import signal
import socket
import subprocess
import time
import uuid


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def process_exists(pid):
    if not pid:
        return False
    try:
        os.kill(pid, 0)
        return True
    except ProcessLookupError:
        return False


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--app", type=Path, required=True)
    parser.add_argument("--agent-check", type=Path, required=True)
    parser.add_argument("--worker-config", type=Path, required=True)
    parser.add_argument("--out", type=Path, required=True)
    parser.add_argument("--pi-directory", type=Path, default=Path.home() / ".pi/agent")
    args = parser.parse_args()

    out = args.out.resolve()
    out.mkdir(mode=0o700, parents=True, exist_ok=False)
    client = out / "memlane-p2-processing-client"
    data = client / "data"
    workspace = out / "synthetic-workspace"
    pi_directory = out / "pi-agent"
    for folder in (client, data, workspace, pi_directory):
        folder.mkdir(mode=0o700, parents=True, exist_ok=True)
    (out / "synthetic-only.json").write_text(json.dumps({
        "synthetic_only": True,
        "empty_business_root": True,
        "old_business_database_copied": False,
        "user_agent_credentials_copied": False,
    }, indent=2) + "\n")

    app = args.app.resolve()
    daemon = app / "Contents/Resources/bin/memlaned"
    bridge = app / "Contents/Resources/bin/memlane"
    agent_check = args.agent_check.resolve()
    pi_executable = Path(shutil.which("pi") or "")
    source_pi = args.pi_directory.expanduser().resolve()
    source_settings = json.loads((source_pi / "settings.json").read_text())
    provider, model = source_settings.get("defaultProvider"), source_settings.get("defaultModel")
    if not provider or not model:
        raise SystemExit("pi default provider/model required")
    source_files = [source_pi / name for name in ("settings.json", "models.json", "auth.json")]
    if not all(path.is_file() for path in source_files):
        raise SystemExit("pi settings/models/auth files required")
    original_digests = {path.name: digest(path) for path in source_files}

    # The isolated settings file is owned by this run. Provider and auth bytes
    # remain pi-owned and are linked read-only; they are removed before handoff.
    (pi_directory / "settings.json").write_text(json.dumps({
        "defaultProvider": provider,
        "defaultModel": model,
        "syntheticProcessingValidation": True,
    }, indent=2) + "\n")
    os.symlink(source_pi / "models.json", pi_directory / "models.json")
    os.symlink(source_pi / "auth.json", pi_directory / "auth.json")

    template = json.loads(args.worker_config.read_text())
    worker = template["worker"]
    worker["DataDir"] = str(data / "index")
    service_config = out / "service.json"
    service_config.write_text(json.dumps({
        "client_root": str(client),
        "root": str(data),
        "worker": worker,
    }, indent=2) + "\n")
    ipc = Path("/tmp") / ("memlane-" + str(os.getuid()) + "-" + hashlib.sha256(str(data).encode()).hexdigest()[:12]) / "service.sock"
    report = {
        "status": "failed",
        "actual_packaged_daemon": True,
        "actual_pi": True,
        "actual_native_schema": False,
        "synthetic_only": True,
        "empty_business_root": True,
        "old_business_database_copied": False,
        "user_agent_credentials_copied": False,
        "selected_provider": provider,
        "selected_model": model,
        "package": str(app),
        "daemon_sha256": digest(daemon),
        "bridge_sha256": digest(bridge),
        "worker": {"version": worker.get("ExpectedVersion"), "executable_sha256": digest(Path(worker["Executable"]))},
        "processes": [],
        "cleanup_errors": [],
    }
    owned = None
    connection = options = None

    def call(operation, *, expect_ok=True, timeout=130, **fields):
        with socket.socket(socket.AF_UNIX) as channel:
            channel.settimeout(timeout)
            channel.connect(str(ipc))
            channel.sendall((json.dumps({"protocol": 1, "id": str(uuid.uuid4()), "op": operation, **fields}) + "\n").encode())
            response = json.loads(channel.makefile("rb", buffering=0).readline())
        if expect_ok:
            assert response["ok"], response
            return response.get("result")
        assert not response["ok"], response
        return response

    def launch(name, resume=False):
        nonlocal owned
        assert not ipc.exists(), "private socket remained before launch"
        log = (out / (name + ".log")).open("w")
        command = [str(daemon), "--config", str(service_config)]
        if resume:
            command.append("--resume")
        owned = subprocess.Popen(command, stdout=log, stderr=log, start_new_session=True)
        report["processes"].append({"name": name, "pid": owned.pid, "resume": resume})
        deadline = time.monotonic() + 30
        while not ipc.exists():
            if owned.poll() is not None or time.monotonic() > deadline:
                raise RuntimeError("packaged daemon did not publish its private socket")
            time.sleep(.05)
        deadline = time.monotonic() + 90
        while time.monotonic() < deadline:
            health = call("health")
            if health["state"] == "ready" and health["worker_pid"]:
                report.setdefault("worker_pids", []).append(health["worker_pid"])
                return
            time.sleep(.1)
        raise RuntimeError("packaged daemon worker did not become ready")

    def stop():
        nonlocal owned
        call("stop")
        owned.wait(timeout=30)
        assert owned.returncode == 0 and not ipc.exists()
        owned = None

    def adapter(action, **fields):
        completed = subprocess.run([str(agent_check)], input=json.dumps({"action": action, **fields}), text=True,
                                   capture_output=True, timeout=230)
        value = json.loads(completed.stdout)
        if completed.returncode != 0 or not value.get("ok"):
            raise RuntimeError("adapter " + action + " failed: " + str(value.get("error")))
        return value.get("result")

    def begin_validation(revision):
        return call("processing_validate", user_confirmed=True, processing_validation={
            "operation_id": str(uuid.uuid4()), "revision": revision,
        })

    def validation_status(identifier):
        return call("processing_validation", processing_validation_id=identifier)

    def wait_validation(identifier, timeout=150):
        deadline = time.monotonic() + timeout
        latest = None
        while time.monotonic() < deadline:
            latest = validation_status(identifier)
            if latest["validation"]["state"] != "validating":
                return latest
            time.sleep(.05)
        raise RuntimeError("processing validation did not reach terminal state: " + repr(latest))

    try:
        for path in (daemon, bridge, agent_check, pi_executable, Path(worker["Executable"]), Path(worker["Library"]), Path(worker["ModelDir"])):
            assert path.exists(), "required executable/component missing: " + str(path)
        launch("daemon-first")
        scopes = call("scopes")
        scope = next(value for value in scopes if value["kind"] == "preference")
        connection = call("configure_connection", user_confirmed=True, connection={
            "agent": "pi", "name": "真实 pi daemon 处理验证", "project": "", "allowed_scopes": [scope["id"]],
        })
        options = {
            "ClientRoot": str(client), "ServiceRoot": str(data), "Bridge": str(bridge),
            "AgentPath": str(pi_executable), "Workspace": str(workspace), "PiDirectory": str(pi_directory),
        }
        installed = adapter("install", connection=connection, options=options)
        assert installed.get("verified_generation", 0) == 0
        execution = adapter("verify_connection", connection=connection, options=options, run={
            "Provider": provider, "Model": model, "PiAuthDirectory": str(pi_directory),
        })
        calls = [value["tool"] for value in execution["calls"] if value["response"].get("ok")]
        assert {"memory_save", "memory_status", "memory_search"}.issubset(calls)
        assert execution["process_group_gone"] and execution["provider"] == provider and execution["model"] == model
        verified_installation = json.loads((client / "agent-adapters" / connection["id"] / "installation.json").read_text())
        assert verified_installation["verified_generation"] == connection["generation"] and verified_installation["verified_at"]
        report["connection_verification"] = {
            "agent_version": execution["version"], "provider": execution["provider"], "model": execution["model"],
            "tool_calls": calls, "process_group_gone": execution["process_group_gone"],
        }

        saved = call("processing_save", user_confirmed=True, processing_save={
            "operation_id": str(uuid.uuid4()), "spec": {
                "mode": "agent", "agent": "pi", "provider": provider, "model": model,
                "connection": connection["id"], "connection_generation": connection["generation"],
            },
        })
        assert not call("processing_get")["current"]["active"]

        cancelled = begin_validation(saved["revision"])
        deadline = time.monotonic() + 120
        before_cancel = None
        while time.monotonic() < deadline:
            status = validation_status(cancelled["validation"])
            progress = status.get("live_progress") or {}
            events = progress.get("events") or {}
            if status["validation"]["state"] != "validating":
                raise RuntimeError("real pi validation completed before active cancellation")
            if progress.get("process_started") and events.get("message_start", 0) >= 1:
                before_cancel = status
                break
            time.sleep(.02)
        if before_cancel is None:
            raise RuntimeError("real pi validation never reached active model streaming")
        cancelled_pid = before_cancel["live_progress"]["pid"]
        call("processing_validation_cancel", user_confirmed=True, processing_validation_id=cancelled["validation"])
        cancelled_terminal = wait_validation(cancelled["validation"])
        cancelled_progress = cancelled_terminal.get("live_progress") or {}
        assert cancelled_terminal["validation"]["state"] == "cancelled"
        assert cancelled_progress.get("abort_sent") and cancelled_progress.get("abort_acknowledged")
        assert cancelled_progress.get("process_reaped") and cancelled_progress.get("process_group_gone")
        assert not process_exists(cancelled_pid)
        report["cancelled_validation"] = cancelled_terminal

        succeeded = begin_validation(saved["revision"])
        succeeded_terminal = wait_validation(succeeded["validation"])
        succeeded_progress = succeeded_terminal.get("live_progress") or {}
        assert succeeded_terminal["validation"]["state"] == "succeeded"
        assert succeeded_terminal["validation"]["receipt_digest"]
        assert succeeded_progress.get("terminal") and succeeded_progress.get("process_reaped") and succeeded_progress.get("process_group_gone")
        assert succeeded_progress.get("native_schema_requested") and not succeeded_progress.get("abort_sent")
        report["actual_native_schema"] = True
        report["successful_validation"] = succeeded_terminal

        activation = call("processing_activate", user_confirmed=True, processing_activation={
            "operation_id": str(uuid.uuid4()), "revision": saved["revision"], "expected_generation": 0,
        })
        assert activation["revision"] == saved["revision"] and activation["applied_generation"] == 1
        current = call("processing_get")["current"]
        assert current["active"] and current["current"]["revision"] == saved["revision"]
        report["activation"] = activation
        stop()

        launch("daemon-restart", resume=True)
        restarted = call("processing_get")["current"]
        assert restarted["active"] and restarted["current"]["revision"] == saved["revision"]
        after_restart = validation_status(succeeded["validation"])
        assert after_restart["validation"]["state"] == "succeeded" and "live_progress" not in after_restart
        report["restart"] = {"current": restarted, "ephemeral_process_observation_cleared": True}
        stop()

        report.update(status="passed", schema=17, explicit_activation=True, current_retained_after_restart=True,
                      scope="真实pi/DeepSeek daemon验证、活动取消、显式启用与重启；不代表Provider、Magpie、Native A页面或正式任务分派通过")
    except Exception as error:
        report["failure"] = repr(error)
    finally:
        if owned is not None and owned.poll() is None:
            try:
                os.killpg(owned.pid, signal.SIGTERM)
                owned.wait(timeout=15)
            except Exception as error:
                report["cleanup_errors"].append("daemon: " + repr(error))
                try:
                    os.killpg(owned.pid, signal.SIGKILL)
                    owned.wait(timeout=5)
                except Exception:
                    pass
        if connection is not None and options is not None:
            try:
                adapter("uninstall", connection=connection, options=options)
            except Exception as error:
                report["cleanup_errors"].append("adapter: " + repr(error))
        for linked in (pi_directory / "models.json", pi_directory / "auth.json"):
            try:
                if linked.is_symlink():
                    linked.unlink()
            except Exception as error:
                report["cleanup_errors"].append("credential-link: " + repr(error))
        report["user_agent_config_unchanged"] = all(path.is_file() and digest(path) == original_digests[path.name] for path in source_files)
        report["credential_links_removed"] = not (pi_directory / "models.json").exists() and not (pi_directory / "auth.json").exists()
        report["socket_absent"] = not ipc.exists()
        report["owned_daemons_absent"] = all(not process_exists(value["pid"]) for value in report["processes"])
        report["cancelled_pi_absent"] = not process_exists((report.get("cancelled_validation", {}).get("live_progress") or {}).get("pid"))
        report["locks_released"] = {}
        for name, path in {"service": data / ".service.lock", "worker": data / "index/.worker.lock"}.items():
            released = False
            try:
                with path.open("a+") as handle:
                    fcntl.flock(handle.fileno(), fcntl.LOCK_EX | fcntl.LOCK_NB)
                    released = True
            except OSError:
                pass
            report["locks_released"][name] = released
        if not all((report["user_agent_config_unchanged"], report["credential_links_removed"], report["socket_absent"],
                    report["owned_daemons_absent"], report["cancelled_pi_absent"], all(report["locks_released"].values()))) or report["cleanup_errors"]:
            report["status"] = "failed"
        (out / "report.json").write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n")
        print(json.dumps({"status": report["status"], "report": str(out / "report.json"), "failure": report.get("failure"),
                          "cleanup_errors": report["cleanup_errors"]}, ensure_ascii=False), flush=True)
    return 0 if report["status"] == "passed" else 1


if __name__ == "__main__":
    raise SystemExit(main())
