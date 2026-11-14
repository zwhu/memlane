# 设置 Native 重构工程账本

## 基线与边界

- 用户附件授权：仅设置八页及对应状态；环境／连接／处理正文复用三步初始化。历史暂停Goal不恢复。
- 工作树：`/Users/bytedance/work/AI/memlane-settings-20261112`；分支 `settings/native-redesign-20261112`。
- 源快照：`7bf679da46b4b80e32e25b6638e197c37beaeac1`，父提交 `efc1b49`。1424文件，49,613,064字节；逐文件SHA256见 `settings-source-snapshot-20261112.json`。从原工作树读取并逐文件写入Git对象，未使用原index，保留原工作区修改。仅纳入源码／文档／设计资源，不含.local/.cache/.mygo/build/output及真实数据、运行组件、本机配置。
- 设计：`prototype/selected-ui` 固定 `c84f75ce4258fb122eb7574e1bd89c64ef7ba714` 的 `prototypes/non-project-20261112/`。用户本轮授权覆盖交接中“候选待认可”的设置部分；其他页面不由此获得实施授权。
- 已读AGENTS、product、design、README、非项目交接、复用契约、Agent连接、记忆处理、动效、workflow及acceptance。已打开环境／处理／服务高清图和Chrome访客本地交互原型，实际切换环境→处理；继续逐页核对。
- 另一session `01a1cbe2-ce07-7231-8d6f-eb210b7a6cf0` 当前推进会话阅读及官方数据；最新桌面回归29.962秒通过，独立GUI构建。共享app.go/design.go/main.go仅本树最小变更，集成前须重新核对。
- 不停止／重启／替换原App、daemon76077、官方服务76867、helper或worker，不写真实Agent配置。独立工具链、模块及编译缓存使用APFS复制；测试临时根由本树task.py拥有。
- 发现disk-space.py的finish会扫全局memlane临时根：本树删除该扫描，保留磁盘门禁与本树缓存管理；不得清理其他session。

## 连续主流程用例（先于实现）

所有用例使用隔离合成根／测试临时目录；普通1200×820、最小800×620。源码与模拟组件证据不替代Native。

| ID／场景 | 连续流程及分支 | 真实能力／所有者 | 文件与集成点 |
| --- | --- | --- | --- |
| S01／35–37 | 设置→环境；就绪复用→重新检查；缺失→准备→下载／校验；取消保留→失败重试；刷新不闪空 | environment/runtime/firstLaunch同一控制器，EnvironmentItem/Components回执 | environment_presentation.go；初始化复用 |
| S02／38–41 | 多选→批量安装→逐项重载／验证；部分成功→仅失败重试；取消保留成功→详情→撤除影响确认 | AgentConnectionBatch持久任务、agents安装器；官方采集迁移由另一session负责 | agent_connection_presentation.go/agents.go；不新增采集器 |
| S03／42–47 | 三页签草稿→保存不启用→验证／取消／失败→明确启用；条件变化禁用；切页保留草稿 | ProcessingConfiguration/validation IPC、已有Provider/Agent/Magpie路径 | processing_settings.go/processing_http_presentation.go；共享初始化 |
| S04／48–50、65 | 运行→停止影响→取消／确认→真实停止回执→恢复失败／后台许可→明确启动；窗口收起与退出偏好 | RuntimeController/现有窗口回调 | maintenance_settings.go；不调用系统注册 |
| S05／51–56 | 列表→创建／取消→校验失败→预览→执行→恢复后停止、连接重验、处理及计划明确启用 | BackupClient/restore现有能力；官方数据覆盖需列缺口 | backups.go/backup_restore.go；不迁移数据 |
| S06／57–58 | 索引状态→范围→确认重建→停止后续→失败重试 | index rebuild现有IPC；仅覆盖现有引擎能力 | index_rebuild.go；不另建索引 |
| S07／59–60 | 检查→更新可用→下载→校验／失败→真实回执；无发布源明确不可用 | mygo.Updater已有回调 | maintenance_updates.go/updates.go；不新增发布源 |
| S08／62 | 关于→真实版本／环境摘要→按需技术信息→诊断 | 已有诊断／运行状态 | maintenance页面；仅设置正文 |
| S09／61、73–74、63 | 存储不可写→保留草稿→重试；首次加载与刷新保留有效结果；长页末尾／Tab／焦点／返回 | 既有存储错误及各页草稿状态 | 设置专用Key与Scroll；不修改阅读刷新 |
| S10／66–70、75 | 三步初始化重复使用环境／连接／处理；无项目／首任务门槛；完成进入首页 | firstLaunch与既有配置所有者 | 仅共享正文改动；初始化视觉不重做 |

## 问题计数

按具体触发、现象与验收标准累计；原失败不重置。已有阅读刷新3轮、helper／图谱停止均不属于本轮实现范围，不重跑。设置本轮尚未执行验收，不虚报0次历史失败；现有设置检查记录继续核对。

## 实施与结果

进行中。未完成Native对照及真实环境集成，不宣称交付通过。

### 首轮集中开发

- 新增 settings_layout.go/settings_environment.go/settings_agent_detail.go；统一八项侧栏与宽幅正文，最小窗160px，普通196px；app.go仅设置toolbar分支委托。没有改design.go、窗口主入口或阅读逻辑。
- 环境改为Cognee引擎／原生运行依赖／本地向量模型；保留实际组件映射与进度，分列包测量／额外下载／占用，未知不填0。修复设置环境刷新失败时丢弃已有组件快照，保留失败提示。初始化继续使用同一正文。
- 处理页三个单行页签、状态栏右侧明确启用、固定底栏；Agent下拉与当前模型，沿用服务账本与校验门禁。
- 服务纳入窗口后台偏好；更新／关于／索引按新卡片层级；备份与恢复明确当前格式只覆盖Memlane本地业务数据，不宣称官方独立库整库恢复。
- Agent详情呈现逐客户端三项待验证与共享撤除影响确认；保持原协议验证，既有采集状态与官方迁移由其他session衔接，不新建采集路径。

### 检查与失败计数（证据不覆盖）

- `build/settings-directed-001.log`：编译成功，旧用例查“向量模型”失败，已按最新设计改为“本地向量模型”；属于过时标签夹具，1轮，未算产品视觉失败。
- `build/settings-directed-002.log`：800px组件未堆叠（实际可用568px，阈值边界错误），具体问题SET-LAYOUT-001第1轮失败，已将阈值修为三卡每项至少200px；服务旧“本机后台服务”锚点检查误用新卡片子标题，夹具改以页面说明锚定顶部，方法错误单列1轮。
- `build/settings-desktop-003.log`：完整desktop29.387s失败。上述窄窗与旧锚点在该输入尚未修复；不同命令再次验收仍累计，SET-LAYOUT-001累计2轮失败，旧锚点方法累计2轮失败；另有旧页签／返回／恢复标题断言不适用（保留草稿／授权／保存启用等行为断言），更新命名。真实回归SET-AUTH-001：连接列表丢失无授权提示1轮；SET-LEGACY-001：旧连接管理层级未呈现1轮，均已补回真实状态。所有失败日志保留。
- `build/settings-desktop-004.log`：31.962s退出1。窄窗／授权／旧连接已通过；新发现普通窗口卡片高度不同导致标题垂直偏移，SET-LAYOUT-002独立问题第1轮，已统一横排拉伸；历史恢复页仍查旧标题，更新该方法断言，保留“不劫持列表”行为检查。截图输出为headless模拟，不是Native。未达到任何同问题第4次停止条件，后续沿各问题计数。

### 定向通过与首次Native观察

- `settings-directed-005.log`：desktop定向1.677s通过，含卡片布局、服务footer、备份恢复确认、旧连接管理、初始化处理与秘密遮蔽。SET-LAYOUT-001（窄窗）2次历史失败后通过；SET-LAYOUT-002（宽窗卡片标题）1次失败后通过；授权和旧连接回归已通过。
- `settings-native-build-001.log`：独立e2e GUI构建通过；`.local/settings-native-001/source-info.json`含synthetic_only、输入与二进制散列。运行独立HOME／CODEX_HOME／PI目录，没有runtime-config，无运行服务连接。
- CUA真实800×620：实际观察环境首屏与滚动末尾、固定底栏、处理页／Magpie禁用启用与未接服务状态。发现导航品牌过低／主导航居中，与稿不符，SET-VISUAL-001首次未通过；已压缩品牌顶栏并左对齐。成功滚动且模型／存储末尾可达。
- 首次Native Agent页被main的默认ConfigureAgents重新发现覆盖合成输入；HOME为本树专用目录，未写用户配置。记录为FIXTURE-001第1轮方法问题，未用该画面验证批量结果。为e2e增加静态设置夹具开关；普通构建恒false，仅本树main在此模式跳过默认发现。此为共享入口必要小补丁。
- 窗口经Cmd+Q退出，CUA inventory二次确认该bundle不在运行；原App／服务未操作。Native图像当前留在CUA回执；后续开启既有本进程截图菜单保存可交付文件。

### 普通窗口证据与Provider纠正

- 全量 `settings-all-test-006.log`：desktop36.040s、daemon38.592s、official4.323s等通过，整体退出1，仅Agent hook测试 `TestCodexCaptureModifiedHookIsPreservedAndConsentRevokedBeforeCleanup` 报 `capture_bridge_unavailable`。`internal/agents` 本轮无diff；不越界修另一session采集。`settings-all-vet-006.log`退出0。
- `settings-navigation-007.log`：0.906s通过，八入口连续导航／三方式草稿保留与不启用／共享撤除需确认可取消。使用独立HOME及Agent目录。
- Native008启动失败：GUI包在前一次task收尾时被清理，重建只有可执行文件，没有Info.plist，系统通知初始化拒绝bundle身份。FIXTURE-PACKAGE-001方法失败1次；补回plist，并在本树磁盘保护中登记唯一保留GUI包后，009相同二进制启动成功。没有触及原运行包。
- Native009：真实1200×820窗口，共11张本进程NSWindow PNG＋同名JSON回执，归档 `docs/images/ui-redesign/settings-native-20261112/`。覆盖八设置页、Agent详情、Provider输入；图片是实际窗口，不是headless或浏览器。原理与输入散列见source-info-1200.json。
- 中文输入未通过：CUA typeText“合成草稿-model”只形成“-model”；随后粘贴未改变值。SET-INPUT-001累计2次失败，原因未明（产品输入链／CUA事件方法均待核对），未宣称中文IME通过。AX setValue英文成功，Tab焦点转至供应商配置；英文草稿切Magpie再返回保留，当前使用与代次未切换。AX写值不能代替键盘／输入法全验。
- 对照43稿发现自配LLM仍沿旧模型目录主布局，SET-PROVIDER-VISUAL-001第1轮未通过。已改为供应商／地址／Key／模型主表单，目录作为按需入口，保持原真实凭据、模型读取、手动ID、保存和验证接口。供应商草稿切换保留；编辑地址／凭据或Magpie地址会禁用旧验证启用。
- `settings-provider-010.log`：相关processing/settings/初始化18.542s通过。`settings-desktop-011.log`：新视图开关字段遗漏导致编译失败，SET-PROVIDER-BUILD-001第1轮；补字段后012完整desktop重验。失败均保留，没有以更换包重置计数。

### Provider与隔离环境回归

- 012完整desktop30.914s失败：两项旧Agent夹具假设CODEX_HOME不存在，但本轮显式隔离目录使Codex具备电脑级目录，因此被误当“不支持卡片”。FIXTURE-CODEX-DIRECTORY-001方法1轮；仅在两个“无目录”用例显式清空CODEX_HOME，其他电脑级用例仍使用各自临时目录，未修改产品门禁。
- 013完整desktop30.924s通过，013全仓vet退出0；新Provider门禁与供应商草稿保留检查通过。
- 进一步核对取消按钮：Provider地址／协议改动也计草稿；取消恢复已保存Spec并清除该方式内存草稿与临时Key；凭据保存进行中不允许取消改写。014定向processing/settings/初始化作为变更后验证。

## 接口缺口与真实能力边界

| 设计范围 | 已复用的真实接口 | 当前缺口／待集成验证 |
| --- | --- | --- |
| 环境与组件 | RuntimeController、DiscoverEnvironment、PreparationReceipt、prepare/cancel | 主客户端包测量未从窗口配置提供，显示未知；首次全新下载及官方迁移后资源映射须原环境验收 |
| Agent连接 | AgentConnectionBatch及各项状态／retry/verify/cancel，已有安装与撤除 | 官方插件安装迁移属于另一session；现有安装器不因UI文案变成新官方实现。缺少逐客户端能力IPC，CLI/Desktop/IDE各项保持待验证，不用品牌总状态替代 |
| 记忆处理 | credential save、supplier list、model catalog、processing ledger save/validate/cancel/activate | 各Provider/Codex/Magpie真实推理与付费调用未在本轮启动；既有IPC夹具与门禁通过不代表真实模型全矩阵 |
| 服务与窗口 | RuntimeController/availability、原窗口hide/quit/stop回调、keepService | 本轮未重启／替换真实helper，不能解除原生命周期停止问题 |
| 备份恢复 | BackupClient list/create/cancel/verify、RestoreManager及旧授权退休 | 当前快照是Memlane本地业务数据，未覆盖Cognee独立官方库。UI明确范围；不能以旧快照宣称完整官方记忆可恢复 |
| 检索索引 | IndexRebuild start/retry/cancel/status | 无官方全库“保存／可检索／待处理”汇总字段，保留任务真实计数，未从当前分页列表捏造全库数字；官方索引迁移由其所有者提供 |
| 客户端更新 | mygo.Updater现有状态／下载／取消／安装回调 | 未配置正式源时明确不可用，本轮未安装更新、签名迁移或发布 |
| 关于诊断 | version、runtime summary、已有诊断／复制入口 | 诊断不复制正文、密钥、服务地址；实际后台错误继续由原环境回执判定 |

## 原目录集成顺序（尚未执行）

1. 重新读取另一session最新状态和原目录diff；不得cherry-pick整个源快照覆盖原成果。
2. 以`7bf679d`为本轮源基线，只应用本轮设置差异；settings专用文件与app.go/main.go最小共享片段分开审阅。app.go只新增视图滚动状态及委托设置toolbar；design.go不改；主入口只加入e2e合成设置fixture避免自动发现覆盖，普通构建恒false。
3. 原目录app.go、main.go、visual_fixture.go是高冲突位置；逐hunk合并，保留会话／知识／官方刷新修改。源快照中的其他session修改是输入，不是本轮待集成补丁。
4. 原环境拥有者串行执行合并后test/vet、普通构建不含e2e探针、1200×820及800×620设置＋初始化回归，再接真实配置／服务／Agent／备份检查；本轮隔离GUI不能替代该验收。
5. 开发磁盘脚本的本树隔离修正单独交付，不自动覆盖原脚本；100 GiB门禁持续生效。只清本轮私有再生载荷；保留一个最新可运行GUI、截图、回执、日志。

### Native015/016交互核对及归类纠正

- 015：最终Provider表单版本800×620实窗，12张PNG/JSON，含八设置页、环境滚动末尾、Provider与Magpie；source-info-800.json记录签名后二进制SHA256。普通1200历史009散列是签名前值，已明确标记pre_sign，不冒充签名后完整包身份。
- 016同一二进制1200×820 Provider：`reproSupplierPopup()`使用新鲜完整AX树，点击“选择供应商配置”后断言DeepSeek出现，实际popover列出全部供应商；点击DeepSeek后地址变为模板地址、密码框启用。没有修改控件源码。此前增量AX“无变化”或直接setValue到popup不能证明弹层未出现／选择失败，保留两个观察失败并纠正为方法边界，不计作已证产品缺陷。
- 地址框CUA AX click后键入未改变值且Tab转至总览：未建立文本焦点，新增输入观察失败1轮。随后setValue收到焦点明确回执，再执行`typeText('-typed')`并断言地址完整值通过；`paste('-中文')`并断言完整Unicode通过；Tab焦点明确到secure text field，切Magpie→自配LLM地址草稿完整保留。聚焦前失败共3轮，均保留，不能用AX写值替代键入；后两次为实际键入／粘贴回执。原中文直接typeText及系统IME候选组合未单独证明。
- diagnosing-bugs技能用于定点核验：实际反馈循环在完整AX观察及明确焦点前置后为绿色，未复现需要源码修复的下拉缺陷；因此未进入根因假设／产品修复阶段。没有改MyGo依赖或扩大输入系统工程。
- 最后批量统一维护页底栏主操作靠左、Agent卡片圆角，并在关于页复用真实“检查更新”；018前源码冻结，最后desktop/vet见017日志。此后不再逐项打包。

### 最终对照追加的明确差异

- 018/019：同一个签名后GUI在1200×820与800×620各保存9张NSWindow截图，回执准确记录两组窗口边界；18张合成实际截图已归档final/并逐文件散列。desktop最终全仓执行中37.762s通过，整体仍退出1，Agent包这轮失败为`TestCodexCaptureHookInstallationPreservesOtherHooksAndRemovesOwnedItems`、同一`capture_bridge_unavailable`原因码，未更改该模块。
- 实际打开最终窄窗Agent、服务、备份、索引、更新、关于、Provider并对照设计后，补记SET-AGENT-CARD-001：旧大图标／顶部方框与顶部辅助行造成卡片标题与控件偏离。回看009、015、018/019三轮均存在这同一偏差（018/019属于同一冻结版本的双尺寸验收，不能拆为两轮），此前未及时登记；保留原图并补记累计3轮。现已删除旧图标行、状态与方框移到底部、辅助操作移至固定footer；下一轮若仍未匹配即第4次停止，不自动第5次。
- 索引页`ready`直接展示是另一个具体文案问题SET-INDEX-LABEL-001首次发现；已仅在设置展示层映射为中文，不动服务状态。
- 017后新增上述两项定向展示变更，020为最终desktop/vet回归；不把旧018图片当作新Agent卡片已通过。

### 最终结果与交付边界

- 020最终desktop30.886s退出0、020全仓vet退出0。021最终全仓test执行：desktop36.718s、daemon40.890s、official4.118s、processing12.403s及其余包通过；只有未改动Agent模块的hook检查报`capture_bridge_unavailable`，整体退出1，不称全仓绿。三次全仓同前置问题均保留，不继续为此重跑或改采集实现。
- 最终单一GUI构建021退出0，codesign校验通过。021/022同签名二进制在普通及最小窗口均保存9张实际NSWindow PNG/JSON，共18张，实际Agent卡片标题、底部状态与多选框、批量footer在两尺寸对照通过：SET-AGENT-CARD-001第4轮通过，累计3次失败记录保留，不触发第四次失败停止。
- 当前图索引 `docs/images/ui-redesign/settings-native-20261112/README.md`，最终图final/；上一冻结输入18/19原样移至historical-018-019，不覆盖失败。source-info在签名后取二进制SHA，所有图带窗口真实边界与synthetic_only。
- 独立GUI约31.8 MiB（32536 KiB，非完整客户端分发包）；本次未下载引擎／模型，下载量不冒充首次准备量；最终产品组件占用及轻量完整主包尚需原运行环境实测。磁盘当前余量约133 GiB。
- 原daemon76077及官方服务76867仍在；所有本轮GUI已退出，未注册／替换系统服务。保留一个最新GUI与小型报告／截图，既有原工作区未改动。
- 交付的是设置UI候选及隔离自验，不是已集成到用户当前App。官方采集／逐客户端能力账本／官方数据完整备份／索引统计接口缺口已明确；真实环境、真实模型、中文IME组合及合并后完整初始化Native必须由原环境session串行复验，不能被本轮合成截图替代。

### 最后保存配置读取检查

- 审阅Provider首次加载发现：供应商元数据未返回前，默认模板可能覆盖已保存的自定义地址／协议。先新增`TestSettingsSavedProviderPreservesCustomAddressBeforeSupplierRefresh`，023红测试准确复现（模板地址及openai-chat替换了saved-custom及openai-responses）；这是复现基线，不是修复后验收失败。
- 修复仅在初次选择时，优先复用当前已保存处理Spec的地址／协议，等待供应商元数据；不复制密钥、不写新配置、不切换生效方式。024完整desktop及vet作为最终小修验证；不再触碰采集模块重跑其已知失败。

### 最终冻结回执

- 024 desktop30.989s通过、024 vet通过；025 GUI构建通过。026/027为该最终签名后二进制的1200×820、800×620九场景，共18张实际截图；已核对两份source-info全部GUI源码散列与当前工作树一致、二进制SHA与当前包一致。capture-index显式保留原图片名与交付名对应关系。
- 最新索引仍为截图README的final/；18/19失败版本仍保留在historical-018-019。021/022用于Agent第4轮修复通过的历史回执，026/027是后续保存配置小修后重复同一静态场景的最终归档，不另称第5次修复尝试。
- 未新增／修改项目阅读、官方采集、知识迁移、全局搜索实现。设置搜索按钮接既有全局搜索入口；设计中的⌘K全局快捷键原主窗口尚未实现，需共享入口所有者补齐，不宣称本轮已支持键盘快捷搜索。
- 全量结果机器索引：`settings-evidence-20261112/results.json`；失败与成功原始日志均归档，普通构建不包含e2e夹具。所有状态矩阵的真实运行环境验收仍见上方缺口和串行清单。

## 连续用例最终判定

下表的“源码通过”来自024完整desktop（30.989s、退出0）；Native只覆盖明确列出的实际动作／画面，不扩大到真实服务或模型。各设计状态的后端缺口仍按接口表交接。

| 用例 | 已取得的结果与证据 | 尚未证明的边界 |
| --- | --- | --- |
| S01 环境 | 源码：同页卡片、取消迟到ready不完成、失败保留实际下载字节、剩余宽度布局通过；Native双尺寸三卡／状态／固定footer及015滚动末尾可达 | 全新组件真实下载／校验／重试、后台刷新连续像素检查 |
| S02 连接 | 源码：批量选择、成功项排除、失败重试、取消／持久intent、旧连接不扩大授权及撤除前确认通过；Native双尺寸卡片、详情逐客户端未知、固定footer通过 | 官方安装迁移、真实各客户端采集、真实共享撤除与重载 |
| S03 处理 | 源码：私有IPC保存／验证／明确启用、daemon取消、旧验证失效、失败回执保留、供应商Key遮蔽、地址改动与草稿取消门禁通过；Native选择DeepSeek、实际ASCII键入／中文粘贴／Tab／跨方式保留及双尺寸页面通过 | 真实付费模型调用；中文IME组合；模型目录嵌套滚动的完整Native操作矩阵 |
| S04 服务 | 源码：先确认、取消不停止、等待真实回执、主动停止及恢复状态、窗口回调通过；Native双尺寸状态与后台偏好、footer位置通过 | 原用户helper／LaunchAgent生命周期、系统权限操作 |
| S05 备份 | 源码：临时本地库的创建／取消终态、校验失败、恢复预览取消、确认／回执、历史恢复不劫持目录通过；Native双尺寸列表及实际范围说明通过 | 官方Cognee独立数据完整备份／恢复；当前环境实际恢复 |
| S06 索引 | 源码：取消版本与失败区分、重试与footer可达通过；Native双尺寸范围、真实任务状态缺省及中文服务标签通过 | 官方全库索引汇总；真实重建流程；不从当前列表捏造总量 |
| S07 更新 | 源码：下载取消请求等待终态、校验安装时禁用取消、固定footer通过；Native双尺寸真实能力未接源状态通过 | 正式更新源、更新替换及签名生命周期 |
| S08 关于 | 源码：诊断不包含配置／正文载荷、按需展开技术ID通过；Native双尺寸版本／摘要／检查更新入口通过 | 当前真实环境诊断与复制内容的额外Native核对 |
| S09 通用状态 | 源码：TestN12TypedStorageFailureRetainsDraftAndOriginalRequest、Socket权限不冒充存储失败、处理初读与失败不冒充未配置、设置跨页草稿／生效配置保持通过；Native焦点／文本／页签见016回执 | 存储不可写实际窗口、长时间后台刷新／滚动连续观察 |
| S10 初始化 | 源码：三步推进、不重建项目／首任务、处理账本复用、未明确启用不能完成、持久步骤恢复及取消通过 | 本轮未重做整套初始化视觉；合并后普通／最小Native全流程须原环境串行验收 |

本轮完成隔离设置UI候选、自验材料和交接；没有将这些未证明项标记为当前真实App已交付。
