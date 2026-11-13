# 项目权限增补交付

2026-11-13，PA01–PA14、PA-A–H 的本机 dev 自验已通过。日常 Codex 接入已在用户明确交接后启用，真实 CLI 和普通 Desktop 问答均已核对。没有进入 release，也没有构建完整 App／DMG、安装 helper 或签名发布。历史全量 Goal 保持暂停。

详细过程、每次失败和方法观察保留在[执行账本](project-access-execution-20261113.md)；去正文的[证据摘要](evidence/project-access-20261113/progress-summary.json)可入 Git。原始回执在忽略的 `.local/project-access-evidence/`，真实库、凭据、聊天记录、宿主私有配置不入 Git。下文完成表示本轮自验，不代替用户体验验收。

## 已交付行为与证据

| 要求 | 实际结果与证据入口 |
| --- | --- |
| PA01 | 新仓首次会话由管理侧登记独立官方组，首写前绑定；Alpha 与日常 daily-cli 实际采集、处理、产生知识，后续会话实际采用 |
| PA02 | 默认规则动态继承，显式 none／false 不被模板覆盖，null 恢复继承；真实 API 和 Native 默认保存／恢复均通过 |
| PA03 | 独立官方子主体有效权限为 none、read、read/write/delete，不授 share；真实 raw/session/search/graph/save/update/delete 允许／拒绝通过，角色与 tenant 并集核对 |
| PA04 | 无写权或禁采时先推进无正文游标，不读新正文／入队；Beta 实际编码及同会话恢复只采新内容，不回扫禁用期 |
| PA05 | 自动处理覆盖 cooldown、idle、final、exit 等入口；真实关闭后的原 pipeline 集合不增加，主动操作另查权限 |
| PA06 | 无权主动保存明确中文拒绝；有写权、关闭自动采集时主动保存可成功，没有换 owner／换组 |
| PA07 | 真实 403 进入 blocked，权限恢复后不自动重试；Native 明确补交一次成功。RST 未知结果保留且不重发，明确 CLI 丢弃通过 |
| PA08 | 手动／自动共用身份。真实已有组关联保留 read/none 覆盖，重复关联不重置；Native 已有组选择器成功复用原 UUID |
| PA09 | 新项目自动出现在 Native；目录失败保留、恢复清理错误状态；关窗期间后台发现，重开补齐 |
| PA10 | Native 通用知识新增／编辑同 DataID、官方原文及处理终态通过；实际删除后 raw404、Native 空列表与正文撤下；可按分类及来源阅读／搜索 |
| PA11 | 空项目实际采用通用知识；同一 coding 注入块合并项目与通用候选，实际冲突保留项目约定；真实撤权后同进程缓存不注入 |
| PA12 | 同仓两主体并发单组、手动／自动去重、同名分离、符号链接／子目录、worktree、移动别名均通过；通用／代码组不误列为项目 |
| PA13 | 实际 Native 关闭／重开、来源／返回阅读通过；读取撤销后拒绝并撤下失效正文与复制入口；管理 owner 与 Agent 权限分开表达 |
| PA14 | 按实际官方能力报告任务；无 cancel 路由时不伪称取消。关闭开关只停止后续触发，原操作、部分成功及未知材料保留 |

PA-A 用 Alpha／daily-cli 新仓连续链；PA-B 用 Beta 禁采与恢复；PA-C 用动态模板、覆盖恢复、新主体及退役凭据；PA-D 用真实 ACL 矩阵、维护 CRUD 和 PATCH 拒绝；PA-E 用原 403／RST 操作及明确处置；PA-F 用身份／路径／并发／新建关联；PA-G 用通用 CRUD、真实采用和项目优先；PA-H 用 Native 生命周期、来源返回及旧材料保护。没有用 HTTP200、合成协议夹具或任务接收代替实际知识与采用。

## 日常接入与最后收尾

- 用户“交接并启用”后，停用旧 `cognee@cognee-local`、启用固定受管 marketplace。仅停止核对了 PID／启动时间的旧采集 watcher，未结束编码父进程；模型、其他插件、日常登录及管理 owner 保留。实际安装缓存即使继承 owner 环境 key，仍固定使用子主体。
- CLI 使用正常仓库／hook 信任流程，逐项审阅本次 9 个 Cognee hook，没有 Trust all 或信任绕过；其他未审阅 hook 保持原状态。真实编码后官方会话 qas1/traces6，知识正文与实际文件一致。
- 用户“已发送”对应普通 Desktop 会话 `01a1d3e9-8312-7032-a96d-c7e51523d326`：真实 UserMessage、同 daily-cli 项目、完成且 qas1/traces1。已在最终 Native 中读到完整问答与边界说明。此前工具委派任务 qas0 的方法观察保留，没有冒充普通用户来源，也未重投提示。
- Native 默认规则实际保存采集关闭，再恢复开启；已有组选择器实际关联本轮空组且保留全部 none。最终默认为维护权限／采集开启／处理开启。
- 原观察工具 cgWindowNotFound 的原因仍未知；用户回复后窗口观察恢复，已完成实际截图、设置及日常知识／问答阅读。未将其归因为 App 崩溃或配置丢失，也未用新模型任务修复观察问题。
- 最后同库重载前观察 313 条活动、125 个最新 pipeline，102 completed、23 历史 errored，均终态；没有为收尾重放模型或索引。统一 stop 确认原服务组退出／原库释放，再在独占 registry 锁下移除验收引用，沿原配置启动。
- 13 个精确白名单合成数据组已删除，2 个验收子主体已退役且原凭据拒绝；移除自有 A/B 宿主、旧插件候选和合成仓等 2173 文件，分配空间 102871040 字节。保留小型回执；日常受管插件产物08、实际安装缓存和 marketplace 保留。
- 原 memlane、原代码组、通用组和 daily-cli 共 4 个组保留，原 12 份 bridge／pending 文件摘要全部不变。保留用户刚使用的 daily-cli 仓、知识及问答。没有迁移、补采、重放或删除已有 memlane／item-fe 会话与材料。
- 最后重载后重新核对有效 ACL：日常主体对两个项目及明确启用的既有代码组为 read/write/delete，对通用组仅 read；无 share、无旧消费者、无项目配置冲突。Native「核对原接入操作」成功，未重装或重写宿主配置。

## 检查与缺陷

本轮最终生产源码未在上述收尾后改变，沿用已通过的 52 项 Python 项目检查、15 项启动器检查、Go 全量 test／vet 和 simpledesktop race。最后另完成同库健康、实际 ACL、旧凭据失效、原材料摘要、重载后原始问答／知识读取与 Native 验证；文档执行 diff 检查。测试夹具、真实链路和 Native 证据分别记录。

| 问题 | 累计失败 | 已证原因／修复与最终结果 |
| --- | --- | --- |
| PA-ACL01 | 1 | owner 无选中 tenant 时查子主体权限404；改用子主体查询自身 principal，真实有效 ACL 通过 |
| PA-CK02 | 1 | 官方文本摄入去掉末尾 LF；确认仅忽略一个尾 LF，原正文／metadata 仍核对，原操作验证通过 |
| PA-CK03 | 1 | 普通通用 Markdown 被旧 session 前缀解析误报；按明确 metadata 呈现，原文与来源正确 |
| PA-NAME01 | 1 | 同名项目无法区分；最短父目录标签加稳定 ID 检索，实际 Native 分离 |
| PA-WIN01 | 1 | 关最后窗口触发 MyGo Quit；普通关闭隐藏、明确退出保留退出语义，实际重开恢复阅读 |
| PA-CONN01 | 1 | watcher 检查跨 profile 误阻塞；限定宿主 home，原安装操作继续成功 |
| PA-CONN02 | 1 | trusted HOME 配置被当作独立项目覆盖；跳过同一文件，独立项目覆盖守卫保留 |
| PA-CONN03 | 1 | 新受管 watcher 误列为旧消费者；只检查预览中被停用的 namespace，最终运行及 Native 无误报 |
| PA-DIR01 | 1 | 目录恢复后旧 GET 错误残留；独立目录错误与写操作未知状态，恢复不抹去未知回执 |

OVR01 的 stdio 修复及原库真实只读检索见[专门记录](overview-stream-repair-20261113.md)，没有重放旧失败任务。历史精简版[错误索引](client-simplification-errors-20261113.md)的计数、停止项继续有效，本轮未重置。Native watcher 外层 task 包装器与实际持锁进程不同、raw 是文本而非 JSON、活动状态使用官方大写枚举等方法纠正在账本留痕，不混为新产品验收失败。

边界保持：未知写入先查原操作；Native 丢弃按钮和通用删除确认按钮未单独真实点击，不把 CLI／官方接口删除回执冒充点击证据，其生产适配／协议有检查。实际删除后的界面撤下和实际 Native 补交已验证。代码图保持新项目默认关闭，不把未启用的 companion 创建说成已交付；不外推其他模型、所有知识质量或其他电脑。

## 资源交接与用户验收

本轮排他验收操作结束，日常后台和窗口继续运行。最后 owner 快照：launcher5546（16:09:00）、completion5548、official5557、project_control6188，ready；进程 ID 会变化，后续启停先重新用 status 核对原库锁、PID、启动时间和配置，不直接按本文 PID 发信号。统一入口仍为：

```sh
python3 scripts/dev.py status --config .local/daily/runtime.json
python3 scripts/dev.py open --config .local/daily/runtime.json
```

当前带 `--watch-ui` 的附着窗口已运行，不再启动第二个 watcher；普通关窗隐藏并保持后台。明确停止使用 `python3 scripts/dev.py stop --config .local/daily/runtime.json`；需要源码调试时沿开发说明处理附着 watcher，不能只结束外层 task 包装器。原官方运行依赖及 build/p3-prototype 工作树未清理，可用空间最后约 115 GiB，继续遵守100／105 GiB门禁。

用户可直接验收现有数据，无需再次发送提示或运行模型：

1. 当前窗口 daily-cli → 原始记录：打开刚发送的普通 Desktop 会话，看到用户问题、回答、一个边界及独立工具记录。
2. daily-cli → 知识：打开 formatSlug 知识，查看出处，再返回阅读；知识与原始问答不混为同一内容。
3. 设置：本机 Codex 显示已收到新会话回执，只有一个日常 Agent；默认维护权限含删除，采集／处理开启；两个保留项目显示已生效。
4. 项目设置中查看各字段继承／覆盖及实际生效状态；通用知识页可查看分类、来源和新增入口，合成测试内容已清理，不预置示例冒充真实知识。

保留工作树原有 design-catalog、视觉方案及用户提供的未跟踪方案文档；未将其混入本次提交。交付不是 release 或用户已验收声明。
