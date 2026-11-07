#!/usr/bin/env python3
"""Check relocated Cognee CLI storage with synthetic data; not a retrieval test."""

import argparse
import ctypes
import json
from pathlib import Path
import shutil
import sqlite3
import subprocess
import tarfile
import tempfile
from urllib.parse import unquote, urlsplit

ROOT = Path(__file__).resolve().parents[1]
BINARY = ROOT / ".cache/cognee-target/release/cognee-cli"
LIBRARY = ROOT / ".local/runtimes/onnxruntime-osx-arm64-1.28.0/lib/libonnxruntime.1.28.0.dylib"


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--archive", type=Path, help="从本地实验归档提取并验证")
    args = parser.parse_args()
    report = {"commands": [], "retrieval_verified": False}
    if not args.archive and (not BINARY.is_file() or not LIBRARY.is_file()):
        raise SystemExit("请先运行 bootstrap-engine.py 与 build-engine.sh。")
    with tempfile.TemporaryDirectory(prefix="memlane-cognee-p0-") as name:
        root = Path(name)
        (root / ".env").write_text("")  # Stop dotenv from searching parent dirs.
        binary, library = root / "cognee-cli", root / LIBRARY.name
        if args.archive:
            with tarfile.open(args.archive) as source:
                for filename, dest in [("cognee-cli", binary), (LIBRARY.name, library)]:
                    member = source.getmember("cognee-cli-p0/" + filename)
                    assert member.isfile(), filename
                    with source.extractfile(member) as data, dest.open("wb") as out:
                        shutil.copyfileobj(data, out)
                    dest.chmod(0o755)
            report["from_archive"] = True
        else:
            shutil.copy2(BINARY, binary)
            shutil.copy2(LIBRARY, library)
        # A fresh environment excludes host credentials and development tools.
        env = {"PATH": "/usr/bin:/bin:/usr/sbin:/sbin",
               "COGNEE_CONFIG_HOME": str(root / "config"),
               "COGNEE_LOG_FILE": "false", "COGNEE_LOGS_DIR": str(root / "logs"),
               "TELEMETRY_DISABLED": "true", "ORT_DYLIB_PATH": str(library),
               "RUST_LOG": "error"}

        def run(args, success=True):
            result = subprocess.run([str(binary), *args], env=env, cwd=root,
                                    capture_output=True, text=True, timeout=45)
            entry = {"args": [a.replace(name, "<temporary>") for a in args],
                     "code": result.returncode,
                     "stdout": result.stdout.replace(name, "<temporary>"),
                     "stderr": result.stderr.replace(name, "<temporary>")}
            report["commands"].append(entry)
            if success and result.returncode:
                raise RuntimeError(entry)
            return result

        run(["--version"])
        for key, directory in [("system_root_directory", "system"),
                               ("data_root_directory", "data"),
                               ("cache_root_directory", "cache")]:
            run(["config", "set", key, str(root / directory)])
        texts = {"p0-alpha": "Memlane P0 synthetic alpha request_id",
                 "p0-beta": "Memlane P0 synthetic beta config_rule"}
        for dataset, text in texts.items():
            run(["add", text, "-d", dataset])
        # Every add ran in a separate process. Read only the synthetic SQLite DB
        # after both exited, verifying persistence and dataset ownership.
        with sqlite3.connect(f"file:{root / 'cognee.db'}?mode=ro", uri=True) as db:
            rows = db.execute("SELECT datasets.name, data.raw_data_location FROM data "
                              "JOIN datasets ON data.dataset_id = datasets.id").fetchall()
        assert len(rows) == 2, rows
        for dataset, location in rows:
            uri = urlsplit(location)
            assert uri.scheme in ("", "file") and not uri.netloc, location
            path = Path(unquote(uri.path))
            if not path.is_absolute():
                path = root / path
            assert root.resolve() in path.resolve().parents, location
            assert path.read_text() == texts[dataset], dataset
        report["relocated_cli"] = True
        report["persisted_synthetic_records"] = len(rows)
        report["dataset_ownership_verified"] = True

        get_api = ctypes.CFUNCTYPE(ctypes.c_void_p, ctypes.c_uint32)
        get_version = ctypes.CFUNCTYPE(ctypes.c_char_p)

        class ApiBase(ctypes.Structure):
            _fields_ = [("GetApi", get_api), ("GetVersionString", get_version)]

        lib = ctypes.CDLL(str(library))
        lib.OrtGetApiBase.restype = ctypes.POINTER(ApiBase)
        api = lib.OrtGetApiBase().contents
        assert api.GetApi(27), "ONNX Runtime does not provide API 27"
        report["onnx_runtime"] = api.GetVersionString().decode()
        report["onnx_api_27"] = True

        # No model/key is configured. Record the actual unmet prerequisite,
        # without claiming that add has made the records searchable.
        result = run(["cognify", "-d", "p0-alpha"], success=False)
        assert result.returncode != 0, "unconfigured cognify unexpectedly succeeded"
        report["unconfigured_cognify_rejected"] = True
    out = ROOT / "build/p0-engine"
    out.mkdir(parents=True, exist_ok=True)
    (out / "verification.json").write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n")
    print(json.dumps(report, ensure_ascii=False, indent=2))


if __name__ == "__main__":
    main()
