# 第二批 OpenCode／Ollama 实施与验证

范围来自[定稿方案](../multi-agent-provider-technical-plan-20261114.md)的MA07／LP05，承接第一批已实现的连接、独立主体、控制协议、生成配置及Native配置页面。首批Desktop／原生账号待补，完整目标保持active；用户明确未安装Agent可暂缓验证、Claude Code不再追加测试，继续其他工作。

## 已核实前置

- OpenCode CLI实际1.3.5已安装。独立HOME／XDG配置中一份全局插件，经真实headless宿主在两个空项目各初始化一次，项目目录保持为空；仅创建空session，无模型请求，服务正常结束。[基线证据](evidence/multi-agent-development-20261114/opencode-second-batch-baseline.json)。
- 固定官方集成归档SHA核对，提取13份原始文件，原文件摘要保持；按原lock安装30个依赖并编译，官方9项测试通过。宿主隐式SDK1.3.5，官方固定测试SDK1.18.29，版本不同，尚不能说明完整宿主兼容或Memlane权限通过。
- [当前官方全局插件说明](https://opencode.ai/docs/plugins/)与安装版CLI存在命令／配置差异；已通过实际全局目录加载入口消除首个未知，不将最新文档接口直接套到旧CLI。
- Ollama CLI已安装，默认服务不可连接，未启动。现有模型manifest仅qwen3:0.6b，旧事实质量失败保留；尚未重跑、下载模型或执行推理。后续模型选择前核对既有具体缺陷与失败计数。

## 影响与实施顺序

1. 受管官方产物：从同一固定归档构造不可变OpenCode代际，保留官方QA／trace／召回／outbox机制与原始manifest。加入私有安装绑定、凭据文件及严格补丁摘要，安装全局薄入口，不写项目配置、不读旧会话。
2. 控制与权限：复用现有独立官方主体／project_control，按实际ctx.directory和native session动态绑定项目；每次写入、召回注入及处理前核对有效策略，通用知识只读。连接不依赖宿主模型登录，明确工具和自动行为分别遵循现有语义。
3. 采集与恢复：补官方读取整段session的启用边界，避免历史回扫；401／403进入blocked，权限恢复不自动重投，明确补交才发送；unknown只按原id核对，不能通过新id或自动重试制造重复。禁写／禁处理不保留可回扫历史；处理会话保持采集隔离。
4. 实际宿主：先编译与定向状态／协议检查，再以真实OpenCode完成编码→官方原始记录→知识→新会话采用，并核对维护／只读／禁止、不同项目／通用知识、重载和其他插件保持。全局加载基线及官方单测不替代这些门槛。
5. Ollama配置：接入既有保存／验证／next／active与启动恢复，不回退到Codex或远端Provider；本机endpoint／认证语义单独核实。之后以合适且未触发停止规则的模型做实际SDK普通／Schema／事实样本及知识链路；不重复旧停止样本。
6. Native增量与交付：只接线新增类型、配置字段及真实状态，避开其他会话未提交UI。按最终候选做必要检查，遵守Cursor／Claude测试调整；继续dev、不构建release。

## 当前缺口与资源

固定官方OpenCode的dataset为插件级配置；outbox对明确401／403会清uncertain并由timer再投；captureConversation读取session messages列表。这些均须完成受管薄适配，尚未启用到Cognee或日常宿主。宿主SDK与固定测试SDK兼容、native消息／工具payload、激活边界、权限实时变化是下一步关键未知。

临时根`.local/multi-agent-opencode-20261114`仅归本任务；13份官方源、两个SDK依赖集与小型回执保留供后续补丁／兼容检查，非日常安装。全局探针的原生服务已退出；Ollama未启动。固定归档、原库、旧宿主缓存和原日常认证／配置保持；至少100GiB与105GiB运行门禁继续生效。
