# Cognee Codex 公开配置入口核对

2026-11-17，只读官方源码审查。未安装、未运行模型、未修改接入／凭证／库。通过 `git ls-remote` 锁定当日 `main`：`0a1df922590d89e0d6765ddef7e1cb731fdac148`；以下链接均固定该提交。插件 manifest 仍标 `1.8.0`，因此版本号相同不能代替 commit 比较。

## 结论

**CLI 有不修改 `_env_file.py` 的配置路径；普通 Desktop 安装尚无已证的“每安装实例配置”路径。** 官方配置加载器支持进程环境中的 `COGNEE_ENV_FILE` 指向私有文件，也支持默认 `~/.cognee/.env`。后者由 Codex／Claude 共用，不能直接承载两套不同的 Agent 凭证。官方插件 manifest 声明 skills 和 hooks，但未声明可传到二者运行环境的每插件配置区。

## 精确入口

| 要求 | 官方入口与行为 | 范围／限制 |
| --- | --- | --- |
| 私有配置文件 | `_env_file.env_file_path()`读取进程环境`COGNEE_ENV_FILE`，否则`~/.cognee/.env`；文件值以setdefault注入，进程exports优先 | 不按plugin root或安装路径选文件；未见`.memlane-installation.json`一类原生入口 |
| 服务及凭证 | `COGNEE_BASE_URL`、`COGNEE_API_KEY`；`COGNEE_BACKEND=cloud`强制HTTP服务模式，`COGNEE_MANAGED_ENDPOINT=true`避免外管loopback服务离线时自启替代服务 | 在此模式里cloud表示HTTP目标，可以是本机官方服务，不要求Cognee Cloud |
| 独立已配置主体 | 将当前子主体key作为`COGNEE_API_KEY`，同时`COGNEE_PLUGIN_IDENTITY=false`忽略插件自动主体、`COGNEE_SHARED_AGENT_MEMORY=false`关闭共享角色接入 | 继续使用服务端现有ACL；不是让插件拿owner再自动provision |
| 默认写入项目 | `COGNEE_PLUGIN_DATASET`；`config.get_dataset()`优先当前launch记录，其次配置/default；UUID可作为datasetId写入 | 更改env不必然覆盖已建立launch；固定dataset不等于按cwd自动绑定Memlane项目 |
| 读取范围 | `COGNEE_PLUGIN_RECALL_MODE=pinned`；或`COGNEE_PLUGIN_READ_DATASET_IDS`明确UUID读集合 | 后者优先；只设置写入dataset不会自动限制所有召回 |
| 当前会话切换 | `scripts/switch-dataset.py`及`cognee-switch-datasets` skill，更新launch记录 | 切换有官方写权限核对及同步行为；不能为了配置归属而静默调用，需核对是否触发处理 |
| skill及hooks共用配置 | shell remember/search先load_env_file，再shell_runtime_overrides；hooks通过官方hook_runner启动脚本 | 必须保证两个执行入口看到同一份有效配置；只给hook进程export不能证明skill也得到 |

源码：[加载器93–98、148–172行](https://github.com/topoteretes/cognee-integrations/blob/0a1df922590d89e0d6765ddef7e1cb731fdac148/integrations/codex/plugins/cognee/scripts/_env_file.py#L93)、[config与get_dataset](https://github.com/topoteretes/cognee-integrations/blob/0a1df922590d89e0d6765ddef7e1cb731fdac148/integrations/codex/plugins/cognee/scripts/config.py#L227)、[配置契约](https://github.com/topoteretes/cognee-integrations/blob/0a1df922590d89e0d6765ddef7e1cb731fdac148/integrations/CONFIGURATION.md)、[remember入口](https://github.com/topoteretes/cognee-integrations/blob/0a1df922590d89e0d6765ddef7e1cb731fdac148/integrations/codex/plugins/cognee/scripts/cognee-remember.sh#L45)、[search入口](https://github.com/topoteretes/cognee-integrations/blob/0a1df922590d89e0d6765ddef7e1cb731fdac148/integrations/codex/plugins/cognee/scripts/cognee-search.sh#L62)、[身份及凭证策略](https://github.com/topoteretes/cognee-integrations/blob/0a1df922590d89e0d6765ddef7e1cb731fdac148/integrations/codex/README.md#auth)、[manifest](https://github.com/topoteretes/cognee-integrations/blob/0a1df922590d89e0d6765ddef7e1cb731fdac148/integrations/codex/plugins/cognee/.codex-plugin/plugin.json)。

## CLI 与 Desktop 区别

- **CLI可行候选**：在创建Codex进程前指定`COGNEE_ENV_FILE=/绝对路径/该Agent.env`。在宿主保留并传递该变量的条件下，hooks与skill子进程均可调用未修改官方加载器；仍需真实Codex进程验收。不能把此前函数／协议探针当作宿主已经通过。
- **Desktop共享默认配置**：文件`~/.cognee/.env`不依赖终端export，普通GUI子进程也可以按默认位置读取。但它和Claude共用；若写入某个Agent的专属key，将影响其他使用默认加载器的插件，不满足“各Agent独立凭证”的既有要求。
- **Desktop私有配置未证**：从图标启动不应假定继承某个终端的export。所查Cognee manifest／配置契约未提供`plugins.<id>.env`或`settings.env_file`入口。是否Codex宿主另有公开的每插件／每会话环境传递能力，应另查宿主契约并实际验证，不能据Cognee源码断言宿主绝对不支持。
- **仅包装hooks不完整**：把`COGNEE_ENV_FILE`前置到hook命令，只作用于hook及其后代；用户经skill执行的shell工具是另一个进程分支。除非skill也显式经过同一外层入口，否则仍可能读取全局默认配置。不能把双入口只修一半称为完成。

## 实施影响

可以先证明CLI进程级私有env传递，再精简该路径的配置补丁。Desktop必须先确定官方宿主提供的双入口配置传递方式；没有证据前保留当前配置绑定，避免影响现有主体及权限。若必须依赖安装目录旁标记文件，当前官方加载器没有这项能力，应列为真实缺口，而不是声称它只是配置管理。**本核对不承诺零补丁，也未授权改全局共享配置或用owner凭证替代。**

## 补充：Codex shell policy＋外部 hooks 的双入口候选

后续锁定官方 `openai/codex` main 为 `090be4df0724cdef5758e6a0934ba557af8b6bbe`。只读源码与官方文档，未修改用户config，未测试已安装Desktop版本。结论修正为：**Desktop存在有公开依据的无Cognee加载器补丁候选，不再仅限“由终端export启动”；但尚未经过本机双入口实测。**

### 已有官方依据

1. 官方开发设置说明：app中的Codex agents继承CLI／IDE相同配置，先进设置可编辑`config.toml`。官方shell配置允许`[shell_environment_policy.set]`添加变量，因此给shell工具设置`COGNEE_ENV_FILE`是公开入口，不必依赖Desktop继承终端export。[app配置说明](https://learn.chatgpt.com/docs/developer-settings#agent-configuration)、[shell环境策略](https://learn.chatgpt.com/docs/config-file/config-advanced#shell-environment-policy)。源码先应用set，后应用include_only；已有过滤规则可能仍去掉变量，实施须合并并验证有效配置，不能覆盖用户策略。[shell_environment.rs:160–169](https://github.com/openai/codex/blob/090be4df0724cdef5758e6a0934ba557af8b6bbe/codex-rs/protocol/src/shell_environment.rs#L160)。
2. **hook分支不使用该shell策略。** hooks注册时从Codex进程`std::env::vars_os()`抓环境快照，命令runner在其上叠hook环境；所查路径未调用shell policy。仅设置shell policy不能覆盖hook分支。[registry.rs:81–86](https://github.com/openai/codex/blob/090be4df0724cdef5758e6a0934ba557af8b6bbe/codex-rs/hooks/src/registry.rs#L81)、[command_runner.rs:488–499](https://github.com/openai/codex/blob/090be4df0724cdef5758e6a0934ba557af8b6bbe/codex-rs/hooks/src/engine/command_runner.rs#L488)。
3. 官方支持用户／项目`hooks.json`及内联hooks，command可调用外部命令，因此可以在用户注册的hook命令中前置`env COGNEE_ENV_FILE=/绝对路径/专属.env`，随后调用未修改的官方runner／脚本。命令在session cwd执行。[公开hook配置](https://learn.chatgpt.com/docs/hooks#config-shape)。
4. **插件hook不会被同名用户hook覆盖。** 官方明确所有来源匹配项一起运行且同事件并发，因此新增包装hook前必须先禁用原插件相应hook，不能依赖执行先后防重复。非managed hook可逐项禁用；用户态hook state有`enabled`，插件hook也应用该state，插件仍可保持启用供skills加载。[发现与禁用说明](https://learn.chatgpt.com/docs/hooks#where-codex-looks-for-hooks)、[HookStateToml](https://github.com/openai/codex/blob/090be4df0724cdef5758e6a0934ba557af8b6bbe/codex-rs/config/src/hook_config.rs#L29)、[插件hook状态应用](https://github.com/openai/codex/blob/090be4df0724cdef5758e6a0934ba557af8b6bbe/codex-rs/hooks/src/engine/discovery.rs#L811)。

### 候选形态（不是已写入的配置）

```toml
[shell_environment_policy.set]
COGNEE_ENV_FILE = "/绝对路径/Codex专属.env"
```

shell工具执行原版skill脚本时由官方loader读取该文件。hook侧另由用户hook配置中的command以前缀`env COGNEE_ENV_FILE=…`调用原版脚本。仅引用配置文件路径，不把API key直接放进Codex配置／命令。两条入口都用原版`_env_file.py`，不向Cognee安装目录注入配置选择逻辑。

### 尚需验证，不能直接切日常

- 当前Desktop所含运行时是否支持所查shell policy和hook state；配置何时重载，新会话／恢复会话是否一致。官方main和网页不能证明已安装版本。
- Desktop真实shell工具最终环境是否保留COGNEE_ENV_FILE，原版skill是否读到Codex子主体；需用脱敏回执证明，而非只打印路径。
- 列出并禁用所有原Cognee hook后，是否仅外部hook运行，其他插件hooks保持原样；新增hook仍须按宿主信任流程处理，不能自动伪造trusted_hash。
- 用户hook并非插件来源，通常没有PLUGIN_ROOT，外层须提供受控官方路径／所需插件环境。升级后重新解析实际安装目录及hook清单，不能继续引用旧缓存或遗漏新增hook导致双采集。
- 该候选只解决配置双入口；项目动态归属、采集恢复边界、自动处理策略及后台对内部函数的依赖仍是独立事项。也不适用于云编排不支持本地命令hooks的Work场景。

**建议：优先以这条公开候选做Desktop合成接入验证，再判断配置绑定补丁是否能删除；当前仅证据与候选成立，未验证可投用。**
