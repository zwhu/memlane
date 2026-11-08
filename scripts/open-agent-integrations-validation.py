#!/usr/bin/env python3
"""打开或停止本轮保留的真实合成记忆 App；不启动 Agent 或聊天采集。"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import plistlib
import re
import signal
import socket
import subprocess
import time
import uuid
from importlib.machinery import SourceFileLoader

ROOT = Path(__file__).resolve().parents[1]
EVIDENCE = ROOT / "build/agent-integrations-validation"


def validate_app(app, fixture, config, identifier):
    app = Path(app).resolve()
    app.relative_to(EVIDENCE.resolve())
    bundle = plistlib.loads((app / "Contents/Info.plist").read_bytes())
    if bundle["CFBundleIdentifier"] != identifier or json.loads((app / "Contents/Resources/runtime-config.json").read_text()) != config:
        raise ValueError("App 与本轮合成环境身份不一致")
    return app, (app / "Contents/MacOS" / bundle["CFBundleExecutable"]).resolve()


def environment():
    fixture = Path((EVIDENCE / "fixture-path.txt").read_text().strip()).resolve()
    marker = json.loads((fixture / "synthetic-only.json").read_text())
    if not fixture.name.startswith("memlane-official-agent-") or marker.get("synthetic_only") is not True:
        raise ValueError("需要本轮明确的合成验证根")
    info = json.loads((fixture / "source-info.json").read_text())
    if not info["identifier"].startswith("io.github.zwhu.memlane.officialagent."):
        raise ValueError("需要本轮独立 App 身份")
    config = json.loads((fixture / "runtime-config.json").read_text())
    if Path(config["root"]).resolve() != fixture / "client":
        raise ValueError("App 数据根必须属于本轮合成目录")
    for relative in ("client", "client/data", "ui-home"):
        path = fixture / relative
        if path.is_symlink() or path.resolve() != path:
            raise ValueError("合成目录不能通过符号链接指向其他环境")
    database = fixture / "client/data/memlane.sqlite"
    if database.is_symlink() or not database.is_file() or database.stat().st_size == 0:
        raise ValueError("缺少本轮已经验证的真实记录")
    service_file = fixture / "client/service.json"
    if service_file.is_symlink():
        raise ValueError("服务配置不能是外部链接")
    service = json.loads(service_file.read_text())
    if Path(service["root"]).resolve() != fixture / "client/data" or not Path(service["worker"]["DataDir"]).resolve().is_relative_to(fixture / "client"):
        raise ValueError("服务和引擎数据必须属于同一个合成根")
    app, _ = validate_app(info["app"], fixture, config, info["identifier"])
    return fixture, app, config


def process_matches(pid, binary):
    result = subprocess.run(["ps", "-p", str(int(pid)), "-o", "comm="], capture_output=True, text=True)
    return result.returncode == 0 and result.stdout.strip() == str(binary)


def package_pids(binary):
    result = subprocess.run(["pgrep", "-f", "^" + re.escape(str(binary)) + "$"], capture_output=True, text=True)
    return {int(value) for value in result.stdout.split()}


def stop_service(config):
    data = Path(config["root"]) / "data"
    service = json.loads((Path(config["root"]) / "service.json").read_text())
    address = "/tmp/memlane-" + str(os.getuid()) + "-" + hashlib.sha256(service["root"].encode()).hexdigest()[:12] + "/service.sock"
    if Path(address).exists():
        with socket.socket(socket.AF_UNIX) as connection:
            connection.settimeout(20)
            connection.connect(address)
            connection.sendall((json.dumps({"protocol": 1, "id": str(uuid.uuid4()), "op": "stop"}) + "\n").encode())
            response = json.loads(connection.makefile("rb", buffering=0).readline())
        if not response["ok"]:
            raise RuntimeError(response)
    if json.loads((data / "service-state.json").read_text()).get("stopped") is not True:
        raise RuntimeError("未取得本轮服务的持久停止事实")


def stop_instance(state, fixture, config, stop_memory=True):
    identifier = json.loads((fixture / "source-info.json").read_text())["identifier"]
    _, binary = validate_app(state["app"], fixture, config, identifier)
    pid = int(state["pid"])
    if process_matches(pid, binary):
        if not stop_memory:
            os.kill(pid, signal.SIGTERM)
            return
        address = Path(state["socket"])
        if not str(address).startswith("/tmp/memlane-p1-") or address.is_symlink():
            raise ValueError("窗口控制地址不属于本轮实例")
        if address.is_socket():
            with socket.socket(socket.AF_UNIX) as connection:
                connection.settimeout(20)
                connection.connect(str(address))
                connection.sendall((json.dumps({"id": 1, "method": "menu", "params": {"path": ["文件", "停止服务并退出"]}}) + "\n").encode())
                line = connection.makefile("rb", buffering=0).readline()
            if line and "error" in json.loads(line):
                raise RuntimeError("实际窗口拒绝了停止菜单请求")
        else:
            stop_service(config)
            if process_matches(pid, binary):
                os.kill(pid, signal.SIGTERM)
        deadline = time.monotonic() + 30
        while process_matches(pid, binary) and time.monotonic() < deadline:
            time.sleep(0.1)
        if process_matches(pid, binary):
            raise RuntimeError("本轮 App 尚未确认退出")
    if stop_memory:
        stop_service(config)


def window_state(ui, app):
    state = {"app": str(app), "pid": ui.pid, "socket": ui.path, "native_socket": getattr(ui, "native_path", "")}
    return state


def write_state(path, ui, app):
    state = window_state(ui, app)
    path.write_text(json.dumps(state, indent=2))
    path.chmod(0o600)
    return state


def attach(driver, state, app, out):
    ui = driver.UI.__new__(driver.UI)
    ui.out, ui.pid, ui.path, ui.seq = out, int(state["pid"]), state["socket"], 0
    ui.native_path = state.get("native_socket", "")
    ui.name = plistlib.loads((app / "Contents/Info.plist").read_bytes())["CFBundleName"]
    ui.log = (out / "window.log").open("a")
    ui.s = socket.socket(socket.AF_UNIX)
    ui.s.settimeout(30)
    ui.s.connect(ui.path)
    ui.f = ui.s.makefile("rb", buffering=0)
    return ui


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("action", nargs="?", choices=("open", "stop"), default="open")
    args = parser.parse_args()
    fixture, app, config = environment()
    state_file = EVIDENCE / "experience-window.json"
    state = json.loads(state_file.read_text()) if state_file.exists() else None
    if args.action == "stop":
        if state:
            stop_instance(state, fixture, config)
        else:
            stop_service(config)
        print("本轮 Memlane 服务已停止，验证 App 已退出；合成记录和验证包保留。")
        return
    driver = SourceFileLoader("official_experience_ui", str(ROOT / "scripts/p1-ui.py")).load_module()
    out = EVIDENCE / "experience"
    out.mkdir(exist_ok=True)
    _, binary = validate_app(app, fixture, config, json.loads((fixture / "source-info.json").read_text())["identifier"])
    before = package_pids(binary)
    was_running = json.loads((Path(config["root"]) / "data/service-state.json").read_text()).get("stopped") is False
    ui, created = None, False
    try:
        if state and state["app"] == str(app) and process_matches(state["pid"], binary):
            ui = attach(driver, state, app, out)
        else:
            if state:
                stop_instance(state, fixture, config)
            created = True
            ui = driver.UI.__new__(driver.UI)
            ui.__init__(app, fixture, out)
        write_state(state_file, ui, app)
        ui.req("menu", path=["文件", "打开 Memlane"])
        ui.press("设置")
        driver.wait(lambda: "启动服务" in ui.labels() or "停止服务" in ui.labels(), timeout=95)
        if "启动服务" in ui.labels():
            ui.press("启动服务")
        driver.wait(lambda: any("服务运行中" in label for label in ui.labels()), timeout=95)
        ui.press("全部记忆")
        driver.wait(lambda: any("Cognee 提炼" in label for label in ui.labels()))
    except BaseException:
        if created:
            if ui is not None and hasattr(ui, "pid"):
                stop_instance(window_state(ui, app), fixture, config, stop_memory=not was_running)
            else:
                started = package_pids(binary) - before
                try:
                    if started and not was_running:
                        stop_service(config)
                finally:
                    for pid in started:
                        if process_matches(pid, binary):
                            os.kill(pid, signal.SIGTERM)
        raise
    finally:
        if ui is not None:
            for attribute in ("f", "s", "log"):
                resource = getattr(ui, attribute, None)
                if resource is not None:
                    resource.close()
    print("已打开含真实记录的隔离 App，服务已启动。进入原始记录或选择知识查看出处、范围与索引。")


if __name__ == "__main__":
    main()
