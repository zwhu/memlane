# Memlane

一个面向程序员的 macOS 记忆客户端，让多个编码 Agent 共用项目知识，减少重复交代背景。

名字取自 **memory lane**。使用 Cognee 作为记忆引擎，把好看、简单使用和方便管理作为产品重点。这是一个个人 side project，P0 核心工程链路、P1 手动记忆管理及 P2 真实 Codex/pi 共享已通过隔离合成数据验证，App 实际系统窗口已完成设计对照。

## 产品方向

- 项目记忆与全局经验：多个 Agent 在同一仓库下共享知识，按范围检索。
- 手动新增与编辑：保留来源、修改记录和单条版本恢复。
- 手动与定期整理：自动提炼和归类，合并、覆盖、删除等建议经用户确认后生效。
- 全局总览：每日新增记忆热力图、最近新增、待确认建议。
- 项目代码图谱：手动启用，后续自动更新。
- 本地运行：管理记忆服务及其后台生命周期。
- 轻量下载包：主客户端包含 UI、自动更新和组件管理；Cognee 引擎与模型独立准备。本轮 App 使用 Rust 原文 worker，Python 归档按相同原文契约完成对照；完整代码图谱仍待适配。

**Codex 0.147.0 MCP 与 pi 0.84.1 Extension** 已完成真实工具往返；TRAE、traex 留待后续适配。记忆数据保存在本地；使用云端模型的 Agent 处理内容时，相关输入仍会交给该模型服务。

## 当前材料

- [产品定义与信息架构](docs/product.md)
- [设计决策与界面](docs/design.md)
- [第一版技术方案](docs/technical-plan.md) — MyGo 轻量客户端、Go 服务、独立下载的 Cognee 运行时和本地数据库。
- [实施清单](docs/implementation-plan.md) — 从打包验证到首个跨 Agent 记忆闭环。
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

已有 MyGo Native UI 客户端、独立 Go 记忆服务、业务 SQLite、Rust/Python worker、组件管理与更新链路。P0 已验证原文、组件准备、后台生命周期及受控 HTTPS 更新；P1 已验证项目登记、来源、新增/编辑/搜索、历史/恢复与过期编辑保护。P2 已完成真实 Codex 保存 v1 → App 查看/编辑 v2 → pi 检索当前正文/来源/版本，另验证 App 恢复 v3、真实 pi 读取、项目/worktree 隔离、幂等重试及停服/重启。App 内 Agent 发现、连接、范围、实际验证与断开已接通，保留无关配置。首页来自实际首次可用活动，整理显示未接入空态；系统窗口完成设计对照，原型 HTML/CSS 仍只作设计参考。自动化均使用隔离配置和合成数据，未覆盖真实 Agent 设置或导入聊天。

当前本机 arm64 验证包：主 DMG **13.11 MB**、App 文件合计 **29.21 MB**；Rust 路线首次额外下载 **285.75 MB**，准备后 App 与实际运行目录普通文件合计 **520.94 MB**（含组件、模型、缓存与合成数据/设置）。三类体积分别统计，详见 [P2 验证记录](docs/p2-validation.md)。当前仅有 ad-hoc 签名；Developer ID、公证和正式更新源尚未配置。

```sh
python3 scripts/bootstrap.py
sh scripts/go.sh mod download
sh scripts/go.sh test ./...
sh scripts/go.sh tool mygo dev
```

完整客户端验证包使用 `python3 scripts/build-client.py --out build/p0-final` 构建，包含 helper 与 LaunchAgent 后处理；源码、依赖清单和验证脚本入库，工具链、模型、运行数据及构建产物保留在忽略目录。开发与验收命令见 [开发说明](docs/development.md)。

下一阶段 P3：固定版本快照、受限后台执行、结构化提案与栏内确认，保留未经确认不得改变有效知识的规则；代码图谱、其余 Agent 与正式分发在 P4。

查看交互原型：

```sh
git switch prototype/selected-ui
python3 -I prototypes/selected-ui/serve.py
```

打开终端打印的本机地址。原型不连接真实 Agent 或 Cognee，刷新后恢复示例数据。查看完成后可用 `git switch main` 回到主分支。
