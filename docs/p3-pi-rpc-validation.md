# R0 pi RPC runner 验证记录

2026-11-08。本轮仅完成独立 runner、协议栅栏及实际活动取消；pi RPC **未列为支持**，正常质量、正式服务/计划/attempt、App 和 Rust 应用链路仍待完成。traex 保持默认整理执行者。正式契约见 [pi RPC](p3-pi-rpc-contract.md)。

## 实际与夹具证据

实际 pi 0.84.1，选定模型为自身配置的 ollama / qwen3:0.6b；未更换或升级 Agent/模型。临时模型服务器只链接已存在的完整缓存，下载量为 0，服务器已停止并记录退出 0。

- `build/p3-pi-rpc/real-cancel.json`：实际活动 message_update 触发 context.Canceled，prompt 接受、abort ack、一次 agent_settled、reaped 和进程组消失全部成立；不释放结果。App/Store 应用/Rust 均为 false。
- `build/p3-pi-rpc/real-native-diagnostic.json`：固定 SDK before_provider_request 的原 Schema SHA 绑定及 HTTP 200 实际可见；小模型候选仍有空必需 title、metadata 不合契约及 pnpm→pmirror 误述，拒绝提交。HTTP 200/采样 Schema 不证明整理质量或来源正确。
- `build/p3-pi-rpc/real-normal-*.json`：保留连接错误、settled 适配前失败、Markdown 包装及候选校验失败。原 Snapshot.Decode 没有放宽。只接受裸 JSON 或唯一完整 json/无语言 fence，其他文字/多个 fence 不通过。
- `build/p3-pi-rpc/model-server-stop-request.json`：临时服务器终止请求与实际退出记录；隔离配置和合成正文未写入生产库或用户日常 Agent 配置。

`internal/agents/pi_rpc_integration_test.go` 是明确 **NO MODEL** 的 Python 子进程协议夹具：裸/fence/Unicode LF、额外/多个 fence、接受后 EOF、模型 error、意外工具、错 ID、缺 settled、取消及缺 abort ack，共 11 情况。夹具不能替代实际正常建议；文档中更全面的大小/重复 ID/guard/后代持 pipe 及服务链路仍需后续对应证据。

## 安全与检查

命令配置检查后的同一 models.json 字节写私有 0400 副本，避免校验后再次读取可变原文件；auth.json 仍由 Agent 自行读取链接。prompt 前 get_state 校验解析的 provider/model，session resolved 信息与未报告的上游实际模型分开。没有生产数据库、记忆工具或批准工具。取消优先于结果，接受回执、agent_end 与完整 settled/idle 终态分别处理。

`build/p3-pi-rpc/test.log` 为全量 `sh scripts/go.sh test ./...` 通过；`vet.log` 无诊断；`race.log` 为受影响 agents 包 race 通过。只读 Bits 分组审查的 `review/group/runtime.jsonl` 与 `fixture.jsonl` 均为空，原配置 TOCTOU 及取消误放行记录保留在对应 resolved 历史。审查只证明其读取的静态范围和已有证据，未执行新的模型或 App。

本切片没有改 UI、worker 或组件选择，不重跑此前未受影响的完整 App 包验收。待兼容模型输入后继续正常建议质量、可信服务/计划接入、活动取消与真实 App/Rust验收；并行推进 G/F。A1b 的 Codex版本/模型 400、A2b 的隔离IDE登录以及 F3 的签名/渠道/干净环境继续独立未通过。总 Goal 未完成。
