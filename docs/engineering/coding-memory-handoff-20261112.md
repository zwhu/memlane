# coding-memory／代码图谱与权限清理交接

状态：**开发已暂停**。用户2026-11-12要求「先暂停所有开发，handoff，文档该更新更新，该整理整理」。本次仅整理文档和证据，不能据交接自动恢复研发。记录时刻：2026-11-12 15:21 UTC（北京时间23:21）。

## 先读这三份文档

1. [目标规格](../coding-memory-codex-spec.md)：原coding-memory、官方代码图谱、实体／知识关系图范围完整保留，不重做已有界面。
2. [移除权限管理](remove-agent-permissions-20261112.md)：最新用户要求，覆盖所有旧权限设计。**所有Agent默认可读写，不保留项目／Agent白名单、角色、授权／撤权、读取复核等能力。** 连接认证、任务取消、去重、来源版本及执行归属继续保留。
3. [工程进度与证据](coding-memory-production-20261112.md)：001–013为历史阶段，014记录最新范围改变后的实际状态；历史权限方案不再作为后续实施依据。

## 当前已完成与验证边界

| 部分 | 已有结果 | 尚不能宣称 |
| --- | --- | --- |
| 正式Codex通道 | 严格Schema适配、配置／生命周期基础及源码检查 | 日常自动整理已跑通 |
| 官方组件 | 固定服务ec6f408、插件a176248；v13曾构建／并排准备 | v14已构建／安装、日常已切换或回退可用 |
| 官方图承载 | 固定前端8853378本地构建；真实MyGo WKWebView合成图运行 | 已接入日常真实图、项目页与全部来源导航 |
| 官方代码SDK | 真实SDK→Enola→Ladybug两轮合成写图／刷新，旧symbol与Insight删除、新symbol可查询，同一DataID、不同PipelineRunID | 日常项目可用、真实Go daemon完整串联、取消全矩阵或Codex使用通过 |
| 权限清理第一批 | 中央Agent请求解析不再使用白名单；项目访问管理API／IPC／编辑器及专用测试删除；跨项目来源脱敏删除；Agent连接页许可勾选删除；项目任务绑定／初始化及使用记录的白名单检查删除 | 全仓权限功能／字段／表／文档已清干净 |
| 本轮验收 | Store新默认行为＋来源保留＋首次项目落库race通过；daemon跨项目读取／重放去重通过；UI Tester设置／连接场景通过 | 全量回归、真实Native窗口或日常链路验收通过 |

权限清理证据：`build/coding-memory-production-20261112/remove-permissions-014/`。最后有效日志为`store-final-race.log`（4.148s）、`daemon-tests-usage-fixed.log`（3.193s）、`desktop-final.log`（1.011s）。四包编译记录`compile-fixed.log`与早于最后局部修改的vet保留；最终局部修改由上述定向测试编译。不是全量回归通过。

官方SDK真实写图证据：`build/coding-memory-production-20261112/official-code-013/sdk-index-typed-query.json`。只用了临时合成用户／代码／存储，已关掉所属SDK worker并清理临时目录，没有新日常服务／模型调用。其他原始失败日志全部保留。

## 未完成的调度代码：必须先重整，不能直接交付

`internal/daemon/official_code_owner.go`、`internal/records/code.go`、`internal/store/official_code_jobs.go`、`internal/serviceipc/types.go`和`internal/daemon/server.go`在收到移除权限要求前写入了epoch绑定、私有任务观察、稳定源码槽和HTTP调度入口。**代码已存在并可编译，refreshCode也已接入该分支，但调度本身未做集成验收。** 013末尾的“daemon尚无处理器／默认unavailable”只描述当时状态，不能当暂停时的源码现状。

- 后续按新范围删除项目／Agent权限逻辑，保留后台连接认证、任务输入／来源核对、取消和去重。私有服务能力令牌与运行所有权不是Agent ACL，不能为清权限而丢掉取消／去重的所有者。
- 已发现但尚未修复：`j,err=BindOfficialCodeEpoch(...)`失败会以空返回值覆盖defer要使用的原任务；应在恢复后保留原任务再处理绑定结果。
- 已发现但尚未补齐：`matchesOfficialCode`只核对身份和终态，还未在daemon侧验证completed所需DataID／PipelineRunID；不能据此先认定执行可发布。
- 完成／取消不明时保留槽和目录的处理、daemon重启后的epoch核对、未发出HTTP前中断、断连／撤换输入与实际存储取消均待端到端验证。
- 同epoch的取消tombstone改动曾拟定但整份patch验证失败，**没有应用**；Python接口当前对未知操作仍返回409。不要根据计划误认已有tombstone保证。
- 目前没有打包／运行这些新调度代码，没有新增日常索引任务；不能仅凭编译或SDK合成写图证明它可用。

## 恢复后的顺序（须用户明确恢复）

1. 重新读AGENTS、目标规格和移除权限记录，核对Git脏树／其他session改动与实际运行根。不要重置、清空build或把整个目录当本session所有。
2. 先完成权限清理：遗留AllowedScopes／allowed_scopes、采集consent、历史读取reauthorization、自动处理CodingMemoryGrant、残留来源／图谱访问拦截、相关接口／UI／测试及持久化。迁移只删除权限元数据，不删除知识、原始记录、来源和版本；schema清理迁移尚未实现。
3. 同步清理全仓旧权限文档和计划。已更新AGENTS与目标规格；其余历史产品／设计／工程文档仍有旧内容，不能沿旧规则继续加权限。
4. 冻结新“所有Agent可读写”契约，补齐全量回归及实际Native页面验收。旧权限专用测试应移除／改为新默认行为证据，不能继续要求旧拒绝结果。
5. 再完成并验收Go daemon→稳定源码快照→私有执行上下文→官方SDK的实际串联，随后完成官方有界代码查询、Codex真实使用和项目页／关系图接入。
6. 官方插件coding-memory配置传递、真实日常采集→模型→知识→新会话复用、日常一致备份／服务切换及恢复仍待完成。不是仅剩代码图谱或仅剩权限清理。

## 运行环境与停止边界

- 暂停时只读核对：旧daemon PID76077、旧Cognee PID76867仍存在；没有停止或重启它们。没有观察到仍在运行的Go编译／测试／vet或本轮SDK验证脚本。
- 日常根仍引用`/var/folders/nv/g6m5_3nx1r9_v9dk3zh_5zw40000gn/T/memlane-task-gnwqmjhd/initialization/client-responsive`及其`data`、`official`。虽然位于临时路径，仍是实际运行依赖，**不得当临时垃圾删除**。恢复时重新核对，不凭旧PID猜当前状态。
- 日常库本次没有迁移；源码SchemaVersion为38。旧日常schema上次审计为31，本次暂停未重读数据库，不把旧读数当最新确认。
- 开发阶段不构建／验收完整App包，独立组件v14也未构建。v13候选、原BGE、已有完整包与实际引用依赖继续保护。
- helper同身份更新／同路径恢复、自有Codex hook捕获、旧README语义图等既有停止问题计数保留；没有恢复或增加重跑。恢复整个Goal不等于另行恢复已停止具体问题。
- 旧后台执行文件恢复不证明完整资源／签名／重启／回退可用；切换前仍须处理这一已停止边界，不能直接关旧服务。
- 至少保留100GiB可用空间，恢复开发后所有构建／验收继续走task.py或go.sh。暂停前空间约123GiB；不删除他人worktree、未提交源码、知识库、会话或运行依赖。

## 工作区与交接物

共享工作树包含大量本轮及其他session的已修改／未跟踪文件，未创建提交。请直接读取当前源码和证据，不能只看HEAD，也不能按untracked批量删除。014的checks.json记录的是所列文件在暂停时的完整内容摘要，不表示文件内全部改动都归本轮所有；需要隔离开发时先保留完整可追溯快照。

本交接完成后继续保持暂停，不启动任何开发、测试、组件准备、模型或实际任务。
