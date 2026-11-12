# pi 官方目录集成与自动采集缺口

本项属于当前Goal的Agent矩阵核对，不扩展产品Agent范围。核对使用npm正式发布包`@kerryhatcher/pi-cognee@0.1.0`，不安装、不运行包代码、不读取宿主配置或会话。

## 固定证据

- 官方Cognee inventory将pi列为社区维护，代码在作者仓库，不是Codex插件的同构版本。来源沿交付账本固定提交`28fde88150661444a0b8ac69cc81ee0e41ec8f5a`。
- [npm固定版本元数据](https://registry.npmjs.org/@kerryhatcher%2fpi-cognee/0.1.0)的gitHead为`5807e9e5e8231db8b04c0d1d7fb4c5dd3a28dd9b`；发布归档SHA256为`a5a0705e8d92de6a529cff4d5d40cb786f70dcc86e669706f4d51c5c007f18e0`。已验证registry给出的SHA512完整性；21个文件只解包到本轮.local，拒绝链接、路径越界和超预算解包。
- 小型证据在`build/conversation-official-20261112/pi-official-contract-001/manifest.json`及`source-inspection.json`，固定各文件SHA；不把registry最新版本漂移到本次结论。

## 能力与缺口

`index.ts`注册9个模型可调用工具及2个配置命令；没有`pi.on`生命周期订阅。发布包的实际TypeScript源文件没有问答／工具事件自动订阅，也没有`/api/v1/remember/entry`的typed QA／trace发送路径。`cognee_remember`必须由模型调用，不能用它证明正常对话的自动采集。

默认SDK模式通过`@cognee/cognee-ts`在进程内运行，不是复用本机既有Memlane Cognee服务。MCP模式需明确配置与重载；工具调用使用Streamable HTTP，当前实现没有服务API key请求头配置。本轮未安装SDK、未创建其默认存储，不自动将SDK模式作为共享官方存储的替代。

因此，已有发布包可作为显式记忆工具的复用候选，但**不能满足本Goal的自动问答／工具采集**。这个缺口与“插件安装不便”不同：已核对固定版本的入口及全部TypeScript源文件，没有对应采集实现。不能继续将pi标为“存在可直接复用的完整自动采集插件”，也不能据此断言所有未来版本没有此能力。

## 后续适配范围

优先保留该社区包作为官方目录中的工具集候选，不在本轮另写第二套remember／recall或SDK知识库。自动采集若补适配，仅做pi宿主事件到Cognee官方QA／trace接口的薄转换，参考已固定Codex官方采集的过滤、截断、隔离与重试契约；授权、项目归属和关闭采集须独立明确，不复活旧自有采集器。

本机pi确认为`@earendil-works/pi-coding-agent@0.84.1`；其实际安装包文档提供`message_end`、`tool_result`、`agent_end`和`session_shutdown`等事件。应按该版本事件类型及真实上下文确定消息身份和工具关联；不得把agent_end当最终稳定结束，文档说明它之后仍可能自动重试／压缩／跟进。下述增量已开始编码和宿主层检查，尚未安装或完成真实自动采集。

Codex真实官方自动采集仍优先；隔离登录／hooks信任的未答复选择不由本项核对代替。TRAE／traex和Claude的既有矩阵边界保持。

## 宿主投影与生命周期增量（未安装）

连续用例：明确授权后记录当前leaf基线；正常问答／工具完成并触发`agent_settled`；以真实宿主entry ID生成稳定操作身份；服务端确认每项保存或策略过滤后推进游标。未完成回复不作QA；临时失败只查询未知回执，不盲目重发；切换会话／工作区、树导航、关闭或撤权隔离旧回调。处理记忆会话不启用采集。只遍历启用后的有界parent链，不调用getEntries／getBranch或读取历史文件。

新增`internal/agents/pi-official-capture.ts`和`pi-official-capture-host.ts`，分别承载消息到官方QA／trace字段的投影，以及真实pi生命周期订阅。工具必须具有匹配的新toolCall／toolResult和明确isError，不从文本猜成功；仅收文字块，不收thinking／图片。输入按UTF-8字节截断并标明范围。`session_tree`重建基线，不回扫选中的旧分支。

官方SDK的QA／trace ID由服务端生成，typed remember请求没有宿主幂等ID。因而稳定operation和内存pending不能单独宣称重启去重；宿主将未知发送结果冻结，后续只调用只读inspect。新的daemon broker仍须补授权、固定官方策略、轻量持久回执及重启核对。策略必须能检查未截断工具参数后再投影，不能仅对截断后的参数做路径拒绝；当前代码尚未安装，不把接口约定当作过滤已经生效。未另建全文／知识／版本库，没有运行improve。

检查结果：11项投影用例、7项生命周期用例通过，共18项。覆盖不读历史、处理中禁用、未settled不读、未完成回复、三重工具ID碰撞、未知送达不重发、部分／错误回执不推进、撤权和晚回调等。首次宿主测试因Node strip-only不支持TypeScript参数属性而在加载阶段失败1次；改成显式字段赋值后通过，不删断言。原错误与修复记录保留，未并入旧Codex hook停止问题。

另用本机实际pi0.84.1的`SessionManager.inMemory`生成合成消息，验证真实entry结构可投影1QA／1trace，只读取基线后4个entry，没有历史边界读取、宿主文件持久化、HTTP写入或模型调用。broker为合成回执，故这不是普通pi对话自动保存、真实服务幂等、Native或交付证明。证据摘要与输入SHA位于`build/conversation-official-20261112/pi-capture-projection-001/`；受管命令均退出0，磁盘余量约127GiB。

仍待：daemon broker和官方策略接线、持久回执与重启恢复、App安装／授权入口、普通真实pi问答工具→官方服务→Native可读；Codex官方插件的真实授权／信任闭环、其余Agent矩阵和正式迁移切换也未因此完成。

## 单次官方写入与轻量账本增量（schema33，未接日常授权）

按plan-change-impact重新沿宿主→授权→过滤→写入→回执→阅读核对：最先影响方案成立的是官方请求无客户端幂等ID，而不是宿主事件是否存在。当前固定SDK的`RememberResult.to_dict`提供status／dataset_id／session_ids／entry_type／entry_id，typed QA／trace路径不自动运行improve；新增适配严格核验这些实际字段，不将HTTP 2xx视为保存证明。

`internal/official/capture.go`仅接受有界QA／trace，不接受feedback、node_set等扩展输入，trace强制generate_feedback_with_llm=false。正常化输入后计算正文摘要，在官方context／memory_context加入宿主操作标记和摘要；凭据仍仅在HTTP头中。写入前后核验原服务身份，只发送一次POST；HTTP失败、回执身份错误或写后服务身份变化都返回未核验结果，不自动重发。授权与未截断参数的官方策略过滤仍由待接daemon broker负责，此方法不能独立充当采集授权。

新增schema33的`official_capture_receipts`，只含操作、库、授权摘要、官方实例／数据集／会话／类型、正文摘要、状态和官方条目ID。发送前提交dispatching，单次并发claim胜出；重复请求只返回已有状态。重启不将dispatching改成可发送，备份恢复保留旧意图且换库身份阻止旧授权重用。policy_filtered明确不调度发送；保存回执重复封存同ID幂等、异ID冲突。没有全文、参数、凭据或新索引表。

迁移只在测试临时库执行，日常库、旧31条源库和已有独立运行根未打开或升级。同步旧版本迁移夹具删除新表的准备步骤，并把旧session处理迁移测试的硬编码32改为SchemaVersion；旧内容保留、迁移备份、任务数、恢复与权限断言不变。新schema32→33升级及备份恢复检查通过。

受影响完整测试store43.055s／official0.867s／records1.376s；新捕获边界定向race store3.093s／official2.153s；三包vet退出0。首次官方写入定向0.741s、账本定向0.872s也通过。未运行已停止的旧hook桥接或全仓test；不存在新真实模型、官方HTTP业务写入或Native验收。证据`pi-capture-transport-ledger-001`，仅为本轮源码／HTTP模拟与临时SQLite证据。

接线前仍需解决两项明确边界：①当前历史读取`AuthorizeCogneeBinding`限定Codex，MaterialSubmission明确不授予历史读取；pi自动采集的授权不能直接拿材料提交或Codex授权冒充，须落独立明确同意与可读范围。②官方现有标准会话详情省略context，未知写入的服务端对账需用已授权完整材料或最小只读适配；当前本地账本查询不证明官方未收到，更不能据不存在匹配就盲重发。下一步接明确授权、固定策略和只读对账，再安装并做真实普通会话与Native验收；原Goal所有其他缺口保持。

## pi新消息采集授权（schema34，未安装／未接写入）

连续用例先明确：已有pi连接和材料绑定时默认仍不采集；App明确确认当前项目与新消息范围后，保存同意；宿主每次稳定结束前取当前授权；用户关闭后旧确认重放不能重开；重新开启必须提交新操作和当前授权key；连接／工作区／官方绑定／库／策略变化拒绝旧同意；重启保留已同意状态，备份恢复默认关闭。此授权不扫描历史，也不把MaterialSubmission升级成历史读取许可。已经采集内容如何在App按来源受控读取仍待接线，不通过放宽历史授权解决。

新增`pi_capture_consents`轻量元数据表、Store检查和daemon／IPC的review、disable、authorize入口。review／disable要求trusted App的UserConfirmed；authorize只接受daily、空tool mode、单项目和宿主工作区，不接受处理、连接检查或空通道。绑定必须已实际登记、是pi材料提交绑定，当前连接和项目授权有效；库身份、官方绑定、工作区Git／物理身份及固定策略SHA进入稳定key。新同意以previous_key作CAS，旧撤销不会关闭新同意，旧确认不会复活已撤销操作。此时不运行Python策略、读取消息或发官方写入；固定SHA只是策略版本合同，不能声称过滤已运行或采集已可用。

新增PI-CAPTURE-WORKSPACE-ALIAS-001，首次定向批失败1轮：把解析后的目录路径与保存路径字符串直接相等，macOS `/var`／`/private/var`别名导致合法授权被误拒绝。store和daemon两个命令运行同一修复前实现、暴露同一现象，按一轮计，不按断言数累计。修正为比较解析后Git与物理身份，新增真实symlink同目录成功及目录替换拒绝，原拒绝断言均保留。后续定向store4.619s／daemon1.656s通过；最终受影响完整store49.292s／daemon33.061s／serviceipc1.648s／records0.671s通过，定向race store8.934s／daemon3.149s及四包vet退出0。schema33→34升级留下备份并保留未知写入意图；恢复保留同意证据但enabled=0。旧迁移夹具同步删除新增表，不减弱内容／备份／授权检查。

所有库均为临时测试库，未打开或升级日常库、旧31条或现有合成运行根；未安装扩展、调用模型、执行真实官方业务写入或新增Native验收。证据`pi-capture-consent-001`。下一步仍须接固定上游策略在截断前的过滤、授权后的持久写入与官方只读对账、安装入口及真实普通pi→官方保存→App可读；Codex真实官方替换、其他Agent、正式数据切换、编辑历史和视觉总验收仍未完成，Goal继续。

## 固定上游策略执行与截断顺序（未安装／未接持久写入）

新增`agents.ApplyOfficialCapturePolicy`，读取host组件路径提供的策略文件，先验证完整SHA，再把同一批已验证字节交给隔离Python进程；不在验证后再次按路径加载。Python使用`-I -S`，不继承Agent环境、认证、PYTHONPATH或site包，只有host显式允许的Cognee采集开关／规则进入子进程；5秒上限、上下文取消杀进程组并等待、输入／输出容量限制、错误不回显正文或异常文本。没有网络、SDK存储、模型或改善任务。

wrapper直接执行未修改的官方`_capture_policy.py`函数：完整参数先allow_tool，再redact，再检查插件自调用和保守低信号。pi builtin bash/read/write/edit只在规则匹配时映射官方名字，保存的工具名仍是宿主实际名字；普通文本工具返回不伪造退出码或结构化成功结果，不能据此删除低信号。问答同样在截断前脱敏。当前daemon未接自定义环境设置；将来支持时须将配置纳入授权摘要，不能仅凭策略源码SHA复用旧同意。

同时移除宿主投影的提前4000／8000字节裁剪：授权后新增范围内，正文与参数在1 MiB易失内存上限内保持完整，超预算拒绝整批、不推进游标、不剪掉过滤依据。新增`official.BoundCaptureAfterPolicy`专门在策略之后执行4000／8000／500字节UTF-8边界、32参数／16KB总量／4KB单值限制，并把真实截断标志写回宿主来源信息。未知工具状态、历史基线、thinking排除、会话隔离和送达不重试契约保持。原宿主UTF-8用例改为检查完整策略输入与整批超限拒绝，存储UTF-8及来源标志由新增Go检查承接，没有取消原容量要求。

### 实际来源与夹具纠正

早期调研引用提交`28fde88150661444a0b8ac69cc81ee0e41ec8f5a`，实际CLI安装回执则固定`997fd6f72253aa97b49996eb1b032793f0257935`，二者插件版本字段均1.7.7但源码不同。首次制作策略夹具误用了早期调研提交，下载SHA断言失败、未写入夹具；随后策略定向检查因夹具缺失失败。按验收准备错误保留，不把摘要校验放宽或把它算作采集产品缺陷失败。改从实际安装回执的提交下载，SHA与已安装文件一致：`aa49445f3d78880d5784c0b47195994aabc88dc32d0a2824a8232d77ad4719b0`，18058字节，源码未改。testdata保留原LICENSE和SOURCE.json；对应提交根NOTICE返回404。两个提交与准备失败记录均保留，不能只按版本号推定相同源码。

策略定向通过1.667s；最终agents策略／daemon授权定向1.484s／1.688s，official完整1.485s；定向race agents2.636s／official2.276s，三包vet退出0。Node宿主18项通过，覆盖完整输入不提前截断与超限拒绝。检查包括超长私钥、工具参数截断范围之外的敏感路径、嵌套凭据、官方开关／匹配错误、自调用、真实结构化成功／错误和普通文本区别、ambient环境隔离、篡改源码拒绝及恶意自定义regex取消。仅测试实际固定上游策略与源码边界，不是实际普通Agent自动采集。证据`pi-capture-policy-001`。

接线仍待：daemon仅在当前同意下执行策略、把accepted结果交后置限制和持久账本、把filtered结果记轻量回执、未知结果只读核对官方缓存；随后安装及正常pi对话→官方服务→Native。未运行停止中的旧hook或全仓test，没有升级日常库、真实业务写入或完整包构建，原Goal保持。

## daemon发送与官方只读对账接线（源码／模拟HTTP，未安装运行）

本轮把前述授权、固定官方策略、后置限制和metadata-only回执账本接入`pi_capture_submit`／`pi_capture_inspect`。宿主会话／entry身份重算稳定operation与官方session，拒绝其他会话、伪造operation、重复项及超预算输入；所有post-policy项在首个发送前验证，坏邻项不会让拒绝批次产生部分写入。Python与策略路径只来自daemon可信配置，Agent请求不能选择路径、环境、endpoint或凭据。

新发送先校验服务能力再claim，持久dispatching提交后只发一次typed POST；相同操作并发只允许一个发送者。已有dispatching只做GET核对，不重发；saved／filtered只返回既有轻量回执。预检失败且账本从未claim时，当前同意／库／来源不变的inspect可返回`not_dispatched`，宿主仅在整批证明没有未知写入时重新获得提交资格。官方缓存查无记录仍是unknown，绝不能据此重发。网络期间监测撤权并取消，结果采纳前重核授权；外部已经开始的写入不能回滚，未知意图保留，撤权结果不返回／不采用。

新增官方同进程只读`capture-receipts`适配，只检查当前认证主体自己的精确dataset／session，不展开visible user、不扫其他会话或文档；缓存不存在／过期返回unknown，同一标记重复、内容不匹配或权限不符拒绝确认。返回的实际字段只在该请求中比对，不进入客户端正文库。身份接口新增`capture_receipt_v1`，发送前拒绝缺该能力的旧组件，不以安装版本号推定接口存在。服务源码和组件构建输入已接，当前保留组件尚未重建／启动该新接口。

### SDK真实存储限制核对

实际Cognee1.6.7 `SessionAgentTraceEntry`还会将字符串截到1000 Unicode字符、容器截到20项并规范部分文本。原Go的4KB参数／8KB结果限制较宽，响应丢失后严格对账会因SDK归一化而不匹配；这是实际SDK结构核对发现的缺口，没有冒称修正前已做真实服务长工具验收。后置限制现先按实际SDK界限规范，再施加字节预算与总量，保留截断标志；过长嵌套key／深度和会被SDK剪坏的operation marker在发送前拒绝，不伪造来源。已用Go生成待发payload，再经真实SDK缓存模型与只读选择器核对，字段完全一致；保留1000字符／20项事实，不宣称工具原文完整。

首次直接SDK导入只隔离数据库根，触发默认用户日志轮转并删一份旧日志，已向用户说明并留下[隔离缺陷复盘](cognee-sdk-log-isolation-incident-20261112.md)。后续子进程有独立HOME／cwd／数据库／日志根、关闭文件日志与bytecode，不带Agent配置或凭据。真实SDK复验期间默认日志目录metadata未变；先前旧日志无法恢复仍是已知影响，不以复验通过抹去。

### 检查与完成边界

最终受影响完整测试daemon56.096s／official1.849s／serviceipc1.683s／records1.370s；定向race daemon28.980s／official3.090s，四包vet退出0。Node宿主20项通过；Python5项包括实际FastAPI HTTP认证／路径合同、own user精确范围、缓存不可用、重复标记和字段选择。模拟HTTP连续覆盖成功重放、响应丢失恢复、请求未到仍unknown、缓存内容变化不确认、并发只POST一次、敏感路径在截断前过滤、坏邻项不部分写、预检失败后安全恢复、旧组件拒绝和发送中撤权。没有更改停止中的旧hook问题或运行全仓test。

证据`pi-capture-broker-001`含SDK结构回执、源码SHA、测试会话与边界。原31条源库SHA仍`59114c4efc3ee166f9f754435e0447da644e69bdf7c959e62d7aa845ff5279ee`；本轮未打开／升级日常业务库或既有运行根。HTTP为模拟服务，实际SDK核对只构造模型，没有启动真实模型、执行官方业务写入、安装扩展或新增Native验收；完整运行包未构建。

下一步仍须完成CLI／pi宿主bridge、App安装与同意入口、新组件运行、正常真实pi问答和工具自动保存、按已采集来源受控读取与Native显示，以及原Goal的其他Agent、迁移切换、知识编辑历史和整体视觉验收。源码主流程通过不代表自动采集已交付，Goal保持进行中。

## CLI、现有扩展安装与实际pi加载器

新增host-only `memlane pi-capture authorize|submit|inspect`，不注册模型工具、不授予／撤销同意、不写outbox。连接、代次、工作区和可选项目来自host参数，正文仅走有界stdin；JSON不能覆盖request／endpoint／凭据，处理／验证通道在IPC前拒绝，错误只返回固定协议和安全code。authorize不要求安装时固定项目，daemon根据可信cwd只解析已授权当前项目，之后仍须存在独立enabled同意；全局授权不变成采集目标。

Node broker只调用配置中的bridge，参数不带正文、子进程不继承Agent凭据或NODE_OPTIONS；等待子进程close后返回，容量超限、取消、错误协议及错batch都不采用。scope_denied等明确无同意状态返回未授权，不伪装成daemon故障。submit／inspect固定原grant、项目、工作区和宿主会话，未知结果仍遵循前述不重发契约。

安装复用现有`agents.Install`和唯一`memlane.ts`设置入口，三个采集模块以摘要纳入Installation并使用已有事务回滚；不新增第二条settings扩展引用，也不保存登录或开采集同意。仅本机已验证pi0.84.1启用观察器，其他版本保留原工具但capture_supported=false。更新前检查所有owned模块，拒绝覆盖未知文件／外部修改；检查安装时验证模块摘要，断开时只清理完整未修改文件族，任何成员改动则保留整个族及依赖，同时移除已授权的settings引用。重连更新代次／文件不能复活服务端撤销的同意。

### 实际边界证据

- `pi-capture-cli-001/transport.json`：真实开发CLI二进制和真实Unix socket／同uid校验，authorize→submit→inspect；daemon为合成协议响应器，无业务数据库／官方服务写入。验证完整长输入未在Node／CLI层提前裁剪，正文不进argv，授权时无需固定项目，发送与查询绑定回执项目。
- `pi-capture-loader-001/loader.json`：通过现有Go安装流程生成扩展，再由实际pi0.84.1 jiti加载器加载；独立HOME／PI目录／cwd，关闭jiti磁盘缓存，不发现用户其他扩展或读取历史。实际SessionManager.inMemory的新4个entry触发1批、1QA／1trace，启用前基线未读，重复settled没有重复提交，shutdown已执行。使用真实CLI和UDS，但daemon仍是合成响应器；未启动普通模型turn、未写官方服务、未新增Native验收。

安装定向首次因测试把custom JSON格式化后的RawMessage当原始紧凑字符串比较而失败，属于夹具断言错误。改为严格比较原模型值和custom对象成员，保留原无关配置要求，复验通过；未计作产品安装失败，也未放宽摘要／权限／外部修改断言。最终定向agents安装与既有pi项目配置6.500s；完整agentbridge2.495s／store54.379s／serviceipc2.705s／records2.160s；定向race agents5.589s／agentbridge1.667s／store10.652s及相关vet通过。Node27项通过。未重跑旧Codex hook或agents全包／全仓test。

本阶段真实安装只作用于临时合成pi目录，临时配置／socket已收尾，没有更改用户pi设置、授权或原数据。开发CLI供下一步真实集成暂留；不是完整运行包，未做release打包。Memlane daemon、CLI和宿主适配均为自有代码，只有指定策略源码／SDK接口／存储属于Cognee复用，不能统称官方实现。

仍待：App明确同意控件、新组件启动与运行准备、普通真实pi对话自动送达、只读取已采集来源的App链路及Native连续验收；其他Agent、正式迁移切换、编辑历史和整体视觉仍按原Goal继续。当前安装／协议／SDK加载通过，不代表日常自动采集业务交付完成。

## 两次普通真实pi对话自动保存（隔离业务链路）

新服务组件已实际编译、启动，通过健康检查、独立主体登录和 `capture_receipt_v1` 能力核对；创建隔离dataset，使用真实 `agents.Install` 在自有pi目录安装唯一扩展及三个companion。独立同意经真实daemon IPC明确启用，未修改用户pi设置，也未给予历史读取权限。Memlane daemon／CLI／宿主适配仍是自有代码，固定Cognee策略、typed API及官方存储按既定边界复用。

实际运行pi0.84.1／DeepSeek Flash普通print JSON模式，禁用其他扩展／上下文发现，仅显式加载已安装扩展并开放bash工具，未开放memory工具、未人工remember。第一会话在合成仓库创建SQLite库存脚本，实际观察无条件扣减到-1的断言失败、修复后affected_rows=[1,0]／accepted=1／rejected=1／stock=0，并生成Markdown回答；4个真实bash调用、1次agent_settled，进程20.048秒退出0。自动获得1QA／4trace的真实官方保存回执，随后通过精确own-user receipt接口逐项读回并核对operation／digest／official ID，官方session详情计数一致。

第一会话的模型使用echo输出脚本退出码，外层bash实际均成功。初版审计误假设应有isError=true，失败1次，归为夹具状态假设错误，保留原工具事件及原官方success状态；不得从stdout中的AssertionError伪造工具失败。随后第二个独立普通pi进程明确执行不吞错的断言命令和修复脚本，7.279秒退出0，2个真实工具isError依次true／false，官方trace状态error／success一致。新会话自动增加1QA／2trace，第一次5条operation／session／kind／state／official ID全部原样保留，未重复。合计2个实际宿主会话、2QA／6trace，8条回执全部saved。

证据：`build/conversation-official-20261112/pi-capture-real-001/{result,reopen-result,official-receipts,reopen-official-receipts,official-session,combined-delivery-ledger,stop-receipt}.json`。私有运行脚本与合成事件位于 `.local/pi-capture-real-20261112-001`，不提交凭据、数据库或对话日志。客户端memories／versions／jobs始终0条；原31条业务源库SHA仍为 `59114c4efc3ee166f9f754435e0447da644e69bdf7c959e62d7aa845ff5279ee`。自有daemon经stop协议、官方服务经核对配置所属PID后停止，既有76077／76867仍在；数据库、服务组件和依赖保护保留。未构建完整运行包。

准备阶段一次服务启动命令相对路径写错，task明确终止后改正确路径；首次health观察超时发生在实际服务首次加载本地embedding期间，保持原进程并后续健康检查成功，没有因超时重启。两项均属运行准备记录，不算自动采集产品验收失败，不重置旧停止缺陷。

本次证明正常模型→真实宿主事件→真实daemon→官方保存及读回，以及Agent重开新增材料和真实工具失败状态；不代表App安装／同意控件、Native阅读、新链路知识处理、临时故障／重开daemon恢复、历史迁移切换、其他Agent或整体Goal已通过。下一步应单独接“已采集来源”的受控读取：由当前连接／项目／库和已确认receipt限定session及entry，不能放宽现有Codex历史授权或将pi材料授权变为任意历史读取。界面入口与相同范围的迟到／撤权刷新须一起验收。

### 已采集会话读取实施顺序

复用影响分析流程：App原始记录入口→当前项目唯一binding→显式captured查询→Store核对原同意、当前连接／代次／物理工作区／库→仅枚举saved receipt→官方own-user精确receipt读取→从官方原文重算原payload摘要→现有Markdown阅读组件。禁止调用历史catalog或将材料binding改成历史binding；客户端仍不保存正文。关闭后续采集不删除已保存内容，读取依赖当前有效连接与原同意身份，不依赖capture enabled；撤连接、换目录、换库或换代次立即拒绝。旧Codex历史读取保持原入口和原规则。

先检查授权和篡改拒绝，再接daemon分页／详情，随后App选择入口和授权控件；测试要覆盖无同意／错误grant／仅dispatching或filtered／内容或官方ID变化／关闭采集后仍可读／连接撤销／网络中撤权／同一session的非receipt正文不得出现。真实现有2session／8receipt为后续集成与Native上游产物，不能用夹具替代。首次只做读取不会重新启动模型或修改旧数据；隔离服务数据和新组件保留供后续Native消费者使用，最后统一收尾。

## 已采集来源受控读取与Native035／036

Store按原明确同意、当前连接／代次／授权项目／物理工作区／库筛选saved receipt，dispatching／filtered／不同grant不进入读取。关闭未来采集仍可读已经确认保存的内容；连接撤销和身份变化拒绝读取。SessionQuery新增显式captured模式，历史入口仍走原Codex规则，未放宽AuthorizeCogneeBinding。daemon分页来自已确认receipt中的session，只调用官方own-user精确receipt路由，不调用历史catalog或任意session详情。读回从官方context恢复原payload并重新计算摘要、核对已确认official ID；每批网络后重新核对授权与被采用的所有receipt。正文、QA时间及会话时间未另存，未知时间继续留空；空receipt页也先验证真实服务身份和能力，服务不可用不能冒充已确认空态。

App原始记录页选择pi材料binding时固定captured模式，复用现有Markdown、工具展开、列表／阅读滚动及后台刷新；其他材料绑定不因此得到历史读取权限。实际重启原隔离官方服务和新版daemon，刷新同一主体的进程期token、复用原binding及同意，不创建新dataset或重复采集，真实IPC读出2session／2QA／6trace，历史接口仍拒绝。证据 `pi-capture-real-001/app-read-ipc.json`。

### 实际窗口及失败记录

- Native034首次已显示真实自动采集会话，但误沿用历史“最近20条”提示，记 `CAPTURED_READ_LIMIT_COPY_001` 失败1轮。已按captured路径修正文案，不修改历史路径的真实限制；035、036实际窗口确认修正，并将有可靠宿主身份的回答标为pi。034默认CGO=0导致专用截图菜单未编入，属于验证构建准备错误，后续显式CGO_ENABLED=1；不拿无截图菜单当闪烁产品失败。
- Native035实际1200×820，自身NSWindow截图2400×1640，两张首屏／工具图。真实失败和成功工具独立显示，保留上游未提供逐轮关联的边界。查询synthetic、搜索焦点、展开状态及scroll_y=857.35645保留；31.000秒309全帧同尺寸，目录与详情32→48。正文及数量区域各1种RGBA，整窗3种，不宣称整窗每像素恒定。
- Native036实际800×620，自身NSWindow首屏1600×1240，查询零结果后清空恢复原选择，工具展开和真实滚动；31.001秒309全帧同尺寸，目录25→41、详情26→42，scroll_y=1207.7227固定。正文及数量区域各1种RGBA，整窗2种。全部帧保留，未丢帧／重采样／改内容凑稳定。

证据 `pi-capture-native-035`、`pi-capture-native-036` 的source-info、captures PNG／JSON、frames.json及pixel-analysis.json。实际打开窗口与截图对照普通／窄稿；这两轮只覆盖正常刷新，不冒称实际故障／撤权／晚回执全矩阵通过。现有工具UI会同时显示相同的结果与错误文本，形成重复错误段，记录为待修显示缺陷1轮；原文字段未改，后续集中修复去重显示并复验。隔离阅读入口未挂完整RuntimeController，侧栏仍显示服务未连接，不能据这些图宣称完整运行状态展示通过；真实daemon及官方读取由独立IPC回执证明。

### 定向与完整回归

Store／official／daemon定向读取检查6.283s／1.609s／4.413s；对应race9.853s／2.189s／4.566s。新增真实接口fixture连续检查：禁止无同意、错误grant、未知session、错误owner、篡改正文或official ID／摘要，关闭采集仍可读、撤连接与网络中撤连接拒绝迟到正文、读取不增加POST；App两尺寸固定captured请求及正确限制提示。补充daemon／desktop定向5.300s／1.280s、race8.476s／2.169s。完整受影响包store67.428s／official1.107s／daemon71.127s／desktop33.633s／records2.213s，相关vet退出0；未运行包含已停止旧hook问题的全仓测试。

完整回归和035／036候选之后再补空receipt页服务身份预检及对应“服务不可用不能显示已确认空页”检查；该只读守卫单独做daemon定向／race，不将旧GUI二进制冒称包含这项最后增量。仍需下一候选统一验证。自有GUI均正常退出，服务已按原根IPC／所属PID停止，官方数据和组件保留；证据 `pi-capture-real-001/stop-read-receipt.json`，日常服务76077／76867仍在。未打完整运行包。

仍待App可操作的采集同意与状态、空页守卫最终候选、新链路实际故障／撤权刷新及知识处理／出处闭环、Codex与其他范围内Agent、正式迁移切换和编辑历史；不把以上读回及正常刷新通过升级为完整Goal交付。

## App明确采集同意与普通业务闭环Native037

新增只读pi_capture_settings接口：按当前pi连接及代次，只返回已有范围授权和材料binding的项目，检查当前目录／库／策略身份，读出原同意及下一次确认所需PreviousKey；查询本身不创建同意。保存数量来自确认过的saved receipt，不把安装成功、已允许或单个CLI的结果外推为正在采集或Desktop／IDE已通过。

设置→Agent连接→pi详情新增项目级正常对话记录卡，复用当前Native设置布局。开启先展示项目范围与重开pi提示，再明确确认；未知保存结果保留同一个operation／binding／PreviousKey供重试，状态重读不自动开启。停止使用当前同意key，保留原数据；换连接／代次和迟到设置响应不采用。新项目或保存位置未就绪时显示缺口，不把电脑级安装变成项目已授权。用户日常跨仓库自动归属与完整接入仍按原目标继续，本卡只承接已有明确范围的真实链路。

### Native037真实连续操作

实际1200×820开发GUI通过正常设置回调和真实daemon IPC：先显示原8条保存回执，点击停止后SQLite同意enabled=0；再打开确认页，独立只读核对仍只有原1条disabled同意及8条saved receipt，确认页没有提前开启。点击确认后产生第2条新同意，旧同意仍disabled，新key enabled；原8条记录身份不变。确认页和后续10条状态、工具去重截图均在 `pi-capture-native-037/captures`，review-before-confirm.json记录确认前实际数据状态。

随后重开普通pi0.84.1／DeepSeek Flash，仅开放bash、未用memory工具或人工remember，一次实际运行inventory_check.py并回答，16.112秒退出0。实际agent_settled自动新增1QA／1trace，两条ledger均绑定App新授权key；官方own-user精确receipt读回核对ID、operation和digest。设置页自动更新8→10；经正常导航进入项目原始记录页，第三段会话的真实回答与1条工具可读。真实daemon再读取3session／3QA／7trace，历史catalog仍拒绝；客户端memories／versions／jobs仍0条，原31条源库SHA未变。结果见 `pi-capture-native-037/result.json`、new-official-receipts.json、delivery-ledger.json、consent-history.json，以及 `pi-capture-real-001/after-app-read-ipc.json`。原前8条回执完整保留，不以新授权重置旧失败计数。

此前“相同结果／错误重复段”具体显示问题失败1轮保留；现仅在错误文本与字符串结果完全相同时省略重复错误段，不改原官方字段或复制内容。Native037打开原失败工具，AX及实际截图均只有1个完整错误结果，成功工具仍独立显示，修复通过。最后空receipt身份守卫已编入037源码／候选；本轮真实窗口有内容，空页仍仅有专项故障检查，不冒称新增空态Native证据。

### 验证与剩余边界

Store／desktop新增设置连续检查1.821s／0.883s；对应race3.714s／2.238s。覆盖状态读取不启用、原key与disabled状态保留、错误代次拒绝、两尺寸开启前确认、丢回执重试复用intent、停止和迟到设置响应。完整受影响store81.776s／daemon84.863s／desktop38.943s／serviceipc2.621s／records6.449s通过，相关vet退出0；旧Codex hook及全仓test没有重跑。

037仅新增普通窗口设置／真实采集／返回阅读证据，没有新的窄窗口设置或31秒录制。上轮035／036正常刷新证据保留；新设置临时失败／明确撤权的状态呈现、实际服务故障恢复与晚回执仍须统一验证，不能据设置读取和开启通过宣称全矩阵已交付。还发现长单行问题完整作为标题导致占用4行，记录标题截取显示问题失败1轮，待按已保存问题截取规则修复；原文保留。隔离入口RuntimeController未配置的状态文案缺口仍在，不能把后台正常IPC读取外推成全运行环境已就绪。

本轮GUI已正常退出；自有daemon／官方服务安全停止并保留数据、组件和新同意，既有76077／76867仍在，stop-receipt.json记录收尾。没有完整运行包或release构建。下一步集中修复标题与权限失效呈现，执行新链路实际失败／撤权刷新、知识处理／出处闭环，并继续Codex、其他Agent、迁移切换及编辑历史等原Goal；本分项不代表完整目标完成。

### Native038连续异常场景计划

沿用同一3session／10receipt／2条同意的隔离根，不重新生成材料。候选修复问题首行截取与两行标题上限，以及pi设置临时失败不展示“已允许”、明确失效移除旧范围和review。复用既有Native截图／连续帧菜单与IPC驱动；先实际阅读并保留工具／滚动，再SIGSTOP核对本根官方进程PID与T状态，验证真实请求超时保留旧正文；同PID SIGCONT后验证恢复及切会话迟到响应不覆盖，最后通过exact-generation disconnect IPC撤销本根连接，确认正文撤下、复制禁用。SIGCONT是必要收尾，不能在服务仍暂停时直接宣布退出。

故障期调用会受真实15秒读取期限约束，31秒观察可能只完成约2次请求；记录实际sequence，不把定时器轮次或采帧数当10次请求。正常／恢复阶段继续要求>=10次刷新，失败／撤权阶段分别如实说明证据范围。连接最终撤销保留原数据和同意／回执，后续若要恢复访问须新确认，不能修改数据库或复活旧代次。日常服务与组件不动，临时GUI和已冻结组件保护到消费结束；原停止问题和累计失败计数保持。

用户在038运行中询问“上游未提供逐轮关联，工具独立列出”的意思：已说明官方返回的工具与QA没有可靠逐轮对应字段，不能猜测关联，也不表示工具失败。源码改为“以下工具记录暂未关联到具体问答。”，保留真实关联缺口；正在运行的038冻结候选仍为旧文案，新文案需进入下一候选再核对，不以源码修改宣称窗口已更新。

### 用户要求先核对官方逐轮关联能力后的纠正

先前直接将问题定性为Memlane遗漏并开始新增turn_key，证据不足，实施顺序不对。用户明确要求先确认Cognee是否已有完整链路、是否仅需展示；已停止该方向，精确撤回本轮尚未测试／安装的turn_key、RelatedQA和解析helper，未覆盖其他修改，未改库或Agent配置。

只读核对本机Cognee1.6.7：`memory/entries.py`的QAEntry／TraceEntry输入没有事件time／prompt_time，`infrastructure/databases/cache/models.py`中SessionQAEntry有qa_id／context，SessionAgentTraceEntry有trace_id／memory_context，但没有直接指向某QA的外键。`session_manager.add_agent_trace_step`按user/session追加trace；sessions详情独立读取并返回qas[-20:]及traces[-20:]。`used_session_context_ids`和SessionContextEntry.source_trace_ids属于回答使用的整理上下文／上下文来源，不可直接当原始工具属于某个用户请求的证明。

官方Codex插件固定997fd6f（1.7.7）中，store-user-prompt把turn_id写入QA context，store-to-session的默认trace entry没有turn_id或memory_context关联；因此不能因QA已有turn_id就宣称两端可直接join。

另一个已研究的官方提交28fde881确有可选COGNEE_CODING_MEMORY路径，`_coding_memory.py`保存QA的time／prompt_time及trace事件time，并显式检查服务端OpenAPI是否具备这些字段；检查失败说明需要Cognee PR #6568。2026-11-12本次直接核对该PR仍为Draft，未合入dev；其服务端coding_turns.py按事件时间把trace分配至请求／答案区间，属于官方已有开发实现，但不是本机已部署能力，也不是sessions接口直接返回嵌套问答工具。来源：[官方插件](https://github.com/topoteretes/cognee-integrations/blob/28fde88150661444a0b8ac69cc81ee0e41ec8f5a/integrations/codex/plugins/cognee/scripts/_coding_memory.py)、[官方PR6568](https://github.com/topoteretes/cognee/pull/6568)。不能用“官方没有”或“只是前端漏展示”概括全部版本。

当前结论：优先核对这条官方方案的可用版本和实际返回能力，再决定最薄接入／展示；不继续自定义平行轮次模型，不默认升级服务或启用会扫描transcript的模式，不把尚未合入的官方代码描述为现有客户端已具备。其余独立UI问题和原Goal范围保留，未标完成。

补充实时版本核对：GitHub releases/latest 与 tags API 均确认最新正式版为 v1.6.7（2026-11-06发布，eaa33be），与本机版本一致；PR #6568 API 返回 state=open、draft=true、merged=false，head=ec6f40859e5f2df9e7862dde27d580ac3268ed43。元数据证据保存在 `build/conversation-official-20261112/official-turn-support-audit-001/{release,pr,tags}.json`。因此当前并非已有正式版本返回完整逐轮链、只需前端展示；官方开发分支已有相关实现，仍需区分内部整理配对与公开阅读接口返回能力。本次仅只读核对，未升级服务、启用该模式或新增自定义关联。

### Native038实际结果与新缺陷

实际800×620读取原3session／10receipt，标题首行截48个Unicode字符并最多显示2行，问题正文完整保留；此前长单行标题失败1轮在本窗口复验修复。pi设置对临时失败改为待核对并禁用变更，对明确失效清除旧范围／review；完整desktop30.645s、定向race2.942s及相关vet通过。用户文案修改后定向布局／权限检查1.377s；这些检查不代表设置失败全矩阵Native已完成。

本根官方服务PID87992经核对配置后SIGSTOP，ps确认Ts，原进程同PID SIGCONT后Rs，不重启、不换token、不重新生成记录。普通基线31.000秒309帧，目录／正文25→41、scroll410.83582；超时31.001秒309帧，目录／正文62→64、错误timeout／service_unavailable，原正文／选择／滚动保持，正文RGBA摘要与正常基线相同。失败期仅2次请求推进受15秒期限限制，没有冒称10次请求。

请求尚未恢复时切换第二会话，再恢复同PID；实际窗口保留新选择，未被原会话迟到响应覆盖。恢复31.001秒309帧，目录84→99、正文85→100，scroll1065.1455，正文／数量区域各1种RGBA。三个阶段全部帧保留且均1600×1240；正常／超时／恢复正文区域各1种RGBA，超时数量区域2种，整窗分别2／4／3种，未丢帧或宣称整窗恒定。证据 `pi-capture-native-038/phase-analysis.json`、各连续目录frames.json／pixel-analysis.json及fault-events.json。

超时截图还暴露窄窗口footer添加“刷新未完成”后把实际会话数量挤成省略号，重试按钮进行中／可用也落入原数量区域：记 `CAPTURED_REFRESH_COUNT_CLIP_001` 失败1轮，不能将这轮判为数量像素稳定已通过。正文不消失的通过边界保留，数量布局待下一候选修复。此问题独立于长标题、原Loading正文闪烁及旧停止hook，不合并累计。

恢复后通过真实DisconnectRevision撤销本根连接，generation1→2、enabled=false；captured读取拒绝，实际窗口问答／工具全部撤下、复制禁用，截图及revocation.json保留。未删除官方10条材料／客户端元数据。撤权提示仍误称“已有材料保留在旧材料入口”，而这批正文只在官方存储，记独立文案缺陷 `CAPTURED_REVOKE_LEGACY_COPY_001` 失败1轮，待改为权限变化／保存内容未删，不得误导旧材料位置。没有新增撤权31秒录制，不扩大通过范围。

GUI正常退出，自有daemon和已恢复运行的官方进程安全停止，stop-receipt.json与原10条材料／回执保留；日常76077／76867仍在。隔离连接现为已撤销状态，后续读取须明确恢复授权，不能直接回写enabled或复活原代次。下一候选还须包含用户指出的技术文案修订、数量布局和撤权提示修复。新链路知识处理／出处、其他Agent、正式迁移及编辑历史等原Goal仍未完成，没有完整运行包。


### Native039：窄底栏与撤权提示修复

连续用例：沿038实际已撤销的同一隔离根，原generation2／disabled状态不改；开发GUI读取真实daemon的拒绝回执，数量与操作在固定48px底栏内于窄窗口分行，权限变化说明不再指向旧材料。正常内容与临时失败仍保留局部重试，数量文本不再拼接失败前缀。宽窗口保持原水平布局；没有改变刷新周期、官方存储或授权校验。使用diagnosing-bugs的已有实际失败证据及定向回归：两条新增测试修改前分别失败（数量标签变化、撤权说明缺失），修复后受影响desktop全包30.443秒通过，vet通过。宽窗口文字容器可随重试按钮缩短，回归验证文本起点／高度不变；窄窗口数量行几何完全不变。没有把测试故意red计为新Native验收失败。

实际800×620 Native039显示“读取权限已变化／已保存的内容未删除，重新确认授权后可继续查看。”，正文撤下、复制禁用、数量完整显示，局部网络重试不冒充恢复授权。连续31.001秒309帧全部纳入分析，目录sequence28→43，始终connection_revoked／0行／无选中；正文与数量区域各1种RGBA摘要。证据`build/conversation-official-20261112/pi-capture-native-039/{source-info,stop-receipt}.json`及`captures`截图、frames、pixel-analysis。原10条官方材料和回执、原同意均保留；只恢复自有daemon供读取拒绝测试，未启动官方服务，GUI退出0、daemon正常stop；76077／76867受保护进程仍存活。未打完整运行包。

`CAPTURED_REVOKE_LEGACY_COPY_001`原失败1次，已在真实撤权状态复验修复。`CAPTURED_REFRESH_COUNT_CLIP_001`原失败1次，源码与定向检查通过；本轮实际只有撤权0行状态，不能替代有正文的临时超时／恢复连续观察，此项Native尚待复验。技术文案“以下工具记录暂未关联到具体问答。”已进入039二进制，但撤权状态没有工具，不宣称已实窗复验。038标题修复与实际超时／恢复证据仍按原范围保留。

### 已采集材料重新授权：下一步影响分析

使用plan-change-impact核对：连接重开后需经过App明确确认，只恢复原saved receipt限定的内容读取；未来采集仍需独立同意，不扩大为历史catalog，不替换原capture provenance。现有`conversationReadCandidateFor`／`PrepareCogneeReadReauthorization`明确排除MaterialSubmission且仅Codex历史路径；`CapturedDeliveries`要求原capture consent.Binding与当前binding完全相同，`piCaptureAuthority`还禁止历史read-review字段。因此现有“重新确认读取范围”不能直接用于pi，不能仅放宽一个判断或回写旧generation来制造恢复。此为当前未完成入口，非官方正文被删除。

实施范围将涉及records中的只读复核intent、Store元数据关联及CAS、daemon精确receipt预检与后置重验、IPC、App明确复核和连接新代次；须保留原consent／receipt不变，不复制正文，也不允许该复核开启采集。先定义元数据关联与失败拒绝测试（旧连接／跨项目／库变化／物理目录变化／并发撤权／重放同一intent），再接Native连续“重连→确认读取→仍为原3session／10receipt→超时与恢复”。现有Codex历史复核不改语义；原stopped hook问题不重跑。当前只完成调用链核对，未实施此授权扩展、未复活旧代次。知识处理／出处、其他Agent、正式迁移切换与编辑历史等原Goal继续保留。


### Native040：明确恢复已采集内容读取与有正文的失败刷新

用户最新分工由本session集中UI，coding-memory隔离验证交给01a1ce8b-e504-78d1-8df6-6e9c439dd609；该session已回报只使用其专属目录，不修改UI或重启本根资源。本项只补齐撤权页面后续可操作流程，没有新增轮次模型、采集器或知识存储。

沿原根通过真实IPC将已撤销connection generation2重新配置为3，没有回写enabled或复活旧代次。schema35新增`pi_capture_read_grants`，只保存复核intent、当前代次与库／物理工作区／固定策略关联；原material binding保持generation1、原capture consent与receipt全部不改。App复用“重新确认读取范围”右栏；pi文案限定已确认保存的本项目材料，明确不启用后续采集、不扩大历史读取。默认Bind与Codex历史复核语义不变；capture仍须独立授权。新代次的凭据刷新只能在当前项目、原binding与原saved receipt的own-user读回核对后更新私有token，不落读取grant；确认操作才会提交元数据，并在网络后重新核验当前代次／物理目录／库等身份。期间撤权不能提交。

普通API／Store测试覆盖未确认不得触网、预览无授权、不同scope／policy／旧代次拒绝、同intent重试一致、另一intent不能覆盖同代次、再次重连需再次确认、物理目录替换拒绝、临时不可用及确认中撤权拒绝；原回执／同意／binding不改、读取不发送POST、不创建正文镜像、不授予history或新capture。受影响store／daemon／desktop／serviceipc／records整包分别56.729／67.881／30.679／0.488／1.688秒通过；其后凭据刷新补充的daemon整包77.624秒通过；store／daemon／desktop相关race通过5.214／10.675／2.204秒，受影响vet通过。停止的旧Codex hook测试没有重跑。

实际800×620 Native040：重连和刷新凭据后仍无法读；打开确认页时read grant=0，点“确认恢复读取”后才落1条generation3 grant，原3session／3QA／7trace实际官方读回通过。原consent／receipt／binding逐表元数据SHA前后完全一致，memories／versions／jobs均0，history与future capture继续拒绝。证据`pi-capture-native-040/{reconnect-intent,reconnect-result,review-before-confirm,review-after-confirm,restored-read-ipc}.json`，没有人工remember、重新生成材料或再跑模型。

正常阅读连续31.001秒309帧，目录50→65、正文28→43；本根官方PID38789实际SIGSTOP为Ts，原正文、3会话及1QA／1trace数量均保留、retry局部显示，随后同PID SIGCONT为Rs。第二段31.001秒309帧包含299帧失败与10帧恢复：在采帧结束前恢复服务，故按“失败到恢复过渡”记，不冒称纯31秒失败态；失败断言与原帧全部保留，没有筛帧。恢复段31.001秒309帧，目录92→108／正文70→86。三段正文、列表数量与阅读数量各只有1个RGBA摘要，且三段摘要完全相同；scroll410.83582／选择／工具展开保持，实际AX搜索焦点不跳。失败期目录72→75／正文50→53受真实15秒超时限制，不宣称10次请求。证据`captures/continuous-*/{frames,pixel-analysis}.json`、静态图与`fault-events.json`。

`CAPTURED_REFRESH_COUNT_CLIP_001`保留原失败1次，本轮有正文、失败和恢复的真实像素检查已通过；`CAPTURED_REVOKE_LEGACY_COPY_001`仍沿039复验记录。工具关联技术文案在040真实展开核对，未伪造QA关联。当前同根新读取grant尚需App／daemon重开保持核对，Native041继续消费；官方服务已恢复，不能在暂停状态留下运行资源。原Goal的知识出处、迁移切换、其余Agent与四稿完整对照仍未完成；无完整运行包。


### Native041：daemon／App重开保持与普通窗口

040 GUI退出0、daemon经stop回执退出，官方服务保持同PID运行；确认daemon旧handle terminal后，显式resume同一schema35开发daemon，再打开1200×820 Native041，未再次复核或发送材料即真实读到原3会话。原捕获内容不是从客户端正文缓存恢复：memories／versions／jobs仍为0，官方精确receipt读取继续，读取grant仍唯一generation3，原consent／receipt／binding元数据摘要不变。普通阅读折叠态和工具展开态静态截图保留，未知时间不填充。

工具展开后滚动169.94678，搜索框保留焦点；连续31.000秒309帧全部纳入分析，目录26→42／正文27→43，正文／目录数量／阅读数量各1个RGBA摘要，无错误、3行及展开保持。证据`pi-capture-native-041/{reopen-metadata,source-info,stop-receipt}.json`及`captures`。普通、窄窗口复用同一冻结二进制与原实际材料，未再次调用模型。GUI退出0、daemon正常stop、已恢复的官方PID38789核对所属配置后SIGTERM并确认退出；受保护76077／76867仍存活，原材料及新schema35关联保留。自有新daemon／GUI消费者已加入依赖保护，不把旧schema34开发daemon当本库的可重开替代。

本次完成已采集材料重连读取及其普通／窄窗口稳定性切片；四稿中的真实知识出处返回、各类Markdown／选择复制的完整矩阵、RuntimeController真实入口整合和旧库迁移／其余Agent仍须按原Goal核对。后续按用户分工集中UI；coding-memory可用性及官方整理／召回由独立session报告，不能用其意图替代实际成果，也不把本切片标为完整Goal完成。

### coding-memory隔离session中间回报

01a1ce8b-e504-78d1-8df6-6e9c439dd609回报：其专属`.local/coding-memory-codex-20261112`中使用官方插件1.8.0与Draft PR6568 ec6f408服务源码，正常Codex hooks信任后真实编码，自动采集1QA／8trace；首次临时模型适配Schema不兼容已留证修正，第二次improve仍在生成typed lessons。本条是该session的中间回报，尚未由本session核验其最终产物，不等同于当前1.6.7服务升级或知识出处UI已通过。本session不重启、配置或修改其专属资源。

用户最新明确无需Token相关测试、不必考虑Token消耗，本轮验收据此取消该项。对方已在源码发现State-Key可能直接删除superseded旧知识，正于合成库验证；此行为与现有覆盖／删除前确认规则存在冲突，暂不接到已有知识，不擅自扩建另一套知识系统。待最终质量／出处／替换行为证据返回，再以真实官方产物验证知识与来源UI。


### Native042／043：固定阅读标题、角色与单条复制

重新实际打开四张02f038f主稿，按当前用户UI分工集中处理。旧Native041长文滚动时标题／提示一起离开窗口，与固定header要求不符，独立记录`CONVERSATION_HEADER_SCROLL_001`失败1轮；不并入旧长标题截断、数量或撤权计数。将标题、记录信息、来源范围提示移到Scroll之外固定Column，消息／工具保留独立Scroll及原稳定Key／ScrollState。普通与窄窗口延用30／20px边距、21／19px标题、56px工具栏和48px底栏；窄窗口记录信息只留有明确Label／Tooltip的图标。用户及Agent补20px单色角色图标，列表增加选中竖条和上游真实Agent名称，未填充日期或生成新标题。

用户与Agent行各加单条原文复制，后台刷新后仅当前有效view／detail可复制，不把另一条消息或渲染后的Markdown转成原文。仅e2e显式synthetic门禁增加系统NSPasteboard比对菜单，只保存长度／SHA／是否一致，不写正文。Native042对实际长问答的用户原文1012字节、Agent Markdown2738字节、首段代码130字节、已载入内容7999字节，四项actual_system_pasteboard=true且matches_original=true；不以Tester剪贴板替代真实系统证据。

标题回归首次误匹配了中栏同名行，不能抓住阅读标题问题，已纠正为唯一“记录信息”控件；受控复现header位于Scroll内时800px坐标{746,121.77734,14,20}→{746,56,0,0}，断言实际失败，恢复固定header源码后通过。此为夹具选择器纠正，不计新产品验收轮。MyGo初次编译使用不存在的JustifyContent，按锁版本源码纠正为Justify，没有修改工具链。实际042长文表格、代码和工具滚动时标题／提示仍固定。连续31.001秒309全帧，目录115→130／正文118→133，scroll2855、工具展开与搜索焦点保持，固定header／正文／数量各1种RGBA摘要。证据`pi-capture-native-042/captures`中的clipboard回执、静态图、frames与pixel-analysis。

042工具标题仍居中，独立记`CONVERSATION_TOOL_HEADING_ALIGN_001`失败1轮；仅设AlignItems不能改变按钮主轴居中，修正Justify(Start)与子Row Grow。043实际800×620已靠左，并将“工具未对应单条问答”的解释放到记录信息，默认工具组只显示会话工具范围，不伪造逐轮关联。普通宽度的此项本轮为布局测试，最新实际左对齐证据限043窄窗口。

### Native043：呈现官方已确认的截断标记

042读取官方精确receipt后，核对原metadata确认两条trace的params_truncated=true（其他所查标记false）；当前SessionTrace归一化丢了这些字段，页面没有提示，独立记`CAPTURED_TRUNCATION_LABEL_001`失败1轮。现在只从已经通过operation／official ID／原payload digest核验的context.source／memory_context.source投影bool到内存records，不新建持久字段、索引或补齐正文。UI对应显示“参数／结果／错误 · 已截断”及明确为true的问答片段提示；未知不猜。原始问答旗标false，不能仅凭句子末尾样式推断截断。

043真实窄窗口可见两处“参数 · 已截断”，记录信息增加当前可信binding的工作目录。连续31.001秒309全帧，目录100→116／正文105→121，scroll3065.4412／选择／展开／搜索焦点保持，header／正文／数量区域各1种RGBA摘要。证据`pi-capture-native-042/truncation-metadata.json`和`pi-capture-native-043/captures`。desktop／daemon／records全包30.888／64.822／1.176秒通过；定向race desktop3.862／daemon5.292秒通过，受影响vet通过。新字段只作已验证数据的展示适配，未改采集、官方SDK或数据库schema。

尚未通过：工具command字符串仍通过JSON.MarshalIndent展示，换行与尖括号变成\n／\u003c等，长命令阅读困难；043真实截图确认，独立记`CONVERSATION_TRACE_PARAM_ESCAPES_001`失败1轮，待将实际string值以等宽、独立横滚呈现，同时保留复合JSON与原始复制范围。不能把header／截断修复描述为四稿完整通过；代码横滚与跨消息选择仍需完整矩阵。新GUI与daemon保留供后续UI消费者，own GUI退出0，daemon及官方服务已安全停止，日常76077／76867仍存活；未构建完整运行包。

独立coding-memory session最终报告及草案来源已落[验收](coding-memory-codex-validation-20261112.md)与[来源分析](../research-coding-memory-origins-20261112.md)。本session已读其最终结论：实际采集／知识／注入存在，完整契约与纠错召回质量未通过，state自动去旧是上游有意设计但与现有App确认规则冲突；不把Draft等同方向无效，不把中间回报当已接入当前UI。当前不开正式迁移、不自建替代提炼系统；后续继续完成独立UI及已授权范围，完整Goal仍未完成。

### Native044–048：工具原值、跨消息选择与完整横向阅读

`CONVERSATION_TRACE_PARAM_ESCAPES_001`原失败1轮保留。工具字段不再将string值直接JSON转义展示：参数对象按键分列，实际字符串保留换行／尖括号／反引号，以11px等宽文字及独立横向Scroll显示；复合值仍用JSON且保留准确数值。ScrollState只保存在当前阅读视图内，以trace／字段／参数键稳定身份关联，不增加数据库正文副本。原文复制仍使用原始SessionDetail，不复制格式化展示结果。新增回归检查先复现旧转义问题，再验证字面字符串、9007199254740993准确值及临时刷新错误不重置横向位置。

044实际1200×820、045实际800×620。首条真实pi问答中，实际选择正文并执行系统复制，跨用户／Agent的450字节选区包含问题及回答，排除固定标题；044连续31.001秒309全帧前后实际NSPasteboard SHA相同。另一段044展开工具、scroll_y=3156.816、横向X=10，在31.001秒309全帧及16次刷新期间保持；这是部分横向偏移证据，不据此声称已能读到行末。044实际点击“复制已载入内容”后，系统剪贴板7999字节与原文一致。证据`pi-capture-native-044/phase-analysis.json`及044／045的clipboard回执。045没有新的31秒录制，不用静态图代替连续观察。

046／047沿同一库、同一3会话和10条已确认回执，以同一冻结二进制验证800×620／1200×820。通过实际拖动横向滚动条，046 Markdown代码可读到SQL尾部`WHERE id = 1")`；两尺寸的首条工具command均可读到`&& ls -la && python3 --version`。046新增横滚区AX标签、仅e2e的MaxX诊断，未修改上游材料或轮次归属。两段连续记录均31.000秒309全帧，header／正文／列表数量／阅读数量各1种RGBA，目录及详情各完成16次刷新；scroll_y分别2029.4412／1708.9524，工具展开保持。工具字段X始终等于MaxX，分别491.57715／215.57715，证明行末位置在刷新后保持，未筛除帧。证据`pi-capture-native-046/047/captures/continuous-*/pixel-analysis.json`。实际截图分别为046 `081840`（代码行末）、`081926`（工具行末），047 `082139`（折叠首屏）、`082202`（展开）、`082324`（工具行末）。

047查询`no-matching-session-047`显示“未找到匹配会话”，右栏仍可读原会话及原横向位置，清空查询恢复3行；静态证据`082502`／`082531`。当次复制探针只读剪贴板而未点击实际复制按钮，回执为81字节、matches_original=false，保留为验收方法错误，不能用044通过结果覆盖。048用同一二进制／库，先明确点击实际“复制已载入内容”按钮，再调用只读探针；实际7999字节、SHA `0f2e5fb2904a0b9f73197827fcfb6b2c89a67966653776ed569af0d582917b5e`与原文一致。没有为此修改产品代码或增加复制缺陷计数。此前口头将044成功代入047的表述已纠正。

其他方法边界保留：macOS scroll不支持pixels参数；CUA坐标是截图物理像素，不是屏幕全局坐标；早先错误坐标未命中窗口。045曾收到外部窗口变化提示并进入旧材料路由，原因未知，后续通过正常知识／原始记录页签回到同一材料，没有重种库或重跑模型。元数据首次摘要序列化使用紧凑JSON，与040基线的默认分隔符不同；统一原算法后consent／receipt／binding三表SHA均与040一致，见048 `original-metadata-comparison.json`，不是业务数据变化。

当前client memories／versions／jobs仍各0，receipt10、consent2、read grant1，数据和运行依赖保留。044–048 GUI均退出0；自有daemon正常stop，自有官方服务确认退出，日常76077／76867继续运行。官方终止句柄241对应本轮SIGTERM，并有退出时12个semaphore清理警告，不能据此声称资源计数为零；没有因此重启服务。收尾见048 `stop-receipt.json`。没有新模型、Token测试、完整运行包或正式数据切换。

046／047完整横向阅读与连续稳定性切片已通过；四稿整体、真实自动采集后知识整理→出处→返回、正常RuntimeController入口整合、其余Agent及正式迁移仍未完成。独立session本轮新增`docs/coding-memory-codex-spec.md`，是规格草案，非实施授权或自动替换／只读产品规则已生效；本session不改其模型通道或运行环境。

当前源码desktop／cmd/memlane-app完整测试31.214／1.052秒通过，Conversation定向race3.049秒及两包vet通过；最后git diff --check通过。048 `checks.json`记录命令、相关源码SHA和046／047／048二进制一致性。旧Codex hook桥接停止项与包含它的全仓测试没有重跑；不将受影响包通过写成全仓通过。
