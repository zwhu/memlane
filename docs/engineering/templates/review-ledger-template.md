# 双独立代码审核与契约例外账本模板

配套工程规范见 [工程规范](../workflow.md)「双独立代码审核与收敛机制」；在 Antigravity（`agy`）中执行 T2/T3 双审或 Swarm 审波时同步加载 `agy` 专属双审 Skill。每次任务的实例化填写产物属于过程记录，请按仓库根目录 `AGENTS.md`「过程文档定义与隔离」存放于本地忽略过程目录，不提交入库；仅将长期生效的架构裁决提炼入正式文档。

---

## 0. 切片元信息与 Pre-Review Gate

- **切片 ID / 主题**：`<SLICE_ID>`
- **任务分级**：`T1 / T2 / T3-Slice-N`（单批目标 ≤30 文件，软上限 40 文件，含测试 ≤50）
- **基线与锚点**：`SLICE_BASE_REF=<sha>` | `R1_BASE_REF=<sha>` | `R1_END_REF=<sha>`
- **改动文件分层清单**：
  - **A 类（生产核心代码，含 `scripts/check.py`、`scripts/utm_guard.py`、`lint_gate_test.go`）**：`<files>`
  - **B 类（测试与验收脚手架，含 `verify_setup_cli.py`、`smoke_release_contracts.py`、`utm_isolated_env.py`）**：`<files>`
  - **C 类（文档与规范）**：`<files>`
- **开审前定向门禁回执**：`python3 scripts/task.py -- python3 scripts/check.py --changed`（退出码 `0`、耗时 `<Ns>`；此阶段不跑全量）

---

## 1. 已确认契约例外账本（Contract Exception Ledger — 严禁翻案区）

> 以下条目已由设计阶段或主控裁决确认。除非本轮增量 diff 破坏了表中声明的前提条件，否则任何审稿人严禁重复提出或据此给出 `REQUEST_CHANGES`。

| 编号 | 涉及文件 / 契约 | 已确认结论 | 裁决依据 / 实测事实 | 确认轮次 |
| --- | --- | --- | --- | --- |
| EX-01 | `<path / contract>` | `<decision>` | `<evidence>` | R0 / R1 |

---

## 2. Round 1 初始审查 Prompt 模板（双路独立只读）

```markdown
## Round 1 独立代码审查指令（只读，严禁运行全量测试套件）

- **切片目标与冻结契约**：`<1-3 句目标与关键契约链接>`
- **Diff 命令**：`git diff <SLICE_BASE_REF>..HEAD`
- **文件分层清单**：
  - A 类生产代码（严格零 P0/P1）：`<files>`
  - B 类测试/脚手架（仅 ①假绿 / ②掩盖缺陷 / ③污染环境 / ④泄密 且须给真实触发路径方可定 P1）：`<files>`
  - C 类文档：`<files>`
- **已确认契约例外账本（严禁翻案）**：`<粘贴第 1 节表格>`

### 审查硬约束
1. 重点核对 Memlane 红线（Cognee 零侵入、`.local/daily` 隔离、禁清 `build/`、`ML001–ML022`、模型参数契约、并发与锁安全）及本切片核心契约。
2. **禁止运行全量测试套件**（`check.py --full`、`go test ./...`）；单次只读核实命令 ≤10 秒。
3. B 类文件提 P1 必须勾选 `[①假绿 / ②掩盖缺陷 / ③污染环境 / ④泄密]` 并给出真实（非合成 mock）触发路径，否则必须归为 P2。
4. **固定输出结构**（≤600 字）：
   - `### P0 / P1 阻断项`（`[ID | 文件:行号 | A/B/C | P0/P1 | B类依据①-④ | 真实影响与修复建议]`；无则写 `无`）
   - `### P2 备注（不阻断）`
   - `### 结论`：`APPROVE` 或 `REQUEST_CHANGES`
```

---

## 3. Round 2 增量 diff 复审 Prompt 模板（只读 + 单路已 `APPROVE` 免唤醒）

- **唤醒核对**：Reviewer A 状态 `APPROVE / REQUEST_CHANGES`（是否唤醒：`是 / 否-免唤醒理由`）；Reviewer B 状态 `APPROVE / REQUEST_CHANGES`（是否唤醒：`是 / 否-免唤醒理由`）。

```markdown
## Round 2 增量复审指令（只读，严禁运行全量测试套件）

- **切片 / 范围**：`<SLICE_ID>` | 基线：`<SLICE_BASE_REF>` | 上轮锚点：`<R1_END_REF>`
- **本轮增量 Diff 命令**：`git diff <R1_END_REF>..HEAD`
- **本轮修改文件分层清单**：
  - A 类生产代码：`<files>`
  - B 类测试/脚手架：`<files>`
  - C 类文档：`<files>`

### 1. 已确认契约例外账本（严禁翻案区）
<粘贴更新后的第 1 节表格>

### 2. R1 待闭环问题清单（逐项核验）
| ID | 分层 | 级别 | R1 问题描述 | 修复摘要与文件:行号 | 定向验证回执 |
| --- | --- | --- | --- | --- | --- |
| R1-01 | A/B | P0/P1 | `<issue>` | `<fix summary>` | `check.py --changed` exit 0 |

### 3. 复审硬约束
1. **仅审查**：① 上表 R1 问题是否真正闭环；② `R1→R2` 增量 diff 是否引入**新的 P0 或 A 类生产 P1 回归**。
2. **禁止**：全仓重读历史代码、翻案「已确认契约例外账本」、对 B 类脚手架做纯合成 mock 边角或“防御套防御”递归挑刺、执行全量测试套件（核实单点限 ≤10s 命令）。
3. **B 类提 P1 举证门槛**：必须勾选 `[①假绿 / ②掩盖缺陷 / ③污染环境 / ④泄密]` 并给出真实运行触发路径，否则必须归为 P2。
4. **固定输出结构**（≤500 字）：
   - `### R1 闭环核对`（逐 ID 标 `CLOSED` / `OPEN` + 一句话依据）
   - `### 增量新发现（仅 P0/P1）`（`[ID | 文件:行号 | A/B/C | P0/P1 | B类依据①-④ | 真实影响]`；无则写 `无`）
   - `### P2 备注（不阻断）`
   - `### 结论`：`APPROVE` 或 `REQUEST_CHANGES`
```

---

## 4. Round 3 主控终局裁决记录（硬上限，严禁进入 R4–R7）

> 仅在 Round 2 结束仍有未收敛项时填写。**红线：R3 既不可豁免 P0，也不可豁免确凿 A 类生产 P1。**

| 编号 | 未收敛项与分层 | 判定归类（情况 A 脚手架/口径分歧 \| 情况 B 确凿 A 类 P0/P1） | 主控实测证据 / 红绿复现命令 | 终局裁决（直接收口 / 精准修后收口 / 连续2轮未收敛升级设计重审） |
| --- | --- | --- | --- | --- |
| R3-01 | `<item (A/B/C)>` | `情况 A / 情况 B` | `<cmd & result>` | `<verdict>` |

- **冻结后单一全量门禁回执**：`python3 scripts/task.py -- python3 scripts/check.py --full`（候选发版 `--release`），退出码 `0`、耗时 `<Ns>`。
