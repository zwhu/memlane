# 项目与权限实施记录

2026-11-13，当前 owner session `01a1d316-954a-7c91-8191-7fdfe3d4a8c9`。原 owner `01a1d30a-d776-7f92-a7e9-9424e5fabc13` 按用户暂停后明确释放，仅留下两个未测试独立模块及本账本。当前用户附件再次明确启动 PA01–PA14，覆盖旧“无权限／共用 owner”的相应方向；历史全量 Goal 保持暂停。dev 模式，不做 release。

## 基线与完成条件

依据项目权限产品、规格与技术方案 `project-access-*-20261113.md`，已完整读取；固定官方源码事实及可行性入口亦已读取。使用仓库 plan-change-impact、verify-continuous-scenarios。当前 thread 已有同范围附件 Goal，继续该 Goal，不新建重复目标。Git 起始已有十一份未跟踪方案文档，保留归属，不自动提交它们。

最终连续流程：默认设置 → 新仓库新会话 → 管理侧幂等登记／建官方组／核对 ACL → 首次写前绑定 → 官方采集及处理 → 桌面自动出现／阅读 → 后续会话实际采用。PA-A–H 全部通过才能交用户验收。协议夹具不证明真实权限、采集、模型、Native。

| 要求 | 影响与依赖 | 完成证据 | 当前 |
| --- | --- | --- | --- |
| PA01/08/09/12 | project_control 登记、官方组、桌面目录 | 并发／路径测试、真实新仓库、自动发现 | 未实现 |
| PA02/03/06 | project_policy、独立 Agent、官方 ACL、项目设置 | 字段继承、有效 ACL 允许／拒绝、Native | 未实现 |
| PA04/05/07/14 | 固定官方插件 patch、无正文游标、blocked、所有触发入口 | 不读取／不排队、403 停重试、显式恢复、真实任务状态 | 未实现 |
| PA10/11 | 官方通用组、CRUD、单一 coding 注入 | 官方终态、另一真实会话采用、项目优先、拒绝不注入 | 未实现 |
| PA13 | 启动器、控制进程、目录刷新、阅读撤权 | 关窗／重开、缓存失效、复制入口撤下 | 未实现 |

## 连续场景与执行次序

1. PA-A/F：同一新仓库两 session 并发 resolve；符号链接／子目录复用、同名不同根和 worktree 分离；首次写前绑定 UUID，新组代码图默认关闭。
2. PA-B/C：默认 maintain/capture/process；显式 false 与 none 覆盖优先；修改模板动态继承，恢复字段；默认只读仍登记，不读取新正文；新 Agent 纳入继承而不改变覆盖。
3. PA-D/E：管理 owner 与两独立官方主体；直接调用 raw/session/search/graph/save/update/delete，核对用户、role、tenant 有效并集。同步失败保留目标 revision 与原操作；不 owner 回退，不把直接 ACL 当有效 ACL。
4. PA-E：403 阻塞原合法 buffer、无自动重试；禁采期仅 stat 推进水位，恢复不回扫；明确补交／丢弃分别验证。401／409／网络未知分开。
5. PA-G：通用知识官方 CRUD／处理终态，空项目及另一真实会话一次注入采用；项目约定优先，无权不注入，缓存撤下。
6. PA-H：Native 默认规则／项目设置／新建关联、目录15秒与激活刷新、关窗重开及来源往返；原 memlane/item-fe 会话不迁移、补采、重放或删除。

先独立模块及定向测试，再官方 HTTP 契约夹具、真实服务权限及采集／模型，最后与阅读 UI 连续 Native 验收。任何结果未知先查原身份，不重发内容／模型任务。

## 文件及运行所有权

- 已向两个指定 session 发联系消息。阅读 UI 首次发送／读取工具返回 list_turns unsupported；第二次发送成功，等待回执。app.go/client.go 尚未取得阅读 UI 释放，暂不修改。
- 总览回执：完成提交 `4db3b8b`，源码 app.go/client.go/dev.py 已释放；app/client 仍须与阅读 UI 协调。组件输入未改、未持有。保留 overview*.go、首日字段及 open --watch-ui 逻辑。
- 总览运行回执：旧 launcher／completion／official 仍拥有日常库；其附着 Native watcher 在 tool session29190。此为历史定位，任何启停前重新核对数据锁／进程树／启动时间；当前不启停共享服务、不切凭据。
- 本任务先独占新增 project_control.py、project_policy.py、project_access*.go、独立插件补丁及测试。本次未授权改写其他 session 源码归属。
- 初始 `df -k` 可用120769380 KiB（约115GiB）；测试沿 task.py/go.sh，运行受105GiB门禁。保留日常官方数据及 build 依赖。

## 既有缺陷与停止边界

- 已读精简版错误索引及 JSON；E17历史累计2，E09/E10/E15各1，其他按原账本，helper／固定328字节README停止问题不恢复。
- OVR01 按总览账本独立保留：真实召回／整理 BrokenPipeError，图 worker multiprocessing._flush_std_streams 刷新继承 stderr PIPE 失败；读端退出原因未知。总览仅定位，未修复、未重启、未重放。不能算作新 PA 缺陷或忽略此真实链路前置。
- 新 PA 产品验收尚未开始，无新增失败轮次。准备／方法错误单记。同一具体问题第4次失败停止该问题及依赖步骤，保留全部旧计数。

## 资源与证据

配置只存关联、策略、同步回执及 session 绑定，不存正文；私有key独立0600文件，不入Git。合成测试使用临时目录并自行收尾，不创建第二日常库。共享后台、watcher、凭据由交接规则保护。真实运行与Native证据待执行，未证明边界保持未完成。

## 新 session 接管与实现阶段

- 已收到原 PA session 明确暂停／释放回执：无后台任务，不持有共享文件、Native、watcher或凭据。新阅读 owner `01a1d316-960e-7280-a9fa-f4abb7abdb75` 先持有 app.go/client.go 阅读接线窗口，完成后释放；本任务暂不修改两文件。阅读将提供缓存失效入口供策略代次接线。
- 本地基线提交 `951e44e`，注释 tag `pre-project-access-20261113`：仅收纳已明确移交的两个草稿模块与本账本，未纳入其他 session 文档／源码、私有配置与数据。
- 新增 `project_control_server.py` 固定路由接口：管理与安装实例 token 分离，Agent身份来自token，拒绝身份伪造字段、正文、浏览器Origin；16KiB请求上限、固定超时。仅配置登记／策略核对，不执行内容或模型任务。
- 登记实现覆盖单写者锁、原子0600配置、并发同仓收敛、稳定路径身份、session固定、模板动态继承与字段覆盖、revision冲突、未确认操作只显式reconcile。发现关联既有组的草稿会套默认ACL，已在实现审查阶段改为先读有效权限并导入访问覆盖；不可表示的组合保持未确认，不能放宽。
- `python3 scripts/task.py -- .local/cognee-python/bin/python -m unittest discover -s engine/python -p test_project_control.py -v`：8项通过，临时合成目录退出后收尾；覆盖并发／路径、session固定、false／继承／恢复、持久重启、未确认不自动重试、手动自动去重、关联ACL保留及HTTP身份边界。属于源码／协议夹具，**不证明真实官方ACL、模型或Native**。
- 当前仍未完成：官方主体创建／安装实例配置、有效ACL真实核验、通用组、插件补丁、启动器接线、Go管理入口与Native，以及PA-A–H真实连续场景。尚无新PA产品验收失败轮次。

### 控制接口及插件策略基础（未接入日常运行）

- 管理侧新增安装实例主体创建，按固定官方 `POST /api/v1/agents/create?name=...` 无JSON body契约；官方key与控制token分别写0600文件，响应不返回凭据。先保存创建操作，再发一次请求；未知结果保留unconfirmed并拒绝自动重建。保存了原key的部分成功仅能显式核对原主体，不能换owner。
- Official HTTP夹具核对独立子用户／parent、四项授权请求、tenant/role并集。角色仍有write时撤销直接grant不能显示已核对。没有在日常官方服务上新建用户／数据组或改ACL。
- 新增 `project_plugin_policy.py`，待作为 `_memlane_policy.py` 装入固定官方插件补丁：按控制token＋宿主session隔离无正文状态；写拒绝／401／403保留blocked；自动process开关不替代手动权限；禁采时只stat推进边界，恢复首次hook建立新边界并跳过整轮；轮转／截短保持暂停；已有非受管launch拒绝静默绑定。该文件**尚未被官方插件入口调用**，不能将其单测当成官方capture已被拦截。
- 同一受管命令 `python3 scripts/task.py -- .local/cognee-python/bin/python -m unittest discover -s engine/python -p 'test_project*.py' -v` 扩至17项通过。插件策略测试以禁止打开transcript的断言核对stat-only、恢复边界、截短、blocked保持、手动／自动区分、既有session不迁移。测试结束未留所属服务／模型进程；日常库和并行Native watcher未动。aiohttp存在RequestKey建议告警，未影响断言。
- 阅读session确认继续持有 app.go/client.go 及 Native 操作窗口，并拟提供 `App.InvalidateReading(dataset)` 接口。共享文件出现其未提交修改属预期，本任务不纳入提交。
- 下一实施依赖：将策略基础接入固定官方capture/drain/idle/cooldown/exit/remember/recall各入口并生成可复现patch及摘要；完成blocked显式处置与游标解析参数，不能只接SessionStart；再接通用组与单次注入、启动器、桌面管理及真实PA场景。整体Goal仍active，未达到交付定义。
