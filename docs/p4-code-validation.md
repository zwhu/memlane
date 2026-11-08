# G0 代码图谱底座验证

2026-11-08。真实固定 Enola → Rust Ladybug 图存储/查询 → 重启持久化及独立归档已通过。这是 G0 历史底座验收；当时未包含 App/Agent/授权更新。后续 G1 与 G2 的实际通过范围分别见 [G1 验证](p4-code-app-validation.md)、[G2 验证](p4-code-update-validation.md)，不能以 G0 独立证明完整交付。正式规则见 [契约](p4-code-contract.md)。

## 实际闭环

`build/p4-code/real-substrate-reviewed.json` 和 `relocated-substrate.json` 均为 passed；后者运行从归档验证 hash 后重新展开的 worker。实际 Enola 0.4.30 / extractor v351、固定 Cognee-RS / Ladybug、worker 0.0.2、原文协议 1，macOS 26.6.2 arm64。静态代码抽取与查询没有模型调用。

合成 TypeScript/Python 两组跨文件样本产生 14 个规范节点、15 条关系。验收核对各语言具体 run→greet 调用和 main→greet 文件导入边、符号行号/文件 SHA/原始 fact ID、最多 3 跳的静态调用影响，查询回执与冻结 Manifest 一致。重放同一快照回执不变；worker 重启后同一 Ladybug 图可查询；新快照完整文件集合排除了删除和重命名旧路径。独立代码 dataset 查询不跨范围。

Git 忽略（含已跟踪但被忽略文件）、node_modules、隐藏凭据样本及外链不进入影子副本；原 worktree 全部文件/链接内容清单在抽取前后一致。实际输出仅在合成运行根的 shadow/.enola 与私有 HOME。项目 mcp-arch.yaml 不被复制或执行。G0 在系统临时合成根运行；G1 将使用受信服务管理目录，仍需单独验收原项目无写入。

## 来源与故障

路径使用目录 inode 祖先判断，避免 APFS 大小写或 `/var` 别名把私有输出写进源仓库。源逐级 descriptor-relative NOFOLLOW/NONBLOCK 读取并要求 regular；root descriptor 与打开的 root 身份一致。控制字符路径/名称明确拒绝。Rust 在发布前、重放和查询时逐项比较持久节点/边与冻结导入，避免 Ladybug 清洗造成来源偏移；原始 Enola fact ID 保留在 extractor_id。

`source-open-final.log` 是真实文件系统 **NO MODEL** 故障夹具：检查后替换 final symlink、父目录 symlink、FIFO 均快速拒绝；两个真实 Git 兄弟目录验证末尾空格属于路径身份。`real-substrate-reviewed.json` 的显式故障包括 APFS 输出别名、外范围、不可变重放冲突、超大源文件、控制字符和非法查询；控制字符导入使用新 dataset/snapshot，要求具体 invalid code node；非法查询要求明确错误，拒绝后同 worker 正常查询成功。最后的 `relocated-substrate-final.json` 重跑包内成员绑定修复；`archive-path-faults.json` 明确 NO MODEL，验证包外绝对 executable 或 `../library` 在执行 checker 前被拒绝。

首次 sandbox 失败保留在 `first-sandbox-failure.json`；路径规范化修复后实际通过。早期只计关系数量及控制字符重放遮蔽版本保留为历史，不能替代 reviewed 的精确关系/独立故障门禁。

## 兼容、包与检查

独立 `memlane-cognee-rust-0.0.2-darwin-arm64.tar.gz` 为 **69,952,134 bytes**，归档文件解包量 **206,998,499 bytes**；不包含模型或 Enola 抽取器。这是 worker 组件大小，不能当主 App、首次完整准备或最终磁盘量。独立抽取器、组件准备与 App/helper 的后续证据见 G1/G2，本段大小仅属于历史 G0 worker。Developer ID/公证/正式更新源与干净系统仍属 F，未通过。

`raw-corpus-v2.json` 用新重定位 worker、同 E5-base int8 配方和 OS 禁止联网，原 9 条检索 9/9 Top-1/Top-3，来源 byte range、长中文 7 分片和跨进程持久化通过。原文 generation 和协议不变；旧组件缺 code_recipe 时，新 CodeBuild/Query 明确拒绝，不影响旧原文调用。

`staged-checks.json` 记录排除后续 G1/用户改动的暂存快照全量 test/vet 和受影响 codegraph/engine race。源路径最终修改另有 `source-open-final.log` / `source-vet-final.log`，归档 probe 实际重跑通过；最新全部暂存检查与提交结果见本目录交付回执。Bits source/runtime/fixture 分组及跨组修复后复核见 `review/`，历史发现保留 resolved。源码正确不等于后续 App/Agent 验收通过。

语料回归脚本第一次沿旧默认路径覆盖 Python 历史报告。本轮已从原始 2026-11-07 完整工具 JSON 恢复全部原报告字段，记录 `evidence-path-recovery.json`；新 Rust 结果单独保存，脚本新增 --out 避免再次覆盖。恢复的是原合成验收报告，不是新的 Python 验收。

历史后续计划由 G1/G2 分别验收：持久设置/代次/任务/current receipt、用户明确开启及停止、服务重启/取消/迟到栅栏、App 三栏和受控日常 Agent；G2 完成变化合并、组件分发与包级故障。R 正常模型质量、A1b/A2b 与 F3 外部条件继续独立保留，总 Goal 未完成。
