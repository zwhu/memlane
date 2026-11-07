# P3-C4 修改建议与审核验收

2026-11-08。正式 schema v5、v2 结构化建议与逐项审核已通过。本切片复用 C3 任务/快照/原子应用及 C1 合并门禁，不代表全局经验、定期计划、完整租约、使用/引导、其他日常 Agent、代码图谱或正式分发完成。后续保持 [完整矩阵](p3-delivery-plan.md)。

## 契约与环境

C3 v1 摘要/标签格式保持兼容；用户选择同项目有效材料并启用修改建议，跨项目移动须显式选择目标项目。v2 支持 merge/overwrite/delete/move/duplicate/conflict，全部进入独立待审核表，不自动改有效知识。duplicate/conflict 确认仅记录已核对，不能隐式裁决、覆盖、合并或删除。语义、决定、回执、版本/范围/来源与索引规则见 [C4 契约](p3-c4-contract.md)。

本机 macOS 26.6.2 arm64、Go 1.27.3、MyGo v0.7.4、traex 0.214.1 internal、GPT-6.1-Sol；Rust 原文 worker 与 E5-base int8 沿用已验证版本。新旧 Agent 入口过滤未经此次请求授权的原范围片段；服务仍是唯一数据库拥有者，模型只返回 JSON，没有批准工具、生产数据库句柄或聊天导入。

## 真实 App / Agent / Rust

所有材料来自单独合成目录；新窗口按包标识、runtime 配置及数据路径门禁验证，正常模型和活动取消没有使用协议夹具。主流程六动作在 App 内选择、生成与审核，证据在 `build/p3-c4/real-app.json`；报告绑定 fixture 与 identifier。

| 动作 | 实际结果 | 证据 |
| --- | --- | --- |
| 合并 | 确认前两来源 v1 保留，确认后新知识 v1、两来源归档；实际 Rust 返回合并版 | real-app.json 的 merge；merge-pending/applied-real-system.png |
| 覆盖 | knowledge + raw 维护记录分别选择，建议与旧正文并列；确认后目标 v2，Rust 返回具体 v2 | overwrite；overwrite-pending/applied-real-system.png |
| 删除 | 用户确认后逻辑删除 v2、原文历史保留；目标此前真实索引过，Rust 排除删除；在 App 恢复 v1 产生新 v3 并可检索，原删除回执保持 | delete；presentation.json；final-delete-restored-system.png |
| 移动 | 用户显式勾选目标项目；确认前原范围不变，确认后同条知识 v2 在目标范围可检索、原范围排除，当前入口按实际 Dataset 导航 | move；final-move-system.png |
| 重复 | 真实模型识别相同 request_id 约定；App 核对只提交决定回执，两来源正文/revision 保留 | duplicate；final-review-pending-system.png |
| 冲突 | 真实模型保留日期版本与语义化版本双方，优先级未知；核对不改正文、不裁决 | conflict；conflict-pending-real-system.png |
| 拒绝 | App 拒绝合并项，来源仍 v1，拒绝决定持久保存 | real_app_reject；app-launch-resume-r2/r3.log |
| 过期 | 明确的可信 IPC 版本变更夹具使待审 move 过期；App 展示具体旧版本/依据和过期状态，不能确认 | real_source_expiry；final-review-expired-system.png |
| 取消 | v2 真实 turn.started 后在 App 取消，进程回收/组消失、无结果、知识版本不变、最终停服 | cancel-app.json；review-cancelled-real-system.png |

系统窗口图在 `build/p3-c4-screenshots/`。开发前实际打开 home.jpg 与 memory-detail.jpg，验收后实际打开待审核、过期、移动与恢复窗口，核对 key/visible 元信息。三栏、连续浅蓝侧栏、紧凑正文与单色操作保持；待审核与执行完成分开、核对与知识应用分开、执行编号采用真实 attempt 记录，重放应用注明不再调用模型。新版显示补验由 presentation.json 记录，没有用新模型替代已通过六动作。

## 持久化、迁移与边界

- 临时 v4 库包含真实摘要/标签/回执与已提交 WAL，迁移前 VACUUM INTO 一致性备份，24 个旧表逐行保留；备份仍为 v4，完整性检查、旧契约编辑/恢复通过。见 TestOrganizationV4MigrationPreservesCommittedWAL / final-store-core.log。
- 六种动作的确认/重放/冲突，拒绝/来源过期/目的地身份变化/取消，scope_history 中途 SQL 拒绝后整项回滚，再重试不重复。模型结果是明确协议夹具；见 TestOrganizationReviewActionsAndReplay、TestOrganizationReviewRejectExpiryAndRollback。
- 摘要覆盖和同条知识连续覆盖，当前批准版本只绑定本项冻结出处，旧版本关联保留；旧目标版本继续作为来源，不丢失 DAG。见 TestOrganizationReviewSummaryRepeatedOverwriteLineage。
- Agent 原范围权限不能读取移入后的新正文；保存 operation 重放返回 scope_denied，B 读取保留当前正文但隐藏 A Original/SourceDetail。旧两条合并及新整理 prompt 同样过滤原范围片段。见 TestOrganizationMoveAgentSaveReplayScope、TestOrganizationMovedProvenanceScopeFilter / agent-scope-regression.log。

这些数据库/调度边界检查不是实际 Codex/pi/TRAE 新版本的日常接入验收；日常渠道与外部版本继续在 A 切片覆盖。SQL 拒绝不是磁盘满，过期版本夹具不是模型故障，未宣称正式租约、崩溃逃逸进程或云端费用撤回。

## 质量与审查

`TestOrganizationRealReviewQualityBaseline` 对六类固定合成输入各调用一次真实 v2 runner，实际输出逐项读过并对照原文/引用；证据 quality-real.jsonl、quality-results.json、quality-review.json。中文返回有出处摘要；TS/Python 分别添加准确标签；同源只登记重复；冲突保持双方和未知优先级；恶意嵌入与来源不足返回零项。schema/pass 不是质量提升指标，没有推断用户接受率或新独立证据数量。

bits-code-guard 三组审查与补审当前无残留。修复来源继承重复插入、当前范围导航/同名目的地、异步切模式回包、混合材料驱动、报告与根绑定；另修复原 save 回执重放与旧 merge prompt 的跨范围泄漏。原发现、修复和复核在 `build/p3-c4/review/group/`，最终报告 report.html/report.md。

## 检查、包与失败历史

全仓 test/vet 和 store/daemon/agents/desktop race 通过，日志 go-test-final.log、go-vet-final.log、go-race-final.log。完整生产包在 `build/p3-c4-production/`，最终实际窗口包 app-build-final-r2.log，包含 helper/LaunchAgent/Agent 桥；仍为 ad-hoc。并行用户的图标资源/构建配置已被当前工作树构建复用，其修改未纳入本切片提交；正式 F2 会再次核对完整提交与包。

首轮结构化接口拒绝 optional destination，已修正 v2 必需字段及序列化；失败保留 first-package-schema-failure。六动作通过后，驱动未等拒绝按钮出现、过期标签精确匹配错误导致两次停止；按当前停服且无 WAL 的合成库核对六条已应用回执后，只续做未通过步骤，没有伪造或重复计数。当前报告记录 continued_from，最后成功为 app-launch-resume-r3.log。

最终包首轮原地重建后启动超时，presentation-first-start-failure 保留；六秒真实启动探针随后通过，重试后的 presentation.json 通过。尚未稳定复现或确定原因，不把重试成功当成 App 更新/移动故障已解决，F1 继续保留。

当前主 DMG **15,062,802 bytes / 15.06 MB**；首次外置组件成功归档 **285,752,286 bytes / 285.75 MB**（复用既有 Rust/模型归档）；合成准备后的 App + runtime/data 逻辑磁盘量 **525,929,204 bytes / 525.93 MB**。详细口径 final-sizes.json；更新下载量未知。Developer ID、公证、正式 Gatekeeper、正式更新源和干净 macOS 环境仍未完成。

临时 handoff 由脚本按源 Skill 格式生成并核对存在，未自动调用禁止模型触发的 /handoff；阶段提交、普通推送 SHA 与 handoff 回执写入 delivery.json，随后继续全局经验及其余矩阵。
