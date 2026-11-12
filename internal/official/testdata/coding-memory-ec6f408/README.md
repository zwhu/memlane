# coding-memory 固定真实样本

这两篇知识来自合成库存仓库的真实 Codex 官方自动采集及 Cognee 整理，不是手写的学习结果。服务提交为 `ec6f40859e5f2df9e7862dde27d580ac3268ed43`，插件提交为 `a176248914fdb32a78354854b300174203b15a5c`。原回执位于 `build/coding-memory-codex-20261112/round-003`，来源和正文摘要见 `provenance.json`。

`catalog.json`保留实际官方目录字段，去除了本机`rawDataLocation`；两个文本文件是当时返回的完整正文。本目录只用于无服务的格式回归，不包含凭据、用户真实聊天或产品知识镜像。源业务知识质量验收未通过，不能以格式可读推定知识完整、正确或日常接入已经完成。

固定渲染契约：[lesson_store.py](https://github.com/topoteretes/cognee/blob/ec6f40859e5f2df9e7862dde27d580ac3268ed43/cognee/modules/session_distillation/lesson_store.py)。会话引用仍须通过实际服务的当前主体、dataset和唯一所属核验；metadata与文档封装只用于识别来源格式。
