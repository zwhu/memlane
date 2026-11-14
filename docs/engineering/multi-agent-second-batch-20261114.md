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

## 受管HTTP／outbox底层适配

`engine/python/opencode_plugin_patch.py`新增固定TS源的严格覆盖，尚未接入生产构造器／全局入口。派生client要求独立安装key，禁止owner登录和cloud模式，只发固定X-Api-Key并移除调用方Authorization；health复用同一规则，307／308不跟随，超时与401／403不自动重试，错误只报状态不回显响应正文。

派生outbox将明确401／403持久化为blocked，自动pump及重启均跳过，但允许后续新合法项发送。`retryBlocked(ids)`仅释放明确选择的blocked ID，整组预检；unknown或不存在ID不改变队列，正在flush时拒绝操作。此函数是底层原语，后续管理入口仍必须先核对当前权限并登记原操作，不能直接暴露为无鉴权重投。unknown继续用官方原id核对，无额外发送；隐私清洗、身份和路径过滤保持。

从固定归档提取实际TS、严格覆盖后用TypeScript编译，再运行真实Node文件队列／loopback HTTP测试。原版outbox有3个新契约用例失败、原版HTTP有4个失败；修复后5项Python检查与10项编译后Node用例通过（2.086秒），覆盖两类重启／拒绝、混合选择、并发flush、307／308读写、认证、超时和错误正文。[源码摘要与脱敏回执](evidence/multi-agent-development-20261114/opencode-managed-core.json)。这些是受管契约的本地红绿反馈，不是7轮宿主验收失败；尚无真实OpenCode／Cognee权限通过声明。

检查入口：`python3 scripts/task.py -- env PYTHONPATH=engine/python MEMLANE_OPENCODE_TEST_NODE_MODULES=/绝对路径/固定npm-ci依赖/node_modules .local/cognee-python/bin/python -m unittest test_opencode_plugin_patch`。未提供组件依赖变量时，两个编译执行检查会明确skip，不能据此判定通过；本轮提供了固定lock安装的依赖，未skip。没有跑Cursor／Claude或全量宿主安装器测试。

下一步将覆盖摘要、固定依赖及license纳入不可变安装产物，接入稳定主体／动态项目解析／有效权限，完成激活水位和真正的受认证补交入口，再进入真实编码与新会话采用。当前全局探针仅证明加载机制，不含这些受管能力。

## 项目控制客户端与原绑定核对

新增`managed-control.ts`，复用现有project_control，单独快照安装选项，只从私有安装控制token文件读取凭据。安装／native键哈希为稳定主键；元数据仅存原workspace、project／dataset ID与绑定状态，不存问答、工具正文或owner凭据。每次read／capture／process／write／delete均读取实时policy，项目和dataset身份漂移、非法权限、common目标异常、unconfirmed均拒绝；项目无读取权限时仍可按独立common只读策略召回。

首次解析前原子保存unknown。新增`GET /v1/project/binding?host_session_key=...`按调用主体核对原映射，缺失时404且不发现／创建项目。原解析响应丢失后，客户端及重启客户端仅GET核对，不重发POST；不存在的绑定持续unknown。查询沿用有效ACL核对，允许撤下失效策略，不修补grant；管理token和其他主体不能借此读原会话绑定。控制端点仅固定loopback HTTP，拒绝重定向、非私有／symlink凭据及超过64KiB响应。

strict TypeScript编译后，Node客户端连接实际Python project_control HTTP服务，官方数据／ACL由合成夹具提供。覆盖两项目、原会话固定／重启、实时maintain→read→none、common独立、自动／明确行为、已提交／未提交的丢响应、选项快照及协议异常。27项Python定向检查通过（4.591秒），包含控制8项＋HTTP6项＋outbox4项编译后Node用例；[脱敏回执](evidence/multi-agent-development-20261114/opencode-project-control.json)。这不代表真实Cognee主体或OpenCode宿主权限已验收。

下一步构造安装代际并接官方回调：待发项须携带可核对的原项目／dataset绑定元数据，发送／补交按原目标查实时策略；不能用新cwd或全局固定dataset改写旧材料归属。本地策略拒绝须单独标记，不能伪称官方HTTP403；unknown提交仍只核对。注入前重新查权限并撤旧context，激活前历史保持水位。原控制服务需正常加载新增只读路由后再启用对应代际，旧服务未提供该路由时保持未知，不回退。

临时根`.local/multi-agent-opencode-20261114`仅归本任务；13份官方源、两个SDK依赖集与小型回执保留供后续补丁／兼容检查，非日常安装。全局探针的原生服务已退出；Ollama未启动。固定归档、原库、旧宿主缓存和原日常认证／配置保持；至少100GiB与105GiB运行门禁继续生效。

## 原目标队列与拒绝来源（检查点三十）

受管官方outbox的enqueue新增可选原目标metadata，严格校验native／project／dataset后只保存这三个字段的快照，避免携带额外凭据。原目标在caller修改、重启和明确补交后保持。兼容原有底层无target用例不等于允许正式受管宿主发送无绑定材料；正式回调仍须只使用有原目标的项。

控制客户端新增`currentTarget`，先核对本机已确认原映射与队列目标一致，再GET有效policy；未绑定native／项目或dataset不一致直接拒绝，不调用resolve、不创建metadata、不从当前cwd登记新项目。该方法只返回事实，后续发送入口必须据此检查capture／write等权限，不能把返回policy等同许可。

新增`LocalPolicyDenied`供接线后在实际HTTP发送前明确拒绝。outbox将其保存为`local_policy_denied`，清除uncertain并blocked；实际401／403记录`official_auth_denied`。二者跨重启均不自动重投，明确释放仍按原ID和原目标；底层retry函数仍不能替代管理鉴权、实时权限和操作账本。

最终6项Python检查通过（3.040秒），包含22项实际编译Node用例：HTTP6／outbox7／control9。[本轮证据](evidence/multi-agent-development-20261114/opencode-original-target.json)。首次两个反馈为检查方法随新增声明和用例数未更新，纠正后全通过，不计真实宿主产品失败轮次。真实控制HTTP中的official ACL仍为夹具；尚未把以上原语接到宿主回调，不声明真实OpenCode链路通过。旧源码证据对应其原提交，当前增量摘要另列，不覆盖旧记录。

## 发送适配接线（检查点三十一）

新增`ManagedDelivery`把固定官方HTTP／outbox接到实际project_control客户端。入队前查write与can_capture，保存原native／project／dataset；发送前`currentTarget`核对原绑定与最新采集权限，再用官方client发送至该policy的数据组。未知结果仅在有read权限时查原session标记，撤权不查询、不清除未知；恢复只读后可核对原提交，不会再次发送。

官方outbox覆盖增加可选异步authorize回调，放在标记uncertain之前。控制不可达或原目标不明时队列保持原样，不能冒充未知写入；明确拒绝的新项转blocked并允许后续合法新项发送，原unknown的拒绝保持原状态。异步核对返回后在短锁内再次核对原项快照，另一个进程已领取／完成时不重复发送。两份同namespace队列并发检查实际只发送一次。

最终6项Python／27项编译Node检查通过，3.396秒，无skip；[证据与摘要](evidence/multi-agent-development-20261114/opencode-managed-delivery.json)。新增链路执行实际Python控制HTTP、固定官方TS队列／client和loopback官方端点夹具，覆盖拒绝恢复、显式底层释放、响应丢失、切换cwd／重启后的原标记核对、原目标未确认及并发领取。实际Cognee数据／ACL、正式宿主回调仍未证明；管理补交尚未接鉴权／账本，测试中显式调用底层释放只验证原语。

下一步以此适配替换官方插件pump／enqueue，并只从本次启用后实际观察到的message metadata选择单条消息读取，去掉全session历史扫描；随后连接安装代际、主体及Native卡片。处理／召回、隔离、取消及真实宿主兼容仍须按原方案验收，不从当前低层检查推定完成。

## 官方采集回调与消息边界（检查点三十二）

新增严格`managed_capture_callbacks`覆盖，把固定官方插件pump／QA／trace入队接到`ManagedDelivery`；原归档不修改。新增metadata-only `CaptureWindow`：只承认本次插件初始化后观察到、时间不早于初始化且当时允许采集的user ID；完成的assistant metadata须关联该user。按候选ID读取单条assistant及parent，读取前逐次检查采集权限，核对返回session／消息／parent／完成事实后沿官方文本过滤和清洗入队；不调用session.messages。

禁采prompt不因恢复权限变为可采；撤权时清除该session候选并将此前prompt标记为不可采，晚到完成事件不补采。已经处理的完成ID去重；重启window为空，不从历史构造候选。每session最多保留64个turn／candidate，完成ID超过4096时停用原window、等待新prompt；不写正文或新建内容索引。

实际执行固定SDK1.18.29编译后的派生官方回调，连接Python控制HTTP及loopback官方端点夹具：旧时间消息／禁采恢复／已观察后撤权／重复完成不读正文；新QA读取恰好assistant和parent两条，正确写入原dataset；新trace正常写入并清洗敏感内容，`.env`工具输出不入队。7项Python及28项编译Node通过（3.790秒，无skip），[证据](evidence/multi-agent-development-20261114/opencode-capture-callbacks.json)。宿主SDK调用是夹具，不能证明本机1.3.5兼容。

此函数明确只负责采集覆盖，尚未由正式安装器启用；原插件resolveConfig仍有ambient选项、recall／lifecycle／improve／显式工具仍须完整策略覆盖，sid／队列namespace及私有安装绑定也需收敛。不能直接把当前组合安装到日常或真实验收环境。下一步完成这些剩余覆盖和独立操作账本，再构造不可变代际及真实宿主验收；旧配置、正文、原主体与待发材料保持。
