# coding-memory 正式接入进度

本记录对应 [规格](../coding-memory-codex-spec.md) 的既有实施，沿用已有界面；当前开发已暂停。开发阶段不打完整包，不导入历史或迁移旧知识。旧交接和全部失败计数保留；当前工作不恢复已停止的自有 Codex hook 探测、README 实体生成或 helper 同路径更新实验。

## 当前状态与交接

**开发已于2026-11-12按用户要求暂停。** 恢复须用户明确指示，不自动继续。当前统一入口为[暂停交接](coding-memory-handoff-20261112.md)，最新范围为[移除权限管理](remove-agent-permissions-20261112.md)：所有Agent默认读写，旧的权限实现／计划待全量清理，不作为后续要求。

| 目标 | 最新实际结果 | 未完成 |
| --- | --- | --- |
| coding-memory主流程 | 正式Schema、处理配置与生命周期基础已有源码和协议证据 | 官方插件日常配置、真实采集／模型／知识／新会话复用 |
| 组件与日常切换 | v13候选曾构建／并排准备；v14配方／源码继续变化 | v14未构建安装；日常备份／切换／恢复未完成；旧helper停止边界保留 |
| 官方图展示 | 006–008官方payload、本地前端与真实WKWebView合成图运行 | 现有项目页、真实官方数据、来源跳转与完整Native验收 |
| 官方代码图 | 011–013真实Enola及官方SDK合成写图／刷新／删除验证通过 | daemon调度新代码未集成验收；有界查询、Codex使用及日常项目未完成 |
| 移除权限管理 | 014完成第一批中央读写／项目访问管理／来源脱敏与连接页清理 | 遗留字段／表、采集与历史、自动处理、其他入口、文档和全量验收 |

以下001–013为各阶段当时的实现与证据，涉及权限的旧方案已被新用户决定替代；保留失败记录不表示保留其产品能力。完整目标没有缩减，也没有完成。

## 001：将临时 Schema 兼容纳入正式 Codex 通道

已证事实：封存 `build/coding-memory-codex-20261112/server_run.py` 在临时 Codex exec 调用前移除 Schema default、关闭对象的额外字段并补全 required；正式 `RunCodexCompletion` 原先原样发送 Schema。封存的五类真实官方请求（CodingAgentContextExtraction、CodingWrittenLesson、CodingCuratorBatchOutput、KnowledgeGraph、StringOutput）包含有默认值和未显式关闭额外字段的 Pydantic 对象，原先只有简单探测 Schema 能证明生产通道可用。这里是原有临时 Schema 适配问题的生产兼容补齐，原失败1轮保留；本次没有重跑真实模型验收。

正式实现：

- 只遍历 Schema 位置，保留 `$defs`／引用、nullable、数组、枚举和约束；去除 default 注解、将有限对象的声明字段全部设为必填并关闭额外字段。名为 default 的字段、const／enum 数据不被当作注解删除；整数不经 float64 舍入。
- 不把开放字典改成空对象；显式开放的 additionalProperties 或没有字段定义的开放对象在启动模型前以 `codex_schema_unsupported` 拒绝。此边界不表示支持任意 JSON Schema，也不引入普通 JSON 降级。
- 正式 app-server 发送转换后的 Schema；返回同时经过原始 Schema 和实际请求 Schema 校验。回执分别保留两者摘要，不持久化完整 Schema、提示或结果。
- Codex adapter dependency 升为 `memlane-codex-completion-v2-strict-schema`，已有旧验证不能直接沿用；后续日常切换需重新进行真实结构化验证，切模型仍需用户明确启用。

用例先明确为：五类封存真实 Schema 可转换且解析；嵌套／引用／默认／nullable 保留约束；开放字典启动前拒绝；Pydantic 默认字段经正式协议传入；原有取消、迟到结果拒绝、工具隔离、配置变化和进程回收不回归。源码和协议测试不等于真实 Codex／Cognee 模型验收，也不证明知识质量提升。

Schema-only 夹具及原始 SHA 在 `internal/agents/testdata/coding-memory-schemas-ec6f408/manifest.json`；来自合成项目封存请求，无提示、响应、凭据或真实项目正文。测试回执目录：`build/coding-memory-production-20261112/schema-001/`。五类真实 Schema 和正式协议定向测试通过；processing 全包测试、race 以及 agents／processing vet 通过。最终源码摘要和命令退出码另存该目录 `checks.json`。

连接前置核对：当前官方插件管理与 processingCandidates／ResolveCodex 共用既有电脑级连接记录。插件安装会拒绝同 profile 仍启用的旧 CaptureConsent，但不替换原连接、账号或模型配置；停用旧采集不等于必须删除 MCP 安装记录。本轮未证明这一共存方式的日常运行结果，也没有证据要求另造连接系统，保留现有连接路径继续集成。

资源归属：本轮仅源码、Schema-only 夹具及小型测试回执；未启动真实模型、GUI、官方服务或代码扫描，没有更改日常数据、Agent 配置或运行依赖。开始时实测可用124 GiB，测试继续通过 task.py 门禁。未构建完整包。

下一步仍须接通官方自动整理到受管 daemon 的正式授权与运行生命周期，再验证日常两项开发任务及图谱；本轮不宣称 CM02／CM04 整项通过。

## 002：自动处理范围授权与官方账号核对

上轮属于源码实施和已执行验证的进展。本轮继续正式自动运行的前置授权，没有缩小 CM／CG 或关系图范围，也未恢复历史停止项。

先核对固定上游 ec6f408 的实际入口：官方 `get_improve_router.py` 以已认证 user 调用公开 SDK improve；SDK 保留 dataset／session 锁、后台任务和阶段结果。`modules/improve/admission.py` 的官方准入仅用于 remember 的自动分支，明确不约束显式 improve；Codex 插件退出／冷却直接请求 improve，不能仅注册该准入检查就宣称已受管。GitHub tree API 本次返回403限流，随后通过 codeload 获取固定归档并与原封存 SHA `6e11047ef690efedac06653c67a11bea0c0e0a8c6af0c89dad634574f4bc01bc` 一致，未改用另一个版本。源码仅解压到 `.cache/coding-memory-source-ec6f408/`，未 import SDK 或启动运行环境；该输入供接下来正式适配消费。

实施前用例：无记录默认关闭；连接／绑定不隐含自动处理授权；开启须为当前精确绑定并核对官方账号；拒绝别的端点、binding operation、工作区、原文提交型绑定及账号；重复操作只回放历史回执，不能在关闭后重放开启；代次竞争拒绝；断连／重连不复活旧许可；服务不可用时仍可关闭；重开保留明确许可；schema35迁移保留备份、不创建默认启用、不生成内容或模型任务。

本轮实现：

- schema36 只新增 `coding_memory_grants`，记录精确官方 binding、官方 user、授权代次、启停和时间；操作回放复用既有轻量 `processing_operations`。没有会话／知识正文、重复索引或模型 Key。
- `coding_memory_grant_change` 私有 IPC 明确要求 UserConfirmed。开启时 daemon 从已绑定端点、既有凭据调用新的官方 scope 探测，核对服务 instance、dataset、官方认证 user，再重新核对绑定和连接代次后写入。scope 探测只做官方 dataset 授权检查，不读取会话／文档正文、不启动模型。关闭不依赖服务在线或连接仍有效。
- `AuthorizeCodingMemoryGrant` 核对精确当前许可、绑定和连接，用于后续自动运行开始、活动监控和结果发布；当前还没有将它描述为实际后台模型取消已经完成。旧 binding／旧许可即使重连也不能直接复活。
- 新增 serviceipc Library 方法供现有项目设置／控制复用；本轮不修改已有界面。新请求默认不被自动发出；日常启用和新服务 scope 探测均未执行。

验证与失败归类：`build/coding-memory-production-20261112/grant-002/`。首轮定向 grant 测试与 Python ASGI 7项通过；扩大 store／session 回归发现旧迁移夹具用当前数据库模拟旧版本时，未移除新 schema36 表，故重复建表。该准备方法问题1轮，原 `store-test.log`／`race.log` 保留，不计为自动处理／知识质量又失败，也不修改已通过的旧历史结论。仅在相关18个旧版本夹具的 DROP 列表前加入新表清理，使模拟状态真实对应旧 schema，未使用 IF NOT EXISTS 掩盖迁移错误。正式 schema35→36 独立测试覆盖预升级备份与默认关闭。

最终检查：store 全包通过（53.785s）；授权／session processing 的 store 与 daemon race 通过；受影响三包 vet 通过；Python ASGI 7项通过。命令与源码摘要见同目录 `checks.json`。这些证据不替代真实自动触发、模型或 Native 验收。

当前状态：持久授权、官方 scope 探测与 IPC 已实现；后台自动事件到 daemon 的受信交接、运行归属、固定处理配置、活动取消／停用和正式插件／服务配套切换仍未完成。没有开启任何日常项目、运行真实 improve／模型／扫描或更改现有 Agent 配置。新增 migration 仅在本轮合成测试库执行，不能写成日常库已升级。

## 003：官方触发转接、受管运行与撤权终态

002为已实施且已验证的进展。本轮沿同一规格继续；保留前两轮源码、回执及原有失败计数，不操作日常服务或已有界面。

进一步核对配套插件 a176248 的 `_plugin_common.py`：`improve_session_via_http` 实际发送 dataset、单一 session_ids 和 run_in_background=true，退出／冷却使用这一入口；2xx是提交接受，不是知识已生成。上游没有在提交后轮询。源码取自固定 raw URL，摘要入本轮 checks，不能把 SDK admission 当这条 HTTP 路径的门禁。

实施前连续用例：无服务凭据拒绝 → 有凭据但无项目授权拒绝 → 明确开启合成范围 → 官方触发且由 daemon 自行确定项目／原文摘要／当前模型 → SQLite固定范围与处理配置 → 官方内部 improve／任务网关 → 返回真实阶段终态 → 同操作回放不重复执行 → 下一次活动请求中关闭项目授权 → 实际取消官方 HTTP 与网关 → 旧成功结果无法发布 → 新触发拒绝。单独覆盖手动请求伪造自动标记、其他账号读取回执、内部调用不递归、仅观察失败不重新提交及已知部分产物保留；无模型配置的完整前置拒绝与App状态仍需后续连续场景。

本轮实现：

- `coding_memory_trigger`／`coding_memory_task` 为受管服务私有 IPC，要求每次 daemon 启动轮换的随机能力凭据；host文件位于服务根的 `coding-memory-owner.json`（0600），普通项目连接与模型输出均不能生成凭据。触发仅传 operation、服务／dataset／账号／session；项目、授权、真实会话摘要及处理选择由 daemon 查权威来源。普通手动整理 IPC 拒绝调用方自填的 automatic_grant。
- 自动运行复用 sessionProcessingOwner、既有五分钟任务所有权、processing配置冻结、模型槽位／网关和官方内部 improve，不另起一个提炼器或任务重试循环。自动授权快照随既有轻量任务持久化；开始、500ms活动监控及终态核对当前代次。最终成功 UPDATE 同时匹配有效授权行，避免撤权与迟到成功回执竞态；失败仍保留真实官方阶段回执，不宣称事务回滚。
- 新 `cognee_coding_memory.py` 在显式配置 processing_owner 时统一转接官方公开 SDK 导出，覆盖实际 REST router 和 remember 动态导入的入口。后台提交返回真实SDK的running结果并注册SDK后台观察任务；其wait和序列化仍用官方类型。观察丢失不表示daemon已停止，也不提交新operation。未知语义选项、无明确账号、非单一session先拒绝，不静默改变请求语义或降级模型。
- daemon内部调用通过context-local owner_execution进入未改写的官方原流水线，保留官方dataset／session锁、水位、抽取、提炼和存储。自动调用使用官方global-context／conflict默认设置，手动旧行为保持原边界；未重写阶段算法。该标记不来自HTTP／模型输入。
- 配置受管转接后，内部 `/memlane/session-improve` 与cancel同时要求daemon的私有身份，不能只持官方API身份冒充owner；凭据仅传递，不进入任务回执／幂等摘要。scope探测返回managed能力状态，尚未配置受管通道时daemon拒绝启用项目自动授权。
- 源码复核发现旧scope探测及手动授权调用的通用 `resolve_authorized_user_dataset` 具有“缺失时创建”语义；改为官方 `get_authorized_existing_datasets(..., 'write', strict=True)`，仅查已有范围。此前纯夹具未证明真实SDK的这项副作用；本轮没有观察到日常数据因此被创建，不虚构实际损坏。后续授权探测不再使用创建型解析器。
- 官方Settings增加processing_owner路径，组件builder纳入新模块和源码摘要核对；本轮仅提供启动接点，尚未给日常配置写入该路径，也未构建组件或完整运行包。

证据目录 `build/coding-memory-production-20261112/trigger-003/`：

- 实际SQLite／loopback HTTP／生产网关、合成SDK及模型响应的daemon/store定向race检查通过；包含自动任务的scope固定、回放、防伪、活动撤权、清理和迟到结果原子拒绝。合成响应不冒称真实Codex推理。
- Python session处理与身份ASGI 8项、转接器／真实Unix socket协议3项通过；包含私有身份、权限文件、回执身份、公开入口统一、内部不递归、背景观察、部分产物与观察失败不重新提交。
- `verify-cognee-managed-improve-contract.py` 在独立日志／HOME的子进程里实测本机Cognee 1.6.7的真实ImproveResult、StageResult、后台注册／wait和序列化。owner及原pipeline为明确合成替身，未运行官方真实流水线或模型；不能把这个结果外推为ec6f408配套运行环境已升级或自动采集已通过。
- 受影响daemon／store／serviceipc／official静态检查通过；最终命令、退出码、源码及固定上游接口摘要见checks.json。没有增加真实模型、知识质量或旧停止问题的验收轮次。

本轮资源只包括源码、固定公开输入、小型回执与自动清理的测试根／socket；最后实测125 GiB可用。日常memlaned与Cognee完整组件、数据和引用依赖保持原状。

尚未完成：把受管路径接入真实启动配置；配套ec6f408服务与a176248插件的组件输入和能力版本核对；日常一致备份与可恢复切换、自动生成知识范围核对及旧人工知识保护；现有项目设置内的授权／运行反馈接入；官方真实退出→选定Codex→知识产物→新会话复用及取消矩阵；原规格代码图谱、官方Web承载和实体／知识关系浏览。CM03／CM04／CM09不因本轮合成链路通过就标整项完成。

## 004：固定组件候选与启动配置衔接

003是源码实施与已执行检查的进展。本轮继续固定组件输入、实际候选构建和启动路径；未进入完整App包／release，不替换当前日常服务。

实施前连续场景：固定归档及源码校验 → 不改共享Python环境地选择固定SDK → 依赖版本核对 → 服务组件编译／资源及嵌入模块检查 → 配套插件归档 → 逐文件与链接验证 → 未配置daemon的实际候选在访问数据库前拒绝 → 启动配置只接受匹配组件且绑定明确daemon根 → 实际握手还须核对SHA／能力，不把1.6.7版本字符串当新功能证明。下一阶段再做停止写入、一致备份和实际日常切换，不能用新试用环境代替。

已实施：

- `engine/coding-memory-components.json` 固定官方ec6f408服务／a176248插件、两份归档SHA及组件版本；`coding_memory_recipe.py`供builder和packager共用。官方SDK三组包（cognee、cognee_db_workers、kuzu）及其模板／迁移资源均来自校验后的原始归档；缓存中被修改、插入额外源码或植入父目录symlink时拒绝。没有覆盖共享`.local/cognee-python`的SDK或默认Python。
- 服务builder增加显式`--coding-memory`模式，旧模式继续保留。通过固定SDK的全部核心依赖约束及源码选择检查后，用独立构建缓存和日志根编译；检查3343份官方源文件、冻结模块、原生JSON扩展代码／依赖／签名。实际产物包含coding turns、distill、lesson store和全部Memlane转接模块，构建前后摘要一致。
- 新组件携带受校验的公开构建契约，启动时要求明确的processing_owner，开启官方coding模式。这个组件不把旧settings中的LLM路由作为默认模型通道；当前Controller以干净环境启动，模型请求通过daemon的context接入。旧model配置留在设置中供旧组件回退，未删除或静默改用其他供应商。
- packager增加同名显式模式，仅归档变更的服务与插件；不重复归档／下载已有BGE模型。服务必须已有匹配冻结契约，插件必须是固定1.8.0源；保留第三方配置及旧组件，不执行实际插件安装。
- Controller新增明确的ServiceRoot；现有初始化控制器只增加这一条后台路径注入，页面布局和交互未重做。新组件配对通过后才设置processing_owner；旧服务仍活着且将要切换组件／owner配置时，在改写切换设置前拒绝，留待一致备份后的受控切换。新coding组件不启动旧独立模型桥，模型验证沿用“设置→记忆处理”。
- Controller与自动任务的运行时握手均要求固定服务SHA和coding format／trigger能力；只报告旧1.6.7／session_improve的服务不满足自动运行入口。静态marker与实际服务握手分别检查。

候选与证据：`build/coding-memory-production-20261112/component-004/`。

| 候选 | 实际版本 | 归档字节 | 解包字节 | 本轮证据边界 |
| --- | --- | --- | --- | --- |
| Cognee服务 | 1.6.7-memlane.13-coding-ec6f408 | 367294861 | 906443663 | 组件编译／冻结／资源检查及启动前置拒绝；不是日常服务已可用 |
| 官方Codex插件 | 1.8.0，a176248 | 376328 | 1237845 | 原始固定源归档与文件／链接完整性；不是安装、信任或采集已通过 |

服务归档SHA `ccedc80788f9f063eab0e7105f004de9d97e7a82cb47fbc840251d23cd484a84`，插件归档SHA `d029d1471244eb9b88776e5b3ee3ec5a7c0795a46374a6ce353390d2ebfe7aba`。这些不是主下载包大小；已有嵌入模型复用，尚未重测真实首次下载总量和最终用户磁盘占用。

实际执行的无owner探针：运行刚编译的独立服务组件，返回明确exit2；`runtime_root_created=false`，在SDK／数据库启动前拒绝。该探针只有短命私有配置，没有另建Cognee运行环境、知识库或调用模型，不代替正常启动／四类存储／真实处理验收。

源码／协议检查：official完整包测试、配方Python2项、匹配组件／自动session任务的official及daemon定向race、official／daemon／desktop静态检查均通过。记录见同目录各log和checks.json；没有新增模型质量、旧hook或helper停止问题的验收轮次。

资源与运行边界：归档逐项核对12154份服务文件、60份插件文件及29条服务内部链接，路径未越界。归档完成后移除本轮专有的stages复制树；未来消费者读取归档，不引用stages。保留一个新服务候选及归档供下一阶段消费；构建缓存保留到正常实际运行验证确认依赖后再清。旧完整包、当前PID76077／76867及其依赖未替换、未停止、未清理。

当前仍未完成：签名开发源与原BGE descriptor的组合、日常基线／项目归属选择、一致备份和恢复核对、实际daemon与Cognee切换、官方插件电脑级配置／信任／无双采集、项目授权控制接入和全套CM连续验收；代码图谱与关系图全部原要求继续保留。已向用户异步询问首轮日常项目，答复前继续独立实现，不据此自动开启代码扫描。

## 005：日常根中的并排组件与一致快照门禁

实际运行根仍为 `initialization/client-responsive`，daemon76077、旧Cognee76867；schema31。新组件仅并排准备，未激活、未升级日常库、未安装插件或开启项目。没有改已有界面，也没有构建完整App包。

连续检查范围：组合固定服务／插件与原BGE描述 → 新签名源预览不覆盖旧缓存 → 在现有组件根并排准备 → 核对运行配置不变与BGE零下载 → 合成停写数据制作全层快照并验证 → 真实运行根仍有写者时明确拒绝快照。最后一步为实际拒绝证据，不是实际日常备份／恢复通过。

已实现与证据（`build/coding-memory-production-20261112/staging-005/`）：

- `Manager.PreviewFeed`共用签名、兼容性和授权描述检查，不发布源缓存；`Controller.StageCodingMemory`只准备配套组件，不保存运行设置、不打开数据库、不启用采集。签名有效／无效、缓存原字节保留和已有资源复用均有定向测试。
- 新开发签名源位于 `.local/coding-memory-delivery-20261112/source-005/`；独立进程持有签名密钥，仅公开公钥，非正式分发。原BGE归档从既有初始化archives引用，没有复制模型。已在实际官方组件根准备v13服务与1.8.0插件；下载合计367671189字节。BGE1.5复用已安装组件，下载0字节。服务配置、旧feed-cache和service.json前后摘要完全一致，详见staged-components.json。
- 新 `CreateUpgradeSnapshot`／`VerifyUpgradeSnapshot` 为升级用离线全层快照原语，不替代现有64MiB客户端手动备份。包括客户端SQLite／WAL、必要客户端状态、官方Cognee整个数据根、官方配置／状态／源缓存及明确指定的单个Agent配置；不枚举Agent HOME／聊天历史，不复制引擎和模型。资源保留引用单列，不能据引用认定旧完整包可重启。
- 快照要求三个实际写者锁可独占，并在复制前后通过macOS lsof观察存储打开文件；锁未知、观测失败／截断／stderr、外部进程持有文件均拒绝，不把未知当零进程。每个观测根须出现本进程持有锁文件的具名fd哨兵。源与目标禁止越界／链接，目标私有且不覆盖；复制前后比对完整目录、字节和摘要，持久化后最后发布manifest。失败保留私有不完整目录，不能当成功快照。Verify只证明字节和清单一致，尚不证明实际恢复或旧引擎兼容。
- 合成真实SQLite／WAL关闭后完成快照、不可变读取与全部文件校验；覆盖篡改、现有目标、路径／父链接、锁冲突和lsof未知输出。实际日常根负向探针得到 `upgrade_writers_active`，`destination_created=false`；没有停止任何源服务。详见live-snapshot-gate.json。

失败留痕：首次upgrade-tests.log为Go测试临时子目录默认0777与本门禁私有父目录要求不符，夹具修正1轮；第二次upgrade-tests-final.log为macOS lsof实际始终带f记录、解析器未接受，适配缺陷1轮，实际格式探针后修正并要求具名fd哨兵。两项触发及标准不同，不能合并或计为知识质量／helper又失败。后续upgrade-tests-lsof.log、components／official全包race、vet均通过；错误分类小修后的定向结果另见upgrade-errno-final.log和checks.json。旧模型质量2轮、Schema1轮及已停止问题计数全部保留。

资源：新候选、归档、原BGE归档已经加入显式依赖保护；签名源保留供下一阶段消费，不清理仍引用的旧组件／包。实测剩余122GiB，执行继续受task.py约束。新增快照测试仅使用合成数据，真实日常快照尚未制作。

切换前提尚未成立：旧后台完整包曾被误清理，当前运行不等于重启／回退可用；原helper同身份更新／同路径恢复已超过停止阈值，不能以本次升级名义继续该问题。已向用户明确询问是否恢复该问题处理，答复前保留旧服务并推进独立源码；不因新operation、开发前台daemon或新组件版本重置原次数。首轮日常项目选择也尚待答复。CM12、实际启用／模型／自动处理／Native及全部CG验收均未因本轮门禁通过而标完成。

随后向用户解释停止旧服务可能无法重启，建议保留旧后台继续独立源码；用户回复「ok，给我说一下大概进度，占比是多少」。按此继续保留，不视为恢复helper问题的授权。已答复按完整可用／验收粗估35%，这是工作量估计，不能当验收通过率；真实主闭环仍未完成。

## 006：官方可视化payload的授权读取入口

本阶段复用plan-change-impact核对图入口的上下游：官方SDK取图／预处理 → daemon项目授权 → 轻量IPC → 后续官方Web组件／Native承载。先做读取与撤权边界，因为画布不能接旧Memlane投影DTO或未核对范围的nodes／edges。保持已有界面，不实现新绘图器、不提前扫描；首轮项目选择和服务切换答复不阻塞本阶段源码。

固定ec6f408源码核对：公开visualize/json调用 `fetch_visualization_data`，当dataset查权结果为空时可把None继续交给数据库上下文；本轮仅证明该源码路径存在，不宣称日常发生过越权。新增薄入口先严格取得唯一、精确且已有的Dataset，直接复用同一路径的官方 `fetch_dataset_graph_data` 和 `build_visualization_payload`，不允许None／datasetless回退，不改布局／关系分类算法。读后再次检查dataset身份和owner；SDK错误只给泛化错误，不返回存储路径或材料。

连续用例：明确已授权项目／service／dataset → 固定SHA和新能力握手 → 只读有界官方子图 → 保留原nodes／links／color_maps／stage／provenance及未知扩展字段 → 回读身份及项目授权 → 返回；另查空授权、其他dataset、断连竞态、服务更换、悬空边／重复ID和无能力服务。子图始终标为bounded_subgraph，不能据节点数量称知识数量或完整图。

实现：`engine/python/cognee_visualization.py`增加只读受认证端点；`VisualizationReader`使用现有无代理／无重定向／8MiB运输与私有凭据，要求ec6f408及 `official_visualization_v1`，读取后再核对身份；daemon复用现有历史读取授权并在HTTP后重新核对完整binding，serviceipc新增typed入口。查询只允许1000以内节点、16以内显式seed和固定2跳；不提供任意数据库语句、向量query或模型路径。原payload保留，不写客户端全文／图镜像，不赋予代码扫描许可。

验证目录 `build/coding-memory-production-20261112/visualization-006/`：Python合成SDK／真实ASGI两项通过，覆盖精确Dataset、空结果不回退、撤权及owner变化、错误不泄漏；Go官方reader与daemon真实SQLite／loopback HTTP的定向race通过，覆盖无授权无网络、读中断连不发布、第三方字段保留、数据／索引／扫描表不新增；三包vet通过。官方SDK数据库／预处理器在这些测试中是明确替身，不把合成链路当真正图谱、官方Web或Native验收。具体退出码与摘要见checks.json。

候选边界：004–005已构建／并排准备的v13仍是原冻结字节，**不包含006端点和新图能力**；没有覆盖同版本不可变组件。后续源码冻结后需使用新组件版本构建，并分别检查运行握手及实际SDK图数据。读取尚未接入页面或Agent工具；知识／代码dataset边界、代码主动开启与索引门禁、官方Web静态资源及事件桥仍待接入和真实验收。原README语义图生成第4轮停止项没有重跑；本轮无模型、扫描、用户数据、后台启停或完整App包变更。

## 007：固定官方画布的局部静态资源与浏览器实测

006构成源码及协议进展。本轮按plan-change-impact继续消除官方组件能否独立承载的未知：固定前端8853378（与SDK ec6f408分别记录）→收拢Canvas／面板导入闭包→保持官方源字节和样式→注入有范围的官方payload→真实浏览器绘制／选中→清空及迟到拒绝；不改变已有Native页面，不以新绘图器替代官方组件。

实现位于 `web/official-graph/`。固定上游归档SHA `a6e1aed78a9272a660862757fe334fc182ddb3dde80ddcb5f820108d5f3f3ccf`；42份官方模块保留原字节与Apache许可，包含Canvas、模拟、相机、命中、代码节点归一化、节点／来源面板和路径功能。build逐文件核对摘要，拒绝实际导入的未列入manifest官方文件。React19.2.4／Mantine8.3.14／d3等采用该提交的实际锁文件版本，而不是将package.json范围当精确版本。独立pnpm锁文件不改变根Go／Python环境。

薄宿主不接官方认证、租户或后台请求；页面仅接受宿主注入的当前项目／service／dataset／epoch及官方payload，CSP禁止fetch和外部网络。apply只接受更新epoch，clear同步撤下Canvas和面板；同一范围刷新保持组件／相机及仍有效的选中ID，换范围重建。实体选择仅回传范围和node ID，不能让网页指定路径／URL；实际MyGo可信页绑定、授权复核和来源导航尚未实现，不能因有JS调用名就宣称Native事件桥已经完成。知识／代码dataset边界及代码索引仍独立待做。

实际检查：TypeScript严格检查通过，静态构建通过。真实Chromium加载构建资源、使用合成官方payload，实际绘制两个CodeSymbol及calls边；Canvas尺寸1100×720且已检查绘制像素。实际鼠标点击打开官方NodePanel并产生范围选中回调，Shift点击显示1跳路径；Records切换、760×540调整、clear后卸载画布、相同／旧epoch拒绝、新范围注入均通过。零页面错误、零外部网络请求。已实际打开并检查canvas／selection／resized截图。证据 `build/coding-memory-production-20261112/official-web-007/`，成功指针轮为pointer-004；浏览器和本轮临时静态服务器已由finally关闭。没有启动Cognee、模型或代码扫描，没有修改日常数据／Agent配置，也没有停止旧后台。

失败及准备记录保留：首次严格类型检查缺少pnpm隔离下的d3子包类型，添加与上游锁一致的显式types后通过（构建类型依赖1轮）；许可收集检查发现部分npm包未附正文，补入Mantine精确gitHead许可，其余两项保留真实缺口，不用伪造正文让检查变绿。两个路径调用错误及一次已安装浏览器文件名错误均发生在启动浏览器前，属准备方法，不是图谱运行失败。第一次真正绘制检查render-001通过；pointer-002定位器未检查alpha，把半透明线条误当节点，方法缺陷1轮；pointer-003已经点击，但用受CSS uppercase影响的innerText等待面板超时，改用不区分大小写的文本定位后pointer-004真实选择／路径全通过，截图可见“USED IN 0 ANSWERS”。这两项不改官方绘图，也不归为旧README生成／模型质量或helper又失败；所有原计数保留。

分发／资源边界：dist约3.2MiB，仅局部网页资源；正式分发未开始，不能当主App包大小。构建随附官方许可及第三方声明；react-remove-scroll-bar2.3.8和use-composed-ref1.4.0发布包缺少许可正文仍列于build.json，正式分发前处理，不宣称许可全部核对完成。开发node_modules约103MiB为当前构建依赖，生成的dist保留供Native接入消费；本轮124MiB上游解压／归档在完成vendor摘要核对后清理，未清理任何旧运行包／组件。磁盘仍高于100GiB。

仍未完成：MyGo局部WebView／可信绑定与来源跳转、真实官方SDK图产物及知识／原文关系分类、代码主动开启／官方索引／查询及模型使用、日常组件切换和CM连续链路。007证明官方组件可独立静态构建并在真实浏览器运行；CG07／规格11和整体Goal仍未完成。

## 008：MyGo可信宿主、实时查权与实际WKWebView

007为实际进展。本轮继续官方数据入口→可信WebView→受范围限制的节点回调，不重做既有Native页面。已重新实际打开首页／三栏基线图；这些旧视觉基线不覆盖较新命名和入口纠正。使用现有MyGo0.7.4，独立源码模块与开发探针未改主UI文件，也未进入完整App包／release。

连续用例：固定资源摘要校验→只向已注册CallerPage服务本地文件→原生宿主选择明确project／instance／dataset→客户端绑定及官方read ACL核对→有界官方payload原样注入→页面接受后才标可见→节点选中时查当前epoch／节点成员／授权→撤权、离开或被新加载取代时隐藏并clear→迟到加载／选中不能恢复旧图。未知来源、非法节点／路径、首次页面拒绝和读取超时分别失败，不用空图冒充成功。

实现与边界：

- `internal/officialgraph` 提供资源Bundle、Registry和Host。构建新增assets.json，Host启动时校验固定前端SHA与每个允许文件摘要，全部读入内存后服务，不按网页输入读任意磁盘路径，也不在请求时重读可变资源。绑定只暴露Ready／Select的facade，Load／Clear／Source／资源生命周期不向网页开放。
- 私有memlanegraph协议与服务绑定要求精确已注册的mygo.CallerPage；页面只允许自身index导航。固定官方JS／CSS保留原字节；Go仅负责供数、范围和原生承载，不复制后端凭据、不引入绘图算法或持久镜像。
- Host沿用006 typed读取，加载前后比对binding，复用官方payload校验；只保存当前可见子图及节点的短期内存。只允许当前epoch／project／instance／dataset内已发布节点回调，回调数据来自该冻结官方节点；文件名、路径或URL本身不是导航许可，后续知识／出处读取仍须经过正式reader。
- 新增 `official_visualization_authorize` IPC及官方 `visualization-scope` 只读端点：严格取得已有且可读的精确Dataset，不创建范围、不读图／材料、不调用模型。可见期间每秒检查客户端授权及官方ACL，失败隐藏并clear；不仅检查本机授权表。窗口关闭／Clear／新加载回收观察；Load的短期请求context结束不会误清空成功显示的图。页面发布按序执行，首次拒绝不算成功，相同已确认epoch的重复Ready才允许回放。
- 原有v13冻结组件未被覆盖；它缺少006和008端点，不能直接给新宿主供真实数据。后续需新不可变组件版本，继续保留旧包／数据及原有失败计数。

验证目录 `build/coding-memory-production-20261112/native-graph-008/`。Go定向race覆盖来源域、节点白名单、撤权观察、读中切项目、选中查权中clear、迟到加载、页面拒绝、请求结束与图显示生命周期，以及资源篡改／越界和无CallerPage拒绝；official／daemon定向race及相关四包vet通过。Python真实ASGI＋合成SDK3项通过，包含官方ACL探测的严格已有范围和错误不泄漏。没有以这些替身证明实际SDK图库／项目读取通过。

实际Native：新增仅显式build tag可编译的 `cmd/memlane-graph-dev`，拒绝无synthetic-only标记或既有输出目录。仅编译一个开发可执行文件，直接使用现有静态资源和合成Source，不创建App完整包、daemon、数据库、Agent HOME或Cognee运行环境。native-run-001实际WKWebView加载／可信回调／非法节点拒绝／撤权隐藏与清空通过；补入页面拒绝检查及本进程系统整窗截图后native-run-002通过；冻结生命周期修正后的结果见native-run-003。已实际打开网页截图和包含系统标题栏的整窗图，画布确实在Native窗口中。绑定验证由网页JS调用真实MyGo桥，不声称已完成OS鼠标、键盘焦点、菜单或既有项目页连续验收；后者仍待集成。

所有本轮源码检查、ASGI、Native轮次均无已观察的失败，不增加旧helper、README生成或模型质量失败次数。首轮Native通过后，基于源码审查补充页面拒绝、请求生命周期和整窗证据，因此才执行后续冻结版本复验；不是不断改ID重跑旧停止问题。命令／退出码／源码与资源摘要及实际Native结果见checks.json。

资源收尾：每轮Native验证只退出自己的窗口和进程；不重启／停止旧日常服务。005的签名预览源在检查仍有真实PID、全部现有运行配置摘要未采纳该源后结束，仅停止本轮所属source-005，不影响其他源；v13候选和归档继续保留，新组件需要新版本／新源，不能复活旧URL假称在线。停止结果单列source-005-stop.json。保留一个最新开发探针供后续消费，小型报告和截图保留；未清理任何运行包／模型／数据。

尚未完成：在现有项目页替换旧自绘图区域、实际知识／原文／实体出处导航、真实官方服务与Native读取连续链路、代码独立授权和官方索引／查询／Agent使用，以及原CM主闭环。原日常升级／回退停止项和首轮日常项目选择仍保留，不借此恢复helper，也不自动开启代码扫描。CG07、规格11及完整Goal继续未完成。

## 009：官方代码后端关联与默认拒绝的SDK执行入口

008为实际Native进展。本轮按plan-change-impact继续CG01／CG05／CG06的前置控制，不把旧G2的Rust图冒充官方Cognee图。完整后续流程仍为项目明确开启→独立官方dataset与账号核对→固定授权代次／来源→受管官方索引→查询和官方图→代码变化／取消／停用；本轮先完成后端关联与索引入口门禁，实际执行所有者和查询并未交付。

源码核对：固定a176248插件的index_repo先POST remember(code)，提交失败即返回，成功后才保存状态／Git fingerprint；fingerprint包含git diff HEAD，故不能仅靠COGNEE_CODE_AUTOINDEX=off声称客户端完全不读代码。coding_memory_enabled分支会跳过session-start自动建图、旧Stop刷新和旧code recall lane，但实际开关要求 `COGNEE_CODING_MEMORY` 为true；仅安装插件或服务端设置DISTILLATION_MODE=coding不等于开启这一插件模式。现有安装器尚未配置／验证该模式，也没有执行日常安装；这是接下来CM接入需补的明确前提，不能当CM03已经完成。SDK ec6f408自带代码管线固定enola-cli0.4.30，可用ENOLA_PATH显式选择已有解析器，不需另写解析器；实际组件路径传递和固定输入仍待接通。

实现：

- schema37只新增 `official_code_scopes`，绑定既有code_worktree与官方instance／dataset／user／endpoint，保留明确操作及唯一dataset归属。旧工作区默认仍走原后端，不自动转换；新项目／Agent连接不创建开启项。CodeAction仅明确enable可选择官方scope；停用增代、旧回放不重启，省略scope的重新开启仍保留官方后端并重新做服务核对，不能退回第二个本地扫描器。
- 知识／材料dataset（含pending绑定）不能被当代码dataset；反方向也拒绝把代码dataset绑定为知识／历史会话读取。已绑定官方scope不能被普通enable静默换掉。切换旧本地后端须先明确停用，旧snapshot／receipt和任务历史保留；官方作用域不使用它们作为当前官方图。
- 新只读code/scope端点严格核对官方已有write范围和真实user，读取最多一条Data元数据及官方直接图计数。未知、非整数／负数计数、未知Data结果均失败；首次关联必须实际观察为空，已属于本工作区的官方dataset可保留既有代码数据重开。daemon要求固定服务SHA、新scope能力和已有私有凭据，探测后才提交元数据授权；不会在探测中创建dataset、材料或知识，也不运行代码抽取。
- 既有runCode在任何Capture／Enola／Rust调用前识别官方后端；当前未接官方执行者时明确记 `official_code_backend_unavailable`，不借旧pipeline凑成功。旧worker作业启动／发布和旧查询也拒绝官方状态；旧本地功能继续保留。Store.AuthorizeOfficialCode供下一步执行者核对精确scope、授权代次、项目／worktree／common dir和当前Git绑定，不把连接当索引许可。
- `cognee_code_guard.py`在配套服务内包装官方公开remember(code)导出、repo解析、代码文件／repo抽取、Enola入口与已加载的官方别名。无私有受管上下文一律官方403，在原索引流水线前拒绝；repo URL和跨仓cluster不在当前本机单worktree权限内。上下文固定预期repository／dataset／真实user，并在边界调用活动授权检查；真正的私有来源快照仍由后续owner创建和核验，当前没有可提交任意路径的受管HTTP执行通道。拒绝开放默认账号／dataset、SDK detached background及向量索引选项。普通remember知识路径继续原样调用官方实现。它是入口／权限适配，不是采集器、抽取器或图数据库。

能力限制：当前只有准入／拒绝路径和可供后续owner使用的内部上下文，**尚未提供创建这个上下文的受管HTTP任务／真实索引执行者**；App开启元数据不会让公开SDK调用绕过门禁。官方scope能力不表示官方执行／查询已就绪。代码召回、Native代码图查权与普通知识图查权还须按不同purpose接入，不能给代码dataset补一条历史会话绑定绕过隔离。

验证目录 `build/coding-memory-production-20261112/official-code-009/`：

- Store全包通过（56.402s）；随后相关store／daemon定向race、schema36→37保留检查和相关包vet通过。合成SQLite覆盖默认无扫描、明确开启／停用／重放、精确代次、后端保持／切换、数据集双向隔离、旧快照元数据保留、真实预迁移备份及原有本地授权不会自动变成官方授权。
- 19个模拟旧版本的夹具在还原旧schema时增加移除新表，旧grant测试的当前schema断言改用SchemaVersion；新表迁移不使用IF NOT EXISTS掩盖错误。本轮这些检查未发生已观察失败，不重置或新增历史具体缺陷次数。
- daemon的真实loopback HTTP＋SQLite合成场景覆盖错误账号、非空新dataset、无新能力旧服务拒绝；合法scope开启后不调用旧Capture／worker，旧查询拒绝，重新开启仍核对已存在scope；没有真实官方数据／源码扫描。
- Python真实ASGI＋合成SDK覆盖write权限、账号、限定dataset和Data／图计数的未知拒绝；内部上下文合成检查覆盖关闭、错误repo／账号／dataset、撤权、取消背景选项和普通知识路径不变。合成成功返回不是实际Enola／代码图结果。
- `verify-cognee-code-guard-contract.py`重新逐文件校验3343份固定上游源，在独立临时HOME／日志／SDK根中导入真实ec6f408，核对remember与Enola／extract公开别名，并实际调用六个未授权入口取得本适配器的明确403。没有调用原索引流水线、真实项目、模型或新服务；临时输入与SDK根随子进程收尾。它证明真实SDK入口拒绝，不证明允许后的完整执行或取消已通过。

版本与资源：为避免覆盖004的v13冻结字节，规范配方、Python契约和Go组件选择同步推进到 **1.6.7-memlane.14-coding-ec6f408**，一致性测试通过；上游服务／插件提交不变。v14本轮未构建、未提供新签名源、未安装；v13候选与归档继续保护。日常库仍未执行schema37迁移，旧daemon76077／Cognee76867未启停，未改已有界面／Agent配置或扫描日常项目。可用空间仍高于100GiB，无完整App打包。

下一步：优先补CM官方插件coding-mode配置／核对与现有处理选择的联动；接通官方代码执行者、稳定私有来源快照、Enola路径及真实取消／终态，再接有界查询／Codex读取和Native purpose查权。项目主动开启、代码变化／删除／重命名、与coding-memory共存及真实项目连续验收均未完成。原helper停止项及首轮日常项目选择保持，不恢复已停止实验；整体Goal、CM与CG全套仍未达成。


## 010：官方插件coding模式核对与配置作用域缺口

前一Goal轮为用户进度问答，只核对账本并汇报估算，没有新增实现；本轮恢复推进CM01／CM03的配置前提，继续保留已有UI与旧日常服务，不恢复helper停止项。

按需求影响与连续场景规则，流程为：固定官方插件及配置语义核对→取得hook实际选择的配置文件及进程环境→核对coding模式、服务凭据与精确dataset→分别呈现安装／hook信任／配置结果→实际采集及模型连续链路。只有第一项和配置核对源码完成；未取得真实hook进程环境，不能跳到日常可用结论。

新发现：a176248官方插件的默认`~/.cognee/.env`与Claude Code共用；`COGNEE_ENV_FILE`可选其他文件，但必须实际进入Codex进程环境。`COGNEE_CODEX_BACKEND`只隔离backend选择，不隔离coding模式、URL、Key或dataset；单写共享文件可能影响另一个Agent。本轮使用OpenAI Docs技能检索并实际打开[插件官方文档](https://developers.openai.com/plugins/build/plugins)，保存[官方配置schema](https://learn.chatgpt.com/docs/config-schema.json)。本次取得的PluginConfig字段无hook环境变量overlay，PluginMcpServerConfig也不提供这一能力；本机CLI为0.167.1。这里仅说明已核对接口缺口，不声称所有Codex分发／启动方式都不支持其他配置入口，也不凭未声明字段编造可用方案。没有擅自把共享文件改写成Codex专属配置，也没有用另一个HOME／专用启动器替代电脑级日常接入。

实现：新增`official_codex_coding_mode.go`的只读核对器，并可由现有VerifyOfficialCodexPlugin返回独立coding_mode结果；该可选结果仅适用于已核对1.8.0插件。输入由宿主提供所选env文件原字节、观察到的hook环境和既有官方binding，不接收hook任意声明作为授权。返回只有模式、绑定相符与问题代码，不回显Key、URL、dataset或环境值。不更改文件、不执行hook、不调用服务。

配置核对遵循固定上游的dotenv语义：UTF-8 BOM／UTF-16、export／引号、重复键最后值、无变量／命令展开；真实环境通过setdefault覆盖文件，空环境值也覆盖。分别复现loader和config的backend行为，拒绝local覆盖／错误服务凭据／错误dataset／保留auto或会被重写的dataset；核对managed endpoint及关闭共享Agent角色。Runtime为nil时明确hook_environment_unobserved，不能把App环境或一份准备好的文件当成日常hook实际配置；configuration_matches仍仅代表配置一致，不代表hooks、官方授权、服务能力、实际捕获或知识产物通过。launch已有记录可能覆盖dataset，后续真实范围与回执验收仍必须单独核对。

验证目录`build/coding-memory-production-20261112/plugin-mode-010/`：

- 固定源配方重新核对59份官方插件源文件，未更改上游字节。定向测试通过（9.220s），包含10种配置状态、4种编码及非法编码、输入保留和结果不泄露配置值；安装／第三方配置保留相关原测试同轮通过。
- 显式选择上述已校验固定源，在每例独立临时HOME中导入真实官方config，8组环境／backend／dataset组合的coding模式和最终binding结果与核对器一致。只导入配置，不运行采集hook、源码读取、HTTP、模型或SDK索引。该证据不能外推真实采集或新会话复用。
- agents包vet通过；现有已跟踪改动的diff空白检查通过。命令结果及源码／schema摘要见checks.json。
- 准备错误保留：环境无gofmt命令，改用仓库自带gofmt；两次prepare_source完成校验后，状态打印误将返回值当Path／配方而失败，读取其实际tree／file-map返回契约后已纠正。这些是本轮命令准备问题，不是日常hook验收或历史helper的新增轮次。配置／安装测试及上游对照本轮无已观察失败。

收尾时日常daemon76077和旧Cognee76867仍存在；未启停、切换、改写Agent配置、升级日常schema或触发扫描／模型。磁盘约121GiB可用，无完整App打包，临时合成HOME随测试退出清理。原运行依赖与失败记录保留。

未完成：官方支持且作用域准确的持久配置传递、真实hook环境观察、插件实际安装／信任／重载、日常组件切换及CM连续链路；本轮核对器尚无日常环境观察者，不把可选源码入口算成已集成UI状态。下一步继续核实可用配置传递方式，同时推进不依赖旧服务切换的官方代码执行者／来源快照与查询。CM01／CM03和整体Goal仍未完成。


## 011：官方Enola受管子进程与有界源码快照

010有新增配置核对源码与官方配置对照，属于进展。本轮继续CG01／CG03／CG06的实际执行前提：明确授权的worktree→有界私有源码→固定官方SDK的Enola入口→受管子进程→官方事实解析→来源复核；真实图库发布与查询仍在后续。本轮没有触发日常项目扫描，也未接管／重启旧服务。

源码发现：ec6f408的`enola._run_enola`使用asyncio子进程，超时时kill／wait，但协程取消路径没有对应的进程收尾。其stdout／stderr通过communicate收集，未设输出上限。这是固定源码事实，未据此宣称日常已有进程泄漏。新增薄执行适配只替换这个私有子进程生命周期；官方generate入口、命令形式、事实解析与后续图库逻辑继续复用原实现，不另写解析器或索引库。

实现：

- `cognee_code_process.EnolaRuntime`仅由私有CodeExecution上下文注入；缺运行时默认拒绝。要求精确generate命令、repo／cwd、明确二进制SHA、规范绝对路径、私有scratch和显式拒读路径；不接受stdin cluster或任意命令。实际服务启动尚未提供该运行时，不能从公开SDK／HTTP生成上下文。
- 以macOS sandbox限制网络、写入范围及源／数据拒读范围；子进程只收到私有HOME／TMPDIR、基础PATH和官方离线开关，没有继承服务凭据、模型配置或用户HOME。二进制必须是普通可执行文件、禁止group/world写，启动前核对摘要。其他平台明确拒绝，不静默放宽。
- 每100ms核对活动授权；取消、撤权、超时、命令失败、输出超限都进入同一收尾，终止所属进程组并等待直接子进程。捕获spawn期间取消时也取得真实句柄后收尾。stdout／stderr合计限制1MiB，错误只使用固定代码，不回显stderr。输出超限读者停止后必须继续丢弃剩余管道字节再wait，否则StreamReader暂停可令已kill进程的wait仍卡住。
- `codegraph.PrepareOfficialSource`复用已有Capture／Git忽略／无链接读取和32MiB／2000文件／每文件1MiB预算，当前明确仅TS／TSX／Python及既有项目manifest。私有目录在源仓之外且必须新建；复制后重新核对worktree／common-dir及来源digest，Verify再核对当前源、所有捕获字节、额外／缺失文件和链接，官方.enola输出单列；不写原仓。
- 仓库basename由worktree身份稳定派生；**官方Data行仍按完整repo路径识别**，后续持久任务owner必须按worktree使用同一个稳定私有目录槽，并在上一任务回收后才清理／重建。不能用每轮不同job路径直接remember而累积多个repo行。这一生命周期尚未接到runCode，helper本身不承担持久发布。
- builder将新执行适配纳入源码摘要；未重新构建v14组件、签名源或完整App，未覆盖既有v13产物。

证据目录`build/coding-memory-production-20261112/official-code-011/`：

1. Python项目解释器3.12执行7项真实子进程／Native sandbox场景，通过正常退出、错误退出、超时、取消、spawn中取消、撤权和输出超限；核对直接子进程实际returncode及PID不存在。合成子进程实际尝试读取拒读源、向scratch外写入，均被拒绝；环境隔离断言通过。guard既有合成测试通过。
2. codegraph全包测试通过（3.646s）；新增快照场景包含tracked但被ignore的文件、隐藏文件／链接、源改动／删除／重命名、捕获副本篡改／缺失／额外文件、输出链接、拒绝复用已有目录与仓内输出，旧目录内容保留。最终定向race与vet通过。这里只证明源码快照，不证明官方图库更新／删除。
3. 重新逐文件校验3343份固定SDK源，六个未授权真实SDK入口仍明确403。另在临时合成TS／Python项目内，通过**真实官方run_enola_generate→新受管子进程→真实Enola0.4.30→官方parse_enola_snapshot**成功得到10条事实、4个symbol，包含greet和add，原合成源码摘要不变。二进制SHA为060cff152dda0c317b378ca04e04023df09a1c7dd2e3cfcb0c174c668b7b9775。未执行官方remember／图库写入／向量／模型，也未使用日常项目。快照和抽取分别验证，尚不是二者与持久任务的完整集成。

失败留痕：首次命令误用系统Python3.9及模块搜索方式，为准备错误；改用项目3.12。输出超限收尾第一次验收未通过（1轮，process-tests.log），已定位StreamReader暂停并修复，后续六项及包含spawn取消的七项冻结检查通过，失败日志保留。真实SDK脚本首次因局部import遮蔽importlib失败，为验证脚本错误；下一次因临时根/var别名未规范成/private/var，在启动前被路径门禁拒绝，为夹具错误，规范化后真实抽取通过。各问题按具体触发／标准分别追踪，不合并成历史helper四次，也不清除任何旧计数。

资源与状态：所有新子进程为本轮所有，测试观察退出；合成HOME／源码／SDK数据目录随任务清理，只保留源码、报告和小型日志。收尾实测日常daemon76077／旧Cognee76867仍存在，可用空间约121GiB，没有完整包、模型下载、Agent配置或日常数据迁移。

未完成：将上述运行时、稳定源码槽、私有授权回查接到持久Code任务owner及实际官方remember／图库写入／刷新；代码过期与失败状态发布、查询、Codex使用及Native purpose授权仍待实施。现有runCode对官方后端仍明确unavailable，没有以新抽取样例冒充CG02／CG03或整体完成。旧helper停止项保持，CM日常插件环境和自动记忆主链路也继续待完成。


## 012：官方索引持久任务、撤权占位与独立回执

011已完成受管子进程／快照和真实合成抽取，属于进展。本轮推进官方索引执行者的持久状态基础：明确开启→固定任务／授权／来源→持久占位→后续实际官方执行→记录真实回执→复核授权后原子发布；取消／重启／迟到结果另行闭环。本轮没有把Store模拟回执当作官方图库产物，私有服务端执行入口和daemon实际调度尚未接通。

设计取舍：复用既有code_jobs唯一活动任务索引、历史、code_actions与code_worktrees，不新建一套任务库。manifest／receipt JSON增加明确official_code_job_v1封装与项目／scope／来源元数据，官方回执与旧worker Receipt分别读取。虽然没有新增表，**schema推进38**，用于拒绝旧读取器把新JSON当旧本地图谱结果；37→38沿用正式迁移前备份，升级本身不重写既有数据。当前日常库仍未迁移。

实现：

- StartOfficialCodeJob以宿主稳定operation建任务，固定project／worktree／common-dir／官方instance／dataset／user／endpoint、授权代次和来源manifest。重放显式Replayed，只用于观察／核对原任务，不能再次执行；换内容／项目／范围的重放拒绝。复核当前明确授权和实际Git绑定；授权检查前固定项目路径，事务内再次核对路径、全部scope与代次。活动任务存在时返回code_busy；中断／取消后须明确refresh，失败同一来源不自动重跑。
- CodeAction针对官方任务只记cancellation_requested，保留running占位、finished_ms=0；改为停用／重开也不能释放尚未观测退出的旧执行。旧本地任务保留原流程。daemon重启恢复同样把官方任务记为official_execution_unconfirmed并保留活动占位，不能将后台未知推断成已停止。
- FinishOfficialCodeJob只接受固定结果代码和已观测的实际完成／停止调用。读取数据库中原任务，调用者替换manifest不生效；官方receipt必须匹配operation／真实scope／来源摘要，completed必须有规范的DataID与PipelineRunID。不保存全文、源码或模型配置。
- 任务回执和当前状态在同一事务提交；错误／部分产物可保留回执，但撤权、代次改变或工作区失效后不会发布ready。旧代次真正结束后才释放占位；若授权变化没有增代也不会留下永久indexing。实际源码最终复核及SDK停止观察仍是后续owner的职责，这个Store接口不能代替它们。
- 新成功结果通过OfficialReceipt呈现，不伪造旧worker Dataset／Snapshot；失败或来源变化保留上一份官方回执并标failed／stale。混合历史读取分别还原旧／官方类型；本地worker保留集合跳过官方receipt，仍保留历史本地图谱，不把官方operation发给旧图清理器。

验证`build/coding-memory-production-20261112/official-code-012/`：Store全包通过57.651s，随后项目元数据／权限围栏复核后的相关定向race通过13.669s，补入固定code_busy结果后的最终官方任务race再次通过；daemon相关Code测试通过4.869s，store／daemon／serviceipc的vet通过。新增7项SQLite场景覆盖重放／并发、错误和缺失回执、事务发布故障回滚、迟到结果、撤权后占位、重启后未知不释放、明确refresh、失败保留、混合旧图保留、工作区消失以及37→38备份与元数据不变。原相关Code与官方scope测试同轮通过；全包结果与最终定向版本区别见checks.json。

准备错误：新增迁移测试误引用不存在的Store.Path字段，首次定向race及vet编译失败；改为从SQLite database_list取得合成数据库路径后通过。两条命令是同一夹具编译问题，不算两轮产品验收。正式任务场景未观察到新的实现失败；旧helper／README／模型等原计数保留。

收尾：测试只使用合成SQLite／Git目录和模拟官方回执，未安装组件／插件、启动新daemon／服务、触发真实项目扫描或模型。旧daemon76077／Cognee76867仍存在，磁盘约121GiB，无完整App包，未清理运行依赖，已有界面未修改。

下一步仍需把稳定源码槽／实际Enola运行时／授权回查接到私有服务执行入口，再连接daemon的串行任务owner、实际官方remember与图库写入／更新，最后接有界查询／Codex与Native purpose权限。当前runCode对官方后端仍默认unavailable；本轮仅补了持久任务基础，CG02／CG03／CG04／CG07及CM日常主链路和整体Goal继续未完成。


## 013：私有官方索引接口与真实SDK写图／刷新

012完成持久任务基础，属于进展。本轮实现后端实际调用入口，并核对官方remember返回契约和真实图库更新。中途用户暂停后再恢复；恢复前确认验证进程已终止，不据日志猜测仍在运行，也未重启旧日常后台。全目标保持，主UI未修改。

实际流程：带owner能力的任务标识→当前服务进程epoch→daemon私有Unix授权观察→真实已有write dataset／账号及单仓范围→上下文固定官方SDK→受管Enola→官方存储／刷新→官方回执→再次查权。后端只接收operation、instance、dataset、user、source_digest和execution_epoch；HTTP不能选择源码目录、可执行文件、模型或任意SDK参数。路径、二进制摘要和拒读范围只接受私有daemon授权响应，owner文件／socket沿用现有私有权限与每次启动旋转的能力，不把owner秘密放入回执。

`cognee_code_index.py`提供同进程index／cancel接口。每进程生成独立epoch，旧epoch或未知操作不能被当成已停止；reserve发生在异步查权后的第二次并发检查之后，避免两个preflight同时执行同一任务。重放只读原结果，未知终态不重试；一服务同一时刻最多一个代码索引，元数据回执有1024上限。撤权／断开观察会取消任务；cancel等待其协程收尾再回答，子进程reap未确认仍返回unconfirmed，不改写为stopped。当前取消证据范围为合成SDK协程与011／013真实子进程，**尚未完成真实图库写入中取消及所有存储操作的完整验收**。

SDK调用只用明确UUID的已有dataset、真实认证user、content_type=code、run_in_background=false、index_vectors=false和raise_on_error=false，复用官方锁、单仓图刷新、Data行和图存储。初次／运行中核对dataset最多一条本仓code_repo材料，拒绝混入其他仓或普通材料。结果仅返回匹配dataset的规范DataID／PipelineRunID和固定状态，不带来源路径、SDK错误或凭据；completed要求真实处理数为整数1。guard按当前私有上下文提供固定Enola路径，无需改进程全局ENOLA_PATH。browsing入口注册新能力和code_execution_epoch，固定启动时source SHA；能力只表示后端接口存在，不表示daemon已接好调度或项目已开启。builder纳入新模块摘要，v14组件尚未构建／安装。

证据`build/coding-memory-production-20261112/official-code-013/`：

- 真实ASGI＋合成SDK的14组场景覆盖scope／owner／epoch／禁止路径输入、SDK真实上下文、错误结果、串行与异步preflight并发、重放、取消、撤权和reap未知；4项测试方法含真实私有Unix socket／owner文件权限／错误响应关联及官方to_dict返回类核对，全部通过。旧guard1项、真实子进程7项通过。
- `verify-cognee-code-index-contract.py --synthetic-only`在临时合成存储内使用3343文件已校验的真实ec6f408 SDK、Enola0.4.30及Ladybug原生存储，经上述**实际ASGI→私有执行函数→官方remember→官方代码流水线**完成两轮实际写图／刷新。授权响应和HTTP认证依赖使用合成宿主／该临时SDK中的真实用户，**不代表真实Go daemon授权已通过**。没有新服务监听端口、完整运行包、日常项目或模型Key。
- 第一轮读取9个图节点／16条边，直接官方图查询包含obsolete.retired_symbol；删除obsolete.py、新增replacement.py后，第二轮同为9节点／16边，查询移除旧symbol并出现replacement.replacement_symbol。DataID两轮一致，PipelineRunID不同；相同操作重放回执不重复执行。旧CodeInsight同步消失。旧文字仅在CodeRepository.last_delta历史变更字段中保留，不是当前CodeSymbol或Insight，保留这一来源事实。
- 临时worker叠放树来自已校验的同版本源；仅补入builder同一SHA的既有Ladybug JSON扩展，避免下载且不改固定源缓存。实际write结果见sdk-index-typed-query.json。structured LLM gateway观测0次，graph-only未启用向量；验证进程环境为白名单，不继承真实凭据。退出前显式shutdown观察到的一份SDK worker session并核对进程不存活，合成存储／源码／叠放树随任务清理。

失败与纠正：首次验证脚本拦截了不存在的LLMGateway.acreate，为夹具错误1轮，改为固定SDK真实acreate_structured_output。进入实际流水线后，适配器误把RememberResult当Pydantic而调用model_dump，导致官方已写图但HTTP503，为返回契约实现错误1轮；查源码确认to_dict后修复，未把首次已有写入称为空／未执行。随后“整张图JSON不含旧符号字符串”断言失败，留下原日志；按真实节点类型、直接符号查询和旧文字所在字段核实，证明它来自应保留的last_delta历史，纠正为验收方法错误1轮。原日志和分类都保留，不抹去这些操作或把不同问题合并为4次；旧helper等停止计数不变。

当前边界：后端默认Unix请求official_code_job_authorize尚未接入Go daemon处理器，runCode仍默认unavailable；真实源码快照→持久任务→daemon私有授权→此接口的完整串联还未完成。持久execution epoch／重启核对、实际图库取消矩阵、官方有界查询、Codex和Native purpose授权仍待接。CM插件日常环境及自动记忆主链路也未完成，整体Goal继续进行，不以两轮合成代码写图代替日常交付。


## 014：最新范围切换及暂停交接

收到用户侧边对话明确要求移除全部权限管理，主线程已停止按旧权限方案推进，改为全局清理；随后用户要求暂停所有开发并handoff。Goal已设为paused。统一交接见[coding-memory-handoff-20261112.md](coding-memory-handoff-20261112.md)，清理范围与剩余项见[remove-agent-permissions-20261112.md](remove-agent-permissions-20261112.md)。

本批实际修改：中央请求解析改为ResolveAgentScopes，连接不要求许可范围；已连接Agent可选择任意已存在数据组读写。保存仍需目标，项目归属／实例／操作代次等不被当成Agent ACL。删除ChangeProjectAccess对应Store／IPC／records／daemon分支，删除项目访问编辑器和专用测试；连接页许可勾选删除。删除跨项目来源脱敏，移动后原文／出处可见；save重放返回原保存回执和当前版本，不重复改写。项目任务绑定、初始化和usage记录的旧白名单检查删除。旧AllowedScopes字段、其他consent／grant／读取复核等仍在，尚未全部清除。

证据位于`build/coding-memory-production-20261112/remove-permissions-014/`。新增Codex／pi向两个项目实际保存与解析读取、默认数据组可见、断开连接技术身份失效、未指定保存目标、移动来源保留和首次项目落库等Store定向race通过4.148s；daemon移动后读取／重放去重及旧组件不进入旧扫描器场景通过3.193s；原设置阅读状态／连接场景和权限编辑器消失的ui.Tester测试通过1.011s。四包编译通过；vet在最后usage／pending局部调整前通过，后续局部改动由最终定向测试编译。没有做全量回归或实际Native窗口验收。

本批留痕：初始编译发现已删除helper仍有一个UI调用，随后删除；来源过滤移除后遗留一个JSON import，随后删除。原测试仍要求移动后拒绝跨项目读取，按新规则替换为当前版本／来源／去重断言；真实dispatch测试又发现usage仍读取旧白名单，删除该残留策略后通过。日志保留，不将这些当旧helper又重跑，不清除既有失败计数。

收到变更前新增的official_code_owner.go及epoch／IPC结构已写入并可编译，refreshCode已接入，但尚未集成验收且有已识别未修复问题；必须按新规则重整，详细见交接。旧013“daemon未接处理器”是历史状态，不描述当前源码。没有启动这些新daemon代码或迁移日常数据。

暂停时未观察到本轮编译／测试／SDK验证进程在运行；旧daemon76077／Cognee76867仍运行。没有完整App包、组件安装、模型调用、实际项目扫描、服务启停或运行依赖清理。暂停后仅更新交接和文档，未启动新开发／验收。
