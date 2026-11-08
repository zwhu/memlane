# G2 代码变化与恢复验收契约

2026-11-08，G2 本机范围已验证，见 [验证记录](p4-code-update-validation.md)；F1 替换和正式分发另验。沿用 [正式代码契约](p4-code-contract.md) 的开启授权、独立 dataset、不可变 snapshot、generation 和 current 原子回执，G1 基线为 `6883cd5425c429b6c472163efd0918bf927eac65`。本切片不增加模型调用或扫描权限。

## G2a 实际变化与 App

使用已通过隔离门禁的合成项目；驱动明确写入该临时根的 `repo-a` 和未开启的 `repo-b`，不修改用户项目。保留 G1 产物，新报告和截图写 `build/p4-code/g2-*` 与 `build/p4-code-g2-screenshots/`。

在真实打包 App 启动服务，等待真实 worker ready，选中已有开启的工作区：

- 连续修改 TS/Python、增加 TS 源，自动得到同一最新快照，无需手动刷新。旧 current 来源变化时拒绝查询；新结果须有精确名称、路径和内容 SHA。
- TS 文件重命名、Python 源删除，新的当前图排除旧文件/符号/边；同一 worktree/dataset 保留，任务与来源历史保留。未开启 B 的变化不能产生 worktree 或图目录。
- 超过单文件上限的故障源明确失败，保留最后成功 receipt，查询不能展示过期旧图。无法捕获有界 digest 的来源故障要求用户明确重试；有已捕获 digest 的抽取失败在实际变化后自动重试，同一失败 digest 不循环重建。
- App 关闭后服务继续运行与显式停止服务后不扫描分别核对；显式停止后重开 App 仍不恢复，只有实际点击启动服务后扫描。重启后 generation/dataset/current 持久，恢复新源得到新 snapshot。
- 用户停用扫描时取消活动任务并增代，迟到结果不得改变 current；重新开启由用户决定，不隐式恢复权限。

正常静态流程使用真实 Enola/Rust，不用模型协议夹具。故障注入明确区分文件预算、进程终止、SQL 不可写和 UI 驱动。Agent 代码工具已有 G1 真实证据，新增影响才重跑对应模型。

## G2b 派生资源与组件

旧图是可重建派生数据，SQLite 来源 manifest、任务状态、授权动作与 receipt 历史必须保留。每个已登记 dataset 保留 current 和最近另一个成功 snapshot；已停用的工作区也保留该集合，以便重新开启。更早图目录可回收，不代表删除知识或来源历史。需要旧代码内容时以保存的来源版本另行重新生成，当前入口不把旧图显示为 current。

服务串行代码循环在一次发布结束后清理，取得 worker gate 及授权 mutex，确保没有 code build/query 使用图目录；从数据库重新读取保留集合，读失败就不清理。清理仅触及 UUID 命名的已登记 dataset 中非保留 UUID snapshot 与 `.building-<UUID>`，不移除未知目录，不接受客户端路径。使用 Go `os.Root` 限定在实际 worker DataDir 内；打开 code/dataset 前拒绝符号链接，再核对打开目录与原 entry 的设备/inode 身份，阻止范围内相对链接把 A 的保留集合应用到 B。不是新 worker 协议，原文和现有 code handshake 保持兼容。清理失败通过 health.code_cleanup_error 记录，不将已成功任务改成失败。

取消或超时的 code build/query 在释放服务 gate 前关闭并回收整个 worker，避免取消 future 后原生任务继续占用图；普通参数错误不关闭 worker。受控回收单独标记，不消费 supervisor 的异常崩溃预算；正常取消不能使服务进入三次崩溃熔断。重启恢复旧 job 后执行同样清理规则，回收未发布 staging；不会持 SQLite 写事务进行目录遍历。

抽取私有目录只按 SQLite 中 completed/failed/cancelled/interrupted 的规范 UUID job ID 回收；串行 owner 在当前操作结束或启动恢复后读取终态。unknown 目录与 active job 保留，查询失败不回收。打开管理根下 code-extraction 时拒绝链接并核对固定 Root 的目录身份，删除不逃逸根。旧 owner 消失后 helper FD guard 结束其整组；真实崩溃验收须验证旧 guard/worker 进程组退出、旧 job 中断、新 job 拥有权及残留清理，不能用正常取消替代。

组件验收覆盖已固定版本的新 worker、解析器和旧原文能力；不支持代码能力的旧 worker 明确拒绝代码入口，不能偷偷选择新协议。受控签名源的升级/回退/下载字节仅是本机组件证据；Developer ID、公证、正式更新源和干净系统仍属于 F，缺失时总目标未完成。
