#!/usr/bin/env python3
"""独立空根的实机首启跳过、故障、下载取消与恢复；故障源显式标记。"""
import argparse
import hashlib
from importlib.machinery import SourceFileLoader
import json
import os
from pathlib import Path
import socket
import urllib.request
import uuid

ROOT = Path(__file__).resolve().parents[1]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--pointer", type=Path, default=ROOT / "build/first-launch-validation/lifecycle/fixture-path.txt")
    parser.add_argument("--out", type=Path, default=ROOT / "build/first-launch-validation/lifecycle/actual")
    parser.add_argument("--resume-existing", action="store_true", help="Resume this Goal's previously failed real lifecycle root")
    args = parser.parse_args(); args.out.mkdir(parents=True, exist_ok=True)
    f = Path(args.pointer.read_text().strip())
    assert json.loads((f / "synthetic-only.json").read_text())["synthetic_only"]
    meta = json.loads((f / "source-info.json").read_text()); source = meta["source"]["http_url"]
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    def control(**fields):
        with opener.open(urllib.request.Request(source + "/control", data=json.dumps(fields).encode(), headers={"Content-Type": "application/json"}), timeout=5) as response: return json.load(response)
    def source_state():
        with opener.open(source + "/state", timeout=5) as response: return json.load(response)
    def state(): return json.loads((f / "client/first-launch.json").read_text())
    ipc = f"/tmp/memlane-{os.getuid()}-{hashlib.sha256(str(f / 'client/data').encode()).hexdigest()[:12]}/service.sock"
    def call(op, **fields):
        with socket.socket(socket.AF_UNIX) as c:
            c.settimeout(12); c.connect(ipc); c.sendall((json.dumps({"protocol": 1, "id": str(uuid.uuid4()), "op": op, **fields}) + "\n").encode())
            r = json.loads(c.makefile("rb", buffering=0).readline())
        assert r["ok"], r
        return r.get("result")
    U = SourceFileLoader("firstlifecyclereal", str(ROOT / "scripts/p1-ui.py")).load_module()
    report = {"state": "failed", "fixture": str(f), "synthetic_only": True, "cases": [], "fault_source": "this isolated controlled real archive source only"}; ui = None
    def launch(name): return U.UI(Path(meta["app"]), f, args.out / name, config=f / "runtime-config.json")
    try:
        if args.resume_existing:
            assert not state()["completed"] and state()["stage"] in ("failed", "cancelled", "results", "interrupted")
            assert (f / "client/runtime").exists() and not (f / "client/data/memlane.sqlite").exists()
            report["continuation_of_owned_failed_root"] = True
            report["initial_empty_business_and_cache"] = "previous real attempt; see lifecycle/actual/report.json"
            control(component_mode="normal", component_chunk_delay=0)
            ui = launch("resumed")
            U.wait(lambda: any(n.get("key") == "first-launch-confirmation" for n in ui.nodes()), timeout=65)
            def resume_press(key):
                n = ui.node(key=key); assert "disabled" not in n.get("states", []), n; ui.req("act", id=n["id"], action="press")
            codex = next(v for v in state()["agents"] if v["agent"] == "codex")
            assert codex["path"].endswith("/MacOS/codex") and "0.167.0-alpha.3.2" in codex["version"]
            report["automatically_discovered_codex"] = codex
            resume_press("first-launch-confirmation"); resume_press("first-launch-prepare")
            U.wait(lambda: state()["stage"] in ("ready", "failed"), timeout=180)
            assert state()["stage"] == "ready", state()
            assert call("health")["state"] == "ready" and call("list", list_query={"kind": "raw"})["total"] == 0 and call("list", list_query={"kind": "knowledge"})["total"] == 0
            assert not call("connections")
            ui.system_capture("05-durable-stop-resume-ready-system.png")
            report["cases"].append({"case": "real_durable_stop_resume_after_actual_cancel", "state": state(), "source": source_state(), "business_records": 0})
            report["state"] = "real_first_launch_durable_stop_recovery_passed"
            print({"state": report["state"], "attempts": state()["attempt"]}, flush=True)
            return
        assert not (f / "client/data/memlane.sqlite").exists() and not (f / "client/runtime").exists(), "Requires a new owned empty root"
        report["initial_empty_business_and_cache"] = True
        ui = launch("first")
        def press(key):
            n = ui.node(key=key); assert "disabled" not in n.get("states", []), n; ui.req("act", id=n["id"], action="press")
        U.wait(lambda: any(n.get("key") == "first-launch-confirmation" for n in ui.nodes()), timeout=65)
        press("first-launch-reduced"); U.wait(lambda: state()["reduced_motion"])
        press("first-launch-skip"); U.wait(lambda: state()["skipped"])
        assert not state()["completed"] and not (f / "client/data/memlane.sqlite").exists()
        ui.system_capture("01-skipped-restricted-system.png")
        report["cases"].append({"case": "skip_without_preparation_or_history_import", "state": state(), "source": source_state()})
        ui.quit(stop=True); ui = launch("reopen-skipped")
        assert state()["skipped"] and not state()["completed"]
        ui.press("设置"); press("first-launch-resume")
        U.wait(lambda: any(n.get("key") == "first-launch-confirmation" for n in ui.nodes()), timeout=65)
        assert not state()["skipped"] and state()["reduced_motion"]
        control(component_mode="http_error")
        press("first-launch-confirmation"); press("first-launch-prepare")
        U.wait(lambda: state()["stage"] == "failed", timeout=100)
        U.wait(lambda: "503" in state()["message"], timeout=10)
        assert not state()["completed"] and not any(n.get("key") == "first-launch-use" for n in ui.nodes())
        ui.system_capture("02-real-http503-failure-system.png")
        report["cases"].append({"case": "download_http503_fault_injection", "state": state(), "source": source_state(), "fault_injection": True})
        control(component_mode="normal", component_chunk_delay=0.03)
        press("first-launch-retry"); U.wait(lambda: any(n.get("key") == "first-launch-confirmation" for n in ui.nodes()), timeout=65)
        press("first-launch-confirmation"); press("first-launch-prepare")
        def partial_bytes():
            return sum(p.stat().st_size for p in (f / "client/runtime/downloads").glob("*.part"))
        U.wait(lambda: partial_bytes() > 65536, timeout=90)
        report["download_before_cancel"] = partial_bytes()
        # Capture the real active phase without the generic idle waiter: the
        # native block animation remains active until a real terminal receipt.
        mac = SourceFileLoader("firstlaunchprogressmac", str(ROOT / "scripts/mac-native.py")).load_module().Mac()
        mac.require_unlocked(); mac.activate(ui.pid)
        mac.capture(ui.pid, ui.out / "03a-real-preparation-system.png")
        (ui.out / "03a-real-preparation-state.json").write_text(json.dumps({"actual_system_window": True, "pid": ui.pid, "state": state(), "native": ui.native()}, ensure_ascii=False, indent=2))
        press("first-launch-cancel")
        U.wait(lambda: state()["stage"] == "cancelled", timeout=50)
        assert not state()["completed"] and not (f / "client/data/memlane.sqlite").exists()
        ui.system_capture("03-download-cancelled-system.png")
        report["cases"].append({"case": "actual_download_cancel", "state": state(), "partial_bytes": partial_bytes(), "throttled_real_archive": True})
        ui.quit(stop=True); ui = None
        control(component_mode="normal", component_chunk_delay=0)
        ui = launch("reopen-cancelled")
        U.wait(lambda: any(n.get("key") == "first-launch-confirmation" for n in ui.nodes()), timeout=65)
        assert state()["reduced_motion"] and not state()["completed"]
        press("first-launch-confirmation"); press("first-launch-prepare")
        U.wait(lambda: state()["stage"] in ("ready", "failed"), timeout=180)
        assert state()["stage"] == "ready", state()
        assert call("health")["state"] == "ready" and call("list", list_query={"kind": "raw"})["total"] == 0 and call("list", list_query={"kind": "knowledge"})["total"] == 0
        assert not call("connections")
        ui.system_capture("04-resumed-real-worker-model-ready-system.png")
        report["cases"].append({"case": "reopen_and_resume_real_signed_components", "state": state(), "source": source_state(), "business_records": 0})
        report["official_agent_path"] = "Separate continuous official task and fresh recall evidence under onboarding-memory-validation/final-cold"
        report["state"] = "real_first_launch_skip_failure_cancel_resume_passed"
        print({"state": report["state"], "attempts": state()["attempt"], "download_bytes": source_state()["archive_bytes_sent"]}, flush=True)
    except Exception as error:
        report["error"] = repr(error)
        if ui is not None:
            try: ui.system_capture("failure-system.png")
            except Exception: pass
        raise
    finally:
        try: control(component_mode="normal", component_chunk_delay=0)
        except Exception as error: report["source_cleanup_error"] = type(error).__name__
        if ui is not None:
            try: ui.quit(stop=True)
            except Exception as error: report["cleanup_error"] = repr(error)
        (args.out / "report.json").write_text(json.dumps(report, ensure_ascii=False, indent=2))


if __name__ == "__main__":
    main()
