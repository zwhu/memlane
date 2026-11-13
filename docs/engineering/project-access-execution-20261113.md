# 项目与权限实施记录

2026-11-13，当前 owner session `01a1d316-954a-7c91-8191-7fdfe3d4a8c9`。原 owner `01a1d30a-d776-7f92-a7e9-9424e5fabc13` 按用户暂停后明确释放，仅留下两个未测试独立模块及本账本。当前用户附件再次明确启动 PA01–PA14，覆盖旧“无权限／共用 owner”的相应方向；历史全量 Goal 保持暂停。dev 模式，不做 release。

## 基线与完成条件

依据项目权限产品、规格与技术方案 `project-access-*-20261113.md`，已完整读取；固定官方源码事实及可行性入口亦已读取。使用仓库 plan-change-impact、verify-continuous-scenarios。当前 thread 已有同范围附件 Goal，继续该 Goal，不新建重复目标。Git 起始已有十一份未跟踪方案文档，保留归属，不自动提交它们。

最终连续流程：默认设置 → 新仓库新会话 → 管理侧幂等登记／建官方组／核对 ACL → 首次写前绑定 → 官方采集及处理 → 桌面自动出现／阅读 → 后续会话实际采用。PA-A–H 全部通过才能交用户验收。协议夹具不证明真实权限、采集、模型、Native。

| 要求 | 影响与依赖 | 完成证据 | 当前 |
| --- | --- | --- | --- |
| PA01/08/09/12 | project_control 登记、官方组、桌面目录 | 并发／路径测试、真实新仓库、自动发现 | 登记及幂等已有源码／夹具；桌面与真实链路待接 |
| PA02/03/06 | project_policy、独立 Agent、官方 ACL、项目设置 | 字段继承、有效 ACL 允许／拒绝、Native | 策略及主体管理已有源码／夹具；真实ACL及Native待验 |
| PA04/05/07/14 | 固定官方插件 patch、无正文游标、blocked、所有触发入口 | 不读取／不排队、403 停重试、显式恢复、真实任务状态 | 独立补丁及真实函数夹具已有；日常安装、UI及真实任务待验 |
| PA10/11 | 官方通用组、CRUD、单一 coding 注入 | 官方终态、另一真实会话采用、项目优先、拒绝不注入 | 管理组／CRUD／合并注入代码及夹具已有；UI及真实采用待验 |
| PA13 | 启动器、控制进程、目录刷新、阅读撤权 | 关窗／重开、缓存失效、复制入口撤下 | 启动器已有源码／夹具；目录刷新及策略UI待接，真实启停待验 |

## 连续场景与执行次序

1. PA-A/F：同一新仓库两 session 并发 resolve；符号链接／子目录复用、同名不同根和 worktree 分离；首次写前绑定 UUID，新组代码图默认关闭。
2. PA-B/C：默认 maintain/capture/process；显式 false 与 none 覆盖优先；修改模板动态继承，恢复字段；默认只读仍登记，不读取新正文；新 Agent 纳入继承而不改变覆盖。
3. PA-D/E：管理 owner 与两独立官方主体；直接调用 raw/session/search/graph/save/update/delete，核对用户、role、tenant 有效并集。同步失败保留目标 revision 与原操作；不 owner 回退，不把直接 ACL 当有效 ACL。
4. PA-E：403 阻塞原合法 buffer、无自动重试；禁采期仅 stat 推进水位，恢复不回扫；明确补交／丢弃分别验证。401／409／网络未知分开。
5. PA-G：通用知识官方 CRUD／处理终态，空项目及另一真实会话一次注入采用；项目约定优先，无权不注入，缓存撤下。
6. PA-H：Native 默认规则／项目设置／新建关联、目录15秒与激活刷新、关窗重开及来源往返；原 memlane/item-fe 会话不迁移、补采、重放或删除。

先独立模块及定向测试，再官方 HTTP 契约夹具、真实服务权限及采集／模型，最后与阅读 UI 连续 Native 验收。任何结果未知先查原身份，不重发内容／模型任务。

## 文件及运行所有权

- 已向两个指定 session 发联系消息。阅读 UI 首次发送／读取工具返回 list_turns unsupported；第二次发送成功，等待回执。app.go/client.go 尚未取得阅读 UI 释放，暂不修改。
- 总览回执：完成提交 `4db3b8b`，源码 app.go/client.go/dev.py 已释放；app/client 仍须与阅读 UI 协调。组件输入未改、未持有。保留 overview*.go、首日字段及 open --watch-ui 逻辑。
- 总览运行回执：旧 launcher／completion／official 仍拥有日常库；其附着 Native watcher 在 tool session29190。此为历史定位，任何启停前重新核对数据锁／进程树／启动时间；当前不启停共享服务、不切凭据。
- 本任务先独占新增 project_control.py、project_policy.py、project_access*.go、独立插件补丁及测试。本次未授权改写其他 session 源码归属。
- 初始 `df -k` 可用120769380 KiB（约115GiB）；测试沿 task.py/go.sh，运行受105GiB门禁。保留日常官方数据及 build 依赖。

## 既有缺陷与停止边界

- 已读精简版错误索引及 JSON；E17历史累计2，E09/E10/E15各1，其他按原账本，helper／固定328字节README停止问题不恢复。
- OVR01 按总览账本独立保留：真实召回／整理 BrokenPipeError，图 worker multiprocessing._flush_std_streams 刷新继承 stderr PIPE 失败；读端退出原因未知。总览仅定位，未修复、未重启、未重放。不能算作新 PA 缺陷或忽略此真实链路前置。
- 新 PA 产品验收尚未开始，无新增失败轮次。准备／方法错误单记。同一具体问题第4次失败停止该问题及依赖步骤，保留全部旧计数。

## 资源与证据

配置只存关联、策略、同步回执及 session 绑定，不存正文；私有key独立0600文件，不入Git。合成测试使用临时目录并自行收尾，不创建第二日常库。共享后台、watcher、凭据由交接规则保护。真实运行与Native证据待执行，未证明边界保持未完成。

## 新 session 接管与实现阶段

- 已收到原 PA session 明确暂停／释放回执：无后台任务，不持有共享文件、Native、watcher或凭据。新阅读 owner `01a1d316-960e-7280-a9fa-f4abb7abdb75` 先持有 app.go/client.go 阅读接线窗口，完成后释放；本任务暂不修改两文件。阅读将提供缓存失效入口供策略代次接线。
- 本地基线提交 `951e44e`，注释 tag `pre-project-access-20261113`：仅收纳已明确移交的两个草稿模块与本账本，未纳入其他 session 文档／源码、私有配置与数据。
- 新增 `project_control_server.py` 固定路由接口：管理与安装实例 token 分离，Agent身份来自token，拒绝身份伪造字段、正文、浏览器Origin；16KiB请求上限、固定超时。仅配置登记／策略核对，不执行内容或模型任务。
- 登记实现覆盖单写者锁、原子0600配置、并发同仓收敛、稳定路径身份、session固定、模板动态继承与字段覆盖、revision冲突、未确认操作只显式reconcile。发现关联既有组的草稿会套默认ACL，已在实现审查阶段改为先读有效权限并导入访问覆盖；不可表示的组合保持未确认，不能放宽。
- `python3 scripts/task.py -- .local/cognee-python/bin/python -m unittest discover -s engine/python -p test_project_control.py -v`：8项通过，临时合成目录退出后收尾；覆盖并发／路径、session固定、false／继承／恢复、持久重启、未确认不自动重试、手动自动去重、关联ACL保留及HTTP身份边界。属于源码／协议夹具，**不证明真实官方ACL、模型或Native**。
- 当前仍未完成：官方主体创建／安装实例配置、有效ACL真实核验、通用组、插件补丁、启动器接线、Go管理入口与Native，以及PA-A–H真实连续场景。尚无新PA产品验收失败轮次。

### 控制接口及插件策略基础（未接入日常运行）

- 管理侧新增安装实例主体创建，按固定官方 `POST /api/v1/agents/create?name=...` 无JSON body契约；官方key与控制token分别写0600文件，响应不返回凭据。先保存创建操作，再发一次请求；未知结果保留unconfirmed并拒绝自动重建。保存了原key的部分成功仅能显式核对原主体，不能换owner。
- Official HTTP夹具核对独立子用户／parent、四项授权请求、tenant/role并集。角色仍有write时撤销直接grant不能显示已核对。没有在日常官方服务上新建用户／数据组或改ACL。
- 新增 `project_plugin_policy.py`，待作为 `_memlane_policy.py` 装入固定官方插件补丁：按控制token＋宿主session隔离无正文状态；写拒绝／401／403保留blocked；自动process开关不替代手动权限；禁采时只stat推进边界，恢复首次hook建立新边界并跳过整轮；轮转／截短保持暂停；已有非受管launch拒绝静默绑定。该文件**尚未被官方插件入口调用**，不能将其单测当成官方capture已被拦截。
- 同一受管命令 `python3 scripts/task.py -- .local/cognee-python/bin/python -m unittest discover -s engine/python -p 'test_project*.py' -v` 扩至17项通过。插件策略测试以禁止打开transcript的断言核对stat-only、恢复边界、截短、blocked保持、手动／自动区分、既有session不迁移。测试结束未留所属服务／模型进程；日常库和并行Native watcher未动。aiohttp存在RequestKey建议告警，未影响断言。
- 阅读session确认继续持有 app.go/client.go 及 Native 操作窗口，并拟提供 `App.InvalidateReading(dataset)` 接口。共享文件出现其未提交修改属预期，本任务不纳入提交。
- 下一实施依赖：将策略基础接入固定官方capture/drain/idle/cooldown/exit/remember/recall各入口并生成可复现patch及摘要；完成blocked显式处置与游标解析参数，不能只接SessionStart；再接通用组与单次注入、启动器、桌面管理及真实PA场景。整体Goal仍active，未达到交付定义。

## 官方补丁、通用知识与启动器接线阶段

本轮前一Goal turn归类为progress：已提交基础接口及17项检查；本轮继续实施，无自动等待／盲重发业务。

### 已实现与证据边界

- `engine/patches/project_access.py` 对固定Codex官方插件做精确锚点补丁；每个锚点恰好匹配一次，失败即停止构建。`scripts/build_project_plugin.py` 校验原版完整56文件输入，输出到新目录，编译修改后的Python；不原地修改归档、固定源码或日常安装缓存。全部最终文件摘要、patch及extension摘要锁定于 `engine/managed-project-plugin.json`，组件清单显式引用。
- 首次SessionStart绑定前管理侧resolve、后续UUID固定；受管路径禁用共享角色刷新、身份自动创建与owner缓存回退。capture在官方正文解析前检查并推进stat-only边界；官方解析器新增offset，跨边界首行及没有新请求的旧turn尾部不采。begin_turn、prompt存储、新buffer入队、drain逐批、自动improve、显式remember及coding recall均接门禁。
- 官方403/401保持原buffer并设置blocked；自动process同样受阻；权限恢复不移除blocked。原版detached-final自动重试在受管模式关闭；ambiguous写入无法从原官方session确认时不盲重发。HTTP409／网络未知不冒充403或主动取消。
- 补交／丢弃函数与命令入口只处理原官方buffer，校验绑定session和已审阅snapshot摘要；发送锁仍持有则拒绝处置，不假取消在途发送。补交重查write；未知原写入仍不能补交。没有另建正文队列。桌面按钮尚待接线。
- 通用组独立于项目目录，管理owner创建、Agent仅read；已配置原项目关联按显式UUID导入，不复制／迁移内容。coding在原build_block内合并项目与common候选，空项目可用common，注明项目优先及来源；CHUNKS结果按document ID与官方metadata核对后读取raw。最多22项目候选＋8通用候选，项目／通用各至多4次缺正文读取（合计8），通用4并发；总预算8秒、最终块6000字符（项目4000、通用2000），首次无query最多3条。策略代次变化撤下当次结果，无第二注入hook。
- `common_knowledge.go` 使用官方普通文本multipart保存、metadata数组、单值node_set；更新上传正文且不传node_set／metadata以保留原值，拒绝改分类／原始来源。HTTP部分失败保留原pipeline回执，保存／处理／未知区分。单文档删除校验官方dataRemaining=false，HTTP200本身不代表完成。管理UI、实际保存与另一真实会话采用尚未证明。
- `dev.py` 可按显式project_control配置管理第三个进程；就绪为带管理token的目录读取，关窗保持，退出不自动重启／切owner。运行记录显式区分service_children，第三服务不误认为UI watcher；配置token私有独立、loopback端口、受管插件完整摘要均检查。实际 `.local/daily/runtime.json` 未改，没有启动控制进程或切换Agent凭据。

检查：30项Python控制／插件检查通过，含12项补丁后的真实官方函数夹具；`python3 scripts/task.py -- python3 -m unittest discover -s scripts -p test_dev.py -q` 12项通过；common Go定向检查、受影响包全量test及vet通过。源码／HTTP夹具与真实服务／模型／Native严格分开，不能据此判PA-A–H通过。

### 方法观察与资源

- 两次patch准备锚点检查拒绝：SessionStart相同赋值存在多个位置、缩进子串误匹配；未生成或安装产物。已将锚点连上上下文并按行首缩进核对。为准备／方法问题，不计PA产品失败轮次。
- 一次以便携Python3.12运行启动器既有venv夹具，复制出来的解释器缺libpython动态库而SIGABRT；未启动产品服务。改用启动器原系统Python入口后12项通过，没有放宽venv路径保留规则。准备／环境观察1次。
- `.local/project-access-plugin-dev-03` 为本任务最新独立补丁产物，非日常安装，摘要随候选输入锁定；此前02曾通过启动器完整摘要校验。临时测试目录均由TemporaryDirectory收尾。可用空间约114GiB，未清理build或日常库。
- 阅读session交付 `62b324f`，其后文档 `c492f32`。已明确释放app.go/client.go/overview_ui.go及Native窗口，本任务已回执接管；保留 `InvalidateReading(dataset)`、`SetReadingPolicyRevision(revision)` 和raw/VisibleBody边界。阅读因OVR01依赖尚未完成RU-G，等待后台恢复，不混为PA通过。
- 总览session已退出，明确说不能确认当前运行所有权，之前源码释放不包含后台启停或凭据切换。故尚不启停日常后台。当前经 `dev.py status` 核对launcher69475、内核启动时间Fri Nov13 02:07:42、原配置及数据锁仍有效；这只是所有者快照，不证明图worker健康或后台操作窗口已交接。

下一步：桌面项目默认／覆盖、新建关联、自动目录刷新、通用知识管理与buffer处置接线；再核对运行排他窗口、真实独立主体ACL、完整新仓采集／处理／采用和PA-A–H。OVR01后台恢复应先保留原任务与数据，不能靠重放模型／索引掩盖。整体Goal继续active。

## 桌面接线、安装配置与运行修复候选

本阶段已接管阅读 session 明确释放的共享源码，保留其 raw／VisibleBody 与阅读失效入口。没有另做阅读界面或总览。

- 桌面新增项目默认规则、逐 Agent 字段覆盖／恢复继承、实际同步状态、新建／关联与别名入口；关联选择管理侧可见组，排除通用及保留代码组。目录每15秒／激活刷新，失败保留旧目录；新增项目不清空原阅读，策略或身份变化调用阅读失效。草稿保留最初revision，冲突不静默覆盖，独立操作不清空其他草稿。
- 通用知识管理接入官方正常文档：分类、适用条件、来源、创建／编辑／删除。编辑前核对原正文SHA，变动不覆盖；保存回执保留部分成功和未知，不自动再投。明确核对原保存结果分别查原文与pipeline，不将正文存在当处理完成。撤权撤下编辑器内原正文，保留在途状态与回执，不假取消。
- 待发材料入口通过原安装实例环境、宿主session和官方buffer查询／补交／丢弃；核对固定产物与helper全部摘要，不信任未校验脚本。显式动作使用已审阅signature，未知写入仍不得重发。
- 安装准备使用每实例独立插件目录与0600环境引用，generation摘要隔离；配置生成不改宿主正在运行的安装或会话。官方dotenv不解码JSON转义，改为兼容原解析器的文字外引号并拒绝换行，Unicode路径已覆盖。受管marker先于官方环境加载，强制独立子主体，拒绝全局owner、共享角色、缺配置CLI回退。
- 官方buffer在每次POST前，先在原entry写入未知结果标记；进程即使在POST后退出也不会无记录自动重发。明确401／403可判定为拒绝，保留blocked；检索403不错误阻塞项目写入。
- 补齐显式recall的固定服务、子key与当前项目／可读通用UUID范围。代码入口在仓库扫描前要求管理侧明确已启用、已核对的代码组，禁止slug建组或本地旧opt-in绕过；新项目保持关闭。**控制进程当前尚未实现既有可选代码组的启用／关联契约，受管插件代码入口因此保持未启用；Native管理身份原代码图入口未改。此为待完成项，不宣称已有Agent代码图接入。**
- 启动器 OVR01 修复源码及独立红绿证据见 `overview-stream-repair-20261113.md`。旧后台尚未重启，不宣称真实图worker已恢复。

### 本阶段检查与方法观察

- Python项目／插件37项通过；系统Python启动器15项通过；Go全量test、vet及受影响包race通过。补丁后的真实官方函数使用合成宿主／协议夹具，仍不证明真实官方ACL、模型采用和Native。
- 更正前一阶段文件数描述：固定上游完整树为57文件（含隐藏plugin元数据），加入extension后58文件；不是56。当前新产物为 `.local/project-access-plugin-dev-06`，修改12个官方脚本；固定输入、patch及最终文件摘要已更新。未修改原官方归档和日常安装缓存。
- buffer adapter夹具曾将macOS `/var` 与 `/private/var` 文字比较，导致同候选的全量和race检查失败；按真实路径比较后定向及最终全量通过。属于路径别名夹具问题，不计PA产品验收。既有便携Python夹具环境观察保留。
- 本阶段没有新PA真实验收轮次；OVR01及旧错误索引计数保持。不得将37个断言当37轮验收。

### 运行交接与未完成边界

日常 `runtime.json`、官方数据和现有memlane／item-fe会话未改。私有 `runtime-pa-candidate.json` 只准备同库配置、独立控制端口18777、管理token引用及新产物路径；不是已生效配置。未启停服务、未创建真实Agent／数据组、未改ACL、未切凭据、未采集／重放旧材料或调用模型。

任务附件第4节要求 Native／watcher／官方后台／凭据明确排他交接。阅读已释放Native窗口；总览退出，源码交接不含后台启停。已向用户提交“后台交接并同库重启／暂不重启”问题，尚无答复；不将无答复视为批准。候选配置仍须按最终产物重新校验，重启前复核原PID、启动时间、锁、原库与凭据引用。

仍需完成：安装实例接入的实际宿主加载及旧消费者边界、可选代码组管理契约、真实独立主体有效ACL及允许／拒绝、新仓实际采集／处理／归属、禁写恢复与buffer显式处置、通用知识真实保存与另一会话采用、PA-A–H及Native连续阅读。源码检查通过不满足交付定义，整体Goal保持active。

### 后台排他交接已收到

用户明确答复“交接并同库重启”，覆盖上段等待状态，授权范围为同库停止／启动一次、日志修复和新增控制进程；不切换Agent凭据、不重放旧任务。重启前核对旧launcher69475启动时间、官方69478及completion69477，三者标准流确为PIPE；原库和候选路径samefile，候选原有字段逐项未改、插件06摘要通过、私有文件0600。`dev.py stop` 确认进程组退出、原库释放。原配置私有保留后原子更新runtime.json，按统一入口 `dev.py start --no-ui` 启动同库。后续就绪及业务健康仍待实际回执，不以stop成功代替恢复通过。

新launcher15986／completion15990／official16019／control16202已ready，三后台FD1/2均REG；健康接口200，原配置项目仍在，控制登记旧会话为0，通用组管理侧创建并核对。OVR01新只读CHUNKS请求200/list1，未重放历史任务，详见独立复盘。

### PA-ACL01：真实权限核对身份错误（累计失败1轮）

首次真实实例 `pa-verify-codex-a` 创建成功，原主体与私有key保留；配置revision3。项目和通用组的ACL核对返回official_http_404，保持unconfirmed，未宣称完成。回执 `.local/project-access-evidence/real-agent-enrollment.json`。

已证根因：固定官方 `authorized_get_principal_datasets` 按请求者当前tenant授权；管理owner没有选择tenant时，代查child会进入has_user_management_permission(None)并抛TenantNotFoundError，404不是子主体丢失。原夹具未模拟该限制。已将夹具改为管理代查404，复现同一红证据；修复为使用原child私有key查询自身principal权限，角色枚举仍由管理身份按官方接口核对。未创建第二主体，不以owner回退。

37项源码／协议检查修复后通过。独立只读调用修复后的Official，核对原child真实有效权限：原项目read/write/delete，通用组read；未新建组、未改权限、未再创建主体。回执 `.local/project-access-evidence/acl01-self-query.json`。运行控制进程仍是修复前模块，applied仍unconfirmed；真实闭环尚未复验通过。已向用户说明此前批准仅一次重启，询问是否允许加载本修复及在已交接窗口内继续必要dev重载；等待答复期间不执行第二次后台重启。该问题累计1轮失败，红绿夹具和只读诊断不另计业务验收轮。

Native准备：旧孤立GUI58489经CUA正常退出，新 `dev.py open --watch-ui` 正在按当前源码加载；窗口由PA独占，阅读session已确认仍blocked且不启动watcher。实际新界面证据待获取。

Native初次观察：MyGo dev完成调试编译，新GUI20222。CUA实际打开设置并展开项目：默认访问／采集／处理、覆盖与恢复、原未确认状态、配置准备入口可见；通用知识空列表和新增表单的分类／标题／正文／适用条件／来源可见，截图已实际查看。未通过UI改权限或保存正文；尚非PA-H整体验收。发现通用知识列表沿用“项目内容”小标题，记为待修正文案，未据此宣称全部视觉通过。当前界面停在未填写的新建通用知识表单，无未保存用户正文。

### 后续源码与真实只读／拒写观察

阶段源码与UI接线已提交 `c9c32dc`。其后补齐可选代码组管理契约：管理身份通过固定PUT code接口关联已拥有的官方组，不允许知识／通用组或其他项目代码组混用；项目访问变化同步可选组ACL，关闭时撤下Agent访问。原Native代码图开启入口复用该接口，只在实际ACL核对后发起索引；关联由控制登记持久化，不将自动目录写成静态配置。启动时只导入显式旧代码关联，不重跑索引。插件校验组UUID和已登记仓库根，不允许默认slug或远程／其他目录落入当前项目。新项目仍默认关闭，未知操作只显式reconcile。前述“可选代码组契约未实现”由此源码覆盖，但真实代码组启用／索引未验收。

39项Python项目检查、Go全量test、vet和race通过（最后通用处理状态小修后再定向核对）。固定产物07已生成并校验，候选配置指向07；活跃控制进程仍使用06和旧Python模块，尚未第二次重启。通用列表小标题源码已修正，watcher重载后的视觉复验待后续Native轮。

用原独立child对空通用组仅发一次合成拒写探针。普通remember后台模式返回200，后续官方日志为write PermissionDeniedError；管理身份只读核对文档数仍为0，原operation记为succeeded/background=true、tokens_in/out均为0。这证明官方“后台已启动”与“实际写入／处理”必须区分，不能把200直接当越权成功，也不能声称HTTP层403。最初探针未保留响应body，仅保留HTTP状态；已按原组、原操作和日志核对，未重投。属于验收方法观察，不新增PA-ACL01失败次数；回执 `.local/project-access-evidence/common-write-denied.json`。

据此修正桌面原结果核对：只有明确pipeline行终态可推进处理完成，background operation的succeeded不能代替处理完成；新增回归断言。正文存在与处理终态继续分别报告。没有因此重跑模型／摄入任务。

## 真实会话阶段

用户进一步明确“允许必要的 dev 重载”，后续在本session已交接窗口内进行必要重载，不需逐次重新索要相同授权；原库、主体和任务保护保持。第二次同库重载现为launcher31782（13:27:49）、completion31790、official31796、control32083，ready，产物07。导入既有代码关联仅记录配置；原项目unconfirmed时不自动重试旧ACL，保留原状态，随后由明确reconcile核对。

PA-ACL01 原主体显式reconcile通过：项目desired/applied revision4、通用desired/applied revision3均verified，既有代码组已关联，没有重建主体、内容或任务。原失败1轮保留，修复后本轮通过。回执 `.local/project-access-evidence/acl01-reconcile-original.json`。

新增只读策略复核：每次hook取policy前重新核对该主体有效ACL（含角色／tenant及可选代码组、通用组），外部撤权撤下本地缓存许可并增加代次，不补grant、不自动将unconfirmed恢复verified。源码40项检查通过。安装／代码组接线及格式整理尚待本阶段提交。

### PA-A 新仓真实采集与处理

- 创建本任务隔离宿主 `.local/pa-verification/host-a`，新仓 `repos/alpha`；保持日常Codex设置、现有会话和Cognee数据路径。OAuth文件只私有复制到隔离Codex home，未切换日常登录／Agent配置。此复制是运行依赖，完成前保留，收尾删除隔离副本。
- 管理接口准备原 `pa-verify-codex-a` 安装引用；新宿主经本机Codex官方marketplace/add实际安装，固定58文件及0600 marker再次核对。使用独立HOME／CODEX_HOME、无daemon；仅对已核对的固定hooks在本次测试调用允许运行，未修改用户全局hook信任。来源：[官方插件配置](https://developers.openai.com/plugins/build/plugins)、[官方隔离宿主验收示例](https://developers.openai.com/cookbook/examples/partners/harness_aware_plugin_evals/harness_aware_plugin_evals)。
- 真实会话 `01a1d363-82e9-7ae3-9818-8aafcf0a4c6b`，官方会话 `codex_01a1d363-82e9-7ae3-9818-8aafcf0a4c6b`。CLI退出0，实际创建TypeScript事件模块及README，不是合成transcript。SessionStart自动建立项目 `decffe38-e26d-5d6c-93dd-d578f130fd1a`、官方组 `f6fbfc52-ccc4-564d-a458-0ca7c55244fe`，verified、代码图默认关闭；首写前绑定，未手动预加项目。
- 官方原始会话和Data均出现，原自动整理操作树产生add/indexing/cognify/memify终态succeeded。各pipeline阶段不是擅自重跑；沿原操作树只读核对。知识内容质量及后续采用仍另验，不仅凭终态判质量。CLI事件／回执、原buffer与hook日志保留在隔离宿主及 `.local/pa-verification/alpha-session-a*`，不入Git。
- Native侧栏自动出现alpha，截图与AX实际观察，未写静态项目清单；原memlane仍在。并发同仓、同名分离、另一主体、限制继承／恢复与blocked处置尚待真实连续验收，不将本条扩大为PA-A–H全通过。

### PA-G 真实通用保存与 PA-CK02 核对缺陷

通过实际Native表单仅提交一次合成验收知识，含分类preference、来源“PA-G 本机合成验收”、适用条件限定隔离验收仓库，明确不是用户真实偏好。官方Data `a8e6c6d9-0c23-4d48-b0e1-d2a1d66c299b`，metadata分类／来源／node_set正确，raw正文核对；原operation `b0873e00-cef1-43d5-a8e1-a7421263284f`，cognify `071cc7e9-79d8-45b8-85e2-a65e45581615` 终态succeeded。回执 `.local/project-access-evidence/common-native-save.json`。

PA-CK02 累计失败1轮：Native核对误报未找到原文。已证官方正常文本摄入去掉末尾LF，客户端生成正文带LF，逐字相等失败；相同候选的两次只读核对属于同轮，未重复提交。修复仅忽略一个末尾LF，内部正文、空白及metadata仍精确核对，夹具改为实际无末尾LF回归。原文档及处理身份保留，未为修复再调用模型。Native源码重载后复验原结果待执行。

固定官方普通文档后台响应不保证提供原pipeline标识，不能安全地将同组并发活动猜成原处理结果。后续手动新建使用官方阻塞摄入（UI仍异步），以取得实际终态与pipeline回执，超时仍未知、不自动再投。已有后台操作按原身份只读核对，不重复保存。pipeline operation启动成功不能当处理完成的回归继续保留。

另一真实新会话正在同一隔离仓库验证实际采用：提示与已有源码不包含通用约定的唯一标记，不预先把期待答案放进仓库。回执 `.local/pa-verification/alpha-session-b.json`，原请求仍在进行，尚不判通过。整体Goal保持active。

后续采用结果：新会话 `01a1d367-ea8c-7860-9e59-e58d30e57102` 退出0，原提示／原仓库均不含唯一标记，实际生成的parseEvent四处错误消息使用该通用约定，同时保持原项目alpha标识与UTC约定。原hook日志仅有一次coding_block_delivered（864字符）；在既有入口内完成，未新增注入hook。此条真实采用通过；不能据此代替整个PA-G的修改／删除／撤权或冲突优先级场景。

PA-CK03 累计失败1轮：普通通用Markdown被旧session-learning前缀解析标为“原文格式未识别”，标题变成通用占位。只对有明确common metadata的文档增加呈现分支，复用原阅读缓存／组件，保留raw正文原值；不重做阅读UI。新增回归通过，实际Native再读同一文档后列表／详情标题正确，正文及来源可见、误报消失，本轮修复复验通过。未再次保存或调用模型。通用列表“项目内容”小标题也已在实际新窗口核对为“通用知识”。

当前剩余：第二独立主体与真实ACL矩阵、默认动态继承／覆盖／恢复、新建关联及并发分组、真实禁采游标恢复和blocked处置、通用编辑／删除及无权不注入、项目优先、空项目通用采用、完整Native连续场景与运行交接／清理。现有真实进展不缩小原Goal完成范围。


## 后续真实连续验收与当前候选（14:49）

此前“当前剩余”是当时状态，下列结果覆盖对应待验项，不覆盖未列出的完成边界。最后一次后台必要重载为13:50:46，launcher56615、completion56617、official56618、control56752；同库同原管理凭据，插件产物08。日常Agent配置未切换。原日常集成仍可能产生正常活动，不把本任务未主动重放旧任务描述为整库没有活动。

### 权限、默认与项目身份

- 第二独立官方主体B注册、有效ACL核对通过。真实none/read/maintain矩阵覆盖raw、session、搜索、图谱、保存；维护主体对本轮独立合成probe真实新增、读取、更新、删除通过，原知识未删。证据：私有 `real-acl-matrix.json`、`maintenance-crud.json`。官方禁止删除返回401 UnauthorizedDataAccessError，原夹具预期403误判保留并附contract_review，属于方法纠正，不算产品越权或新增验收轮。
- 默认改变不覆盖B的显式none；恢复继承采用当前read。原全局默认maintain/capture=true/auto_process=true已恢复。证据 `default-inheritance-real.json`。B在beta的维护／采集开启／自动处理关闭覆盖仍供本轮连续验证使用，收尾前应恢复原覆盖。
- PA-F真实控制端覆盖手动先建／自动先发现、两独立主体并发首次同仓、软链接与子目录、同名不同路径、worktree分离、移动后明确别名关联；单项目／单官方组及排除通用和代码组通过。证据 `real-project-identities.json`。这些是实际登记／ACL，不冒充额外真实模型会话。
- PA-NAME01累计失败1轮：Native两个same显示相同名称，无法可靠选择。改为最短可区分父目录后缀；搜索底层使用稳定项目ID，未知ID不回退全项目，选择回调捕获同一目录快照。真实Native侧栏／搜索已显示same · one/two；总览卡片同样已核对。设置接线同步使用同一标签，最后设置视觉尚待覆盖。旧失败不清零。
- 手动创建来源错误为discovered属于登记来源缺陷；源码改为manual／associated且自动再发现保留来源，42项检查包含回归。当前后台仍载入改动前模块，新的真实手动来源核对待必要重载后进行，不用旧结果当通过。

### 真实禁采／恢复及待发材料

- Beta空新仓在只读策略下真实编码成功，并采用更新后的通用PA_NOTE_99；官方该仓文档0、session404、pipeline0，插件仅保留stat游标和paused状态，无会话正文入buffer。证据 `readonly-beta-capture.json`。
- 自动采集关闭但有维护权限时，主动保存独立合成probe成功；只读时同一生产helper明确拒绝写入，中文错误正确。证据 `manual-save-capture-off.json`。采集开关与主动保存权限分开。
- 原Beta会话显式恢复采集、自动处理保持关闭，真实resume两轮正常退出。官方仅出现恢复后新内容，禁用期间原提示不在原始记录；pipeline集合与之前相同，没有新自动处理。证据 `resume-capture-real.json`。未回扫禁用区间。
- 在最后权限检查后、真实POST前撤写，官方实际403。原合法buffer保留并blocked；恢复ACL及越过SDK退避后仍不自动重发。实际Native先核对再点击明确补交，原问题只写入一次、pending归零。证据 `blocked-buffer-real.json`（explicit_native_replay_verified）。
- 网络未知使用本轮独立loopback RST，仅截断合成请求，未停止共享服务。未知journal保留，自动drain及显式replay均不重发。核对signature后通过生产CLI明确丢弃未知entry及新增合法tail共2条，原已补交材料仍在。证据 `unknown-buffer-real.json`（explicit_cli_discard_verified）；不宣称实际点击Native丢弃按钮。上述不重跑模型。

### 通用知识与实际采用

- 实际Native编辑原合成通用文档，将独有标记42改为99，保持原Data ID、分类及来源；原PATCH pipeline 46f36dc3-8e12-460e-b285-dde2bb14a8ea完成，UI核对显示官方正文已核对／处理完成。PA-CK02原失败1轮保留，实际修复复验通过。
- Beta空项目真实新会话采用99；Alpha后续真实会话同一次coding注入同时包含项目42和通用99，实际生成代码保留项目42，项目优先通过。原提示未携带期待标记，不新增第二注入hook；证据在隔离宿主alpha-session-c／beta-session-a事件及回执。
- 同一生产build_block进程先可读99，实际撤B通用read后直接raw403、下一次注入为空；明确reconcile后恢复99。证据 `common-revoked-no-injection.json`。不声称能撤回旧模型上下文。通用删除后的撤下仍待真实验证。

### 当前补丁与源码检查

产物08补齐官方forget/index shell门禁；上游shell曾吞掉配置异常并落回继承导出／owner缓存，现四个shell在配置失败时停止，forget另核对read/delete。带继承owner和缓存owner的夹具确认不能发请求；没有修改日常已安装缓存。固定上游57文件、产物58文件，现修改14个官方脚本，摘要随产物更新。当前隔离宿主A仍用07，B用08，路径及私有引用均保留，不在原目录就地替换。

最新 `sh scripts/go.sh test ./...`、`vet ./...`通过；`scripts/task.py -- .local/cognee-python/bin/python -m unittest discover -s engine/python -p test_project*.py` 42项通过。Go新同名回归验证不同dataset的选择范围不合并。aiohttp NotAppKeyWarning是现有建议警告，检查结果成功。

### PA-WIN01：关窗阅读状态丢失（累计失败1轮）

验收目标：关窗不影响后台登记，重开保留知识／来源阅读。真实Native先选alpha知识再进入原始来源，点击关闭后watcher日志为Memlane Dev quit；后续工具自动激活bundle缺少MEMLANE_DEV_RUNTIME_CONFIG，显示配置空路径。后台56615/56617/56618/56752保持，未丢材料。之前未知原因的watcher退出仅保留未知，不归因本缺陷，不额外计轮。

已证MyGo 0.7.4默认最后窗口关闭后Quit；原OnActivate只对旧窗口Show，无法保留已销毁阅读树。生产入口改为普通OnClose PreventDefault+Hide；OnBeforeQuit标记明确退出，确保CmdQ和源码重载仍可结束。未改依赖库。经统一dev open恢复配置实例后，实际关闭／激活保留同一PID5606、原来源正文及返回阅读；返回后仍是原知识。再CmdW隐藏期间，由真实控制端首次登记background-window，重开自动出现且原知识保持。私有 `native-background-discovery.json` passed；截图已实际查看。此轮修复复验通过，原失败1轮保留，与旧E22进程组EPERM问题不同。长正文滚动位置和焦点仍不据这个短正文样本宣称全覆盖。

### 未完成边界

整体Goal仍active。尚需日常CLI／Desktop实际受管接入与旧凭据失效、正常安装／启用入口及明确凭据排他交接；隔离A/B不代替日常接入。另需通用删除、最新手动来源真实核对、Native项目设置连续操作及撤权阅读边界、完整PA14核对、最终文档／测试与本轮物料收尾。无问题达到第4次失败停止阈值；不得为补记录重跑既有模型或索引。


### 通用删除与最后检查补充

在真实采用／优先级／撤权均有原回执后，仅删除本轮自建的合成通用Data a8e6c6d9-0c23-4d48-b0e1-d2a1d66c299b。使用与生产Client相同的官方DELETE端点（不是Native确认删除按钮），删除前核对原Data正文99及PA-G合成来源。官方receipt success、dataRemaining=false、datasetDeleted=false；随后raw404，同一生产注入进程不再出现99，实际Native通用列表0且无旧正文。证据 `common-delete-real.json` passed，model_calls=0。保留原新增／编辑／采用证据，不再为截图重建该知识。通用组仍在。

最后Go受影响包race通过、diff --check通过；上述测试及真实删除不重置任何问题失败次数。当前使用本机工具链gofmt；系统PATH没有gofmt属于命令准备观察，不算产品失败。


### 日常接入缺口的依赖安排

当前prepare仅产出插件目录和私有env，没有供正常宿主安装的marketplace，也没有Native登记新主体入口。先补可安装目录与稳定实例登记，再做激活预览／配置基线核对和旧消费者盘点；最后在明确凭据排他窗口执行日常启用。CLI／Desktop共享同一Codex home时按一个配置实例管理，不能凭品牌拆成两个可隔离主体；隔离HOME验收不证明该日常边界。旧owner不得直接撤销（仍被管理服务和旧消费者使用），应明确退出受管Agent配置，并核对旧采集入口与缓存，不清空旧buffer或重放旧会话。每一层保留准备／加载／真实使用状态，不以复制配置标为连接成功。

本轮继续使用OpenAI Docs与现有影响分析方法。已读取当前本机plugin help，并核对[官方本地marketplace与配置说明](https://developers.openai.com/plugins/build/plugins)。该文档列明CLI与Desktop可共享本地插件设置，实际本机版本行为仍需分别拿加载回执。下一步生成独立受管marketplace，不写用户全局配置；此准备不需要重复申请已授权的源码变更许可。


安装准备源码现生成 `managed-marketplaces/<instance>/<generation>/plugins/cognee` 及相对路径catalog，返回marketplace_root/name供后续宿主管理器使用。marketplace名称同时绑定实例与产物代次，旧受管目录与已加载cache不覆盖；catalog没有key，私有env／marker继续0600。新增回归核对实际catalog源路径、不同实例不同命名及旧marker保留；Python42项、Go受影响包检查通过。现有A/B安装回执仍保留其原路径，后续不能把生成catalog描述为日常宿主已安装／已加载。后台尚未加载本prepare源码；不为补文档而立即重载。


## 本机接入流程实施与准备（15:09）

新增管理侧CodexConnection和Native「Agent连接」：稳定ID由实际Codex home推导，CLI／Desktop共享配置时只建一个主体；准备先保留原创建尝试，未知不能创建替身。生产API只接受expected_revision及已审阅proposed_sha，不允许请求指定任意可执行文件或用户配置路径。Agent token调用准备／启用／核对均403。

准备调用原生Codex plugin marketplace/add在私有临时home实际安装，不复制OAuth、不调用模型，不修改日常配置。独立marketplace按实例与代次分隔；全部固定文件和安装marker核对。tomlkit编辑只改变受管marketplace、Cognee入口及必须的hooks开关，保留模型、登录、其他插件和注释。原配置／拟用配置0600保存、摘要绑定预览；启用前核对两次原配置、缓存和旧采集进程，先journal未知再写缓存／原子配置。未知结果明确review原配置和原缓存，不重装、不自动恢复owner或重发材料。启用完成只表示configured_pending_new_session，不冒充真实加载。

### PA-CONN01：旧消费者检查跨配置目录误阻塞（累计失败1轮）

实际安装器首次在无登录／无有效官方凭据的合成profile完成安装，启用被其他profile的旧watcher误阻塞。原回执 `codex-connection-installer.json` 保留original_round。已证进程扫描没有限定Codex home，已改为只匹配当前配置目录cache并补异profile／同profile回归。沿原prepared状态、原产物继续启用成功，没有重复调用安装器、没有调用模型。该证据只证明实际安装器与合成连接配置，不冒充真实独立主体或日常加载。系统/bin/true不存在的初始单测夹具及手写preview缺字段是准备／夹具观察，不另计产品验收。

### 后台和真实本机准备

按用户“允许必要dev重载”加载新接口前，观察273条活动、109个不同pipeline的最新状态：86 completed、23 errored，未观察到非终态。该范围不证明所有进程均空闲；没有重放这些任务。核对旧launcher56615启动时间／锁后，统一dev stop确认owned groups退出和原库释放；新launcher26730于15:00:07同库启动，completion26732、official26741，随后control就绪。日常Agent配置及原主体未切换。

只提交一次生产 `/v1/codex/prepare`：本机稳定实例codex-2564787a0a96464c49ccf335真实官方主体已注册，默认项目／通用权限按现有控制核对。固定产物08由实际安装器成功准备，revision35；原本机config摘要保持ad6ae9c4476917b38d3ef67a1f4b70f7731d7ecd2e42a5ab6d01786b9222a0db，拟用摘要ff291ac4e51d227ebad61a221b1ba1f7d817967c3d713b1b2ce4bfd3fca9beef。将停用cognee@cognee-local、启用cognee@memlane-16e71bd70ad5-f36cad920703147fb9d4。私有回执 `daily-codex-connection-prepare.json` prepared/configuration_unchanged=true/model_calls=0；不是已启用。

真实旧cache仍有idle watcher8770、exit watcher56700及64233，均核对到同一Codex home；启动时间分别14:48:13、09:09:16、12:33:08。未向它们发信号、未结束编码会话。Native实际显示预览及旧采集仍在运行，启用按钮disabled；截图已打开。后续凭据排他交接按附件第4节单独取得，之前后台交接不覆盖该切换。

进一步补齐trusted项目级配置检查：项目独立启用旧插件可覆盖用户级disable，激活前再次读取已登记trusted项目的.codex/config.toml并阻止此冲突；不偷偷修改项目设置。该后续守卫源码已测试，当前后台仍是本轮重载时版本，最终启用前必须加载。正常信任提示不通过全局bypass处理。

### PA-DIR01：目录恢复后旧错误提示残留（累计失败1轮）

Native后台恢复后目录和策略均已载入，但上方仍显示此前服务重载期间的“项目目录暂时不可用”。已证目录GET错误与未知保存结果共用Message，成功GET未清理。分为DirectoryError和操作Message：成功目录读取只清目录错误，未知保存结果保留。回归模拟503→200并验证原保存待核对消息不丢；Native新窗口已无错误残留。原失败1轮保留。

### Native项目设置连续操作

真实Native创建自有空仓native-manual，显示名PA Native manual；官方来源manual、verified、代码图false，项目ID6fb6103c-998e-5079-bb66-6362b2be7697。对A实际选择自动采集关闭并保存，当前规则显示关闭且其他字段继承；再实际点击恢复全部默认，规则回到采集开启／继承默认。官方最终overrides[A]={}、revision38；证据 `native-project-settings.json` passed/model_calls=0。同名设置卡片same · one/two已在实际AX核对。没有通过本场景改原memlane/item-fe内容。

本轮新增检查50项Python、Go全量test、vet通过；受影响race在最后小修前通过，最终集中检查仍按最后候选执行。整体Goal保持active，正常日常CLI／Desktop加载、旧消费者／旧入口退出、PA14与最终清理／交付边界尚未完成。


最终候选补充：连接“新会话回执”只统计本次启用后新增的host绑定，旧绑定不充当当前配置加载证据；未知原配置已提交时review仅核对原缓存和原配置。50项Python全量、Go全量、vet、受影响包race通过，最后计数小修定向8项通过。PA-CONN01原失败1轮保留，最后fixture补齐enabled_plugin字段不算新的真实验收轮次。日常预览仍未启用，旧watcher未停止。


## 用户已交接日常连接；CLI真实接入已通过（15:30）

用户明确答复“交接并启用”，授权停止同一旧插件采集watcher、必要宿主重载及新CLI／Desktop会话验证；原库、管理owner、登录、其他插件及旧会话／材料保护保持。没有重复询问后台许可。

### 实际启用及旧材料保护

按PID及内核启动时间核对后，仅向旧idle8770、exit56700/64233发送SIGTERM，编码父进程未收到信号。确认旧消费者0后记录12个已存在bridge/pending文件摘要。启用前再次发现同一旧插件idle40002（15:11:23）已被旧宿主重新建立，首次预检因此拒绝，尚未提交activate；这不是未知写入后重投。按同一授权核对并停止该进程，在同一受控执行中重新确认无旧进程，再按原预览只提交一次activate。

实际回执 `daily-codex-connection-activation.json`：revision39、configured_pending_new_session、当前配置摘要ff291ac4e51d227ebad61a221b1ba1f7d817967c3d713b1b2ce4bfd3fca9beef与原预览一致，旧入口disabled，新入口enabled。没有修改owner凭据或登录。后来CLI正常保存folder trust使config加入该测试仓的trusted条目，不能因此倒推激活失败；后续核对只读检查受管入口／缓存，保留这些无关用户配置变化，启用前仍严格核对完整基线。

两次同库dev重载均核对原owner／数据锁，统一stop返回groups退出和原库释放；最后一次加载home守卫修复后才启用。原活动快照273条／109个最新pipeline均终态，未重放旧任务。该观察不表示所有可能在途调用均取消，也不把正常旧宿主活动推断为零。

旧12个文件在启用及真实CLI之后摘要保持。原owner key仍用于管理连接；实际新cache loader在故意继承原owner环境key时仍选本实例child，并通过官方users/me确认子主体身份，见 `daily-installed-credential-pin.json` passed，未调用模型。证据证明受管入口不落回owner，不声称管理owner key本身已被撤销。

### PA-CONN02／03 与核对语义

PA-CONN02累计失败1轮：启用前源码守卫把trusted用户主目录的 `.codex/config.toml` 当成另一份项目级覆盖，实际就是正在替换的主配置。按真实路径跳过同一文件，独立项目覆盖仍拦截，9项定向回归通过后沿原预览继续，没有修改预览范围。

PA-CONN03累计失败1轮：CLI接入后目录接口把新代次的正常managed watcher也列为legacy。改为只匹配预览中实际被停用的旧plugin/cache namespace；其他profile和当前新代次不误判，仍保留同profile旧入口守卫。该修复及正常folder-trust后只读核对的回归已在源码通过；当前控制进程尚未加载这组最后状态守卫，待普通Desktop验证结束后再集中重载，避免中断正在等待的新会话。

### 日常CLI：没有trust bypass的真实闭环

原生Codex0.167.2，日常HOME／CODEX_HOME，在新空仓daily-cli交互启动。按正常界面先信任自建空仓，再逐项核对并信任本次固定Cognee的9个hook；另外4个待审阅的非Cognee hook保持原状态，没有Trust all或dangerously-bypass-hook-trust。第一空会话仅做信任准备；随后正常/new在当前工作树建立新会话，未重新运行旧任务。

真实会话01a1d3c3-767b-74d0-b64a-c87c6729df96实现src/slug.ts与README，实际node检查成功，CLI正常退出0。新主体首次prompt自动登记项目e7bec60d-3c04-544b-a3f6-cffc717ffea2、dataset933effd1-de17-550c-8c8a-fbc9390de5f7，verified、code_enabled=false；官方原始会话qas1/traces6。官方正常流水线沿原操作树观察从started到全部completed，未为核对重投模型／索引。文档3项中有一条session_learnings；已实际读原文，事实为formatSlug位于src/slug.ts、README有例子、未新增依赖或配置，与实际改动一致。不声称该条覆盖所有函数边界。证据 `daily-cli-session.json` passed，模型使用本机既有gpt-6.1-sol设置。

### Desktop宿主部分证据与正常用户问答缺口

CUA明确拒绝操作com.openai.codex（安全限制），所以没有实际点击其发送按钮。按用户已授权的新Desktop会话，通过已连接任务接口创建只读任务01a1d3c7-5efe-7c22-88d6-5b1ec6a66dcf，继承实际cwd=/Users/bytedance/work/AI/memlane；任务已completed，只读合成slug两个文件且答复合同，无文件改动。真实SessionStart经同一独立主体绑定原memlane项目510a31bd-650a-584a-9719-0dcd28880d13，官方原始session存在，traces2，证明宿主hook／cwd／主体接入。

首次检查按普通用户问答预期qas1，实际qas0。检查该新rollout结构后确认该工具创建的是Agent委派任务：FunctionCallOutput，没有UserMessage。此方法不能代替桌面输入框普通用户消息，原失败断言和qas0保留为方法观察，不给普通桌面采集判通过，也不为凑证据修改官方parser来冒充用户来源。私有 `daily-desktop-session.json` 标记delegated_host_verified_normal_user_qa_unproven。已请求用户只在daily-cli仓的Desktop新对话发送一个只读提示；回答待到，不能按超时当已发送。此前连接授权继续有效。

### PA14与收尾

实际服务openapi快照只有POST /api/v1/improve，没有cancel路径；固定源码improve的detached任务保留终态记录。产品明确“关闭只停止后续触发，不取消已有任务”，插件auto/idle/final/exit/coding_cooldown夹具均在网络前暂停，Beta真实auto_process=false后原pipeline集合不变；已有部分写入及unknown／blocked材料保留证据已存在。没有调用不存在的取消接口、没有把断开请求或开关关闭写成已取消；不为补运行证据再跑模型。

两份自建隔离宿主A/B OAuth副本已删除，原日常登录未动，其余受引用安装／buffer证据保留。普通Desktop验证与最终资源／官方合成数据白名单收尾仍未完成。

最后Native观察：此前新建／覆盖／恢复、关闭重开／来源返回均有实际成功证据。此后CUA连续报cgWindowNotFound，同时App进程与watcher存活；只对核对了身份的自有前台UI watcher做一次正常重载，服务保持，新的UI进程仍被观察工具报相同错误，浏览器inventory另报access host unavailable。原因未知，不把它认定为App退出／配置丢失，不继续盲重载。最新daily-cli知识的实际窗口截图未取得，留待工具窗口观察恢复；不得把HTTP内容检查扩大成最新视觉通过。

当前源码52项Python全量通过，Go源在此后未改，已有全量／vet／race证据沿用。公开去正文的阶段回执在 `evidence/project-access-20261113/progress-summary.json`。整体Goal仍active，未向用户宣称整体验收完成。


## 逐项收口审计（当前仍未完成）

本轮重新读取完整PA规格与技术方案，对照当前源码及已有真实回执，没有把“用户尚未回复”视作已经发送，也没有因等待重投模型或索引。当前线程列表没有新的daily-cli普通桌面对话，后台39752（15:11:12）仍ready。

| 要求 | 已有直接证据 | 尚未证明／收尾边界 |
| --- | --- | --- |
| PA01 | daily-cli真实新会话首次绑定、原始qas1、原流水线终态与实际知识正文；Alpha后续会话实际采用 | 普通Desktop用户消息待发送；最新daily-cli Native截图未取得 |
| PA02 | 实际默认动态继承／显式none保留／恢复null；Native项目采集覆盖保存及恢复 | Native默认模板按钮成功路径未单独补验，源码交互／真实控制API已有证据 |
| PA03 | 两独立主体none/read/maintain直连官方矩阵、maintenance CRUD、有效角色／tenant核对 | 本轮补齐PATCH的none/read拒绝，见下；旧401断言纠正保留 |
| PA04 | Beta真实只读编码无正文入队；stat-only水位与原会话恢复只收新内容 | 普通Desktop同一来源形态的完整问答仍待前置发送 |
| PA05 | auto/idle/final/exit/cooldown生产函数门禁断言；Beta真实关闭后pipeline集合不增 | 不据此承诺服务禁止所有主动处理 |
| PA06 | 生产remember helper真实只读拒绝且中文原因正确；capture=false但有权主动保存成功 | 无新增未证明项 |
| PA07 | 真实403竞态→blocked→权限恢复不重试→Native明确补交一次；RST未知→不重发→明确CLI丢弃 | Native丢弃按钮未实际点击，不以CLI回执冒充；生产helper签名路径同一实现 |
| PA08 | Native新建manual及自动登记共用身份；新增真实已有组导入ACL及重复关联测试通过 | 已有组选择器的Native成功路径未单独补验 |
| PA09 | Alpha自动出现在Native；隐藏窗口期间后台登记、重开补发现；目录失败保留与恢复错误清理回归 | 最近日常项目Native截图受cgWindowNotFound阻断 |
| PA10 | Native普通通用文档新增／编辑、同DataID及真实pipeline核对；实际删除raw404／列表0；真实后续采用 | 删除使用官方API后Native核对，未冒充Native删除确认按钮 |
| PA11 | 空Beta真实使用通用99；Alpha同一注入块含项目42与通用99、实际保留项目42；真实撤权后缓存不注入 | 不外推其他模型或全部偏好质量 |
| PA12 | 真实并发两主体单组、同名分离、软链接／子目录、worktree、移动别名；目录排除已登记code/common | 当前受管模式关闭官方companion创建，不把未启用的伴随能力说成已交付 |
| PA13 | 关窗/重开/来源返回实际Native通过；真实Agent撤权拒绝及同进程缓存撤下；Native读取失效403回归与实际通用删除撤下 | 桌面管理owner仍有读取权，不能把Agent撤权描述为owner失权；最新窗口观察待恢复 |
| PA14 | 实际OpenAPI无cancel路由；开关只停后续触发的界面和协议；真实已接收流水线按原ID跟踪终态；partial/unknown保留 | 不宣称关闭开关取消已运行任务或撤回已发送字节 |

PA-A至H的主证据分别为Alpha/daily-cli真实链、Beta禁采/恢复、动态模板与新主体、真实ACL/CRUD、真实403/RST与原操作恢复、身份/新建关联、通用CRUD/采用/优先级、Native生命周期/来源返回。尚未关闭的用户来源、Native观察和清理项继续阻止整组Goal完成。本文是证据审计，不是用户验收通过报告。

### 本轮补齐两项实际接口断言

1. 自建空组82af692a-aa13-5fde-a8dc-42713a61acdd在关联前只给A与本机主体read、B none。生产create关联后source=associated、verified，三主体覆盖分别read/none/read；逐主体自动resolve仍是同一项目与组。再显式关联相同root+dataset不增加revision、不重置覆盖，官方组仍1个。回执 `existing-association-real.json` passed；模型／索引调用均0。使用本轮自有空组，不改原日常项目。
2. 既有真实矩阵尚未单独验证PATCH拒绝，本轮对本任务Beta合成probe466f6511-95de-4f77-8b79-e75d3254f1f3提交相同原文，none/read两种权限均直接403，最终原文摘要一致，B原覆盖完整恢复。回执 `update-denied-real.json` passed；没有触发处理或调用模型。维护权限下真实更新已由原maintenance-crud验证，不重复做。

删除了实际安装器的自有无登录合成fixture目录connection-installer：191文件、逻辑3771707字节／分配4169728字节。删除前确认实际runtime/registry无引用、进程参数无引用；保留原小型通过回执，没有删除当前日常marketplace/cache或任何原库。A/B官方主体、其余合成组和被引用材料仍保留，最终白名单清理尚待收口。


## 阻塞审计与运行保留

连续三轮均遇到同一外部前置：普通Desktop用户消息尚未发送，CUA不允许操作Codex窗口，且Memlane窗口观察持续cgWindowNotFound／browser access host unavailable。第一轮已完成实际日常CLI及源码守卫并请求手动发送；第二轮完成已有组ACL导入、PATCH拒绝和安全临时fixture清理，前置仍未发生；本轮重新读取附件、查最近12个线程及窗口观察服务，仍只有CLI测试和委派任务，没有新的daily-cli普通用户对话。不是将无答复当成已发送，也不以后台ready冒充验证通过。

当前可独立完成的必要接口检查、证据审计与安全清理已完成。最终普通Desktop QA、受影响Native交互／窗口观察及相应收口仍依赖用户动作／观察服务恢复；不会为保持active而重复模型、索引、接口绿例或盲重载。按Goal阻塞门槛标记blocked，不标记complete，不缩减PA01–PA14／PA-A–H。

运行保留：已核对launcher39752启动时间15:11:12及原配置，phase=ready，completion39754、official39763、control40046；它们和当前日常受管连接保持，不因Goal等待而停止。最后状态守卫源码已提交并通过检查，运行加载仍按待办保留，普通Desktop验证完成后再集中处理。当前实际库、owner身份、登录、旧材料及仍被验证引用的A/B安装／合成组继续保护；不在验收前清空这些依赖。工作树内design-catalog及visual-refinement-selection等非本任务改动未提交或修改。

恢复入口：用户在daily-cli项目的Codex桌面普通新对话发送只读slug提示并回报已发送后，读取该原线程与官方session，不创建替代任务或重投原提示；观察工具恢复后继续Native待验。复核原任务终态、加载最后守卫、白名单收尾、更新完整交付及资源交接后，才再执行完成审计。
