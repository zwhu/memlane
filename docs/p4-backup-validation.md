# F1b 手动一致性备份验证

2026-11-08，F1b 对应本机范围通过；F1c 离线恢复仍未实现。[契约](p4-backup-recovery-contract.md) 区分备份/校验、灾难恢复、单条版本恢复及原文索引重建。worker0.0.3/protocol1、schema13与模型/Enola版本沿用前阶段，不升级 Agent、不读取聊天。

## 真实 App 与 SQLite

`build/p4-delivery/backup-actual-app.json` / `.log`：完整 `build/p4-backup-app/darwin-arm64/MemlaneBackup.app` 从新隔离根准备组件。App 登记项目、保存 v1、编辑至 v2；外部只读事务保持 WAL，未修改真实库。App 实际点击创建，SQLite 在线复制当前已提交 WAL，快照全部业务表的行数/SHA 与当前库一致，原库未改变。Root 发布 snapshot.sqlite+receipt.json，目录0700/文件0600，vault UUID、schema13、大小、SHA与时间来自实际结果。

App 实际校验通过；编辑至 v3 后，受信 IPC 同 UUID 重放仍返回 v2 原快照且没有新增备份。再次实际点击创建得到另一个 v3 快照，SHA变化、vault保持。重放是明确 IPC 检查，不能替代两次真实 UI 创建。

一字节损坏与暂缺回执是明确 IO 夹具：App 校验失败，原损坏字节未被偷偷覆盖；恢复夹具后，缺回执只把该项标为无效，健康第二项仍可見并实际校验通过。停止服务退出，再重开实际 App，direct FS 目录和校验可用，IPC不存在、stopped保持；明确启动后真实 Rust 检索仍返回当前 v3。所有 IO 夹具恢复、读事务/窗口/测试源/服务均收口，cleanup_errors为空。

`build/p4-backup-screenshots/backup-created-system.png`、`backup-invalid-beside-valid-system.png` 及 stopped/checksum 帧为真实系统窗口。已实际打开创建和无效/健康并存帧，对照已打开的设计图：连续浅蓝侧栏、三栏、栏内创建/状态/校验，操作先 scrollIntoView 到实际栏内。包外观含用户本机 branding 输入，但未将其改动纳入本切片。

## 实际 IO 取舍与故障

FD VACUUM 输出失败、内存在线复制成功的底座分别保留 `backup-native-fd-probe.json`、`backup-modernc-fd-probe.json`、`backup-memory-probe.json`。小型接口实验不作为完整 App 验收。生产采用固定现代 SQLite 分页复制到内存，64MiB cap、完整性/FK检查、Serialize，随后仅通过固定 Root 的普通文件句柄写出，不使用 SQLite 任意目标路径。

`backup-core-wal-tests.log` 与 `backup-after-review-tests.log` 是明确 NO MODEL/RUST 的实际 FS/SQLite 夹具：非空 WAL、所有旧表、版本/活动保持、同UUID不重新捕获，跨vault、硬链接、内部相对链接、字节篡改、取消与未知目录保留；负向检查后正常备份仍可执行。

目录 fsync 故障用私有 IO hook，在实际 UUID Rename 后触发。第一次错误保留发布内容，同UUID重放仍返回同步错误、source只捕获一次；解除后同步备份目录/父目录/数据根再报告成功。旧 vault 文件自身同步也在创建重试时完成。它不证明断电或真实磁盘满。

`backup-vault-no-rotation.log` 明确验证已有备份但 marker丢失时，source调用为0、marker仍不存在；不能以泛化错误推断没有旋转。损坏单项不阻断健康目录记录，缺整个数据根或 vault身份不伪装为空。

`backup-driver-partial-write.json` 读取实际 driver AST，用合成文件部分写入后抛 IO 错误，验证写入前已登记恢复目标、finally恢复原字节；这是错误注入夹具，非真实磁盘耗尽。正常 App 报告保持原样，未因后补清理改写失败为成功。

## 审查、检查和包

Bits 分组产物 `build/p4-delivery/review-backup/` 保留目录 Loading卡死、ErrNotExist空列表泛化、fsync重放的P1与driver部分写入清理P2修复历史。原无组件 Shell 测试因新增内容后缩放按钮在屏外而失败，调整为在已有 Scroll 容器内滚动，再点击相同按钮，业务缩放与断言不改；回归通过。

`backup-final-test.log`、`backup-final-vet.log`、`backup-final-race.log` 记录全仓test/vet及Store/recoveryfiles/daemon/desktop race通过。完整 App/helper/LaunchAgent/CLI-MCP桥及pi extension内嵌、DMG在 `build/p4-backup-app/`；`backup-package-integrity.json` 为严格深度codesign与hdiutil verify通过，仍为ad-hoc验证包。

`backup-package-sizes.json`：

| 指标 | bytes | 范围 |
| --- | ---: | --- |
| 主 DMG | 15,673,964 | 客户端/helper/桥，不含引擎模型 |
| App 普通文件 | 35,263,511 | 安装 App 本身 |
| 首次额外组件 | 301,171,513 | 固定Rust/E5/Enola受控feed |
| App + 实际准备根逻辑占用 | 593,268,012 | 含两份实际SQLite快照与数据/组件/模型/设置/缓存，不含worktree/Agent/工具链 |
| OS分配文件块 | 607,981,568 | APFS共享块不等于可释放空间，目录元数据不计 |
| 主 App 更新下载 | 未知 | helper替换/正式源尚未通过 |

## 剩余边界

本阶段只交付64MiB内的手动在线备份、真实目录和当前文件校验。超限在线备份拒绝；未将大库离线备份、F1c确认恢复或恢复前隔离副本标为已实现。需要在 F1c 验证新连接ID、停用计划与代码、旧建议失效、stopped及原文索引重建。

F1 helper同身份替换仍EX_CONFIG78，真实磁盘满/许可撤销等故障、R正常模型与App链路、A1b/A2b及Developer ID/公证/正式源/Gatekeeper/干净macOS15+arm64仍未完成。此包/技术/SQLite通过不代表总目标完成。
