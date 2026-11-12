# 当前设计目录

2026-11-12。当前目录仅包含[精简版规格](../client-simplification-spec-20261112.md)中的页面。旧全量设计的编号、数量与固定稿不再构成本阶段交付清单。

统一打开[精简版原型](../../build/p3-prototype/prototypes/client-simplification-20261112/index.html)，图片见[当前截图目录](../images/ui-redesign/client-simplification-20261112/)。这些入口只承载设计示例；实际数据和生产能力仍以工程结果为准。

## 页面清单

| 页面 | 原型入口 | 内容与主要操作 |
| --- | --- | --- |
| 总览 | [home](../../build/p3-prototype/prototypes/client-simplification-20261112/index.html?scene=home) | 项目、最近知识、服务与模型状态；进入项目、打开知识、查看连接状态 |
| 项目知识 | [knowledge](../../build/p3-prototype/prototypes/client-simplification-20261112/index.html?scene=knowledge) | 官方列表、Markdown 正文、分页、搜索、复制、出处 |
| 原始记录 | [records](../../build/p3-prototype/prototypes/client-simplification-20261112/index.html?scene=records) | 会话或文档、问答、工具展开、复制已载入内容、返回知识 |
| 搜索 | [search](../../build/p3-prototype/prototypes/client-simplification-20261112/index.html?scene=search) | 官方结果、项目分组、查询与真实来源跳转 |
| 通用知识 | [common](../../build/p3-prototype/prototypes/client-simplification-20261112/index.html?scene=common) | 复用只读阅读，按官方实际类别区分个人偏好与通用经验 |
| 图谱 | [graph](../../build/p3-prototype/prototypes/client-simplification-20261112/index.html?scene=graph) | 官方实体图与代码结构；选择、缩放、项目索引开关与手动更新 |
| 设置 | [settings](../../build/p3-prototype/prototypes/client-simplification-20261112/index.html?scene=settings) | 官方服务、Codex 当前状态、必要项目关联与开发入口说明 |

项目知识和原始记录仍是同一项目的两个内容页签；图谱是该项目的视图。通用知识表示跨项目参考，不把项目知识自动提升为通用知识。跨项目搜索保留在全局搜索，不增加“全部记忆”入口。

## 共用状态与交互

- 默认和最小窗口均为 1200×820，允许放大；首版不包含窄窗口或响应式布局，不维护多尺寸适配矩阵。
- 首次加载、真实空结果、无匹配、刷新、读取失败和内容失效分别表达。
- 刷新保留正文、选择、滚动与展开状态，读取失败不显示成空库，不将计数归零。
- 来源跳转后恢复原知识及原阅读位置；无法精确定位时打开实际关联材料并说明范围。
- 未提供的字段省略或显示未知，不补造进度、数量、时间或成功状态。
- 图谱沿用官方组件，不自绘布局，不将代码静态关系描述为完整运行时影响。
- 代码图谱首次显式开启时调用官方索引，后续手动更新。关闭后不发起新扫描；取消依官方实际能力处理。

## 当前范围的删减

设计中不再保留手动新增与编辑、版本历史与恢复、审核建议、复杂整理、定期计划、项目或 Agent 权限、多模型方式切换、配置历史、维护平台及 release 设置。相关按钮、跳转和独立状态页一并退出当前稿。

首页不再因旧布局而保留待确认、计划或使用账本。热力图、动态等只有在后续直接映射到可靠官方字段时再加入，本轮没有相应空壳卡片。

页面详细交接见[界面交接](full-design-handoff.md)。
