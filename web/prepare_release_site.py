"""Prepare the static Memlane website against a verified release package."""

import argparse
import hashlib
import json
import re
import shutil
from pathlib import Path


ROOT = Path(__file__).resolve().parent.parent


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("tag")
    parser.add_argument("assets", type=Path)
    parser.add_argument("output", type=Path)
    parser.add_argument("--repository", default="zwhu/memlane")
    args = parser.parse_args()

    version = json.loads((ROOT / "mygo.json").read_text())["version"]
    if args.tag != f"v{version}" or not re.fullmatch(r"[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+", args.repository):
        parser.error("release tag or GitHub repository does not match the website")

    dmg = args.assets / f"Memlane-{version}-arm64.dmg"
    checksum = args.assets / "SHA256SUMS.txt"
    with dmg.open("rb") as package:
        digest = hashlib.file_digest(package, "sha256").hexdigest()
    if checksum.read_text().split() != [digest, dmg.name]:
        parser.error("SHA256SUMS.txt does not match the arm64 DMG")

    prefix = f"https://github.com/{args.repository}/releases/download/{args.tag}"
    source = ROOT / "web/website"
    html = (source / "index.html").read_text()
    replacements = {
        '  <meta name="robots" content="noindex">\n': "",
        '<a class="pill" href="#download">下载 macOS 内测版</a>':
            f'<a class="pill" href="{prefix}/{dmg.name}">下载 macOS 内测版</a>',
        '<p class="release-status">内测安装包待发布</p>':
            f'<p class="release-status"><a class="pill" href="{prefix}/{dmg.name}">下载 {args.tag}</a></p>\n'
            f'      <p><a class="text-link" href="{prefix}/{checksum.name}">SHA256 校验文件</a></p>',
    }
    for before, after in replacements.items():
        if html.count(before) != 1:
            parser.error("expected exactly one website release placeholder")
        html = html.replace(before, after)

    shutil.copytree(source, args.output)
    (args.output / "index.html").write_text(html)


if __name__ == "__main__":
    main()
