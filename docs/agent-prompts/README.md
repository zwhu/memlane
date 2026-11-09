# Agent 角色 Prompt

保存日期：2026-11-09。

以下五份中文角色 Prompt 保存自本次对话，按社区来源的方法改编，不是上游原文的逐字翻译，也不包含完整 Skill 的脚本和工具调度。

使用时复制对应文件的全部正文作为 Agent 的角色说明，再补充本轮任务、输入材料和交付要求。项目规则与用户本轮明确要求优先。

| 角色 | 本地 Prompt | 参考来源 |
| --- | --- | --- |
| 交互设计师 | [interaction-designer.md](interaction-designer.md) | Impeccable [shape](https://github.com/pbakaus/impeccable/blob/main/.agents/skills/impeccable/reference/shape.md)；Matt [prototype](https://github.com/mattpocock/skills/blob/main/skills/engineering/prototype/SKILL.md) |
| 设计老师／设计导师 | [design-mentor.md](design-mentor.md) | Impeccable [critique](https://github.com/pbakaus/impeccable/blob/main/.agents/skills/impeccable/reference/critique.md)；Matt [teach](https://github.com/mattpocock/skills/blob/main/skills/productivity/teach/SKILL.md) |
| 产品经理 | [product-manager.md](product-manager.md) | agency-agents [Product Manager](https://github.com/msitarzewski/agency-agents/blob/main/product/product-manager.md)；Matt [to-spec](https://github.com/mattpocock/skills/blob/main/skills/engineering/to-spec/SKILL.md) |
| 测试工程师 | [test-engineer.md](test-engineer.md) | Matt [tdd](https://github.com/mattpocock/skills/blob/main/skills/engineering/tdd/SKILL.md)；poteto [prove-it-works](https://github.com/cursor/plugins/blob/main/pstack/skills/principle-prove-it-works/SKILL.md)；agency-agents [Evidence Collector](https://github.com/msitarzewski/agency-agents/blob/main/testing/testing-evidence-collector.md) |
| 架构师 | [architect.md](architect.md) | poteto [architect](https://github.com/cursor/plugins/blob/main/pstack/skills/architect/SKILL.md)；Matt [codebase-design](https://github.com/mattpocock/skills/blob/main/skills/engineering/codebase-design/SKILL.md) |

“设计老师”指能够评图、解释设计问题并指导修改的设计导师。这里的架构师 Prompt 先交付架构设计，是否推进实现由具体任务明确。
