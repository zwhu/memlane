# Memlane 桌面 UI 规范与渲染护栏（`app/` & `kit/ui/`）

本文件为改动 `app/` 与 `kit/ui/` 时的常驻执行护栏；完整页面交互与原型以 [V4 视觉与交互规范](../docs/design/visual-standards-v4-20261120.md) 和 [MyGo 动效契约](../docs/design/motion-implementation.md) 为唯一事实源。

## 1. 布局与信息
- **风格与窗口骨架**：原生 macOS 专业工具感（Things 3 / Linear / Raycast），默认与最小窗口 `1200×820`；标题栏（`52px`）与内容区融为一体，不加额外灰色底壳或浮起面板。
- **禁止大框套小框与嵌套卡片**：阅读类页面（首页、项目/通用记忆/用户画像三栏）只用 `1px` 发丝分隔线与留白分区；配置型分组列表（设置、项目「管理」、服务）统一使用 `kit/ui.Group` 单层 `Fill1` 极浅底圆角容器（仅相邻行间 `1px` 发丝线，无首顶线、无末底线），严禁卡片套卡片。
- **信息降噪**：靠排版对齐、字级对比与留白区分层级；能自解释时不堆砌标语、副标题或解释性描述文字；原始记录始终展示可读会话主题，不暴露裸 UUID。
- **统一三栏几何**：项目、通用记忆、用户画像三栏共享 `SidebarW=220`、`ListPaneW=360`（顶栏 `36px` / 底栏 `48px`）、阅读栏正文最大宽 `680px`（顶部留白 `24px`）。

## 2. 视觉、色彩与留白
- **唯一色彩令牌源**：严禁在 `app/` 直接写 `ui.Hex` / `ui.RGBA`（`brand_icons/` 除外），全部复用 `kit/ui.Tokens`：
  - 浅色：`Bg #FFFFFF`、`Sidebar #F4F4F6`、`Fill1 #F5F5F7`、`Fill2 #EDEDF0`、`Border #E6E6EA`、`Text1 #1B1B1F`、`Text2 #55585F`、`Text3 #7A7D85`、`Accent #5E6AD2`。
  - 深色：`Bg #1C1C1F`、`Sidebar/BgElevated #242428`、`Fill1 #26262A`、`Fill2 #2D2D32`、`Border #2E2E33`、`Text1 #EDEDF0`、`Text2 #A8A9B0`、`Text3 #808189`、`Accent #7C86E8`。
- **颜色只承载含义**：中性灰阶为主 + 单一 `Accent`（仅用于选中态、主按钮、链接、未读点、进行中状态与热力图阶梯）；分类标签一律中性灰（`Fill2 + Text2`），严禁按分类配色；状态色（`OK`/`Warning`/`Danger`）只表状态；仅 Agent 品牌图标保留品牌色。
- **克制装饰**：禁止高饱和霓虹色与装饰性重阴影卡片。
- **分级留白节奏**：按容器层级使用 `kit/ui` 留白令牌（`PageInset=32`、`PageTopInset=24`、`PaneInset=24`、`ModuleInset=16`、`SectionGap=24`、`ControlGap=16`、`ListItemInset=8`、`GroupWrap Gap=32`），区块间留白明确大于区块内间距。

## 3. 字体、栅格与图标
- **字体与字重**：全局 UI 统一使用 `kit/ui.FontUI`（系统字体 SF Pro + 苹方，不内嵌第三方字体），代码与等宽指标使用 `kit/ui.FontMono`（SF Mono + `tnum`）。字重一律显式指定 `400 / 500 / 600`（CoreText 标题可用 `550` 对齐浏览器 `600` 观感），**禁止调用 `.Bold()`（700）**。
- **六级字号阶梯**：统一使用 `kit/ui` 常量，禁止散落魔法字号——`FSCaption=11`（元信息/标签/角标）、`FSFootnote=12`（摘要/按钮/菜单/次要说明）、`FSBody=13`（导航/列表标题/控件）、`FSReading=14`（阅读正文，行高 `1.8`）、`FSHeading=16`（区块标题/品牌名）、`FSTitle=22`（等宽统计数字）。
- **4px 间距栅格**：所有 `Padding`、`Gap`、`Margin` 严格取 4px 整数倍令牌（`Sp1=4` 至 `Sp10=40`）；仅两类显式例外：MyGo `.Gap(1).Dividers(1, tok.Border)` 1px 分隔线槽，以及 V4 规范定义的热力图 11px 格子 `3px` 间距。
- **圆角层级**：小元素与标签用 `RadiusSmall=4`（热力格 `2`），按钮/输入框/列表行/选中块用 `Radius=6`，下拉菜单/分组容器/浮层/弹窗用 `RadiusMenu`/`RadiusCard`/`RadiusPopover`/`RadiusDialog=8`。
- **图标规范**：保留内置 SVG 路径，统一经 `kit/ui` 的 `24×24` 透明外框（`IconBox=24`、`IconSVG`/`SolidSVG`/`IconFrame`）与 `1.5px` 线性描边绘制；常规显示尺寸 `16–18px`，侧栏选中项切为实心 `Accent` 图标。

## 4. 交互、动效与桌面端菜单
- **按意图显示次要控件**：行内复制与次要辅助操作默认透明隐藏（`kit/ui.HoverCopyAction` / `kit/ui.IntentReveal`），悬停或键盘聚焦时原位显现，保持布局稳定不抖动。
- **桌面端悬停浮层与多级菜单**：必须使用 `kit/ui.HoverIntent`，同时具备开启延时（`300ms`）、关闭延时（`200ms`）、安全三角区保护与键盘聚焦开启，禁止手写裸轮询定时器。
- **弹窗与危险操作契约**：统一使用 `kit/ui.Dialog`（`480` 确认 / `640` 表单详情）；删除类危险弹窗主按钮用 `Danger` 底色并写明具体动作，**禁止绑定 `Enter` 确认且必须拦截回车防击穿**。
- **真实状态驱动动效**：仅用 `kit/ui` 四档预设（`Smooth`/`Snappy`/`Bouncy`/`Fast`），动画只读取真实状态，绝不以动画结束宣告任务完成；必须遵循 `c.Preferences().ReduceMotion` 直接到终态。

## 5. 渲染验收（强制）
- **以真实渲染画面为准**：禁止仅凭代码计算属性或单测通过宣布 UI 改动完成。每次 UI 改动必须生成浅色与深色 `@2x` 真实截图并亲自读图验收，逐项检查：**① 对齐与基线、② 意外换行与截断、③ 留白节奏与层级、④ 遮挡与双外观对比度（WCAG AA）**。
- 截图输出到本机忽略目录 `docs/process/`（严禁入库或被正式 `docs/` 引用）：
  ```bash
  MEMLANE_CAPTURE_UI_DIR=/绝对路径/docs/process/ui-check \
    sh scripts/go.sh test -v -run TestCaptureVisualCompareScreenshots ./app
  ```
