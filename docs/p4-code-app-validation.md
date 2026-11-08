# G1 App、服务与日常代码工具验证

2026-11-08，G1 对应范围本机通过。G0 底座见 [底座验证](p4-code-validation.md)，正式规则见 [契约](p4-code-contract.md)。G2 文件自动更新、重启/崩溃/取消、派生索引资源和组件升级/回退仍需对应验收；完整代码图谱及总 Goal 尚未完成。

## 真实 App 与 Agent

`build/p4-code/app-static-mainflow.json` / `app-static-mainflow-third.log`：真实 MyGo App 从新隔离根、受控 loopback 签名源下载并准备全部 **301,171,065 bytes** 组件，启动打包 helper/LaunchAgent、Rust worker 0.0.2 及独立 Enola 0.4.30/v351。项目登记不会自动开启扫描；用户在三栏中明确开启 A，B 不建图；TS/Python 函数、文件/行号/SHA/原始 fact ID 和 `src.run → calls → src.greet` 来自真实当前图。停用后代码查询返回 code_disabled，最后成功快照保留，知识表数量仍为 0。冷准备与静态图谱没有 LLM 调用。

实际打开两张设计图后开发，最终 `code-ready-system.png`、`code-calls-system.png`、`code-disabled-system.png` 均已实际查看并对照：连续浅蓝侧栏、紧凑三栏、单色按钮、栏内开启/查询/来源/管理，首页总览不增加固定第三栏。系统截图与 native key/visible 回执在 `build/p4-code-screenshots/`，不使用原型截图替代。

`actual-traex-code-tool.json` / `actual-traex-code-tool-third.log`：在该实际 App 连接固定 **traecli 0.214.1 / internal**，Agent 沿用自身默认选择 **GPT-6.1-Sol**；上游实际模型未报告，保留 unknown。真实 `memory_code_search` 返回 A 的当前 generation/snapshot、TS/Python 两个符号及文件 SHA/fact ID；没有保存记忆或扫描未授权项目的工具。正常退出及 process_group_gone 均成立。实际 App 断开等待连接撤销、自有 metadata 删除及完整 UI 回执，原知识和其他配置保留。仅该版本/渠道的新代码工具有此证据，不推断 IDE、其他 CLI 或 pi/Codex 新工具也已通过。

## 受信服务与迁移

schema 11→12 在修改前创建一致性 VACUUM INTO 备份。`g1-migration-v11.log` 用含 committed WAL 的临时旧库核对备份 integrity/user_version、所有旧表内容一致，升级不创建已开启 worktree。新增表仅用于真实 worktree 授权、持久 jobs 和动作幂等回执；旧知识/使用/来源/历史未变化。

`g1-code-jobs.log` 是明确 NO MODEL 的真实 SQLite 契约/故障集成：独立 dataset、stable action replay、并发去重、generation、停用/中断/迟到拒绝、相同失败 digest 不循环重试及原子发布。第二通道故障回滚，current 和任务不能部分提交。

`g1-daemon-code-faults.log` 额外调用实际 daemon helper：SQL ready-publication ABORT 经有限三次短事务重试后进入 publication_failed，未产生 current；旧批次的已停用记录在首次 Capture 前被拒绝；明确 reader instrumentation 故障夹具证明初始扫描 owner 已登记，停用取消 context。夹具不代替真实 Enola/Rust/App 正常流程。全库不可写后 owner 退出与实际启动恢复尚需后续故障验收，此处仅有源码栅栏。

stop/quiesce 与发布共用 codeStopping/mutex；旧 generation 不能更新 current。网络/文件/模型/worker 等待不持 SQLite 写事务。helper owner FD guard 先获 S 才运行，服务消失杀整组，FD 不交 Enola；该接线正常 App 已运行，崩溃故障仍在 G2/F。

## 审查、失败历史与包

Bits Store/runtime/UI 三组和跨组 review 修后 active 0，四项原发现保留 resolved：发布错误丢弃、首次扫描使用旧授权、UI 迟到动作覆盖新选择、失效来源保留旧详情。新增真实 Agent driver/隔离 gate 补审发现断开异常跳过关闭 App/service，已拆成独立清理尝试；`g1-agent-cleanup-fault.json` 的 NO APP/MODEL AST 故障夹具证明断开失败仍调用关闭，两个错误均保留且结果为 failed。补审与最终提交清单见交付回执。`g1-full-test.log`、`g1-vet-after-review.log` 和 `g1-race-after-review.log` 记录全仓 test/vet 与受影响 store/daemon/desktop/agents/codegraph race；必要新故障集成另有上述日志。

保留失败：`first-app-failure/` 是 driver 误点自动准备期间不可用按钮；原 flags 初始化错误不能作实际 Rust/Enola 证明，analysis.json 明确校正。`second-app-failure/` 已实际得到符号/来源，但 driver 把组合 row 标签当纯名称比较而超时；改用稳定节点 key 后第三轮完整通过。`agent-path-failure/` 是隔离 HOME 不含已知 CLI 安装，模型未启动；后在 App 明确填写实际路径并检查。`agent-version-expectation-failure/` 有真实成功工具，但错误预期版本字符串；使用实际完整 `traecli 0.214.1` / internal 后完整重测及断开通过。没有把这些原命令改标 passed。

`g1-sizes.json`：arm64 本机验证 DMG **15,611,848 bytes**，App 普通文件 **35,062,929 bytes**，首次额外冷下载 **301,171,065 bytes**，App 加合成 prepared client 根的逻辑文件量 **593,419,189 bytes**。worker 归档 69,952,134 bytes，Enola 归档 11,719,969 bytes，后者不含 Python runtime。更新下载量未测，不记为 0；不包含开发工具链、Agent 自身模型或源项目。该测量不是干净系统或正式发布体积验收。

完整验证包包含 UI/helper/LaunchAgent/Agent 桥，只有本机 ad-hoc 完整性。Developer ID、公证、Gatekeeper 正式信任、正式更新源、干净 macOS 15+ arm64 仍未通过，不能用受控签名源或开发机替代。G2/R/A1b/A2b/F 继续，总 Goal 保持未完成。
