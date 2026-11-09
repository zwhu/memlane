# Memlane

这是面向程序员的 macOS 记忆客户端，使用 Cognee 作为记忆引擎。桌面框架已选择 MyGo；P0 核心客户端、服务与组件链路、P1 手动记忆管理与实际窗口设计对照已通过。P2 已接通真实 Codex MCP、pi Extension 和 App Agent 管理，闭环与交付结果见 docs/p2-validation.md；P3-C1 正式服务和 P3-C2 App 手动合并审核已通过，见 docs/p3-c2-validation.md；P3-C3 摘要/标签、来源失效和逐项恢复已通过，见 docs/p3-c3-validation.md；P3-C4 修改建议与审核已通过，见 docs/p3-c4-validation.md；P3-E 全局经验复制/提炼、条件和来源范围已通过，见 docs/p3-global-validation.md；P3-D1 持久计划、明确启停与真实槽位/取消已通过，见 docs/p3-schedule-validation.md；D2a 持久租约/进程保护已通过，见 docs/p3-recovery-validation.md；D2b 有限自动重试/完整恢复和其余交付继续按 docs/p3-delivery-plan.md 实施。

- 开始工作前阅读 [产品定义](docs/product.md) 和 [设计决策](docs/design.md)，以已确认的产品规则为准。
- UI 重设计开发先读 [设计交接入口](docs/ui-redesign/README.md)；A01–A04 按 [已认可的环境安装设计契约](docs/ui-redesign/a-environment.md) 与截图实现。确认和安装进度同页，组件卡片内显示进度，主按钮为「安装并继续」；A05–A10 与 B 后续稿尚未在本轮认可，不得把示例下载数字或原型状态当成真实结果。
- Agent 连接按 [电脑级配置规则](docs/ui-redesign/agent-connection-contract.md)：先将 Memlane 配置到当前电脑的编码 Agent，连接不以项目、工作目录或首任务模型为前置条件；项目/任务范围另行确认。现有项目级适配不能被描述为电脑级配置已实现。
- Agent 连接页使用多选卡片批量接入，逐项同页反馈安装、重载、验证与结果；已连接/不支持项不可重复提交，只重试失败项，取消保留成功项。处理记忆仍只选一个已连接 pi/Codex；页面删除登录/模型和提炼会话的技术说明，仅留当前模型与账号额度提示，隔离要求继续保存在开发契约。
- 连接与处理记忆按 [最新设计交接](docs/ui-redesign/memory-processing-design.md)：连接参考 Cognee Web UI 安装 skill/hooks/插件等；处理方式为自配 LLM provider、已连接 pi/Codex、Magpie 网关。模型目录参考 Magpie，供应商配置复用；Magpie 上游密钥由其管理。整理会话必须隔离采集以防循环；本轮是待评审设计，不宣称真实桥接、Provider 或 Magpie 已交付。
- 环境与组件、记忆处理按 [页面复用契约](docs/ui-redesign/reusable-setup.md) 同时用于初始化与后续设置；三种配置可同时保留，仅一个当前使用，保存配置与明确启用分开，切换页签不启用。Magpie 默认自动检测本机网关，无需访问凭据；环境准备复用已有组件与后台任务，不重走首任务。
- 初始配置最新边界同样以 [页面复用契约](docs/ui-redesign/reusable-setup.md) 为准：仅「准备环境／连接 Agent／配置记忆处理」三步，完成后进入日常首页；第一项任务与记忆结果移出初始化。此前产品文档和验收中将首任务列为初始化步骤的安排属于旧版本，不覆盖本轮纠正。「设为当前使用」置于方式状态栏右侧。
- 开发或验收本地 Agent 模型桥接前，先读 [桥接需求与方案](docs/agent-model-bridge-requirements.md)、[研究依据](docs/research-cognee-agent-appserver.md) 和 [pi 实现与测试记录](docs/pi-model-bridge-validation.md)。pi 0.84.1 / Ollama 的完整包技术链路已通过，qwen3:0.6b 事实质量未通过；测试复用真实默认模型并替换为隔离服务地址，原始 provider 配置直接复用及云端认证尚未实测。按现有实现继续，不把后续界面设计、其他 provider、Codex app-server 模型桥或 Magpie 视为已实现；复现入口与未通过项以验证记录为准。
- 开发或验收 UI 前，实际打开 [首页设计图](docs/images/home.jpg) 和 [三栏详情设计图](docs/images/memory-detail.jpg)，不能只读 Markdown 图片路径。完成后对照实际窗口截图；工具无法看图时明确视觉验收未完成。
- 实现依据 [技术方案](docs/technical-plan.md) 与 [实施清单](docs/implementation-plan.md)；区分已选框架、待验证依赖和已通过的验收。
- 文档默认中文。区分已实现、原型演示与待验证能力，不将示例状态描述为真实接入结果。
- 首页保留宽幅总览；列表与详情页采用三栏；普通操作在栏内完成。保持紧凑排版、单色图标和按钮、连续浅蓝侧边栏。
- 不自动扩大首版范围。技术选型通过实际接口与打包验证确定，不由原型技术反推。
- 主下载包保持轻量，包含 UI、自动更新和组件管理等客户端能力；Cognee 引擎、原生依赖和模型作为独立组件自动准备。本轮 App 使用 Rust 原文 worker，Python 按相同原文契约完成对照，完整代码图谱仍待适配。分别报告主包大小、首次准备下载量与最终磁盘占用。
- 其他主机的记忆管理仅记录于 [Roadmap](docs/roadmap.md)，本次不设计或实现远程管理。
- 编辑与整理保留记录。AI 提出的合并、覆盖、删除、跨项目移动在确认前不得改变已有有效知识；恢复旧版本产生新版本。
- 保留用户无关修改。不要提交密钥、真实记忆库、聊天记录或本机 Agent 配置。
- 可交互设计原型归档于 `prototype/selected-ui` 分支；主分支保留产品规则与选定设计，生产实现按最终技术方案编写。

开发命令见 [开发说明](docs/development.md)，实际验收与限制见 [P0 验证记录](docs/p0-validation.md)。使用 `sh scripts/go.sh test ./...` 测试，`python3 scripts/build-client.py --out build/p0-final` 构建包含 helper/LaunchAgent 的本机验证包；工具链保存在仓库内，不替换用户默认环境。
