# daily-cli 侧栏黄点只读核对

2026-11-16，用户询问下午可见黄点，未操作为何消失。未修改实现、权限、运行配置或官方数据，未补采、重发或启动模型；未操作其他会话持有的Native窗口。

实际读取当前管理 /v1/projects 与项目 backlog，原日常官方 /datasets/{dataset}/data，以及 Native 相同的已安装 _memlane_policy.py --action show：daily-cli project e7bec60d-3c04-544b-a3f6-cffc717ffea2、dataset 933effd1-de17-550c-8c8a-fbc9390de5f7，status=verified。四条活动Codex绑定均退出0，pending=0、unknown_outcomes=0、blocked=null。另一个旧pi主体已归档，不在活动agents，按当前UI规则不纳入队列；其绑定保留。backlog job=null、automatic_enabled=true、checks=[]。

官方当前6份对象：1份session_learnings、3份user_sessions_from_cache、2份agent_trace_feedbacks。按当前统一derivedMaterial规则无独立待整理原始文档；不能把这6个对象相加当作6份待整理，也不凭数量证明历史所有材料完整。

实现：sidebarQueueBadge count>0显示数字，count=0且DocumentError或会话未读/读取错误显示!（UI绘制橙黄点），count=0且无错误隐藏；未加载初始态也可能无标记。后台刷新可改变此提示，不要求用户操作；它不是持久的未完成任务凭证。

结论：当前四条活动绑定的保存队列确实为空，官方当前没有独立待整理文档，符合隐藏条件；本轮没有删除材料。下午黄点对应的具体DocumentError/会话ReadError没有找到按时间保留的快照，因此不能确定当时是瞬时读取失败、旧主体状态、尚未加载还是其他错误，不能把旧pi退役或某次修复直接认定为本次消失根因。确认当前条件不等于还原历史原因。已知产品可观测性缺口：状态提示不保留出现/消失原因，用户无法区分刷新成功与状态未加载。
