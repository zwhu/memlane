## 2026-11-13 项目权限增补

用户已明确授权 PA01–PA14，覆盖本文旧“无项目权限／共用 owner／通用知识只读”的相应范围，不恢复历史全量 Goal。当前产品规则见 `docs/product.md`、`docs/design.md`；自验及日常接入边界、缺陷计数、资源与启动入口见 `docs/engineering/project-access-delivery-20261113.md`、`docs/engineering/project-access-execution-20261113.md`。使用独立官方 Agent 主体、管理侧 project_control、固定官方插件补丁、动态默认策略及逐字段项目覆盖；维护权限包括 read/write/delete，不授 share。保留原库、管理 owner、登录、其他插件及旧会话／材料，不自动迁移、补采或重放。继续 dev，不做 release。下文相反表述为先前阶段历史，不覆盖 PA 增补。

## 2026-11-13 精简版实施授权

本轮D01–D09已按明确范围收口，交付入口为 `docs/engineering/client-simplification-delivery-20261113.md`。下文2026-11-12的“没有启停／未清理／未打tag”等表述保留为当时设计阶段历史，不覆盖本次已完成事实；历史全量Goal继续暂停。

后续Agent定位或修改精简版前，先查[分类错误与防复发索引](docs/engineering/client-simplification-errors-20261113.md)及同版JSON。沿用具体问题编号、失败计数和证据边界，不为补记录自动重跑模型、索引或启停服务。

用户已明确启动本次精简版 Goal，覆盖下文旧“开发暂停／仅方案”状态。仅执行 `docs/client-simplification-spec-20261112.md` 与技术方案范围，使用 `scripts/dev.py`、MyGo dev；不恢复历史全量 Goal、不进入 release。开发前直接提交当前相关代码并创建本地 tag，不采用独立 index、快照分支或多 worktree 快照。进度与所有权见 `docs/engineering/client-simplification-execution-20261113.md`。旧具体问题停止次数与运行依赖保护继续有效。

## 2026-11-12 当前精简设计入口

本轮用户要求从设计中删除已砍掉的功能。当前产品范围以 `docs/client-simplification-spec-20261112.md`、`docs/client-simplification-technical-plan-20261112.md` 为准，当前界面入口为 `docs/ui-redesign/design-catalog.md`。旧 A–O、三种处理方式、编辑／历史／审核／计划／权限等交接不再构成首版设计要求；实际工程和缺陷记录保留历史事实。

最新决定：窄窗口不设计、不实现、不继续修复或验收；首版默认及最小 1200×820，允许放大。后续实施同步清除此前遗留的问题代码，首次生产代码清理前先形成包含相关未提交源码的快照提交并创建注释 tag，具体步骤见当前技术方案。历史失败记录不改为通过；本轮文档与设计调整不代表已执行生产代码清理或打 tag。

UI 工作查看 `docs/images/ui-redesign/client-simplification-20261112/` 中首页、知识与原始记录稿。旧设计图已退役，不能从旧截图恢复删除的按钮。最新精简原型仅是设计资产，不代表生产实现完成；本轮没有启停服务、修改 Agent 配置或删除运行数据。

## 当前暂停交接（2026-11-12）

用户要求暂停所有开发并整理handoff。coding-memory／代码图谱及移除权限管理相关开发已暂停，未经用户明确恢复不自动启动实现、编译、测试、组件准备或模型任务。统一入口为 docs/engineering/coding-memory-handoff-20261112.md。暂停不代表完成，也不取消旧运行依赖保护和具体问题停止计数。

## 2026-11-12 最新范围：移除权限管理

本阶段默认所有 Agent 均可读写 Memlane 中的数据，不保留项目／Agent 读写白名单、角色、授权／撤权确认、权限管理接口、界面、测试或计划。项目和 dataset 用于归属及检索分组，不再作为访问许可。服务连接认证与任务所有权、取消、去重、来源版本核对继续保留；启动／停止开关控制任务执行，不限制 Agent 访问已有数据。此最新要求覆盖下文旧版本中与之冲突的权限规则；全局清理见 docs/engineering/remove-agent-permissions-20261112.md。

## 2026-11-14 单一日常库（用户最新要求）

用户原话：“我现在不需要隔离测试库，就一个库就行了，作为我日常使用的库，把两个库统一起来”。当前运行、Native展示及后续真实链路验收统一连接同一套日常Cognee官方存储，不再为开发另建或切换独立测试库。项目仍按各自dataset分组；“一个库”不表示把不同项目混成同一dataset。

本次已授权将原日常库和本轮隔离库合并，以原日常库为主，保留双方原始记录、知识、来源关联、主体及权限，并统一Native、服务及已接入Agent的目标。先盘点和备份，再合并及连续核验；备份只用于回滚，不作为第二运行库。未核对完成前不删除原材料／依赖，不用切换另一库展示“数据恢复”。此要求覆盖旧文档中本轮使用隔离测试库的相应执行安排；既有具体问题停止计数不重置。

# Memlane

这是面向程序员的 macOS 记忆客户端，使用 Cognee 作为记忆引擎。桌面框架已选择 MyGo；P0 核心客户端、服务与组件链路、P1 手动记忆管理与实际窗口设计对照、P2 真实 Codex/pi 共享与 App Agent 管理已通过。P3-C1–C4 正式整理、P3-E 全局经验、P3-D1/D2 计划与有限恢复、U 使用记录、I 引导、A1a traex 日常接入均已通过对应本机验收；G2 代码图谱更新与 F1a–F1d 索引/备份/恢复/空间不足已分层通过，具体边界见 docs/p3-delivery-plan.md。2026-11-09 最新实施转入已认可 A/B 重设计：pi/DeepSeek 限定同包闭环、schema17 记忆处理配置账本、daemon 所有的真实 pi 验证／取消／显式启用／重启保持，以及「设置 → 记忆处理」Native 页的真实 pi 保存／活动取消／验证／显式启用已通过；Provider/Magpie 实际推理、Codex 处理桥、正式任务分派、A 初始化承载与环境／Agent 连接页、B/C Native 页面、最终包和正式分发未完成。

- 开始工作前阅读 [产品定义](docs/product.md) 和 [设计决策](docs/design.md)，以已确认的产品规则为准。
- UI 重设计开发先读 [设计交接入口](docs/ui-redesign/README.md) 和对应已认可交接。A 环境正文、三步初始化、批量连接、记忆处理与设置复用已认可，固定原型 `e27a49e35020972d6aea4ab070ec898fb6c9c22e`；B 日常首页 revision 5 已认可，固定原型 `ed6cd37858c5d6f56f9307ecbf6ac454bb6e1edb`；C Agent 设置与项目知识整理 revision 7 已认可，固定原型 `35a5bad5f5398f2c9cf3073b673983f5dab0a48c`，实施先读 [C 交接](docs/ui-redesign/c-agent-handoff.md)。不得将设计认可、示例数字或浏览器验证当成生产能力已交付。
- Agent 日常管理按 [当前入口调整](docs/ui-redesign/agent-settings-placement.md)：设置 → Agent 连接；共享环境与组件／Agent 连接／记忆处理。项目整理可一直跳过，不阻塞正常提交与日常记忆；空知识页签显示「整理项目」主入口，已有知识从项目「⋯」菜单「补充项目知识」回补，已启动任务通过「查看整理进度」恢复。右栏由绑定 Agent 整理并保存，启动前明确 Token 消耗，可选择「同时整理历史会话」（默认关闭，限当前 Agent／项目的已授权会话，额外 Token 提示）；运行中固定范围、取消和失败停动效。记忆是总称，项目页以「知识／原始记录」分开显示，知识与原文不会因保存／索引状态互换，术语见 [CONTEXT](CONTEXT.md) 和 [内容分层](docs/ui-redesign/content-model.md)；知识列表与详情统一「知识／知识详情」，「查看知识」不停止任务。项目整理真实任务、历史会话读取、C Native 页面和知识质量仍待工程验收。
- 界面原“全局记忆”统一称“通用知识”，知识内分类“个人偏好／通用经验”，按[已确认命名交接](docs/ui-redesign/common-knowledge-naming.md)同步侧栏、页面标题、来源、授权和复制／提炼动作。此名称表示跨项目参考，保留适用条件、项目约定优先和独立授权；不迁移内部scope或把项目知识自动提升为通用知识。2026-11-10 最新纠正：当前侧栏去掉“全部记忆”，跨项目入口保留在全局搜索；同步B/C/D及F–O共用导航，不恢复旧入口。
- 后续功能按固定设计稿还原，先读[全量设计交接](docs/ui-redesign/full-design-handoff.md)与对应已认可交接，实际打开场景截图和可交互稿；还原入口、布局、字段、按钮、状态、焦点、滚动、草稿及来源版本，不从代码结构重定页面。每一步完成即在[进度记录](docs/ui-redesign/full-design-progress.md)或对应工程账本落稿，附设计基线、实际窗口对照、操作回执、偏差和完成边界。F–O固定候选`780745fc55d71edb22e46d9c3e4f208d5ecb602f`已制作，认可状态与工程授权分别按交接和用户最新指示记录，不把制作／浏览器核验当生产完成。
- 开发任何后续动效先读 [MyGo 动效实施与复用契约](docs/ui-redesign/motion-implementation.md)：复用几何、时序、缓动与状态规则，以 Native Go 组件实施；记录每阶段参数、稳定 Key、中断／重入、减少动态效果和实际包对照。动画只表达真实状态，不驱动任务完成；原型和GIF不作为生产功能验收。
- 知识数据以 Cognee 官方存储为主要数据源，Memlane 负责呈现及操作适配，不默认另存完整原文／知识副本或另建重复索引；只保留必要客户端状态与关联信息。2026-11-12 用户最新纠正及现有双库待调整边界见 [内容分层](docs/ui-redesign/content-model.md#2026-11-12-用户纠正官方数据为主减少重复存储)。官方缺少的能力先明确缺口，不自动扩建独立知识系统；旧数据未经迁移核对不得删除。
- Agent 信息采集统一按 [官方方案优先规则](docs/ui-redesign/agent-connection-contract.md#2026-11-12-用户确认所有-agent-采集统一复用官方方案)：有官方方案就复用，不另写并行采集器；仅核实无官方方案时，才参考官方插件结构补适配。Memlane 的职责是将插件安装、配置到对应 Agent 并管理接入状态，接收材料及项目归属另循现有契约。当前自有 Codex 日常采集标记为待迁移，既有实现与验收不代表新方案已完成；迁移保留已采集数据、来源和回执，避免双重采集。
- Agent 连接按 [电脑级配置规则](docs/ui-redesign/agent-connection-contract.md)：先将 Memlane 配置到当前电脑的编码 Agent，连接不以项目、工作目录或首任务模型为前置条件；项目/任务范围另行确认。现有项目级适配不能被描述为电脑级配置已实现。
- Agent 连接页使用多选卡片批量接入，逐项同页反馈安装、重载、验证与结果；已连接/不支持项不可重复提交，只重试失败项，取消保留成功项。处理记忆仍只选一个已连接 pi/Codex；页面删除登录/模型和提炼会话的技术说明，仅留当前模型与账号额度提示，隔离要求继续保存在开发契约。
- 连接与处理记忆按 [最新设计交接](docs/ui-redesign/memory-processing-design.md)：连接参考 Cognee Web UI 安装 skill/hooks/插件等；处理方式为自配 LLM provider、已连接 pi/Codex、Magpie 网关。模型目录参考 Magpie，供应商配置复用；Magpie 上游密钥由其管理。整理会话必须隔离采集以防循环；设计已认可归档，真实桥接、Provider 与 Magpie 分别按工程证据验收。
- 环境与组件、记忆处理按 [页面复用契约](docs/ui-redesign/reusable-setup.md) 同时用于初始化与后续设置；三种配置可同时保留，仅一个当前使用，保存配置与明确启用分开，切换页签不启用。Magpie 默认自动检测本机网关，无需访问凭据；环境准备复用已有组件与后台任务，不重走首任务。
- 初始配置最新边界同样以 [页面复用契约](docs/ui-redesign/reusable-setup.md) 为准：仅「准备环境／连接 Agent／配置记忆处理」三步，完成后进入日常首页；第一项任务与记忆结果移出初始化。此前产品文档和验收中将首任务列为初始化步骤的安排属于旧版本，不覆盖本轮纠正。「设为当前使用」置于方式状态栏右侧。
- 开发或验收本地 Agent 模型桥接前，先读 [桥接需求与方案](docs/agent-model-bridge-requirements.md)、[研究依据](docs/research-cognee-agent-appserver.md)、[旧 pi/qwen 记录](docs/pi-model-bridge-validation.md) 和 [pi/DeepSeek 新记录](docs/pi-deepseek-validation.md)。pi 0.84.1 / Ollama 的完整包技术链路已通过但 qwen3:0.6b 事实质量未通过；随后 pi 0.84.1 / DeepSeek V4.1 Flash 的完整 Schema、六个固定样本和第五包限定闭环通过。两组结果都只覆盖各自模型、样本和隔离包；本轮「设置 → 记忆处理」只增加真实 pi UI 证据，独立 Provider、Codex app-server、Magpie、共享配置分派和其余新版 UI 仍须分别验收。
- 首页 A 还原与验收先读 [A视觉规范](docs/ui-redesign/visual-standards-20261113.md)：按固定原型最终样式实施，显式字体角色，统一组件与状态；应用及浏览器均在用户指定的外接显示器操作。以同尺寸、同缩放、同内容对照真实Native，功能通过不能替代视觉通过。其他页面不因此启动改造。
- 开发或验收 UI 前，实际打开 [当前首页设计图](docs/images/ui-redesign/client-simplification-20261112/01-home.png) 和 [当前三栏详情设计图](docs/images/ui-redesign/client-simplification-20261112/02-knowledge.png)，不能只读 Markdown 图片路径。首页A使用上述视觉规范中的更新基准；完成后对照实际窗口截图；工具无法看图时明确视觉验收未完成。
- 实现依据 [技术方案](docs/technical-plan.md) 与 [实施清单](docs/implementation-plan.md)；区分已选框架、待验证依赖和已通过的验收。
- 文档默认中文。区分已实现、原型演示与待验证能力，不将示例状态描述为真实接入结果。
- 首页保留宽幅总览；列表与详情页采用三栏；普通操作在栏内完成。保持紧凑排版、单色图标和按钮、连续浅蓝侧边栏。
- 不自动扩大首版范围。技术选型通过实际接口与打包验证确定，不由原型技术反推。
- 主下载包保持轻量，包含 UI、自动更新和组件管理等客户端能力；Cognee 引擎、原生依赖和模型作为独立组件自动准备。本轮 App 使用 Rust 原文 worker，Python 按相同原文契约完成对照；G2 代码图谱本机链路已通过，最终包与正式分发仍单独验收。分别报告主包大小、首次准备下载量与最终磁盘占用。
- 其他主机的记忆管理仅记录于 [Roadmap](docs/roadmap.md)，本次不设计或实现远程管理。
- 编辑与整理保留记录。AI 提出的合并、覆盖、删除、跨项目移动在确认前不得改变已有有效知识；恢复旧版本产生新版本。
- 保留用户无关修改。不要提交密钥、真实记忆库、聊天记录或本机 Agent 配置。
- 可交互设计原型归档于 `prototype/selected-ui` 分支；主分支保留产品规则与选定设计，生产实现按最终技术方案编写。

当前开发命令见 [开发说明](docs/development.md)，统一使用 `python3 scripts/dev.py`；Go检查使用 `sh scripts/go.sh test ./...`，Python端点检查通过已有环境及 `scripts/task.py`。旧完整builder和helper专属驱动已随旧链退役，不作为当前命令；P0记录保留历史事实。工具链保存在仓库内，不替换用户默认环境。

## 开发磁盘安全空间（用户2026-11-17调整为50 GiB）

用户本轮将最低保留空间从100 GiB放宽到50 GiB，运行监测继续提前5 GiB停止受管任务。历史交付记录中的100／105 GiB保留为当时事实，不覆盖当前50／55 GiB规则；此调整不恢复暂停的开发或授权安装新依赖。

- 开发Goal暂停期间不恢复研发。清理、磁盘防护按用户本次授权执行。
- 至少保留50 GiB可用空间；不足先清理本项目可再生缓存／任务载荷，仍不足则停止，不继续下载或构建。
- Go和完整builder已接入scripts/task.py；其他验收／组件准备一律通过 `python3 scripts/task.py -- <命令>` 执行。运行中余量监测低于55 GiB停止受管任务。新阈值对新启动的包装器生效；已运行的旧包装器须在后续明确授权的正常收尾／重启后才加载新值，不因修改门禁擅自停止共享服务。
- 临时环境、引擎／模型重复副本跑完任务即清；长期仅留报告、截图、小型回执及一个最新可运行包。不得继续无限保留完整隔离环境。禁止清空build：其中p3-prototype是有未提交修改的Git工作树。

## 工程执行与问题闭环

跨模块实施使用 [plan-change-impact](.agents/skills/plan-change-impact/SKILL.md)，集成／交付验收使用 [verify-continuous-scenarios](.agents/skills/verify-continuous-scenarios/SKILL.md)；局部小改动按需简化。项目填写入口为[影响与验证模板](docs/engineering/task-brief-template.md)，命令工具见[场景执行入口](docs/engineering/scenario-runner.md)。方法由Skill维护，产品规则、失败阈值、工具和状态仍以本仓库为准。

后续编码遵循 [工程规范](docs/engineering/workflow.md)：先列连续主流程用例，再集中开发和定向检查，统一集成验收；失败按同类集中修复。使用 [用例模板](docs/engineering/testcase-template.md) 与 [复盘模板](docs/engineering/incident-template.md)。诊断不得把未知模型调用或进程事实推断为零／已回收。工程规范试运行与未完成门禁见 [实施验收清单](docs/engineering/acceptance.md)。

同一具体问题累计第4次验收仍未通过，停止该问题的继续尝试并汇报；“4次”按同一具体缺陷计数，不按连续4轮整体验收失败计数。按触发条件、失败现象与验收标准识别问题，不得将整个Goal、页面或功能中的不同缺陷合并凑成4次。验收夹具／方法错误另行记录；修复引入的独立回归单独追踪。根因未明时保留归类依据与未知，不因同属UI或同一测试命令就合并。换版本、路径、Agent或操作ID也不能拆分同一问题以重置次数。失败证据始终保留，纠正误归类需留痕。详见[停止规则](docs/engineering/workflow.md)。不依赖该问题的工作可继续。

计数示例：四轮分别发现布局、复制、焦点和来源权限问题，各问题分别记1次，不触发“四次失败停止”。同一具体缺陷即使中间穿插其他问题，累计第4次失败仍须停止该缺陷；停止范围限于该问题及确实依赖它的工作。


## 开发与 release 阶段边界（用户2026-11-12明确要求）

- 当前处于开发阶段，不构建或验收完整运行包，不将完整包作为开发任务完成门槛；等用户明确要求进入 release 阶段后再开始打包。
- 开发阶段继续进行源码检查、必要测试、真实链路及实际 Native 界面验收；必要的开发调试编译不等于完整运行包交付。
- 既有完整包依赖保护及同一问题停止规则继续有效，不因暂缓打包而清理仍被引用的运行依赖。

## 运行包与清理依赖保护（用户2026-11-11要求记录防复发）

- 新GUI包不能视为完整运行时替代；清理前核对daemon/helper所属完整包、Agent桥接路径、服务注册路径及组件引用。保护完整包及其资源/签名，不仅保护执行文件；不能仅凭ps命令名或无运行进程判定可删。
- 切换保留包时同步保留实际依赖集合，只有真实迁移并验证后才清理旧包。依赖未知则保留；无安全清理候选且空间不足时停止构建/下载。
- 复盘见[运行包误清理](docs/engineering/runtime-package-cleanup-incident-20261111.md)；后续[卸载与依赖资源清理](docs/roadmap.md)只记录待办，本次未实现或执行卸载。

## Codebase ownership and communication

1/ This codebase is yours: what it's for and how it's built are your call, and you'll keep building on it.

2/ Before you start, say in a line what you're about to do; brief updates while you work help the user follow along. Close with a short recap that stands on its own (what you found, what you did, and what's next), so a reader who only sees the last message has the full picture.

## 2026-11-18 桥接与直连的模型参数防回归

提交相关修改前必须运行`python3 scripts/task.py -- python3 scripts/lint.py`。这是不依赖第三方库的AST检查，覆盖全部Python生产模块；ML001禁止绕过共享构造器，ML002禁止注入字面量参数，ML003禁止参数回退值。规则回归使用`python3 scripts/task.py -- python3 -m unittest discover -s scripts -p test_lint.py`。静态检查不替代下述真实协议回归。

用户明确要求两种模式都防止本次参数错误复发。未配置的选项保持未配置，不补成客户端显式采样/输出默认值；用户显式填写的0必须保留。启动、验证和切换统一使用`generation_config.native_llm_configuration` / `native_llm_kwargs`，消费者不得独立构造`LLMConfig`或继承前一配置环境。修改这些路径必须通过`test_generation_parameters`、`test_generation_probe`、`test_generation_handoff`、`test_generation_startup`，使用`scripts/task.py`执行。实际原生协议请求体须覆盖桥接/直连、未填写、显式0/上限及旧环境隔离。复盘与证据见[处理配置接续](docs/engineering/processing-handoff-20261118.md)，执行要求见[工程规范](docs/engineering/workflow.md)。
