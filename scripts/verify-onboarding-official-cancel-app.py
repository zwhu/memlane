#!/usr/bin/env python3
"""项目 B 的真实 Agent 活动取消与授权撤销；不以故障夹具代替实际取消。"""
import argparse
import hashlib
from importlib.machinery import SourceFileLoader
import json
import os
from pathlib import Path
import socket
import subprocess
import time
import urllib.request
import uuid

ROOT = Path(__file__).resolve().parents[1]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--pointer", type=Path, default=ROOT / "build/onboarding-memory-validation/final-cold/fixture-path.txt")
    parser.add_argument("--out", type=Path, default=ROOT / "build/onboarding-memory-validation/final-cold/actual-cancel")
    args = parser.parse_args(); args.out.mkdir(parents=True, exist_ok=True)
    fixture = Path(args.pointer.read_text().strip())
    assert json.loads((fixture / "synthetic-only.json").read_text())["synthetic_only"]
    meta = json.loads((fixture / "source-info.json").read_text())
    ipc = f"/tmp/memlane-{os.getuid()}-{hashlib.sha256(str(fixture / 'client/data').encode()).hexdigest()[:12]}/service.sock"

    def ipc_response(op, **fields):
        with socket.socket(socket.AF_UNIX) as c:
            c.settimeout(25); c.connect(ipc); c.sendall((json.dumps({"protocol": 1, "id": str(uuid.uuid4()), "op": op, **fields}) + "\n").encode())
            return json.loads(c.makefile("rb", buffering=0).readline())

    def call(op, **fields):
        r = ipc_response(op, **fields); assert r["ok"], r
        return r.get("result")

    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    def official(endpoint, path, token, payload=None):
        request = urllib.request.Request(endpoint + path, headers={"X-Api-Key": token, "Content-Type": "application/json"}, data=None if payload is None else json.dumps(payload).encode())
        with opener.open(request, timeout=20) as r: return json.load(r)

    U = SourceFileLoader("officialcancelui", str(ROOT / "scripts/p1-ui.py")).load_module()
    report = {"state": "failed", "synthetic_only": True, "fault_injection": False}; ui = None
    try:
        ui = U.UI(Path(meta["app"]), fixture, args.out / "screenshots", config=fixture / "runtime-config.json")
        def press(key):
            n = ui.node(key=key); assert "disabled" not in n.get("states", []), n; ui.req("act", id=n["id"], action="press")
        ui.press("设置"); U.wait(lambda: "启动服务" in ui.labels()); ui.press("启动服务")
        U.wait(lambda: call("health")["state"] == "ready", timeout=100)
        project = next(p for p in call("scopes") if p["name"] == "连续合成项目 B")
        report["project"] = project
        before = call("list", list_query={"datasets": [project["id"]]}); assert before["total"] == 0
        press("cognee-settings"); U.wait(lambda: any(n.get("key") == "cognee-project-" + project["id"] for n in ui.nodes())); press("cognee-project-" + project["id"])
        U.wait(lambda: "disabled" not in ui.node(key="cognee-prepare").get("states", [])); press("cognee-prepare")
        U.wait(lambda: json.loads((fixture / "client/official-onboarding.json").read_text())["Phase"] == "ready" and "disabled" not in ui.node(key="cognee-prepare").get("states", []), timeout=240)
        if any(n.get("key") == "official-agent-prepare" for n in ui.nodes()):
            ui.set("official-agent-auth", "/Users/bytedance/.codex"); press("official-agent-prepare")
            U.wait(lambda: any(n.get("key") == "official-agent-task" for n in ui.nodes()), timeout=130)
        agent_root = fixture / "client/official/agents" / project["id"]
        scope = json.loads((agent_root / "scope.json").read_text())
        token = (agent_root / "cognee-access.token").read_text().strip()
        state = json.loads((fixture / "client/official/cognee-service-state.json").read_text())
        datasets = official(state["endpoint"], "/api/v1/datasets", token)
        dataset = next((v for v in datasets if v["name"] == scope["dataset_name"]), None)
        if dataset is None:
            # Real official scope creation during explicitly approved isolated
            # setup. No QA, trace, knowledge or graph is supplied here.
            dataset = official(state["endpoint"], "/api/v1/datasets", token, {"name": scope["dataset_name"]})
        report["dataset"] = dataset["id"]
        bindings = call("cognee_bindings", scope=project["id"])
        if not bindings:
            ui.set("cognee-endpoint", state["endpoint"]); ui.set("cognee-dataset", dataset["id"]); ui.set("cognee-credential", token); press("cognee-authorize")
        # Sync correctly stays disabled until a real session exists. The
        # credential-review action, not that button, marks completed binding.
        U.wait(lambda: any(n.get("key") == "cognee-refresh-auth" for n in ui.nodes()))
        binding = call("cognee_bindings", scope=project["id"])[0]; report["binding"] = binding
        report["memlane_authorization_enabled_before_task"] = next(v["enabled"] for v in call("connections") if v["id"] == binding["connection_id"])
        if not report["memlane_authorization_enabled_before_task"]:
            assert any("接入已撤销" in label for label in ui.labels())
            assert "disabled" in ui.node(key="cognee-sync").get("states", [])
            report["reopened_retired_scope_is_explicitly_restricted"] = True
        marker = "memlane-cancel-probe-" + uuid.uuid4().hex
        prompt = f"""这是连续合成项目 B 的正常编码任务。请实现 safe_divide(a,b)，处理除数为零并编写 unittest。正式编码前先用工具执行 python3 -c 'import time; print(\"{marker}\", flush=True); time.sleep(90)'，这是用户明确要求的可中断准备步骤。然后实现函数并运行测试。只处理本项目，不读其他项目或历史会话。仅授权官方 Cognee 插件采集本轮合成对话与工具轨迹。"""
        ui.set("official-agent-task", prompt); press("official-agent-run")
        def probe_pids():
            # Read only matches to this unique synthetic marker; never report
            # unrelated process command lines.
            result = subprocess.run(["pgrep", "-f", marker], capture_output=True, text=True)
            values = []
            for raw_pid in result.stdout.split():
                pid = int(raw_pid)
                command = subprocess.run(["/bin/ps", "-ww", "-p", str(pid), "-o", "command="], capture_output=True, text=True).stdout
                executable = command.split(maxsplit=1)[0] if command.strip() else ""
                if " -c " in command and "time.sleep(90)" in command and "python" in Path(executable).name.lower():
                    values.append(pid)
            return values
        pids = U.wait(probe_pids, timeout=150); report["actual_probe_pids"] = pids
        report["actual_probe_commands"] = [subprocess.check_output(["/bin/ps", "-ww", "-p", str(pid), "-o", "command="]).decode().strip() for pid in pids]
        ui.system_capture("01-live-agent-task-before-cancel-system.png")
        cancelled_at = time.monotonic()
        press("official-agent-cancel")
        U.wait(lambda: not probe_pids(), timeout=20)
        report["actual_cancel_seconds"] = round(time.monotonic() - cancelled_at, 2)
        U.wait(lambda: any(v["id"] == binding["connection_id"] and not v["enabled"] for v in call("connections")), timeout=40)
        U.wait(lambda: not (Path(scope["home"]) / ".codex/auth.json").exists(), timeout=30)
        ui.system_capture("02-actual-cancel-revoked-system.png")
        after = call("list", list_query={"datasets": [project["id"]]}); assert after["total"] == before["total"] == 0
        session_files = sorted((Path(scope["home"]) / ".codex/sessions").rglob("*.jsonl"), key=lambda p: p.stat().st_mtime)
        rows = [json.loads(v) for v in session_files[-1].read_text().splitlines()]
        session_id = "codex_" + next(v["payload"]["id"] for v in rows if v["type"] == "session_meta")
        report["cancelled_session"] = session_id
        old = ipc_response("cognee_sync", user_confirmed=True, cognee_sync={"project": project["id"], "dataset": dataset["id"], "session": session_id})
        assert not old["ok"], old; report["late_memlane_sync_denied"] = old
        old_agent = ipc_response("agent", agent_request={"protocol_version": 1, "connection_id": binding["connection_id"], "generation": binding["generation"], "workspace": binding["workspace"], "tool": "memory_status", "arguments": {"scope": project["id"]}})
        assert not old_agent["ok"] and old_agent.get("code") in ("disconnected", "generation_retired"), old_agent
        report["retired_agent_access_denied"] = old_agent
        try:
            material = official(state["endpoint"], "/api/v1/memlane/datasets/" + dataset["id"] + "/sessions/" + session_id + "?expected_instance=" + binding["instance"], token)
            report["official_after_cancel"] = {k: v for k, v in material.items() if k not in ("raw", "documents")}
            report["official_after_cancel"].update(raw_records=len(material["raw"]), learning_documents=len(material["documents"]))
        except Exception as error: report["official_after_cancel"] = {"read_error_type": type(error).__name__, "http_status": getattr(error, "code", None), "persistence_acknowledged": False}
        report["state"] = "actual_agent_cancel_and_retired_scope_passed"
        print({"state": report["state"], "project": project["id"], "session": session_id, "memlane_records": after["total"]}, flush=True)
    except Exception as error:
        report["error"] = repr(error)
        if ui is not None:
            try: ui.system_capture("failure-system.png")
            except Exception: pass
        raise
    finally:
        if ui is not None:
            try: ui.quit(stop=True)
            except Exception as error: report["cleanup_error"] = repr(error)
        (args.out / "report.json").write_text(json.dumps(report, ensure_ascii=False, indent=2))


if __name__ == "__main__":
    main()
