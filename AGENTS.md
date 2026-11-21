# Memlane 开发指南

Memlane 是面向程序员的 macOS 记忆客户端，采用 **MyGo Native 桌面端 + 固定版本官方 Cognee 引擎**。核心领域术语与分层定义见 [CONTEXT.md](CONTEXT.md)。

## 1. 基础架构

全景图与详细分层见 [架构总览](docs/architecture/overview.md)（[架构图 SVG](docs/images/memlane-architecture-20261120.svg)）：

- **宿主 Coding Agents（接入层）**：Codex、Antigravity / Gemini App、pi、OpenCode、TraeX、Claude Code & Cursor；按电脑级受管主体接入，各自持有独立官方身份与 `X-Api-Key`。
- **MyGo 原生桌面端（`cmd/memlane-app/`、`internal/simpledesktop/`）**：`1200×820` 无 WebView 自绘客户端，划分为 `overview` / `reading` / `search` / `graph` 四个控制器，通过 `http_transport.go` 直连本地服务，不本地缓存正文副本。
- **引导与原生守护（`internal/bootstrap/`、`internal/supervisor/`、`scripts/dev.py`）**：持有 `.service-owner` flock 单实例独占锁、以 `Setsid` 管理独立进程组、20 MiB 活 inode 日志截尾脱敏及防抖健康检查；崩溃不静默自启。
- **控制面与零侵入外置网关（`engine/python/project_control_server.py`、`cognee_extension_gateway.py`）**：Loopback 控制面管理项目注册表、连接编排、队列调度、热切换与三层监控（`/v1/runtime/monitor`）；`/cognee/*` 网关统一执行 ACL 门禁、召回合并通用记忆、Diff/日期注入与召回打点。
- **官方记忆引擎与外置存储（`official_service_dev.py`、`.local/daily/official-store`）**：启动前 SHA256 校验上游 `1.6.7` (`ec6f408`) 源码；通过 `capture_receipts` / `provenance_store` 私有 `0600` SQLite WAL 与 `language_egress` 扩展能力，正文、向量（LanceDB）与图谱（Ladybug）唯一真相保留在 `.local/daily/official-store`。推理端支持「本地 Agent 补全桥」与「直连 LLM Provider / Ollama」并存，运行期由唯一 `active` 决定。

## 2. 运行方式

| 用途 | 命令 |
| --- | --- |
| 启动日常服务与 UI（默认配置 `.local/daily/runtime.json`） | `python3 scripts/dev.py start --config /绝对路径/runtime.json` |
| 仅启动后台 / 查看状态 / 重开窗口 / 停止 / 异常恢复 | `python3 scripts/dev.py {start --no-ui \| status \| open \| stop \| recover} --config /绝对路径/runtime.json` |
| Go 全量测试（含 `perf_budget_test.go` 冷启动首页 `<150ms`、帧耗时 `<12ms`/`<16ms` 与切页堆内存 `<=25 MiB` 验收）与静态检查 | `sh scripts/go.sh test ./...`、`sh scripts/go.sh vet ./...` |
| 零依赖静态检查（`ML001`–`ML003` 模型边界、`ML007` 环境变量隔离、`ML008`–`ML012` UI 与冷启动首页性能门禁） | `python3 scripts/task.py -- python3 scripts/lint.py` |
| Lint 规则回归单测 | `python3 scripts/task.py -- python3 -m unittest discover -s scripts -p test_lint.py` |
| 模型参数 / 探针 / 切换 / 启动回归（修改模型路径时必跑） | `python3 scripts/task.py -- python3 -m unittest` 运行 `test_generation_parameters`、`test_generation_probe`、`test_generation_handoff`、`test_generation_startup` |

运行边界：
- **单一日常库**：日常运行、Native 界面与真实链路验收统一连接 `.local/daily/official-store`，不另建独立测试库；未核对前不删除原材料或运行依赖。
- **磁盘门禁与目录保护**：受管任务经 `scripts/task.py` 执行，至少保留 `50 GiB` 可用空间（运行中低于 `55 GiB` 自动停止）；**禁止清空 `build/`**（其中 `build/p3-prototype` 是含未提交修改的 Git 工作树）。清理旧运行包前必须先核对 daemon/helper 所属完整包与桥接依赖，依赖未知则保留。
- **隐私与交付范围**：实际运行配置、凭据与真实记忆库位于 Git 跟踪之外的 `.local/`；不提交密钥、真实库、聊天记录或本机 Agent 配置。除非用户明确要求，不构建 DMG 或执行签名、公证、更新与 helper 安装。
- **过程文档定义与隔离（`docs/process/`）**：「过程文档」指仅服务于当次需求探索、调试排查、评审或验收留证、不构成项目长期基线的全部临时产物——无论格式是文档/草稿、图片/截图（PNG/JPG/WebP/GIF）还是录屏视频（MP4/MOV/WebM）与实验原始报告（判定口诀：任务完成后删掉不影响理解当前系统设计、契约或长期复盘）。所有过程文档一律放入 `docs/process/` 子目录（`.gitignore` 已忽略，仅保留 `docs/process/README.md` 入库），不得散落到 `docs/design/`、`docs/images/` 或源码目录；长期生效的规范、架构契约、专题调研、防复发复盘与正式文档直接引用的少量静态图表才分别入库对应 `docs/` 分类目录与 `docs/images/`（完整边界见 [docs/process/README.md](docs/process/README.md)）。

## 3. 文档路由

全仓文档总索引见 [docs/README.md](docs/README.md)，按任务类型查阅对应基准文档：

| 任务领域 | 查阅文档 |
| --- | --- |
| **产品定位、内容分层与演进** | [产品定义](docs/product/product.md)、[内容分层](docs/product/content-model.md)、[Roadmap](docs/product/roadmap.md)、[部署方向](docs/product/deployment-options-20261117.md) |
| **系统架构、开发运维与改造记录** | [架构总览](docs/architecture/overview.md)、[开发说明](docs/architecture/development.md)、[架构改造记录](docs/architecture/architecture-refactor-20261120.md)、[环境准备报告](docs/architecture/environment-readiness-reliability-20261117.html) |
| **Agent 接入、模型契约与队列隔离** | [电脑级接入契约](docs/architecture/agent-connection-contract.md)、[有效模型契约](docs/architecture/agent-effective-model-contract-20261118.md)、[队列隔离记录](docs/architecture/queue-isolation-20261119.md) |
| **界面交互、V4 视觉与动效** | [设计决策](docs/design/design.md)、[视觉规范 V4](docs/design/visual-standards-v4-20261120.md)、[MyGo 动效契约](docs/design/motion-implementation.md)、[V4 可交互原型](docs/design/visual-v4-prototype/)、[V4 实施账本](docs/design/visual-v4-implementation-20261120.md) |
| **专题技术调研** | [多 Agent 驱动协议](docs/research/agent-driving-protocols-20261118.md)、[Hindsight 审计](docs/research/hindsight-vs-memlane-20261117.md)、[macOS 更新调研](docs/research/macos-updater-20261119.md)、[TRAE IDE 整理调研](docs/research/trae-ide-memory-processing-20261119.md) |
| **工程规范、复盘与模板** | [工程规范](docs/engineering/workflow.md)、[运行包误清理复盘](docs/engineering/runtime-package-cleanup-incident-20261111.md)、[任务说明模板](docs/engineering/templates/task-brief-template.md)、[用例模板](docs/engineering/templates/testcase-template.md)、[复盘模板](docs/engineering/templates/incident-template.md) |
| **历史归档（仅供追溯）** | [归档目录](docs/archive/)（含 V3 设计稿、旧视觉规范与已退役脚本说明，不作新改动依据） |
| **过程文档与验收媒体（本地忽略）** | [过程产物目录](docs/process/README.md)（独立存放截图、录屏视频、草稿与验收过程文件，已配置 `.gitignore` 不入库） |

## 4. 设计原则

- **全局最优决策**：从全局出发思考当前最优解，不被过时文档约束；若最优解与旧文档冲突，先提出最优解并标明冲突所在与原因，经用户确认后同步更新相关文档。
- **Cognee 零侵入红线**：严禁修改上游 Cognee 源码、严禁维护私有 Fork 或本地源码补丁副本、严禁绕过 `official_service_dev.verify_source` 的上游 SHA256 校验，也不得在运行期对 Cognee 内部模块、路由或管线做无契约 monkey-patch。一切 Memlane 扩展能力（ACL 门禁、召回合并、Diff/日期注入、召回打点、回执与溯源、语言出口改写等）必须落在外置网关（`cognee_extension_gateway.py`）、外置存储（`capture_receipts` / `provenance_store`）、LLM 出口层（`language_egress.py`）或宿主插件层，保证上游 Cognee 升级时可原样替换。
- **记忆日期口径**：优先取外置 `provenance_store` 按 `(dataset_id, lesson_type, state_key)` 锁定的最早日期，其次取原生 `session_date`，缺失时回退 `createdAt` 对应本地日历日（`createdAt` 单独保留）。
- **唯一真相与用户确认边界**：Cognee 官方存储为唯一主数据源；通用记忆与用户画像按来源独立保存，不自动合并为通用记忆。AI 提出的合并、覆盖、删除或跨项目移动，在用户明确确认前不得改变已有有效知识。
- **模型参数与设置契约**：未配置的模型选项保持省略，用户显式填写的 `0` 必须保留；启动、验证与热切换统一使用 `generation_config.native_llm_configuration` / `native_llm_kwargs`，不继承前一配置环境。桥接与 Provider 并存时查看、编辑或保存均不激活，仅确认后的服务回执更新唯一 `active`；用户画像生成不得按固定总时长超时中断，保存阶段不可取消。
- **桌面界面与视觉验收**：保持 `1200×820` 默认与最小窗口（允许放大），首页宽幅总览、详情页三栏、关键词搜索统一交给全局 `⌘K`、无全局「全部记忆」侧栏入口。UI 改动须对照 V4 规范与原型在浅色、深色两套外观下实际验收。
- **工程闭环与协作底线**：跨模块实施与集成验收优先使用 [plan-change-impact](.agents/skills/plan-change-impact/SKILL.md) 与 [verify-continuous-scenarios](.agents/skills/verify-continuous-scenarios/SKILL.md)。同一具体缺陷累计第 4 次验收仍未通过时停止该缺陷尝试并保留证据汇报（规则细节见 [工程规范](docs/engineering/workflow.md)）。诊断区分已实现与待验证，不将未知模型调用或进程状态推断为零；保留无关修改。文档与交流默认使用中文，动工前用一行说明即将执行的工作，结束时给出可独立读懂的简短小结。
