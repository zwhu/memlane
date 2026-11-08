#!/usr/bin/env python3
"""空业务/空缓存的正式 App 连续验收；仅本轮合成正常任务，不注入记忆。"""
import argparse
import hashlib
from importlib.machinery import SourceFileLoader
import json
import os
from pathlib import Path
import socket
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
        report["official_session"]=raw["items"][0]["source_detail"]["official"]["session"]
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
        report["state"]="normal_task_browsing_slice_passed"
        report["remaining_required"]=["full source reader navigation and system comparison","project B and reopen","fresh-session official recall","replay/cancel/failure boundaries","final checks/review/cleanup/commits"]
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
