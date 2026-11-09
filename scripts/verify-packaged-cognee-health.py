#!/usr/bin/env python3
"""在新合成根对真实打包 Cognee 发起一次完整基础健康请求；不调用模型。"""
import argparse
import json
import os
from pathlib import Path
import signal
import subprocess
import time
import urllib.error
import urllib.request


def group_members(group):
    lines = subprocess.check_output(["ps", "-axo", "pid=,pgid=,uid=,comm="], text=True).splitlines()
    return [line.strip() for line in lines if len(line.split()) >= 4 and line.split()[1] == str(group)]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--pointer", type=Path, required=True, help="已标记合成验收根，只复用组件文件")
    parser.add_argument("--out", type=Path, required=True, help="新的独立诊断根；已存在时拒绝覆盖")
    parser.add_argument("--request-timeout", type=int, default=125)
    parser.add_argument("--component-directory", type=Path, help="本轮新的已打包组件目录，包含 component.json；只读对照")
    args = parser.parse_args()
    if not 5 <= args.request_timeout <= 180:
        parser.error("request timeout must be between 5 and 180 seconds")
    previous = Path(args.pointer.read_text().strip()).resolve()
    if not json.loads((previous / "synthetic-only.json").read_text()).get("synthetic_only"):
        parser.error("only marked synthetic roots are allowed")
    component = args.component_directory.resolve() if args.component_directory else previous / "client/official/runtime/components/cognee-service/1.6.7-memlane.5"
    descriptor = json.loads((component / "component.json").read_text())
    executable = (component / descriptor["executable"]).resolve()
    if not executable.is_relative_to(component.resolve()) or not executable.is_file():
        parser.error("packaged executable must belong to the marked component")
    out = args.out.resolve()
    out.mkdir(mode=0o700, parents=True, exist_ok=False)
    (out / "synthetic-only.json").write_text(json.dumps({"synthetic_only": True, "no_business_data": True,
        "no_model_inference": True, "compatible_component_files_reused": True}))
    settings = json.loads((previous / "client/official/cognee-config.json").read_text())
    settings["root"] = str(out / "cognee")
    settings.pop("model_bridge", None)
    settings.pop("model_restart_required", None)
    for key in ("LLM_API_KEY", "LLM_PROVIDER", "LLM_MODEL", "LLM_ENDPOINT"):
        settings["environment"].pop(key, None)
    config = out / "cognee-config.json"
    config.write_text(json.dumps(settings))
    config.chmod(0o600)
    report = {"status": "failed", "actual_packaged_service": True, "actual_app": False,
              "component_version": descriptor["version"], "root": str(out), "llm_invoked": False,
              "request_count": 0, "cleanup_errors": []}
    process = None
    started = time.monotonic()
    with (out / "service.log").open("w") as log:
        try:
            process = subprocess.Popen([str(executable), "--config", str(config)], cwd=out,
                env={"PATH": "/usr/bin:/bin", "HOME": str(out), "PYTHON_DOTENV_DISABLED": "1"},
                stdout=log, stderr=log, start_new_session=True)
            report["pid"] = process.pid
            state_path = out / "cognee-service-state.json"
            while not state_path.exists():
                if process.poll() is not None or time.monotonic() - started > 90:
                    raise RuntimeError("packaged service did not publish its owned endpoint")
                time.sleep(.1)
            state = json.loads(state_path.read_text())
            if state["pid"] != process.pid or state["root"] != settings["root"]:
                raise RuntimeError("service state ownership differs")
            # One request: repeated short timeouts can trigger overlapping
            # initialization probes and obscure the original health failure.
            report["request_count"] = 1
            request_started = time.monotonic()
            try:
                with urllib.request.urlopen(state["endpoint"] + "/health", timeout=args.request_timeout) as response:
                    status, body = response.status, json.load(response)
            except urllib.error.HTTPError as error:
                status, body = error.code, json.load(error)
            report.update(http_status=status, health_response=body,
                          request_seconds=time.monotonic() - request_started)
            if status == 200 and body.get("status") == "ready" and body.get("health") == "healthy":
                report["status"] = "passed"
        except Exception as error:
            report["failure"] = type(error).__name__ + ": " + str(error)
        finally:
            if process is not None:
                try:
                    if process.poll() is None:
                        try:
                            os.killpg(process.pid, signal.SIGTERM)
                        except ProcessLookupError:
                            pass
                        process.wait(timeout=25)
                except Exception as error:
                    report["cleanup_errors"].append(type(error).__name__ + ": " + str(error))
                    if process.poll() is None:
                        try:
                            os.killpg(process.pid, signal.SIGKILL)
                        except ProcessLookupError:
                            pass
                        try:
                            process.wait(timeout=8)
                        except subprocess.TimeoutExpired:
                            report["cleanup_errors"].append("owned process did not exit after kill")
                report["process_reaped"] = process.poll() is not None
                try:
                    members = group_members(process.pid)
                    report["remaining_group_members"] = members
                    report["group_gone"] = not members
                except (OSError, subprocess.SubprocessError) as error:
                    report["cleanup_errors"].append("group observer: " + type(error).__name__)
            if report["cleanup_errors"] or not report.get("group_gone"):
                report["status"] = "failed"
            report["seconds"] = time.monotonic() - started
            (out / "report.json").write_text(json.dumps(report, ensure_ascii=False, indent=2))
            print(json.dumps({"status": report["status"], "report": str(out / "report.json"),
                "http_status": report.get("http_status"), "failure": report.get("failure"),
                "group_gone": report.get("group_gone")}, ensure_ascii=False), flush=True)
    return 0 if report["status"] == "passed" else 1


if __name__ == "__main__":
    raise SystemExit(main())
