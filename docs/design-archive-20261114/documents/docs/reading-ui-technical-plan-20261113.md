# UI 优化技术方案：知识阅读

日期：2026-11-13。状态：用户已启动dev实施，当前实现和未完成的RU-G真实搜索见[执行记录](engineering/reading-ui-execution-20261113.md)。下文保留方案规则，原“未验收”表述是方案阶段事实。配套：[产品](reading-ui-product-plan-20261113.md)、[规格](reading-ui-spec-20261113.md)。

## 1. 当前调用链与最小变更

现有 `Document.title()` 取label否则name，`documents()`仅请求目录；`Client.read()`按真实dataset／document读取raw并保存在内存；`App.selectItem()`会将读取后的标题更新回列表。`detail()`目前直接展示UUID和raw，出处直接展示session ID。相关源码：[client.go](../../../../internal/simpledesktop/client.go)、[app.go](../../../../internal/simpledesktop/app.go)。

本方案只改Go展示与只读加载，新增 `reading_presentation.go` 和 `reading_titles.go`，复用官方Client和已有pane request／epoch／context取消。少量接线位于read、load、selectItem和detail；不改官方库／插件，不新增模型或进程，不修改运行配置。

固定服务 `lesson_metadata()` 不包含完整statement，因此不能只读列表metadata就承诺全部标题可读。补标题必须取raw；只取本页缺少标题的条目。`label`可靠时直接使用，不额外取raw。[官方metadata](https://github.com/topoteretes/cognee/blob/ec6f40859e5f2df9e7862dde27d580ac3268ed43/cognee/modules/session_distillation/distill.py#L1005)

## 2. 内存模型与身份

`Presentation`包含 DisplayTitle、VisibleBody、WrapperRecognized、TechnicalFields；`Item.Body`继续保持官方原文，禁止为显示清理覆盖原文。pane保存按官方对象身份的标题状态与技术详情折叠状态，稳定UI Key仍为官方ID，不能换成显示标题。

标题缓存键为 `(service_namespace, credential_generation, dataset_id, document_id, content_hash, updated_at, policy_revision)`；本轮没有完整权限模块时用当前连接／凭据代次，并在配置更改清空。已有Document DTO补读取官方contentHash，字段缺失则以updatedAt；两者均缺失不跨目录刷新复用。新权限方案接入后加入其revision，不假定URL或token文件路径足以代表身份。

LRU最多128对象且原文合计最多8MiB，单原文缓存上限256KiB，超限可供当前详情读取，但不预热缓存。只在内存存原文与派生显示，不写磁盘。后台读取的正文均视为敏感内容，策略／身份撤销清对应缓存；不记录正文或用户目录到日志。

## 3. 确定性清理

纯函数 `presentKnowledge(Document, raw)`：

1. 非官方知识类型直接保持原文，不对普通文档／搜索片段做全局字符串替换。
2. 从metadata的node_set提取唯一 `session_learnings:<session>`，与metadata session_id存在时交叉核对；冲突则不清理。
3. 仅在raw开头的可选空行、连续 `Lesson-Type:`／`State-Key:` 元信息及准确 `# Session learning (session <id>)` 包装与已核对session一致时剥离前缀。类型值／状态键与metadata存在时也必须一致。重复字段、缺来源、冲突或未知模板保持原文，给“原文格式未识别”的轻提示。
4. 包装后所有正文按原顺序保留，包括用户自己的同名字段、Markdown标题、代码块、链接；不全局删除中途出现的字符串。

参考固定[官方解析器](https://github.com/topoteretes/cognee-integrations/blob/a176248914fdb32a78354854b300174203b15a5c/integrations/codex/plugins/cognee/scripts/_coding_memory.py#L1404)与renderer结构，只复用格式规则，不调用Python插件或模型。截图中Markdown将多个元信息行显示为一段，不代表应按渲染后的单行正则解析；始终基于官方raw行结构。

## 4. 标题提取规则

优先非空可读label，再取可读name；只将完整匹配已知机器模式 `text_[0-9a-fA-F]{32}`（可带已知.md/.txt后缀）或纯UUID视为无可读名称，不因长名称／英文就丢弃。

没有可读标题时，从VisibleBody取首个非空文本段，标题行可作文本；跳过完整fenced代码区域，去掉展示标题的行首Markdown标记、合并空白。按中文句号／问号／感叹号或ASCII句号后空白分句，最多80个rune，超限加省略号；不在路径里的`.`处截断。仅用于标题，正文不做这些文本化转换。只有代码／图片／空白时维持“知识 · 日期”，不生成虚构概括。

未知官方包装不能把 `Session learning`／技术元信息当内容标题：保留可读label/name，否则继续中性占位并显示原文，避免误删正文。重复首句允许重复，日期作辅助区分，不能加ID进主标题。英文原样，不调用摘要／翻译。

## 5. 当前页自动补标题

目录成功后立即渲染真实行数和占位标题，创建绑定当前service／project／pane／request的任务组。对最多30个本页对象，只为无可读标题且无有效缓存的项入队，最多4并发，每请求15秒；使用现有HTTP raw路径和X-Api-Key，不提供“只取第一句”的不存在接口。

成功按对象身份发布单条Presentation，不替换列表切片、不排序、不触发重新选中或重建ScrollState。失败只记该行failed，无自动循环重试；单条重试／用户刷新重新读取。切项目／页／查询取消任务，回调再次核对代次，即使网络取消后迟到也丢弃。

选中项读取优先：若同对象预热正进行，复用同一请求，避免重复raw；待执行项提升优先级。详情仍需按当前版本与权限确认，预热缓存不作为永远有效事实。当前页外请求不继续排队；已发请求尽力取消，不声称取消ACK证明服务端没读。

最近知识只补已显示的最多8项，由相同调度器消费，合计仍4并发；不改变总览统计／热力图扫描。全局搜索已有片段直接保留片段Body，名称命中机器格式时用片段首句展示；进入完整文档后再走该文档Presentation，不能把片段当全文缓存。

## 6. 技术详情、出处和复制

主标题下显示真实时间与知识类型，不再直出UUID。固定Data模型created_at／updated_at由UTC时间生成；显式偏移按其偏移转换本地，本固定SQLite输入丢失偏移的同字段按UTC解释后转换，未知来源的无时区时间不作这个假设。列表格式为本地 `YYYY-MM-DD HH:mm`，详情可显示完整日期与时区，原始时间串保留在技术详情。右栏正文后的“技术详情”默认折叠，放官方名称、dataset/document ID、session ID及已识别元信息；折叠状态按对象key保存在pane内，不借标题请求重置。[Data时间定义](https://github.com/topoteretes/cognee/blob/ec6f40859e5f2df9e7862dde27d580ac3268ed43/cognee/modules/data/models/Data.py#L72)

出处卡片默认“来源会话”，存在可信官方日期时追加日期；不为美化出处逐条请求session详情，也不编造会话标题或时间。点击沿现有source与返回栈；已有真实会话标题可复用，缺失不阻塞。

“复制知识内容”复制VisibleBody；“复制原文”从技术详情复制原始Item.Body；ID复制取对应原始字段。普通原始记录／文档的复制语义保持。原文依然可检查，不修改官方对象。

401／403／404或身份不符：清对应Presentation／raw缓存，撤下正文与复制；超时／5xx保留同身份最近已确认内容并显示错误，不将失败当空结果。权限方案生效后收到撤权事件，立即清对应数据组缓存及pane正文；已经进入模型上下文与系统剪贴板的内容不宣称被追溯撤回。

## 7. 验证、协作与交付

纯函数测试覆盖label优先、中文／英文截断、精确机器名、包装一致／冲突、代码／空正文和原文保留；HTTP场景覆盖4并发上限、取消／迟到回调、同对象请求合并和权限失效。这些断言验证数据误删和并发风险，不为每个样式常量写测试。

真实官方只读核对截图同类知识的列表／raw身份，再Native连续执行自动补标题→选中→复制→技术详情→来源→返回→刷新。实际打开截图对照1200×820，证据区分源码、HTTP与Native。无需模型／索引，也不为演示改真实数据。

本轮已实际查看当前首页与知识三栏稿；未生成新原型或新Native截图，不报告视觉验收完成。受影响的E12标题溢出与E16返回栈历史计数继续保留，新的具体问题按原规则登记，第4次仍失败停止。

另一session拥有总览实现及app.go／client.go接线，本部分实施前必须收到文件所有权交接。优先独立模块，复用统一标题结果但不修改总览算法。启动／停止服务、完整App包和数据迁移均不属于本方案。
