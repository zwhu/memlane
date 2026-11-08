# 本机首次使用与项目记忆浏览的共同契约

2026-11-08。两部分必需连续运行验收通过，见本轮验证记录。仅交付本机环境准备、官方 Codex/Cognee 首次合成任务与项目记忆浏览；不恢复旧全量工程 Goal，不选择日常自动记忆策略。Q1–Q4 保持开放。

## 共享身份与状态

| 对象 | 真相与读取边界 |
| --- | --- |
| 环境 | App 独立身份与明确数据根；发现、兼容复用、配置、授权、验证分别记录。首次使用不是导入历史会话 |
| 项目 | 服务登记的 UUID 与 Git common directory；连接代次和受信工作区决定允许范围，标题与模型输出不能指定归属 |
| 可用性 | 组件清单、文件校验、helper/LaunchAgent 及实际服务/worker回执；已安装不等于可用 |
| 原始记录 | 官方实际 QA/trace 稳定 ID、会话、原样分段和导入版本；本轮批准的完整 Codex 日志另存同源 raw，不计为新增知识 |
| 知识 | 官方 session_learning 实际产物，经受信 cognee_import 新建版本与冻结来源；保存、提炼、导入和 Rust 当前版本可检索分别表达 |
| 实体与关系 | 官方 dataset 图中的稳定 ID；只展示能回到已授权具体材料的关联。实体数量与关系数量不计为知识 |
| 导航 | 项目、视图、记忆 ID、实体 ID 与来源具体版本；项目切换清除前项目响应，重开恢复选择后重新校验授权与版本 |

官方新会话 recall 查询独立 Cognee；App 知识搜索查询 Memlane 与 Rust。图查询不替代原文索引，App 编辑不自动写回 Cognee，全局经验仍按已有逐条复制/提炼、确认、独立版本与来源规则执行。

## 实施切片与验收矩阵

| 切片 | 当前状态 | 证据与剩余条件 |
| --- | --- | --- |
| 基线 | 已核对并保护原 index/worktree | build/onboarding-memory-validation/baseline/ |
| 来源阅读器 | SDK 分段正式窗口通过；完整 Codex 原文解析与正式窗口、长分页/复制/定位均通过 | build/memory-browsing-validation/official-app-4/、native-original-report.json |
| 受信图与三视图 | 27 实体/30 关系真实来源、两个项目、跨视图和重开已通过 | 当前知识与 SDK 来源保持具体 v1；Native 不改图/知识来源 |
| 首启/方块灯泡 | 真实空根/空缓存、下载/全部服务验证、失败/下载取消/跳过/同根恢复通过 | 正常路径与 HTTP 503 故障注入分开报告 |
| 官方正常任务与保存 | 首 session 正常工具任务、真实 QA/trace 持久水位和 4 knowledge 已通过 | 缓冲、后台提交、持久化、提炼、Memlane 导入分开；Native补接及正式包通过 |
| Rust 当前版本 | 4 knowledge逐条实际查询通过 | 图检索独立；新增 Native raw 不作为 knowledge |
| 全新官方 recall | 无 resume、无首 session历史、正式官方 graph召回已通过 | final-cold/fresh-recall-evidence/report.json |
| 活动取消/迟到与失败恢复 | SIGINT真实工具取消、授权撤销、迟到sync及旧请求拒绝已通过；重开显示真实受限状态 | 不以驱动失败、正常完成或故障夹具算真实取消 |
| 完整包与交付 | 完整包、全仓test/vet/受影响race通过；清理后完整包、体积和交接通过；提交/普通push以Git最终核对为准 | 反复原地重建包的 macOS launch constraint失败保留；新独立身份验证当前代码 |

证据分别位于 `build/memory-browsing-validation/`、`build/first-launch-validation/` 和 `build/onboarding-memory-validation/`。故障或协议夹具只证明对应边界，不作为正常 Agent、组件来源或正式 recall 证据。

## 完整会话与 SDK 分段

Cognee 1.6.7/官方插件 1.7.7 的 trace DTO 对工具返回最多保留 1,000 字符。SDK 分段原样保存；不补改 SDK 数据或官方 session_learning。受信 daemon 只从 App-owned Codex home 读取准确 session UUID 的唯一已完成 exec 日志，检查项目工作区、CLI 版本、final answer与task complete。读取保留按日志顺序的用户、助手及工具请求/返回；不导入环境注入、developer指令和内部推理，不扫描日常历史。

新增 `native` raw稳定 ID仍使用既有 `cognee-import-v1`命名与幂等账本，附实例/dataset/session/workspace/CLI/时间/正文SHA256；没有新增迁移。知识、SDK来源与具体版本不被修改。`source_read`为SDK引用附带同session的可读Native原文，读取前后核验当前本地授权/版本，官方dataset权限和准确原文摘要；不可用时不返回原文。普通JSON导入和Agent保存不能声明官方或Native戳。

阅读器显示“完整会话原文”与“官方采集分段”；复制当前模式全文，“定位引用”回到实际被引用的SDK分段，保留官方结果与原文差异。普通原始记录详情/历史/编辑不能绕过阅读器当前可读性检查。Native与SDK属于一份会话依据，不新增图实体或独立证据，不写回Cognee。

## 初始核对结果与剩余条件

开始时本地与远端为 `58a603b79498d8aa9073c93ab6f679c9a086d0e0`；原型为 `63841b1d40c29b71b261daf702fbb5671dd8599e`。工作区与暂存区的后台权限、图标、构建及既有文档改动保留，不混入本轮提交。已实际打开首页/三栏图、三视图及完整来源截图、方块灯泡图；原型 HTML/JS 只作为设计参考。

现有 P3-I 是可选所选文件初始化，不代表从空缓存准备到官方首次任务的连续路径已通过。开始 schema 为 14，本轮材料绑定增量到 15；旧文档中的 v3、C3/C4/E/D2/I/G 等待交付描述属于分段历史，当前以对应验证记录和源码为准。正式签名、远程管理、其他 Agent、旧 Goal 其余故障不属于此切片。

最终连续验收补充：最新不可变包已在新空业务/空组件缓存根的同一系统窗口，通过真实准备→正常官方任务→持久化→完整原文/三视图→所有Rust当前版本→全新官方recall；过程中包SHA未变。权威证据为 `build/onboarding-memory-validation/immutable-full-run/continuous/report.json`，恢复、重放和清理在同根证据中。旧恢复链路不替代这份连续证明。实际11 raw、2 knowledge、21实体/22关系按模型产物记录，不预置或补齐旧验收数量。

## 首启交互呈现续改（2026-11-09）

采用选定原型 `prototypes/first-launch/index.html` 的分阶段页面和固定操作结构：全白发现画面、紧凑 Agent/记忆环境分组、默认折叠检查详情、具体准备确认、实际任务与组件进度、明确首次使用。顶部状态栏和底部操作区固定，右侧编号/已完成/当前步骤高亮。初始化视觉继续采用方块散落→归整→灯泡，小灯泡只表达本机环境终态，不代表官方提炼已经完成。

生产呈现读取原有 FirstLaunchState、真实发现结果与 RuntimeStatus；不复制原型示例、版本、开关或固定等待。首启确认只准备本机 Rust/模型/helper，官方 Cognee 模型与插件的影响仍在真实项目内独立确认；发现 Agent 不显示为已接入。准备中的 MB 指组件处理进度，可复用量与真实网络下载量分别报告。

此续改的独立完整包和实际窗口位于 `build/onboarding-memory-validation/selected-interaction/`；新包全链路与重开验收结果以该目录报告为准。失败/取消/跳过/恢复的续验位于 `build/first-launch-validation/selected-interaction/`，与原有旧包证据分开。此前不可变包的连续证明继续保留，不替代续改包尚未完成的检查。
