# Memlane Native 首屏 20× 性能研究与审查修正

2026-11-22，源码基线 `df3472c`，MyGo `v0.7.4` + 项目渲染补丁。测试环境为 Apple Silicon M3 Pro、内置 120 Hz ProMotion 屏幕、`1200×820` 原生窗口，连接本机单一日常库与在线后台服务，通过 Metal `MTLDrawable.addPresentedHandler` 的 `presentedTime`、MyGo 帧统计、系统统一日志与只读探针拆解端到端耗时。

## 1. 20× 验收口径、样本基线与可见性边界

**真实首屏定义**：主窗口已可交互，且视口内**以非零不透明度实际呈现**已确认当前授权的真实侧栏项目、记忆分布热力图、最近记忆与近期动态。视口外内容与非首屏设置探针可延后加载；骨架屏、未重验当前项目授权的隐藏前内存旧快照、以及因入场动画仍处于全透明态（`opacity = 0`）的内容均不能计作首屏完成。

| 启动场景与测量分群 | 首个可见帧 | 就绪状态首帧（注 1） | 按 20× 折算耗时 |
| --- | ---: | ---: | ---: |
| 同版本新进程直启（轻量帧统计包，`n=3`） | 中位数 631 ms（625–666 ms） | 中位数 1,081 ms（1,075–1,116 ms） | 约 32 ms / 54 ms |
| 同版本新进程直启（含阶段与 HTTP 打点包，`n=3`） | 中位数 654 ms（580–676 ms） | 中位数 1,705 ms（1,621–1,864 ms） | 约 33 ms / 85 ms |
| LaunchServices 打开同版本包（`open -n`，`n=3`，注 2） | 中位数 885 ms（743–939 ms） | 中位数 1,243 ms（1,210–1,248 ms） | 约 44 ms / 62 ms |
| 本地 ad hoc 新构建包首次执行（两个独立包各 1 次） | 3.904 s / 3.107 s | 5.662 s / 6.382 s | 约 195–155 ms / 283–319 ms |

- **注 1（历史「就绪状态首帧」的全透明裁剪失真与动效互斥）**：上表历史「就绪状态首帧」取 `Loaded && ActivityLoaded` 首次进入 `App.View` 后提交的首个非零 `presentedTime`。代码与 GPU 批次日志核对证实：当 `overviewBootstrap` 首次完成并递增 `motionRuns`（[overview_ui.go](../../internal/simpledesktop/overview_ui.go#L288)）时，首帧由 `poseEnterTracker`（[motion_tokens.go](../../internal/simpledesktop/motion_tokens.go#L65)）对整个 `overview-page` 施加初始姿态 `Fade: 1`（即 `opacity = 0`），该帧 `paint = 0.0 ms`，GPU 绘制实例从骨架态的 `633 instances / 53 batches` 骤降至 `121 instances / 18 batches`（仅剩左侧栏，右侧总览视口因全透明被完全剔除为空白）。随后 `v4StaggerEnter` 对前 8 条记忆/动态施加 `40 + 28×i ms`（`40–236 ms`）的全透明延迟，热力图波浪生长（[overview_ui.go](../../internal/simpledesktop/overview_ui.go#L1019)）按 `cell.w × 7 ms` 推进，使代表「今天」的第 52 周在 `ready + 364 ms` 前 `alpha = 0`。**因此，若保留现行冷启动入场动效延迟，即使进程启动与数据读取耗时为 `0 ms`，在数学上也不可能于 `54–62 ms` 内呈现非透明真实内容。** 正式验收须将 `(sceneEpoch, overviewGeneration, visibleAlpha > 0)` 绑定到具体 drawable，并同步裁决冷启动/恢复窗口时的首屏直出或零延迟动效策略。
- **注 2（启动计时起点对称性）**：`open -n --stderr --env` 采样包含了测试脚本创建文件与拉起 `/usr/bin/open` CLI 的外部开销（`launch → main` 约 `75–115 ms`，而 `Popen` 直启仅 `11–17 ms`），且标准错误重定向属于调试路径；正式验收须统一使用 `mach_absolute_time` 单调时钟并独立覆盖真实 Finder / Dock 启动。

**可行性与立项裁决**：
1. **普通新进程冷启动目前无达到 `54–62 ms` 的实测路径**：单是暖缓存下 `Go main → MyGo OnReady` 直启即需 `126–175 ms`、LaunchServices 需 `259–347 ms`。新进程冷启动建议第一阶段优先消除客户端自踩踏造成的秒级双峰长尾（如上表两批直启样本 `1,081 ms` vs `1,705 ms` 的 `624 ms` 断崖），将首个可见帧压至 `P50 ≤ 350–400 ms`、非透明真实首屏压至 `P50 ≤ 450–550 ms`。
2. **驻留窗口恢复（`⌘W` 隐藏重开）须先补齐授权核验与后台止漏**：现有 `⌘W`（[main.go](../../cmd/memlane-app/main.go#L230)）隐藏窗口保留阅读树，虽可在 1–2 帧内显示旧画面，但存在两项硬约束：① 隐藏期间总览与队列轮询暂停，唤醒首帧绘制的是**未重验当前授权与最新采集的隐藏前旧快照**（`SetWindowActive(true)` 在后台异步重拉目录与总览，[overview_refresh.go](../../internal/simpledesktop/overview_refresh.go#L15)），不能把未重验的旧缓存直接计为达标；② 审查发现 [app.go](../../internal/simpledesktop/app.go#L747) 的 `viewContent` 无条件注册 `c.After(2 * time.Second)` 且未检查 `a.windowInactive` 即每 15 秒调用 `RefreshProjectDirectory()`（触发服务端同步 `ps` 扫描），外加约 `187 MiB` 客户端常驻 RSS。驻留恢复须单独作为产品与资源方案评估，并先行修复隐藏态后台唤醒漏点。

## 2. 首次执行秒级耗时拆解（3.90s / 5.66s 与 3.11s / 6.38s）

| 阶段 | 构建包 A 首次执行 | 同包三轮复跑 | 归因证据与边界 |
| --- | ---: | ---: | --- |
| 启动请求 → LaunchServices `CHECKIN` | 2.076 s | 85–87 ms | 系统日志确认 `syspolicyd` 对同路径哈希执行 `GK performScan` 用时 **1.827 s**（其中同线程 `CFNetwork` 在线请求约 `1.504 s`、`XProtect` 交互约 `255 ms`），复跑不再触发 |
| `CHECKIN` → 设为前台 | 803 ms | 363–367 ms | 系统日志确认差值；首次激活的 AppKit / WindowServer 细分栈待采样 |
| 第一帧主线程 / `present` | 686.9 ms / 665.0 ms | 40.1–42.5 ms / 17.4–19.9 ms | 同 PID 的 `MTLCompilerService` 记录多次管线编译与 `present` 区间重叠；首次 `drawable` 记录 `presentedTime=0`，`3.904 s` 为第二帧首个非零呈现 |
| 首个可见帧 → 就绪状态首帧 | 1.758 s | 约 450 ms | 该包未打细分 HTTP 时间戳，保留为未分解区间 |

**构建包 B（含阶段与 HTTP 打点）首次执行**同样触发 Gatekeeper 评估，首帧 **3.107 s**、就绪状态首帧 **6.382 s**：`launch → main` `969 ms`，`main → OnReady` `277 ms`，`createWindow` `540 ms`，`AttachGraph` `195 ms`。其首帧与数据阶段暴露了两项关键事实：
- **冷启动首帧并非纯 GPU PSO 编译开销**：Frame 1（总计 `571.5 ms`，`presentedTime=0` 未上屏）除 `present 449.4 ms` 外，**`layout` 独占 `106.6 ms`**（97 次文本排版触发的 CoreText / 字体服务冷缓存开销，复跑仅 `9.0–17.4 ms`）；而真正首个上屏的 Frame 2 耗时 `129.8 ms`，其中 **Go 侧 `build` 占 `127.5 ms`、`present` 仅 `0.9 ms`**。此外，两个临时测试包均改写了全新 ad hoc `CFBundleIdentifier`，导致系统级按包 Metal 缓存冷未命中；Apple `MTLBinaryArchive` 强绑定具体 GPU 架构与系统驱动版本，开发机预构建产物无法在跨代 Apple Silicon 或不同 macOS 版本的用户首装时命中，仅同机持久缓存对二次启动有辅助作用，不应写入首装交付承诺。
- **独立的服务端事件循环争用可将首屏再拖慢逾 3 秒**：该轮并发的第二次 `/v1/projects` 与 `/v1/overview/bootstrap` 等待响应头分别达 **3,128 ms** 与 **3,361 ms**，而响应体读取仅 `13.8 ms`、JSON 解码 `7.5 ms`、Go 聚合 `7.9 ms`（具体争用链路见第 4 节）。正式发布包须在 Developer ID 签名、公证与 ticket 装订条件下独立采样 Finder 首开。 [Apple 启动性能指南](https://developer.apple.com/documentation/xcode/reducing-your-app-s-launch-time) · [Gatekeeper](https://support.apple.com/guide/security/gatekeeper-and-runtime-protection-sec5599b66df/web) · [macOS 软件公证](https://developer.apple.com/documentation/security/notarizing-macos-software-before-distribution)

## 3. 先剪窗口启动路径（`OnReady → Show` 可直接削减约 170 ms）

| 位置 | 实测与源码证据 | 优化措施与契约边界 |
| --- | --- | --- |
| `main → OnReady` | 直启 `126–175 ms`（配置读取 + `simpledesktop.New` 合计约 `9–15 ms`），LaunchServices `259–347 ms` | `New()` 内 `initGraph`（[graph.go](../../internal/simpledesktop/graph.go#L189)）当前在 `main` 同步读取并 SHA-256 校验全部图谱静态资产，可移至后台校验；同时让首屏只读请求在 `App.Run()` 前尽早与 AppKit 初始化并行 |
| `OnReady → Show` 总览 | 直启 `255–269 ms`（其中 `createWindow` `70–81 ms`、`NewTray` `9–16 ms`），LaunchServices `208–230 ms`（`createWindow` `42–44 ms`） | `Show()` 前仅保留主窗创建、关闭拦截与开机自启来源判定（`WasOpenedAtLogin`）；其余非首屏初始化全部移至首屏稳定后 |
| ① 图谱 `WebView` 挂载 | `AttachGraph` 同步耗时 **`84–99 ms`**（首次冷启动 `195 ms`）。`n=3` 交替对照首帧 `[580, 654, 676] ms` → `[491, 500, 607] ms` | 改为延迟挂载 `win.NewWebView` + `LoadURL`，并在首屏入场完成后空闲预热（避免把 `100–200 ms` 卡顿转嫁给首次点击「图谱」）。**注意 SDK 约束**：`mygo.BindAs("OfficialGraph", ...)` 与 `mygo.Protocol.Handle("memlanegraph", ...)`（[graph.go](../../internal/simpledesktop/graph.go#L129)）须继续在 `App.Run()` 前完成注册，同时保持沙箱导航拦截、切页取消与 `win.OnClosed` 单次绑定 |
| ② 菜单与登录项状态读回 | `SetMenu + RefreshOpenAtLogin` 在直启与 LS 样本中稳定耗时 **`73–89 ms`**（主要为 `SMAppService` 登录项开启状态同步查询），与 `AttachGraph` 等重 | 登录项开启状态仅在托盘弹层（点击时 [main.go](../../cmd/memlane-app/main.go#L164) 已实时重查）与设置页显示，与首屏无关；将 `RefreshOpenAtLogin()` 移出 `win.Show()` 前即可零风险省去约 `85 ms` |
| 第一帧与数据首轮布局 | 常态第一帧主线程 `40–43 ms`（`layout` `9–17 ms`、`present` `17–25 ms`）；数据就绪帧 `layout` `16.8–21.7 ms`（约 228 次文本排版），入场期主线程中位数 `0.9 ms`、P95 `2.3–3.0 ms`（120 FPS） | 优先收敛数据到达帧的 228 次文本排版与 `2.5×10⁴` 次分配（仅构建视口与最小预绘行、固定双行槽位高度），保持与现行视觉布局一致 |
| 渲染路径同步文件检查 | `overviewFirstUseDays` 首次调 `os.ReadFile`；`viewContent → syncReadingIdentity → readCachedToken` 每帧/定时同步 `Lstat`/`Stat` | 首用天数在后台预读；身份快照结合轮换/撤权事件失效，严守凭据权限变更即时撤下正文的红线 |

[启动入口](../../cmd/memlane-app/main.go#L18) · [图谱宿主](../../internal/simpledesktop/graph.go#L122) · [总览视图](../../internal/simpledesktop/overview_ui.go#L578) · [首用天数](../../internal/simpledesktop/overview.go#L30) · [凭据缓存](../../internal/simpledesktop/http_transport.go#L74)

## 4. 拆除客户端启动并发风暴，审慎演进首屏投影

### 4.1 秒级响应头长尾与重复 `/v1/projects` 的已证实根因

单点空闲探针表明服务端数据读取本身并不慢，真正的秒级等待来自**桌面端在启动前 `700 ms` 内向单线程 `project_control_server` 事件循环连续打出的三波自踩踏风暴**：

| 只读探针与对照实验 | 实测结果 | 诊断结论 |
| --- | --- | --- |
| 独立连续请求 `GET /v1/projects`（8 次） | `61.7–93.0 ms`，`50 KB` | 单一前置请求已超出 `54 ms` 的 20× 全程预算 |
| 同一注册表快照直接调用 `Registry.directory()` | `0.59–2.19 ms`（精简目录约 `1.7 KB`） | 项目与权限目录本身极轻 |
| 同配置调用 `CodexConnection.view()` | `54.0–65.9 ms`（同步 `ps -axo` 占 `42.0–52.2 ms`，TOML 解析 `10.1–12.0 ms`） | 设置页专用的宿主进程扫描在 `/v1/projects` 内持 `registry.mutex` 同步阻塞事件循环，必须从首屏目录拆离 |
| 独立连续请求 `GET /v1/overview/bootstrap`（8 次） | `36.8–96.3 ms`，约 `1.06 MB` | 空闲服务下总览投影可在百毫秒内完成 |
| 独立进程直接执行 `_read_overview_snapshot`（5 次） | `28.3–31.9 ms`（919 文档 `746 KB`、143 会话 `43 KB`、30 预览 `17 KB`、250 动态 `130 KB`） | `data` 与 `session_records` 均已命中 `dataset_id` 索引，`pipeline_runs` 命中创建时间索引，无需盲目改 SQLite 索引 |
| 真实 UI 启动同期的 `bootstrap` 等待响应头（TTFB） | LS 组 `293–432 ms`（1 次目录）；打点直启组 **`925–1,163 ms`**（2 次目录）；新构建首次 **`3,361 ms`** | 由下方客户端启动三波并发请求阻塞单线程 `asyncio` 事件循环所致 |

**源码与时序完整还原的三波自踩踏链路**：
1. **重复 `/v1/projects` 的精确来源**：`win.Show()` 后 `app.Start()`（[app.go](../../internal/simpledesktop/app.go#L195)）发出第 1 次 `/v1/projects`，约 `80–105 ms` 返回并将 `a.access.Loading` 置回 `false`；数十毫秒后 WindowServer 异步送达 `win.OnFocus`（[main.go](../../cmd/memlane-app/main.go#L236)）→ `SetWindowActive(true)`（[overview_refresh.go](../../internal/simpledesktop/overview_refresh.go#L15)）**未检查 `a.access.Next`（15s 节流窗）便无条件再调 `RefreshProjectDirectory()`**，绕过在途锁发出第 2 次 `/v1/projects`，在服务端再次同步执行 `ps -axo` 冻结事件循环约 `60 ms`（LaunchServices 组因焦点事件落在第 1 次请求在途期内被 `Loading` 吸收，故仅 1 次）。
2. **第 1–2 波并发争抢（拉高至 `293–432 ms`）**：
   - **第 1 波（`Start()`）**：`refreshServiceMonitor()`（[service_monitor.go](../../internal/simpledesktop/service_monitor.go#L107)）并发请求官方 `/health` 与控制面 `/v1/runtime/monitor`；后者在服务端拉起最多 **3 个 `ps -axo` 子进程**，并在事件循环**同步执行 `os.walk` 遍历图数据库目录、`scandir` 统计积压文件及读取运行日志正则脱敏**（[service_monitor.py](../../engine/python/service_monitor.py#L172)、[#L474](../../engine/python/service_monitor.py#L474)）。
   - **第 2 波（`a.access.Loaded` 首帧）**：紧随第 1 次 `/v1/projects` 返回，下一帧在发起 `/v1/overview/bootstrap` 的**同一帧**内，侧栏 `overviewNavigation` 调用 `refreshSidebarQueueIfDue`（[overview_navigation.go](../../internal/simpledesktop/overview_navigation.go#L40) → [project_queue_badge.go](../../internal/simpledesktop/project_queue_badge.go#L39)），立即并发拉起**最多 3 个项目的 `/v1/projects/{id}/message-summary`**（争抢 `asyncio.to_thread` 工作线程与 SQLite 只读连接）及**最多 4 个 `runPendingMaterials` Python 子进程**（[project_queue.go](../../internal/simpledesktop/project_queue.go#L334)）。
3. **第 3 波 `Start() + 700 ms` 定时炸弹（制造 `925–1,163 ms` 双峰断崖）**：[app.go](../../internal/simpledesktop/app.go#L199) 使用硬编码 `time.AfterFunc(700*time.Millisecond, ...)` 触发 `RefreshIntegrations()`，并发请求 `/v1/generation` 与 `/v1/connections?force=1`（[integrations.go](../../internal/simpledesktop/integrations.go#L457)）。服务端 `Connections.checked_view(force=True)`（[agent_connections.py](../../engine/python/agent_connections.py#L524)）在单事件循环上**调用两次 `Connections.view()`**——同步执行 **2 次 `CodexConnection.view()`（2× `ps` + 2× TOML 解析 ≈ `110–130 ms` 纯事件循环冻结）+ 全宿主插件逐文件 SHA-256 校验**，并穿透缓存拉起 `ConnectionCapture` 子进程探测。凡赶在 `Start() + 700 ms` 前完成 `bootstrap` 的样本总耗时为 `916–1,248 ms`；凡因第 2 次 `/v1/projects` 推迟而跨过 `+700 ms` 边界的样本直接撞上该事件循环冻结，`bootstrap` TTFB 跃升至 `925–1,163 ms`，总耗时跳变至 `1,621–1,864 ms`。

[目录路由](../../engine/python/project_control_server.py#L118) · [Codex 进程扫描](../../engine/python/project_codex_connection.py#L125) · [宿主连接检查](../../engine/python/agent_connections.py#L467) · [三层监控快照](../../engine/python/service_monitor.py#L440) · [当前总览路由](../../engine/python/v2_api.py#L901) · [召回存储](../../engine/python/knowledge_recall.py#L171)

### 4.2 首屏数据路径演进约束

1. **先做零风险拆障：拆离 `CodexConnection.view()` 并按真实首屏完成事件门控后台任务**。从 `/v1/projects` 移除仅设置页消费的同步 `CodexConnection.view()`，使权威目录读取回归 `<5 ms`；修复 `SetWindowActive(true)` 遵守 `a.access.Next`；将 `refreshServiceMonitor`、`refreshSidebarQueueIfDue`、`restoreCodeState` 与 `RefreshIntegrations` 全部改为在首屏 `overviewBootstrap` 呈现稳定后按序触发，同时将 `service_monitor.py` 与 `agent_connections.py` 中的同步磁盘/进程扫描移入后台线程。
2. **精简首屏载荷时必须保留 4 项隐式全量/跨接口真值**。当前 `bootstrap` 约 `1.06 MB` 中 `documents` 占 `746 KB`，若下钻为「轻量首屏响应 + 按需分页补齐」，不能简单只传 Top-20 文档与 365 天桶，必须同时精确给出：
   - **全历史总记忆数**（含早于 365 天热力图窗口及 `MissingDates` 的记忆，不能用 365 天桶求和替代，见 [overview_ui.go](../../internal/simpledesktop/overview_ui.go#L883)）；
   - **去重后的全量原始记录数**（跨会话与非派生原始文档按身份去重，见 [overview.go](../../internal/simpledesktop/overview.go#L175)）；
   - **经足够窗口（现为 250 条）完成父子操作折叠与连续失败归并后的动态列表**（不能在 SQL 层直接截断为前 20 条原始 `pipeline_runs`，旧实现见 `3436cf9:internal/simpledesktop/overview_bootstrap.go`）；
   - **首屏近期记忆与侧栏项目的未读状态**（现行 `itemIsUnread` 与 `unreadProjectBadge` 依赖 `projectQueueState.KnowledgeRead`，见 [knowledge_unread.go](../../internal/simpledesktop/knowledge_unread.go#L196)，须避免延迟队列后导致未读蓝点与徽标二次跳变）。
3. **严守 `503` 回退解耦、跨库修订复核与日期口径单一真相**：
   - **禁止合并接口整包 `503` 拖垮项目目录**：按架构契约，SQLite 投影遇 schema 不匹配或锁超时须 fail-closed 回退官方 Cognee HTTP（`loadOverviewLegacy`）。若合并目录与首屏投影，投影降级时仍须正常返回权威目录并标记 `projection_unavailable`，否则客户端缺失 `a.Config.Projects` 将导致侧栏与官方 HTTP 回退同时失效。
   - **跨库读取后的修订复核**：`overview_bootstrap` 横跨 `cognee_db`、`lesson-provenance.sqlite3` 与 `knowledge-recall.sqlite3` 三个独立 SQLite 库，须在 `asyncio.to_thread` 读取前后复核 `registry` 管理修订与授权数据集未发生撤权/解绑变更，并在 UI 接收与 drawable 呈现两处校验代次。
   - **消除 `rememberKnowledgeDays` 的隐藏同步落盘 `fsync` 与日期桶分裂**：现行 `buildOverviewFromBootstrap` 调用的 `rememberKnowledgeDays`（[knowledge_dates.go](../../internal/simpledesktop/knowledge_dates.go#L52)）在无新记忆复跑时仅耗时 `6.5–11.4 ms`（命中 `!changed` 早退），但**一旦存在新提炼记忆便会在上屏前同步执行 `CreateTemp + Sync() + Rename`**；同时若改为服务端计算热力图日期桶，须先核对并收敛客户端私有 `knowledge-source-dates-*.json` 与外置 `provenance_store` 的历史映射，确保总览桶计数与项目页日期完全一致。
4. **服务端内存物化仅作兜底备选**：仅在完成上述拆障与载荷精简后实测仍无法达标时再评估；候选只允许缓存可重建的轻量元数据，严禁桌面落盘缓存副本（工程门禁 `ML012`），且须基于存储水位、管理目录修订与服务身份验证确定性失效，严禁按 TTL 推断可见性。

[当前 Go 首屏聚合](../../internal/simpledesktop/v2_client.go#L278) · [历史日期锁](../../internal/simpledesktop/knowledge_dates.go#L52) · [未读状态读取](../../internal/simpledesktop/knowledge_unread.go#L196) · [开发说明与 ML012](../architecture/development.md)

### 4.3 为什么点击进入 `Memlane`（1.51–1.85 s）比 `item-fe`（0.71–0.78 s）慢

实窗点击侧栏切换项目时，两者入场帧率均为 `117–120 FPS`，等待全部发生在数据准备阶段：
- **串行目录分页差异**：按现行官方接口 `limit=100` 串行拉取，`Memlane` 枚举 **853 个文档（9 个串行请求、约 `889 KB`、`601–639 ms`）**，其中可见知识目录为 645 条；`item-fe` 枚举 **16 个文档（1 个请求、约 `14 KB`、`57–61 ms`）**，可见知识目录为 9 条。
- **`~650–1,200 ms` 额外等待的真因**：`item-fe` 目录仅 `60 ms` 却仍耗时 `710–780 ms`，是因为 `loadKnowledgeFeed`（[knowledge_waterfall.go](../../internal/simpledesktop/knowledge_waterfall.go#L130)）在调用 `a.Update` 上屏前，会同步等待最多前 10 条知识通过 `titles.read` 请求官方 `/raw` 正文（受 4 并发槽位限制分批串行，而固定版 Cognee 单数据集接口每次均重走 12 个 SQLite 连接 / 14 条 SQL 的主体 ACL 查询，单波约 `90–100 ms`，直接顶满代码设定的 **`600 ms` `preCtx` 预读截止线**）。
- **零侵入优化路径与排序硬约束**：总览与原始记录均已具备控制面只读 SQLite + 本地 `raw_data_location` 4KB 预览投影，唯独项目知识流仍走官方串行分页 + 逐条 `/raw` 预读。应补齐同构的项目知识流只读投影接口（保留显式 schema 守卫与 `503` 官方 HTTP 回退）；**注意不能在底层 `data` 表直接做 `LIMIT 30` 分页**——因为知识流支持按外置 `provenance_store` 最早来源日期或 `knowledge_recall` 召回次数全局排序及分类/未读筛选（[knowledge_waterfall.go](../../internal/simpledesktop/knowledge_waterfall.go#L67)），必须在投影层低成本合并全量元数据完成排序与过滤后，再一并返回完整轻量目录与首屏 Top-30 正文预览。

[官方串行枚举](../../internal/simpledesktop/overview.go#L108) · [项目知识流与 600ms 预读](../../internal/simpledesktop/knowledge_waterfall.go#L77)

## 5. 实施与验证顺序

| 顺序 | 交付内容 | 决策与验收依据 |
| --- | --- | --- |
| 1 | **拆除客户端启动并发风暴与同步进程扫描**：`SetWindowActive` 遵守 `a.access.Next`；从 `/v1/projects` 拆出 `CodexConnection.view()`；将监控、侧栏队列汇总与 `RefreshIntegrations` 延后至首屏呈现稳定后；修复 `viewContent` 隐藏态 `a.windowInactive` 轮询漏点；`service_monitor` / `agent_connections` 同步 I/O 移出事件循环 | 零存储改动下复测直启与 LaunchServices 各 20–30 轮，验证 `925–1,163 ms` 双峰长尾是否消除并回落至 `<60 ms` TTFB |
| 2 | **裁剪 `OnReady → Show` 并校准真实可见帧打点**：移出 `win.Show()` 前的 `RefreshOpenAtLogin()`（省 `~85 ms`）；惰性挂载图谱 `WebView` 并在首屏后空闲预热（省 `~85–99 ms`）；`initGraph` 资产校验后台化；统一 `mach_absolute_time` 单调时钟并将 `(epoch, generation, visibleAlpha > 0)` 绑定到 drawable，确认首屏入场动效策略 | 单改与组合分别复测 P50 / P95 / Max；覆盖菜单栏恢复、开机自启隐藏、首次进图谱、切页取消与快捷键 |
| 3 | **项目知识流只读 SQLite + Top-30 本地预览投影**：复用 `overview_bootstrap` / `raw_records_read` 守卫模式，在服务端合并 `provenance` 与 `recall` 全量排序后附带首批预览返回，保留 `503` 官方回退 | 验证 `Memlane` 与 `item-fe` 切换耗时从 `0.71–1.85 s` 降至 `<100 ms`，核对日期/召回排序、分类/未读筛选与滚动锚点完全等价 |
| 4 | **首屏载荷精简与 `rememberKnowledgeDays` 落盘解耦**：按需拆分总览首屏轻量字段与分页游标（保留 4 项全量/跨接口真值及目录与投影 `503` 解耦），移除上屏路径同步 `Sync()` | 对照新记忆刚提炼完成、权限撤换、token 轮换、`503` 回退与跨日场景逐字段核验；仅当实测仍超预算时再评估服务端内存物化 |
| 5 | **真实签名公证包与驻留窗口恢复专项评估**：在 Developer ID 签名、公证及装订包上实测首次与复跑启动；在修复隐藏态轮询后实测驻留恢复的可交互延迟、常驻 RSS / 空闲 CPU 与撤权后重开安全性 | 区分冷进程启动与驻留窗口恢复两套产品指标；同一具体缺陷累计第 4 次验收未过按工程规范停止并汇报 |

固定参考机对各启动场景至少采样 20–30 轮，报告 P50、P95、最大值与失败率，覆盖浅/深双外观、60/120 Hz、`ReduceMotion` 开/关、较大日常库及后台采集/提炼并发场景；既有 headless 性能门禁继续守护组件帧耗时与堆内存上限。 [既有性能预算测试](../../internal/simpledesktop/perf_budget_test.go#L172) · [工程规范](../engineering/workflow.md)
