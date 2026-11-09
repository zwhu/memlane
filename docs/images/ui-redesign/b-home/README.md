# B 日常首页：已认可截图与动效

2026-11-09，用户已认可 revision 5 的当前①宽幅首页。固定原型提交 `ed6cd37858c5d6f56f9307ecbf6ac454bb6e1edb`，分支 `prototype/selected-ui`。主布局为累计统计 → 全年热力图 → 最近知识与待确认并列，底部整理计划摘要。

- [正常首页](layout-1.png)、[少量知识](sparse.png)、[空首页](empty.png)、[只有原始记录](raw.png)、[导入知识](imported.png)。
- [所选日期](selected-day.png)、[零新增日期](zero-day.png)、[首页读取失败](failed.png)、[服务主动停止](stopped.png)、[窄窗口](narrow.png)。
- [按钮悬停](button-hover.png)、[整理中](button-running.png)、[完成](button-completed.png)、[实际渲染动图](organize-button.gif)。
- `list-*.png` 与 `*-handoff*.png` 保存标题／导航及首页承接的示意，不能作为完整 C–O 专项设计已认可的证据。

38 张 PNG、1 张 GIF 和两份浏览器核验 JSON 的固定来源与 SHA256 见 [归档清单](archive.json)。原始核验包含3个候选布局，归档截图排除②③；42帧动效是浏览器合成的交互演示。正式 MyGo 窗口、真实统计、Agent、模型与知识质量仍需工程验证。实施见 [B 交接](../../../ui-redesign/b-home-handoff.md)。
