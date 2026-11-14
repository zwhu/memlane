# 项目权限卡片收口

2026-11-14，源码基线`d916056`。用户连续明确：删除“已生效”、长说明、手动保存／恢复／撤销按钮；改为checkbox且不显示继承术语；采集和处理并入Agent读写权限；删除项目页tabs与重复项目名，按Agent卡片呈现；无外接屏时允许在自带屏验收。

## 最终行为

左侧项目行“⋯ → 项目设置”直接显示该项目各Agent卡片，仅有“允许读取知识”“允许写入和删除知识”。读取关闭时写入项不可勾选；读取开启为只读，写入开启为维护权限（read/write/delete，无share）。修改即时提交，忙碌时防重复。

一次权限修改使用原`PUT /v1/projects/{id}/overrides`与当前expected_revision，原子提交三字段：维护权限同时`capture=true,auto_process=true`；只读／不可访问同时false。没有独立采集／处理开关，也没有恢复默认操作。未知结果锁住该Agent选项，目录GET核对原修改，不自动重发。核对同时匹配访问级别和关联两项能力，未确认官方生效前不解锁。底层兼容字段和原有数据保留，本轮不重写引擎或批量迁移既有策略；用户实际修改权限时绑定这三项。

项目页移除tabs、重复项目标题、常驻生效状态、解释段落与底部操作栏。原待发材料恢复能力移到“设置 → 高级 → 未保存的会话”，用户主动展开后才可检查，不在日常权限页混排；不自动读取或补交材料。全局设置隐藏旧默认规则表单入口，其他连接／关联／高级能力保留。

## 验证

- 两尺寸MyGo Tester：省略号进入对应项目、读取／写入checkbox、旧tabs／继承文案／状态／按钮不存在、返回原pane与侧栏全局设置连续通过。
- HTTP夹具：读取关闭→开启→维护权限三次修改均正确携带目标项目／Agent／expected_revision及关联capture/process；不改单独字段，不提交share；模拟服务已保存但返回500，选项锁定，GET确认后解锁，没有第二次PUT。
- 全量Go测试通过（卡片候选5.008s；最终高级诊断仅改显示文案后再次全量检查）；定向race通过3.291s，vet与diff检查通过。证据目录[project-checkbox-20261114](../../../../engineering/evidence/project-checkbox-20261114)：根目录早期4checkbox，permissions-only为2checkbox但仍有tabs的阶段，cards-final才是最终无tabs候选。均为人工数据组件夹具，不是真实窗口截图。
- 实际Native：用户允许内建屏后，通过CUA在当前Memlane Dev实际1200×820窗口打开“memlane”行省略号→项目设置。最终AX仅含“本机 Codex”、两项checkbox（当前均勾选），无tabs、重复项目名、说明或底部按钮；已检查实际截图。原生证据在本次CUA工具回执中，另存[AX摘要](../../../../engineering/evidence/project-checkbox-20261114/native-ax.txt)，没有伪造本地Native PNG。高级诊断未做真实材料操作。
- 原生未改变日常权限值；真实写入行为由上述HTTP夹具及现有官方控制API契约分别证明。没有实际撤权／重授权、模型调用、真实知识写入、补采或重放。

方法问题保留：最初测试使用不存在的Tester.Render，改为SDK提供的Frame；随后用HasText("默认规则")误匹配说明句，改为精确文本判断。不是产品验收失败，不改写既有PA／PR／AN／E计数；用户改变控件、能力合并与tabs设计的反馈分别保留，不按整个页面合并凑四次。

旧launcher、completion、official、project_control和UI watcher继续运行，开发热重编译成功，不重启后端，不进入release。已有运行依赖、库和7份无关未跟踪文档保留，磁盘可用超过100 GiB。
