# Agent 优先首任务：本机验证

2026-11-09。按用户要求先走通本机主流程，分发、签名、升级及其他独立验收暂缓。

**本轮限定主流程通过：空根准备 → 选择 Codex → 跳过项目并连接 → 确认工作目录 → 准备官方环境/插件 → 正常真实任务 → 首份有效材料后登记项目 → 查看知识与完整出处 → App 编辑 v2 → 新 Agent 会话读取当前 v2。** 四 Agent 的完整支持矩阵、独立官方 graph 的维护后召回、正式分发和长驻故障不包含在这一结论中。

## 固定对象

- 验收时工作区基线：`99246f43e1ae7ab7c1adf3ac9e5a859dd80cfcb1` 加原有未提交修改和本轮实现。2026-11-09 基线整理将相关源码与复现脚本纳入 Git；本记录的固定包与原始散列继续代表当时验收对象，不作为整理后源码已重新执行全链路的证明。
- 完整 App：`build/agent-first-task-validation/2026-11-09-001/app/darwin-arm64/MemlaneFirstTask.app`，0.0.1，独立身份 `io.github.zwhu.memlane.firsttask.c1d862c9d1ce`。包含真实 UI、helper、CLI、LaunchAgent；有验收 automation，没有原型数据。
- 固定清单：[package-source-integrity.json](../build/agent-first-task-validation/2026-11-09-001/package-source-integrity.json)：9 个包内普通文件与 167 个运行 Go 源文件散列。本轮前后均一致；测试文件和后续文档修订另行记录，不冒充运行包变化。
- 根：[fixture-path.txt](../build/agent-first-task-validation/2026-11-09-001/fixture-path.txt)。起点无业务库、组件缓存、项目、连接或记忆；只复用以前合成验收的独立模型授权条件，不复制旧业务、聊天、Agent home 或运行缓存。
- 环境：本机 macOS arm64。Codex `0.167.0-alpha.3.2`，沿用其配置中的 `gpt-6.1-sol`；官方 Cognee 服务 `1.6.7`，插件 `1.7.7`，固定 BGE 模型；Rust `0.0.3` 与 E5 int8 完成原文索引。

## 逐段实证

| 阶段 | 实际结果 | 原始证据 |
| --- | --- | --- |
| 本机准备 | 实际 helper/Rust ready，业务范围只有两个空全局 scope | [01 就绪](../build/agent-first-task-validation/2026-11-09-001/runtime/screenshots/01-ready-agent-first.png) |
| 选择 Agent | ready 主按钮为“选择 Agent”，进入真实发现列表；未进入添加项目表单 | [02 可跳过项目](../build/agent-first-task-validation/2026-11-09-001/runtime/screenshots/02-agent-project-optional.png) |
| 无项目连接 | 连接已持久化，`auto_project=true`，无 project/预留 ID；仍只有两个全局范围 | 新运行报告 `connect-without-project-or-workspace` |
| 确认工作目录 | 核对本轮 `repo-a` 实际 Git 身份，预留未来项目 ID，但 `scopes` 仍无项目 | [03 目录确认](../build/agent-first-task-validation/2026-11-09-001/runtime/screenshots/03-workspace-approved-no-empty-project.png) |
| 官方准备与插件 | 真实组件下载、独立模型/嵌入探测、官方插件安装完成；业务仍无项目/原始记录 | [04 插件准备](../build/agent-first-task-validation/2026-11-09-001/runtime/screenshots/04-plugin-ready-no-project.png) |
| 正常任务与接收 | 实际 Codex 实现并测试 `normalize_ticket`；官方采集/提炼后 App 自动接收 | [05 任务与材料](../build/agent-first-task-validation/2026-11-09-001/runtime/screenshots/05-task-material-and-project.png) |
| 有数据后建立项目 | 首任务产生 1 条知识、5 份 SDK 分段和 1 份完整会话原文，共 6 份 raw；此后才出现唯一 `repo-a` 项目 | `first-effective-material-created-project` 中实际 scopes/knowledge/raw 回执 |
| 完整出处 | 从同条知识打开完整会话阅读器，保留本轮来源、版本、会话和 SDK 分段 | [06 完整原文](../build/agent-first-task-validation/2026-11-09-001/runtime/screenshots/06-full-original-reader.png)；已实际看图 |
| 编辑后当前版本 | App 保存用户修订的发布约定为 v2，等待索引完成 | [07 当前版本](../build/agent-first-task-validation/2026-11-09-001/runtime/screenshots/07-user-current-version.png) |
| 新会话读取 | 新 session、无 resume；问题不包含答案；3 次实际记忆工具调用返回同一项目/记忆的 v2 正文，真实回答采用新口令并标明旧口令失效 | [校正后的报告](../build/agent-first-task-validation/2026-11-09-001/runtime/report-corrected.json)，`current_version_evidence`；[真实回答画面](../build/agent-first-task-validation/2026-11-09-001/runtime/screenshots/failure.png) |

项目 ID：`c12fecf7-e906-5900-a228-f200bf1d2500`。当前知识 ID：`23b241ba-721c-504f-be64-3fafa9c12525`。首任务 session：`codex_01a1bc68-ddc8-7380-8826-3cb3b4c02850`；新读取 session：`codex_01a1bc6b-30a2-7920-85ff-cae1fe4b490e`。两次任务与工具结果均来自本轮实际运行，没有 SQL 注入记忆或伪造 Agent 回执。

用户在 App 中修订后，后续任务通过 Memlane 当前知识工具读取版本；官方插件继续采集本轮任务，历史图谱保留原材料。没有自动覆盖、删除或回写官方知识。这个读取通道的实际证明不能换称为“独立官方 graph 已与 App 双向同步”。

## 驱动误报及复核

首次驱动在末段只识别 `function_call`，实际 Codex 版本以 `custom_tool_call` 记录三次调用，因此误报“没有调用当前知识工具”，驱动退出 1。原 [report.json](../build/agent-first-task-validation/2026-11-09-001/runtime/report.json)、[runtime.log](../build/agent-first-task-validation/2026-11-09-001/runtime.log) 和 `failure.png` 保留；截图展示的产品回答本身已经包含正确 v2。

解析器现同时处理两种事件。以 `--capture-existing` 只读复核已完成的两个真实 session，逐项核对 App v2 状态、实际工具调用/返回、项目/记忆 ID、revision=2、问题未含答案和实际回答，退出 0，生成独立 `report-corrected.json`。没有重新执行任务、替换材料或覆盖失败记录；原驱动退出 1 不写成整段脚本退出 0。

## 代码与事务检查

全仓 `sh scripts/go.sh test ./...`、`sh scripts/go.sh vet ./...`，以及 `CGO_ENABLED=1 sh scripts/go.sh test -race ./internal/desktop ./internal/store ./internal/daemon ./internal/official` 均通过。日志为 [test](../build/agent-first-task-validation/2026-11-09-001/go-test.log)、[vet](../build/agent-first-task-validation/2026-11-09-001/go-vet.log)、[race](../build/agent-first-task-validation/2026-11-09-001/go-race.log)。完整包构建及严格签名结构检查通过，见 [build.log](../build/agent-first-task-validation/2026-11-09-001/build.log)。

新增真实 SQLite/Git 测试核对：连接/确认目录不产生空项目；首记录故障整体回滚，binding 不提前提升；成功后项目、范围与记录共同提交；重放不重复；同仓待接入/手动登记复用；独立 clone 不误合；不同工作区和撤销后迟到材料拒绝。schema2–15 旧连接表保持原列结构，新增待接入表按 schema16 迁移；既有迁移、备份及恢复回归通过。

官方当前知识配置测试确认保留原模型/插件、同 binding 重放可用、变更 bridge 身份拒绝静默覆盖。这些合同测试不代替上表中的真实 Agent/模型/窗口证据。

## 本机结果与体验入口

最终只读 SQLite `integrity_check=ok`、schema16；3 个 scope（1 项目＋2 全局）、1 个连接、13 条材料/知识、14 个版本，待建立 binding 为 0、正常 binding 为 1。数据包括第二次查询会话的官方实际采集，不能全部称为首任务新增。完整 App、运行源、服务停止状态与自有进程清理见 [final-integrity.json](../build/agent-first-task-validation/2026-11-09-001/final-integrity.json)。

| 本轮口径 | 字节 | 约值 |
| --- | ---: | ---: |
| 完整验证 App 普通文件 | 40,633,213 | 40.63 MB |
| 首次实际准备归档 | 711,839,971 | 711.84 MB |
| App＋已准备合成 client 根逻辑大小 | 1,596,041,316 | 1.60 GB |

下载量来自 [source-access.json](../build/agent-first-task-validation/2026-11-09-001/source-access.json)，5 个归档各实际请求一次，含 Rust/E5/官方 Cognee/BGE/plugin。尺寸明细见 [package-size.json](../build/agent-first-task-validation/2026-11-09-001/package-size.json)。归档为本机固定构件经 loopback 真下载；不含 HTTP/TLS、模型调用网络量、开发工具链或源归档。此为带 automation 的本机验证 App；本轮没有生成 DMG 或验证正式分发。

可打开已保留的新 App：

```sh
open "/Users/bytedance/work/AI/memlane/build/agent-first-task-validation/2026-11-09-001/app/darwin-arm64/MemlaneFirstTask.app"
```

打开时服务已停止。先在设置明确“启动服务”，再进入 `repo-a` 看当前知识和出处。若继续真实任务，从 Agent 的该连接进入首任务页，复核已准备的官方环境后使用；已经准备的组件可从本轮缓存复用。未安装或启用用户日常采集 hook。

原整体验收失败记录保留；本轮只关闭上述 Codex 主流程缺口。请求预算、断线停服等长驻故障和发布门槛暂缓，不据此宣布整体验收或正式分发通过。
