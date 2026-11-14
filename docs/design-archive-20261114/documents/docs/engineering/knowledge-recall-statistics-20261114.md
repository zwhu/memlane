# 知识召回次数：方案与实施记录

2026-11-14。用户要求单条知识展示召回次数，允许从 0 开始；随后授权独立完成，要求沉淀存储与表结构方案。属于新的增量需求，不恢复历史全量 Goal，不进入 release。

## 当前结论与边界

源码已实现 SQLite 统计、认证接口、官方插件补丁记录及项目／通用知识详情展示。隔离协议与持久化测试通过。当前日常服务及已安装插件仍为旧版本，尚未开始日常累计；没有重启服务或改写活动 Agent 配置。Native 窗口验收因 CUA `cgWindowNotFound` 未完成，不把源码／夹具当成实际窗口通过。

Cognee 仍是知识正文与知识 ID 的唯一来源。本次只补其缺少的客户端使用统计，不修改 Cognee 数据库或另建知识索引。

## 口径

- “召回 N 次”表示新补丁实际记录的 Agent 上下文交付次数，不表示模型确实采用了这条知识，也不是浏览次数。
- 一次自动上下文构建或一次显式召回为一个事件，同事件同 dataset/document 去重；一次命中多个知识，每条各加 1。
- 自动上下文只统计通过最终权限检查、完整出现在最终返回文本中的知识。候选、预热、被预算截断的内容、空结果、失败结果不计。
- 人工搜索弹框、人工打开详情、复制不写统计。知识与原始会话分开，原始记录不显示知识召回计数。
- 已有历史不补算；没有事件的知识为 0。提示中展示统计起点；接口不可用时按用户授权显示默认 0，同时提示“暂不可用、默认值，不代表历史从未召回”。不伪造最近召回时间。
- 显式召回只接受结果中明确的官方 `dataset_id` / `document_id`，绝不根据相似正文或图节点 ID 猜测归属。当前官方图回答常常没有 document ID，此类结果不计；这仍是覆盖缺口。
- 回执是尽力记录，0.5 秒超时不阻塞正常召回，不落正文队列、不自动重放；失败向 stderr 输出固定提示。因此统计为“已记录次数”，不能解释为所有调用的完整审计账本。

## 数据位置与表结构

文件为 `project_control.registry_file` 同目录的 `knowledge-recall.sqlite3`。当前日常配置对应 `.local/daily/knowledge-recall.sqlite3`，新服务首次加载时创建；本次未在日常库写入模拟次数。数据库权限 0600，沿用私有目录；SQLite 默认事务日志，未开启 WAL。不存查询词、正文、会话内容、凭据或知识副本。

| 表 | 字段 | 约束／用途 |
| --- | --- | --- |
| recall_meta | singleton, schema_version, started_at | 单行，主键恒为 1，版本 1；统计起点为服务首次创建统计库的 UTC 时间 |
| recall_event | agent_id, event_id, channel, received_at, payload_hash | 主键 `(agent_id,event_id)`；channel 为 automatic/explicit；服务端 UTC 接收时间；摘要防止同 ID 改写命中集合 |
| recall_hit | agent_id, event_id, dataset_id, document_id | 四字段联合主键；前两字段外键指向 event；同次多片段不会多计 |

`recall_hit(dataset_id,document_id)` 有索引。次数用命中表 `COUNT(*)`，最近时间通过关联事件 `MAX(received_at)` 查询，不再维护一份可能漂移的累计值。一次事件及全部命中在同一事务提交。

事件 ID 为 UUID。相同 Agent／event、相同规范化命中集合重传返回 `recorded:false`；同 ID 不同集合或通道返回 409，不能追加命中绕开幂等。不同 Agent 作用域独立。dataset/document 也必须为合法 UUID。单事件 1–100 条；接口沿用 16 KiB 请求上限。

统计库随对应本地 registry 保留，当前不做自动清零或保留期裁剪。知识删除后其计数可继续作为无正文关联记录保留，新知识使用新官方 ID，不继承旧次数。未来删除统计／导出作为独立需求处理；不要手工改库补算历史。

## 完整链路与影响范围

1. 官方 `_coding_memory.py` 选择知识后暂存 `(dataset_id, data_id, rendered_line)`；只存在当前调用内存中。
2. `_memlane_policy.merge_block` 完成跨项目参考合并、最终权限复核与预算截断，选出完整进入返回块的知识，再发送回执。
3. 官方 `_recall_http.py` 的成功响应只提取明确文档引用，向同一回执接口发送。错误响应不记录。
4. `POST /v1/knowledge/recalls` 只允许已认证 Agent，身份取 token 对应主体，不接受 body 传入 agent_id。只允许该 Agent 最近核实为可读的受管数据集，不因统计重新同步 ACL 或改变策略状态。管理凭据不能冒充 Agent 写入。
5. `GET /v1/knowledge/recalls?dataset_id=…&document_id=…` 只允许管理端，限制已登记数据集。返回 count、last_recalled_at、started_at。
6. App 打开项目／通用知识详情时，正文成功后查询计数，最多等 2 秒；原有请求代次、身份检查及取消继续生效。统计失败不影响正文读取。在日期后显示“召回 N 次”，详情重新打开时刷新。

实现入口：`engine/python/knowledge_recall.py`、`project_control_server.py`、`project_plugin_policy.py`、`engine/patches/project_access.py`、`internal/simpledesktop/knowledge_recall.go`、`app.go` 及两种知识详情 UI。

受管 Codex 官方补丁以及共享同一 Python 召回链的 pi／TraeX、Claude Code／Cursor 可使用自动交付回执；OpenCode 独立 TS 路径及未升级的插件未接入。不将一条路径测试通过当成所有 Agent 已启用。

## 插件版本与日常启用

新补丁锁更新到 `engine/managed-project-plugin.json`。旧锁保留为 `managed-project-plugin-pre-recall.json`；dev 校验和安装准备只兼容这一份明确旧锁，并逐文件校验原始摘要，不接受任意旧产物。新构建仍校验当前补丁源码与新锁。已安装的旧不可变路径继续保留，不替换正在使用的插件。

日常启用剩余步骤：由当前服务所有者在共享任务空闲时，按 `scripts/dev.py` 的已有启停入口加载新 control 源码；使用 `scripts/build_project_plugin.py` 从固定 upstream 构建新的不可变产物，经现有连接升级流程切换后续会话。不能覆盖活动插件、清空 buffer、重放旧会话或为了统计执行模型／索引。

验收需在新 Agent 会话实际召回已知知识后，确认回执、官方 document ID、统计接口和 Native 同条知识次数一致；再做一次人工搜索确认不增。未取得这组证据前保持“源码已实现、日常待启用”。

## 已有验证与问题计数

- `test_knowledge_recall.py`：6 项通过。覆盖零值、重复片段、相同请求重传、冲突拒绝、不同 Agent、重启保持、非法请求全事务回滚、默认 0600、最终交付过滤、无官方引用不猜测、失败不影响召回、Agent／管理认证及实际 hook HTTP 回执到查询。
- 固定官方插件补丁测试 23 项通过；管理服务回归 21 项通过；Claude Code／Cursor 官方宿主安装与补丁回归 7 项通过。均为隔离目录／协议，不代表日常 Agent 已升级或真实模型闭环完成。
- Go 召回统计与项目／通用知识详情定向检查通过，包含 0／7 次、dataset/document 与管理凭据正确、原始记录不展示。
- 曾运行 Go simpledesktop 全套，另一个正在开发的 backlog 测试 `TestBacklogUIStartsOneOwnedJobThenOnlyPollsAndShowsIsolatedCounts` 在 `reading update deadline` 失败；保留此结果，未归为召回统计缺陷，未修改其他任务的 backlog 实现。
- Native 工具两次无法绑定开发窗口（`cgWindowNotFound`），属于实际窗口验收前置受阻，没有产生“视觉通过”结论。
- 日常服务保留原 PID／端点，状态入口确认 ready。兼容旧插件后 `scripts/dev.py status --config .local/daily/runtime.json` 正常。临时数据库、隔离插件由测试 TemporaryDirectory 清理；未创建新的日常内容或模型任务。
