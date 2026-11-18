# 自动整理冷却与队列公平性修复

2026-11-18，用户明确要求修复24小时默认冷却及队首会话反复占用的问题。沿用影响分析和连续场景验收方法，范围为现有Backlog调度及官方插件参数；不增加新处理器、不清除原水位、不重投未知结果、不修改项目权限或用户暂停状态。

## 已证问题与验收标准

- `project_backlog.automatic_tick`固定从pending_processing首条queued/deferred开始，处理一次即break。首条处于cooldown时仍留在首位，后面一直queued。
- Backlog补存后的自动整理未明确传入已有的`incremental_cooldown_seconds=300`约定，因此插件采用86400秒默认值。既有`incremental_processing`本身已有300秒约定，不应误述为整个系统都未配置。
- 日常读取：memlane两条会话分别deferred/cooldown与queued，桥空闲；当天只有一个会话派生原始材料文档，没有新增session_learnings。整理完成事件不能替代知识条目生成证据。

验收：首条冷却不阻塞第二条；记录下一次允许尝试时间，重启不丢失且不到期不重复调用；自动补存整理显式使用300秒，与已有增量处理一致；权限、未知结果屏障、原队列及去重保持。先定向回归，再正常加载日常服务，核对旧任务状态是否接续与官方实际结果，分别报告提交、完成和产物。

实现与实际服务验证待本轮完成后补充。历史DEV-STOP01四次失败保留；若本次正常停止失败，继续记录，不以新owner清零。

## 实现与本轮验证

已修复：自动补存整理显式使用既有增量处理的`COOLDOWN_SECONDS=300`，由官方worker使用插件支持的`COGNEE_IMPROVE_COOLDOWN`配置；不修改插件固定源码或用户配置文件。记录每条会话的尝试时间，按最久未尝试排序；cooldown/backoff等待300秒，其他明确未提交的deferred等待30秒，不到期跳过。新增消息合入同一会话时保留等待状态与期限，服务重启也保留；submitting/unknown继续阻断同一会话的自动重投。

原缺陷定向复现：两条会话时旧代码得到`one, one`，且自动整理没有传300秒设置。回归40项通过（7.343秒），包含Backlog、官方worker、增量处理与配置接续；队列用例首轮缺少第二会话的项目绑定，修正夹具后通过，没有放宽真实绑定检查。lint通过。

正常切换：确认桥active_or_waiting=0、官方pending_background_tasks=0、图任务0，先按内核身份收尾本轮UI watcher，再正常dev.py stop。日志明确`Owned service groups exited; daily store released.`，此次未发生DEV-STOP01超时；历史4次失败不重置。正常start恢复同库、同端口、existing-codex修订1，新owner3817；再以现有open --watch-ui附着新runtime，避免旧临时配置引用。

实际接续：沿用原操作`6497a692-21cb-4805-9e14-53af4f2fe6c3`，没有新建操作绕过原状态。两条原队列会话已分别提交：

- `codex_01a1e887-f8af-70d3-9058-b8c05a6130a3`，17:31:59开始尝试，submitted，300秒配置。
- `codex_01a1ea43-2f98-7a91-af12-734a3712ab96`，17:33:25开始尝试，submitted，300秒配置。

第一条提交后，后面的会话已轮到，未再固定检查第一条；官方活动出现实际cognify/memify流程，桥有实际请求。旧20条无原回执记录仍为unconfirmed，未重投。可审阅元数据回执`build/diagnostics/processing-fairness-live-20261118.json`，测试日志`build/diagnostics/processing-fairness-tests-20261118.log`，正常停止日志`build/diagnostics/fairness-stop-20261118.log`。

完成边界：队列与冷却修复已实际加载，两条积压已进入官方处理；本轮核对时今天新增session_learnings仍为0，模型任务还在运行。提交不等于整理完成，也不保证生成新记忆；保留后台继续处理，不将“修复调度”写成“已产出新记忆”。

## 后续产物核对：仍未生成今日记忆

用户追问能否实际产生后重新读取日常官方目录：memlane的session_learnings共112条，createdAt为2026-11-18的条目仍为0。第一条会话的improve日志在17:40:17报告`status=no_gated_entries documents=0`；第二条会话的原始材料图构建在17:45:31完成，不代表后续提炼成功。

发现独立下游错误：SessionQAVector_text.lance引用的数据分片缺失，官方记录`Session QA vector indexing failed open`；17:45:36起多次模型请求返回502／`codex_model_failed`，原生适配器正在按自身策略重试。桥的失败映射目前没有保留远端完整错误信息，尚不能判断502的具体上游原因；不能凭此推断额度耗尽。索引警告为failed-open，也不能未经证明认定它就是本次零记忆的唯一原因。

本轮没有重跑模型探针、修改水位、重建／清空索引或回放旧消息。队列修复的已验证结论保留；完整“今日知识生成已恢复”的验收仍未通过，后续需单独诊断提炼与索引问题，不能把排队测试通过写成端到端产物通过。

## 502根因与协议修复

使用固定官方`CodingAgentContextExtraction` Schema做一次隔离只读探测，直接保留app-server的错误对象。原始错误：HTTP400、`invalid_json_schema`，`/properties/lesson`包含strict输出不支持的`oneOf`。Codex将其放在`codexErrorInfo=other`的JSON message中，桥接只读顶层错误标签，误报通用502。回执`build/diagnostics/codex-502-probe-20261118.json`。这是第1轮真实协议失败；不是已证额度、网络或采样参数问题。

传输层将Pydantic带discriminator的oneOf转换为anyOf并移除discriminator注解，原始Schema不变；模型结果继续同时通过传输Schema和原始oneOf校验，不放宽业务结果接收。无法转换的组合仍拒绝。错误分类只读取provider JSON中的machine code，将invalid_json_schema映射400，不暴露任意provider文本。相同Schema的第2轮隔离真实探测已通过，回执`build/diagnostics/codex-502-probe-fixed-20261118.json`。

之前的Schema夹具测试仅证明转换函数不抛异常，没有证明目标strict协议接受该关键字；旧知识存在／保存与图构建成功，也不能证明这条提炼Schema路径成功。当前尚无完整旧原始请求及响应，不能断言Codex何时改变过规则，也不能把更早知识的实际处理方式作无证据归因。

针对“502为何状态页不知”：新增管理侧按项目读取最近24小时模型错误的只读投影，只扫描既有两份受限大小的结构化日志尾部，不返回正文、密钥或原始异常文本。项目消息状态将展示最近模型调用异常，与保存结果分开；原有pipeline完成事件不足以证明后续提炼成功。当前实现与测试已通过，实际加载及Native核对待后续本轮步骤补充。


## 修复加载、状态展示与受控复验

协议修复与状态展示的Python定向回归57项通过，Go的ProjectQueue／ModelFailure／Message／Unread定向回归通过，lint通过。相同官方提炼Schema真实协议两轮：第1轮暴露不兼容，第2轮修复后返回并通过原始Schema校验；不以本地夹具替代真实协议证据。

正常启动日常服务的新owner为27501，继续使用同一官方库和既有处理配置。管理接口已返回该项目最近调用异常；实际Native项目设置的辅助功能状态明确显示「记忆提炼调用失败」「整理异常」和「最近一次提炼调用返回 HTTP 502，尚不能据此确认记忆已生成」。这是最近24小时历史错误的展示，不自动宣称整个任务当前仍失败，也不把其他成功请求当成这次错误已解决。

本轮重启操作发生一处执行错误：原计划先确认模型空闲再停止，但Python断言发现active_or_waiting=1后，shell仍继续执行下一行正常stop，未正确阻断。不能记录为停止前空闲。正常stop最终返回0并释放日常存储，旧UI watcher随后按身份正常收尾，再正常启动服务及附着UI。没有手动删除所有权记录或重置处理水位。证据为build/diagnostics/schema-stop-20261118.log及schema-start-20261118.log；DEV-STOP01历史4次失败保留。

确认两条原提炼任务已有失败终态且后台容量为0后，18:02通过原官方插件improve_session_via_http对codex_01a1e887-f8af-70d3-9058-b8c05a6130a3做一次受控复验，沿用服务端材料和去重水位，没有重发原消息。提交回执build/diagnostics/schema-official-repair-20261118.json仅证明受理。18:05读取时后台任务仍为1，操作3624f5ff-2d38-495a-9225-b0105b4d7440已出现连续HTTP200实际模型请求，尚未取得整个提炼结束及今日新知识产物证据；不宣称今日知识已经恢复。


## 18:20 返回内容与候选层核对

用户要求区分Codex故障、Cognee故障及合理不生成。只读查询官方cache_session_context：受控复验会话当前有132条今日context候选，按官方置信度>=0.75、净helpful计数>=0规则均通过初筛；分类34条workflow_state、50条failure_lessons、26条success_patterns、21条tool_rules、1条environment_facts。其中部分失败候选由官方确定性错误分组生成，132不能全部称为模型逐条生成，也不能当作132条正式知识。模型产生的tool_rules包括画像不显示版本号、未知提交状态禁止自动重投。四个提取窗口共处理1699条trace，水位已到1699，证明不只是空HTTP200。

截至本轮读取，该操作已取得93次以上有效结构化返回（桥在200前执行JSON及原始Schema校验），有实际非空响应体。后台仍在继续，未见该次distillation完成结果或distill水位，不能将阶段pipeline完成当成全任务完成。现有证据排除模型完全无输出、候选全部被初筛淘汰；尚不能判定最终候选应全部淘汰，也不能把索引分片缺失警告直接认定为零产物根因。最近的SessionQAVector分片缺失为已证独立存储异常，仍待单独修复。元数据回执build/diagnostics/processing-return-evidence-20261118.json，不存候选正文。
