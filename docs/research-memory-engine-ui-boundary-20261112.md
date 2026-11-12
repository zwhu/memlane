# 只做界面时的记忆引擎选型复核

日期：2026-11-12。依据本次读取的官方主分支文档和源码；未安装或重跑两套引擎，不代表当前 Memlane 已完成迁移或新版兼容验收。

## 结论

Cognee 是合理选择，但理由应是它的官方采集、知识处理与检索链路能够承接 Memlane 不想承担的职责；不能用“程序员选 Cognee、个人偏好选 Mem0”判断。Mem0 同样能管理项目和 Agent 的记忆，也提供本地服务、管理界面与历史接口。若需求只剩条目浏览、编辑和搜索，Mem0 的接口也是直接候选。

“只管美观”还意味着接受引擎原有的数据语义和功能边界。自定义审核、精确版本恢复、跨项目授权、后台任务调度等要求，不能因为选择某个引擎就假定全由底层提供。缺少的能力先显示为缺口，不在客户端补建第二套记忆系统。

## 已核实的能力与边界

| 项目 | 官方证据 | 对本项目的含义 |
| --- | --- | --- |
| Cognee | 官方介绍将文档、代码和会话构建为可复用记忆，提供本地处理与检索；无 LLM 时部分处理可用，依赖 LLM 的 improve 阶段会跳过。[README](https://github.com/topoteretes/cognee/blob/main/README.md) | 可直接使用底层处理能力；本地运行和提炼质量是不同问题。 |
| Cognee Agent 接入 | 官方 integrations 提供 Claude Code/Codex 插件，本地服务配置可共享；Claude Code 插件记录 prompts、tool traces、responses，并自动 recall。pi 被明确列为社区集成。[integrations](https://github.com/topoteretes/cognee-integrations/blob/main/README.md) | App 负责安装、配置与展示连接状态，避免重写并行采集器；不同 Agent 的覆盖仍须逐项核验。 |
| Cognee improve / UI | improve 是引擎的公开自改进编排入口；官方开发说明的 UI 命令同时启动前端、API、MCP。[improve 源码](https://github.com/topoteretes/cognee/blob/main/cognee/api/v1/improve/improve.py)、[AGENTS](https://github.com/topoteretes/cognee/blob/main/AGENTS.md) | 已有官方处理和展示入口可供适配，但不是 Memlane 全套审核与版本产品规则已经原生满足的证据。 |
| Mem0 OSS | 本地 SDK 支持 add/search/get/get_all/update/delete/history；REST server 暴露 OSS 操作。[REST 文档](https://github.com/mem0ai/mem0/blob/main/docs/open-source/features/rest-api.mdx)、[Python 使用参考](https://github.com/mem0ai/mem0/blob/main/skills/mem0/client/python.md) | 做条目管理界面完全可行，不能排除 Mem0。history 不自动等于完整产品级恢复流程。 |
| Mem0 self-hosted server | 官方维护 FastAPI + dashboard，提供记忆浏览、按用户过滤、实体、API key、配置、请求审计。[server README](https://github.com/mem0ai/mem0/blob/main/server/README.md) | Mem0 也有可直接作为 UI 后端的本地产品基础。 |
| Mem0 OSS 与 Platform | 最新迁移文档将 OSS 自动提炼改为 ADD-only，检索为 semantic/BM25/entity hybrid；外部图存储从 OSS 移除，graph memory 移到 Platform。[迁移文档](https://github.com/mem0ai/mem0/blob/main/docs/migration/oss-v2-to-v3.mdx) | 不应再用旧版“自动 UPDATE/DELETE”“OSS 自带图谱”描述当前方案；显式 update/delete API 仍需与自动提炼区分。 |
| Mem0 MCP | 当前 Platform MCP 文档明确记忆在 Mem0 账户中，不在本机。[MCP 文档](https://github.com/mem0ai/mem0/blob/main/docs/platform/mem0-mcp.mdx) | 云 MCP 不能直接当成本地自托管接入证据。 |
| OpenMemory | 旧本地 MCP + dashboard 已明确不再维护，官方推荐本地需求转 Mem0 self-hosted server。[归档说明](https://github.com/mem0ai/openmemory/blob/main/openmemory-archive/README.md)；同名仓库当前主 README 是会话导入导出工具，autosync 尚列为 coming soon。[当前 README](https://github.com/mem0ai/openmemory/blob/main/README.md) | 不能依据旧 OpenMemory 宣传断言当前仍有维护中的本地自动采集闭环；同名产品与仓库须区分。 |

## 判断与未知

基于这些资料，继续用 Cognee 并收缩 Memlane 职责，有合理依据；当前证据不足以证明换 Mem0 会显著减少整体维护成本。Mem0 的 CRUD/history 形态可能更容易直接映射普通列表详情页，这是接口层面的推断，不是两套完整产品实测结果。

尚未比较：相同真实会话的记忆质量、纠错后跨 Agent 使用、长期稳定性、中文效果、桌面部署成本，以及各版本 API 兼容性。两者都不应仅靠 README 或 benchmark 宣称已满足这些条件。

本次仅形成选型讨论材料，没有启动开发、构建、迁移或删除已有数据。
