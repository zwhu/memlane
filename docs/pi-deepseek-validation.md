# pi → DeepSeek V4.1 Flash：本轮验收

2026-11-09。工程入口及 A/B 总矩阵见 [本轮交付记录](ui-redesign/implementation-20261109.md)。本页分别记录独立桥、固定事实质量样本和新完整包。**pi独立桥、六个固定样本第三轮、第五包同根官方采集/提炼/完整出处/编辑/新会话闭环已通过；两条初始知识逐项审核通过。** 失败报告保留，结论限所列模型、样本与本机包；A/B新UI、全配置分派及最终普通包仍未完成。

## 实际版本与认证所有权

- 模型执行端 pi `0.84.1`，原始默认为 `provider=deepseek`、`model=deepseek-flash`。本机定义名 `DeepSeek V4.1 Flash (1017)`，原 API 为 `openai-completions`。
- 官方 [模型目录](https://api-docs.deepseek.com/api/list-models) 将 `deepseek-flash` 映射为 `DeepSeek-V4.1-Flash`；本轮读取的官方页面快照保存在 `build/ui-delivery-20261109-001/deepseek-models.html`。实际推理的 pi RPC 核对精确 provider/model，不把显示名称当作路由。
- pi 自己读取原 `auth.json`，隔离进程仅临时链接认证；原 `models.json` 按已校验字节用于 pi 配置。密钥没有转存到 Cognee 或独立 Provider，用户默认配置未修改。独立桥报告的原 settings/models/auth 散列核对为不变。
- 实际官方采集端为 ChatGPT App 所带 Codex `0.167.0-alpha.3.2`，沿用其 `gpt-6.1-sol`，Cognee plugin `1.7.7`。模型执行端另为pi/DeepSeek。PATH中Codex `0.147.0`只是发现结果，不冒称本次采集版本。
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

### 空根健康修复与新运行

一次有完整预算的基础健康请求 `health-minimal-002/report.json` 实际返回503，耗时43.42秒，服务进程组回收。原错误为 Ladybug 初始化尝试从 `extension.ladybugdb.com` 下载 JSON 扩展失败。锁定的 Cognee wheel 已带匹配资源，冻结打包却遗漏了它；现在将 `v0.19.0/osx_arm64/libjson.lbug_extension` 收入组件，避免新私有 HOME 在基础健康检查时额外联网下载。

PyInstaller 会重签该 Mach-O；第一轮构建后的原字节SHA检查失败保留。复核11个原生代码/数据节及动态依赖完全相同、签名有效后，构建门禁改为分别记录源/分发SHA并核对原生内容与依赖。源630,440字节、SHA `e6482a025de31d662ee4b32a39c991706b8cc3c2ec49673ef5d8ce45cc23ce7a`；分发644,928字节、SHA `a10e51c0fe8f91958300c753cfe6bcf6d52be753caf50539e8b456ff09547822`。证据为 `json-extension-native-sections.json`、`json-extension-packaging-fixed.json` 与 `cognee-service-fixed-verified-build/cognee-service-build-result.json`，修正后完整组件构建退出0。

新组件 `1.6.7-memlane.6` 在另一个空合成根 `health-fixed-001/report.json` 单次真实 `/health` 返回200、ready/healthy，耗时33.14秒；没有LLM请求，进程组回收。该归档366,777,590字节、SHA `9f3e515c308e874ab2576389fc511b4d0d22fd853d27a61dce193c4a56c839f9`；本轮五个组件归档合计712,037,978字节。第二包真实环境准备、无项目连接、App选择pi与短模型验证通过，但活动取消驱动错过短请求而超时。第三包已通过同样准备，在首次验证自动进入插件页后仍寻找上一页按钮而失败。原报告分别保留在 `pi-package-002/runtime/report.json` 与 `pi-package-003/runtime/report.json`，均不计取消或官方闭环通过。

第四包使用新业务/状态/身份/会话，仅复用已校验组件文件。取消夹具先通过Cognee真实验证，再发起实际pi长合成请求与App验证共用槽位，在设置的共享准备页操作取消；报告明确该长请求不是官方采集材料。随后另行执行真实官方编码任务，不用受控请求代替采集和事实质量验收。

第二、第三包的App/服务已停止，socket不存在；自有组件源逐一核对完整argv后SIGTERM退出，新清理事实见各自 `component-source-cleanup.json`。第四包已退出，socket不存在，自有源清理见 `pi-package-004/component-source-cleanup.json`。

### B02断线停止的局部修复

daemon原实现仅在回执Encode成功后停止；调用者关闭socket会留下已停止标记和仍运行的进程。现在成功执行stop/quiesce后登记defer停止，回执写失败也回收；执行停止失败时不关闭服务。原无worker三次失败见 `stop-disconnect-baseline-001/report.json`，修后无worker三次通过见 `stop-disconnect-fixed-001/report.json`；实际Rust 0.0.3 worker的三个独立根均通过，见 `stop-disconnect-rust-fixed-001/report.json`：停止持久化、daemon/socket/锁退出及父PID归属核对后的worker退出全部成立。

这是B02断线回执根因的修复证据，尚不代表活动官方模型、最终普通App包或全部停止恢复矩阵通过。daemon/official race及相关test/vet通过，日志 `go-race-stop-service-fix.log`。停止及取消驱动3文件直接审查见 `review-stop-001/`，组件修复3文件审查见 `review-service-fix-001/`；当前没有剩余可确认P0–P2。

全仓 Go test/vet 和 agents/modelbridge/official race 通过；最后 pi 指令变化后全仓 test/vet再次通过，日志为 `go-test-final-pi.log`、`go-vet-final-pi.log`，Python语法/diff检查通过。

`bits-code-guard` 对本轮八文件进行分组/跨组审查。发现收尾配置核对及截断回执可阻止原失败报告写出的 P2，现已保护读取、逐行容错并保留原 failure，三项独立 Python/文件故障复验通过。初始缺陷、修复复核与 HTML 报告见 `review-pi-001/`，故障夹具见 `verifier-audit-fault-002/report.json`；这些不称为真实云端故障。

该历史检查点的下一步为新包取得同包证据；最新第五包结果见下节。A/B、共享配置、旧计划迁移、Provider独立授权、Codex桥与Magpie均继续未完成。


## 第五包同根闭环与保留的取证失败

包身份 `io.github.zwhu.memlane.firsttask.41b3df22ae9d`。App、helper、CLI、LaunchAgent为一次完整构建；新业务/状态/身份/会话，仅复用经过SHA验证的组件文件，仍使用Rust0.0.3、Cognee1.6.7-memlane.6、pi0.84.1/deepseek/deepseek-flash、官方Codex0.167.0-alpha.3.2/plugin1.7.7。没有用新B03的0.0.4报告拼接该包。首启/选择/取消与原任务报告 `pi-package-005/runtime/report.json` 保留；同包继续的实际最终报告为 `pi-package-005/runtime-continuation-005/report.json`，不是第二个包或新的初始质量样本。

1. 环境与无项目接入、确认工作区、App选择pi、真实Cognee验证、实际pi流式取消408/abort ACK/回收/后续401通过。长取消请求明确是合成并发负载，不冒称官方材料。
2. 取消后返回首次任务丢目录的缓存问题修复：确认后同步连接快照，并提升刷新代次防旧异步结果回写。重试保留原授权目录，重新验证，官方插件真实准备。
3. 官方Codex在合成仓库复现normalize_ticket既有测试的1失败/1错误，实际读源码和测试、修复后两项通过。官方会话与工具轨迹保存，Cognee实际模型请求经pi/DeepSeek完整Schema执行，Memlane保存7条raw（完整会话、QA和trace）及2条知识，Rust索引实际确认。
4. 新出处取证脚本误用source字段名而失败。继续001误触空根门禁，002/003读到旧验证状态，004误从全部记忆等待项目控件，均保留各自failed报告；没有重新运行原编码任务或替换其材料。修正后同根明确启动服务，等待新的实际模型回执，再从原知识逐项读取6份QA/trace来源及6306字节完整会话。不同SDK分段不算独立来源。
5. Native App实际打开完整原文阅读器，编辑原知识为v2，Rust当前索引确认；新Codex会话先调用memory_search/memory_get，实际返回v2、当前审核口令、项目范围和出处，问题中没有答案、不resume、不读取旧会话猜答案。原始出处与v1保留。

初始内容审核：负责人实际阅读每条知识、全部来源及完整会话，对来源、实体、结论、适用范围与冲突逐项核对，记录 `pi-package-005/initial-quality-itemwise-review.json`。第一条修复流程建议对应实际先失败、读取代码/测试、patch、两项回归通过；第二条发布前unittest且不削弱断言的项目约定来自用户明确材料。两条均有限通过；没有虚构实体、通过所有输入或其他项目规则。此次未提炼全部函数字段规则和初始审核口令，原文保留，不宣称知识覆盖完整或广泛可靠性。此前六样本及旧qwen失败继续独立保留。

实际窗口已打开并由负责人看图：`runtime-continuation-005/screenshots/06-full-original-reader.png`、`07-user-current-version.png`、`08-new-agent-session-current-recall.png`。这些是现有Native技术承载，不能当作A revision13/B revision5视觉通过。

收尾 `pi-package-005/final-cleanup-audit.json`：App/CLI/worker/Cognee/桥及自有源实际均退出，没有剩余该bundle/root进程，socket不存在。Cognee状态文件仍显示starting，而PID已退出；准确记录差异，后续共享持久状态应修复，不把文件旧状态当运行事实。新报告中的本机访问token仅为该根桥接控制凭据；用户Agent认证不转存。正式签名分发不在本轮。

`pi-package-005/package-size-final.json`：App41,668,388字节，DMG18,563,281字节；五组件新空缓存所需归档总量712,037,978字节；此次明确复用组件文件，实际HTTP下载量未单独取得，不冒称这轮实际下载同样字节。最终client逻辑文件1,558,609,461字节，含独立服务/模型/业务/会话与运行日志，不含开发工具链、已有Agent及云流量；不叫最终A/B首版占用。

下一阶段补齐正式配置/任务所有者、旧授权迁移和三步/设置复用。B03新归档已有独立原文与daemon证据，仍需绑定后续Native包；Provider独立条件、Codex模型桥、Magpie、电脑级配置、B统计与普通包R01继续未完成。
