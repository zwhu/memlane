# 官方 Agent／Cognee 本机验证

2026-11-08。独立验证切片，开始基线为 `main@0a82942ad8fab48ecba664c7fb0797b911deded0`。本轮正常任务、官方持久化／提炼、Memlane 衔接、真实 App、Rust 当前版本检索和新会话召回已通过；必要重放、隔离、取消与故障恢复也已取得证据。旧全量工程 Goal 仍暂停，[Q1–Q4](product-questions.md) 仍待讨论；验证通过不选择日常自动记忆策略。

证据根为 `build/agent-integrations-validation/`，只含本轮合成材料，不提交配置、凭据、数据、会话或临时 handoff。本记录描述实际结果，完整链路不以插件安装、页面 Connected 或单行 success 为判据。

## 版本与实际接入

| 对象 | 本机事实 | 证据与边界 |
| --- | --- | --- |
| Memlane | 开始时本地与远端均为 `0a82942`，本轮在 main 上分阶段提交 | `baseline.json`、`protected-files-final.json`；F1e、图标及用户文档修改保留，不混入提交 |
| 官方上游 | 固定 `997fd6f72253aa97b49996eb1b032793f0257935`，真实远端 HEAD 相同 | `versions-and-install.json`；隔离 checkout 的实际源码 |
| Codex Cognee 插件 | 官方 marketplace 安装 `1.7.7`，安装文件匹配固定上游 | `plugin-install.json`；插件源码没有修改 |
| 采用的 Agent | 机器已有 `/Applications/ChatGPT.app/Contents/Resources/codex-cli/CodexCLI.app/Contents/MacOS/codex`，`0.167.0-alpha.3.2`，`gpt-6-astra` | `app-codex-preflight.jsonl`、真实任务事件；没有安装／升级 Agent 或切换模型 |
| PATH Codex | `0.147.0` 对同模型实际返回 HTTP 400，要求新版 CLI | `model-preflight.jsonl`；失败保留，随后找到已有 App CLI |
| 参考页面与相邻仓库 | `localhost:3000/integrations` 返回 200；相邻 SHA `e93a4f0c76e6af27142c25189f14c200e1749d51`，源码 1.5.4、venv 实装 1.5.3 | 只读参考，没有向该服务或用户数据写入 |
| 本轮 Cognee | 复用 `.local/cognee-python` 的实装 1.6.7；FastEmbed 0.9.1、Uvicorn 0.54.0、LiteLLM 1.108.2 | 独立 `127.0.0.1:18011` 和合成根；插件自举 pin 1.6.5 是另一条安装路径，本轮没有宣称安装成功 |
| Cognee 模型 | 既有 `deepseek/deepseek-v4-flash`、既有 DeepSeek endpoint；BGE `BAAI/bge-small-zh-v1.5` | 只读既有 Cognee 设置进入进程环境，不提取 Agent 登录凭据、不输出密钥 |
| Memlane 索引 | Rust worker 0.0.1，ONNX Runtime 1.28.0，multilingual-e5-base-int8 | `final-runtime-evidence.json` 中实际 worker PID、当前候选版本与来源；worker 禁止网络 |

DeepSeek 真实拒绝 `json_schema`，最初处理阶段失败。`scripts/cognee-validation-server.py` 使用 LiteLLM 官方 `register_model` 登记实测的 `supports_response_schema=false`，让 Cognee 1.6.7 走已有 `json_object` 分支，再由真实模型和 Cognee 校验结果。没有替换模型响应、伪造成功或修改安装运行时；正式脚本也已实际重启验证。

## 验收矩阵

| 切片 | 实际结果 | 主要证据 |
| --- | --- | --- |
| 官方接入 | 真实 Codex 加载官方 hooks，向独立 Cognee 提交，取得可查询服务记录 | `learn-events.jsonl`、`learn-session-detail.json`、插件隔离 hook 日志 |
| 正常采集 | A 正常完善 normalize_ticket，9 项 unittest 通过；形成 1 QA、5 trace | `learn-persistence-evidence.json`；唯一口令只在首会话，不在项目文件 |
| 同步与提炼 | 官方结束／manual sync 调用 improve；实际阶段终态成功，产生 2 session_learning 文档 | `official-pipeline-status-after-compat.json`、`official-learned-documents.json` |
| Memlane 衔接 | 受信 IPC 导入 6 raw、2 knowledge，冻结来源和具体 v1；无新 migration | `actual-sync-inputs.json`、`memlane-import-payloads.json`、`memlane-import-receipts.json` |
| 真实 App | 完整 UI／helper／LaunchAgent／新导入契约包实际打开，查看知识、原始出处、Agent／会话、范围及索引状态 | `real-app-report.json`、`source-window-evidence.json`、`screenshots/`、`final-runtime-evidence.json` |
| Rust 当前检索 | 真实 E5 worker 查询命中字符串约束知识 `6d7bb883-672c-515b-9efc-a6f0bd58d70f@v1` | `rust-current-search.json`、最新包的 `final-runtime-evidence.json` |
| 新会话召回 | 无 resume／首会话历史，通过官方 `cognee-search.sh --graph` 正式 recall 返回原会话口令和来源，实际回答正确 | `recall-command.json`、`recall-events.jsonl`、`recall-evidence.json` |
| 重放 | 同输入重放未增加知识、版本或活动，最新包再次复核通过 | `memlane-replay.json`、`final-runtime-evidence.json` |
| 双范围隔离 | B 正常任务 5 项测试通过；B 新会话正式召回 A 口令时明确不知道，没有越过 dataset | `btask-session-detail.json`、`official-b-isolation.json`；Memlane B 查询也无 A 记录 |
| 真实取消 | 在真实 180 秒工具校验仍 active 时撤销 B 连接并 SIGINT 取消；已经提交的 3 trace 保留，迟到同步全部拒绝，B 记录未变 | `real-cancel-report.json`、`cancel-session-detail.json` |
| 服务故障恢复 | 停本轮 Cognee，真实 Codex 完成任务，官方缓冲 3 项；重启后官方有限 manual sync 持久化 1 QA／2 trace，再同步 ID／数量不变、缓冲归零 | `outage-buffered.json`、`real-outage-report.json`、`outage-session-after-1.json`、`outage-session-after-2.json` |
| 撤销后恢复 | 故障恢复的实际 Cognee 材料仍无法进入已撤销 Memlane B 连接 | `recovered-records-after-disconnect.json` |
| 检查／审查 | 全仓 test、vet、受影响 store／daemon／serviceipc race 通过；两组和跨组审查修复后无高置信 P0–P2 | `go-test-final.log`、`go-vet-final.log`、`go-race-final.log`、`review/report.html` |
| 清理／保留 | 停止本轮 Agent、hooks 和独立 Cognee；删除临时 Agent auth 链接；保留真实记录、完整包与显式启停入口 | `official-cleanup.json`、最终体验／清理回执 |

## 数据归属与转换契约

官方 `hooks.json` 实际覆盖 SessionStart、UserPromptSubmit、PostToolUse、Stop、PreCompact 和 SessionEnd。prompt 先进入插件 pending；PostToolUse 提交 typed trace；Stop 将 prompt 与实际回答组成 QA，写入 `POST /api/v1/remember/entry`。会话缓存采用独立 SQLite，具有 TTL；它与图谱持久化、提炼文件是不同事实。正式召回是 `POST /api/v1/recall`，结束同步是有写入副作用的 session-aware `POST /api/v1/improve`，只在合成数据中执行。

本轮没有使用普通 Memlane MCP 保存替代官方接入。先从官方实际 session detail 与提炼文件取得原文及稳定 ID，再由本轮宿主转换，通过受信 Memlane 服务的私有 `cognee_import` 操作导入。转换和导入发生在正常官方持久化之后；没有 curl 注入、直接 SQLite 写入或预造正常记录。该入口是最小衔接，尚未构成日常后台同步产品。

| 层 | 项目 A 的稳定身份与保存位置 |
| --- | --- |
| 工作区 | `fixture-path.txt` 所指合成根的 `repo-a`，真实 Git 根 |
| Codex 会话 | `01a1ba3a-db9e-7e43-b997-c4a82446a30e`，隔离 HOME 下 `.codex/sessions/` |
| Cognee 会话／dataset | `codex_01a1ba3a-db9e-7e43-b997-c4a82446a30e`；`38d60399-d553-56ca-bca5-ada23d48e407`（memlane_validation_a），合成根 `core/system`／`core/data`／`core/cache` |
| 官方原始 QA | `a552e5ea-ea4d-4a9d-986a-2a1d0154207d`；另有 5 个真实 trace，ID 见持久证据 |
| 官方提炼 | data ID `feba8884-ebd7-43c7-9430-91bc49b0707a` 与 `34fd0b4f-4097-4b5e-9198-c4dd4cd0d0a0`；原文见实际提炼文件证据 |
| Memlane 项目 | `b1e742bf-0e73-46a4-8934-4f190b5b688a`，实际工作区与批准连接绑定；B 为独立 project／dataset |
| Memlane 记录／版本 | `cognee://<dataset>/sessions/<session>/qa\|trace\|data/<id>` 绑定确定性 memory ID；本轮首次版本均 v1，保存于合成根 `client/data/memlane.sqlite` |
| 当前索引 | 同一根 `client/data/index`；导入提炼原文后，实际 Rust worker 从索引任务构建当前版本块，再通过服务查询过滤当前版本 |

Memlane 不直接消费 Cognee 实体图。实际路径是官方 session_learning 原文 → 带 raw 来源的 knowledge v1 → 既有索引队列 → Rust 原文 worker／E5 → 当前版本检索。普通实体图、抽取文件或临时表没有被当作 Rust 接入完成。

私有导入需要用户确认、有效 Codex 连接代次、真实工作区与唯一批准项目；普通 Agent 工具没有导入或批准入口。宿主通过现有连接授权和 operation digest 去重，事务内持久化版本／索引任务／派生来源，不覆盖已有知识；重放保留首次保存版本并返回当前版本。dataset 归属只由受信 raw 导入账本建立，不能由普通 raw 自由引用伪造。

session_learning 只能引用同一 dataset／session 的受信 raw 当前版本，引用必须包含精确原文。`organization_sources` 冻结具体版本、摘录与证据根；同源摘要不增加独立证据。源码集成检查覆盖跨 session、同项目跨 dataset、假受信引用、用户后续版本保留、来源失效、事务回滚和断开拒绝。导入与撤销分别持授权读／写锁；迟到结果不能越过撤销屏障。未确认合并、覆盖、删除或跨项目移动均不改变已有有效知识。本轮未增加迁移，因此没有新 migration 的旧库迁移步骤。

## App 与用户体验

已实际打开首页／三栏详情设计图，并对照系统窗口截图。宽幅首页、三栏列表详情、连续浅蓝侧栏与栏内操作保持。截图包含 `home-system.png`、`knowledge-system.png`、`frozen-sources-system.png`、`raw-session-system.png`；截图回执验证实际系统窗口可见，UI render tree 仅作为辅助。

在本机直接运行：

```sh
python3 /Users/bytedance/work/AI/memlane/scripts/open-agent-integrations-validation.py
```

该命令打开并启动已准备的隔离 Memlane 服务，然后进入含本轮真实记录的全部记忆。选择“Cognee 提炼：字符串参数约束”，查看项目 A、来源 codex、v1 和“可检索”；展开“查看摘要依据”，再打开“Codex 合成会话：类型约定与任务”。切到“原始记录”可查看真实 QA 与工具轨迹；搜索 `normalize_ticket str TypeError` 可查看当前知识命中。所有材料标为合成，不是用户日常库。

停止命令：

```sh
python3 /Users/bytedance/work/AI/memlane/scripts/open-agent-integrations-validation.py stop
```

停止会等待本轮窗口退出并核对持久停服事实。入口只启动隔离 Memlane App／Rust，不启动 Codex、官方采集 hooks 或收费 Cognee 模型调用。完整包路径在 `app-current/darwin-arm64/MemlaneOfficialAgent.app`；数据根通过 `fixture-path.txt` 定位，记录保留于 `client/data`。删除该系统临时根或 `build/` 会使体验环境失效；保留期间请勿清理它们。官方数据也保留用于查证，但服务已停止，日常 Agent 配置没有启用本轮插件。

完整验证包包含当前工作树最新 UI、helper 和 LaunchAgent；用户既有 UI／图标／F1e 修改仅纳入本机包，不纳入本次提交。真实截图还发现原始记录页计数和行类别沿用知识文案，已按 raw 类型修正并重建复核。App 搬到新 `app-final` 路径时曾服务启动超时；重建回已验证的 `app-current` 路径后真实启动通过。本轮没有声称修复任意搬包／F1e／后台许可问题。

`footprint.json` 记录主 DMG 17,642,031 bytes（约 16.83 MiB），App 39,406,336 bytes（约 37.58 MiB）；Memlane 数据及外置组件 492,336,953 bytes（约 469.53 MiB），官方合成 core 25,624,366 bytes（约 24.44 MiB）。签名组件清单首次下载声明合计 285,752,286 bytes（约 272.51 MiB），解包 491,568,381 bytes。本轮复用已验收 Rust／E5 组件，没有重新实测首次联网下载总量。另有共享的既有 Python venv 约 1.13 GiB，以及隔离 Agent cache 约 91.3 MiB；这些不在主下载包中，不能把合成根占用当成全部首次安装占用。

## 失败历史与验证边界

- 初始 hook 确实触发，失败原因是可选身份／自动项目标签初始化时序，并非缺少 SessionStart。关闭可选 project tags，采用独立 dataset；共享 Agent 记忆、代码自动索引、idle 和 auto improve 关闭，正常 capture 保持开启。配置选择只属于隔离验证。
- 首次 dataset 尚未建立时注册曾返回 403；真实正常提交随后建立 dataset。旧 PATH CLI 模型错误、1.6.5 下载超时和初始 DeepSeek schema 错误均保留。
- 首次新会话的显式 recall 曾被工作区网络 sandbox 拒绝；自动官方 hook 已召回，之后在隔离 CLI 配置启用 network_access 的另一个全新会话，显式正式 recall 通过。失败不被后续通过覆盖。
- 官方一条提炼把实际 `python3 -B` 转述成 `python3 -b`。Memlane 保留该官方原文及正确的原始出处，不悄悄改写产物；这说明提炼仍可能出现事实误差，不能由链路通过推断自动策略应默认开启。
- App 首轮探针误用 `activity`，来源截图探针曾过早进入页面；实际操作改为 `activities`／等待列表后通过。故障恢复探针曾误读 backlog 的 `total` 字段，正确官方字段是 `pending`；沿用同一个真实故障会话完成恢复，没有重跑任务造数据。
- 第一场 25 秒取消任务已自然完成，没有算作取消通过；另起真实 180 秒任务，在 active 工具事件出现后及时取消，取得实际撤销证据。
- SIGINT、服务停止、SQL 失败及启动脚本异常都是明确标注的故障验证。正常采集／模型／提炼／召回来自真实 Agent、真实模型与官方调用，未用这些夹具替代。
- 本轮未验证全部 Agent、完整代码图谱、日常自动同步、安装升级、其他崩溃、许可撤销或正式分发；这些保持原阶段边界。Q1–Q4 保留待讨论状态。

## 审查、检查与交接

应用 bits-code-guard，对本轮 10 个代码文件按持久契约和宿主生命周期分组，再跨组检查。已修复来源 namespace／受信账本绑定和启动异常清理／假停止等实质问题，复核后没有剩余高置信 P0–P2；初始缺陷与最终空结果均保留。最终 `sh scripts/go.sh test ./...`、`sh scripts/go.sh vet ./...`、受影响 race、Python 语法及 diff 检查通过。验证包构建使用 `scripts/build-client.py` 的完整 automation 包路径，不是后台探针或空壳 App。

阶段和最终交接实际执行 `python3 scripts/p3-handoff.py <阶段> --result ... --next ...`，核对生成文件存在，具体系统临时路径见本轮 handoff 回执。脚本直接生成，未通过 Skill 调用，也未修改源 Skill 的禁用属性；交接不恢复或扩展旧 Goal。本轮提交与普通推送的完整本地／远端快照保存在最终交付证据，所有用户无关修改保持未提交。
