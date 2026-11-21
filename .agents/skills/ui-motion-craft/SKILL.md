---
name: ui-motion-craft
description: Design, prototype, and faithfully port refined UI motion (springs, staggered entrances, wave grow, hover/press micro-interactions, and flicker-free async loading) from interactive prototypes to native desktop or web apps. Use when building new UI transitions, fixing janky/flickering loading animations, or porting motion from HTML prototypes to MyGo/SwiftUI/native frameworks.
---

# UI Motion Craft

设计、命名、验证并跨端还原克制、顺滑、零闪动的界面动效（适用于 HTML 交互原型与 MyGo / SwiftUI / Web 客户端）。

## 1. 标准动效命名词典（所有交互动效必须使用统一命名）

| 标准名称（英 / 中） | 档位 | 运动轨迹与参数 | 典型使用场景 |
| --- | --- | --- | --- |
| **Page Rise / 页面上浮入场** | `Smooth` | `TranslateY: +10px → 0`, `Opacity: 0 → 1`（单调从下往上，无回弹） | 首次启动与切页时的页面主体内容区（顶栏/Tab 栏保持固定） |
| **Stagger Cascade / 列表错开缓进** | `Smooth` | `TranslateY: +8px → 0`, `Opacity: 0 → 1`, `delay = i * 28ms + 40ms`（仅首屏前 8 项） | 首页「最近记忆 / 近期动态」、项目列表、筛选/切日后刷新 |
| **Wave Grow / 热力图波浪弹性生长** | `Bouncy` | `Scale: 0 → 1`（欠阻尼弹性轻弹）, `Opacity: 0 → 1`, 按列递增 `w * 7ms`（从左到右涟漪绽放） | 总览热力图首次加载与切回总览入场（单 Canvas 自绘，跑完自动停帧） |
| **Cell Tap / 热力格弹性选按** | `Bouncy` + `Fast` | `Scale: 0.6 → 1.0`（选中与取消均弹回）+ 1.5px 背景间隙 + 1.5px 强调色双环淡入淡出 | 点击热力图日期格筛选或取消筛选 |
| **Doc Glide / 阅读区侧滑切换** | `Smooth` | `TranslateX: +8px → 0`, `Opacity: 0 → 1` | 三栏阅读区切换选中记忆或切入原位来源会话 |
| **Slide Indicator / 弹簧滑块指示** | `Snappy` | 仅 X 或 Y 轴弹簧滑动，位于文字图层严格下方 | 侧栏选中背景块、下划线标签指示条、分段控件滑块 |
| **Hover Lift / 悬停轻抬** | `Snappy` + `Fast` | `TranslateY: 0 → -1px` + 柔和阴影 + 隐藏上下双分隔线 | 列表可点击行悬停反馈 |
| **Press Tap / 按压微缩** | `Snappy` | `Scale: 1 → 0.99`（行）或 `0.96`（按钮）或 `0.92`（图标按钮） | 按钮、导航项、列表行按下触感 |
| **Drop In / 新条目弹性落入** | `Bouncy` | `TranslateY: -16px → 0`, `Scale: 0.98 → 1`, `Opacity: 0 → 1` + 强调色闪光淡出 | 收到真实服务回执后的新记忆入库 |
| **Floating Parallax / 空态漂浮视差** | `Smooth` | 三方块持续轻缓正弦漂浮 + 指针反向视差（`ReduceMotion` 下静默停帧，不中途卡停） | 无数据空态插画 |
| **Stage Pulse / 进行中舞台脉冲** | `Bouncy` | 3×2 迷你方块依次落入跳动（仅真实进行中任务播放，历史/完成态绝对静默） | 动态流中的「整理中 / 已启动」任务 |

## 2. 动效档位基线（收敛为 4 档，禁止随手写魔数）

| 档位 | 参数（Duration, Bounce） | 曲线特征 | 适用场景 |
| --- | --- | --- | --- |
| **Smooth** | `0.35s, 0` | 临界阻尼，严格单调无过冲 | `Page Rise`、`Stagger Cascade`、`Doc Glide` |
| **Snappy** | `0.30s, 0.15` | 轻快微回弹 | `Slide Indicator`、`Hover Lift`、`Press Tap`、下拉菜单 |
| **Bouncy** | `0.42s, 0.30` | 明显弹性反馈 | `Wave Grow`、`Drop In`、开关圆钮、角标跳动、热力格点击 |
| **Fast** | `120ms linear` | 纯线性短过渡 | 悬停背景色、边框色、分隔线与箭头显隐 |

- **跨端同曲线**：HTML 原型按弹簧微分方程采样 `linear(...)`；Native 端使用原生弹簧或精确解析解（临界阻尼 `1 - (1 + ωt)e^(-ωt)`、欠阻尼 `1 - e^(-ζωt)(cos(ω_d t) + (ζ/√(1-ζ²))sin(ω_d t))`），保证两端手感与方向完全一致。

## 3. 四条工程铁律

### 铁律 1：合成层优先（Paint-Only），严禁动画化布局或每帧重建整棵视图
- `Page Rise`、`Stagger Cascade`、`Doc Glide`、`Slide Indicator` 必须走纯绘制合成通道（`Transform` / `Opacity`），不得补间布局宽高/位置，不得在每帧触发全量 View 重建。
- **固定锚点不动**：顶栏标题、顶部 Tab 栏、侧栏导航在切页或切子视图时保持固定，只让下方内容区缓进。
- **首次启动与切页一致**：初始化时即武装 `run = 1` 与入场时钟，确保冷启动首开与切页回访拥有同样的入场体验。

### 铁律 2：异步冷启动去闪动（杜绝三段跳变）
- 严禁在骨架屏、未就绪裸 ID 或逐条流式回填时反复触发入场动画；骨架占位与异步标题槽必须固定行高，防止挤压后续列表。
- 本地应用优先将首屏多接口收拢为**单次只读聚合接口**（`<50ms` 直出真实数据与首屏标题预览，无需落盘 SWR 副本）；仅在远端高延迟场景下使用只读 SWR。无缓存异步回填时必须为标题槽锁定固定 2 行高度，避免回填时把下方列表一路撑开。
- 热切回数据仍新鲜的页面时，不在 500ms 入场动画中途立即重发全量网络请求打断帧节奏。

### 铁律 3：状态真实性 & 空闲零空转
- 仅 `running` / `pending` 等真实进行中状态播放 `Stage Pulse`；已完成、失败与历史记录（如搜索历史）绝对静默。
- 所有动画到达终态后必须立即停帧（空闲 CPU `0.0%`）；系统开启 `ReduceMotion` 时全部直达终态且停帧。
- 高密网格（如 53×7 热力图）合并为单个自绘 Canvas 节点（500 节点 → 1 节点），在 `Draw` 内完成 `Wave Grow` 与 O(1) 坐标命中。

### 铁律 4：细节联动不露破绽
- `Hover Lift` 列表行浮起时，必须同时隐藏**自身顶部**与**下一行顶部**分隔线，避免浮起卡片下方出现双线。
- `Slide Indicator` 背景滑块必须声明在文字之前且处于同一普通流绘制层，防止遮挡标签文字。

## 4. MyGo 落地避坑清单

1. **优先 `AnimatePose` 仅重绘通道，避免 `Keyframes` / `Spring` 每帧重建整棵 View**：
   - `el.Keyframes(...)` 和 `el.Spring(...)` 每帧调用 `c.AnimationFrame()` 会导致全量 `App.View` 重建与布局，在 120Hz ProMotion 屏上极易掉帧。
   - **正确做法（两帧启动 + 仅重绘合成）**：第 1 帧用 `AnimatePose(startPose, v4Smooth)` 定位起点并请求 1 次重建；第 2 帧切到 `AnimatePose(restPose, Animation{Spring: v4Smooth.Spring, Delay: delay})` 后不再调用 `AnimationFrame()`，交由 MyGo `repose()` 以纯 GPU 120Hz 跑完弹簧。
2. **严禁用 `Duration: 1ns` 重置已有节点的起点（防「先上去再下来」过冲）**：
   - MyGo 对曲线动画会在结束时按 `span / duration` 计算速度并传递给后续弹簧；用 `1ns` 瞬移会产生极大瞬时速度，导致元素反向冲过终点再掉回来。
   - **正确做法**：把 `run` 编入元素的 `c.Key(...)`（如 `fmt.Sprintf("overview-page-%d", run)`），确保每次新 `run` 的第 1 帧 `r == nil`，以 `velocity == 0` 干净起跳，临界阻尼 `Smooth` 严格单调从下往上进入。
3. **滑块背景层级与稳定组件树**：MyGo 中 `Absolute()` 子节点绘制在普通流子节点之上；`Slide Indicator` 滑块应放在前置 `Size(0, 0)` 普通流容器内（不加 `Absolute()`），配合 `AnimatePose` 即可严格画在文字下方且 120Hz 仅重绘滑动。同时切选中态时**不要条件增删 `ui.Themed` 外壳**，保持一致子树与稳定 `c.Key(...)`，且 `itemBox.H == 0` 时保留上一次有效坐标，防止首帧 `Bounds() == 0` 导致滑块跳飞到负数坐标。

## 5. 执行与验收清单

- [ ] 首次启动与切页均正确播放 `Page Rise`、`Wave Grow`（从左到右）与 `Stagger Cascade`（从下到上单调进入，无「先上去再下来」）；
- [ ] 冷启动首屏无「骨架 → 裸文件名 → 逐条跳变」闪动；
- [ ] 切换子标签时顶栏与 Tab 栏纹丝不动，`Slide Indicator` 不遮挡文字；
- [ ] 悬停 `Hover Lift` 列表行浮起时上下双分隔线均隐藏；
- [ ] 空闲态与历史记录页 CPU 回到 `0.0%`，单帧构建 `< 16ms`，`ReduceMotion` 全部直达终态；
- [ ] 浅色与深色两套外观均在真实窗口核对通过。
