# 登录时静默启动仍弹出主窗口

## 复现

2026-11-24 在 macOS 26.6.2 arm64 UTM 客体上验证公开发布包 `v0.1.0-beta.1`。客体沿用已完成首装的测试库。打开「登录时静默启动」后，macOS 成功登记正式版登录项。重启前关闭 Memlane 主窗口并保持系统的「登录时重新打开窗口」关闭。

两次冷登录后，正式版均自动显示主窗口。首轮登录后约 1 分钟，Cognee、Agent 模型代理及接入网关项目目录均返回 HTTP 200。

## 已确认根因

MyGo v0.7.4 在 `applicationWillFinishLaunching` 内读取并缓存登录启动标记。UTM 中，同版本的独立诊断应用在该回调里读到的 `currentAppleEvent` 为空。到应用就绪时，系统已经送达 `kAEOpenApplication`，其中 `prdt` 的值为 `lgit`，但 `WasOpenedAtLogin()` 仍返回缓存的 `false`。探针记录到窗口由就绪回调显示，未收到重新打开窗口回调。[Apple 登录标记说明](https://developer.apple.com/documentation/coreservices/1556410-launch_apple_event_constants/keyaelaunchedasloginitem)

[Memlane 的启动回调](../../cmd/memlane-app/main.go#L286)原先仅依据该返回值决定是否显示主窗口，因此把本次登录启动当作普通打开。

## 修复与验收

- **修复实现**：新增 [login_launch_darwin.go](../../cmd/memlane-app/login_launch_darwin.go)，在 `OnReady` 阶段经 `NSAppleEventManager.sharedAppleEventManager.currentAppleEvent` 读取 `kAEOpenApplication` 的 `prdt`/`lgit` 登录项标记；在 [main.go](../../cmd/memlane-app/main.go#L286-L294) 结合 `mygo.App.WasOpenedAtLogin()` 判定登录静默启动，保持主窗口隐藏并同步启动隐藏态托盘按需/低频状态刷新与托盘弹层自适应高度（提交 `a85fce2`、`fe53407`）。
- **UTM 实机复验（通过）**：2026-11-24 在 macOS 26.6.2 arm64 UTM 客体安装 `v0.1.0-beta.2` 候选包（提交 `01202d2`），确认「登录时静默启动」开启且系统「登录时重新打开窗口」关闭后执行人工冷重启并重新登录。复验确认冷登录后主窗口保持隐藏，后台服务正常启动，菜单栏托盘与点击 Dock / 手动打开均可正常唤起主窗口。
