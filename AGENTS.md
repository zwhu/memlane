# Memlane

这是面向程序员的 macOS 记忆客户端，使用 Cognee 作为记忆引擎。桌面框架已选择 MyGo；P0 核心客户端、服务与组件链路、P1 手动记忆管理与实际窗口设计对照已通过。P2 已接通真实 Codex MCP、pi Extension 和 App Agent 管理，闭环与交付结果见 docs/p2-validation.md；P3-C1 正式服务和 P3-C2 App 手动合并审核已通过，见 docs/p3-c2-validation.md；P3-C3 摘要/标签、来源失效和逐项恢复已通过，见 docs/p3-c3-validation.md；P3-C4 修改建议与审核已通过，见 docs/p3-c4-validation.md；全局经验、计划和其余交付继续按 docs/p3-delivery-plan.md 实施。

- 开始工作前阅读 [产品定义](docs/product.md) 和 [设计决策](docs/design.md)，以已确认的产品规则为准。
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
