# C 历史会话：真实归档、worktree 与任务连续性

2026-11-10。本批延续 [明确历史选择](project-history-20261110.md)，固定C原型为 `35a5bad5f5398f2c9cf3073b673983f5dab0a48c`，项目菜单与阅读采用当前交接。重新阅读C交接、动效契约、内容分层及全量进度；实际打开首页／三栏设计图和历史选择窗口。原型仍为 `780745fc55d71edb22e46d9c3e4f208d5ecb602f`，没有新增提交。123场景与C07–C09继续逐项验收。

## 实施与真实格式

`scripts/verify-codex-history-layout.py`在全新隔离Codex根与合成Git仓库／worktree中生成正常会话，然后调用实际CLI的 `thread/read → thread/archive → thread/read`。归档API只操作本轮会话，不读取用户旧历史或改用户默认配置。

`build/ui-fidelity-20261110-013/memlane-p2-history-layout-001/report.json`通过：CLI `0.167.0-alpha.3.2` 将 `sessions/2026/11/10/rollout-…jsonl` 移到扁平 `archived_sessions/rollout-…jsonl`；文件名、元数据及全部字节保持。正常会话工作目录为同仓库的worktree，模型为当前正常 `gpt-6.1-sol/openai`。协议响应、原会话日志与散列独立保存，此结果只证明本轮实际格式。

生产 `CodexProjectHistory`现在固定绑定Codex根，分别遍历正常日期目录与扁平归档目录，仍只按正式绑定和项目授权读取。两个目录共用目录项／日志预算、UUID身份、完整文本与内容散列规则；没有按名字猜测其他布局或放宽旧版本。

- 允许仅有归档目录；首次归档创建的目录被纳入身份固定。
- 正常归档保持同一会话ID和内容，因此已选版本可以继续读取；内容变化仍需重新选择。
- 跨目录出现相同会话ID时拒绝不明确的来源；不会取第一个或合并日志。
- 逐层 `openat`／`NOFOLLOW` 保持目录与文件防护，固定已见目录身份；目录替换或链接、根路径替换为链接均拒绝。
- worktree与主工作区共享同一Git共同目录才属于本项目；日志中的每个工作区与turn范围继续核对，混入其他项目不能成为可用选择。
- 归档不会扩大授权。daemon的绑定／安装检查、冻结来源、活跃task／attempt所有权以及读取／发送完成门禁继续沿用schema21；本批不迁移数据库。

`history-contracts-001.log`通过正常目录到新归档目录的移动、重复列出／读取、仅归档、跨目录重复身份、目录／文件链接与目录替换、真实Git worktree及混合项目根夹具。根路径链接的后补用例由 `final-tests-001.log` 核对。夹具不代替真实模型与App结果。

## 实际包与窗口

`project-history-archive`使用新App、空业务根及合成仓库／worktree，仅复用Rust／E5组件。先选择本轮正常历史，再由Codex官方归档，读取正式目录确认选择散列保持；原字节变化时拒绝旧选择，重新读取后才启动任务。

`memlane-p2-c-history-archive-runtime-001/report.json`通过实际历史任务的App重开与取消：进程PID和task／attempt保持，历史选择版本不变；减少动态效果的重开窗口显示静态归整方块。停止后真实 `turn/interrupt` 发送并确认，模型未正常完成，进程已回收且进程组消失，旧任务固定为cancelled。此时没有将任务完成书或完成勾用于取消状态。明确新请求后再选择当前归档版本，新任务最终保存3条知识及3份出处，并分别完成3／3 Rust索引；两项正式任务，一项已取消，一项已完成，旧取消回执保持。

已完成任务 `1f8d18ba-c1b2-48ec-933a-c1db90576877`；取消任务 `eab00f80-ec69-4abf-ab2e-ebd44d463aaa`。二者都真实读取并发送1份历史材料；完成任务历史回执 Selected=Read=Sent=1、Bytes=1601、摘要 `3950e25ac355e539dad9651132b9a686cbd5dd7986864d5ab577941d602105d1`。启动后原日志再变化没有替换第一次冻结版本，新的明确选择才采用新版本。

通过正式 `organization_sources`／`source_read`核对出处及精确引文、原会话时间、工作区和日志散列。保存后的知识与来源都是版本1，索引也是版本1。原始记录总数4，其中3份任务出处和1条正常连接验证记录；不把连接验证算成仓库或历史知识来源，不把本机历史当Cognee官方实例。

清理后在正式停止、socket消失及两把锁释放的前提下，使用只读immutable SQLite审计7条记录，写入 `knowledge-and-originals-audit.json`。三条知识逐项质量记录另存 `fact-quality-review.json`：pnpm／锁文件保持文档约定，字符串规范化与代码逐项一致，release／审批号明确属于历史用户要求，仓库未见强制执行实现。此结果限定本次3条、项目与模型，原始report中的自动质量待审标记保留。

App文件43,016,559字节；本路径Rust／E5组件归档285,752,286字节，仅复用组件且没有重复下载；App和client分配磁盘535,580,672字节。最终socket消失、两把锁释放、cleanup_errors=[]。本包不证明完整Cognee准备或最终签名／公证／正式分发。

主流程包构建后补正了根路径链接身份检查，由全仓测试覆盖；随后以最终源码的新 `project-history-archive-guard`包专门补验归档选择及版本变化拒绝，不为这项补正重复运行C整理模型。该包结果单列，正常来源生成与连接验证仍使用真实模型，不描述为无模型调用。

`memlane-p2-c-history-archive-guard-001/report.json`通过：同仓库worktree的官方归档会话仍可选择，日志变化拒绝启动，历史卡片只有一处重新读取入口，舞台显示静态失败方块，C任务数量为0。默认配置未改变；已打开最终窗口对照。App文件43,016,555字节、组件归档285,752,286字节、App及client分配磁盘535,429,120字节，无重复组件下载；socket消失、两把锁释放、cleanup_errors=[]。

`final-tests-001.log`全仓、`final-race-001.log` agents／daemon／desktop／store及 `final-vet-001.log`通过。Python场景脚本编译与git差异检查通过；原格式探针、主流程和后补包分别保留，不混为同一次运行。

## 继续推进的边界

归档格式仅适配本机准确CLI版本与正常文本会话。旧版本、迁移／目录不可归属、worktree已被删除、未知来源与pi历史仍分别待补。App重开只恢复原task／attempt，不授权自动恢复已取消任务；新请求继续明确消耗当前绑定模型Token。

普通／980×620历史选择、真实范围、减少动态效果和取消窗口已打开对照。Native保留真实知识／原始记录数量、查询及排序控件，历史卡片不添加虚构数量或费用。完整C动效仍未通过：快速保存／索引的实际阶段帧、卡片倾斜／遮挡、指针回位、隐藏停帧／能耗及完整静态矩阵下一批推进。`full_c_motion_verified`继续为false，不能用本批归档闭环关闭全部设计内容。
