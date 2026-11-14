# 全界面官方组件替换

基线6198179。用户明确要求在项目管理任务后开始替换，范围覆盖布局、按钮、下拉、侧边栏、分段、拖放、滚动和分栏。继续固定MyGo v0.7.4、Native dev，不升级SDK或恢复release。沿用plan-change-impact及verify-continuous-scenarios。

## 流程与影响

侧栏选择页面／项目 → 官方分栏内筛选和阅读 → 编辑通用知识／退出确认 → 项目创建选文件夹 → 返回原阅读状态。只替换表现和交互承载，保留当前内容来源、草稿、来源返回栈、异步代次、权限、未知结果防重投。用户正在操作实窗时不插入点击或替其放弃草稿。

| 场景 | 当前情况 | 替换依据与边界 |
| --- | --- | --- |
| 导航 | 手工Button列表 | Sidebar、SidebarItem；保留项目⋯官方菜单和浅蓝主题 |
| 列表／正文 | 手工固定宽度Row | Split，保留初始300px，可键盘和拖动调整 |
| 分类／记录类型／搜索类型 | 手工一组按钮 | Segmented；页面页签使用Tabs |
| 普通动作／主动作／图标动作 | Button覆写外观、手写hover | Button、PrimaryButton、Toolbar，由统一theme管理状态 |
| 滚动 | 已是官方Scroll及TrackScroll | 保留；不为替换而重写同等官方能力 |
| 弹框／菜单／下拉／勾选 | 上轮已改官方组件，Select、Checkbox本来就是官方 | 继续复用，移除剩余TextInputBase，退出草稿采用AlertDialog |
| 拖放 | 当前没有产品内排序或文件上传流程 | 复用SDK现有DroppedFiles处理创建文件夹选择；仅接受目录，不新增文档上传、自动关联或写入 |
| 底部线条 | 侧栏、分页、阅读、编辑分别40／48／64px | 共用48px底栏与边框色，设置按钮移到状态底栏上方，不另造一条错位分隔线 |

## 验收先列

- U01 两尺寸1200×820及1440×900：三个底栏Y一致；字体、溢出、按钮和对齐实图核对。
- U02 Sidebar鼠标和键盘选择仍按项目ID定位，项目菜单不会误导航；官方Split改变宽度不丢分页、选中、滚动和草稿。
- U03 Segmented／Tabs切换保持原数据语义，表单Select与Checkbox仍官方，保存与未知结果保护不变。
- U04 AlertDialog取消／Escape保留草稿，明确放弃才清理；创建目录选择取消不提交，拖入目录仅填草稿。
- U05 源码检查、全量Go test/vet及有关race集中执行，合成图先打开，再真实Native核对；未验证能力单列，不以API存在冒充验收。

## 问题计数

底部线条错位首次用户验收失败1轮（03:01截图）；单独追踪，不合并整个UI反馈。原具体问题及停止次数保留。新组件引入的独立回归按症状分别登记，第四次同问题失败即停。


## 实施结果

- `overviewNavigation` 使用官方Sidebar／SidebarItem；项目菜单作为同一行的兄弟MenuButton，避免被AX树的导航项隐藏。主外壳在页面之间保持稳定Key，解决切换首页后键盘焦点丢失；底部设置使用Toolbar。
- 阅读与通用知识使用官方Split、List（Key、Selected、Label、TrackScroll）；分页、来源返回、草稿与异步请求语义保留。宽度保存在本次App状态，不新增磁盘配置。
- 记录类型、通用分类、搜索类型使用Segmented；项目视图和设置分类使用Tabs；按钮、主按钮、输入、Field、Select、Checkbox、Toolbar均使用官方组件状态。删除与放弃草稿采用AlertDialog，默认聚焦保留动作。Modal／Popover／PopoverMenuButton沿上一提交复用。
- 创建弹框使用官方DroppedFiles／FileDragOver，只接受一个本机目录并填草稿；文件、多目录、已关联项目、提交中均不改变绑定。不引入文件上传、排序或自动提交。
- 统一48px底栏和同色上边框；侧栏状态、列表分页、正文／编辑底栏共享基准。设置按钮位于状态栏上方。空阅读区也保留底栏。编辑动作统一居中。
- 业务内容仍需组合官方Row／Column等布局：热力图、知识正文、队列状态和官方图谱宿主没有等价通用控件，保留其业务绘制／承载；不以无关组件代替业务语义。当前simpledesktop无DialogBase／PopoverBase／TextInputBase等无样式控件调用，保留官方SDK及图谱组件，不新增第三方UI库或SDK补丁。

## 验证与证据边界

- U01：1200×820与1440×900合成布局逐栏断言底栏Y一致、高48并贴窗口底部；按钮中心一致。已打开 `evidence/official-ui-components-20261114/final/fixture-official-1200x820-editor-aligned.png` 核对。round-1保留初次候选，不能当最终图。
- U02–U04：定向检查覆盖官方Sidebar和List键盘选择、Split移动后草稿保留、AlertDialog Escape保留／明确放弃、目录拒绝文件／多目录／固定关联及不自动提交。原有知识保存未知结果、项目权限和名称改动保护回归继续保留。
- Go全量test、vet及定向race分开记录；本任务不重跑模型、索引或完整包。最终命令结果补在下方。
- Native自带屏已观察：官方Sidebar、Tabs／Segmented、表单、三栏底线和按钮居中，草稿AlertDialog打开／Escape返回，Split方向键右移和左移恢复，以及官方项目菜单打开／取消且保持原页面。最后窗口保留空编辑草稿用于审阅；未修改真实知识、权限或重放材料。真实1440×900实窗和Finder跨应用目录实拖没有执行，不把夹具或SDK接口存在当作该项已通过。
- 模型服务不随本轮UI替换重启；沿用前一任务已更新的dev后台与唯一UI watcher。没有release产物。

## 缺陷与方法记录

| 问题 | 失败记录与根因 | 修复及边界 |
| --- | --- | --- |
| 底部线条不齐 | 用户03:01截图，产品失败1轮；原40／48／64px及侧栏外边距不一致 | 统一48px，双尺寸断言与Native均核对通过，失败计数保留1 |
| Link构造后设置Key触发panic | 替换时源码UI检查1轮；官方Link已初始化状态，旧Button调用方式不能照搬 | Key移到Context构造前；内部动作最终统一官方Button，不保留自写hover |
| 首页切换后Sidebar键盘失焦 | 新键盘场景失败1轮；旧首页多一层独立shell改变节点身份 | 统一mainShell与稳定Key，定向检查通过 |
| 列表选中项对比不足 | 首轮合成图观察1轮；业务字体颜色覆盖了官方选中前景 | 选中项使用主题AccentText，已打开最终图核对 |
| 编辑底栏放弃按钮靠上 | 首轮Native观察1轮；旧projectLink的AlignSelf(Start)覆盖父栏居中 | 编辑栏内改Center，增加动作中心断言并Native核对通过 |
| 项目菜单未出现在AX树 | Native观察1轮；将交互菜单作为SidebarItem子内容使用，AX树导航项吞掉子按钮；仅设Expanded未解决，不作为通过 | 将官方MenuButton置为行内兄弟，实际AX显示menu button并可打开，最终观察通过；同一问题累计2轮失败，最终通过后失败计数仍为2 |
| 权限race夹具共享map | 新race检查暴露1次方法缺陷，原fixture浅复制使模拟HTTP服务与UI共享数据 | JSON解码到零值隔离所有slice/map；定向race通过，不据此认定生产保存逻辑竞态 |

编译阶段Toolbar签名、服务状态字段和函数提取范围错误均在编译检查修正，未发布为验收候选。测试里同名“知识”原先点到背景页签，改按官方搜索控件组定位；不把定位方法错误当作搜索业务失败。没有任何具体问题达到第4次失败门槛；既有历史问题次数不改写。


## 最终检查回执

- `sh scripts/go.sh test ./... -count=1 -timeout=120s`：退出0，simpledesktop 3.036s；cmd/memlane-app无测试文件。
- `sh scripts/go.sh vet ./...`：退出0。
- 相关组件／草稿／保存／删除／改名／权限race检查：退出0（2.202s）；随后将权限fixture解码目标明确改为零值，`TestProjectCheckbox` race再次退出0（1.889s）。只改夹具，不改变生产权限控制。
- 官方菜单兄弟节点及Toolbar最后一轮定向检查退出0（1.657s），最终Native AX已可识别项目MenuButton，实际打开／关闭成功。
- 运行余量约113GiB，未下载新组件或SDK；原后台和UI watcher继续保留。工作区出现其他会话的Python管理／接入改动和多Agent方案文档，不在本次组件提交中纳入或回滚。
