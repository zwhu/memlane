# 行内复制与刷新图标

2026-11-15 用户要求：复制图标更小、放在对应文字旁，悬停才展示；检查其他类似复制入口。随后要求刷新统一为刷新icon。本轮是局部交互调整。

## 实现

- 共用固定MyGo SDK的Button，复制与刷新使用14px单色图标、22×22点击区，保留Label、Tooltip、键盘激活与Disabled语义。
- 项目路径、内容信息字段、代码块、项目及通用知识正文工具栏、旧阅读视图的字段/原文/正文复制统一紧邻文字，间距4px。悬停所在行或键盘焦点进入时显示；隐藏只改变透明度，文字及图标位置不跳动。截短展示仍复制原始值；正文与原文复制保持各自语义和失效限制。
- 待处理区、项目/通用知识列表、图谱、默认规则、项目建议及旧列表刷新统一图标，保留原读取回调及提示。队列行保留动作列宽与右对齐，图标不占整列点击区。
- 权限未知时的“重新核对”、安装/处理的“核对原操作”等独立动作保留文字；诊断信息复制保留在原失败菜单内。

## 验收

- 首轮定向检查通过：TestInlineCopy、TestRefreshIcon、TestProjectReadingModal、TestProjectQueueActions、TestProjectHeaderSettings、TestSettingsProjectPermissions匹配组。覆盖悬停显隐、紧邻尺寸、悬停不移动文字、原值复制、Tab显示与Enter激活、刷新禁用及导航。
- 已实际打开当前日常Native的daily-cli项目设置，对照用户截图查看隐藏态及鼠标移至路径后的显示态；复制图标位于路径末尾，待处理刷新和会话重新读取均为刷新图标。未触发模型、未重启服务、未改名或修改权限。
- 其他入口复用同一组件，完整实际交互不据项目设置画面外推；源码回归结果继续追加。
- 补查同期新增的待整理文档详情页，原文复制移至“原文”标题旁，刷新处理状态/重试读取统一图标。
- 包级test首次发现官方完成状态用例的session-copy/trace-copy夹具缺少dataset，已补齐为ds，不修改生产身份校验；该用例及本轮定向检查随后通过。包级复跑另有同期新增TestQueuedDocumentReaderDoesNotEnumerateAnotherCatalogAndRestoresScroll失败（请求other/processing-status和activity/pipeline-runs、夹具未滚动），已交对应会话续办，未冒称全包通过；与本轮图标交互是否相关尚无证据。
- 无新增产品验收失败。队列旧测试从“所有动作同宽”改为“动作右侧对齐”，匹配新图标要求；状态列与动作列布局保留。

实现：`internal/simpledesktop/inline_actions.go`。原共享未提交修改保留，未提交他人代码。
