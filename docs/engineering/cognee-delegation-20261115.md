# Cognee 官方能力复用收口

2026-11-16官方原版替换首轮核验：当前代码已以`04746da`提交并推送。原版配置／写入协议与真实只读ACL核对已执行；恢复采集补读禁用期、final整理不受两个自动参数关闭影响的差异已复现。尚未替换日常安装或删除补丁，证据与剩余项见[首轮核验](official-plugin-feasibility-20261116.md)。

2026-11-16后续授权：用户要求参考上游并发控制思路，覆盖所有同类模型服务且不绑定Agent/模型。新增通用端点容量适配，复用现有SDK HTTP策略，仍不补丁官方引擎、不另建业务调度；[方案与回归](model-service-capacity-20261116.md)。原单块候选正在真实处理，故未重启日常服务，新代码尚未生效。下文“不添加自有HTTP闸门”保留为前一轮授权及运行事实，不覆盖这次明确新增授权。

**最新决定与触发证据**：用户要求先移除限流，按官方合并／分窗／默认调度运行，后续仍出现拥堵才采用限流。临时2/120 RPM和批次2覆盖先已移除并正常重启；15:43 UTC恢复同一会话后，实际bridge占用32、出现113条新的codex_busy告警（告警数非唯一请求数）。据用户条件授权，选用官方CHUNKS_PER_BATCH=1限制每文档每批的扇出，不添加按分钟限流、自有HTTP闸门或30/300秒防抖。Provider不受覆盖。下文运行过程保留历史，不将会话合并误称为只调用一次模型。

2026-11-15，用户明确要求按 Cognee 方案执行，同时清除 Memlane 重复实现。仅 dev，不改官方存储、不进入 release，不恢复历史全量 Goal。

## 目标与影响

连续流程：真实 Agent 采集 → 官方缓冲 → 已授权材料持续保存 → 官方插件决定是否提交整理 → 官方 improve 分窗、去重、提炼 → 官方活动和知识展示。保存、提交、完成与知识产出保持区分。

清理前检查点：`pre-cognee-delegation-20261115-backlog`，commit `799ac86`，包含 project_backlog.py、project_backlog_worker.py、test_project_backlog.py 的当前改动；其余共享修改及运行数据未提交。

| 能力 | 官方已有 | Memlane 现状及本轮处理 |
| --- | --- | --- |
| 整理提交与冷却、失败退避、无新记录判断 | 固定插件 `_plugin_common.improve_throttle_reason`、`run_session_improve_detailed` | 删除后台直接 POST improve 和自有30/300秒防抖，委托原安装插件；只保存 UI 状态及未知提交保护 |
| 会话分窗、成功水位、失败续接、内容哈希去重 | 官方 extract_user_sessions/cognify_session、session_persist_watermark | 已复用，不另建窗口、知识副本或水位 |
| 图谱提取、摘要、知识提炼 | 官方 cognify/improve；当前 from_extraction | 已复用；不实现自有压缩、批量提炼、图谱生成或更改批次默认值 |
| 模型重试与过载降速 | 官方 native_adapter/retry_config/rate_limiting | 保留原实现；不叠加自有 LLM 重试或新传输闸门 |
| 保存缓冲、锁、基本 drain、发送退避 | 官方插件 drain_warmup_entries | 其默认未知写入核对失败会降级重发；当前要求未知不重投且保存任务绑定审阅范围。保留现有安全续存适配，不将“不等价”误删为重复；后续仅在官方提供等价范围/取消能力时替换 |
| 自动 remember 防抖 | 官方 auto_improve_debounce | 当前 typed `/remember/entry` 接口不等同 remember()；不能仅设置该参数宣称接管插件触发 |
| 任务活动、知识来源与正文、搜索、实体/代码图谱 | 官方 API/存储/画布 | 当前客户端仅查询/展示/操作适配，继续复用 |
| 权限、Agent 身份、项目 dataset | 官方认证与 ACL + Memlane 安装归属管理 | 官方授权保留；电脑级接入、当前权限核对、环境定位是客户端必要职责 |
| 召回统计 | 官方搜索记录与实际注入统计口径不同 | 保留只存知识 ID/时间的实际交付统计；不能拿搜索次数替代已注入次数 |
| Codex 订阅桥 | 官方无等价 OpenAI Provider 凭据 | 保留桥接及容量保护；不宣称官方方案已解决 codex_busy |

固定源码：service ec6f40859e5f2df9e7862dde27d580ac3268ed43，plugin a176248914fdb32a78354854b300174203b15a5c。所有判断基于本机固定源码，而非最新上游能力推测。

## 实施与验收顺序

1. 先用定向用例证明 backlog 不再直接 POST improve，而由已验证安装的插件处理；官方冷却延后仍可后续检查，未知提交不重投，原失败记录保留。
2. 删除客户端防抖计时，保留最小待处理关联用于跨进程交接；官方负责是否应整理。普通与 OpenCode 宿主均通过原插件入口。
3. 检查其他能力是否实际重复；没有等价能力不删保护，不增加另一套实现来完成“精简”。
4. 定向测试后跑相关 Python/Go 检查。运行链路先只读检查当前任务与所有者，确认安全再通过 dev 正常重启应用候选。真实整理以官方终态、实际知识及出处为准，不能以插件提交成功代替。

## 验收边界与问题计数

沿用 codex_busy 既有研究与失败证据；本轮不实施此前建议的自有共享请求闸门，不将旧失败操作当成新的验收轮次。首次本轮验收尚未执行。未知20条不重投，库和权限不变。临时用例仅用本地临时目录及假传输，不建立第二运行库。

## 当前进度

已完成影响核对及清理前检查点。

- project_backlog 删除直接 improve POST、自有 first_saved_at/due_at 计算和到时派发判断；旧日志保留，不重投 submitted/unknown。后续队列只存必要的会话关联与委托状态。
- project_backlog_worker.process 使用原安装 `_plugin_common.improve_throttle_reason` 和 `run_session_improve_detailed(trigger='auto')`。deferred 由官方冷却/退避决定，后续只再次询问；无新增材料为 skipped；无法确认的提交为 unknown、不自动重复。
- 保留 OpenCode 原有安装适配路径；它没有等价的 Python 官方入口，不描述为已迁移到 Python 插件。补充其 pending 关联保留 native session，避免跨宿主丢失原身份。
- 保存通道不因整理延后而停止。官方插件最终 drain 可能超过45秒静默预算，worker只发等待心跳，最终收到明确返回才报告 submitted。
- 定向 Python 49 项：46通过、3项现有环境条件跳过；新增官方插件真实函数测试通过：只替换网络，无模型／库写入，确认原提交负载、原成功状态、原冷却及失败退避。后续状态口径调整后13项定向复验通过。Go全量通过，4.205秒。

运行前：completion active_or_waiting=0，最近100条官方活动无运行项；旧后台任务仍policy_unconfirmed暂停，3条旧submitted状态保持原样，不据此称成功。

正常 dev stop 第一次超过35秒未确认；服务子进程已退出，原owner52210仍存活并持续运行ps。源码stop_descendants对每个历史PID重新扫描全进程表，11小时运行历史可能导致较长退出；采样支持在读取子进程输出，具体历史PID数量未知。保留owner和锁，不启动第二实例；等待原owner安全退出。此为开发启动器收尾问题，不是本轮codex_busy恢复验收失败。

随后安全恢复：核对原owner启动身份、原登记children全部消失、三个端口可绑定后，SIGINT结束卡在收尾的旧owner；独占取得原库锁，私有备份旧owner记录后移除失效占用。没有更换库或删除组件。之后两次正常dev stop均确认释放，没有重复该恢复操作。

## Codex积压核对与修复

- 旧自动任务一直paused/policy_unconfirmed，现权限已ready，但扫描器把所有paused永久跳过；正常官方hooks已将该会话缓冲保存到0，任务仍显示旧105。
- 只对automatic且未人工暂停、原因严格为policy_unconfirmed的任务重读当前权限与官方buffer；当前允许且未blocked才恢复watching。未知提交、写入拒绝、用户停止不绕过。
- 用原token集合与新buffer核对被其他发送器处理的部分，记handled而非saved，避免伪造本任务保存数。真实结果：saved361、handled105、remaining0、watching，旧操作ID不变。
- 额外全目录盘点：旧未绑定名字memlane的两份历史buffer共11条（3+8），不在当前UUID绑定的自动队列内；另有原20条未知仍隔离。不能把当前队列0扩称所有历史文件清空。旧11条中一份官方会话存在，另一份GET404；尚未证明每条保存结果，未重投或删除。

## TraeX与pi

- TraeX默认~/.trae正常只读任务01a1dfd8-46ac-7bb2-981a-fe127ec2e02b退出0；真实host为traex-ce51482beb71547844dd36cc2312ed774c7268ba5e489a8366c0dfb1f4b67bd6，官方保存1QA/1trace，新增1次实际上下文交付。环境回执true、buffer0。没有补采历史。
- pi真实默认~/.pi/agent未接入，旧pi-e180...仅是验证环境。通过原管理prepare/activate为默认pi-033f3aca88231dfdb2fd47fb接入，保留登录／模型与其他配置。首次activate调用参数名错误导致HTTP400，属于调用方法错误，未进入激活或写模型；纠正后按原prepared快照激活。
- 第一次默认pi任务因失效Flux auto-loader无法启动（独立失败1轮）。只读Flux官方本机状态确认plugin:pi原本enabled=false且依赖缺失；备份并移出遗留auto-loader，Flux包和设置不改，不安装／加载／卸载Flux Extension。备份与SHA留私有目录。
- 默认pi真实只读任务01a1dfd6-f7a6-7d14-9a05-e87b3e94a5d5退出0，调用cognee_recall/read；官方1QA/1trace，真实交付统计3次，buffer0。
- 发现实际profile回执仍false（独立失败1轮）：默认pi没有导出PI_CODING_AGENT_DIR，Python helper只看HOME不能证明宿主环境。pi适配改为调用官方公共getAgentDir()，将真实解析目录传给helper，不复制默认目录规则。原prepare/activate刷新同一身份的新artifact；第二个无工具只读会话退出0，真实environment_verified=true。未拿旧验证profile冒充日常。
- 34项连接／积压／生成参数检查通过；新的SDK导入、默认模型、默认profile、实际宿主回执已由真实pi执行验证。没有手工增加召回次数。
- 后续用户在并行会话指出权限列表有两个同名pi，并明确授权安全清理。并行会话通过现有retire接口退役旧验证pi-e180...，revision93→94；本轮只读确认旧身份不在active agents、archive存在、retirement=complete，新pi-033...仍在并保留2个真实绑定。旧会话和材料保留，未为此打断Codex整理。

## 知识处理恢复轮次与边界

1. 15:28 UTC单一明确会话的官方恢复已提交，保守限流下完成多个构图批次、没有新busy；尚未提炼时用户要求移除限流，受控退出并保留数据。此轮未标通过。
2. 移除所有新增调度覆盖后，15:43 UTC按官方manual触发恢复，bridge32、新busy113，默认扇出问题复现；按用户条件受控停止，不能算修复通过。
3. 官方单块批次候选实现及17项定向检查通过。真实恢复累计147个构图批次后，16:27:26 UTC问答水位推进到3；这一阶段无新busy、重试或错误。完整improve仍未结束，不能把该阶段通过当知识产出通过。

本次知识目录实时审计仍10条／当日新增0；原始材料已保存、Agent召回已有知识成功不代替本次知识产出。官方cognify_session会对dataset内待处理Data执行cognify，单选session不等于只计算这一份文档；本机embedding预算510 tokens使历史长材料产生很多图谱调用。这是工作量事实，不能拿3QA推算只有3次模型请求。

## 补齐的工具输出构图调用链

第一份长材料d0b28022-4a6c-4a4b-b2fe-8ace843670a9来自旧会话codex_01a1d3ef-481f-7ef2-8d14-a64727e3cf8c，创建于2026-11-14T02:47:40 UTC，node_set实际为agent_trace_feedbacks。它不是本次3QA窗口；采用固定官方纯文本切分器、同一BGE tokenizer和510预算离线计算，实际138块、57179个不含特殊标记的计数token，拼接完整复原原文；计算未调用模型或数据库。

阶段2完成后，阶段3又创建当前会话工具输出副本87524a04-53d3-44a9-abd1-6c822ef478aa：126210 bytes、node_set=agent_trace_feedbacks、214条原工具记录。固定官方extract_agent_trace_feedbacks._effective_feedback在session_feedback仅为默认成功文本时，把method_return_value追加进去；raw_trace_content=False并不表示只处理短摘要。两段大规模构图均由官方当前默认行为产生，不归因于Memlane自写逐条提炼。

官方IMPROVE_STAGES_DISABLED允许关闭非fatal的persist_agent_traces；后续coding_unit_extraction独立从SessionManager.get_agent_trace_session读取原工具数据，不依赖该图谱副本。关闭会减少完整工具输出的图谱检索覆盖，属于产品取舍。已向用户异步提供“关闭工具输出构图／保持默认”选择；未收到明确答复前继续默认运行，不停止或删除数据。仅将本轮新增副本私有备份及SHA留存，原3QA／214trace未改动。

官方问答持久化persist_session_qa是fatal，validate_stages_disabled明确禁止关闭，不能用跳过它来伪造处理完成。


## 2026-11-16 00:51 日常运行交接

按用户“后续还有问题再限流”的顺序，已先移除新增调度限制并真实复验默认流程；复现113条新codex_busy后，才恢复官方CHUNKS_PER_BATCH=1，无RPM覆盖。当前本轮208个构图批次完成，busy／重试／error均0，QA水位3；17份Data中16份完成，剩本轮126KB工具输出构图。完整improve和新增知识仍未确认，不标完成。

运行所有者仍为本轮scripts/dev.py start（工具会话9079），原日常库不变；不要为加载候选打断当前任务。模型端点共享容量候选已由并行会话完成源码和协议回归，见model-service-capacity-20261116.md，尚未加载。待自然结束后正常dev重启，核对applied回执max_concurrent_requests=2、capacity_wait_timeout_seconds=300，再核对真实链路；当前无busy不能归因于新容量候选。

用户尚未回复是否关闭persist_agent_traces；当前保留官方默认。新派生Data副本已私有备份，但未删除、未改阶段开关。原始QA、工具记录、知识、未知20条及未绑定历史11条保持。最新可读证据见evidence/cognee-delegation-20261115/verification.json；真实长任务运行中，不把提交或问答水位等同于新增知识。


## 2026-11-16 09:55 15日知识核对

只读官方全部dataset目录并按externalMetadata.node_set=session_learnings归类，目前全库39条知识，原10条保留，新增29条均在主memlane dataset（该dataset现32条）。新的29条分两轮：恢复会话01a1d7bc于UTC17:11:30提炼completed/documents=7（北京时间16日01:11）；自动会话01a1daed于UTC20:32:54提炼completed/documents=22（北京时间16日04:32）。后者22条中15条session_date=2026-11-15、7条=2026-11-14；前者7条session_date均14日。不能将29条全部称15日知识。

15日来源的15条createdAt均UTC2026-11-15T20:30:17，即北京时间16日04:30；实际15日本地日入库0条，16日入库29条。首页overviewBuckets及overviewKnowledgeRows明确按Document.Created转换本地日期统计和过滤，不使用metadata.session_date。这解释了热力图15日仍0：来源日和实际入库日不同，不是现有15条丢失。已抽读队列调研、官方单块回退、共享容量三条原文确认知识内容存在；仅证明已保存及提炼，不代表新容量候选已加载或15日全部会话已处理。

先前仅报告已完成批次数却未及时补报知识终态，原运行中结论现已过时。零知识最初与codex_busy失败有关；恢复后耗时还包括dataset历史工具输出138块以及当前会话126KB工具输出构图，随后才提炼知识。工具输出构图决定处理内容范围，批大小决定一次放入多少块；两者不能合并解释。未收到关闭工具构图的用户指令，仍保留完整官方阶段；未在本次只读核对中重启或重跑。


## 工具构图耗时细分（第二轮）

按UTC17:21:42.922572至20:21:29.911617、约2小时59分47秒的阶段窗口，对固定日志中同名Coroutine started/completed顺序配对：extract_graph_and_summarize共483批、累计10627.3秒（2小时57分07秒），均值22.0秒／批，中位20.4秒、范围9.0–55.9秒；其内部add_data_points同为483次、累计360.6秒（6分01秒），均值0.7秒。内部时间不能再次相加。此窗口第一轮已结束，所观察抽取为单块顺序批次。

因此已证大量时间在图谱抽取阶段、不是数据库写入。扣除内部add_data_points后约2小时51分07秒仍包含模型请求等待、桥接排队、Codex app-server启动/协议与结构校验等，不能直接称纯模型推理。现codex_completion_endpoint.log为0字节，桥接未留逐请求队列/启动/推理计时，历史这几项无法精确拆分；没有证据称Codex本身故障。483批由官方工具文档切块产生，最终22条知识是后续提炼结果，不等于22次模型请求。
