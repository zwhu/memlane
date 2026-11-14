# 项目Native启动与Go IPC核对 · 2026-11-11

003两次Native启动未新增任务且只显示泛化「启动未确认」，之后Python最小请求成功。该历史错误没有保留完整请求／错误，不能据此认定为nil与空数组、Go传输、授权或执行者错误。

## 本次真实核对

在同一活daemon中，以已存在的operation `47ca7c2c-5bf4-4a38-93d2-6a1c6f335b78`、原项目和binding revision1调用真实`serviceipc.ProjectTaskClient.Start`重放。Go完整Request包含额外空结构体、`sessions:null`；返回既有`cd8e38e7-ddd7-405d-86d1-55a7669dedeb` completed，没有另起模型任务。证据`build/goal-concentrated-20261111-003/project-start-go-replay.json`，执行源保存在同目录client-replay-probe/main.go。没有重启服务、操作Native、读取auth或历史聊天。

这排除当前同参数下的确定性Go Request解析差异；不能恢复003原来两次Native请求和已经丢弃的原错误，原根因仍待下一包真实操作。

## 实现

- 项目启动错误经真实IPC保留request／executor／history／persist阶段和原有错误码；`Response.Stage`、`ServiceError.Stage`为向后兼容的可选字段。错误包装保留Unwrap，原授权、配置、历史范围和存储校验不放宽。
- Native操作区分传输连接／超时与后台拒绝阶段，展示固定中文阶段及安全错误码；不展示provider原始错误文本、私有路径或凭据。错误继续保留到明确重新读取，不因后台刷新短暂消失。
- 已存在启动意图在解析当前可执行文件／配置前恢复原任务回执；丢回执后的明确重试不应因环境变化创建另一任务，也不应要求旧付费执行者再次有效。不同输入重用同operation仍严格冲突。
- 未选择历史时，Native仅发送仓库范围，不附带隐藏旧session或workspace选择；勾选历史仍保留完整明确授权，后台对恶意直接IPC携带历史继续严格拒绝。该防御边界不被宣称为003已证实根因。

## 验证边界

真实Unix socket、严格JSON decoder和SQLite回归比较Python最小空数组与Go完整Request／nil及空数组，检查同一任务和零模型执行；覆盖执行者变化后的原意图恢复、请求冲突、执行者拒绝、持久化绑定变化、阶段／错误码传输。Native控制器夹具检查未勾选历史不传递残留范围、原错误码保留、传输超时分层及错误文本不泄露。

定向`TestProject` daemon／desktop／serviceipc通过；完整包、新Native按钮成功及原始失败根因不由本次测试替代。后续004集中包应直接记录真实启动阶段与错误码，继续对照实际操作闭环。

本批定向40769最终exit0：daemon5.579s、desktop1.633s、serviceipc无独立测试文件。相关race首次3244的daemon通过，desktop因另一并行UI修改的在途undefined owner编译失败；该Agent修正后60212输出daemon2.462s、desktop3.535s均ok。原失败保留，不修改生产约束迁就测试。`git diff --check`通过。真实Go重放47915最终exit0。
