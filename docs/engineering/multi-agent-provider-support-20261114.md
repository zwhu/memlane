# 多 Agent / Provider 当前支持与验收矩阵

2026-11-14，dev；以当前源码及[执行账本](multi-agent-provider-execution-20261114.md)最新检查点为准。用户已暂缓未安装Agent／Cursor后续测试、免除Claude Code追加测试；未验项不改为通过，既有结果保留。实现、固定SDK协议、真实宿主／模型、实际Native分别列证；完整首批尚未交付。历史检查点保留当时状态，不以旧“待实施”覆盖后续事实。

## 第一批 Agent

| 要求 | 当前实现与真实证据 | 尚未证明的边界 |
| --- | --- | --- |
| MA01 Codex | 原实例ID／主体／ACL保持，实际隔离CLI安装、正常编码采集、采用既有pi／TraeX知识；[初轮回执](evidence/multi-agent-development-20261114/real-codex-checkpoint.json)。diff保留后新增真实顺序调整／26项回归、官方正确替换旧状态、新会话工具0次采用，MA-CODEX-FACT01目标关系复验通过、历史失败1保留；[位置关系证据](evidence/multi-agent-development-20261114/codex-position-regression.json) | Desktop独立证据未补；其他旧footprint的新鲜度不由此样本外推；旧错误正文取证保留，原Data被官方状态替换后404；前后端混合版本不作纯单版本通过 |
| MA08 pi | 全局扩展、独立主体、官方核心传输；实际编码→官方知识→新会话无工具采用；[编码链](evidence/multi-agent-development-20261114/real-coding-hosts.json)、[实际权限](evidence/multi-agent-development-20261114/real-agent-permissions.json) | 限固定版本及编码样本；原笼统约定／口令样本仍未通过，不泛化任意聊天沉淀；分支／恢复不回扫有定向检查，不冒充全部真实分支流程 |
| MA09 TraeX | 正常交互式CLI原生插件安装／信任、真实问答与工具、知识及新会话采用；后续兼容协议处理也通过；[编码链](evidence/multi-agent-development-20261114/real-coding-hosts.json)、[跨协议](evidence/multi-agent-development-20261114/real-cross-protocol-switch.json) | 证明TraeX CLI，不证明TRAE IDE；后续知识类清单未列全，不能当完整源码清单验收 |
| MA02 Claude Code | 原生CLI、受管官方插件、首轮QA／trace、官方知识及新会话无工具采用；[Claude回执](evidence/multi-agent-development-20261114/real-claude-checkpoint.json)。用户已明确不再要求追加测试 | 既有来源新鲜度／措辞限制、旧7条未投递材料保留；DeepSeek兼容端点不是Anthropic原生账号，后者属于独立Provider范围 |
| MA03 Cursor | 官方产物、独立主体／安装绑定、延迟召回及PA薄适配已实现并做固定SDK／适配检查；[宿主适配](evidence/multi-agent-development-20261114/host-adapters-checkpoint.json)。官方workspaceOpen加载不可变代际的管理器回归通过；[管理器证据](evidence/multi-agent-development-20261114/cursor-generation-loader.json)及既有后端145项通过 | **用户暂缓后续测试。** 本轮独立3.30.14准备遇SIGKILL／CUA超时，未取得真实宿主证据，临时App及镜像已清；[准备边界](evidence/multi-agent-development-20261114/cursor-host-preparation-deferred.json)。原当轮注入限制、宿主加载／代际共存／迁移未知及MA-CONN-CURSOR-UPGRADE01历史失败1均保留 |

## 共享、权限与故障

| 要求 | 当前证据 | 未完成或限制 |
| --- | --- | --- |
| MA04 跨Agent／项目／通用知识 | pi→TraeX／Codex新会话采用；pi／TraeX不同项目隔离；真实Native通用默认5与项目明确3由Claude正确采用；[通用优先级](evidence/multi-agent-development-20261114/real-common-priority.json) | Cursor实链依赖宿主；来源新鲜度及错误知识不能由“召回成功”抵消 |
| MA05 独立主体、read/write/delete、无share | pi／TraeX真实官方ACL、维护CRUD、只读／禁止、禁采恢复不回扫；四主体／八组实际项目ACL在跨协议重启后完全保持；实际Native Claude卡片连续变更；[权限矩阵](evidence/multi-agent-development-20261114/real-agent-permissions.json)、[Native卡片](evidence/multi-agent-development-20261114/native-cross-agent-checkpoint.json) | Codex／Claude已补显式脚本CRUD及四主体通用知识门禁，见[新增证据](evidence/multi-agent-development-20261114/real-explicit-tools-and-common.json)；四宿主已正常升级并产生新会话绑定，Codex／Claude实际缓存只读write403／delete403及common read200通过，见[正常升级](evidence/multi-agent-development-20261114/normal-host-tool-upgrade.json)；Cursor仍待真实宿主 |
| MA06 安装部分成功／取消／配置冲突／未知 | 实际Native+生产控制代码+合成主体，真实文件系统准备、丢弃响应、取消、重开原回执与仅处理剩余项；配置冲突及摘要有定向检查；[批量恢复](evidence/multi-agent-development-20261114/native-batch-recovery.json) | 合成Native故障不替代各真实宿主升级／退出行为；真正unknown继续保留，不因回执不存在自动重装 |
| MA06 服务不可达／403明确补交 | 固定插件禁止owner／CLI fallback；实际官方403后恢复不自动发送，明确replay恰好一条；[403回执](evidence/multi-agent-development-20261114/real-403-replay.json) | 403为受控真实ACL竞态注入，不冒充普通宿主自然发生；其他宿主仍按共用核心与各自独立证据说明 |

## 第一批知识处理与Native

| 要求 | 当前证据 | 未完成或限制 |
| --- | --- | --- |
| LP01 Codex桥 | 实际官方adapter普通输出／嵌套中文null／Schema／固定事实样本通过，隔离请求进程回收；[桥接回执](evidence/multi-agent-development-20261114/bridge-lifecycle-checkpoint.json) | 远端原生严格Schema模式保持unknown；合成探针不抵消真实知识质量失败 |
| LP01 DeepSeek兼容 | OpenAI兼容与Anthropic兼容两端点均有真实模型与实际处理证据；[跨协议切换](evidence/multi-agent-development-20261114/real-cross-protocol-switch.json)、[新HTTP策略实测](evidence/multi-agent-development-20261114/redirect-protection.json) | 同一供应商的两协议不替代三类原生供应商账号 |
| LP01 OpenAI／Anthropic／Gemini原生 | 配置编译、隔离凭据、固定SDK的实际wire、普通／Schema／错误及完成状态检查；[原生协议](evidence/multi-agent-development-20261114/native-vendor-wire-protocols.json) | 三个远端原生账号同组质量样本仍缺。当前环境未配置相应key，已知pi配置只有DeepSeek；其他私有配置路径尚未提供，不宣称机器上绝不存在这些凭据 |
| LP02 接入与推理解耦 | pi／TraeX／Codex／Claude采集+API处理；API启动没有Codex认证输入，独立主体及权限保持；[真实API启动](evidence/multi-agent-development-20261114/real-provider-startup.json) | 不穷举Agent×Provider；各协议远端门槛仍独立 |
| LP03 保存／next／重启 | 真实Native保存不切换、旧验证失效、next与当前分开、重启摘要确认才active；实际旧库身份／ACL／知识保持；[切换](evidence/multi-agent-development-20261114/real-cross-protocol-switch.json) | 官方处理中的任务并非“停稳快照”，不承诺无损恢复或自动重放 |
| LP02/03 首次Provider配置 | 从空账本运行setup，实际Native调用DeepSeek验证／选next，正常stop/start健康及摘要匹配后首个active；真实首候选摘要故障清理后停止、不回退；[首次配置](evidence/multi-agent-development-20261114/first-provider-initialization.json) | 环境／官方owner预先准备，不是新安装器；真实密钥由受认证API保存，未在Native输入；不替代三类原生账号 |
| LP03 候选失败／清理未知／一次恢复 | 独立空库、真实固定官方服务进程：候选摘要故障→确认退出→旧配置一次；清理未知不启动旧配置；旧配置也失败后停止；[真实进程](evidence/multi-agent-development-20261114/startup-recovery-real-processes.json)；锁／stale验证另有[检查](evidence/multi-agent-development-20261114/startup-checkpoint.json) | 进程故障门禁不等于真实远端模型故障全矩阵，空库连续性标记不冒充已有知识恢复 |
| LP04 错误与隔离 | 401/403/429、超时unknown、截断、Schema拒绝、质量错误、固定模式、无自动换模型；三协议307/308拒绝、缓存重建保护；[探针边界](generation-probe-boundaries-20261114.md)、[重定向](generation-redirect-protection-20261114.md) | wire夹具不替代远端账号；全部宿主处理输出隔离仍按宿主及桥接实际证据，不以单测作全量声明 |
| UI01 Native | 实际1200×820列表、Provider表单、密码不回填、错误／原回执／重开、批量取消、项目checkbox与跨协议重启；[UI记录](native-settings-continuity-20261114.md)、[后续批量](connection-batch-recovery-20261114.md) | 截图在CUA会话中实际观察，部分未导出仓库PNG；原生账号及Cursor路径仍未完整验收 |

## 实施与交付范围

第二批OpenCode当前已完成电脑级prepare／activate／review、实际1.3.5编码与新会话采用、真实API权限阶段、原dataset UUID核对及认证待发worker，不能再按最初“只有全局加载”理解。Native增量／正常日常接入及旧无租约代际共存仍待完成，具体见检查点三十八至四十一。Ollama的配置与真实模型结果见后续检查点；旧qwen3:0.6b质量失败保持，不据CLI安装判定模型可用。

I01基线提交／tag、I02通用连接与迁移、I03官方核心薄适配、I06配置模块、I07启动接线、I09 Native源码已实现，各自证据范围见上表。I04／I05主编码闭环有真实证据。I08中Claude已补限定实链，Cursor未补；I06／I10不能因配置和SDK夹具通过而算远端账号完成，I01–I10完整冻结交付尚未完成。

检查点二十五的[剩余门槛审计](evidence/multi-agent-development-20261114/remaining-gates-checkpoint25.json)确认候选7份源码摘要一致及原日常数据组带认证读取200。当前会话source为vscode，不作Desktop证据；已请求普通Desktop新会话与原生Provider私有配置路径，等待实际输入后继续相应验收及I10日常收口。

MA07／LP05属于原定第二批，[实施入口](multi-agent-second-batch-20261114.md)已启动：本机OpenCode1.3.5的真实两项目全局加载通过、固定官方9项测试通过，但受管权限及完整知识链未实施完成；Ollama CLI已装、服务未启动，Schema／质量待验。[准备基线](evidence/multi-agent-development-20261114/opencode-second-batch-baseline.json)。第一批外部前置保留，第二批不计入首批已交付。旧图谱328字节README及旧helper停止问题不重跑。

OpenCode受管HTTP／outbox覆盖源码已实现，5项Python及10项实际编译Node检查通过；[底层回执](evidence/multi-agent-development-20261114/opencode-managed-core.json)。尚未纳入安装构造／主体／策略／实际宿主，不能将本地403状态机通过算作真实ACL验收或已提供补交入口。

检查点二十八补TS项目控制客户端及原绑定只读核对API，27项Python／18项编译Node检查通过；[控制协议回执](evidence/multi-agent-development-20261114/opencode-project-control.json)。已接实际控制HTTP、official ACL仍为夹具；正式宿主回调／安装代际和真实主体权限尚未接通。

检查点二十九已补设置进入自动读取／重新检测及OpenCode真实检测但不可绑定的卡片；3项Python＋1项组件检查通过，实际Native增量待验。检查点三十补原目标队列及`currentTarget`，本地拒绝与实际官方401／403分开持久化；6项Python／22项编译Node检查通过，[原目标证据](evidence/multi-agent-development-20261114/opencode-original-target.json)。这些仍为正式宿主接线前的原语，不代表OpenCode已经完成绑定或真实采集。

检查点三十一补`ManagedDelivery`，官方TS队列／HTTP与实时原绑定策略接通；6项Python／27项编译Node检查通过，[发送接线证据](evidence/multi-agent-development-20261114/opencode-managed-delivery.json)。控制HTTP实际执行，官方写入／ACL仍为夹具；正式OpenCode回调、激活边界、安装代际和实链待完成。

检查点三十二补固定官方采集回调覆盖及metadata-only启用窗口，QA／trace已接原目标队列；7项Python／28项编译Node通过，[采集回调证据](evidence/multi-agent-development-20261114/opencode-capture-callbacks.json)。宿主消息和官方内容／ACL仍为夹具，配置／召回／lifecycle／处理／显式工具覆盖与安装代际尚未齐备，当前未安装启用，不宣称真实OpenCode采集通过。

检查点三十三补私有安装key／显式本地地址配置及实时common／project召回、注入撤权和处理会话初始化隔离；8项Python／32项编译Node通过，[证据](evidence/multi-agent-development-20261114/opencode-managed-recall.json)。lifecycle／处理／显式工具／操作账本及不可变安装仍待完成，当前组合未安装启用；真实OpenCode与知识内容未验收。

检查点三十四接原目标lifecycle／自动处理／明确remember及metadata操作回执，稳定安装／native namespace与已保存水位；9项Python／38项编译Node通过，[动作回执证据](evidence/multi-agent-development-20261114/opencode-managed-actions.json)。unknown不随时间、新材料或新message ID重投，running不冒充完成。不可变安装／来源绑定、认证管理处置及真实1.3.5宿主仍待完成，未安装启用。

检查点三十五补离线不可变组件／固定manifest prepare／私有全局入口；4项安装检查及正式CLI产物复核通过，[证据](evidence/multi-agent-development-20261114/opencode-immutable-artifact.json)。已保留3671文件、约55.4MB组件，Node入口初始化不等于实际1.3.5宿主；Connections接线及认证处置仍待完成，本机配置未写入，卡片继续开发中。

检查点三十六已接OpenCode电脑级prepare／activate／review及设置回执：11项管理／发现、插件9项Python含39项Node、Go1项通过；[管理证据](evidence/multi-agent-development-20261114/opencode-connection-manager.json)。实际1.3.5两个空目录各得独立绑定，去探针复验通过；MA-OPENCODE-NATIVE-BIND01历史失败2轮保留，[原生证据](evidence/multi-agent-development-20261114/opencode-native-handshake.json)。official身份／ACL／内容仍为夹具，真实编码QA／trace、处理／知识及采用、认证补交和Native增量尚未完成；日常配置未切换。

检查点三十七补齐OpenCode实际编码→官方QA1／trace5→自动生成5份知识→全新会话工具0次采用，独立7项回归通过。MA-OPENCODE-RECALL01历史失败2轮保留；自动召回已改为官方CHUNKS／only_context、项目知识节点集及无session完成缓存。真实权限／待发材料／Native增量仍待验，不能将本样本当完整交付；[证据](evidence/multi-agent-development-20261114/opencode-real-coding.json)。

检查点三十八纠正上轮目标证据：旧官方会话曾绑定同名子组（MA-OPENCODE-TARGET01失败1），已有正确项目Data不等于会话归属正确。当前改用UUID后，新实际Native QA1／trace2及工具0次采用的官方dataset均正确。显式文件＋session正文丢失亦修复（MA-OPENCODE-REMEMBER01失败1保留），真实正文／召回通过；真实API权限六阶段及空索引404隔离已取证。原覆盖恢复、临时文档删除，原同名组和旧会话保留，Native权限UI未由协议检查代替。待发材料及旧unknown目标核对仍未完成，[范围与计数](multi-agent-second-batch-20261114.md)、[证据](evidence/multi-agent-development-20261114/opencode-real-acl.json)。

检查点三十九完成旧unknown的原官方session／dataset／marker字段核对；MA-OPENCODE-UNKNOWN-TARGET01历史失败1保留。实际原服务GET三项及41项Node／8项Python补丁通过，无内容重发或模型调用；专用unknown保持原样。补交／丢弃的管理认证、快照同意和跨进程互斥及Native接线仍未完成，[证据](evidence/multi-agent-development-20261114/opencode-unknown-target.json)。

检查点四十完成待发材料内部原语及发送租约：45项Node／8项Python补丁通过，含真实子进程互斥、精确快照、所选项处理和discard墓碑。没有新增Agent管理工具或激活未鉴权入口，认证worker／Native接线仍未完成，[证据](evidence/multi-agent-development-20261114/opencode-pending-primitives.json)。

检查点四十一已实现管理认证的固定OpenCode worker及Native源码分派，实际原服务8次Go→Node核对／补交／丢弃／拒绝调用通过；合成材料、处理关闭，无新增模型或实窗证据。原策略恢复，独立shadow unknown保留，日常未切换。设置自动检测／重新检测及多选准备／启用继续复用现有入口，实窗增量待验；Ollama与外部门槛保持。[证据](evidence/multi-agent-development-20261114/opencode-pending-management.json)。

检查点四十二已接Ollama本机profile／无个人密钥表单／原生adapter及模型metadata地址隔离。41项Python、10项Go定向检查通过，实际0.34.0缺模型拒绝与进程回收通过。独立Llama 3.2 3B首次加载遇105GiB门禁中断1次；确认退出和空间回升后以同模型／同账本／同样本、8K上下文验证，普通输出通过、JSON对象模式嵌套Schema失败（MA-OLLAMA-LOCAL-SCHEMA01失败1轮），未启用或生成知识。保留模型和失败证据，真实质量及完整官方处理链仍待完成；Native实窗未由组件检查代替。[证据](evidence/multi-agent-development-20261114/ollama-local-provider.json)。

检查点四十三修正OpenCode确认后unknown计数的呈现，2项Go回归通过，实际点击仍待验。安全字段／结构诊断及验证协议20项Python通过；同模型官方adapter原Schema问题累计失败2轮，实际三个响应都回显定义。独立原生SDK Schema实验结构通过但事实比较未通过（独立质量实验失败1次），不把它算作原路径或完整知识处理通过。方法TypeError另记，已知验证进程组及daemon退出，无新模型下载或日常变更。[证据](evidence/multi-agent-development-20261114/ollama-schema-diagnostics.json)。


### 检查点四十四增量

Ollama增加实际Schema随机约束证据，最新同digest完整质量通过才允许next及启动编译。独立Qwen3 4B instruct通过不变固定样本；真实Native共用配置页保存／重开／实际验证／next通过，active仍空。49项Python及12项Go检查通过，另2项断言复验；旧Llama Schema失败2与原生事实失败2保持。正常官方启动、知识／采用及Agent连接设置实窗仍待完成，不能以首次配置页替代。详见[检查点四十四](multi-agent-provider-execution-20261114.md#检查点四十四ollama原生能力与真实native配置流程)及[脱敏证据](evidence/multi-agent-development-20261114/ollama-native-schema-ui.json)。


### 检查点四十五增量

正常官方启动应用Native已验证Ollama候选通过，active／next／applied及健康鉴权一致，无Codex推理桥。真实设置自动检测／重新检测／pi与TraeX多选准备启用通过；独立验收profile，日常配置不变。真实pi编码独立4项测试通过、官方QA1／trace9正确归属，Native收到新会话回执。原Ollama处理6项完成、生成4条知识；新pi会话自动注入、工具0次。

知识质量仍未通过：MA-OLLAMA-CODING-QUALITY01失败1轮，变更记录把旧错误写成结果，与正确规则矛盾，新会话明确指出冲突并拒绝权威当前答案。技术链路通过不能覆盖此质量失败；原数据与全部旧失败计数保留。TraeX本轮仅绑定，未新增宿主调用；OpenCode Native等边界待验。详见[检查点四十五](multi-agent-provider-execution-20261114.md#检查点四十五正常启动与真实native多agent绑定)、[实际证据](evidence/multi-agent-development-20261114/native-agent-binding-ollama-startup.json)。
