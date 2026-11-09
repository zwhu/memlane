#!/usr/bin/env python3
"""构建真实固定 Cognee 1.6.7 服务组件，模型和业务数据不进入归档。"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import subprocess

ROOT = Path(__file__).resolve().parents[1]


def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--out",type=Path,default=ROOT/"build/cognee-service-component")
    parser.add_argument("--report-out",type=Path,default=ROOT/"build/onboarding-memory-validation")
    args=parser.parse_args()
    source_hashes = {name: hashlib.sha256((ROOT / "engine/python" / name).read_bytes()).hexdigest()
                     for name in ("cognee_service.py", "cognee_browsing.py")}
    build = ROOT / ".cache/cognee-service-build"
    build.mkdir(parents=True, exist_ok=True)
    home = build / "private-home"
    home.mkdir(exist_ok=True)
    env = {"HOME": str(home), "PATH": "/usr/bin:/bin", "PYTHON_DOTENV_DISABLED": "1",
           "HF_HUB_OFFLINE": "1", "COGNEE_TRACING_ENABLED": "false", "TELEMETRY_DISABLED": "true",
           "LITELLM_LOCAL_MODEL_COST_MAP": "true", "SYSTEM_ROOT_DIRECTORY": str(build / "system"),
           "DATA_ROOT_DIRECTORY": str(build / "raw"), "CACHE_ROOT_DIRECTORY": str(build / "cache"),
           "LOGS_ROOT_DIRECTORY": str(build / "logs")}
    encoding_cache = build / "tiktoken-cache"
    encoding_cache.mkdir(exist_ok=True)
    env["TIKTOKEN_CACHE_DIR"] = str(encoding_cache)
    subprocess.run([str(ROOT / ".local/cognee-python/bin/python"), "-c",
                    "import tiktoken; tiktoken.get_encoding('cl100k_base'); tiktoken.get_encoding('o200k_base')"], env=env, check=True, timeout=180)
    command = [str(ROOT / ".local/cognee-python/bin/python"), "-m", "PyInstaller", "--noconfirm", "--onedir", "--noupx",
               "--name", "memlane-cognee-service", "--distpath", str(args.out.resolve()),
               "--workpath", str(build / "work"), "--specpath", str(build),
               "--paths", str(ROOT / "engine/python")]
    command += ["--add-data", str(encoding_cache) + ":tiktoken-cache", "--hidden-import", "tiktoken_ext.openai_public"]
    for module in ("cognee", "fastembed", "litellm", "uvicorn", "fastapi", "lancedb", "lance", "onnxruntime", "tokenizers", "sqlalchemy", "aiosqlite", "alembic", "ladybug", "kuzu", "tiktoken", "tiktoken_ext"):
        command += ["--collect-all", module]
    for distribution in ("cognee", "fastembed", "litellm", "uvicorn"):
        command += ["--copy-metadata", distribution]
    command.append(str(ROOT / "engine/python/cognee_service.py"))
    out = args.report_out.resolve()
    out.mkdir(parents=True, exist_ok=True)
    with (out / "cognee-service-build.log").open("w") as log:
        result = subprocess.run(command, cwd=build, env=env, stdout=log, stderr=subprocess.STDOUT)
    if any(hashlib.sha256((ROOT / "engine/python" / name).read_bytes()).hexdigest() != value for name, value in source_hashes.items()):
        raise RuntimeError("构建期间入口或适配源码变化，需要重新构建")
    (out / "cognee-service-build-result.json").write_text(json.dumps({"exit": result.returncode, "command": command,
        "entry_sha256": hashlib.sha256((ROOT / "engine/python/cognee_service.py").read_bytes()).hexdigest(),
        "adapter_sha256": hashlib.sha256((ROOT / "engine/python/cognee_browsing.py").read_bytes()).hexdigest()}, indent=2))
    print("Cognee service freeze exit", result.returncode, flush=True)
    raise SystemExit(result.returncode)


if __name__ == "__main__":
    main()
