# F1e 后台许可撤销真实验收契约

2026-11-08，验收准备中，未通过。基线为 [F1d验证](../../../p4-diskfull-validation.md) 的完整0.0.9 App、schema14与Rust0.0.3；本切片使用普通宿主目录，不挂载磁盘故障卷。

## 输入与权限边界

只使用新 `memlane-p3-code-app-*` 合成根及独立App名称 `MemlaneBackground`、全新bundle/service身份。组件、项目与知识均为合成材料；使用实际App、helper/LaunchAgent、Rust/E5，不调用云模型或改写日常Agent配置。

系统设置定位到“登录项与扩展”中的“App后台活动”。先只读核对实际AX角色、唯一 `background-switch-MemlaneBackground`、当前值与enabled状态；确认该App的service plist、bundle身份、helper/worker PID和运行根一致后才操作。其他开关值作只读前后对照，不能修改相似名称、分组开发者、MDM或未知项目。若不能唯一定位，保留未通过且继续其他独立工作，不猜坐标点击。

## 操作与完成标准

1. 真实App自动准备与启动，手动登记项目、保存合成v1，当前Rust检索与来源/历史正常；实际bundle的只读 `--p0-action status` 核对Enabled。
2. 用实际系统设置的该项AXPress撤销许可，核对开关变为关闭、SMAppService实际状态、原helper与worker是否由系统退出，以及App设置/保存/读取行为。不能用主动Unregister、kill、假IPC或仅改JSON状态替代许可撤销。
3. 撤销期间不显示成功读取或保存回执，不丢原文、来源、版本和草稿，不因后台失联误显示ready。需要系统批准时应提供准确可恢复状态；网络/组件无需重复下载。
4. 用同一真实系统开关恢复许可，在App中显式准备/重试，核对实际状态、当前检索与手动重试；不虚构历史版本。主App主动停止后的持久停止规则仍生效，退出/重启不能自动恢复计划。
5. 成功或失败均恢复该开关的原值，停止自己启动的服务与App并核对PID/IPC。恢复许可或清理失败使验收failed。原失败和后续继续分别保存，不覆盖旧报告。

## 证据与限制

记录checkbox身份/原值/决定/结果、实际bundle状态、绑定PID、真实App系统窗口、当前版本与Rust回执、恢复及清理。只保存合成材料与技术证据，不读取聊天或认证。SystemSettings只读预检不是撤销通过，NativeStatus不是App UI通过，ad-hoc本机证据不是Developer ID/公证/干净系统分发。

其他worker/服务崩溃、App移动/同身份helper更新、R/pi、A1b/Codex、A2b/TRAE和F2/F3继续独立验收。本切片不重新实现已通过的整理attempt/租约链路；受撤销影响的在途任务栅栏若发现问题，应按真实影响补验并保留未通过边界。
