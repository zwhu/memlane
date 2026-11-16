# 当日知识零产出核对

2026-11-15，用户问“今天没有产生任何知识是什么原因”。本轮只读核对，不新建模型任务、不重提失败improve、不重启。

## 已证事实

官方所有可见dataset的Data目录按与客户端一致的externalMetadata.node_set=session_learnings及createdAt核对：共10条知识，今天北京时间新增0。首次审计脚本误用了metadata/created_at字段而读成总计0，核对DTO后纠正为10／今天0；此为方法错误，不是产品数据丢失，也不将第一次统计作为证据。

今天有6条improve operation：4失败、2成功。近期自动提交的3条均失败，更早一条失败只保留其通用错误，本轮未证明它与最近3条同根因。

| operation | 北京时间 | 结果 |
| --- | --- | --- |
| a11b578b-8b00-457d-8a00-a95e7b829a3c | 11:47:41—11:52:05 | failed / CogneeSystemError |
| 23d57139-d846-41e9-855b-eede9abce886 | 11:49:04—11:56:19 | failed / CogneeSystemError |
| 46fdae5a-34c9-4821-9b23-01de149a6e3a | 11:50:21—12:00:44 | failed / CogneeSystemError |

日志确认近期失败链：`429 codex_busy` → LiteLLM RateLimitError → cognify_session失败 → fatal persist_session_qa停止improve。知识蒸馏阶段未执行，不能解释为“没有值得记的内容”。原始会话缓存已保存与入知识图／蒸馏成功是不同阶段。

`engine/python/codex_completion_endpoint.py`的complete入口在admitted达到max_inflight时主动返回codex_busy／429。实际health为max_concurrency2、max_inflight32；说明是本机桥接准入队列到上限。核对时active_or_waiting0不代表失败时也为0，亦不证明历史请求、子进程事实。并未由此认定订阅供应商额度用尽。

多个operation起止时间重叠，但官方improve包含dataset/session锁和等待；不能仅据时间重叠断言三个任务的LLM调用同时执行，或把自动提交三个任务写成唯一已证根因。单个管线内部并发、请求等待／取消与重试的贡献仍需用定向场景区分。

两次成功任务中dcim保存验证只产生原始材料，目录未有知识；其0 token统计只按官方记录陈述，不单凭它推广“没有任何模型调用”。[完整只读摘要](evidence/today-knowledge-zero-20261115/audit.json)。

## 当前边界与后续

- 最近3个自动整理的submitted只表示POST被接受，实际已经失败，不能继续报告知识整理成功。本次确认了终态追踪缺口。
- 首批349条保存已确认；后续新批次又保存12条后因policy_unconfirmed暂停，仍有105条待保存。旧20条未知项保留，不能将后续暂停掩盖为“积压全部清空”。本轮未证明该权限核对问题与codex_busy同根因。
- 接下来应先核对并发准入、等待／取消和终态跟踪，再决定同一批材料的恢复；不通过换模型、换库或换任务号掩盖失败。
- 4条失败operation是实际运行结果，不自动等同4轮同一问题验收；沿原具体问题记录核对计数。本轮只读审计无新验收重跑，旧GLiNER／模型质量等不同问题次数不重置。
