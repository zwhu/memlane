#!/usr/bin/env python3
"""Measure an experimental Cognee CLI + ONNX bundle, without model weights."""

import hashlib
import json
from pathlib import Path
import tarfile

ROOT = Path(__file__).resolve().parents[1]
SOURCE = ROOT / ".local/sources/cognee-rs-151905c302fc9b5b9aad88d6b13931c09f0eac30"
RUNTIME = ROOT / ".local/runtimes/onnxruntime-osx-arm64-1.28.0"


def main():
    files = {
        "cognee-cli": ROOT / ".cache/cognee-target/release/cognee-cli",
        "libonnxruntime.1.28.0.dylib": RUNTIME / "lib/libonnxruntime.1.28.0.dylib",
        "LICENSE-MIT.cognee": SOURCE / "LICENSE-MIT",
        "LICENSE-APACHE.cognee": SOURCE / "LICENSE-APACHE",
        "LICENSE.onnxruntime": RUNTIME / "LICENSE",
        "ThirdPartyNotices.onnxruntime.txt": RUNTIME / "ThirdPartyNotices.txt",
    }
    for path in files.values():
        if not path.is_file():
            raise SystemExit("缺少产物，请先准备并构建引擎：" + str(path))
    out = ROOT / "build/p0-engine"
    out.mkdir(parents=True, exist_ok=True)
    archive = out / "cognee-cli-p0-darwin-arm64.tar.gz"
    with tarfile.open(archive, "w:gz", compresslevel=9) as tar:
        for name, path in files.items():
            tar.add(path, arcname="cognee-cli-p0/" + name)
    report = {"archive_bytes": archive.stat().st_size,
              "file_bytes": sum(path.stat().st_size for path in files.values()),
              "sha256": hashlib.sha256(archive.read_bytes()).hexdigest(),
              "files": {name: path.stat().st_size for name, path in files.items()},
              "model_bytes": None,
              "status": "experimental CLI and ONNX bundle, not a release worker"}
    (out / "sizes.json").write_text(json.dumps(report, indent=2) + "\n")
    print(json.dumps(report, indent=2))


if __name__ == "__main__":
    main()
