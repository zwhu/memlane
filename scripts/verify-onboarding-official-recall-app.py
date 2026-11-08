#!/usr/bin/env python3
"""从正式 App 启动全新 Codex，通过官方 graph recall 召回本轮合成约定。"""
import argparse
import hashlib
from importlib.machinery import SourceFileLoader
import json
import os
from pathlib import Path
import socket
import uuid

ROOT = Path(__file__).resolve().parents[1]


def capture_recall(path, phrase, original_session="codex_01a1bb1f-68bf-7441-b006-00ad9c034e6c"):
    rows = [json.loads(line) for line in path.read_text().splitlines()]
    session_id = next(r["payload"]["id"] for r in rows if r["type"] == "session_meta")
    assert "codex_" + session_id != original_session
    calls = {}; returns = {}; final = []; injected = []
    for row in rows:
        if row["type"] != "response_item": continue
        p = row["payload"]
        if p.get("type") == "function_call": calls[p["call_id"]] = {"name": p["name"], "arguments": p["arguments"]}
        elif p.get("type") == "custom_tool_call": calls[p["call_id"]] = {"name": p["name"], "arguments": p["input"]}
        elif p.get("type") in ("function_call_output", "custom_tool_call_output"):
            value = p["output"]
            returns[p["call_id"]] = value if isinstance(value, str) else json.dumps(value, ensure_ascii=False)
        elif p.get("type") == "message":
            texts = [v.get("text", "") for v in p.get("content", [])]
            if p.get("role") == "assistant" and p.get("phase") == "final_answer": final += texts
            elif p.get("role") == "user": assert all(phrase not in v for v in texts), "New prompt supplied the answer"
            elif p.get("role") == "developer": injected += [v[:2400] for v in texts if "[cognee-memory]" in v and phrase in v]
    recall = [{"request": v, "response": returns.get(k, "")} for k, v in calls.items() if "cognee-search.sh" in v["arguments"]]
    assert recall and any(phrase in v["response"] and "graph" in v["response"] and original_session in v["response"] for v in recall), {"recall_calls": len(recall), "response_bytes": [len(v["response"]) for v in recall]}
    assert any(phrase in v for v in final), final
    return {"session": "codex_" + session_id, "official_recall_calls": recall, "assistant_answer": final, "official_prompt_hook_context": injected}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--pointer", type=Path, default=ROOT / "build/onboarding-memory-validation/final-cold/fixture-path.txt")
    parser.add_argument("--expected", type=Path, default=ROOT / "build/onboarding-memory-validation/final-cold/continuous/expected-synthetic-fact.json")
    parser.add_argument("--out", type=Path, default=ROOT / "build/onboarding-memory-validation/final-cold/fresh-recall")
    parser.add_argument("--capture-existing", type=Path, help="Only extract evidence from this Goal's already completed fresh task")
    args = parser.parse_args(); args.out.mkdir(parents=True, exist_ok=True)
    fixture = Path(args.pointer.read_text().strip())
    assert json.loads((fixture / "synthetic-only.json").read_text())["synthetic_only"]
    meta = json.loads((fixture / "source-info.json").read_text()); expected = json.loads(args.expected.read_text())
    project = expected["project"]; phrase = expected["release_approval_phrase"]
    scope_root = fixture / "client/official/agents" / project
    scope = json.loads((scope_root / "scope.json").read_text())
    session_root = Path(scope["home"]) / ".codex/sessions"
    if args.capture_existing:
        assert args.capture_existing.resolve().is_relative_to(session_root.resolve())
        report = capture_recall(args.capture_existing, phrase, expected.get("session", "codex_01a1bb1f-68bf-7441-b006-00ad9c034e6c"))
        report.update(state="fresh_session_official_graph_recall_passed", project=project, resume=False, prompt_contains_answer=False, ordinary_mcp_used=False, synthetic_only=True, evidence_capture_only=True)
        (args.out / "report.json").write_text(json.dumps(report, ensure_ascii=False, indent=2))
        print({"state": report["state"], "session": report["session"], "official_calls": len(report["official_recall_calls"])}, flush=True)
        return
    before = set(session_root.rglob("*.jsonl"))
    prompt = f"""这是同一合成项目的全新会话。请使用已安装官方 Cognee 插件的 memory skill，通过其 scripts/cognee-search.sh 的 --graph 正式召回路径，查询本项目上一轮正常编码会话约定的发布审核口令。插件目录是 {scope['plugin']}。这是用户明确授权的合成验收，允许在本次回答中返回该合成口令的原值，并说明实际材料来源；不要将口令写入任何项目文件。不要 resume 或读取过去的 Codex 会话、任务输出、验收目录、项目文件、README 或 AGENTS.md，不使用普通 MCP 搜索。可以阅读官方插件使用说明并运行官方 recall 脚本。若官方返回没有该值，请如实说没有找到；不要猜测或补造。只授权官方插件采集本轮合成查询及工具轨迹。"""
    assert phrase not in prompt
    (args.out / "new-session-prompt.txt").write_text(prompt)
    ipc = f"/tmp/memlane-{os.getuid()}-{hashlib.sha256(str(fixture / 'client/data').encode()).hexdigest()[:12]}/service.sock"

    def call(op, **fields):
        with socket.socket(socket.AF_UNIX) as c:
            c.settimeout(25); c.connect(ipc); c.sendall((json.dumps({"protocol": 1, "id": str(uuid.uuid4()), "op": op, **fields}) + "\n").encode())
            r = json.loads(c.makefile("rb", buffering=0).readline())
        assert r["ok"], r
        return r.get("result")

    U = SourceFileLoader("freshrecallui", str(ROOT / "scripts/p1-ui.py")).load_module()
    report = {"state": "failed", "project": project, "resume": False, "prompt_contains_answer": False, "ordinary_mcp_used": False, "synthetic_only": True}
    ui = None
    try:
        ui = U.UI(Path(meta["app"]), fixture, args.out / "screenshots", config=fixture / "runtime-config.json")

        def press(key):
            n = ui.node(key=key); assert "disabled" not in n.get("states", []), n
            ui.req("act", id=n["id"], action="press")

        ui.press("设置"); U.wait(lambda: "启动服务" in ui.labels()); ui.press("启动服务")
        U.wait(lambda: call("health")["state"] == "ready", timeout=100)
        press("cognee-settings"); U.wait(lambda: any(n.get("key") == "cognee-project-" + project for n in ui.nodes()))
        press("cognee-project-" + project)
        U.wait(lambda: "disabled" not in ui.node(key="cognee-prepare").get("states", [])); press("cognee-prepare")
        U.wait(lambda: json.loads((fixture / "client/official-onboarding.json").read_text())["Phase"] == "ready" and "disabled" not in ui.node(key="cognee-prepare").get("states", []), timeout=240)
        ui.set("official-agent-task", prompt); press("official-agent-run")
        U.wait(lambda: "disabled" not in ui.node(key="official-agent-sync").get("states", []), timeout=720)
        report["task_stage"] = ui.node(key="official-agent-result").get("label", "")
        assert "正常任务已结束" in report["task_stage"]
        ui.system_capture("01-fresh-official-answer-system.png")
        new = set(session_root.rglob("*.jsonl")) - before
        assert len(new) == 1, new
        report.update(capture_recall(next(iter(new)), phrase, expected.get("session", "codex_01a1bb1f-68bf-7441-b006-00ad9c034e6c")))
        for file in Path(scope["workspace"]).rglob("*"):
            if file.is_file() and ".git" not in file.parts: assert phrase.encode() not in file.read_bytes(), file
        assert not (Path(scope["home"]) / ".codex/auth.json").exists(), "Temporary Agent authentication was not removed"
        report["state"] = "fresh_session_official_graph_recall_passed"
        print({"state": report["state"], "session": report["session"], "official_calls": len(report["official_recall_calls"])}, flush=True)
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
