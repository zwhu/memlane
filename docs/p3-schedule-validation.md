# P3-D1 持久计划验证

2026-11-08，本机 macOS 26.6.2 arm64。D1 通过；D2 租约、有限重试与崩溃恢复仍未完成。正式契约见 [D 契约](p3-schedule-contract.md)，完整剩余事项见 [交付矩阵](p3-delivery-plan.md)。

## 实际 App / traex / Rust

使用全新 `memlane-p3-schedules-app-*` 合成根，只复用固定组件缓存，不复制业务库、聊天或日常 Agent 配置。独立 App 标识 `io.github.zwhu.memlane.schedules.255f1b1d8d2f`，完整 MyGo 0.7.4 验收包包含 helper、LaunchAgent 与记忆桥；真实执行者 traex 0.214.1 internal，Rust worker 固定 commit `151905c302fc9b5b9aad88d6b13931c09f0eac30` / E5-base int8。

| 验收 | 实际结果 | 证据 |
| --- | --- | --- |
| 草稿与授权 | App 明确选择项目、每日、UTC、未来分钟、窗口和材料上限；保存后停用/next=0、无模型任务；单独启用才计算未来槽 | `build/p3-schedules/real-app.json` |
| 正常真实槽 | generation 2、due=1794083220000，实际自然时间触发稳定 task；真实 traex 返回有出处摘要，原文 v1 保留，真实 Rust 命中摘要 | real-app.json、real-app-run.log |
| 导航与历史 | 三栏计划列表/详情，查看本次整理打开正式任务；终态显示执行完成，并区分审核和应用 | 系统截图、`presentation.json` |
| 活动取消 | App 编辑来源为 v2 后，generation 3、due=1794083340000 自然触发；实际 traex 进程活动时 App 取消，回收且组消失，无结果/条目 | real-app.json、presentation.json |
| 停用与停服 | App 停用成为 generation 4/next=0；明确停止后重开仍显示启动服务，没有 IPC socket | real-app.json |
| 终态与回执核对 | 只读已停服唯一拥有者合成库，将两个 run 绑定各 generation 的原 operation receipt/due，分别 completed/cancelled；任务总数保持 2 | presentation.json |
| 当前检索 | 重启后源 v2 真实 Rust 返回；此前摘要因源变化 stale，当前检索排除；停用计划没有新增任务 | presentation.json |

验收驱动选择实际未来 UTC 分钟，不改变系统时钟、不覆盖产品最小间隔；两个正常/取消任务都使用真实模型，非协议或时钟夹具。首次三次 UI 探针因同名侧栏/Select popup 无 press 动作未成功选项目，均未启用计划或调用模型；第四个新根才通过。失败报告以 initial/wrong-menu/menu-actions 前缀保留，MyGo 0.7.4 popup 选择改用实际 bounds 内指针输入。

原 real-app 报告中的 run.state 是发现时 running 快照，后续任务终态和取消进程断言已通过；presentation 另核对持久终态与不可变启用回执，不修改原历史报告。补验脚本路径 `/var`/`/private/var` 哈希、DTO 键和标签整串比较错误已修正，失败结果保留，最终补验使用实际 service.json.root 计算 socket 并校验同一隔离目录。正常模型没有为报告修正重复运行。

## 存储、时钟与故障夹具

计划/operation/generation、启用唯一项目、原子触发槽位/任务/next、重复调用回执、旧 generation/项目与目的范围变化门禁均由真实 SQLite 集成覆盖。`store-current.log`、`controller.log` 包含以下明确夹具：

- 只补每计划最新一个到期槽位，超过用户窗口记 missed_window；忙项目/no_materials/scope_changed/input_limit 准确跳过。
- no_changes 只参考已有持久结果；取消且没有结果不阻止下一正常槽。动作/目的/executor/model 改变可重分析，单改时间/窗口不重复分析相同来源。
- 每日/每周/小时间隔、纽约 DST gap/fold，Nuuk 跨午夜 gap 顺延到 `2026-03-29T01:00:00Z`；fold 每本地日期只执行一次。
- SQL 触发器故障证明任务/槽位/next 同事务回滚；历史 operation 重放不能取消新 generation。手动/计划/旧 merge 共用执行槽，取消真实 owner context 后释放。
- 停用/代次/冻结身份在 Claim、Stage、自动 Apply 拒绝；已完成建议仍可用户审核，频率停用不抹掉历史。

`ui-fault.log` 是 GUI/IPC 故障夹具：health 成功但随后业务范围读取失败时，计划循环停止诊断仍更新并显示；health 失败保留旧诊断。以上夹具不冒充真实租约、系统休眠、崩溃或 D2 自动恢复通过。

schema 6→7 迁移测试使用非空提交 WAL，唯一拥有者升级前的一致性备份为 v6、integrity_check=ok；两版本全局适用条件、审核决定、全部旧表逐项保留，升级后恢复旧条件产生 v3。实际旧 E helper 拒绝未来 schema 7；复制独立已停服 v6 合成备份后旧 helper 真实读取全局 v3 正文/条件并停止，见 `old-version-compat.json`。此项未验证旧索引重建，不能当正式回退分发证明。

最终全仓 test/vet 和受影响 Store/daemon/agents/desktop race 通过，日志为 `test-final.log`、`vet-final.log`、`race-final.log`。Bits Code Guard 分组、跨组与脚本补审修复 DST/no_changes/GUI 诊断/验收字段问题，剩余确定缺陷为零，历史修复记录与报告在 `build/p3-schedules/review/`。

## 窗口与完整包

UI 开发前实际打开首页/三栏设计图；完成后实际查看 `build/p3-schedules-screenshots/` 的草稿、完成、取消、停用、最终历史和源 v2 系统窗口，对照三栏、连续浅蓝侧栏、紧凑排版和单色控件。计划普通编辑与查看本次任务均在栏内完成。

完整生产 App/DMG 位于 `build/p3-schedules-production/`，深度严格 codesign 和 SHA256SUMS 通过。当前是 ad-hoc 本机包，Developer ID、公证、Gatekeeper、正式更新源和干净 macOS 15+ arm64 仍未通过。用户并行图标资源影响实际包，但其设计/图标/roadmap 改动不纳入 D1 提交。

| 体积口径 | 字节 |
| --- | ---: |
| 主下载 DMG | 15,130,915 |
| App 普通文件逻辑大小 | 33,967,295 |
| 首次额外 Rust/模型归档 | 285,752,286 |
| App + 实际准备合成根逻辑占用 | 525,922,246 |
| 更新下载量 | 未验证 |

细分与 SHA 在 `sizes.json`；组件归档字节沿用实际固定缓存，逻辑磁盘占用使用本轮实际准备目录，APFS 分配不等于删除可释放空间，不包含 HTTP/TLS/失败重试开销。

## 阶段交付与剩余

检查/审查/窗口与包回执见 `final-checks.json`；commit、普通推送的准确远端 SHA、临时 handoff 见 `delivery.json`。handoff 由脚本直接按格式生成并注明未通过禁止自动模型调用的源 Skill 工具，不入库。

D2 持久 attempt/租约、有限自动重试/退避、旧 owner/迟到/崩溃/部分提交恢复继续；使用/排序、引导/初始化、TRAE/traex 日常、pi RPC、完整 Rust 代码图谱与 F 分发也继续。App 原地重建偶发启动失败仍属于 F1 未闭环项；缺 Developer ID、公证、正式源和干净设备不标总 Goal complete。
