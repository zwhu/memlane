# 知识阅读UI实施记录

2026-11-13。所有者：`01a1d316-960e-7280-a9fa-f4abb7abdb75`。用户附件已明确启动；当前Goal为该附件的持续任务，不另建重复Goal。完整目标：实现并自验Memlane知识阅读UI优化，包括当前页自动补标题、固定包装清理、技术信息折叠、可读出处与正确复制，完成RU规格和实际Native连续验收，使用dev模式。

## 基线、影响与所有权

- 依据reading-ui的产品／规格／技术方案，覆盖RU01–RU12、RU-A至RU-G；不进入release，不改官方数据、插件、模型、索引、dev.py或组件输入。
- 连续主流程：列表自动补标题 → 选择知识 → 清理正文 → 技术详情折叠／两种复制 → 真实来源 → 逐层返回 → 刷新／分页／切项目；保持选择、数量、滚动、工具状态。
- 影响：Document DTO、纯展示派生、进程内调度缓存、pane请求代次、详情复制与来源；最近知识仅消费统一展示，不改总览扫描和统计；搜索保留片段语义。
- 先纯函数与HTTP夹具，再共享接线、真实官方只读和Native1200×820。HTTP／源码不代替视觉验收；真实库不足30条时分页采用夹具，不造数据。
- 总览实现基线`4db3b8b`；总览session已退出，交接记录为overview-execution-20261113.md。旧PA确认暂停且未占共享文件；新PA `01a1d316-954a-7c91-8191-7fdfe3d4a8c9` 明确由阅读先持有app.go/client.go，自己仅做独立后端／插件。阅读结束后显式释放。
- Native/watcher使用前核对活跃进程和owner；不擅自启停官方服务。OVR01为继承stderr的BrokenPipe，健康和raw读取不证明图worker健康，不为本任务重放业务。
- 当前未提交PA后端／方案和其他研究文档均保留，不纳入本任务提交。禁止reset/clean/stash或git add .。
- 初始可用约115GiB；Go使用go.sh/task.py门禁，不清build或未知依赖。仅有限内存正文，不写磁盘副本。

## 验证与停止边界

- 定向覆盖可读label／精确机器名、80 rune和中英文首句、代码空白、严格唯一来源及包装冲突／原文保持。
- 调度覆盖4并发、请求合并、15秒、版本和凭据隔离、有界LRU、取消及迟到结果、403/404撤下／超时保留。
- 集成门禁：Go test/vet及race；真实只读仅保留脱敏metadata，Native实际观察并区分证据。
- E12标题溢出、E16多层返回原累计失败各1次；本任务按具体问题续记，第4次仍失败停止，不重置、不合并不同缺陷。
- 资源交接必须说明共享文件版本、标题接口、权限／身份失效入口、Native/watcher所有者和未验证边界。

## 当前进度

已实现阅读展示与调度，普通列表／详情／最近知识Native主流程通过；全局搜索真实返回500，RU-G的真实搜索与逐层返回尚未完成，不将Goal标为complete。

## 实现与证据

- 开发前范围checkpoint：`26ac754`，注释tag `reading-ui-before-implementation-20261113`。保留总览与PA的提交及无关工作树。
- `reading_presentation.go`：只读派生DisplayTitle／VisibleBody／TechnicalFields；精确机器名、80 rune正文首句、唯一来源与固定包装一致校验。未知模板保留原文；搜索片段不当完整文档。
- `reading_titles.go`：统一4并发、每次实际HTTP读取15秒；选中项提升队列优先级／合并同对象请求；进程内128对象／8MiB LRU，单原文超过256KiB不缓存。服务、凭据内容摘要、策略revision、官方对象和版本参与隔离；无版本不跨目录代次复用；不落盘正文。
- `reading_loading.go`和共享接线：目录先渲染，当前页最多30、最近知识最多8自动补标题；稳定对象key、不重排／不换滚动状态；切页／项目／查询取消并双重检查迟到回执。返回项目先重读目录确认版本；选中正文重核权限。401、403、404及身份变更清除对应缓存和阅读／复制，超时保留同版本已确认正文并显示失败。身份指纹每2秒随UI检查，不调用网络。
- 技术详情按对象保存折叠状态，原始ID和原文复制留在其中；主复制仅VisibleBody。来源使用可靠session_date及真实ID，原始记录标题可用真实QA首句，不编造标题。
- 本轮未改dev.py、组件输入、日常配置、官方插件或数据。PA的独立后端／patch属于另一session，未纳入阅读提交。

### 验收矩阵

| 要求 | 当前证据 | 结论与边界 |
| --- | --- | --- |
| RU01–RU02、RU07 / RU-B | ReadingTitlePriorityAndUnicode、StrictWrapperAndRaw、SearchSnippetRemainsPartial；真实3条只读 | 通过；不翻译不摘要、不调用模型 |
| RU03–RU04 / RU-A | ReadingPaneAutomaticBoundedAndContinuous、真实Native3行无需点击补标题 | 通过；不改变计数／顺序／滚动 |
| RU05–RU06 | StrictWrapperAndRaw；真实3条recognized；实际展开／折叠技术详情 | 通过；未知／冲突原文保持 |
| RU08–RU09 / RU-E | Native来源／返回、两种复制及ID在本地输入框读回；NestedOfficialSourceReturnsToOriginalSearch | 普通知识Native通过；搜索两层返回仅HTTP夹具 |
| RU10 / RU-D | ReadingLateReplyDoesNotCrossPaneOrRefillCache、既有LateResultsDoNotOverwriteSwitchedProject；Native刷新／离开重入 | 通过对应边界；真实库只一个项目、一个知识页，不造数据；跨项目及分页用夹具 |
| RU11 / RU-C | MergeCancelTimeoutAndDenied、CredentialRotationRejectsInflightRaw、TimeoutKeepsConfirmedBody；Native真实500错误 | 拒绝、超时、代次失效按HTTP夹具通过；未改变真实凭据／ACL造拒绝 |
| 有界缓存／版本 / RU-F | SchedulerConcurrencyDedupAndCache、CredentialAndInvalidationBounds、VersionlessRefreshAndOversize；30行HTTP夹具 | 通过；目录请求31、只发30个本页raw；真实小库不外推为1000条实窗性能 |
| RU12 / RU-G | 最近知识Native一致；搜索片段和两层返回HTTP夹具通过 | **真实搜索Native未通过**：一次请求HTTP500，阻塞搜索→知识→会话→两层返回 |

源码检查：`sh scripts/go.sh test ./...`、`sh scripts/go.sh vet ./...`和`CGO_ENABLED=1 sh scripts/go.sh test -race ./internal/simpledesktop`最终均退出0；包含最后补充的超时保留正文和请求中凭据轮换断言。证据JSON解析及diff空白检查通过。

真实只读命令：`MEMLANE_READING_CONFIG=/Users/bytedance/work/AI/memlane/.local/daily/runtime.json sh scripts/go.sh test ./internal/simpledesktop -run '^TestReadingOfficialReadonly$' -v`退出0，仅目录与raw；[脱敏回执](evidence/reading-ui-20261113/official-readonly-check.json)。[Native操作摘要](evidence/reading-ui-20261113/native-check.json)区分已实际观察与未完成；截图实际查看但未另存，不声称独立图片已入Git。

### 阻塞与计数

- RU-G原标准：真实全局搜索片段→完整知识→来源会话→逐层返回，保留片段／查询／滚动。2026-11-13约12:18本地的一次X-Api-Key查询返回official HTTP500；总览随后显示搜索失败、连接管道断开。没有自动重发或改用索引／模型。
- 与总览已记录OVR01一致的依赖现象：当前长期后台继承stderr失效，图worker启动前flush失败。此次HTTP500与总览失败摘要是新证据；本轮未独立读取该次worker完整栈，不把所有500直接断言为同一根因。原OVR01证据见overview-execution-20261113.md。
- 本轮真实搜索验收失败1轮；不是E12标题溢出或E16返回丢片段的新失败，二者历史累计仍各1。未达到同一问题第4次停止阈值，也不为凑次数重试。
- 保留全部官方数据、原活动终态和现有运行依赖；没有后台重启、业务重放或raw落盘。纯测试有一次等待helper自身判据消费回调后仍等队列非空，改为直接执行收到的回调；属于夹具方法问题，不计产品失败。
- 当前附件明确“不修改scripts/dev.py、不擅自启停官方服务”，故不在阅读范围内修复继承日志／重启后台。需由用户或持有运行窗口的获授权session处理该依赖，随后只补一次真实RU-G；在此之前Goal保持未完成。

## 权限／身份接线交接

- 仅在UI线程调用 `App.InvalidateReading(datasetID)`：清该数据组缓存／正文／返回历史及复制入口；空字符串清全部身份。撤权事件应先调用，不等待新HTTP请求失败。
- 同URL／同凭据下策略变更调用 `App.SetReadingPolicyRevision(revision)`，将revision纳入命名空间并撤下旧阅读状态。凭据文件原地轮换、服务URL改变也清理，不以文件路径充当凭据代次。
- 标题仍存Item.Title，原文始终Item.Body，是否已取得完整文档由BodyLoaded区分；可见正文使用itemPresentation，不复用搜索Preview为全文缓存。
- 阅读接线和必要Native自验已结束；正式源码版本及共享app.go/client.go/overview_ui.go释放回执随提交发送PA。后续如需修改共享文件须重新取得窗口。

### 2026-11-13 12:25 用户决策与正式释放

- 阅读实现提交为 `62b324f`；已向PA session `01a1d316-954a-7c91-8191-7fdfe3d4a8c9` 发送版本、文件清单、失效接口和app.go/client.go/overview_ui.go及Native窗口的明确释放回执。
- 用户回复：“由其他 session 修复后台后再补验”。本session不修改后台／dev.py、不发重启、不重发搜索或业务任务；当前Goal继续未完成，等待外部恢复证据，不自动改为complete或paused。
- 下一次补验前先取得后台修复版本、原数据根与身份保持、服务所有权／健康及搜索恢复回执，并重新协调共享文件／Native窗口。只补RU-G真实搜索→片段→知识→会话→逐层返回及受修复影响的阅读检查；既有源码、真实raw和Native证据在版本条件成立时复用。
