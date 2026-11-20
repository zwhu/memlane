# 两个工作会话统一 Handoff

2026-11-17。用户最新要求“把目前两个session的内容都handoff”。当前停止新增开发、测试、模型调用及安装切换，仅整理交接和必要安全收尾。接手者先读本文；旧协调文档中的暂停、PID和阶段可能已过时，以本文核对时事实及原账本为准。本轮没有实施未完成项。

## 会话与代码基线

| 角色 | 会话 | 本轮状态 |
| --- | --- | --- |
| A 官方接入收口 | `01a1e4cd-8134-7730-87e8-4dd491b2cf24` | 已完成限定显式保存／跨Agent原文读取；其余待办交接 |
| B 首次安装三项任务 | `01a1e554-3a9e-7eb2-9315-ff8a9eb565e4` | 已回执无在途任务，发现修复及任务账本未提交 |
| B 的旧画像／范围会话 | `01a1e4ba-d768-7d81-9182-d040fe2fdbb3` | 已回执无在途任务；只作历史补充，不能覆盖B的新结果 |
| 原总协调 | `01a1e54f-dbfd-7303-86e0-84ad3528e5a1` | 已通知本次交接及运行状态变化 |

交接时本地 HEAD 与本地 origin/main 引用均为 `2516c446ecd4a5e60fe5bacc6b770a558d772af7`。A 已实际成功push该提交，含20个明确路径；前序方案基线 `ced7bfc`，快照 `04746da`，本地注释tag `pre-official-agent-convergence-20261117`。本次没有再次联网核对远端，也没有替B提交源码。

## A：官方接入收口

已实现／已证：

- 官方remember直接保存的 `user_context`／现有adapter `memory` 进入Memlane列表、统计、搜索范围及详情，标记“直接保存”；不伪造模型提炼、接受理由或会话出处。原始资料不自动转记忆，没有新增正文库。
- 撤销自定义召回候选数22及未缓存读取数4的覆盖，恢复固定官方默认；十个skill文本未改。仍有14个官方脚本补丁，不能称“零补丁”。公开shell环境＋外部hooks候选只完成研究，未实施。
- 修复TraeX显式skill／hook会话键不一致、读取绑定要求写home，以及managed搜索退回另一套本地CLI库的问题。
- 原项目权限状态因 `official_result_unknown` 未确认，插件正确拒绝本地绑定。新增管理reconcile的 `verify_only=true`，仅GET核对全部存活主体及代码组，全部一致才恢复verified；不重发授权或内容请求。原最初未知HTTP请求的原因仍未证明。
- 同一日常库中原版官方skill保存一次合成材料，Data ID `594581d8-867b-45ed-8cc6-c59c3a629153`。Native实际列表／详情可见；Codex及TraeX独立子凭证读回一致；新TraeX thread `01a1e557-6be6-7c60-b197-bc56ae40728d` 经官方skill目录和raw入口读到同一原文、HTTP200。没有重新保存或替换库。

未完成：语义搜索只返回历史讨论，目录定位不等于搜索命中；连接卡仍 `configured_pending_new_session`；Codex新版仅prepare、激活409阻塞；自然语言主动保存到Cognee与自动采集→处理→召回→采用没有完整验收。官方升级的低耦合目标仍未完成。

证据：[A执行账本](official-agent-convergence-20261117.md)、[职责方案](../research-official-agent-responsibilities-20261117.md)、[公开配置研究](../research-cognee-public-config-20261117.md)。检查包括相关88项、后续80项和补充control29项Python检查，以及Go test/vet、58文件构建校验；存在重叠，不相加。后续未改Go时沿用原检查证据。实际私有日志在 `.local/official-agent-convergence-20261117/`，不提交真实会话、凭证或记忆库。

## B：首次安装、Agent主流程、TraeX保存

- **Codex App-only发现已修**：`detect_executable`补 `/Applications`、`~/Applications` 下Codex.app／ChatGPT.app的内置运行时路径；保留显式配置优先、错误不回退及可执行权限检查。发现／环境8项测试通过，遮蔽PATH的本机发现通过；没有执行该运行时证明完整接入。
- **本地首次准备未实现**：现有dev启动器要求运行时、Cognee与日常库已经存在。临时缺路径检查为FileNotFoundError；不下载、不建库。SQLite仅内存能力检查，不代表空白电脑首次准备通过。正常启动拒绝缺库的保护不能直接删掉；需要独立显式准备入口。
- **Docker／远程仅方向**：先本地，复用同套API；Docker涉及持久卷、loopback、宿主模型桥与宿主Python依赖，尚未实现或部署。不扩展多租户。
- **已定位一条TraeX保存去向**：2026-11-16 22:08，thread `01a1e4ae-f786-7cf0-ba49-af710cd30a74`，Agent实际写入并读回TraeX宿主本地笔记，没有调用Cognee remember；自动提炼另有0文档记录。不能描述为“Cognee保存成功但App丢失”。该事件与用户所说“下午”时间不一致，原事件覆盖仍有缺口。原笔记不补交、不重放。

证据：[B三项执行账本](agent-acceptance-20261116.md)、[部署方向](../deployment-options-20261117.md)（原 `session-coordination-20261117-validation-status.md` 临时协调记录已在后续清理中移除）。B已两次明确回执：无在途命令、测试、模型、写入、Native操作，不持有服务或锁。

## 未提交文件及保留规则

以下是当前工作树事实，接手时需重新检查；不能因为A已push就忽略B未提交的改动。

| 路径 | 状态／所有权 |
| --- | --- |
| `engine/python/agent_connections.py` | 未暂存，B新增4行发现修复；B已明确交接 |
| `docs/engineering/agent-acceptance-20261116.md` | 未暂存，B新增52行三项范围与证据；B已明确交接 |
| `docs/product.md` | 未暂存，仅部署方向段；A直接保存段已经在2516c44中，勿混淆 |
| `docs/deployment-options-20261117.md` | 未跟踪，部署方向；保留 |
| `docs/engineering/session-coordination-20261117*.md` | 临时协调及旧验收回执（2026-11-20 已清理） |
| `docs/engineering/two-session-acceptance-report-20261117.md` | 独立监看报告；报告结论“部分交付、整体未通过”，不当作实施已完成 |
| `docs/research-hindsight-vs-memlane-20261117.md`、各历史evidence目录 | 既有研究及已精简的历史 evidence（冗余副本 `internal/simpledesktop/docs/` 于 2026-11-20 已清理） |

本次交接入口本身也是新增文件。上述未提交/未跟踪材料仍在本机，不保证仅克隆远端能恢复全部交接。旧真实日志、原笔记、库、来源关联、cache与buffer均保留；禁止git add -A或清理这些材料。

## 具体问题失败计数

| 问题 | 已知累计与状态 |
| --- | --- |
| Codex App-only发现 | 历史1＋B修前复现1＝2轮；修后定向通过，真实宿主链待验 |
| INIT-PREPARE01 | 现状核验失败1轮，首次准备未实现 |
| OAC-T01 | 1轮失败；后续越过会话键症状 |
| OAC-T02 | 1轮已证失败；后续未重现锁写异常／本地fallback |
| OAC-T03 | 第3轮1次失败保留；GET-only恢复后第4轮未再现。原本地binding为空归类已纠正，原证据不删 |
| 语义搜索原文未定位 | 第4轮明确观察1次；与历史同类问题是否同根未核对，不据新名称清零 |
| TraeX自然语言保存 | 历史事件定位不是修复验收；没有本轮新增复验次数，不能用A合成材料代替 |
| UP01／UP02／UP03 | 历史分别1轮DTO问题、2轮定位方法失败、1轮TraeX画像失败；详见旧画像交接，不与主动保存混合 |
| MA-OLLAMA-CODING-QUALITY01 | 累计4轮停止，继续保持停止 |

同一具体问题第4次验收失败就停止该问题的实现、实验及重跑并汇报；不是整个流程第4轮就自动停止，也不能换版本／会话／命名重置。原先TraeX前三轮未过、第4轮原文读取过，不等于同一缺陷失败4次。

## 最新运行现场与锁

此前A成功启动的dev工具session为69936，owner登记92567（2026-11-17 01:10:11），登记子进程92579／92580／92985；配置 `.local/daily/runtime.json`，base为 `.local/official-agent-convergence-20261117/base-plugin-readonly`。

**本次交接重新只读核对：`dev.py status`报 `no active launcher owns this store`；上述4个PID均不在进程表；service与completion的health均URLError。** 旧owner登记仍在。退出时间、原因及是否有其他未登记消费者未知；不能继续按“服务仍运行”安排验收，也不能仅据这四个PID缺失认定整个运行依赖可删。本次未重启、未删除旧owner、未向进程发信号。

公共 `.local/continuous-loops/action.lock` 已验证只属于A、本目录仅owner.json，元数据归档至 `.local/official-agent-convergence-20261117/handoff-lock-owner.json` 后释放。旧日常运行owner登记、完整依赖、TraeX新旧cache、Codex旧配置和buffer保留。锁释放不是允许绕过未知保存、清理依赖或自动重启服务。

## 接手顺序与Codex切换

1. 读AGENTS.md及上面A/B账本，检查Git并接收B未提交改动；先明确所有权，再取得公共动作锁。不自动扩大到旧全量Goal、release、远程、多租户或已停止质量实验。
2. 若恢复实际验证，先检查单一日常库、残留owner、PID启动身份／进程组／端口及运行依赖。确认无其他消费者后按dev.py既有安全路径恢复；无法确认就保留现场，禁止复制新库展示“恢复”。
3. Codex新代已prepare但曾激活返回409 `codex_legacy_consumers_still_active`。这是本项目保护，实际检查旧插件的idle-watcher、exit-watcher、sync-session-to-graph、store-user-prompt等进程，不等于必须杀全部Codex。先核对在途保存和旧会话，再正常结束相关会话，等待消费者退出；从Memlane或不占旧插件的终端重新核对预览／配置摘要并激活。配置变动则重新prepare。保留旧cache及buffer，不伪造信任hash。
4. 新Codex会话分别验证新代实际加载、主体／项目、读取和保存；旧会话读回不替代新代验收。先前未知保存不重投、不自动迁移／补采／重放。本handoff并未执行关闭会话或激活。
5. 其他待办为本地首次准备、自然语言保存目标、语义搜索准确定位、连接卡回执、实际窗口状态及完整Agent主链。按依赖组织集中验收，不把样例原文读取当所有目标已完成。详见[独立验收报告](two-session-acceptance-report-20261117.md)。

可供新session直接使用：在 `/Users/bytedance/work/AI/memlane`，先读本handoff及A/B账本；保留未提交修改和单一日常库；先确认运行资源，再承接上述未完成项。报告必须区分实现、源码测试、真实宿主、模型质量、Native与release；沿具体问题累计失败计数。用户本次仅要求handoff，不据此启动上述待办。

## 2026-11-17 用户恢复推进后的更新

用户已要求继续推进，并明确结束旧进程后“直接切换”。上文暂停与激活阻塞现已属于历史状态：Codex原prepared预览经摘要及消费者核对后成功激活，仍等待新会话；同一日常库服务已恢复，健康与认证读取通过。详细过程、工具session39851及未证明边界见[A账本新增段](official-agent-convergence-20261117.md#2026-11-17-handoff续作codex已激活)。不得将本次配置切换当作真实新会话通过。

本地首次准备续作见[独立执行记录](local-prepare-20261117.md)。本轮实现组件准备入口，不代表首次存储／App引导或完整Agent主链已完成；B未提交改动及所有原材料继续保留。
