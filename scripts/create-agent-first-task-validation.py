#!/usr/bin/env python3
"""建立主流程专用空根与真实组件源；不复制历史业务、聊天或组件缓存。"""
import argparse
import secrets
import shutil
import json
import os
from pathlib import Path
import subprocess
import tempfile
import time
import uuid

ROOT = Path(__file__).resolve().parents[1]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--out", type=Path, required=True)
    choice=parser.add_mutually_exclusive_group(required=True)
    choice.add_argument("--settings-fixture-pointer", type=Path,
                        help="已有合成验收根指针，仅复用独立模型授权条件")
    choice.add_argument("--pi-auth-directory",type=Path,help="本轮由 pi 自行读取的配置目录，不复制云模型凭据")
    parser.add_argument("--pi-provider")
    parser.add_argument("--pi-model")
    parser.add_argument("--official-archives",type=Path,default=ROOT/"build/onboarding-components/archives.json")
    parser.add_argument("--name", default="MemlaneFirstTask")
    parser.add_argument("--reuse-components-from-pointer", type=Path,
                        help="仅复用已标记合成根的校验组件文件，不复制业务、状态、身份或会话")
    args = parser.parse_args()
    assert args.name.isalnum()
    out = args.out.resolve()
    out.mkdir(parents=True, exist_ok=True)
    fixture = Path(tempfile.mkdtemp(prefix="memlane-first-task-", dir=out))
    marker = {"synthetic_only": True, "no_historical_chat": True,
              "scope": "本轮App引导的正常合成任务与新会话召回"}
    (fixture / "synthetic-only.json").write_text(json.dumps(marker, ensure_ascii=False))
    for name in ("client", "source", "ui-home", "pi-agent"):
        (fixture / name).mkdir(mode=0o700)
    for name in ("repo-a", "repo-b"):
        subprocess.run(["/usr/bin/git", "init", str(fixture / name)],
                       check=True, capture_output=True)
    archives = [str(ROOT / "build/p4-code-root-component/memlane-cognee-rust-0.0.3-darwin-arm64.tar.gz"),
                str(ROOT / "build/p0-engine/memlane-model-e5-base-int8.tar.gz")]
    archives += json.loads(args.official_archives.read_text())
    assert all(Path(path).is_file() for path in archives)
    (fixture / "archives.json").write_text(json.dumps(archives, indent=2))
    command = [str(ROOT / ".local/cognee-python/bin/python"),
               str(ROOT / "scripts/onboarding-component-source.py"),
               "--root", str(fixture / "source"), "--archives", str(fixture / "archives.json")]
    with (out / "component-source.log").open("w") as log:
        process = subprocess.Popen(command, cwd=ROOT, stdin=subprocess.DEVNULL,
                                   stdout=log, stderr=log, start_new_session=True)
    ready = fixture / "source/source-info.json"
    deadline = time.monotonic() + 30
    try:
        while not ready.exists():
            if process.poll() is not None or time.monotonic() >= deadline:
                raise RuntimeError("组件源未能启动，见component-source.log")
            time.sleep(0.1)
        source = json.loads(ready.read_text())
        identifier = "io.github.zwhu.memlane.firsttask." + uuid.uuid4().hex[:12]
        config = {"root": str(fixture / "client"),
                  "component_source": source["source"],
                  "component_public_key": source["public_key"],
                  "worker_id": "cognee-rust", "model_id": "multilingual-e5-base-int8",
                  "service_plist": identifier + ".service.plist", "allow_local_test_source": True,
                  "official": {"source": source["source"], "public_key": source["public_key"],
                               "allow_local_test_source": True}}
        (fixture / "runtime-config.json").write_text(json.dumps(config, indent=2))
        if args.pi_auth_directory:
            assert args.pi_provider and args.pi_model, "明确本轮选择的 pi provider/model"
            app=out/"app/darwin-arm64"/(args.name+".app")
            settings={"environment":{"DEFAULT_USER_EMAIL":"memlane-local@example.com","DEFAULT_USER_PASSWORD":secrets.token_hex(32),"EMBEDDING_PROVIDER":"fastembed","EMBEDDING_MODEL":"BAAI/bge-small-zh-v1.5","EMBEDDING_DIMENSIONS":"512","EMBEDDING_MAX_TOKENS":"512"},
                "model_restart_required":True,"model_bridge":{"root":str(fixture/"client/official/model-bridge"),"executable":subprocess.check_output(["/bin/zsh","-c","command -v pi"],text=True).strip(),"auth_home":str(args.pi_auth_directory.resolve()),"provider":args.pi_provider,"model":args.pi_model,"helper":str(app/"Contents/Resources/bin/memlane"),"owner_state":str(fixture/"client/official/cognee-service-state.json")}}
        else:
            old = Path(args.settings_fixture_pointer.read_text().strip())
            assert json.loads((old / "synthetic-only.json").read_text())["synthetic_only"]
            settings = json.loads((old / "client/official/cognee-config.json").read_text())
        settings["root"] = str(fixture / "client/official/cognee")
        # No old cache, dataset, principal, API key files, Agent home or task
        # history is copied. Model authorization stays private and unprinted.
        for key in ("HF_HOME", "HF_HUB_OFFLINE"):
            settings["environment"].pop(key, None)
        target = fixture / "client/official/cognee-config.json"
        target.parent.mkdir(mode=0o700)
        target.write_text(json.dumps(settings))
        target.chmod(0o600)
        if args.reuse_components_from_pointer:
            previous = Path(args.reuse_components_from_pointer.read_text().strip())
            assert json.loads((previous / "synthetic-only.json").read_text())["synthetic_only"]
            for relative in ("client/runtime/components", "client/official/runtime/components"):
                shutil.copytree(previous / relative, fixture / relative, symlinks=True)
        info = {"identifier": identifier, "synthetic_only": True,
                "app": str(out / "app/darwin-arm64" / (args.name + ".app")),
                "source": source, "root": str(fixture), "component_source_pid": process.pid,
                "cache_copied": bool(args.reuse_components_from_pointer), "business_data_copied": False,
                "component_files_only_reused": bool(args.reuse_components_from_pointer),
                  "model_authorization_only_reused": not bool(args.pi_auth_directory), "pi_model_bridge":bool(args.pi_auth_directory), "distribution_in_scope": False}
        (fixture / "source-info.json").write_text(json.dumps(info, indent=2))
        (out / "fixture-path.txt").write_text(str(fixture) + "\n")
        (out / "component-source-process.json").write_text(json.dumps({"pid": process.pid,
            "argv": command, "root": str(fixture), "owned": True}, indent=2))
        print(json.dumps({"fixture": str(fixture), "identifier": identifier, "app": info["app"],
                          "source_pid": process.pid, "archive_bytes": sum(c["bytes"] for c in source["actual_components"])}, indent=2))
    except BaseException:
        process.terminate()
        process.wait(timeout=10)
        raise


if __name__ == "__main__":
    main()
