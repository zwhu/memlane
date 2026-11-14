# 本地开发

2026-11-14多Agent／Provider增补已进入dev实施，覆盖下文旧“唯一Codex生成通道”的相应说明。当前支持、真实证据与未完成项见[支持矩阵](engineering/multi-agent-provider-support-20261114.md)。现有日常runtime／插件不自动迁移，仍以各自交付记录为准。

2026-11-13 PA项目权限增补已完成dev自验，范围及实际日常接入见[项目权限交付](engineering/project-access-delivery-20261113.md)，详细证据见[执行记录](engineering/project-access-execution-20261113.md)。原D01–D09的共用owner和只读通用知识规则是此前交付事实；当前受管PA使用独立Agent主体、官方dataset ACL与本机项目控制进程。经用户明确交接已切换本机Codex，新CLI与普通Desktop问答均已验证；既有会话及材料未自动迁移、补采或重放。

PA配置模板新增可选 `project_control`：显式loopback `url`、私有 `registry_file`、独立 `management_token_file`、安装实例 `agent_control_token_files` 以及固定 `plugin_dir`。启用后启动器一并管理控制进程，关窗保持，明确stop才收尾。未配置时保留原启动流程；模板路径仍是占位符。受管插件在独立目录重放 `engine/patches/project_access.py`，启动器核对 `engine/managed-project-plugin.json` 的全部摘要；禁止手改日常插件缓存。项目权限源码、真实隔离Agent及Native验证边界见 `docs/engineering/project-access-execution-20261113.md`；本机Codex准备、启用及实际加载分别记录，不用准备成功代替日常接入。

2026-11-13 起，精简版唯一开发入口为 `scripts/dev.py`。本轮接管与验收已完成，实际范围和限制见[交付记录](engineering/client-simplification-delivery-20261113.md)，阶段证据见[执行记录](engineering/client-simplification-execution-20261113.md)。配置模板为 [`config/daily-runtime.example.json`](../config/daily-runtime.example.json)，其中路径均为占位符，不能直接启动。

从仓库根目录执行，所有操作使用同一个私有配置文件的绝对路径。先在一个终端启动并保持前台：

```sh
python3 scripts/dev.py start --config /绝对路径/runtime.json
```

服务就绪后，在另一个终端按需查看状态、重开窗口或停止：

```sh
python3 scripts/dev.py status --config /绝对路径/runtime.json
python3 scripts/dev.py open --config /绝对路径/runtime.json
python3 scripts/dev.py stop --config /绝对路径/runtime.json
```

启动器在官方服务及当前模式所需端点健康检查通过后启动MyGo dev；桥模式启动completion，Provider模式不启动它。健康检查就绪不代表真实模型推理已验证。先只运行后台时，使用 `python3 scripts/dev.py start --no-ui --config /绝对路径/runtime.json`，随后仍可用 `open` 打开窗口。

关闭桌面窗口或 MyGo 源码重载不会停止这两个端点，也不要再次执行 `start` 来重开窗口。`open` 向现有启动器发送重开请求，停止其已有 MyGo watcher（若有），再启动一个 watcher；它不会启动第二套服务，也不代表窗口已完成显示。UI 源码由 MyGo dev 增量编译、重载。

启动终端中的 Ctrl+C 等同明确停止；另一终端的 `stop` 会核对数据锁、启动器 PID、内核启动时间和配置路径，通知启动器收尾所属进程组及已跟踪的模型子进程。等待 `Owned service groups exited; daily store released.` 或前台启动器正常退出后再启动。服务源码、模型或运行配置变更时，先用原配置停止，再修改并执行 `start`；不要在运行中改掉用于识别所有者的配置路径或数据位置。电脑重启后手动启动，不注册 helper，不恢复业务任务。

`status` 检查启动器所有权并输出运行记录，不是模型推理或全部子进程健康检查。未启动、配置不匹配、端口占用、数据锁被占用或残留所有权记录都应先核实；不要删除锁／运行记录或直接杀掉未知进程来绕过检查。停止未确认时保留记录，不把失败当作已停止。

旧启动器仍拥有健康服务、但其 MyGo watcher 已退出时，可通过 `python3 scripts/dev.py open --watch-ui --config /绝对路径/runtime.json` 在当前终端附着一个前台 UI watcher。该入口先核对现有服务所有者和就绪状态，拒绝第二个活跃 watcher；退出此 watcher 只收尾它自己的界面进程组，不停止官方服务。普通 `open` 检测到该附着锁时不再通知旧启动器另开 watcher。此方式用于开发调试，不替代日常服务的 `start`／`stop`。

总览的首次初始化完成日期由启动器在必要配置、带认证官方内容检查及当前模式所需健康检查通过后保存。状态默认位于 `runtime.json` 同目录的 `client-state.json`；可用可选绝对路径字段 `client_state_file` 指定同一持久文件。启动、重开、模型或项目重配不会覆盖已有首日，单次运行清理不得删除该文件；损坏或写入失败保持错误，不用当前日期覆盖。实际知识与活动仍读取官方服务，不保存在此文件中。

实际配置使用已有官方日常数据和组件，路径填写绝对路径；不静默新建空库。省略 `--config` 时默认读取仓库 `.local/daily/runtime.json`，显式指定路径更便于保证启动、停止与重开使用同一配置。

| 模板字段 | 配置含义 |
| --- | --- |
| `official_data` | 已有官方日常存储根目录，必须含 `system/`、`data/`；规范路径持有单实例锁，跨源码目录或符号链接也不能启动第二个写者。 |
| `official_source`、`official_archive`、`service_commit` | 固定上游源码目录、原始归档与提交。提交须匹配 `engine/coding-memory-components.json`；启动时校验归档摘要及官方包源码，不使用修改后的上游副本。 |
| `python` | 已有 Python 3.12 虚拟环境的 Python 入口，保留 venv 路径，不替换成符号链接最终指向的基础 Python。复用已有依赖。 |
| `service_url` | 桌面与官方插件共同访问的官方 Cognee API origin。 |
| `completion_url` | Codex桥模式所需的本机completion origin，使用带明确端口的 `http://127.0.0.1:端口`，不含业务路径；服务侧追加 `/v1`。Provider模式可省略。 |
| `token_file` | 存放官方 Cognee 正式 API key 的私有纯文本文件路径，桌面读取并去除首尾空白后发送 `X-Api-Key`。 |
| `completion_token_file` | Codex桥模式所需的独立本机completion Bearer凭据文件；Provider模式可省略。 |
| `service_environment_file` | 私有 JSON 对象文件路径，提供官方服务已有的 embedding 配置／凭据、默认用户和认证 secret 等允许项；不是 shell `.env` 文件。启动脚本拒绝允许列表之外的键，LLM 端点与凭据由运行配置生成。 |
| `codex_executable`、`auth_home`、`model` | 原Codex桥配置及旧runtime首个profile的来源。原生二进制、登录home目录和模型分别配置，不能把auth文件填成home。已有Provider active配置时可省略；Agent的Codex安装发现另用 `agent_hosts.codex`。 |
| `generation_file` | 私有schema2生成配置文件的绝对路径，保存不可变修订、active／next及验证回执；凭据只存私有引用。省略时沿旧runtime同目录生成。已有配置不能复制模板内容覆盖。 |
| `agent_hosts`、`agent_host_home` | 各类型选定profile与原生executable；`agent_host_home`为安装器使用的已存在绝对HOME目录。Agent安装不依赖知识处理模型或对应订阅登录。 |
| `graph_assets` | 已准备的官方图谱 Web 静态产物目录，供桌面图谱页加载。 |
| `projects` | 项目关联数组，条目使用 `id`、`name`、`path`、官方 `dataset_id`，以及按需提供的 `code_dataset_id`、`code_enabled`。dataset ID 来自同一官方身份下的真实数据，不以项目显示名代替。 |
| `common_datasets` | 旧显式通用文档关联数组，条目结构不变。PA管理侧另有专用通用知识数据组与独立ACL；空数组不迁移已有内容。 |

`token_file`、`service_environment_file` 及桥模式的 `completion_token_file` 必须已存在且为私有文件（0600）；实际配置与凭据保存在 Git 之外的私有位置，不写进模板、命令行或日志。不要将真实 token、环境文件或登录文件内容贴进文档。

各条连接的凭据分别用于不同端点：

| 连接 | 凭据与发送方式 |
| --- | --- |
| 桌面 → 官方 Cognee | `token_file` 中由目标官方服务签发、属于日常 owner 的正式 API key；请求头为 `X-Api-Key`，无 Bearer 回退。 |
| 受管官方插件 → 官方 Cognee | 使用该Agent实例独立官方子主体的key，`COGNEE_BASE_URL`对齐 `service_url`；安装绑定定位私有环境，不复制桌面owner凭据。 |
| 官方 Cognee → 本机 completion | 服务读取 `completion_token_file` 并设置为 `LLM_API_KEY`，通过 `Authorization: Bearer …` 调用 `completion_url` 下的 `/v1` 接口；端点校验同一份凭据。它不是 Cognee API key。 |
| completion → Codex 订阅推理 | 从 `auth_home` 仅复制必要订阅登录状态到每次请求的临时隔离 Codex home；使用独立工作目录和配置，不复制日常插件、历史或项目上下文。它不是上述两种端点 token。 |

保持同一官方服务与数据归属，需要对齐服务、实例子主体及受管dataset。固定受管产物设置 `COGNEE_PLUGIN_IDENTITY=false` 和 `COGNEE_MANAGED_ENDPOINT=true`，不另建身份或启动fallback；项目解析和权限由管理侧既有PA协议及官方有效ACL决定。`dev.py`本身不代装插件或改写宿主配置；准备／启用经设置中的连接管理API执行。

已解决的接入问题是把 owner 登录 JWT 放进 `X-Api-Key` 导致官方 hook 返回 401；修复为同 owner 正式 API key，桌面也统一使用 `X-Api-Key`，详见[接管与认证记录](engineering/client-simplification-execution-20261113.md)。不要将登录 JWT、completion Bearer 凭据或 Codex 订阅登录混填为插件／桌面 API key。出现 401 时先核对目标端点和凭据类型，不通过新建身份、另起服务或空库来规避。

不启动旧memlaned、私有内容IPC、独立开发全文库或第二套索引。固定官方原始源码通过当前Codex桥或Provider配置推理，embedding复用已有组件。源码及轻量调试容器可隔离，日常内容只有一份官方存储。

## 多Agent与Provider配置（dev）

设置“服务与连接”按类型多选准备、显式启用，再由实际宿主完成信任／重载与新会话；安装摘要不代表采集通过。选择目标profile，不读取其历史会话，不把其他类型的权限覆盖复制过来。无工具回合的Cursor当轮注入限制及尚未验证的宿主见支持矩阵。

Cursor准备官方派生插件的不可变目录，显式启用时将其加载入口写入目标profile的`hooks.json`之`workspaceOpen`，通过官方`pluginPaths`返回目录。其他hook配置保持，旧代际目录不清理；重新打开工作区／重载窗口后才验证实际加载。检测到旧local Cognee插件或Claude重复导入时停止并保留原物，不自动迁移。当前仅管理器及配置切换验证通过，真实Cursor宿主仍待验。

OpenCode第二批开发使用`engine/agents/opencode/managed-control.ts`访问既有控制协议；新增`GET /v1/project/binding?host_session_key=...`仅按独立Agent控制token读取原映射，不发现／创建项目，供unknown解析结果核对。配套服务尚未正常加载新路由时保持未知，不重发解析或回退。当前控制客户端／HTTP／outbox局部检查已通过，实际OpenCode受管安装与回调未接通，不能按源码存在启用日常入口；进度见[第二批记录](engineering/multi-agent-second-batch-20261114.md)。

设置“知识处理”分别保存配置、验证（消耗所选账号额度）、选择“下次启动使用”；当前配置持续服务原任务。正常stop/start后，launcher确认实际配置摘要和健康才改active；候选失败仅在所属资源确认退出后恢复原配置一次，清理未知或原配置失败则停止。原操作缺失或unknown时只核对，不新建编号重发安装或模型请求。

Provider协议值为 `openai`、`anthropic`、`gemini`、`custom`；DeepSeek等明确OpenAI兼容端点选 `custom`。endpoint/model/key均由用户明确配置，不猜供应商或错误后换模型。凭据0600、目录0700，不回填Native；HTTP重定向拒绝。验证记录实际adapter请求模式，不能当作远端严格Schema保证。

模板 [`daily-runtime.example.json`](../config/daily-runtime.example.json)保留旧Codex桥兼容字段；[`provider-runtime.example.json`](../config/provider-runtime.example.json)展示不含Codex桥认证输入的API运行配置。两者都需已准备的官方环境／数据及私有凭据；本入口不创建或替换内容库。已有active时正常`start`。尚无生成配置时，先执行 `python3 scripts/dev.py setup --config /绝对路径/runtime.json`：只启动受管理token认证的本机配置服务，并复用Native“知识处理”表单，不启动官方引擎、接入Agent或写初始化完成标记。

首次配置依次保存Provider、点击会消耗额度的验证、选择“下次启动使用”；保存或验证不会制造active。完成后通过 `python3 scripts/dev.py stop --config /绝对路径/runtime.json` 停止配置服务，再正常 `start`。启动器只有在已验证候选的健康及完整摘要匹配后才写首个active；首个候选失败时清理并停止，不恢复不存在的旧配置。没有已验证next时不启动引擎。`setup`拒绝已存在active的账本；已有日常配置继续在正常设置页编辑。不得手写passed、复制验收账本或修改活动修订绕过验证。

开发阶段不运行完整打包、DMG、签名、公证、更新或 helper 安装。Go/MyGo 固定版本和仓库内工具链继续复用。磁盘门禁至少保留 100 GiB，`dev.py` 与其他检查均通过既有 `scripts/task.py`；Go 检查使用 `sh scripts/go.sh`。

首页A的字体需要与稿件的`-webkit-font-smoothing: antialiased`一致。MyGo0.7.4只在自绘字形公开此能力，当前以`scripts/patches/mygo-antialiased.json`固定两个SDK源文件及其SHA，补充主题内的可选`Antialiased`绘制标志；仅首页主题启用，其他页面默认绘制不变。`go.sh`及`dev.py`通过`scripts/mygo_sdk.py`准备一份约24 MiB的私有SDK及临时`-modfile`，路径在`.local/mygo-sdk/`，不会改模块缓存、仓库`go.mod/go.sum`或系统字体偏好。Go不允许用`-overlay`替换模块缓存中的文件，因此不采用该方式。

命令入口不变；SDK原始SHA、项目版本或已准备补丁副本不符会停止构建，升级MyGo时先评审这份补丁，不能静默覆盖。现有UI watcher在启用这项构建环境前需由其所有者退出，再用`open --watch-ui`附着，健康后台继续复用。私有SDK是当前watcher的编译依赖，运行期间保留；上游提供等价主题接口后应移除此补丁和准备步骤，不扩展为长期SDK分叉。

历史完整包、helper与旧索引验收命令已从当前开发说明退役。历史证据保留于对应文档及开发前tag；不得用旧脚本重启被删除链路。

代码图谱使用根目录 `mcp-arch.yaml` 的官方 Enola 配置：保留除 `manifests` 外全部默认源码解析器，并排除本项目生成目录。固定版的 `manifests` 扫描器绕过 ignore、会带入 `build/` 历史依赖清单，因此当前停用该扫描器；不提供它派生的依赖清单事实。语言源码的符号、调用与导入解析继续保留。更改索引配置后在项目图谱中手动更新，不启动文件监听。


## 本机 Codex 接入（PA 开发）

在设置的「Agent 连接」中准备本机Codex接入。控制进程按实际 `auth_home` 固定安装实例，为它创建／核对独立官方主体，再用已配置原生 `codex_executable` 的插件管理器，在私有临时Codex home内安装固定受管marketplace。准备不复制OAuth、不调用模型、不修改日常配置。

预览保存原配置摘要、拟启用配置摘要及待停用的Cognee入口；模型、登录和其他插件保留。共享同一Codex home的CLI／Desktop共用此主体，不提供session级权限隔离。已有项目配置若单独启用旧Cognee插件，需要先核对；旧采集watcher未交接时不允许启用。不要为了过门禁擅自结束编码会话或删除旧buffer。

明确启用时再核对配置摘要与缓存全部文件，先写未知journal，再将新代次缓存放入独立目录、原子保存配置。冲突、超时或中断先「核对原接入操作」，该入口只查原配置／原缓存，不重复安装或恢复owner回退。启用成功仍显示等待新会话，直至有实际接入回执；宿主的插件加载／信任提示需按其正常流程处理，不能全局关闭信任检查。CLI与Desktop的真实hook及cwd需分别验证。

控制进程复用官方Python环境中已有的 `tomlkit`，用于保留Codex配置中的注释及无关设置。本机环境已确认可用；不使用系统Python或修改用户默认工具链。接入预览、配置备份及安装日志含本机信息，保存在私有运行目录，不入Git。

### OpenCode 第二批插件组件（dev）

当前可离线构造独立插件组件，不是完整运行包；连接adapter尚未启用。使用固定上游lock预先填充的npm缓存，不读取已安装node_modules作为可信源码，不在宿主启动时联网安装。每次输出必须是新目录，保留CLI返回的manifest SHA用于prepare，不能仅信产物自报摘要。

```sh
python3 scripts/task.py -- .local/cognee-python/bin/python scripts/build_opencode_plugin.py --output /绝对路径/新组件目录 --npm-cache /绝对路径/固定npm缓存
```

安装检查入口：`python3 scripts/task.py -- env PYTHONPATH=engine/python MEMLANE_OPENCODE_TEST_NPM_CACHE=/绝对路径/固定npm缓存 .local/cognee-python/bin/python -m unittest test_opencode_installation`。缺缓存明确skip，不算完整通过。构造临时依赖自动清理，已保留组件的消费者与实际范围见[第二批账本](engineering/multi-agent-second-batch-20261114.md)。
