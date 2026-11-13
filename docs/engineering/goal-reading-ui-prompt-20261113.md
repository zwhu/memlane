# UI 优化 Goal 执行 Prompt

目标 session：`01a1d30a-f0f6-7bc2-a0a9-14e3c819845f`。工作目录：`/Users/bytedance/work/AI/memlane`。

用户已明确授权你使用 Goal 模式，依据本session整理的UI阅读方案实施并完成自验。本消息覆盖方案“仅文档／未启动”的阶段状态，只启动本范围，不恢复历史全量Goal、不进入release。直接执行，不停在计划或只给代码建议。

最新总览交接：总览session已提交 `4db3b8b` 并明确释放 `app.go`／`client.go`／`scripts/dev.py` 源码所有权；你仍需与项目session确认本prompt规定的先后接线。保留 `overview*.go`、新增配置字段及失败诊断，不把总览已交付内容覆盖掉。账本为 `docs/engineering/overview-execution-20261113.md`。OVR01后台stderr失效仅已定位、尚未修复，后台未重启；现有UI watcher只供源码重载观察。Native操作和后台所有权仍需核对，源码释放不等于可任意启停服务。

## 1. Goal与输入

先简短说明动作，读AGENTS与当前工作树。用 `get_goal` 核对本线程：无未完成Goal时 `create_goal`，objective为“实现并自验Memlane知识阅读UI优化：当前页自动补标题、固定包装清理、技术信息折叠、可读出处与正确复制，完成RU规格和实际Native连续验收，dev模式”。用户没有指定预算，不设置token_budget。已有同范围Goal则继续，不恢复无关旧Goal；不兼容Goal保留原状态并报告具体阻塞，不重复建Goal。

完整阅读：

1. `docs/reading-ui-product-plan-20261113.md`
2. `docs/reading-ui-spec-20261113.md`
3. `docs/reading-ui-technical-plan-20261113.md`
4. `docs/project-knowledge-feasibility-20261113.md`（范围入口）
5. `docs/product.md`、`docs/design.md`、当前设计目录、`docs/development.md`、精简版交付、分类错误及JSON。

采用三份新文档中的明确方案，当前页自动读取缺标题正文，不要求用户逐条点；不额外请用户重复确认。官方固定格式与源身份核对见能力研究文档及源码，勿将未知格式误删。使用仓库plan-change-impact与verify-continuous-scenarios，先在 `docs/engineering/reading-ui-execution-20261113.md` 记录主流程、影响与验证顺序。

## 2. 实施范围

- 优先官方可读label/name；明确机器名才触发当前页raw读取，按清理后首句取标题，不调用AI或翻译。
- 仅当前页30条与当前已有最近知识最多8条，统一4并发、15秒单请求超时；只为缺少标题且无有效缓存的对象读取。先占位后逐条补齐，无需逐条点击。
- 知识类型、唯一来源session、metadata与固定包装一致才剥离Lesson-Type／State-Key／Session learning头；正文、代码、链接原样保留，未知／冲突留原文。
- 原始Item.Body保留；VisibleBody与DisplayTitle单独派生。dataset／document UUID、原始名称及机器字段折叠在技术详情，不放主阅读标题。
- 来源显示可理解的来源会话和可靠时间，真实ID跳转、嵌套返回仍保持；不为美化来源乱发额外详情请求。
- 复制知识内容=可见正文；复制原文=官方原始内容，权限／来源失效撤下复制入口。
- 有限内存缓存按服务／凭据代次、对象及版本隔离，切页／项目／查询取消且丢迟到回执；身份失效清缓存，重载保留选择与滚动。
- 不修改官方数据、不改插件、不迁移、不新建索引／正文副本／模型任务。总览统计、热力图／动态和图谱不属于你的范围。

## 3. 并行共享文件与运行所有权

项目权限管理session：`01a1d30a-d776-7f92-a7e9-9424e5fabc13`。总览session：`01a1d2d2-da26-7561-8cde-ff417eb20b5f`。方案发起者：`01a1d2cc-3ede-7e52-985d-ee29705cc8b7`。

先给总览和项目session发边界／交接消息，取得共享文件与运行环境回执后修改。顺序为：

- 总览当前拥有 `app.go`／`client.go` 总览接线与运行验收；你先确认其共享接线窗口已释放。
- 你取得后优先新增 `reading_presentation.go`／`reading_titles.go`及测试，再对app.go/client.go作阅读相关小范围接线，保留总览结果。项目session同期做独立控制／策略／插件模块，不得同时改这两个文件。
- 完成你的共享接线和必要Native自验后，明确向项目session发送版本、文件清单、未提交修改及共享文件释放回执；此后项目session才能做其设置／权限接线。若还有独立测试／记录可继续，但别默默重开共享修改。
- 不改scripts/dev.py／组件清单／日常Agent配置；Native窗口和watcher使用先向总览取得排他操作窗口，不擅自启停官方服务。
- 总览最近报告其日常官方服务图worker启动时stderr BrokenPipeError，属于它观察到的独立问题线索；不要为标题验证重放模型／索引或用重启掩盖它，读最新记录并协调真实运行边界。

等待回执时继续独立模块和源码／协议验证；不要覆盖他人修改或在冲突共享文件上并写。用户已授权必要协作消息，无需再问能否联系session。

## 4. 设计、Git与执行规则

实际打开当前首页与三栏知识稿，以及本session截图问题对应记录；先复用真实设计资产，不仅看Markdown路径。保持默认及最小1200×820、三栏、紧凑排版与既有滚动／工具展开。当前稿示例不等于生产视觉完成。

工作树有总览源码和其他session文档。保留无关改动；不reset/clean/stash全树、不git add .。提交仅限你明确拥有的文件和已交接的共享改动；不包含密钥、聊天、数据库。需checkpoint/tag时先按仓库规则和实际文件所有权安排，不能把其他任务源码当自己的快照提交。

统一 `sh scripts/go.sh`，必要检查通过既有task.py磁盘门禁，至少100GiB，运行监测105GiB。使用MyGo dev增量调试，启动/重开依现有dev.py入口且先核对owner。不做完整App、release、helper、签名或数据清理，不删除build及未知依赖。

## 5. 验证与完成

实现RU01–RU12并通过RU-A至RU-G。纯函数／HTTP检查证明不误删内容、并发上限、请求合并、版本缓存和迟到结果；再用真实官方只读及实际Native执行列表自动补标题→选中→正文／技术详情→两种复制→出处→逐层返回→刷新／切页。至少覆盖可读label、机器名、中文／英文长句、空白／代码、未知／冲突格式、403/404/超时与切页取消。测试次数与真实产品验收轮次分别记，不用夹具替代Native。

实际查看截图对照，不将命令成功当视觉完成。保留E12标题溢出、E16来源返回等既有计数与证据边界；同一具体问题第4次失败停止该问题并汇报，不改名／换路径重置，其他独立工作继续。

更新本范围执行账本，记录源码检查、官方读取和Native各自结果、未验证范围和资源交接。自验通过才给用户验收步骤；未实际完成不要Goal complete。完成后向项目与总览回报共享文件／窗口释放、标题展示API以及权限代次失效接线点，避免项目权限后续接线绕过UI缓存失效。不需要等待项目整组功能全部完成才结束本UI Goal，但你范围的拒绝／失效契约必须可验证。
