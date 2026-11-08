#!/usr/bin/env python3
"""修正接收接口后，在正式窗口接收已有本轮官方会话；不重造材料。"""
import argparse
import hashlib
from importlib.machinery import SourceFileLoader
import json
import os
from pathlib import Path
import socket
import time
import uuid

ROOT=Path(__file__).resolve().parents[1]

def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--pointer",type=Path,default=ROOT/"build/onboarding-memory-validation/final-cold/fixture-path.txt")
    parser.add_argument("--session",required=True)
    parser.add_argument("--out",type=Path,default=ROOT/"build/onboarding-memory-validation/final-cold/resume")
    args=parser.parse_args();args.out.mkdir(parents=True,exist_ok=True)
    f=Path(args.pointer.read_text().strip());assert json.loads((f/"synthetic-only.json").read_text())["synthetic_only"]
    meta=json.loads((f/"source-info.json").read_text());data=f/"client/data";ipc=f"/tmp/memlane-{os.getuid()}-{hashlib.sha256(str(data).encode()).hexdigest()[:12]}/service.sock"
    def call(op,**fields):
        with socket.socket(socket.AF_UNIX) as connection:
            connection.settimeout(25);connection.connect(ipc);connection.sendall((json.dumps({"protocol":1,"id":str(uuid.uuid4()),"op":op,**fields})+"\n").encode());response=json.loads(connection.makefile("rb",buffering=0).readline())
        assert response["ok"],response
        return response.get("result")
    U=SourceFileLoader("resumeofficialui",str(ROOT/"scripts/p1-ui.py")).load_module();ui=None
    report={"state":"failed","root":str(f),"session":args.session,"original_material_reused":True,"synthetic_only":True}
    try:
        ui=U.UI(Path(meta["app"]),f,args.out/"screenshots",config=f/"runtime-config.json")
        def press(key):
            n=ui.node(key=key);assert "disabled" not in n.get("states",[]),n;ui.req("act",id=n["id"],action="press")
        ui.press("设置");U.wait(lambda:"启动服务" in ui.labels());ui.press("启动服务");U.wait(lambda:call("health")["state"]=="ready",timeout=100)
        scopes=call("scopes");project=next(p for p in scopes if p.get("name")=="连续合成项目 A");report["project"]=project
        press("cognee-settings");U.wait(lambda:any(n.get("key")=="cognee-project-"+project["id"] for n in ui.nodes()));press("cognee-project-"+project["id"])
        U.wait(lambda:any(n.get("key")=="cognee-prepare" and "disabled" not in n.get("states",[]) for n in ui.nodes()));press("cognee-prepare")
        state_file=f/"client/official-onboarding.json"
        U.wait(lambda:json.loads(state_file.read_text())["Phase"] in ("ready","failed","cancelled") and any(n.get("key")=="cognee-prepare" and "disabled" not in n.get("states",[]) for n in ui.nodes()),timeout=240)
        state=json.loads(state_file.read_text());assert state["Phase"]=="ready",state
        ui.system_capture("01-reused-real-service-system.png")
        ui.set("cognee-session",args.session);press("official-agent-sync")
        U.wait(lambda:any(n.get("key")=="cognee-sync" and "disabled" not in n.get("states",[]) for n in ui.nodes()) or any(n.get("key")=="official-agent-result" and "同步或授权未完成" in n.get("label","") for n in ui.nodes()),timeout=360)
        assert "同步或授权未完成" not in ui.node(key="official-agent-result").get("label",""),ui.labels()
        U.wait(lambda:any(n.get("key")=="cognee-result" for n in ui.nodes()),timeout=40)
        report["ui_result"]=ui.node(key="cognee-result").get("label","")
        raw=call("list",list_query={"kind":"raw","datasets":[project["id"]]});knowledge=call("list",list_query={"kind":"knowledge","datasets":[project["id"]]})
        # Official --strict acknowledges the background improve submission.
        # Keep the real App/service alive and retry only the approved material
        # read; never re-submit improve or substitute cache for persistence.
        report["material_wait"]=[]
        deadline=time.monotonic()+600
        while raw["total"]==0 or knowledge["total"]==0:
            report["material_wait"].append({"elapsed":round(600-(deadline-time.monotonic()),1),"message":ui.node(key="cognee-result").get("label",""),"raw":raw["total"],"knowledge":knowledge["total"]})
            assert time.monotonic()<deadline,report["material_wait"]
            time.sleep(3)
            press("cognee-sync")
            U.wait(lambda:"disabled" not in ui.node(key="cognee-sync").get("states",[]),timeout=60)
            raw=call("list",list_query={"kind":"raw","datasets":[project["id"]]});knowledge=call("list",list_query={"kind":"knowledge","datasets":[project["id"]]})
        report["ui_result"]=ui.node(key="cognee-result").get("label","")
        report["raw"]=raw;report["knowledge"]=knowledge
        assert raw["total"]>0 and knowledge["total"]>0,(report["ui_result"],raw,knowledge)
        graph=call("cognee_graph",cognee_graph_query={"project":project["id"]});report["graph"]=graph
        ui.system_capture("02-real-import-receipt-system.png")
        report["state"]="real_material_import_passed"
        print({"state":report["state"],"raw":raw["total"],"knowledge":knowledge["total"],"entities":len(graph["entities"]),"relations":len(graph["relations"])},flush=True)
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
