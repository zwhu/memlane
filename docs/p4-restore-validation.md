# F1c 离线恢复验证

2026-11-08。F1c1 主恢复切片本机通过：真实 App 校验/预览/取消/确认，停止拥有者、完整文件恢复、App 重载，明确启动后 Rust 检索当前 v2。F1c2 的旧 Agent/计划/建议/初始化/代码授权真实 App 综合验收及恢复后的再次操作重放继续验证；目前只有其临时 SQLite/FS 回归证据。整个 F1c 与总交付目标仍未完成。正式规则见 [备份恢复契约](p4-backup-recovery-contract.md)。

## 真实主流程

完整新身份包 `build/p4-restore-verified/darwin-arm64/MemlaneRestoreVerified.app`，DMG版本0.0.6，helper、LaunchAgent、Agent桥与UI同包。独立 Rust worker0.0.3/protocol1、e5模型与Enola组件从本轮签名loopback清单实际准备；没有调用模型、修改日常Agent配置或使用真实记忆。

`build/p4-delivery/restore-actual-app-v3.json` 和 `.log` 为passed，`actual_app=true`、`actual_rust=true`、cleanup_errors为空。实际App登记合成项目、保存v1、编辑v2并备份、后来编辑v3；取消恢复保留v3且无恢复目录/意图。再次确认恢复v2，恢复前副本保留v3，源备份SHA不变，原文/来源/版本/活动等表与所选快照一致；schema14有效当前原文排队且检索绑定清零。App PID实际变化，重载后持久stopped、无IPC socket；点击启动后真实Rust返回当前v2，旧v3不作为该知识当前结果。结束时service stopped，App与worker关闭。

真实窗口截图已实际打开，对照首页和三栏设计图：连续浅蓝侧栏、三栏设置/详情、单色操作与栏内恢复确认保持。`build/p4-restore-verified-screenshots/restore-confirm-system.png`初始确认截图底部裁剪，不单独作为完整确认视觉证据；随后 `build/p4-delivery/restore-full-preview.json` passed，仅在已停服App再次预览、滚动至确认按钮并取消，没有再次恢复。完整预览为 `build/p4-restore-visual-screenshots/restore-complete-preview-system.png`；停服恢复记录与当前v2详情分别见 `restore-stopped-reloaded-system.png`、`restore-current-v2-system.png`。没有用原型或Headless截图代替系统窗口。

首轮 `restore-actual-app.json` failed：driver以普通ro读取保留WAL模式头的序列化快照，SQLite返回unable to open database file。第二轮 `restore-actual-app-v2.json` failed：已产生真实恢复回执并重载，driver读取恢复前归档时同类ro失败；actual_rust仍false。这两次失败记录保留，未改成passed。修后源快照以immutable读取；当前真实停服已checkpoint的归档明确断言无WAL，再immutable读取，不创建归档侧文件。包含WAL的恢复前原件另由下面明确IO夹具核对。

## 临时SQLite与故障夹具

`restore-source-and-open-tests.log`：真实schema13非空WAL迁移到14前一致性备份，各旧业务表保留；隔离内存转换原备份字节不变，具体版本/来源/已提交结果保留；连接永久退休，重新连接返回新ID，旧代次无法授权；计划停用、lease/自动恢复/未提交初始化/待审核全局建议失效，代码代次增加并停用，旧代码发布冲突拒绝。完整Store→FS→Store恢复实际v2，模拟开库前journal位置恢复，返回明确恢复结果而不先打开SQLite。

`restore-after-review-v2.log`：文件位置中断夹具覆盖归档发布、意图持久化、主库rename后、旧WAL/SHM删除后、回执持久化及stopped持久化后。同ID恢复不重复转换或替换之后的新数据；原库/WAL/SHM逐字节保留。实际两层flock拒绝仍活owner；停服未确认、当前未知SHA、目录父链同步失败重试、意图前取消分别拒绝或保持当前文件。取消在归档后触发，但意图尚未发布；后续显式重试可以继续原ID。

这些是明确SQLite/IO/状态夹具，没有杀死真实App来证明恢复崩溃，也没有模拟磁盘满替代真实磁盘满。目录同步错误为注入的错误；正常路径调用真实fsync。stopped持久边界夹具从清除stop的状态开始，验证stop代次与意图顺序；停服状态实际写入失败还有后续专项需求。daemon在该定向日志中为no tests to run，不能据此宣称daemon专项通过。

`restore-ui-contract-tests.log`：Headless UI驱动真实SQLite/FS的校验/预览/取消/确认与完成回执；不代表native App重载。`restore-driver-owner-fault.json`为AST和合成PID的旧socket close失败夹具，证明发现新PID后先登记清理目标；未运行App或发信号。

`restore-full-test.log`、`restore-full-vet.log`、`restore-full-race.log`记录全仓test/vet及store/recoveryfiles/daemon/desktop受影响race退出0。审查目录为 `build/p4-delivery/review-restore/`；三个组修后三P1和两P2历史保留，active0。跨组校验单独收尾；没有在审查中执行App、模型或测试。

## 包与剩余边界

`restore-package-sizes.json`：DMG17,602,443字节；App普通文件39,318,450字节；首次独立组件归档301,171,513字节；本轮App+实际准备根普通文件逻辑597,857,980字节，OS分配609,419,264字节。包含合成知识、一个备份与一个恢复前副本，不是用户长期库体积。App新增离线SQLite校验/内存迁移能力，实时数据库仍由唯一服务拥有；Cognee与模型不进入主包。主App正式更新下载量未知。

本机为macOS26.6.2 arm64、Go1.27.3/MyGo0.7.4。只支持本数据根已验证schema13/14手动备份，64MiB内快照及每个恢复前文件；不是任意外部数据库导入或离线大库恢复。原文Rust路径通过；完整恢复之后的真实代码重新开启/扫描与Agent新ID接入留F1c2。

当前包是ad-hoc签名。Developer ID、公证、Gatekeeper正式信任、正式发布/更新源、干净macOS15+arm64设备、helper同路径替换、后台许可撤销、真实磁盘满及其他尚未通过F1故障仍需独立证据。R/pi正常模型、A1b Codex版本/模型、A2b TRAE登录及F2/F3外部条件不由本切片解决。主恢复通过不代表正式分发或总目标完成。
