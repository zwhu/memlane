# 精简版旧链清理清单

2026-11-13。本文保留切换前盘点与随后实际清理结果；各阶段状态见对应章节，原盘点不覆盖后续已完成事实。源码基线 `pre-simplification-20261113-000958`，commit `559eab851ec306d44ed2549e4ad575b035b829b6`。不改写历史失败事实和次数。

## 已核对的两个旧根

- R：`/var/folders/nv/g6m5_3nx1r9_v9dk3zh_5zw40000gn/T/memlane-task-gnwqmjhd/initialization/client-responsive`，实际原日常运行根。官方数据位于 R/official/cognee，必须保留。
- V：`/Users/bytedance/work/AI/memlane/.local/dev/client`，当前日常 Codex 与 pi 配置实际连接的旧开发内容根。不能因 R 是原日常根而忽略 V 的采集写者。

只读取配置、进程、文件元数据及数据库结构／项目映射；没有读取会话、知识、outbox 或采集事件正文。

## 切换前必须退出的消费者

| 消费者 | 已观察事实 | 后续动作 |
| --- | --- | --- |
| R 旧 daemon | PID76077；launchctl `gui/501/io.github.zwhu.memlane.concentrated20261111.twelve.service`，由 smd 提交；真实二进制 `build/goal-native-runtime015-20261111/MemlaneConcentratedTwelve.app/Contents/Resources/bin/memlaned` | 唯一运行执行者精确停止该 service，核对 PID、组及旧写者退出；不修复 helper |
| R 旧 Cognee | PID76867、子77407/77408/77409，监听127.0.0.1:61255；读取 R/official/cognee | 单独停止并核对子进程；官方一致快照后由新入口接管同一位置 |
| V Codex MCP | 观察到PID18928、34638、76955；命令 `.local/dev/memlane mcp --root V/data --connection 65193290-9ec6-4789-8cc5-2c5dc4721819 --generation 1` | 移除对应自有 MCP 配置，宿主重载后核对实际进程退出；PID为观察值，操作前必须复核 |
| V Codex capture | `~/.codex/hooks.json` 的 Stop、UserPromptSubmit 各有一条 `.local/dev/memlane capture-codex --client-root V --connection 65193290-9ec6-4789-8cc5-2c5dc4721819` | 仅删除这两条 Memlane hook；保护同事件中的 Flux、Electron、node 等第三方 hook |
| V pi extension | `~/.pi/agent/settings.json` extensions 含 `V/agent-adapters/b6d0cd86-4e9a-4f19-a4c5-3339d7028aea/memlane.ts` | 移除这个精确 extension 引用，保留其他设置和扩展；不扩大为全 Agent 新接入 |

`~/.codex/config.toml` 的 `[mcp_servers.memlane]` command 指向 `.local/dev/memlane`，存在子表 `[mcp_servers.memlane.tools.memory_save]`。移除这套自有服务及其子表；不删除项目 trust 配置、账号、模型选择或其他 MCP。当前没有在 `~/.codex/plugins/cache` 找到 Cognee 官方插件 manifest；不能将旧 MCP 或已下载的固定插件源码描述为官方插件已生效。

## 完成切换后可精确退出的旧内容对象

以下每项须先确认全部实际消费者退出，再执行；不递归清空 R、V、build 或所有数据库。

| 精确相对对象 | 根 | 职责与处理 |
| --- | --- | --- |
| `data/memlane.sqlite`、`data/memlane.sqlite-wal`、`data/memlane.sqlite-shm` | R、V（存在时） | 旧正文、全文版本、任务、权限、双库绑定和 outbox 状态在该自有库；少量必要配置已另行提取后退出整套旧库，不迁移正文 |
| `data/index` | R、V | Rust 自建原文索引，退出消费者后删除；不触及 official/cognee 下官方图和向量 |
| `data/agent-outbox` | R、V | 旧采集持久队列；本次盘点分别2文件5394字节、2文件136381字节；停止采集后直接退出，不转投官方 |
| `client-drafts` | R、V | 旧手动编辑草稿；分别4文件1619字节、1空文件，功能取消后退出 |
| `project-preference-intents` | R | 旧跨项目偏好决策意图；1空文件，功能取消 |
| `data/backups` | R | 旧 Memlane 自有库备份，12文件约6.4MiB；先从既有备份元数据核实未混入应保留官方数据，再列具体文件清理，不能仅凭目录名删除 |
| `data/cognee-auth` | R | 旧自有协议凭据，2文件128字节；消费者退出后删除，区别于官方账号凭据 |
| `agent-adapters/65193290-9ec6-4789-8cc5-2c5dc4721819` | V | 自有 Codex capture安装、事件、状态；只退出此安装，Agent自身聊天保留 |
| `agent-adapters/b6d0cd86-4e9a-4f19-a4c5-3339d7028aea` | V | 自有 pi扩展及安装记录，精确解除 settings 引用后退出 |
| `agent-adapters/1339f6af-3a05-467c-98df-73e6d46315b2`、`agent-adapters/ec8acb80-0b3a-4c8a-ad2c-1f314275604f`、`agent-adapters/5965c3e8-1675-44ed-a8b9-236418a7e039` | R | 旧安装记录及扩展；先逐installation.json核对 config_path、宿主引用，尚未核对完，不直接删除 |
| `.local/dev/memlane`、`.local/dev/memlaned`（若存在）及旧service/runtime配置 | 仓库 | 新启动器不再消费。须先解除上述真实MCP/hook消费者，再删除二进制；不删除共享Go/Python缓存与组件 |

旧 lock、service-state、browsing、first-launch、agent-connection-intent 等不再作为新入口配置；处理时逐项核对消费者，而非删除整个父目录。旧数据库中项目记录不自动成为新项目：V中仅发现item-fe映射，R的两个旧绑定均为初始化合成项目，没有真实memlane仓的现成dataset关联。

## 明确保留

- R/official/cognee 全部官方存储及必要官方凭据、既有官方数据；新入口继续同一位置。
- R/official/runtime/components/cognee-bge-zh/1.5：当前 embedding `HF_HOME` 依赖，不可作为旧索引删除。
- 仍被引用的完整 App、Cognee 运行组件、Rust worker、模型及其完整资源。新GUI不是完整运行时替代；实际切换并确认依赖解除后再列资源清单。
- `~/.codex` 的账号、会话、历史、原生数据库、第三方插件与配置；只移除精确归属 Memlane 的 MCP/hook。
- `~/.pi/agent` 的账号、会话、其他扩展和设置。
- Git源码tag、历史缺陷和停止问题证据，及所有无关修改。

以上为切换前盘点。随后实际结果见下文及统一执行记录，不能仅凭候选清单认定D06/D09完成。

## 2026-11-13 剩余旧备份精确删除

旧运行所有者已由主执行者停止，官方一致快照及同库接管由主执行者记录。本轮只处理 R/data/backups 与三个 R/agent-adapters 安装项，不启停进程、不改宿主配置。

R/data/backups 在删除前实际只有以下12个文件：8份SQLite、4份备份／恢复元数据。只以 `mode=ro&immutable=1` 读取 SQLite 表结构，全部含旧 Memlane 的 `memories`、`versions`、`processing_current`、`scope_history`、`connections` 等自有表；没有读取知识／聊天行。手动备份的 SHA256 与其 receipt 相符。目录不含官方图、向量或其他官方文件，未将旧内容复制归档。逐个明确文件 unlink，随后仅移除空目录，没有递归清空R或所有数据库。

| R/data/backups 下已删除文件 | 字节 |
| --- | ---: |
| `manual/22ff6d5f-745e-4c31-9f1a-506c85267b82/receipt.json` | 249 |
| `manual/22ff6d5f-745e-4c31-9f1a-506c85267b82/snapshot.sqlite` | 749568 |
| `manual/f5fa3c65-dfff-4f02-b9e4-a1a369031d57/receipt.json` | 248 |
| `manual/f5fa3c65-dfff-4f02-b9e4-a1a369031d57/snapshot.sqlite` | 937984 |
| `restores/82761887-44ab-496d-adae-21a1df089461/journal.json` | 743 |
| `restores/82761887-44ab-496d-adae-21a1df089461/prepared.sqlite` | 937984 |
| `restores/82761887-44ab-496d-adae-21a1df089461/previous-memlane.sqlite` | 937984 |
| `restores/82761887-44ab-496d-adae-21a1df089461/receipt.json` | 491 |
| `schema-v24-20261110T165105.181206000.sqlite` | 720896 |
| `schema-v25-20261110T171203.499159000.sqlite` | 737280 |
| `schema-v26-20261110T222456.054677000.sqlite` | 774144 |
| `schema-v30-20261111T025240.201247000.sqlite` | 913408 |

实际删除合计 6710979 字节；R/data/backups 已不存在，R/official/cognee 仍存在，未操作该官方根及其embedding组件。

## R 的旧适配器仍有宿主引用，暂保留

| R/agent-adapters 安装 | 实际宿主引用 | 当前处理 |
| --- | --- | --- |
| `5965c3e8-1675-44ed-a8b9-236418a7e039` | `initialization/codex-responsive/config.toml` 仍含该connection标识和旧Memlane MCP配置 | 保留安装；已报告主执行者，等待精确解除引用 |
| `1339f6af-3a05-467c-98df-73e6d46315b2` | `initialization/pi-responsive/settings.json` extensions 仍含该 `memlane.ts` 绝对路径 | 保留安装；已报告主执行者，等待精确解除引用 |
| `ec8acb80-0b3a-4c8a-ad2c-1f314275604f` | 同一 pi settings 的 extensions 仍含该 `memlane.ts` 绝对路径 | 保留安装；已报告主执行者，等待精确解除引用 |

本轮进程表未观察到这些安装路径／标识的匹配活动PID；这不消除宿主配置引用，不能据此删除。没有修改这些隔离宿主的任何配置、第三方扩展、账号或聊天；也没有恢复旧采集。配置解除后才能由唯一执行者结清这些安装项，不能把本轮清单写成全部旧链已退出。

## R 隔离宿主引用已精确解除，三适配器已退出

主执行者随后明确授权解除上述三个R安装在专属初始化宿主中的引用。修改前将两个必要配置原字节以0600保存于 `.local/daily/prior-config/r-codex-responsive-config-before-retirement.toml` 与 `r-pi-responsive-settings-before-retirement.json`，记录原／新SHA256于同目录 `r-initialization-retirement-receipt.json`；没有复制内容数据库、聊天或其他宿主文件。

- Codex只删除 `[mcp_servers.memlane]`、`[mcp_servers.memlane.tools.memory_save]` 及该安装的begin注释标记，事先核对command/args的root和connection确属 `5965c3e8-1675-44ed-a8b9-236418a7e039`；TOML解析后其余键值与原配置相同。`codex-responsive/hooks.json` 不存在，没有修改任何hooks或第三方设置。
- pi只删除 extensions 中精确指向 `1339f6af-3a05-467c-98df-73e6d46315b2/memlane.ts` 和 `ec8acb80-0b3a-4c8a-ad2c-1f314275604f/memlane.ts` 的两个绝对路径，保留其他键值；两份扩展源码SHA256均与installation记录一致。
- 重新核对宿主配置中三个安装标识均已消失，随后逐文件删除三份installation.json和两份memlane.ts，再移除三个空安装目录。没有删除整个initialization或宿主目录，没有启停进程。

执行留痕：第一次安全检查在配置表已解除后因剩余自有begin注释中仍有connection标识而停止，尚未删除adapter；仅删除该精确自有注释后重新核对解析后的无关设置与原配置一致，再完成删除。这是清理检查条件的准备问题，没有重启业务或运行产品验收。

此前“暂保留”是引用解除前的已证状态；本段记录随后完成的实际变更，不抹除原观察。R/official/cognee、embedding组件、Agent账号／聊天及无关设置均保留。旧配置备份仅为本次配置回退证据，不挂载为活动接入。

最终核对：R/data/backups及上述三安装目录已不存在；R/official/cognee、R/official/runtime/components/cognee-bge-zh/1.5均仍在。R/agent-adapters还留有 `4ea36c76-a360-47cf-a242-22e475314191`、`0aa8625f-6b1f-4a7d-a421-bd583cbc4553` 两个空目录，无文件与运行实现，不在本次三个installation清单中，未扩大删除。


## 实际首条链路的认证配置缺陷（第1轮）

真实 Codex thread `01a1d091-2be2-7a33-8a74-b2616a07b10b` 已加载固定官方插件，产生官方 session map 和 idle-watcher；插件 hook.log 记录 SessionStart、UserPromptSubmit、Stop／SessionEnd 自己运行。因此不是“安装未生效”或“hook未信任”。该轮 `dataset_ensure_before_register_failed`、`agent_register_failed`、`coding_capture_failed`、`coding_delivery_failed` 均返回 HTTP401；16:24:07–16:24:10 UTC 的官方最终 sync／improve 同样401，记录 `sync_failed`、`sync_retry_scheduled`。这轮尚未证明材料已保存。

已证原因：先前将 owner 登录 JWT 放进 `COGNEE_API_KEY`；固定官方插件发送 `X-Api-Key`，JWT应走Bearer，不能当成API key使用。之前“配置值与token相同”的检查不足以证明传输兼容。

修正：使用既有 owner Bearer 身份调用固定官方 `POST /api/v1/auth/api-keys` 签发同身份 API key，私有保存 `.local/daily/official-plugin-key.json`，将 `~/.cognee/.env` 的唯一 `COGNEE_API_KEY` 替换为该key。随后按插件实际的 `X-Api-Key` 传输只读调用官方datasets成功，能看到真实memlane dataset。没有关闭官方认证、改业务函数、手动补写材料或模拟hook。修正只证明接线认证可用，不能把原轮标为采集通过。

已运行的官方 watcher／同步重试可能保留此前进程环境中的旧key；不能仅修改.env便宣称原进程已重新加载。保留它的真实状态及官方重试事实，由运行执行者核对，不盲目重发同一模型请求。该具体认证配置问题累计第1轮失败；其他旧问题次数保持。


## D02 已观察范围

认证配置修正后，第二个正常编码任务 thread `01a1d093-9db5-7c01-86bf-5d6e89a33203` 的官方会话 `codex_01a1d093-9db5-7c01-86bf-5d6e89a33203` 已实际保存1条QA和15条trace。官方捕获日志时间为2026-11-12 16:25:58 UTC。用同owner新鲜Bearer及插件实际X-Api-Key分别GET精确会话均200，owner、dataset、session相同；小型无正文回执在 `.local/daily/d02-capture-receipt.json`。原先列表0采样在捕获之前，后续同官方列表total=1，不构成另一个缺陷。本项证明正常Codex材料经官方插件进入官方存储，尚不证明D03知识生成或D04后续采用。

## D03 首次官方处理结果（未通过，第1轮）

同一正常会话的官方 shutdown worker 于16:26:00 UTC claim final sync，drain 16项且remaining0；16:26:02 `improve_fired`、`sync_bridge_done`接受成功。固定插件不轮询处理，不能据该日志判断知识生成。

随后只读官方 `/api/v1/activity/pipeline-runs` 显示：operation `fa37b278-9a53-4565-8996-430d1d4fc627`，operation_name=improve、background=true、outcome=failed、error_class=CogneeSystemError，起止16:26:02.276159–16:26:34.293900 UTC，session_ids明确绑定第二正常会话。关联 memify_pipeline run `9f33b909-5ff1-4f31-a26f-e44fbeb3fe47` 状态 DATASET_PROCESSING_ERRORED；add_pipeline `a21ebb77-5463-4bae-a774-cf9135aebc44` 已完成。该dataset processing-status为total1/completed0/pending1。

无正文元数据回执 `.local/daily/d03-first-official-processing-metadata.json`。具体根因待当前服务日志核对；operation中的tokens计数不足以证明没有模型调用。不重新触发improve或补写材料，保留原操作及部分官方数据。该处理问题第1轮，与前述认证配置问题分别追踪。


## 旧链退出最终只读复核（2026-11-13T01:47:35+08:00）

- `ps` 未发现旧自有 `memlaned`、Memlane MCP `mcp --root` 或 `capture-codex` 进程。旧 `io.github.zwhu.memlane.concentrated20261111.twelve.service` 已不在 launchctl list。这里只报告本次观察，不以旧PID记忆代替当前状态。
- `~/.codex/config.toml` 无旧 `[mcp_servers.memlane]`、`.local/dev/memlane`、durable-delivery 引用；`~/.codex/hooks.json` 无旧capture命令；pi settings无已退出的Memlane扩展引用。第三方配置未在此轮修改。
- R与V的旧 `data/memlane.sqlite`（含wal/shm）、`data/index`、`data/agent-outbox`、`data/cognee-auth`、`data/backups`、`client-drafts`、`project-preference-intents` 均已不存在。V/agent-adapters为空；R/agent-adapters仅剩两个空目录 `0aa8625f-6b1f-4a7d-a421-bd583cbc4553`、`4ea36c76-a360-47cf-a242-22e475314191`，无可执行扩展、采集记录或安装文件，不构成活动兼容链；本轮不删。
- 明确保留的官方根 R/official/cognee、官方固定source/archive、Python环境、native Codex binary及BGE组件HF_HOME均实际存在；既有 `.local/cognee-python/bin/enola` 也仍存在。
- 当前新启动器PID40911拥有completion PID40916、官方服务PID40965，两个命令均引用当前`.local/daily`配置／凭据路径。官方服务与原数据根关系已由私有runtime配置只读核对。本轮未启停、写入内容或删除资源。
- 上述仅收口旧内容／旧接入退出和保留依赖事实，不代表当前第二轮真实代码索引已完成，不更改任何历史失败次数。
