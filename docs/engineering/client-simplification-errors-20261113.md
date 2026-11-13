# 精简版错误与防复发索引

2026-11-13。供后续Agent接续、定位和复核；记录的是当时事实，不是当前运行健康检查。保留原E01–E14编号，补齐E15–E24；不删除失败，不将历史状态冒充最新结果。

机器可读同版数据：[结构化JSON](client-simplification-errors-20261113.json)。阶段事实：[执行记录](client-simplification-execution-20261113.md)；最终范围：[交付记录](client-simplification-delivery-20261113.md)。

## 后续Agent先读

- 同一触发、现象和验收标准沿用编号及累计次数。E17两次失败、第三轮通过，失败累计仍为2；E09/E10/E15各1轮，不合并整个D03计数。
- 产品验收、准备/诊断事件和官方同轮重试分开。表中“—”表示未登记产品验收轮次；E19的0表示风险提前规避，没有实际失败。
- 同一具体问题第4次仍失败即停止，按[工程停止规则](workflow.md)留证汇报。[原停止账本](repeated-failures-20261111.md)中的helper和固定328字节README问题不恢复。
- 先查证据与实现，不重复模型、索引、清理或启停服务补材料；私有原件缺失时报告缺口，不自动重跑。
- E01是存活检查，E22是UI异常传播，E23是TIME_WAIT预检；相同command或页面不足以合并计数。

- 历史PID、工具session和路径只用于定位证据；启停前重新核对当前owner、启动时间、数据锁及依赖，不按旧PID直接发信号。

## 分类速查

| 类别 | 编号 |
| --- | --- |
| 生命周期 | [E01](#e01), [E22](#e22), [E23](#e23) |
| 运行环境 | [E02](#e02), [E10](#e10) |
| 工具与诊断 | [E03](#e03), [E06](#e06), [E07](#e07), [E11](#e11), [E14](#e14), [E21](#e21), [E24](#e24) |
| 旧链与清理 | [E04](#e04), [E13](#e13), [E18](#e18) |
| 构建与扫描 | [E05](#e05) |
| 认证与采集 | [E08](#e08) |
| 推理契约 | [E09](#e09) |
| Native界面 | [E12](#e12), [E16](#e16) |
| 推理容量 | [E15](#e15) |
| 代码索引范围 | [E17](#e17) |
| 任务身份 | [E19](#e19) |
| 图谱宿主 | [E20](#e20) |

## 状态与计数

| 编号 | 问题 | 状态 | 产品失败轮次 | 准备/观察事件 |
| --- | --- | --- | ---: | ---: |
| [E01](#e01) | 进程组存活检查将EPERM当成退出判据 | 已修复 | 1 | — |
| [E02](#e02) | Python符号链接规范化绕过venv | 已修复 | — | 1 |
| [E03](#e03) | 回执脚本使用错误的owner字段名 | 已修复 | — | 1 |
| [E04](#e04) | 旧MCP被缓存配置重新拉起 | 已修复 | — | 1 |
| [E05](#e05) | Go把build历史源码当成活动包 | 已修复 | — | 1 |
| [E06](#e06) | 编码沙箱限制测试监听端口 | 环境限制已绕开 | — | 1 |
| [E07](#e07) | 浏览器自动化host不可用 | 替代工具已完成目标 | — | — |
| [E08](#e08) | 将JWT误当官方API key | 已修复 | 1 | — |
| [E09](#e09) | 普通completion被强制要求业务Schema | 已修复 | 1 | — |
| [E10](#e10) | 隔离PATH无法运行codex.js包装器 | 已修复 | 1 | — |
| [E11](#e11) | 诊断001的隔离HOME准备不一致 | 诊断方法已纠正 | — | 1 |
| [E12](#e12) | 标准窗口UUID标题溢出 | 已修复 | 1 | — |
| [E13](#e13) | 旧adapter引用检查仍发现自有注释标识 | 清理检查已结清 | — | 1 |
| [E14](#e14) | 辅助只读数据库检查失败 | 原因未知／非产品故障结论 | — | 2 |
| [E15](#e15) | 单执行槽/单等待不足以接收官方并发批次 | 已修复 | 1 | — |
| [E16](#e16) | 多层来源返回丢失原搜索片段 | 已修复 | 1 | — |
| [E17](#e17) | 官方代码图混入生成目录与历史依赖清单 | 配置规避后验收通过（能力缺口保留） | 2 | — |
| [E18](#e18) | 大规模错误代码图公开DELETE超时 | 替代官方清理已完成／原请求未成功确认 | — | 1 |
| [E19](#e19) | 同名重建dataset继承旧STARTED记录的风险 | 防护已验证／未记实际失败 | 0 | — |
| [E20](#e20) | 查看记录按钮接到错误官方缩放层级 | 已修复 | 1 | — |
| [E21](#e21) | fake-owner回归夹具路径规范化不一致 | 夹具已纠正 | — | 1 |
| [E22](#e22) | 窗口重开清理EPERM向后台所有者传播 | 已修复 | 1 | — |
| [E23](#e23) | 端口预检把TIME_WAIT误判为活动监听 | 已修复 | 1 | — |
| [E24](#e24) | 长Unicode内联文档脚本被Python拒绝解析 | 改用文件脚本完成／传输原因未知 | — | 3 |

## 证据保留与可信边界

- [原始E01–E14文档](evidence/client-simplification-20261113/original-errors.md)按整理前字节保留；旧状态不能覆盖本索引后续结果。
- [证据清单](evidence/client-simplification-20261113/manifest.json)保留16份脱敏metadata摘录、一份工具错误转录摘要、各归档SHA256及可用的私有原件SHA256。未复制凭据、请求头、模型输入输出、聊天正文、数据库或完整本机配置。
- 摘录证明记录时的终态、数量、身份、PID、健康等字段，不是完整日志或现时重测。摘要只证明保留完整性，不证明原始记录无遗漏。
- Native截图有原会话工具观察及执行账本，当前归档没有独立图片文件，不声称截图已入Git。E14等缺少独立原始输出的条目保持未知。
- 被清理的重复JSONL、prompt和一次性脚本不重建；官方session身份、原Agent聊天与必要私有证据按原契约保护。

## 逐项记录

<a id="e01"></a>

### E01 · 进程组存活检查将EPERM当成退出判据

- **类别**：生命周期
- **当前记录状态**：已修复
- **触发与范围**：启动器定向收尾检查
- **原始错误/现象**：os.killpg(pid, 0): PermissionError [Errno 1]
- **影响与保留状态**：收尾判据不能确认进程组状态；不能据此释放所有权或宣称已停止。
- **已证原因**：当次子进程已退出；EPERM本身不能证明进程组存活或消失。
- **修复/处理**：EPERM时读取真实进程表核对group；保留未知而非直接成功。
- **已记录验证**：随后定向收尾检查通过；scripts/dev.py group_exists与test_stop_reaps_child_group覆盖当前实现。
- **未知/限制**：当次macOS内核返回EPERM的精确时序没有完整追踪。
- **后续指导**：killpg异常不是进程状态；检查真实进程组。与E22的UI异常传播是不同触发及修复对象，计数不合并。
- **计数**：1轮。
- **证据**：[原记录E01](evidence/client-simplification-20261113/original-errors.md)；[执行记录：检查与当前问题](client-simplification-execution-20261113.md)；[scripts/dev.py:group_exists](../../scripts/dev.py)；[scripts/test_dev.py:test_stop_reaps_child_group](../../scripts/test_dev.py)。

<a id="e02"></a>

### E02 · Python符号链接规范化绕过venv

- **类别**：运行环境
- **当前记录状态**：已修复
- **触发与范围**：首次启动新服务session98950
- **原始错误/现象**：ModuleNotFoundError: aiohttp；owned service exited before readiness
- **影响与保留状态**：首次服务未ready，所属端点退出后恢复原环境；未下载依赖或新建日常库。
- **已证原因**：把虚拟环境Python入口resolve为基础解释器，绕过pyvenv.cfg及已有依赖。
- **修复/处理**：保留venv可执行路径；只对需要规范化的数据根等路径resolve。
- **已记录验证**：真实服务后续启动成功；test_config_keeps_virtual_environment_python验证实际sys.prefix。
- **未知/限制**：未记录独立剩余原因；不外推到其他版本或环境。
- **后续指导**：先核对实际解释器/venv，不以重新安装依赖掩盖入口错误。
- **计数**：未登记产品验收轮次；准备/观察事件1次。
- **证据**：[原记录E02](evidence/client-simplification-20261113/original-errors.md)；[执行记录：日常接管进行中](client-simplification-execution-20261113.md)；[scripts/test_dev.py:test_config_keeps_virtual_environment_python](../../scripts/test_dev.py)。

<a id="e03"></a>

### E03 · 回执脚本使用错误的owner字段名

- **类别**：工具与诊断
- **当前记录状态**：已修复
- **触发与范围**：官方项目dataset已创建后整理回执
- **原始错误/现象**：KeyError: owner_id
- **影响与保留状态**：回执整理失败但dataset与配置写入成功；盲重发可能产生重复业务操作。
- **已证原因**：脚本字段名与官方DTO实际命名不同；创建与配置保存已完成。
- **修复/处理**：修正回执读取；先读取已有dataset及配置，不重发创建。
- **已记录验证**：后续官方dataset与Native项目读取成功。
- **未知/限制**：未记录独立剩余原因；不外推到其他版本或环境。
- **后续指导**：回执写失败与业务提交失败分开；未知时先查原操作身份，禁止盲重发写入。
- **计数**：未登记产品验收轮次；准备/观察事件1次。
- **证据**：[原记录E03](evidence/client-simplification-20261113/original-errors.md)；[执行记录：当前已验证与下一步](client-simplification-execution-20261113.md)。

<a id="e04"></a>

### E04 · 旧MCP被缓存配置重新拉起

- **类别**：旧链与清理
- **当前记录状态**：已修复
- **触发与范围**：原旧MCP PID退出之后
- **原始错误/现象**：再次出现 .local/dev/memlane mcp，PID50281
- **影响与保留状态**：旧写者可能继续消费旧配置；最终清理前必须再确认全部消费者退出。
- **已证原因**：宿主仍持有旧连接设置；停止原PID没有消除旧启动入口。
- **修复/处理**：核对精确命令身份，移除旧引用、退役自有CLI并退出该进程，再核对消费者后清理旧库。
- **已记录验证**：最终进程/配置只读审计未发现旧daemon、MCP、capture或pi扩展引用。
- **未知/限制**：未记录独立剩余原因；不外推到其他版本或环境。
- **后续指导**：进程退出、宿主配置解除、旧启动入口退出须分别核对；保留第三方配置。
- **计数**：未登记产品验收轮次；准备/观察事件1次。
- **证据**：[原记录E04](evidence/client-simplification-20261113/original-errors.md)；[清理记录：最终只读复核](client-simplification-cleanup-20261113.md)；[执行记录：最后只读旧链核对](client-simplification-execution-20261113.md)。

<a id="e05"></a>

### E05 · Go把build历史源码当成活动包

- **类别**：构建与扫描
- **当前记录状态**：已修复
- **触发与范围**：go mod tidy扫描仓库
- **原始错误/现象**：查找已删除internal包，随后proxy超时
- **影响与保留状态**：依赖分析被历史源码污染；不能据此恢复已取消模块或删除build证据。
- **已证原因**：build内历史Go片段没有module边界，被Go包发现机制纳入。
- **修复/处理**：scripts/go.sh建立build/go.mod嵌套模块边界，保留历史证据，不递归删build。
- **已记录验证**：后续tidy、活动Go test与vet通过。
- **未知/限制**：未记录独立剩余原因；不外推到其他版本或环境。
- **后续指导**：按活动源码边界排查；包发现污染不能靠恢复已取消代码或清空证据解决。
- **计数**：未登记产品验收轮次；准备/观察事件1次。
- **证据**：[原记录E05](evidence/client-simplification-20261113/original-errors.md)；[执行记录：自动采集首条真实链路](client-simplification-execution-20261113.md)；[scripts/go.sh](../../scripts/go.sh)。

<a id="e06"></a>

### E06 · 编码沙箱限制测试监听端口

- **类别**：工具与诊断
- **当前记录状态**：环境限制已绕开
- **触发与范围**：正常编码任务内运行HTTP包测试
- **原始错误/现象**：不能监听本地端口
- **影响与保留状态**：当次沙箱内包测试没有通过，后续开发环境结果须单独记录。
- **已证原因**：当次编码会话的执行沙箱限制；不证明实现测试失败。
- **修复/处理**：保留沙箱限制结果，由主执行者在已授权开发环境运行受影响包。
- **已记录验证**：sh scripts/go.sh test ./internal/simpledesktop 后续通过。
- **未知/限制**：未记录独立剩余原因；不外推到其他版本或环境。
- **后续指导**：编译通过不能替代测试通过；环境限制单列，不记成产品回归次数。
- **计数**：未登记产品验收轮次；准备/观察事件1次。
- **证据**：[原记录E06](evidence/client-simplification-20261113/original-errors.md)；`.local/daily/desktop-auth-result.txt`（本机私有或仅原会话输出）；[执行记录：自动采集首条真实链路](client-simplification-execution-20261113.md)。

<a id="e07"></a>

### E07 · 浏览器自动化host不可用

- **类别**：工具与诊断
- **当前记录状态**：替代工具已完成目标
- **触发与范围**：CUA直接打开HTML原型
- **原始错误/现象**：The browser access host is unavailable
- **影响与保留状态**：最初交互原型打开未验证；不能以设计截图替代可交互稿。
- **已证原因**：当时浏览器工具surface不可用，Native CUA能力独立可用。
- **修复/处理**：先看设计截图；后续用Chrome Guest原生应用打开同一HTML原型并查看三栏页。
- **已记录验证**：执行记录17:44UTC补充确认实际HTML原型已打开；原记录中的未打开为当时状态。
- **未知/限制**：未记录独立剩余原因；不外推到其他版本或环境。
- **后续指导**：工具surface不可用不等于页面不可用；截图验证与可交互原型打开分别记录。
- **计数**：未登记产品验收轮次。
- **证据**：[原记录E07](evidence/client-simplification-20261113/original-errors.md)；[执行记录：原日常服务恢复与代码组重建](client-simplification-execution-20261113.md)。

<a id="e08"></a>

### E08 · 将JWT误当官方API key

- **类别**：认证与采集
- **当前记录状态**：已修复
- **触发与范围**：首个真实编码会话官方hook/capture/sync/improve
- **原始错误/现象**：HTTP401
- **影响与保留状态**：首会话未被官方采集；第二会话成功不能改写第一会话的失败事实。
- **已证原因**：把owner JWT放入COGNEE_API_KEY；官方插件实际发送X-Api-Key。
- **修复/处理**：同owner签发正式API key，插件与桌面统一X-Api-Key，不做Bearer回退。
- **已记录验证**：后续真实会话codex_01a1d093-9db5-7c01-86bf-5d6e89a33203保存1QA/15trace，Native可读。
- **未知/限制**：未记录独立剩余原因；不外推到其他版本或环境。
- **后续指导**：官方API key、本机completion Bearer、Codex订阅登录分开；原未采集会话不补写成成功。
- **计数**：1轮。
- **证据**：[原记录E08](evidence/client-simplification-20261113/original-errors.md)；[执行记录：自动采集首条真实链路](client-simplification-execution-20261113.md)；[internal/simpledesktop/client_test.go:TestOfficialReadUsesDatasetAndCredential](../../internal/simpledesktop/client_test.go)。

<a id="e09"></a>

### E09 · 普通completion被强制要求业务Schema

- **类别**：推理契约
- **当前记录状态**：已修复
- **触发与范围**：D03第1轮官方连接检查
- **原始错误/现象**：codex_schema_required；随后30秒超时
- **影响与保留状态**：首轮官方处理failed，原始材料保持，不能宣称知识已生成。
- **已证原因**：端点要求所有请求携带业务JSON Schema，但官方连接检查使用普通文本completion。
- **修复/处理**：普通文本使用内部闭合字符串Schema执行/校验后解包，业务Schema保留原始及传输层双校验。
- **已记录验证**：协议回归与普通completion检查通过；D03第4轮operation实际succeeded并保存两条知识。
- **未知/限制**：未记录独立剩余原因；不外推到其他版本或环境。
- **后续指导**：先满足官方真实请求契约；同轮官方重试不额外计次，不以跳过连接检查求通过。
- **计数**：1轮。
- **证据**：[d03-first-official-processing-metadata.json](evidence/client-simplification-20261113/d03-first-official-processing-metadata.json)；[d03-round4-final-metadata.json](evidence/client-simplification-20261113/d03-round4-final-metadata.json)；[engine/python/test_codex_completion_endpoint.py:test_official_connection_plain_completion](../../engine/python/test_codex_completion_endpoint.py)；[执行记录：D03首轮实际失败](client-simplification-execution-20261113.md)。
- **failed_operation_id**：`fa37b278-9a53-4565-8996-430d1d4fc627`。
- **later_success_operation_id**：`0e86e197-ab13-4762-bc3f-3c143472d072`。

<a id="e10"></a>

### E10 · 隔离PATH无法运行codex.js包装器

- **类别**：运行环境
- **当前记录状态**：已修复
- **触发与范围**：D03第2轮真实Codex启动
- **原始错误/现象**：codex_transport_closed；env找不到node，exit127
- **影响与保留状态**：第二轮处理failed；请求数与实际模型执行次数不能凭transport错误推断。
- **已证原因**：resolved codex.js依赖日常Node/PATH，隔离环境没有该解释器。
- **修复/处理**：改用同一已安装版本的原生Codex二进制；增加有界私有stderr诊断。
- **已记录验证**：002原生initialize成功（无模型）；003与实际隔离环境一致的red；D03第4轮真实模型处理成功。
- **未知/限制**：未记录独立剩余原因；不外推到其他版本或环境。
- **后续指导**：协议closed只是表象；核对真正执行文件、解释器、PATH及stderr，不默认换模型/版本。
- **计数**：1轮。
- **证据**：[codex-start-diagnostic-002.json](evidence/client-simplification-20261113/codex-start-diagnostic-002.json)；[codex-start-diagnostic-003.json](evidence/client-simplification-20261113/codex-start-diagnostic-003.json)；[d03-round2.json](evidence/client-simplification-20261113/d03-round2.json)；[d03-round4-final-metadata.json](evidence/client-simplification-20261113/d03-round4-final-metadata.json)；[执行记录：第3轮结果及容量修正](client-simplification-execution-20261113.md)。
- **failed_operation_id**：`1ce04343-eb6d-42c2-aef1-44922d6f3f73`。
- **later_success_operation_id**：`0e86e197-ab13-4762-bc3f-3c143472d072`。

<a id="e11"></a>

### E11 · 诊断001的隔离HOME准备不一致

- **类别**：工具与诊断
- **当前记录状态**：诊断方法已纠正
- **触发与范围**：首次复现E10
- **原始错误/现象**：隔离home不存在而退出
- **影响与保留状态**：首诊断不能支持真实根因，须保留方法差异及后续正确red。
- **已证原因**：诊断环境与真实端点环境不同，不能证明E10根因。
- **修复/处理**：保留001，采用003的存在HOME/同PATH/同resolved入口，002单列原生无模型初始化。
- **已记录验证**：003提供E10对应red；001不再作为node缺失的证明。
- **未知/限制**：未记录独立剩余原因；不外推到其他版本或环境。
- **后续指导**：红证据必须匹配真实前置条件；诊断准备错误不新增/重置产品失败次数。
- **计数**：未登记产品验收轮次；准备/观察事件1次。
- **证据**：[codex-start-diagnostic-001.json](evidence/client-simplification-20261113/codex-start-diagnostic-001.json)；[codex-start-diagnostic-002.json](evidence/client-simplification-20261113/codex-start-diagnostic-002.json)；[codex-start-diagnostic-003.json](evidence/client-simplification-20261113/codex-start-diagnostic-003.json)；[原记录E11](evidence/client-simplification-20261113/original-errors.md)。

<a id="e12"></a>

### E12 · 标准窗口UUID标题溢出

- **类别**：Native界面
- **当前记录状态**：已修复
- **触发与范围**：1200×820列表显示长UUID
- **原始错误/现象**：文字超出卡片区域
- **影响与保留状态**：正常尺寸列表的阅读与身份辨识受影响；不扩大为窄窗工作。
- **已证原因**：列表文字可用宽度未充分约束。
- **修复/处理**：约束文字宽度，未读取时省略UUID，已载入后用首句摘要；正文保留完整身份。
- **已记录验证**：实际Native同标准尺寸复查通过。
- **未知/限制**：未记录独立剩余原因；不外推到其他版本或环境。
- **后续指导**：属于正常尺寸缺陷；不恢复已取消的窄窗验收。
- **计数**：1轮。
- **证据**：[原记录E12](evidence/client-simplification-20261113/original-errors.md)；[执行记录：D03第2轮与运行保护](client-simplification-execution-20261113.md)；[internal/simpledesktop/app.go](../../internal/simpledesktop/app.go)。

<a id="e13"></a>

### E13 · 旧adapter引用检查仍发现自有注释标识

- **类别**：旧链与清理
- **当前记录状态**：清理检查已结清
- **触发与范围**：解除MCP配置表后准备删除adapter
- **原始错误/现象**：connection标识仍存在
- **影响与保留状态**：删除在安全检查失败时停止；引用解除后才完成精确删除。
- **已证原因**：配置表解除后，自有begin注释仍含该标识。
- **修复/处理**：停止删除，只移除该精确注释，核对无关配置解析值未变后再删除adapter。
- **已记录验证**：旧R三安装目录及对应配置引用退出，官方与第三方数据保持。
- **未知/限制**：未记录独立剩余原因；不外推到其他版本或环境。
- **后续指导**：安全检查失败先停止删除；不能扩大为清空宿主配置或整个适配器目录。
- **计数**：未登记产品验收轮次；准备/观察事件1次。
- **证据**：[原记录E13](evidence/client-simplification-20261113/original-errors.md)；[清理记录：R隔离宿主引用已精确解除](client-simplification-cleanup-20261113.md)。

<a id="e14"></a>

### E14 · 辅助只读数据库检查失败

- **类别**：工具与诊断
- **当前记录状态**：原因未知／非产品故障结论
- **触发与范围**：辅助元数据检查
- **原始错误/现象**：sqlite3.OperationalError: unable to open database file；NotADirectoryError
- **影响与保留状态**：辅助元数据未取得；没有修改数据库，不能据此认定损坏或无数据。
- **已证原因**：两个辅助检查未取得有效元数据；具体打开失败原因没有证实。
- **修复/处理**：没有修改数据库，也没有据此认定官方数据损坏。
- **已记录验证**：后续主链官方读取/处理成功独立成立；没有对这两条辅助错误完成根因复验。
- **未知/限制**：原始错误的具体打开失败原因未结清；原始工具输出未独立入库。
- **后续指导**：方法失败保持未知，不转写为数据损坏、无数据或产品验证通过。
- **计数**：未登记产品验收轮次；准备/观察事件2次。
- **证据**：[原记录E14](evidence/client-simplification-20261113/original-errors.md)；`原会话工具结果（无独立回执副本）`（本机私有或仅原会话输出）。

<a id="e15"></a>

### E15 · 单执行槽/单等待不足以接收官方并发批次

- **类别**：推理容量
- **当前记录状态**：已修复
- **触发与范围**：D03第3轮官方并行抽取
- **原始错误/现象**：HTTP429 codex_busy；部分pipeline ERRORED
- **影响与保留状态**：部分官方写入已发生，后来pipeline失败；保留部分数据，不当作完全未执行。
- **已证原因**：自选1执行/1等待容量不足；该轮已有部分官方写入，不是模型未运行。
- **修复/处理**：使用2执行/32总在途的有界内存Semaphore；不加持久队列/自动重试，不重放采集材料。
- **已记录验证**：13项协议/HTTP/子进程检查含并发与shutdown；第4轮operation 0e86e197-ab13-4762-bc3f-3c143472d072成功，官方保存两条知识。
- **未知/限制**：未记录独立剩余原因；不外推到其他版本或环境。
- **后续指导**：真实并发契约须有证据；D03三轮分别E09/E10/E15各1，不能将整个功能合并成同一问题3次。
- **计数**：1轮。
- **证据**：[d03-round3-terminal-metadata.json](evidence/client-simplification-20261113/d03-round3-terminal-metadata.json)；[d03-round4-final-metadata.json](evidence/client-simplification-20261113/d03-round4-final-metadata.json)；[engine/python/test_codex_completion_endpoint.py:test_two_execution_slots_bound_parallel_batch](../../engine/python/test_codex_completion_endpoint.py)；[engine/python/test_codex_completion_endpoint.py:test_capacity_and_server_shutdown_reap_process](../../engine/python/test_codex_completion_endpoint.py)。
- **later_success_operation_id**：`0e86e197-ab13-4762-bc3f-3c143472d072`。
- **failed_pipeline_ids**：`['984f634f-eded-4862-8f44-f64c449a0286', '44b6bbb2-1cc2-4769-ae83-a3e4496d618e']`。

<a id="e16"></a>

### E16 · 多层来源返回丢失原搜索片段

- **类别**：Native界面
- **当前记录状态**：已修复
- **触发与范围**：搜索片段→知识→会话→连续返回
- **原始错误/现象**：只能回一层，原片段/阅读上下文丢失
- **影响与保留状态**：来源链返回丢失原片段及阅读上下文。
- **已证原因**：单个Return状态被后一次来源跳转覆盖。
- **修复/处理**：用内存返回栈保存逐层阅读上下文，不复制官方正文持久化。
- **已记录验证**：实窗回到原片段，查询/类型/计数/位置保持；TestNestedOfficialSourceReturnsToOriginalSearch通过。
- **未知/限制**：未记录独立剩余原因；不外推到其他版本或环境。
- **后续指导**：按完整连续路径验证，单次跳转/返回不能证明多层来源可恢复。
- **计数**：1轮。
- **证据**：[执行记录：D03成功与D04实际采用](client-simplification-execution-20261113.md)；[internal/simpledesktop/client_test.go:TestNestedOfficialSourceReturnsToOriginalSearch](../../internal/simpledesktop/client_test.go)。

<a id="e17"></a>

### E17 · 官方代码图混入生成目录与历史依赖清单

- **类别**：代码索引范围
- **当前记录状态**：配置规避后验收通过（能力缺口保留）
- **触发与范围**：真实memlane仓首次索引及第二轮修正后更新
- **原始错误/现象**：首轮61539解析文件/1828913 facts被.cache等污染；第二轮仍有22个build manifest出处
- **影响与保留状态**：首轮服务内存约9.64GiB、可用空间117降至110GiB；目标错误代码图精确收尾，primary7文档保留；第二轮112旧节点由第三轮官方sweep清除。
- **已证原因**：首轮Enola不继承.gitignore且默认未排.local/.cache/.mygo；第二轮manifest extractor丢弃filtered file list自行Walk，不遵守全局ignore。
- **修复/处理**：保留115默认ignore并加6条项目生成目录规则；第二轮后停用官方manifests extractor，保留其余19默认解析器。普通手动更新原dataset，官方stale sweep，不改SDK或过滤图。
- **已记录验证**：第三轮pipeline807eee39-a3d0-47a6-b597-a0ad9082657f COMPLETED；2287基础facts/276出处违规0；全量持久化2316节点/5597边无污染，官方删112旧节点。
- **未知/限制**：manifest派生跨生态依赖清单事实当前不可用；不是上游扫描器已修复。
- **后续指导**：非空图/200/大节点数不代表scope正确；新facts与持久化全图都要核对。两轮同一范围问题累计2，第三轮通过不清零。
- **计数**：2轮。
- **证据**：[t5-first-enola-receipt.json](evidence/client-simplification-20261113/t5-first-enola-receipt.json)；[t5-round2-scope-metadata.json](evidence/client-simplification-20261113/t5-round2-scope-metadata.json)；[t5-round3-scope-metadata.json](evidence/client-simplification-20261113/t5-round3-scope-metadata.json)；[t5-round3-persisted-graph-scope.json](evidence/client-simplification-20261113/t5-round3-persisted-graph-scope.json)；[mcp-arch.yaml](../../mcp-arch.yaml)；[执行记录：第二轮代码范围验收失败](client-simplification-execution-20261113.md)。

| 轮次 | 版本/配置或工具session | 任务身份 | 结果 | 累计失败 |
| --- | --- | --- | --- | ---: |
| 1 | Enola 0.4.30 official defaults | 0c7e1101-23d0-4649-97f0-19a1e4963103 | 范围失败；中断后官方最终标记abandoned，不是成功或已主动取消 | 1 |
| 2 | Enola 0.4.30 121 ignores; manifests enabled | f4de61f7-c764-4db1-a25f-fd81e7272fed | 技术completed但范围失败：22个build manifest出处 | 2 |
| 3 | Enola 0.4.30 121 ignores; 19 extractors, manifests disabled | 807eee39-a3d0-47a6-b597-a0ad9082657f | 范围与全量持久化图复核通过；112旧节点已清除，manifest能力缺口保留 | 2 |

<a id="e18"></a>

### E18 · 大规模错误代码图公开DELETE超时

- **类别**：旧链与清理
- **当前记录状态**：替代官方清理已完成／原请求未成功确认
- **触发与范围**：清理E17首轮错误dataset
- **原始错误/现象**：公开DELETE客户端600秒timeout，后台worker仍CPU100%
- **影响与保留状态**：客户端超时后后台仍活跃，不能重发或直接清文件；替代SDK只删除独立错误code组。
- **已证原因**：固定公开路径先做大图来源遍历再drop；sample仅证明在Ladybug原生查询，具体慢算子与剩余时间未知。
- **修复/处理**：未重发DELETE；显式停止所属进程后锁定原库，核对目标独立图/向量映射及owner权限，使用固定官方delete_dataset等SDK方法清理。
- **已记录验证**：SDK43700退出0，target dataset/mapping及独立图/向量均退出；primary原7文档含两条已验知识保持。
- **未知/限制**：原公开DELETE结果未成功确认；不能将SDK替代收尾写成原HTTP请求成功。
- **后续指导**：超时不是取消；先查同worker，停止确认与独立映射校验后才可维护，禁止私删数据库或大范围drop。
- **计数**：未登记产品验收轮次；准备/观察事件1次。
- **证据**：[t5-invalid-code-cleanup.json](evidence/client-simplification-20261113/t5-invalid-code-cleanup.json)；[t5-official-scoped-drop.json](evidence/client-simplification-20261113/t5-official-scoped-drop.json)；`.local/daily/t5-delete-worker-sample.txt`（本机私有或仅原会话输出）；[执行记录：官方错误code dataset清理完成](client-simplification-execution-20261113.md)。

<a id="e19"></a>

### E19 · 同名重建dataset继承旧STARTED记录的风险

- **类别**：任务身份
- **当前记录状态**：防护已验证／未记实际失败
- **触发与范围**：错误dataset删除后以同名确定性ID重建
- **原始错误/现象**：旧pipeline0c7...仍STARTED，新dataset ID相同
- **影响与保留状态**：可能误挡新世代更新；未记录实际错误提交或新的一轮scope失败。
- **已证原因**：活动账本保留前一dataset世代；只按dataset ID会混淆创建前历史与当前活动。
- **修复/处理**：比较官方dataset createdAt和活动started_at/created_at；只忽略明确早于新世代的记录，缺失/坏时间及读取中重建保持未知。
- **已记录验证**：边界测试通过；第二轮Native未自动重发，手动更新可发起，真正当前活动仍阻止重复POST；旧0c于17:56:26UTC被官方恢复标abandoned。
- **未知/限制**：未记录独立剩余原因；不外推到其他版本或环境。
- **后续指导**：这是预防性风险和检查，不虚构一轮验收失败；不得私改旧任务终态来放行。
- **计数**：0轮。
- **证据**：[执行记录：原日常服务恢复与代码组重建](client-simplification-execution-20261113.md)；[internal/simpledesktop/client_test.go:TestRecreatedOfficialDatasetDoesNotAdoptHistoricalRun](../../internal/simpledesktop/client_test.go)；[internal/simpledesktop/client_test.go:TestDatasetRecreatedDuringActivityReadRemainsUnknown](../../internal/simpledesktop/client_test.go)。

<a id="e20"></a>

### E20 · 查看记录按钮接到错误官方缩放层级

- **类别**：图谱宿主
- **当前记录状态**：已修复
- **触发与范围**：真实图谱点击Records
- **原始错误/现象**：仍停schema聚类，未进入节点记录层
- **影响与保留状态**：按钮未进入记录层，画布有节点不代表该交互可用。
- **已证原因**：宿主调用level3，官方定义是plumbing；记录层是level2。
- **修复/处理**：宿主改调用官方level2，文案查看节点，不改vendor、布局或命中算法。
- **已记录验证**：TypeScript检查/build通过；MyGo重载后实窗展开并选中实际节点。
- **未知/限制**：未记录独立剩余原因；不外推到其他版本或环境。
- **后续指导**：以固定官方API含义接线，不能仅凭按钮名字或画布非空判通过。
- **计数**：1轮。
- **证据**：[执行记录：独立图宿主缺陷](client-simplification-execution-20261113.md)；[web/official-graph/src/main.tsx](../../web/official-graph/src/main.tsx)；[web/official-graph/vendor/cognee/src/modules/business/canvas/useBusinessCamera.ts](../../web/official-graph/vendor/cognee/src/modules/business/canvas/useBusinessCamera.ts)。

<a id="e21"></a>

### E21 · fake-owner回归夹具路径规范化不一致

- **类别**：工具与诊断
- **当前记录状态**：夹具已纠正
- **触发与范围**：测试stop不能仅凭锁释放就宣称成功
- **原始错误/现象**：身份核对提前拒绝，未走到预期残留owner断言
- **影响与保留状态**：回归未到达目标断言，不是所有权规则本身失败。
- **已证原因**：夹具传/var，而真实规范路径为/private/var，配置身份不匹配。
- **修复/处理**：夹具传规范路径，保留原失败为准备问题。
- **已记录验证**：test_stop_does_not_claim_cleanup_from_released_lock_alone最终通过；未改变真实身份校验以迁就夹具。
- **未知/限制**：未记录独立剩余原因；不外推到其他版本或环境。
- **后续指导**：区分准备错误与待测断言；不要放宽产品所有权校验来让测试过。
- **计数**：未登记产品验收轮次；准备/观察事件1次。
- **证据**：[执行记录：续接补充17:26UTC](client-simplification-execution-20261113.md)；[scripts/test_dev.py:test_stop_does_not_claim_cleanup_from_released_lock_alone](../../scripts/test_dev.py)。

<a id="e22"></a>

### E22 · 窗口重开清理EPERM向后台所有者传播

- **类别**：生命周期
- **当前记录状态**：已修复
- **触发与范围**：普通close后执行dev.py open
- **原始错误/现象**：stop_group的os.killpg EPERM；launcher finally退出并停止服务
- **影响与保留状态**：普通关窗保持后台，但open异常导致后台实际停止；同数据根恢复，未重放模型或索引。
- **已证原因**：UI清理异常未隔离，传播到持有后台的主循环；EPERM的确切内核时序未证。
- **修复/处理**：poll并核对group；EPERM后仅在确认消失时继续，活组拒绝信号仍失败；reopen异常只保留旧watcher/报告失败，不停后台、不启第二UI。
- **已记录验证**：7项回归后扩至9项；第二轮close/open原66705/66707/66710及health200全程不变，窗口同库可读，最后明确stop退出0。
- **未知/限制**：原EPERM内核原因仍未知；不能将退出竞争假设写为已证。
- **后续指导**：UI生命周期错误不能触发日常后台收尾；PID/锁/端口/健康/窗口实际结果分别留证。
- **计数**：1轮。
- **证据**：[d01-final-window-lifecycle.json](evidence/client-simplification-20261113/d01-final-window-lifecycle.json)；[d01-reopen-failure-recovery.json](evidence/client-simplification-20261113/d01-reopen-failure-recovery.json)；[d01-final-window-round2.json](evidence/client-simplification-20261113/d01-final-window-round2.json)；[scripts/test_dev.py:test_live_group_permission_failure_is_not_hidden](../../scripts/test_dev.py)；[scripts/test_dev.py:test_reopen_failure_reserves_watcher_without_stopping_services](../../scripts/test_dev.py)。

| 轮次 | 版本/配置或工具session | 任务身份 | 结果 | 累计失败 |
| --- | --- | --- | --- | ---: |
| 1 | tool session 96745 | 见本条回执 | 普通close后台保持；open抛EPERM导致launcher退出1 | 1 |
| 2 | tool session 27869 | 见本条回执 | close/open后台原PID与health保持，Native同库可读 | 1 |

<a id="e23"></a>

### E23 · 端口预检把TIME_WAIT误判为活动监听

- **类别**：生命周期
- **当前记录状态**：已修复
- **触发与范围**：明确stop成功后立即start29147
- **原始错误/现象**：OSError [Errno 48] Address already in use
- **影响与保留状态**：明确stop已成功，立即restart被预检查误拒绝；未创建空库或切换服务位置。
- **已证原因**：lsof无listener，netstat61255为TIME_WAIT；预检查普通bind语义与服务可复用bind不一致。
- **修复/处理**：预检查加SO_REUSEADDR，保持真实同地址listener冲突拒绝。
- **已记录验证**：两项真实socket回归与总9项启动器测试通过；随后同库start36423 ready，Native原正文和来源可读。
- **未知/限制**：未记录独立剩余原因；不外推到其他版本或环境。
- **后续指导**：TIME_WAIT不是进程未退出；不要删除锁、杀未知进程或换空库绕过。
- **计数**：1轮。
- **证据**：[d01-final-window-round2.json](evidence/client-simplification-20261113/d01-final-window-round2.json)；[scripts/test_dev.py:test_port_check_rejects_an_active_listener](../../scripts/test_dev.py)；[scripts/test_dev.py:test_port_check_allows_restart_after_server_closes_connection](../../scripts/test_dev.py)；[执行记录：最终收口](client-simplification-execution-20261113.md)。

| 轮次 | 版本/配置或工具session | 任务身份 | 结果 | 累计失败 |
| --- | --- | --- | --- | ---: |
| 1 | tool session 29147 | 见本条回执 | 无listener但TIME_WAIT使预检查拒绝，exit1 | 1 |
| 2 | tool session 36423 | 见本条回执 | 修正bind复用语义后同库启动，Native可读 | 1 |

<a id="e24"></a>

### E24 · 长Unicode内联文档脚本被Python拒绝解析

- **类别**：工具与诊断
- **当前记录状态**：改用文件脚本完成／传输原因未知
- **触发与范围**：本轮交付文档收口一次、本次错误文档生成两次，执行含中文的长heredoc Python源码
- **原始错误/现象**：SyntaxError: Non-UTF-8 code starting with ...; no encoding declared
- **影响与保留状态**：三次命令在解析阶段退出；当次脚本未执行写文件逻辑，原文已另行保留。
- **已证原因**：Python在解析输入源码阶段拒绝非UTF-8字节；未进入当次脚本写文件逻辑。字节为何在内联输入中异常未证。
- **修复/处理**：不继续重复长heredoc；用apply_patch写UTF-8文件脚本，再以ASCII命令执行；保留三次错误摘要。
- **已记录验证**：本次结构化JSON已生成；文件脚本完成Markdown后核对JSON、相对链接、SHA256及原文原字节。属于文档检查，不运行产品任务。
- **未知/限制**：具体shell/tool输入编码异常来源未查明；替代写文件方法不是根因修复。
- **后续指导**：生成脚本解析失败与文件写入失败分开；切换可检查文件输入，不猜测产品源码或数据损坏。
- **计数**：未登记产品验收轮次；准备/观察事件3次。
- **证据**：[documentation-generation-observation.json](evidence/client-simplification-20261113/documentation-generation-observation.json)。

## 官方/环境告警（不与产品失败合并）

### W01 · 官方认证配置

- **状态**：配置已固定
- **现象**：FASTAPI_USERS_*_SECRET未设置，随机进程secret使重启JWT失效
- **处理**：在私有配置固定secret，官方认证保留
- **未知**：不公开secret值；不外推其他服务实例。
- **证据**：[原记录：另行保留的运行告警](evidence/client-simplification-20261113/original-errors.md)；[执行记录：当前已验证与下一步](client-simplification-execution-20261113.md)。

### W02 · 固定上游告警

- **状态**：仍保留／未阻断启动
- **现象**：FastAPIDeprecationWarning: example参数弃用
- **处理**：未改固定官方源码消除告警
- **未知**：未来上游版本何时调整未核实。
- **证据**：[原记录：另行保留的运行告警](evidence/client-simplification-20261113/original-errors.md)。

### W03 · 固定上游退出告警

- **状态**：原因未知／进程退出另有证据
- **现象**：resource_tracker leaked semaphore对象，曾出现48/18/24等计数
- **处理**：核对所属进程组实际退出，不用告警数字推断存活进程数量
- **未知**：具体semaphore告警根因未结清。
- **证据**：[原记录：另行保留的运行告警](evidence/client-simplification-20261113/original-errors.md)；[d01-final-window-round2.json](evidence/client-simplification-20261113/d01-final-window-round2.json)。

## 能力边界与待决策项（不是已确认实现缺陷）

| 编号 | 主题/状态 | 事实 | 本记录不启动的动作 |
| --- | --- | --- | --- |
| B01 | 通用知识/用户确认的能力缺口 | 保留入口并显示官方暂不支持；后续独立dataset可供所有Agent读取；本轮未创建 | 只留档，不自动创建dataset或私有接口 |
| B02 | 分页与图谱源码来源/证据边界 | 分页是0/30/31/60/61 HTTP夹具，非>30真实数据实窗；图谱来源是官方路径/行号，不宣称本地编辑器跳转 | 不造生产数据或重复验收补材料 |
| D01 | 实体关系与代码结构共页/待设计决策／未实施调整 | 共用官方画布的页面组织为Agent实施选择；两者使用不同dataset。用户已询问合理性，尚未明确要求实施拆入口 | 不擅自调整UI |
| D02 | 1000节点上限/缺少决策依据／未实施调整 | 宿主请求和WebView校验自行设为1000；固定官方默认500。未有性能测试支持1000，非用户已确认产品规则 | 保留事实及当前代码位置，不自动调整上限或跑性能实验 |

## 后续追加格式

按[问题模板](incident-template.md)补充同一编号的轮次：时间与版本/操作ID、原目标、触发与原错、事实和未知、修复、复验、数据及进程影响、证据可用性、累计失败及停止决定。状态变化新增证据，不覆盖旧失败。

新增文件或摘录须更新JSON与证据manifest，核对相对链接及SHA256。记录本身不构成重跑业务任务的授权。
