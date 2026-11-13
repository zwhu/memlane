# Provider 重定向保护

范围：技术方案的凭据目的地约束及 LP04；探针与正式服务使用同一 HTTP 策略。保持固定官方引擎源码与 archive 完全一致。

## MA-PROVIDER-REDIRECT01

验收标准：OpenAI、Anthropic、Gemini 的已配置请求收到重定向时，不向未经配置的目的地发起请求；SDK 缓存过期或清空后仍成立。全部实验使用 loopback 服务及合成密钥，无真实供应商重定向事故证据。

第 1 轮失败：固定 LiteLLM 1.108.2 三个协议均跟随同源 307，目标实际收到请求；OpenAI 样本甚至被记为 passed。这证明原始地址参数观察不能保护 HTTP 重定向。三条子断言归同一轮，不拆分计数。

修复：`generation_http.py` 使用 SDK 显式 HTTP session 与 LLMClientCache 扩展，给新建 HTTPHandler 设置 follow_redirects=false；缓存清空后每个新客户端重新配置。保留 SDK 原有过期、逐事件循环隔离与回收行为。策略作用域只关闭自身客户端，用 WeakSet 跟踪 handler，避免长期服务不断持有已淘汰实例。正式 Uvicorn 生命周期和隔离探针均进入此作用域；不包装模型请求、不改目的地、不引入网关或备用模型，不改官方引擎源码。

第 2 轮定向复验：三个协议各只请求初始地址一次，307 不跟随，18.636 秒通过。扩大检查另发现诊断问题：Anthropic/Gemini 把 3xx 包装为连接错误，回执错误码仍为 unknown；同轮跨地址直接调用检查在该诊断断言提前终止，不能据此宣称完整矩阵通过。传输保护失败仍累计 1 轮；错误诊断独立记 MA-PROVIDER-REDIRECT-STATUS01，失败 1 轮。

诊断修复：优先检查异常 cause/context 中真实 response.status_code，识别 3xx，不解析或回显供应商正文。Native 提示核对并明确配置最终地址。最终定向两项检查 16.989 秒通过：三个协议同源 307，以及直接官方 adapter（无探针 observer）跨地址 308，分别在 SDK 缓存清空前后请求；初始请求共六次，目标请求零次，作用域恢复原 SDK 配置并关闭其显式客户端。这里的错误码修复不算新增传输失败轮次。

本次最初测试启动遗漏 PYTHONPATH，导入失败、未发请求；作为命令夹具错误保留，不计产品验收失败。

SDK 配置依据为本机固定版本源码：`litellm.llms.openai.common_utils` 的显式 session、`custom_httpx.http_handler` 的公开 handler/client、`caching.llm_caching_handler.LLMClientCache` 的实例扩展。当前在线 [HTTP handler 文档](https://docs.litellm.ai/docs/completion/http_handler_config)主要描述 aiohttp_openai，不能用于宣称普通 openai/Anthropic/Gemini 都读取该 aiohttp 配置；本次以三个实际固定 SDK 协议实验验证配置范围。
