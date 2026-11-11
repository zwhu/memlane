# Cognee 原始材料与检索边界

2026-11-09。本文核对 Cognee 官方文档和 Memlane 当前打包的 Cognee 1.6.7 源码，回答原始文档、会话原文和提炼知识分别在什么阶段可以被检索。这里只记录上游行为和对 Memlane 契约的影响，不表示已经修改 Memlane 的检索策略。

## 结论

Cognee 支持检索原始材料，但不是 `add` 后立刻可检索：

1. `add` 只把原始文本、文件或 URL 持久化到 dataset，供后续处理；此时没有 chunks、embedding 或知识图谱。
2. `cognify` 先切分并索引原文 chunks。这个索引阶段完成后，`CHUNKS`、`CHUNKS_LEXICAL`、`RAG_COMPLETION` 和 `HYBRID_COMPLETION` 已经能够使用原文，即使后续实体与关系提取仍在执行。
3. `CHUNKS` 直接返回匹配的原文片段；`RAG_COMPLETION` 基于片段生成回答；`HYBRID_COMPLETION` 同时使用文档片段和实体邻域；图谱检索则使用提取后的实体与关系。
4. `improve(session_ids=...)` 不是原文可检索的前置条件。它会先把会话问答和 Agent trace 持久化、cognify 到永久图谱，再把通过门禁的有效指导提炼为独立 `session_learnings` 文档。原始会话材料和提炼知识同时保留。
5. Cognee 1.6.7 将持久化会话问答放入 `user_sessions_from_cache` node set，将提炼知识放入 `session_learnings` node set。搜索 API 支持按 `NodeSet + node_name` 限定范围；不传过滤条件时，默认混合检索可能同时使用原文 chunks 和图谱知识。

因此，“原始记录可检索”和“原始记录已经成为知识”是两件事。原始记录完成 chunk 索引后可以被查到，但整理仍会新建独立知识，不会把原始记录改名或覆盖。

## 会话缓存何时成为长期内容

Cognee 的 session cache 保存当前会话的提问、回答、工具轨迹和上下文，是短期会话状态，不是 Memlane 项目中的第三种内容类型。它不会整体改名为“长期记忆”，而是由 `improve(session_ids=...)` 分阶段产生两类长期内容：

1. `persist_session_qa` 与 `persist_agent_traces` 将会话问答和 Agent trace cognify 到永久 dataset。成功后原始材料进入 `user_sessions_from_cache` 等 node set，成为可长期保存和检索的来源材料。
2. `distill_sessions` 只处理通过门禁的有效指导，由 curator 和 writer/rejecter 决定是否生成独立 `session_learnings` 文档。没有合格指导、重复或被拒绝时，可以不生成任何学习知识。

Memlane 当前固定的 Codex 官方插件路径在累计到自动计数阈值、`SessionEnd` 或手动同步时提交 improve；当前固定版本的默认阈值是 150 个计数事件，不等于 150 轮对话。提交成功只表示后台任务已受理；原始材料持久化和学习知识完成需要分别读取真实结果。具体时序见 [Codex、Cognee 与 Memlane 的动态记忆流程](cognee-codex-flow/README.md)。

用户或 Agent 直接把明确结论提交为 `knowledge` 是另一条路径：取得持久化成功回执后可以直接成为长期知识，不要求先制造一份会话原始记录，也不等待 improve。

## 对 Memlane 的含义

Cognee 没有要求 Memlane 必须把原始记录排除在所有搜索之外，也没有要求所有 Agent 搜索都混合返回原文。以下是待确认的产品策略候选，不是已经实现或认可的行为：

- 默认复用：只返回 `knowledge`，用于稳定的项目约定和经验。
- 原文查找：显式搜索 `raw`，用于日志、工具输出、会话片段和来源核对。
- 混合检索：仅在调用者明确需要证据或排障材料时同时搜索两类，并在每个结果中保留 `origin`、来源和版本。

当前 Memlane 的列表默认排除 `raw`，但 `memory_search` 的索引与搜索路径没有按 `origin` 过滤。该行为与 Cognee 支持原文检索并不冲突，但产品契约尚未明确“默认知识／显式原文／明确混合”三种范围，因此不能仅凭当前实现认定所有原始记录都应进入 Agent 的默认上下文。

检索策略与列表信息架构是两个维度。即使原始记录可检索，项目页面仍可保持「知识／原始记录」分开展示；不需要为了底层支持混合检索而增加一个混合内容时间线。

## 一手来源

- Cognee 官方 [add 文档](https://docs.cognee.ai/python-api/add)：`add` 摄入并暂存原始数据，后续调用 `cognify`。
- Cognee 官方 [cognify 文档](https://docs.cognee.ai/python-api/cognify)：构建 chunks、embedding、summary、nodes 和 edges，并为搜索准备数据。
- Cognee 官方 [search 文档](https://docs.cognee.ai/python-api/search)：`CHUNKS` 返回原始文本片段，`RAG_COMPLETION` 使用文档 chunks，`HYBRID_COMPLETION` 使用文档片段与实体邻域。
- Cognee 官方 [Improve 文档](https://docs.cognee.ai/core-concepts/main-operations/improve)：把选定 session 桥接到永久图谱，并将有效指导提炼为 `session_learnings`。
- Cognee 官方 [Sessions and Caching](https://docs.cognee.ai/core-concepts/sessions-and-caching)：session 是短期缓存，和永久图谱分开。
- Memlane 打包的 Cognee 1.6.7：`build/cognee-service-component/memlane-cognee-service/_internal/cognee/api/v1/add/add.py:42`、`api/v1/cognify/cognify.py:137`、`api/v1/search/search.py:42`、`api/v1/improve/improve.py:141`。
- Cognee 1.6.7 node set 与检索过滤：`modules/improve/constants.py:13`、`modules/improve/stages.py:159`、`modules/improve/stages.py:355`、`modules/search/methods/node_name_scope.py:1`。
- Memlane 当前分层与搜索路径：`docs/ui-redesign/content-model.md:7`、`internal/store/library.go:174`、`internal/memory/service.go:58`、`internal/daemon/agents.go:277`。
