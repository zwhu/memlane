# UI 重设计：开发 Agent 入口

2026-11-09。本轮逐组设计并由用户逐项确认。实现前先看本页、对应设计契约及实际截图，不能将历史探索稿或待讨论稿作为已选定设计。

用户最新纠正的 [Agent 电脑级连接规则](agent-connection-contract.md) 已确认：连接先配置当前电脑的编码 Agent，不要求关联项目。新版 A 后半段画面据此修正，仍待视觉评审；上一版将项目/模型准备放在连接页的取舍已作废。

最新补充以 [连接与记忆处理设计](memory-processing-design.md) 为准：连接按 Cognee Web UI 安装 skill/hooks/插件；记忆处理优先 LLM provider，另一方式只选择已连接的 pi/Codex。当前 revision 5 为设计预览，用户已明确先做设计，正式实现未在本轮进行。

| 分组 | 状态 | 依据 |
| --- | --- | --- |
| A01–A04 本机环境检测与安装 | 用户已认可，待按此稿实现并做真实窗口验收 | [A 开发契约](a-environment.md) |
| A05–A10 Agent、项目、连接、第一项任务与恢复 | 已补讨论稿，待用户评审；既有产品规则继续有效 | 原型分支 `a-agent.html`、`A-AGENT-DRAFT.md`；[全部场景](scenarios.md)、[产品问题](../product-questions.md) |
| B01–B08 日常首页 | 按用户最新要求暂缓，先补 A | 待恢复设计，不是最终开发要求 |
| C–O | 仅完成场景枚举 | [全部场景](scenarios.md) |

已认可的 A 原型归档在 `prototype/selected-ui` 分支，路径为 `prototypes/ui-redesign-20261109/`，冻结提交为 `5f4c504bfbc6ee07a571b015f7ccc809d0943848`。此提交仅保存在本地；未推送。按该目录 README 用一条 Python 命令启动浏览器预览。主分支保留规则、截图与核验记录，生产实现继续使用 MyGo，不移植 HTML 原型。

旧 `first-launch-design.md` 中的分离确认/进度、泛化检查详情等描述，在 A01–A04 范围内由本轮 A 契约替代。已有工程通过记录继续作为对应版本的历史证据，不能视为新稿已经实现。

首页继续采用侧边栏＋宽幅总览，日常首页不重复首次使用引导。其余已确认产品规则见 [产品定义](../product.md)、[设计决策](../design.md)。
