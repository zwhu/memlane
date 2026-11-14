# 项目与通用知识：固定官方能力核对

日期：2026-11-13。状态：只读源码调研；没有运行模型、索引、启停服务、改写插件配置或官方数据。本文是配套事实材料，不是实施授权或运行验收。

后续对话已确认：新项目继承用户预先设置的默认策略，自动接入；具体项目可覆盖，不要求新项目出现前逐项授权。管理侧按模板创建／关联并授予官方权限，Agent 不自行建组成为 owner。本文保留固定源码事实；最新流程见[两部分文档入口](project-knowledge-feasibility-20261113.md)。禁写的源码核查与确定方案已补在本文第8节及[正式技术方案](project-access-technical-plan-20261113.md)，不再仅列待研究；运行验证未执行。

## 结论

四项需求可以共用“项目身份 → 官方 dataset → Agent 身份与授权 → 采集／处理策略”的基础。官方已有 dataset、文档读写和授权原语，但没有在当前固定插件中发现完整的“按仓库自动建项目、动态项目策略、通用知识自动注入”流程。仅把桌面改为列出所有 dataset 不能完成需求。

用户本次已澄清：第四项为新建／关联项目；权限同时包括 Agent 项目读写和项目采集／处理控制。该需求改变此前默认所有 Agent 读写的产品方向；本次仅分析，不迁移身份或实施权限。

| 能力 | 固定官方已有 | 仍需 Memlane 适配／设计 |
| --- | --- | --- |
| 自动同步新项目 | 会话绑定 dataset、可选仓库 node_set、公开 dataset 与会话列表 | 首次写前解析仓库与 dataset、可靠项目登记、桌面增量发现与同名去重 |
| 新建／关联项目 | POST dataset，同一用户同名返回已有；UUID 定位已有组 | 项目路径／身份与数据组映射、区分空项目和未知数据组；不能只按显示名自动合并 |
| Agent 项目读写 | Agent 子用户与 API key、principal dataset 的 read/write/delete/share 授权 | 独立凭据分发、有效权限核对、切断共享 owner key／共享角色自动补授权 |
| 项目采集／处理 | 插件环境开关与官方 improve/remember 流程 | 按项目动态生效；所有入口和后台 watcher 一致检查；已接受任务和缓存的收尾语义 |
| 通用知识管理 | 官方 dataset＋文档 add/read/update/delete；普通召回可跨 dataset | 分类与编辑体验、通用知识保存约定、coding-memory 自动召回扩展、失败后的实际状态复核 |

## 核对版本与来源

- Cognee `ec6f40859e5f2df9e7862dde27d580ac3268ed43`，SDK 1.6.7；插件 `a176248914fdb32a78354854b300174203b15a5c`，版本 1.8.0，来源见 [组件锁定清单](../engine/coding-memory-components.json)。
- 本机固定源码根分别为 `.cache/coding-memory-components/service-ec6f40859e5f2df9e7862dde27d580ac3268ed43/`、`.cache/coding-memory-components/plugin-a176248914fdb32a78354854b300174203b15a5c/`。
- 当前安装插件下 `config.py`、`_project_memory.py`、`_coding_memory.py`、`_plugin_common.py`、`_dataset_access.py` 与固定归档逐文件 SHA256 相同。
- 已通过网页打开核对固定 commit 的官方 [插件 config.py](https://github.com/topoteretes/cognee-integrations/blob/a176248914fdb32a78354854b300174203b15a5c/integrations/codex/plugins/cognee/scripts/config.py)、[coding-memory 实现](https://github.com/topoteretes/cognee-integrations/blob/a176248914fdb32a78354854b300174203b15a5c/integrations/codex/plugins/cognee/scripts/_coding_memory.py)、[权限路由](https://github.com/topoteretes/cognee/blob/ec6f40859e5f2df9e7862dde27d580ac3268ed43/cognee/api/v1/permissions/routers/get_permissions_router.py)、[Agent 路由](https://github.com/topoteretes/cognee/blob/ec6f40859e5f2df9e7862dde27d580ac3268ed43/cognee/api/v1/agents/routers/get_agents_router.py)。以下判断只针对锁定版本，不代表最新上游已经或尚未补齐。

## 1. 自动发现与项目归属

### dataset 列表不能直接当项目列表

`GET /api/v1/datasets` 返回当前用户可读数据组；DTO 只有 ID、名称、创建／更新时间、owner。该接口本身没有仓库路径、项目类型或通用知识类型字段，也没有分页参数。`POST /api/v1/datasets` 接收名称，同一用户已有同名组时返回原组；新建者自动获得 read/write/share/delete。代码图、会话伴随组、通用知识等也会是 dataset，因此不能全部作为项目显示。依据：官方 [`get_datasets_router.py:148–153,186–193,300–387`](https://github.com/topoteretes/cognee/blob/ec6f40859e5f2df9e7862dde27d580ac3268ed43/cognee/api/v1/datasets/routers/get_datasets_router.py#L148)。

插件 `config.py:227–248` 先取当前 launch 已绑定的 dataset，再取配置／环境变量，默认 `agent_sessions`；`auto` 在这里表示默认组，不是按 cwd 自动创建同名项目组。`session-start.py:2098–2120` 将配置 dataset 固定到 launch record。故在 item-fe 开会话时，若宿主仍配置 memlane，不能推定会自动写入 item-fe 独立组。依据：[插件配置](https://github.com/topoteretes/cognee-integrations/blob/a176248914fdb32a78354854b300174203b15a5c/integrations/codex/plugins/cognee/scripts/config.py#L227)、[启动绑定](https://github.com/topoteretes/cognee-integrations/blob/a176248914fdb32a78354854b300174203b15a5c/integrations/codex/plugins/cognee/scripts/session-start.py#L2098)。

### 可复用的项目身份材料及限制

- `_project_memory.py:12–21,41–79` 可用真实规范路径生成 `project-<目录名>-<路径hash>` node_set；coding 模式会先取 Git 根。它只给知识分组，不改变 dataset。worktree 的不同路径可能生成不同 tag，路径移动也改变 hash；需要产品决定何时合并，不能只用 basename。
- `SessionRecord.to_dict()` 返回 session、user、dataset、agent 和统计，没有 cwd／repo 字段。会话列表需 `range=all` 及分页，否则默认只看30天；limit 最大500。依据：官方 [`models.py:78–99`](https://github.com/topoteretes/cognee/blob/ec6f40859e5f2df9e7862dde27d580ac3268ed43/cognee/modules/session_lifecycle/models.py#L78)、[`get_sessions_router.py:54–134`](https://github.com/topoteretes/cognee/blob/ec6f40859e5f2df9e7862dde27d580ac3268ed43/cognee/api/v1/sessions/routers/get_sessions_router.py#L54)。
- coding QA 的 `context` 可含 cwd、transcript_path、日期、commit、branch；这是官方约定但字段可缺。`GET sessions/{id}` 原样返回最近20条QA，不是独立项目元数据接口，遍历会读到正文，且缓存缺失时为空。可以用于有限历史辅助关联，不能证明所有项目完整发现。依据：[`coding_turns.py:44–56`](https://github.com/topoteretes/cognee/blob/ec6f40859e5f2df9e7862dde27d580ac3268ed43/cognee/infrastructure/session/coding_turns.py#L44)、[`get_sessions_router.py:463–531`](https://github.com/topoteretes/cognee/blob/ec6f40859e5f2df9e7862dde27d580ac3268ed43/cognee/api/v1/sessions/routers/get_sessions_router.py#L463)。
- 官方 Agent register DTO 支持自由 `metadata`，但当前插件的 `register_agent_via_http` payload 不传 cwd 等项目字段。Agent connection ID 是连接标识，不能直接当权限 principal。依据：[`agents/models.py:88–100`](https://github.com/topoteretes/cognee/blob/ec6f40859e5f2df9e7862dde27d580ac3268ed43/cognee/modules/agents/models.py#L88)、[`_plugin_common.py:5198–5243`](https://github.com/topoteretes/cognee-integrations/blob/a176248914fdb32a78354854b300174203b15a5c/integrations/codex/plugins/cognee/scripts/_plugin_common.py#L5198)。

推荐：新会话在首次写入前把规范仓库身份提交给小型项目登记接口，由管理侧创建／复用官方 dataset 并返回明确 UUID；桌面按登记关系自动显示。历史未知组先列为“待关联”，不从聊天正文猜项目、不静默搬移混存数据。登记只保存身份、路径、dataset 和策略，不保存正文或另建搜索索引。

## 2. Agent 项目权限

官方权限主体是 user／role／tenant，授权目标是 dataset，操作为 read/write/delete/share。接口包括 `POST/DELETE /api/v1/permissions/datasets/{principal_id}` 及有效关系查询；授权本身需对应管理权限。官方 `POST /api/v1/agents/create` 可生成 Agent 子用户和独立 key。依据：[权限接口](https://github.com/topoteretes/cognee/blob/ec6f40859e5f2df9e7862dde27d580ac3268ed43/cognee/api/v1/permissions/routers/get_permissions_router.py#L38)、[Agent 创建](https://github.com/topoteretes/cognee/blob/ec6f40859e5f2df9e7862dde27d580ac3268ed43/cognee/api/v1/agents/routers/get_agents_router.py#L77)。

同一用户的多个 key 最终仍解析成同一 user，不能按连接名或 key 标签给予不同 dataset 权限；同一 owner key 被所有 Agent 共用时，桌面上的白名单无法成为服务端限制。依据：[`api_key_jwt_strategy.py:10–13`](https://github.com/topoteretes/cognee/blob/ec6f40859e5f2df9e7862dde27d580ac3268ed43/cognee/modules/users/authentication/api_key/api_key_jwt_strategy.py#L10)。

当前官方插件默认 shared-agent-memory 会把插件 Agent 加到一个共享角色并补全用户 dataset 的 read/write；identity=auto 在不支持时可退回 principal。细粒度权限需要独立受限身份，禁用这条自动补广泛权限路径，并检查用户、角色和tenant的有效授权并集，撤掉一条直接授权未必撤掉继承权限。依据：[`config.py:65–78`](https://github.com/topoteretes/cognee-integrations/blob/a176248914fdb32a78354854b300174203b15a5c/integrations/codex/plugins/cognee/scripts/config.py#L65)、[`_plugin_common.py:2601–2685,2700–2899`](https://github.com/topoteretes/cognee-integrations/blob/a176248914fdb32a78354854b300174203b15a5c/integrations/codex/plugins/cognee/scripts/_plugin_common.py#L2601)。

建议权限最小实现：桌面管理侧保留 owner 凭据；每个受管理的 Agent 实例使用官方子用户 key；按项目知识组、可选代码组、通用知识组分别授权，不让 Agent 自建同名 owner 副本。定义“Agent”粒度为安装实例／配置实例，不能仅展示 Codex 品牌就承诺同品牌各session相互隔离。

仍待实测：跨身份会话采集、知识提炼和来源读取是否完整可用；coding memory 对 delete 权限有检查，不能只给 write 就承诺可正常维护合并知识；撤权后的官方图、向量、会话缓存、插件本地 lesson body cache和已注入上下文边界。已经进入模型上下文的内容无法靠撤权追溯收回。上述是后续验收项，本次未做权限变更或越权探测。

服务端凭据隔离也不等于对同一个 macOS 用户下有任意文件读取／shell能力的恶意 Agent 提供安全沙箱；若 Agent 能读 owner key，就可能绕过。要承诺抵抗这种对手，需要 OS／进程边界另行设计，不能把设置开关称为强隔离。

## 3. 采集和处理是独立控制

插件 `COGNEE_CAPTURE=false` 会在 coding capture 入口提前返回；普通模式的 `COGNEE_AUTO_IMPROVE_EVERY=0` 只停对应计数触发。coding 模式还有 cooldown improve，结束同步也会触发 improve。故单改一个环境变量不能证明已停全部处理，更不能自动在已启动宿主内按项目热更新。依据：[`_capture_policy.py:78–79`](https://github.com/topoteretes/cognee-integrations/blob/a176248914fdb32a78354854b300174203b15a5c/integrations/codex/plugins/cognee/scripts/_capture_policy.py#L78)、[`_coding_memory.py:1230–1247,1359–1398`](https://github.com/topoteretes/cognee-integrations/blob/a176248914fdb32a78354854b300174203b15a5c/integrations/codex/plugins/cognee/scripts/_coding_memory.py#L1230)、[`_plugin_common.py:1983–2024`](https://github.com/topoteretes/cognee-integrations/blob/a176248914fdb32a78354854b300174203b15a5c/integrations/codex/plugins/cognee/scripts/_plugin_common.py#L1983)。

推荐在复用官方插件的配置／发送边界增加统一策略解析：每个 capture、buffer drain、improve、idle／exit sync 和 recall 都核对项目、Agent、策略代次。采集关闭必须在读取／缓存新正文前生效；处理关闭保留已合法采集材料但禁止新 improve。服务端补相应准入校验，避免旧后台任务用缓存策略继续提交。已经接受的任务如何取消、等待终态或保留部分写入，单独明确，不承诺关闭后既有知识消失。

只在HTTP网关拒绝入库可以阻断服务端写入，但无法保证插件本地不先读聊天或保存buffer；只在UI隐藏按钮不能阻止插件的直接API请求。这两者都不能完整替代插件侧适配。

## 4. 通用知识管理与自动召回

存储可复用独立官方 dataset。内容按“个人偏好／通用经验”标记分类和适用条件，保留来源；项目知识不会自动提升。官方已有文档列表、raw、add、update和单文档delete，足以支撑最小管理，不必另建正文库。分页列表 limit 1–1000、offset最大1,000,000，非稳定快照；同步按官方身份和ID去重，不能把第一页当全量。依据：[dataset 文档分页](https://github.com/topoteretes/cognee/blob/ec6f40859e5f2df9e7862dde27d580ac3268ed43/cognee/api/v1/datasets/routers/get_datasets_router.py#L654)、[add](https://github.com/topoteretes/cognee/blob/ec6f40859e5f2df9e7862dde27d580ac3268ed43/cognee/api/v1/add/routers/get_add_router.py)、[update](https://github.com/topoteretes/cognee/blob/ec6f40859e5f2df9e7862dde27d580ac3268ed43/cognee/api/v1/update/routers/get_update_router.py#L29)、[delete](https://github.com/topoteretes/cognee/blob/ec6f40859e5f2df9e7862dde27d580ac3268ed43/cognee/api/v1/datasets/routers/get_datasets_router.py#L448)。

编辑不能只以HTTP 2xx判断成功。update有 `unchanged/incremental/full_rebuild/failed`，失败重建可能已刷新原文、移除旧图记忆；保留data_id不等于原内容原子回滚。应显示保存与处理两个状态，出错后按原document／pipeline ID只读核对，禁止盲重试。依据：[`update/update.py:55–65,140–158`](https://github.com/topoteretes/cognee/blob/ec6f40859e5f2df9e7862dde27d580ac3268ed43/cognee/api/v1/update/update.py#L55)、[update 路由状态](https://github.com/topoteretes/cognee/blob/ec6f40859e5f2df9e7862dde27d580ac3268ed43/cognee/api/v1/update/routers/get_update_router.py#L103)。

普通graph recall支持多个dataset UUID，但当前 coding-memory `build_block` 只从当前dataset分页列 lesson、按项目node_set过滤，没有lesson就直接返回；其 `recall_passages` 又固定单dataset和session_learnings。因而设置跨dataset环境变量或创建通用dataset，不足以证明通用知识会自动注入coding会话。依据：[`_dataset_access.py:22–40`](https://github.com/topoteretes/cognee-integrations/blob/a176248914fdb32a78354854b300174203b15a5c/integrations/codex/plugins/cognee/scripts/_dataset_access.py#L22)、[`_coding_memory.py:1537–1611,1639–1662,1947–1957`](https://github.com/topoteretes/cognee-integrations/blob/a176248914fdb32a78354854b300174203b15a5c/integrations/codex/plugins/cognee/scripts/_coding_memory.py#L1947)。

推荐在官方coding块构建处扩展为“项目候选＋允许读取的通用候选”一次统一选择与输出，控制总预算、来源标记和项目优先级；手动通用文档不能强制伪造为session lesson。空项目也要能读取通用知识。继续由Cognee检索，不另建第二次注入器和索引。

## 5. 必须明确的架构增量

四项完整实施需要两个薄适配边界：

1. **持久项目与策略登记**：身份、路径别名、dataset UUID、Agent principal、读写策略与采集／处理开关；不存正文。官方dataset列表负责事实核对，不承担缺失的项目类型元数据。
2. **官方插件策略接线**：SessionStart首次绑定之前解析项目、每次采集／处理／召回前核对策略；coding块读通用候选。可向上游贡献扩展点，或维护固定小补丁／包装入口；后者须明确版本锁定、升级重放、禁双采集和受影响测试。

若坚持官方插件一行不改、也不提供等价扩展点，只能做“已有dataset的桌面展示＋手动关联＋静态权限配置”的部分能力，不能宣称完成本次四项整体目标。网关若承担管理API和策略准入，只接白名单路由并调用官方SDK／HTTP；不能成为持有owner key的任意通用转发代理。

## 6. 后续最小验证门槛

- 新项目第一次真实会话：首次写前取得正确dataset；同名不同路径、不含Git、子目录、worktree、重开不重复创建；桌面自动出现且当前阅读不闪烁。
- 读写权限：两个真实独立principal，对明确两个dataset验证允许与拒绝；直接调用官方API仍拒绝；检查继承授权和撤权后缓存，不把connection metadata当鉴权。
- 采集／处理：活动宿主内切换，覆盖coding、ordinary、idle、exit、buffer drain；停采集后不新读写buffer；停处理后不新发improve；已接收任务给出真实终态和部分数据。
- 通用知识：官方保存／编辑／删除、原文与索引状态、空项目自动召回、项目约定优先、无权限时不注入；手动文档和官方lesson分别覆盖。
- 既有数据保护：原memlane与item-fe记录不搬移、不删除、不重放；混组历史只给明确关联／迁移方案。所有失败沿用具体问题计数，第4次同问题失败停止。

本报告所有运行行为结论均为源码契约或推断；尚未执行上述新需求验收。

## 7. 自动新项目的三条路线专项比较

本节按用户后续要求，只分析“自动同步新项目”，不启动实现。没有运行插件或hook、读取真实launch record或聊天，也没有调用模型或官方API。

| 路线 | 是否可只靠固定版现成配置 | 首次写入正确归属 | 与后续项目ACL是否兼容 | 判断 |
| --- | --- | --- | --- | --- |
| A：SessionStart前解析cwd／Git根，确定独立dataset | 否；现成env/config仅配置静态dataset | 解析绑定在首条缓存／发送前完成才可保证 | 是，独立dataset可复用官方ACL | 推荐主线；增加一个官方项目路由扩展点 |
| B：保持共享dataset，以project node_set分组 | tag本身可配置auto，但桌面目录仍需适配 | tag成功时可逻辑分组，dataset仍共享 | 否；同dataset权限覆盖所有tag | 可作展示过渡，不能作为四项共同底座 |
| C：桌面读launch record／Agent metadata后发现并改路由 | 可事后发现部分项目，不能只靠现成官方metadata自动切写入组 | 无法保证，发现前可能已写默认组 | 独立dataset后可，但需复用A或正式切换状态机 | 辅助发现与历史关联，不独立承担主线 |

### A：推荐最小接线位置与具体契约

当前 `session-start.py:2040–2043` 已有宿主payload cwd，`2075–2093` 已确定host session key，`2102–2107` 随即用静态配置dataset调用 `ensure_launch_record`。适合在两者之间加入官方扩展：解析本地仓库身份，调用受管项目解析器，拿到已确认的dataset UUID后再固定launch。不要等异步bootstrap或桌面第一次刷新才改dataset。依据：[SessionStart入口](https://github.com/topoteretes/cognee-integrations/blob/a176248914fdb32a78354854b300174203b15a5c/integrations/codex/plugins/cognee/scripts/session-start.py#L2040)。

推荐解析器请求只含服务身份、安装实例、规范仓库根／路径别名和host session key；响应含项目ID、dataset UUID、策略代次和明确状态。管理侧负责官方dataset创建／复用及登记，并按新项目默认策略自动应用权限；已有项目的明确覆盖不重置。返回pending或不可达时，不以原memlane／默认组静默回退；正常编码可继续，记忆采集状态保持未就绪。是否使用官方buffer机制、正文缓存边界及补交条件尚待确定，不能默认提前缓存被禁写或禁采集的内容，也不能把稍后同步当作无损承诺。

无需每仓配置：扩展安装在电脑级官方插件入口，对所有传入可靠cwd的宿主生效。现成静态环境变量无法计算每次cwd；终端shell wrapper可给新CLI设置dataset，但Desktop不一定经wrapper启动，不能因此承诺CLI＋Desktop全覆盖。Desktop是否触发同一SessionStart、payload中是否有正确cwd，仍需各入口实际验收。

路径归一化用真实Git根或显式普通目录；目录basename只用于显示，不作为唯一键。共享Git remote、worktree、嵌套仓库和移动目录的合并关系要明确，不能默认同remote就是同一项目。已有launch恢复保留原绑定，不由新cwd猜测覆盖用户已经选择的dataset。

并发首次登记可复用官方创建语义：`create_dataset.py:16–47` 按owner＋tenant＋name查询、生成确定性UUID；并发INSERT主键冲突后回读同scope结果。项目登记本身仍需服务身份＋规范项目键的唯一约束／锁，以及幂等状态机；不能多个Agent各自创建同名但不同owner的数据组。仅官方“dataset同名幂等”不能替代项目映射的并发控制。依据：[官方dataset并发实现](https://github.com/topoteretes/cognee/blob/ec6f40859e5f2df9e7862dde27d580ac3268ed43/cognee/modules/data/methods/create_dataset.py#L16)。

### B：共享dataset＋node_set能够和不能够完成的内容

官方 `POST search` 和 `POST recall` 的 `node_name` 可限制node_set；coding lesson元数据也带project_node_sets，因而能够做项目相关检索／候选过滤。依据：[`search`字段](https://github.com/topoteretes/cognee/blob/ec6f40859e5f2df9e7862dde27d580ac3268ed43/cognee/api/v1/search/routers/get_search_router.py#L60)、[`recall`字段](https://github.com/topoteretes/cognee/blob/ec6f40859e5f2df9e7862dde27d580ac3268ed43/cognee/api/v1/recall/routers/get_recall_router.py#L62)。

但 `GET datasets/{id}/data` 没有node_set过滤参数，知识列表必须分页拉取后按项目metadata过滤并重新定义分页；不能过滤每页后说已经显示全部。sessions列表也没有项目tag筛选。来源raw和session读取仍按dataset／owner权限控制，不能仅凭UI隐藏跨tag内容保证拒绝读取。node_set标签可以缺失或变化，未标记历史内容要保留未知归属。

因此B可以降低初次自动分组的插件改动量，但把后续项目读写权限建立在tag上需要新增服务端行／节点级授权，并覆盖图、向量、文档、session和缓存；这比复用独立dataset ACL复杂，亦违背本项目尽量复用官方能力的方向。

### C：事后发现的用途与竞态

官方launch record源码结构已有cwd、host key、session、dataset、conn_uuid，且first-writer-wins用O_EXCL创建；这是同电脑可靠关联线索，可用于发现未显示项目、诊断已有绑定或关联历史。它是官方插件私有状态，非公开项目API，格式升级需版本锁定；本次只查源码结构，没有读取实际record。依据：[`_plugin_common.py:239–280,339–378`](https://github.com/topoteretes/cognee-integrations/blob/a176248914fdb32a78354854b300174203b15a5c/integrations/codex/plugins/cognee/scripts/_plugin_common.py#L239)。

桌面可读到record时，SessionStart可能已经使用旧dataset，后续hook可立即写入；所以“桌面发现再改环境／配置”无法保证首条归属，也不会覆盖已固定launch record。窗口关闭后后台官方服务还在，但桌面观察器不在，若由后台观察器补位，则仍存在先写后发现的竞态。官方agents register支持metadata但当前插件不发送cwd，给该字段补值也只能改善发现，不能单独解决路由时序。

推荐C只用于补齐项目目录和显示当前事实；不能静默编辑官方record、重放历史材料或宣称旧写入已迁移。已经运行的item-fe会话如何绑定，要先只读核对事实，再由用户明确选择维持本轮绑定、后续新会话使用新项目，或执行正式dataset切换。

### 中途换仓与switch-dataset语义

官方明确一个Cognee session不能跨dataset。`switch-dataset.py:388–503` 的切换是：检查目标写权限 → 同步旧session（失败默认中止）→ 准备目标组 → 注册新的session和连接 → 原子换launch record → 释放旧连接 → 重启对应watcher；旧session／dataset留在touched用于最终收尾。仅改全局env、record的dataset字段或根据shell的cd即时切换都不等价。依据：[官方切换实现](https://github.com/topoteretes/cognee-integrations/blob/a176248914fdb32a78354854b300174203b15a5c/integrations/codex/plugins/cognee/scripts/switch-dataset.py#L388)。

推荐首版自动新项目以“新launch／明确工作区”固定归属；工具内临时cd或分析其他仓库不自动切写入组。若用户要在同宿主会话切项目，提供明确动作并复用完整官方切换机制，绑定新项目tag和权限，保留旧任务结果。自动切换若要覆盖每次实际工作区变化，须另外设计事件边界、并发hook、watcher、旧buffer／transcript游标的所有权，当前源码不能证明直接调用switch脚本即可满足。

## 8. 禁写、缓冲、自动整理和官方授权的确定实现依据

本节补足后续规格／技术方案的行为选择。标为“现有”的内容来自固定源码；标为“建议”的内容是本次方案建议，尚未成为用户已确认产品规则。没有进行运行实验。

### 8.1 禁写时现有自动采集到底做什么

**现有顺序明确为先本地采集、后服务器验权。** coding `capture()` 在 `_coding_memory.py:1250` 读取transcript，1291调用 `_deliver`；1314–1337先将QA／trace放官方持久warmup buffer或cloud spool，再在1298 `_drain`发送。服务端typed入口 `remember.py:630–633` 则在写官方session cache前解析dataset并验write；显式或已绑定UUID由472–503调用 `get_authorized_dataset(...,"write")`，无权就拒绝。两端行为不能混为“禁写后完全没有采集”。依据：[插件capture／buffer](https://github.com/topoteretes/cognee-integrations/blob/a176248914fdb32a78354854b300174203b15a5c/integrations/codex/plugins/cognee/scripts/_coding_memory.py#L1230)、[服务器写前验权](https://github.com/topoteretes/cognee/blob/ec6f40859e5f2df9e7862dde27d580ac3268ed43/cognee/api/v1/remember/remember.py#L472)。

`PermissionDeniedError` 默认HTTP403；typed路由将 `CogneeApiError` 交给全局处理，不包装为成功。绑定dataset不匹配是ValueError→400，缓存不可用RuntimeError→503，未知异常→409。依据：[权限错误定义42–51](https://github.com/topoteretes/cognee/blob/ec6f40859e5f2df9e7862dde27d580ac3268ed43/cognee/modules/users/exceptions/exceptions.py#L42)、[typed路由764–784](https://github.com/topoteretes/cognee/blob/ec6f40859e5f2df9e7862dde27d580ac3268ed43/cognee/api/v1/remember/routers/get_remember_router.py#L764)。

**现有拒绝会留下buffer并可能再发。** `_plugin_common.py:5717–5725` 对任意HTTPError（含403）只记状态并break；5734起只移除成功前缀，剩余保持。未来prompt／Stop／final会再drain；`run_session_improve_detailed:5951–5956` 当轮final还会短暂停顿后额外drain一次。这不是权限拒绝后停止重发的状态机。依据：[buffer drain](https://github.com/topoteretes/cognee-integrations/blob/a176248914fdb32a78354854b300174203b15a5c/integrations/codex/plugins/cognee/scripts/_plugin_common.py#L5693)、[final重试](https://github.com/topoteretes/cognee-integrations/blob/a176248914fdb32a78354854b300174203b15a5c/integrations/codex/plugins/cognee/scripts/_plugin_common.py#L5924)。

**建议采用明确策略：** 自动capture在parse_transcript前查询当前项目／Agent策略；无write或capture关闭时不读取新正文、不新建buffer；只记无正文的拒绝码和禁用边界。既有buffer遇到401／403进入blocked，不作自动重试，不改投其他组；网络暂不可达按已有合法采集的官方缓冲处理，但不能把权限未知改成允许。恢复权限后，不自动补采禁用期间内容；需要以禁用／恢复时的宿主turn边界和capture游标排除这段历史。已有blocked材料由用户显式选择继续发送或清除，原dataset／session身份保持，不能以恢复权限为由重放全部transcript。

### 8.2 显式remember与自动capture不是同一个拒绝出口

`cognee-remember.sh` 调 `_remember_http.py`。后者271–277把所有HTTP错误（含401／403）返回权威错误，shell194–200不会fallback；超时返回结果未知，不重发。只有UNREACHABLE才走shell208–211的本地CLI降级。显式remember因此不会按自动buffer路径保存403后的请求，但受管Memlane应禁用UNREACHABLE的CLI回退，避免另库写入。依据：[HTTP结果处理](https://github.com/topoteretes/cognee-integrations/blob/a176248914fdb32a78354854b300174203b15a5c/integrations/codex/plugins/cognee/scripts/_remember_http.py#L267)、[shell分支](https://github.com/topoteretes/cognee-integrations/blob/a176248914fdb32a78354854b300174203b15a5c/integrations/codex/plugins/cognee/scripts/cognee-remember.sh#L194)。

**建议：** 显式写入也先查询策略，拒绝时直接说明目标项目不可写／处理关闭，不排队、不改投通用组；实际官方403仍是最终拒绝依据。已经发送而超时的操作保留原句柄及未知结果，只查状态，不盲重发。

### 8.3 write与delete的分工必须在规格中明确

**现有：coding自动整理确实需要delete。** `distill.py:189–227` 在 `replaces_lessons=True` 时除write外验证delete，因为同State-Key的新知识会替换并删除旧lesson。插件 `delete_permission:254–282` 是doctor诊断，不是capture准入，而且只查直接grant，角色／tenant继承可能令它误报没有直接delete。依据：[服务端scope门禁](https://github.com/topoteretes/cognee/blob/ec6f40859e5f2df9e7862dde27d580ac3268ed43/cognee/modules/session_distillation/distill.py#L189)、[插件diagnostic](https://github.com/topoteretes/cognee-integrations/blob/a176248914fdb32a78354854b300174203b15a5c/integrations/codex/plugins/cognee/scripts/_coding_memory.py#L254)。

**现有owner代调用不能直接解决。** improve路由151–158将认证user传给SDK；improve的scope479–503保持该user；distill读取cache时 `user_id=scope.user_id, session_id=scope.session_id`（distill.py:299–301）。所以owner拿Agent session_id调用现成improve，不会因为桌面有父子Agent阅读可见性就自动改用Agent的cache。`get_visible_user_ids`是阅读路由可见性，不是写任务代理参数。

**建议的最小复用选择：** 将产品“允许Agent写入项目”明确映射为官方 `read＋write＋delete`，其中delete用于官方知识自动维护。该身份因此技术上也可直接调用官方删除API，UI隐藏删除按钮不能否认这一事实；产品说明不得同时承诺“Agent可自动整理”与“Agent完全无删除能力”。share仍只保留给管理身份。这是待用户认可的产品取舍，不是既定授权。

若产品必须严格区分“可写但绝不能自行删除”，则不能沿用该最小映射。确定可行但更大的替代方案为受信处理窄端点：验证调用Agent／dataset／session所有权和处理策略后，由SDK扩展将读取session的身份与维护知识的身份拆开；只给受信维护身份delete。它需要固定服务端扩展，现成HTTP improve和现成SDK参数没有这种双身份契约，不能写成零改动接线。不建议临时授delete后撤回，其窗口内其他并发请求仍可删除。

improve的拒绝也不能统一假设为403：路由163–182对fatal stage附带partial result的异常返回409（支付耗尽402），其他Cognee错误保留原码；partial result说明先前阶段可能已执行。客户端应同时识别HTTP和官方结构化阶段结果。依据：[improve路由151–182](https://github.com/topoteretes/cognee/blob/ec6f40859e5f2df9e7862dde27d580ac3268ed43/cognee/api/v1/improve/routers/get_improve_router.py#L151)。

### 8.4 官方管理API精确协议及继承关系

以下均对同一受控官方实例，管理凭据用 `X-Api-Key`，UUID是实际官方值，不是connection显示名：

| 操作 | 精确请求 | 说明 |
| --- | --- | --- |
| 创建Agent主体 | `POST /api/v1/agents/create?name=<urlencoded>`，无JSON name body | 返回DTO序列化字段agentId／agentEmail／agentApiKey；name冲突409，Agent不能创建Agent403；原key只在受保护配置中保存 |
| 新建／复用组 | `POST /api/v1/datasets`，JSON `{"name":"..."}` | 同owner同tenant名称幂等；Agent自己创建的组会自动获全权，应由管理侧建项目组 |
| 授权 | `POST /api/v1/permissions/datasets/<principal_uuid>?permission_name=read`，JSON `["<dataset_uuid>"]` | write/delete/share分别独立调用；调用者需目标组share权限 |
| 撤权 | `DELETE /api/v1/permissions/datasets/<principal_uuid>?permission_name=read`，JSON `["<dataset_uuid>"]` | 仅删除该principal与该permission的ACL；不是negative deny |
| 查principal直接授权 | `GET /api/v1/permissions/principals/<principal_uuid>/datasets?permission_name=read` | 不等同用户所有有效继承权限，角色／tenant也要核对 |
| 加／移出角色 | `POST/DELETE /api/v1/permissions/users/<user_uuid>/roles?role_id=<role_uuid>` | 仅本次迁移明确管理的角色，不自动改无关授权 |

依据：[Agent创建77–106](https://github.com/topoteretes/cognee/blob/ec6f40859e5f2df9e7862dde27d580ac3268ed43/cognee/api/v1/agents/routers/get_agents_router.py#L77)、[权限接口38–195,277–366](https://github.com/topoteretes/cognee/blob/ec6f40859e5f2df9e7862dde27d580ac3268ed43/cognee/api/v1/permissions/routers/get_permissions_router.py#L38)。

有效权限由用户直接ACL、其创建Agent的直接ACL、加入tenant的ACL及role ACL合并，最后按当前tenant过滤。没有显式deny覆盖grant；从个人ACL撤write不覆盖role的write。`get_principal_datasets`只查ACL，不因owner标签自动赋权；新建时 `create_authorized_dataset` 给创建者及其parent完整四项grant，所以不能只靠修改owner显示实现拒绝。依据：[有效权限合并11–55](https://github.com/topoteretes/cognee/blob/ec6f40859e5f2df9e7862dde27d580ac3268ed43/cognee/modules/users/permissions/methods/get_all_user_permission_datasets.py#L11)、[直接ACL查询25–35](https://github.com/topoteretes/cognee/blob/ec6f40859e5f2df9e7862dde27d580ac3268ed43/cognee/modules/users/permissions/methods/get_principal_datasets.py#L25)、[精确撤ACL43–50](https://github.com/topoteretes/cognee/blob/ec6f40859e5f2df9e7862dde27d580ac3268ed43/cognee/modules/users/permissions/methods/revoke_permission_on_dataset.py#L43)。

“所有写入口拒绝一致”应理解为同一UUID目标的权限约束，不是同HTTP码：typed entry先验write；raw remember/add/improve复用write resolver；update验write；forget/delete验delete。name方式可能创建该身份自己的同名组，UUID方式禁止这种重定向；绑定session显式UUID不匹配另报400。因此受管Agent必须只用管理侧签定的UUID，禁name创建／fallback；不能只测试一个typed403就宣称所有入口都已覆盖。依据：[统一write resolver](https://github.com/topoteretes/cognee/blob/ec6f40859e5f2df9e7862dde27d580ac3268ed43/cognee/modules/pipelines/layers/resolve_authorized_user_datasets.py)、[UUID缺失不新建29–33](https://github.com/topoteretes/cognee/blob/ec6f40859e5f2df9e7862dde27d580ac3268ed43/cognee/modules/data/methods/load_or_create_datasets.py#L29)、[update验write219](https://github.com/topoteretes/cognee/blob/ec6f40859e5f2df9e7862dde27d580ac3268ed43/cognee/api/v1/update/update.py#L219)、[forget验delete](https://github.com/topoteretes/cognee/blob/ec6f40859e5f2df9e7862dde27d580ac3268ed43/cognee/api/v1/forget/forget.py#L671)。

### 8.5 只新增策略接线，不另写采集器的确定边界

新增代码只做三件事：SessionStart绑定前解析项目；调用官方capture前判定是否允许；调用官方drain／remember／improve／recall前复核策略。官方transcript解析、redaction、QA／trace结构、发送buffer、session绑定、去重／水位、任务流程继续复用。buffer只增加blocked状态与策略代次元信息，不复制正文建另一条outbox。

策略解析器返回明确的project／dataset UUID、principal、capture/process/read/write及revision。它不接收transcript正文，不提炼知识、不转发任意官方API、不持久化聊天。主动插件入口、idle／exit watcher及显式skill共用同一策略函数；不能只包SessionStart就承诺热撤权。服务器官方ACL提供最终读写拒绝，项目capture/process准入另由受控发送边界执行；需要阻止Agent绕过process开关自行调用improve时，则必须把相应调用置于白名单窄服务端点，不能只依赖自律插件。

### 8.6 后续运行验证只证明实现，没有未决技术选型

采用上述最小映射时，实施可直接按确定契约编码。必要验证为：禁write时不新读正文／不新buffer、官方UUID403、buffer blocked不重试、explicit remember拒绝无fallback、写角色继承撤除、read/write/delete组合与coding自动维护、partial improve状态、恢复后不补采禁用期。这些是实现验收，不再把“官方到底有没有API”或“同key能否隔离Agent”列为待研究项。
