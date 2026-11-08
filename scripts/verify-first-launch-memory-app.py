#!/usr/bin/env python3
"""本轮独立 App 的空根首启验收；真实下载/服务，不代替官方 Agent 主线。"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import socket
import subprocess
import sys
import time
import uuid
from importlib.machinery import SourceFileLoader

ROOT = Path(__file__).resolve().parents[1]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--pointer", type=Path, default=ROOT / "build/onboarding-memory-validation/fixture-path.txt")
    parser.add_argument("--out", type=Path, default=ROOT / "build/first-launch-validation")
    args = parser.parse_args()
    fixture = Path(args.pointer.read_text().strip())
    assert fixture.name.startswith("memlane-official-agent-onboarding-")
    assert json.loads((fixture / "synthetic-only.json").read_text())["synthetic_only"] is True
    meta = json.loads((fixture / "source-info.json").read_text())
    config = json.loads((fixture / "runtime-config.json").read_text())
    assert config["root"] == str(fixture / "client")
    app = Path(meta["app"])
    args.out.mkdir(parents=True, exist_ok=True)
    U = SourceFileLoader("firstmemoryui", str(ROOT / "scripts/p1-ui.py")).load_module()
    data = fixture / "client/data"
    ipc = f"/tmp/memlane-{os.getuid()}-{hashlib.sha256(str(data).encode()).hexdigest()[:12]}/service.sock"

    def call(op, **fields):
        with socket.socket(socket.AF_UNIX) as connection:
            connection.settimeout(6)
            connection.connect(ipc)
            connection.sendall((json.dumps({"protocol": 1, "id": str(uuid.uuid4()), "op": op, **fields}) + "\n").encode())
            response = json.loads(connection.makefile("rb", buffering=0).readline())
        assert response["ok"], response
        return response.get("result")

    ui = None
    report = {"state": "failed", "actual_app": str(app), "fixture": str(fixture),
              "business_root_empty": not (data / "memlane.sqlite").exists(),
              "client_cache_empty": not (fixture / "client/runtime").exists(),
              "official_agent_validation": "not_run_by_this_script", "cases": []}
    try:
        assert report["business_root_empty"] and report["client_cache_empty"], "This first-run check requires an untouched business/cache root"
        ui = U.UI(app, fixture, args.out / "screenshots")
        U.wait(lambda: any(node.get("key") == "first-launch-confirmation" for node in ui.nodes()), timeout=55)
        assert not (data / "memlane.sqlite").exists(), "Discovery started the business service without confirmation"
        state = json.loads((fixture / "client/first-launch.json").read_text())
        assert state["stage"] == "results" and not state.get("completed")
        ui.system_capture("discovery-system.png")
        report["cases"].append({"case": "actual_discovery_no_service_start", "state": state})
        print("实际发现完成，业务根仍为空。", flush=True)
        ui.press_key("first-launch-confirmation")
        ui.system_capture("confirmation-system.png")
        # Preparing deliberately animates until a real terminal state. A
        # generic automation 'idle' deadline is not a preparation failure.
        button = ui.node(key="first-launch-prepare")
        ui.req("act", id=button["id"], action="press")
        print("已在隔离 App 确认准备，等待实际下载、校验与服务验证。", flush=True)
        U.wait(lambda: json.loads((fixture / "client/first-launch.json").read_text())["stage"] in ("ready", "failed"), timeout=150)
        state = json.loads((fixture / "client/first-launch.json").read_text())
        report["preparation_result"] = state
        if state["stage"] != "ready":
            ui.system_capture("preparation-failure-system.png")
            raise AssertionError(("real preparation did not pass", state["message"]))
        assert call("health")["state"] == "ready"
        assert call("list", list_query={"kind": "knowledge"})["total"] == 0
        assert call("list", list_query={"kind": "raw"})["total"] == 0
        assert not call("connections")
        ui.system_capture("ready-blocks-system.png")
        report["cases"].append({"case": "real_components_helper_rust_verified_no_preloaded_memory", "state": state})
        ui.press_key("first-launch-use")
        U.wait(lambda: "添加本地项目" in ui.labels())
        assert json.loads((fixture / "client/first-launch.json").read_text())["completed"]
        ui.system_capture("first-project-system.png")
        ui.quit(stop=True)
        ui = None
        report["state"] = "local_first_launch_passed"
        report["limits"] = ["Local signed real worker/model archives are served by a controlled source, not public distribution.",
                            "Cognee/official plugin preparation and normal Agent task/recall are separate required Goal checks."]
    except Exception as error:
        report["error"] = repr(error)
        raise
    finally:
        report["cleanup"] = []
        if ui is not None:
            try:
                ui.quit(stop=True)
            except Exception as error:
                report["cleanup"].append(repr(error))
        (args.out / "actual-first-launch.json").write_text(json.dumps(report, ensure_ascii=False, indent=2))
        print(json.dumps({"state": report["state"], "error": report.get("error"), "cleanup": report["cleanup"]}, ensure_ascii=False), flush=True)


if __name__ == "__main__":
    main()
