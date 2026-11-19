# 保存筛选隔离与部分整理接续

2026-11-19，用户明确“修复吧”。沿当前唯一日常库和dev入口修复，不进入release。承接消息操作及当前实际账本：memlane自动任务6497a692…因capture_filter_changed暂停；01a1ea43…整理记录为unknown/incomplete_drain；旧20条工具记录无唯一凭证继续保留。

## 影响、连续用例与边界

目标：过滤记录保留且不阻断可发送记录；只对已确认的可恢复暂停重新核对原范围；官方已提交但未排空的整理单独呈现并沿官方冷却、水位及去重接续。读失败、真实未知提交、明确拒绝和用户暂停不能借此解除。安装、采集、保存、整理提交、整理完成及知识产出分别核对。

生产改动限定project_backlog_worker.py、project_backlog.py、incremental_processing.py及Native消息状态/后台投影；复用安装插件的_capture_policy、同一buffer、消息编号和官方整理接口，不改活跃插件缓存或另建采集器/内容库。原队列过滤部分保留，后台仅返回对应快照的计数/散列；UI只在快照匹配时解释数量。历史incomplete_drain按官方明确契约纠正为部分提交，保留旧分类和纠正依据；其他unknown/submitting不迁移。旧TraeX映射只读核对，不能凭缺文件造会话或补采。

连续用例：过滤trace在前、正常QA在后→QA保存而trace保留；冻结后筛选变动→单项隔离、其他发送；权限/用户暂停仍阻断；旧过滤暂停→重新读当前范围→同操作恢复、同库回执；partial→冷却等待→尾部保存→官方增量接续；启动接受/正在提交不计失败；原真实unknown保持；旧20条原内容/标记不变。先定向反馈循环，再集中修复和受影响检查、全Go/lint，最后核对在途及owner后正常重载同库、真实积压回执及Native。

当前关键原因已通过只读账本与源码定位；诊断阶段沿diagnosing-bugs以真实工作器调用及受管后台测试构成秒级反馈，避免靠新日常写入试错。保存筛选范围不一致、partial解释错误、正在提交误计失败分别复现与计数，不与旧MSG-ACTION或DEV-STOP01合并。原失败及资源保护继续。

运行入口.local/daily/runtime.json；本轮开始owner158及160/161/367服务ready、日常库official-store。PID仅是当前快照，启停前重新核对身份和在途任务。磁盘79GiB满足50/55GiB门禁。其他会话environment/generation设置修改保留；不擅自重启其在途任务或抢占Native。长任务及慢模型通过原操作持续观察，观察预算结束不等同失败或允许重投。

## 状态

已实现并加载同一日常库；源码、后台恢复及真实提交接续通过，最终整理产出仍按官方在途终态核对。

## 修复与检查

冻结和发送复用已安装官方_capture_policy；冻结时只选择允许发送的条目，冻结后单条筛选变化只退出该条发送token，原记录保留，其他记录继续。原capture_filter_changed自动任务重新核对当前权限、绑定及快照后恢复，同操作ID保留；用户暂停及真实未知/拒绝仍阻断。仅返回按原buffer完整签名绑定的过滤计数，Native快照不同即不套用旧计数，不把原记录标为保存成功。

官方incomplete_drain明确是已提交但材料未全部同步，改为partial；后台与增量触发沿原冷却、水位/去重等待尾部保存并接续。历史同原因unknown在加载时保留previous_processing与recovery_reason后纠正，其他unknown/submitting不改变。submitting只计活动，不计失败；Native分别显示部分提交待接续、正在提交和真正失败。

通过：61项Python保存/接续/回执检查（26.079秒）；Go完整test/vet与lint，历史提示更新后的最终全Go再次通过（bootstrap17.275秒、simpledesktop6.099秒）。原日志分别为build/diagnostics/save-processing-final-tests-20261119.log、save-processing-with-history-go-all-20261119.log、save-processing-with-history-go-vet-20261119.log。无release包。

初次确定性复现：FILTER-SCOPE01、PARTIAL-STATE01、ACTIVE-COUNT01各1次失败，修复后通过。恢复用例的空installation夹具被真实调度器跳过，共3次命令带有此方法错误；改为已配置安装后通过，未把这3次方法错误记为产品恢复缺陷。初次失败保留原工具输出，未覆盖；旧MSG-ACTION、DEV-STOP01等计数不变。

## 同库恢复与真实证据

重载前bridge active_or_waiting=0、官方background=0、图任务0；已备份registry、jobs、incremental与相关原队列，共7个私有文件。原owner158正常stop返回日常库释放；新owner63204、completion63239、official63311、control63482同库ready。旧Native watcher73597按内核启动时间/命令确认后正常TERM收尾，当前Native由本轮dev.py open --watch-ui附着，未新建业务库、升级活跃插件或改变Agent/Provider配置。

原自动任务6497a692…由paused/capture_filter_changed变为watching、automatic_enabled=true、remaining0。TraeX171d…原队列实际3条，全部被官方规则筛除；前后完整队列签名一致b997195c…，材料保留。另一个当前TraeX队列1条也被筛除，不阻断项目。原Codex20条完整签名与legacy_sha均保持b9b8c618…5885，数量20、unknown20不变。旧TraeX2f6d…仍无官方session map，未凭猜测补绑定或补采。

原01a1ea43…partial在原调度器等待后真正接续，processing_submitted4→5、partial1→0、pending1→0、failed0，保留unknown旧分类与纠正依据。已观察到对应主体/dataset的官方管线阶段终态，根后台仍需以实际在途回执核对；不把提交接受、子阶段结束或黄点消失当作全部记忆生成/质量通过。元数据与队列核对位于.local/daily/save-processing-recovery-20261119/，不提交真实材料。

Native通过Computer Use实际进入memlane项目设置，核对自动保存已开启、3条及1条“因采集规则未保存”、部分提交等待接续，随后部分提交卡随真实提交回执消失。原始窗口完整树/截图保留在本会话工具结果。工具scroll两次没有取得阅读位置变化，作为方法边界保留；未另改页面布局，不将离屏卡片截图当完整视觉通过。用户期间也操作了窗口，按工具提示重新读取状态后继续，不推断用户点击了某个保存操作。

## 用户新增：清空全部历史问题提示

用户明确“然后把我过去所有有的问题都清空吧”。按本轮提示语义，清空所有项目现有历史提示与黄点，原消息、记忆、失败证据和问题计数保留；不停止正在执行的整理。已知晓项收进默认折叠“已知晓的历史提示”，可展开恢复提醒；新消息标识/新原因仍重新提醒。源码轮询重新读取本机知晓记录，让本次全项目处理同步到现有窗口。

使用一次性Go操作驱动读取真实目录、原插件计数/签名和后台回执，调用既有acknowledgeNotice写入客户端状态，不调用保存/删除/停止接口。驱动实际6.88秒结束，8项目yellow_remaining均false；memlane4项、item-fe2项已有历史提示全部知晓（本次新增3项，其余用户之前已知晓）。原知晓文件先备份，回执在reminder-backup/summary.json；驱动完成后归档build/diagnostics/notice-cleanup-live-20261119.go.txt，不留可默认操作日常库的测试。生产组件回归覆盖提示退出当前区、折叠历史、恢复提醒和新异常出现。

保留当前同库后台及UI，旧20条和旧会话未确认状态只结束提醒，不标为保存成功；不清空工程失败账本，不清理运行依赖。

## 用户追问：3条记录为何被过滤

只读逐条核对发现新的CAPTURE-KEY01：3条全部由官方_capture_policy._command_reads_secret的*.key文本匹配触发，采集开关开启、工具在允许范围、无工具deny、无显式敏感path参数。Bash记录的命中词为.Key，实际来自Python脚本中替换Go源码的ui.Row(c).Key(...)文本；两条apply_patch命中补丁正文的c.Key。函数将整个command/补丁正文拆词、转小写后匹配文件名，未区分代码字段与真实文件路径。因此这3条是规则误判，不能描述为真正读取了密钥文件。原3条队列前后仍保留；此次只读定位，没有重发或放宽*.key规则。CAPTURE-KEY01尚未修复，与FILTER-SCOPE01单条阻断整个项目分别记录；本次未执行修复验收，不新增验收失败轮次。

## CAPTURE-KEY01修复与实际保存（覆盖上节尚未修复状态）

归因更正（用户追问补丁归属后）：此前把“.Key误判来自我们的适配”说得过头，混淆了根因与异常放大。原版官方_capture_policy对标准Bash/补丁输入已可复现误判，官方自己的drain_warmup_entries也调用同一allow_tool；错误传参没有被证实，不能把调用本身判为错误。Memlane自有缺陷是单条过滤暂停整个项目，已在project_backlog_worker修复；Key文本误判根因是官方原函数，修正在既有固定插件补丁。下文先前“共同形成／适配”表述保留历史，不覆盖本次更正。此纠正不改变已有失败证据、具体问题计数或原3条保存回执。

用户明确“如果是你，你就修复掉”，随后质疑“为什么要适配”。已确认_capture_policy文件保持官方提交a1762489…的原SHA894e13e3…，而Memlane后台调用从65401ade引入；官方函数过度拆词、Memlane没有识别工具输入差异共同形成此症状，不归咎用户。撤掉准备新增的plugin_capture.allow_tool包装函数，plugin_capture.py最终无此差异。

最终沿现有project_capture_patch.apply_policy修正同一个官方函数：apply_patch检查保留输入的实际文件头，正文中的c.Key不当成路径；补丁正文因采集截断缺结束标记时仍按文件头核对。Python源码编辑脚本只从文件检查中排除已解析的字符串replace操作中的Go.Key文本，真正的Path/open文件名、shell命令、未知语法仍走原敏感路径规则。原工具allow/deny、采集开关、显式敏感path字段和sanitize保留，不新增采集器、过滤服务或内容库。project_installation把该修改放入新不可变插件制品，旧缓存不原地改写。

验证：49项真实官方规则/截断补丁/真实敏感文件/显式拒绝/原采集回执与调度检查通过（26.094秒），源码lint通过，最终Go全量通过（bootstrap13.014秒、simpledesktop4.951秒）。原3条实际内容只读套用候选规则，均可放行。首轮规则回归复现误判1次；原数据干运行发现一条截断补丁未放行，累计第2次，补文件头规则后通过。自定义deny-path夹具用了官方不匹配的相对glob、只读脚本缺Path导入分别属方法错误，保留输出，不计该产品失败轮次。

通过管理接口准备/核验/激活现有TraeX，保留主体、HOME、原队列、项目权限、账号及其他插件。新制品a07dec57781a9440fb54，policy SHA b4ff9538…；激活后revision178、configured_pending_new_session。管理操作原ID及旧registry/env私有备份在key-policy/，未提交本机配置。源码构建入口对Codex/pi/TraeX共用；本次实际部署与保存证明覆盖TraeX，不声称其他现用宿主插件已同时热更新。

原TraeX171d…三条均保留原_capture_id，在官方原编号查询中confirmed=true，原队列pending0/unknown0/filtered0；这是实际持久保存证据，不是删队列或只看后台累计数。回执original-receipts.json已保留。后台原操作6497a692…仍watching、remaining0、failed0，后续日常新材料继续保存。旧20条unknown数量20、内容及标记SHA b9b8c618…5885不变，旧TraeX2f6d…未造会话、未补采。历史问题清理结果继续保留，正常新增异常不被压掉。

Native使用本轮现有watcher，规则源码变化触发正常热更新；原始工具树/截图保留。本次不是release，无完整包。当前日常后台及运行依赖保留，实际后续整理的在途工作不被这些只读核对停止。

## 用户最新纠正：官方规则保持原样

用户明确“如果官方就是这样设定，那就先这样做，不要随便改，万一拦截错误呢”。上节Key规则修正是已撤回的历史实现，不再是当前方案。`project_capture_patch.py`的规则变换及`project_installation.py`对应调用已全部撤回，实际TraeX制品也通过管理prepare/activate恢复：`_capture_policy.py` SHA256为`894e13e3c145fc621b1e855e3f250c2a0ac019c6a6840017d91b0b1584abbe3f`，与固定官方原文件一致。原三条实际保存回执保留，不删除数据；原CAPTURE-KEY01归因、失败计数及撤回事实保留。

后续只修复Memlane保存队列中单条/单会话异常放大的问题，不修改官方采集允许/拒绝规则。范围、QI01–QI06、89个新增Python回归场景及最终同库加载见[队列隔离交付](queue-isolation-20261119.md)。原20条缺唯一凭证的unknown仍保留，不自动重发、不补造编号；历史提示知晓不代表这些消息已经保存。
