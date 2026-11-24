<div align="center">

<img src="web/website/icon.png" width="96" alt="Memlane 图标">

# Memlane

**换个 Agent，接着做。**

面向程序员的 macOS 本地记忆客户端：让多个编码 Agent 共享项目记忆，每条记忆都能追溯到当时的原始对话。

[![Release](https://img.shields.io/github/v/release/zwhu/memlane?include_prereleases&label=release)](https://github.com/zwhu/memlane/releases)
[![Platform](https://img.shields.io/badge/platform-macOS%2026%2B%20%C2%B7%20Apple%20Silicon-lightgrey)](#安装)
[![License](https://img.shields.io/badge/license-Apache--2.0-blue)](LICENSE)

简体中文 · [English](README.en.md)

</div>

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="web/website/demo-overview-dark.png">
  <img src="web/website/demo-overview.png" alt="Memlane 总览界面（演示数据）">
</picture>

## 为什么需要 Memlane

同一个项目，今天用 Codex 排查问题，明天换 Antigravity 改代码。每个 Agent 各记各的，换一个就得从头讲一遍：项目约定、踩过的坑、上次为什么这么改。

Memlane 把这些项目知识沉淀在你自己的 Mac 上，供所有接入的 Agent 按需召回；同时给你一个独立的原生界面，查看、搜索和管理这些记忆。

## 核心特性

- **跨 Agent 共享项目记忆**：在一个 Agent 里确认的约定，换一个 Agent 也能在需要时找到。
- **可追溯的出处**：每条记忆都保留记忆点和来源日期，可以回到当时的问答与工具操作，看懂约定为什么存在。
- **用已有的 Agent 额度提炼**：可直接借用本机已登录 Agent 的模型（Coding Plan 或订阅额度）整理记忆，无需另购 API；也支持直连 LLM Provider 或本地 Ollama。
- **本地优先**：记忆库、向量与图谱都存放在本机，向量模型（`bge-small-zh-v1.5`）完全离线运行。
- **按项目授权**：Agent 接入本机不等于获得项目权限，每个项目单独授权，通用记忆与用户画像分开保存。
- **原生 macOS 客户端**：总览、项目记忆、通用记忆、用户画像、原始记录、知识图谱和 `⌘K` 全局搜索，支持浅色与深色外观。

## 支持的 Agent

| Agent | 状态 |
| --- | --- |
| Codex | ✅ 已支持 |
| Antigravity（Gemini App） | ✅ 已支持（目前覆盖任务 / CodeMode 新会话） |
| pi | ✅ 已支持 |
| TraeX | ✅ 已支持 |
| Claude Code、Cursor、OpenCode | 🚧 规划中 |

## 安装

**系统要求**：Apple Silicon（M1 及以上）、macOS 26 或更高版本；首次准备约需 4–6 GiB 磁盘空间。

1. 从 [GitHub Releases](https://github.com/zwhu/memlane/releases) 下载最新的 `.dmg`，把 Memlane 拖入「应用程序」。
2. 内测版使用 ad-hoc 签名、尚未经 Apple 公证，首次打开会被系统拦截：前往 **系统设置 → 隐私与安全性**，点击「仍要打开」。
3. 按首次启动引导准备运行环境（支持国内镜像源 / 国外官方源，所有下载均校验固定 SHA256），然后在设置中接入你的 Agent。

完整步骤、已知限制与卸载方法见 [内测安装指南](docs/product/beta-install-guide.md)。

## 工作原理

![Memlane 系统架构](docs/images/memlane-architecture-20261120.svg)

- **原生桌面端**：基于 [MyGo](https://github.com/egoist/mygo) 的 macOS 原生客户端，只通过本地回环接口读取数据，不缓存正文副本。
- **接入网关**：管理项目注册、Agent 连接、采集与提炼队列，并在外置网关中统一执行项目权限校验与召回合并。
- **记忆引擎**：固定版本的官方 [Cognee](https://github.com/topoteretes/cognee)（`1.6.7`）。Memlane 不修改上游源码，所有扩展都放在外置网关、外置存储与插件层，便于原样升级。

详细设计见 [架构总览](docs/architecture/overview.md)。

## 隐私与数据

- 记忆正文、向量与图谱都保存在本机，不上传到 Memlane 的任何服务器（Memlane 本身没有云端服务）。
- 提炼记忆时，相关对话内容会发送给**你选择的**模型：本机 Agent 的模型代理、你配置的 LLM Provider，或本地 Ollama。
- 首次准备运行环境时会从镜像源或官方源下载依赖，「检查更新」只会访问 GitHub Releases。

## 从源码构建

开发环境需要受管 Python 3.12（由 `scripts/dev.py prepare` 准备）与仓库自带的 Go 工具链脚本：

```sh
# 启动后台服务与桌面窗口（runtime.json 为本机私有配置）
.local/cognee-python/bin/python scripts/dev.py start --config /path/to/runtime.json

# 测试与静态检查
sh scripts/go.sh test ./...
python3 scripts/task.py -- python3 scripts/lint.py
```

首次准备、运行配置、运维命令与测试说明见 [开发说明](docs/architecture/development.md)；提交前请先执行 `sh scripts/install-hooks.sh` 启用仓库钩子，协作约定见 [工程规范](docs/engineering/workflow.md)。

## 项目状态

Memlane 目前处于**公开内测**阶段（`v0.1.0-beta`），功能与数据格式仍可能调整。后续计划见 [Roadmap](docs/product/roadmap.md)，问题反馈请提交 [Issue](https://github.com/zwhu/memlane/issues)。

## 文档

| 面向 | 文档 |
| --- | --- |
| 用户 | [内测安装指南](docs/product/beta-install-guide.md) · [产品定义](docs/product/product.md) · [内容分层](docs/product/content-model.md) |
| 开发者 | [架构总览](docs/architecture/overview.md) · [开发说明](docs/architecture/development.md) · [Agent 接入契约](docs/architecture/agent-connection-contract.md) · [工程规范](docs/engineering/workflow.md) |
| 设计 | [设计决策](docs/design/design.md) · [视觉规范 V4](docs/design/visual-standards-v4-20261120.md) |

全部文档索引见 [docs/README.md](docs/README.md)，领域术语见 [CONTEXT.md](CONTEXT.md)。

## License

Memlane 基于 [Apache License 2.0](LICENSE) 开源，归属声明见 [NOTICE](NOTICE)。第三方组件与模型（包括 Apache-2.0 授权的 Cognee）的许可信息见 [THIRD_PARTY_LICENSES.md](THIRD_PARTY_LICENSES.md)。
