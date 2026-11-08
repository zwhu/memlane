#!/usr/bin/env python3
"""空业务/空缓存的正式 App 连续验收；仅本轮合成正常任务，不注入记忆。"""
import argparse
import hashlib
from importlib.machinery import SourceFileLoader
import json
import os
from pathlib import Path
import socket
import subprocess
import uuid

ROOT=Path(__file__).resolve().parents[1]

def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--pointer",type=Path,default=ROOT/"build/onboarding-memory-validation/full-run/fixture-path.txt")
    parser.add_argument("--out",type=Path,default=ROOT/"build/onboarding-memory-validation/full-run/continuous")
    parser.add_argument("--auth-directory",type=Path,required=True)
    args=parser.parse_args();args.out.mkdir(parents=True,exist_ok=True)
    fixture=Path(args.pointer.read_text().strip())
    assert json.loads((fixture/"synthetic-only.json").read_text())["synthetic_only"]
    meta=json.loads((fixture/"source-info.json").read_text());app=Path(meta["app"])
    config=json.loads((fixture/"runtime-config.json").read_text());assert config["root"]==str(fixture/"client")
    data=fixture/"client/data";ipc=f"/tmp/memlane-{os.getuid()}-{hashlib.sha256(str(data).encode()).hexdigest()[:12]}/service.sock"
    def call(op,**fields):
        with socket.socket(socket.AF_UNIX) as conn:
            conn.settimeout(25);conn.connect(ipc);conn.sendall((json.dumps({"protocol":1,"id":str(uuid.uuid4()),"op":op,**fields})+"\n").encode());response=json.loads(conn.makefile("rb",buffering=0).readline())
        assert response["ok"],response
        return response.get("result")
    U=SourceFileLoader("continuousonboardingui",str(ROOT/"scripts/p1-ui.py")).load_module()
    ui=None;report={"state":"failed","root":str(fixture),"app":str(app),"cases":[],"fixture_injection":False}
    try:
        assert not (data/"memlane.sqlite").exists() and not (fixture/"client/runtime").exists() and not (fixture/"client/official/runtime").exists()
        ui=U.UI(app,fixture,args.out/"screenshots",config=fixture/"runtime-config.json")
        def press(key):
            button=ui.node(key=key);assert "disabled" not in button.get("states",[]),button;ui.req("act",id=button["id"],action="press")
        U.wait(lambda:any(n.get("key")=="first-launch-confirmation" for n in ui.nodes()),timeout=65)
        assert not (data/"memlane.sqlite").exists();ui.system_capture("01-discovery-system.png")
        press("first-launch-confirmation");ui.system_capture("02-confirmation-system.png");press("first-launch-prepare")
        state_path=fixture/"client/first-launch.json"
        U.wait(lambda:json.loads(state_path.read_text())["stage"] in ("ready","failed"),timeout=180)
        state=json.loads(state_path.read_text());assert state["stage"]=="ready",state
        assert call("health")["state"]=="ready" and call("list",list_query={"kind":"knowledge"})["total"]==0 and call("list",list_query={"kind":"raw"})["total"]==0
        ui.system_capture("03-native-ready-system.png");report["cases"].append({"case":"real_native_components_empty_business","state":state})
        press("first-launch-use");U.wait(lambda:any(n.get("key")=="project-name" for n in ui.nodes()))
        ui.set("project-name","连续合成项目 A");ui.set("project-path",str(fixture/"repo-a"));press("register-project")
        U.wait(lambda:any(n.get("key")=="browse-cognee" for n in ui.nodes()));scope=next(s for s in call("scopes") if s.get("name")=="连续合成项目 A");report["project_a"]=scope
        press("browse-cognee");U.wait(lambda:any(n.get("key")=="cognee-prepare" for n in ui.nodes()));ui.system_capture("04-official-confirmation-system.png")
        press("cognee-prepare");official_path=fixture/"client/official-onboarding.json"
        U.wait(lambda:official_path.exists() and json.loads(official_path.read_text()).get("Phase") in ("ready","failed","cancelled"),timeout=360)
        official=json.loads(official_path.read_text());assert official["Phase"]=="ready",official
        assert call("list",list_query={"kind":"knowledge"})["total"]==0 and call("list",list_query={"kind":"raw"})["total"]==0
        ui.system_capture("05-official-ready-system.png");report["cases"].append({"case":"real_cognee_components_model_probe","state":official})
        ui.set("official-agent-auth",str(args.auth_directory.resolve()));press("official-agent-prepare")
        U.wait(lambda:any(n.get("key")=="official-agent-task" for n in ui.nodes()),timeout=130)
        secret="cedar-orbit-"+uuid.uuid4().hex[:12];(args.out/"expected-synthetic-fact.json").write_text(json.dumps({"project":scope["id"],"release_approval_phrase":secret}))
        task=f"""这是新建合成项目的正常编码任务。项目发布前的审核口令是 {secret}，仅属于本轮会话约定，不要写入代码、测试、README、AGENTS.md或其他项目文件。请实现 normalize_ticket(text)：只接受 str，其他输入抛 TypeError；去掉两端空白、连续空白变为短横线、英文字母小写。使用 Python 标准库，写几个 unittest 用例并实际运行。最后简要用中文报告结果并确认你知道本项目审核口令。正常使用工具完成任务，不手工调用接口注入记忆，不读取其他项目、用户聊天或验证目录。仅授权官方 Cognee 插件采集本轮合成对话和工具轨迹；完成后采用官方支持的结束同步。"""
        ui.set("official-agent-task",task);press("official-agent-run")
        U.wait(lambda:any(n.get("key")=="official-agent-sync" and "disabled" not in n.get("states",[]) for n in ui.nodes()),timeout=720)
        assert "正常任务已结束" in ui.node(key="official-agent-result").get("label",""),ui.labels()
        ui.system_capture("06-normal-agent-task-system.png");press("official-agent-sync")
        U.wait(lambda:any(n.get("key")=="cognee-sync" and "disabled" not in n.get("states",[]) for n in ui.nodes()) or any(n.get("key")=="official-agent-result" and "同步或授权未完成" in n.get("label","") for n in ui.nodes()),timeout=360)
        assert "同步或授权未完成" not in ui.node(key="official-agent-result").get("label",""),ui.labels()
        knowledge=call("list",list_query={"kind":"knowledge","datasets":[scope["id"]]});raw=call("list",list_query={"kind":"raw","datasets":[scope["id"]]})
        report["knowledge"]=knowledge;report["raw"]=raw
        assert knowledge["total"]>0 and raw["total"]>0,(knowledge,raw,ui.labels())
        qa=next(v for v in raw["items"] if v.get("source_detail",{}).get("official",{}).get("kind")=="qa")
        report["official_session"]=qa["source_detail"]["official"]["session"]
        (args.out/"expected-synthetic-fact.json").write_text(json.dumps({"project":scope["id"],"release_approval_phrase":secret,"session":report["official_session"]}))
        assert all(item["dataset"]==scope["id"] for item in knowledge["items"]+raw["items"])
        for path in (fixture/"repo-a").rglob("*"):
            if path.is_file() and ".git" not in path.parts:assert secret.encode() not in path.read_bytes(),str(path)
        graph=call("cognee_graph",cognee_graph_query={"project":scope["id"]});report["graph"]=graph
        assert graph["entities"] and graph["relations"],graph
        U.wait(lambda:all(call("get",memory=item["memory"])["index_state"]=="indexed" for item in knowledge["items"]),timeout=90)
        item=knowledge["items"][0];hits=call("library_search",scope=scope["id"],query="normalize_ticket 字符串 类型 校验",limit=30);report["rust_hits"]=hits
        assert any(hit["version"]["memory"]==item["memory"] and hit["version"]["revision"]==item["revision"] and hit["version"]["dataset"]==scope["id"] for hit in hits),hits
        press("cognee-browse");U.wait(lambda:any(n.get("key")=="browse-tab-memories" for n in ui.nodes()));ui.system_capture("07-memory-system.png")
        chosen=graph["mentions"][0]["memory"];press("memory-"+chosen)
        press("browse-tab-entities");U.wait(lambda:any(n.get("key","").startswith("browse-entity-") and n.get("key")!="browse-entity-search" for n in ui.nodes()));ui.system_capture("08-entities-system.png")
        entity=graph["entities"][0];press("browse-entity-"+entity["id"]);press("browse-entity-graph");ui.system_capture("09-relations-system.png")
        # Continue in this same window and immutable bundle: actual source,
        # every current Rust version, replay, project isolation and fresh recall.
        source=graph["relations"][0]["sources"][0]
        chosen=next(v for v in knowledge["items"] if v["memory"]==source["memory"])
        ui.press("连续合成项目 A");press("browse-tab-memories");press("memory-"+chosen["memory"])
        U.wait(lambda:any(n.get("key")=="source-reader-open-"+qa["memory"] for n in ui.nodes()))
        native=next(v for v in raw["items"] if v.get("source_detail",{}).get("native"))
        read=call("source_read",source_read={"project":scope["id"],"owner":chosen["memory"],"owner_revision":chosen["revision"],"memory":qa["memory"],"revision":qa["revision"]})
        assert read["state"]=="available" and read["original_state"]=="available" and read["session_original"]["text"]==native["text"]
        report["source_read"]=read
        press("source-reader-open-"+qa["memory"]);U.wait(lambda:any(n.get("key")=="source-reader-copy" for n in ui.nodes()))
        press("source-reader-copy");copied=subprocess.check_output(["/usr/bin/pbpaste"]).decode();assert copied==native["text"]
        report["full_original_copy"]={"bytes":len(copied.encode()),"sha256":hashlib.sha256(copied.encode()).hexdigest()}
        assert report["full_original_copy"]["sha256"]==native["source_detail"]["native"]["digest"]
        ui.system_capture("10-full-original-system.png")
        if "disabled" not in ui.node(key="source-reader-next").get("states",[]):press("source-reader-next");ui.system_capture("11-original-next-page-system.png")
        ui.set("source-reader-search","工具返回");press("source-reader-find")
        assert any("处匹配" in n.get("label","") and not n.get("label","").startswith("0 ") for n in ui.nodes())
        press("source-reader-quote");press("source-reader-copy");assert subprocess.check_output(["/usr/bin/pbpaste"]).decode()==qa["text"]
        ui.system_capture("12-sdk-quote-system.png");press("source-reader-close")
        report["rust_current_versions"]=[]
        for v in knowledge["items"]:
            hits=call("library_search",scope=scope["id"],query=v["text"].split("\n\n",1)[-1][:180],limit=30)
            assert any(h["version"]["memory"]==v["memory"] and h["version"]["revision"]==v["revision"] and h["version"]["dataset"]==scope["id"] and h["version"]["text"]==v["text"] for h in hits)
            report["rust_current_versions"].append({"memory":v["memory"],"revision":v["revision"],"hits":hits})
        ui.press("添加项目");U.wait(lambda:any(n.get("key")=="project-name" for n in ui.nodes()))
        ui.set("project-name","连续合成项目 B");ui.set("project-path",str(fixture/"repo-b"));press("register-project")
        U.wait(lambda:any(p.get("name")=="连续合成项目 B" for p in call("scopes")))
        project_b=next(p for p in call("scopes") if p["name"]=="连续合成项目 B");report["project_b"]=project_b
        assert call("list",list_query={"datasets":[project_b["id"]]})["total"]==0
        assert not any(n.get("key")=="memory-"+v["memory"] for n in ui.nodes() for v in raw["items"]+knowledge["items"])
        press("browse-tab-entities");ui.system_capture("13-project-b-empty-system.png")
        ui.press("连续合成项目 A");press("browse-cognee")
        U.wait(lambda:any(n.get("key")=="cognee-project-"+scope["id"] for n in ui.nodes()));press("cognee-project-"+scope["id"])
        U.wait(lambda:any(n.get("key")=="cognee-refresh-auth" for n in ui.nodes()))
        ui.set("cognee-session",report["official_session"]);press("cognee-sync")
        U.wait(lambda:"disabled" not in ui.node(key="cognee-sync").get("states",[]),timeout=330)
        for kind,prior in [("knowledge",knowledge),("raw",raw)]:
            after=call("list",list_query={"kind":kind,"datasets":[scope["id"]]})
            assert sorted((v["memory"],v["revision"],v["text"]) for v in prior["items"])==sorted((v["memory"],v["revision"],v["text"]) for v in after["items"])
        agent_scope=json.loads((fixture/"client/official/agents"/scope["id"]/"scope.json").read_text())
        logs=Path(agent_scope["home"])/".codex/sessions";before=set(logs.rglob("*.jsonl"))
        prompt=f"这是本项目全新会话。请通过官方 Cognee 插件 {agent_scope['plugin']} 的 scripts/cognee-search.sh --graph 正式召回上一轮 normalize_ticket 正常编码会话约定的发布审核口令，并注明来源。这是用户授权的合成验收，允许在回答中返回合成原值。禁止resume、读历史会话或项目/验收文件、使用普通MCP搜索、把答案写入文件；没有找到就如实说没有，不猜测。仅授权采集本轮合成查询和工具轨迹。"
        assert secret not in prompt
        (args.out/"fresh-prompt.txt").write_text(prompt)
        ui.set("official-agent-task",prompt);press("official-agent-run")
        U.wait(lambda:"disabled" not in ui.node(key="official-agent-sync").get("states",[]),timeout=720)
        assert "正常任务已结束" in ui.node(key="official-agent-result").get("label","")
        new=set(logs.rglob("*.jsonl"))-before;assert len(new)==1
        R=SourceFileLoader("continuousrecallcapture",str(ROOT/"scripts/verify-onboarding-official-recall-app.py")).load_module()
        report["fresh_official_recall"]=R.capture_recall(next(iter(new)),secret,report["official_session"])
        ui.system_capture("14-fresh-official-recall-system.png")
        for p in Path(agent_scope["workspace"]).rglob("*"):
            if p.is_file() and ".git" not in p.parts:assert secret.encode() not in p.read_bytes()
        assert not (Path(agent_scope["home"])/".codex/auth.json").exists()
        report["immutable_app_continuous_window"]=True
        report["state"]="empty_business_empty_cache_normal_task_reader_graph_rust_and_fresh_official_recall_passed"
        report["remaining_required"]=["reopen persistence and cleanup; existing lifecycle boundaries remain separate evidence"]
        print({"state":report["state"],"project":scope["id"],"raw":raw["total"],"knowledge":knowledge["total"]},flush=True)
    except Exception as error:
        report["error"]=repr(error)
        if ui is not None:
            try:ui.system_capture("failure-system.png")
            except Exception:pass
        raise
    finally:
        if ui is not None:
            try:ui.quit(stop=True)
            except Exception as error:report["cleanup_error"]=repr(error)
        (args.out/"report.json").write_text(json.dumps(report,ensure_ascii=False,indent=2))

if __name__=="__main__":main()
