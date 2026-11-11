# 索引重建取消与有限重试 · 2026-11-11

本批补N维护主线的取消／仅重试未完成项。真实Native操作和完整包整体验收仍由主Agent集中执行。

## 实现与持久化

schema25增加`index_attempts`（每条知识版本的执行令牌、重建所有者）及`index_rebuild_controls`（任务取消标记、重试来源）；保留原jobs／index_rebuilds表结构和知识版本，迁移前仍生成原有数据库备份。错误字段仅用于真实错误，不充当执行令牌。

普通索引由memory.Service调用`ClaimIndex`，内部令牌随context传递。成功、失败回执均在业务写入事务中核对同一令牌；取消或明确重试后，迟到成功不能重新绑定检索结果，迟到失败不能覆盖新尝试。daemon重新取得数据库所有权时清空旧令牌，原索引队列可继续；取消的重建独占job保持取消，不能自动重放。整库恢复清空令牌和所有者，并按原契约将全部有效当前版本重新排队，不恢复旧任务授权。

`Library.CancelIndexRebuild(ctx,id)`只撤销本任务未完成项：已完成版本保留；任务新发起的独占job失效；原先由普通保存／单项索引排队的job继续；另一活跃重建仍需要的共享job转交该任务所有者。取消历史不因后续正常索引成功改写为完成。引擎已发出的物理请求可能返回，但其被撤销令牌不能发表业务绑定或任务完成回执。

重试使用`RebuildIndex({OperationID: 新UUID, RetryOf: 旧ID})`，其余范围字段留空；只冻结原任务未完成版本，不增加新知识／历史，不重跑已完成版本，不扩展到新加入的知识。已失效版本保留失效；项目任务恢复继续受非退役、非整库恢复的原保存回执约束。相同新OperationID重放原重试，不重新排队已完成内容。

## 验证与边界

定向真实SQLite测试覆盖取消后迟到成功／失败、明确重试后的旧令牌、已完成项保留、版本与活动不变、普通在途保存、另一重建共享、单项retry_index解除取消、重启后旧令牌失效、schema25整库恢复清理。旧V12/V19迁移、项目保存索引恢复、计划和备份相关检查共同回归；失败的早期迁移尝试保留在执行输出，修订为新增专用表后通过。

API和持久化测试不表示N全部设计状态、Native焦点／滚动／窄窗、真实Cognee重建或整体十条链路已经验收。该批未构建独立完整包、未操作用户记忆库。

最终定向命令：`sh scripts/go.sh test ./internal/store ./internal/memory ./internal/daemon -run 'TestIndexRebuild|TestProjectIndex|TestBackup|TestSchedule|TestRestore' -count=1`，store 5.486s、memory 1.052s、daemon 2.042s均输出ok；受管会话28378继续完成清理。`git diff --check`针对本批生产／测试改动通过。全量测试与完整包不在子任务重复执行。

受管会话28378最终exit0，清理后可用153556340736 bytes（约143.0 GiB），未触发100 GiB底线。
