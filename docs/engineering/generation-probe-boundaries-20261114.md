# 生成模型验证的完成边界与错误回执

2026-11-14。LP01／LP04的隔离候选验证补齐；没有修改固定官方服务、替换供应商SDK或为日常任务新增模型代理。新版样本版本nested-unicode-null-3，事实和普通文本指令不变，新增完成状态与请求模式证据。

## 已补缺口

源码审阅确认：固定native adapter先解析正文，未在该路径检查finish_reason；即使JSON完整，length标记也不能作为完整回答通过。旧探针固定写structured_mode=unknown，错误只区分401／403／429。新检查第一轮即通过，没有新增真实验收失败次数；不重计旧E09、MA-FIXTURE-PLAIN01或停止样本。

隔离探针临时观察官方adapter调用LiteLLM的请求和返回，仅保存模式与次数；finally恢复函数。请求前核对model、endpoint、key仍为当前client，拒绝变化目标和配置中的fallback。记录的Schema／非严格Schema／JSON对象是**官方adapter实际请求形状**，不是远端原生严格Schema保证；未知模型仍走官方JSON路径，不伪造能力目录。Schema在同一模型内降级沿官方实现，区别于跨模型／Provider回退。

普通与结构化样本均要求stop；length、refusal、content_filter明确未通过，缺完成标记／缺choices保留未知。结构校验与固定事实质量分别返回。只输出固定错误码，不输出异常文本、供应商正文、密钥或原回答。401／403、额度／限流、拒绝、截断、Schema校验、不支持参数／结构、事实错误可区分；供应商超时／连接和本机验证截止保留result_unknown。

仅在探针中，将官方公有tenacity retry_with设为一次外层attempt，SDK瞬态重试为0；官方JSON自纠错3次和同模型Schema降级保留。日常引擎的重试和任务处理完全不变。管理侧仍拥有探针进程组、120秒截止、取消和退出确认；截止标记validation_timeout，取消保持validation_interrupted。未知不自动补投，不以超时推断远端未运行或零消耗。

## 最新验证门禁

源码同时发现“任一历史成功即可启用”的缺口。新验证以持久账本revision登记registered_revision，不使用operation ID、文件键顺序或完成先后猜测顺序。管理API选择next、正常启动候选和Native按钮统一要求该配置摘要的最新记录成功；更新的queued／running／failed／unknown均撤下旧成功资格。旧记录没有顺序且相互冲突时保守拒绝，需显式新验证；没有自动模型调用。

不清除已有next或自动停止active。启动遇到无有效最新验证的next，沿原入口保留next及错误，继续原active；更早的成功记录仍保留。持久化并重开检查已覆盖旧成功、较新未知／失败、重新明确通过；原当前配置不受草稿／验证变化影响。

## 证据和未完成

- 固定Cognee＋LiteLLM＋真实loopback兼容HTTP（合成服务，不是供应商模型）：Schema、未知模型JSON模式、严格→非严格→JSON同模型降级；401／429各只收到1次请求；拒绝、有效JSON却length、不符Schema、事实错误、不支持格式、真实HTTP超时；变化model／endpoint／key在发送前拒绝。JSON自纠错仍为原3次。
- 真实现有DeepSeek兼容配置独立验证：普通、嵌套中文／数组／null与精确事实通过，13.881秒、2次adapter调用，实际模式json_object。原active未改；所属组退出，私有验证key副本与一次性脚本已清理。没有新内容服务或日常库任务。
- generation相关Python31项通过；Go全量测试、vet、Generation／Integration race通过。Native用1200×820组件检查确认错误原因／模式和未知保护；新增文案后定向检查通过，**实际Native窗口仍待集中验收**。

[脱敏回执](evidence/multi-agent-development-20261114/probe-v3-errors-and-mode.json)。本轮覆盖候选验证，不把合成HTTP错误当OpenAI／Anthropic／Gemini实际可用证明，也不把本次样本扩张为日常所有业务任务质量通过。日常任务层的拒绝／截断和其他协议仍需分别验收；固定官方实现缺口若影响日常任务，先明确边界，不以此探针冒充已修引擎。
