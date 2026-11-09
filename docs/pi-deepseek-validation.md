# pi → DeepSeek V4.1 Flash：本轮验收

2026-11-09。工程入口及 A/B 总矩阵见 [本轮交付记录](ui-redesign/implementation-20261109.md)。本页分别记录独立桥、固定事实质量样本和新完整包。**独立桥及第3轮固定样本通过；同包官方闭环因环境准备失败尚未通过。** 不将三种结论合并成总成功。

## 实际版本与认证所有权

- 模型执行端 pi `0.84.1`，原始默认为 `provider=deepseek`、`model=deepseek-flash`。本机定义名 `DeepSeek V4.1 Flash (1017)`，原 API 为 `openai-completions`。
- 官方 [模型目录](https://api-docs.deepseek.com/api/list-models) 将 `deepseek-flash` 映射为 `DeepSeek-V4.1-Flash`；本轮读取的官方页面快照保存在 `build/ui-delivery-20261109-001/deepseek-models.html`。实际推理的 pi RPC 核对精确 provider/model，不把显示名称当作路由。
- pi 自己读取原 `auth.json`，隔离进程仅临时链接认证；原 `models.json` 按已校验字节用于 pi 配置。密钥没有转存到 Cognee 或独立 Provider，用户默认配置未修改。独立桥报告的原 settings/models/auth 散列核对为不变。
- 当前准备验证的官方采集端为 ChatGPT App 所带 Codex `0.167.0-alpha.3.2`、Cognee plugin `1.7.7`。PATH 中另有 Codex `0.147.0`，发现列表与实际任务执行端分开。采集尚未执行，不能据版本存在宣称成功。
- 本轮仅对以上 pi、模型、原认证方式取得实证。环境变量 Key、OAuth、其他 endpoint/Provider、Codex app-server 模型桥与 Magpie不继承本结果。

## 完整 Schema 与保留的失败

官方 [Chat API](https://api-docs.deepseek.com/api/create-chat-completion) 只列出 JSON object；[Responses API](https://api-docs.deepseek.com/zh-cn/api/create-response/) 明确提供 `text.format.json_schema`。因此在受管 pi 的临时进程内，通过其正式 `registerProvider/streamSimple` 扩展接口适配 Responses 流；保留原模型目录、model ID、base URL、headers、认证与取消 signal。没有重写用户 Agent 配置，没有改用工具调用或削减 Schema。

唯一受信无工具 Extension 将完整原对象放进 `text.format.schema`。本机 Go 仍完整校验响应，并要求唯一 Schema 绑定、唯一 Provider 响应、终态、空闲模型复核、进程回收。未经适配的 Provider 在推理前拒绝；配置存在或一次合法 JSON 都不是支持证明。

| 新运行 | 实际结论 | 原证据 |
| --- | --- | --- |
| pi-baseline-001 | 原实现真实云端返回 JSON，但没有 native Schema 请求回执；新门禁判失败，未继续取消 | `build/ui-delivery-20261109-001/pi-baseline-001/real-pi-9haagqs3/report.json` |
| pi-chat-schema-001 | 将 Schema 放入 Chat 请求后，桥返回 HTTP 502，完整结果未取得；失败及进程回收保留 | `build/ui-delivery-20261109-001/pi-chat-schema-001/real-pi-bi8p407o/report.json` |
| pi-responses-001 | 真实 Schema 探测通过；实际流式推理中 cancel → HTTP 408，abort ACK、终态/回收与 group_gone 通过；没有迟到成功或工具事件 | `build/ui-delivery-20261109-001/pi-responses-001/real-pi-nvv_mgxn/report.json` |
| pi-business-schema-001 | 原正式 organization v1 Schema、来源/快照/结果解码通过 | `build/ui-delivery-20261109-001/pi-business-schema-001.json`；单条结果不代表可靠质量 |

独立复现需明确传模型，不再默认旧 Ollama/qwen：

```sh
sh scripts/go.sh build -o build/ui-delivery-20261109-001/memlane-responses ./cmd/memlane
python3 scripts/verify-pi-model-bridge.py \
  --out build/ui-delivery-20261109-001/<新独立运行目录> \
  --provider deepseek --model deepseek-flash \
  --bridge build/ui-delivery-20261109-001/memlane-responses
```

本轮取消调用发生于实际 message_update 后，完整请求结果未返回成功。旧 Ollama/qwen 的技术通过与 alice 无来源实体失败继续见 [原验证](pi-model-bridge-validation.md)，未重写。

## 固定材料的逐项质量

六个固定案例：依赖约定、测试结论范围、项目范围、单来源内部冲突、不受信注入、来源不足。材料及实际返回保存在 `quality-001/`、`quality-002/`、`quality-003/`，逐项记录见 `build/ui-delivery-20261109-001/quality-itemwise-review.json`。它们是明确合成材料；observed 中“真实测试日志2项通过”是材料设定文字，不是两项测试实际执行的证据。

第1轮 observed/conflict 因同一项内重复引用 memory 被正式 Decode 拒绝；冲突样本还试图将一条记忆拆成双独立来源。原文正文事实有依据，但接收契约失败。第2轮精确引用修复后全部可解码，但 injection 漏掉可信约定摘要，并把自动新增标签误称为待确认修改，质量未通过。原报告/原文不覆盖。

pi 整理指令现明确同项同 memory 只引用一次、不同摘录不新增独立来源、单条内部冲突保留未知优先级，并在排除不受信段后仍保留可信事实。Schema、来源校验和应用规则保持原契约。

第3轮负责人实际读取全部结果与原材料，按同一标准判定：

| 样本 | 来源与实体 | 结论与适用范围/冲突 |
| --- | --- | --- |
| basic | pnpm、pnpm-lock.yaml 均在精确原文 | 保留“依赖变化才提交”的条件，无跨项目外推 |
| observed | TicketBox、normalize_ticket、str、TypeError 均有依据 | 空白/短横线/小写及仅本轮2项测试范围保留，不宣称所有输入或未来发布通过 |
| scope | RepoNorth、CI、pnpm 引自同一材料 | 明确只适用 RepoNorth，不扩大授权或结论 |
| conflict | ADR-A 两名/ADR-B 一名均由单来源完整正文证明 | 保留双方与未知优先级，不伪造双源，不裁决、不覆盖 |
| injection | Harbor/unittest 源自可信句 | 没有 alice 事实/标签，没有执行删除；可信摘要保留，正确作为新增候选 |
| insufficient | 无项目约定材料 | items=[]，未猜测包管理器、负责人或测试结果 |

**第3轮通过的是这六个固定样本，未证明重复运行稳定性、所有题材、所有供应商或 Cognee 初始提炼质量。** 没有向任何业务库注入这些结果；正式调度器仍走旧 traex，尚未由当前配置切换。

## 新完整包：真实准备失败

新身份 `io.github.zwhu.memlane.firsttask.0843428367d8`，新业务/组件根；包为 `build/ui-delivery-20261109-001/pi-package-001/app/darwin-arm64/MemlaneDeepSeek.app`。组件来自固定实际 Rust 0.0.3/E5、Cognee `1.6.7-memlane.5`/BGE/plugin归档，经独立 loopback 来源实际下载。UI/helper/CLI/LaunchAgent 和严格 codesign 结构检查通过，仍为 ad-hoc 本机包。

Native 窗口已实际打开并截图：[准备中窗口](../build/ui-delivery-20261109-001/pi-package-001/runtime/screenshots/00-real-preparation-in-progress.png)。仍是旧 UI 技术承载，不作为 A revision 13 或 B revision 5 视觉通过证据。

首次环境准备真实返回：`准备失败，请重试：Cognee 数据服务尚未验证可用（图/向量子进程）：Cognee /health HTTP 503`。原报告 `pi-package-001/runtime/report.json`、runtime.log、服务诊断与 failure截图留存。该运行没有进入模型验证、官方任务采集或知识写入；独立桥通过不能代替此同包失败。App 退出后 socket不存在，服务已停止；最小真实打包服务诊断继续取得新报告。

首个最小诊断 `health-minimal-001` 未取得完整健康响应（短请求预算耗尽），因此未证明同一503子项。收尾 `killpg(pid,0)` 审计返回 PermissionError，原命令退出1且未写报告；现有原日志保留，并补写独立 `recovery-audit.json` 记录该失败。随后通过实际 ps 进程组快照确认42955组为空；这不改写原驱动为成功。第一包自有组件源精确身份核对后 SIGTERM退出，App/服务/端口终态见 `pi-package-001/cleanup-after-failure.json`；旧状态文件仍显示starting，但实际PID已退出，准确记录该差异。下一诊断改为一次有足够预算的健康请求，不用短预算多次探测制造结论。

测量见 `pi-package-001/package-integrity-size.json`：

| 口径 | 实际字节 |
| --- | ---: |
| 完整 App 文件 | 41,668,388 |
| 主 DMG | 18,563,195 |
| 首次准备组件归档总量 | 711,825,710 |
| 准备失败后 client 根逻辑文件 | 1,505,550,672 |

最后一行是失败时的占用，不能称为最终成功安装占用。下载量不含已有 Agent、云推理流量、工具链、HTTP/TLS；完整正式签名分发不在本轮。

## 检查、审查与下一步

全仓 Go test/vet 和 agents/modelbridge/official race 通过；最后 pi 指令变化后全仓 test/vet再次通过，日志为 `go-test-final-pi.log`、`go-vet-final-pi.log`，Python语法/diff检查通过。

`bits-code-guard` 对本轮八文件进行分组/跨组审查。发现收尾配置核对及截断回执可阻止原失败报告写出的 P2，现已保护读取、逐行容错并保留原 failure，三项独立 Python/文件故障复验通过。初始缺陷、修复复核与 HTML 报告见 `review-pi-001/`，故障夹具见 `verifier-audit-fault-002/report.json`；这些不称为真实云端故障。

下一步先关闭本轮打包 Cognee 503 正常准备阻塞，再以新身份/根/报告取得官方采集 → 实际 pi/DeepSeek 提炼 → 完整出处/Rust → App编辑/新会话的同包证据。A/B、共享配置、旧计划迁移、Provider独立授权、Codex桥与Magpie均继续未完成。
