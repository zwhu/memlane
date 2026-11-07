#!/usr/bin/env python3
"""Measure the main package and optional actual synthetic prepared runtime."""

import argparse
import hashlib
import json
import os
from pathlib import Path


def directory_size(root):
    logical=allocated=files=links=0
    seen=set()
    for base, dirs, names in os.walk(root, followlinks=False):
        for name in names:
            path=Path(base)/name
            stat=path.lstat()
            if path.is_symlink():
                links+=1
                continue
            key=(stat.st_dev,stat.st_ino)
            if key in seen:continue
            seen.add(key)
            logical+=stat.st_size
            allocated+=stat.st_blocks*512
            files+=1
    return {"file_bytes":logical,"allocated_file_bytes":allocated,"files":files,"symlinks":links}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("dmg", type=Path)
    parser.add_argument("app", type=Path)
    parser.add_argument("--runtime-root",type=Path)
    parser.add_argument("--worker-archive",type=Path)
    parser.add_argument("--model-archive",type=Path)
    parser.add_argument("--python-archive",type=Path)
    parser.add_argument("--out",type=Path)
    args = parser.parse_args()
    if not args.dmg.is_file() or not args.app.is_dir():
        parser.error("需要已构建的 DMG 文件与 .app 目录")
    digest = hashlib.sha256()
    with args.dmg.open("rb") as source:
        while chunk := source.read(1024 * 1024):
            digest.update(chunk)
    report = {
        "dmg_bytes": args.dmg.stat().st_size,
        "dmg_sha256": digest.hexdigest(),
        "app_file_bytes": sum(p.stat().st_size for p in args.app.rglob("*")
                              if p.is_file() and not p.is_symlink()),
        "first_preparation_download_bytes": None,
        "prepared_install_disk_bytes": None,
        "note": "App 文件逻辑大小不含引擎、模型与用户数据；null 表示尚未测量。",
    }
    report["main_app"]=directory_size(args.app)
    if args.worker_archive and args.model_archive:
        report["first_preparation_download_bytes"]=args.worker_archive.stat().st_size+args.model_archive.stat().st_size
        report["component_archives"]={"worker_bytes":args.worker_archive.stat().st_size,"model_bytes":args.model_archive.stat().st_size}
    if args.python_archive and args.model_archive:
        report["python_alternative_preparation_download_bytes"]=args.python_archive.stat().st_size+args.model_archive.stat().st_size
    if args.runtime_root:
        if not (args.runtime_root/"data/memlane.sqlite").is_file() or not (args.runtime_root/"runtime/components").is_dir():
            parser.error("必须使用本轮合成的实际准备目录，不能把缺失路径记为0")
        report["prepared_client_root"]=directory_size(args.runtime_root)
        report["parts"]={name:directory_size(args.runtime_root/name) for name in ["runtime","data"]}
        report["prepared_install_disk_bytes"]=report["main_app"]["file_bytes"]+report["prepared_client_root"]["file_bytes"]
        report["prepared_install_allocated_file_bytes"]=report["main_app"]["allocated_file_bytes"]+report["prepared_client_root"]["allocated_file_bytes"]
        report["note"]="主DMG单列；安装后为App+实际组件/模型/数据/设置/缓存目录普通文件逻辑大小。st_blocks为OS分配指标，目录元数据不计，APFS共享块不等于删除可释放空间。额外下载为成功首轮组件归档字节，不含失败重试、HTTP/TLS开销；Python归档对照不冒充App安装路径磁盘验收。"
    if args.out:args.out.write_text(json.dumps(report,ensure_ascii=False,indent=2)+"\n")
    print(json.dumps(report, ensure_ascii=False, indent=2))


if __name__ == "__main__":
    main()
