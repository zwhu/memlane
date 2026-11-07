# P3-U 使用记录与透明排序验证

2026-11-08，U本机主流程已通过，最终交付检查见 `build/p3-usage/final-checks.json`；本切片不结束总Goal。契约见 [使用契约](p3-usage-contract.md)，下一步为I/A/R/G/F。

## 真实 App / pi / Rust

最终独立根 `memlane-p3-usage-app-jwuie3_l`，bundle `io.github.zwhu.memlane.usage.170c8029f25e`，验收包 `build/p3-usage-app-r2/darwin-arm64/MemlaneUsage2.app`。MyGo0.7.4、pi0.84.1、pi自己认证和选择的deepseek/deepseek-v4-pro；真实固定Rust/E5组件。仅复制已有合成组件缓存，UI/Agent配置与两项目都隔离，没有复制真实记忆、聊天或登录凭据。

| 验收 | 真实结果 | 证据 |
| --- | --- | --- |
| App新增/浏览 | App登记两项目并保存两条知识；展示当前正文后记浏览，4.5秒自动轮询不增加次数 | `build/p3-usage/real-app.json` |
| 项目登记页 | 实际点击登记页左栏知识时，右栏仍是登记表，没有记成正文浏览 | 同一驱动断言；不是视觉夹具 |
| 真实读取/返回 | pi通过自己Extension读取当前v1、检索Rust当前正文；来源与版本保留 | `pi-real-usage.json` |
| 明确报告采用 | pi拿实际get的usage_receipt.id提交memory_adopt；同稳定操作调用两次，回执一致且只生成一个事件 | real-app.json、pi-real-usage.json |
| 范围/工作区 | 未授权范围拒绝；同一未绑定连接在A读全局、B用旧exposure报告采用拒绝为exposure_required | real-app.json；这部分是无模型服务协议检查 |
| 通道分开 | 连接验证读取单列；常用只按日常Agent read，不把返回/采用、App或验证混入 | real-app.json与实际窗口 |
| 常用/重要 | App常用将真实被读取条目置前；用户标记另一条重要，重要优先显示它，正文与revision未修改 | real-app.json |
| 重启 | App停服关闭再打开，明确启动服务；采集起点、日常计数与重要标记保持 | real-app.json |
| App搜索 | 另一次实际App/Rust搜索只增加目标的App return一次；daily及verification保持 | `app-search.json`、`app-search-run.log` |
| 实际视觉 | 已实际查看设计两图及使用前/后、常用、重要、重启和App搜索六张系统窗口截图；保持三栏、浅蓝侧栏与正文/来源/状态分区 | `build/p3-usage-screenshots/usage-*-system.png` |

正文读取、检索与采用的模型主流程使用真实pi，没有模型协议替身。采用只是Agent明确报告，没有独立证明代码提交或实际采用。后台整理次数未采集，界面和DTO标明unknown/background_collected=false，冻结来源仍在整理记录；不从整理任务数量补造使用次数。旧Agent未报告的采用及采集起点之前的历史未知。

首轮驱动等不存在于语义树的layout key usage-detail而超时，未调用模型；失败根与包保留，证据在 `attempt1/`。修正为真实正文与“使用记录”文本后新根r2通过，没有放宽计数或排序断言。

## 数据、故障与兼容

真实SQLite集成覆盖读取/返回/采用去重、回执重放与参数冲突、匹配连接/代次/工作区/范围/具体版本、取消连接、移动后不搬使用次数、90天明细清理后旧ID不再计数、过期exposure拒绝及SQL事务回滚。明确SQL、状态、版本、时钟和协议夹具只证明这些边界，不能替代上表模型与App正常结果。

schema9临时旧库按真实旧DDL构建，包含C1冻结任务、非空D2b等待恢复策略与已释放lease；持有已提交WAL读取事务时一致性备份，旧v9备份integrity_check=ok，全部旧表与当前库逐项相同。升级仅新增使用记录/用户重要标记及实际覆盖起点，旧历史不回填计数，原恢复策略waiting/used0/limit1保持。见 `migration-v9-final.log`。实际D2b schema9 helper在停服合成schema10副本上拒绝，错误为 `database schema is newer than this client`，见 `stopped-audit-compat.json`；不代表正式回退或索引重建验收。

显式memory_get保留P2的归档/删除状态查看能力，记录真实成功读取并保留状态；这些回执不能采用为有效知识。所有新使用事件仍在事务内检查当前scope/revision与失效派生来源，避免Current之后源变化、摘要revision没变时误记成功。memory_status不计读取。实际SQLite服务契约与源变化间隙夹具见 `flagged-compat.log`、`usage-source-gate-final.log`；普通active App/pi路径没有改变。

MCP、CLI与pi正式schema新增memory_adopt，旧protocol仍为1。此次新增采用工具的模型验收为pi0.84.1；Codex原P2模型闭环的历史通过范围保持，新增采用工具的Codex模型验收仍须在Agent适配阶段核对，不能用静态schema表示已实测。Codex验证通道通过显式argv传给MCP bridge，避免该固定版本过滤环境变量；pi同样显式传桥argv。

全仓测试、vet与四包race通过；最后读取兼容/来源门禁增量又执行受影响检查，最终路径见final-checks.json。分组、跨组和脚本补审当前无残留；原工作区、Codex环境过滤、范围混入、浏览/错误归属及后续stale门禁问题均保留resolved历史，见 `review/`。普通写入的历史accepted回执不挂当前使用统计栅栏，保留其幂等语义。

## 本机包与交付边界

完整最新UI/helper/LaunchAgent/Agent桥和DMG在 `build/p3-usage-production/`，严格深度codesign通过；仍为ad-hoc本机验证包。用户并行图标/design/roadmap/builder变动保留并排除于提交，它们影响本机包外观。体积、提交和远端回执分别为 `sizes.json`、`delivery.json`，最终临时handoff由脚本按格式生成，未自动调用禁止模型调用的源Skill，不入库。

Developer ID、公证、正式更新源、Gatekeeper与干净macOS15+ arm64仍未通过。后续I引导与初始化、A/R其他日常Agent与pi RPC、G代码图谱、F分发/故障继续，不能标整体完成。

| 体积口径 | 字节 |
| --- | ---: |
| 主DMG | 15197867 |
| App普通文件逻辑大小 | 34135647 |
| 首次额外Rust/E5归档 | 285752286 |
| App+实际准备最终根逻辑占用 | 526158277 |
| 更新下载量 | 未验证 |

组件归档未变化，沿用已有成功下载量；不重复下载，不计失败根或开发工具链。APFS分配不等于可释放空间。
