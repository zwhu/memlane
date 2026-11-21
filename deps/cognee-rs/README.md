> **状态说明（已退役，仅作 P0 历史记录保留）**：文中引用的 `scripts/build-engine.sh`、`scripts/bootstrap-engine.py`、`scripts/apply-engine-patches.py` 已随旧版 Rust 原文构建链路退役并移除，不再作为当前运行入口。本目录中的锁文件与补丁文件按历史材料冻结保留。

# Cognee-RS 构建候选锁文件

- 上游：<https://github.com/topoteretes/cognee-rs>
- 固定提交：`151905c302fc9b5b9aad88d6b13931c09f0eac30`
- Rust：`1.91.1`
- 生成日期：2026-11-07。

上游未提交 `Cargo.lock`。这里保留本轮由 Cargo 生成的 workspace 依赖解析结果；`scripts/build-engine.sh` 将它复制到隔离源码目录，并使用 `--locked` 构建。源码归档的 SHA-256 固定在 `scripts/bootstrap-engine.py`。

锁文件包含 workspace 中未被当前 CLI feature 选中的依赖，不能用锁文件条目数推算产品依赖数量或下载包大小。此文件冻结实验候选，**不代表已通过发布验收**（原 P0 验证记录已随旧版 Rust 原文构建链路退役移除；现行架构见 [系统架构总览](../../docs/architecture/overview.md)）。

## 已验收的原文模型适配补丁

`onnx-input-contract.patch` 修正上游provider固定传入三输入及硬编码padding ID的行为：根据ONNX实际输入集合选择是否传入token_type_ids，使用本地tokenizer定义的padding token。实际E5-base模型首轮报错 `Invalid input name: token_type_ids`，修正后原9条查询、持久化及长文末尾检索通过。该修补是Memlane本地适配，未向上游提交，也不代表上游原始提交具有相同兼容性。

`patches.json` 固定补丁与前后源码哈希；`scripts/apply-engine-patches.py` 核对后应用到仓库内忽略的上游源码目录，已修补则验证复用，漂移时拒绝覆盖。构建仍使用原workspace锁文件和features；客户端主包不包含编译工具。
