# P3-E 全局经验验证

2026-11-08，本机 macOS arm64。E 已通过；完整 P3 Goal 仍继续计划、使用/引导、日常 Agent、图谱和正式分发。契约见 [全局经验契约](p3-global-contract.md)，总矩阵见 [交付计划](p3-delivery-plan.md)。

## 真实主流程

新建 `memlane-p3-global-app-*` 合成根，仅复用已验证组件，不复制业务库、聊天或日常 Agent 配置。独立 App 标识 `io.github.zwhu.memlane.global.a10ae1a760f6`。主流程使用完整 MyGo 验收包、真实 traex 0.214.1 和 Rust worker/E5-base int8；没有正常模型协议夹具。

| 验收 | 实际结果 | 证据 |
| --- | --- | --- |
| App 原文复制 | 预览前后全局数量不变；确认新建全局 v1；项目原文与来源保留；Rust 返回副本 v1 | `build/p3-global/real-app.json` copy |
| AI 通用提炼 | 稳定任务 ID；条件、正文与冻结引用可预览；确认前不新增全局知识，确认后真实 Rust 命中 | 同文件 distill |
| 决定重放 | 同 receipt/digest 返回原事实，不产生新 revision | 主驱动与 Store 集成检查 |
| 条件编辑与恢复 | App 编辑产生 v2；恢复 v1 产生 v3，正文和当时条件恢复，原确认回执仍 v1 | real-app.json、`presentation.json` |
| 拒绝 | App 拒绝后全局知识数与原项目知识不变 | real-app.json reject |
| 过期 | App 编辑源知识为 v2；原建议过期，不再可确认，窗口呈现过期状态 | real-app.json expire |
| 整理候选 | 在通用整理中显式开启全局候选；实际模型候选待审核；App 拒绝不提升 | real-app.json organization_candidate |
| 活动取消 | 实际 traex 进程活动时 App 取消，结果不提交，进程已回收且组消失 | real-app.json active_cancel |
| 重启与出处变化 | 重开/明确启动后 v3 条件、原回执与 Rust 检索保留；源当前 v2、冻结 v1，显示变化提示 | presentation.json |
| 真实日常 pi | pi 0.84.1 Extension 从其他项目显式读取/检索全局 v3，返回条件；指定源项目 ID 在 global scope 下返回 scope_denied | `build/p3-global/pi-real-read.json` |

pi 验收由真实 `tool_execution_start` 的 `toolCallId/toolName/args` 与 end 回执关联，拒绝断言精确绑定来源 ID 和全局范围，不能用任意失败代替。接入配置仅写本轮合成 `pi-agent` 目录，认证和模型选择由 pi 自己持有；断开后合成的其他设置字段保留。原失败字段层级和无参数关联报告分别保留在 `pi-real-read-initial.json`、`pi-real-read-unbound-arguments.json`，最终报告才具有上述通过范围。

App 查看的是受信冻结出处；日常全局工具回执只携带已复制或确认的全局正文、条件与版本来源标识，没有未提升的项目 SourceDetail 片段。P2 原有明确授权的全局新事实保存仍保持可用。

## 质量、故障与代码检查

真实 v3 六样本包含中文部署约定、TypeScript/Python 代码约定、同源重复、版本冲突、恶意嵌入指令和来源不足。逐项语义核对在 `build/p3-global/quality-review.json`：部署条件限定具备回滚能力，语言条件分别保留；同源 pnpm 摘要没有变成独立证据或通用要求；冲突只登记，恶意/不足来源返回空建议。样本是小规模质量基线，不代表任意输入都可靠。

`store-integration.log` 通过真实 SQLite 的复制/确认/拒绝/过期/取消、源范围/操作 ID 冲突、删除后复制重放不恢复、条件历史/新版本恢复。SQL 触发器故障只用于验证全局元数据写入失败后的整项回滚和同请求重试。底层原文变化使直接引用的自动摘要 stale 时，已确认全局事实保留并显示来源变化。

schema 5→6 的临时旧库保留完整旧表、决定和非空提交 WAL；唯一拥有者升级前的一致性备份仍为 schema 5，integrity_check 为 ok，旧表逐项相同；升级后旧确认回执可重放。不会重建 v5 固定动作审核表。

`ui-regressions.log` 是 GUI/IPC 故障夹具：已提交副本读取失败、源 revision 后变仍可重试回执；复制晚回包不覆盖用户新进入的编辑草稿。它们不替代上面的真实 App/Agent 主流程。

全量 `sh scripts/go.sh test ./...`、`vet ./...` 和 Store/daemon/agents/desktop 的 CGO race 通过，日志为 `test.log`、`vet.log`、`race.log`。Bits Code Guard 三组与跨组审查已修复来源 stale 提示、复制回执/导航和 pi 参数关联问题，最终剩余确定缺陷为零；产物位于 `build/p3-global/review/`。

## 实际窗口与安装包

开发前实际查看首页与三栏图稿；完成后实际打开系统窗口截图，对照三栏、连续浅蓝侧栏、单色按钮与正文层次。`build/p3-global-screenshots/` 包含复制预览/当前、提炼待审核/当前、条件恢复、过期、候选与最终全局页。全局产出当前版本导航已改为全局页；最终窗口见 `final-global-page-system.png` 与实际窗口采集元信息。

完整本机生产包：`build/p3-global-production/darwin-arm64/Memlane.app` 与 `memlane-0.0.1-arm64.dmg`，包含 helper、LaunchAgent 和 Agent 桥，不附带模型与 Cognee。SHA256SUMS 及深度严格 codesign 验证通过；当前仍为 ad-hoc，不能代表 Developer ID、公证、Gatekeeper 或正式渠道通过。用户并行图标资源影响本机包，但图标/design/roadmap 等独立改动不纳入 E 提交。

| 体积口径 | 字节 |
| --- | ---: |
| 主下载 DMG | 15,083,533 |
| App 普通文件逻辑大小 | 33,882,495 |
| 首次额外 Rust/模型归档 | 285,752,286 |
| App + 实际准备合成根逻辑占用 | 525,840,576 |
| 更新下载量 | 未验证 |

`build/p3-global/sizes.json` 列明组件、数据及 OS 分配文件字节；不包含网络失败重试/TLS 开销，APFS 分配不等于删除可释放空间。

## 剩余与阶段交付

E 是已通过切片。持久计划/租约/重试、使用排序/引导初始化、TRAE/traex 日常接入、pi RPC 后台、Rust 代码图谱和 F 分发故障继续完成。Developer ID、公证、正式源与干净 macOS 验收外部条件仍缺，不标总 Goal complete。App 原地重建偶发启动失败仍作为 F1 未闭环事项保留。

阶段 commit、远端 SHA、检查和临时 handoff 回执分别由 `build/p3-global/delivery.json`、`final-checks.json` 记录。handoff 由脚本生成并明确未调用禁止自动模型调用的源 Skill；不入库，不复制凭据或模型输入。
