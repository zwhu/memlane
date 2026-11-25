# Memlane 目标架构重构实施蓝图（2026-11-26）

- **状态**：正式架构实施蓝图（Stage 0 全仓摸底完成，配合 [boundaries.md](boundaries.md) 落地执行）
- **关联基准**：
  - 目标架构边界与预算红线：[boundaries.md](boundaries.md)
  - 代码体量与边界客观基线：[architecture-boundary-and-loc-facts-20261125.md](../engineering/architecture-boundary-and-loc-facts-20261125.md)（`b1a82e0` 基线 122,482 行；`cccb1bb` 落地 6 态首装向导后 `internal/simpledesktop/` 为 133 生产文件 / 44,064 行，`engine/python/` 为 105 生产文件 / 41,825 行，`scripts/` 14,967 行，`web/official-graph/` 5,597 行）
  - 增量门禁与 Python 冻结：[architecture-governance-and-py-freeze.md](../engineering/architecture-governance-and-py-freeze.md)
  - 产品基线：[product.md](../product/product.md)
  - 工程治理与发版门禁：[workflow.md](../engineering/workflow.md)

---

## 1. 目标体量预算核验

按 Stage 0 全仓逐文件摸底结果，本蓝图严格收敛至 [boundaries.md](boundaries.md) §5 的 ~50,000 行预算红线内：

| 分层 | 当前实测（`cccb1bb`） | 重构后预估 | 预算上限 | 收敛方式 |
| --- | ---: | ---: | ---: | --- |
| **Python 引擎薄壳**（`engines/cognee`） | 41,825 | ~2,250–2,550 | **≤ 3,500** | 仅保留直接依赖 `cognee` / `litellm` / `ladybug` / `httpx` 出口的 ~12 文件（净减 94%） |
| **Go 底座 + 领域 + 网关 + 入口**（`kit/*` + `core/*` + `cmd/memlane`） | ~16,635 | ~16,000–17,500 | **≤ 18,000** | 吸收控制面、单 SQLite、`/api/*` + `/ingest` + `/mcp`、`agent_bridge` 与 `user_profiles`，消灭双套启动器 |
| **Go 原生桌面 UI**（`app/`） | 44,064 | ~20,000 | **≤ 22,000** | 抽离零业务 `kit/ui`（~3,200 行）；删除两层模型下 21 个重型队列文件（-5,311 行）并瘦身 5 个文件（-2,834 行）；按页拆子包 |
| **图谱画布 + 非 Hook 扩展**（`web/graph` + `pi`） | 7,469 *(含 vendor 5,487)* | ~200 自研 *(vendor 单列)* | **≤ 4,000 *(自研)*** | 淘汰 `engine/agents/opencode/` 与历史补丁；`pi` 缩为 ~90 行无 Python 转发壳；已确认 `web/official-graph/vendor/cognee/src/`（5,487 行）按上游第三方 vendor 资产单列，不计入 4,000 行自研预算 |
| **工程脚手架**（`scripts/`） | 14,967 | ~2,290 | **≤ 2,500** | `dev.py` / `uninstall.py` 收归 `cmd/memlane`；契约冒烟与压测迁入 Go tests；`lint.py` Go 规则迁入 `arch_guard_test.go` |

---

## 2. Stage 0 核实的关键架构决策与技术约束

1. **先补齐门禁盲区再写新目录**：现行 `scripts/diff_guard.py` 仅对 `engine/python/`、`scripts/`、`internal/`、`cmd/` 执行 Tier-A 生产检查，新建的 `kit/`、`core/`、`app/`、`engines/cognee/` 会绕过 `DG001–DG003`。Wave 0 必须先将其纳入 Tier-A，并新增 `DG006` 增量依赖检查与 Go 原生 `arch_guard_test.go`（`go/parser` 强拦 `kit/*` 反向 import、`core/*` import UI、非引擎区 Cognee 渗透，并将 `ML016` WAL + `journal_size_limit` 覆盖到 Go SQLite）。
2. **Go SQLite 选型与构建签名约束**：`scripts/go.sh` 默认 `CGO_ENABLED=0`，`scripts/build_release.py` 强制 `codesign --verify --deep --strict` 且要求 `doctor < 5s`。首选纯 Go **`modernc.org/sqlite`**（零 CGO、零动态库、完整 WAL/FTS5/`busy_timeout`/`journal_size_limit`）；若与 `go 1.27.4` 存在兼容异常则回退 `github.com/ncruces/go-sqlite3`（WASM）。Wave 0 先跑单命令编译+签名冒烟验证再锁定 `go.mod`。
3. **冷启动 `<150ms` 性能红线与 `MemoryEngine` 边界**：Cognee HTTP `/raw` 会触发全表扫描，不可用于 UI 分页。须将严格只读的 `cognee_db`/`cache.db` 投影器（`?mode=ro` + `PRAGMA query_only=ON`，禁用 `immutable=1`，零正文拷贝）与 `dir_fd`+`O_NOFOLLOW` 本地正文读取封装在 `core/engine/cognee` 内部，写入 `memlane.sqlite3` 中可原地重建的 `catalog_*` 表。
4. **`MemoryEngine` 6 原语与引擎内部扩展能力**：对外仅暴露 `Health/Capabilities`、`Ingest`、`Extract`、`Search`、`List/Get/Delete`、`Graph` 六原语；针对 Cognee 1.6.7 进程内状态（`litellm.model_cost`、`get_cognify_config`、`llm_config` ContextVar、`EndpointCapacity`、`PipelineRun` 终态校验、直连 Provider `httpx` `language_egress` 与 SDK 专属 `tool_graph` 管线），在 `core/engine` 内以可选能力接口 `ModelController` 与 `ToolGraphRunner` 隔离，并由 `Extract` 携带 `expected_model_digest` 与 `output_language`，避免泄露 Cognee 细节给上层领域或 UI。
5. **存量 `.local/daily/official-store` 零侵入与子 Agent 身份兼容**：主真相库原地不动。ACL 全部收归 Go 网关集中裁决后，严禁删除 `cognee_db` 中已有的子 Agent Principal UUID（`codex-*`/`pi-*`/`traex-*`/`antigravity-*`）；`core/engine/cognee` 保留按 `user_id` 读取历史会话与构建 `tool_graph` 的能力。
6. **单 SQLite 原子无损迁移协议（`.local/daily/memlane.sqlite3`）**：
   - 将现有 6 份外置 SQLite 与 4 类 JSON 合并为 Go 网关单写者私有 SQLite（`0600` + WAL + `journal_size_limit=4 MiB` + `busy_timeout=5000` + `foreign_keys=ON`）；原始 API 密钥继续留在 `0600` `generation-credentials/` 文件中，严禁写入 SQLite；`catalog_*` 可重建；重型 `backlog/jobs.json` 等仅归档不迁移。
   - 首次切入流程：持 `.service-owner` flock 严禁旧 Python 与新 Go 并发打开日常库 → 对全部旧 SQLite 执行 `PRAGMA wal_checkpoint(TRUNCATE)` → 全量快照备份至 `.local/daily/migration-backup-<ts>/`（`0700`）→ 单事务导入 `memlane.sqlite3.tmp` 并校验表行数、最早日期锁定、`//` `scope_hash` 与 9 位 ns UTC 时间戳不变量 → 原子 rename 为 `memlane.sqlite3` 并落 `.memlane-v2-owner` 防旧二进制误启造成脑裂。
7. **日期锁定与两层采集语义**：`Ingest(kind=capture_qa)` 写入前注入本地 `context.date`；`Extract()` 完成后立即从 `cognee_db` 读取 `system_metadata` 并锁定 `(dataset_id, lesson_type, state_key)` 最早日期至 `memlane.sqlite3`，保证 `GET` 接口全程严格只读。

---

## 3. 产品功能处置账本（防隐性丢功能基线）

在删除任何存量 Python 或 UI 代码前，对照 [product.md](../product/product.md) 与 [boundaries.md](boundaries.md) 锁定每一项用户能力的明确处置口径。**凡未显式标为 `DROP` 的能力一律按 `KEEP-EXACT` 保真，编码阶段禁止擅自降级或删减**：

| 功能域 | 处置 | 具体保真 / 变更口径 |
| --- | --- | --- |
| **电脑级接入与零默认授权** | `KEEP-EXACT` | 电脑级连接只建立宿主接入与凭证，绝不自动授任何项目、通用记忆或 `ml-user-profiles` 权限；项目三档权限 `none`/`read`/`maintain` 即时生效 |
| **项目自动采集开关** | `KEEP-EXACT` | 关闭后宿主/网关均停止自动写入，重启后保持关闭，不阻塞手动读取与主动记忆操作（对齐 `39cac4c` 语义） |
| **记忆日期锁定与默认语言** | `KEEP-EXACT` | 最早日期锁定（重提炼不漂移、`GET` 无写副作用）；新记忆按 `zh-CN` 出口改写，不回写历史英文记忆 |
| **通用记忆独立数据集与召回合并** | `KEEP-EXACT` | 个人偏好/通用经验独立管理、编辑与删除；Agent 具备读权限时在检索/召回入口合并（纯代码检索除外） |
| **用户画像（按来源独立 Markdown）** | `KEEP-EXACT` | 迁入 Go 执行，严格保持手动触发、原样展示、不自动并入通用记忆、无固定总时长超时与保存阶段不可取消（`ML006` 延展至 Go） |
| **提炼模型互斥激活与参数契约** | `KEEP-EXACT` | `agent_bridge`（Go 实现，覆盖 Codex/TraeX/pi/Antigravity）与直连 Provider/Ollama 并存但唯一 `active`；保存不激活；保显式 `0`、省未配项（`ML001–ML003` + 4 组 `test_generation_*`） |
| **首页总览、三栏阅读、`⌘K` 搜索、图谱沙箱** | `KEEP-EXACT` | `1200×820` 默认/最小窗口、V4 浅/深双外观、热力图、未读计数（仅人工读正文消未读）、滚动追加、图谱沙箱 `{nodes, edges}`、冷启动 `<150ms`/帧 `<12ms`/`<16ms`/堆 `<=25 MiB` |
| **6 态专注式首装向导与托盘守护** | `KEEP-EXACT` | 完整保留 `cccb1bb` 6 态向导、页内同意、脱敏日志、批量接入、验证并启用、M1 保活退出策略与 `.service-owner` flock |
| **宿主接入与冲突治理** | `SIMPLIFY` | 按方案 C 切换为 `memlane hook`（TRAE CN/TraeX/Codex/Antigravity）+ 极薄无 Python `pi` 扩展 + 标准 Go `/mcp`；在 `kit/agents` 落地 3 级 Cognee 冲突检测（旧 Memlane 迁移替换 / 第三方 Cognee 拦截提示不静默删改 / 无关插件零触碰） |
| **采集流水线与异常表达** | `SIMPLIFY` | 收敛为「原始采集 Capture → 结构化记忆 Memory」实时单向流水线；异常收口为「最近失败原因 + 一键重试」 |
| **重型历史补采与旧插件债务** | `DROP` | 砍除本地历史 rollout 补采队列、手动勾选待采集消息、多代暂停/审阅/恢复队列状态机、`engine/agents/opencode/` 厚插件、`engine/patches/`、Python `*hook*.py` 与 7 份历史插件清单快照 |

---

## 4. 产品功能完整性与正确性六层保障体系

跨语言、跨模块大规模重构（122.5k → ≤50k 行）最容易出现“单测全绿但产品丢功能、行为漂移、存量库损坏或真实宿主断链”。本项目强制执行以下六层验收与防漂移机制：

### 第 1 层 · 功能账本硬约束（防遗漏）
- 以 §3 功能处置账本为验收总纲，每个垂直切片的 Task Brief 必须写明所承接的账本条目，审核与验收按条目逐项打勾，禁止隐性删减。

### 第 2 层 · 双轨黑盒黄金契约测试（Golden Contract Parity Harness，防行为漂移）
- 在退役任何 Python 能力（目录投影、日期锁定、未读状态、召回打点、项目 ACL、自动采集门禁、模型代理、用户画像、图谱数据）前，先编写**语言无关的黑盒契约测试集**（固定 HTTP 请求/状态码/JSON 契约与 SQLite 状态不变量）：
  1. 先对**现行 Python 服务**运行，确认测试集 100% 反映现网真实契约；
  2. 再对**新 Go `core/domain` + `core/gateway`** 运行同一套测试集（并加 `-race` 并发检查）；
  3. 仅当新 Go 100% 通过等价黑盒契约测试后，才允许进入切换切片；**旧 Python 删除永远安排在切换验证完成后的独立切片**，绝不同批次边迁边删。

### 第 3 层 · 真实日常库克隆迁移与全量对账（Shadow Clone Gate，防数据损坏）
- 严禁仅靠几条合成测试数据宣告迁移通过。切换前将本机 `.local/daily` 只读复制至隔离 `$TMPDIR` 克隆库，执行 `ImportLegacyDailyStore()` 并启动新 Go 守护进程，运行自动化对账脚本逐项比对旧 Python 与新 Go 在同一份真实数据上的输出：
  - 项目表、别名、三档 ACL、自动采集开关、会话绑定 100% 一致；
  - 全部记忆条目的最早锁定日期、召回次数与热力图统计 100% 一致；
  - 全部未读计数、`//` `scope_hash` 与 9 位 ns UTC 时间戳 100% 一致；
  - 首页 bootstrap、知识分页、原始记录、通用记忆、全局搜索、图谱 `{nodes, edges}` 与用户画像文档指针完整无缺；
  - 验证回滚保护：备份目录完整未损，且 `.memlane-v2-owner` 能阻断旧二进制误启。

### 第 4 层 · 三权分立协同机制（防自己写代码自己放水）
充分利用各方优势并切断“编写者自我放行”环路：

| 角色 | 承担方 | 权限与硬职责 |
| --- | --- | --- |
| **架构总控与证据终审** | Antigravity 主控 + Swarm Verifier | 冻结架构边界、接口骨架与黄金契约测试（`diff_guard.py --lock`/`--allow`）；跑自动化门禁（`DG001–DG006`、`lint.py`、`check.py --full`）；执行 2+1 独立静态代码审查；**逐项审阅真实链路验收落盘的原始证据（截图、数据库快照、回执日志、退出码）并拥有唯一合入签收权** |
| **隔离实现编码** | Codex CLI（编码会话，独立 `git worktree`） | 在互斥文件白名单内实现切片代码；严禁修改锁死的契约与黄金测试，严禁新增非引擎 `.py` 或静默吞错；遇契约阻塞停机上报架构总控 |
| **真实链路与 GUI 验收取证** | Codex CLI（**独立冷启动只读验收会话**，发挥 Computer-Use 能力） | **严禁复用编码会话上下文、严禁改动源码**；持架构总控下发的《真机验收用例单》，直接操作真实 macOS Native 窗口与真实 Coding Agent 宿主执行连续用户场景，将每步原始证据（全窗浅/深色 PNG、宿主会话回执、SQLite 对账输出、`lsof` 隔离结果与退出码）落盘至本地过程目录交由架构总控终审 |

### 第 5 层 · 分层实机与真窗连续场景验收（复用 Level 0 / 1 / 2）
单元测试不替代真实二进制、真实窗口与真实宿主。每波次收口须通过对应层次的真实验证：
- **Level 0 无 Mock 契约冒烟**：真实单二进制 `memlane` + 真实 `engines/cognee` 在 `$TMPDIR` 隔离环境下通过冷启动、`doctor < 5s`、锁竞争、优雅停服、卸载与 `lsof` 零污染断言。
- **Codex Computer-Use 真实宿主闭环**：覆盖 Codex、TRAE CN、TraeX、Antigravity 与 `pi`——实测开启自动采集新会话入库提炼、关闭自动采集零新写入且重启保持关闭、跨会话真实召回（含通用记忆合并）、第三方 Cognee 插件冲突拦截不静默覆盖、以及 `agent_bridge` + 直连 Provider/Ollama 真实模型验证与热切换。
- **Codex Computer-Use Native `1200×820` 双外观验收 + 性能门禁**：`perf_budget_test.go`（冷启动 `<150ms`、帧 `<12ms`/`<16ms`、堆 `<=25 MiB`）全绿 + 浅/深两套外观逐屏实窗验收（6 态首装向导、总览热力图、三栏阅读、`⌘K` 搜索、图谱画布、设置与权限、托盘弹层）。
- **Level 1 / Level 2 发版门禁**：大波次里程碑与发版前跑通既有 UTM 净室首装、半装中断恢复与旧版升级自愈场景。

### 第 6 层 · 渐进绞杀与随时可回退（Strangler Fig）
- 每个切片控制在 ≤30 文件，合入主干前必须独立编译、独立过测、不破坏日常使用。
- 严格遵循「先建新 → 黄金契约对账 → 克隆库迁移验证 → 实机/真窗验收 → 日常浸泡 → 独立切片删除旧代码」顺序；遵守「同一具体缺陷累计第 4 次验收未通过即停止并汇报」红线。

---

## 5. Wave 0 – Wave 4 分波次垂直切片路线图

### Wave 0 · 架构护栏补齐与存量快速止血（零业务破坏，可并行推进）
- **W0-A · 门禁补盲与 SQLite 冒烟（~6 文件）**：更新 `scripts/diff_guard.py` 将 `kit/`、`core/`、`app/`、`engines/cognee/` 纳入 Tier-A 并新增 `DG006` 依赖边界门禁；新增 Go `arch_guard_test.go` 与 Go SQLite `ML016` 检查；在 `CGO_ENABLED=0` + `mygo build -sign -` 下验证 `modernc.org/sqlite` 编译签名全绿。
- **W0-B · 清理桌面端旧全量回退与 `ML019`（26 文件，-800 行）**：修复 `common_knowledge.go:confirmCommon` 走 V2，清理 6 处 `!hasControl` 僵尸回退，删除 `project_records.go`，迁移 13 个单测，移除 `scripts/lint.py` 中 `ML019` 及白名单。
- **W0-C · 清理废弃插件快照与死补丁（~18 文件，纯删除）**：删除 6 份 `engine/managed-project-plugin-pre-*.json` 与无引用旧补丁/夹具。

### Wave 1 · 零业务 `kit/*` 底座 + 单 SQLite + `core/engine` 契约冻结
- **W1-A · `kit/daemon` 抽离（~12 文件）**：参数化 `internal/supervisor` + `quit_policy.go`（零 Memlane import）。
- **W1-B · `kit/ui` 抽离（~22 文件）**：解耦 `tok`/`PoseTracker`/`ChunkCache`，将 14 个纯 UI 文件（~3,071 行）迁入零业务 `kit/ui`，同步更新 `lint.py` `ML008–ML011` 路径。
- **W1-C · `kit/aiinstaller` 抽离（~18 文件）**：抽离通用安装器、镜像源、诊断与 CLI 协议壳，剥离 `bootstrap/plugin.go` 对根包的反向引用，Memlane 5 组件清单收归 `core/setup`。
- **W1-D · `core/domain` 单 SQLite + 原子无损迁移器（~18 文件）**：落地 `memlane.sqlite3` Schema、单写者 DAL、`ImportLegacyDailyStore()`，并在克隆 `.local/daily` 上通过全字段对账。
- **W1-E · `core/engine` 6 原语 + `core/engine/cognee`（~16 文件）**：冻结 `MemoryEngine`（+ `ModelController`/`ToolGraphRunner`）、Go 只读 `cognee_db`/`cache.db` 投影器与 `O_NOFOLLOW` 本地正文读取。

### Wave 2 · Go 网关与模型代理上线 + `engines/cognee` 薄壳切换
- **W2-A · `kit/agents` + Go `agent_bridge`（~22 文件）**：统一宿主探测、3 级 Cognee 冲突扫描、4 家 CLI 模型代理与 `core/gateway/completion`（过黄金契约测试后退役 Python `*_completion*.py`）。
- **W2-B · `engines/cognee` 极薄 Python Runner（~14 文件，≤2,600 行）**：组装无状态 `/internal/v1/*` 薄壳，完整保留 `verify_source` v2、`ML001–ML003`、`language_egress`、`ollama_schema`、`probe.py`、`handoff.py`、`tool_graph.py`（须显式 `--allow-new-py` 并挂钩净减计划）。
- **W2-C · `core/gateway` 切换上线（~26 文件）**：承接 `/api/*`（过渡期兼容 `/v1/*`）+ `/ingest` + `/mcp` + 两层实时异步收件箱 + Go `user_profiles`（带 `ML006` 无总超时守卫）；通过黄金契约对账 + 克隆库全量迁移验收 + Codex 独立真机验证后再切实库浸泡。

### Wave 3 · 单一二进制 `cmd/memlane` + 宿主方案 C + 旧 Python 退场
- **W3-A · `cmd/memlane` + `memlane hook` + `/mcp` + 极薄 `pi`（~24 文件）**：切 `mygo.json` 至 `./cmd/memlane`（无头子命令零 Cocoa 初始化，守住 `doctor < 5s`）；核心宿主切 `memlane hook`，`pi` 去 Python 化；删 `engine/agents/opencode/` 与 `engine/patches/`；经 Codex 独立真机宿主闭环验收。
- **W3-B · 分两批删除旧 `engine/python/`（P5a/P5b，各 ≤28 文件）**：退场约 39,000 行旧 Python，在 `lint.py` 锁死 `engines/cognee ≤ 3,500 LOC`。

### Wave 4 · 桌面端两层模型瘦身拆包 + `scripts/` 退场
- **W4-A · 两层模型重型队列 UI 瘦身（U2a/U2b，各 ≤28 文件）**：删 21 个文件（-5,311 行）并精简 `project_queue*`。
- **W4-B · 统一 `app/api` 切 `/api/*`（~18 文件）**：合并双 HTTP 客户端，清零 UI 侧 Cognee 渗透，瘦身 `knowledge_unread.go`（1,284 → ~250 行）。
- **W4-C · 拆包 `app/{shell,overview,reading,search,settings,graph}`（U4a–U4d，4 批 ≤26 文件）**：完好保留 `cccb1bb` 6 态首装向导、V4 双外观与 `perf_budget_test.go` 红线，移除 `internal/simpledesktop/`，每批由 Codex 独立执行浅/深双外观实窗取证并由架构总控终审。
- **W4-D · `scripts/` 瘦身至 ≤2,500 行（~25 文件）**：`uninstall.py` 入 `cmd/memlane setup uninstall`，契约冒烟与压测入 Go tests，精简 `lint.py` / `check.py`，最终以全套 Level 0 / 1 / 2 闭环收口。
