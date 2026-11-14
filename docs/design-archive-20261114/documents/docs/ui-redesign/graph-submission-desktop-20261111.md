# 正式项目图材料入口（004 之后源码批次）

2026-11-11。依据 F–O 固定候选 `780745fc55d71edb22e46d9c3e4f208d5ecb602f` 的 I 三栏和 J 选材/Token 确认布局；实际打开 I05 窄图、J02/J04 普通图后实施。沿用当前项目实体/关系图右栏，不新增路由或 HTML。外层固定 header、正文独立滚动、底部确认/查询/取消固定，列表仍保留项目实体上下文。

## 实现边界

默认入口改为本项目真实知识/原始记录材料提交。通过 `Library.List` 按当前 project、真实 origin、搜索、原始 source、cursor 分页（每页 30）；显式选择最多 16 个确切 ID/revision，保持类型，跨页已选清单逐项显示，不自动全选。新操作重新读取并明确选材，不偷偷提升旧版本。

使用 daemon `AuthorizeProjectCogneeMaterial` 确认当前项目材料范围，连接须已有当前项目明确授权，支持 pi/Codex。服务端生成真实 binding；不要求用户填写 endpoint/dataset/key，不重走初始化/首任务。处理方式必须已明确启用，确认页展示当前请求模型及 Token 消耗说明；最终请求只带处理账本 identity/generation，由服务冻结依赖并再次校验。

Start 前写当前 MaterialPreview 实际 identity 与 project 分开的客户端 intent：0600/O_NOFOLLOW/openat、排他锁、64KiB 有界严格 JSON、UUID/source 上限验证、fsync+原子替换+目录 fsync。文件仅含原 operation、ID/revision、处理账本身份，无正文/密钥。未确认本地保存则不发送 Start，保留原请求供明确重试。未知远端回执只 GET 原 operation，不自动再启；重开优先恢复本地原 operation，不被其他最新任务覆盖；无本地 intent 时才读当前库服务 Latest。

关闭面板/离项目不取消 daemon 任务。客户端 2 秒节流查询原操作；取消只提交取消请求，等待真实清理终态，不把 `CancelRequested` 显示成已取消。`interrupted` 显示状态待核验，不开放新操作。成功才刷新当前项目真实实体图；授权/保存/处理请求均不补造实体或来源。旧会话同步作为明确次要入口保留，MaterialSubmission binding 不能进入旧 session 同步。

所有读取/Start/授权/GET/Cancel同步冻结 client、lifetime、dispatcher、epoch。跨项目只更新原项目缓存，不改变当前项目图；跨库旧回执拒绝。ConfigureLibrary 和整库恢复退休客户端 watch/缓存，并取消尚未完成的本地 intent/授权/Start 客户端 context，不主动取消 daemon（恢复由服务所有者执行收口）。

## 定向证据

- `29178`：专项 race exit 0，2.200s。
- `51724`：专项 race exit 0，4.442s；图材料选材/持久屏障、原 operation 重开、取消等待、私有文件/epoch 隔离及既有图展示回归。
- `89260`：增加真实 Unix IPC project/kind/search/source/cursor 过滤和越域返回拒绝后，race exit 0，2.291s。
- 窄右栏 388×640 对应 800 窗口三栏余量，headless 图片在 `build/graph-submission-20261111/headless/`；已实际打开正文截图，固定底部可见，正文可滚动。

`25459` 窄窗专项 race exit 0（4.671s）；最后生命周期补齐后 `41620` race exit 0（2.241s），底部数量文案 `69896` 单项 exit 0（0.911s），包含底部分页/固定 footer/Token 确认可达几何断言。已实际打开正文、滚动底部和确认截图，未见关键遮挡。本批只运行定向测试和 headless 组件，不操作 Native、真实模型、用户配置或全量包。新 Cognee 组件/正式模型处理/完整包 Native 主线 9 验收仍由主任务独立完成，不能将本页测试写作最终交付。
