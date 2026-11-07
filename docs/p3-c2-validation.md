# C2 App 手动整理验收

2026-11-08。复用正式 C1 的 schema v3、持久任务、来源快照、原子审核、取消/attempt 栅栏与 Rust 原文索引，增加类型化私有列表与三栏界面。正式规则见 [C2 契约](p3-c2-contract.md)，全部后续工作见 [交付矩阵](p3-delivery-plan.md)。本切片通过不表示完整整理或正式分发通过。

## 实际通过

验收包为 `build/p3-c2-final-app/darwin-arm64/MemlaneC2Final.app`，包含 UI、helper、LaunchAgent 和记忆桥，通过 Launch Services 启动。每轮使用新系统临时合成根和独立包标识，只复制已通过的组件缓存，没有用户知识/聊天/Agent 配置；当前根由 `build/p3-c2/fixture-path.txt` 指定。测试环境 macOS 26.6.2 arm64、traex 0.214.1 internal、既有认证与模型选择、真实 Rust worker/E5-base int8。

| 场景 | 实际事实 | 证据 |
| --- | --- | --- |
| 选择与开始 | App 登记两个合成项目，在一个项目内选择两条有效知识，开始返回稳定 task ID；五场景驱动观察 1.06–1.64 秒，含窗口 idle/查询耗时 | build/p3-c2/real-app.json |
| 纯建议与审核 | 真实 traex 返回正式结果，终态、退出 0、进程等待与组消失通过；确认前两来源仍 active v1/原正文；右栏展示冻结正文、版本、原出处、精确引用和改动对照 | 同上；review-real-system.png |
| 离页返回 | App 首页显示最近待确认，点击返回同一持久任务与建议，确认后新建合并 v1 | 同上 |
| 确认与检索 | App 确认，一次应用回执、合并结果 v1，真实 Rust 搜索返回合并当前版本，归档来源旧命中不返回 | 同上；merged-real-system.png |
| 来源与恢复 | 从结果持久关联返回任务，查看归档来源和 v2 整理归档历史；App 恢复 v1 产生 active v3，合并仍 v1 | 同上 |
| 拒绝 | 真实建议经 App 拒绝，原知识仍 v1，任务显示已拒绝 | 同上；rejected.png |
| 活动取消 | 真实 turn.started 后 App 取消，持久 cancelled、无建议、源 v1；确认进程已等待退出且同组进程消失 | 同上；cancelled.png |
| 过期 | 明确的实际服务编辑夹具改变一条来源；整项 expired，App 显示过期并移除确认按钮，冻结依据保留 | 同上；expired.png |
| App 重开 | 退出保留后台后重开，从持久列表重新找到过期任务 | 同上 |
| 主动停服与重启 | 另一真实活动 turn 经 App 停服退出，重开后明确启动服务；任务 interrupted/attempt2，无建议、不自动重跑，源 v1 | 同上；interrupted.png |
| 失败状态 | 专用开发工具只在显式停止的合成根用实际 Store 提交执行器 timeout 夹具；App 显示失败和 agent_timeout | ui-failure-fixture.json；explicit-ui-failure-fixture.png；不是正常模型替身 |
| 索引失败 | 新合成项目的实际 Rust dataset 输出路径放置阻挡文件，真实 worker 已正常启动，索引写入返回错误；真实 traex 建议经 App 确认，正文/回执保留，App 显示索引失败 | build/p3-c2/index-fault-app.json；index-failed-real-system.png |
| 索引恢复 | App 停服后移除专用阻挡文件，重开明确启动真实 Rust；合并可检索且应用回执相同，实际搜索返回 v1 | 同上；index-recovered-real-system.png |
| 列表与关联 | 实际 SQLite 有界分页/同秒顺序、项目游标拒绝、来源变化推导过期、从新结果与归档来源找任务；不改 schema，不把列表当审核授权 | TestMergeListPagingProvenanceAndExpiry；go-test-final.txt |
| 新客户端/旧服务 | 从原始 C1 SHA 构建旧服务，在新临时根实际运行；新 IPC 客户端的 health/scopes/apply/get/stop 通过，新列表在旧服务明确不支持 | build/p3-c2/legacy-c1-compatibility.json |

截图均在 `build/p3-c2-screenshots/`。正常、拒绝、活动取消、停服使用真实 traex；过期是显式版本编辑，失败状态是 UI 执行器夹具，索引错误是明确的真实 Rust 文件系统故障。不将源码检查、普通测试 skip 或后台 C1 证据代替本表的 App 操作。

## 窗口与审查

开发前实际打开 home.jpg 与 memory-detail.jpg；验收后实际打开审核、合并、索引失败与恢复的系统窗口截图。采集元信息核对本 App key/visible 状态。三栏宽度、连续浅蓝侧栏、白色内容、单色按钮、右栏来源与固定审核按钮符合选定设计；长来源通过右栏滚动承载，首页保持宽幅布局。普通查看、确认、拒绝、恢复均未引入模态窗口。

按 bits-code-guard 分组检查 UI 与服务列表、共享类型及调用方；修复六类问题：零值新增字段破坏旧服务严格解码、关联失败牵连知识刷新、可选关联超时牵连历史、后续归档结果误显示等待索引、切换知识时旧关联跨选择显示、启动前未绑定合成根与实际包身份。最终当前缺陷清单为空，原发现与修复记录保留在 `build/p3-c2/review/`，报告为 report.html/report.md。补审验收隔离与清理产物另外记录，启动前门禁正向核对当前实际包，错误生产包在启动前被拒绝（fixture-identity-verification.json）；代码审查不代替实际 App。

全仓 test/vet 与受影响 store/agents/daemon/desktop race 通过，日志和退出码见 `build/p3-c2/final-checks.json`；修订后的完整全仓检查另存 go-test-final.txt/go-vet-final.txt。本地文档链接与 diff 检查通过。完整包构建日志 production-build.log，签名仅 ad-hoc。

## 失败历史与能力边界

首轮驱动误用不存在的 execution.status 字段，失败保留 real-app-first-assertion-failure.json；改为实际终态/进程回执后重新验收。原地重建修订包后启动超时保留 real-app-rebuild-startup-failure.json，新的独立标识与合成根取得最终通过；该失败原因未在本切片完整定位，不能声称 App 更新/移动路径故障已通过。

引擎无法启动时任务保持等待索引，首次故障驱动错误期待 failed，记录 index-unavailable-waiting-failure.json；随后改用实际 Rust dataset 写入错误并取得独立通过。不把前两次失败或修订前通过计入最终包通过。

本机 internal CLI/当前模型组合通过，其他 traex 版本渠道、TRAE 日常接入、pi RPC 和完整整理动作仍待后续。取消保证本机同组进程收口，不声称云端计费撤回。完整租约、计划恢复和 SIGKILL/逃逸进程留在对应切片。

## 本机包与体积

生产构建仍使用 `python3 scripts/build-client.py --out build/p0-final`，主 DMG **14,848,463 bytes / 14.85 MB**，App 逻辑文件 **33,384,224 bytes / 33.38 MB**。固定成功归档的首次额外下载 **285,752,286 bytes / 285.75 MB**（Rust 66.25 MB + 模型 219.50 MB，复用已验收归档）；实际合成安装根加 App 的逻辑磁盘量以 `build/p3-c2/final-sizes.json` 为准，数据变化会影响最终数值。失败重试与传输开销不包含于下载字节，APFS 分配/共享块与可回收空间不等同。

此次未测真实更新渠道下载量，不能记为 0；没有正式更新源。Developer ID、公证、Gatekeeper 正式通过和干净 macOS 15+ 环境仍未完成。主包不包含 Cognee/模型，当前本机功能验收依赖隔离公共运行配置，不能把这个包描述为已完成正式分发。

每检查点直接执行 p3-handoff.py，在系统临时目录核对存在并注明未调用禁止自动模型调用的 Skill；临时文件/build/本机配置不进入提交。阶段提交与 SHA 回执写入 build/p3-c2/delivery.json，提交后继续 C3 及全部矩阵项。
