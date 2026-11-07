# P3-D2 执行恢复验证

2026-11-08，D2a 持久租约与进程保护、D2b 有限自动恢复均已通过本机验收。真实 App/Agent/Rust、明确故障夹具与正式分发边界分别记录；U/I/A/R/G/F 和总 Goal 继续。契约见 [D2 契约](p3-recovery-contract.md)，依赖与其余必需项见 [交付矩阵](p3-delivery-plan.md)。

## D2a 真实 App 主流程

使用新 `memlane-p3-recovery-app-*` 合成根，独立标识 `io.github.zwhu.memlane.recovery.ccf42d931435`；仅复用已验证组件，不复制业务库、聊天或日常 Agent 配置。MyGo 0.7.4 完整验收包，真实 traex 0.214.1 internal，固定 Rust worker / E5-base int8。

| 项目 | 真实结果 | 证据 |
| --- | --- | --- |
| 正常整理 | App 选两条有效项目知识，立即取得稳定 task；guard 登记后放行实际 traex，完成摘要/标签并保留来源 v1 | `build/p3-recovery/real-app.json` |
| Rust | 自动摘要 v1 被真实 Rust 检索返回 | 同文件 real_rust_memory |
| 活动取消 | App 在真实 turn.started/process_active 后取消；无结果/条目，guard 与 Agent 组已回收消失 | 同文件 cancel_task |
| 持久拥有权 | 停服且唯一拥有者锁下只读核对 schema8；两个 attempt1 租约 PID>1、expires=0、released/reaped/group_gone 全真 | 同文件 leases |
| 实际窗口 | 正常/取消三栏、冻结来源版本与产出检索状态实际查看，对照既定图稿 | `build/p3-recovery-screenshots/lease-*-system.png` |

正常和取消均使用真实 Agent，不使用模型协议夹具。新生产 helper 的内部 `--proposal-guard` 在读取服务配置前分派；无生产数据库和批准工具。模型执行仍保留已有两分钟上限，不自动新增收费重试。

## D2a 故障与迁移证据

`lease-final.log` 使用真实 SQLite、明确过期和结构化结果夹具：缺 owner、错 owner、到期、已取消和旧 attempt 都拒绝 Claim/Stage/新 Apply/失败标记/Finish；到期不能续租复活或接管旧 attempt。有效 lease 下计划代次变化也拒绝续租/失败/Finish，避免旧任务被误写 partial_failed。

部分成功夹具先提交一项回执，再过期；已有回执仍能重放，新项不能写。中断后创建无模型 apply attempt，原结果、项 ID 和回执保持，新摘要只有 v1，标签事件未重复。此项是受信 Store 恢复故障证明，尚不代表 D2b 自动调度恢复已实现。

schema7→8 临时旧库有计划 operation、持久模型结果与已提交项；非空提交 WAL 下的一致性 schema7 备份 integrity_check=ok，全部旧表逐项相同，迁移后部分结果和回执保留。只增加实际 organization_leases 表；旧计划不因此启用重试。

`guard-crash.json` 是实际 helper 的无模型进程故障夹具：未收到持久登记许可时 `/usr/bin/touch` 不执行；正常 guard 子命令退出0且 FD3 不继承；控制写端的 owner 被实际 SIGKILL 后，活跃合成 sleep 进程组因 EOF 消失。它不调用模型、不打开业务库，不替代 App 正常/取消证据。首轮 fixture 将 macOS 临时 EPERM 当异常已修正为有界等待，失败记录不计通过。

`TestOrganizationLeaseStartupBlocksLiveGroupAndPersistsReleasedCleanup` 使用实际独立 sleep 组：旧组存活时启动明确阻塞且不发信号；组消失后 released 行也持久 group_gone，避免后来反复检查已复用的 PID。服务重启不盲目 kill 历史 PID；D2a 只中断旧任务，不自动模型重跑。

现有真实服务验收在 daemon.test 内启动服务。该可执行文件也增加内部 guard TestMain 分派，实际测试二进制无模型 FD 夹具通过，见 `test-main-guard.json`；没有绕过 guard。旧 C1 合并入口同样沿用独立 guard，并在放行前持久 PID、事件更新保留 PID、组未回收时停服、启动核对并保存历史组消失事实；租约表仅属于 organization attempt。`legacy-real-service.log` 的真实 traex/Rust 验收再次通过正常/未确认不变/确认检索/拒绝/活动取消/停服/回执重启保留，迟到协议和索引阻挡仍明确为故障夹具；旧 C1 审核/版本规则不变。

最终全仓 `test-final.log`、`vet-final.log` 与 Store/daemon/agents/desktop `race-final.log` 通过；旧合并 guard forwarding 的中间补检查另存 `test-final-affected.log`、`race-final-affected.log`。分组/跨组审查修复控制 FD、计划写栅栏和 released 组事实保存问题，当前确定残留为零；历史修复与报告保留于 `build/p3-recovery/review/`。

## 包与边界

完整 UI/helper/LaunchAgent/Agent 桥及 DMG 在 `build/p3-recovery-production/`；深度严格 codesign 和 SHA256SUMS 通过，当前为 ad-hoc 本机包。Developer ID、公证、正式更新源、Gatekeeper 和干净 macOS 15+ arm64 验收仍未通过。

| 体积口径 | 字节 |
| --- | ---: |
| 主 DMG | 15,160,205 |
| App 普通文件逻辑大小 | 34,000,735 |
| 首次额外 Rust/模型归档 | 285,752,286 |
| App + 本轮实际准备合成根逻辑占用 | 525,962,145 |
| 更新下载量 | 未验证 |

细分见 `sizes.json`，不包含失败重试/TLS开销；APFS 分配不等于删除可释放空间。用户并行图标/design/roadmap 改动影响本机包，仍保留并排除于 D2a 提交。

检查、阶段 SHA/远端匹配和临时 handoff 分别在 `final-checks.json`、`delivery-d2a.json`。handoff 由脚本直接生成，明确未调用禁止自动模型调用的源 Skill，不入库。下一步 D2b 后继续 U/I/A/R/G/F，不因 D2a 或交接结束总 Goal。

## D2b 有限自动恢复：真实 App

2026-11-08，独立 `memlane-p3-recovery-app-m_yo_lmj` 合成根与标识 `io.github.zwhu.memlane.recovery.104424935fbc`。真实 MyGo App / traex 0.214.1 internal / 固定 Rust worker，三个实际未来 UTC 槽位；没有改变系统时钟，没有模型协议替身。正常模型生成和活动取消为真实调用；SQL tag INSERT 拒绝与实际 helper SIGKILL 明确为故障注入。

| 主流程 | 通过结果 | 证据 |
| --- | --- | --- |
| 用户授权 | 新计划重试默认关闭；App 明确选择额外1次、总窗口10分钟，保存停用草稿后单独启用 | `build/p3-recovery-d2b/real-app.json` |
| 部分成功后恢复 | 真实模型生成摘要/标签；SQL故障留下摘要回执，解除故障后同task新apply attempt仅继续未提交项，无模型；原回执不变，摘要由真实Rust返回，来源仍v1 | 同文件 real_model_partial_sql_then_auto_apply |
| 活动取消 | 新来源v2、真实turn活动时App取消；无结果/条目，进程组消失，恢复cancelled，不补跑 | 同文件 real_active_cancel |
| 服务崩溃/重启 | 仅核验此隔离锁拥有者的helper路径后实际SIGKILL；旧guard组先消失，launchd恢复，同task新model attempt完成，唯一结果与真实Rust当前版本返回 | 同文件 actual_service_crash_and_real_model_retry |
| 终态/清理 | App停用计划并停服，持久stop_generation=1；全部recovery terminal，五个lease released/gone；四个model PID>1，一个无模型apply PID=0，cleanup_errors为空 | `stopped-audit-compat.json` |
| 实际视觉 | 已实际打开两张设计图及计划授权、部分失败、apply完成、取消、崩溃恢复五张系统窗口截图；连续浅蓝侧栏、三栏、来源版本、UTC窗口、无模型说明与执行记录一致 | `build/p3-recovery-d2b-screenshots/retry-*-system.png` |

崩溃推进栅栏使执行编号为1/3，历史只有两个model attempt，界面称“执行记录”，不表示三次付费调用。部分结果的单result行已在停服拥有者锁下核对；原回执保持由驱动逐项断言，不以最终标题相同推断。服务/计划停止后的旧恢复不会因显式重启重新启用。

## D2b 故障、迁移与审查

Store真实SQLite集成覆盖默认0/永久失败不重跑、15/60秒退避、额外1–2次预算、1–1440分钟窗口、原任务新attempt、有限去重、来源过期、取消、手动优先和已有结果apply-only。领取槽后与最终guard许可前分别重查来源与计划代次；手动开始/继续暂停尚未持租约的自动queued/applying，已持lease不抢占。已提交事实不重复，窗口耗尽只终止自动恢复；明确手动继续已有结果仍按来源规则应用。

Controller故障夹具覆盖已知永久模型结果遇SQL终态写入失败、内存保留、final shutdown与存储恢复，不将已知永久失败覆写成可收费重跑的unknown。stop epoch在实际持久MarkStopped/Resume上验证，旧epoch被关闭，同epoch同毫秒及created_ms回退1小时仍保留，下一次stop推进代次；这是状态/时钟夹具，不代表真实时间旅行。

v8临时库从实际旧DDL构建，含非空WAL、全局条件/历史、持久lease和部分已提交回执。唯一拥有者一致性备份v8 integrity_check=ok；全部旧表逐项保持，升级schema9仅增实际recovery表，旧计划不会获得重试授权；全局restore产生v3。见 `store-recovery.log`。实际D2a schema8 helper在新隔离schema9副本上拒绝，错误为 `database schema is newer than this client`；不冒称正式回退更新或索引重建通过。

全仓 `test-pre-app.log`、`vet-pre-app.log`、Store/daemon/agents/desktop `race-pre-app.log` 通过；末次stop rollback补断言 `stop-rollback-final.log` 通过，未改变生产代码。分组与跨组审查27文件当前0项，5个已解决根因及脚本清理修复保留于 `review/resolved_comments.jsonl`；HTML/Markdown与覆盖在同目录。原审查时“实际App待执行”是当时状态，最终以 `real-app.json` 为准。

## D2b 本机交付

完整 UI/helper/LaunchAgent/Agent 桥与DMG在 `build/p3-recovery-d2b-production/`，严格深度codesign通过；仍为ad-hoc本机验证包。用户图标/design/roadmap/builder变动保留并排除于阶段提交；它们影响本机包外观，不以本包冒称正式发行。

| 口径 | 字节 |
| --- | ---: |
| 主DMG | 15,165,389 |
| App普通文件逻辑大小 | 34,067,711 |
| 首次额外Rust/E5归档 | 285,752,286 |
| App + 实际准备合成根逻辑占用 | 526,057,445 |
| 更新下载量 | 未验证 |

体积明细 `sizes.json`；组件归档未变化，沿用已有下载验证，没有重复下载。APFS分配不等于可释放空间。Developer ID、公证、正式更新源、Gatekeeper与干净macOS15+ arm64仍未通过，继续F3。

检查与提交/远端回执分别为 `build/p3-recovery-d2b/final-checks.json`、`delivery-d2b.json`。每阶段临时handoff由脚本直接按格式生成，未自动调用禁止模型调用的源Skill，不入库。D2完成后继续U/I/A/R/G/F，总Goal未完成。
