# pi 真链的知识与新会话采用未通过

编号 MA-KNOWLEDGE01，累计失败1轮；后续真实编码样本已通过，原约定／口令样本未重跑，不能外推为任意问答都能沉淀。关联 MA08 / LP01。首轮trace传输故障单独计入MA-TRACE01，不重复作为本问题的一轮失败。

目标：普通宿主会话自动采集、官方整理产出可用知识，新会话自动得到并采用项目约定。仅API健康、Schema探针、问答持久化或工具事件通过均不替代这个目标。

已证事实：复用官方trace格式后，pi0.84.1正常CLI对话留下1条问答、2条bash记录；remember缓冲为空，final improve正常接受，官方完成问答／工具持久化、整理与全局索引阶段。整理回执为 `no_accepted_lessons`，知识文档数0；新pi会话的自动注入字符数0，未说出合成项目约定。证据见[真链检查点](evidence/multi-agent-development-20261114/real-pi-checkpoint.json)。

生成使用真实现有pi DeepSeek配置，供应商key仅临时私有复制，没有使用伪模型或跨Provider fallback。独立小Schema探针通过，只证明其固定合成样本；本轮结果说明它不足以代替完整SDK任务质量验证。

未知：尚未取得逐条writer／judge拒绝原因，不能判定为模型能力、适配实现、采样内容或产品检索设计单一原因。样本以项目约定和合成“发布口令”为主，是否符合官方coding模式的可沉淀经验标准须先核对；“发布口令”是明确合成字符串，不能外推为真实凭据应进入知识。测试项目位于Memlane仓库子目录，宿主还继承了祖先AGENTS，后续夹具需避免这类无关上下文。

后续先核对官方coding gate／judge标准和已有通过的质量样本，再确定一次完整用例及其可观察验收点；不盲目重跑相同材料，不修改官方引擎降低门槛，不手工写入知识来冒充自动链通过。原trace失败及此处失败计数保留。TraeX、其他宿主／Provider和Native跨进程操作恢复等独立工作仍可继续。

2026-11-14补验：固定coding writer要求可用于代码工作的具体仓库事实，首轮笼统约定／合成口令不适合作为该能力唯一门槛；没有取得旧轮逐条judge拒绝原因，因此仍不将此认定为旧失败的唯一根因。没有降低官方门槛或改引擎。使用独立仓库正常复现并修复零值bug，官方产生3条知识；新会话空召回另行确认为MA-RECALL01的项目初始化缺口。补官方begin/prepare后，同库pi新增retry_options，真实7项测试通过、官方生成4条带项目标签知识并被新pi和TraeX采用；TraeX正常新增describe_retry，真实10项测试通过、官方生成2条知识并被新TraeX采用。知识逐条对照真实源码、宿主工具输出及独立回归，未手工保存知识。限定链路证据见[real-coding-hosts.json](evidence/multi-agent-development-20261114/real-coding-hosts.json)。
