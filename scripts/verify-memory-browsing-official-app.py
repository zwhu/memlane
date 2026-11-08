#!/usr/bin/env python3
"""正式 App 浏览本轮官方材料；不注入记忆，不以图谱替代 Rust 检索。"""
import argparse
import hashlib
from importlib.machinery import SourceFileLoader
import json
import os
from pathlib import Path
import socket
import subprocess
import uuid

ROOT = Path(__file__).resolve().parents[1]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--pointer", type=Path, default=ROOT / "build/onboarding-memory-validation/final-cold/fixture-path.txt")
    parser.add_argument("--out", type=Path, default=ROOT / "build/memory-browsing-validation/official-app")
    parser.add_argument("--session", default="codex_01a1bb1f-68bf-7441-b006-00ad9c034e6c")
    parser.add_argument("--native-expected", type=Path, default=ROOT / "build/memory-browsing-validation/native-original.txt")
    args = parser.parse_args(); args.out.mkdir(parents=True, exist_ok=True)
    fixture = Path(args.pointer.read_text().strip())
    assert json.loads((fixture / "synthetic-only.json").read_text())["synthetic_only"]
    meta = json.loads((fixture / "source-info.json").read_text())
    data = fixture / "client/data"
    ipc = f"/tmp/memlane-{os.getuid()}-{hashlib.sha256(str(data).encode()).hexdigest()[:12]}/service.sock"

    def call(op, **fields):
        with socket.socket(socket.AF_UNIX) as connection:
            connection.settimeout(25); connection.connect(ipc)
            connection.sendall((json.dumps({"protocol": 1, "id": str(uuid.uuid4()), "op": op, **fields}) + "\n").encode())
            response = json.loads(connection.makefile("rb", buffering=0).readline())
        assert response["ok"], response
        return response.get("result")

    U = SourceFileLoader("officialbrowsingui", str(ROOT / "scripts/p1-ui.py")).load_module()
    report = {"state": "failed", "fixture": str(fixture), "app": meta["app"], "synthetic_only": True, "fixture_injection": False}
    ui = None
    try:
        ui = U.UI(Path(meta["app"]), fixture, args.out / "screenshots", config=fixture / "runtime-config.json")

        def press(key):
            n = ui.node(key=key); assert "disabled" not in n.get("states", []), n
            ui.req("act", id=n["id"], action="press")

        ui.press("设置"); U.wait(lambda: "启动服务" in ui.labels())
        ui.press("启动服务"); U.wait(lambda: call("health")["state"] == "ready", timeout=100)
        project = next(p for p in call("scopes") if p["name"] == "连续合成项目 A")
        report["project"] = project
        press("cognee-settings"); U.wait(lambda: any(n.get("key") == "cognee-project-" + project["id"] for n in ui.nodes()))
        press("cognee-project-" + project["id"])
        U.wait(lambda: "disabled" not in ui.node(key="cognee-prepare").get("states", []))
        press("cognee-prepare")
        U.wait(lambda: json.loads((fixture / "client/official-onboarding.json").read_text())["Phase"] in ("ready", "failed", "cancelled") and "disabled" not in ui.node(key="cognee-prepare").get("states", []), timeout=240)
        assert json.loads((fixture / "client/official-onboarding.json").read_text())["Phase"] == "ready"
        before = {kind: call("list", list_query={"kind": kind, "datasets": [project["id"]]}) for kind in ("raw", "knowledge")}
        ui.set("cognee-session", args.session); press("cognee-sync")
        U.wait(lambda: "disabled" not in ui.node(key="cognee-sync").get("states", []), timeout=330)
        report["sync_message"] = ui.node(key="cognee-result").get("label", "")
        assert "Memlane 已持久化" in report["sync_message"] and "取消" not in report["sync_message"]
        for kind in before:
            after = call("list", list_query={"kind": kind, "datasets": [project["id"]]})
            prior = {v["memory"]: v for v in before[kind]["items"]}
            current = {v["memory"]: v for v in after["items"]}
            assert all(current[k] == v for k, v in prior.items())
            added = [v for k, v in current.items() if k not in prior]
            assert not added if kind == "knowledge" else all(v.get("source_detail", {}).get("native") for v in added)
        stable = {kind: call("list", list_query={"kind": kind, "datasets": [project["id"]]}) for kind in before}
        press("cognee-sync")
        U.wait(lambda: "disabled" not in ui.node(key="cognee-sync").get("states", []), timeout=330)
        for kind in stable:
            assert stable[kind] == call("list", list_query={"kind": kind, "datasets": [project["id"]]})
        report["replay_same_memories_and_revisions"] = True
        graph = call("cognee_graph", cognee_graph_query={"project": project["id"]})
        report["graph"] = graph
        assert graph["entities"] and graph["relations"], graph
        raw = call("list", list_query={"kind": "raw", "datasets": [project["id"]]})
        knowledge = call("list", list_query={"kind": "knowledge", "datasets": [project["id"]]})
        report["counts"] = {"raw": raw["total"], "knowledge": knowledge["total"]}
        report["knowledge"] = knowledge
        relation = graph["relations"][0]; source = relation["sources"][0]
        item = next(v for v in knowledge["items"] if v["memory"] == source["memory"])
        U.wait(lambda: all(call("get", memory=v["memory"])["index_state"] == "indexed" for v in knowledge["items"]), timeout=120)
        report["rust_queries"] = []
        for v in knowledge["items"]:
            query = v["text"].split("\n\n", 1)[-1][:180]
            hits = call("library_search", scope=project["id"], query=query, limit=30)
            assert any(h["version"]["memory"] == v["memory"] and h["version"]["revision"] == v["revision"] and h["version"]["dataset"] == project["id"] and h["version"]["text"] == v["text"] for h in hits), hits
            report["rust_queries"].append({"query": query, "memory": v["memory"], "revision": v["revision"], "hits": hits})

        press("cognee-browse"); U.wait(lambda: any(n.get("key") == "browse-tab-memories" for n in ui.nodes()))
        press("memory-" + item["memory"])
        U.wait(lambda: any(n.get("key", "").startswith("source-reader-open-") for n in ui.nodes()))
        ui.system_capture("01-memory-knowledge-system.png")
        sdk_raw = [v for v in raw["items"] if v.get("source_detail", {}).get("official")]
        native = next(v for v in raw["items"] if v.get("source_detail", {}).get("native"))
        assert native["text"] == args.native_expected.read_text()
        qa = next(v for v in sdk_raw if v["source_detail"]["official"]["kind"] == "qa")
        reader_key = "source-reader-open-" + qa["memory"]
        raw_id = reader_key.removeprefix("source-reader-open-")
        raw_item = next(v for v in raw["items"] if v["memory"] == raw_id)
        query = {"project": project["id"], "owner": item["memory"], "owner_revision": item["revision"], "memory": raw_id, "revision": raw_item["revision"]}
        read = call("source_read", source_read=query)
        report["source_read"] = read
        assert read["state"] == "available" and read["record"]["text"] == raw_item["text"]
        assert read["original_state"] == "available", read["original_state"]
        assert all(read["session_original"][key] == native[key] for key in ("memory", "revision", "dataset", "text", "source_detail"))
        press(reader_key); U.wait(lambda: any(n.get("key") == "source-reader-copy" for n in ui.nodes()))
        expected_order = sorted(sdk_raw, key=lambda v: (0 if v["source_detail"]["official"]["kind"] == "qa" else 1, v["source_detail"]["official"]["sequence"]))
        source_keys = [n["key"] for n in ui.nodes() if n.get("key", "").startswith("source-reader-record-")]
        assert source_keys == ["source-reader-record-" + v["memory"] for v in expected_order], source_keys
        report["official_source_order"] = source_keys
        ui.system_capture("02-full-original-system.png")
        press("source-reader-copy")
        full_copied = subprocess.check_output(["/usr/bin/pbpaste"]).decode()
        assert full_copied == native["text"]
        report["full_copy"] = {"bytes": len(full_copied.encode()), "sha256": hashlib.sha256(full_copied.encode()).hexdigest(), "exact_completed_codex_original": True}
        press("source-reader-next"); ui.system_capture("02b-long-original-next-page-system.png")
        ui.set("source-reader-search", "工具返回"); press("source-reader-find")
        assert any("处匹配" in n.get("label", "") and not n.get("label", "").startswith("0 ") for n in ui.nodes())
        ui.system_capture("02c-full-original-search-system.png")
        press("source-reader-quote")
        ui.set("source-reader-search", "Question:"); press("source-reader-find")
        ui.system_capture("03-original-search-quote-system.png")
        press("source-reader-copy")
        copied = subprocess.check_output(["/usr/bin/pbpaste"]).decode()
        assert copied == read["record"]["text"]
        report["sdk_segment_copy"] = {"bytes": len(copied.encode()), "sha256": hashlib.sha256(copied.encode()).hexdigest(), "exact_official_segment": True}
        press("source-reader-close")
        entity = next(e for e in graph["entities"] if e["id"] == relation["source"])
        press("browse-memory-entity-" + entity["id"])
        ui.system_capture("04-entity-system.png")
        press("browse-entity-graph")
        U.wait(lambda: any(n.get("key") == "browse-graph-entity-" + entity["id"] for n in ui.nodes()))
        U.wait(lambda: any(n.get("key") == "browse-relation-" + relation["id"] for n in ui.nodes()))
        press("browse-relation-" + relation["id"])
        ui.system_capture("05-supported-relations-system.png")
        press("browse-relation-source-" + source["chunk"])
        U.wait(lambda: any(n.get("key") == reader_key for n in ui.nodes()))
        press(reader_key); U.wait(lambda: any(n.get("key") == "source-reader-copy" for n in ui.nodes()))
        press("source-reader-copy"); assert subprocess.check_output(["/usr/bin/pbpaste"]).decode() == full_copied
        press("source-reader-close")
        report["cross_view_same_source"] = {"knowledge": item["memory"], "entity": entity["id"], "relation": relation["id"], "raw": raw_id}

        project_b = next((p for p in call("scopes") if p.get("name") == "连续合成项目 B"), None)
        if project_b is None:
            ui.press("添加项目"); U.wait(lambda: any(n.get("key") == "project-name" for n in ui.nodes()))
            ui.set("project-name", "连续合成项目 B"); ui.set("project-path", str(fixture / "repo-b")); press("register-project")
            U.wait(lambda: any(p.get("name") == "连续合成项目 B" for p in call("scopes")))
            project_b = next(p for p in call("scopes") if p.get("name") == "连续合成项目 B")
        else:
            ui.press(project_b["name"])
        U.wait(lambda: any(n.get("key") == "browse-tab-memories" for n in ui.nodes()))
        assert not any(n.get("key") == "memory-" + v["memory"] for n in ui.nodes() for v in raw["items"] + knowledge["items"])
        assert call("list", list_query={"datasets": [project_b["id"]]})["total"] == 0
        press("browse-tab-entities"); ui.system_capture("06-second-project-empty-system.png")
        denied_query = dict(query, project=project_b["id"])
        try: call("source_read", source_read=denied_query)
        except AssertionError as error: report["source_cross_project_denied"] = str(error)
        else: raise AssertionError("Cross-project source read was allowed")
        report["project_b"] = project_b
        ui.press(project["name"]); U.wait(lambda: any(n.get("key") == "browse-tab-memories" for n in ui.nodes()))
        press("browse-tab-entities"); press("browse-entity-" + entity["id"])
        saved = json.loads((fixture / "client/browsing.json").read_text()); report["persisted_selection"] = saved
        assert saved["project"] == project["id"] and saved["selections"][project["id"]]["entity"] == entity["id"]
        ui.quit(stop=True); ui = None
        ui = U.UI(Path(meta["app"]), fixture, args.out / "reopen-screenshots", config=fixture / "runtime-config.json")
        U.wait(lambda: any(n.get("key") == "browse-tab-entities" for n in ui.nodes()))
        assert json.loads((fixture / "client/browsing.json").read_text()) == saved
        ui.press("设置"); U.wait(lambda: "启动服务" in ui.labels()); ui.press("启动服务")
        U.wait(lambda: call("health")["state"] == "ready", timeout=100)
        press("cognee-settings"); U.wait(lambda: any(n.get("key") == "cognee-project-" + project["id"] for n in ui.nodes()))
        press("cognee-project-" + project["id"])
        U.wait(lambda: "disabled" not in ui.node(key="cognee-prepare").get("states", [])); press("cognee-prepare")
        U.wait(lambda: json.loads((fixture / "client/official-onboarding.json").read_text())["Phase"] == "ready" and "disabled" not in ui.node(key="cognee-prepare").get("states", []), timeout=240)
        ui.press(project["name"])
        U.wait(lambda: any(n.get("key") == "browse-entity-" + entity["id"] for n in ui.nodes()))
        assert entity["name"] in ui.labels()
        assert json.loads((fixture / "client/browsing.json").read_text()) == saved
        report["reopen_actual_services_and_selected_entity"] = True
        ui.system_capture("07-reopen-selection-system.png")
        report["state"] = "official_browsing_source_rust_and_project_isolation_passed"
        print({"state": report["state"], "counts": report["counts"], "entities": len(graph["entities"]), "relations": len(graph["relations"])}, flush=True)
    except Exception as error:
        report["error"] = repr(error)
        if ui is not None:
            try: ui.system_capture("failure-system.png")
            except Exception: pass
        raise
    finally:
        if ui is not None:
            try: ui.quit(stop=True)
            except Exception as error: report["cleanup_error"] = repr(error)
        (args.out / "report.json").write_text(json.dumps(report, ensure_ascii=False, indent=2))


if __name__ == "__main__":
    main()
