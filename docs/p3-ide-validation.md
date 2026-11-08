# P3-A2 TRAE IDE 验证

2026-11-08。A2a 项目配置及外部回执等待已实现，真实 IDE 模型验收尚未通过。契约见 [IDE契约](p3-ide-contract.md)，剩余矩阵见 [交付计划](p3-delivery-plan.md)。配置写入、握手、复制prompt、无模型协议夹具分别不代表真实 IDE 日常接入通过。

## 版本、隔离与外部条件

本机 TRAE CN 3.4.14，产品commit `5d9926ed75dce7fa36dff87dbef5fb53e06b790a`，渠道cn/stable。项目 `.trae/mcp.json` 的实际加载器及stdio schema见 `build/p3-agents/trae-mcp-loader-snippets.json` / `trae-mcp-schema-snippets.json`。IDE launcher 的 `--version` 输出是VS Code基底1.107.1，须与产品commit和 `--help` 的实际TRAE版本共同核对；空用户目录会先输出 `No config file found`，仅移除这个精确前缀再校验。

`build/p3-ide/discovery.json`记录实际安装发现。独立 `--user-data-dir` / `--extensions-dir` 与两个合成workspace见 `ide-launch.json` / `ide-instance.json`。已打开实际首次引导，跳过安装全局CLI命令；`ide-login-block.json`及`build/p3-ide-screenshots/ide-login-loaded.png`表明该空profile要求登录后使用AI功能。未复制日常登录库、聊天、凭据或Agent配置；待用户在这个隔离实例登录后才能做实际IDE模型工具验收。TRAE SOLO CN0.1.19、国际版及其他commit仅发现或待适配，不标支持。

## 已实现的受信链路

App连接仅增补所选项目的Memlane JSONC entry，备份原文件，保留其他entry/注释/尾逗号；外来同名entry、重复key和已被外部修改的拥有项拒绝覆盖。范围变更更新代次，断开撤销连接并只移除拥有项，其他配置与记忆保留。

App准备独立挑战并保存到安装回执；重启与重复复制继续原挑战，取消后新挑战使用新nonce。私有typed IPC在同一SQLite读取快照中检查当前连接代次/工作区、精确raw正文/来源、save操作、indexed v1及实际status/get/search账本。复制或模型文本均不能置可用。安装修改/验证完成/取消在进程内串行；GUI刷新有Sequence栅栏，晚到快照不能覆盖取消或新配置。

合成验证的status/read/return事件独立归于verification，普通daily命中保留日常通道；混合检索在一个Store短事务校验和写入，搜索/网络在事务外。没有新增表或迁移，schema11/protocol1不变，status仅为验证回执证据，不变成日常成功读取。

## 检查与故障夹具

`config-integration.json`是无模型的实际IDE探测/拥有配置集成验证：备份、JSONC、范围代次重装、其他配置外部变化、撤销、同名/重复key/损坏格式和修改拥有项全部保留。

`usage-batch-fault.log`是实际临时SQLite的第二通道insert失败夹具：整个batch无operations/events/last_seen残留；解除故障重试与重放保留完整回执，日常/验证统计各一次，撤销后拒绝重放。不是模型运行。

`app-protocol-fixture.json`和 `scripts/verify-p3-ide-app.py`明确是**无模型的实际App/桥/真实Rust协议与故障夹具**。本轮九项实际App协议流程已通过，保存/状态/读取后仍不置可用，真实Rust检索回执齐全才完成；它只证明回执等待/取消/范围/重启/真实索引/断开，不能替代真实IDE自身模型的保存、读取、检索、采用及初始化。原始失败记录保留于 `first-app-failure/`（空profile探测前缀）、`second-app-start-failure/`（helper启动超时）与 `third-driver-reuse-failure/`（复用目录重复登记）。重试不能消除F1启动根因未解决的限制。

全仓test、vet和store/daemon/agents/desktop race见 `build/p3-ide/test.log` / `vet.log` / `race.log`。分组review保留混合统计原子性、GUI旧快照与驱动修复历史，见 `build/p3-ide/review/`。设计图已实际打开；App三张系统窗口截图（待验证、协议夹具回执齐全、断开）已逐张对照，实际IDE截图单独记录。

## 包与剩余验收

`build/p3-ide-app/`为完整隔离UI/helper/LaunchAgent/Agent桥与DMG的ad-hoc验证包；`build/p3-ide-production/`使用生产身份、无自动化/隔离service路径的完整包已构建，strict签名完整性见production-package-check.json（仍是ad-hoc）。主DMG 15,317,029字节、App普通文件 34,420,815字节，首次独立组件下载 285,752,286字节、App加实际合成准备根 526,438,322字节，更新下载量未知；不包括工作区/Agent profile/开发工具，普通文件字节不等于APFS分配。体积明细见sizes.json。不将ad-hoc签名当作Developer ID、公证或Gatekeeper通过。正式源、干净macOS15+arm64、F1启动/路径故障仍未完成，R piRPC、G代码图谱继续，总Goal未完成。阶段提交、远端SHA和临时handoff以delivery.json记录为准。
