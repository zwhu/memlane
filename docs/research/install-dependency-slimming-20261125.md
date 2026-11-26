# Memlane 首次安装依赖体积分析与零侵入瘦身研究

2026-11-25，源码基线 `6b005c2`（[mygo.json](../../mygo.json) 版本 `0.1.0-beta.3`，最低系统要求 `macOS 26.0`，目标架构 `darwin/arm64`）。基于受管环境 `.local/cognee-python` 全量文件计量、171 个锁定发行包精确 wheel 字节与 SHA-256 核对、`otool -L` 动态库链接分析、以及隔离只读沙箱单包/组合断包导入探针，梳理 Memlane 首次安装的真实体积构成、Cognee `1.6.7`（`ec6f408`）真实导入链及严格遵守 Cognee 零侵入红线的分级瘦身方案。

> **红线边界**：全文方案严守 [AGENTS.md](../../AGENTS.md) 中的 **Cognee 零侵入红线**——不修改上游 Cognee `1.6.7`（`ec6f408`）源码、不维护私有 Fork/本地源码补丁、不绕过 `engine/python/official_service_dev.py#L280` 的 `verify_source` v2 逐文件 SHA-256 与无 `.pyc`/`.pyo` 校验、不对 Cognee 内部做无契约 monkey-patch，且保持单一日常库与现有向量空间不变（[first-install-components.json](../../engine/first-install-components.json#L46) `change_existing_vector_space: false`）。

## 1. 核心结论

1. **「2 GiB 下载」为 Go UI 层的保守硬编码预算，而非真实下载量**：
   - [environment.go](../../internal/bootstrap/environment.go#L165-L170) 将 6 组安装项（含归入 `runtime` 的 OpenSSL 共 7 项下载资产）预算写死为 **下载约 `2.34 GiB` / 落盘约 `4.57 GiB`**（其中 `runtime` 单独标为 `2 GiB` 下载 / `4 GiB` 落盘；beta.3 新增的 Setup CLI `internal/setupcli/` 同样复用该安装计划与 `requirements.lock`）。
   - **实测结果（171/171 包精确匹配 [requirements.lock](../../core/setup/requirements.lock) SHA-256）**：Apple Silicon（`darwin/arm64`）下 `runtime` 的 171 个锁定 wheel **真实总下载量仅为 `339.61 MiB`（`356.10 MB`）**；全部 7 项首次安装资产在无复用的净室环境下，**真实总下载量仅 `550.58 MiB`（官方源） / `551.28 MiB`（国内镜像源）**，实测 venv 解压后逻辑大小 **`979.02 MiB`**（运行生成 `__pycache__` 后 **`1,076.31 MiB`**，APFS 物理块 **`1,165.52 MiB`**）。
   - 安装后表观磁盘膨胀至 `1.8–2.4 GiB` 的主因并非下载量，而是 [runtime.go](../../internal/bootstrap/runtime.go#L109-L118) 为 `uv` 指定 `<runtime>/cache` 后**安装成功从未清理**，叠加 `python` / `uv` / `agent-plugin` 解包后保留的原始归档与 `__pycache__`。
2. **锁文件中不存在 `torch` / `transformers` / `scipy` / `playwright` / `docling` 等常见重型 AI 依赖**：
   - 上游 Cognee `1.6.7` 已将这些放入 41 个可选 extras，`engine/python/requirements.in#L1-L4` 未开启任何 extra。
   - **真正的体积前 5 名全为二进制/Rust/C++ 库**：`pylance`（`169.94 MiB`）、`lancedb`（`141.28 MiB`）、`pyarrow`（`124.07 MiB`）、`litellm`（`98.69 MiB`）、`onnxruntime`（`74.40 MiB`）——这 5 个包占 `runtime` 下载的 **`64.4%`**、解压体积的 **`62.1%`**。
3. **最大「零侵入结构性冗余」来自双重安装 `cognee==1.6.7`**：
   - Memlane 运行时通过 `sys.path.insert(0, str(source))` 加载经 `verify_source` 校验的 GitHub `ec6f408` 源码树（`68.95 MiB` 归档 + `39.09 MiB` 解包），但 `requirements.in` 同时从 PyPI 安装了 `cognee==1.6.7` wheel（**`22.24 MiB` 下载 / `61.18 MiB` 落盘**）。
   - 经逐文件 SHA-256 比对，GitHub `ec6f408` 比 PyPI `1.6.7` 更新了 `175` 个文件并新增 `62` 个文件（含 coding 提炼 prompt），PyPI wheel 中的 `.py` 代码在运行时 **100% 被遮蔽**；其唯一实质用途仅是被 `engine/python/ladybug_extension.py#L49-L51` 用来提取 1 个 **`0.60 MiB`**（`630,440 B`）的 `osx_arm64/libjson.lbug_extension`——却为此附带下载并落盘了 **`26.53 MiB` 的 Windows DLL**、`5.15 MiB` Linux/x86_64/旧版扩展与 `18.73 MiB` 重复 `tests/`+`eval_framework/`，并被迫锁死了 Cognee 全部 51 项核心依赖。
4. **分梯度瘦身收益**：
   - **Tier 1（零代码改动，仅删 `requirements.in` 中遗留的 `pyinstaller`）**：减 4 包，下载 **`-1.48 MiB`** / 解压 **`-3.39 MiB`**。
   - **Tier 2（A 类全部：脱钩 PyPI `cognee` wheel + 独立三平台 Ladybug 扩展资产 + 去 12 个零引用核心包）**：减 17 包（macOS 净补 `0.60 MiB` 扩展），下载 **`-26.43 MiB`（`-7.8%`）** / 解压 **`-73.51 MiB`（`-7.5%`）**。
   - **Tier 3（Tier 2 + B1 经 `uv pip compile --excludes` 裁剪 `boto3/botocore`、`pandas`、`hf-xet`、`black` 等 27 个未用传递依赖，已通过 27 核心模块组合断包探针）**：共减 44 包，**零功能降级**，下载 **`-61.02 MiB`（`runtime` 降 `-18.0%`）** / 解压 **`-159.65 MiB`（`venv` 降 `-16.3%`）**。
   - **叠加 B2（`enola-cli` 改为开启代码图谱时按需下载）**：额外下载 **`-11.28 MiB`** / 解压 **`-51.11 MiB`**，累计下载 **`-72.30 MiB`（`-21.3%`）** / 解压 **`-210.76 MiB`（`-21.5%`）**。
   - **叠加 B3（纯落盘/缓存治理：清理 `<runtime>/cache`、禁用 `__pycache__`、清理一次性 `uv`/归档/插件上游目录）**：**额外释放 `178.9–258.2 MiB`（已实测）+ `uv cache`（估算 `50–350 MiB` 物理 / `340–980 MiB` 表观）**，总落盘压入 **`~0.93–1.06 GiB`**。
   - **红线避坑**：① **`pylance`（`169.94 MiB`）不可盲删**——虽不阻塞启动，但每次 `cognify` 尾声执行 `compact_vector_store()` 会无保护 `import lance` 报错，且失去碎片压缩与旧表 schema 迁移能力；② **`pillow`（`11.41 MiB`）、`datamodel-code-generator`（`8.19 MiB`）、`networkx`（`6.96 MiB`）、`dlt`+`sqlglot`（`7.63 MiB`）、`redis`（`2.48 MiB`）、`rdflib`、`pypdf`、`dateparser`、`opentelemetry-api`、`httpx2`** 均存在顶层强 import 耦合，删去即炸启动或本地嵌入探针。

## 2. 当前首次安装全部依赖与体积全景

### 2.1 全部 7 项首次安装资产实测对照（`darwin/arm64` 净室场景）

> **计量口径**：严格区分 **「已实测数据」**（直接读取文件 `st_size`、APFS `st_blocks×512`、manifest 精确字节或 PyPI 元数据与 `requirements.lock` SHA-256 精确匹配值）与 **「估算值」**。`1 MiB = 1,048,576 B`，`1 MB = 1,000,000 B`。

| 资产 / Requirement ID | UI 预算下载 / 落盘（[environment.go](../../internal/bootstrap/environment.go#L165-L170)） | 实测下载体积（精确） | 实测解压后落盘（逻辑 / APFS 块） | 当前归档/缓存保留行为 | 复用条件 |
| --- | --- | --- | --- | --- | --- |
| **1. 引擎运行库 `runtime`**（171 包） | `2 GiB` / `4 GiB` | **`339.61 MiB`**（`356,101,905 B`，171/171 实测） | **`979.02 MiB`** 逻辑 / **`1,049.76 MiB`** 块（无 `.pyc`）；含 `__pycache__` 后 **`1,076.31 MiB`** 逻辑 / **`1,165.52 MiB`** 块 | [runtime.go](../../internal/bootstrap/runtime.go#L109-L118) 写入 `<runtime>/cache`，**成功后不清理**（估算表观另增 `+340~980 MiB`，APFS clone 物理增量估算 `+50~350 MiB`） | 现有环境 171 包版本全匹配且原生探针通过 |
| **2. OpenSSL 3.5.9**（`native_openssl_arm64`，归入 `runtime`） | 含于 `runtime` 预算 | **`10.33 MiB`**（`10,829,301 B`） | **`15.83 MiB`**（`artifact.tar.gz` `10.33` + `libssl.3.dylib` `0.85` + `libcrypto.3.dylib` `4.65`） | [native.go](../../internal/bootstrap/native.go#L43-L54) 持续重读 `native/artifact.tar.gz` 校验，**永久保留原归档** | 若复用已有兼容 venv 且 `_lbug` 可加载可跳过 |
| **3. 本地向量模型 `embedding`**（`bge-small-zh-v1.5`，7 文件） | `91.23 MiB` / `91.23 MiB`（发版文档写 `~95.7 MiB` 实为十进制 `95.66 MB`） | **`91.23 MiB`**（`95,660,924 B`） | **`91.23 MiB`**（其中 `model_optimized.onnx` 占 `90.39 MiB`；两仓库重复存 `tokenizer.json` `0.42 MiB`） | 直接落 HF snapshot 布局，无额外压缩包 | 已有兼容缓存可只读复用 |
| **4. Cognee 源码 `cognee-source`**（`ec6f408`） | `150 MiB` / `300 MiB` | **`68.95 MiB`**（`72,296,858 B`） | **`108.04 MiB`** 逻辑 / 约 **`118.7 MiB`** 块（`service.tar.gz` `68.95` + 解包 3 包 `39.09` 逻辑 / `49.7` 块） | [source.go](../../internal/bootstrap/source.go#L62)、`engine/python/official_service_dev.py#L280` 需原归档逐文件核验，**永久保留 `service.tar.gz`** | 可指定已有验证通过的源码+归档 |
| **5. 安装工具 `uv`**（`0.12.3`） | `16.87 MiB` / `60 MiB` | 官方 `tar.gz` **`16.87 MiB`**（`17,686,637 B`）；国内 `whl` **`17.57 MiB`**（`18,426,262 B`） | **`55.99–56.69 MiB`**（保留原归档 `16.87/17.57` + `uv` 二进制实测 `39.12 MiB`） | [tools.go](../../internal/bootstrap/tools.go#L124-L170) 仅删 `.payload-*` staging，**永久保留归档及 `payload/uv`** | 宿主 `PATH`/`~/.local/bin`/`brew` 已有兼容 `uv` 直接复用（`runtime` 就绪后计划跳过 `uv`） |
| **6. Python 解释器 `python`**（`3.12.12`） | `16.85 MiB` / `100 MiB` | **`16.85 MiB`**（`17,667,661 B`） | **`68.71 MiB`**（保留 `artifact.tar.gz` `16.85` + `payload/` 实测 `51.86 MiB`） | [tools.go](../../internal/bootstrap/tools.go#L138-L170) **永久保留 `artifact.tar.gz`** | 宿主已有 `arm64` CPython `3.12.x` 直接复用 |
| **7. 官方插件 `agent-plugin`**（`a176248`） | `20 MiB` / `60 MiB` | **`6.74 MiB`**（`7,070,055 B`） | **`9.32 MiB`**（`integrations-upstream/service.tar.gz` `6.74` + `source/` `1.35` + 最终 `plugin/` `1.23`） | [plugin.go](../../internal/bootstrap/plugin.go#L315-L347) 构建完 `plugin/` 后**保留整个 `integrations-upstream/`**（`8.09 MiB`），而 `VerifyPlugin` 仅核验 `plugin/` | 可复用已验证插件目录 |
| **合计（首次净室安装）** | **下载 ~`2.34 GiB` / 落盘 ~`4.57 GiB`** | **官方 `550.58 MiB`（`577.3 MB`）**<br>**国内 `551.28 MiB`（`578.1 MB`）** | **无 `.pyc`/缓存：`1,328.84 MiB`（`1.30 GiB`）**<br>**含 `.pyc`：`1,426.10 MiB`（`1.39 GiB` 逻辑 / `1.50 GiB` 块）**<br>**含 `<runtime>/cache`（估算）：`1.8–2.4 GiB` 表观 / `1.45–1.85 GiB` 物理** | — | 若复用宿主 `python`+`uv`，净室下载降至 **`516.86 MiB`** |

> **附：`Memlane.app` 本体**：按 [build_release.py](../../scripts/build_release.py) 与 公测发版方案（历史路径 `docs/archive/release-v0.1.0-beta.1-20261121.md`） 实测，DMG 约 `6.0 MB`，解压后 `Memlane.app` 约 `22.3 MB`（含内嵌 `web/official-graph/dist/*` 与 `engine/python/` 脚本），不是体积瓶颈。

### 2.2 `runtime` 171 个锁定依赖 Top 30 体积大户明细（全量实测）

`engine/python/requirements.in#L1-L4` 仅有 3 个直接输入：`cognee==1.6.7`、`fastembed==0.9.1`、`pyinstaller==6.16.0`。解析 [requirements.lock](../../core/setup/requirements.lock)（`4,193` 行）共锁定 **171 个发行包**（`305` 条 `# via` 边），与本机 `.local/cognee-python` 的 171 个已装发行包 **100% 一一对应（0 缺失、0 额外）**。

下表按 **解压后 `RECORD` 逻辑体积** 排序列出 Top 30（已覆盖全部 `runtime` 解压体积的 **`95.2%`**、下载体积的 **`94.8%`**）：

| 排名 | 包名 | 版本 | 实测下载 Wheel (`MiB`) | 实测解压 RECORD (`MiB`) | 引入来源 (`# via`) | 内部大头构成（已实测） | 分级 |
| ---: | --- | --- | ---: | ---: | --- | --- | --- |
| 1 | `pylance` | `12.0.0` | `68.99` | `169.94` | `cognee` | `lance/lance.abi3.so` (`168.20 MiB`) | **B2（默认应保留）** |
| 2 | `lancedb` | `0.39.0` | `60.83` | `141.28` | `cognee` | `lancedb/_lancedb.abi3.so` (`139.15 MiB`) | **C（刚需）** |
| 3 | `pyarrow` | `26.0.1` | `35.56` | `124.07` | `lancedb`, `pylance` | `libarrow` (`37.25`) + `libarrow_flight` (`23.49`) + `compute` (`14.89`) + `s3` (`7.66`) + `parquet` (`4.47`) | **C（刚需）** |
| 4 | `litellm` | `1.108.2` | `33.35` | `98.69` | `cognee` | `proxy/` (`36.94`, 含 Next.js/Swagger UI) + `rust_bridge/_native.abi3.so` (`27.56`) + `llms/` (`10.28`) + `tokenizers/` (`9.56`) | **C（刚需）** |
| 5 | `onnxruntime` | `1.31.1` | `19.91` | `74.40` | `cognee`, `fastembed` | `onnxruntime_pybind11_state.so` (`38.88`) + `libonnxruntime.1.31.1.dylib` (`31.51`) | **C（刚需）** |
| 6 | `cognee` | `1.6.7` | `22.24` | `61.18` | `requirements.in` | `ladybug_extensions/` (`32.28`, 含 `26.53` Windows DLL) + `cognee/tests/` (`11.86`) + `eval_framework/` (`6.87`) + 被遮蔽源码 (`10.17`) | **A2（脱钩后可省）** |
| 7 | `enola-cli` | `0.4.30` | `11.28` | `51.11` | `cognee` | `bin/enola` 单体二进制 (`51.09 MiB`)，仅代码结构图谱调用 | **B2（按需/条件可省）** |
| 8 | `pandas` | `2.3.3` | `10.23` | `37.71` | `cognee` | `pandas/_libs/` (`17.81`) + `pandas/tests/` (`12.36`) + `core/` (`5.07`) | **B1（条件可省）** |
| 9 | `botocore` | `1.43.126` | `15.48` | `19.81` | `boto3`, `s3transfer` (← `litellm`) | `botocore/data/` AWS API 定义 (`17.96 MiB`) | **B1（条件可省）** |
| 10 | `numpy` | `2.5.4` | `5.20` | `19.64` | `cognee`, `fastembed`, `lancedb`, `onnxruntime`, `pandas`, `pylance` | 核心科学计算/向量运算二进制 | **C（刚需）** |
| 11 | `ladybug` | `0.19.0` | `4.35` | `12.88` | `cognee` | `ladybug/_lbug.cpython-312-darwin.so` (`12.67 MiB`) | **C（刚需）** |
| 12 | `pillow` | `13.0.0` | `4.63` | `11.41` | `fastembed` | `PIL/.dylibs/` (`9.06`, avif/harfbuzz/freetype 等) | **C（强耦合坑）** |
| 13 | `cryptography` | `50.0.2` | `3.73` | `10.50` | `cognee`, `pyjwt` | `_rust.abi3.so`（JWT/文件及集成凭据加密） | **C（刚需）** |
| 14 | `openai` | `3.46.0` | `2.33` | `9.24` | `cognee`, `litellm` | OpenAI 客户端与类型定义（LiteLLM/Cognee 顶层导入） | **C（刚需）** |
| 15 | `SQLAlchemy` | `2.0.57` | `2.10` | `8.65` | `alembic`, `cognee`, `dlt`, `fastapi-users-db-sqlalchemy` | SQLite/异步 ORM 与连接池 | **C（刚需）** |
| 16 | `datamodel-code-generator` | `0.90.0` | `2.19` | `8.19` | `cognee` | JSON Schema 转 Pydantic 模型（`get_cognify_router` 顶层强导入） | **C（强耦合坑）** |
| 17 | `tokenizers` | `0.22.2` | `2.84` | `8.11` | `cognee`, `fastembed`, `litellm` | Rust 分词原生扩展（`ProbeModel` 与 `Fastembed` 直接调用） | **C（刚需）** |
| 18 | `black` | `26.10.1` | `1.90` | `7.96` | `datamodel-code-generator` | `mypyc` 编译 `.so` (`5.77 MiB`)，仅动态生成代码格式化时惰性用 | **B1（条件可省）** |
| 19 | `hf-xet` | `1.8.0` | `3.43` | `7.44` | `huggingface-hub` | HF Xet 协议 Rust 二进制（`HF_HUB_OFFLINE=1` 下未触发） | **B1（条件可省）** |
| 20 | `networkx` | `3.7` | `2.04` | `6.96` | `cognee` | `graph_report_retriever.py:5` 顶层强导入 | **C（强耦合坑）** |
| 21 | `dlt` | `1.30.0` | `1.37` | `4.65` | `cognee` | `dlt_temporal.py:27` / `add.py:32-36` 顶层强导入 | **C（强耦合坑）** |
| 22 | `pydantic-core` | `2.50.4` | `2.01` | `4.64` | `pydantic` | Rust 校验内核 | **C（刚需）** |
| 23 | `Pygments` | `2.21.0` | `1.19` | `4.41` | `rich` (← `rich-argparse` ← `dlt`) | 终端语法高亮词法器 | **B1（条件可省）** |
| 24 | `lupa` | `2.8` | `1.13` | `3.15` | `fakeredis` (← `cognee`) | Lua 运行时原生扩展（仅供 FakeRedis 模拟 Redis Lua） | **A2（脱钩后可省）** |
| 25 | `sqlglot` | `30.30.0` | `0.77` | `2.98` | `dlt` | `dlt/common/destination/capabilities.py:19` 顶层强导入 | **C（强耦合坑）** |
| 26 | `setuptools` | `84.2.1` | `0.84` | `2.86` | `dlt`, `pyinstaller` | 运行期零导入 | **B1（条件可省）** |
| 27 | `tiktoken` | `0.14.0` | `0.99` | `2.83` | `cognee`, `litellm` | `TikToken/adapter.py:3` 顶层强导入 | **C（刚需）** |
| 28 | `lance-namespace-urllib3-client` | `0.11.1` | `0.39` | `2.76` | `lance-namespace` (← `lancedb`, `pylance`) | `lancedb/__init__.py` → `db.py:36` 顶层强导入 | **C（刚需）** |
| 29 | `pyinstaller` | `6.16.0` | `0.98` | `2.51` | `requirements.in` | P0 比较遗留，发布流已用 `mygo build` | **A1（立即可删）** |
| 30 | `redis` | `8.1.0` | `0.53` | `2.48` | `fakeredis` (← `cognee`) | `session_manager.py:9` → `RedisAdapter.py:6` 顶层强导入 | **C（强耦合坑）** |
| — | **Top 30 合计** | — | **`321.81`** (`94.8%`) | **`932.49`** (`95.2%`) | — | — | — |
| — | **其余 141 包合计** | — | **`17.80`** (`5.2%`) | **`46.53`** (`4.8%`) | 含 `langdetect` (`2.20`)、`huggingface-hub` (`2.15`)、`rdflib` (`1.98`)、`aiohttp` (`1.97`)、`pydantic` (`1.90`)、`pypdf` (`1.70`)、`protobuf` (`1.59`)、`dateparser` (`1.43`)、`cbor2` (`1.15`) 等 | — | — |

> **关键核实结论**：`torch`、`scipy`、`transformers`、`playwright`、`unstructured`、`docling`、`neo4j`、`psycopg2`、`asyncpg`、`google-api-python-client`、`anthropic` 等均**未进入** `requirements.lock`。Cognee `1.6.7` 核心 `pyproject.toml` 声明了 53 条核心依赖（51 个发行包名），另设 41 个 extras，当前未启用任何 extra。

## 3. PyPI `cognee` Wheel 与 GitHub `ec6f408` 源码的关系

### 3.1 两者为何并存且逐文件比对存在真实差异

对经 `verify_source` 校验的 GitHub `ec6f408` 源码树与 `.local/cognee-python/lib/python3.12/site-packages/` 中 PyPI `cognee==1.6.7` wheel 逐字节比对（排除 `.pyc`/`.pyo`）：

| 包目录 | 内容相同文件 | 同路径内容不同文件 | 仅 GitHub `ec6f408` 独有 | 仅 PyPI `1.6.7` wheel 独有 |
| --- | ---: | ---: | ---: | ---: |
| `cognee/` | `3,089` | **`175`**（如 `add.py`、`cognify.py`、`forget.py`、各 router） | **`62`**（含 `prompts/coding/session_distillation_*.txt` 与新 alembic 迁移） | `0` |
| `cognee_db_workers/` | `12` | `0` | `0` | **`10`**（跨平台 `ladybug_extensions/v{0.17.0,0.19.0}/*/libjson.lbug_extension`，合计 **`32.28 MiB`**） |
| `kuzu/` | `2` | `0` | `0` | `0` |

**机制成因**：
- `ec6f408` 是上游在 PyPI `1.6.7` 发布之后的 commit（`pyproject.toml` 版本号仍为 `1.6.7`，[coding-memory-components.json](../../engine/coding-memory-components.json#L9) 记为 `1.6.7-memlane.14-coding-ec6f408`）。`engine/python/official_service_dev.py#L394-L395` 安装仅读 `.py` 的 `_install_source_only_import_hook(source)` 并将 `source` 插入 `sys.path[0]`，因此**运行时 100% 执行的是 `ec6f408` 源码，`site-packages` 中的 `cognee/` 代码完全被遮蔽**。
- Cognee 自身读取版本号时（上游 `cognee/version.py:7-26`）**优先解析源码树同级 `pyproject.toml`** 并返回 `1.6.7-local`，不依赖 `importlib.metadata.version('cognee')`（已通过 `-I -B -S` 离线复核）。

### 3.2 移除 PyPI `cognee` wheel 的 4 处阻塞点与零侵入解耦方案

直接从 `requirements.lock` 删除 `cognee==1.6.7` 会触发 **4 处已知断层**，均位于 Memlane 外置层，**无需修改 Cognee 上游任何一行源码**：

1. **Ladybug JSON 原生扩展提取（硬阻塞）**：
   - 经空 `HOME` 离线实测，`ladybug 0.19.0` 在 macOS `arm64` 上**已不再静态内置 JSON 扩展**，直接执行 `LOAD EXTENSION JSON;` 抛出 `RuntimeError: Binder exception: Extension: json is an official extension and has not been installed`。
   - Memlane 在 `engine/python/first_install_store.py#L122-L136` 与 `engine/python/official_service_dev.py#L387-L392` 调用 `engine/python/ladybug_extension.py#L34-L51`，从 `distribution('cognee').locate_file('cognee_db_workers/ladybug_extensions/v0.19.0/osx_arm64/libjson.lbug_extension')` 拷出该 **`630,440 B`（`0.60 MiB`）** 二进制至 `$HOME/.lbdb/extension/0.19.0/osx_arm64/json/`。
   - **零侵入解法（macOS / Linux 三平台统一资产表）**：按上游 `cognee_db_workers/ladybug_extensions/README.md:66-75` 说明，该二进制来源于官方 `extension.ladybugdb.com`（底层镜像 `ghcr.io/ladybugdb/extension-repo`）。将其升级为与 PyPI `cognee` wheel 完全解耦的**三平台固定 SHA-256 资产**（登记在 [first-install-components.json](../../engine/first-install-components.json) / 容器构建清单中，国内可镜像到自维护静态源，macOS 亦可评估许可后随 `.app` `Resources` 分发；PyPI wheel 提取仅作脱钩前的过渡路径），即可同时服务原生 macOS 与 [Docker 部署架构调研](docker-deployment-architecture-20261125.md) 镜像，并彻底摆脱 `22.24 MiB` 下载 / `61.18 MiB` 落盘的 PyPI `cognee` wheel：
     - `osx_arm64`（`v0.19.0`）：**`630,440 B`（`0.60 MiB`）**，SHA-256 `e6482a025de31d662ee4b32a39c991706b8cc3c2ec49673ef5d8ce45cc23ce7a`
     - `linux_arm64`（`v0.19.0`，glibc）：**`896,464 B`（`0.85 MiB`）**，SHA-256 `e5bedcd4e51f4acbe9505efb9f4ab7ba970bb5359f64b131ef8bc9c3e026712f`
     - `linux_amd64`（`v0.19.0`，glibc）：**`846,920 B`（`0.81 MiB`）**，SHA-256 `39c51fa9b1915590a500eef732c76913aeb10cd942e46e1c259497e609b97426`
2. **开发探针版本断言**：`scripts/local_prepare.py#L207` 硬编码 `assert importlib.metadata.version('cognee') == '1.6.7'`。改为校验 `cognee.__version__.startswith('1.6.7')` 叠加 `verify_source` 即可。
3. **Native 锁定探针**：[dependencies.go](../../internal/bootstrap/dependencies.go#L38-L61) 逐项核对 `requirements.lock` 中的发行包版本；同步更新 `requirements.lock` 后自动适配，无需特殊补丁。
4. **CLI 入口与 GLiNER 元数据**：PyPI wheel 声明了 `cognee-cli` console script，GLiNER demo (`cognee/tasks/graph/gliner_demo/install.py:109`) 读取 `importlib.metadata.requires('cognee')`。Memlane 不使用 GLiNER（使用 `agent_bridge` / 直连 LLM），也不通过 `cognee-cli` 启动服务（直接 `official_service_dev.py` 启动 `uvicorn.Server`）；仅需回归确认首发宿主插件脚本不调用 `cognee-cli`。

## 4. Memlane 真实运行链路的导入路径与断包实测

### 4.1 Memlane 自身代码（`engine/python/` 与 `scripts/`）的第三方依赖

经 `6b005c2` 全仓 AST 扫描，`engine/python/` 与 `scripts/` 生产代码仅直接导入 **12 个第三方发行包**：
- **`aiohttp`**：`engine/python/project_control_server.py#L11`、`engine/python/cognee_extension_gateway.py#L19-L20`、`engine/python/codex_completion_endpoint.py#L22`、`scripts/bench_scaling.py`
- **`httpx`**：`engine/python/language_egress.py#L10`、`engine/python/generation_capacity.py#L16`、`engine/python/generation_http.py#L13`
- **`litellm`**：`engine/python/official_service_dev.py#L405`、`engine/python/generation_handoff_runtime.py#L36`
- **`uvicorn`**：`engine/python/official_service_dev.py#L406`
- **`fastapi` / `starlette` / `pydantic`**：`engine/python/session_history.py#L12`、`engine/python/capture_receipts.py#L81-L82`、`engine/python/provenance_store.py#L1063`、`engine/python/generation_handoff_runtime.py#L357-L382`、`engine/python/tool_graph_service.py#L473`
- **`sqlalchemy`**：`engine/python/capture_receipts.py#L57`、`engine/python/generation_handoff_runtime.py#L121`
- **`ladybug`**：`engine/python/ladybug_extension.py#L55`、`engine/python/official_service_dev.py#L398`
- **`jsonschema`**（**易遗漏**）：`engine/python/antigravity_completion.py#L11`、`engine/python/codex_completion_endpoint.py#L23`、`engine/python/pi_completion.py#L8` 顶层导入
- **`tomlkit`**（**高优易遗漏**）：`engine/python/agent_connections.py#L14`、`engine/python/project_codex_connection.py#L16`、user_profiles/providers/codex.py（历史路径 `engine/python/user_profiles/providers/codex.py#L16`） 顶层/函数内导入；**当前 `tomlkit==0.15.2` 在 `requirements.lock` 中仅靠 `# via dlt` 间接带入**，脱钩 PyPI `cognee` wheel 编写 `requirements-slim.in` 或拆出 Docker 宿主机微型环境 `requirements-host.in` 时，必须将 Memlane 直接依赖的 `tomlkit`、`aiohttp`、`jsonschema`、`httpx`、`tenacity` 显式顶层锁定
- **`tenacity`**：`engine/python/generation_probe.py#L203`
- 测试专用额外引入：`openai`（`engine/python/test_generation_capacity.py#L17`）。

### 4.2 上游 Cognee `1.6.7` 在 Memlane 启用后端组合下的真实导入与断包实证

在隔离临时目录中配置 Memlane 完整环境变量（`CACHE_BACKEND=sqlite`、`CACHING=true`、`DISTILLATION_MODE=coding`、`IMPROVE_STAGES_DISABLED=persist_agent_traces`、`EMBEDDING_PROVIDER=fastembed`、`HF_HUB_OFFLINE=1`、`COGNEE_TRACING_ENABLED=false`），使用 `sys.meta_path` 断包拦截器对 **27 个核心模块**（`cognee`、`cognee.api.client`、`SqlAlchemyAdapter`、`SqlCacheAdapter`、`LanceDBAdapter`、`LadybugAdapter`、`FastembedEmbeddingEngine`、`litellm_native.native_adapter`、`remember`/`recall`/`improve`/`forget`/`cognify`/`search`/`add`、`lancedb_compaction`、`fastembed`、`lancedb`、`ladybug._lbug`、`uvicorn`、`litellm` 以及 Memlane 6 个主模块）执行单包与组合断包实测：

| 屏蔽测试对象 | 实测结果 | 精确失败调用链 / 关键备注 |
| --- | --- | --- |
| `pyinstaller` + `altgraph` + `macholib` | **PASS (27/27)** | 全链路零导入 |
| `fakeredis` + `lupa` + `sortedcontainers` | **PASS (27/27)** | 仅需**保留 `redis`** |
| `nbformat` + `jupyter_core` + `traitlets` + `fastjsonschema` + `platformdirs` | **PASS (27/27)** | 全链路零导入 |
| `gunicorn` / `websockets` / `pympler` / `cbor2` / `langdetect` | **PASS (27/27)** | 全链路零导入（`langdetect` 仅翻译任务内惰性） |
| `pandas` | **PASS (27/27)** | `pyarrow/dataset.py:24` 对 `pandas` 有 `try/except` 保护；需在写入/检索端到端回归复核 |
| `boto3` + `botocore` + `s3transfer` + `jmespath` | **PASS (27/27)** | `litellm` 导入与常规 OpenAI/Ollama 路径不触发；AWS Bedrock/KMS/S3 禁用 |
| `hf_xet` | **PASS (27/27)** | `HF_HUB_OFFLINE=1` 下 `huggingface_hub` 不触发 |
| `black` + `mypy_extensions` + `pathspec` + `pytokens` + `isort` + `genson` + `argcomplete` + `inflect` + `typeguard` + `jaraco` + `more_itertools` | **PASS (27/27)** | `datamodel_code_generator` 仅在调用 `generate()` 生成自定义 schema 时惰性使用 |
| `git` (`gitpython`) + `gitdb` + `smmap` + `setuptools` + `rich` + `pygments` + `markdown_it` + `mdurl` + `rich_argparse` | **PASS (27/27)** | `structlog` 在无 `rich` 时自动降级纯文本 |
| `enola` | **PASS (27/27)** | 仅在代码图谱子进程 `run_enola_generate` 调用 `bin/enola` |
| `lance` (`pylance`) | **导入 PASS，但运行期高危** | **陷阱**：`cognify.py:630` 注册 `after_run_completed=compact_vector_store` → `LanceDBAdapter.compact()`（`LanceDBAdapter.py:835`）→ `_compaction_skip_reason()`（`LanceDBAdapter.py:819-833`，L825 调用 `lance_core_mismatch()`）→ `lancedb_compaction.py:77` **无条件 `import lance`**（无 `ImportError` 保护），且旧表 schema 迁移（`LanceDBAdapter.py:998`）强制要求 `pylance` |
| **`COMBO_TIER3_43_PKGS`（44 个裁剪包除去由源码树加载的 `cognee` 本体，对其余 43 个第三方包执行组合屏蔽）** | **PASS (27/27)** | 一次性组合屏蔽全部 43 个第三方候选包，27 个 Cognee+Memlane 核心模块零报错 |
| **`pillow` (`PIL`)** | **FAIL** | `fastembed/__init__.py:3` → `fastembed/image/` → `PIL`（`FastembedEmbeddingEngine.py:13` 报错） |
| **`py_rust_stemmers` / `mmh3` / `loguru`** | **FAIL** | `fastembed/__init__.py:6` → `fastembed/sparse/bm25.py:7,9` / `model_management.py:24` |
| **`datamodel_code_generator`** | **FAIL** | `cognee/api/client.py:35` → `get_cognify_router.py:35` → `cognee/shared/graph_model_utils.py:28` |
| **`dlt`** | **FAIL** | `cognee/tasks/ingestion/resolve_dlt_sources.py:40` → `dlt_temporal.py:27`；另 `add.py:32-36` → `dlt_config.py:22` → `dlt_csv_loader.py:21` |
| **`sqlglot`** | **FAIL** | `dlt/common/destination/capabilities.py:19` → `dlt/common/libs/sqlglot.py:16` |
| **`tomlkit`** | **FAIL** | `dlt/common/configuration/providers/doc.py:1`（且 Memlane `agent_connections.py:14` 直接顶层导入） |
| **`redis`** | **FAIL** | `cognee/infrastructure/session/session_manager.py:9` → `RedisAdapter.py:6-7`（即使 `CACHE_BACKEND=sqlite`） |
| **`networkx`** | **FAIL** | `get_search_type_retriever_instance.py:32` → `cognee/modules/retrieval/graph_report_retriever.py:5` |
| **`rdflib`** | **FAIL** | `get_default_ontology_resolver.py:14` → `RDFLibOntologyResolver.py:6` |
| **`pypdf`** | **FAIL** | `document_types/__init__.py:2` → `PdfDocument.py:1` |
| **`dateparser`** | **FAIL** | `timestamp_from_text.py:6` → `cognee/modules/engine/utils/temporal_hints.py:17` |
| **`opentelemetry-api`** | **FAIL** | **隐藏陷阱**：虽 Cognee 用 `try/except` 保护，但 `fastapi==0.162.0` 的 `fastapi/background.py:5` → `fastapi/telemetry/_api.py:9` 无条件导入 `opentelemetry` |
| **`httpx2` (`httpcore2`/`truststore`)** | **FAIL** | `openai/types/batch.py:6` → `openai/_models.py:43` → `openai/_types.py:34` 顶层导入 |
| **`lance_namespace` / `lance_namespace_urllib3_client`** | **FAIL** | `lancedb/__init__.py:19` → `lancedb/db.py:36` → `lance_namespace/__init__.py:62` |
| **`filetype`** | **FAIL** | `cognee/infrastructure/files/__init__.py:1` → `get_file_metadata.py:10` → `guess_file_type.py:6` |
| **`deprecated` / `wrapt`** | **FAIL** | `cognee/__init__.py:41` → `cognee/api/v1/delete/__init__.py:3` |
| **`tiktoken` / `openai` / `litellm`** | **FAIL** | 分别为 `TikToken/adapter.py:3`、`litellm/types/llms/base.py:5`、`get_llm_router.py:8` 顶层强导入 |

## 5. A / B / C 三级可省依赖清单

### 5.1 A 类：零侵入可直接省去（不降级功能、不违背 Cognee 红线）

| 子级 | 依赖包清单 | 数量 | 实测可省下载 (`MiB`) | 实测可省解压 (`MiB`) | 实施前置与零侵入依据 |
| --- | --- | ---: | ---: | ---: | --- |
| **A1（立即可删）** | `pyinstaller`、`pyinstaller-hooks-contrib`、`macholib`、`altgraph` | 4 | `1.48` | `3.39` | 直接从 `engine/python/requirements.in#L4` 删除 `pyinstaller==6.16.0` 并重编译锁文件；[build_release.py](../../scripts/build_release.py) 已完全采用 `mygo build` |
| **A2-a（脱钩 PyPI `cognee` wheel）** | `cognee==1.6.7`（替换为三平台固定 SHA-256 `libjson.lbug_extension` 独立资产：`osx_arm64` `0.60 MiB` / `linux_arm64` `0.85 MiB` / `linux_amd64` `0.81 MiB`） | 1 | 净 **`21.64`**（`22.24 - 0.60`，macOS 口径） | 净 **`60.58`**（`61.18 - 0.60`，macOS 口径） | ① [first-install-components.json](../../engine/first-install-components.json) / 容器构建清单登记官方 `ghcr.io/ladybugdb/extension-repo` 三平台固定 SHA-256 资产（见 3.2）；② `engine/python/ladybug_extension.py#L49` 改读该独立资产路径（PyPI wheel 提取仅作过渡）；③ `scripts/local_prepare.py#L207` 改验源码版本与 `verify_source`；④ `requirements-slim.in`（及 Docker 宿主机 `requirements-host.in`）显式顶层锁定 Cognee 1.6.7 核心依赖与 Memlane 直接导入的 `tomlkit`（当前仅 `# via dlt` 间接带入）、`aiohttp`、`jsonschema`、`httpx`、`tenacity` |
| **A2-b（Cognee 零启用核心包）** | `fakeredis`、`lupa`、`sortedcontainers`、`nbformat`、`jupyter-core`、`traitlets`、`fastjsonschema`、`gunicorn`、`websockets`、`pympler`、`cbor2`、`langdetect` | 12 | `3.32` | `9.53` | 脱钩 PyPI `cognee` wheel 后自然不引入；**必须显式保留 `redis`**（因 `session_manager.py:9` 强导入）；已通过单包与组合断包探针 |
| **A 类小计** | — | **17** | **净 `26.43`** | **净 `73.51`** | 零功能降级、零 Cognee 源码改动 |

### 5.2 B 类：条件可省 / 需评估取舍

#### B1：第三方库未用传递依赖裁剪（通过 `uv pip compile --excludes` 实现，保持 `--require-hashes`）

> **机制与风险**：`uv 0.12.3` 的 `uv pip compile` 原生支持 `--excludes <file>` 参数，可生成不含以下冗余传递依赖、但仍保留全部 SHA-256 哈希的 `requirements.lock`；`uv pip sync --require-hashes` 按锁精确安装不会反拉被排除包。风险在于 `uv pip check` 会报告上游 wheel 声明的 `Requires-Dist` 缺口，须在自维护锁契约中显式登记并经端到端业务回归验收。

| 依赖组 | 包含包 | 数量 | 实测下载 (`MiB`) | 实测解压 (`MiB`) | 触发条件与风险说明 |
| --- | --- | ---: | ---: | ---: | --- |
| **B1-1 AWS SDK** | `boto3`、`botocore`、`s3transfer`、`jmespath` | 4 | `15.72` | `21.15` | 由 `litellm` 核心声明强制引入。仅当不直连 AWS Bedrock / S3 存储 / AWS KMS 加密时可删；若未来开放直连 Bedrock 需加回 |
| **B1-2 Pandas** | `pandas` | 1 | `10.23` | `37.71` | Cognee 仅用于 DLT CSV 导入（`create_dlt_source.py:524`）；`lancedb`/`pyarrow` 在缺 `pandas` 时导入正常，但**必须做完整写入/检索/旧表升级端到端回归**确保不触发任何 `to_pandas()` 路径 |
| **B1-3 HF Xet** | `hf-xet` | 1 | `3.43` | `7.44` | 由 `huggingface-hub` 引入。Memlane 固定 `HF_HUB_OFFLINE=1` 且由 Go 直接拉取 7 个模型文件，不走 Xet 协议；若未来在线按需拉取 HF 大模型需重新评估 |
| **B1-4 代码生成工具链** | `black`、`mypy-extensions`、`pathspec`、`pytokens`、`platformdirs`、`isort`、`genson`、`argcomplete`、`inflect`、`typeguard`、`jaraco-functools`、`more-itertools` | 12 | `2.45` | `10.07` | 由 `datamodel-code-generator` 拉入（生成器本体为 C 类不可删）。Memlane 从不向 `/api/v1/cognify` 传自定义 JSON Schema，导入期零依赖；若未来开放自定义图模型 API 会触发报错 |
| **B1-5 DLT 未用工具链** | `gitpython`、`gitdb`、`smmap`、`setuptools`、`rich-argparse`、`rich`、`pygments`、`markdown-it-py`、`mdurl` | 9 | `2.75` | `9.78` | 由 `dlt` 拉入（`dlt` 本体与 `sqlglot`/`pendulum`/`tomlkit` 为 C 类不可删）。不调用 `dlt` CLI、Git source 及 rich 彩色异常格式时可安全省略，`structlog` 自动回退纯文本 |
| **B1 小计** | — | **27** | **`34.59`** | **`86.14`** | 全部 27 包已纳入 `COMBO_TIER3_43_PKGS` 组合断包探针并 **27/27 PASS** |

#### B2：涉及明确功能取舍或高风险依赖

| 依赖项 | 实测下载 (`MiB`) | 实测解压 (`MiB`) | 触发条件、取舍与风险 |
| --- | ---: | ---: | --- |
| **`enola-cli==0.4.30`** | `11.28` | `51.11` | **产品取舍 / 按需下载**：Memlane 项目默认 `code_enabled: False`（`engine/python/project_control.py#L395`、#L625（历史路径 `engine/python/project_control.py#L625`）、#L1031（历史路径 `engine/python/project_control.py#L1031`）），仅在用户显式开启「代码结构图谱」索引（`internal/simpledesktop/graph.go#L790` 发送 `POST /api/v1/remember` `content_type=code`）或调用 `cognee-index-repo.sh` 时启动 `bin/enola`。**推荐方案**：首次安装不预装，用户首次点击开启项目代码结构索引时按需下载单个 `enola` 二进制（约 `11.3 MiB`），普通用户立省 `51.11 MiB` 磁盘。 |
| **`pylance==12.0.0`** | `68.99` | `169.94` | **高风险，默认本地版不建议删除**：虽不阻塞启动，但每次 `cognify` 结束后固定回调 `compact_vector_store()`（`cognify.py:630`）→ `LanceDBAdapter.compact()`（`LanceDBAdapter.py:835`）→ `_compaction_skip_reason()`（`LanceDBAdapter.py:819-833`，L825 调用 `lance_core_mismatch()`）→ `lancedb_compaction.py:77` **直接 `import lance` 无异常降级**，缺包会令 `cognify` 收尾抛 `ModuleNotFoundError`；同时丧失碎片压缩（上游 #4684 记录 6 GB 向量膨胀至 136 GB）与就地 payload schema 升级能力。仅当上游 Cognee 未来改为 `lancedb` 原生 compaction 或官方提供关闭开关时才可移除。 |
| **本地模型 INT8 量化（`bge-small-zh-v1.5`）** | 可省约 `67.9`（`91.23 → 23.32`） | 可省约 `67.9` | **触发向量空间红线**：[first-install-components.json](../../engine/first-install-components.json#L46) 显式约束 `"change_existing_vector_space": false`。FP32 切 INT8 会改变向量坐标并影响既有日常单库召回一致性，**当前版本不可更换**；仅可作为未来全库重建架构的备选。另 [embedding-components.json](../../engine/embedding-components.json) 中重复的 `tokenizer.json`（`0.42 MiB`）可复用单次下载+硬链接。 |

#### B3：纯工程磁盘/缓存治理（零功能影响、零依赖改动）

| 治理项 | 涉及代码位置 | 影响下载 | 可省落盘空间 | 口径说明与风险 |
| --- | --- | --- | --- | --- |
| **1. 清理 `<runtime>/cache`（`uv` 缓存）** | [runtime.go](../../internal/bootstrap/runtime.go#L109-L118) | `0` | **表观 `340–980 MiB` / APFS 物理 `50–350 MiB`（估算）** | `uv pip sync` 成功后删除 `filepath.Join(r.Destination, "cache")` 或改用 `--no-cache`；失败重试期间保留以支持断点续装 |
| **2. 统一透传 `PYTHONDONTWRITEBYTECODE=1`** | `scripts/dev.py`、[first_runtime.go](../../internal/bootstrap/first_runtime.go) | `0` | **`97.26 MiB` 逻辑 / `115.70 MiB` 块（已实测）** | 目前仅安装期（[runtime.go](../../internal/bootstrap/runtime.go#L181)）与 `engine/python/official_service_dev.py#L393` 禁写了 `.pyc`，`project_control_server` 等运行后在 venv 写入大量 `.pyc`；统一禁写对常驻进程仅微增冷启几十毫秒，且符合 `verify_source` 零字节码原则 |
| **3. 解包验证后清理一次性归档与 `uv`** | [tools.go](../../internal/bootstrap/tools.go#L124-L170)、[plugin.go](../../internal/bootstrap/plugin.go#L315-L347)、[environment.go](../../internal/bootstrap/environment.go#L319) | `0` | **`81.63 MiB`（已实测）** | 含 `python/artifact.tar.gz`（`16.85`）+ `uv` 归档与二进制（`17.57 + 39.12 = 56.69`，`environment.go:319` 已明示非永久运行前提，若保留 `uv` 二进制仅删归档则省 `17.57`）+ `plugin/integrations-upstream/`（`6.74 + 1.35 = 8.09`，`VerifyPlugin` 不读它） |
| **4. （可选）以随 App 嵌入的逐文件 SHA-256 清单替代永久留盘原始 tarball** | [probe.go](../../internal/bootstrap/probe.go#L231)、[native.go](../../internal/bootstrap/native.go#L43)、`engine/python/official_service_dev.py#L280` | `0` | **`79.28 MiB`（已实测）** | `cognee/service.tar.gz`（`68.95`）+ `native/artifact.tar.gz`（`10.33`）。下载后仍核对整包 SHA-256，校验通过解包后即删原归档，日常启动校验改为对照内嵌的固定 commit 逐文件 SHA-256 清单（与现行安全强度完全等价，且不违背 Cognee 零侵入红线） |
| **5. 修正 UI 估算预算文案** | [environment.go](../../internal/bootstrap/environment.go#L167-L170) | `0` | 体验优化 | 将 `runtime` 估算从 `2 GiB / 4 GiB` 改为实测准确值（现状 `~350 MiB / ~1.0 GiB`；瘦身后 `~290 MiB / ~0.85 GiB`），消除公测用户心理门槛 |
| **B3 合计** | — | **`0`** | **已实测 `178.9–258.2 MiB` + `uv cache`（估算 `50–350 MiB` 物理 / `340–980 MiB` 表观）** | 完全不修改任何 Python 包集合，独立可先落地 |

### 5.3 C 类：刚需不可省（核心底座 + 10 大「伪闲置顶层强耦合陷阱」）

1. **核心存储、向量与推理底座（约 `425 MiB` 解压 / `136 MiB` 下载）**：
   - `lancedb` (`141.28 MiB`) + `pyarrow` (`124.07 MiB`) + `lance-namespace` + `lance-namespace-urllib3-client` (`2.76 MiB`) + `deprecation`
   - `onnxruntime` (`74.40 MiB`) + `protobuf` (`1.59 MiB`) + `flatbuffers` + `fastembed` (`0.46 MiB`) + `tokenizers` (`8.11 MiB`) + `huggingface-hub` (`2.15 MiB`) + `numpy` (`19.64 MiB`) + `tqdm`
   - `ladybug` (`12.88 MiB`) + `native_openssl_arm64`（`10.33 MiB` 下载，`_lbug` `otool -L` 实测硬链接 `/opt/homebrew/opt/openssl@3/lib/libssl.3.dylib` 与 `libcrypto.3.dylib`；CPython 3.12.12 的 `_ssl` 为内置 OpenSSL 3.5.5，仅 `_lbug` 依赖外部 dylib）+ `libjson.lbug_extension`（`0.60 MiB`，0.19.0 离线动态加载必需）
   - `SQLAlchemy` (`8.65 MiB`) + `greenlet` + `aiosqlite` + `alembic` + `mako`（首装迁移 `engine/python/first_install_store.py#L122-L138` 必需）
2. **核心 LLM、网关、认证与 Memlane 自身依赖（约 `145 MiB` 解压 / `48 MiB` 下载）**：
   - `litellm` (`98.69 MiB` — 含未用的 `36.94 MiB` proxy 与 `27.67 MiB` rust_bridge，但在单一上游 wheel 内，零侵入前提下无法只装子集) + `fastuuid` + `openai` (`9.24 MiB`) + **`httpx2`/`httpcore2`/`truststore`**（`openai/_types.py:34` 强导入）+ `tiktoken` (`2.83 MiB`)
   - `fastapi` + `starlette` + `uvicorn` + `pydantic` + `pydantic-core` + `pydantic-settings` + `fastapi-users` + `fastapi-users-db-sqlalchemy` + `pwdlib` + `argon2-cffi`/`argon2-cffi-bindings` + `bcrypt` + `email-validator` + `dnspython` + `pyjwt` + `cryptography` (`10.50 MiB`) + `cffi`/`pycparser` + `python-multipart` + `makefun`
   - `aiohttp`（+ `yarl`/`multidict`/`frozenlist`/`aiosignal`/`aiohappyeyeballs`/`propcache`/`attrs`）、`httpx`（+ `httpcore`/`h11`/`h2`/`hpack`/`hyperframe`/`anyio`/`sniffio`/`idna`）、**`jsonschema`**（Memlane 3 个 completion 桥顶层导入 + `referencing`/`rpds-py`/`jsonschema-specifications`）、**`tomlkit`**（Memlane `agent_connections.py` 顶层导入 + `dlt` 强导入）、`tenacity`、`structlog`、`jinja2`/`markupsafe`、`filelock`、`python-dotenv`、`aiofiles`、`aiolimiter`、`requests`/`charset-normalizer`/`urllib3`/`certifi`、`pyyaml`、`click`、`packaging`、`typing-extensions`/`typing-inspection`/`annotated-types`/`annotated-doc`、`importlib-metadata`/`zipp`、`limits`/`deprecated`/`wrapt`、`filetype`。
3. **10 大极易误删的「伪闲置顶层强耦合陷阱」（合计约 `48.5 MiB` 解压 / 约 `17.3 MiB` 下载，零侵入红线下不可删）**：
   - `pillow`（`11.41 MiB`）+ `py-rust-stemmers`（`0.66`）+ `mmh3`（`0.20`）+ `loguru`（`0.22`）← `fastembed/__init__.py:3-6` 无条件导入图像/稀疏子模块
   - `datamodel-code-generator`（`8.19 MiB`）← `get_cognify_router.py:35` → `graph_model_utils.py:28`
   - `networkx`（`6.96 MiB`）← `get_search_type_retriever_instance.py:32` → `graph_report_retriever.py:5`
   - `dlt`（`4.65 MiB`）+ `sqlglot`（`2.98 MiB`）+ `pendulum` + `orjson` + `humanize` + `giturlparse` + `pathvalidate` + `semver` + `simplejson` + `jsonpath-ng` + `fsspec` + `pluggy` + `requirements-parser` + `tzdata` ← `resolve_dlt_sources.py:40` → `dlt_temporal.py:27` & `add.py:32-36`
   - `redis`（`2.48 MiB`）← `session_manager.py:9` → `RedisAdapter.py:6-7`（即使 `CACHE_BACKEND=sqlite`）
   - `rdflib`（`1.98 MiB`）+ `pyparsing` ← `get_default_ontology_resolver.py:14` → `RDFLibOntologyResolver.py:6`
   - `pypdf`（`1.70 MiB`）← `document_types/__init__.py:2` → `PdfDocument.py:1`
   - `dateparser`（`1.43 MiB`）+ `tzlocal` + `regex` + `pytz` + `python-dateutil` + `six` ← `timestamp_from_text.py:6` → `temporal_hints.py:17`
   - `opentelemetry-api`（`0.24 MiB`）← `fastapi==0.162.0` `fastapi/telemetry/_api.py:9` 无条件导入（与 Cognee tracing 开关无关）
   - `httpx2` + `httpcore2` + `truststore`（`0.56 MiB`）← `openai/_types.py:34` 无条件导入。

## 6. 量化瘦身收益汇总

### 6.1 Python `runtime` 下载与解压体积瘦身阶梯（基于 171 包精确字节实测）

| 方案阶梯 | 移除范围 | 剩余包数 | `runtime` 下载 (`MiB`) | 较基线降幅（下载） | `venv` RECORD 解压 (`MiB`) | 较基线降幅（解压） | 全 7 项首装总下载 (`MiB`, 官方源) |
| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: |
| **基线（现状）** | 无 | `171` | **`339.61`** | `0%` | **`979.02`** | `0%` | **`550.58`** |
| **Tier 1（立即可做）** | A1：`pyinstaller` 4 包 | `167` | **`338.13`** | `-1.48` (`-0.4%`) | **`975.63`** | `-3.39` (`-0.3%`) | **`549.10`** |
| **Tier 2（纯 A 类推荐基线）** | A1 + A2（脱钩 PyPI `cognee` wheel + 补 `0.60 MiB` 扩展 + 12 无用包） | `154` | **`313.18`** *(含 +0.60 扩展)* | **`-26.43` (`-7.8%`)** | **`905.51`** *(含 +0.60 扩展)* | **`-73.51` (`-7.5%`)** | **`524.15`** |
| **Tier 3（推荐全功能最优解）** | Tier 2 + B1（`boto3`/`botocore`、`pandas`、`hf-xet`、`black` 等 27 个未用传递包） | `127` | **`278.59`** *(含 +0.60 扩展)* | **`-61.02` (`-18.0%`)** | **`819.37`** *(含 +0.60 扩展)* | **`-159.65` (`-16.3%`)** | **`489.56`** |
| **Tier 3 + Enola 按需化（B2）** | Tier 3 + `enola-cli` 改为开启代码图谱时按需下载 | `126` | **`267.31`** *(含 +0.60 扩展)* | **`-72.30` (`-21.3%`)** | **`768.26`** *(含 +0.60 扩展)* | **`-210.76` (`-21.5%`)** | **`478.28`** |
| **理论上限（含 `pylance`，不推荐）** | 上述 + 删 `pylance`（仅作参考，会破坏 `cognify` compaction 与旧表迁移） | `125` | `198.32` | `-141.28` (`-41.6%`) | `598.33` | `-380.69` (`-38.9%`) | `409.30` |

### 6.2 叠加 B3 磁盘治理后的总落盘收益对比（净室全装，未复用 `python`/`uv`）

| 场景 | 实测常驻落盘（不含 `uv cache`） | 未清理 `uv cache`（估算） | 合计表观占用（估算） | 合计 APFS 物理占用（估算） |
| --- | ---: | ---: | ---: | ---: |
| **现状（含 `.pyc` + 未清理归档 + 未清理缓存）** | **`1,426.1 MiB`** (`1.39 GiB`) | 表观 `+340~980 MiB` / 物理 `+50~350 MiB` | `~1.8–2.4 GiB` | `~1.55–1.85 GiB` |
| **仅落地 B3（不改任何 Python 包，删 cache/禁用 `.pyc`/清 `uv` 与一次性归档，保留 `service.tar.gz`）** | **`1,247.2 MiB`** (`1.22 GiB`) | `0` | **`~1.22 GiB`**（较现状表观省 `0.6–1.2 GiB`） | **`~1.30 GiB`** |
| **Tier 3 + B3（全功能保留，暂保留 `enola` 与 `service.tar.gz`）** | **`1,087.6 MiB`** (`1.06 GiB`) | `0` | **`~1.06 GiB`** | **`~1.14 GiB`** |
| **Tier 3 + Enola 按需 + B3（含逐文件清单替代留盘 tarball）** | **`957.2 MiB`** (`0.93 GiB`) | `0` | **`~0.93 GiB`**（**低于 1 GiB**） | **`~1.01 GiB`** |

> **与 Docker 方案的协同价值**：在 [Docker 部署架构调研](docker-deployment-architecture-20261125.md) 的镜像构建中，Tier 3 + `enola` 按需化同样直接生效——按 `darwin/arm64` 实测推算的**量级估算**约为减少镜像 Python 层 **`~211 MiB`** 未压缩体积 / **`~72 MiB`** 拉取流量（注：原生 Linux 锁本就不含 macOS 专用的 `macholib`，Linux glibc `libjson.lbug_extension` 为 `0.81–0.85 MiB`，且 `manylinux` 原生 wheel 体积与 `darwin/arm64` 略有差异，精确收益须在原生 Linux 锁上实测确认）；此外容器镜像天然无宿主机 `uv cache`、`__pycache__` 和一次性归档留存，还可利用 `python:3.12-slim` 与系统 OpenSSL 省去独立 `python`（`51.9 MiB`）、`uv`（`39.1 MiB`）与 `native_openssl_arm64`（`15.8 MiB`）。

## 7. 可执行离线验证与回归方案

任何依赖裁剪（Tier 1 → Tier 3）落地前，须按以下 **4 阶段门禁**在**临时隔离目录 + 合成数据**下全部通过，严禁触碰 `.local/daily/official-store`：

### 阶段 1：锁生成、离线安装与静态校验门禁

1. **生成精简锁文件（保持全量哈希校验）**：
   ```sh
   # Tier 1：仅删 requirements.in 中的 pyinstaller==6.16.0 后重编译
   uv pip compile engine/python/requirements.in --python-version 3.12 --generate-hashes -o engine/python/requirements.lock

   # Tier 2/3：以显式核心依赖列表 + --excludes 排除清单生成带哈希锁文件
   uv pip compile engine/python/requirements-slim.in \
     --excludes engine/python/requirements-excludes.txt \
     --python-version 3.12 --generate-hashes -o "$TMPDIR/requirements.slim.lock"
   ```
2. **离线/受控 venv 同步与基础测试**：
   - 在临时目录执行 `uv venv` + `uv pip sync --no-cache --require-hashes --build-constraints <lock> <lock>`，记录 `uv pip check` 输出并显式核对因 `--excludes` 产生的预期声明缺口。
   - 执行 `sh scripts/go.sh test ./internal/bootstrap`、`python3 scripts/task.py -- python3 scripts/lint.py`、`python3 scripts/task.py -- python3 -m unittest discover -s scripts -p "test_*.py"` 以及打包产物 `--check-first-store-resources`。

### 阶段 2：离线只读导入与断包探针（零网络、零写盘）

将 `<candidate_python>` 指向裁剪后 venv，在断网只读沙箱中执行：

```sh
env -i PATH=/usr/bin:/bin HOME=/var/empty HF_HOME=/var/empty \
  XDG_CACHE_HOME=/var/empty HF_HUB_OFFLINE=1 TRANSFORMERS_OFFLINE=1 \
  PYTHON_DOTENV_DISABLED=1 COGNEE_LOG_FILE=false TELEMETRY_DISABLED=true \
  COGNEE_TRACING_ENABLED=false LITELLM_LOCAL_MODEL_COST_MAP=true \
  CACHE_BACKEND=sqlite CACHING=true DISTILLATION_MODE=coding \
  IMPROVE_STAGES_DISABLED=persist_agent_traces \
  <candidate_python> -I -B -c '
import importlib, os, sys, tempfile
from pathlib import Path
repo = Path.cwd()
sys.path.insert(0, str(repo / "engine/python"))
import official_service_dev as guard
src = guard.verify_source({
    "official_source": sys.argv[1],
    "official_archive": sys.argv[2],
})
guard._install_source_only_import_hook(src)
sys.path.insert(0, str(src))
os.chdir("/var/empty"); tempfile.tempdir = "/var/empty"
targets = [
    "cognee", "cognee.api.client",
    "cognee.infrastructure.databases.relational.sqlalchemy.SqlAlchemyAdapter",
    "cognee.infrastructure.databases.cache.sql.SqlCacheAdapter",
    "cognee.infrastructure.databases.vector.lancedb.LanceDBAdapter",
    "cognee.infrastructure.databases.graph.ladybug.adapter",
    "cognee.infrastructure.databases.vector.embeddings.FastembedEmbeddingEngine",
    "cognee.infrastructure.llm.structured_output_framework.litellm_native.native_adapter",
    "cognee.api.v1.remember.remember", "cognee.api.v1.recall.recall",
    "cognee.api.v1.improve.improve", "cognee.api.v1.forget.forget",
    "cognee.api.v1.cognify.cognify", "cognee.api.v1.search.search", "cognee.api.v1.add.add",
    "cognee_db_workers.lancedb_compaction",
    "ladybug._lbug", "lancedb", "fastembed", "uvicorn", "litellm",
    "official_service_dev", "project_control_server", "cognee_extension_gateway",
    "language_egress", "agent_connections", "v2_api",
]
for mod in targets:
    importlib.import_module(mod)
print(f"ALL {len(targets)} CORE MODULES IMPORTED CLEANLY")
' \
  .cache/coding-memory-components/service-ec6f40859e5f2df9e7862dde27d580ac3268ed43 \
  .cache/coding-memory-components/ec6f40859e5f2df9e7862dde27d580ac3268ed43.tar.gz
```

### 阶段 3：净室空 HOME 原生扩展、建库迁移与 ASGI Lifespan 验收

在临时可写 `HOME` 且**屏蔽 `/opt/homebrew/opt/openssl@3/lib`**（仅通过 `memlane-python` wrapper 的 `DYLD_LIBRARY_PATH` 暴露受管 OpenSSL）的断网环境下依次验证：
1. `ladybug_extension.prepare_json_extension(tmp_home)` + `probe_json_extension()`（`LOAD EXTENSION JSON;` + `RETURN json('{}');` 通过，验证无 PyPI `cognee` wheel 时扩展准备闭环）。
2. [model.go](../../internal/bootstrap/model.go#L179) 离线向量探针（`bge-small-zh-v1.5` 产出有限非零 512 维中文向量）。
3. `first_install_store.initialize()` 完成 Alembic 建库迁移、默认用户与 API Key 初始化。
4. 启动 `official_service_dev.py` + `project_control_server.py` 完成 ASGI lifespan、健康检查与 `/cognee/api/v1/health`。

### 阶段 4：端到端业务回归与体积复核

1. **Python 现有全量单测**（用裁剪后 `<candidate_python>` 跑 `engine/python/test_*.py`，重点覆盖 `test_official_service_dev`、`test_cognee_extension_gateway`、`test_language_egress`、`test_generation_{parameters,probe,handoff,startup}`、`test_first_install_*`、`test_codex_completion_endpoint`、`test_antigravity_bridge`、`test_pi_completion`、`test_user_profiles`）。
2. **合成数据端到端主链路（一次性临时库）**：
   - `POST /cognee/api/v1/remember`（普通文本 + 模拟会话 trace）→ `cognify` / `improve`（验证 **`compact_vector_store()` 正常完成无报错**，且删 `pandas` 后 LanceDB 写入/检索/旧表读取均不触发 `ModuleNotFoundError`）→ `recall` / `search` → `forget`。
   - 用本地确定性 stub 端点分别走 `agent_bridge` 与直连 OpenAI/Ollama 路径，验证 `language_egress.py` 改写、结构化输出与并发限流。
   - 若实施 `enola` 按需化，额外验收：未开启 `code_enabled` 时全程不报错；开启并按需拉取后 `POST /api/v1/remember` (`content_type=code`) 正常生成代码图谱。
3. **最终体积复核**：在净室重新计量四项实测指标（总下载字节、`venv` 逻辑/APFS 块大小、`<runtime>/cache` 清理后为 `0`、其余 6 项归档清理后总落盘），确认与预期误差在 `±5 MiB` 内。

## 8. 推荐落地顺序

1. **P0（零风险立即可做）**：
   - **A1**：删除 `engine/python/requirements.in#L4` 中遗留的 `pyinstaller==6.16.0` 并重锁。
   - **B3（缓存/归档/字节码 + UI 预算修正）**：`runtime.go` 安装成功后清理 `<runtime>/cache`；`tools.go` 与 `plugin.go` 解包/构建校验后删 `python/artifact.tar.gz`、`uv` 归档、`integrations-upstream/`；统一运行环境透传 `PYTHONDONTWRITEBYTECODE=1`；将 `environment.go` 的 `2 GiB / 4 GiB` 预算更正为实测值。
   - **收益**：立省 **约 `180+ MiB` 实测落盘 + `uv cache` 释放**，且 UI 展示下载量从 `~2.34 GiB` 回归真实的 `~550 MiB`。
2. **P1（高性价比瘦身）**：
   - **A2 + B1（Tier 3）**：将三平台 `libjson.lbug_extension` 独立为首装/镜像资产、脱钩冗余 PyPI `cognee` wheel、显式锁定 Memlane 顶层依赖（含 `tomlkit`）并用 `uv pip compile --excludes` 产出裁剪 44 包的带哈希锁文件，走完 4 阶段离线/净室回归。
   - **收益**：`runtime` 下载 **`-61.02 MiB`（`-18.0%`）**、venv 解压 **`-159.65 MiB`（`-16.3%`）**，总首装下载压至 **`~490 MiB`**，零功能损失。
3. **P2（可选进阶，视产品排期）**：
   - **B2 `enola-cli` 按需化**（再省 `11.28 MiB` 下载 / `51.11 MiB` 磁盘）+ **B3-4 内嵌逐文件 SHA-256 清单替代留盘原归档**（再省 `79.28 MiB` 磁盘），将首次安装后总落盘压入 **`1 GiB` 以内（约 `0.93 GiB`）**。

## 关联文档

- [Docker 部署架构调研](docker-deployment-architecture-20261125.md)
- 单人记忆库部署方向评估（历史归档）（历史路径 `docs/archive/deployment-options-20261117.md`）
- 公测发版方案与国内镜像源调研（历史归档）（历史路径 `docs/archive/release-v0.1.0-beta.1-20261121.md`）
- [系统架构总览](../architecture/overview.md) · [本地开发与配置说明](../architecture/development.md) · [工程规范](../engineering/workflow.md)
