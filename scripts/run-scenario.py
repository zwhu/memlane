#!/usr/bin/env python3
"""Execute an explicitly selected continuous scenario; never retry or infer delivery."""
import argparse
import datetime
import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys
import time
import uuid

ROOT = Path(__file__).resolve().parents[1]


def load_plan(path):
    plan = json.loads(path.read_text())
    for key in ("id", "requirement", "baseline", "proves", "does_not_prove"):
        if not isinstance(plan.get(key), str) or not plan[key].strip():
            raise ValueError(f"missing nonempty text: {key}")
    for group in ("preflight", "steps", "cleanup"):
        items = plan.get(group)
        if not isinstance(items, list) or not items:
            raise ValueError(f"{group} must contain explicit checks/actions")
        for item in items:
            if not isinstance(item, dict) or not isinstance(item.get("id"), str) or not item["id"]:
                raise ValueError(f"invalid {group} item")
            command = item.get("argv")
            if not isinstance(command, list) or not command or any(not isinstance(v, str) or not v for v in command):
                raise ValueError(f"invalid argv: {item['id']}")
    ids = [item["id"] for group in ("preflight", "steps", "cleanup") for item in plan[group]]
    if len(ids) != len(set(ids)):
        raise ValueError("duplicate action id")
    return plan


def execute(plan, output, cwd=ROOT):
    # A fresh directory preserves every attempt; evidence is task-owned, not scratch.
    output.mkdir(parents=True, exist_ok=False)
    frozen = json.dumps(plan, ensure_ascii=False, indent=2).encode()
    (output / "plan.json").write_bytes(frozen)
    report = {"plan_sha256": hashlib.sha256(frozen).hexdigest(),
              "started_at": datetime.datetime.now(datetime.timezone.utc).isoformat(),
              "proves": plan["proves"], "does_not_prove": plan["does_not_prove"],
              "status": "running", "actions": []}

    def save():
        temporary = output / "report.tmp"
        temporary.write_text(json.dumps(report, ensure_ascii=False, indent=2))
        temporary.replace(output / "report.json")

    def action(group, index, item):
        log = f"{group}-{index:02d}.log"
        entry = {"group": group, "id": item["id"], "log": log, "status": "running"}
        report["actions"].append(entry)
        save()
        start = time.monotonic()
        env = dict(os.environ, MEMLANE_SCENARIO_EVIDENCE=str(output.resolve()))
        try:
            with (output / log).open("wb") as stream:
                result = subprocess.run(item["argv"], cwd=cwd, env=env,
                                        stdout=stream, stderr=subprocess.STDOUT)
            entry.update(status="passed" if result.returncode == 0 else "failed",
                         exit_code=result.returncode)
        except OSError as error:
            entry.update(status="failed", error_type=type(error).__name__)
        except BaseException:
            entry["status"] = "interrupted"
            raise
        finally:
            entry["seconds"] = round(time.monotonic() - start, 3)
            save()
        return entry["status"] == "passed"

    failed = False
    save()
    try:
        for group in ("preflight", "steps"):
            for index, item in enumerate(plan[group]):
                if not action(group, index, item):
                    failed = True
                    report["failure_kind"] = "precondition" if group == "preflight" else "unclassified"
                    break
            if failed:
                break
    except BaseException:
        failed = True
        report["failure_kind"] = "interrupted"
        raise
    finally:
        # Cleanup must be idempotent and limited to resources owned by this scenario.
        try:
            for index, item in enumerate(plan["cleanup"]):
                if not action("cleanup", index, item):
                    failed = True
                    report.setdefault("failure_kind", "cleanup")
        except BaseException:
            failed = True
            report.setdefault("failure_kind", "interrupted")
            raise
        finally:
            report["status"] = "failed" if failed else "commands_passed"
            save()
    return 1 if failed else 0


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("plan", type=Path)
    parser.add_argument("--check", action="store_true", help="validate structure only; runs no commands")
    args = parser.parse_args()
    plan = load_plan(args.plan)
    if args.check:
        print("结构检查通过；未验证需求理解、环境或业务。")
        return 0
    if os.environ.get("MEMLANE_DISK_TASK") != "1":
        parser.error("run via python3 scripts/task.py -- python3 scripts/run-scenario.py PLAN")
    output = ROOT / "build/scenarios" / str(uuid.uuid4())
    print(output, flush=True)
    return execute(plan, output)


if __name__ == "__main__":
    sys.exit(main())
