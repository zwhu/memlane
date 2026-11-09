# 2026-11-09 整体验收：测试工程报告

结论：**冻结版本的隔离 App 手动记忆主链通过，但整体验收不通过。** 普通生产包没有可执行的本机准备入口，首次使用仍要求先选项目；另外管理者在同轮真实 daemon/Rust 上复现了请求上限及断线停止问题。不能把 test/vet/race 全绿、完整包构建成功或隔离配置包通过写成产品已可正式交付。

## 版本、环境与证据口径

- 冻结源：`build/acceptance/2026-11-09-001/source`，HEAD `99246f43e1ae7ab7c1adf3ac9e5a859dd80cfcb1` 加 [baseline.json](../../../build/acceptance/2026-11-09-001/baseline.json) 所列本机修改；结束时 528 个冻结文件 SHA-256 全部未变。
- 环境：macOS 26.6.2、Apple Silicon arm64、仓库 Go 1.27.3。未替换默认工具链，未修改业务源码或测试，也未复制认证、聊天、个人 Agent 配置或真实记忆库。
- 本轮新包：`testing/app/darwin-arm64/MemlaneAcceptance.app`，0.0.1，Bundle ID `io.github.zwhu.memlane.code.486017d0dd26`；含真实 helper/LaunchAgent，编译启用仅供验收的 automation/native probe。
- 本轮普通包：`testing/production/darwin-arm64/Memlane.app`，0.0.1，标准 Bundle ID；**无 runtime-config、无 automation**。两个包都生成完整 DMG。ad-hoc 签名，不是 Developer ID 或公证包。
- 新独立合成根：`testing/memlane-p3-code-app-5kyfxnlk/client`。起点没有业务 SQLite、组件缓存、Agent 连接或预置记忆；UI 经 Launch Services 启动。真实原文索引由 Rust worker 0.0.3 和固定 E5-base 模型完成。
- 已有 worker/model/Enola 归档只作为构件复用，经本轮签名 loopback HTTP 源供 App 实际下载、校验和安装；**不是公开首次网络下载**。夹具包含 `code_extractor_id=enola-code`，实际准备三组件；未打开代码图谱入口。
- UI 操作复用冻结源码 `scripts/p1-ui.py`、`mac-native.py`。夹具按 `create-p4-code-fixture.py` 适配输出路径；专用脚本只写在 testing。全部动作与结果存于 [control-commands.jsonl](../../../build/acceptance/2026-11-09-001/testing/control-commands.jsonl)，关键断言汇总于 [runtime-results.json](../../../build/acceptance/2026-11-09-001/testing/runtime-results.json)。`control-commands.jsonl` 是审计记录，不应在已有 v3 根上重放首次保存步骤。

## 检查与结果

| 编号 | 需求 / 风险 | 本轮方法与结果 | 证据 |
| --- | --- | --- | --- |
| TEST-01 | 基础逻辑回归 | `sh scripts/go.sh test ./...`，exit **0**；未修改用例来获得通过 | [go-test.log](../../../build/acceptance/2026-11-09-001/testing/go-test.log) / `go-test.exit` |
| TEST-02 | 静态问题 | `sh scripts/go.sh vet ./...`，exit **0** | [go-vet.log](../../../build/acceptance/2026-11-09-001/testing/go-vet.log) / `go-vet.exit` |
| TEST-03 | 本轮后台可用性变更的并发风险 | `CGO_ENABLED=1 sh scripts/go.sh test -race ./internal/desktop ./internal/daemon`，exit **0** | [go-race.log](../../../build/acceptance/2026-11-09-001/testing/go-race.log) / `go-race.exit` |
| TEST-04 | 构建与包内 helper/LaunchAgent | 冻结源执行 builder。隔离包用 `--runtime-config <新根>/runtime-config.json --identifier io.github.zwhu.memlane.code.486017d0dd26 --name MemlaneAcceptance --automation`；普通包只用 `--out ../testing/production`。两个构建均 exit **0**；两包 `codesign --verify --deep --strict` 与两张 DMG 的 `hdiutil verify` 均 exit **0** | [build-command.json](../../../build/acceptance/2026-11-09-001/testing/build-command.json)、[build.log](../../../build/acceptance/2026-11-09-001/testing/build.log)、[production-build.log](../../../build/acceptance/2026-11-09-001/testing/production-build.log)、[package-integrity.json](../../../build/acceptance/2026-11-09-001/testing/package-integrity.json) |
| TEST-05 | 空库首启不能提前启动业务或预置数据 | 实际检测结果页仍无 SQLite；确认准备后真实组件/helper/Rust ready，knowledge=0、raw=0、connections=[]，只有两个全局 scope。通过；不包含官方 Cognee/Agent 就绪 | `runtime-results.json` 的 discovery / preparation / empty-real-service-ready；图 01–06 |
| TEST-06 | 手动新增：保存与可检索分别成立 | 在 App 新增项目、手写一条知识，IPC 核对 v1/全文/项目，再等 `index_state=indexed`。通过 | manual-save-indexed；图 08–10 |
| TEST-07 | 草稿与编辑、旧版本不能误当当前搜索结果 | 编辑未保存点“返回”后持久正文仍 v1，再次编辑草稿保留；UI 保存 v2、UI 搜索当前 token，Rust `library_search` 核对目标 memory/revision=2/全文。通过 | edit-search-current；图 11–12 |
| TEST-08 | 恢复确认边界与历史不可覆盖 | 历史页点“恢复 v1”时当前仍 v2；点确认后新增 v3，正文=v1，history=[3,2,1] 且 v2 全文保留。通过 | restore-confirm-no-mutation / restore-new-revision；图 13–15 |
| TEST-09 | 关闭、退出、重启与持久停止 | 关闭窗口后 native.visible=false、health ready；重开同进程。默认退出后 worker 继续就绪；新 App PID 读取 v3/历史。设置停止后 socket 消失、stopped=true、旧 worker 退出；明确启动产生新 worker。关闭“退出后继续运行”后普通退出也停服，该设置跨重启保持；最后恢复默认设置，明确停止服务并退出。通过 | runtime-results 后七项；图 20–25；[final-integrity.json](../../../build/acceptance/2026-11-09-001/testing/final-integrity.json) |
| TEST-10 | 项目三视图/Agent/设置真实状态 | 已发现 Codex 0.167.0-alpha.3.2；未创建或伪装已验证连接。实体、关系图、原始记录为真实空态。入口/状态展示抽样完成，非空图与来源阅读器未验证 | 图 07、16–20；project-three-views-empty-materials / settings-and-agent-status |

实际 UI 请求均保留 `ok=true` 回执；这只与对应持久读取、进程事实合并作为通过依据。最终 SQLite 只读 `integrity_check=ok`、schema=15、memories=1、versions=3、operations=3，connections/organization_tasks/cognee_bindings 等均为 0。App 与 helper/worker 的本轮归属进程清理后无残留，服务 socket 不存在，`stopped=true`、`stop_generation=3`。普通包单独退出亦无残留，两个清理没有影响其他实例。

## 可复现缺陷与交付阻塞

### TEST-11：普通生产包无法进入本机组件准备（P1，交付阻塞）

前置：按标准 builder 生成的普通 `Memlane.app`，包中没有 `Contents/Resources/runtime-config.json`，不传 `--p0-config`；本轮以新的 `testing/production-home` 启动，未提供运行数据根。

复现：`python3 scripts/build-client.py --out ../testing/production` → Finder 等价 Launch Services 打开实际包 → 点击设置。

预期：用户可以从新安装 App 进入真实本机准备与后续使用。实际：直接落到普通空首页；设置显示“服务未连接”“本地引擎：待准备”“当前尚无可安装的组件版本”，没有准备/启动服务入口，备份和索引重建不可用。包可以运行但不能由此走到可保存/可检索状态。

当前源 `cmd/memlane-app/main.go:182` 仅在参数或包内存在运行配置时创建 controller、配置 library/首次引导；标准 builder 只有显式 `--runtime-config` 才复制该文件。这解释了普通包与隔离包表现差异。不能用独立 fixture 注入配置的成功抵消该阻塞。

证据：[production-runtime.json](../../../build/acceptance/2026-11-09-001/testing/production-runtime.json)、[普通包首页](../../../build/acceptance/2026-11-09-001/testing/production-screenshots/01-production-home.png)、[普通包设置](../../../build/acceptance/2026-11-09-001/testing/production-screenshots/02-production-settings.png)、同名 `-ax.json`。该普通包未使用自动化编译，采用系统窗口截图/AX 点击；验证脚本 exit 0 仅表示成功观察并清理，**不表示产品主链通过**。

### TEST-12：首次使用和 Agent 连接仍强制项目前置（P1，产品主链阻塞）

前置：新隔离根经真实准备 ready，尚无项目/连接。

复现：准备完成 → 查看主按钮 → 点击 → 总览 → Agent → Codex。实际主按钮仍为“选择项目并开始记录”，进入添加项目页；Codex 已实际发现，但 `connect-agent.states=[disabled]`，没有跳过项目入口。scopes 仅“个人偏好”“全局经验”，connections=[]。

预期依产品最新规则和 Q1–Q3：可先选 Agent，项目可跳过，连接后有有效数据再建立/复用项目。当前 `internal/desktop/first_launch_presentation.go:397` 仍导向项目；`internal/desktop/agents.go:374` 将空项目直接列为禁用条件。此为已确认需求未落实，不是等待窗口加载或 fixture 故障。

证据：[准备完成](../../../build/acceptance/2026-11-09-001/testing/screenshots/04-preparation-terminal.png)、[进入项目](../../../build/acceptance/2026-11-09-001/testing/screenshots/05-start-use-project.png)、[空项目 Codex](../../../build/acceptance/2026-11-09-001/testing/screenshots/07-agent-without-project.png) 及状态 JSON、empty-project-agent。

### TEST-13：长期请求后检索不可用，但健康状态继续 ready（P1，管理者本轮独立复现）

此项不是测试工程师重复运行，也不是历史证据。已阅读管理者本轮独立真实 worker/daemon 报告：同一 worker 连接成功处理 10000 请求后，第 10001 个请求报 `duplicate_id: request IDs must be unique; reconnect after 10000 requests`，新 ID 也被拒绝；shutdown 同样被拒绝。daemon 连续 health 后 search 报相同错误，但后续 health 仍为 ready。空数据请求足以复现，影响长期运行的检索可用性与健康状态可信度。

复现输入、真实二进制/归档 SHA、响应与清理见 [manager-worker-limit/report.json](../../../build/acceptance/2026-11-09-001/manager-worker-limit/report.json)、[manager-daemon-limit/report.json](../../../build/acceptance/2026-11-09-001/manager-daemon-limit/report.json)。管理者已清理独立进程。该反例不否定 TEST-06–09 的有限新连接主链，也不能被有限主链通过覆盖。

### TEST-14：stop 调用方断线后持久停止状态与进程不一致（P1，管理者本轮独立复现）

管理者向同轮独立真实 daemon 发出 stop 后断开调用连接，报告显示 `stopped=true`、`stop_generation=1` 已落盘，daemon 仍存活，health 仍 ready 且 worker 存活。预期停止的持久状态和实际执行生命周期收敛；该场景可能对停止与后台恢复造成歧义。此处仅转述已读取的当前报告，不声称 GUI 正常退出曾触发该故障；本轮 App 正常菜单停止路径实际通过。

证据同 [manager-daemon-limit/report.json](../../../build/acceptance/2026-11-09-001/manager-daemon-limit/report.json) 的 `persisted_after_disconnected_stop`、`daemon_alive_after_disconnected_stop`、`health_after_disconnected_stop`。清理 exit 0、socket 消失、worker 已退出。

## 截图与视觉边界

实际打开并对照了冻结源 `docs/images/home.jpg` 与 `docs/images/memory-detail.jpg`。本轮截图使用系统窗口捕捉，不是渲染 fixture。默认 1200×820；准备确认最小窗口为 980×620，底部操作可见。完整设计评价由本轮 [设计报告](design.md) 和 [交互报告](interaction.md) 负责；这里不以截图代替保存断言。

| 状态 | 新截图 |
| --- | --- |
| 检测结果默认折叠 / 确认 / 最小窗口 / 准备中 / ready | `testing/screenshots/01-*`、`02-*`、`02b-*`、`03-*`、`04-*` |
| 空首页 / 有一条真实保存数据首页 | `06-home-empty.png` / `10-home-with-memory.png` |
| 三栏详情 / 编辑 / 搜索 / 历史 / 恢复确认 | `09-*`、`11-*` 至 `15-*` |
| 实体空态 / 关系空态 / 原始记录空态 | `16-*`、`17-*`、`18-*` |
| 无项目 Agent / 有项目 Agent / 默认设置 | `07-*`、`19-*`、`20-settings-ready.png` |
| 关闭重开 / 新进程历史 / 停服 / 退出选项 / 重启服务 | `21-*` 至 `25-*` |

截图目录为 [testing/screenshots](../../../build/acceptance/2026-11-09-001/testing/screenshots)，每张有同名 `-state.json`，包括 native 尺寸、UI tree、App PID 与首启状态。03 是真实活动阶段 fast capture；其后 state 采集可能已进入 ready，不能将后采 JSON 当作与 PNG 同时刻的进度。全白方块检测瞬态未捕获，不宣称动效完整验收。

本轮未生成官方材料，因而完整原文阅读器、非空实体/关系图及任务来源导航均未重跑；真实 raw=0 不能靠合成 UI 示例填充。这些属于未验证范围。详情中的使用记录先于出处、项目列表较松、无结果搜索仍保留选中详情等抽样观察交设计/交互报告列为可延后问题；没有为本轮扩范围修改布局。

## 主包、准备下载与磁盘占用

[package-size.json](../../../build/acceptance/2026-11-09-001/testing/package-size.json) 在本轮明确停服后测量。均为十进制 MB。

| 口径 | 字节 | MB | 边界 |
| --- | ---: | ---: | --- |
| 普通主 DMG | 17,985,512 | 17.99 | 不含外置引擎/模型；SHA-256 `5feb423937174600b1e206cedbad3aa5e1842d70f73fe33932bb300369b8c105` |
| 普通 .app 逻辑文件 | 40,176,879 | 40.18 | UI、helper、CLI、LaunchAgent 等主包资源 |
| **本轮首次准备实际归档合计** | **301,171,513** | **301.17** | worker/model/Enola 三组件，已有归档经本地源实际下载；Enola 因沿用夹具额外安装，首版代码图谱入口隐藏 |
| 其中 Rust worker + E5 模型归档子集 | 289,451,544 | 289.45 | 69,952,582 + 219,498,962；不代表本轮全部下载 |
| 准备后的合成 client 根逻辑文件 | 557,081,278 | 557.08 | 本轮组件、模型、业务数据、设置、缓存 |
| 普通 App + 本轮合成根逻辑总量 | 597,258,157 | 597.26 | 普通包文件 + 隔离 automation 包准备的根，不表示普通包已自行完成准备 |
| 上述总量 OS 分配块 | 605,605,888 | 605.61 | st_blocks，不含目录元数据；APFS 共享块不等于可释放空间 |

初检/确认页显示 worker+model 的 289.45 MB，而该含 Enola 配置的实际准备状态合计为 301.17 MB。此为本夹具配置下可观察的口径差异，未外推成默认正式组件源的下载量。以上首次准备量**不含官方 Cognee、BGE、Agent plugin 的首次准备量**。未测公开分发首装网络开销、失败重试、Python 替代方案的本轮最终占用。

## 未执行、阻塞与历史证据

- 阻塞：TEST-11 普通包不能自行准备；TEST-12 当前首次业务路径不符合新规则；TEST-13/14 管理者同轮反例；Developer ID、公证及正式更新源仍未具备。
- 未执行：真实 Codex/traex/pi/TRAE IDE 日常任务、官方 Cognee 绑定与实际 Agent 验证、跨 Agent 读写、初始化/整理模型任务、建议确认/拒绝、非空图和完整原始记录阅读器。未使用已有凭证复制来绕过隔离；初检“已安装”仅是发现事实。
- 未扩展：长时后台/系统重启/权限撤销及其他 F1 场景、升级回退、备份恢复和磁盘满的本轮 App 重跑。Go 全仓测试覆盖其中部分机制，但不能替代其真实窗口验收。父代理明确不再增加 launchd 失败循环探针，本轮保持已完成主链与精确清理。
- 可延后：视觉密度、非关键状态文案等见专职报告。首启动效瞬态与窄窗其他页面没有完整采样，不按通过计算。
- 历史 P0/P1/P2/P3/P4 与 onboarding-memory 文档只用作契约和现有驱动入口；本报告没有把旧截图或旧 Agent 调用搬进当前实测结果。历史真实性与阶段通过边界由管理者汇总。以上 TEST-13/14 例外明确是管理者**当前同轮独立实测**。

本轮新增持久交付仅为本报告与 testing 下验收脚本、日志、截图、包和合成根。没有业务修复、没有新写单元测试、没有提交或推送。`final-integrity.json` 已核对冻结源码不变和最终合成库状态；普通包独立清理结果见 `production-runtime.json`。
