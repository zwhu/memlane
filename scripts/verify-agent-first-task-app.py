#!/usr/bin/env python3
"""通过新完整App走Agent优先/跳过项目/真实官方任务/当前知识召回。"""
import argparse
import hashlib
from importlib.machinery import SourceFileLoader
import json
import os
from pathlib import Path
import re
import socket
import subprocess
import urllib.error
import urllib.request
import uuid

ROOT = Path(__file__).resolve().parents[1]


def confirm_existing(args, fixture, out):
    """Re-evaluate the completed real run; never rerun Agent or mutate data."""
    original = json.loads((out / "report.json").read_text())
    assert original["status"] == "failed" and "did not call the current-knowledge reader" in original.get("error", "")
    edited = next(c for c in original["cases"] if c["name"] == "app-edit-current-version")
    project = next(c["project"] for c in original["cases"] if c["name"] == "first-effective-material-created-project")
    state = (out / "screenshots/07-user-current-version-state.json").read_text()
    values = set(re.findall(r"linden-current-[a-f0-9]{12}", state))
    assert len(values) == 1
    expected = next(iter(values))
    sessions = sorted((fixture / "client/official/agents" / project["id"] / "home/.codex/sessions").rglob("*.jsonl"))
    assert len(sessions) == 2, "This correction requires the two already executed real sessions"
    events = [json.loads(line) for line in sessions[-1].read_text().splitlines()]
    payloads = [v.get("payload", {}) for v in events]
    requests = [v for v in payloads if v.get("type") in ("function_call", "custom_tool_call") and "memory_" in json.dumps(v, ensure_ascii=False)]
    ids = {v.get("call_id") for v in requests}
    returns = [v for v in payloads if v.get("type") in ("function_call_output", "custom_tool_call_output") and v.get("call_id") in ids]
    assert requests and returns

    def walk(value):
        if isinstance(value, dict):
            yield value
            for child in value.values():
                yield from walk(child)
        elif isinstance(value, list):
            for child in value:
                yield from walk(child)
        elif isinstance(value, str):
            try:
                parsed = json.loads(value)
            except (ValueError, TypeError):
                return
            if not isinstance(parsed, str):
                yield from walk(parsed)

    versions = [v for response in returns for v in walk(response)
                if v.get("memory") == edited["memory"] and v.get("revision") == 2 and v.get("dataset") == project["id"] and expected in v.get("text", "")]
    assert versions, "the actual current reader did not return this project's edited v2"
    answers = [v for v in payloads if v.get("type") == "message" and v.get("role") == "assistant" and expected in json.dumps(v, ensure_ascii=False)]
    assert answers
    questions = [v for v in payloads if v.get("type") == "message" and v.get("role") == "user"]
    assert questions and all(expected not in json.dumps(v, ensure_ascii=False) for v in questions)
    assert original.get("socket_exists_after_stop") is False
    corrected = {**original, "status": "main_flow_passed", "driver_failure_preserved": str(out / "report.json"),
                 "driver_correction": "同时解析function_call和custom_tool_call；只复核已完成的真实会话，没有重新运行任务或构造回执",
                 "current_version_evidence": {"session": str(sessions[-1]), "memory_calls": requests,
                                              "actual_returns": returns, "matched_v2": versions[0],
                                              "assistant_answers": answers, "question_contains_answer": False,
                                              "new_session": True, "resume": False},
                 "scope": "Codex官方采集＋Memlane当前知识读取；不宣称独立官方graph自动回写App编辑"}
    corrected["cases"] = list(original["cases"]) + [{"name": "new-agent-read-user-maintained-current-version", "memory": edited["memory"], "revision": 2, "actual_tool_returns_verified": True}]
    corrected.pop("error", None)
    (out / "report-corrected.json").write_text(json.dumps(corrected, ensure_ascii=False, indent=2))
    print(json.dumps({"status": corrected["status"], "actual_current_calls": len(requests), "memory": edited["memory"], "revision": 2, "original_driver_failure_preserved": True}, ensure_ascii=False))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--pointer", type=Path, required=True)
    parser.add_argument("--out", type=Path, required=True)
    parser.add_argument("--auth-directory", type=Path, required=True)
    parser.add_argument("--capture-existing", action="store_true", help="修正旧事件解析误报，仅核验已完成的两次真实会话")
    parser.add_argument("--pi-bridge",action="store_true",help="核对无云模型Key初始化及真实 pi 提炼请求")
    parser.add_argument("--pi-select-in-app", action="store_true", help="初始化时不配置模型桥，在实际界面明确选择 pi")
    parser.add_argument("--pi-lifecycle-only", action="store_true", help="仅验证空环境、界面选择、活动取消与明确重试")
    parser.add_argument("--pi-cancel-check", action="store_true", help="在真实采集任务前验证活动推理取消与重试")
    parser.add_argument("--regression-task", action="store_true", help="让真实 Agent 复现并修复合成仓库的既有规范化缺陷")
    parser.add_argument("--require-full-schema", action="store_true", help="核对 Cognee 已显式发送完整结构化 Schema")
    parser.add_argument("--codex-executable",type=Path,help="本轮已核对版本的真实采集端可执行文件")
    args = parser.parse_args()
    assert not args.pi_select_in_app or args.pi_bridge
    assert not args.pi_lifecycle_only or args.pi_select_in_app
    assert not args.pi_cancel_check or args.pi_select_in_app
    out = args.out.resolve()
    out.mkdir(parents=True, exist_ok=True)
    fixture = Path(args.pointer.read_text().strip())
    assert json.loads((fixture / "synthetic-only.json").read_text())["synthetic_only"]
    if args.capture_existing:
        confirm_existing(args, fixture, out)
        return
    meta = json.loads((fixture / "source-info.json").read_text())
    app = Path(meta["app"])
    data = fixture / "client/data"
    ipc = f"/tmp/memlane-{os.getuid()}-{hashlib.sha256(str(data).encode()).hexdigest()[:12]}/service.sock"
    U = SourceFileLoader("firsttaskui", str(ROOT / "scripts/p1-ui.py")).load_module()
    report = {"status": "running", "app": str(app), "fixture": str(fixture),
              "synthetic_only": True, "cases": [], "distribution_in_scope": False}
    ui = None
    selected_pi = None

    def bridge_rows():
        path = fixture / "client/official/model-bridge/events.jsonl"
        return [json.loads(line) for line in path.read_text().splitlines()] if path.exists() else []

    def cancel_active_verification():
        offset = len(bridge_rows())
        press("cognee-prepare")
        cancel_node = ui.node(key="cognee-prepare-cancel")
        assert "disabled" not in cancel_node.get("states", [])
        def streaming():
            rows = bridge_rows()[offset:]
            finished = {row["request_id"] for row in rows if "http_status" in row}
            return next((row["request_id"] for row in rows
                         if row.get("event") == "message_update" and row["request_id"] not in finished), None)
        request_id = U.wait(streaming, timeout=100)
        ui.req("act", id=cancel_node["id"], action="press")
        def cancelled():
            rows = bridge_rows()[offset:]
            receipt = next((row for row in rows if row.get("request_id") == request_id and "http_status" in row), None)
            state = json.loads((fixture / "client/official-onboarding.json").read_text())
            return receipt if receipt and state.get("Phase") == "cancelled" else None
        receipt = U.wait(cancelled, timeout=25)
        assert receipt["http_status"] == 408 and receipt["abort_sent"] and receipt["abort_acknowledged"]
        assert receipt["reaped"] and receipt["group_gone"]
        settings = json.loads((fixture / "client/official/cognee-config.json").read_text())
        assert settings["model_restart_required"]
        state = json.loads((fixture / "client/official/model-bridge/state.json").read_text())
        token = (fixture / "client/official/model-bridge/access.token").read_text()
        # The control credential is local. The paused gate rejects before
        # starting another pi process or forwarding any model material.
        request = urllib.request.Request(state["endpoint"] + "/v1/chat/completions", b"{}",
                                         headers={"Authorization": "Bearer " + token, "Content-Type": "application/json"})
        before = len(bridge_rows())
        try:
            urllib.request.urlopen(request, timeout=5).close()
            raise AssertionError("cancelled bridge admitted another inference")
        except urllib.error.HTTPError as error:
            assert error.code == 401 and json.load(error)["error"]["code"] == "bridge_cancelled"
        assert len(bridge_rows()) == before
        assert call("list", list_query={"kind": "knowledge"})["total"] == 0
        assert call("list", list_query={"kind": "raw"})["total"] == 0
        capture("04-active-pi-verification-cancelled")
        case("app-cancelled-real-streaming-pi-and-rejected-late-inference", receipt=receipt)

    def save():
        (out / "report.json").write_text(json.dumps(report, ensure_ascii=False, indent=2))

    def call(op, **fields):
        with socket.socket(socket.AF_UNIX) as channel:
            channel.settimeout(30)
            channel.connect(ipc)
            channel.sendall((json.dumps({"protocol": 1, "id": str(uuid.uuid4()), "op": op, **fields}) + "\n").encode())
            response = json.loads(channel.makefile("rb", buffering=0).readline())
        assert response["ok"], response
        return response.get("result")

    def case(name, **fields):
        report["cases"].append({"name": name, **fields})
        save()
        print(json.dumps({"case": name, "status": "passed"}), flush=True)

    def press(key):
        node = ui.node(key=key)
        assert "disabled" not in node.get("states", []), node
        ui.req("act", id=node["id"], action="press")

    def capture(name):
        ui.system_capture(name + ".png")
        (out / "screenshots" / (name + "-state.json")).write_text(json.dumps(ui.native(), ensure_ascii=False, indent=2))

    def wait_task():
        def finished():
            nodes = ui.nodes()
            failure = next((n.get("label", "") for n in nodes if n.get("key") in ("official-agent-result", "cognee-result") and any(v in n.get("label", "") for v in ("任务未完成", "同步未完成", "授权未完成", "接入未完成", "未接通"))), "")
            if failure:
                raise RuntimeError(failure)
            count = next((n for n in nodes if n.get("key") == "first-task-memory-counts"), None)
            run = next((n for n in nodes if n.get("key") == "official-agent-run"), None)
            return count if count and run and "disabled" not in run.get("states", []) else None
        return U.wait(finished, timeout=1000)

    def new_current_read(question, expected):
        assert expected not in question
        agent = json.loads((fixture / "client/official/agents" / project["id"] / "scope.json").read_text())
        sessions = Path(agent["home"]) / ".codex/sessions"
        before = set(sessions.rglob("*.jsonl"))
        ui.set("official-agent-task", question)
        press("official-agent-run")
        wait_task()
        new = set(sessions.rglob("*.jsonl")) - before
        assert len(new) == 1, [str(p) for p in new]
        events = [json.loads(line) for line in next(iter(new)).read_text().splitlines()]
        calls = []
        for event in events:
            payload = event.get("payload", {})
            if payload.get("type") in ("function_call", "function_call_output", "custom_tool_call", "custom_tool_call_output"):
                calls.append(payload)
        memory_calls = [p for p in calls if p.get("type") in ("function_call", "custom_tool_call") and "memory_" in json.dumps(p, ensure_ascii=False)]
        assert memory_calls, "new Agent session did not call the current-knowledge reader"
        called = {p.get("call_id") for p in memory_calls}
        responses = [p for p in calls if p.get("type") in ("function_call_output", "custom_tool_call_output") and p.get("call_id") in called]
        assert any(expected in json.dumps(p, ensure_ascii=False) for p in responses), "current reader did not return the expected value"
        assert expected in ui.node(key="official-agent-result").get("label", "") or expected in "\n".join(ui.labels())
        return {"new_session_file": str(next(iter(new))), "memory_calls": memory_calls, "current_responses": responses,
                "question_contains_answer": False, "resume": False}

    try:
        assert not (data / "memlane.sqlite").exists()
        if meta.get("component_files_only_reused"):
            assert not (fixture / "client/runtime/feed-cache.json").exists()
            assert not (fixture / "client/official/runtime/feed-cache.json").exists()
            assert not (fixture / "client/official/cognee").exists()
            report["compatible_component_files_reused"] = True
        else:
            assert not (fixture / "client/runtime").exists()
            assert not (fixture / "client/official/runtime").exists()
        extra_env=None
        if args.codex_executable:
            extra_env={"PATH":str(args.codex_executable.resolve().parent)+":/usr/bin:/bin"}
            if args.pi_bridge:
                settings=json.loads((fixture/"client/official/cognee-config.json").read_text())
                extra_env["PATH"]=str(Path(settings["model_bridge"]["executable"]).parent)+":"+extra_env["PATH"]
        if args.pi_select_in_app:
            path = fixture / "client/official/cognee-config.json"
            settings = json.loads(path.read_text())
            selected_pi = settings.pop("model_bridge")
            settings.pop("model_restart_required", None)
            for key in ("LLM_API_KEY", "LLM_MODEL", "LLM_PROVIDER", "LLM_ENDPOINT"):
                settings["environment"].pop(key, None)
            path.write_text(json.dumps(settings))
        ui = U.UI(app, fixture, out / "screenshots", config=fixture / "runtime-config.json",extra_env=extra_env)
        U.wait(lambda: any(n.get("key") == "first-launch-confirmation" for n in ui.nodes()), timeout=70)
        press("first-launch-confirmation")
        press("first-launch-prepare")
        def initialized():
            state=fixture/"client/first-launch.json"
            if state.exists():
                status=json.loads(state.read_text())
                if status.get("stage")=="failed":raise RuntimeError(status.get("message"))
                if status.get("stage")!="ready":return False
            mac=SourceFileLoader("bridgefocus",str(ROOT/"scripts/mac-native.py")).load_module().Mac()
            mac.require_unlocked();mac.activate(ui.pid)
            return any(n.get("key")=="first-launch-use" for n in ui.nodes())
        U.wait(initialized, timeout=320 if args.pi_bridge else 220)
        assert call("health")["state"] == "ready" and len(call("scopes")) == 2
        if args.pi_bridge:
            settings=json.loads((fixture/"client/official/cognee-config.json").read_text())
            if args.pi_select_in_app:
                assert not settings.get("model_bridge") and not settings["environment"].get("LLM_API_KEY")
                assert not (fixture / "client/official/model-bridge/state.json").exists()
                case("app-unified-initialization-without-selected-agent-or-model-key")
            else:
                assert settings.get("model_bridge") and settings["environment"]["LLM_API_KEY"]==(fixture/"client/official/model-bridge/access.token").read_text()
                assert settings["environment"]["LLM_ENDPOINT"].startswith("http://127.0.0.1:")
            records=fixture/"client/official/model-bridge/events.jsonl"
            assert not records.exists() or not any("http_status" in json.loads(line) for line in records.read_text().splitlines())
            case("unified-services-ready-without-cloud-model-key-or-inference")
        capture("01-ready-agent-first")
        press("first-launch-use")
        U.wait(lambda: any(n.get("key") == "connect-agent" for n in ui.nodes()))
        assert not any(n.get("key") == "project-name" for n in ui.nodes())
        press("agent-codex")
        capture("02-agent-project-optional")
        press("connect-agent")
        U.wait(lambda: any(n.get("key") == "official-workspace-confirm" for n in ui.nodes()), timeout=50)
        connection = call("connections")[0]
        assert connection.get("auto_project") and not connection.get("project") and not connection.get("pending_project")
        assert len(call("scopes")) == 2
        case("connect-without-project-or-workspace", connection=connection)
        ui.set("official-agent-workspace", str(fixture / "repo-a"))
        press("official-workspace-confirm")
        U.wait(lambda: any(n.get("key") == "cognee-prepare" for n in ui.nodes()), timeout=45)
        pending = call("connections")[0]
        assert pending.get("pending_project") and not pending.get("project") and len(call("scopes")) == 2
        capture("03-workspace-approved-no-empty-project")
        case("workspace-approved-without-registering-project", connection=pending)
        if args.pi_select_in_app:
            assert "disabled" in ui.node(key="cognee-prepare").get("states", [])
            ui.set("pi-model-auth", selected_pi["auth_home"])
            ui.set("pi-model-provider", selected_pi["provider"])
            ui.set("pi-model-name", selected_pi["model"])
            press("pi-model-save")
            U.wait(lambda: json.loads((fixture / "client/official/cognee-config.json").read_text()).get("model_bridge"), timeout=15)
            settings = json.loads((fixture / "client/official/cognee-config.json").read_text())
            assert settings["model_bridge"]["provider"] == selected_pi["provider"]
            assert settings["model_bridge"]["model"] == selected_pi["model"]
            assert not settings["environment"].get("LLM_API_KEY") and not bridge_rows()
            case("actual-app-selected-pi-without-cloud-key-or-inference")
        if args.pi_lifecycle_only or args.pi_cancel_check:
            cancel_active_verification()
        press("cognee-prepare")
        def inference_ready():
            status_path=fixture/"client/official-onboarding.json"
            if args.pi_bridge and status_path.exists():
                status=json.loads(status_path.read_text())
                if status.get("Phase")=="failed":raise RuntimeError(status.get("Message"))
                if status.get("Phase")!="ready":return False
            mac=SourceFileLoader("bridgefocusmodel",str(ROOT/"scripts/mac-native.py")).load_module().Mac()
            mac.require_unlocked();mac.activate(ui.pid)
            return any(n.get("key")=="official-agent-prepare" for n in ui.nodes())
        U.wait(inference_ready, timeout=450)
        assert len(call("scopes")) == 2 and call("list", list_query={"kind": "raw"})["total"] == 0
        if args.pi_lifecycle_only:
            successful = [row for row in bridge_rows() if row.get("http_status") == 200]
            assert successful and all(row["terminal"] and row["reaped"] and row["group_gone"] for row in successful)
            assert not json.loads((fixture / "client/official/cognee-config.json").read_text()).get("model_restart_required")
            capture("05-explicit-pi-retry-verified")
            case("explicit-app-retry-restarted-and-verified-pi-without-memory-write", receipts=successful)
            report["status"] = "pi_lifecycle_passed"
            report["scope"] = "当前完整包空环境统一初始化、界面明确选择pi、真实流式推理取消与重试；不代替官方采集闭环。"
            return
        if args.codex_executable:
            ui.set("official-agent-executable", str(args.codex_executable.resolve()))
        ui.set("official-agent-auth", str(args.auth_directory.resolve()))
        press("official-agent-prepare")
        def capture_agent_ready():
            nodes = ui.nodes()
            failure = next((n.get("label", "") for n in nodes
                            if n.get("key") == "official-agent-result"
                            and "接入未完成" in n.get("label", "")), "")
            if failure:
                raise RuntimeError(failure)
            return any(n.get("key") == "official-agent-task" for n in nodes)
        U.wait(capture_agent_ready, timeout=150)
        assert len(call("scopes")) == 2
        capture("04-plugin-ready-no-project")
        case("actual-official-preparation-without-project")
        model_requests_before_task=0
        if args.pi_bridge:
            rows=[json.loads(line) for line in (fixture/"client/official/model-bridge/events.jsonl").read_text().splitlines()]
            model_requests_before_task=sum(row.get("http_status")==200 for row in rows)
            assert model_requests_before_task>0
            if args.require_full_schema:
                generic = hashlib.sha256(b'{"type":"object"}').hexdigest()
                assert all(row["schema_sha256"] != generic for row in rows if row.get("http_status") == 200)
                case("actual-cognee-transmitted-full-response-schema")
            case("explicit-real-pi-model-and-local-embedding-verification",model_requests=model_requests_before_task)
        original = "linden-gate-" + uuid.uuid4().hex[:12]
        task = f"""这是本轮新建合成仓库的正常编码任务。项目发布审核口令为 {original}，只属于本轮会话约定，不写进代码、测试、README、AGENTS.md或其他文件。请实现 Python normalize_ticket(text)：仅接受str，其他类型抛TypeError；去掉两端空白，连续空白变为短横线，英文字母转小写。用标准库写几个unittest并实际运行。最后用中文报告结果并确认你知道审核口令。按官方 Cognee 插件正常采集本轮会话与工具轨迹，不手工调用接口注入记忆，不读其他项目或验收目录。"""
        if args.regression_task:
            workspace = fixture / "repo-a"
            assert not (workspace / "ticket.py").exists()
            (workspace / "ticket.py").write_text('def normalize_ticket(text):\n    return text.lower()\n')
            (workspace / "test_ticket.py").write_text('import unittest\nfrom ticket import normalize_ticket\n\nclass TicketTests(unittest.TestCase):\n    def test_normalization(self):\n        self.assertEqual(normalize_ticket("  A\\t B  "), "a-b")\n    def test_type(self):\n        with self.assertRaises(TypeError):\n            normalize_ticket(42)\n\nif __name__ == "__main__":\n    unittest.main()\n')
            task = f"""请修复当前合成仓库 ticket.py 的 normalize_ticket 缺陷。既有 test_ticket.py 定义了项目约定：仅接受 str，其他类型抛 TypeError；去掉两端空白，连续空白变为短横线，英文字母转小写。请先实际运行 python3 -m unittest 复现失败，再修复实现并重跑全部测试，不削弱现有断言。发布前都应运行这条检查。最后中文报告实际失败原因与修复结果。本轮项目审核口令为 {original}，不写到仓库任何文件。只授权官方 Cognee 插件采集这次正常修复会话与工具轨迹，不手工注入记忆，不访问其他项目或验收目录。"""
        (out / "expected-synthetic-fact.json").write_text(json.dumps({"original": original}))
        ui.set("official-agent-task", task)
        press("official-agent-run")
        wait_task()
        projects = [p for p in call("scopes") if p["kind"] == "project"]
        assert len(projects) == 1 and projects[0]["id"] == pending["pending_project"]
        project = projects[0]
        knowledge = call("list", list_query={"kind": "knowledge", "datasets": [project["id"]]})
        raw = call("list", list_query={"kind": "raw", "datasets": [project["id"]]})
        assert knowledge["total"] > 0 and raw["total"] > 0
        U.wait(lambda: all(call("get", memory=v["memory"])["index_state"] == "indexed" for v in knowledge["items"]), timeout=100)
        assert call("connections")[0].get("project") == project["id"]
        assert (fixture / "client/official/agents" / project["id"] / "current-knowledge.json").exists()
        for p in (fixture / "repo-a").rglob("*"):
            if p.is_file() and ".git" not in p.parts:
                assert original.encode() not in p.read_bytes(), str(p)
        capture("05-task-material-and-project")
        case("first-effective-material-created-project", project=project, knowledge=knowledge, raw=raw)
        if args.pi_bridge:
            rows=[json.loads(line) for line in (fixture/"client/official/model-bridge/events.jsonl").read_text().splitlines()]
            after=sum(row.get("http_status")==200 for row in rows)
            assert after>model_requests_before_task
            case("official-task-triggered-real-pi-distillation",model_requests=after-model_requests_before_task)
        press("cognee-browse")
        U.wait(lambda: any(n.get("key") == "browse-tab-memories" for n in ui.nodes()))
        target = next((v for v in knowledge["items"] if original in v["text"]), knowledge["items"][0])
        press("memory-" + target["memory"])
        U.wait(lambda: any(n.get("key", "").startswith("source-reader-open-") for n in ui.nodes()))
        source = next(n for n in ui.nodes() if n.get("key", "").startswith("source-reader-open-"))
        press(source["key"])
        U.wait(lambda: any(n.get("key") == "source-reader-copy" for n in ui.nodes()))
        capture("06-full-original-reader")
        press("source-reader-close")
        # The explicit user edit becomes current knowledge. The historical
        # official graph retains the original fact and is not written back.
        revised = "linden-current-" + uuid.uuid4().hex[:12]
        ui.press("编辑")
        ui.set("memory-draft", f"本项目发布前先运行 python3 -m unittest；当前审核口令为 {revised}。这条约定由用户在App中明确修订，旧口令已失效。")
        ui.press("保存")
        U.wait(lambda: call("get", memory=target["memory"])["revision"] == 2 and call("get", memory=target["memory"])["index_state"] == "indexed", timeout=100)
        capture("07-user-current-version")
        case("app-edit-current-version", memory=target["memory"], revision=2)
        ui.press("Agent")
        U.wait(lambda: any(n.get("key") == "connection-" + pending["id"] for n in ui.nodes()))
        press("connection-" + pending["id"])
        press("agent-first-task")
        U.wait(lambda: any(n.get("key") == "official-agent-task" for n in ui.nodes()))
        question = "请先使用Memlane当前知识工具查询我在App里修订的发布审核约定，回答现在应该运行什么检查、使用什么审核口令，并说明当前版本和出处。不要读取仓库文件、过去的Agent会话或验收目录，不resume，不猜测。只授权官方插件采集这次查询及工具轨迹。"
        current = new_current_read(question, revised)
        capture("08-new-agent-session-current-recall")
        case("new-agent-read-user-maintained-current-version", evidence=current)
        if args.pi_bridge:
            receipts=[json.loads(line) for line in (fixture/"client/official/model-bridge/events.jsonl").read_text().splitlines()]
            completed=[row for row in receipts if row.get("http_status")==200]
            assert len(completed)>=2
            assert all(row["agent"]=="pi" and row["terminal"] and row["reaped"] and row["group_gone"] for row in completed)
            assert all(not name.startswith("tool_execution") for row in completed for name in row["events"])
            if args.require_full_schema:
                generic = hashlib.sha256(b'{"type":"object"}').hexdigest()
                assert all(row["schema_sha256"] != generic for row in completed), "processing fell back to an unspecified JSON object"
            case("official-cognee-processing-through-real-pi",successful_model_requests=len(completed),provider=completed[0]["rpc_resolved_provider"],model=completed[0]["rpc_resolved_model"],receipts=completed)
        report["status"] = "main_flow_passed"
        report["scope"] = "当前完整配置包的Codex官方采集+Memlane当前知识读取；独立官方graph不宣称自动同步App修改。"
        if args.pi_bridge:report["scope"]+=" 本轮 Cognee 模型请求由 pi RPC 执行；采集端仍为官方 Codex 插件。"
    except BaseException as error:
        report["status"] = "failed"
        report["error"] = repr(error)
        if ui is not None:
            try:
                capture("failure")
            except Exception as capture_error:
                report["capture_error"] = repr(capture_error)
        raise
    finally:
        if ui is not None:
            try:
                ui.quit(stop=True)
            except Exception as error:
                report["app_cleanup_error"] = repr(error)
        report["socket_exists_after_stop"] = Path(ipc).exists()
        save()


if __name__ == "__main__":
    main()
