# D/G 使用事实展示与只读缺口核对（2026-11-11）

本批按 design-catalog 的 D05/G07 与 D-DAILY-REDESIGN、F-G-H-I-DESIGN 对照。实际打开普通/窄 G07/G08、普通/窄 G06，以及 D adopted.png、历史候选 delivery-failed.png。图片是设计夹具，旧 D 图导航不用于恢复“全部记忆”。没有 Native、模型或整包构建操作。

## 本批实现

`internal/desktop/usage.go` 保持现有默认收起的使用记录面板，分别展示检索返回、成功读取、报告采用三种真实汇总。StartedMS<=0 或未取得数据明确未知，不显示1970和已知零次；只有覆盖起点明确时0才显示0。统计窗口/保留期采用回执字段，不硬补缺失30/90；说明当前范围内同条记录各版本合计。App/连接验证独立，后台未采集仍未知。报告采用不代表核对代码修改。

`usage_events.go` 接 `Library.UsageEvents` 实际只读页，不从汇总或首页feed构造逐条事件。默认daily，App/verification分开切换；20条分页，按实际ID/版本去重。每项保留原观察版本和时间，折叠回执展示事件ID、知识ID/版本、scope、connection/gen及采用exposure。无法确认目标/频道/连接时不把记录归入日常。

页失败保留之前真实事实；更多页失败重试保留原cursor；签名cursor失效可刷新，目标版本/范围变化提供刷新当前知识。Root/memory/dataset/current revision/Sequence绑定回调，换库/恢复由主Agent重置。分页与展开不拥有导航或原文状态，没有新route，也不会以App点击上报Agent采用。

后台协议与实际SQLite/Unix IPC证据另见 [usage-events-20261111.md](usage-events-20261111.md)。本批没有更改usage后端统计口径，没有修改冻结Python。

## 验证边界

- 普通定向42099 exit0。
- 最终 `test -race ./internal/desktop -run 'TestUsage|TestSourceReaderKeepsKnowledge|TestHome'` 会话46480 exit0，desktop2.836s；前版35873亦exit0。
- 合成headless/Unix IPC覆盖0与未知、覆盖窗口、三事实分离、真实页字段、旧版本不改写、分页去重/失败保留/游标重试、错频道拒绝、迟到回执隔离。
- 1200×820及980×620 headless覆盖首页返回标记、所选知识、正文滚动、原文返回和已展开事实回执保持。

以上不关闭D05/G07 Native。仍需真实daily returned/read/adopted回执→首页→当前知识→独立原文→返回的同库连续窗口证据，以及普通/窄布局、分页、故障恢复与真实ID/version/connection核对。production Go已交主Agent冻结005；不以headless截图/断言当作Native。

## 冻结后的只读核对：D04/G06/G08

| 场景 | 当前真实实现/证据 | 实际缺口 |
| --- | --- | --- |
| D04 接收/同步失败 | pi-extension明确要求稳定operation_id、persisted才成功；服务保存幂等已存在。手动草稿另有持久化，但不是Agent传输outbox。 | 当前pi/Codex MCP路径没有可证实的适配方持久outbox/补发账本及Native本机暂存→等待补发→失败状态；不能承诺断连时“本机暂存仍保留”。需先逐适配方实现/验证durable暂存与同operation补发，再提供真实UI；不拿原型buffered当完成。 |
| G06 来源异常 | source reader保留owner/source版本、授权、来源变化/上游状态；核验时停止读/复制；既有source-reading-fidelity记录含实际撤权与版本证据。 | refreshSourceReader将scope_denied、服务失败等统一成“检查服务与权限后重试”；sourceReader对全部不可读状态都提供重新读取，未把权限/owner变化的不可原地重试与临时服务故障清晰分开。缺失/上游故障、恢复及普通/窄完整Native矩阵仍缺。 |
| G08 重要与关联对象 | markImportant通过ExpectedRevision CAS和明确确认保存；重要排序有SQLite测试。精确memory/revision实体入口及图→知识→原文返回有headless，真实图提取另待主线9。 | 知识详情只在memoryEntities非空时展示关联对象；空/未读取/图读取失败在该详情位置均不显示，尚未区分“确实无关联”和“未能确认关联”。独立实体图页已有状态处理，不能替代知识详情分支；重要切换/失败/关联空与失败仍待同包Native。 |

此表为源码/设计/旧证据只读审查，冻结期间没有补改上述生产路径，不降低既有来源/授权门禁。

### D04 再追失败链：已有持久能力不等于当前 Agent 前置 outbox

只读核对的实际链路是：pi Extension spawn `memlane call`，或 Codex MCP tool handler；二者进入 `agentbridge.Config.Call` → `serviceipc.Call(Op=agent)`。CLI helper同样只调用一次Call。Extension遇spawn/超时/协议失败返回错误，agentbridge保留retryable/code；这些路径没有写adapter磁盘spool，也没有调用App pending writer。

已有能力分层保留：

1. **daemon受理后的持久队列已经存在。** memory_save经ApplyAgentSave把正文版本、operation去重和jobs原子落SQLite，再返回persisted。worker失败时service仍可受理正文，之后重放索引；已提交后service SIGKILL也可恢复。`scripts/verify-daemon.py` 的d2c/d2f与P0记录证明这一层，不证明IPC不可达前Agent写入已暂存。
2. **App手动保存/编辑已有client pending intent。** `manual_drafts.go:865`的persistManualMutation先持久化确切Pending.Request/operation再Library.Apply，失败保留草稿与请求；这是手动草稿范围、库身份与重开回执恢复的实现，不是通用Agent outbox，也不能静默重放修改/删除/跨库旧请求。
3. **当前pi/Codex MCP与CLI的前置投递没有接入该持久intent。** 稳定operation可手动重试且服务端去重，但未送到daemon时不能承诺“接入方本机内容已保留”。后续应针对适配方明确冻结connection/generation/project/workspace及同operation请求，验收暂存、拒写、重开、撤权、补发和未知回执，再开放D04状态；不能直接借用P0下游队列结论。

因此本次结论不是“整个持久队列能力从未实现”，而是已有下游队列与App手动intent尚未接到当前Agent前置投递。没有为D04改变冻结源码或重启005构建。
