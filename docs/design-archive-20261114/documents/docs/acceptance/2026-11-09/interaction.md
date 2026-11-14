# 交互独立验收

日期：2026-11-09。角色：交互设计师，按 `docs/agent-prompts/interaction-designer.md` 验收。

固定源码：`build/acceptance/2026-11-09-001/source`，基于 `99246f43e1ae7ab7c1adf3ac9e5a859dd80cfcb1` 加本轮 dirty snapshot。下文源码行号均指该固定目录，不指后续可能变化的工作树。未修改业务代码、用户配置或业务数据，未提交 Git；窗口由测试角色统一操作。

## 范围与方法

覆盖从空应用检查和准备本机环境，到选择 Agent、项目可跳过、接入验证、首个真实任务与记忆回看；同时检查编辑、未保存草稿、历史恢复、建议审核、失败恢复、关闭窗口与退出。按“入口 → 操作 → 反馈 → 结果 → 下一步”逐段核对。

已阅读固定快照的 `AGENTS.md`、`product.md`、`design.md`、`core-business-flows.md`、最新 `product-questions.md`、`first-launch-design.md`、`memory-browsing-design.md`，并通过 `view_image` 实际打开 `docs/images/home.jpg` 与 `docs/images/memory-detail.jpg`。设计图是设计参考，不是本轮运行证据。最新 Q1–Q3 规则优先于较早的 F00 项目优先叙述。

独立阅读源码形成断言，再向统一窗口操作者请求新截图与动作事实；不阅读其他角色的结论报告。已通过 `view_image` 独立查看本轮 `testing/screenshots/01` 至 `25` 的截图（未查看 `02b`），另查看普通无运行配置包的 `production-screenshots/01`、`02`，并读取对应状态及 `runtime-results.json`、`production-runtime.json` 的实际动作/IPC/AX 结果。源码审阅只能证明实现分支与阻塞，不能证明窗口交互、真实 Agent 调用或保存副作用已在本轮通过。认知走查不是用户测试。

## 当前结论

**按最新确认的首次使用主流程，不通过。** 准备完成后的主按钮仍要求先选项目；无项目时连接按钮不可用，官方路径也要求项目 UUID。因此“准备 → Agent 选择 → 项目可跳过 → 接入 → 首任务”存在确定性实现缺口。记忆维护与真实任务后的若干反馈分支已有代码依据，需分别以本轮实际窗口证据判断，不抵消主流程阻塞。

普通无 `runtime-config.json` 包还有更早的阻塞：实际启动后服务未连接，设置显示本地引擎待准备且当前无可安装版本，用户没有准备/启动入口。受控测试包的准备通过不能覆盖该普通包路径，见 UX-20。

本轮独立通过的范围：受控组件源下的本机准备状态、真实空首页、手动新增并可检索、返回保留草稿、编辑 v2 与当前正文检索、恢复确认前保持 v2、确认后产生 v3 并保留 v1/v2、关窗继续服务、默认退出继续服务、重开保留版本、明确停服与退出选项生效。实体/关系/原始记录只验证了空材料状态。**本轮没有真实官方 Agent 正常任务、连接验证、来源阅读器或建议审核的执行证据，不给这些路径签通过。**

## 阻塞项

### UX-01：准备后的第一步仍是项目，未兑现 Agent 优先

- **级别 / 状态：主流程阻塞，源码与本轮窗口共同确认。**
- 规则：`docs/product-questions.md:16` 确认环境准备完成先选择 Agent；`:26` 给出完整新顺序。
- 实际入口与行为：`internal/desktop/first_launch_presentation.go:351` 的就绪说明要求选择项目；`:397` 主按钮为“选择项目并开始记录”，`:404–408` 标记首启完成并进入项目录入。用户尚未选择 Agent 就被引导到项目。
- 实机：`04-preparation-terminal.png` 的 ready 主按钮及 `05-start-use-project.png` 的点击后“添加本地项目”表单共同复现。`04-preparation-terminal-state.json` 的 `first_launch.stage=ready`、`first-launch-use.label` 与截图一致。
- 影响：用户关于“进来第一步做什么”的已确认决定没有落实；不能将旧项目优先旅程通过作为此项通过。
- **关闭条件：** 空业务根完成实际准备后首个业务主操作进入 Agent 选择，选择结果衔接后续接入；保留准备阶段与 Agent 尚不可用的差别，并提供该连续窗口证据。

### UX-02：项目无法跳过，空项目用户不能继续接入

- **级别 / 状态：主流程阻塞，源码与本轮窗口共同确认。**
- 规则：`docs/product-questions.md:32–38` 要求可跳过项目，在首条有效数据到达后依据受信仓库身份建立或复用项目；连接本身不建立空项目。
- 实际入口与行为：`internal/desktop/agents.go:343` 要求连接项目，`:374` 因 `s.Project == ""` 禁用“连接”；`:502` 先执行工作区校验。官方入口 `internal/desktop/cognee_agent.go:56` 无项目直接返回，`:233` 同样禁用准备插件。`internal/desktop/cognee.go:246` 手动授权也必须具备项目。
- 实机：`07-agent-without-project.png` 已选真实发现的 Codex，项目下拉为“选择本地项目”，“连接”不可用且无跳过入口；对应 state 的 `connect-agent.states=["disabled"]`。`runtime-results.json` 的 `empty-project-agent` 显示仅两个全局范围、`connections=[]`。观察前没有预建项目来绕过此状态。
- 影响：空根用户不能完成已确认的跳过路径；增加一个不改变接入契约的跳过按钮也不能关闭此项。
- **关闭条件：** 实机演示无项目接入成功且未创建空项目；真实有效数据进入后正确建立/复用项目，来源不明时保留待归属并给出选择入口；失败与重试不重复建立项目或记录。

### UX-03：首任务已有执行入口，但新手主线与回看尚未整合为新顺序

- **级别 / 状态：主流程未通过；现有局部能力与新旅程分开验收。**
- 代码中的局部路径：项目页“官方接入与同步” → 准备环境 → 确认插件 → 输入正常任务 → “确认采集并运行此任务” → “官方结束同步并接收材料” → “进入项目记忆”。见 `internal/desktop/browsing.go:211`、`cognee_agent.go:227–246`、`cognee.go:267`。
- 已有正确反馈：插件安装成功仍明确“首次正常任务和保存尚未验证”（`cognee_agent.go:86`）；任务结束提示下一步同步（`:124`）；取消和失败明确可能存在官方已提交材料但尚未导入（`:121`、`:203`）。这些文案符合不把任务退出或配置写入当记忆保存成功的原则。
- 未闭合部分：该入口必须经过项目，尚未承接 UX-01/02 所要求的 Agent 选择与可跳过项目；当前代码要求用户自行输入任务，按按钮完成多段同步。新顺序的首任务完成页与明确的“本次产生了什么／归到哪里／点哪里看来源”反馈未在本轮证明。
- **关闭条件：** 同一空根从 Agent 选择连续执行正常真实任务，观察真实保存/索引结果，直接回看本次记录、知识及出处，并在新会话召回；不得以验证 marker、直接数据库写入或历史截图代替。

### UX-20：普通包停在“等待本地记忆服务”，没有可执行的准备下一步

- **级别 / 状态：首次使用阻塞，普通包新窗口和 AX 事实确认。**
- 普通构建的 `Memlane.app` 没有内嵌运行配置，运行时也未提供 runtime root；使用隔离 home，未启用 automation。`production-runtime.json` 明确 `runtime_config_present=false`、`no_runtime_root_supplied=true`、`no_automation=true`。
- [普通包首页](../../../../../../build/acceptance/2026-11-09-001/testing/production-screenshots/01-production-home.png) 显示“等待本地记忆服务”“服务未连接”；经实际 AX 点击设置，[普通包设置](../../../../../../build/acceptance/2026-11-09-001/testing/production-screenshots/02-production-settings.png) 显示“本地引擎：待准备”“当前尚无可安装的组件版本”，没有准备/启动按钮。页面仍提供记录第一条记忆，但本机能力无法由该包继续准备。
- 影响：用户能打开窗口，却无法独立从该普通包进入首次保存。等待文案不能解释何时会继续或用户如何恢复，不能将其算作可恢复的正常准备中。
- **关闭条件：** 按正式构建入口产出的普通包，在无额外运行配置、空业务根及无组件运行目录下，提供有效组件源与可执行的准备入口；实际完成下载、验证、启动，再衔接 UX-01–03。离线或源失败时给出具体原因和有效重试入口。

## 状态与转换核对表

“代码符合”只表示静态走查通过；“未验证”均不计为本轮实机通过。

| 编号 | 入口、操作与状态转换 | 预期反馈与下一步 | 固定源码证据 | 本轮判定 |
| --- | --- | --- | --- | --- |
| UX-04 | 首启检测 → 结果 → 确认准备 → 准备/验证 → 就绪 | 默认折叠详情；准备前确认；就绪来自实际服务回执 | `first_launch_presentation.go:384–411`；`first_launch.go:311–359` | 本轮受控组件源通过，图 01–04 与 ready/空库 IPC；普通包被 UX-20 阻塞。未捕获启动检测动效全程和下载字节递增帧 |
| UX-05 | 准备中取消 / 失败 / 上次中断 → 重新检查 | 取消等待停止回执；失败保留具体原因；不显示可用 | `first_launch.go:316–329`；`first_launch_presentation.go:379–411` | 代码符合；运行失败/取消未验证 |
| UX-06 | Agent 已发现 → 配置已安装/待重载 → 实际验证 → 可用；服务不可用 → 异常 | 已发现不等于可用，下一步要求重载验证；失败保留原因 | `agents.go:238–270`、`:529`、`:653–672` | 01/07/19 证明发现状态未伪装可用；无连接回执，后续转换未验证；未找到状态歧义见 UX-18 |
| UX-07 | 记忆详情 → 编辑 → 返回/切页 → 再次编辑 | 栏内操作，保留同条草稿；未保存说明限定当前窗口 | `content.go:33–86`、`:123–148`；`app.go:687–700` | 本轮返回保留草稿通过；图 11 及 `edit-search-current.draft_back_retained=true`，返回时 IPC 仍为 v1；跨页/退出草稿未复验 |
| UX-08 | 点击保存 → 正在保存 → 已保存 → 等待/正在索引 → 可检索；保存失败/版本冲突 | 保存时禁用提交；失败保留草稿；冲突显示最新内容与继续编辑入口；索引失败保留正文 | `app.go:695–705`；`mutations.go:68–117`；`content.go:189–202`、`:249–252` | v1 新增、v2 编辑和 Rust 当前正文检索通过，图 08/09/11/12；保存失败、冲突、索引等待过程未验证 |
| UX-09 | 历史 → 选择旧版本 → 栏内确认 → 新当前版本 | 确认前不改动；显示旧正文与后果；返回历史；恢复新增版本 | `app.go:892–955`；`mutations.go:60–66` | 本轮图 13–15、确认前 IPC v2、确认后 v3、历史 [3,2,1] 通过；取消恢复未单独执行 |
| UX-10 | 发起整理 → 等待建议 → 查看来源/变化 → 确认或拒绝 | 确认前知识不变；合并后新建知识并归档来源；拒绝保留结果；加载与错误禁用确认 | `merge.go:348–420`；`organization.go:689–714` | 代码符合；本轮建议及副作用未验证 |
| UX-11 | 项目记忆 → 实体/关系图 → 返回正文；切项目再返回 | 项目/记忆/实体上下文保持，稳定身份关联；不补造实体 | `browsing.go:134–155`、`:177–212` | 图 16/17 三视图入口与真实空材料通过；有材料的上下文、跨项目恢复未验证；空态文案见 UX-19 |
| UX-12 | 来源 → 完整阅读器 → 查找/定位/切换分段 → 返回；来源变化/不可用 | 知识与原始记录分开；可区分完整会话和 SDK 分段；不可用给原因与重试 | `source_reader.go:197–249`、`:254–278` | 图 18 只有 raw=0 的空态；完整来源与恢复分支均未验证 |
| UX-13 | 搜索 → 加载 / 零命中 / 服务错误 | 搜索失败不伪装成零结果；索引未完成说明明确 | `app.go:524–526`；`mutations.go:207` | 图 12 当前正文命中、图 15 恢复后原 v2 token 零命中通过；服务失败状态未验证 |
| UX-14 | 关闭窗口 / 退出 App / 停止服务并退出 | 关闭隐藏窗口；默认退出继续服务；明确停止失败时保留 App 并允许重试 | `cmd/memlane-app/main.go:66–92`、`:387–403`；`app.go:1031` | 本轮正常关窗、默认退出、关闭保留选项退出、明确停止/启动/停止服务并退出通过，图 20–25 及生命周期 IPC；停止失败与意外崩溃恢复未验证 |

## 关键交互说明与可延后项

- **UX-15：首任务页面技术字段影响理解（可延后优化，但不能替代 UX-03 的完成反馈）。** `cognee.go:239–262` 将服务地址、dataset UUID、凭据、会话 ID 与首次任务操作放在同一页。源码证明字段存在；是否在本轮窗口形成明显认知负担仍需看图。可将普通任务入口与高级手动绑定收进不同层级，默认以 Agent、项目、实际记录与下一步表达。关闭条件是无需理解 dataset 和会话 ID 的新用户仍能完成常规接入与回看。
- **UX-16：返回保留草稿已有明确语义；主动丢弃草稿入口可延后讨论。** 当前按钮叫“返回”，执行保留草稿，不能在测试中把它当“取消并丢弃”；文案已说明仅当前窗口暂存（`app.go:687–700`）。本轮不新增退出后草稿持久化要求。
- **UX-17：键盘和焦点仍须实机补验。** 首启检测提供 Escape 稍后准备；原文搜索支持提交。仅存在快捷键或按钮不能证明 Tab 顺序、焦点可见性、打开阅读器后焦点落点、关闭后返回来源触发点正确。未获得实际键盘操作记录前保持未验证。该项可延后于阻塞主线修复，不阻碍先确认 UX-01/02。
- **UX-18：未找到安装与已断开共用“待连接”（本轮复现，可延后）。** 图 01 的 pi/TRAE CLI 为“未找到”，图 07/19 列表变为“待连接”；`agents.go:226–227` 对无路径返回待连接，与 `:241–242` 的已断开相同。用户无法在列表区分“先安装/指定路径”和“直接连接”。关闭条件：列表表达未找到安装，选中后给实际路径或重新检查入口，已断开继续保留独立状态。
- **UX-19：空态的对象与上下文不够明确（本轮复现，可延后）。** 图 17 选“关系图”后空态标题仍是“项目实体”；图 18 已切“原始记录”、中栏显示 0 条，右栏却继续展示上一条手动知识且没有“保留的知识详情”标记，中栏空态还写“保存的记忆会出现在这里”。保留上下文本身不是错误，但应明确右栏对象不属于当前空列表。关闭条件：关系图空态以关系说明原因/下一步；原始记录为空时右栏显示该类型空态，或明确标注保留的知识详情，用户不会误认已经采集到原始记录。

## 实际窗口与行为证据

证据根为 `build/acceptance/2026-11-09-001/testing/`。以下新图均已独立通过 `view_image` 打开。统一操作者提供操作，`runtime-results.json` 提供实际窗口状态与只读 IPC 事实；本角色根据原始证据独立判定。未使用历史截图作本轮通过依据。

| 证据 | 观察与行为结果 | 对应结论 |
| --- | --- | --- |
| [01 检查结果](../../../../../../build/acceptance/2026-11-09-001/testing/screenshots/01-first-launch-results.png)、[02 准备确认](../../../../../../build/acceptance/2026-11-09-001/testing/screenshots/02-preparation-confirmation.png)、[03 准备中](../../../../../../build/acceptance/2026-11-09-001/testing/screenshots/03-preparing.png) | 检查与安装分开；Codex 已安装未验证；缺组件，预计下载 289.45 MB；准备前确认，当前步骤与取消入口可见 | UX-04 的状态可理解，失败/取消未实际触发 |
| [04 本机环境就绪](../../../../../../build/acceptance/2026-11-09-001/testing/screenshots/04-preparation-terminal.png)、[05 点击后进入项目](../../../../../../build/acceptance/2026-11-09-001/testing/screenshots/05-start-use-project.png)、[07 无项目 Agent](../../../../../../build/acceptance/2026-11-09-001/testing/screenshots/07-agent-without-project.png) | 实际 ready，业务空库；点击首启主按钮到项目输入；手动打开 Agent 后无项目不能连接 | UX-01/02 不通过；正常准备不冒充 Agent 接入成功 |
| [06 空首页](../../../../../../build/acceptance/2026-11-09-001/testing/screenshots/06-home-empty.png)、[10 保存后首页](../../../../../../build/acceptance/2026-11-09-001/testing/screenshots/10-home-with-memory.png) | 宽幅首页，空库为 0，手动保存后实际新增 1，最近知识与真实标题出现；没有示例活动 | 首页宽幅和真实空/有数据反馈通过；热力图全部日期/重放边界不在此结论内 |
| [08 新增](../../../../../../build/acceptance/2026-11-09-001/testing/screenshots/08-new-memory-editor.png)、[09 保存](../../../../../../build/acceptance/2026-11-09-001/testing/screenshots/09-memory-created.png)、[11 编辑](../../../../../../build/acceptance/2026-11-09-001/testing/screenshots/11-memory-edit.png)、[12 当前搜索](../../../../../../build/acceptance/2026-11-09-001/testing/screenshots/12-search-current.png) | 右栏编辑，中间保留选中；“当前窗口暂存”明确。返回后未保存，重新编辑草稿保留。v2 实际搜索返回新全文，详情另列“已保存”和“可检索” | UX-07/08 正常路径通过 |
| [13 历史](../../../../../../build/acceptance/2026-11-09-001/testing/screenshots/13-memory-history.png)、[14 恢复确认](../../../../../../build/acceptance/2026-11-09-001/testing/screenshots/14-restore-confirmation.png)、[15 恢复结果](../../../../../../build/acceptance/2026-11-09-001/testing/screenshots/15-memory-restored.png) | 历史正文和差异可读；恢复在栏内明确预览。确认前 IPC v2，确认后 v3，旧正文保留为历史。原 v2 搜索 token 此时零命中，右栏保留操作后的 v3 | UX-09 正常恢复通过；UX-13 正常命中/零命中通过 |
| [16 实体空态](../../../../../../build/acceptance/2026-11-09-001/testing/screenshots/16-project-entities-empty.png)、[17 关系空态](../../../../../../build/acceptance/2026-11-09-001/testing/screenshots/17-project-relations-empty.png)、[18 原始记录空态](../../../../../../build/acceptance/2026-11-09-001/testing/screenshots/18-project-raw-empty.png) | 未接入官方材料，0 实体/关系、raw=0；三标签仍可见。没有补造实体或来源。关系标题与原始记录右栏上下文有歧义 | 仅空态通过；UX-19 可延后 |
| [20 设置](../../../../../../build/acceptance/2026-11-09-001/testing/screenshots/20-settings-ready.png)、[21 关窗后重开](../../../../../../build/acceptance/2026-11-09-001/testing/screenshots/21-reopened-window.png)、[22 App 重启历史](../../../../../../build/acceptance/2026-11-09-001/testing/screenshots/22-restarted-history.png)、[23 主动停服](../../../../../../build/acceptance/2026-11-09-001/testing/screenshots/23-service-stopped.png)、[24 取消保留服务](../../../../../../build/acceptance/2026-11-09-001/testing/screenshots/24-quit-option-disabled.png)、[25 服务重启](../../../../../../build/acceptance/2026-11-09-001/testing/screenshots/25-restarted-service.png) | 退出选项默认勾选。关窗 `visible=false`、health ready，同 PID 菜单重开；默认退出后原 worker 仍 ready，新 PID App 可读 v3 和历史；主动停服 `stopped=true`、socket 消失、旧 worker 退出；显式启动产生新 worker；取消勾选后普通退出停服，选项跨 App 重启保留；最后“停止服务并退出”清理服务 | UX-14 正常生命周期通过；图 23 可明确看到“记忆服务已主动停止”及“启动服务”，不会误认仍可用 |

对照设计图：本轮首页保留侧栏加宽幅总览；新增、编辑、历史和恢复都在右栏，列表与选中仍可见，没有普通操作弹框。设计要求的这部分交互承载通过；完整来源阅读器未在本轮打开，不推断其视觉通过。

生命周期与版本事实集中在 [原始运行记录](../../../../../../build/acceptance/2026-11-09-001/testing/runtime-results.json) 的 `close-hides-service-remains`、`default-quit-keeps-service`、`actual-app-restart-persistence`、`explicit-stop-durable`、`explicit-service-restart`、`unchecked-quit-stops-service`、`stop-and-quit-cleanup`。例如 App 从 PID 72536 重启为 86865，默认退出期间 worker 74336 仍 ready；显式停止后旧 worker 不存在，显式启动后 worker 为 87039。只据此关闭正常生命周期，不外推停服失败、睡眠唤醒或崩溃重试。

代码回归由统一测试角色在固定源码执行：`sh scripts/go.sh test ./...`、`sh scripts/go.sh vet ./...`、`CGO_ENABLED=1 sh scripts/go.sh test -race ./internal/desktop ./internal/daemon` 全部 exit 0；对应 `go-test.log`、`go-vet.log`、`go-race.log`。已读取 test/race 结果日志。它们支持代码分支回归，不能替代上述未执行的实机分支。

## 未验证与验收关闭

本轮组件准备使用真实本地归档和隔离业务根，只证明受控源下本机准备，不能当无配置正式分发冷启动。无项目连接、新首任务连续路径、真实模型失败与恢复、编辑冲突、建议确认/拒绝副作用、有材料的三视图与完整来源均未验证；代码阅读不关闭这些验证。历史官方路径记录保留为历史，不推断当前固定产物重新通过。新流程仍被 UX-01/02 阻塞，独立维护通过后整体结论仍为不通过。

验收关闭需要同时满足：UX-20 的普通包准备路径与 UX-01–03 的新旅程完成条件；保存/历史/审核保护未回退；本轮固定产物的真实窗口和实际读写结果可对应。旧包通过、原型通过、仅编译成功、验证 marker 或真实任务退出码 0 都不能单独替代。
