# Agent 接入回执收尾验收

2026-11-15，用户要求继续推进上一轮接入页自动刷新及宿主升级复核收尾。沿用 [环境接入记录](agent-environment-delivery-20261115.md)，不扩展为默认pi／OpenCode接入、历史补采或release。

## 前置与资源归属

- 只读dev状态显示日常服务ready，使用原日常配置与同一官方存储；completion健康显示ready、活动／等待0、并发上限2。此前32请求是旧时点，不能继续作为当前阻塞事实。
- 共享会话正在实际Native中处理权限布局和dcim-fe保存未知结果，已明确要求本轮不重启、不操作Native，直到交回资源。尊重其任务及原始材料，不由空闲completion单项推断整套服务可安全重启。
- 本轮新增 `internal/simpledesktop/agent_environments_test.go` 的检查，并修复 `agent_environments.go` 中查询恢复后残留自身错误提示；未修改真实宿主配置、权限、统计或数据，未执行额外模型任务。

## 已完成检查

1. `TestWaitingAgentReceiptSettlesFromIdleViewTimer`，两种等待状态分别执行：`configured_pending_new_session`、`environment_receipt_unconfirmed`。进入实际App.View，先确认截止时间未到时不发请求；到期后不输入、不手工重绘、不直接调用刷新函数，只推进MyGo已有定时帧，确认GET `/v1/connections`更新状态为已连接、保留其他选择。收敛后继续推进一分钟，确认没有再发接入回执查询。
2. 既有 `TestWaitingAgentReceiptsRefreshWithoutInstallingOrLosingSelection`继续通过，核对限频、只读与状态收敛。Go定向检查0.755s通过。
3. `test_host_upgrade_review_requires_fresh_receipt_without_reinstalling`：升级指纹变化触发复核；显式复核保留配置字节、不调用prepare，旧回执不足以显示已连接。
4. `test_loaded_environment_receipt_mismatch_never_registers_wrong_project`：错误环境回执被拒绝，不登记错误项目；正确回执可核对实际环境。上述两项Python检查1.155s通过。
5. 本轮文件diff空白检查通过。

## 自动查询失败后的恢复

`TestAgentReceiptRecoveryClearsOnlyItsOwnError`先红：HTTP503后下一轮成功，连接虽已确认，仍残留“接入状态暂时无法核对，保留上次结果。”。最小修复只在成功且当前错误精确匹配这条查询错误时清除，不清除其他安装／操作未确认错误。测试同时核对失败保留旧结果、恢复状态及其他操作错误保护。

修复后环境选择、等待／静置刷新及恢复的Go定向检查0.789s通过，随后Go全量 `sh scripts/go.sh test ./... -count=1 -timeout=120s`通过（simpledesktop 2.965s）。

独立问题RECEIPT-RECOVERY-ERROR01累计失败1轮（本次定向红），不与TRAEX-DAILY-DISCOVERY01或验收API编译错误合并；未达到停止阈值。实际Native断连恢复仍未验证，不为此中断日常服务或人为改写真实回执。

源码检索纠正：最初局部只看到agentConnectionSettings按渲染检查10秒截止时间，疑似缺少唤醒；进一步核对发现正常App.viewContent已有 `c.After(2*time.Second)`。撤回缺计时器推断，未增加重复计时器。新增测试验证该现有定时链。此结论限正常设置页，不外推其他承载路径。

方法错误：首次编译新测试用了MyGo Tester不存在的Update API；改用已有Frame，仅用于HTTP完成回调后的重绘。首次错误是验收代码编译失败，不算真实产品自动刷新失败。自动触发GET的前半程不使用Frame，避免用手工重绘掩盖静置问题。

## 实际加载与窗口结果

- 共享会话已交回服务与窗口。停前再次确认completion活动0、backlog completed、官方四类pipeline无非终态后，通过原dev入口正常stop并确认释放原日常库。
- 下述模型缓存修复后正常start成功，新owner PID55101、启动2026-11-15 09:58:06、核对时phase ready；官方健康ready、completion ready/活动0。原official_data路径保持，没有切库或创建新运行库。
- 实际Native设置显示Cognee已连接、TraeX已连接；展开环境显示当前目标 `~/.trae`、最近会话回执已收到、实际加载环境已核对及原插件版本。pi／OpenCode仍未连接，没有为本轮补装。
- 管理GET确认原知识计数daily-cli=3、Memlane=4，重启后未丢失；Native首页原10条知识及33条原始记录可见。
- 自动等待→已连接的时间变化由实际App.View定时帧＋HTTP检查证明；本次真实窗口没有尚在等待的新接入项，因此没有把静置不变画面当作这段变化的Native证据。真实宿主升级由受控协议检查证明，不为验收替换用户二进制。
- 最后Go全量及vet通过，源码在实际窗口检查期间冻结。

等待窗口期间的只读预检：completion ready、活动／等待0；backlog日志1个completed；官方四类pipeline均处于终态，包含既有cognify errored，不重跑。连接API显示真实TraeX environment_verified=true、4个新会话，pi／OpenCode未连接。Codex返回14个新会话、environment_verified=false，沿旧接入证据，不能宣称本轮已核对其新环境。未据此抢占共享服务或执行重启。

接入问题沿原TRAEX-DAILY-DISCOVERY01计数，不因本次收尾重置；本次未新增真实TraeX接入失败。错误恢复与模型缓存问题按各自具体触发条件独立记录，不混入接入计数。

## 重启暴露的本地模型依赖缺失

独立问题EMBED-CACHE-MISSING01，首次重启失败1轮；恢复后第二次启动通过。09:55:11启动的官方服务无法加载原BAAI/bge-small-zh-v1.5，launcher报generation_start_failed并退出清理。原HF_HOME仍存在，但两个仓库共7个snapshot链接的blob目标全部缺失，refs/main也缺失。目录存在和旧服务ready不足以证明重启依赖完整；本次重启前遗漏了模型链接目标检查。缺失发生时间与原因未知，不归因于本轮或其他会话清理。

仓库另存Xenova int8转换模型，摘要不同，未拿它替换日常embedding。按原snapshot固定revision恢复BAAI `7999e1d3359715c523056ef9478215996d62a620`及Qdrant `46fbe35fd4374a00fee7de77dfddaeb6dd6a2c59`的7个文件，共95,660,924字节。64位blob名用SHA256核对，40位用git blob SHA1核对；全部匹配才原子发布，并恢复固定refs/main。原模型ONNX SHA256为 `1294ea4b6331115a353d81f96b85e8c8d7fdcc284453d5b2fab5b016230aad38`。7个链接均恢复有效。

恢复前后同一官方库、原配置、模型版本和索引保持，不重建向量或整理、不重放会话。下载临时part文件和本轮恢复脚本已清理，保留恢复后的必需模型及小型校验回执。[恢复摘要](evidence/agent-receipt-final-20261115/recovery.json)。磁盘始终高于100GiB。

后续服务重启前，应同时检查HF_HOME snapshot软链目标与固定refs，而不只检查目录；目录被日常配置引用就属于运行依赖。当前模型及官方数据仍位于历史临时根，应继续保护；迁出该根属于独立运行依赖迁移，不能先删旧根或仅复制界面包。本轮未实施迁移。
