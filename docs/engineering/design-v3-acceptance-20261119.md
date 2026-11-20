# V3 筛选范围的实际验收报告

2026-11-19，Asia/Shanghai。本报告记录当前候选已取得的源码和实际Native证据。**限定的V3 dev主流程已通过：日期完整展开/末条阅读/来源返回/位置保持/切日，搜索提交/分类/来源返回/查询与位置保持，真实通用空分类，处理候选取消与配置浏览，画像只浏览。新增搜索来源返回缺口已修复并复验。非空通用及服务故障分支由已有夹具覆盖；滚轮工具投递未确认的历史问题保留，不代表该工具已修复。不进入release。**

用户要求阅读 `docs/ui-redesign/memlane-design-spec-v3.html` 后筛选实施范围，随后明确要求推进完成、通过Computer Use实际验收并给出报告。范围与连续用例见[执行记录](design-v3-execution-20261119.md)。窄窗口、悬停项目菜单、全局紫色主题、新项目状态页、全量动效、模型摘要、数据迁移/索引重建均不在本轮。

## 最终汇总核对（01a1f008，仅审阅证据）

按用户指定，本会话未再次操作窗口，已读取01a1eed7的最终连续验收回执，并实际打开8张关键截图。最终提交为 `b26184b`；其中 `project_reading_ui.go` 和 `search_source_return_test.go` 与验收时 `final-source.json` 的SHA-256均一致。主证据目录共有29张实际PNG和29份控件树回执，数量不表示29个独立用户场景。

| 验收项 | 最终汇总结论 | 关键实际证据与边界 |
| --- | --- | --- |
| 日期完整列表与返回 | 通过 | 20/80→80/80，键盘定位末条并阅读来源后返回，80条及末端位置保留；[末端列表](../../.local/design-v3-acceptance-20261119/native-verified-mchgg9c3/25-day80-bottom.png)、[来源返回后](../../.local/design-v3-acceptance-20261119/native-verified-mchgg9c3/28-day80-return.png)。 |
| 切日与恢复最近 | 通过 | 切11月17日恢复20/69，返回最近恢复初始范围；[切日回执](../../.local/design-v3-acceptance-20261119/native-verified-mchgg9c3/29-day69-reset.png)。 |
| 搜索/类型/来源/位置 | 通过 | dev_recovery搜索、记忆/原始记录本地切换、官方来源阅读、返回查询与非顶部位置；[来源阅读](../../.local/design-v3-acceptance-20261119/native-verified-mchgg9c3/14-search-source.png)、[返回搜索](../../.local/design-v3-acceptance-20261119/native-verified-mchgg9c3/16-search-return.png)、[非顶部返回](../../.local/design-v3-acceptance-20261119/native-verified-mchgg9c3/18-search-scroll-return.png)。 |
| 真实通用空分类 | 通过 | 真实库0条，个人偏好/通用经验显示分类空，显示全部恢复空库状态；[个人偏好空态](../../.local/design-v3-acceptance-20261119/native-verified-mchgg9c3/31-common-preference.png)。没有新增样本或清空业务库。 |
| 处理候选取消与配置浏览 | 通过 | pi候选短Modal取消，Provider仅浏览；[取消回执](../../.local/design-v3-acceptance-20261119/native-verified-mchgg9c3/20-processing-cancel.png)。actual始终existing-codex revision2，generation revision17不变，不认定已启用候选。 |
| 完整记忆与画像只浏览 | 通过限定阅读流程 | 实际正文、来源及返回已执行；画像已有文档只读，`profile-read-only-check.json`的unchanged=true，原三种Agent operation_id未变。未发起新生成或模型质量验证。 |
| 搜索部分失败/非空通用/服务错误 | 夹具通过，Native条件未覆盖 | 复用已有HTTP/UI专用夹具；没有以制造日常故障或写入示例补覆盖。 |
| 滚轮工具投递 | 历史未确认，保留 | 本轮实际位置保持由键盘/滚动条方式证明，不能写成滚轮工具缺陷已修复；旧METH-SCROLL01与CU-PREP01证据不清零。 |

配置回执三份（state-before、state-after-cancel、state-after-browse）均为existing-codex revision2/generation revision17。较早召回统计从84/521增长至87/546、之后88/554，属于不同阶段，不能混成“全程没变化”；仅最终搜索核对窗口的两份state-final-search-start/end均为87事件、546命中，可据此确认该窗口的人工检索未增加统计。

最终全Go test/vet、相关race和Python lint通过来自验收会话的检查回执；本汇总会话未重跑。搜索来源返回丢入口R19-SEARCH-RETURN01已按旧基线1轮失败→修复→同窗复验通过记录，夹具身份错误另行保留。实际来源只读了已展示范围，不把20/97轮描述为97轮全部读完。

**交付结论：本轮筛选采用的V3 dev主流程验收完成。** 非空通用/服务故障的实际分支、滚轮工具修复、其他Agent新桥认证与模型质量、完整包/release、严格冷启动不包含在本结论内。此前“等待接续”的段落保留为历史状态，最终结论以本汇总及下方最终连续回执为准。

## 早期候选与真实环境（历史）

- 源码为 `main / 6c524b2` 加当前工作树。本轮生产修改涉及8个Go文件，另有行为回归；工作树包含大量此前修改，不能用HEAD代表实际候选。
- 精确本轮原文件及补丁：`.local/design-v3-acceptance-20261119/before-source/` 与同目录 `*.go.patch`。最终相关文件摘要为 `source-manifest.json`，不是整个工作树冻结证明。
- 复用 `.local/daily/runtime.json`、唯一 `.local/daily/official-store`，官方服务61255、控制18777、桥18173；本轮初查owner24736、actual existing-codex revision1。没有为验收切换另一库、造记忆或生成画像。
- 使用MyGo Dev真实窗口，bundle ID `io.github.zwhu.memlane.dev`。窗口逻辑1200×820，Computer Use输出图1124×768；二者尺度不同，不能把截图像素当作逻辑字号测量。
- Computer Use Accessibility、Screen Recording均granted。旧候选窗口320982/PID25662，新界面截图窗口321367/PID84319，之后watcher热重载至321384；不是完整release包。
- 私有实际截图/完整控件树含日常内容，保留在 `.local/design-v3-acceptance-20261119/`，不提交真实记忆、聊天、配置或密钥。

## 早期实际操作与当时结论（历史）

| 场景 | 实际执行与观察 | 当前结论 |
| --- | --- | --- |
| 旧详情关闭与来源阅读 | Computer Use点击“关闭记忆详情”，随后实际窗口显示原始会话、来源记忆上下文及“返回来源记忆” | 该次关闭动作成功；不是新候选完整详情→来源→返回验收 |
| 旧总览与日期缺陷 | 点击总览，再点击2026-11-16“新增80条记忆”；实际控件树只有20条记忆入口，没有展开全部 | V3-DATE01旧失败已实测，1轮；截图已保留 |
| 新总览可读性和月份 | 读取热重载后真实窗口：总览无三行口号，统计仍显示，月份随真实周列定位，列表字体较清晰 | 新候选实际截图视觉核对已执行；不是像素字号测量或点击验收 |
| 日期20→80→来源→返回→切日 | 新候选点击被工具准备状态失效阻断；源码中真实80样本及UI Tester展开/最近复位通过 | Native连续操作未完成 |
| 搜索提交→类型过滤→详情→来源→返回 | 查询签名、类型本地筛选和返回由源码回归覆盖；本轮未完成新候选真实输入/提交链 | Native连续操作未完成 |
| 搜索部分失败与重试 | HTTP专用夹具覆盖两种单路失败、成功0条仍不重发、仅失败路恢复；不停止日常后台制造故障 | 夹具验证通过；不是日常故障注入或Native失败场景 |
| 通用记忆未选择/分类空/读取失败 | 旧缺陷定向复现，新UI状态及回归检查通过 | Native分类切换与阅读未完成 |
| 配置浏览/候选/取消与active | 原确认和回执逻辑保留，相关Go行为检查通过；本轮没有实际确认切换或验证模型 | Native操作未完成；不声称真实切换或模型质量通过 |
| 项目6行预览→完整详情→来源→返回 | 保留三列、召回排序与原文，UI及阅读行为检查通过 | Native新候选完整链未完成 |
| 用户画像仅浏览 | 手动入口文案与未知不重试分支通过Go检查；本轮未代用户发起生成 | Native浏览未完成，不声称生成/保存验收 |

## 实际截图

- [旧详情关闭后的来源阅读](../../.local/design-v3-acceptance-20261119/before-source.png)
- [旧总览](../../.local/design-v3-acceptance-20261119/before-home.png)
- [旧80条日期只显示20条](../../.local/design-v3-acceptance-20261119/before-day-80.png)
- [新总览与月份实际窗口](../../.local/design-v3-acceptance-20261119/candidate-home.png)

部分截图对应完整控件树同名txt；其他原始树保留在Computer Use工具回执中。截图均实际打开审阅，不把HTML合成示意作为Native结果。

## 早期源码检查（历史）

| 检查 | 真实结果 | 回执 |
| --- | --- | --- |
| `sh scripts/go.sh test -json ./...` | 退出0，424个通过事件、3个跳过，包含子测试；无失败事件 | `go-all-final.jsonl`、`verification-summary.json` |
| `sh scripts/go.sh vet ./...` | 退出0 | `go-vet.log` |
| 搜索分路/取消定向race | 退出0，无race报告 | `search-race.log` |
| `python3 scripts/task.py -- python3 scripts/lint.py` | 退出0，模型参数与桥接/Provider展示门禁通过 | `lint.log` |

测试顶层与子测试分开统计在 `verification-summary.json`；424不是424个独立用户场景。初始四类缺陷失败回执 `regression-before.log`、中间编译/旧断言问题 `targeted-after*.log`、旧HTTP夹具问题 `go-all.jsonl` 全部保留，没有覆盖原失败。具体问题计数见执行记录。

## 最终验收回执与历史接续

### 01a1eed7 最终连续验收回执

在用户确认停止其他窗口操作后完成后续验收。当前候选为0853e50加本轮readingLocation修复及行为回归；最终Native PID73656/window324089，逻辑1200×820、截图1124×768，使用外接屏。拖动窗口与后续控件坐标为负Y，证据保留于操作回执，不用截图像素推算逻辑字号。

| 场景 | 本轮实际结果 | 结论/边界 |
| --- | --- | --- |
| V3-C01 | 11月16日20/80→显示80/80→键盘焦点导航滚至第80条→完整详情→对应官方Codex会话→返回详情/总览；80条与末端位置保持。切11月17日恢复20/69，返回最近恢复初始范围 | 主流程通过。使用键盘滚动，不宣称滚轮工具已恢复。来源读取20/97轮，97轮/1890工具总数来自官方回执，不说本轮读完97轮。 |
| V3-C02 | 输入dev_recovery并Enter，全部7个关键词/62个语义结果；记忆4/10、原始记录3/52本地切换；打开记忆/来源并返回搜索，词/类型/结果保持。键盘定位第四条造成实际滚动，打开详情后返回位置和选中保持 | 主流程通过。统计核对阶段87个事件/546条命中保持不变；较早84/521的基线与期间Agent自动召回分开，不归因于人工检索。 |
| V3-C03 | 复用两路失败、成功0条、仅失败路重试的HTTP夹具；最终全Go门禁再次通过 | 夹具通过，不中断日常服务制造故障。 |
| V3-C04 | 真实通用库0条，个人偏好/通用经验均显示分类空，显示全部返回空库状态；没有误显示“第一条”阅读提示 | 真空库/分类切换通过。非空未选择及读取失败仍为夹具证明，无造样本或新保存。 |
| V3-C05 | 选择已有pi候选、打开短确认Modal、明确取消；浏览Provider不应用。管理active始终existing-codex revision2，generation revision17不变 | 通过，没有启用pi/Provider、写配置或发起模型验证。一次用户输入打断另记；最终自动取消已另取得完整回执。 |
| V3-C06 | 完整记忆正文/元信息/官方来源读取及返回已执行；进入用户画像显示已有Codex文档，未点击重新导入 | 阅读/只浏览通过；Codex/pi/TraeX原画像operation_id均未改变，不声称重新生成或模型质量验收。 |

截图及完整树在`.local/design-v3-acceptance-20261119/native-verified-mchgg9c3/`，10–30为本轮主要连续回执，31–33为通用空分类。实际图片已打开审阅；真实内容/配置不提交。旧final-native和native-final产物保留，不能把别的会话局部操作改称本会话重新执行。

R19-SEARCH-RETURN01累计旧基线失败1轮：openSourceRecord保存阅读位置，restoreReadingLocation普通导航清掉搜索关联，快照却没有恢复它，导致关闭详情后无“返回搜索”。现加入原搜索位置、Anchor及读取身份；恢复同时检查当前dataset和身份，普通导航继续清除。新增行为回归、完整Go test/vet、相关race和Python lint通过；真实同窗来源返回/搜索词/类型/位置复验通过。第一次身份夹具只写缓存值，被真实身份重算覆盖，已改实际连接变化，原失败日志保留为方法错误。

额外方法边界：被终端遮挡时MyGo暂停绘制，旧截图不能判断后台请求是否完成。显露窗口后来源自动读取；本轮随后保持窗口实际可见。窗口热重载后由正式dev入口接续同库，再用官方open_app重新发现当前可见窗口，实际点击成功；不将其外推为旧Host已具备MCP重连能力。控件ID失效、focused属性导致的定位正则错误、clickCount=0实际按下按钮等原回执保留。METH-SCROLL01未确认投递的历史记录继续有效，本轮不靠换版本/窗口清零；键盘焦点导航和拖动滚动条为已实际执行的另一输入方式。

本轮日常owner158及三个数据服务保持，正常收尾仅本轮旧UIwatcher86666；当前通过dev.py附着同库新窗口。TraeX另因host_executable_changed被接入保护要求审阅，未将其描述为保存失败或自动重装。Gemini真实认证/模型任务边界仍见对应交付记录，不因第四行可见而宣布通过。当前不做完整包/release或无缓存冷启动。

### 用户指定的验收与汇总分工（历史交接，现已取得最终回执）

用户明确选择：“由另一会话完成，本会话只汇总报告”。据此：

- 会话 `01a1eed7-55ca-7c63-9c2b-9acdc8129524` 独占后续Computer Use、Native导航/滚动/来源/搜索/配置取消及必要修复。
- 本会话 `01a1f008-6548-7773-a05e-8cd527274bb9` 停止所有窗口操作，不新增生产实现、模型任务或运行服务变更；仅汇总已有及验收会话最终提供的证据。
- 本会话既有私有证据目录为 `.local/design-v3-acceptance-20261119/final-native/`；03/05是真实空分类，06/07是日期20/80→80/80，08/09/10是滚动投递未确认的原始图。01首张过渡截图与02落稳截图分别保留，不混用。
- 当时待验收会话明确回执：来源返回详情/日期与切日、实际滚动和位置恢复、搜索提交/类型过滤/返回、处理候选取消前后active一致、画像仅浏览。现已按上方最终回执收口；结果继续按来源注明，不改称本会话重新操作。
- 最终汇总按“通过／未通过／未覆盖”逐项记录，分别链接操作、截图、对应候选和问题计数。收到完整闭环回执前不把工具连接恢复、截图读取或源码通过当作连续Native通过。

当时轮次的中断/结束只说明该轮状态，不能替代完成回执。后续已取得最终连续回执并完成汇总；既有数据、源码与所有失败证据保留。

### 2026-11-19 新Host实际操作与窗口占用核对

本轮“继续验收”后新MCP已能点击，窗口323748/PID97697保持，逻辑1200×820。日常owner158 ready，同一官方库，actual existing-codex revision2。当前HEAD0853e50包含另一会话Gemini提交；本轮不代其验收新增桥，不修改生产代码或重跑已有完整矩阵。

已由本会话01a1f008实际操作并保存：

- 总览导航及落稳画面：264条记忆、66条原始记录，8项目。首次点击回执截图仍是前一通用页，随后fresh get_app_state的02-home-settled.png才是总览，原图保留，不能把首张过渡画面当成已落稳。
- 2026-11-16日期：显示20/80→点击显示全部→显示80/80，实际新窗口行为通过。证据final-native/06-day-20-of-80.png、07-day-80-of-80.png，完整树另存同名full.txt。
- 真实通用库0条：个人偏好、通用经验均“当前分类没有记忆”，没有“新增第一条”阅读主按钮；显示全部返回全部分类，刷新阶段准确显示正在刷新。证据03-common-preference-empty.png、04-common-all-empty.png、05-common-experience-empty.png。没有造内容，因此“已有内容但无选择”正向Native条件仍未覆盖，原UI夹具边界保留。
- 日期滚动：语义scroll、坐标scroll、Raise后坐标scroll三次回执均未取得第一可见条目改变，分别保存08/09/10图。不把投递成功等同于滚动成功；尚未证明产品根因，方法/投递问题METH-SCROLL01暂记3次未确认，不能并入日期截断缺陷计数。

窗口发生本轮未发起的通用导航/分类变化。只读核对日志确认另一会话01a1eed7正使用同一窗口：mac-cpu-mcp-21123.log在14:07:53点击通用记忆，14:08:13点击个人偏好，14:13:29再次通用导航，14:13:50个人偏好，14:14:09通用经验；本轮动作记录在mac-cpu-mcp-22658.log。**本次导航覆盖已经证实为并行会话操作，不能当作产品自行跳页回归。**它不证明过去准备缓存故障由争用引起。

已请求用户明确后续由本会话或另一会话独占连续验收，等待期间不继续争用窗口。全部当前实际截图与中间结果在 `.local/design-v3-acceptance-20261119/final-native/`，已有结果不覆盖。剩余详情/来源返回、切日、搜索、配置取消和画像浏览继续待验。

### 2026-11-19 12:01 工具诊断与本会话收尾

用户再次明确“继续推动吧”，恢复工具问题调查，原CU-PREP01四次准备状态失败保留。使用diagnosing-bugs方法及TRAE CLI MCP指引，沿实际已失败的get_app_state→click回执核对官方MCP实现和本会话日志。

已证：

- 官方 `checkAppPrepared` 将当前应用解析出的bundle/PID与get_app_state登记的窗口所属bundle/PID比较。拒绝发生在点击handler之前，尚未向Native投递点击。
- 本会话MCP45049（wrapper45045，Host44633）日志在11:57:07仍输出 `knownPids:[25662]`，该PID来自最初03:00时的Memlane；当前内核中的实际Native为85873，窗口322535。去掉windowId的get_app_state不能找到窗口，日志明确显示旧PID匹配失败。旧应用身份清单滞留能解释读取指定新窗口成功、动作门禁仍失败；不是已证明的系统权限或窗口争用问题。
- 元数据解析未取得chatSessionId/messageId，两次均undefined，实际落在同一默认准备状态域；不能仅凭元数据命名差异认定此次错误由会话键改变导致。
- 官方macOS绑定的listRunningApps走MCP进程内原生绑定；当前可调用工具和CLI mcp命令没有清空该清单或重连当前已加载客户端的入口。未修改已安装TRAE资源、插件脚本或系统权限。

收尾：核对45049内核启动身份、命令、wrapper及Host归属后，仅向本会话MCP子进程发TERM。子进程和wrapper正常退出；随后同一Host调用list_apps返回 `Transport closed`，未自动创建新MCP。此Transport closed是本次明确工具收尾后状态，不当作新的产品失败或原日期缺陷轮次。

诊断小型回执保存在 `.local/design-v3-acceptance-20261119/cu-diagnosis/`：`stale-app-resolution.log`、`mcp-before-reconnect.json`、`mcp-after-reconnect.json`。不保存全量其他会话日志。

本轮后核对 `dev.py status`：日常owner84481（10:57:43启动）、phase ready，官方/控制/桥端口仍61255/18777/18173，实际引用existing-codex revision2，同一 `.local/daily/official-store`。Native85873及UI watcher属另一已验收会话，未向它们或日常服务发信号。当前HEAD为b42ec80，V3源摘要仍无漂移；早期HEAD/PID保留为历史时点。

当前必须在新的Host连接中恢复本会话。已由当前0.215.0的resume --help核对：

```sh
traex resume --no-daemon 01a1f008-6548-7773-a05e-8cd527274bb9 -C /Users/bytedance/work/AI/memlane
```

先退出当前TraeX，再运行；--no-daemon建立新的嵌入Host，避免复用旧客户端。此命令不代表本轮已执行恢复或验收通过。

恢复后重新发现工具、list_apps并读取实际窗口；首先验证get_app_state→点击总览确实改变/保持可确认状态，再进入80条日期→20/80→显示全部→来源返回、搜索与配置取消的既定用例。新窗口/源码漂移重新核对，旧失败计数保留。无新变动不重复全量测试、模型下载或可靠性故障注入。

### 另一会话结束后的本轮实际尝试

用户先明确“等另外一个会话结束之后一起验收”，随后明确“执行验收”。读取最新 `roadmap-reliability-handoff-20261119.md`：另一会话五项可靠性修复及dev主流程已经收口，有24张实际Native截图；本轮不重复模型下载、故障注入或全量测试。其结果来源和局部边界继续引用[可靠性交接](roadmap-reliability-handoff-20261119.md)，不描述为本会话重新操作通过。

本轮 `dev.py status` 实际返回owner94208 ready、官方61255/控制18777/桥18173、同一 `.local/daily/official-store`、existing-codex revision2。V3相关14文件与原 `source-manifest.json` 无漂移；当前工作区141项修改/未跟踪条目，并非全部属于本轮。未重启后台、另启服务或修改Agent配置。

Computer Use本轮实际前置检查：

- `check_permissions`：Accessibility和Screen Recording均granted。
- `list_apps`：Memlane Dev窗口321801、逻辑1200×820。
- `get_app_state`：实际PID97915，截图1124×768，总览264条记忆、58条原始记录；完整控件树中2026-11-16仍80条，目标ID2510。已实际查看并保存[本轮总览](../../.local/design-v3-acceptance-20261119/r-01-home.png)及同名完整树。
- 第一次同一exec：`get_app_state(app=io.github.zwhu.memlane.dev,windowId=321801)`后点击最新树目标2510，工具返回`Computer Use is not prepared`，未产生日期选择结果。
- 第二次核对工具列出的应用名，在同一exec以`app=Memlane Dev`、同一窗口先读取再点击同一目标，仍返回相同错误。应用标识替代没有解决准备状态，不继续盲试。

因此，等待另一会话结束并未解决错误，**不能把窗口争用认定为根因**。CU-PREP01原2次、本次2次累计4次具体准备状态失败；它属于工具/方法问题，不累计到V3-DATE01或其他产品缺陷。本轮按Computer Use技能的持续错误停止要求结束点击尝试，等待Host的Computer Use连接恢复。既有失败和本轮尝试均保留，不把截图读取当作日期/搜索/配置操作通过。

### 保留的原错误

新窗口两次在同一exec读取 `get_app_state` 后点击仍返回：

```
Computer Use is not prepared for 'io.github.zwhu.memlane.dev'. You first must call `get_app_state` to get the latest state before doing other Computer Use actions.
```

此前同一工具确实成功点击旧详情、总览和日期，故不能把当前错误描述为从未获得权限。窗口期间出现本轮未提交的roadmap搜索输入及图谱切换，是否来自其他会话/本人尚未确认，不能据此认定根因。

依[Computer Use技能](/Users/bytedance/.trae/plugins/cache/traex-bd-plugins/computer-use/0.3.2/skills/computer-use/SKILL.md)“Stop and report persistent Code Mode, transport, permission, screenshot, or signing errors.”暂停重复工具尝试，不使用其他UI自动化替代。

工具恢复并确认窗口可由本轮连续操作后，重新list_apps/get_app_state，只补表中未完成的Native流程，保存截图、操作结果和真实边界。无新代码/失败不重复通过的全量矩阵。日常后台和运行依赖继续保留，当前报告不能作为Native验收通过或release完成证明。

### 接续实际操作及第二批修复

接续以 main/b42ec80 加当前工作树为候选，复用 owner86475 和唯一日常库。实际窗口323665/PID68101，逻辑1200×820，输出图1124×768；已查看实际截图。旧CU-PREP01计数及失败证据保留。

1. get_app_state 后点击2026-11-16：显示「已显示20条，共80条」与「显示全部」。再点击展开，显示「已显示80条，共80条」；完整控件树中该列表恰有80个记忆入口。
2. 选中第一条记忆，实际读取完整正文、日期、分类和召回0次；点击「查看来源会话」，实际读取对应官方Codex会话的16轮问答、55条工具记录。来源为会话级，没有具体问答定位，界面准确提示此边界。
3. 两次滚动分别使用列表element_id及截图坐标，工具返回已投递，但截图位置没有可确认变化。此处只证明展开与读取，不声称滚动／位置恢复已通过。
4. 来源返回点击前，旧窗口消失；list_apps定位热重载后的323695，新get_app_state回到总览。随后点击通用记忆仍返回「Computer Use is not prepared」。这次重载打断与准备错误另留痕，不累计为日期产品缺陷失败，不清零CU-PREP01。停止该窗口后续重复操作。

私有截图和树在 `.local/design-v3-acceptance-20261119/native-final/`：01-date20、02-date80、03-detail、04-source、05/06-scroll、07-after-reload。图片均为实际工具返回，真实内容不入Git。

本批还修复召回计数30秒刷新重排、滚动中保持展示范围、总览按dataset批量读取及离页取消目录／统计。80条同dataset只发1次counts请求与迟到目录不启动统计由HTTP夹具及Go检查证明；未将其冒称为实际负载或全部Native通过。消息明确拒绝、编号未知恢复与激活marker中断恢复的修复另见[日常收口账本](daily-consolidation-20261119.md)。最终工作树完整Go test/vet、lint和35项相关Python检查通过，提交候选另行独立核验。

剩余Native：来源→返回详情→返回日期／切日，真实滚动与位置恢复，搜索提交／类型过滤／返回，通用分类与阅读，处理候选取消及画像只浏览。环境已有能力的当前源码复核见[环境准备复核](environment-readiness-20261119.md)。不进入release，不因工具故障重启日常服务或重复模型任务。

用户再次恢复后，当前窗口323748/PID97697可读；本会话MCP49710仍保留旧PID68101，最新导航点击在准备门禁被拒绝，1次，旧计数保留。已核对本次调用所属客户端、代理退出行为及CLI无已加载客户端重连入口；本轮没有继续试点或启停日常栈。准确的新Host命令、源码摘要和剩余用例见[Native接续](native-verification-resume-20261119.md)，不能将本次诊断描述为工具已恢复或Native已通过。
