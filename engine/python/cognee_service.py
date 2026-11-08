"""独立 Cognee 服务组件入口；不依赖用户 Python、Agent 凭据或开发 checkout。"""
import argparse
import fcntl
import importlib.metadata
import json
import os
from pathlib import Path
import socket
import sys


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--config", required=True, type=Path)
    args = parser.parse_args()
    path = args.config
    if not path.is_absolute() or path.is_symlink() or path.stat().st_mode & 0o077 or path.stat().st_size > 65536:
        parser.error("需要明确的私有 Cognee 配置文件")
    settings = json.loads(path.read_text())
    root = Path(settings["root"])
    if root != path.parent / "cognee" or root.is_symlink():
        parser.error("Cognee 数据只能位于该 App 的独立数据根")
    if importlib.metadata.version("cognee") != "1.6.7":
        parser.error("Cognee 服务组件版本不匹配")
    root.mkdir(parents=True, exist_ok=True, mode=0o700)
    owner = (root / ".service-owner").open("a+")
    fcntl.flock(owner.fileno(), fcntl.LOCK_EX | fcntl.LOCK_NB)
    for name in ("system", "data", "cache", "logs", "home"):
        (root / name).mkdir(exist_ok=True, mode=0o700)
    environment = {
        "HOME": str(root / "home"), "SYSTEM_ROOT_DIRECTORY": str(root / "system"),
        "DATA_ROOT_DIRECTORY": str(root / "data"), "CACHE_ROOT_DIRECTORY": str(root / "cache"),
        "DB_PATH": str(root / "system/databases"), "LOGS_ROOT_DIRECTORY": str(root / "logs"),
        "COGNEE_LOGS_DIR": str(root / "logs"), "TELEMETRY_DISABLED": "true",
        "COGNEE_TRACING_ENABLED": "false", "CACHE_BACKEND": "sqlite", "CACHING": "true",
        "ENABLE_BACKEND_ACCESS_CONTROL": "true", "REQUIRE_AUTHENTICATION": "true",
        "PYTHON_DOTENV_DISABLED": "1", "COGNEE_AGENT_MODE": "false",
        "STRUCTURED_OUTPUT_FRAMEWORK": "litellm_native", "LITELLM_LOCAL_MODEL_COST_MAP": "true",
        "TIKTOKEN_CACHE_DIR": str(Path(getattr(sys, "_MEIPASS", Path(__file__).parent)) / "tiktoken-cache"),
    }
    allowed = ("LLM_PROVIDER", "LLM_MODEL", "LLM_ENDPOINT", "LLM_API_KEY", "LLM_MAX_COMPLETION_TOKENS",
               "EMBEDDING_PROVIDER", "EMBEDDING_MODEL", "EMBEDDING_DIMENSIONS", "EMBEDDING_MAX_TOKENS",
               "DEFAULT_USER_EMAIL", "DEFAULT_USER_PASSWORD", "HF_HOME", "HF_HUB_OFFLINE")
    for name in allowed:
        value = settings.get("environment", {}).get(name)
        if value is not None:
            environment[name] = str(value)
    if not environment.get("LLM_API_KEY"):
        parser.error("Cognee 独立模型授权未提供，不能使用 Agent 登录替代")
    os.environ.update(environment)
    import litellm
    import uvicorn
    model = environment["LLM_MODEL"]
    # The already verified DeepSeek capability uses LiteLLM's official
    # registration, preserving transport/output validation and model choice.
    if model == "deepseek/deepseek-v4-flash":
        entry = dict(litellm.model_cost.get(model, {}) or litellm.model_cost.get("deepseek-v4-flash", {}))
        entry.update(supports_response_schema=False, litellm_provider="deepseek", mode="chat")
        litellm.register_model({model: entry})
    from cognee.api.client import app
    from cognee_browsing import install
    install(app)
    bound = socket.socket()
    target = path.parent / "cognee-service-state.json"
    port = 0
    if target.exists():
        previous = json.loads(target.read_text())
        if previous.get("root") != str(root):
            parser.error("已有服务状态属于另一数据根")
        port = int(previous.get("port", 0))
    bound.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    bound.bind(("127.0.0.1", port))
    bound.listen(128)
    endpoint = "http://127.0.0.1:" + str(bound.getsockname()[1])
    state = {"pid": os.getpid(), "endpoint": endpoint, "port": bound.getsockname()[1], "root": str(root), "version": "1.6.7", "phase": "starting"}
    with os.fdopen(os.open(target, os.O_WRONLY | os.O_CREAT | os.O_TRUNC | os.O_NOFOLLOW, 0o600), "w") as file:
        os.fchmod(file.fileno(), 0o600)
        json.dump(state, file)
        file.flush()
        os.fsync(file.fileno())
    config = uvicorn.Config(app, host="127.0.0.1", port=bound.getsockname()[1], access_log=False, timeout_graceful_shutdown=15)
    try:
        uvicorn.Server(config).run(sockets=[bound])
    finally:
        state["phase"] = "stopped"
        target.write_text(json.dumps(state))
        bound.close()
        owner.close()


if __name__ == "__main__":
    # Cognee owns real graph/vector subprocesses. Frozen spawn re-enters this
    # executable; PyInstaller must dispatch those workers before App parsing.
    import multiprocessing
    multiprocessing.freeze_support()
    main()
