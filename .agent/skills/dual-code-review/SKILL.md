---
name: dual-code-review
description: 仅供 Antigravity（agy）主控与 agy Swarm 审稿 Worker 在 Memlane 仓库发起 T2/T3 独立双路代码审核、Swarm Review Wave 或增量复审时使用。强制执行 A/B/C 分层 P1 举证门槛、2+1 收敛上限、增量 diff 与已确认契约例外账本、单路 APPROVE 免唤醒、审稿人禁跑全量测试及 Memlane 红线核对。非 agy 宿主（Codex / TRAE / pi / OpenCode）不使用本 Skill；单路通用两轴审查见全局 code-review。
---

# Memlane 收敛式双独立代码审核（仅 `agy` 专用）

本 Skill 仅面向 **Antigravity（`agy`）主控与 `agy` Swarm 审稿 Worker**（使用 `agy` 的 `/swarm` / `invoke_subagent`，可选外接只读 `codex exec --ephemeral` 提供异构对抗视角）。目的是在不放松任何生产红线的前提下，将双独立代码审核稳定收敛在 **1–2 轮修复 + 可选第 3 轮主控终局裁决（2+1）**，杜绝把测试/验收脚手架当生产代码拉锯 6–7 轮。

配套可填表模板见 [review-ledger-template.md](../../../docs/engineering/templates/review-ledger-template.md)。实例化审核账本属于过程产物，一律存放于 `docs/process/<主题或日期>/`（不提交入库）。

---

## 1. 仓库路径 A / B / C 三层归类

- **A 类 · 生产核心代码（Strict Tier，零 P0 / 零 P1）**：
  - `engine/python/*.py`（不含 `test_*.py`）、`engine/*.json`、`engine/agents/`、`engine/prompts/`、`engine/patches/`
  - `internal/**/*.go`（不含 `*_test.go`）、`cmd/**/*.go`（不含 `*_test.go`）、`runtime_assets.go`、`lint_gate_test.go`、`web/official-graph/`（不含 `verify.mjs`）
  - 生产发版、守护与门禁脚本：`scripts/build_release.py`、`scripts/dev.py`、`scripts/dev_recovery.py`、`scripts/uninstall.py`、`scripts/disk-space.py`、`scripts/task.py`、`scripts/lint.py`、`scripts/check.py`、`scripts/utm_guard.py`、`scripts/go.sh`、`scripts/install-hooks.sh`、`scripts/local_prepare.py`、`scripts/mygo_sdk.py`、`scripts/build_*_plugin.py`、`scripts/bootstrap.py`
  - `.githooks/*`
- **B 类 · 测试、验收驱动与开发夹具（Scaffold Tier，仅 4 类真风险可定 P1）**：
  - `**/*_test.go`（`lint_gate_test.go` 除外）、`**/test_*.py`、`scripts/test_*.mjs`、`runtime_assets_test.go`
  - 验收、冒烟与诊断脚手架：`scripts/verify_setup_cli.py`、`scripts/smoke_release_contracts.py`、`scripts/utm_isolated_env.py`、`scripts/probe_*.py`、`scripts/bench_scaling.py`、`scripts/processing-timings.py`、`scripts/regress-project-queue.sh`、`web/official-graph/verify.mjs`
- **C 类 · 文档与规范（Doc Tier）**：
  - `docs/**/*.md`、`AGENTS.md`、`CONTEXT.md`、`README*.md`、`.agents/skills/**`、`.agent/skills/**`

---

## 2. 分层 P0 / P1 / P2 判定清单与主控预筛权

| 级别 | A 类：生产核心代码 | B 类：测试与验收脚手架 | C 类：文档与规范 |
| --- | --- | --- | --- |
| **P0（硬阻断，不可豁免）** | 安全/ACL 绕过/凭据泄露；破坏 `.local/daily` 或清空 `build/`；违反 Cognee 零侵入红线；数据丢失/死锁/孤儿进程失控；无法编译或核心主流程不可用；违反 `ML001–ML022` | 破坏宿主真实运行环境或登录态（如误删 `Trae CN` / `Memlane` 目录或日常库）；泄露真实密钥/凭据到日志或仓库；硬编码吞失败造成明确假绿 | 引导危险命令（清 `build/`、改上游 Cognee、改全局 `~/.gitconfig`）；正式 `docs/` 引用 `docs/process/`（触发 `ML018`） |
| **P1（阻断本轮合入）** | 真实场景可复现的功能正确性缺陷、并发/状态机竞态、HTTP/JSON/退出码契约偏差、FD/WAL/进程泄漏、错误吞噬、模型参数补默认值或丢显式 `0`、画像总超时（`ML006`）；**违背既定架构的绕行/旁路实现、未经审批的冗余防御兜底/静默降级、为过测塞进生产代码的临时特判**（不只查漏做，也要打回删掉多余盔甲） | **仅允许以下 4 类且必须给出真实（非合成 mock）触发路径**：<br>① **假绿风险**：断言缺失/永真/过度 mock 致被测核心逻辑挂掉仍过测；<br>② **掩盖真实产品缺陷**：吞掉非零退出码/异常、测错版本/二进制却报通过；<br>③ **环境污染/状态串扰**：未隔离 `$TMPDIR`/`HOME`、残留进程、跨场景污染；<br>④ **凭据泄露/越权写入**：打印 token 或写入仓库跟踪目录 | 接口/命令行参数/退出码文档与现行实现直接矛盾且会误导使用 |
| **P2（记录不阻断，禁拉新轮）** | 可读性、命名风格、非热路径微优化、极端不可达防御分支 | **一律强制归 P2（严禁升级 P1）**：仅合成 mock 异常序列才出现的轮询/重试边角；对上轮新增防御代码再做“防御套防御”的次生挑刺；测试 helper 风格重构或文案润色 | 措辞润色、排版微调 |

- **主控预筛降级权**：审稿人对 B 类文件提 P1 时，若未显式勾选 ①–④ 并写明真实（非合成 mock）触发路径，主控在汇总去重阶段**直接降级为 P2 并记入账本**，不下发修复。

---

## 3. 「2 轮修复 + 第 3 轮主控裁决（2+1）」执行协议

### Stage 0 · Pre-Review Gate（主控 1 分钟预检）
1. 核对单切片规模：目标 ≤30 文件、软上限 40 文件（含测试 ≤50、净增产码 ≤2,500 行）；超限先按垂直切片拆批，禁止把 100+ 文件巨型 diff 直接送审。
2. 初始化本切片「已确认契约例外账本（Contract Exception Ledger）」：填入设计阶段已拍板取舍、刻意保留的兼容行为或有意暂缓项。
3. 确认编码侧已通过改动感知定向门禁 `python3 scripts/task.py -- python3 scripts/check.py --changed`（自动联动 `scripts/diff_guard.py` 硬拦 `DG001–DG005`（含非 Cognee Python 新增冻结）并输出新增防御/兜底/豁免/跨层 `import` 审计清单；若本切片锁定了骨架文件须附 `--lock`/`--allow`；**本阶段不跑无关全量测试**），并将 `diff_guard` 审计清单与 `SLICE_BASE_REF`、`R1_BASE_REF` 一并写入账本。

### Round 1 · 双路独立全量对抗审查
1. 并行启动两路只读审稿人（`agy` Swarm 审稿 Worker / `invoke_subagent`，可选其中一路调用只读 `codex exec --ephemeral` 实现异构视角），使用 [review-ledger-template.md](../../../docs/engineering/templates/review-ledger-template.md) 的 R1 Prompt，注入切片目标、A/B/C 分层清单与初始契约例外账本。
2. **审稿人禁令**：严禁执行全量测试套件（`check.py --full`、`go test ./...`）；单次只读核实命令不得超过 10 秒。
3. **主控合并去重（Triage）**：合并两路重复项 → 剔除已入例外账本项 → 降级不合格的 B 类伪 P1 → 将新确认的设计取舍立即追加写入例外账本 → 一次性下发全部确认 P0/P1 修复。合并后零 P0 且零确认 P1 → 直接 `CONVERGED`，无需 Round 2。

### Round 2 · 增量 diff 定向复审 + 单路已 `APPROVE` 免唤醒
1. 修复完成后仅跑 `python3 scripts/task.py -- python3 scripts/check.py --changed`，打下 `R1_END_REF`，生成增量 diff `git diff <R1_END_REF>..HEAD`。
2. **单路已 `APPROVE` 免唤醒规则**：
   - **按域分工双审**（如 Go 路 / Python 路）：某路 R1 已 `APPROVE` 且 `R1→R2` 增量 diff 未触及其负责域与共享跨层契约文件 → **免唤醒**，直接沿用 `APPROVE`；
   - **同域全量双审**：一路 R1 已 `APPROVE`，若 `R1→R2` 仅修改 B 类脚手架或 ≤3 处无契约变更的局部补丁 → **仅唤醒原 `REQUEST_CHANGES` 一路**确认闭环；仅当 `R1→R2` 修改 A 类生产核心代码的接口/状态机/并发/安全路径时，才增量复审已 `APPROVE` 一路（同样只看增量 diff）。
3. 复审必须使用模板中的 **Round 2 增量复审 Prompt**，强制附带完整「已确认契约例外账本」与 R1 问题修复回执；**严禁**全仓重扫历史代码、翻案例外账本、或对 B 类新增防御代码递归挑边角。

### Round 3 · 主控终局裁决（硬上限，严禁进入 R4–R7）
若 Round 2 后仍存在未收敛项，立即由主控收口，严禁无休止互评：
- **情况 A（仅剩 B 类脚手架边角、P2 争议或契约口径分歧）**：主控结合实测直接裁决收口，将结论与依据写入审核账本（归入契约例外或后续待办），宣布审核通过，不再唤醒审稿人。
- **情况 B（仍存在确凿 P0 或 A 类生产 P1）**：**既不可豁免 P0，也不可豁免确凿 A 类生产 P1**。不开开放式 R3 全量复审；主控针对该单点执行精准修复 + **可执行红绿复现验证**（必要时仅对该 1 个 hunk 做 ≤30 秒单路核验）+ 冻结门禁收口。若**同一具体生产缺陷连续 2 轮审核修复仍未根治**，立即停止打补丁，升级为底层设计/根因重审。
- **冻结后单一全量门禁**：审核收敛并冻结源码后，由**单一验证节点**执行 `python3 scripts/task.py -- python3 scripts/check.py --full`（候选发版用 `--release`），杜绝编码、审核、提交三方重复跑全量。

---

## 4. Memlane 审稿必查红线清单（任一违规直判 P0）

1. **Cognee 零侵入**：未修改上游 Cognee 源码、未绕过 `official_service_dev.verify_source` v2 校验、无运行期无契约 monkey-patch、受校验源码目录零 `.pyc`/`.pyo`。
2. **单一日常库与目录保护**：未触碰或污染 `.local/daily/official-store`、未清空 `build/` 或破坏 `build/disk-cleanup/` 回执、受管重任务经 `scripts/task.py`（50/55 GiB 门禁）。
3. **静态与模型契约**：全仓 `scripts/lint.py`（`ML001–ML003`、`ML006–ML022`）零违规；改动模型参数/构造/探针/切换链路时已跑 4 项 `test_generation_*` 回归且未 stub 共享构造器；保留显式 `0`、省略未配置项、画像无总超时（`ML006`）。
4. **隐私、身份与过程隔离**：无密钥/真实路径/聊天记录入库；未修改 `.githooks/_identity_guard.sh` 或全局 `~/.gitconfig`；正式 `docs/` 零引用 `docs/process/`（`ML018`）。
5. **与「四次失败停止规则」正交**：本协议只收敛静态代码审核轮次，不替代或削弱 `docs/engineering/workflow.md` 中「同一具体缺陷累计第 4 次验收未通过即停止并汇报」的实测验收红线。
