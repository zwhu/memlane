# P3-I 指引与初始化验证

2026-11-08。正式契约见 [初始化契约](p3-onboarding-contract.md)。本切片的服务、App、真实 pi/Rust 主流程、索引故障与迁移分别验证；总 Goal 的 A/R/G/F 仍未完成，正式签名公证/发布源/干净 macOS 外部条件仍缺失。

## 实现与已通过范围

首次打开直接进入宽幅首页；设置指引可跳过并从设置恢复，组件、已登记项目和 Agent 状态从实际接口更新。初始化是可选栏内流程，App 不自动调用初始化模型。用户选择项目、已验证连接、工作目录和 1–16 个具体文件，准备冻结 SHA256/原文快照后复制 prompt；复制只登记待提交批次。

schema11 在 v10 一致性 WAL 备份后仅增加实际 initialization_batches；新增版本、具体引用、操作、索引任务、导入活动与批次回执共用短原子事务。读取文件与模型都在 SQLite 写事务外。准备与提交并发重放在短事务里核对 operation/digest，慢文件读取失败也不覆盖已存在的并发成功回执。受控文件读取使用规范仓库根与 descriptor-relative openat/NOFOLLOW/CLOEXEC，拒绝仓库外链接、隐藏配置、数据库及聊天材料。

真实主流程证据在 `build/p3-onboarding/real-app.json`、`pi-real-initialization.json` 与 `pi-real-cancel.json`：App 内连接并真实验证 pi 0.84.1，选择 README.md/conventions.md 后仍显示等待提交，真实 pi 使用自己认证和 deepseek/deepseek-v4-pro，仅来源查询/状态/结构化初始化工具保存两条项目约定。具体路径/SHA/quote 被服务校验，保存 origin=imported，Rust 返回 v1。App 可从批次打开当前知识。导入量单列；同源转述不能成为新独立证据。取消在实际来源读取后由 App 发起，真实 Agent 后续状态为 cancelled，保存0条，无普通保存绕过。

App 重启保留已保存回执、取消与过期批次；连接断开保留其他 pi 配置。最终系统截图在 `build/p3-onboarding-screenshots/`，已实际查看首页、指引、等待提交、已保存、当前知识、取消、过期和重启状态；维持连续浅蓝侧边栏、宽幅首页与三栏详情。截图发现的旧等待提示在终态清除；导入 v1 标为“导入”，不将旧库 imported 记录误称初始化。

## 明确故障与协议夹具

| 检查 | 证据与边界 |
| --- | --- |
| 源变化后回执重放 | 无模型协议请求；已收到批次返回原回执，保存出处保持导入时版本 |
| 源变化时未提交批次 | 有效提交触发 source_changed，App 显示材料过期，0条导入 |
| 新批次重复稳定ID、错误quote、其他scope | 无模型协议夹具；整批拒绝，错误分别 initialization_item_exists/source_quote_mismatch/scope_denied |
| workspace/连接代次/撤销/模式 | daemon真实SQLite服务集成检查；初始化模式不能普通保存，其他连接/工作区及撤销拒绝 |
| Rust索引写失败及恢复 | `index-fault-app.json`；新合成project的Rust dataset路径用普通文件阻断。提交是明确无模型协议夹具，实际worker写失败；App显示保存保留/索引失败，移除夹具并重启后Rust返回同一v1和回执 |
| v10非空WAL迁移与备份 | `store-replay-all-final.log`；全部旧表、U事件/重要/曝光/采用、计划/lease与回执保留 |
| 并发准备/提交与文件删除 | 真实Git/FS/SQLite屏障夹具；相同批次返回同ID和冻结Sources，提交回执重放不重复活动 |

第一轮驱动未传入连接身份，在实际初始化模型调用前失败，保留 `build/p3-onboarding/attempt1/`。第二轮主流程与故障已通过，保留 `accepted-r2/`；随后因实际截图文案修正重建r3，正常/真实取消/故障再次通过；最新通用导入标签、历史与导入3/新增0的实际窗口另见 final-display.json，已实际看图。夹具不能替代真实正常或取消模型。

## 检查、审查与包

全仓 `sh scripts/go.sh test ./...`、`vet ./...`，以及 store/daemon/agents/desktop 的 race 已通过；最后展示修正重跑受影响 desktop 检查。三个分组只读审查覆盖服务、源/版本/范围、并发/幂等/取消/迁移、工具模式与 UI；发现和修复保留 `build/p3-onboarding/review/`，最终 active 数与文件覆盖见 final-checks.json。

完整最新生产验证包包含 UI/helper/LaunchAgent/Agent桥，位于 `build/p3-onboarding-production-sealed/`。主包不含引擎/模型；体积与签名日志在 `sizes.json`、`codesign-final.log`。主DMG 15,254,336 字节，App普通文件34,254,271字节；首次额外组件下载沿用未改变的 Rust/E5 归档 285,752,286 字节，并未为本次重复下载。最终App加准备根526,292,909字节；APFS分配和可释放空间不同，更新下载字节未知。正式 Developer ID、公证、Gatekeeper、渠道可达/更新下载字节和干净 macOS 15+ arm64 均仍未通过。

pi 初始化模型仅标记已实际验收的0.84.1；Codex模式argv已修正并静态/协议验证，新的初始化/采用模型验收随 A/R继续。TRAE/traex日常与 pi RPC 不从本切片推断支持。stage交付回执记录普通提交/推送的精确SHA及系统临时handoff，生成后继续剩余切片。
