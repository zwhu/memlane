#!/usr/bin/env python3
"""建立本轮空业务/空组件缓存根与真实归档受控源，不预置记忆或 Agent hook。"""
import argparse
import json
from pathlib import Path
import subprocess
import tempfile
import uuid

ROOT = Path(__file__).resolve().parents[1]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--out", type=Path, default=ROOT / "build/onboarding-memory-validation")
    parser.add_argument("--name", default="MemlaneOnboardingMemory")
    parser.add_argument("--app-directory", default="app-2")
    args = parser.parse_args()
    assert args.name.isalnum() and Path(args.app_directory).name == args.app_directory
    args.out.mkdir(parents=True, exist_ok=True)
    fixture = Path(tempfile.mkdtemp(prefix="memlane-official-agent-onboarding-"))
    (fixture / "synthetic-only.json").write_text(json.dumps({"synthetic_only": True, "scope": "this independent Goal only; no historical chat collection"}))
    for path in ("client", "core/data", "core/system", "core/cache", "agent-home", "prepare-source"):
        (fixture / path).mkdir(parents=True, exist_ok=True)
    for name in ("repo-a", "repo-b"):
        subprocess.run(["git", "init", str(fixture / name)], capture_output=True, check=True)
    command = [str(ROOT / ".local/cognee-python/bin/python"), str(ROOT / "scripts/p0-client-source.py"), "--root", str(fixture / "prepare-source")]
    with (args.out / "component-source.log").open("w") as log:
        process = subprocess.Popen(command, cwd=ROOT, stdin=subprocess.DEVNULL, stdout=log, stderr=log, start_new_session=True)
    (args.out / "fixture-path.txt").write_text(str(fixture) + "\n")
    (args.out / "component-source-process.json").write_text(json.dumps({"pid": process.pid, "argv": command, "root": str(fixture), "owned": True, "real_worker_model_archives": True, "release_signature": "controlled source, not public distribution"}, indent=2))
    # The source initializes its own key/feed asynchronously. A bounded wait
    # only waits for that real process's file, never fabricates readiness.
    import time
    deadline = time.monotonic() + 20
    while not (fixture / "prepare-source/source-info.json").exists():
        if process.poll() is not None or time.monotonic() > deadline:
            raise RuntimeError("real component source failed to initialize")
        time.sleep(0.1)
    source = json.loads((fixture / "prepare-source/source-info.json").read_text())
    config = json.loads((fixture / "prepare-source/runtime-config.json").read_text())
    config.update(root=str(fixture / "client"))
    config.pop("update_manifest_url", None)
    config.pop("update_ca_pem", None)
    (fixture / "runtime-config.json").write_text(json.dumps(config, indent=2))
    meta = {"identifier": source["identifier"], "synthetic_only": True, "app": str(args.out / args.app_directory / "darwin-arm64" / (args.name + ".app")), "source": source, "root": str(fixture)}
    (fixture / "source-info.json").write_text(json.dumps(meta, indent=2))
    (args.out / "initial-empty-state.json").write_text(json.dumps({"root": str(fixture), "client_entries": [], "client_component_cache_empty": True, "business_empty": True, "archive_reuse": "fixed real compiled worker/model archives from this repository", "public_distribution": False}, indent=2))
    print(json.dumps({"fixture": str(fixture), "identifier": meta["identifier"], "app": meta["app"], "component_source_pid": process.pid}, indent=2))


if __name__ == "__main__":
    main()
