# macOS 首发更新机制建议

2026-11-19。状态：官方固定源码核对与建议方案，尚未获产品方案确认或实施授权；没有构建、安装、升级、服务启停、模型调用或数据迁移。当前仍为 dev，不恢复旧 helper 更新实验或历史全量 Goal。

## 建议结论

首发复用固定 MyGo v0.7.4 的底层 `mygo.Updater`，由 Memlane 提供原生更新提示和安装前协调。先提供一个 stable 通道和完整 App 更新包，使用公开 GitHub Release 或 HTTPS 静态目录，不建设更新服务器，不要求另引入 Sparkle。App、运行适配代码、Cognee／原生依赖、向量模型、Agent 插件分别固定版本，但由一个已验证的发行版本组合决定是否可以一起使用。

推荐自动检查、明确安装；首版不开启静默安装。新版本存在不代表必须升级 Cognee、模型或所有 Agent 插件，普通启动不跟随上游 latest。

## 已核对的 MyGo 能力及限制

- `mygo.Updater` 支持从 GitHub 或 HTTPS 更新源检查，更新公钥内嵌于 App，发布归档由独立更新私钥签名。更新签名与 Apple Developer ID 签名、公证是两套需要分别完成的事项。[固定更新文档](https://github.com/egoist/mygo/blob/v0.7.4/docs/updates.md)、[分发文档](https://github.com/egoist/mygo/blob/v0.7.4/docs/distribution.md)。
- 官方 updater 插件提供检查、说明、进度、跳过和稍后提醒，也提供 Native UI 版本；但默认功能包含自动安装选择，并不掌握 Memlane 后台任务或资源引用。[固定插件文档](https://github.com/egoist/mygo/blob/v0.7.4/docs/plugins/updater.md)。
- macOS `Update.Install` 在下载／校验后调用 `stageBundle`，启动独立 shell helper 等待 GUI PID 退出后替换 `.app`。`Install` 返回成功不是已经替换或新版本健康的回执。[固定源码](https://github.com/egoist/mygo/blob/v0.7.4/updater.go)。
- `Check` 读取的 feed JSON 没有整体签名；当前 Ed25519 校验针对压缩包摘要，不覆盖说明、日期或全部版本字段。SDK 也不在 Install 前提供新包资源解包预览。“仅界面更新”与组件兼容必须由额外签名且绑定目标归档摘要的兼容清单判定，不能直接信任普通 feed 字段；这份清单的发布和验证仍待实施。
- helper 等待的是 GUI，不是 Memlane owner、Cognee、模型桥或 Agent 消费者；正常退出、崩溃或强杀都可能触发替换。SDK updater Options 及底层 Install 没有业务 `BeforeInstall` 门禁；`OnBeforeQuit` 不能拦截崩溃或撤销已经安排的替换。因此业务检查必须在调用 Install 之前完成。
- helper 只在新包移动失败时尝试恢复旧包，没有后台／数据健康检查；恢复移动的成功也不等同业务恢复。不能把 SDK 文档里的失败保护解释为 Memlane 数据和全套运行时的自动回滚。

## Memlane 当前可复用的基础

- [runtime_assets.go](../runtime_assets.go) 内嵌小型适配代码和固定清单，Python、Cognee 原生依赖及 embedding 权重仍在 App 外。
- [resources.go](../internal/bootstrap/resources.go) 根据全部运行资源摘要生成 `runtime-<摘要>` 私有目录，逐文件检查原内容，不就地覆盖不同版本。这是升级时同时保留旧运行资源与候选资源的基础，不代表更新事务已经实现。
- [first_runtime.go](../internal/bootstrap/first_runtime.go) 从明确资源目录启动后台，保存官方库路径与身份；当前仍复用资源目录中的开发启动器，正式更新协调未接入。
- [组件清单](../engine/coding-memory-components.json) 固定官方服务和插件输入；[安装器](../internal/bootstrap/installer.go)已有绑定具体计划摘要的确认及恢复机制。
- [插件准备](../engine/python/project_installation.py)生成独立安装产物，宿主的启用与信任仍按现有流程处理；新 App 不自动证明已有 Agent 已切换插件。

## 用户流程

设置中的更新入口或 App 菜单提供“检查更新”。发现版本后显示版本号、变更说明、下载量，以及是否需要重启记忆服务、更新插件或迁移存储。用户可选择更新或稍后。

| 更新内容 | 执行原则 |
| --- | --- |
| 仅界面／App，后台资源摘要相同且兼容 | 确认后台和 Agent 无旧 `.app` 资源引用后替换 GUI，继续复用原后台和组件；不重复下载引擎或模型 |
| 运行适配代码／Cognee／原生依赖 | 先准备候选版本，进入维护阶段，停止新的处理提交，等待原任务收尾，再按实际身份停止自有后台和核对独占锁 |
| Agent 插件 | 计划列出受影响宿主；沿原 prepare／activate／review 更新，保留登录、无关插件、原消息和权限，实际宿主加载与信任单独显示 |
| 向量模型／索引格式 | 作为单独明确操作，不附带在普通 App 更新中；先说明兼容性、下载及索引影响 |

首版不因任务慢就强杀任务。用户可以继续等待或稍后更新；未知操作沿原编号核对，不重新提交收费模型任务。维护期间的采集暂停与本地缓冲需沿现有编号保存机制验证，不能无证据承诺绝不丢消息或无感更新。

## 升级执行与恢复

1. 检查发行版本与固定兼容组合。外部组件的清单必须来自可信的签名发行输入，不能只用同一未认证下载源提供的 SHA 来证明可信。
2. 将变更的外部组件准备到新版本目录，沿现有计划确认、下载校验、取消与归属保护执行；没有变化的组件复用。首版 App 完整包下载仍由 MyGo Install 执行，SDK 没有暴露独立的“仅下载、随后提交替换”事务接口。
3. **在调用 MyGo Install 前**完成运行依赖许可。涉及后台变更时，先阻止新增处理任务、确认已接纳请求与写入收尾，再停止所属服务并确认同库锁释放；未知事实保留，不能通过杀 GUI 来强制推进。
4. 在 App 外持久保存一个小型更新操作记录：旧版本组合、候选组合、官方库身份／路径、阶段、原操作编号和恢复入口。保存配置恢复点；涉及官方存储格式变化时，先形成一致备份。备份仅供回滚，不启动第二运行库。
5. 调用 MyGo Install，显示“更新已准备，重启应用”。下载或校验失败时恢复被暂停的原运行，不声称已安装。已安排替换后不再提供含义不明的“取消安装”。
6. 新 App 读取原操作记录，仍使用同一官方库。核对实际安装版本、原项目／主体／权限／配置，以及候选后台与内容读取；通过后才写完成回执并恢复任务接纳。首次启动不以空库代替失败。
7. 候选运行失败且存储格式保持兼容时，恢复旧运行组件组合；官方存储已经迁移则必须按配套组件和一致备份恢复，不能单独降级可执行文件。自动恢复范围需由具体实现和验收证明。

首版不承诺跨 App、所有 Agent 和数据库的完整自动回滚。App 恢复保留上一个已签名版本的获取／人工安装入口；运行组件按兼容条件恢复。清理之前核对 owner、启动路径、服务注册、Agent 配置和组件引用；SDK 替换 `.app` 不代替这些检查。旧版本资源只保留必要的一个恢复组合及仍有消费者引用的集合，不无限积累完整环境。

## 版本与首发验收

发行输入至少固定 App build、运行资源摘要、官方服务版本、插件产物摘要、支持的 API／存储格式和最低 macOS／架构要求。Python／原生库／模型只在组合变化时更新，不让用户手动挑选互相未经验证的组合。

进入 release 后至少验证：正常更新；下载中断及坏签名保持原版本；正在处理时等待／稍后；GUI 意外退出及整机重启后的原记录恢复；候选后台失败；同库内容和主体／权限／配置保持；插件需重新审阅；旧资源仍被引用时不清理。App 内检查应避免为升级验证另发模型调用；最终端到端验收按明确范围执行。

本建议未改变产品／设计基线，未启用 MyGo updater、生成签名密钥或发布任何制品。旧运行包误清理与失败计数继续见[事故记录](engineering/runtime-package-cleanup-incident-20261111.md)，不以本报告恢复该已停止实验。

## 核对来源补充

- [MyGo updater.go](https://github.com/egoist/mygo/blob/v0.7.4/updater.go#L187)：187–205为feed解析；219–319为安装；381–436为macOS暂存及GUI退出后替换。
- [签名实现](https://github.com/egoist/mygo/blob/v0.7.4/internal/update/update.go#L99)：Ed25519更新签名；[插件 Options](https://github.com/egoist/mygo/blob/v0.7.4/plugins/updater/updater.go#L50)无业务安装前回调；[Relaunch](https://github.com/egoist/mygo/blob/v0.7.4/login.go#L70)尊重正常退出拦截。
- [Sparkle官方文档](https://sparkle-project.org/documentation/)及[Updater delegate](https://sparkle-project.org/documentation/api-reference/Protocols/SPUUpdaterDelegate.html)提供EdDSA、安装及延迟重启机制，但这些回调同样不能证明Memlane数据／后台健康自动回滚。首发优先复用现有MyGo，避免另接框架；此次没有核对Sparkle installer源码，也没有实际更新验收。
