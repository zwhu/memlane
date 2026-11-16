# Cognee 显式记忆与自动提炼调研

2026-11-16。仅查官方文档、官方源码、已固定的本地源码及插件，不调用写入API、不启动模型、不重放用户会话。研究范围是“用户明确要求记住一句话，怎样保证写入并在Memlane记忆中显示”。

## 结论

Cognee 已有显式永久写入能力；Memlane 也已有适配，不需要另建存储或先实现新接口。当前缺口是：Agent是否真正调用、回执是否如实传回，以及显式写入文档能否进入Memlane“记忆”分类。自然语言回复“已保存”不能代替操作回执。

## 1. 两条路径语义不同

| 路径 | 实际行为 | 是否保证生成提炼记忆 |
| --- | --- | --- |
| `remember(text, dataset_id=..., session_id=None)` | 永久模式，执行官方`add → cognify`；可选后续improve | 直接存入原始内容并构建检索产物，不依赖coding候选门槛，但不是自动生成`session_learnings` |
| `remember(..., session_id=...)` | 先写会话缓存；可选后台improve桥接 | 会话写成功不等于永久图完成 |
| `remember/entry`的QA/trace | 会话材料写入，返回entry身份 | 不等于生成经验条目 |
| `improve(session_ids=...)` | 桥接会话、提取并筛选候选、后续整理 | `no_gated_entries`可正常返回0条 |

官方[Remember文档](https://docs.cognee.ai/core-concepts/main-operations/remember)明确区分有无session_id；[Improve文档](https://docs.cognee.ai/core-concepts/main-operations/improve)说明session_learnings来自有门槛的会话提炼。本地固定服务`ec6f40859e5f2df9e7862dde27d580ac3268ed43`的`cognee/api/v1/remember/remember.py:2173–2231`是会话分支，`:2243–2312`是永久分支；`cognee/modules/session_distillation/distill.py:246–265,1218–1220`证实无候选时返回no_gated_entries。网上main会变化，实施以本地固定版本为准。

永久模式可显式传`self_improvement=false`，跳过后续improve，仍执行add+cognify；因此无需强行制造coding候选来保存用户指定内容。原文与图中的实体/摘要是不同产物，不能把图生成结果当原文逐字副本。官方保留Data及raw_data_location，可用`GET /api/v1/datasets/{dataset_id}/data/{data_id}/raw`核对原文。需要严格原样记住时，使用UTF-8文本文件上传可避免raw_data中的URL/路径被当作抓取目标；原始字节仍应读回核对。源码：`remember/routers/get_remember_router.py:120–141,615–623`，`datasets/routers/get_datasets_router.py:1164–1240`；[官方源码](https://github.com/topoteretes/cognee/blob/ec6f40859e5f2df9e7862dde27d580ac3268ed43/cognee/api/v1/remember/remember.py)。

## 2. 官方插件和Memlane已有什么

官方插件1.8.0的`skills/memory/SKILL.md`已指导调用`cognee-remember.sh`显式保存，默认`--node-set user_context`，项目资料可用project_docs。插件`_remember_http.py:254–268`把内容作为文本上传，字段包括node_set、datasetId、run_in_background，**没有session_id**，因此走永久模式。不同node_set主要是分类/检索标签，不能把user_context理解成“只在会话缓存中”。[官方仓库](https://github.com/topoteretes/cognee)与[Remember文档](https://docs.cognee.ai/core-concepts/main-operations/remember)可交叉核对；本地插件位于`/Users/bytedance/.codex/plugins/cache/memlane-16e71bd70ad5-f36cad920703147fb9d4/cognee/1.8.0/`。

Memlane已有`engine/python/agent_memory_adapter.py:222–236`的remember操作：核对当前项目write权限，调用同一`do_remember(..., node_set='memory', dataset_id=dataset)`。但它仅把`ok=true`归并为submitted，没有把DataID、pipelineID、queryable等全部保留。`engine/python/project_tool_patch.py:8–24`继续核对dataset必须等于当前绑定、具有write权限，显式remember并未被一概禁用。

TraeX安装复用官方全部文件（含skills和scripts），再替换宿主hooks；不能断言TraeX“没有remember技能”。`engine/python/project_installation.py:75–103`。TraeX hook仅做自动召回、问答/工具采集和flush，不解析“把这句话记住”并自动调用显式remember：`engine/python/traex_hook.py:25–73`。因此需要核查Agent有没有实际执行继承的记忆技能；当前案例的文字回答本身不能证明它执行了写入。Agent为什么未调用，现有证据不足。

通用记忆已采用官方永久remember：`internal/simpledesktop/common_knowledge.go:107–119`上传raw_data、datasetId、memlane_common、同步执行、self_improvement=false，并有已知结果/未知结果处理。可复用该写入与回执处理能力，不能把项目显式内容直接冒充通用记忆。

## 3. 回执需要分层判断

官方RememberResult有status、dataset_id、pipeline_run_id、items（可含DataID）、content_hash等字段；同步完成后才解析实际管线及Data身份。固定服务后台提交可能尚未得到pipelineID，不能保证初始响应总有data_id/pipeline_run_id。源码`remember.py:865–1005,2317以后`。

插件`_remember_http.py:306–344`的ok只表明HTTP接受，随后可限时轮询dataset状态得到queryable/wait_outcome。该轮询是dataset级，遇到并发写入不能当作本条写入的唯一证明；插件回包也只透传datasetID、pipelineID、status，未完整透传items/DataID。Memlane现通用写入特意用同步结果核对，见`common_knowledge.go:109–112,159–175`。

建议区分“已提交”“原文已保存”“索引可检索”，只有拿到匹配本次写入的DataID并读回一致才能明确原文已保存；超时/未知不自动重投，防止重复。

## 4. 仅接remember还不够：当前UI会归到原始文档

`internal/simpledesktop/client.go:151–160`只把session_learnings或带common_knowledge/category元数据的文档识别为记忆。`knowledge_waterfall.go:65–68`据此筛卡片，`client.go:313–315`的记忆搜索也限定session_learnings/memlane_common。因此官方插件user_context/project_docs、现适配器memory的永久写入，按当前逻辑仍是独立原始文档，不会自然进入项目记忆卡片。

不要伪造session_learnings、lesson_type、模型接受理由来解决显示。最小改动方向是复用现有remember写入，把“用户明确保存”作为可区分的来源/metadata，并让记忆列表、搜索、统计、未读和来源识别该类真实官方Data；数据继续只存在Cognee，客户端不另建正文库。具体标签及权限契约应先定清，不能把项目内容自动提升为通用记忆。

## 未做与待验证

本轮未新写任何真实内容、未跑模型；未实际验收“TraeX执行技能→write许可→原文读回→记忆卡片→Agent召回”。后续验收需保留明确项目、来源、操作回执及超时未知分支；自动采集/提炼与显式写入分别验收，不能以其中一个通过代替另一个。
