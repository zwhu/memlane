# 记忆处理验证与独立凭据：在途工程记录

2026-11-09。此页记录 A1 账本之后的实现。pi 验证所有者现已接入 daemon 生命周期、共享执行槽位、私有 RPC 与显式启用动作；尚未接入 Native 页面、Cognee/P3 整项任务分派，也未用新包执行这条 daemon runner 的真实 pi 推理。不能称 A1、Provider 或全局任务分派通过。账本包证据见 [A1 验证](memory-processing-ledger-validation.md)。

## pi 验证所有者

`internal/processing` 将已保存配置与实时依赖分开。解析 pi 时检查实际安装元数据、工具集 2、验证代次、Extension 原文摘要、Agent 0.84.1 协议、配置目录及 Provider/模型选择；凭据内容不进入账本或报告，只将依赖摘要用于失效判断。pi 原 Provider 与认证仍由 Agent 使用，不转存成独立 Provider 条件。

Owner 接受 daemon 生命周期与共享执行槽位，提交请求的 context 不拥有后台任务。排队完成后再次检查连接与依赖；真实 runner 固定使用完整 Schema 的小型合成探测，不采集知识、执行工具或证明事实质量。成功须有完整终态、进程回收和原生 Schema 请求；依赖再次变化则失败。新的验证尝试可撤销旧尝试，旧回执不能覆盖新尝试。

取消先持久化，再请求实际 runner 退出。最终 CAS 遇到并发取消时重新读取并持久化 cancelled，不留无进程的 validating 或迟到成功。未能持久化终态或进程组未退出时要求关闭所有者；新 daemon 所有者将旧 validating 记为中断，不自动恢复推理。活动进程和事件计数是即时观察，不能被重启缓存当成成功。

`go-test-processing-owner-002.log` 和 `go-race-processing-owner-002.log` 位于 `build/ui-delivery-20261109-001/`。真实 SQLite 加明确模型协议夹具覆盖：提交断开、活动/终态重放、取消迟到、Close 等待清理、排队撤销连接、依赖变化、崩溃状态协调及取消恰到最终 CAS。未用这些夹具证明真实后端或 daemon 接入。

## daemon 私有控制面

daemon 启动时先把遗留 `validating` 记录协调为中断，再创建唯一验证所有者。新服务配置显式记录客户端根；旧 `service.json` 仅为兼容从服务根回推一次。pi 安装元数据、服务根和实际 guard 可执行文件因此由 daemon 自己解析，不由 UI 传入临时结果。

私有 IPC 已提供开始验证、读取持久状态与即时进程观察、明确取消和明确启用。开始、取消、启用均要求用户动作；UI/Agent 仍没有伪造完成回执的接口。验证与正式整理共用 `organizerSlot`，排队后重新检查连接和依赖。启用再次解析实时依赖，并由账本核对同一成功验证、当前候选和选择代次；保存或验证成功都不会自动切换当前方式。

定向测试覆盖 daemon 调用门禁、真实 SQLite 终态、共享所有者状态、取消、启用和依赖漂移；私有 Unix socket 集成测试确认未支持的 Provider 不会因 RPC 接通而获得验证。全仓 test/vet 与 processing/daemon/store race 通过。这里仍使用协议 runner 夹具，没有启动真实 pi、创建新完整包或打开 Native 页面，因此不能替代后续真实验收。

## 独立 Provider 凭据版本

采用 macOS Security.framework 的 Keychain 通用凭据项，通过当前仓库已有 purego/Objective-C 路径调用，不经过带密钥 argv 的 shell 命令。版本键为规范 UUID 与修订号，命名空间绑定明确客户端根；不更新已有版本。一个供应商的多个模型可共享同一版本，读取时核对完整供应商地址和协议；地址或协议变化要求新凭据。Secret 不进入 SQL、配置响应、日志、进程 argv 或工程报告。

新项使用本机可访问属性且不启用同步；读取禁用隐式认证 UI。Keychain 不可用、锁定、旧包访问被拒等情况返回错误，不退回明文文件。当前只验证本机测试二进制身份；后续 Native 包、helper 和升级身份对同一 Keychain 项的访问仍需单独验收，不能由源代码或本机单进程通过代替。

Native 验收只用随机合成字节和独立根/UUID，不使用 pi 密钥或任何供应商账号。`native-credential-vault-001.log`、`002.log` 的真实 Keychain 存取、重复版本拒绝、地址变化拒绝、两份版本保持和精确删除均通过。测试在写系统项之前保存仅含自有引用的合成 marker，以便异常退出后精确清理；正常结束删除两项并由测试清理临时根。

`go-vet-processing-owner-vault-001.log` 失败保留：两处原生地址转换触发 unsafe.Pointer 检查。已改为 NSData 拷贝动态符号和凭据返回字节，不依赖键字符串猜测，也不关闭 vet。`go-vet-processing-owner-vault-002.log` 与 `native-credential-vault-003.log` 均实际退出 0；两项合成 Keychain 凭据删除、合成临时根也实际不存在。

凭据引用的供应商配置登记、配置保存事务和删除前引用检查还未接入服务；当前没有开放删除用户凭据的 UI/RPC。Q02 实际供应商及独立授权条件仍待用户，Native 合成 Keychain 通过不等于 Provider 推理通过。

## HTTP 协议与模型目录基础实现

新增完整 Schema 的 HTTP 请求适配，不添加工具，不退回 prompt-only/json_object，不自动切换协议或供应商。具体字段依据官方 API 定义：

| 协议 | 原生请求字段 | 官方依据 | 本轮实际状态 |
| --- | --- | --- | --- |
| OpenAI Responses | `text.format`，`type=json_schema`，`schema`，`strict=true` | [官方 SDK 定义](https://github.com/openai/openai-python/blob/main/src/openai/types/responses/response_format_text_json_schema_config_param.py) | HTTP 协议夹具通过，实际独立供应商未验证 |
| OpenAI Chat Completions | `response_format.json_schema`，`schema`，`strict=true` | 既有 pi 原生 Schema 请求绑定与对应 OpenAI API 结构；供应商适配仍须实际验证 | HTTP 协议夹具通过，没有把 OpenAI 兼容名称视为所有模型支持 |
| Anthropic Messages | `output_config.format`，`type=json_schema`，完整 `schema` | [官方文档](https://platform.claude.com/docs/en/build-with-claude/structured-outputs) | HTTP 协议夹具通过，完整 Schema 若不受模型支持则失败，未剥除约束 |

模型目录从所选服务的 `/models` 获取，保留 API 返回的 ID/名称和读取时间。Anthropic 目录保留 `has_more/last_id` 与受控 `after_id`，未读后续页时不能称完整目录；依据 [Models API](https://platform.claude.com/docs/en/api/models)。目录拒绝缺失字段、重复 ID 和不完整分页身份。接口读取失败不变成可信零；不能从目录成功推断 Schema、授权或事实质量。目录不可读供应商的手动模型 ID 页面仍待接入。

`go-race-processing-http-001.log`、`go-race-processing-http-vault-002.log`、`go-vet-processing-http-001.log` 通过。明确 HTTP 夹具覆盖三种完整 Schema 绑定、无回退、Schema 不符、重定向不转发凭据、活动请求取消/迟到拒绝、Magpie 禁止附独立凭据、分页身份和坏目录未知； transient Credential 不参与 JSON 序列化。没有执行真实供应商或 Magpie 推理。

当前新增模块后的全仓 `go-test-processing-http-vault-all-002.log` 与 `go-vet-processing-http-vault-all-002.log` 均实际退出 0；Python 验收驱动 AST 与 git diff 检查通过。此结果不代替尚未实施的 daemon 接入、完整新包和真实推理验收。

请求取消仅证明本地传输退出与迟到结果拒绝；没有通用上游活动推理/计费取消回执，不将本地断开称为三种上游取消已验。正式验证任务和取消报告须继续补各真实后端的适用证据。

Magpie 公共三协议和目录参考固定 [接入说明](https://github.com/yetone/magpie/blob/ed0ab567cba78fae8ea9d9c9a07a7446e315b787/docs/reference.md#connecting-anything-else)，无本机访问凭据。网关当前不可连，源码/HTTP 夹具均不代替真实服务条件。Provider 独立凭据经 Keychain 的精确供应商目标读取；不会借用 pi 的认证。

## 下一步

用新包、新合成根取得 daemon 所有的实际 pi Schema、进程回收与活动取消回执，再接 Native 保存／验证／启用状态。随后将冻结配置纳入 Cognee/手动整理/新计划的整项任务事务。Q01 旧 traex 计划迁移未获决定，不更换其已授权执行者或启停意图。
