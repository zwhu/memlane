# 无项目 pi 连接协议验收

2026-11-10。对应 A05/A07/C03 的基础切片。电脑级多选安装、持久批次、其他 Agent 和 Native 整链路仍待实施，不把本记录作为这些场景全部通过。

## 设计与实现

依据 [电脑级连接契约](agent-connection-contract.md)、[A交接](a-setup-handoff.md)、[页面复用](reusable-setup.md)。连接身份、记忆授权、模型能力和当前启用配置分别管理。此前新增的 `connection_only` 请求提供空授权身份；本批增加真正不需要仓库、认证或模型的 pi 协议握手。

本机安装包 `@earendil-works/pi-coding-agent` 0.84.1 的 `docs/extensions.md`、`docs/rpc.md` 与 `dist/core/agent-session.js` 表明 Extension 命令先于模型处理。官方[Extension文档](https://pi.dev/docs/latest/extensions)、[RPC文档](https://pi.dev/docs/latest/rpc)仅作背景；最新在线文档不替代本机0.84.1源码和实际运行。

正式 Extension 在 `connection` 模式仅注册 `memory_scopes` 和专用验证命令；命令直接使用原有 bridge 调用 daemon，将真实协议回执作为 pi 自定义消息返回，不触发模型。桥接层和 daemon 同样只允许 scope 握手。运行于新的空工作目录及私有 HOME，关闭上下文、技能、其他 Extension、内置工具和会话保存；不链接认证，不传供应商或模型，子进程环境不继承模型凭据。

`Installation` 的 `protocol_generation/protocol_verified_at/protocol_error` 与旧读写验证分开。失败或取消清除协议成功，保存仍核对安装代次。处理配置候选可识别这份连接证明，依赖摘要使用独立 `protocol:` 标记；旧证明的摘要输入保持。正式模型 Schema 验证、保存与启用门禁没有合并。项目授权变化后不能沿用空授权协议证明。

Native 旧连接详情针对无授权 pi 使用「验证连接」及「尚未授权」状态，隐藏该路径的模型字段；不宣称完整多选页已完成。当前批量连接设计已通过本机 Edge 原生控制实际打开并看图，部分成功时同页保留成功项、逐项重试及固定底部操作。浏览器专用宿主不可用，但 Native 浏览器可用，不能继续把所有设计浏览都记为受阻。没有新增实际 App 截图。

## 运行证据

所有运行使用新合成根，没有项目材料、用户记忆或历史聊天，没有更改用户 Agent 配置。

| 运行 | 结果与边界 |
| --- | --- |
| `build/goal-protocol-20261110-001/protocol-1902108725/report.json` | 失败。直接 stdout 不在 pi 的 JSON 事件流中；仅 session 事件，不接受为成功。原报告保留。 |
| `goal-protocol-20261110-002/protocol-4171021885/report.json` | 改用正式自定义消息后真实 pi 握手通过，空授权、零项目／记忆、进程回收。daemon在测试进程内。 |
| `goal-protocol-20261110-003`、`004` | 新根补撤销后重新验证、清除旧成功；004增加结构化失败回执。003只观察到缺失回执，不能单独证明具体撤销错误。 |
| `goal-protocol-20261110-005/protocol-1977400544/report.json` | 使用完整包002中的实际 CLI／helper 通过正常与撤销路径。没有启动 Native App。 |
| `goal-protocol-20261110-006/protocol-3419000522/report.json` | 最终严格运行：同包实际 CLI／helper、真实 pi 0.84.1；成功空授权、撤销后精确 `disconnected` 回执、协议成功清除；项目与记忆数量均0，无模型／工具执行事件，两个 pi 进程组退出，helper正常停止。独立 `cleanup-audit.json` 核对 socket 与记录的PID。 |

运行根完整前缀为仓库 `build/`。002–006均为新根，没有覆盖001失败。`message_start/message_end` 是自定义协议消息，不是 assistant 模型响应；判定同时检查 `agent_start/turn_start` 与工具执行不存在。

## 检查与复现

- 全量 `sh scripts/go.sh test ./...` 通过。
- agents／agentbridge／daemon／processing／desktop 的 vet 与 race 通过。
- 协议解析测试拒绝缺失／null／非空授权、意外工具、失败协议；进程夹具覆盖空目录、工具白名单、独立回执、代次和真实活动取消后的进程组回收与成功清除。这些夹具不冒充真实 pi 取消。
- 私有IPC检查 connection 模式的其他工具在授权前即拒绝；Native状态测试区分旧读写证明、协议证明、未授权、停服及旧代次。

复现 opt-in 真实无模型运行时，指定新输出目录以及完整包中的绝对CLI／helper路径：

```sh
MEMLANE_REAL_PROTOCOL_BRIDGE=<App>/Contents/Resources/bin/memlane \
MEMLANE_REAL_PROTOCOL_HELPER=<App>/Contents/Resources/bin/memlaned \
MEMLANE_REAL_PROTOCOL_OUT=<新的绝对输出目录> \
sh scripts/go.sh test ./internal/agents -run '^TestProtocolRealPiWithoutModel$' -count=1 -v
```

完整包 `build/goal-protocol-package-20261110-002/darwin-arm64/Memlane.app` 与 DMG 已构建，helper／LaunchAgent检查通过，未公证。App文件总量42,999,247字节、DMG19,126,155字节，见同目录 `package-size.json`。未测本批首次准备下载量与最终安装占用，不引用其他批次代填。

## 下一步

继续实现持久批次拥有者和 Native 多选卡片：冻结目标与代次、防重复、逐项安装／验证、仅失败项重试、取消保留成功以及重开恢复。先复用已证实的 pi 用户目录 Extension；Codex／traex／IDE 的电脑级格式、动态工作区与官方采集需要独立核对，不能沿用项目配置路径冒充电脑级接入。App、helper与新连接同包的实际窗口闭环仍待补，A05/A07/C03保持待实现，整体Goal继续。
