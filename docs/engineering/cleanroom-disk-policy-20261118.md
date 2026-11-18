# 净室误用开发磁盘门禁

用户要求继续处理净室容量并走通安装。已证：虚拟硬盘128GiB，原APFS62.8GB，新增68.7GB未分配；原工作卷方案失败3轮，未开始第4轮。本轮检查确认App环境安装、资源落盘、建库、启动及首次服务监测都硬编码55GiB，且App资源副本调用dev.py再次触发task.py门禁。该规则原用于宿主开发保留50GiB／提前5GiB停止，不是App的安装容量要求。将用户净室33GiB可用直接判为不能安装是边界错误。

本轮按diagnosing-bugs、plan-change-impact与verify-continuous-scenarios处理：先回归证明App无开发门禁仍可完成批准安装；取消App硬编码门禁，保留用户批准、目标所有权、原子日志、取消与真实IO错误。源码工作树task.py保持50/55；App由资源副本启动时明确分开，不将开发资源清理器复制执行到用户安装中。先检查两类启动路径与源码测试，再使用净室实际余量和安装计划判断是否足够，连续验证批准／安装／服务启动。检查不等于下载或新模型调用。

共享scripts/dev.py需先与日常会话协调；不启停其服务。首次启动、启用重启及内部停止都要保持同一App资源副本识别，不能只去掉第一道检查。当前尚未修改磁盘或删除任何分区。原扩容问题及失败计数保留，此产品门禁缺陷独立记录。

## 已执行与首次真实安装

已删除App环境／资源／建库／启动的55GiB阈值。Installer仅在开发调用方显式提供CheckSpace时执行保护与监控；App仍保留同意范围、锁、来源、进程取消、原始IO错误和分步回执。scripts/dev.py通过Go已校验资源树的私有marker及哈希／目录名识别App副本，App副本不进入task.py开发清理器；源码工作树仍进task.py并保留first_install数据盘保护。未设置绕过环境变量。

回归先红后绿：无开发回调的批准安装原报resource guard unavailable，修正后通过。`test_app_runtime_entrypoint.py`3项通过，覆盖App副本start／stop／status、开发工作树仍用包装器、无效marker拒绝；`test_dev.py`16项通过；全量Go test与vet、模型参数lint通过。未修改模型参数。

净室新`df -h /`实测59GiB总量、32GiB可用。独立disk-policy调试App二进制SHA256 `02c3105e49b8861751cc2134c3560d5ca7d3be087155d3076c10bf05e8b13182`，从原共享程序目录启动并批准既有六项计划，uv和Python真实安装后可用；组件目录实际124MiB。证明已越过误门禁，不能声称后续安装通过。

运行库出现独立缺陷BOOT-PATH-SPACE-01第1轮：uv退出2，File not found路径截到Library/Application；原默认目录含Application Support。host使用真实uv、含空格临时目录、空锁文件、离线／dry-run，无下载即可复现；相对约束文件名同命令退出0。新增真实uv回归先红，修正RuntimeDriver在已固定cwd内传requirements.lock相对文件名，保留同一锁内容／哈希及目标路径。当前正在回归，未重新下载前两项，原分区未修改。

后续回归：BOOT-PATH-SPACE-01真实uv安装回归通过，bootstrap全包3.615s通过；disk-policy-2 SHA256 `37212e9d47453765da95fbd2291980497ba2088cc39fb86b24f6c84624ecedb8`在guest读取原回执并显示仅4项缺失。批准新计划后遇到独立BOOT-UV-IDENTITY-01第1轮：同一uv重探测指纹变化导致ErrPlanChanged，未进入运行库下载。已用真实uv连续两次隔离探测复现（两个不同指纹）；原因是完整help含随机UV_CACHE_DIR默认值。修改为校验帮助能力后对版本与实际可执行文件内容哈希，不对临时help文本哈希。真实uv重复探测稳定及同版本／同help但可执行内容被替换必须改变指纹两个回归均先红后绿；bootstrap全包4.390s通过。未绕过批准范围或删除旧回执。

最初用宿主默认Python3.9运行generation四模块时有两个依赖导入错误（缺aiohttp/httpx），属于环境方法错误；已改用现有`.local/cognee-python/bin/python`运行，不安装依赖。结果待本轮结束补充。所有外部模型响应均由既有测试本地fixture提供，不启动净室Codex。

启动协议回归最终使用现有Python环境，34项通过（98.404s）。宿主最新候选与guest共享路径sha256一致为`d6bc7037994b4b28698dd7ffa0cdeb3a86249369be334dbffa5c1d22160420f8`。首次试图按进程名关闭旧GUI未真正使PID1567退出，后续open复用了旧进程；UV误判又出现，保留其操作证据，不把文件新hash当作运行候选已更新。已按明确PID1567发送TERM，pgrep无输出且GUI消失后再open；属于候选切换方法纠正，不宣称上一轮运行了新修复。共享目录cp回执被权限拒绝，未放宽共享权限；用guest原plutil只读核对steps.uv.error即可。控制台下划线输入被丢失，active_step查询失败是输入方法问题。

运行候选切换后续仍看到UV步骤ErrPlanChanged，现保守累计该误判2轮；已改用guest已安装Python执行只读诊断脚本，对比批准指纹、实际uv文件指纹和运行GUI路径，不重新提交安装。必须确认根因和候选身份后才继续；不以整个安装流程累计停止次数，也不把方法问题清零成新缺陷。

只读定位新证据：guest安装的uv按实际文件内容计算指纹为`d3b539f0cb651570105306b2d235bd6ad1015509b0629c6bd922b8e5551fd427`；独立新构建memlane-inventory-v3连续两次真实inventory返回同一值，与Python独立算法一致，未下载／初始化。旧GUI批准回执仍是`d365...`，与新算法不同；已确认其PID1605及共享路径，文件sha与host一致，但不能以文件hash证明已运行代码页。主GUI在同一共享路径被覆盖过，候选加载差异机制尚未证实。保留旧候选，按明确PID退出并确认pgrep为空，再使用未覆盖过的disk-policy-3固定副本（同d6bc...hash），不改修复代码或绕过批准校验。前次按名称退出及同路径覆盖相关尝试保留为方法边界，不重置具体误判失败记录；若固定新副本仍失败，按同一问题累计第4轮停止并汇报，不开始第5轮。

固定副本真实结果：PID1715明确运行disk-policy-3，批准UV指纹为d3b539…，与实际文件一致，UV ready；运行库ready（累计attempts2），Cognee源码ready。BOOT-UV-IDENTITY-01本轮通过，未触发第4次失败停止。原路径空格缺陷亦已通过实际运行库安装；尚不代表完整环境通过。剩余embedding第1轮失败：官方huggingface.co锁定revision的model_optimized.onnx连接被对端重置（read tcp 192.168.64.2→65.8.76.91:443 connection reset by peer），独立记录NET-EMBEDDING-01，非容量不足。已从原批准计划重试剩余步骤，不重装ready组件。只读诊断尾部StopIteration是inventory在UV已ready后不再将其列入缺失计划导致，状态与已打印指纹不受影响，未启动额外安装。

NET-EMBEDDING-01第2轮通过：Native向量模型显示可用，未更改DNS/VPN/源或摘要。继而首次触发BOOT-PLUGIN-LAYOUT-01：agent-plugin失败required source missing: codex（第1轮）。真实目录是integrations/codex/plugins/cognee；bootstrap错误按顶层codex选取。此codex是官方插件源码目录，不是要求净室登录/测试Codex CLI；前一条进度将它描述为强制依赖Codex不准确。

## 用户要求持续推进后的插件修复

选择integrations，从source/integrations/codex/plugins/cognee构建。真实固定归档也含顶层pyproject.toml，前文认为缺少它是未经证实的推断，现纠正；最终不放宽SourceDriver/VerifySource校验。新plugin-integrations产物位置保留旧失败根，内部使用integrations-upstream，不覆盖旧所有权标记。

固定归档连续回归覆盖解包、固定PA补丁、逐文件验证、重复安装复用、篡改拒绝且保留、pyproject必需。最初HTTP夹具被HTTPS策略拒绝、先预置子缓存导致父所有权缺失，均属准备错误，已改为准确私有所有权和既有下载缓存。最终全量Go测试通过：bootstrap12.856s、simpledesktop6.346s；vet及参数lint通过。仅debug编译，不release，不修改日常服务。

净室候选plugin-layout-1，二进制SHA256 cb61ad170547f2204e18e8d57f0fce850623a552e4fd5993a8f225aafe26f4e0，签名验证通过。旧PID1715退出、pgrep为空后启动新副本，待续装结果。

真实第2轮：Native仅列最后1项插件缺失，批准固定官方来源和新位置后完成，全部六项可用，准备概览显示已就绪。BOOT-PLUGIN-LAYOUT-01修复通过。随后点击继续，界面真实显示启动记忆服务，正在等待首次存储/服务回执，尚不代表Agent或模型通过。

## BOOT-NATIVE-OPENSSL-01（首次启动第1轮失败）

数据库迁移最终报MigrationError，直接原因是Ladybug的_lbug扩展引用/opt/homebrew/opt/openssl@3/lib/libssl.3.dylib及libcrypto.3.dylib。净室文件存在但dlopen失败；旧依赖检查仅import ladybug，没有实际加载_lbug，误报运行库可用。未重试建库，原store目录及迁移部分结果保留。UTM输入捕获及Shift字符丢失使几条诊断命令未按预期运行，属于方法事件，不新增迁移失败轮次。

采用私有运行库方案，不在guest安装Homebrew或修改/opt。先用Homebrew固定官方ARM bottle（3.5.9，Tahoe，sha e8d7…）的两项库做独立加载探针，guest输出NATIVE PORTABLE LOAD OK、退出0。随后安装器采用Sequoia bottle以避免把最低系统要求抬到26：[官方固定来源元数据](https://formulae.brew.sh/api/formula/openssl@3.json)，归档sha82b33ce156b289eaf95657b9ec16e8f49cb49c882cdeaeaf57e3e7d67fa85e25，10829301字节。仅保留两项库与LICENSE.txt；归档摘要、逐文件摘要、私有所有权、固定启动脚本及额外库拒绝都在探测前核对。公开GHCR匿名拉取头仅用于官方Homebrew路径，不读取用户registry凭据。

运行库原lock及目录保留，计划版本/说明明确列OpenSSL版本、来源、摘要，经新计划确认后补装；既有pip缓存可继续用。专用memlane-python入口只给该进程设置固定私有DYLD_LIBRARY_PATH，再执行原venv Python；不更改用户PATH/默认Python。ProbeDependencies增加实际ladybug._lbug加载。首次运行配置显式保存已验证Python入口，避免sys.executable把包装入口丢失。当前原生库补齐仅实现ARM开发候选，Intel分发及完整release仍未验证，不外推。

原生库私有安装/恢复/修改拒绝/原文件保留、含空格启动入口回归通过；全量Go测试通过（bootstrap17.613s、simpledesktop8.621s）。新增额外库拒绝后做定向回归；首次存储/启动与模型参数协议回归正在运行，真实净室完整启动仍待验证。

上述定向回归2.173s通过，首次安装/启动及四个模型参数模块共47项95.110s通过；仅本地协议fixture，无真实外部模型。原生修复候选native-libs-1 SHA256 5cedeb1a332da066c9ce75f80d548e9174e3516f5b29f1bd7f9e3209e62591ce，签名验证通过。旧PID2822确认退出后启动新候选；实际计划显示运行库补OpenSSL、已有模型待重新验证两项，来源/摘要及原目标目录可审阅，已按用户继续授权批准，正在执行。此阶段不重试首次建库，等待组件复核就绪后再恢复。

真实补齐成功并重新显示六项可用。随后首次迁移/建库通过，store-identity.json及runtime.json已生成，Python字段为venv/bin/memlane-python，BOOT-NATIVE-OPENSSL-01修复闭环通过。新独立BOOT-SETUP-HEALTH-01第1轮：SDK日志确认迁移完成、Uvicorn启动，但/health进入完整vector/graph检查，日志停在FastEmbed模型加载，启动器60s健康检查超时并退出1；未把建库成功当服务通过。具体为何完整模型加载超过时限仍未知。

首次模式现明确只对GET /health或/health/返回setup_ready、generation unconfigured、checks liveness_only，不调用完整内容健康路由；仅SDK ASGI lifespan完成后才可处理该请求。认证users/me仍走官方认证，启动器随后单独核对owner身份。内容请求继续503，真实配置及启用后的完整引擎检查保留，不以配置服务存活冒充记忆处理可用。11项首次模式/运行配置回归及lint通过，原生候选仅作开发调试；完整内容引擎冷启动须在后续真实记忆流程继续核对。

setup-health-1候选SHA256 bd365e971337cf8af156507faa3b14f094d586ae6c5a6c1d8a537998375ee4c3，签名验证通过，旧GUI确认退出后从全新目录启动。真实首次服务启动通过，Native进入第二步连接Agent，BOOT-SETUP-HEALTH-01的首次模式启动门槛已通过；完整内容健康仍待启用后核验。

净室便携工具最初只在guest-tools目录，App未自动发现。只在guest的标准用户目录.local/bin增加traecli软链及pi启动脚本，pi用原Node与dist/cli.js并保留参数；没有在产品中硬编码guest-tools，也没有改登录、模型或宿主配置。刷新后两项均被发现，Native批量准备/审阅/确认成功，均显示等待新会话验证。pi新会话实际加载memlane-cognee扩展，页脚仍为deepseek-v4-flash high；正在验证新消息和服务回执，不提前记为连接通过。
