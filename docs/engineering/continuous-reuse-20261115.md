# Cognee 复用巡检

## 本轮状态

- reuse线程：`01a1dfcb-dabc-7510-9833-126664820cc9`。
- 2026-11-15用户经设置线程要求改为每天一次：原30分钟高频循环已退出，不开始下一轮；本线程不创建调度。
- 本轮完成只读初步盘点，尚未完成全工程逐项等价验证。没有生产修改、提交、tag、测试、模型调用、数据写入、服务启停或新增临时环境。
- 没有占用公共action锁，无需释放其他线程的锁。
- 工作区存在大量未提交修改；设置线程确认不拥有backlog/worker，不能代原作者交接。因此不修改或提交这些候选；原库、运行依赖、权限和材料保持。
- 磁盘检查117 GiB可用。没有新增产品验收轮次或失败计数，未尝试停止项。
- 首次生成本记录的stdin Python命令因缺少编码声明被拒绝，未执行写入；属于既有E24方法问题，非产品验收失败。改用apply_patch记录，无模型或运行任务重跑。

## 目标与影响

真实Agent采集→官方buffer→冻结范围保存→官方插件提交整理→官方知识与来源→搜索及实际注入。仅在固定官方版本具有同等行为时移除重复实现，保留未知写入保护、范围冻结/取消、真实交付统计、独立身份与ACL及订阅桥。

已读AGENTS、product/design、精简错误索引及JSON、workflow、cognee-delegation及循环契约。方案与后续连续验收分别使用plan-change-impact、verify-continuous-scenarios；本轮尚未进行集成验收。

固定基线为`engine/coding-memory-components.json`中的service `ec6f40859e5f2df9e7862dde27d580ac3268ed43`、plugin `a176248914fdb32a78354854b300174203b15a5c`。核对本机缓存源码，不推测最新上游。HEAD `799ac86`，工作树脏；观察不代表取得文件所有权。

## 初步核对

| 模块 | 已读证据及结论 | 后续边界 |
| --- | --- | --- |
| 后台整理 | project_backlog_worker.process已调用官方improve_throttle_reason和run_session_improve_detailed；project_backlog保留pending关联及未知提交标记 | 属于现有未交接改动；cognee-delegation仍记待验，不能宣称委托清理已交付 |
| 保存buffer | 官方_plugin_common.drain_warmup_entries存在，本地worker另有冻结tokens、取消及未知隔离 | 按既有委托记录尚不等价，不能删安全适配 |
| pi/TraeX采集 | agent_memory_adapter实际调用官方sanitize/trace_entry/_deliver/_drain/deliver/最终improve | 本地event去重及禁用期水位承担宿主/权限边界，未证重复，保留 |
| OpenCode整理 | managed-backlog经actions.improve，后者调用client.improve并绑定已保存watermark | 需继续对照固定OpenCode官方动作语义，不能仅凭本地类名认定重复或完全委托 |
| 召回统计 | knowledge_recall只持久化event、dataset/document ID、时间，核对当前可读组 | 实际交付口径与官方搜索次数不同，保留 |
| 搜索 | search_matches.go调用官方CHUNKS_LEXICAL/CHUNKS，客户端标注匹配、合并命中、截取预览 | 是展示适配，未发现自建检索索引，不删除 |
| Provider/Ollama | generation_http控制SDK重定向和作用域清理；generation_ollama用显式验证回执补选定模型schema元数据 | 非知识引擎或模型重试重复，保持验证/正常运行边界 |
| 宿主与权限补丁 | project_host_plugin基于固定archive应用权限及新会话边界；project_tool_patch区分拒绝与未知 | 不能移除权限、禁用历史补采或错误回执保护 |

已枚举engine、internal、cmd及scripts文件，对上述边界追踪调用；其他模块尚未逐一完成等价审查。没有证据支持本轮直接删除生产实现。

## 后续单次巡检顺序

1. 先获取backlog/worker当前作者的明确交接，重读diff和交付账本；共享修改/重型动作取得原子action锁。
2. 核对固定OpenCode原生improve入口、冷却/水位/未知提交语义，明确官方职责及管理适配。
3. 继续逐模块核对其余接入、配置与呈现；证明等价后形成自有候选摘要、相关检查点及最小改动。
4. 按改动执行定向测试及必要集成，不以源码测试代替日常库真实知识与实际Agent召回。

高频循环已退出，不宣称已有每日调度或后台持续运行。仅保留本独立报告，无需资源清理。原问题计数及停止边界不变。
