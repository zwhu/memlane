# Cognee 技术依据与集成边界

调研日期：2026-11-07。结论：Cognee 可以作为 Memlane 内置的本地索引和图谱引擎，但知识版本、审核、持久队列、Agent 适配和后台服务生命周期仍需 Memlane 管理。首版应固定发行版本，并把 Cognee 放在单一受管 worker 后面；不要让每个 Agent 独立启动并写同一份数据库。

本文只完成官方文档、源码和依赖声明核对，**未安装依赖、运行 Cognee、下载模型、使用用户 Agent 凭据或验证安装包**。建议不等于已实现能力。

分发决策更新：用户随后明确要求主下载包保持轻量。本文中的“内置”表示 App 管理的本地运行时；当前方案改为引擎与模型独立下载、自动准备，不将这些重型依赖放入主 `.app`。以下源码事实继续作为依据，最终分发与验收以 [技术方案](technical-plan.md#12-轻量分发组件准备与自动更新) 和 [实施清单](implementation-plan.md) 为准。

实现路线补充：本文核查的是 **Python Cognee 1.6.7**，不代表 Cognee 只能使用 Python。官方另有实验阶段的 [Cognee-RS](research-cognee-rust.md)，是本地引擎的 Rust 移植；已加入 P0 对照验证。本文的 wheel、FastEmbed、Enola 和 Python 内部 API 结论不能直接套用到 Rust。

## 1. 固定证据版本

- 最新发布版本为 [v1.6.7](https://github.com/topoteretes/cognee/releases/tag/v1.6.7)，GitHub 标记发布时间为 2026-11-06 17:15 UTC；tag 对应提交 `eaa33be5c58cd14584e64e3e0ed2e1479472ee09`。
- 本次核对的官方 `main` 为 `8853378eae5366addb064e758e560a456146ae1a`。它比该 tag 多 4 个提交，[差异仅涉及 cognee-mcp 的依赖声明与锁文件](https://github.com/topoteretes/cognee/compare/eaa33be5c58cd14584e64e3e0ed2e1479472ee09...8853378eae5366addb064e758e560a456146ae1a)，本文引用的 core 文件可对应发行提交。
- 机器上已有 Cognee checkout 为 `e93a4f0c76e6af27142c25189f14c200e1749d51`、版本 1.5.4，存在与本任务无关的未跟踪资料；本次未修改它。技术方案不能把这个旧 checkout 当成当前发行版。

建议以 `cognee==1.6.7` 作为首轮适配候选，锁定完整依赖与 wheel 哈希；验证失败时记录原因再选择版本，不能依赖浮动 `main`。

## 2. 内置存储和打包

| 已核实事实 | 对 Memlane 的含义 |
| --- | --- |
| Python 声明范围为 `>=3.10,<3.15`；核心依赖含 FastEmbed、ONNX Runtime、LanceDB、Ladybug 和 `enola-cli==0.4.30` | 需要打包 Python 运行时、原生库和 Enola 可执行文件；不能只把 Python 源文件塞进 App |
| 关系库默认 SQLite；向量库默认 LanceDB；图库默认 Ladybug | 用户不必部署独立数据库服务器。Memlane 的业务 SQLite 与 Cognee 自己的关系库保持分离 |
| 图库、向量库默认可由 Cognee 的数据库子进程承载 | “一个受管 worker”指一个对外拥有者，内部仍可能有数据库子进程；退出、重启和签名需覆盖子进程树 |
| Ladybug 0.19.0 的 macOS wheel 要求 macOS 15；13/14 的依赖声明回退 0.17.x，上游注明该线仍有后来修复的数据损坏缺陷 | 建议首版 macOS 15+。Apple Silicon 优先是缩小打包矩阵的产品实现建议，不代表已验证兼容性 |

来源：[依赖与平台条件](https://github.com/topoteretes/cognee/blob/eaa33be5c58cd14584e64e3e0ed2e1479472ee09/pyproject.toml)、[关系库配置](https://github.com/topoteretes/cognee/blob/eaa33be5c58cd14584e64e3e0ed2e1479472ee09/cognee/infrastructure/databases/relational/config.py)、[向量库配置](https://github.com/topoteretes/cognee/blob/eaa33be5c58cd14584e64e3e0ed2e1479472ee09/cognee/infrastructure/databases/vector/config.py)、[图库配置](https://github.com/topoteretes/cognee/blob/eaa33be5c58cd14584e64e3e0ed2e1479472ee09/cognee/infrastructure/databases/graph/config.py)。

建议：运行时与依赖在构建阶段准备，App 启动不执行 `pip install`；数据与模型缓存显式放在 Memlane 的 Application Support 目录。Cognee 的默认 `.data_storage`、`.cognee_system` 路径需要覆盖，不能落到 App bundle 或碰到用户已有 Cognee 数据。[路径配置](https://github.com/topoteretes/cognee/blob/eaa33be5c58cd14584e64e3e0ed2e1479472ee09/cognee/base_config.py)

仍需验证：可重定位 Python、arm64 wheel 可用性、Ladybug 扩展文件、子进程入口、嵌套代码签名与公证、完全离线冷启动、升级迁移与中断恢复。源码中包含依赖不代表已通过独立 `.app` 分发验证。

## 3. 项目隔离与检索范围

`ENABLE_BACKEND_ACCESS_CONTROL` 未设置时，会根据所选图/向量数据库是否支持多用户隔离决定；显式设为 `true` 时仍会核查 provider 能力。默认 Ladybug 与 LanceDB 的 handler 为每个 dataset 分配独立数据库：图库名使用 dataset UUID 的 `.lbug` 文件，向量库使用 UUID 的 `.lance.db` 目录。数据上下文将这些路径绑定到当前请求。[上下文与开关](https://github.com/topoteretes/cognee/blob/eaa33be5c58cd14584e64e3e0ed2e1479472ee09/cognee/context_global_variables.py)、[Ladybug handler](https://github.com/topoteretes/cognee/blob/eaa33be5c58cd14584e64e3e0ed2e1479472ee09/cognee/infrastructure/databases/graph/ladybug/LadybugDatasetDatabaseHandler.py)、[LanceDB handler](https://github.com/topoteretes/cognee/blob/eaa33be5c58cd14584e64e3e0ed2e1479472ee09/cognee/infrastructure/databases/vector/lancedb/LanceDBDatasetDatabaseHandler.py)

搜索先解析读取权限，再在各 dataset 的上下文中运行；关闭 access control 后所有 dataset 共用上下文，搜索只运行一次。`datasets=None` 表示所有可读 dataset，不是当前项目。NodeSet 是标签与过滤工具，不是权限边界。[搜索入口](https://github.com/topoteretes/cognee/blob/eaa33be5c58cd14584e64e3e0ed2e1479472ee09/cognee/api/v1/search/search.py)、[搜索上下文实现](https://github.com/topoteretes/cognee/blob/eaa33be5c58cd14584e64e3e0ed2e1479472ee09/cognee/modules/search/methods/search.py)、[更新接口中的 NodeSet 说明](https://github.com/topoteretes/cognee/blob/eaa33be5c58cd14584e64e3e0ed2e1479472ee09/cognee/api/v1/update/update.py)

Memlane 建议采用：

- 显式开启 backend access control，并在启动时断言 Ladybug/LanceDB handler 与预期一致。
- 一份 Cognee 关系库、一个受管 worker；项目知识、全局偏好、全局经验、项目代码图谱分别映射到 dataset UUID。无需每个项目部署一套 Python 服务。
- 服务端计算允许查询的 UUID 集合，默认仅当前项目＋全局偏好＋全局经验。Agent 不直接提交任意 dataset 名称，不允许 `None` 扩展到全部。
- 检索返回 dataset、原始文档/条目标识与命中依据，服务层再检查范围和当前版本。NodeSet 只做类别等过滤。
- 当前项目优先是 Memlane 的显式排序和上下文规则，不能假定 Cognee 会自动解决项目与全局的语义冲突。

隔离验收必须使用 A/B 两项目的同名实体与相反约定，验证 B 的内容不会出现在只读 A 的 chunk、graph 和 code 结果中，并覆盖一次并发上下文切换。需要确认底层隔离后，才能启用图谱检索结果直接参与回答。

## 4. 新增、编辑与版本一致性

`remember()` 不只是保存：默认执行 add、cognify，并可能自动 improve。可以关闭 `self_improvement`；普通文本的 graph extraction 仍可能调用 LLM。`update()` 保留 `data_id`，优先对变动片段增量处理，不满足条件时执行删除旧记忆、固定 ID 重新摄入、重建索引；返回值需要检查 `status`、`fallback` 和 `error`。它不是 Memlane 的版本历史数据库，也不提供业务层的跨库原子提交。[remember](https://github.com/topoteretes/cognee/blob/eaa33be5c58cd14584e64e3e0ed2e1479472ee09/cognee/api/v1/remember/remember.py)、[update](https://github.com/topoteretes/cognee/blob/eaa33be5c58cd14584e64e3e0ed2e1479472ee09/cognee/api/v1/update/update.py)

`cognify` 内部有独立的 `get_indexing_tasks()`：分类、切片、保存并向量化，任务标记 `needs_llm=False`，用于先让 chunk 可检索，再提取实体。`SearchType.CHUNKS` 返回原始 chunk payload，不生成 LLM 答案，也关闭了会话预处理。因此可将“原文已经可查”和“知识图谱处理完成”分开。[索引任务](https://github.com/topoteretes/cognee/blob/eaa33be5c58cd14584e64e3e0ed2e1479472ee09/cognee/api/v1/cognify/cognify.py)、[Chunk retriever](https://github.com/topoteretes/cognee/blob/eaa33be5c58cd14584e64e3e0ed2e1479472ee09/cognee/modules/retrieval/chunks_retriever.py)

建议契约：

1. 业务 SQLite 事务保存原文、revision、来源、变更事件和待索引任务，作为唯一业务真相。
2. Worker 只处理该条当前 revision；所有请求具有幂等 ID。成功后回传 `data_id`、已索引 revision、索引 generation 和状态。
3. 业务保存成功后，界面立即显示新正文与“索引中”；旧版进入历史。默认检索不允许用旧正文冒充新正文。
4. 查询以 chunk/文档命中为候选，经 ledger 检查 `current_revision == indexed_revision`、有效状态、所属范围，再读取对应版本正文返回。过滤后不足可扩大候选集，不直接把旧片段补回来。
5. Worker 提交结果时再次比较 revision；处理过程中已有更新，则丢弃旧任务的“当前已索引”资格，排入最新 revision。更新失败保留业务原文和可重试任务。
6. 旧版只留在业务历史，不作为当前 dataset 的有效知识。恢复版本是新建 revision，再走同样的索引流程。

内部索引任务不是承诺稳定的公共 API；采用它必须封装在固定版本适配器内，并实测 `add → index-only → CHUNKS`、`forget/update → reindex`。仅靠 chunk 结果过滤无法阻止旧实体关系影响通用 graph completion；在来源映射与增量删除通过契约测试前，Agent 的日常记忆读取先使用可追溯的 chunk 结果。删除清理由 `forget(data_id, dataset_id)` 承担，逻辑删除先在业务库生效。[forget](https://github.com/topoteretes/cognee/blob/eaa33be5c58cd14584e64e3e0ed2e1479472ee09/cognee/api/v1/forget/forget.py)

## 5. 本地向量模型

无 LLM key 且没有显式 embedding 配置时，Cognee 当前可回退 FastEmbed 的 `BAAI/bge-small-en-v1.5`。这是英文模型；若后来增加 Agent bridge 的 LLM 配置，不能依赖这个隐式回退保持模型不变。Memlane 必须始终显式设置 embedding provider、模型与维度，并记录模型/分词/切片版本。[配置解析](https://github.com/topoteretes/cognee/blob/eaa33be5c58cd14584e64e3e0ed2e1479472ee09/cognee/infrastructure/databases/vector/embeddings/config.py)

以下为 FastEmbed 0.9.1 官方 registry 可核实的候选。大小是 registry 的模型文件估值，不是实测内存或最终安装包大小。

| 候选 | 维度 / 输入限制 | 前缀 | 文件估值与取舍 |
| --- | --- | --- | --- |
| `jinaai/jina-embeddings-v2-base-zh` | 768 / 8192 token，中英混合 | 无需 query/passage 前缀 | 约 0.64 GB；建议优先验证中文开发知识质量，Apache-2.0 |
| `sentence-transformers/paraphrase-multilingual-MiniLM-L12-v2` | 384 / 128 token，多语言 | 无需前缀 | 约 0.22 GB；较小，但长条目必须切片，不能静默截断 |
| `BAAI/bge-small-zh-v1.5` | 512 / 512 token，中文 | registry 标注前缀不强制 | 约 0.09 GB；可作较轻的中文基线，不能据此承诺中英跨语检索效果 |

来源：[Jina 多语 registry](https://github.com/qdrant/fastembed/blob/v0.9.1/fastembed/text/pooled_normalized_embedding.py)、[MiniLM registry](https://github.com/qdrant/fastembed/blob/v0.9.1/fastembed/text/pooled_embedding.py)、[BGE registry](https://github.com/qdrant/fastembed/blob/v0.9.1/fastembed/text/onnx_embedding.py)、[Jina 官方模型卡与许可证](https://huggingface.co/jinaai/jina-embeddings-v2-base-zh)。

`multilingual-e5-small` 虽有官方模型卡，但不能据此声称 FastEmbed 默认支持；本次核对的 registry 明确列出的是 E5-large，体积更大且要求区分查询与文档前缀，不作为首版默认候选。[E5-small 模型要求](https://huggingface.co/intfloat/multilingual-e5-small)、[FastEmbed pooled registry](https://github.com/qdrant/fastembed/blob/v0.9.1/fastembed/text/pooled_embedding.py)

FastEmbed 会使用模型缓存，Cognee 支持通过 `FASTEMBED_CACHE_PATH` 指定位置。首次没有模型文件时需要获取模型；“本地推理无按次 API 费”不等于零下载、零内存或无需准备。建议构建/安装时准备固定模型文件并校验哈希，模型未就绪时仍允许原文保存，清楚显示索引状态，不偷偷换用云 embedding。[缓存实现](https://github.com/topoteretes/cognee/blob/eaa33be5c58cd14584e64e3e0ed2e1479472ee09/cognee/infrastructure/databases/vector/embeddings/FastembedEmbeddingEngine.py)

更换模型必须创建新索引 generation 并重建，不能将不同模型甚至同维度的向量混合使用。最终默认模型由中文、英文、代码标识符混合的检索集，以及冷启动、峰值内存、后台耗时测试决定。本次未得到这些性能数据。

## 6. 用用户的 Agent 提供 LLM

Cognee 有自定义 provider、endpoint 与结构化输出配置，因此可以在本机设置一个兼容其模型请求的桥。官方 Claude Code integration 已给出参考：将 Cognee 请求转为 `claude -p` 非交互调用，并传递 JSON schema；它使用独立子进程、关闭 hooks/plugins 防递归、设置超时并限制并发。这证明桥接路径存在，**不证明 Codex、pi、TRAE、traex 已有相同实现**。[LLM 配置](https://github.com/topoteretes/cognee/blob/eaa33be5c58cd14584e64e3e0ed2e1479472ee09/cognee/infrastructure/llm/config.py)、[官方 Claude observer 说明](https://github.com/topoteretes/cognee-integrations/tree/main/integrations/claude-code#claude-observer-local-mode-on-your-claude-subscription)

桥接建议契约：请求携带 request ID、prompt/messages、JSON schema、超时和取消信号；返回经 schema 校验的数据及真实错误。后台调用使用独立会话，不继承用户项目内的记忆写入 hooks，避免“保存记忆→调用 Agent→再次保存记忆”的递归。认证交给 Agent 自己处理，不复制用户登录密钥进 Cognee。模型推理仍消耗对应 Agent 的额度；云模型会接收所处理的内容。

桥接未通过验证前，手动保存与本地 chunk 检索可以先运行；AI 提炼显示“需要可用的整理 Agent”。不要为通过测试而自动让用户额外购买 API key。GLiNER 可作另一条无 LLM 抽取路径，但官方示例要求额外模型和依赖，不等价于免费的完整摘要与整理，暂不增加到首版默认包。[无 LLM 示例](https://github.com/topoteretes/cognee/blob/eaa33be5c58cd14584e64e3e0ed2e1479472ee09/examples/guides/no_llm_remember_recall.py)

## 7. “立即整理”的审核边界

`improve()` 是执行流程，不是纯预览函数。默认阶段包括保存 session Q&A/Agent trace、提取上下文、提炼、偏好更新、冲突复核和全局上下文索引。阶段会写入持久数据；没有 session IDs 时若干阶段跳过，部分非致命失败后仍继续后续阶段。返回总结果不能简单视为所有阶段都成功。[improve 实现](https://github.com/topoteretes/cognee/blob/eaa33be5c58cd14584e64e3e0ed2e1479472ee09/cognee/api/v1/improve/improve.py)、[阶段实现](https://github.com/topoteretes/cognee/blob/eaa33be5c58cd14584e64e3e0ed2e1479472ee09/cognee/modules/improve/stages.py)

因此 Memlane 不应把按钮直接接到生产 dataset 的默认 `improve()`，再在界面补一层确认。建议用整理 Agent 读取服务提供的当前知识快照，返回结构化的新增摘要、标签与修改建议；业务服务根据操作类型决定自动提交或待确认。每条建议绑定目标 revision，用户确认时重新检查，过期建议需要重新生成。Agent 的“整理模式”只拿到读取和提交建议能力，不能直接调用生产 `update/forget`。

Cognee 的阶段可按需复用；若未来使用完整 `improve` 做提案实验，应在独立的影子 dataset/存储上下文执行，并明确隔离 session 水位、偏好等副作用，不能仅换一个名称就假定所有全局状态已隔离。首版避免为此设计一套通用图谱 diff/合并引擎。

## 8. 代码图谱

发行版核心依赖固定 Enola CLI 0.4.30；Cognee 通过 `ENOLA_PATH`、运行时 scripts 目录或 PATH 找到可执行文件，调用 `enola --generate` 生成 snapshot，校验格式后映射图节点。当前实现不在运行时下载 Enola，解析任务与向量化分离，向量索引默认关闭。`SearchType.CODE` 可使用结构索引；自然语言 completion 不是所有代码查询的必要条件。[Enola 调用](https://github.com/topoteretes/cognee/blob/eaa33be5c58cd14584e64e3e0ed2e1479472ee09/cognee/tasks/code_graph/enola.py)、[图谱任务与 index_vectors](https://github.com/topoteretes/cognee/blob/eaa33be5c58cd14584e64e3e0ed2e1479472ee09/cognee/tasks/code_graph/extract_code_graph.py)

Memlane 建议将代码图谱放在独立的项目 code dataset；只有用户手动启用后才把该仓库交给代码索引任务。文件变更由 Memlane 合并后调度，记录最后成功的仓库状态与 snapshot ID。Enola 是否支持该项目语言、忽略规则、增量行为与调用边的覆盖范围，需要用真实仓库样本验证；结果是静态分析依据，不承诺覆盖动态调用或完整运行时影响。

App 打包需要把 Enola 与运行时一起固定，并显式传可执行路径，不能依赖用户 shell 的 PATH。当前 `run_enola_generate` 以仓库为工作目录，默认输出目录为仓库下的 `.enola`，并读取其中的 `facts.jsonl`；上游默认会写仓库。外置输出是 Memlane 待验证的适配能力，启用前应说明实际路径，不能宣称已有零仓库写入保证。

## 9. 开发前必须通过的契约检查

| 检查 | 通过标准 |
| --- | --- |
| 独立运行时 | 干净 macOS 15+ arm64 环境无系统 Python、无 Homebrew、无 Docker，仍可加载模型、创建库和检索；离线启动无隐式下载 |
| 数据隔离 | A/B 同名实体、全局与项目相反规则，多范围并发查询均按显式范围返回 |
| 原文可用性 | 不启动整理 Agent，也可保存原文并在本地索引完成后命中；无 API key 消耗 |
| 版本一致性 | 连续编辑、删除、恢复、处理途中重启后，检索不返回失效正文；历史完整保留 |
| 受控整理 | 提案生成和拒绝不会改已有有效知识；确认过期 revision 的提案被拦截；部分失败可追踪 |
| Agent bridge | 至少一个目标 Agent 能后台执行结构化任务、处理取消/超时/登录失败，且不会递归触发记忆写入 |
| 代码图谱 | 未启用不扫描；启用后能查询样本文件/函数/调用关系；更新和失败均有状态 |
| 恢复 | 仅杀 Cognee worker、连同数据库子进程异常、整服务退出三种情况都不会把读取错误伪装成空结果，持久任务可重试 |

这里的检查用于收敛实现风险，不要求在技术方案阶段假装已经通过。
