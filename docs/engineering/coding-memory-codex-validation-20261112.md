# Cognee coding-memory：Codex 隔离验证

2026-11-12。用户授权：正常合成编码 → 官方自动采集 → 整理生成知识 → 新会话召回，核对知识质量、出处和自动替换与 App 确认规则的冲突。执行中用户取消 Token 相关测试，不再以 Token 消耗作为验收项。

## 结论

**官方自动采集、实际知识生成和新会话注入均已观察到，但本样本的完整知识质量验收未通过；官方自动替换确实违反现有 App 的确认规则。不能据此开启正式迁移或宣布 coding-memory 已满足产品要求。**

- 正常 Codex 编码实际复现库存超卖，完成修复并通过 7 项测试；官方插件自动保存 1 QA / 8 trace，包含真实失败轨迹，无人工提交记忆。
- 官方整理最终只生成“7 项测试通过”的状态知识。空仓库新会话自动收到该知识，0 工具执行；函数契约和并发问题均回答 unknown。
- 再进行一次正常需求变更后，9 项测试通过，官方自动保存 1 QA / 6 trace，生成两篇状态知识。第二个盲会话召回了测试基线与部分校验／回滚规则，仍缺原并发问题、原子更新修复，以及无效输入返回 False 的精确约定。
- 同一 `test-baseline` 的旧知识被官方直接删除，旧正文 API 返回 404、旧文件消失，没有 App 确认步骤。旧正文和前后元数据已提前保存在独立证据目录。

本报告没有修改产品规则或实施新的采集／知识／确认系统，也没有为了让验收通过而补写知识。

## 固定输入与隔离

| 项目 | 本轮实际输入 |
| --- | --- |
| Codex | 本机 CLI 0.167.1；沿用用户选择的 gpt-6.1-sol / medium |
| 官方插件 | 1.8.0；上游 main 固定 `a176248914fdb32a78354854b300174203b15a5c` |
| 官方服务源码 | PR #6568 head 固定 `ec6f40859e5f2df9e7862dde27d580ac3268ed43`；核对时仍 Draft，不能当作已发布能力 |
| Python 依赖 | 复用现有 Cognee 1.6.7 环境的依赖；通过专用源码路径加载上述 Draft，不覆盖已安装 SDK |
| 提炼方式 | 临时、仅用于验收的 Codex structured-output 适配器；SDK 提示、提炼、筛选和删除逻辑保持官方源码。不是 Memlane 正式 Codex 网关验收 |
| 独立服务 | `127.0.0.1:18171`；专用 HOME、数据库、数据和日志目录；`DISTILLATION_MODE=coding` |
| Dataset | `coding_memory_codex_20261112`；ID `bca400f0-9674-51a2-aefc-8fa8d6257ce4` |
| 项目范围 | `coding-memory-synthetic-inventory`，只含合成材料 |
| 私有工作目录 | `.local/coding-memory-codex-20261112` |
| 证据目录 | `build/coding-memory-codex-20261112` |

官方 CLI marketplace add / plugin add 实际完成。通过隔离 TUI 的 Folder access 和 Hooks review 正常信任流程启用 9 个新官方 hook，未用 hook trust bypass。之后的业务会话使用正常 `codex exec`。本轮信任仅存在于任务私有 Codex 目录，不证明用户日常配置已安装或信任。

为复用已有登录，只在私有宿主与处理目录各建立一份临时 auth 文件拷贝，未符号链接回原文件；提炼目录不安装采集插件，防止循环采集。用户 `~/.codex/config.toml` 与 `auth.json` 前后 SHA256 相同，拷贝已在收尾删除。未读取用户历史会话、修改日常插件／模型配置，未打开 App、修改主 session 的 UI 文件或替换正式运行依赖。

源码、压缩包和小型合同摘录的摘要见 `versions.json`、`source-contract/sha256.json`。官方实现参见 [插件](https://github.com/topoteretes/cognee-integrations/tree/a176248914fdb32a78354854b300174203b15a5c/integrations/codex)、[服务端 PR](https://github.com/topoteretes/cognee/pull/6568)、[固定替换实现](https://github.com/topoteretes/cognee/blob/ec6f40859e5f2df9e7862dde27d580ac3268ed43/cognee/modules/session_distillation/lesson_store.py)。

## 连续业务与真实结果

### 第一轮：正常纠错、采集与提炼

合成仓库 Blue Heron 最初先 SELECT stock 再无条件扣减。Codex 自行编写并执行 unittest，先观察到 7 项测试中 failures=6 / errors=3，其中并发断言为两名买家均成功、stock=-1。修复后使用条件 UPDATE、检查受影响行数；调用者管理提交／回滚，无效数量（含 bool）抛 ValueError，未知 SKU／不足返回 False。实际 7 项测试通过。

业务 session：`codex_01a1ce95-b9b8-76b0-80c4-482f33b36cfb`。官方 Stop 从宿主 transcript 自动捕获 1 QA / 8 trace，8 条分别为 6 Bash、1 Write、1 Edit；其中一条 Bash 为 error。没有调用 remember/entry 或 memory_save 人工构造来源。`coding_unit_drained` 显示 drained=9 / remaining=0。服务读取同样为 1 QA / 8 trace。

插件会话退出自动发起的首次 improve 因临时模型适配错误失败。修正适配后，对同一已采集 session 明确提交一次前台 improve，未重新提交 QA／trace：

- feedback_weights、persist_session_qa、persist_agent_traces、extract_agent_context、distill_sessions 完成。
- 官方 stage 回执记录 extract_agent_context lessons=4，distill_sessions documents=1。
- 实际官方文档共 3 篇：QA 材料、trace 材料、1 篇带 session_learnings 元数据的知识。图谱／原始材料不另计为可复用知识。
- 单篇知识正文只保留 `python -m unittest -v` 的 7 项通过、0 失败，`lesson_type=state`、`state_key=test-baseline`。
- 中间 CodingAgentContextExtraction 实际抽出并发失败修复、调用者事务和测试状态；CodingCuratorBatchOutput 也提出契约候选。后续 CodingWrittenLesson 对契约候选返回 `accept=false, reason=unsupported`。这是已观察的筛选结果；为何拒绝、是否由模型／提示／分类规则造成尚未证实。

证据：`coding-events.jsonl`、`official-plugin-hook.log`、`improve-002/response.json`、`round-001`、`round-002`、`model-evidence`。保留失败请求和部分入库材料，未删库重跑。

### 第一轮盲召回

新 session `01a1ce9b-8f87-76d1-923c-de33743e99cc` 在另一个空 Git 仓库执行。通过官方项目 node-set 固定到同一授权合成范围；目录中没有 inventory 源码。提示仅问 Blue Heron 契约、复现问题与测试证据，禁止工具／文件读取，不提供答案；`COGNEE_CAPTURE=false` 防止评估对话进入知识。

宿主原始 transcript 中 `hooks.additional_context` 确认官方实际注入了“7 tests passed”知识；模型 0 工具执行，正确回答测试状态，并明确契约和并发问题未知。不是把测试脚本直接输出给模型作为答案，也不是仅检查检索接口非空。

### 第二轮：正常需求变更与替换

另一正常编码 session 将无效数量行为由 ValueError 改成 False，增加调用者事务保护和两次预留后整体回滚的测试；实际 9 项测试通过。session 为 `codex_01a1ce9c-2a72-7983-8a63-c1f6f488a656`。

官方自动捕获 1 QA / 6 trace（4 Bash、2 Edit；一次宿主文件修改涉及两个文件），退出自动 improve 完成。此轮没有人工发起补写或人工插入 State-Key。官方实际新增两篇知识：

1. `footprint:reservation-validation`：拒绝无效数量（含 bool），不改变库存和调用者事务；有连续两次预留整体回滚的覆盖。
2. `test-baseline`：9 项测试通过，0 失败。

第二次盲 session `01a1ce9f-2aa4-70e1-902e-9e8c3964dcd3` 同样 0 工具执行，自动收到这两条知识。召回了部分规则和当前 9 项基线，但不能回答原并发问题、条件 UPDATE 修复及完整契约；“拒绝”仍不明确等于返回 False。第一轮质量缺口未因换 session 清零，最终仍不能判定完整质量通过。

最终官方数据为 2 个有材料的业务会话、6 篇当前文档，其中 2 篇 session_learnings。两个盲召回没有新增 QA／trace。证据：`evolve-events.jsonl`、`round-003`、`recall-001-events.jsonl`、`recall-002-events.jsonl` 及各 `*-injected-memory.json`。

## 出处质量

官方知识元数据包含 dataset、session_id、session_date、git_commit、git_branch、files、project_node_sets。可从知识回到已保存的对应会话材料；本轮也独立导出官方正文及会话读取结果。

限制已经实测：

- 空仓库盲召回的注入块只给 dataset、早前 session 数量、知识正文，未给具体 session ID／commit／精确 QA 或 trace ID。正常同仓编码的新会话块会显示 session 日期、短 commit 和 recent commits；两种实际注入均已封存，不笼统声称所有召回都含完整出处。
- 元数据 git_commit 是创建合成基线的 `d7a435c…`；修复与第二次变更均未提交。该 commit 不能证明生成知识时的最终代码，需结合宿主工具轨迹和快照核对。
- files 是知识涉及文件的列表，不是版本化文件摘录；session 关联也不等于精确断言出处。
- 当前 sessions API 返回的状态仍为 running、ended_at=null，即使宿主已退出、插件 unregister 成功；不能将此字段直接用作宿主存活／任务完成的依据。

## 自动替换与确认规则：实际冲突

旧知识 ID `efb6ee2d-ff25-5285-829e-bce96c6403cf`，正文为 7 项通过；第二轮新知识 ID `3485ffc2-6a46-56b0-95c0-6c8044a6da5c`，正文为 9 项通过。

官方日志在 07:17:51 UTC 记录 `replaced 1 older lesson(s) with state key 'test-baseline'`。前后官方列表确认旧 ID 消失、新 ID 存在；旧 ID raw API 返回 404；旧原始正文文件不存在。证据：`round-002`、`round-003`、`superseded-readback.json`。

固定源码 `replace_superseded_lessons` 在新知识存入后自行调用 `datasets.delete_data`，没有 App 审批入口；按同 dataset、项目 node-set、state_key 决定替换。两次 session_date 均为同日，当前规则在没有更晚日期的其他行时保留刚存入的行。插件也明确要求此路径具备 delete 权限。

这与仓库产品规则“AI 提出的合并、覆盖、删除需确认，确认前不改变已有有效知识”冲突。事实不是仅更新客户端显示：官方原知识行和正文已删除。旧 QA／trace 仍保留，但这不能代替知识逐版全文历史／恢复。本轮保存的验收旧正文不是新增的产品历史镜像库。

待产品决策：是否允许某类 coding-memory 状态知识自动替换，或继续坚持现有规则并要求官方具备预览／确认／保留旧版本的能力。在决策前，不把本次 Draft 模式默认打开，不静默放宽规则，也不自动扩建第二套知识库。本报告不要求用户提前批准尚未准备的正式迁移。

## 调用次数与耗时

用户已取消 Token 测试，本轮没有 Token 门槛、对比或优化；CLI 自动产生的 usage 只随原始回执保留，不用于结论。

| 阶段 | 实际次数 | 已观察耗时 |
| --- | --- | --- |
| 第一次正常编码 | 1 宿主 turn；6 条完成的 shell command、2 次 file change | 112.57 s，含初始服务缓慢导致的 hook 等待 |
| 首次自动 improve | 2 次临时 Codex 处理执行：连接探测成功、图谱 schema 请求拒绝 | 单次适配执行 11.01 / 4.43 s；不是一次完整成功整理 |
| 修正后同材料前台 improve | 10 次 Codex 处理执行，均成功且无工具执行 | 整体 137.41 s |
| 第一次盲召回 | 1 新 turn，0 工具 | 22.36 s |
| 正常需求变更 | 1 新 turn；4 条完成的 shell command、1 次 file change（涉及两个文件） | 45.96 s |
| 第二次自动 improve | 10 次 Codex 处理执行，均成功且无工具执行 | 从插件发起至官方 distill 完成约 127.5 s，按日志时间观察 |
| 第二次盲召回 | 1 新 turn，0 工具 | 18.15 s |

合计 22 次提炼适配执行，21 成功、1 schema 拒绝；另有 4 次业务／召回宿主 turn。普通编码 turn 内会多次请求模型，未额外代理供应商网络，**不能把 turn 数说成供应商 API 请求总数**。独立服务日志共观察到 135 个完成响应，包含准备、官方插件和只读审计；不代表全部网络尝试或纯业务请求数。详细按路由分组见 `summary.json`。

## 失败计数和边界

| 问题 | 本轮计数与处置 |
| --- | --- |
| 源码包不含锁定的 Ladybug JSON 二进制 | 准备失败 1 轮。仅复制现有锁定官方扩展到独立源码依赖目录，未改官方逻辑；服务随后恢复，无清库 |
| 临时 Codex Schema 转换 | 1 轮 improve 失败。缺少 strict 所需 additionalProperties=false；递归收紧传输 schema、仍用原 Pydantic 校验返回值，明确新 improve 后通过 |
| 完整契约／并发纠错不能从知识召回 | 两个业务状态下各观察一次不完整结果，累计 2 轮，未修提炼算法／换模型反复试到通过；保留原失败，当前未通过 |
| 自动删除与确认规则冲突 | 合成业务变更场景 1 次实际观察，已证规则冲突，未实施产品修复 |

另有 GitHub API 查询限流，改用 git ls-remote 固定官方提交，属准备问题，不计知识失败。插件 `improve_fired ok=true` 是后台提交被接受的回执，首次随后仍在服务端失败；本轮坚持检查真实阶段及产物，不用插件退出0替代知识验收。

既有旧自写 Codex hook 桥接已达到停止阈值的问题没有运行；原 SDK／helper／图谱等历史失败未删除、未清零、未用当前 Draft 的结果宣称修复。没有运行会触发停止问题的全仓验收，没有构建完整包。

## 收尾与交接

实际停止本任务服务 45543／59778，两个 PID 均不存在，18171 不再监听；未留活动编码或提炼进程。日常 76077／76867 仍运行。用户 auth／config SHA 复核一致。

已删除任务临时凭据拷贝、测试 API key 文件、重复插件／上游源码、协议生成物及宿主运行缓存。保留约十余 MiB 的合成官方库、新会话原件和小型证据；没有删除用户历史数据、其他 session 构建物或旧失败证据。具体收尾见 `cleanup.json`。脚本经过 `scripts/task.py` 磁盘门禁执行，结束余量约 127 GiB。

报告与证据交给主 session `01a1cbe2-ce07-7231-8d6f-eb210b7a6cf0`。本轮完成的是隔离验证与问题报告；日常自动采集迁移、正式配置安装、App 审批适配和完整产品交付均未由本轮完成。

## 用户追问后的只读深入分析

用户进一步要求核对草案动机及 issue 来源。本节没有恢复服务、调用模型或重跑验收，不增加质量验收轮次。完整来源追溯另见 [上游来源研究](../research-coding-memory-origins-20261112.md)。

先纠正推断范围：本轮两个合成业务状态的知识不完整，不能推出“coding-memory 没有价值”或“官方没必要提出此方案”。本轮没有默认模式对照、没有同任务有／无记忆的后续编码比较；第一轮提示还明确给出了条件 UPDATE 修复方向，因此不是对自主发现修复或降低重复犯错的完整测量。当前通过的是来源链路的若干环节，未通过的是这个样本的知识保留标准。Draft 身份本身也不构成技术失败原因。

### 可定位的证据传递断层

只读查询本次独立 `cache.db` 的 `cache_session_context`，得到 curator 引用的两个实际 member；摘录保存于 `writer-support-readonly-analysis.json`。两条分别是：

- `be157bbd-e09c-4531-a4ce-cc01e6e7052e`：先读再扣导致负库存 → 使用条件 UPDATE 与受影响行数。
- `396d6a75-f7a6-4d0a-83ad-3d02b10e7895`：事务提交／回滚由调用者负责。

两者都有 source_qa_ids，第一条还有 source_trace_ids。但它们的正文没有“未知 SKU／不足返回 False”或“正整数且排除 bool”的约定。

本轮真实模型输出 `call-zr1lrn5n/final.json` 将这些候选汇总为一个 footprint，额外包含了 False、正整数、bool 等原始问答中的细节。固定源码随后调用 `build_writer_input`，只传 proposed lesson、proposed state key、member 正文、相似旧知识、glossary 与 style，不传 member 所关联的 QA 或 trace 正文。`call-2pc73kdl/final.json` 的真实结果为 `unsupported`。

官方 [curator 提示](https://github.com/topoteretes/cognee/blob/ec6f40859e5f2df9e7862dde27d580ac3268ed43/cognee/infrastructure/llm/prompts/coding/session_distillation_curator_system.txt) 允许候选使用原始对话作为依据，甚至允许只来自对话时 member_entry_ids 为空；[writer 提示](https://github.com/topoteretes/cognee/blob/ec6f40859e5f2df9e7862dde27d580ac3268ed43/cognee/infrastructure/llm/prompts/coding/session_distillation_writer_system.txt) 却要求 MEMBER ENTRIES 支持整个 statement，否则 unsupported。这形成了已证的输入／校验边界不一致：上游允许使用的证据没有完整传给下游审核。

**强假设**：汇总扩大了陈述范围，审核只看到较窄证据，进而整条拒绝，把原本有证据支持的并发纠错和事务知识也一起丢掉。此机制与实际输入、提示和返回一致，但当时未保存完整 writer 请求，模型的拒绝也没有逐项解释；因此不能称已证明唯一根因。无需“换一个更强模型”即可指出这处需要核对的工程问题，也尚未授权／执行生产修复。

### 自动替换需要分开判断

7 tests → 9 tests 是当前状态更新；旧状态不应继续被新编码会话当成当前事实。因此上游使用同键替换有清楚的产品动机，并非仅凭本轮删除就能认定设计错误。它与 Memlane 现行审批／历史要求不兼容，是两套产品语义的差异；“状态需要更新”与“旧知识是否应物理删除、是否需确认、是否保留版本”仍是不同决策。现有冲突证据和暂不能直接迁移的结论保留。
