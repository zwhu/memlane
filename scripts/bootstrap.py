#!/usr/bin/env python3
"""Prepare the pinned Go toolchain inside the repository; leave the host unchanged."""

import hashlib
from pathlib import Path
import platform
import subprocess
import tarfile
import urllib.request


ROOT = Path(__file__).resolve().parents[1]
VERSION = "1.27.3"
ARCHIVES = {
    ("Darwin", "arm64"): (
        "go1.27.3.darwin-arm64.tar.gz",
        "dd3908cf20959d798c1e06ebe5b72213f0505ce01373b40ce3110975a6cd99c2",
    ),
}


def main():
    spec = ARCHIVES.get((platform.system(), platform.machine()))
    if spec is None:
        raise SystemExit("当前工具链脚本仅验证 macOS Apple Silicon。")
    tools = ROOT / ".local" / "toolchains"
    destination = tools / ("go-" + VERSION)
    binary = destination / "go" / "bin" / "go"
    if binary.exists():
        subprocess.run([str(binary), "version"], check=True)
        return
    tools.mkdir(parents=True, exist_ok=True)
    filename, expected = spec
    archive = tools / filename
    if not archive.exists():
        partial = archive.with_suffix(archive.suffix + ".part")
        print("下载 Go 工具链（约 68 MB，仅开发使用）…", flush=True)
        with urllib.request.urlopen("https://go.dev/dl/" + filename, timeout=60) as response:
            with partial.open("wb") as output:
                while chunk := response.read(1024 * 1024):
                    output.write(chunk)
        partial.replace(archive)
    hasher = hashlib.sha256()
    with archive.open("rb") as stream:
        while chunk := stream.read(1024 * 1024):
            hasher.update(chunk)
    digest = hasher.hexdigest()
    if digest != expected:
        raise SystemExit("Go 归档 SHA-256 不匹配；未解包，请移走错误归档后重试。")
    destination.mkdir(exist_ok=True)
    with tarfile.open(archive) as source:
        base = destination.resolve()
        for member in source.getmembers():
            target = (base / member.name).resolve()
            if base not in target.parents or member.issym() or member.islnk():
                raise SystemExit("Go 归档包含非预期路径或链接；停止解包。")
        source.extractall(destination)
    subprocess.run([str(binary), "version"], check=True)


if __name__ == "__main__":
    main()
