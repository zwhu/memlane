# Memlane 开发指南

Memlane 是面向程序员的 macOS 记忆客户端，采用 **Go 单二进制（MyGo Native 桌面端 + 本地接入网关与守护）+ 固定版本官方记忆引擎（Cognee `1.6.7`）**。核心领域术语与分层定义见 [CONTEXT.md](CONTEXT.md)，现行硬架构边界见 [架构边界契约](docs/architecture/boundaries.md)。

## 1. 基础架构

全景图与详细分层见 [架构总览](docs/architecture/overview.md)（[架构图 SVG](docs/images/memlane-architecture-20261126.svg)）：

- **宿主 Coding Agents（方案 C 接入层）**：Codex、Antigravity / Gemini App、pi、TRAE CLI / TraeX、TRAE CN（IDE）及长尾 MCP 宿主；按电脑级受管主体接入，各自持有独立令牌（电脑级准备不授任何项目权）。支持 Shell Hook 的核心宿主统一调用 Go 子命令 `memlane hook`（`core/hooks`），无 Shell Hook 的 `pi` 保留极薄无状态转发扩展（`engine/agents/pi/`），长尾宿主统一接入网关原生标准 `/mcp`（`core/gateway/mcp`）；第三方 Cognee 插件冲突执行三级检测策略（`kit/agents`）。
- **Go 单二进制入口（`cmd/memlane/`）**：收口为单一多模式二进制：`memlane app`（桌面端）| `memlane daemon`（M1 保活后台、接入网关、模型代理与进程组守护）| `memlane hook`（宿主事件无状态转发）| `memlane setup`（零 Skill Setup CLI：`doctor`/`install`/`connect`/`verify`/`repair`/`stop`）| `memlane dev`（开发态 `start`/`status`/`open`/`stop`/`recover`）。
- **MyGo 原生桌面端（`app/`）**：`1200×820` 客户端，主界面与托盘弹层（`app/tray_flyout.go`）为纯 MyGo 原生自绘，仅图谱画布使用沙箱 `mygo.WebView` 加载受控内嵌官方图谱静态资产（`web/official-graph/`，无凭据/无直连网络）；拆分为 `app/` 组合根 + 6 个无环 UI 子包（`app/shell`、`app/overview`、`app/reading`、`app/search`、`app/graph`、`app/settings`）及统一传输包 `app/api`，通过本地网关接口直连读取，不落盘缓存正文副本。
- **零业务通用底座（`kit/*`）**：`kit/ui`（MyGo 组件、双外观令牌与动效原语 `motion_tokens.go`）、`kit/agents`（本机宿主发现、受管配置读写、三级 Cognee 冲突检测与 `kit/agents/bridge` CLI 模型代理）、`kit/aiinstaller`（Agent-First 依赖自举与健康诊断，含 `kit/aiinstaller/cli`）、`kit/daemon`（`.service-owner` flock 单实例独占锁、`Setsid` 进程组守护、20 MiB 活 inode 日志截尾与 `.previous` 脱敏、防抖健康检查及 M1 保活/退出策略，崩溃不静默自启）。`kit/*` 严禁依赖 `core/*`、`app/*` 或 `internal/*`。
- **Memlane 领域与接入网关（`core/*`）**：`core/domain` 管理极简两层模型（`Capture → Memory`）、项目注册表、主体 ACL、目录投影与单点独占写入的私有 `0600` SQLite WAL（`memlane.sqlite3`，强制 `PRAGMA journal_size_limit`）；`core/engine` 暴露引擎无关 6 原语接口 `MemoryEngine`（1 号实现 `core/engine/cognee`）；`core/gateway` 提供 `/api/*`（兼容 `/v1/*`）、`/ingest`、`/recall`、`/mcp` 与 OpenAI 兼容 Agent 模型代理（`core/gateway/completion`），执行项目 ACL、召回合并通用记忆、异步收件箱提炼与三层监控；`core/profiles` 负责各 AI 来源独立用户画像导出与存储；`core/hooks` 与 `core/setup` 承担方案 C Hook 转发、清单与品牌常量。
- **无状态记忆引擎薄壳（`engines/cognee/`，~2.6k LOC，`ML023` 限额 ≤3,500）**：仅保留直接依赖 `cognee / litellm / ladybug` 的薄壳适配器，零业务状态、禁导 `sqlite3`/`aiosqlite`/`engine`。启动前经 `engines/cognee/runner.py` 的 `verify_source` v2 缓存（逐文件 `O_NOFOLLOW` SHA-256 + `_SourceOnlyFileLoader` 禁止源码目录存在 `.pyc`/`.pyo`）校验固定上游 Cognee `1.6.7` (`ec6f408`) 源码；正文、向量（LanceDB）与图谱（Ladybug）唯一真相保留在 `.local/daily/official-store`。提炼推理支持「Agent 模型代理（`agent_bridge`）」与「直连 LLM Provider / Ollama」并存，运行期由唯一 `active` 决定。

## 2. 运行方式

| 用途 | 命令 |
| --- | --- |
| 启动日常服务与 UI（默认配置 `.local/daily/runtime.json`） | `sh scripts/go.sh run ./cmd/memlane dev start --config /绝对路径/runtime.json` |
| 仅启动后台 / 查看状态 / 重开窗口 / 停止 / 异常恢复 | `sh scripts/go.sh run ./cmd/memlane dev {start --no-ui \| status \| open \| stop \| recover} --config /绝对路径/runtime.json` |
| 统一改动感知 / 冻结门禁（开发与审改轮用 `--changed` / `--staged`；源码冻结后打包提交前单一节点用 `--full`；候选发版含 Level 0 契约冒烟用 `--release`） | `python3 scripts/task.py -- python3 scripts/check.py {--changed \| --staged \| --full \| --release}` |
| Go 全量测试（含 `arch_guard_test.go` 分层依赖与 WAL 限额守卫、`app/perf_budget_test.go` 冷启动首页 `<150ms` / 帧耗时 `<12ms`/`<16ms` / 切页堆内存 `<=25 MiB` 及 `lint_gate_test.go` 恒常运行 `lint.py` + `test_lint.py`）与静态检查 | `sh scripts/go.sh test ./...`、`sh scripts/go.sh vet ./...` |
| 零依赖静态检查（`ML001`–`ML003` 模型边界、`ML007` 环境隔离、`ML008`–`ML012` UI 性能与首屏聚合、`ML015`–`ML016` 大文件流式哈希与 WAL 限额、`ML018` 正式文档禁引 `docs/process/`、`ML023` 无状态 `engines/cognee` 边界与 `<=3,500` LOC 上限；`go test ./...` 自动强制执行） | `python3 scripts/task.py -- python3 scripts/lint.py` |
| 增量架构与防过度防御门禁（`DG001`–`DG002` 静默吞错、`DG003` 临时补丁注释、`DG004` 骨架锁/白名单、`DG005` 冻结非 Cognee `.py` 新增、`DG006` Go 分层 import 红线；接入 `check.py` 与 `pre-commit`） | `python3 scripts/task.py -- python3 scripts/diff_guard.py` |
| 引擎薄壳与 Scripts 回归（`scripts/install-hooks.sh` 仅启用局部 `.githooks` 暂存态 `pre-commit` + 调用 `check.py --pre-push` 的 `pre-push` 并全阶段透传全局钩子，不改全局 `~/.gitconfig`） | `python3 scripts/task.py -- env PYTHONPATH=. .local/cognee-python/bin/python -m unittest discover -s engines/cognee -p "test_*.py"`、`python3 scripts/task.py -- python3 -m unittest discover -s scripts -p "test_*.py"` |
| 模型参数 / 探针 / 切换 / Agent 代理回归（修改模型路径时必跑） | `sh scripts/go.sh test ./core/... ./kit/agents/...` 与 `python3 scripts/task.py -- env PYTHONPATH=. .local/cognee-python/bin/python -m unittest discover -s engines/cognee -p "test_*.py"` |

运行边界：
- **日常启动与工具链**：使用 `sh scripts/go.sh run ./cmd/memlane dev ...` 启动日常服务与桌面端；`runtime.json` 中 `python` 字段仍指向受管 Python 3.12 虚拟环境（供 `engines/cognee` 子进程加载固定版 Cognee 依赖），运行 `engines/cognee` 单测时同样优先使用 `.local/cognee-python/bin/python`。
- **单一日常库**：日常运行、Native 界面与真实链路验收统一连接 `.local/daily/official-store`，不另建独立测试库；未核对前不删除原材料或运行依赖。
- **磁盘门禁与目录保护**：开发态受管任务经 `scripts/task.py` 执行，至少保留 `50 GiB` 可用空间（运行中低于 `55 GiB` 自动停止）；独立 `.app` 安装态监控阈值为最低 `2 GiB` / 告警线 `5 GiB`。**禁止清空 `build/`**（2026-11-11 曾因清理旧构建误删仍被运行中 helper 依赖的完整 `.app`，见[运行包误清理复盘](docs/engineering/runtime-package-cleanup-incident-20261111.md)；`scripts/disk-space.py` 依赖 `build/disk-cleanup/` 的保留清单与回执）。清理旧运行包前必须先核对 daemon/helper 所属完整包与代理依赖，依赖未知则保留。
- **隐私与交付范围**：实际运行配置、凭据与真实记忆库位于 Git 跟踪之外的 `.local/`；不提交密钥、真实库、聊天记录或本机 Agent 配置。除非用户明确要求，不构建 DMG 或执行签名、公证、更新与 helper 安装。
- **提交身份**：本仓库所有提交（含 cherry-pick、rebase、标签）身份固定为 `zwhu <331946763@qq.com>`，只写仓库级配置、不改全局 `~/.gitconfig`。独立副本、Agent 工作区或新克隆在首次提交前先执行 `sh scripts/install-hooks.sh`（写入仓库级 `user.*` 并启用 `.githooks/_identity_guard.sh`：`pre-commit` 校验当前身份，`pre-push` 拒绝推送任何身份不符的提交或标签）。
- **过程文档定义与隔离（`docs/process/`）**：「过程文档」指仅服务于当次需求探索、调试排查、评审或验收留证、不构成项目长期基线的全部临时产物——无论格式是文档/草稿、图片/截图（PNG/JPG/WebP/GIF）还是录屏视频（MP4/MOV/WebM）与实验原始报告（判定口诀：任务完成后删掉不影响理解当前系统设计、契约或长期复盘）。所有过程文档一律放入 `docs/process/` 子目录（`.gitignore` 已忽略，仅保留 `docs/process/README.md` 入库），不得散落到 `docs/design/`、`docs/images/` 或源码目录；临时编译产物（`.app`、可执行二进制、源码覆盖拷贝）一律走系统 `$TMPDIR` 并在退出时清理，不进 `docs/process/`。单次命令超时、环境插曲与待重启提示仅在会话汇报，未定稿内容禁止提前写入正式 `docs/`；长期生效的规范、架构契约、专题调研、防复发复盘与正式文档直接引用的少量静态图表才分别入库对应 `docs/` 分类目录与 `docs/images/`，且正式 `docs/` 必须自包含、严禁引用 `docs/process/`（由 `ML018` 与 `pre-commit` 自动拦截），定稿晋升后顺手清理同主题原始日志（完整边界见 [docs/process/README.md](docs/process/README.md)）。
- **正式文档生命周期与清理标准**：坚持「**无 `docs/archive/`、唯一事实源、淘汰即删、变更必同步**」——已被新契约/新规范取代的旧文档、已完工的阶段实施计划与一次性基线快照、以及被替换的旧静态图表一律直接 `git rm`（历史靠 Git 追溯，严禁囤积 `docs/archive/`）；目录、命令、分层或门禁变更时须同步对齐 `AGENTS.md`、`README*.md`、`CONTEXT.md`、`docs/README.md` 与 `docs/architecture/`（架构图保持可流动 SVG 并渲染 PNG 目视复核）；防复发复盘、现行产品/设计规范与仍具前瞻价值的 `docs/research/` 严禁误删；每次清理须通过全仓 `.md` 零断链校验与 `lint.py`（`ML018`）门禁（完整细则见 [工程规范 · 文档生命周期与清理标准](docs/engineering/workflow.md#文档生命周期与清理标准2026-11-26-起执行)）。

## 3. 文档路由

全仓文档总索引见 [docs/README.md](docs/README.md)，按任务类型查阅对应基准文档：

| 任务领域 | 查阅文档 |
| --- | --- |
| **产品定位、内容分层与演进** | [产品定义](docs/product/product.md)（含内容分层）、[Roadmap](docs/product/roadmap.md)、[内测安装指南](docs/product/beta-install-guide.md)（`v0.1.0-beta.3`） |
| **系统架构、硬边界与开发运维** | [架构总览](docs/architecture/overview.md)、[架构边界契约](docs/architecture/boundaries.md)（含方案 C 接入与三级冲突策略）、[开发说明](docs/architecture/development.md)（含国内镜像源与手工终端兜底）、[有效模型契约](docs/architecture/agent-effective-model-contract-20261118.md) |
| **界面交互、V4 视觉与动效** | [视觉与交互规范 V4](docs/design/visual-standards-v4-20261120.md)、[MyGo 动效契约](docs/design/motion-implementation.md)、[V4 可交互原型](docs/design/visual-v4-prototype/)、[首次启动向导原型](docs/design/onboarding-prototype/) |
| **专题技术调研** | [安装依赖瘦身调研](docs/research/install-dependency-slimming-20261125.md)、[Docker 部署架构调研](docs/research/docker-deployment-architecture-20261125.md)、[首屏 20× 性能调研](docs/research/native-first-screen-performance-20261122.md)、[多 Agent 驱动协议](docs/research/agent-driving-protocols-20261118.md)、[Hindsight 审计](docs/research/hindsight-vs-memlane-20261117.md)、[macOS 更新调研](docs/research/macos-updater-20261119.md)、[TRAE IDE 整理调研](docs/research/trae-ide-memory-processing-20261119.md) |
| **工程规范、架构治理与复盘** | [工程规范](docs/engineering/workflow.md)、[架构治理与 Python 冻结](docs/engineering/architecture-governance-and-py-freeze.md)、[Beta 安装问题库](docs/engineering/beta-install-issues.md)、[缺陷复盘汇总](docs/engineering/defect-postmortems.md)、[多 Agent 耗时复盘](docs/engineering/agent-workflow-efficiency-retrospective-20261125.md)、[运行包误清理复盘](docs/engineering/runtime-package-cleanup-incident-20261111.md)、[任务说明模板](docs/engineering/templates/task-brief-template.md)、[用例模板](docs/engineering/templates/testcase-template.md)、[双审与契约例外账本模板](docs/engineering/templates/review-ledger-template.md)、[复盘模板](docs/engineering/templates/incident-template.md) |
| **过程文档与验收媒体（本地忽略）** | [过程产物目录](docs/process/README.md)（独立存放截图、录屏视频、草稿与验收过程文件，已配置 `.gitignore` 不入库） |

## 4. 设计原则

- **全局最优决策**：从全局出发思考当前最优解，不被过时文档约束；若最优解与旧文档冲突，先提出最优解并标明冲突所在与原因，经用户确认后同步更新相关文档。
- **Go 单二进制与分层硬边界**：业务、网关、单点 SQLite（`memlane.sqlite3`）、Agent 模型代理、宿主 Hook 转发与开发启停统一收口至单一 Go 二进制（`cmd/memlane`）；`kit/*` 零 Memlane 业务依赖，`core/*` 不依赖 UI，除 `core/engine/cognee`、`core/engine` 工厂与 `cmd/` 外全仓不感知 Cognee 专有实现；上述边界由 `arch_guard_test.go`、`DG005`/`DG006` 与 `ML023` 机械强制守护。
- **Cognee 零侵入红线**：严禁修改上游 Cognee 源码、严禁维护私有 Fork 或本地源码补丁副本、严禁绕过 `engines/cognee/runner.py` 的 `verify_source` v2 逐文件 SHA256 校验（同时禁止源码目录存在 `.pyc`/`.pyo`，采用源码直编加载），也不得在运行期对 Cognee 内部模块、路由或管线做无契约 monkey-patch。`engines/cognee/` 保持无状态薄壳（`<=3,500` LOC、禁导 `sqlite3`/`aiosqlite`），一切治理状态、ACL 门禁、召回合并、收件箱与溯源落在 Go `core/domain` + `core/gateway`，保证上游 Cognee 升级或未来替换引擎时可平滑演进。
- **极简两层模型与方案 C 宿主边界**：内容流坚持两层（`Capture → Memory`），实时上报入轻量收件箱后异步提炼；宿主接入采用方案 C（核心宿主统一调 `memlane hook`、`pi` 保留极薄转发扩展、长尾宿主走 `/mcp`），坚持电脑级零默认项目授权，并以三级冲突策略保护用户原有第三方 Cognee 插件与无关配置（绝不静默覆盖或删除）。
- **记忆日期口径与唯一真相**：优先取 `core/domain`（`memlane.sqlite3`）按 `(dataset_id, lesson_type, state_key)` 锁定的最早日期（`system_metadata` 优先于用户可编辑 `external_metadata`，GET 目录纯只读无写副作用），其次取原生 `session_date`，缺失时回退 `createdAt` 对应本地日历日（`createdAt` 单独保留）。Cognee 官方存储（`.local/daily/official-store`）为正文、向量与图谱唯一主数据源；通用记忆与用户画像按来源独立保存，不自动合并为通用记忆。AI 提出的合并、覆盖、删除或跨项目移动，在用户明确确认前不得改变已有有效记忆。
- **模型参数与设置契约**：未配置的模型选项保持省略，用户显式填写的 `0` 必须保留；启动、验证与热切换统一使用 `engines/cognee/llm_config.py` 的 `native_llm_configuration` / `native_llm_kwargs`（由 `ML001`–`ML003` 静态守卫），不继承前一配置环境。Agent 模型代理（`agent_bridge`）与直连 Provider 并存时查看、编辑或保存均不激活，仅确认后的服务回执更新唯一 `active`；用户画像导出（`core/profiles`）沿用原 `ML006` 契约，不得按固定总时长超时中断，保存阶段不可取消。
- **桌面界面与视觉验收**：保持 `1200×820` 默认与最小窗口（允许放大），首页宽幅总览、详情页三栏、关键词搜索统一交给全局 `⌘K`、无全局「全部记忆」侧栏入口。UI 改动须对照 V4 规范与原型在浅色、深色两套外观下实际验收。
- **任务分级与反嵌套（T0–T3）**：常规编排严格 ≤2 层，严禁小任务走「主控 → 新建 worker → 套 `codex exec`」多层嵌套。**T0**（≤3 文件、单目录/文档/打包/提交；执行 DMG/签名仅限用户明确要求后直跑）主控单步直做，改 A 类生产代码必跑 `scripts/check.py --changed`（触及安全/ACL/模型参数/守护锁/Cognee 边界自动升格 T1 走单路审）；**T1**（4–15 文件单层）主控或单 subagent；**T2**（≤30–40 文件单垂直切片）按互斥文件并行；**T3**（>40 文件/多子系统）先经 [plan-change-impact](.agents/skills/plan-change-impact/SKILL.md) 拆批逐片合入（详见 [工程规范](docs/engineering/workflow.md)）。
- **双审收敛与测试单一归属**：在 Antigravity（`agy`）中发起 T2/T3 双独立代码审核或 Swarm 审波前，必须读取 `agy` 专属 Skill [dual-code-review](.agent/skills/dual-code-review/SKILL.md)（仅 `agy` 可见）与[审核账本模板](docs/engineering/templates/review-ledger-template.md)——执行 A/B/C 分层 P1 举证门槛、「2 轮修复 + 第 3 轮主控裁决」（R3 不可豁免 P0 及确凿 A 类生产 P1）、增量 diff 携带「已确认契约例外账本」与单路 `APPROVE` 免唤醒，审稿人禁跑全量测试。开发与审改轮统一跑 `scripts/check.py --changed`；全量门禁集中在源码冻结后、打包/提交前由单一节点执行 `scripts/check.py --full`（发版用 `--release`）。
- **工程闭环与协作底线**：跨模块实施与集成验收优先使用 [plan-change-impact](.agents/skills/plan-change-impact/SKILL.md) 与 [verify-continuous-scenarios](.agents/skills/verify-continuous-scenarios/SKILL.md)。同一具体缺陷累计第 4 次验收仍未通过时停止该缺陷尝试并保留证据汇报（与双审 2+1、环境前置失败 `exit 31` 正交，规则细节见 [工程规范](docs/engineering/workflow.md)）。诊断区分已实现与待验证，不将未知模型调用或进程状态推断为零；保留无关修改。文档与交流默认使用中文，动工前用一行说明即将执行的工作，结束时给出可独立读懂的简短小结。
