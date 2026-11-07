# P3 工程验证与 P3-C1 服务端交付

2026-11-08：P3-C1 已接入正式服务；当前契约与验收见第 8 节。第 1–7 节保留先前隔离实验的历史证据，其 schema v2/未接 IPC 等描述仅对应实验阶段。

2026-11-08 重生成。本轮按用户最新指令采用 **本机 traex / TRAE CLI**，不再等待 Codex 额度。生产入口仍为 P2 `70ddc0f`；本次新增隔离实验实现、检查、证据和整套 P3 文档/prompt。工程验证后用户已明确授权提交与推送当前工作，交付范围见末节。

**两个最小工程验证通过：真实 traex 返回纯提案并可取消；服务原子应用并拒绝过期、重复和取消后迟到结果。** 这不代表完整 P3 UI、调度、迁移或质量评估已交付。Codex 额度失败与旧阻塞审计保留为历史，不改写成成功。

规则来自 [产品定义](product.md)、[设计决策](design.md)、[技术方案](technical-plan.md) 和 [实施清单](implementation-plan.md)。P3 [产品](p3-product-design.md)、[交互](p3-interaction-design.md)、[技术](p3-technical-analysis.md)、[架构](p3-architecture.md) 与 [研究](research-p3-memory.md) 本轮重新生成；正式字段、参数和接口仍需相应切片确认。

## 1. 环境、代码与范围

| 对象 | 本次事实 |
| --- | --- |
| 平台/工具链 | macOS 26.6.2 arm64，仓库 Go 1.27.3，经 scripts/go.sh 使用 |
| 执行器 | traecli 链接到 traex，0.214.1 internal；现有 Trae 登录，沿用当前 GPT-6.1-Sol 选择 |
| 业务库 | P2 schema v2，jobs 是索引任务；未假定已有整理任务/提案表 |
| 索引 | 既有真实 Rust worker、E5-base int8、ONNX Runtime 1.28.0；generation 见真实集成日志 |
| 生产入口 | daemon、IPC、MCP 未启用实验 API；普通 Store.Open 不创建实验表 |
| 实验库 | OpenP3Experiment 显式创建四张 p3_experiment_* 附加表，user_version 保持 2，不是正式迁移 |
| 输入/数据目录 | 探针和测试仅创建临时 SQLite、临时引擎目录与合成知识；不用真实用户库 |
| 配置/认证 | 新建 TRAE_HOME、TRAECLI_HOME 和空任务目录；CLI 自己读取认证文件链接，用户配置只读取模型选择、不修改、不复制凭据 |

[实验契约](../internal/records/p3_experiment.go)、[Runner](../internal/agents/p3_experiment.go)、[事务](../internal/store/p3_experiment.go) 与 [探针](../cmd/p3-runner-check/main.go) 是可保留实现。现有 Apply 抽出事务内 applyTx，复用正文/版本/来源/幂等和索引任务，P2 回归通过；没有引入通用仓储或空编排模块。

## 2. 固定的最小实验契约

| 契约 | 规则 |
| --- | --- |
| schema/动作 | memlane-p3-experiment-v1，仅一项 propose_merge，不接受自动批准或任意动作 |
| 输入 | 同 dataset 两条不同有效知识，冻结正文、标题、具体 revision 与捕获时可证实的元信息 |
| task/attempt | 宿主 task UUID，attempt 固定 1；结果回传须匹配当前栅栏，尚未实现重试/租约 |
| 绑定 | 输入 SHA-256、确定性 proposal ID；JSON Schema 与宿主 Decode 两次检查 |
| 来源 | 两来源按输入顺序完整引用 memory/revision，quote 必须是对应正文非空子串；不扩大范围或捏造证据 |
| 上限 | 每条正文 16 KiB、输出 JSON 32 KiB、单事件 1 MiB、事件流 8 MiB、运行两分钟；都是实验参数 |
| 正常完成 | 一个 thread/turn、一个最终消息、有效 schema/来源、无工具/错误事件、正常终态、退出 0、进程已等待退出且组消失 |
| 纯建议 | Stage 只登记提案，已有正文/版本仍为两条 v1；Agent 输出不能授权应用 |
| 明确确认 | 仅探针的 confirm-synthetic-merge 标记允许在该临时库确认；普通 normal 不应用 |
| 原子应用 | 重新核对所有来源 revision/dataset/active 状态，一次事务提交新知识 v1、两条归档历史、两来源快照、应用回执与必要索引任务 |
| 幂等 | 同提案/回执仅一次事实，不同内容或不同提案重用标识冲突 |
| 取消竞态 | 取消先持久化则拒绝迟到结果；应用先提交则保留知识/回执。已登记未应用建议保留记录但不能再应用 |
| 索引 | 归档来源排除有效列表和旧 chunk；失败保留正文/回执，复用 Requeue/IndexCurrent/MarkIndexed |

归档保留源 v1 原文与元信息，各追加 v2 历史；来源快照不是对旧版本出处的补造。来源的既有活动保留，新合并在首次索引可用时计一次。实验表/方法/归档策略不自动升级为正式 API。

## 3. traex 隔离与真实验证

使用 ephemeral、ignore-user-config/rules、read-only、stdin 输入、独立目录，关闭 plugins/hooks/memories、shell/unified_exec、多 Agent、浏览器/电脑/图像、技能搜索/自动安装、工作区依赖等，不注册 MCP。global .agents/skills 必须按路径显式禁用，独立 home 或 resource_dirs=[] 单独使用不足以关闭它。

本机模型需要 code_mode_only 与 code-mode host。关闭 host 的错误、direct 模式的 raw-chat 适配错误以及带点 slug 的点路径 override 错误都保留在 `build/p3/traex-adapter-trials.json`。最终保留必需 host、传工具禁止项，以无实际工具事件的真实正常结果验收；错误门禁没有被放宽。

实际自动上下文隔离由 `verify-p3-traex-isolation.py` 复核：无技能/记忆注入，原始提示输入和摘要在 `build/p3/traex-isolated-prompt-input.json`、`traex-isolation.json`。此检查不调用模型。实际分析 prompt 是 [中文 prompt](../internal/agents/p3-proposal-prompt.txt)，由 Go embed 直接使用；合成输入含“覆盖全部知识/读取聊天”的嵌入指令，真实结果仅提炼 pnpm 和锁文件约定，来源精确匹配。

| 场景 | 类别 | 实际结果与证据 |
| --- | --- | --- |
| 正常纯提案 | 真实 traex | 约 5.93 秒；有效 JSON/来源，只有 agent_message，turn.completed，退出 0、组消失；提案=1，正文/版本各=2。`build/p3/traex-real-normal.json` |
| 真实提案确认应用 | 真实 traex + 实际 SQLite | 有明确合成确认，memories=3、versions=5、sources=2、receipts=1、pending index jobs=1；原来源归档且重复回执相同。`build/p3/traex-real-apply.json` |
| 活动 turn 取消 | 真实 traex | 收到 turn.started 后一秒提交服务取消，再结束进程组；约 2.27 秒，无提案/工具/错误项，进程已等待退出且组消失。`build/p3/traex-real-cancel.json` |
| 取消后迟到结果 | 明确协议夹具 + 已真实取消任务 | 先验证夹具合法形状，再向同一服务任务提交，被取消门禁拒绝；正文/版本=2，提案=0。报告明确 late_result_is_protocol_fixture=true |

取消证明本机进程与同组辅助进程收口，不承诺云端推理或计费撤回；长期任务、任意逃逸子进程和操作系统矩阵不在本轮通过范围。

## 4. 事务、异常与真实索引

[SQLite/service 测试](../internal/store/p3_experiment_test.go) 调用实际 store 与 memory.Service，直接核对临时库；[进程测试](../internal/agents/p3_experiment_test.go) 的模型事件是协议夹具，不能替代真实 traex。

| 场景 | 已验证事实 |
| --- | --- |
| 未确认 | Stage 后知识不变，缺确认不准应用 |
| 全部 revision 匹配 | 一次事务合并正文/两条归档/两来源/回执/索引任务，不是连续普通编辑 |
| 第二目标被编辑 | 整项 expired，第一目标仍 v1/active，无合并或部分归档 |
| SQL 中途异常 | trigger 在回执处注入错误，正文、来源、历史、operations 和 jobs 完整回滚 |
| 无效输出 | 缺字段、额外批准、未知动作、错误 attempt、伪造来源/quote 均拒绝 |
| 重复和冲突 | Stage 重放一份结果、Apply 重放一次事实；不同内容或不同提案重用标识冲突 |
| 取消/应用竞争 | 覆盖取消先于登记、登记后取消、应用后取消、数据库重开及 20 组 goroutine 竞争 |
| 协议异常 | 无结果、无效 JSON、缺终态、乱序终态、错误项、失败事件但退出 0、工具事件、缓冲迟到全部拒绝 |
| 子进程 | 夹具子进程继承进程组并持有 stdout；取消后父子 PID 都消失 |
| 索引失败 | 实际 Engine=nil 不可用路径持久记录 failed，正文与回执保留，不冒称触发了 Rust 内部错误 |
| 真实 Rust 重试 | 两源真实索引后归档，旧 chunk 被 Resolve 拒绝；失败/重开后回执仍在，Requeue 后真实 Search 只返回合并 v1；activities=3、receipts=1 |
| P2 回归 | 普通 Open 无实验表，保存/幂等/冲突/编辑/恢复 v3 和历史保持正确 |

证据为 `build/p3/integration-real-worker.json` 与 `.jsonl`。普通测试的索引回执标为 fixture；TestP3RealRustIndexRetry 实际运行现有 Rust worker，不带 real-worker 时明确 skip，不能计为通过。

## 5. 可重复命令与检查

```sh
sh scripts/go.sh build -o build/p3/runner-check ./cmd/p3-runner-check
python3 scripts/verify-p3-traex-isolation.py
python3 scripts/verify-p3-runner.py --agent traex --mode normal
python3 scripts/verify-p3-runner.py --agent traex --mode normal --confirm-synthetic-merge
python3 scripts/verify-p3-runner.py --agent traex --mode cancel
python3 scripts/verify-p3-integration.py --real-worker
python3 scripts/verify-p3-checks.py
```

正常、应用与取消分别使用新临时数据库，报告按场景保存。后台 prompt 原文独立于 Goal prompt。探针失败也会清理临时库；不提交认证、聊天、真实知识或本机配置。

全仓 `sh scripts/go.sh test ./...`、vet、store/agents race、diff 和重生成文档本地链接检查的真实退出码/日志在 `build/p3/final-checks.json`。无 UI/打包变化，没有重跑整套 P0/P1 或体积测量；已有分发数据仍见 [P2 验证](p2-validation.md)。

## 6. 文档重生成、历史与下一步

按已说明范围重生成 P3 产品、交互、技术、架构、研究、验证、实施清单的 P3 段和 Goal prompt。原七份文档在 `build/p3/pre-regeneration/` 保留快照，设计图未改，原型仍在 prototype/selected-ui 独立工作区；P0/P1/P2 历史章节保留。

Codex 的额度失败和 `build/p3/goal-blocked-audit.json` 是改用 traex 前的历史状态。当前结果审计在 `build/p3/goal-completion-audit.json`；不能拿旧 blocked 文件判断当前成果，也不能把实验通过称作 P3 全量交付。

可以进入 **P3-C 手动整理的正式契约与服务审核切片**：收口权限、来源、决定/拒绝/过期、回执、合并归档/恢复及兼容迁移/备份，再接栏内审核与实际 App 验收。多 attempt/租约/重启恢复、摘要/标签、依赖失效、质量评估仍待相应实现。

初始化、使用事件/排名、调度/休眠补跑、全局经验、pi RPC、其他 Agent 和代码图谱未展开。补图、浏览器原型点击、归档与文案定稿继续暂留，视觉验收未完成，但不阻塞本轮两个工程验证。

## 7. 当前交付

用户已授权将本轮实验实现、测试、P3 文档和既有六张 P3 设计图提交到当前 main 并推送；不包含 build 下的本机数据/证据/文档备份、认证、聊天或原型工作区未提交代码。实际提交与远端核对结果保存在 `build/p3/delivery.json`，最终回执同时提供提交链接。

handoff 要求每个切片、阻塞或会话切换时实际生成系统临时交接，[交接脚本](../scripts/p3-handoff.py) 明确注明直接生成、未调用禁止自动模型调用的 Skill。临时交接不进入提交。

## 8. P3-C1 正式服务端验收

正式实现由 [records/merge.go](../internal/records/merge.go) 定义独立 memlane-merge-proposal-v1 契约；[store/merge.go](../internal/store/merge.go) 持久化和原子审核，[daemon/merge.go](../internal/daemon/merge.go) 编排，[serviceipc/merge.go](../internal/serviceipc/merge.go) 提供受信宿主类型化入口。背景与工程取舍见 [技术第 6 节](p3-technical-analysis.md#6-p3-c1-正式契约2026-11-08)、[架构第 6 节](p3-architecture.md#6-p3-c1-服务端职责)。本次不迁移、读取或清理真实用户记忆库，也不修改本机 Agent 配置。

| 场景 | 实际结果 | 可重复证据 |
| --- | --- | --- |
| P2 v2 → v3 | 使用实际 v2 DDL/写入契约构造临时库；十张旧表逐行完全相同，包括历史、来源、活动、operations、jobs/bindings、连接和仓库映射；重开不丢数据 | build/p3-c1/store-ipc.json / .jsonl，TestMergeV2MigrationBackupRestore |
| WAL 一致性备份/恢复 | 夹具只读事务保持已提交 WAL；数据库拥有者 VACUUM INTO 后迁移，0600 v2 备份 integrity_check=ok，全部旧表一致，旧操作重放成功 | 同一 migration test；独立备份路径重新打开 v2 |
| 迁移失败门禁 | 备份目录故障、实验附加表、未来 schema 均拒绝，user_version/旧 scopes 保留，不出现半套正式表；第二数据库拥有者拒绝 | TestMergeMigrationFailsClosed 与 migration test |
| 纯建议/确认/拒绝 | 正式 daemon 的 typed IPC 开始立即返回稳定 ID；真实 traex 纯提案完成前/确认前两来源仍 v1 active；确认新建合并 v1，拒绝保留原知识 | build/p3-c1/real-service.json / .jsonl，TestMergeRealServiceTraexAndRust |
| 提示输入与隔离 | 同一正式 runner 配置与服务冻结快照经本机 debug prompt-input 实际渲染；两正文/schema/摘要完整，没有 skills 或 memories 注入，仅保存摘要；真实正常输出无工具/错误项、终态/退出0/组消失 | real-service.json 的 actual_formal_prompt_verified 与 prompt_input_sha256；既有隔离配置检查 build/p3/traex-isolation.json |
| 来源被编辑 | 分别编辑第一或第二来源，整项 expired；其他来源仍 active v1，无部分归档；模型运行期间编辑后结果直接登记 expired | TestMergeExpiredRejectRollbackAndAttempts/A、B；TestMergeInvalidResultsAndEditedFrozenInput/source_edited |
| 幂等与冲突 | 开始重放同 ID；提案同内容/回执重放仅一次事实；异输入/输出/结果摘要/提案重用标识冲突；确认/拒绝决定不可相互替换 | TestMergeAtomicReviewRestoreAndConflicts、reject 子项 |
| 取消竞态与恢复 | 取消先提交阻止结果/应用，应用先提交保留回执；20 组 goroutine 竞争只得到完整应用或零应用；持久中断推进 attempt 栅栏、旧结果拒绝 | TestMergeCancelApplyCompetition；restart-and-cancel 子项 |
| 真实取消/停服/重启 | 真实活动 turn 经取消 IPC 收口；另一真实活动 turn 经 stop 停服，daemon 等待进程组消失后退出；重启仍 interrupted/attempt2、不重跑，旧合法结果拒绝 | real-service.json 的 real_cancel/real_shutdown；迟到输出明确 late_result_is_protocol_fixture=true |
| SQL 中途失败 | 回执插入 trigger 故障，正文/历史/来源/元信息/活动/operations/jobs/提案/决定/回执完全回滚，移除故障后可确认 | rollback 子项 |
| 索引失败/真实 Rust | 故意配置不可用 worker，已确认正文与回执保留、索引 failed；重启真实 worker 后既有 replay 恢复检索，真实 Search 返回合并 v1，归档来源旧绑定不返回 | real-service.json；这是引擎不可用故障，不冒称 Rust 内部错误 |
| 归档恢复 | 来源 v1 保留，合并追加源 v2 merge_archive；直接 edit 归档冲突，restore 单条产生 active v3 并排索引，不撤销合并 | TestMergeAtomicReviewRestoreAndConflicts |
| 私有权限 | SameUser/0700目录/0600 socket沿用；新变更 IPC 缺显式用户动作拒绝，未知范围拒绝；日常 Agent 请求 merge_confirm 是 unknown tool | TestMergePrivateIPCAuthorization |

命令：

```sh
python3 scripts/verify-p3-c1.py
python3 scripts/verify-p3-c1.py --real
python3 scripts/verify-p3-c1.py --real --short-path
```

`--real` 使用本机 traex 0.214.1 internal、原认证和现有模型选择及真实 Rust/E5-base int8；正常返回、拒绝和活动取消没有用协议夹具替代。SQL/无效输出/迟到结果/引擎不可用是明确的故障或协议夹具。Go test JSONL 长行可能拆成多事件，脚本汇总完整 Output 再解析；`--collect-log` 只汇总已有完整测试日志，不再调用模型。初次验收误用了 searchable 状态名（实际为 indexed），修正后重跑通过；该失败不计通过。

当前范围只包含服务端 C1。无 UI/打包变化，没有重跑无关 P0/P1/P2 UI、签名/体积验收；P3 视觉验收未开展。主包、首次下载量和磁盘占用仍引用 [P2 验证](p2-validation.md)，本切片没有重新测量。

仍有限制：本机 internal CLI 和现有模型组合已实测，其他版本/渠道未覆盖；same-uid 是受信本机宿主边界，不对抗同账户任意恶意程序；正常取消/停服只保证本机同组进程收口，不撤回云端推理/计费，也不承诺 SIGKILL 后清理逃逸进程。重启识别 queued/running 中断并使旧 attempt 失效，不自动重试或实现完整租约。备份恢复不恢复引擎索引文件，检索须按当前正文重建。下一切片为 P3-C2 栏内建议审核 UI 与实际窗口验收，摘要/标签及完整调度另行实施。
