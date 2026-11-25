# 首次启动向导原型（2026-11-25）

本目录是首次启动向导专注式重设计的可交互 HTML 原型，用于评审和对照实现，**不是生产实现**。规格以[视觉规范 V4 §7.7](../visual-standards-v4-20261120.md#77-首次启动向导2026-11-25-专注式向导重设计)为准，决议见[设计决策](../design.md)。它取代 [V4 原型](../visual-v4-prototype/)中的旧「首次启动」页面。

## 打开方式

```sh
# 在仓库根目录执行（原型引用 V4 令牌与 internal/simpledesktop 下的图标资产）
python3 -m http.server 18795 --bind 127.0.0.1
# 浏览器打开 http://127.0.0.1:18795/docs/design/onboarding-prototype/
```

顶部深色条是原型控制，不属于设计。可以切换外观、跳到任意一步，并切换以下场景：检测（检测到 4 个 / 一个都没有）、环境（需安装 / 已复用就绪 / 安装失败）、提炼 CLI（已检测到 / 未检测到）、验证（通过 / 较慢后通过 / 失败），以及「减少动态效果」。也可以用 URL 参数直接打开，例如 `?theme=dark&stage=process&verify=fail`。

## 文件

| 文件 | 内容 |
| --- | --- |
| `index.html` | 入口与原型控制条 |
| `onboarding.css` | 样式，仅使用 [V4 令牌](../visual-v4-prototype/tokens.css) |
| `onboarding.js` | 流程状态机与交互模拟 |
| `shoot.js` | 浅色、深色关键状态自查截图（playwright-core 加本机 Chrome），默认输出到本地忽略的过程目录 |
