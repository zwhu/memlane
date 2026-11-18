# Codex 日常采集缺口排查

## 2026-11-18 12:35 后续保存与界面验收

前述冻结588条已保存；自动任务6497a692-21cb-4805-9e14-53af4f2fe6c3随后累计保存118条新材料，最新状态watching、remaining=0、automatic_enabled=true。两条当前Codex会话buffer均0；旧20条pending_entries规范摘要仍不变。只读回执见`build/diagnostics/backlog-post-ui-20261118.json`及`build/diagnostics/recall-cards-20261118/final-live.json`。保存完成不等于全部知识整理完成；重载前两项SDK整理任务曾在请求错误后等待重试，未据此重发原消息。

为加载召回历史/聚合只读API正常stop旧owner25209：服务均退出，35秒收尾期限仍超时，DEV-STOP01累计第3次失败；随后owner自行完成收尾退出，所有权记录已自行删除，实际取得同库独占锁。此次没有补发SIGINT、没有手工清理所有权记录。原库、端口、配置及existing-codex修订1保持，启动owner73758；UI由既有dev open --watch-ui附着。DEV-STOP01仍待修复，不能把自然完成收尾说成35秒内正常停止。

## 2026-11-18 12:00 会话核对失败隔离（实施中）

用户要求解决11:27恢复任务再次暂停：d935339a-4436-4682-92a2-c9d28ee5089b在冻结阶段依次核对旧会话，首个旧TraeX返回未确认状态而非完整快照，触发backlog_snapshot_unconfirmed；后面的两条当前Codex会话尚未核对，也没有保存。旧停止任务与本次恢复任务是不同事实，不再把当前阻断都归因于旧.stop文件。

本轮范围：管理后台在只读review阶段按会话隔离异常，保留review_status/review_reason及review_failed计数；其余已核对会话继续沿原发送器及原编号保存。快照冻结仍在任何发送之前完成，失败会话不进入save/process；不重置未知条目、水位、来源及权限，不处理未登记历史会话，不删除旧材料。真正发送结果未知／子进程退出不明等原屏障继续保留，不将只读隔离扩大为自动重发。UI补充部分会话未核对提示；重试标签读取真实暂停／检查状态，不能把可重试能力当作正在运行。

验收主流程：旧会话核对失败→正常会话保存→失败项保留未确认；异常快照含非法token／抛出PolicyError不授权发送；明确暂停不自动恢复；原有编号重试／旧unknown隔离保持；加载同一日常服务后经原API恢复当前明确未发队列并核对官方回执和本机余量。复用同库，不建立第二内容库，不调用额外模型来验保存。相关28项Python、全量Go及lint已通过，待日常运行核对。

### 同库加载与真实恢复

当前修复已加载日常服务。切换前completion active_or_waiting=0、官方pending_background_tasks=0、图任务0；原UI watcher25319按身份正常退出，原71515收到dev.py stop后三个服务均退出，但启动器因历史process scanner未在35秒内完成收尾，DEV-STOP01累计第2次失败（保留此前第1次）。随后核对同一内核启动时间、原命令、端口均空闲、三个原服务退出、剩余子进程仅启动器自己的ps扫描；向该已确认owner发送SIGINT，确认退出并取得同库排他锁后归档残留所有权记录。未终止未知进程、未删除运行依赖、未另开库。

新owner25209，原runtime.json、库、端口及existing-codex修订1保持，自动恢复未绕过item-fe的明确暂停。经原管理API新建用户授权恢复操作12d6e4ef-493a-459b-9cc6-2da408d290ca，冻结两条当前Codex会话的原编号消息352+236=588条。首个旧TraeX未确认会话已单独跳过（policy_unconfirmed），后续Codex已开始保存。12:02只读核对本次冻结588条还剩523条，65条已从原队列取出，官方第一会话last_activity_at同步推进；旧20条无凭证记录的pending_entries规范摘要保持不变。后台任务仍running，未把启动成功或中途进度记为全部完成。证据`build/diagnostics/backlog-isolation-live-20261118.json`只含编号及元数据，不含正文。

Native实际核对“自动保存已开启”、两条会话“后台保存任务正在运行”、重试提示“等待重试”及“1个会话的保存状态未能核对，已单独保留”。全量Go test/vet、28项相关Python、lint均通过。恢复脚本首次误用不存在的/backlog/start返回404，确认原操作未写入后，沿用同一operation_id调用原/backlog入口；属于方法错误，没有换编号重发未知写入。

12:12最终冻结范围核对：两批原编号消息分别saved=352/236、remaining=0，本次588条均保存；原ID集合在本机buffer中剩余0。官方capture-status第一会话129轮问答/1313条工具记录，第二会话34轮问答/996条工具记录（含恢复期间新到材料）；旧20条pending_entries摘要不变。后续新到的第二会话51条另计，后台处于后续process阶段，不把新增尾部写成本次588条恢复失败，也不宣称所有新消息已经清空。

### 用户追问自动召回与卡片计数

同日只读对照activity与knowledge-recall.sqlite3：截图11:00、10:08、10:03、08:57等动态对应本机Codex的automatic回执，今天6个事件、45条次命中、23个不同document_id。project_plugin_policy.merge_block只对最终返回上下文块中仍存在的记忆记录automatic，不证明Agent回答实际采用了这些材料。高亮文档594581d8-867b-45ed-8cc6-c59c3a629153在官方目录中确认为memlane-direct-contract-20261117，其recall_hit记录为0，卡片0与该记录一致；不是所有卡片都应因一次召回增加。截图另一卡已显示3次。当前动态只写“全局/已完成”且无命中条目关联，难与卡片对应，是展示缺口。另查到知识卡统计未读时回退显示0，不能将这种回退当作所有卡片真实0；本轮仅核对高亮条目，不在问答中偷偷改计数或触发召回。

状态：当前插件信任阻断已解除，接入检测与许可引导已实现；新真实会话保存、历史缺口恢复及旧20条未知工具记录尚未闭环。用户指出11月17日及18日会话缺失。

只读实时核对：同一日常官方服务 sessions 总数51，按真实 Codex UUID 会话键识别28条（排除 codex_traex 前缀）。最后活动为 UTC 2026-11-16T15:53:53，即北京时间11月16日23:53；17日、18日没有官方 Codex 更新。本机17日有15个会话文件、18日1个，文件数量不能直接作为待上传消息数量。

当前受管 marketplace 为 a47cbd4ac0fbfe221122，状态 configured_pending_new_session，新绑定0。当前 receipt 来自 authenticated_host_resolve，artifact_verified=true 但 environment_verified=false，不足以证明真实会话 hook 或保存链路已运行。

插件日志最后更新北京时间17日18:54，为旧会话的 sync_session_key、sync_payload；counter 和 save_counter 停在16日23:53。不能将日志最后更新时间作为保存成功时间。

配置中的 hook 信任记录只有 cognee-local 和旧 marketplace f36cad920703147fb9d4，新 marketplace 未发现对应信任记录；这是候选原因，尚未确认宿主实际跳过原因。宿主当前有其他 hook started/completed 事件，不代表 Cognee hook 执行。

保留数据与未知状态，没有重装、重启、授信、补采、重发或改已连接状态。后续先确认新插件 hook 是否加载及宿主信任状态，再核对采集与发送。历史缺口恢复须另核对范围和去重。

官方依据：[插件打包规则](https://developers.openai.com/plugins/build/plugins)、[Hooks](https://learn.chatgpt.com/docs/hooks) 明确安装或启用插件不会自动信任其 hook；新定义或脚本变化需要重新审阅，未信任时跳过。宿主入口为设置 → Hooks，CLI 为 /hooks。本机缺少新 marketplace 信任记录符合该阻断条件，但尚未取得宿主逐项 hook 状态，不能将其作为已完成端到端修复。

尝试通过已运行 ChatGPT/Codex 宿主界面只读核查，CUA 工具拒绝访问 com.openai.codex。未绕过访问限制，未代写 trusted_hash 或使用信任绕过参数。接下来需用户在宿主审阅该插件采集脚本，再核验新会话从采集到同库保存；已有缺口不因授信自动宣布恢复。

## 用户授权信任后的处理

用户明确要求“你给我信任吧”。通过当前官方 Codex 可执行文件的短时 stdio app-server 调用 `hooks/list`，真实返回当前受管插件9项 `enabled=true, trustStatus=untrusted`，无加载错误。由此补齐此前只依据配置推断的阻断证据。

校验当前 cache 与已登记固定 manifest 的全部文件哈希；按官方返回的当前定义和内容哈希，使用官方 `config/batchWrite` 并带用户配置版本并发校验，只为该插件9项记录信任。写入前保存0600私有配置备份。随后官方 `hooks/list` 9项全部返回 `trusted`。未使用绕过信任参数，没有信任其他插件、重启服务、启动模型或重放会话。私有脚本和回执在 `build/diagnostics/trust-codex-plugin-20261118.py`、`codex-hook-trust-result-20261118.json`。

完成边界：采集脚本信任已完成，新的真实消息保存和历史缺口恢复仍待验证，不以信任成功代替自动保存成功。

## 接入引导设计（待实施）

不能要求用户理解 hook/hash，也不能只提示重新打开会话。代码通过宿主官方状态区分“需要允许自动保存”“用户暂不开启”“正在验证”“自动保存正常”；只收到回执或已安装插件不代表保存正常。提供当前宿主审阅入口和可操作步骤；没有受支持的直达入口时明确展示实际路径，不伪造可跳转能力。

待信任时文案“还差一步：允许自动保存会话”，说明允许后仅授权的项目会话按现有规则保存到本机记忆库，已有权限规则继续适用。操作“去 Codex 允许”“暂不开启”。用户拒绝后显示“自动保存未开启”，保留一次性开启入口，不反复弹出、不自动修改信任、不把其当作系统故障；已有记录可读，新会话不自动保存。只有同一新消息完成实际保存后再显示“自动保存正常”。拒绝与未知应由代码及用户明确动作记录，不能从没有信任记录推断用户已经拒绝。

## 跨 Agent 同类风险

用户指出其他 Agent 也可能遇到类似阻断。现有 `Connections.view()` 汇总安装状态、激活后新会话绑定及环境回执，不能证明消息已成功保存，也未统一检查每种宿主的执行许可。宿主的具体信任机制不同，不能把 Codex 的9项 hook 规则机械用于其他 Agent。

本次实时读取：pi、TraeX 各有2个激活后会话绑定，environment_verified=true；只能确认相应接入证据，不代表逐消息保存链已完成。Claude Code、Cursor、OpenCode 当前 not_connected，没有证据称其已经出现与 Codex 相同的故障。统一产品状态需覆盖配置完成、需要用户许可、等待真实消息、保存正常及实际故障；宿主没有许可状态接口时应标记无法检测，不能猜为已许可。安装、执行许可、消息保存的证据分别核对。

同轮图谱UI修订：删除项目图谱分段控件固定280px宽度，按三个选项的实际宽度排版，去掉末尾空白。图谱定向测试2项通过，实际Native已确认，无设置值变更。

## 统一实施范围与连续用例

用户授权只统一 Codex、pi、TraeX。影响管理接口的接入投影、Codex官方许可探针和已审阅授权、官方会话的只读保存元数据投影、Native接入列表及既有操作回执恢复；不改其他Agent、项目权限、会话采集器或历史未知消息，不自动安装、补采或重放。沿用当前日常库，不做release。

先验证最关键的边界：官方 hooks/list 可在不启动会话或模型时读取精确未信任/已修改/禁用状态；保存验证必须来自当前安装身份、同一会话及项目的官方持久记录，不能由握手、旧成功或HTTP200空响应替代。后台探针异步、限时并合并刷新，不在UI线程调用，不新增共享状态锁。配置/目录/插件变更立即使旧探针失效。

连续用例：已配置但未许可→审阅→明确允许→等待新消息→官方保存证据→自动保存正常；暂不开启→刷新/重启仍保留→再次开启；授信期间配置变化拒绝旧审阅；请求结果未知保留原操作、只核对不重投；pi/TraeX未收到执行证据时提供对应步骤，真实回执仍不足以标保存正常；空记录、旧记录、错误身份/项目、读失败、权限拒绝不能冒充成功；只读检测不写内容、不触发模型；其他Agent行为保持。相关单测/HTTP夹具、真实只读探针及Native窗口分别留证，既有锁测试EAGAIN累计至少2次不重置。

### 源码与协议检查

已实现三种Agent的capture投影、官方授权边界内的仅元数据capture-status端点、Codex当前脚本指纹审阅与版本保护写入、持久暂缓选择、原未知许可操作只读核对、Native明确状态及步骤。保存验证核对当前安装代次、会话绑定、实际用户及项目、真实问答时间和数量；禁采/权限待确认不能沿用旧绿色状态；读取失败保留上次保存时间但撤下正常状态。原有安装及其他Agent路径保留。

全量Go test/vet通过；新增交互的race检查通过；相关Python90项通过。真实Codex许可闭环以临时配置目录复用既有固定插件，不启动会话/模型：required→granted→重开仍granted，9项脚本，日常config校验值不变。首次该探针因macOS `/var` 与 `/private/var` 规范路径差异被安全拒绝，记录1次，路径标准化后通过；未放宽产物哈希校验。SDK真实get_session返回条目对象而非字典，新增针对对象的失败断言复现后修正，7项端点检查通过；旧消息时间不以会话活动时间替代。

### 旧黄点只读核对

原Codex会话01a1d40f…c482b597本机buffer中确有20条未知项，全部type=trace。条目没有qa_id、trace_id或idempotency_key，只有原事件时间/工具字段及未知标记。留存规范JSON校验值b9b8c61883c02b7eecc4c7135543dcda79b0d94a7f7946f6df53ee83daf85885；没有清除、归档或重发。此类历史恢复与新消息的唯一编号/服务端幂等协议属于后续可靠性整改，本次状态/许可检查不能被描述为已经解决旧20条。

### 开发后台重载观察

为加载本次官方只读端点和管理接口，核对completion active_or_waiting=0、补存stopped/paused、工具图任务无活动、原验证未知项所属进程组已退出，停止本轮UI watcher并调用现有dev.py stop。三个服务子进程退出，但旧启动器14559在历史后代核对中超过35秒，stop返回未确认，留原所有权记录；当前不启动第二实例、不删除锁或登记、不强杀未知进程。记为DEV-STOP01第1次，继续只读核实原启动器收尾。

随后核实旧启动器14559的内核启动时间、命令和配置身份，三个服务进程组及端口已退出，仅旧启动器持有该库service-owner锁；其持续扫描历史后代。只终止该已确认的旧owner，取得同库独占锁后归档旧登记（0600）并清理失效登记，再启动同一runtime.json。新owner35095，官方/管理/桥接端口及库路径保持不变。未终止未知进程或改写记忆。DEV-STOP01仍记1次；历史后代扫描是已观察卡点，尚未完成启动器根因修复。

### 日常库与Native最终观察

新接口实时返回Codex waiting_session / permission=granted，pi及TraeX healthy，均核对当前安装代次的官方问答保存证据。Native实际列表与接口一致；Codex尚无新的真实宿主会话保存证据，不能宣布自动采集恢复。实际窗口确认添加配置居中弹框、删除确认包含具体模型、取消后原两条配置仍在；未保存、验证、切换或删除用户配置。许可面板首次合成图发现右侧越界（视觉1次），改为外层padding后定向检查及重新看图通过；没有修改日常许可来制造演示。

全量Go test/vet、定向race及相关Python90项检查通过，追加删除HTTP保护后generation_config共11项通过。许可面板padding修正后的定向检查通过。证据位于build/diagnostics/capture-live-status-20261118.json、capture-permission-official-host-20261118.json及capture-provider-ui-20261118/；这些是本机私有诊断，不提交实际配置或会话内容。

### 黄点的恢复边界与后续方案

只读遍历原官方会话全部38页、757条工具记录，原20条中有6条找到规范化时间及完整工具字段一致的候选。原请求没有可核对的唯一回执，因此6条不能自动确认为同一次保存；其他14条未匹配也不能据此断言原请求未保存。原buffer数量及规范JSON校验值与重载前一致，auto_resolved=0，未归档、删除或重发。元数据审计回执：build/diagnostics/legacy-20-trace-readonly-audit-20261118.json。

要消除长期未知，后续可靠性整改必须同时覆盖：发送前持久化稳定消息编号、服务端按同一编号幂等保存并可查询、丢失回执后按原编号核对或重试、明确失败自动退避重试、每条记录有可追踪的最终结果。此协议本轮未实现，不能仅改变文案、超时隐藏或重启计时来宣布修复。

历史20条不能因为补了新协议而自动获得原请求编号。须另行实现有证据的历史核对/补存方案，并记录重复风险与最终处置；无唯一证据时继续保留异常，不把“是否已保存”交给用户猜测。黄点应在系统确认保存成功或明确完成恢复后解除；用户主动暂不开启自动保存属于选择，独立显示而不伪装成保存故障。当前黄点仍可能持续，是未完成的恢复能力，不能对用户声称“系统正在自动恢复”。


## 继续推进：编号保存与自动恢复

目标与验收：只覆盖 Codex/pi/TraeX 新采集消息。首发前持久保存编号，丢回信、并发请求、进程/数据库重开均沿原编号核对，不重复追加官方QA/trace；确认原凭证后才消费buffer。旧无编号未知项不补造编号、不相似度确认、不重放，不阻止其他新编号消息保存。显式文档remember及整理提交不纳入此幂等协议。

影响：服务固定ec6f408源码不动，沿lesson_provenance派生组件精确修改SessionManager编号及SqlCacheAdapter原插入事务；cache_kv仅保留编号/范围/正文摘要/官方entry_id，无第二正文队列，无新增UI锁。凭证占位和原QA/trace插入同事务提交或回滚。服务未加载对应补丁时在写前拒绝新协议。GET按本Agent与项目读权限，PUT沿原官方写权限及会话绑定校验；撤权后不能借旧凭证访问。凭证证明原写入成功，不表示缓存正文永不过期。

三种host的prepare共享project_capture_patch/plugin_capture，摘要进入原安装代次及文件manifest，不原地改旧cache。新编号只PUT新端点，不回落到非幂等旧端点。正常hooks复用原buffer/锁/发送lease，跳过无编号未知项而保留它们。后台沿原操作ID，暂时错误按2秒至300秒退避；明确协议拒绝停止重试，用户暂停持久保留。只读检查可GET同一编号/范围/摘要凭证后消费该项。Native区分可按编号重试与旧数据缺凭证，不能给新条目显示“缺唯一凭证、无法自动恢复”。

连续场景：并发同编号仅1条；相同编号改正文409且不覆盖；本体插入失败无凭证；本体已提交而回信丢失，buffer保持原编号，经退避重试返回同凭证后消费；重开仍可核对；旧未知与新编号混排时旧项不变、新项独立完成；检查只GET原编号；401/403、用户暂停和协议冲突不自动继续发；未加载服务补丁不能先按旧接口保存。

检查证据：59项相关Python检查通过；追加后台退避/持久暂停、只读凭证确认、未加载补丁保护及明确拒绝检查后，17项定向检查通过。固定SDK+临时原生SQLite通过ASGI接通三个真实prepare产物，模拟提交后丢回信，同编号重试只增加1条、旧无编号项逐字段不变。协议测试中的权限入口、模型和向量调用为夹具，不替代日常权限或真实宿主新会话验收。完整Go test/vet通过，新增状态分类定向通过。日志：build/diagnostics/capture-retry-tests-20261118.log、capture-go-tests-20261118.log、capture-go-vet-20261118.log。

CAPTURE-ANCHOR01：首轮派生组件检查安全拒绝cache transaction anchor（同句官方锁调用在多个方法存在），累计1次。改为AST定位create_qa_entry/append_agent_trace_step，再在方法内精确匹配后通过；未放宽原文件校验。DEV-STOP01与旧EAGAIN等计数保留。

启用顺序：核对owner及在途模型/保存/图任务→重载同库服务→检查新端点/派生组件→准备并审阅三种插件更新→正常激活并由真实新会话验证。Codex脚本变更仍需宿主许可审阅，不自动继承旧脚本哈希信任。旧20条在重载/更新前后核对原校验值。本段是继续推进后的新实现，覆盖上文当时“协议未实现”的状态；日常启用结果另续。


### 日常启用结果（02:02后）

旧owner35095在completion active_or_waiting=0、补存任务均停止/终态、全部验证所属进程组已退出、图任务终态且另一会话确认无依赖后，经dev.py stop正常退出、释放同库；没有强杀。新owner71515，官方71521、control71886、bridge71519，同一runtime.json、库和端口，后台保留运行。此正常收尾不重置DEV-STOP01历史1次失败，也不表示该历史卡点根因已修。

新服务加载精确派生组件；新capture路由未认证401，三种真实Agent在各自已有会话/项目范围内只读查询不存在的新编号返回404，没有写验收消息或调用模型。三个真实安装经管理接口prepare→逐文件manifest核验→activate成功：Codex aeb3e9ff…9ffcb7，pi aac906d4…7739，TraeX 7393cefb…64e19f。Codex配置前后结构核对仅对应marketplace/插件启停变化，其他账号、项目和插件保留。

沿用用户此前明确“你给我信任吧”的授权，人工核对本次固定新增编号发送代码后，仅通过官方许可接口记录当前新版Memlane 9项脚本的当前哈希；没有继承旧哈希或信任其他插件。当前Codex permission=granted、waiting_session，pi/TraeX waiting_session；本轮不启动另一个编码Agent制造成功记录，真实新会话保存仍待正常使用验证。需要重新打开相应Agent并新建会话加载新版。

旧20条未知项数量和规范校验值b9b8c61883c02b7eecc4c7135543dcda79b0d94a7f7946f6df53ee83daf85885与重载/升级前一致；没有补造编号、删除、归档或重发，历史异常尚未恢复。新旧状态同框的MyGo合成图已实际打开检查，标题/分类/按钮正常；本轮不抢占另一会话UTM所用外接屏，未把合成图当新宿主真实保存或Native实窗验收。

私有操作回执：build/diagnostics/capture-live-upgrade-20261118.json、capture-live-final-20261118.json；源码断言与旧配置备份保留0600，不提交账户配置。新增状态合成图：build/diagnostics/capture-provider-ui-20261118/fixture-capture-retry-and-legacy.png。没有构建release包或恢复历史全量Goal。

## 09:01 用户复核：今天仍无会话（CAPTURE-DATASET01）

目标仍是Codex真实会话自动保存到同一日常库。两个当前活跃host在官方sessions详情均404，本机编号队列分别1090/725项，共1815；采集日志持续更新、drained=0。材料仍在本机buffer，未将握手或授信当作保存成功。此具体UUID解析缺陷本轮验收失败1次；旧授信阻断与无编号20项保持原证据和计数，同轮三个host断言不重复计次。

实际插件capture/write权限及会话身份核对均通过，wire在网络请求前抛capture_dataset_id_required：managed route返回UUID，但官方dataset_id_for仅按名字匹配launch record，名字/UUID不同即空。此前测试把该函数stub为UUID，掩盖真实调用契约。去除stub后三种真实prepare产物同轮全部失败，修复为先用官方_dataset_access.dataset_id解析UUID，只有非UUID名称才走原resolver；显式ID仍须有效UUID。修复后2项集成检查通过（24.137秒），其中包含三个实际prepare产物及真实SQLite，权限/模型仍为夹具。

影响与连续验收按plan-change-impact及verify-continuous-scenarios简化执行：仅改三host共享sender和回归夹具，不改ACL、项目绑定、服务/库、原消息编号及正文。三host真实resolver与丢回信幂等测试→派生产物prepare/核验/激活→审阅当前Codex脚本授权→原编号积压经正常sender保存→同库API及官方记录核对。保留无编号未知旧20项，不以重新采集transcript补齐旧缺口；新消息与历史完整性分别报告。

首次unittest未设PYTHONPATH导致ModuleNotFoundError，为方法错误1次，未计产品缺陷；正确命令为task.py -- env PYTHONPATH=engine/python .local/cognee-python/bin/python -m unittest test_capture_receipts。
