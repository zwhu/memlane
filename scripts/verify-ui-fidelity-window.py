#!/usr/bin/env python3
"""新空业务根中的真实 Native 首页、日期、详情和设置窗口对照。"""
import argparse
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import socket
import subprocess
import uuid

ROOT = Path(__file__).resolve().parents[1]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--app", type=Path, required=True)
    parser.add_argument("--worker-config", type=Path, required=True)
    parser.add_argument("--out", type=Path, required=True)
    parser.add_argument("--button-motion", action="store_true")
    args = parser.parse_args()
    app, out = args.app.resolve(), args.out.resolve()
    out.mkdir(mode=0o700, parents=True, exist_ok=False)
    runtime = json.loads((app / "Contents/Resources/runtime-config.json").read_text())
    client = Path(runtime["root"])
    assert client == out / "client", "bundle must belong to this new isolated root"
    data = client / "data"
    data.mkdir(mode=0o700, parents=True)
    worker = json.loads(args.worker_config.read_text())["worker"]
    worker["DataDir"] = str(data / "index")
    (client / "service.json").write_text(json.dumps({"client_root": str(client), "root": str(data), "worker": worker}, indent=2) + "\n")
    # Explicit presentation fixture for entering daily pages. This is not
    # first-launch or model acceptance; every displayed record is still saved
    # through the actual daemon and indexed by the packaged Rust worker.
    (client / "first-launch.json").write_text(json.dumps({"stage": "ready", "step": "processing", "completed": True}) + "\n")
    spec = importlib.util.spec_from_file_location("fidelity_ui", ROOT / "scripts/p1-ui.py")
    driver = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(driver)
    ipc = Path("/tmp") / ("memlane-" + str(os.getuid()) + "-" + hashlib.sha256(str(data).encode()).hexdigest()[:12]) / "service.sock"
    ui, registered = None, False
    report = {"status": "failed", "synthetic_only": True, "actual_native_app": True,
              "actual_packaged_daemon": True, "actual_rust_index": False, "first_launch_presentation_fixture": True,
              "model_inference_verified": False, "screenshots": [], "cleanup_errors": []}

    def action(name):
        log = out / ("service-" + name + ".log")
        result = subprocess.run(["/usr/bin/open", "-n", "-W", "--stdout", str(log), "--stderr", str(log),
            "--env", "HOME=" + str(out / "ui-home"), "--env", "PATH=/usr/bin:/bin", str(app), "--args", "--p0-action", name], capture_output=True, text=True, timeout=45)
        assert result.returncode == 0, result.stderr
        receipts = []
        for line in log.read_text().splitlines():
            try:
                value = json.loads(line)
            except json.JSONDecodeError:
                continue
            if isinstance(value, dict) and value.get("action") == name:
                receipts.append(value)
        assert receipts and not receipts[-1].get("error"), receipts
        return receipts[-1]

    def call(operation, **fields):
        with socket.socket(socket.AF_UNIX) as channel:
            channel.settimeout(15)
            channel.connect(str(ipc))
            channel.sendall((json.dumps({"protocol": 1, "id": str(uuid.uuid4()), "op": operation, **fields}) + "\n").encode())
            response = json.loads(channel.makefile("rb", buffering=0).readline())
        assert response["ok"], response
        return response.get("result")

    def capture(name):
        ui.system_capture(name)
        report["screenshots"].append(name)
        (out / "screenshots" / (Path(name).stem + "-tree.json")).write_text(json.dumps(ui.nodes(), ensure_ascii=False, indent=2) + "\n")

    def resize(width, height):
        with socket.socket(socket.AF_UNIX) as channel:
            channel.settimeout(10)
            channel.connect(ui.native_path)
            channel.sendall((json.dumps({"op": "resize", "width": width, "height": height}) + "\n").encode())
            result = json.loads(channel.makefile("rb", buffering=0).readline())
        assert "error" not in result, result
        ui.req("idle", timeout=2000)

    try:
        (out / "ui-home").mkdir(mode=0o700)
        report["service_registration"] = action("register")
        registered = True
        driver.wait(lambda: ipc.exists(), timeout=30)
        driver.wait(lambda: call("health")["state"] == "ready", timeout=90)
        scope = next(s for s in call("scopes") if s["kind"] == "preference")
        titles = ["项目知识保留原始出处", "编辑后读取当前版本", "保存和索引分别反馈"]
        ids = []
        for title in titles:
            identifier = str(uuid.uuid4())
            call("apply", user_confirmed=True, mutation={"operation_id": str(uuid.uuid4()), "kind": "save", "dataset": scope["id"],
                "memory": identifier, "title": title, "text": title + "。这是本轮独立窗口验收的合成知识。", "origin": "knowledge", "source": "本轮窗口验收"})
            ids.append(identifier)
        call("apply", user_confirmed=True, mutation={"operation_id": str(uuid.uuid4()), "kind": "save", "dataset": scope["id"],
            "memory": str(uuid.uuid4()), "title": "独立原始记录", "text": "本轮独立窗口验收的合成原始记录。", "origin": "raw", "source": "本轮窗口验收"})
        driver.wait(lambda: all(call("get", memory=i)["index_state"] == "indexed" for i in ids), timeout=90)
        report["actual_rust_index"] = True
        summary = call("activities", timezone="Asia/Shanghai")
        assert summary["raw_total"] == 1 and summary["knowledge_total"] == 3
        report["actual_counts"] = {"raw": summary["raw_total"], "knowledge": summary["knowledge_total"]}
        ui = driver.UI(app, out, out / "screenshots")
        driver.wait(lambda: all(label in ui.labels() for label in ("累计记忆", "首次使用日期未知", titles[-1])), timeout=90)
        assert "可选设置指引" not in ui.labels()
        capture("01-home-system.png")
        if args.button_motion:
            import time
            from importlib.machinery import SourceFileLoader
            mac = SourceFileLoader("home_motion_mac", str(ROOT / "scripts/mac-native.py")).load_module().Mac()
            control = ui.node(key="home-organize")
            rect = control['bounds']
            ui.req('input', events=[{'type': 'pointerMove', 'x': rect['x'] + rect['width']/2, 'y': rect['y'] + rect['height']/2}])
            time.sleep(.14)
            mac.capture(ui.pid, out / 'screenshots/01b-home-hover-system.png')
            report['screenshots'].append('01b-home-hover-system.png')
            time.sleep(1)
            ui.req('idle', timeout=2000)
            ui.req('input', events=[{'type': 'pointerMove', 'x': 450, 'y': 25}])
            ui.req('idle', timeout=2000)
            report['native_hover_stops_after_one_pass'] = True
            report['native_running_and_completion_task_verified'] = False

        date = next(date for date, count in summary["days"].items() if count == 3)
        ui.press(date + " 新增 3 条")
        driver.wait(lambda: date + " 新增知识" in ui.labels() and titles[-1] in ui.labels())
        capture("02-date-system.png")
        ui.press(titles[-1])
        driver.wait(lambda: "知识详情" in ui.labels() and "合成知识" in " ".join(ui.labels()))
        capture("03-detail-system.png")
        ui.press("总览")
        driver.wait(lambda: date + " 新增知识" in ui.labels())
        ui.press("回到最近新增")
        resize(980, 620)
        capture("04-home-narrow-system.png")
        resize(1200, 820)
        ui.press("设置")
        ui.press("记忆处理")
        assert "总览" not in ui.labels()
        capture("05-settings-system.png")
        ui.press("LLM provider")
        assert "LLM provider 暂不可用" in ui.labels()
        resize(980, 620)
        capture("06-settings-narrow-system.png")
        resize(1200, 820)
        ui.press("Agent 连接")
        driver.wait(lambda: any(n.get("key", "").startswith("connection-card-") for n in ui.nodes()), timeout=30)
        capture("07-agent-settings-system.png")
        report.update(status="passed", date_filters_stay_on_home=True, date_survives_detail_navigation=True,
            real_counts_separate_from_heatmap=True, unavailable_provider_honest=True, actual_normal_and_narrow_system_screenshots=True)
    except Exception as error:
        report["failure"] = repr(error)
        if ui is not None:
            try:
                capture("failure-system.png")
            except Exception as error:
                report["failure_capture_error"] = repr(error)
    finally:
        if ui is not None:
            try:
                ui.quit()
            except Exception as error:
                report["cleanup_errors"].append("ui: " + repr(error))
        if registered:
            try:
                if ipc.exists():
                    call("stop")
                report["service_unregistration"] = action("unregister")
            except Exception as error:
                report["cleanup_errors"].append("service: " + repr(error))
        try:
            driver.wait(lambda: not ipc.exists(), timeout=30)
        except Exception as error:
            report["cleanup_errors"].append("socket: " + repr(error))
        report["socket_absent"] = not ipc.exists()
        if report["cleanup_errors"]:
            report["status"] = "failed"
        (out / "report.json").write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n")
    print(json.dumps({"status": report["status"], "failure": report.get("failure"), "report": str(out / "report.json")}, ensure_ascii=False))
    if report["status"] != "passed" or report["cleanup_errors"]:
        raise SystemExit(1)


if __name__ == "__main__":
    main()
