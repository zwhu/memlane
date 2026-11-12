# Cognee 官方插件：采集范围与存储

2026-11-12，只读源码调研，未安装插件、读取用户会话、修改运行配置或实施迁移。插件核对提交 `28fde88150661444a0b8ac69cc81ee0e41ec8f5a`，由远端 main 定位；服务端部分为当日官方文档，不能外推到 Memlane 现有固定 Cognee 版本。

## 结论

用户已确认官方采集优先，并以官方数据为主要数据源。Memlane 应围绕官方接口提供安装管理、浏览和操作适配，减少原文／知识／索引的重复保存。官方采集不是完整聊天归档承诺，不能据此直接实现完整会话回放。

## 采集什么

以 Codex 默认路径为例：UserPromptSubmit 留存用户输入及可用的 cwd、model、turn_id、transcript_path；Stop 取得最终助手回复，与待发输入组成 QAEntry；PostToolUse 记录工具名称、参数、返回内容、状态和错误，作为 TraceEntry。[hooks](https://github.com/topoteretes/cognee-integrations/blob/28fde88150661444a0b8ac69cc81ee0e41ec8f5a/integrations/codex/plugins/cognee/hooks.json)、[输入](https://github.com/topoteretes/cognee-integrations/blob/28fde88150661444a0b8ac69cc81ee0e41ec8f5a/integrations/codex/plugins/cognee/scripts/store-user-prompt.py)、[回复与工具](https://github.com/topoteretes/cognee-integrations/blob/28fde88150661444a0b8ac69cc81ee0e41ec8f5a/integrations/codex/plugins/cognee/scripts/store-to-session.py)

该默认路径中输入上限4000字符，助手正文8000 UTF-8字节，工具每个参数值4000字节、返回8000字节；采集还有开关、工具／敏感路径过滤、脱敏与自调用／低信号过滤。它没有保存每次中间发言、附件与完整原始日志的统一承诺。[采集策略](https://github.com/topoteretes/cognee-integrations/blob/28fde88150661444a0b8ac69cc81ee0e41ec8f5a/integrations/codex/plugins/cognee/scripts/_capture_policy.py)

上游新增可选 `COGNEE_CODING_MEMORY=true`，默认关闭。开启后，原逐工具路径退出，Stop读取宿主transcript，发送尚未发送的工具调用（含失败）和每个已回答请求的输入／最终回复；包含事件时间及QA的prompt_time。其上限为输入8000字符、回答8000字节，每次最多400 trace及50 QA；保留发送身份用于去重。它仍不是完整消息流，不能理解为所有中间消息均已归档。[模式与解析](https://github.com/topoteretes/cognee-integrations/blob/28fde88150661444a0b8ac69cc81ee0e41ec8f5a/integrations/codex/plugins/cognee/scripts/_coding_memory.py)、[Stop交接](https://github.com/topoteretes/cognee-integrations/blob/28fde88150661444a0b8ac69cc81ee0e41ec8f5a/integrations/codex/plugins/cognee/scripts/send-unit.py)

Claude Code也有QA／trace及可选编码记忆路径，但默认输入与回答写入时机和Codex不同，不能假定所有Agent字段、配对及完整性一致。编码记忆模块由两者共享，按Agent选择transcript解析器。

## 存到哪里

1. **Agent自己的会话原文件**：由Codex／Claude保存。不是Cognee数据库，也不是插件完整复制一份。
2. **插件本机状态与待发送材料**：Codex为 `~/.cognee-plugin/codex/`，包括 `pending/`（待配对输入）、`bridge/`（暖机／可重试待发材料）、`sessions/`等。可选编码记忆使用 `coding/` 下状态。它们不是长期知识库。[插件路径与HTTP发送](https://github.com/topoteretes/cognee-integrations/blob/28fde88150661444a0b8ac69cc81ee0e41ec8f5a/integrations/codex/plugins/cognee/scripts/_plugin_common.py)
3. **Cognee服务的会话层**：插件通过 `POST /api/v1/remember/entry` 提交QA／trace，按用户和会话保存。默认SQL缓存，SQLite为关系数据库旁的 `cache.db`，也能配置外部数据库／Redis。当前文档默认TTL为最后写入后约7天，缓存不等于永久保存；部署版本和配置需核对。[Sessions and Caching](https://docs.cognee.ai/core-concepts/sessions-and-caching)
4. **Cognee长期材料与知识**：`improve`把会话材料接入永久dataset并提炼合格经验，知识文档标记为 `session_learnings`；是否生成知识看实际处理结果。由Cognee管理原始数据、关系元数据、向量和图谱，不需要Memlane再复制一套。[Improve](https://docs.cognee.ai/core-concepts/main-operations/improve)

插件自管本地服务默认将系统／数据／缓存根设为 `~/.cognee/system`、`~/.cognee/data`、`~/.cognee/cache`；环境变量可覆盖。Codex与Claude共享该本地服务及存储，只有插件状态分开。连接远端／云端时，服务数据存于所配置服务端，本机仍可保留插件状态／待发缓存，不能说云模式本机完全无数据。默认目录不代表当前Memlane本机实际目录已核查。

## 对阅读与迁移的影响

- 知识页以官方文档和来源为准；对话页呈现官方实际保留的QA与trace，范围不足时说明，不能冒称完整原文。
- 插件助手函数中的 `GET /api/v1/sessions/{id}`用于读取最近QA／trace尾部（注释约20条），不能把该接口一次返回当完整历史。全文分页／顺序／来源／编辑与版本接口仍需单独核对。
- App侧仅保留必要状态与关联；官方缓存会过期，不因“想永久保留所有聊天”自动增建独立档案库。额外归档需求需单独明确。
- 既有Memlane独立原始记录、知识版本和索引为已存在实现；尚未执行迁移，用户数据与历史回执保留。

核验限制：此次核对源码和文档，没有运行真实官方插件验证；未宣称全部Agent都有插件，也未查明当前本机官方服务的实际数据目录。
