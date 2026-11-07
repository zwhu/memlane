#!/usr/bin/env python3
"""Apply the recorded compatibility patches only to exact pinned upstream files."""
import hashlib
import json
from pathlib import Path
import subprocess

ROOT = Path(__file__).resolve().parents[1]


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def main():
    manifest = json.loads((ROOT / "deps/cognee-rs/patches.json").read_text())
    source = ROOT / ".local/sources" / ("cognee-rs-" + manifest["source_commit"])
    for entry in manifest["patches"]:
        target = source / entry["path"]
        patch = ROOT / "deps/cognee-rs" / entry["patch"]
        if digest(patch) != entry["patch_sha256"]:
            raise SystemExit("patch hash mismatch")
        if digest(target) == entry["after_sha256"]:
            print("patch already verified:", entry["patch"])
            continue
        if digest(target) != entry["before_sha256"]:
            raise SystemExit("upstream file changed; refusing patch: " + entry["path"])
        subprocess.run(["/usr/bin/patch", "-p1", "--forward", "--input", str(patch)], cwd=source, check=True)
        if digest(target) != entry["after_sha256"]:
            raise SystemExit("patched source hash mismatch")
        print("patch applied and verified:", entry["patch"])


if __name__ == "__main__":
    main()
