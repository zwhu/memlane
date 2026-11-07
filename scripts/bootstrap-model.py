#!/usr/bin/env python3
"""Prepare the pinned P0 model in the repository, verifying every file."""
import hashlib
import argparse
import json
from pathlib import Path
import urllib.request

ROOT = Path(__file__).resolve().parents[1]
MANIFEST = ROOT / "deps/models/multilingual-e5-base-int8.json"


def valid(path, entry):
    return (path.is_file() and path.stat().st_size == entry["bytes"]
            and hashlib.sha256(path.read_bytes()).hexdigest() == entry["sha256"])


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--manifest", type=Path, default=MANIFEST)
    args = parser.parse_args()
    manifest = json.loads(args.manifest.read_text())
    directory = ROOT / ".local/models" / manifest["id"]
    directory.mkdir(parents=True, exist_ok=True)
    downloaded = 0
    entries = manifest["files"] + manifest.get("additional_files", [])
    for entry in entries:
        path = directory / entry["name"]
        if valid(path, entry):
            print("verified cached:", entry["name"], flush=True)
            continue
        temporary = path.with_suffix(path.suffix + ".part")
        print("downloading:", entry["name"], entry["bytes"], "bytes", flush=True)
        with urllib.request.urlopen(entry["url"], timeout=60) as response, temporary.open("wb") as out:
            count = 0
            while chunk := response.read(1024 * 1024):
                count += len(chunk)
                if count > entry["bytes"]:
                    raise RuntimeError("download exceeds manifest size: " + entry["name"])
                out.write(chunk)
        if not valid(temporary, entry):
            raise RuntimeError("model size or SHA-256 mismatch: " + entry["name"])
        temporary.replace(path)
        downloaded += entry["bytes"]
    (directory / "recipe.json").write_text(json.dumps(manifest, ensure_ascii=False, indent=2) + "\n")
    print(json.dumps({"downloaded_bytes": downloaded,
                      "model_file_bytes": sum(e["bytes"] for e in entries)}))


if __name__ == "__main__":
    main()
