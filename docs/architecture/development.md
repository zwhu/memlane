# 本地开发与运维说明

面向 Memlane 源码开发与本地日常运维，系统三组件与链路术语统一以 [CONTEXT.md](../../CONTEXT.md) 为准：**记忆引擎（Cognee）**、**接入网关**、**Agent 模型代理（`agent_bridge`）**，业务动作区分**自动采集（Capture）**与**记忆提炼（Memory）**。

## 启动与运维

Wave 0–4 收口后，日常开发运维统一由 Go 单二进制入口 `cmd/memlane dev` 承接（原 `scripts/dev.py` 已退役），默认读取 `.local/daily/runtime.json`（亦可用 `MEMLANE_DEV_RUNTIME_CONFIG` 或 `--config` 指定绝对路径）：

```sh
# 启动后台网关/引擎与桌面窗口
sh scripts/go.sh run ./cmd/memlane dev start --config /绝对路径/runtime.json

# 仅启动后台服务（不拉起桌面窗口）
sh scripts/go.sh run ./cmd/memlane dev start --no-ui --config /绝对路径/runtime.json

# 状态 / 重开窗口 / 停止 / 异常恢复（均支持 --json 输出）
sh scripts/go.sh run ./cmd/memlane dev {status|open|stop|recover} --config /绝对路径/runtime.json

# 零 Skill Setup CLI（诊断 / 安装规划 / 连接 / 核验 / 修复）
sh scripts/go.sh run ./cmd/memlane setup doctor --json
```

| 动作 | 行为与边界 |
| --- | --- |
| `dev start` | 校验 `store_root`/`official_data` 与 loopback 网关地址；若日常库锁未占用则以 `Setsid` 拉起独立 `memlane daemon` 进程组（加载 `memlane.sqlite3`、启动 `engines/cognee` 子进程与内置 Agent 模型代理），轮询 `/api/health` 就绪后拉起 `memlane app`（`--no-ui` 跳过窗口）。检测到残留 owner 回执时拒绝覆盖，提示先执行 `dev recover`。 |
| `dev status` | 只读检查 `.service-owner` 锁与网关 `/api/health`，输出 `ready` / `unavailable` / `stale` / `stopped`（或 `--json` 详情），不触碰运行中进程或锁文件。 |
| `dev open` | 拉起独立 `memlane app` 桌面窗口连接现有后台，不启动第二套服务；关闭桌面窗口不会停止后台服务。 |
| `dev stop` | 核对 `.service-owner` 记录、PID、启动时间与配置路径，按统一超时预算停止所属进程组及子进程并释放日常库锁。 |
| `dev recover` | 异常退出后安全核验原进程组已消失、目标端口空闲后归档遗留 owner 记录；不终止未知进程，PID 复用或未知监听一律拒绝。 |
| `setup` | 统一自举与排障 CLI（顶层亦支持 `doctor`/`install`/`connect`/`verify`/`repair`/`stop` 快捷入口），默认只读诊断或 `--plan`/`--dry-run` 预览，变更须显式 `--apply`。 |

### 开发态运维约束

- **单一日常库与重启边界**：日常运行、Native 界面与真机验证统一使用同一 `.local/daily/official-store`，不静默新建空库；服务源码、模型或配置变更时先用原配置 `dev stop`，再修改并 `dev start`。电脑重启后手动启动，开发态不注册 helper。
- **磁盘门禁与仓库钩子**：受管开发任务经 `scripts/task.py` 执行，至少保留 `50 GiB` 可用磁盘空间（运行中低于 `55 GiB` 自动停止；独立 `.app` 安装态监控阈值为最低 `2 GiB` / 告警线 `5 GiB`）。首次提交前执行 `sh scripts/install-hooks.sh` 启用局部 `.githooks` 与仓库级提交身份守卫（不改全局 `~/.gitconfig`）。除非用户明确要求，开发阶段不执行 DMG 打包、签名、公证、更新或 helper 安装。
- **MyGo 字体抗锯齿补丁**：首页字体抗锯齿通过 `scripts/patches/mygo-antialiased.json` 固定 MyGo 0.7.4 两个源文件及其 SHA，`scripts/go.sh` 经 `scripts/mygo_sdk.py` 在 `.local/mygo-sdk/` 准备私有 SDK 并通过 `-modfile` 编译（不改模块缓存与 `go.mod`/`go.sum`）；升级 MyGo 前需先评审该补丁，上游支持等价主题标志后移除。
- **代码图谱配置**：使用根目录 `mcp-arch.yaml` 的官方 Enola 配置，保留除 `manifests` 外全部默认源码解析器（停用 `manifests` 以防绕过忽略规则扫描 `build/`），更改索引配置后在项目图谱页手动更新，不启动文件监听。

## 统一门禁与静态检查

```sh
# 统一改动感知 / 冻结 / 发版门禁（开发与审改用 --changed / --staged；源码冻结后单一节点用 --full；候选发版用 --release）
python3 scripts/task.py -- python3 scripts/check.py {--changed|--staged|--full|--release}

# Go 全量测试（自动联动 arch_guard_test.go、app/perf_budget_test.go 与 lint_gate_test.go）与静态分析
sh scripts/go.sh test ./...
sh scripts/go.sh vet ./...

# 零依赖静态检查与增量架构/防过度防御门禁
python3 scripts/task.py -- python3 scripts/lint.py
python3 scripts/task.py -- python3 scripts/diff_guard.py

# 无状态引擎薄壳与 Scripts 回归
python3 scripts/task.py -- env PYTHONPATH=. .local/cognee-python/bin/python -m unittest \
  discover -s engines/cognee -p "test_*.py"
python3 scripts/task.py -- python3 -m unittest discover -s scripts -p "test_*.py"
```

现行自动化架构与静态规则覆盖三套互补门禁（其中 `lint.py` + `test_lint.py` 与 `arch_guard_test.go` 由 `sh scripts/go.sh test ./...` 恒常强制执行）：

- **[scripts/lint.py](../../scripts/lint.py)（`ML001–ML003`、`ML007–ML012`、`ML015`、`ML016`、`ML018`、`ML023`）**：
  - **模型参数与环境隔离（`ML001–ML003`、`ML007`）**：原生模型配置须经 `engines/cognee/llm_config.py` 共享 `native_llm_configuration`，禁止字面量默认值与 `or`/`get`/`setdefault` 回退（保留显式 `0`）；禁止非白名单模块修改全局 `os.environ`。
  - **UI 性能与冷启动聚合（`ML008–ML012`）**：守卫 `kit/ui/motion_tokens.go` 动效 `ReduceMotion`、禁止 `<500ms` 高频重绘、禁止 `Draw`/`App.View` 热路径同步磁盘或网络 I/O、禁止桌面端落盘首页 SWR 缓存并强制首页冷启动经 `overviewBootstrap` 聚合及延迟 `RefreshIntegrations`。
  - **资源与正式文档隔离（`ML015`、`ML016`、`ML018`）**：禁止对大文件/二进制调用全量 `read_bytes()` 哈希；启用 SQLite WAL 必须配置 `PRAGMA journal_size_limit`；正式 `docs/` 禁止引用本地忽略的过程产物目录。
  - **引擎薄壳边界（`ML023`）**：`engines/cognee/` 非测试代码严格 `<= 3,500` LOC，禁止导入 `engine`、`sqlite3`、`aiosqlite`。
- **[scripts/diff_guard.py](../../scripts/diff_guard.py)（`DG001–DG006`）**：只扫本次 diff 新增 Tier-A 生产代码，阻断静默吞错（`DG001`/`DG002`）、临时补丁/TODO 注释（`DG003`）、锁文件越界（`DG004`）、随意新增非 Cognee `.py` 文件（`DG005`）及 Go 跨层非法 import（`DG006`）。
- **[arch_guard_test.go](../../arch_guard_test.go)**：全仓静态校验 `kit/*` 零业务依赖、`core/*` 零 UI 依赖、`app/*` 六子包无环隔离、`core/engine/cognee` 导入隔离与 Tier-A Go SQLite WAL `journal_size_limit`。

完整任务分级、双审收敛与发版验收门禁见 [工程规范](../engineering/workflow.md)。

## `runtime.json` 与凭据隔离

配置模板见 [`config/daily-runtime.example.json`](../../config/daily-runtime.example.json)（含 Agent 模型代理字段）与 [`config/provider-runtime.example.json`](../../config/provider-runtime.example.json)（纯直连 Provider 形态），其中路径均为占位符、不能直接启动。真实运行配置、凭据与日常记忆库全部放在 Git 跟踪之外的 `.local/`（私钥/凭据文件 `0600`、目录 `0700`），严禁写入模板、命令行参数、日志或文档。

| 关键字段 | 配置含义 |
| --- | --- |
| `store_root` / `official_data` | 日常存储根目录（须为持久绝对路径，如 `.local/daily/official-store`），持有 `.service-owner` 单实例锁、`memlane.sqlite3` 与官方 `system/`、`data/` 目录；严禁指向 `$TMPDIR` 临时目录。 |
| `official_source`、`official_archive`、`service_commit` | 固定上游 Cognee 源码目录、原始归档与提交哈希（须匹配 [`engine/coding-memory-components.json`](../../engine/coding-memory-components.json)）；启动前经 `engines/cognee/runner.py` `verify_source` v2 校验，禁止修改上游源码或在源码目录留 `.pyc`/`.pyo`。 |
| `python` | 受管 Python 3.12 虚拟环境入口（供 `engines/cognee` 子进程加载依赖），保留 venv 路径，不替换为符号链接指向的基础解释器。 |
| `gateway_url` / `project_control.url` | Go 接入网关唯一 loopback origin（默认 `http://127.0.0.1:18790`），承接 `/api/*`、`/ingest`、`/recall`、`/mcp` 与 `/v1/chat/completions`。 |
| `engine_url` / `service_url` | 内部 `engines/cognee` 薄壳 loopback API origin（仅 Go 网关调用，不直接暴露给宿主插件）。 |
| `management_token_file` / `engine_token_file` / `completion_token_file` | 分别用于桌面端→网关管理面、网关→`engines/cognee` 内部接口、引擎→Agent 模型代理的独立 `0600` 凭据文件；各域严格隔离，不得混用。 |
| `agent_key_files` | 各接入宿主（`codex`、`antigravity`、`pi`、`traex`、`trae_cn` 等）独立主体令牌文件（`0600`），仅用于 `memlane hook` / `pi` / `/mcp` 鉴权，不共享管理面令牌。 |
| `service_environment_file` | 私有 JSON 对象文件（`0600`，非 shell `.env`），提供允许列表内的 embedding 配置、默认用户与认证 secret；LLM 端点与凭据由运行配置动态生成。 |
| `graph_assets` | 官方图谱 Web 静态资产目录（[`web/official-graph/dist`](../../web/official-graph/)），供桌面端沙箱 `mygo.WebView` 加载。 |

## 多 Agent 与模型调试

### 1. Agent 模型代理与直连 Provider / Ollama

- **Agent 模型代理（`agent_bridge`）**：由 Go `core/gateway/completion` + `kit/agents/bridge` 原生承接，支持 `codex`、`antigravity`（Gemini App `agy`，额外检查 `Gemini.app/Contents/Helpers/agy`）、`pi`（需指定已有 `bridge_provider` 为 `deepseek` 或 `ollama`，不复制密钥到 Provider）与 `traex`（TRAE CLI）。执行时仅从目标登录目录提取必要认证状态到单次隔离临时目录，不加载日常插件、skills/hooks、历史或项目工具；依赖漂移须重新验证（详见 [有效模型契约](agent-effective-model-contract-20261118.md)）。
- **直连 LLM Provider**：协议支持 `openai`、`anthropic`、`gemini`、`custom`（DeepSeek 等 OpenAI 兼容端点选 `custom`），endpoint/model/key 均由用户显式填写，拒绝 HTTP 重定向、凭据不回填界面。
- **本机 Ollama**：在「设置 › 记忆提炼 › 添加 Provider」选「Ollama（本机接口）」，填本机根地址（如 `http://127.0.0.1:11434`）与实际模型 ID，无需 API Key；经 `ollama_chat/` 路由调用（`engines/cognee/ollama_schema.py`），不回退其他代理或端口，不自动下载模型或拉起服务。
- **保存 / 验证 / 激活分离**：保存草稿与点击验证（消耗对应额度）均不改变运行期唯一 `active`；需选择「下次启动使用」或确认热切换回执（`engines/cognee/handoff.py`）后生效。

### 2. 宿主连接调试说明（方案 C）

- **5 个主线支持宿主**：Codex、Antigravity（Gemini App）、pi、TraeX（TRAE CLI）、TRAE CN（IDE）；方案 C 接入原则与三级 Cognee 冲突处理边界见 [架构边界契约 §3](boundaries.md#3-宿主接入与-cognee-冲突处理边界)。
- **Hook 调试**：核心宿主统一调用 `memlane hook <host> <event> --gateway-url ... --key-file ...`（支持 `--verbose` 输出脱敏诊断）；`pi` 扩展位于 `engine/agents/pi/`，可通过 `node scripts/test_pi_extension.mjs` 验证加载与事件转发；长尾 MCP 宿主直连网关 `/mcp` 端点。
- **受管配置编辑**：所有宿主配置读写经 `kit/agents`（支持 JSON/JSONC/TOML 注释保留与原子写入）与冲突检测器（`kit/agents/conflicts.go`）执行，不静默修改第三方插件。

## 国内镜像源与手工终端兜底

**实际净室下载量与磁盘占用以 [安装依赖包瘦身调研](../research/install-dependency-slimming-20261125.md) 实测为准**（全部 7 项合计官方源实测下载 **`550.58 MiB`**、国内源 **`551.28 MiB`**，解压落盘约 **`1.30–1.50 GiB`**；界面保守空间预估含安全余量）。

### 1. 7 项首次安装依赖下载源与已证实避坑

| 依赖项（ID） | 实测下载量（原预算） | 国外官方源 | 推荐国内镜像源（实测可用） | 校验方式与已证实避坑 |
| --- | --- | --- | --- | --- |
| **引擎运行库**（`runtime`） | `339.61 MiB`（预算 `2 GiB`） | `https://pypi.org/simple/` | 主：`https://pypi.tuna.tsinghua.edu.cn/simple/`（清华 TUNA）<br>备：`https://mirrors.aliyun.com/pypi/simple/`、`https://mirrors.ustc.edu.cn/pypi/simple/` | `uv pip sync --default-index <mirror> --require-hashes --build-constraints requirements.lock`，强制保留 [`core/setup/requirements.lock`](../../core/setup/requirements.lock) 全量哈希校验 |
| **本地向量模型**（`embedding`） | `91.23 MiB`（`95.66 MB`，7 文件） | `https://huggingface.co/Qdrant/bge-small-zh-v1.5/resolve/46fbe35fd4374a00fee7de77dfddaeb6dd6a2c59/<file>` 与 `https://huggingface.co/BAAI/bge-small-zh-v1.5/resolve/7999e1d3359715c523056ef9478215996d62a620/<file>` | **魔搭社区 ModelScope**：<br>`https://modelscope.cn/models/Qdrant/bge-small-zh-v1.5/resolve/master/<file>` 与 `https://modelscope.cn/models/BAAI/bge-small-zh-v1.5/resolve/master/<file>` | 7 文件与 [`embedding-components.json`](../../engine/embedding-components.json) 逐字节 SHA256 一致。<br>**避坑**：`hf-mirror.com` 会将 `model_optimized.onnx` 302 重定向至海外 `cas-bridge.xethub.hf.co`，不作国内主源 |
| **Python 3.12.12**（`python`） | `16.85 MiB`（`17.7 MB`） | `https://github.com/astral-sh/python-build-standalone/releases/download/20260211/cpython-3.12.12%2B20260211-aarch64-apple-darwin-install_only_stripped.tar.gz` | **阿里云 npmmirror**：<br>`https://registry.npmmirror.com/-/binary/python-build-standalone/20260211/cpython-3.12.12%2B20260211-aarch64-apple-darwin-install_only_stripped.tar.gz` | 字节数 `17,667,661` 与 [`first-install-components.json`](../../engine/first-install-components.json) 一致，复用同一 SHA256 `22625dea...`。<br>**避坑**：南京大学镜像站（NJU）该构建返回 HTTP 404 |
| **安装工具 `uv` 0.12.3**（`uv`） | 官方 `16.87 MiB` / 国内 whl `17.57 MiB` | `https://github.com/astral-sh/uv/releases/download/0.12.3/uv-aarch64-apple-darwin.tar.gz`（`17,686,637 B`，SHA256 `546f7f8a...`） | **主（大厂 PyPI CDN wheel）**：清华 TUNA / 阿里云：<br>`https://pypi.tuna.tsinghua.edu.cn/packages/e4/66/ba257c91d69921d773f523c9e4c3ffb04e233694c415517f5ef797403a4f/uv-0.12.3-py3-none-macosx_11_0_arm64.whl`（阿里云 `/pypi/packages/...` 同路径）<br>**备（tar.gz）**：`https://gh-proxy.org/<official_url>` 或 `https://gh-proxy.com/<official_url>` | wheel 为 `18,426,262 B`（SHA256 `7c99f2524fe4b11d74dec85cef9b4d8b725d142dec04cd237f1c06fbdae1ee54`），经 Go `archive/zip` 解出 `uv-0.12.3.data/scripts/uv`。<br>**避坑**：`npmmirror`、`NJU` 均无 `uv`（404）；`ghfast.top` 返回 HTTP 403 |
| **OpenSSL 3.5.9**（`native_openssl_arm64`） | `10.33 MiB`（`10.8 MB`） | `https://ghcr.io/v2/homebrew/core/openssl/3/blobs/sha256:82b33ce156b289eaf95657b9ec16e8f49cb49c882cdeaeaf57e3e7d67fa85e25`（需匿名头 `Authorization: Bearer QQ==`） | 主：**中科大 USTC**：`https://mirrors.ustc.edu.cn/homebrew-bottles/v2/homebrew/core/openssl/3/blobs/sha256:82b33ce156b289eaf95657b9ec16e8f49cb49c882cdeaeaf57e3e7d67fa85e25`<br>备：**清华 TUNA**：`https://mirrors.tuna.tsinghua.edu.cn/homebrew-bottles/openssl%403-3.5.9.arm64_sequoia.bottle.tar.gz` | 两者均 `10,829,301 B`，复用同一 SHA256 `82b33ce1...`。<br>**避坑**：TUNA 与阿里云的 `/v2/.../blobs/sha256:...` 返回 404，须按站点区分 URL；请求国内镜像时**不得**附带 `Authorization` 头 |
| **Cognee 上游源码**（`cognee-source`） | `68.95 MiB`（预算 `150 MiB`） | `https://codeload.github.com/topoteretes/cognee/tar.gz/ec6f40859e5f2df9e7862dde27d580ac3268ed43` | 公测主：`https://gh-proxy.org/<codeload_url>`<br>公测备：`https://gh-proxy.com/<codeload_url>` | 强制核对 [`coding-memory-components.json`](../../engine/coding-memory-components.json) 固定 SHA256 `6e11047e...`。<br>**避坑与红线**：`ghfast.top` 对 `codeload` TLS 超时已排除；严禁向第三方反代发送账号/Token，严禁重打包改变归档哈希 |
| **官方插件参考归档**（`agent-plugin`） | `6.74 MiB`（预算 `20 MiB`） | `https://codeload.github.com/topoteretes/cognee-integrations/tar.gz/a176248914fdb32a78354854b300174203b15a5c` | 公测主：`https://gh-proxy.org/<codeload_url>`<br>公测备：`https://gh-proxy.com/<codeload_url>` | 强制核对固定 SHA256 `a896399a...`；后续发版建议将上游原样归档迁移至自维护国内只读静态源，公益反代降为备选 |

### 2. 下载器、代理与「重新检测」契约

- **系统代理兼容**：[source.go](../../internal/bootstrap/source.go)、[runtime.go](../../internal/bootstrap/runtime.go) 与 [mirrors_proxy.go](../../kit/aiinstaller/mirrors_proxy.go) 读取 macOS 系统代理（`scutil --proxy`）并兼容 `HTTP_PROXY` / `HTTPS_PROXY` / `ALL_PROXY` / `NO_PROXY`，避免双击 `.app` 启动丢失代理；代理地址与凭据严禁写入日志或诊断导出包。
- **不降级安全基线**：切换国内镜像源与国外官方源只改变 URL 与回退顺序，不降低任何 SHA256、TLS HTTPS 或路径归属校验；仅在 host 为 `ghcr.io` 且路径前缀 `/v2/homebrew/core/` 时附带匿名头 `Authorization: Bearer QQ==`。`uv` wheel 经 [tools.go](../../internal/bootstrap/tools.go) 整包校验后由 Go `archive/zip` 解包。
- **统一「重新检测」**：无论自动下载、失败重试或终端手工准备完成，点击「重新检测」均执行完整 SHA256 校验与离线能力探针，全部通过后放行。

### 3. 官网 `curl` / CLI 手工终端兜底命令

当企业内网或个别镜像节点临时异常时，可在个人终端（继承个人代理）执行以下兜底命令，随后回到 Memlane 点击「重新检测」自动接管校验（界面内「复制安装命令」按钮会自动填入本机目标路径并随源切换更新）：

- **安装工具 `uv`**（[environment.go](../../internal/bootstrap/environment.go) 自动发现 `PATH`、`/opt/homebrew/bin/uv`、`/usr/local/bin/uv`、`~/.local/bin/uv`、`~/.cargo/bin/uv`）：
  ```sh
  # 官网一键脚本（默认安装至 ~/.local/bin/uv）
  curl -LsSf https://astral.sh/uv/install.sh | sh
  # 或 Homebrew
  brew install uv
  ```
- **Python 3.12**（[probe.go](../../internal/bootstrap/probe.go) 自动扫描 `~/.local/share/uv/python/cpython-3.12*/bin/python3.12` 与 `/opt/homebrew/bin/python3.12`）：
  ```sh
  # 国内镜像源推荐
  UV_PYTHON_INSTALL_MIRROR="https://registry.npmmirror.com/-/binary/python-build-standalone/" uv python install 3.12.12
  # 国外官方源（或 brew install python@3.12）
  uv python install 3.12.12
  ```
- **Cognee 上游源码（`ec6f408`）**（[source.go](../../internal/bootstrap/source.go) 原生复用 SHA256 匹配的已有归档）：
  ```sh
  # 国外官方直链
  curl -fL --create-dirs -o "<cognee目录>/service.tar.gz" \
    "https://codeload.github.com/topoteretes/cognee/tar.gz/ec6f40859e5f2df9e7862dde27d580ac3268ed43"
  # 国内镜像直链
  curl -fL --create-dirs -o "<cognee目录>/service.tar.gz" \
    "https://gh-proxy.org/https://codeload.github.com/topoteretes/cognee/tar.gz/ec6f40859e5f2df9e7862dde27d580ac3268ed43"
  ```
- **OpenSSL 3.5.9 原生库（`arm64`）**：
  ```sh
  # 国内 USTC
  curl -fL --create-dirs -o "<runtime目录>/native/artifact.tar.gz" \
    "https://mirrors.ustc.edu.cn/homebrew-bottles/v2/homebrew/core/openssl/3/blobs/sha256:82b33ce156b289eaf95657b9ec16e8f49cb49c882cdeaeaf57e3e7d67fa85e25"
  # 国外官方 ghcr.io
  curl -fL --create-dirs -H "Authorization: Bearer QQ==" -o "<runtime目录>/native/artifact.tar.gz" \
    "https://ghcr.io/v2/homebrew/core/openssl/3/blobs/sha256:82b33ce156b289eaf95657b9ec16e8f49cb49c882cdeaeaf57e3e7d67fa85e25"
  ```
- **本地向量模型（`BAAI/bge-small-zh-v1.5`，7 文件）**：按 HuggingFace 快照布局（`hub/models--Qdrant--bge-small-zh-v1.5/snapshots/46fbe35fd4374a00fee7de77dfddaeb6dd6a2c59/` 及 `hub/models--BAAI--bge-small-zh-v1.5/snapshots/7999e1d3359715c523056ef9478215996d62a620/`）使用 `curl -fL --create-dirs` 从 ModelScope 或 HuggingFace 拉取全部 7 文件至 `<组件目录>/embedding-<摘要>/`（或在界面指定已有 `ModelCache` 路径），由 [model.go](../../internal/bootstrap/model.go) `VerifyModelFiles` 逐文件校验 SHA256 并执行离线编码探针。
- **引擎运行库（`runtime`）**：
  ```sh
  uv venv --python "<python3.12>" "<runtime目录>/venv"
  # 国内清华源锁定同步（含全量哈希校验）
  uv pip sync --python "<runtime目录>/venv/bin/python" \
    --default-index https://pypi.tuna.tsinghua.edu.cn/simple/ \
    --require-hashes --build-constraints "<lock路径>" "<lock路径>"
  # 国外官方源锁定同步
  uv pip sync --python "<runtime目录>/venv/bin/python" \
    --require-hashes --build-constraints "<lock路径>" "<lock路径>"
  ```
