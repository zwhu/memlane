# Codex 电脑级连接：版本限定后台与协议证据

2026-11-11。本批在完整包004之后实施；不替代004的Native基线，也不宣称A05/A07/C03整页、真实日常采集或Codex记忆处理模型桥已经验收。

## 依据与可信工作目录

遵循[电脑级连接契约](agent-connection-contract.md)、[空授权连接协议](connection-protocol-20261110.md)与[模型桥要求](../../../../agent-model-bridge-requirements.md)。连接身份不绑定项目、工作目录或用户首任务模型；项目授权仍由App独立确认。

官方[MCP配置文档](https://developers.openai.com/codex/mcp)说明stdio的`cwd`是服务器启动目录。[固定0.167.0源码的server身份](https://github.com/openai/codex/blob/rust-v0.167.0/codex-rs/codex-mcp/src/server.rs)将无显式cwd的本机stdio之`local_stdio_fallback_cwd`纳入连接身份；[连接池实现](https://github.com/openai/codex/blob/rust-v0.167.0/codex-rs/codex-mcp/src/connection_pool.rs)限定新版协议才能共享连接，旧协议保持chat私有。

新`--host-workspace codex-stdio-v1`仅用于MCP启动，不能用于CLI单次call，不能同时提供固定`--workspace`或初始化模式。bridge只从操作系统`os.Getwd`取得并规范化host启动目录，固定目录身份；请求前再次核对目录未被替换。它不使用模型参数、`_meta.cwd`、`threadId`或opaque sandbox对象推断项目。SDK默认已支持2026协议，因此此路径明确只协商`2025-06-18`，不参与连接共享。电脑级自有配置不写`cwd`，检查时拒绝固定cwd或失效TOML。

本机实测同时限定两个准确版本：CLI `codex-cli 0.167.0`，App内 `codex-cli 0.167.0-alpha.3.2`。其他版本在`agents.ComputerConnectionError`和daemon批次中明确unsupported；不由版本号大小推断兼容。旧项目adapter保留原边界，不改名冒充电脑级连接，也不被批次复用为电脑级成功。

自有MCP参数同时冻结所发现的绝对Codex executable与准确version。每次bridge启动在新的私有空配置执行该executable的`--version`，有时限和输出上限；原路径CLI升级、取消或检查失败时在提供工具前拒绝，要求重新接入。它不读取用户的认证或模型配置。受支持的路径是用户明确选择并安装的本机Codex；不能从此推导任意其他客户端/二进制借用这份配置的兼容性。

## 已实现

- `InstallOptions.ComputerLevel`与显式`CodexDirectory`支持该配置目录的增量MCP安装；保存原文件备份、保留其他条目、冲突时不覆盖，自有项卸载后恢复其他配置；拒绝配置文件symlink，避免意外改写其源。安装无固定工作区。实际本机操作均在新临时目录，没有修改用户`.codex`。
- 批次为Codex创建原有`ConnectionOnly`稳定空授权身份，安装、待重载、协议验证、失败重试及取消仍使用已有daemon拥有者。无需新增schema，schema28留给独立提交账本。
- 协议验证读取自有安装block后，在新的私有HOME/CODEX_HOME和空目录启动真实app-server，只发送initialize、thread/start与直接memory_scopes调用。没有turn/start、登录、模型解析、auth链接、聊天读取或外部模型请求。内部不可用loopback provider仅阻止账号/模型发现，不成为用户保存的配置或模型。
- protocol证明与历史读写证明分开持久化。只接受真实空授权回执；取消、失败、断开及旧代次不能沿用成功。授权变化增加代次，旧stdio请求收到reload_required。具备协议证明不表示具备记忆处理模型能力。
- 日常请求使用原有daemon/Store.Authorize，以host目录求当前项目，范围继续受明确App授权限制；不隐式扫描或创建项目，不自动安装采集hooks，不扩大到所有允许项目。

## 真实证据与失败记录

小型源代码和JSON回执位于`build/goal-codex-computer-protocol-20261111-001/`，临时配置、SQLite与source bridge二进制均在任务结束清理。仅执行定向Go测试与source CLI build，没有Native操作或完整包构建。

| 证据 | 结果与范围 |
| --- | --- |
| `report.json`、`hooks-report.json` | 直接MCP API的最初调查。host覆盖伪造threadId，但调用端提供的opaque sandboxCwd会被原样转发；因此不能将它作为授权依据。这些probe的passed仅指探针执行完毕。 |
| `loopback-turn-report-first/second/third/fourth.json` | 保留失败或不足证据：合成SSE/工具批准门禁与探针解析错误。真实turn完成不等于MCP或自有hook完成。 |
| `loopback-turn-report.json`、`app-loopback-turn-report.json` | 本地固定Responses夹具驱动真实CLI，同thread从生成目录A切换B；host更新MCP进程cwd，两次真实MCP工具回执均匹配当前目录。无外部模型、无密钥；不冒充模型质量验收。 |
| `cli-concurrent-report.json`、`app-concurrent-report.json` | 同一个真实app-server内两个thread的MCP调用同时进行；各自PID不同、约0.5秒执行区间重叠，目录不串线。零turn、零模型请求。 |
| `codex-empty-grant-190516.753558000.json`、`190517.113260000.json` | 失败：先前独立build产物被既有磁盘finish清理，安装发现bridge不存在。保留，未算协议通过。 |
| `codex-empty-grant-190813.111765000.json`、`190815.487767000.json` | 两版本真实source CLI bridge→daemon→SQLite空授权通过；无turn/item启动、0知识/0项目、独立协议证明、撤销精确disconnected、成功清除与进程组回收。Codex的受控关闭实际exit为-1，不伪称自然exit0。 |
| `codex-scope-isolation-190813.910801000.json`、`190816.199522000.json` | 两版本真实bridge与daemon：并发thread A/B返回各自current_project；伪造request meta目录被忽略；未授权当前项目及显式外scope均scope_denied；授权代次变更后旧进程reload_required。仅生成两个授权测试仓库，无用户知识/聊天。 |

调查还实际配置了自有hooks。虽配置读取能看到定义，当前运行只收到system hooks事件，没有取得自有SessionStart/PreToolUse回执；因此没有以hook方案交付或绕过信任门禁。官方[hooks文档](https://developers.openai.com/codex/hooks)的host cwd与tool_use_id是后续可研究接口，本实现不依赖它；opaque策略按[官方定义](https://github.com/openai/codex/blob/rust-v0.167.0/codex-rs/codex-mcp/src/sandbox_metadata.rs)保持不解释。

早期CLI loopback报告沿用了错误命名的`model_turns_started: 0`，不能据此声称app-server零turn；其真实事件显示两次turn/start，均请求本地合成上游。随后探针拆成`real_model_requests`与`app_server_turns_started`，App报告准确记录0与2。原始失败/早期报告保留，不改写为新的验收回执。

## 检查与剩余边界

配置测试覆盖增量保留/备份/卸载、固定workspace拒绝、版本限制、协议证明不自动授范围、授权后旧证明失效；原批次“始终unsupported”夹具改用尚未适配的TRAE CLI，未放宽生产门禁。真实测试以`MEMLANE_REAL_CODEX`、`MEMLANE_REAL_PROTOCOL_BRIDGE`、`MEMLANE_REAL_PROTOCOL_OUT`明确启用，默认跳过，不触碰当前用户配置。

最终启动版本冻结加入后，受管3133两版本真实链exit0；新的空授权回执为`codex-empty-grant-191532.258124000.json`、`191534.594406000.json`，真实范围回执为`codex-scope-isolation-191533.056928000.json`、`191535.212522000.json`。专项race受管2664的agents/agentbridge/daemon均输出ok（19.904s/5.261s/4.420s）；较早99467亦通过。26521/66682因并行Cognee提交IPC字段未落而编译失败，字段稳定后上述最终重跑通过，不将它算作Codex协议失败或隐藏成从未失败。定向普通测试23363和source CLI build通过，差异空白检查通过；没有执行全量包或Native验收。

复现：通过`python3 scripts/task.py -- python3 build/goal-codex-computer-protocol-20261111-001/run_real.py <绝对CLI路径> <绝对App内CLI路径>`在同一受管任务中临时build source bridge并运行两版本真实协议/范围测试；生成回执后立即清理二进制。

Native多选卡片、目录选择/重载提示与新包同包闭环由主Agent统一验收。现有项目整理模型runner仍要求自己的历史读写/绑定证明，未将本次连接证明冒充项目模型能力；后续项目范围适配须单独完成。实际编码会话保存/读取、新会话上下文、官方采集、真实模型推理/质量与完整Codex记忆处理桥均未在本批验收，不从连接证明推导这些能力。用户主动改写更高优先级Codex配置或启动参数属于配置变化，不作为本版本默认stdio生命周期的证明。
