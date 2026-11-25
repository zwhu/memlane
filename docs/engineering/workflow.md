# 工程规范

2026-11-11 起实施；不改变产品、授权和设计。磁盘规则以 AGENTS.md 最新要求为准：开发态（`scripts/task.py`）最低保留 50 GiB、运行监测低于 55 GiB 停止受管任务；独立 `.app` 安装态监控阈值为最低 2 GiB / 告警线 5 GiB。

## 开发节奏

通用影响分析与排序方法已抽到 [plan-change-impact](../../.agents/skills/plan-change-impact/SKILL.md)。本项目使用[任务说明模板](templates/task-brief-template.md)，写入现有任务／交付账本；产品与设计以对应已确认交接为准。

1. 编码前明确用户主流程、真实入口、前置条件、预期数据变化、不允许变化和证据位置。用例先于代码；诊断问题先复现再修。
2. 集中修改同一类问题。实施与审核修复期间运行改动感知定向门禁 `python3 scripts/task.py -- python3 scripts/check.py --changed`（覆盖受影响包与全仓 `lint.py`）；授权越界、数据破坏、编译错误和主流程阻断立即修复。
3. 源码冻结后、打包或提交前由单一验证节点统一执行全量门禁 `python3 scripts/task.py -- python3 scripts/check.py --full`（候选发版用 `--release`，自动串行错峰 `lint` → `go vet/test` → 并行 Python 单测，护住 `perf_budget_test.go`）、组件冻结和完整 App 构建。记录输入摘要、版本和退出码；杜绝编码/审核/提交三方重复跑全量，不为每个间距或文案变更全量打包。
4. 同一合成根连续验收。失败保留原回执，按根因类别集中修复和复验；新操作使用新ID，不能覆盖旧失败。
5. 功能、UI、运行包分别登记。源码测试、协议夹具、模拟窗口、真实Native、实际模型各自只证明对应边界。

## 任务分级、垂直切片与编排路由

按变更体量与风险选择最小必要执行层级，避免小任务多层代理嵌套和大需求巨型 diff 跨层震荡（复盘依据见[多 Agent 耗时复盘](agent-workflow-efficiency-retrospective-20261125.md)）：

| 级别 | 触发边界 | 编排方式（强制上限） | 审核方式 | 门禁归属 |
| --- | --- | --- | --- | --- |
| **T0 · 确定性微操作** | ≤3 文件且单目录/单语言；文档或 TODO 更新、单点小修、配置调整、执行打包（**DMG/签名仅限用户明确要求后执行**）、本地 `git commit`/`tag` | 主控直接单步执行；严禁新建 worker、严禁套 `codex exec`、严禁 `/swarm` | 仅改文档/注释/非敏感小修免双审（自查 `git diff`）；**若触及安全/ACL/模型参数/守护锁/Cognee 边界，自动升格为 T1** | 改 A 类生产代码必跑 `scripts/check.py --changed`（命中模型文件自动跑 4 项 `test_generation_*`） |
| **T1 · 单模块功能 / 定向修复** | 4–15 文件，单一技术层，无跨进程协议或持久化 Schema 变更 | 主控直做，或最多 1 个扁平子代理（隔离排查/单路审）；禁止子代理内再套 `codex exec` 编码 | 触及安全/ACL/存储/模型参数/守护锁/迁移 → 1 路独立审（增量复审 ≤1 轮）；否则自查 | 开发轮 `check.py --changed`；冻结后改生产代码跑 `check.py --full` |
| **T2 · 单垂直切片跨层开发** | **16–40 文件**（目标 ≤30，软上限 40；含测试 ≤50，净增产码 ≤2,500 行），跨 2–3 层且文件归属可无重叠切分 | 聚焦并行 2–3 路按互斥文件编码 | 走「双独立代码审核与收敛机制（2+1）」 | 编码/审改轮 `check.py --changed`；冻结后单一节点跑 `check.py --full` |
| **T3 · 大型版本 / 多子系统重构** | 预估 **>40 文件**或包含 ≥2 个独立用户能力 | 须先经 `plan-change-impact` 拆分为多个 T2 垂直切片，严禁 100+ 文件合包开发与合包双审 | 保留 Stage 0 轻量对抗设计收敛（冻结跨层契约）→ **逐切片独立 2+1 双审并提交** | 每切片独立定向门禁 + 合入前全量门禁；候选包再跑发版门禁 |

- **反嵌套铁律**：常规编码、改文档、打包与提交上限 **≤2 层**（主控 → Worker，T0 为 1 层），禁止 Worker 内部再套 `codex exec` 代写代码或提交；仅 T2/T3 双独立审核中为获取异构对抗视角允许调用只读外部模型（如 `codex exec --ephemeral`），且严禁其跑全量测试。
- **垂直切片 4 条独立性标准**：每个切片须满足 ① 独立编译通过；② 独立跑通受影响单测与 `lint.py`；③ 不破坏日常库与现网兼容（中间态用可选字段或兼容回退过渡）；④ 文件数 ≤30（软上限 40，超限非机械重命名须先拆分或经用户确认豁免）。
- **流水线审查**：Stage 0 冻结共享接口契约后，解耦切片先完工者立即进入对应模块审查，不死等最慢编码路；全部合入后再做一次轻量跨层接口核对。

## 双独立代码审核与收敛机制

T2/T3 双独立审核与 Swarm 审波流程封装为 **Antigravity（`agy`）专属 Skill**（`.agent/skills/dual-code-review/SKILL.md`，仅 `agy` 主控与 `agy` Swarm 审稿 Worker 加载，不向 Codex / TRAE / pi / OpenCode 暴露），配套[审核与契约例外账本模板](templates/review-ledger-template.md)（实例化过程记录按 `AGENTS.md` 过程文档隔离规则存放于本地忽略目录，不提交入库）。

### 代码分层归类与 P0 / P1 / P2 判定清单

- **A 类 · 生产核心代码（零 P0 / 零 P1）**：`engine/python/*.py`（不含 `test_*.py`）、`engine/*.json`、`engine/{agents,prompts,patches}/`、`internal/**/*.go`（不含 `*_test.go`）、`cmd/**/*.go`（不含 `*_test.go`）、`runtime_assets.go`、`lint_gate_test.go`、`web/official-graph/`（不含 `verify.mjs`）、生产发版/守护/门禁脚本（`scripts/build_release.py`、`dev.py`、`dev_recovery.py`、`uninstall.py`、`disk-space.py`、`task.py`、`lint.py`、`check.py`、`utm_guard.py`、`go.sh`、`install-hooks.sh`、`local_prepare.py`、`mygo_sdk.py`、`build_*_plugin.py`、`bootstrap.py`）、`.githooks/*`。
- **B 类 · 测试、验收驱动与开发夹具（仅 4 类真风险可定 P1）**：`**/*_test.go`（`lint_gate_test.go` 除外）、`**/test_*.py`、`scripts/test_*.mjs`、`runtime_assets_test.go`、`scripts/verify_setup_cli.py`、`scripts/smoke_release_contracts.py`、`scripts/utm_isolated_env.py`、`scripts/probe_*.py`、`scripts/bench_scaling.py`、`scripts/processing-timings.py`、`scripts/regress-project-queue.sh`、`web/official-graph/verify.mjs`。
- **C 类 · 文档与规范**：`docs/**/*.md`、`AGENTS.md`、`CONTEXT.md`、`README*.md`、`.agents/skills/**`、`.agent/skills/**`。

| 级别 | A 类生产核心代码 | B 类测试与验收脚手架 | C 类文档与规范 |
| --- | --- | --- | --- |
| **P0（硬阻断，不可豁免）** | 安全/ACL/凭据泄露；破坏 `.local/daily` 或清空 `build/`；违反 Cognee 零侵入红线；数据丢失/死锁/孤儿进程失控；编译或主流程不可用；违反 `ML001–ML022` | 破坏宿主运行环境或登录态（误删 `Trae CN`/`Memlane` 目录或日常库）；泄露凭据到日志/仓库；硬编码吞失败致明确假绿 | 引导违背红线的危险命令；正式文档违规引用本地过程目录（触发 `ML018`） |
| **P1（阻断本轮）** | 真实场景可复现的功能正确性缺陷、并发/状态机竞态、协议/退出码偏差、资源/WAL 泄漏、错误吞噬、模型参数补默认值或丢显式 `0`、画像总超时（`ML006`） | **仅允许 4 类且必须给出真实（非合成 mock）触发路径**：① **假绿风险**（断言缺失/永真/过度 mock）；② **掩盖真实产品缺陷**（吞非零退出码/测错版本或二进制）；③ **环境污染/状态串扰**（未隔离 `$TMPDIR`/`HOME`/残留进程）；④ **凭据泄露/越权写入** | 接口/参数/退出码文档与现行实现直接矛盾且会误导调用 |
| **P2（记录不阻断，禁拉新轮）** | 命名风格、可读性、非热路径微优化、极端不可达防御分支 | **一律强制归 P2**：仅合成 mock 序列才出现的轮询/重试边角；对上轮新增防御代码的“防御套防御”次生挑刺；测试 helper 重构或文案润色 | 措辞润色、排版微调 |

> **主控预筛降级权**：审稿人对 B 类文件提 P1 但未勾选 ①–④ 且未给真实触发路径的，主控汇总时直接降级为 P2 并记入账本，不下发修复。

### 「2 轮修复 + 第 3 轮主控裁决（2+1）」收敛协议

1. **Pre-Review Gate**：主控核对切片 ≤40 文件、初始化「已确认契约例外账本」、确认 `scripts/check.py --changed` 已绿并锁定 `R1_BASE_REF`。
2. **Round 1（双路独立全量对抗审查）**：双路只读审查 `git diff <slice_base>..HEAD` + 契约摘要 + 例外账本；**审稿人严禁跑全量测试**（单次核实命令 ≤10s）。主控去重汇总、剔除例外账本项、降级不合格 B 类伪 P1、将新增设计取舍立即入账，一次性下发全部确认 P0/P1 修复。零 P0/P1 直接 `CONVERGED`。
3. **Round 2（增量 diff 定向复审 + 单路已 `APPROVE` 免唤醒）**：仅审查 `git diff <R1_END_REF>..HEAD` 与 R1 问题闭环回执，强制携带更新后的「已确认契约例外账本」，严禁翻案例外账本、全仓重扫或对 B 类防御代码递归追边角。
   - **按域分工双审**：某路 R1 已 `APPROVE` 且增量 diff 未触及其负责域与共享跨层契约文件 → **免唤醒**；
   - **同域全量双审**：一路 R1 已 `APPROVE` 且增量 diff 仅改 B 类脚手架或 ≤3 处无契约变更的局部补丁 → **仅唤醒原 `REQUEST_CHANGES` 一路**；仅当增量 diff 改动 A 类生产核心代码的接口/状态机/并发/安全路径时，才增量复审已 `APPROVE` 一路。
4. **Round 3（主控终局裁决，硬上限，严禁进入 R4–R7）**：
   - **情况 A（仅剩 B 类脚手架边角、P2 争议或契约口径分歧）**：主控结合实测直接裁决收口并写入账本，禁止再拉起审稿人。
   - **情况 B（仍存在确凿 P0 或 A 类生产 P1）**：**既不可豁免 P0，也不可豁免确凿 A 类生产 P1**。不开开放式 R3 全量复审；主控针对该单点精准修复 + 可执行红绿复现验证 + 冻结门禁收口。若**同一具体生产缺陷连续 2 轮审核修复仍不收敛**，停止打补丁并升级底层设计重审。
5. **与「四次失败停止规则」的正交边界**：2+1 上限仅收敛静态代码审核轮次，绝不替代实测验收或抵消下文同一具体缺陷的 4 次验收失败停止计数。

## 同一问题反复验收失败的停止规则

用户2026-11-11明确要求：同一问题如果超过3次验收未通过，可能是产品设计的问题，不用一直尝试，记录下来汇报。

2026-11-12用户纠正：不同类型的问题不能累计成同一问题4次，并要求同步修改规则。以下按具体问题计数，不按整项交付的失败总数计数。“连续4次”不再表示连续4轮整体验收失败；四轮分别暴露不同缺陷时，各缺陷分别计数，不触发整项交付停止。

计数示例：四轮分别发现布局、复制、焦点和来源权限问题，各问题分别记1次，不触发“四次失败停止”。同一具体缺陷即使中间穿插其他问题，累计第4次失败仍须停止该缺陷；停止范围限于该问题及确实依赖它的工作。

- 同一问题累计第4次验收仍未通过，即停止继续针对该问题的实现尝试、实验和验收重跑，先记录并向用户汇报；不自动开始第5次。新操作ID、包版本、运行路径、Agent或错误表述变化不能重置同一问题的累计次数。
- **同一问题的识别**：记录触发条件、失败现象、验收标准及证据；围绕同一缺陷的多次修复失败继续累计。整个Goal、页面、功能、包或测试命令不作为问题身份；“都属于UI”“整包又失败”不足以合并计数。根因未知不等于所有失败同源，也不得仅因换了根因假设就拆分同一缺陷。
- **不同问题分别记录**：验收夹具／方法错误、修复引入的独立兼容回归、不同页面的布局缺陷等，若触发条件、失败现象及修复对象不同，分别追踪；夹具错误不计为被测产品缺陷又失败一次。若后续证据证明同源，合并时去重同一验收轮次；若发现误合并，保留原记录、归类依据与纠正过程，不删除失败证据。
- **触发停止前核对**：逐轮核对四次是否确实验证同一具体问题。报告必须给出关联依据；不能仅列四个失败的命令或窗口就宣布达到阈值。分类未明先查现有证据，不擅自扩大停止范围。
- 不直接认定产品设计错误。检查产品设计、技术方案、实现和验收方法是否存在问题，区分已证事实、假设与未知。
- 汇报至少包含：原目标／验收标准、累计失败次数、每次版本与结果及证据、已经尝试的方案、未证实的原因、影响与保留数据、设计或方案疑点、待用户决策项。
- 停止后可整理已有证据、执行必要安全收尾，并推进不依赖该问题的其他工作；不得以“诊断”“换路径”“换模型”名义继续同一问题的尝试。等待用户明确后续方向后再恢复，原失败记录和计数保留。
- 次数指针对同一问题的验收轮次；同一轮的多个断言或定向单测不重复计次，也不能用拆分、改名或合并记录掩盖已失败的验收轮次。

## Go与MyGo

工具链固定Go 1.27.4、MyGo v0.7.4，以go.mod/go.sum和scripts/go.sh为准，不改系统默认环境。当前锁版本静态检查采用该Go工具链的gofmt与go vet；尚未引入第三方lint，不能写成已配置golangci-lint。

改动Go文件先gofmt，开发与审改轮优先运行 `python3 scripts/task.py -- python3 scripts/check.py --changed`（或 `sh scripts/go.sh vet ./受影响包` 与定向测试）；冻结后单一全量集成门禁为 `python3 scripts/task.py -- python3 scripts/check.py --full`（覆盖全仓 `lint.py`、`sh scripts/go.sh vet ./...`、`sh scripts/go.sh test -count=1 ./...` 及并行 Python 单测）。有并发所有权变更时对受影响包运行 `--race-pkg` 或 `go test -race`，不重复无关全矩阵。

UI组件保持稳定Key，异步结果核对项目／配置代次和当前操作。任务归daemon所有；UI动效只表达真实状态。布局用实际可用宽度，长正文独立滚动、操作可达；保存与启用、知识与原文分离。实际普通／窄窗口、焦点、草稿、返回路径仍须Native验证。

## 首装发版门禁

1. **打包产物双探针 + Level 0 本地 30 秒无 Mock 契约冒烟。** 每次构建候选包并验签后，直接运行 `.app` 内的 `--check-first-store-resources`，在临时工作目录用隔离的 Python 进程检查包内运行资源、首装依赖加载和私有文件写入；同时用 `verify_packaged_cli_doctor` 在隔离临时 `HOME` 运行 `Memlane doctor --json`，要求退出码 `10`、`schema_version == 1`、`stderr` 为空、输出无凭据泄露且耗时小于 5 秒。紧接着默认执行 Level 0 本地无 Mock 契约冒烟（[契约冒烟脚本](../../scripts/smoke_release_contracts.py)，亦可通过 `python3 scripts/task.py -- python3 scripts/check.py --release` 触发）：在 resolved `$TMPDIR` 下以 `python -E -s -B` 启动真实控制面与真实二进制，配合配置路径校验与 `lsof` 隔离守卫断言零触碰日常库与真实 `HOME`，覆盖冷态基线、慢初始化 `--wait` 超时与锁竞争、未登录 CLI `/v1/generation` 识别、TRAE 6→4 迁移计数、`pending-ops` 分类及优雅停服/卸载联动。退出码契约：`0` 通过、`1` 产品契约失败、`2` 运行前提缺失、`3` 隔离守卫违规；[构建脚本](../../scripts/build_release.py)对任意非零退出（`1`/`2`/`3`）一律 fail-closed 阻断校验和产出与进 UTM，`--skip-contract-smoke` 会显式记录 `status="skipped"` 且发版门禁拒收 `skipped`。
2. **双场景 / 三快照 UTM Level 1 自动化门禁（Codex + TRAE CN，纯 CLI 非交互）。** 每个发布候选记录提交与 `.app` 可执行文件 SHA256。所有 UTM 执行先经[环境守卫模块](../../scripts/utm_guard.py)并发运行 3 秒 Fail-Fast 前置探针（DNS/代理/磁盘/宿主登录态 JWT），任一不通过立即以 `exit 31` 中止（明确归属环境阻断，不计入「同一产品缺陷 4 次失败停止」计数）；同时受硬编码不可触碰保护白名单与运行前后哨兵 `HostStateSentinel` 守卫，任何误删或截断宿主登录态/业务数据立报 `exit 32`；同版本热复用校验可执行文件流式 SHA256，版本不一致报 `exit 33`。按以下两组场景验收：[驱动脚本](../../scripts/verify_setup_cli.py)
   - **场景 1 · 净室首装与已有第三方 Hooks。** 在 `clean-codex-traecn` 和 `dirty-hooks-preexisting` 快照分别执行 `doctor --check-network --json` 确认 DNS 正常（I-06）。从 `install --plan` 取得摘要后，执行 `install --apply --plan-digest <PLAN_DIGEST> --wait 45` 启动安装，若返回 `20` 则按 `retry_after_ms` 用 `doctor --operation-id <id> --json` 轮询至组件与服务就绪，要求 `verify --stage setup` 返回 `0`。对 Codex 和 TRAE CN 分别执行 `connect --dry-run` 预览与 `--apply` 接入，检查 TRAE CN 的 `hooks.json` 严格采用 `SessionStart`、`UserPromptSubmit`、`PostToolUse`、`Stop` 四个事件，清除空键并保留第三方配置。真实新会话前，两者的 `verify --stage connection` 均须返回 `11`。
   - **场景 2 · 半装中断恢复与 `beta.2` 升级自愈。** 从 `clean-codex-traecn` 快照启动安装，在 worker 开始后用 `SIGKILL` 终止父 CLI，确认经 `Setsid` 分离的后台 worker 持锁续跑、无孤儿 `uv`，且 `verify --stage setup` 返回 `0`。在 `stuck-beta2-upgrade` 快照中，以 `beta.3` 覆盖安装到仍运行 `beta.2` 后台的环境，确认 `doctor` 检出 `stale_version_detected=true` 与 `components.complete=true`。依次预览并执行 `repair stale-daemon`、`repair pending-ops`（证据充分的 409 解锁，歧义样本返回 `40` 并保留回执）、`repair trae-hooks`（6→4 事件原子迁移并清理空键）。要求 `verify --stage setup --expect-zero-downloads --expect-runtime-current` 返回 `0`，确认零重下载且受管 `graph_assets` 已切换。最后用 `stop --apply --owner-fingerprint <OWNER_FINGERPRINT>` 优雅停服，确认服务锁释放且 `scripts/uninstall.py` 预览通过。
3. **Level 2 真机 / 实窗闭环验收（拆分为 Level 2a / Level 2b，严禁用已初始化热库跳过首装向导）。**
   - **Level 2a（发版必测 · 首装向导与净室建库）**：在候选 DMG 经浏览器下载并触发 Quarantine 后，留证 Gatekeeper 放行，并在**未初始化空库 + 组件金仓（如 APFS 克隆免 222 MiB 重复下载）**下留证浅、深色两套 Native 外观的三步首装、净室初始化核对库标识、官方初始化回执、`0600` 属主令牌与运行配置，退出并重启后确认沿用同一库与属主凭据。
   - **Level 2b（实窗总览与真实宿主会话）**：复用 Level 2a（或非发版迭代轮 Level 1 隔离热后台）完成浅、深色总览与托盘实窗检查，并分别留证 Codex 和 TRAE CN 的真实宿主验证（含带工具调用的新会话、实际模型验证与跨会话记忆召回；TRAE CN 须用 `⌘Q` 完全退出并重开，确认全局 Hooks 已开启）。Pi 的应用接入和真实模型响应分别记录。发版要求打包双探针 + Level 0 + Level 1 自动化 + Level 2a/2b 真实宿主与模型验收全部通过。

## 留证契约

Codex App Server 宣布在同一回合重试时，模型代理等待该回合最终结果，并记录安全错误码与重试标记。见[Codex 代理提前结束可重试的断流](defect-postmortems.md#2026-11-22-codex-代理提前结束可重试的断流)。

### 模型参数契约（2026-11-18，用户明确要求桥接与直连都防复发）

- 静态门禁：`python3 scripts/task.py -- python3 scripts/lint.py`（亦可经统一门禁 `python3 scripts/task.py -- python3 scripts/check.py --changed / --full` 触发），覆盖 Python 与 Go 生产模块（含 `scripts/*.py` 与正式 `docs/`），执行 `ML001`–`ML003`、`ML006`–`ML022`（其中 `ML006` 守卫画像无固定总超时、`ML015` 扩展覆盖 `scripts/*.py`、`ML018` 守卫正式文档隔离、`ML019`–`ML022` 守卫扩展架构边界），并由 `sh scripts/go.sh test ./...` 经 `lint_gate_test.go` 恒常运行 `lint.py` + `test_lint.py`（零跳过开关）；违规输出文件／行号／ML规则编号并非零退出。检查器回归：`python3 scripts/task.py -- python3 -m unittest discover -s scripts -p test_lint.py`。覆盖原始 `setdefault` 错误、别名构造器、请求字面量、合法显式配置及语法错误；不能只在测试里保留架构约束而不接 lint 入口。

- Memlane 未配置的采样/输出参数保持未配置，不补成显式0、1或其他默认值；不得把SDK已有默认行为改成客户端显式参数。桥接模式不开放温度等不受支持字段；直连用户显式填写的0必须保留。
- 启动、验证、在线切换统一使用generation_config中的native_llm_configuration/native_llm_kwargs，不在消费者中直接构造LLMConfig，不继承前一配置的环境变量、llm_args、fallback或凭据。
- 修改配置编译、原生客户端构造、模型验证或切换逻辑时，必须通过test_generation_parameters、test_generation_probe、test_generation_handoff、test_generation_startup。覆盖桥接/直连、未配置/显式0/显式上限、旧环境残留及真实原生协议请求体；不能用stub替掉共享构造器冒充通过。
- 相关复盘和累计失败证据见处理配置接续。真实模型调用、合成协议、Native实窗分别留证；SDK参数拒绝时检查原错误，不能把持续重试误报为模型慢。

UI动作→IPC请求→后台任务→模型网关各层关联操作ID／任务ID与阶段。保留失败阶段、固定安全原因码、HTTP状态、配置代次及耗时；前置拒绝也需要明确可观测边界，不能只记录成功或模型启动后的错误。

错误链仅保留允许的结构化安全码，不复制raw Error、正文、schema、认证、用户路径或provider响应。每层记录容量上限和准入规则；未知保持unknown。请求观察数不是模型次数，completion标签不证明启动；Process缺失不证明已回收。安全外层错误码和内部有限原因链分别记录，不由时间长短猜根因。

## Harness复用

通用验收设计、证据审阅与缺陷归类方法已抽到 [verify-continuous-scenarios](../../.agents/skills/verify-continuous-scenarios/SKILL.md)。本项目命令编排见[run-scenario使用说明](../archive/scenario-runner.md)，复用task.py磁盘门禁。现有具体驱动按任务逐步接入，不把新入口描述为全部Native验收已统一；失败阈值及停止范围继续按上文执行。

优先复用现有 MyGo Tester／headless 组件测试（`internal/simpledesktop/*_test.go`、`perf_budget_test.go`）、受控图谱验证（`web/official-graph/verify.mjs` 与 `-tags memlane_graph_e2e` 调试入口）、IPC 与实际窗口核验，不搭另一套 UI 引擎（早期 `scripts/verify-window.py` 已随旧导航精简移除）。普通包不能携带 e2e 探针。

每个驱动须校验synthetic_only标记、bundle/root、实际可执行文件与版本；输出阶段结果和证据，超时不得静默重跑收费任务。数据库快照与进程退出分别验证；不要把无窗口capture当实际Native截图。当前统一Harness尚未完成，见清单。

## 真实缺陷防复发要点（用户 2026-11-12 要求）

- **缺陷建档与官方组件优先**：真实缺陷修复后按[复盘模板](templates/incident-template.md)记录症状、触发条件、已证根因、修复边界、回归证据与未完成项，并按日期倒序合入[缺陷复盘合集](defect-postmortems.md)（追加同一问题沿用原编号与累计次数，明确区分已修复、配置规避、原因未知、风险防护与待决策项，不把未验证推测写成根因）。UI 弹框、菜单与表单优先复用固定 MyGo SDK 官方成品组件；使用 Base 须明确缺口并封装后经实际 Native 视觉核对。
- **投影一致与后台刷新不变量**：同一接口的 SQLite 投影与官方 HTTP 回退必须共用文档分类与排除规则并分别验证待提炼候选（见[用户画像误计为项目待提炼](defect-postmortems.md#2026-11-22-用户画像误计为项目待提炼)）。已有同范围、同来源的已核验数据时，后台轮询不得卸载正文、清空列表数量或切回整块加载页；但收到权限撤销、版本变化、来源缺失或读取失败回执后须立即撤下失效正文与复制入口（不能借「防闪烁」保留失效内容），并对项目/来源切换与过期异步回调执行身份与代次隔离；回归须同时覆盖首次加载、连续刷新保位与失效撤权。
- **隔离边界、密码释放与切换接续**：真实 SDK 验证须使用独立子进程 `HOME`/`cwd`、显式日志与缓存根并排除凭据（SDK 导入可能带文件副作用，不能仅靠临时数据库根替代隔离）；清空密码等敏感输入须同时核对业务字段、画面与真实 Native 辅助功能值（视觉遮罩不证明底层输入控件已释放）；历史处理切换的 `applied` 只证明当时应用，恢复与整组重启须同时核对当前运行配置并接续已应用但管理侧未确认的同一操作。
- **宿主接入、模型验证与积压队列底线**：新宿主工具记录须复用官方 trace 构造器与字典参数契约、保持写入 `node_set` 与召回 `scope` 一致，原生安装/升级后核对实际 cache 摘要与旧代兼容，正常退出或采集事件回执不能替代队列排空与新会话采用；实时策略明确拒绝（权威 403）与传输/策略未知必须分别返回、互不混淆；宿主发现与模型认证分离，重开设置应读取已保存原验证结果，验证模型须核对完成状态与真实请求模式且新失败/未知不可被历史成功遮盖，小 Schema 探针不替代官方知识质量，精确文本断言须用无歧义样本；待采集总数是动态存量，操作回执不被轮询擦除，不以最近窗口首条改写会话标题，用户确认后的冻结积压由后台持有并持续推进（隔离未知项、不把底层单批预算结束当交付、UI 重开不重投、停止等待在途回执、后台重启不自动续投）。
