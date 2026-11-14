# E05 待归属 MyGo 工程记录

2026-11-11。已实际打开固定full-design-maintenance的project-mismatch普通/窄图并读取交互稿。backend schema30和home受控spool v2/claim/ack已由root合入；content仅在`build/goal-delivery-assignment-ui-20261111/`完成独立UI/flow/tests与App局部patch，未改生产路径。

独立“待归属提交”三栏由D04全局入口进入，中栏316/窄216，数量只来自本次真实outbox且失败未知不补0。原始类型/工作目录/原文保留，实际repo/worktree匹配由daemon核验，不由UI路径字符串断言。用户明确选择后仅真实匹配可确认，错选卡首屏可打开匹配项目；选择内部保留Scope.ID，显示真实Name，重名用实际Path，不展示内部序号/ID。

独立用户归属意图先journal/fsync、claim再Confirm/Ack，未知保持同operation/payload。重开只读恢复SQL已存但本机未Ack的独立回执，不改原Agent save范围/operation，不自动grant，不伪造原Agent PersistedConfirmed。root/life/epoch门禁及retire hook拒绝跨库迟到UI；a.navigate保留知识上下文，结果raw保持raw。

已修正并实际打开800图：去掉首次预览重复提示和标题，紧凑来源行，footer一句不增加Agent授权，匹配按钮Y约442、Y+H≤550可达。Pending/Receipt时显示重载连接提示；首次预览不显示。旧helper拒写只按delivery_storage_unavailable处理，保留本机原文，不杜撰升级码/丢失或成功。

最终组件**92339 exit0 desktop1.056s**，直接使用生产backend/home+UI overlay。压缩前flow已通过完整**87970 race exit0 desktop3.466s**及真实临时spool/SQLite/Unix IPC：持久先于发送、错选禁确认、unknown只读恢复+原操作重放不重复、原Agent状态/原文/raw不变、库epoch退休。UI压缩未重跑后端矩阵，未操作Native、模型、用户库或完整包。完整字段、命令、SHA、局部diff与剩余root hooks见artifact README；root仅合App小hooks与home单独D04入口patch。

状态为源码实现待root统一集成/新包Native验证，不以headless或既有006包提高E05 Native通过。saved后返回D04的焦点fallback需root结合稳定key接入；不将已消失的归属按钮视为可聚焦。
