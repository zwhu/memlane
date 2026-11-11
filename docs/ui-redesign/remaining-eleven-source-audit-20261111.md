# 剩余11项源码证据审阅

2026-11-11，H03/H04、G06/G08 经主任务集中合入后。仅审阅此前仍为“未检查”的 F03/F04/F07、N03/N04/N06/N09/N10/N12/N13/N14；不重审125项，不修改生产代码，不运行测试、窗口、模型或构建。本批7项标“已实现待验证”、4项标“待实现”，没有增加“已验证”。逐项字段已更新到 [工程覆盖账本](scenario-engineering-coverage.md)。

证据分层：列出的测试是已存在源码/历史日志线索，本轮未重跑；P0/P4旧包与旧schema的真实功能证据不等于最新005/后续集中包整页通过。003/004维护切片也不等于所有维护场景完成。

| 场景 | 当前源码证据 | 确切边界/缺口 | 状态 |
| --- | --- | --- | --- |
| F03 | `reading_filters.go` 折叠筛选、实际raw source目录、真实scope.kind类别与当前Version更新时间；`library.go` 冻结query/epoch、各项目/页面返回；Store.List独立source目录、scope总数先于过滤、全参数cursor | 非空语义搜索只筛最多30条snapshot并明示；示例“工程约定”不是持久分类。最新Native组合/清空/零匹配/800矩阵待验 | 已实现待验证 |
| F04 | `usageSortExplanation` 三种含义；Store.List按当前版更新时间、近30天daily成功read次数、用户important，再以时间+ID稳定排序；游标包含排序 | Coverage为单库共享起点，显示未知不补零；SQL没有逐记录coverage优先项，不能声称已验“已知零/未知覆盖混排”。已有`TestUsageSortPagingScopeAndImportant`和覆盖展示回归，当前排序切换/焦点/选择Native待验 | 已实现待验证 |
| F07 | `memoryRow`稳定ID Key/单行标题摘要/来源收敛、选中和Hovered背景；列表scope+kind独立ScrollState、真实cursor加载更多；长文UTF8有界读窗 | 不是虚拟列表，不宣称任意规模性能；完整多页长标题/摘要、Tab焦点、悬停和返回滚动最新Native待验 | 已实现待验证 |
| N03 | `daemon/server.go:supervise`持久记录worker失败，10分钟3次上限、指数退避、failed等待wake；P0有真实worker崩溃/熔断证据；health观察与诊断入口在 | 维护UI将很多引擎非ready归为暂不可用；缺恢复阶段/达到上限的明确分态、次数与直达手动重试。launchd对daemon异常退出是10秒节流，不应描述成同一个3次上限 | 待实现 |
| N04 | SMAppService `RequiresApproval` → `ObserveAvailability` authorization，说明登录项与扩展；页面显示许可需要恢复与读写不可用；只读轮询可观察恢复 | 未见固定稿可执行“查看系统设置入口”及“恢复许可后重新验证”专用动作。文字指引/环境重查不能替代，真实撤权窗口也待验 | 待实现 |
| N06 | `maintenanceUpdatesPage`独立header/body/footer，实际Check/下载/安装/取消回执；main复用MyGo Updater、停服/quiesce、草稿屏障、取消恢复原运行证明和成功relaunch | P0受控HTTPS错误签名/短传/真实替换是旧限定证据；当前包完整更新和Native回执待验，正式更新源/签名公证/分发另验 | 已实现待验证 |
| N09 | Verify→预览/取消→确认；固定footer；控制器先停service/worker再取两锁，RestoreManager恢复前副本/持久ID；失败原ID再确认，重开`recovery_required`续接 | `p4-restore-validation.md`真实schema14恢复有证据；当前schema新处理/草稿/图任务、故障续接与普通/窄窗口需集中复验 | 已实现待验证 |
| N10 | `store/restore.go`撤连接、停计划/处理启用、退休旧任务/预览/图身份、原文重新排队；App递增epoch、清旧草稿/watch并relaunch，stopped持久；备份页从FS读最近恢复统计 | 缺固定稿“恢复后的知识库”汇总承载与明确启动/重建索引/重新验证Agent就地行动组。目前散在备份统计和通用服务页，不以旧恢复后功能通过代替该页面还原 | 待实现 |
| N12 | 保存/审核事务错误回滚、备份同operation续接、草稿保留及退出屏障；P4受限APFS真实ENOSPC与释放后恢复限定通过，L04也有真实SQLite写入故障 | 当前手动保存错误统一建议“恢复服务后保存”，不能准确表达磁盘不足/存储不可写；缺释放空间后的正确分态与行动。新版整理/备份/多writer全矩阵待验，不需再次填满宿主磁盘 | 待实现 |
| N13 | `maintenanceDiagnosticsPage`概要状态与可处理说明、按需Collapsible技术摘要、复制白名单状态/回执，返回服务footer；`TestDiagnosticSummaryExcludesConfigurationAndRecordPayloads`检查排敏 | 最新普通/窄Native展开/复制/长错误/焦点待验；只读源码不宣称所有错误源已实测 | 已实现待验证 |
| N14 | 真实Tray、OnClose拦截→Hide，OnHide/OnShow可见状态，Show/Focus原窗口，关闭不重初始化/取消daemon；退出另走保留服务设置与Flush屏障 | 集中包实际关闭→后台继续→同页/任务/焦点滚动重开待验；Tray创建失败会明确退回关闭即退出，不能在该环境仍声称已收起 | 已实现待验证 |

## 证据索引

- F：[最新筛选专项](reading-filters-20261111.md)、`reading_filters_test.go`、`store/reading_filters_integration_test.go`、`long_text_test.go`、`store/usage_integration_test.go`、[旧U限定验收](../p3-usage-validation.md)、[旧阅读重设计](reading-fidelity-20261110.md)。
- N03/N04：[P0后台/worker/更新验证](../p0-validation.md)、`runtime_availability_test.go`、`maintenance_settings_test.go`。当前只读观察不会注册helper、清主动停止或重试任务。
- N06/N13/N14：[维护工程记录](maintenance-native-20261111.md)、`maintenance_updates_test.go`、`runtime_update_cancellation_test.go`、`maintenance_diagnostics_test.go`、`cmd/memlane-app/main.go`。
- N09/N10：[真实旧整库恢复与授权](../p4-restore-validation.md)、`backup_restore_integration_test.go`、`internal/recoveryfiles`恢复测试、`store/restore.go`及新身份退休相关回归。
- N12：[真实旧APFS磁盘故障](../p4-diskfull-validation.md)、[提炼恢复](experience-recovery-fidelity-20261110.md)、[手动草稿屏障](manual-drafts-persistence-20261111.md)。开发100GiB安全余量不是产品磁盘故障UI的验收证据。

## G06合入后的L05措辞纠正

主任务明确提醒的新G06已合入：`sourceReaderFailureMessage` 已有来源权限/撤权专用提示，并禁止在无效授权上原地重试。旧19项审计中“没有专用状态、均通用提示”已过期，现已修正相应段落和L05缺口措辞；L05状态不提高，完整固定稿专用无权限页的几何、返回/版本/撤权/重开整体仍需单独对照。此更正不构成第12项重新验收。
