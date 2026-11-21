# Agent 有效模型读取与调用契约

用户于2026-11-18要求：产品使用各 Agent 自己的模型设置；不通过修改本机配置、固定模型名或复制 API key 实现。后续 Agent 接入沿用本方案。本文区分实现规则与验收状态，不把配置读取成功等同于真实推理成功。

## 选择规则

每个请求进入执行槽时解析所选 Agent 的有效模型，形成不可变快照；在途请求不随配置变化，后续请求重新读取。启动、验证、切换、后台处理和用户画像导入使用同一读取入口。Memlane 保存的旧模型字段仅作路由与历史记录，不能覆盖 Agent 当前设置。

优先级：

1. 有明确会话身份且属于所选 Agent home 时，读取官方会话元数据中的模型、供应商及显式思考设置。
2. 对会话元数据未提供的字段，继承所选 Agent 配置文件中的显式配置；命名 profile 按官方配置层覆盖。
3. 没有可确认的会话身份时，使用该 Agent 的默认配置，标记来源为 `agent_config`；不能声称获得了前台窗口或内存中的设置。

不按最近修改时间挑选会话。多窗口没有唯一“当前会话”；后台服务继承的调用会话身份也不等于后来用户切换到的窗口。若产品需要追随另一会话，须由官方协议或明确绑定提供身份，再走同一解析入口，不能扫描聊天内容猜测。

已知会话存在却无法读取模型、配置损坏或来源不支持时，返回明确错误，不回退旧 Memlane 模型、其他 Agent 或客户端硬编码模型。提供模型名的原生接口缺口应记录为能力缺口。

## 当前适配

| Agent | 配置地址相对于选定 home | 会话来源 | 显式参数 |
| --- | --- | --- | --- |
| Codex | `config.toml`，支持选中 profile | 精确 `CODEX_THREAD_ID` / `CODEX_SESSION_ID` 对应官方 `state_*.sqlite` 的 threads 行 | model、provider、reasoning effort、service tier |
| Antigravity（Gemini App） | 选定 profile 为 `~/.gemini/config`，CLI 模型配置读自同级 `../antigravity-cli/settings.json`（即 `~/.gemini/antigravity-cli/settings.json`） | 当前实现读取 CLI 默认配置（`source: agent_config`、`configuration_source: agy_cli_settings`），不扫描或猜测前台对话窗口 | `model`（`provider: google`；原生模型别名可能隐含推理档位，不额外推断 `--effort` 或 `service_tier`） |
| TraeX | `traecli.toml`，支持选中 profile | 精确 TRAE 会话身份；兼容身份仅在该 Trae home 官方数据库中确实存在时生效 | model、provider、reasoning effort、service tier |
| pi | `settings.json` | 当前实现使用默认配置，尚未接入会话临时覆盖 | defaultProvider、defaultModel、defaultThinkingLevel |

读取入口：`engine/python/agent_bridge_runtime.py` 的 `read_agent_defaults`（其中 `antigravity` 委托给 `engine/python/antigravity_defaults.py` 的 `read_defaults` / `settings_path`）、`current_session_model`、`snapshot_agent_defaults`。会话数据库只读连接、参数化精确查找，只取模型字段，不读取会话正文。返回来源及会话标识供诊断；服务 tier 未出现在会话元数据时，只继承配置中显式值，例如 `fast`。不能把界面显示的 Fast 凭空补成默认。

## 调用隔离

认证继续引用原 Agent 的登录，不读取、展示或复制密钥到 Provider。Agent 模型代理（`agent_bridge`）只建立任务私有配置，继承已解析的模型标量，禁用工具、hooks、插件和采集，防止提炼结果回采与循环。并发请求上限在 `generation_config.py` 与 `codex_completion_endpoint.py` 统一收敛为 `AGENT_BRIDGE_MAX_CONCURRENT_REQUESTS = 32`；LLM 出站改写传输层（`generation_http.py` `ClientCache.set_cache`）在启用并发闸门或语言出站改写（`gate is not None or request_rewriter is not None`）时均生效。

Codex / TraeX 代理通过官方 app-server；Antigravity（Gemini App）代理（`engine/python/antigravity_completion.py`）通过原生 `agy --input-format stream-json --output-format stream-json --model <model> --agent memlane-processing --json-schema <schema> --disable-slash-commands` 在仓库外临时工作区与私有 `.gemini/config`（清空 `plugins`/`hooks`/`mcpServers` 与 `tools: []`）中运行，复用操作系统用户 keyring 登录而不复制凭据、历史或自定义配置。画像导入通过各宿主只读 CLI 链路执行，与后台提炼链路分别验收、不能互相替代：画像只读导出原生已有记忆，不启动原生记忆生成任务；内部记忆模型若必须传入，统一使用同一模型快照。此约束仅覆盖 Memlane 发起的调用，不修改独立运行的外部 Agent 行为。

Codex 画像使用官方 `exec --ignore-user-config` 后显式传入所需模型参数（并按命名 profile 合并 `features`/`memories`），保持原 home 与原生记忆可读；Antigravity 画像（`engine/python/user_profiles/providers/antigravity.py`）与 pi 画像（`providers/pi.py`）均统一经 `read_agent_defaults` 读取有效模型，前者在临时隔离 git 工作区中通过 `agy --output-format json --mode plan --model <model> --disable-slash-commands --sandbox --print=...` 配合 `PreToolUse` 工具拒绝钩子执行，后者沿用原配置与结构化适配不固定替代模型；TraeX home 与 CLI home 分别指向选定目录及其 `cli` 子目录。画像运行器（`user_profiles/runner.py`）启动子进程前剥离 `LLM_*`、`OPENAI_*`、`ANTHROPIC_*`、`GEMINI_*`、`OLLAMA_*`、`FALLBACK_*`、`COGNEE_*` 与 `MEMLANE_PROJECT_*` 环境变量，杜绝跨通道环境污染。

直连 Provider 仍由用户明确选择模型，与 Agent 继承规则分开。采样/输出参数共用 `generation_config.native_llm_configuration` / `native_llm_kwargs`；未配置保持未配置，显式 0 保留，不继承前一配置环境。

## 状态与错误

设置及初始化共用解析结果展示模型、显式思考级别与服务 tier，不再让用户在 Memlane 重填 Agent 模型。

**统一启动与热切换校验契约（2026-11-22 同步）**：
- `engine/python/agent_bridge_runtime.py` 提供单一启动门禁 `require_bridge_startup(store, runtime, row)` 及共享辅助 `mark_connected_bridge_selection`、`bridge_selection_authorized`、`require_bridge_validation`，由 `scripts/dev.py`、`generation_startup.py`、`generation_handoff.py`、`generation_config.py` 与 `generation_bridge.py` 统一调用，消除热切换与重启校验口径分叉。
- 通过「已连接 Agent 选择」授权（在账本中记录 `connected_agent_selections[digest] = True`）或存在历史 `status == 'applied'` 热切换回执的 `agent_bridge` 修订，重启时统一核对 `check_bridge_runtime`、`read_agent_defaults`（及 `pi` 的 `models.json` 合法性）后放行；未经已连接选择授权的非 Codex 修订仍须具备与当前 `bridge_runtime_fingerprint` 一致的显式验证通过回执，依赖或配置漂移时拒绝启动（`generation_bridge_changed_revalidate`）。
- 热切换收尾 `generation_handoff.py:apply_receipt()` 确认引擎 `runtime_active` 后先原子持久化 `active`、`connected_agent_selections`、清 `next` 与 `status='applied'`，随后再以只读方式（`write=False`）逐会话容错对账取消会话，避免旧撤权项目 403/409 造成引擎与控制面 `active` 脑裂或 `review()` 永久卡死。
- 运行期热切换（`generation_handoff_runtime.py`）同步清理/恢复 `litellm.model_cost` 注册、Ollama `register_verified_schema` 的 `restore()` 回调及 Cognee `CHUNKS_PER_BATCH` 缓存；桌面端（`integrations.go`、`generation_workspace.go`）在任意保存错误或表单切换时清零 `GenerationAgentApply`，杜绝残留误触发切换。

画像主界面只保留自动导入、手动导入及已有文档。失败显示脱敏原生错误与退出码；不以泛化提示代替实际错误，不保存原始 stderr。重试保留原操作元数据；保存结果未知先核对，不能重复生成。

## 后续 Agent 接入要求

1. 先确认官方配置路径、profile 合并顺序、会话身份与可用元数据；明确默认配置和临时会话覆盖的差异。
2. 将适配加入共享解析入口，返回模型、供应商、可支持的显式选项与来源。缺少官方信息时暴露缺口，不创建猜测性回退。
3. 验证原生请求实际使用该快照；登录、全局文件及原始记忆保持不变。检查取消后所属进程回收。
4. 覆盖全局与会话模型不同、多会话精确匹配、无会话时默认来源、显式 Fast/思考级别、配置变化、在途冻结、缺失拒绝和旧环境隔离。
5. 启动、验证、切换、处理与画像分别跑通连续流程；源码测试、真实协议与 Native 展示分别记录，不能互相冒充。

提交必须通过参数 AST lint，以及 `test_generation_parameters`、`test_generation_probe`、`test_generation_handoff`、`test_generation_startup` 和相关 Agent 的协议回归。具体失败累计遵循仓库停止规则，不因换模型、路径或 Agent 重置。

## 本次证据与剩余边界

`test_agent_defaults` 当前4项通过：三桥请求动态读取/在途冻结、缺失拒绝、命名 profile，以及 Codex/TraeX 精确会话优先与显式 Fast 继承。之前三桥真实推理已通过，但其中 Codex 当时只读取全局6.2，不能作为当前会话6.1已通过的证据；最新真实调用与画像入库结果继续记入执行记录。

pi 临时会话覆盖、独立运行的 TraeX 原生后台记忆默认模型问题，不在当前已实现范围内。后续适配必须补相应官方来源与证据后再标记支持。

## 方式互斥及展示防回归

Agent 模型代理（`agent_bridge`）和直连 Provider 允许同时配置，但实际记忆提炼只能二选一：唯一 `active` 引用决定执行，`next` 仅表示待切换候选，不是第二执行者。切换遵循现有排空、验证、提交及失败保留协议；不创建按方式分别的 active。初始化与设置共用 `generationConfiguration`，浏览、编辑或保存草稿不能启用、切换或重发请求。

统一 `generationProfileGroups` 投影：Agent 模型代理以 Agent 身份为主标题、实时模型及显式参数为详情；Provider 以供应商+地址分组、下列各模型。分组不合并配置、认证或修订。UI 方式互斥、默认参数、模型独立编辑及不同地址不混组由 Go 行为回归测试（`generation_presentation_test.go`、`integrations_test.go` 等）保障；`scripts/lint.py` 现行静态门禁覆盖 `ML001`–`ML003`（模型配置构造与零值/省略守卫）、`ML006`（画像生成 `user_profiles/runner.py` 禁用固定总时长超时）、`ML007`（非白名单模块禁止修改全局 `os.environ`）及 `ML008`–`ML017`（UI 性能与运行时资源门禁），早期脆弱的界面文本规则已从 lint 精简退役并移交行为单测。

最新真实 Codex 调用使用会话6.1/medium/fast并通过；真实画像自动导入完成，官方库重新读取19058字节并匹配文档引用。Native实窗核对两个导入操作与保存文档，以及查看Provider页签时当前执行仍为Agent模型代理。相关执行记录保留失败轮次和历史6.2结果。

2026-11-18 22:29 用户再纠正：Provider与Agent配置在同一个记忆提炼页签，用绑定实际运行方式的单选项/分段控件，禁止以两个浏览配置页签冒充实际选择。切换确认取消时原active及选中状态保持；初始化未启用时只有配置草稿。上述交互不变量由 Go 行为与连续交互回归防回归，覆盖上文及此前报告的“配置视图页签”表述；画像导出任务取消固定 180 秒总截止（由 `ML006` 静态门禁守卫），区分输出活动、等待、进程退出和用户停止。
