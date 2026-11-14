# Memlane 产品经理独立整体验收

验收日期：2026-11-09。角色依据：冻结目录中的 `docs/agent-prompts/product-manager.md:1–21`。

**独立结论：当前冻结版本不能作为符合最新产品规则的完整首版放行，也不能正式分发。已有本机受控切片足以支持继续体验和开发：官方 Codex 首次任务、真实采集／提炼、App 浏览与 Rust 检索有连续历史证据；Memlane 受控桥的跨 Agent 编辑／恢复闭环也有历史证据。两组证据不能合并成最新版所有入口和所有 Agent 都通过。**

没有发现足以判定 P0 的证据。已证实的当前产品缺口是 Agent 优先入口与项目可跳过尚未落地；官方召回与 App 维护后的当前版本一致性属于未闭合的产品验收门槛，尚未实证为错误召回；同身份 helper 替换是已有实际失败；正式渠道与信任链仍未通过。

## 1. 固定范围、方法与证据等级

- 冻结源码目录：`/Users/bytedance/work/AI/memlane/build/acceptance/2026-11-09-001/source`，下文没有 `build/` 前缀的路径和行号均指向此目录，不指向继续变化的原工作区。
- 基线：`HEAD 99246f43e1ae7ab7c1adf3ac9e5a859dd80cfcb1` **加冻结时的 staged／unstaged／untracked 修改**，不是单独该提交。`baseline.json` 捕获时间为 `2026-11-08T17:06:28.047855+00:00`。
- 已逐文件重新计算基线列出的 **528 个文件** SHA-256，全部匹配。证据：[product/evidence.json](/Users/bytedance/work/AI/memlane/build/acceptance/2026-11-09-001/product/evidence.json:1)。这只验证读取版本固定，不等于运行验收。
- 使用需求对照、关键入口／状态／范围契约源码追踪、历史报告字段与散列核对、设计图及历史系统窗口图对照。未启动 App、测试、真实模型或 Agent，未读取个人记忆库或 Agent 凭据，未阅读其他角色报告，未改业务代码或提交 Git。
- 已实际打开 `docs/images/home.jpg`、`docs/images/memory-detail.jpg`。另打开历史 `selected-final/continuous/screenshots/03-native-ready-system.png` 和 `q4-hidden/accepted-2/screenshots/02-memory-tabs-system.png`：前者确实为“选择项目并开始记录”，后者无代码图谱导航并保留记忆／实体／关系图。**本轮冻结包的实时视觉验收未执行**；历史图不能代替它。

证据等级：**S** 为冻结源码直接证据；**H** 为已读取的历史实际结果／图；**D** 为历史验证文档的声明；**U** 为本轮未验证／尚无所需闭环。H/D 均不标为本轮重新通过，协议、SQL 或故障夹具不升级为正常模型结果。

实际阅读的主要材料：

| 材料 | 实际阅读范围与用途 |
| --- | --- |
| `AGENTS.md`、角色 prompt、`docs/product.md`、`docs/design.md`、`docs/product-questions.md`、`docs/core-business-flows.md` | 目标用户、产品范围、最新 Q1–Q4、主流程和验收边界；全文阅读 |
| `docs/p3-delivery-plan.md`、`docs/implementation-plan.md`、`docs/technical-plan.md` | 交付矩阵全文，实施清单分层状态与未勾选项，技术方案的项目／Agent／整理／生命周期／分发条款及各阶段当前落点 |
| `docs/product-memory-flow.md`、`docs/agent-integrations.md`、`docs/onboarding-memory-contract.md` | 日常策略未决、四 Agent 能力边界、官方与 Memlane 数据路径 |
| `docs/onboarding-memory-validation.md:5–98`、`docs/agent-integrations-validation.md:23–98` | 最新连续历史验收、严格空缓存、真实任务／来源／召回、失败历史、体积与隔离边界 |
| `docs/p1-validation.md`、`docs/p2-validation.md:45–105`、P3 C2/C3/C4/E/D1/D2/U/I 验证文档 | 读取对应真实结果表、主流程、明确故障与剩余范围；不把旧章节“待交付”覆盖后续结果 |
| `docs/p3-agent-validation.md`、`docs/p3-ide-validation.md`、`docs/p3-pi-rpc-validation.md` | CLI、IDE、普通桥、官方插件、后台执行器分别判断 |
| `docs/p4-helper-replacement-validation.md`、备份／恢复／磁盘验证文档 | helper 更新实际失败；备份、恢复和故障切片的已验／未验范围 |
| `internal/desktop/{app,first_launch,first_launch_presentation,agents,onboarding,cognee,cognee_agent,mutations,library}.go` | 首启下一步、连接门禁、状态文案、项目限制、真实任务入口、保存／恢复请求 |
| `internal/official/agent.go`、`internal/daemon/{cognee_sync,cognee_graph}.go`、`internal/store/cognee_import.go`、`cmd/memlane-app/main.go`、`mygo.json` | 已登记项目约束、同步身份／版本契约、确定性导入、更新器与签名配置 |

历史 build 均读取自原仓 `/Users/bytedance/work/AI/memlane/build`。其中 `selected-final/delivery-matrix.json` 的 `source_commit=64a8243bba977d1c92f35b3a590d47a285b17861`、`delivery_commit=99246f43e1ae7ab7c1adf3ac9e5a859dd80cfcb1`；本次验收另含 dirty 修改。连续报告的 `remaining_required` 当时仍列重开／清理，最终交付表另列其通过，不能仅见连续报告 state 就宣布整体验收结束。

## 2. 用户问题、期望结果与成功口径

目标用户是使用多个编码 Agent 的 macOS 程序员。已知用户痛点不是缺少功能数量，而是“第一步做什么”“发现 Agent 后是否就可以使用”“项目为什么要先创建”。证据为 `docs/product.md:7` 和 `docs/product-questions.md:9–12`。没有访谈样本量、使用时长、留存或节省时间基线，不编造这些指标。

本轮问题定义：**用户能否从准备好的本机环境，经自己选定的 Agent 产生第一份真实记忆，理解其来源与归属，并让后续 Agent 使用经过自己维护的当前知识。**

成功口径以可核对结果定义，不以按钮存在、任务退出 0 或模型自称记住定义：

| 口径 | 通过标准 | 当前判断 |
| --- | --- | --- |
| 首次入口 | 本机准备完成后首先选择 Agent，明确项目提示可跳过 | 不通过，PROD-01/02 |
| 首次价值 | 一次真实任务后能从 App 找到实际 raw／knowledge、出处与持久回执，再在新会话召回 | 已有 Codex 官方路径 H；最新入口未通过 |
| 当前知识 | 编辑／恢复／批准变更后，获准 Agent 读取和检索返回当前有效正文及版本 | Memlane 桥有 D；官方路径维护后的新会话为 U，PROD-03 |
| 确认边界 | AI 合并／覆盖／删除／跨项目移动确认前有效知识不变，批准时复核版本 | C2/C3/C4/E 有 D；当前未重跑 |
| 故障诚实性 | 保存与索引、发现与可用、未送达与已保存、失败与零结果分开 | S 与多阶段 D 支持；全量生命周期仍有缺口 |
| 可持续使用 | 重开保留知识与授权；主动停止不自启；更新后 helper 可用且数据保留 | 重开／停服有 D；更新 helper 有 H 失败，PROD-04 |
| 分发 | 普通用户在目标干净 macOS 下载、安装、准备、离线检索、更新可用 | U／缺条件，PROD-06 |

## 3. 产品范围 → 能力 → 实现／验收证据矩阵

| 产品范围 | 关键实现／契约 | 验收证据与判断 |
| --- | --- | --- |
| 轻量主包与真实本机准备 | `product.md:47–48,99`；`first_launch.go:337–353`；`first_launch_presentation.go:346–408` | H：selected-final 从空业务／空缓存真实准备及验证；ready 只称本机服务与 Rust 可用。公开来源／签名另验 |
| Agent 优先、项目可跳过、首任务引导 | `product.md:34,97`；`product-questions.md:16–38` | S：当前 ready 去项目页；连接和官方路径要求项目 UUID。完整新顺序缺失，PROD-01/02 |
| 四 Agent 发现／接入／真实可用 | `agents.go:225–270,343–381`；`agent-integrations.md:13–37` | S：发现、待适配、重载、验证、服务不可用分开；D：Codex/pi/traex 不同版本已有结果，TRAE IDE 真模型未通过，PROD-05 |
| 官方正常任务 → 采集／提炼 → Memlane | `cognee_agent.go:53–215,220–240`；`cognee_sync.go:121–150`；`cognee_import.go:95–105` | H：selected-final 实际 8 raw、2 knowledge，真实 session_learning 后确定性导入，两个当前 v1 Rust 检索；不代表日常自动同步 |
| 三视图与完整出处 | `app.go:159,718–745`；`onboarding-memory-contract.md:11–17,37–41` | H/D：真实实体13、关系14与具体材料，全文26,723 B、分页／复制／引用定位；Q4历史图保留三视图。停官方服务时实体读取失败仍需如实展示 |
| 手动新增、编辑、历史恢复 | `mutations.go:14–76` 显式 expected revision、恢复请求与业务 Apply；`product.md:127–131` | D：`p2-validation.md:45–49,77–81` 证明 App v2、恢复 v3、pi 当前读取；恢复产生新版本。官方召回并非同一路径，PROD-03 |
| 项目隔离、额外范围与全局 | `product.md:34–38`；`cognee_sync.go:123–149`；`agents.go:342–368` | D：P2 真实跨项目／worktree；E 真实 pi 从他项目读取全局并拒绝源项目越权；新“无项目先连接”授权契约尚缺 |
| 立即整理、自动摘要／标签、审核修改 | `product.md:29–33,133–137`；`technical-plan.md:163–169` | D：`p3-c2-validation.md:10–24`、`p3-c3-validation.md:13–23`、`p3-c4-validation.md:15–25`；确认前不改有效知识、过期拒绝、来源失效、取消／部分提交分别有结果 |
| 全局原文复制／AI 提炼／条件 | `product.md:37–38,139–141` | D：`p3-global-validation.md:11–20`，原项目保留、确认新建全局、条件编辑恢复、实际 pi 读取 v3；不是待实现功能 |
| 明确计划、单执行者与有限恢复 | `product.md:32,41,45–46` | D：`p3-schedule-validation.md:9–17` 和 `p3-recovery-validation.md:55–72` 真实自然到期、活动取消、helper SIGKILL 恢复；新计划／重试默认关闭。pi RPC 非首版唯一执行者前提 |
| 热力图／使用观察与排序 | `product.md:103–115`；`app.go:319–331`；`library.go:326–329` | D：P2 恢复与重放不新增活动；U 验证读取／返回／报告采用分开、采集前历史未知。没有证明代码实际采用或提高效率 |
| 可选文件初始化 | `product.md:42,125`；`onboarding.go:60–71` | D：`p3-onboarding-validation.md:5–11,15–27` 真实 pi 两条约定、冻结来源、导入与新增分开、取消零保存；与新首启项目跳过是不同能力 |
| 关闭窗口／退出／停止／故障 | `product.md:43–46,151–159` | D：P2停服错误和重启、D2有限恢复、F1备份／恢复／ENOSPC分层证据；F1 helper替换 H 失败，后台权限／移动全部组合 U |
| 代码图谱隐藏、记忆实体关系保留 | `product.md:39,93,145`；`app.go:192–206` | S+H：导航数组不含代码图谱；q4-hidden 两个布尔值为 true，8 raw／2 knowledge仍读。保留内部代码路由不等于可见入口；不要求删除已有索引 |
| 自动更新与正式分发 | `cmd/memlane-app/main.go:100–168`；`mygo.json:9–12` | S：有更新协调逻辑，但未启用时明确提示；签名身份“-”。D/H：受控旧更新不能关闭 helper 实际失败；F2/F3未完成 |

## 4. 核心旅程闭环判断

| 旅程 | Given / When / Then 验收条件 | 判断 |
| --- | --- | --- |
| F00 最新首次使用 | Given 空业务且准备完成；When 选择 Agent 并跳过项目、授权首任务；Then 连接本身无空项目，首份有效数据受信创建／复用项目，App 展示实际记录和出处 | **未实现关键前半段**。现有连续包是项目优先，不能替代此验收 |
| F01 已登记项目接入 | Given 已登记真实仓库；When 明确连接并真实验证；Then 只获准范围可用、重载／失败状态准确 | Codex/pi/traex 各历史支持版本有证据；TRAE IDE及当前版本组合不齐 |
| F02 官方首次积累 | Given 获准项目／官方配置；When 执行正常任务并官方同步；Then raw与knowledge分开、Rust命中当前导入版本、新会话官方召回 | selected-final 历史连续通过；新项目可跳过路径 U；不宣称四 Agent 官方路径均通过 |
| F03 维护再复用 | Given 有效知识v1；When App编辑v2或恢复得v3；Then 下一读取端看到当前正文、版本、范围和出处 | Memlane桥有P2真实v2/v3证据；官方 Agent 新会话未证明，PROD-03 |
| F04 整理与复查 | Given 明确材料／执行者；When 生成并批准或拒绝建议；Then 确认前不变、过期不覆盖、应用后可检索、取消保留已提交事实 | C2/C3/C4/E/D有分层历史证据；冻结最新版未重跑，不降格为原型 |
| F05 全局复用 | Given 项目经验；When 原文复制或批准通用提炼；Then 新全局条目可由其他获准项目读取，原项目保留 | E历史真实App/traex/Rust/pi已闭合；官方独立Cognee全局召回未纳入此证明 |

整体缺的是按最新起步顺序、同一固定交付包，把 F00 与维护后复用连接起来的验收。既有“官方采集→v1召回”和“普通MCP→App编辑→pi v2/v3读取”分别有效，但读取通道不同，不能拼接成同一旅程。

## 5. 阻塞项与关闭条件

严重度按用户影响：P0 为已证实重大数据损失／广泛越权等；P1 为关键承诺路径不可完成或放行门槛缺失；P2 为范围受限或可规避的体验／交付一致性问题。U 类型的 P1 表示必须补齐的放行门槛，不表示已证实运行缺陷。

### PROD-01 · P1 · 已证实需求缺口：准备完成后仍先创建项目

- 用户影响：用户已明确“先选择 Agent”，仍被带到不理解的项目登记页；首任务入口没有按选定 Agent 串起来。不是缺少首次任务执行能力，现有项目内官方任务能力可复用。
- 需求：`docs/product-questions.md:16–28`、`docs/product.md:97`。
- S：[first_launch_presentation.go:397](/Users/bytedance/work/AI/memlane/build/acceptance/2026-11-09-001/source/internal/desktop/first_launch_presentation.go:397) 显示“选择项目并开始记录”，407行导航项目；351–360行仍以项目开头。`cognee_agent.go:227–240` 已有项目内官方任务能力。
- H：已打开 `build/onboarding-memory-validation/selected-final/continuous/screenshots/03-native-ready-system.png`，与代码一致。此为旧包截图，不是冻结包复现。
- 复验步骤：新空业务根完成真实准备 → 点击ready主按钮 → 检查首个业务选择和后续接入／任务引导。当前静态分支必达项目页。
- 建议／关闭条件：接通 Agent 选择→项目可选→必要接入与真实验证→App首任务→实际记忆与出处；在同一固定完整包连续证明，准备完成不冒充 Agent 可用。阻塞最新版首启放行。

### PROD-02 · P1 · 已证实需求缺口：项目跳过没有对应受信接入能力

- 用户影响：没有先登记项目就不能继续连接，用户确认的“有数据后再建立”不可用；仅加跳过按钮不能解决。
- 需求：`docs/product-questions.md:32–38` 明确连接不创建空项目、来源可靠时才自动建立／复用、歧义提示归属且不默认全局。
- S：[agents.go:374](/Users/bytedance/work/AI/memlane/build/acceptance/2026-11-09-001/source/internal/desktop/agents.go:374) 以 `s.Project == ""` 禁用连接；`cognee_agent.go:233` 同样门禁；[official/agent.go:41](/Users/bytedance/work/AI/memlane/build/acceptance/2026-11-09-001/source/internal/official/agent.go:41) 要求 approved project UUID；`daemon/cognee_sync.go:123–125` 拒绝无项目UUID同步。
- 复验步骤：无任何项目进入 Agent 页，选择已支持安装，保留“选择本地项目”，检查连接；官方准备同样检查。当前源码将禁止继续。
- 建议／关闭条件：先明确无项目阶段的接入身份、授权范围与待归属状态；首份有效数据接收与项目创建／复用／授权绑定须幂等。至少证明无数据不建项目、重复送达仅一个项目、同仓复用、跨仓不误归、来源未知待归属、失败重试和撤权拒绝。阻塞项目可跳过承诺。

### PROD-03 · P1 放行门槛 · 未验证：官方召回与用户维护后的当前知识关系未闭合

- 用户影响／必须确认的结果：用户修正或恢复知识后，下一次 Agent 使用不能把旧约定当作当前有效知识。此结论来自首版契约，而不是要求两库实现相同功能。
- 契约依据：`docs/product.md:129–131` 要求编辑保存与新当前版本；`docs/core-business-flows.md:158–173,243–245` 明确后续Agent读取新版本、列表／详情／Agent检索围绕当前有效版本；`docs/product-memory-flow.md:15–16,49`仍确认此规则。
- 已证实边界：`docs/onboarding-memory-contract.md:17` 明写官方recall查独立Cognee、App搜索查Memlane/Rust、App编辑不自动写回；[cognee.go:210](/Users/bytedance/work/AI/memlane/build/acceptance/2026-11-09-001/source/internal/desktop/cognee.go:210) 在UI说明两个读取通道；`mutations.go:34,63,76` 为Memlane业务Apply；`store/cognee_import.go:104–105` 是外部材料确定性新增。
- 证据不足之处：selected-final 只核对两条导入v1和全新官方recall；P2编辑v2／恢复v3后的真实读取是pi的Memlane桥。**本轮没有执行“官方导入→App编辑／恢复→新官方召回”，因此未证实实际错误召回，也不能仅因没有回写就判定代码bug。**
- 建议／关闭条件：产品先明确官方历史材料召回与Memlane当前有效知识的关系，再以真实合成流程证明编辑、恢复及已确认删除／合并后，下次正式入口给用户的答案遵循选定契约，历史材料有正确状态和出处。可以通过读取路由、当前版本校验或其他方案满足，不指定自动双向同步。已向管理者提出补验请求，本轮不调用模型。

### PROD-04 · P1 · 历史已证实未关闭：更新 helper 后服务无法启动

- 用户影响：更新包含helper变化的App后，窗口能打开但记忆服务不可用；影响后续保存／读取与“自动更新保留可用”承诺。
- 需求：`docs/product.md:44–48`；`docs/p3-delivery-plan.md:30,36–37`。
- H/D：`docs/p4-helper-replacement-validation.md:7–9,15–23,29–35`；已读取 `build/p4-delivery/helper-replacement-producer-signature.json`：`status=failed`、`actual_app=true`、`actual_helper=false`，错误为 `actual LaunchAgent BundleProgram EX_CONFIG (78); helper never served ready IPC`。系统诊断记录 `Launch Constraint Violation`。
- 可复现入口：`scripts/verify-p4-helper-replacement.py`，仅对隔离合成根；本轮未运行。新独立App身份的启动成功不能关闭本项。
- 建议／关闭条件：在真实旧身份／同路径helper更换及受支持移动路径下，证明更新、服务ready、当前检索、原数据与主动停止状态保留，并核对进程拥有权。没有证据证明正式签名一定修复，也不把ad-hoc失败外推为正式签名必失败。阻塞完整可更新版本，不否定固定旧包受控体验。

### PROD-05 · P2 · 支持范围不完整：四种 Agent 的版本与通道没有统一放行矩阵

- 用户影响：只使用TRAE IDE的用户尚不能得到已验收的日常闭环；看到“四款目标支持”容易误解为任意当前版本可用。现有能力门禁已保护用户，故不按广泛错误可用状态定级。
- 需求与证据：`product.md:40`；`agent-integrations.md:58–69` 记录发现与已验格式区别；`p3-ide-validation.md:3,9,25` 明确真实IDE模型未验；`p3-agent-validation.md:31–35` 的旧Codex新增工具受旧版本／模型阻塞。
- 最新官方成功不能覆盖旧工具清单：`onboarding-memory-validation.md:21` 是Codex `0.167.0-alpha.3.2` 官方插件任务；`official/agent.go:137` 固定该版本；它不能证明Memlane新增采用／初始化工具的模型验收。`agents.go:232–270`当前保持发现／待适配／真实验证分界。
- 关闭条件：按“Agent＋版本／渠道＋接入通道＋读／写／检索／采用／初始化／后台”列真支持表；对公开承诺的组合取得真实App和Agent回执，未验组合继续清楚标记。四Agent完整首版承诺尚不放行；限定已验组合的本机体验可继续。

### PROD-06 · P1 分发门槛 · 未完成：正式下载、更新与系统信任链无通过证据

- 用户影响：普通目标用户尚不能凭受控测试源和ad-hoc包完成可支持的安装／升级流程。
- S：`mygo.json:9–12` 的签名身份为“-”，无正式updater配置；`cmd/memlane-app/main.go:147–149` 未启用更新器时显示“此版本尚未启用自动更新”。
- D：`docs/p3-delivery-plan.md:36–37,50–54`、`onboarding-memory-validation.md:23–25`。正式Developer ID／公证、Gatekeeper、公开组件与更新源、干净macOS15+ arm64未通过。历史零有效签名身份只作当时条件，不宣称本轮查询了当前钥匙串。
- 关闭条件：选定正式产物和可访问渠道后，证明主包及下载组件签名／校验／公证、干净目标系统首次准备、已准备后离线主要功能、真实升级及失败恢复，并填实更新下载字节。关闭前不得把本机阶段“正式实现”称为正式分发。

### PROD-07 · P2 · 可延后文档修正，但新E2E设计前必须关闭：主流程稿仍以旧状态描述当前范围

- 用户影响：据此生成流程或用例会继续把创建项目设为前提，并漏掉已实现的全局／完整整理，导致验收遗漏与错误入口延续。
- S：`core-business-flows.md:14–16,80–83,92,100`仍项目优先；58–59、194–209、226仍把完整整理／全局标为未完成；其3行声明作为后续E2E依据。相对地 `product.md:34,97` 和 `p3-delivery-plan.md:19–25`已经更新。`product-memory-flow.md:5`有旧状态提醒，但不能使直接消费主流程稿的用例正确。
- 关闭条件：保留历史标识，重写当前F00及能力状态，与Q1–Q4和最新交付矩阵一致；全量交付矩阵中隐藏代码图谱、piRPC候选与必需用户承诺也应分开。不需要为文档修正重跑模型。

## 6. 可延后与未验证项

| 项目 | 判断与原因 | 恢复条件 |
| --- | --- | --- |
| 代码图谱可见入口、完整代码图谱后续工程 | 明确首版隐藏，既有代码／索引／任务保留；不能继续把可见代码图谱当最新首版放行门槛 | 用户以后重新确认可见范围；本次仅保证隐藏不改既有任务 |
| pi RPC第二后台执行器 | `product.md:41`只要求统一一个具备能力的执行者；traex有既有证据，piRPC见 `p3-pi-rpc-validation.md:3,9–14`仍未支持。可延后，不因矩阵有一行就替用户扩大首版 | 承诺提供pi后台执行时再验正常质量、服务／计划／App／Rust与活动取消 |
| 日常默认自动保存／采集策略 | `product-memory-flow.md:40,53–59`未选择。可以继续显式单次任务和明确采集范围体验；不得默认为自动开启日常hook或历史采集 | 用户选定触发时机、范围、暂停／撤销与反馈；若要以“日常自动记忆产品”交付则成为必要决策 |
| 词云、Top-K、远程主机、多系统、旁白增强 | 不属于当前必须范围，见主流程252行、交付矩阵54行及Roadmap引用 | 独立产品授权和支持矩阵 |
| 官方v1召回后的维护一致性 | 不是可默认忽略项；属于PROD-03 U门槛 | 先确认关系，再完成真实新会话验收 |
| 最新冻结包全量运行与视觉 | 本角色按授权没有运行；旧完整包与dirty改动并非同一版本证据 | 管理者整合本轮限定运行证据；未执行内容保持未验证 |
| 规模、长时间稳定性和产品效果 | 当前合成样本与小规模质量结果不能证明真实长期库、所有代码语料或接受率；未指定性能／体积硬上限 | 明确目标工作量及口径后测量，不伪造目标阈值 |

## 7. 本机与正式分发两层放行

| 层级 | 本角色判断 | 必要条件 |
| --- | --- | --- |
| 既有固定包、已验组合、隔离合成材料的本机体验 | **可继续体验已有能力**。这是历史已验切片的使用边界，不是本轮整体放行 | 使用其原始根／身份与明确启停；不启用未经选择的日常采集；认识到官方与Memlane读取路径不同 |
| 最新规则下完整本机首版 | **不放行** | PROD-01/02落地；PROD-03关系和实测门槛关闭；受支持Agent矩阵明确；PROD-04和适用生命周期必需项闭合；冻结最终包连续验收完成 |
| 面向普通用户正式分发 | **不放行** | 上层本机条件＋PROD-06＋干净目标设备与真实渠道／更新验证；有限本机ad-hoc结果不能替代 |

最新历史体积来自 `build/onboarding-memory-validation/selected-final/sizes-final.json`，本轮只读取并核对散列：

| 口径 | 字节 | 适用边界 |
| --- | ---: | --- |
| 主DMG | 18,166,963 | selected-final历史完整包 |
| 主App普通文件 | 40,583,159 | 包内普通文件逻辑大小 |
| 首次额外组件归档下载 | 708,140,713 | Rust/E5及官方Cognee/BGE/plugin受控源计数；不含HTTP/TLS或模型请求开销 |
| App＋client普通文件 | 1,583,373,218 | 包含该次组件、模型和合成业务／官方数据，不含开发工具链或其他验收根 |
| OS分配文件量 | 1,653,649,408 | 不等于APFS可释放空间 |
| 最新正式主App更新下载量 | 未验证 | 不记为0；不能混用早期P0更新字节 |

产品尚未指定体积上限，因此这些数字是成本事实而不是超标缺陷。首轮准备约708MB与主包约18MB应同时告知，不能只宣传主包大小。

本轮建议先完成Q1–Q3的最小用户旅程和读取关系对齐，再进行对应的同包连续验收；已有受控整理、版本、范围与来源契约继续复用。安装升级／分发工作仅据本报告列出放行条件，不由本次产品审计自动恢复暂停工程Goal。
