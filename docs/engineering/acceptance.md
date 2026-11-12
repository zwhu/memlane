# 工程规范实施验收清单

| 项目 | 当前状态 | 证据／下一步 |
| --- | --- | --- |
| AGENTS入口、规范、用例与复盘模板 | 已落稿 | 本目录；不等于生产交付 |
| 需求整体影响与关键依赖分析 | 入口与模板已接入 | task-brief-template.md；写入现有任务账本，不自动判定理解完整，实际任务效果待观察 |
| 通用方法Skill拆分 | 两个repo内Skill结构校验通过 | .agents/skills/plan-change-impact、verify-continuous-scenarios；无仓库外安装；审查候选见skill-extraction-review-20261112.md，尚无独立任务行为验证 |
| 连续场景命令编排 | 工具定向与CLI自验通过 | run-scenario.py；6项测试覆盖前置阻断、不重试、证据不覆盖、收尾失败与中断；CLI合成证据build/scenarios/6e177bda-d49b-4be2-92a7-9d582a38c4c2，不证明业务或Native |
| Go/MyGo锁版本与内置静态检查 | 已有；规范明确 | Go1.27.3、MyGo0.7.4；015 test/vet日志 |
| 第三方lint | 未引入 | 若引入先选兼容版本并锁定，不能用未锁最新版 |
| 网关前置拒绝安全留证 | 源码定向/race通过 | goal-graph-early-receipts-20261111；实际包验证待做 |
| Adapter分层安全错误 | 源码11项通过，v12已冻结 | goal-graph-adapter-diagnostics-20261111；活组件尚未替换 |
| UI/IPC/后台统一关联与错误链 | 部分已有，未全覆盖 | 本轮仅修复合法请求准入后Acquire/ResolveTask盲区；schema准入前不在覆盖内 |
| MyGo Harness统一入口 | 未完成 | 已有headless/e2e/Native驱动；旧导航驱动须迁移，不能宣称统一完成 |
| 真实问题试运行 | 进行中 | graph-diagnostics-trial.md；015构建通过，实际同身份helper替换EX_CONFIG失败；710e原因仍未知 |

不升级基本链路60%或125场景整体状态。运行包、UI、功能均按原Goal验收。

当前运行门禁：015更新失败且014回退启动仍被系统约束拒绝，已明确停服。六条连续流程为此前历史证据，当前包恢复可用须单独证明。
