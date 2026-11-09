#!/usr/bin/env python3
"""在隔离空业务根验证三步初始化复用真实 pi 记忆处理账本。"""
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
    parser.add_argument("--fidelity", action="store_true", help="增加设置复用、首页和窄窗口系统截图")
    args = parser.parse_args()

    out = args.out.resolve()
    out.mkdir(mode=0o700, parents=True, exist_ok=False)
    screenshots = out / "screenshots"
    # The reused hardened P2 adapter only accepts an explicitly isolated P2
    # client root; the enclosing output directory still identifies this as the
    # first-launch validation rather than a settings-page rerun.
    client = out / "memlane-p2-first-launch-processing-client"
    data = client / "data"
    workspace = out / "synthetic-workspace"
    pi_directory = out / "pi-agent"
    for folder in (client, data, workspace, pi_directory):
        folder.mkdir(mode=0o700, parents=True, exist_ok=True)
    (client / "first-launch.json").write_text(json.dumps({
        "stage": "ready",
        "step": "agents",
        "completed": False,
        "skipped": False,
        "message": "环境已通过独立验收并在本次启动中真实可用。",
    }, ensure_ascii=False) + "\n")

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
        "syntheticFirstLaunchProcessingValidation": True,
    }, ensure_ascii=False, indent=2) + "\n")
    os.symlink(source_pi / "models.json", pi_directory / "models.json")
    os.symlink(source_pi / "auth.json", pi_directory / "auth.json")

    template = json.loads(args.worker_config.read_text())
    worker = template["worker"]
    worker["DataDir"] = str(data / "index")
    service_config = client / "service.json"
    service_config.write_text(json.dumps({
        "client_root": str(client), "root": str(data), "worker": worker,
    }, indent=2) + "\n")
    runtime_config = out / "runtime-config.json"
    bundled_runtime_path = app / "Contents/Resources/runtime-config.json"
    bundled_runtime = json.loads(bundled_runtime_path.read_text()) if bundled_runtime_path.is_file() else {}
    service_plist = bundled_runtime.get("service_plist", "io.github.zwhu.memlane.service.plist")
    runtime = {
        "root": str(client),
        "component_source": "http://127.0.0.1:1/components.json",
        "component_public_key": "MSvmo63IIUrG6RdiUg8vryu80IUbIMDCHN1GGSaoaWk=",
        "worker_id": "cognee-rust",
        "model_id": "multilingual-e5-base-int8",
        "service_plist": service_plist,
        "allow_local_test_source": True,
    }
    runtime_config.write_text(json.dumps(runtime, indent=2) + "\n")
    ipc = Path("/tmp") / ("memlane-" + str(os.getuid()) + "-" + hashlib.sha256(str(data).encode()).hexdigest()[:12]) / "service.sock"
    report = {
        "status": "failed", "actual_native_app": True, "actual_packaged_daemon": True,
        "actual_pi": True, "synthetic_only": True, "empty_business_root": True,
        "environment_preparation_replayed": False, "environment_runtime_ready": False,
        "packaged_service_registered": False,
        "old_business_database_copied": False, "user_agent_credentials_copied": False,
        "selected_provider": provider, "selected_model": model, "app": str(app),
        "screenshots": [], "cleanup_errors": [],
    }
    owned = None
    ui = None
    connection = options = None
    installed = False
    service_registered = False

    def app_action(action):
        action_log = out / ("app-action-" + action + ".log")
        ui_home = out / "ui-home"
        ui_home.mkdir(mode=0o700, exist_ok=True)
        command = [
            "/usr/bin/open", "-n", "-W", "--stdout", str(action_log), "--stderr", str(action_log),
            "--env", "HOME=" + str(ui_home), "--env", "PATH=/usr/bin:/bin", str(app),
            "--args", "--p0-action", action,
        ]
        completed = subprocess.run(command, capture_output=True, text=True, timeout=45)
        if completed.returncode != 0:
            raise RuntimeError("app action " + action + " failed: " + completed.stderr)
        receipts = []
        for line in action_log.read_text().splitlines():
            try:
                value = json.loads(line)
            except json.JSONDecodeError:
                continue
            if isinstance(value, dict) and value.get("action") == action:
                receipts.append(value)
        if not receipts:
            raise RuntimeError("app action " + action + " returned no receipt")
        receipt = receipts[-1]
        if receipt.get("error"):
            raise RuntimeError("app action " + action + " failed: " + str(receipt["error"]))
        return receipt

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
            if action == "verify_connection":
                diagnostics = out / ("adapter-verification-" + uuid.uuid4().hex[:8] + ".json")
                diagnostics.write_text(json.dumps(value, ensure_ascii=False, indent=2) + "\n")
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

    def capture(name):
        ui.system_capture(name)
        report["screenshots"].append(name)

    def resize(width, height):
        with socket.socket(socket.AF_UNIX) as channel:
            channel.settimeout(10)
            channel.connect(ui.native_path)
            channel.sendall((json.dumps({"op": "resize", "width": width, "height": height}) + "\n").encode())
            result = json.loads(channel.makefile("rb", buffering=0).readline())
        assert "error" not in result, result
        ui.req("idle", timeout=2000)

    def assert_fixed_actions(labels, width, height):
        nodes = ui.nodes()
        for label in labels:
            node = next(node for node in nodes if node.get("label") == label and node.get("role") == "button")
            bounds = node["bounds"]
            assert bounds["x"] >= 0 and bounds["y"] >= 0 and bounds["x"] + bounds["width"] <= width + 1 and bounds["y"] + bounds["height"] <= height + 1, (label, bounds)

    try:
        for path in (daemon, bridge, agent_check, pi_executable, Path(worker["Executable"]), Path(worker["Library"]), Path(worker["ModelDir"])):
            assert path.exists(), "required executable/component missing: " + str(path)
        packaged_service = bundled_runtime.get("root") == str(client) and bundled_runtime.get("service_plist") == service_plist
        if packaged_service:
            report["service_registration"] = app_action("register")
            service_registered = True
            report["packaged_service_registered"] = True
        else:
            daemon_log = (out / "daemon.log").open("w")
            owned = subprocess.Popen([str(daemon), "--config", str(service_config)], stdout=daemon_log, stderr=daemon_log, start_new_session=True)
        wait(lambda: ipc.exists(), timeout=30)
        wait(lambda: call("health").get("state") == "ready", timeout=90)
        report["environment_runtime_ready"] = True
        scope = next(value for value in call("scopes") if value["kind"] == "preference")
        connection = call("configure_connection", user_confirmed=True, connection={
            "agent": "pi", "name": "pi", "allowed_scopes": [scope["id"]],
        })
        options = {
            "ClientRoot": str(client), "ServiceRoot": str(data), "Bridge": str(bridge),
            "AgentPath": str(pi_executable), "Workspace": str(workspace), "PiDirectory": str(pi_directory),
        }
        adapter("install", connection=connection, options=options)
        installed = True
        if args.fidelity:
            driver = load_ui_driver()
            ui = driver.UI(app, out, screenshots, config=runtime_config, extra_env={
                "PI_CODING_AGENT_DIR": str(pi_directory),
                "PATH": str(pi_executable.parent) + ":/usr/bin:/bin",
            })
            wait(lambda: "继续配置记忆处理" in ui.labels())
            capture("00-agent-before-verification-system.png")
        report["connection_verification_attempts"] = []
        verified = None
        for attempt in range(1, 3):
            try:
                verified = adapter("verify_connection", connection=connection, options=options, run={
                    "Provider": provider, "Model": model, "PiAuthDirectory": str(pi_directory),
                })
                report["connection_verification_attempts"].append({"attempt": attempt, "status": "passed"})
                break
            except RuntimeError as error:
                report["connection_verification_attempts"].append({"attempt": attempt, "status": "failed", "error": str(error)})
                if attempt == 2:
                    raise
                time.sleep(1)
        assert verified["process_group_gone"] and verified["provider"] == provider and verified["model"] == model
        baseline_counts = {
            kind: call("list", list_query={"kind": kind})["total"]
            for kind in ("knowledge", "raw")
        }
        report["pre_ui_record_counts"] = baseline_counts

        if ui is None:
            driver = load_ui_driver()
            ui = driver.UI(app, out, screenshots, config=runtime_config, extra_env={
                "PI_CODING_AGENT_DIR": str(pi_directory),
                "PATH": str(pi_executable.parent) + ":/usr/bin:/bin",
            })
        wait(lambda: all(label in ui.labels() for label in ("准备环境", "连接 Agent", "配置记忆处理", "继续配置记忆处理")))
        # Native buttons expose the card as one accessibility node; internal
        # Text children are not independent labels. Check the actual card's
        # receipt-derived label rather than waiting for an invisible Text node.
        wait(lambda: ui.node(key="connection-card-pi")["label"] == "pi · 可用")
        state = json.loads((client / "first-launch.json").read_text())
        assert state["step"] == "agents" and state["stage"] == "ready" and not state.get("completed")
        capture("01-agents-system.png")

        ui.press_key("first-launch-processing")
        wait(lambda: "使用已连接 Agent" in ui.labels() and "pi" in ui.labels())
        state = json.loads((client / "first-launch.json").read_text())
        assert state["step"] == "processing" and not state.get("completed")
        assert "disabled" in ui.node(key="first-launch-complete").get("states", [])
        capture("02-processing-system.png")

        ui.press("pi")
        wait(lambda: "有未保存修改" in ui.labels())
        ui.press("保存配置")
        wait(lambda: "已保存，等待验证" in ui.labels())
        capture("03-saved-system.png")

        ui.press("验证配置")
        validation = wait(current_validation)
        identifier = validation["validation"]

        def active_validation():
            status = call("processing_validation", processing_validation_id=identifier)
            progress = status.get("live_progress") or {}
            events = progress.get("events") or {}
            return status if status["validation"]["state"] == "validating" and progress.get("process_started") and events.get("message_start", 0) else None

        wait(active_validation, timeout=120)
        ui.press("取消验证")
        cancelled = wait(lambda: (lambda value: value if value["validation"]["state"] == "cancelled" else None)(call("processing_validation", processing_validation_id=identifier)))
        wait(lambda: "验证已取消" in ui.labels())
        capture("04-cancelled-system.png")
        assert cancelled["live_progress"]["process_reaped"] and cancelled["live_progress"]["process_group_gone"]

        ui.press("验证配置")
        succeeded_id = wait(lambda: (lambda value: value if value and value["validation"] != identifier else None)(current_validation()))["validation"]
        succeeded = wait(lambda: (lambda value: value if value["validation"]["state"] == "succeeded" else None)(call("processing_validation", processing_validation_id=succeeded_id)))
        wait(lambda: "已保存并验证，可启用" in ui.labels())
        capture("05-verified-system.png")
        assert "disabled" in ui.node(key="first-launch-complete").get("states", [])

        ui.press("设为当前使用")
        wait(lambda: call("processing_get")["current"]["active"])
        wait(lambda: "当前正在使用" in ui.labels())
        assert "disabled" not in ui.node(key="first-launch-complete").get("states", [])
        capture("06-active-system.png")

        if args.fidelity:
            labels = ui.labels()
            assert not any(any(term in label for term in ("当前生产边界", "终态回收", "整理能力验证")) for label in labels)
            resize(980, 620)
            assert_fixed_actions(["设为当前使用", "保存配置", "验证配置", "完成配置"], 980, 620)
            capture("06-active-narrow-system.png")
            resize(1200, 820)

        ui.press_key("first-launch-complete")
        wait(lambda: not any(node.get("key") == "first-launch-complete" for node in ui.nodes()))
        state = json.loads((client / "first-launch.json").read_text())
        assert state["completed"] and not state.get("skipped") and state["step"] == "processing"
        completed_counts = {
            kind: call("list", list_query={"kind": kind})["total"]
            for kind in ("knowledge", "raw")
        }
        report["post_completion_record_counts"] = completed_counts
        assert completed_counts == baseline_counts
        capture("07-completed-home-system.png")
        if args.fidelity:
            wait(lambda: all(label in ui.labels() for label in ("累计记忆", "知识", "使用天数", "首次使用日期未知")))
            assert "可选设置指引" not in ui.labels()
            summary = call("activities", timezone="Asia/Shanghai")
            assert summary["raw_total"] == completed_counts["raw"] and summary["knowledge_total"] == completed_counts["knowledge"]
            resize(980, 620)
            assert_fixed_actions(["管理计划"], 980, 620)
            capture("08-home-narrow-system.png")
            resize(1200, 820)
            ui.press("设置")
            ui.press("记忆处理")
            wait(lambda: "当前正在使用" in ui.labels())
            assert "总览" not in ui.labels()
            capture("09-settings-active-system.png")
            resize(980, 620)
            assert_fixed_actions(["设为当前使用", "取消修改", "保存配置", "验证配置"], 980, 620)
            capture("10-settings-narrow-system.png")
            resize(1200, 820)
            ui.press("LLM provider")
            assert "LLM provider 暂不可用" in ui.labels()
            capture("11-provider-unavailable-system.png")
            ui.press("使用 Magpie")
            assert "Magpie 暂不可用" in ui.labels()
            ui.press("Agent 连接")
            wait(lambda: any(node.get("key") == "connection-card-pi" for node in ui.nodes()))
            capture("12-agent-settings-system.png")
            report["fidelity_checks"] = {"actual_narrow_system_screenshots": True, "fixed_actions_visible": True,
                "shared_settings_sidebar": True, "implementation_details_removed": True, "real_home_counts": summary,
                "unknown_first_use_preserved": True, "unavailable_backends_not_enabled": True}
        report.update({
            "status": "passed", "agent_step_real_connection": True,
            "processing_page_reused_ledger": True, "completion_blocked_before_activation": True,
            "save_via_onboarding_ui": True, "active_cancel_via_onboarding_ui": True,
            "validation_via_onboarding_ui": True, "explicit_activation_via_onboarding_ui": True,
            "completion_via_onboarding_ui": True, "onboarding_record_delta_zero": True,
            "cancelled_validation": cancelled, "successful_validation": succeeded,
            "scope": "Native 三步初始化中的真实 pi 保存、活动取消、重验、显式启用与完成配置；记录数按真实连接验证后的基线核对零增量；环境准备沿用已验收状态但本次运行真实 ready，不代表 Provider、Codex、Magpie 或电脑级批量连接通过",
        })
    except Exception as error:
        report["failure"] = repr(error)
        if ui is not None:
            try:
                capture("failure-system.png")
            except Exception as capture_error:
                report["failure_capture_error"] = repr(capture_error)
    finally:
        if ui is not None:
            try:
                ui.quit()
            except Exception as error:
                report["cleanup_errors"].append("ui: " + repr(error))
        if service_registered:
            try:
                if ipc.exists():
                    call("stop")
                report["service_unregistration"] = app_action("unregister")
                service_registered = False
            except Exception as error:
                report["cleanup_errors"].append("service: " + repr(error))
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
        if installed and connection is not None and options is not None:
            try:
                adapter("uninstall", connection=connection, options=options)
            except Exception as error:
                report["cleanup_errors"].append("adapter: " + repr(error))
        for linked in (pi_directory / "models.json", pi_directory / "auth.json"):
            if linked.is_symlink():
                linked.unlink()
        deadline = time.monotonic() + 15
        while ipc.exists() and time.monotonic() < deadline:
            time.sleep(.1)
        report["user_agent_config_unchanged"] = all(path.is_file() and digest(path) == original_digests[path.name] for path in source_files)
        report["credential_links_removed"] = not (pi_directory / "models.json").exists() and not (pi_directory / "auth.json").exists()
        report["socket_absent"] = not ipc.exists()
        report["locks_released"] = {}
        for name, path in {"service": data / ".service.lock", "worker": data / "index/.worker.lock"}.items():
            released = False
            deadline = time.monotonic() + 15
            while time.monotonic() < deadline:
                try:
                    with path.open("a+") as handle:
                        fcntl.flock(handle.fileno(), fcntl.LOCK_EX | fcntl.LOCK_NB)
                        released = True
                    break
                except OSError:
                    time.sleep(.1)
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
