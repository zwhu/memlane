# P3-R pi RPC 后台契约

2026-11-08，实施中，未通过真实验收。固定实际pi0.84.1，依据已安装docs/rpc.md、dist/modes/rpc/rpc-mode.js及rpc-types.d.ts；旧print/json和日常Extension通过不能替代RPC生命周期。traex继续为默认整理执行者，pi仅在实际适配并验收后成为可明确选择的执行者。

## 输入、权限与结果

复用已有冻结OrganizationSnapshot、严格JSON Schema/来源/版本/范围验证、持久任务/attempt/租约和短事务应用，不给pi生产数据库、日常记忆工具或批准工具。后台pi只接收宿主冻结材料与输出schema；禁用全部工具及用户Extension发现、context files、skills、prompt templates、themes和发现，禁止隐式读取真实项目/聊天。工作目录、Agent目录、会话和日志使用独立临时目录；不把项目根作为pi的cwd。禁止会话恢复，关闭pi内自动重试，次数由持久attempt预算决定。

沿用pi自身认证和模型：Agent自行读取auth.json链接，models.json使用已校验的私有只读副本，Agent不会重新读取可变原文件绕过命令值检查；宿主不提取或向Cognee/worker/结果日志转交凭据；只读当前defaultProvider/defaultModel标量选择。显式选项优先，未配置或实际不可用时明确失败，不自动换模型/升级Agent。自定义provider只有凭据无提取、配置副作用明确且实际验收后才支持。requested与get_state解析后的rpc_resolved_model/provider分开记录；后者是Agent会话模型描述，不声称上游实际推理模型已报告。解析ID在prompt前检查，不能先调用不同模型再拒绝结果。

## RPC状态与取消

stdin/stdout使用严格LF JSONL，CRLF仅移除行末CR，U+2028/U+2029是JSON字符串内容。每个命令有独立id；get_state建立本次实际模型状态，prompt response success=true仅为接受，agent_end仅是底层一轮结束，willRetry必须为false；还需agent_settled（无续跑/压缩/重试）和有效assistant stop，再查询空闲get_state才是终态。单次只发一个prompt，不用steer/followup；意外工具事件、拒绝、错误、截断、重复/不对应回执、EOF或没有终态均不得提交结果。只从已完成assistant文本解析JSON，仍通过原Snapshot.Decode，文本传输只接受裸JSON，或唯一完整的```json代码块（也允许无语言标签的同类JSON块），块外只能有空白；禁止说明文字、多个块、其他语言标签、额外字段、伪造来源或批准。包装由适配器严格移除，原Snapshot.Decode仍只收到JSON并执行同一schema/来源/版本校验，不渲染Markdown、不解析命令。正常模型提示仍要求裸JSON；兼容包装作为真实0.84.1文本传输的工程收口记录。

取消先发送独立abort id，有限等待其response和当前operation的agent_settled收尾，再关闭管道并清理独立进程组；缺abort回执、子进程不退出、服务/owner终止都需有界强制清理且任务不能成功。取消优先于迟到正常结果；只有reaped/group gone且结果有效才交给Stage，旧attempt/owner仍由受信服务栅栏拒绝。复用guard控制FD，先登记持久PID再放行，父服务死亡不能遗留模型进程。

事件单条1MiB/总8MiB，最终结构化结果256KiB；单次2分钟，启动/退出/abort等待有上限。网络/模型/RPC等待不持SQLite写事务。临时目录只包含必要路径、schema和合成/授权材料，交接只引用证据，不复制完整模型输入。

## 验收

先用明确协议故障夹具覆盖接受后失败、EOF、工具调用、错误id/重复回执、Unicode分隔、abort无回执、stdout后代持pipe、guard与取消/迟到栅栏，再用真实pi0.84.1正常建议和活动取消，服务/真实Rust链路与App计划执行另核对。只在对应版本/渠道真实正常及取消通过后标支持，不能用夹具或print通过替代。受影响tests/vet/race、review、包与文档/handoff/提交后继续G/F，A1b与A2b外部条件保持独立未通过。

实际Ollama/openai-completions路线使用固定pi0.84.1的官方before_provider_request钩子，唯一宿主Extension只把已有OutputSchema传给provider的response_format，提供原生JSON采样约束，不注册任何工具，不记录payload/headers或赋予写库/批准。模型正文及来源仍经原Snapshot.Decode验证，采样形式正确不能代替来源/版本检查。仅在实际接口验收后标该provider路线通过。

本机实际发现pi内置隐藏llama.cpp Extension（get_commands仅含其llama与隔离探针命令）；不把--no-extensions描述为禁用所有pi内置模块。模型工具名单仍为空，显式加载的Memlane Extension不注册工具，只绑定采样schema。

0.84.1实际将宿主Extension的console/stdout元信息重定向到stderr；只解析带本次schema SHA的绑定/HTTP状态小记录，读完进程再核对一次，不保存payload/headers。当前默认qwen3:0.6b的真实候选虽受采样约束仍出现空必需title、metadata偏差及pnpm→pmirror误述，正常产出不通过；不能用原生采样或协议正常代替质量/来源校验。真实message_update取消、abort确认、settled与进程组回收已通过，证据real-cancel.json；App/服务/真实Rust仍未验收。
