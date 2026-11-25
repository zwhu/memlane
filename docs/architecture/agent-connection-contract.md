# 电脑级配置与 Agent 接入契约

本文定义 Memlane 的宿主 Coding Agent 接入、电脑级身份隔离、官方集成优先及设置界面状态契约。统一领域术语以 [CONTEXT.md](../../CONTEXT.md) 为准。

当前界面正式开放 **5 类宿主**：**Codex**、**Antigravity（Gemini App）**、**pi**、**TraeX（TRAE CLI）** 与 **TRAE CN（IDE）**。OpenCode、Claude Code 与 Cursor 保留后端兼容与既有已配置记录，待真机闭环后逐步开放。

## 1. 官方集成优先与最小改写原则

- **复用官方集成**：采集与召回优先复用目标 Agent 的 Cognee 官方集成，不新增并行采集器或第二次上下文注入；后续扩展大量宿主按官方 SDK / RPC / ACP / 结构化 CLI 协议演进（参见 [多 Agent 驱动协议](../research/agent-driving-protocols-20261118.md)），不改变官方采集优先规则。
- **入口分别表达**：同一宿主下的 CLI 与 Desktop（如 Codex CLI/Desktop、TRAE CLI 与 TRAE CN IDE）分别表达连接与采集状态，不以一个入口的回执替代另一个。
- **仅改受管项**：遵循官方安装、信任与重载流程，只修改明确属于 Memlane 的受管条目，严格保留用户第三方插件、MCP、模型配置、账号登录与历史会话；不建设通用安装管理平台。

## 2. 电脑级连接与零项目授权红线

- **电脑级身份无前置依赖**：接入是电脑级连接，不以项目、工作目录、首次任务或提炼模型配置为前置。
- **零项目授权红线（硬约束）**：电脑级身份准备（`prepare()`）仅建立该 Agent 实例的独立官方子主体与私有凭据，**严禁据此向任何已有项目、通用记忆或受保护数据集（如 `ml-user-profiles`）自动授予访问权限**。项目关联与权限必须在项目「管理」页显式配置，不得猜测无明确工作区来源的材料归属。
- **独立主体与 ACL 边界**：所有有效接入指向同一个本机服务，每个宿主类型与独立 profile 持有各自官方子主体与独立 `X-Api-Key`（管理 owner 凭据留在接入网关控制侧）。逐项目权限按 PA 契约执行：`none` / `read` / `maintain`（`maintain` 含 `read/write/delete`，不授 `share`）。服务不可达时严禁回退使用 owner、离线 CLI 或第二套日常库。

## 3. 各宿主接入契约

### 3.1 Codex 与 pi

- **Codex**：基于 `auth_home` 固定安装实例，通过受管 `tomlkit` 原子更新 `~/.codex/` 配置并保留注释与其他插件；同一 `auth_home` 下的 CLI 与 Desktop 共享独立子主体（不提供 session 级权限隔离）。支持作为 Agent 模型代理执行后台记忆提炼。
- **pi**：接入 `~/.pi/agent/extensions/memlane-cognee` 受管扩展；作为 Agent 模型代理时需显式指定已有 `deepseek` 或 `ollama` 作为 `bridge_provider`，绝不向 Provider 账本复制宿主密钥。

### 3.2 Antigravity（Gemini App）

- **安装与配置目录**：宿主类型为 `antigravity`（`Gemini.app` 与原生 `agy` 辅助程序，自动扫描 `PATH`、标准用户 bin 与 `Gemini.app/Contents/Helpers/agy`）；电脑级配置目录固定为 `~/.gemini/config`（拒绝自定义非标准路径），配置文件为 `~/.gemini/config/config.json`，受管派生插件目录为 `~/.gemini/config/plugins/memlane-cognee`（基于固定官方 `integrations/antigravity/` 与共享 `_coding_memory.py` 构造，保留其他插件；检测到冲突旧 Cognee 插件时拒绝覆盖并要求人工复核）。
- **入口与采集范围**：`entrypoints` 为 `['desktop']`，自动采集面向 Gemini App task / CodeMode 的**新会话**（不自动回采启用前历史会话，普通闲聊尚待后续核验）；通过 `antigravity_hook_policy.py` 执行实时策略门禁、采集与召回，并在 `~/.cognee-plugin/antigravity-<instance_id>/last-hook-error.json` 只读暴露最近一次有效 hook 错误供诊断。
- **升级保护**：对已激活实例重新准备升级时必须核对 `upgrade_review_sha`，未经用户确认不得覆写活动代次插件。

### 3.3 TraeX（TRAE CLI）问答采集

- `Stop` 与 `SessionEnd` 回读当前会话的 rollout，复用 Cognee 的 Codex 解析与问答配对，补齐已写入日志的中断提问与阶段回复；运行中追加提问各自参与配对，工具调用仍由 TraeX 原生工具事件采集。
- 回读仅限于当前 TraeX profile 中与宿主会话 ID 匹配的日志，受项目采集授权与暂停水位约束；升级后首次恢复旧会话从切换时的水位继续采集。TRAE CLI 使用 `traex` 身份并可选作后台记忆提炼的 Agent 模型代理。

### 3.4 TRAE CN（IDE）接入规范

- **独立身份命名**：Agent 连接界面分别显示 **TRAE CLI** 与 **TRAE CN（IDE）**，项目权限按连接身份独立设置。TRAE CLI 使用既有 `traex` 身份并可选作记忆提炼处理器；TRAE CN（IDE）使用独立的 `trae_ide` 采集身份。
- **受管 Hook 合并**：经用户明确确认后，Memlane 在 `~/.trae-cn/hooks.json` 原子合并经校验的 4 事件受管命令组（`SessionStart`、`UserPromptSubmit`、`PostToolUse`、`Stop`），保留用户已有第三方 Hook 组；升级自旧版 6 事件配置时支持原子迁移。
- **采集、召回与提炼边界**：先在 Memlane 授权对应项目，再在 TRAE CN 打开该单根工作区并新建会话（在「设置 → Hooks → 全局开关」确认已开启）。适配器使用 IDE 的提问、工具完成与回复结束事件实时采集，并在提问时按项目权限自动召回；Hook 在默认沙箱内经回环接口转发，由接入网关按 `trae_ide` 身份鉴权保存。多根工作区无法从全局 Hook 工作目录确认会话归属项目时自动跳过。后台记忆提炼继续由已配置的 Agent 模型代理（如 `codex` / `traex` / `pi` / `agy`）或直连 LLM Provider / Ollama 执行。

## 4. 界面状态、提炼隔离与设置入口规范

### 4.1 真实事实表达与记忆提炼隔离

- **状态不混同**：界面仅呈现已安装、已准备、待重载/新会话、已连接或需核对等真实连接事实；安装完成不等于生成记忆，模型连接、自动采集、队列状态与记忆提炼分别表达。
- **提炼通道独立与隔离**：后台记忆提炼独立支持 Agent 模型代理（Codex、Antigravity `agy`、pi、TraeX）与直连 LLM Provider / Ollama，多个已保存配置中运行期只有一个唯一 `active`（详见 [有效模型契约](agent-effective-model-contract-20261118.md)）。后台提炼会话严格隔离项目上下文、采集插件、工具与日常历史，认证复用不等于复制全部宿主配置。

### 4.2 本机发现与设置页交互（V4 三标签）

设置收敛为「记忆提炼 / Agent 与模型 / 服务」三标签，不另建独立连接向导（首次启动聚焦引导见 [视觉规范 V4](../design/visual-standards-v4-20261120.md#77-首次启动向导2026-11-25-专注式向导重设计)）：

- **本机自动检测**：启动及进入设置时自动检测本机安装并提供「重新检测」；检测只读安装事实，不读聊天记录或密钥，不自动修改宿主配置。未安装项不可勾选，未安装或暂不支持项默认折叠，存在待确认操作的项保持可见。
- **绑定与预览闭环**：流程为「选择 Agent → 连接所选 → 同页查看具体配置变更预览 → 确认连接 → 按提示重启宿主并新建会话 → 显示已连接」。底部仅呈现当前阶段唯一主操作，后台分开执行 `prepare` 与 `activate`，严禁跳过配置变更确认；批量取消保留已成功项，结果未知时保留原操作编号，重开后仅做只读核对。
- **主界面状态语义**：统一使用「未连接 / 等待确认 / 等待新会话 / 已连接 / 连接结果待确认」；已连接项不展示灰色空复选框且保持正常文字色，列表用分隔线替代嵌套卡片边框；保留兼容宿主（如 Cursor 无工具回合当轮注入限制）的特殊说明置于提示中，不干扰主连接流程。
