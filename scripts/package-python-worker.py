#!/usr/bin/env python3
"""Package a real frozen Cognee worker, dereferencing only internal symlinks."""
import gzip
import hashlib
import io
import json
import os
from pathlib import Path
import tarfile

ROOT = Path(__file__).resolve().parents[1]


def digest(path):
    checksum = hashlib.sha256()
    with path.open("rb") as src:
        while chunk := src.read(1024*1024):
            checksum.update(chunk)
    return checksum.hexdigest()


def main():
    directory = ROOT / "build/python-worker/memlane-cognee-worker"
    if not (directory / "memlane-cognee-worker").is_file():
        raise SystemExit("freeze build is missing")
    files = {}
    links = []
    for current, directories, names in os.walk(directory, followlinks=False):
        for name in directories + names:
            path = Path(current) / name
            if path.is_symlink():
                target = str(path.readlink())
                if Path(target).is_absolute():
                    raise SystemExit("absolute runtime symlink: "+str(path))
                links.append({"path":str(path.relative_to(directory)),"target":target})
            if not path.resolve().is_relative_to(directory.resolve()):
                raise SystemExit("frozen runtime contains an external link: "+str(path))
        for name in names:
            path = Path(current) / name
            if path.is_symlink():
                continue
            if not path.is_file():
                raise SystemExit("unsupported runtime file: "+str(path))
            files[str(path.relative_to(directory))] = path
    files["requirements.lock"] = ROOT / "engine/python/requirements.lock"
    assets = [{"path":name,"bytes":path.stat().st_size,"sha256":digest(path),
               "executable": bool(path.stat().st_mode & 0o111)} for name,path in sorted(files.items())]
    manifest = {"id":"cognee-python","version":"0.0.1","platform":"darwin-arm64",
                "protocol_min":1,"protocol_max":1,"executable":"memlane-cognee-worker",
                "library":"_internal/onnxruntime/capi/libonnxruntime.1.31.1.dylib",
                "model":"multilingual-e5-base-int8","cognee_version":"1.6.7",
                "fastembed_version":"0.9.1","python_version":"3.12.12",
                "enola_version":"0.4.30","files":assets,"links":sorted(links,key=lambda x:x["path"])}
    data = (json.dumps(manifest,indent=2)+"\n").encode()
    out = ROOT / "build/p0-engine"
    archive = out / "memlane-cognee-python-0.0.1-darwin-arm64.tar.gz"
    with archive.open("wb") as raw, gzip.GzipFile(fileobj=raw,mode="wb",mtime=0,filename="",compresslevel=6) as compressed:
        with tarfile.open(fileobj=compressed,mode="w") as tar:
            for entry in assets:
                path = files[entry["path"]]
                info = tarfile.TarInfo(entry["path"])
                info.size=entry["bytes"];info.mode=0o755 if entry["executable"] else 0o644
                with path.open("rb") as src:
                    tar.addfile(info,src)
            for link in manifest["links"]:
                info=tarfile.TarInfo(link["path"]);info.type=tarfile.SYMTYPE;info.linkname=link["target"];info.mode=0o777
                tar.addfile(info)
            info=tarfile.TarInfo("component.json");info.size=len(data);info.mode=0o644
            tar.addfile(info,io.BytesIO(data))
    report={"archive":str(archive.relative_to(ROOT)),"archive_bytes":archive.stat().st_size,
            "unpacked_file_bytes":sum(e["bytes"] for e in assets)+len(data),
            "sha256":digest(archive),"manifest_sha256":hashlib.sha256(data).hexdigest(),
            "component":manifest,"models_included":False,"internal_link_count":len(links),"developer_id_notarization":"not configured"}
    (out / "python-worker-sizes.json").write_text(json.dumps(report,indent=2)+"\n")
    print(json.dumps({k:v for k,v in report.items() if k != "component"},indent=2),flush=True)


if __name__ == "__main__":
    main()
