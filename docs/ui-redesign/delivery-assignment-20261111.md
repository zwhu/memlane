# E05 待归属提交：独立用户决定与保存

2026-11-11。后端与本机spool按主Agent指定范围隔离实施，局部patch交主Agent统一集成；不是006原包或Native验收。本轮未完整构建、调用模型、操作Native或读取用户聊天／认证。

## 设计与可用事实

已读D-DAILY-DRAFT／REDESIGN、E-PROJECTS-DRAFT及`full-design-maintenance.js`的assignment，并实际打开`artifacts/full-design/final-maintenance/project-mismatch.png`和窄图。原型中的仓库、项目和提交为示例，不作为真实归属证据；旧“全部记忆”导航不恢复。新入口由主Agent定为独立“待归属提交”，普通内容栏仍保留知识／原始记录的实际Origin。

只接受自有私有D04 spool内没有grant、从未save尝试、没有未知发送结果的条目；原请求明确指向当前库preference／experience时拒绝E05，继续保留D04原状态。不能把普通全局授权失败转成项目待归属，也不能给已尝试／有grant的原请求换范围重放。

新确认是App用户独立operation，不是Agent补发或新授权。原connection／operation／request digest／review digest／正文／来源和Origin保留。新记录Source明确为“用户确认项目归属”，receipt.Source=user_assignment；不生成原Agent SaveReceipt，不设置原spool的PersistedConfirmed，也不写入原Agent operation namespace。空Origin遵循原memory_save默认knowledge契约；raw保持raw，索引不改变类型。protected Native／official／history来源拒绝，不冒充受信采集证据。

## 实际核验与事务

`DeliveryAssignmentPreview`只从当前daemon自有root的spool读取原文，RPC仅提交entry／review digest／所选项目；不接受调用方正文。预览复核捕获目录dev／ino、规范化实际workspace及Git common目录，与已登记project的真实Git common目录及实体匹配，同仓库worktree归同一项目。错选返回真实MatchingProject且CanConfirm=false；没有登记匹配、不是真实Git、目录变化或不可读均独立表达，不自动注册或猜项目。

确认Intent冻结canonical用户operation UUID、当前LibraryID、EntryID、RequestDigest、ReviewDigest、Project、WorkspaceDigest。spool先持久journal／claim并撤销原live owner；SQL保存前及同事务内再次核库身份、原source digest、项目登记和workspace／Git实体，提交前再stat。这里验证目录和Git指针实体，不推断内容主题或凭项目名称归属。

schema30新增`delivery_assignments`，保存用户独立回执与原Agent namespace关联；新知识及该回执同一SQLite事务提交。先查原Agent operation已有真实持久事实则拒绝新归属；反向在Store.applyTx的agent:新save入口核查独立归属账本，原Agent晚到legacy或D04请求被拒绝，不能再创建第二条知识。这是事务内双向防重，不把用户保存伪装成Agent成功。对其他已有知识、Agent scope和generation不作修改。

同用户op相同payload重放实际回执，换payload冲突；同entry或原operation不可另起用户op重复保存。SQL已提交后的恢复先查实际回执，再尝试本机Ack，因此目录后来变化或spool短暂不可读不会隐藏已保存事实。Ack失败返回真实saved回执及SpoolAckPending，仅本机关联待同步，不谎报保存失败或原Agent成功；明确重试仍沿同冻结Intent。

## 接口与状态

Library方法为`DeliveryAssignmentPreview(ctx, DeliveryAssignmentPreviewQuery)`和`ConfirmDeliveryAssignment(ctx, DeliveryAssignmentIntent)`。Preview含实际库身份、源摘要、原Item／Arguments、真实项目列表／MatchingProject、所选项目、WorkspaceDigest、CanConfirm和Code。常见Code：choose_project、ready、workspace_mismatch、workspace_changed、workspace_unavailable、workspace_unverified、project_unregistered、scope_ambiguous。original_already_saved只提供真实OriginalMemory／OriginalRevision，不能称用户归属成功；assignment_saved的Assignment是独立SQL历史回执，不代表当前目录仍通过检查。

只有精确真实匹配及完整确认字段时可新保存。claim／unknown重开保留原review digest；已提交Assignment可恢复同op Intent，不要求旧workspace仍存在。不存在任何自动授权、自动补发、自动确认或“已解决冲突”推断。

## schema30、备份与恢复

29→30严格新增表，不用IF NOT EXISTS掩盖伪旧库。备份格式保持不变，动态SchemaVersion边界包含30；schema30实际WAL快照保留知识与账本。管理恢复先迁移，再将独立assignment及payload显式retired，并沿用库identity轮换；旧Intent不可跨库重绑。退休源entry／原operation的唯一约束保留，不再提供重复归属。源spool journal由home模块独立库身份绑定，不把旧claim重新授权。

真实schema29旧库及其快照可迁移／恢复，创建空assignment表；旧程序仍按现有SchemaVersion上限拒绝恢复未来schema。已有13／23／26等手工降级夹具先Drop新增表；上一批project_preference测试的“当前schema=29”断言改为SchemaVersion，受控旧28／29 PRAGMA保持不变。

## 证据与未验边界

构件为`build/goal-delivery-assignment-20261111-001/`；`integration.patch`仅backend 24个目标，home spool为独立补丁。公共部分仅局部迁移／restore／applyTx gate、Request两指针／server两分支和必要旧库夹具，未整覆盖生产，未触碰主Agent新Graph标签实现。`production-targets.txt`、baseline／artifact摘要及report保留可审查边界。

- 36068：Store／daemon／IPC编译通过。
- 46697：真实SQLite／Git／schema30 Store专项通过，5.165s。
- 17286：Store及agentbridge race通过，daemon新测试最后List断言失败。测试保存raw却查默认knowledge，Items为0；原`race.log`保留，不记为全绿。
- 19612：修测试Kind=raw并加入旧project_preference迁移检查后，E05／D04／实际spool／真实Unix IPC／备份恢复与相关迁移专项race exit0，Store16.047s、daemon5.696s、agentbridge3.081s、serviceipc2.215s。包括本机journal版本屏障拒旧writer、原scope不改、调用方伪造正文被忽略、raw／来源保留、Ack故障回执恢复与重启、两方向原Agent保存防重。

这些为后台和临时SQLite／spool／Git／IPC实测，不代表普通／窄Native窗口、真实用户Agent日常操作或新的完整包已通过。UI由content／root接入并另行验收。

## 主集成与窄窗口对照

root已合入spool及schema30后台局部补丁，尚未构建新包。实际对照固定窄稿与组件图，发现上方重复说明将错选处理按钮推出首屏、项目选择泄露内部ID；已要求收敛并查看修正图：初始核对只保留紧凑来源/目录/匹配项目，确认与授权分开说明移固定footer，重载提示仅在journal之后的Pending或Receipt显示；选择真实名称，重名按真实路径区分，ID仅作内部映射。800图中错选处理按钮现首屏可达。组件截图不作Native验收。

主集成完成：home spool、backend24目标、content3独立文件与App局部hooks均按patch合入；ConfigureLibrary与restore同时退休归属状态。63148主仓组合race exit0：desktop3.998s、daemon6.155s、store15.399s、agentbridge3.750s，覆盖原D04、E05真实队列/SQLite/IPC链、类型投影与偏好备份。当前E05记已实现待验证；没有新的完整包或Native通过，006仍为最新完整包且不含本批。
