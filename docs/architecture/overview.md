# Memlane 系统架构总览

![Memlane 系统架构全景](../images/memlane-architecture-20261120.svg)

Memlane 是面向程序员的 macOS 记忆客户端，坚持 **Cognee 上游源码零侵入**与**单一日常存储为唯一内容真相**。系统核心三组件统一命名为 **记忆引擎（Cognee）**、**接入网关** 与 **Agent 模型代理**（唯一术语表见 [CONTEXT.md](../../CONTEXT.md)）；外置网关、WAL 账本、Agent 适配器与守护进程均位于上游引擎之外。

## 分层与源码入口

| 层级 | 核心职责 | 关键源码入口 |
| --- | --- | --- |
| **宿主 Coding Agents** | 电脑级受管接入（Codex、Antigravity / Gemini App、pi、OpenCode、TraeX、Claude Code & Cursor），各自持有独立官方子主体与 `X-Api-Key`（电脑级准备不授任何项目/通用记忆/画像权限） | [agent_connections.py](../../engine/python/agent_connections.py)、[antigravity_connection.py](../../engine/python/antigravity_connection.py)、[engine/agents/](../../engine/agents/)、[engine/patches/project_access.py](../../engine/patches/project_access.py) |
| **MyGo 原生桌面端** | `1200×820` 客户端，主界面与状态栏托盘弹层为纯 MyGo 原生自绘，仅图谱画布使用沙箱 `mygo.WebView` 加载受控内嵌官方图谱静态资产（无凭据/无直连网络）；分为总览、阅读、搜索、图谱四大控制器，通过统一 HTTP 客户端直连本地服务（不缓存正文副本） | [main.go](../../cmd/memlane-app/main.go)、[app_controllers.go](../../internal/simpledesktop/app_controllers.go)、[tray_flyout.go](../../internal/simpledesktop/tray_flyout.go)、[graph.go](../../internal/simpledesktop/graph.go)、[http_transport.go](../../internal/simpledesktop/http_transport.go) |
| **引导与进程守护** | `.service-owner` flock 单实例独占锁、`Setsid` 独立进程组、20 MiB 活 inode 日志截尾及 `.previous` 凭证脱敏与防抖健康检查；落实 M1 保活/退出策略（附着态与首次启动在途句柄受控停服，统一停止超时预算）；磁盘阈值区分安装态 `2 GiB / 5 GiB` 与开发态 `50 GiB / 55 GiB` | [quit_policy.go](../../cmd/memlane-app/quit_policy.go)、[first_runtime.go](../../internal/bootstrap/first_runtime.go)、[supervisor.go](../../internal/supervisor/supervisor.go)、[owner.go](../../internal/supervisor/owner.go)、[dev.py](../../scripts/dev.py) |
| **接入网关（控制面与外置 `/cognee/*` 网关）** | Loopback 接入治理面负责项目注册表、连接编排、采集/提炼队列与工具图谱调度、热切换、三层监控（`/v1/runtime/monitor`）及 `/v1` 游标读取与只读 SQLite 目录投影；挂载 `/cognee/*` 外置网关执行 ACL 门禁、召回合并通用记忆、Diff/日期注入与召回打点 | [project_control_server.py](../../engine/python/project_control_server.py)、[cognee_extension_gateway.py](../../engine/python/cognee_extension_gateway.py)、[service_monitor.py](../../engine/python/service_monitor.py)、[读取路由](../../engine/python/v2_api.py)、[目录投影](../../engine/python/catalog_index.py)、[会话历史读取](../../engine/python/raw_records_read.py) |
| **记忆引擎（Cognee）与外置中间件** | 启动前经 `verify_source` v2 缓存（逐文件 `O_NOFOLLOW` SHA-256 + 源码目录禁止 `.pyc`/`.pyo`，开启 `sys.dont_write_bytecode`）校验固定上游源码（`1.6.7` / `ec6f408`）；外置路由与中间件承担采集回执、最早来源日期锁（纯只读 GET 补全）、并发闸门、语言注入与运行期热切换 | [official_service_dev.py](../../engine/python/official_service_dev.py)、[external_capture_store.py](../../engine/python/external_capture_store.py)、[provenance_store.py](../../engine/python/provenance_store.py)、[language_egress.py](../../engine/python/language_egress.py) |
| **双通道 LLM 提炼推理** | 支持「Agent 模型代理（`agent_bridge`：Codex / Antigravity `agy` / pi / TraeX）」与「直连 LLM Provider / Ollama」并存配置，启动与热切换统一经 `require_bridge_startup` / `connected_agent_selections` 校验，运行期由唯一 `active` 决定执行者 | [agent_completion_endpoint.py](../../engine/python/agent_completion_endpoint.py)、[agent_bridge_runtime.py](../../engine/python/agent_bridge_runtime.py)、[generation_config.py](../../engine/python/generation_config.py)、[generation_handoff_runtime.py](../../engine/python/generation_handoff_runtime.py) |
| **存储层** | `.local/daily/official-store`（SQLite + LanceDB + Ladybug）保存全部正文、向量与图谱唯一真相；私有 `0600` 外置 SQLite WAL 与 JSON 保存回执、日期索引、漂移记录、召回计数、画像与工具图谱作业及策略状态 | `capture-receipts.sqlite3`、`lesson-provenance.sqlite3`、`session-drift.sqlite3`（接入网关单点写入）、`knowledge-recall.sqlite3`（`0600` + WAL `4 MiB` 限额）、`project-registry.json`、`user-profiles.json`、`tool-graph-*.json` |

## 核心数据流

1. **采集与召回流**：宿主 Agent 携带独立 `X-Api-Key` 请求 `/cognee/*` → 外置网关校验主体、项目 ACL 与采集开关（`capture=False` 服务端返回 `403 capture_paused`）后透传记忆引擎（Cognee）→ 读写 `official-store`；召回时网关透明合并可读通用记忆、单点写入 `session-drift.sqlite3` 并在 `knowledge-recall.sqlite3` 记录命中打点。
2. **管理、策略与只读投影流**：桌面端与宿主（`/v1/project/*`、`/v1/runtime/monitor`）请求接入网关 `project_control_server.py` → 更新项目策略、连接状态、采集/提炼队列或触发运行期热切换，并将策略与回执写入私有 WAL/JSON 元数据；首页、原始记录目录和消息汇总统一通过 `/v1` 读取目录投影，列表使用限量游标分页和响应字节上限。桌面端通过 `/v1/capabilities` 核对 `catalog-cursor-v1` 读取契约，旧后台需重启。同一读取入口遇到权限拒绝或索引重建时直接报告状态。正文和会话详情保留受授权的官方来源读取。
3. **只读正文与沙箱图谱流**：桌面端直接从记忆引擎（Cognee）读取正文、会话原文与图谱数据（图谱画布运行在无凭据/无网络的沙箱 `mygo.WebView` 中，由宿主经单调 `epoch` 注入已鉴权 payload）；撤权或身份失效时即时清空界面内存正文、画像缓存、图谱画布与复制入口。
4. **LLM 提炼出站流**：记忆引擎（Cognee）的提炼请求经过 `generation_http.py` 并发控制与 `language_egress.py` 语言注入后，路由至当前 `active` 指定的 Agent 模型代理（`agent_bridge`）或直连 Provider / Ollama。
5. **进程守护与退出流**：`internal/supervisor`（及开发入口 `scripts/dev.py`）统一拉起与清理接入网关、记忆引擎（Cognee）及 Agent 模型代理进程组；遵循 M1 保活/退出策略（默认退出界面保留后台，托盘「彻底停止并退出」或关闭保活后按统一超时预算停服，含附着态与首次启动在途句柄），崩溃不静默自启。

## 延伸阅读

- [本地开发与配置说明](development.md)
- [架构四阶段改造记录（2026-11-20）](architecture-refactor-20261120.md)
- [电脑级配置与 Agent 接入契约](agent-connection-contract.md)
- [Agent 有效模型读取与调用契约](agent-effective-model-contract-20261118.md)
- [采集与提炼队列异常范围与隔离契约](queue-isolation-20261119.md)
