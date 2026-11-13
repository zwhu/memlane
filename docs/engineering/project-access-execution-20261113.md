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
