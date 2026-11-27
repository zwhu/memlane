# 架构守护、Python 扩张冻结与定时架构巡检规划

- **日期**：2026-11-25（2026-11-26 Wave 0–4 收口；2026-11-28 实测行数与门禁口径更新）
- **适用范围**：Memlane 全仓日常开发、Swarm 多 Agent 协同、代码审查与长期架构治理

---

## 1. 背景：为什么要立这套机制

在高频使用多 Agent（Swarm 主控协调 + 并行编码）推进迭代后，工程上暴露出三类长期会让代码库腐化的问题：

1. **为了“能跑通”堆出大量防御代码、临时补丁与架构漂移**：执行型 Agent 天然优先让当前任务和测试变绿，遇到接口不对齐、边界异常或审核挑刺时，倾向于原地套 `try/except`、加 `fallback`/`legacy` 分支或写临时特判，而不是回上游修契约；靠口头规范无法根治，必须下沉到自动化门禁。
2. **早期 Python 文件数量与职责严重膨胀**：2026-11-25 重构前盘点曾达 **227 个 `.py`（95,071 行）**，其中非测试生产/脚本代码 **127 个（56,123 行）**。经 Wave 0–4 重构后，全部业务控制面、接入网关、单点 SQLite、Agent 模型代理、宿主 Hook 转发与开发启停已迁移至 Go 单二进制（`cmd/memlane`），Python 生产代码收敛为 `engines/cognee/` 下 **13 个文件**无状态引擎薄壳（Wave 0–4 初版 2,670 行；2026-11-28 清理死代码后实测 **3,303 / 3,500 物理行**），必须靠硬门禁守住不再回涨。
3. **过渡期兼容回退与迁移 lint 规则容易沦为“僵尸”**：早期 `ML019` 及桌面端旧全量拉表回退曾滞留仓内，Wave 0–4 已同步清零；未来演进仍需配套机制定期清理过期白名单与过渡规则。

---

## 2. 已落地的机械硬门禁（不靠 Prompt 靠机器）

### 2.1 增量 diff 防过度防御与分层护栏（`scripts/diff_guard.py`）

[scripts/diff_guard.py](../../scripts/diff_guard.py)（单测 [scripts/test_diff_guard.py](../../scripts/test_diff_guard.py)）已接入 [scripts/check.py](../../scripts/check.py)（`--changed` / `--staged` / `--full`）与 [.githooks/pre-commit](../../.githooks/pre-commit)：

| 规则 | 拦截/审计内容 | 执行方式 |
| --- | --- | --- |
| **`DG001`** | Python A 类生产代码新增宽泛 `except:` / `except Exception:` 且静默吞错（无日志/无上抛） | 硬阻断（需显式处理、删掉冗余兜底，或写明 `# memlane:allow-guard(理由)` 进审查） |
| **`DG002`** | Go A 类生产代码新增空的 `if err != nil {}` 静默吞错 | 硬阻断（同上，支持 `// memlane:allow-guard(理由)`） |
| **`DG003`** | A 类生产代码新增注释包含 `TODO` / `FIXME` / `HACK` / `WORKAROUND` / `临时补丁` 等临时标记，或空白豁免理由 | 硬阻断 |
| **`DG004`** | 修改 `--lock` 锁定的骨架/契约文件，或越界改动 `--allow` 白名单外的目录/文件 | 硬阻断（Swarm 切片按需启用，迫使写不下去时停机找主控改设计） |
| **`DG005`** | **禁止随意新增 `.py` 文件**：冻结非 Cognee 胶水的 Python 扩张，仅放行为已有 `.py` 补 1:1 `test_<stem>.py`，或经人工明确确认后通过 `--allow-new-py` 放行 | 硬阻断（`check.py` 与 `pre-commit` 全路径拦截） |
| **`DG006`** | **Go 分层 import 红线**：禁止 `kit/*` 导入 `core`/`app`/`internal`/根模块，禁止 `core/*` 导入 `app`/`kit/ui`，禁止非适配器/工厂/`cmd` 导入 `core/engine/cognee` | 硬阻断（增量 diff 即时拦截，全仓由 `arch_guard_test.go` 兜底） |
| **审计清单** | 自动从 diff 拎出新增的 `except`、`fallback`/`compat`/`legacy`/`sleep`、豁免声明及新增 `import` | 自动打印到门禁输出，带入双审逐条核查并删掉多余防御 |

### 2.2 全仓架构与无状态引擎薄壳守卫（`arch_guard_test.go` & `scripts/lint.py`）

- **[arch_guard_test.go](../../arch_guard_test.go)**（随 `sh scripts/go.sh test ./...` 自动运行）：
  - `TestArchitectureImportBoundaries`：全仓强制校验 `kit/*` 零业务依赖、`core/*` 零 UI 依赖、`app/*` 6 个子包无环隔离、`app/settings` 白名单边界，以及 `core/engine/cognee` 导入隔离。
  - `TestGoSQLiteWALJournalSizeLimit`：全仓校验 Tier-A Go 生产代码启用 SQLite WAL 时必须显式配置 `PRAGMA journal_size_limit` 或经共享 opener 打开。
- **[scripts/lint.py](../../scripts/lint.py)**（由 [lint_gate_test.go](../../lint_gate_test.go) 与 `check.py` 强制联动）：
  - **`ML023`**：守卫 `engines/cognee/`（递归含子目录，排除 `test_*.py`、`__pycache__` 与符号链接）无状态引擎薄壳边界——禁止导入 `engine`、`sqlite3`、`aiosqlite`，且非测试代码总量严格 `<= 3,500` 物理行（2026-11-28 实测 **3,303 / 3,500** 物理行）。
  - **`ML001–ML003`、`ML007–ML012`、`ML015`、`ML016`、`ML018`**：分别守护模型参数无默认值/保 `0`、环境隔离、UI 动效与首屏冷启动聚合、大文件流式哈希、Python SQLite WAL 限额与正式文档禁引过程产物目录。

---

## 3. 已完成清理 · 桌面端旧全量拉表回退与 `ML019`（2026-11-26 收口）

Wave 0–4 重构已彻底完成原 `ML019` 清零与桌面端架构收口：

- [x] 旧 `internal/simpledesktop/` 已重构拆分为 `app/` 组合根 + 6 个无环子包与 `app/api/`，并移除全部 `overviewDocuments` / `overviewSessions` / `loadOverviewLegacy` 旧全量拉表回退。
- [x] `scripts/lint.py` 中原过渡期规则 `ML019`–`ML022` 已随旧 Python `v2_api.py` / `catalog_index.py` 一并清理退场，不再保留僵尸规则与白名单；首页冷启动继续由 `ML012` 守卫走单次聚合 `overviewBootstrap`。

---

## 4. 中长期 TODO · 用定时 Loop 扫描项目架构与僵尸规则（待细化设计）

### 为什么考虑定时 Loop，而不是全塞进提交门禁？

- 提交门禁（`lint.py` / `diff_guard.py` / `arch_guard_test.go` / `check.py`）必须**快、低误报、只卡确定性红线**；如果把复杂的架构审计全塞进每次提交，既拖慢日常迭代，又容易因为模糊判断引发误报疲劳。
- **核心思路**：保留轻量提交门禁负责“实时止血”，另设一个**定时 Loop（周期性项目巡检任务）**，按固定周期对全仓做一次只读深度扫描，输出精简的“架构健康与待清理清单”，由人或专门的清理切片按优先级消化。

### 定时架构巡检需要定型的规则维度（TODO）

以下维度在正式写 Loop 前需要先逐项敲定判定标准与退场口径，避免扫出一堆没用的噪音：

- [ ] **1. 僵尸规则与过期白名单识别**
  - 区分「永久红线」（如 Cognee 零侵入、模型参数保 `0`、`ML023`、`arch_guard_test.go`）与「迁移期过渡规则」。
  - 检查 `scripts/lint.py` 与 `arch_guard_test.go` 中所有白名单：若登记的文件/函数已不存在，或实际命中数降为 0，自动列出「建议缩减白名单或整条删除的规则」。
- [ ] **2. 僵尸回退与超期豁免清点**
  - 扫描全仓长期滞留的 `legacy` / `compat` / `fallback` 分支（含 `internal/*` 存量兼容包）与 `memlane:allow-guard(...)` 豁免标记，结合 Git 历史定位早已无真实调用方的“防弹衣代码”。
- [ ] **3. 代码体量预算与语言边界趋势巡检**
  - 追踪 `engines/cognee/`（≤3,500）、`scripts/`（≤2,500）、`app/`、`kit/*`、`core/*` 与 `internal/*` 的文件数、行数与单文件 >500 行超标趋势，持续输出高价值瘦身候选清单。
- [ ] **4. 模块分层与跨层依赖健康度**
  - 定期检查 Go 与 Python 模块的依赖图：是否出现新功能绕开 `MemoryEngine` / `core/domain`、重复造轮子、或桌面端/网关/存储之间出现反向依赖与双份真相。
- [ ] **5. 巡检输出契约与防噪音机制**
  - 明确 Loop 的执行边界：**纯只读扫描 + 结构化短报告（Top N 高价值可删/可改项）**，严禁无人值守静默大改代码。
  - 约定“已确认有意保留项”的静默机制，避免同一条已知取舍每周反复告警。
