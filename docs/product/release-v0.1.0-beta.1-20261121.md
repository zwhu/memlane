# v0.1.0-beta.1 小范围公测发版方案与国内镜像源调研

2026-11-21。当前仓库基线：`bdcaf2d`（P9 代码与领域术语统一已入库，工作区干净；日常 `dev.py` 与桌面窗口仍待重启做 P9 实窗终验）。

状态：已确认 `v0.1.0-beta.1` 小范围公测发版边界并完成 **M1**（提交 `e9eb1c0`；实窗目视待重启确认）、**M2**（提交 `e9eb1c0`；独立 `.app` 功能闭环、国内外源切换与手动 CLI 备选安装、GitHub Releases 更新检查跳转、安全卸载脚本及全量自动化测试）与 **M3**（提交 `859a4ad`、`e46957d`；已完成 `mygo.json` 版本与系统基线调整、[build_release.py](../../scripts/build_release.py) 构建脚本、`Memlane-0.1.0-beta.1-arm64.dmg` 与 `SHA256SUMS.txt` 本地打包验签、以及 [公测安装指南](beta-install-guide.md) 与 [第三方许可声明](../../THIRD_PARTY_LICENSES.md)）；当前尚未进入 **M4**（无缓存净室真机连续场景验收与实际字体目视确认）或对外推送/发布。不改变现有日常单库，不修改上游 Cognee 源码。统一领域词汇见 [CONTEXT.md](../../CONTEXT.md)。

## 一、`v0.1.0-beta.1` 公测发版边界（已确认）

| 维度 | 已确认边界 | 关键约束与说明 |
| --- | --- | --- |
| **版本号与分发渠道** | `v0.1.0-beta.1`（GitHub Releases Pre-release） | 分发 `arm64` `.dmg` 安装镜像与 `SHA256SUMS.txt`；采用 ad-hoc 签名，发布页需附 macOS Gatekeeper 放行指引（`xattr -dr com.apple.quarantine /Applications/Memlane.app` 或「系统设置 → 隐私与安全性 → 仍要打开」），并明示未公证签名包上「登录时静默启动」受系统登录项权限限制（[desktop_preferences.go](../../internal/simpledesktop/desktop_preferences.go) 已有失败回滚与诚实提示） |
| **目标平台与系统版本** | 仅 Apple Silicon (`arm64`)，最低 **macOS 14.0+** | 将 [mygo.json](../../mygo.json) 中的 `minimumSystemVersion` 从 `"15.0"` 下调为 `"14.0"`；本版不提供 Intel (`amd64`) 或 Universal 安装包 |
| **首发 Agent 范围（精简 4 个）** | **Codex、Antigravity（Gemini App）、pi、TraeX** | 界面仅开放上述 4 个已完成真机主链路验收的宿主，暂隐藏未完成真机闭环的 Claude Code、Cursor 与 OpenCode；保留已有后端数据兼容性，首次安装不携带 OpenCode Node / 插件构建包 |
| **首次安装下载体验** | **国内镜像源 + 国外官方源**自主选择 + 统一「重新检测」校验 | 默认提供国内镜像源安装，同时支持切换国外官方源，并透明展示各依赖实际下载地址，允许自动下载或手动准备后点击「重新检测」逐项复核 SHA256 与离线探针，全部通过后放行；下载器需兼容读取 macOS 系统代理（`scutil --proxy`）与环境变量代理 |
| **后台服务拉起链** | 沿用 `internal/supervisor` → 内嵌 `scripts/dev.py --no-ui` | 公测前不切换为 Go `StartFirstRuntimeSupervised` 直管三进程，复用现役已验证的生命周期、健康检查与独占锁机制 |
| **更新与卸载能力** | 轻量「检查新版本 + 打开下载页」+ 安全卸载清理脚本/指引 | 本版暂不接入 `mygo.Updater` 自动更新与图形化卸载器；设置页/菜单提供 GitHub Releases 版本检查与下载跳转，随附安全卸载脚本移除 Memlane 独占依赖与受管宿主接入项，不删除用户已有共享环境与记忆库 |
| **明确不做范围** | 不含正式商用打包与后续规划特性 | 不做 Intel (`amd64`)、Apple Developer ID 签名与公证、Claude Code / Cursor / OpenCode 真机接入、本机 Docker / 远程 Cognee、ChatGPT 普通对话采集、词云与 Top-K |

## 二、首次安装国内镜像源与国外官方源实测调研（2026-11-21）

> **事实边界**：以下结论基于 2026-11-21 对各候选节点执行的 HTTP `HEAD` / `Range` / gzip 首字节实测与文件清单核对；完整整包下载与净室端到端 SHA256 校验留待 M4 实机验收完成。

### 1. 7 项首次安装依赖下载源对照表

| 依赖项（ID） | 预估下载量 | 国外官方源 | 推荐国内镜像源（2026-11-21 实测） | 校验方式与已证实避坑 |
| --- | --- | --- | --- | --- |
| **引擎运行库**（`runtime`） | ~2 GiB | `https://pypi.org/simple/` | 主：`https://pypi.tuna.tsinghua.edu.cn/simple/`（清华 TUNA，实测 HTTP 200）<br>备：`https://mirrors.aliyun.com/pypi/simple/`、`https://mirrors.ustc.edu.cn/pypi/simple/`（均 HTTP 200） | `uv pip sync --default-index <mirror> --require-hashes --build-constraints requirements.lock`，严格保留 [requirements.lock](../../engine/python/requirements.lock) 全量哈希强制校验 |
| **本地向量模型**（`embedding`） | ~95.7 MiB（7 文件） | `https://huggingface.co/Qdrant/bge-small-zh-v1.5/resolve/46fbe35fd4374a00fee7de77dfddaeb6dd6a2c59/<file>` 与 `https://huggingface.co/BAAI/bge-small-zh-v1.5/resolve/7999e1d3359715c523056ef9478215996d62a620/<file>` | **魔搭社区 ModelScope**：<br>`https://modelscope.cn/models/Qdrant/bge-small-zh-v1.5/resolve/master/<file>` 与 `https://modelscope.cn/models/BAAI/bge-small-zh-v1.5/resolve/master/<file>` | **实测通过**：`Range` 请求返回 HTTP 206，重定向至国内 `cdn-lfs-cn-1.modelscope.cn`，7 文件（含 `model_optimized.onnx` 94,781,076 字节及全部 config/tokenizer）与 [embedding-components.json](../../engine/embedding-components.json) 逐字节 SHA256 100% 一致。<br>**避坑**：`hf-mirror.com` 会将 `model_optimized.onnx` 302 重定向至海外 `cas-bridge.xethub.hf.co`，不选作国内主源 |
| **Python 3.12.12**（`python`） | 17.7 MB | `https://github.com/astral-sh/python-build-standalone/releases/download/20260211/cpython-3.12.12%2B20260211-aarch64-apple-darwin-install_only_stripped.tar.gz` | **阿里云 npmmirror**：<br>`https://registry.npmmirror.com/-/binary/python-build-standalone/20260211/cpython-3.12.12%2B20260211-aarch64-apple-darwin-install_only_stripped.tar.gz` | **实测通过**：HTTP 200，跳转 `cdn.npmmirror.com`，字节数 `17667661` 与 [first-install-components.json](../../engine/first-install-components.json) 一致，复用原 SHA256 `22625dea...`。<br>**避坑**：南京大学镜像站（NJU）该历史构建返回 HTTP 404，不采用 |
| **安装工具 `uv` 0.12.3**（`uv`） | ~18 MB | `https://github.com/astral-sh/uv/releases/download/0.12.3/uv-aarch64-apple-darwin.tar.gz`（17,686,637 字节，SHA256 `546f7f8a...`） | **方案 A（推荐，大厂稳定 CDN）**：清华 TUNA / 阿里云 PyPI 下载官方 wheel：<br>`https://pypi.tuna.tsinghua.edu.cn/packages/e4/66/ba257c91d69921d773f523c9e4c3ffb04e233694c415517f5ef797403a4f/uv-0.12.3-py3-none-macosx_11_0_arm64.whl`（阿里云 `/pypi/packages/...` 同路径）<br>**方案 B（备选 tar.gz）**：`https://gh-proxy.org/<official_url>` 或 `https://gh-proxy.com/<official_url>` | **实测通过**：wheel 在 TUNA 与阿里云均 HTTP 200（18,426,262 字节，SHA256 `7c99f2524fe4b11d74dec85cef9b4d8b725d142dec04cd237f1c06fbdae1ee54`），可由 Go 标准库 `archive/zip` 解出 `uv-0.12.3.data/scripts/uv`；`gh-proxy.org` 返回 200 且字节数 `17686637` 一致。<br>**避坑**：`npmmirror`、`NJU` 均无 `uv` 二进制（404）；`ghfast.top` 返回 HTTP 403 |
| **OpenSSL 3.5.9**（`native_openssl_arm64`） | 10.8 MB | `https://ghcr.io/v2/homebrew/core/openssl/3/blobs/sha256:82b33ce156b289eaf95657b9ec16e8f49cb49c882cdeaeaf57e3e7d67fa85e25`（需匿名头 `Authorization: Bearer QQ==`） | 主：**中科大 USTC**：`https://mirrors.ustc.edu.cn/homebrew-bottles/v2/homebrew/core/openssl/3/blobs/sha256:82b33ce156b289eaf95657b9ec16e8f49cb49c882cdeaeaf57e3e7d67fa85e25`<br>备：**清华 TUNA**：`https://mirrors.tuna.tsinghua.edu.cn/homebrew-bottles/openssl%403-3.5.9.arm64_sequoia.bottle.tar.gz` | **实测通过**：USTC（OCI 路径）与 TUNA（扁平路径）均 HTTP 200 且字节数 `10829301` 一致，复用同一 SHA256 `82b33ce1...`。<br>**避坑**：TUNA 与阿里云的 `/v2/.../blobs/sha256:...` 均返回 HTTP 404，必须按镜像站区分 URL；请求国内镜像时不得附带 `Authorization` 头 |
| **Cognee 上游源码**（`cognee-source`） | 上游归档（见注） | `https://codeload.github.com/topoteretes/cognee/tar.gz/ec6f40859e5f2df9e7862dde27d580ac3268ed43` | 公测主：`https://gh-proxy.org/<codeload_url>`<br>公测备：`https://gh-proxy.com/<codeload_url>` | **实测通过**：两者均 HTTP 200 且返回有效 gzip 魔数 `\x1f\x8b`，下载后强制核对 [coding-memory-components.json](../../engine/coding-memory-components.json) 固定 SHA256 `6e11047e...`。<br>**避坑与红线**：`ghfast.top` 对 `codeload` TLS 握手超时已排除；严禁向任何第三方反代发送账号/Token，严禁重打包改变归档哈希 |
| **官方插件源码**（`agent-plugin`） | 上游归档（见注） | `https://codeload.github.com/topoteretes/cognee-integrations/tar.gz/a176248914fdb32a78354854b300174203b15a5c` | 公测主：`https://gh-proxy.org/<codeload_url>`<br>公测备：`https://gh-proxy.com/<codeload_url>` | **实测通过**：两者均 HTTP 200 且返回有效 gzip 魔数 `\x1f\x8b`，下载后强制核对固定 SHA256 `a896399a...`。<br>**长期建议**：后续建议将上述两份原样未改动归档同步至自维护只读国内静态源（如 ModelScope Dataset / CNB / 自有 OSS）作为一级国内源，公益反代降级为二级备选 |

> 注：ModelScope `bge-small-zh-v1.5` 全部 7 个文件已由仓库 API 核对完整 SHA256，与 [embedding-components.json](../../engine/embedding-components.json) 一一对应：`Qdrant/bge-small-zh-v1.5` 下 `model_optimized.onnx`、`config.json`、`tokenizer.json`、`tokenizer_config.json`、`special_tokens_map.json`；`BAAI/bge-small-zh-v1.5` 下 `tokenizer.json`、`tokenizer_config.json`。`cognee-source` 与 `agent-plugin` 的精确字节数在 [environment.go](../../internal/bootstrap/environment.go#L167-L169) 当前以估算上限（`150 MiB` / `20 MiB`）登记，实测整包精确大小在 M4 净室拉取时一并记录。

### 2. 下载器、系统代理与「重新检测」契约（M2 实施依据）

- **源选择与 URL 透明展示**：首次安装向导及「设置 › 服务 › 运行环境」提供源切换（默认「国内镜像源」，可切换「国外官方源」），并在各依赖详情展示当前源及国外官方源下载地址，支持复制地址后由用户自行下载或定位已有文件/目录。
- **macOS 系统代理与环境变量兼容**：[source.go](../../internal/bootstrap/source.go) 的 `fetchArchive` 与 [runtime.go](../../internal/bootstrap/runtime.go) 的 `runManaged` 需补齐读取 macOS 系统代理（`scutil --proxy`）并兼容 `HTTP_PROXY` / `HTTPS_PROXY` / `ALL_PROXY` / `NO_PROXY`，避免双击 `.app` 启动时丢失代理配置；代理地址与任何凭据不得写入日志或诊断导出包。
- **`uv` 与 `archive/zip` 支持**：当选择国内镜像源且采用 PyPI 官方 wheel（`uv-0.12.3-py3-none-macosx_11_0_arm64.whl`）时，[tools.go](../../internal/bootstrap/tools.go) 在按整包 SHA256 校验通过后通过 Go 标准库 `archive/zip` 解出 `uv-0.12.3.data/scripts/uv` 并赋予可执行权限，不改变 `uv` 对外路径与探针契约。
- **安全与完整校验闭环**：切换镜像源只改变下载源 URL 与备选回退顺序，**不降低任何 SHA256 校验、TLS HTTPS 校验与路径归属保护**；仅当目标 host 为 `ghcr.io` 且路径前缀为 `/v2/homebrew/core/` 时附带匿名头 `Authorization: Bearer QQ==`。无论自动下载、失败重试或用户手动准备完依赖，点击**「重新检测」**均重新执行完整 SHA256 校验与离线能力探针，全部显示可用后方可继续下一步。

### 3. 官网 `curl` / CLI 一键复制备选安装方案（手工终端兜底）

当用户网络环境复杂、更习惯在自己的终端（可继承个人代理配置）安装，或个别内置镜像节点临时异常时，M2 会在界面为缺失依赖提供可一键复制的终端安装命令，执行完毕点击**「重新检测」**即可自动接管校验：

- **安装工具 `uv`**：
  - 官网一键脚本（默认装至 `~/.local/bin/uv`）：`curl -LsSf https://astral.sh/uv/install.sh | sh`
  - Homebrew 备选：`brew install uv`
  - **自动发现补齐（M2）**：[environment.go](../../internal/bootstrap/environment.go#L141-L151) 当前仅查找 `PATH`、`/opt/homebrew/bin/uv` 与 `/usr/local/bin/uv`，M2 需补充扫描 `~/.local/bin/uv`，确保用户运行官网 `curl` 脚本后点「重新检测」直接复用。
- **Python 3.12**：
  - 国内推荐命令：`UV_PYTHON_INSTALL_MIRROR="https://registry.npmmirror.com/-/binary/python-build-standalone/" uv python install 3.12.12`
  - 官网官方命令：`uv python install 3.12.12`（或 `brew install python@3.12`）
  - **自动识别**：[probe.go](../../internal/bootstrap/probe.go#L34) 的 `PythonCandidates` 已内置扫描 `~/.local/share/uv/python/cpython-3.12*/bin/python3.12` 与 `/opt/homebrew/bin/python3.12`。
- **Cognee 上游源码（`ec6f408`）与官方插件源码（`a176248`）**：
  - 使用 `curl -fL --create-dirs` 下载原始压缩包至受管目标或自定义位置（以 Cognee 源码为例，`agent-plugin` 同理替换 URL 与目标目录）：
    - 国外官网直链：`curl -fL --create-dirs -o "<cognee目录>/service.tar.gz" "https://codeload.github.com/topoteretes/cognee/tar.gz/ec6f40859e5f2df9e7862dde27d580ac3268ed43"`
    - 国内镜像直链：`curl -fL --create-dirs -o "<cognee目录>/service.tar.gz" "https://gh-proxy.org/https://codeload.github.com/topoteretes/cognee/tar.gz/ec6f40859e5f2df9e7862dde27d580ac3268ed43"`
  - **自动识别与校验**：[source.go](../../internal/bootstrap/source.go#L130) 的 `fetchArchive` 原生支持复用 SHA256 匹配的已有压缩包（跳过重复下载），用户也可手动解压后通过界面「指定已有路径」填入源码与归档路径，经 `VerifySource` 核对固定 SHA256 与包白名单。
- **OpenSSL 3.5.9 原生库（`arm64`）**：
  - 直接落盘至 `<runtime目录>/native/artifact.tar.gz` 后由安装器校验解包：
    - 国内 USTC：`curl -fL --create-dirs -o "<runtime目录>/native/artifact.tar.gz" "https://mirrors.ustc.edu.cn/homebrew-bottles/v2/homebrew/core/openssl/3/blobs/sha256:82b33ce156b289eaf95657b9ec16e8f49cb49c882cdeaeaf57e3e7d67fa85e25"`
    - 国外官方 `ghcr.io`：`curl -fL --create-dirs -H "Authorization: Bearer QQ==" -o "<runtime目录>/native/artifact.tar.gz" "https://ghcr.io/v2/homebrew/core/openssl/3/blobs/sha256:82b33ce156b289eaf95657b9ec16e8f49cb49c882cdeaeaf57e3e7d67fa85e25"`
- **本地向量模型（`BAAI/bge-small-zh-v1.5`，7 文件）**：
  - 按 HuggingFace 缓存快照目录（`hub/models--Qdrant--bge-small-zh-v1.5/snapshots/46fbe35fd4374a00fee7de77dfddaeb6dd6a2c59/` 及 `hub/models--BAAI--bge-small-zh-v1.5/snapshots/7999e1d3359715c523056ef9478215996d62a620/`）使用 `curl -fL --create-dirs` 拉取全部 7 个文件（默认 ModelScope 国内直链，附 HuggingFace 官网直链），落盘至 `<组件目录>/embedding-<摘要>/` 或在「指定已有路径」填入 `ModelCache` 后点击「重新检测」，由 [model.go](../../internal/bootstrap/model.go#L60) `VerifyModelFiles` 逐一核对 SHA256 并运行离线编码探针。
- **引擎运行库（`runtime`）**：
  - 在 `uv` 与 Python 3.12 就绪后，可复制执行锁定同步命令（含 `requirements.lock` 哈希校验）：
    - 创建环境：`uv venv --python "<python3.12>" "<runtime目录>/venv"`
    - 国内镜像同步：`uv pip sync --python "<runtime目录>/venv/bin/python" --default-index https://pypi.tuna.tsinghua.edu.cn/simple/ --require-hashes --build-constraints "<lock路径>" "<lock路径>"`
    - 官网官方同步：`uv pip sync --python "<runtime目录>/venv/bin/python" --require-hashes --build-constraints "<lock路径>" "<lock路径>"`
- **界面呈现原则**：M2 在首次安装向导与「设置 › 服务 › 运行环境」中为缺失依赖及整体环境提供「复制终端安装命令」按钮，命令自动填入本机真实目标路径并随「国内镜像源 / 国外官方源」切换同步更新；用户执行后点「重新检测」统一复核。

## 三、四阶段实施里程碑（M1–M4）与验收门槛

| 阶段 | 核心交付事项 | 验收门槛 |
| --- | --- | --- |
| **M1：收口 P9 与已知代码欠账**（代码与单测已完成；实窗目视待并行改动稳定后重启确认） | ① 通过 [quit_policy.go](../../cmd/memlane-app/quit_policy.go) 与 [main.go](../../cmd/memlane-app/main.go) 修复 `⌘Q` / 系统退出未遵循「退出时保持后台运行」开关的缺口（`OnWillQuit` 同步释放托盘图标，6 项单测覆盖并发防重入与开/关保活切换）；<br>② 复核 [architecture-refactor-20261120.md](../architecture/architecture-refactor-20261120.md#L36) 原记录的 2 个旧 Python 测试已全部通过（35/35），并顺带修复 [test_performance_trace.py](../../engine/python/test_performance_trace.py#L80) 缺失 `auth/config.toml` 的过时夹具；<br>③ 在线 `GET /v1/projects/{id}/message-summary` 在停服前已核对返回 HTTP 200 与完整字段；因工作区存在并行在途改动且当前日常 `scripts/dev.py` 未运行，最新原生 UI 实窗、状态栏弹层点击与深浅色目视留待并行改动稳定、重启 `dev.py start` 后确认 | `./cmd/memlane-app` `go vet` / `-race` 与修复前全量 `go test ./...` 通过；`lint.py` 与 `scripts` 92/92 通过；`test_performance_trace` + `test_codex_completion_endpoint` 29/29 通过（此前 `engine/python` 549 项单测唯一失败项已修复）；实窗及状态栏人工目视待确认 |
| **M2：独立 `.app` 功能闭环与安装体验补齐**（代码与自动化测试已完成；真机/净室实测留待 M4） | ① [runtime_assets.go](../../runtime_assets.go) 已嵌入 `web/official-graph/dist/*`，[first_install_runtime.py](../../engine/python/first_install_runtime.py) 支持自动与 `--graph-assets` 写入 `graph_assets`（[first_runtime.go](../../internal/bootstrap/first_runtime.go) 已透传），[graph.go](../../internal/simpledesktop/graph.go) 实现按需零落盘 `RegisterGraphFS`；<br>② [integrations_ui.go](../../internal/simpledesktop/integrations_ui.go) 已收敛展示 Codex / Antigravity / pi / TraeX 四个首发宿主并保留既有已配置记录；<br>③ 新增 [mirrors.go](../../internal/bootstrap/mirrors.go)、[proxy.go](../../internal/bootstrap/proxy.go)、[manual_commands.go](../../internal/bootstrap/manual_commands.go) 并接入全部依赖驱动与 [environment_setup_ui.go](../../internal/simpledesktop/environment_setup_ui.go) / [initial_setup_layout.go](../../internal/simpledesktop/initial_setup_layout.go)，落地国内镜像源 / 国外官方源切换、macOS 系统代理读取、`archive/zip` `uv` wheel 解包、`~/.local/bin/uv` 与 `~/.cargo/bin/uv` 自动发现、按当前源复制终端安装命令及「重新检测」校验；<br>④ 新增 [update_check.go](../../internal/simpledesktop/update_check.go)（「设置 › 服务 › 版本与更新」手动检查 GitHub Releases 含预发布版本并安全跳转发布页）与无第三方依赖安全卸载脚本 [uninstall.py](../../scripts/uninstall.py)（默认 dry-run、`.service-owner` 锁保护、防符号链接逃逸、保留记忆库、四宿主仅删 Memlane 配置项并备份） | `sh scripts/go.sh vet ./...`、`go test -race ./cmd/memlane-app ./internal/bootstrap ./internal/supervisor`、全量 `go test ./...`（含图谱内嵌、4 宿主过滤、镜像/代理/wheel/手动命令、`update_check_test`、性能与 lint 门禁）、`lint.py`、`scripts` 单测 `97/97` 及 `first_install_*` `28/28` 全部通过；无缓存净室下载与独立 `.app` 实机验收留待 M4 |
| **M3：构建 `v0.1.0-beta.1` DMG 与公测文档**（代码、构建与校验已完成；`.app` 实机字体目视留待 M4） | ① 更新 [mygo.json](../../mygo.json)（`version: "0.1.0-beta.1"`、`macos.minimumSystemVersion: "14.0"`）；<br>② 新增发布构建脚本 [build_release.py](../../scripts/build_release.py) 与单测 [test_build_release.py](../../scripts/test_build_release.py)，支持 `--git-ref HEAD` 从干净 worktree 串联 [scripts/mygo_sdk.py](../../scripts/mygo_sdk.py) 私有字体抗锯齿补丁、`web/official-graph/dist` 与 `mygo build`，自 `e46957d` 产出 `build/release-v0.1.0-beta.1/darwin-arm64/` 下 `Memlane.app`（`22.3 MB`，`arm64`）、`Memlane-0.1.0-beta.1-arm64.dmg`（`6.0M`，SHA256 `540850f272bcd279f905db27e4b88e006b96634d4846db8bdce0335c1cdb8c1d`）与 `SHA256SUMS.txt`；<br>③ 新增 [beta-install-guide.md](beta-install-guide.md) 与 [THIRD_PARTY_LICENSES.md](../../THIRD_PARTY_LICENSES.md) 并接入 [docs/README.md](../README.md) | `scripts/test_build_release.py`（全量 `scripts` 单测 `101/101`）与 `lint.py` 通过；`Memlane.app` 的 `Info.plist`（`0.1.0-beta.1` / `14.0`）、`codesign --verify --deep --strict`（adhoc）、`hdiutil verify` 与 `shasum -a 256 -c SHA256SUMS.txt` 全部通过（提交 `859a4ad`、`e46957d`）；`.app` 实窗字体渲染目视与净室安装留待 M4 |
| **M4：无缓存净室实机验收与发布** | 在无缓存、未预装项目环境的干净 macOS 14+ Apple Silicon 环境从双击 `.dmg` 起执行连续场景验收：<br>Gatekeeper 放行 → 国内镜像源首次安装（含中断/手动补齐后「重新检测」）→ 首次配置（模型验证与激活）→ 主窗/图谱/状态栏与后台保活 → 接入 4 个首发 Agent → 自动采集 → 记忆提炼 → 阅读与实际召回 → 卸载脚本演练 | 全连续场景实际证据按过程文档规范本机留档；确认零阻塞缺陷后打 tag 并在 GitHub Releases 发布 `v0.1.0-beta.1`（Pre-release） |

M4 每个候选包须满足[首装发版门禁](../engineering/workflow.md#首装发版门禁)。

## 四、已知风险与后续正式版（v1.0）增量

1. **公测期主要风险**：
   - **公益 GitHub 反代波动**：`gh-proxy.org` / `gh-proxy.com` 为第三方公益节点，极端情况下可能限流或临时不可用；界面需保留国外官方源 URL 与「手动下载 + 重新检测」兜底路径，并在后续版本尽快迁移至自维护国内静态镜像源。
   - **未公证提示与登录项受限**：ad-hoc 签名包首次打开会触发 Gatekeeper 拦截，且 macOS `SMAppService`「登录时静默启动」可能被系统拒绝；需在公测 Release Notes 明确告知边界。
2. **从 `v0.1.0-beta.1` 迈向正式版 `v1.0` 的增量工作**：
   - 接入 Apple Developer ID Application 证书签名与 `notarytool` 公证装订（同时解锁系统「登录项」完整体验）。
   - 将 Cognee 与插件上游原始归档镜像迁移至自维护国内只读静态存储（保留原归档字节与固定 SHA256）。
   - 落地 [macos-updater-20261119.md](../research/macos-updater-20261119.md) 规划的 `mygo.Updater` 应用内更新（含签名兼容清单与停服务/独占锁协调）与图形化卸载引导。
   - 按实际真机验收进度逐步恢复开放 Claude Code、Cursor 与 OpenCode。

## 关联基准文档

- [产品定义](product.md) / [后续 Roadmap](roadmap.md)
- [公测安装、校验与卸载指南](beta-install-guide.md) / [第三方开源许可声明](../../THIRD_PARTY_LICENSES.md)
- [视觉 V4 实施账本](../design/visual-v4-implementation-20261120.md)
- [架构四阶段改造记录](../architecture/architecture-refactor-20261120.md)
- [本地开发与配置说明](../architecture/development.md)
- [macOS 首发更新机制建议](../research/macos-updater-20261119.md)
