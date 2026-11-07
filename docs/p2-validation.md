# P2 验证记录

目标：真实 Codex 保存项目经验 → App 查看与编辑 → 真实 pi 检索当前正文、来源和版本。P1 基线 `0a39b40`，只用隔离合成仓库、数据和 Agent 配置，不读取真实聊天或提交本机配置。

## 当前进度

2026-11-07 开始 P2。已读取 Goal 附件、AGENTS.md 和指定产品/设计/技术/实施/开发/P1/Agent 研究文档，实际打开首页与三栏详情设计图。开始时 `main` 工作区干净，当前 P1 审计产物 `build/p1/final-audit.json` 为 passed；该报告是 P1 证据，不充作 P2 验收。

只读探测：PATH 中 Codex `0.147.0`、pi `0.84.1`。Codex 提供 stdio MCP 与 exec，pi 提供 Extension/RPC，帮助未列出原生 MCP 命令；本机先用 Extension，不自动升级。pi 帮助在原沙箱中尝试建立 settings lock 被拒绝，没有修改真实设置；后续运行使用隔离 `PI_CODING_AGENT_DIR`。

P2.1/P2.2 统一契约、MCP/CLI 协议与项目/范围已通过。尚未完成真实 Agent、App Agent 页或实际 P2 窗口验收，不能描述为 Codex/pi 已接通。

官方 Go SDK 固定 `v1.8.0`。`proxy.golang.org` 在沙箱及沙箱外均连接超时；`goproxy.cn` 实际访问成功，命令级 `GOPROXY=https://goproxy.cn` 完成版本查询与下载，不修改用户默认 Go 环境。

## P2.1：统一契约、MCP/CLI 与持久化回执

`python3 scripts/verify-p2-contract.py` 七组协议检查通过，证据为 `build/p2/contract-verification.json`。使用新建系统临时目录、真实私有 Go 服务与固定 Rust worker（操作系统禁止 worker 联网），MCP 客户端通过 stdio 调用构建入口；该客户端是协议检查器，不是 Codex/pi Agent。

实际覆盖：P1 schema v1 → v2 保留正文、版本、操作、任务、绑定、活动及旧幂等回执；重复保存只有一条，同操作不同正文或范围冲突；保存来源片段与引用；编辑 v2、恢复 v3 后检索仅返回当前版本，原保存重放仍返回当前正文；服务停止时 MCP isError、CLI 非零且无假成功；显式恢复后连接与 v3 保留，代次变化使旧连接失效，断开撤销访问。

项目基础检查同时覆盖真实 Git worktree、子目录、符号链接、独立 clone；默认 A/B 隔离与允许全局，额外获批项目仍需显式 scope，桥进程 cwd 不用于识别项目。无 Git 工作区能列出获批范围并要求选择，显式选择全局可读；MCP 非法参数返回统一 `invalid_request`。

扩展边界检查先发现非 Git 工作区的 `requires_scope` 错误（只看是否传入 workspace，未看解析结果），已改为真实上下文结果。参数拒绝最初由 SDK 返回普通文本，与统一错误封装不符；改用 SDK 原始工具处理器和共用 JSON Schema 校验，MCP/CLI 均返回结构化错误。失败摘要保留在 `build/p2/contract-schema-failure.json`，通过报告独立标注协议类别，不当作真实 Agent 成果。

受影响 P1 服务回归六组通过，最新 schema v2 的独立报告 `build/p2/p1-regression/service-verification.json`，夹具改为真实 Git 仓库。完整 `sh scripts/go.sh test ./...` 和受影响 Go vet 通过。P1 旧构建审计属于历史基线，不以其散列证明 P2 当前包；P2 按新的包和报告核对。

完整构建包含 `Contents/Resources/bin/memlane`、`memlaned` 与 LaunchAgent，App/DMG 已完成本机 ad-hoc 签名校验；阶段包构建日志为 `build/p2/p21-build.log`。最终 UI 和体积仍在 P2.5 重新构建/测量，组件及模型未变化，复用 P0 下载证据。没有正式签名、公证或发布源。

## 真实 Agent 运行条件

Codex `login status` 报告 ChatGPT 已登录（未输出凭据）。pi 使用 `auth check --json --no-refresh`，`openai-codex`、`google`、`anthropic`、`openai` 均返回 `not_ready/credentials_not_configured`。已请求用户选择可用 provider/model 或在 pi 完成登录；不复制 Codex 凭据给 pi，不将协议通过代替 pi 真实进程验收。其余开发继续。

## P2.2：工作区、全局白名单与授权边界

P2.1 提交 `b7cb4d5`；P2.2 在此基础增加显式项目绑定优先、动态工作区错误和服务授权屏障。`build/p2/contract-verification.json` 扩展为十组通过；受影响包的 Go test/vet 退出 0。

新增真实检查：未登记仓库默认返回 scope_required，显式获批范围可读；未批准全局的 B 连接默认只搜索 B，显式请求全局仍拒绝；raw 原始记录保存与索引完成不增加热力图。Codex 明确绑定 A、桥进程在 B 启动时仍使用已确认 A 绑定，不反推进程 cwd；绑定＋工作区冲突拒绝。

在单服务 owner 停止时人为加入 P1 历史重复项目映射，再重启：未绑定动态工作区返回 scope_ambiguous 并要求选择；明确选择 A 的连接仍核对实际 worktree 的 common directory，可以正确读取 A，不合并或删除历史项目。配置变更/断开与工具调用使用读写屏障，变更完成后的旧代次调用被拒绝；先前十组中的重载/撤销检查仍通过。

使用合成配置将 worker Executable 指向不存在的路径，服务真实进入 failed：MCP 新保存返回持久化回执，index_state=pending；搜索返回 index_unavailable。恢复原 worker 配置并明确重启，持久任务补索引，重复保存仍为同一 v1，原文可检索。该实验只改变本轮临时根，未停止或改动其他服务。
