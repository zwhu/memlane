# 精简版 dev 执行记录

状态：D01–D09按本轮明确范围完成；下文保留分阶段状态与失败原始事实，最终结果见文末及交付记录。本Goal只覆盖本次精简版，不恢复历史全量Goal、不进入release；窄窗取消，历史缺陷证据与累计次数不变。

## 开发前基线

- 当前分支 `main`，commit `559eab851ec306d44ed2549e4ad575b035b829b6`。
- 本地注释 tag `pre-simplification-20261113-000958`（Asia/Shanghai），未推送。
- 提交731个相关源码／配置／文档路径，含未跟踪源码；未采用独立 index、分支或额外 worktree。完整清单可用 `git show --name-status <tag>` 获取。
- 排除 `.mygo/` 调试产物、构建与组件、真实数据库、账号与本机Agent配置。凭据模式扫描唯一命中为 `official_capture_policy_test.go` 合成私钥夹具。

## 所有权与连续流程

主执行者独占 `scripts/dev.py`、官方启动入口、开发说明与本记录，负责日常运行接管；completion Agent 独占 Python 模型端点及专属测试；Native Agent 独占 `cmd/memlane-app/` 与新增 `internal/simpledesktop/`；运行盘点 Agent 只读检查后负责 `.local/daily/` 私有配置准备。只有主执行者可启停日常服务。

主流程：启动器取得现有官方数据所有权 → completion → 固定官方 API → Native → 正常 Codex 官方插件采集 → 官方处理与保存 → Native 阅读／搜索／出处 → 新会话召回并实际采用 → 官方图谱与手动代码索引。测试夹具只证明局部行为，不能替代日常流程。

## 当前运行事实与保留项

- 日常官方根：`/var/folders/nv/g6m5_3nx1r9_v9dk3zh_5zw40000gn/T/memlane-task-gnwqmjhd/initialization/client-responsive/official/cognee`；虽位于临时目录，属于必须保护的日常数据。
- 只读盘点时旧 Cognee PID76867（子77407/77408/77409），地址 `http://127.0.0.1:61255`；旧 daemon PID76077 仍持有旧 SQLite。尚未停写或接管，遗留 state.starting 不当成健康事实。
- 旧 daemon 依赖 `build/goal-native-runtime015-20261111/MemlaneConcentratedTwelve.app` 完整包，embedding 依赖该日常 official/runtime/components/cognee-bge-zh/1.5；消费者退出前保护。
- 固定官方源码 `.cache/coding-memory-components/service-ec6f40859e5f2df9e7862dde27d580ac3268ed43`，3340个归档文件逐一比对0不匹配，归档SHA符合清单。使用既有 `.local/cognee-python/bin/python` 3.12.12及依赖，通过 source path 载入固定官方包，不修改共享环境。
- 起始可用空间约124 GiB；所有测试／开发运行仍经现有磁盘门禁。

## 实施及清理状态

- 已替换 `scripts/dev.py` 的旧 memlaned/CLI 编译、私有IPC健康检查、独立开发全文库与worker配置启动路径；新入口提供 start/status/open/stop，锁绑定规范化官方数据根，拒绝空库与占用端口。尚未完成真实日常接管验证。
- 新官方启动模块仅载入固定原始包与官方 API，不安装旧 browsing／managed improve monkey patches；固定来源与端口、现有数据、私有配置均需明确。
- 已更新 AGENTS、精简规格／技术方案与开发说明中的暂停及独立快照流程冲突。
- Native 官方直读与completion端点并行实施中。全仓旧实现、内容与运行资源尚未退役，不宣称D06/D09完成。
- CUA浏览器host不可用，HTML原型打开未完成；实际已查看首页、知识设计截图。Native实际视觉验收未完成。

## 检查与当前问题

- Python启动模块语法检查通过。
- 启动器定向测试首轮：canonical store锁跨进程／符号链接通过；进程组收尾检测遇macOS瞬态EPERM，实际子进程已退出。该具体问题累计1次，改为EPERM时核对真实进程表，定向重查通过（2项），随后加入请求子进程身份跟踪并再检查通过；不改写原失败。
- completion端点10项合成协议及HTTP检查通过，未调用真实模型；私有日常配置已就绪，owner登录可用，项目暂为空，等待同库接管后建立真实memlane关联。
- D01–D09均待对应真实证据收口；下一步端点与UI完成接线、检查所有权及后台进程收尾，再按停写→官方一致快照→同数据接管→退役旧链顺序执行。

## 日常接管进行中

- 已精确移除Codex自有 MCP 两个表及 Stop/UserPromptSubmit 两条capture hook，保留第三方hooks；移除pi自有扩展精确引用。必要原配置私有保存在 `.local/daily/prior-config/`，未复制聊天。
- 原三个MCP、原daemon及Rust worker、原Cognee及子进程共9个已观察PID均确认退出；精确bootout原launchctl service，无helper修复试验。
- 官方数据根完整一致快照已保存 `.local/daily/official-snapshot-*`，约5.8MiB，仅官方根；未把旧Memlane内容归档或迁移。交接回执 `.local/daily/cutover-receipt.json`。
- 新入口首次启动因规范化Python符号链接绕过venv失败（准备问题1次），已修正保留venv入口。未下载依赖、未写新库、所属端点确认退出。
- 第二次启动session99954：completion18173健康200；固定原始官方服务启动中，已在原根执行官方关系库迁移并加载已有embedding及向量collection；尚未宣称API可用。新服务接管不能等同D02–D04真实采集／生成／采用完成。

## 当前已验证与下一步

- 新入口start→明确stop→同配置restart完成：服务61255、completion18173均ready；当前运行session62532。稳定官方认证密钥已私有保存，代码索引允许官方本地路径入口，未关闭官方认证。
- 官方memlane dataset已创建，配置映射到当前真实仓；token已更新，项目创建回执脚本仅因owner字段命名不同报错，配置与官方创建本身已经成功，不重发写入。
- `dev.py open`启动MyGo dev，轻量调试编译1.2秒并监控源码。Native Agent实窗确认1200×820逻辑窗口、服务及端点连接，官方空组显示0条；空组不代表D02–D04完成。图组件真实承载0节点仍待真实内容与代码索引。
- 官方插件1.8.0已由CLI从固定本地marketplace安装启用，57文件逐一比对0差异；私有.env配置同身份／服务／memlane dataset，禁用独立插件身份与本地fallback。尚待新会话hook信任、正常编码采集与真实召回。
- 已退役240个旧脚本／Python业务适配／Rust原文worker源码路径：私有IPC验收、历史功能夹具、helper／完整包实验驱动、二次业务编排、旧独立索引。完整Git删除清单可从本轮diff审阅；未删除实际运行组件或官方数据。旧Go业务模块同步退役中。

## 自动采集首条真实链路

- 正常CLI会话 `01a1d091-2be2-7a33-8a74-b2616a07b10b` 已完成README真实修改，但官方hook及最终sync返回401：把owner JWT放进插件X-Api-Key字段，认证接线错误第1轮，未采集，不写成通过。
- 已通过官方接口签发同owner正式API key并核对其X-Api-Key读取dataset成功；正式 `/hooks` UI逐项信任9条官方hook，不绕过信任，第三方修改项保留。
- 第二个正常编码会话 `01a1d093-9db5-7c01-86bf-5d6e89a33203` 修正桌面长期JWT问题：client也统一X-Api-Key，不保留Bearer回退。官方会话 `codex_01a1d093-9db5-7c01-86bf-5d6e89a33203` 精确GET和列表均可见，含1问答/15工具trace；Native已实窗读到同一身份与实际内容。
- 官方材料文档 `f450aef3-a9be-4e10-994d-54c7bf47cd93` 位于真实memlane dataset，node_set=user_sessions_from_cache；这是原始材料，不当成知识生成成功。D03/D04仍待官方处理和新会话采用。
- 实际任务的包测试在编码沙箱因不能监听本地端口而受限，保留事实；主执行者在开发环境执行受影响包检查，结果另记。
- 另已删除12个R旧库备份/恢复元数据（约6.4MiB）及精确解除宿主引用后的3个旧adapter安装；配置必要原件与hash私有保存，官方数据/embedding保持。
- Go依赖已收敛到MyGo及其间接依赖，无自有SQLite/MCP/TOML业务依赖。首次tidy误扫描build历史Go片段为准备问题，已在go.sh建立build嵌套module边界保护证据并排除活动包，tidy随后通过。

## D03 首轮实际失败

官方后台operation `fa37b278-9a53-4565-8996-430d1d4fc627`（improve，精确session为上述真实编码会话）终态failed/CogneeSystemError；memify run `9f33b909-5ff1-4f31-a26f-e44fbeb3fe47` errored。服务日志证实NativeLiteLLMAdapter请求触发endpoint `codex_schema_required`，官方自带重试后连接检查30秒超时。此具体请求契约问题第1轮失败，官方同轮重试不拆成多轮；未重发处理。

原材料和问答已保存，知识仍0；先修复模型请求契约再由唯一执行者安排实际重试。局部协议测试不替代D03完成。原固定328字节README图谱四轮停止问题与helper更新问题保持停止，不采用旧样本重跑。

桌面API key变更后主执行者 `sh scripts/go.sh test ./internal/simpledesktop` 通过；新增venv入口回归与锁／进程组共3项启动器检查通过。当前Native真实原始记录展开、复制、刷新保留和切换后阅读位置已观察通过；长UUID列表标题超出区域作为新的标准窗口布局问题第1轮记录，由Native修正。

## D03 第2轮与运行保护

- 已修正第1轮普通completion契约：使用内部闭合字符串Schema执行并校验后解包；业务JSON Schema双校验保留。11项合成协议检查通过，不跳过官方连接检查。
- 明确stop后确认服务和UI所属组退出，新start恢复原数据并打开MyGo（live session6435）。官方第2轮improve接受200，回执 `.local/daily/d03-round2.json`；operation `1ce04343-eb6d-42c2-aef1-44922d6f3f73` 最终failed，memify `7cc52fa6-a3a5-4ea0-8c32-f543a97e35f6` errored。
- 第2轮服务实证为 `codex_transport_closed`，随后官方连接检查30秒超时；与第1轮请求格式400分别保留触发及证据，不因同属D03就抹去各自归因，D03实际总失败2轮。正在核对真实子进程启动stderr，不重发处理、不推断实际模型调用为0。
- 原始记录连续Native检查完成：1QA/15trace、工具展开真实参数/错误、复制、独立滚动、刷新及离开返回保留阅读状态。标准长UUID溢出修正后实窗复验通过（该缺陷首次失败1轮）。
- 清理器已从日常配置规范化实际数据/源码/归档/模型/组件依赖，保护其祖先载荷，配置不可解析则拒清；9项磁盘测试通过，未执行真实prune。原R外层临时任务目录现在被明确保护，不依赖ps命令是否展开数据根。

## 第3轮结果及容量修正

- 第2轮启动错误的真实red回执为`.local/daily/codex-start-diagnostic-003.json`：实际resolved codex.js、已存在隔离home及同PATH下exit127，env无法找到node。001因准备差异不是该根因证明，保留更正；002为同npm安装包原生binary initialize成功（无模型）。运行已改当前同版本原生binary，不换版本或模型。端点私有有界stderr诊断及12项检查通过。
- 第3轮已真实启动所属Codex子进程，部分官方memify/cognify/indexing完成并保存agent_trace_feedbacks文档；这不等于session_learnings。原memify `984f634f-eded-4862-8f44-f64c449a0286` 及cognify `44b6bbb2-1cc2-4769-ae83-a3e4496d618e` 于16:41:19–22UTC ERRORED，最终明确429 codex_busy。此容量缺陷第1轮，D03实际总失败3轮；Schema、解释器启动、容量三项的触发与证据分别保留，不合并整个功能凑阈值。
- 根因是自选1执行/1等待不足以接收官方并行抽取批次。已改为2执行/32总在途HTTP，只有有界内存Semaphore与计数，不加持久队列、调度或业务重试；13项检查证明并发上限、容量429及shutdown进程退出。
- 官方无operation取消接口，等待超时不代表取消。已显式stop旧launcher并确认服务/端点/Codex/UI所属进程退出后再start，保留原部分官方写入和所有失败回执，不重放编码材料。
- 通用知识存在真实官方缺口：个人偏好内部节点无公开列表正文API、global context是项目摘要。已向用户提出展示边界决策；不把空映射称交付，不新增私有route补齐。

## 用户确认的通用知识边界

用户2026-11-13原话：“保留入口，明确显示官方暂不支持（后续我们可以支持，专门建一个通用知识的dataset即可，通用知识可以给所有agent读取）”。本轮保留入口并显示官方缺口，不创建通用dataset、不复制项目知识或扩建私有接口。后续独立dataset作为跨项目通用知识来源，所有已连接Agent可读，不引入项目／Agent权限产品。此决策解除该页面当前展示边界冲突；真实通用知识能力仍属后续。

## D03成功与D04实际采用

- 第4轮最终官方improve operation `0e86e197-ab13-4762-bc3f-3c143472d072` 于16:47:18UTC succeeded，所有当前pipeline成功，端点在途0且无所属推理进程；源码核对该operation跨全部stage并在后台完成后才finish，非仅提交接受。元数据 `.local/daily/d03-round4-final-metadata.json`。
- 官方保存2条session_learnings：`ada020e3-7444-524f-a462-d034940c1e33`（state）及 `f55f8c96-7d1a-5a64-9b7e-25edc08ad9aa`（pitfall），关联真实原会话。Native读取核对：API key请求头与无Bearer回退符合源码；沙箱测试监听受限保留适用条件与未知，不虚报全部测试通过。
- 新正常CLI会话 `01a1d0a9-31d1-7402-9ebc-8961c382786a` 完成开发配置文档真实任务。官方SessionStart预热2条知识并注入1234字符，非手动拼接；metadata回执 `.local/daily/d04-automatic-recall-metadata.json`。最终回执明确实际采用已召回的token_file→X-Api-Key、无Bearer回退规则，结果写入docs/development.md；根执行者已核对文档差异。插件未给选中知识ID时不伪造逐条选中；候选为上述真实知识，额外主动召回未取得新内容不当成功。来源所记HEAD559eab8是当时HEAD，不等同未提交修改的精确源码版本。
- Native项目搜索X-Api-Key返回2真实片段；全局搜索13片段按知识筛为2。state片段 `a27c98d4-a426-53cc-96d8-9fbf4e3e2ee8`→真实知识→真实session链路有效。多层来源单Return覆盖导致原片段返回丢失作为独立缺陷第1轮，改内存返回栈后实窗复验回到原片段，查询／类型／计数／位置保留。

## T5首轮范围错误及安全收尾（当前续接点）

- Native首次开启自动建立独立code dataset `86845625-0288-58a3-b556-744d5c556dc4`（memlane-code），一次官方blocking remember。code_graph_pipeline `0c7e1101-23d0-4649-97f0-19a1e4963103`，前置add已完成；原请求一直保留，没有并行发第二次。
- 官方Cognee输入清单65个source、0文档、759136跳过；官方Enola默认不读取.gitignore，也未排除.local/.cache/.mygo，实际61539/128965文件、1,828,913 facts，首5条file均.cache/capture-native-seed/main.go。范围验收明确失败1轮，技术非空或2xx都不算通过；没有使用旧328字节README样本。
- Enola完成后导入映射1,312,631 datapoints，服务内存一度约9.64GiB，可用磁盘117→110GiB。接近运行105GiB停止门禁，且范围错误已证，root显式stop并确认所有所属服务／端点／UI／子进程退出；不是因观察超时重启。原code任务业务终态未确认，保留部分官方写入，不写成已取消成功。
- receipt及仅路径/ID样本私有保留：`.local/daily/t5-first-enola-receipt.json`、`t5-code-first-interruption.json`。确认退出后删除本轮生成的.enola错误快照／缓存约2.2GiB，可用回到112GiB；.enola已加入Git忽略。
- 已按固定Enola0.4.30官方配置完整保留115条默认ignore，追加6条.local/.cache/.mygo根／嵌套规则，落根目录mcp-arch.yaml；没有自写扫描器、改SDK或换成小repo。
- 同时修正启动器所有权记录：starting期也带真实启动身份，原子写记录；每次启动／重开UI记录真实所属watcher并移除已确认退出旧watcher。3项启动器检查通过。此为真实源码变更，也会进入下一次官方代码manifest。
- 当前仅维护后台运行：tool session97019，launcher22446、completion22448、official service22449，均同一原官方数据；无UI。通过官方DELETE **仅清理本轮新建且范围错误的memlane-code dataset**，客户端tool session93447仍在等正式结果，receipt `.local/daily/t5-invalid-code-cleanup.json` phase=submitted。请求前已核对primary memlane中2个已验证知识ID仍在；必须等删除终态及脚本的primary保留核对，不重发。
- 最近只读观察：官方清理所属worker23196实际live且100%CPU，parent22449约3.4GiB；可用112GiB。此为活跃清理等待，不能把父CPU0或观察超时当任务结束。
- 后续：等待原93447结果；确认新错误code dataset已删除且primary知识保持，再官方创建/复用同名独立code数据组并恢复关联，检查旧STARTED记录不误作新任务；重开Native后由Native唯一发第二轮实际索引/手动更新，核对真实项目范围和源码出处。再完成关窗不断后台、open重开、最终检查/交付。
- 不再重跑D02–D04模型链路来补材料；它们已有真实闭环证据。D05实窗已补独立列表/正文滚动、真实长JSON代码横滚、键盘CmdK及刷新保留；通用知识最新用户确认空态实窗已确认。
- 新增`client-simplification-errors-20261113.md`并非本team创建，疑似其他会话工作，已保留且不覆盖、不纳入当前提交；当前所有新实现已跟踪以供真实索引，新配置/最终修改待收口，尚未最终提交。

### 续接补充（17:26UTC）

官方DELETE原客户端93447仍live等待，未返回、未重发；当前维护launcher97019/22446与服务22449正常，Graph worker23196在执行原清理，磁盘约112GiB。先poll同93447；超时只记结果未知并核对同worker/API资源，不另发DELETE。

本轮API清理脚本为`.local/daily/cleanup_wrong_code.py`（一次性私有材料，结束后删除）；它在DELETE后会GET primary数据并验证已知2知识ID再写`deleted_primary_preserved`，不能仅看到客户端退出就当成功。必要再核对datasets目标已消失，才创建同名code dataset并由Native更新。

额外完成：当前活动Go模块`sh scripts/go.sh vet ./...`通过，diff/cached diff检查通过。启动器stop增加失败闭合：锁已释放但ownership记录仍在时不能声称全部清理成功；新增真实fake-owner子进程回归，首次fixture因/var与/private规范路径不匹配提前被身份校验拒绝（准备错误），修正fixture后4项通过。UI所有权原子记录改动及stop检查源码都会进入下一次真实代码manifest。

当前已应用根mcp-arch.yaml、.gitignore中的.enola忽略；还没有第二轮代码索引，也没有最终源码提交/Goal complete。通用知识范围用户已确认，无待授权问题。保留其他会话未跟踪errors文档，勿覆盖或默认提交。

## 官方错误code dataset清理完成

- 原公开DELETE客户端93447在600秒后timeout，后台worker23196仍活跃；结果记未知，没有重发。sample确认在Ladybug原生查询执行，仅此不能断言具体算子或剩余时间。
- 固定源码确认公开DELETE先逐项图来源遍历、再整体drop；官方底层`modules.data.methods.delete_dataset`本身支持不打开大图、按DatasetDatabase精确drop独立图/向量存储。
- root显式stop维护服务，70519及97019均终态0，确认全部所属进程退出。唯一SDK维护进程取得同规范数据根flock，使用同固定source/已有Python/既有owner及官方delete权限检查。预检5个映射，code图/向量物理位置均独立，target确认为本Goal之后新建、仅含1个code_repo manifest，primary7个文档含2知识都在。
- 按官方方法清code dataset搜索历史／0个关联session／旧关系账本，调用官方delete_dataset，不改源码、不私写数据库、不手动unlink数据库；SDK维护43700退出0。target Dataset和mapping均退出，图/向量存储不存在，primary原7文档全保持；回执`.local/daily/t5-official-scoped-drop.json` phase=target_dropped_primary_preserved。
- 可用空间恢复约115GiB。此为首轮范围错误的收尾，不重置该问题失败1轮；原公开API长遍历及客户端超时事实保留，不写成其成功。
- 当前准备恢复同一dev服务（以最新工具句柄为准），尚未发第二次索引；下一步正式创建同名独立code group、核对旧活动记录后由Native手动更新。原知识采集／处理／召回不重跑。

## 原日常服务恢复与代码组重建（17:44UTC）

- 新launcher40911、completion40916、official40965，工具session96745，ready；沿用原官方数据根与同owner，未启动第二套日常环境。
- 官方API复核primary仍7份文档且两条已验知识保持；原memlane-code absent。通过正式POST重建同名独立code dataset，ID仍86845625-0288-58a3-b556-744d5c556dc4，createdAt=2026-11-12T17:44:03.114200Z，runtime原关联无需更改；私有回执t5-code-recreated.json。
- 旧pipeline STARTED由官方启动恢复暂视为活跃（距原启动不足3600秒），但它属于已删除的前一dataset世代且旧进程已确认退出。Native正在核对官方dataset创建时间与活动时间，避免误挡新世代，同时保留对真正活跃任务的阻止；不修改官方旧状态、不假称旧任务成功／取消。
- 根执行者已实际通过Chrome Guest打开精简交互稿并查看三栏知识页；此前浏览器surface不可用不再构成设计资产未打开。

- 世代判断定向测试通过：官方createdAt与活动started_at/created_at均明确严格早于新dataset创建才忽略；等于／晚于仍拦截，缺失／坏时间／读取期间重建保持未知。dev open退出0，MyGo增量编译1.4秒，后台原PID未变；第二轮只由Native提交。
- 分页HTTP边界夹具通过：知识及原文各0/30/31/60/61项、隐藏lookahead、offset与最后页、类型过滤后继续翻页；没有声称日常7文档已做>30生产数据实窗翻页。
- 最后只读旧链核对已追加清理清单：旧daemon/MCP/capture进程、Codex/pi旧配置引用、R/V自有内容对象均已退出；官方数据／BGE／当前Python／固定源码归档／nativeCodex保持。

## 第二轮Native手动代码更新已提交（执行中）

Native唯一点击“更新代码图谱”，不是自动恢复，UI随后禁用更新。官方预扫描66个source、0文档、759797跳过；add a47c2275-122f-435a-9d44-84e07a00dd62完成，code_graph_pipeline f4de61f7-c764-4db1-a25f-fd81e7272fed于17:49:27.180960Z启动，Enola50393实际运行。旧0c历史行仍保持原STARTED，不改写成功／取消。预扫描期间一次activity GET15秒超时仅记观察未知，没有重发POST或重启。最终范围、源码出处及pipeline终态待原请求返回后核对，尚不计通过。

## 第二轮代码范围验收失败（同一问题累计2）

官方f4de61f7-c764-4db1-a25f-fd81e7272fed技术执行于17:51:22完成，Enola265/534源候选、2389基础facts加47衍生facts，映射2418datapoints；3277边解析、987外部/未解析目标跳过，不能将这些统计单独当产品通过。

root对全量facts的file字段核对：298个出处，.cache/.local/.mygo已全部排除，但仍有22个build历史依赖清单（go.mod/pyproject.toml/package.json）；当前cmd/internal/scripts/engine实际源码也在。精确元数据t5-round2-scope-metadata.json，facts SHA256 ba36e65a6063325ad88f030551553a6215eb232c616170bc3837ca05fc78ea12。此与首轮“非当前项目生成/缓存文件入图”同一具体范围问题，累计失败2次；不因manifest路径／dataset世代变化重置。暂停再次POST，先只读核对固定Enola官方ignore在manifest路径的实际实现。不能声称整个T5已通过。

### 固定官方manifest扫描器缺口与配置取舍

已证非根glob问题：receipt明确build/已被**/build/**跳过；Enola v0.4.30 [manifestextractor/manifest.go](https://raw.githubusercontent.com/enola-labs/enola/v0.4.30/internal/extractors/manifestextractor/manifest.go) 的Extract丢弃过滤后的file list，独立调用[detectnames.Walk](https://raw.githubusercontent.com/enola-labs/enola/v0.4.30/internal/extractors/detectnames/detectnames.go)，后者只剪dot目录及node_modules/vendor/testdata，不读取ignore。追加build忽略不能解决。

采用官方配置取舍：mcp-arch.yaml显式列出Config.Default的20个extractor中除manifests外其余19个，保留当前与未来默认语言解析；121条ignore不变。当前不提供manifest派生的跨生态SBOM依赖事实，语言符号／调用／导入关系仍保留。此为已授权范围内官方配置选择，不声称修复上游实现，没有自写扫描器、过滤官方图或修改SDK。YAML解析与数量核对通过，尚未提交第三轮；先核对官方手动更新及stale删除语义，防止错误历史节点残留。

固定官方源码已核对普通手动remember会重新Enola，入口没有源码hash前置跳过；pipeline禁用旧processed缓存。extract_code_graph先运行Enola才比较包含配置的snapshot，随后insert-then-stale-sweep删除同repo不再存在的代码节点／关系并最后stamp。故第三轮普通更新原code dataset，不删除／重建、不force、不实验新路径；由Native唯一UI提交。

新增收口交付记录client-simplification-delivery-20261113.md，保留D01最后窗口操作与D08第三轮未完成标识，尚不标Goal complete。最新活动Go test ./...通过（涵盖新世代与分页检查）。

独立图宿主缺陷：Records按钮错用官方level3（plumbing），初次实窗失败1次；改官方level2并名“查看节点”，仅改宿主接线、不改vendor／算法。TS check/build通过，MyGo增量重载后实窗进入实际独立记录与连线，该缺陷复验通过。技术图能操作不解除T5范围累计2次失败。

收尾清理已结束的本轮临时载荷：删除3个已完成编码任务的重复JSONL／stderr／prompt副本，以及已退出的cutover／错误code清理SDK／草案脚本，共13个文件343913字节；官方session／文档保持、原始Agent聊天不动。保留私有运行配置／凭据、一次官方快照、失败metadata／stack证据及小型任务结果；私有temporary-payload-cleanup.json记逐文件摘要，不保存另一份会话正文。

官方后台于17:56:26UTC按其自身启动恢复将首轮0c7e1101-23d0-4649-97f0-19a1e4963103关闭为abandoned（AbandonedPipelineRunError）；这是官方新终态，不是root私改状态，也不把首轮写成成功／主动取消。第二轮f4仍为原completed但范围验收失败。

## 第三轮代码scope新快照通过，终态及实窗收口中

第三次同dataset普通手动更新官方18:00:12完成；配置19解析器中本项目实际命中6类，无manifests。新基础facts2287、276个出处；root全量核对生成/缓存目录违规为0，当前cmd入口、simpledesktop/graph、dev.py、completion端点四关键文件都在。私有t5-round3-scope-metadata.json；facts SHA256 7e8707c92be349b5cae894b1ca1211d70c807a713b645f9c8c703bab429ec796。

官方日志确认insert-then-sweep删除112个过期代码节点，全图2316节点／5597边，3282关系解析、996未解析目标跳过；UI仅显示1000节点子图，不冒称全图。仍待Native全图出处核对、正式pipeline终态ID及当前源码实窗选中，再最后关窗/reopen。前两次scope失败保留累计2，第三轮配置取舍生效不抹去历史记录。

第三轮正式pipeline `807eee39-a3d0-47a6-b597-a0ad9082657f`（add fb2b6208-2761-4345-b4ec-7535b30de81c）官方COMPLETED，18:00:12.774512UTC结束。全量持久化graph复核2316nodes/5597edges，2272个带官方file_path的源节点／276文件，build/.local/.cache/.mygo/.enola/node_modules/dist违规0，四关键源码均有实际节点和line。私有t5-round3-persisted-graph-scope.json；不是仅新facts或1000节点子图抽查。scope第三轮通过，前两轮失败2次保留；待最后实际节点选中与窗口操作。

## 最后窗口生命周期的实际失败与修正（独立问题1轮）

普通close后root实核owner40911、completion40916、official40965不变且两个health200，d01-final-window-lifecycle.json留before/after证据。但随后dev open清旧watcher47937时stop_group的os.killpg抛EPERM，未捕获异常触发launcher finally，96745实际exit1并停止后台。因此关窗已通过但重开失败，不能提前写D01全通过。已证为UI清理异常向服务所有者传播；EPERM与退出时序的确切内核原因未证，不把竞争假设当根因。

修正：stop_group先poll并核对group，不存在时确认wait；EPERM后再次核对，只在确认为消失时继续，真正存活拒绝信号仍失败。UI reopen异常单独捕获，保留未确认退出watcher且不停止服务、不启动第二个UI。7项检查通过，覆盖真实子进程退出、EPERM竞争／活跃拒绝，以及UI失败保留backend。

恢复前取得同数据flock，核对40911/40916/40965/47937 PID及进程组全不存在、两个端口空闲，按d01-reopen-failure-recovery.json小型receipt保留旧所有权后精确移除残留owner记录。没有绕过live锁或未知进程。随后正常start同配置；不重跑模型或代码索引，第三轮scope通过事实保持。

修复后第二轮普通close/open实际通过：66705/66707/66710全程不变、两health200，old watcher66909退出，open85555退出0并新watcher68002，MyGo1.2秒重开；Native实际读同3知识、ada020e3正文及原codex来源，未再索引。私有d01-final-window-round2.json。

随后针对修改过stop_group执行最后明确stop：55607与owner27869均exit0，官方／端点／UI组退出、owner删除、flock释放。立即start29147首次被端口普通bind拒绝；lsof无listener、netstat61255为TIME_WAIT，已证预检查语义错误，作为独立首次失败1轮保留，不归为后台未退出或旧EPERM缺陷。端口预检查改SO_REUSEADDR匹配实际服务重启，真实active listener仍拒绝。新增两项真实socket检查后9项启动器测试通过；旧组及锁再次核对，已正常启动同配置（最新工具句柄为准），不模型／不索引。

## 最终收口（2026-11-13 Asia/Shanghai）

- 最后明确stop实际exit0且旧所属组／owner清理通过；TIME_WAIT预检修正后start36423正常复用原官方数据，launcher69475／completion69477／official69478／watcher69634，两个health200。9项启动器检查通过。Native最终实窗确认3项知识、已验证ada020e3正文及codex_01a1d093真实来源可读，窗口保持运行。
- D01普通close与open第二轮通过，最后明确stop/start通过；EPERM传播与TIME_WAIT误拒绝两个独立缺陷各失败1轮、修正后通过，没有将它们合并凑阈值。
- D08同一范围问题前两轮失败、第三轮通过，官方全量持久化图违规0；官方来源字段与实窗节点选中成立，不声称本地文件／编辑器打开。当前不提供manifest派生SBOM事实。无第4次索引，无新增模型调用。
- D02–D04沿用真实采集→官方保存→后续召回采用证据，不重复模型补材料。D05分页仅HTTP边界检查，不声称超过30真实文档实窗翻页。D06/D07/D09运行引用、旧数据及源码退役核对完成，开发前tag保持。
- 交付入口为client-simplification-delivery-20261113.md，含准确命令、数据与owner、tag、D01–D09、删除／保留清单及能力边界。最终提交后结束本Goal；不进入release、不推送，保留其他会话未跟踪errors文档。
