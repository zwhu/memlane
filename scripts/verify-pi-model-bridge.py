#!/usr/bin/env python3
"""用真实 pi 验证模型桥的结构化返回和活动取消，仅使用本轮合成材料。"""
import argparse
import concurrent.futures
import hashlib
import json
import os
from pathlib import Path
import shutil
import signal
import socket
import subprocess
import tempfile
import time
import urllib.error
import urllib.request

ROOT = Path(__file__).resolve().parents[1]

def call(endpoint, route, token, body=None, timeout=125):
    raw = None if body is None else json.dumps(body).encode()
    request = urllib.request.Request(endpoint + route, raw, headers={"Authorization": "Bearer " + token, "Content-Type": "application/json"})
    try:
        with urllib.request.urlopen(request, timeout=timeout) as response:
            return response.status, json.load(response)
    except urllib.error.HTTPError as error:
        return error.code, json.load(error)

def stop(process):
    if process.poll() is None:
        os.killpg(process.pid, signal.SIGTERM)
        try: process.wait(timeout=8)
        except subprocess.TimeoutExpired:
            os.killpg(process.pid, signal.SIGKILL); process.wait(timeout=5)

def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--out", required=True, type=Path)
    parser.add_argument("--provider", required=True)
    parser.add_argument("--model", required=True)
    parser.add_argument("--auth-directory", type=Path, default=Path(os.environ.get("PI_CODING_AGENT_DIR", str(Path.home() / ".pi/agent"))))
    parser.add_argument("--bridge", type=Path, default=ROOT / "build/pi-model-bridge/memlane")
    args = parser.parse_args()
    args.out.mkdir(parents=True, exist_ok=True)
    root = Path(tempfile.mkdtemp(prefix="real-pi-", dir=args.out.resolve()))
    (root / "synthetic-only.json").write_text(json.dumps({"synthetic_only": True, "no_history": True}))
    report = {"status": "failed", "root": str(root), "actual_pi": True, "credentials_copied_to_cognee": False, "cases": []}
    processes = []
    endpoint = token = ""
    try:
        auth = args.auth_directory.resolve()
        original = json.loads((auth / "settings.json").read_text())
        assert original["defaultProvider"] == args.provider and original["defaultModel"] == args.model, "本轮必须显式选择真实 pi 默认模型"
        original_digests = {name: hashlib.sha256((auth / name).read_bytes()).hexdigest()
                            for name in ("settings.json", "models.json", "auth.json") if (auth / name).is_file()}
        report["selected_provider"] = args.provider
        report["selected_model"] = args.model
        report["original_provider_config_reused"] = args.provider != "ollama"
        if args.provider == "ollama":
            assert args.model == "qwen3:0.6b", "只复用用户实际选择的固定模型"
            original = json.loads((auth / "settings.json").read_text())
            assert original["defaultProvider"] == args.provider and original["defaultModel"] == args.model
            source = Path(os.environ.get("OLLAMA_MODELS", str(Path.home() / ".ollama/models")))
            relative = Path("manifests/registry.ollama.ai/library/qwen3/0.6b")
            manifest = json.loads((source / relative).read_text())
            models = root / "models"; (models / relative).parent.mkdir(parents=True); (models / "blobs").mkdir()
            shutil.copyfile(source / relative, models / relative)
            for item in [manifest["config"], *manifest["layers"]]:
                filename = item["digest"].replace(":", "-")
                assert (source / "blobs" / filename).stat().st_size == item["size"]
                (models / "blobs" / filename).symlink_to(source / "blobs" / filename)
            with socket.socket() as probe: probe.bind(("127.0.0.1", 0)); port = probe.getsockname()[1]
            env = {**os.environ, "OLLAMA_HOST": "127.0.0.1:" + str(port), "OLLAMA_MODELS": str(models), "HOME": str(root / "model-home")}
            (root / "model-home").mkdir()
            log = (root / "ollama.log").open("w")
            process = subprocess.Popen([shutil.which("ollama"), "serve"], env=env, stdout=log, stderr=log, start_new_session=True)
            processes.append(process)
            deadline = time.monotonic() + 40
            while True:
                try:
                    urllib.request.urlopen("http://127.0.0.1:" + str(port) + "/api/version", timeout=1).close(); break
                except Exception:
                    if process.poll() is not None or time.monotonic() > deadline: raise RuntimeError("isolated Ollama did not start")
                    time.sleep(.1)
            provider = json.loads((auth / "models.json").read_text())["providers"]["ollama"]
            selected = next(value for value in provider["models"] if value["id"] == args.model)
            auth = root / "pi-agent"; auth.mkdir(mode=0o700)
            safe = {key: value for key, value in selected.items() if key in ("id", "name", "reasoning", "input", "contextWindow", "maxTokens", "cost")}
            (auth / "settings.json").write_text(json.dumps({"defaultProvider": args.provider, "defaultModel": args.model}))
            (auth / "models.json").write_text(json.dumps({"providers": {"ollama": {"api": "openai-completions", "baseUrl": "http://127.0.0.1:" + str(port) + "/v1", "apiKey": "ollama", "models": [safe]}}}))
            report["local_model_download_bytes"] = 0
        bridge_root = root / "official/model-bridge"; bridge_root.mkdir(parents=True, mode=0o700)
        cfg = {"root": str(bridge_root), "executable": shutil.which("pi"), "auth_home": str(auth), "provider": args.provider, "model": args.model, "helper": str(args.bridge.resolve()), "owner_state": str(root / "official/cognee-service-state.json")}
        config = bridge_root / "config.json"; config.write_text(json.dumps(cfg)); config.chmod(0o600)
        log = (root / "bridge.log").open("w")
        bridge = subprocess.Popen([str(args.bridge.resolve()), "model-bridge", "--config", str(config)], stdout=log, stderr=log, start_new_session=True)
        processes.append(bridge)
        deadline = time.monotonic() + 20
        while not (bridge_root / "state.json").exists():
            if bridge.poll() is not None or time.monotonic() > deadline: raise RuntimeError("real bridge did not start")
            time.sleep(.1)
        endpoint = json.loads((bridge_root / "state.json").read_text())["endpoint"]
        token = (bridge_root / "access.token").read_text()
        schema = {"type": "object", "properties": {"ready": {"type": "boolean", "const": True}}, "required": ["ready"], "additionalProperties": False}
        body = {"model": "memlane-pi", "messages": [{"role": "system", "content": "Return only the JSON object requested by the user."}, {"role": "user", "content": "Set ready to true."}], "response_format": {"type": "json_schema", "json_schema": {"name": "probe", "schema": schema}}}
        status, response = call(endpoint, "/v1/chat/completions", token, body)
        (root / "probe-response.json").write_text(json.dumps({"status": status, "response": response}, indent=2))
        assert status == 200 and json.loads(response["choices"][0]["message"]["content"]) == {"ready": True}, "real model probe failed"
        probe_receipts = [json.loads(line) for line in (bridge_root / "events.jsonl").read_text().splitlines()]
        probe_terminal = [value for value in probe_receipts if "http_status" in value]
        assert len(probe_terminal) == 1
        assert probe_terminal[0].get("native_schema_requested") and probe_terminal[0].get("native_schema_response_status") == 200, "完整 Schema 未实际绑定到 Provider 请求并取得响应"
        report["cases"].append({"name": "real-pi-structured-probe", "passed": True})
        body["messages"][1]["content"] = "Write a detailed 8000-character explanation of how sorting algorithms work inside the text property. Keep producing text until the requested length is reached."
        body["response_format"]["json_schema"]["schema"] = {"type": "object", "properties": {"text": {"type": "string", "minLength": 8000}}, "required": ["text"], "additionalProperties": False}
        with concurrent.futures.ThreadPoolExecutor(max_workers=1) as pool:
            before = (bridge_root / "events.jsonl").stat().st_size
            running = pool.submit(call, endpoint, "/v1/chat/completions", token, body)
            deadline = time.monotonic() + 60
            while True:
                data = (bridge_root / "events.jsonl").read_text()[before:]
                if any(json.loads(line).get("event") == "message_update" for line in data.splitlines()): break
                if running.done() or time.monotonic() > deadline: raise RuntimeError("no actual streaming inference to cancel")
                time.sleep(.05)
            assert call(endpoint, "/v1/cancel", token, {})[0] == 200
            status, response = running.result(timeout=12)
            assert status == 408, "cancel returned a successful late result"
        receipts = [json.loads(line) for line in (bridge_root / "events.jsonl").read_text().splitlines()]
        terminal = [value for value in receipts if "http_status" in value]
        assert len(terminal) == 2 and all(value["reaped"] and value["group_gone"] for value in terminal)
        assert terminal[-1]["abort_sent"] and terminal[-1]["abort_acknowledged"]
        assert all(value.get("native_schema_requested") and value.get("native_schema_response_status") == 200 for value in terminal)
        assert all(value["rpc_resolved_provider"] == args.provider and value["rpc_resolved_model"] == args.model for value in terminal)
        assert all(not name.startswith("tool_execution") for value in terminal for name in value["events"])
        report["cases"].append({"name": "real-streaming-abort-and-process-reaping", "passed": True})
        report["receipts"] = terminal
        report["status"] = "passed"
    except Exception as error:
        report["failure"] = type(error).__name__ + ": " + str(error)
    finally:
        def audit_failure(message):
            report["status"] = "failed"
            if report.get("failure"):
                report.setdefault("additional_failures", []).append(message)
            else:
                report["failure"] = message
        if endpoint and token:
            try: call(endpoint, "/shutdown", token, {}, timeout=10)
            except Exception: pass
        for process in reversed(processes): stop(process)
        report["cleanup"] = {"owned_processes_exited": all(process.poll() is not None for process in processes)}
        if "original_digests" in locals():
            try:
                report["original_agent_config_unchanged"] = all(
                    hashlib.sha256((args.auth_directory.resolve() / name).read_bytes()).hexdigest() == digest
                    for name, digest in original_digests.items())
            except OSError as error:
                report["original_agent_config_unchanged"] = False
                report["config_audit_error"] = type(error).__name__
            if not report["original_agent_config_unchanged"]:
                audit_failure("original Agent configuration changed or could not be audited during validation")
        if "bridge_root" in locals() and (bridge_root / "events.jsonl").is_file():
            try:
                report["receipts"] = []
                for number, line in enumerate((bridge_root / "events.jsonl").read_text().splitlines(), 1):
                    try:
                        value = json.loads(line)
                        if not isinstance(value, dict):
                            raise ValueError("receipt must be an object")
                        if "http_status" in value:
                            report["receipts"].append(value)
                    except ValueError:
                        report.setdefault("receipt_audit_errors", []).append({"line": number, "error": "invalid_json_receipt"})
                        audit_failure("bridge receipt audit encountered invalid JSON")
            except (OSError, UnicodeError) as error:
                report.setdefault("receipt_audit_errors", []).append({"error": type(error).__name__})
                audit_failure("bridge receipts could not be audited")
        (root / "report.json").write_text(json.dumps(report, ensure_ascii=False, indent=2))
        (args.out / "latest-probe-path.txt").write_text(str(root) + "\n")
        print(json.dumps({"status": report["status"], "report": str(root / "report.json"), "failure": report.get("failure")}, ensure_ascii=False), flush=True)
    return 0 if report["status"] == "passed" else 1

if __name__ == "__main__": raise SystemExit(main())
