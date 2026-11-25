# 本地开发与运维说明

面向 Memlane 源码开发与本地日常运维，系统三组件与链路术语统一以 [CONTEXT.md](../../CONTEXT.md) 为准：**记忆引擎（Cognee）**、**接入网关**、**Agent 模型代理（`agent_bridge`）**，业务动作区分**自动采集**与**记忆提炼**。

## 启动与运维

从仓库根目录使用 `runtime.json` 中 `python` 字段指定的受管 Python 3.12 运行 `scripts/dev.py`（默认配置文件为 `.local/daily/runtime.json`）。`scripts/dev.py` 在自身进程先执行启动预检，再拉起子进程；裸 `python3` 若解析到 Python 3.9，读取 Codex 配置时会因缺少 `tomllib` 失败。首次运行 `prepare` 时受管环境尚未创建，可用宿主 `python3` 执行。

```sh
# 启动后台服务与桌面窗口
.local/cognee-python/bin/python scripts/dev.py start --config /绝对路径/runtime.json

# 仅启动后台服务（不拉起窗口）
.local/cognee-python/bin/python scripts/dev.py start --no-ui --config /绝对路径/runtime.json

# 状态 / 重开窗口 / 附着前台 UI watcher / 停止 / 异常恢复
.local/cognee-python/bin/python scripts/dev.py {status|open|stop|recover} --config /绝对路径/runtime.json
.local/cognee-python/bin/python scripts/dev.py open --watch-ui --config /绝对路径/runtime.json

# 尚无 active 提炼配置时启动最小配置服务
.local/cognee-python/bin/python scripts/dev.py setup --config /绝对路径/runtime.json

# 开发态显式准备引擎组件（仅准备受管 Python/venv/依赖与上游源码，不建库或写 runtime.json）
python3 scripts/dev.py prepare --component-root /绝对路径/私有引擎组件目录
```

| 动作 | 行为与边界 |
| --- | --- |
| `start` | 获取日常数据单实例锁，依次启动 Agent 模型代理（桥模式）、记忆引擎（Cognee）、接入网关（`project_control_server`）与桌面端；Provider 模式不启动模型代理。已有实例运行时不重复启动，健康检查就绪不代表真实模型推理已验证。 |
| `status` | 校验数据锁、启动器 PID、进程启动时间与配置路径并输出状态摘要；未启动、配置不匹配或残留锁需先核实，不得删锁或盲杀未知进程。 |
| `open` | 向现有启动器发送重开窗口请求，不启动第二套服务；关闭桌面窗口不会停止后台服务。 |
| `open --watch-ui` | 旧启动器后台健康、原 UI watcher 已退出时，在当前终端附着单个前台 MyGo watcher 供开发热重载；退出仅收尾 UI 进程组，不停止后台。 |
| `stop` | 核对数据锁、启动器 PID、内核启动时间与配置路径，收尾所属进程组及模型子进程并释放日常数据锁；等待 `Owned service groups exited; daily store released.` 后再重启。 |
| `recover` | 异常退出后持有同库锁，确认原进程与进程组全部消失、端口空闲、登记与配置锁已释放后安全归档遗留 owner 记录；不终止任何进程，PID 复用或未知监听一律拒绝。 |
| `setup` | 尚无 `active` 提炼配置时，仅拉起受管理 token 认证的配置服务并复用桌面端「记忆提炼」表单；拒绝已存在 `active` 的账本，完成后 `stop` 再正常 `start`。 |
| `prepare` | 开发态组件准备入口，使用 `uv`（支持 `--uv` 指定）准备受管 Python 3.12.12、独立 venv、哈希锁依赖与上游源码，进度写入 `status.json`（就绪为 `components_ready`）；独立 `.app` 终端用户首装由 `internal/bootstrap` 与内置 Setup CLI 托管。 |

### 开发态运维约束

- **单一日常库与重启边界**：日常运行、Native 界面与真机验证统一使用同一 `.local/daily/official-store`，不静默新建空库，日常启动拒绝已知验收 Agent 目录；服务源码、模型或配置变更时先用原配置 `stop`，再修改并 `start`。电脑重启后手动启动，开发态不注册 helper，不恢复历史业务任务。
- **首日持久化**：总览的首次初始化完成日期保存在 `runtime.json` 同目录的 `client-state.json`（可用 `client_state_file` 显式指定），重启、重开或重配不会覆盖；损坏或写入失败保留错误，不用当天日期覆盖。
- **磁盘门禁与仓库钩子**：受管开发任务经 `scripts/task.py` 执行，至少保留 `50 GiB` 可用磁盘空间（运行中低于 `55 GiB` 自动停止；独立 `.app` 安装态监控阈值为最低 `2 GiB` / 告警线 `5 GiB`）。首次提交前执行 `sh scripts/install-hooks.sh` 启用局部 `.githooks` 与仓库级提交身份守卫（不改全局 `~/.gitconfig`）。除非用户明确要求，开发阶段不执行 DMG 打包、签名、公证、更新或 helper 安装。
- **MyGo 字体抗锯齿补丁**：首页字体抗锯齿通过 `scripts/patches/mygo-antialiased.json` 固定 MyGo 0.7.4 两个源文件及其 SHA，`scripts/go.sh` 与 `scripts/dev.py` 经 `scripts/mygo_sdk.py` 在 `.local/mygo-sdk/` 准备私有 SDK 并通过 `-modfile` 编译（不改模块缓存与 `go.mod`/`go.sum`）；升级 MyGo 前需先评审该补丁，上游支持等价主题标志后移除。
- **代码图谱配置**：使用根目录 `mcp-arch.yaml` 的官方 Enola 配置，保留除 `manifests` 外全部默认源码解析器（停用 `manifests` 以防绕过忽略规则扫描 `build/`），更改索引配置后在项目图谱页手动更新，不启动文件监听。

## 统一门禁与静态检查

```sh
# 统一改动感知 / 冻结 / 发版门禁（开发与审改用 --changed / --staged；源码冻结后单一节点用 --full；候选发版用 --release）
python3 scripts/task.py -- python3 scripts/check.py {--changed|--staged|--full|--release}

# Go 全量测试（自动联动 perf_budget_test.go 与 lint_gate_test.go）与静态分析
sh scripts/go.sh test ./...
sh scripts/go.sh vet ./...

# 零依赖静态检查与规则自测（不加载引擎、不调用模型）
python3 scripts/task.py -- python3 scripts/lint.py
python3 scripts/task.py -- python3 -m unittest discover -s scripts -p test_lint.py

# 模型路径改动必跑的参数 / 探针 / 切换 / 启动回归
python3 scripts/task.py -- .local/cognee-python/bin/python -m unittest \
  discover -s engine/python -p "test_generation_*.py"
```

现行 `scripts/lint.py` 规则覆盖 `ML001`–`ML003` 与 `ML006`–`ML022`（测试夹具不参与，且由 `sh scripts/go.sh test ./...` 经 `lint_gate_test.go` 强制联动）：
- **模型与画像边界（`ML001`–`ML003`、`ML006`）**：原生模型配置须经共享 `native_llm_configuration`，禁止字面量默认值与 `or`/`get`/`setdefault` 回退（保留显式 `0`）；禁止用户画像生成（`user_profiles/runner.py`）使用固定总时长超时中断。
- **环境隔离与 UI 性能（`ML007`–`ML012`）**：禁止非白名单模块修改全局 `os.environ`；守卫 MyGo 动效 `ReduceMotion`、禁止 `<500ms` 高频重绘、禁止 `Draw`/`App.View` 热路径同步磁盘或网络 I/O、禁止桌面端落盘首页 SWR 缓存并强制首页冷启动经 `overviewBootstrap` 聚合及延迟 `RefreshIntegrations`。
- **运行时资源与文档隔离（`ML013`–`ML018`）**：守卫异步高频轮询、异步内阻塞子进程、大文件/二进制全量 `read_bytes()` 哈希与临时 worker 清理（`ML015` 覆盖 `scripts/*.py`）、SQLite WAL `PRAGMA journal_size_limit`、禁止无条件 `mygo dev` 热重载与清理回执轮转；`ML018` 拦截正式文档引用本地忽略的过程产物目录。
- **扩展架构与只读存储门禁（`ML019`–`ML022`）**：禁止 Go 桌面端新增全量拉表调用；强制 `/v1` 列表接口游标上限（`<=50`）、UTF-8 JSON 响应与字节预算裁剪；限定只读访问官方库走 `sqlite_ro.open_official_sqlite_ro` 并禁用 `immutable=1`；守卫 `catalog_index.py` 仅保留 5 张轻量元数据白名单表/列。

完整任务分级、双审收敛与发版验收门禁见 [工程规范](../engineering/workflow.md)。

## `runtime.json` 与凭据隔离

配置模板见 [`config/daily-runtime.example.json`](../../config/daily-runtime.example.json)（含 Agent 模型代理兼容字段）与 [`config/provider-runtime.example.json`](../../config/provider-runtime.example.json)（纯直连 Provider 形态），其中路径均为占位符、不能直接启动。真实运行配置、凭据与日常记忆库全部放在 Git 跟踪之外的 `.local/`（私钥/凭据文件 `0600`、目录 `0700`），严禁写入模板、命令行参数、日志或文档。

| 模板字段 | 配置含义 |
| --- | --- |
| `official_data` | 已有官方日常存储根目录（须含 `system/`、`data/`），规范路径持有单实例锁；必须为持久目录（如 `.local/daily/official-store`），严禁沿用 `$TMPDIR` / `memlane-task-*` 临时目录。 |
| `official_source`、`official_archive`、`service_commit` | 固定上游 Cognee 源码目录、原始归档与提交哈希（须匹配 [`engine/coding-memory-components.json`](../../engine/coding-memory-components.json)）；启动前经 `verify_source` v2 校验，禁止修改上游源码或在源码目录留 `.pyc`/`.pyo`。 |
| `python` | 受管 Python 3.12 虚拟环境入口，保留 venv 路径，不替换为符号链接指向的基础解释器。 |
| `service_url` | 桌面端与受管插件共同访问的记忆引擎（Cognee）loopback API origin。 |
| `completion_url` | Agent 模型代理模式所需的本机 origin（`http://127.0.0.1:<端口>`，不含业务路径，服务侧追加 `/v1`）；纯 Provider 模式可省略。 |
| `token_file` | 记忆引擎（Cognee）日常 owner 正式 API key 纯文本文件（`0600`），桌面端去首尾空白后经 `X-Api-Key` 发送。 |
| `completion_token_file` | Agent 模型代理专用 Bearer 凭据文件（`0600`）；纯 Provider 模式可省略。 |
| `service_environment_file` | 私有 JSON 对象文件（`0600`，非 shell `.env`），提供允许列表内的 embedding 配置、默认用户与认证 secret；拒绝未授权键，LLM 端点与凭据由运行配置动态生成。 |
| `project_control` | 接入网关配置：显式 loopback `url`、私有 `registry_file`、独立 `management_token_file`、实例 `agent_control_token_files` 与受管 `plugin_dir`；启动器校验 `engine/managed-project-plugin.json` 摘要，禁止手改插件缓存。 |
| `generation_file` | 私有 schema2 提炼配置账本路径，记录不可变修订、`active`/`next` 与验证回执；凭据仅存私有引用，不可用模板覆盖或手改 `passed`。 |
| `codex_executable`、`auth_home`、`model` | 旧 Codex 代理兼容字段（原生二进制、登录 home 目录与模型分设，不可把 auth 文件填成 home）；已有 Provider `active` 时可省略。 |
| `agent_hosts`、`agent_host_home` | 各宿主选定 profile 与原生可执行路径，及安装器使用的已存在绝对 `HOME` 目录；Agent 接入不依赖提炼模型配置。 |
| `graph_assets` | 官方图谱 Web 静态资产目录，供桌面端沙箱 `mygo.WebView` 加载。 |
| `projects`、`common_datasets` | 项目绑定（`id`、`name`、`path`、`dataset_id` 及可选 `code_dataset_id`/`code_enabled`）与旧显式通用文档关联；dataset ID 须来自同一官方身份真实数据。 |
| `opencode_*`（顶层，开发兼容） | OpenCode 离线插件组件路径 `opencode_plugin_dir`、清单摘要 `opencode_manifest_sha256` 与 `opencode_node_executable`（Node `>= 20`），位于顶层而非 `project_control` 内。 |

各条连接的凭据严格分域，不得混填（如将 owner 登录 JWT、代理 Bearer 或订阅登录混用为 `X-Api-Key`）：

| 连接路径 | 凭据来源与发送方式 |
| --- | --- |
| 桌面端 → 记忆引擎（Cognee） | `token_file` 中由目标服务签发的日常 owner 正式 API key，通过 `X-Api-Key` 发送（无 Bearer 回退）。 |
| 受管插件 → 接入网关 `/cognee/*` | 该 Agent 实例独立官方子主体的 `X-Api-Key`（固定设置 `COGNEE_PLUGIN_IDENTITY=false`、`COGNEE_MANAGED_ENDPOINT=true`），不复制桌面 owner 凭据。 |
| 记忆引擎 → Agent 模型代理 | 服务读取 `completion_token_file` 注入 `LLM_API_KEY`，经 `Authorization: Bearer ...` 调用 `completion_url/v1`。 |
| Agent 模型代理 → 本机 Agent CLI | 仅从目标登录目录提取必要认证状态到单次隔离临时目录，不加载日常插件、skills/hooks、历史或项目工具。 |

系统不启动旧 `memlaned`、私有内容 IPC、独立全文库或第二套索引；日常内容唯一真相始终为 `.local/daily/official-store`。

## 多 Agent 与模型调试

### 1. Agent 模型代理与直连 Provider / Ollama

- **Agent 模型代理（`agent_bridge`）**：支持 `codex`、`antigravity`（Gemini App `agy`，额外检查 `Gemini.app/Contents/Helpers/agy`）、`pi`（需指定已有 `bridge_provider` 为 `deepseek` 或 `ollama`，不复制密钥到 Provider）与 `traex`（TRAE CLI）。优先读取 `agent_hosts.<kind>.executable/profile`，显式配置但无效时不回退。启动与热切换统一经 `agent_bridge_runtime.require_bridge_startup` 核验运行环境、有效模型及摘要，依赖漂移须重新验证（详见 [有效模型契约](agent-effective-model-contract-20261118.md)）。
- **直连 LLM Provider**：协议支持 `openai`、`anthropic`、`gemini`、`custom`（DeepSeek 等 OpenAI 兼容端点选 `custom`），endpoint/model/key 均由用户显式填写，拒绝 HTTP 重定向、凭据不回填界面。
- **本机 Ollama**：在「设置 › 记忆提炼 › 添加 Provider」选「Ollama（本机接口）」，填本机根地址（如 `http://127.0.0.1:11434`）与实际模型 ID，无需 API Key；账本 profile 形如 `{"mode":"provider","provider":"ollama","model":"llama3.2:3b","endpoint":"http://127.0.0.1:11434"}`，经 `ollama_chat/` 路由调用，不回退其他代理或端口，不自动下载模型或拉起服务。
- **保存 / 验证 / 激活分离**：保存草稿与点击验证（消耗对应额度）均不改变运行期唯一 `active`；需选择「下次启动使用」或确认热切换回执后生效。首个候选失败则清理并停止，已存在 `active` 时候选失败仅在资源确认退出后回退原配置一次；状态 `unknown` 时仅只读核对，不新建编号重发。

### 2. 宿主连接调试说明

- **5 个主线支持宿主**：Codex、Antigravity（Gemini App）、pi、TraeX（TRAE CLI）、TRAE CN（IDE）；电脑级零项目授权与接入细节见 [电脑级接入契约](agent-connection-contract.md)。`dev.py` 本身不代装插件或改写宿主配置，准备/预览/启用/核对统一经接入网关 API 执行。
- **本机 Codex 调试要点**：控制进程使用受管 Python 内的 `tomlkit` 原子改写配置并保留注释与第三方插件；同一 `auth_home` 下的 CLI 与 Desktop 共享独立子主体，需分别验证真实 hook 与 `cwd`。遇到冲突/超时/中断走只读「核对原接入操作」，不覆盖或删除旧 buffer。
- **保留兼容的实验宿主（Cursor / OpenCode）**：界面暂未开放，后端保留既有兼容与单测入口。
  - Cursor 生成不可变派生插件目录并经 `hooks.json` 的 `workspaceOpen` 返回 `pluginPaths`，检测旧插件冲突时拒绝自动迁移。
  - OpenCode 需本机 Node `>= 20`，可通过固定 npm 缓存离线构建独立插件组件并验证（缺缓存自动 skip）：
    ```sh
    python3 scripts/task.py -- .local/cognee-python/bin/python scripts/build_opencode_plugin.py \
      --output /绝对路径/新组件目录 --npm-cache /绝对路径/固定npm缓存

    python3 scripts/task.py -- env PYTHONPATH=engine/python \
      MEMLANE_OPENCODE_TEST_NPM_CACHE=/绝对路径/固定npm缓存 \
      .local/cognee-python/bin/python -m unittest test_opencode_installation
    ```

## 国内镜像源与手工终端兜底

本节沉淀自 `v0.1.0-beta.1` 首次安装依赖镜像实测（原历史发版方案已归档至 [release-v0.1.0-beta.1-20261121.md](../archive/release-v0.1.0-beta.1-20261121.md)）。**实际净室下载量与磁盘占用以 [安装依赖包瘦身调研](../research/install-dependency-slimming-20261125.md) 实测为准**（全部 7 项合计官方源实测下载 **`550.58 MiB`**、国内源 **`551.28 MiB`**，解压落盘约 **`1.30–1.50 GiB`**；早期 `environment.go` 预估 `~2.34 GiB / 4.57 GiB` 含保守空间余量）。

### 1. 7 项首次安装依赖下载源与已证实避坑

| 依赖项（ID） | 实测下载量（原预算） | 国外官方源 | 推荐国内镜像源（实测可用） | 校验方式与已证实避坑 |
| --- | --- | --- | --- | --- |
| **引擎运行库**（`runtime`） | `339.61 MiB`（预算 `2 GiB`） | `https://pypi.org/simple/` | 主：`https://pypi.tuna.tsinghua.edu.cn/simple/`（清华 TUNA）<br>备：`https://mirrors.aliyun.com/pypi/simple/`、`https://mirrors.ustc.edu.cn/pypi/simple/` | `uv pip sync --default-index <mirror> --require-hashes --build-constraints requirements.lock`，强制保留 [`requirements.lock`](../../engine/python/requirements.lock) 全部 171 包哈希校验 |
| **本地向量模型**（`embedding`） | `91.23 MiB`（`95.66 MB`，7 文件） | `https://huggingface.co/Qdrant/bge-small-zh-v1.5/resolve/46fbe35fd4374a00fee7de77dfddaeb6dd6a2c59/<file>` 与 `https://huggingface.co/BAAI/bge-small-zh-v1.5/resolve/7999e1d3359715c523056ef9478215996d62a620/<file>` | **魔搭社区 ModelScope**：<br>`https://modelscope.cn/models/Qdrant/bge-small-zh-v1.5/resolve/master/<file>` 与 `https://modelscope.cn/models/BAAI/bge-small-zh-v1.5/resolve/master/<file>` | 7 文件（`Qdrant` 下 `model_optimized.onnx` 94,781,076 B + `config.json` + `tokenizer.json` + `tokenizer_config.json` + `special_tokens_map.json`；`BAAI` 下 `tokenizer.json` + `tokenizer_config.json`）与 [`embedding-components.json`](../../engine/embedding-components.json) 逐字节 SHA256 一致。<br>**避坑**：`hf-mirror.com` 会将 `model_optimized.onnx` 302 重定向至海外 `cas-bridge.xethub.hf.co`，不作国内主源 |
| **Python 3.12.12**（`python`） | `16.85 MiB`（`17.7 MB`） | `https://github.com/astral-sh/python-build-standalone/releases/download/20260211/cpython-3.12.12%2B20260211-aarch64-apple-darwin-install_only_stripped.tar.gz` | **阿里云 npmmirror**：<br>`https://registry.npmmirror.com/-/binary/python-build-standalone/20260211/cpython-3.12.12%2B20260211-aarch64-apple-darwin-install_only_stripped.tar.gz` | 字节数 `17,667,661` 与 [`first-install-components.json`](../../engine/first-install-components.json) 一致，复用同一 SHA256 `22625dea...`。<br>**避坑**：南京大学镜像站（NJU）该构建返回 HTTP 404 |
| **安装工具 `uv` 0.12.3**（`uv`） | 官方 `16.87 MiB` / 国内 whl `17.57 MiB` | `https://github.com/astral-sh/uv/releases/download/0.12.3/uv-aarch64-apple-darwin.tar.gz`（`17,686,637 B`，SHA256 `546f7f8a...`） | **主（大厂 PyPI CDN wheel）**：清华 TUNA / 阿里云：<br>`https://pypi.tuna.tsinghua.edu.cn/packages/e4/66/ba257c91d69921d773f523c9e4c3ffb04e233694c415517f5ef797403a4f/uv-0.12.3-py3-none-macosx_11_0_arm64.whl`（阿里云 `/pypi/packages/...` 同路径）<br>**备（tar.gz）**：`https://gh-proxy.org/<official_url>` 或 `https://gh-proxy.com/<official_url>` | wheel 为 `18,426,262 B`（SHA256 `7c99f2524fe4b11d74dec85cef9b4d8b725d142dec04cd237f1c06fbdae1ee54`），经 Go `archive/zip` 解出 `uv-0.12.3.data/scripts/uv`。<br>**避坑**：`npmmirror`、`NJU` 均无 `uv`（404）；`ghfast.top` 返回 HTTP 403 |
| **OpenSSL 3.5.9**（`native_openssl_arm64`） | `10.33 MiB`（`10.8 MB`） | `https://ghcr.io/v2/homebrew/core/openssl/3/blobs/sha256:82b33ce156b289eaf95657b9ec16e8f49cb49c882cdeaeaf57e3e7d67fa85e25`（需匿名头 `Authorization: Bearer QQ==`） | 主：**中科大 USTC**：`https://mirrors.ustc.edu.cn/homebrew-bottles/v2/homebrew/core/openssl/3/blobs/sha256:82b33ce156b289eaf95657b9ec16e8f49cb49c882cdeaeaf57e3e7d67fa85e25`<br>备：**清华 TUNA**：`https://mirrors.tuna.tsinghua.edu.cn/homebrew-bottles/openssl%403-3.5.9.arm64_sequoia.bottle.tar.gz` | 两者均 `10,829,301 B`，复用同一 SHA256 `82b33ce1...`。<br>**避坑**：TUNA 与阿里云的 `/v2/.../blobs/sha256:...` 返回 404，须按站点区分 URL；请求国内镜像时**不得**附带 `Authorization` 头 |
| **Cognee 上游源码**（`cognee-source`） | `68.95 MiB`（预算 `150 MiB`） | `https://codeload.github.com/topoteretes/cognee/tar.gz/ec6f40859e5f2df9e7862dde27d580ac3268ed43` | 公测主：`https://gh-proxy.org/<codeload_url>`<br>公测备：`https://gh-proxy.com/<codeload_url>` | 强制核对 [`coding-memory-components.json`](../../engine/coding-memory-components.json) 固定 SHA256 `6e11047e...`。<br>**避坑与红线**：`ghfast.top` 对 `codeload` TLS 超时已排除；严禁向第三方反代发送账号/Token，严禁重打包改变归档哈希 |
| **官方插件源码**（`agent-plugin`） | `6.74 MiB`（预算 `20 MiB`） | `https://codeload.github.com/topoteretes/cognee-integrations/tar.gz/a176248914fdb32a78354854b300174203b15a5c` | 公测主：`https://gh-proxy.org/<codeload_url>`<br>公测备：`https://gh-proxy.com/<codeload_url>` | 强制核对固定 SHA256 `a896399a...`；后续发版建议将两份上游原样归档迁移至自维护国内只读静态源，公益反代降为备选 |

### 2. 下载器、代理与「重新检测」契约

- **系统代理兼容**：[source.go](../../internal/bootstrap/source.go) 与 [runtime.go](../../internal/bootstrap/runtime.go) 读取 macOS 系统代理（`scutil --proxy`）并兼容 `HTTP_PROXY` / `HTTPS_PROXY` / `ALL_PROXY` / `NO_PROXY`，避免双击 `.app` 启动丢失代理；代理地址与凭据严禁写入日志或诊断导出包。
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
- **Cognee 上游源码（`ec6f408`）与官方插件源码（`a176248`）**（[source.go](../../internal/bootstrap/source.go) 原生复用 SHA256 匹配的已有归档，`agent-plugin` 替换对应 URL 与目标目录）：
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
