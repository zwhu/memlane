# 架构守护、Python 扩张冻结与定时架构巡检规划

- **日期**：2026-11-25
- **适用范围**：Memlane 全仓日常开发、Swarm 多 Agent 协同、代码审查与后续重构治理

---

## 1. 背景：为什么要立这套机制

在高频使用多 Agent（Swarm 主控协调 + Codex 并行编码）推进迭代后，工程上暴露出三类长期会让代码库腐化的问题：

1. **为了“能跑通”堆出大量防御代码、临时补丁与架构漂移**
   - 执行型 Agent 天然优先让当前任务和测试变绿，遇到接口不对齐、边界异常或审核挑刺时，倾向于原地套 `try/except`、加 `fallback`/`legacy` 分支或写临时特判，而不是回上游修契约。
   - 靠 Prompt 和口头规范无法根治，上下文一长就会复发，必须把关键约束下沉到自动化门禁与审查清单。
2. **Python 文件数量与职责严重膨胀**
   - 2026-11-25 全仓盘点：已入库 `.py` 共 **227 个文件（95,071 行）**，其中非测试生产/脚本代码 **127 个文件（56,123 行）**——`engine/python/` 占 104 个（41,803 行），`scripts/` 占 22 个（14,030 行）。
   - 真正必须依赖 Python 对接 Cognee 与底层模型管线的模块不到 20%；其余大部分（`catalog_index.py`、`v2_api*`、`reading_state_store.py`、`project_control*`、各宿主 Hook/安装配置与运维脚本）本质是普通 HTTP 服务、SQLite 读写与子进程工具，因早期惯性全堆进了 Python，推高了运行态依赖体积与维护复杂度。
3. **过渡期兼容回退与迁移 lint 规则容易沦为“僵尸”**
   - 以 [lint.py](../../scripts/lint.py) 中的 `ML019` 为例：规则表面上是禁止 Go 桌面端调用旧全量拉表接口 `overviewDocuments` / `overviewSessions`，但实际盘点发现桌面端仍残留 7 处 `overviewDocuments` 与 1 处 `overviewSessions` 的回退路径及对应白名单。
   - 当旧接口在重构中逐步退场后，如果没有配套机制触发清理，过渡期的回退代码和对应迁移规则会一起滞留在仓库里，既制造维护噪音又让人不敢动。

---

## 2. 已落地的硬门禁（不靠 Prompt 靠机器）

已新增 [scripts/diff_guard.py](../../scripts/diff_guard.py)（单测 [scripts/test_diff_guard.py](../../scripts/test_diff_guard.py)），并接入 [scripts/check.py](../../scripts/check.py)（`--changed` / `--staged`）与 [.githooks/pre-commit](../../.githooks/pre-commit)：

| 规则 | 拦截/审计内容 | 执行方式 |
| --- | --- | --- |
| **`DG001`** | Python A 类生产代码新增宽泛 `except:` / `except Exception:` 且静默吞错（无日志/无上抛） | 硬阻断（需显式处理、删掉冗余兜底，或写明 `# memlane:allow-guard(理由)` 进审查） |
| **`DG002`** | Go A 类生产代码新增空的 `if err != nil {}` 静默吞错 | 硬阻断（同上，支持 `// memlane:allow-guard(理由)`） |
| **`DG003`** | A 类生产代码新增注释包含 `TODO` / `FIXME` / `HACK` / `WORKAROUND` / `临时补丁` 等临时标记，或空白豁免理由 | 硬阻断 |
| **`DG004`** | 修改 `--lock` 锁定的骨架/契约文件，或越界改动 `--allow` 白名单外的目录/文件 | 硬阻断（Swarm 切片按需启用，迫使写不下去时停机找主控改设计） |
| **`DG005`** | **禁止随意新增 `.py` 文件**：冻结非 Cognee 胶水的 Python 扩张，仅放行为已有 `.py` 补 1:1 `test_<stem>.py`，或经人工明确确认后通过 `--allow-new-py` 放行 | 硬阻断（`check.py --changed`、`--staged` 与 `pre-commit` 全路径拦截） |
| **审计清单** | 自动从 diff 拎出新增的 `except`、`fallback`/`compat`/`legacy`/`sleep`、豁免声明及新增 `import` | 自动打印到门禁输出，带入双审逐条核查并删掉多余防御 |

---

## 3. 待清理 TODO · 桌面端旧全量拉表回退与 `ML019`

2026-11-25 逐点核查 [internal/simpledesktop/](../../internal/simpledesktop/) 的实测结论：

- **需保留的主链路**：`/v1/overview/bootstrap`（`overviewBootstrap`）是现行 V2 首页冷启动聚合接口（`ML012` 强制要求走它），不属于废弃接口。
- **真正需要清理的旧回退（共 7 处 `overviewDocuments` + 1 处 `overviewSessions`）**：
  1. **真实运行态漏网点（1 处）**：`common_knowledge.go:confirmCommon`（保存/编辑结果未知时核对原结果）尚未切到 V2 点查/分页，仍在直接调 `commonDocumentItems` → `overviewDocuments`。
  2. **仅 `!hasControl` 旧测试路径可达的僵尸回退（6 处）**：`overview_ui.go:loadOverviewLegacy`（含 `overview.go:overview` / `overviewSessions`）、`knowledge_waterfall.go`、`app.go`（通用知识 & 原始记录）、`overview_open.go`、`project_queue.go:pendingProjectCatalog`、`common_knowledge_page.go:confirmCommonDeletion`、`unified_search.go:recentSearchItems`。真实运行态必有 `ControlURL` 且 V2 失败已 `failClosed`，这些分支仅为未配 `ControlURL` 的旧 `httptest` 单测保留。
- **清理步骤（单开独立垂直切片，避开当前未提交改动）**：
  - [ ] 将 `common_knowledge.go:confirmCommon` 切换至 V2 控制面接口（`documentLookupV2` / `commonKnowledgePageV2`）。
  - [ ] 迁移依赖无控制面 mock 的旧 Go 单测（`overview_test.go`、`overview_ui_test.go`、`common_knowledge_page_test.go`、`project_queue_test.go`、`project_reading_ui_test.go`、`session_feedback_test.go`、`search_interaction_test.go`、`cold_start_scheduling_test.go`）。
  - [ ] 一次性删除 `overviewDocuments`、`overviewSessions`、`loadOverviewLegacy`、`commonDocumentItems`、`projectRecordCatalog`、`pendingProjectCatalog`、`recentSearchItems` 等旧全量拉表回退代码。
  - [ ] 同步移除 `scripts/lint.py` 中的 `ML019` 及其白名单和对应测试，避免遗留僵尸规则。

---

## 4. 中长期 TODO · 用定时 Loop 扫描项目架构与僵尸规则（待细化设计）

### 为什么考虑定时 Loop，而不是全塞进提交门禁？

- 提交门禁（`lint.py` / `diff_guard.py` / `check.py`）必须**快、低误报、只卡确定性红线**；如果把复杂的架构审计全塞进每次提交，既拖慢日常迭代，又容易因为模糊判断引发误报疲劳。
- **核心思路**：保留轻量提交门禁负责“实时止血”，另设一个**定时 Loop（周期性项目巡检任务）**，按固定周期对全仓做一次只读深度扫描，输出精简的“架构健康与待清理清单”，由人或专门的清理切片按优先级消化。

### 定时架构巡检需要仔细思考和定型的规则维度（TODO）

以下维度在正式写 Loop 前需要先逐项敲定判定标准与退场口径，避免扫出一堆没用的噪音：

- [ ] **1. 僵尸规则与过期白名单识别**
  - 区分「永久红线」（如 Cognee 零侵入、模型参数保 `0`）与「迁移期过渡规则」（如 `ML019`）。
  - 检查 `scripts/lint.py` 中所有 `ALLOWLIST`：若登记的文件/函数已不存在，或实际命中数已低于上限/降为 0，自动列出「建议缩减白名单或整条删除的规则」。
- [ ] **2. 僵尸回退与超期豁免清点**
  - 扫描全仓长期滞留的 `!hasControl` / `legacy` / `compat` / `fallback` 分支与 `memlane:allow-guard(...)` 豁免标记，结合 Git 历史定位存活超过一定周期却早已无真实调用方的“防弹衣代码”。
- [ ] **3. Python 存量瘦身与语言边界漂移巡检**
  - 追踪 `engine/python/` 与 `scripts/` 的文件数、行数趋势（目标只降不升）。
  - 识别不依赖 Cognee Python API 的纯业务/SQLite/Hook/CLI 模块，持续输出“最容易搬进 Go 的下一批候选清单”。
- [ ] **4. 模块分层与跨层依赖健康度**
  - 定期检查 Go 与 Python 模块的依赖图：是否出现新功能绕开核心契约层、重复造轮子、或桌面端/网关/存储之间出现反向依赖与双份真相。
- [ ] **5. 巡检输出契约与防噪音机制**
  - 明确 Loop 的执行边界：**纯只读扫描 + 结构化短报告（Top N 高价值可删/可改项）**，严禁无人值守静默大改代码。
  - 约定“已确认有意保留项”的静默机制，避免同一条已知取舍每周反复告警。
