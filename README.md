# Memlane

一个面向程序员的 macOS 记忆客户端，让多个编码 Agent 共用项目知识，减少重复交代背景。

名字取自 **memory lane**。使用 Cognee 作为记忆引擎，把好看、简单使用和方便管理作为产品重点。这是一个个人 side project。P0–P2 核心客户端、真实记忆管理与 Codex/pi 共享已通过；P3 整理、审核、计划、恢复、使用记录和初始化的主要切片，以及 G2 代码图谱与 F1a–F1d 恢复切片已有本机证据。当前开发重点是把已认可的 A/B 重设计落到 Native App，并统一 Provider、pi/Codex 与 Magpie 的记忆处理配置。

## 产品方向

- 项目记忆与全局经验：多个 Agent 在同一仓库下共享知识，按范围检索。
- 手动新增与编辑：保留来源、修改记录和单条版本恢复。
- 手动与定期整理：自动提炼和归类，合并、覆盖、删除等建议经用户确认后生效。
- 全局总览：每日新增记忆热力图、最近新增、待确认建议。
- 项目代码图谱：手动启用、自动合并变化并保留来源与状态。
- 本地运行：管理记忆服务及其后台生命周期。
- 轻量下载包：主客户端包含 UI、自动更新和组件管理；Cognee 引擎与模型独立准备。本轮 App 使用 Rust 原文 worker，Python 归档按相同原文契约完成对照；最终包与正式分发仍待收口。

Codex、pi 与 traex 已分别取得限定版本的真实往返证据；TRAE IDE 真实模型仍待隔离登录验收。旧 qwen3:0.6b 桥保留“技术通过、事实质量失败”，最新 pi/DeepSeek V4.1 Flash 已通过完整 Schema、固定样本和第五包限定闭环。记忆数据保存在本地；使用云端模型的 Agent 处理内容时，相关输入仍会交给该模型服务。

## 当前材料

- [产品定义与信息架构](docs/product.md)
- [设计决策与界面](docs/design.md)
- [第一版技术方案](docs/technical-plan.md) — MyGo 轻量客户端、Go 服务、独立下载的 Cognee 运行时和本地数据库。
- [实施清单](docs/implementation-plan.md) — 从打包验证到首个跨 Agent 记忆闭环。
- [当前交付矩阵](docs/p3-delivery-plan.md) — 已通过切片、剩余工程与正式分发条件。
- [A/B 实施记录](docs/ui-redesign/implementation-20261109.md) — 最新重设计的真实工程状态与验收矩阵。
- [记忆处理配置账本](docs/memory-processing-ledger-validation.md) — schema17 持久配置与迁移验证。
- [记忆处理运行时记录](docs/processing-validation-runtime.md) — 在途 runner、Keychain 与协议适配边界。
- [Codex → Cognee → Memlane 动态流程](docs/cognee-codex-flow/README.md) — 原始会话、后台提炼、分层接收与当前版本读取的职责边界。
- [本地开发](docs/development.md) — 工具链准备、运行、测试与打包命令。
- [P2 验证记录](docs/p2-validation.md) — 真实 Codex→App→pi、连接管理、范围、恢复与故障、完整包和三类体积。
- [Agent 接入契约](docs/agent-contract.md) — MCP/CLI 工具、持久化回执、当前版本与授权范围。
- [P1 验证记录](docs/p1-validation.md) — App 主流程、真实活动、状态检查、实际系统截图与剩余分发条件。
- [P0 验证记录](docs/p0-validation.md) — 实测下载包大小、真实窗口验证和未完成事项。
- [后续 Roadmap](docs/roadmap.md) — 其他主机的记忆管理等后续方向，不纳入本次实现。
- [Cognee-RS 研究](docs/research-cognee-rust.md) — 官方 Rust 实现、依赖裁剪与功能验证边界。
- [Nowledge Mem 研究](docs/research-nowledge-mem.md) — 桌面客户端、实际下载体积与更新机制。
- [选定的可交互原型](https://github.com/zwhu/memlane/tree/prototype/selected-ui/prototypes/selected-ui) — 保存在 `prototype/selected-ui` 分支，使用内存中的示例数据。

![Memlane 首页设计](docs/images/home.jpg)

首页采用侧边栏加宽幅总览，记忆等详情页面采用三栏。新增、编辑、历史与建议确认在栏内完成。

## 当前实现与开发

已有 MyGo Native UI、独立 Go 服务、业务 SQLite、Rust/Python worker、组件管理、更新、整理审核、持久计划、有限恢复和代码图谱链路。Codex 首任务已在空业务根完成“官方采集 → 首材料登记项目 → 完整出处 → App 编辑 → 新会话读取当前版本”；pi/DeepSeek 第五包完成同根提炼与限定质量核对。schema17 已加入三种记忆处理方式的不可变配置账本和独立当前选择；新的验证所有者、Keychain 凭据版本与三协议 HTTP 适配已有测试，但尚未接入 daemon 私有 API、正式任务分派或 A/B Native 页面。

不同阶段包的体积口径分别保留在对应验证记录中；最新 pi/DeepSeek 隔离包和组件数据见 [pi/DeepSeek 验证](docs/pi-deepseek-validation.md)。当前仍只有本机 ad-hoc 签名；最终 F2 包、Developer ID、公证、Gatekeeper、正式更新源和干净 macOS 验收均未完成。

```sh
python3 scripts/bootstrap.py
sh scripts/go.sh mod download
sh scripts/go.sh test ./...
sh scripts/go.sh tool mygo dev
```

完整客户端验证包使用 `python3 scripts/build-client.py --out build/p0-final` 构建，包含 helper 与 LaunchAgent 后处理；源码、依赖清单和验证脚本入库，工具链、模型、运行数据及构建产物保留在忽略目录。开发与验收命令见 [开发说明](docs/development.md)。

当前下一阶段：先把真实验证 runner、凭据引用、共享执行槽位、状态/取消 API 和显式启用接进 daemon，再实现 A 三步配置与设置复用；随后落地 B 首页真实统计与任务状态，最后收口剩余 F1、F2 最终包与 F3 正式分发。未经确认不得改变有效知识的规则继续有效。

查看交互原型：

```sh
git switch prototype/selected-ui
python3 -I prototypes/selected-ui/serve.py
```

打开终端打印的本机地址。原型不连接真实 Agent 或 Cognee，刷新后恢复示例数据。查看完成后可用 `git switch main` 回到主分支。
