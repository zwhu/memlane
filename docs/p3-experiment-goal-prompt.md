# P3 已完成实验的复查 Goal prompt

2026-11-08 重生成，执行器按用户最新要求采用本机 traex / TRAE CLI。此文件是可复制的任务 prompt，不是运行回执；当前成果先查 [验证记录](p3-validation.md) 和 [实施清单](implementation-plan.md)。实际发给后台模型的分析 prompt 单独保存在 [p3-proposal-prompt.txt](../internal/agents/p3-proposal-prompt.txt)，由 Go embed 直接使用。

## 可复制执行内容

```text
/goal

推进 Memlane P3 的两个最小工程验证：
1. 真实后台 traex 在隔离环境中返回纯提案，进程和子进程能可靠取消。
2. 服务原子应用提案，拒绝过期版本、重复提交和取消后的迟到结果。

先阅读 AGENTS.md、docs/product.md、docs/design.md、docs/technical-plan.md、docs/implementation-plan.md，以及全部 P3 产品、交互、技术、架构、研究和验证记录。已确认规则优先；实验字段、参数和接口不自动升级为正式 API。

开始时核对当前分支、未提交内容、实际 schema 和已有证据。生产入口基线为 P2，保留已有用户修改、设计图和原型，不因文档提到对象就假设已经存在。已经通过且仍匹配当前实现的验证优先审计，不盲目重做。

优先复用已有 Store.Apply/applyTx、revision、operations、索引 jobs、Current/Resolve/Requeue/IndexCurrent。冻结最小输入、来源和预期 revision，绑定 task、attempt、input digest 与结构化输出；契约明确标为实验，不创建空模块或空表。

采用已安装 traex / TRAE CLI，不升级，不改用户配置或认证。分别隔离 TRAE_HOME 与 TRAECLI_HOME，由 CLI 读取现有认证文件链接；保留当前模型选择。使用合成快照、空任务目录和独立 ephemeral 会话，不读真实记忆或聊天，不注册生产写工具。

按本机版本核对实际工具与自动加载能力。不能只设置独立 home 就假设 .agents/skills 已关闭；用实际提示输入和事件验证。模型所需协议辅助 host 与工具执行权限分开处理，保留有效协议但限制工具。不要用忽略错误、自然语言成功或退出码 0 代替有效 schema、终态、进程和来源证据。

验证正常输出、无结果/无效输出、取消及迟到结果；真实调用至少覆盖正常与取消。协议夹具必须标注，不能代替真实 Agent。取消先持久化服务门禁，再停止进程组；等待退出并检查存活。认证、额度或协议阻塞时保留原始错误，继续不依赖模型的工作，不宣称完整通过。

使用临时数据库和合成知识验证：
- 全部来源 revision 匹配时，一个事务提交合并知识、两条归档历史、两来源快照、唯一应用回执和必要索引任务。
- 分析后任一目标被编辑，整项提案过期，无部分覆盖。
- 同提案/回执重放只应用一次；不同内容或不同提案重用标识冲突。
- 取消先提交拒绝迟到结果；应用先提交保留已提交事实。
- 索引失败保留正文和回执；沿用既有机制重排，并用实际 Rust worker 验证当前版本检索。
- SQL 中途失败全部回滚。不能用两次普通编辑冒充原子合并。

后台 Agent 只返回建议。纯提案阶段核对已有正文未改变；合成确认实验通过明确确认标记触发，Agent 输出的批准字段不得授权应用。不迁移或清理用户真实数据库。

执行 sh scripts/go.sh test ./... 和受影响的检查。无 UI/打包变化时不重跑无关 P0/P1 窗口或分发验收。更新 docs/p3-validation.md，记录环境、契约、命令、场景、结果、证据和未通过项；只勾选有证据的实施项，检查重生成文档本地链接。

本轮不展开完整 UI、定期调度、排名、初始化批次、代码图谱或其他 Agent；不更换 Rust worker、本地模型或默认工具链；不运行生产 Cognee improve；不提交密钥、真实记忆、聊天或本机配置；不提交或推送。

补充图稿、原型点击、归档和文案定稿可暂留，但明确标记。最终说明两个验证真实通过了什么、剩余限制、下一阶段可进入的 P3 切片和关键证据。只有当前真实证据覆盖全部必需项时才能标记完成。
```

## 现有复查入口

```sh
sh scripts/go.sh build -o build/p3/runner-check ./cmd/p3-runner-check
python3 scripts/verify-p3-traex-isolation.py
python3 scripts/verify-p3-runner.py --agent traex --mode normal
python3 scripts/verify-p3-runner.py --agent traex --mode normal --confirm-synthetic-merge
python3 scripts/verify-p3-runner.py --agent traex --mode cancel
python3 scripts/verify-p3-integration.py --real-worker
python3 scripts/verify-p3-checks.py
```

合成确认只作用于探针创建的临时数据库；普通 normal 仅登记建议。下一阶段正式 P3-C 的接口、迁移与 UI 工作需要单独确定范围，此复查 prompt 不自动授权扩大首版。
