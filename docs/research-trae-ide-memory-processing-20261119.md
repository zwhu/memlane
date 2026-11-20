# 只有TRAE IDE时，如何整理Memlane记忆

调研日期：2026-11-19（Asia/Shanghai）。本报告只调查**记忆整理的模型执行入口**，不把已知的hooks采集能力当作整理能力结论。依据为TRAE第一方文档、官方开源仓库固定源码及同轮本机TRAE CN检查。未发送prompt、调用模型、安装依赖、修改登录／配置或启停Memlane日常服务；一次辅助程序help误初始化及其清理单独记录。

## 结论与对比

**只有TRAE IDE，也能整理材料。后续源码核对已找到当前IDE命令系统内发送推理并取得结果的内部入口，官方Stop hook也能自动让本Agent继续提炼。当前Memlane四个处理入口确实由CLI驱动（含App内置CLI），尚未实现这里的IDE适配；长期准入条件是能被后台可靠调用，而不是必须叫CLI。**

这不是“TRAE IDE不可能被调用”的判断。IDE本身有推理和harness实现；当前缺口是把它变成有明确请求／结果／身份／取消协议的Memlane后台执行者，而不是证明IDE能否生成文本。

| 场景／产品 | 已确认能力 | 对“只有IDE”的含义 |
| --- | --- | --- |
| 用户在TRAE IDE内手动整理 | 官方支持聊天、模型选择、自定义Agent的prompt和工具配置；内置Agent可独立分析并完成多步任务。[T1][T2][T3] | 可以在IDE内发起整理，将结果交回Memlane；若对应记忆工具已接入，也可以让Agent按工具契约保存。本机这条真实整理／保存闭环未执行验收。 |
| IDE内自动续整理 | 官方Stop可用 `decision=block/reason`让当前Agent继续推理；有次数限制。[T10] | 沿现有会话Agent／模型／登录追加提炼，不需要另启动CLI。 |
| Memlane后台调用TRAE IDE自身模型 | 当前安装包已有 `im.bridge.sendPromptStreaming`→`sendToAgent`→`{text,files}`及session取消链；属于内部命令，见第8节。[L3] | 可从命令系统调用的内部入口存在。extensionHost实际可达、参数序列化及真实账号任务尚未运行验证；Memlane尚未适配，不能仅凭hooks已启用就在处理列表标可用。 |
| TRAE CLI 2.0／TraeCode CLI | 官方明确 `exec`、JSONL、JSON Schema、ACP、app-server等；是独立CLI产品。[T5][T6][T7] | 不等于当前IDE的能力。CN官方概述限定企业版旗舰套餐资格，并需CLI安装和登录；不能假设仅安装IDE的用户已经拥有该执行入口。 |
| 本项目已有TraeX桥 | 本项目已有CLI协议与处理链，但其入口、配置及回执属于该CLI | 不外推到TRAE IDE。本报告不执行或重验TraeX。 |
| 开源 `bytedance/trae-agent` | 独立 `trae-cli`，使用模型Provider配置／API Key；源码可核。[T8] | 是独立开源Agent，不是复用IDE当前登录与模型的官方SDK。不能为解决IDE缺口自动替换用户账号／模型。 |
| IDE MCP客户端 | 官方支持调用外部工具；工具可有 `inputSchema/outputSchema`、Structured Content。[T4] | 可承载“用户在IDE中整理后调用保存工具”的流程。它本身不是Memlane对IDE发起模型请求的证明。 |

## 1. 官方证据：IDE里能进行什么整理

TRAE Agent官方总览将Chat描述为回答技术问题的Agent，将内置Agent描述为能理解需求、读取上下文、拆解并执行多步任务的主Agent。自定义Agent文档允许用户配置prompt、MCP服务器和内置工具，并在输入框用 `@` 选择Agent。[T1][T2]

模型官方文档明确：在输入框右下角点击当前模型，选择本次使用的模型；也可以添加自有Provider模型。后者是**把外部模型接入IDE**，不是开放IDE订阅账号给外部程序使用的API。[T3]

由此可以给只有IDE的用户提供一条直接的手动整理流程：

1. 在TRAE IDE中选择自己当前要使用的模型与整理Agent。
2. 提供本次明确范围的材料，或通过已接入的记忆读取工具获取材料。
3. 让Agent提炼，并检查内容、来源及适用条件。
4. 把结果交回Memlane；如果对应保存工具已连接且有权限，则在IDE内按工具契约保存并核对实际服务回执。

这只是基于官方能力的流程建议，不声称本机已经执行。IDE输出一段“已保存”文字不能替代服务写入回执；它也不证明Memlane后台已经能调用同一个Agent。

官方最新Desktop入门文档区分Agent Window与IDE Window：前者以任务管理为中心，后者以代码编辑为中心。两者共享本地项目、聊天与最终结果，以及MCP、skills、rules、memory等配置。**窗口之间共享数据不等于对外开放推理SDK。**[T1]

## 2. 后台处理所需能力：IDE与CLI分别核对

| 能力 | TRAE IDE／TRAE CN官方证据 | 官方TRAE CLI 2.0证据 | 当前工程判断 |
| --- | --- | --- | --- |
| 外部启动一次模型任务 | 后续源码确认内部注册命令 `im.bridge.sendPromptStreaming`，见第8节 | `traecli exec [选项] [PROMPT]`；省略PROMPT或用 `-`可读stdin | IDE也有可适配的推理入口；启动编辑器命令仍不等于推理。 |
| 使用自身登录／权益 | IDE在首次启动完成账号登录，并在界面选择模型 | CLI首次TUI引导独立登录；自动化另有PAT路径 | 未找到“IDE登录已自动授权外部CLI／SDK”的公开承诺。不提取或复制IDE凭据。 |
| 模型继承 | 界面支持本次模型选择；部分模型有Auto／推理配置 | `model`／profile配置，ACP可覆盖 `model_reasoning_effort` | IDE后台桥应以明确绑定任务／会话元数据为先；无该元数据不猜最近聊天或套TraeX默认。 |
| JSON Schema约束最终模型输出 | 未确认IDE对外模型请求的Schema字段；MCP tool Schema另有明确支持 | `exec --output-schema <FILE>` | 工具的input/output Schema与模型最终输出Schema分开。要求模型“输出JSON”只是提示约束。 |
| 流式事件与完成回执 | 内部命令返回 `{text,files}`并有chunk／interaction callback；另有完成／session stopped扩展事件 | `exec --json` 输出JSONL | callback跨extensionHost IPC与业务task匹配须由适配器处理，不能当标准JSONL协议。 |
| 后台取消 | IM处理链实际调用 `icube.chat.stopSession({sessionId})` | 官方声明ACP；当前已读页未列出具体cancel响应字段 | 使用明确session ID；不以停止终端命令或关闭窗口代替模型任务取消。 |
| 工具／MCP隔离 | 自定义Agent可选择MCP和Read/Edit/Terminal/Web等工具 | `--allowed-tool/--disallowed-tool`、sandbox等 | IDE界面配置可用于手动最小工具Agent；未确认调用级、无全局副作用的后台隔离入口。 |
| hooks／skills隔离 | 官方有目录信任与配置管理，但本题不重验采集 | CLI有feature/config/profile与Hooks信任规则 | 后台整理会话应隔离正常采集以防循环；有hooks不自动满足这个执行隔离契约。 |

本表的“未确认”限定为本次已核官方资料和当前本机证据，**不是对产品实现可能性的否定**。后续若发现正式SDK、已启用的sampling handler或合法内部桥，可继续从那条明确入口核对，避免先扩建未经证明的接口。

## 3. 官方对外入口确实存在，但主体是CLI

TRAE CN当前官方CLI概述写明：“TRAE CLI 2.0是运行在本地终端里的编码Agent”，支持交互TUI和非交互命令，并可用ACP作为智能体服务端。概述的使用限制明确为**仅TRAE企业版旗舰版套餐客户可使用**。[T5]

官方命令行参考进一步列出：[T6]

- `exec --json`：JSONL事件输出。
- `exec --output-schema <FILE>`：约束最终响应结构。
- `exec --ephemeral`：不把会话文件写盘。
- `exec --ignore-user-config`：不加载用户 `traecli.toml`。
- `--allowed-tool/--disallowed-tool`、profile/config、sandbox等。
- `acp`、`app-server`、`mcp-server`作为CLI子命令。

ACP专页的服务启动是 `traecli acp serve`；模型／effort配置覆盖写在 `acp serve`之前。页面示例是Zed用subprocess启动TraeCode CLI服务端，**没有描述让客户端连到已运行TRAE IDE的模型服务**。[T7]

CLI快速开始要求单独安装CLI，首次进入TUI引导登录；自动化提供 `TRAECLI_PERSONAL_ACCESS_TOKEN` 与PAT登录，配置文件位于 `~/.trae/traecli.toml`。这些是CLI自己的认证与配置前提，不能由TRAE IDE已登录推定自动满足，也不构成本轮安装或读取PAT的授权。[T7]

## 4. 本机TRAE CN 3.4.20：内部推理库与外部调用边界

此节合入同轮根代理的只读检查；本报告作者未重复读取用户配置、登录或聊天。

| 本机对象 | 只读结果 | 解释 |
| --- | --- | --- |
| `/Applications/Trae CN.app` | 版本 `3.4.20`；product commit `28d26af97a603c458001613c87d6f16fc6098d0a`；VS Code base `1.107.1` | 限定该安装／渠道，不以国际版文档版本替代本机版本。 |
| `modules/ai-agent/libai_agent.dylib` | 204,233,264字节 | 有内部Agent实现，不能仅凭文件存在认定可外部调用。 |
| `modules/ai-agent/libharness.dylib` | 214,590,976字节 | 有内部harness；其初始化、模型身份及请求协议还需独立适配。 |
| `libtoolhost.dylib`、`agent-tool-host` | 工具宿主组件 | 不是已确认的模型headless CLI。 |
| `ctx-cli` | help只有 `ls/cat/write/grep/glob/mkdir/rm` | 上下文文件系统工具，不是“传prompt得到模型回复”的入口。 |
| `bin/trae-cn`、`bin/code` | 仅用 `ELECTRON_RUN_AS_NODE`调用`out/cli.js` | IDE启动器，不是独立对话／模型CLI；本机PATH仅检测到traecli／traex，没有trae命令。 |
| `start.sh/meta.json` | IDE内部模块启动／连接40005的声明 | 不等于给Memlane承诺的外部SDK或开放端口；本轮没有连接或启动该内部服务。 |

### MCP sampling候选不能直接记为已支持

本机 `workbench.desktop.main.js` 包含generic MCPClient的 `sampling/createMessage` 处理逻辑，但只有在 `options.createMessageRequestHandler`存在时才声明sampling能力。该identifier在当前代码只出现4次：2次capability声明、constructor参数及 `this.y`赋值；同轮检查未发现实际传入handler的调用证据。[L1]

因此有协议实现候选，但当前不能称为“TRAE IDE已提供可用sampling推理接口”。官方MCP支持表列出tool listing/calling、schema及Structured Content，未给出IDE sampling模型选择、Schema、取消与隔离的契约。[T4][L1]

即使后续确认sampling已启用，也还需核对明确授权、选择的模型、输出约束、取消与来源隔离。**MCP client能调用保存工具与MCP server能反向请求IDE推理是两个能力。**

### vscode.lm同样需要provider证据

`extensionHostProcess.js`包含上游 `vscode.lm`注册接口，当前检查没有找到TRAE内置模型provider已注册到该接口的证据。[L1]

这保留了经扩展适配的调查方向，不能直接外推“任意扩展已能调用IDE账号模型”。本次未安装扩展、调用该接口或注册provider。

### 只读探测方法的副作用记录

根代理探测 `agent-tool-host --help` 时，该程序并不支持help；它进入内部初始化，产生baseline缓存 **128,935,385字节／4,513文件**，随后因 `ICUBE_APP_ROOT`缺失退出1。所跟踪进程3570已退出，任务临时目录已清。[L1]

这次是**探测方法有副作用**，不能把exit1判为“IDE没有推理能力”或“后台整理模型失败”。同轮没有发送prompt或修改登录。随后核对缓存及父目录均在15:15:19由本次探测创建，5,236项全部birthtime位于本次探测窗口、无后续mtime修改或符号链接；lsof无缓存占用，IDE前端及MCP扩展未引用该路径。只清理`.trae-local/builtin/baseline`这批新增缓存，并移除空父目录；进程专属shell snapshot不存在，未删除旧运行包或其他资源。小型0600回执位于`.local/trae-ide-research-20261119/probe-cache-cleanup.json`。

## 5. 旧A2b方案没有证明后台整理桥

旧 `docs/p3-ide-contract.md`／`docs/p3-ide-validation.md` 的A2a路径是：项目MCP配置→用户复制验证prompt到IDE→等待实际工具回执。A2b的隔离登录／真实IDE模型工具验收仍未通过；当时检查版本为TRAE CN3.4.14等，不能当作本机3.4.20已经通过。[L2]

旧契约也明确“不把IDE当后台整理执行器”。这是当时工程范围和未验证事实，不是永远禁止未来IDE处理桥的产品规则。当前用户已询问IDE整理，应针对本轮模型执行入口继续判断；无需先恢复旧全量Goal或重跑旧登录／包验收。

## 6. 官方开源Trae Agent不是IDE账号SDK

`bytedance/trae-agent` 官方仓库当日main为 `e839e559ac61bdd0e057c375dd1dee391fee797d`，commit日期2026-02-05 11:21:00 UTC；本次用 `git ls-remote`和官方commit Atom feed交叉核对，读取固定SHA源码。[T8]

`pyproject.toml`版本为 `0.1.0`，命令入口 `trae-cli = trae_agent.cli:main`。`utils/config.py`的 `ModelProvider`包含 `api_key/provider/base_url`，通过CLI／环境变量／配置解析自己的模型凭据。[T8]

因此这套公开代码可用于研究Agent机制，不能当成读取TRAE IDE当前登录、订阅额度或前台模型的现成SDK。本次没有安装它、执行其CLI或把用户模型改为Provider。

## 7. 版本、来源与调研限制

官方文档当日返回的数据包括正文及元数据；本次静态解析官方HTML内的 `window._ROUTER_DATA`，只读取 `docDetail`或CN的 `data.doc`，没有执行网站脚本。不同渠道文档的版本分开记录：

| 来源 | 元数据／已读内容 |
| --- | --- |
| 国际版Models | 发布／更新2026-11-18 11:55:47 UTC；version_id `6afd9243372fa602d42819a3`。 |
| 国际版Agent、Agent overview、Desktop入门、MCP | 发布2026-11-06约02:51:26–27 UTC；各自独立正文。 |
| 国际版Changelog | 文档更新2026-11-12；所读正文首条是2026-10-31 v3.6.13，不能替代CN3.4.20。 |
| CN Changelog | 文档更新2026-11-12 09:46:16 UTC；首条2026-11-10 v3.4.18，和本机3.4.20有版本差。 |
| CN CLI概述／快速开始 | 更新2026-10-16 08:45–08:47 UTC；完整正文含企业旗舰限制与独立安装／登录。 |
| CN CLI参数 | 更新2026-09-20 02:14:56 UTC；完整正文含exec JSON Schema／事件输出等。 |
| CN ACP／配置参考 | 更新2026-08-20；完整正文主体为TraeCode CLI。 |

部分CN URL首次返回400，带明确语言与浏览器UA后取得正文；不存在／已迁移的路径有时返回其他文章，已按**真实标题与path**核对，没有把fallback页面当作原请求文档。GitHub API返回403限流后改用官方Git远端、Atom和固定SHA raw源码，没有为限流安装工具。

“未找到IDE公开外部入口”的范围是上述已读第一方资料与当前本机源证据。用户可以在IDE内整理这一点不依赖TraeX；Memlane后台处理仍须有自己的接入证明。报告不以搜索无结果、CLI启动文件存在或内部dylib存在单独作正／反结论。

## 引用

- [T1] TRAE [Agent overview](https://docs.trae.ai/ide/agent-overview?_lang=en)、[Get started with TRAE Desktop](https://docs.trae.ai/ide/set-up-trae?_lang=en)，完整官方正文及元数据。
- [T2] TRAE [Create and manage custom agents](https://docs.trae.ai/ide/agent?_lang=en)；TRAE CN [内置Agent](https://docs.trae.cn/ide/solo-coder?_lang=zh)（官方当前path为 `ide_built-in-agent`）。
- [T3] TRAE [Built-in models & custom models](https://docs.trae.ai/ide/models?_lang=en)，界面选模型与把外部Provider接入IDE的方向；[CN自定义模型](https://docs.trae.cn/ide/models?_lang=zh)。
- [T4] TRAE [MCP overview](https://docs.trae.ai/ide/model-context-protocol?_lang=en)，官方支持列表：client tools、input/output Schema、Structured Content；不能直接当模型输出Schema／sampling证明。
- [T5] TRAE CN [TRAE CLI 2.0概述](https://docs.trae.cn/cli_about-trae-code-cli-2?_lang=zh)，独立终端Agent与企业旗舰资格。
- [T6] TRAE CN [命令行参数](https://docs.trae.cn/cli_command-line-parameters?_lang=zh)，`exec`、`--json`、`--output-schema`、工具限制及CLI server子命令。
- [T7] TRAE CN [快速开始](https://docs.trae.cn/cli_get-started-with-trae-cli-2?_lang=zh)、[ACP](https://docs.trae.cn/cli_agent-client-protocol?_lang=zh)、[配置参考](https://docs.trae.cn/cli_config-file?_lang=zh)，独立安装／登录与CLI配置、ACP参数。
- [T8] 官方 [Trae Agent固定commit](https://github.com/bytedance/trae-agent/commit/e839e559ac61bdd0e057c375dd1dee391fee797d)、[README](https://github.com/bytedance/trae-agent/blob/e839e559ac61bdd0e057c375dd1dee391fee797d/README.md)、[pyproject.toml](https://github.com/bytedance/trae-agent/blob/e839e559ac61bdd0e057c375dd1dee391fee797d/pyproject.toml)、[Provider配置](https://github.com/bytedance/trae-agent/blob/e839e559ac61bdd0e057c375dd1dee391fee797d/trae_agent/utils/config.py)、[官方commit feed](https://github.com/bytedance/trae-agent/commits/main.atom)。
- [T9] 官方 [国际版Changelog](https://docs.trae.ai/ide/changelog?_lang=en)、[CN Changelog](https://docs.trae.cn/ide/changelog?_lang=zh)，版本／渠道分别解释。
- [L1] 同轮根代理对 `/Applications/Trae CN.app/Contents/Resources/app` 的product、modules、workbench及extension host只读核对；`agent-tool-host --help`方法副作用另行记录，不记为模型验收失败。
- [L2] 本地历史 [IDE契约](p3-ide-contract.md)、[IDE验证](p3-ide-validation.md)、[Agent研究](research-agents.md)。只用作历史起点，未把A2a／A2b外推为当前后台处理能力。

## 8. 后续源码核对：确定的IDE推理接口

本节补充首次调研的缺口。只读取安装包声明／JavaScript源码及官方hook正文；未启动extensionDevelopmentHost、执行推理命令或helper、安装依赖或更改全局配置。并行补入的baseline依赖核对／清理记录保留在第4节。

### 8.1 命令系统已有发送、结果与取消链

当前 `workbench.desktop.main.js`中的IM桥是真正的“外部输入→IDE Agent推理→最终回复”代码，不仅是工具注册或通知。[L3]

| 入口／方法 | 当前实现 | 具体条件／限制 |
| --- | --- | --- |
| `im.bridge.sendPromptStreaming` | class `ZJr`注册该命令，`run(accessor,text,images,onChunk,onInteraction,channel)`返回 `sendPromptStreamingResponse` Promise | 可以据命令系统设计适配；extensionHost实际调用与callback参数序列化本轮未验证。 |
| `sendPromptStreamingResponse(text,images,onChunk,onInteraction)` | 获取当前Agent对象，执行 `sendToAgent(inputs,{enableUnstableFields:true,...options})`，按本轮消息起点提取最终 `{text,files}` | 要有当前可运行task／session；结果读取 `unstableFields.session`或当前session，不能当跨版本稳定SDK。 |
| `im.bridge.handleIncomingMessage(payload)` | 第一方IM扩展调用；接受 `channel/text/images/messageId/conversationId/chatId/appId/fileContent/filePath/files`等 | **handler自身不向caller返回最终result**；它把结果送到Feishu／微信回复命令。它与独立的sendPromptStreaming返回Promise不同。 |
| `getCurrentSessionRunningStatus()` | handler按状态路由 | 非 `WaitingInput`时改用 `submitInterjectMessage`，会干预当前在途任务；不能把用户任务当成Memlane拥有的后台任务。 |
| `icube.chat.getCurrentSessionId` | 注册命令返回当前session ID | “当前”不等于Memlane拥有，不可猜最近聊天。 |
| `im.bridge.subscribeAssistantReply(channel,callback)` | 注册命令订阅助手回复 | 是内部callback，不是已有Memlane结果协议。 |
| `icube.chat.stopSession({sessionId})` | IM交互处理实际调用，用于停止对应session | 必须绑定明确session ID；本轮未执行取消。 |

在普通等待状态，IM handler发送任务、取得 `{text,files}`后调用 `icube.imBridge.feishu.streamEnd`或 `icube.imBridge.wx.reply`。Feishu带message ID有专门会话路由；Lite窗口分支则先入队，另有 `im.bridge.getIncomingMessages`读取。[L3]

第一方扩展激活核对 `icube.imBridge.isSupportedWindow`和 `icube.isAgentWindow`；功能配置来自 `icube.common.getDynamicConfig().iCubeApp.imBridge`，并检查渠道启用、workspace／session bot绑定与ownership。现有链是渠道专用适配，不能给任意channel名字就声称Memlane已接通。[L3]

内部chunk轮询间隔200ms；有5分钟的轮询／interaction收尾预算；轮询直接调用 `onChunk`，异常由catch处理。callback能否跨extensionHost IPC、最终result如何绑定业务任务、错误及取消后晚到结果如何处理，都属于实际适配工作，不能从源代码调用存在跳到真实账号任务通过。[L3]

**源证结论：IDE命令系统内可调用的推理与结果入口已经存在，也有取消调用链。当前四项走CLI是Memlane已实现适配的范围，不是IDE被架构禁止。Memlane尚未接通这个内部命令入口。**

### 8.2 实际扩展对象与权限条件

`extensionHostProcess.js`实际返回以下对象：[L4]

- `vscode.icubeAI.onAiTaskComplete`：仅完成事件。MainThread将payload送到 `$onAiTaskCompleteEvent`，再 `fire`给扩展；该对象没有submit／cancel方法，不能把名称当完整推理API。
- `vscode.trae.hooks.onSessionStopped`：停止事件。`$notifySessionStopped(payload)`只fire并通知MCP服务，使用 `sessionId/computerUseOnly/observedAt`；没有消费监听者返回值来继续模型推理。
- `vscode.trae.registerMcpProvider/registerMcpServerDefinition/registerMcpTransportServer`：MCP工具／transport注册，不是模型请求。
- `vscode.icube.getWebSocketRpcClient(endpoint,options)`：通用RPC工厂，本身不附Agent请求协议或登录许可。

该文件的 `icubeAI`共3处：proposal registry名称、registry URL及最终API返回。实际对象为 `sU={get onAiTaskComplete(){return xe.onAiTaskCompleteEvent}}`。指向microsoft proposed d.ts的registry URL只是字符串，不能当作该提议API已经公开发布的证明。[L4]

本机通用proposal检查当前为 `function it(e,t){return!0}`；`T(e,t)`仅在 `!it`时抛错，因此此build的通用proposal检查恒放行。独立业务allowlist仍存在，例如 `icubeContextState`会按extension ID与目标extension检查并直接throw。不能把通用检查放行外推成所有业务权限开放，或未来版本承诺。第一方manifest声明 `enabledApiProposals:["icube"]`／`["trae"]`也不等于模型账号权限。[L4]

### 8.3 标准vscode.lm的完整调用声明

本机 `out/vscode-dts/vscode.d.ts`有以下完整接口：[L5]

```typescript
vscode.lm.selectChatModels(selector?): Thenable<LanguageModelChat[]>
LanguageModelChat.sendRequest(messages, options?, token?): Thenable<LanguageModelChatResponse>
LanguageModelChatResponse.text: AsyncIterable<string>
LanguageModelChatResponse.stream: AsyncIterable<LanguageModelTextPart | LanguageModelToolCallPart | ...>
vscode.chat.createChatParticipant(id, handler): ChatParticipant
ChatRequest.model: LanguageModelChat
```

`ChatRequest.model`是该request在UI中选中的模型，不能超出request生命周期持有。`selectChatModels`可能返回空列表，只有实际provider注册／暴露才会给出模型。首次 `sendRequest`可能要求用户consent，声明要求响应用户操作；无权限、model不存在、配额受阻会拒绝Promise。取消使用本次请求的 `CancellationTokenSource.cancel()`；响应迭代也可能中途报错。[L5]

tools只有作为 `LanguageModelChatRequestOptions.tools`传入才对该次请求开放，可以据此设计无工具模型请求。provider-specific `modelOptions`不是已有通用JSON Schema保证。当前未执行 `selectChatModels`或sendRequest探测，不能仅由标准声明推定TRAE内置provider已可用。[L5]

### 8.4 官方Stop hook能自动让本Agent继续提炼

官方 [Hook configuration reference](https://docs.trae.ai/ide/hook-configuration-reference?_lang=en)明确：Stop在Agent准备结束当前query时触发，能阻止结束并要求继续处理。stdin专用字段为：[T10]

```json
{
  "session_id": "<current-session>",
  "hook_event_name": "Stop",
  "stop_hook_active": false,
  "loop_count": 0,
  "last_assistant_message": "<final-assistant-text>"
}
```

命令hook退出0并输出以下JSON时，`reason`成为**新用户query**，让同Agent继续推理：[T10]

```json
{"decision":"block","reason":"<本次明确的提炼任务>"}
```

退出2也等价block，stderr成为续处理query；其他错误退出不阻塞。Stop hook group的 `loop_limit`支持正整数、默认5，`loop_count>=loop_limit`时跳过该组。通用 `continue:false`优先于事件decision，表示停止。`stop_hook_active`说明当前query是否已经被Stop阻拦过，可以用于防止重复续处理。[T10]

这能在完成边界让本Agent自动提炼当前已授权材料，沿IDE会话自身登录／模型，并通过已配置的记忆工具交回服务回执；不需启动额外CLI。它要有已有Agent会话及Stop边界，**不是IDE没有任务时任意新开后台任务的入口**，也不授权用户取消后强制续跑。本轮未安装hook或调用模型。

被动监听 `vscode.trae.hooks.onSessionStopped`与上述命令hook返回decision完全不同；Notification忽略stdout，也不能要求续推理。[T10][L4]

### 8.5 代码定位与完成边界

以下偏移为**UTF-8解码后的0-based字符位置**，可用 `Path(...).read_text().index(token)`核对，不是二进制byte offset。版本／commit沿用第4节。

| 文件 | 标识／位置 |
| --- | --- |
| `out/vs/workbench/workbench.desktop.main.js` | 字符16073092：`async sendPromptStreamingResponse`；16091869附近：`im.bridge.sendPromptStreaming`注册与调用；16101123附近：`im.bridge.handleIncomingMessage`注册。 |
| 同文件 | 字符4832746附近：完成event转发；13210006附近：完成event emitter；IM handler中调用 `icube.chat.stopSession`。 |
| `out/vs/workbench/api/node/extensionHostProcess.js` | 字符1670417附近：icubeAI对象；1619182：trae factory `xse`；1897148附近：`$notifySessionStopped`。 |
| 同文件 | 字符589624：恒true的proposal检查 `it`；589650：`T`；1614521附近：独立allowlist错误helper `V1`。 |
| `extensions/cloudide.icube-im-bridge/dist/extension.js` | 字符49749附近：`oi(payload)`转发输入；55103附近：窗口适用性激活检查。 |
| `out/vscode-dts/vscode.d.ts` | 行19781：ChatRequest；20120：response；20168：LanguageModelChat；20228：sendRequest；20695：selectChatModels。 |

本节只提供源证与精确接口条件；extensionHost实际可达、参数序列化、真实账号推理／取消和Memlane任务关联仍未运行验证。当前四项CLI处理者由同轮根代理核对现有实现确认。源码结论是内部入口存在，交付结论是Memlane尚未适配，二者不混写。

- [T10] TRAE官方 [Hook configuration reference](https://docs.trae.ai/ide/hook-configuration-reference?_lang=en)、[Automate actions with hooks](https://docs.trae.ai/ide/automate-actions-with-hooks?_lang=en)，本次完整读取；reference发布／更新2026-11-06，明确Stop续query／循环次数及输出优先级。
- [L3] 本机 [workbench源码](</Applications/Trae CN.app/Contents/Resources/app/out/vs/workbench/workbench.desktop.main.js>)、[第一方IM扩展](</Applications/Trae CN.app/Contents/Resources/app/extensions/cloudide.icube-im-bridge/dist/extension.js>)，只读源码。
- [L4] 本机 [extensionHostProcess源码](</Applications/Trae CN.app/Contents/Resources/app/out/vs/workbench/api/node/extensionHostProcess.js>)，实际API对象、门禁与event实现。
- [L5] 本机完整 [vscode.d.ts](</Applications/Trae CN.app/Contents/Resources/app/out/vscode-dts/vscode.d.ts:19781>)，模型、consent、request生命周期和取消声明。
