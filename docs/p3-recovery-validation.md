# P3-D2 执行恢复验证

2026-11-08，D2a 持久租约与进程保护通过。D2b 有限自动重试、退避、恢复预算和完整崩溃重启/迟到/部分失败闭环仍未完成，不标 D2 或总 Goal 完成。契约见 [D2 契约](p3-recovery-contract.md)，依赖与其余必需项见 [交付矩阵](p3-delivery-plan.md)。

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
