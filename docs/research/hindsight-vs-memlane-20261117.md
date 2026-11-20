# Hindsight 当前能力及替换边界审计

核查时间：2026-11-17（Asia/Shanghai）。仅官方文档及源码静态审查，未安装依赖、未启动服务、未实测召回。浅克隆位于 `/tmp/hindsight-audit-20261116`。固定提交 `958468e44caadfeb6abad22f12cf464d39c37085`，提交时间 `2026-11-16T15:42:14Z`。

## 对 Memlane 的直接威胁

Hindsight 已是引擎、Web Control Plane、跨编码 Agent 自动采集与注入的整套系统。不能把“本地存储/跨 Agent 项目记忆/可视化/来源追溯/编辑管理”概括为其缺失的能力。Native 桌面体验、完整事件原始记录、日常权限管理与可靠本机运维仍可能构成差异，但价值必须用真实用户流程证明。

### 已有实现

- `hindsight-integrations/coding-agents/src/core/bank.ts:53` 默认 `coding-agent::{gitProject}`，多个 Agent 共享每仓库 bank；176-179 行项目身份是主工作树根目录 basename。支持 worktree 归一、静态 bank、路径最长前缀映射、opt-in。因此同名不同仓库有身份冲突可能，需显式 mapping，不应把“per-repo”理解成远端 URL 全局唯一身份。
- `src/core/config.ts` 提供 retainSessions、autoInject、optInOnly、harnesses/banks 覆盖。
- `src/core/runtime.ts:248-307,355-410` 每轮写会话、空闲补最终回复，异步 upsert，不阻塞会话；记录失败诊断。
- 官方 integration 文档详细列 Claude Code、Codex、Cursor、Copilot、opencode、pi、DeepSeek Harness、TraeCode 等适配。当前文档默认自动 git history ingestion、session ingestion、第一页/reflect/recall 注入，支持关闭与按项目 opt-in。
- Web Control Plane 提供 banks、entities/relations、ingestion/operations、交互 recall。源码还有 documents-view、document-history、memory-detail-modal、edit-memory-form、invalidate-memory-dialog、observation-history-view 和 attachments 渲染。不能说它只有 API 没 UI。
- Documents API 保留 original_text/chunks、源 document id、tag、更新/append/删除、重处理、history。编辑 fact、invalidate/restore 会级联重建派生观察。删除 document 永久不可撤销；invalidate fact 可恢复。
- Document History 是 operations 导出的事件及 ids/counts，不等价正文每个版本的完整快照。Observations 有变化历史，但当前 documents 不能据此直接声称具备 Memlane 的任意正文版本回滚。
- fact 编辑/失效不改源 document；重处理源 document 会重置 curation，因此迁移时必须保留人工修正关系，避免“重导入又回到错误事实”。

证据：
- https://hindsight.vectorize.io/sdks/integrations/coding-agents
- https://github.com/vectorize-io/hindsight/blob/958468e44caadfeb6abad22f12cf464d39c37085/hindsight-integrations/coding-agents/src/core/bank.ts#L53
- https://github.com/vectorize-io/hindsight/blob/958468e44caadfeb6abad22f12cf464d39c37085/hindsight-integrations/coding-agents/src/core/runtime.ts#L248
- https://hindsight.vectorize.io/developer/services
- https://hindsight.vectorize.io/developer/api/documents
- https://hindsight.vectorize.io/developer/api/memories

## 可以替换什么，不能直接替换什么

### Cognee 引擎替换可行，但不是改 URL

项目/dataset 可映射 bank；原文材料和会话可映射 document/chunks；明确记忆条目可映射 world/experience fact；归纳记忆可重建 observation；持续主题摘要可映射 pages/mental models。Cognee 通用知识图、代码图、任意图节点边与 Hindsight 类型化事实/实体/时间/因果模型语义不一，无法保证无损一对一搬迁。原始记录 UI、审批、版本、权限、工作区发现和任务语义不应由 bank 自动继承。

官方特别支持从外部抽取结果构建 ZIP archive，`mode=merge` 导入 chunks/facts，省去 LLM 抽取、重切片；仍重新 embedding、entity resolve 和关系索引，并按配置触发 consolidation/webhooks。这为迁移已整理记忆提供路径，不能因此保证 Cognee 图和 Memlane 业务状态自动兼容。

固定证据：
https://github.com/vectorize-io/hindsight/blob/958468e44caadfeb6abad22f12cf464d39c37085/hindsight-docs/docs/developer/api/memory-banks.mdx#L749

`749-799` 行明确 archive 结构以及 `original_text`、chunks、facts、fact metadata/dates；document 本身不携带 metadata 或时间字段。迁移 session 元数据应放 fact metadata，或保留 Memlane 自己的原始记录存储。

### 不能把默认会话采集当完整原始档案

Codex reader `transcript-codex.ts:1-35` 明确保留用户/助手文本、compact action（工具名+目标，无完整参数），丢弃 function_call_output；104-111 行只取文本 content，图片无 text。去掉 developer、推理与记忆注入以防反馈回路。这有利于记忆抽取，但不足以替代完整 tool trace 和截图/附件归档。Hindsight 引擎本身另有文件/inline attachments API，不意味着 coding-agents reader 已完整导入这些内容。

https://github.com/vectorize-io/hindsight/blob/958468e44caadfeb6abad22f12cf464d39c37085/hindsight-integrations/coding-agents/src/core/transcript-codex.ts#L1
https://github.com/vectorize-io/hindsight/blob/958468e44caadfeb6abad22f12cf464d39c37085/hindsight-integrations/coding-agents/src/core/transcript-codex.ts#L104

### ACL

默认 `DefaultTenantExtension` 无认证，单 schema；内置 `ApiKeyTenantExtension` 比较一个环境变量 key，通过后进入同一个 schema。不是现成每 Agent × 每项目 CRUD 权限 UI。

但有可实现细粒度权限的正式扩展机制：TenantExtension 可按 bank 验证，OperationValidator 的 `resolve_tag_scope` 与 `resolve_write_tag_scope` 分开限制读与写，并封闭整 bank 操作。不能说“不支持权限”，应说“默认包没有与 Memlane 当前权限模型对等的现成配置，需保留门面或写扩展”。Cloud 的组织 RBAC/API keys 也不自动等于本地每 Agent CRUD。

固定证据：
https://github.com/vectorize-io/hindsight/blob/958468e44caadfeb6abad22f12cf464d39c37085/hindsight-api-slim/hindsight_api/extensions/builtin/tenant.py#L8
https://github.com/vectorize-io/hindsight/blob/958468e44caadfeb6abad22f12cf464d39c37085/hindsight-api-slim/hindsight_api/extensions/builtin/tenant.py#L44
https://github.com/vectorize-io/hindsight/blob/958468e44caadfeb6abad22f12cf464d39c37085/hindsight-api-slim/hindsight_api/extensions/operation_validator.py#L1176
https://github.com/vectorize-io/hindsight/blob/958468e44caadfeb6abad22f12cf464d39c37085/hindsight-api-slim/hindsight_api/extensions/operation_validator.py#L1220

### Embedding 与部署

可使用兼容 provider 及同一 embedding 模型配置；不能直接复用 Cognee 向量，因为抽取对象、切分和关系模型不同。Hindsight archive 格式有意不携带 embeddings/数据库 ids，导入重建向量。官方要求同维度更换模型也 reembed，避免空间混用。

https://hindsight.vectorize.io/developer/models
https://hindsight.vectorize.io/developer/admin-cli#reembed

本地运行受支持：Linux/macOS/Windows，pg0 内嵌 PostgreSQL；支持 Ollama/LM Studio 等本地模型，也支持外部 LLM 和编码订阅 provider。本地存库不等于内容不发外网，取决于三个模型槽（LLM/embedding/rerank）的配置。

官方部署文档称 Full 镜像约9GB（压缩3.7GB）、API 建议2GB RAM，UI约256MB、Postgres建议1GB+；Slim镜像约500MB、API建议1GB，但需外置embedding/rerank。这是官方估算，非本机测量。Mac Intel 有 slim/ONNX/Python版本限制。pg0官方定位开发便利，正式生产建议外部Postgres。

https://hindsight.vectorize.io/developer/installation

## 当前成本（官方实时核实）

Self-host 软件 MIT 免费，无按量软件费；仍有本机资源、模型推理和维护成本。使用自有 API 模型按模型商账单，不能拿 Cloud Retain 单价估自建成本。

Cloud（2026-07-06 后费率）：Retain $10/百万输入tokens；Import $5/百万输入tokens；Recall $0.75/百万输出tokens；Reflect $0.05/次；Retrieve Model/Page $0.25/百万输出tokens；创建/刷新Model/Page $0.05/次；超过30天存储 $0.25/百万tokens/月。标准文件抽取无额外费，Iris $7.5/百万输出tokens 后还需 Retain。

计费细节：Retain计 content+context，相同 document 仅变动内容收费、identical免费；consolidation已包含，Reflect/Page内部recall不另收费；Cloud导入虽免LLM抽取仍$5/M。pages自动刷新可能成为最大成本项；coding-agents 默认hourly cron，通用pages API默认 consolidation后刷新，应按真实路径区分。

例：每月新写1M tokens、recall返回2M、200次reflect、5页每天刷新一次（150次）、既有10M过期计费tokens：10+1.5+10+7.5+2.5=$31.5/月，未含Page读取/Iris/初始化导入；仅情景预算，不是用户实测。初始化10M历史全文 retain $100 或符合archive导入格式时 import $50，后续维护另算。

https://vectorize.io/pricing
https://docs.hindsight.vectorize.io/cloud/billing/

Inference 单独服务（用于自建）只含指定模型调用：decision $0.02/M input、rerank $0.015/M、embed $0.005/M；不是retain/reflect整条流程费用。抽取及生成仍可能需要LLM。
https://vectorize.io/models

## 未验证与建议

未运行原生安装、未测中文代码记忆准确率、冲突纠正/删除后召回、实时采集恢复和本机占用。最合理验证是保留现有存储快照、以一个项目副本进行侧向评估，比较同一真实任务的人工纠正次数、相关证据引用、错误记忆残留、日均成本和维护时间。没有依据直接断言 Hindsight 已能整体替换 Memlane，也没有依据继续做同样的全栈而不收缩范围。


## Memlane 当前状态核对与投入判断

本地 Memlane HEAD 为 `73c7343`；本次读取了当前工作树的产品定义及2026-11-13至16日交付记录。存在其他会话未提交证据；未改代码、配置、服务或现有库。

现定位：Cognee 为唯一内容数据源，Memlane 负责 macOS 阅读、Agent 接入、模型配置和运行入口。不能再拿早期自建正文版本库、任务恢复全栈作为当前产品差异。项目权限已重新授权并有独立官方主体及实际 ACL 证据，也不能据旧“移除权限”文档判定不存在。

当前工程证据：
- `docs/product.md`：当前定位、统一日常库、页面和来源约定。
- `docs/engineering/project-access-delivery-20261113.md`：真实项目/Agent read/write/delete、通用记忆采用、撤权后缓存、403待发等。
- `docs/engineering/multi-agent-provider-support-20261114.md`：Codex/pi/TraeX/Claude实际链路；Cursor真实宿主仍未证，完整首批未交付。特定Ollama旧状态误写累计4轮，已停止，不能为本次调研重试。
- `docs/engineering/extraction-performance-20261116.md`：Codex与TraeX宿主保持打开时自动保存/整理/产出记忆，约112/124秒处理样本；非同负载Hindsight对照，不能据此判定快慢优劣。
- `docs/engineering/knowledge-source-chain-audit-20261116.md`：实际来源会话分页/返回阅读已验证；历史仅会话级来源不得伪造精确问答因果。
- `docs/engineering/evidence/incremental-provenance-20261116/live-result.json`：pi样本的实际问答来源ID；不外推所有历史数据均有精确锚点。

判断：若目的只是“跨编码Agent共享本地项目记忆”，不建议继续建设一套功能相同的完整系统，优先试用Hindsight官方组合。若目的还包括每天主动阅读、核查记忆、直观管理Agent权限、完整原始记录，Memlane仍有可验证的产品空间；原生UI和中文界面本身还不是已证实的付费需求。学习/自用价值与独立商业产品可行性应分开，不以既有投入作为继续理由。

不建议立刻改成多引擎平台。先单项目比较官方Hindsight与现有Memlane，确认具体缺口后再决定是否保留桌面壳和必要适配。没有证据证明Hindsight已经解决用户真实任务上的错记/漏记，也没有同一数据集证明优于现用Cognee。论文LongMemEval/LoCoMo结果和官网当前榜单不可直接外推代码项目；官方sdebench表明61项任务两侧都完成60–61，主要变化是纠正次数和成本，未在本机复现。

### 替换路线与开发成本粗估

以下为单名熟悉项目的工程师的有效人日估算，包含实现与定向验收，非已完成排期；在单项目试迁后修正。保留现有独立主体权限、完整trace和任意图语义会显著影响范围。

| 路线 | 粗估 | 交付边界 |
| --- | --- | --- |
| 官方服务+一个Agent+一个新项目 | 0.5–2人日验证 | 不是生产替代，也不含旧数据迁移 |
| 迁入已有文本记忆及来源索引，改用官方Web UI | 额外2–5人日 | 需写Cognee导出/格式映射/校验，旧图与ACL不保证等价 |
| 保留Memlane主要阅读/搜索/接入界面，更换底层 | 5–10人日基础范围 | 不含全量权限、工具原始档案、旧图及全部故障语义对等 |
| 保留上述当前语义并完整回归 | 10–20人日起 | ACL、图谱或来源映射缺口可能继续增加；不是工期承诺 |

原文/人工记忆可以保留；向量、引擎派生图和观察通常需重建。若把Cognee已归纳内容作为事实导入，应标明它是旧引擎产物，保留出处与人工修正；不能包装为原始用户确认事实。找不到原问答映射的历史数据，换引擎不能补回。

## 自托管最小路径（供用户执行，尚未运行）

优先官方Full Docker镜像：包含API、Web UI、内置Postgres与本地embedding/reranker；LLM另配。macOS Docker Desktop运行容器，Mac上本地Ollama用host.docker.internal访问，不把容器localhost误作宿主。见 https://hindsight.vectorize.io/developer/installation 。

示例 `compose.yaml`：

```yaml
services:
  hindsight:
    image: ghcr.io/vectorize-io/hindsight:latest
    restart: unless-stopped
    shm_size: 1gb
    stop_grace_period: 75s
    ports:
      - "127.0.0.1:8888:8888"
      - "127.0.0.1:9999:9999"
    environment:
      HINDSIGHT_API_LLM_PROVIDER: openai
      HINDSIGHT_API_LLM_MODEL: gpt-4o-mini
      HINDSIGHT_API_LLM_API_KEY: ${HINDSIGHT_API_LLM_API_KEY}
      HINDSIGHT_API_WORKER_ID: hindsight-local
    volumes:
      - hindsight-data:/home/hindsight/.pg0
volumes:
  hindsight-data:
```

将模型key放本目录`.env`（不要提交Git），执行`docker compose up -d`及`docker compose logs -f`，访问 http://127.0.0.1:9999 。`latest`用于首次验证，稳定后固定实际镜像digest；命名卷持久化不是备份。Cloud单价不适用于此自托管方案，模型费用另计。

Agent安装示例：

```sh
npx @vectorize-io/hindsight-coding-agents install codex --server self-hosted --api-url http://127.0.0.1:8888
```

配置`~/.hindsight/coding-agent.json`启用`optInOnly: true`并仅列目标项目`optInPaths`；初次不导入全部历史。默认按主仓basename生成bank，同名仓库需`mapPathToBank`区分。首次正式切日常之前明确旧Cognee插件采集开关和回滚边界，避免两套同时注入造成对照失真。

本次仅研究与提供步骤；未执行Docker/安装器、未修改Agent配置、未切换日常库。
