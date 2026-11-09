# Memlane

这是面向程序员的 macOS 记忆客户端，使用 Cognee 作为记忆引擎。桌面框架已选择 MyGo；P0 核心客户端、服务与组件链路、P1 手动记忆管理与实际窗口设计对照、P2 真实 Codex/pi 共享与 App Agent 管理已通过。P3-C1–C4 正式整理、P3-E 全局经验、P3-D1/D2 计划与有限恢复、U 使用记录、I 引导、A1a traex 日常接入均已通过对应本机验收；G2 代码图谱更新与 F1a–F1d 索引/备份/恢复/空间不足已分层通过，具体边界见 docs/p3-delivery-plan.md。2026-11-09 最新实施转入已认可 A/B 重设计：pi/DeepSeek 限定同包闭环、schema17 记忆处理配置账本、daemon 所有的真实 pi 验证／取消／显式启用／重启保持，以及「设置 → 记忆处理」Native 页的真实 pi 保存／活动取消／验证／显式启用已通过；Provider/Magpie 实际推理、Codex 处理桥、正式任务分派、A 初始化承载与环境／Agent 连接页、B/C Native 页面、最终包和正式分发未完成。

- 开始工作前阅读 [产品定义](docs/product.md) 和 [设计决策](docs/design.md)，以已确认的产品规则为准。
- UI 重设计开发先读 [设计交接入口](docs/ui-redesign/README.md) 和对应已认可交接。A 环境正文、三步初始化、批量连接、记忆处理与设置复用已认可，固定原型 `e27a49e35020972d6aea4ab070ec898fb6c9c22e`；B 日常首页 revision 5 已认可，固定原型 `ed6cd37858c5d6f56f9307ecbf6ac454bb6e1edb`；C Agent 设置与项目知识整理 revision 7 已认可，固定原型 `35a5bad5f5398f2c9cf3073b673983f5dab0a48c`，实施先读 [C 交接](docs/ui-redesign/c-agent-handoff.md)。不得将设计认可、示例数字或浏览器验证当成生产能力已交付。
- Agent 日常管理按 [当前入口调整](docs/ui-redesign/agent-settings-placement.md)：设置 → Agent 连接；共享环境与组件／Agent 连接／记忆处理。项目整理可一直跳过，不阻塞正常提交与日常记忆；空知识页签显示「整理项目」主入口，已有知识从项目「⋯」菜单「补充项目知识」回补，已启动任务通过「查看整理进度」恢复。右栏由绑定 Agent 整理并保存，启动前明确 Token 消耗，可选择「同时整理历史会话」（默认关闭，限当前 Agent／项目的已授权会话，额外 Token 提示）；运行中固定范围、取消和失败停动效。记忆是总称，项目页以「知识／原始记录」分开显示，知识与原文不会因保存／索引状态互换，术语见 [CONTEXT](CONTEXT.md) 和 [内容分层](docs/ui-redesign/content-model.md)；知识列表与详情统一「知识／知识详情」，「查看知识」不停止任务。项目整理真实任务、历史会话读取、C Native 页面和知识质量仍待工程验收。
- 开发任何后续动效先读 [MyGo 动效实施与复用契约](docs/ui-redesign/motion-implementation.md)：复用几何、时序、缓动与状态规则，以 Native Go 组件实施；记录每阶段参数、稳定 Key、中断／重入、减少动态效果和实际包对照。动画只表达真实状态，不驱动任务完成；原型和GIF不作为生产功能验收。
- Agent 连接按 [电脑级配置规则](docs/ui-redesign/agent-connection-contract.md)：先将 Memlane 配置到当前电脑的编码 Agent，连接不以项目、工作目录或首任务模型为前置条件；项目/任务范围另行确认。现有项目级适配不能被描述为电脑级配置已实现。
- Agent 连接页使用多选卡片批量接入，逐项同页反馈安装、重载、验证与结果；已连接/不支持项不可重复提交，只重试失败项，取消保留成功项。处理记忆仍只选一个已连接 pi/Codex；页面删除登录/模型和提炼会话的技术说明，仅留当前模型与账号额度提示，隔离要求继续保存在开发契约。
- 连接与处理记忆按 [最新设计交接](docs/ui-redesign/memory-processing-design.md)：连接参考 Cognee Web UI 安装 skill/hooks/插件等；处理方式为自配 LLM provider、已连接 pi/Codex、Magpie 网关。模型目录参考 Magpie，供应商配置复用；Magpie 上游密钥由其管理。整理会话必须隔离采集以防循环；设计已认可归档，真实桥接、Provider 与 Magpie 分别按工程证据验收。
- 环境与组件、记忆处理按 [页面复用契约](docs/ui-redesign/reusable-setup.md) 同时用于初始化与后续设置；三种配置可同时保留，仅一个当前使用，保存配置与明确启用分开，切换页签不启用。Magpie 默认自动检测本机网关，无需访问凭据；环境准备复用已有组件与后台任务，不重走首任务。
- 初始配置最新边界同样以 [页面复用契约](docs/ui-redesign/reusable-setup.md) 为准：仅「准备环境／连接 Agent／配置记忆处理」三步，完成后进入日常首页；第一项任务与记忆结果移出初始化。此前产品文档和验收中将首任务列为初始化步骤的安排属于旧版本，不覆盖本轮纠正。「设为当前使用」置于方式状态栏右侧。
- 开发或验收本地 Agent 模型桥接前，先读 [桥接需求与方案](docs/agent-model-bridge-requirements.md)、[研究依据](docs/research-cognee-agent-appserver.md)、[旧 pi/qwen 记录](docs/pi-model-bridge-validation.md) 和 [pi/DeepSeek 新记录](docs/pi-deepseek-validation.md)。pi 0.84.1 / Ollama 的完整包技术链路已通过但 qwen3:0.6b 事实质量未通过；随后 pi 0.84.1 / DeepSeek V4.1 Flash 的完整 Schema、六个固定样本和第五包限定闭环通过。两组结果都只覆盖各自模型、样本和隔离包；本轮「设置 → 记忆处理」只增加真实 pi UI 证据，独立 Provider、Codex app-server、Magpie、共享配置分派和其余新版 UI 仍须分别验收。
- 开发或验收 UI 前，实际打开 [首页设计图](docs/images/home.jpg) 和 [三栏详情设计图](docs/images/memory-detail.jpg)，不能只读 Markdown 图片路径。完成后对照实际窗口截图；工具无法看图时明确视觉验收未完成。
- 实现依据 [技术方案](docs/technical-plan.md) 与 [实施清单](docs/implementation-plan.md)；区分已选框架、待验证依赖和已通过的验收。
- 文档默认中文。区分已实现、原型演示与待验证能力，不将示例状态描述为真实接入结果。
- 首页保留宽幅总览；列表与详情页采用三栏；普通操作在栏内完成。保持紧凑排版、单色图标和按钮、连续浅蓝侧边栏。
- 不自动扩大首版范围。技术选型通过实际接口与打包验证确定，不由原型技术反推。
- 主下载包保持轻量，包含 UI、自动更新和组件管理等客户端能力；Cognee 引擎、原生依赖和模型作为独立组件自动准备。本轮 App 使用 Rust 原文 worker，Python 按相同原文契约完成对照；G2 代码图谱本机链路已通过，最终包与正式分发仍单独验收。分别报告主包大小、首次准备下载量与最终磁盘占用。
- 其他主机的记忆管理仅记录于 [Roadmap](docs/roadmap.md)，本次不设计或实现远程管理。
- 编辑与整理保留记录。AI 提出的合并、覆盖、删除、跨项目移动在确认前不得改变已有有效知识；恢复旧版本产生新版本。
- 保留用户无关修改。不要提交密钥、真实记忆库、聊天记录或本机 Agent 配置。
- 可交互设计原型归档于 `prototype/selected-ui` 分支；主分支保留产品规则与选定设计，生产实现按最终技术方案编写。

开发命令见 [开发说明](docs/development.md)，实际验收与限制见 [P0 验证记录](docs/p0-validation.md)。使用 `sh scripts/go.sh test ./...` 测试，`python3 scripts/build-client.py --out build/p0-final` 构建包含 helper/LaunchAgent 的本机验证包；工具链保存在仓库内，不替换用户默认环境。
