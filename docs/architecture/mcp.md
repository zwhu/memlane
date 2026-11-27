# Memlane MCP 架构与工具契约

- **状态**：现行生效架构文档（对齐 2026-11-28 主线实现）
- **定位**：说明 MCP（Model Context Protocol）在 Memlane 方案 C 架构中的职责边界、两段式传输形态、标准工具契约、执行链路与安全约束。
- **关联文档**：
  - 系统架构全景与源码映射：[overview.md](overview.md)
  - 现行硬性架构边界契约：[boundaries.md](boundaries.md)
  - 产品定义与权限边界：[product.md](../product/product.md)
  - 领域唯一术语表：[CONTEXT.md](../../CONTEXT.md)

---

## 1. 在整体架构中的定位

在 Memlane 的**方案 C 宿主接入架构**（见 [boundaries.md](boundaries.md#1-五项核心边界决策)）中，宿主侧接入按触发方式拆分为两条互补通道，**MCP 承担「主动记忆工具通道」与「长尾宿主标准接入层」的双重角色**：

| 通道 | 触发方式 | 核心职责 | 入口与链路 |
| --- | --- | --- | --- |
| **Hook / 扩展生命周期通道** | 被动、自动（随每轮对话、工具执行与会话结束触发） | 每轮自动召回并注入上下文、自动采集问答与工具轨迹、会话结束结算 | `memlane hook`（或 `pi` 扩展 [index.ts](../../engine/agents/pi/index.ts)）→ 接入网关 `POST /recall`、`/ingest`、`/session-end` |
| **MCP 标准工具通道** | 主动、显式（由 Agent 或用户显式发起工具调用） | 显式写入记忆（`remember`）、查询异步保存状态（`remember_status`）、显式检索（`recall`）、探测健康与授权范围（`health`） | `memlane mcp` stdio 桥接（或 `pi` 扩展 [tools.ts](../../engine/agents/pi/tools.ts) / 长尾宿主直连）→ 接入网关 `GET/POST /mcp` |

- **对核心宿主（Codex、Antigravity、TraeX、TRAE CN IDE、`pi`）**：MCP 与 Hook 互补共存——Hook 负责无感的自动采集与上下文注入，MCP 负责按需显式保存、状态查询、定向检索与连接诊断。
- **对长尾宿主（标准 MCP 客户端）**：无需开发或分发宿主专属插件，直接对接接入网关原生暴露的标准 MCP 接口（协议版本 `2025-03-26`，兼容 `2024-11-05`）即可获得受同一套主体鉴权与 ACL 约束的记忆能力。

### 各宿主当前 MCP 接入形态

以 [core/hooks/install.go](../../core/hooks/install.go) 与 [engine/agents/pi/tools.ts](../../engine/agents/pi/tools.ts) 现行实现为准：

| 宿主 | 配置文件 / 载体 | 接入形态 |
| --- | --- | --- |
| **Codex** | `<profile>/config.toml`（`[mcp_servers.memlane]`） | 受管 stdio MCP，启动 `memlane mcp --gateway-url ... --key-file ...`（带 `_memlane_managed` 标记） |
| **TraeX** | `<profile>/traecli.toml`（`[mcp_servers.memlane]`） | 同上 |
| **Antigravity** | `<profile>/mcp_config.json`（`mcpServers.memlane`） | 同上 |
| **TRAE CN IDE** | `~/Library/Application Support/Trae CN/User/mcp.json`（`mcpServers.memlane`） | 同上（由 `DefaultHostMCPConfigPath` 解析外部配置路径） |
| **pi** | 极薄扩展 [engine/agents/pi/tools.ts](../../engine/agents/pi/tools.ts) | 注册 `cognee_health` / `cognee_remember` / `cognee_recall` 三个工具，直接转发 JSON-RPC 请求至网关 `POST /mcp`（并在客户端按 `[cwd, data]` 生成确定性幂等键） |
| **长尾标准 MCP 宿主** | 宿主自有 MCP 配置 | 通过 `memlane mcp` stdio 桥接或直接请求 Loopback `GET/POST /mcp`（携 `Authorization: Bearer` 或 `X-Api-Key`） |

---

## 2. 两段式运行架构

MCP 链路分为「宿主侧轻量 stdio 桥接」与「接入网关内嵌 MCP Server」两段，所有治理状态与业务裁决全部收口在 Go 接入网关一处：

```text
[宿主 Coding Agent]
   │  stdio JSON-RPC 2.0（或直连 HTTP）
   ▼
[cmd/memlane mcp]（轻量无状态 stdio 桥接）
   ├─ 若 MEMLANE_PROCESSING_SESSION=1 → 本地熔断短路（返回空工具表 / 拒绝调用，防提炼递归）
   └─ 正常会话 → 读取 0600 独立私钥文件，附加 X-Api-Key 转发 Loopback POST /mcp
   ▼
[接入网关 core/gateway/server.go · /mcp]
   │
   ├─ 统一主体鉴权：core/gateway/agentauth（与 /ingest /recall 共用 Authenticator）
   ├─ 协议分发层：core/gateway/mcp（JSON-RPC 2.0 单条/批量，GET 能力探针）
   │    ├─ remember / remember_status ──▶ core/gateway/mcp_remember.go（3s 快路径 + capture_inbox 异步回退）
   │    ├─ recall / health            ──▶ core/gateway/recall（多范围并发故障隔离 + 配额合并 + 授权过滤）
   │    └─ 单点状态与 ACL             ──▶ core/domain（memlane.sqlite3）
   ▼
[引擎无关接口 core/engine.MemoryEngine（6 原语）]
   ▼
[记忆引擎（Cognee）core/engine/cognee → engines/cognee]
```

### 2.1 客户端侧：`memlane mcp` stdio 桥接（[cmd/memlane/mcp.go](../../cmd/memlane/mcp.go)）

- **为何统一采用 stdio 桥接**：各家宿主对 HTTP MCP 的鉴权配置字段不统一，且普遍不支持直接读取本地密钥文件（如 `bearer_token_file`）。通过单一 Go 二进制子命令 `memlane mcp --gateway-url <loopback> --key-file <abs-path>`（亦支持 `--config` + `--host` 回退解析），宿主只需配置标准 stdio 命令即可安全复用本机 `0600` 独立 Agent 私钥，无须将明文令牌写入宿主配置文件。
- **传输与资源边界**：强制校验 Loopback 网关地址、禁用 HTTP 重定向、默认单请求超时 30s（可通过 `--timeout` 覆盖）、单帧输入上限 `1 MiB + 4 KiB`、单响应上限 `8 MiB`、最大 8 路并发；`validMCPID` 与网关侧 JSON-RPC ID 校验规则保持一致。
- **防递归熔断**：当环境变量 `MEMLANE_PROCESSING_SESSION=1`（表示当前进程是 Memlane 通过 Agent 模型代理拉起的后台记忆提炼会话）时，桥接器不连接接入网关，在本地直接响应 `initialize` / `ping` / `notifications/initialized`，对 `tools/list` 返回空工具列表，对 `tools/call` 返回工具已禁用提示，从协议入口阻断提炼任务递归回调 Memlane。

### 2.2 服务端侧：接入网关 `/mcp` 端点（[core/gateway/mcp/](../../core/gateway/mcp/)）

- **装配与挂载**：由 [cmd/memlane/daemon.go](../../cmd/memlane/daemon.go) 的 `MountGatewayStack` 创建并挂载至 [core/gateway/server.go](../../core/gateway/server.go) 的 `/mcp` 路由，运行期注入网关共享的 `agentauth.Authenticator`、`MCPRememberService` 与 `RegistryLocker`。
- **HTTP 与 JSON-RPC 语义**：
  - `GET /mcp`：鉴权后调用 `MemoryEngine.Health`，返回 `protocolVersion`、`serverInfo`、`capabilities`、`engine`、`agent_id`、`ok`。
  - `POST /mcp`：单请求体上限 `1 MiB`，支持 JSON-RPC 2.0 单条及数组批量请求（支持 `initialize`、`ping`、`notifications/initialized`、`tools/list`、`tools/call`）；无 `id` 的通知消息返回 `202 Accepted`。

---

## 3. 标准工具契约与执行链路

接入网关 `/mcp` 当前向宿主暴露 **4 个工具**（定义见 [core/gateway/mcp/tools.go](../../core/gateway/mcp/tools.go) 与 [core/gateway/mcp/recall.go](../../core/gateway/mcp/recall.go)）。

> **删除权限收口边界**：宿主侧**不提供任何删除工具**（`maintain` 权限仅含 `read/write`，不授 `delete` 与 `share`）。项目记忆与通用记忆的主动删除统一收口在 Memlane 桌面客户端经用户二次确认执行（见 [product.md](../product/product.md#4-自动采集记忆提炼与权限原则)）。

| 工具 | 定位 | 必填参数 | 所需权限 | 关键输出 |
| --- | --- | --- | --- | --- |
| `remember` | 显式保存内容到指定授权项目 | `content` +（`project_id` 或 `project_path`） | 项目 `maintain`（仅当 `auto_capture=true` 时额外校验自动采集开关） | `status`（`saved` / `queued`）、`saved`、`task_id`、`entry_id`、`document_id`、`idempotency_key`、`deduplicated` |
| `remember_status` | 查询之前提交的 `remember` 任务进度 | `task_id` | 任务所属 Agent 本人 + 项目 `read`/`maintain` | `status`（`pending`/`processing`/`succeeded`/`failed`/`cancelled_project_removed`）、`saved`、`entry_id`、`document_id`、`last_error`（脱敏） |
| `recall` | 主动按自然语言检索授权记忆与图谱 | `query`（可选 `project_id`/`project_path`、`kind`、`mode`、`limit`） | 项目与/或通用记忆 `read`/`maintain` | `hits`、`graph_context`、`mode`、`degraded_to_chunks`、`partial`、`scope_errors` |
| `health` | 查看记忆引擎状态与当前主体授权范围 | 无（`{}`） | 已注册且未退役的 Agent 身份 | `engine`（不含本机 `StorePath`）、`scopes`（`project`/`code`/`common` 列表） |

### 3.1 `remember`：确定性幂等 + 3 秒快路径 + 收件箱异步回退

完整实现位于 [core/gateway/mcp/tools.go](../../core/gateway/mcp/tools.go) 与 [core/gateway/mcp_remember.go](../../core/gateway/mcp_remember.go)：

1. **输入与确定性幂等键**：支持 `kind` 为 `document`（默认）、`capture_qa` 或 `capture_trace`，以及可选 `session_id`、`message_id`、`idempotency_key`、`context_date`、`metadata`。若调用方未显式传入 `idempotency_key`，当 `session_id` 与 `message_id` 齐全时按 `(receiptUser, datasetID, sessionID, messageID, kind)` 计算 `mcp:<sha256>`；否则对完整规范化内容与元数据计算确定性 `mcp:<sha256>`，确保客户端重试不产生重复入库。
2. **单事务入账与去重**：持有 `RegistryLocker` 二次复核项目与 `maintain` 权限后，在 `memlane.sqlite3` 单事务内核对 `capture_receipts` 与 `capture_inbox`（生成确定性 `task_id`）：
   - 同幂等键但载荷或归属不一致 → 返回冲突错误（`ErrConflict`）。
   - 同幂等键且已持久化成功 → 直接返回 `status: "saved", saved: true, deduplicated: true`。
   - 同幂等键仍在排队/处理（或之前失败/因项目移除取消后重排）→ 返回 `status: "queued", saved: false, deduplicated: true` 并唤醒 `InboxWorker`。
3. **3 秒快路径（Fast-Path）**：当当前无在途模型热切换且存在有效 `active` 模型摘要时，尝试在 3 秒预算内（且为上游 context 预留 500ms 余量）连续完成：
   - `MemoryEngine.Ingest` 写入原始记录 → 标记 `capture_receipts` 已保存；
   - `MemoryEngine.Extract` 触发记忆提炼（`capture_trace` 除外）；
   - 按目标 `dataset_id` 增量发布目录投影（`SyncDatasetCatalog` / `SyncDatasetSessions`）；
   - 标记 `capture_inbox` 为 `succeeded`，向调用方返回 `status: "saved", saved: true`。
4. **分层回退（零丢单、零重复调模型）**：
   - **明确 4xx 客户端拒绝**（参数非法、非 UTF-8、内容超限、权限拒绝等，不含 408/409/429）：立刻回滚 `capture_inbox` 与 `pending` 回执并返回错误，不入重试队列。
   - **无有效模型摘要、正处模型切换、快路径超时或引擎暂时不可用**：将 `capture_inbox` 置回 `pending` 并唤醒 `InboxWorker` 后台异步续跑，向调用方返回 `status: "queued", saved: false, task_id, message: "已接收，等待保存"`，宿主可按 `task_id` 轮询 `remember_status`。
   - **仅目录投影发布超时或失败**：因 `Ingest` + `Extract` 已经完成，原位标记收件箱完成并登记独立 `catalog_publish_state`（返回 `status: "queued", saved: false, message: "记忆已生成，目录待更新"`），后台重试仅重跑目录发布，**不再重复调用大模型**。

### 3.2 `recall`：共享召回引擎、故障隔离与项目优先配额

完整实现位于 [core/gateway/mcp/recall.go](../../core/gateway/mcp/recall.go) 与 [core/gateway/recall/recall.go](../../core/gateway/recall/recall.go)：

1. **模式与范围解析**：默认 `mode` 为 `hybrid`（支持 `hybrid` / `graph` / `chunks` / `code`；`kind="code"` 时默认切为 `code`），`limit` 范围 `1..50`（默认 `10`）。经 `recall.ResolveScopes` 校验项目 ACL 并排除受保护项目；非 `code` 模式下若 Agent 具备通用记忆读权限，自动透明纳入通用记忆数据集。
2. **多范围并发与故障隔离**：`recall.Execute` 以最多 4 路并发按单个 `scope` 独立调用 `MemoryEngine.Search`：
   - 新建项目尚未在底层引擎初始化数据集（`ErrDatasetNotProvisioned`）时，视为健康的 0 命中，不视为故障。
   - 若所有主项目范围全部失败（或仅请求通用记忆且失败），**fail-closed 直接报错**，绝不静默降级成只返回通用记忆。
   - 若部分范围失败而其余范围成功，返回可用结果并显式标记 `partial: true` 与脱敏 `scope_errors`。
3. **项目优先配额合并（`mergeHitsWithQuota`）**：`limit < 3` 时优先填满主项目命中；`limit >= 3` 时为通用记忆预留 `max(1, limit/5)` 配额，其余归主项目并允许空位回补，防止通用记忆挤占当前项目约定。
4. **图谱上下文白名单过滤与显式打点**：`mergeGraphContexts` 按授权 `dataset_id` 白名单过滤并去重 `entities`、`facts`、`conflicts`、`code_facts` 与 `session_guidance`，透出 `degraded_to_chunks` 降级标记。每次查询在 `memlane.sqlite3` 中记入 `channel: "explicit"` 统计与命中事件（与 Hook 自动召回和桌面端人工搜索区分；打点写入异常仅脱敏记日志，不阻塞召回响应）。

---

## 4. 安全与架构边界守卫

1. **统一主体鉴权（[core/gateway/agentauth/auth.go](../../core/gateway/agentauth/auth.go)）**：`/mcp` 与 Hook 路由（`/ingest`、`/recall`、`/session-end`）共享同一 `agentauth.Authenticator`，仅接受单一无歧义的 `Authorization: Bearer` 或 `X-Api-Key`，经 `O_NOFOLLOW` + `0600` 私钥读取与常量时间 SHA-256 比对识别主体；未注册返回 `401`，已归档/退役或歧义凭据返回 `403` 及对应错误码。
2. **零默认项目授权与保护数据集隔离**：电脑级连接身份不自动获得任何项目或通用记忆权限；MCP 所有读写请求均经 `core/domain` ACL 裁决，且严格拒绝读写受保护项目（如用户画像数据集）。
3. **引擎无关边界**：`core/gateway/mcp` 仅依赖 `core/domain`、`core/engine`、`core/gateway/agentauth`、`core/gateway/recall` 与 `RememberService` 接口，严禁导入 `core/engine/cognee`，保证未来替换底层记忆引擎时 MCP 协议与全部宿主配置保持不变。
4. **三级第三方冲突防护**：宿主安装逻辑（[core/hooks/install.go](../../core/hooks/install.go) 与 [kit/agents/conflicts.go](../../kit/agents/conflicts.go)）仅修改带受管标识的条目；对各宿主配置文件及 TRAE CN IDE 外部 `User/mcp.json`（`scanExtraMCPConflict`）均执行第三方 Cognee MCP 检测，检出冲突默认拦截并提示精确路径，严禁静默覆盖或删除用户原有配置。

---

## 5. 关键源码索引

| 模块 | 路径 | 职责 |
| --- | --- | --- |
| CLI stdio 桥接 | [cmd/memlane/mcp.go](../../cmd/memlane/mcp.go) | `memlane mcp` 子命令、私钥读取、Loopback 转发、并发限流与 `MEMLANE_PROCESSING_SESSION=1` 本地熔断 |
| 网关装配与路由 | [cmd/memlane/daemon.go](../../cmd/memlane/daemon.go)、[core/gateway/server.go](../../core/gateway/server.go) | 创建 `mcp.Server`、注入统一 `Authenticator` / `MCPRememberService` / `RegistryLocker` 并挂载 `/mcp` |
| MCP 协议与工具 | [core/gateway/mcp/server.go](../../core/gateway/mcp/server.go)、[tools.go](../../core/gateway/mcp/tools.go)、[recall.go](../../core/gateway/mcp/recall.go)、[auth.go](../../core/gateway/mcp/auth.go) | JSON-RPC 2.0 协议分发、`remember`/`remember_status`/`recall`/`health` 工具封装与鉴权适配 |
| 显式保存编排 | [core/gateway/mcp_remember.go](../../core/gateway/mcp_remember.go) | 3 秒快路径（`Ingest` → `Extract` → 目录发布）与 `capture_inbox` / `catalog_publish_state` 零丢单回退 |
| 统一鉴权与召回引擎 | [core/gateway/agentauth/auth.go](../../core/gateway/agentauth/auth.go)、[core/gateway/recall/recall.go](../../core/gateway/recall/recall.go) | 跨 Hook 与 MCP 共享的主体鉴权、授权范围解析、并发故障隔离检索与项目优先配额合并 |
| 宿主配置与扩展 | [core/hooks/install.go](../../core/hooks/install.go)、[engine/agents/pi/tools.ts](../../engine/agents/pi/tools.ts) | 受管 MCP 配置预览与原子写入、外部 `mcp.json` 冲突扫描及 `pi` 宿主工具转发 |
