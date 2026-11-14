# Agent 连接页可理解性纠正

2026-11-14。用户原话：“这是什么什么奇怪的设计，我完全看不懂”，针对设置／服务与连接中的 Agent 列表截图。

## 判断与范围

已证界面把“准备、启用、收到回执”等工程阶段直接交给用户；已连接项仍显示灰色空复选框；未安装项、未完成适配及 Cursor 时序解释和可连接项同权重展示；每行再套边框、底部两个常驻禁用按钮，缺少明确下一步。

本次保留当前页面和批量连接，调整呈现与操作顺序，不新建向导，不修改主体、授权、官方插件、配置摘要核对、取消与未知结果恢复协议。不点击真实连接，不运行模型，不重启日常后台。失败问题记为 `AGENT-CONNECTION-UX-01`：用户本次验收不通过 1 轮；不合并此前侧栏／阅读栏对齐问题。

## 最终行为

- 已连接的 Agent：正常文字色名称和“已连接”，不显示不可选的空复选框。
- 未连接且可用：可多选；选择后出现唯一主操作“连接所选（N）”。
- 配置准备完成：同页显示将添加的 Memlane 连接、将替换的旧 Cognee 连接数量及新会话要求；主操作变成“确认连接（N）”。确认前不调用 activate。
- 批次中混合未准备和已准备项时，先准备剩余项，随后统一确认；准备阶段按钮数量为所选总数。
- 确认后显示“等待新会话”，直接提示“重新打开 X 并新建会话”；成功项从选择中移除。只有真实新会话回执才能显示已连接。
- 未安装／暂不支持项收在“其他 Agent（N）”；连接结果待确认的项保持可见，仍可检查原操作，不被折叠隐藏。
- 删除嵌套卡片边框，使用分隔线；Cursor 限制移到状态提示；取消剩余项、逐项失败和原操作核对保留。

## 连续验证与证据

`sh scripts/go.sh test ./internal/simpledesktop -run 'TestAgentConnection|TestIntegration|TestLocalAgent|TestConnection' -count=1 -timeout=90s` 通过，包含已有部分成功／取消／未知写入重开恢复及新交互流程。

新增场景使用真实 Go HTTP 测试端点：点击选择 pi → 连接所选 → prepare 恰好 1 次、activate 为 0 → 显示配置变更 → 用户确认 → activate 恰好 1 次，携带原 proposed_sha256 → 显示等待新会话提示、取消选择框。默认态检查已连接项不可选、不可用项折叠、展开及选择正常。没有把该夹具当成日常 Agent 连接实链。

渲染图实际打开核对，位于 `build/agent-connection-ui-20261114/`：

- `fixture-agent-connection-default.png`
- `fixture-agent-connection-confirmation.png`
- `fixture-agent-connection-waiting-session.png`
- `fixture-multi-agent-1200x820-connections.png`

首次检查仍期待默认显示 Cursor 的旧断言失败，按新折叠规则更新夹具断言后通过；这是验收断言更新，不新增产品验收失败次数。

实际 Native 待验：CUA 当前应用清单没有运行中的 Memlane，现有日常配置的 dev owner 文件不存在，不启动另一套服务或将夹具称为实窗。当前结论为“源码与定向交互检查通过，实际窗口待验”。

## 资源与共享状态

执行期间空间下降到约 103 GiB，任务保护中止了格式化／status 检查。随后按既有依赖保护函数、工作树清单、进程参数及 lsof 核对，清理三个无引用可再生缓存 `.cache/cognee-target`、`.cache/python-worker-build`、`.cache/cognee-service-build`，约 4.2 GiB；没有清理运行包、源码、配置或数据。回执在 `build/disk-cleanup/*-agent-ui.json`，检查后约 107 GiB 可用。

共享工作区期间 HEAD 前进到 `3f0d63b`，已包含部分本轮界面修改；保留其他会话提交，不重置工作树。当前补充状态和测试以本文件及实际 diff 为准。
