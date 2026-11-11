#!/usr/bin/env python3
"""构建真实固定 Cognee 1.6.7 服务组件，模型和业务数据不进入归档。"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import subprocess
import struct

ROOT = Path(__file__).resolve().parents[1]


def native_sections(path):
    """Compare arm64 code/data across PyInstaller's Mach-O re-signing."""
    data = path.read_bytes()
    magic, cpu, _, _, commands, _, _, _ = struct.unpack_from("<8I", data)
    if magic != 0xFEEDFACF or cpu != 0x100000C or commands > 4096:
        raise RuntimeError("expected the locked thin arm64 Mach-O extension")
    offset, result = 32, {}
    for _ in range(commands):
        command, length = struct.unpack_from("<II", data, offset)
        if length < 8 or offset + length > len(data):
            raise RuntimeError("invalid Mach-O load command")
        if command == 0x19:  # LC_SEGMENT_64
            fields = struct.unpack_from("<16s4Q4I", data, offset + 8)
            sections = fields[7]
            if 72 + sections * 80 > length:
                raise RuntimeError("invalid Mach-O section table")
            for index in range(sections):
                section, segment, _, size, position, *rest = struct.unpack_from("<16s16sQQ8I", data, offset + 72 + index * 80)
                if rest[3] & 0xFF in (1, 12, 18):  # zero-fill has no file bytes
                    continue
                if position + size > len(data):
                    raise RuntimeError("invalid Mach-O section bounds")
                key = (segment.rstrip(b"\0"), section.rstrip(b"\0"))
                result[key] = (size, hashlib.sha256(data[position:position + size]).hexdigest())
        offset += length
    return result


def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--out",type=Path,default=ROOT/"build/cognee-service-component")
    parser.add_argument("--report-out",type=Path,default=ROOT/"build/onboarding-memory-validation")
    args=parser.parse_args()
    source_hashes = {name: hashlib.sha256((ROOT / "engine/python" / name).read_bytes()).hexdigest()
                     for name in ("cognee_service.py", "cognee_browsing.py", "cognee_submission.py", "cognee_processing_adapter.py", "cognee_grounded_graph.py", "cognee_grounding_audit.py")}
    # Cognee's wheel bundles the matching Ladybug JSON extension. PyInstaller
    # discovers the worker Python modules but does not include this data file.
    # Ship the pinned macOS arm64 resource so a fresh private HOME never needs
    # Ladybug's runtime extension download to initialize the graph database.
    extension = ROOT / ".local/cognee-python/lib/python3.12/site-packages/cognee_db_workers/ladybug_extensions/v0.19.0/osx_arm64/libjson.lbug_extension"
    extension_sha = "e6482a025de31d662ee4b32a39c991706b8cc3c2ec49673ef5d8ce45cc23ce7a"
    if not extension.is_file() or hashlib.sha256(extension.read_bytes()).hexdigest() != extension_sha:
        raise RuntimeError("locked Cognee 1.6.7 Ladybug 0.19.0 arm64 JSON extension missing or changed")
    build = ROOT / ".cache/cognee-service-build"
    build.mkdir(parents=True, exist_ok=True)
    home = build / "private-home"
    home.mkdir(exist_ok=True)
    env = {"HOME": str(home), "PATH": "/usr/bin:/bin", "PYTHON_DOTENV_DISABLED": "1",
           "HF_HUB_OFFLINE": "1", "COGNEE_TRACING_ENABLED": "false", "TELEMETRY_DISABLED": "true",
           "LITELLM_LOCAL_MODEL_COST_MAP": "true", "SYSTEM_ROOT_DIRECTORY": str(build / "system"),
           "DATA_ROOT_DIRECTORY": str(build / "raw"), "CACHE_ROOT_DIRECTORY": str(build / "cache"),
           "LOGS_ROOT_DIRECTORY": str(build / "logs"), "COGNEE_LOGS_DIR": str(build / "logs")}
    encoding_cache = build / "tiktoken-cache"
    encoding_cache.mkdir(exist_ok=True)
    env["TIKTOKEN_CACHE_DIR"] = str(encoding_cache)
    subprocess.run([str(ROOT / ".local/cognee-python/bin/python"), "-c",
                    "import tiktoken; tiktoken.get_encoding('cl100k_base'); tiktoken.get_encoding('o200k_base')"], env=env, check=True, timeout=180)
    command = [str(ROOT / ".local/cognee-python/bin/python"), "-m", "PyInstaller", "--noconfirm", "--onedir", "--noupx",
               "--name", "memlane-cognee-service", "--distpath", str(args.out.resolve()),
               "--workpath", str(build / "work"), "--specpath", str(build),
               "--paths", str(ROOT / "engine/python")]
    command += ["--add-data", str(encoding_cache) + ":tiktoken-cache", "--hidden-import", "tiktoken_ext.openai_public"]
    for module in ("cognee_grounded_graph", "cognee_grounding_audit"):
        command += ["--hidden-import", module]
    command += ["--add-data", str(extension) + ":cognee_db_workers/ladybug_extensions/v0.19.0/osx_arm64"]
    for module in ("cognee", "fastembed", "litellm", "uvicorn", "fastapi", "lancedb", "lance", "onnxruntime", "tokenizers", "sqlalchemy", "aiosqlite", "alembic", "ladybug", "kuzu", "tiktoken", "tiktoken_ext"):
        command += ["--collect-all", module]
    for distribution in ("cognee", "fastembed", "litellm", "uvicorn"):
        command += ["--copy-metadata", distribution]
    command.append(str(ROOT / "engine/python/cognee_service.py"))
    out = args.report_out.resolve()
    out.mkdir(parents=True, exist_ok=True)
    with (out / "cognee-service-build.log").open("w") as log:
        result = subprocess.run(command, cwd=build, env=env, stdout=log, stderr=subprocess.STDOUT)
    if any(hashlib.sha256((ROOT / "engine/python" / name).read_bytes()).hexdigest() != value for name, value in source_hashes.items()):
        raise RuntimeError("构建期间入口或适配源码变化，需要重新构建")
    bundled = args.out.resolve() / "memlane-cognee-service/_internal/cognee_db_workers/ladybug_extensions/v0.19.0/osx_arm64/libjson.lbug_extension"
    if result.returncode == 0 and (not bundled.is_file() or native_sections(bundled) != native_sections(extension)):
        raise RuntimeError("frozen service lost the matching offline JSON extension")
    bundled_modules = []
    if result.returncode == 0:
        # Inspect the shipped executable, rather than a reusable build cache.
        required_modules = [Path(name).stem for name in source_hashes if name != "cognee_service.py"]
        inspect_archive = """import json,sys
from PyInstaller.archive.readers import CArchiveReader, PKG_ITEM_PYZ
archive=CArchiveReader(sys.argv[1])
names=[name for name,entry in archive.toc.items() if entry[-1]==PKG_ITEM_PYZ]
assert len(names)==1, 'expected one embedded Python archive'
modules=archive.open_embedded_archive(names[0]).toc
required=json.loads(sys.argv[2])
assert all(name in modules for name in required), 'frozen Memlane module missing'
print(json.dumps(required))
"""
        binary = args.out.resolve() / "memlane-cognee-service/memlane-cognee-service"
        bundled_modules = json.loads(subprocess.check_output(
            [str(ROOT / ".local/cognee-python/bin/python"), "-c", inspect_archive, str(binary), json.dumps(required_modules)],
            env=env, text=True))
        original_dependencies = subprocess.check_output(["otool", "-L", str(extension)], text=True).splitlines()[1:]
        bundled_dependencies = subprocess.check_output(["otool", "-L", str(bundled)], text=True).splitlines()[1:]
        if original_dependencies != bundled_dependencies:
            raise RuntimeError("frozen JSON extension changed its native library dependencies")
        subprocess.run(["codesign", "--verify", "--strict", str(bundled)], check=True, capture_output=True)
    (out / "cognee-service-build-result.json").write_text(json.dumps({"exit": result.returncode, "command": command,
        "source_sha256": source_hashes,
        "verified_embedded_modules": bundled_modules,
        "entry_sha256": hashlib.sha256((ROOT / "engine/python/cognee_service.py").read_bytes()).hexdigest(),
        "adapter_sha256": hashlib.sha256((ROOT / "engine/python/cognee_browsing.py").read_bytes()).hexdigest(),
        "ladybug_json_extension_source_sha256": extension_sha, "ladybug_json_extension_source_bytes": extension.stat().st_size,
        "ladybug_json_extension_bundled_sha256": hashlib.sha256(bundled.read_bytes()).hexdigest() if bundled.is_file() else None,
        "ladybug_json_extension_bundled_bytes": bundled.stat().st_size if bundled.is_file() else None,
        "native_sections_preserved": result.returncode == 0}, indent=2))
    print("Cognee service freeze exit", result.returncode, flush=True)
    raise SystemExit(result.returncode)


if __name__ == "__main__":
    main()
