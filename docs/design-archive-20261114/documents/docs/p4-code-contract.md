# G 代码图谱正式契约

2026-11-08，G0 底座及 G1 本机 App/traex 静态主流程通过，见 [G1 验证](../../../p4-code-app-validation.md)；G2 更新、取消、根目录语言、CLI 恢复与组件升级/回退本机通过，见 [G2 验证](../../../p4-code-update-validation.md)。选定路线是固定 Cognee-RS `151905c302fc9b5b9aad88d6b13931c09f0eac30` 的 Rust worker + Ladybug，静态抽取器为 Enola 0.4.30 / v351。单独抽取、普通实体图或编译不能算完整代码图谱完成。

## 授权与来源

项目必须在 App 中明确开启；开启记录绑定已登记项目和实际 Git worktree 根。不同 worktree 共享知识范围，但使用不同 code dataset；代码 dataset 与知识 dataset 分离。日常 Agent 只能查询已开启、且连接明确授权的项目对应工作区，不能开启扫描、导入图或执行任意图查询。全局经验不隐式包含代码；其他项目仍要求明确范围授权。

扫描使用 Git 文件清单并遵守 `.gitignore`，另排除 `.git`、`.enola`、依赖/构建/缓存目录、隐藏凭据和符号链接。首轮语言仅 TypeScript（含 TSX）与 Python；复制相应静态源和必要包清单，不运行项目脚本、安装依赖或读取聊天。每次限制 2000 文件、单文件 1MiB、总 32MiB、20000 节点/60000 边；超限明确失败，不能静默得到部分“就绪”图。

原始 worktree 只读。私有副本置于服务管理目录 `code-extraction/<job>/shadow`，Enola 默认输出只写该副本 `.enola` 与同 job 的私有 HOME；不复制项目的 `mcp-arch.yaml`、自定义 extractor、Agent 配置或 `.git`。实际原项目零写入须通过前后文件清单及内容核对，不能只依赖参数名。静态抽取不调用模型，不需要 Cognee 模型桥。

来源快照记录规范 worktree、Git common directory、commit（无提交为空）、选定语言/忽略规则版本、每个相对文件路径/内容 SHA/大小及整体 digest；开始和提交前重新核对。dirty/未跟踪文件使用内容快照，不能只比较 commit。文件读取约束在打开的 worktree root 内；跳转到外部的路径和符号链接不参与抽取。来源变化时拒绝该次结果，保留旧图为过期状态。

## 图存储、查询和限制

Rust worker 增加有类型的 `code_build` / `code_query`；原文协议 1 和 generation 保持兼容，新增能力以组件版本/握手明确探测。服务只写有界规范化导入文件到 worker 管理目录 `code-imports/<UUID>.json`，worker 不接受外部绝对路径、Cypher 或全 dataset 查询。节点保留文件、行号、语言、符号类别、原始 extractor ID；关系区分 declares/contains/calls/imports。未解析的外部依赖单独标为 unresolved，不伪造本地目标或完整运行时调用图。

每个 dataset/snapshot 使用独立 Ladybug 图目录 `code/<dataset>/<snapshot>`，先构建私有 staging，持久 flush/close 成功后发布不可变目录。数据库中已持久化的 current snapshot 是唯一正常查询入口；失败、取消或旧 attempt 即使留下派生目录也不能成为 current。重放相同 snapshot 必须内容 digest 一致；不能覆盖既有图。查询实际读取 Ladybug 节点/边，支持文件、函数/符号、调用、依赖和最多 3 跳的可能影响范围。影响是静态候选，不代表运行时必然影响。结果有 snapshot、commit、worktree、文件 SHA、行号与 unresolved/truncated 标记；没有来源/版本绑定的结果不展示为当前。

## 更新、恢复与确认

启用/停用由受信 App 显式操作。持久设置、generation、任务、来源快照和 current receipt 随实际功能引入；schema 迁移前复用一致性备份，临时旧库证明所有旧数据保留。停用增代、取消活动任务并排除旧图；重新启用产生新代。服务停止后不扫描、不运行或恢复计划。

手动刷新和文件变化共用每 worktree 单活动任务及服务总并发门禁。轮询仅检查已启用 worktree；变化合并到一个新快照，删除/重命名通过新不可变快照替换，完整小规模重建避免残留边；内容未变不重复建图。故障保留最后成功快照并显示过期/失败，重试由用户或下一次实际变化触发，避免每轮重复失败。重启将未完成任务标中断，启用配置保留；旧 generation、迟到结果或已取消任务不得改变 current。文件/Git/抽取/worker 等待不持有 SQLite 写事务。

## 验收矩阵

G0：合成 TS/Python，真实固定 Enola → 规范化 → 真实 Rust Ladybug 持久化/搜索/调用/依赖/影响 → worker 重启；验证原目录无写入、忽略/外链、输出路径、范围与错误。证据 `build/p4-code/`，不标 App 或日常 Agent通过。

G1/G2：真实 App 栏内开启/关闭、三栏查找/详情来源，受控日常 Agent 查询，同名项目/不同 worktree 隔离，未启用不扫描，变化合并/删除/重命名/过期/失败/重启/取消/迟到；正式迁移备份/恢复及完整可重定位组件包。开发 UI 前实际看设计图，完成后核对真实窗口截图。正常静态查询不需模型；日常 Agent 使用必须有对应 Agent 回执。故障夹具明确标注。

Developer ID/公证/正式更新源/干净系统仍属 F3，G 的本机 ad-hoc 包不能替代。

## G1 接口与运行收口

schema 12 只新增实际所需 code_worktrees、code_jobs 和 code_actions；一致性 VACUUM INTO 备份在迁移前完成。code_actions 的 operation UUID、请求 digest 和固定原回执防止重放改变授权；回执 replay 标记只用于调用方，不是新的开启指令。已有授权变更使用 expected_generation，停用保留最后成功 receipt/source manifest 并增代。每个 worktree 最多一条 queued/running job；当前服务只串行扫描/建图，已开启 worktree 上限 16，15 秒轮询合并变化，不创建未启用项目的扫描任务。

后续 F1a schema13 新增原文检索重建任务，不改变代码表或扫描授权，见 [维护契约](p4-index-recovery-contract.md)。G1/G2 的 schema12 记录是当时验收版本。

受信 IPC code_action 接受 enable/disable/refresh，均需要 App 明确操作；code_list/code_jobs 读取持久状态；code_query 由 worktree record ID 选取服务 current snapshot，不能由客户端直接提供 dataset、snapshot 或文件路径。结果前后重读内容快照并复核授权代次/current receipt；变化明确返回 code_stale，旧结果从 UI 排除。失败保留成功历史；同一失败 source digest 不每轮重试，来源不可读时要求明确刷新。实际新变化可再次生成任务。

持久 job ID 与 generation 拦住取消、重启和迟到结果。服务 stop/quiesce 与发布共用 codeStopping/mutex 栅栏，取消活动上下文；中断保留 last current 并清除该次 observed digest。显式用户 stop 的持久标记仍阻止服务重启扫描，只有明确 resume 后才恢复已开启范围。抽取子进程使用已有 helper owner FD guard：数据库任务先存在，guard 收到 S 才启动，父服务 FD 消失则杀整组；不把数据库或 owner FD 传给 Enola。网络、文件读取、模型和 worker 等待不持 SQLite 写事务。

MCP/pi 增加只读 memory_code_search（mode/query/node/limit/hops 和可选 scope）。工作区来自桥的固定绑定或 pi ctx.cwd，不能由模型参数伪造。只查询已开启且连接授权的 worktree，其他项目要求显式授权 scope，并选已登记的该项目根；不授予扫描、导入、Cypher、删除或批准工具。初始化模式不开放代码查询。代码查询只更新真实连接使用回执，不虚构知识读取/采用计数。

主包仍无引擎/模型/解析器。worker 0.0.3（旧 0.0.2 合法图兼容；根模块需显式能力）与独立 enola-code 0.4.30-v351、原模型分别准备，配置明确选择解析器组件时一并校验版本和实际 executable；旧配置没有解析器项时仍可使用原文能力，代码入口明确要求组件。静态 Enola 包携带其实际 Apache-2.0 许可，不含 Python runtime。受控组件升级/回退已有 G2 证据；生产默认清单与正式源仍须 F 对应实际验收，不能用受控 loopback 签名源标记正式分发。
