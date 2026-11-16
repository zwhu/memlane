# 官方 Agent 链路收口执行

2026-11-17，用户已授权按职责收口方案实施。只做 dev；保留单一日常库、现有主体与权限、来源及未知写入记录，不迁移历史、不安装 Hindsight、不做 release。方法沿用 plan-change-impact / verify-continuous-scenarios。

## 基线与所有权

- 源码检查点 `73c7343`；方案提交 `ced7bfc`，清理前注释 tag `pre-official-agent-convergence-20261117`。
- 本轮持有 `.local/continuous-loops/action.lock`，范围为官方插件收口、显式记忆展示及相关检查。不修改其他会话未提交的研究和历史截图。
- 官方固定插件仍为 `a176248914fdb32a78354854b300174203b15a5c`。不把当日 main 的文档当作已验证本机能力。

## 连续主流程及验收

| 流程／规则 | 处理方式 | 完成证据 |
| --- | --- | --- |
| 官方 skill 直接保存 → 同项目官方 Data → Native 记忆列表／搜索／计数／原文 | 识别官方 `user_context` 及现有 adapter 的 `memory` 标签；不冒充 session_learnings、不制造模型接受理由；project_docs 仍是资料 | 分类、列表、搜索及原文一致性测试；同日常库真实写入／读回与实窗另记 |
| 官方采集 → 提炼 → 新 Agent 召回 | 继续官方管线；先撤掉无产品必要性的客户端召回数量调优，不重写处理算法 | 固定原版对照、既有权限／召回回归；真实新会话另记 |
| 新代安装／官方升级 | 原版输入固定与校验；已安装旧代仍可验证，不原地编辑cache | 新旧代校验、构建及安装测试 |
| 权限、恢复采集边界、未知写入 | 保留现有保护；G01/G02尚无公开扩展点替代证据，不宣布零补丁 | 沿原规则回归、逐项剩余补丁说明 |

## 顺序与边界

1. 先打通官方直接保存内容的读取分类，统一复用现有列表／搜索／统计／未读和正文读取，不加新正文库或新写入代理。
2. 撤销召回数量的定制，沿用官方默认；版本化新基线并保留现有安装，不自动切换旧会话或其buffer。
3. 核对公开配置／宿主hooks的替代能力；没有等价能力的权限及采集保护列为暂留，不能仅搬出文件就称消除内部耦合。
4. 定向测试通过后集中集成；模型及原始内容真实验证使用日常库，先查在途处理，不重投未知操作。UI沿现有设计，只改数据语义，实际打开设计与Native后才能报告视觉通过。

G01/G02沿前轮各一次官方契约差异观察，非当前产品验收失败。历史具体缺陷失败次数不重置。临时合成宿主由测试创建并收尾，不另建运行库；旧安装及其运行依赖在切换验证前保留。

## 当前结果与交付边界

用户曾要求全部暂停，本会话保留暂存修改、日常服务与锁，未提交。随后在本会话明确“继续推进”，仅恢复本任务，已通知总协调；其他验收和历史全量Goal不自动恢复。

已实现官方直接记忆的读取语义；撤销客户端对项目召回候选数22、未缓存原文读取数4的覆盖，回到固定官方默认。十个skill文本未修改；权限、配置绑定和恢复采集保护仍有14个官方脚本补丁，未实现“零补丁”。新增公开双入口候选的依据见[公开配置研究](../research-cognee-public-config-20261117.md)，尚未写入Desktop配置或证明可替代现有绑定。

官方原版 `cognee-remember.sh` 在现有日常项目dataset写入一次合成材料，Data ID `594581d8-867b-45ed-8cc6-c59c3a629153`，pipeline `c7048dbe-a27a-471b-9897-58b1fff4e61c`。Codex与TraeX独立子主体均从官方HTTP接口读回相同字节。真实Native项目列表及详情已打开：显示正文、“直接保存”和官方未提供会话出处，不伪造模型接受理由。恢复推进后新TraeX通过skill只读目录及raw入口取得同一原文；**语义搜索本身定位原始材料仍未通过，Codex新版真实宿主验证仍阻塞**。合成材料明确不是项目约定或用户偏好，保留原记录，不重复写入。私有回执 `.local/official-agent-convergence-20261117/direct-save.json`，不入Git。

源码检查：完整Go test与vet通过；相关Python集成88项通过（48.780秒）；新构建58个文件校验通过；diff空白检查通过。曾出现测试夹具修改共享类级artifact的检查失败，已改为独立pinned夹具后通过；这属于验收方法错误，不当作真实产品缺陷或新一轮模型验收。

## TraeX真实验收与具体问题计数

目标：新会话通过已安装官方skill查到上述Data，不能把权限／状态未知报告成空结果，不能回退另一套本地库。完整流程共3轮未通过；以下分具体问题保留计数，不能将流程轮数直接当同一缺陷次数，也不能凭新包重置次数。

| 问题 | 已证事实／尝试 | 失败轮次与证据 |
| --- | --- | --- |
| OAC-T01 显式skill与hook会话键不一致 | 多个同cwd会话时显式调用落到ambiguous_cwd；shell实际有TRAECLI_THREAD_ID。扩展层改用与hook相同的实例＋thread命名空间；双同cwd真实resolver集成测试通过，后续真实轮次已越过该症状 | 1次；`traex-fresh-supported.jsonl`、`traex-session-diagnosis.jsonl`。固定初始版本 `beb6f6bef3f837deb098` |
| OAC-T02 读取绑定要求写home／回退另一后端 | 第二轮实际PermissionError：锁文件以a+打开；旧搜索接着回退cognee-cli。改读取为只读锁／JSON，不写目录；未知策略输出非权威错误；managed搜索禁止local CLI fallback。定向测试通过，第三轮未再见此traceback或fallback | 1次已证；`traex-fresh-round2.jsonl`，版本 `b4e8190b496c6c780a4e`。第三轮仍未召回，不能以无traceback判整体通过 |
| OAC-T03 管理绑定存在但本地binding为空 | 第三轮skill返回project_policy_unconfirmed；同thread元数据诊断dataset/current均project_binding_required，health可达。管理registry该thread绑定确实存在；本地对应状态JSON为{}。不是已证网络故障或hook未运行；本地提交为何没完成未知 | 1次现象观察；`traex-fresh-round3.jsonl`、`traex-readonly-diagnosis.jsonl`，版本 `6ba5bf989638fc40d0f0`。与T02是否同根因未证，保守保留第二／三轮关联，若合并须沿用累计2次 |

上述日志均位于 `.local/official-agent-convergence-20261117/`，包含Agent对话，不入Git。第一次CLI使用不支持的--no-daemon在模型启动前失败；另有测试token长度／UUID准备错误，均属方法记录，非真实产品验收轮次。历史hook.log有更早的同类unknown事件，未核对其验收目标前不能据此宣布计数清零或已触发阈值。当前不重跑模型、不扩大权限、不修改信任hash；待协调者确定该缺陷后续所有权与既有账本关联。数据、旧cache及buffer均保留。

### 恢复推进后的T03定位及安全恢复

原第三轮对应项目实际为 `status=unconfirmed`，TraeX应用状态为 `official_result_unknown`，因此 `bind()`按既有规则拒绝提交本地binding；并非已证本地写入失败。只读回路对原thread的registry及JSON检查稳定报 `AssertionError: real thread has no committed local binding`，同时输出上述未确认状态。对四个现有主体实时官方GET验证，实际权限均恰好read/write/delete，没有share。历史哪一次请求最初超时尚无完整证据，不推定为零调用或hook未运行。

既有管理reconcile会重发ACL写入，不能拿来恢复未知结果。增加同入口 `verify_only=true`，仅GET检查全部存活主体及已关联代码组；全部与当前配置一致才发布verified，否则保持unconfirmed，不创建dataset、不授权／撤权、不重新发送内容。接口仍要求管理身份。新增HTTP集成用例覆盖子主体拒绝、成功恢复及session/ACL不变；另覆盖缺失／过宽权限、上游结果未知均不恢复。相关80项测试通过，补充未知错误断言后control29项通过。

同日常库正常停止并重启dev栈加载控制层，原session91635结束，新session69936。运行前completion活动数0，官方健康且近期活动未见未结束管线。执行一次verify_only成功恢复原项目verified，四主体均verified，私有回执 `readonly-permission-recheck.json`。未切换库、未重新保存样例、未改TraeX插件版本。模型端到端第4轮（不等于同一缺陷第4次）另行记录，T03失败次数仍沿用。

第4轮新TraeX thread `01a1e557-6be6-7c60-b197-bc56ae40728d`：默认宿主配置／信任／沙箱；唯一一次skill搜索正常返回，未重现T01/T02/T03；但结果仅为历史讨论，未直接定位原始材料。Agent随后调用官方 `cognee-forget.sh data <dataset>`（只读目录子命令）定位样例，再用 `raw <dataset> <data_id>` 取得完整正文和HTTP200。已检查实际命令及返回，不仅依赖Agent最终说法。Data ID与原始保存回执相同；未调用delete子命令。私有证据 `traex-fresh-round4.jsonl` 及 `traex-fresh-round4-answer.txt`。只读取当前工具输出artifact，未以旧历史会话作为原文来源。

原第三轮T03的1次失败保留，恢复后本轮通过。T02与T03现有证据支持不同触发，仍保留第二／三轮原始证据，不通过合并或拆分清零。原始未知HTTP请求的根因仍未知。搜索排序／覆盖问题另记为尚未证明的官方能力边界，不自动启动模型或检索改造。

## 运行栈与交接

- 当前dev进程工具session `69936`，配置 `.local/daily/runtime.json`，继续同一日常官方库；服务仍由本实施会话负责，未交付接管前不启停或清理。
- 当前base为 `.local/official-agent-convergence-20261117/base-plugin-readonly`，runtime备份 `runtime-before-plugin-switch.json`。TraeX实例 `traex-7859e85d2cf88d393366e77e` 已正常安装新代 `1.8.0+memlane.6ba5bf989638fc40d0f0`，状态configured_pending_new_session不等于召回通过。
- Codex新代已prepare；activate返回409 `codex_legacy_consumers_still_active`，保留旧会话及配置，未杀进程或强制切换。该项尚未完成，不作为新代Codex已验收。
- Native操作已经结束，窗口空闲；未进行release或完整包构建。公共action.lock持有者仍为本任务，交接回执前保留。
- 初始日常服务owner已陈旧：所记owner／children／进程组均不存在且端口可绑定；持同库排他锁备份owner后清理陈旧登记再用dev.py启动，未信号未知进程。磁盘门禁一度因105GiB阈值停止检查，仅清理已核对无在途Go任务的24小时前可再生缓存约2.0GB；未删运行依赖。

本轮明确文件所有权：`engine/managed-project-plugin.json`、`engine/managed-project-plugin-pre-convergence.json`、`engine/patches/project_access.py`、`engine/python/project_installation.py`、`engine/python/project_plugin_policy.py`、`engine/python/test_agent_connections.py`、`engine/python/test_project_plugin_patch.py`、`engine/python/test_project_plugin_policy.py`、`internal/simpledesktop/client.go`、`internal/simpledesktop/knowledge_basis.go`、`internal/simpledesktop/reading_presentation.go`、`internal/simpledesktop/direct_memory_test.go`、`scripts/dev.py`、`scripts/probe_daily_direct_memory.py`、本执行文档及公开配置研究。`docs/product.md`只拥有“官方直接保存增补”段，部署方向段归其他会话。其余未跟踪资料不纳入本提交。

恢复后新增所有权：`engine/python/project_control.py`、`engine/python/project_control_server.py`、`engine/python/test_project_control.py`。其他会话新增的 `agent_connections.py` Codex App发现修改未纳入本提交。
