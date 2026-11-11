# E06／L02 最小用户确认 UI 工程记录

2026-11-11。设计基线为固定 E06 普通/窄窗与最新 `project-preference-evidence-contract-20261111.md`；已实际打开两张固定图和本片1200/800 headless输出。生产 backend schema29 已由主Agent集成，本片 desktop 在 `build/goal-project-preference-ui-20261111/` 隔离实施，未直接改公共生产文件。

完整实施、字段、版本/范围/库门禁与局部补丁见该目录 README、diff.patch、app-hooks.patch、base-sha256.json。实现允许用户从当前可读个人偏好真实分页/筛选结果中明确选一条，与当前项目知识完整原文、来源/版本及真实条件对照。没有已记录条件时明确说明。确认前引文和适用说明由用户填写，项目约定复选框默认关闭；决定独立持久化，不改两条知识。badge 限“用户确认：本次按项目约定参考”，不推导语义冲突或 Agent 实际执行。

operation/payload 在本地私有有界文件持久完成后发送；未知回执保留原请求显式重试，重开只读恢复，源版本/范围/库身份变化不能复活旧徽标。回执冻结 epoch/root/owner，root 必须在 ConfigureLibrary/restore 接 `retireProjectPreference()`。返回保持原知识选择、查询、滚动和草稿；偏好编辑复用现有版本化手动编辑。

最终定向 race **78812 exit0 desktop6.067s**。覆盖真实临时SQLite与Unix IPC、30+2分页、无默认选择/勾选、durable-before-send、unknown同op重放不重复决定、知识正文/revision不变、重开只读恢复、pref更新CAS、旧epoch回执拒绝、写失败不发送、偏好编辑返回双草稿与查询滚动、私有文件身份/权限/有界读取/损坏拒绝。正常组件/渲染专项2643 exit0；headless1200双卡/800上下及底部checkbox与固定确认可达。

状态仅源码实现/局部patch待主Agent集成与Native验证；不将005旧包、原型示例或headless图片当本片正式Native通过。未操作窗口、真实用户库、模型、全量测试或打包。自动检测差异、Agent采用报告和个人偏好新增结构化条件均不在本片范围。
