# Cognee 与 Gemini Mac App：hooks、MCP 与聊天采集调研

调研日期：2026-11-19（Asia/Shanghai）。本报告核对当日 Cognee 官方文档、官方 GitHub live main／发布记录，并结合 Google 官方文档和本机只读证据。仅调研；未安装插件、运行推理、修改 Agent 配置、启动／停止服务或读写日常 Cognee 库。其他未提交改动未纳入本报告。

## 结论

**存在可复用的官方 hooks 路径：Cognee 已提供 Antigravity 原生插件；Gemini Mac App 的任务／CodeMode 内置 `agy` helper，与这条路径有直接关联。关键待验的是实际 App 是否加载插件、触发 hooks 并提供可配对的问答原文，不能笼统判断“Gemini App 没 hooks”。**

用户提出“只要 Gemini 支持 hooks 应该就可以”符合本项目现有采集方向：宿主能在提交／完成时触发，且能取得输入、完成回复及稳定会话／轮次身份，即可优先沿官方插件适配接入。是否还需要 MCP 是另一项能力；MCP 可供模型主动读写记忆，并不保证每轮聊天自动保存。

| 对象／能力 | 当日结论 | 证据与边界 |
| --- | --- | --- |
| Cognee → Antigravity hooks | **官方源码已实现** | `PreInvocation` 采集输入／召回，`PostToolUse` 采集工具结果，`Stop` 保存完成问答并请求整理；通过 `agy plugin install` 注册。不是仅 MCP。[C1][C2][C3] |
| Gemini Mac App 任务／CodeMode复用该插件 | **有明确技术路径，实际采集待验** | Google 的 Antigravity hooks 文档覆盖 App／CLI／IDE；本机 Gemini 主程序明确调用 bundled `agy`，包含 CodeMode host bridge。当前任务界面提示需要 Google AI Pro／Ultra，未产生新任务／hooks写入回执。[G1][L1] |
| Gemini Mac App 普通聊天每轮自动采集 | **覆盖未知** | 普通 Mac 帮助未说明 hooks；不能仅凭 task／CodeMode 使用 `agy` 就外推所有普通聊天。也不能从没有 `hooks.json` 文件名推出无 hooks。[G1][G3][L1] |
| Gemini App MCP 读写 Cognee | **Google 已提供 custom MCP app 路径；Cognee提供读写server** | Google 当前文档覆盖 chats、quick actions、task threads；Cognee `remember`／`recall` 工具已实现。此组合的实际配置、协议兼容与本机回执未验证。[G2][C6] |
| Gemini CLI MCP | **官方文档和前端源码明确支持** | 客户端是终端命令 `gemini`，配置 `~/.gemini/settings.json`，用 `/mcp` 核对。不能据此替代 Gemini Mac App／Antigravity 的 hooks调查。[C4][C5] |
| Gemini CLI 专用 Cognee hooks插件 | **未在本次 live 官方集成树中发现独立实现** | 完整 `cognee-integrations` tree 不含 `integrations/gemini*`；现有相关原生插件名为 `antigravity`，hook契约是 `PreInvocation` 等。Gemini CLI 自身支持hooks不等于这两套载荷和安装协议相同。[C1][C2] |
| Gemini LLM／Embedding Provider | **支持** | 指向 Google AI／Vertex 等模型 API，用于 Cognee 生成／向量处理；不能读取 Gemini App 聊天，也不等于复用 App 账号、订阅或临时模型设置。[C7][C8] |
| Gemini 历史聊天自动导入 | **未发现官方 Gemini 专用导入器** | 当日官方 `import_agent_sessions` cookbook 的 `AGENTS` 只有 Claude Code／Codex。Antigravity运行期仅读末尾transcript辅助采集，不是历史回扫／Google账号聊天导入器。[C3][C9] |
| Memlane 当前已经接入 Gemini／Antigravity | **没有对应受管入口证据** | 当前环境名单只有 Codex、pi、TraeX、Claude Code、Cursor、OpenCode。上游源码已存在与本机已经安装／实际接收是不同事实。[L2] |

## 1. Live 版本与本地固定版本

| 核对项 | Live 官方状态 | Memlane 当前固定配置 |
| --- | --- | --- |
| `topoteretes/cognee-integrations` main | `e682f55203b9dff2eb9849ff34f142475ab3f40d`；2026-11-18 14:02:06 UTC；合并恢复 Claude Code 1.8.7、以1.8.10发布的变更 | `a176248914fdb32a78354854b300174203b15a5c`；2026-11-11 20:34:09 UTC；配置中的通用plugin版本 `1.8.0`。[C10][L3] |
| Antigravity 子插件 | `integrations/antigravity/plugin.json`：`name=cognee`、`version=1.6.8` | 固定commit中同样是 Antigravity `1.6.8`；不能拿通用plugin `1.8.0` 当其子插件版本。[C1] |
| `topoteretes/cognee` main | `c8a094375821f2d8c7c4945d3b2202e2e5d6603e`；2026-11-18 21:40:46 UTC；源码版本 `1.6.10` | 固定 `ec6f40859e5f2df9e7862dde27d580ac3268ed43`，SDK `1.6.7`，组件 `1.6.7-memlane.14-coding-ec6f408`。[C11][L3] |
| Cognee 最新正式发布 | `v1.6.10`；2026-11-18 21:42:57 UTC | 本次没有升级，也没有把live行为认定为固定服务已经具备。[C12] |

GitHub tree API 对集成仓库返回1648个路径，对引擎仓库返回6518个路径，均 `truncated=false`。集成仓库完整归档用于全文搜索，并非只根据搜索引擎无结果作否定判断。其 releases API当日返回空列表，不能给整个集成仓库套一个统一“最新release版本”；各子插件分别使用manifest／changelog。[C13]

### Antigravity 固定版本与 live 的逐文件比较

对两个SHA中的 `integrations/antigravity/` 完整目录比较，固定版本51个文件、live52个文件。仅以下三项不同：

| 文件 | Live 相对固定版本的变化 |
| --- | --- |
| `scripts/_capture_policy.py` | deny path清单提为独立数据文件；读取失败时拒绝全部路径。环境参数读取／说明有所调整。 |
| `scripts/capture_deny_paths.txt` | 新增上述敏感路径清单。 |
| `scripts/_remember_http.py` | `_float_env` 改为接受原始值，再由调用处显式读取环境；相关HTTP等待／轮询参数传入点调整。 |

`README.md`、`hooks.json`、`plugin.json`、`scripts/agy_hook.py`、`scripts/store-user-prompt.py`、`scripts/store-to-session.py`、`scripts/_plugin_common.py` 在两版本中逐字相同。**Antigravity hooks采集不需要以“等待最新版新增Gemini支持”为前提：当前固定的官方repo版本已经有这套实现。**这只证明代码来源可复用；Memlane目前没有将该目录接到Gemini App的安装／身份／项目归属流程。[C14][L2][L3]

## 2. Cognee Antigravity hooks 的实际实现

### 安装与目录

官方 README 使用 `agy plugin validate integrations/antigravity` 和 `agy plugin install integrations/antigravity`，通过 `plugin.json`／`hooks.json` 声明注册。它注明 Antigravity CLI1.1.27全局安装位于 `~/.gemini/config/plugins/cognee`，另提及 `~/.gemini/antigravity-cli/plugins/cognee` 布局可通过 `COGNEE_ANTIGRAVITY_PLUGIN_ROOT` 指向。这项路径说明来自已读插件README，未独立核对其所链接的CLI plugins网页。[C1]

这里有明确版本／安装路径差异，因此不应把 `~/.gemini/settings.json`、`~/.gemini/config/hooks.json`、两种plugin根目录混为一个文件。官方插件默认通过共享 `~/.cognee/.env` 或进程环境找到服务；是否连接本项目的同一日常库，必须以后续受管配置回执为准，不能直接执行README默认本地启动步骤。[C1][L3]

### 触发与原文来源

| 原生事件／named hook | 官方处理 | 真实字段／回执要求 |
| --- | --- | --- |
| `PreInvocation`／`cognee-bootstrap` | 会话启动／连接，bootstrap按宿主进程所有权核对 | `conversationId` 与宿主身份；当前官方插件有服务注册／连接逻辑。 |
| `PreInvocation`／`cognee-recall` | 按输入召回记忆，输出 `injectSteps: [{ephemeralMessage: ...}]` | `lastUserInput`／`prompt`，模型必须实际接受该临时上下文。 |
| `PreInvocation`／`cognee-capture` | 将用户输入放入待配对状态 | 会话与轮次身份；不会仅凭触发就声称问答已保存。 |
| `PostToolUse`／`cognee-capture` | 工具调用与匹配结果转 `TraceEntry` | `toolCall`、`result`、`error`、`stepIdx`及tool-call ID；乱序结果按身份匹配。 |
| `Stop`／`cognee-stop` | 完成回复与待配对输入转 `QAEntry`；另请求整理 | 优先 `finalModelOutput`／规范化assistant字段，缺失时从当轮transcript找完成的 `PLANNER_RESPONSE`。 |

原生字段由 `scripts/agy_hook.py:174` 的 `normalize_payload` 映射：`conversationId→session_id`、`transcriptPath→transcript_path`、`modelName→model`、`lastUserInput→prompt`，`workspacePaths[0]` 作为可用cwd。`executionId` 可用于轮次／执行去重；部分宿主 `Stop` 只给 `executionNum` 时，加入transcript turn构造执行身份，避免把重置的计数器当作同一轮。[C2][C3]

Google当前公开字段表没有 `finalModelOutput`／`executionId`；不能把插件兼容字段写成当前宿主保证提供的字段。插件已处理当前的 `conversationId`、`executionNum`、`lastUserInput`、`fullyIdle`：在原生完成正文缺失时从 `transcriptPath` 末尾查找 `source=MODEL`、`type=PLANNER_RESPONSE`、`status=DONE` 的正文，并从 `USER_EXPLICIT`／`USER_INPUT`／`DONE` 记录定位当前用户轮次。**因此没有直接完成输出字段并不阻断其现有fallback，但当前App transcript是否确为该格式，以及末尾1 MiB是否覆盖完整当轮，仍须真实协议核验。**[C3][G1]

`Stop` 的状态边界也已实现：`fullyIdle=false` 时不保存该Stop回复；整理仅在 `fullyIdle=true` 时执行。缺少prompt／assistant正文时退出，hooks是best effort，不阻塞宿主。保存、请求整理、整理成功、实际产生记忆应分别看回执。[C3]

### 保存内容的限制

这条路径能保存符合采集策略的问答和工具轨迹，**不是“完整复制所有聊天数据”的保证**：

| 内容 | 当前实现上限／限制 |
| --- | --- |
| 用户输入 | `MAX_TEXT=4000`，按字符截取；太短／缺失输入会跳过。 |
| 助手完成回复 | `_MAX_ASSISTANT_BYTES=8000`，按UTF-8字节截取。 |
| 工具参数 | 每个值 `_MAX_PARAMS_BYTES=4000` 字节。 |
| 工具返回 | `_MAX_RETURN_BYTES=8000` 字节。 |
| transcript回读 | 仅末尾 `1_048_576` 字节，用于补字段／配对／fallback；丢弃起始半行，忽略无效JSON／非object。 |
| 策略 | `COGNEE_CAPTURE`、工具／路径过滤、脱敏、自调用过滤；不是所有中间发言、附件或多媒体的完整归档承诺。 |

用户输入先留在 `pending`，完成回复到来后形成一条paired `QAEntry`，通过HTTP `remember_entry_via_http` 或本地Cognee调用保存；插件私有状态位于 `~/.cognee-plugin/antigravity/`。这不是Gemini账号中的全部历史，也不等于每次触发后立刻进入永久图谱。[C1][C3][C15]

官方README还要求服务plugin registry认识 `antigravity` 才能provision独立主体；其上游 `auto` 策略允许fallback到principal。后续本项目若接入，必须沿用当前已授权的独立官方主体和项目ACL契约核对；本次未检查日常库主体或改动服务。[C1]

## 3. Gemini CLI MCP 与 Gemini Provider 分别支持什么

### Gemini CLI MCP

Cognee Cloud当前集成页把Gemini card明写为 **Gemini CLI／Connect via MCP**，步骤是编辑 `~/.gemini/settings.json`、运行终端命令 `gemini`，再用 `/mcp` 查 `cognee`。对应 live前端 `agentCards.tsx:194` 的key也是 `gemini-cli`；这项claim已追到实现。[C4][C5]

Cloud文档的MCP launcher固定为 `cognee-mcp@0.5.6`。MCP源码对外注册 `remember` 和 `recall`，支持文本或文件写入、按session写缓存或进入永久处理，以及召回。**只有客户端实际调用工具才发生相应读写**；单纯注册server不会自动获得每轮宿主prompt和reply。[C4][C6]

因此，“Gemini CLI能用Cognee”可以确认；“同一个配置已经覆盖Gemini Mac App”不能从这页推出。用户关心App每轮自动采集时应优先检查上一节的Antigravity插件，而不是仅完成CLI `/mcp` 核对。

### Gemini LLM／Embedding Provider

官方LLM文档列Google Gemini，配置使用 `LLM_PROVIDER=gemini`、模型带 `gemini/` 前缀并使用对应API凭据；Embedding文档也单列Google Gemini。引擎源码有 `GeminiAdapter`，接到LiteLLM等模型API路径。[C7][C8]

这是“Cognee用哪家模型处理材料”，不是“从哪款聊天App采集材料”。Provider没有App会话读取／普通聊天hook注册的实现，也没有证明能继承Gemini App账号权益或App临时模型配置。Google ADK／Band集成里提到Gemini同理属于框架／adapter能力，不应当成Mac App接入回执。

## 4. Google官方与本机Gemini App证据

此节由同轮Google官方调查和本机只读检查证据合入；与上述Cognee源代码核对分开记录。完整App聊天覆盖及真实hooks回执仍未完成。

### 官方文档

Google [Antigravity hooks](https://antigravity.google/docs/hooks/) 文档当日明确标注 **Available on Antigravity App／CLI／IDE**，提供 `SessionStart`、`PreToolUse`、`PostToolUse`、`PreInvocation`、`PostInvocation`、`Stop`。全局hooks配置为 `~/.gemini/config/hooks.json`，App数据在 `~/.gemini/antigravity`，CLI在 `~/.gemini/antigravity-cli`。载荷含 `conversationId`、`workspacePaths`、`transcriptPath`、`modelName`、`lastUserInput`；transcript路径指向 `brain/<conversationId>/.system_generated/logs/transcript.jsonl`。[G1]

这与Cognee Antigravity `normalize_payload` 的字段形状直接吻合；原生注册／字段映射已有主源依据。但文档的产品表面名称是Antigravity，仍需本机Gemini App实际加载机制证据连接两者。

Google当前Gemini帮助 [Use apps in Gemini](https://support.google.com/gemini/answer/13695044?hl=en) 已说明 custom MCP apps 可用于 **chats、quick actions、task threads**，并链接 [custom apps说明](https://support.google.com/gemini/answer/17209137?hl=en)。后者本轮仅从前者提取为相关链接，未独立读取全文；本报告的能力claim来自已读的Use apps页面。所以不应把“Gemini App MCP仅限Spark／任务”写成当前官方限制。普通Mac使用帮助没有给出hooks说明，MCP广泛覆盖不代表hooks同样覆盖所有聊天。[G2][G3]

### 本机只读与实际窗口

本机 `/Applications/Gemini.app`：bundle `com.google.GeminiMacOS`，版本 `1.144.9.1499`。包内存在 `Contents/Helpers/agy`（arm64 Mach-O，204,858,992字节）及 `git_mcp_server`、`obsidian_mcp_server` 等helper。[L1]

Gemini主程序只读字节搜索出现 `[JetskiServer] Found bundled agy helper`、`Contents/Helpers/agy`、`--host_bridge_url=http://127.0.0.1` 和 `com.google.gemini.CodeMode.HostServer`，建立了 **CodeMode使用bundled Antigravity helper** 的直接关联。经 `scripts/task.py` 包装的 `agy --help` 仅查看帮助，无模型调用，显示 `plugin`／`mcp`／`acp`／`print` 命令；helper还含hooks、transcript、共享配置根相关字符串。[L1]

这些证据足以将Antigravity官方插件列为首选接入候选，不足以证明普通聊天的全部请求也走该harness。

实际Gemini窗口已选 `memlane` 文件夹，并显示任务进度栏；当时提示需要升级Google AI Pro或Ultra才能继续分配任务。没有发送任何消息。因此本次没有新任务的 `PreInvocation`／`Stop`／`QAEntry` 回执；这是任务可执行前提受限，**不是已发现Cognee插件失败**。没有安装插件，也没有用另一运行库进行展示。[L1]

本机可复核的只读命令与检查方式如下；本报告不保存含日常会话的窗口截图：

```python
import pathlib, plistlib
bundle = pathlib.Path('/Applications/Gemini.app/Contents')
info = plistlib.loads((bundle / 'Info.plist').read_bytes())
print(info.get('CFBundleIdentifier'), info.get('CFBundleShortVersionString'))
helper = bundle / 'Helpers/agy'
print(helper.stat().st_size)
main = bundle / 'MacOS' / info['CFBundleExecutable']
raw = main.read_bytes()
for marker in (
    b'[JetskiServer] Found bundled agy helper.',
    b'--host_bridge_url=http://127.0.0.1:',
    b'com.google.gemini.CodeMode.HostServer',
):
    print(marker.decode(), marker in raw)
```

```sh
file /Applications/Gemini.app/Contents/Helpers/agy
python3 scripts/task.py -- /Applications/Gemini.app/Contents/Helpers/agy --help
```

本轮上述help退出码0，只有帮助内容；不调用 `print`／任务／模型。Computer Use只读设置检查中，扩展程序页仅显示Finder系统扩展，未见可见Hooks设置。**没有GUI入口不代表没有hooks**，原生plugin声明路径仍以官方契约和实际加载回执为准。本轮打开的设置窗口已关闭，未改配置。

## 5. 历史聊天导入边界

当日Cognee官方 `examples/cookbooks/import_agent_sessions` 将本机Claude Code／Codex日志转为Markdown，再沿官方入口导入。`read_sessions.py:31` 的 `AGENTS={"claude":"Claude Code","codex":"Codex"}`，`find_agents.py` 的安装发现也只有这两类；没有Gemini／Antigravity／Google Takeout解析器。[C9]

Antigravity插件读取transcript尾部只为运行期字段补全、配对和去重；没有提供扫描所有旧conversation、访问Google账号聊天、导入Gemini导出包的步骤。通用文本／文件 `remember` 可接受用户提供的内容，但那属于材料导入，不能称为已支持Gemini历史格式、所有附件或完整聊天映射。[C3][C6][C9]

## 6. 适用于Memlane的后续最小核验

本报告只形成调研结论，不启动开发／安装。若后续明确接入，最小闭环应先围绕**当前Gemini App任务／CodeMode→官方Antigravity插件**，而不是自写并行采集器：

1. 核对当前App版本实际plugin根、注册机制及任务额度前提，验证bundled `agy` 与App读取同一安装实例；区分CLI配置和App配置。
2. 把官方插件配置到本项目已存在的同一日常服务，使用独立Agent主体，核对dataset归属／read-write-delete权限；不沿上游owner fallback自动接入。
3. 一轮受控新任务取得 `PreInvocation` 输入、`Stop`完成回复、稳定conversation／turn身份，验证官方服务实际保存paired `QAEntry`；分别核对后台整理提交／完成／产出，不补采旧记录。
4. 再单独核验普通chat、quick action和task thread各自hooks是否触发；未知表面先保持未知，不能从CodeMode通过推出全覆盖。

MCP读写可以独立验证，但不能代替逐轮自动采集闭环。源码比较说明当前固定repo已经含Antigravity插件；仍需Memlane安装／环境／身份适配，并核对固定Cognee服务与独立主体注册兼容性。无需为了本次调研升级整个Cognee栈。

## 引用与核验范围

所有外部链接为第一方文档／官方仓库。本次直接使用GitHub API、raw源码与codeload归档，网络可读；部分API完整commit响应含大量diff，只保留SHA／日期等结论。在线文档是当日live页面，未提供文档commit，故与固定SHA源码分别标注。未运行上游测试、模型或采集器；源码内测试仅说明覆盖意图，不记为本机验收通过。

- [C1] [Antigravity README](https://github.com/topoteretes/cognee-integrations/blob/e682f55203b9dff2eb9849ff34f142475ab3f40d/integrations/antigravity/README.md)、[plugin.json](https://github.com/topoteretes/cognee-integrations/blob/e682f55203b9dff2eb9849ff34f142475ab3f40d/integrations/antigravity/plugin.json)。
- [C2] [Antigravity hooks.json](https://github.com/topoteretes/cognee-integrations/blob/e682f55203b9dff2eb9849ff34f142475ab3f40d/integrations/antigravity/hooks.json)。
- [C3] [agy_hook.py](https://github.com/topoteretes/cognee-integrations/blob/e682f55203b9dff2eb9849ff34f142475ab3f40d/integrations/antigravity/scripts/agy_hook.py#L174)，字段映射、tail回读、去重、`injectSteps`、`fullyIdle`门槛。
- [C4] [Cognee Cloud集成文档](https://docs.cognee.ai/cognee-cloud/ui/integrations)，当日live Gemini CLI卡片、MCP launcher及自动采集区分。
- [C5] [agentCards.tsx](https://github.com/topoteretes/cognee/blob/c8a094375821f2d8c7c4945d3b2202e2e5d6603e/cognee-frontend/src/modules/integrations/agentCards.tsx#L194)、[connectorHelpers.tsx](https://github.com/topoteretes/cognee/blob/c8a094375821f2d8c7c4945d3b2202e2e5d6603e/cognee-frontend/src/modules/integrations/connectorHelpers.tsx#L173)，Gemini CLI配置实现。
- [C6] [MCP server remember](https://github.com/topoteretes/cognee/blob/c8a094375821f2d8c7c4945d3b2202e2e5d6603e/cognee-mcp/src/server.py#L379)、[recall](https://github.com/topoteretes/cognee/blob/c8a094375821f2d8c7c4945d3b2202e2e5d6603e/cognee-mcp/src/server.py#L564)、[tool registry](https://github.com/topoteretes/cognee/blob/c8a094375821f2d8c7c4945d3b2202e2e5d6603e/cognee-mcp/src/tool_registry.py)。
- [C7] [LLM Providers](https://docs.cognee.ai/setup-configuration/llm-providers)、[Embedding Providers](https://docs.cognee.ai/setup-configuration/embedding-providers)，当日live。
- [C8] [Gemini API adapter](https://github.com/topoteretes/cognee/blob/c8a094375821f2d8c7c4945d3b2202e2e5d6603e/cognee/infrastructure/llm/structured_output_framework/litellm_instructor/llm/gemini/adapter.py)。
- [C9] [历史导入cookbook](https://github.com/topoteretes/cognee/tree/c8a094375821f2d8c7c4945d3b2202e2e5d6603e/examples/cookbooks/import_agent_sessions)、[read_sessions.py](https://github.com/topoteretes/cognee/blob/c8a094375821f2d8c7c4945d3b2202e2e5d6603e/examples/cookbooks/import_agent_sessions/scripts/read_sessions.py#L31)、[find_agents.py](https://github.com/topoteretes/cognee/blob/c8a094375821f2d8c7c4945d3b2202e2e5d6603e/examples/cookbooks/import_agent_sessions/scripts/find_agents.py)。
- [C10] [Live integrations commit](https://github.com/topoteretes/cognee-integrations/commit/e682f55203b9dff2eb9849ff34f142475ab3f40d)、[固定commit](https://github.com/topoteretes/cognee-integrations/commit/a176248914fdb32a78354854b300174203b15a5c)。
- [C11] [Live Cognee commit](https://github.com/topoteretes/cognee/commit/c8a094375821f2d8c7c4945d3b2202e2e5d6603e)、[pyproject.toml](https://github.com/topoteretes/cognee/blob/c8a094375821f2d8c7c4945d3b2202e2e5d6603e/pyproject.toml)。
- [C12] [v1.6.10正式发布](https://github.com/topoteretes/cognee/releases/tag/v1.6.10)。
- [C13] [固定tree：integrations](https://api.github.com/repos/topoteretes/cognee-integrations/git/trees/e682f55203b9dff2eb9849ff34f142475ab3f40d?recursive=1)、[固定tree：Cognee](https://api.github.com/repos/topoteretes/cognee/git/trees/c8a094375821f2d8c7c4945d3b2202e2e5d6603e?recursive=1)、[集成仓库releases API](https://api.github.com/repos/topoteretes/cognee-integrations/releases?per_page=5)。
- [C14] [固定版本Antigravity目录](https://github.com/topoteretes/cognee-integrations/tree/a176248914fdb32a78354854b300174203b15a5c/integrations/antigravity)、[live目录](https://github.com/topoteretes/cognee-integrations/tree/e682f55203b9dff2eb9849ff34f142475ab3f40d/integrations/antigravity)，本次逐文件字节比较。
- [C15] [store-user-prompt.py](https://github.com/topoteretes/cognee-integrations/blob/e682f55203b9dff2eb9849ff34f142475ab3f40d/integrations/antigravity/scripts/store-user-prompt.py#L45)、[store-to-session.py](https://github.com/topoteretes/cognee-integrations/blob/e682f55203b9dff2eb9849ff34f142475ab3f40d/integrations/antigravity/scripts/store-to-session.py#L59)、[capture policy](https://github.com/topoteretes/cognee-integrations/blob/e682f55203b9dff2eb9849ff34f142475ab3f40d/integrations/antigravity/scripts/_capture_policy.py)。
- [G1] Google [Antigravity hooks](https://antigravity.google/docs/hooks/)，由同轮Google调查核对；CLI plugins网页本轮直接读取返回空body，不用作证据。
- [G2] Google [Use apps in Gemini](https://support.google.com/gemini/answer/13695044?hl=en)，当日已读live页面；[custom apps](https://support.google.com/gemini/answer/17209137?hl=en)仅是从该页面提取的相关链接，未独立读取全文。
- [G3] Google [Gemini Mac帮助](https://support.google.com/gemini/answer/17011627?hl=en)。
- [L1] 同轮根代理的本机bundle／主程序只读检查、受管 `agy --help` 和Computer Use窗口查看；可复现命令见第4节。设置扩展页仅显示Finder系统扩展、没有可见Hooks设置，不证明缺少配置机制。是本机安装／表面证据，未产生采集回执。
- [L2] [Memlane Agent环境名单](../engine/python/agent_environments.py#L7)、[当前连接适配](../engine/python/agent_connections.py)、[宿主官方插件补丁范围](../engine/python/project_host_plugin.py#L172)。
- [L3] [本地固定组件配置](../engine/coding-memory-components.json)、[受管补丁manifest](../engine/managed-project-plugin.json)。

历史起点：[2026-11-12官方插件采集报告](research-official-plugin-capture-20261112.md)。其旧SHA／日期只用于定位问题，本报告的版本及能力结论已使用当日live源码重新核对。

## 2026-11-19增补：agy记忆处理桥原生协议

本节为后续处理桥的独立只读调查，前文接入状态是其调研时点的记录。本次**没有发送实际或模拟prompt、启动模型／ACP服务、安装SDK、修改登录或用户配置**。只运行原生命令的help，读取Google官方文档／SDK源码和本机配置标量／元数据schema。协议例子仅说明字段，未执行。

### 当前能确认的边界

| 事项 | 确认结论 | 未完成的证明 |
| --- | --- | --- |
| 原生处理通道 | `agy --input-format stream-json --output-format stream-json --json-schema <schema>` 有官方stdin输入／结构化最终结果协议。[P1] | 未调用模型；未取得当前机器的真实schema结果。 |
| 明确默认模型 | 本机 `~/.gemini/antigravity-cli/settings.json.model` 显式为 `Gemini 3.8 Flash (High)`；这是**agy CLI默认配置**。[P2][P10] | 不是Gemini App前台临时模型；没有明确conversation绑定时不猜最近会话。 |
| effort | 原生 `--effort` 支持 `low/medium/high/xhigh/max`，只覆盖本次调用；交互 `/effort`会把选择持久化为模型变体。[P3] | CLI settings schema没有独立effort键；不能仅从model label括号文字构造额外effort。 |
| 临时settings | `--settings`只覆盖本次session，不保存，内容为settings.json字段。[P2][P10] | **不能用 `hooks:{}` 禁采集**：官方schema明确hooks字段不读取。plugins也不在settings字段契约中。[P4] |
| 私有目录参数 | 本机接受 `--gemini-dir <绝对私有根>`、`--app-data-dir <绝对私有根>`、`--include-default-customizations=false` 与help组合；ACP help也公开这些参数。[P10] | 仅语法通过，不代表已经验证所有global／project／builtin定制均被关闭。 |
| 原Google登录 | 官方local silent keyring sign-in说明原生agy从Apple Keychain等OS keyring读取已保存token profile；保留原OS用户／HOME，让native自行读取有官方依据。[P5] | 私有GeminiDir组合下是否找到该用户的具体profile未执行验证；未读／复制任何token。 |
| 工具禁用 | SDK的 `CapabilitiesConfig(enabled_tools=[],enable_subagents=False)` 真正移除工具，源码生成各工具 `enabled=false` 的proto。[P6][P7] | 公开SDK的LocalAgentConfig与原生print CLI是不同入口，不能把SDK字段直接塞进 `--settings`；Markdown `tools:[]` 的本机loader行为仍待核验。 |
| 取消 | print可由已有独立进程组取消；ACP有原生 `session/cancel`，取消开放工具及pending permissions。[P1][P8] | 本轮没有在途任务，不记真实取消验收通过。 |

### 模型配置与精确conversation身份

官方settings文档说明CLI采用稀疏持久化，省略字段保留省略；`model`保存 `/model` 选中的label，CLI flags仅本次覆盖。本机 `~/.gemini/config/config.json` 没有model／effort，App和CLI各自目录也没有 `config.json`，不能从全局plugin配置推导模型。[P2][P4][P10]

对这次明确默认配置，模型请求快照应标明 `source=cli_default_settings` 并保留原始label。原生 `--model`既有官方headless slug说明，也有settings页使用friendly label的例子；不要自行把显示文字改写为某个替代模型slug。原生找不到显式 `--model` 时会报 `ERROR`，不要捕获后改用默认模型继续。未独立配置的effort不补默认；model label中的 `High` 不等于应用又传了 `--effort high`。[P1][P2][P3]

官方 `/effort` 文档另提供headless只读查询方式，但这仍是 `-p` 命令，本轮遵守用户“不执行任何prompt”的要求，没有执行它。也没有调用 `/model`、`--continue` 或 `--conversation`。[P3]

精确绑定会话时，可使用宿主hook原生 `conversationId`、`modelName` 回执，或已取得的官方ACP会话model／thinking配置；身份与模型必须来自同一请求快照。无该身份只读上述默认配置，不能按修改时间选择会话。[G1][P8]

本机只读结构核对发现以下元数据路径：

| 路径／结构 | 当前可确认内容 |
| --- | --- |
| `~/.gemini/antigravity/conversation_summaries.db` | SQLite表 `conversation_summaries`，主键 `conversation_id`，包含 `source`、`app_data_dir`、`raw_summary`、`last_user_input_step_index`等；**没有显式model／effort列**。 |
| `~/.gemini/antigravity-cli/conversation_summaries.db` | 相同主表，另有parent映射。未读取标题、preview、用户／助手正文或按时间查行。 |
| App `brain/<conversationId>/.system_generated/logs/transcript.jsonl` | 官方hooks路径；本机文件名还包括 `transcript_full.jsonl` 及chunks。本轮只查看路径模式，未读取聊天正文。 |
| `antigravity_state.pbtxt`／`jetski_state.pbtxt` | 本机顶层只含installation／migration／onboarding相关字段，没有明确模型字段。 |

SQLite schema读取使用只读URI `mode=ro&immutable=1`；最初 `mode=ro` 因live文件状态无法打开，改为immutable只读后仅查询 `sqlite_master`。immutable视图不保证包含最新WAL内容，因此不将此方法当作运行期有效模型同步机制。[P10]

公开SDK的 `localharness.proto.ModelConfig` 确实定义模型名、API endpoint及 `GeminiModelOptions(thinking_level,service_tier,max_output_tokens)`，但这是SDK传输配置，**不是App summary blob的已确认磁盘schema**。本轮未找到可将该proto直接应用到 `raw_summary` 的官方映射，不冒称已支持解析App任意临时模型。[P7]

### stdin输入与stream-json结果

处理材料通过stdin发送，每行一个NDJSON对象：

```json
{"event":"user","message":{"content":"<本次处理材料>"}}
```

`content`也接受 `[{"type":"text","text":"<材料>"}]`。text是唯一受支持block type；其他类型会产生错误，不是静默跳过。不要把材料放在 `-p`／`--print` 的argv里。连续stdin模式每轮都有一个result；默认不要接续用户的已有日常conversation。[P1]

官方输入输出使用 **`event`**，不是Claude风格的 `type`；以下是canonical路径：

| 外层event | Payload | 字段与解释 |
| --- | --- | --- |
| `init` | `.init`，外层还有 `.conversation_id` | `cwd`、`tools`、`permission_mode`；`model`、`agent`仅显式override时出现；schema在 `json_schema`。不能从init没有model推出“无模型”。 |
| `step_update` | `.step_update` | `conversation_id`、`step_index`、`state`、`step_type`、`text_delta`、`duration_seconds`、`usage`，工具步骤还有 `tool_name/tool_info`，subagent步骤可有 `subagent_info`。 |
| `result` | `.result` | `conversation_id`、`status`、`response`、`error`、`duration_seconds`、`num_turns`、`usage`；schema结果在 `structured_output`，schema在 `json_schema`。 |

`step_type`官方例子包括 `user_input/agent_response/tool/checkpoint`；`state`为 `ACTIVE/DONE`。`text_delta`是增量，不能把每个DONE都当完整最终回复。连续session中 `response`只属于当前轮，而 `num_turns/usage/duration_seconds`是session累计值，不能每轮直接相加。[P1]

`--json-schema`接受JSON字符串、schema文件路径和部分primitive类型名；在stream模式只约束最终result，过程事件不服从业务schema。桥接应读取 `.result.structured_output` 并按本次schema校验，不能从中间文本或进程退出0推断结构化成功。标准字段没有 `finalText`／`structuredOutput`／`modelName`别名；本报告不据其他客户端格式增补aliases。[P1]

终态必须原样区分：`SUCCESS`为完成并产出；`ERROR`、`CANCELED`、`INTERRUPTED`、`INVALID`都不是成功；`WAITING`表示结束时仍等输入，`RUNNING`表示未达到终态。错误写stderr，并在JSON result的 `error` 中报告。未出现符合本次conversation／schema的SUCCESS result时保留未知或失败状态，不能只靠退出码／现有文本入库。[P1]

`--print-timeout`默认 `0s`，等到该轮完成；保持该原生默认，不增设180秒等总超时。关闭stdin／EOF是连续输入结束路径，实际退出和进程组清理仍须取得回执，不将“已关stdin”描述为全部进程已退出。[P1]

### 隔离配置与工具能力

Google官方customization discovery明确分开以下来源：[P9]

- 工作区及向上到repo根：`AGENTS.md`、`GEMINI.md`、`.agents/`及兼容dot-dir；非repo工作区只扫描该工作区。
- 原用户个人根：`~/.gemini/config/`的rules／skills／agents／plugins／hooks／MCP，另有 `~/.gemini/AGENTS.md`、`GEMINI.md`。
- CLI额外skills：`~/.gemini/antigravity-cli/skills/`、`~/.gemini/skills/`；个人manifest exclude不能隐藏这两处。
- Builtin定制及启用插件所带hooks／MCP／skills等。

因此任务cwd应是私有非repo目录；仅在仓库里新建空 `.agents` 不能隔离仓库根规则。`personal_customization_dir`是额外个人repo来源，discovery manifest也是追加，不是全局根替换。官方当日manifest文档还明确exclude不支持wildcards，不能写 `exclude:["*"]` 后声称全关闭。[P9]

本机help已确认以下参数拼写，所有核对命令都带 `--help`，未启动模型、也未创建所给help用私有根：[P10]

```text
--gemini-dir <absolute-task-gemini-root>
--app-data-dir <absolute-task-app-data-root>
--include-default-customizations=false
--disable-slash-commands
--settings <inline-json-or-settings-file>
--model <explicit-agent-model-snapshot>
--effort <only-if-explicitly-sourced>
--input-format stream-json
--output-format stream-json
--json-schema <business-schema-file>
```

`--gemini-dir`标为Gemini文件根，`--app-data-dir`标为应用数据根；ACP还有 `--cli_dir_name`。参数接受绝对路径，但help语法核对不是完整定制隔离验收。闭源CLI本机符号包含 `ServerBackend.SetIncludeDefaultCustomizations`、`store.Manager.SetIncludeDefaultCustomizationsOverride`，证明flag有原生实现入口，**不能仅凭函数名证明它关闭了global／workspace的每种加载源**。[P10]

`--settings`保留未覆盖字段；官方schema的 `hooks`明标deprecated且不读取，应使用原生独立hooks文件。schema `additionalProperties=true`表示保留未知字段，不表示 `plugins`、`enabledTools`等任意字段会被执行。permission deny是调用拦截，不能替代从模型上下文移除工具，也不能阻止宿主hook先行执行。[P4][P6]

官方支持Markdown自定义agent位于任务工作区 `.agents/agents/<name>.md`，所需frontmatter为 `name/description`。最小候选格式可据主源写为：[P11]

```yaml
---
name: memlane-processing
description: Memlane private memory processing
mainAgent: true
subagent: false
model: inherit
tools: []
mcpServers: []
skills: []
plugins: []
---
```

模型继承仍由同一请求快照的 `--model` 控制，不把该文件固定成替代模型。文档描述 `tools`为明确允许列表，但本轮没有执行本机loader来证明空列表的实际行为；陌生／拼错的tool名称有官方已知问题，不能用假名称构造“无工具”。原生help将 `--agent` 描述为Agent选择，公开文档确认按发现后的名称选择；仅有底层 `AgentPath`符号不足以证明任意绝对路径作为 `--agent`值被接受。**任务私有目录发现agent并选择其明确名称有主源依据；直接绝对路径方式需额外loader证据。**[P10][P11]

公开SDK提供更精确的工具能力证明：`CapabilitiesConfig.enabled_tools=[]`与 `disabled_tools`互斥，`connection.resolve_active_tools`使用 `is not None`保留明确空列表；`local_connection._to_harness_side_tools_proto`对command、file、web、image、subagent、workflow等逐项写 `enabled=false`。SDK的 `hooks/tools/mcp_servers/skills_paths/subagents`也可以为空。这里的传输类型是 `HarnessConfig`与 `HarnessSideTools`，不是CLI settings.json。[P6][P7]

该SDK main固定于 `75ba22bd00afae2f46393ff31f30c3f0df52982f`（2026-10-28 04:14:15 UTC），tree208项、未截断。官方SDK代码及安装文档目前主要使用Gemini API Key、Vertex／ADC或本地模型；不是直接复用App订阅模型的已验实现。不能为方便使用CapabilitiesConfig而把订阅桥自动换成Provider或安装SDK／runtime。[P5][P6][P7]

### 认证、取消与切换

官方认证页明确本机原生CLI会尝试OS secure keyring，Apple Keychain存在有效token profile时静默登录。保留用户HOME和OS身份，让原生客户端自行读取Keychain，是优先路径；不读取token、不复制登录目录或Keychain内容到任务目录／Provider。`--gemini-dir`会改变文件根，是否影响其具体登录profile发现不能只由HOME不变推断；本次未执行登录检查／模型任务，实际组合保持待验证。[P5][P10]

print使用已有进程组取消路径：创建独立session／进程组，取消时给拥有的组发SIGTERM，必要时SIGKILL，再核对全组消失。源码当前 `generation_validation.stop_process`／`process_groups.group_exists`已经有该机制；仅parent process退出不等于descendants回收，无法确认时保留 `*_group_exit_unconfirmed`，不标取消成功。[P12]

Google ACP文档明确取消结束开放工具、抛弃pending permission requests；本机原生协议含 `session/cancel`，ACP规范以显式 `sessionId`绑定取消。若未来采用ACP，可先发取消并等该prompt以cancelled stop reason结束，再以同样进程组机制负责关闭owned worker。`--process-per-session`用于隔离各会话worker；它不单独关闭全局定制或改变模型认证。[P8]

当前单次原生print桥无需仅为取消改造为ACP：保留一个任务一个owned进程组及明确终态即可。ACP model/thinking session配置可以补充明确会话快照，但官方指出不同ACP client对第三方模型的可用范围不同，不能为了隐藏这些差异而固定替代模型。[P8]

查看、编辑、保存桥接配置不更新实际active；明确切换成功的服务回执才更新active。切换与取消复用既有共享分派／进程所有权，不在研究中启动任务；协议可实施与实际模型／schema／取消／隔离验收分开记录。

### 增补引用

- [P1] Google [Headless mode](https://antigravity.google/docs/cli/headless/)、[同页官方Markdown](https://antigravity.google/docs/cli/headless.md)，当日完整读取；stdin、result、schema、状态、超时与累计计数。
- [P2] Google [Settings](https://antigravity.google/docs/settings?tab=cli)、[同页官方Markdown](https://antigravity.google/docs/settings.md?tab=cli)，CLI设置位置、稀疏持久化及单次flags；[CLI reference](https://antigravity.google/docs/cli/reference.md)。
- [P3] Google [Effort](https://antigravity.google/docs/cli/commands/effort.md)，effort会切模型变体，原生flag仅单次覆盖。本轮未运行文档中的任何 `-p` 查询／示例。
- [P4] Google [settings.json schema](https://antigravity.google/schemas/v1/settings.json)，`properties.hooks`明确Not read；`model`保存label，`additionalProperties=true`不等于执行未知key。
- [P5] Google [Authenticate](https://antigravity.google/docs/install.md)，本机silent keyring及SDK的API/Vertex/本地模型认证范围。
- [P6] Google [SDK tools](https://antigravity.google/docs/sdk/tools.md)；官方SDK [types.py](https://github.com/google-antigravity/antigravity-sdk-python/blob/75ba22bd00afae2f46393ff31f30c3f0df52982f/google/antigravity/types.py#L696)、[resolve_active_tools](https://github.com/google-antigravity/antigravity-sdk-python/blob/75ba22bd00afae2f46393ff31f30c3f0df52982f/google/antigravity/connections/connection.py#L112)。
- [P7] 官方SDK [localharness.proto](https://github.com/google-antigravity/antigravity-sdk-python/blob/75ba22bd00afae2f46393ff31f30c3f0df52982f/google/antigravity/proto/localharness.proto#L86)、[工具开关序列化](https://github.com/google-antigravity/antigravity-sdk-python/blob/75ba22bd00afae2f46393ff31f30c3f0df52982f/google/antigravity/connections/local/local_connection.py#L1031)、[LocalAgentConfig](https://github.com/google-antigravity/antigravity-sdk-python/blob/75ba22bd00afae2f46393ff31f30c3f0df52982f/google/antigravity/connections/local/local_connection_config.py#L193)、[固定commit](https://github.com/google-antigravity/antigravity-sdk-python/commit/75ba22bd00afae2f46393ff31f30c3f0df52982f)。
- [P8] Google [Other editors / ACP](https://antigravity.google/docs/ide/extensions/acp.md)、[ACP入口](https://antigravity.google/docs/cli/acp.md)，取消、会话model/thinking与worker flags；原生 `session/cancel`也由bundled协议符号核对。ACP外部规范的完整取消回执本轮未另行执行验证。
- [P9] Google [Customization discovery](https://antigravity.google/docs/customization-discovery.md)，根目录、repo扫描停止、manifest追加、无wildcards和CLI额外skills的例外。
- [P10] 本机Gemini1.144.9.1499 bundled `Contents/Helpers/agy` help：`python3 scripts/task.py -- <agy> --help`、`<agy> acp --help`以及私有目录flags与help的组合均只打印帮助；配置标量／元数据schema只读，未调用任何prompt。
- [P11] Google [Custom subagents](https://antigravity.google/docs/subagents.md)，Markdown frontmatter、tools／MCP／skills／plugins及已知tool校验限制。
- [P12] 本地 [进程组核对](../engine/python/process_groups.py)、[既有受管进程取消](../engine/python/generation_validation.py#L215)，只读源码，不代表本轮真实取消验收。

## 2026-11-19后续纠正：root print参数与认证前提

本节记录后续真实无输入启动及独立只读认证调查。**此前将“与 `--help` 同用退出0”作为这些隐藏参数在root print可用的证明，判断不充分：help可能提前退出，未进入实际参数／执行校验。**原记录保留为当时的帮助输出证据，本节纠正其解释，不改成实际运行通过。

### 新的实际回执

| 场景 | 已取得的事实 | 能证明的范围 |
| --- | --- | --- |
| root print／stream flags加 `--gemini-dir`，无输入 | 实际stderr为 `flags provided but not defined: -gemini-dir`。[A1] | **root执行入口拒绝该flag**；ACP help公开该flag不能外推root print也支持。不能继续采用先前参数组合。 |
| 私有HOME／私有workspace、按Agent名称及原生stream flags，无输入 | 原生返回 `event=result`，错误 `authentication required`；所属进程组已清理。[A1] | 该启动未满足原生认证前提。没有提交prompt或运行模型；无法仅据此判断是私有HOME破坏profile发现，还是CLI原来未登录／其他认证差异。 |
| 原HOME下只读 `agy models` | stdin为DEVNULL，20秒有界，退出0，进程组已不存在。stderr仅 `Fetching available models...`，stdout返回model目录。[A2] | 原用户环境能取得model目录；**目录成功不等于prompt认证或订阅模型调用成功**，也不能与不同入口的上述失败组成完整因果对照。 |

本轮独立调查只执行了 `agy models`，没有启动登录、打开浏览器、发送任何实际或模拟prompt，也没有改变用户配置。无输入启动两条回执来自同轮根代理，保留与此只读catalog调查的证据区分。[A1][A2]

原HOME model目录明确包含以下映射，现可由目录回执规范化名称，不能再把它当作推测slug：[A2]

| 原生model ID | 原生label |
| --- | --- |
| `gemini-3.8-flash-high` | `Gemini 3.8 Flash (High)` |
| `gemini-3.8-flash-medium` | `Gemini 3.8 Flash (Medium)` |
| `gemini-3.8-flash-low` | `Gemini 3.8 Flash (Low)` |
| `claude-sonnet-4-6` | `Claude Sonnet 4.6 (Thinking)` |
| `claude-opus-4-6-thinking` | `Claude Opus 4.6 (Thinking)` |
| `gpt-oss-120b-medium` | `GPT-OSS 120B (Medium)` |

结合前节的明确settings，本机CLI默认label可通过目录对应 `gemini-3.8-flash-high`，source仍是 `cli_default_settings`。不能因为Gemini App也安装在这台电脑，就把它标为App前台临时模型。

### 原生credential profile与App host bridge

Google官方认证页声明local CLI会访问OS secure keyring，有valid token profile时静默认证。其适用前提是**原生找到可用profile**，不是“只要OS用户不变就保证任意私有HOME启动已认证”。[A3]

本次只在已知 `~/.gemini`、`~/.gemini/config`、`~/.gemini/antigravity`、`~/.gemini/antigravity-cli` 的直接子项检查文件名／类型和非secret JSON顶层字段；没有遍历其他私有目录。看到的相关文件为 `settings.json`／`config.json`，未找到可确认的credential-profile索引文件。没有读取任何auth／token文件内容、Keychain项目值或复制认证材料。[A2]

bundled agy有以下原生类型／函数符号：[A4]

- `codeassistclient.KeyringTokenStorage.LoadStoredToken`／`LoadToken`。
- `codeassistclient.compositeTokenStorage.loadFromKeyring`。
- `codeassistclient.DefaultKeyringMarkerPath`及 `keyring-marker-*` 字符串。
- 内置 `zalando/go-keyring` 的 `macOSXKeychain.Get`。

这些证明native CLI包含Keychain读取机制；未公开其本机credential-profile索引的精确文件路径／结构。`keyring-marker-*`只定位到可用性marker的符号／模式，不能当作登录profile路径。此阶段没有足够证据给出某个文件symlink／拷贝就是“最小auth引用”的实施建议，更不能复制token绕过该缺口。

Gemini主程序则明确把以下参数传给其bundled helper：[A4]

```text
--host_bridge_url=http://127.0.0.1:<port>
--read_host_bridge_token_from_stdin
```

helper的原生proto包含 `exa.host_bridge_pb.HostBridgeService/GetOAuthToken`；`GetOAuthTokenRequest`为空请求，响应有 `token_info`，其字段为 `access_token/token_type/refresh_token/expiry/is_gcp_tos`。本次只从程序自带descriptor识别schema，未调用该接口或读取字段值。[A4]

这是App能给内置helper提供OAuth token的直接通道证据，但不能推成“普通CLI必需App host bridge”。官方CLI另有独立silent Keychain路径。helper字符串还明确提示 `host_bridge_url was passed without host_bridge_token; not attaching a host bridge`，说明单独提供URL并不足以连接，桥接认证材料必须由合法宿主提供；本轮不猜端口、不提取桥接token、不启动App桥接请求。[A3][A4]

### 当前最小结论与后续边界

root print需按其实际支持的原生flags实现；不要再使用仅ACP help列出的私有根flags。私人HOME能够隔离文件来源，但认证profile是否仍可被同OS用户找到保持未知。原HOME下的目录成功不等于能安全保留原HOME运行记忆处理：其global hooks／插件仍需有明确隔离证据。

当前没有足够证据同时宣称“私有HOME完全隔离”与“复用已登录App账号已通过”。同样参数、同样无输入路径的认证对照或native明确profile回执能进一步区分原因；这属于额外核验，不以本轮目录结果代替。继续保持无prompt／无模型边界，不以重新登录、复制认证文件、改Provider或固定另一模型掩盖认证未知。

本轮root runner／解析／取消的源码检查和真实账号调用是不同完成条件。`authentication required`须作为明确前提错误反馈，不能记为结构化模型质量失败或猜“模型不存在”；原始失败与进程组回收证据继续保留。

### 本次纠正引用

- [A1] 同轮根代理提供的两次无输入实际启动回执：root `-gemini-dir`未知flag及私有HOME的原生 `event=result/authentication required`；均未提交prompt，进程组已回收。对应失败保持原义，本节纠正先前help解释。
- [A2] 本轮本机只读 `python3 scripts/task.py -- <bounded Python subprocess>` → `/Applications/Gemini.app/Contents/Helpers/agy models`；stdin DEVNULL、20秒等待、必要时owned组TERM/KILL，实际退出0并确认组消失；目录及非secret文件名／schema检查未读取token。
- [A3] Google [Authenticate](https://antigravity.google/docs/install.md)，local silent keyring sign-in有profile前提；本轮直接重读官方文字。
- [A4] `/Applications/Gemini.app/Contents/Helpers/agy` 与主程序的只读符号／descriptor／参数字符串检查：KeyringTokenStorage、macOSXKeychain、host bridge OAuth schema及缺token提示。闭源二进制符号是原生机制证据，不等于完成实际认证或确定磁盘profile路径。
