# pi 模型桥接：实现与本机闭环验收

2026-11-09。用户要求“实现和真实闭环验收，先拿 PI 试试”。本轮已实现统一本地服务准备与 Cognee → pi 模型桥。**本机完整包的技术闭环已通过；当前 qwen3:0.6b 的事实质量未通过。** 第八次报告的原始主线状态为 `main_flow_passed`，正文复核后的综合状态为 `technical_flow_passed_quality_failed`。不能将技术通过扩大为可靠提炼模型或全部需求完成。最新证据位于 `build/pi-model-bridge/`，不提交本机配置、token、会话或记忆库。

## 实现范围

- `internal/official/runtime.go` 的 `PrepareEnvironment` 准备官方 Cognee/BGE/固定插件并验证本地四类存储；没有 Agent 或云模型 Key 时仍能启动。`Prepare` 另行发送实际推理与嵌入探测。
- `internal/desktop/runtime.go` 将上述准备纳入基础 helper/Rust/E5 初始化，汇总真实组件归档大小。首次任务页面选择并验证 pi，与电脑级接入和项目/任务授权分开。
- `internal/agents/pi_completion.go` 抽出通用 pi RPC 模型任务；原有组织建议 runner 继续复用它并保留自身快照及结果校验。
- `internal/modelbridge` 提供私有 loopback 的 OpenAI 兼容模型接口。Cognee 内保存 App 生成的本机 token，模型登录继续由 pi 使用自身配置；没有把云端模型 Key 放进 Cognee。
- 新打包服务 `1.6.7-memlane.5` 将私有模型别名的真实 Schema 能力登记到 LiteLLM，保留 Cognee 原生适配器并传递完整 `response_format.json_schema`。旧组件在模型验证前明确拒绝，不继续使用通用 JSON 回退。
- `internal/official/model_bridge.go` 管理包内 helper 的独立模型桥进程与 Cognee owner，使用一个活动推理槽位、请求 ID、期限、暂停取消门禁和明确重试。
- 推理使用临时目录和独立上下文，不加载日常会话、仓库上下文、技能、插件、MCP 或工具。仅 Ollama 加载包内无工具 Schema 扩展；每次响应都经过 Go 的完整 Schema 校验和 RPC 终态/空闲/进程回收检查。

采集端为官方 **Codex 0.167.0-alpha.3.2 + Cognee plugin 1.7.7**。模型执行端为 **pi 0.84.1 → Ollama / qwen3:0.6b**。这两个角色须分别报告；本轮没有实现 pi 的官方整轮自动采集，也没有实现 Codex app-server 模型桥。

本机默认 Ollama 服务原先停止。本轮另起隔离端口，复用已有模型 blob 的只读链接并使用合成 pi 配置；未启动或修改日常 Ollama 服务，未下载新推理模型。Codex 登录仅在隔离任务期间按既有规则提供给该 Agent，不复制过去的会话。

## 真实配置与测试配置的区别

本轮沿用用户 pi 的默认 provider/model 选择，并使用真实安装的 pi 和已有模型文件。测试 provider 配置在临时目录重建，服务地址替换为独立 Ollama 实例。

| 项目 | 原始 pi 配置 | 本轮测试配置 |
| --- | --- | --- |
| Provider | `ollama` | `ollama` |
| 模型 | `qwen3:0.6b` | 同一模型，复用已有 blob |
| API 类型 | `openai-completions` | 相同 |
| 模型服务地址 | `http://127.0.0.1:11434/v1` | 本轮临时实例 `http://127.0.0.1:61649/v1` |
| pi 配置目录 | 用户的 `.pi/agent` | 临时隔离目录中的 `pi-agent` |
| 日常会话、工具与插件 | 用户日常配置 | 推理中关闭，仅加载 Memlane 的无工具 Schema 扩展 |

因此，本轮验证的是实际 Cognee → pi → Ollama → qwen 模型链路。**原始 provider 配置和原服务地址的直接复用，以及云端 provider 的登录/API Key 复用，尚未实测。** 不能用“复用了默认选择”概括为“原始配置已原样验证”。

后续只读查询 `pi --offline --no-extensions --no-context-files --no-skills --no-prompt-templates --no-themes --list-models`，还列出 `deepseek/deepseek-v4-flash` 与 `deepseek/deepseek-v4-pro`。这两款是 pi 内置目录在当前配置/认证下列出的模型，本轮未选择它们、未向它们发起桥接推理；模型列表状态不代替实际调用验收。

## 测试脚本与复现入口

| 入口 | 用途 |
| --- | --- |
| [隔离模型路由](../scripts/create-p3-pi-model-fixture.py) | 读取实际默认选择，为已有 qwen 模型创建只读 blob 链接和独立 provider 路由；不下载新模型。 |
| [独立模型桥验证](../scripts/verify-pi-model-bridge.py) | 使用真实 pi 验证 Schema 返回、实际流式推理取消、abort ACK 和进程组回收。 |
| [完整 App 验收根](../scripts/create-agent-first-task-validation.py) | 创建新业务根、Git 仓库、真实组件源和运行配置；可明确只复用已校验组件文件。 |
| [完整 App 主线](../scripts/verify-agent-first-task-app.py) | 执行无模型 Key 初始化、无项目连接、界面选择 pi、取消/重试、真实官方任务、提炼/索引、完整出处、用户编辑和新会话当前版本读取。 |
| [完整包构建](../scripts/build-client.py) | 构建包含 UI/helper/LaunchAgent 的本机验证包。 |
| [服务组件构建](../scripts/build-cognee-service.py) | 构建固定 Cognee 1.6.7 的真实服务组件；本轮适配版本为 `1.6.7-memlane.5`。 |

第八次主线的关键参数为 `--pi-bridge --pi-select-in-app --pi-cancel-check --regression-task --require-full-schema`。另以 `--pointer` 指定新验收根、`--out` 指定报告目录、`--auth-directory` 指定本机现有 Codex 配置目录、`--codex-executable` 明确选择已经核对版本的实际采集程序。每个参数及依赖通过脚本 `--help` 查看；不把本机密钥或配置文件复制进文档。

旧验收根已经有业务记录，主线脚本会拒绝把它当作空根重跑。复现需要创建新根、新包身份，并重新启动本轮拥有的模型实例和组件源；原始模型路由脚本只准备文件，服务进程仍需按保存的路由条件单独启动。最终清理后直接打开旧包，不代表这些测试服务仍在运行。

第八次报告入口为 `build/pi-model-bridge/schema-run-008/runtime/report.json`，实际窗口截图在同目录的 `screenshots/`；独立桥报告、历次失败、版本门禁结果、体积和清理事实均在后续章节定位。凭据、会话和记忆库作为本机验收产物保存，不提交 Git。

## 已取得的证据

| 场景 | 结果与证据 |
| --- | --- |
| 无 Agent、无模型 Key 的打包 Cognee 服务 | 通过。`no-agent-initialization/result-storage-only.json`：`service_ready=true`、`model_bridge_selected=false`、`cloud_model_key_present=false`、清理通过。 |
| 真实 pi 结构化响应 | 通过。`real-pi-hllboudb/report.json`：实际 `0.84.1 / qwen3:0.6b`、HTTP 200、完整 Schema、terminal/reaped/group_gone 全部成立。 |
| 独立桥的活动推理取消 | 通过。同一报告有实际 `message_update` 后取消、abort ACK、HTTP 408、进程组消失；没有把 ACK 当成成功终态。 |
| 完整 App 未选 Agent 的统一初始化 | 通过。`lifecycle-run-005/runtime/report.json` 包含 `app-unified-initialization-without-selected-agent-or-model-key`；实际初始化阶段没有模型桥进程或推理回执。 |
| 实际界面选择 pi | 通过。`schema-run-008/runtime/report.json` 包含 `actual-app-selected-pi-without-cloud-key-or-inference`；原生控件保存所选配置，此时未发模型请求、未安装本机 token。 |
| App 活动取消与明确重试 | 通过。第八次报告记录完整 Schema 下实际流式 `message_update` 后点击取消、abort ACK、HTTP 408、reaped/group_gone；后续请求获 401 bridge_cancelled。再次明确点击验证后服务和桥重新准备并实际通过探测，仍没有写业务记忆。 |
| 无项目连接、工作区确认仍不登记空项目 | 通过。`full-run-004/runtime/report.json` 与 `lifecycle-run-005/runtime/report.json` 有独立连接身份、待项目 UUID 和仅两个全局 scope 的真实 IPC 回执。该切片不扩大为四种 Agent 通用电脑级配置已经完成。 |
| 官方插件准备、真实 pi/BGE 推理验证 | 通过。第八次有实际固定插件安装与真实模型/嵌入探测。所有成功请求的 Schema 都已检查，未出现通用 `{type:object}` 回退。 |
| 官方采集和持久接收 | 通过。第八次正常修复任务实际先跑出 1 个失败、1 个错误，修改实现后原有 2 个测试通过；官方产生 1 份 QA 与 5 份工具轨迹，加上完整会话原文，Memlane 接收 7 条原始记录。 |
| 提炼知识和完整出处 | 技术通过。官方 `improve` 返回 `completed documents=1`，首次任务形成 1 条提炼知识，保留完整会话和六份 SDK 分段引用，Rust 索引实际就绪；正文质量发现见下节。 |
| App 编辑后新会话读取 | 通过。用户在实际 App 中将该条目改为第 2 版；新 Codex 会话不 resume，提问不含新口令，实际调用 Memlane 工具返回新内容、revision 2 和出处。 |
| 完整 Schema 与隔离终态 | 通过。第八次有 18 次成功 pi 模型请求，全部经完整 Schema、terminal/reaped/group_gone 校验；推理事件中没有工具执行，采集端和提炼端角色分开。 |
| 旧组件拒绝 | 通过。`schema-version-check/result.json` 确认旧组件在推理前被版本门禁拒绝，没有新增模型请求。 |

`no_gated_entries` 是官方 `distill_session` 的前置材料门禁：会话须存在净 helpfulness 非负且 confidence 达标的有效上下文候选。仅有 QA/trace 水位不等于存在可提炼候选。本轮对第四次合成缓存只读核查，看到三个状态/水位行，未看到有效知识候选；未改库或绕过门禁。模型调用成功与能产生可用知识是两个不同的验收结果。

## 保留的失败与修正

| 运行 | 失败与处理 |
| --- | --- |
| 001 | 控制请求误用 Cognee 的 `X-Api-Key` 传输，而模型桥要求 Bearer；已增加专用客户端与回归测试，失败报告保留。 |
| 002 / 最初无 Agent 检查 | 官方 `/health/detailed` 会发 LLM 请求；改用验证本地四类存储的 `/health`，实际推理留在明确验证入口。 |
| 002 | 原生窗口自动化捕获超时；样本显示 AppKit 主线程空闲，窗口位置和焦点需要恢复。驱动增加明确激活，App 已通过原生菜单停止并清理。 |
| 003 | pi 的目录中同时存在旧版 Codex，PATH 发现选中了 0.147.0；采集准备被版本门禁拒绝。验收现在显式填写已核对的采集二进制，并直接报告准备失败。 |
| 004 | 正常任务与采集完成，提炼没有有效候选；保留 5 份原始材料、0 条知识和官方状态。主动结束本轮空结果等待，driver 原始错误为 KeyboardInterrupt，另记实际观测原因。 |
| 005 | 界面选择 pi 已成功，但驱动在 UI 动作处理前读配置，误报缺少 model_bridge；已改为等待持久配置。失败截图显示“已选择 pi；模型可用性尚待真实验证”。 |

第六次的实际选择、取消和重试通过，但后续仍没有有效候选，有限接收最终报 IPC 超时。诊断发现 Cognee 对未知私有模型别名走通用 JSON 回退，模型桥没有取得完整 Schema；直接使用官方复杂 Schema 的诊断曾被校验器拒绝（HTTP 502）。第七次在配置中选择其他框架，但服务固定启动策略覆盖了该设置，实际 Schema 断言仍失败，未开始编码任务。

随后在服务入口登记私有兼容接口的真实 Schema 能力，重建 `1.6.7-memlane.5`，并加入旧组件版本门禁。第八次保留新业务根、身份和会话，仅复用已校验组件文件；旧服务组件与新服务并存，新版实际下载和准备。首次任务、实际 pi 提炼、完整出处、用户编辑与新会话读取全部完成。没有切换模型、降低提炼门禁或注入知识。

## 事实质量未通过

第八次初始提炼正文为“Fixed ticket normalization with edge whitespace stripping and all tests pass”，后面列出 `ticket.py, test normalization, test type, alice, memlane` 等实体。修复与测试结论可在原始任务中核对，但 **`alice` 在原始 Codex 会话、两份 QA 和六份工具轨迹中均未出现**。这是一项无来源实体，属于事实质量失败。

JSON Schema、持久接收、完整出处和当前版本读取并不证明正文每个事实都正确。原始产物与旧版本保持留存，未删除或改写来补造通过。当前模型不标为可靠提炼模型；更强模型和事实质量样本仍需另验。本轮完成的是 pi 模型桥的实现与真实技术链路，质量未通过项保留在最终报告中。

## 交付限制

- 本轮 UI 沿用既有 MyGo 页面。实际截图只验证当前页面；已认可 A01–A04 的同页安装设计及后续新连接/处理设计没有因此完成。
- 已实际查看第八次的任务、完整出处、第 2 版三栏详情和新会话读取截图；现有界面的准备动画与长内容仍使用旧布局，不作为新设计的视觉通过证明。
- 原有 P3 整理调度器、持久 attempt/租约、确认/版本/来源规则继续由各自模块管理；本轮模型桥没有替换它们。全部崩溃/重启/配置变更代次、真实登录失效/额度耗尽矩阵仍需补验，不能宣称需求 R01–R11 全部通过。
- 原生 Schema 适配仅实际验证 Ollama；其他 pi provider 仍须验证。模型变更需明确选择并重新验证，不能因为 pi 能列出某个模型就标为可用。
- 主包含 UI/helper/LaunchAgent，不含重型引擎与模型；组件源为固定真实归档的合成 loopback 分发。Developer ID、公证、线上分发和升级不在本轮结果内。
- [Magpie 待办](agent-model-bridge-requirements.md#后续调研待办) 已记录，完整多 Agent provider 研究尚未开展。

## 检查与体积

全仓 `sh scripts/go.sh test ./...` 与 `vet ./...` 通过。鉴权/Schema/终态、取消后拒绝新请求、payload 不进诊断回执的单测通过；modelbridge/official/agents 的 race 检查通过。最后初始化文案修正后 desktop/agents 回归通过。相关 Python 脚本语法和补丁空白检查通过。

第八次五个组件的完整首次归档量为 **711,825,710 bytes**，其中新服务组件为 **366,565,322 bytes**，SHA256 为 `a3a3dbbe2cfb3d9ea1ae277f4389aaeb24e7e172ef5377745fb47e43ed4c64a3`。旧模型与插件归档保持固定；本次升级验收复用兼容组件文件。已有 qwen 推理模型新增下载量为 **0**，其现存 blob 总量为 **522,653,767 bytes**；未安装 pi 的用户还需另计 Agent 和推理模型准备。

最终完整 App 为 **41,668,370 bytes**，DMG 为 **18,562,866 bytes**。App 加验收数据/组件目录的实际分配占用为 **2,572,328,960 bytes**，包含留存旧服务组件 **933,515,264 bytes**；扣除该旧版目录后为 **1,638,813,696 bytes**。以上不含已有推理模型，见 `schema-run-008/package-size.json`。没有把纯 UI 编译日志大小或该升级根的占用冒称全新用户安装结果。

所有本轮组件源、完整 App/helper、Cognee 服务、模型桥及独立 Ollama 进程已实际退出，服务 socket 已移除。清理事实见 `final-source-cleanup.json`、`final-model-cleanup.json`、`final-service-cleanup.json` 和 `final-owned-executable-cleanup.json`；此前失败报告继续保留。

最初 Git 交接只发布桥接需求、研究依据与本验证记录。2026-11-09 基线整理将桥接实现、相关测试、复现脚本及构建支持一并纳入 Git，后续开发可从同一源码版本继续；`AGENTS.md` 已提供对应开发入口。构建与验收产物仍只保存在本机，凭据、聊天记录、本机 Agent 配置与记忆库不纳入 Git。本次提交整理不增加模型、Provider 或新版 A UI 的验收通过范围；上述 qwen3:0.6b 结果保留为当时固定模型的历史证据。
