# 设置 UI 隔离候选交接

工作树 `/Users/bytedance/work/AI/memlane-settings-20261112`，分支 `settings/native-redesign-20261112`。完整输入快照 `7bf679da46b4b80e32e25b6638e197c37beaeac1`（1424文件及逐文件散列）；该快照包含另一session当时未提交源码，仅是输入，**不要把整个快照集成回原目录**。

本轮只重构设置八页及专用组件，复用既有配置、IPC与任务所有者。三种处理保存／验证／明确启用分离；Provider的地址／Key修改会禁用旧验证启用，草稿切换与取消保留边界已检查。官方采集迁移、官方知识直读及其刷新没有改写。

## 可审阅内容

- [连续用例、失败计数、真实能力与缺口、串行集成步骤](settings-redesign-20261112.md)
- [双尺寸实际窗口与固定稿对应索引](../images/ui-redesign/settings-native-20261112/README.md)
- [机器结果及原始日志](../../../../engineering/settings-evidence-20261112/results.json)
- 补丁目录：`build/settings-handoff-20261112/`，`manifest.json`记录SHA256。图片在分支提交内，补丁04只含文档／回执，不内嵌PNG。

| 补丁 | 内容与依赖 |
| --- | --- |
| 01-settings-pages.patch | 设置专用布局／表单／状态展示、共享初始化正文及对应测试；需要02的App字段 |
| 02-shared-entry.patch | app.go仅1个视图状态字段＋设置toolbar委托；main.go与visual_fixture仅e2e隔离开关，普通构建恒false；不修改design.go |
| 03-isolation-disk-guard.patch | 本树开发脚本不扫描系统临时根，独立审阅，不自动覆盖原磁盘脚本 |
| 04-docs-and-results.patch | 本轮中文账本、测试与运行证据、截图索引 |

在交接前重新读取了原环境session最新状态：正在推进官方知识直读。对原目录分别执行01、02的`git apply --check`均退出0；仅检查，**未应用**。检查回执在补丁目录，后续原目录再变化时须重查。

## 验证结果及边界

最终desktop30.989s通过，最终全仓vet通过。最后一次全仓test中desktop及其他业务包通过，但Agent hook测试仍报`capture_bridge_unavailable`，整体退出1；本轮没有修改internal/agents，不声称全仓绿。原运行daemon76077及官方服务76867最后观察仍在。

最终包026/027两种尺寸共18张实际NSWindow PNG及JSON回执，源码与签名后二进制散列已核对。Agent卡片布局累计3轮偏差后第4轮通过；旧失败图仍保留。英文键入、中文粘贴、Tab焦点、Provider草稿跨方式保留已在实际窗口断言；系统中文IME组合及完整初始化Native矩阵未覆盖。

官方插件安装迁移、逐客户端能力IPC、官方独立库完整备份及索引汇总、真实Provider/Codex/Magpie推理、本机helper生命周期仍按原所有者分别验收。设置页显示未知／待验证／实际备份范围，不借UI填造成功。

主窗口原有全局⌘K尚未实现；本轮设置搜索按钮接既有点击入口，快捷键列为共享集成需求。已保存Provider自定义地址读取的回归为023红→024完整通过。

## 集成后必须重跑

保留原工作区修改，逐hunk应用01＋02，不cherry-pick源快照。重点冲突文件为`internal/desktop/app.go`、`cmd/memlane-app/main.go`、`visual_fixture.go`，保证会话／知识／官方刷新逻辑保留。随后由原环境拥有者串行执行`sh scripts/go.sh test ./...`、`sh scripts/go.sh vet ./...`、普通生产构建无e2e探针、设置双尺寸／三步初始化／真实后台配置与任务回执验证。读取失败、取消、保存不启用、活动任务配置冻结及共享撤除影响继续沿原规则检查。

本session未执行原目录集成、未启动真实模型、未修改LaunchAgent／用户Agent配置／当前处理方式，也未停止或替换原App与服务。保留单个最新GUI位于`build/settings-native/Memlane Settings Review.app`，约31.8 MiB，仅合成调试包，非完整客户端分发包。
