# Agent 接入与记忆处理：最新设计交接

2026-11-09，预览 revision 5。**用户已确认下面的产品取舍，画面仍待评审；本轮仅完成设计，不改正式 App。** 本文件替代之前将 API Key 作为唯一途径、将任意已发现 Agent 视为可整理、以及只用 MCP 配置解释连接的设计。

## 两项配置

| 配置 | 用户选择与作用 | 前置条件 |
| --- | --- | --- |
| 连接 Agent | 给该电脑上的编码工具安装记忆 skill、hooks、插件、Extension 等适配内容；参考 Cognee Web UI 的对应步骤 | 不要求项目、仓库目录或提炼执行者 |
| 配置记忆处理 | 默认 LLM provider；另一项为使用已连接的 pi 或 Codex | Provider 可独立配置；Agent 候选必须已连接并符合后台处理能力 |

LLM provider 优先设计并作为后续优先实现方式。处理记忆的 Agent 候选只支持 **pi、Codex**；这个限制不自动缩减可安装记忆接入的编码工具目录。安装接入、验证连通、验证整理能力和授权具体任务分别反馈。

## 画面与状态

入口为原型分支 `prototypes/ui-redesign-20261109/a-agent.html?screen=organize&state=configure`。

- 「配置记忆处理」使用同页切换的两种方式：LLM provider、使用已连接 Agent。
- Provider 页包含 Provider、模型名称、API 地址、API Key。未填写完整或地址格式无效时「保存并验证」不可执行；密钥使用密码输入。验证中、成功、失败原位更新，成功后可修改配置或继续首任务。
- Provider 名称与支持协议需要后续按真实适配能力核对。原型中的 OpenAI 兼容接口、OpenAI、Anthropic、DeepSeek 选项是设计目录，不是这版正式 App 已验证所有供应商的承诺。
- Agent 页仅显示已连接的 pi/Codex，默认不自动选中。使用它自己的登录与模型配置；明确验证与处理会消耗其模型额度。
- Agent 验证区区分登录/模型条件、结构化输出、提炼会话隔离。任一项失败不得显示可用于整理。支持登录失效、额度不足、取消、采集隔离失败和没有已连接候选的画面。
- 连接页显示将安装的内容、安装、重载和接入验证。Codex 为官方记忆插件与 hooks；pi 沿用其实际 Extension 接入，skill 的安装内容另按支持格式落实。
- 项目与工作目录仍在实际首任务中处理，不进入 Agent 安装或 Provider 配置页。已验证的处理方式在任务页面复用。
- 四步进度为「准备环境／连接 Agent／设置整理方式／第一项任务」。原安装稿的视觉仍保留；步骤名称衔接随最终评审落实。
- 控件固定在顶部/底部，中部滚动；窄窗口将 Provider 字段改为单列。评审工具栏和「下一状态」不进入正式产品。

## 参考依据与边界

本轮只读核对 Cognee Web UI 提交 `e93a4f0c76e6af27142c25189f14c200e1749d51`：

- `cognee-frontend/src/modules/integrations/agentCards.tsx`：Codex 的启用 hooks、插件市场、记忆插件、保存/召回步骤；其他接入按对应工具使用 skill、MCP 或插件。
- `cognee-frontend/src/app/(app)/integrations/partials/SetupWizardModal.tsx`：分步呈现与步骤切换。Memlane 在主内容区承载步骤，继续遵守紧凑浅色布局。
- `useAgentConnectionStatus.ts`：参考页按有限 session 记录和 sticky 标记显示连接。Memlane 必须使用当前安装、连接代次和真实回执，不能照搬历史标记来判定当前可用。
- `cognee-frontend/public/visuals/logos/codex.svg`：本稿 Codex 图标取自 Cognee 接入页资源。pi/TRAE 的其余资源来源保留在原型 `icon-sources/agent-sources.json`。

本轮 `localhost:3000/integrations` 未运行，未完成参考页的现场视觉验收；已检查真实页面源码。设计稿本身已实际浏览器渲染并查看截图。

Cognee Web UI 的服务访问 API Key 与 LLM provider 的模型 Key 是不同用途。Memlane 本机服务访问凭据由 App 管理；不能把参考页的 Cloud 凭据输入直接搬成用户必须提供的模型 Key。

## Agent 桥接与防循环

Agent 处理模式下，Cognee 的模型请求经 Memlane 管理的适配层交给所选 pi/Codex，返回经 Schema 校验的结果；Agent 自己管理登录和模型调用，用户无需为这一模式额外填写云模型 Key。Provider 模式直接使用用户配置的模型接口，两者独立保存和验证，不自动故障切换。

- 提炼使用受管独立上下文，仅接收此次获准材料；不继承日常编码会话、其他仓库或历史聊天。
- 提炼会话关闭记忆采集 hooks/插件及知识变更工具，不能仅用 prompt 声明不采集。用受信来源/任务身份标识内部提炼请求，采集入口须排除这些请求与输出。
- 临时线程、只读 sandbox、空 dynamicTools、禁止审批均不能单独证明完整隔离。需验证实际配置、工具暴露和事件，确认没有新增采集会话、递归提炼或额外写入。
- Schema 校验、单并发/受限槽位、超时、取消、终态与迟到结果拒绝由一个明确拥有者管理；不重复累计调用或将取消 ACK 当完成。
- 处理结果按 Cognee/Memlane 的材料接收、来源、版本和审核契约应用；不得由桥接 Agent 直接覆盖、删除或移动已有知识。
- pi 与 Codex 的具体协议、版本、结构化输出和隔离分别验收；本稿可选择它们，不表示实际后台桥接已通过。首个 Codex app-server 方案和官方 Claude 桥接参考见 [协议研究](../research-cognee-agent-appserver.md)。

## 后续实现与证据

配置必须区分未保存、保存但未验证、可用、失效。保存与验证失败保留原有效配置及输入；更换 Provider/Agent 不复用另一方凭据，运行中任务冻结所选处理方式与代次。密钥不回显、不写日志、URL、截图或仓库，遵守客户端的私有存储约束。

正式实现优先交付 LLM provider。Agent 模式限 pi/Codex，实际处理能力通过后才开放。用户最后要求“先把设计做出来”，本轮未修改生产代码、安装用户插件或执行模型请求。

原型 `artifacts/memory-processing/` 保存13个状态/窄窗口截图、pi成功示例截图和 `verification.json`。核验通过 Provider 默认、配置条件、原位验证状态、Provider 不依赖 Agent、候选限制、pi选中保留和隔离失败阻止继续；运行时错误0。原型保存/验证仅为示例，不代表真实持久化或模型调用。
