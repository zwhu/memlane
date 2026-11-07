# P0 逐步执行记录

2026-11-07 继续执行。用户已明确原要求中的 `/handsoff` 指 `/handoff`。每个步骤以及修复、重试均读取仓库 `.trae/skills/handoff/SKILL.md` 并实际生成交接文档，保存在系统临时目录的 `memlane-p0-handoffs/`。本会话没有独立的 Skill 调用工具，执行证据是工具读取与规定文档的实际生成，不声称调用了不存在的工具。该 skill 的 `disable-model-invocation: true` 禁止自行触发；本轮由用户明确要求执行。

范围与已通过基线引用 [实施清单](implementation-plan.md) 和 [P0 验证记录](p0-validation.md)。没有重复执行未受改动影响的客户端测试及已有存储检查。所有模型实验仅用合成文本，配置、工作目录和运行环境隔离。

## A1：BGE 中文小模型候选（未通过）

- handoff：系统临时目录 `memlane-p0-handoffs/01-a1-model-and-index.md`。
- 改动：`deps/models/bge-small-zh-v1.5-int8.json` 固定 ONNX 转换仓库 revision、文件大小及 SHA-256；`engine/rust/embedding-probe.rs` 实际调用固定 Cognee-RS ONNX provider；增加构建、模型准备和推理验证脚本。`build-engine.sh` 增加构建参数透传，保留原 features 与锁文件。
- 已执行命令：`sh scripts/build-embedding-probe.sh`，退出 0，增量构建 5.95 秒；`python3 scripts/bootstrap-model.py`，退出 0，已缓存文件全部通过校验，本次额外下载 0 bytes。
- 模型首次从固定 revision URL 下载，核对 ONNX LFS SHA-256 和小文件 Git blob SHA-1 后写入完整 SHA-256 清单。合计 24,451,740 bytes；这是被评估的候选，不代表最终模型大小。
- 功能命令：`python3 scripts/verify-embedding.py`，退出 1；实际错误 `AssertionError: [[0, 1, 2], [2, 1, 0], [2, 1, 0]]`。ONNX 实际加载和推理完成，返回 512 维、有限且归一化的向量；中文与标识符查询正确，英文 `HTTP retry strategy` 错将 `getUserInfo` 记录排在重试策略前面。
- 结论：不得勾选模型验收。此初版验证脚本在断言失败前未写出 JSON；失败证据是实际命令输出，后续修正为成功和失败均写报告，不补造本次不存在的分数或资源数据。

## A1-R1：多语言候选与失败报告（模型 smoke 已通过）

- handoff：系统临时目录 `memlane-p0-handoffs/02-a1-retry-multilingual.md`。
- 目标：核对多语言模型的输入、padding、池化与前缀，保持原查询及 Top-1 判定要求；失败报告同样落盘。完成后补充真实命令与结果。

- 中断后继续的 handoff：系统临时目录 `memlane-p0-handoffs/03-a1-resume-multilingual.md`。
- 已执行 `sh scripts/build-embedding-probe.sh`，退出 0，增量构建 6.87 秒。探针按模型 `recipe.json` 初始化，不使用全局 Cognee 配置或自动模型回退。
- 已执行 `python3 scripts/bootstrap-model.py && python3 scripts/verify-embedding.py`，退出 1。缓存复用下载 0 bytes；BGE 英文失败已复现并保存于 `build/p0-engine/bge-small-zh-v1.5-int8-embedding-verification.json`。模型加载 926.66 ms，三条正文向量化 50.92 ms，三条查询 47.06 ms，子进程峰值 RSS 137,265,152 bytes。英文重试正文分数 0.43119，错误标识符记录分数 0.44589。
- 多语言候选清单为 `deps/models/multilingual-e5-small-int8.json`，固定转换 revision `761b726dd34fb83930e26aab4e9ac3899aa1fa78`；384 维、mean pooling、L2 normalization、query/passage 前缀，单批一条、最大 512 tokens。模型与必要文件合计 135,392,650 bytes，正在实际下载并校验。该值不代表最终首次组件准备总量。
- 增加 `scripts/p0-handoff.py` 便于每个小步骤实际读取 skill 并生成独立临时交接文档，使用方式：`python3 scripts/p0-handoff.py <step-slug> '<下一步用途>' --result '<当前结果与遗留问题>'`。

- `python3 scripts/bootstrap-model.py --manifest deps/models/multilingual-e5-small-int8.json` 退出 0，首次实际下载并校验 135,392,650 bytes。
- `python3 scripts/verify-embedding.py --manifest deps/models/multilingual-e5-small-int8.json` 退出 0；原三条中文、英文、标识符查询 Top-1 全部正确。报告 `build/p0-engine/multilingual-e5-small-int8-embedding-verification.json`。模型加载 1,023.73 ms，三条正文 116.73 ms，三条查询 127.46 ms；峰值子进程 RSS 796,983,296 bytes。较小的主安装包不意味着模型内存或首次下载也很小。
- 该步骤只通过模型 smoke，尚未验证持久化原文索引、长文、范围及版本过滤，不勾选完整模型基线或 Cognee 检索契约。

## A2：持久化原文索引（完整检索集未通过）

- 开始与结束均实际执行 handoff；本步开始为 `*-a2-durable-index.md`，位于系统临时目录 `memlane-p0-handoffs/`。
- 新增 `engine/rust/backend.rs` 与 `index-probe.rs`，通过固定 Cognee-RS ONNX 与 LanceDB 低层接口索引原文；不调用图谱提取或完整 cognify。按显式 dataset UUID 分目录存储，chunk 元信息含 memory、revision、generation、原文字节范围和 chunk UUID。
- 长文按本地分词器计数，在 Unicode 字符边界切分 384 tokens、48 tokens 重叠；额外检查含前缀后的模型长度，不能静默截掉正文尾部。
- `sh scripts/build-index-probe.sh` 退出 0，增量构建 13.78 秒。
- `python3 scripts/verify-index.py` 退出 1，真实索引 9 条合成正文，长中文无空格记录分成 7 片；索引阶段 845.75 ms。不同子进程的前三条中文、英文、代码标识符查询 Top-1 正确，字节范围与原文一致。
- 第四条英文 `indentation preference` 未在 Top-3 命中中文缩进规则，完整 A2 未通过。失败报告保存在 `build/p0-engine/raw-index-e5-int8-failed.json`；峰值 RSS 1,161,838,592 bytes。不能用 A1 的 smoke 替代完整效果验证。

## A2-R1：对照未量化权重（进行中）

- handoff：系统临时目录 `memlane-p0-handoffs/*-a2-r1-fp32.md`。
- 同一模型转换 revision，改为 FP32 ONNX，固定哈希见 `deps/models/multilingual-e5-small-fp32.json`；模型及配套文件合计 487,352,998 bytes。保留 int8 失败报告，不改检索集与命中阈值。
- 修改验证脚本为所有查询均记录，失败不提前停止，以完整报告定位差异；跨语言查询及标识符 Top-1 判定保持不变。

- generation 一致性修正单独执行了 `*-a2-r1-generation.md` handoff：generation 现在绑定模型文件哈希、模型 revision、池化、维度、前缀、长度及切片配置，以 UUID 作为目录名。修改任一配方会打开独立索引，不混用旧向量。
- `sh scripts/build-index-probe.sh` 退出 0，13.10 秒；修正后执行 `*-a2-r1-int8-full.md` handoff 并运行 `python3 scripts/verify-index.py`，退出 1。9 条查询有 8 条 Top-1/Top-3 正确，仅原英文缩进查询失败；7 片长中文末尾标识符命中 Top-1。完整失败报告 `build/p0-engine/raw-index-verification.json`，第一次失败仍保留在 `raw-index-e5-int8-failed.json`。

- `python3 scripts/bootstrap-model.py --manifest deps/models/multilingual-e5-small-fp32.json` 退出 0；复用相同分词器和配置文件后，本次额外下载 470,268,533 bytes，完整模型目录文件清单大小为 487,352,998 bytes。
- `python3 scripts/verify-index.py --model .local/models/multilingual-e5-small-fp32` 退出 1：完整9条查询仍是8条Top-1/Top-3，仅原跨语言缩进查询失败。真实索引阶段1,286.71 ms，模型首次加载1,668.29 ms，峰值RSS 2,094,104,576 bytes。报告 `build/p0-engine/raw-index-e5-fp32-failed.json`。
- 结论：量化不是唯一原因，FP32也未通过且资源占用更大，不选作默认。

## A2-R2：E5-base int8 对照（进行中）

- handoff：系统临时目录 `memlane-p0-handoffs/*-a2-r2-e5-base.md`。
- 目标：保持9条合成查询和原Top-3/标识符Top-1要求，对照更强的多语言模型，并继续报告额外下载与峰值内存；不更换 Rust 引擎路线。

- E5-base 清单为 `deps/models/multilingual-e5-base-int8.json`，转换 revision `1ec9243030a27d1a115d5c340572074c125b58b2`，768维 mean pooling、512 token上限。模型与必要文件合计295,732,082 bytes，实际下载中。
- 后续SQLite依赖元数据查询独立执行 `*-dependency-sqlite-proxy.md` handoff：默认Go代理超时，使用开发说明中的单次 `GOPROXY=https://goproxy.cn,direct sh scripts/go.sh list -m -json modernc.org/sqlite@latest` 成功，候选固定为v1.61.1；尚未安装或开始B验收。

## 原文保存与绑定前置模块（功能待验收）

- 在模型下载期间独立执行 `*-a2-save-prerequisite.md`、`*-a2-save-check.md`、`*-a2-save-check-r1.md`、`*-a2-save-probe.md` handoff。没有提前勾选B。
- 固定 `modernc.org/sqlite v1.61.1`，模块缓存和Go工具链仍在仓库；依赖下载返回0，过程中HTTP/2提示 `received DATA after END_STREAM`，下载最终成功。
- 新增 `internal/store` 的原文事务保存、不可变版本追加、索引绑定和持久索引任务；`internal/memory` 排除失效状态、过期版本、未绑定chunk和原文字节范围不匹配的候选。UUID规范化，恢复追加版本。
- 新增 `cmd/p0-memory-probe`，仅作为合成功能验收入口，不进入主安装包；连接同一store/service与真实Rust索引进程，全部路径需显式绝对路径，子进程不继承凭据。
- `GOPROXY=https://goproxy.cn,direct sh scripts/go.sh test ./internal/engine ./internal/store ./internal/memory` 退出0，但输出为 `no test files`，只作构建检查，不算功能验收。
- `sh scripts/go.sh fmt ./cmd/p0-memory-probe && sh scripts/go.sh build -o build/p0-engine/memory-probe ./cmd/p0-memory-probe` 退出0。版本和范围功能仍必须用真实合成操作独立验收。

- `python3 scripts/bootstrap-model.py --manifest deps/models/multilingual-e5-base-int8.json` 退出0，首次下载并校验295,732,082 bytes。
- `python3 scripts/verify-index.py --model .local/models/multilingual-e5-base-int8` 首次退出1，实际模型推理失败 `Invalid input name: token_type_ids`。失败报告 `build/p0-engine/raw-index-e5-base-input-failed.json`，不得算模型验收。

## A2-R3：Rust ONNX 输入与padding适配（进行中）

- handoff：系统临时目录 `memlane-p0-handoffs/*-a2-r3-onnx-inputs.md`。
- 修正固定provider的三输入假设：加载时核对实际输入集合，只有声明token_type_ids时才传入；padding使用tokenizer声明的token ID而非硬编码0。RoBERTa类模型的padding影响位置计算，必须按实际模型配方处理。
- 补丁保存为 `deps/cognee-rs/onnx-input-contract.patch`，`patches.json` 记录上游文件、修补后文件和补丁哈希。`scripts/apply-engine-patches.py` 仅处理准确匹配的固定源码，支持已修补文件复用；遇到漂移拒绝覆盖。构建脚本自动调用该步骤，依赖锁未改变。
- 这是已发现的Rust兼容缺口及实际适配，不将Python接口或模型下载成功视为Rust验收。

- `sh scripts/build-index-probe.sh` 应用并核对补丁后退出0，增量重编译1分钟。
- `python3 scripts/verify-index.py --model .local/models/multilingual-e5-base-int8` 退出0，原9条查询Top-1/Top-3均为9/9，跨语言缩进查询也正确；长中文7片，末尾标识符Top-1，字节范围核对通过。索引1,571.79 ms，模型首次加载1,618.50 ms，峰值RSS 1,513,275,392 bytes。报告 `build/p0-engine/raw-index-verification.json`。
- 当前原文模型选定E5-base int8，默认准备与索引脚本已指向固定清单，候选对照仍保留。A原文路径及模型基线通过，不等于完整P0通过。
- 文档同步第一次apply_patch因上下文不匹配失败，未修改磁盘；实际执行 `*-a2-docs-retry.md` 后核对并修正。结束handoff `*-a2-complete.md` 已生成。

## B：项目隔离与版本正确性（进行中）

- handoff：系统临时目录 `memlane-p0-handoffs/*-b-isolation-versions.md`。
- 基于原文事务保存、索引绑定和真实Rust ONNX/LanceDB验证，不把dataset文件归属当作检索隔离。测试需故意保留旧chunk，证明服务排除已失效版本；恢复必须追加新版本。只使用独立临时目录及合成数据。

- `python3 scripts/verify-memory-contract.py` 退出0，实际5类合成功能验收通过；报告 `build/p0-engine/memory-contract-verification.json`。
- B1 handoff在脚本内实际执行，A/B、相同正文分项目、显式全局范围及空范围拒绝通过，15.14秒。
- B2 handoff独立执行，编辑即时过滤旧chunk、删除即时失效、恢复追加revision4、相同旧正文不放行、伪造绑定/正文拒绝和派生chunk实际清理通过，9.24秒。
- B3 handoff独立执行，真实移走仅在测试临时目录的引擎触发错误；原文与失败任务保留，读取错误不伪装空结果，幂等重试及同键不同内容冲突通过，2.53秒。
- B4 handoff独立执行，真实模型索引期间由第二个合成fixture进程编辑，迟到旧revision回执拒绝，新revision重索引正确，3.31秒。该第二进程只用于竞争条件测试，不代表生产采用多数据库拥有者。
- B5在重启验证前由独立工具调用执行handoff；Go进程重启后revision4原文和检索持久化通过。后续脚本已将B5纳入统一逐步记录，增加每步落盘和结束handoff；只改记录流程，未重跑已通过的业务检查。
- B结束handoff `*-b-complete.md` 已实际生成，未勾选尚未执行的Python或独立worker部分。

## C1：独立worker协议（进行中）

- handoff：系统临时目录 `memlane-p0-handoffs/*-c1-worker.md`。
- 下一步把已验证的真实Rust原文backend封装成长期JSONL进程，独立验证握手、就绪、错误、超时、取消和归档运行；Go业务规则继续由既有store/service负责。

- worker首次编译退出101，E0716临时借用错误；独立 `*-c1-r1-jsonl.md` handoff 后修正为长生命周期reader并保留跨事件分支的部分JSONL缓存。取消和超时优先于迟到结果。
- `*-c1-owner-lock.md` handoff 后增加操作系统文件独占锁，进程退出自动释放，不用锁文件存在模拟进程活跃；generation额外绑定适配器版本。
- `*-c1-cancel-cpu.md` handoff 后将长原文切片移入后台CPU任务，避免它阻塞超时/取消响应。已经启动的单批ONNX推理可能短暂继续，不把请求取消说成所有CPU已瞬时停止；后续服务必须有进程树停止能力。
- 修正后 `sh scripts/build-worker.sh` 退出0，增量构建11.55秒。`*-c1-package.md` handoff 后 `python3 scripts/package-worker.py` 退出0：独立归档66,253,324 bytes，解包文件195,652,569 bytes；worker二进制156,000,368 bytes，ONNX动态库39,312,136 bytes。归档不含模型或开发工具，SHA-256 `3b2ecb2763eb1c3b2ba38414240032927f5e50d3ef1be522677aa09bdb0e0e0c`。正式Developer ID及公证未配置，不能当正式发布包。

## Python对照依赖（进行中）

- 独立 `*-c-python-dependencies.md` handoff 后运行 `python3 scripts/bootstrap-python-engine.py`；Python3.12.12安装到 `.local/python`，16.8MiB工具链下载，不替换默认Python。
- 初次依赖解析退出1：Cognee1.6.7要求FastEmbed>=0.9.1,<0.10，与此前候选0.7.4冲突；未将旧版本接口当作当前Cognee能力。
- `*-c-python-r1-fastembed.md` handoff 后固定FastEmbed0.9.1，复用隔离解释器和环境，继续生成带哈希锁文件。Python运行或冻结分发尚未验收。

- `python3 scripts/verify-worker.py` 的C1基础协议验收实际通过：握手/就绪、版本不匹配拒绝索引、真实原文索引检索及来源、缺范围错误、未知图谱操作、畸形JSON和重复请求ID均通过；C2首次读取回执超时，失败报告保存在 `build/p0-engine/worker-verification-buffered-failed.json`。
- 独立 `*-c2-r1-reader.md` handoff 后改验证器stdout为无缓冲读取并保存消息trace，未修改worker基础协议；`python3 scripts/verify-worker.py --from-step c2` 退出0，复用C1已通过项，仅重试C2及后续。实际1ms期限的长原文请求在3.20ms返回timeout，取消请求及原请求cancelled回执均收到，JSONL分片跨完成事件通过。原故障确认是验证器预读第二行后又等待FD导致，未把假等待描述为worker失败。
- C3实际双进程拥有者拒绝、终止后锁释放及索引持久化重启、派生删除、主动shutdown和缺模型永不就绪均通过。独立归档无开发目录动态链接依赖。完整报告 `build/p0-engine/worker-verification.json`。
- Go客户端 `internal/engine/client.go` 已连接真实JSONL契约，具备请求ID路由、就绪与版本核对、独立取消消息、读写时间边界、受限stderr诊断及进程组停止；已构建，正在 `python3 scripts/verify-client.py` 的真实store/worker链路验收。仅编译输出 `no test files` 不作功能验收。
- Python依赖锁解析经修正成功，固定171个包并生成哈希；实际解析4分30秒。正在按哈希下载/安装，尚不称为可运行Cognee worker。

- `sh scripts/go.sh build -o build/p0-engine/worker-client-check ./cmd/p0-worker-client` 与 `python3 scripts/verify-client.py` 退出0：真实Go事务保存→JSONL worker索引→绑定检索通过；10ms请求期限实际12.22ms返回错误，索引标记仍为0且失败任务保留。Close后操作系统确认workerPID不存在，读取返回错误；重启后有效绑定检索通过，版本99.0.0被拒绝。报告 `build/p0-engine/client-verification.json`。
- Rustworker和Go客户端C1已完成，实际 `*-c1-complete.md` handoff 已生成；Python对照和D/E仍未通过。固定Rust源码未查到Enola/code_graph/index_repo实现，独立worker实际code_graph调用返回unsupported_operation；代码图谱缺口不冒充已具备能力。

- Python安装首轮同一批大wheel出现重复下载提示，仍活跃但未完成；独立 `*-c-python-network-retry.md` handoff 后先核对PID44743命令属于本任务，再仅对该进程发SIGINT。原工具句柄确认终止后，调整本任务UV并发4、HTTP超时300秒，复用哈希锁及已校验缓存重试，未改用户网络或默认Python环境。
- Python接口通过固定Cognee1.6.7 wheel的HTTP Range读取核对，不重复完整wheel下载：`index_data_points`接受显式vector_engine；`LanceDBAdapter`接受明确本地URL及EmbeddingGateway，当前索引payload仅保留external_metadata等字段。对照worker将revision/generation/原文字节范围编码到external_metadata，以同一Go业务绑定规则验证。
- FastEmbed0.9.1已核对支持`add_custom_model`、`specific_model_path`及`local_files_only`，无需默认自动模型下载；另需同revision的tokenizer_config与special_tokens_map共698bytes，已固定SHA-256。附加文件不改变Rust推理配方和generation。
- 对照worker `engine/python/worker.py` 使用真实Cognee索引任务、LanceDB及本地FastEmbed，自身封装JSONL与独占锁、超时及取消；尚未运行或冻结验收。

## D1前置代码准备（未验收）

- 在C对照依赖下载期间独立执行 `*-d1-preparation.md`、`*-d1-preparation-trust.md`、`*-d-model-archive.md` handoff。仅准备共用代码，D功能验收仍等待C完成。
- `internal/components` 已实现Ed25519签名Feed、协议及平台兼容、显式授权描述符、离线签名清单缓存、真实下载与Range重试、归档/资产哈希、限量解包及链接路径拒绝、版本目录原子安装和已校验组件复用。当前 `sh scripts/go.sh test ./internal/components` 退出0，但为`no test files`，不勾选功能通过。

- 原模型卡已从固定base revision下载并保留，新增额外tokenizer配置698bytes；`python3 scripts/package-model.py` 退出0，独立模型归档219,498,962 bytes，解包295,915,812 bytes，SHA-256 `a39b77bb0ae44ec68001d64e4b85cf2abbc386be3000c92e1e8a808e3d4678ae`。当前Rustworker+模型归档合计285,752,286 bytes，不含主包及最终数据占用；还未由App自动准备验收。
- Python真实BaseSettings/usage代码核对：无session时usage记录直接返回；系统/原文/缓存/日志/仓库/关系库目录均在worker显式隔离根下，禁用dotenv与追踪，不读取真实记忆或Agent配置。
- 低并发重试后uv进程仍活跃；网络接收实际约27MB/16分钟。对官方pylance固定wheel做1MiB Range诊断，实际23.96秒约43.8KB/s，网络速度偏低；没有因观察超时假定任务结束。继续评估保留原SHA-256的镜像下载来源，不换依赖版本。

## C2 Python固定环境与源码worker

- `*-c-python-runtime.md`、`*-c-python-verifier.md`及每个`python-c1/c2/c3` handoff均实际生成到系统临时目录。
- 官方源低速进程已确认终止；`python3 scripts/bootstrap-python-engine.py --index-url https://pypi.tuna.tsinghua.edu.cn/simple`退出0，原171包完整哈希锁不变，镜像准备35.78秒、安装577ms；不替换默认Python。先前网络诊断见`python-mirror-probe.json`。
- `python3 scripts/verify-worker.py --python-source`真实C1原文索引/来源检索/错误、C2超时取消及分片JSONL通过；1ms索引期限实际32.11ms返回timeout。C3重启持久化和删除响应成功，但正常退出断言失败；失败报告`python-source-shutdown-failed.json`保留。旧验证器空AssertionError错误字符串曾导致退出码0，已改用repr与明确失败状态，绝不据此勾选成功。
- 独立`*-c-python-shutdown-fix.md`后将daemon stdin读取改为原始os.read，避免解释器退出时Python缓冲锁；`python3 scripts/verify-worker.py --python-source --from-step c3`退出0，仅重试失败C3并复用C1/C2。双拥有者拒绝、SIGKILL后重启持久化、真实删除、正常shutdown、缺模型永不ready全部通过；报告`python-source-verification.json`。初次依赖导入+模型加载44.08秒，后续11.14秒，不能说等同Rust启动速度。
- `*-c-python-business-adapter.md`与`*-c-python-contract-harness.md`后，P0合成入口可按同一store/service连接长期worker，真实stop/start用于故障实验。`sh scripts/go.sh build -o build/p0-engine/memory-probe ./cmd/p0-memory-probe`退出0，仅构建；Python业务契约仍待归档实测。Go worker环境新增私有HOME，不继承凭据。
- `*-c-python-freeze-build.md`后运行`python3 scripts/build-python-worker.py`退出0，PyInstaller onedir构建170.16秒；收集固定Cognee、FastEmbed、LanceDB、原生资源和Enola。可选云provider缺失提示只作构建日志，是否影响本地worker仍需真实验收。日志`python-worker-build.log`。不复制venv，不在用户启动时pip安装。
- `*-c-enola-synthetic.md`后`python3 scripts/verify-enola.py`退出0。独立复制的Enola0.4.30/extractor v351在合成Go仓库实际生成3个fact，main到greet的calls关系通过；报告`enola-verification.json`。实际输出在合成仓库`.enola`及隔离HOME`.enola`。只证明抽取工具可用，Cognee图谱入库/查询、自动刷新、外置输出未验收；Rust没有等价入口，两个worker都明确graph_ready=false。

## C2冻结分发修正（进行中）

- `python3 scripts/package-python-worker.py`退出0，首个Python归档398,657,888 bytes、解包文件1,035,603,427 bytes，不含模型；原SHA-256为ca6bcb3801f6520ee8fc4545d8ff7b4417b9419b5dacd7b73142d0c1f0d2f8f2。独立产物并不轻，总量需另算模型。
- `*-c-python-archive-contract.md`与`*-c-python-archive-corpus.md`后，首个归档完整协议和原九条检索集都在启动阶段60秒超时，均退出1。失败报告保留为`python-worker-verification-startup-failed.json`、`python-worker-corpus-startup-failed.json`，未勾选分发通过。
- `*-c-python-frozen-startup-diagnosis.md`与`*-c-python-frozen-diagnostics-fix.md`后补齐构造失败trace/stderr及进程回收。检查时两个原测试进程已退出，没有重复启动遗留进程。单独冻结目录诊断加`LITELLM_LOCAL_MODEL_COST_MAP=true`实际26.60秒ready且shutdown退出0，报告`python-frozen-startup-diagnostic.json`；sample显示初始化期间大量Python导入及原生库加载，未据此假称已证明唯一根因。
- `*-c-python-local-init-rebuild.md`后将本地成本表固定在worker，重新冻结。`*-c-python-privacy-envelope.md`后核对当前Cognee明确的COGNEE_ENV_FILE解析；PYTHON_DOTENV_DISABLED单独不能避免.env向上搜索。worker现在生成隔离空env并显式指定路径，初始化后删除，不读取任何现存.env；此前仅使用新建合成目录的空.env。
- `*-c-offline-process-contract.md`后新增子进程级sandbox-exec网络拒绝选项；后续协议和检索集在操作系统禁止worker联网下验收。仅关闭测试下载源不足以证明离线能力。
- D1受控签名源/真实下载/中断Range/校验失败/离线复用验收工具已准备并构建成功，未执行功能验收。SMAppService纯Go桥接按固定SDK实现四类真实状态；首次Class.Send类型错误经独立`*-d2-smappservice-build-fix.md`修正，`sh scripts/go.sh test ./internal/platform`退出0但为no test files，只算构建，不算系统注册通过。

## C2源码对照功能完成、冻结仍失败

- `*-c-python-source-corpus.md`后`python3 scripts/verify-worker-corpus.py --python-source`退出0；操作系统禁止worker网络，原9条查询9/9 Top-1/Top-3、7片长中文、来源字节范围及重启通过。索引2,274.86ms、加载7,200.73ms、RSS1,409,908,736 bytes；`python-source-corpus.json`，不标为归档重定位通过。
- `*-c-python-source-business.md`及各`python-b1`至`python-b5` handoff后，`python3 scripts/verify-memory-contract.py --python-source`退出0；同一Go store/service的全部B规则通过。真实stop/start引擎故障，保存原文/失败任务/幂等一版本；迟到旧回执拒绝，恢复revision4与重启检索正确。报告`python-source-memory-contract.json`。
- 修订归档的协议及原corpus在OS禁止网络下仍60秒启动超时，hello已收到，日志停在Cognee导入阶段；两项均退出1，保存为对应`*-offline-startup-failed.json`。直接源码离线诊断9.75秒ready，`python-source-offline-diagnostic.json`。没有把本地成本表的修正说成已解决全部问题。
- `*-c-python-freeze-trace-build.md`后第三次冻结正在构建，增加30秒初始化栈诊断，下一步直接在冻结目录定位具体栈，再处理归档。文档同步`*-c-python-source-docs.md`已实际执行；P0-validation/development/implementation均分别标明源码通过与独立分发失败。

## C2内部链接契约与归档协议通过

- `*-c-python-frozen-stack-probe.md`后复制展开链接的诊断运行时，在OS禁止网络下最终58.05秒ready、总61.12秒退出0；只记录诊断，不放宽60秒功能条件。30秒Python栈明确在pyarrow._dataset原生加载，报告`python-frozen-stack-diagnostic.json`。
- `*-c-python-link-structure-probe.md`后新复制运行时保留PyInstaller链接，32.15秒ready、总34.34秒；报告`python-frozen-links-diagnostic.json`。存在OS缓存差异，不据此声称已证明唯一根因；原生资源结构必须保留，避免重复加载副本。
- `*-c-python-archive-link-contract.md`后归档改为保留29条内部相对链接，并在component.json声明路径/目标；不再将链接目标重复打包。`python3 scripts/package-python-worker.py`退出0，压缩364,829,447 bytes、普通文件922,836,095 bytes（链接单列29条），SHA-256 `5e607d424c67fdc68ae4c31230d91089e907895882b9732d706ae6d23c3f195a`。不含模型，正式Developer ID/公证未配置。
- `*-d1-signed-internal-links.md`补丁第一次上下文不匹配，未写磁盘；独立`*-d1-signed-links-patch-retry.md`后补齐组件管理器的签名声明链接验证。只接受相对且最终解析仍在组件根内的链接；普通payload写完和gzip校验成功后才创建链接，拒绝外部/未声明/循环/特殊文件。已构建，尚未功能验收。
- `*-c-python-linked-archive-protocol.md`后`python3 scripts/verify-worker.py --archive build/p0-engine/memlane-cognee-python-0.0.1-darwin-arm64.tar.gz --report build/p0-engine/python-worker-verification.json --deny-network`退出0。完整C1/C2/C3均通过，脱离开发目录且操作系统禁止网络；首次初始化54,460.87ms，原60秒条件未改变，1ms长文期限32.29ms返回timeout，取消没有完成回执；重启/删除/正常退出和缺模型错误通过。`python-worker-verification.json`。
- 独立归档的原9条语义检索与B1–B5业务正在按依赖继续；源码通过证据复用，不替代新分发产物验收。

- `*-c-python-linked-archive-corpus.md`后`python3 scripts/verify-worker-corpus.py --archive build/p0-engine/memlane-cognee-python-0.0.1-darwin-arm64.tar.gz`退出0，独立归档在系统禁止网络下原9条全部Top-1/Top-3命中；7片长中文、字节范围与重启通过。首次加载28,074.37ms、索引2,104.78ms、峰值RSS1,425,424,384 bytes。`python-worker-corpus.json`。不同加载耗时包含OS缓存与原生库首次验证差异，不能当作真实断电冷启动比较。
- `*-c-python-linked-archive-business.md`后开始独立归档B1–B5对照；原Rust及Python源码已通过且未受改动影响的检查不重复。D2RuntimeController工程准备已经构建，尚未注册系统服务或由UI使用；仍不勾选D。

- 独立归档B1–B5第一次退出1，夹具初始化FileExistsError：Harness的source布尔参数被文件复制/归档句柄同名变量覆盖，尚未执行业务验收。独立`*-c-python-archive-business-harness-fix.md`后参数改为source_worker并增加启动失败落盘；失败记录`python-memory-contract-harness-failed.json`。实际重试不修改引擎、语料或业务规则；已有协议及原9条检索结果不重复。

## C2完成：独立Python原文与业务对照

- 修正夹具同名参数后`python3 scripts/verify-memory-contract.py --archive build/p0-engine/memlane-cognee-python-0.0.1-darwin-arm64.tar.gz`退出0；每个B1–B5实际handoff均生成。A/B与同文隔离、显式全局/空scope错误、编辑删除即时失效、恢复revision4、伪造/过时chunk拒绝、实际停启失败和幂等重试、迟到旧回执拒绝及重启持久化全部通过。`python-memory-contract-verification.json`。首次调用含初始化55.96秒，后续重启约2.42秒，统计包含OS缓存，不能说每次都同样快速。
- C2原文同契约与独立分发对照完成；Rust/Python都不宣称graph_ready，Enola工具的真实符号/调用抽取与P4适配缺口保留。正式Developer ID/公证未配置；仅测试签名，不发布。独立`*-c2-complete.md`后进入D1。
- D2前置源码：RuntimeController真实签名源/进度/离线缓存/组件配对与SMAppService状态已准备并构建；不对非网络的签名/兼容错误回退旧Feed后误报成功。memlaned新增版本化Unix IPC客户端、同uid身份、单并发引擎、持久索引补发及服务入口确认约束，拒绝未确认的编辑删除恢复和尾随JSON。尚未执行真实服务/GUI/系统注册验收，不能勾选D2。

## D1完成：受控签名源真实组件准备

- `*-d1-controlled-source-run.md`及d1每个子步骤handoff均实际生成。`build/p0-engine/component-check --repo /Users/bytedance/work/AI/memlane --worker /Users/bytedance/work/AI/memlane/build/p0-engine/memlane-cognee-rust-0.0.1-darwin-arm64.tar.gz --model /Users/bytedance/work/AI/memlane/build/p0-engine/memlane-model-e5-base-int8.tar.gz`退出0；完整报告`component-verification.json`，保留实际4421条进度事件，不用占位进度。
- 签名Feed与真实worker/模型下载285,752,286 bytes、归档/资产校验、版本目录原子安装及真实索引检索通过，8.95秒（本机受控HTTP源，不代表公网下载速度）。
- 实际短传128KiB后失败，重试Range=bytes=131072-，只续传66,122,252 bytes；真实修改首字节导致SHA失败并重试成功；已有有效版本保持可复用。5.88秒。
- 真实篡改Feed签名、协议2不兼容、未批准描述符均拒绝；仅测试内存生成的Ed25519密钥，没有写正式发布凭据。
- 真实签名合成归档的有效内部链接安装/读取通过，外部/绝对/未声明/自循环链接均拒绝；实际篡改已安装链接后重新校验及修复通过。
- 两个实际子进程竞争相同组件根，源只接收一次worker归档请求，一个下载一个校验复用；实际合计66,253,324 bytes，没有重复下载。3.45秒。
- 测试源关闭后由新Manager核对缓存签名并复用组件，下载量0；空缓存离线不误报就绪。另在sandbox-exec拒绝worker联网下真实索引与检索通过，1.20秒。D1基础管理器验收完成，但尚未验收App自动准备、系统注册和最终主包。

## D2独立服务基础通过

- `*-d2-daemon-functional.md`及d2a-e独立handoff后`python3 scripts/verify-daemon.py`退出0。独立memlaned/真实Rustworker/固定模型，全在合成临时目录；私有socket0600、目录0700、同uid核对，保存后真实后台索引检索、双服务拥有者拒绝通过。
- 实际IPC拒绝未确认编辑/删除/恢复，原文及revision保持不变；尾随JSON拒绝。实际SIGKILLworker时读取报错，恢复期间原文被持久接收，恢复后待索引任务补发通过；主动停止后worker退出、socket清理，正常重启仍保持停止，明确resume后数据与检索正确。
- 实际连续3次worker崩溃触发熔断，观察期内未无限拉起；用户retry后恢复。随后独立`*-d2-daemon-crash-prerequisite.md`与`*-d2f-service-crash.md`后`python3 scripts/verify-daemon.py --from-step d2f`退出0，仅补测服务自身被SIGKILL，复用d2a-e：索引进行中终止服务，worker因stdin管道EOF退出，新服务重获拥有者锁，原文/待索引任务保留并补发正确。1.70秒。报告`daemon-verification.json`。
- socket现在固定用户私有短目录`/tmp/memlane-<uid>-<root-hash>/service.sock`，避免UI/LaunchAgent不同TMPDIR导致IPC不一致。这里是本机IPC，不增加远程管理。系统注册、真实窗口退出与自动更新仍未通过；客户端右栏正在接准备/重试/取消/停止状态。

## D2客户端与系统注册推进

- UI/服务共享DTO与IPC移到轻量records/serviceipc包，业务SQLite只由memlaned链接；字段与存储规则不变。拆分首次未使用context导入失败，经独立`*-d2-light-client-import-fix.md`修正；构建检查通过。客户端新增真实准备/重试/取消/停止/恢复、退出保留服务设置；列表编辑属于P1，服务已就绪时不再错误显示未连接或编辑已完成。
- `python3 scripts/build-client.py --out build/p0-client`实际退出0，MyGo编译、helper签名、Contents/Library/LaunchAgents plist、完整App重新ad-hoc签名、DMG生成均通过；App文件约18.4MB是当前构建提示，完整三类体积仍需最终E实测。主包不含Cognee/模型/重原生库。MyGo原本只复制Resources，因此使用显式builder补齐Library布局并重新封包，不沿用旧签名归档。
- 独立测试源`p0-client-source.py`在系统临时memlane-client-p0根运行，组件HTTP与更新HTTPS均绑定127.0.0.1；新生成测试签名密钥在内存，TLS测试私钥只在该临时根，不读正式凭据。公共配置与App服务ID为io.github.zwhu.memlane.p0.70731be7e631，避免影响其他服务。正式发布仍未配置。
- 测试App实际调用SMAppService status返回not_found，随后register无错误且enabled；unregister无错误且not_registered。只记录实际系统返回，不把静态plist当成功。探测注册前未完成组件配置，已立即注销以停止该测试job；后续准备完成写好service.json后才注册。
- 根据实测修正首次not_found：先核对NSBundle包内plist存在，再允许注册；缺失时才报告组件缺失。独立`*-d2-initial-system-status-fix.md`后已构建，正在重建测试App并实测完整自动准备。没有申请真实后台许可或操作其他注册服务。

- 测试App自动prepare实际返回ready/enabled且退出0；`system-prepare-verification.json`实际验证UI退出后托管服务仍ready，合成原文保存/检索成功。后续更新旧helper先真实stop并注销，重建支持quiesce的测试App，合成原文保留。
- 实际0.0.2测试App更新归档8,108,439 bytes，通过本机HTTPS与临时内存Ed25519签名提供。第一次错误签名install明确拒绝，报the update is not signed with the app's key。但前置resume尚未完成时启动install，注销动作干扰了等待就绪，resume最终超时；没有把这个场景算作后台恢复通过。独立`*-d2-update-fixture-order-retry.md`后先只运行resume，等待真实ready，再重试恢复验收。完整自动更新仍未勾选。

## D2受控更新失败恢复已通过

- 后台resume单独完成后返回ready/enabled；只查询本测试LaunchAgent，launchctl明确managed_by=com.apple.xpc.ServiceManagement、state=running、版本0.0.1，未查看或修改其他服务。
- 错误签名更新实际被MyGo拒绝，旧App二进制SHA未变；后台worker重启，合成原文revision1及检索保持。错误the update is not signed with the app's key，`update-verification.json`的bad-signature通过。
- 实际HTTPS短传128KiB引发downloading: unexpected EOF；旧App和原文保留，后台恢复ready/enabled，truncated-download通过。每个case执行handoff。
- `verify-client-update.py`封装可复现case，按case执行不重复其他已通过项；正在success场景使用实际0.0.2 App、8,108,439bytes更新归档替换，并检查重新注册后台、版本、原文及组件GET次数未增加。正式签名/发布源仍未配置。

## D2 更新替换与主动停止状态通过

- success 实际从0.0.1替换到0.0.2，完整App codesign校验通过；新App准备后后台ready，原文revision1与检索保留，worker和模型归档GET次数不增加。
- preserve-stop 实际从0.0.2替换到0.0.3，准备返回stopped、持久停止标记为true、签名有效。首次Python `mode=ro` 打开已停止数据库失败，保留原错误；文件实际存在45,056bytes，未据此推断丢失。
- 独立handoff `stop-update-readback`、`stop-update-readback-fix`后，`python3 scripts/verify-client-update.py --case preserve-stop --readback-only --fixture <合成临时根> --app build/p0-system/darwin-arm64/MemlaneP0.app`退出0，仅补查未通过项，没有重复0.0.3替换。取得服务拥有者锁、确认stopped且无待提交WAL后，以immutable只读打开；integrity_check=ok、revision1原文正确、检查前后数据库SHA不变，组件复用通过。报告保留prior_readback_error和supplement_only。
- `d2-window-lifecycle`后为文件菜单增加与托盘同一路径的“停止服务并退出”，便于系统菜单/键盘访问；正在构建隔离自动化App。完整窗口与原生输入/辅助功能仍待验收。

## D2 真实窗口生命周期完成

- 每次修复实际执行handoff：`d2-automation-build-fix`、`d2-window-ready-fix`、`d2-packaged-inspector-fix`、`d2-window-activation-fix`、`d2-ui-snapshot-refresh`。初版MyGo发布构建剔除检查器；debug模式的首次尝试未作为分发验收，改为e2e标签+`MYGO_INSPECTOR=1`保留production资源路径。socket先于内容窗口就绪时实际出现no_window，脚本改为等待窗口与内容挂接。
- 当前环境实际窗口focused=false，检查器只读取旧帧；捕获实际内容帧后同一窗口立即显示真实stopped/ready状态。验收脚本在核对异步状态前先捕获当前Native内容帧，不把内容帧当系统输入法或屏幕录像。
- `python3 scripts/verify-lifecycle.py --app build/p0-lifecycle/darwin-arm64/MemlaneP0.app --fixture <本轮合成根>`最终退出0：关闭隐藏/重开、默认退出保留后台且原文revision1/检索保持、停止服务并退出、keepfalse退出等待worker退出、持久stopped及原文只读检查均通过。每个子步骤和结束handoff实际生成，`lifecycle-verification.json`。

## D 准备GUI与独立大小补验通过

- 新建独立受控源及服务ID `io.github.zwhu.memlane.p0.cfb0e56ff8ee`，不重启旧更新源或更换原签名。默认Python缺cryptography首次退出1，经独立`d-ui-source-runtime-retry`后使用仓库已锁`.local/cognee-python/bin/python`启动，不安装全局包。
- `verify-preparation-ui.py`首次503失败界面可用/重试入口通过；progress角色误写为progressBar、部分文件误写为.partial导致验证器失败。独立修正为真实progress和.part；之后取消发生在worker安装完成/模型请求开始边界，已取消但没有部分文件，该下载中取消子项仍判失败，没有冒称通过。
- `d-ui-download-cancel-fix`后明确等待非空且未完成的真实部分文件再取消，并将取消提示改为中文。`python3 scripts/verify-preparation-ui.py --from-step cancel --app build/p0-ui-prepare/darwin-arm64/MemlaneUITest.app --fixture <新合成根>`退出0，复用503已通过项：真实285.75MB总量与进度，取消保留2,686,976bytes，重试ready/系统服务启动，空缓存断网不误报ready全部通过。`preparation-ui-verification.json`和实际截图。
- 对照清单发现UI此前只有总量，补齐从签名Feed读取的引擎/模型独立大小。首次补丁上下文失败未写磁盘，经独立handoff重试。受控源停止后，实际App仍从签名缓存显示“引擎66.25MB · 模型219.50MB”与285.75MB总量，保持主动停止；`offline-app-preparation.json`。只重验新大小行影响的80%–140%布局，原下载/长文不重复。

## D GUI更新与自动重启通过

- 原App探针安装和新App prepare的证据不代替GUI按钮/自动Relaunch。独立`d-gui-update-prepare`、`d-gui-update-harness`后构建0.0.4测试源完整App，归档8,279,946bytes；测试签名仍仅在本机源内存中，不发布。
- `python3 scripts/verify-gui-update.py --app build/p0-lifecycle/darwin-arm64/MemlaneP0.app --fixture <原合成根>`退出0，两个子步骤分别实际handoff：GUI错误签名报错、旧App不变、后台恢复及原文/检索正确；随后实际点击检查/安装，0.0.3→0.0.4替换，旧UI退出，Launch Services自动启动新UI（PID90472），后台ready，原文检索与兼容组件复用，codesign校验均通过。`gui-update-verification.json`。
- 测试结束先用App明确stop并确认无error，再退出新UI，不留下后台任务。未重复已经完成的0.0.3替换；`update-verification.json`只读补查完成后四个子项均为passed。

## E 原生内容、材质及剩余条件

- e2e专用原生探针直接读取AppKit窗口和NSAccessibility，不进入正式包。首次计数类型编译失败，经handoff修正。探针曾遍历RemoteUIElement并触发未支持selector异常；原失败与日志保留，已限制到本App MyGoSurfaceView并检查selector支持，不遍历其他应用。
- 系统红黄绿按钮对象存在，但performClick后的状态等待超时，buttons仍failed；不将对象存在或窗口API当真实系统按钮点击通过。转入人工真实操作清单，不阻塞独立布局与文本检查。
- `verify-native-ui.py --from-step layout`的布局通过：侧栏收起/展开、80%–140%、980×620最小窗口、NSVisualEffectView material=7。5000字符长文最初8秒及显式60秒请求超时；采样主线程空闲，timing显示setValue整帧未返回。验证器未缓冲SocketIO单次write不保证大帧完整，改为sendall后原5000字符全选、Tab焦点移动、跨页保留和实际AXTextArea/AXButton均通过，未降低原内容条件。`native-content-timing.json`、`longtext-sample.txt`和失败记录保留。
- 新大小行后 `python3 scripts/verify-native-ui.py --only-step layout ...`退出0，只重验受影响布局。native报告为partial_passed，系统按钮、拖动、系统中文候选窗及VoiceOver仍pending；`native-permissions.json`实际权限均false。已向用户提供[人工清单](p0-native-manual.md)，等待真实结果或系统权限，P0和goal不标记完成。

## E 完整体积、检查与测试源清理

- `python3 scripts/build-client.py --out build/p0-final`退出0：完整UI/更新/组件管理/helper/LaunchAgent重新ad-hoc签名与DMG构建；初版e2e DMG曾hdiutil退出1，未当成功，后续保留诊断输出的完整生产包构建通过。builder在最终DMG生成后写SHA256SUMS，实际`shasum -a 256 -c SHA256SUMS`退出0。
- 最新完整主DMG9,029,281bytes，App19,427,850bytes；Rust额外成功首轮下载285,752,286bytes，Python归档对照584,328,409bytes；实际Rust运行目录与App合计逻辑511,050,528bytes、文件分配指标518,934,528bytes。包含合成原文一条、索引、设置与实际缓存；最终精确命令和边界见development与`final-sizes.json`，不把主包轻量解释为总下载很小。
- `sh scripts/go.sh test ./...`与相关vet检查通过，主要功能验收仍以上述真实进程/App结果为准，不将no test files或编译通过当功能成功。中文文档首次命令解析UTF-8失败没有写入，改结构化补丁同步；implementation/product/technical/development/P0记录已按实际状态更新，原用户无关修改保留。
- codesign本机完整性通过；spctl正式执行信任对App/Rust worker/ORT/Python worker实际退出3、rejected，`distribution-assessment.json`，正式Developer ID、公证和发布源缺失如实记录，不绕过或自动发布。
- 两组测试状态均stopped，实际App status均not_registered。仅对本轮受控源PID40917/71370发SIGINT，工具确认退出130（主动中断），finally删除了新生成TLS私钥。合成组件/数据与公共配置保留用于体积复查及剩余人工验收，未读取或修改真实记忆、聊天、Agent配置或凭据。

## E 系统权限开启后的真实操作与输入诊断

- 用户为宿主 iTerm2 开启权限并解锁桌面。`check-native-permissions.py` 在授权环境实际检查四项均 true；`native-session-status.json` 的 screen_locked=false。旧 false/锁屏失败为历史结果，不能继续作为当前阻塞。没有改用户 Dock 设置或读取密码。
- `verify-native-system.py --step minimize`、`--step close`、`--step drag` 分步 handoff 后实际退出0：CG 鼠标点击黄色最小化、红色隐藏且进程保留；Dock 的真实 AXPress 恢复/重开；侧栏顶部和正文工具栏分别拖动35×30，导航点击不误拖。`native-system-verification.json` 保存各自状态及窗口坐标；Dock恢复明确是系统AX操作。
- 绿色按钮实际点击可进入系统全屏，但 Ctrl+Cmd+F 退出超时，子项保持 failed。为客户端添加标准“视图/全屏”菜单；构建通过尚不代表退出验收通过，最终生产包与体积需随改动重建。
- 每次诊断/修复/重试执行本地handoff流程生成系统临时交接文件。测试 App 经 Launch Services 启动，所有草稿/配置均来自本轮合成根。实际键盘事件已进入本 App，key_window=true、第一响应者MyGoSurfaceView、内容焦点memory-draft；安全输入false。不能再把候选失败笼统归因未授权或未聚焦。
- 原生 e2e 探针记录本 App 的键码/flags/字符长度及文本协议计数，不记录其他应用或真实文字。最新 `keyDown` 和 `setMarkedText` 各7次，inputContext存在、系统拼音源正确；组合输入的未提交内容不等于已保存正文。候选查找增加SCIM XPC实际PID与TextInputMenuAgent，正在核对真实面板及空格提交，尚未勾选通过。前台恢复改为尽力而为，原输入源恢复和核对独立执行。

## E 系统操作与真实拼音增量通过

- `native-fullscreen-system-menu` handoff后，`python3 scripts/verify-native-system.py --step fullscreen`退出0：绿色实际进入全屏，系统视图菜单AXPress的Toggle Full Screen退出并恢复尺寸。`--step nondrag`独立handoff后退出0：正文输入框与工具栏新增按钮实际拖动不移动窗口。报告各步骤保留，旧controls失败不替代独立通过结果，快捷键旧失败没有改写成成功。
- 输入诊断依次执行`native-ime-cleanup-diagnostic`、`native-ime-candidate-process-fix`、`native-ime-preedit-window-diagnostic`、`native-ime-home-context-probe`、`native-ime-owner-metadata`；每次修复/重试均生成handoff。正常GUI HOME下已经实际提交中文，但早期候选查找仍失败；新增InputMethodKit/UI宿主和本App PID后，实际`python3 scripts/verify-system-ime.py --system-home`退出0。候选面板由测试App PID56368持有、layer20、397×28，截图实际显示“项目/项目部”等候选；连续项目经验、删除为项目经、ASCII request_id与跨页保留全部通过，原输入源恢复。未改产品输入实现，不把“原文为空”误当组合输入没发生。
- `final-native-build`后完整生产包重建退出0；新DMG9,029,309bytes，SHA256 `1daad9faba662c67662abaa0fabf9bf51f726686ef6be073249bad9c9cd676be`；App仍19,427,850bytes。`measure-package.py`、`shasum -a 256 -c SHA256SUMS`、完整codesign均退出0。额外Rust下载285,752,286bytes、Python对照584,328,409bytes；逻辑占用511,050,528bytes、分配指标518,934,528bytes。相关`sh scripts/go.sh test ./cmd/memlane-app ./internal/desktop`通过（desktop缓存复用）；没有重跑未受改动影响的A-D。
- `native-final-doc-size-sync`首次补丁上下文不匹配未写入，经独立`native-final-doc-size-retry`读取真实行后同步新体积与散列。原有用户修改保留，未提交或发布。

## E VoiceOver实际启用与输出验收进行中

- 新建`verify-voiceover.py`，实际启动/导航输出，不调用预写`output`。open App、session/HID层Cmd+F5等待均未启用，分别保留失败与handoff；只进入系统首次VoiceOver Quickstart欢迎窗口。直接读取该窗口按钮后，真实点击“使用旁白”启用通过，但AppleScript读取last phrase超时，不能勾选朗读通过。
- AppleScript quit使VoiceOver进程退出但系统开关仍1；不能按无PID认定已恢复。`native-voiceover-cleanup-retry`、`native-voiceover-live-state`、`native-voiceover-process-contract`后核对系统旁白页唯一开关实际1，真实鼠标点击恢复0，NSWorkspace重新false。不修改“不再显示此信息”或用户其他设置，不读取真实记忆/聊天/Agent配置。
- 系统公开VoiceOver默认命令清单明确VO+Command+F10显示字幕；AppleScript路径超时后新增实际字幕渠道。第一次字幕路径卡在启用，改为系统旁白页实际开关后启用通过，字幕窗口仍未读到输出；继续发送完整物理修饰键按下/flagsChanged/释放序列评估系统命令。每次修复和重试独立handoff；P0整体未通过，真实输出及状态恢复仍按结果记录。

## 本轮按用户最新要求收口P0主流程

- 用户明确指示：“别测这样没用旁边了啥的了，主流程走通就行”。立即停止VoiceOver旁支，不再启动新验收；未通过结果保留，移到P4辅助功能增强，不把它勾选为成功。主流程仍按既定A/B/C/D和主包/体积验收，未扩大P1/P2或其他主机范围。
- `p0-mainflow-scope`、`p0-mainflow-cleanup`、`p0-mainflow-cleanup-final`分别实际handoff。最后VoiceOver脚本已退出；一次新检查发现延迟Starter再次打开开关，只终止本轮Starter PID78350并经系统旁白页唯一开关恢复0。3秒后实际开关0、NSWorkspace=false，原输入源为SCIM拼音。退出结果`ancillary-cleanup.json`；没有继续旁白测试。
- 主流程只读证据审计：原文索引、Rust/Python隔离版本与独立协议、组件6步、独立daemon6步、App实际准备、GUI失败取消重试/离线、生命周期3步、更新4步及GUI更新2步的报告状态全部passed。原系统controls历史失败保留，独立minimize/close/drag/fullscreen/nondrag已通过；系统拼音passed。没有重跑已通过且未受改动影响的A-D。
- `p0-mainflow-docs`及`p0-mainflow-docs-finish`后，产品/技术/实施/开发/P0记录同步本轮核心工程流程完成；主包新DMG与SHA、额外下载和安装占用均按实际测量更新。App内真实记忆管理仍属P1，Codex/pi互通属P2；正式Developer ID、公证与正式发布源未配置，不自动发布。
- 最终`p0-mainflow-final-audit`与`p0-mainflow-close`分别实际handoff。只读审计16份既有真实功能报告、各自SHA、最新DMG实际SHA/字节及清理证据，通过；P0必要清单没有未勾选项，报告`build/p0-engine/p0-mainflow-final-audit.json`。本审计不冒充重跑功能测试。相关vet、Python语法检查和git diff --check退出0；新主App的spctl实际退出3、rejected，保留为正式发布限制，codesign签名完整性已通过。未重跑A-D或已停止的旁白。
