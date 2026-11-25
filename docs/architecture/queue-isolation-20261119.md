# 采集与提炼队列异常隔离契约

本契约规定 Memlane 采集发送（`plugin_capture.drain`）、补存工作器（`project_backlog_worker`）与队列调度（`project_backlog`）在单条或单会话异常时的隔离边界：保持上游 Cognee 官方筛选与 Key 规则原样，**单条或单会话异常不得阻塞同队列健康尾部**；全部状态仅写入原官方 buffer 或原 jobs，不复制正文、不新建内容库、不篡改原 `_capture_id` 编号与来源归属。

## 1. QI01–QI06 异常隔离规则

| 编号 | 责任模块 | 隔离契约 |
| --- | --- | --- |
| **QI01** | `plugin_capture.drain`<br>`project_backlog_worker.save` | 服务端返回 HTTP `400/409/413/422` 明确拒绝时，仅隔离该条至 `capture_rejections`（保留原编号与载荷、不自动重试、`409` 不抹除旧 `unknown`），同队列其余已授权健康条目继续发送。 |
| **QI02** | `project_backlog.run` | 已冻结多会话中首个会话因权限、策略或传输暂停时，仅隔离该会话的 `save_status`/`save_reason` 与会话级 `save_retries`，继续处理后续健康会话；身份、进程或统计完整性未知仍维持整体屏障。 |
| **QI03** | `plugin_capture.drain`<br>`project_backlog_worker.save` | 真实 `POST` 发送前校验载荷可 JSON 序列化（拒绝非有限数字 `NaN`/`Inf`）；序列化失败仅隔离该条元数据、保留原载荷且不伪造已保存；真实回信解析失败则按 `unknown` 原编号逐条退避核对。 |
| **QI04** | `project_backlog.automatic_tick` | 复用官方提炼调用并以独立在途 `processing_task` 跟踪原 operation/target，保存调度不阻塞等待在途提炼返回，保留原提炼所有权与关闭边界。 |
| **QI05** | `project_backlog.start` | 手动多目标补存遇到被拒绝目标时单独登记至 `admission_blocked` 及原因，其余已核对健康目标正常冻结与补存；本操作不因后续授权变化自动补交原拒绝目标。 |
| **QI06** | `project_backlog.run` | 最终发布前复核终态（terminal）、冻结计数守恒与 `save_rejected`/`save_deferred` 分类子集；非终态或异常返回保持整操作未知屏障，不发布错误的 `saved` 或悬空 `running`。 |

## 2. Per-entry Buffer 版本兼容契约

- 逐条隔离状态仅存放在原官方 buffer 的 `capture_rejections` 与 `capture_retries` 字段中；新失败同时在原 policy `blocked` 字段标记 `entries` 作用范围。
- 已加载的旧版正常 sender 无法识别逐条字段时，继续遵循 `blocked` 触发的原会话屏障，防止重复发送已拒绝或未知条目；新版 sender 与工作器按逐条状态继续投递健康条目，并在解决后解除局部兼容标记。
- 既有无定位（无 scope）的旧 `blocked` 或 `write_result_unknown` 禁止猜测归因或静默清除；桌面端拒绝/退避提示绑定原 buffer 签名，不将 `unknown` 误作缺旧凭证或重复触发黄点。

## 3. 保留屏障与必跑回归边界

- **不得放宽的保留屏障**：官方筛选拒绝与 Key 规则保持原样；`401/403` 仍约束对应主体/会话；旧无编号 `unknown` 不重发；原编号丢回信按退避与只读 `GET` 回执核对；snapshot 失败隔离；用户停止、发送 lease 变化、sender 退出未确认或进度损坏必须阻断受影响受管操作。
- **配置态与实采事实区分**：管理配置枚举 `configured_pending_new_session` 表示等待新会话接管；真实接入须以宿主机新会话回执（`new_sessions`、`environment_verified`、`capture.state` 与逐条原编号 `GET` 回执）确认，不以安装完成或队列为空替代。
- **必跑回归入口**：修改保存/补存/调度链路时须回归 [`engine/python/test_plugin_capture_drain.py`](../../engine/python/test_plugin_capture_drain.py)（含旧版 sender 兼容夹具）、`project_backlog_worker` 与 `project_backlog` 对应单测，以及 Go 侧 `internal/simpledesktop` buffer 投影与提示测试。
