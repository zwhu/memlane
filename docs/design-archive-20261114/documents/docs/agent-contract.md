# Agent 共享记忆接入契约 v1

P2 使用主客户端随附的 `memlane` Go 桥。MCP stdio 与 CLI 均连接唯一 `memlaned`，复用业务 SQLite、不可变版本、持久索引任务和当前版本过滤；桥不自动启动已被用户停止的服务，不打开记忆库或启动 Cognee。

## 连接与工作区

连接由用户在 App 明确创建/修改，持久记录 `id`、`agent`、`project`、`allowed_scopes`、`generation`、`enabled`。批准范围必须来自已登记项目或全局偏好/经验。变更范围或断开使旧代次失效，旧 Agent 必须重载。找到可执行文件和 MCP 握手不代表记忆工具可用。

桥启动参数 `--root` 是服务数据目录；`--connection`、`--generation` 是 App 安装的连接身份。`--workspace` 只能来自明确配置或 pi Extension 的 `ctx.cwd`，不作为模型工具参数公开，不读取桥进程 cwd。连接也可明确绑定项目。二者同时存在时必须映射到同一仓库。无法确定项目时返回 `scope_required`；Agent 调用 `memory_scopes` 列出已批准范围，让用户选择。

项目以本地 Git common directory 识别，worktree、仓库子目录和符号链接归属同一项目；同 remote 的独立 clone 不自动合并。未登记仓库或多个历史项目匹配时要求用户选择，不能默认为全部项目。服务端核对批准范围，不能靠 Skill 或 Agent 自报名字授权。

明确项目绑定优先于历史映射歧义，但仍核对工作区属于同一个本地仓库。没有固定绑定时，即使工作区未登记或不是 Git 仓库，用户也可显式选择已批准范围；默认查询仍拒绝。授权修改/断开与正在执行的记忆工具形成屏障：先完成已授权调用，再使旧代次失效；配置操作完成后不允许旧调用继续写入。

默认搜索只选当前项目和已批准的全局范围。额外项目即使已获批准，也必须使用 `scope` 或 `scopes` 显式选择。保存默认当前项目，全局保存显式选择对应 scope。当前项目明确约定优先于全局建议，但结果保留各自来源与适用范围。

## 工具

| 工具 | 参数 | 成功结果 |
| --- | --- | --- |
| `memory_scopes` | 无 | 本连接批准的范围名称、UUID 与绑定状态，不读取知识 |
| `memory_save` | 必填 `operation_id`、`text`；可选 `scope`、`title`、`origin`（knowledge/raw）、`source` | `persisted=true`、`saved_revision` 和当前 `record` |
| `memory_search` | 必填 `query`；可选 `scope` 或 `scopes`、`limit`（1–100，默认 10） | 授权范围内当前版本的 `hits`、实际 `scopes` 与项目优先规则 |
| `memory_get` | 必填 `memory_id`；可选 `scope` | 当前正文、版本、原始正文、来源、范围与索引状态 |
| `memory_status` | 同 get | 当前版本和独立索引状态 |

`source` 只包含实际提交的 `fragment` 与 `references`；客户端不抓取 Agent 聊天历史。记录来源的 Agent 名称由连接决定，原始片段与引用在业务库保存，App 编辑后仍保留。Agent 只能创建新知识或原始记录，本阶段不暴露覆盖、删除、恢复、跨项目移动等直接工具；这些修改需用户确认，P3 再接提案。

## 写入与版本

`operation_id` 必须在生成保存请求时固定，响应丢失/失败重试复用同 ID 和内容。服务按连接隔离幂等键，由连接＋操作 ID 生成稳定记忆 ID。同键不同正文、范围、来源或其他保存字段返回 `conflict`。业务事务提交后才返回 `persisted=true`，桥在超时、断线或服务停止时不报告成功。

`saved_revision` 是该次操作提交的版本；`record.revision/text` 始终是当前正文。用户编辑之后重放原始保存，回执仍指向原提交，返回正文使用最新版本，不把旧正文当成当前知识。索引状态为 `pending/indexing/indexed/failed`；`indexed` 只表示该版本索引完成，服务/引擎可用性另查。失败不丢已保存正文。检索沿用现有 revision、generation、chunk binding 验证，不返回旧版本补足数量。

## 封装与错误

返回 `{protocol_version:1, ok, result}` 或 `{protocol_version:1, ok:false, error:{code,message,retryable}}`。MCP 工具错误同时设置 `isError=true`；CLI 输出相同 JSON，失败退出非零。stdout 仅协议，stderr 用于启动诊断，不记录正文或凭据。

| code | 含义 |
| --- | --- |
| `service_unavailable` / `timeout` / `cancelled` | 服务不可达、超时或取消；保存使用原操作 ID 重试 |
| `index_unavailable` | 引擎不可用，不能当成零命中 |
| `connection_not_found` / `disconnected` / `reload_required` | 尚未连接、已断开或代次已变更 |
| `scope_required` / `scope_ambiguous` / `scope_denied` / `workspace_mismatch` / `workspace_unavailable` | 需要选择、存在歧义、越权或工作区无效 |
| `conflict` / `not_found` / `invalid_request` / `protocol_mismatch` | 幂等冲突、条目不存在、参数或协议不合法 |
| `permission_denied` / `service_error` | 本机权限或服务内部错误 |

CLI 示例仅描述参数，不代表真实 Agent 已接通：

```sh
memlane mcp --root /absolute/service/data --connection <UUID> --generation 1 --workspace /absolute/repo
memlane call --root /absolute/service/data --connection <UUID> --generation 1 --tool memory_save
# stdin: {"operation_id":"stable-example-1","text":"合成经验"}
```

MCP 固定官方 Go SDK `v1.8.0`，支持与 Agent 协商协议版本。本地业务协议仍为 v1，数据库迁移为 schema v2。该边界用于减少正常 Agent 的范围混淆，不声称能隔离同一 macOS 账户下具备任意执行权限的恶意程序。
