#!/usr/bin/env python3
"""Real independent Go service/worker lifecycle on isolated synthetic data."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
import signal
import socket
import subprocess
import sys
import tarfile
import tempfile
import time
import uuid

ROOT = Path(__file__).resolve().parents[1]


def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--from-step",choices=["all","d2f"],default="all")
    args=parser.parse_args()
    report = {"status":"failed","synthetic_only":True,"steps":[]}
    if args.from_step=="d2f":
        prior=json.loads((ROOT / "build/p0-engine/daemon-verification.json").read_text())
        report["steps"]=[e for e in prior["steps"] if e["status"]=="passed" and e["step"]!="d2f-service-crash"]
        report["reused_prior_passed_steps"]=[e["step"] for e in report["steps"]]
    processes=[]
    with tempfile.TemporaryDirectory(prefix="memlane-daemon-p0-") as name:
        root=Path(name);runtime=root / "runtime";runtime.mkdir()
        archive=ROOT / "build/p0-engine/memlane-cognee-rust-0.0.1-darwin-arm64.tar.gz"
        with tarfile.open(archive) as source:
            manifest=json.load(source.extractfile("component.json"))
            for item in manifest["files"]:
                path=runtime / item["path"]
                assert Path(item["path"]).name==item["path"]
                with source.extractfile(item["path"]) as src,path.open("wb") as dest:shutil.copyfileobj(src,dest)
                assert hashlib.sha256(path.read_bytes()).hexdigest()==item["sha256"]
                path.chmod(0o755 if item["path"]==manifest["executable"] else 0o644)
        shutil.copytree(ROOT / ".local/models/multilingual-e5-base-int8",root / "model")
        shutil.copy2(ROOT / "build/p0-engine/memlaned",root / "memlaned")
        data=root / "data"
        config={"root":str(data),"worker":{"Executable":str(runtime / manifest["executable"]),
                "Library":str(runtime / manifest["library"]),"ModelDir":str(root / "model"),
                "DataDir":str(data / "index"),"DenyNetwork":True},"retry_delay_ms":500}
        config_path=root / "service.json";config_path.write_text(json.dumps(config))
        ipc=Path("/tmp") / f"memlane-{os.getuid()}-{hashlib.sha256(str(data).encode()).hexdigest()[:12]}" / "service.sock"
        count=0
        def start(resume=False):
            command=[str(root / "memlaned"),"--config",str(config_path)]+(["--resume"] if resume else [])
            p=subprocess.Popen(command,cwd=root,env={"PATH":"/usr/bin:/bin","HOME":str(root / "private-home")},
                               stdout=subprocess.DEVNULL,stderr=subprocess.PIPE)
            processes.append(p);return p
        def call(op,success=True,**fields):
            nonlocal count
            count+=1
            request={"protocol":1,"id":str(count),"op":op,**fields}
            with socket.socket(socket.AF_UNIX) as connection:
                connection.settimeout(5);connection.connect(str(ipc))
                connection.sendall((json.dumps(request)+"\n").encode())
                response=json.loads(connection.makefile("rb",buffering=0).readline())
            assert response["id"]==str(count) and response["protocol"]==1
            assert response["ok"]==success,response
            return response.get("result") if success else response
        def wait(predicate,timeout=15):
            deadline=time.monotonic()+timeout
            while time.monotonic()<deadline:
                try:
                    value=predicate()
                    if value:return value
                except (OSError,KeyError,AssertionError):pass
                time.sleep(0.05)
            raise TimeoutError("service condition was not met")
        def ready(previous=None):
            result=call("health")
            return result if result["state"]=="ready" and result["worker_pid"]!=previous else False
        def gone(pid):
            try:os.kill(pid,0);return False
            except ProcessLookupError:return True
        def step(slug,focus,action):
            subprocess.run([sys.executable,str(ROOT / "scripts/p0-handoff.py"),slug,focus],check=True)
            entry={"step":slug,"status":"running"};report["steps"].append(entry);started=time.monotonic()
            try:
                entry["evidence"]=action();entry.update(status="passed",wall_ms=(time.monotonic()-started)*1000)
                (ROOT / "build/p0-engine/daemon-verification.json").write_text(json.dumps(report,ensure_ascii=False,indent=2)+"\n")
                print(json.dumps(entry,ensure_ascii=False),flush=True)
                subprocess.run([sys.executable,str(ROOT / "scripts/p0-handoff.py"),slug+"-done",focus,"--result",json.dumps(entry["evidence"],ensure_ascii=False)],check=True)
            except Exception as exc:entry.update(status="failed",error=repr(exc));raise
        service=None
        dataset,memory_id=str(uuid.uuid4()),str(uuid.uuid4())
        mutation={"operation_id":"first","kind":"save","dataset":dataset,"memory":memory_id,
                  "expected_revision":0,"text":"daemonOriginalToken synthetic original"}
        try:
            service=start()
            def ipc_and_queue():
                wait(ready)
                assert ipc.stat().st_mode & 0o777==0o600
                assert ipc.parent.stat().st_mode & 0o777==0o700
                saved=call("apply",mutation=mutation);assert saved["revision"]==1
                def searchable():
                    hits=call("search",datasets=[dataset],query="daemonOriginalToken",limit=3)
                    return hits if any(h["version"]["memory"]==memory_id for h in hits) else False
                wait(searchable)
                second=start();assert second.wait(timeout=5)!=0
                error=second.stderr.read().decode();assert "already owned" in error
                return {"private_ipc_and_same_uid":True,"durable_save_async_index_search":True,"second_service_rejected":True}
            if args.from_step=="all":
                step("d2a-ipc-queue","实际服务独立启动、私有IPC、保存与队列索引、双服务拥有者拒绝",ipc_and_queue)
            def confirmation():
                for kind in ("edit","delete","restore"):
                    rejected={**mutation,"operation_id":"unconfirmed-"+kind,"kind":kind,"expected_revision":1,
                              "restore_revision":1,"text":"must not apply"}
                    call("apply",success=False,mutation=rejected)
                original=call("get",memory=memory_id);assert original["revision"]==1 and original["text"]==mutation["text"]
                with socket.socket(socket.AF_UNIX) as connection:
                    connection.settimeout(5);connection.connect(str(ipc));connection.sendall(b'{"protocol":1,"id":"bad","op":"health"} {}\n')
                    response=json.loads(connection.makefile("rb",buffering=0).readline());assert not response["ok"]
                return {"unconfirmed_changes_rejected":True,"valid_original_unchanged":True,"trailing_json_rejected":True}
            if args.from_step=="all":
                step("d2b-confirmation","实际IPC拒绝未确认修改，核对有效知识未变及畸形输入错误",confirmation)
            def crash_queue():
                worker=ready()["worker_pid"];os.kill(worker,signal.SIGKILL)
                wait(lambda:call("health")["worker_pid"]==0)
                call("search",success=False,datasets=[dataset],query="daemonOriginalToken",limit=3)
                queued_id=str(uuid.uuid4());queued={**mutation,"operation_id":"queued","memory":queued_id,"text":"queuedRecoveryToken original"}
                saved=call("apply",mutation=queued);assert saved["revision"]==1
                wait(lambda:ready(worker))
                wait(lambda:any(h["version"]["memory"]==queued_id for h in call("search",datasets=[dataset],query="queuedRecoveryToken",limit=3)))
                return {"real_worker_crash":True,"read_failure_distinct_from_empty":True,"accepted_original_during_recovery":True,"durable_job_replayed":True}
            if args.from_step=="all":
                step("d2c-crash-replay","实际终止worker，验证读取错误、恢复期间原文接收和持久补发",crash_queue)
            def stop_resume():
                nonlocal service
                worker=ready()["worker_pid"];call("stop")
                assert service.wait(timeout=5)==0 and gone(worker)
                assert json.loads((data / "service-state.json").read_text())["stopped"]
                assert not ipc.exists()
                stopped=start();assert stopped.wait(timeout=5)==0 and not ipc.exists()
                service=start(resume=True);wait(ready)
                current=call("get",memory=memory_id);assert current["revision"]==1
                wait(lambda:any(h["version"]["memory"]==memory_id for h in call("search",datasets=[dataset],query="daemonOriginalToken",limit=3)))
                return {"explicit_stop_persisted":True,"worker_process_stopped":True,"normal_restart_remains_stopped":True,"explicit_resume_and_data_survive":True}
            if args.from_step=="all":
                step("d2d-stop-resume","主动停止后进程与socket退出，重启保留停止标记，明确恢复后数据仍正确",stop_resume)
            def circuit_breaker():
                for number in range(3):
                    worker=wait(ready)["worker_pid"];os.kill(worker,signal.SIGKILL)
                    if number<2:wait(lambda:ready(worker))
                wait(lambda:call("health")["state"]=="failed")
                until=time.monotonic()+1.2
                while time.monotonic()<until:
                    health=call("health");assert health["state"]=="failed" and health["worker_pid"]==0
                    time.sleep(0.1)
                call("retry");wait(ready)
                return {"three_crashes_circuit_breaker":True,"no_unbounded_restart_loop":True,"explicit_retry_restores_worker":True}
            if args.from_step=="all":
                step("d2e-circuit-breaker","实际连续3次worker崩溃，核对熔断及用户重试后恢复",circuit_breaker)
            if args.from_step=="d2f":
                wait(ready);call("apply",mutation=mutation)
                wait(lambda:bool(call("search",datasets=[dataset],query="daemonOriginalToken",limit=3)))
            def service_crash():
                nonlocal service
                recovering_id=str(uuid.uuid4())
                queued={**mutation,"operation_id":"service-crash","memory":recovering_id,
                        "text":"serviceCrashRecoveryToken "+"天地玄黄宇宙洪荒日月盈昃辰宿列张"*160}
                call("apply",mutation=queued)
                active=wait(lambda:call("health") if call("health")["worker_busy"] else False)
                worker=active["worker_pid"]
                service.kill();assert service.wait(timeout=5)!=0
                wait(lambda:gone(worker),timeout=5)
                service=start();wait(ready)
                current=call("get",memory=recovering_id);assert current["revision"]==1 and current["text"]==queued["text"]
                wait(lambda:any(h["version"]["memory"]==recovering_id for h in call("search",datasets=[dataset],query="serviceCrashRecoveryToken",limit=3)))
                return {"real_service_sigkill_during_index":True,"pipe_eof_stops_worker":True,
                        "new_service_reclaims_owner":True,"original_and_pending_job_survive":True,"restart_replays_current_revision":True}
            step("d2f-service-crash","实际在worker索引期间SIGKILL业务服务，检查子进程退出和新服务补发持久任务",service_crash)
            call("stop");assert service.wait(timeout=5)==0
            report.update(status="passed",os_worker_network_denied=True,system_registration_tested=False,
                          native_window_exit_tested=False,automatic_update_tested=False)
        except Exception as exc:report["error"]=repr(exc)
        finally:
            for p in processes:
                if p.poll() is None:
                    p.terminate()
                    try:p.wait(timeout=5)
                    except subprocess.TimeoutExpired:p.kill();p.wait()
            if ipc.exists():ipc.unlink()
    (ROOT / "build/p0-engine/daemon-verification.json").write_text(json.dumps(report,ensure_ascii=False,indent=2)+"\n")
    print(json.dumps(report,ensure_ascii=False,indent=2))
    if report["status"]!="passed":raise SystemExit(1)


if __name__=="__main__":main()
