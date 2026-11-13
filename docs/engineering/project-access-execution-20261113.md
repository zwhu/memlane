# 项目与权限实施记录

2026-11-13，owner session `01a1d30a-d776-7f92-a7e9-9424e5fabc13`。用户附件明确启动 PA01–PA14，覆盖旧“无权限／共用 owner”的相应方向；历史全量 Goal 保持暂停。dev 模式，不做 release。

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
