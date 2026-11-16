# 官方 Coding Agent 方案与 Memlane 职责收口

2026-11-17。仅方案研究：未安装、未调用模型、未修改接入或数据。Hindsight 固定源码为 `958468e44caadfeb6abad22f12cf464d39c37085`，Cognee 插件为 `a176248914fdb32a78354854b300174203b15a5c`；另核对当日官方网页。网页描述不代表本地固定版本已具备全部能力。不将更换引擎或多引擎支持作为既定方向。

## 官方已经负责什么

| 方面 | Hindsight | Cognee | 对 Memlane 的含义 |
| --- | --- | --- | --- |
| Agent 自动接入 | coding-agents 包有共享运行核心和各宿主入口，自动写入会话并注入记忆 | 官方 hooks 采集问答和获准工具轨迹，调用官方 improve，再向后续会话召回 | 不再另造采集、提炼和注入主链；缺宿主适配时只补事件转换 |
| 明确要求记住／查找 | companion skill 配合 MCP 或原生工具；工具和自动 hooks 共用 bank | memory skill 调官方脚本，经 HTTP remember／recall；自动 hooks 同样连接官方服务 | 用户显式保存应走官方能力；不能依赖自动提炼恰好接受该句 |
| 项目共享 | 默认 `coding-agent::{gitProject}`，同仓库不同 Agent 共享；支持路径映射和 worktree 归一 | 写入 dataset 可配置；读取范围另由 recall mode／read dataset ids 控制 | 项目归属应成为清晰配置，不应靠多处内部补丁隐式改变目标 |
| 安装与升级 | install 合并宿主配置；update 刷新运行时不重写配置，固定源码还提供可关闭的自动更新 | 官方插件及其配置入口负责实际运行；本地已验证私有 env、独立凭证、UUID 可通过官方入口传入 | Memlane 管理官方安装、配置和验证结果；不修改安装产物来维持一套长期分叉 |

来源：[Hindsight 固定 README](https://github.com/vectorize-io/hindsight/blob/958468e44caadfeb6abad22f12cf464d39c37085/hindsight-integrations/coding-agents/README.md)、[Hindsight 当前 Coding Agents 文档](https://hindsight.vectorize.io/sdks/integrations/coding-agents)、[Cognee 官方链路说明](https://www.cognee.ai/coding-agent-memory-hooks)、[Cognee 当前配置契约](https://github.com/topoteretes/cognee-integrations/blob/main/integrations/CONFIGURATION.md)、[Cognee 固定 memory skill](https://github.com/topoteretes/cognee-integrations/blob/a176248914fdb32a78354854b300174203b15a5c/integrations/codex/plugins/cognee/skills/memory/SKILL.md)。本地协议实测见[首轮核验](engineering/official-plugin-feasibility-20261116.md)。

## 应借鉴的边界，不能直接照搬的默认值

1. **显式工具与自动 hooks 并行、共用数据。** skill 负责告诉 Agent 何时主动使用工具；hooks 负责模型没调用工具时仍能采集。两者不能互相替代。[Hindsight 官方说明](https://hindsight.vectorize.io/sdks/integrations/agent-plugin)、[Cognee 官方说明](https://www.cognee.ai/coding-agent-memory-hooks)。
2. **项目身份不是目录名字就足够。** Hindsight 默认从主工作树 basename 生成 bank，同名仓库需用路径映射消歧；可以借鉴官方映射能力，不能把它当作无冲突项目注册表。[固定 bank.ts](https://github.com/vectorize-io/hindsight/blob/958468e44caadfeb6abad22f12cf464d39c37085/hindsight-integrations/coding-agents/src/core/bank.ts)。
3. **记忆材料不是完整聊天档案。** Hindsight Codex reader 保留用户／助手文本及压缩工具动作，丢弃完整工具输出。借鉴它的简洁不能意味着对外继续承诺完整 trace／附件；先明确 Memlane 是否确实需要这类档案。[固定 reader](https://github.com/vectorize-io/hindsight/blob/958468e44caadfeb6abad22f12cf464d39c37085/hindsight-integrations/coding-agents/src/core/transcript-codex.ts)。
4. **官方默认不自动成为本项目规则。** 不启用 Hindsight 默认历史导入、自动 survey 或第二运行库；不因希望零补丁就放宽现有权限。当前仅参考方案，不安装或迁移。Cognee 恢复采集补读、final 处理开关的 G01／G02 只证明固定插件 `a176248…` 的行为，既不改为已解决，也不据此断言当前 main 仍有相同缺口；升级设计须按拟采用版本重新核对公开配置与扩展点。[现有核验](engineering/official-plugin-feasibility-20261116.md)。

## 建议做什么（判断，不是已实施）

Memlane 收缩为 **官方记忆系统的本机控制与阅读客户端**：

- **保留**：安装／连接可视化、项目与官方数据组关联、官方权限的配置和核对、原始内容与记忆结果的阅读及来源跳转、真实保存／处理状态；安装版本继续保留工程上的切换／回退能力，不新增产品UI。
- **交还官方**：采集格式、提炼筛选、分窗、索引、召回策略、注入和默认重试。官方已有 UI 也能完成的功能，不再作为 Memlane 独有价值宣传。
- **先作产品取舍**：逐项决定采集恢复边界、自动处理开关、完整工具原始档案是否值得长期定制。保留时须证明官方扩展点可承载；否则明确成本，不在官方函数里继续补规则。
- **暂不做**：Hindsight 引擎替换、多引擎平台、平行采集器、独立正文库或另一个任务调度框架。

下一步应先交付一张“当前产品要求 → 官方配置／公开扩展点 → 仍有缺口”的表，再动代码。验收重点是：同项目换 Agent 后记忆仍可用；明确保存可读回并显示；权限真实生效；官方升级无需逐函数修补。现有数据及单一日常库保持不变。

尚未实测 Hindsight 安装、提炼质量、权限与本机资源，也未证明两项固定版 Cognee 差异在当前官方版中的状态及公开扩展点替代方案；不能据本研究断言 Hindsight 更优或零补丁迁移已经可行。
