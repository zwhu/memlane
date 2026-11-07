# P0 工程验证记录

验证日期：2026-11-07。环境为 macOS 26.6.2（25G83）、Apple Silicon。配置中的最低系统版本为 macOS 15，尚未在 macOS 15 或 Intel 上验收。

已建立真实 MyGo 客户端、独立 Go 服务和外置 Cognee worker。**按2026-11-07用户最新确认范围，P0核心工程流程已通过**：组件自动准备→真实引擎就绪→合成原文保存/索引/检索→项目与当前版本正确→后台保留/停止→受控更新失败恢复及实际替换重启，均有可复现证据；完整主包及三类体积已同步。系统按钮、拖动和拼音也已通过。用户明确要求停止旁白等旁支验证，以主流程走通为本轮标准；VoiceOver尝试未通过，保留失败并移出本轮必要门槛。

这里通过的是P0工程链路。App内真实记忆新增/编辑/查看仍属P1，Codex/pi等真实Agent往返属P2；未接入或修改真实Agent。正式Developer ID、公证及发布源未配置，不能将本机基线描述为正式发布通过。

## 完整轻量主包与准备后体积

`python3 scripts/build-client.py --out build/p0-final` 构建 UI、组件管理、更新器和轻量 Go helper，补齐 `Contents/Library/LaunchAgents` 后重新签名 App 与生成 DMG。生产包不包含 e2e 自动化、Cognee、模型、重型原生库、编译器或开发缓存；业务 SQLite 由独立 helper 链接，UI 只使用轻量 IPC。

| 项目 | 实测 bytes | 边界 |
| --- | ---: | --- |
| 完整主 DMG | 9,029,309（9.03 MB） | 本机 arm64 ad-hoc 包；历史 3.53 MB 不再代表完整包 |
| App 普通文件逻辑大小 | 19,427,850（19.43 MB） | 包含 UI、helper 和 LaunchAgent |
| Rust 路线首次额外组件下载 | 285,752,286（285.75 MB） | worker 66,253,324 + 模型 219,498,962；真实首轮传输与清单一致 |
| Python 对照路线额外组件归档 | 584,328,409（584.33 MB） | 已独立归档实测，未将它冒充 App 默认路线 |
| 安装准备后普通文件逻辑大小 | 511,050,528（511.05 MB） | 完整 App + 实际组件、模型、设置、缓存及一条合成原文/索引 |
| 安装准备后文件分配指标 | 518,934,528（518.93 MB） | `st_blocks × 512`；目录元数据未计，不等于 APFS 共享块删除后释放量 |

DMG SHA-256：`1daad9faba662c67662abaa0fabf9bf51f726686ef6be073249bad9c9cd676be`。报告 `build/p0-engine/final-sizes.json`；完整重测命令见开发说明。运行目录已保留实际缓存，但已安装归档由组件管理器清理，不能再额外把下载归档重复加到磁盘占用。DMG 下载文件单列，未计入运行目录；失败重试、HTTP/TLS 开销也不包含在成功首次下载归档数中。**主包轻量不代表总下载量小**。

`codesign --verify --deep --strict` 对主 App、Rust worker、ORT 与 Python worker 均退出 0，实际签名为 ad-hoc/linker-signed，TeamIdentifier 未配置；`spctl --assess --type execute --verbose=2` 对这些产物均退出 3、`rejected`。报告 `distribution-assessment.json`。此结果只完成系统限制评估，尚未通过正式 Gatekeeper 分发信任；未去除用户产物的 quarantine、绕过系统策略或自动发布。Developer ID、公证及正式更新源仍需发布阶段条件。

## 初始主下载包测量（未加入新增helper）

构建：`sh scripts/go.sh tool mygo build -skip-notarize`，MyGo `v0.7.4`，Go `1.27.3`，版本 `0.0.1`，arm64，ad-hoc 签名。

| 项目 | 本轮结果 | 统计边界 |
| --- | --- | --- |
| 主 DMG | 3,527,168 bytes，约 **3.53 MB / 3.36 MiB** | `build/darwin-arm64/memlane-0.0.1-arm64.dmg` |
| App 文件合计 | 11,222,226 bytes，约 **11.22 MB / 10.70 MiB** | `.app` 内普通文件逻辑大小合计，非 APFS 实际分配空间 |
| 首次准备额外下载量 | 总量未测得；本次引擎候选约 80.94 MB | 还要计入模型及最终 worker 差异，尚未由 App 自动准备 |
| 安装并准备后的最终磁盘占用 | 未测得 | 必须计入引擎、模型、初始数据与缓存；不能用 App 大小代替 |

DMG SHA-256：`f9707fbd9d2d6e0e59ada412423e37815843ad92b2afb30fbb1d3e3c68065131`。重新构建可能改变归档散列，表格只记录本次产物。

当前包包含 Native UI、菜单栏、页面状态、临时草稿和 `mygo.Updater` 检查入口。不含 Cognee、模型、Rust/Go 编译器、Python 或开发缓存；也尚未加入业务 SQLite、Go 后台服务、组件下载器和更新安装链路。因此只能作为起步体积，不能承诺完整首版仍为 3.53 MB。

`codesign --verify --deep --strict --verbose=2 build/darwin-arm64/Memlane.app` 返回 `valid on disk` 和 `satisfies its Designated Requirement`。这只验证本机签名完整性，尚未完成 Developer ID 签名、公证或 Gatekeeper 的正式分发验收。

已使用 `open -n -W build/darwin-arm64/Memlane.app` 由 macOS 启动实际打包产物，确认 `.app/Contents/MacOS/Memlane` 进程运行，并通过应用的 quit Apple event 正常退出。未将应用复制到系统 Applications 目录。

## 窗口与交互

`sh scripts/go.sh test ./...` 与 `sh scripts/go.sh vet ./...` 已通过。无窗口交互测试覆盖中文组合输入、Unicode 复制、页面切换保留草稿、未连接状态和缩放控制。它不等价于真实系统输入法候选窗与辅助功能验收。

真实窗口用仅在 `e2e` 构建启用的 MyGo automation 验证；正式包不包含该插件。命令见 [开发说明](development.md)。

| 项目 | 结果 |
| --- | --- |
| 真实窗口创建 | `kind: content`，1200 × 780；实际渲染截图 2400 × 1560 |
| 宽幅首页与三栏页面 | 已显示，采用连续浅蓝侧栏、单色图标与按钮 |
| 草稿跨页保留 | 使用合成输入，切换设置再返回后文本保持一致 |
| 服务未就绪 | 保存按钮 disabled，无虚构保存成功或连接结果 |
| 更新入口 | 未配置更新源时明确显示「此版本尚未启用自动更新」 |
| 关闭与重新打开 | 主窗口关闭事件隐藏窗口，进程保持运行；重新打开后可见 |

窗口关闭验证中，原生 `RoleClose` 使用 AppKit responder chain，自动化环境下窗口未获焦，菜单执行没有关闭目标。文件菜单改为显式调用主窗口的 `Close()`，仍经过正常 `OnClose` 事件；并增加打开窗口菜单。关闭后的 `visible == false`、重新打开后的 `visible == true` 均通过断言。该结果不是对系统红色关闭按钮的实际点击验收，也不是完整后台服务生命周期验收。

截图在本机 `build/p0-screenshots/native-home.png`、`native-draft.png`、`native-settings.png`，报告为 `native-verification.json`；均是可由脚本重新生成的忽略产物。macOS `screencapture` 区域截图未成功，本轮使用应用 renderer 截图，因此未据此验收系统红绿灯。

本段保留初始窗口基线。后续长文、键盘内容事件、侧栏收起、缩放、材质、系统按钮、实际拖动和系统输入法的增量通过证据见E；VoiceOver失败与本轮范围调整也独立记录。

## Cognee-RS 初始CLI实验（历史基线）

固定上游提交 `151905c302fc9b5b9aad88d6b13931c09f0eac30`，Rust `1.91.1`。最终构建关闭默认 feature，开启 `sqlite,ladybug,lancedb,onnx-dynamic,hf-tokenizer`；保留 SQLite、Ladybug、LanceDB 与 ONNX 推理接口，ONNX Runtime 作为独立动态库提供。

`python3 scripts/bootstrap-engine.py` 在依赖准备完成后重跑通过，输出 Rust 1.91.1、CMake 4.1.2 与 protoc 33.1；首次安装流程由等价的隔离命令完成，完整脚本尚未在空缓存环境从头验收。源码 tar 的 SHA-256 固定为 `d190a15e282933a1066065ab454fba5831346e9e5bb63ba137cda5c04f644955`。

上游未提交 `Cargo.lock`；本轮解析结果已归档于 `deps/cognee-rs/Cargo.lock`，SHA-256 为 `2a94e309b400cae70792681b836b129788035ba8232fa6439b5d3177052fe7f6`，构建脚本固定使用该文件和 `--locked`。

初始 `onnx` 静态链接构建在 ONNX 预编译库下载处连续数分钟没有进展，主动停止该次构建，改用上游已有的 `onnx-dynamic`。数据库编译产物得以复用，最终 release 构建成功。初次下载在上游默认系统缓存生成的临时文件已移回仓库 `.cache/ort-interrupted`；后续脚本显式设置 `ORT_CACHE_DIR`。

动态库使用微软官方 [ONNX Runtime 1.28.0 ARM64 归档](https://github.com/microsoft/onnxruntime/releases/download/v1.28.0/onnxruntime-osx-arm64-1.28.0.tgz)，下载为 32,396,562 bytes，SHA-256 为 `1268b359718099bde2cedb55787f182a130067bc4f31e8c88478c445b850d3d8`；已实际校验。该归档包含开发文件和重复库文件，实验组件仅打包所需的版本化动态库与许可文件。

### 独立组件测量

`python3 scripts/package-engine.py` 生成实验归档；它不是已经可发布的 Memlane worker，尚未加入 JSONL 协议适配与 App 自动准备流程。

| 项目 | 大小 |
| --- | --- |
| Rust CLI | 193,221,392 bytes，约 193.22 MB |
| ONNX 动态库 | 39,312,136 bytes，约 39.31 MB |
| CLI＋动态库＋许可文件合计 | 232,872,111 bytes，约 **232.87 MB** |
| gzip 实验归档 | 80,940,344 bytes，约 **80.94 MB** |
| 模型 | 未选定、未下载，不包含在上述数值中 |

本次 `build/p0-engine/cognee-cli-p0-darwin-arm64.tar.gz` 的 SHA-256 为 `5c140a4f610853bf3794560c84bcf81021d034938fe8584c2b886dc4b8cfb6a0`。归档时间戳会影响重新打包后的散列。这只是当前候选体积，并非已完成最小化的定版结果。

### 独立启动与存储检查

`python3 scripts/verify-engine.py --archive build/p0-engine/cognee-cli-p0-darwin-arm64.tar.gz` 从实验归档提取 CLI 与动态库到独立临时目录，使用不含开发工具链路径和凭据的环境；配置、日志和数据均隔离。

- CLI 正常返回 `cognee-cli 0.2.0`；`otool -L` 未发现开发目录下的链接依赖。
- 两个独立 CLI 进程向 `p0-alpha`、`p0-beta` 分别写入合成原文，均退出成功；退出后只读检查 SQLite，两个 dataset 分别拥有一条记录，持久化文件与原文一致。
- 复制后的 ONNX 动态库可加载，返回版本 `1.28.0`，C API 27 存在。此检查没有执行模型推理。
- 未配置 LLM/图谱提取器时，`cognify -d p0-alpha` 返回退出码 1，报错包含 `no usable LLM is configured` 和 `local gliner extractor, but none is registered in this build`（原日志中 gliner 带反引号）。

因此本轮证实了基础存储和可重定位启动，**没有证实完整检索契约**。默认 `cognify` 仍不能直接承担当前无 LLM 的原文索引要求，后续应验证低层索引入口与模型请求桥接。dataset 归属检查也不能替代跨项目检索隔离验收。编辑删除、旧版本过滤、准备后离线推理和模型效果仍待验证。

观察到默认关系库文件是当前工作目录下的 `cognee.db`，仅修改 system/data root 不会移动它；worker 适配必须显式配置数据库位置。本轮工作目录为临时目录，未写入真实知识库。报告在 `build/p0-engine/verification.json`，临时测试数据已清理。

## A：原文索引与模型基线通过

`sh scripts/build-index-probe.sh`与`python3 scripts/verify-index.py`已通过。固定E5-base int8 revision `1ec9243030a27d1a115d5c340572074c125b58b2`，768维mean/L2、query/passage前缀、512长度、384tokens切片及48重叠。实际Rust ONNX/LanceDB索引，不执行cognify、LLM或图谱抽取；原9条中英/标识符查询Top-1与Top-3均9/9，长中文无空格正文7片、末尾标识符和来源字节范围通过，跨进程持久化通过。

首次模型加载1,618.50ms，后续独立进程约514–521ms（OS缓存仍在）；索引1,571.79ms，峰值RSS1,513,275,392 bytes。必要模型文件295,732,082 bytes，Python另需同revision配置698 bytes。报告`build/p0-engine/raw-index-verification.json`。上游固定三输入/Pad0需要Memlane补丁，前后源哈希与补丁见`deps/cognee-rs/patches.json`。

BGE中文int8英文查询失败，E5-small int8与FP32完整检索均8/9，因此没有选作默认；smoke成功不代表完整检索通过。失败证据仍保留。本检索集只证明小规模合成基线，不代表所有项目文本质量。

## B：项目隔离与当前版本契约通过

`python3 scripts/verify-memory-contract.py`已通过共享Go SQLite/store/service与真实Rust模型实验。A/B、同文不同项目、显式全局和空scope错误均通过；编辑、删除后故意保留旧chunk，仍即时排除失效版本。恢复旧正文追加revision4；旧相同正文、伪造绑定及片段正文不得生效。实际删除派生chunk通过。

实际移走仅测试目录的引擎触发索引/读取错误，原文与失败任务保留；稳定opid重试只有一版本，异文同键冲突。真实索引中并发编辑，迟到回执不能标记旧版本有效；新版本重索引、Go重启持久化通过。报告`build/p0-engine/memory-contract-verification.json`。竞争测试的第二fixture进程只针对合成数据库；生产仍为单数据库拥有者。

## C：独立worker与实现对照

Rust独立归档的`hello/ready`、版本/协议握手、索引/可追溯检索、错误、1ms超时、取消、JSONL分片、第二拥有者拒绝、SIGKILL后重启、派生删除、正常shutdown与缺模型永不ready均通过。真实1ms长文期限3.20ms返回timeout；取消没有完成索引回执，已开始的单批推理可能短暂继续。报告`build/p0-engine/worker-verification.json`。归档动态链接未指向开发目录。

Go客户端实际store→JSONL worker→绑定检索通过；10ms期限12.22ms报错，原文未标indexed且失败job保留；Close后系统确认PID消失，读取返回错误；重启绑定仍有效，99.0.0版本拒绝。报告`build/p0-engine/client-verification.json`。后续新增私有HOME及P0离线子进程选项，不重跑无影响的原文/业务基线。

Python3.12.12、Cognee1.6.7、FastEmbed0.9.1、PyInstaller6.16.0已用171包哈希锁隔离安装，不替换默认环境。官方源低速安装明确终止后，只换清华镜像，版本/哈希保持一致。源码worker在系统拒绝网络下约9.75秒ready；完整原9条查询Top-1/Top-3均9/9，7片长中文、来源范围及重启通过，索引2,274.86ms、首次加载7,200.73ms、峰值RSS1,409,908,736 bytes。报告`python-source-offline-diagnostic.json`和`python-source-corpus.json`。源码的B1–B5同规则对照也通过，报告`python-source-memory-contract.json`：实际停止worker造成失败，原文/任务保留，重启重试正确；旧版本及迟到回执排除。

**Python独立归档协议、原9条检索及B1–B5业务对照均已通过**。首次和修订归档启动都在60秒内未ready，后者使用操作系统网络拒绝；已保存`python-worker-verification-startup-failed.json`、`python-worker-verification-offline-startup-failed.json`及对应corpus失败文件。直接冻结目录加本地成本表曾26.60秒ready，但不能替代独立归档/离线验收，也未证明唯一根因。初始化栈显示PyArrow原生模块加载，归档已改为保留29条受清单约束的内部链接，避免重复副本。修正后独立协议在OS网络拒绝下通过，首次初始化54.46秒、1ms请求实际32.29ms报timeout，仍使用原60秒就绪条件；原失败证据保留。报告`python-worker-verification.json`；独立原9条查询Top-1/Top-3均9/9，7片长中文、来源范围及重启通过，首次加载28.07秒、索引2,104.78ms、峰值RSS1,425,424,384 bytes，报告`python-worker-corpus.json`；不换模型、语料或降低阈值。Cognee_ENV_FILE明确指向新生成的隔离空文件，初始化后删除，禁止现存.env向上搜索。源码成功不等于可分发。

Rust固定源码未发现Enola/index_repo等价入口，两个worker实际code_graph均明确unsupported且graph_ready=false。独立Enola0.4.30/extractor v351在合成Go项目抽出3个fact，main→greet调用关系通过，报告`enola-verification.json`。它实际写项目`.enola`及隔离HOME`.enola`；Cognee图谱入库/查询、自动刷新、外置输出仍需适配。此项记录能力缺口，不删除原产品功能，也不静默切换实现。

Python独立归档业务报告`python-memory-contract-verification.json`与源码执行相同规则；实际故障用停止worker制造，未修改业务规则或检索阈值。初始化首次调用55.96秒，后续重启约2.42秒。相关已通过项不重复。

## 独立组件体积

| 产物 | gzip bytes | 解包文件bytes | 验收状态 |
| --- | ---: | ---: | --- |
| Rust worker＋ORT＋许可 | 66,253,324 | 195,652,569 | 独立进程契约通过 |
| E5-base int8模型组件 | 219,498,962 | 295,915,812 | 固定文件及归档校验，App 实际准备与离线推理通过 |
| Python冻结worker＋原生依赖＋Enola | 364,829,447 | 922,836,095 | 内部链接29条单列；独立协议与原9条检索通过，业务归档通过 |

Rust＋模型 gzip 合计 285,752,286 bytes，App 首次实际准备也传输这些组件；Python 候选＋模型合计 584,328,409 bytes。均不含主包。正式 Developer ID、组件公证与发布更新签名未配置，不自动发布；完整主包和实际运行目录测量见上表。

## D：组件、后台与更新

组件管理器的受控签名源 6 步真实下载、安装、128KiB 中断/Range 续传、校验失败重试、未授权拒绝、内部链接安全、两进程独占与离线索引检索通过，`component-verification.json`。离线复用传输 0 bytes。已通过且未受改动影响的 D1 与 A/B/C 不重复。

独立 memlaned 的私有 Unix IPC、同 uid 校验、确认门禁、持久任务队列、worker 崩溃恢复、三次熔断、主动停止持久化，以及活动索引中 SIGKILL 服务后 worker EOF 退出/新服务补发通过，`daemon-verification.json`。

真实 SMAppService 对独立测试标识注册返回 enabled，注销返回 not_registered；App 组件 prepare 返回 ready/enabled，App 退出后由托管服务实际保存并检索合成原文，`system-prepare-verification.json`。首次 not_found 先核对实际包内 plist 再注册；缺失布局仍报错。

App 准备 UI 的 503 失败/可重试、真实 285.75 MB 总量与下载进度、下载中取消、重试就绪和空缓存离线不误报 ready 均通过，`preparation-ui-verification.json`。取消后实际保留 2,686,976 bytes 部分文件；重试使用已准备组件和部分下载。取消展示中文状态，不再直接展示 context canceled。补齐独立大小行后，受控源已停止时仍从签名缓存实际显示“引擎66.25MB · 模型219.50MB”和285.75MB总量，保留主动停止；`offline-app-preparation.json`。只重验受影响的布局，未重复原下载故障与长文验收。

真实窗口关闭隐藏并可重新打开，后台继续运行；默认退出 UI 后原文 revision 1 和检索保持。“停止服务并退出”及关闭“退出 App 后继续运行”均等待服务/worker 退出、持久化 stopped，原文只读核对通过，`lifecycle-verification.json`。文件菜单和托盘使用同一停止退出逻辑。

受控 HTTPS 更新的错误签名与128KiB短传失败均保留旧 App/原文并恢复后台；实际0.0.1→0.0.2替换、签名核对、新App后台ready及组件GET不增加通过。主动停止时0.0.2→0.0.3实际替换后仍stopped，原文保留，`update-verification.json`。只读补查使用服务拥有者锁确认退出且无待提交WAL后immutable打开，integrity_check=ok、原文revision1正确、数据库SHA不变；保留原mode=ro错误，没有重复0.0.3替换。GUI按钮的错误签名恢复与0.0.3→0.0.4实际替换、旧UI退出、Launch Services自动启动新UI（PID90472）、后台ready、原文检索与组件复用通过，`gui-update-verification.json`；新UI测试结束先明确停止服务再退出。

## E：窗口补验与剩余条件

实际 Native renderer 的侧栏收起/展开、80%–140%缩放和980×620最小窗口渲染通过；直接AppKit查询存在NSVisualEffectView、material=7（sidebar），正文不透明。5000字符合成长文全选、Tab焦点移动、跨页草稿保留及实际NSAccessibility的AXTextArea/AXButton暴露通过，`native-ui-verification.json`。键盘输入走MyGo内容事件，未将它冒充系统输入法候选窗或VoiceOver。

当前环境窗口未获系统焦点，读取检查器可能见旧帧；测试先捕获实际内容帧再核对状态。最初e2e发布包剔除检查器，修正为仅验收包`MYGO_INSPECTOR=1`且保留production资源路径。长文8秒及60秒超时最终定位为验证器未缓冲SocketIO单次write不能保证大JSONL帧完整，改sendall后原5000字符通过；主线程采样空闲，未据此错误宣称产品长文计算很慢。原失败与`native-content-timing.json`保留。

原生探针曾遍历系统RemoteUIElement触发未支持selector异常，已限制到本App的MyGoSurfaceView。早期performClick等待超时、权限false为历史失败。用户为宿主iTerm2开启权限并解锁后，授权环境四项权限实际true；系统操作已增量验收，原失败保留，见[原生操作清单](p0-native-manual.md)。

`verify-native-system.py`独立minimize/close/drag/fullscreen通过：真实CG鼠标点击红色隐藏/黄色最小化且进程保留，系统Dock AXPress恢复/重开；侧栏顶部与正文工具栏分别移动35×30，导航点击不误拖；绿色点击进入系统全屏，真实视图菜单AXPress退出并恢复1200×780。Ctrl+Cmd+F曾超时，未将菜单通过写成快捷键通过。报告`native-system-verification.json`与系统窗口截图。

`python3 scripts/verify-system-ime.py --system-home`退出0：正常系统GUI环境、合成配置和数据根；真实拼音键码输入xiangmu，实际候选面板显示“项目”，位于正文插入点附近；空格提交项目，再输入jingyan提交经验，删除后正文项目经；切ASCII输入request_id，跨页保留“项目经 request_id”，原输入源恢复。面板在本机由测试App PID持有、layer20、397×28，原脚本只查SCIM进程漏掉它，未据此修改产品输入代码。实际候选截图`system-pinyin-candidate.png`及正文截图`system-ime-committed.png`，报告`system-ime-verification.json`。隔离HOME失败不等于真实客户端输入失败；没有用Unicode直接注入替代系统拼音。

VoiceOver尝试未通过：首次欢迎/系统开关、实际读屏进程及AppleScript/字幕输出路径均保留诊断；不能将开关true或NSAccessibility暴露写成朗读成功。用户最新指示已停止本旁支，列为后续增强，报告`voiceover-verification.json`保留失败。测试进程和系统开关清理结果见`ancillary-cleanup.json`。

本轮以用户最新确认的P0主流程范围收口；未通过的旁白和正式分发条件各自记录，不冒称通过。只用合成数据和隔离目录，不接真实Agent或真实记忆库；其他主机管理只保留在Roadmap。逐步命令、失败、范围调整和最终核对见[P0执行记录](p0-execution.md)。

最终只读证据审计`build/p0-engine/p0-mainflow-final-audit.json`核对16份真实功能报告及其SHA、最新DMG实际大小/散列、P0必要清单和系统清理结果，退出0；不等同于重新执行功能验证。相关`go test ./cmd/memlane-app ./internal/desktop`、`go vet`、Python语法与`git diff --check`通过。最新主App本机codesign完整性通过，spctl仍实际rejected，正式签名/公证条件未具备。
