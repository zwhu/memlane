# 首次使用与项目记忆浏览：实施检查点

2026-11-08。此记录对应本轮独立 Goal；**两部分连续真实运行验收已通过**，提交与远端以 Git 和最终交接为准。不恢复旧工程 Goal，不选择 Q1–Q4 或日常自动记忆策略。没有启动其他 Agent 开发会话。

## 最新正式包与最终验收（2026-11-09）

当前推荐包为 `build/onboarding-memory-validation/selected-final/app/darwin-arm64/MemlaneOnboardingReady.app`，独立身份 `io.github.zwhu.memlane.p0.24b79c7bfa2c`。首启已按所选 `index.html` 交互还原分阶段页面、紧凑分组、默认折叠检查详情、固定顶部/底部操作和右侧编号高亮；保留方块散落→归整→灯泡，不搬入原型数据、开关或固定成功计时。准备/验证/ready页使用小型动效标记，任务与下一步保持可见。具体HTTP错误后补回执丢失已修正，最新真实503窗口与重开/取消恢复通过。

| 必需部分 | 最新真实结果 | 权威证据 |
| --- | --- | --- |
| 三视图与完整原始记录 | 记忆/实体/关系共享项目与选择，跨视图返回同一出处；完整复制、长分页、查找及SDK引用定位通过 | `selected-final/continuous/report.json`、`after-cleanup-reopen/report.json` |
| Cognee实体/关系衔接 | 13个真实实体、14条有具体材料支持的关系，受信项目/实例/dataset读取 | `selected-final/continuous/report.json` 中graph与source_read |
| 空环境准备与首次使用 | 新空业务根、严格空Rust/E5/Cognee组件缓存，实际下载/校验/helper/完整健康/模型验证；本机与项目内官方影响分别确认 | `selected-final/before-app-empty-state.json`、`continuous/report.json` |
| 官方Agent/Cognee | Codex正常工具任务、官方结束同步、实际持久水位和session_learning，再受信导入 | 同一窗口连续报告；没有预置记忆 |
| Memlane/Rust当前版本 | 8 raw（1完整会话、1 QA、6 trace）与2 knowledge分别计数，2条知识逐条真实Rust正文/范围/具体revision通过 | 连续报告中的rust_current_versions；新包重放保持ID/版本/正文 |
| 全新正式召回 | 新exec、无resume、无首会话输入或文件答案，正式官方graph返回首会话来源与实际回答 | 连续报告中的fresh_official_recall及实际窗口 |
| 必要边界与恢复 | 最终UI真实503、下载取消/跳过/同根恢复通过；两个项目及重开通过。真实Agent SIGINT取消/迟到撤权、暂停服务/重放账本、旧库/WAL保留沿用未受影响正式路径证据 | `build/first-launch-validation/selected-final/actual/report.json`；`final-cold/cancel-accepted/`、`boundaries-accepted/`、store迁移检查 |

最终完整原文26,723 B，SHA256 `6a8d11b5be05731bc2afc4cf3c71b8f0576e37a63a1895604c9d0d2efd2a2967`；保留实际SDK截短分段和官方learning，不改写产物制造一致。首session `codex_01a1bbf5-70a0-77a3-8c22-73023690dd13`；fresh `codex_01a1bbf8-8539-7692-93a4-38800b7e43a7`。项目A `7fa97bff-aff3-4253-89cb-3b55152a89dd`，B `f27069c0-d560-49ca-a0be-ea3f7e4b835c`；Cognee实例 `36be3e2a-2cc4-4575-a347-303fdafc6976`，dataset `f962aa4d-5053-5d3a-b2ad-9499b064b69c`，实际验证服务地址 `http://127.0.0.1:53215`（当前已停止）。实体查询、Memlane/Rust检索和官方recall分别走各自路径，没有双向同步或全局dataset自动提升。

实际版本：Codex0.167.0-alpha.3.2，沿用用户当前gpt-6.1-sol；官方插件1.7.7/上游997fd6f72253aa97b49996eb1b032793f0257935，Cognee1.6.7（冻结适配组件1.6.7-memlane.3），LiteLLM1.108.2、FastEmbed0.9.1、Uvicorn0.54.0、Go1.27.3、MyGo0.7.4。Cognee独立模型deepseek/deepseek-v4-flash，BGE512维；模型条件与Agent认证分别核对，没有升级Agent或改动日常模型。

全仓test/vet、desktop/official/components race通过；选定提交源码剥离用户未提交修改后test通过。包内全部9个普通文件前后SHA清单相同，helper与LaunchAgent也在清单内；当前包没有原地重建。自检及实机发现的失败消息修正见 `selected-interaction/review/`，自检不代替真实窗口。原型和故障夹具只作为对应设计/边界证据；503与节流下载明确标记，不替代正常Agent任务。公开组件源、正式签名分发、完整代码图谱和旧工程Goal其余生命周期不在本轮完成范围。

主DMG 18,166,963 B，App 40,583,159 B；首轮真实组件归档下载 708,140,713 B；最终App+client普通文件 1,583,373,218 B，OS分配文件量 1,653,649,408 B。下载量由真实受控源计数，排除HTTP/TLS及模型请求开销；组件归档复用仓库固定发布候选，仍是本机受控分发。最终磁盘含本轮组件、模型、业务与官方数据，不含开发工具链/编译缓存或其他验收根。

用户打开有效环境的一条命令：

```sh
open "/Users/bytedance/work/AI/memlane/build/onboarding-memory-validation/selected-final/app/darwin-arm64/MemlaneOnboardingReady.app" --args --p0-config "/var/folders/nv/g6m5_3nx1r9_v9dk3zh_5zw40000gn/T/memlane-official-agent-onboarding-yvz0iu65/runtime-config.json"
```

打开时服务已停止。在设置明确“启动服务”，从项目A“官方接入与同步”确认复核已安装环境，再在记忆→实体→关系图浏览并打开完整出处。无需再运行Agent即可体验保留的真实合成材料；若要查看首启页面，从设置重新检查本机准备，结果来自已有环境。停止使用“文件→停止服务并退出”，不删除数据。实际数据根为 `/var/folders/nv/g6m5_3nx1r9_v9dk3zh_5zw40000gn/T/memlane-official-agent-onboarding-yvz0iu65/client`，包与合成项目保留原路径；没有开启日常采集hook。系统临时根长期迁移仍需后续明确操作。

两个准备源、最终App/Agent/Cognee/LaunchAgent已停止，临时Agent auth与本轮TLS私钥不存在；停止后在下载源不可用条件下，同包全文/Rust/项目隔离/重开选择仍通过。日常Codex config/auth在最终起点与终点的SHA和mtime相同。清理、体积、清单与完整矩阵分别见 `selected-final/cleanup-final.json`、`sizes-final.json`、`immutable-bundle-*.json`、`delivery-matrix.json`。提交/远端与临时handoff以最终Git/交接核对为准；Q1–Q4保持开放，不恢复旧Goal。

## 首启交互续改前的不可变包验收（历史证据）

2026-11-08，`build/onboarding-memory-validation/immutable-full-run/continuous/report.json`通过：最新完整包从新空业务库、空Rust/E5与官方组件缓存开始，在同一个实际窗口内完成环境发现、确认、真实下载与验证、正常Codex工具任务、官方持久同步、Native完整原文、三视图、全部当前Rust和全新官方graph recall。主二进制、helper和配置的前后SHA均相同，过程中没有修包、手工补数据或切换模型。此项是该阶段连续证据；本轮首启交互和具体错误续改后的最新证据见 selected-final，不混用不同包。

本次实际产物为11 raw（1完整会话＋1 QA＋9 trace）、2 knowledge、21实体、22条有出处关系。原文51,401 B，SHA256 `a12098adbe9e9c380d72364ced3086e853329e6a39a6d605f950f094485cace1`，实际复制、长分页、搜索与SDK摘录定位通过。首session `codex_01a1bbc5-980c-7ce0-9a99-a0ee2fca0b9d`，新session `codex_01a1bbc9-c676-7fb1-a183-d24156b90a19`；新prompt和项目文件没有答案，通过官方脚本返回首session材料及真实回答。数目按实际模型产物记录，没有为了匹配旧记录补造4条知识或27个实体。

同包重放、第二项目隔离、来源跨项目拒绝、停止及重开恢复再次通过，证据 `immutable-full-run/browsing-reopen/report.json`。本轮服务、Agent与两个组件源已停止，临时auth与TLS测试私钥清理，包与合成材料保留；`immutable-bundle-before.json`、`immutable-bundle-after.json`和`cleanup-final.json`证明过程与最终状态。故障/取消/旧库迁移仍沿用下方实际边界证据，独立于正常连续链路。

此根只复用开发工具链、现有Codex认证及已批准Cognee独立模型条件；没有组件缓存、业务记忆或历史聊天。一次源启动脚本误解引用venv Python而缺cryptography，属于App启动前的源工具错误，已纠正入口并保留日志，不计作成功准备或App运行失败。

该阶段保留包：`build/onboarding-memory-validation/immutable-full-run/app/darwin-arm64/MemlaneOnboardingFinal.app`，身份 `io.github.zwhu.memlane.p0.d84a77123654`。主DMG18,149,754 B、App40,550,279 B，实际首下载708,140,713 B，App加client普通文件1,556,330,556 B（du约1.63 GB）。旧恢复环境额外保留的SDK数据使体积不同，两根测量分别保存，不能混用。

```sh
open "/Users/bytedance/work/AI/memlane/build/onboarding-memory-validation/immutable-full-run/app/darwin-arm64/MemlaneOnboardingFinal.app" --args --p0-config "/var/folders/nv/g6m5_3nx1r9_v9dk3zh_5zw40000gn/T/memlane-official-agent-onboarding-59oevo8l/runtime-config.json"
```

此环境打开时服务已停止。在设置“启动服务”，从项目A的官方接入入口确认复核已安装环境后阅读三视图与原文。停止用“文件→停止服务并退出”，只停进程不删数据。体验根为 `immutable-full-run/fixture-path.txt` 所指目录，包/数据原路径保留；系统临时根的长期迁移需后续显式操作。原恢复根也保留，下面命令仅是该旧根入口。

## 此前恢复链路与生命周期边界


以下是当前口径；后文按时间保留尝试历史，旧的“待验”描述不覆盖此表。

| 项目 | 当前结果 | 证据 |
| --- | --- | --- |
| 工作区与无关修改 | 已保护；本轮尚未提交。用户后台权限、图标、构建和原设计文件仍按初始 index/worktree 保留 | build/onboarding-memory-validation/baseline/ |
| 正式三视图 | 已在完整 MyGo 包实际通过共享项目/知识/实体、来源返回、项目 B 空态及重开恢复；完整原文补接最新版本也已通过 | build/memory-browsing-validation/official-app-4/report.json |
| Cognee 图 | 真实官方材料投影 27 实体、30 有具体出处的关系、0 缺依据关系；图查询检查实例、dataset、项目、连接代次和当前知识来源版本 | 同上及 final-cold/resume-5/；SQL/HTTP 契约检查另列 |
| 原文 | SDK QA/trace 分段逐字保存与阅读已过；新发现工具返回被官方 SDK 截到 1,000 字符，因此早期复制成功不证明完整对话。准确本轮完整 Codex 日志受信补接与正式窗口现已通过 | official-trace-completeness.json、native-original-report.json、native-original.txt |
| 空业务与严格空缓存 | final-cold 新空根通过真实 Rust/E5、冻结 Cognee 服务/BGE/官方插件的下载、校验、全部数据库健康和独立模型验证；没有预置业务记录 | build/onboarding-memory-validation/final-cold/continuous/、组件源实际发送记录 |
| 正常官方任务 | 已沿用现有 Codex/gpt-6-astra 执行正常合成编码与工具；官方后台持久水位和提炼分别核对。Memlane 持久收到 9 SDK raw、4 knowledge；Native 最新正式包已接收 | final-cold/resume-5/ |
| Rust 当前检索 | 4 条 knowledge 均由真实 worker 命中正文、项目、记忆 ID 和 v1；Native raw 不加入 knowledge 数量或图证据 | build/memory-browsing-validation/official-app-4/report.json |
| 全新官方召回 | 已通过：新 exec session 无 resume/旧历史输入，官方 graph recall 返回首 session 原始 QA 并回答唯一合成约定 | final-cold/fresh-recall-evidence/report.json |
| 方块灯泡、跳过与恢复 | 真实阶段动效、跳过、HTTP 503 故障、真实下载取消、同根明确恢复至 ready 且 0 memories 已通过；故障注入与正常路径分别报告 | build/first-launch-validation/lifecycle/actual/report.json、recovery/report.json |
| 真实 Agent 取消 | SIGINT真实按钮取消已通过，0.88秒停止实际工具、B仍零记录、迟到sync disconnected；此前失败继续保留 | final-cold/actual-cancel/ 至 actual-cancel-4/ |
| 迁移与失效 | 临时 v14 非空 WAL 一致性备份到 v15逐表保留；合成 transport/SQL 验证当前来源改变、移除和撤权拒绝。不是官方真实提取证明 | cognee_graph_integration_test.go、source_reader_transport_test.go |
| 最新检查/交付 | 六组本地串行审查完成，实质问题已修复并复验；全仓test/vet与受影响race通过，清理后最新包再次通过。提交不包含原用户修改 | build/onboarding-memory-validation/review/、checks/ |

原始记录独立预算为 256 KiB，知识与精确引用为 16 KiB。阅读器按原 UTF-8 分段，复制包含当前模式全文；超过预算真实失败，不能截短冒充完整。Native 与 SDK 分段明确属于同一会话，不变成独立证据或新增知识。

必需运行项已齐：完整原文、三视图、Rust、全新官方recall、真实取消、权限拒绝、重放、故障恢复、迁移检查和清理均有独立证据。相关提交及普通推送在最终交接核对。

## 版本、数据根与体积

有效包为 `build/onboarding-memory-validation/final-cold/app-accepted/darwin-arm64/MemlaneOnboardingFinal.app`，身份 `io.github.zwhu.memlane.p0.d59c86f16da7`。完整UI/helper/LaunchAgent本机ad-hoc验证包，不是正式签名公证或升级分发。根以 `final-cold/fixture-path.txt` 为准，当前 `/var/folders/nv/g6m5_3nx1r9_v9dk3zh_5zw40000gn/T/memlane-official-agent-onboarding-i4kxj4ce`；Cognee端点 `http://127.0.0.1:64588`，仍需App明确启动与验证。

包内实测：Codex `0.167.0-alpha.3.2`沿用`gpt-6-astra`，官方插件`1.7.7`/上游`997fd6f72253aa97b49996eb1b032793f0257935`，Cognee`1.6.7`、LiteLLM`1.108.2`、FastEmbed`0.9.1`、Uvicorn`0.54.0`；Go`1.27.3`与MyGo`0.7.4`。Cognee独立模型为批准的`deepseek/deepseek-v4-flash`，BGE512维；未切Agent模型。组件服务`1.6.7-memlane.3`、BGE`1.5`、Rust`0.0.1`和E5既定版本；记录在 `final-versions.json`。

主DMG 18,149,751 B，App普通文件40,550,279 B。严格空缓存实际发送Rust/E5 285,752,286 B、官方服务/BGE/plugin 422,388,427 B，共708,140,713 B，五个归档各只发送一次。App加client普通文件1,733,828,588 B，du分配占用约1.80 GB。开发工具链、源归档、旧尝试和截图另存build，不计客户端安装量。证据 `final-sizes.json` 与 `final-download-source-state.json`。

空业务根同时采用空客户端组件缓存；开发工具链、已有Codex及其认证复用，真实归档来自固定SDK和模型，经本机签名受控源发送，不宣称公开发布。没有预置记忆或采集历史。后续同根重开复用已校验组件属于另一种起点。下载源关闭后 `accepted-after-cleanup` 仍从已安装组件和签名缓存恢复真实服务，没有额外下载。

## 失败历史与边界

1. Cognee 预检启动旧合成根时执行了官方 abandoned pipeline 恢复，另有独立认证 401。已停止该进程并保留 `build/memory-browsing-validation/cognee-reader-console.log`、`preflight-stop.json`。启动恢复具有写入副作用，不能将此预检称为整个服务只读或本轮正常采集证据；后续仅用新根。
2. 第一完整 App 因 Checkbox 在构造后调用 Key 而 panic，未下载组件或创建业务库。已修复为 Context.Key，补各阶段原生 frame 检查；历史在 first-launch-validation/attempt-1。
3. 第二窗口真实发现通过，准备按钮后持续动效触发通用 automation idle 的两秒超时。该误判属于验证驱动，已改为直接 press 并等待真实终态；历史在 attempt-2，不将其算作真实组件失败。
4. 保护快照里的 `.go` 被 `go test ./...` 扫到；保留快照并改用 `_files/` 排除 Go 包发现，之后全量检查通过。失败日志在 checks/test-attempt-1.log。

当前 Memlane 验证窗口和服务已按 App 停止回执关闭；没有运行本轮 Agent、采集 hook 或 Cognee 服务。受控组件源为后续同一 Goal 验收继续运行，PID/argv/根记录在 component-source-process.json，最终需关闭并只清理它生成的私有测试 TLS key。前三个失败/当前环境及证据保留，不清理用户缓存或日常配置。

阶段 handoff 仅在系统临时目录实际生成，路径随检查点记录；它不结束此 Goal 或解除旧 Goal 的暂停。共同规则和矩阵见 [契约](onboarding-memory-contract.md)。

## 官方准备与受信同步实施续点（2026-11-08）

正式 App 已新增项目内“官方接入与同步”入口，以及独立模型条件、服务组件准备、实际 Agent 插件准备、正常任务和官方结束同步入口。凭据输入使用原生密码字段，临时发送给受信服务；Cognee 与 Agent 登录分别处理。官方材料读取复用同进程完整 SessionManager、QA/trace 持久水位、固定 session_learning 标题及逐文档完成状态，不使用官方会话详情的最后 20 项截尾作为完整来源。每条原始材料保留真实 QA 时间或 trace SDK 顺序；普通 Agent 写入不能声明官方来源戳。

Cognee 服务、FastEmbed BGE 与官方插件现在有独立真实归档及签名受控源脚本。服务冻结包依赖自身 Python 和原生库，主 App 不装入这些依赖；编码资源由官方 tiktoken 下载器及其内置校验准备。组件准备使用现有下载、签名、哈希、断点与不可变目录规则。受控源仅证明本机分发路径，公开发布和正式签名没有纳入本 Goal。

服务初次冻结运行依次暴露 `aiosqlite`、`ladybug._lbug_capi`、兼容 `kuzu` 动态依赖缺项；修正后实际服务启动、官方登录和 `/api/v1/memlane/identity` 已通过。随后真实模型探测因 tiktoken 编码插件/资源缺失返回 HTTP 503 `Model verification failed: TimeoutError`，没有显示 ready。依赖与资源正在补齐重建，尚不能记为模型准备通过。失败分别保留在 `build/onboarding-memory-validation/frozen-service-preflight-attempt1.log` 至 `attempt4.log`、`frozen-service-model-probe-attempt4.json`；成功登录证据为 `frozen-service-probe.json`。该预检没有正常 Agent 任务或记忆产物。

两个项目计划使用同服务下各自独立 Cognee principal 与官方插件身份，关闭默认共享记忆；Memlane 的凭据按“服务实例＋dataset”保存，避免第二个项目覆盖第一个项目授权。服务图与同步仍检查批准的项目、工作区、连接代次、具体原始版本和有效知识。取消后撤销 Memlane 接入范围不会删除官方服务已提交材料；该行为已实施，实际活动取消与迟到结果仍待实机验证。

新的连续验收根在 `build/onboarding-memory-validation/full-run/fixture-path.txt`，当前业务库、Rust 缓存、Cognee 服务数据与组件缓存起点为空，只提供本轮独立配置及复用的 Cognee 模型授权条件。最新 App、严格空缓存官方准备、实际三视图材料、Rust 和新会话 recall 尚待执行。当前新增代码的 desktop/official/daemon/store test 与 vet 已通过；这不是完整正式路径验收，不替代最终全仓检查及系统窗口证据。没有提交或推送本轮修改，原有用户修改仍留在原 index/worktree。

实际冻结包模型探测现已通过，结果在 `build/onboarding-memory-validation/frozen-service-model-probe.json`：服务实例 `34bd929f-9960-43eb-983f-03c6a01754d2`，`deepseek/deepseek-v4-flash`，本地 BGE 512 维，`memory_written=false`。这份预检复用本轮已下载的模型源，不代替新空根准备。编码插件已包含两份由官方下载器校验的实际资源；归档阶段一次人工误判中断记录在 `cognee-build-interruption.json`，随后重建退出 0。最新材料适配还采用官方 stale-watermark 归零规则，最新独立组件版本为 `1.6.7-memlane.2`。图和原文衔接补查服务实例身份，旧数据缺少正式实例戳时不会投影为新服务的实体。

最新完整 MyGo/helper/LaunchAgent 包在 `build/onboarding-memory-validation/full-run/app-1/darwin-arm64/MemlaneOnboardingFull.app`，构建、签名验证成功；原生连续驱动 `scripts/verify-onboarding-official-app.py` 已开始空根实际验收。其 `full-run/continuous/report.json` 只有相应真实路径通过才报告切片通过；后续阅读器、项目 B、重开、正式新会话 recall、失败边界和最终提交仍是本 Goal 必需项。

连续窗口尝试已结束并保留失败：`full-run/continuous/report.json`。空业务与空 Rust/E5 缓存准备通过，官方真实归档下载 422,397,746 B、API 登录与 BGE/DeepSeek 模型探测通过，现有 Codex/gpt-6-astra 正常创建并测试了合成代码；**采集与结束同步未通过，没有计为保存成功**。官方 SessionStart 发现服务 `/health` 仍 busy，后续报 `Plugin identity is enabled but not connected; run SessionStart`。

真实原因来自冻结 SDK 子进程：图与向量库 worker 再次执行冻结入口时未经过 `multiprocessing.freeze_support()`，进入 App 参数解析并因缺 `--config` 退出 2，官方健康检查报 `SubprocessTransportError`。这说明 API/模型探测不等于数据服务可用。已补标准冻结子进程分派，并把正式 ready 门禁扩为 `/health/detailed` 全部 healthy，再做实际模型探测；新组件版本 `1.6.7-memlane.3` 正在构建。没有切换 Agent 模型或修改官方插件来绕过错误。

旧 `full-run` 的准备、正常编码任务、SDK worker 失败、窗口截图与清理均保留；新 `final-cold` 从新的空业务与空缓存根重新验收修正后的完整准备路径。第一轮模型探测成功仍只证明对应 API/模型切片，不能作为 Cognee 图/向量服务、采集或首启全链路通过。驱动也补了失败终态识别，避免等待不存在的已授权同步按钮。

修正后的完整 SDK 运行已真实通过：`frozen-service-full-health.json` 返回所有组件 healthy（SQLite、LanceDB、Ladybug、文件、LLM、向量）；`1.6.7-memlane.3` 使用标准冻结子进程分派。最新准备状态只在完整健康与模型探测通过后 ready。`final-cold/fixture-path.txt` 指向重新建立的严格空业务/组件缓存根，新完整包正在构建并将重复真实连续路径；尚未记为采集/同步/三视图材料或本 Goal 完成。

`final-cold` 连续尝试中，修正后的全量准备通过，现有 Codex/gpt-6-astra 正常任务与 8 条真实工具轨迹采集通过，官方结束同步返回真实桥接回执。App 受信接收尚未通过：Cognee API key 需要正式 `X-Api-Key` transport，而此前 App/daemon 当作登录 JWT 使用 Bearer，导致 dataset 查询 HTTP 401。已按 Cognee 1.6.7 官方 transport 修正；登录三段 JWT 继续使用 Bearer。下一步复用本轮已有真实会话验证 App 接收，不重建官方材料、不手工注入或修改官方产物。失败报告、正常工具轨迹及官方回执保留在 `final-cold/continuous/` 与该隔离根。

重开接收尝试第一次在 helper 启动等待中超时，记录在 `final-cold/resume/report.json` 与实际失败窗口。随后用同一完整包、同一根保持窗口打开，在正式设置入口启动：系统 LaunchAgent 已登记，launchd 显示 helper running，App 收到 ready 回执。实时证据为 `helper-launchctl-live.txt`、`helper-system-log.txt` 和 `helper-diagnosis/live-system.png`。没有用独立协议服务绕过包，也没有改动其他会话的测试服务；前次超时继续保留。已再次从正式包执行已有会话接收。

## 官方后台完成与正式接收续验（2026-11-08）

实际根以 `final-cold/fixture-path.txt` 为准，当前为本轮 `memlane-official-agent-onboarding-i4kxj4ce`；服务实例 `d7cc1e4d-1b2f-4112-a126-16cbe2d06589`，项目 A `42f3d2d4-d107-45df-b30d-9727d3b47d8b`，dataset `71b59d27-6055-5f8b-baeb-d70999d70d5c`，会话 `codex_01a1bb1f-68bf-7441-b006-00ad9c034e6c`。没有重造材料或改写官方提炼产物。

`resume-2` 的失败属于活动动效与通用 idle 等待的竞态；驱动现在同时等待真实准备终态及按钮恢复可用。下一次 `resume-3` 已通过正式鉴权和环境复核，但返回 `awaiting_official_sync`：1 条 QA、8 条 trace 在官方缓存，持久水位均为 0，没有导入 Memlane。官方插件 1.7.7 的 `--strict` 只证明后台 improve 提交成功，代码明确“不在提交后轮询”；不能称为持久回执。服务日志证明过早退出时后台实体提炼仍在执行；正常任务、提交、后台持久化分别保留证据。

`resume-4` 保持正式 App 与其服务运行，仅从已批准的会话入口重试受信材料读取，不重复提交 improve。真实状态依次为 QA 水位 1、trace 水位 8、4 份待提炼材料，以及第一条知识完成；报告和材料快照在 `final-cold/resume-4/`。这次只接收了首份已完成知识，不把它当作全部知识完成。

包含有限等待实现的完整 MyGo/helper/LaunchAgent 包 `final-cold/app-2` 在 `resume-5` 正式接收 9 条原始记录和 4 条知识，当前图投影为 27 个实体、30 条有出处关系、0 条缺依据关系。官方 QA 保留实际审核口令，官方 learning 将其提炼为保密规则，二者实际内容不同，均原样保留；不为一致性修改产物。Rust 对当前知识的逐条检索、原文导航、系统窗口对照和新会话 recall 仍待后续实证。

接收等待最多 30 次受信读取、5 分钟，停止条件检查真实原文持久化、材料处理状态和已产出知识；原始水位已推进但 learning 尚未创建的空档不会立即结束等待。它不额外调用 improve、不建立默认自动记忆策略，出错或取消后提供明确结果。transport 回归仅证明等待、有限次数和取消停止继续读取，不代替官方服务证据。

`resume-5` 另发现正常完成后的 deferred context 清理被界面回调误判为用户取消，导致文案错误；已在回调入队前记录真实取消状态并修正，数据持久化结果不受该文案错误影响。最新完整包正在 `final-cold/app-3` 构建，须继续实际窗口复验。当前仍未提交或推送；全链路与本 Goal 均未完成。

## 三视图、全文与全新官方召回实证

完整包 `final-cold/app-4` 已在 `build/memory-browsing-validation/official-app-2/` 通过真实窗口验收：9 条原始记录、4 条知识分别计数；共享项目、所选知识与实体，记忆→实体→关系→来源知识→同一原始记录可返回；27 个实体和 30 条关系均从官方材料读入。完整 QA 为 1,826 B，App 复制与正式 `source_read` 返回原文逐字相同，SHA256 `e83d92a1d23a46e1e85fd493747edd7861598a0a7961a0f83b51dabb16d2a034`。阅读器按 QA 和官方 trace 顺序分组排列，不杜撰 trace 时间或两类材料的混合时钟。

四条当前知识分别由实际 Rust worker 查询，命中的正文、项目、记忆 ID 和 revision 均与持久库一致。项目 B `2664c89e-78b8-43ec-87b7-a4b0b77a6ab0` 切换后为真实空态，不显示 A 的记忆或实体；跨项目原文读取被拒绝。重开后经 App 明确启动并复核本机和 Cognee 服务，再恢复所选项目、知识、实体与视图。系统窗口截图与 Go/HTTP 夹具分开，当前窗口已实际对照设计；实体区和长引用预览已收紧，完整正文没有裁剪。

第一会话已结束后，正式 App 的真实 Codex exec 创建新会话 `codex_01a1bb58-3a75-7ea0-8a35-0f92c382d00a`，没有 resume、旧会话输入或在新 prompt 提供答案。官方插件先通过正式 prompt hook 提供图来源材料，新 Agent 又实际运行官方 `cognee-search.sh … 10 --graph`，返回来源 `graph` 与第一会话 `codex_01a1bb1f-68bf-7441-b006-00ad9c034e6c` 的原始 QA。实际回答为该会话的唯一合成口令，并说明来源。未将答案写入项目文件，也未使用 Memlane MCP 搜索替代召回。

新会话的实机截图在 `final-cold/fresh-recall/screenshots/`，请求、实际返回与回答在 `final-cold/fresh-recall-evidence/report.json`。首次驱动只识别旧 function-call 日志，误报未见调用；之后又发现 custom-tool-call 的 output 是结构化 content list，补齐两种格式后仅从同一已结束会话提取证据，没有重跑 Agent 或注入材料。两次解析失败属验收驱动边界，不是官方 recall 失败。

审查新增了官方原文的当前可读性核验：在已有 Memlane 项目/版本检查之外，受信 daemon 读取已绑定实例和 dataset 的实际会话材料，拒绝官方权限丢失、原文改变或移除；网络返回后再次核对本地版本及连接代次。副本与有效知识不被自动改写。该新增契约的 transport 回归已通过，最新 `final-cold/app-5` 与对应正式原文路径正在复验；实际取消、失败/跳过/恢复、最终检查、清理和普通提交/推送仍待完成。

## 完整 Codex 原文与最终三视图续验

`build/memory-browsing-validation/final-package/report.json` 已通过完整正式包：10 raw（1 份完整会话、1 QA、8 trace）与4 knowledge分别计数。准确已结束Codex原文49,482 B，App全文复制SHA256 `245a39b2bdca489ac94c25a9a221cb0bbec1e5f5c42d7668df6fd65a719ddc68` 与受信解析一致；实际长分页、搜索和SDK摘录定位、同源跨视图返回、项目B空态、重开与明确恢复真实服务均通过。原4 knowledge和SDK来源仍v1，Rust逐条当前版本检索仍正确。SDK截短产物保持原样，不将Native补接算作独立证据。

该包同时验证了定位实体排在第9个时仍出现在8个局部节点中；普通官方raw的详情/历史/编辑不再直接绕过阅读器。源版本与授权的HTTP/SQLite回归属于故障契约检查，与系统窗口及正常官方提取分开。

两次反复原地重建 `app-5` 后，helper未执行，系统明确 `OS_REASON_CODESIGNING | Launch Constraint Violation`；失败在 `official-app-native/`、`official-app-native-2/`及`final-cold/native-helper-system-errors.txt`。采用新的独立App身份后，实际helper和服务可用，完整原文验收通过。这不是正式升级/签名分发证明，旧失败保留，不扩大到旧全量Goal。

真实Agent取消首次按钮已按下且Memlane范围撤销，但SIGTERM留下实际90秒工具进程，失败在 `final-cold/actual-cancel-final/report.json`。已改为Codex正常SIGINT中断，再按12秒/5秒有限升级终止；当前重新构建完整包验证工具确实停止。项目B的旧Memlane授权保持撤销，后续试验不隐式恢复它；官方B principal仍属于本輪批准的合成任务范围。取消仍未记为通过。

## 最终取消、服务边界与清理

`final-cold/cancel-accepted/report.json` 的真实按钮取消在0.86秒停止实际90秒工具，移除临时opaque auth，B的Memlane记录仍为0；有效memory_status旧请求和迟到sync均返回disconnected。重开明确显示接入已撤销并禁用同步。B的Memlane授权在前次真实取消中撤销，本次没有恢复；官方principal仍属于本轮合成任务范围。

同session官方材料查询返回HTTP404，没有取得该被取消session的持久回执；不能据此宣称官方从未写入部分材料，也不删除官方B中前面正常尝试的产物。此前驱动用不支持的memory_list得到invalid_request，后续改为真实memory_status并核对disconnected；旧报告保留，不能将invalid_request算作授权证明。

`final-cold/boundaries-accepted/report.json` 的故障干预只暂停本轮真实Cognee进程，Memlane仍ready；App同步显示失败，复核/重启后受信同步成功。完整账本前后均为14 memories、14 versions、14 operations、14 activities、36 organization_sources、2 bindings，逐表SHA相同；实体/关系查询不写本地关联表。A principal查询B dataset返回HTTP403，跨项目同步拒绝。此干预与HTTP503下载夹具分别标记，不代替正常Agent或真实取消。

本轮组件源、测试Agent与服务进程已关闭，当前LaunchAgent不再登记，A/B临时auth.json均不存在；仅本轮测试TLS私钥清理，数据、包、截图和失败历史保留。cleanup-owned-processes.json与cleanup-final.json记录PID/argv核对，没有停止其他会话服务。日常Codex配置mtime早于本Goal，末次SHA与本轮只读审核一致；起点未保存其hash，因此不宣称完整起点hash对比。代码只向App-owned home写配置，不复制日常历史。

清理后最新包再次通过系统窗口、完整原文、四条Rust、项目B隔离和重开：`build/memory-browsing-validation/accepted-after-cleanup/report.json`。最终test/vet/race为checks/final/*-accepted.log，六组自检及已修问题/HTML在review/，自检不作为运行证据。源码提交只包含本轮文件和main.go首启hunk，用户背景权限、图标、构建脚本和原设计文档保留。

用户打开命令：

```sh
open "/Users/bytedance/work/AI/memlane/build/onboarding-memory-validation/final-cold/app-accepted/darwin-arm64/MemlaneOnboardingFinal.app" --args --p0-config "/var/folders/nv/g6m5_3nx1r9_v9dk3zh_5zw40000gn/T/memlane-official-agent-onboarding-i4kxj4ce/runtime-config.json"
```

App打开时服务已停止：在设置明确“启动服务”，从项目A的“官方接入与同步”确认复核已安装环境，再进入三视图和完整原文。无需新Agent即可体验现有合成材料；B保持撤销的受限状态。停止用“文件→停止服务并退出”，只停进程，不删数据。根和build包保留原路径，数据根位于系统临时目录，本轮未移动已授权项目；长期迁移保留为后续显式操作。

Q1–Q4仍待讨论。本轮技术与流程通过不选择日常自动记忆策略、项目创建策略、全局自动共享或完整代码图谱，不恢复旧全量Goal。

## 首启原型交互续改（2026-11-09）

原先首启统一长卡片列表及大动效没有完整承接所选首启原型的信息层级与交互，已改为紧凑分组、折叠详情、具体准备确认、任务进度与明确下一步。固定顶部状态/底部操作/右侧编号与步骤高亮，方块灯泡仍是选定动效；薄雾 URL 仅作为用户指出的交互参考。正式 MyGo 实现使用真实状态，不搬入 HTML 或模拟成功。

新的独立 App `MemlaneOnboardingFlow.app` 位于 `build/onboarding-memory-validation/selected-interaction/app/darwin-arm64/`。实际系统窗口已对照原型，检测页默认无需长滚动，ready 页小灯泡保留终态并引导项目首次使用。原生 frame 检查只证明布局/按钮门禁；真实窗口与本机准备另有证据。全仓 test、vet 与 desktop/official/components race 通过，日志在同目录。

本次 UI 预检创建了两个仅含签名 feed 的缓存目录；严格空缓存续验前只清理本轮拥有的这两份 feed-cache，详情在 `strict-cache-reset-after-ui-review.json`。业务库、已安装组件、官方服务数据和 Agent home 都没有预置。预检后首次驱动因目录存在的严格断言提前结束，保留 `continuous-untouched-cache-check.log`；没有把该断言算作准备成功。独立 Cognee 条件复用本 Goal 已授权模型配置，换成本轮根并移除旧 HF_HOME；没有复制服务数据、模型缓存或历史会话。

真实连续路径在 `selected-interaction/continuous-accepted/` 执行；故障与恢复在 `build/first-launch-validation/selected-interaction/` 续验。最终通过状态、数量、体积和清理结果在执行结束后补记。

### 具体失败原因补验

`selected-interaction` 的首启跳过、503拒绝、下载取消与重开恢复都运行完成，但实际失败窗口暴露具体原因缺失：组件 progress 提前发出 failed，首启状态进入 failed 后忽略外层随后的 HTTP 原因，显示旧“正在准备”文案。原生测试与首轮自检没有发现这个问题，不能据其宣布失败展示通过。已修正 failed→failed 的具体错误更新与持久保存，并确保迟到 ready 不覆盖失败；实机驱动新增必须出现 503 的断言。

最终新包与独立空根在 `build/onboarding-memory-validation/selected-final/` 和 `build/first-launch-validation/selected-final/`，与上一包/旧失败分别保留。前一包的正常任务、来源、Rust、新会话召回与重开证据保留；最终包会继续按同一连续标准复验。
