> **状态说明（已退役，仅作 P0 历史基线保留）**：本文记录的是早期 Rust 原文检索实验阶段的本地 embedding 基线，已不再作为当前运行入口。**现行生产与首次安装统一使用的本地嵌入模型为 `BAAI/bge-small-zh-v1.5`**（经 FastEmbed 加载，7 个固定哈希文件清单见 [embedding-components.json](../../engine/embedding-components.json) 与 [公测发版方案](../../docs/product/release-v0.1.0-beta.1-20261121.md)）。本目录中的历史清单仅作对照留存。

# P0 本地 embedding 基线（历史）

当时 P0 原文检索候选曾选用 `multilingual-e5-base-int8.json`；按固定转换仓库 revision 下载 ONNX 和分词器，以逐文件大小及 SHA-256 校验。原模型是 `intfloat/multilingual-e5-base`，模型卡声明 MIT。正式组件分发前继续核对与收集完整许可证，不把转换仓库 README 当作许可证原文。

配方：768 维、mean pooling、L2 normalization、`query: ` 与 `passage: ` 前缀、单条批次、最大512 tokens。原文在Unicode字符边界按实际分词器切片，384 tokens、48 tokens重叠。模型文件合计295,732,082 bytes；完整首次准备还包含引擎与其他运行组件。

当时选择依据为合成数据实测（原 P0 执行记录已随 Rust 原文实验链路退役移除）。BGE中文小模型的英文smoke失败；E5-small int8及FP32的跨语言缩进查询失败。E5-base int8修正Rust输入和padding契约后，在同一9条查询上全部Top-1命中，包含中英文、代码标识符及无空格长中文末尾；这个小规模基线不等同于所有项目文本的质量保证。

保留其他清单以复现失败对照，实际权重和实验数据被Git忽略。改变模型文件、前缀、池化、维度或切片配方时生成独立索引generation，禁止混用旧向量。固定Cognee-RS适配补丁见 [deps/cognee-rs](../cognee-rs/README.md)。
