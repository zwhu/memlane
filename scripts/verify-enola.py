#!/usr/bin/env python3
"""Evaluate the pinned Python route's code extractor only on synthetic files."""
import argparse
import json
from pathlib import Path
import shutil
import subprocess
import tempfile
import time

ROOT = Path(__file__).resolve().parents[1]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", type=Path, default=ROOT / ".local/cognee-python/bin/enola")
    args = parser.parse_args()
    report = {"status":"failed","synthetic_only":True,"worker_graph_ready":False}
    with tempfile.TemporaryDirectory(prefix="memlane-enola-p0-") as name:
        root = Path(name)
        binary = root / "enola"
        shutil.copy2(args.binary, binary)
        binary.chmod(0o755)
        repo = root / "synthetic-repo"
        repo.mkdir()
        (repo / "go.mod").write_text("module example.invalid/memlane-p0\n\ngo 1.23\n")
        (repo / "main.go").write_text('package main\nfunc greet() string {return "synthetic"}\nfunc main(){println(greet())}\n')
        env = {"PATH":"/usr/bin:/bin","HOME":str(root / "private-home"),
               "ENOLA_NO_UPDATE_CHECK":"1","ENOLA_NO_PROMPTS":"1"}
        commands = []
        def run(arguments):
            started = time.monotonic()
            p = subprocess.run([str(binary)]+arguments,cwd=repo,env=env,capture_output=True,text=True,timeout=60)
            commands.append({"args":arguments,"exit_code":p.returncode,"wall_ms":(time.monotonic()-started)*1000,
                             "stdout":p.stdout.replace(name,"<temporary>"),"stderr":p.stderr.replace(name,"<temporary>")})
            assert p.returncode == 0, commands[-1]
            return p.stdout
        try:
            report["version"] = json.loads(run(["--version","--json"]))
            run(["--generate",str(repo)])
            snapshot = repo / ".enola"
            facts = [json.loads(line) for line in (snapshot / "facts.jsonl").read_text().splitlines() if line]
            report["receipt"] = json.loads((snapshot / "receipt.json").read_text())
            report["facts"] = facts
            assert facts, "no extracted facts"
            symbols = [f for f in facts if "greet" in str(f.get("name",""))]
            assert symbols, "greet function absent"
            assert any("greet" in json.dumps(f.get("relations",[])) for f in facts if "main" in str(f.get("name",""))), "call relation absent"
            report.update(status="passed",relocated_binary=True,local_symbols_and_calls=True,
                          actual_output="synthetic-repo/.enola",repo_writes=True,
                          limit="Enola extraction demonstrated; Cognee ingestion, graph query, automatic refresh and external output still require adaptation")
        except Exception as exc:
            report["error"] = repr(exc)
        report["commands"] = commands
    (ROOT / "build/p0-engine/enola-verification.json").write_text(json.dumps(report,ensure_ascii=False,indent=2)+"\n")
    print(json.dumps(report,ensure_ascii=False,indent=2))
    if report["status"] != "passed":
        raise SystemExit(1)


if __name__ == "__main__":
    main()
