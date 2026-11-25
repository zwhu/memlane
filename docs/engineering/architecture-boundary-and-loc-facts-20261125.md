# Memlane 架构边界与代码行数现状事实记录（2026-11-25）

> **文档性质**：本文仅记录截至指定 Git 基线的客观测量数据与现行结构事实，**不包含根因分析、重构结论或目标方案**，供后续讨论与决策作为事实基线使用。

---

## 1. 快照基线与统计口径

| 项目 | 记录值 |
| --- | --- |
| **Git 基线 Commit** | `b1a82e0`（`b1a82e0175a47a0f0391f19edaf5649db3fc90b8`） |
| **Commit 时间** | `2026-11-25 22:07:41 +0800` |
| **统计范围** | `git ls-tree -r b1a82e0` 全部已入库非二进制文本文件（自动排除 `.local/`、`build/`、被 `.gitignore` 忽略目录及 PNG/ICO/字体等二进制文件） |
| **行数口径** | 物理行数（含注释与空行，按 `\n` 计数） |
| **角色归类口径** | `prod`：运行态生产源码（含 `web/official-graph` 前端代码）；`scripts`：`scripts/` 下非测试文件；`test`：`*_test.go`、`test_*.py`、`*.test.*` 及 `testdata/`、`fixtures/`；`config_lock`：`.lock`/`.json`/`.yaml`/`.mod`/`.sum`/`.patch` 等配置与锁文件；`prototype`：`docs/design/*prototype*`；`docs`：其余 `docs/` 文档；`tooling`：`.githooks/`、`.github/`、`.agent/`、`.agents/`；`website`：`web/website/` |

---

## 2. 全仓代码规模与语言分布

### 2.1 按角色汇总

全仓共 **823 个受管文本文件，总计 250,416 行**。其中生产源码（`prod`）与工程脚手架（`scripts`）合计 **383 个文件、122,482 行**（纯代码文件 382 个、122,449 行，另含 1 个 33 行 JSON 补丁配置）；若计入测试代码（`test`），纯开发相关文件合计 **651 个、207,118 行**。

| 角色 | 文件数 | 行数 | 占全仓文本比 |
| --- | ---: | ---: | ---: |
| `prod`（运行态生产代码） | 355 | 107,515 | 42.9% |
| `test`（测试与测试夹具） | 268 | 84,636 | 33.8% |
| `config_lock`（锁文件与配置资产） | 92 | 24,612 | 9.8% |
| `scripts`（工程门禁与运维脚手架） | 28 | 14,967 | 6.0% |
| `prototype`（HTML/JS/CSS 交互原型） | 24 | 11,939 | 4.8% |
| `docs`（正式与归档文档） | 38 | 5,881 | 2.3% |
| `tooling`（Hooks / CI / Agent 规范） | 13 | 583 | 0.2% |
| `website`（发布站点静态页） | 5 | 283 | 0.1% |
| **合计** | **823** | **250,416** | **100.0%** |

### 2.2 按语言汇总

| 语言 / 文件类型 | 文件数 | 总行数 | `prod` | `test` | `scripts` | `prototype` | `docs` | `config_lock` / 其他 |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| **Go** | 342 | 101,266 | 58,066 | 43,200 | 0 | 0 | 0 | 0 |
| **Python** | 229 | 96,187 | 41,945 | 39,401 | 14,841 | 0 | 0 | 0 |
| **TypeScript (`.ts`/`.tsx`)** | 58 | 7,469 | 7,469 | 0 | 0 | 0 | 0 | 0 |
| **JavaScript (`.js`/`.mjs`)** | 19 | 10,026 | 18 | 1,801 | 0 | 7,967 | 0 | 240 |
| **CSS** | 13 | 3,946 | 13 | 0 | 0 | 3,793 | 0 | 140 |
| **HTML** | 7 | 1,505 | 4 | 0 | 0 | 107 | 1,308 | 86 |
| **Shell** | 10 | 299 | 0 | 0 | 66 | 0 | 0 | 233 |
| **Swift** | 1 | 27 | 0 | 0 | 27 | 0 | 0 | 0 |
| **Lockfile (`.lock`)** | 2 | 15,845 | 0 | 0 | 0 | 0 | 0 | 15,845 |
| **Markdown** | 54 | 4,810 | 0 | 0 | 0 | 72 | 3,960 | 778 |
| **JSON** | 35 | 4,071 | 0 | 234 | 33 | 0 | 0 | 3,804 |
| **YAML** | 5 | 2,032 | 0 | 0 | 0 | 0 | 0 | 2,032 |
| **Text / SVG / 其余配置** | 48 | 2,933 | 0 | 0 | 0 | 0 | 613 | 2,320 |

- 在 **生产 + 脚手架纯代码（122,449 行）** 中：
  - **Go**：58,066 行（**47.4%**）
  - **Python**：56,786 行（**46.4%**，其中 `prod` 41,945 行 + `scripts` 14,841 行）
  - **TypeScript**：7,469 行（**6.1%**，其中 `web/official-graph/` 5,587 行 + `engine/agents/` 与 `engine/prompts/` 1,882 行）
  - **其余（Shell / Swift / JS / CSS / HTML）**：128 行（**0.1%**）

---

## 3. 历史增长时间线（2026-11-07 至 2026-11-25）

仓库自首次提交 `3d859ac`（`2026-11-07`）至 `b1a82e0`（`2026-11-25`）共计 **19 个活跃日、369 个提交**。按当日末次提交通过 `git cat-file` 实测（仅统计 `.go` / `.py` / `.ts` / `.tsx`，排除 `docs/`）：

| 日期 | Commit | Go 生产 | Go 测试 | Python 生产 | Python 脚手架 (`scripts/`) | Python 测试 | TS 生产 | 生产 + 脚手架合计 |
| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 2026-11-07 | `917f386` | 8,788 | 76 | 351 | 5,612 | 0 | 61 | **14,812** |
| 2026-11-10 | `ed11c15` | 46,771 | 16,784 | 680 | 19,170 | 0 | 109 | **66,730** |
| 2026-11-13 | `aab907d` | 6,113 | 2,411 | 2,667 | 978 | 1,887 | 5,587 | **15,345** |
| 2026-11-16 | `37cfdd6` | 14,630 | 8,358 | 10,024 | 1,393 | 7,972 | 6,518 | **32,565** |
| 2026-11-19 | `3d5df96` | 23,160 | 12,825 | 14,230 | 2,011 | 12,896 | 6,561 | **45,962** |
| 2026-11-22 | `79b1889` | 47,383 | 29,129 | 37,837 | 8,214 | 29,961 | 7,286 | **100,720** |
| 2026-11-25 | `b1a82e0` | 58,066 | 43,200 | 42,115 | 14,841 | 39,231 | 7,469 | **122,491** |

*(注：本表 Python 生产列按文件名后缀直接统计，未单独剔除极少数 `fixtures/` 下 `.py` 文件，故与 §2.2 角色口径相差约 170 行。)*

---

## 4. 模块体量、目录层级与大文件分布

### 4.1 主要目录代码体量

| 目录 | 文本文件数 | 总行数 | `prod` | `scripts` | `test` | 配置 / 原型 / 文档 |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| `engine/python/` | 194 | 78,430 | 41,655 | 0 | 32,578 | 4,197 |
| `internal/simpledesktop/` | 274 | 71,089 | 41,404 | 0 | 29,549 | 136 |
| `scripts/` | 47 | 21,902 | 0 | 14,967 | 6,935 | 0 |
| `internal/bootstrap/` | 46 | 15,296 | 8,228 | 0 | 7,068 | 0 |
| `docs/design/` | 26 | 12,381 | 0 | 0 | 0 | 12,381 |
| `web/official-graph/` | 67 | 12,212 | 5,622 | 0 | 0 | 6,590 |
| `deps/` | 10 | 11,951 | 0 | 0 | 0 | 11,951 |
| `internal/setupcli/` | 14 | 6,307 | 3,035 | 0 | 3,272 | 0 |
| `internal/supervisor/` | 11 | 5,729 | 3,540 | 0 | 2,189 | 0 |
| `engine/agents/opencode/` | 15 | 3,432 | 1,711 | 0 | 1,721 | 0 |
| `internal/controlplane/` | 10 | 1,764 | 1,093 | 0 | 671 | 0 |
| `cmd/memlane-app/` | 9 | 1,074 | 624 | 0 | 450 | 0 |
| `engine/patches/` | 2 | 304 | 226 | 0 | 0 | 78 |
| `engine/agents/pi/` | 3 | 149 | 128 | 0 | 0 | 21 |
| `cmd/memlane-bootstrap/` | 1 | 115 | 115 | 0 | 0 | 0 |

### 4.2 核心目录扁平度实测

| 目录 | 入库总文件数（含二进制） | 顶层直接文件数 | 子目录内文件数 | 现有子目录 |
| --- | ---: | ---: | ---: | --- |
| `internal/simpledesktop/` | 280 | **248** | 32 | `agent_icons`、`brand_icons`、`dependency_icons`、`testdata`（全部 Go 代码均在同一 package `simpledesktop` 内：129 生产 + 118 测试） |
| `engine/python/` | 194 | **174** | 20 | `user_profiles`、`fixtures`、`testdata` |
| `internal/bootstrap/` | 46 | **46** | 0 | 无 |
| `scripts/` | 47 | **46** | 1 | `patches` |

### 4.3 生产代码文件体量分档与 ≥1,000 行清单

355 个运行态生产代码文件的行数分档：
- **≥ 1,000 行**：16 个
- **500 – 999 行**：46 个
- **300 – 499 行**：60 个
- **100 – 299 行**：125 个
- **< 100 行**：108 个

全仓所有 **≥ 1,000 行** 的生产与脚手架代码文件（共 23 个）：

| 行数 | 语言 | 角色 | 文件路径 |
| ---: | --- | --- | --- |
| 3,822 | Python | prod | `engine/python/catalog_index.py` |
| 3,072 | Python | scripts | `scripts/lint.py` |
| 1,901 | Go | prod | `internal/simpledesktop/overview_ui.go` |
| 1,789 | Python | prod | `engine/python/catalog_sync_stages.py` |
| 1,726 | Go | prod | `internal/bootstrap/first_runtime.go` |
| 1,699 | Python | scripts | `scripts/check.py` |
| 1,559 | Python | prod | `engine/python/project_backlog.py` |
| 1,558 | Python | prod | `engine/python/agent_connections.py` |
| 1,463 | Python | prod | `engine/python/reading_state_store.py` |
| 1,409 | Python | scripts | `scripts/bench_scaling.py` |
| 1,376 | Python | scripts | `scripts/verify_setup_cli.py` |
| 1,333 | Go | prod | `internal/supervisor/owner.go` |
| 1,317 | Python | scripts | `scripts/smoke_release_contracts.py` |
| 1,284 | Go | prod | `internal/simpledesktop/knowledge_unread.go` |
| 1,226 | Go | prod | `internal/simpledesktop/app.go` |
| 1,220 | Python | prod | `engine/python/project_control.py` |
| 1,204 | Go | prod | `internal/simpledesktop/integrations.go` |
| 1,163 | Python | scripts | `scripts/dev.py` |
| 1,085 | Python | prod | `engine/python/provenance_store.py` |
| 1,079 | Python | prod | `engine/python/cognee_extension_gateway.py` |
| 1,074 | Python | scripts | `scripts/uninstall.py` |
| 1,034 | Go | prod | `internal/simpledesktop/graph.go` |
| 1,003 | Go | prod | `internal/simpledesktop/project_queue_ui.go` |

---

## 5. 现行架构边界事实

### 5.1 进程、入口与实现语言映射

依据 [docs/architecture/overview.md](../architecture/overview.md) 与当前源码入口，运行态组件与语言对应关系如下：

| 组件 / 层级 | 实现语言 | 主要入口 | 承载职责（现状） |
| --- | --- | --- | --- |
| **原生桌面端** | Go（MyGo）+ TS（图谱画布） | `cmd/memlane-app/`、`internal/simpledesktop/`、`web/official-graph/` | 主界面、托盘弹层、总览/阅读/搜索/图谱控制器、项目采集队列 UI、HTTP 客户端 |
| **安装引导 / CLI / 进程守护** | Go + Python（双入口并存） | `internal/bootstrap/`、`internal/supervisor/`、`internal/setupcli/`、`scripts/dev.py`、`scripts/uninstall.py` | 首次安装环境自举、Setup CLI、`.service-owner` flock 守护与进程组启停、开发态启停 |
| **接入网关（控制面 + `/cognee/*` 网关）** | Python | `engine/python/project_control_server.py`、`cognee_extension_gateway.py`、`v2_api.py`、`catalog_index.py` | 项目注册与 ACL、连接治理、采集/提炼队列、三层监控、`/v1` 目录游标投影、`/cognee/*` 网关门禁与召回合并 |
| **记忆引擎（Cognee）** | Python | `engine/python/official_service_dev.py` + 上游 `cognee 1.6.7` | 源码 SHA-256 校验、Cognee 主服务、外置中间件与热切换 |
| **Agent 模型代理** | Python | `engine/python/agent_completion_endpoint.py`、`codex_completion_endpoint.py`、`agent_bridge_runtime.py` | 本地 HTTP 模型代理，转发提炼请求至本机 Agent CLI |
| **宿主接入插件与 Hook** | Python + TS + JSON | `engine/python/*hook*.py`、`engine/agents/{opencode,pi}/`、`engine/patches/project_access.py`、`engine/managed-project-plugin*.json` | 宿主会话事件监听、本地 outbox/backlog、策略缓存、历史插件兼容 |

### 5.2 Python 生产代码外部依赖（`import`）分级实测

统计全部 **105 个 Python 生产文件（41,945 行）** 的 `import` 语句：

| 依赖层级 | 定义 | 文件数 | 行数 | 占 Python 生产代码比 |
| --- | --- | ---: | ---: | ---: |
| **Tier 1 · 引擎/模型库直接依赖** | 直接 `import` `cognee` / `litellm` / `ladybug` / `lancedb` / `kuzu` | **11** | **3,074** | **7.3%** |
| **Tier 2 · Web/ORM 框架依赖** | 无 Tier 1，但 `import` `fastapi` / `starlette` / `uvicorn` / `pydantic` / `sqlalchemy` | **1** | **1,085** | **2.6%** |
| **Tier 3 · 仅 HTTP 客户端/服务库** | 无 Tier 1/2，仅 `import` `aiohttp` / `httpx` / `requests` | **22** | **11,721** | **27.9%** |
| **Tier 4 · 纯标准库与仓内模块** | 无上述第三方依赖，仅用 Python 标准库或仓内 `.py` | **71** | **26,065** | **62.1%** |
| **合计** | 全部 Python 生产文件 | **105** | **41,945** | **100.0%** |

Tier 1 与 Tier 2 的 12 个文件逐一明细：

| 层级 | 行数 | 文件路径 | 实际 import 的外部库 |
| --- | ---: | --- | --- |
| Tier 1 | 569 | `engine/python/tool_graph_service.py` | `cognee`, `fastapi`, `pydantic` |
| Tier 1 | 485 | `engine/python/official_service_dev.py` | `cognee`, `litellm`, `uvicorn` |
| Tier 1 | 438 | `engine/python/generation_handoff_runtime.py` | `cognee`, `litellm`, `fastapi`, `pydantic`, `sqlalchemy`, `starlette`, `aiohttp` |
| Tier 1 | 396 | `engine/python/generation_config.py` | `cognee` |
| Tier 1 | 315 | `engine/python/generation_probe.py` | `cognee`, `litellm`, `pydantic` |
| Tier 1 | 248 | `engine/python/capture_receipts.py` | `cognee`, `fastapi`, `pydantic`, `sqlalchemy` |
| Tier 1 | 225 | `engine/python/first_install_store.py` | `cognee` |
| Tier 1 | 148 | `engine/python/session_history.py` | `cognee`, `fastapi` |
| Tier 1 | 90 | `engine/python/generation_ollama.py` | `litellm` |
| Tier 1 | 87 | `engine/python/generation_http.py` | `litellm`, `httpx` |
| Tier 1 | 73 | `engine/python/ladybug_extension.py` | `ladybug` |
| Tier 2 | 1,085 | `engine/python/provenance_store.py` | `fastapi` |

### 5.3 两大核心目录的内部职能体量

**`engine/python/` 非测试 `.py`（103 文件 / 41,655 行）**：

| 职能簇 | 文件数 | 行数 |
| --- | ---: | ---: |
| 项目治理 / Agent 接入 / 采集与队列 | 32 | 14,263 |
| 目录投影 / 只读 API / 会话读取 | 15 | 13,453 |
| Cognee 外置网关 / 外置存储 / 图谱扩展 | 12 | 5,824 |
| 监控 / 增量处理 / 首次安装与公共基础 | 18 | 3,868 |
| LLM 提炼配置与 Agent 模型代理 | 13 | 3,181 |
| 用户画像 (`user_profiles/`) | 13 | 1,066 |

**`internal/simpledesktop/` 生产 Go（129 文件 / 41,404 行）**：

| 职能簇 | 文件数 | 行数 |
| --- | ---: | ---: |
| 详情 / 阅读 / 会话 / 记忆展示 | 21 | 9,500 |
| 设置 / Agent 接入 / 模型 / 引导向导 | 28 | 8,206 |
| 项目管理 / 采集队列 / 权限 UI | 24 | 6,811 |
| 主应用框架与其他 (`app.go` 等) | 18 | 4,443 |
| 首页 / 总览 | 10 | 4,305 |
| HTTP 客户端 / API 适配 / 状态同步 | 5 | 2,261 |
| 托盘 / 状态监控 / 通知 / 生命周期 | 6 | 1,704 |
| 全局搜索 / 筛选 | 4 | 1,553 |
| 图谱画布 / WebView | 5 | 1,486 |
| 自绘组件 / 主题 / 动效 / 图标 | 8 | 1,135 |

### 5.4 存储落盘、宿主接入与历史快照事实

- **持久化文件现状**：
  - 主真相库：`.local/daily/official-store`（Cognee SQLite + LanceDB + Ladybug）
  - 外置私有 SQLite WAL（`0600`）：`capture-receipts.sqlite3`、`lesson-provenance.sqlite3`、`session-drift.sqlite3`、`knowledge-recall.sqlite3`（另含只读目录索引与阅读状态存储）
  - 外置 JSON 元数据：`project-registry.json`、`user-profiles.json`、`tool-graph-*.json` 及运行态配置文件
- **宿主覆盖现状**：代码同时面向 8 个宿主入口——Codex、Antigravity / Gemini App、pi、TRAE CLI / TraeX、TRAE CN（IDE），以及保留兼容的 OpenCode、Claude Code、Cursor。
- **插件清单历史快照**：`engine/` 目录下当前并存 **7 个** `managed-project-plugin*.json` 文件：
  - `engine/managed-project-plugin.json`
  - `engine/managed-project-plugin-pre-convergence.json`
  - `engine/managed-project-plugin-pre-cursor-policy.json`
  - `engine/managed-project-plugin-pre-empty-common.json`
  - `engine/managed-project-plugin-pre-environment.json`
  - `engine/managed-project-plugin-pre-plugin-extract.json`
  - `engine/managed-project-plugin-pre-recall.json`

### 5.5 工程脚手架与门禁清单（`scripts/` 非测试文件）

`scripts/` 下共 **28 个非测试文件、14,967 行**（另含 19 个测试文件、6,935 行），其中 ≥ 200 行的 13 个文件如下：

| 行数 | 文件路径 |
| ---: | --- |
| 3,072 | `scripts/lint.py` |
| 1,699 | `scripts/check.py` |
| 1,409 | `scripts/bench_scaling.py` |
| 1,376 | `scripts/verify_setup_cli.py` |
| 1,317 | `scripts/smoke_release_contracts.py` |
| 1,163 | `scripts/dev.py` |
| 1,074 | `scripts/uninstall.py` |
| 961 | `scripts/utm_guard.py` |
| 790 | `scripts/diff_guard.py` |
| 545 | `scripts/build_release.py` |
| 268 | `scripts/disk-space.py` |
| 217 | `scripts/local_prepare.py` |
| 200 | `scripts/utm_isolated_env.py` |

*(其余 15 个非测试脚本文件合计 876 行。)*

---

## 6. 关联记录与后续边界说明

- 本文仅记录截至 `b1a82e0` 的客观事实基线，**不构成重构方案或架构定论**。
- 与本基线同日落地的增量硬门禁（`diff_guard.py` 与 `DG005` 冻结新增 `.py`）记录在 [architecture-governance-and-py-freeze.md](architecture-governance-and-py-freeze.md)。
