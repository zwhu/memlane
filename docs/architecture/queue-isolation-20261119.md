# 保存队列异常范围与回归用例

用户最新要求：官方规则先保持原样，不随意改拦截；检查我们的保存链路是否还有单条/单会话异常阻塞全部保存的情况，并补齐测试。原Key规则补丁源码和TraeX运行制品规则均已恢复官方原样，原3条已经保存不回滚。最终队列代码的同库加载记录在本文末尾，保留原材料与在途任务边界。

测试流程：bits-unit-test-gen Step1完成，复用已有utree；TMP_ROOT=/var/folders/nv/g6m5_3nx1r9_v9dk3zh_5zw40000gn/T/tmp.NdTfQcVcqv，EXEC_SOURCE为空。Step2完成，LANG=python，项目惯例unittest/IsolatedAsyncioTestCase，Python3.12已有环境，命令通过task.py，不新增依赖。biz_spec为本轮用户两项指令及现行AGENTS、product与工程workflow保存独立于整理、未知/原编号/权限/所有权规则。

## Step3 TARGETS

按用户指定保存链路处理模块，分组记录已有diff与无diff同族实现；不是全仓库审查。目标均是手写业务文件，未选安装器、生成代码或类型声明。

| 文件与目标 | 来源与范围 | 当前证据定位 |
| --- | --- | --- |
| plugin_capture.py:drain，wire/send直接依赖 | non_diff，同族正常续存实现；explicit | drain79–174，HTTP失败break，发送前后异常使用同一出口 |
| project_backlog_worker.py:review/save/check | diff，现有筛选隔离变更及其直接发送路径 | review43，save114，hunk旧159–160→173–179只隔离筛选；同循环HTTP/异常return仍停余下消息 |
| project_backlog.py:run/automatic_tick/_execute/close/public | diff，筛选恢复与部分整理接续的直接消费方 | run371，result非completed后return；automatic_tick481在发送扫描前await process_saved；_execute273进程结束与进度校验 |

## Step4 BUG_MAP

| 编号 | 分类/级别 | 触发与当前错误结果 | 正确期望与责任位置 |
| --- | --- | --- | --- |
| QI01 | Error Handling/P1 | 正常buffer含一条被服务400/409/413/422拒绝的记录，drain标记整个会话capture_request_rejected并break，后续健康消息不发送 | 原编号/正文保留，明确拒绝只隔离该条，不反复重试；同队列其他已获授权记录继续。plugin_capture.drain HTTP出口及worker.save同族 |
| QI02 | Business Gaps/P1 | 已冻结多会话，第一个会话保存因权限/策略/传输暂停，run直接return，后续健康会话不处理 | 受阻会话保留状态，其他已核对健康会话继续；身份/进程/统计完整性未知的必要整体屏障保留。Backlog.run非completed出口 |
| QI03 | Error Handling/P1 | 有效采集字典中的不可序列化载荷（包括非有限数字，当前stdin/cache JSON读取未拒绝），wire发请求前失败，被当未知发送后break，后续正常记录不发送 | 在真实POST前验证可序列化，不伪造已保存；该条元数据隔离、原载荷保留，真实回信解析失败仍按未知原编号核对。plugin_capture.drain stamp/send，worker.save sanitize/stamp |
| QI04 | Business Gaps/P1 | 一条旧整理提交依赖尚未返回，automatic_tick在发送扫描前同步await，新的可保存消息也等待 | 复用原官方处理调用并独立跟踪一次在途提交，保存调度不等待整理；原处理所有权和关闭边界保留。Backlog.automatic_tick与process_saved调用 |
| QI05 | Business Gaps/P1 | 手动项目补存start遍历多个Agent/会话时，任一checked_target拒绝便raise，健康目标没有进入run | 单独登记被拒绝目标及原因，冻结并保存其余已核对目标；本操作不因随后授权变化补交原拒绝目标。Backlog.start目标接纳循环 |
| QI06 | Boundary Errors/P1 | 保留屏障测试确认run未核对worker最终返回计数，分类rejected/deferred未纳入校验，running返回可留下无活动任务的运行状态 | 校验原冻结计数守恒及分类子集后才发布；最终非terminal/异常返回保持整操作未知屏障，不呈现错误saved。Backlog.run进度和最终结果入口 |

QI01在未改动正常sender中独立成立；worker的筛选新hunk已表达单项隔离而HTTP同族出口尚未贯彻，依用户明确要求审查。QI02是该发送出口的直接消费方，现有diff只恢复筛选暂停未覆盖其他暂停；QI04是新增partial接续的直接消费位置，其同步等待会阻塞发送。错误分类不因是否容易测试改变。QI03使用当前Python JSON/capture真实解析路径可产生的非有限值，禁止以人为传None反推缺陷。

TARGETS增补：Backlog.start为run直接调用方，用户明确检查整条保存链路；源码189–216 checked_target在目标遍历中无局部处理，权限不一致是实际项目多Agent正常场景。Step4在补新用例前登记QI05，区别于发送期间QI02，不由覆盖率或通过率反推，不更改其他既有缺陷归因。

Step4复核增补QI06：Step5保留屏障用例实际出现错误最终计数/分类计数被接受与running状态悬空；回看责任代码，只有流中基础计数与filtered被验证，最终返回未再次验证。按原明确保存完整性契约确认，独立于消息异常传播QI02追踪，不以测试失败本身作为缺陷依据。

其他边界须回归但不认定新缺陷：官方筛选拒绝及Key保持；旧无编号unknown不重发；401/403约束对应主体/会话；原编号丢回信退避及重开；snapshot失败隔离；用户停止；发送lease变化、sender退出未确认、进度损坏必须阻断受影响受管操作；既有无定位拒绝不能猜是哪条。明确错误与未知提交分别保留。

## 执行与数据边界

Step5按模块生成→主Agent执行→归类，测试技能负责测试阶段，用户已授权的生产修复独立执行，不为全通过放宽断言。失败按QI01–04及各自具体回归记次数，原CAPTURE-KEY01撤回不清零。先正常sender，再工作器，再管理调度，补相关UI投影检查。必要状态仅加在原官方buffer/原jobs中，不复制正文，不新建内容库；原编号和来源不改变。仅该记录的明确拒绝停止重试，无法归因的旧会话阻塞状态仍保留；新异常继续通知，历史知晓不代表保存成功。

本节初始状态：Step3/4完成，Step5待生成。当时官方规则制品恢复与同库加载尚未执行，在途模型/后台非零，未强行停止。后续完成事实以各阶段及末尾记录为准。

## Step5 正常续存模块

版本兼容增补：新per-entry状态不能被已加载的旧正常sender忽略。保留旧版本源码作测试夹具，新失败在原policy blocked字段标记entries作用范围；旧sender继续遵循它的原会话屏障，新sender/worker按已有单项状态处理。无定位旧blocked无scope仍不能解除；原block若为write_result_unknown留存后恢复，不抹除原未知事实。新增两种旧sender场景核对拒绝/未知不被重复发送及解决后解除局部兼容标记。此为同族sender新状态契约的必要兼容验证，不修改官方筛选规则。

Writer按技能生成test_plugin_capture_drain.py，5个入口25个subTest；主Agent执行round1，12个子场景失败，稳定映射QI01（拒绝/传输与回信错误停止健康尾部）及QI03（发送前非有限JSON序列化失败阻塞后续）。其余权限、官方筛选、旧未知、lease、预算与精确trim通过。原失败日志queue-drain-round1-20261119.log保留。

测试阶段按core-loop记录已确认问题；随后生产修复依据用户明确授权独立进行，不把测试技能的测试专用边界用来停在发现问题。新增必要状态仅放在原buffer的capture_rejections/capture_retries：拒绝保留且不自动再发；编号未知逐条退避；其他已授权健康条目继续；原JSON载荷与编号不改，409不抹除旧unknown。POST前可序列化检查与回信后的JSON失败分开。401/403及旧无定位阻塞继续是会话屏障。round2同25场景通过（11.345s），失败与修复后验证分别保存。

官方规则源码改动已撤回；原制品restore_prepare/activate成功，TraeX政策文件SHA894e13e3…与原官方文件逐字一致，revision182。原3条已保存记录没有删除或回滚。恢复前使用无在途快照，dev.py正常释放日常库后同库启动，未换业务根。其他源码/模型/账号及既有资源保留。

## Step5 工作器、调度器与Native

工作器新增8个入口、35个subTest，使用真实save→原编号send与官方buffer，仅控制HTTP/时钟等外部边界。round1有17个失败；round2剩旧浅夹具第四槽None和401/403权限屏障。夹具改为真实契约字典，生产保存复核原hold，round3通过（含历史共18个入口）。QI01/03与各自回归按具体触发分别保留，不把两种失败合并凑4次。

调度器新增6个入口、27个subTest，覆盖跨会话3、最终计数完整性11、公开投影3、独立整理提交3、进程退出4、手动接纳3。round1有10个失败、3个错误；round2只剩旧全项目退避断言；调整为会话级save_retries后round3通过（含历史共22个入口）。QI02隔离会话save_status/save_reason；QI04一次在途processing_task独立跟踪原operation/target；QI05登记原admission_blocked，不因后来授权补交原拒绝目标；QI06核对最终terminal状态与冻结计数守恒。身份、lease、进程与完整性未知继续暂停受管操作。

真实SDK/SQLite/三种prepare制品协议回归通过，仅更新旧全队列drain_fail_at断言为逐条capture_retries/capture_rejections，不放宽编号、去重或unknown保护。Native增加与原buffer签名绑定的拒绝/退避计数；拒绝unknown不误作缺旧凭证，不重复黄点。初次新拒绝提示重复黄点1次，修复后定向Go通过（0.810秒）。全Go初次失败为旧project_buffer_test夹具把状态设为None；同样改为官方字典契约，原安装摘要、只读内容与精确hash断言保留，属于方法错误，不计产品缺陷失败。

## Step6、Step7 与最终验证

Step1–5完成。Step6按技能条件跳过：EXEC_SOURCE为空，cov_config=null，CHECK_COV_MODE=skip，无用户或项目CI覆盖率门禁，不安装新coverage依赖，不声称覆盖率已达某百分比。Step7已执行utree flush并本地落盘，FAILED_REASON为空；测试技能入口为`/Users/bytedance/.trae/skills/bits-unit-test-gen/SKILL.md`。

本轮新增89个Python子场景（正常sender25、worker35、manager27、旧sender兼容2），89/89通过；统一受影响Python回归共72个unittest入口，54.889秒通过，日志`build/diagnostics/queue-isolation-final-with-compat-20261119.log`。最终Go全量test通过（bootstrap11.329秒、simpledesktop5.324秒），日志`queue-isolation-go-all-final-20261119.log`；Go vet和模型参数/桥接展示lint均通过。原失败日志继续保留，无release、无提交或推送。

## 最终同库加载与证据边界

加载前bridge active_or_waiting=0、官方后台0、图任务0；原registry/jobs/incremental/配置及相关buffer共7个文件已私有备份，未为运行另建库。原owner99883正常stop得到`Owned service groups exited; daily store released.`。新owner62711、completion62806、official62921、control63274同一`.local/daily/runtime.json`及`official-store` ready；Native由统一MyGo dev watcher构建，1.8秒成功，无完整release包。

随后通过管理API以独立持久queue_prepare/queue_activate操作准备并启用现有TraeX，revision184→186、状态`configured_pending_new_session`。制品为`.local/daily/managed-marketplaces/traex-7859e85d2cf88d393366e77e/7e2da4290aa1d7efc3fa/plugins/cognee`；`_capture_policy.py` SHA256`894e13e3c145fc621b1e855e3f250c2a0ac019c6a6840017d91b0b1584abbe3f`保持固定官方原样；`_memlane_capture.py` SHA256`252d066b1076606bc3bb43a457be71e0638a1c9f7d0c45967883e3db3cdf62aa`与已验证sender源码相同，manifest`e385b11528c15bf3554b0eb18004ea4e746c08e3354e59d95830c60c5a0d680e`。新宿主会话加载仍按官方接入回执确认，不能把prepare/activate等同当前旧会话热更新；本次没有顺便改其他Agent插件配置。

真实管理回执已含新save_rejected/save_deferred字段；memlane原自动操作6497a692…继续watching、automatic_enabled=true、remaining0、review_failed0、processing_failed0。原20条unknown20、完整签名/legacy_sha均为`b9b8c61883c02b7eecc4c7135543dcda79b0d94a7f7946f6df53ee83daf85885`，前后不变；旧缺映射TraeX仍unlinked，没有猜测绑定或补采。原三条以备份中原_capture_id再次只读查询，3/3 confirmed=true，队列pending0。保存回执与处理完成分开；当时尚有1项pending_processing，不把它声称为全部知识生成完成。

前后元数据及只读原编号回执位于私有`.local/daily/save-processing-recovery-20261119/queue-final-*.json`与`key-policy/queue-final-original-receipts.json`。历史知晓文件及8项目yellow_remaining=false清理收据仍在；本轮不重新清空此后新出现的异常，不将旧未知标为保存成功。

Native验收边界：本次重载后Computer Use未取得可交互窗口，open_app又返回用户已手动交互提示，刷新仍无窗口；按工具规则停止操作，未通过shell/AppleScript或另一自动化绕行。此前实际消息卡/历史操作证据保留，本次源码投影及完整Go行为验证通过，但未新增重载后Native视觉通过结论。官方规则与队列隔离通过临时真实buffer/SDK/SQLite和制品协议验证；没有把异常载荷投进日常库人为制造拒绝。

## Native窗口查询复核（用户追问后补充）

此前“未取得可交互窗口”是Computer Use默认窗口查询的失败，不能推断为Native未启动。当前只读复核：原owner62711保持ready、MyGo watcher63282及Native63413自19:30运行，UI日志构建成功、无退出记录。Computer Use list_apps同时列出两个同bundle `io.github.zwhu.memlane.dev` 的应用记录，其中一项有可见窗口326666（Memlane，1200×820），另一项无窗口；只传app的get_app_state仍返回无可交互窗口。随后使用同一工具明确传入windowId326666，成功取得真实Native截图和完整AX控件树，进程63413与运行所有者一致，当前总览显示服务已连接。

已确认的问题是默认窗口定位失败；工具内部究竟选择了哪个重复应用记录未进一步验证，不将此推断为窗口被用户关闭、Native崩溃或整个Computer Use不可用。此前未按list_apps实际窗口ID完成进一步核对，故原验收边界不够充分；保留原失败输出并以本节补充更正。本次没有重启服务、修改生产代码或操作业务任务。当前截图/控件树证明Native窗口可被正确定位和读取，不替代此前队列隔离的功能回归，也不声称已重新跑完全部页面视觉验收。

## 用户实际新会话：加载与原编号保存通过

用户已新建TraeX会话后，按verify-continuous-scenarios核对真实上游产物，不创建或发送人工验收消息。实际新会话`traex-2357edff…`属于item-fe，安装代次`1.8.0+memlane.7e2da4290aa1d7efc3fa`；管理侧authenticated_host_resolve回执的environment_verified/artifact_verified均true，宿主实际cache中的_capture_policy.py SHA保持894e13e3…官方原样，_memlane_capture.py SHA252d066b…与最终sender源码一致。

正式会话capture-status按原TraeX主体、session和item-fe dataset核对，有1条QA、2条trace。只读官方cache.db中该会话3条capture凭证，再逐条调用原message_id的GET回执：3/3与原已提交凭证完全一致，protocol1、status saved；未调用PUT或重发。原buffer同一官方session map核对后pending0、unknown0、rejected0、deferred0。此证明正常新会话已经加载新插件并实际保存，不是仅凭安装成功或队列为空判断。

状态解释纠正：管理配置的status枚举继续为configured_pending_new_session，NativeConnection.review本身不将正常配置记录改为另一个终态；实际接入证据是new_sessions1、environment_verified true与capture.state healthy。此前只读原status标签便报告“还等待新会话”不够完整，现以实际加载及保存回执补充完成结论，不为改标签重新prepare/activate或修改宿主配置。保存主体、项目dataset和采集健康都已核对，不能把item-fe历史管理侧补存关闭解释为此次新会话未保存。

小型私有元数据回执：`.local/daily/save-processing-recovery-20261119/new-session-live-receipt.json`与`new-session-numbered-receipts.json`；只读驱动`build/diagnostics/new-session-verify-20261119.py`由task.py执行。原20条unknown与历史提示保留规则不变，本次没有重启服务或调用模型，不将原始记录保存称为已生成新知识。

Native连续操作边界：指定326666能读取真实窗口截图/AX树，但两次click都被Computer Use的“not prepared”门禁拒绝，紧接get_app_state仍可读取；第二次后停止点击，没有改用shell/AppleScript、perform_action或另一自动化绕过。由此只证明窗口读取可用，不能把上节定位修复扩大为所有点击可用。新会话的Native Agent状态/原始记录实际页面对照尚未完成；工具准备/方法问题独立于QI01–QI06，不计作保存产品缺陷验收失败。遵循`/Users/bytedance/.trae/plugins/cache/traex-bd-plugins/computer-use/0.3.2/skills/computer-use/SKILL.md`的“Stop and report persistent Code Mode, transport, permission, screenshot, or signing errors.”。
