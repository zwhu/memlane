# 会话阅读旧 Goal → coding-memory 新规格交接

2026-11-12。用户经规格会话明确要求：完成交接后暂停旧任务，统一按 [coding-memory 与代码图谱接入规格](../coding-memory-codex-spec.md) 实施。本交接不代表旧 Goal 完成。交接收尾后调用 Goal 的 `paused`，不调用 `complete`，不再自主扩展旧范围。

## 1. 接手入口和最新边界

- 原 Goal：[会话阅读、官方数据与刷新](../goal-conversation-official-data-20261112.md)。保留全部历史证据及未完成事实；不要恢复整个旧125场景或把旧待办全部带入新规格。
- 后续唯一实施规格：[coding-memory-codex-spec.md](../coding-memory-codex-spec.md)。当前优先 Codex、正式受管模型通道、官方自动整理及日常项目连续使用；新规格当前不扩展其他 Agent、不迁移旧知识、不导入聊天历史、不做 Token 测试、不打完整运行包。
- 用户已允许限定官方自动生成知识同键替换并暂只读；人工知识／编辑／历史继续保护。直接日常环境升级，不另搭 HOME／数据库／服务作为前置门槛；升级前仍要一致备份并保留旧组件。
- 代码图谱由用户在项目内主动开启，新项目默认关闭；插件安装、连接、打开仓库或开启 coding-memory 均不能代替。复用官方代码索引／查询和官方 Web 图组件，局部 WebView 承载，不继续优化 Memlane 自绘图。具体版本、权限、资源与事件桥以规格10／11节及[Web承载研究](../research-cognee-web-mygo-graph-20261112.md)为准；会话正文仍用 MyGo Native。
- 用户规定的100 GiB门禁、task.py、同一具体缺陷第4次停止、保护第三方配置和运行依赖继续有效。禁止调用 sys-ai-retro。

## 2. 已完成与证据范围

### 实际 Native 阅读与刷新

- 会话列表、Markdown、工具默认折叠、独立滚动、固定标题／底栏、角色与单条原文复制已经进入实际 Native 候选。未知时间不填造；无法证明QA关联的工具仍作为会话独立组。
- Native044–048：真实pi自动采集材料3会话／3QA／7trace，共10条确认回执；两尺寸可读长代码／工具命令行末，横向位置在31秒／309帧及16次刷新中保持；实际系统复制7999字节与原文一致。047漏点复制按钮的失败回执保留，048补齐实际动作通过，不能用前轮结果覆盖。
- Native049–051：修复知识标题随正文滚走、长标题两行和窄窗口信息按钮，补列表选中条。两篇先前官方真实学习知识→真实2QA／3trace出处→返回，在窄窗口保留inventory查询、选择、信息展开及47.261353非零滚动。普通来源及窄窗口跳转前／后、真实服务暂停失败／同PID恢复，共5段31秒，每段309全帧；标题／正文／数量按范围检查稳定。
- 049–051的原始QA／trace是人工合成验收材料，知识由此前官方真实处理生成；不能把这组证据称为日常 Codex 自动采集→自动整理已闭环。
- 证据入口：[pi专项](pi-official-capture-gap-20261112.md)、[知识专项](official-knowledge-reading-20261112.md)、[刷新复盘](reading-refresh-flicker-20261112.md)。原始帧与回执在 `build/conversation-official-20261112/pi-capture-native-044` 至 `048`、`knowledge-source-native-049` 至 `051`。

### 当前源码兼容增量（已检查，尚未 Native 新格式验收）

实际新知识在旧标题前带`Lesson-Type`和可选`State-Key`，旧解析器会漏掉这类知识。已添加薄格式适配，不新增采集器、提炼器、全文库、协议或schema：

- `internal/official/coding_knowledge.go`：固定新封装与metadata／node_set唯一会话一致性核对；仅投影已有类型、状态键、日期、Git和文件元数据。冲突不猜；可选字段非法时不展示；不据此授予访问或证明AI质量。
- `internal/official/documents.go`：旧、新模板引用兼容；读取授权／唯一所属会话和前后身份核验仍沿原路径。
- `internal/official/knowledge.go`：只去除匹配的固定封装；原文保持完整，知识复制保留正文原字节。
- `internal/desktop/official_knowledge.go`：类型显示为项目状态／避坑经验／变更记录等；状态键、Git、日期及文件放记录信息。现有IPC的externalMetadata已经足够，无需新增返回字段。
- 回归在 `internal/official/coding_knowledge_test.go`、`knowledge_test.go`、`internal/desktop/official_knowledge_test.go`。两篇真实官方合成知识的最小夹具在 `internal/official/testdata/coding-memory-ec6f408/`，来源与SHA有记录，移除了本机rawDataLocation；它们不属于产品存储。

结果：

| 检查 | 结果 |
| --- | --- |
| 受影响official／desktop／daemon完整测试 | 1.324／34.443／70.323秒，exit0 |
| official／desktop相关race | 2.106／2.250秒，exit0 |
| 全仓`sh scripts/go.sh vet ./...` | exit0；没有运行已停止的旧hook测试 |
| 最后UI元数据补充后的desktop定向／两包vet | 1.122秒／exit0 |
| 固定上游真实render函数与两篇封存原文逐字比较 | 2篇一致，无SDK import、模型或服务写入 |
| 开发CGO e2e GUI与daemon编译 | exit0；仅开发二进制，未启动／未构建完整运行包 |

证据：`build/conversation-official-20261112/coding-knowledge-reading-001/{checks,official-renderer-compatibility}.json`。第一次新格式回归先红，属于读取兼容缺口1轮；已通过源码复验，不加入上游知识质量失败2轮。render检查初用系统Python3.9不支持`int | None`，改用项目3.12；是检查方法错误，不是产品再失败。

最新开发二进制：`.local/session-processing-20261112-001/memlane-app-coding-read`及`memlaned-coding-read`。没有把它们复制到当前日常服务路径；仅编译不证明可切换。二进制SHA及源码SHA见checks.json。

## 3. 仍未完成

- 日常服务／配套官方插件升级、App选定Codex正式推理、官方自动improve经受管通道执行、真实取消与恢复；封存报告中的临时脚本不能成为产品依赖。
- 新coding-memory格式的实际官方服务→当前Native知识→真实出处→返回与替换状态验收。本轮只完成封存真实样本、协议／组件检查和代码编译，没有制造Native业务证据。
- 同键新ID替换后的UI连续流程及当前来源可用性；不得只凭state_key猜替换、QA归属或权限。
- 官方代码图谱显式开启／关闭、Codex实际查询、更新与官方Web画布整合；没有启动扫描或改自绘图。
- 四稿成套最终对照、正常运行入口的一体化验收及最终可交用户审阅的日常结果仍未完成。旧隔离Native探针“服务未连接”来自未配置RuntimeController，不代表服务状态链已经交付。
- 原旧Goal的其他Agent扩展、旧知识正式迁移／编辑历史合流不在新规格当前实施范围，保留现状和历史证据；不要借交接清理或删除数据。

## 4. 未提交代码与工作树边界

当前HEAD为`efc1b4985df00fa6cec65db525f1fdf9495de7e2`，工作树含大量先前及并行修改，未作提交。`git status`和源文件摘要在 `build/conversation-official-20261112/coding-memory-handoff-001/{git-status.txt,source-fingerprints.json}`。该清单明确不是所有修改都归本会话所有。

本次最后一段只改上述格式读取／知识UI／回归／合成testdata及工程账本。此前本会话还涉及conversation／official documents／pi capture读取授权、schema33–35轻量回执与grant、相关CLI／IPC／服务模块；按专项账本逐文件核对。设置、`browsing.go`、模型通道、组件构建等另有历史或并行修改，禁止整文件覆盖、reset、clean或按HEAD新建一个缺少必要未跟踪输入的工作树冒充当前实现。

## 5. 运行进程、数据、备份与依赖

### 保持运行的共享服务（只读核对，未停止）

| PID | 当前所属 |
| --- | --- |
| 76077 | memlaned；lsof实际exe为`build/goal-native-runtime015-20261111/MemlaneConcentratedTwelve.app/Contents/Resources/bin/memlaned` |
| 76080 | 上述服务的Rust worker，组件位于下述临时根 |
| 76867 | Cognee1.6.7-memlane.12官方服务，完整组件位于下述临时根 |
| 77407／77408／77409 | 76867的资源跟踪／worker子进程 |

实际共享数据／组件引用根：`/var/folders/nv/g6m5_3nx1r9_v9dk3zh_5zw40000gn/T/memlane-task-gnwqmjhd/initialization/client-responsive`。76077的sqlite-shm也仍映射此根；绝不能因目录在/tmp、GUI退出或命令名短而删除。完整依赖集合继续保护，见 `build/disk-cleanup/retained-dependencies.txt`。

当前没有本会话拥有的GUI、daemon、官方服务或测试任务在运行；049–051均已退出，故障暂停在停止前恢复同PID。051 stop-receipt确认原Native探针配置已逐字节恢复。最后检查磁盘约125 GiB可用；后续每次仍实际复测，不能引用此数字当门禁。

### 保留数据及备份事实

- `.local/pi-capture-real-20261112-001`：schema35，实际3会话、10条官方capture receipt、2条原consent、1条读取grant；客户端正文／版本／jobs均0。原binding／consent／receipt SHA曾与040基线相同。重连后的generation3 read grant只允许原保存材料，不授予future capture或历史。
- `.local/session-processing-20261112-001`：本轮从schema32升级35；两篇官方学习知识和人工原材料保留，旧处理任务仍2 completed／1 failed，memories／versions／jobs／计划均0。源库和官方数据没有删除。
- `.local/dev`及旧31条迁移原库没有在本次收尾删除；旧31条曾做隔离导入／重启核对，不能称日常库已切换。现时内容以实际库为准，不能把旧SHA当成当前业务状态。
- 本次只有源摘要、测试回执和探针配置前像；**没有制作整个日常环境的一致备份，也没有完成日常升级前备份或实际回滚。** 先前单库／迁移演练／保留旧包不能替代新规格要求的升级备份。
- 不读取或输出凭据值，不迁入日常Agent历史，不清理其他session产物。Cognee新SDK import须先隔离日志副作用，见[日志事故](cognee-sdk-log-isolation-incident-20261112.md)；不要用直接import把用户旧日志再轮转掉。

## 6. 停止问题与失败计数

| 问题 | 保留计数／边界 |
| --- | --- |
| 旧Codex自有hook安装`capture_bridge_unavailable` | 至少4轮，停止；不重跑该探测及会包含它的全仓test。见[记录](codex-hook-bridge-stop-20261112.md)。全仓vet可独立执行 |
| 固定328B README实体／关系生成原链 | 4轮，停止；见[重复失败](repeated-failures-20261111.md)。这是旧语义图谱问题，不能误称静态代码图谱G2失败4轮，也不能借新官方图页面重跑它 |
| 同身份helper更新／同路径回退启动 | 超过3次且已达到停止阈值；精确历史总数未去重，不伪造。见同一[记录](repeated-failures-20261111.md)。不以新签名／路径继续实验 |
| coding-memory知识完整性／纠错召回 | 2轮；临时Schema适配问题1轮，原证据保留；没有本轮模型重跑 |
| 旧UI整项“4次失败”停止 | 已因不同问题被错误聚合而撤销；详见[纠正](conversation-ui-stop-20261112.md)。按具体缺陷继续累计，不清除真实失败 |
| 知识标题滚走、工具转义、标题／数量等UI问题 | 各自专项账本保留原失败次数与修复证据，不聚合成整项4次；049知识标题问题1轮已源码与Native复验 |
| 本次typed知识漏读 | 1轮源码红检查后修复；新格式Native仍未验，不宣称已交付 |

新规格追加代码图谱、放弃自绘和直接日常环境，不自动清零这些计数，也不要求把无关停止问题当新规格阻塞。

## 7. 新规格建议接手顺序（不在旧Goal继续执行）

1. 完整读取新规格及引用，核对当前共享运行根、可执行组件、授权、实际Codex插件／连接和项目／dataset映射。明确同一运行拥有者，先停止写入并制作一致备份／保留可恢复旧组件，再串行升级日常服务；不新搭运行环境作门槛。
2. 复用本次读取兼容代码和既有模型runner／配置账本，优先接通正式后台模型生命周期。实际官方请求要绑定当前启用Codex配置及撤权／取消；不把验证脚本或仅App手动整理当官方自动触发通过。
3. 升级固定配套官方插件与服务，按能力实测授权、信任／重载、防双采集、防处理上下文循环及官方自动improve；不要回到自有Codex采集器扩功能。
4. 在明确日常项目取得新真实产物，验正文／类型／状态／来源／返回、同键替换、失败与重开；继续保留上游质量失败记录，按新规格完成两项连续开发与新会话实际复用。
5. 按用户主动开启契约接官方代码索引／查询与官方Web组件、来源和关闭门禁；核对SDK／Web固定输入组合及coding-memory关闭autocodeindex的事实，不同时维护第二份图或默认开始性能／外观重写。
6. 分开完成源码、协议、模型／官方服务、实际Native证据；完整包等用户明确release阶段再开始。旧Goal的暂停不等于上述任一新步骤已经获验通过。
