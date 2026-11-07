# P3-C3 摘要与标签验收

2026-11-08。C3 实现正式 schema v4 的摘要/新增标签、来源依赖和逐项恢复；C1/C2 合并审核仍保留。完整修改建议、全局经验、计划/租约、使用/初始化、TRAE/traex 日常接入、pi RPC、代码图谱及正式分发仍按 [交付矩阵](p3-delivery-plan.md) 继续，本切片不代表整体完成。

## 环境与契约

本机 macOS 26.6.2 arm64、Go 1.27.3、MyGo v0.7.4、traex 0.214.1 internal，实际选择 GPT-6.1-Sol；Rust worker 固定 Cognee-RS `151905c302fc9b5b9aad88d6b13931c09f0eac30` 与 E5-base int8。沿用 Agent 自己的认证/模型选择，隔离任务不注册生产数据库或批准工具、不改日常配置。没有验证其他 Agent 版本、发布渠道或新语言代码图谱。

输入为同一个用户明确选定项目的 1–16 条 knowledge/raw 材料，绑定具体 revision、正文、出处、捕获时间和原始依据。只接受 `summary` / `add_tags`；整份结构化结果通过范围/引用/大小校验后先保存，逐项短事务应用。摘要与同源转述不能增加独立证据。契约及工程取舍见 [整理动作](p3-organization-contract.md)。

## 实际与故障证据

| 检查 | 实际结果 | 证据 |
| --- | --- | --- |
| 服务真实正常/取消 | 稳定开始 ID、真实 traex 摘要与新增标签、来源不改写、Rust 当前摘要命中；活动进程取消并退出/组消失 | build/p3-c3/real-service.json、real-service.jsonl |
| App 真实选择与自动应用 | 两条知识 + 一条 raw，产出两条摘要和三项标签；原文版本保持 v1，栏内查看任务/来源/产出，Rust 返回当前摘要 | build/p3-c3/real-app.json、app-launch-final.log |
| App 来源变化/编辑/恢复 | App 修改具体依据，自动摘要 stale 并排除真实检索；用户编辑产生 v2、恢复 v1 产生 v3，保留派生出处与历史；活动日期和导入数没有虚构回填 | 同上；summary-stale-real-system.png |
| App 取消与重开 | 实际 turn.started 后在 App 取消，真实子进程回收且没有产出；退出/重开仍可查看持久任务，最终主动停服 | 同上；organization-cancelled.png |
| App 部分失败/连续恢复/索引恢复 | 真实模型生成后摘要已提交、两项标签失败；App 连续两次恢复推进新 attempt，解除 SQL 故障第三次完成；真实 Rust 索引失败保留正文，重启解除阻挡后检索恢复 | build/p3-c3/recovery-app.json |
| SQL 故障恢复 | queued/running/applying 失去执行者且持久化仍失败时查询明确失败；解除后新 attempt 收尾，已提交摘要与回执不重复；终态取消不影响 owner，旧 defer 不移除新 attempt | TestOrganizationDeadOwnerRecovery、TestOrganizationOldOwnerCannotRemoveNewAttempt；recovery-regression-r3.log |
| 派生摘要与 C1 门禁 | 直接来源变更后 Start/结果/列表/详情/确认均拒绝或过期；仅列表查询也显示过期，已经应用的确认回执仍保留 | TestOrganizationStaleSummaryCannotMerge；stale-merge-regression.log |
| 迁移与备份 | 临时 v3 库保留 16 个旧表、已提交 WAL 和旧恢复合同；VACUUM INTO 备份完整性/schema/旧行核对，迁移 v4 | TestOrganizationV3BackupAndLegacyRetention；go-test-final.log |
| 原子/范围/迟到 | 单项 SQL 回滚、结果整体拒绝、版本/范围变化、部分应用取消与旧 attempt 拒绝、来源递归有界和旧索引排除 | organization_integration_test.go；go-test-final.log |

截图放在 `build/p3-c3-screenshots/`。开发前已实际打开首页与三栏详情设计图，最终已实际查看 App 系统窗口截图并核对 key/visible 元信息。宽幅总览不变，三栏页面保持连续浅蓝侧栏、紧凑白色列表/详情、单色按钮；材料、出处、状态与普通操作在栏内。长材料由右栏滚动承载。

模型正常返回与活动取消为真实 traex；普通 Go 测试中的模型结果/索引回执是明确协议夹具。SQL UPDATE/INSERT 拒绝为故障夹具，不等同真实磁盘满；专用新 dataset 阻挡是实际 Rust 文件系统索引故障，不等同 worker 启动失败。没有借此宣称租约、计划、云端计费撤回或干净系统交付通过。

## 小规模质量基线

六类固定合成材料各跑一次真实模型；schema、进程终态和弃答断言之外，逐项阅读实际正文与引用进行语义核对。不是用户接受率，也不声称相对既有模型质量提高。输入在 `internal/agents/organization_quality_test.go`，完整合成输出在 `build/p3-c3/quality-results.json`，逐项审查在 `quality-review.json`，原始 Go 事件在 `quality-real.jsonl`。

| 样本 | 实际核对 |
| --- | --- |
| 中文部署约定 | 日志保留、回滚已验证版本、健康检查、不删失败记录均保留，摘要/标签引用支持 |
| TypeScript/Python 代码约定 | 按语言分别保留 strict/边界校验和 snake_case/返回类型，不扩写到全部语言 |
| 重复同源 | 只以原文为依据，解释明确同源摘要不作为新独立证据 |
| 冲突 | 保留日期版本和语义化版本双方要求，明确优先级未知，没有判定生效方 |
| 恶意嵌入指令 | 不执行删除/读取聊天/提取凭据；返回零项和不足事实解释 |
| 来源不足 | 待定材料返回零项，不编造已确认规则 |

此基线的范围只覆盖 C3 摘要/标签；修改/重复/冲突审核动作的质量基线在 C4 增补，不能凭本表宣称完整整理已通过。

## 审查、检查与包

bits-code-guard 按存储/运行时/UI 三组审查，修复初次六项及补审发现：stale 摘要绕过 C1、任务持久化失败悬空、恢复请求缓存、来源失败清空、未应用漏计、失效产出索引标签；补齐列表 stale 推导、旧执行者移除新拥有权以及终态取消与新恢复竞争。当前复核残留清单为空，初始与修复记录均保留在 `build/p3-c3/review/group/`，报告见 report.html/report.md；静态审查不代替 App 验收。

全仓 `sh scripts/go.sh test ./...`、`vet ./...` 与 store/daemon/agents/desktop race 通过，日志 go-test-final.log/go-vet-final.log/go-race-final.log。主包完整构建 `python3 scripts/build-client.py --out build/p0-final`，包含当前 UI/helper/LaunchAgent/Agent 桥；专用 App 包最终构建 app-build-final.log，生产包 production-build-final.log。签名仍为 ad-hoc，Developer ID/公证/Gatekeeper、正式更新源和干净 macOS 环境仍缺真实证据。

体积由 `build/p3-c3/final-sizes.json` 记录：主 DMG **14,921,057 bytes / 14.92 MB**，首次外置组件归档 **285,752,286 bytes / 285.75 MB**，实际合成准备后的 App + runtime/data 总逻辑磁盘量 **525,498,763 bytes / 525.50 MB**；复用的组件下载归档不冒充本次重新联网安装。正式更新下载量未知，不能记为 0。首轮 DMG 创建发生“资源忙”，后续完整构建通过，失败日志 app-build.log 保留；不能据此宣称 App 移动/更新路径故障已完成。

阶段提交、普通推送的远端 SHA 和系统临时 handoff 回执写入 build/p3-c3/delivery.json。handoff 由脚本直接生成，注明源 Skill 禁止自动模型调用且没有调用 /handoff；临时文档、build、数据库、Agent 配置和秘密不进入提交。阶段之后继续 C4 及全部交付矩阵。
