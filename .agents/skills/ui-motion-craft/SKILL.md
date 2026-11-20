---
name: ui-motion-craft
description: Design, prototype, and faithfully port refined UI motion (springs, staggered entrances, hover/press micro-interactions, and flicker-free async loading) from interactive prototypes to native desktop or web apps. Use when building new UI transitions, fixing janky/flickering loading animations, or porting motion from HTML prototypes to MyGo/SwiftUI/native frameworks.
---

# UI Motion Craft

设计、验证并跨端还原克制、顺滑、零闪动的界面动效（适用于 HTML 交互原型与 MyGo / SwiftUI / Web 客户端）。

## 1. 动效档位基线（收敛为 4 档，禁止随手写魔数）

| 档位 | 参数（Duration, Bounce） | 曲线特征 | 适用场景 |
| --- | --- | --- | --- |
| **Smooth** | `0.35s, 0` | 临界阻尼，无过冲 | 页面切换进入、列表错开缓进、详情阅读区切换 |
| **Snappy** | `0.30s, 0.15` | 轻快微回弹 | 侧栏/列表/分段控件选中滑块、下划线标签、悬停抬起、弹层菜单 |
| **Bouncy** | `0.42s, 0.30` | 明显弹性反馈 | 开关圆钮、角标跳动、热力格点击回弹、新条目落入 |
| **Fast** | `120ms linear` | 纯线性短过渡 | 悬停背景色、边框色、分隔线与箭头显隐 |

- **跨端同曲线**：HTML 原型按弹簧微分方程采样 `linear(...)`；Native 端使用精确临界阻尼公式 `1 - (1 + ωt)e^(-ωt)`（`ω = 2π / 0.35`）或框架原生弹簧，确保两端手感一致。

## 2. 四条工程铁律

### 铁律 1：入场缓进只走合成层（Transform + Opacity），严禁补间布局宽高/位置
- 页面进入（`TranslateY: 8px → 0`, `Opacity: 0 → 1`）、列表错开（`TranslateY: 6px → 0`, `Opacity: 0 → 1`）、详情切换（`TranslateX: 8px → 0`, `Opacity: 0 → 1`）必须使用**纯视觉变换**，不改变布局占位。
- **固定锚点不动**：顶栏标题、顶部 Tab 栏、侧栏导航在切页或切子视图时保持固定，只让下方内容区缓进。
- **列表错开只给首屏**：仅对前 10–12 条可见行加递增延迟（`delay = index * 28ms + 60ms`），滚动区外行立即可见。

### 铁律 2：异步冷启动必须去闪动（杜绝三段跳变）
- **严禁在骨架屏、未就绪的裸 ID，或逐条流式回填时反复触发入场动画**。
- **固定骨架行高**：骨架占位与异步标题槽位必须与最终 2 行/3 行真实行高一致，避免数据回填时挤压后续虚拟化列表项。
- **SWR + 首批原子预读**：
  - 优先读取按身份隔离的本地轻量只读快照（SWR）实现冷启动秒开，后台静默刷新覆盖且不重复播入场动画；
  - 无缓存冷启动时，在有界预算（600–800ms）内并发预读首屏可见项（如前 10 条），就绪后**一次性原子替换骨架并仅播一次错开缓进**。

### 铁律 3：状态真实性与零空转
- 动画只读取真实业务状态：仅 `running` / `pending` 等真实进行中任务可播放循环动效，已完成、失败与历史记录（如搜索历史）必须保持静默。
- 绝不用动画结束来宣布异步任务完成；新条目落入动画只能在收到真实服务回执之后触发。
- 动画到达终态后立即停止请求下一帧（空闲态零 CPU/GPU 占用）；系统开启 `ReduceMotion` 时全部直达终态且自绘循环停帧。

### 铁律 4：细节联动不露破绽
- 悬停抬起带阴影的列表行，必须同时隐藏**自身顶部**与**下一行顶部**的分隔线（对应 CSS `.row:hover::before, .row:hover + .row::before { opacity: 0 }`），避免浮起卡片下方出现双线。

## 3. MyGo 落地避坑清单

1. **切页入场不要依赖子节点 `ElementTransition.Enter`**：MyGo 在父容器同帧创建时会抑制子节点的 `Enter`，导致切页错开动画不播放；且 `ElementTransition` 未限定字段时默认动画化 `Position + Size + Colors`，搭配 `Delay` 会在内容重排时引起延迟挤压。
   - **正确做法**：使用 `el.Keyframes(key, frames, KeyframeOptions{Run: run, Delay: delay})` 驱动 `el.Opacity(p)` + `el.Transform(ui.Translation(dx, dy))`，在导航/筛选/切日时显式递增对应 `run` 计数；`p >= 1` 后不再附加变换。
2. **入场与悬停可自然叠加**：`el.Transform(...)`（用于入场缓进）与 `el.AnimatePose(...)`（用于悬停 `-1px` / 按下 `0.99`）按合成顺序叠加，不会互相打架。
3. **热力图等高频网格**：不要在每次回到首页时对 52 列做半透明横扫，随整页 `PageEnter` 一起上浮即可；仅在交互点击/新入库回执时对单个格子播 `Bouncy` 缩放。

## 4. 执行与验收清单

1. **先做交互原型再移植**：在同一页面提供浅色/深色、`ReduceMotion` 开关与关键态触发（切页、筛选、新条目落入、空态），确认手感。
2. **逐项对照真窗验收**：
   - [ ] 冷启动首屏无「骨架 → 裸文件名 → 逐条跳变」闪动；
   - [ ] 切换子标签时顶栏与 Tab 栏纹丝不动，无 8px 下移跳动；
   - [ ] 悬停列表行浮起时上下分隔线均隐藏，无双线；
   - [ ] 历史记录/静态页零循环动画、无持续重绘；
   - [ ] 浅色与深色两套外观及 `ReduceMotion` 均在真实窗口核对通过。
