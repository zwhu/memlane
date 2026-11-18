# 日常后台生效与 TraeX 自身记忆报错跟进

用户要求重点处理开发状态中的1（本机后台未加载新三桥）和4（TraeX自身记忆整合报错）。不进入release，不处理VPN或分区，不更换用户的当前处理模型，不删除历史记忆或登录。

## 连续验收与归属

1. 只读核对日常持久库、配置、启动者身份与在途请求；取得原恢复线程明确交接，登记原进程与正文摘要。
2. 正常停止已确认的UI watcher，再通过现有dev.py停旧栈；停止结果确认后同配置启动，不切换库或模型。
3. 核对新三桥目录、处理服务完整健康、原模型/主体/项目/正文保留和Native实际页面；有新在途任务先等待，未知状态不重投。
4. TraeX以现有CLI官方模型目录、功能状态、协议配置和原错误构成反馈；核对主模型与后台整合模型分别的解析，不把主请求成功当后台记忆整合成功。必要修正仅针对已证配置缺陷，保留原文件与原错误；不通过关闭功能掩盖问题。

原恢复线程01a1e887-f8af-70d3-9058-b8c05a6130a3已明确移交启停所有权，私有回执.local/daily/store-recovery-20261118/restart-handoff.json。旧owner77544，UI watcher78172，服务77546/77547/77657；实际库为.local/daily/official-store，当前existing-codex revision1 / gpt-6.1-sol。原始配置SHA256 c009ed07f589d6e4140d08b7a4a01c18c31ea711a224161af2cfa5e6227977ca。共享action.lock目录属本首次安装线程，不覆盖其他线程锁。首次只读capacity的后台/图任务/桥请求均0，catalog为空复现旧后台未加载；不是安装缺失或模型验证失败。

## TraeX初步证据与假设

原净室报错为codex_memories_write::phase2，GPT-5.4 metadata could not be resolved；该原错误保留，不记为Memlane处理桥失败。当前宿主同版0.215.0的features list显示memories experimental true；配置仅设置use_memories/generate_memories，没有显式整合模型。官方models --json返回12项，包含GPT-5.6-Sol与GPT-6.1-Sol，不含GPT-5.4。二进制同时含memories.consolidation_model、默认模型和原错误路径，尚不能单凭字符串认定运行时默认。

待验证的排序假设：1）后台整合默认模型过时：显式指向目录中模型后应能正常解析；2）配置层/安装目录覆盖：实际effective config应能定位旧模型来源；3）目录获取短暂缺项：同一会话模型目录恢复后原名应出现。先读实际配置与模型目录，不改全局主模型或关闭memories。当前没有重新执行净室原业务会话。

方法修正：只读脚本曾误用不存在的bridge_runtime函数名，改为实际resolve_bridge_runtime；--strict-config不支持features子命令，后续用官方app-server配置读取，不把这些命令错误计产品验收轮次。

## 本机后台实际生效

已按交接及新鲜容量0核对，正常结束UI watcher78172，再由dev.py stop确认旧服务组退出、释放库。相同runtime.json启动新owner93452，completion改用agent_completion_endpoint.py；完整health ready/healthy。管理API已返回Codex/gpt-6.2-sol、pi/deepseek/deepseek-flash、TraeX/GPT-6.1-Sol三种候选；当前active仍existing-codex revision1，next仍null，runtime.json SHA256完全不变。data目录346份txt的相对路径集合与逐文件SHA256与停止前完全相同。

正常重开同配置Native watcher后，实际窗口进入记忆处理：添加配置弹框能选择Codex/pi/TraeX；TraeX模型自动填GPT-6.1-Sol，pi填deepseek/deepseek-flash，均没有API Key字段；未保存检查草稿，关闭弹框后当前Codex保持。后台及watcher现在由本线程持有，保留运行。旧切换/验证历史异常未删除，也没有用本次启动覆盖其状态。

TraeX官方config/read确认use/generate均true、extract_model/consolidation_model均null；命令行单次覆盖consolidation_model=GPT-5.6-Sol可被strict app-server识别并读回，主模型仍GPT-6.1-Sol。尚不以读回成功冒充实际后台整合通过。独立合成CLI夹具首版误用exec不支持的--no-daemon，在推理前退出1，方法错误；新夹具去掉该参数，保留独立TRAE_HOME/TRAECLI_HOME及原登录只读链接。

独立合成夹具真实复现：空的私有TRAE_HOME也在第一条会话启动时报codex_memories_write::phase2 / GPT-5.4 metadata unavailable，主回答仍退出0。这排除已有个人历史或旧安装目录残留为必要条件；默认后台整合模型过时为当前已证原因。第二条会话已由原夹具自动启动，原未修配置继续保留；不手动重发相同业务。首次尝试在第二条开始前切换私有配置的保护断言发现已进入下一条，拒绝修改，尚未改全局设置。

## 用户纠正后的产品范围

用户进一步明确：“不是本机改成什么，而是产品逻辑需要使用默认模型”“其他agent都要是这个逻辑”。之前将本机TraeX后台先改5.6再对齐6.1均不符合要求，已逐字节恢复原始全局配置，原始摘要26f09413f65aa2e4220a945bc0735d69dddb98420c605518d30d4469f0950537；净室尚未改全局配置。修复转入产品调用链，不把本机配置办法当交付。

影响与验收：三桥每次进入执行槽时只读取所选Agent默认模型/供应商/显式思考设置，冻结在途请求，后续请求读取更新；不使用旧Memlane模型作为回退。隔离配置仅复制这些标量和原登录引用，保留无工具/无hooks/无插件/完整Schema。模型服务Provider保留显式模型配置。画像导入同样沿用各Agent默认设置；Codex/TraeX的画像子任务由调用参数统一指向所选默认模型，不改用户文件。Claude/pi原生CLI调用不传固定模型；安装排查只打开既有宿主，不指定模型。验证回执绑定当时实际默认值，默认改变后不显示旧模型已验证。初始化/设置共用只读默认模型展示，删除Agent模型/供应商编辑框。

DEFAULT-MODEL01：真实HTTP回归首次在三种Agent子场景均复现旧模型覆盖（obsolete-saved-model != native-first），缺默认配置时原代码仍执行旧模型；同一轮2个测试、4个失败断言不算4轮失败。修正后2项HTTP检查通过，覆盖默认变化、在途快照、实际响应模型及缺失默认拒绝。补实际stdio子场景核对Codex/TraeX的model/medium/内部记忆模型，以及pi的provider/model/high；初轮49项相关Python通过，完整参数/启动/切换回归继续执行。

## 画像失败与界面精简

用户截图要求主体仅自动导入/手动导入，失败直接报错。当前真实Codex回执退出1，仅agent_exited，原stderr未保留，不能凭旧回执断定原因。相同CLI参数的合成空任务首次诊断退出1但旧安全提取器未捕获详情，第二次改为识别native错误行后确认Error loading config.toml: invalid transport。问题发生在生成前；原逐MCP条目使用带引号的点路径可能创建仅enabled字段的伪服务器，此处归因以调用边界为准，不修改用户Codex全局文件。

画像调用改为官方exec --ignore-user-config，保留原Agent home的认证和原生记忆，仅传默认model/provider/显式思考设置及原生memory布尔配置，MCP/hooks/plugins不加载；不再逐项构造配置点路径。TraeX的exec去掉不支持的--no-daemon，TRAECLI_HOME修正至profile/cli。修正后相同合成CLI实际退出0，返回import-ready；stderr只发布脱敏错误行，不保存原始输出。保守计数UP-CODEX-TRANSPORT01：用户失败+两次同路诊断共3轮，修正后的第4次检查通过，没有第4次失败或第5次重试。原始画像尚未据此标为入库通过。

错误详情增加安全单行提取、退出码及持久回执，URL/认证/密钥/路径/邮箱脱敏；重试保留旧attempt元数据。主页面删说明卡、两步教程、重复状态/成功提示和底部说明，只保留标题、两个导入操作、实际错误与已有文档。手动弹框仅复制提示词、输入和保存/取消。首轮新增Go断言发现静态文本Label遮盖真实模型/错误的可访问名称，移除多余Label后继续检查，不将其与原CLI失败合并计数。

## 当前会话优先与统一契约

用户截图指出当前Codex为GPT-6.1-Sol/medium/Fast，而全局配置为gpt-6.2-sol。已证两者来源不同，原三桥测试的Codex6.2结果保留为历史，不标当前会话跟随通过。共享解析新增精确会话身份读取官方只读threads元数据；无匹配身份明确标agent_config，不猜最近会话。Fast从显式service_tier继承。4项读取/HTTP回归已通过。后续Agent统一遵循[有效模型技术契约](agent-effective-model-contract-20261118.md)，包含配置地址、隔离、错误与验收边界。

当前会话真实协议回归通过：Codex模型gpt-6.1-sol、medium、显式service_tier=fast，source=agent_session；原生返回default-ready，旧路由模型未覆盖且所属进程组为空。原生隔离配置中的Fast另有stdio断言验证。最新18项Codex协议及16项画像回归通过；lint及6项lint规则回归通过。更新后同配置加载owner31545，管理API返回同一会话模型，原346份正文逐文件摘要不变。Native实际记忆处理页显示gpt-6.1-sol / medium / fast；画像页已观察为仅两个导入操作，正在执行的自动导入禁用重复提交。

## 桥接展示与互斥方式回归

MODE-PRESENTATION01：用户再次指出桥接被渲染为固定模型配置，列为重复回归；目前可核实本轮截图1次失败，历史相同问题轮次待已有记录核对，不能擅自重置。已证原因是配置列表直接逐profile渲染model/provider，没有按mode统一投影；Codex因而与Provider模型混列。配置中的两条Ollama是不同模型记录，不是重复供应商；展示应按供应商及地址分组保留两模型，不能删除配置消除重复。

新增generationProfileGroups统一投影：桥接主标题是Agent身份，当前模型只作实时详情；Provider按供应商+地址分组，不合并认证或配置ID。初始化与设置共用Agent桥接/Provider两个互斥配置视图。查看或保存另一种方式不启用，实际执行仍唯一active引用；切换须既有明确启用/验证协议。ML004阻止配置列表重新直接渲染Profile.Model/Provider/BridgeKind或遍历原始Profiles绕过分组；纯投影、互斥显示与独立编辑另有Go回归，lint不替代Native验收。

Codex自动画像本次真实ready，官方库保存后重新读取19058字节，文档引用相同；私有回执profile-live-v1.json保留摘要，正文不提交仓库。原失败操作保留为attempt，不标为通过。

扩大检查发现旧OllamaSchema探针测试仍期待硬编码256/0，而生产已按省略契约取消这些值；定向重跑确认该旧断言失败。另一个全量交叉模块400在Ollama模块独跑未复现，先记录隔离未知；不能将该轮92项称全通过。修正旧断言仅要求随机Schema探针省略采样参数，显式Provider参数仍核对实际协议值，不恢复生产默认值迎合测试。

最终扩大检查确认400来自test_generation_capacity的模拟args缺auth_home/配置，不是跨模块随机问题；补齐合成配置后8项并发检查通过。全量generation最终92项通过（101.210秒）；独立导出暂存源码43项模型/协议/画像通过（7.067秒），独立Go全量通过。原两轮失败及Ollama旧断言保留。Native最终观察：Agent桥接页以Codex为主标题，Provider页只显示一组Ollama及两模型，切页后当前执行仍Agent桥接；画像文档已读取，主区两个导入操作可见，没有旧教程。仅UI查看，没有启用或删除配置。
