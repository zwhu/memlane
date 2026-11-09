#!/usr/bin/env python3
"""在新空业务根通过真实 Native 页面验证 pi 处理配置保存、取消、验证与启用。"""
import argparse
import fcntl
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import shutil
import signal
import socket
import subprocess
import time
import uuid


ROOT = Path(__file__).resolve().parents[1]


def load_ui_driver():
    spec = importlib.util.spec_from_file_location("memlane_p1_ui", ROOT / "scripts/p1-ui.py")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


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
    screenshots = out / "screenshots"
    client = out / "memlane-p2-processing-ui-client"
    data = client / "data"
    workspace = out / "synthetic-workspace"
    pi_directory = out / "pi-agent"
    for folder in (client, data, workspace, pi_directory):
        folder.mkdir(mode=0o700, parents=True, exist_ok=True)
    (client / "first-launch.json").write_text(json.dumps({"stage": "skipped", "skipped": True}) + "\n")

    app = args.app.resolve()
    daemon = app / "Contents/Resources/bin/memlaned"
    bridge = app / "Contents/Resources/bin/memlane"
    agent_check = args.agent_check.resolve()
    pi_executable = Path(shutil.which("pi") or "")
    source_pi = args.pi_directory.expanduser().resolve()
    source_files = [source_pi / name for name in ("settings.json", "models.json", "auth.json")]
    if not all(path.is_file() for path in source_files):
        raise SystemExit("pi settings/models/auth files required")
    original_digests = {path.name: digest(path) for path in source_files}
    source_settings = json.loads((source_pi / "settings.json").read_text())
    provider, model = source_settings.get("defaultProvider"), source_settings.get("defaultModel")
    if not provider or not model:
        raise SystemExit("pi default provider/model required")
    (pi_directory / "settings.json").write_text(json.dumps({
        "defaultProvider": provider,
        "defaultModel": model,
        "syntheticProcessingUIValidation": True,
    }, ensure_ascii=False, indent=2) + "\n")
    os.symlink(source_pi / "models.json", pi_directory / "models.json")
    os.symlink(source_pi / "auth.json", pi_directory / "auth.json")

    template = json.loads(args.worker_config.read_text())
    worker = template["worker"]
    worker["DataDir"] = str(data / "index")
    service_config = out / "service.json"
    service_config.write_text(json.dumps({
        "client_root": str(client), "root": str(data), "worker": worker,
    }, indent=2) + "\n")
    runtime_config = out / "runtime-config.json"
    runtime_config.write_text(json.dumps({
        "root": str(client),
        "component_source": "http://127.0.0.1:1/components.json",
        "component_public_key": "MSvmo63IIUrG6RdiUg8vryu80IUbIMDCHN1GGSaoaWk=",
        "worker_id": "cognee-rust",
        "model_id": "multilingual-e5-base-int8",
        "service_plist": "io.github.zwhu.memlane.service.plist",
        "allow_local_test_source": True,
    }, indent=2) + "\n")
    ipc = Path("/tmp") / ("memlane-" + str(os.getuid()) + "-" + hashlib.sha256(str(data).encode()).hexdigest()[:12]) / "service.sock"
    report = {
        "status": "failed", "actual_native_app": True, "actual_packaged_daemon": True,
        "actual_pi": True, "synthetic_only": True, "empty_business_root": True,
        "old_business_database_copied": False, "user_agent_credentials_copied": False,
        "selected_provider": provider, "selected_model": model, "app": str(app),
        "screenshots": [], "cleanup_errors": [],
    }
    owned = None
    ui = None
    connection = options = None

    def call(operation, **fields):
        with socket.socket(socket.AF_UNIX) as channel:
            channel.settimeout(130)
            channel.connect(str(ipc))
            channel.sendall((json.dumps({"protocol": 1, "id": str(uuid.uuid4()), "op": operation, **fields}) + "\n").encode())
            response = json.loads(channel.makefile("rb", buffering=0).readline())
        assert response["ok"], response
        return response.get("result")

    def adapter(action, **fields):
        completed = subprocess.run([str(agent_check)], input=json.dumps({"action": action, **fields}), text=True,
                                   capture_output=True, timeout=230)
        try:
            value = json.loads(completed.stdout)
        except json.JSONDecodeError as error:
            raise RuntimeError("adapter " + action + " returned invalid JSON: exit=" + str(completed.returncode) +
                               " stdout=" + repr(completed.stdout) + " stderr=" + repr(completed.stderr)) from error
        if completed.returncode != 0 or not value.get("ok"):
            raise RuntimeError("adapter " + action + " failed: " + str(value.get("error")))
        return value.get("result")

    def wait(fn, timeout=150):
        deadline = time.monotonic() + timeout
        last = None
        while time.monotonic() < deadline:
            try:
                value = fn()
                if value:
                    return value
            except (AssertionError, OSError, StopIteration) as error:
                last = repr(error)
            time.sleep(.08)
        raise AssertionError(("deadline", last))

    def current_validation():
        configuration = call("processing_get")
        saved = configuration.get("saved", {}).get("agent")
        return None if not saved else saved.get("validation")

    try:
        for path in (daemon, bridge, agent_check, pi_executable, Path(worker["Executable"]), Path(worker["Library"]), Path(worker["ModelDir"])):
            assert path.exists(), "required executable/component missing: " + str(path)
        daemon_log = (out / "daemon.log").open("w")
        owned = subprocess.Popen([str(daemon), "--config", str(service_config)], stdout=daemon_log, stderr=daemon_log, start_new_session=True)
        wait(lambda: ipc.exists(), timeout=30)
        wait(lambda: call("health").get("state") == "ready", timeout=90)
        scope = next(value for value in call("scopes") if value["kind"] == "preference")
        connection = call("configure_connection", user_confirmed=True, connection={
            "agent": "pi", "name": "pi", "allowed_scopes": [scope["id"]],
        })
        options = {
            "ClientRoot": str(client), "ServiceRoot": str(data), "Bridge": str(bridge),
            "AgentPath": str(pi_executable), "Workspace": str(workspace), "PiDirectory": str(pi_directory),
        }
        adapter("install", connection=connection, options=options)
        verified = adapter("verify_connection", connection=connection, options=options, run={
            "Provider": provider, "Model": model, "PiAuthDirectory": str(pi_directory),
        })
        assert verified["process_group_gone"] and verified["provider"] == provider and verified["model"] == model

        driver = load_ui_driver()
        ui = driver.UI(app, out, screenshots, config=runtime_config, extra_env={"PI_CODING_AGENT_DIR": str(pi_directory)})
        ui.press("设置")
        ui.press("记忆处理")
        wait(lambda: "pi" in ui.labels() and "当前生产边界：pi 已接入真实 daemon 验证；Codex 处理桥尚未接入。Provider 处理桥尚未接入；Magpie 处理桥尚未接入。" in ui.labels())
        ui.system_capture("01-agent-ready.png")
        report["screenshots"].append("01-agent-ready.png")
        ui.press("pi")
        wait(lambda: "有未保存修改" in ui.labels())
        ui.press("保存配置")
        wait(lambda: "已保存，等待验证" in ui.labels())
        ui.capture("02-saved.png")
        report["screenshots"].append("02-saved.png")

        ui.press("验证配置")
        validation = wait(current_validation)
        identifier = validation["validation"]
        def active_validation():
            status = call("processing_validation", processing_validation_id=identifier)
            progress = status.get("live_progress") or {}
            events = progress.get("events") or {}
            return status if status["validation"]["state"] == "validating" and progress.get("process_started") and events.get("message_start", 0) else None
        active_validation()
        wait(active_validation, timeout=120)
        ui.press("取消验证")
        cancelled = wait(lambda: (lambda value: value if value["validation"]["state"] == "cancelled" else None)(call("processing_validation", processing_validation_id=identifier)))
        wait(lambda: "验证已取消" in ui.labels())
        ui.system_capture("03-cancelled.png")
        report["screenshots"].append("03-cancelled.png")
        assert cancelled["live_progress"]["process_reaped"] and cancelled["live_progress"]["process_group_gone"]

        ui.press("验证配置")
        succeeded_id = wait(lambda: (lambda value: value if value and value["validation"] != identifier else None)(current_validation()))["validation"]
        succeeded = wait(lambda: (lambda value: value if value["validation"]["state"] == "succeeded" else None)(call("processing_validation", processing_validation_id=succeeded_id)))
        wait(lambda: "已保存并验证，可启用" in ui.labels())
        ui.system_capture("04-verified.png")
        report["screenshots"].append("04-verified.png")
        ui.press("设为当前使用")
        wait(lambda: call("processing_get")["current"]["active"])
        wait(lambda: "当前正在使用" in ui.labels())
        ui.system_capture("05-active.png")
        report["screenshots"].append("05-active.png")
        with socket.socket(socket.AF_UNIX) as native:
            native.settimeout(5)
            native.connect(ui.native_path)
            native.sendall((json.dumps({"op": "resize", "width": 980, "height": 620}) + "\n").encode())
            resized = json.loads(native.makefile("rb", buffering=0).readline())
        assert "error" not in resized, resized
        wait(lambda: ui.native()["bounds"]["Width"] == 980 and ui.native()["bounds"]["Height"] == 620)
        ui.system_capture("05-active-narrow.png")
        report["screenshots"].append("05-active-narrow.png")
        ui.press("LLM provider")
        wait(lambda: "Provider 处理桥尚未接入" in ui.labels())
        ui.press("使用 Magpie")
        wait(lambda: "Magpie 处理桥尚未接入" in ui.labels())
        ui.capture("06-unavailable-modes.png")
        report["screenshots"].append("06-unavailable-modes.png")
        report.update({
            "status": "passed", "save_via_ui": True, "active_cancel_via_ui": True,
            "validation_via_ui": True, "explicit_activation_via_ui": True,
            "cancelled_validation": cancelled, "successful_validation": succeeded,
            "provider_unavailable_honest": True, "magpie_unavailable_honest": True,
            "scope": "Native 设置记忆处理页的真实 pi 保存、活动取消、验证与显式启用；不代表 Provider、Codex、Magpie 或正式任务分派通过",
        })
    except Exception as error:
        report["failure"] = repr(error)
    finally:
        if ui is not None:
            try:
                ui.quit()
            except Exception as error:
                report["cleanup_errors"].append("ui: " + repr(error))
        if owned is not None and owned.poll() is None:
            try:
                call("stop")
                owned.wait(timeout=30)
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
            if linked.is_symlink():
                linked.unlink()
        report["user_agent_config_unchanged"] = all(path.is_file() and digest(path) == original_digests[path.name] for path in source_files)
        report["credential_links_removed"] = not (pi_directory / "models.json").exists() and not (pi_directory / "auth.json").exists()
        report["socket_absent"] = not ipc.exists()
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
        if (not report["user_agent_config_unchanged"] or not report["credential_links_removed"] or
                not report["socket_absent"] or not all(report["locks_released"].values()) or report["cleanup_errors"]):
            report["status"] = "failed"
        (out / "report.json").write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n")
        print(json.dumps({"status": report["status"], "report": str(out / "report.json"),
                          "failure": report.get("failure"), "cleanup_errors": report["cleanup_errors"]}, ensure_ascii=False), flush=True)
    return 0 if report["status"] == "passed" else 1


if __name__ == "__main__":
    raise SystemExit(main())
