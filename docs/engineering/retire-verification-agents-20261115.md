# 旧验收身份退役

2026-11-15用户明确“上面重复的赶紧清理掉”。目标仅旧Codex `codex-f9a8c7fa219c8b9577043271` 与旧TraeX `traex-a8d33590475315b81f419bb6`；真实日常身份保持，已保存内容、原会话绑定、buffer和私有凭据不删除、不重放。

管理接口 `/v1/agents/{id}/retire`：管理认证、revision核对、阻止退役当前Connections目标；先落退役进行中回执并拒绝旧控制令牌/resolve/policy，撤销官方直接权限并核对有效权限为空，再从活动agents移入archived_agents。保留原安装与逐项目覆盖／生效回执。失败停在unconfirmed并保留材料，仅显式恢复；不自动重建已退役主体。

影响链：退役 → 控制认证/策略拒绝 → 官方ACL收回（含历史未关联通用组）→ 归档活动身份 → 权限页仅显示剩余身份。旧bindings仍保留，但不再出现在活跃Agent队列。退役不代表删除官方Agent账号或历史数据。

验收：管理身份限定；当前日常目标不可退役；撤权失败不归档且拒绝旧控制接入；成功保留项目／datasets／sessions，不改其他主体权限；重复调用完成结果不重复变更；旧ID不得通过恢复接口自动重建。已通过26项项目控制测试和48项相关检查，包含历史未关联dataset撤权与旧令牌失效。

运行前：官方datasets状态全部terminal、completion active_or_waiting=0、原backlog completed；私有registry/runtime备份在 `.local/retire-legacy-agents-20261115/`。按正常dev stop确认释放后start，同一official_data不变。

实际结果：dev owner38905正常ready；管理接口依次完成两条退役，各3.26秒，revision81→82→83。两条旧身份的官方直接read/write/delete/share均为0，原控制令牌均返回401；逐组sync_acl已核对有效权限为空。历史未关联通用组6619cf7a-471c-5d3b-8b77-38ad8f9fb7e4一并撤权，未删组。

原projects与全部sessions绑定与私有备份相同，真实日常Codex／TraeX Agent配置逐项相同；官方数据组11、会话34保持。Native实际权限列表已见四行：Codex、OpenCode、pi、TraeX，Codex／TraeX各一条，无重复；dcim待保存记录显示0条。本轮没有补采或重放天气会话，天气恢复由另一已授权会话完成并单独留证。

## 2026-11-16 日常pi接入后的同名验收身份

用户再次截图出现两个“本机pi”。只读确认旧pi-e18044f334a06fa478ddbd27属于.local/multi-agent-ollama-20261114/native-setup验收环境；新pi-033f3aca88231dfdb2fd47fb属于真实日常~/.pi/agent，已由接入线程完成宿主回执。两者确实是不同主体，不是渲染重复。原清理仅退役旧Codex/TraeX，未覆盖随后被日常pi替代的旧验收主体。

与正在执行真实整理的所有者核对，旧pi没有在途依赖；原Codex任务继续。通过原管理retire接口退役旧pi，revision93→94，retirement=complete，active agents中pi仅余新日常身份。旧主体归档及所有sessions绑定保持；私有前置备份在.local/retire-legacy-agents-20261116/，不提交凭据或运行库。不停止服务、不删除原始记录、不改日常pi模型与连接配置。此轮完成管理数据核对，未另行操作Native窗口；其目录轮询应采用新revision。
