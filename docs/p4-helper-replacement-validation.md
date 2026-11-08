# F1 helper 替换诊断与未通过边界

2026-11-08，**未通过**。G2 首次安装、代码功能和独立组件升级已通过，不证明主 App/helper 同身份替换恢复。全部复现绑定 `build/p4-code/g2-app-fixture-path.txt` 标记的隔离 App 与合成数据，不操作日常 App、Agent 配置、全局 BTM 数据或系统防护。

## 已确认现象

最新 helper 同路径替换后，实际 App 启动成功，打包 LaunchAgent 的 helper 未提供 ready IPC，launchd 退出 EX_CONFIG 78。`build/p4-delivery/helper-fingerprint-system.log` 的实际系统原因是 `OS_REASON_CODESIGNING | Launch Constraint Violation`，`c[5]p[1]m[1]e[0]`、`Constraint not matched`、launch type 0、failure proc `[vc:10]`。严格深度 codesign 通过；直接执行该 helper 成功，见 `helper-direct-stopped-probe.json`。因此签名完整性或普通执行成功不足以证明 SMAppService 启动约束匹配。

`scripts/verify-p4-helper-replacement.py` 提供无模型快速复现；真实 Go helper 使用未启用的随机 build tag 改变标准构建元数据，然后按 production producer 签名重新封装。`helper-replacement-canonical-go-build.json`、`helper-replacement-producer-signature.json` 仍失败，排除仅由早期强制 build ID 或显式重签夹具造成的解释。该脚本不是正式 updater 验收。

## 已执行的单项假设

| 实际探针 | 结果 / 证据（build/p4-delivery/） |
| --- | --- |
| 仅 App 版本变化、helper 不变 | 通过 `helper-replacement-after-version-replace.json`；不证明 helper 更新 |
| 改 helper，再次打开同包 | 两轮失败 `helper-replacement-after-helper-change*.json` |
| 仅刷新本隔离 App 的 LaunchServices 注册 | 失败 `helper-replacement-refresh-ls.json` |
| 从已变化 App 运行原生 unregister 再 register | 失败 `helper-replacement-new-app-unregister.json` |
| 将 helper SHA 写入隔离 plist 环境并重新封装 | 失败 `helper-replacement-fingerprint.json` |
| helper 移至 Contents/MacOS，形成嵌套代码封装 | 失败 `helper-replacement-macos-location.json` |
| 同时改变 App 版本与 helper | 失败 `helper-replacement-version-and-helper.json` |
| producer Go linker 签名与显式 ad-hoc 签名 | 两类都失败，见 `helper-signature-comparison.json` 与 producer 报告 |

BTM tuple UUID 是注册项 ID，不能当 Mach-O UUID。曾有该方向推测，实际日志已纠正。未重置全局 BTM、升级系统或削弱签名防护。Apple 论坛正文获取遇到安全验证；未读完整帖子，不以搜索摘要作为已证实修复。

## 后续验收

继续检查旧身份注销、实际 updater 时序、launch constraint 与正式签名类别之间的关系；每次只改变一项，并保持真实 helper、旧身份和失败报告。任何修复须经过实际同路径/移动更新、停服保持、数据与进程拥有权检查。不能换新 App 身份后移除本失败。

Developer ID、公证、正式源、Gatekeeper 与干净 macOS 15+ arm64 尚无必需条件及证据。当前 ad-hoc 本机结果既不能宣称正式签名失败，也不能宣称正式分发通过。具体外部条件见 [交付矩阵](p3-delivery-plan.md)。
