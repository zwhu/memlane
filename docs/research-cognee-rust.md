# Cognee-RS：本地 Rust 引擎与轻量分发评估

调研日期：2026-11-07。结论：**Cognee 确实有官方 Rust 实现 Cognee-RS，包含本地记忆管线，不只是调用远程服务的 SDK。** 之前只分析 Python 运行时不完整，应把 Rust 纳入工程验证。它可以去掉 Python 解释器与 Python 包的分发，但不能据此推断最终引擎很小，也不能假定与 Python 完全等价。

本文初始调研核对官方文档、固定提交源码、仓库文件树及 Release API，当时未编译或运行引擎。后续已完成同一提交的 Rust CLI 构建、ONNX 动态库准备和合成数据持久化检查，实验归档约 80.94 MB，不含模型；实际命令、测量和限制见 [P0 验证记录](p0-validation.md)。未使用真实知识库或 Agent 配置，下文源码接口结论仍不能替代完整产品验收。

## 1. 项目身份与证据版本

官方 [Getting Started](https://docs.cognee.ai/rust/getting-started) 明确写明：

> Experimental. Cognee-RS is a Rust port of the Python `cognee` SDK.

它面向设备端运行，通过 Rust 库与 CLI 暴露 `remember`、`recall`、`improve`、`forget`，内部包含摄入、切片、图谱抽取、向量检索、删除和本地存储；另有 C API、TypeScript、Python、Java 绑定。功能对齐是目标，不是所有接口和行为都已等价的保证。[架构](https://docs.cognee.ai/rust/architecture)、[README][readme]

| 证据 | 本次固定值 | 使用边界 |
| --- | --- | --- |
| 官方仓库 | `topoteretes/cognee-rs` | 与 Python Cognee 位于同一组织 |
| 核查源码 | `151905c302fc9b5b9aad88d6b13931c09f0eac30` | 后续适配必须固定源码，不能依赖浮动 main |
| 工作区版本 | `0.2.0` | 源码版本字段不代表与同名 Release 内容相同 |
| Rust 工具链 | `1.91.1`，edition `2024` | 依赖与原生工具仍需构建验证 |
| 当日最新 Release | `v0.2.0`，`2026-07-30T17:08:04Z` | 不能用这个旧二进制验证上述较新提交的全部功能 |

来源：[固定提交](https://github.com/topoteretes/cognee-rs/commit/151905c302fc9b5b9aad88d6b13931c09f0eac30)、[workspace manifest][workspace]、[Release API][release-api]。下文固定源码与在线文档分别引用；在线文档可能继续更新。

## 2. 能减少什么，仍然要带什么

Rust 路线可以用预编译 worker 替代 Python 解释器及 Python 依赖收集流程。用户无须安装 Rust，发布方负责生成系统与架构匹配的产物。默认桌面存储仍是 **SQLite + Ladybug + LanceDB**；本地 embedding 使用 ONNX 时仍需要原生运行库和模型文件。[README][readme]、[构建要求][build]

默认构建还包含 PostgreSQL 后端、Bedrock、PDF、音频、图片等 loader 和其他功能。库与 CLI 都有 Cargo features，CLI 对 `cognee` 使用 `default-features = false`，支持有意义地裁剪依赖；应按 Memlane 实际范围选择，而不是打包所有默认 provider。[CLI manifest][cli]、[库 manifest][lib]

两个不能混淆的限制：

- `cargo build -p cognee-cli --no-default-features` 是 **compile canary**。官方明确它没有注册所需后端，默认配置无法工作；不能用这样的空功能产物证明完整引擎很小。
- `BruteForceVectorDB` 是内存向量索引；不能为了减小包而把它当成可重启恢复的 LanceDB 替代品。

来源：[构建要求][build]、[架构中的向量后端](https://docs.cognee.ai/rust/architecture)。默认构建会编译原生 C/C++ 依赖，启用 ONNX 时构建脚本可能下载 ONNX Runtime；“Rust 实现”不等于仅包含少量 Rust 代码。

### 已公开的体积参照

| 产物 | 下载字节数 | 十进制 MB | 能证明什么 |
| --- | ---: | ---: | --- |
| `cognee-capi-v0.2.0-macos-aarch64.tar.gz` | 210,182,385 | 210.18 | 官方存在 macOS ARM 的预编译 C API 归档 |
| Memlane 最小可用 Rust worker | 未测量 | 未测量 | 需按选定功能实际构建、压缩和运行 |

来源：[Release API][release-api]、[C API 下载](https://github.com/topoteretes/cognee-rs/releases/download/v0.2.0/cognee-capi-v0.2.0-macos-aarch64.tar.gz)。该归档不是最终 App、独立 CLI 或 Memlane worker；本次没有解包分析其内容。不能把它当作最小体积，也不能把它与 Python 依赖估算直接相减得到节省量。

## 3. 与 Memlane 产品规则相关的能力

| 产品需求 | 官方证据 | 接入判断 |
| --- | --- | --- |
| 本地记忆读写与图谱 | Rust 库包含 add/cognify/search 与四动词 API | 是可评估的本地引擎，不需要以 Python 作运行前提 |
| 项目隔离 | 支持 per-dataset 图库/向量库及 ACL；存储隔离默认关闭 | 必须显式配置并做 A/B 项目数据验证 |
| 不调用 LLM 的原文保存与检索 | 有 chunk 检索、上下文返回与本地 embedding；默认 remember 仍运行 cognify/improve | 完整的 index-only 写入路径、来源映射和更新删除行为待验证，不能仅以检索枚举存在认定已满足 |
| 版本、审批与恢复 | 引擎提供数据操作，不提供 Memlane 的业务审批历史 | 保留 Go 业务服务与版本库，恢复产生新版本 |
| 自动整理 | `improve` 有多个写入阶段；`review_conflicts` 尚未移植 | 不能直接运行默认 improve 后再让用户确认 |
| MCP 接入 | 官方缺口清单说明 Rust workspace 没有 MCP server crate | 使用 Memlane 原计划的 Go MCP 桥，不要求引擎自带 MCP |
| HTTP 服务 | 文档明确 `cognee-http-server` 已不在该公开仓库 | 不能把 HTTP 文档中的服务当作可直接从本仓构建的已验证组件 |
| 代码图谱 | 当前文件树未找到对应 Enola / code_graph 模块 | 不能把一般知识图谱当作文件/函数/调用图谱；等价能力或独立适配仍待核查和实现 |

来源：[Operations][operations]、[未完成能力清单][gaps]、[datasets 文档](https://docs.cognee.ai/rust/configuration/datasets-and-access)、[固定仓库树](https://github.com/topoteretes/cognee-rs/tree/151905c302fc9b5b9aad88d6b13931c09f0eac30/crates)。未发现对应文件只说明当前未找到可复用入口，不是证明任何外部实现都不存在。

### 隔离需要显式接线

Rust 配置文档将 `ENABLE_BACKEND_ACCESS_CONTROL` / `enable_access_control` 默认值列为 `false`：各 dataset 共用图与向量库。开启后，通过 dataset scope 路由到各自的 Ladybug/LanceDB 存储。直接使用 Rust `SearchBuilder` 时还需按文档开启 `with_per_dataset_storage(true)`；没有 dataset scope 的数据访问应报错。隔离开关不代替调用者的权限验证。[配置](https://docs.cognee.ai/rust/configuration/datasets-and-access)、[固定设计与实现说明][dataset]

即使显式传 dataset，在共享存储模式也不能据此认定结果已隔离。Memlane 应从空白专用目录开启隔离、验证权限与返回来源；切换已有共享库不会自动迁移数据，不能直接翻开关并宣称迁移完成。读取仍限制为服务计算的当前项目与全局集合。

### 无密钥不等于完整的无 LLM 摄入

Rust 的配置默认 embedding 为 OpenAI，Getting Started 要求显式使用 `EMBEDDING_PROVIDER=onnx` 才能固定设备内推理。较新的 cognify 文档同时记录 keyless 回退：没有可用 LLM 且 embedding 保持默认时，实际使用本地 ONNX；之后配置 LLM 又可能恢复原 provider。因此不能依赖隐式回退保持模型和向量维度稳定。[入门](https://docs.cognee.ai/rust/getting-started)、[Keyless cognify](https://docs.cognee.ai/rust/configuration/cognify#keyless-cognify)

同一文档说明，无 LLM 的 `auto` 图谱抽取会解析为 GLiNER，但该公开 Rust 仓没有注册 GLiNER extractor，可能直接失败。这个回退不能证明 Memlane 的“保存原文 → 无 LLM 索引 → 返回当前版本”已经成立。P0 必须验证可独立使用的切片/向量路径，不以 mock 或删除图谱需求代替验收。

Python FastEmbed 的 Jina/BGE registry 也不能直接当成 Rust ONNX 的模型支持表。应核对模型输入输出、tokenizer、池化、归一化和维度，再测中英混合与代码标识符检索；模型 revision 与索引 generation 一并管理。

## 4. 对工程方案的修订

主包方案保持 **MyGo UI + 自动更新 + 组件管理 + 必要的轻量 Go 服务/MCP 桥**。Rust 或 Python 的 Cognee worker 与模型都独立下载。这样主包的大小先由桌面与轻量服务决定，完整首次下载量和磁盘占用仍需另外统计。

建议先验证独立 Rust worker，在内部调用 Cognee-RS，通过 Memlane 自己定义的 JSONL 协议连接 Go 服务；这条协议需要实现，不是上游 CLI 的现成功能。暂不直接通过 C FFI 把重型引擎链接进主 App，以便引擎单独升级、准备与恢复。Python worker 保留为对照候选；不同时给普通用户下载两套完整引擎。

P0 按以下顺序收敛：

1. 固定 Rust commit、工具链与依赖，构建包含本地持久存储与 embedding 的可用 worker；按首版需求裁剪 provider/loader。
2. 用合成数据验证无 LLM 原文索引、重启后检索、项目隔离、编辑/删除/恢复与来源追溯。验证默认 improve 不会越过审批层。
3. 核查代码图谱和用户 Agent 模型桥接，记录 Rust 已有能力、需要适配的部分及成本。任何缺口都不能默默缩减产品规则。
4. 分别测主 DMG、引擎压缩归档、模型额外下载量、解包占用和启动/内存；验证独立组件下载、签名、公证与更新。
5. 按相同契约与 Python 1.6.7 比较，选定实现后再确定发布基线。只有实际构建能回答“能小多少”。

产品对照：[Nowledge Mem 研究](research-nowledge-mem.md) 已确认其采用 Rust 服务，但当前 ARM 主 DMG 仍约 371.48 MB，而且服务随主包提供。这说明要控制主下载包，分发边界与依赖选择同样关键。

工程落实见 [技术方案](technical-plan.md) 与 [实施清单](implementation-plan.md)；原 Python 的源码证据保留于 [Cognee 研究](research-cognee.md)。

[readme]: https://github.com/topoteretes/cognee-rs/blob/151905c302fc9b5b9aad88d6b13931c09f0eac30/README.md
[workspace]: https://github.com/topoteretes/cognee-rs/blob/151905c302fc9b5b9aad88d6b13931c09f0eac30/Cargo.toml
[release-api]: https://api.github.com/repos/topoteretes/cognee-rs/releases/latest
[build]: https://github.com/topoteretes/cognee-rs/blob/151905c302fc9b5b9aad88d6b13931c09f0eac30/docs/build/prerequisites.md
[cli]: https://github.com/topoteretes/cognee-rs/blob/151905c302fc9b5b9aad88d6b13931c09f0eac30/crates/cli/Cargo.toml
[lib]: https://github.com/topoteretes/cognee-rs/blob/151905c302fc9b5b9aad88d6b13931c09f0eac30/crates/lib/Cargo.toml
[operations]: https://github.com/topoteretes/cognee-rs/blob/151905c302fc9b5b9aad88d6b13931c09f0eac30/docs/operations.md
[gaps]: https://github.com/topoteretes/cognee-rs/blob/151905c302fc9b5b9aad88d6b13931c09f0eac30/docs/roadmap/not-implemented.md
[dataset]: https://github.com/topoteretes/cognee-rs/blob/151905c302fc9b5b9aad88d6b13931c09f0eac30/docs/design/per-dataset-storage.md
