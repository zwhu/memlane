#!/usr/bin/env python3
"""Package the verified local model separately from the main App and engine."""
import argparse
import gzip
import hashlib
import io
import json
from pathlib import Path
import tarfile
import urllib.request

ROOT = Path(__file__).resolve().parents[1]


def digest(path):
    checksum = hashlib.sha256()
    with path.open("rb") as source:
        while block := source.read(1024*1024):
            checksum.update(block)
    return checksum.hexdigest()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--out", type=Path, default=ROOT / "build/p0-engine")
    args = parser.parse_args()
    recipe = json.loads((ROOT / "deps/models/multilingual-e5-base-int8.json").read_text())
    directory = ROOT / ".local/models" / recipe["id"]
    entries = recipe["files"]+recipe.get("additional_files", [])
    for entry in entries:
        path = directory / entry["name"]
        assert path.stat().st_size == entry["bytes"] and digest(path) == entry["sha256"], entry["name"]
    base_card = directory / "README.base-model.md"
    if not base_card.exists():
        url = f'https://huggingface.co/{recipe["base_model"]}/resolve/{recipe["base_revision"]}/README.md'
        with urllib.request.urlopen(url, timeout=30) as response:
            data = response.read()
        assert "license: mit" in data.decode().lower(), "base model license declaration changed"
        base_card.write_bytes(data)
    paths = [directory / e["name"] for e in entries]+[directory / "recipe.json", base_card]
    metadata = {"id": recipe["id"], "version": recipe["revision"], "platform": "darwin-arm64",
                "protocol_min": 1, "protocol_max": 1, "kind": "model",
                "files": [{"path": p.name, "bytes": p.stat().st_size, "sha256": digest(p)} for p in paths]}
    manifest = (json.dumps(metadata, ensure_ascii=False, indent=2)+"\n").encode()
    out = args.out.resolve()
    out.mkdir(parents=True, exist_ok=True)
    archive = out / "memlane-model-e5-base-int8.tar.gz"
    with archive.open("wb") as raw, gzip.GzipFile(fileobj=raw, mode="wb", filename="", mtime=0, compresslevel=9) as compressed:
        with tarfile.open(fileobj=compressed, mode="w") as tar:
            for path in paths:
                info = tarfile.TarInfo(path.name)
                info.size = path.stat().st_size; info.mode = 0o644
                with path.open("rb") as source:
                    tar.addfile(info, source)
            info = tarfile.TarInfo("component.json"); info.size = len(manifest); info.mode = 0o644
            tar.addfile(info, io.BytesIO(manifest))
    report = {"archive": str(archive.relative_to(ROOT)) if archive.is_relative_to(ROOT) else str(archive), "archive_bytes": archive.stat().st_size,
              "unpacked_file_bytes": sum(p.stat().st_size for p in paths)+len(manifest),
              "sha256": digest(archive), "manifest_sha256": hashlib.sha256(manifest).hexdigest(),
              "component": metadata, "models_in_main_package": False}
    (out / "model-sizes.json").write_text(json.dumps(report, ensure_ascii=False, indent=2)+"\n")
    print(json.dumps(report, ensure_ascii=False, indent=2))


if __name__ == "__main__":
    main()
