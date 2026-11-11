# E06／L02 项目约定与个人偏好：证据缺口及最小契约

2026-11-11。只读调查及拟议契约，不代表接口、持久化或Native已实现；本轮未新增模型调用、语义检测、Agent工具或schema。

后续授权更新：主Agent已选定本文的最小用户确认路径，隔离records／Store／IPC及schema29决定账本已实施于`build/goal-project-preference-20261111-001/`，最终受管85973专项race通过，局部patch未应用生产。该更新不增加Agent报告工具或语义检测；下文保留最初调查时点和未采用的可选Agent报告方案。生产UI／Native验收仍待主Agent接入，不将隔离实现写成场景已交付。

## 设计与实际证据

已精读当前原型`E-PROJECTS-DRAFT.md`、`e-projects.js`的`conflictHTML`及轻量入口，并实际打开`artifacts/e-projects/20261110-004/project-and-global-rule.png`与`narrow-projectonflictprojectiewconflict.png`。设计要求两张独立内容／来源／适用范围卡片、项目采用标记、返回知识及查看不覆盖／删除。截图使用旧导航名称，仅参考正文交互；当前生产仍遵循“通用知识”、无“全部记忆”导航规则。

原型以`projectMemories()[2]`选取项目内容，标题及个人偏好npm文本、采用徽标均为固定示例；轻量提示由`projectConflict=1`、指定项目和选中序号触发。这不是生产冲突检测、真实采用或规则身份的证据。

| 当前生产事实 | 可以证明 | 不能证明 |
| --- | --- | --- |
| `records.Version`的memory／dataset／revision、scope kind、Source／SourceDetail | 某条具体知识、版本、范围及已有来源 | project类型不表示其中每条知识均为明确规则 |
| `Store.Authorize`与Agent `ContextProject` | 可信当前项目与明确授权范围；默认读取当前项目及已授权通用范围 | 同时返回项目和偏好不表示二者冲突或已选择项目约定 |
| `ProjectKnowledgeContextPage/Receipt` | 当前项目知识上下文及发送完整性／摘要 | 规则身份、项目－偏好配对、差异原因或采用决策 |
| `memory_adopt`／`UsageObservation`／`usage_events` | Agent明确报告采用单一memory／dataset／revision，且关联同连接代次／workspace的近期read或return回执 | 第二条偏好、差异、原因、适用条件、相比偏好选择了项目规则；不证明实际代码或提交遵守 |
| organization `conflict`建议 | 当前整理任务的候选及审核状态 | 不是项目－个人偏好独立配对；确认该类建议也不产生本项目采用决策 |

实际代码依据：`internal/agentbridge/bridge.go`的`memory_adopt`输入只有operation_id、memory_id、revision、exposure_receipt；`internal/daemon/agents.go`的observe及adopt分支；`internal/store/usage.go`的`recordUsageTx`检查单target、当前有效版本、scope、generation及匹配exposure。workspace保存为当前项目与规范化目录的摘要，不是事件中的可逆项目标识。事件保留90天，不能据此构造永久项目规则。个人偏好没有与项目规则配对的结构化适用条件；`ExperienceInfo.Conditions`属于通用经验，不能冒充个人偏好的已记录条件。

结论：尚不能从既有表构造完整可信的E06／L02只读事实接口。不实现“搜索词相似即冲突”、固定pnpm/npm、不默认全部项目知识为规则，也不由单条adopt推导二选一采用。

## 最小可实现路径：显式用户确认

优先只增加一个独立的配对／适用关系确认账本，不需要增加语义检测或Agent工具。用户明确选择一条当前项目知识和一条个人偏好，阅读两张卡后确认“此条是本项目明确约定；在所述条件下，本项目按它执行，个人偏好继续保留”。确认前只叫“记录对照”，不提示已存在冲突；确认结果注明“用户确认”，不称Agent实际执行。

拟议请求（未落Go类型）：

- `OperationID`、`Project`、`ProjectRecord`与`PreferenceRecord`（复用`records.UsageTarget`三元组）；两条记录各自ExpectedRevision必须精确。
- `ProjectQuote`、`PreferenceQuote`：各自原版本中真实存在的引文；可将完整正文作为卡片，不要求模型提取。引文只能证明文字存在，不能自动证明语义冲突。
- `Conditions`、`Reason`：用户明确填写或确认的本次适用说明，不回写任一原知识。原知识未记录条件时显示“未记录”，不能补造“其他项目默认”等条件。
- `ConfirmProjectRule`与`Decision=use_project`：必须是本次显式确认，不能由打开对照、知识scope或项目优先总规则默认勾选。

拟议回执保留独立ID、上述两条冻结引用与摘要、确认来源`user`、服务时间及适用说明；它是适用关系元数据，不是新增／覆盖／删除／移动知识。当前显示须重新检查两条知识的版本、范围、active／derived状态与授权。任何一条编辑、移动、删除、失效或授权变化都显示旧决定“待复核／历史确认”，不得把旧徽标转移到新版本。恢复知识创建新版本，同样需要重新核对。

在此契约下可显示“本项目采用 · 用户确认”，含义仅是明确的项目适用决定；不能显示“Agent本次实际采用”。确认之前允许普通只读对照，但不提供“已确认冲突”或“本项目采用”事实。

## 若要展示Agent本次报告的采用

这是独立可选来源，不能由上面的用户确认伪造。最小报告需要可信host项目上下文、认证连接ID／generation／workspace identity、两条冻结引用、两个匹配的read／return exposure、真实引文及报告的差异／条件／选择；稳定OperationID摘要必须冻结全部内容，重放不可换配对。两个exposure可以来自同一搜索回执，但必须分别存在对应target版本。

报告还须关联现有真实project记录的adopt回执，并由服务核对同一连接代次、同一可信项目workspace、同一具体项目记录及版本。若没有匹配adopt，只能显示“Agent报告存在差异／建议按项目约定”，不能显示“已采用”。若有匹配adopt，标注“Agent本次报告采用”，不独立断言实际编码行为，也不自动提升为永久项目规则。报告中的Project不能取caller自报路径；须按现有Authorize与可信host cwd映射核验，严格要求项目记录在该project、另一条在已授权preference scope。

Agent报告是声明，不直接确认为用户项目规则。需要持久“本项目明确约定”时仍走上面的用户确认；不能调用原知识Mutation代替该确认。usage过期、撤权、restore退休或记录CAS改变时必须展示证据缺失／历史状态，不补零、不从aggregate或现有知识猜测原配对。

## 后续只读接口与门禁

只有建立上述真实账本后，才增加独立的`ProjectPreferenceComparison`查询：精确绑定project、两条记录当前三元组、授权覆盖与具体确认／报告ID；响应分别返回两条Current／Frozen证据、来源类型、适用说明、证据状态及可展示徽标。查询不生成报告、不确认、不写知识；没有配对时为“尚无已确认关系”，读取失败与版本／授权变化独立报错，不能表示“不存在冲突”。

最小验收需要真实SQLite配对确认／重放／请求冲突、跨项目或非preference拒绝、无授权拒绝、任一版本／移动／删除／restore失效、引文不符、仅打开不变、Agent两份exposure和adopt不可互借、证据过期未知、两条正文及版本始终独立。未建立契约前E06／L02保持缺实现，不以双卡渲染或项目优先说明关闭场景。本轮仅形成该契约，不实现没有事实来源的API，也不预占下一schema版本。

## 后续实施时点：已隔离实现，尚未合入

主Agent随后明确选定最小显式用户确认路径，已于`build/goal-project-preference-20261111-001/`实现独立records／Store／IPC overlay、schema29决定表、备份迁移及整库restore退休；生产公共文件未由本任务修改。用户确认只写独立决定、不改两条知识，版本／库身份／scope严格绑定。新增`Library.ProjectPreferenceDecisions`按当前项目知识ref发现本库、当前双版本、active／knowledge／非stale、可读范围内的明确确认；响应CoverageDatasets，无当前确认不能推断不存在差异／冲突。`ProjectPreferenceComparison`读取双卡，`ConfirmProjectPreference`写用户显式决定；没有新Agent工具、语义检测或Agent报告方案实现。

受管52414专项race已通过，包括真实Unix IPC、当前配对发现／过滤、只读及独立决定、重放／冲突、严格目标检查、重启保持、单条恢复新版本、schema29备份／整库恢复退休、schema28迁移及旧快照恢复和schema13／23／26迁移边界。此后为避免遮盖主Agent新N/K源码，按其当前公共文件刷新overlay并做最后定向检查；最终回执见该目录`report.json`。局部patch统一交主Agent集成，content仅接desktop。前述“本轮只形成契约”仅指最初调查阶段，不再代表当前隔离实现状态；生产合入、Native及场景交付仍分别待验。
