# A1 配置账本切片验证

2026-11-09。本切片实现三份已保存候选、一个独立有效选择、不可变修订和验证账本。它尚未接入真实验证 runner、Cognee/P3 整项任务分派或 A 页面，因此 A1 总体验收和本轮 Goal 继续未完成。产品与执行边界见 [运行契约](memory-processing-runtime-contract.md)。

## 实现与检查

正式 schema16→17 仅新增处理配置账本。保存通过 daemon 单写事务；同模式新候选不修改当前选择。修订的更新/删除被 SQL 触发器拒绝，读时核对配置与摘要；操作 ID 绑定请求和原回执，重放不重新执行。保存的配置只含凭据 UUID/修订引用；后续已实现独立 Keychain 凭据版本基础，但供应商配置登记、保存事务和引用检查尚未接入服务，见 [在途记录](processing-validation-runtime.md)。

验证的尝试号、配置/依赖摘要、连接代次、取消和终态持久化。内部完成接口拒绝迟到成功；启用要求最新成功验证、当前候选与选择代次 CAS。冻结函数用于组合正式任务创建事务，目前尚无正式任务调用。恢复保留配置候选与历史选择，停用当前授权、提升代次并撤销旧验证；重新验证不会自动启用。

私有 IPC 仅开放读取和用户明确保存，未开放伪造成功/启用入口。SQL 成功回执是明确账本夹具，不证明认证、模型、Schema 或隔离通过。

全仓 test/vet、Store/daemon 受影响 race 及迁移/IPC 集成检查通过，日志位于 `build/ui-delivery-20261109-001/`：

- `go-test-processing-ledger-002.log`、`go-test-processing-api.log`：真实 SQLite/WAL、保存/当前分离、冻结、取消迟到、连接失效、恢复、私有 IPC。
- `go-race-processing-ledger.log`：受影响 Store/daemon race。
- `go-test-processing-all.log`、`go-vet-processing-all.log`：全仓检查。

旧 schema13/14 恢复夹具修正为删除后加表，再重标旧版本；保留真实旧记录与 WAL。没有用容错迁移掩盖首轮夹具错误。

## 完整包 daemon 与真实旧库迁移

普通格式新包为 `build/ui-delivery-20261109-001/a1-ledger-package/darwin-arm64/MemlaneLedger.app`，包含 UI、helper/CLI、daemon 与 LaunchAgent，无 automation/runtime-config 和模型。使用 `scripts/verify-processing-ledger-package.py` 在全新合成根验证，未打开 Native 窗口、注册系统服务、运行模型或修改日常配置。

`a1-ledger-package-runtime-001/report.json` 和 `002/report.json` 失败保留。数据库存在、服务已退出，但本机 Python3.9/SQLite3.51 的普通只读 WAL 查询无法初始化 sidecar；首轮验收驱动也错误假设已停止的 daemon 会继续发布 socket。修正驱动：先证明新包遇停止标记直接退出且数据库/标记不变，仅在合成根明确 `--resume` 后迁移；读取限制在服务完全退出且无未提交 WAL/journal 的不可变只读快照。没有对活动库使用 immutable，也没有改变生产停止规则。

`a1-ledger-package-runtime-003/report.json` 实际通过：

- 真实旧 schema16 包通过私有 IPC 创建合成原文 v1、编辑 v2；正常停止后保留主动停止意图。
- 新包在停止状态启动退出 0，不发布 socket，保留 schema16 全库与停止文件。
- 明确恢复后 schema17 迁移成功；49 张旧表全部保持，`VACUUM INTO` 备份与旧库全表一致，原文仍为 v2。
- Provider 候选仅使用 `provider.invalid` 与明确未验证名称，不调用模型；保存/重放/重启后仍保留候选，当前始终未启用。
- 再次停止、启动保护、明确恢复均符合规则。最终停止文件为 stopped，6 个自有 daemon 全部退出，socket 不存在，清理错误为 0。

实际新 daemon SHA256 为 `468b1b1241265ca0c90d50665c289ab8f003565b629d98eba6900c5a548376d9`。真实 WAL 未检查点迁移由 Go 集成验收覆盖；包验收读取干净停止库，不把这两种证据混写为同一个场景。

包测量见 `a1-ledger-package-size.json`。本轮没有组件下载或用户安装后的磁盘测量；本机 ad-hoc 包不代表签名分发。该包 App 使用独立 identifier，但随包 LaunchAgent 仍为默认服务 label；未注册它，独立普通 Native 包的服务身份需后续修正并验收。R01 普通准备入口也仍未通过。

## 剩余交付

Q01 旧 traex 计划授权、Q02 独立 Provider 条件、Q03 历史首日口径仍按 [本轮资料](ui-redesign/implementation-20261109.md) 等待必要决定。迁移未更换旧执行者、预算、启停、模型配置或统计口径。

下一切片接真实验证 runner 与凭据版本引用，随后将整项任务冻结纳入正式任务/租约事务和共享执行槽位；再接三步初始化、设置复用及电脑级批次。旧整理仍走 traex，不能用当前账本或 pi 桥通过宣布已统一切换。
