# Codex 桥接队列：官方调用链与最小适配调研

2026-11-15。独立只读调研；未调用模型、写入记忆库、重启服务或修改生产源码。本记录辅助当前 `codex_busy` 诊断，不把源码路径推断当作历史请求的完整追踪。

## 结论

当前固定 Cognee 的文本块批次默认最多 2000，每个批次直接并发提取全部块；单个 pipeline 同时可处理 20 个 Data 文档。桥接只执行 2 个请求、连执行及等待总共接纳 32 个，因此一条整理任务也有能力将接纳队列占满。当前日志失败走 `from_extraction`，并非图谱与额外摘要两路同时调用的模式。不能拿 3 个 improve operation 的时间重叠证明实际有 3 个模型任务并发，也不能证明当时 32 个名额的完整归属。

官方已有 RPM 限流应复用，但它不是全局并发上限：默认 60/60 的桶可首批放行 60；Codex 桥接走 OpenAI 兼容协议，不会得到 Ollama/llama.cpp 的本地 10 RPM 默认值。因此单独开启默认 limiter 或单独限制 improve 数量，不足以修复桥接超载。

推荐最小方案为：保留官方引擎，明确 `SUMMARY_METHOD=from_extraction`、缩小 `CHUNKS_PER_BATCH=2`；在现有 SDK HTTP 策略层加入仅针对当前 Codex bridge origin 的进程共享 2 并发闸门，实际请求完成或取消后释放。闸门覆盖各整理阶段、不同项目及 SDK 重试，不在客户端另建内容队列。桥接原 32 准入保留为异常保护而非正常调度。失败后的兄弟协程收尾问题应独立验证，并通过官方结构化并发改进解决；限流不能冒充任务收尾。

## 固定源码依据

以下 Cognee 路径均相对于 `.cache/coding-memory-components/service-ec6f40859e5f2df9e7862dde27d580ac3268ed43/`，版本为 `ec6f40859e5f2df9e7862dde27d580ac3268ed43`。线上只读日志审计见 [今日零知识记录](today-knowledge-zero-20261115.md)。

| 层次 | 已确认的行为 | 源码 |
| --- | --- | --- |
| improve | `persist_session_qa` 是 fatal；先等待 dataset/session lock，再进入阶段；开始时间可含等待 | `cognee/api/v1/improve/improve.py:169–185,335–363`；`cognee/modules/improve/stages.py:159–188` |
| QA 持久化 | 按 session window 串行 `add`、`cognify`；持久化水位仅在成功后推进 | `cognee/tasks/memify/cognify_session.py:88–142` |
| 默认块批次 | 显式参数优先，其次 `CHUNKS_PER_BATCH`，否则 2000 | `cognee/modules/cognify/chunking_tasks.py:15–24` |
| 默认摘要 | `from_extraction`，从图谱结果构建摘要而不额外调用模型 | `cognee/modules/cognify/config.py:18–23`；`cognee/tasks/graph/extract_graph_and_summarize.py:26–44` |
| 单批提取 | 每个文本块创建一次 `extract_content_graph`，通过 `asyncio.gather` 同时执行 | `cognee/tasks/graph/extract_graph_from_data.py:259–273` |
| 可选 llm 摘要 | 若明确 `SUMMARY_METHOD=llm`，graph 与 summarize 并发，summary 内也按块 gather；本次失败不是该分支 | `cognee/tasks/graph/extract_graph_and_summarize.py:46–60`；`cognee/tasks/summarization/summarize_text.py:58–61` |
| 批间顺序 | 生成器 yield 满一批后，下游递归处理完成才继续拉下一批；同文档块批次串行 | `cognee/modules/pipelines/tasks/task.py:283–298`；`cognee/modules/pipelines/operations/run_tasks_base.py:209–232` |
| 文档级并发 | `data_per_batch=20` 默认，Semaphore 限制同时处理的 Data，独立于每文档块数量 | `cognee/api/v1/cognify/cognify.py:151,617`；`cognee/modules/pipelines/operations/run_tasks.py:205–208,269–301` |

这解释了为什么 `CHUNKS_PER_BATCH=2` 本身也不等于全局 2 并发：20 个文档各 2 块理论上仍可形成 40 个图谱调用，且其他处理阶段、其他项目也可能发请求。这里是代码能力上界，不是历史实际 40 个调用的证明。

## 现有限流与重试

官方 `cognee/shared/rate_limiting.py:21–30,34–57` 使用单个延迟初始化 `aiolimiter.AsyncLimiter`。每次 native adapter 的真实 dispatch 尝试进入它，包括 tenacity 重试。平时默认 `LLM_RATE_LIMIT_ENABLED=false`；遇到 429、503、529 或超时后，`AUTO_RATE_LIMIT=true` 的过载策略把 limiter 开启 900 秒，持续错误会延长。参数来自 `cognee/infrastructure/llm/config.py:169–177`、`overload_policy.py:20,27–28,95–121`。

这是时间速率限流，不是“到 2 个在执行就等待”：`AsyncLimiter(60,60)` 开始有 60 份桶容量，可立即允许 60 个请求。官方 [aiolimiter burst 文档](https://aiolimiter.readthedocs.io/en/stable/#bursting) 明确了初始 burst 与 `max_rate` 的关系。因此默认过载自动降速是在错误后启动，且启动时仍可能大于桥接 32 接纳上限。`config.py:39–69` 的本地 10 默认只识别 Ollama、llama.cpp、LM Studio 前缀；`engine/python/generation_config.py:88–113` 将 Codex 桥映射为 OpenAI，不命中该分支。

`cognee/infrastructure/llm/retry_config.py:29–37` 的停止条件是“至少 2 次尝试且已过 240 秒”两个条件同时成立，不是最多重试 2 次。`native_adapter.py:469–479` 使用 8 到 128 秒的指数抖动退避，`codex_busy` 会按暂时性 RateLimitError 重试。重试有退避，不宜表述为完全无节制立即重发；但多个块各自重试会共同等待并竞争桥接有限容量。

## 不能直接让用户 export 配置

`engine/python/official_service_dev.py:55–65` 在启动时清除 `LLM_*` 等宿主变量，私有 service env allowlist 也没有 `LLM_RATE_LIMIT_*`；`generation_config.compile_environment` 当前不注入相关限流参数。因此只给用户 `export LLM_RATE_LIMIT_ENABLED=true` 不是本项目可执行方案。正式修改应在现有启动编译层按 `agent_bridge` 模式有界注入并测试，避免悄悄改变 Provider 配置。

`CHUNKS_PER_BATCH` 不在上述清理前缀内，但当前也没有受控配置入口。应和 `SUMMARY_METHOD` 一起由受管启动器显式设置，防止依赖偶然继承的 shell 环境。

## 全局闸门放哪里

`cognee/infrastructure/llm/LLMGateway.py:116–162` 确实统一普通文本（`response_model=str`）和结构化提取，但它没有公开的拦截器注册接口。替换它或 native adapter 的方法属于猴补丁；不符合 `engine/python/official_service_dev.py:17–35` 对固定源码的校验及当前不补丁引擎规则，并可能漏掉直接 client 调用。

已有 `engine/python/generation_http.py:12–54` 是明确的 SDK HTTP 适配边界，持有显式 async/sync session 与 LiteLLM client cache，且只改变传输策略。因此推荐扩展该边界，使用共享 limiter，限于 runtime 当前 `completion_url` 的精确 origin 与 completion 路径。连接池的每实例 `max_connections=2` 可辅助，但多个 SDK client 各自 2 不能证明全局 2；必须共享同一 admission 状态。

接口建议：`generation_http_policy(bridge_origin=None, max_concurrency=2, wait_timeout=...)`。Provider 模式仍保留原策略。闸门须覆盖实际请求体发送至响应体读完或关闭的完整生命周期，不能收到 headers 就释放；也不能在 SDK 返回一个尚未读取的 stream 时提前释放。失败、取消、断连、SDK 内部重试、client cache 重建均验证共享上限与释放。只保留必要计数/关联 ID，不记录 prompt 正文或密钥。

具体等待超时数值须与桥接执行 120 秒、清理及 SDK request timeout 同步确定，不能仅把 120 秒移到更外层然后让上游先读超时。源码层已确认桥接 `execute` 的 120 秒计时位于获得 Semaphore 之后；排队等待不受这个计时保护。父 Agent 的本机假执行 HTTP 检查负责验证完整 deadline/取消，不由本子任务触发模型。

## 取消、失败与未知

`engine/python/codex_completion_endpoint.py:459–484` 对 admitted 有 `finally` 释放。生产 main（同文件 516）和验证 bridge（`generation_bridge.py:46–47`）均启用 `handler_cancellation=True`。因此不能因手动 transport 检查在 slot 之后，就断言排队请求断连一定无法取消；aiohttp 会在等待 Semaphore 的 await 点取消 handler。官方 [aiohttp peer disconnection 文档](https://docs.aiohttp.org/en/stable/web_advanced.html#peer-disconnection) 支持这个语义。父 Agent 已报告假执行测试中执行中及排队断连均释放，最终计数为 0；该结果由父 Agent 保存原始证据，不作为此子任务独立执行结果。

Python 官方 [gather 文档](https://docs.python.org/3/library/asyncio-task.html#asyncio.gather) 说明：默认首个异常会立即传播，其他 awaitables 不自动取消。固定 `extract_graph_from_data` 的内层 gather 没有显式 cancel+await，因此有“某块失败时，其他块仍在跑”的代码风险。但外层文档 runner 的 `run_tasks.py:301–310` 已有遇错取消并等待 item futures，不能笼统说整个官方 pipeline 没有取消。外层取消已结束的失败 item 不保证能重新找到内层 gather 脱离的块任务；需用假模型复现来证明实际影响，不能把它写成历史32名额的已证来源。

最小上游修复应在拥有每批块任务的官方函数内使用结构化并发（或显式建 task 列表，异常时 cancel 并 gather 等待），覆盖 graph 分支及可选 llm 摘要分支。固定源码不可原地修改；如需要该修复，应走固定官方版本升级/补丁归档流程。在此之前，只有“模型请求受限”成立，不能声称“任务 failed 后全部兄弟调用已回收”。HTTP 闸门也不能单独修复这个生命周期问题。

## 推荐实施与验收顺序

1. 先用假执行器覆盖实际 HTTP 路径：33 请求在原代码触发 429；原断连/取消场景释放到 0；定位异常是源端扇出还是清理泄漏。不要重放真实用户模型任务用于压力诊断。
2. 为 Codex 桥模式显式编译官方小块批次与 `from_extraction`，并在现有 SDK HTTP 策略加入共享 2 并发。原库、模型、embedding、主体及权限不变，官方源码校验继续通过。
3. 假 endpoint 输入超过 32 个提取请求、多文档/多项目、重试与取消交错，确认桥接同时执行不超过 2，正常路径不再因扇出达到32；关闭后 active/queued 均归零。加入响应体延迟用例，防止 headers 早释放。
4. 单独覆盖失败 gather 的兄弟收尾；若该问题未解决，作为真实恢复前的明确阻塞，不把执行上限当生命周期验收。
5. 自动 improve 必须补齐终态关联；submitted、running、failed、succeeded 与新增知识数分别表达。父 Agent 核对 `ImproveResult` 当前不返回 operation ID，`backlog.process_saved` 又只取 status 丢弃结果，因此不能假设现成 ID 可直接轮询。兼容方案是用 session、dataset、提交后的新时间水位在 activity 中唯一匹配，歧义则暂停；更稳定的方案是由官方响应补回 operation ID，再由客户端存关联。`Official.request` 为15秒请求超时，不能仅改为前台 improve 同步等待。失败原操作保留，恢复基于当前官方水位及去重，不用新 UUID 隐去原失败。
6. 在源码假测试通过后，使用现有已保存材料执行一个范围明确的真实整理，检查最终 operation、阶段结果、官方新增知识与来源；再核对后续 Agent 可召回。只 HTTP 200 或 POST 接收不能算完成。

以上是调研建议，尚未实施或验收真实修复。本任务没有创建新运行库、变更模型或重试失败整理。

## 本轮机制验证与可执行决策

主 Agent 已执行现有 endpoint 两项定向测试，2 tests / 1.765s / OK；另执行 [可重跑的无模型探针](evidence/codex-queue-research-20261115/probe.py)，使用真实 HTTP handler、生产相同的 `handler_cancellation=True`，只替换模型执行函数。命令及运行环境摘要见 [findings.json](evidence/codex-queue-research-20261115/findings.json)。这些是机制测试，不是日常知识产出验收，也不证明真实模型耗时或产物质量。

| 检查 | 本轮结果 | 证明边界 |
| --- | --- | --- |
| 原桥接直接突发32再加1 | 第33个429 codex_busy，执行峰值2 | 准入上限可确定复现，非供应商返回 |
| 人为阻塞假执行器 | 等待请求没有独立准入deadline | 假执行器故意绕开execute超时，不冒充真实execute超时失效 |
| 分别断开执行中、排队中的请求 | admitted从32降31、30，收尾后0 | 当前取消路径可释放，未复现名额泄漏 |
| 官方OpenAI SDK共用HTTPX两连接，突发64 | 64全部成功、SDK retries=0、执行峰值2、最终0 | 证明发送端限流可行；未替代完整LiteLLM缓存/多客户端及真实模型验收 |
| gather首异常后检查另一个任务 | 父调用已失败，兄弟任务仍运行，探针随后取消并等待收尾 | 证明标准语义风险，不证明历史32个请求具体归属 |

当前进程52213启动环境中，CHUNKS_PER_BATCH、SUMMARY_METHOD及三项LLM_RATE_LIMIT参数均未设置。固定源码因此采用上述默认值；日志栈也确认走from_extraction。桥接health本轮active_or_waiting为0、执行2、接纳32。历史北京时间11:45–12:01窗口有652条codex_busy重试告警，退避8–65秒；652是日志告警数，不是652个独立请求、模型调用或知识条目。

**选定方向：发送端统一控并发，官方小批次减少扇出，补齐任务终态与失败收尾；保持桥接2执行/32接纳，不加大队列、不切模型、不换库。**

按以下依赖顺序实施，前四步通过后才执行一次真实材料恢复：

1. `generation_config.py` / `official_service_dev.py`：仅agent_bridge模式显式注入CHUNKS_PER_BATCH=2、SUMMARY_METHOD=from_extraction。Provider模式不改变。不要依赖shell export；保留固定源码校验与原embedding配置。
2. `generation_http.py`：向现有policy传入当前模式及completion origin，为所有发往该桥接的普通文本/structured请求共用2份传输容量。优先共享一个HTTPX客户端及连接池（HTTP/1.1、max_connections=2、max_keepalive_connections=2）；若SDK缓存路径会重建多个独立客户端，则必须改用共享transport容量，不能每个客户端各给2份。HTTPX官方已提供[连接池上限配置](https://www.python-httpx.org/advanced/resource-limits/)。必须通过真实固定LiteLLM路径的缓存创建/过期及response body延迟测试后才认定覆盖完整，不能直接把本轮OpenAI SDK探针当成已集成。
3. `project_backlog.py`：同一处理通道的自动整理一次只派发一个，后续session合并排队；保存通道继续独立推进。提交前持久关联上下文，activity唯一识别原operation后持续读取终态；不唯一时保持unknown且阻止重投。保留原始失败operation，不以新任务ID清掉失败。不要假设ImproveResult已有operation_id，也不要在15秒HTTP请求内同步等整理。官方补operation_id是长期更稳的接口改进，兼容方案先以dataset/session/提交前活动水位严格唯一关联。
4. 请求生命周期：补桥接排队deadline，保留执行120秒并把等待、启动、执行、清理分别计时；SDK读取预算覆盖桥接执行及清理，连接池等待预算与工作量分开。第一候选可用桥接等待30秒、执行120秒、SDK读150秒、源端池等待300秒，均为待压力场景验证的实施初值，不是本轮测得的性能结论；不得声称这组数值保证任意大历史批次成功。池等待到限时应明确暂停/失败并保留水位，不能用增加重试掩盖容量不足。若20文档并发场景仍不能在预算内完成，则通过官方支持补充document并发配置（目标1）后再做真实恢复，不把无限等待当修复。首异常收尾必须覆盖提取块的兄弟任务；应在固定官方版本流程中修复该所有者，不修改.cache校验后的源码。生命周期未通过前，不自动恢复失败的真实任务。
5. 最后从最近3个失败整理中选一个已保存、范围明确的会话，重新核对当前权限和官方watermark后继续官方improve。它可能需要补建失败部分的图谱，但不重新采集/重发已保存原始问答；不得承诺零Token或完全没有重复计算。旧20条保存结果未知项继续隔离。确认operation终态、各阶段、知识条目及出处，并在后续Agent只读任务中召回；再逐个恢复其余会话。

必须一起通过的验收：固定LiteLLM下64以上并发普通文本/structured调用及SDK重试混合，bridge峰值不超过2且正常流0个codex_busy；响应头先到/正文延迟时不早释放；排队与执行取消、首异常、客户端缓存重建、服务正常关闭后占用与受管子进程均可确认；大文档/20文档及多项目竞争不死等，超预算有真实终态；unknown提交不重发；成功但零知识显示零，不伪造产出。真实整理需以官方实际新知识及出处、可召回为证；如该材料无可提炼结果，应如实记录并选已有明确知识事实的材料做一次受控验收，不无限重跑同一问题。

本轮没有修改上述生产文件，没有启动真实恢复，也没有将机制探针标记为产品已修复。
