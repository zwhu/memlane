# Memlane 开发指南

Memlane 是面向程序员的 macOS 记忆客户端，采用 **MyGo Native 桌面端 + 固定版本官方记忆引擎（Cognee）**。核心领域术语与分层定义见 [CONTEXT.md](CONTEXT.md)。

## 1. 基础架构

全景图与详细分层见 [架构总览](docs/architecture/overview.md)（[架构图 SVG](docs/images/memlane-architecture-20261120.svg)）：

- **宿主 Coding Agents（接入层）**：Codex、Antigravity / Gemini App、pi、TRAE CLI / TraeX、TRAE CN（IDE），以及保留兼容的 OpenCode、Claude Code & Cursor；按电脑级受管主体接入，各自持有独立官方身份与 `X-Api-Key`（电脑级准备不授任何项目权）。
- **MyGo 原生桌面端（`cmd/memlane/app.go`、`app/`）**：`1200×820` 客户端，主界面与托盘弹层（`tray_flyout.go`）为纯 MyGo 原生自绘，仅图谱画布使用沙箱 `mygo.WebView` 加载受控内嵌官方图谱静态资产（无凭据/无直连网络）；划分为 `overview` / `reading` / `search` / `graph` / `settings` 五个功能包，通过 `http_transport.go` 直连本地服务，不本地缓存正文副本。
- **引导与原生守护（`internal/bootstrap/`、`internal/supervisor/`、`cmd/memlane/quit_policy.go`、`scripts/dev.py`）**：持有 `.service-owner` flock 单实例独占锁、以 `Setsid` 管理独立进程组、20 MiB 活 inode 日志截尾及 `.previous` 凭证脱敏与防抖健康检查；遵循 M1 保活/退出策略（含附着态与首次启动在途受控停服），崩溃不静默自启。
- **接入网关（`engine/python/project_control_server.py`、`cognee_extension_gateway.py`）**：Loopback 接入治理面管理项目注册表、连接编排、采集/提炼队列调度、热切换、三层监控（`/v1/runtime/monitor`）及`/v1` 游标读取与只读 SQLite 目录投影（`v2_api.py` / `catalog_index.py` / `raw_records_read.py`）；`/cognee/*` 零侵入外置网关统一执行 ACL 门禁、召回合并通用记忆、Diff/日期注入与召回打点。
- **记忆引擎（Cognee）与外置存储（`official_service_dev.py`、`.local/daily/official-store`）**：启动前经 `verify_source` v2 缓存（逐文件 `O_NOFOLLOW` SHA-256 + 源码目录禁止 `.pyc`/`.pyo`）校验上游 `1.6.7` (`ec6f408`) 源码；通过外置私有 `0600` SQLite WAL（`capture-receipts.sqlite3`、`lesson-provenance.sqlite3`、接入网关单点写入的 `session-drift.sqlite3`、带 `4 MiB` WAL 限额的 `knowledge-recall.sqlite3`）与外置元数据（`user-profiles.json`、`tool-graph-*.json`）及 `language_egress` 扩展能力，正文、向量（LanceDB）与图谱（Ladybug）唯一真相保留在 `.local/daily/official-store`。提炼推理端支持「Agent 模型代理（`agent_bridge`）」与「直连 LLM Provider / Ollama」并存，运行期由唯一 `active` 决定。

## 2. 运行方式

| 用途 | 命令 |
| --- | --- |
| 启动日常服务与 UI（默认配置 `.local/daily/runtime.json`） | `.local/cognee-python/bin/python scripts/dev.py start --config /绝对路径/runtime.json` |
| 仅启动后台 / 查看状态 / 重开窗口 / 停止 / 异常恢复 | `.local/cognee-python/bin/python scripts/dev.py {start --no-ui \| status \| open \| stop \| recover} --config /绝对路径/runtime.json` |
| 统一改动感知 / 冻结门禁（开发与审改轮用 `--changed` / `--staged`；源码冻结后打包提交前单一节点用 `--full`；候选发版含 Level 0 契约冒烟用 `--release`） | `python3 scripts/task.py -- python3 scripts/check.py {--changed \| --staged \| --full \| --release}` |
| Go 全量测试（含 `perf_budget_test.go` 冷启动首页 `<150ms`、帧耗时 `<12ms`/`<16ms`、切页堆内存 `<=25 MiB` 及 `lint_gate_test.go` 恒常运行 `lint.py` + `test_lint.py`）与静态检查 | `sh scripts/go.sh test ./...`、`sh scripts/go.sh vet ./...` |
| 零依赖静态检查（`ML001`–`ML003` 模型边界、`ML006` 画像无总超时守卫、`ML007` 环境隔离、`ML008`–`ML012` UI 性能、`ML013`–`ML017` 运行时资源、`ML018` 正式文档禁引 `docs/process/` 与 `ML019`–`ML022` 扩展架构门禁，其中 `ML015` 覆盖 `scripts/*.py`；`go test ./...` 自动强制执行） | `python3 scripts/task.py -- python3 scripts/lint.py` |
| Scripts 回归与 Hook 配置（`scripts/install-hooks.sh` 仅启用局部 `.githooks` 暂存态 `pre-commit` + 调用 `check.py --pre-push` 的 `pre-push` 并全阶段透传全局钩子，不改全局 `~/.gitconfig`） | `python3 scripts/task.py -- python3 -m unittest discover -s scripts -p "test_*.py"` |
| 模型参数 / 探针 / 切换 / 启动回归（修改模型路径时必跑） | `python3 scripts/task.py -- python3 -m unittest` 运行 `test_generation_parameters`、`test_generation_probe`、`test_generation_handoff`、`test_generation_startup` |

运行边界：
- **日常启动解释器**：从仓库根目录用 `runtime.json` 的 `python` 字段指定的受管 Python 3.12 执行 `scripts/dev.py`，本机日常配置对应上表路径。启动预检在启动器进程中执行，裸 `python3` 可能选到缺少 `tomllib` 的旧版本。
- **单一日常库**：日常运行、Native 界面与真实链路验收统一连接 `.local/daily/official-store`，不另建独立测试库；未核对前不删除原材料或运行依赖。
- **磁盘门禁与目录保护**：开发态受管任务经 `scripts/task.py` 执行，至少保留 `50 GiB` 可用空间（运行中低于 `55 GiB` 自动停止）；独立 `.app` 安装态监控阈值为最低 `2 GiB` / 告警线 `5 GiB`。**禁止清空 `build/`**（2026-11-11 曾因清理旧构建误删仍被运行中 helper 依赖的完整 `.app`，见[运行包误清理复盘](docs/engineering/runtime-package-cleanup-incident-20261111.md)；`scripts/disk-space.py` 依赖 `build/disk-cleanup/` 的保留清单与回执）。清理旧运行包前必须先核对 daemon/helper 所属完整包与代理依赖，依赖未知则保留。
- **隐私与交付范围**：实际运行配置、凭据与真实记忆库位于 Git 跟踪之外的 `.local/`；不提交密钥、真实库、聊天记录或本机 Agent 配置。除非用户明确要求，不构建 DMG 或执行签名、公证、更新与 helper 安装。
- **提交身份**：本仓库所有提交（含 cherry-pick、rebase、标签）身份固定为 `zwhu <331946763@qq.com>`，只写仓库级配置、不改全局 `~/.gitconfig`。独立副本、Agent 工作区或新克隆在首次提交前先执行 `sh scripts/install-hooks.sh`（写入仓库级 `user.*` 并启用 `.githooks/_identity_guard.sh`：`pre-commit` 校验当前身份，`pre-push` 拒绝推送任何身份不符的提交或标签）。
- **过程文档定义与隔离（`docs/process/`）**：「过程文档」指仅服务于当次需求探索、调试排查、评审或验收留证、不构成项目长期基线的全部临时产物——无论格式是文档/草稿、图片/截图（PNG/JPG/WebP/GIF）还是录屏视频（MP4/MOV/WebM）与实验原始报告（判定口诀：任务完成后删掉不影响理解当前系统设计、契约或长期复盘）。所有过程文档一律放入 `docs/process/` 子目录（`.gitignore` 已忽略，仅保留 `docs/process/README.md` 入库），不得散落到 `docs/design/`、`docs/images/` 或源码目录；临时编译产物（`.app`、可执行二进制、源码覆盖拷贝）一律走系统 `$TMPDIR` 并在退出时清理，不进 `docs/process/`。单次命令超时、环境插曲与待重启提示仅在会话汇报，未定稿内容禁止提前写入正式 `docs/`；长期生效的规范、架构契约、专题调研、防复发复盘与正式文档直接引用的少量静态图表才分别入库对应 `docs/` 分类目录与 `docs/images/`，且正式 `docs/` 必须自包含、严禁引用 `docs/process/`（由 `ML018` 与 `pre-commit` 自动拦截），定稿晋升后顺手清理同主题原始日志（完整边界见 [docs/process/README.md](docs/process/README.md)）。

## 3. 文档路由

全仓文档总索引见 [docs/README.md](docs/README.md)，按任务类型查阅对应基准文档：

| 任务领域 | 查阅文档 |
| --- | --- |
| **产品定位、内容分层与演进** | [产品定义](docs/product/product.md)（含内容分层）、[Roadmap](docs/product/roadmap.md)、[内测安装指南](docs/product/beta-install-guide.md)（`v0.1.0-beta.3`） |
| **系统架构与开发运维** | [架构总览](docs/architecture/overview.md)、[开发说明](docs/architecture/development.md)（含国内镜像源与手工终端兜底） |
| **Agent 接入、模型契约与队列隔离** | [电脑级接入契约](docs/architecture/agent-connection-contract.md)、[有效模型契约](docs/architecture/agent-effective-model-contract-20261118.md)、[队列隔离契约](docs/architecture/queue-isolation-20261119.md) |
| **界面交互、V4 视觉与动效** | [视觉与交互规范 V4](docs/design/visual-standards-v4-20261120.md)、[MyGo 动效契约](docs/design/motion-implementation.md)、[V4 可交互原型](docs/design/visual-v4-prototype/)、[首次启动向导原型](docs/design/onboarding-prototype/) |
| **专题技术调研** | [安装依赖瘦身调研](docs/research/install-dependency-slimming-20261125.md)、[Docker 部署架构调研](docs/research/docker-deployment-architecture-20261125.md)、[首屏 20× 性能调研](docs/research/native-first-screen-performance-20261122.md)、[多 Agent 驱动协议](docs/research/agent-driving-protocols-20261118.md)、[Hindsight 审计](docs/research/hindsight-vs-memlane-20261117.md)、[macOS 更新调研](docs/research/macos-updater-20261119.md)、[TRAE IDE 整理调研](docs/research/trae-ide-memory-processing-20261119.md) |
| **工程规范、复盘与模板** | [工程规范](docs/engineering/workflow.md)、[Beta 安装问题库](docs/engineering/beta-install-issues.md)、[缺陷复盘汇总](docs/engineering/defect-postmortems.md)、[多 Agent 耗时复盘](docs/engineering/agent-workflow-efficiency-retrospective-20261125.md)、[运行包误清理复盘](docs/engineering/runtime-package-cleanup-incident-20261111.md)、[任务说明模板](docs/engineering/templates/task-brief-template.md)、[用例模板](docs/engineering/templates/testcase-template.md)、[双审与契约例外账本模板](docs/engineering/templates/review-ledger-template.md)、[复盘模板](docs/engineering/templates/incident-template.md) |
| **历史归档（仅供追溯）** | [归档目录](docs/archive/)（含 V3 设计稿、V4 实施账本、旧视觉规范、beta.1 发版方案、四阶段架构改造记录、部署方向旧稿与已退役说明，不作新改动依据） |
| **过程文档与验收媒体（本地忽略）** | [过程产物目录](docs/process/README.md)（独立存放截图、录屏视频、草稿与验收过程文件，已配置 `.gitignore` 不入库） |

## 4. 设计原则

- **全局最优决策**：从全局出发思考当前最优解，不被过时文档约束；若最优解与旧文档冲突，先提出最优解并标明冲突所在与原因，经用户确认后同步更新相关文档。
- **Cognee 零侵入红线**：严禁修改上游 Cognee 源码、严禁维护私有 Fork 或本地源码补丁副本、严禁绕过 `official_service_dev.verify_source` 的上游逐文件 SHA256 校验（v2 缓存同时禁止源码目录存在 `.pyc`/`.pyo`），也不得在运行期对 Cognee 内部模块、路由或管线做无契约 monkey-patch。一切 Memlane 扩展能力（ACL 门禁、召回合并、Diff/日期注入、召回打点、回执与溯源、语言出口改写等）必须落在外置网关（`cognee_extension_gateway.py`）、外置存储（`capture_receipts` / `provenance_store` / `session_drift` / `knowledge_recall`）、LLM 出口层（`language_egress.py`）或宿主插件层，保证上游 Cognee 升级时可原样替换。
- **记忆日期口径**：优先取外置 `provenance_store` 按 `(dataset_id, lesson_type, state_key)` 锁定的最早日期（`system_metadata` 优先于用户可编辑 `external_metadata`，GET 目录纯只读无写副作用），其次取原生 `session_date`，缺失时回退 `createdAt` 对应本地日历日（`createdAt` 单独保留）。
- **唯一真相与用户确认边界**：Cognee 官方存储为唯一主数据源；通用记忆与用户画像按来源独立保存，不自动合并为通用记忆。AI 提出的合并、覆盖、删除或跨项目移动，在用户明确确认前不得改变已有有效记忆。
- **模型参数与设置契约**：未配置的模型选项保持省略，用户显式填写的 `0` 必须保留；启动、验证与热切换统一使用 `generation_config.native_llm_configuration` / `native_llm_kwargs`，不继承前一配置环境。Agent 模型代理（`agent_bridge`）与直连 Provider 并存时查看、编辑或保存均不激活，仅确认后的服务回执更新唯一 `active`；用户画像生成不得按固定总时长超时中断（由 `ML006` 静态守卫），保存阶段不可取消。
- **桌面界面与视觉验收**：保持 `1200×820` 默认与最小窗口（允许放大），首页宽幅总览、详情页三栏、关键词搜索统一交给全局 `⌘K`、无全局「全部记忆」侧栏入口。UI 改动须对照 V4 规范与原型在浅色、深色两套外观下实际验收。
- **任务分级与反嵌套（T0–T3）**：常规编排严格 ≤2 层，严禁小任务走「主控 → 新建 worker → 套 `codex exec`」多层嵌套。**T0**（≤3 文件、单目录/文档/打包/提交；执行 DMG/签名仅限用户明确要求后直跑）主控单步直做，改 A 类生产代码必跑 `scripts/check.py --changed`（触及安全/ACL/模型参数/守护锁/Cognee 边界自动升格 T1 走单路审）；**T1**（4–15 文件单层）主控或单 subagent；**T2**（≤30–40 文件单垂直切片）按互斥文件并行；**T3**（>40 文件/多子系统）先经 [plan-change-impact](.agents/skills/plan-change-impact/SKILL.md) 拆批逐片合入（详见 [工程规范](docs/engineering/workflow.md)）。
- **双审收敛与测试单一归属**：在 Antigravity（`agy`）中发起 T2/T3 双独立代码审核或 Swarm 审波前，必须读取 `agy` 专属 Skill [dual-code-review](.agent/skills/dual-code-review/SKILL.md)（仅 `agy` 可见）与[审核账本模板](docs/engineering/templates/review-ledger-template.md)——执行 A/B/C 分层 P1 举证门槛、「2 轮修复 + 第 3 轮主控裁决」（R3 不可豁免 P0 及确凿 A 类生产 P1）、增量 diff 携带「已确认契约例外账本」与单路 `APPROVE` 免唤醒，审稿人禁跑全量测试。开发与审改轮统一跑 `scripts/check.py --changed`；全量门禁集中在源码冻结后、打包/提交前由单一节点执行 `scripts/check.py --full`（发版用 `--release`）。
- **工程闭环与协作底线**：跨模块实施与集成验收优先使用 [plan-change-impact](.agents/skills/plan-change-impact/SKILL.md) 与 [verify-continuous-scenarios](.agents/skills/verify-continuous-scenarios/SKILL.md)。同一具体缺陷累计第 4 次验收仍未通过时停止该缺陷尝试并保留证据汇报（与双审 2+1、环境前置失败 `exit 31` 正交，规则细节见 [工程规范](docs/engineering/workflow.md)）。诊断区分已实现与待验证，不将未知模型调用或进程状态推断为零；保留无关修改。文档与交流默认使用中文，动工前用一行说明即将执行的工作，结束时给出可独立读懂的简短小结。
