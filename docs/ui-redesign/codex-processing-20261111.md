# Codex 记忆处理桥与授权后协议恢复

2026-11-11，完整包004之后的源码批次。实现已接入保存、验证、明确启用、冻结任务和Cognee gateway分派；本批只运行协议夹具和真实无模型连接验证，**没有调用真实Codex模型，不属于真实推理、质量或Native验收通过**。

依据[桥接要求](../agent-model-bridge-requirements.md)、[研究边界](../research-cognee-agent-appserver.md)、pi既有[验证记录](../pi-model-bridge-validation.md)及[电脑级连接证据](codex-computer-connection-20261111.md)。连接证明与模型验证分别管理，保存或协议连通都不能启用模型。

## 实现

`agents/codex_completion.go`复用已有app-server JSONL拥有者、helper控制管道和进程组回收。每次请求创建私有HOME、CODEX_HOME和空工作目录，只有白名单中的当前模型/provider配置进入临时配置；不复制MCP、日常hooks、插件或会话，不读取项目或历史聊天。禁用shell、文件、图像、web、协作、技能和code mode等工具，空dynamicTools不是唯一隔离措施；权限profile拒绝任意文件与工具网络访问。任何工具/审批请求、工具item或工具delta都立即失败并回收，不能返回候选。

认证在Codex自己的短期私有目录使用有界快照，任务结束删除；不会把密钥写入Cognee或账本，也不会让临时登录刷新改写原auth文件。只传入当前provider明确引用的环境变量；HOME/CODEX_HOME/PATH、Node加载选项、DYLD等隔离/注入变量不能充当credential env。版本检查同样使用最小私有环境。未调用真实用户凭据，实际账号、Keychain与token刷新兼容性仍待验收。

当前模型metadata和实际执行共用同一个解析器。显式配置与原生/default provider/model都进行准确匹配，缺省项通过私有no-turn thread回复解析，不猜默认模型。显式profile目前返回`codex_profile_unsupported`，不能静默忽略profile。只读API为`agents.CodexCompletionSelection(ctx, installation, guard)`，不会产生模型turn。

完整Schema原样交给`turn/start.outputSchema`，收到该请求ACK后才标记Schema已请求；最终单一非commentary消息还必须通过完整本地Schema校验。独立`CodexCompletionReceipt`保留thread、turn、permission profile、Schema摘要、ACK、本地校验和无工具证明，**不伪造pi的HTTP 200字段或供应商原生响应证据**。线程/轮次、模型/provider、权限和终态不一致均失败。

取消发送真实turn/interrupt，分别等待ACK及同一thread/turn的实际终态；ACK不替代终止。即使取消后到达成功结果也不能释放。始终回收实际进程组，不能确认回收时由既有Owner停止服务；cleanup的实际exit可为-1，不冒称自然退出0。

`processing/codex.go`绑定当前连接代次/协议证明、准确Agent版本、config.toml/auth摘要、选定provider环境、模型配置与helper身份。私有计划不序列化。排队后、执行前、执行后及正式任务发布前均复核依赖；切换保存候选或当前使用不会替换旧任务冻结选择，配置漂移、撤销或验证失效则拒绝旧结果。组织候选仍走完整`Snapshot.Decode`与既有审核/发布事务，不直接覆盖有效知识。

Owner、gateway及正式组织任务分派由主Agent集成，使用`CodexCompletionConfirmed`的独立条件，不借用pi HTTP状态。安全错误只映射版本化`codexErrorInfo`和有界HTTP状态，区分认证、额度、限流、模型、上下文、请求、隔离、上游、超时和传输关闭；不按上游message猜错误或保存原文。本机0.167.0生成的JSON Schema核对过这些机器字段，未调用模型。

## 授权后无模型恢复

电脑级Codex授项目范围后，旧协议证明失效。现在原`VerifyProtocolConnection`可在明确选定且确实已授权的workspace使用真实host cwd调用memory_scopes，核对返回scope集合、绑定项目与实际当前项目；仅通用scope仍可不选项目，初始空授权仍没有项目前置。

安装回执新增`ProtocolScopeDigest`、`ProtocolProject`与`ProtocolWorkspaceDigest`，没有数据库迁移。总体连接只认当前代次协议证明，不接受旧读写字段遮盖；逐项目状态另用`CodexProjectVerification`比较项目ID、规范路径及目录inode身份。A的证明不能显示B已核验；目录被替换也失效。

daemon已有批次可接已有Codex进入待重载验证。`AgentConnectionBatchAction.Workspace`由daemon用Store登记的仓库身份及AllowedScopes核对，冻结具体项目/目录身份；`ActionDigests`单独绑定操作、目标代次与范围，保留原Actions kind以兼容UI。相同operation换范围冲突，重试沿用原范围。旧项目模型runner仍有自己的历史绑定要求，本批不把新范围证明冒称该runner已经迁移。

## 检查与证据

- agents协议夹具：完整Schema、私有配置、原auth不变、实际进程回收、模型/权限变更、任意工具请求或delta、跨thread/turn、无效JSON/Schema、多final、失败终态、取消后迟到成功均覆盖。
- Owner/真实SQLite/协议进程：保存不启用、连接proof不能启用模型、验证后仍待明确启用、操作重放不重复执行、冻结任务、伪造出处拒绝、已存在知识不被覆盖、活动取消ACK/终态/回收与排队依赖漂移均覆盖。这些模型内容是明确夹具。
- `build/goal-codex-processing-20261111-001/connection-lifecycle-runs.json`：真实已安装CLI0.167.0和App内alpha.3.2、source bridge、daemon与SQLite无模型授权恢复全部通过，受管70794 exit0。scope回执`194021.415023000`与`194025.055749000`证明授权→旧代次失效→重载→在B重新验证，且A/B证明不互用。
- 最终受管24838专项race exit0：agents30.448s、processing10.400s、daemon5.233s、store4.185s。普通定向测试与此前专项race亦通过；原先fixture误用Schema enum/const和macOS路径别名预期失败已经保留在任务输出，修正测试数据，没有放宽生产验证。

实际模型、真实供应商/账户限制、原生上游Schema行为、知识质量、全链路取消、防递归采集及新包Native操作仍须用户明确授权后独立验收。本批未操作Native、未完整打包、未读取用户聊天或认证内容，临时配置/二进制已清理；保留源码、小型回执和失败边界。

### 005集中检查的夹具启动修正

005普通及race全量检查首次启动Codex夹具时，版本探测分别触及生产5秒上限；原始失败保留在`build/goal-concentrated-20261111-005/go-test.log`和`go-race.log`。两个测试夹具原先通过系统Python启动并导入模块后才处理静态版本/help。现仅将夹具metadata和守护控制字节前置到`/bin/sh`，协议仍由原Python代码运行，使用`-S`跳过无关site初始化。生产探测上限、完整Schema、权限、取消、终态和回收断言均未改变。

新增回归在独立HOME/CODEX_HOME下故意设置无效PYTHONHOME；修前两个包均无法返回版本，修后版本/help不依赖Python初始化。普通与race两批同时运行，各重复5次Codex/ComputerConnection/Protocol专项，均exit0：agents155.508s/157.070s，processing38.784s/43.702s。证据位于`build/goal-codex-fixture-startup-20261111-001/`（red/green-probe、pressure-test、pressure-race、startup-measurement）。对005冻结清单核对，生产Go改动为零。

96次、12路并发的metadata对照未复现原5秒超时；未清操作系统缓存，首次执行延迟也出现在shell对照。因此不能将原超时精确归因于Python site，只能确认已移除不必要的解释器启动依赖，并通过上述并发回归；集中全量检查仍由主Agent重跑确认。

### 005集中验证纠正：夹具启动根因与最终处理

前述001 shell元数据分派去掉Python初始化依赖，但没有解决原始5秒超时；005全量96362与race44430再次失败，失败记录保留。002对照180次执行：直接执行新脚本43/60超时，同内容由既有/bin/sh解释0/60超时，既有/bin/echo亦0/60；43次超时中42次首条入口标记尚未写。采样显示解释器dyld入口前等待，具体系统子因尚未证明，不能归因于Python或某款安全软件。

仅两个_test.go夹具改为复用已运行的原生testbinary入口（新symlink），静态元数据不进入Python，协议主体继续经原Python JSONL契约；不改生产5秒超时、Schema、工具隔离、取消或进程回收断言。60个新symlink对照零超时，metadata两包各count10通过；6957普通及28610 race专项各count3通过。证据build/goal-codex-fixture-startup-20261111-002/report.json。005最终全量62471/race16146尚在运行时，不宣称全绿。
