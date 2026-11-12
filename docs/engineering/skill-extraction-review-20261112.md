# 项目工作流的Skill拆分审查

2026-11-12。已拆出两个repo内Skill；其他场景仅审查，不继续创建或修改本机Skill配置。用户最新要求：只放repo。Skill方法、项目规则、工具与事实各维护一处，不把具体产品场景逐个变成Skill。

## 已完成拆分

- [plan-change-impact](../../.agents/skills/plan-change-impact/SKILL.md)：整体影响、依赖、关键未知和实施顺序。
- [verify-continuous-scenarios](../../.agents/skills/verify-continuous-scenarios/SKILL.md)：连续验收、前置条件、证据审阅、缺陷关联和收尾。

AGENTS与工程规范已改为方法入口，项目模板保留适配字段。两个Skill通过skill-creator的quick_validate；校验仅证明结构有效，尚无独立任务行为验证。不复制task.py、run-scenario.py、磁盘保护、产品定义、失败计数和工程账本到Skill。

## 项目其他场景的拆分判断

审查覆盖[产品场景A–O](../ui-redesign/scenarios.md)、[设计交接](../ui-redesign/full-design-handoff.md)、[工程规范](workflow.md)、[开发入口](../development.md)及以下专项契约。这是方法抽取审查，不是125场景逐项功能或源码验收。

| 场景族 | 可抽取的方法与建议承载 | 仍留在项目中的内容 | 依据 |
| --- | --- | --- | --- |
| A初始化／组件准备 | 环境复用、恢复进度与前置检查，归入连续验收，不单建初始化Skill | 三步流程、组件兼容与下载策略 | [复用契约](../ui-redesign/reusable-setup.md) |
| B首页／D日常使用统计 | 指标必须由持久事实证明，作为验收专项，不单建报表Skill | 返回／读取／采用口径、首页展示 | [日常使用证据](../ui-redesign/daily-agent-usage-harness-20261111.md) |
| C Agent接入／官方采集 | 候选：官方能力核实→最小适配→增量安装→真实宿主往返验证 | 支持版本、电脑级配置、项目授权、具体插件与信任状态 | [接入契约](../ui-redesign/agent-connection-contract.md) |
| E项目范围／F搜索／G阅读／I关系图 | 跨页上下文、来源与授权变化、实际产物核对，作为连续验收专项 | 项目优先级、内容分类、搜索范围、图谱入口 | [内容分层](../ui-redesign/content-model.md)、[连续性](../ui-redesign/content-continuity-20261111.md) |
| H编辑／历史／K审核／L通用知识 | 冻结依据、冲突、确认与幂等的验证方法，归入连续验收；不按动作各建Skill | 哪些动作需确认、恢复新版本、跨项目授权 | [整理契约](../p3-organization-contract.md)、[通用经验契约](../p3-global-contract.md) |
| J后台任务／M计划 | 单一任务拥有者、取消、迟到结果、重启与有限恢复，作为异步任务验收专项 | 计划时间、尝试上限、租约、启停语义与实现 | [恢复契约](../p3-recovery-contract.md)、[计划契约](../p3-schedule-contract.md) |
| N备份／恢复／索引／旧数据迁移 | 候选：只读盘点→可靠身份比对→隔离演练→切换核对→回退验证 | 数据源方向、schema、授权退休、历史保留及实际迁移状态 | [迁移核对](official-data-migration-20261112.md)、[备份恢复](../p4-backup-recovery-contract.md) |
| N更新／运行包切换／清理 | 候选：建立完整依赖集合、保护待消费产物、验证迁移后再清理 | 100GiB门禁、保留包清单、签名／服务注册与脚本 | [运行包误清理](runtime-package-cleanup-incident-20261111.md)、[产物存续期](build-artifact-lifetime-20261112.md) |
| O异常／草稿／焦点／窄窗口 | 跨状态组合和受影响回归，归入连续验收；不为单个错误建Skill | 草稿持久化边界、实际窗口尺寸和布局规则 | [连续性](../ui-redesign/content-continuity-20261111.md)、[场景清单](../ui-redesign/scenarios.md) |
| 全页设计还原 | 优先候选：确认设计基线→实际看图／交互→整页结构映射→状态及窗口对照→差异回写 | 已认可稿、MyGo API、布局参数与当前偏差 | [全量交接](../ui-redesign/full-design-handoff.md)、[UI失败纠正](conversation-ui-stop-20261112.md) |
| 动效实施 | 作为设计还原Skill的按需参考，不单建Skill | 状态映射、时序几何、稳定Key、减少动态效果和Native参数 | [动效契约](../ui-redesign/motion-implementation.md) |
| Agent模型桥接／Provider／Magpie | 先复用需求影响与连续验收；和Agent接入候选共享上游契约核实方法，但分开连接与推理证据 | 实际SDK类型、schema、凭据隔离、取消协议与模型质量 | [桥接需求](../agent-model-bridge-requirements.md)、[处理专项](official-session-processing-20261112.md) |
| 缺陷诊断与复盘 | 复用现有diagnosing-bugs的诊断能力；本次停止判断放连续验收，不再创建同类Skill | 具体失败计数、停止阈值、根因证据、复盘记录 | [工程规范](workflow.md)、[复盘模板](incident-template.md) |
| 技术调研／领域词汇／代码审查／原型 | 已有research、domain-modeling、code-review、prototype等能力，按实际任务选用，不创建Memlane重复版本 | 官方优先的产品决定、已批准设计、词汇与项目状态 | [产品定义](../product.md)、[设计规则](../design.md) |

## 后续候选优先级（本次不创建）

1. **按已认可设计还原**：跨多个页面反复出现；解决原型探索与固定稿生产实施的不同目标。先映射整页结构，再做细节；不把浏览器或headless通过外推为Native通过。
2. **运行包切换与资源清理**：错误后果大且已有两种不同事故。方法可复用，执行必须依赖项目的确定性保护工具；Skill不能取代依赖清单与删除门禁。
3. **上游官方集成与宿主验证**：集中官方能力核实、最小适配、配置所有权与真实宿主往返；连接、自动采集、推理和产物分别证明。优先复用已有调研能力，不写新的通用研究Skill。
4. **数据迁移与恢复验证**：先在实际迁移任务中验证复用价值，再抽方法。备份、迁移和索引重建的保证不同，不能以一个成功命令互相替代。

刷新、迟到回执、选择复制、单项取消等先作为连续验收的任务场景，不增加更多顶层Skill。产品策略、错误码、SDK适配和所有实时账本继续留在对应模块；“四次停止”和磁盘数值不提升为通用Skill规则。

## 入口边界检查

人工检查的路由例子：跨模块知识来源开发使用影响分析；已有候选的断连→恢复→撤权验收使用连续验收；单行文案不加载完整流程；崩溃根因定位优先已有诊断能力；新UI方向探索用原型能力，固定稿还原属于上述候选；磁盘清理只读审计不因候选存在而授权删除。未执行真实模型、数据迁移、Native复验或清理实验。
