# 日常版收口与环境准备推进

用户在b42ec80提交后要求继续推进：通用记忆空库误报、现有功能与V3实窗收口、环境准备/依赖复用和安装确认。继续dev与唯一日常库，不恢复历史全量Goal，不做release；私有配置/消息/库/截图不入Git。既有具体问题停止计数和运行依赖保护继续。

## 连续用例与影响

- CE01：已授权空通用记忆→原列表确认为空→跳过搜索/无新增NoDataError→原项目知识仍交付→最终权限代次复核；非空候选仍检索，索引/引擎故障不伪装为空。
- 现有消息状态：暂停/明确拒绝/带编号未知保存/原回执核对/已保存未整理/实际模型失败按职责显示；保留未知材料、原失败和来源。
- V3：日期20→80→详情/来源/返回，搜索两路结果/筛选/失败分路重试，通用未选择/分类空/读取失败，设置浏览/取消/实际active，画像只浏览不自动推理。复用对应版已验证证据，只补缺口。
- 环境：已有兼容依赖复用；缺失/不兼容先列计划与来源/大小/位置，显式确认后安装；拒绝保持已完成部分；诊断和Agent排查不依赖引擎已就绪。不为验收下载新依赖或另建日常库。

## CE01 空库误报

实际官方日常通用dataset9e5f21a7-acdd-579d-bd14-4fd4aaed3060无文档，原固定插件_merge_block读取/data后仍对非空query发起CHUNKS搜索，产生官方search failed/NoDataError回执。9项策略回归旧实现失败（同一用例3个分支），CE01累计失败1轮；修复只在候选为空时跳过搜索，不跳过最后权限核对，不吞掉非空候选的检索错误。

修复后9项通过，来源固定a176248914fdb32a78354854b300174203b15a5c的58文件不可变产物已构造；extension SHA28735b773bb0dbe1cb96d6f00d5596f80228bce76e29d27b85e24f48173a175d。旧manifest单独固定为managed-project-plugin-pre-empty-common.json，启动和安装只接受逐文件匹配的已知旧回执。旧安装未原地替换，旧HOME/instance及会话状态保持；实际接入切换与新会话验收待后续。历史官方错误回执不改写，不能仅清掉首页记录声称修复。

证据沿用build/diagnostics/roadmap-reliability-20261119/common-empty-*.log。初始工作区61个已跟踪文件有其他会话改动，按功能范围纳入审查和验收，不批量重置；源码检查、真实Agent、官方存储、Native与release分别记录。

CE01进一步检查：26项固定插件回归、18项连接回归与lint通过。新产物位于.local/daily/components/managed-project-plugin/common-empty-20261119；HOME/实例命名的.cognee-plugin状态目录稳定，prepare只构造新代次，不覆盖旧缓存。当前空闲，已备份runtime、registry、三种Agent接入配置和原安装路径；正常收尾原UIwatcher85709。接续必须先确认同库stop退出，再只改runtime.project_control.plugin_dir，正常start ready后依正式prepare/activate/review操作ID核对；错误/未知不盲重试。

当前同库owner74255 ready，基础路径已切新产物。pi prepare/activate已按原操作确认，等待新会话；Codex prepare完成，但activate ce01-codex-activate原操作409，GET原回执为unknown/error_code=codex_legacy_consumers_still_active，未写主配置。检测到旧exit-watcher.py PID65007（Wed Nov18 12:25:36），具体依赖与在途状态待核对，不绕过保护、不重发安装。TraeX尚未升级；可独立继续其正式操作及后续Native验收。原操作状态保存在私有consolidation-20261119-backup/upgrade-operations.json。

Codex阻挡进一步核对：65007是原受管插件退出监听器，bootstrap parent64374为常驻Codex App daemon；退出监听器父进程为1是其正常detached设计，不认定为孤立废弃进程。原激活在Codex.activate写配置前的legacy_consumers guard拒绝，主config与prepared baseline一致，未发生未知写入。按维护授权仅对匹配实例、脚本、内核启动时间且无sync子任务的监听器正常SIGTERM；保留daemon、pidfile备份、原旧配置/缓存/材料，不触发finalsync。不因为父daemon仍存活而杀掉用户客户端。后续确认旧消费者为空和prepared摘要后才新明确激活；原409/unknown原回执作为前置拒绝历史保留。

接入状态：pi、Codex、TraeX均激活成功，Codex当前审核SHA对应执行许可已登记；身份/HOME/原数据保持。新pi任务第一次在模型启动前的官方只读GET被connection refused阻断，未生成session/未启动pi；原owner74255启动命令随后正常退出，后来同库新owner86475 ready，配置与新基础插件相同。停启发起方未核实，不推断成引擎崩溃或模型调用失败，不另启第二栈。已在当前ready前提下补实际新pi任务，保留前置失败。

CE01真实新会话：pi 34.37秒、Codex45.98秒、TraeX43.26秒均退出0，分别官方保存1轮问答/1条工具记录，原主体和memlane dataset一致。新pi前后NoDataError操作集合无增加，之后近期common错误为0；历史失败保留。三代插件已通过正式流程激活，Codex当前审核hook许可登记成功。新owner86475同库ready、基础新产物路径保持。

现有源码按bits-code-guard三组只读审查和跨组校验完成。独立问题6项：手动capture_retry_pending误称自动重试；仅编号未知消息user_stopped无resume；official_http_403未进入补交分支；召回30秒计数更新不重排；总览全目录逐条统计请求放大；新安装marker以prepare时间判定旧会话。排序跨组重复去重，既有检查只证明代码路径，真实负载/Native失败不伪报。新增Agent适配只读审查，不额外激活；接续集中修复后定向检查与真实场景。

消息恢复/403补交、30秒计数重排、总览按dataset批量请求/离页取消定向检查通过。恢复只允许确定未发送或带原编号可核对消息，旧无编号未知不重发；手动暂停不冒称自动重试。总览80条同dataset由逐条80请求变为1次counts读取，取消通过真实HTTP请求Context验证；按次数刷新顶部立刻重排、滚动中保持位置，回顶部再重排。新增适配准备marker为未激活，确认enable后同步源/Native实际时间，恢复核对不把未激活当已连接。尚未做此批新Native或真实负载测试，全Go门禁进行中。

修复复核发现独立回归：恢复aggregate排除拒绝源但后端项目级恢复仍会发送，该缺口已在worker review+逐token发送前硬拦capture_request_rejected，混合两源与旧tokens回归通过10项；总览目录未返回时离页仍会迟到启动统计，导航取消目录并推进generation已修。原测试首个Update可能是activity而非目录，已使用独立activity阻断信号保证实际目录回调入队，保留初测试方法边界。安装双marker部分写入通过journal仅修复已知前/后SHA，恢复不重装；首次新增回归发现record激活时间缺失（1次独立修复回归），已从已核验marker无条件发布原时间，原失败log保留。相应新回归/静态复核分别记录，不合并到CE01空库问题或重新清零。

最终边界检查：消息worker10项、完整导航迟到目录回调定向Go检查通过；安装准备/实际启用/双marker IO中断恢复8项通过，原失败log未覆盖。测试隔离了并行activity回调，保证迟到目录时序有效。所有审查复核产物在/tmp/memlane_consolidation_review_20261119；生产、协议、实际Agent和Native证据分别保存。当前空库闭环已完成；现有改动集成/Native及环境准备仍在推进，不能将批次已通过视全部交付。

环境准备定位纠正：initial_setup/environment_setup_ui及bootstrap已有正式检测、手动已有路径复用、具体依赖计划/安装确认、拒绝保持、诊断包/Agent入口和首次运行创建保护，旧local-prepare文档末尾“仅CLI”不能作当前未实现证据。本轮改为复核现有代码和安装前置，不另建日常库或无确认下载。一次合并Python检查遗漏PYTHONPATH，仅加载器报模块不存在，属于方法错误；补明确路径复核，既有独立通过日志保持。

本次接续：环境契约两个包检查退出0，最终工作树全Go test/vet、lint及35项策略/恢复/marker/统计Python检查通过。实际Native窗口323665/PID68101已完成2026-11-16日期20→80展开，完整树核对80个入口，读取第一条完整详情与官方来源会话16轮问答/55条工具记录。两次滚动投递未取得位置变化证据；返回前窗口热重载为323695，fresh state读取后点击通用记忆出现原同类prepared错误，停止该窗口重复操作。日期问题未再次失败，工具旧计数保持；剩余返回/切日/搜索/分类/配置取消/画像仅浏览不标为通过。截图及完整树在.local/design-v3-acceptance-20261119/native-final，不提交真实内容。

第一批后端独立候选tree ea60b07fb14cdf0c76b2fd51d4aa680848642633 经lint及63项Python检查通过，已提交推送3c4c43e。只暂存project_installation的旧manifest兼容行，新Gemini接入与处理桥改动保留在工作树。临时导出的源候选随task清理，复用已有工具链/官方源码，无第二业务库或模型副本。第二批消息/召回/V3候选集中独立Go test/vet/race、Python检查；其他会话正在实施的Gemini模型桥不并入本批。

第二批已提交推送2f7d8ac。独立候选tree b4f4fa98b8b340342a4a00b9c8abd11337fad523 的lint、完整Go test/vet、统计/搜索相关race及9项统计/诊断Python检查全部退出0；随后只修改5份Markdown，核对全部提交源码与已通过候选一致。70文件含此前本功能未提交的生产代码和相应行为测试，私有证据、其他会话Gemini源码及其未完成处理桥保持未提交。第一批10文件与第二批70文件各自独立验证，不能用整体工作树通过替代净候选证明。

当前本轮源码与环境复核已收口，剩余V3 Native返回/切日/搜索/分类/配置取消/画像浏览仍受工具prepared故障阻断；原工具失败计数不重置，不反复重开日常栈。当前为dev、唯一日常库保留，未做release、无新依赖下载／索引重建／旧消息重放。正式审查修复对照见[审查闭环](consolidation-review-20261119.md)。
