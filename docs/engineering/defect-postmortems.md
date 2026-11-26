# Memlane 真实缺陷复盘合集

按日期倒序收录已证根因并完成修复的真实缺陷复盘，保留完整复现条件、提交号、验证证据与未覆盖边界，供工程防复发与回归核对。新缺陷按[问题复盘模板](templates/incident-template.md)记录并按日期倒序追加至本文顶部；运行包误清理专项事故仍保留在 [运行包误清理复盘](runtime-package-cleanup-incident-20261111.md)。

---

## 2026-11-24 登录时静默启动仍弹出主窗口

### 复现

2026-11-24 在 macOS 26.6.2 arm64 UTM 客体上验证公开发布包 `v0.1.0-beta.1`。客体沿用已完成首装的测试库。打开「登录时静默启动」后，macOS 成功登记正式版登录项。重启前关闭 Memlane 主窗口并保持系统的「登录时重新打开窗口」关闭。

两次冷登录后，正式版均自动显示主窗口。首轮登录后约 1 分钟，Cognee、Agent 模型代理及接入网关项目目录均返回 HTTP 200。

### 已确认根因

MyGo v0.7.4 在 `applicationWillFinishLaunching` 内读取并缓存登录启动标记。UTM 中，同版本的独立诊断应用在该回调里读到的 `currentAppleEvent` 为空。到应用就绪时，系统已经送达 `kAEOpenApplication`，其中 `prdt` 的值为 `lgit`，但 `WasOpenedAtLogin()` 仍返回缓存的 `false`。探针记录到窗口由就绪回调显示，未收到重新打开窗口回调。[Apple 登录标记说明](https://developer.apple.com/documentation/coreservices/1556410-launch_apple_event_constants/keyaelaunchedasloginitem)

Memlane 的启动回调（原 `cmd/memlane-app/main.go`，现位于 [cmd/memlane/app.go](../../cmd/memlane/app.go)）原先仅依据该返回值决定是否显示主窗口，因此把本次登录启动当作普通打开。

### 修复与验收

- **修复实现**：新增 [login_launch_darwin.go](../../cmd/memlane/login_launch_darwin.go)，在 `OnReady` 阶段经 `NSAppleEventManager.sharedAppleEventManager.currentAppleEvent` 读取 `kAEOpenApplication` 的 `prdt`/`lgit` 登录项标记；在 [app.go](../../cmd/memlane/app.go) 结合 `mygo.App.WasOpenedAtLogin()` 判定登录静默启动，保持主窗口隐藏并同步启动隐藏态托盘按需/低频状态刷新与托盘弹层自适应高度（提交 `a85fce2`、`fe53407`）。
- **UTM 实机复验（通过）**：2026-11-24 在 macOS 26.6.2 arm64 UTM 客体安装 `v0.1.0-beta.2` 候选包（提交 `01202d2`），确认「登录时静默启动」开启且系统「登录时重新打开窗口」关闭后执行人工冷重启并重新登录。复验确认冷登录后主窗口保持隐藏，后台服务正常启动，菜单栏托盘与点击 Dock / 手动打开均可正常唤起主窗口。

---

## 2026-11-22 用户画像误计为项目待提炼

### 现象与根因

2026-11-22，日常库托盘把 `ml-user-profiles` 显示为「5 项进行中」。项目消息接口返回 5 项待提炼文档，类型均为 `user_profile`。Cognee 记录了 5 次成功保存，没有对应的提炼活动，5 份原文均可读取。

项目消息汇总将未完成 `cognify_pipeline` 的文档作为待提炼候选。画像的完成条件是保存原文并核对文档身份。SQLite 投影和官方 HTTP 回退都漏掉了这一文档类型，托盘把接口返回的文档逐项计为进行中。

### 修复与验证

修复在项目消息汇总共用画像文档的排除规则。两条接口回归测试在修复前均因误计画像而失败，修复后通过，同时仍返回普通待提炼文档。日常库只读集成验证覆盖两条路径，画像待提炼计数从 5 降为 0，5 份原文仍可读取。画像保存、读取权限和存量数据无需变更。

受控重启后，日常网关实测画像待提炼为 0，原文仍可读取 5/5，主项目返回 660 条知识。Native 主窗显示服务已连接，能打开 Codex 用户画像。macOS 菜单栏弹层未能通过辅助功能访问，托盘实窗待复核。

首次重启使用了系统默认 Python 3.9，在模型代理启动预检中缺少 `tomllib`。改用日常配置指定的 Python 3.12 后启动成功，模型与库配置未改动。首次启动失败属于环境问题，未进入本缺陷的验收步骤。

---

## 2026-11-22 Codex 代理提前结束可重试的断流

### 现象与证据

2026-11-22 的一次项目提炼中，模型代理累计记录 49 次 `codex_stream_disconnected`，Cognee 收到 HTTP 502。历史日志记录了请求和安全错误码，未采集 Codex 的 `willRetry` 标记。具体哪些历史调用被提前结束，以及断流的网络原因，仍无法确认。

Codex CLI 0.171.0 生成的 App Server 协议定义 `ErrorNotification.willRetry`。修复前，本地代理收到回合内的任意 `error` 通知就结束进程。仿真 App Server 先发出带 `willRetry=true` 的断流通知，随后在同一回合返回有效结果，现有 HTTP 入口仍返回 502，稳定复现了缺陷。

### 修复与回归

代理核对可重试通知的线程和回合身份。Codex 明确宣布将重试时，代理丢弃中断尝试留下的最终结果，继续等待同一次回合的结束事件。空闲超时、取消和进程回收使用原有路径。通知日志记录本地请求 ID、固定错误码及布尔值 `will_retry`。

回归从本地 HTTP 入口验证两种事件顺序，覆盖已收到回合应答和先收到 `turn/started`。旧版两种都返回 502，修复后均在同一次请求中返回一份通过校验的 200 结果。独立用例验证最终失败、不可重试通知、错配的回合 ID 和重试后空闲超时。测试回收仿真子进程，并检查诊断不包含供应方错误正文。Codex 代理、模型参数、探针、热切换、启动和提醒相关共 84 项回归通过，仓库静态门禁通过。

日常服务仍需加载新版本。此次重放使用仿真 App Server，生产自然断流的恢复效果待实测。
