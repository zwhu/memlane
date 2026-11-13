# 精简版运行报错记录

用户要求（2026-11-13）：运行过程中遇到的报错都记录下来，包括已经修复的问题，不能只记录最后仍未解决的问题。

本记录依据当前执行账本、清理账本和本次对话已观察到的工具结果汇总。整理记录不重跑任务、不启停服务；下表状态表示已有证据覆盖的范围，不推定后台当前状态。后续新报错继续追加到本记录。

## 记录规则

- 每项保留时间／场景、原始错误、版本或操作身份、证据、影响、已证原因、修复动作、复查结果与剩余未知。没有证明的原因写“未知”。
- 实现错误、配置错误、工具／环境限制和记录脚本错误均须留痕；已经修复也不删除原失败。
- 产品验收失败次数按同一具体问题累计；同轮官方重试或多个断言不重复计次。准备／诊断方法错误单列，不拿它们重置产品缺陷次数，也不把不同缺陷合并计数。
- 同一具体问题第4轮仍失败，遵守仓库停止规则，不自动第5轮。历史图谱及helper停止记录继续有效。
- 只记录必要错误和元数据；凭据、认证头、真实会话正文不进入本文件。原始敏感诊断保留在私有路径。

## 已观察到的报错和异常

| 编号 | 场景及原始现象 | 已证原因／未知 | 处理及已验证边界 | 证据／次数 |
| --- | --- | --- | --- | --- |
| E01 | 启动器进程组收尾检查：`PermissionError: [Errno 1] Operation not permitted`，来自 `os.killpg(pid, 0)` | 当次子进程实际已退出；不能把EPERM直接解释为存活或已退出 | EPERM时核对真实进程表；随后锁及收尾定向检查通过 | 执行记录“检查与当前问题”；该具体问题失败1轮 |
| E02 | 首次新启动：`ModuleNotFoundError: No module named 'aiohttp'`，随后 `owned service exited before readiness` | Python入口符号链接被规范化到基础解释器，绕过venv依赖 | 保留venv入口路径；随后启动成功，新增真实venv前缀回归通过；没有下载依赖 | 首次启动session98950；执行记录“日常接管进行中”；准备失败1次 |
| E03 | 创建项目回执脚本：`KeyError: 'owner_id'` | 回执使用的字段名与官方响应命名不同；dataset创建及配置保存已经完成 | 未重发创建请求；后续官方dataset及Native项目读取成功。回执缺字段不等于创建失败 | 项目创建工具结果；执行记录“当前已验证与下一步”；记录脚本错误1次 |
| E04 | 停掉原MCP后，又出现旧 `.local/dev/memlane mcp` 进程PID50281 | 宿主仍持有旧连接设置并重新拉起；原PID退出不证明旧链完全退出 | 复核精确身份后退役旧CLI二进制并停止该进程，防止继续由缓存设置拉起；随后清理旧库前再次检查消费者 | 主执行者进程表及精确删除工具结果；旧链退出检查发现1次 |
| E05 | `go mod tidy` 查找已删除的 `internal/official`、`agents`、`daemon` 等包，最后访问proxy超时 | Go扫描了build中历史Go片段，把它们当成活动包；并非新Native实际仍依赖这些包 | `scripts/go.sh`建立build嵌套module边界，保护历史文件并排除活动包扫描；后续tidy通过 | 执行记录“自动采集首条真实链路”；准备错误1次 |
| E06 | 编码会话中的包测试无法监听本地端口 | 该会话执行沙箱限制；不能把测试编译成功当包测试通过 | 保留外部会话的受限结果；主执行者随后在开发环境执行 `sh scripts/go.sh test ./internal/simpledesktop` 通过 | `.local/daily/desktop-auth-result.txt`及执行记录；环境限制1次 |
| E07 | CUA打开HTML原型：`The browser access host is unavailable` | 浏览器工具host不可用；Native CUA能力与浏览器能力不同 | 已查看设计截图；后续Native窗口可观察，不把它写成HTML原型已打开 | 执行记录“实施及清理状态”；工具限制，不计产品验收失败 |
| E08 | 首个正常编码会话的官方hook、capture、sync及improve返回HTTP401 | 将owner JWT放入 `COGNEE_API_KEY`；插件实际发送 `X-Api-Key`，两种凭据不能混用 | 官方签发同owner API key并更新私有配置；第二个正常会话真实保存1QA／15trace。原首轮仍记未采集 | thread `01a1d091-2be2-7a33-8a74-b2616a07b10b`；清理记录“认证配置缺陷”；该问题失败1轮 |
| E09 | D03首轮：`codex_schema_required`，官方重试后连接检查30秒超时，operation最终failed | 官方连接测试发普通文本completion；端点当时强制所有请求带JSON Schema | 普通文本改经内部字符串Schema执行并校验，再解包；业务结构化请求保留双校验。11项合成检查通过，不代表D03已通过 | operation `fa37b278-9a53-4565-8996-430d1d4fc627`；memify `9f33b909-5ff1-4f31-a26f-e44fbeb3fe47`；该契约问题失败1轮 |
| E10 | D03第2轮：`codex_transport_closed`，官方重试后连接检查30秒超时，operation最终failed | 正确复现中，resolved `codex.js` 在同一隔离PATH下找不到node，退出127 | 配置改指同一已安装包的原生二进制；该二进制无模型initialize成功。端点增加有界私有stderr诊断，12项合成检查通过；尚不能据此宣称官方处理成功 | operation `1ce04343-eb6d-42c2-aef1-44922d6f3f73`；memify `7cc52fa6-a3a5-4ea0-8c32-f543a97e35f6`；正确复现为 `.local/daily/codex-start-diagnostic-003.json`；该启动问题实际失败1轮 |
| E11 | 首份启动诊断001因隔离home不存在而退出 | 诊断准备与实际端点环境不同，不能证明node缺失 | 原001保留并标明方法差异；003才是同实际resolved脚本、存在的隔离home及PATH的失败复现；002记录同包native initialize成功 | `.local/daily/codex-start-diagnostic-001.json`、002、003；诊断准备错误单列，不新增或重置E10验收轮次 |
| E12 | 标准窗口列表中长UUID标题超出卡片文字区域 | Native列表文字区域未充分约束；属于1200×820标准窗口问题 | 限制文字宽度；未读取时省略UUID，读取后用已载入首句作阅读摘要，正文保留完整身份；实窗复查通过 | Native实际窗口观察及执行记录；该布局问题失败1轮，修正后1轮通过，不涉及旧窄窗 |
| E13 | 旧adapter删除前检查发现配置中仍残留connection标识 | 自有MCP表已解除，但精确自有begin注释仍含标识 | 安全检查停止删除；精确移除该注释、确认无关解析键值未变后，才删除adapter | 清理记录“R隔离宿主引用已精确解除”；清理检查准备问题1次 |
| E14 | 只读元数据检查：`sqlite3.OperationalError: unable to open database file`；随后将同路径当目录检查又报 `NotADirectoryError` | 两次辅助检查未得到有效元数据；具体打开失败原因没有证实 | 没有修改数据库，没有据此判断官方数据损坏；不把这两条检查当产品读写失败或成功 | 主执行者辅助只读检查工具结果；检查方法问题，原因未结清 |

## 另行保留的运行告警

- 首次官方启动提示未设置 `FASTAPI_USERS_*_SECRET`：随机进程级密钥会使重启后的JWT失效。随后已在私有服务配置固定认证密钥；没有关闭官方认证，不在此记录其值。
- 官方API启动出现 `FastAPIDeprecationWarning`：`example`参数被弃用。未观察到它阻止启动；属于固定上游告警，未改官方源码消除。
- 官方服务退出出现 `resource_tracker` semaphore清理告警，已观察到不同退出中的48／18／24等计数。这些数字不能证明还有相同数量的存活进程；已有所属进程组退出证据，semaphore告警的具体原因仍未结清。
- Native首页曾残留此前HTTP401提示，后续成功刷新后消失；保留恢复前事实，不把旧提示与新的认证请求失败混淆。

## 相关账本与范围

- [精简版执行记录](client-simplification-execution-20261113.md)
- [旧链清理及官方采集回执](client-simplification-cleanup-20261113.md)
- [历史达到停止阈值的问题](repeated-failures-20261111.md)

以上为截至本次整理可确认的报错汇总，不是对全部历史日志无遗漏的声明。后续运行新出现的报错、修复和复查结果继续按编号追加；同一问题关联原编号和原计数，不因修复或新操作ID另起计数。
