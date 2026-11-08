# G2 更新、取消与派生资源验证

2026-11-08，G2 实际 App 更新、真实 Rust 保留回收与三轮活动取消、根目录混合语言、组件升级/回退及 CLI owner 崩溃恢复已通过。完整最新包验收另列，F1 helper 替换仍未通过。正式契约见 [G2 契约](p4-code-update-contract.md)，G1 基线已提交推送 `6883cd5425c429b6c472163efd0918bf927eac65`。

## 实际 App 更新

`build/p4-code/g2-actual-app-updates.json` / `g2-actual-app-updates-second.log` 在新的 `memlane-p3-code-app-iclijwwv` 隔离根通过。当时完整 builder 产物在 `build/p4-code-g2-app/`，含 helper/LaunchAgent、Agent 桥、独立 Enola/Rust/模型准备接线；当时 worker 为 0.0.2、原文协议 1、schema 12。此路径随后用于 F1 helper 替换实验，原 DMG 已被移除，不能将历史体积视为当前产物。受控 loopback 源下载量沿用固定组件 **301,171,065 bytes**，不是正式更新渠道。

真实 UI 选择登记项目 A、明确开启后，连续 TS/Python 修改与新文件由轮询合并生成新 snapshot；无手动刷新。旧 current 的来源变化立即返回 code_stale，新符号/路径/SHA 核对实际文件。B 虽已登记且源发生变化，没有开启记录或图。TS 重命名与 Python 删除后，当前 manifest 和节点排除旧文件和符号。

单文件超过 1MiB 的明确文件预算故障保持最后成功 receipt，App 显示失败并清除旧详情，当前查询失败。来源捕获错误清空 observed digest，修复源后观察 17 秒仍 failed，完整 jobs 未变化；实际 App 点击重新扫描后恢复。此前源码保留旧 observed digest 会继续自动扫描，该问题已修，不能把第一次立即点刷新当作禁止自动重试的证据。

停止服务并退出后，重开实际 App 仍保持 stopped，不存在运行 IPC；点击启动服务后对离线期间变化生成新图。同一 worktree/dataset 保留；generation 与停止前最近一次手动重试后的值一致，不与最初 generation 混淆。新任务只增加一次，来源/任务历史保留，知识数量仍为 0。四张 `build/p4-code-g2-screenshots/g2-*-system.png` 实际系统截图已打开核对：三栏、连续浅蓝栏、明确失败与新来源/SHA；未使用原型截图替代。

第一次报告与日志保留于 `g2-first-generation-assertion-failure/`：六个流程返回后，driver 错把手动重试增代后的值与最初值比较，命令保持 failed。修正比较基准后在新根完整重跑，不修改原失败状态。

## 实际 Rust 与故障夹具

`g2-relocated-retention-after-review.json` / `g2-relocated-retention-after-review.log` 使用原固定 0.0.2 worker 独立归档重新定位，真实 Enola 抽取并生成三份 Ladybug 图；清理后当前与最近成功历史仍可实际查询，更早图目录不存在，查询返回正式 missing-source engine_error，然后 worker 继续成功查询两张保留图。额外 `.building-UUID` 是明确的无活动进程残留夹具，非真实崩溃证明。原 G0 报告未覆盖。

`g2-retention-faults-after-review.log` 为 NO MODEL/RUST 的真实 FS/SQLite 故障夹具：保留 current/previous/未知目录，旧 UUID 目录和 staging 删除；绝对外链、相对 A→B 与 code→other 均拒绝，另一范围两份保留图与 sentinel 不变。生产 Store helper 的 disabled/current/previous 选择、全部 SQL jobs 保留、损坏 receipt 时停止清理，以及来源失败清 digest、旧轮询不读取，均通过。

`g2-actual-app-lifecycle.json` / `g2-actual-app-lifecycle-final.log` 是真实 App 三次停用扫描：每轮写入 3500 个合成 TS 函数，等待本轮源 SHA/任务代次绑定的新增 Ladybug `.building-UUID` 及真实 worker Busy，再由 App 停用。旧 PID 和整个进程组退出，新 worker ready 后重新核对 disabled、generation、snapshot、完整 receipt 及目标 job cancelled，迟到发布被排除；三轮都没有消耗异常崩溃预算。删除负载、实际 App 重新开启后查询当前源成功，最多两图与无 staging/临时提取残留，SQL 任务与来源历史保留。正常取消没有模型协议夹具。

代码调用取消/超时会在释放 worker gate 前回收整个 worker，防止取消 future 后 native 操作继续占用目录。受控回收与故障崩溃分别计数；参数错误继续保持 worker 可用。早期 lifecycle 产物分别保留在 `g2-lifecycle-before-late-fence-proof/` 与 `g2-lifecycle-before-new-stage-proof/`：前者未核对回收后的迟到发布，后者可能复用上一轮 orphan stage。它们不作为最终三轮 Rust 活动证明。

`g2-legacy-source-failure.log` 额外证明 G1 旧 source_unavailable/failed 即使残留成功 observed digest，也在首次 Capture 前要求显式重试。此处是生产 helper/真实 SQL 兼容夹具，不替代旧库升级后的实际 App 场景。

## 根目录与组件升级

固定 Enola 的 `.` 根模块与 TS/Python 共享 ID 是真实适配缺口，失败记录 `g2-component-root-module-failure.json` 与 `g2-root-language-extraction.json` 保留。现按 [根模块契约](p4-code-root-contract.md) 仅接受有非空 manifest 的受限 module 根节点；共享 module 仅在完整事实一致时去重，文件/符号/依赖路径不获得点路径例外。新 worker 0.0.3 / protocol 1 显式广告 code_root_modules，旧 0.0.2 不支持根导入，仍可读取已有合法图。

`g2-component-root-upgrade-final.json` 与 `g2-component-root-legacy-upgrade.json` 使用真实签名 loopback feed、真实 worker/Enola：0.0.1 原文 → 0.0.3 根 TS/Python 图与原文 → 回退 0.0.1（代码明确拒绝，原文保持）→ 恢复 0.0.3（同图回执）。真实 0.0.2 旧图另由 0.0.3 读取，dataset/snapshot/graph/source digest、原 fact ID、行号及 SHA 一致。签名错误拒绝，截断下载保留运行旧组件；失败首轮 4096 + 续传 69,948,486 = **69,952,582 bytes**，恢复和回退缓存均为 0 额外下载。不是主 App 更新量或正式源验收。

`g2-root-worker-raw-corpus.json` 真实原文回归 9/9 top1，generation 不变。`g2-root-actual-app.json` / `g2-root-actual-app-second.log` 实际冷准备 App 使用 0.0.3，只有明确开启后的项目生成根 TS/Python 图；两种语言的具体函数、line 1、文件 SHA、extractor ID 和 current manifest 核对实际文件。默认全局范围的第一次错误期望保留为 failed；第二轮完整新根通过。实际系统截图 `build/p4-code-root-screenshots/root-mixed-symbols-system.png` 已打开核对。首次额外下载 **301,171,513 bytes**，来源为受控测试源。

## CLI owner 崩溃恢复

`g2-code-owner-crash.json` / `g2-code-owner-crash-after-review.log` 为明确 **actual_app=false** 的真实进程故障：新合成根内，真实 Enola 生成 3500 函数时 SIGKILL 该 CLI helper 所有者；kill 前将 guard 与 Rust worker 的 PPID/PGID 绑定到该服务。旧组退出后才启动新 CLI 服务，旧 job 为 interrupted，授权 generation/dataset 与基线 current snapshot 保持，该代次仅一个 completed 新 job；实际当前函数可查询，真实崩溃提取残留已回收，health.code_cleanup_error 为空。该范围不证明 App/launchd 在旧原生组仍活时立即重启的竞争。

owner 驱动的确认遗漏、ps 表头解析失败分别保留 `g2-owner-confirmation-gate-failure.json`、`g2-owner-ps-header-failure.json`。补审发现已死 owner 的失败路径遗漏孤儿组清理；现仅对已捕获身份的安全组进行有界最终回收，强制清理保留 failed，二次退出超时写入报告。`g2-owner-cleanup-fault.json` 是隔离 sleep 进程组夹具，验证整组退出和危险 PGID 拒绝，不代替真实 Enola/Rust。修后真实 owner 流程完整重跑通过且无需强制清理。

终态提取回收的 `g2-extraction-cleanup-fault.log` / `g2-cleanup-tests.log` 为 FS/SQL 夹具：只删除 completed/failed/cancelled/interrupted 的全部规范 UUID，active/unknown 保留，非法 UUID/链接/目录身份不符拒绝，读取失败不删除，SQL 历史不变。

## 最新完整验证包与体积

包含最终终态提取清理的完整包位于 `build/p4-code-g2-final/darwin-arm64/MemlaneCodeG2Final.app` 与 `memlane-0.0.3-arm64.dmg`。`g2-complete-build.log`、`g2-complete-package-integrity.json` 与 `g2-complete-package-app.json` / `.log` 记录严格深度 codesign、hdiutil verify，以及新的隔离身份实际冷准备、打包 helper/LaunchAgent 服务启动、根 TS/Python 三栏查询。MCP/CLI 桥为 `Contents/Resources/bin/memlane`，pi extension 内嵌于 App/helper，按隔离连接安装；不以不存在的独立 extension 文件作为包回执。

`build/p4-code-g2-final-screenshots/root-mixed-symbols-system.png` 已实际打开对照设计图：连续浅蓝侧栏、紧凑三栏、单色操作、实际来源 SHA 与 snapshot。此包首次身份安装通过，`replacement_verified=false`，不能覆盖 F1。用户图标、branding、design 和 builder 修改保留但不纳入 G2 提交；当前包外观包含本机这些输入，不冒称仅由提交树可完全复现的正式发布物。

`g2-complete-package-sizes.json` 的独立测量：

| 指标 | bytes | 范围 |
| --- | ---: | --- |
| 主 DMG 下载 | 15,614,974 | 含 UI/helper/LaunchAgent/桥，不含引擎模型 |
| App 普通文件逻辑大小 | 35,129,574 | 安装 App 本身 |
| 首次额外组件下载 | 301,171,513 | Rust 0.0.3 + E5 int8 + Enola，受控 feed |
| App + 实际准备根磁盘逻辑大小 | 593,553,297 | 含组件、模型、数据、设置与缓存，不含源 worktree/Agent/开发工具链 |
| OS 分配文件块 | 610,172,928 | 不含目录元数据；APFS共享块不等于可释放空间 |
| 独立 worker 更新下载 | 69,952,582 | 4096 故障首轮 + 69,948,486 续传 |
| 主 App 更新下载 | 未知 | F1/正式 updater 尚未通过 |

新 worker 归档本身为 69,952,582 bytes，展开普通文件 206,998,480 bytes，见 `build/p4-code-root-component/worker-sizes.json`。历史 G2a 体积属于替换前包，不代替本轮测量。

## 检查与剩余边界

`g2-complete-test.log`、`g2-complete-vet.log`、`g2-complete-race.log` 是包含根模块和最终提取清理的全仓 test/vet 与 store/daemon/engine/codegraph race，通过。Bits Store/runtime/UI/cross-root/cleanup 及修复历史在 `build/p4-code/review-g2/`。初期通过日志仍保留，不能代替最终范围。

F1 的同路径 helper 替换触发系统 Launch Constraint Violation / EX_CONFIG 78，见 [F1 诊断](p4-helper-replacement-validation.md)。完整 F 分发及 A1b/A2b/R 必需项继续。Developer ID/公证/正式更新源/干净系统没有此处证据，总目标未完成。主 App 更新下载量未知，未测不记为 0。
