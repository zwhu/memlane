# 正式图材料的处理配置网关 · 2026-11-11

本批承接主线9的真实实体图来源缺口。已有Cognee实例的启动模型配置不能冒充设置中当前启用的处理方式；新增任务网关为官方单文档流水线提供独立、冻结且可取消的纯模型调用。源码与协议检查不代表实际图抽取、付费模型或Native窗口已通过。

## 实现边界

- `Owner.StartCogneeGateway(ctx, snapshot)`只接受能够重新解析的正式ProcessingSnapshot；绑定固定保存修订与依赖摘要，不随设置切换改用另一个模型。每次推理前后重新核对同一快照，撤权或依赖漂移时不返回成功内容。
- 每任务仅监听随机本机127.0.0.1端口，32字节随机令牌仅在内存及受认证服务传输中使用；结构体默认JSON排除端点与令牌。没有公共代理、聊天/文件/记忆工具、路由透传或模型名称选择。
- 仅提供`POST /v1/chat/completions`，固定请求别名`memlane-processing`、完整json_schema和非流式文本消息；拒绝工具、未知参数、额外JSON及不受支持内容。请求2MiB、schema64KiB、prompt1MiB、最多64条消息，每任务最多128次请求、16个等待者，模型调用串行并复用daemon正式执行槽。
- 接口只接受既有适配的8192生成参数，不把该参数或字数换算成实际账单；返回模型来自冻结配置，实际Provider/Magpie报告另保存在HTTP回执，不编造usage。
- 当前复用真实pi纯completion与Provider/Magpie适配。pi仍核验完整原生Schema请求、HTTP200、模型身份和terminal/reaped/groupGone；HTTP使用实际请求、完整响应与Schema校验回执。Codex纯模型分派随后按独立app-server Schema ACK、ToolFree和本地校验/回收证明接入，不填造HTTP200；真实Codex模型验收不由电脑级连接证明替代。
- `Close(ctx)`取消在途请求并等待执行器收口；Owner关闭同时取消所有任务网关并等待。客户端断连只取消本次调用，任务整体取消由daemon正式所有者负责；Python流水线还须另取得取消回执，不能仅以HTTP断开宣称后台已停止。

`Receipts()`输出有界安全执行事实：请求序号、处理修订、请求模型、成功/失败以及实际HTTP或进程回收/Schema证明；不包含材料、模型正文、Schema、凭据、端点和上游原始错误。正式任务需在关闭网关后持久化这些证明，不以单份Python响应冒充完整推理证据。

## 公共集成与检查

Request已接`CogneeSubmission`和独立`CogneeSubmissionID`。daemon在store关闭前退休材料任务所有者，start/cancel要求明确确认，get只读原任务；未准备好执行者不返回伪成功。新图绑定通过独立MaterialSubmission标记分流授权，历史session同步与旧import门禁保留。

初轮7585在公共ID字段尚未落齐时编译失败，保留日志；字段补齐后10128的processing与daemon定向检查输出ok。协议检查使用真实SQLite账本、loopback HTTP和显式模型夹具，覆盖任务令牌、严格请求、Schema、依赖漂移、无虚构usage、关闭等待和执行证据不含材料/令牌。安全回执增加后的95256 race已exit0；随后Codex分支、处理候选与App的42967/86886/96773定向检查均exit0。没有调用真实模型、读取用户聊天或启动新Native包。

Python侧按官方单项Data和正式任务流水线提交，固定所有stage的任务网关配置，独立SDK适配层避免默认fallback与隐式重试；其官方SDK实际调用、隔离、来源与取消证据由对应材料提交记录单独登记。真实完整图、版本/出处往返、普通窄窗、启动/取消/重开与125场景整体目标保持未完成。
