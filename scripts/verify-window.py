#!/usr/bin/env python3
"""Exercise a running e2e build through MyGo's local automation socket."""

import argparse
import base64
import json
from pathlib import Path
import socket


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--socket", default="/tmp/memlane-p0.sock")
    parser.add_argument("--out", default="build/p0-screenshots")
    args = parser.parse_args()
    out = Path(args.out)
    out.mkdir(parents=True, exist_ok=True)
    sequence = 0
    with socket.socket(socket.AF_UNIX) as connection:
        connection.settimeout(10)
        connection.connect(args.socket)
        stream = connection.makefile("rwb")

        def request(method, params=None):
            nonlocal sequence
            sequence += 1
            stream.write((json.dumps({"id": sequence, "method": method,
                                      "params": params or {}}) + "\n").encode())
            stream.flush()
            response = json.loads(stream.readline())
            if "error" in response:
                raise RuntimeError(response["error"])
            return response["result"]

        def settle():
            request("idle", {"timeout": 2000})

        def nodes():
            return request("tree")["nodes"]

        def press(label):
            button = next(node for node in nodes()
                          if node.get("label") == label and "press" in node.get("actions", []))
            request("act", {"id": button["id"], "action": "press"})
            settle()

        def capture(name):
            result = request("capture")
            (out / name).write_bytes(base64.b64decode(result["png"]))

        windows = request("windows")["windows"]
        assert windows and windows[0]["kind"] == "content", windows
        request("menu", {"path": ["文件", "打开 Memlane"]})
        settle()
        press("总览")
        capture("native-home.png")
        press("新增记忆")
        field = next(node for node in nodes() if node.get("key") == "memory-draft")
        text = "P0 合成输入：项目经验与 request_id"
        request("act", {"id": field["id"], "action": "setValue", "text": text})
        settle()
        press("设置")
        press("全部记忆")
        field = next(node for node in nodes() if node.get("key") == "memory-draft")
        assert field["value"] == text, field
        save = next(node for node in nodes() if node.get("label") == "保存")
        assert "disabled" in save.get("states", []), save
        capture("native-draft.png")
        press("设置")
        press("检查更新")
        assert any(node.get("label") == "此版本尚未启用自动更新" for node in nodes())
        capture("native-settings.png")
        press("总览")
        request("menu", {"path": ["文件", "关闭窗口"]})
        # The real close event must hide the window and keep the process alive.
        settle()
        assert not request("windows")["windows"][0]["visible"]
        request("menu", {"path": ["文件", "打开 Memlane"]})
        settle()
        assert request("windows")["windows"][0]["visible"]
        report = {"native_window": True, "draft_preserved": True,
                  "save_disabled": True, "updates_disabled_reported": True,
                  "close_hides_window": True, "reopen_window": True, "window": windows[0]}
        (out / "native-verification.json").write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n")
        print("PASS: real window, draft navigation, disabled save, update state, close and reopen")


if __name__ == "__main__":
    main()
