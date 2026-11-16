# 模型服务容量控制

用户要求结合 Cognee 并发方案改进实现，同时覆盖其他同类模型服务；按端点容量复用，不绑定模型名或 Agent。仅 dev，保留正在执行的真实整理任务。

## 流程和验收

Agent 已保存材料 → 官方 improve / cognify / 提炼 → 官方 native adapter / LiteLLM → HTTP 容量准入 → 当前模型服务 → 读完响应或取消 → 释放容量。官方分窗、水位、图谱、去重、重试、退避继续由 Cognee 负责；不补采、不重放未知材料、不另建内容队列。

| 要求 | 实现与依据 | 验收 |
| --- | --- | --- |
| 限制总在途，而非每文档块数 | generation_capacity.EndpointCapacity，按当前端点 origin 匹配；普通/结构化、不同阶段、模型共用额度 | 多客户端64突发、真实固定官方SDK路径 |
| 同步/异步/多事件循环共享 | 同一policy持有线程安全FIFO准入，Future通知；不使用每事件循环一份的独立Semaphore | 同步线程与异步请求争用、取消与授予竞争 |
| 响应头不算完成 | HTTPX公开transport/stream扩展，读完正文或关闭才释放 | 延迟正文、读取取消、幂等关闭 |
| 等待时间有界 | 默认等待300秒，到限抛出PoolTimeout固定原因generation_capacity_wait_timeout；不添加业务重试 | 短预算确定性超时，后续请求可进入 |
| SDK缓存重建不能绕过 | 现有policy通过公开client属性注入共享HTTPX客户端；新建缓存handler也复用，废弃客户端及时关闭 | 清空缓存前后普通/结构化混合请求 |
| 其他服务及代理 | 只限制当前模型origin；不同origin不占额度；沿用固定LiteLLM已有环境代理mount构造器，每个代理transport仍共享额度 | 本地代理、SDK一次429重试；native协议回归 |
| 配置不污染其他身份 | profile新增可选max_concurrent_requests，整数1–256，缺省2；进入原不可变版本和验证摘要，运行启动回执记录容量 | 配置校验/摘要与旧版本兼容 |

主要改动：generation_capacity.py、generation_http.py、official_service_dev.py、generation_probe.py、generation_validation.py、generation_config.py。入口使用同一policy，现有Provider与Agent桥接均生效。默认2是保守实施值，不是供应商容量测量结果；可按已验证后端容量调整profile，不从模型名称猜测。等待300秒同样是本地预算，不保证任意历史批量均能在预算内完成。

## 对上游方案的取舍

参考[PR5685](https://github.com/topoteretes/cognee/pull/5685)及[后续提交](https://github.com/jstevenjacobs/cognee/commit/c413ac03bbd647312724d04183d180516c9020fb)的“dispatch之前控制在途”思路。没有直接复制：

- 上游提交按event loop/model分配额度，多个模型可能共用同一机器容量；当前按选定端点origin统一，覆盖同步/异步请求。
- 固定Cognee没有公开的pacing插件注册入口，替换其全局函数或修改.cache会破坏现有固定源码契约；复用现有SDK HTTP策略边界，不修改引擎。
- 仅调整CHUNKS_PER_BATCH不能保证跨文档/阶段的总并发。保留已在运行的单块配置，作为现有工作量控制，不宣称它就是容量限制。
- 每个SDK客户端单独配置连接池会成倍扩容；共享准入与客户端，避免缓存重建重置额度。
- 容量持有至正文结束，重试退避发生在额度之外。不新增RPM、不扩大桥接32准入队列、不自己重试。

边界：同一policy跨线程/事件循环共享准入，不代表HTTPX客户端可跨循环任意复用；客户端仍由当前服务事件循环持有。不同进程或独立policy不共享额度，服务端容量保护必须保留。当前单实例官方服务通过asyncio.run/单Uvicorn实例运行；未来多worker必须新增跨进程容量方案，不能将本实现称为机器全局限额。SDK代理mount依赖固定LiteLLM内部辅助函数；组件升级必须执行这组回归。当前profile不开放自定义TLS/专用transport；未来加入这些配置需同步保留其传输配置。

## 回归命令与边界

复用现有task.py门禁和unittest，无模型调用、无记忆库写入、无服务启停：

```sh
python3 scripts/task.py -- env PYTHONPATH=engine/python .local/cognee-python/bin/python -m unittest test_generation_capacity test_generation_parameters test_generation_probe test_generation_config test_generation_ollama test_codex_completion_endpoint
```

已建立：旧policy不接受端点参数的红灯；64突发、多客户端、响应正文、排队超时、执行取消、授予/取消竞争、同步/异步、独立事件循环、代理与SDK重试；固定Cognee/LiteLLM的OpenAI兼容、Anthropic、Gemini三协议各64个普通/结构化混合请求，缓存清空前后峰值2、最终占用0。真实桥接准入代码仅替换execute，64个SDK请求全部成功且无重试、最终占用0。Ollama补原生64请求及缓存重建检查。具体整组结果在下方收口，不把协议夹具当真实知识产出。

原有codex_busy问题、失败证据和次数沿用[codex队列研究](codex-queue-official-research-20261115.md)及[官方委托记录](cognee-delegation-20261115.md)，本轮没有发起新的真实恢复验收。运行所有者确认单块候选仍在处理真实长材料，因此本轮不重启以免打断；代码通过后交接所有者于当前任务结束后正常加载，再核对实际回执和真实处理终态。

## 当前验证结果

生成相关整组55项通过，86.388秒；随后最终容量8项通过，5.211秒；新增Ollama并发及启用传输策略后的原生协议10项通过，24.150秒。三组有重叠，不累加为独立用例数。证据日志分别在.local/generation-capacity-regression-20261116.log、.local/generation-capacity-unit-final-20261116.log、.local/generation-capacity-ollama-final-20261116.log。源码及检查回执摘要见[evidence/model-service-capacity-20261116/result.json](evidence/model-service-capacity-20261116/result.json)。git diff --check通过；只修改Python和文档，不启动无关Go/Native或release构建。

首次整组出现两个子用例超时：早先429负面用例遗留官方进程级过载冷却，后续native容量压力用例等待RPM而非本地容量。独立用例已通过，日志栈明确停在aiolimiter.acquire。修正仅在该测试以新OverloadPolicy隔离旧状态，不修改生产退避；整组复验通过。另有首次MockTransport返回已读正文造成测试等待，传输层补齐已消费响应的立即释放语义并定向通过；属于开发反馈，不用新测试名清零既有真实容量缺陷记录。

未重启日常服务、未重跑真实材料、未调用付费模型。本轮是源码及真实SDK/HTTP协议机制验证，实际长材料处理、产出及召回继续由原服务任务验收，不把已有单块运行无busy归功于尚未加载的新策略。未经正常重启的新源码不描述为已在日常服务生效。
