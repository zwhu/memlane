#!/usr/bin/env python3
"""Freeze the pinned Python comparison worker; no model, venv or pip at runtime."""
import os
from pathlib import Path
import subprocess

ROOT = Path(__file__).resolve().parents[1]


def main():
    python = ROOT / ".local/cognee-python/bin/python"
    build = ROOT / ".cache/python-worker-build"
    build.mkdir(parents=True, exist_ok=True)
    home = build / "private-home"
    home.mkdir(exist_ok=True)
    empty_env = build / "isolated.env"
    empty_env.write_text("")
    environment = {"COGNEE_ENV_FILE": str(empty_env), "PATH": "/usr/bin:/bin", "HOME": str(home),
                   "PYTHON_DOTENV_DISABLED": "1", "COGNEE_TRACING_ENABLED": "false",
                   "HF_HUB_OFFLINE": "1", "COGNEE_LOGS_DIR": str(build / "logs"),
                   "SYSTEM_ROOT_DIRECTORY": str(build / "system"),
                   "DATA_ROOT_DIRECTORY": str(build / "raw"),
                   "CACHE_ROOT_DIRECTORY": str(build / "cache"),
                   "LOGS_ROOT_DIRECTORY": str(build / "logs"),
                   "LITELLM_LOCAL_MODEL_COST_MAP": "true"}
    command = [str(python), "-m", "PyInstaller", "--noconfirm", "--onedir", "--noupx",
               "--name", "memlane-cognee-worker", "--distpath", str(ROOT / "build/python-worker"),
               "--workpath", str(build / "work"), "--specpath", str(build),
               "--add-binary", str(ROOT / ".local/cognee-python/bin/enola")+":tools"]
    for module in ("cognee", "fastembed", "litellm", "lancedb", "lance", "tokenizers", "onnxruntime"):
        command += ["--collect-all", module]
    for distribution in ("cognee", "fastembed", "enola-cli"):
        command += ["--copy-metadata", distribution]
    command.append(str(ROOT / "engine/python/worker.py"))
    log = ROOT / "build/p0-engine/python-worker-build.log"
    print("Building pinned onedir worker; log:", log, flush=True)
    with log.open("w") as output:
        result = subprocess.run(command, cwd=build, env=environment, stdout=output, stderr=subprocess.STDOUT)
    if result.returncode:
        print(log.read_text()[-7000:])
        raise SystemExit(result.returncode)
    print("Frozen worker:", ROOT / "build/python-worker/memlane-cognee-worker", flush=True)


if __name__ == "__main__":
    main()
