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
