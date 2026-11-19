# Gemini／agy 记忆处理桥执行记录

2026-11-19。用户要求补齐 agy 模型处理桥、模型设置继承、结构化输出、取消、切换及 Native 处理来源选择。沿用“先保留安装，暂不验证实际任务”：本轮实施源码、受控协议回归与界面检查，不启动真实模型或自动改变当前处理者。

## 连续流程

1. 已安装的 Gemini App／agy → 读取所选 agy 的明确默认模型 → 处理来源列表显示 Gemini。
2. 浏览与编辑不启用；明确应用沿现有服务回执改变 active，取消保持原处理者。
3. Cognee 请求 → 同一共享执行槽 → 每次冻结 Agent 设置 → 原生 JSON Schema → 完整响应校验。
4. 任务私有上下文与无工具 Agent，隔离插件／Hooks／MCP／技能及日常采集；原生读取已有登录，不复制密钥为 Provider。
5. 取消或连接中断 → 原进程组退出确认 → 拒绝迟到结果 → 原材料及处理账本保持。

agy 默认模型来自 `~/.gemini/antigravity-cli/settings.json.model`，不等于 App 前台临时选择；无明确会话身份不猜最近会话。账号地区失败及采集待验见[接入记录](gemini-app-connection-20261119.md)。

## 影响与验证

复用 agent_bridge_runtime、generation_config、ValidationBridge、启动及 handoff，增加 agy runner与默认配置读取。Native经统一generationProfileGroups投影，保留Codex／pi／TraeX顺序并追加Gemini。使用已有环境及task.py；必要门禁为参数lint、test_generation_parameters、test_generation_probe、test_generation_handoff、test_generation_startup与定向原生协议回归。不改原库、当前active、Provider密钥或已安装采集插件。

先消除原生目录隔离与stdin／stdout契约未知，再接入runner。`--settings`不承载插件或Hooks开关，不能以空对象冒充隔离。官方帮助／文档、模拟协议、实际Native及真实模型证据分别记录。

## 当前进度

源码实现、受控协议回归、同库服务加载及实际Native候选展示已完成。当前执行者保持原Codex配置，未发起Gemini真实推理或实际切换。

### 已实现

- 四桥共用配置、启动、验证、请求槽和切换账本；新增antigravity默认模型读取与原生print runner。模型仅从agy明确的CLI settings读取，不把旧Memlane模型作为回退；请求接纳时冻结，在途不随后续配置变化。
- Native经generationProfileGroups投影追加Gemini，保持Codex／pi／TraeX顺序。模型来源注明agy默认配置；未绑定App前台会话，不声称继承其临时模型。
- 原生输入采用stdin NDJSON，不将材料放入argv；使用原生json-schema，仅读SUCCESS终态的structured_output，校验业务原Schema和传输Schema。工具／subagent事件、身份变化、缺失终态、认证及Schema错误均不作为成功。
- 处理工作区与HOME位于独立临时根，工作区不在本仓库内；明确的私有无工具Agent定义不复制旧规则、会话、插件、技能或登录文件。收到原生init的模型、空工具、私有cwd和Schema声明后才传材料，未声明则拒绝发送。
- 取消尝试原生Ctrl+C等价SIGINT，随后沿既有terminate及进程组检查收尾；取消、超时、连接中断和未知清理不复用迟到结果。未知进程仍登记并保留临时根。

### 验证与仍待真实证据

必要模型参数／Probe／Handoff／Startup及新桥回归46项通过；新增启动认证拒绝用例后agy7项通过，lint规则11项通过。完整Go test/vet、Gemini候选顺序及浏览不启用检查通过。受控执行者是测试中生成的独立进程，不能当作Google模型、真实订阅登录、Native全部工具隔离或质量验收。

无任务输入的原生加载检查明确发现root print拒绝`--gemini-dir`；此前help退出0不是运行支持证明。现采用私有HOME隔离文件，保留相同OS用户以供native自行读取Keychain，不复制或引用不明认证文件。私有HOME加载返回认证需要，尚不能判断是原CLI未登录还是私有环境的profile发现不同。没有发送prompt，不以原HOME models目录命令退出0冒称登录／模型成功。此边界留在真实验证待办，不自动迁移账号或切到API Key。

### 本轮问题记录

| 编号 | 事实与计数 | 处理及边界 |
| --- | --- | --- |
| GMB-ARGS01 | 第一次无输入运行探测root目录参数被拒绝，准备失败1轮 | 诊断读取确认root与ACP参数入口不同；后续改用私有HOME。两次只为读取同一错误分类的诊断不重复计算验收轮次。原help误判与失败保留在[研究报告](../research-cognee-gemini-app-20261119.md)。 |
| GMB-AUTH01 | 私有HOME无输入加载返回authentication required，前置失败1次 | root已进入原生认证路径，未发送任务；不能归因于私有HOME或账号未登录，真实认证互通仍待验证。 |
| GMB-METHOD01 | 首次集中apply_patch因hunk格式错误原子拒绝，方法错误1次 | 核对没有留下半写文件后拆成明确文件补丁，未影响运行服务。 |

同一具体问题按原停止规则追踪；原采集地区资格失败GA-ACCOUNT01不与此处认证或参数问题合并。没有改全局采集插件、旧会话、Provider凭据或日常材料。

### 最终接入与实际窗口

管理API已实际返回antigravity／codex／pi／traex四种候选，Gemini默认值为`Gemini 3.8 Flash (High)`，来源agent_config／agy_cli_settings。同库重载前后generation文件SHA、8项目ID、6主体ID和原库根均保持；active仍existing-codex revision2，next为空。

实际Memlane窗口97697／323748的「设置 → 记忆处理」已显示第四行Gemini及`Gemini 3.8 Flash (High) / agy 默认配置`。选中该候选后显示“切换”，当前使用标题仍Codex；随后恢复Codex候选，generation文件摘要未改变。未点击切换、发起验证、保存或触发模型；真实结构化质量、认证复用、活动推理取消与日常处理闭环仍待用户恢复实际验证。

本轮先打开当前窗口仍显示旧三候选，原因是已加载的客户端缓存尚未重读；通过Agent与模型页的既有“重新检测”重新读取后，四行实际显示通过。新UI驱动已由既有watcher持锁，本轮不新增第二驱动；同bundle多个实例导致两次“not prepared”，经computer-use重新打开并绑定当前可见窗口恢复，未用其他自动化绕过。原失败状态与准备事件保留，不把界面缓存或工具状态错误计成原生推理失败。

新的普通工作区检查结束后，当前开发后台继续运行；既有UIwatcher保持，不清理他人资源、旧包或p3-prototype工作树。临时协议进程及目录在所属任务结束后清理，没有新增第二运行库或SDK依赖。
