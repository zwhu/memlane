# J/K 材料整理分派与逐项审核实施

本批按固定原型 `d2e3923de31ecd4bf7288c2527a9b98578a1ef17` 的 J/K 页面实施，沿用 [全量设计交接](full-design-handoff.md)、[开发账本](development-progress.md) 与已有 L04 配置冻结契约。完整 123 场景目标继续；本批不代表其他模型后端、完整审核矩阵或正式分发已经完成。

## 本批实现

- 整理任务作为整理记录的默认入口，普通操作在右栏完成；原合并记录仍有独立入口。启动前显示项目、所选知识／原始记录、具体执行者与 Token 消耗。页面只读取记忆处理账本，不因打开、切页或选材料而验证或启用配置。
- 三种整理 Schema 均可冻结明确启用的处理配置修订、选择代次及依赖摘要。后台使用该修订分派；设置后来切换不改派旧任务，连接／依赖变化仍须拒绝。旧的未带 Processing 的历史任务保留其原执行契约。
- 自动新增摘要／标签与待确认修改分组显示，另保留已确认、已拒绝、未完成和过期结果。执行完成与建议确认分别记录，摘要来源使用实际生产者，pi 结果不再标为 traex。
- 单项审核显示冻结来源及版本、建议正文、具体影响和合并／覆盖差异。普通窗口左右排列，窄窗口纵向排列，拒绝／确认／下一项固定在底部；返回和同项重入保留滚动。
- 当前来源读取失败、版本变化或范围变化禁用新的确认。已冻结确认请求仍可读取原回执；后台继续执行原子的版本／范围／出处校验。取消必须取得进程已回收与进程组已消失的回执后才显示结束。
- 整理状态增加库 Epoch，异步读取和操作捕获库、Client 与生命周期，拒绝切库后的晚回执。未取得启动回执的同窗重入保留原 operation 和材料，不创建另一个请求。

## 失败与诊断证据

证据目录 `build/ui-fidelity-20261110-018/`。每个新包使用空业务根、合成项目及新完整 App，组件独立复用，不复制用户业务库、聊天或 Agent 配置；pi models/auth 只临时链接，由 pi 自行读取。

| 新包 | 原始结果 |
|---|---|
| `memlane-p2-material-organization-001` | 已有知识项目中查找空态大按钮失败，验证器改从整理记录进入；没有恢复已被 C 设计移除的按钮 |
| `memlane-p2-material-organization-002` | 原生 Select 的 setValue 未选中项目，改为实际打开下拉 |
| `memlane-p2-material-organization-003` | 将 MyGo 原生选项当 menuItem／option，实际是 popup 内的 text 子节点；原树保留 |
| `memlane-p2-material-organization-004` | 对 popup 父节点 press 未选中内部项，改为真实指针点击选项 bounds 中心 |
| `memlane-p2-material-organization-005` | 真 pi 自动新增／Rust 索引与 App 重开不重跑已通过；随后模型合法只输出自动候选，没有待审核建议，验证器的审核预期失败。此结果不是模型传输或 Schema 错误 |
| `memlane-p2-material-organization-006` | 第一项任务在进程启动前失败，原回执只有 `agent_failed`，没有 Agent 版本，未保存新知识。原证据不足以确定具体原因；12Gi 可用空间不能据此推断磁盘耗尽 |
| `memlane-p2-material-organization-007` | 真实连接验证成功，但 processing_validate 启动前返回 `unsupported_agent_version`；尚未开始材料整理。原标记 actual_processing_dispatch_verified 不作为任务实际分派的证据 |
| `memlane-p2-material-organization-008` | 限定主线通过：真实 pi 配置分派、摘要／标签保存与 Rust 索引、App 重开同任务不重跑、实际活动取消回收、真实合并候选的普通／窄窗口预览、确认前源版本不变、原子确认后保存／索引与同任务分组结果。10 张系统窗口图保留 |

006 后先建立回归检查：真实 RunPiCompletion 的合成探测进程退出时，JSON 回执原本无法区分失败阶段，`TestPiCompletionPrelaunchFailureReceipt` 已取得失败输出。增加有界 failure_stage／failure_code 后通过；Schema、认证目录、版本／能力探测、配置读取、私有目录、认证链接、Schema 扩展、RPC 与输出校验分别记录阶段。探测进一步区分退出、超时、不可用、权限与输出上限，ResolvePi 不再把这些命令故障统一记成版本不支持。没有持久化原 stderr、模型原输出、路径或凭据。

`pi-probe-diagnostic-001.json` 的只读命令与 `pi-probe-diagnostic-002.log` 的生产 Probe 路径均连续三次通过，实际 pi 为 0.84.1；后者使用短 PATH 与空 HOME。这些探测没有调用模型，不能代替真实材料整理，也没有证明 006／007 的具体根因。保持版本、Schema、出处和隔离门禁，不提高超时或自动更换模型来绕过失败。

## 分层验证与后续

Store／IPC 检查覆盖三种 Schema 的配置冻结、配置切换后的旧请求回放与新请求旧代次拒绝；Desktop 检查覆盖只读配置、跨库晚回执、未知启动同窗重入、结果分组、来源版本失效、取消回收与单项滚动。七类审核 × 普通／窄窗口的 14 份 Native 帧位于 `native-review-matrix/`，这是合成渲染证据，不是系统窗口或真实模型结果。

008 真实报告为 [report.json](../../../../../build/ui-fidelity-20261110-018/memlane-p2-material-organization-008/report.json)，完整回执在同目录 `material-organization-receipts.json`。本机 pi 0.84.1／DeepSeek deepseek-flash 完成一次自动任务、一次实际活动取消和一次包含 duplicate／merge／summary／add_tags 的审核任务。三项各有一个模型 attempt；取消后的 task attempt 旋转用于拒绝旧回执，不表示第二次模型推理。App 重开后第一任务及执行回执不变。

本轮模型原候选包含真实合并建议。确认前两条源知识均为 v1；Native 确认后新合并知识 v1 已索引，原来源以 v2 归档，正文与历史保留。两条精确 v1 引用与原模型候选保持，另一项 duplicate 因来源变化过期，先前自动摘要仍保存但被排除当前检索。`terminal-material-lineage-recheck-002.json` 是停服且两锁释放后的不可变只读复核；前次普通只读打开失败独立保存在 `terminal-material-lineage-recheck-001-failed.json`，没有覆盖原包报告或重新打开 App。

已实际打开首页／三栏参考、固定 J05／K01 场景及 008 的配置、自动结果、取消、普通／窄审核和确认后结果图。普通审核保留左右来源／建议、具体影响与逐来源差异；窄窗纵向来源／建议及其后内容可滚动，底部拒绝／确认／下一项可见。结果按自动完成／已确认／过期分组，不混入其他任务建议。七类 Native 帧仅证明渲染，真实模型与实际窗口本次只覆盖上述合并主线。

008 已核对 socket 消失、service／worker 两锁释放、临时认证链接删除、用户 pi 原配置散列保持。主 App 文件 43,289,483 字节；首次组件清单下载量 285,752,286 字节，此包组件复用无重下载；App 与 client 的分配占用 535,842,816 字节。未进行 Developer ID 公证、最终分发或严格无组件缓存冷启动验收。

最终全量 Go 测试、vet 与 desktop／store／daemon／agents／processing 的 race 检查记录为 `build/material-organization-*-016.log`；启动前诊断回归和三种 Schema／配置门禁通过。008 没有复现 006／007 的启动前故障，其具体根因仍未确定，不将新增诊断写成原故障已修复。

重新读取设计入口、J/K/L/M 与全量覆盖，原型最新仍为 `d2e3923`，没有新增固定设计。本批仍不关闭未知启动的完整持久恢复／取消、配置改变后的原请求处理、所有模型后端、七类真实模型审核、完整故障／权限／分页与多任务窗口矩阵。下一批先补 J07/J08 与 K07/K09 的恢复、分页筛选、拒绝及多项上下文，再接完整配置后端与剩余 123 场景。每项继续在开发账本追踪，不把已通过切片缩成总目标。
