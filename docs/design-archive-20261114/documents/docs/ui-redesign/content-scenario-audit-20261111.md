# K/L、H03/H04/H07、E05/E06 只读工程核对

2026-11-11，主任务准备 005 source freeze 时核对。只更新工程账本，不改生产 Go/Python、不构建、不运行 Native 或模型；本轮没有新增通过的场景或测试结果。19 行从“未检查”细分为 10 行“已实现待验证”、9 行“待实现”，没有改成“已验证”。对应逐项字段、缺口与证据均写入 [覆盖账本](scenario-engineering-coverage.md)。

## 设计与证据分层

当前 dirty 原型目录 `build/p3-prototype/prototypes/ui-redesign-20261109/` 保留未提交改动；目录 SHA256 仍为 `e549e9846e5a617a44828934e09d9678f30f411b8893cc28d57a67b0a73d93b1`。读了当前 J-K-L-M-DESIGN、F-G-H-I-DESIGN、全量交接与现行命名/导航纠正。没有 checkout 或改写原型。

实际打开 K01–K09、L01–L05、H03/H04/H07 普通和窄图：`artifacts/navigation-cleanup-20261110/normal-*.png`、`narrow-*.png`。为便于逐项读图生成只读拼图于 `build/scenario-ledger-review-20261111/` 并实际打开。E05 打开 `artifacts/full-design/final-maintenance/narrow-areaassignmentcaseassignmentmismatch.png`，E06 打开 `artifacts/e-projects/20261110-004/project-and-global-rule.png` 及窄图，并读当前 `full-design-maintenance.js:assignment`、`e-projects.js`。E 的旧截图仍含旧侧栏名称，仅用其正文几何/字段作线索；当前导航以移除“全部记忆”和“通用知识”规则为准，不能恢复旧入口。

本轮源码为当前未打包源树，不声称等于旧限定 Native 包。历史证据要保留其包名、日期与模型限制：

- 通用知识 `ui-fidelity-20261110-015/...common-knowledge-005`：旧导航下的分类、偏好、复制、条件、索引与独立 scope 权限切片，无模型。
- 提炼 `...016/...experience-distill-005` 与 `...017/...experience-recovery-005`：真实 pi 0.84.1/DeepSeek deepseek-flash，候选生成/编辑/取消/独立保存及草稿/原回执重开；不代表全部后端、全部故障或最新页面已验。
- J/K `material-organization-fidelity`、`material-context-004`、`material-recovery-007`：真 pi 的合并/拒绝/恢复切片与其他七类组件/事务检查；不能把其他动作的合成渲染写成每类真实模型窗口通过。
- P1 索引失败、C2 归档来源恢复是旧实际功能证据，不能替代当前 H03/H04/H07 普通/800 窄窗完整流程。
- 当前手动草稿 epoch 文件、原请求屏障和 F 筛选有最新源码定向/race记录；尚不属于正在运行的 004，也不是 005 Native 验收。

## A05/A07/C03 连接账本只读校正

2026-11-11重新核对当前source与005冻结摘要：`agents.ComputerConnectionError`、`VerifyProtocolConnection`、`daemon.installConnectionBatchItem/verifyConnectionBatchItem`及`desktop.machineConnectionEligible/selectedConnectionTargets`已包含pi 0.84.1和Codex CLI 0.167.0／App内0.167.0-alpha.3.2的严格电脑级路径；相关源码与005摘要一致。`TestAgentBatchSupportsVerifiedCodexVersionsWithSeparateConfiguration`覆盖两款可选及配置目录分离；已连接／不支持项禁用有独立回归。此前三行“其他适配待实现”漏记最新实现，现改为已实现待验证，不能由发现了未支持Agent反推首版必须全部适配。

真实证据分别是pi历史限定Native切片、Codex两版本电脑级无模型协议／并发目录隔离，以及受管70794的授权→旧代次失效→重载→在B重新验证。后者`build/goal-codex-processing-20261111-001/connection-lifecycle-runs.json`两项exit0、real_model_calls均0；证明不在A/B项目互用，不能冒称Codex实际模型或新Native多选通过。当前需要005普通／窄窗多选、逐项安装重载验证、仅失败项重试、取消保留成功、重开和键盘分支的连续窗口验收；未发现这三行需继续开发所有Agent适配的依据。本轮仅校正文档，不运行测试、Native或模型。

## E05/E06

E05 当前原型包含待归属提交列表、真实工作区线索、归属项目选择、所选项目与实际仓库不匹配提示、打开匹配项目和禁用确认。当前 desktop 未找到对应 assignment 状态/入口或待归属提交账本。已有连接/项目授权/注册工作区校验仅构成安全门禁，不等于归属处理 UI。标待实现。

E06 当前设计有“个人偏好不同，本项目采用 pnpm”入口、项目与通用偏好双卡、各自来源/条件和“本项目采用”标记。当前 app/experience/project_settings 只有静态项目优先说明和独立范围保留，未找到该对照页。标待实现；L02 的冲突提示分支同样未闭。

这些是既有场景缺口，已告知主任务，不为此改 source 或要求重打冻结包。

## H03/H04/H07

H03：`mutations.go:applyMutation/continueFromLatest` 的 CAS 冲突→GET 最新→保留草稿→明确改变 revision 基线已在；`manual_drafts.go` 保留源版本及原保存 operation，不因新源版本丢草稿。但 `app.go:editor` 只显示 MaxLines(3) 的最新正文，没有设计要求的完整“查看最新正文”入口/对照，因此整行待实现。后续应同时验证最新正文很长、多次并发更新、退出重开后再冲突。

H04：保存结果的正文与索引状态分离；`retryIndex` 不重做保存，成功/失败都保留已有正文，旧库回执被 epoch 拒绝。明确布局缺口是失败提示/重试目前藏在默认折叠“保存与检索状态”，固定图首屏显示“正文已保存，索引失败”卡。标待实现，仍保留旧 P1 真索引故障与源码 epoch 回归证据；未称新版失败→重试失败→恢复已通过。

H07：现有历史/恢复预览和 `restoreMemory` 固定 ExpectedRevision，确认产生新版本；C2 真实归档来源 v2→恢复 active v3 后，合并知识仍 v1。当前 Store.List 默认 active，归档/已删条目须经历史来源、日期活动或持久来源关联定位，不能说已有通用归档浏览器。当前版本的该完整定位→历史→确认→新版本→合并仍保留，普通/窄窗待复验，标已实现待验证。

## K01–K09

共同实现位于 `organization_results.go`、`organization.go`、`organization_persistence.go` 和 Store 原子审核事务。冻结输入中的每条来源正文/revision、建议、Reason、目标范围、差异和确认影响均按实际候选显示；普通双列、窄窗堆叠，footer 独立。历史 schema23 材料预览保存同项理由、焦点、滚动与原决定，确认前不会先改有效知识。

- K01 合并：全部 Sources、逐源差异、归档影响与原子结果/出处均在。最新多来源/长正文/失败/800 实窗矩阵待验。
- K02 覆盖：冻结原文与建议、逐项差异、原因及产生新版本说明在。真实覆盖候选/目标移动和失败重放全窗口待验。
- K03 删除：逻辑删除、检索排除、历史保留与恢复新版本说明在；未见被引用影响明细或同审核到单条恢复的完整 UI，标待实现。
- K04 移动：启动前明确 Destinations，审核显示目标与访问影响、服务校验真实授权。当前审核缺设计的独立“明确接受跨项目移动带来的范围变化”勾选，标待实现；不能把启动前范围选择当该控件。
- K05 通用候选：标题/通用表述/适用条件编辑、原候选/引用、独立保存与拒绝在；真实多来源、字段错误与撤权窗口待验。
- K06 确认：Busy/同 receipt 防重复、事务原子提交、正文与索引分离、失败保存原请求在。旧真合并和七类 SQLite/组件矩阵只支持相应部分。
- K07 拒绝/离开：可选理由上限、同回执重试、同项重入、持久焦点/滚动和退出 barrier 均在；007 真 pi 及中继丢弃决定响应有证据，最新包全类拒绝/停服退出待验。
- K08 过期/撤权：当前 revision/dataset/active/SourceErrors 门禁和原/当前版本对照在；缺原型“重新整理相关材料”专用动作，权限撤销/读取失败未分成明确状态，标待实现。
- K09 混合建议：自动保存/待确认/已拒绝/过期失败分组与真实待确认数在；逐项理由、滚动和决定恢复在。当前下一项遍历所有需审核类型而非只跳待确认；需核对混合终态连续体验，不把同任务拒绝/合并两项切片泛化为五种并存通过。

对应已存在专项：`TestMaterialReviewMatrixRendersAllActionsAtBothWidths`（1280/980，非完整 800 Native）、`TestMaterialReviewSourceChangesBlockNewConfirmation`、`organization_context_test.go`、Store review actions/replay/reject/expiry/rollback。只读确认其存在与断言，不在本轮重跑或宣称新通过。

## L01–L05

L01：真实 preference/experience scope 分类、数量、空态、匹配详情保持在；当前新增 F 来源/范围/当前 Version 更新时间筛选与旧通用知识 Native 005 的导航不同。已实现待验证，不能直接借旧图关闭最新页面。

L02：偏好新增/编辑、标题正文、历史与索引、当前手动草稿重开均在；缺 E06 具体冲突提示/对照与采用说明，标待实现。

L03：`experience.go` 复制预览读取原来源版本/只读正文，可改标题与条件，取消返回项目位置；新建独立 experience，不调用模型。来源变更禁新确认，已提交原请求仍可恢复，不改原项目。最新包长文本/退出/恢复/故障组合待验，已实现待验证。

L04：`experience_distill.go/review.go` 已有生成/取消/失败、标题/通用表述/条件可编辑、模型原候选、配置冻结、确认/拒绝、服务预览持久恢复；原账本“条件编辑方式待设计”已过期，已更正。真实 pi 限定主线和恢复 005 支持该实现，不能替代 Provider/Codex/Magpie 或所有未知启动/多任务故障窗口。已实现待验证。

L05：`knowledgeProvenance` 可显示具体来源版本、变化标记并打开阅读器/当前来源知识；未核验旧全文不可复制，权限不随来源关联扩大。本次原审阅时尚缺明确权限提示；随后主任务合入 G06，`sourceReaderFailureMessage` 已有权限/撤权专用提示，并禁止在无效授权上原地重试，原“均为通用提示”结论已过期。完整固定图“没有这个项目的访问权限”专用页几何、返回/来源版本/撤权/重开整体仍需对照；旧 Native 回看与 Agent IPC scope_denied 不替代新版整场景。状态仍保留待实现，不提高 Native 通过。
