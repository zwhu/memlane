# Gemini App 接入执行记录

2026-11-19。用户明确要求将 Gemini App 接入当前同一日常库；仅实施本次 Agent 接入，不恢复历史全量 Goal，不进入 release。依据[调研报告](../research-cognee-gemini-app-20261119.md)、产品／设计及现有项目权限契约。

## 目标与连续验收

1. 发现实际 Gemini App 的 bundled agy，核对官方原生插件格式、固定归档和已有插件；准备不写宿主配置。
2. 创建独立官方 Agent 主体，继承当前默认与各项目权限，保持已有主体、数据和配置；明确激活仅本次插件。
3. 通过原生 plugin validate/install/list/enable 核对安装产物；当前服务不可用时不另建服务、库或安装依赖。
4. 新的 App 会话／任务触发官方输入、工具及完成回复 Hooks；旧会话不补采。官方问答经过现有编号缓冲保存到同库，核对真实项目与主体。
5. 同一 Memlane Native 入口列出 Gemini App 接入状态；安装／执行／实际保存分别报告。任务额度或登录前置不足时保留已安装成果与待验范围，不制造模型成功回执。

## 影响与所有权

本次拥有新增 antigravity_plugin.py、antigravity_hook_policy.py、antigravity_connection.py 及相关检查；在现有 agent_environments、agent_connections、project_installation、project_host_plugin、connection_capture 和 Agent 名称／图标入口添加该宿主。现有队列、独立主体和权限实现复用。保留其他未提交改动，不修改生成处理者或模型参数。

当前日常 runtime 为 `.local/daily/runtime.json`、存储 `.local/daily/official-store`。初始 owner84481、official84612、control84714；PID仅作当时快照，重载前重新核对身份与在途容量。开发50／55 GiB门禁继续有效。官方源码归档复用本机固定文件，不升级整个服务。原生插件安装已获本次接入授权，不修改登录、其他插件及历史材料。

## 实施与证据

**已完成源码适配、真实原生插件安装／启用、独立主体与权限核对，以及 Gemini App 和 Memlane Native 的实际界面检查。真实问答保存尚未通过：唯一一次 App 任务尝试被 Google 地区资格前置拒绝。** 保留已安装成果，未再次启动模型尝试。

### 当前已接入

- 实际 App：`com.google.GeminiMacOS`，`1.144.9.1499`；App 右上角的 Antigravity 模式已打开并识别 `memlane-cognee`。普通 Gemini 聊天的 Hooks 覆盖仍未核验，不能称全部聊天自动保存。
- 原生插件：`~/.gemini/config/plugins/memlane-cognee`，固定官方 Antigravity `1.6.8`；`agy plugin list --json` 返回 `enabled=true`、4组Hooks、5个官方skill。其他 `swarm` 配置保留。
- 独立安装身份：`antigravity-bc559c74ebf1abe9aa50e222`；官方主体 `cfff15f2-b69c-4f02-bfb3-6e64ea5de031`。沿原默认策略维护项目，权限不含share；`ml-user-profiles` 保持管理侧私有，新Agent不自动获得该组访问权。
- prepare／activate 已由管理接口执行并核对原生复制产物、完整摘要与私有安装绑定。原有8项目ID、旧主体ID、旧项目覆盖规则、同库根及generation配置摘要均保持。
- Memlane 实际窗口「设置 → Agent 与模型」显示 Gemini App、原生图标与“等待新会话验证”。新增步骤明确从App右上角进入Antigravity、选择项目和发送新消息，未把安装成功显示成自动保存正常。

### 实际任务与未完成边界

在 Gemini App 的 Antigravity 模式中关联现有 memlane 文件夹，新建会话并提交一次只读项目规则查询；没有修改项目文件、安装依赖或派生其他Agent。Google原生任务终止错误为：

> Your current account is not eligible for Gemini Code Assist for individuals because it is not currently available in your location.

App界面显示当前模型为 Gemini 3.8 Flash High。该失败属于账号／地区前置，不记录为插件采集失败；没有取得新的host环境回执或QAEntry持久保存回执。模型调用次数不由本次终止文字推断为零，亦不宣称实际问答、工具记录、整理、召回及普通聊天覆盖已经通过。失败会话及私有回执保留，未补采旧聊天、未改账户地区或登录。

后续需要可正常运行该模式的账号／环境，再执行本记录第4项真实新会话闭环。当前不自动重发原任务或重放既有会话。

### 检查与运行结果

- 新旧宿主发现／环境／官方派生产物及编号保存回归39项通过；随后原生安装目录身份支持与项目控制36项通过（包含5项Gemini定向检查）。两组有重叠，不相加作为独立用例数。
- 本机原生 `validate/install/list/enable` 先在临时宿主验证，再在真实日常配置中执行；临时目录已随任务清理。真实原生安装和App“Installed”页面的插件识别分别核对，未用CLI安装回执代替模型任务成功。
- 完整Go test／vet、接入和采集相关定向检查、强制Python AST lint通过。仅开发编译，没有构建release。
- 本轮补齐管理侧原生插件路径白名单：仅已登记的该安装目录可提交环境回执，继续核对源manifest、身份和runtime profile；安装文件的只读核验通过，不伪造一次host执行回执。
- 最终 owner86475 ready，completion86477、official86478、control86820；原配置与`.local/daily/official-store`保持。为加载回执校验通过dev.py正常stop，返回`Owned service groups exited; daily store released.`后再正常start；历史DEV-STOP01失败计数保留。
- 新Native watcher已由另一个工作会话附着同一当前runtime；本轮open --watch-ui被锁拒绝后复用该窗口，不终止或替换对方驱动。实际窗口86879／323271已核对当前服务61255及Gemini行。

### 本轮独立问题与证据

| 编号 | 事实／计数 | 处理与保留 |
| --- | --- | --- |
| GA-PREP01 | 首轮既有三宿主编号检查出现共同召回overlay锚点错误，1次 | 当前共享基准已包含空候选门禁；重复制备再次叠加触发。只对已核验产物接受同义的既有门禁，复验39项通过；原失败输出保留。与账号地区失败分开。 |
| GA-ACCOUNT01 | 唯一一次实际任务因地区资格拒绝，前置失败1次 | 不重复模型，不声称采集失败根因；保留安装和失败会话，等待可用账号／环境。 |
| GA-RECEIPT01 | 原路径白名单仅覆盖source和marketplace cache，缺Antigravity原生目录；在真实host回执前修复 | 只接受管理侧登记的native_plugin_dir，增加拒绝伪造路径检查。没有因账号错误而推断已经触发此产品失败。 |
| GA-PREFLIGHT01 | 一次空闲核对遇到服务读取未知 | 随后确认其他工作会话正在正常同库重启；新服务ready后继续，没有在未知时强行停止。 |
| GA-UI-OWNER01 | 本轮附着UI入口返回EAGAIN，准备事件1次 | 已有新watcher持锁；复用当前窗口并正常显示，不将锁占用计为Gemini产品缺陷。 |

私有备份和回执在 `.local/gemini-app-20261119/`（0600文件），包含配置备份、原操作ID、安装与最终状态，不提交密钥、原聊天或记忆库。preview `eb4dc696-7b85-41ad-b012-fc44c21dbfb1`、prepare `e74be2a4-03f9-4448-b995-0275e7ad7689`、activate `2a80f3e0-3b41-4f02-bec0-148c95f578cf` 均有完成回执；最终登记revision166。后续业务可能增加revision和PID，这些数字只对应本次核对。

### 已发现的兼容边界

- Antigravity 官方布尔型 run_session_improve 与 Codex详细回执不同；适配保持原语义并接入相同处理权限门禁。
- 固定服务的插件目录不认识 antigravity 自动provision；沿管理侧既有官方 agents/create 登记独立主体，禁止回退管理owner。
- 官方默认bootstrap可能准备本地服务；受管派生产物固定使用现有服务并禁止独立bootstrap。
- 同类问题计数沿原编号；本次初始兼容盘点不是新的真实采集失败，实际失败另按具体缺陷留证。

## 提交前激活边界补充

准备阶段的安装标记为未激活；只有原生安装和启用核对成功后，才向source和native两份标记发布相同的激活时间。双文件更新先保存私有恢复账本，中断后review仅核对原产物并恢复原边界，不重复安装。未激活标记拒绝采集，避免以prepare时间错误接纳激活前的会话。提交前当前接入与处理桥15项定向检查通过；已保留的本机插件未因commit再次重装，真实任务验证继续暂停。
