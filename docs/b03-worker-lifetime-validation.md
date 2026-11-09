# B03：原文 worker 请求寿命修复

2026-11-09。本轮A/B交付所需的持续使用阻塞切片。**新Rust归档、Python源、Go客户端及daemon真实跨限通过；最终Native包绑定尚未完成，B03不据此整体关闭。** 原失败见 [旧验收](acceptance/2026-11-09/summary.md)。pi第五包继续使用0.0.3，不拼接本页证明其worker已升级。

## 请求契约与兼容

Rust0.0.4及Python源在ready中声明 `request_id_scheme=sequence-uuid-v1`。受管Go客户端在实际写锁内生成 `seq:<递增uint64>:<规范UUID>`，完整wire ID是此流内的唯一身份；UUID作为客户端的相关键。worker仅保存序号高水位，拒绝任意旧序号、非规范格式及legacy/sequence混用，不通过淘汰早期ID缩小重放保护。失败写入形成序号空档仍可继续，uint64耗尽明确拒绝，不回绕。

响应回传完整ID，Go关联到UUID等待者；取消支持完整ID或该UUID，仍须同时核对实际取消ACK与目标终态。ready之前取消时Close可以发shutdown，因此协商字段发布也使用同一写锁。来源、memory/revision、索引generation、租约和主动停止契约均保持。

旧worker未声明能力时Go继续使用legacy ID；旧直接协议流仍保留10000个ID的容量限制。新受管长期连接明确协商序列模式，不能把未升级旧组件称为修复。daemon health不再吞worker错误：调用失败、未确认ready或worker不存在时返回unavailable及worker_error，不能显示缓存ready。已有Enola固定ID/版本组合允许新0.0.4，原图谱配方与能力不扩展。

## 实际证据

均位于 `build/ui-delivery-20261109-001/`，新合成根，未调用LLM。

| 运行 | 结果 |
| --- | --- |
| b03-rust-lifetime-001、b03-python-lifetime-001 | 10005健康请求与最早序号重放拒绝通过；驱动给record额外protocol字段，活动索引被拒绝，取消验收失败。原failed报告保留 |
| b03-rust-lifetime-002 | Rust0.0.4同一进程跨10005、最早重放拒绝、真实长原文活动取消ACK/终态、后续health/shutdown通过；Go独立实例再跨10005后真实SQLite保存/索引/当前检索、超时不标索引成功、重启有效绑定通过 |
| b03-python-lifetime-002 | Python0.0.1源采用同一序列契约，跨限/最早重放拒绝/活动取消/后续health/shutdown通过；不冒称Python冻结包已升级 |
| b03-rust-lifetime-003 | 监督Go进程与worker组的新驱动正常复验通过 |
| b03-daemon-lifetime-001、002 | daemon＋Rust0.0.4同一worker跨10005后仍ready，真实保存、索引和当前检索通过；明确stop持久化，daemon/socket/worker退出；002收尾门禁包含正常退出码 |
| b03-daemon-old-error-002 | 新daemon搭配真实旧0.0.3，原容量故障继续重现，但health准确unavailable/duplicate_id；停止与实际回收通过 |
| b03-checker-timeout-001 | 明确将Go进程预算设1秒：保留failed/TimeoutExpired，捕获其真实worker父PID与PGID54282，有界回收，组清空。该报告不是正常执行通过 |

`b03-daemon-old-error-001`仅元数据路径前置失败，未启动daemon；不能当作真实跨限故障。所有新报告独立命名，不覆盖旧失败。坏归档、超时和清理异常必须分别保留primary失败与附加清理错误，不能以正常模型结果替代。

新归档 `b03-component/memlane-cognee-rust-0.0.4-darwin-arm64.tar.gz`：69,956,704字节，SHA `4c8fc431d867aa572ef7433c4cf7127019376aa450ce07251008d21453aa4e1d`，解包逻辑文件206,998,555字节，不含模型。Rust入口完整构建退出0，见b03-worker-build.log；包与源码版本、序列协议通过真实ready/handshake核对。

## 检查与剩余边界

全仓Go test/vet通过（go-test-runtime-final.log、go-vet-runtime-final.log）；engine/daemon/desktop race通过（go-race-b03-cache-fixed.log）。新增协议进程夹具只模拟ready和shutdown，不加载模型、不制造业务成功；race覆盖启动取消与ready发布，并用40个并发写者验证12000个实际wire ID顺序。结果go-race-request-sequence.log。Python语法与diff检查通过。

bits-code-guard分组和跨组发现的启动取消数据竞争、停止验收漏掉清理失败、归档失败缺报告、Go超时独立组回收/primary丢失及Enola版本门禁均修复；原缺陷与修复证据保留于review-runtime-continuation-001。原文实时取消仍不承诺中断所有原生阻塞任务，因此Go超时故障采用自有进程组有界回收，不让任务继续占用当前服务。

后续新Native完整包必须实际包含0.0.4，再验证正常准备、持续轮询、当前检索、取消和stop；普通入口R01、A/B状态持久化及全部配置分派继续独立完成。本文不宣布新UI、正式签名、公证或分发通过。
