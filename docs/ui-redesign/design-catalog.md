# 当前设计目录

2026-11-14最新增补：[通用知识、全局设置与项目设置](common-settings-redesign-20261114.md)。用户已授权重设计及实现，生产组件与隔离检查完成，外接屏实窗待验；本范围覆盖下文旧通用知识／设置布局及项目筛选空区设计，首页移除常驻刷新与无作用页脚。

2026-11-13。用户已选 [A 清晰总览](visual-refinement-selection-20261113.md)。当前整套细化入口为 [A 完整原型](http://127.0.0.1:8097/?variant=A&page=home)，固定提交及原型自验见[进度](visual-refinement-progress-20261113.md)，还原参数见[Native 交接](visual-refinement-handoff-20261113.md)。首页Native开发与Agent自验完成：按[视觉规范](visual-standards-20261113.md)修正字体、动态高度、返回及项目链接；最终知识顶线偏差1.5 DIP，进入既定2 DIP门槛，两尺寸实际截图、连续流程及历史失败见[首页执行记录](../engineering/overview-native-a-execution-20261113.md)。本版待用户审阅，不等同用户视觉认可。其余细化页沿用现有生产实现。

功能范围核对当前源码和[PA 执行记录](../engineering/project-access-execution-20261113.md)，不沿用旧目录的权限／通用知识只读删减，也不恢复旧 A–O 全量功能。原型所有内容和回执均为明确标记的人工静态示例，不连接业务服务。

## 2026-11-14 项目页与统一搜索候选

用户要求重做项目页，移除侧栏与列表搜索，统一顶栏弹框。本轮改用[带标注的图片与交互说明](project-reading-redesign-20261114.md)，包含两尺寸阅读页、Codex结构的浅色搜索弹框、内容信息浮层及状态板；同名SVG可编辑。用户已授权实施，源码与隔离检查完成，指定外接屏的真实Native验收尚未完成，见[实施记录](../engineering/project-reading-execution-20261114.md)。下方旧CSS项目／搜索入口保留历史，不再作为本轮新设计的还原依据；不代表其他旧细化页已获认可。

## 当前页面

| 页面 | 可打开入口 | 主要设计 |
| --- | --- | --- |
| 总览 | [home](http://127.0.0.1:8097/?variant=A&page=home) | A 宽幅方向、三项统计、稀疏热力图、最近知识／动态、项目网格 |
| 项目目录 | [projects](http://127.0.0.1:8097/?variant=A&page=projects) | 13 项目，长名称与同名不同路径 |
| 项目知识 | [knowledge](http://127.0.0.1:8097/?variant=A&page=project&p=p0&tab=knowledge&item=k0) | 三栏、列表、Markdown、复制、出处、返回恢复 |
| 原始记录 | [records](http://127.0.0.1:8097/?variant=A&page=project&p=p0&tab=records&item=r0) | 会话／文档、问答、工具折叠、长代码、已载入范围 |
| 搜索 | [search](http://127.0.0.1:8097/?variant=A&page=search) | 输入、项目及类型筛选、分组结果、片段、高亮、详情与返回 |
| 通用知识 | [common](http://127.0.0.1:8097/?variant=A&page=common&item=c0) | 个人偏好／通用经验、适用条件、来源、新增／编辑／删除及核对回执 |
| 图谱 | [graph](http://127.0.0.1:8097/?variant=A&page=project&p=p0&tab=graph) | 实体与代码视图、官方画布承载、显式开启与手动更新；画布未接入此原型 |
| 设置 | [settings](http://127.0.0.1:8097/?variant=A&page=settings&settings=service) | 服务、Codex 接入、默认规则、项目覆盖／继承与生效核对、新建／关联、待发材料 |

默认及最小窗口为1200×820，允许放大，不做窄窗口。总览及设置宽幅；项目知识、原始记录、通用知识采用导航／列表／正文三栏。无“全部记忆”入口；跨项目搜索仍在全局搜索。图谱保留官方画布边界，不自绘图引擎或伪造能力。

底部原型控制可检查首次加载、后台刷新、真空、无匹配、失败、失效、处理中、完成。生产必须依据真实官方字段表达，不能复制原型成功示例或统计数字。

## 历史资产

[2026-11-12 精简原型](../../build/p3-prototype/prototypes/client-simplification-20261112/index.html)、[原截图](../images/ui-redesign/client-simplification-20261112/)和[旧交接](full-design-handoff.md)保留历史。对应范围与样式被本次明确更新覆盖时，以本页及本轮交接为准；历史工程验收、失败计数和未完成项不改写。总览来源见[总览规格](../overview-spec-20261113.md)。

版本、审核、计划、多模型配置、维护平台及 release 设置仍不在本轮；本地材料上传、Codex 图片采集、新 Agent 接入与知识质量评估也不包含。

2026-11-14后续修正：各项目设置从左侧项目行右侧“⋯ → 项目设置”进入；会话标题在列表载入后预读，无需先点击。[实施与验证](../engineering/session-title-sidebar-settings-20261114.md)。
