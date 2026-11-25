# Memlane 目标架构边界契约

- **状态**：已拍板（目标架构准绳，2026-11-25 生效）
- **定位**：定义 Memlane 后续重构、瘦身与新功能开发的**硬性架构边界、语言边界、模块依赖方向与代码规模预算**。
- **关联文档**：
  - 现行代码规模与边界客观基线：[architecture-boundary-and-loc-facts-20261125.md](../engineering/architecture-boundary-and-loc-facts-20261125.md)（`b1a82e0` 基线：生产 + 脚手架 122,482 行，其中 Python 56,786 行）
  - 现行增量止血门禁：[architecture-governance-and-py-freeze.md](../engineering/architecture-governance-and-py-freeze.md)（`diff_guard.py` 与 `DG005` 禁止随意新增 `.py`）
  - 领域唯一术语表：[CONTEXT.md](../../CONTEXT.md)

> **阅读口径**：本文规定的是**目标边界**而非已迁移完成的代码现状。存量代码按本契约分批收口；自本文生效起，所有新模块与改造必须单向朝本边界收敛，禁止继续扩大旧的跨语言平铺与兼容债务。

---

## 1. 五项核心边界决策

1. **进程与语言边界（Go 单一主二进制，Python 退回引擎薄壳）**
   - Go 收口为单一多模式二进制入口：`memlane app`（桌面端）| `memlane daemon`（M1 保活后台与接入网关）| `memlane hook`（宿主事件薄转发）| `memlane setup` / `dev`（安装自举、诊断修复与开发启停），消除 `internal/bootstrap` 与 `scripts/dev.py` 双轨并行。
   - **Python 唯一存在理由**：仅限直接 `import cognee / litellm / ladybug` 的引擎胶水代码；其余全部控制面、网关、SQLite 存储、Agent 模型代理、宿主适配与运维脚本统一归 Go。
2. **宿主接入负边界（方案 C：统一 Go `memlane hook` + 极简扩展 + 标准 MCP）**
   - 宿主侧彻底与 Cognee 官方插件及跨代旧插件兼容脱钩，不再向宿主分发 Python Hook 脚本、本地补采队列或运行时补丁。
   - 支持 Shell Hook 的核心宿主（TRAE CN、TraeX、Codex、Antigravity 等）统一配置调用本机 Go 子命令 `memlane hook`；无 Shell Hook 的宿主（如 pi）仅保留极薄无状态事件转发扩展；其余长尾宿主统一走 Go 网关原生暴露的标准 MCP Server。
   - 升级 Memlane 二进制即自动升级全部 Hook 逻辑，彻底消灭多代旧插件降级兼容分支与历史插件清单快照。
3. **产品与状态模型边界（极简两层模型，砍掉重型消息/补采状态机）**
   - 内容流收敛为极简两层：**原始采集记录（Capture） → 结构化记忆（Memory）**。
   - 采集流收敛为单向实时流水线：**项目开启自动采集 → 宿主实时上报 → Go 网关落本地轻量收件箱 → 异步提炼写入记忆引擎**；放弃本地历史 rollout 补采队列、手动勾选待采集消息、暂停/审阅/恢复多代任务状态机与跨进程消息版本仲裁。
   - 异常仅表达“最近失败原因 + 一键重试”，不再维护重型消息运营后台。
4. **记忆引擎边界（引擎无关 6 原语接口，保留替换 Cognee 能力）**
   - Go 领域层与接入网关不向上暴露 Cognee 的原生路由（`/cognee/*`）、Dataset 结构或内部概念；所有引擎交互经由统一的 `MemoryEngine` 接口完成。
   - Cognee `1.6.7`（继续遵守上游源码零侵入红线）仅作为 `MemoryEngine` 的 1 号实现，未来可无缝替换为其他本地或远程记忆引擎，而不影响 Go 领域层、桌面 UI 与任何宿主接入。
5. **跨 App 通用能力预留（零业务依赖隔离，暂不提前拆仓）**
   - 将 UI 框架、Agent 检测与接入、AI 辅助安装与进程守护沉淀在独立的 `kit/*` 分层中，为未来复用至新 App 保留干净切口。
   - 现阶段不另建独立仓库、不做过度泛化抽象，只严格执行单向依赖红线。

---

## 2. 目标分层与单向依赖图

系统自底向上划分为三段，依赖方向**严格单向**（上层依赖下层，同层禁止循环依赖，下层严禁反向 `import` 上层）：

```text
======================== 通用底座层 (kit/* · 零 Memlane 业务依赖) ========================
  kit/ui            MyGo 组件库、主题令牌、动效原语、虚拟列表、弹窗与托盘外壳
  kit/agents        本机 Agent 发现、安装检测、配置安全读写、受管 Hook/MCP 与 CLI 代理
  kit/aiinstaller   Agent-First 依赖安装、环境自举、健康诊断与自我修复框架
  kit/daemon        .service-owner flock 单实例锁、Setsid 进程组守护、日志截尾与退出策略

=========================== Memlane 领域与应用层 (Go 单二进制) ===========================
  core/engine       引擎无关抽象接口 MemoryEngine（6 原语）+ 具体引擎适配器（1 号：cognee）
  core/domain       极简领域模型、项目注册与独立主体 ACL、轻量元数据 SQLite（单点独占读写）
  core/gateway      本地唯一入口：/api/*（UI/CLI）、/ingest（memlane hook）、/mcp（通用 Agent）
  app/              桌面 UI 按页面拆分子包（shell / overview / reading / search / settings / graph）
  cmd/memlane       单一 Go 二进制入口（app | daemon | hook | setup | dev）

=========================== 隔离外部引擎与画布层 (非 Go 最小区) ===========================
  engines/cognee    极薄 Python Runner（仅 Cognee / LiteLLM / Ladybug 适配，零业务状态）
  web/graph         沙箱图谱 WebView（仅消费标准 {nodes, edges}，无凭据、无直连网络）
```

### 强制依赖红线

| 规则 | 硬约束 |
| --- | --- |
| **通用层零业务污染** | `kit/*` 下任何包**严禁** `import` `core/*` 或 `app/*` |
| **领域层无 UI 依赖** | `core/*` **严禁** `import` `app/*` 或 `kit/ui` |
| **UI 层无业务仲裁** | `app/*` 仅负责渲染与触发意图，禁止跨包互访私有状态，禁止承载权限裁决、采集水位或版本乐观锁 |
| **引擎隔离** | 除 `core/engine/cognee` 与 `engines/cognee` 外，全仓任何模块不得感知 Cognee 特有数据结构或路由 |
| **Python 冻结边界** | 任何不直接 `import cognee / litellm / ladybug` 的新功能禁止写入 Python（由 `DG005` 自动拦截） |

---

## 3. 宿主接入与 Cognee 冲突处理边界

### 3.1 宿主接入原则
- **唯一决策点**：是否允许采集、项目权限校验（`none` / `read` / `maintain`，坚持电脑级连接零默认项目授权红线）、去重与入队，100% 在 Go 接入网关一处裁决；宿主侧保持无状态哑转发。
- **Agent 模型代理归 Go**：`agent_bridge` 本质为本地 OpenAI 兼容 HTTP 接口转发至本机 Agent CLI 子进程，纳入 Go `kit/agents` 与 `core/gateway` 实现，不再依赖 Python 补全端点。
- **仅改受管项**：安装与配置写入只修改明确带有 Memlane 受管标识的条目，严禁触碰用户无关第三方插件、MCP、密钥与历史会话。

### 3.2 用户已安装 Cognee 时的分级冲突策略

先区分物理边界：**用户本机独立安装的 Cognee 软件包、Docker 容器或云端账号与 Memlane 物理隔离，完全不冲突，不得提示用户删除**。仅针对**宿主 Agent 内的记忆插件 / Hook / MCP 挂载点**执行冲突检测与分级处理（沉淀为 `kit/agents` 标准能力）：

| 冲突类别 | 识别口径 | 处理边界 |
| --- | --- | --- |
| **1. 旧版 Memlane 派生 Cognee 插件** | 含 Memlane 受管标识（如 `memlane-cognee` 路径或标记） | 在连接变更预览中明示「将旧版 Memlane 插件迁移替换为 `memlane hook`」，用户确认后由 Memlane 原子替换并清理旧插件 |
| **2. 用户自装第三方 / 官方 Cognee 插件或 Hook** | 宿主配置中存在非 Memlane 受管的 `cognee` 插件、Hook 或 MCP | **默认拦截叠加，严禁静默删除或覆盖**：界面明确展示冲突插件名与精确配置文件路径，说明并存会导致重复采集与双份注入；引导用户停用/移除后重新检测（若提供辅助停用，须经二次预览确认且仅移入备份目录而非不可逆删除） |
| **3. 其他无关第三方插件 / MCP** | 非记忆冲突条目 | 零改动、零提示 |

---

## 4. 可替换记忆引擎接口边界（`MemoryEngine`）

`core/engine` 向上层领域暴露且仅暴露 **6 个引擎无关原语**；底层实现（如 `engines/cognee`）通过内部 Loopback HTTP 承接：

1. `Health() / Capabilities()`：进程存活、引擎版本、存储路径与能力标记。
2. `Ingest(scope, source_meta, content)`：写入原始采集材料，返回引擎侧稳定文档标识。
3. `Extract(scope, doc_ids, model_config)`：按当前唯一激活的提炼配置（Agent 模型代理或直连 Provider / Ollama）从原始材料抽取结构化记忆与关系。
4. `Search(scopes, query, filters, limit)`：语义、关键词或图谱联合召回，返回标准化命中列表与出处引用。
5. `List / Get / Delete(scope, item_id)`：原始记录与结构化记忆条目的受授权读取与删除。
6. `Graph(scope, filter)`：返回引擎无关的标准 `{nodes, edges}` 图谱载荷供 `web/graph` 沙箱渲染。

**存储真相边界**：
- **内容唯一真相**：当前生效记忆引擎的日常存储目录（Cognee 实现下仍为 `.local/daily/official-store`，遵守 Cognee 上游源码零侵入与逐文件 SHA-256 校验红线）。
- **Memlane 自有治理状态**：将现有分散的多份外置 SQLite 与 JSON 收口为 **1 个由 Go 网关单点独占读写的私有 SQLite**（`0600` + WAL），消除跨进程多写者竞态与文件锁复杂度。

---

## 5. 代码体量预算红线（目标 ~50,000 行）

以 `b1a82e0` 基线（生产 + 脚手架 **122,482 行**）为参照，设定长期硬预算。任一分层触及上限时，**必须先清理存量债务或简化设计，再合入新代码**：

| 分层 | 基线实测 (`b1a82e0`) | 目标预算上限 | 收敛要点 |
| --- | ---: | ---: | --- |
| **Python 引擎壳** (`engines/cognee`) | 41,945 *(全部 Python 生产)* | **≤ 3,500** | 仅留 Cognee / LiteLLM / Ladybug 必需胶水，零业务状态 |
| **Go 通用底座 + 领域 + 网关** (`kit/*` + `core/*` + `cmd/`) | 16,635 *(原底座)* | **≤ 18,000** | 吸收原 Python 网关/项目治理/轻索引/模型代理，消灭双语重复与双套启动器 |
| **Go 原生桌面 UI** (`app/`) | 41,404 *(`simpledesktop`)* | **≤ 22,000** | 按页面拆子包、砍掉跨进程状态同步样板与重型消息队列 UI |
| **图谱画布 + 宿主极薄扩展** (`web/graph` + 非 Hook 扩展) | 7,469 | **≤ 4,000** | 淘汰 OpenCode 厚插件与历史补丁；保留图谱画布与极简转发壳 |
| **工程脚手架** (`scripts/`) | 14,967 | **≤ 2,500** | 收口至 Go 原生工具链（`go test` / `go vet`）与薄打包脚本，退出万行双语编排 |
| **生产 + 脚手架合计** | **~122,500** | **≤ 50,000** | Python 占比从 46% 压至 ~7%，单文件软上限 500 行 |

---

## 6. 跨 App 可复用模块备忘（暂不提前抽仓）

以下通用能力在后续按边界重构时自然落入 `kit/*`，**现阶段只保接口干净与零 Memlane 依赖，不单开抽离工程**：

- **`kit/ui`**：MyGo 桌面自绘基础组件、双外观令牌、动效原语、虚拟列表、托盘与窗口框架。
- **`kit/agents`**：各家 Coding Agent 自动检测、版本识别、受管配置预览与原子读写、第三方冲突识别、CLI 模型代理封装。
- **`kit/aiinstaller`**：面向终端用户的 Agent-First 辅助安装、环境预检、镜像源兜底与可恢复诊断框架。
- **`kit/daemon`**：macOS 单实例独占锁、进程组生命周期管理、日志截尾与防抖健康检查。

---

## 7. 与现行文档的演进关系

本文在以下两点明确替代既有文档的旧方向，后续按阶段同步修订对应规范与实现：

1. 替代 [agent-connection-contract.md](agent-connection-contract.md) 中「优先复用目标 Agent 的 Cognee 官方集成」条款 → 演进为本文 §3「宿主侧与 Cognee 解耦，统一走 `memlane hook` + 极简扩展 + 标准 MCP，冲突第三方 Cognee 插件检测提示停用」。
2. 替代 [overview.md](overview.md) 中「接入网关、目录投影与模型代理位于 `engine/python/`」的现状分层 → 演进为本文 §2「全部业务收归单一 Go 二进制，Python 退回 ≤ 3,500 行可替换引擎薄壳」。
