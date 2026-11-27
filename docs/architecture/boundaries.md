# Memlane 架构边界契约

- **状态**：现行生效架构边界契约（Wave 0–4 已于 2026-11-26 落地收口）
- **定位**：定义 Memlane 现行系统与后续演进的**硬性架构边界、语言边界、模块依赖方向与代码规模预算**。
- **关联文档**：
  - 现行系统架构全景与源码映射：[overview.md](overview.md)
  - 机械守护与定时架构巡检规划：[architecture-governance-and-py-freeze.md](../engineering/architecture-governance-and-py-freeze.md)（`diff_guard.py` `DG001–DG006`、`arch_guard_test.go` 与 `ML023`）
  - 领域唯一术语表：[CONTEXT.md](../../CONTEXT.md)

> **执行口径**：本文全部边界已由 `arch_guard_test.go`、`scripts/diff_guard.py`（`DG005` / `DG006`）与 `scripts/lint.py`（`ML023`）机械强制执行。所有新功能与缺陷修复必须单向遵守本契约，禁止重新引入跨语言平铺、有状态 Python 业务逻辑或逆向依赖。

---

## 1. 五项核心边界决策

1. **进程与语言边界（Go 单一主二进制，Python 退回无状态引擎薄壳）**
   - Go 收口为单一多模式二进制入口（`cmd/memlane`）：`memlane app`（桌面端，具备生产态 `RequestSingleInstanceLock` 与开发态按库隔离 `.app-instance.lock` 单实例保护）| `memlane daemon`（M1 保活后台与 Gateway-First 接入网关：先拿 `.service-owner` 锁并清理过期 `cache/daemon-run`、先监网关、后异步拉起 `cognee-engine`；通过统一 `OwnerCodec` 兼容 `.daemon-owner.json` 与 `.memlane-dev-owner.json` 双文件名协议）| `memlane hook`（宿主事件薄转发）| `memlane setup`（安装自举、诊断修复）| `memlane dev`（开发启停），已彻底退役旧 `scripts/dev.py` 与 `engine/python/` 业务双轨。
   - **双态运行模型**：网关（`gateway_ok`）与记忆引擎（`engine_ok`）解耦；`/api/health` 区分 `ready` / `starting` / `degraded` / `exited`，`/v1/capabilities` 非阻塞返回缓存快照并随引擎就绪递增 `capabilities_epoch`，`/v1/runtime/monitor` 透出 `engine_phase`、`startup_error` 与完整收件箱积压/暂停状态；引擎启动失败或退出不静默自启，网关保持存活继续提供只读浏览与采集入队。
   - **Python 唯一存在理由**：仅限 `engines/cognee/` 下直接 `import cognee / litellm / ladybug` 的无状态引擎胶水代码（现行 ~3.2k 非测 LOC，硬上限 ≤3,500，由 `ML023` 守卫，禁导 `sqlite3`/`aiosqlite`/`engine`）；其余全部控制面、网关、SQLite 存储、Agent 模型代理、宿主适配与开发运维统一归 Go。
2. **宿主接入负边界（方案 C：统一 Go `memlane hook` + 极简扩展 + 标准 MCP）**
   - 宿主侧彻底与 Cognee 官方插件及跨代旧插件兼容脱钩，不再向宿主分发 Python Hook 脚本、本地补采队列或运行时补丁。
   - 支持 Shell Hook 的核心宿主（TRAE CN、TraeX、Codex、Antigravity 等）统一配置调用本机 Go 子命令 `memlane hook`（`core/hooks`）；无 Shell Hook 的宿主（`pi`）仅保留极薄无状态事件转发扩展（`engine/agents/pi/`）；其余长尾宿主统一走 Go 网关原生暴露的标准 `/mcp`（`core/gateway/mcp`）。
   - 升级 Memlane 二进制即自动升级全部 Hook 逻辑，彻底消灭多代旧插件降级兼容分支与历史插件清单快照。
3. **产品与状态模型边界（极简两层模型，砍掉重型消息/补采状态机）**
   - 内容流收敛为极简两层：**原始采集记录（Capture） → 结构化记忆（Memory）**。
   - 采集流收敛为单向实时流水线：**项目开启自动采集 → 宿主实时上报 → Go 网关单事务落本地轻量收件箱（`capture_inbox` 与持久化 `session_flush_jobs`，统一有界状态 `pending` / `processing` / `succeeded` / `failed` 及项目移除取消终态 `cancelled_project_removed`，按 `dataset_id` 同项目串行、跨项目并行）→ 异步提炼写入记忆引擎 → 按目标 `dataset_id` 单事务增量发布目录投影（`capture_trace` 仅轻量刷新会话，不重写 `catalog_docs`）**；放弃本地历史 rollout 补采队列、手动勾选待采集消息、暂停/审阅/恢复多代任务状态机与跨进程消息版本仲裁。
   - 异常仅表达“最近失败原因 + 一键重试”，不再维护重型消息运营后台；当 `Ingest` + `Extract` 已全部成功、仅最后的目录发布失败时，独立记录至 `catalog_publish_state`（重试仅重跑目录发布，零重调 LLM）。
4. **记忆引擎边界（引擎无关 6 原语接口，保留替换 Cognee 能力）**
   - Go 领域层与接入网关不向上暴露 Cognee 的原生路由、Dataset 内部结构或私有概念；所有引擎交互经由统一的 `core/engine` `MemoryEngine` 接口完成。
   - Cognee `1.6.7`（继续遵守上游源码零侵入与逐文件 SHA-256 校验红线）仅作为 `MemoryEngine` 的 1 号实现（`core/engine/cognee` + `engines/cognee`），未来可无缝替换为其他本地或远程记忆引擎，而不影响 Go 领域层、桌面 UI 与任何宿主接入。
5. **跨 App 通用能力隔离（`kit/*` 零业务依赖，暂不提前拆仓）**
   - 将 UI 框架、Agent 检测与接入、AI 辅助安装与进程守护沉淀在独立的 `kit/*` 分层中，为未来复用至新 App 保留干净切口。
   - 现阶段不另建独立仓库、不做过度泛化抽象，只严格执行单向依赖红线。

---

## 2. 现行分层与单向依赖图

系统自底向上划分为三段，依赖方向**严格单向**（上层依赖下层，同层禁止循环依赖，下层严禁反向 `import` 上层）：

```text
======================== 通用底座层 (kit/* · 零 Memlane 业务依赖) ========================
  kit/ui            MyGo 组件库、主题令牌、动效原语、虚拟列表、弹窗与托盘外壳
  kit/agents        本机 Agent 发现、安装检测、配置安全读写、冲突检测与 CLI 模型代理 (bridge/)
  kit/aiinstaller   Agent-First 依赖安装、环境自举、健康诊断与自我修复框架 (cli/)
  kit/daemon        .service-owner flock 单实例锁、Setsid 进程组守护、日志截尾与退出策略

=========================== Memlane 领域与应用层 (Go 单二进制) ===========================
  core/engine       引擎无关抽象接口 MemoryEngine（6 原语）+ 具体引擎适配器（1 号：cognee）
  core/domain       极简两层模型、项目注册与独立主体 ACL、单点私有 SQLite WAL (memlane.sqlite3)
  core/gateway      本地唯一入口：/api/* (/v1/*)、/ingest、/recall、/mcp、/v1/chat/completions
  core/profiles     各 AI 来源独立用户画像导出与存储
  core/hooks        方案 C 宿主 Hook 事件解析与无状态转发
  core/setup        首装依赖清单 (requirements.lock)、路径与品牌常量
  app/              桌面 UI 组合根 + 6 个无环子包 (shell/overview/reading/search/graph/settings) + api/
  cmd/memlane       单一 Go 二进制入口（app | daemon | hook | setup | dev）

=========================== 隔离外部引擎与画布层 (非 Go 最小区) ===========================
  engines/cognee    无状态 Python Runner（~3.2k LOC，仅 Cognee / LiteLLM / Ladybug 适配，零业务状态）
  engine/agents/pi  pi 宿主极薄无状态 TypeScript 事件转发扩展（~130 LOC）
  web/official-graph 沙箱图谱 WebView（仅消费标准 {nodes, edges}，无凭据、无直连网络）
```

### 机械强制依赖红线

以下全部规则已在 [arch_guard_test.go](../../arch_guard_test.go)、[scripts/diff_guard.py](../../scripts/diff_guard.py) 与 [scripts/lint.py](../../scripts/lint.py) 落地，每次 `go test ./...`、`check.py --changed` 与 `pre-commit` 自动拦截：

| 规则 | 硬约束 | 机械执行点 |
| --- | --- | --- |
| **通用层零业务污染** | `kit/*` 下任何包**严禁** `import` 根模块、`core/*`、`app/*` 或 `internal/*` | `arch_guard_test.go` + `DG006` |
| **领域层无 UI 依赖** | `core/*` **严禁** `import` `app/*` 或 `kit/ui` | `arch_guard_test.go` + `DG006` |
| **UI 层无环隔离** | `app/shell` 禁止导入 `app` 控制器；`overview`/`reading`/`search`/`graph`/`settings` 禁止互相导入或反向导入 `app` 根包；`app/settings` 仅允许白名单依赖 | `arch_guard_test.go` |
| **引擎隔离** | 除 `core/engine/cognee`、`core/engine` 工厂与 `cmd/` 外，全仓任何 Go 包不得导入 `core/engine/cognee`；`core/engine/...` 严禁反向导入 `core/domain` | `arch_guard_test.go` + `DG006` |
| **Python 无状态与冻结边界** | 禁止随意新增非 Cognee `.py` 文件；`engines/cognee` 非测试代码严格 `<= 3,500` 行且禁止导入 `engine`/`sqlite3`/`aiosqlite` | `DG005` + `ML023` |
| **SQLite WAL 限额** | Tier-A Go 与 Python 生产代码凡开启 SQLite WAL 必须显式配置 `PRAGMA journal_size_limit`（或经共享 opener） | `TestGoSQLiteWALJournalSizeLimit` + `ML016` |

---

## 3. 宿主接入与 Cognee 冲突处理边界

### 3.1 宿主接入原则
- **唯一决策点**：是否允许采集、项目权限校验（`none` / `read` / `maintain`，坚持电脑级连接零默认项目授权红线）、去重与入队，100% 在 Go 接入网关（`core/gateway`）一处裁决；宿主侧保持无状态哑转发。
- **Agent 模型代理归 Go**：`agent_bridge` 作为本地 OpenAI 兼容 HTTP 接口转发至本机 Agent CLI 子进程，已由 `kit/agents/bridge` 与 `core/gateway/completion` 实现，不再依赖 Python 补全端点。
- **仅改受管项**：安装与配置写入只修改明确带有 Memlane 受管标识的条目，严禁触碰用户无关第三方插件、MCP、密钥与历史会话。

### 3.2 用户已安装 Cognee 时的分级冲突策略

先区分物理边界：**用户本机独立安装的 Cognee 软件包、Docker 容器或云端账号与 Memlane 物理隔离，完全不冲突，不得提示用户删除**。仅针对**宿主 Agent 内的记忆插件 / Hook / MCP 挂载点**执行冲突检测与分级处理（实现见 `kit/agents/conflicts.go`）：

| 冲突类别 | 识别口径 | 处理边界 |
| --- | --- | --- |
| **1. 旧版 Memlane 派生 Cognee 插件** | 含 Memlane 受管标识（如 `memlane-cognee`、`memlane_cognee`、`memlane-memory` 等受管标记） | 在连接变更预览中明示「将旧版 Memlane 插件迁移替换为 `memlane hook`」，用户确认后由 Memlane 原子替换并清理旧插件 |
| **2. 用户自装第三方 / 官方 Cognee 插件或 Hook** | 宿主配置中存在非 Memlane 受管的 `cognee` 插件、Hook 或 MCP | **默认拦截叠加，严禁静默删除或覆盖**：界面明确展示冲突插件名与精确配置文件路径，说明并存会导致重复采集与双份注入；引导用户停用/移除后重新检测（若提供辅助停用，须经二次预览确认且仅移入备份目录而非不可逆删除） |
| **3. 其他无关第三方插件 / MCP** | 非记忆冲突条目 | 零改动、零提示 |

---

## 4. 可替换记忆引擎接口边界（`MemoryEngine`）

`core/engine/engine.go` 向上层领域暴露且仅暴露 **6 组引擎无关原语**；底层实现（如 `core/engine/cognee` → `engines/cognee/engine_api.py`）通过内部 Loopback HTTP 承接：

1. `Health()`：进程存活、引擎版本、运行模式、模型摘要、存储路径与能力标记。
2. `Ingest(scope, kind, idempotency_key, content, source_meta)`：写入原始采集材料（`capture_qa` / `capture_trace` / `document`），返回引擎侧稳定标识与去重结果。
3. `Extract(scope, doc_ids, expected_model_digest, output_language, mode)`：按当前唯一激活的提炼配置（Agent 模型代理或直连 Provider / Ollama）从原始材料抽取结构化记忆与关系（支持默认模式与 `tool_graph` 模式）。
4. `Search(scopes, query, filters, limit)`：语义、关键词或图谱联合召回，返回标准化命中列表与出处引用。
5. `ListItems / GetItem / DeleteItem(scope, item_id)`：原始记录与结构化记忆条目的受授权读取与删除。
6. `Graph(scope, filter)`：返回引擎无关的标准 `{nodes, edges}` 图谱载荷供 `web/official-graph` 沙箱渲染。

**存储真相边界**：
- **内容唯一真相**：当前生效记忆引擎的日常存储目录（Cognee 实现下仍为 `.local/daily/official-store`，遵守 Cognee 上游源码零侵入与逐文件 SHA-256 校验红线）。
- **Memlane 自有治理状态**：统一收敛为 **1 个由 Go 网关单点独占读写的私有 SQLite**（`memlane.sqlite3`，`0600` + WAL + `journal_size_limit`），消除跨进程多写者竞态与文件锁复杂度（`core/domain/migrate_legacy.go` 负责从早期多文件外置账本一次性平滑迁移）。

---

## 5. 代码体量预算与 Wave 0–4 收口对照

以重构前 `b1a82e0` 基线（生产 + 脚手架 **122,482 行**，其中 Python 生产代码 41,945 行、脚手架 14,967 行）为参照，Wave 0–4 已完成语言与模块边界收口，各分层长期受以下预算约束（后续演进触及上限前须先清理存量债务）：

| 分层 | 重构前基线 (`b1a82e0`) | Wave 4 后实测 (非测) | 长期目标上限 | 现状与持续收敛重点 |
| --- | ---: | ---: | ---: | --- |
| **Python 引擎壳** (`engines/cognee`) | 41,945 *(原 Python 生产)* | **3,229** | **≤ 3,500** | 已达标；由 `ML023` 与 `DG005` 硬守护 |
| **Go 底座 + 领域 + 入口** (`kit/*` + `core/*` + `cmd/` + 存量 `internal/`) | 16,635 *(原底座)* | 42,425 *(含 `internal/` 11,040)* | **≤ 18,000** *(远期瘦身)* | 已接管原 4 万行 Python 全部控制面/网关/存储/代理，并收敛统一 `OwnerCodec` 与 Gateway-First 守护；后续继续收拢 `internal/*` 残留并精简冗余代码 |
| **Go 原生桌面 UI** (`app/`) | 41,404 *(`simpledesktop`)* | 41,399 | **≤ 22,000** *(远期瘦身)* | 已完成 6 个无环子包拆分与 `arch_guard_test.go` 隔离；后续持续收敛大文件与旧 UI 样板 |
| **图谱画布 + 极薄宿主扩展** (`web/official-graph/src` + `engine/agents/pi`) | 7,469 *(含 vendor 5,487)* | **239** *(自研源码)* | **≤ 4,000** *(自研)* | 已达标；已淘汰 OpenCode 厚插件与历史补丁 |
| **工程脚手架** (`scripts/`) | 14,967 | **2,339** | **≤ 2,500** | 已达标；`scripts/dev.py` 等已退役收归 Go |

---

## 6. 跨 App 可复用模块（`kit/*`）

以下通用能力已落入零业务依赖的 `kit/*` 分层，**现阶段保持接口干净与 `arch_guard_test.go` 零业务依赖红线，不提前拆仓**：

- **`kit/ui`**：MyGo 桌面自绘基础组件、双外观令牌、动效原语、虚拟列表、托盘与窗口框架。
- **`kit/agents`**：各家 Coding Agent 自动检测、版本识别、受管配置预览与原子读写、三级第三方冲突识别、CLI 模型代理封装（`kit/agents/bridge`）。
- **`kit/aiinstaller`**：面向终端用户的 Agent-First 辅助安装、环境预检、镜像源兜底与可恢复诊断框架（含 `kit/aiinstaller/cli`）。
- **`kit/daemon`**：macOS 单实例独占锁、进程组生命周期管理、日志截尾与防抖健康检查。
