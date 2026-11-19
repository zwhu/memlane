# 2026-11-19 Roadmap 可靠性修复与 dev 验收

> 本轮五项可靠性工作已完成修复及 dev 主流程核验，最终结论见[最终交付与证据边界](#最终交付与证据边界)。下表保留最初范围登记，后文保留各轮失败与交接时点，不将历史阻塞描述当成当前状态。最新运行入口及未覆盖范围见[接续文件](roadmap-reliability-handoff-20261119.md)。本文件随本轮代码提交归档，历史各节的未提交状态保留为当时时点；未进入 release。

用户已授权修复 11 月 19 日五项问题，并使用 Computer Use 完成完整 dev 流程。继续同一日常官方库，不进入 release；不重放旧无编号未知材料、不改变权限、不删除原始内容。基线 main / 6c524b2，工作区已有 53 个已跟踪文件修改及其他未跟踪产物，保留原修改。

## 目标与证据

| 项目 | 连续流程与验收标准 | 实施边界 | 当前状态 |
| --- | --- | --- | --- |
| R19-01 自动采集 | 当前插件的新消息进入正确项目；官方编号回执及 Native 原文可读；保存与整理分别核对 | 复用官方插件与原编号协议，先验真再定向修复 | 待验证；沿用 CAPTURE-DATASET01 与旧未知记录 |
| R19-02 项目目录 | 启动接管已有绑定一次；刷新以登记表为准；新增、目录移除/重绑、失败快照、阅读位置和重启一致 | 目录投影及启动接管，不删除 dataset 或 ACL，不新增删除产品入口 | 待实现 |
| R19-03 异常恢复 | 父进程消失后受管服务退出；核对进程身份、端口、同库锁后恢复；未知进程及 PID 复用不被终止 | 统一父进程监测，增加明确 dev 恢复入口 | 待实现；与 DEV-STOP01 正常停止超时分开 |
| R19-04 处理交接 | 已应用但丢回信→整组重启→查原操作；管理 active、实际 active 与新请求一致；保留取消水位和未知屏障 | 关联启动恢复与历史回执，不能把历史 applied 当当前生效 | 已复现，累计失败 1 次，待修复 |
| R19-05 图谱契约 | 固定后端真实响应→当前渲染器→节点及来源；空图正常；图谱和原文返回不中断 | 保留当前固定版本；有实证才调整契约 | 未证实兼容故障，待真实验证 |

初始只读盘点：runtime 中 1 个项目，登记表 7 个；config-only 为 0，registry-only 为 6，原代码绑定均已入登记表。这不是已发生串项目的证据。

## TARGETS 与 BUG_MAP

测试语言为 Python（主）及 Go（桌面），EXEC_SOURCE 为空；复用 unittest / IsolatedAsyncioTestCase、testing / MyGo Tester，全部执行经 scripts/task.py 或 scripts/go.sh。范围为上述具体符号及直接集成，不审查无关未提交修改。

| TARGETS | BUG_MAP / 正确结果 | 类型与优先级 |
| --- | --- | --- |
| generation_startup.start_generation；GenerationHandoffRuntime.get/inspect；GenerationHandoff.review | 引擎应用 B、管理仍 A 后重启，不能用历史回执把当前 A 标为 B；恢复必须有当前生效证据 | Business Gaps，P1，R19-04 |
| scripts/dev.py 的受管启动/恢复；project_control_server 生命周期 | 失去 owner 后退出；确认无存活消费者才归档旧 owner 并允许同库启动，未知/复用 PID 拒绝 | Resource / Error Handling，P1，R19-03 |
| App.applyAccessDirectory；configured_app 的启动登记 | 成功刷新目录替换旧绑定，稳定项目 ID 保留阅读；启动只接管一次，已有登记规则不被旧配置重放 | Business Gaps，P2，R19-02 |

R19-01 / R19-05 不凭测试缺口加入缺陷清单。新具体缺陷追加独立编号；同一问题累计第 4 次验收失败停止，不因本轮换文件或操作 ID 重置。DEV-STOP01 保留历史 4 次失败及后续通过，不借异常恢复重跑其旧失败轮次。

## 执行顺序与资源

先写代表性回归并确认失败，集中修复三个边界；定向检查后统一 Python 相关协议、Go test/vet（并发变动加受影响 race）及 lint。测试技能只约束测试生成阶段；用户本轮明确修复授权包含生产源码修复，不因技能的测试专用限制停止业务修复。

真实 dev 前核对在途模型/保存/图任务、当前 owner/子进程、窗口 watcher 与同库配置；按 scripts/dev.py 正常停止并重启。通过 Computer Use 检查实际设置、切换、项目阅读、搜索、图谱及返回。异常退出用受控空闲时点验证，先保留私有配置和元数据备份；不复制第二业务库、不强杀未知消费者。

临时协议状态仅使用 TemporaryDirectory，执行结束清理；保留小型日志、回执和截图于本轮证据目录，凭据、原文和本机配置不入 Git。只保留当前日常后台和当前 Native dev 入口，具体 PID 与结束状态待验收后补齐。

## 轮次与结果

- R19-04 初始复现：生产状态类，SDK/传输夹具，零模型调用、无日常库访问。管理 active=new，实际 active/model=old，历史操作 applied；累计失败 1。原脚本及回执位于 /tmp/memlane-roadmap-analysis-20261119/，后续新增回归不覆盖原证据。
- 修复、测试和 Native 结果待追加，不将设计或源码分析记为通过。

## 本轮实施与检查结果

- R19-04：启动器按管理侧未完成操作与引擎持久 `applied` 回执的操作 ID、配置引用及摘要匹配恢复目标；仅健康/应用摘要通过后更新 active。引擎查询附当前 `runtime_active`，管理侧拒绝用不匹配的历史成功回执更新当前配置；历史记录不被覆盖。恢复目标启动失败时停止，不猜测回退成功。
- R19-03：三类受管服务复用 `owner_lifecycle.watch_owner`。新增 `dev.py recover`，仅核验全部退出后归档原记录；保留 PID 复用、未知监听、活跃锁拒绝。顺带关闭 `acquire` 加锁失败时原有的文件句柄泄漏。正常停止的旧 DEV-STOP01 记录不改写。
- R19-02：运行配置只进行一次目录接管，标记保存在已有登记表；后续目录和代码绑定由登记表投影，成功刷新移除未登记项，失败保留快照。按项目 ID 与 dataset 身份迁移阅读 pane；无关项目移除保留选中/正文/滚动，改绑撤下旧内容。未增加项目删除入口或删除官方数据。

测试：新增 Python 8 个顶层用例，Go 1 个顶层入口含 3 个场景。生成阶段两条切换断言在旧实现失败、一条历史已完成操作场景通过；属于 R19-04 原基线补充证据，不新增修复后失败轮次。修复后 11 项启动/恢复检查通过，随后 19 项控制/交接检查通过。最终 118 项相关 Python 回归通过（含参数、真实 SDK 原生协议请求体、交接、启动、采集、目录、生命周期及 lint 规则）；完整 Go test/vet、受影响目录/图谱 race、全 Python 参数 lint 均通过。无覆盖率卡口要求，按测试技能 Step6 跳过覆盖率；utree flush 已执行。日志在 `build/diagnostics/roadmap-reliability-20261119/`。

## 同一日常库的真实验证

1. 正常 dev 停止原 owner 505，明确返回 `Owned service groups exited; daily store released.`；原服务 512/522/681 均退出。
2. 修复后 owner 23467 启动同一日常库，项目目录一次性接管成功。后台/图任务与桥接请求均为 0 时受控终止该 owner；三个服务 23469/23470/23554 随后全部退出，原 owner 记录原字节保留。
3. `dev.py recover` 确认进程、端口和锁后原样归档记录；随后正常 start 成功。7 个项目的 ID、dataset、别名、权限、代码关联及默认策略与私有备份一致。
4. 真实切换先遇到已保存 Ollama 服务未运行，返回 `not_applied / generation_provider_unavailable`，管理侧和运行时均保持原 Codex。此为准备条件缺失及拒绝路径通过，不计 R19-04 又失败。复用原 `.local/multi-agent-ollama-20261114/models`、不下载，启动原 50639 端口后，Codex→Ollama→原 Codex 均 `applied`，每次管理 active 与运行时引用/摘要一致。两次操作为 `r19-b24b8e38a67741929d9d5ba1ebb442a0`、`r19-4de9cea70ba948da9fa16be9a893a840`。这是服务 API 连续验证，未代替 Native 点击或新一轮模型质量验收。
5. R19-01：当前 TraeX thread 按安装实例加 thread 的真实哈希解析，官方确认 2 条 QA、152 条 trace，主体/项目正确，最新 trace 时间 1795027380.981496，晚于本轮服务恢复。本轮未改采集器、未补采或重发旧未知材料。Codex 接入状态也有真实保存回执；pi 仍等待新会话，未外推其当前采集已通过。
6. R19-05：固定服务真实返回 1000 节点/2756 边，节点唯一、边端点有效，`node_set/pipeline/task/user` 颜色映射齐全（14 个 node_set 颜色）。未发现该响应的契约不兼容，不升级固定版本。实际画布点击/来源返回仍未完成。

方法问题另记：采集核对首次误用原 thread UUID 查询管理绑定，按实际插件哈希契约纠正后通过，未改产品；容量核对首次使用错误路径/缺 owner API key，改为现有 `/capacity` 双认证后通过。Computer Use 首次窗口超时及热更新窗口 ID 变化是工具准备问题，不计产品失败。

## Native 验收阻塞与接续

2026-11-19接续：用户已恢复并要求完成剩余任务，Computer Use动作连接恢复。过程中另一会话修改UI并操作同一窗口，产生热更新和旧ID失效，已保留其修改并重新定位。本轮核心修复源码摘要仍匹配原manifest。

新增具体问题 R19-SESSION01：官方固定ImprovePayloadDTO和实际插件均支持/发送`session_ids`，交接中间件只取`sessionIds`，真实请求的scope.sessions会为空。当前旧SDK回归发送camelCase且路由fixture忽略body，不能证明真实官方payload正确。该问题与R19-04的历史成功回执/当前状态混淆为不同触发及责任代码，独立计数，初始代码确认，实测待下述回归。TARGETS限定`install_generation_handoff.generation_context`及`test_generation_handoff`现有SDK HTTP用例；BUG_MAP为Business Gaps/P1：正确请求身份应与官方DTO解析结果相同，错误结果为正在处理数量漏记及真实任务不可接续。复用Python/unittest、已加载技能与本轮准备脚本，不扩到其他配置或UI改造。

R19-SESSION01回归：旧实现对真实`session_ids`请求的task scope断言失败，1轮；修复直接复用固定上游ImprovePayloadDTO（接受snake/camel及官方别名优先级），非法输入仍走422且不产生任务。7项SDK/交接回归、39项generation参数/协议/启动/交接门禁、lint均通过。日志`session-field-before.log`与`session-field-after.log`保留原失败。

R19-EMBED-PATH01：加载上述修复的正常同库重启失败1轮，实际原因是私有环境HF_HOME仍指向已不存在的`memlane-task-gnwqmjhd/.../cognee-bge-zh/1.5`目录，FastEmbed离线加载失败。日常库保持；同型号已有模型位于`build/onboarding-components/stages/model`，仅验证和复用，不下载、不换模型、不重建向量索引。此为运行依赖位置问题，不与R19-SESSION01合并。此前运行进程已加载的模型不证明重启依赖仍有效。

后续纠正：上述build目录实际只剩小文件/回执，模型本体94781076字节已缺失，离线探测失败。依赖步骤未正确按探测退出码阻断，导致又启动同一未修复配置，已按正常stop收尾；R19-EMBED-PATH01实际启动失败累计2轮，离线探测为准备问题，不凑产品计数。未改私有环境、未下载或换模型。按原component.json确认应恢复SHA-256 `1294ea4b6331115a353d81f96b85e8c8d7fdcc284453d5b2fab5b016230aad38`，来源Qdrant/bge-small-zh-v1.5固定commit `46fbe35fd4374a00fee7de77dfddaeb6dd6a2c59`，90.4MiB，目标`.local/daily/components/cognee-bge-zh/1.5`。用户下载确认待答复；不能擅自用现有int8模型替代或改HF缓存metadata伪装完整。

方法事件：本次UI重开/整理准备在服务ready前开始，整理在policy前置被拒绝、未写提交记录、未调用模型；不是已证撤权。后续必须以真实服务ready和ACL核对后再执行。

Computer Use 插件 0.3.2 已加载，辅助功能及录屏权限均 granted。读取真实 1200×820 窗口成功，首页保留 7 项目、250 记忆、55 原始记录；没有用合成截图替代。但 click 在紧接同一 get_app_state、使用一致 bundle ID/窗口 ID 的情况下仍返回 `Computer Use is not prepared ...`。已停止重复点击，未绕过到另一套 UI 自动化；需要重启/恢复 TraeX 会话重建工具连接。此时完整 dev 验收未完成。

接续按序：

1. 重新加载 Computer Use，list_apps 后用同一 app/window 获取状态并点击设置，确认动作连接已恢复。
2. Native 核对当前 Codex→直连→Codex的选择、取消及实际回执；直连原服务按上述目录/端口复用，结束恢复原配置。若需要真实推理，沿既有受管验证入口，不能凭切换回执声称模型质量通过。
3. 项目记忆/原始记录阅读、统一搜索、切换项目/返回；核对 7 个登记项目和有效阅读状态。
4. 图谱打开、节点点击、来源返回、空图；保存真实窗口证据，分别记录真实响应与 Native 行为。
5. 更新五项完成边界和最终资源清单。不要重跑已通过的完整矩阵，除非新的源码变化或失败要求；保留本轮全部失败证据与历史计数。

当前同库后台 owner 24736，服务 completion 24738 / official 24739 / project_control 24753；当前处理引用恢复为 existing-codex revision 1。Native 由本轮 `dev.py open --watch-ui` 保持，窗口 ID 可能随重开变化，不复用旧 ID。临时 Ollama 在 API 检查结束后正常停止，原模型文件保留。私有回滚元数据位于 `.local/daily/reliability-20261119-backup/`，不提交；本轮源码未提交、未 release，原有无关改动保留。

## 重启后接续结果与当前状态

本节覆盖上文历史“Computer Use未恢复／后台保留”的当前状态，保留历史证据。已实际完成：

- Native浏览Provider仍显示原Codex；应用确认取消后没有产生新的切换操作。随后Native确认启用Ollama（`ui-MHVPFGQSCIALRV3EOVW3DZGWZH`），界面、管理active、引擎active引用一致。再从Native确认Codex（`ui-AB5CSDBIWAURBULDNJ7P63RAGN`），使用该Agent最新已保存revision2，界面/管理/运行时一致，不等同于原revision1。模型详情读取当前Agent有效设置，未修改其账号或全局配置。
- 当前本次TraeX会话的4条问答、218条工具记录在Native可读，原官方主体与memlane项目一致；问答/工具记录切换和跨项目返回成功。数量是当时快照，日常新记录继续增加。
- Native记忆详情→真实来源会话→返回原记忆弹框已完成；显式会话级出处，不伪造原文锚点。搜索输入/Enter提交、原始记录类型筛选及真实命中展示已完成；搜索结果→详情→返回搜索的完整新候选连续验收仍待最后收口。
- 固定官方图谱实际显示1000节点，查看节点/点击可见实体触发前端选中和Native身份回执。对缺少官方document_id/data_id的节点，出处操作明确反馈不可定位，不伪造来源。另读取7个真实项目及已有代码图：均非空；没有删除或造数据制造空图，实际空图样本不可用。来源正文正向跳转仅在记忆链路已验证，图谱缺少文档身份的实体不记为正向跳转通过。
- 真实截图已保存于`build/diagnostics/roadmap-reliability-20261119/native-resumed/`，已实际查看，不提交日常正文或完整控件树。期间另一会话修改/验收同一UI，产生窗口ID漂移和导航变化；按当前实际窗口重新定位，保留原有修改，没有把工具旧ID失败记为产品失败。

协议增补R19-SESSION01已完成生产修复与原失败/修复回归、39项必要generation门禁、当前完整Go test/vet、lint和utree flush。覆盖率按无卡口要求跳过。新的实际在途交接仍依赖日常服务恢复，不能用这组SDK协议夹具冒充真实在途通过。

当前：原日常owner24736及其服务已正常stop、锁释放；加载修复时暴露R19-EMBED-PATH01并已收尾失败启动，日常库及生成配置保留（active为existing-codex revision2，next=null）。下载许可仍等待用户答复，恢复计划`restore-embedding.py`已落盘但未执行下载。没有留下可运行日常后台，不再沿用上文的旧PID“运行中”描述。本轮临时Ollama55360按内核身份正常停止；模型、旧未知消息、库和原回执未删除。

收到下载同意后的接续：`scripts/task.py`执行已保存`restore-embedding.py --download`，校验原94781076字节及SHA-256，完整缓存放`.local/daily/components/cognee-bge-zh/1.5`；离线512维实际probe通过后，备份私有service_environment_file，只修正HF_HOME为该持久路径。必须逐项检查前一步退出码，失败不得继续start。正常同库start ready后再open Native；再次核对新官方任务使用snake字段可识别，确认在途取消终态及剩余材料接续，完成剩余搜索返回和窗口关闭/重开；最后更新资源与交付边界。

用户已明确同意原模型下载恢复，授权仅原90.4MiB文件及持久组件目录，不需再次询问。首次urllib下载出现socket read timeout，部分文件保留，使用原固定URL及Range断点续传；完整大小/SHA-256通过前不启用、不改配置。该下载错误是准备/网络事件，不计日常启动又失败一轮，日志`embedding-restoration.log`与`embedding-resume.log`分别保留。

真实在途接续追加：Native `ui-E7QIVQVDCFWFHM7QVSDJ2LCPHY`在真实pi任务仍运行时确认回切Codex，取消1会话。官方原操作`9e75f2ab-08e2-4160-b68a-4757047183bf`实际终态failed/CancelledError/ended_at已核对，主体和会话与取消回执相同。该会话重新登记watching，随后发现R19-CONTINUE01：IncrementalProcessing.tick将同项目旧stopped批次一律当作项目停止，当前已确认交接的新会话不在旧targets/pending范围内，也被阻断。TARGETS新增tick的停止范围判断；BUG_MAP Business Gaps/P1，真实接续未执行1轮。修复仅针对带已确认handoff_operation的会话，原批次明确范围不相交时才不阻断；明确automatic_user_paused、同会话停止、范围未知、原提交unknown/submitting及项目有效权限继续阻断。原停止任务、旧未知材料和水位不改写。测试框架和准备复用本轮已完成Step1–4，新增范围/缺陷分析后执行Step5回归。


## 最终交付与证据边界

2026-11-19 08:54 +08 收口。本节覆盖上文历史“等待下载／工具未恢复／未就绪”的当前状态。用户同意恢复原模型后，已从官方固定 Hugging Face 来源恢复原 94,781,076 字节文件，SHA-256 与原组件一致；持久根为 `.local/daily/components/cognee-bge-zh/1.5`。真实离线 embedding 输出有限值、512 维。仅修正私有 HF_HOME，保留 HF_HUB_OFFLINE=1；未更换模型、重建原索引或建立第二运行库。三次网络准备错误及两次原启动失败证据均保留。

### 五项判断与结果

| 编号 | 从必要不变量判断真实性与优先级 | 最终实施与验证 |
| --- | --- | --- |
| R19-01 自动采集 | 保存回执必须对应原消息、主体和项目；丢原文影响后续全部价值，因此优先核验。旧授权故障真实，但未发现当前官方编号链路新增缺陷 | 保留官方采集实现。当前 TraeX 会话实际问答/trace 在 Native 可读；Codex 有官方保存回执；两条真实 pi 只读任务保存成功，后一条接续整理完成。pi 验证隔离了原有损坏第三方扩展，不能外推默认 pi 全体扩展启动正常 |
| R19-02 项目目录 | 桌面和 Agent 必须对同一项目使用同一 dataset。双来源和旧配置重放有真实源码路径，未证明本次已发生错误查询，P2 | 登记表单一投影、一次性接管、按身份保持阅读；目录移除/重绑/失败快照有行为检查，实际重启前后 7 个项目/绑定/权限一致，跨项目阅读与搜索返回通过 |
| R19-03 异常恢复 | 服务生命周期必须与 owner 一致，确认旧消费者退出后应可恢复原库；缺失父进程监测和恢复入口真实，P1 | 统一三服务监测，新增 recover。真实空闲 owner 异常退出后三服务退出，原记录原样归档，同库重启成功；PID复用/未知端口/活组/锁拒绝有回归。Native 关闭/重开时后台保持 |
| R19-04 处理交接 | 历史成功不能证明当前 active，取消必须有原任务终态且剩余材料继续处理；丢回信重启不一致真实，P1 | 配置引用/摘要/操作匹配恢复，历史回执核对当前运行引用；Native 浏览/取消保持实际配置，双向确认与回执一致。真实在途取消及同源接续由下述官方终态证明 |
| R19-05 图谱契约 | 前后端提交号不同不是故障证据；需证明当前真实响应无法被消费，P2 核验 | 固定 `ec6f408`/`8853378` 保持。真实 1000 节点/边/颜色映射、Native 画布与节点点击通过；无 document_id/data_id 的实体明确不可定位。7 项目及已有代码图均非空，实际空图和缺少身份实体的正向出处跳转未覆盖，不删除或造数据制造样本 |

### 真实取消与接续闭环

R19-SESSION01 修复复用官方 ImprovePayloadDTO 的 `session_ids`/`sessionIds` 解析；R19-CONTINUE01 修复旧停止范围对确认取消源的阻挡。原停止批次 `2841513d-b165-48b5-9c5e-2498119c9571` 保留 stopped/user_stopped，不清除旧 unknown/submitting 或修改旧水位。

Native 操作 `ui-E7QIVQVDCFWFHM7QVSDJ2LCPHY` 确认取消正在执行的 pi 源并回切 Codex。原官方 improve `9e75f2ab-08e2-4160-b68a-4757047183bf` 的 failed/CancelledError 及 00:31:59.261816 终态保留。加载窄修复的新 owner 启动后，原源自动提交（管理操作 `740c376d-49c8-4ca2-80d8-66562e3fc2dc`），新官方 improve `32487eb6-41d2-4b7a-bff4-0432539472af` 在 00:47:22.202018 UTC succeeded。官方日志确认同一会话 completed/documents=3；user_id、dataset_id、session_ids 与原取消源一致，没有重投其他未知消息。

证据为 `continuation-after-restart.json`、`continuation-terminal.json` 和原服务日志。实际模型由已启用 Codex 身份执行，不能把 pi 采集主体误写为 pi 整理模型；不能把这 3 份文档外推所有历史知识质量通过。最终管理和引擎 active 均 `existing-codex revision 2`、next=null；旧画像标量不替代 Agent 当前有效请求快照。

### Native 连续验证与候选

同一日常库、1200×820 实窗，经 Computer Use：搜索 roadmap → 10 个关键词匹配/63 个语义相关 → 记忆正文 → 关闭详情 → 返回搜索，原关键词/结果保持；改为原始记录 → 8 个关键词匹配/50 个语义相关 → 官方保存原文 → 返回搜索，筛选/结果保持。关闭搜索恢复原总览。cmd+w 使 Native 窗口隐藏，三服务仍 ready，active/7 项目不变；通过同一实际 App 路径重开，1200×820 窗口及连接恢复。

本轮 24 张 PNG 位于 `native-resumed/`，其中 18–24 为最后连续搜索/重开截图，已实际查看。截图只留私有诊断目录，不提交真实正文。旧失败工具截图及回执不改写。其他会话的 V3 UI 改动保持，以上只是本轮可靠性操作验证，不代表 V3 全量视觉验收或新 release 已完成。

检查复用：初始 118 项 Python、完整 Go test/vet 和受影响 race 已通过；官方字段修复后 39 项 generation 必需门禁通过；接续修复后 34 项相关 Python 与 lint 通过。最终 Go 文件最近修改早于 resumed-go-all/vet 通过时间，未无理由重复矩阵。源码空白检查通过。最终 `source-manifest-final.json` 增补字段解析和接续候选，保留早期 manifest。没有新增生产代码修改后沿用旧测试的情况。

累计失败：R19-04=1，R19-SESSION01=1，R19-CONTINUE01=1，R19-EMBED-PATH01=2；均在原问题修复后通过。方法/前置/下载错误分别留痕，不凑产品失败；DEV-STOP01 历史四次及后续通过不改写。

### 保留运行与收尾

唯一业务根 `.local/daily/official-store`，启动配置 `.local/daily/runtime.json`。08:54 +08 owner 94208 ready，completion 94212、official 94263、project_control 94341；保留同组及其实际依赖。Native PID 97915、window 321801，附着 UI watcher 97794 保留。PID 只是此时快照，后续必须先核对内核启动时间/端口/锁，不凭本文直接发信号。

本轮 Ollama 78338（50639）在恢复 Codex、无加载模型/连接后正常 SIGTERM 退出，回执 `ollama-final-cleanup.json`；原模型资源保留。最终 official running_sessions=0、background=0、graph_jobs=0、bridge active_or_waiting=0，证据 `final-live-state.json`。磁盘约 83.9 GiB 可用，满足 50/55 GiB 门禁。保留原库、ACL、主体、凭据、其他插件和运行依赖，不清空 build 或删除 p3-prototype 工作树。

本轮源码及文档未提交，基线仍 main/6c524b2；其他会话修改保留，无 MR、无 release、无完整包构建。五项本轮修复与 dev 主流程任务收口；剩余证据限制为实际空图无样本、缺官方来源身份实体无法正向定位、pi 默认第三方扩展错误及 V3 全量视觉范围，均不伪报通过。


## pi 官方 Flux 扩展恢复（用户追加授权）

2026-11-19 09:27 +08。用户选择第一种处理：保留 Flux Island，通过官方流程恢复 pi 接入。本节覆盖上文“pi 默认第三方扩展仍损坏”的当前状态，原失败与隔离验证事实保留。

默认 pi 无推理 RPC 启动实测退出1，明确 failed to load extension / hook-client.mjs 缺失；整个 `~/.pi/agent/extensions/.flux/flux.island-pi-plugin:pi` 目录不存在。Flux Island 0.1.49-hotfix.857946c 的内置 `flux.island-pi` 0.1.0 插件存在、active，但其 `plugin:pi` Agent 为disabled；遗留桥接仍由pi自动加载。本轮未推断缺失目录的删除来源。

先备份原 flux-bridge.ts、pi settings.json、Memlane 官方入口及摘要到私有 `.local/daily/reliability-20261119-backup/flux-pi-restore/`。通过 Computer Use 打开 Flux 设置 → Agents，启用 Pi；官方安装计划恢复原路径 hook-client.mjs（3653字节），页面显示已就绪。没有手写替代Hook、重装pi、下载新的模型或改pi模型/账号。原桥接、pi设置及Memlane入口SHA全部保持；官方管理API核对仅plugin:pi从disabled变enabled，其他Agent启用状态保持。

原无推理RPC检查随后退出0，get_state成功，继承deepseek/deepseek-flash/high。再使用默认扩展发现（未传--no-extensions或替代extension），执行只读scripts/dev_recovery.py的真实任务，44.44秒完成、退出0；没有项目源码写入。官方单一日常库保存1轮问答、1条工具记录：主体pi-033f3aca88231dfdb2fd47fb，host pi-ffe787d89afdf3dbef84bab1181a202d6e43a944a3aa9dba0a0c67668e12ccfe，dataset与memlane项目一致。默认任务使用pi自身DeepSeek，日常处理active仍Codex revision2，未切换后台处理方式。

证据：pi-default-startup-readonly.json原失败；pi-default-startup-restored.json默认启动通过；pi-default-real-task.json真实任务；pi-default-capture-restored.json原主体/项目官方保存；pi-restoration-final.json资源与配置。native-resumed/25-flux-pi-ready.png为已实际查看的Flux页面。本次额外读取Memlane窗口在Computer Use 120秒超时，未获取新Native正文截图；只记录CLI+官方API新保存通过，不冒充新增Native阅读通过，不重复该工具调用或替换UI自动化。

本轮真实pi任务已退出；Flux应用保留运行，官方Pi健康ready；日常owner94208和原Codexactive保留。临时浏览器标签已关闭并返回原GitHub标签。此次为本机官方接入修复及验证，不新增Memlane生产代码，不进入release，不提交本机配置、凭据、会话或正文。历史pi隔离验证不覆盖原错误，这次新的默认启动及默认扩展任务证据才证明修复。


## R19-LINK01 已有记录直接关联（用户最新纠正）

用户截图显示选择ml-user-profiles后“关联项目”禁用，随后明确“已经已有记录，不需要选择源文件夹”。旧产品/代码统一要求folder，初步仅补提示的处理已撤回；本轮按用户最新规则实施：新建空项目需要本机文件夹，已有记录直接加入项目目录，不必提供workspace、不生成空路径别名、不重建dataset或修改已有ACL。选择已有记录时清除新建表单的旧文件夹草稿，避免隐藏路径被误绑定；已登记项目确认只读返回，不重复写入。

影响为project_create_ui/create、ProjectControlClient.create及project_control/server登记契约；项目目录继续使用登记表权威投影。连续用例：打开新建→选择真实已有记录→源文件夹区撤下/关联可点→取消不写→确认关联→原数据组内容可读→返回/刷新/正常重启保持。定向HTTP/Registry及MyGo组件检查先行；真实单库下先盘点/备份目录和源身份/ACL，正常重载服务再操作，不创建第二业务库、不猜用户文件夹。新建项目仍拒绝空路径，共用/代码/保留数据组继续禁止关联，未知保存只查询原目录确认。

R19-LINK01初始用户实窗失败1次；已有记录缺路径被拒为本轮责任，不计为旧R19-02目录投影失败。Computer Use读取Memlane再次未及时返回，已取消该只读工具执行，不凭截图猜取消按钮失效，不重复同一旧窗口调用；Native证据待工具与当前窗口重新定位。


R19-LINK01定向结果：31项项目控制HTTP/Registry检查、桌面连续操作/取消/未知回执定向检查、完整Go test/vet及lint通过。服务正常同库stop/start成功。实窗观察到ml-user-profiles关联后进入原始记录，目录变为8项目；同组3条原文可读。实际登记ID566ea5e7-646c-54a5-bb03-2700bad05c04、dataset7490f263-f9d6-564f-9462-582c0652df52、aliases=[]、status=verified；关联前后文档ID/contentHash、样本文字SHA及全部Agent有效权限相同，没有新建数据组或空路径。证据link-records-before/content-before/after.json，Native截图26-linked-existing-source.png。中间选项/确认没有留齐独立截图，不将这一张当作每个按钮动作的单独证明；取消和隐藏旧文件夹由实际组件连续操作检查覆盖。

本次接续保留并行工作区修改。格式化第一次使用Go包级fmt，涉及已有knowledge_unread_test.go的格式，后续改为只格式化本次四个文件，未回滚该文件的其他会话实质修改。重启准备的watcher检查首次错误地要求CLI配置为绝对路径，真实入口保留相对路径，严格检查拒绝且未发信号；核对原PID/启动时间/真实命令后按正常SIGTERM收尾，为方法前置错误，不计R19-LINK01新增产品失败。


R19-LINK01最终收口：正常stop/start后owner84481 ready，8项目保留；原7项目dataset/aliases/overrides/code绑定字段不变，新增ml-user-profiles仍aliases=[]、verified。重开Native85873/window322535后，经Computer Use选中该项目→原始记录，实际载入原3条并打开原文；截图27-linked-records-after-restart.png已实际查看，补齐新候选的重启后Native阅读证据。用户无需再选源文件夹；仅新建项目仍要求本机路径。取消/撤回选项和未知回执不重提由组件连续检查证明，不把选项中途缺失的截图补写成独立Native通过。

交付证据link-records-restart-result/final-receipt/final-live.json及source-manifest-link-records.json。日常Codexrevision2、running_sessions/background/bridge在途均0；唯一日常库及embedding持久根保持，服务和最终Nativewatcher保留。累计R19-LINK01失败1次，修复后定向、集成及真实同库/Native验证通过。所有原失败、私有目录备份、其他会话修改保留；源码和文档未提交，未release。


提交前补验：Computer Use在真实未关联记录上仅执行选择与取消，未确认关联其他数据组。选定后源文件夹区隐藏、“关联项目”启用；“取消选择”恢复新建表单；再选择后“取消”关闭弹框，8个项目ID集合保持，未新增关联。截图28-existing-selection-enabled、29-cancel-selection-restores-form、30-cancel-closes-without-association及link-records-cancel-native.json补齐实际窗口证据。未知保存后的核对仍由真实HTTP/组件回归证明，未向日常库注入异常。

源码交付：本文件、当前handoff及可靠性/已有记录关联修改一同归档。共享工作区的通知、召回、V3和其他未提交修改保持独立；私有运行配置、数据库、模型、会话、正文截图和回执不纳入Git。发布仍暂停。

提交候选独立检查发现project_create_ui中的panelShadowInk来自另一会话未提交视觉修改。首次独立Go编译失败为暂存范围/依赖归类问题；已只在index中排除该阴影调用，工作区原视觉样式保留。候选与日常运行工作区分别检查，首次staged-go-all.log/staged-check-result.json失败证据保留，后续检查用v2独立日志，不改写原失败。
