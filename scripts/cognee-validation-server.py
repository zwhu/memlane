#!/usr/bin/env python3
"""在本轮合成数据根启动真实 Cognee，登记已测 DeepSeek JSON 协议能力。"""
import argparse
import importlib.metadata
import json
import os
from pathlib import Path


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--report", required=True, type=Path)
    parser.add_argument("--port", type=int, default=18011)
    args = parser.parse_args()
    data = Path(os.environ["DATA_ROOT_DIRECTORY"]).resolve()
    fixture = data.parent.parent
    marker = json.loads((fixture / "synthetic-only.json").read_text())
    if not fixture.name.startswith("memlane-official-agent-") or marker.get("synthetic_only") is not True or data != fixture / "core/data":
        parser.error("只允许本轮明确的合成 Cognee 数据根")
    for name, relative in (("SYSTEM_ROOT_DIRECTORY", "core/system"), ("CACHE_ROOT_DIRECTORY", "core/cache"), ("DB_PATH", "core/system/databases")):
        if Path(os.environ[name]).resolve() != fixture / relative:
            parser.error("所有数据库和缓存路径必须属于同一个合成根")
    if not 1024 <= args.port <= 65535 or not args.report.is_absolute():
        parser.error("需要有效本机端口和明确报告路径")
    model = os.environ["LLM_MODEL"]
    if model != "deepseek/deepseek-v4-flash" or importlib.metadata.version("cognee") != "1.6.7":
        parser.error("该兼容适配只验证过 Cognee 1.6.7 与既有 DeepSeek V4 Flash")
    import litellm
    import uvicorn
    import sys

    # The real endpoint rejected json_schema. Preserve model/cost metadata,
    # register that measured capability through LiteLLM's own API, and let
    # Cognee's existing json_object fallback validate actual model output.
    entry = dict(litellm.model_cost.get(model, {}) or litellm.model_cost.get("deepseek-v4-flash", {}))
    entry.update(supports_response_schema=False, litellm_provider="deepseek", mode="chat")
    litellm.register_model({model: entry})
    if litellm.supports_response_schema(model):
        raise RuntimeError("已测协议能力未被实际注册，拒绝启动")
    args.report.write_text(json.dumps({"model": model, "cognee": "1.6.7", "litellm": importlib.metadata.version("litellm"), "framework": "litellm_native", "response_schema": False, "real_model_transport": True, "synthesized_responses": False}, indent=2))
    from cognee.api.client import app
    sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "engine/python"))
    from cognee_browsing import install
    install(app)
    uvicorn.run(app, host="127.0.0.1", port=args.port, access_log=False)


if __name__ == "__main__":
    main()
