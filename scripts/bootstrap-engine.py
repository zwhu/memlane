#!/usr/bin/env python3
"""Prepare Rust build tools and the candidate runtime without changing profiles."""

import hashlib
import os
from pathlib import Path
import subprocess
import tarfile
import urllib.request
import zipfile

ROOT = Path(__file__).resolve().parents[1]
LOCAL = ROOT / ".local"
TOOLS = LOCAL / "toolchains"
SHA = "151905c302fc9b5b9aad88d6b13931c09f0eac30"
SOURCE_SHA256 = "d190a15e282933a1066065ab454fba5831346e9e5bb63ba137cda5c04f644955"


def download(url, path, expected=None):
    path.parent.mkdir(parents=True, exist_ok=True)
    if not path.exists():
        partial = path.with_suffix(path.suffix + ".part")
        print("下载 " + path.name, flush=True)
        with urllib.request.urlopen(url, timeout=60) as response, partial.open("wb") as out:
            while chunk := response.read(1024 * 1024):
                out.write(chunk)
        partial.replace(path)
    if expected and hashlib.sha256(path.read_bytes()).hexdigest() != expected:
        raise SystemExit("校验失败：" + str(path))


def main():
    if os.uname().sysname != "Darwin" or os.uname().machine != "arm64":
        raise SystemExit("当前引擎准备脚本仅验证 macOS Apple Silicon。")
    env = os.environ.copy()
    env.update(CARGO_HOME=str(LOCAL / "cargo"), RUSTUP_HOME=str(LOCAL / "rustup"))
    rustc = LOCAL / "rustup/toolchains/1.91.1-aarch64-apple-darwin/bin/rustc"
    if not rustc.exists():
        url = "https://static.rust-lang.org/rustup/dist/aarch64-apple-darwin/rustup-init"
        expected = urllib.request.urlopen(url + ".sha256", timeout=30).read().decode().split()[0]
        rustup = TOOLS / ("rustup-init-" + expected[:12])
        download(url, rustup, expected)
        rustup.chmod(0o755)
        subprocess.run([str(rustup), "-y", "--no-modify-path", "--profile", "minimal",
                        "--default-toolchain", "1.91.1"], env=env, check=True)
    subprocess.run([str(rustc), "--version"], env=env, check=True)
    cmake = LOCAL / "build-tools/cmake/data/bin/cmake"
    if not cmake.exists():
        subprocess.run(["uv", "--cache-dir", str(ROOT / ".cache/uv"), "pip", "install",
                        "--python", "/usr/bin/python3", "--target", str(LOCAL / "build-tools"),
                        "cmake==4.1.2"], check=True)
    subprocess.run([str(cmake), "--version"], check=True)

    archive = TOOLS / "protoc-33.1-osx-aarch_64.zip"
    download("https://github.com/protocolbuffers/protobuf/releases/download/v33.1/" + archive.name,
             archive, "db7e66ff7f9080614d0f5505a6b0ac488cf89a15621b6a361672d1332ec2e14e")
    dest = TOOLS / "protoc-33.1"
    if not (dest / "bin/protoc").exists():
        with zipfile.ZipFile(archive) as source:
            base = dest.resolve()
            for name in source.namelist():
                if base not in (base / name).resolve().parents:
                    raise SystemExit("protoc 归档包含非预期路径")
            source.extractall(dest)
        (dest / "bin/protoc").chmod(0o755)
    subprocess.run([str(dest / "bin/protoc"), "--version"], check=True)

    source_dir = LOCAL / "sources" / ("cognee-rs-" + SHA)
    if not (source_dir / "Cargo.toml").exists():
        archive = LOCAL / "cognee-rs.tar.gz"
        download("https://codeload.github.com/topoteretes/cognee-rs/tar.gz/" + SHA,
                 archive, SOURCE_SHA256)
        with tarfile.open(archive) as source:
            base = source_dir.parent.resolve()
            for member in source.getmembers():
                if (base not in (base / member.name).resolve().parents
                        or member.issym() or member.islnk()):
                    raise SystemExit("Cognee 源码归档包含非预期路径或链接")
            source.extractall(base)
    ort_archive = LOCAL / "onnxruntime-osx-arm64-1.28.0.tgz"
    download("https://github.com/microsoft/onnxruntime/releases/download/v1.28.0/" + ort_archive.name,
             ort_archive, "1268b359718099bde2cedb55787f182a130067bc4f31e8c88478c445b850d3d8")
    ort_dir = LOCAL / "runtimes"
    if not (ort_dir / "onnxruntime-osx-arm64-1.28.0/lib/libonnxruntime.1.28.0.dylib").exists():
        with tarfile.open(ort_archive) as source:
            base = ort_dir.resolve()
            members = []
            for member in source.getmembers():
                if base not in (base / member.name).resolve().parents:
                    raise SystemExit("ONNX Runtime 归档包含非预期路径")
                # Use the versioned dylib directly; do not extract link aliases.
                if member.isdir() or member.isfile():
                    members.append(member)
            source.extractall(base, members=members)
    print("引擎工具与 ONNX Runtime 候选已准备；运行 sh scripts/build-engine.sh。")


if __name__ == "__main__":
    main()
