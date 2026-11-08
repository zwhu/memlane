#!/usr/bin/env python3
"""实际本轮官方服务暂停/恢复、导入账本重放与双项目权限；无HTTP成功夹具。"""
import argparse
import hashlib
from importlib.machinery import SourceFileLoader
import json
import os
from pathlib import Path
import socket
import sqlite3
import subprocess
import urllib.error
import urllib.request
import uuid

ROOT = Path(__file__).resolve().parents[1]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--pointer", type=Path, default=ROOT / "build/onboarding-memory-validation/final-cold/fixture-path.txt")
    parser.add_argument("--out", type=Path, default=ROOT / "build/onboarding-memory-validation/final-cold/service-boundaries")
    args = parser.parse_args(); args.out.mkdir(parents=True, exist_ok=True)
    assert args.out.resolve().is_relative_to((ROOT / "build").resolve())
    root = Path(args.pointer.read_text().strip())
    assert json.loads((root / "synthetic-only.json").read_text())["synthetic_only"]
    meta = json.loads((root / "source-info.json").read_text())
    ipc = f"/tmp/memlane-{os.getuid()}-{hashlib.sha256(str(root / 'client/data').encode()).hexdigest()[:12]}/service.sock"

    def response(op, **fields):
        with socket.socket(socket.AF_UNIX) as c:
            c.settimeout(25); c.connect(ipc)
            c.sendall((json.dumps({"protocol": 1, "id": str(uuid.uuid4()), "op": op, **fields}) + "\n").encode())
            return json.loads(c.makefile("rb", buffering=0).readline())

    def call(op, **fields):
        r = response(op, **fields); assert r["ok"], r
        return r.get("result")

    def ledger():
        # Read-only, consistent SQLite snapshot; no knowledge or activity is
        # injected. UI usage observations belong to a separate table.
        with sqlite3.connect(f"file:{root / 'client/data/memlane.sqlite'}?mode=ro", uri=True) as db:
            db.execute("BEGIN")
            result = {}
            for table in ("memories", "versions", "operations", "activities", "organization_sources", "cognee_bindings"):
                rows = db.execute("SELECT * FROM " + table).fetchall()
                encoded = json.dumps(sorted(rows, key=str), ensure_ascii=False, separators=(",", ":"), default=str).encode()
                result[table] = {"count": len(rows), "sha256": hashlib.sha256(encoded).hexdigest()}
            return result

    U = SourceFileLoader("serviceboundaryui", str(ROOT / "scripts/p1-ui.py")).load_module()
    report = {"state": "failed", "synthetic_only": True, "http_fixture": False, "fault_injection": "intentional ownership-checked real Cognee process stop", "intervention": "Stop only this owned real Cognee process; Memlane remains running"}
    ui = None
    try:
        ui = U.UI(Path(meta["app"]), root, args.out / "screenshots", config=root / "runtime-config.json")
        def press(key):
            n = ui.node(key=key); assert "disabled" not in n.get("states", []), n
            ui.req("act", id=n["id"], action="press")
        ui.press("设置"); U.wait(lambda: "启动服务" in ui.labels()); ui.press("启动服务")
        U.wait(lambda: call("health")["state"] == "ready", timeout=100)
        a = next(p for p in call("scopes") if p["name"] == "连续合成项目 A")
        b = next(p for p in call("scopes") if p["name"] == "连续合成项目 B")
        press("cognee-settings"); U.wait(lambda: any(n.get("key") == "cognee-project-" + a["id"] for n in ui.nodes())); press("cognee-project-" + a["id"])
        U.wait(lambda: "disabled" not in ui.node(key="cognee-prepare").get("states", [])); press("cognee-prepare")
        U.wait(lambda: json.loads((root / "client/official-onboarding.json").read_text())["Phase"] == "ready" and "disabled" not in ui.node(key="cognee-prepare").get("states", []), timeout=240)
        binding_a = call("cognee_bindings", scope=a["id"])[0]
        binding_b = call("cognee_bindings", scope=b["id"])[0]
        retired = response("agent", agent_request={"protocol_version": 1, "connection_id": binding_b["connection_id"], "generation": binding_b["generation"], "workspace": binding_b["workspace"], "tool": "memory_status", "arguments": {"scope": b["id"]}})
        assert not retired["ok"] and retired.get("code") in ("disconnected", "generation_retired"), retired
        report["retired_valid_agent_request_denied"] = retired
        cancel_report = ROOT / "build/onboarding-memory-validation/final-cold/cancel-accepted/report.json"
        if not cancel_report.exists():
            cancel_report = ROOT / "build/onboarding-memory-validation/final-cold/actual-cancel-interrupt/report.json"
        cancelled = json.loads(cancel_report.read_text())["cancelled_session"]
        token_b = (root / "client/official/agents" / b["id"] / "cognee-access.token").read_text().strip()
        opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
        request = urllib.request.Request(binding_b["endpoint"] + "/api/v1/memlane/datasets/" + binding_b["dataset"] + "/sessions/" + cancelled + "?expected_instance=" + binding_b["instance"], headers={"X-Api-Key": token_b})
        try:
            with opener.open(request, timeout=20) as r: material = json.load(r)
            report["official_cancelled_session"] = {"session": cancelled, "state": material["state"], "persisted_qa": material["persisted_qa"], "persisted_trace": material["persisted_trace"], "raw_count": len(material["raw"]), "documents": len(material["documents"])}
        except urllib.error.HTTPError as error:
            report["official_cancelled_session"] = {"session": cancelled, "http_status": error.code, "persistence_acknowledged": False}
        session = "codex_01a1bb1f-68bf-7441-b006-00ad9c034e6c"
        ui.set("cognee-session", session)
        before = ledger(); report["ledger_before"] = before
        # This invokes the real ownership-checked process stop contract;
        # it neither uses a success fixture nor deletes the official data.
        stop_code = args.out / "stop-owned-official.go"
        stop_code.write_text('''package main
import ("context";"os";"time";"github.com/zwhu/memlane/internal/official")
func main(){ctx,cancel:=context.WithTimeout(context.Background(),30*time.Second);defer cancel();c:=official.Controller{Root:os.Args[1]};if err:=c.Stop(ctx);err!=nil{panic(err)}}
''')
        try:
            subprocess.run(["sh", "scripts/go.sh", "run", str(stop_code.resolve()), str(root / "client/official")], cwd=ROOT, check=True, timeout=45)
        finally:
            stop_code.rename(args.out / "stop-owned-official.go.txt")
        assert call("health")["state"] == "ready"
        press("cognee-sync"); U.wait(lambda: "disabled" not in ui.node(key="cognee-sync").get("states", []), timeout=45)
        message = ui.node(key="cognee-result").get("label", "")
        assert "同步未完成" in message and "HTTP 200" not in message, message
        report["actual_service_unavailable_message"] = message
        assert ledger() == before
        ui.system_capture("01-real-cognee-unavailable-system.png")
        press("cognee-prepare")
        U.wait(lambda: json.loads((root / "client/official-onboarding.json").read_text())["Phase"] == "ready" and "disabled" not in ui.node(key="cognee-prepare").get("states", []), timeout=240)
        press("cognee-sync"); U.wait(lambda: "disabled" not in ui.node(key="cognee-sync").get("states", []), timeout=330)
        report["recovered_sync_message"] = ui.node(key="cognee-result").get("label", "")
        assert "Memlane 已持久化" in report["recovered_sync_message"]
        after = ledger(); assert after == before; report["ledger_after"] = after
        ui.system_capture("02-real-service-recovered-replay-system.png")
        token_a = (root / "client/official/agents" / a["id"] / "cognee-access.token").read_text().strip()
        opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
        request = urllib.request.Request(binding_a["endpoint"] + "/api/v1/datasets/" + binding_b["dataset"] + "/schema", headers={"X-Api-Key": token_a})
        try:
            with opener.open(request, timeout=20) as r: r.read(1)
        except urllib.error.HTTPError as error:
            assert error.code in (403, 404), error.code
            report["official_project_a_cannot_read_b_status"] = error.code
        else: raise AssertionError("Official principal A could read dataset B")
        wrong = response("cognee_sync", user_confirmed=True, cognee_sync={"project": a["id"], "dataset": binding_b["dataset"], "session": session})
        assert not wrong["ok"]; report["cross_project_sync_denied"] = wrong
        assert ledger() == before
        report["state"] = "real_service_failure_recovery_replay_and_project_authorization_passed"
        print({"state": report["state"], "ledger_counts": {k: v["count"] for k, v in after.items()}}, flush=True)
    except Exception as error:
        report["error"] = repr(error)
        if ui:
            try: ui.system_capture("failure-system.png")
            except Exception: pass
        raise
    finally:
        if ui:
            try: ui.quit(stop=True)
            except Exception as error: report["cleanup_error"] = repr(error)
        (args.out / "report.json").write_text(json.dumps(report, ensure_ascii=False, indent=2))


if __name__ == "__main__":
    main()
