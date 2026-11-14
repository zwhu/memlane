# Ollama历史错误被写成现状：第4轮失败停止报告

2026-11-14，MA-OLLAMA-CODING-QUALITY01，累计失败4轮，当前停止该问题及依赖它的质量／采用实验；不启动第5轮。依据仓库AGENTS.md及[工程停止规则](workflow.md)。本报告不把产品设计错误作为已证结论，不将其他Agent／Provider或整个Goal一并判为4次失败。

## 原目标与验收标准

普通pi编码会话在合成仓库复现并修复`retry_config.retry_limit`，固定四项测试独立通过：零值禁用重试、None默认3、正数保持、负数抛ValueError。官方处理应产生与最终源码一致的知识；新会话在不读源码、不调用工具的情况下，使用实际召回知识给出这些语义和真实测试基线。旧错误可作为明确的历史陷阱保留，但不能写成当前行为或与正确现状冲突。普通输出／Schema／固定小样本通过不替代这项验收。

## 失败轮次与证据

| 轮次 | 版本与方法 | 结果及证据 |
| --- | --- | --- |
| 1 | Qwen3 4B Instruct 2507 q4_K_M，配置摘要ad557900…；真实编码→官方处理→新pi会话 | 知识将旧错误写成结果，与正确规则冲突，新会话拒绝给出权威当前规则。[检查点45](evidence/multi-agent-development-20261114/native-agent-binding-ollama-startup.json) |
| 2 | 同Instruct／固定官方服务ec6f408…，原QA及全部5条已门控候选，隔离原curator／writer | curator把旧错误写成现状，writer接受并与正确规则并存；未落库。1次curator及4次writer属于同一轮，不按请求数计轮。[检查点51](evidence/multi-agent-development-20261114/ollama-original-quality-isolation.json) |
| 3 | Qwen3 4B Thinking 2507 q4_K_M，revision3摘要33fa1d7a…；温度0、上下文8192、输出8192，原QA与唯一候选的1648字符批次 | 单次curator在252.73秒正常返回2条proposal，再次声明当前函数的truthiness使0变3，仍称需要显式检查；最终源码早已修复。未调用writer或落库。[实际结果](evidence/multi-agent-development-20261114/thinking-curator-boundaries/curator-8k.json) |
| 4 | 同Thinking及原输入；保留温度0，只在隔离请求中先将上下文增至16384，再将输出上限8192增至14336；消息摘要逐字节一致 | 最后单次curator375.76秒正常结束，finish_reason=stop，输入1334／输出10844 token；3条proposal中再次出现与第3轮相同的旧错误现状表述。没有上下文滑移或输出截断来解释本轮结果；未调用writer、落库或采用。[最终结果](evidence/multi-agent-development-20261114/thinking-curator-boundaries/curator-16k-output14k-final.json) |

两次Thinking实验之间另有一次“上下文16384、输出8192”的单变量检查：244.59秒后输出耗尽，被生产CompletionObservation拒绝为completion_truncated，没有完整语义答案，故不计作本问题第4次失败。[截断回执](evidence/multi-agent-development-20261114/thinking-curator-boundaries/curator-16k-output8k.json)。原完整Thinking流程的箭头措辞含糊，未把同一正文的反复读取或未证实的现状矛盾追加到这4轮中。

## 已尝试与已排除的解释

- 原真实编码和独立测试均通过，问题在记忆生成链路，不能用修复源码测试通过覆盖。
- 固定官方prompt已有FINAL STATE ONLY要求；未降低样本、改写官方prompt／SDK或手工修正知识来制造通过。
- 原问题460字符、回答946字符，低于各1200字符上限；重建的官方问答块包含全部四项语义。原缓存只有一条150字符的零值／默认值陷阱候选，但官方curator同时收到完整QA。[输入边界](evidence/multi-agent-development-20261114/thinking-improve-final-gate/turn-boundary.json)、[原候选](evidence/multi-agent-development-20261114/thinking-curator-boundaries/original-candidate-boundary.json)。
- 第3轮独立请求确有8K上下文滑移；扩大上下文后先暴露输出截断，再增加输出取得完整答案，旧错误仍存在。因此容量不足并非本次现状误写的充分解释，也未证明与所有历史结果无关。
- 已隔离到curator输出即可出现该错误，不能归因为召回器凭空改写；本轮没有调用writer，不能声称已证明writer会接受这份新proposal。

尚未证实的原因包括：所选4B模型对时序和最终状态指令的遵循、陷阱候选带来的锚定、官方“保留可复用陷阱／忽略代码直接可见事实”与本案例期望的相互影响。未取得原全流程全部中间响应，不能宣称已证明唯一根因或某项产品设计必然错误。

## 影响、计数与保留数据

Ollama的配置、Schema验证、Native保存／启用、正常启动和官方材料处理仍有各自证据；本模型完整知识质量及新会话采用未通过，不能宣传为已验收的日常知识处理模型。DeepSeek及已通过的Agent链路不由此撤销通过。

独立MA-OLLAMA-KNOWLEDGE-COVERAGE01累计3轮：原完整正文／实际召回遗漏正数与负数规则1轮，加本次两次取得完整proposal的curator实验各1轮。MA-OLLAMA-CURATOR-TRUNCATION01保持1轮；无完整答案的截断不重复记为语义／覆盖失败。原处理超时等其他缺陷保留原计数。

原Instruct及Thinking权重、失败问答／工具记录、原生成知识、主体／绑定／私有配置／验证回执均保留，没有自动补采或重放。第4轮exec24052终态0、PID44549已退出；自有Ollama api/ps加载模型数0，既有dev服务／Native watcher及其依赖保留供独立工作使用。仅核对这些已知消费者，不对未知进程或全部历史job作归零声明。[收尾回执](evidence/multi-agent-development-20261114/thinking-curator-boundaries/final-stop-safety.json)。

## 方案疑点与待用户方向

固定小样本验证目前证明协议及限定样本可用，不足以保证完整coding蒸馏正确。后续需决定是否调整验证标准／处理方案，或在明确指定的模型及资源条件下恢复；不能自动降低四项语义、手动修正知识或继续换模型重试。

在用户明确方向前，不再针对MA-OLLAMA-CODING-QUALITY01进行诊断实验、参数修改、模型替换、writer检查或完整链重跑；原4轮计数保留。可继续推进不依赖该问题的工作。剩余独立门槛为原生OpenAI／Anthropic／Gemini可用私有配置、普通Codex Desktop新会话、Native积压按钮及日常采用／冻结交付；仍按既有用户暂缓边界执行，不追加Cursor、Claude或未安装Agent验证。
