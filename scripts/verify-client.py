#!/usr/bin/env python3
"""Run the Go worker client check against relocated real worker/model files."""
import hashlib
import json
from pathlib import Path
import shutil
import subprocess
import tarfile
import tempfile

ROOT = Path(__file__).resolve().parents[1]


def main():
    with tempfile.TemporaryDirectory(prefix="memlane-client-p0-") as name:
        root = Path(name)
        archive = ROOT / "build/p0-engine/memlane-cognee-rust-0.0.1-darwin-arm64.tar.gz"
        with tarfile.open(archive) as source:
            manifest = json.load(source.extractfile("component.json"))
            for entry in manifest["files"]:
                member = source.getmember(entry["path"])
                assert member.isfile() and Path(entry["path"]).name == entry["path"]
                path = root / entry["path"]
                with source.extractfile(member) as data, path.open("wb") as out:
                    shutil.copyfileobj(data, out)
                assert hashlib.sha256(path.read_bytes()).hexdigest() == entry["sha256"]
                path.chmod(0o755 if entry["path"] == manifest["executable"] else 0o644)
        shutil.copytree(ROOT / ".local/models/multilingual-e5-base-int8", root / "model")
        binary = root / "client-check"
        shutil.copy2(ROOT / "build/p0-engine/worker-client-check", binary)
        result = subprocess.run([str(binary), "--worker", str(root / manifest["executable"]),
                                 "--library", str(root / manifest["library"]),
                                 "--model", str(root / "model"), "--data", str(root / "data")],
                                env={"PATH": "/usr/bin:/bin"}, cwd=root,
                                capture_output=True, text=True, timeout=90)
        try:
            report = json.loads(result.stdout)
        except Exception:
            report = {"status": "failed", "stdout": result.stdout, "stderr": result.stderr}
        report["command_exit_code"] = result.returncode
        if result.stderr:
            report["stderr"] = result.stderr.replace(name, "<temporary>")
    (ROOT / "build/p0-engine/client-verification.json").write_text(json.dumps(report, ensure_ascii=False, indent=2)+"\n")
    print(json.dumps(report, ensure_ascii=False, indent=2))
    if result.returncode or report["status"] != "passed":
        raise SystemExit(1)


if __name__ == "__main__":
    main()
