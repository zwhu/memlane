# A/B 真实开发与本机交付记录

2026-11-09 开始。本资料记录工程计划、决定和新证据，不保存 Goal prompt。最终完成条件仍以本轮用户指令与 AGENTS.md 为准；未通过项不因阶段提交而关闭。

## 基线与并行修改

开始分支 `main`，HEAD `ba96a54268220aa86cb7a3f00b6fccfb5c5d6d4d`；远端 main `2c1c681bfce4ba54589738589a1b4ae031deb376`，本地领先 7 个提交。实际工作区不干净：AGENTS.md、产品、设计、重设计入口、页面复用已有修改，另有未跟踪的 Agent 设置入口文档。保留这些用户修改，只独立提交本轮文件。原始状态及散列见 `build/ui-delivery-20261109-001/baseline.json`。

2026-11-09 执行期间，并行设计改动独立提交为 `d7956789b439e61fbe8fba57a48ffbbab3057ca6`（Agent 连接归入设置、导入归入项目）。已重新核对 HEAD/工作区；本轮继续接在该提交之后，未覆盖其文件。

随后并行设计继续提交 `e688060`：用户原话「这里没必要导入相关知识，直接给个按钮，让用户自己选择使用自己的Agent去分析知识存入」。新项目入口只复制带当前项目的指令，不设计 App 文件列表/导入向导/分析调度；保存和索引只读实际回执，正常 Agent 保存不套用旧 imported 批次口径。旧 P3-I 代码与历史统计保留。这条最新已确认规则叠加固定 A/B，不据此移植完整 C 原型。

A 固定 `e27a49e35020972d6aea4ab070ec898fb6c9c22e` revision 13，B 固定 `ed6cd37858c5d6f56f9307ecbf6ac454bb6e1edb` revision 5。已实际打开 home.jpg、memory-detail.jpg、A 环境/三步/批次部分失败/窄窗口/Provider失败/Magpie失败/环境设置窄窗口，以及 B 正常/窄窗口/日期选择/运行/完成截图。Agent 入口采用当前用户已确认的「设置 → Agent 连接」补充，C 完整稿不移植。

开始 handoff 已生成并核对非空：`/var/folders/nv/g6m5_3nx1r9_v9dk3zh_5zw40000gn/T/memlane-p3-handoffs/20261109T053201.561040Z-redesign-start.md`。

## 所有者与实施顺序

| 顺序 | 现有可复用能力 | 本轮补齐 | 必须重新取证 |
| --- | --- | --- | --- |
| 1 pi/DeepSeek | pi_completion、RPC 终态/guard、私有模型桥、官方采集及当前知识工具 | DeepSeek 完整 Schema 请求绑定、原始 Provider/认证复用验证、事实质量集 | 真实推理、Schema、活动取消、无工具/无采集、同包官方任务/出处/编辑/新会话 |
| 2 共享配置与迁移 | SQLite 单服务所有者、持久任务/attempt/租约/恢复、官方配置文件 | 草稿/候选/三份保存/不可变有效修订、验证指纹、任务冻结、旧授权迁移 | WAL 迁移、同模式保存不改当前、运行中切换、重启、失效与迟到 |
| 3 A 承载与环境 | Native UI、组件 Manager、统一 Cognee/BGE 与 Rust/E5 准备 | 三步与完成/推迟持久状态、共享设置、同页状态、普通包准备入口 | 空根、真实准备/失败/取消/复用/重开、普通/窄 Native 截图 |
| 4 电脑级批量连接 | 稳定连接 ID/代次、逐 Agent 增量配置、接入验证 | 各实际版本的电脑级格式、无项目回执、多选批次与逐项恢复 | 安装/重载/独立回执、部分成功、仅失败重试、取消与迟到、配置保留 |
| 5 三种处理后端 | 已有 pi 桥与官方模型调用 | 优先 Provider、Codex app-server、Magpie，本机规范目录、统一任务分派 | 各自推理/完整 Schema/隔离/不可用/超时/取消，不借 pi 或旧报告证明其他后端 |
| 6 B 首页 | activities 独立创建/导入事件、知识/原文列表、建议、计划/任务 | 真实累计与可信首日、全年日期交互/导航上下文、有效配置摘要、真实按钮状态 | 去重、导入单列、编辑/恢复不新增、未知与可信缓存、确认和计划定位、窗口 |
| 7 故障与完整包 | build-client UI/helper/CLI/LaunchAgent、私有合成根 | B02 停止断线、B03 请求上限与健康传播、R01 普通入口 | 新 worker/新完整包、跨限实际检索/停止、活动回收、普通入口与最终清理/三类体积 |

运行中网络/模型调用不持 SQLite 事务；UI 不成为数据库或执行任务所有者。共享配置必须进入 Cognee 与正式整理/计划分派，不能只修改 Cognee 环境变量。来源文案和执行报告取实际执行者，不保留写死的 traex 标签。

## 未决问题与真实条件

用户原话：「不明确的地方必须提出来，不要为了完成而完成。」以下问题已向用户提出，回复到达后逐字记入决定；依赖决定的行为暂不实施。

| ID | 证据与问题 | 推荐/取舍 | 状态与影响 |
| --- | --- | --- | --- |
| Q01 | ScheduleSpec 固定 executor=traex，daemon RunOrganization 与模型策略仍走 traex；全局配置不能静默继承旧收费授权 | 保留原计划、范围、预算和启停意图，已有运行用原快照；旧计划后续标需确认执行者。备选保留旧 traex 路径，则全局当前配置仅作用于新计划 | 等待用户；阻止旧计划迁移分派，不阻止 pi 取证与配置结构实现 |
| Q02 | pi auth.json 有 DeepSeek api_key，但不能复制到独立 Provider | 由用户指定独立供应商及安全存储引用，只让 Agent 读取自己的认证 | 等待独立 Provider 条件；不阻止 pi 推理 |
| Q03 | activities 已有 created/imported，当前无受信首次使用字段；不能用文件或最早知识时间补造首日。旧原文身份覆盖需逐表核对 | 缺失字段保持未知；可由用户明确确认日期并记录来源 | 等待迁移口径确认；新空根可以建立受信首日 |
| E01 | pi 0.84.1，默认 deepseek/deepseek-flash，模型定义 DeepSeek V4.1 Flash (1017)；原认证实际复用，隔离Responses完整Schema/流式取消通过 | 原配置不变，认证不转存；同包官方任务另取证 | 独立桥通过，第四包在验证；不以单请求代替同包和质量 |
| E02 | Codex PATH 0.147.0；历史官方采集用独立 0.167.0-alpha.3.2 | 重新核对实际采集可用通道与 app-server schema，分别记录版本 | 未验证；新模型桥不继承采集结果 |
| E03 | 本机 127.0.0.1:3425/v1/models 当前连接失败 | 先查实际可用安装与公共接口，不读取 Magpie 私有凭据；无真实服务条件则保留未通过 | 未验证；Magpie 成功/隔离/取消不能用 HTTP 夹具替代 |
| E04 | 各 Agent 当前 config.go 仍有项目配置门禁，pi 用指定配置目录 | 逐版本查用户级接口和无项目回执，未支持禁用 | 待调查；无项目连接身份不算电脑级安装 |
| E05 | 固定六样本第三轮逐项通过；第一、二轮失败保留；尚无本轮Cognee初始知识 | 同材料修复复验，来源/实体/结论/范围/冲突单列 | 固定样本通过；官方初始质量待验，不外推全部题材 |

## 验收矩阵

本轮 pi 新证据、三轮质量失败与修复、完整包环境503阻塞单列于 [pi/DeepSeek 验收](../pi-deepseek-validation.md)。独立技术通过不关闭同包链路或A/B。

所有新证据根为 `build/ui-delivery-20261109-001/`；每次失败/修复使用独立子目录。真实模型、真实进程、故障注入与未验证场景分别标识，不复写旧失败。

| 条目 | 当前状态 | 新证据要求 |
| --- | --- | --- |
| pi 原配置/认证、完整 Schema、流式取消/终态/回收/迟到 | 独立桥与第五包取消/重试通过 | pi-responses-001报告；同包受控推理不能冒称官方材料 |
| pi 事实质量与官方同包闭环 | 第五包同根原材料/完整出处/Rust/App v2/新会话通过，两条初始内容和六固定样本逐项通过 | pi-package-005/runtime-continuation-005/report.json及initial-quality-itemwise-review.json；保留原失败，限制题材/稳定性/知识覆盖 |
| A V01–V02 环境与三步 | 未实现新稿 | 空根、失败/取消/重开/兼容复用、真实下载字节与占用 |
| A V03–V05 批量电脑级连接 | 未实现新稿 | 默认空、逐项状态与稳定代次、部分成功/重载/只失败重试/迟到 |
| A V06 Provider | 未实现 | 公共协议/目录证据、独立配置复用、地址变更凭据门禁、实际推理 |
| A V07–V09 配置/启用/失效 | schema17账本、daemon真实pi验证，以及设置页pi保存／活动取消／重验／明确启用通过；三方式完整保存与正式任务冻结仍未接 | Provider/Agent/Magpie三份保存、运行快照、非pi失败/取消/迟到；单后端UI不关闭整项 |
| A V10 pi/Codex，V11 Magpie | pi桥/同包及设置页真实pi子路径通过；Codex/Magpie仍不可用并如实显示 | Codex/Magpie各自完整 Schema/真实隔离/超时/取消、实际模型及路由 |
| A V12–V14 设置/窗口/完整链路 | 设置页pi普通窗口与生产格式包通过；窄窗口仅MyGo测试，初始化复用/完整恢复/日常任务未通过 | 重启后的实际页面恢复、窄窗口系统截图、长错误、初始化承载与完整日常任务 |
| B 总览统计与日期 | 未实现新稿 | 原文身份去重/有效知识/首日证据、独立活动/导入、零与未来、选择/返回保持 |
| B 建议/计划/整理/动效/缓存 | 未实现新稿 | 实际任务驱动、不重复启动、审核前不变、主动停止保留、可信缓存时间、减少动态 |
| B02/B03/R01 | B02断线回执根因修复，真实Rust三根通过；最终活动停止、B03/R01仍未关闭 | 原失败与修复报告并存；新归档/包、断线活动停止与跨限检索、普通入口 |
| 全仓检查与最终包 | pi/组件/停止test、vet、race及语法/diff通过；最终A/B包待验 | 当前证据不代替后续修改的检查、Native窗口和三类体积 |

阶段完成后独立审查、修复、验证、中文记录、提交、生成实际临时 handoff，再继续。正式签名发布、全量旧工程、C–O 未认可重设计、远程管理不纳入本轮完成声明。

## 当前检查点

pi 独立 Schema/取消与固定样本切片已提交 `b77517db7b7c3cf5f264db4035edf632588b12eb`，未推送。每个失败保留；同包官方采集因空环境 Cognee 503 未进入任务。详见 [pi 新验证](../pi-deepseek-validation.md)。

正常准备的最小反馈命令已实际运行并返回503：

```sh
python3 scripts/verify-packaged-cognee-health.py \
  --pointer build/ui-delivery-20261109-001/pi-package-001/fixture-path.txt \
  --out build/ui-delivery-20261109-001/health-minimal-002
```

新合成根、仅复用已核对组件文件；单次完整健康请求 43.42 秒返回 `not ready/unhealthy`，无 LLM 推理，进程组实际清空，见该目录 report.json。该红灯仍需要真实冻结服务/本地存储初始化，因此不是秒级单元测试；已从完整App数分钟准备收敛到单请求诊断，不用浅层HTTP夹具替代。

日志精确根因：Ladybug worker `INSTALL JSON` 下载 `https://extension.ladybugdb.com/v0.19.0/osx_arm64/json/libjson.lbug_extension` 失败。三个待证假设是冻结包漏收资源、子进程路径丢失、版本/ABI不匹配。实际核对锁定 Cognee1.6.7 wheel：资源存在，630440字节，SHA256 `e6482a025de31d662ee4b32a39c991706b8cc3c2ec49673ef5d8ce45cc23ce7a`，与wheel RECORD一致；旧1.6.7-memlane.5组件中同资源缺失。官方cognee_db_workers已支持按数据库报告的版本/平台加载该资源，无需另改上游算法或联网安装。

build-cognee-service.py已补入这份固定arm64资源。PyInstaller重签后原字节SHA不同的构建失败保留；复核原生11节、动态依赖及签名不变后，门禁分别记录源/分发SHA，完整组件构建退出0。新1.6.7-memlane.6在health-fixed-001新根中单次/health真实200、ready/healthy，无LLM，进程组回收。完整证据和新归档字节见[pi新验证](../pi-deepseek-validation.md)。

第二、第三包真实准备通过但取消驱动分别错过短请求和定位已离开的准备页，失败保留。第四包685c727c52a5使用新业务/状态/身份/会话及新完整UI/helper/CLI/LaunchAgent，仅复用组件文件；先真实Cognee验证、再在共享设置页取消实际pi长合成请求，随后另跑官方编码材料闭环。第二、第三包自有组件源已精确核对argv后退出；第四包仍在运行。

B02根因是stop/quiesce成功后仅在写回执成功时停止。成功perform后defer停止保证调用者断线也完成；失败perform不停止。无worker原三次失败、修后三次通过，真实Rust0.0.3三个新根均通过停止持久化、daemon/socket/锁及自有worker退出。报告stop-disconnect-rust-fixed-001，相关race通过；最终活动执行者/App停止仍需独立复验，B03/R01不随之关闭。

最新并行产品资料已将项目知识入口修正为绑定Agent主动初始化，原话与具体规则见agent-settings-placement.md；此前单复制按钮为历史设计，C revision4动效仍待认可。保留这些用户修改，本轮仅按A/B实现范围接现有目标页，不追随未认可C动效扩展。

后续接入只读版本补核：traecli/traex真实0.214.1 internal，TRAE IDE实际CN3.4.15当前适配门禁不通过。Magpie默认公共网关不可连，两个标准App位置也未发现；不据此全盘扫描、不读取其私有凭据或宣称成功。


## pi同包检查点与后续所有者

第五包 `41b3df22ae9d` 的同根闭环完成：官方Codex0.167.0-alpha.3.2/gpt-6.1-sol采集，Cognee1.6.7-memlane.6实际经pi0.84.1/deepseek/deepseek-flash推理，7 raw/2初始知识/Rust0.0.3索引/完整会话/App v2/新会话当前读取。`runtime/report.json`与continuation001–004的驱动失败保留；continuation005实际passed，未换原任务/材料或包。两条初始知识逐项审核通过；未提炼全部函数规则和初始口令，不宣称知识覆盖完整。Native原文阅读器/当前召回截图实际打开看图，仍不是A/B新视觉验收。

清理核对无该App/CLI/服务/worker/桥/组件源残留、socket不存在；Cognee文件仍starting但PID已退出的差异保留。主App41,668,388字节/DMG18,563,281字节/最终client逻辑文件1,558,609,461字节。本轮五归档总量712,037,978字节；第五包复用组件，实际HTTP下载未单独测量，不能把归档总量当这轮下载。

B03的0.0.4新Rust归档、Python源和daemon各跨10005真实请求，旧序号重放仍拒绝、活动取消/终态/停止、Go版本索引/当前检索通过；旧0.0.3跨限故障准确传播为unavailable。新归档未拼接第五包；待后续Native包包含0.0.4再完成适用验收。审查发现的启动取消race、Agent旧刷新覆盖、恢复路由、原报告覆盖、Go超时组清理/primary保持及Enola版本门禁均修复；证据review-runtime-continuation-001，短Go请求预算是明确真实进程故障报告而非正常通过。

共享处理配置的正式持久所有者与不可变修订已由A1账本完成；真实 pi 验证所有者、共享槽位、状态/取消私有API和显式启用现已接进daemon，并由新包和新合成根取得实际 Schema、abort ACK、回收、启用与重启回执。草稿/查看方式仍属于UI，三份已保存候选与一个当前修订属于daemon单写SQLite；运行任务冻结其启动修订尚未接正式 Cognee/P3 任务。旧单配置与traex计划迁移必须保留原件和授权，Q01–Q03决定仍待用户，不静默收费切换或制造首日。正式整理/计划仍走traex，不能据本次 pi 验证宣称已全局切换。

A1账本切片全仓test/vet和Store/daemon race通过；普通格式完整包daemon在新合成根验证schema16→17、49张旧表及一致性备份、v2原文、保存候选重放/重启、主动停止保护与明确恢复。驱动001/002只读WAL失败保留，003实际passed；最终6个自有daemon全部退出、socket不存在。详见[账本验证](../memory-processing-ledger-validation.md)。未调用模型、未打开新A窗口、未接真实验证/全局分派，不关闭A1整体或R01。独立App随包LaunchAgent仍为默认label，尚未注册，后续普通Native包身份须修正。

账本后的实现新增pi依赖解析与验证所有者、macOS Keychain不可变凭据版本，以及OpenAI Responses/Chat、Anthropic和Magpie协议基础。pi 验证所有者已接入 daemon 生命周期、与整理共用的执行槽位、开始/状态/取消私有 IPC 和显式启用门禁；新 `service.json` 显式携带客户端根。新完整包已在空业务根通过真实 pi/DeepSeek 连接验证、活动取消、abort ACK、原生 Schema、进程回收、启用和重启保持；首轮驱动误判保留。真实独立Provider/Magpie、Codex处理桥和正式任务分派仍未验收。详见[运行时记录](../processing-validation-runtime.md)。

## Native 记忆处理设置页检查点

「设置 → 记忆处理」已接到既有私有 IPC，并保持服务端顺序：保存不可变候选 → daemon 验证该修订 → 用户明确启用。实际 `processing-settings-runtime-003/report.json` 使用新空业务根、真实 pi 0.84.1 与 `deepseek/deepseek-flash`，从 Native 页面完成保存、活动验证取消、第二次验证成功和「设为当前使用」；取消回执包含 abort ACK、reaped 和 group-gone。用户 pi 配置散列未变，临时认证链接、socket 与 service/worker 锁均清理。

失败证据没有覆盖：001 为驱动未保留 adapter stderr 导致 JSON 解析失败，002 精确暴露 `isolated P2 root required`，004/005 在补窄窗口系统截图前被真实 Agent 保存／索引／检索预检门禁拦截。普通窗口的候选、取消、验证、当前使用系统截图均实际打开对照认可稿；二级导航、当前使用行、三页签、状态栏右侧启用、pi 卡片、三项验证和底部固定动作的核心关系一致。980×620 动作可见性由 MyGo 测试通过，但没有把它写成实际窄窗口截图。

生产格式 `processing-settings-package-002` 的 App 深度严格 codesign 通过，DMG `hdiutil verify` 通过；DMG 18,605,222 bytes，SHA256 `dc0e565f757fddf7e67d0265b76639b5f73225f3738239989dd23b2443198142`。该包仍为本机 ad-hoc 验证包。外层旧主侧栏、初始化承载、环境与批量 Agent 连接、Provider/Codex/Magpie、正式任务分派、B/C 页面和正式分发继续未完成。
