# 第二批 OpenCode／Ollama 实施与验证

范围来自[定稿方案](../multi-agent-provider-technical-plan-20261114.md)的MA07／LP05，承接第一批已实现的连接、独立主体、控制协议、生成配置及Native配置页面。首批Desktop／原生账号待补，完整目标保持active；用户明确未安装Agent可暂缓验证、Claude Code不再追加测试，继续其他工作。

## 已核实前置

- OpenCode CLI实际1.3.5已安装。独立HOME／XDG配置中一份全局插件，经真实headless宿主在两个空项目各初始化一次，项目目录保持为空；仅创建空session，无模型请求，服务正常结束。[基线证据](evidence/multi-agent-development-20261114/opencode-second-batch-baseline.json)。
- 固定官方集成归档SHA核对，提取13份原始文件，原文件摘要保持；按原lock安装30个依赖并编译，官方9项测试通过。宿主隐式SDK1.3.5，官方固定测试SDK1.18.29，版本不同，尚不能说明完整宿主兼容或Memlane权限通过。
- [当前官方全局插件说明](https://opencode.ai/docs/plugins/)与安装版CLI存在命令／配置差异；已通过实际全局目录加载入口消除首个未知，不将最新文档接口直接套到旧CLI。
- Ollama CLI已安装，默认服务不可连接，未启动。现有模型manifest仅qwen3:0.6b，旧事实质量失败保留；尚未重跑、下载模型或执行推理。后续模型选择前核对既有具体缺陷与失败计数。

## 影响与实施顺序

1. 受管官方产物：从同一固定归档构造不可变OpenCode代际，保留官方QA／trace／召回／outbox机制与原始manifest。加入私有安装绑定、凭据文件及严格补丁摘要，安装全局薄入口，不写项目配置、不读旧会话。
2. 控制与权限：复用现有独立官方主体／project_control，按实际ctx.directory和native session动态绑定项目；每次写入、召回注入及处理前核对有效策略，通用知识只读。连接不依赖宿主模型登录，明确工具和自动行为分别遵循现有语义。
3. 采集与恢复：补官方读取整段session的启用边界，避免历史回扫；401／403进入blocked，权限恢复不自动重投，明确补交才发送；unknown只按原id核对，不能通过新id或自动重试制造重复。禁写／禁处理不保留可回扫历史；处理会话保持采集隔离。
4. 实际宿主：先编译与定向状态／协议检查，再以真实OpenCode完成编码→官方原始记录→知识→新会话采用，并核对维护／只读／禁止、不同项目／通用知识、重载和其他插件保持。全局加载基线及官方单测不替代这些门槛。
5. Ollama配置：接入既有保存／验证／next／active与启动恢复，不回退到Codex或远端Provider；本机endpoint／认证语义单独核实。之后以合适且未触发停止规则的模型做实际SDK普通／Schema／事实样本及知识链路；不重复旧停止样本。
6. Native增量与交付：只接线新增类型、配置字段及真实状态，避开其他会话未提交UI。按最终候选做必要检查，遵守Cursor／Claude测试调整；继续dev、不构建release。

## 当前缺口与资源

最初基线中，官方OpenCode使用插件级dataset、拒绝后自动重投、整段session消息读取；后续受管覆盖已处理这些问题。当前以检查点三十九为准：管理侧准备／启用、原生绑定与知识采用样本、真实API权限阶段及旧unknown目标核对通过；同名组错误及unknown误确认均另记并修复，认证待发材料处置与Native增量仍待完成。以下早期检查点保留当时事实，不覆盖后续源码与证据。

## 受管HTTP／outbox底层适配

`engine/python/opencode_plugin_patch.py`新增固定TS源的严格覆盖，尚未接入生产构造器／全局入口。派生client要求独立安装key，禁止owner登录和cloud模式，只发固定X-Api-Key并移除调用方Authorization；health复用同一规则，307／308不跟随，超时与401／403不自动重试，错误只报状态不回显响应正文。

派生outbox将明确401／403持久化为blocked，自动pump及重启均跳过，但允许后续新合法项发送。`retryBlocked(ids)`仅释放明确选择的blocked ID，整组预检；unknown或不存在ID不改变队列，正在flush时拒绝操作。此函数是底层原语，后续管理入口仍必须先核对当前权限并登记原操作，不能直接暴露为无鉴权重投。unknown继续用官方原id核对，无额外发送；隐私清洗、身份和路径过滤保持。

从固定归档提取实际TS、严格覆盖后用TypeScript编译，再运行真实Node文件队列／loopback HTTP测试。原版outbox有3个新契约用例失败、原版HTTP有4个失败；修复后5项Python检查与10项编译后Node用例通过（2.086秒），覆盖两类重启／拒绝、混合选择、并发flush、307／308读写、认证、超时和错误正文。[源码摘要与脱敏回执](evidence/multi-agent-development-20261114/opencode-managed-core.json)。这些是受管契约的本地红绿反馈，不是7轮宿主验收失败；尚无真实OpenCode／Cognee权限通过声明。

检查入口：`python3 scripts/task.py -- env PYTHONPATH=engine/python MEMLANE_OPENCODE_TEST_NODE_MODULES=/绝对路径/固定npm-ci依赖/node_modules .local/cognee-python/bin/python -m unittest test_opencode_plugin_patch`。未提供组件依赖变量时，两个编译执行检查会明确skip，不能据此判定通过；本轮提供了固定lock安装的依赖，未skip。没有跑Cursor／Claude或全量宿主安装器测试。

下一步将覆盖摘要、固定依赖及license纳入不可变安装产物，接入稳定主体／动态项目解析／有效权限，完成激活水位和真正的受认证补交入口，再进入真实编码与新会话采用。当前全局探针仅证明加载机制，不含这些受管能力。

## 项目控制客户端与原绑定核对

新增`managed-control.ts`，复用现有project_control，单独快照安装选项，只从私有安装控制token文件读取凭据。安装／native键哈希为稳定主键；元数据仅存原workspace、project／dataset ID与绑定状态，不存问答、工具正文或owner凭据。每次read／capture／process／write／delete均读取实时policy，项目和dataset身份漂移、非法权限、common目标异常、unconfirmed均拒绝；项目无读取权限时仍可按独立common只读策略召回。

首次解析前原子保存unknown。新增`GET /v1/project/binding?host_session_key=...`按调用主体核对原映射，缺失时404且不发现／创建项目。原解析响应丢失后，客户端及重启客户端仅GET核对，不重发POST；不存在的绑定持续unknown。查询沿用有效ACL核对，允许撤下失效策略，不修补grant；管理token和其他主体不能借此读原会话绑定。控制端点仅固定loopback HTTP，拒绝重定向、非私有／symlink凭据及超过64KiB响应。

strict TypeScript编译后，Node客户端连接实际Python project_control HTTP服务，官方数据／ACL由合成夹具提供。覆盖两项目、原会话固定／重启、实时maintain→read→none、common独立、自动／明确行为、已提交／未提交的丢响应、选项快照及协议异常。27项Python定向检查通过（4.591秒），包含控制8项＋HTTP6项＋outbox4项编译后Node用例；[脱敏回执](evidence/multi-agent-development-20261114/opencode-project-control.json)。这不代表真实Cognee主体或OpenCode宿主权限已验收。

下一步构造安装代际并接官方回调：待发项须携带可核对的原项目／dataset绑定元数据，发送／补交按原目标查实时策略；不能用新cwd或全局固定dataset改写旧材料归属。本地策略拒绝须单独标记，不能伪称官方HTTP403；unknown提交仍只核对。注入前重新查权限并撤旧context，激活前历史保持水位。原控制服务需正常加载新增只读路由后再启用对应代际，旧服务未提供该路由时保持未知，不回退。

临时根`.local/multi-agent-opencode-20261114`仅归本任务；13份官方源、两个SDK依赖集与小型回执保留供后续补丁／兼容检查，非日常安装。全局探针的原生服务已退出；Ollama未启动。固定归档、原库、旧宿主缓存和原日常认证／配置保持；至少100GiB与105GiB运行门禁继续生效。

## 原目标队列与拒绝来源（检查点三十）

受管官方outbox的enqueue新增可选原目标metadata，严格校验native／project／dataset后只保存这三个字段的快照，避免携带额外凭据。原目标在caller修改、重启和明确补交后保持。兼容原有底层无target用例不等于允许正式受管宿主发送无绑定材料；正式回调仍须只使用有原目标的项。

控制客户端新增`currentTarget`，先核对本机已确认原映射与队列目标一致，再GET有效policy；未绑定native／项目或dataset不一致直接拒绝，不调用resolve、不创建metadata、不从当前cwd登记新项目。该方法只返回事实，后续发送入口必须据此检查capture／write等权限，不能把返回policy等同许可。

新增`LocalPolicyDenied`供接线后在实际HTTP发送前明确拒绝。outbox将其保存为`local_policy_denied`，清除uncertain并blocked；实际401／403记录`official_auth_denied`。二者跨重启均不自动重投，明确释放仍按原ID和原目标；底层retry函数仍不能替代管理鉴权、实时权限和操作账本。

最终6项Python检查通过（3.040秒），包含22项实际编译Node用例：HTTP6／outbox7／control9。[本轮证据](evidence/multi-agent-development-20261114/opencode-original-target.json)。首次两个反馈为检查方法随新增声明和用例数未更新，纠正后全通过，不计真实宿主产品失败轮次。真实控制HTTP中的official ACL仍为夹具；尚未把以上原语接到宿主回调，不声明真实OpenCode链路通过。旧源码证据对应其原提交，当前增量摘要另列，不覆盖旧记录。

## 发送适配接线（检查点三十一）

新增`ManagedDelivery`把固定官方HTTP／outbox接到实际project_control客户端。入队前查write与can_capture，保存原native／project／dataset；发送前`currentTarget`核对原绑定与最新采集权限，再用官方client发送至该policy的数据组。未知结果仅在有read权限时查原session标记，撤权不查询、不清除未知；恢复只读后可核对原提交，不会再次发送。

官方outbox覆盖增加可选异步authorize回调，放在标记uncertain之前。控制不可达或原目标不明时队列保持原样，不能冒充未知写入；明确拒绝的新项转blocked并允许后续合法新项发送，原unknown的拒绝保持原状态。异步核对返回后在短锁内再次核对原项快照，另一个进程已领取／完成时不重复发送。两份同namespace队列并发检查实际只发送一次。

最终6项Python／27项编译Node检查通过，3.396秒，无skip；[证据与摘要](evidence/multi-agent-development-20261114/opencode-managed-delivery.json)。新增链路执行实际Python控制HTTP、固定官方TS队列／client和loopback官方端点夹具，覆盖拒绝恢复、显式底层释放、响应丢失、切换cwd／重启后的原标记核对、原目标未确认及并发领取。实际Cognee数据／ACL、正式宿主回调仍未证明；管理补交尚未接鉴权／账本，测试中显式调用底层释放只验证原语。

下一步以此适配替换官方插件pump／enqueue，并只从本次启用后实际观察到的message metadata选择单条消息读取，去掉全session历史扫描；随后连接安装代际、主体及Native卡片。处理／召回、隔离、取消及真实宿主兼容仍须按原方案验收，不从当前低层检查推定完成。

## 官方采集回调与消息边界（检查点三十二）

新增严格`managed_capture_callbacks`覆盖，把固定官方插件pump／QA／trace入队接到`ManagedDelivery`；原归档不修改。新增metadata-only `CaptureWindow`：只承认本次插件初始化后观察到、时间不早于初始化且当时允许采集的user ID；完成的assistant metadata须关联该user。按候选ID读取单条assistant及parent，读取前逐次检查采集权限，核对返回session／消息／parent／完成事实后沿官方文本过滤和清洗入队；不调用session.messages。

禁采prompt不因恢复权限变为可采；撤权时清除该session候选并将此前prompt标记为不可采，晚到完成事件不补采。已经处理的完成ID去重；重启window为空，不从历史构造候选。每session最多保留64个turn／candidate，完成ID超过4096时停用原window、等待新prompt；不写正文或新建内容索引。

实际执行固定SDK1.18.29编译后的派生官方回调，连接Python控制HTTP及loopback官方端点夹具：旧时间消息／禁采恢复／已观察后撤权／重复完成不读正文；新QA读取恰好assistant和parent两条，正确写入原dataset；新trace正常写入并清洗敏感内容，`.env`工具输出不入队。7项Python及28项编译Node通过（3.790秒，无skip），[证据](evidence/multi-agent-development-20261114/opencode-capture-callbacks.json)。宿主SDK调用是夹具，不能证明本机1.3.5兼容。

此函数明确只负责采集覆盖，尚未由正式安装器启用；原插件resolveConfig仍有ambient选项、recall／lifecycle／improve／显式工具仍须完整策略覆盖，sid／队列namespace及私有安装绑定也需收敛。不能直接把当前组合安装到日常或真实验收环境。下一步完成这些剩余覆盖和独立操作账本，再构造不可变代际及真实宿主验收；旧配置、正文、原主体与待发材料保持。

## 配置隔离与召回撤权（检查点三十三）

`managed_config_recall`覆盖在采集覆盖之后应用。新增`managed-config.ts`只使用明确loopback服务地址与canonical／0600安装key文件，检查私有state目录；不继承Cognee环境地址、key、mode、dataset，不接受cloud／owner登录和额外readScopes。处理会话标记为1时初始化直接返回空hooks，不读取凭据或创建后台采集任务。安装key路径的来源仍待不可变安装绑定固定，当前私有文件校验不替代来源核对。

`ManagedRecall`保留官方client的recall协议，按有效policy的common／project ID查询，不遍历datasets；项目权限和通用只读独立，common不挂项目session。候选片段在系统注入前再次按当前policy过滤，已撤下片段或control未知后丢弃，不因恢复权限自动复现。新prompt明确重查后才能更新。外层上下文标记包含安装ID，system／compacting先移除本安装旧块再加入有效块，保留其他插件正文。异步policy结果若属于旧片段，不得覆盖新turn或clear后的状态。

8项Python／32项编译Node检查通过（4.145秒，无skip），[证据](evidence/multi-agent-development-20261114/opencode-managed-recall.json)。实际控制HTTP和固定SDK派生hook执行，官方recall与宿主消息为夹具；验证环境覆盖、私有／symlink凭据拒绝、精确数据组、撤权／恢复／control不可达、其他插件保留、压缩与处理会话隔离，以及迟到回调。未启用真实宿主、模型或日常服务。

剩余lifecycle／improve／显式remember仍有旧数据组和操作语义，sid／队列namespace与安装来源也未收敛，当前组合仍不可直接安装启用。下一步完成这些策略覆盖与持久操作回执，再构造不可变代际和受认证补交入口；之后做本机1.3.5兼容、真实采集／知识／新会话采用。第二批Ollama与首批外部前置均保持原范围。

## 变更回执、处理水位与稳定身份（检查点三十四）

新增`ManagedActions`及严格action覆盖，lifecycle／remember／improve均沿原绑定查实时policy，用原dataset／session。登记要求write与采集许可，明确remember只要求write，不因自动采集或自动处理关闭而禁用；自动improve要求write与auto_process。固定SDK的remember必须实际返回dataset ID，缺失／漂移保留unknown，不以请求参数补出成功。

每个逻辑变更先在私有目录保存metadata-only unknown回执，再发HTTP。仅存原native／project／dataset／session、摘要、代次和结果分类，不存fact正文或key。短文件锁不跨网络；另一个实例领取同一动作时只见原unknown／accepted，不再发送。显式remember按native＋fact摘要固定原动作，换host message ID也不绕过unknown。accepted后新心跳／新已保存材料可产生下一代，原终态回执另存；unknown阻止后续代次，明确拒绝的同一指纹不自动再发。生命周期同进程登记与关闭串行，404不支持独立记录。

outbox已确认seen项同时保存原target元数据，`completedTarget`只在本native无待发项时生成稳定已保存ID水位，其他native待发不阻塞。idle按该水位调用处理，同一水位只提交一次；未知处理即使有新水位也不自动重提。固定官方返回running只记“已接收／running”，不当作已产出知识；其后处理终态仍待官方任务核对。受管sid及队列namespace改为安装主体／native，脱离cwd、key和静态dataset，所有旧目录及材料保持，未执行迁移。

最终9项Python／38项编译Node通过（4.743秒，无skip），[证据](evidence/multi-agent-development-20261114/opencode-managed-actions.json)。覆盖真实控制HTTP与loopback官方请求、同内容并发两实例、三类丢响应重启、换message ID、处理新水位阻断、回执无正文、权限拒绝、明确／自动行为及官方hook连续流程。派生插件已无静态dataset直接变更／ambient Cognee字段／整段消息列表；仍为固定SDK夹具，未调用模型或实际宿主。

下一步构造不可变安装与固定依赖／license清单、配置和独立主体绑定，再接管理侧认证核对／补交／丢弃和实际1.3.5兼容。原unknown尚不具备自动核对终态依据时继续保留，不通过新动作规避；当前组合尚未安装启用，真实知识与完整Goal未交付。

## 离线不可变产物与绑定入口（检查点三十五）

新增`opencode_installation.py`与离线构造入口。从固定官方归档校验SHA256，再按原package-lock逐个校验本机缓存tarball的SHA512，拒绝链接／越界／重复路径；按当前平台排除5个不兼容的optional包。临时目录严格编译受管插件，保留原13份源码和官方LICENSE、依赖原license文件及清单；运行产物排除TypeScript／types等dev依赖、未受管CLI及安装脚本。没有网络下载或npm脚本执行。

4项安装检查通过（43.852秒，无skip）。实际Node加载生成入口并得到派生hooks；key变更、额外未登记文件、错误固定manifest摘要均拒绝。prepare必须接收控制侧预先固定的artifact SHA，校验整个tree及上游commit／归档来源；私有binding只存key路径与摘要、独立主体／服务／控制地址，0600且相同内容复用、endpoint变化形成新代际，旧binding不覆盖。全局薄入口在dynamic import之前核对全部文件／binding／key，不继承宿主传入选项；处理会话直接跳过。

用正式CLI又离线构造一份保留组件，generation `b6ff5a4ef91da122b559b56f`，manifest `df1714c0573ea57fef2a8a5fc313d6fd090bf6cd53faeaa99dd2432d9a93a6ff`，3671文件／55,378,054字节，之后重新校验tree与builder源码摘要。[回执](evidence/multi-agent-development-20261114/opencode-immutable-artifact.json)。组件位于本任务临时根，后续连接适配消费前保留；开发临时构造／测试目录已由上下文正常清理，没有完整App／引擎／模型副本。

本地反馈分别记录：MA-OPENCODE-ARTIFACT-PREFIX01失败1次（@types/node固定包根为node v20.19），MA-OPENCODE-LOADER01失败1次（Python拼接只对末段encode）；两项均在当前构造／入口检查通过，不与旧问题或两个断言合并计数。不是实际OpenCode宿主验收。

尚未写入本机OpenCode配置，也未把Connections的开发中卡片改为可绑定。下一步新增电脑级OpenCode连接adapter，接准备／启用／原操作核对，处理全局重复插件与配置冲突；再接认证补交／丢弃和真实1.3.5宿主。Node加载夹具不替代native加载、真实ACL或知识质量，原库及其他主体／会话保持。

## 设置接线与真实原生空会话（检查点三十六）

新增`OpenCodeConnection`接现有Connections API及Native多选卡片。默认按XDG_CONFIG_HOME／opencode发现，明确profile优先；未提供固定组件继续integration_pending。开发配置在顶层提供`opencode_plugin_dir`及`opencode_manifest_sha256`，不扩展原`project_control`字段白名单、不触及其他会话的launcher改动。prepare创建独立主体和私有绑定，不创建项目／dataset或改宿主；activate只原子替换本项目全局loader，保存／核对原操作ID和配置摘要，正常重载后按真实新绑定显示回执。

JSON／JSONC只读解析支持注释与尾逗号，拒绝重复键；检查已知Cognee配置入口和全局plugin目录，不覆盖外来／空loader或symlink。配置并发编辑或key／组件变化阻止启用；新代际保留旧binding。崩溃后GET原操作只核对原文件／代际，不重装或重建主体；明确preflight拒绝能作为原终态返回，Native清除待核对项但不自动重试，允许用户明确重新准备OpenCode候选。

管理侧／发现11项检查通过（39.388秒），插件9项Python含39项编译Node通过（5.290秒），Go原拒绝回执1项通过（0.983秒）；[管理证据](evidence/multi-agent-development-20261114/opencode-connection-manager.json)。管理HTTP实际执行，official身份与ACL为夹具；Native增量实窗仍待验。

**MA-OPENCODE-NATIVE-BIND01累计失败2轮，限定空会话绑定目标现已通过。** 前两次本机1.3.5创建两个空session但绑定0；第二轮探针init在两目录均执行，宿主有session.created发布而探针无event、控制请求0。保留版本／私有日志回执；原生订阅启动时序缺口有源码及日志支持，唯一内部根因未证明。未将其改名为准备问题或合并进旧失败项。

修复复用官方`session.list`核对元数据，限当前directory、created不早于本次启用，不取正文、不回放旧会话、不新增内容采集器；启动一次核对，之后沿30秒生命周期周期核对，dispose撤销启动定时器。注册／解析仍走原独立主体和policy。第三轮使用新组件生成`cd03a4c96cfb9450b73851e6`、manifest `e3bab36d3ab2fbca5707c04bade511c5b23e88870bb094a3ea037b437713af53`，两个新目录各得独立project绑定及生命周期登记；去掉探针后最终复验仍通过（10.486秒）。[真实宿主证据](evidence/multi-agent-development-20261114/opencode-native-handshake.json)。所有轮次均无模型prompt；主进程正常terminate并等待退出，夹具服务关闭，临时profile／registry清理。

当前只证明实际1.3.5加载、空会话原目标与控制协议；official内容／ACL端点仍为夹具，消息QA／trace、处理、真实知识／新会话采用没有通过声明。下一步接真实官方服务及编码链、认证管理补交／丢弃，再做Native窗口与日常收口；Ollama和首批外部前置仍保留。新组件供后续消费，旧约55MB组件暂作失败基线；原生SDK缓存及旧运行依赖继续保护，未改日常配置或原库。

## 实际编码、知识与新会话采用（检查点三十七）

实际OpenCode 1.3.5／DeepSeek Flash，使用生产prepare和不可变loader进入独立profile，沿原真实官方服务创建独立子主体及合成项目；未经过Native连接页，也未改日常配置。正常编码只改`batch_size.py`，宿主调用read三次、write一次、bash一次，没有调用Cognee工具；独立运行原测试7项通过。官方原始记录保存QA1／trace5，自动整理生成5份知识，编码阶段快照7份Data均处理完成。没有手动sync／improve、重放材料或手造知识。[脱敏证据](evidence/multi-agent-development-20261114/opencode-real-coding.json)。

**MA-OPENCODE-RECALL01累计失败2轮，第三轮限定样本通过。** 首轮新会话工具0次，但回答不知道；第二轮仅将默认期限2.5秒增加到10秒，仍回答不知道。原版不可变产物与仅改期限的产物、原会话／数据和失败日志均保留，未换主体／项目重置计数。只读核对发现：官方GraphCompletion请求一次2.565秒，确实超过原期限；另一次实际ManagedRecall返回493字符，内容却是图谱生成的“不知道／未提供”，并非宿主注入不存在的证据。直接加grounding prompt仍混入历史拒答和旧bug关系，不能作为可信当前事实，未交付该实验。

当前自动召回改用官方`CHUNKS`、`only_context=true`，项目限定官方`session_learnings`节点集；不提供session_id，不由召回模型代答或混入会话完成缓存。默认8个候选，每范围最多8000字符；当前权限核对、通用／项目隔离、撤权清除及迟到结果门禁不变。官方取源探针0.074秒返回带来源的实际知识；第三个全新原生会话工具0次，正确答出None→8、0禁用、1–64、bool TypeError、越界ValueError和7项测试。通过只限本模型／合成项目，不外推全部知识新鲜度。

2.7秒延迟的query→context回归在旧默认下失败、修复后通过。当前23项编译Node控制／召回检查，8项Python补丁检查含6项HTTP与10项outbox Node检查通过；严格编译的新不可变组件3671文件。真实原生进程按PID、启动时间和端口确认后停止，队列pending0；原官方／控制服务和原库继续保留。期间观察到一次register403，后有成功注册；原因未知，尚不据此认定ACL全矩阵通过。

后续仍须完成OpenCode真实权限与跨项目／通用知识矩阵、认证待发材料处置、Native增量；第二批Ollama及首批外部前置继续保留。用户暂缓的Cursor／Claude Code及未安装Agent不追加验证。

## UUID归属、显式记忆与真实权限（检查点三十八）

承接检查点三十七的隔离profile、原子主体与项目。**纠正上一检查点的证据边界：** 旧编码会话的官方`dataset_id`实际指向子主体自建的同名组，虽然指定项目内确实已有原始Data、5份知识及正确采用，也不能据此认定官方会话归属正确。原同名组、旧会话、旧回执与数据均保留，未自动改绑、迁移或重放。本问题MA-OPENCODE-TARGET01累计失败1轮。

受管`entry`改发`dataset_id`，登记改发`dataset_ids`，均使用原实时策略UUID，取消名称解析歧义。当前不可变产物下，真实OpenCode1.3.5新会话只read源码、bash执行7项测试，官方QA1／trace2的会话UUID归属与指定项目完全相同；另一个全新会话工具0次，正确回答原6项规则，官方会话也指向同一UUID。MA-OPENCODE-TARGET01本范围复验通过，原失败1保留；MA-OPENCODE-RECALL01失败2不清零。此前register403与名称歧义相符，但其唯一触发原因未证明。

MA-OPENCODE-REMEMBER01累计失败1轮：旧显式工具上传文件同时传session_id，官方跳过文件占位内容，接口accepted但正文未出现在原会话或预期Data。现在走官方永久上传、不传session_id、`run_in_background=true`，沿共享官方remember工具的`memory`节点集；自动召回同时取`session_learnings`／`memory`。一份新明确请求的专用验证正文完整保存并可召回，原已接收但未保存请求未重投，历史回执不改成失败或成功落库。

[真实ACL证据](evidence/multi-agent-development-20261114/opencode-real-acl.json)记录6个连续阶段：维护时读取／显式写入及召回；只读时正文200、官方写入403／删除403、显式工具拒绝且自动采集不入队；禁止时项目正文403、旧项目注入撤下，通用正文200且写／删除403；恢复只读不复活旧缓存，明确新查询可召回；拒绝材料与动作不自动重放；恢复维护后删除唯一专用临时文档，raw404。最后恢复原空覆盖，其他既有主体和既有项目覆盖保持。ACL切换由生产类和真实认证API直接执行，不冒充Native权限卡片操作。

跨项目用原控制协议登记一个空项目；同一native键仍固定原项目，新键取得另一数据组。只读补查确认新组Data列表200且0条，召回只请求新组与通用组，从未请求原组；新组空索引返回404，组件不注入项目正文、不沿用原组，通用仍可用。这证明空索引错误时的隔离，不宣称第二个项目已有原生模型对话或健康空检索200。

验收方法单独留痕：MA-OPENCODE-ACL-DOC-FIXTURE01失败1（把session上传直接等同永久文件）；MA-OPENCODE-ACL-COMMON-FIXTURE01失败1（已绑定项目会话跨组先被400拒绝，改用未绑定的专用拒绝请求后403）；MA-OPENCODE-ACL-EMPTY-FIXTURE01失败1（误要求空索引200，实际404，补查证明错误时隔离）。四次总流程停点不合并为同一缺陷4次，也不覆盖真实REMEMBER／TARGET失败。每次原记录及恢复回执保留；后续未重跑已通过的全流程来制造整轮绿色。

源码23项控制Node与8项Python补丁检查（含6项HTTP、10项outbox Node）通过；实际当前产物manifest `a2c682df90704957cbd830ae076e2859d9b4c697402632476ade7e677a63f4bf`。本轮真实Native进程均按PID／启动时间／端口确认后退出，队列pending0。专用文档已删除；4份旧知识正文摘要不变，test-baseline被官方正常整理替换为新验证会话来源，仍为7项测试，旧快照保留。原服务与控制继续保留，日常配置未改，不追加Cursor／Claude测试。

下一步补受认证待发材料review／retry／discard；尤其旧unknown核对必须同时验证官方会话dataset_id，不能仅看到内容marker就当原项目已保存。当前该目标校验尚未补齐，不将本轮当完整OpenCode交付。Native设置增量、Ollama、首批外部前置仍保持原范围。

## 旧unknown的原目标核对（检查点三十九）

MA-OPENCODE-UNKNOWN-TARGET01累计失败1轮。沿原主体／原项目，用一条受控本机unknown记录核对真实旧官方会话：它的marker确实存在，但官方dataset是旧同名子组。旧client在整段JSON中查找marker，会把该本机项从unknown错误标成saved。受控传输故障只制造本机不确定状态，没有向官方发送请求正文；不是实际网络丢回执或新宿主采集验收。失败队列、原官方会话及前轮所有计数保留。[红绿证据](evidence/multi-agent-development-20261114/opencode-unknown-target.json)。

现在`ManagedDelivery`传入已核对的原dataset UUID；官方核对必须同时满足`session_id`／`dataset_id`一致，QA只比较`qas.context`，trace只比较`traces.method_params.cognee_capture_id`。其他字段的字符串、不完整结果、无效marker或服务不可达都不确认原材料。不会根据“未找到”重新发送，仍保持unknown。

当前不可变产物的真实GET复验3项通过：旧同名子组marker保持unknown／saved0；正确原项目的QA和trace分别确认saved1。全部原主体／原会话／原项目，没有新增或改绑身份，不向服务POST内容，不启动Native或模型，不重投旧材料。主队列pending0；专用核对夹具的一条unknown继续原样保留，不自行丢弃或补交。

定向检查：24项控制Node、7项HTTP、10项outbox、8项Python补丁通过；严格构造产物manifest `908b9ad1dbe2a077b4a0e4223bb2f2b521f9fd21a2d588c0043e25b9a4188be9`。新增检查后的首次辅助失败另记：Python仍要求23项而Node已24项通过；HTTP观察器未记录method却断言GET。两处各1次检查失败，修正观察／数量后同生产候选通过，不计为新一轮官方核对失败。

本轮只完成管理处置的原目标前置。下一步将原show／check／replay／discard入口接受管OpenCode worker：复用管理身份认证，绑定原instance／project／host key／dataset，核对精确待发快照，串行排除宿主发送；重投仍沿原官方队列和实时策略，只处理已确认的原项，未知不重发，丢弃只移除本机副本。worker及运行时须在不可变产物和管理侧校验后调用，不把管理能力注册成Agent工具、不增加HTTP服务或内容队列。认证操作、并发快照和Native入口尚未实现／验收，不能将本次核对通过当补交功能完成。Ollama及首批外部前置仍保留。

## 待发快照与共享发送租约（检查点四十）

在固定官方outbox内加入管理处置原语，不另建内容队列。宿主flush与管理关键区共用PID所有的`.sender`租约，保留短状态锁；发送期间不持有状态锁。存活owner不按时间抢锁，记录owner实际退出后才恢复。管理关键区可持不可伪造的进程内token完成“核对→决定→仅发送所选ID”，不中途释放租约；token不等于管理身份认证，未暴露成Agent工具。

`pendingSessions`仅给原target／session元数据；`reviewPending`给数量和原会话精确快照摘要，不返回正文。相同native的project／dataset／official session不一致则拒绝，缺失原target不猜归属。同一会话新增材料使旧同意失效，其他会话不扩大本次范围。unknown探针只获得marker字段，未证明原结果则不能replay；匹配项可原地确认，discard只移除本机副本，另存ID墓碑防止重启后同ID重新采集，不计saved，不伪造处理水位。发送适配增加精确ID集合，原目标策略逐项照常核对。

[当前源码检查](evidence/multi-agent-development-20261114/opencode-pending-primitives.json)：24项控制、7项HTTP、14项队列Node及8项Python补丁通过。实际OS子进程持租约时父进程在任何policy／sender回调前被拒绝，文件不变；子进程已等待退出，死owner可恢复。另验证原快照、同意变化、所选项隔离、未知确认、丢弃墓碑及伪token。首次契约红灯为无发送租约／缺少管理原语；原双预检并发测试随更强互斥调整、严格补丁锚点笔误各产生1次辅助检查失败，保留私有日志，未作为真实宿主多轮失败。

本阶段只为源码／真实进程锁检查，没有构造或激活新管理组件、请求模型或启动Native；仍使用检查点三十九的私有安装候选。临时队列及子进程均收尾。尚不能宣称用户可补交／丢弃：管理认证和controller原绑定核对、不可变worker及实际运行时验证、现有Native入口分派、操作回执和旧无租约开发产物共存均待接通。当前锁检查不证明任何旧进程已退出，接线前仍须按既有所有权／升级约束核实；不自动迁移或重投旧材料。

## 管理侧处置接线（检查点四十一）

已补固定Node worker、管理认证、原目标与运行时摘要核对及现有Native源码分派；Node预检在主体创建／binding写入之前。实际原服务8次Go→Node调用通过，覆盖Agent token拒绝、所选A补交、B本机丢弃及墓碑、正确unknown确认、旧同名组unknown保留与重投拒绝。原项目策略恢复、Agent ID保留；主队列0，独立marker-only受控unknown仍1项。A/B为新增合成材料，关闭处理且action账本未变化，无模型请求；未向原未知材料重发正文。

新增错误白名单遗漏问题失败1次，限定组件检查修复通过，计数与证据保留。当前组件manifest `9f3a31944dd6e8bffcfe634223a17b17c94c357a29d4ad1daa335fec3b4ef31d`；纯Python预检顺序调整晚于该构造，TS文件未变。Go检查使用共享工作树，相关提交仅采用所需DTO字段及分派钩子，不含其他会话改动。[完整检查与边界](multi-agent-provider-execution-20261114.md)、[脱敏证据](evidence/multi-agent-development-20261114/opencode-pending-management.json)。Native实际窗口／点击、正常接入日常采用、旧无租约代际共存、Ollama及首批外部门槛继续未完成。未改日常配置，未用重置卡，保留原服务和数据，完整Goal保持active。

## Ollama配置与真实Schema结果（检查点四十二）

已实现本机Ollama独立profile、无需个人Key的既有表单、`ollama_chat/`原生路由和metadata同源／禁止重定向；保存、验证、next及启动编译沿用原账本。41项Python及10项Go检查通过，实际0.34.0空模型服务404拒绝通过，未运行Codex或远端Provider作为fallback。

独立下载Llama 3.2 3B Q4_K_M约2.02GB。32K首次加载被105GiB门禁中断1次，原状态保留；已知进程退出、空间恢复后，复用原模型及账本，以8K上下文／单并发执行同一nested-unicode-null-3。普通输出通过，实际JSON对象模式嵌套Schema校验失败，MA-OLLAMA-LOCAL-SCHEMA01累计失败1轮，质量不通过，active／next为空。4次adapter调用仍是同轮普通／结构化及自纠正；具体错误字段未知，不改样本、不注册伪Schema能力、不补知识。新旧进程组均已核对退出，唯一模型供后续消费保留，旧qwen记录保持。[证据与方法记录](evidence/multi-agent-development-20261114/ollama-local-provider.json)。

下一步查明并完成真实Schema／质量及官方知识处理与采用链，继续Native增量及日常收口。共享Native check计数另有源码口径差异：OpenCode返回核对后剩余unknown，当前文案再次减matched可能为负；实际验收轮次0，实窗前须修正。首批外部前置仍保留，完整Goal保持active。

## Schema回显定位与核对计数（检查点四十三）

OpenCode核对计数已按“确认后剩余”修正，保留Python只读tail匹配的独立含义，2项基于真实回执的Go回归通过。只接线共享UI的check分支，其余WIP保留，实窗仍未验。

新增无正文的字段／错误类型与响应类别诊断，20项Python通过；同模型同样本固定官方adapter重验仍失败，MA-OLLAMA-LOCAL-SCHEMA01累计2轮。实际三次结构化响应均schema_definition，缺project／ports／source，定位到JSON对象路径回显定义。独立SDK显式原生Schema实验从同一探针AST复用类、事实和期望值，结构通过、事实精确比较失败（MA-OLLAMA-LOCAL-NATIVE-FACT01独立实验失败1次），不代表原路径修复或知识链通过。具体错误事实值未留存；首个实验上下文TypeError另记方法错误，未请求模型，不重置／合并原计数。

进程组及独立daemon已核对退出，账本／原回执／模型保留，未改日常、下载新模型或打完整包。[证据](evidence/multi-agent-development-20261114/ollama-schema-diagnostics.json)。仍须确定有实际证据的原生Schema策略、完成事实质量与官方知识链，继续Native增量、日常收口和首批外部前置，完整Goal保持active。
