# 官方会话处理：模型上下文接线

属于本轮Goal的官方知识产生与出处链路。原失败`OFFICIAL-IMPROVE-RUNTIME-001`仍为1轮；本文件不清零、不重发其POST，也不恢复已停止的旧README图谱或helper验收。

## 依据与执行前用例

当前官方improve使用服务的默认模型上下文。直接请求没有携带Memlane已验证且明确启用的处理配置，导致无可用LLM时落到GLiNER。新版材料任务已有daemon拥有的`StartCogneeGateway`和`ProcessingAdapter`：每个任务冻结配置与依赖，用短期loopback能力向实际pi／Codex／Provider分派；无需向官方服务全局保存Provider key。旧`SelectPiBridge`仅适配早期服务版本，不能当作当前任务分派方案使用。

本次按以下连续链路补接，不以单测代替最终业务：

1. Native明确发起当前项目、官方dataset及会话的处理；daemon在任务事务内冻结当前已启用且有效的处理配置、授权和来源身份。
2. daemon启动该任务网关，调用固定服务的新会话处理入口。服务核对写权限和当前身份拥有的确切会话／dataset，在单次上下文中调用官方improve，不重放QA或另建全文库。
3. 并发任务不能互用模型能力；失败保留阶段结果，取消等待SDK任务清理；未确认的操作不能自动重发。重启后的恢复需依赖daemon持久任务，不能仅依赖服务内存回执。
4. 读取真实官方产物、处理状态与来源；没有session_learnings时如实显示。返回知识／文档保持阅读位置，撤权及时撤下，完成实际Native验收。

## 本次已实现的服务源代码

新增`engine/python/cognee_session_processing.py`，入口`/api/v1/memlane/session-improve`及`/cancel`。请求限定instance／library／operation／project／dataset／session、冻结处理摘要和短期网关；拒绝额外正文、非规范身份、超界输入、远端网关、配置摘要缺失。服务调用官方写授权resolver；仅允许当前认证用户自己的同dataset会话，不从可见目录选择其他用户或同名会话。插件子用户的会话如需处理，后续必须明确使用对应身份，不冒用父用户上下文。

入口复用现有schema-only ProcessingAdapter，通过官方ContextVar安装单任务LLMConfig，覆盖extraction／summarization／query阶段，finally恢复原上下文。不修改环境或服务全局模型，不安装GLiNER，不新增全文、知识或索引副本。SDK负责持久化和提炼；无关global context index／truth subspace／conflict review关闭。若服务开启全局个人偏好更新或SDK远端转发，当前入口明确拒绝，不能借当前项目处理隐式改变此范围。

每个服务进程对最多1024个操作保存轻量摘要／回执；相同操作不同请求拒绝，活动冲突拒绝，取消留墓碑。结果未确认的同一操作拒绝重跑，而非HTTP200假装成功。此内存机制**不是跨进程恢复或持久幂等保证**；完整daemon任务接线仍待实施。

服务返回固定阶段名、状态、数字计数及安全错误码，不返回原始Provider错误、模型正文或运行payload。任务网关的真实执行回执仍须由daemon单独收尾与封存，不能从阶段数量推算实际模型调用次数。

新模块已接源代码路由与构建输入摘要；现运行1.6.7-memlane.12组件不含本模块，未替换／重启日常服务。尚未构建新组件、更新签名feed或开放Native处理按钮。

## 已取得证据与未完成边界

`session-processing-context-001`记录源SHA、命令和结果：

- 6项合成边界检查通过：身份／摘要／网关校验、错误脱敏、并发上下文与取消恢复、重放／请求变化／未知结果拒绝、错所有者／dataset／全局偏好或远端模式拒绝、活动取消等待清理。
- 既有材料提交4项合成检查通过；未运行其已停止的真实图谱验收。
- 实际安装的Cognee1.6.7 SDK选择检查通过：两并发任务中`llm_available`为真，auto extractor选择llm，native client选择任务ProcessingAdapter，上下文恢复。该检查未运行模型、improve或安装GLiNER；不等于新入口已成功产生知识。

下一步仍需daemon持久任务与授权／配置代次冻结、能力检测、取消及执行回执封存，随后集中构建服务组件与实际模型／Native业务验收。原失败请求的部分写入和旧数据保留；不能用当前检查把官方知识产生或整个Goal标为完成。

## daemon持久任务接线增量

新增轻量session_processing_tasks及schema32迁移，只保存来源身份／观察摘要、授权绑定、冻结处理配置和安全阶段／执行回执，不保存QA、工具正文或生成知识。迁移先保留schema31备份；当前只在隔离测试库验证，未升级受保护运行库或旧001阅读库。

daemon新增start／get／latest／cancel IPC和独立生命周期拥有者。启动必须由可信界面明确发起，先核对当前来源、固定服务能力和已启用的处理选择，再在同一事务冻结；同操作重放先返回原任务，不因后续配置变化重跑。所选范围是官方当前保留的整个会话，观察摘要只覆盖接口已返回尾部及数量，不能冒充完整会话快照；活动时间／状态更新不改变观察摘要。启动前及结束后核对该观察，运行中监测原项目授权，撤权触发取消。

运行使用任务网关，服务回执须匹配用户／项目／dataset／会话／library／operation和模型摘要。关闭网关并封存真实执行观察后才结束任务，最终成功采用与授权变更串行；迟到成功不能越过取消。传输结果未知时请求SDK取消并等确认，清理未确认则保留interrupted。重启和恢复备份均保留任务意图，标中断而不恢复执行；恢复不会复活旧授权。

检查证据`session-processing-owner-001`：Store最终完整测试45.435s，daemon完整31.351s、serviceipc1.080s、records2.302s；定向race覆盖Store／daemon／records（5.062／5.587／1.564s），新增恢复race7.074s，受影响包vet通过；Python6项边界检查2.909s通过。真实SQLite＋HTTP＋任务网关的合成测试验证一次执行、重放、撤权取消与回执封存，不是实际SDK improve或模型调用。命令／日志／源摘要分列，不外推全仓或Native交付。

本次三个验证准备问题分开记录：新用例保存候选时缺ExpectedSaved，触发正确CAS拒绝；旧版本夹具未移除schema32新表，伪造了不一致旧库；另一用例把当前版本硬编码为31。均保留首次失败与修正，未放宽生产迁移或内容／授权断言，不按整包失败数聚合。新增schema31升级及恢复测试确认旧内容、备份和中断意图保留。

daemon候选已构建，服务组件首次构建通过后被自动收尾误删大载荷，详见[产物存续期复盘](build-artifact-lifetime-20261112.md)。修正后同位置重建退出0并确认产物仍在；随后真实打包组件在独立合成根的健康检查HTTP200，声明session_improve_v1，实际新路由拒绝无效范围、确认取消并拒绝取消操作的后续提交。7次请求，没有运行会话pipeline或LLM；独立进程已回收、进程组为空，回执`session-service-health-001/report.json`。未将路由和健康通过外推为实际知识处理。

当前候选未注册为正式签名组件、未替换日常服务，也未开放新Native处理操作。实际模型、知识生成与正向出处窗口验收继续未完成，原improve失败1轮不重置、原POST未自动重发。

## 隔离真实 pi 与官方处理验收增量

证据目录 `build/conversation-official-20261112/session-processing-real-001`。沿用同一个合成根和 dataset；官方组件68058、新daemon68109与日常76077／76867分离，未替换或迁移日常数据。pi公开模型定义经字段核对后仅复制所选DeepSeek配置；凭据只由原auth文件符号链接读取，没有复制到报告或官方服务配置。

实际pi0.84.1协议检查通过：仅memory_scopes元数据回执，无模型和工具执行。daemon保存候选、运行真实DeepSeek模型验证并明确启用通过；验证ID `a72d53de-487f-422e-92c1-da0bb9f5b016`，实际进程72426已回收、组为空、结构有效。该验证仅证明能力，不是知识质量或自动采集。

同dataset人工提交1QA／1trace，明确标记manual fixture，不能替代正常Agent自动采集。随后daemon持久任务 `a0bf3378-b2b8-4bc2-ad08-6acc12daa0c2` 真实调用新组件官方improve。结果failed／official_stage_failed：feedback_weights完成，persist_session_qa失败，后续阶段未运行。官方已写入的部分原始文档与缓存保留；未产生session_learnings，执行回执为空，不能把流程判为知识闭环通过。

新缺陷 `OFFICIAL-IMPROVE-SCHEMA-001` 第1次验收失败：SDK的test_llm_connection实际以response_model=str探测，而ProcessingAdapter直接调用model_json_schema，故在网关传输前报material_model_request_schema_invalid。与旧 `OFFICIAL-IMPROVE-RUNTIME-001` 的GLiNER安装失败表现、配置和修复对象不同，分别记1次，保留关联依据，不能据此宣称旧运行问题已最终通过。

适配器已增加严格JSON字符串封装：仍走任务schema网关，结果验证后只向SDK返回str，不退回未验证普通completion。实际安装Cognee1.6.7的Task执行器、LLMGateway与test_llm_connection定向检查通过，6次loopback请求覆盖并发隔离、官方字符串探测、字符串返回类型及数值拒绝、脱敏和取消；未使用真实模型运行这组夹具。修复尚未重建组件或再次运行真实improve，因此不能标记该缺陷验收通过。原失败任务不自动重发。

隔离准备错误另行保留：保留域邮箱被上游拒绝、ProtocolOnly传入Workspace、CLI桥误填daemon、操作ID未用UUID；均未执行官方知识处理，不混入上述缺陷计数。收尾IPC字段错误和退出进程观察竞态也留在本轮命令记录，不作为知识处理失败计数。

下一步：重建含字符串修复的候选并核对源摘要；在同一合成库保留原操作、部分写入与计数，明确新处理操作后验证真实官方产物及Native来源窗口。Codex信任／登录和官方编辑历史缺口的既有决策项仍未获得新答复，自动采集、迁移及最终完整交付继续未完成。

## 字符串修复的真实组件复验（round 002）

独立候选同输出重建exit0，`string-fix-build-001/cognee-service-build-result.json`核对当前ProcessingAdapter源码SHA、嵌入模块、原生扩展和完整可执行文件摘要。未替换日常服务或注册正式签名feed。6项会话处理边界检查仍通过。

按原配置在同一合成根、同一官方用户／dataset／session重启。首次健康请求在本地BGE初始化时超时；进程78772始终存活，未因观察超时重启，随后同一进程健康和登录通过。刷新原绑定的短期凭据，scope／连接／代次不变；旧凭据、实际凭据值均不进入回执。

新任务 `5379f52f-1ced-4563-a778-aee76f328b76` 实际完成。官方feedback_weights、persist_session_qa、persist_agent_traces完成；extract_agent_context完成但lessons=0，distill_sessions返回documents=0／no_gated_entries。任务网关封存4次真实pi／DeepSeek执行，均成功、schema验证有效，进程已回收且组为空。此前字符串缺陷本轮复验通过，历史失败1轮保留；旧GLiNER问题失败1轮的原回执、原库及部分写入不删除、不清零。

官方最终文档2条，分别为QA和工具轨迹处理产物；同一会话仍为1QA／1trace，没有重复提交源条目。**没有session_learnings，不把图谱入图或任务completed当作生成可复用知识**，正向知识／出处Native链路仍未通过。该合成样本只提供一次成功工具执行，官方提炼没有接受可用指导；这是已观察的空产物结果，尚未证明提炼器缺陷，不人为填充context或学习结果。

证据 `session-processing-real-002` 包含固定源构建回执、原绑定刷新、任务意图／状态、4次执行回执、官方文档／会话核对及收尾。实际验证完成后通过IPC停止隔离daemon，再停止隔离服务；数据和完整候选保留，76077／76867仍运行，余量约128GiB。下一步需使用有实际错误／纠正证据的合成业务样本验证官方知识提炼，再做Native知识出处与返回；自动采集、迁移切换、用户编辑历史缺口和最终交付仍分别未完成。

## 实际错误／纠正样本与官方学习生成（round 003）

在同一合成根执行内存SQLite：确定性调度两个买家先读stock=1再无条件扣减，接受2笔、final_stock=-1；实际条件更新受影响行数[1,0]，仅接受1笔、final_stock=0。此测试不声称生产并发数据库验证。将实际错误、修正结果两条trace及说明QA各提交一次，保存逐条输入SHA和官方回执；属于人工验收材料，不是正常Agent自动采集，也没有直接注入context／session_learnings。脚本首次字符串语法错误保留在命令记录，发生在SQLite或官方提交前，不计为产品处理失败。

新任务765d849d-d5c8-4fb4-9261-5efbddd65c35完成：官方QA与trace入图，extract_agent_context产生2条lesson，distill_sessions实际生成2篇文档。9次真实pi／DeepSeek执行均成功且进程回收／组为空。官方共6篇文档，直接读取两篇带真实session_learnings元数据／官方模板的学习正文，并通过正式IPC验证各自来源匹配同一授权会话2QA／3trace。两个旧任务、全部原始条目及文档保留；原运行问题失败1轮、字符串问题失败1轮仍保留，不因新操作重置。

`session-processing-real-003`封存实际SQLite结果、逐条提交、任务及9次执行回执、两篇官方正文／来源核对。隔离owner停止后只读核对客户端memories／versions／jobs均0，session_processing_tasks3条；没有为了展示学习结果把正文存回镜像知识表。

## Native023真实文档出处与连续像素

GUI基线沿用022已固定源码，复制成独立023 bundle并核对输入／签名，只修改自有Info.plist隔离环境与截图路径。截图目录初次未位于合成marker下面，菜单未启用；修正路径后重开，原始配置错误留痕，不混入产品验收计数。未重建完整运行包或修改日常服务。

CUA实际1200×820打开当前官方学习正文→阅读来源会话→展开3条工具→返回原文档，选中文档保持；页面明确只关联整个会话，不能定位具体QA／trace。三张自身NSWindow截图及回执在native-official-learning-023/captures。正文来自上述真实模型产物，不是visual fixture造行。当前学习正文很短，未对文档非零滚动返回作新通过声明；来源独立滚动到工具区实际可见。

来源工具展开窗口连续31.001秒309帧、最大采样间隔116ms、0采帧错误；目录／文档读取各启动15轮，65帧有刷新进行中，正文有效且数量／选中不变。解码像素比较整窗、正文、固定数量三块，所有309帧与首帧一致。诊断仅记录文档轮询序列，不另行推算来源请求次数；本次未新增停服／撤权／晚回执场景，旧证据边界保持。

**尚未交付完整知识页**：当前实际学习文档仍显示在原始记录→文档，列表标题为官方文件名，固定处理状态说明未接真实处理账本，不能把本次文档来源通过写成知识页／四稿已通过。知识页官方直读及状态、标题／正文展示、非零滚动返回与800×620还需实施和Native核验；编辑／历史缺口仍按已提交决策项保留现有能力。自动采集、迁移与最终完整包继续未完成。

CUA退出独立GUI并由inventory确认消失；本次真实任务完成后通过IPC停止own daemon，再核对并停止own服务83704及其进程组，保留数据和完整候选。76077／76867仍运行，磁盘约128GiB。Goal保持active。
