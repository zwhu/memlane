# UI 重设计：开发 Agent 入口

**2026-11-09，A revision 13 与 B 日常首页 revision 5 已由用户确认归档。**

后续实现从 [A 已认可设计实施交接](a-setup-handoff.md) 开始。该文档写明最终范围、三步流程、全部页面/按钮/状态、配置所有权、真实适配要求、代码入口、开发顺序与 V01–V14 验收清单。再实际打开 [选定截图与证据](../images/ui-redesign/a-setup/README.md)，不能只阅读图片路径。

B 首页实施从 [B 已认可设计交接](b-home-handoff.md) 与 [固定截图／动效](../images/ui-redesign/b-home/README.md) 开始。C Agent 设置与项目知识整理 revision 7 已认可，固定原型 `35a5bad5f5398f2c9cf3073b673983f5dab0a48c`：Agent 连接位于设置，空知识列表提供「整理项目」，右栏明确Token消耗与可选历史会话，已有知识隐藏空态大按钮并通过项目菜单补充，可一直跳过主动整理；记忆为总称，以知识／原始记录分别展示。实施先读 [C 交接](c-agent-handoff.md)、[入口与范围](agent-settings-placement.md) 与 [内容分层](content-model.md)。开发本稿及后续所有动效先读 [MyGo 动效实施与复用](motion-implementation.md)。

## 已确认范围

| 分组 | 设计状态 | 实施依据 |
| --- | --- | --- |
| A 环境检测、安装确认与同页进度 | 已认可；原环境正文 revision 11 保留，导航按最新三步 | [环境契约](a-environment.md)、[A 交接](a-setup-handoff.md) |
| A Agent 连接 | revision 13 已认可：多选批量接入、逐项回执、仅失败项重试、取消保留成功 | [批量连接契约](agent-connection-contract.md) |
| A 记忆处理与完成配置 | revision 13 已认可：Provider、已连接 pi/Codex、Magpie；三份保存、一个当前使用，保存与启用分开 | [处理契约](memory-processing-design.md) |
| 环境与记忆处理的设置复用 | 已认可：恢复已有组件、任务和配置，不重走初始化 | [复用契约](reusable-setup.md) |
| B 日常首页 | revision 5 已认可：①宽幅首页、累计统计、日期筛选、具体处理配置、通用标题导航去重和整理按钮动效 | [B 交接](b-home-handoff.md)、[证据](../images/ui-redesign/b-home/README.md) |
| C Agent 设置与项目知识任务 | 入口位置已确认，完整稿待评审；设置 → Agent 连接；仅项目知识空态提供分析入口，Token提示、可选历史会话，完整稿继续评审 | [场景](scenarios.md)、[电脑级规则](agent-connection-contract.md) |
| D–O | 仅场景枚举及各自已有规则 | [全部场景](scenarios.md) |

初始化只有「准备环境 → 连接 Agent → 配置记忆处理」，之后完成配置进入首页。项目、第一项任务和记忆结果不再属于初始化；建立项目知识在日常项目空态独立触发。批量连接不等于多个处理执行者。三个方式页签只显示单行名称；「设为当前使用」位于方式状态栏右侧。

## 当前工程状态

设计认可与生产实现继续分开记录。pi/DeepSeek 的限定同包闭环已经通过；A1 schema17 配置账本、不可变修订、独立当前选择、完整包 daemon 迁移，以及 daemon 所有的真实 pi 验证／活动取消／显式启用／重启保持也已通过。Keychain 凭据版本与三协议 HTTP 适配仍只有基础和夹具，尚未取得真实独立 Provider/Magpie 推理证据；正式任务分派与 Native 页面也未完成。

A V01–V06、V10–V14 和 B 新首页仍未通过生产验收。当前矩阵、失败保留和下一步见 [A/B 实施记录](implementation-20261109.md)、[配置账本验证](../memory-processing-ledger-validation.md)与[运行时在途记录](../processing-validation-runtime.md)。不得用这些基础结构恢复旧初始化流程，或把技术夹具写成新设计已交付。

## 固定归档与证据

- 可交互原型分支：`prototype/selected-ui`。
- A 选定原型提交：`e27a49e35020972d6aea4ab070ec898fb6c9c22e`，路径 `prototypes/ui-redesign-20261109/`。
- B 选定原型提交：`ed6cd37858c5d6f56f9307ecbf6ac454bb6e1edb`，`b-home.html?variant=1`；主分支固定38张PNG、1张GIF及核验JSON和来源清单。②③仍是探索。
- 原环境正文冻结提交：`5f4c504bfbc6ee07a571b015f7ccc809d0943848`，原图及旧导航留作对应版本证据。
- 主分支保存44张当前截图、27状态核验 JSON 与 [归档清单](../images/ui-redesign/a-setup/archive.json)，包括固定来源及 SHA256。
- 启动、当前预览路由和原型核验命令见 [A 交接](a-setup-handoff.md#2-固定版本与打开方法)。原型分支以后推进时，仍按固定提交核对本轮设计。

本轮是设计归档，生产 UI、批量连接与三种实际模型后端按各自工程证据验收。原型输入/保存均是内存示例，完成按钮只演示正式出口，不能代替持久化、真实接入、实际窗口和知识质量验收。完整交接不授权现在扩大生产开发范围。

## 文档冲突的处理

本次最终设计及专项契约在本范围内替代旧的四步/首任务初始化、单选后独立连接页、Magpie 本机凭据必填、保存即启用、方式页签状态小字，以及登录/模型与提炼会话说明表格。旧 HTML 函数和测试保留为历史，不移植到正式初始化。

旧产品、技术及验收文档中的已实现结果继续有效于对应版本，不能用它们恢复已被用户否定的流程，也不能将新设计标成已经实现。统一 Cognee/BGE 准备、E5/BGE 配方与索引边界继续遵循环境和技术契约；实际下载、占用、模型目录、Agent 版本及可用性来自真实状态。

日常首页继续采用宽幅总览，普通列表与详情采用三栏，普通操作在栏内完成，页面不能重新加入用户已删除的无关备注。通用规则见 [产品定义](../product.md)、[设计决策](../design.md)。
