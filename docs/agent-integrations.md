# Agent 接入：本期流程与复用依据

2026-11-08。当前先梳理产品，并完善本地 Agent 发现与连接状态；安装、升级、许可撤销和其余真实接入验收继续保留在原交付矩阵，当前不恢复这些收尾任务。

本期以现有 Codex、pi、TRAE IDE、TRAE CLI（traex）四种为范围。其他 Agent 可沿同一方式扩展，参考页的全部 Agent 尚未纳入本期支持承诺。规则以 [产品定义](product.md) 为准，用语见 [领域词汇](../CONTEXT.md)。

## 用户流程

2026-11-09 用户纠正：首次连接先将 Memlane 配置到当前电脑的编码 Agent，不要求先关联项目。电脑级接入和项目/任务范围分开；[新产品规则](ui-redesign/agent-connection-contract.md) 约束后续设计与实现。下文项目级配置及其实际验证继续作为当前已实现能力记录，不代表新电脑级流程已经完成。

进入 Agent 页 → 自动发现已知安装 → 查看版本与接入能力 → 选择项目和允许范围 → 明确连接 → 按 Agent 要求重载 → 验证真实读写 → 显示可用。

Agent 页保留已选定的三栏：左栏导航，中栏安装与连接列表，右栏展示安装信息、范围和下一步操作。连接时明确接入哪个项目、允许哪些知识范围。初始化记忆是连接后的独立可选步骤。

| 信息 | 含义与表现 |
| --- | --- |
| 未找到安装 | 提供路径输入和重新发现；不能推断该 Agent 一定未安装 |
| 已发现 | 已读到安装路径和产品版本；尚未授予记忆访问权限 |
| 接入尚未验证 | 安装信息仍可见，说明具体版本或渠道尚未适配；连接按钮不可执行 |
| 配置已安装／待重载 | 已有配置回执，提示用户重载；不能显示为可用 |
| 待验证 | 等待当前连接的真实读写回执；复制 prompt 不代表验证成功 |
| 可用 | 当前连接代次已验证，且记忆服务和索引当前可用 |
| 待适配 | Agent 更新为尚未验证版本，保留配置与断开入口，暂停连接修改和验证 |
| 异常 | 安装信息读取、配置、验证或记忆服务实际失败，展示具体原因 |

安装与记忆连接分开呈现。连接详情取当前安装探测版本，不能用旧安装回执盖掉新版本。连接路径不同于当前探测路径时先提示检查，不能让另一份安装继承当前版本的接入能力。

## 已有连接方式

| Agent | 自动发现范围 | 已有连接方式 | 后续动作 |
| --- | --- | --- | --- |
| Codex | PATH、常见 bin、有限 nvm 目录和 Codex App 已知路径 | 项目 `.codex/config.toml` 中自有 stdio MCP 项，启动 Memlane bridge | 项目中重载 MCP，验证读写 |
| pi | PATH、常见 bin、有限 nvm 目录 | 选定 pi 配置目录的 Extension，请求项目来自会话工作目录 | 重载 Extension，在选定项目验证 |
| TRAE CLI | `traecli`／`traex` 的已知 bin 路径 | 项目 `.trae/traecli.toml` 中自有 stdio MCP 项 | 项目中重载，验证读写 |
| TRAE IDE | 用户与系统 Applications 中 TRAE CN 的已知 launcher | 项目 `.trae/mcp.json` 中自有 MCP 项，格式绑定已验证版本/渠道 | IDE 中重载 MCP，执行验证 prompt，等待工具回执 |

发现仅执行版本和能力帮助探测。TRAE 产品版本来自 App bundle，不能用 VS Code 基础版本代替。发现不连接模型、不读取登录凭据、不导入聊天、不修改 Agent 配置。手动检查有效安装路径可保存到 Memlane 自己的路径偏好，未支持版本也可记住路径。

连接复用 `internal/agents/config.go` 的增量配置、备份和自有项撤销；请求复用 stdio bridge、工作区身份、范围与连接代次校验。自动发现不会触发连接。日常接入与后台整理执行者分别验证，沿用 Agent 自己的认证和模型选择。

## Cognee Integrations 参考结论

参考服务是本机 Cognee 前端 `http://localhost:3000/integrations`，源码位于相邻 Cognee 仓库，核对时 HEAD 为 `e93a4f0c76e6af27142c25189f14c200e1749d51`。已读取 HTTP 页面和真实页面代码；headless 渲染超时，参考页视觉检查未完成。

| 参考能力 | 核对结果 | 本期取舍 |
| --- | --- | --- |
| 卡片和分步连接 | 静态支持目录，按插件、MCP、Extension 展示步骤 | 复用流程和配置格式，结合本机发现显示安装信息 |
| Connected | 按近 24 小时、最多 50 条 session 判断；另一 dashboard hook 将历史连接标记存在 localStorage | 继续以当前连接、真实验证回执和服务状态判断可用 |
| 官方 MCP | 支持 stdio/SSE/HTTP，通常由 `uvx cognee-mcp` 提供 remember/recall/forget | 复用 MCP 接入协议；已有 Memlane bridge 保持来源、版本、范围与确认门禁 |
| Codex／Claude 插件 | hooks 捕获 prompt、工具轨迹与会话，退出时同步；本地模式准备另一套 Python 服务 | 结构供后续参考；本期不安装聊天采集 hooks 或另一套记忆服务 |

官方 remember、forget 等动作具有写入副作用，直接接入需另行满足 Memlane 确认和历史规则。后续采用插件前需定义用户批准的采集材料、出处与知识的区别，以及受信服务审查方式；自动发现不代表授权会话采集。

插件按固定上游 `997fd6f72253aa97b49996eb1b032793f0257935` 核对：[Codex 说明](https://github.com/topoteretes/cognee-integrations/blob/997fd6f72253aa97b49996eb1b032793f0257935/integrations/codex/README.md)、[Claude hooks](https://github.com/topoteretes/cognee-integrations/blob/997fd6f72253aa97b49996eb1b032793f0257935/integrations/claude-code/hooks/hooks.json)。本期复用协议和产品流程，未复制其插件源码。

## 当前证据与验收边界

真实发现：`build/integrations-reference/local-agent-discovery-final.json`。

| Agent | 本机版本／渠道 | 本轮证明 |
| --- | --- | --- |
| Codex | codex-cli 0.147.0 | 找到安装，读到 stdio MCP 帮助能力 |
| pi | 0.84.1 | 找到安装，读到 Extension 帮助能力 |
| TRAE CLI | traecli 0.214.1，internal | 找到安装，读到 stdio MCP 帮助能力 |
| TRAE IDE | CN 3.4.15，stable | 找到安装并读到产品版本；该版本项目 MCP 接入尚未验证 |

TRAE 既有格式门禁继续绑定 CN 3.4.14、stable、具体 commit 和隔离 launcher 能力。本轮没有为 3.4.15 放开门禁；旧门禁通过也不能代替真实 IDE 模型验收。

回归覆盖更新版本、不同 commit／渠道、产品版本、嵌套／重复元信息、未验证版本不能生成配置或安装回执、旧回执不能盖掉当前版本，以及服务 ready 不能使未适配连接可用。测试 bundle、launcher 和连接对象明确为合成夹具。

本轮真实窗口和检查结果记录于 `build/integrations-reference/`。完整读写接入仍以 [P2](p2-validation.md)、[Agent 验证](p3-agent-validation.md) 与 [IDE 验证](p3-ide-validation.md) 各自证据为准，发现通过不能代替全部新版本日常接入通过。

### 本轮已通过

- `tests-final.log`：`sh scripts/go.sh test ./...`。
- `vet-final.log`：`sh scripts/go.sh vet ./...`。
- `race-final.log`：`CGO_ENABLED=1 sh scripts/go.sh test -race ./internal/agents ./internal/desktop -count=1`。
- `build-final.log`：完整 UI/helper/LaunchAgent/Agent bridge 的隔离 automation App 已构建并通过本机 ad-hoc 签名完整性检查；没有进行安装升级或正式分发验收。
- `actual-app-discovery.json`：真实 App 显示四种本机安装和版本、TRAE 3.4.15 未适配提示、重新发现；验证 App 已退出，cleanup 为空。
- `screenshots/agent-discovery-trae-system.png` 和同名 capture JSON：实际系统窗口截图，已打开并对照首页／三栏设计图，保持紧凑三栏、连续浅蓝侧栏和单色操作。

窗口使用隔离 HOME／Memlane 设置，仅提供本机已知可执行文件的 PATH。未注入示例 Agent 安装，未启动记忆服务或模型，未修改日常 Agent 配置。本机验证包包含工作区保留的其他未提交改动，本次只验证发现与展示，不能据此认定暂停的 F1e 等工程验收通过。`source-evidence.json` 保存本切片源码摘要与检查路径。

本轮对实际 diff 自查了配置／执行门禁、旧客户端私有 IPC（未改协议）、现有安装回执与当前探测、范围和取消路径。发现并修复两处状态问题：旧回执盖掉当前版本，以及不同安装路径在列表继承已验证状态。相应回归通过。没有修改数据库 schema 或记忆写入契约，也没有采用需要用户认证信息的官方插件。
