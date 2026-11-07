#!/usr/bin/env python3
"""Prepare an isolated, fixed Python comparison; never changes the default Python."""
from pathlib import Path
import argparse
import os
import shutil
import subprocess

ROOT = Path(__file__).resolve().parents[1]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--resolve", action="store_true", help="explicitly regenerate the dependency lock")
    parser.add_argument("--index-url", default="https://pypi.org/simple", help="download mirror; existing lock hashes still required")
    args = parser.parse_args()
    uv = shutil.which("uv")
    if not uv:
        raise SystemExit("uv is required for the isolated comparison build")
    env = dict(os.environ, UV_CACHE_DIR=str(ROOT / ".cache/uv"),
               UV_PYTHON_INSTALL_DIR=str(ROOT / ".local/python"),
               UV_PYTHON_BIN_DIR=str(ROOT / ".local/python-bin"), UV_NO_CONFIG="true",
               UV_CONCURRENT_DOWNLOADS="4", UV_HTTP_TIMEOUT="300")
    commands = [
        [uv, "python", "install", "3.12.12", "--no-bin"],
        [uv, "venv", str(ROOT / ".local/cognee-python"), "--python", "3.12.12", "--managed-python"],
        [uv, "pip", "compile", str(ROOT / "engine/python/requirements.in"),
         "--python-version", "3.12", "--generate-hashes", "--index-url", args.index_url,
         "--output-file", str(ROOT / "engine/python/requirements.lock")],
        [uv, "pip", "sync", "--python", str(ROOT / ".local/cognee-python/bin/python"),
         "--require-hashes", "--index-url", args.index_url, str(ROOT / "engine/python/requirements.lock")],
    ]
    for command in commands:
        if command[1] == "venv" and (ROOT / ".local/cognee-python/bin/python").exists():
            print("reusing isolated Python environment", flush=True)
            continue
        if command[1:3] == ["pip", "compile"]:
            lock = ROOT / "engine/python/requirements.lock"
            if lock.exists() and not args.resolve:
                lines = lock.read_text().splitlines()
                for pin in (ROOT / "engine/python/requirements.in").read_text().splitlines():
                    if pin and not pin.startswith("#") and not any(line.startswith(pin+" ") or line == pin for line in lines):
                        raise SystemExit("lock does not match direct requirements; rerun with --resolve")
                print("reusing frozen dependency lock", flush=True)
                continue
            log = ROOT / ".cache/python-engine-resolve.log"
            log.parent.mkdir(parents=True, exist_ok=True)
            print("resolving dependency lock; full output:", log, flush=True)
            with log.open("w") as out:
                subprocess.run(command, cwd=ROOT, env=env, stdout=out, stderr=out, check=True)
            continue
        print("running:", " ".join(command), flush=True)
        subprocess.run(command, cwd=ROOT, env=env, check=True)


if __name__ == "__main__":
    main()
