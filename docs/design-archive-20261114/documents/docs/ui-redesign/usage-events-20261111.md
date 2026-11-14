# D/G 知识使用事件只读页

2026-11-11，完整包004之后的源码批次。对应已认可知识详情/使用记录展示；本记录覆盖后台与私有IPC，Native截图和新包对照由主Agent统一验收。

新增`records.UsageEventsQuery`、`UsageEvent`、`UsageEventsPage`和`Library.UsageEvents`。请求必须指向一个确切memory；UI可同时传当前dataset与expected_revision。默认每页20、最大50，频道默认daily；app、verification与background分别请求，不能把验证活动混入日常使用。Kind可选，游标绑定完整有效目标、当前版本/范围、频道、kind和页大小。

事件直接来自既有`usage_events`，返回原ID、kind、channel、observed_ms、memory/revision/dataset、connection/generation、Agent与adoption exposure。Agent来自不可更改Agent类型的同一连接身份；连接不存在时为空，不能猜。没有查询文本、聊天或知识正文，也不从aggregate、首页动态或计数反推事件。采用仍只是Agent明确报告，不表示已核验代码提交。

和已有`Usage`一样，只查询memory当前dataset中的记录，可显示该scope内各版本历史；旧项目的事件不随知识跨scope移动而泄露。目标版本/范围变化使旧页失效。缺目标、SQL失败、游标错误均返回错误，不伪装成成功空页；确实无保留事件时Events为明确空数组。

同毫秒事件按`observed_ms DESC,id DESC,revision DESC`进行稳定keyset分页。首请求冻结时间与插入高水位，之后新插入事件不进入该次分页。游标由Store实例私有随机key签名，绑定数据库拥有者；重启/跨库游标失效，不能搬到另一知识或切换频道续用。key不进入SQLite、日志或回执，没有schema迁移。

保留原90天清理契约：读取只返回当前保留窗口内真实行，不执行DELETE、不重写usage_coverage、不延长留存，也不把90天当成完整覆盖。CoverageStartedMS原样返回，包括0未知；SnapshotMS仅表示此分页快照，不代替覆盖起点。真正删除/留存清理仍由原有拥有者负责，翻页不承诺保留已清理的历史。

定向检查：

- 22077真实SQLite分页专项exit0。
- 66120真实SQLite、私有Unix IPC、typed Library专项exit0（store1.338s、daemon0.950s）。默认daily、App browse原回执时间/ID/版本、verification独立空页、缺目标和过大页/过期版本失败均覆盖。
- 99221最终专项race exit0：store2.662s、daemon2.605s，serviceipc由真实typed IPC测试覆盖；差异空白检查通过。
- SQLite测试覆盖同时间53行无重复/遗漏、操作幂等不加行、首请求后同时间新行被高水位排除、游标换目标/过滤/页大小和修改签名拒绝、Store重开失效、各版本与采用exposure、未知coverage不迁移、90天到期不返回但读取不删行、移scope后旧游标/旧目标拒绝。

同时间、过期时间和跨scope变动在测试中明确使用受控SQL夹具；事件先由真实事务RecordUsage接受。这是分页/范围门禁测试，不作为用户真实使用数据或App移动验收。没有操作真实知识库、Native、模型或完整包。
