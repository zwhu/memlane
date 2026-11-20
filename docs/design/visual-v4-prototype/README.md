# Memlane 视觉 V4 原型（2026-11-20）

本目录是可交互的 HTML 原型，用于评审设计方向，**不是生产实现**。访谈结论见对话中的 redesign-decisions；用户确认后，再依据本原型编写「视觉规范 V4」并修改 MyGo Native。

## 打开方式

```sh
cd docs/design/visual-v4-prototype
python3 -m http.server 18790 --bind 127.0.0.1
# 浏览器打开 http://127.0.0.1:18790/
```

顶部深色条是原型控制，不属于设计。它提供以下操作：切换浅色／深色／跟随系统；切换总览／项目页；项目页在**卡片**与**三栏**两种布局间切换对比；模拟一次新记忆入库；重放入场动画；打开「减少动态效果」。也可以用 URL 参数直接打开，例如 `?theme=dark&page=project&layout=three`。

## 结构依据（2026-11-20 核对当前 Native）

- 侧栏依次为：总览、通用记忆、用户画像；项目区标题行带「全部 N」和「＋」；未读数字点击后进入该项目并自动打开「仅未读」。
- 全局搜索「搜索记忆与原始记录 ⌘K」放在标题栏；项目页标题栏另有「项目设置」。内容区不再浮在一块面板上，直接与标题栏融为一体。
- 记忆没有单独的标题，以正文作为内容；正文中的代码路径用等宽字体显示，并在 `/`、`_`、`.` 处断行。每条记忆显示召回次数和官方分类（项目状态、踩坑经验、项目约定、代码位置与关联、代码变更、约束条件、其他）。
- 卡片布局下，点开卡片会弹出详情；三栏布局下，详情显示在右侧阅读栏。
- **筛选（已确认）**：列表头一行工具条，包括「分类」下拉（带条数）、「仅未读」开关和排序下拉（按时间／按召回次数）；不放文字搜索框，关键词搜索交给全局 ⌘K。从近期动态里的召回事件点进来时，显示「本次召回 · N 条」筛选条，并提供「显示全部记忆」和「返回动态」。

## 文件

| 文件 | 内容 |
| --- | --- |
| `tokens.css` | 设计令牌：6 级字号、4px 间距、圆角 6、浅色／深色两套颜色、热力梯度 |
| `app.css` | 组件样式与动效 |
| `app.js` | 交互逻辑；弹簧曲线按 MyGo `SpringOptions{Duration, Bounce}` 的语义生成 |
| `data.js` | 合成示例数据 |
| `shoot.js` | 自查截图脚本（playwright-core + 本机 Chrome） |

## 动效与 MyGo 能力对照

| 原型效果 | MyGo v0.7.4 对应 |
| --- | --- |
| 弹簧预设 smooth（0.35s, bounce 0）／snappy（0.3s, 0.15）／bouncy（0.42s, 0.3） | `ui.Smooth` / `ui.Snappy` / `ui.Bouncy`，`Element.Spring` |
| 侧栏选中块、列表选中块、分段滑块弹性移动 | 对 `Translate` 目标值使用 `Spring` / `AnimateTo`，目标变化时保留速度 |
| 按下微缩、悬停抬升 1px 并加阴影 | `AnimatePose` + `Shadow` |
| 切换页面或文档：淡入并位移 6–8px；列表错开入场 | `Keyframes` + `KeyframeOptions.Delay` |
| 热力图按周生长、点击缩放、今日格脉冲 | `Keyframes` 逐格延迟；`Scale` |
| 新记忆「落入」：行高展开、下落回弹、背景闪烁淡出 | `Timeline` / `Tween`；动画只在服务回执之后开始 |
| 整理中方块舞台、空态漂浮、指针视差 | `Draw` + `c.Now()` + `AnimationFrame()`（运动时才请求下一帧）；`PointerPosition` |
| 侧栏半透明材质 | `Window.SetVibrancy` / `Painter.EffectBackdrop` |
| 深色跟随系统 | `Theme.Dark` / `DarkTheme()`，窗口 `isDark` |
| 减少动态效果：直接跳到终态 | `c.Preferences().ReduceMotion` |

以上只核对了接口是否存在，尚未在 Memlane Native 中实测。
