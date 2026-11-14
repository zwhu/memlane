# 会话标题预加载与侧栏项目菜单

2026-11-14，基线`4f35011`。用户指出会话标题只有点击后才更新，要求修复；项目设置最终明确放在左侧项目行右侧，以省略号菜单承载，不使用齿轮或顶栏重复按钮。

## 实施与连续检查

- 已证根因：reading_loading.startTitles只接受知识Document，Session被跳过。扩展同一读取调度器处理当前页会话，最多30条、4并发；异步回调沿用epoch／request／身份／权限代次保护，切页取消、失败可定向重试。不引入模型生成标题。
- 复用Client.read中官方首条问题提取sessionTitle。列表只更新标题和读取状态，不复制正文／工具明细。进程内会话预热缓存仅保留派生标题，不保存问答或工具；命中缓存仍返回该标题，未扩大已有128条缓存上限。点击阅读继续走原官方正文读取。
- 每个侧栏项目行由互不嵌套的项目按钮和“⋯”菜单组成。菜单“项目设置”捕获该行项目ID；进入时默认Agent规则，返回恢复进入前的阅读位置，正在设置的项目行保持选中。顶栏菜单保留项目路径能力，不重复放设置。
- TestSessionTitlesPreloadBeforeSelectionAndReuseTitleOnlyCache：不点击任何条目，后台得到标题；重复预热命中缓存、不再GET；列表及缓存没有问答和工具明细。
- TestSettingsProjectScopeDraftInheritanceAndNavigation：两尺寸通过左侧菜单进入项目规则、草稿／继承／材料页签／返回／全局设置的连续流程。
- TestSidebarMenuTargetsItsOwnProjectAndReturnsToOriginal：阅读项目A时打开项目B行菜单，进入B设置，返回仍为A原pane。

全量`sh scripts/go.sh test ./... -count=1 -timeout=120s`通过（4.859s）；标题及设置定向race通过（4.113s）。末次只更新入口提示文案、增加跨项目菜单断言，定向检查通过（1.030s）；vet、git diff --check通过。生产组件夹具见[evidence](../../../../engineering/evidence/session-title-sidebar-settings-20261114)，人工数据，不是实际Native截图；已查看1200×820项目设置及菜单展开图。源码由既有dev watcher重编译成功。

本轮用户报告的“会话仅点击后更新标题”记录为1次验收反馈，源码根因已证、隔离修复通过，真实窗口尚未验收；历史同问题如有登记须继承，不将本记录作为清零。入口位置属于用户设计调整，最终按侧栏省略号执行。

没有真实写入知识或修改日常ACL，没有重启后台、调用模型或进入release。原运行依赖与7份无关未跟踪文档保留，可用空间114 GiB。外接HP离线的实窗限制仍未解除，本轮未操作实际窗口，不把夹具通过称作实窗通过。
