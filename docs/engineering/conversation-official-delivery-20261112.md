# 会话阅读与官方数据交付账本

最新收尾：用户明确要求交接后暂停此旧Goal、统一按新规格实施。当前新知识封装读取兼容已完成源码／样本／受影响测试和开发编译，尚未新格式Native或日常服务升级验收；全部原UI证据与失败计数保留。共享日常服务未停止，未清理数据或其他session产物。接手以[完整交接](conversation-to-coding-memory-handoff-20261112.md)为准；历史“Goal继续”描述保留当时状态，不覆盖本次用户暂停。

最新知识UI切片：049真实发现知识标题随正文滚走，失败1轮；050／051同一候选修复固定标题与状态、两行标题、窄窗口信息图标及列表选中条。普通／窄窗口实际读取既有两篇官方真实学习知识→2QA／3trace来源→返回；窄窗口非零位置47.261353、查询／选择／信息展开保留。普通来源31秒309帧，窄窗口跳转前／后、真实服务暂停失败／同PID恢复各31秒309帧，像素及阅读状态通过对应范围检查。数据、原任务和日常依赖保留，自有进程收尾；无新模型或完整包。详见[知识专项](official-knowledge-reading-20261112.md#native050051固定知识标题与真实出处返回)。正常自动采集→新知识处理、日常切换及成套交付仍未完成，Goal继续。

最新UI切片：Native044–048完成工具原值显示、实际跨消息文字选择、Markdown代码及工具命令行末横滚。046／047同一冻结候选在窄／普通窗口各31秒309全帧，16次刷新保持正文／标题／数量和X=MaxX；查询零结果不清空当前阅读，048实际按钮复制7999字节与原文一致。047漏点实际复制按钮的失败回执保留，未冒称当次通过。原consent／receipt／binding摘要未变、客户端正文镜像仍0；自有GUI／daemon／官方进程收尾，日常服务及数据保留。详见[专项账本](pi-official-capture-gap-20261112.md#native044048工具原值跨消息选择与完整横向阅读)。四稿整体、知识新链路、运行入口及正式迁移仍未完成，Goal保持active，无完整运行包。

空列表补充任务已闭环到当前开发候选：保留当前库／查询的有效空回执，后台读取及暂时失败不替换空态；固定故障反馈，权限失效撤下，保留草稿。032普通窗正常／中断／恢复各31秒309帧，三阶段空态文字像素相同；033窄窗项目空引导31秒309帧，栏布局稳定。desktop完整30.896s、定向race4.057s及vet通过，未重跑停止中的旧hook／全仓测试。详见[修复与证据](empty-list-refresh-observation-20261112.md#修复与同候选自验)。旧数据及日常依赖保留，自有进程收尾；官方自动采集、迁移切换等Goal剩余项保持未完成，未构建完整运行包。

当前增量：无本地知识且已有官方读取授权的项目，可从知识页签直接进入Cognee内容；未迁移知识、草稿、未知数量与仅提交授权不切换。030普通窗、031窄窗真实读取既有两篇SDK学习知识，出处2QA／3trace可读；031取得返回前后89.638306非零滚动、两轮各31秒309帧且正文跨轮像素一致的证据。详见[知识页最新记录](official-knowledge-reading-20261112.md#默认入口与真实非零滚动返回)。这不是正式迁移／自动采集／全部UI完成。全仓vet通过；desktop完整35.309s及定向race通过，全仓test仍因旧hook桥接探测失败。历史至少4轮，已按[停止记录](codex-hook-bridge-stop-20261112.md)停止该具体问题及全仓重跑，其余独立工作继续。自有服务收尾、源库与日常依赖保留，完整包推迟release。

最新增量：开发阶段不构建或验收完整运行包，待用户明确启动release阶段。旧31条完成隔离正文／出处导入、重复送达与重启核对；原库未删、默认入口未切换。迁移知识→原始文档入口、五包完整测试、定向race及vet通过，真实官方1条出处身份和全文摘要通过，尚未Native验收。原标题遗漏的上一轮判断已纠正：31条官方label均与原标题一致，无需重写数据；MIGRATION-TITLE-001留作验收判断错误，产品失败计数0。详见[迁移记录](official-data-migration-20261112.md#隔离迁移与原文出处读取增量)。自动采集、编辑历史、默认入口合流、Native连续观察及全仓门禁仍待完成，Goal保持active。

设计基线：`02f038f`，已实际打开01–04四张图。完整范围以 [Goal](../goal-conversation-official-data-20261112.md) 为准。本账本不将局部检查替代最终交付。

## 连续主流程用例（实施前）

| 编号 | 连续操作及前置条件 | 预期与禁止变化 | 所需证据／当前状态 |
| --- | --- | --- | --- |
| O01 | 隔离合成仓库，App安装官方插件→宿主信任／重载→普通对话及工具→官方保存→App阅读 | 不靠memory_save补写；旧hook不可双采集；第三方配置不变 | 固定插件／服务版本、自动采集回执、Native；待实施 |
| O02 | 同一合成根官方处理→知识正文／状态→出处问答／工具→返回 | 官方真实来源；保留选择与滚动；未产知识如实记录 | 官方回执与Native；待实施 |
| O03 | 新旧读取切换→App／Agent重开→新增→重复→服务短暂失败恢复 | 迁移核对唯一内容、用户编辑、来源与授权；不删旧库／历史 | 迁移清单、回退与逐次回执；待实施 |
| U01 | 普通1200×820／最小800×620窗口→会话→工具展开→长Markdown→复制→来源返回 | 四稿对照；代码横滚、列表／正文独立滚动；不造日期或关联 | 四张实际窗口、键盘／选择／复制；待实施 |
| U02 | 空列表→零结果→首次失败重试→已有正文刷新失败→恢复 | 局部错误；已有有效内容保留；明确撤权或来源失效立即撤下 | 定向检查及Native；待实施 |
| R01 | 阅读中连续30秒／至少10轮刷新→临时超时→重试→明确撤权→切会话晚回执 | 保留阅读窗口、搜索／焦点／选择／滚动；晚回执不覆盖新视图 | 连续像素采帧与状态回执；待实施 |

用例使用隔离库、自有合成会话，不扫描用户历史或复制凭据。最终还需受影响测试／必要race、全量test／vet、完整依赖核对、独立输出包、可启动产物与启动方式。受保护helper／图谱问题不重跑。

## 本轮起点核对

- 工作树已有大量未提交UI／采集／设计修改，全部保留；未清理任何运行包。
- 2026-11-12 可用空间约133 GiB，后续命令受task.py门禁。实际daemon76077及其临时目录内worker／官方服务仍运行，开发Native96359仍运行；这些路径不作为清理候选。
- 现有官方适配固定Cognee1.6.7、Codex插件1.7.7，安装器当前只覆盖受控项目隔离home；不能据此声称日常电脑级官方迁移完成。
- 刷新既有累计失败1轮，见[原复盘](reading-refresh-flicker-20261112.md)。本轮发现临时IPC错误仍清空Result／Window，尚不满足Goal；明确来源失效继续拒绝。已有9秒AX观察不能替代30秒像素验收。

## 完成边界

本轮尚未完成Native交付、官方日常采集迁移或最终包；Goal保持active。进度与失败计数继续追加，不能重置。

## 官方覆盖矩阵与读取契约核对

上游固定提交仍为`28fde88150661444a0b8ac69cc81ee0e41ec8f5a`。实际通过GitHub tree、inventory及插件manifest核对；小型原始证据及SHA256位于`build/conversation-official-20261112/upstream-contract/manifest.json`，不安装或改写日常配置。

| 当前产品Agent | 上游证据／安装入口 | 本机现状与本轮边界 |
| --- | --- | --- |
| Codex CLI／Desktop编码 | 官方Codex插件，marketplace安装；上游manifest为1.8.0 | 现有隔离安装器固定1.7.7，不得将1.8.0新采集字段外推；日常自有hook尚未迁移。CLI／Desktop要分别验收 |
| pi | 官方inventory列出社区维护`@kerryhatcher/pi-cognee`，`pi install npm:@kerryhatcher/pi-cognee` | 固定发布0.1.0已核对：9个模型工具、2个命令，无生命周期自动采集或typed QA／trace发送；默认SDK不复用现有服务。不能作为自动采集交付，薄事件适配仍待实现。见[具体缺口](pi-official-capture-gap-20261112.md) |
| TRAE／traex | 固定tree及inventory未找到对应集成 | 只证明该提交官方目录缺项；仍需核对宿主安装／事件机制。已有Memlane MCP接入不等于自动采集，可参照官方结构补适配的范围待落实 |
| Claude Code | 官方claude-code插件，inventory1.8.0 | 仅核对现有范围；不扩大承诺到Desktop普通聊天，不安装 |

来源：[官方固定inventory](https://github.com/topoteretes/cognee-integrations/blob/28fde88150661444a0b8ac69cc81ee0e41ec8f5a/integrations/inventory.yml)、[Codex固定manifest](https://github.com/topoteretes/cognee-integrations/blob/28fde88150661444a0b8ac69cc81ee0e41ec8f5a/integrations/codex/plugins/cognee/.codex-plugin/plugin.json)。

本机固定Cognee1.6.7源码已核对`api/v1/sessions/routers/get_sessions_router.py`、`modules/session_lifecycle/models.py`、`infrastructure/databases/cache/models.py`。正在运行的原受保护服务76867的只读OpenAPI确认`/api/v1/sessions`、`/api/v1/sessions/{session_id}`及`/api/v1/remember/entry`确实注册；回执`build/conversation-official-20261112/service-openapi.json`。未读取其会话／业务正文或凭据。

- 列表支持limit≤500／offset、range、order_by、descending；服务按主体及dataset可见性返回，列表不提供dataset查询参数。客户端必须过滤明确绑定dataset，页游标仍按上游推进；上游total不是项目总数。
- 详情返回label、msg_count、tool_calls、qas[-20:]、traces[-20:]；没有QA／trace正文分页。QA.time为官方QA创建时间，不能显示成用户和助手各自发言时间；trace模型无时间或QA关联键，须独立成组。
- `(user_id, session_id)`共同标识会话。详情接口只接session_id，所以客户端必须再次核对实际返回owner／dataset／session及前后服务instance；不凭session字符串跨主体拼接。
- 缓存不可用时官方详情仍可能200并返回空QA／trace；空结果不能证明完整空会话或明确过期。既有自定义complete_session接口能回503，但将QA拼成文本及附带知识文档，需要后续改为结构化官方输出，不能继续靠解析拼接文本来造消息。
- 新增`internal/official/sessions.go`只读适配直接读官方接口，不写Memlane全文、版本或索引。包含前后服务身份、dataset与owner核对及上游分页边界；尚未接入IPC／Native，不能标为真实业务闭环。

## 刷新问题本轮记录

关联原问题，不重置累计次数。新增测试先红：timeout、deadline_exceeded、service_unavailable、cognee_unavailable都会撤下已核验正文，为复现基线，不算修复后验收轮次。

1. 修复后定向检查：正文／窗口保留断言通过，但测试点了滚动区外的「复制全文」，失败；测试方法问题，仍保守计一次失败。改为原始记录固定底栏「复制正文」，不放宽正文／复制断言。随后定向SourceRefresh／SourceReader／首次核验检查通过（0.937秒）。
2. 全桌面包检查：29.837秒失败，`TestNextG06TypedFailuresKeepTextUnreadableAndRetryTruthful`中timeout／cognee_unavailable仍要求隐藏此前已核验正文。证据`build/conversation-official-20261112/refresh-checks-001/1.log`。这是旧测试契约与Goal新要求冲突，修改该用例为首次无回执失败仍不可读；后台临时错误保留、恢复以及撤权／来源／版本／未知错误撤下分别新增验证，不删除权限与来源断言。

连同原记录失败1轮，当前累计失败3轮。下一轮若同一问题仍失败即停止，不启动第五轮；实际Native及30秒连续像素验收尚未完成。

3. 下一轮受影响检查通过：desktop全包30.441秒、official全包0.539秒；`TestSourceRefresh` race通过；两包vet退出0。命令与退出码位于`build/conversation-official-20261112/refresh-checks-002/results.json`，三个原始日志保留。累计失败仍为3，未新增第4次失败；不能由这次通过重置计数。新增检查覆盖临时故障后原窗口／正文／搜索状态与固定底栏复制、成功恢复、明确撤权／来源／版本／未知错误撤下、关闭阅读器后迟到回执拒绝。

实现仅对显式临时安全码保留同一query／library epoch的已核验回执；首次无结果不显示旧文，未知解析或协议错误不视为临时故障。原文轮询和权限核验未取消。临时错误显示局部上次内容提示及重试。

实际Native起点只读观察发现：现有开发窗口显示「记忆读取失败」与原文服务不可用，复制禁用；不是这次源码的Native验收。没有为了取得绿色窗口重启受保护daemon/helper，也没有将旧窗口当成热更新已加载本次实现。下一步需在合成服务／会话根接入新读取与Markdown Native页面，再做30秒像素、撤权和迟到回执的实际窗口验收。全量test／vet留待这轮实现收敛统一运行，不能写成已通过。

## 续做：官方IPC与Native会话阅读初版

上一Goal轮属于progress：修改了刷新错误处理、官方只读适配和对应证据。本轮继续推进新读取，没有恢复旧125场景或helper／图谱实验。

- `records`承载会话数据契约，`serviceipc`新增`official_sessions`／`official_session`；daemon只从已持久化绑定取endpoint及私有token，前后复核现有历史会话授权和绑定代次。材料提交绑定明确不含历史读取权，保持拒绝；没有隐式扩大日常连接权限。独立token不返回UI。
- 项目原始记录点击进入官方会话阅读初版，旧材料从独立入口打开；列表、阅读区独立滚动，底栏固定，普通316／窄248列表及窄160侧栏。新读取不写Memlane正文／版本／索引，旧数据仍保留。尚未完成旧知识列表数据源切换及官方日常插件安装迁移。
- Markdown复用MyGo0.7.4的View和稳定Options，不用Stream重播；用户浅底块、Agent开放正文，工具独立成组默认折叠、真实状态／参数／结果／错误可展开；上游未提供逐轮关联时明确提示。时间只显示官方会话活动时间，不伪造用户／助手各自发言时间；详情明确各自最近20条的范围，空渠道不宣称完整空会话。
- 读取状态沿用同一会话的展开／元数据／ScrollState；后台轮询继续执行，迟到响应复核根／epoch／会话／绑定。临时失败保留，明确拒绝撤下并禁用复制；新会话挤动分页后，即使当前会话离开已载入页，也继续独立核验选定会话。
- 新用例直接验证：SQLite＋合成HTTP读取不产生memories／versions／jobs；途中断开拒绝回执；材料提交权限不发出历史请求；普通1200×820／最小800×620的载入范围、搜索零结果、工具默认折叠与展开、固定底栏复制；有效读取／临时超时／明确拒绝区分。不是官方服务或模型真实闭环。

### 本轮检查

`build/conversation-official-20261112/session-ui-checks-001`定向检查通过：desktop0.881秒、daemon3.163秒、official1.155秒；serviceipc无匹配测试，仅编译。`session-ui-checks-002`完整受影响包通过：desktop30.985秒、daemon23.900秒、official1.008秒、serviceipc0.634秒。新会话UI及daemon授权路径race、四包vet退出0。原始命令／退出码／日志均保留。

上述完整检查后，代码复核补入分页移动时独立保留SelectedRow以继续核验当前会话；新增验证在明确拒绝前移除当前列表行，仍须撤下正文并禁止复制。最终定向race／vet另记`selection-boundary-checks-001`，不能把前一组完整检查外推为这一处改动后的全包检查。

`selection-boundary-checks-001`最终结果：Conversation定向race通过（2.184秒），desktop vet退出0；失败计数未增加。末次定点磁盘仍在100GiB安全线以上，未下载引擎／模型或清理运行依赖。

### 首轮实际Native对照与未通过项

- 构建独立32MiB左右e2e GUI和轻量`.app`调试容器，bundle id `io.github.zwhu.memlane.conversation20261112`，仅合成界面夹具，无服务／Agent／模型。通过CUA实际打开默认1200×820窗口，截图2400×1640；实际滚动并展开工具组，确认原生Markdown代码／列表／表格、长代码横滚容器、工具结果及固定底栏存在。实际截图保留在本轮CUA工具回执，未补造本地图片路径。
- 首轮Native对照尚未通过，**会话UI还原累计未通过1轮**：夹具未设置scope名字，项目标题缺失（已修夹具，尚未以新包复验）；工具字符串结果显示JSON转义（已修源码解码，尚未以新包复验）；代码仍使用SDK默认红／紫语法配色、列表排序按钮及标题间距与四稿有偏差；知识出处联动、实际800×620窗口、原生选择／剪贴板、30秒连续像素与真实数据闭环仍未验收。没有用本轮静态窗口宣称刷新无闪烁。
- 旧helper／图谱与刷新计数不变，刷新仍累计失败3轮。本次UI未通过不是将刷新问题改名重新计数；后续若刷新仍失败即第4次，必须停止同问题。
- 裸二进制不能被CUA定位，随后使用独立App容器完成上述观察；不是正式包／helper迁移。通过CUA退出本轮容器，最后App inventory确认该bundle不再运行；受保护daemon76077及服务76867未停止／替换。因退出后的AX查询会重新打开App而额外出现一次无夹具的空首页，已再次退出并核对，不将该重开当成业务持久化验收。

下一步仍按完整Goal推进：完善官方授权／列表读取入口及知识出处返回、四稿Native还原；先隔离验证官方插件真实自动采集与处理，再核对迁移和按授权切换。最终还缺全仓test／vet、四张同尺寸实际对照、真实官方回执、迁移清单与回退、连续像素证据和完整可启动运行包。Goal保持active，不交用户最终验收。

## 续做：出处联动、迁移盘点与UI停止（停止结论已纠正）

- 新增`official_source_session`：每次读取前后核对知识／来源版本与真实出处；旧来源没有user_id时，限定授权dataset并在有界目录内证明会话所属用户唯一，不接受同名会话跨用户的任意结果。引用QA／trace若不在官方最近片段中，明确未定位，不伪造消息关联。UI从可靠官方来源显示「阅读对话」，返回恢复知识选择／滚动并拒绝迟到回执。
- 官方来源知识使用Markdown及克制代码色／中文代码复制，旧纯文本知识保留原展示语义；集中检查后四包完整通过：desktop30.724秒、daemon24.873秒、official0.457秒、serviceipc0.946秒。出处路径race通过（desktop2.681秒、daemon5.421秒），四包vet退出0，见`source-link-checks-004`。全仓门禁仍未运行／未宣称通过。
- 执行[只读迁移盘点](official-data-migration-20261112.md)：开发库知识1／原始记录30，共31条、31版本，官方绑定0；均无可靠官方身份，保留待比对。源库SHA256前后一致，未复制正文或改写配置／队列，未迁移或删除旧数据。
- 本轮Native候选003已保存普通／工具／知识三张实际AppKit截图及逻辑尺寸回执，源输入／二进制散列在同目录source-info.json。知识页实际对照仍未通过。当时将前轮Native及两次不同原因检查失败合计为同一问题4轮并停止，**该累计口径经用户2026-11-12纠正，停止结论撤销**。全部原始失败保留，见[失败记录与计数纠正](conversation-ui-stop-20261112.md)。原停止期间未继续800×620实际窗口或像素验收。
- 纠正后：Native视觉未通过2轮（具体会话／知识缺陷分别跟踪）；返回滚动验收夹具失败1轮，后续通过；全局Markdown旧知识回归失败1轮，后续通过。不能以整个UI交付聚合触发同一问题停止。Goal保持active，按原授权继续，先审查阅读结构再实施。后台刷新原账本3轮保留，旧helper／图谱亦不重跑。

## 用户纠正后的续做：阅读壳与计次规则

2026-11-12：同步修改AGENTS.md及workflow.md，按具体触发条件／现象／验收标准识别问题；不同缺陷、夹具方法错误与独立回归不再合计为整个UI失败4次。原失败及纠正记录保留。

源码新增knowledge_layout.go，普通项目知识阅读复用会话顶栏及316／248列表宽度，移除重复详情顶栏，保留编辑／历史入口、来源说明、过滤／新增及固定已载入数量底栏；官方知识预览先取首段，避免后续Markdown标题混进摘要。未进行新Native构建／验收，未宣称还原完成。

第一次desktop完整回归29.887s未通过：TestContentExplanationPreservesReadingAndReturnsFocus缺内容来源说明入口，TestE05AssignmentMismatchFramesAndOriginalType缺待归属提交返回入口。归为本次“新阅读壳入口遗漏”失败1轮，两个断言不重复计次；已补回原入口，定向复验待回执。测试断言未修改。首次格式化命令因系统PATH没有gofmt而未启动测试，随后使用仓库固定工具链；该命令错误不是产品验收失败。磁盘门禁前余量约133GiB。

计数一致性复核：刷新旧“累计3轮”亦是历史汇总，按新版规则不再自动推导“下次即第4次”；复制方法错误与旧测试契约冲突须按具体问题追踪。详见刷新复盘新增“计数口径纠正”，原回执与记录均保留。

阅读壳入口遗漏补回后，两项定向检查通过（0.922s）；随后desktop完整回归通过（29.962s，task会话46575最终exit0）。新GUI独立构建，Native待实际观察；未改变已有测试断言。

Native候选native-knowledge-004：GUI-only e2e/ad-hoc签名包，独立bundle conversationreview20261112。CUA实际查看1200×820知识窗口；自身NSWindow截图193616及同名JSON已保存，源输入／签名后二进制散列在source-info.json。确认双顶栏移除、固定底栏及“已载入1条知识”、预览无后续Markdown标题；编辑／历史与来源说明仍可达。仍未通过第三稿整体还原：Markdown小标题偏大，辅助折叠区占位偏多，列表直接排序入口待补。保守按知识视觉还原未通过第2轮保留（原native-source-003为第1轮），不与入口遗漏回归合并。普通页签切换会话可达，截图193645保存，不据此宣称业务或刷新闭环。通过CUA退出，App inventory确认独立bundle消失；未操作现有后台依赖。

同一004二进制以800×620启动conversation-tools合成场景，AppKit首屏193758、右侧工具末尾193833及同名bounds回执已保存。实际观察三栏保持、右侧独立滚动至失败记录、列表／固定底栏持续可见。设计04重新实际打开比较；无逐轮工具关联／QA时间时依契约降级，不能补造；窄侧栏品牌及部分间距仍有设计差距，不标整稿通过。仅记录最小窗口布局／滚动切片，不证明官方采集、剪贴板、来源或30秒刷新闭环。CUA退出并确认bundle消失。对照入口native-knowledge-004/comparison.html，完整索引capture-index.json。

## 005阅读收敛与官方插件真实安装

- 源码：官方知识Markdown明确H1/H2/H3为18/15/13px；辅助使用／状态／更多操作仅在“记录信息”展开时呈现，顶部编辑／历史保留。知识列表直接提供现有“最近更新／常用／重要优先”，展开过滤区不重复创建同key排序器；窄阅读侧栏补Memlane文字。
- desktop完整测试29.828s通过。首次vet报knowledgeMarkdownOptions按值返回含sync.Map的Options；作为“新辅助函数复制锁值”失败1轮保留check-0.log，改为指针返回后vet通过、受影响阅读／返回／入口测试0.934s通过。编译通过。签名命令曾误指Contents而失败，已对正确.app签名成功；sign-correct-path.json记录，不重跑构建、不计产品验收失败。二进制／源输入散列及完整命令见native-knowledge-005。
- 实际1200×820 Native截图194428及bounds回执已保存；两项排版改动生效，辅助区默认不再占正文空间，记录信息可展开并再次收起，编辑／历史可达。发现两项独立问题：合成夹具未设置listSort导致排序器空值（真实ConfigureLibrary已有默认值，不归因为生产排序默认缺陷）；未知CreatedAt显示0001（缺失字段降级缺陷）。分别失败1轮，已修夹具默认值与“时间未知”，修复晚于005二进制，尚未Native复验。不将新缺陷视为旧双顶栏／字号问题复发。窄侧栏文字本次仅源码，未以005窄窗口实测。
- CUA退出005并确认独立bundle消失，daemon76077／官方服务76867未替换。005仍不代表四稿整体、真实来源或刷新通过。
- official-plugin-install-001：使用仓库已有固定官方组件（upstream997fd6f72253aa97b49996eb1b032793f0257935，插件1.7.7），在全新隔离host的HOME／CODEX_HOME下调用真实Codex0.167.1 marketplace add、plugin add、plugin list，均exit0。CLI回执确认cognee@cognee1.7.7已安装，隔离插件状态enabled=true；尚未配置服务或运行宿主会话，不能据enabled宣称信任／自动采集已验收。安装前后官方源码SHA逐文件一致；未复制auth.json、读取用户历史或调用模型，未使用hook trust bypass。receipt.json与四个日志保留，日常宿主／旧hooks未修改。此证据只覆盖真实CLI安装，不覆盖App电脑级安装入口、信任／重载、模型自动采集或迁移切换。

005后缺失时间／夹具修正的阅读、出处与返回定向检查通过（1.032s；task65498）。不替代Native复验。后续真实Codex验证的无凭据复制候选：本机0.167.1 `exec --help`明确支持`--ignore-user-config`（不加载用户config.toml，但auth仍用CODEX_HOME）及`--ephemeral`（不持久化session文件）；尚未证明能同时隔离所有已有hooks、加载官方插件并满足正常信任，未实际运行模型。不能仅凭这两个参数就宣称无双采集或完整隔离。优先核清宿主hooks加载／信任边界，再执行已授权的自有合成会话。

## 官方hooks宿主解析与只读检查

- 核对[OpenAI Hooks文档](https://learn.chatgpt.com/docs/hooks)：多来源hooks会合并，插件安装／enabled不等于已信任；hooks/list须按宿主实际结果判断。CLI的[参数说明](https://learn.chatgpt.com/docs/cli/reference)仅承诺ignore-user-config不加载config.toml，不能据此推断hooks.json必然被排除。
- 本机Codex0.167.1实际导出experimental JSON schema，确认hooks/list及其cwd、pluginId、source、currentHash、enabled、trustStatus字段；留存codex-protocol-01671（约5.8MiB）。
- official-plugin-hooks-001：在上轮隔离host运行真实app-server stdio，仅initialize／initialized／hooks/list，无thread/start或turn/start；8个cognee@cognee插件hooks全部untrusted，另有3个系统managed hooks，warnings/errors均0。不会关闭或改变系统managed hooks。进程PID20449最终exit0。summary.json剔除命令与系统细节；原回执仅存build诊断材料，不提交主机配置。
- 新增internal/agents/codex_plugin_hooks.go，复用现有RPC进程生命周期；只读解析宿主实际hooks。返回脱敏的事件／hash／enabled／trust与数量，识别review_required、disabled、缺项、目录不匹配和不完整目录；hooks_ready只表示观察到的hooks定义已获信任，不能表示服务或采集成功，也不自行证明插件源码身份。必要事件由已核验插件manifest的调用方提供。没有新采集器、认证复制、配置写入、信任写入或模型调用；尚未接入Native连接页。
- 新增10项边界检查及显式opt-in真实隔离宿主检查：全部通过，agents0.886s，真实结果review_required／8插件hooks／3其他hooks；未产生auth.json。agents vet退出0（official-plugin-hooks-001/vet.log）。失败计数未新增。
- 下一步用独立合成hooks.json加载对照核实ignore-user-config的实际边界；仅回环合成失败端点，不接真实模型，不使用trust bypass，不能当作自动采集验收。

加载对照codex-ignore-hook-canary-001完成：独立合成host的hooks.json故意使用非法JSON，normal与ignore-user-config两次均在事件流报告同一文件解析错误。两次仅向自有127.0.0.1合成503端点各发1次/v1/responses请求，均exit1且无超时；不是实际模型调用或产品验收失败，不计入产品缺陷计数。这证明本机0.167.1的ignore-user-config仍读取该hooks.json，不能据它隔离日常旧采集。未使用真实配置／认证、未改用户文件或关闭系统managed hooks。

已异步向用户提出独立配置目录登录与官方hooks正常信任的选项。该外部步骤未完成前不运行依赖它的真实模型采集、不替换日常旧hooks；继续独立Native和官方数据工作。Goal不因此自动标blocked或complete。

官方插件服务准备：official-plugin-service-001实际register201／login200／mint key200；探测脚本误用Bearer携带API key，identity401。作为“验证脚本认证头错误”1轮保留，生产SetCredential已正确区分JWT与API key，未改生产认证逻辑。002复用同一合成principal与同一API key，改用X-Api-Key后identity／datasets均200，官方版本1.6.7，新账号dataset目录为空。只为隔离插件写入0600 .cognee/.env（managed endpoint，独立目标dataset名，不自动启动另一服务），未复制Codex认证、未创建业务材料、未改日常宿主或旧hooks。私有密码与key仅在.local/official-plugin-probe-20261112下，不进日志／报告／提交；receipt.json只含非密钥身份与阶段。等待用户的隔离登录／信任选择期间，不启动真实采集。

独立GUI候选006构建／正确.app签名均exit0，全部internal／cmd Go输入散列前后一致。真实800×620知识窗口截图200152及bounds回执已保存：排序默认“最近更新”、记录信息中未知时间显示“时间未知”均已现场确认；复制／来源／真实业务不在该切片验收范围。窄侧栏品牌仍省略，关联同一窄品牌问题，累计第2轮未通过（004缺文字为第1轮，005没有运行窄窗口，不计额外轮次）。保留24px按钮命中区，缩窄顶栏右padding／gap共6px以给品牌文字腾出空间；同时官方知识窄窗标题收至交接19px。两处源码晚于006，未再构建或Native复验；不宣称解决。CUA退出006，inventory确认独立bundle消失。

加载canary私有host已在lsof确认无打开句柄后清理，报告保留。初始清理上限检查将两个apply_patch符号链接目标二进制计入逻辑大小而中止，未删除任何文件；改按lstat／不跟随链接核对后，只删除自有合成host（约3MiB），符号链接目标的inode／大小／mtime前后保持，真实Codex安装与待登录官方插件host保留。cleanup.json记账。

## 官方文档主源读取与真实存储契约

- 从当前运行服务实际OpenAPI提取DataDTO、datasets分页／raw路由及add表单，证据official-document-contract-001。DataDTO为camelCase且日期／大小／hash可空；列表是数组，没有内置has_more或快照保证；content_hash筛选只限自身添加行，不能当共享读者的通用定位。不存在GET单条文档元数据路由。
- 新增records/documents.go、official/documents.go、daemon/documents.go与serviceipc/documents.go：官方文档元数据分页采用一行前瞻，不编造总数；正文仅通过官方dataset/data/raw读取，前后核对服务身份、文档元数据及当前项目绑定。所选位置移动时按官方有界offset分页重新定位，超过上游分页边界明确失败，不另建索引。未知日期／label／hash保留为空，不推断内容是知识、处理完成或本地版本。非UTF-8或不支持预览的类型返回unsupported_content，区别权限失效。
- 共享HTTP传输抽成requestBytes，JSON Request沿用该传输；保持原127.0.0.1、禁代理／重定向、凭据头和8MiB响应上限。原文件路径rawDataLocation不进入文档DTO，不用该字段直接访问文件系统。新增official_documents与official_document_text IPC；需要现有明确历史读取授权，材料提交授权不扩大为读取权限。读取不写memories／versions／jobs。
- 额外补强共享sessionBinding：项目必须为明确canonical UUID，绑定的Project必须与请求相同；空项目不能利用全局绑定枚举来匹配现有授权。空项目与错误dataset均在网络工作前拒绝。
- 定向official文档检查0.655s通过。首轮整包official0.980s、serviceipc0.596s通过，但新增daemon测试夹具误用不存在的AddProject方法而未编译，归“新文档daemon夹具注册方法错误”失败1轮；001原日志保留。修正为既有RegisterProject后，daemon整包34.633s通过；文档路径race official1.725s／daemon3.895s通过；三包vet退出0，见official-document-checks-002。显式项目检查补入后，官方session／source／document定向daemon6.361s通过；随后预览错误分类补入后的定向检查仍在运行，不提前宣称通过。
- official-document-real-001：在此前新建合成principal的另一独立dataset中，通过官方add连续提交两次相同合成UTF-8材料，最终官方list仅1行。label为memlane_contract_probe，明确不是session_learnings、Agent采集或提炼知识；未使用待验证插件的目标dataset，也未请求cognify／improve，不据此宣称内部未知模型调用为零。真实Go DocumentReader读取该行并比对上传输入SHA256一致（actual-service用例0.849s，日志go-read.log）；该输入只验证存储／读取契约，不替代Goal的真实自动采集与知识链路。
- 当前默认Native知识列表尚未切到该IPC；旧31条记录／版本／队列未迁移或删除；官方知识类别、来源与编辑／历史的覆盖仍须核清再接UI。旧已保存知识不因本次增加读取接口而被隐藏或改变。

官方文档最后定向检查完成：official0.646s、daemon6.555s通过（final-focused.log），覆盖文档、会话与出处以及空项目拒绝。实际Go原文读取用例0.849s通过，私有fixture配置仅引用既有合成key文件，不复制Codex凭据。当前新API均为源码／协议与直接服务读取证据；尚未替换保护中的正式daemon或Native默认读取入口，不能将其写为完整App闭环。

后续接知识页前的分类约束：当前本地Cognee1.6.7源码将session_learnings用作node_set；distill通过add(...,node_set=...)发布，再调用cognify，渲染模板含session身份。它不是DataDTO.label的通用语义，不能只凭label／文件名或正文形似结论就标为已提炼知识。DataDTO本身也没有逐条处理状态／版本历史；实际服务的知识分类、来源与处理状态需按正式返回值／已有官方material适配核实，保留未知，不把本轮memlane_contract_probe作为知识示例。旧EnableCurrentKnowledge仍以Memlane当前版本为主，属于待迁移旧语义，未在本轮擅自改变用户有效知识或Agent配置。

## 设置补丁串行集成与全量检查

按外部交接679ff8c串行导入01／02源码及04证据，未导入03磁盘脚本、快照基线或运行包；preimage与非目标源码散列保留于settings-integration-20261112-001。新增移除确认的连接／安装代次核对、普通包不可启用合成场景检查，并将Cmd+K连接到现有全局搜索；定向检查三组均exit0，实际Native尚未复验。

首次全量test／vet失败：自建preimage备份目录保留.go后被./...误扫描，属于验收材料隔离错误1轮，并非产品源码缺失类型；另有历史TestCodexCaptureHookInstallationPreservesOtherHooksAndRemovesOwnedItems的capture_bridge_unavailable，独立保留，不归入UI计数。普通GUI构建exit0。已为preimage添加独立go.mod边界，原文件不改动；重跑回执另存，不覆盖full-0／1／2.log。保护中的daemon／服务未替换。

集成复验：preimage隔离后全量vet exit0；test仍exit1。本轮agents完整通过31.777s，失败变为旧TestCaptureDeliveryPrivateIPCAndOfflineRestart在离线重启后等待回执超时（capture_delivery_ipc_test.go:135），daemon38.044s。未修改断言或将整仓写为通过，不与GUI或之前capture_bridge_unavailable合并计数；desktop完整通过32.917s。

GUI-only native-integrated-007：实际800×620窗口侧栏Memlane完整显示，原窄品牌问题通过本轮复验；文件菜单搜索入口及项目内Cmd+K均进入全局搜索，AX明确焦点为搜索记忆输入框。知识→阅读对话后为空：合成场景未配置libraryContext，openConversationSource清空旧内存夹具并等待真实SourceSession，不能作为来源业务通过；返回知识恢复原选中及焦点已观察。该项为合成路径覆盖缺口，真实IPC来源测试已有证据，实际来源闭环仍待验收。

007构建未显式CGO_ENABLED=1且截图目录未放source-info下一层，未提供自己的截图菜单，属于验证设置错误；CUA已实际看图，未制造持久截图。退出并在inventory确认独立bundle消失。008沿相同源码显式启用cgo，重新形成独立源／二进制散列；不替换旧证据。补写本段的首次Python命令遇编码错误未执行，随后使用补丁追加。

008构建／签名exit0。首次CUA选择发生在task门禁尚未启动子进程时，getApp启动了未带合成参数的空首页；当即退出并确认inventory消失，未操作真实配置或数据，未作为设计证据。随后只读ps确认受管子进程79531已启动，再选择同一.app；合成场景正确载入。

008实际800×620工具场景：保存三张自身NSWindow截图及bounds回执（205132首屏、205149展开工具末尾、205242收起）；capture-index.json和review.html汇总于native-integrated-008。首屏品牌完整、三栏和底栏固定；右栏独立滚动至失败工具，列表保持不动；工具组点击后AX移除工具内容并显示收起状态；搜索zz-no-match-008显示“未找到匹配会话”，清除后列表恢复。没有因纯视觉场景不运行后台读取而宣称刷新通过。横向滚动操作未观察到代码末尾变化，仍未验证，不记为已通过；复制往返亦未完成。通过CUA退出并确认inventory为空，保护中的daemon76077／官方服务76867仍在原路径。

## 官方文档来源直接读取

新增官方文档一致会话引用解析及`official_document_session`，摆脱旧Memlane知识／原文副本对来源读取的前置依赖；复用既有授权和唯一owner解析，读取后再次核对文档及连接代次。格式引用不等于已提炼知识或已处理，仍不伪造具体QA／trace；缓存过期不让永久文档一起消失。具体契约和边界见[迁移核对](official-data-migration-20261112.md#官方文档到来源会话的直接读取)。document-reference-001整包／vet／既有真实合成文档只读检查通过，document-source-001定向／race／vet通过；本轮无新增已证产品失败，历史失败和计数保持。正式daemon、Native默认知识读取、迁移切换、模型采集与完整业务验收仍未完成。

## 009：真实读取与31秒连续Native观察

- 新增仅e2e的conversation-live入口：要求显式绝对路径、synthetic_only标记、canonical项目ID和同根native-reading-probe.json；不注入视觉样例，使用正式ConfigureLibrary、导航、IPC、两秒轮询和权限读取路径。普通包入口为空函数。仅e2e截图菜单增加31秒／100ms自身NSWindow采帧，并同帧记录阅读状态／请求代次／滚动位置，不捕获桌面或其他App。源输入、GUI签名后二进制散列及构建日志保留native-live-009/source-info.json，两个构建阶段exit0；e2e启动隔离／会话定向cmd0.707s、desktop0.890s通过。
- 独立.local/native-reading-20261112-001创建合成仓库、客户端数据库、明确读取绑定和独立daemon，复用此前新建合成principal的服务key；没有复制用户Codex认证、读取历史或改日常hooks。daemon没有配置原文worker，不验证处理能力／helper，界面相应仍显示服务未连接；本切片仅证明独立真实IPC→运行中的官方1.6.7读取。未替换受保护daemon76077／官方服务76867或其完整包。
- 官方remember/entry向已有自有contract dataset写入2条QA、2条trace，四次HTTP200；manual-entry-receipts.json明确manual_api_fixture与not_automatic_capture，未请求improve／cognify，不作为自动采集或模型提炼证据。第一次准备脚本因字段Endpoint大小写错误，在HTTP请求前失败；修正后才执行上述四次写入。无重复执行已写入材料。官方返回日期为空，Native真实显示时间未知。
- 实际800×620窗口：阅读滚动到Y=516并聚焦会话搜索框，之后通过CUA启动31秒记录；309帧、采样跨度30799ms、最大间隔101ms、截图失败0。catalog/read均启动16次新轮询，分别观察到15次完成；80帧处于请求进行中，正文缺失0。全部帧选中身份相同、行数保持1、滚动位置保持516；正文ROI(848,140)-(1560,1120)与固定数量ROI(340,1150)-(1560,1225)逐像素一致。采帧前后AX焦点均为会话搜索框。analysis.json、frames.json、309张原始帧和可播放review.html保留于native-live-009/window-captures/continuous-20261111T210625.564707000Z；像素分析用Go标准image/png解码，未生成替代画面。
- 实际点击SQL复制代码，再在同一App搜索框粘贴，AX返回完整SQL（含末尾库存条件），清除后列表恢复。横向滚动尚未观察到代码末尾，未标通过；部分指针操作坐标／控件目标存在方法不确定性，尚不能据此断言MyGo实现根因。人工输入与工具观察不替代对应功能验收。
- CUA退出并确认独立bundle消失，向本次明确root的私有IPC发送stop并获OK；新daemon90258／GUI90259均已退出。只读离线盘点mirror-audit.json显示memories／versions／operations／jobs／organization_sources全部0、绑定1，DB散列前后一致。首次Python mode=ro读取失败，改用既有停止库盘点脚本的immutable只读契约；原数据库未改写。PIL不可用后使用Go标准库分析，没有下载依赖。
- 本次只通过有有效内容的常规刷新切片；临时失败、明确撤权、迟到回执与其他知识／来源窗口仍需分别实际验证。自动官方插件、真实知识生成、迁移切换及完整运行包未完成，Goal继续active；既有具体问题失败记录不重置，也不按全部命令累计成UI失败。

## 010–011：断连反馈位置与撤权

010复验发现临时断连时内容虽然未消失，但插入错误说明／重试导致列表与正文移动（143帧正文ROI变化）；记录为该具体问题失败1轮。最小几何回归明确红后，失败反馈改放固定底栏，正文范围说明保持稳定；未减少轮询或放宽来源核验。011完整desktop29.780s、CGO e2e vet、构建及签名通过；实际同样31秒／309帧经过独立服务断连与恢复，正文和列表行ROI全部相同，Y／选中项／焦点保持。错误反馈仅在底栏变化，数量与复制仍可见。详见[刷新复盘](reading-refresh-flicker-20261112.md#010临时失败反馈改变内容几何)，原失败保留，011不是重置计数。

010对合成连接明确disconnect_revision后实际正文撤下、复制禁用，该撤权切片通过。之后重新授权测试连接时发现旧BindCognee不能更新相同instance／dataset的授权代次，返回冲突；此缺口未修复，001原库保留，未直接改绑定SQL。011几何复验使用新建002客户端测试库读取既有同一批合成官方材料，不重复提交、不把新库通过当旧库重新接入通过。所有新GUI／独立daemon已正常退出，受保护76077／76867未替换。新旧链路迁移、重新接入缺口、迟到回执与真实官方采集等仍在Goal范围内，未宣布完成。

## 原001库重新授权后端复验

新增单独的、需明确确认且核对旧快照的读取重新授权操作，普通BindCognee的不可变契约保留。服务权限／当前连接代次前后核验，条件更新原绑定，仅增加前操作ID和审阅摘要，不新增材料镜像或修改schema。固定授权类型／身份／范围，拒绝材料提交升级读取、旧回执复活及核验中撤权，凭据刷新保留新回执。

read-reauthorization-001定向／race／vet通过；受影响Store／daemon／serviceipc／desktop整包通过。真实使用发生冲突的同一001客户端库及原官方材料，generation1→3，重复请求回执相同、绑定仍1，2QA／2trace保持可读；未确认和旧绑定请求拒绝。离线审计客户端材料／版本／任务仍0。详细边界见[迁移记录](official-data-migration-20261112.md#同一客户端库重新确认读取授权)。独立daemon正常停止，正式依赖保持。该项后端缺口已有真实协议证据，Native确认入口尚未接入，不标完整产品重新授权或Goal完成；原失败和新库011复验边界均保留。

## Native读取范围确认013

已在会话页接入右栏范围确认；候选要求同一连接仍有项目授权、新代次有效，打开／取消不改授权，固定快照／操作ID重试，错回执及切页迟到回执拒绝。实际reload_required错误纳入scope_changed，确保旧正文先撤下并提供正确入口；不是把权限错误变成保留旧内容的暂时错误。

013完整desktop29.885s、review路径race2.163s、CGO e2e vet、GUI构建和签名通过。实际800×620沿原001库确认：取消后绑定3不变，明确点击确认后绑定4并恢复原2QA／2trace，审阅摘要与Native所见范围匹配。两张实际NSWindow截图／bounds、取消前后IPC回执和源输入散列见native-read-review-013及review.html。该正常Native路径已通过；源码首轮primary命名编译错误保留为独立失败，012未验证真实错误发现链路，不冒充013证据。其他业务及交付要求仍按原Goal继续。

## 官方Codex插件安装器：配置与重复安装边界

新增`internal/agents/official_codex_plugin.go`，只通过Codex官方CLI安装签名组件目录中的官方插件，不要求项目、模型或登录；安装与hooks信任／服务验证分开。已启用的旧自有采集器阻止直接安装，保留迁移前置；不覆盖同名外部marketplace，不复制凭据、读取历史或自动信任hooks。

本轮补齐重复安装前的官方`plugin list --json`核对：注册、版本、来源和实际缓存逐文件摘要一致才复用，并跳过`plugin add`。已有禁用状态返回`installed_disabled`，不重新启用；冲突注册拒绝切换。缓存路径和配置文件符号链接拒绝，保留非Cognee配置／hooks摘要核对。合成CLI检查覆盖首次安装、重复安装、禁用保持、旧采集器冲突、组件／安装内容变化、外部marketplace、路径链接及错误输出不泄密。

定向测试通过（7.940s），定向race通过（5.722s），agents vet退出0；命令均经`go.sh`磁盘门禁，可用空间约131GiB。首次格式化调用因系统PATH无gofmt退出127，已改用仓库固定工具链并完成格式化，非产品验收失败。上述是安装器边界检查，尚未接入App安装按钮，也未替代真实官方自动采集、正常信任／重载、迁移或最终Native业务验收，Goal保持未完成。

### App安装入口接线

设置→Agent连接→Codex详情及旧Agent采集区域已接入“安装 Cognee 官方插件”。移除新开启旧自有采集器的按钮，并在调用入口拒绝启用旧采集；停止旧采集和查看待发送原文保留。官方卡片保留旧采集停止入口，不要求用户另找旧页面。

安装前读取本客户端全部adapter记录（含已断开的项），无法核对的记录拒绝安装，避免忽略旧授权形成双采集；不扫描Agent历史。重新读取当前连接代次及安装路径，再通过组件管理器核对官方插件。异步结果按客户端代次／操作序列隔离，成功仅显示等待信任／重载／采集验证，已禁用状态不提升为可采集。安装回执目前只在本次App状态中呈现；重开后的重新发现、真实安装点击和正常采集仍待完成，不宣称电脑级迁移已交付。

桌面定向检查通过（0.855s），覆盖不把安装当采集证明、禁用提示、旧采集停止入口和断开／损坏库存；desktop vet退出0。补充当前连接核对后的定向race通过（2.132s）。实际Native与真实模型闭环没有被这些检查替代。

### 真实签名组件安装002

`official-plugin-managed-install-002`使用新的`.local/official-plugin-install-managed-20261112/host`，通过生产安装器读取现有受保护组件管理器的已签名目录，在真实Codex0.167.1安装官方1.7.7；未替换受保护daemon／服务及其运行包。首次安装返回`reused=false`，同一入口再次执行返回`reused=true`，两次均为`installed_pending_verification`，安装文件逐项匹配签名组件清单。

随后仅initialize／hooks/list实际核对：8个官方hooks为untrusted／review_required，系统hooks另计，不改变信任或系统配置。隔离宿主无auth.json，未复制日常凭据、启动模型会话或调用人工保存。真实结果、hooks摘要和生产源输入SHA见该目录`receipt.json`与`source-info.json`；这是实际安装器／宿主证据，尚非Native按钮点击或自动采集验收。

接线后完整`sh scripts/go.sh test ./internal/desktop -count=1`通过（29.990s），保留前述定向race和vet边界；不是全仓测试或最终包已通过。

### Native官方插件安装014：安装成功，接入检查兼容缺陷失败1轮

新建独立客户端／Codex HOME，通过正常`agents.Install`建立电脑级MCP配置，无项目授权、旧采集授权、登录与模型。e2e入口仅导航到真实设置详情，正常读取客户端IPC与签名组件；没有注入插件结果。800×620实际Native点击“安装 Cognee 官方插件”后，真实安装1.7.7并显示“插件已安装，等待信任、重载与采集验证”。NSWindow截图与bounds保存在`native-official-plugin-014/window-captures`。

同轮实际观察到连接状态由“待验证”变“异常”，因此整体安装接入兼容验收未通过，记该具体缺陷失败1轮。后续只读核对证实：官方CLI重排TOML后，完整MCP解析树与原安装回执相同、所属begin/end标记仍在，但旧逐字Block不再匹配；`CheckInstallation`返回“Memlane 配置已变更，请重新连接”。原因证据在`after-audit.json`，不是凭截图推断。8官方hooks仍untrusted、errors0，无auth.json，无真实模型或采集；不扩大为UI累计4次，不删除此失败。

独立GUI已通过正常退出并在inventory确认消失，独立daemon通过本根IPC停止；受保护服务未停止或替换。需修复官方CLI格式化与本机MCP所有权回执的兼容，保留内容变更拒绝、断连和撤除边界，再沿同一隔离根复验，不把另建根当修复证据。

### 同根兼容修复与Native015

首次修复只识别begin/end之间的等价TOML，现场定向核对仍失败：官方CLI把end注释留在文件末尾，新插件表插到它前面。保留`after-source-fix-001.json`，此轮同一具体问题累计2次失败（014 Native与首次现场复核），不重置。第二方案按锁版本TOML解析器识别Memlane表边界，检查原回执、实际表及全文件同名空间完全等值；撤除／重装只移除该表与唯一所属标记，外部插件表保留。命令、参数、额外字段／表变化、重复标记仍拒绝。

新增定向检查含等价引号／排版、父表、结束标记后移、恶意改值与域外扩展、重装和撤除后的外部插件保留。第一轮新父表用例未通过（同一实施轮中暴露父容器识别缺口，原失败保留），修正后定向race通过2.165s，agents vet退出0；014同一实际配置只读检查已通过（`after-source-fix-002.json`），未改写旧安装回执或宿主配置来绕过检查。

015沿014原客户端／HOME／连接重新打开800×620真实Native窗口，安装前“待验证”，再次点击安装后显示等待信任／重载，经历正常Agent刷新后仍“待验证”，不再变“异常”。两张实际NSWindow图、源／二进制SHA见`native-official-plugin-015`，独立GUI正常退出且inventory确认消失，独立daemon本根IPC停止。累计2次失败记录保留，015仅证明这个具体兼容缺陷与安装重放通过；没有证明信任、模型自动采集、知识生成、迁移或完整Goal交付。

015收尾只读SQL核对：连接1、memories／operations／jobs均0，未创建原文或知识来冒充采集。系统Python SQLite对本根只读打开失败两次，保留为审计方法未通过，随后仓库锁版本SQLite驱动只读审计成功；没有因此改动数据库、权限或重复启动服务。014／015各自的`review.html`可查看实际截图，所有旧失败与修复阶段回执分文件保留。

所有权兼容修复后完整agents包测试通过39.078s；此前定向race2.165s、agents vet退出0。原受保护daemon76077／官方服务76867仍在，独立015运行进程已退出。其余Goal要求继续按原范围推进。

### 已安装插件重新核对与Native016

安装器共用签名来源／实际缓存／宿主注册核验新增只读入口，禁止发送add／enable／trust／login；未安装或已禁用时如实返回。`VerifyOfficialCodexPlugin`从已验证插件manifest取得hooks事件，实际读取宿主信任，并在返回前重新核对插件文件与注册，不能把hooks可信当作服务或采集已通过。

设置新增“核对官方插件”。结果绑定当前连接代次，操作开始清除旧结果；失败不沿用旧就绪提示。重开后不依赖上次App临时回执，用户可直接从真实宿主重新读取状态；不在每三秒Agent刷新中反复启动宿主。界面明确上次核对边界，服务与自动采集另验。

定向agents／desktop检查6.028s／1.080s、race7.403s／2.149s通过，两个包vet退出0。真实`official-plugin-inspect-003`沿既有隔离HOME核对得到1.7.7已安装、8官方hooks待信任，config.toml前后SHA相同，无auth.json、模型会话或自动信任。

Native016沿同一根重新打开800×620窗口，不点安装，直接点击“核对官方插件”，实际显示“插件已安装，等待 Codex 确认信任并重载”，连接仍待验证。截图／源SHA／核对后配置SHA／stop回执及review.html均在`native-official-plugin-016`；配置SHA与真实接口核对一致。独立GUI正常退出且inventory确认消失，独立daemon按本根IPC停止；日常宿主与受保护服务保持。该项只通过安装后重新发现与待信任反馈，正常信任、真实自动采集、处理与迁移等原Goal要求继续未完成。

本次接线后完整受影响包测试通过：agents40.399s、desktop29.916s，详细命令与定向／race／vet边界见016的`checks.json`；不外推为全仓或最终完整运行包已验收。

### Native017–018：代码横滚、跨消息选择、复制与键盘

继续使用016已签名GUI及原001客户端／原官方合成材料，无新种子、模型、授权变更或生产源码修改。017为800×620；018为1200×820，截图bounds分别确认。两尺寸均通过真实水平滚动条拖动看到长TS代码结尾`(sku, quantity, transaction);`；017另验证SQL行尾与横向返回。列表／纵向阅读位置不随代码横滚移动。此前AX长文字目标滚动未证明行尾，属于操作目标方法边界；本轮截图坐标拖动已取得直接证据，没有据此改写MyGo或引入另一套代码渲染。

017跨第一条Agent正文、第二条用户问题和第二条Agent标题拖选，实际蓝色高亮保留。随后连续31秒／309帧（有效跨度30800ms，最大间隔101ms），catalog／detail各启动15次，76帧正在刷新；正文缺失0、行数／选中会话／Y各只有一个值。正文、固定数量、选区和代码行尾四个ROI在全部帧逐像素相同。首帧实际PNG已打开复核蓝色选区与代码行尾，非只靠AX或状态数值。`native-reading-interaction-017`保留全部PNG、frames／analysis.json、交互回执及可拖动时间轴review.html。

连续观察后Cmd+C选区并粘贴回App搜索框，实际返回正文＋你＋第二问题＋Agent＋Verification；长TS“复制代码”粘贴回完整函数调用。底栏复制已载入内容后，实际搜索值保留角色、两条QA、Markdown标题／围栏／表格标记及独立工具记录。搜索字段单行显示会折叠换行，本轮验证内容与源码标记，不把它宣称为换行字节级比对。清除搜索恢复列表。真实工具成功／失败与参数／未提供值均展开，Tab从搜索依次到排序、会话、旧材料、记录信息、两个代码复制、工具组；space收起、Return展开。CUA大写Space别名被工具拒绝，改用其xdotool标准小写space成功，非产品验收失败。

018保存默认三栏首屏、长代码行尾和工具展开3张实际NSWindow图；017另有4张静态图，两个目录的capture-index记录尺寸，review.html供查看。两次GUI均正常退出并由inventory确认消失，同一独立daemon在018结束后本根IPC停止。通过边界仍是手工合成官方QA／trace的实际阅读，不冒充插件自动采集、知识产生／出处闭环、迟到回执全矩阵、全部四稿交付或正式运行包；旧失败计数保留。

### Native019：切会话后的旧IPC响应隔离

新增一次手工合成官方QA会话B，复用原001客户端、同一数据集／授权和原会话A，未重建或复制业务库。e2e GUI016仍使用正常读取、取消和代次检查，生产源码未改。只读Unix IPC代理转发白名单查询到原001 daemon，不替换成功响应字节，不传递写操作、图谱或服务控制；代理根仅承载轻量UI状态，无业务库或凭据副本。

实际Native先显示B，再为A读取暂存真实成功响应。暂存后10068ms已切到B并发起B读取，早于旧请求15秒上限；15891ms控制释放A原响应，SHA与暂存时相同。释放时旧前端连接已关闭，send未完成。此为正常取消传输分支的直接证据，不声称旧成功回调已经进入UI队列或只凭该例证明所有晚回执路径。实际窗口继续显示B的标记正文及1QA／0trace，复制已载入内容后粘贴搜索也只有B，没有A正文。019保留held／released响应摘要、请求事件时间线、手工写入回执、800×620自身NSWindow图、交互回执和review.html。

另补`TestConversationQueuedSuccessCannotOverwriteSwitchedOrReopenedSession`：先令旧成功响应真正排入UI回调队列，再切B并完成新响应，或切B后回A完成更新响应，最后执行旧A成功回调。当前身份／正文／请求状态／有效80px阅读位置及复制内容均保持最新结果。定向race通过2.355s；这是源码／队列时序检查，和上述Native传输分支分列，不当成第二轮真实窗口验收。

GUI正常退出且inventory确认消失，测试代理SIGTERM并留下退出回执、原001独立daemon通过直接IPC停止，受保护76077／76867保持。未删除旧数据、未请求模型turn／cognify／improve，不推断服务内部模型调用为零。官方自动采集、实际知识与出处返回、迁移切换和最终包仍在原Goal范围内，未完成。

019收尾只读SQL审计：原001库memories／versions／operations／jobs仍0、连接和官方绑定仍各1，见`after-audit.json`。本轮新增B只存在于官方合成会话数据，当前目录从1会话变2；017／018截图中的1会话为此前现场状态，未重写旧截图。队列定向检查命令／退出码／源码SHA单列`queue-checks.json`，代理及两个独立受管进程均最终退出0。

### Native020：官方非会话文档的真实阅读与正常刷新

新增「原始记录 → 文档」入口，读取官方DataDTO目录和受授权的UTF-8原文；20项分页、仅搜索已载入名称。文档不被当作会话或已提炼知识，缺失更新时间不编造；页面明确当前未提供处理状态，不提供虚构编辑／历史。原文分段只存在阅读内存，不另存全文或版本。列表与正文均按绑定、来源元数据、当前页面和请求代次核对；撤权撤下文档与隐藏会话缓存，来源变化先撤旧正文再读新版本。

020实际800×620窗口经原001 daemon读取先前官方保存的合成纯文本文档，展开真实文档／数据集／MIME信息并复制原文，再返回原B会话。单行搜索回贴显示中英文原文，换行折叠不作为字节级复制证明。真实NSWindow图已打开复核。正常31秒记录309帧、有效跨度30800ms，catalog／detail各15次，62帧处于刷新中；正文缺失0，行数／选择／Y各只有一个值，正文及固定数量两个ROI全帧逐像素一致。证据、源SHA、操作回执与可拖动时间轴位于`build/conversation-official-20261112/native-official-document-020/`。GUI已正常退出并由CUA inventory确认。

独立定向检查发现文档临时失败会用错误文案替换已载入数量（具体缺陷DOC-REFRESH-COUNT-001，失败1轮，红日志保留）。已修正为保留有效数量和长文当前分段，在既有固定底栏提供重试及提示；定向race含长文第二段、临时失败、撤权与排队旧响应，退出0（2.288s），desktop vet退出0。修正前desktop完整测试30.284s通过，不能外推为修正后完整回归；020签名二进制早于此修正，不能用其正常轮询记录证明失败恢复通过。新021候选另行构建，临时失败／恢复和撤权实际窗口仍待验证。

本项仅证明官方原始文档读取及正常刷新。真实官方自动采集、官方处理产生知识与出处返回、旧数据迁移、其余Agent和最终完整包仍按原Goal继续，不以原始文档替代知识交付。

### Native021：官方原文临时失败恢复与真实撤权

021使用数量提示修正后的签名GUI，继续原001客户端／相同官方文档／gen4授权，无重新种子或复制业务库。31秒连续记录中，只向核对了完整命令行的本轮独立daemon99393发SIGTERM，原受保护daemon76077及官方服务76867未动；同一根`--resume`重启后读取自动恢复。尝试点击先前重试AX索引时，该按钮已因自动恢复消失，工具报索引过期；随后完整AX确认原文及数量仍在、重试已消失，此为观察时序，不计产品失败。

309帧、有效跨度30800ms、最大间隔102ms，catalog／detail各启动16次。225帧正常、82帧目录及正文均为`service_unavailable`、2帧仅正文仍失败；正文缺失0，选择／行数／Y各只有一个值。整块底栏有84帧像素变化，对应预期局部重试入口；单独比较正文、已载入数量和「官方原文」标签三个ROI，全部帧逐像素一致（`content-analysis.json`）。短文本不具备有效纵向滚动，故此记录不能外推为长文锚点实机通过；长文第二段保持另由定向race覆盖。

连续记录结束后，对同一合成连接执行带gen4 CAS的真实`disconnect_revision`，回执成功。实际Native及时清空文档列表、来源信息与正文，复制文档变为禁用；恢复和撤权两个静态NSWindow图均保留。没有重新授权，原001连接当前保持撤销状态，后续不能把旧marker的gen4当有效授权或直接跑旧重绑脚本。需要继续读取时须走已有明确重新授权入口及代次契约。完整原文与官方数据未删。

证据在`native-official-document-021`：签名候选源SHA、停止／撤权回执、连续帧及错误状态统计、两种ROI分析、时间轴和两张静态窗口。GUI正常退出并由CUA inventory确认，重启的自有daemon随后经本根stop IPC收尾；两个受管任务退出0。DOC-REFRESH-COUNT-001本轮Native复验通过，保留此前失败1轮，不清零。原Goal其余真实采集／知识／迁移和最终完整包边界不变。

021修正后desktop完整测试30.224s退出0，未重复无关全仓矩阵。只读离线审计确认同一连接已禁用且generation5；绑定仍1、连接仍1，memories／operations／jobs仍0，见`after-audit.json`。审计已产出结果后原工具句柄55277不再存在，未据此重复启动或重新授权。旧marker仍记录gen4，明确作为历史准备信息保留。磁盘余量约130GiB，未清理受保护运行依赖。

### pi发布包核对：工具复用与自动采集分开

从npm正式registry读取`@kerryhatcher/pi-cognee@0.1.0`并验证发布SHA512，固定gitHead及21个源文件SHA；仅在.local解包，未安装、运行包代码、读取会话或创建SDK存储。6个实际TypeScript源文件无pi生命周期事件订阅；入口只注册9个模型工具和2个命令。默认进程内SDK不复用现有官方服务，远端MCP需配置／重载且该固定客户端无API key头配置。故该包不能证明自动问答／工具采集存在；不能用主动cognee_remember替代Goal正常会话采集。

发布证据及结构核对位于`pi-official-contract-001`，详细依据与薄适配范围见[pi缺口](pi-official-capture-gap-20261112.md)。本机pi0.84.1实际文档已有相应宿主事件，但尚未编码或验证薄适配。矩阵同步纠正；Codex官方自动采集、pi适配、TRAE／traex、旧数据迁移和最终Native完整交付继续待完成。

### 官方文档来源会话接线（022候选，Native尚未通过）

仅当已读取文档含固定模板与node_set一致的session_reference时显示「阅读来源会话」，打开后走已有`official_document_session`后端授权及唯一会话归属核验。来源面板复用现有Markdown／工具／复制组件，明确只关联会话、未定位具体问答或工具，不把格式标记当作模型生成证明。返回文档保留选择、查询、长文分段、信息展开及滚动；来源超时保留有效会话，来源缺失仅撤来源会话、不删除独立有效原文；权限撤销同时撤下文档及共享会话缓存。文档元数据／正文／引用变化、切换文档、关闭和退页均取消旧来源请求，排队回执不能复活路由。

普通与窄尺寸定向检查覆盖上述返回及失败路径；错文档／dataset／session／空用户回执均拒绝。受影响desktop完整测试30.030s、定向race2.769s、vet退出0；新e2e GUI022构建及签名校验退出0。源SHA及边界在`native-document-source-022`。当前没有实际已提炼官方知识可供该新入口的正向Native验证，因此022只是候选，不能以构建或源码检查宣称来源业务闭环通过，也未替代03知识稿的完整交付。

### 官方improve首次实测：GLiNER运行准备失败，保留部分写入

使用原合成principal／dataset和此前手工提交的会话A（2QA／2trace），没有新增／重放采集输入，没有切换服务、修改配置或重跑旧328字节README图谱验收。先从正在运行的1.6.7服务取得OpenAPI，确认camelCase参数，证据`official-improve-contract-001`。随后只提交一次前台`POST /api/v1/improve`，指定同一dataset／sessionIds；不做无关全局索引、冲突复审或truth-subspace。本请求不是自动采集证据，且服务内部模型调用次数未知。

HTTP409、ImproveResult=errored：feedback_weights完成（103ms），persist_session_qa失败（1488ms），其余阶段因fatal stage中止而跳过。私有原响应明确为GlinerInstallError：服务走自动GLiNER准备，uv把PyInstaller的memlane-cognee-service当作Python解释器启动探测，程序因缺少--config退出2。该回执不证明成功下载／安装PyTorch，也不据短耗时声称没有网络或模型调用。客户端runtime源码已有verifyModel门禁，但本次直接服务API不经过该客户端模型准备路径；实际服务的处理配置与官方improve桥接仍需单独完成，不能直接用旧UI已启用pi推断此HTTP路径已配置。

具体问题OFFICIAL-IMPROVE-RUNTIME-001本轮失败1次，未自动重发POST、安装依赖、添加临时模型凭据或修改受保护运行包。旧README图谱失败保留且未复验；其原诊断是隔离ProcessingAdapter的material_model_failed／网关盲区，本次直接API的GLiNER解释器失败证据分开，不能只因都涉及cognify就合并或据新ID清零真正同源问题。

失败后只读官方审计：原会话仍2QA／2trace，原合成文档仍存在；目录从1篇变2篇，新增`10c71848-6a4d-4e11-9117-f9fd78018b1b`标记`user_sessions_from_cache`，是部分持久化材料，未生成session_learnings。保留新旧两篇及所有回执，不把部分写入删除或当作已提炼知识。旧真实读取用例的「目录恰好1篇」仅适用于此前快照，后续不能在新增材料后沿用该旧目录前提。intent／result／after-audit及响应SHA在`official-improve-session-001`；原始错误仅存.local私有文件，报告不含key或凭据。知识产生与正向Native出处返回仍未完成。

### 官方会话处理的任务模型上下文（服务源码切片）

已定位新版配置不经旧全局pi桥，而由daemon任务的StartCogneeGateway提供短期模型能力。新增会话improve服务源码入口，在当前认证用户／确切dataset／会话写授权下用ContextVar传递该能力，复用官方improve与现有ProcessingAdapter；不改服务全局模型、安装GLiNER或重放原始材料。错误只返回安全阶段结果，活动取消等待清理，未确认操作不自动重跑；当前服务内存回执不冒充重启恢复。

6项合成边界与4项既有材料检查通过，实际安装SDK的两并发任务选择检查确认auto extractor选择llm且native client为ProcessingAdapter，未运行模型或improve。证据`session-processing-context-001`，完整接线用例、事实和边界见[专项记录](official-session-processing-20261112.md)。服务路由与构建输入已接源代码，但daemon持久任务、签名组件、真实模型与Native业务仍未接完。受保护76077／76867保持；原实际失败1轮及其部分写入保留，未自动重试原POST。

### daemon持久任务及真实组件路由增量

已补schema32轻量任务账本与start／get／latest／cancel IPC，冻结来源观察和处理配置，重放保留原任务，重启／备份恢复标中断不重跑。daemon拥有任务网关、取消和执行回执封存，运行中监测授权，采用成功结果前再核对并与撤权串行。只保存身份／摘要和安全回执，不保存QA、工具正文或生成知识；目前仅在隔离库验证schema升级，日常库和旧001根未迁移。

最终Store完整测试45.435s，daemon／serviceipc／records完整测试31.351／1.080／2.302s，定向race及恢复race、受影响vet和Python边界通过。旧版测试准备中的漏删新表及硬编码31已分别纠正并保留红记录，未放宽内容、来源或权限断言。详细命令、源SHA与各日志边界在`session-processing-owner-001/checks.json`。

新服务组件首次构建成功后被task收尾历史清理删除12个大载荷；明确回执及修复见[存续期复盘](build-artifact-lifetime-20261112.md)。任务收尾现在仅处理自身临时目录和已知缓存，历史载荷须显式prune，候选完整依赖另登记保护。磁盘门禁仍保留，约129GiB余量；原受保护76077／76867未停止或替换。

同一候选输出重建退出0，真实二进制已确认包含新模块；独立打包服务健康HTTP200，实际新会话处理路由、无效范围拒绝及取消墓碑通过，共7次请求，SDK会话pipeline与LLM未运行。进程已回收且组为空，证据`session-service-health-001`。这不是Native或真实模型验收，也不是正式签名组件注册；原improve失败计数1轮和所有部分写入保持，未重发原POST。下一步继续实际配置／模型处理、知识与出处窗口、自动采集和迁移，完整Goal未完成。

## 真实pi处理增量：未通过知识生成

独立合成根完成真实pi协议验证、DeepSeek模型能力验证与明确启用；新daemon任务首次实际运行官方improve，QA入图在SDK字符串连接探测处失败。新增OFFICIAL-IMPROVE-SCHEMA-001记1次，与旧GLiNER运行问题分列；部分官方写入保留，未造知识。字符串schema适配修复及实际SDK定向检查已通过，尚未重建和重跑真实处理。证据session-processing-real-001，详见[处理记录](official-session-processing-20261112.md#隔离真实-pi-与官方处理验收增量)。自动采集、迁移、正向知识出处Native验收及最终包仍未完成，Goal保持进行中。

## 官方处理round002：真实模型接线通过，知识产物为空

含字符串修复的候选重建通过，按原合成根重启后确认同一进程从BGE初始化超时恢复，没有因观察超时重启。原授权刷新和新任务5379f52f-1ced-4563-a778-aee76f328b76完成，4次真实pi／DeepSeek调用均有效且进程回收；QA／trace入图成功，源数量仍1／1、官方文档2条。distill返回no_gated_entries、知识0条；不以任务completed宣称知识生成／Native出处闭环成功。字符串缺陷复验通过，原失败计数及全部部分写入保留。隔离进程已停止，日常依赖保留，证据session-processing-real-002，详见[处理记录](official-session-processing-20261112.md)。Goal仍active。

## round003／Native023：真实学习文档与来源通过，知识页仍缺

实际SQLite错误／纠正材料一次提交至原会话，官方任务765d849d-d5c8-4fb4-9261-5efbddd65c35生成2篇学习文档，9次真实pi调用均回收；来源IPC核对2QA／3trace，客户端正文／版本／job表均0。Native023完成真实文档→来源会话／工具→返回；31秒309帧，15轮目录／文档刷新，整窗／正文／数量像素一致。证据session-processing-real-003与native-official-learning-023。知识文档仍在原始文档入口且标题／处理状态未接知识页，不宣称第三稿或整项交付完成；自动采集、迁移切换、窄窗口该链路及最终包仍待完成。详细边界见[会话处理记录](official-session-processing-20261112.md)。原失败计数和数据保留，隔离进程收尾，日常运行依赖未变。

## 官方知识页增量与用户文案反馈

直接官方会话知识读取、逐项入图状态、Markdown／复制／出处返回已接候选；025普通窗和026最小窗实际显示原两篇学习文档，不写客户端镜像。空项目入口遗漏失败1轮已复验通过，末次目录校验部分返回失败1轮修复，计数分列保留。026连续观察有31帧尺寸变换，像素稳定仍未证实，不丢帧冒充通过。用户指出“旧知识／官方知识／入图”等文案难懂，已解释但尚未改完，不将询问当作接受只读限制。完整边界与证据见[官方知识页记录](official-knowledge-reading-20261112.md)。自动采集、迁移、全仓门禁与最终包仍未完成。

## 未上线数据策略更新

用户明确授权旧业务数据低成本可迁则迁、兼容成本高可删，取消长期维护双知识入口的约束。重新盘点31条／12163字节／各1版，当前适合迁移，实际迁移0／删除0；[影响和连续验证顺序](official-data-migration-20261112.md)已更新。界面技术文案在源码中修订，记录信息承担来源和能力说明，实际新包与Native仍待验证。此前自动采集、知识编辑历史、迁移及最终包缺口均未因此宣称完成。

## pi自动采集宿主层增量（源码／SDK结构，未安装）

补齐投影与生命周期适配代码：在授权后冻结leaf基线，仅于pi0.84.1的agent_settled读取新增parent链；不扫历史、无thinking／图片、未完成回复不作QA、工具需要真实匹配的call／result与isError。未知写入结果保留pending，仅查询回执、不盲目重发；工作区／会话切换、撤权、树导航和关闭隔离旧回调。18项定向检查通过；实际已安装SDK的SessionManager.inMemory结构检查通过，4个新entry投影1QA／1trace，基线历史未读取。

这些是宿主源码和真实SDK数据结构证据，broker仅合成回执：没有安装扩展、普通模型对话、官方HTTP写入或新增Native验收。首次宿主测试因strip-only不支持参数属性而加载失败1次，改显式字段赋值后通过；错误保留，不与旧hook桥接问题合并。实际daemon授权、固定官方策略在截断前的过滤、轻量持久回执／重启核对及App安装仍待实现；不能把稳定宿主ID当作官方服务幂等。证据pi-capture-projection-001，细节见[pi缺口](pi-official-capture-gap-20261112.md)。未重跑停止中的旧Codex hook或全仓test，没有构建完整运行包；Goal保持进行中。

本账本较早“旧数据尚未迁移”等段落保留当时状态；后续31条隔离迁移及Native027–033等实际增量分别以[迁移记录](official-data-migration-20261112.md)和[空态修复记录](empty-list-refresh-observation-20261112.md)为准。隔离核对通过不代表日常库已正式切换，知识编辑历史和最终视觉验收仍未完成。

## pi官方单次写入与持久回执切片

新增严格QA／trace typed remember适配，服务身份前后核验、官方回执的dataset／session／kind／ID逐项校验，单次POST不重试；操作标记保存在官方context，客户端仅保存摘要和身份。schema33轻量回执账本在网络前提交dispatching，并发仅一发送资格；未知结果重启／恢复不变成重发许可，已确认官方ID不可替换。迁移夹具同步移除新表、硬编码版本改当前常量，未放宽原内容／备份／恢复断言。

完整store43.055s／official0.867s／records1.376s通过，定向race store3.093s／official2.153s及三包vet通过；git diff --check退出0。仅临时SQLite及HTTP模拟，日常库／原31条／自有运行根未升级，没有模型或真实服务写入。新源码未接daemon授权入口、固定官方策略、未知结果的服务端对账和Native普通会话闭环；不能宣称自动采集已完成。现有历史读取限定Codex、材料授权不授予历史读取的边界已显式记入[pi缺口与影响分析](pi-official-capture-gap-20261112.md)，下一步按明确同意与可读范围接线，不复用错误授权。证据pi-capture-transport-ledger-001；Goal继续，旧停止问题不重跑，开发阶段不打完整运行包。

## pi明确采集授权增量

schema34增加独立新消息同意账本，Store／daemon／IPC接review、disable、authorize。连接与材料绑定不自动开启采集；确认和关闭有trusted App门槛，重开需新操作及前次key；daily及当前项目、连接代次、物理工作区、库和固定策略版本每次复核。旧确认不能复活撤销，备份恢复默认关闭，历史读取权限保持原边界。尚未接实际窗口入口、过滤执行、持久写入和新采集会话的受控读取，不宣称真实采集可用。

路径字符串误拒绝macOS别名具体缺陷首次失败1轮，已改物理身份并通过symlink正向／目录替换负向。最终完整store49.292s／daemon33.061s／serviceipc1.648s／records0.671s，定向race store8.934s／daemon3.149s及四包vet通过。旧迁移夹具同步新表，schema33升级备份、旧未知意图和恢复关闭检查通过。失败、SHA及边界见pi-capture-consent-001和[pi专项记录](pi-official-capture-gap-20261112.md)。所有库为临时库，原数据及运行依赖未动，没有真实模型／业务写入／新Native或完整运行包；Goal保持进行中。

## 官方策略复用与后置截断增量

已实现固定SHA的上游策略执行wrapper，复用allow_tool／redact／自调用／保守低信号函数，不另写过滤规则。源码只读一次校验后从同一字节执行，Python隔离环境与site、不接Agent凭据，容量／超时／取消有界。宿主不再在策略前截断正文与参数，新增1 MiB易失范围上限，官方typed写入限制由策略后Go阶段执行并保留截断标志。原18项宿主检查通过，UTF-8存储边界转由Go检查承接。

实际安装源码提交997fd6f与早期研究28fde881不是同一版本内容，虽然插件均标1.7.7。初次下载误用研究提交而SHA拒绝，后续缺失夹具导致定向失败，记录为准备错误；已按安装回执恢复正确来源及未修改策略、LICENSE，未放宽摘要。最终official完整1.485s、策略与授权定向agents1.484s／daemon1.688s，race agents2.636s／official2.276s及三包vet通过。证据pi-capture-policy-001，详细来源与失败见[pi记录](pi-official-capture-gap-20261112.md)。这仍是策略／源码层，尚未接授权持久发送、官方未知对账、安装和真实普通会话Native闭环；日常数据与运行依赖未动，旧停止问题不重跑，不打完整运行包，Goal继续。

## pi daemon发送与只读回执主流程（未安装）

submit／inspect现已接授权、固定策略、后置限制与持久回执，校验宿主来源及操作身份；发送前拒绝坏批次和无capture_receipt_v1能力的旧组件。单次POST、并发claim、响应丢失只读核对、明确未claim才可重提、缓存缺失仍unknown及发送中撤权均有模拟HTTP连续证据。新增官方own-user精确会话只读路由，返回实际字段验证，不存第二份正文；构建输入已接，新服务组件尚未运行，不能据源码说接口已上线。

实际SDK结构核对发现工具字段1000字符／20项规范，已在后置限制中采用并通过Go待发字段→真实SDK模型→receipt字段一致检查，截断边界如实保留。该次SDK导入另误触默认日志轮转，删除一份旧日志；已说明、留[复盘](cognee-sdk-log-isolation-incident-20261112.md)，隔离修正后默认日志metadata未变，旧日志未恢复。原31条业务源库SHA未变，不用修正后证据覆盖该影响。

最终完整daemon56.096s／official1.849s／serviceipc1.683s／records1.370s，race daemon28.980s／official3.090s与四包vet通过；Node20项、Python5项（含实际FastAPI HTTP路由）通过。证据pi-capture-broker-001，细节见[pi专项记录](pi-official-capture-gap-20261112.md)。本轮没有真实模型／官方业务写入／安装／新Native或完整运行包；CLI与宿主bridge、App同意安装入口、运行组件、普通真实会话→官方→Native仍待完成，原Goal范围保持。

## pi CLI、安装与真实SDK加载增量

CLI host-only授权／提交／查询和Node broker已接；stdin传全文、参数只含host身份，隔离通道拒绝，未知状态不盲重发。连接时不固定项目，daemon按cwd和既有授权解析；独立采集同意仍必需。安装复用唯一pi扩展入口，三个模块作为owned companion附摘要；检查、重连升级、旧入口升级、未知文件拒绝、外部修改保留和断开文件族依赖保护已覆盖，未新增模型工具或自动同意。

真实开发CLI+UDS通过；现有Go安装流程生成的入口已由实际pi0.84.1 jiti加载，实际SDK内存消息触发1QA／1trace且不读基线、不重复提交，shutdown后收尾。daemon为合成响应器，不是普通模型或官方服务闭环。最新Node27项，定向agents6.500s、完整agentbridge2.495s／store54.379s／serviceipc2.705s／records2.160s，race agents5.589s／agentbridge1.667s／store10.652s及vet通过。夹具JSON格式比较错误保留并纠正，产品失败计数未增加；未重跑停止中的旧hook／全仓test。证据pi-capture-cli-001与pi-capture-loader-001，详见[pi记录](pi-official-capture-gap-20261112.md)。

只有临时合成配置被安装，用户Agent配置未动；仍待App同意UI、新服务运行、普通真实对话自动保存和已采集来源Native读取，以及其余原Goal。自有daemon／CLI／宿主适配与Cognee官方策略／SDK／存储分列，不能整体称官方代码。开发CLI暂留下一步使用，没有完整运行包或release构建，Goal继续。

## 普通pi真实自动采集与重开增量

新组件实际启动并核对receipt能力，隔离主体／dataset及真实daemon就绪，真实安装入口和独立同意经IPC启用。两次实际pi0.84.1／DeepSeek Flash普通对话仅开放bash、不使用memory工具或人工remember，自动保存2QA／6trace／2session；8条回执通过官方own-user精确接口读回核对。第二次为重开pi进程，原5条回执身份／状态完全保留，新失败／成功工具与官方error／success一致。客户端正文／版本／任务表0条，原31条源库SHA未变，既有服务仍在；自有进程已安全停止，数据和组件保留。

首轮脚本失败由模型echo退出码，外层工具成功；审计误把脚本失败等同isError，夹具失败1次已据真实事件纠正，未修改官方状态；第二次普通会话另外取得真实失败工具证据。启动路径准备错误与首次health超时也保留，超时后核对原进程正常、不重启。证据`pi-capture-real-001`，细节见[pi专项记录](pi-official-capture-gap-20261112.md)。

该增量没有新增Native验收，App安装／采集同意UI、已采集来源受控读取、新链路知识处理、故障恢复和其余Agent仍待完成。既有历史读取契约仍保持，不以真实写入通过宣称App可用；完整包按用户要求未构建，Goal继续。

## 已采集会话受控读取及Native035／036增量

新captured模式仅从原明确同意和当前连接／项目／目录／库对应的saved receipt读取官方own-user精确记录，重算正文摘要、核对official ID，网络后再检查权限。关闭未来采集保留已保存内容的读取；材料权限仍不授予历史catalog。App接现有原始记录／Markdown／工具页，实际服务重启后复用原数据、binding和同意，真实daemon IPC读到2session／2QA／6trace，历史目录仍拒绝。

Native035普通与036最小窗口实际打开这批自动采集数据，分别31.000秒／31.001秒、各309全帧，覆盖16次刷新；正文／数量区域各1种RGBA，真实工具展开、搜索焦点及非零滚动857.35645／1207.7227保留。窗口截图、全帧、范围及坐标见`pi-capture-native-035/036`。新增“误沿用最近20条限制”失败1轮已在两尺寸复验通过；截图CGO构建准备错误单列。重复错误段、隔离入口未挂RuntimeController的状态文案及未做真实故障／撤权全矩阵等边界如实列于[pi专项](pi-official-capture-gap-20261112.md#已采集来源受控读取与native035036)，未宣称完整视觉交付。

完整受影响五包测试和相关vet通过，读取授权／篡改／迟到撤销／无正文镜像及App路由检查、定向race通过；最后空receipt页服务身份守卫另做定向检查，尚未进入035／036二进制，保留下轮统一候选项。自有进程收尾、原数据和组件保留、既有服务未动，没有完整运行包。App同意UI、新链路知识处理／出处、其他Agent和原Goal剩余项继续。

## Native037设置明确授权→普通pi→官方→阅读

App新增pi项目记录权限与保存状态卡，状态读取不启用；开启需明确确认，未知结果重试保留同一个intent，停止保留已保存内容。真实窗口停止后独立SQLite核对disabled，打开确认页仍未启用；点击确认产生新同意key，旧同意保留disabled。重开普通pi／DeepSeek正常对话，实际bash执行、无memory工具或人工remember，自动新增1QA／1trace，绑定App新key并由官方精确回执读回。设置页8→10，正常导航可读第三段会话；累计3session／3QA／7trace，原8条回执未改、客户端正文／版本／任务表仍0、原31条源库SHA未变。

实际证据`pi-capture-native-037`，含确认前实际数据核对、3张NSWindow截图、新回执、同意历史与收尾；真实daemon读取证据`pi-capture-real-001/after-app-read-ipc.json`。重复工具错误段已在实际窗口复验修复，原失败1次保留。037包含最后空页服务身份守卫，但没有新增空页或失败／撤权连续Native录制；长单行标题占4行的新显示问题失败1轮已记录，隔离RuntimeController状态文案边界保留。细节与下一步见[pi专项](pi-official-capture-gap-20261112.md#app明确采集同意与普通业务闭环native037)。

完整受影响五包测试、相关vet和新增设置权限流程race通过；停止中的旧问题不重跑。自有进程退出并保留组件、数据及新同意，日常服务未动，没有完整包。新设置故障／撤权状态、知识处理／出处、其他Agent、正式迁移切换和编辑历史等仍待完成，Goal继续。


## Native038–041后续

新增实际超时／同PID恢复／撤权与重连读取证据；窄窗口数量改为固定48px底栏内分行，与操作分开，撤权不再误指旧材料。schema35只增已采集receipt的读取复核元数据，原capture binding／consent／receipt不改，未授予历史或future capture。原3session／10receipt在App明确复核后恢复，daemon／App重开后保持；普通／窄窗口的31秒全帧与测试证据详见[pi专项最新记录](pi-official-capture-gap-20261112.md#native040明确恢复已采集内容读取与有正文的失败刷新)。未完整交付Goal或构建完整包。
