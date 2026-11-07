# P2 验证记录

目标：真实 Codex 保存项目经验 → App 查看与编辑 → 真实 pi 检索当前正文、来源和版本。P1 基线 `0a39b40`，只用隔离合成仓库、数据和 Agent 配置，不读取真实聊天或提交本机配置。

## 当前进度

2026-11-07 开始 P2。已读取 Goal 附件、AGENTS.md 和指定产品/设计/技术/实施/开发/P1/Agent 研究文档，实际打开首页与三栏详情设计图。开始时 `main` 工作区干净，当前 P1 审计产物 `build/p1/final-audit.json` 为 passed；该报告是 P1 证据，不充作 P2 验收。

只读探测：PATH 中 Codex `0.147.0`、pi `0.84.1`。Codex 提供 stdio MCP 与 exec，pi 提供 Extension/RPC，帮助未列出原生 MCP 命令；本机先用 Extension，不自动升级。pi 帮助在原沙箱中尝试建立 settings lock 被拒绝，没有修改真实设置；后续运行使用隔离 `PI_CODING_AGENT_DIR`。

P2.1/P2.2 统一契约与范围协议已通过；P2.3 的真实 Codex → App 查看/编辑 → pi 当前版本读取已通过；P2.4 App 内 Codex/pi 读写、连接管理与实际系统窗口对照已通过。P2.5 恢复、停止/重启和最终交付审计继续执行。

官方 Go SDK 固定 `v1.8.0`。`proxy.golang.org` 在沙箱及沙箱外均连接超时；`goproxy.cn` 实际访问成功，命令级 `GOPROXY=https://goproxy.cn` 完成版本查询与下载，不修改用户默认 Go 环境。

## P2.1：统一契约、MCP/CLI 与持久化回执

`python3 scripts/verify-p2-contract.py` 七组协议检查通过，证据为 `build/p2/contract-verification.json`。使用新建系统临时目录、真实私有 Go 服务与固定 Rust worker（操作系统禁止 worker 联网），MCP 客户端通过 stdio 调用构建入口；该客户端是协议检查器，不是 Codex/pi Agent。

实际覆盖：P1 schema v1 → v2 保留正文、版本、操作、任务、绑定、活动及旧幂等回执；重复保存只有一条，同操作不同正文或范围冲突；保存来源片段与引用；编辑 v2、恢复 v3 后检索仅返回当前版本，原保存重放仍返回当前正文；服务停止时 MCP isError、CLI 非零且无假成功；显式恢复后连接与 v3 保留，代次变化使旧连接失效，断开撤销访问。

项目基础检查同时覆盖真实 Git worktree、子目录、符号链接、独立 clone；默认 A/B 隔离与允许全局，额外获批项目仍需显式 scope，桥进程 cwd 不用于识别项目。无 Git 工作区能列出获批范围并要求选择，显式选择全局可读；MCP 非法参数返回统一 `invalid_request`。

扩展边界检查先发现非 Git 工作区的 `requires_scope` 错误（只看是否传入 workspace，未看解析结果），已改为真实上下文结果。参数拒绝最初由 SDK 返回普通文本，与统一错误封装不符；改用 SDK 原始工具处理器和共用 JSON Schema 校验，MCP/CLI 均返回结构化错误。失败摘要保留在 `build/p2/contract-schema-failure.json`，通过报告独立标注协议类别，不当作真实 Agent 成果。

受影响 P1 服务回归六组通过，最新 schema v2 的独立报告 `build/p2/p1-regression/service-verification.json`，夹具改为真实 Git 仓库。完整 `sh scripts/go.sh test ./...` 和受影响 Go vet 通过。P1 旧构建审计属于历史基线，不以其散列证明 P2 当前包；P2 按新的包和报告核对。

完整构建包含 `Contents/Resources/bin/memlane`、`memlaned` 与 LaunchAgent，App/DMG 已完成本机 ad-hoc 签名校验；阶段包构建日志为 `build/p2/p21-build.log`。最终 UI 和体积仍在 P2.5 重新构建/测量，组件及模型未变化，复用 P0 下载证据。没有正式签名、公证或发布源。

## 真实 Agent 运行条件

Codex `login status` 报告 ChatGPT 已登录（未输出凭据）。pi 对四个常见 provider 的 `auth check --json --no-refresh` 返回未配置，但真实 pi 默认运行路径随后成功调用工具，故不能把这四项观察推论为 pi 整体不可用；登录询问已解除。认证始终由 Agent 处理，隔离目录只链接原 Agent 的 auth 文件，不提取或转交凭据，也不加载原聊天/项目规则/其他工具。

## P2.2：工作区、全局白名单与授权边界

P2.1 提交 `b7cb4d5`；P2.2 在此基础增加显式项目绑定优先、动态工作区错误和服务授权屏障。`build/p2/contract-verification.json` 扩展为十组通过；受影响包的 Go test/vet 退出 0。

新增真实检查：未登记仓库默认返回 scope_required，显式获批范围可读；未批准全局的 B 连接默认只搜索 B，显式请求全局仍拒绝；raw 原始记录保存与索引完成不增加热力图。Codex 明确绑定 A、桥进程在 B 启动时仍使用已确认 A 绑定，不反推进程 cwd；绑定＋工作区冲突拒绝。

在单服务 owner 停止时人为加入 P1 历史重复项目映射，再重启：未绑定动态工作区返回 scope_ambiguous 并要求选择；明确选择 A 的连接仍核对实际 worktree 的 common directory，可以正确读取 A，不合并或删除历史项目。配置变更/断开与工具调用使用读写屏障，变更完成后的旧代次调用被拒绝；先前十组中的重载/撤销检查仍通过。

使用合成配置将 worker Executable 指向不存在的路径，服务真实进入 failed：MCP 新保存返回持久化回执，index_state=pending；搜索返回 index_unavailable。恢复原 worker 配置并明确重启，持久任务补索引，重复保存仍为同一 v1，原文可检索。该实验只改变本轮临时根，未停止或改动其他服务。

## P2.3：真实 Codex、App 与 pi

真实 Codex `0.147.0` 的 `mcp_tool_call memory_save` 已 completed，返回 `persisted=true`、saved_revision=1 和实际正文/来源引用；记录 ID `3abaeb03-02c9-55b0-ac9d-db098d5460a7`。证据 `build/p2/codex-real-save.json`；回执的 index_state=pending 独立于持久化成功，随后真实 worker 完成索引。

`python3 scripts/verify-p2-app-edit.py` 通过实际 App 查看 Codex 正文、来源片段与 `synthetic://p2/project-a/requestId`，在右栏编辑保存为 v2，并核对原始正文与引用保留；证据 `build/p2/cross-agent-app-edit.json`。这里是实际 App 操作，不是脚本直接编辑数据库。

`python3 scripts/verify-p2-real-agents.py --agent pi --mode read` 的真实 pi `0.84.1` 通过薄 Extension 执行 memory_search 与 memory_get，回执正文/版本为 v2，保留 codex 来源与原引用；证据 `build/p2/pi-real-read.json`。`build/p2/pi-real-host.json` 另外证明真实 pi 的 scope 工具往返。普通 MCP/CLI 契约报告不代替上述真实 Agent 证据。

执行失败与修复均保留：Codex 最初返回真实 usage limit，提示 21:13 后重试；额度恢复后主机取消 memory_save，原始 MCP 事件为 `user cancelled MCP tool call`，没有持久化，不能以 exit=0 判成功。核对实际版本与 [OpenAI 配置 schema](https://raw.githubusercontent.com/openai/codex/main/codex-rs/core/config.schema.json) 后，仅为 Memlane 的新增保存工具设置 `tools.memory_save.approval_mode="approve"`；原真实流程随后通过。主机 shell 仍使用只读沙箱。安装器持久写入同一工具设置，MCP 正确标记只读/仅新增/幂等/本地工具提示；权限仍由服务白名单校验。

验收运行关闭用户配置、项目规则和非必要工具，使用隔离配置/会话与合成 Git 项目；没有覆盖真实 Agent 的配置或导入聊天。主包内提供 Go 桥，pi Extension 使用其已安装的运行环境，不额外安装 Node/Python 或重型引擎。

## P2.4：App 功能与实际系统窗口已通过

`build/p2/adapter-verification.json` 六组通过：限定位置发现、Codex 精确增量 TOML/备份/断开、配置冲突拒绝、pi Extension 增量/断开、外部修改保留、符号链接与失败回滚。用户选定可执行路径由客户端保存，首次打开重新探测；连接配置记录实际版本。不会扫描全盘或在发现时改 Agent 设置。

Agent 页沿用导航—列表—详情三栏，连接、范围、验证、断开均在栏内；所选项目/worktree 由服务校验，再安装对应配置。初始化 prompt 只保留禁用入口位置。`build/p2/agent-ui-verification.json` 八组 `functional_passed`，包含 App 实际调用 pi 保存原始验证记录、memory_status 和 memory_search，取得回执后才显示可用；raw 不计新增活动。`build/p2/agent-verification-failure.json` 保留 App 调用 Codex 额度失败时显示异常的实际证据。

一次原地重建后的明确停止/恢复曾失败：该隔离 launchd job 无 helper/socket、EX_CONFIG 78、runs=16。通过 App「停止服务并退出」注销旧 job，保持同一数据目录，改用新验证标识/路径 r2；真实 App 恢复后 job running、runs=1、never exited，pi 读写通过。该结果支持验证 App 注册缓存相关解释，不将直接 helper 启动当作 App 验收，也不改动其他已运行服务。当前测试 App 路径由临时 `source-info.json` 记录，脚本按该权威路径启动。

系统截图最初发现 graphical_session=true、on_console=true、screen_locked=true，未把渲染检查算作视觉通过。随后桌面解锁，执行 `python3 scripts/verify-p2-agent-ui.py --verify --verify-pi --require-success`，九组 passed、visual_complete=true；实际 App 内 Codex 和 pi 都通过保存、状态、检索回执，待重载转为可用。系统采集前检查桌面解锁、实际激活并点击测试窗口，NSWindow key/visible 均为 true。

已实际打开两张设计图，并查看系统帧逐项对照：窗口内容 1200×820，首页宽幅沿用 P1；Agent 为导航—列表—右栏详情，浅蓝侧栏从红绿灯顶部连续到底，正文与工具栏白色，字号/间距沿用 P1 token，按钮与图标单色。连接、范围、验证、断开在右栏，长配置路径在输入框或文本行显示并可滚动；初始化保留禁用入口。截图的重复待连接行来自多轮合成验收留下的断开连接历史，不代表真实 Agent 数量。没有普通操作弹框。

系统 Codex/pi 可用帧归档于 [Codex](images/p2-codex.png) 和 [pi](images/p2-pi.png)，全部中间状态与系统采集元信息在 `build/p2-screenshots/system-agent-*.png` 及同名 `*-capture.json`。先前仅功能结果保留为 `build/p2/agent-ui-functional-verification.json`，失败报告仍保留。服务读取失败时 UI 保留上次正文，但将服务状态标为不可用，不继续显示可检索/Agent 可用。

## 真实范围与 worktree

`build/p2/real-scope-verification.json` 七组通过，均启动实际 pi/Codex 进程并核对工具事件。pi 实际默认模型为 `deepseek/deepseek-v4-pro`，由 Agent 自己处理认证。覆盖显式全局保存、B 默认排除 A（即使连接已允许 A）、额外项目显式授权、未授权 A 的 scope_denied、B 默认允许全局、真实 pi worktree ctx.cwd 映射 A，以及真实 Codex MCP 在 worktree 读取 A 的当前 v2。

Codex worktree 首次运行正常退出但没有工具回执，不能以 exit=0 宣称通过；同一精确工具请求重试取得真实 MCP memory_get 回执后才计通过。验收器保留终态与工具事件，未以直接 IPC 或模型最终文本替代工具调用。CLI/MCP 原契约十组和配置适配六组仍通过，证据分别为 `build/p2/contract-verification.json`、`build/p2/adapter-verification.json`。
