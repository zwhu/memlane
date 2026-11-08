# F1a 检索索引重建验证

2026-11-08，F1a 本机对应范围通过。正式规则见 [重建契约](p4-index-recovery-contract.md)。新 schema13 只新增实际维护任务；worker 0.0.3 / protocol1、Enola 和原文 generation 沿用 G2。F1 helper 同身份替换、离线数据库备份恢复及正式分发仍单独未通过。

## 真实 App 与 Rust

`build/p4-delivery/index-recovery-final-app.json` / `.log` 使用完整 `build/p4-index-recovery-final/darwin-arm64/MemlaneIndexFinal.app`。App 实际登记项目、新增三条合成知识、编辑其中一条至 v2，真实 Rust 检索先证明当前 v2。通过 App 明确停服，等待服务与 worker 退出，再把此隔离项目的 raw index 目录移到合成根外侧，作为真实目录丢失故障；一条知识的 archived 状态是明确停服 SQL 夹具，不能作为 App 归档操作通过。原文和 v1/v2 历史保留。该运行报告由旧的 fault_fixture 标签生成，结果未改写；实际 SQL 步骤在脚本和本文明示。

重开 App 并明确启动后，当前正文和历史可读，丢失索引的真实检索为空；组件正常 ready。App 在栏内开始确认但取消，服务没有任务；再次实际确认后稳定任务冻结两条有效知识，排除归档条目。离页返回仍显示该任务；真实 Rust 完成 v1/v2 的当前索引，检索包含当前 v2、排除该条旧 v1 和归档条目。UUID 重放返回同任务，不新增索引工作；历史、版本表和活动表逐项保持。再次停服与重开后，完成任务和真实当前检索持久，代码开启记录一直为空，没有隐式扫描。全程未调用模型。

`index-recovery-scope-app.json` / `.log` 是实际 App 范围选择与上限补验：10001 条 raw 行及完成 jobs 明确为 SQL 上限夹具，不宣称其有真实索引。默认全部范围超限，服务拒绝且没有新任务，App 显示需选择较小范围；实际 popup 选择原项目后，仅两条目标入队、范围 UUID 一致，真实 Rust 完成。原已完成任务保持。停服后断言预算行没有任务引用，仅清理该随机 dataset 的夹具行；cleanup_errors为空。正常主流程报告不以这些 SQL 行证明知识或索引数量。

实际系统截图 `build/p4-index-recovery-final-screenshots/index-rebuild-completed-system.png`、`index-rebuild-budget-system.png` 和 `index-rebuild-scope-system.png` 已打开对照首页/三栏设计图：连续浅蓝侧栏、栏内确认、真实任务状态/统计与明确上限原因，无模态审核或虚构记录。

## 迁移、版本与故障

`index-rebuild-store.log`：临时真实 schema12 SQLite 含非空 WAL、非空原文/版本/索引绑定/活动与 disabled 代码记录。唯一 owner 迁移前 VACUUM INTO 备份为12、integrity_check=ok，全部旧表在备份和新13库逐项一致，迁移没有自动重建或扫描。范围、active/stale/archived 排除、事务原子回滚、同UUID重放、不同范围冲突、重建期间编辑后的旧回执拒绝均通过。

最新 App 主流程还将新13合成库副本交给真实 G2 schema12 helper，明确退出1，错误精确为 `database schema is newer than this client`。不是正式更新回退通过，也没有从这次拒绝推断备份恢复已完成。

`index-rebuild-publication-fault.log` 是 NO RUST/MODEL 的明确 SQL 夹具：只拒绝维护项 completed 更新。此前 Engine 返回成功但 MarkIndexed 回滚后，job/task 会停留 indexing/queued。现非版本冲突的发布错误独立持久化 IndexFailed，失败对任务和原文状态可见；解除故障后可完成，曾失败错误保留，原文/revision不变。版本冲突按过期处理，不标作当前版本失败。

首轮 `index-recovery-actual-app.json` / `.log` 保留 failed：Select 构造后设置 Key，MyGo 实际 panic 关闭窗口。完整堆栈在 `build/p4-index-recovery-screenshots/window.log`；已改为 Context.Key 构造前绑定，新身份完整重跑后通过。源码测试通过不能代替这个实际 UI 故障与修后复跑。

## 检查、包与体积

最终 `index-rebuild-final-test.log`、`index-rebuild-final-vet.log`、`index-rebuild-final-race.log` 为全仓 test/vet，以及 Store/daemon/desktop/memory race，通过。Bits 分组 review 位于 `build/p4-delivery/review-index/`，发布失败 P1、控件 Key P1 和错误文案 P2 保留 resolved 历史；active为空。App 超限消费 ServiceError，网络提交结果未知仍保留稳定 UUID，实际 SQL 上限 UI 已通过。

完整 UI/helper/LaunchAgent/MCP-CLI 桥、pi extension 内嵌及 DMG 在 `build/p4-index-recovery-final/`，严格深度 codesign 和 hdiutil verify 通过，仍是本机 ad-hoc 包。`index-recovery-package-sizes.json`：

| 指标 | bytes | 范围 |
| --- | ---: | --- |
| 主 DMG | 15,627,999 | 客户端/helper/桥，不含引擎模型 |
| App 普通文件 | 35,163,379 | 安装 App 本身 |
| 首次额外准备 | 301,171,513 | Rust0.0.3/E5/Enola 受控 feed |
| App + 实际准备根逻辑占用 | 598,644,947 | 包含 SQL 上限夹具清理后的 SQLite 空闲页，不含移走的 raw 故障副本/源 worktree/Agent/工具链 |
| OS 分配文件块 | 628,596,736 | 不含目录元数据，APFS共享块不等于可释放空间 |
| 主 App 更新下载 | 未知 | helper替换及正式源尚未通过 |

用户 branding/design/roadmap/builder 输入保留且不纳入本切片提交，包外观含本机这些输入。不能宣称此包是由仅本阶段提交树生成的正式分发物。

## 剩余边界

本切片只从有效原文重建检索，不恢复损坏数据库，不删除运行时目录，不启动收费模型任务，不更改代码图谱授权。离线一致性备份恢复、后台许可撤销、磁盘满及剩余系统故障继续 F1；[helper替换](p4-helper-replacement-validation.md) 仍失败。R 正常模型/App 服务链路、A1b/A2b、Developer ID/公证/正式源/Gatekeeper及干净 macOS15+arm64仍缺必需证据，总 Goal 未完成。
