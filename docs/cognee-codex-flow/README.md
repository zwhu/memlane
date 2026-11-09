# Codex、Cognee 与 Memlane 的动态记忆流程

打开 [动态流程图](index.html)，点击「播放流程」。页面可以离线运行，支持暂停、逐步前进、拖动时间轴和点击节点。右侧同时展示该步产物、尚未完成的工作、数据位置和源码依据。

**会话增量先保存；最终知识由后续 improve 提炼。** 采集时可能生成反馈或轻量上下文候选，这些不能当成最终学习知识。Memlane 的接收可以与 Cognee 后台提炼并行：先收到 raw，之后再补入已经处理的 knowledge。

本图核对官方 Codex 插件 **1.7.7**、上游 **997fd6f72253aa97b49996eb1b032793f0257935**，以及本仓库实装 Cognee **1.6.7**。Memlane 源码核对期间主分支已经推进到 `578dafc19e672380f66d0c9746f87cfb16171c76`；每步引用在 [facts.json](facts.json)。版本证据见 [真实插件接入验证](../agent-integrations-validation.md)。插件自行安装 pin 1.6.5 属于另一条安装路径。

示例为 1 条 QA、2 条 Trace 和 1 条学习知识，用来说明时序。页面不读取真实会话、数据库、Agent 配置或凭据，也不调用模型。这是调用链说明，不是 App 生产界面或实时运行监控。

## 怎么观察

| 操作 | 可以看清什么 |
| --- | --- |
| 正常流程，播放到第 3 步 | `session_stored` 表示会话条目保存，学习知识仍为 0 |
| 切换「短会话：尚未触发同步」 | 本轮采集已保存，但自动条件未满足且尚未结束同步 |
| 切换「Cognee 失联」 | 未送达条目先落插件本地暂存，恢复后补送 |
| 切换「原始已持久化：知识仍处理中」 | 官方原始材料和 Memlane raw 已保存，但学习知识仍为 0；虚线框表示提前发生的原始接收 |
| 切换「本轮未产生已处理知识」 | 原始材料可以保留，不能编造一条知识或统一判定为成功 |
| 第 11 步后模拟 App 编辑为 v2 | 当前正文更新，官方历史 v1 保留；两侧显示不同正文 |
| 模拟重复接收 | 同一来源重放不增加原始记录、知识或版本数量 |
| 查看源码依据 | 查阅该阶段实际实现与行号，而不是只看演示文案 |

键盘左右方向键可前后移动，Esc 暂停；节点支持 Enter 和空格。减少动态效果的系统设置会停用运动效果，仍可逐步查看。

## 完整主链路

图按职责拆为三排。第 9–11 步是 Memlane 内部的读取、导入和索引职责，不能理解为绝对排在所有 Cognee 后台工作之后；一次 `SyncCognee` 可以在第 5–8 步期间先导入原始材料。

| 阶段 | 调用与产物 | 此时不能据此认定什么 |
| --- | --- | --- |
| 1. Codex 正常工作 | UserPromptSubmit 暂存用户提示；工具 hook 收集结果；Stop 接收回答。新提示也可查询已有记忆 | 提示保存不是完整会话已整理 |
| 2. 提交会话增量 | Stop 将提示与回答配成 QA；工具结果按 Trace 单独提交 `POST /api/v1/remember/entry` | 每次请求不是提交完整 rollout |
| 3. 会话层保存 | typed entry 调用 `add_qa` / `add_agent_trace_step`；返回 `session_stored` | 未证明 Session learning 已产生 |
| 4. 提交 improve | 按 session_ids 提交 `POST /api/v1/improve`，`run_in_background=true` | 已受理不是后台完成 |
| 5. 原始材料持久化 | `persist_session_qa`、`persist_agent_traces` 写官方材料、图谱并推进对应水位 | raw 持久化不是提炼完成；原始材料的实体关系也不是提炼知识 |
| 6. 提取上下文 | Cognee 读取待处理上下文，形成指导与候选；受模型、反馈与 session 门禁影响 | 上下文候选仍不等于知识文档 |
| 7. 提炼与筛选 | 加载、curator 归纳、查询既有知识、writer/rejecter 接受或拒绝 | 每个 session 不保证产生知识 |
| 8. 构建学习知识 | 接受的经验成为独立 `Session learning` 文档，经 add / cognify 建立官方图谱与向量 | 学习文档完成不等于 improve 全部阶段结束，也不等于 Memlane 已接收 |
| 9. Memlane 受信读取 | 校验服务实例、dataset、session、连接代次与范围，读取官方材料 | Memlane SyncCognee 不是插件触发 improve 的动作 |
| 10. 分层导入 | QA / Trace 导入 raw；只把 Processed=true 的学习文档导入 knowledge；绑定具体原文版本 | 原始记录不会因为已保存或已索引自动改名为知识 |
| 11. 当前版本索引 | 原文、版本和来源保存在业务库；Rust worker / E5 为检索建立索引 | 保存成功不等于已经可检索；App 编辑不自动回写官方历史图谱 |
| 12. 新会话消费 | Memlane `memory_search` / `memory_get` 读当前正文和范围；官方插件继续查询会话／图谱历史 | 官方历史 v1 不是用户在 App 编辑后的 v2 |

### 什么时候触发提炼

- 工具调用与 Stop 保存之后，插件更新自动计数。固定版本默认阈值是 **150 个计数事件**，还受冷却与失败退避影响。这不是 150 轮聊天。
- SessionEnd 或官方手动同步也会触发，并补送待发条目。
- 所以提炼可以在会话进行中触发，也可以在结束或手动同步时触发。不能统一说成「每次保存都整理完」或「只有退出才整理」。

Memlane `SyncSession` 通过官方 `sync-session-to-graph.py --strict` 提交结束同步，随后才按授权接收材料。后台接收检查不会反复提交新的 improve。

### 谁在调用提炼模型

采集原始会话的 Codex，与 Cognee 提炼阶段调用的 LLM，是两个角色。Cognee 根据记忆处理配置调用模型；不能从「Codex 已有一个正常回答」推导「Cognee 的提炼模型也已经处理完」。

本仓库已验证 pi 模型桥接的技术链路，质量与其他 provider 的限制见 [pi 模型桥验证](../pi-model-bridge-validation.md)。Codex app-server 模型桥与 Magpie 等能力应按各自的工程证据判断，不能从本图推导已接通。

### improve 还有哪些阶段

固定 Cognee 的阶段顺序为：

1. feedback_weights
2. persist_session_qa
3. persist_agent_traces
4. extract_agent_context
5. distill_sessions
6. update_user_preferences
7. build_truth_subspace
8. review_conflicts
9. global_context_index

这些阶段有各自门禁，并非每次都运行。主图展开会话到学习知识的关键部分；学习文档完成后，其余后台阶段仍可能继续。

## 数据实际在哪一层

下表是源码定义的目录结构。根目录由实际配置决定，本次没有读取用户运行配置来猜当前数据库的绝对位置。

设 `A` 是 App 数据根，正式配置中的 Memlane 业务服务根 `M = A/data`，受管 Cognee 根 `C = A/official/cognee`。插件的 `HOME` 是启动该 Agent 时配置的 HOME，App 托管 Codex 使用隔离 HOME，因此不一定是用户的 `~`。

| 层 | 位置 | 保存什么 | 主要消费者 |
| --- | --- | --- | --- |
| Codex 自身会话原文 | 由 Codex rollout 与 App 托管会话身份定位 | 完整原生事件，不是插件 QA/Trace DTO | 完整原文读取／来源追溯 |
| 插件待配对提示 | `HOME/.cognee-plugin/codex/pending/` | 尚待 Stop 配成 QA 的提示 | 官方插件 |
| 插件未送达暂存 | `HOME/.cognee-plugin/codex/bridge/` | pending_entries 与发送状态 | 恢复时补送；不确定是否已收到时先核对 |
| 插件 improve 状态 | `HOME/.cognee-plugin/codex/improve-state/` | 触发、冷却和失败退避状态 | 官方插件 |
| Cognee session 层 | `C/system/databases/cache.db` | QA、Trace、上下文、持久化与提炼水位 | SessionManager、improve、session 召回 |
| 官方文件 | `C/data/` | add 接收的原始文件和独立学习文档，具体文件位置以 Data 元数据为准 | Cognee 管线、来源读取 |
| 官方关系数据库 | `C/system/databases/cognee_db` | dataset、Data、管线状态、来源等元数据 | Cognee 和受信材料读取 |
| 官方图谱 | `C/system/databases/<dataset>.lbug` | 原始材料与学习知识的节点、实体、关系 | 官方图谱召回／浏览 |
| 官方向量 | `C/system/databases/<dataset>.lance.db` | 官方 cognify 的向量索引 | 官方召回 |
| Memlane 业务库 | `M/memlane.sqlite` | raw、knowledge、不可变版本、当前版本、来源、索引任务 | App 与当前知识 MCP |
| Memlane worker 索引 | `M/index/<dataset>/<generation>/` | Rust worker 生成的 LanceDB 检索数据 | Memlane 搜索；结果仍回业务库取当前正文 |

路径依据：`engine/python/cognee_service.py:20`、`internal/desktop/runtime.go:179`、`internal/daemon/server.go:98`、`engine/rust/backend.rs:100` 和官方插件 `_plugin_common.py:43`。图谱／向量目录结构也有既有实机验证，本图不代表本机当前正在使用某个 dataset。

**怎么消费数据：** 日常 Agent 通过 MCP 搜索并读取正文；App 展示知识、原始记录、版本和来源。数据库与向量索引是这些服务的持久层，不需要用户手动拼接它们。当前知识读取以 Memlane 为准，官方历史材料用于独立召回和追溯。

## 空结果、失败与版本边界

「知识为 0」需要区分无新经验、候选被拒绝、阶段跳过、处理尚未完成与模型／索引失败。它不等于数据丢失，也不能统一视为正常完成。

当前 App 的有限观察逻辑最多读取 30 次，间隔 10 秒，外层超时 5 分钟。提前结束等待的条件为：

```go
receipt.OfficialState == "persisted" &&
receipt.PendingDocuments == 0 &&
len(receipt.Knowledge) > 0
```

这是当前实现的等待判据，不是所有合法会话必须产生知识的规则。没有新知识的会话仍会有限观察，应结合阶段状态解释结果。轮询期间已经接收的 raw 可以留在 Memlane，稍后接收已处理的 knowledge；相同来源标识重放按导入契约去重。

用户在 App 编辑知识产生新版本。新会话的 Memlane MCP 读当前版本；官方 Cognee 历史图谱保持原内容。本链路没有自动回写 App 编辑。直接手动保存明确知识是另一条路径，不要求先生成一份 session 再提炼。

## 文件和验证

- [index.html](index.html)：自包含离线动态页面，SVG、脚本与步骤事实全部内嵌。
- [flow.svg](flow.svg) / [flow.png](flow.png)：静态完整图；PNG 为 2400 × 1774。
- [facts.json](facts.json)：步骤与源码引用；[flow.json](flow.json)：图形定义。
- [template.html](template.html) / [build.py](build.py)：页面模板与生成入口。
- [verification.json](verification.json)：Chrome 实际播放、场景、版本与重放交互核验。

图形通过 SVG 结构、箭头、碰撞与布局检查；Chrome 动态页面和导出 PNG 均实际看图。此验证仅覆盖流程说明的交互与显示，不替代 Cognee / Memlane 的运行链路验收。

需要重新生成时，在仓库根依次执行：

```sh
python3 docs/cognee-codex-flow/build.py
python3 /path/to/fireworks-tech-graph/scripts/fireworks.py render flowchart docs/cognee-codex-flow/flow.json docs/cognee-codex-flow/flow.svg --report docs/cognee-codex-flow/layout.json
python3 docs/cognee-codex-flow/build.py
python3 /path/to/fireworks-tech-graph/scripts/fireworks.py check docs/cognee-codex-flow/flow.svg
python3 /path/to/fireworks-tech-graph/scripts/fireworks.py export-png docs/cognee-codex-flow/flow.svg docs/cognee-codex-flow/flow.png --width 2400
```

SVG 生成器使用另行安装的 `fireworks-tech-graph`，命令中的路径应替换为当前机器实际安装位置。打开 HTML 本身无需生成器、服务器或联网。
