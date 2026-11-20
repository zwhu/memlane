# 当前执行边界

模型服务并发回归复用task.py与现有unittest，覆盖多协议、缓存重建、取消、超时及桥接准入；命令与边界见模型服务容量控制。不调用真实模型或启停日常服务。

当前可执行的待整理文档连续回归入口：`sh scripts/regress-project-queue.sh`。用例、证据目录、Native核对和未覆盖边界见待整理链路回归。该入口只复用当前Go/MyGo测试，不恢复下文退役的旧编排器。

2026-11-13精简版使用现有命令和实际Native操作直接验证，不恢复旧全量矩阵。旧run-scenario.py及旧业务专属驱动已随模块退役。下文仅为历史工程说明，不构成当前可执行入口；当前进度见执行记录。

# 连续场景执行入口

`scripts/run-scenario.py`只编排已有命令／驱动，复用`task.py`磁盘门禁与进程组所有权，不新增UI引擎。任务的影响与依赖先按[任务说明](../engineering/templates/task-brief-template.md)写入现有账本。它不自动判断需求是否理解完整、缺陷是否同源或知识质量是否通过。

## 使用

将下例保存为任务自己的JSON，替换为已有测试、IPC或Native驱动的真实argv；例子故意引用待替换路径，不能作为业务证据执行。命令无shell插值，参数分别填入数组。计划文件是受信任的可执行输入，运行前审阅；不要写凭据、用户原文或真实模型响应。日志由驱动负责脱敏。

```json
{
  "id": "reading-recovery",
  "requirement": "同一来源断连恢复后保持正文；撤权后不可读",
  "baseline": "填写源码摘要、完整包与组件版本及设计基线",
  "proves": "填写本轮覆盖的真实入口、数据、状态和断言",
  "does_not_prove": "例如：合成材料阅读不证明自动采集或知识生成",
  "preflight": [
    {"id": "identity-and-fixture", "argv": ["python3", "任务目录/check-fixture.py"]}
  ],
  "steps": [
    {"id": "continuous-flow", "argv": ["python3", "任务目录/existing-driver.py"]}
  ],
  "cleanup": [
    {"id": "owned-resources", "argv": ["python3", "任务目录/cleanup-owned.py"]}
  ]
}
```

```sh
python3 scripts/run-scenario.py 任务目录/scenario.json --check
python3 scripts/task.py -- python3 scripts/run-scenario.py 任务目录/scenario.json
```

`--check`仅检查结构，无任何场景命令执行。实际执行为每轮创建独立UUID目录`build/scenarios/`，冻结计划与SHA，逐项记录退出码、耗时和日志；驱动可用环境变量`MEMLANE_SCENARIO_EVIDENCE`保存截图和小型回执。前置或步骤失败即跳过后续业务步骤，无自动重试；始终尝试所有收尾动作，收尾失败也使本轮失败。硬杀、断电或收尾自身中断不能保证收尾完成，报告保留running／interrupted时须人工核对资源，不从缺失进程句柄推断已回收。

## 前置检查与判定责任

- 任务驱动检查synthetic_only、bundle/root、实际包及源摘要、服务身份、授权代次、夹具数量和有效滚动点；失败停止场景，不通过重种数据绕开。不能由编排器仅凭文件存在推断这些条件成立。
- 连续场景涵盖相关正常、失败、恢复与返回路径，合并准备与采证，不合并不同缺陷计数。新增改动按影响范围选择重跑，不每次重跑无关矩阵。
- 所有命令返回0只记`commands_passed`。Agent检查截图、实际数据与回执后，才能更新要求状态；测试、Native、模型、正式包分别对应其证明范围。
- `precondition`为准备失败，不自动计入产品失败；`unclassified`须依据触发条件、现象和验收标准关联已有具体缺陷。编排器不创建缺陷身份或自动清零累计次数，达到既有停止阈值不得再启动该场景中的受阻步骤。
- 收尾命令必须可重复、只处理本轮所属资源，并验证退出；产物从构建到后续验收消费结束前按现有保留依赖机制保护。编排器不自动注册／释放清理保护，不修改并行任务或日常服务。

此入口已提供命令编排和留证，不代表现有所有Native驱动已迁移，也不代表已完成统一MyGo Harness。
