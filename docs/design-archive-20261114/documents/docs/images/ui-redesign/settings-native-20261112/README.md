# 设置Native实际窗口对照

固定设计 `c84f75ce4258fb122eb7574e1bd89c64ef7ba714`。final/为源码冻结后026/027同一签名后GUI的双尺寸NSWindow截图，每张带JSON回执；不是浏览器、headless或真实用户环境。

| 页面 | 固定设计 | 普通实际窗口 | 最小实际窗口 |
| --- | --- | --- | --- |
| 环境与组件 | [设计](../non-project-20261112/35-environment.png) | [1200×820](final/environment-1200.png) | [800×620](final/environment-800.png) |
| Agent连接 | [设计](../non-project-20261112/38-agents.png) | [1200×820](final/agents-1200.png) | [800×620](final/agents-800.png) |
| 记忆处理 | [设计](../non-project-20261112/42-processing.png) | [1200×820](final/processing-1200.png) | [800×620](final/processing-800.png) |
| 记忆服务 | [设计](../non-project-20261112/48-service.png) | [1200×820](final/service-1200.png) | [800×620](final/service-800.png) |
| 备份恢复 | [设计](../non-project-20261112/51-backup.png) | [1200×820](final/backup-1200.png) | [800×620](final/backup-800.png) |
| 检索索引 | [设计](../non-project-20261112/57-index.png) | [1200×820](final/index-1200.png) | [800×620](final/index-800.png) |
| 客户端更新 | [设计](../non-project-20261112/59-update.png) | [1200×820](final/update-1200.png) | [800×620](final/update-800.png) |
| 关于诊断 | [设计](../non-project-20261112/62-about.png) | [1200×820](final/about-1200.png) | [800×620](final/about-800.png) |
| 自配LLM | [设计](../non-project-20261112/43-processing-provider.png) | [1200×820](final/provider-1200.png) | [800×620](final/provider-800.png) |

窗口边界分别为1200×820、800×620，截图为Retina 2倍。两个source-info含签名后二进制／plist／全部GUI输入SHA256；SHA256SUMS.json覆盖图片与回执。

所有数字、账号、路径与结果均为显式合成输入，未连接用户记忆服务或模型。已连接卡片的只读、未发现、未配置及按钮禁用保持真实门禁，不能据此宣称这些Agent已在本机接通。

根目录与historical-018-019保留历史／未通过图，不能替代final：Agent旧图标和选择框布局经009、015、018/019三轮偏差后，021/022第4轮通过；失败计数不重置。

输入补充：provider-input-1200.png及provider-input-retained-1200.png为016实际聚焦字段的ASCII键入、中文粘贴及切方式保留草稿；中文IME候选组合未覆盖。更多用例、命令、失败和接口缺口见[工程账本](../../../engineering/settings-redesign-20261112.md)。
