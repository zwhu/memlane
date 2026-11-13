# 多 Agent / Provider 开发执行账本

目标：落实[定稿方案](../multi-agent-provider-technical-plan-20261114.md)的第一批全部要求，继续 dev，不恢复历史全量 Goal。第二批按原方案单独验收。

## 基线与所有权

2026-11-14 开始，源码 HEAD `6198179`，生产源码无未提交修改。未跟踪的项目权限、知识可行性与研究文档属于既有工作，保留、不提交。本轮独占新增连接／生成配置模块及其必要调用处；编辑共享文件前核对差异。磁盘可用113GiB。无服务／模型探测在运行；不操作日常配置和原库。

基线提交仅包含本轮方案、脱敏接口事实和此账本；实施前创建本地注释tag。不得提交本机凭据。

## 连续场景与依赖

| 场景 | 用户流程与验收证据 | 当前状态 |
| --- | --- | --- |
| MA01 | 原Codex主体、ACL、插件与正常采集召回保持；旧API兼容 | 待实施／回归 |
| MA08/09 | pi/TraeX电脑级预览→准备→激活→新会话问答工具→官方知识→新会话采用 | 优先实施；宿主信任需正常流程 |
| MA02/03/04 | Claude/Cursor同流程及跨Agent共享；限制逐项披露 | 待实施 |
| MA05/06 | 独立身份、禁采不回扫、403不重投、冲突/未知/部分成功 | 待实施 |
| LP01/04 | 各协议配置→验证Schema及质量→保存；错误安全、无跨供应商fallback | 待实施；真实协议验证需要对应可用凭据 |
| LP02/03 | 接入与模型独立；保存不切换→验证摘要→next→重启→active；失败单次恢复 | 待实施 |
| UI01 | 真实Native 1200×820，批量连接、生成配置、错误及重开连续性 | 后端固定后集中实现 |

实施顺序I01→I02/I03→I04/I05→I06/I07→I08→I09→I10。单测、协议夹具、真实宿主、模型质量和Native分别留证，不相互替代。I01基线完成，其他均未完成。

原E/PA缺陷停止记录保持；328字节README图谱停止项及helper替换问题不重试。临时物料限本轮`.local`目录，由本轮负责，用后清理，不删除既有依赖。

## 开发检查点一

基线提交`2539147`，本地注释tag `multi-agent-provider-baseline-20261114`。实施期间另一会话修改Native组件；本轮未编辑或提交这些UI源码。后续UI接线先重新核对其最新提交。

已编写，尚非日常接入交付：

- registry schema1→2迁移保留Codex ID、主体、ACL、会话关联，并留私有schema1备份；旧Codex API继续使用原适配器。
- 通用连接API及pi／TraeX准备、预览、激活与原操作幂等回执；独立不可变产物、宿主状态命名空间、摘要及配置冲突检查。不在准备阶段改宿主配置。
- pi全局扩展、裁剪的社区MIT四工具和JSON helper；TraeX原生命令hook；共用官方过滤、缓冲、PA门禁、未知写入语义。禁采事件只留ID水位；退出整理沿官方路径，不重复提交未知操作。
- 生成配置私有凭据、不可变修订、协议环境映射、验证摘要与next管理API；单独验证进程及已确认退出／未知资源边界。普通GET不触发模型。
- 启动候选／一次恢复模块与官方服务环境编译已编写；`scripts/dev.py`的实际接线仍未完成，所以当前日常启动尚不能使用新增Provider。

本次证据：[Python检查点](evidence/multi-agent-development-20261114/backend-checkpoint.json)。7个测试模块共67项通过，覆盖原控制、Codex和固定插件回归，以及新增配置、进程、迁移与隔离连接。随后补齐helper最终整理的防重提交，并重新通过6项连接检查（含TraeX问答／工具字段适配且不读transcript）。`scripts/test_pi_extension.mjs`通过实际pi0.84.1扩展加载器，使用合成helper验证四工具、问答、失败工具和撤权移除注入。TraeX0.214.1实际用户级安装器在临时profile安装通过；未绕过信任，不等于hooks已触发或模型采用。

无本轮真实模型调用、日常插件配置修改、原库迁移或启停日常服务。临时profile和测试目录已随测试清理；无本轮存活验收进程。未进入Native或完整包验收。

当前完成边界：I01完成；I02/I03/I04/I05/I06为部分实现待真实链路；I07待启动器接线；I08/I09/I10未完成。按首批完整功能及真实验收口径估算整体约20%，不把上述测试折算为宿主E2E通过。

后续顺序：补通用API／宿主失败回执与真实事件验证→启动器条件completion及下次启动应用→首批其他Agent→Native集中接线→各协议及跨Agent真实链路、权限和窗口验收。原MA/LP/UI范围保持。

用户本轮补充授权：Codex剩余用量额度低于2%时允许使用重置卡，无需重复确认；尚未使用。此授权不包含其他付费或供应商切换。

## 开发检查点二

继续完成I07源码接线：`scripts/dev.py`按所选不可变profile启动，Provider模式无需Codex executable/auth home/completion token，不启动completion子进程；官方服务读取固定revision。启动器持配置锁启动官方服务，核对私有回执的PID、profile引用和digest以及真实健康检查后才提交active，随后将写锁交给控制进程。失败仅在完整受管进程组及后代退出确认后恢复原配置一次。UI重开沿owner记录读取同一运行配置；保存候选不修改它。API模式初始化不再要求Codex健康端点。

生成配置摘要同时绑定私有key文件内容摘要，外部修改同一key版本也会使验证失效；API模式缺少Codex宿主时不阻塞独立Agent连接管理。日常配置／库／服务未改动。

[启动检查证据](evidence/multi-agent-development-20261114/startup-checkpoint.json)：启动器与生成配置共28项检查通过，API无Codex输入、配置摘要门禁、锁交接及一次恢复均有隔离证据；实际服务／Provider模型端到端仍待验。随后加严回执私有权限并定向复验相应2项用例。首次脚本检查有1项**测试夹具错误**：macOS受管可搬移Python被`venv`默认复制后找不到libpython；改用正常symlink venv后14项脚本检查通过。该轮不计作产品启动失败。

[固定上游兼容性核对](evidence/multi-agent-development-20261114/host-patch-compatibility.json)：Claude Code有4个入口需单独改写补丁，其余10个锚点匹配但语义仍待核对；Cursor有13个锚点匹配，缺少同版`_coding_memory.py`。特别要补Claude transcript禁采水位、Cursor事件模式的召回合并／撤权及无回答时不读历史，不能直接复制Codex插件宣称支持。源码审阅还发现主动search的旧CLI fallback需在受管产物明确拦截；这是待验证源码缺口，尚非本轮验收失败计数，也没有修改／重装日常插件。

整体估算约25%（对比检查点一增加约5个百分点），范围保持第一批全部Agent／Provider／Native及真实验收。I07已实现待真实链路验证，I08继续宿主专用patch及安装适配，I09/I10仍未完成。

## 开发检查点三

新增`project_host_plugin.py`，从已固定摘要的官方archive分别生成Claude Code、Cursor受管产物。保留各自官方入口；不将Codex插件改名。Claude四处不同入口独立补丁；主动Python工具固定受管凭据与绑定项目，服务不可用时不启动CLI fallback。Cursor继续使用官方payload适配和事件采集，复用同commit的官方coding recall库补公共知识，延迟注入时重建当前授权上下文，不返回旧缓存块；缺回答不扫transcript尾部。

Claude新连接在SessionStart建立stat边界，受管解析只读本session主transcript，不扫sidechain旧文件；sidechain须独立绑定。若SessionStart时transcript还不存在，保守跳过首次捕获轮，后续真链必须核对并补齐正常首轮采集，不能因此宣称MA02完成。Cursor无工具回合／首个Read工具的官方召回限制继续作为能力条件，真实IDE尚未验证。

通用连接管理已加入Claude Code和Cursor：准备仅创建独立主体产物，激活才调用Claude用户级原生marketplace/install或复制Cursor本地插件。CLI信任不绕过。核对Claude宿主installed_plugins的用户级cache及全部固定文件、私有marker；Cursor仅接受自有、摘要一致的现有产物，不原地替换运行代际；未知结果只review原产物，不重跑install。检测到Cursor可能导入已启用Claude Cognee插件时停止并要求消歧，保留全部既有配置。

[本轮证据](evidence/multi-agent-development-20261114/host-adapters-checkpoint.json)：11项检查通过，包括实际Claude原生安装器的隔离profile安装与cache摘要、Cursor准备不写host／显式激活复制、Claude禁采边界解析、Cursor延迟注入重建与无transcript读取，以及pi／TraeX连接回归。普通GET不调用模型；没有日常配置改动、真实宿主模型对话、真实Cursor IDE或新增服务实例。

本轮临时安装profile及测试产物均已清理，官方archive原件保留。整体估算约35%（增加约10个百分点）：I08代码路径已接入但仍待宿主真链和权限验证；I09 Native接线及I10整体验收仍未完成，原MA/LP/UI范围不缩减。

## 开发检查点四

I09已接Native源码：设置分“服务与连接／知识处理／高级”；读取真实通用连接和generation API。Agent多选准备、逐项结果、启用已准备项与“取消剩余项”保留部分成功；未知结果仅核对。Provider表单使用官方Form／Select／TextInput／Password，保存／验证／下次启动分别操作，修改草稿后不能沿用旧验证。APIkey只在提交时传给私有管理端，之后清空、后台不回填。原项目checkbox保持。

管理侧补充原配置操作GET回执及显式取消验证。取消等待拥有的本机进程退出，远端结果仍按unknown，不把取消当作零调用。原保存／next结果未知时使用原operation_id读取，不重新提交；明确4xx拒绝和传输未知分开处理。验证轮询只GET，可停止本机验证，不触发另一个模型请求。

已实际打开当前首页／知识设计图，并检查新增合成图。1200×820实际Native使用当前生产组件和隔离真实管理API／GenerationStore（服务数据与key均为合成），核对了：多选pi／TraeX使准备按钮可用、Provider填写和保存、当前active不切换、编辑模型后禁用旧验证、重开窗口读到已保存配置。没有点真实模型验证或安装日常Agent。截图与AX回执在本次CUA工具记录，机器摘要见[Native检查点](evidence/multi-agent-development-20261114/native-checkpoint.json)，[合成图](evidence/multi-agent-development-20261114/ui-fixtures/)仅证明组件渲染。

首轮发现保存后视觉已清空但AX残留合成key，失败1轮；按配置与控件代次更换输入Key后，第二轮Native保存后的完整AX树无旧值。详见[MA-UI-KEY01](provider-key-native-clear-20261114.md)，原失败计数保留。测试准备时无bundle的调试进程不在CUA应用清单，改为独立的UI调试容器，不包含引擎／模型，不是release运行包。退出阶段遇到macOS killpg的EPERM退出竞争；终端句柄退出及内核进程组表均已确认，没有将异常当作退出成功。全部本轮临时UI／合成服务／profile／key文件已清理，日常窗口及后台未停动。

检查：全量`sh scripts/go.sh test ./...`、`vet ./...`退出0；新增Generation／Integration定向race退出0。Python generation回归14项通过，原操作回执和queued取消补测4项通过。Native截图不作为模型质量或真实Agent采集证据。

整体估算约50%（增加约15个百分点）。仍需优先完成真实pi／TraeX采集→官方知识→新会话采用、其余宿主／权限、各Provider协议与质量、候选启动真实链，以及I10集中验收。还需补未知UI操作的跨进程恢复元数据和API模式下重新验证Codex桥的独立端点生命周期；当前源码检查未证明这两项完整，因此不能把完整方案标成完成。

## 开发检查点五

真实DeepSeek Provider探针已通过：复用现有pi模型endpoint／key，向固定官方native adapter仅发送合成样本，普通输出与含中文、null、数组的嵌套Schema及精确事实均通过，耗时13.278秒。[安全回执](evidence/multi-agent-development-20261114/real-deepseek-probe.json)。仅覆盖该配置及nested-unicode-null-1样本，不外推为所有Provider或完整官方SDK任务质量通过；结构化fallback细节仍unknown，没有跨供应商fallback。

真实启动候选链已通过：独立合成官方数据库、独立owner／JWT／child身份，环境复用已有本地embedding组件；通过管理账本将已验证DeepSeek设next，正常dev启动完成健康／应用摘要核对及active提交，next清空。运行配置没有Codex executable/auth/token/completion输入，证明Provider启动无需Codex登录。[启动证据](evidence/multi-agent-development-20261114/real-provider-startup.json)。未修改日常runtime、owner、原库或插件配置。

pi普通CLI链开始真实验收，当前MA08仍未通过。第一轮发现MA-TRACE01：helper错误地把工具参数dict转字符串，官方接口400，按顺序缓冲阻塞后续问答。已改为复用官方trace_entry，第二轮1条问答和2条工具进入官方缓存、缓冲0、final无incomplete_drain，传输复验通过；原失败次数1轮保留。问题／修复／夹具错误与HOME边界见[记录](agent-trace-schema-20261114.md)。

第二轮官方知识整理却返回no_accepted_lessons、文档0，新pi会话自动注入字符0，未采用合成约定。另记MA-KNOWLEDGE01，累计1轮失败，原因未知；先核对官方coding门槛、writer／judge拒绝及样本适用性，不直接判定模型不支持、不盲重跑或手工造知识。[问题记录](agent-knowledge-quality-20261114.md)、[真链证据](evidence/multi-agent-development-20261114/real-pi-checkpoint.json)。首轮传输缺陷不重复计入知识问题。

验证进程清理补macOS EPERM退出竞争：只有内核进程组表确无该组才确认退出，读取失败保留unknown。新增2项覆盖组仍存在与不可读取的分支；generation validation共6项、Agent连接／helper共6项通过。新trace断言增加一个事件后修正测试原metadata计数断言；不把夹具计数失败当真实宿主第二轮失败。此次未改Go，无新增Go复验要求。

合成服务处理阶段完成后，通过dev所属进程停止入口退出；终端退出0、owner文件移除、进程命令表无本轮根路径。私有Provider／pi凭据副本、owner／管理token、合成官方库及受管安装产物已清理；首轮误用实际HOME的本轮唯一pi状态命名空间在备存失败缓冲后清理，其他共享目录未删除。本地仅保留约11KB小型失败回执／合成缓冲和测试日志，仓库只提交脱敏摘要，未提交真实聊天或配置。原库和日常服务保留，无release包。

整体约55%（较上次增加约5个百分点，按首批完整功能与真实验收估算）。尚需知识质量及新会话采用、TraeX真实正常CLI hooks／信任链、其余Agent与权限、各Provider完整质量／错误矩阵、UI跨进程未知操作恢复、API模式Codex桥端点生命周期及集中验收。目标继续进行中，不缩减原范围。Codex重置卡仍仅在确认剩余额度低于2%时允许使用；本轮未使用，未发现可用额度查询／重置工具。

## 开发检查点六

补齐UI未知配置操作的持久化恢复源码：每次保存、验证或next发送前，先在稳定generation文件旁的私有小型目录写入并fsync操作编号、种类和配置引用。没有密钥、模型请求正文、endpoint或问答；管理端仍是配置与回执的权威来源。私有文件锁串行登记／清理，避免多个窗口为同一配置创建重复未知请求。记录不可写／损坏或同配置已有待核对操作时，本次请求不发送；其他配置可独立处理。

重开设置会读取待核对列表，不依赖先打开编辑器；用户核对时只GET原operations／validations回执，不重发PUT或POST、不重新调用模型。明确拒绝或确认回执后才清理本地条目；验证result_unknown继续保留。保存回执恢复原配置引用和安全字段，密钥不回填。原active／next生效规则保持，读取／核对不热切换。

[检查证据](evidence/multi-agent-development-20261114/ui-operation-recovery.json)：保存、next、验证三条“503未知→重开→GET原回执”连续检查通过；独立测试进程从磁盘读到原操作；覆盖请求前日志已存在、记录不含密钥／正文、同配置防重、其他配置继续、非私有／损坏记录门禁。1200×820组件检查确认编辑器未打开时仍有核对入口，不作为实际Native窗口证据。Generation／Integration定向检查、对应race、全量Go测试及vet退出0；最后新增纯UI恢复入口断言后再次通过定向检查。没有调用模型、启停日常服务、写用户Agent配置或构建release包。

整体估算约60%（增加约5个百分点）。此项源码和跨进程／HTTP检查已补齐，最终Native连续验收仍待做。下一步补API模式下Codex桥验证的独立受管端点生命周期，再处理真实知识质量／宿主与Provider矩阵。MA-TRACE01、MA-KNOWLEDGE01和旧停止项计数不变；目标仍未完成。

## 开发检查点七

已补API模式下Codex桥候选验证：`generation_bridge.py`在管理进程中为一次验证建立独立回环端口、私有令牌、单请求槽位，复用原completion执行／严格Schema／隔离auth home逻辑；不依赖当前completion URL或token，不修改当前API环境。缺少Codex登录／私有认证文件时，在登记模型操作前明确409拒绝，避免把已知前置失败留下为未知请求。

验证所有者同时管理独立探针和app-server进程组；创建／确认退出写小型回执。取消先停探针再关闭自身端点，等待各请求清理并核对已拥有的组。清理未知则保留临时工作区和凭据副本、标记unknown、阻止下一个验证；旧未知组存在或内核状态不可读时，不信号旧PID、不宣称本机已退出。公共`process_groups.py`沿用macOS EPERM后核对内核组表的方法。原普通endpoint仍复用既有执行和工具隔离，未重装日常Codex、升级组件或恢复旧helper停止项。

6项新生命周期检查使用真实本机HTTP＋合成stdio：API配置无completion URL的成功链、取消探针和独立app-server、缺登录拒绝、旧未知组防重、不可读组状态、清理未知保留与门禁。另有endpoint工作区清理失败保留auth副本的断言；测试安全收尾确认仅其自己创建的合成进程退出后删除材料。全量Python后端103项、启动器14项通过；其中generation相关23项、endpoint14项，均不作为真实模型证据。

真实模型另验：使用现有Codex gpt-6.1-sol认证、固定官方native adapter和合成样本，在独立配置账本里模拟当前API配置；没有启动另一个API内容服务或变更日常模型。首轮普通精确文本失败，诊断轮观察到样本指令自带句号，第三次修正指令后完整普通文本、嵌套中文／null／数组Schema和精确事实通过（20.505秒）。原API引用不变，完成后bridge组为空、探针实际组不存在、资源全部释放。[脱敏证据](evidence/multi-agent-development-20261114/bridge-lifecycle-checkpoint.json)。MA-FIXTURE-PLAIN01按方法问题记2轮失败，[记录](generation-plain-probe-fixture-20261114.md)保留原结果；旧E09和其他产品计数不变。此结果仍只覆盖该模型及固定样本，不替代真实知识质量或全部协议矩阵；模式仍unknown，不宣称远端严格原生Schema。

真实探针／诊断及所有检查终端已退出。检查两轮探针实际PGID不存在、本轮根路径无存活进程后，清理临时脚本、合成配置及令牌，仅留本机小型失败回执；不清理其他运行依赖。未做release或Native重验，Go源码本轮未改。

外部前置现状：常用环境及pi／Claude已检查入口没有OpenAI、Anthropic、Gemini API配置，不代表全盘不存在；Cursor常用CLI与两个App目录未检测到安装。已异步询问用户私有配置路径／可用入口，不请求聊天粘贴密钥，同时继续独立工作。TraeX正常`login status`返回已登录标记，只证明登录前置，不证明hook真链或允许绕过信任。

整体估算约65%（增加约5个百分点）。桥候选验证生命周期源码与上述证据已补齐；后续优先pi／TraeX普通编码材料→官方知识→新会话采用，补宿主采集上下文与知识质量的证据，其他Agent／Provider、权限、Native和启动恢复矩阵仍按原范围完成。外部前置尚未阻断所有工作，目标保持进行中。

## 开发检查点八

pi及TraeX普通编码→官方知识→新会话实际采用的限定链路已通过。两者保留独立官方身份、同一规范项目与dataset，后台由真实DeepSeek API处理。pi先修复合成零值bug，接着正常扩展retry_options并真实运行7项测试；官方产生4条正确项目标签知识，新pi会话自动注入1630字符、工具0次，准确采用。正常交互式TraeX也在工具0次的回合采用pi知识；随后真实新增describe_retry、运行10项测试，官方产生2条知识；全新交互式TraeX自动注入1886字符、工具0次，正确说出新函数与10项测试基线。实际官方问答、trace、知识raw、源码与独立回归已相互核对。[脱敏证据](evidence/multi-agent-development-20261114/real-coding-hosts.json)。不以TUI自动标题或ANSI截断文本判定知识采用。

修复MA-RECALL01：helper绑定主体和launch record后调用官方_project_memory.begin/prepare；原先漏调用，写入无项目标签而召回按auto标签过滤。原3条试验知识和失败会话保留，无手工打标签／重放。失败1轮保留，修复后第二轮新写入与自动召回通过。[问题](agent-project-scope-20261114.md)。MA-KNOWLEDGE01原失败1轮也保留，当前证明真实编码材料可沉淀；不外推任意约定／口令样本或所有Provider质量。[质量边界](agent-knowledge-quality-20261114.md)。

宿主来源元数据只取提示／回答边界的git/cwd/date，未读取历史transcript；测试区分start_commit和end_commit，不声称提交包含工作区尚未提交改动。pi只采集正常最终stop回答，跳过工具中间说明／截断／错误；处理会话提前跳过。TraeX修复官方pending返回不带ts导致空prompt_time，以及缺turn_id时事件键碰撞；保持官方pending和trace格式。hooks绑定准备时的绝对Python，不改用户默认环境。[契约](traex-event-contract-20261114.md)。

TraeX原生安装器会改name并清理同插件旧cache版本；补每代独立marketplace、准备时目标manifest及完整native cache核对。新代启用后仅disable登记的旧自有入口，保留旧cache及其他插件。未知review只核对原生回执／完整cache和启用状态，不重发install。[失败计数与复验](traex-native-cache-20261114.md)。真实安装器连续两代cache均通过摘要，篡改／禁用拒绝、未知只读核对通过。原Codex产物／manifest未变。

最终当前源码全量Python后端104项通过；定向7项含上述真实安装器、官方导入顺序、真实Git边界与事件契约；真实pi0.84.1 loader通过4工具、1问答、1错误trace和撤权撤下上下文。Go源码未改，无额外Go复验。测试调用均走task.py，未下载模型、升级组件或制作release包。一次loader命令误用旧npm scope路径，定位实际symlink后通过，记录为前置方法问题。

资源：两轮正常交互式TraeX均退出0，最新实际PGID经内核组表确认不存在。独立服务仍由本轮dev launcher工具句柄89919持有（本轮已实际poll确认存活），仅服务81379与控制81535继续运行；官方最后一次全局索引完成。`.local/multi-agent-coding-e2e-20261114`及`/private/tmp/memlane-coding-fixture-20261114-9srdz5c9`保留供下一步同主体／同库权限与原会话回归，包含私有测试凭据副本，不提交；消费结束后先核对所属进程退出再清理。当前余量约112GiB，未触及原库、日常Agent profile、共享embedding依赖或build。

整体估算约70%（较检查点七增加约5个百分点，以首批全部功能和真实验收为口径）。MA08/09的上述正常链及MA04同仓共享已补齐；逐主体权限／不同项目隔离、Claude首次采集与正常宿主、Cursor真实IDE、其他三种Provider凭据及真实质量／错误矩阵、原Codex回归、Native连续窗口仍未收口。首批范围及第二批单列规则保持，目标继续active。Codex重置卡仅限核实低于2%；尚未使用。

## 开发检查点九

MA05／MA04继续以原隔离库和两独立主体验收。部署的pi／TraeX helper走真实HTTP：TraeX维护→只读→禁止→恢复，同一会话召回1886→1886→0→1886；只读写／删除及官方直连均403，pi权限不变；同一禁采事件恢复后仍返回skipped_policy duplicate，官方没有创建该材料会话。第二项目得到独立dataset、两个宿主归属相同，召回没有混入第一项目知识；原会话临时改变cwd仍保持原dataset。维护态两个主体分别完成显式remember→官方Data/raw→处理完成→指定单条DataID forget→Data消失。官方有效ACL包含角色／tenant联合核对：项目read/write/delete、通用read、均无share。[证据](evidence/multi-agent-development-20261114/real-agent-permissions.json)。

发现并修复MA-ACL-REPLY01，失败1轮保留：明确写／删除拒绝曾被通用异常出口显示为未知。现在实时确认ACL后返回denied／403及固定错误码；策略查询失败、其他传输未知仍保守unconfirmed。官方401／403也单独分类，绝不输出异常正文或自动补投。[问题与方法错误](agent-policy-replies-20261114.md)。全量后端104项、定向7项通过，Go未改。权限矩阵使用真实官方服务、部署helper及私有独立凭据，不称为新增CLI模型会话。

为加载候选，在确认原官方processing-status为17／17完成、pending0后，正常dev stop确认组退出；工具89919终态退出0。同一runtime与数据根正常重新启动，现由工具48466持有，未改active Provider。随后按管理API显式准备／激活新代，身份和原事件水位保留；TraeX新代独立marketplace，旧自有入口禁用、旧cache及第三方插件保留。两条合成显式remember均仅提交一次；夹具遇到文件名stem／extension及后台材料化时机问题后，仅核对原操作，没有重发。

本轮原日常配置和库、原Codex产物及登录未变。当前隔离根仍为`.local/multi-agent-coding-e2e-20261114`，补充标记第二合成仓库`secondary_project`，私有attempt记录显式写／删结果；供后续原Codex与Native验收，消费结束再按所有权清理。已用正常CLI的TraeX无存活进程。Claude本机auth status已实际返回loggedIn=false／authMethod=none；已异步询问可用登录或私有配置入口，不索要聊天粘贴密钥。

整体约73%（比检查点八增加约3个百分点）。MA05的上述实链及MA04不同项目过滤已补；通用知识有内容时的优先级、真实buffer403／显式补交、Claude正常宿主／首轮、Cursor、其他三类Provider、原Codex与Native仍未完成，不以此缩减首批范围。无release，未使用重置卡。

## 开发检查点十

补候选探针LP04：使用固定官方adapter与SDK，观察请求形状、完成状态和固定事实；length／拒绝／Schema／质量明确拒绝，供应商／本机超时保持unknown，401／403／限流等固定错误码分开。模式来自实际adapter请求，不冒充远端严格Schema。只对隔离探针限制一次外层attempt和SDK瞬态重试0，官方JSON自纠错与同模型Schema降级保留；日常引擎未改。Native显示固定中文原因和请求模式，未知错误码不会回显供应商正文。[实现边界](generation-probe-boundaries-20261114.md)。

同时补最新验证门禁：registered_revision保存登记顺序，管理next、启动候选和Native统一检查最新验证；新的失败／未知／进行中不再被历史成功遮盖。旧记录无顺序且冲突则拒绝，不猜键顺序、不自动重新验证；active不变、next与原错误保留。保存重开、明确新通过后再放行的连续检查通过。本轮为源码审阅发现并新增覆盖，未新增旧E／MA失败计数。

固定Cognee/LiteLLM对合成loopback服务的协议／错误检查通过：Schema与JSON、同模型降级、401／429各一次、拒绝、有效JSON却被截断、Schema不符、事实错误、unsupported、HTTP超时、变化目标发送前拒绝。generation31项通过；Go全量、vet、相应race通过；新增Native文案后定向组件检查通过，实窗仍待做。两套早期定向命令重叠运行了相同loopback用例，只算同一组覆盖，不虚增测试项或失败次数；没有重复供应商模型调用。

新版真实DeepSeek隔离探针通过，13.881秒，2次adapter调用，模式json_object，普通／嵌套Schema／事实均通过。未修改原generation账本的active，未启动第二内容服务；私有验证目录及key副本在组退出确认后删除，一次性launcher脚本已删除。[回执](evidence/multi-agent-development-20261114/probe-v3-errors-and-mode.json)。这不替代其余供应商凭据、真实质量、日常业务任务拒绝／截断或原Codex验收。

隔离原服务仍由48466持有，保留原库／Agent状态供后续回归；其管理进程仍是检查点九启动时加载的版本，执行新版管理／Native实链前需在确认官方任务结束后正常重启该隔离实例。没有启停日常服务、重装日常插件、触碰旧停止样本或构建release。全局设置与要求的首页／三栏图片已实际查看，沿现有Native控件加状态说明，未宣称视觉通过。

整体估算约76%（增加约3个百分点）。下一步原Codex及Native连续验证，补403待发补交与通用知识优先；Claude登录、Cursor和三种Provider的真实前置仍待用户可用入口。目标范围与未完成矩阵保持，未使用重置卡。

## 开发检查点十一

实际1200×820 Native完成独立API配置下的连接列表、保存／密码清空、合成401错误、原验证回执重开、原回执只读核对、已有配置密钥不回填，以及当前／next分开显示。截图通过CUA实际观察（2400×1640物理像素），本轮未导出仓库PNG。自有debug窗口与bundle独立于原Memlane Dev，不构建release或完整引擎运行包。[连续记录](native-settings-continuity-20261114.md)、[脱敏证据](evidence/multi-agent-development-20261114/native-settings-checkpoint.json)。

发现MA-CONNECTION-API01：连接发现错误依赖模型桥runtime字段，API模式遗漏Codex；改为Agent独立配置／默认宿主探测，兼容旧home／ID和旧API。无登录、profile尚未创建时也能只读发现；缺安装显示禁用；坏目录在创建身份前拒绝且不遮住其他Agent。Native复验出现Codex，隔离目标仍未创建，原模型auth字段仍缺省。

发现MA-UI-VALIDATION-RESTORE01：重开配置清空已有验证状态，官方失败无法展示；改为从配置digest的唯一最新原回执恢复操作编号与状态。实际同一原401验证重开后显示错误，核对原验证不再调用模型，测试端点全程只有1次请求。两缺陷各失败1轮、复验通过，原计数保留。一次CUA AX在Cmd+Q后隐式启动缺少dev环境的自有bundle，作为方法错误记录，原服务／库没有丢失。

全量Python后端113项、Go全量及vet通过；最终坏目标前置保护加严后18项连接定向通过，Go生产代码未再改。Native本轮next选择是原已生效DeepSeek配置，不宣称完成不同Provider切换；active保持原引用。合成401端点已按实际所有权停止并确认组退出，一次性脚本移除；失败候选及原操作元数据保留，没有重复模型请求。

最新隔离dev launcher为29176工具句柄（owner77579）；Native为1277工具句柄。原48466／64706等均已正常dev停止并确认，401端点2336为有意SIGTERM后退出，非产品验收失败。`.local/multi-agent-native-20261114`保存一份最新调试窗口资源供剩余Native场景，`.local/multi-agent-coding-e2e-20261114`继续保留原官方库、认证和合成repo；日常runtime、原Agent配置及其他窗口未改。后续读取owner实况，不仅凭此处PID判断存活。

按首批完整功能与验收口径约80%（比检查点十增加约4个百分点）。仍需原Codex CLI／Desktop、Native批量／未知操作／项目checkbox、403待发补交、通用知识优先、Claude／Cursor及其余Provider实链；第二批边界保持单列，目标未完成。未使用重置卡。

## 开发检查点十二

独立Native连接页完成Codex准备与激活，使用新官方子主体、隔离profile及原合成项目dataset，pi／TraeX状态保留。正常CLI首次信任、插件信任经宿主界面操作，没有绕过；真正首个提示触发SessionStart并绑定原项目。新会话自动获得1886字符既有知识，不使用任何工具即正确回答此前pi／TraeX生成的默认值、零值、负数、测试数和命令，补充跨Agent采用证据。随后正常编码增加retry_indices及三项测试，官方记录两轮问答、五条工具trace，完整13项测试通过。[脱敏回执](evidence/multi-agent-development-20261114/real-codex-checkpoint.json)。

原始前端为Codex0.167.2，但自有profile后台daemon实际自动更新为0.169.0，必须按混合版本记录，不声称纯0.167.2基线通过。前端升级提示已跳过，未执行全局CLI升级；原auth与config内容摘要复核不变。中途发生后台重连，一次输入经本地队列／turn及官方问答核对确认未提交后才重新提交；后续确实观察到额度切换提示，未将其推断为前一次丢输入的已证唯一原因。保持本会话Sol模型，未使用重置卡。实测周剩余额度9%；用户授权仍严格为确认低于2%，等于2%不触发。

**MA-CODEX-FACT01，累计失败1轮，尚未修复。** 官方自动完成整理并产生三条知识，其中footprint:retry-indices将函数位置写为“placed after retry_config.describe_retry”，实际retry_indices位于retry_limit之后、retry_options与describe_retry之前。原始Edit diff保留了正确插入位置，其他函数行为与13项测试事实正确；不能凭部分正确将本轮知识质量判为通过。尚未定位错误首次出现于官方候选提取、curator或writer；已读官方writer输入构造及unsupported拒绝规则，它仍属于模型判定，不能据此证明中间输入无误或认定某一阶段根因。

错误知识、原库及原始私有会话保留；未人工修正文案、删错行、换简单输入或重放整理来制造通过。Codex自身新知识到新会话采用暂未验收通过。此问题是本次正常编码知识出现错误位置事实，区别于MA-KNOWLEDGE01的旧样本无accepted lessons及已停止的328字节README图谱问题；不合并计数，也不重试旧停止项。后续同一位置事实问题跨版本／模型／操作仍沿此计数。

CLI正常退出不等于其后台daemon已退出。自有profile的后台与watcher进程仍有实际消费者，认证、组件和隔离库保留供剩余场景；未清理原包或日常服务依赖。整体仍约80%，本轮新增Codex采集与跨Agent采用证据，但质量缺陷及原先其他未完成项仍在，目标保持active。

## 开发检查点十三（进行中）

MA-CODEX-FACT01只读定位取得新证据：官方cache_session_context中的workflow_state候选已包含错误“after describe_retry”，confidence为0.9，来源指向实际编码问答。因此错误至少早于curator/writer。固定coding_unit_extraction.render_trace_input只渲染Edit.old_string/new_string，Codex实际Edit.diff未进入此紧凑输入；正确diff在原始官方trace中仍存在。此静态证据说明编辑证据缺失，尚不证明完整推理因果。官方服务仍严格校验原archive，不新增引擎补丁、不改提示词或人工知识来绕过质量验收；失败次数仍为1。

MA06补齐实际官方403→恢复→明确补交流程：[脱敏证据](evidence/multi-agent-development-20261114/real-403-replay.json)。使用TraeX独立主体和第二合成项目，将已合法缓冲的QA在本地permit完成后注入真实ACL撤权竞态，内容POST确实由原官方API返回403。随后恢复原write ACL，自动drain保持0发送、1待发；用户明确replay按原signature发送1条，官方QA恰好1条，后续自动drain为0/0。全程只有两次内容POST（一次403、一次明确补交成功），未发起模型处理。属于受控真实故障注入，不冒充普通宿主自然发生的竞态。

本组两个验收方法错误分别保留：首次误把registry首项当主项目，前置断言即停止；其次HTTP拦截器漏了GET的可选payload，内容POST次数0，但发送前标记已使一条合成材料呈未知。先核对官方session404，再通过产品明确discard丢弃这一条测试材料，保留错误回执后修正方法；没有重投未知材料，不计作产品403失败，不删除业务记录。

源码审阅补安装器HOME归属：Claude／TraeX子进程明确使用installation.home，避免指定隔离profile时其附属状态仍继承控制进程HOME。真实Claude安装测试将ambient HOME与安装HOME分离，核对两条真实安装命令及ambient目录无写入；host plugin5项、connections8项通过。此为预防性修正，非已证用户目录被修改。

DeepSeek官方[Anthropic兼容说明](https://api-docs.deepseek.com/guides/anthropic_api/)及[Claude Code接入说明](https://api-docs.deepseek.com/quick_start/agent_integrations/claude_code/)提供可推进Claude宿主实链的入口。仅在本轮隔离profile，使用原DeepSeek凭据、明确固定deepseek-flash与https://api.deepseek.com/anthropic，不替代Anthropic原生服务验收，不切日常Provider或Agent账号。Claude Code2.1.96正常CLI已完成首次界面与项目信任，真实模型读源码及13项基线测试通过，新增编码任务仍在执行；尚不宣称首轮采集／知识／新会话采用通过。

为加载隔离Claude profile与新版安装器，确认两个dataset均pending0后，正常停止原dev owner77579及本轮Native78646，并确认原工具29176／1277退出0。新dev owner28696、工具81878已健康启动，原库与原generation保持；Native尚未重开。后续按实时owner核对，不据旧PID清理。原Codex后台消费者与认证仍保留，未触碰日常实例。

Claude真实链取得两项独立缺陷，完整宿主闭环仍未通过：[回执](evidence/multi-agent-development-20261114/real-claude-checkpoint.json)。

- **MA-CLAUDE-FIRST01，失败1轮，首轮边界定向复验通过。** 第一实际任务完成retry_enabled及16项测试，但SessionStart时transcript不存在，旧适配标记paused；Stop才以全文末尾建立边界，官方session404。仅在Claude宿主补丁处理官方startup、同一UUID／profile内预期路径、初始确实不存在、目录身份及策略revision未变的首个文件，才允许从0采集。早期flush遇文件尚未出现时保留缺失回执；resume／fork／clear／compact、已有文件、权限变化或路径不符仍不回扫。官方[SessionStart定义](https://code.claude.com/docs/en/hooks#sessionstart)区分这些source；本机2.1.96普通CLI验证到startup，未借新文档推定本机其他新事件可用。修复后第二个新会话的首轮正常编码确实捕获1QA／6trace，边界size0，无旧首轮补采。
- **MA-CLAUDE-SPOOL01，失败1轮，源码修复及定向检查通过，真实复验待做。** 上述第二任务完成last_retry_index及19项测试，但固定Claude插件强制cloud分支选择独立官方spool，而受管环境关闭其idle worker；7条材料仅入本机spool，官方仍404。源码改为受管Claude的_cloud_worker.enabled返回false，未来材料统一复用已有官方warmup buffer及PA drain；不启用缺少PA门禁的独立worker。原7条材料均attempts0、ambiguous=false、文件0600，完整保留；未迁移、补交或重放。后续真实升级必须单独验证官方入库与知识、新会话采用。

同时补Claude升级保护：新代启用后只停用记录中的上一代自有插件，保留旧cache／产物；只读核对要求当前代启用且没有其他Cognee插件同时启用，部分升级或外部恢复双启用不会被核对为成功。真实CLI隔离升级验证只有一代启用，测试也覆盖再开启旧代时拒绝。原Codex固定插件manifest与官方引擎archive未改。

在首轮及升级修复后，后端全量115项通过，67.271秒；随后只增Claude cloud-spool路由及异常settings类型的核对保护，并通过最终host-plugin7项，11.390秒，未重复无关检查。新增边界用例早期有两个夹具问题（合成control token太短、current stub漏位置参数），分别修正并保留本轮输出；不是生产首轮重复失败，不重置真实缺陷计数。Go源码未改，沿用检查点十一全量／vet证据。

本轮Claude两个普通CLI均已通过/exit结束并确认对应工具退出0。隔离dev经正常启停加载首轮／升级修复，最新owner41651／工具97508；Native已通过新runtime路径重开，工具77122，仅是窗口启动，不算新的视觉验收。当前controller及已启用Claude插件仍是spool修复前代，下一步须正常重启控制进程并经原管理接口升级，不能声称源码修复已在该宿主生效。原Codex后台、隔离库与原认证仍保留；未启动release。首批整体估算约81%（增加约1个百分点，完成403明确补交与部分Claude实链），Codex事实质量、Claude完整闭环、Cursor、原生三类Provider、剩余Native／权限及第二批边界均保持，目标active。

## 检查点十四：Claude完整样本、跨协议切换与HTTP保护

承接上轮正常升级后，Claude Code 2.1.96 / DeepSeek 新会话真实增加 first_retry_index，22项测试通过；首轮 QA1／trace6 进入同一官方项目dataset，正常退出后官方整理完成3份知识。新会话不调用任何工具，正确给出函数位置、复用关系及0／None／负数规则。MA-CLAUDE-FIRST01、MA-CLAUDE-SPOOL01 均完成本样本真实复验，各保留原失败1轮；旧7条未投递spool原样保留，没有迁移或重放。[Claude回执](evidence/multi-agent-development-20261114/real-claude-checkpoint.json)。

实际Native只保存一次合成通用偏好“没有项目明确规则时默认重试5”，随后核对原保存回执和官方处理完成。上述Claude新会话正确采用项目明确值3，说明项目规则优先。知识中记录22项测试，但官方git来源将基线提交后的脏文件标为变化，新会话谨慎报告历史验证结果，没有无条件确认当前22项仍新鲜；此来源粒度限制保留。helper知识中的“empty input”也未明确指空indices，不能据正确数字结果泛化全部知识质量通过。[优先级证据](evidence/multi-agent-development-20261114/real-common-priority.json)。

第二个合成项目的实际Native中，Claude维护→只读→无权限→只读→维护逐项立即保存，Codex／pi／TraeX卡片保持；使用各独立主体核对官方有效权限：无权限为空、只读只有read、维护read/write/delete且无share。重开状态保持。新发现并修复MA-UI-BACK01（从通用知识打开项目设置时返回标签错误）、MA-UI-COMMON-REV01（其他项目策略变化撤下旧正文时错误提示已撤权），各失败1轮，实际Native复验通过；原提交回执保留，通用知识正文哈希与owner读取不变。最终恢复显式maintain/capture/auto_process，不误记为恢复继承。[Native证据](evidence/multi-agent-development-20261114/native-cross-agent-checkpoint.json)。截图由CUA实际观察，未导出仓库PNG。

真实DeepSeek Anthropic兼容配置保存、固定样本验证通过（2次adapter请求、json_object），Native“下次启动使用”与当前配置分开。正常重启后当前配置才改为Anthropic兼容端点，next清空；服务实际settings确认provider/model/endpoint，四主体身份与八组实际项目ACL均完全保持。随后正常TraeX源码／测试检查采集QA1／trace4，22测试通过，官方整理完成2份知识，同一项目30条数据全部处理完成。这是同一供应商的两种协议，不是Anthropic/OpenAI/Gemini原生账号验收。[切换证据](evidence/multi-agent-development-20261114/real-cross-protocol-switch.json)。新测试基线的数量／命令正确，类清单只列实际7类中的5类，不宣称完整类清单通过；原MA-CODEX-FACT01错误位置知识仍保留且未解决。

固定SDK的Anthropic/Gemini原生wire已覆盖普通输出、嵌套中文/null/Schema、401、429与有效JSON截断，准确检查原生路径与认证头；纯loopback合成服务不替代远端模型。新增真实SDK重定向检查发现默认跟随307，MA-PROVIDER-REDIRECT01失败1轮。探针和正式服务现共用进程所有的SDK HTTP策略，关闭自动重定向；SDK缓存淘汰后新客户端仍应用策略。三个协议同源307与无探针observer的跨地址308／缓存清空复验通过，目标请求零次。另修3xx被SDK包装成“连接未知”的诊断，MA-PROVIDER-REDIRECT-STATUS01独立失败1轮。[问题及轮次](generation-redirect-protection-20261114.md)。固定官方引擎源码／archive未改，没有新网关或知识副本。

最终探针全模块10项通过（66.884秒），配置／验证／启动／桥接25项通过（1.387秒），Go全量通过（simpledesktop 2.115秒）、vet退出0。新策略下真实DeepSeek Anthropic兼容探针再次通过普通文本／Schema／事实检查，2次请求，探针进程组已退出。[HTTP策略回执](evidence/multi-agent-development-20261114/redirect-protection.json)。隔离dev正常停止owner81101后，以owner8824／工具26238加载新策略并健康启动；原registry逐字节不变、当前配置及next不变。Native旧进程82788已退出，更新debug二进制与runtime路径；不把重新启动窗口记作新的视觉验收。原Codex后台及后续消费者依赖继续保留。

本轮只做dev源码、必要调试编译与实际Native验收，未构建release／完整运行包。首批整体估算约85%（相对81%增加约4个百分点），主要增加Claude实链、通用／项目规则采用、Native权限连续性及跨协议重启证据。Cursor真实宿主、三类原生供应商账号、Codex事实质量与剩余连续故障场景仍须完成；第二批OpenCode/Ollama不丢弃、不纳入已通过。目标保持active。额度重置卡仅在确认Codex剩余额度严格低于2%时允许；本轮没有使用重置卡。

## 检查点十五：批量接入取消与窗口重开

MA-UI-CONNECT-UNKNOWN01，失败1轮后修复并复验：Agent准备／启用原操作编号原来只在内存，503后新App构造即丢失，可能产生新请求。现复用私有UI回执目录，在网络变更前持久化原编号、类别及实例标识；管理侧新增只读原回执GET，缺失或unknown保持原编号，不重装。Agent与Provider操作按类别隔离，即使同名ID也不相互阻塞。资格判断同时用于卡片、按钮和每项实际发送前，已准备、已连接、冲突、未安装或结果未知不会因陈旧选中而重复准备。[完整记录](connection-batch-recovery-20261114.md)。

实际Native三项批次：pi真实文件系统准备成功但丢弃成功响应；TraeX当前项完成后取消，Claude未执行。重开窗口保留pi原编号，核对仅GET且POST仍为2次；只选择剩余Claude后POST总数3，全部产物摘要核对通过，无待核对本机记录。实际控制代码及文件系统、合成主体和服务；没有宿主启用或模型调用，不冒充新Agent实链。第一轮夹具因HOME未创建导致两项安装前拒绝，方法失败1轮，原两个unknown回执保留；成功夹具未清除或重发旧轮材料。[脱敏证据](evidence/multi-agent-development-20261114/native-batch-recovery.json)。

最终Go全量通过，simpledesktop 2.783秒；新增定向race 2.007秒，覆盖准备／启用丢失响应、持久化顺序、原回执核对、取消、陈旧资格与Provider隔离。vet退出0。后端Agent／原控制27项通过（6.042秒），随后新增管理专用HTTP回执检查1项通过（0.711秒），含未认证401、子主体403、管理200、缺失原记录及不写registry。固定官方引擎、原Codex产物和manifest未改，Codex错误位置问题未重新调用模型，仍失败1轮、未解决。

原隔离库三dataset均pending0后，正常停止owner8824并以owner31343／工具28327启动新管理端点。实际原连接旧回执读取再验401／403／200，registry在正常重启及GET前后逐字节不变。Native已恢复原隔离runtime并实际看到“已连接”、原两项目及14条知识；工具61956保留运行。两个批量夹具进程已退出，成功夹具临时插件副本消费完后清理，保留小型manifest／回执与初始未知证据；原Codex后台和实际依赖保持。本轮无release、无模型调用、无重置卡。

首批整体估算约86%（相对85%增加约1个百分点），补齐MA06/UI01中批量部分成功／取消、丢失回执及重开恢复。完整目标保持active；Cursor、原生三类Provider账号、Codex知识事实质量及其他未证明场景继续保留，第二批不缩减。
