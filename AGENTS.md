# Memlane 开发指南

Memlane 是面向程序员的 macOS 记忆客户端，采用 **Go 单二进制（MyGo 原生桌面端 + 本地网关与守护）+ 固定版本官方记忆引擎（Cognee `1.6.7`）**。术语定义见 [CONTEXT.md](CONTEXT.md)，硬架构边界见 [架构边界契约](docs/architecture/boundaries.md)，全景分层与数据流见 [架构总览](docs/architecture/overview.md)（[架构图 SVG](docs/images/memlane-architecture-20261126.svg)）。

## 1. 目录结构与职责

| 目录 | 语言 | 核心职责 |
| --- | --- | --- |
| `app/` | Go | MyGo 原生桌面端（`1200×820`）。根包负责窗口装配、托盘与跨页交互；子包拆为 6 个无环页面域（`shell`/`overview`/`reading`/`search`/`graph`/`settings`）及统一网关客户端 `api/`，不落盘缓存正文副本。 |
| `core/` | Go | Memlane 业务核心。`domain/` 管单点私有 SQLite（`memlane.sqlite3`）、项目注册与主体 ACL；`gateway/` 管 `/api/*`（兼容 `/v1/*`）、`/ingest`、`/recall`、`/mcp`、Agent 模型代理与异步提炼；另含引擎无关接口 `engine/`（1 号实现 `engine/cognee`）、`hooks/`、`profiles/`、`setup/`。 |
| `kit/` | Go | 零业务通用底座。`agents/` 管宿主发现、三级冲突检测与 CLI 模型代理（`bridge/`），`aiinstaller/` 管依赖自举与诊断，`daemon/` 管单实例锁与进程组守护，`ui/` 管原生组件、双外观令牌与动效原语。 |
| `internal/` | Go | Memlane 运行编排。`bootstrap/` 管首装与首次启动，`setupcli/` 串联安装/修复流程，另含控制面客户端与守护适配。 |
| `cmd/memlane/` | Go | 单一多模式二进制入口，分发 `app`、`daemon`、`hook`、`mcp`、`setup`、`dev` 六种模式。 |
| `engine/` | Go / TS / JSON | 暴露嵌入运行资源与组件清单、提示词，并存放 `pi` 宿主的极薄无状态 TypeScript 转发扩展（`engine/agents/pi/`）。 |
| `engines/cognee/` | Python | 无状态 Cognee `1.6.7` 薄壳（`ML023` 限额 `<=3,500` LOC），仅含子进程入口、上游源码 `verify_source` SHA-256 校验、引擎 API 与模型配置；零业务状态，禁导 `sqlite3`/`aiosqlite`/`engine`。 |
| `scripts/` | Python / Shell | 工程门禁（`check.py`/`lint.py`/`diff_guard.py`）、磁盘守卫（`task.py`/`disk-space.py`）、工具链包装（`go.sh`）与发版脚本。 |
| `web/` | TS / JS | `official-graph/` 为沙箱图谱 WebView 静态资产（无凭据、无直连网络）；`website/` 为官网源码。 |
| 根目录直属 | Go | 嵌入运行资源与图标，以及分层、性能与 Lint 守卫测试（`arch_guard_test.go`、`lint_gate_test.go`）。 |

## 2. 运行方式与边界

| 用途 | 命令 |
| --- | --- |
| 启动日常服务与 UI（默认 `.local/daily/runtime.json`） | `sh scripts/go.sh run ./cmd/memlane dev start --config /绝对路径/runtime.json` |
| 仅启后台 / 状态 / 重开窗口 / 停止 / 恢复 | `sh scripts/go.sh run ./cmd/memlane dev {start --no-ui \| status \| open \| stop \| recover} --config /绝对路径/runtime.json` |
| 统一门禁（开发与审改用 `--changed`/`--staged`；冻结后单一节点用 `--full`；发版用 `--release`） | `python3 scripts/task.py -- python3 scripts/check.py {--changed \| --staged \| --full \| --release}` |
| Go 全量测试与静态检查（自动联动分层守卫、性能预算与 `lint.py`） | `sh scripts/go.sh test ./...`、`sh scripts/go.sh vet ./...` |
| 静态检查（`ML*`）与增量架构门禁（`DG*`） | `python3 scripts/task.py -- python3 scripts/lint.py`、`python3 scripts/task.py -- python3 scripts/diff_guard.py` |
| 引擎薄壳与脚本单测 | `python3 scripts/task.py -- env PYTHONPATH=. .local/cognee-python/bin/python -m unittest discover -s engines/cognee -p "test_*.py"`、`python3 scripts/task.py -- python3 -m unittest discover -s scripts -p "test_*.py"` |
| 模型路径回归（改模型参数/探针/切换/代理时必跑） | `sh scripts/go.sh test ./core/... ./kit/agents/...` + 上述 `engines/cognee` 单测 |

运行边界：
- **单一日常库与工具链**：日常运行、UI 与真实链路验收统一连接 `.local/daily/official-store`，不另建独立测试库；`engines/cognee` 运行与单测统一使用受管虚拟环境 `.local/cognee-python/bin/python`。
- **磁盘门禁与目录保护**：受管开发任务经 `scripts/task.py` 至少保留 `50 GiB`（运行中低于 `55 GiB` 自动停止；独立 `.app` 阈值 `2 GiB` / 告警 `5 GiB`）。**禁止清空 `build/`**（`scripts/disk-space.py` 依赖 `build/disk-cleanup/`，清理旧运行包前须核对 daemon/helper 所属完整包与配置依赖，依赖未知则保留；复盘见 [11-11 事故](docs/engineering/runtime-package-cleanup-incident-20261111.md)）。
- **隐私、发版与提交身份**：运行配置、凭据与真实库留在 `.local/`，严禁提交；除非用户明确要求，不构建 DMG 或执行签名、公证、更新与 helper 安装。提交身份固定为 `zwhu <331946763@qq.com>`（仅写仓库配置、不改全局 `~/.gitconfig`），新克隆或独立工作区首次提交前执行 `sh scripts/install-hooks.sh`。
- **过程产物隔离与文档生命周期**：临时草稿、截图、录屏与验收日志一律放 `docs/process/`（本地忽略），临时 `.app`/二进制走 `$TMPDIR` 并在退出时清理；单次超时与环境插曲仅在会话汇报，正式 `docs/` 严禁引用 `docs/process/`（`ML018` 拦截）。正式文档坚持「无 `docs/archive/`、唯一事实源、淘汰即 `git rm`、变更必同步」，细则见 [docs/process/README.md](docs/process/README.md) 与 [工程规范](docs/engineering/workflow.md#文档生命周期与清理标准2026-11-26-起执行)。

## 3. 文档路由

全仓完整目录（含全部调研报告与模板）见 [docs/README.md](docs/README.md)，按领域查阅对应基准文档：

- **产品与安装**：[产品定义](docs/product/product.md)、[Roadmap](docs/product/roadmap.md)、[内测安装指南](docs/product/beta-install-guide.md)
- **架构与契约**：[架构总览](docs/architecture/overview.md)、[架构边界契约](docs/architecture/boundaries.md)（含分层红线、方案 C 接入、存储与日期口径）、[开发说明](docs/architecture/development.md)、[有效模型契约](docs/architecture/agent-effective-model-contract-20261118.md)
- **界面与动效**：[V4 视觉与交互规范](docs/design/visual-standards-v4-20261120.md)（含窗口规格、浅/深双外观与交互不变量）、[桌面 UI 护栏](app/AGENTS.md)、[MyGo 动效契约](docs/design/motion-implementation.md)、[V4 原型](docs/design/visual-v4-prototype/)、[首装向导原型](docs/design/onboarding-prototype/)
- **工程与复盘**：[工程规范](docs/engineering/workflow.md)（含 T0–T3 分级、双审 2+1、发版与模型参数门禁、四次失败停止规则）、[架构治理与 Python 冻结](docs/engineering/architecture-governance-and-py-freeze.md)、[缺陷复盘汇总](docs/engineering/defect-postmortems.md)、[双审账本模板](docs/engineering/templates/review-ledger-template.md)

## 4. 协作原则与不可碰红线

- **架构与零侵入红线**：严格遵守 [boundaries.md](docs/architecture/boundaries.md) 的单向分层边界（`kit/*` 零业务、`core/*` 无 UI、引擎隔离）、Python 冻结与 Cognee 上游源码零侵入红线（严禁修改上游源码、绕过 `verify_source` SHA-256 校验、在源码目录留 `.pyc`/`.pyo` 或做无契约 monkey-patch）；具体数据模型、模型参数与界面细则以第 3 节对应契约文档为准。
- **全局最优决策**：从全局出发思考当前最优解，不被过时文档约束；若与旧文档冲突，先提出最优解并标明冲突原因，经用户确认后同步更新文档。
- **论证举证底线**：回应用户的技术观点、直觉或质疑时，先给结论再给依据，禁止空口附和或纯抽象反驳——赞同必举具体正例、反对必举具体反例并标明边界；当前工作区源码或本地运行库可只读核验时，优先以真实数据做控制变量实测对照。
- **任务分级与双审收敛**：常规编排严格 ≤2 层（反嵌套），按 [workflow.md](docs/engineering/workflow.md) 的 T0–T3 选最小层级；在 `agy` 发起 T2/T3 双审或 Swarm 审波前必读 [dual-code-review](.agent/skills/dual-code-review/SKILL.md)（审稿人禁跑全量测试）；开发与审改跑 `check.py --changed`，冻结后单一节点跑 `--full`。
- **工程闭环与停止规则**：跨模块实施与集成验收优先用 [plan-change-impact](.agents/skills/plan-change-impact/SKILL.md) 与 [verify-continuous-scenarios](.agents/skills/verify-continuous-scenarios/SKILL.md)；同一具体缺陷累计第 4 次验收未通过即停止尝试并保留证据汇报（与双审 2+1、环境前置失败 `exit 31` 正交）。
- **诚实沟通与改动保护**：诊断区分已实现与待验证，不将未知模型调用或进程状态推断为零；保留无关修改；默认中文，动工前用一行说明即将执行的工作，结束时给出可独立读懂的简短小结。
