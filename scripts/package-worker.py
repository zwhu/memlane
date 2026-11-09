#!/usr/bin/env python3
"""Build the 0.0.4 raw/code worker with sequenced requests; excludes models and extractor."""
import argparse
import gzip
import hashlib
import io
import json
from pathlib import Path
import tarfile

ROOT = Path(__file__).resolve().parents[1]
SOURCE = ROOT / ".local/sources/cognee-rs-151905c302fc9b5b9aad88d6b13931c09f0eac30"
RUNTIME = ROOT / ".local/runtimes/onnxruntime-osx-arm64-1.28.0"


def digest(path):
    checksum = hashlib.sha256()
    with path.open("rb") as src:
        while chunk := src.read(1024*1024):
            checksum.update(chunk)
    return checksum.hexdigest()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--out", type=Path, default=ROOT / "build/p4-code-component")
    args = parser.parse_args()
    files = {
        "memlane-cognee-worker": ROOT / ".cache/cognee-target/release/memlane-cognee-worker",
        "libonnxruntime.1.28.0.dylib": RUNTIME / "lib/libonnxruntime.1.28.0.dylib",
        "LICENSE-MIT.cognee": SOURCE / "LICENSE-MIT",
        "LICENSE-APACHE.cognee": SOURCE / "LICENSE-APACHE",
        "LICENSE.onnxruntime": RUNTIME / "LICENSE",
        "ThirdPartyNotices.onnxruntime.txt": RUNTIME / "ThirdPartyNotices.txt",
    }
    manifest = {"id": "cognee-rust", "version": "0.0.4", "platform": "darwin-arm64",
                "protocol_min": 1, "protocol_max": 1, "executable": "memlane-cognee-worker",
                "library": "libonnxruntime.1.28.0.dylib", "model": "multilingual-e5-base-int8",
                "engine_commit": "151905c302fc9b5b9aad88d6b13931c09f0eac30",
                "adapter_patch_sha256": "c73d00e0f09c0ef481fb0c1422be98206dcabafea14c10e0ed6d9ddf98292e89",
                "capabilities": ["raw_index", "raw_search", "remove", "cancel", "timeout", "code_build", "code_query"],
                "code_recipe": "enola-0.4.30-v351",
                "code_root_modules": True,
                "request_id_scheme": "sequence-uuid-v1",
                "files": [{"path": name, "bytes": path.stat().st_size, "sha256": digest(path)} for name, path in files.items()]}
    data = (json.dumps(manifest, indent=2)+"\n").encode()
    out = args.out.resolve()
    out.mkdir(parents=True, exist_ok=True)
    archive = out / "memlane-cognee-rust-0.0.4-darwin-arm64.tar.gz"
    with archive.open("wb") as raw, gzip.GzipFile(fileobj=raw, mode="wb", mtime=0, filename="", compresslevel=9) as compressed:
        with tarfile.open(fileobj=compressed, mode="w") as tar:
            for name, path in files.items():
                info = tarfile.TarInfo(name)
                info.size = path.stat().st_size
                info.mode = 0o755 if name == manifest["executable"] else 0o644
                with path.open("rb") as source:
                    tar.addfile(info, source)
            info = tarfile.TarInfo("component.json")
            info.size = len(data); info.mode = 0o644
            tar.addfile(info, io.BytesIO(data))
    report = {"archive": str(archive), "archive_bytes": archive.stat().st_size,
              "unpacked_file_bytes": sum(x["bytes"] for x in manifest["files"])+len(data),
              "sha256": digest(archive), "component": manifest,
              "models_included": False, "developer_id_notarization": "not configured"}
    (out / "worker-sizes.json").write_text(json.dumps(report, indent=2)+"\n")
    print(json.dumps(report, indent=2))


if __name__ == "__main__":
    main()
