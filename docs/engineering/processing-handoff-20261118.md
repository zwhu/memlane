> 2026-11-18最终更新：本轮三桥与净室主流程已通过，当前实现及边界以[结构化验收报告](cleanroom-acceptance-report-20261118.md)为准。下文各“正在／待完成”保留其历史时点，不代表最终状态。

# 更换处理配置并接续积压

## 最新用户纠正：Codex、TraeX、pi 都需支持

用户在净室续验中明确指出，本地此前支持pi，已要求Codex/TraeX/pi三者均可处理记忆。下文“本轮不额外承诺pi/TraeX桥”为此前收窄的实施边界，不再代表本轮授权范围；只实现Codex不能称完成。用户拒绝把pi的DeepSeek key转存成Memlane Provider，不读取/复制该key，不用API配置替代Agent桥。

本轮按plan-change-impact与verify-continuous-scenarios推进：复用现有不可变处理配置、验证、启动、实时切换、共享参数构造器和受管进程生命周期；增加pi/TraeX类型、可执行程序及配置目录解析、模型选择和同页Native展示。旧pi0.84.1的RPC与受信Schema扩展作为已有实现依据，不能把历史结果外推当前Python链路；TraeX先核对实际app-server协议及工具隔离，再接入。Agent自行读取已有登录，处理会话禁用日常采集、工具、skills/hooks及历史配置，原始记录来源主体不变。

连续验收：选择各Agent/实际模型→保存→完整Schema真实验证→明确启用→保存及整理→新会话召回；分别覆盖未登录、配置漂移、取消及进程组确认退出、重开核对、从Codex切换到pi/TraeX，未配置参数保持未配置，显式0保留。静态与本地协议检查先于实际Agent；源码任务一律task.py，不启停共享日常服务、不进入release。净室Codex继续按用户要求跳过，但本地Codex回归保留。旧qwen质量停止及VM工作卷3轮失败均不重置，不继续其原路径。

当前：现有generation_config仅允许codex，generation_bridge与scripts/dev.py启动同样硬编码Codex；旧pi实现保存在b77517d的internal/agents/pi_rpc.go、pi_completion.go与pi-structured-output.ts。正在核对TraeX0.215.0官方CLI导出的协议Schema，无真实模型调用。不得据prepare/plugin加载/pi聊天成功宣称后台记忆处理已接通。

本轮实施已更新上述状态：三种桥共用配置/验证/启动/切换，pi RPC恢复完整Schema扩展，TraeX复用其实际app-server的outputSchema、空工作区及固定禁止工具的permissions；当前已有登录只供Agent子进程自行读取。UI提供三种类型并读取已有模型标量，不转存Provider key。pi与TraeX均已真实返回并通过一个合成对象Schema，受管进程组为空。pi进一步通过当前Cognee官方适配器的普通响应/嵌套Schema/固定事实质量全部检查，耗时18.296s，报告.local/first-install-20261118/real-agent-validation/pi-result.json，非保存/召回闭环。

TraeX首次适配中分开记录：TRX-START-ARGS01第1轮拒绝app-server不支持的--no-daemon，删除该参数后启动通过；TRX-CONFIG-FEATURE01第1轮严格配置拒绝features.view_image，实际features list确认view_image/remote_plugin均不存在，移除这两项后通过；TRX-AUTH-LAYOUT01第1轮无法读取登录，只读login status三种私有布局确认TRAECLI_HOME应为private/cli，修正后真实Schema请求通过。以上均保留原错误，不合并为同一问题4轮。

TRX-TEXT-ENVELOPE01第1轮：完整Cognee验证的plain请求要求精确文本，TraeX未返回可校验JSON文本信封，桥正确拒绝并收尾；SDK回执result_unknown/provider_unavailable，plain/schema/quality均不通过，原回执及日志保留。修正仅在TraeX受信developerInstructions明确输出Schema/文本信封语义，仍在原生outputSchema传完整约束，本机照原Schema严格校验，不降级为纯提示词。第2轮三项全部通过，耗时21.128s，报告`.local/first-install-20261118/real-agent-validation-r2/traex-result.json`；第1轮保留，未重投业务材料。

真实活动取消：pi、TraeX分别观察到流式消息后取消，均确认cancelled、owned_groups为空、all_groups_exited为true；报告在`.local/first-install-20261118/real-agent-cancel/`。此前模型验证发生于依赖指纹门禁加入前，不能将这些旧回执直接用于当前启用；当前新验证记录可执行程序身份、pi模型配置和受信扩展指纹，验证或启用期间漂移均拒绝。登录文件由Agent读取，不读取其正文生成指纹。

最终源码检查：32项桥接/切换/启动/首次启用/pi检查通过，19项开发启动器回归通过，模型参数lint及其6项回归通过，完整Go测试、simpledesktop vet及diff检查通过。启动器桥接元数据模块仅用标准库，Python3.9的`-S`导入通过，第三方SDK只由受管Python3.12加载。Native已实窗核对Codex/pi/TraeX三种选择，未保存或改变宿主日常配置。新版调试候选`guest-tools/three-agent-1/Memlane Dev.app`已签名核验，二进制SHA256为`5ceaa8101df0058345033d3ff874df03f84fe57b7df823627e66a8b29367303b`；仅开发GUI及源码资源，不是release完整包。

净室已按owner配置、PID出生时间和参数确认旧setup服务，正常停止后回执`guest_setup_handoff_complete=true`、`remaining_owned_pids=[]`。新候选已启动，正在核对实际配置和首次启用；保留原组件、库与Agent登录，不触碰宿主日常服务。完整保存/整理/召回仍待完成。

净室新增实际证据：三步Native中选择pi，自动读取`deepseek/deepseek-v4-flash`；保存后原生Schema验证通过，plain/schema/quality均true；明确选择下次使用及“启用并完成”，first_install.setup变为false，active为该pi配置，进入日常首页。完整内容服务及管理HTTP200。此处使用Agent原有登录，未复制登录密钥到Provider。真实合成仓库`/Users/zwhu/memlane-cleanroom-demo`的pi新会话退出0，环境/插件身份核对通过，官方capture-status的qa_count=1，历史接口最终读到对应问答，连接capture.state=healthy。

采集核对方法修正：最初只看连接基础status（仍为configured_pending_new_session），漏读独立capture字段；在实际入库期间读取历史尚为0，后续原会话只读核对为1且事实完整，不能据早期查询认定丢失。诊断脚本曾错误按list读取generation.profiles（实际map）、使用project.path（实际aliases）；修正后读取通过。在只读VirtioFS中原地改脚本出现guest缓存null bytes，宿主同文件0个null且AST通过，新唯一文件名读取成功；均是验收方法问题，未重发业务消息、不重置具体产品缺陷计数。

知识产出仍未通过：首个合成提示词只要求回复确认，官方improve日志为`no_agent_entries documents=0`，仅产出会话原文文档，不能把pipeline completed算知识成功。保留该会话，补充完整技术说明合成会话继续验收，不回放原问答；具体no_agent_entries条件尚需核对固定SDK，未据此认定模型质量或产品错误。用户新要求：全部通过后提供结构化报告并commit/push；当前尚未提交或推送。

上述知识产出停点后续更新：第二条完整纯问答退出0、回答7903字节，原文保存成功，仍未生成知识。固定SDK的distill读取经过门槛的session context；ExtractAgentContextStage从工具轨迹提取上下文，纯问答正文增长不能替代该前置。第三轮使用合成README、仅允许read工具，实际读文件再解释约定，退出0、回答4157字节。官方会话`codex_pi-bcd19ea0dd228a9be5657b19f5766b61463b9878dc1fe8a78e38485370d397ec`于guest日志12:03:37提炼completed、documents=1，12:03:49对应operation succeeded。实际Data `4d374f4d-235a-5202-a8f0-7f9224d4d954`含session_learnings及来源session node_set，原文296字节；不把其余三份user_sessions_from_cache原始文档算成知识。该知识并未同时保留合成代号和37秒数字，完整事实范围及跨Agent召回仍继续核对。前三轮材料与结果分别保留，不把前两轮标成知识通过；此处没有第4次失败或第5次尝试。

当前代码另完成本地Codex真实回归，读取现有实际模型`gpt-6.2-sol`，严格Schema返回`{"text":"bridge-ready"}`，owned_groups为空、退出0；回执`build/diagnostics/three-agent-current-codex-20261118.json`。未重启日常服务、未切换其配置，净室Codex仍按要求跳过。

跨Agent实测：TraeX普通exec（无信任/审批绕过）退出0，pi/TraeX的capture均healthy、environment_verified=true。TraeX回答准确复述官方296字节知识中的“README唯一验收约定来源、窗口锚点/闭合边界/去重TTL/重试次数未定义，须显式澄清”，对未进入知识的验收代号和37秒数字回答不知道。最初脚本只检查代号+37导致fact_recalled=false，不把它改写为三项事实全部命中；真实被注入知识与回答逐项核对一致。Native实际显示1知识、4原始记录和TraeX自动召回1条，知识卡来源为pi、召回次数1。TraeX另有自身记忆整合模型GPT-5.4元数据不可用的stderr，主请求/插件保存召回通过；没有为消除该日志修改其模型或全局配置。

新增独立缺陷EMPTY-COMMON-RECALL01：同一次实窗验收发现空通用库产生NoDataError动态（界面聚合5条后台事件，不计5轮人工验收）。已安装策略在GET候选为空时仍POST search。初步基础夹具复现后，将回归收紧到真正prepare安装产物；Codex/pi/TraeX均复现该多余POST，属于同一诊断轮次的子场景。修复在现有project_tool_patch和project_installation中做精确安装overlay，候选为空或只有非通用知识原始记录时不发搜索请求；项目块和已有通用知识搜索保持，原固定base插件及manifest不改、不在已安装路径原地改文件，新产物代次由已有tool_patch_sha256标识。65项工具/官方插件/连接/保存状态测试通过（47.767s）。新调试候选empty-common-1已签名核验，SHA256 `7e9b2f19af2f5500deac34719e8030e222031bbd67bf86a9c499a6def2305e59`；正在净室更新和实际回归，历史失败动态保留，尚未宣称该缺陷实窗关闭。

本地检查：新增pi完成/工具拒绝/重试拒绝/取消与模型配置保护、三种配置类型与修改后验证身份失效、显式可执行路径不回退；simpledesktop全套5.792s通过，21项completion/pi协议回归4.217s通过，19项桥/参数回归3.849s通过，45项参数/探针/切换/启动/首次启用97.136s通过。新增TraeX夹具最初仍读取CODEX_HOME导致自身退出，属于方法错误，改为TraeX实际路径后通过。19项dev启动器回归通过；lint通过。真实完整闭环、真实取消、配置漂移/启用与Native三种选择仍待完成；不release。

用户已授权“那就支持掉”。待保存原始记录由原发送器自动恢复，不用模型；待整理材料可以换已验证的处理配置。来源Agent身份、项目权限与处理执行者分开，不能把Codex来源改成pi来源。

范围：现有受支持、已验证的Codex订阅与Provider配置，立即切换后台记忆处理，接续未完成整理；不额外承诺尚未实现的pi/TraeX订阅桥。无需重启内容服务。复用Cognee官方LLMConfig ContextVar、原improve及水位、后台任务注册表和既有配置账本，不新增内容副本或并行处理引擎。

主流程：用户选择已验证配置→显示当前待整理会话/正在处理数量→明确“切换并接续”→停止接收旧配置新任务→取消已确认属于旧配置的improve并等待官方终态写入→结果不明则保留核对状态且不重投→启用新配置→原调度器提交剩余材料，沿用官方增量水位，保留已完成知识及原文。保存队列独立继续。用户取消弹框不产生操作。

影响：GenerationStore不可变修订与操作回执、管理API、服务请求级配置与可核对的取消、增量调度及未确认提交屏障、Native模型配置弹框与切换进度；不改项目ACL/采集规则/Embedding。切换发生时冻结所选修订，配置编辑不能影响在途操作。重启或丢回信只查询原操作，不另发起切换。并发切换拒绝，配置未验证或授权失败拒绝；未知旧任务不能被新模型绿色状态掩盖。

验证顺序：官方ContextVar真实SDK隔离/继承→取消与终态/未知屏障→已验证配置切换与持久回执→队列接续和增量水位→Native取消/确认/重开。先定向源码检查，再同库真实只读与受控模型链路验证；各自记录边界。开发阶段不构建release包。当前Codex编号队列恢复独立继续，服务71515及UI8505/UTM归原所有者，不擅自重启。

状态：设计与影响分析已记录，实施中；尚未交付。具体失败按问题分别计数，不将整个功能合并计次。

## 源码与连续检查

已实现原GenerationStore不可变修订上的handoff操作、管理API、服务任务级LLMConfig、当前配置实时生效、Native“更换”弹框/取消/确认/进度与重开只读核对。只有已验证配置可选，初始化仍走原next/首次安装路径。Provider与Codex配置统一列出，添加弹框可选Codex订阅或模型服务；pi/TraeX订阅桥没有新增，不将其采集接入当作后台处理能力。

服务只取消当前受管主体、同一原会话的明确improve任务，以API Key摘要和会话ID同时匹配来源，不把处理执行者写回来源Agent。取消复用固定SDK根任务及原OperationContext，等待官方后台收尾、HTTP容量和必要Codex进程退出证据；再读原pipeline_runs的ended_at/outcome/error_class及user_id，只有原取消终态已持久化才应用新配置。官方finish_operation会吞掉SQL写错，因此不能用task.done代替数据库回执。其他后台工作、无原会话绑定或旧提交结果未知时保留核对屏障。

仅确认取消的来源通过原绑定/策略和官方switch触发重新进入既有增量调度，不重置Cognee已应用标记、水位、知识或原文。旧插件在2xx受理后就记录节流，普通auto触发会因no_new_entries跳过；此处仅对确认取消的会话用官方switch触发，不删节流历史。已完成、其他未知、禁用或未授权会话不借切换自动重投。切换期间新整理暂停接纳，原始记录保存独立继续。

已通过：真实固定SDK的ContextVar继承与真实后台launcher、FastAPI/ASGI切换、真实临时原生关系表取消终态核对、模拟终态写入丢失时禁止应用；主控制器同操作幂等/丢回信只GET原回执/不同主体隔离；Native Tester选择/取消零POST/丢回信重开仅核对原操作。完整Go test与vet通过；77项相关Python通过（12.250秒），新增根终态保护后6项通过（6.609秒）。日志在build/diagnostics/generation-handoff-*。无实际模型调用或日常配置切换，尚待加载新服务及实际Native核对；合成图不算实窗验收。

方法错误：首次扩大Python集合误列不存在的test_generation_http，其他77项已通过但命令整体失败；去掉不存在模块后77项通过，不计产品验收失败。不把此方法错误、原capture的UUID解析错误或其他UI问题合并计数。

## 模型参数错误与 lint 防回归（2026-11-18）

HANDOFF-TEMP01：新切换代码把未填写的temperature补为0，导致gpt-6.1-sol在LiteLLM层拒绝，尚未到达Codex桥；不能归因为模型慢。累计3次失败证据保留，修复后的第4次检查通过，未达到第4次失败停止线。独立Codex桥真实调用通过，日常配置及内容未改变，证据为`build/diagnostics/generation-handoff-codex-model-fixed-20261118.log`。该结果覆盖隔离模型调用，不代表新的管理服务或Native切换已完成加载。

启动、验证和切换现统一通过`native_llm_configuration`，仅传入明确配置字段，并隔离前一配置的环境。未填写参数省略，用户明确填写0保留；供应商SDK自身行为仍须按真实协议检查。

用户要求加入lint后，新增`scripts/lint.py`，不安装依赖、不调用模型，扫描`engine/python`全部生产模块。ML001阻止独立或别名LLMConfig构造及共享入口内的旁路；ML002阻止字面量采样／输出值注入；ML003阻止get/setdefault补回退参数。首次扫描另发现`generation_ollama.verify_native_schema`硬编码temperature=0、max_tokens=256，一并移除。检查器5项回归覆盖原错误、字面量注入、别名构造、共享入口旁路、合法显式配置与语法错误。静态检查针对这些已知写法，不声称能证明任意动态Python代码的参数行为；原协议与环境隔离测试继续必跑。

执行入口：`python3 scripts/task.py -- python3 scripts/lint.py`；检查器测试：`python3 scripts/task.py -- python3 -m unittest discover -s scripts -p test_lint.py`。失败输出文件、行号、规则编号并非零退出，已加入AGENTS及工程规范。日常服务尚未因这项lint修改重启。

最终检查：lint通过；检查器6项测试通过（含入口非零失败及测试夹具排除）；参数／协议／接续／启动34项测试通过，95.064秒，日志`build/diagnostics/generation-lint-regression-20261118.log`。本轮协议回归使用本机合成端点，未发起新的真实账号模型调用。此前“5项”为首次检查时数量，追加入口测试后为6项。

## 最终实际回归

EMPTY-COMMON-RECALL01通过：按真实管理API准备并审阅新插件代次、激活后，pi与TraeX各开一个正常新会话。两者均退出0，准确召回README约束、明确不能自行补默认值，model_tool_actions均空。之后同一common dataset的NoDataError原操作ID集合仍为5个，集合与更新前完全一致；未删除旧动态或材料。两连接capture healthy、environment_verified=true，完整内容health ready/healthy，管理连接查询成功。原问题实测关闭；同一轮多个Agent子场景不拆轮计数。

净室TraeX使用已登录宿主配置中的GPT-5.6-Sol保存独立配置并执行现有验证API，plain/schema/quality全部true、structured_mode=json_schema；验证前后active和next完全不变，当前仍pi/deepseek/deepseek-v4-flash、next=null。未复制pi密钥到Provider。最新8项tool patch测试通过（含严格anchor拒绝），完整Go通过（bootstrap25.385s/simpledesktop11.222s），模型参数lint通过。最终只读脚本第1版把管理健康写成不存在的/health产生404，改用现有/v1/connections后全部断言通过；属于验收方法纠正，无业务消息重发。

最终Native导航刷新后显示1条记忆、6条原始记录，该记忆召回3次；两条最新pi/TraeX自动召回动态均为1条。API共7个Data对象包含1个session_learnings及6个原始记录，不把7个对象统称7条记忆。截图及脱敏元数据见evidence/cleanroom-20261118。主流程已收口；正式分发、VPN全局模式联网、净室Codex与扩APFS不据此改为通过。

提交前再核对暂存源：71项模型/协议/启动/首次安装测试通过（97.813s），Go全量通过。新增Provider表单原预填16384输出上限，已清除并在既有添加弹框测试核对序列化省略采样/输出字段；显式值与旧环境隔离回归保留。此为新建表单局部修正，不重投已验收净室业务。
