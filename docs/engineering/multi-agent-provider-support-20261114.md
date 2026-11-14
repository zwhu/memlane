# 多 Agent / Provider 当前支持与验收矩阵

2026-11-14，dev；以当前源码及[执行账本](multi-agent-provider-execution-20261114.md)检查点二十为准。实现、固定SDK协议、真实宿主／模型、实际Native分别列证；完整首批尚未交付。历史检查点保留当时状态，不以旧“待实施”覆盖后续事实。

## 第一批 Agent

| 要求 | 当前实现与真实证据 | 尚未证明的边界 |
| --- | --- | --- |
| MA01 Codex | 原实例ID／主体／ACL保持，实际隔离CLI安装、正常编码采集、采用既有pi／TraeX知识；[初轮回执](evidence/multi-agent-development-20261114/real-codex-checkpoint.json)；后续diff返回值保留、真实25测试及新会话工具0次采用见[新证据](evidence/multi-agent-development-20261114/codex-file-change-evidence.json) | Desktop独立证据未补；MA-CODEX-FACT01失败1轮仍未结案，新知识未给相邻顺序，不能视为原错误关系已修复；旧错误Data保留；前后端混合版本不作纯单版本通过 |
| MA08 pi | 全局扩展、独立主体、官方核心传输；实际编码→官方知识→新会话无工具采用；[编码链](evidence/multi-agent-development-20261114/real-coding-hosts.json)、[实际权限](evidence/multi-agent-development-20261114/real-agent-permissions.json) | 限固定版本及编码样本；原笼统约定／口令样本仍未通过，不泛化任意聊天沉淀；分支／恢复不回扫有定向检查，不冒充全部真实分支流程 |
| MA09 TraeX | 正常交互式CLI原生插件安装／信任、真实问答与工具、知识及新会话采用；后续兼容协议处理也通过；[编码链](evidence/multi-agent-development-20261114/real-coding-hosts.json)、[跨协议](evidence/multi-agent-development-20261114/real-cross-protocol-switch.json) | 证明TraeX CLI，不证明TRAE IDE；后续知识类清单未列全，不能当完整源码清单验收 |
| MA02 Claude Code | 原生CLI、受管官方插件、首轮QA／trace、官方知识及新会话无工具采用；[Claude回执](evidence/multi-agent-development-20261114/real-claude-checkpoint.json) | DeepSeek兼容端点不是Anthropic原生账号；已记录来源新鲜度／措辞限制；旧7条未投递材料保留、未补采 |
| MA03 Cursor | 官方产物、独立主体／安装绑定、延迟召回及PA薄适配已实现并做固定SDK／适配检查；[宿主适配](evidence/multi-agent-development-20261114/host-adapters-checkpoint.json) | 本机Cursor IDE／CLI均未检测到，未有真实宿主信任、工具／无工具回答、重复导入及采用证据；无工具当轮注入限制明确保留 |

## 共享、权限与故障

| 要求 | 当前证据 | 未完成或限制 |
| --- | --- | --- |
| MA04 跨Agent／项目／通用知识 | pi→TraeX／Codex新会话采用；pi／TraeX不同项目隔离；真实Native通用默认5与项目明确3由Claude正确采用；[通用优先级](evidence/multi-agent-development-20261114/real-common-priority.json) | Cursor实链依赖宿主；来源新鲜度及错误知识不能由“召回成功”抵消 |
| MA05 独立主体、read/write/delete、无share | pi／TraeX真实官方ACL、维护CRUD、只读／禁止、禁采恢复不回扫；四主体／八组实际项目ACL在跨协议重启后完全保持；实际Native Claude卡片连续变更；[权限矩阵](evidence/multi-agent-development-20261114/real-agent-permissions.json)、[Native卡片](evidence/multi-agent-development-20261114/native-cross-agent-checkpoint.json) | Codex／Claude已补显式脚本CRUD及四主体通用知识门禁，见[新增证据](evidence/multi-agent-development-20261114/real-explicit-tools-and-common.json)；四宿主已正常升级并产生新会话绑定，Codex／Claude实际缓存只读write403／delete403及common read200通过，见[正常升级](evidence/multi-agent-development-20261114/normal-host-tool-upgrade.json)；Cursor仍待真实宿主 |
| MA06 安装部分成功／取消／配置冲突／未知 | 实际Native+生产控制代码+合成主体，真实文件系统准备、丢弃响应、取消、重开原回执与仅处理剩余项；配置冲突及摘要有定向检查；[批量恢复](evidence/multi-agent-development-20261114/native-batch-recovery.json) | 合成Native故障不替代各真实宿主升级／退出行为；真正unknown继续保留，不因回执不存在自动重装 |
| MA06 服务不可达／403明确补交 | 固定插件禁止owner／CLI fallback；实际官方403后恢复不自动发送，明确replay恰好一条；[403回执](evidence/multi-agent-development-20261114/real-403-replay.json) | 403为受控真实ACL竞态注入，不冒充普通宿主自然发生；其他宿主仍按共用核心与各自独立证据说明 |

## 第一批知识处理与Native

| 要求 | 当前证据 | 未完成或限制 |
| --- | --- | --- |
| LP01 Codex桥 | 实际官方adapter普通输出／嵌套中文null／Schema／固定事实样本通过，隔离请求进程回收；[桥接回执](evidence/multi-agent-development-20261114/bridge-lifecycle-checkpoint.json) | 远端原生严格Schema模式保持unknown；合成探针不抵消真实知识质量失败 |
| LP01 DeepSeek兼容 | OpenAI兼容与Anthropic兼容两端点均有真实模型与实际处理证据；[跨协议切换](evidence/multi-agent-development-20261114/real-cross-protocol-switch.json)、[新HTTP策略实测](evidence/multi-agent-development-20261114/redirect-protection.json) | 同一供应商的两协议不替代三类原生供应商账号 |
| LP01 OpenAI／Anthropic／Gemini原生 | 配置编译、隔离凭据、固定SDK的实际wire、普通／Schema／错误及完成状态检查；[原生协议](evidence/multi-agent-development-20261114/native-vendor-wire-protocols.json) | 三个远端原生账号同组质量样本仍缺。当前环境未配置相应key，已知pi配置只有DeepSeek；其他私有配置路径尚未提供，不宣称机器上绝不存在这些凭据 |
| LP02 接入与推理解耦 | pi／TraeX／Codex／Claude采集+API处理；API启动没有Codex认证输入，独立主体及权限保持；[真实API启动](evidence/multi-agent-development-20261114/real-provider-startup.json) | 不穷举Agent×Provider；各协议远端门槛仍独立 |
| LP03 保存／next／重启 | 真实Native保存不切换、旧验证失效、next与当前分开、重启摘要确认才active；实际旧库身份／ACL／知识保持；[切换](evidence/multi-agent-development-20261114/real-cross-protocol-switch.json) | 官方处理中的任务并非“停稳快照”，不承诺无损恢复或自动重放 |
| LP02/03 首次Provider配置 | 从空账本运行setup，实际Native调用DeepSeek验证／选next，正常stop/start健康及摘要匹配后首个active；真实首候选摘要故障清理后停止、不回退；[首次配置](evidence/multi-agent-development-20261114/first-provider-initialization.json) | 环境／官方owner预先准备，不是新安装器；真实密钥由受认证API保存，未在Native输入；不替代三类原生账号 |
| LP03 候选失败／清理未知／一次恢复 | 独立空库、真实固定官方服务进程：候选摘要故障→确认退出→旧配置一次；清理未知不启动旧配置；旧配置也失败后停止；[真实进程](evidence/multi-agent-development-20261114/startup-recovery-real-processes.json)；锁／stale验证另有[检查](evidence/multi-agent-development-20261114/startup-checkpoint.json) | 进程故障门禁不等于真实远端模型故障全矩阵，空库连续性标记不冒充已有知识恢复 |
| LP04 错误与隔离 | 401/403/429、超时unknown、截断、Schema拒绝、质量错误、固定模式、无自动换模型；三协议307/308拒绝、缓存重建保护；[探针边界](generation-probe-boundaries-20261114.md)、[重定向](generation-redirect-protection-20261114.md) | wire夹具不替代远端账号；全部宿主处理输出隔离仍按宿主及桥接实际证据，不以单测作全量声明 |
| UI01 Native | 实际1200×820列表、Provider表单、密码不回填、错误／原回执／重开、批量取消、项目checkbox与跨协议重启；[UI记录](native-settings-continuity-20261114.md)、[后续批量](connection-batch-recovery-20261114.md) | 截图在CUA会话中实际观察，部分未导出仓库PNG；原生账号及Cursor路径仍未完整验收 |

## 实施与交付范围

I01基线提交／tag、I02通用连接与迁移、I03官方核心薄适配、I06配置模块、I07启动接线、I09 Native源码已实现，各自证据范围见上表。I04／I05主编码闭环有真实证据。I08中Claude已补限定实链，Cursor未补；I06／I10不能因配置和SDK夹具通过而算远端账号完成，I01–I10完整冻结交付尚未完成。

MA07／LP05 OpenCode电脑级全流程与Ollama／本机模型Schema及质量属于原定第二批，仍单独待实施／验收；本轮不挤掉首批、不改为首批已交付，也不从完整目标记录中删除。旧图谱328字节README及旧helper停止问题不重跑。
