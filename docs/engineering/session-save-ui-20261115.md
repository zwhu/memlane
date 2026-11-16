# 会话保存区可读性优化

## 2026-11-16 自动检查保存结果

用户指出“核对保存结果”不理解，确认新方案后授权修复。改动链路：后台独立检查 → 原安装/会话/项目身份校验 → 官方只读回执查询 → 真实检查状态 → 页面只在未确认时提供重试。新记录保存、官方整理及用户暂停策略保持原流程，不新增模型调用或内容副本。

- Backlog新增独立check_monitor及最多2个查询任务；关闭窗口仍执行。后台轮询按绑定轮转，同一未知集合检查后300秒再试，明确重试只重查、不启动保存。状态仅保留内存、重启重新检查；不持久化第二份队列。
- 复用原插件读取与指纹规则，但严格区分“内容相似”和“写入回执”。Python旧记录没有唯一写入ID，仅返回最近内容，匹配和未匹配均不删除未知记录、更不重发。OpenCode复用原managed-pending check，只有原session/dataset精确marker可确认并移出本地待确认队列。
- 新的管理接口POST /v1/projects/{id}/backlog/check只接受agent_id/host并校验原绑定，Agent凭据不可调用。与save/process分离，检查失败不设置发送停止文件，不阻塞新尾部保存。
- Native显示“自动保存已开启”，数量改为“保存状态待确认”。无后台回执显示“等待检查”，真实查询时显示“检查中”；无法确认后才出现“重试检查”。原因和上次检查时间收进“检查详情”，服务失联不会留下永久检查中状态。

连续回归：无需打开窗口即可自动检查；旧20条未知仍隔离且新安全条目可保存；同一检查单飞/有等待间隔；手动重试不发内容；错误session/dataset、读取不可用不能认定未保存；OpenCode真实固定Node worker精确确认marker且零POST；Native状态、重试动作、操作列对齐和详情时间。

本轮沿用旧“未知20条”及原记录保护，不把新版操作或配置当作重置历史失败次数。首次Go全量两次分别被并行来源阅读实现/缺少strings导入阻断，属于共享源码准备问题；已交接其所有者，不修改或跳过其测试。旧文案断言统一同步到新方案；不是未知写入丢失或重投。

检查入口：

```sh
python3 scripts/task.py -- env PYTHONPATH=engine/python .local/cognee-python/bin/python -m unittest test_project_backlog test_project_backlog_worker test_opencode_backlog
sh scripts/go.sh test ./internal/simpledesktop -run 'Test(PendingAutoCheck|ProjectQueue|BacklogUI|AutomaticBacklog|PausedAutomatic|PendingCheck)' -count=1
```

OpenCode固定artifact可通过MEMLANE_OPENCODE_BACKLOG_TEST_ARTIFACT指定已有backlog-operator-v2，本轮实际运行8项通过，无下载/模型。普通Python组24项通过、3项因未指定artifact跳过，与该8项有重叠，不相加计数。新UI定向通过并打开夹具截图；Go全量通过2.983秒、vet通过。实际Native已见0待保存/20状态待确认及“自动保存已开启”，等待服务所有者统一安全加载后台候选后继续确认自动状态，未点击保存/重试/权限。

收口补充：Backend owner81462已正常加载新check_monitor；只读GET确认automatic_enabled=true，20条旧记录已自动完成检查，status=unconfirmed、reason=original_receipt_unavailable、matched=0，未点击重试或保存。最后17项后台定向0.222秒通过；Go新增异步检查场景race1.959秒通过。race首轮夹具因并行新增设置区导致目标在窗口外，补实际滚动后通过，属于验收方法修正。

前台热重建期间CUA报告cgWindowNotFound，本线程三次读取及来源线程独立读取均不可用；已停止重复尝试并由服务所有者通过原dev入口恢复GUI，不另启未配置窗口、不重启后台。重启后的实际“暂无法确认/检查详情”窗口证据尚待补齐，不能用API或夹具冒充。只读运行回执与候选摘要见[evidence/pending-auto-check-20261116/result.json](evidence/pending-auto-check-20261116/result.json)。

2026-11-15。用户截图指出项目设置中的“待保存与核对”看不懂，授权优化UI。

## 范围和实现

沿用现有队列读取、权限检查、单任务启停及未知结果核对；不操作真实保存、重放或修改项目权限。

- 外层改为“待处理内容”，会话卡改为“会话保存”。顶部用当前会话快照分别显示待保存数（Pending减Unknown）、需确认数（Unknown）及未读取的会话数。未加载不展示伪造的零值。
- 移除“按真实会话汇总”“固定范围”等流程说明，状态区直接显示暂停／后台保存／已完成及当前可执行操作。
- 暂停优先于automatic标记。写入权限未确认时只展示“重新检查权限”，调用既有管理侧读取；不会由此启动保存。其他恢复／停止继续复用原命令。
- 后台任务统计默认收起，展开后明确“任务内剩余”不代表当前队列；提交整理不表示整理完成。
- 会话列表将“0条待发送＋20条结果待核对”改为“20条尚未确认是否保存成功”；读取失败提供“重新读取”，技术错误留在悬停信息。待整理文档列表不改数据或动作。

## 发现及证据边界

旧UI将后台任务Remaining展示为“当前剩余”，与当前会话读取快照并非相同口径；截图105条与会话当前0条待保存、20条未知无法直接对照。已修正呈现，未推断后台Remaining变旧的具体原因，也未将未知条目视为已保存或失败。

本轮定向Go检查首轮通过：TestProjectQueue、TestBacklog、TestAutomaticBacklog、TestPausedAutomatic、TestSidebarPendingBadge匹配组。覆盖两尺寸列表／动作对齐、未知状态、运行／完成、只读轮询不重启任务、详情折叠及暂停automatic优先级。没有新增验收失败轮次。

真实Native已打开项目设置并滚动到该区，核对当前“0条待保存／20条需确认／1个会话暂未读取”、暂停状态、“重新检查权限”、折叠详情及列表的实际布局；未点击权限检查、保存或核对按钮。服务未重启，未调用模型或构建完整包。用户视觉认可待反馈。
