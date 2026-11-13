# 多 Agent / Provider 开发执行账本

目标：落实[定稿方案](../multi-agent-provider-technical-plan-20261114.md)的第一批全部要求，继续 dev，不恢复历史全量 Goal。第二批按原方案单独验收。

## 基线与所有权

2026-11-14 开始，源码 HEAD `6198179`，生产源码无未提交修改。未跟踪的项目权限、知识可行性与研究文档属于既有工作，保留、不提交。本轮独占新增连接／生成配置模块及其必要调用处；编辑共享文件前核对差异。磁盘可用113GiB。无服务／模型探测在运行；不操作日常配置和原库。

基线提交仅包含本轮方案、脱敏接口事实和此账本；实施前创建本地注释tag。不得提交本机凭据。

## 连续场景与依赖

| 场景 | 用户流程与验收证据 | 当前状态 |
| --- | --- | --- |
| MA01 | 原Codex主体、ACL、插件与正常采集召回保持；旧API兼容 | 待实施／回归 |
| MA08/09 | pi/TraeX电脑级预览→准备→激活→新会话问答工具→官方知识→新会话采用 | 优先实施；宿主信任需正常流程 |
| MA02/03/04 | Claude/Cursor同流程及跨Agent共享；限制逐项披露 | 待实施 |
| MA05/06 | 独立身份、禁采不回扫、403不重投、冲突/未知/部分成功 | 待实施 |
| LP01/04 | 各协议配置→验证Schema及质量→保存；错误安全、无跨供应商fallback | 待实施；真实协议验证需要对应可用凭据 |
| LP02/03 | 接入与模型独立；保存不切换→验证摘要→next→重启→active；失败单次恢复 | 待实施 |
| UI01 | 真实Native 1200×820，批量连接、生成配置、错误及重开连续性 | 后端固定后集中实现 |

实施顺序I01→I02/I03→I04/I05→I06/I07→I08→I09→I10。单测、协议夹具、真实宿主、模型质量和Native分别留证，不相互替代。I01基线完成，其他均未完成。

原E/PA缺陷停止记录保持；328字节README图谱停止项及helper替换问题不重试。临时物料限本轮`.local`目录，由本轮负责，用后清理，不删除既有依赖。

## 开发检查点一

基线提交`2539147`，本地注释tag `multi-agent-provider-baseline-20261114`。实施期间另一会话修改Native组件；本轮未编辑或提交这些UI源码。后续UI接线先重新核对其最新提交。

已编写，尚非日常接入交付：

- registry schema1→2迁移保留Codex ID、主体、ACL、会话关联，并留私有schema1备份；旧Codex API继续使用原适配器。
- 通用连接API及pi／TraeX准备、预览、激活与原操作幂等回执；独立不可变产物、宿主状态命名空间、摘要及配置冲突检查。不在准备阶段改宿主配置。
- pi全局扩展、裁剪的社区MIT四工具和JSON helper；TraeX原生命令hook；共用官方过滤、缓冲、PA门禁、未知写入语义。禁采事件只留ID水位；退出整理沿官方路径，不重复提交未知操作。
- 生成配置私有凭据、不可变修订、协议环境映射、验证摘要与next管理API；单独验证进程及已确认退出／未知资源边界。普通GET不触发模型。
- 启动候选／一次恢复模块与官方服务环境编译已编写；`scripts/dev.py`的实际接线仍未完成，所以当前日常启动尚不能使用新增Provider。

本次证据：[Python检查点](evidence/multi-agent-development-20261114/backend-checkpoint.json)。7个测试模块共67项通过，覆盖原控制、Codex和固定插件回归，以及新增配置、进程、迁移与隔离连接。随后补齐helper最终整理的防重提交，并重新通过6项连接检查（含TraeX问答／工具字段适配且不读transcript）。`scripts/test_pi_extension.mjs`通过实际pi0.84.1扩展加载器，使用合成helper验证四工具、问答、失败工具和撤权移除注入。TraeX0.214.1实际用户级安装器在临时profile安装通过；未绕过信任，不等于hooks已触发或模型采用。

无本轮真实模型调用、日常插件配置修改、原库迁移或启停日常服务。临时profile和测试目录已随测试清理；无本轮存活验收进程。未进入Native或完整包验收。

当前完成边界：I01完成；I02/I03/I04/I05/I06为部分实现待真实链路；I07待启动器接线；I08/I09/I10未完成。按首批完整功能及真实验收口径估算整体约20%，不把上述测试折算为宿主E2E通过。

后续顺序：补通用API／宿主失败回执与真实事件验证→启动器条件completion及下次启动应用→首批其他Agent→Native集中接线→各协议及跨Agent真实链路、权限和窗口验收。原MA/LP/UI范围保持。

用户本轮补充授权：Codex剩余用量额度低于2%时允许使用重置卡，无需重复确认；尚未使用。此授权不包含其他付费或供应商切换。

## 开发检查点二

继续完成I07源码接线：`scripts/dev.py`按所选不可变profile启动，Provider模式无需Codex executable/auth home/completion token，不启动completion子进程；官方服务读取固定revision。启动器持配置锁启动官方服务，核对私有回执的PID、profile引用和digest以及真实健康检查后才提交active，随后将写锁交给控制进程。失败仅在完整受管进程组及后代退出确认后恢复原配置一次。UI重开沿owner记录读取同一运行配置；保存候选不修改它。API模式初始化不再要求Codex健康端点。

生成配置摘要同时绑定私有key文件内容摘要，外部修改同一key版本也会使验证失效；API模式缺少Codex宿主时不阻塞独立Agent连接管理。日常配置／库／服务未改动。

[启动检查证据](evidence/multi-agent-development-20261114/startup-checkpoint.json)：启动器与生成配置共28项检查通过，API无Codex输入、配置摘要门禁、锁交接及一次恢复均有隔离证据；实际服务／Provider模型端到端仍待验。随后加严回执私有权限并定向复验相应2项用例。首次脚本检查有1项**测试夹具错误**：macOS受管可搬移Python被`venv`默认复制后找不到libpython；改用正常symlink venv后14项脚本检查通过。该轮不计作产品启动失败。

[固定上游兼容性核对](evidence/multi-agent-development-20261114/host-patch-compatibility.json)：Claude Code有4个入口需单独改写补丁，其余10个锚点匹配但语义仍待核对；Cursor有13个锚点匹配，缺少同版`_coding_memory.py`。特别要补Claude transcript禁采水位、Cursor事件模式的召回合并／撤权及无回答时不读历史，不能直接复制Codex插件宣称支持。源码审阅还发现主动search的旧CLI fallback需在受管产物明确拦截；这是待验证源码缺口，尚非本轮验收失败计数，也没有修改／重装日常插件。

整体估算约25%（对比检查点一增加约5个百分点），范围保持第一批全部Agent／Provider／Native及真实验收。I07已实现待真实链路验证，I08继续宿主专用patch及安装适配，I09/I10仍未完成。

## 开发检查点三

新增`project_host_plugin.py`，从已固定摘要的官方archive分别生成Claude Code、Cursor受管产物。保留各自官方入口；不将Codex插件改名。Claude四处不同入口独立补丁；主动Python工具固定受管凭据与绑定项目，服务不可用时不启动CLI fallback。Cursor继续使用官方payload适配和事件采集，复用同commit的官方coding recall库补公共知识，延迟注入时重建当前授权上下文，不返回旧缓存块；缺回答不扫transcript尾部。

Claude新连接在SessionStart建立stat边界，受管解析只读本session主transcript，不扫sidechain旧文件；sidechain须独立绑定。若SessionStart时transcript还不存在，保守跳过首次捕获轮，后续真链必须核对并补齐正常首轮采集，不能因此宣称MA02完成。Cursor无工具回合／首个Read工具的官方召回限制继续作为能力条件，真实IDE尚未验证。

通用连接管理已加入Claude Code和Cursor：准备仅创建独立主体产物，激活才调用Claude用户级原生marketplace/install或复制Cursor本地插件。CLI信任不绕过。核对Claude宿主installed_plugins的用户级cache及全部固定文件、私有marker；Cursor仅接受自有、摘要一致的现有产物，不原地替换运行代际；未知结果只review原产物，不重跑install。检测到Cursor可能导入已启用Claude Cognee插件时停止并要求消歧，保留全部既有配置。

[本轮证据](evidence/multi-agent-development-20261114/host-adapters-checkpoint.json)：11项检查通过，包括实际Claude原生安装器的隔离profile安装与cache摘要、Cursor准备不写host／显式激活复制、Claude禁采边界解析、Cursor延迟注入重建与无transcript读取，以及pi／TraeX连接回归。普通GET不调用模型；没有日常配置改动、真实宿主模型对话、真实Cursor IDE或新增服务实例。

本轮临时安装profile及测试产物均已清理，官方archive原件保留。整体估算约35%（增加约10个百分点）：I08代码路径已接入但仍待宿主真链和权限验证；I09 Native接线及I10整体验收仍未完成，原MA/LP/UI范围不缩减。
