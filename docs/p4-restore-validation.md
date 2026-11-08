# F1c 离线恢复验证

2026-11-08。F1c1 主恢复及 F1c2 恢复后的授权本机通过：真实 App 校验/预览/取消/确认，停止拥有者、完整恢复、App 重载，明确启动后 Rust 当前 v2；旧 Agent/计划/建议/初始化/代码失效、新ID真实traex验证与显式代码重开，以及完成恢复ID的全表无改动重放。其余F1故障与正式分发仍未通过，总交付目标未完成。正式规则见 [备份恢复契约](p4-backup-recovery-contract.md)。

## 真实主流程

完整新身份包 `build/p4-restore-verified/darwin-arm64/MemlaneRestoreVerified.app`，DMG版本0.0.6，helper、LaunchAgent、Agent桥与UI同包。独立 Rust worker0.0.3/protocol1、e5模型与Enola组件从本轮签名loopback清单实际准备；没有调用模型、修改日常Agent配置或使用真实记忆。

`build/p4-delivery/restore-actual-app-v3.json` 和 `.log` 为passed，`actual_app=true`、`actual_rust=true`、cleanup_errors为空。实际App登记合成项目、保存v1、编辑v2并备份、后来编辑v3；取消恢复保留v3且无恢复目录/意图。再次确认恢复v2，恢复前副本保留v3，源备份SHA不变，原文/来源/版本/活动等表与所选快照一致；schema14有效当前原文排队且检索绑定清零。App PID实际变化，重载后持久stopped、无IPC socket；点击启动后真实Rust返回当前v2，旧v3不作为该知识当前结果。结束时service stopped，App与worker关闭。

真实窗口截图已实际打开，对照首页和三栏设计图：连续浅蓝侧栏、三栏设置/详情、单色操作与栏内恢复确认保持。`build/p4-restore-verified-screenshots/restore-confirm-system.png`初始确认截图底部裁剪，不单独作为完整确认视觉证据；随后 `build/p4-delivery/restore-full-preview.json` passed，仅在已停服App再次预览、滚动至确认按钮并取消，没有再次恢复。完整预览为 `build/p4-restore-visual-screenshots/restore-complete-preview-system.png`；停服恢复记录与当前v2详情分别见 `restore-stopped-reloaded-system.png`、`restore-current-v2-system.png`。没有用原型或Headless截图代替系统窗口。

首轮 `restore-actual-app.json` failed：driver以普通ro读取保留WAL模式头的序列化快照，SQLite返回unable to open database file。第二轮 `restore-actual-app-v2.json` failed：已产生真实恢复回执并重载，driver读取恢复前归档时同类ro失败；actual_rust仍false。这两次失败记录保留，未改成passed。修后源快照以immutable读取；当前真实停服已checkpoint的归档明确断言无WAL，再immutable读取，不创建归档侧文件。包含WAL的恢复前原件另由下面明确IO夹具核对。

## 临时SQLite与故障夹具

`restore-source-and-open-tests.log`：真实schema13非空WAL迁移到14前一致性备份，各旧业务表保留；隔离内存转换原备份字节不变，具体版本/来源/已提交结果保留；连接永久退休，重新连接返回新ID，旧代次无法授权；计划停用、lease/自动恢复/未提交初始化/待审核全局建议失效，代码代次增加并停用，旧代码发布冲突拒绝。完整Store→FS→Store恢复实际v2，模拟开库前journal位置恢复，返回明确恢复结果而不先打开SQLite。

`restore-after-review-v2.log`：文件位置中断夹具覆盖归档发布、意图持久化、主库rename后、旧WAL/SHM删除后、回执持久化及stopped持久化后。同ID恢复不重复转换或替换之后的新数据；原库/WAL/SHM逐字节保留。实际两层flock拒绝仍活owner；停服未确认、当前未知SHA、目录父链同步失败重试、意图前取消分别拒绝或保持当前文件。取消在归档后触发，但意图尚未发布；后续显式重试可以继续原ID。

这些是明确SQLite/IO/状态夹具，没有杀死真实App来证明恢复崩溃，也没有模拟磁盘满替代真实磁盘满。目录同步错误为注入的错误；正常路径调用真实fsync。stopped持久边界夹具从清除stop的状态开始，验证stop代次与意图顺序；停服状态实际写入失败还有后续专项需求。daemon在该定向日志中为no tests to run，不能据此宣称daemon专项通过。

F1c2推进中的 `restore-auth-and-stop-write-tests.log` 已通过真实daemon Agent分派：退休ID的旧/新代次对scopes/get/save/initialize均返回restore_reconnect_required；新ID可读恢复当前版本，未获批项目scope_denied，原文没有迟到写入。追加的明确persist-stop错误注入保留pending和全部恢复前副本，解除后同ID继续，不重复准备。它是受信分派/SQLite/FS夹具，没有启动真实日常Agent、调用模型或注入实际磁盘满；不能替代旧授权App综合验收。

`restore-ui-contract-tests.log`：Headless UI驱动真实SQLite/FS的校验/预览/取消/确认与完成回执；不代表native App重载。`restore-driver-owner-fault.json`为AST和合成PID的旧socket close失败夹具，证明发现新PID后先登记清理目标；未运行App或发信号。

`restore-full-test.log`、`restore-full-vet.log`、`restore-full-race.log`记录全仓test/vet及store/recoveryfiles/daemon/desktop受影响race退出0。审查目录为 `build/p4-delivery/review-restore/`；三个组修后三P1和两P2历史保留，active0。跨组校验单独收尾；没有在审查中执行App、模型或测试。

## 包与剩余边界

`restore-package-sizes.json`：DMG17,602,443字节；App普通文件39,318,450字节；首次独立组件归档301,171,513字节；本轮App+实际准备根普通文件逻辑597,857,980字节，OS分配609,419,264字节。包含合成知识、一个备份与一个恢复前副本，不是用户长期库体积。App新增离线SQLite校验/内存迁移能力，实时数据库仍由唯一服务拥有；Cognee与模型不进入主包。主App正式更新下载量未知。

本机为macOS26.6.2 arm64、Go1.27.3/MyGo0.7.4。只支持本数据根已验证schema13/14手动备份，64MiB内快照及每个恢复前文件；不是任意外部数据库导入或离线大库恢复。原文Rust路径通过；完整恢复之后的真实代码重新开启/扫描与Agent新ID接入由下方F1c2独立补验。

当前包是ad-hoc签名。Developer ID、公证、Gatekeeper正式信任、正式发布/更新源、干净macOS15+arm64设备、helper同路径替换、后台许可撤销、真实磁盘满及其他尚未通过F1故障仍需独立证据。R/pi正常模型、A1b Codex版本/模型、A2b TRAE登录及F2/F3外部条件不由本切片解决。恢复通过不代表正式分发或总目标完成。

F1c1主恢复提交 `45d43f91f02f6a36c2f2304cd06c303132a0f90e` 已普通推送main并核对远端一致，记录 `build/p4-delivery/restore-primary-delivery.json`。F1c2在该基线继续，未把后续未完成项删掉或移出总目标。

## F1c2 真实恢复授权与最终包

`restore-authorizations-app-v2.json`记录实际App连接traex、真实读写Verify、准备未提交初始化、建立真实Enola/Rust代码图、显式启用24小时间隔计划、实际traex产生待审合并建议后创建第二备份并恢复。连接/计划/代码各1、当前原文3条进入重建；旧连接退休、新ID安装且旧owned区块撤销。该driver在新ID读取探针中用了错误JSON字段memory而failed，不能将其报告改passed。修为正式memory_id后，沿用其已停服、已恢复合成根继续。

`restore-authorizations-presentation-v3.json`和`.log`为独立passed：App重开后原ID旧/新代次均restore_reconnect_required；计划generation3停用/next_run0、未提交初始化expired、真实旧建议expired且无确认操作；旧图code_disabled。新连接ID执行真实traecli0.214.1/internal保存/状态/检索Verify成功，原项目配置保留合成用户选项、旧ID区块和安装项已撤销。当前v2真实Rust返回；用户明确重新开启代码，generation3 ready并返回实际TypeScript/Python符号。模型沿用traex当前选择，安装/merge回执没有保存模型名字，因此该字段明确未知，不将null当成模型或版本证据。恢复后新ID实际CLI验证与受信IPC探针分开记录。

`restore-real-root-replay.json`为实际已停服App数据根的离线checker通过：已完成恢复23c8e458…再次提交，46张当前业务表逐项不变，保留新ID、重新开启代码和之后的新活动。它是FS/SQLite检查器，`actual_app=false/actual_model=false`；不冒称App点击了同ID操作。检查器先只读核对source-info合成标记、runtime-config绑定、实际临时根且逐层无链接、规范ID、无pending、所选已完成回执/source SHA及当前schema14，再允许Store.Open/重放。新ID、pending、链接/错误标记/绑定的拒绝回归见`restore-replay-gate-tests.log`。

呈现脚本两轮失败也保留：计划启用按钮实际为“确认启用计划”；初始化要从设置“初始化记录”进入。原第一轮呈现的详细JSON被续跑覆盖，stdout失败报告在`restore-authorizations-presentation-plan-failure.json`，其中明确此证据限制；后续StopIteration富报告与日志独立保留。第三轮通过后新增--out使用独立路径。最终包第一轮等待重启后不存在的瞬时Verify提示而failed，保留`restore-final-package-app-failed-v1.json`；修后依据持久安装回执和“可用”说明。

实际截图发现整库恢复错误归因于材料变化，UI改为“备份已恢复，批次过期”与中文重新准备说明；合并过期标题/正文仅说明建议失效与重新生成，不假称具体源版本改变。`restore-final-package-app-v3.json`/`.log`验证包含最新UI/helper/LaunchAgent/桥的完整0.0.7包，App/Rust/Enola通过、明确断言最新过期标题与正文、cleanup为空。该最终窗口核对未再次调用模型；新ID模型Verify由上面的独立真实报告证明。系统图位于`build/p4-recovery-final-screenshots/`，已实际打开对照。新包用新身份复用已停服合成根，原source-info/runtime-config另存`.before-f1c2-ui`；不是同身份helper更新或正式分发证明。

F1c2审查目录`review-restore-auth/`保留checker两P1和driver路径/证据标签的修复过程，适用全仓test/vet与受影响race分别见`restore-auth-full-*`、`restore-auth-final-*`和UI回归日志。停服写失败是明确私有函数故障注入，实际磁盘满/系统fsync失败/真实断电仍需独立F1验收。

最终0.0.7严格深度codesign/DMG校验通过，证据`restore-auth-package-integrity-final.json`，仍是ad-hoc。`restore-auth-package-sizes-final.json`记录DMG17,601,980字节、App39,318,445字节、首次组件归档301,171,513字节，App+当前实际根逻辑600,807,968字节、OS分配612,585,472字节；包含两快照/两恢复前档、实际新连接/代码图/使用记录，首次下载沿用该根真实冷准备结果，本次新包准备复用现有组件。正式主App更新下载量未知。
