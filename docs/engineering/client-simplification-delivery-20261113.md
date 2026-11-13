# Memlane 精简版开发交付

日期：2026-11-13。D01–D09按本轮明确范围收口通过。仅开发模式，不含release、完整App/DMG或helper安装。

## 日常操作

从 `/Users/bytedance/work/AI/memlane` 执行，启动终端保持运行：

```sh
python3 scripts/dev.py start --config /Users/bytedance/work/AI/memlane/.local/daily/runtime.json
```

在另一个终端查看状态、重开窗口或明确停止：

```sh
python3 scripts/dev.py status --config /Users/bytedance/work/AI/memlane/.local/daily/runtime.json
python3 scripts/dev.py open --config /Users/bytedance/work/AI/memlane/.local/daily/runtime.json
python3 scripts/dev.py stop --config /Users/bytedance/work/AI/memlane/.local/daily/runtime.json
```

关闭窗口和源码重载保留后台；`open`复用现有服务，`stop`或启动终端Ctrl+C才停止所属后台。电脑重启后手动启动。配置变更先stop、再修改、再start。详见[开发说明](../development.md)。

## 当前数据与所有权

- 唯一官方数据：`/private/var/folders/nv/g6m5_3nx1r9_v9dk3zh_5zw40000gn/T/memlane-task-gnwqmjhd/initialization/client-responsive/official/cognee`。尽管上层在临时目录，仍是本机既有日常数据，不可当缓存清理。
- 启动器锁绑定该规范数据位置，实际所有者由`status`及数据根`.memlane-dev-owner.json`核对，不以源码目录或GUI进程名判断。
- 官方API：`http://127.0.0.1:61255`；本机completion：`http://127.0.0.1:18173`；实际服务owner、桌面与官方插件身份一致。
- 主项目`memlane` dataset：`0785e3d2-881a-58c4-883e-cc3d2dcf76ef`；独立代码dataset：`86845625-0288-58a3-b556-744d5c556dc4`。
- 当前唯一首条日常链路是Codex CLI 0.167.1与官方Cognee插件1.8.0；不外推为全部客户端矩阵。模型`gpt-6.1-sol`，completion每请求隔离app-server，不加载日常插件、MCP、历史与代码工具。

## 开发前基线

本地注释tag：`pre-simplification-20261113-000958`，commit：`559eab851ec306d44ed2549e4ad575b035b829b6`。实际开始前已保存相关源码及文档；未推送，未创建额外快照分支／worktree／独立index。凭据、数据与构建资源未入Git。

## 流程证据

| 标准 | 当前结果与边界 |
| --- | --- |
| D01 | 同库单实例、明确停止／重启、普通关窗不断后台及open重开实测通过。UI清理EPERM传播与TIME_WAIT预检误拒绝已修正，9项启动器检查通过；两个独立缺陷各保留首次失败1轮。 |
| D02 | 正常Codex任务经官方插件进入官方session，1QA/15trace，实际doc f450aef3-a9be-4e10-994d-54c7bf47cd93。 |
| D03 | 官方operation 0e86e197-ab13-4762-bc3f-3c143472d072成功，官方保存ada020e3-7444-524f-a462-d034940c1e33及f55f8c96-7d1a-5a64-9b7e-25edc08ad9aa两条实际知识；不是只接收请求。 |
| D04 | 后续真实会话01a1d0a9-31d1-7402-9ebc-8961c382786a由官方预热2条、注入1234字符，并在开发文档任务中采用X-Api-Key／无Bearer回退规则；插件未返回逐条选中ID，不伪造。 |
| D05 | Native实窗阅读、Markdown／工具展开、搜索、片段→知识→会话逐层返回、独立滚动、代码横滚、复制、CmdK及刷新保留。分页0/30/31/60/61条与类型过滤为HTTP夹具覆盖，不是>30真实数据实窗验收。 |
| D06 | 旧内容库、原文索引、outbox、草稿、自有MCP/hooks/扩展及旧daemon已退出，官方数据与必要依赖保留。 |
| D07 | 当前有效Codex插件与桌面使用同一官方身份，无自有项目／Agent权限模块；项目dataset只用于归属分组。 |
| D08 | 官方实体图321节点实窗；第三轮手动代码更新scope通过，全图2316节点／5597边／276来源文件无生成目录污染，扫除112旧节点。1000节点子图可展开／选中并显示官方来源字段；不含本地编辑器跳转。前两轮scope失败保持。 |
| D09 | 开发前tag可恢复；旧源码及问题路径按职责删除，清理记录与历史失败保持。最终源码提交见本地Git历史；未推送。 |

## 删除、保留与限制

已退役旧Go 33目录828路径，以及旧Python/Rust业务链、旧脚本／夹具、SQLite/MCP依赖；实际逐项依据见[源码清理](client-simplification-source-cleanup-20261113.md)与[运行清理](client-simplification-cleanup-20261113.md)。不是按日期或递归清空build。

R/V旧Memlane SQLite及wal/shm、原文index、agent-outbox、草稿、旧权限/处理状态、自有库备份及旧适配器已退出，不迁移为官方归档数据。保留原官方全部数据及切换前一次一致快照、原Agent聊天／账号／第三方配置、BGE embedding、同Python环境、固定官方源码与归档、原生Codex、Enola及仍需保护的旧完整包资源。两份R适配器空目录无实现或引用，未扩大删除。

通用知识保留入口并明确“官方暂不支持”独立阅读；按用户确认，后续可用独立dataset供所有Agent读取，本轮未创建、不自动提升项目知识。

Enola固定版`manifests`扫描器绕过ignore，会带入build历史依赖清单。本轮通过官方配置停用该扫描器，保留其余19个默认解析器及121条忽略规则；不提供其派生的跨生态依赖清单事实，语言源码符号、调用和导入继续保留。不修改官方SDK或自写过滤图。

所有本轮具体失败、尝试与证据见[统一执行记录](client-simplification-execution-20261113.md)。D03三种错误分别计数，不能合并整个D03凑阈值；T5同一范围问题前两轮失败、第三轮通过，历史累计2次不重置。错误记录原先未纳入交付提交；用户随后明确授权结构化整理并commit/push，现见[分类错误索引](client-simplification-errors-20261113.md)，原文与脱敏证据另行保留。

## 最终运行与检查

最终launcher PID69475（tool session36423）、completion69477、official69478、MyGo watcher69634。记录时两端health200，Native同库3项知识、已验ada020e3正文及原codex会话出处可读，窗口保持运行。PID是交付时快照，后续以status为准；可用空间约115GiB。

必要检查通过：当前活动Go `sh scripts/go.sh test ./...`及vet；Python completion 13项协议／HTTP／合成子进程检查；启动器9项、磁盘保护9项；图宿主TypeScript检查／构建；Native实际连续操作及全量官方图scope检查。没有完整包、窄窗、全Agent或旧全仓矩阵。最后索引发生于18:00UTC，后续启动器修正不自动重扫，源码变更仍由用户手动更新图谱。

当前范围无未完成阻塞。通用知识独立阅读与manifest派生依赖事实的上述能力边界保留，未虚报为实现。已清理13项临时任务重复JSONL、prompt及一次性脚本；原官方数据、失败证据及必要运行依赖保留。
