# coding-memory 草案的来源与证据边界

2026-11-12，只读上游 PR、固定源码文档和公开工单链接；没有重跑模型或产品验收，没有改用户配置、历史数据或 UI。本文追溯提案为何存在；本机实测见[Codex 隔离验证](engineering/coding-memory-codex-validation-20261112.md)。

## 来源链

公开可核对的链条为：研究原型 → SDK-1335 服务端草案 → SDK-1336 插件适配 → 实际 A/B 发现问题 → 补充修正。不能把这条链说成“没有实验就随便提了个 PR”，也不能把它说成“已证明正式效果”。

1. **研究原型。** [PR #6568 的 Why](https://github.com/topoteretes/cognee/pull/6568) 引用了 `cognee-research` 的 `feature/cog-7073-7074-code-memory-demo` 分支。本次公开访问研究仓库返回 404，未取得原型代码、完整实验日志或原始工单。原型存在及其影响目前只能归为作者在公开 PR 中的陈述，不能当作独立复核过的研究结果。
2. **SDK-1335 服务端。** [Cognee PR #6568](https://github.com/topoteretes/cognee/pull/6568) 于 11 月 5 日提出，核对时仍为 Draft，目标分支是 `dev`。其公开描述明确解释需求，并非 GitHub issue #6568；SDK-1335 是另一套工单编号，不能将两者混同。
3. **SDK-1336 插件。** [cognee-integrations PR #581](https://github.com/topoteretes/cognee-integrations/pull/581) 显式链接 [Linear SDK-1336](https://linear.app/cognee/issue/SDK-1336/claude-code-plugin-required-changes)，并依赖服务端 #6568。插件 PR 已于 11 月 10 日合入 dataset-routing 特性分支，版本升到 1.8.0；这不表示服务端草案已合入 `dev`。本次 Linear 页面没有可读取正文，无法补出原 ticket 的完整验收条款或 corrections comment。
4. **实际实验后的修正。** [Cognee PR #6638](https://github.com/topoteretes/cognee/pull/6638) 明确称修正来自 Claude Code 插件、gpt-5-mini、xitkit issues 2–8 的配对 A/B；11 月 11 日合入 #6568 的特性分支。这里的“已合并”不是服务端功能已正式发布。

## 他要解决的具体问题

作者对旧方案的诊断是：执行过程中按十条工具轨迹抽取，上下文窗口有限，缺少工具输入和任务最终结果，因此经验容易泛泛、记住失败却没记住修复，并让旧项目状态不断累积。草案因此改用完整工作单元，关联请求／工具／最终回答，建立类型化经验与状态键。[PR #6568](https://github.com/topoteretes/cognee/pull/6568)

固定版本[设计文档](https://github.com/topoteretes/cognee/blob/ec6f40859e5f2df9e7862dde27d580ac3268ed43/docs/coding-distillation.md)进一步区分 `location`、`pitfall`、`convention`、`constraint`、`change`、`state` 等知识；保留带项目范围的当前状态，给经验关联会话和 Git 信息。它有选择地提炼短经验，不承诺把全部函数契约与整个聊天逐条重现。

插件则把这些经验送回编码会话：一次注入优先选最新测试基线、最近陷阱，再加入相关知识，并标记 Git 变化。这解释了“测试基线”为什么容易稳定出现在首个记忆块，而不应将该块当作全部原始材料。[PR #581 的功能描述](https://github.com/topoteretes/cognee-integrations/pull/581)

**旧状态替换是该提案的明确设计，不是我们碰巧触发的异常。** 官方文档规定：同项目同状态键只保留最新 session date 对应知识，删除旧行，因此需要 delete 权限。例如保留“现在 9 项测试通过”可避免新会话同时看到 7／9 项两个当前基线。它与 Memlane“覆盖／删除前确认”的约束不一致，但该不一致不能直接证明上游方案自身没有意义。[固定设计文档 Replacement](https://github.com/topoteretes/cognee/blob/ec6f40859e5f2df9e7862dde27d580ac3268ed43/docs/coding-distillation.md#the-lesson-document)

## 作者是否测过，以及测出了什么

[PR #581](https://github.com/topoteretes/cognee-integrations/pull/581) 的初始描述明确承认：尚未对带 #6568 的真实服务运行，已有单元测试和停服 smoke。不能用其成千项测试数字代替知识效果验证。这个描述与几天后出现的真实 A/B 修正也不是矛盾：属于不同时间的证据。

[PR #6638](https://github.com/topoteretes/cognee/pull/6638) 是作者报告的实际质量诊断：两次重复实验出现 4 条 pitfall 对 76 条 change，很多“问题→修复”被错误归类；另有 17 对同会话内的近重复知识。作者据此补上失败候选到真实 error trace 的关联，以及跨批候选去重。它还明确留下两类问题：错误 key 与不相关经验可能关联；有些失败被工具记录为 success，当前补丁覆盖不到。

这能证明团队跑过实际编码任务、发现过知识质量问题并针对它修正；不能证明修正后的任务成功率提升或稳定胜过无记忆。公开 PR 没有给出可供本轮复算的完整 A/B 数据、确切 xitkit 仓库／基线 commit 或所有轮次原始记录，不能凭项目同名自行补认某个公开仓库就是实验夹具。

此外，[#6568 的 github-actions 评论](https://github.com/topoteretes/cognee/pull/6568) 提出过分块、候选上限、配置一致性等审查意见。它们是自动审查意见，部分代码后来已变化；不能写成维护者最终定论，也不能据此解释 PR 为什么仍为 Draft。当前公开页面未提供作者对 Draft 状态的完整原因说明。

## 对本机结果应作的判断

本次实际未保留并发纠错知识，触及该提案希望改善的核心问题，值得继续定位；不能用“这是摘要所以什么都可以省略”排除它。另一方面，本次样本不含旧方案对照，且使用另一模型和临时模型适配，无法证明“草案比原方案差”或“根本没有提案价值”。具体 writer 的证据输入断层分析应与真实回执一起阅读，不能只用最终知识条数裁决整个方向。

上游的可取方向与尚未达成的结果可同时成立：完整工作单元、失败与修复配对、当前状态去旧有明确工程动机；本机样本说明提炼筛选仍可能丢关键信息，公开 A/B 也显示证据关联和经验去重尚在修正。是否能满足 Memlane，需要分别验知识效果与产品确认规则，不能用其中一项替代另一项。
