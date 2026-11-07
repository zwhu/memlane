#!/usr/bin/env python3
"""Verify real App replacement/failure against the controlled HTTPS source."""
import argparse
import hashlib
import fcntl
import json
import os
from pathlib import Path
import plistlib
import socket
import sqlite3
import subprocess
import sys
import time
import urllib.request
import uuid

ROOT=Path(__file__).resolve().parents[1]


def stopped_original(data, record):
    # Only use immutable after the service owning lock proves shutdown and
    # there is no live WAL. This reads the database without creating shm/WAL.
    assert json.loads((data / "service-state.json").read_text())["stopped"]
    with (data / ".service.lock").open("rb") as owner:
        fcntl.flock(owner, fcntl.LOCK_EX | fcntl.LOCK_NB)
        path=data / "memlane.sqlite"
        wal=Path(str(path)+"-wal")
        assert not wal.exists() or wal.stat().st_size==0, "uncheckpointed WAL"
        before=hashlib.sha256(path.read_bytes()).hexdigest()
        with sqlite3.connect(path.as_uri()+"?mode=ro&immutable=1",uri=True) as db:
            assert db.execute("PRAGMA integrity_check").fetchone()==("ok",)
            row=db.execute("SELECT m.current_revision,v.text FROM memories m JOIN versions v ON v.memory=m.id AND v.revision=m.current_revision WHERE m.id=?",(record["memory"],)).fetchone()
            assert row==(1,record["text"]),row
        assert hashlib.sha256(path.read_bytes()).hexdigest()==before


def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--case",choices=["bad-signature","truncate","success","preserve-stop"],required=True)
    parser.add_argument("--fixture",type=Path,required=True)
    parser.add_argument("--app",type=Path,required=True)
    parser.add_argument("--readback-only",action="store_true",help="Supplement an already replaced preserve-stop case without repeating installation")
    args=parser.parse_args()
    info=json.loads((args.fixture / "source-info.json").read_text())
    cfg=json.loads((args.fixture / "runtime-config.json").read_text())
    record=json.loads((args.fixture / "knowledge-fixture.json").read_text())
    data=Path(cfg["root"]) / "data"
    ipc=f"/tmp/memlane-{os.getuid()}-{hashlib.sha256(str(data).encode()).hexdigest()[:12]}/service.sock"
    app=args.app.resolve()
    plist=app / "Contents/Info.plist"
    metadata=plistlib.loads(plist.read_bytes())
    binary=app / "Contents/MacOS" / metadata["CFBundleExecutable"]
    old_version=metadata["CFBundleShortVersionString"]
    old_hash=hashlib.sha256(binary.read_bytes()).hexdigest()
    def http(path):return json.load(urllib.request.urlopen(info["http_url"]+path,timeout=10))
    def call(op,**fields):
        with socket.socket(socket.AF_UNIX) as connection:
            connection.settimeout(10);connection.connect(ipc)
            connection.sendall((json.dumps({"protocol":1,"id":str(uuid.uuid4()),"op":op,**fields})+"\n").encode())
            response=json.loads(connection.makefile("rb",buffering=0).readline())
        assert response["ok"],response
        return response.get("result")
    def run(action):
        result=subprocess.run([str(binary),"--p0-action",action],cwd=app.parent,capture_output=True,text=True,timeout=90)
        assert result.returncode==0,result.stderr
        return json.loads(result.stdout)
    mode={"bad-signature":"signature_bad","truncate":"truncate","success":"normal","preserve-stop":"normal"}[args.case]
    request=urllib.request.Request(info["http_url"]+"/control",data=json.dumps({"update_mode":mode}).encode(),headers={"Content-Type":"application/json"})
    urllib.request.urlopen(request,timeout=10).read()
    slug="d2-update-"+args.case
    subprocess.run([sys.executable,str(ROOT / "scripts/p0-handoff.py"),slug,"实际MyGo更新内容、后台停启、版本和合成原文校验"],check=True)
    report={"step":args.case,"status":"failed","old_version":old_version}
    out=ROOT / "build/p0-engine/update-verification.json"
    if args.readback_only:
        assert args.case=="preserve-stop"
        report=next(s for s in json.loads(out.read_text())["steps"] if s["step"]==args.case)
        assert report["actual_app_replaced"] and report["new_version"]==old_version
        report["prior_readback_error"]=report.pop("error",None)
        report["supplement_only"]=True
    before=http("/state")["requests"]
    if args.case=="preserve-stop":
        assert json.loads((data / "service-state.json").read_text())["stopped"]
    else:assert call("health")["state"]=="ready"
    try:
        if args.readback_only:
            prepared=run("prepare")
            assert prepared.get("phase")=="stopped" and "error" not in prepared,prepared
        else:
            result=run("install");report["actual_result"]=result
        if args.readback_only:
            pass
        elif args.case in ("bad-signature","truncate"):
            assert "error" in result,result
            if args.case=="bad-signature":assert "not signed" in result["error"]
            assert hashlib.sha256(binary.read_bytes()).hexdigest()==old_hash
            assert call("health")["state"]=="ready"
            report.update(old_app_unchanged=True,background_restored=True)
        else:
            assert result.get("phase")=="update_installed" and "error" not in result,result
            expected=json.loads((args.fixture / "latest-update.json").read_text())["version"]
            deadline=time.monotonic()+15
            while time.monotonic()<deadline:
                version=plistlib.loads(plist.read_bytes())["CFBundleShortVersionString"]
                if version==expected:break
                time.sleep(.1)
            assert version==expected and version!=old_version,(version,expected)
            subprocess.run(["/usr/bin/codesign","--verify","--deep","--strict",str(app)],check=True,capture_output=True)
            prepared=run("prepare")
            if args.case=="preserve-stop":
                assert prepared.get("phase")=="stopped" and "error" not in prepared,prepared
                assert json.loads((data / "service-state.json").read_text())["stopped"]
                report["user_stop_preserved"]=True
            else:
                assert prepared.get("phase")=="ready" and "error" not in prepared,prepared
                assert call("health")["state"]=="ready"
            report.update(new_version=version,actual_app_replaced=True,code_signature_valid=True)
        if args.case!="preserve-stop":
            current=call("get",memory=record["memory"])
            assert current["revision"]==1 and current["text"]==record["text"]
            hits=call("search",datasets=[record["dataset"]],query="clientPersistentOriginalToken",limit=3)
            assert any(h["version"]["memory"]==record["memory"] for h in hits)
        if args.case=="preserve-stop":
            stopped_original(data,record)
            report["stopped_database_readonly_original_check"]=True
        after=http("/state")["requests"]
        for id in (cfg["worker_id"],cfg["model_id"]):
            path="/components/"+id+".tar.gz"
            assert before.get(path,0)==after.get(path,0),(path,before,after)
        report.update(status="passed",component_archives_reused=True,original_preserved=True)
    except Exception as exc:report["error"]=repr(exc)
    combined=json.loads(out.read_text()) if out.exists() else {"status":"in_progress","steps":[]}
    combined["steps"]=[s for s in combined["steps"] if s["step"]!=args.case]+[report]
    combined["status"]="in_progress"
    out.write_text(json.dumps(combined,ensure_ascii=False,indent=2)+"\n")
    print(json.dumps(report,ensure_ascii=False,indent=2))
    if report["status"]!="passed":raise SystemExit(1)
    subprocess.run([sys.executable,str(ROOT / "scripts/p0-handoff.py"),slug+"-done","本次实际更新验收完成，复用未受改动影响的其余证据","--result",json.dumps(report,ensure_ascii=False)],check=True)


if __name__=="__main__":main()
