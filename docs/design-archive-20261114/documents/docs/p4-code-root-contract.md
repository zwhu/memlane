# G2 根模块与混合语言兼容

2026-11-08，根语言适配、真实 App、原文回归与组件兼容本机通过，见 [G2 验证](../../../p4-code-update-validation.md)。固定 Enola 0.4.30/v351 的真实输出见 `build/p4-code/g2-root-language-extraction.json`：平铺 TS/Python 源产生 `kind=module,name=.,file=.`，两种语言的根模块 ID 相同。原规范化器拒绝点路径和重复 ID，不能算完整选定语言适配。

只将 `kind=module,name=.,file=.,line=0` 视为已捕获 worktree 的根聚合节点，要求 manifest 非空。它的来源是整个已冻结 manifest，FileSHA 为空；`.` 不成为合法 source file，文件清单、符号/依赖路径及外部路径限制保持严格。不读根模块路径、不赋予独立知识证据。

同 ID 的重复记录只允许完全一致的 module kind/name/file/line/relations/其余 props，language 各为已支持的 TS/Python。跨语言模块合成 language 为空的共享模块，保留原 extractor ID，关系按既有去重规则保存。重复 symbol、file_ref、冲突 module 或未支持语言仍失败，不静默丢弃源或事实。

worker 0.0.3 / protocol 1 新增 `code_root_modules` 显式握手能力，接受上述受限根节点；原文 generation 不变，旧合法图继续读取。Go 在导入根模块前探测能力，旧 worker 拒绝该能力，不能把未经支持的图交给旧组件。App 解析器组合须固定到验证过的版本。SQLite schema 不变，不改已持久知识或成功 source receipt。

验收须用真实根 TS/Python 与同目录混合样本检查函数/文件/来源/关系；新 worker/归档重定位、旧图兼容、原文检索回归、旧组件拒绝、升级/回退及实际 App 分层记录。不能用移动样本到子目录来跳过问题。正式分发与 F1 签名约束仍需独立证据。
