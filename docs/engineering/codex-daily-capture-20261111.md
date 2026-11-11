# Codex 日常采集实施账本

当前状态先读[交接摘要](codex-capture-handoff.md)。下文按时间保留阶段结论，旧的“未启用／未修改日常配置”等描述不代表最新状态。

目标：首次电脑级连接并授权后，新增普通对话自动作为原始记录保存；未知仓库先保留、后询问项目归属。不是明确 memory_save、历史批量导入或自动提炼。

## 当前核对

- 当前 CLI 为 0.167.1（实际 `codex --version` 和安装 package.json）。现有电脑级安装在 `internal/agents/config.go`，Native 入口在 `internal/desktop/agents.go`；不能将电脑级 MCP 配置和协议验证当采集授权或采集通过。
- `internal/official/transcript.go` 只读 App 自有、指定项目、指定旧版本的 exec 会话；`internal/agents/codex_history.go` 是已授权项目历史读取，不适合直接扫描日常历史。
- `internal/agentbridge/outbox.go` 已有私有持久暂存、锁与不可变提交；既有补送是明确人工确认，不可偷偷改成全局自动重放。`delivery_assignment.go` 有独立用户归属操作和防重，Native 已有待归属承载，可以评估复用。
- 当前进程核对：76077 memlaned；47698 为 build/mvp GUI。未停止、替换或重新注册任何后台。130 GiB 可用；未构建、下载或清理运行依赖。
- 运行后台缺失完整所属包问题和 helper 更新问题保持停止，见既有复盘。新增服务协议是否能由当前进程承载尚未证明；不能用新 GUI 的代码假装旧服务已更新。

## 官方接口及限制

来源：[OpenAI Hooks](https://learn.chatgpt.com/docs/hooks)，本轮已实际打开正文。

- 用户级 hooks.json 与 config.toml inline hooks 均支持；不必逐仓库安装。不同配置层会合并，须保留其他 hook。
- UserPromptSubmit 有 prompt/turn_id；Stop 有 last_assistant_message/turn_id；公共输入包含 session_id、cwd、transcript_path。
- transcript 格式明确不是稳定接口。仅 Stop 的最后回复不足以宣称收齐所有助手中间发言、附件、取消中的内容。
- 新 hook 要求 Codex 信任，定义变更会重新审核。CLI help 有单次 bypass 选项，但这不是日常接入方案，不替用户修改信任数据库或降低保护。
- App 必须区分：已安装、待 Codex 信任/重载、事件已实际到达、暂存、服务确认保存。实际版本行为仍待受控验证；文档能力不等于本机已验收。

## 候选最小方案（尚非交付）

1. 首次连接页单独持久化 Codex 新增对话采集授权及开始边界，不授予检索其他项目知识的权限。
2. 用户级 hook 只接当前事件关联会话；先在私有存储 fsync，再尝试送达。以连接代次/会话/turn/事件种类为身份，重放不新建记录，内容冲突显式报错。
3. 原文与来源独立持久化；未知仓库不创建有效项目或归入通用知识。路径规范化与 git common-dir 沿用领域识别规则；未知保持未知。
4. App 复用待归属阅读/确认链路；候选交互是侧栏「待归属原始记录」、仓库提示「添加项目／暂缓」。已向用户提出，尚待确认，不冒充认可稿。
5. 已确认项目后关联先前材料；对相同授权后新增轮次送达已有项目。后台隔离 home 与显式处理标记均排除采集。
6. 完整对话解析必须先取得 0.167.1 受控材料格式证据；未知格式不截断后标为完整。

## 连续验收用例（先于实现）

| 编号 | 操作与预期 | 必要证据 |
| --- | --- | --- |
| C01 | App 实际日常电脑级安装并授权，保留其他配置，未加载不显示可用 | 配置增量摘要、备份存在、Native 状态及实际 hook 到达；不输出密钥 |
| C02 | 未登记测试仓库两轮正常 CLI 对话，无 memory_save | 用户及助手实际正文独立持久读取、会话/turn/时间/cwd/仓库身份 |
| C03 | 发现仓库，暂缓后原文仍可阅读；后确认建/复用项目 | 实际窗口、前后材料身份和唯一项目证据 |
| C04 | 第三轮自动归入同项目，重复事件/路径别名无重复 | 持久化唯一性与来源核对 |
| C05 | Native 选材料→范围/Token→既有 pi 整理→保存→来源 | 实际任务/保存回执及窗口，不重复收费重跑 |
| C06 | 仅重开 GUI/CLI，旧材料保留，新对话继续采集 | 重开前后独立快照；不重启受保护 helper |
| C07 | 隔离目的端不可用后恢复，暂存自动补送且无重复 | 隔离可恢复故障、暂存与确认回执；不影响真实后台 |
| C08 | 未授权/撤权/旧代次/处理会话/旧历史不得采集 | 定向合成检查，失败不扩大真实数据范围 |

本轮尚无真实验收轮次；解析器定向检查不替代 C01–C08。既有停止问题计数保留，不因本账本改名重置。

## 受控接口探索及 Native 观察

- 0.167.1 电脑级工作目录边界：复用 `TestProtocolRealCodexGrantedWorkspaceIsolation`，实际已安装 CLI、隔离 daemon/配置、两个临时仓库；通过（3.17 秒）。覆盖私有当前目录、伪造 meta 不生效、越权拒绝、授权代次改变后重载及项目验证不得互用。没有启动模型 turn。回执在 `build/codex-daily-capture/codex-scope-isolation-*.json`。源码白名单已增加此版本；不等于正在运行的旧 GUI 已更新。
- hooks 探针 `scripts/probe-codex-capture.py`：隔离 home/配置与本地固定 SSE 回应，不读取用户历史，不改真实配置，不调用真实模型。只在该受控命令使用自有脚本的单次 hook trust bypass；生产安装绝不沿用该参数或替用户写信任数据库。
- probe-001：CLI 成功产生三类事件；取证脚本将 `/var` 与 `/private/var` 误判为越界，未完成 transcript 取证。保留 stdout/stderr/events；已修正探针根目录规范化。此失败不是模型失败或采集机制已失败的结论。
- probe-002：成功退出，收到 SessionStart/UserPromptSubmit/Stop，以及一份确切会话 transcript。probe-003 进一步在 hook 回调内冻结快照，同样成功；Stop 快照已有 UserMessage/AgentMessage，但没有 task_complete。不能将停止事件与最终文件刷新视为同一时刻。
- 当前原生消息可从指定 turn 的 `event_msg.item_completed` 取得，避免把注入的 environment/developer 或 response_item 重复记为对话。新增适配器仅解析该 turn；未知非文本类型明确拒绝，不宣称覆盖图片、取消、compact、多轮或所有版本。完整会话及送达仍未实现。
- 本轮实际通过 CUA 打开当前 MemlaneMVP 窗口，进入设置→Agent连接并读取无障碍树与截图；卡片显示 Codex 0.167.0／「连接已变化」，下方历史连接仍显示「已连接」。未点击安装、授权、重新验证、停止服务或模型操作。当前系统 CLI 是0.167.1，两者差异待核对发现刷新与配置引用，不能单凭截图断定原因。
- 用户问为何限制版本：目前是针对私有目录协议和非稳定 transcript 的保守防线；最终宜按能力／格式协商，版本是诊断信息，而不是无依据永久卡死补丁版本。尚未完成能力协商实现，不为了放行删除边界。

## 用户明确纠正：取消版本限制

用户原话：「我觉得不用限制codex的版本，只要记录在什么地方或者是什么变量，类似 which xxx ?」

- 本节覆盖以上候选中的版本白名单思路；产品契约已同步。电脑级连接、桥接启动、采集事件/原生消息解析、历史读取与旧官方适配准备不再按固定 Codex 版本拒绝。
- CLI 通过 PATH（等价于 `command -v codex` 的定位原则）、已有用户明确选择和 Finder 补充搜索定位；配置使用 CODEX_HOME 或 `~/.codex`。当前终端定位到 `/Users/bytedance/.nvm/versions/node/v22.23.1/bin/codex`，CODEX_HOME 未设置，配置目录为 `/Users/bytedance/.codex`。这些位置是本机事实，不硬编码到产品或提交本机配置。
- 版本仍用于来源/诊断；相同会话/turn/event 不因诊断版本变化产生新的去重 ID。格式不符合、身份不一致、可执行路径失效、协议失败仍明确拒绝，不把任意未知格式判为成功。
- 源码新增未来版本字符串和版本变化重放检查，只证明逻辑没有版本门槛；不声称测试过未来 Codex。正在运行的旧包尚未随源码更换。
- 上一阶段 agents/agentbridge 全包测试通过（32.891秒／2.383秒）；本次取消版本限制的受影响包测试单独执行，不沿用上一阶段结果。
- 取消版本限制后，agents/agentbridge/official/desktop 四包测试通过（32.945／2.604／0.943／30.346秒）；最后新增版本无关去重断言的定向检查通过（agents 1.371秒、agentbridge 0.368秒）。四包 go vet 与 git diff --check 通过。此处仅源码检查，未替换当前普通包或后台。

## 授权及持久化实施切片（未安装到日常配置）

- 新增独立 Codex 采集授权，必须是用户明确操作且电脑级连接已验证；不增加任何项目或通用知识读写范围。保存授权不代表 hook 已安装、信任或开始工作。项目工具权限变化保留独立采集授权；断开删除安装回执，重连不能复活旧授权。
- 授权／采集／已有授权安装更新和断开共用跨进程文件锁；同事件先 fsync 私有暂存再复用现有原始记录 outbox。原始正文、会话、turn、消息身份、工作目录、时间种类、Agent 版本及授权来源保留。用户提交事件只有观察时间，明确标为采集时间，不伪称原生消息时间。
- `RetainCapturedRaw` 不调用网络、不需要仓库已登记或已有 Git 身份，不创建项目，不请求整理模型；回执明确为本机暂存、待归属，绝不显示服务已确认保存。原有人工补送契约未被修改。
- 相同事件重放保留原观察时间、配置代次与去重身份；路径别名规范化；正文冲突保留旧文并报错。Stop 只读该 hook 指定的 sessions/archived_sessions 内确切会话，按当前 turn 获取助手原生消息，不扫描其他会话，不重复 response_item／注入上下文；核对 turn 工作目录与 CLI 来源。
- 解析或暂存容量出错时原始 hook 事件及原因仍有私有落盘；尚待 App 提供这些异常的实际入口。并发、异常恢复、未授权后台隔离的完整验证仍待补，尚未运行真实普通对话。
- 定向检查已通过：未知目录无服务／无项目保留、独立读取、路径别名重放、正文冲突不覆盖（0.826秒）；显式授权／未验证拒绝／不扩大知识权限／代次变化／撤权／断开重连边界（0.874秒）。这些不是 C01–C08 真实业务验收。
- 下一步仍需：完成 hook 增量安装与自有项撤销、CLI 入口、后台整理隔离、App 授权/状态/新仓库提示与异常入口、已确认项目自动送达、隔离补送与完整真实链路。当前不允许将新切片描述为 item-fe 已可日常使用。

### 本切片后续进展与边界

- 已增加 `memlane capture-codex` hook 入口及配置安装函数：只在显式采集授权后向指定 Codex home 增量添加 UserPromptSubmit/Stop；保留其他事件和定义、生成备份、不写信任数据库、不使用生产 trust bypass。Memlane 自己的 tool-mode／非 daily usage-channel 在入口排除。仍未接 Native 授权控件或修改本机实际 hooks。
- 断开先持久化撤销采集授权，再移除精确匹配的自有 hook；第三方 hook 或用户改动的自有定义保留。用户改动导致清理失败时不继续采集。
- 授权/运行定向检查通过：未授权不留原文、正常用户事件、别名重放、撤权不新增不删除、Stop 原生助手文本、解析失败先保留事件（1.075秒）。
- hooks 检查第一次有一个断言失败：实际输出保留第三方 Stop/PreToolUse，测试复用已反序列化的 map 导致缺失键仍残留在测试对象。已清空测试 map 后定向复验通过（0.844秒）；未修改生产撤销逻辑来迎合断言。首次失败保留在本记录及会话工具回执，未算作真实日常业务通过。其他同时执行的 hook 修改保留／授权先撤销、运行授权及解析检查均通过。
- agents/agentbridge/cmd/memlane 的 go vet 通过；git diff --check 通过。当前磁盘约130 GiB，未替换或重启受保护服务。

### 当前运行后台的具体兼容性依赖

只读进程映射确认 daemon76077 所属路径仍为 `build/goal-native-runtime015-20261111/MemlaneConcentratedTwelve.app/Contents/Resources/bin/memlaned`。该文件仍含旧 Codex 固定版本拒绝文案，不含新增 capture_consent 字段。源码的电脑级批量安装由 daemon 调用 `agents.ComputerConnectionError` 和 `agents.Install`；新 GUI 不会让旧后台自动使用新代码。

因此尚不能在当前真实配置中启动本切片：除旧批量接入限制外，旧后台重写安装回执可能丢弃它不认识的授权字段。需要先完成独立授权状态的兼容保存，或在用户明确后续方向后更新实际后台；不能以源码测试通过掩盖运行差异。当前没有停止、替换、重新注册 helper，也没有围绕已停止问题追加试验。该依赖不阻止独立源码/UI工作，但阻止声称真实日常链路已交付。

## 独立授权兼容、App 状态及后续 Stop（更新）

- 旧后台覆写授权的风险已在源码解决：采集授权/自有 hook 定义迁至私有 `capture-state.json`，不再序列化到 daemon 所有的安装回执。读取绑定连接及实际配置目录，拒绝符号链接、不安全权限、无效格式和跨目录复用；写入 rename 后同步目录。旧格式回执重写不能丢弃授权，也不能复活已撤销授权；旧版卸载留下侧文件后重新安装会退休该授权，原始事件不删除。
- 兼容测试第一轮失败是旧断开夹具仅删除安装回执、未移除其 MCP 块；生产安装正确拒绝覆盖看似无主的已有配置。补齐真实旧断开的配置移除后，授权/状态/采集组合定向检查通过（1.481秒）。未修改生产冲突门禁来迎合测试。
- 已在现有 Native 连接详情接入授权／停止、等待确认与重载、最近事件、异常及待发送入口；Codex 卡片另列对话记录状态。工具连接不冒充采集证明；安装未收到事件不显示采集中，旧授权事件不替当前授权作证，本机保留不显示项目保存完成。当前连接若使用非日常配置目录，授权按钮禁用并明确解释。
- 采集 hook 可独立使用当前 GUI 包内的 CLI，不依赖旧安装回执引用的旧 CLI；安装前真实检查 `capture-codex --help` 的参数能力，仍不限制 Codex 版本，不改变 MCP 桥或服务拥有者。尚未构建/安装实际新版包，未点击真实授权。
- Stop 可以继续同一 turn；以原生消息 ID 保留新增回复，重复快照不重复原文。不同最终文本事件分别暂存，同一最终文本的后续快照仍重新核对新增消息。原生助手来源使用稳定会话版本与消息时间，不以重放观察时间改变原文摘要；工具权限代次变化不重复捕获已有未授权发送的原文。
- 扩展继续执行场景后的第一轮全包检查中，测试仍断言旧固定 final 文本，实际阶段已改用另一段合成 final；修正为核对当前事件原文后，agents 全包通过（36.416秒），agentbridge/desktop 前轮全包已通过（2.276／30.468秒）。独立 UI 状态检查通过（1.086秒）；受影响四包 vet 通过；并发采集、授权/撤权、重放和本机保留的 race 检查通过（agents 2.863秒、agentbridge 2.185秒）。首次失败保留在本账本和会话回执，真实 C01–C08 仍未执行。
- 已实际打开首页/详情参考图、A revision13 待重载截图，以及固定提交导出的 HTML/CSS/JS 原型。浏览器连接器不可用时改用本机 Edge；第一次原型预览缺少图标，已补齐固定提交的四个图标资源。补图后的再预览遇用户正在操作浏览器，保留用户页面，未继续导航；不声称完成补图后的视觉验收。两个本轮预览进程均已停止，未触碰其他服务。

### 当前日常配置的只读核对

受保护实际 App 的 Codex 安装回执只有一条电脑级连接，配置目录是原验收目录 `initialization/codex-responsive`，不等于日常 `~/.codex`。实际 Agent 路径已指向当前 nvm 中的 Codex；回执记录工具协议代次2，不证明日常采集。日常 config.toml 没有 Memlane MCP 段，hooks.json 没有 capture-codex 命令。本轮没有把这条隔离连接改写为日常连接，也没有修改实际用户配置。

旧后台的固定版本接入门禁仍在实际运行代码中。独立授权文件解决了兼容保存问题，没有解决旧后台执行新安装逻辑的问题；该部分继续受 helper 停止规则约束，不自行更新实际后台。

仍需完成：仓库身份冻结与重放核对、新仓库提示/暂缓/确认及异常原文入口、已确认项目自动归属与断线补送、真实普通 Codex 两轮及 pi 来源/重开验收、新版 Native 实际窗口和固定普通包。上述任何源码切片均不代表 item-fe 已可直接日常使用。

## 仓库身份切片及后台方案决策

- 已完成采集时本地仓库身份快照，原文保留 canonical 工作目录、Git root/common-dir 及物理身份；身份未知仍保存事件，不猜项目。正文保存前不会因仓库目录突然不可用而直接丢弃整个 hook 事件。
- 新服务端归属检查会拒绝未知身份或采集后被替换的仓库；既有手动提交规则保持。新增检查没有改变 outbox 格式，也没有更新正在运行的旧后台。
- 定向检查覆盖注册身份摘要与现有 InspectProject 契约一致、同路径替换 Git 仓库拒绝、未知身份不归属、路径别名和 worktree 复用已有项目。全部通过（store 3.179秒、agents 1.740秒、agentbridge 1.105秒）。受影响完整四包随后通过：store 46.584秒、agents 36.535秒、agentbridge 3.862秒、desktop 30.017秒；四包及 cmd/memlane vet 通过，git diff --check 通过。
- 真实链路的后台依赖已具体化：现有归属 API 是逐条明确用户确认，不能被静默改作后续对话自动保存。新增路由授权和自动送达必须保持独立回执，不能伪造 UserConfirmed。当前实际后台没有这些接口，且电脑级安装仍有旧版本门禁。
- 已提出方向问题，并整理可审阅的[后台依赖与方案边界](daily-capture-runtime-decision-20261111.md)：先评审后台更新方案（仍不启动或替换 helper），或维持停止、不推进真实接入。用户尚未选择；没有将普通 Goal 继续指令解释为恢复 helper 授权。
- 本轮取得源码和新证据，属于进展。尚未交付日常接入包，也未进行 C01–C08。接下来的实际接入取决于上述方向；不以进一步堆 UI 替代该决策，不缩减原验收标准。

## 自动归属接口实施进展（用户选择方向1后）

- 已增加独立原文路由授权、私有 IPC 确认/送达接口和 daemon 补送循环源码；自动送达不携带 `UserConfirmed`，不授予知识读写 scope。沿用已有原文 outbox 与 SQL operations 幂等命名空间，不启动或替换真实 helper。
- 路由绑定当前采集授权、连接、记忆库及 Git common-dir 物理身份。事务内检查连接停用和恢复后退役；同一原文的 SQL 保存与私有 outbox 回执分别处理，回执写入失败保留 AckPending，不虚报未提交的内容已保存。
- 新增 store 与 agentbridge 定向测试覆盖重复投递只增一个版本、保留正文与 raw 类型、不扩大知识权限、拒绝库/授权不匹配、连接停用、仓库替换、回执和源摘要核对。第一轮 store 测试发现合成 fixture 换连接后未更新来源 ID，修正 fixture 后通过（2.536秒）。随后 Capture 定向四包通过：agentbridge 0.763秒、store 3.969秒、agents 3.773秒、daemon 1.748秒；daemon 结果仅证明已有 Capture 匹配测试，尚无新接口完整 IPC 验收。
- 尚需：真实私有 IPC 隔离服务集成、离线恢复与撤权竞争、仅 stage 尚未入 outbox 的重试、Native 新仓库确认与批量归属联动。上述接口源码不等于已完成日常运行链路；实际受保护后台保持原样。
- 基础配置集中记录独立记入 Roadmap TODO，不在此切片迁移路径或日常 Codex 配置。

### 隔离私有 IPC 与恢复验收

- `TestCaptureDeliveryPrivateIPCAndOfflineRestart` 使用独立临时根目录、合成配置和真正私有 Unix socket，运行当前 daemon 源码，不注册系统服务、不碰真实 helper、不调用模型。验证未明确确认仓库时拒绝自动送达、明确确认后原文落库、重复送达同一回执、关闭隔离服务期间暂存、重开后无 GUI 自动补送、采集撤权后拒绝新写入、新授权不继承旧采集原文。
- 将该测试自己的 outbox 恢复为 SQL 提交前镜像，模拟提交完成但本机 ack 丢失；重开隔离服务后恢复同一 memory 的真实回执，版本仍为1。此方法不停止受保护后台，也不将伪造回执视为成功。
- `TestCapturedDeliveryAndManualAssignmentExcludeEachOther` 覆盖自动优先和手动优先两种顺序，均只增加一个原文版本。
- 上述新增检查首次通过（store 3.715秒、daemon 2.452秒）；加入 ack 丢失后 IPC 检查通过（2.980秒）。最终 Capture/Captured 定向四包通过（daemon 3.091秒、agents 3.782秒、agentbridge 0.537秒、store 6.003秒）；五个受影响后台包 vet 通过。仍缺仅 stage 未入 outbox 重试、UI 联动及真实日常验收，不将此次隔离服务通过视为固定 App 包已交付。

### 仅暂存事件的后台恢复

- 新增 `CodexCaptureRetry`：仅遍历 Memlane 自有 capture-events，按批次保留目录迭代位置；只重试尚未完成入队及明确暂态错误。读取时检查私有目录/文件所有者、权限、普通文件与大小，拒绝符号链接；每次入队再次核对原授权与冻结事件，原观察时间保持，不扫描 Agent 历史。
- daemon 在现有补送循环中恢复已启用 Codex 的事件；撤权、断连或服务关闭时停止并关闭迭代器，不调用模型或注册 helper。
- `TestCaptureRetryAfterActualOutboxLockAndRevocation` 使用实际 flock 触发 delivery_busy，验证原文先落 stage、锁释放后仅入队一次、撤权后不补送（1.170秒通过）。隔离 IPC 增加 stage-only 事件：停止的独立服务恢复后无需再次启动 CLI，stage → outbox → SQL 原文全链路通过（8.786秒）。
- 完整后台五包回归：agentbridge 2.246秒、store 84.540秒、daemon 48.951秒、serviceipc 1.184秒通过；agents 失败于既有 `TestCodexProjectActualProtocolSeam/native` context deadline，首次本轮完整回归失败已保留，不声明全包通过。agents/daemon vet 通过；待单独核对协议 fixture 超时，不触碰真实模型/helper。
- 未完成边界改为：Native 状态/新仓库确认与批量归属联动、实际日常电脑配置授权、固定完整包及原目标真实验收。基础配置集中存储仍为独立 TODO。
- 单独核对 `TestCodexProjectActualProtocolSeam/native` 与 `TestCaptureRetry` 通过（agents 2.558秒），未改变协议测试期限；完整回归中的首次超时保留，不用该定向通过覆盖为全包通过。已向用户提出新仓库最小交互确认，答复前不将其视为已认可 UI 设计。
- 新暂存恢复及真实隔离 IPC 的 race 检查通过（agents 2.543秒、daemon 7.519秒），没有发现所覆盖路径的数据竞争。该证据仍不涵盖实际 Native 包与受保护后台迁移。

## 用户认可新仓库交互并纠正日常开发方式

- 用户明确同意新仓库“添加项目／暂缓”的最小交互，解除设计待答复项；Native 联动仍待实施。
- 新增 `python3 scripts/dev.py`：独立开发库、前台 daemon/CLI、真实 health IPC 与 MyGo dev 自动重载，不走完整封包、系统 helper 注册或真实后台切换。`--check` 的实际前台 IPC 检查通过，退出收尾已观察无开发 daemon/GUI 残留；第二次普通 dev 会话保留用于后续开发。
- 初次实际窗口检查发现开发 placeholder service_plist 不符合校验，且全新开发库误入需要已安装包的组件准备。已修正合法但不注册的 label，并仅对显式 dev 环境跳过该首启路径；不伪造初始化完成。随后源码变更由 MyGo 约1.1秒重载，实际 Native 窗口进入总览，数据计数为0。空开发库未准备索引/模型，窗口如实显示索引服务异常，不能算完整环境已就绪。
- 开发连接使用 `.local/dev/codex`、`.local/dev/pi` 与本地编译 bridge；实际日常 Codex 配置未改。受保护 PID47698/76077 保持运行。后续 UI/业务调试继续走 dev，完整包仅用于安装、签名、helper 更新及最终交付验收。

## 2026-11-12 新仓库 Native 联动切片

- 按用户已认可的最小交互，在总览已有待发送区域加入“发现待归属仓库”、添加项目/暂缓，以及固定“待归属原始记录”入口。提示仅来自可读取的本机 capture 原文，不把普通手动提交或发现安装当成采集授权。
- 复用现有三栏待归属页面；capture 原文显示仓库位置、项目名称或匹配项目、全文、此前材料及后续新增对话的归属范围。确认前重新 InspectProject，按采集时物理身份建立/复用项目，再调用独立 capture_route_confirm 和 capture_deliver。项目创建失败或后续回执丢失不删除原文；已建立项目可复用。路由及保存回执逐项核对库/连接/项目/授权/源摘要，不调用 agent_tool 或独立 user_assignment 来冒充自动保存。
- 暂缓本次提示只改变当前窗口的提示显示，不删除 outbox、不建立项目、不增加 Agent 权限；App 重开后提示可能再次出现，待归属原文入口和内容不依赖该临时显示状态。未知仓库身份仍只保留，不自动猜项目。
- 新增界面契约检查覆盖暂缓保留原文、建立与复用项目、明确路由确认与无 UserConfirmed 的自动送达。首次通过（0.822秒）；补齐回执 RouteID 等核对后定向检查通过（0.839秒）。desktop 全包通过（30.043秒），desktop/cmd-memlane-app vet 通过。上述 UI 契约测试使用合成响应，真实保存的服务端证据仍以隔离 IPC 检查为准。
- 已在实际 Memlane Dev 窗口完成：发现提示 → 暂缓 → 固定入口 → 选择材料 → 全文及项目确认面板；截图由本轮 CUA 工具回执展示，参照本轮实际打开的 home.jpg/memory-detail.jpg 和用户认可最小交互，保留既有浅蓝侧栏与三栏布局。首次实际窗口检查通过；没有点击最终归属按钮，没有改变已授权采集状态。
- Native 核对使用 `.local/dev/synthetic-capture-layout.json` 标识的无敏感合成材料，正文明确写明“不是实际 Codex 捕获”；只用于布局、暂缓与原文可读性。该开发连接没有真实协议/采集授权，不能作为真实采集或最终归属通过证据。开发数据与真实运行根分开；本轮未打包、未重启受保护后台。
- 尚需：实际获准采集连接下的新项目确认、此前多条与后续一轮自动归属的 Native 回执、pi 整理/来源/重开贯通，以及原目标日常 Codex 和最终包验收。

## 2026-11-12 日常 Codex 工具接入

- 新增 dev `--daily-agents`：保留日常 Agent 目录发现，开发库仍独立。发现实际 Codex 0.167.1、日常 `~/.codex` 及 `/Users/bytedance/work/prod/item-fe`，尚未读取该业务仓库内容或采集真实对话。
- 重开 dev 后出现旧 GUI 残留，导致窗口观察命中未连接的旧窗口。已清理本入口拥有的两个开发 GUI；真实 PID47698/76077 不变。dev runner 改用独立进程组同时收尾 Go launcher/watcher，并加单入口文件锁；当前唯一开发 GUI 与 daemon 已观察，新的组退出收尾仍需单独实测，不把源码修改当完整验证。
- 实际连接页暴露 raw-only 未安装身份阻挡电脑级接入的问题。UI 现在仅把当前 Codex 配置目录的 ComputerLevel 安装回执视为已有电脑级连接；无安装身份/其他目录不阻挡，同目录已有安装仍禁用重复接入。定向原有检查通过（1.206秒）；加入该回归场景后的批量连接检查通过（0.885秒）。没有删除合成原文来源身份来掩盖问题。
- 通过真实 Native 页选中 Codex → 连接所选 → 已重载，验证连接。实际 daemon 批次返回 completed/connected；日常 config.toml 已增量安装且原配置备份存在。连接 ID `65193290-9ec6-4789-8cc5-2c5dc4721819`，协议代次1，AllowedScopes为空。当前连接指向开发服务/稳定开发 CLI 路径，不是受保护旧 daemon；不将此描述成实际系统后台已迁移。
- 协议检查走真实 Codex app-server initialize/thread-start/direct MCP，未启动模型 turn；Native 卡片截图如实显示“已连接”“未开启自动记录”“记忆环境尚未就绪，项目与通用知识尚未授权”。最小回执在 `build/codex-daily-capture/daily-connection-20261112.json`，不保存日常配置正文、密钥或业务聊天。
- 已展示实际采集授权面板并向用户提出当场确认；原生操作规则对新增敏感数据访问要求即时确认，答复前未点击授权。capture-state 尚不存在，日常 hook 尚未安装，不宣称正常对话已可自动记录。确认后还需 Codex 自己的新增 hook 信任/重载，以及受限新增对话验收；不绕过信任检查。
- dev `--worker-config` 源码支持复用已安装 worker/模型/运行库路径，但强制开发索引 DataDir，避免复用真实索引库；语法检查通过，尚未执行该 worker 配置的开发运行验收。后续需要实际索引/pi 条件，不能长期用空 worker 的索引异常状态冒充完整主流程。

### dev 退出与已有索引组件复用实测

- 在未开启采集的状态下停止本入口拥有的 dev runner，逐项核对 runner、daemon、Go launcher、MyGo watcher 和 Native GUI 原 PID 均已不存在；真实 GUI47698/daemon76077 保持。这次验证了组收尾修正，不把上一轮清理旧孤儿进程视为修正通过证据。
- 用已存在的服务配置作为 worker 路径来源重开 dev（未改来源配置）；只读复用原 Rust0.0.4、ONNX Runtime 与 multilingual-e5 模型，DataDir 强制为 `.local/dev/client/data/index`，不复用真实索引库。没有下载、完整打包、注册系统服务或触发图谱重跑。
- dev 在显式 worker 模式等待真实 health.state=ready 后打开窗口；独立 IPC 核对 state=ready、worker_pid=24286、worker_error为空、worker_busy=false、DenyNetwork=true；Native AX 显示“服务运行中”，不再是上一轮空 worker 的“索引服务异常”。回执为 `build/codex-daily-capture/dev-worker-readiness-20261112.json`。此证据证明组件进程就绪，不替代具体原文索引/检索和 pi 整理的逐项验收。
- 日常连接仍只有工具协议授权：capture-state 文件不存在，未安装采集 hook，未读取 item-fe 内容，仍等待用户对开启采集的明确答复。自动 Goal 继续没有被当成该答复。

## 2026-11-12 用户确认开启后的真实新增对话验收

- 用户明确回复“确认开启”。已在实际 Native 日常 Codex 详情点击授权，增量安装 UserPromptSubmit/Stop hook，状态显示“等待 Codex 确认与重载”，没有提前显示采集可用。日常 CLI 在 item-fe 普通启动（默认 daemon、无信任绕过参数）；Codex 提示6项待审核，逐项核对后只信任 Memlane 两项新 hook，保留4项无关待审核项。
- 第一次日常采集验收失败：用户原文已保留，但助手 Stop 原文未完成。最初最新回执指向缺少 transcript 的辅助标题会话；进一步用事件/会话身份区分，实际 TUI 主会话有 transcript，metadata.source 为 vscode、originator为codex-tui，旧 cli/exec 门禁错误拒绝。原失败次数为1，旧 helper/图谱失败次数不变；没有把几个定向断言或重放拆成新的验收轮次。
- 使用 diagnosing-bugs 流程先建立单事件重放和红色回归：原真实主 Stop 重放稳定返回 capture_session_source_excluded；普通 daemon TUI 来源识别与无 transcript 辅助 prompt 两项回归先失败。首个临时重放脚本误选最新辅助事件，断言失败后已纠正为明确主会话 Stop；未用该错误脚本输出代替真实问题证据。
- 修复仅在 vscode + codex-tui 的明确组合接受默认 daemon 下的普通 CLI；IDE originator继续拒绝。用户 prompt 入队前也核对当前确切 transcript，辅助/来源未证实事件保留 stage而不混入可归属原文。对已排队旧材料，自动路由和新保存前从同一 turn 的确切 prompt stage 重新核对，既不扫描 Codex 历史，也不删除/改写旧原文。Native 预览禁用未证实会话来源的确认，发现提示过滤此类事件；仅经核实的主会话更新最近采集状态。
- 回归及来源/队列/私有 IPC 定向检查通过；旧辅助材料不能获得自动归属证明的新增检查通过（0.927秒），后续 Capture/Captured 检查通过（agents4.118秒、daemon6.704秒）。agents/daemon/desktop vet通过。受影响完整四包通过：agents38.951秒、agentbridge2.694秒、daemon28.055秒、desktop30.797秒。
- 仅重启自有 dev runner，真实 GUI47698/daemon76077 未停止或替换。第一轮主会话失败 Stop 由后台自动补送，原操作身份保持；单事件原重放随后通过，诊断脚本保留于 build/codex-daily-capture/replay-daemon-tui-stop.py，无模型重调用。
- 在未登记 item-fe 的情况下，普通日常 CLI 连续两轮只使用明确标记的蓝色/42无敏感测试文本，不读仓库文件、不调用工具。独立读取该会话确切原生事件与自有队列，2个 UserMessage、2个 AgentMessage，四条正文逐字匹配、助手时间匹配、来源会话/轮次/工作目录正确、无重复，且仓库尚未登记。回执：normal-cli-two-turns-20261112.json。
- 实际 Native：发现 item-fe → 暂缓 → 固定待归属入口 → 阅读第二轮助手原文与来源 → 确认“添加项目并关联原始记录”。API独立核对只建立一个项目（66af36b0-8d04-4fb1-a97f-5eabdb93ccd4），四条原文保存为四个不同 memory/v1，正文与Source.Fragment和暂存完全一致，均indexed；辅助标题原文保留但未归入项目。回执：native-project-attribution-20261112.json；实际窗口截图见本轮 CUA 回执。
- 未再点击 App 归属按钮，同一普通 CLI 继续第三轮。新用户/助手两条自动归入同一项目，合计6条不同原文/v1，均indexed；Codex AllowedScopes仍为空。回执：normal-cli-continuation-20261112.json。正常 CLI 已通过 /exit 退出，退出码0；后台 daemon 模式自身的共享进程不据此推断为零或已回收。
- 真实日常接入以上通过仅覆盖这次模型、三轮限定材料、dev 数据库和当前CLI0.167.1；不是完整交付。仍需 App/CLI重开后继续、pi选材整理/保存知识/来源及最终运行交付。日常Agent配置已实际修改且备份存在，不能继续沿用此前“日常配置未改”的阶段结论。

### pi 整理、来源及关闭重开贯通

- 实际 Native 连接日常 pi0.84.1，增量安装自有 extension；验证 deepseek/deepseek-flash 后明确设为当前使用。未复制凭据，未扩展 Codex 项目知识读取权限。
- Native 选择 item-fe 的六条原文 v1，显示固定范围及 Token 消耗后启动真实 pi 整理。任务225792d9-b745-490e-a7c2-553c91396021首次完成，新增知识00bbcce2-262c-5277-8910-1b5715705804/v1并索引；正文为“在 item-fe 工作目录的 Codex 无敏感测试对话中，验收约定为颜色蓝色、编号42。”六条输入正文、来源和版本未变。六条是输入数量，知识实际引用一条第二轮用户原文，其引用可支持两个事实，不宣称六条来源引用。
- 实际 Native 从知识“阅读原文”打开对应 R2 用户原文；独立核对引用片段和来源 v1。真实执行回执 terminal_completed/process_reaped/process_group_gone=true、exit_code=0。证据 pi-organization-source-receipt-20261112.json。
- 停止并重新启动自有 dev runner，原六条 memory 身份、知识正文/v1、当前 pi 和采集授权均保留。受保护旧 GUI47698/daemon76077不变；证据 reopen-persistence-20261112.json。
- 关闭并重新普通启动日常 Codex CLI，无信任绕过；Memlane 两项 hook 信任保留，四项无关待审核 hook 未信任。新会话01a1cb94-20d6-7183-907c-a523b75963de新增 R4 无敏感测试，用户/助手两条无需 App 操作自动归入原项目且 indexed/v1。此前六条仍为v1，知识仍为v1；证据 reopen-cli-continuation-20261112.json。测试 CLI 正常 /exit，退出码0，未停止共享 Codex daemon。
- 再次关闭自有 dev，直接 python3 scripts/dev.py --daily-agents 启动，成功复用自身 service.json 中已验证的 worker 路径并等待真实索引 ready，不再每次传入旧组件来源配置。当前 dev 保留运行；实际 Native 显示服务运行中、知识1、原始记录8，并打开 R4 助手原文核对会话和正文。无完整封包、系统服务注册或受保护后台更新。
- 上述是开发环境真实限定主流程通过；正式固定运行包/签名/helper迁移仍未完成，旧 helper 问题停止规则继续有效，不因开发路径通过而重置次数。离线补送以此前私有 IPC 故障注入证据为限，尚未把日常 CLI 的真实断网/离线轮次描述为通过。日常采集已启用且实际配置已修改，开发服务需运行才能继续自动落入该开发库；停止期间保留材料依照既有重试契约恢复。

## 2026-11-12 Codex App 自动记录接入启动

- 用户问 App 能否使用并同意继续接入。目标主流程：App 在 item-fe 发一轮指定无敏感测试 → Memlane 收到真实 UserPromptSubmit/Stop → 核对当前确切 transcript 的来源、会话/轮次/cwd和用户/助手正文 → 两条原文自动归入既有项目 → Native 可阅读；不重新打包、不改变既有知识或重置旧失败计数。
- 当前源码来源门禁仅覆盖 cli/exec 和 vscode+codex-tui，尚不能从中推断 App 来源。没有为了支持 App 直接放开全部 vscode 或去扫描历史。
- 电脑工具明确拒绝操作 com.openai.codex；已结束该 UI 操作阶段，没有尝试用其他 UI 技术绕过。请求用户在 App 的 item-fe 新对话发送 MEMLANE_APP_TEST_20261112 限定测试消息并回复“已发送”。可以继续通过本项目自有 capture-events 观察真实 hook 及确切 transcript；暂未收到指定事件不计为一次验收失败。
- 观察回执 build/codex-daily-capture/app-test-observation-20261112.json 当前为 awaiting_user_app_turn。App hook执行、真实来源和完整采集均未知，不宣称已实现或通过；收到样本后再确定最小实现及回归。

### App 用户测试位置确认

- 用户已发送并明确 blog 即 zwhu.github.io。核对自有 capture-events，尚无该工作目录的指定测试标记事件；已有两项同目录事件属于自动建议生成，没有 transcript，继续保留 stage 且不归属项目。当前对话自身的助手消息含测试标记，工作目录却是 memlane，不把它误判为 blog 测试。
- 为定位用户明确指定的单次测试，只读查询两个实际 state 元数据目录的确切标记/项目元数据，没有导入或修改 Agent 历史、没有补造 hook 或手工保存来冒充自动采集。尚未定位该测试会话，不能据此判定 App 不执行 hook，也不能确认原因是来源门禁。
- 已请求该段 App 会话链接，或标题及实际测试原句，以便精确定位。状态 user_sent_blog_test_not_yet_located；原有失败记录和计数保持，不将未知状态算作已通过或重复重跑。

### 本机 Work 开发模式只读核查结论

- 用户明确确认本机并授权核查。现已定位实际本机 Codex 任务01a1cba6-29fb-7bd2-a129-84581bdcd07f，轮次01a1cba6-2c53-7900-bd40-b8889483f2a6，cwd=/Users/bytedance/work/AI/zwhu.github.io。与此前截图中“应用测试回复”是否为同一段不能仅凭原句推定；本次匹配任务标题为“确认 Memlane App 测试”，原句与用户指定标记一致。
- UserPromptSubmit及Stop均真实执行，已将指定测试原句及“App测试收到”保留到自有 stage，均报capture_session_source_excluded；并非hook未加载、电脑级授权缺失或自动建议事件。metadata.source=vscode、originator=Codex Desktop，实际App会话版本0.167.0-alpha.3.2，与PATH安装CLI0.167.1不同；版本仅诊断，不以换版本解决。
- 当前来源门禁仅接受vscode+codex-tui，明确拒绝Codex Desktop，构成本轮直接阻断点。确切transcript存在单一metadata，当前轮次具备UserMessage/AgentMessage的item_completed和task_complete，具备既有解析器需要的事件类型；尚未以完整解析/保存重放证明修改门禁即可全链路通过。
- 本轮仅核查，未修改日常配置、信任、来源校验或保存原文，未重启后台/重跑模型。回执app-test-observation-20261112.json更新为local_work_hooks_retained_source_gate_rejected；本机材料保留不等同项目已保存，后续新仓库归属仍需明确确认。
- 纠正此前仅凭局部截图将测试归为普通聊天的解释：本机Work/Codex Desktop的实际hook已可观察，应按现有采集适配兼容问题处理，不以另做普通ChatGPT采集的5–10人日估算套用。普通聊天采集TODO仍独立保留，不在本轮恢复实施。原有失败记录保留，本次只读核查没有重跑验收。

### 用户授权补齐 Desktop 与多客户端适配规则

- 用户要求“1.补上；2.思考未来类似情况，包括Claude的两种入口”。来源回归加入真实观察的vscode+Codex Desktop后先红（capture_session_source_excluded），修复为同时接受codex-tui/Codex Desktop两个已观察组合；codex-vscode和未知originator仍拒绝。版本不设白名单，现有授权、当前确切transcript、会话/轮次和仓库校验不变。没有放开所有vscode，也没有改变MCP/hook/信任或知识范围。
- 只重启自有前台dev，后台原事件恢复机制自动将刚才同一App测试的UserPromptSubmit/Stop补入队列，原事件和操作身份不变、无需新模型调用。两条正文逐字匹配指定原句与“App测试收到”，会话/工作目录保持，形成两条不同原文。回执desktop-recovery-20261112.json；仍待首次项目归属，不冒充项目SQL已保存。
- 实际Native首次读取队列失败，独立同库读取成功；限定安全诊断确认delivery_busy且根目录匹配。捕获后台与UI共享非阻塞队列锁，UI单次读取遇到短暂持锁会报不可读。首个修正覆盖列表，下一步归属预览暴露同类读取点；同类集中补齐列表、原文、归属请求读取和预览的有限等待，最多1秒，25ms间隔，随所属视图取消，仅重试delivery_busy（包括IPC安全码），其他错误原样交给现有提示。一次未使用import的编译失败已修正，未作为验收通过。
- 使用真实flock竞争的TestDeliveryViewWaitsForCaptureLockAndHonorsCancellation覆盖锁释放后的成功及关闭视图取消；定向Capture/Captured/Assignment检查通过（desktop2.505秒），等待路径race通过（2.169秒）。来源修复后agents全包通过（47.001秒）；最初三个采集包定向通过（agents5.633秒/daemon5.353秒/desktop1.115秒）。agents/daemon/desktop vet通过；所有临时DEBUG-desktop-capture诊断已清理。
- 最终实际Native显示“发现待归属仓库 /Users/bytedance/work/AI/zwhu.github.io”，打开“添加项目”预览后列表正常、显示指定测试原句及真实session/turn/cwd，项目名zwhu.github.io，“添加项目并关联原始记录”可用。未点最终确认：保留给用户确认新仓库的当前及后续自动归属；这不阻塞已授权原文的本地保留，也不增加项目知识读取授权。用户不用重发测试，普通后续对话沿用已启用的采集hook。
- 多客户端规则已写入agent-connection-contract.md：按Agent家族/客户端形态/执行位置/实际配置目录识别，同配置共享接入，逐能力验收，来源兼容与授权分开，未知来源保留并可恢复、按原生消息身份去重。Claude CLI/Desktop编码/普通聊天不预设共用协议，须分别实证；能力账本及设置反馈记入Roadmap，未在本轮实现整套框架或Claude接入。
- 本轮没有重跑受停止规则保护的旧helper/图谱验收，没有迁移旧系统后台或构建正式包。上述只证明本机Work测试消息捕获及恢复到可确认归属的实际开发路径；首次blog归属后的真实保存及后续新增轮次未在本轮执行。
- 上述等待修正及归属读取统一后的desktop全包最终通过（32.742秒），git diff --check通过。原始失败与各阶段结果保留，不用最终通过覆盖历史。
