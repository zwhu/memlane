# TraeX 会话保存与记忆提炼核对

用户反馈TraeX会话没有存成记忆。使用diagnosing-bugs的只读端点/日志反馈，检查当前日常库，未重投、未调用模型、未修改采集与后台运行。反馈样本为item-fe最近TraeX会话，2轮问答/2条工具记录。

已证：原始会话保存成功。北京时间22:10:14官方提取2/2轮，22:10:39问答持久化watermark推进至2。22:10:47编码候选提取traces=2/turns=1/inputs=1/error_groups=0/touched=0；distillation返回no_gated_entries/documents=0。对应operation于22:10:48结束，模型HTTP请求成功。不是尚未触发、网络失败或分页漏显。

用户第二轮明确要求将视频提示句保留到记忆，Agent回复已原样保存；但当前存储只有原始问答，没有该会话新增的session_learnings。回复文本不等于真实记忆写入回执。当前官方自动coding提炼先从context候选中过门槛，再交curator/writer；该样本没有符合门槛候选，未进入后续记忆写入。尚无逐候选拒绝理由，不能声称已证明具体哪条语义规则拒绝。本轮仅诊断，不自行改变官方筛选、补写或重跑。

调度器一度显示deferred/cooldown（5分钟），官方插件按会话上次成功提交节流，不能用这个状态推断本次管线未运行；日志证明22:10这次确实运行且0记忆。后续相同counts轮询不代表重新入库。

其他TraeX：今日memlane会话15:08:45历史日志记载completed/documents=1，15:09再查为no_new_entries；并非所有TraeX都不能产记忆。其余13:28启用自动增量前的旧会话进入基线watching，不自动补采/重放。新增项目/旧宿主身份未修改。

证据为官方目录元数据、session history只读数量、incremental状态及官方日志时间线。status.json只含ID/计数/状态，无用户正文。第一次诊断脚本误假定registry有state包装（当前无）已纠正，属于方法错误；文件脚本已清理。

同轮Provider卡片：用户要求Codex不是Provider应移除。settings_model_cards.go只列latest profile.mode=provider，标题“已保存 Provider 配置”；保留当前active的agent_bridge。两张Ollama分别instruct与thinking，非同模型重复。包测通过3.076s，实窗Provider列表只见两张Ollama，顶部当前Codex保留。

补充：memlane的今日TraeX会话15:08:45实际completed/documents=1，并非所有TraeX都不能提炼；旧回合早于增量启用时间（13:28:14）作为基线，不自动回放。item-fe的cooldown是自动插件300秒节流，不是最终无记忆的根因；真正结果是候选0。未保存该用户提示正文到工程证据。

独立UI要求：Codex是agent_bridge，不是Provider；settings_model_cards.go现仅列mode=provider的最新版本，标题“已保存 Provider 配置”。当前桥接配置及active引用保留；实际设置页只出现instruct/thinking两张Ollama配置卡，当前处理仍Codex。包测通过，未删除配置文件或切模型。
