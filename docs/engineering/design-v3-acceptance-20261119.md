# V3 筛选范围的实际验收报告

2026-11-19，Asia/Shanghai。本报告记录当前候选已取得的源码和实际Native证据。**源码检查通过；接续已实际完成80条日期展开、详情及官方来源读取。开发窗口热重载后工具再次出现准备状态错误，返回、搜索、分类和配置取消等连续操作仍未完成，当前不能认定V3全部Native验收完成。**

用户要求阅读 `memlane-design-spec-v3.html` 后筛选实施范围，随后明确要求推进完成、通过Computer Use实际验收并给出报告。范围与连续用例见[执行记录](design-v3-execution-20261119.md)。窄窗口、悬停项目菜单、全局紫色主题、新项目状态页、全量动效、模型摘要、数据迁移/索引重建均不在本轮。

## 候选与真实环境

- 源码为 `main / 6c524b2` 加当前工作树。本轮生产修改涉及8个Go文件，另有行为回归；工作树包含大量此前修改，不能用HEAD代表实际候选。
- 精确本轮原文件及补丁：`.local/design-v3-acceptance-20261119/before-source/` 与同目录 `*.go.patch`。最终相关文件摘要为 `source-manifest.json`，不是整个工作树冻结证明。
- 复用 `.local/daily/runtime.json`、唯一 `.local/daily/official-store`，官方服务61255、控制18777、桥18173；本轮初查owner24736、actual existing-codex revision1。没有为验收切换另一库、造记忆或生成画像。
- 使用MyGo Dev真实窗口，bundle ID `io.github.zwhu.memlane.dev`。窗口逻辑1200×820，Computer Use输出图1124×768；二者尺度不同，不能把截图像素当作逻辑字号测量。
- Computer Use Accessibility、Screen Recording均granted。旧候选窗口320982/PID25662，新界面截图窗口321367/PID84319，之后watcher热重载至321384；不是完整release包。
- 私有实际截图/完整控件树含日常内容，保留在 `.local/design-v3-acceptance-20261119/`，不提交真实记忆、聊天、配置或密钥。

## 实际操作与逐项结论

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

## 源码检查

| 检查 | 真实结果 | 回执 |
| --- | --- | --- |
| `sh scripts/go.sh test -json ./...` | 退出0，424个通过事件、3个跳过，包含子测试；无失败事件 | `go-all-final.jsonl`、`verification-summary.json` |
| `sh scripts/go.sh vet ./...` | 退出0 | `go-vet.log` |
| 搜索分路/取消定向race | 退出0，无race报告 | `search-race.log` |
| `python3 scripts/task.py -- python3 scripts/lint.py` | 退出0，模型参数与桥接/Provider展示门禁通过 | `lint.log` |

测试顶层与子测试分开统计在 `verification-summary.json`；424不是424个独立用户场景。初始四类缺陷失败回执 `regression-before.log`、中间编译/旧断言问题 `targeted-after*.log`、旧HTTP夹具问题 `go-all.jsonl` 全部保留，没有覆盖原失败。具体问题计数见执行记录。

## 当前阻断和下一步

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
