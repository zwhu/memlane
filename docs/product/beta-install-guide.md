# Memlane v0.1.0-beta.3 内测安装与卸载指南

## 1. 运行环境与要求

- **芯片与系统**为 Apple Silicon（`arm64`，M1/M2/M3/M4/M5 系列 Mac）和 **macOS 26 及以上**。本内测版不提供 Intel（`amd64`）包。
- **磁盘空间**方面，首次准备需要约 **4–6 GiB**，用于隔离安装 Python 3.12、记忆引擎运行库（含 Cognee、LanceDB、Ladybug、`fastembed` / `onnxruntime` 等）及本地向量模型 `BAAI/bge-small-zh-v1.5`。

## 2. 安装 Memlane

1. 在项目发布页找到 `v0.1.0-beta.3` 的 `.dmg`。下载后双击，把 **Memlane** 拖入「应用程序」，再从「应用程序」打开 Memlane。[GitHub 发布页](https://github.com/zwhu/memlane/releases)
2. 如果弹出「未打开‘Memlane’」，点 **「完成」**，确认安装包来自项目发布页。
3. 打开 **系统设置 → 隐私与安全性**，向下滚动到 **「安全性」**。找到「已阻止‘Memlane’以保护 Mac」右侧的 **「仍要打开」**，点击它。
4. 在确认框里再次点 **「仍要打开」**，按 macOS 提示用管理员触控 ID 或密码完成验证。Memlane 打开后就可以继续使用。

![macOS 隐私与安全性页面显示 Memlane 被阻止，提示右侧是「仍要打开」按钮](../images/memlane-gatekeeper-open-anyway-20261123.jpg)

## 3. 为什么会出现系统提示

内测版使用 ad-hoc 签名，尚未经过 Apple 公证。macOS 可能在首次打开时拦截。「仍要打开」会为这个 App 单独添加例外。[Apple 操作说明](https://support.apple.com/zh-cn/102445)

确认安装包来自项目发布页后，也可使用以下终端命令。

```sh
xattr -dr com.apple.quarantine /Applications/Memlane.app
```

## 4. 首次启动与下载来源说明

1. 首次运行引导支持在**「国内镜像源」**（默认，优先走清华 PyPI / 阿里云 npmmirror / 中科大 / ModelScope / GitHub 加速节点）与**「国外官方源」**之间切换，并自动读取 macOS 系统代理配置（`scutil --proxy`）与标准代理环境变量；**无论选用哪个下载源，均强制校验固定 SHA256 摘要，不降低任何安全基线**。
2. 如遇公司内网或个别镜像节点临时不可用，可在界面点击**「复制安装命令」**（或在安装确认弹层复制单项命令）后粘贴到个人终端执行，完成后回到 Memlane 点击**「重新检测」**，应用会自动识别并复用通过校验的组件（包括通过官网脚本安装到 `~/.local/bin/uv` 的 `uv`）。
3. 已有日常开发库用户若保持默认独立路径，不会覆盖现有日常记忆库或个人 Python 环境。

### 4.4 让本机 Coding Agent 帮你自动安装或排障（零 Skill）

`Memlane.app` 内置 Setup CLI（`/Applications/Memlane.app/Contents/MacOS/Memlane <subcmd> --json`），可以先只读检查，再预览并确认改动。把应用拖入「应用程序」并按 §2–§3 完成 macOS 放行后，选择下方对应的模板，粘贴给能在本机执行终端命令的 Coding Agent，例如 TRAE CN、Codex、Claude Code、Antigravity、Cursor 或 pi。无需安装 Skill。

**模板 A · 全新安装与首次配置**

```text
帮我完成本机 Memlane 的首次安装与配置（无需安装 Skill）：
1. 先只读运行：/Applications/Memlane.app/Contents/MacOS/Memlane doctor --json
2. 按 next_actions 顺序推进：所有 requires=explicit_confirmation 的 --apply 操作，先给我看 --plan / --dry-run 预览并等我确认；若返回退出码 20，按 retry_after_ms 用 doctor --operation-id <id> --json 轮询进度直到完成。
3. 严禁读取或输出任何 token/密钥/.local/聊天内容，严禁执行 xattr 或手动修改 hooks.json/Cognee 源码。若返回退出码 11 或同一问题重试 3 次仍失败，立刻停止并把 human_steps 交给我。
```

**模板 B · `beta.2` 升级卡死自救**

```text
帮我安全恢复升级后卡住的 Memlane（旧版后台 / 409 待核对 / TRAE CN hooks 报错）：
1. 先只读运行：/Applications/Memlane.app/Contents/MacOS/Memlane doctor --json
2. 严格按 blockers 先运行对应 repair 的 --dry-run --json 给我确认（保留原记忆库、已下载组件和未知回执，不手工改文件、不读凭据）；确认后执行 --apply，并用 verify --stage setup --expect-zero-downloads --expect-runtime-current --json 复验。
3. 若返回退出码 11 或 40（需人工核对）或重试 3 次失败，立刻停止交给我处理。
```

**模板 C · Agent 连接 / 自动采集排障**

```text
帮我排查 Memlane 与本机 Coding Agent 的连接或自动采集问题：
1. 先只读运行：/Applications/Memlane.app/Contents/MacOS/Memlane doctor --include-logs --json
2. 仅通过 next_actions 中的 connect / repair 命令先预览、经我确认再 --apply，不手工编辑宿主配置文件或读取凭据。
3. 完成后运行 verify --stage connection --agent <kind> --json 核验，若返回退出码 11 请把重启宿主/开启全局 Hooks/新会话验证步骤交给我。
```

**只使用桌面端 Agent 时**，TRAE CN 接入后支持自动采集对话。要将对话自动整理成**记忆**，还需在本机安装并登录可用于后台提炼的 CLI（如 `codex-cli`、`trae-cli` / `traex`、`pi` 或 `agy`），或在 **「设置 › 记忆提炼」**中配置直连模型 API 或 Ollama。

你也可以选择**「暂不配置提炼，先进入 Memlane」**体验界面。安装并登录 CLI 或配好模型 API 后，在桌面端点击**「重新检测」**即可启用自动记忆提炼，无需重启 Memlane。

- **Codex CLI**可用 `npm i -g @openai/codex` 安装，然后在终端运行 `codex` 完成登录。[Codex CLI 官方文档](https://developers.openai.com/codex/cli)
- **TRAE CLI / TraeX**按官方方式安装，安装后在终端完成登录。[TRAE CLI 官方快速开始](https://docs.trae.cn/cli_get-started-with-trae-cli-2?lang=zh)

## 5. 本次内测支持的 Agent

`v0.1.0-beta.3` 的接入页支持以下 5 个 Coding Agent。

- **Codex**
- **Antigravity（Gemini App）**（提示：目前验证覆盖任务/CodeMode 新会话，普通闲聊尚待后续适配）
- **pi**
- **TraeX**
- **TRAE CN** 使用经过校验的 4 事件 `hooks.json` 配置（`SessionStart`、`UserPromptSubmit`、`PostToolUse`、`Stop`）。从 `beta.2` 升级时，可用**「一键修复 TRAE Hooks」**迁移旧版 6 事件配置。接入或迁移后，用 `⌘Q` 完全退出 TRAE CN 并重新打开，在 **「设置 → Hooks → 全局开关」**确认已开启。

接入前支持查看具体修改范围预览；记忆引擎（Cognee）、接入网关与 Agent 模型代理状态可在顶部菜单栏图标及**「设置 › 服务」**查看。若在**「设置 › 服务 › 版本与更新」**点击**「检查更新」**，会按需向 GitHub Releases 查询新版本（含内测预发布版本）并提供安全跳转链接。

## 6. 内测已知限制

1. **登录时静默启动限制**：因未做 Developer ID 签名与 Apple 公证，macOS `SMAppService` 的“登录时静默启动”开关可能被系统权限拒绝；拒绝时桌面端会自动回滚开关状态并提示原因，不影响手动启动与关闭窗口时后台保活。
2. **第三方反代波动**：`cognee` 与 `cognee-integrations` 源码在国内源默认经第三方公益反代拉取；如遇波动，可切换为「国外官方源」或在界面复制对应 `curl` 命令手动下载至目标路径后点击「重新检测」。
3. **不提供自动应用内更新**：内测版仅提供手动检查与跳转 GitHub Releases 下载新安装包，更新前请先彻底退出旧版 Memlane。

## 7. 安全卸载

如需卸载 Memlane，请**先完全退出 Memlane 桌面端与后台服务**（点击顶部菜单栏 Memlane 图标 →「退出 Memlane」），再按需执行以下清理步骤。

### 7.1 依赖与数据存放位置

Memlane 默认将运行依赖与本地数据库全部隔离存放在 `~/Library/Application Support/Memlane/` 中，**不会修改系统自带或 Homebrew / pyenv 中的 Python 环境**：

| 类别 | 包含内容 | 默认路径 | 卸载说明 |
| --- | --- | --- | --- |
| **桌面应用与登录项** | `Memlane.app`、macOS 登录项（`SMAppService`）与旧版 LaunchAgent | `/Applications/Memlane.app`、`~/Library/LaunchAgents/io.github.zwhu.memlane.plist`（及旧版 `com.memlane.desktop.plist`） | 传 `--remove-app` 移除 `.app`（系统会自动清理登录项），脚本同时清理 LaunchAgent |
| **记忆引擎与运行依赖** | 官方 **Cognee**（`1.6.7`）源码与运行包、**LanceDB**（`0.39.0`）、**Ladybug**（`0.19.0`，含 `kuzu` 兼容层）、`fastembed` / `onnxruntime`、本地向量模型 `BAAI/bge-small-zh-v1.5`、独立 OpenSSL（`3.5.9`）、隔离安装的 Python 3.12 / `uv` 与 Agent 插件 | `~/Library/Application Support/Memlane/components/`（`cognee-*`、`runtime-*`、`embedding-*`、`python-*`、`uv-*`、`plugin-integrations-*`、`installation/`） | 运行 `--apply` **自动全部移除** |
| **宿主 Agent 接入项** | Codex、Antigravity、pi、TraeX、TRAE IDE 中的 Memlane 插件/Hook 配置及插件运行态缓冲 | `~/.codex/`、`~/.gemini/config/`、`~/.pi/agent/extensions/memlane-cognee`、`~/.traecli/`、`~/.trae/`、`~/.trae-cn/hooks.json`、`~/.cognee-plugin/` 中 Memlane 条目 | 运行 `--apply` **仅移除 Memlane 条目并生成时间戳备份**，不删除其他插件或 MCP 配置 |
| **个人记忆库与运行配置** | **Cognee** 关系库（`cognee_db` / `cache.db`）、**LanceDB** 向量库（`cognee.lancedb`）、**Ladybug** 图数据库（`cognee_graph_kuzu`）及其隔离的 JSON 扩展（`home/.lbdb/`）、外置状态库与 `runtime.json` | `~/Library/Application Support/Memlane/library/`（或旧版 `official-store/`）及同级配置文件与日志 | 默认**保留**以便重装恢复；传 `--remove-data --confirm-remove-data` 可一并彻底删除（不可逆） |

### 7.2 使用安全卸载脚本（推荐）

卸载脚本 [`scripts/uninstall.py`](../../scripts/uninstall.py) 仅使用 macOS 自带 Python 3 标准库（零第三方依赖），内置 `.service-owner` 运行锁 / 残留进程端口检测与防符号链接逃逸保护：服务仍在运行时会自动拒绝执行。

如果你仅安装了 `.dmg`（本地未克隆仓库），可先下载该脚本到临时目录再运行（如已克隆仓库，将下方 `/tmp/memlane-uninstall.py` 替换为 `scripts/uninstall.py` 即可）：

```sh
# 获取安全卸载脚本（仅需执行一次）
curl -fsSL https://raw.githubusercontent.com/zwhu/memlane/main/scripts/uninstall.py -o /tmp/memlane-uninstall.py

# 1. 先运行 dry-run（默认模式，仅预览计划操作，不修改任何文件）
python3 /tmp/memlane-uninstall.py

# 2. 卸载应用与全部隔离依赖（移除 Memlane.app、Cognee/LanceDB/Ladybug 等运行库、向量模型、LaunchAgent 及宿主 Memlane 条目；保留个人记忆数据库）
python3 /tmp/memlane-uninstall.py --apply --remove-app

# 3. 如需彻底清除所有个人记忆库与运行配置（含 Cognee / LanceDB / Ladybug 数据库与隔离扩展，不可逆，需双重确认）
python3 /tmp/memlane-uninstall.py --apply --remove-app --remove-data --confirm-remove-data

# 完成后可清理临时脚本
rm -f /tmp/memlane-uninstall.py
```

> 若仅想重装运行依赖、暂时保留 `Memlane.app` 与记忆库，去掉 `--remove-app` 直接运行 `python3 /tmp/memlane-uninstall.py --apply` 即可。

### 7.3 可选：手动清理外部共享依赖与缓存

出于安全考虑，卸载脚本**不会删除**可能被其他开发工具共用的系统级或用户级组件。如果你仅因试用 Memlane 手动安装了以下组件并确认不再需要，可按需手动卸载：

- **手动安装的 `uv`**：若通过 Homebrew 安装可执行 `brew uninstall uv`；若通过官网脚本安装可移除 `~/.local/bin/uv`（及 `~/.local/bin/uvx`）。
- **手动安装的 Python 3.12**：若通过 `uv` 安装可执行 `uv python uninstall 3.12.12`；若通过 Homebrew 安装可执行 `brew uninstall python@3.12`。
- **用户目录残留的 Ladybug / Cognee 缓存**：Memlane 独立 `.app` 默认将 Ladybug JSON 扩展隔离在 `~/Library/Application Support/Memlane/library/home/.lbdb/`；若本机曾单独运行过 Ladybug、Cognee 或早期开发版本，确认没有其他项目依赖后，可检查并清理 `~/.lbdb` 以及已为空或不再需要的 `~/.cognee-plugin` 目录。

> 如果不想使用脚本，也可在完全退出 Memlane 后手动将 `/Applications/Memlane.app` 移入废纸篓，删除 `~/Library/Application Support/Memlane`（即会一并移除隔离安装的 Cognee、LanceDB、Ladybug 运行库、向量模型与本地记忆库），并按上表检查清理对应宿主配置文件中的 Memlane 条目。
