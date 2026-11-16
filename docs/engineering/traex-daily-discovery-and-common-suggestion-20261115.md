# TraeX 日常项目未发现与通用组误入关联建议

2026-11-15，只读诊断，不改宿主配置、权限或官方数据，不补采历史。保留其他会话的图谱及设计改动。

## TRAEX-DAILY-DISCOVERY01

用户在item-fe下使用TraeX后未见项目添加提示。本次用户反馈计1轮；此前隔离TraeX宿主通过不等于日常宿主接入通过。

已证事实：当前日常runtime的agent_hosts.traex.profile指向`.local/multi-agent-ollama-20261114/native-setup/traex-profile`，登记安装home也来自该验收目录；并非用户正常`~/.trae`。正常profile中的traecli.toml没有Cognee插件声明，也没有Cognee hook命令。管理registry内TraeX绑定数0，状态configured_pending_new_session；项目5个，没有item-fe。

反馈循环：只读比较runtime profile和正常profile的规范路径、检查正常配置中的Cognee插件／hook声明、检查registry的TraeX bindings及item-fe aliases。结果为路径不一致、声明／hook均0、bindings0、item-fe不存在。没有读取用户问答正文。

源码中TraeX事件经traex_hook → agent_memory_adapter.setup →项目resolve建立归属，resolve会ensure_locked登记项目；未配置插件的正常宿主无法通过这条链发现项目。当前自动登记与“发现项目后询问添加”的弹框不是同一功能；现有创建弹框的关联建议由用户主动打开后加载，未看到自动询问入口。

未知：用户刚才那次TraeX进程的实际profile启动参数、对应事件是否曾发送到其他旧接入。不以正常profile缺插件推断所有来源均未保存。未执行安装／重绑或让用户重跑对话。

## PROJECT-SUGGEST-COMMON01

用户创建项目弹框显示`ml-common-707208d4-afd2-5f1e-8c47-7c7e405f6f32`，并标“已有记录，尚未关联源文件夹”。本次反馈计1轮。

已证源码：linkable-datasets仅排除当前managed_groups、代码组及reserved_datasets，随后把其他owner数据组都作为候选返回；loadProjectSuggestions再把未登记的候选全部当作可关联dataset。当前registry通用组名称为`ml-common-2497a6a7-b25a-5cec-b3bc-ba23bfb65ecd`，与截图中的组不同。历史通用组名称未被语义排除，因而进入项目建议。

候选数据只有ID与名称，没有正文存在或记录条数核实，不能仅凭数据组存在写“已有记录”。截图中的项目关联建议与item-fe没有可信关联。

后续需保留历史数据，排除非项目用途组，并按实际已知事实表述候选。当前仅记录诊断，尚未修改筛选或声明修复通过。
