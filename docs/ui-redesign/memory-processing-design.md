# Agent 接入与记忆处理：最新设计交接

2026-11-09，预览 revision 12。**用户已确认下面的产品取舍，画面仍待评审；本轮仅完成设计，不改正式 App。** 本文件替代之前将 API Key 作为唯一途径、将任意已发现 Agent 视为可整理、以及只用 MCP 配置解释连接的设计。

最新纠正：第一项任务与记忆结果不进入初始配置；初始化只有环境、Agent 连接、记忆处理三步，之后完成配置进入首页。「设为当前使用」移到页签下方方式状态栏右侧，默认可见。以 [最新边界](reusable-setup.md) 为准。

本轮补充：三种配置可同时保存、仅启用一种，初始化和后续设置复用同一配置页；环境准备页同样复用。设置中的当前使用、候选验证、独立保存、明确启用与取消，以及 Magpie 本机免密检测，以 [页面复用契约](reusable-setup.md) 为准。revision 8 保存即启用与 revision 7 本机访问凭据必填设计作废。

最新补充：用户以 Magpie 的模型选择设计为参考，并要求“我计划这里还支持一个模式就是直接支持magpie就行了”。当前稿增加「使用 Magpie」第三种处理方式；模型选择改为搜索、供应商筛选、分组列表、常用与选中勾选，供应商只配置一次。此前 revision 6 的 Provider 卡片目录退为历史设计；已连接 pi/Codex 的卡片选择继续保留。

## 两项配置

| 配置 | 用户选择与作用 | 前置条件 |
| --- | --- | --- |
| 连接 Agent | 给该电脑上的编码工具安装记忆 skill、hooks、插件、Extension 等适配内容；参考 Cognee Web UI 的对应步骤 | 不要求项目、仓库目录或提炼执行者 |
| 配置记忆处理 | 自配 LLM provider、已连接 pi/Codex、Magpie 可同时配置，选择一种当前使用 | Provider 可独立配置；Agent 候选必须已连接并符合后台处理能力；Magpie 默认自动检测本机网关，无需访问凭据 |

LLM provider 优先设计并作为后续优先实现方式。处理记忆的 Agent 候选只支持 **pi、Codex**；这个限制不自动缩减可安装记忆接入的编码工具目录。安装接入、验证连通、验证整理能力和授权具体任务分别反馈。

## 画面与状态

入口为原型分支 `prototypes/ui-redesign-20261109/a-agent.html?screen=organize&state=configure`。

- 「配置记忆处理」使用同页切换的三种方式：LLM provider、使用已连接 Agent、使用 Magpie。
- 最新反馈改为参考 Magpie 的模型目录：顶部搜索名称/ID，左侧供应商与常用筛选，右侧按供应商分组显示模型；选中行高亮并显示勾选，列表上方显示当前处理模型。模型与供应商不是一一对应，不为每个模型重复填写密钥。
- 自配 Provider 的「供应商配置」单独展示供应商列表、API 地址和密码输入。未配置供应商的模型先进入对应配置，保存后回到模型选择；新增或修改配置本身不代表模型推理验证通过。修改服务地址时需重新提供相应密钥，不继承旧服务凭据。
- Provider 与模型目录当前为设计示例，实际实现应来自适配器/供应商目录和真实配置。图中 DeepSeek、Kimi、GLM、OpenAI 模型名借鉴参考页，不宣称当前 Memlane 已验证这些模型，模型 ID 也不能由显示名称推断。此前 DSH 候选词义仍未确认，不作为已验证模型供应商映射。
- 已选模型与对应供应商配置满足条件后，才可「验证配置」。验证中、成功、失败原位更新；结构化输出通过后先保存配置，再明确设为当前使用，保存不自动切换。支持手动添加实际供应商提供的模型 ID，按供应商命名空间去重和绑定。
- Magpie 模式自动检测本机服务，连接后读取同一套分组模型列表；未运行时提供打开 Magpie 与重新检测，非默认本机地址放在连接设置中。默认不填写访问凭据；提供刷新与「在 Magpie 中管理」，不复制供应商 Key，也不在 Memlane 修改其供应商/账号/Agent 模型配置。
- Agent 页仅显示已连接的 pi/Codex，两张卡片显示图标、名称、版本和已连接状态；默认不自动选择，删除下拉框。选中后显示它自己的登录/模型与独立提炼会话说明，再明确验证；处理会消耗所选 Agent 的模型额度。
- Agent 验证区区分登录/模型条件、结构化输出、提炼会话隔离。任一项失败不得显示可用于整理。支持登录失效、额度不足、取消、采集隔离失败和没有已连接候选的画面。
- 连接页显示将安装的内容、安装、重载和接入验证。Codex 为官方记忆插件与 hooks；pi 沿用其实际 Extension 接入，skill 的安装内容另按支持格式落实。
- 项目、工作目录与材料范围在日常任务中处理，不进入 Agent 安装或 Provider 配置页。已验证并启用的方式供后续任务复用。
- 三步进度为「准备环境／连接 Agent／配置记忆处理」，最后使用「完成配置」。已认可安装正文保留，最新三步导航单独归档；不再出现首任务与记忆结果步骤。
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

Magpie 模式将 Cognee 模型请求发到其网关。已核对官方仓库提交 `ed0ab567cba78fae8ea9d9c9a07a7446e315b787` 的 `docs/reference.md` 与 `internal/gateway/model_api_test.go`：本机示例地址为 `http://127.0.0.1:3425/v1`，OpenAI 格式模型目录为 `GET /v1/models`，网关提供 `/v1/chat/completions` 等兼容请求。本机 loopback 访问网关自身端口无需密钥；网络/代理访问的鉴权条件不同，本轮不设计远程连接。真实发现和地址由实际运行状态确定。

客户端使用网关实际返回的模型 ID/供应商信息，记录 Magpie 来源；不把 Codex 专用目录 `/v1/codex/models` 与普通目录混用，不凭显示名拼接真实路由。图中的 ID 为演示数据，尚未实际连接 Magpie。原始模型调用、Schema、错误、取消和使用量仍需单独真实验收。Magpie 可能按自身账号/路由策略处理回退，Memlane 不能将未报告的实际模型冒称固定命中所选模型。

供应商和模型图标参考 `https://usemagpie.ai/zh/` 及其 `/icons/` 资源；在本稿以灰度呈现，沿用 Memlane 已确认的浅色风格。参考页面已只读获取，公开截图已实际打开；没有启动、安装或读写用户本机 Magpie 配置。

- 提炼使用受管独立上下文，仅接收此次获准材料；不继承日常编码会话、其他仓库或历史聊天。
- 提炼会话关闭记忆采集 hooks/插件及知识变更工具，不能仅用 prompt 声明不采集。用受信来源/任务身份标识内部提炼请求，采集入口须排除这些请求与输出。
- 临时线程、只读 sandbox、空 dynamicTools、禁止审批均不能单独证明完整隔离。需验证实际配置、工具暴露和事件，确认没有新增采集会话、递归提炼或额外写入。
- Schema 校验、单并发/受限槽位、超时、取消、终态与迟到结果拒绝由一个明确拥有者管理；不重复累计调用或将取消 ACK 当完成。
- 处理结果按 Cognee/Memlane 的材料接收、来源、版本和审核契约应用；不得由桥接 Agent 直接覆盖、删除或移动已有知识。
- 如果 Magpie 所选模型自身使用 Agent/订阅桥接，也需证明其中的内部推理不会触发 Memlane/Cognee 记忆采集。不能因请求发往网关就跳过已有隔离与防循环要求。
- pi 与 Codex 的具体协议、版本、结构化输出和隔离分别验收；本稿可选择它们，不表示实际后台桥接已通过。首个 Codex app-server 方案和官方 Claude 桥接参考见 [协议研究](../research-cognee-agent-appserver.md)。

## 后续实现与证据

配置必须区分未保存、保存但未验证、可用、失效。保存与验证失败保留原有效配置及输入；更换 Provider/Agent 不复用另一方凭据，运行中任务冻结所选处理方式与代次。密钥不回显、不写日志、URL、截图或仓库，遵守客户端的私有存储约束。

正式实现优先交付 LLM provider。Agent 模式限 pi/Codex，实际处理能力通过后才开放。用户最后要求“先把设计做出来”，本轮未修改生产代码、安装用户插件或执行模型请求。

当前原型核验与截图使用 `artifacts/setup-final/`、`node verify-setup-final.cjs`，覆盖三步初始化、首任务和结果移除、方式状态栏启用按钮，以及三份配置、独立保存与启用、本机 Magpie、两页复用。`artifacts/processing-modes/` 为 revision 9，`artifacts/setup-reuse/` 为 revision 8，`artifacts/model-picker/` 为 revision 7，`artifacts/memory-processing/` 为 revision 5/6 历史证据；当前保存/验证仍为设计演示，不代表真实持久化、模型或 Magpie 调用。
