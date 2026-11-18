> 最终状态：本轮dev首次使用与净室主流程已通过，详见[结构化验收报告](cleanroom-acceptance-report-20261118.md)。下文按各阶段保留，不将早期未完成描述当作最终状态。

# 首次安装与环境准备执行账本

2026-11-18，承接用户粘贴任务及 `local-prepare-20261117.md`。本轮已授权实施和 dev 验证；新增软件、系统镜像与缺失组件仍须先展示计划并取得确认。不恢复旧全量 Goal，不进入 release。

## 2026-11-18 净室容量结论纠正

App原错误继承开发55GiB限制，已按[门禁纠正记录](cleanroom-disk-policy-20261118.md)修正；宿主开发task.py 50/55不变。净室新实测仍32GiB可用，现已实际安装uv及Python并继续其余依赖，不能再以不足55GiB作为App安装阻断。原128GiB中68.7GB未分配及工作卷3轮失败仍保留，未执行第4轮分区方案。后续路径空格和uv探测指纹两个独立安装缺陷、回归及候选身份见同一记录。

## 范围与依赖

连续流程：Native 无 Python 启动 → 检测兼容依赖／手动路径 → 展示缺项用途、版本、来源、下载量、占用和位置 → 用户批量确认或拒绝 → 持久化分步安装／取消／重试 → 本地能力检测 → 官方存储初始化与身份恢复 → 连接 Agent → 配置记忆处理 → 重启后保持结果。

| 要求 | 实施位置与关键未知 | 完成所需证据 | 当前状态 |
| --- | --- | --- | --- |
| 原生引导与依赖复用 | Go bootstrap；已有 Python、uv、固定 Cognee 源码、锁定库和模型；仅版本号不足以证明兼容 | 无下载检测、兼容／不兼容／手动路径实测 | 开始实施 |
| 确认与恢复 | 计划摘要、原子 journal、进程锁、取消与重新检测；计划改变重新确认 | 拒绝无写入／下载、分步中断恢复与重启 | 待实施 |
| 固定引擎 | `coding-memory-components.json` 的 ec6f408 与哈希、requirements.lock、既有适配 | 固定源码和能力检测，不以 PyPI 版本替代 | 旧准备器仅部分实现 |
| Native UI | 复用设置／初始化，三步保持；安装失败保留状态、重试／Agent排查／详情 | 实际外接屏窗口、操作回执 | 待实施；先与布局会话交接入口 |
| Agent 排查 | Go 生成脱敏材料与完整 prompt，复制不依赖服务；直连需验证宿主接口 | 无 Python/Cognee 可导出，修复后安装器重检 | 待实施 |
| 官方初始化 | 先核对固定版迁移、owner/API key 幂等和半完成恢复 | 合成材料真实链、宿主唯一日常库保持 | 待验证 |
| 净室 | Apple Silicon 真 macOS VM，新增下载待确认 | 无开发工具与依赖的真实首次安装及故障矩阵 | 已盘点，下载未授权 |

## 连续验收

全新环境；已有兼容依赖直接复用；不兼容说明原因并允许换路径；拒绝保留进度；网络失败不报完成；取消后继续；Agent 诊断无需引擎就绪且文字不能当成功；安装后重启重新核对。源码夹具、宿主链、净室、Native 和正式包分别报告；本轮不构建正式包。

先实现无 Python 的安装控制与诊断，再接实际准备动作和 Native 页面，最后集中验证矩阵。所有安装动作必须从经确认的冻结计划执行，不能在探测时隐式下载。现有 `local_prepare.py` 不能直接充当新的完整安装器。

## 所有权与现场

- 保留其他会话全部未提交修改。布局会话 `01a1e887-f8af-70d3-9058-b8c05a6130a3` 仍 active；已发送源码／窗口所有权核对，暂不改它的布局文件，不启停 Native watch 或日常服务。
- 旧准备会话 `01a1e72b-6a9b-79f3-b80b-e561f182fda5` 已核实 notLoaded，最后 turn interrupted；原准备命令 terminal、现场无 local_prepare 进程。遗留公共锁元数据归档 `.local/first-install-20261118/previous-lock-owner.json` 后登记本轮范围。
- 日常服务 PID14559、14629 和 Native watch PID92813/92849/92865 在读时存在，不据此认定全主链健康或允许清理依赖。磁盘约132 GiB；门禁保持50/55 GiB。
- 常见入口未发现 UTM／VirtualBuddy／Parallels／VMware／tart／qemu，未发现已登记的常用 VM 根。UTM 官方 latest API 当前 v5.0.9，DMG304702085字节（约291MiB）。拟使用 Apple 官方恢复镜像，不挂载真实数据／凭证，下载前向用户确认；系统镜像15–20GiB只是预算，实际URL／大小需下载前核实。
- `INIT-PREPARE01` 原1轮未闭环；`PREP-DEPS01` 原1轮wheel限制失败保留；后续网络不可达为已有证据，不自动重投。旧停止实验不恢复。

## 正式发布前必查（本轮不升级）

- 重新核对 Cognee PR #6568 状态、正式 GitHub release 与 PyPI 构建，确认所需能力及当前 Memlane 补丁是否已纳入；不能仅比较 `1.6.7` 字符串。
- 决定升级前验证锁定依赖、API／插件及补丁兼容性、已有数据与身份／权限迁移、部分失败恢复及回滚；保留旧库与来源关联。
- 用户明确进入 release 后另验签名、公证、分发制品与真实下载安装／更新；当前 dev 和净室结果不替代正式包验收。

## 2026-11-18 首轮源码与宿主证据

已新增 `internal/bootstrap` 与 `cmd/memlane-bootstrap`，仅为完整安装器的实施进展，不是首次安装已交付：

- 安装计划逐组件记录用途／版本／来源／摘要／下载与占用／位置，以展示计划摘要确认或拒绝；拒绝不执行安装，计划变化重新确认。原子journal、跨进程排他锁、已完成步骤重新检测、失败／取消保留结果，重启可读原批准范围；外部复用组件消失不静默改成新装。
- 准备动作运行时每秒检查资源回调，低空间取消传递给driver。真实50／55GiB策略的Native绑定尚待接入；不把可注入回调当作全部安装入口已经受管。
- 无Python的诊断JSON与完整Agent prompt，允许缺少journal及引擎；只接受安装器上下文，不扫描配置、凭证、会话或内容库；URL凭证／查询／fragment、常见key/token及用户home脱敏。尚未接Native复制／导出与宿主直接交接。
- 原生Python候选检测保留venv入口；临时HOME、cwd、缓存／日志、最小子进程环境与离线标记；核对3.12 ABI／本机架构、锁定distribution版本及ladybug/lancedb/fastembed/uvicorn加载。它不创建库、不推理、不下载。
- 固定源码driver复用已验证归档、SHA256、包树逐文件核对和安全解包；拒绝未知目录、链接、被改动源码及新模块。尚未绑定Native批准入口，不代表全Python／模型／存储安装已完成。
- `engine/first-install-components.json` 从官方GitHub release API锁定uv0.12.3和CPython3.12.12+20260211的URL／SHA256／下载字节数。只读取了metadata，没有下载或安装新软件；既有兼容uv/Python优先复用。embedding文件级manifest仍待补齐，不能显示整体准备完成。元数据来源：[uv](https://api.github.com/repos/astral-sh/uv/releases/tags/0.12.3)、[Python](https://api.github.com/repos/astral-sh/python-build-standalone/releases/tags/20260211)。

**源码检查：** 15项定向Go测试通过，覆盖拒绝／计划变化／外部组件丢失／步骤恢复／取消后不重复安装／资源运行期取消／诊断脱敏／归档及源码篡改／未知目录保护／固定依赖解析。测试使用合成归档和driver；不是净室或完整真实安装通过。bootstrap包及CLI的vet已通过，后续变动会重查。

**真实宿主：** 编译约7MiB原生dev诊断CLI，在仅有系统PATH的条件下直接运行；它自行探测已有Python，不依赖PATH上存在Python或uv。后续同源检测经task.py资源门禁运行。固定ec6f408归档与现有源码逐文件一致；日常Python3.12.12的全部锁定distribution及4个原生库探测通过，另发现已有Python3.12.13候选，系统3.9.6明确不兼容。没有下载、升级、新建内容库或写日常材料。仅修改PATH是宿主探测条件，不称净室。

私有证据在 `.local/first-install-20261118/`：`native-detection.json`、`native-detection-round2.json`、`dependency-detection.json`、`python-catalog.json`；带task门禁的stdout含前后回执，解析时读取目标JSON对象，不能假定整个文件是单一JSON。CLI暂保留供Native对接后续使用，非正式包。

**新问题与轮次：** `BOOT-SOURCE01`：真实固定归档首次校验错误报告multiple archive roots，累计失败1轮。已证根因是Go tar.Reader将GitHub全局PAX元数据返回给调用者；元数据不是文件根。修复跳过全局PAX记录并补合成回归，真实同一归档第2轮通过，原失败保留。初始Go编译unused import和macOS测试temp的`/var`链接触发保护为源码／夹具纠正，不计成INIT-PREPARE01闭环验收。旧次数不变。

**未完成：** uv／Python制品执行、依赖安装的确认接入、模型完整manifest与离线推理、官方存储和owner/key半完成恢复、Native三步安装及设置复用、失败复制/导出/直接交接、真macOS净室全矩阵与实际窗口验收。VM下载询问已发出，未收到确认前不下载。没有进入release，也没有把原准备器升级表述为完整安装器。

## 2026-11-18 续作：真实依赖计划与 Native 环境页

上段“未完成”保留为首轮时点，本段更新已实施范围，不将组件完成扩大为首次安装完成。

- 新增uv／独立Python制品driver：无需已有Python/uv即可下载、校验、解包；保留执行权限、仅接受payload内链接、拒绝未知目录。兼容uv按锁定安装能力检测并复用。未在宿主实际下载新Python/uv；新制品执行仍待净室验证。
- 运行库driver：已有解释器和锁定distribution／原生库兼容则直接复用；否则在最终路径创建独立受管venv，明确禁用额外Python下载，不同步用户环境。沿固定requirements哈希和build constraints安装；取消只终止本轮独占进程组，保留退出码及脱敏末尾日志。新增软件实际安装未在本轮宿主执行。
- 固定模型清单 `engine/embedding-components.json`：Qdrant ONNX revision `46fbe35fd4374a00fee7de77dfddaeb6dd6a2c59` 与 BAAI tokenizer revision `7999e1d3359715c523056ef9478215996d62a620`，共7文件、95660924字节。现有文件与官方revision tree的Git blob/LFS对象核对，再固定SHA256；仅读取远端metadata，没有下载模型。新增driver按已确认清单下载；已有兼容缓存只读复用。
- 原生控制器串联安装工具→Python→运行库→固定源码→模型。运行库已有可用时，uv不作为服务永久前置条件。源及模型逐项验证；模型用显式snapshot路径和离线参数编码合成短文本，核对512维、有限非零向量以及分词器可加载，不写内容库、不调用生成模型。初始资源及运行期回调均绑定55GiB运行保护线（比50GiB最低保留线提前停止）。
- `engine/bootstrap_assets.go`仅将固定清单和320444字节依赖lock嵌入原生程序；Cognee源码和模型仍是独立组件。
- Native首次无配置入口和“设置→服务与连接”复用同一环境组件：检测、冻结缺项计划、用途／版本／来源／估算下载/占用／目标位置、批量确认或拒绝、安装步骤回执、停止、重试、详情、手动已有路径、复制诊断和完整Agent提示词。不以文字回复更改成功状态；重开遇到残留installing回执显示“准备结果待核对”，不据journal冒充后台存活。

### 宿主及窗口证据

`complete-host-inventory.json`为原生CLI真实只读检测：Python、运行库、固定源码、本地模型全部available，`components_ready=true`，`downloads_performed=false`、`storage_initialized=false`。它复用同一日常运行依赖，没有复制或改写宿主唯一内容库。模型能力探测只覆盖本机现有锁定环境，不能外推干净VM。

实际外接屏 `Memlane Dev` 窗口已查看“服务与连接”，真实检测后四项显示可用，未出现补装操作；实际打开详情核对复用路径。窗口由已有watch正常热重载，未启停日常服务／watch，未连接或变更Agent。

用户随后明确反馈初版环境卡“太丑了，把文案精简，各个依赖把版本加上，有图标就用原图标”。沿该反馈把环境卡改为紧凑列表、删说明段和重复“准备完成”，顶部只留“运行环境／已就绪”，右侧放重新检测与详情；名称、版本及状态同排。原始品牌SVG本地内置并保留颜色：Python、Cognee、uv、Hugging Face模型分发来源；运行库是集合，沿用通用图标，来源见 `internal/simpledesktop/dependency_icons/README.md`。

改版后再次实际查看Native截图与AX：Python `3.12.12`；运行库 `LanceDB 0.39.0 · FastEmbed 0.9.1`；Cognee `1.6.7 · ec6f408`；向量模型 `BGE Small ZH v1.5`，四项均“可用”，官方原色图标可见，未再显示长说明和底部重复标题。视觉改动已实施并自验，用户尚未认可；该反馈记为本环境卡视觉问题1次，不能并入旧布局缺陷或重置其他问题次数。

### 检查与未完成范围

bootstrap新增合成制品、链接保护、模型复用/篡改、安装命令取消和脱敏等测试（目前22项）通过；环境UI的确认前不安装、已有依赖继续、无runtime复制排查、重启回执状态4项定向通过。实际新依赖下载/安装、网络失败/拒绝/取消全连续矩阵尚未在净室执行。

一次综合包检查中旧 `TestGenerationOperationJournalRejectsDuplicateAndDamagedMetadata` 在“unrelated profile”断言收到EAGAIN，保留1次检查失败，尚未证明根因；未改generation操作代码。后续只运行该问题与本轮环境用例的定向检查通过，不据此擦除原失败或宣称全量通过。更新后的全量结果另记。

尚未接通：全新官方存储／owner/key的幂等及半完成恢复、首次服务生命周期、三步引导实际推进到Agent与记忆处理完成、手动路径跨App重启保存、Agent宿主直连与诊断文件导出、真实净室全矩阵。组件就绪不能越过首次存储/服务门禁；`continueEnvironment`目前保留该门禁，不能描述为完整三步初始化已完成。VM新增软件和镜像下载仍等待原询问的明确确认。发布前核对清单不变，不升级Cognee、不进入release。

本轮候选冻结后的 `sh scripts/go.sh test ./...` 退出0，bootstrap及simpledesktop包均通过；此前旧journal测试EAGAIN未在该轮重现，原1次失败与原因未知继续保留。相关bootstrap、Native、CLI与engine的vet退出0；`git diff --check`通过。未构建完整包。公共动作锁继续登记本轮安装模块及明确App/Config/设置插入范围，其他会话源码与日常运行依赖保留。

## 2026-11-18 续作：路径恢复、诊断导出、首次存储适配

### 本轮实现和已证范围

- 手动路径以本机私有selection文件保存，按runtime配置位置绑定；重开重新读取并检测，空路径保留为自动检测。路径选择不授予安装权限，不改日常服务配置，不在现有内容库内写selection。读取拒绝链接／公开权限／超大状态文件；状态锁增加close-on-exec，避免安装子进程继承锁描述符。Go测试覆盖两个App实例恢复、不同配置不串用、空选择、权限／链接和日常库保护；没有为此重启共享Native或服务，实际整进程重启仍留在连续矩阵。
- 安装回执增加当前步骤、真实退出码及脱敏安装日志；无法启动的命令保留原启动错误，退出码未知不伪造为正常进程退出码。诊断包含当前检测计划，即使尚无journal／Python／Cognee也可生成。
- 原生ZIP导出含 `diagnostic.json`、`agent-prompt.md`、`README.txt`，文件0600；沿用MyGo原生保存对话框。已实际在Native“运行环境→详情→导出诊断包”选择本轮私有证据目录，观察到“诊断包已导出”，随后读回ZIP核对3个成员、完整提示词、4项检测和home路径脱敏。实物为 `.local/first-install-20261118/native-diagnostic.zip`；未导出密钥、登录文件、原文或记忆。
- “用Agent排查”复制完整提示词并列出现有Agent；可打开已安装App，CLI-only保留复制入口。没有自动发送对话或执行修复。直接会话投递的官方接口及只读／批准边界尚未逐宿主验证，当前使用“复制＋打开已有Agent／由用户粘贴”回退，不描述为自动投递或Agent已完成修复。

### 首次存储适配（已实现，真实SDK及Native连接待验）

新增 `engine/python/first_install_store.py` 和Go `Environment.InitializeStore`：显式首次操作、独占锁、组件全部复核、固定源码与模型指纹、私有资源按内容摘要绑定；有既有日常库绑定时直接拒绝新建另一套库，未知已有目录不接管。适配器引用既有 `official_service_dev.verify_source`，然后调用固定Cognee的 `run_migrations`、`get_default_user` 和官方API-key模型／构造器，未另建内容系统。

owner key在官方提交前先保存私有pending材料，未知提交恢复时按原key ID、owner及官方存储值读取核对；成功后才发布owner.token与不含密钥的store身份回执。缺少pending但发现已有key、身份冲突、迁移失败均停止，不重复生成key或覆盖原token。SDK子进程隔离host凭证／dotenv／日志／缓存，禁止模型下载；新owner不生成可登录的默认密码。目录只是SDK前置条件，不把mkdir当建表成功。

7项Python协议测试使用FakeSDK和合成临时材料：正常重入、owner／pending／key提交／token保存／身份保存5个边界中断后恢复、未知key不重建、冲突token不覆盖、迁移失败不发key、归属变化阻断、SDK环境不继承host凭证。它们没有导入或执行真实Cognee建库；不能称真实SDK或首次安装通过。

`continueEnvironment`的首次服务门禁仍保留，尚未把该适配器连到Native继续按钮，也未启动新的SDK/内容库/服务。下一关键链路为无生成模型配置时的官方服务＋管理侧启动、同一初始化/设置组件串联Agent和记忆处理、成功后重启恢复；当前旧generation_setup_only只启动模型配置API且不提供Agent连接，不能直接冒充新三步引导。

### 净室前置盘点更新

用已有Swift工具和Apple Virtualization只查询恢复镜像目录（[Apple接口说明](https://developer.apple.com/documentation/virtualization/vzmacosrestoreimage/fetchlatestsupported(completionhandler:).md)），没有下载系统镜像。第一次工具编译因版本属性名不正确退出，修正为SDK已有的operatingSystemVersion后，实际目录查询返回 `VZErrorDomain Code=10001`、`The restore image catalog failed to load`。这是目录元数据前置检查1次失败，原因未知；不算已创建VM或进行首次安装验收。日志 `.local/first-install-20261118/restore-image-metadata.log`。未自动重复目录请求。

查询结束并确认工具进程退出后，已清理本轮独占Swift module cache约220097840字节，保留小型查询源码／日志、诊断ZIP及最新原生诊断CLI。最低50／运行55GiB规则不变，现场约131GiB可用。UTM和系统镜像新增下载仍等待此前问题的明确答复，未收到同意，不下载、不安装、不接受系统许可。

本轮全量Go测试通过（bootstrap现29项）；Native环境6项行为测试包含在simpledesktop包内。7项首次存储协议测试通过；相关vet与diff检查另按当前最后回执核对。旧问题与失败计数保留，发布前正式Cognee核对清单不变。任务仍未完整交付，不构建release包。

### 用户优先级调整：先准备净室登录

用户要求先检查Codex、TraeX、pi在净室中的操作，让用户完成登录后再继续其他测试。本轮暂停其他测试；重新盘点仍未发现已安装的常用虚拟化工具或可用净室。已重新展示UTM约291MiB、Apple官方系统镜像预计15–20GiB的下载确认，未收到答复前不下载。此时没有可供用户登录的净室窗口。

宿主现有CLI只读帮助核实：Codex支持`login --device-auth`；TraeX（traecli）支持`login --sso-device`及`--sso`；本机pi文档要求交互`/login`选择供应商，或配置供应商API key。它们仅证明登录入口，尚未在guest执行，不代表净室认证成功。未读取或复制宿主凭证、未启动宿主登录、未执行模型调用。

后续只读盘点：Spotlight未返回ipsw/utm/pvm/vmwarevm，已挂载卷仅Macintosh HD；没有发现可以跳过系统安装直接登录的现成VM。现有Codex为0.169.0，pi为0.84.1，TraeX实际入口是本机arm64原生可执行文件。pi需要Node运行时，不能只拷贝一个命令链接进guest。净室准备顺序固定为：干净系统基线→安装Agent程序及所需运行时→在guest启动登录→用户完成认证→只读认证状态检查→再恢复其他测试。宿主程序文件可盘点复用，但不带入HOME、Agent配置、插件、hooks、skills或凭证；此方案未实施拷贝或安装。

### 用户确认后：开始净室下载

用户回复“确认”，批准上述UTM及macOS镜像准备。UTM固定5.0.9、304702085字节，GitHub官方release API提供SHA256 `607c3b7eb4ad856163c433232237dfa17a42bf2492d2e8d4b7971aebad2e6b17`。Apple恢复目录第二次仍返回VZErrorDomain10001（该元数据问题累计2次），未继续重复目录查询；改为发现并对Apple官方CDN做HEAD核对，选择与宿主相同的macOS26.6.2/25G83，镜像19772231540字节。镜像下载直接来自updates.cdn-apple.com，不从第三方镜像站获取。下载后仍须完整摘要与Apple Virtualization本地加载验证，未下载完的稀疏文件不能当可用镜像。

已启动task.py受管下载，单连接速度偏慢（镜像估算约一小时、UTM更慢）；验证两端Range响应后切换8连接分块、逐块校验Content-Range和大小、写入最终偏移、完成后全文件SHA256，保留50/55GiB门禁。旧单连接任务收到TERM并已观察退出143，属于主动切换下载方式，不是安装验收失败。当前仍未创建VM、未执行guest登录、未恢复其他测试。

后续纠正：查本机Apple SDK头文件发现恢复镜像加载要求`com.apple.security.virtualization` entitlement；此前直接用Swift解释器的工具未具备该项。编译并只添加该entitlement、检查签名后，第3次恢复目录查询成功，返回26.6.2/25G83及与当前下载完全相同的Apple官方CDN地址。原2次失败保留，不能归因为已证网络故障；补齐entitlement后成功是当前事实。另编写本地IPSW检查工具，等待完整下载及摘要核对后读取硬件支持条件，不对未完成文件执行验证。

UTM的32MiB块在10分钟超时后curl重试导致片段重下，已停止该受管下载并观察退出143，改用512KiB块且完成片段逐个提交；已有约28%进度时未继续改动下载参数。系统镜像32MiB块下载保持原进程。单独准备guest-tools目录：程序包874286712字节、19073个普通文件及内部相对链接，逐文件摘要清单；只有官方已装程序与依赖和本轮登录脚本，没有宿主用户目录或认证。未运行登录脚本。macOS26的Apple backend不支持UTM snapshot，干净基线需在关机后用受控的APFS克隆替代，不能把运行中磁盘副本当可恢复基线。

UTM现已完成下载（304702085字节），最终SHA与官方release API一致；hdiutil只读挂载完整CRC通过，源App的codesign深度严格检查退出0，Turing Software LLC / TeamIdentifier WDNLXAD4W8，spctl接受Notarized Developer ID。task.py中用ditto安装至`~/Applications/UTM.app`（约1.3GiB），安装后签名复核退出0，已卸载本轮DMG挂载。真实Native已打开UTM的新建界面，选Apple虚拟化/macOS、4CPU/8192MiB，停在导入IPSW步骤，不触发第二份自动下载。尚未生成虚拟磁盘或启动guest。系统镜像当时约42%，磁盘约120GiB，下载继续受task.py门禁管理。与其他会话核对外接屏已释放后才操作UTM；其日常同库重载不属于本轮VM验收。

## 用户休息后的独立推进

用户随后要求下载完成后继续处理其他工作，需要用户确认的内容留到明天。本轮据此恢复独立实现／源码及协议检查；账号登录、系统许可、本地账户新密码和新安装计划确认仍留待用户，不复制宿主认证，不擅自替用户完成这些步骤。

### 新增实现和边界

- `first_install_mode.py`只接纳已有完整SDK初始化回执、同一store_id／固定提交／owner-token绑定且active为空的新库；普通／损坏日常配置不降级为此模式。官方API保留身份、数据组及权限配置，处理／保存／WebSocket请求返回明确未配置。`official_service_dev.py`在此模式不构造假LLM profile／key；保留另一会话的capture路由和provenance派生补丁。管理侧首次模式跳过backlog／增量／工具图谱后台，日常分支保留。
- 固定官方Agent插件加入批量安装计划（1.8.0/a176248，补丁摘要进入计划身份）；逐文件及完整回执校验已有插件，缺项先确认再从固定归档准备，不改已有Agent认证／配置。新增手动插件路径，版本简写和Cognee原图标。此时只读宿主新五组件inventory全部available（含插件），`downloads_performed=false`、未新建内容库；证据`complete-host-inventory-with-plugin.json`。
- 根`runtime_assets.go`仅内嵌轻量Python/Agent适配和固定清单，不内嵌Python／Cognee／模型。解包排除test_模块，按内容摘要分代、私有写入、拒绝变更／额外导入模块，保留旧代。复用dev资源门禁的`.cache`和`build/disk-cleanup`仅存锁及小回执，不作为代码路径。
- `first_install_runtime.py`准备私有首次runtime（原端口／秘密重入不变、无默认登录密码、无生成profile），之后复用现有`dev.py --no-ui`生命周期、同一`.service-owner`、PID起始身份和正常退出。dev新增显式新库分支：先核对真实owner再启动管理侧，不写初始化完成。Go`PrepareFirstRuntime`／`StartFirstRuntime`已补源实现，拒绝缺配置的已有库和损坏旧配置；实际SDK／后台启动和Native继续按钮绑定尚待后续实现及VM验证，不能称三步已完成。

### 本轮检查

18项首次协议Python测试通过，覆盖原SDK协议7项、新模式边界5项、runtime配置／owner核对6项；只用合成文件／FakeSDK／假HTTP，不创建真实库或调用生成模型。新模式首轮测试因TMPDIR的macOS`/var`别名触发链接保护，夹具改用resolve后通过：方法问题1次，不计产品守卫失效。7项原generation_startup测试通过。bootstrap／engine／simpledesktop定向Go通过（新增资源／插件检查也通过），新增FirstRuntime检查仍按最新运行回执核对。没有以这些检查替代真实SDK、净室或Native三步。

### 后续连续链路

继续把Go首次生命周期接入Native三步：默认首次配置入口与已有损坏配置区分→已有／新库服务就绪→复用Agent列表→复用生成配置（初始化不展示全局图谱设置）→仅在验证并选next后显式启用，沿现有正常启动健康／配置摘要回执及`complete_initialization`提交。完成／故障／重开分别验，不把组件ready或保存profile当初始化完成。真实首次SDK／缺项安装计划仍在VM用户确认后执行。

## 2026-11-18 夜间收尾更新

最新停点：用户要求先 handoff，净室操作已暂停。恢复入口为 [净室交接](cleanroom-handoff-20261118.md)，其中记录公网故障、Codex 配套程序缺失、pi 已保存认证但 Flash 尚未切换，以及剪贴板安装验证未完成。下文账号、watcher等状态含历史记录，当前以该交接为准。

后续现场更新：用户已完成guest系统设置，已实际看到桌面并恢复成功。导入前xcode-select无开发目录，PATH无node/codex/traecli/traex/pi/uv，guest余量36GiB。未保存关机冷基线，不能宣称已有可还原冷基线。程序归档935861248字节，SHA256 `b2446b33d63672b9acd13ab45a37a15837ed30fb3c9d45a5f5a74bb16c7d5242`，guest实读一致，解包成功；仅VM网段、只服务该归档的临时传输服务已退出，host临时tar已清理，无host认证/skills/hooks。运行中共享目录已登记但guest未见卷，没有把登记当挂载成功。

guest首次Codex设备码登录请求授权接口失败（1轮，未取得设备码），host同域HEAD 200，guest原因未明。TraeX设备码登录返回only available in internal installations（1轮）；不伪造内部标记或复制认证来绕过。pi0.84.1启动成功；用户选择DeepSeek API key并自行推进至输入框，认证结果未核实。模拟输入会受拼音及修饰键影响，冒号/反斜线等丢失，用ABC和无修饰键命令适配；只读错误不充作安装验收。guest仍无真实SDK安装验收。

用户进一步授权准备跨机剪贴板：官方vd_agent v0.22.1-2，5352291字节，SHA256 `b9b9ac590738d26f1d734ed9c9b082e1bcdaab109189d1c04803fef97cf59714`与官方release匹配，host pkgutil签名退出0、Turing Software LLC/WDNLXAD4W8、Apple notarized。已传入guest的clipboard.pkg，guest摘要及开发者签名再次核对一致，临时传输服务退出。安装/权限批准/真实粘贴待完成，不把下载准备当可用。

### 已完成下载、真实系统安装与资源收尾

Apple IPSW已完整下载19772231540字节，SHA256为`885503b7f4b06609e9a512f2befd40f59730640a3f1233e3892d60affdd51c95`。有virtualization entitlement的本地Apple加载检查确认supported=true、26.6.2/25G83、最低2CPU/4GiB。UTM真实安装该镜像，VM UUID为`D591D80E-1C1D-4244-BD78-8C067A7EE6EE`，4CPU/8192MiB、64GiB虚拟容量、NAT、共享目录为空。窗口在外接LG HDR 4K，实际到达Setup Assistant语言页，简体中文已选；未接受系统协议、未创建本地账户／密码、未登录Apple或Agent，未传入guest-tools。

暂停操作曾长时间未返回；随后只用正常Cmd+Q退出UTM，核对manager及VM进程退出。磁盘上出现约2.2GiB的vmstate、约22GiB系统盘；恢复尚未验证，不能作为关机冷基线。宿主监测正常退出0、未触发55GiB停止；不留后台下载或VM进程。保留完整镜像、UTM及guest程序、VM和小型证据。核对下载／Swift工具无活动引用后清理本轮独占旧part、旧分块、速度探针及Swift缓存10项，逻辑约945652689字节，实际回收受稀疏文件影响，以现场free为准（约83GiB）。回执`artifact-cleanup.json`和更新后的`vm-download-state.json`位于本轮`.local`目录。

### 新增源码闭环与检查

Native继续按钮现已连接`PrepareFirstRuntime/StartFirstRuntime`，默认首次入口区别于显式已有损坏配置；重开先恢复同一owned store／步骤，再显示共享Agent／模型组件。运行时准备未确认时不允许用空配置进入后续步骤。初始化不显示全局工具图谱设置；保存／验证／显式启用沿原组件，切换期间冻结配置修改。启用记录绑定store、operation、reference、digest、revision，先确认原服务正常退出，再用同一service-owner锁启动；未知退出保留结果，不自动重投。并发App启用增加独立私有nonblocking锁，防止同时向原owner重复发停止请求。

只有正常服务健康、实际active配置与mode/model一致、持久化带时区的初始化回执均成立才退出引导。新增Go检查覆盖旧日期不能跳过setup、仅保存不能完成、active版本不符拒绝、完整回执成功、时区缺失拒绝、步骤恢复和其他store进度拒绝。上述仍属源码／协议验证，未执行真实SDK新库、净室安装或Native三步。

本轮最终`sh scripts/go.sh test ./...`、`sh scripts/go.sh vet ./...`和`git diff --check`通过；bootstrap现37项测试。使用既有`.local/cognee-python/bin/python`执行25项first_install协议测试全部通过，7项generation_startup测试通过。曾误用宿主默认Python3.9执行first_install测试，13项导入时缺tomlkit；这是测试环境选择错误1轮，未据此安装依赖或修改产品。改用已有锁定环境后通过，失败记录保留。新多App启用检查只有合成文件和mock停止命令，无实际Agent／模型调用。

### 待继续与明天确认

1. 64GiB虚拟容量扣除macOS占用后不足55GiB当前门禁。标记`VM-CAPACITY-01`为前置方案冲突、尚未运行首次安装验收（失败验收计数0）；未绕过或降低50/55GiB规则。待决定扩大虚拟容量还是明确App与开发脚本磁盘规则，不能在当前容量下反复启动安装。
2. 系统协议、本地账户新密码及Codex／TraeX／pi登录须用户完成；新增缺项安装计划仍需具体确认。未复制宿主认证、真实记忆或会话，宿主唯一日常库未新建或切换。
3. 旧附着watcher曾引用已退役的`.runtime-active-35095.json`，正常`open`被已附着锁拒绝；相关会话确认其所有权、按内核身份正常SIGTERM该watcher并回执三个UI进程均退出、锁可取得，日常owner71515及服务未停止。本线程随后用`dev.py open --watch-ui`附着当前配置，实际Native已连接。真实“设置→服务与连接”检测5项均可用：Python3.12.12、运行库LanceDB0.39.0/FastEmbed0.9.1、Cognee1.6.7/ec6f408、BGE Small ZH v1.5、插件1.8.0/a176248。实际截图确认紧凑逐行、版本同排，Python/Cognee/HuggingFace原图标，无旧长说明或底部重复完成文案；这是宿主兼容复用及实际页面核对，不代表净室三步通过。新附着watcher由本线程持有（tool session8505），保留以便明天继续。
4. 净室全新／兼容复用／不兼容／拒绝／网络失败／取消继续／Agent诊断／重启恢复连续矩阵、真实SDK及配置后真实服务链路仍未通过。不进入release，不把本次下载或源码完成描述为完整交付。
