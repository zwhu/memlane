# dcim-fe 原天气回合与积压界面

2026-11-15。用户再次指出原TraeX天气回合未显示，随后追问memlane积压记录和按钮未对齐。沿用DCIM-SYNC01，保留首次1轮反馈，本次为同一原回合第2轮反馈；后来的验证会话成功不算原天气问题通过。

## 范围与验收

原TraeX会话→hook／本地待发→官方会话→Native原始记录；同时只读核对memlane积压条目的类型、任务范围与发送状态，统一待处理页状态／操作列。保留20条写入结果未知项，不批量重发、删除或清空。天气仅恢复用户指出的一轮，不补其他历史、不调用模型或知识整理。

## 原天气证据及恢复

- 源会话 `01a1dcdd-bc48-78d3-a358-fef7f71ab7f0`，TraeX 0.215.0、cwd为dcim-fe。源中保留09:42:04的问题与09:43:02的最终回答。
- 原host key `traex-98623c5857eb0d1170709ee785096d2afb4114e6e7ae3e173b243e7d598807e1`。原时点4次hook均unconfirmed，项目权限当时official_result_unknown；未有本地正文buffer／launch map。最初官方目录仅含另一条保存验证会话，原天气未在其中。`.local/check-dcim-weather-20261115.py`先红，输出weather_session_present False；该命令仅GET。
- 本次先对齐历史迁移规则的适用范围：AGENTS中“不自动迁移、补采或重放”约束权限改造时的历史迁移，不是禁止正常会话自动保存，也不能要求用户针对已明确指明的单条缺失故障反复确认。首次询问范围过宽，已向用户纠正。仍不批量导入其他历史。
- 从源精确提取这一轮，核对源文件SHA256，正文只保存在私有.local。通过受管TraeX原主体的只读binding接口核对已有项目／dataset，恢复这一会话未落盘的本地绑定；不调用resolve冒充新宿主回执、不改权限。
- 复用原官方launch map、warmup buffer、typed QA和drain。恢复保持原prompt_time／time、源会话ID及明确恢复标记；同内容先查重，操作状态先落私有回执，未知不重发。最终官方GET精确匹配一轮，feedback脚本转绿。
- 目标session `codex_traex-98623c5857eb0d1170709ee785096d2afb4114e6e7ae3e173b243e7d598807e1`；前缀codex来自既有官方会话命名默认，不是本次执行Agent身份。
- 原official_result_unknown的具体异常没有留在旧日志中，仍未知。原数据恢复不等于首次权限请求为何失败已根治。

## 积压数量的已证原因

当前采集开关为true，不能解释成用户关闭了保存。只读官方pending show核对：154、80和182条，均无unknown；截图中209条在另一轮自动发送后降为182。工具日志记录本会话多次warmup_drain_budget_exceeded，单次保存18／25／27／29条后留尾；另有发送锁占用而跳过。原官方hook按事件触发、单次默认20秒预算，不是后台无限清空队列。

| 会话尾部 | 待保存 | 工具记录 | 问答 |
| --- | ---: | ---: | ---: |
| 1f2d824d | 154 | 151 | 3 |
| d0926b68 | 80 | 79 | 1 |
| f9b17dc3 | 182 | 171 | 11 |

计数按条目而不是会话或知识，工具调用会快速增加数量。时间范围只表示原事件时间，不能独自证明条目何时进入buffer。

上次后台job状态completed、Saved404、Remaining0，是固定快照范围的任务结果。该job中1f2d824d仅固定54条且已保存54；d0926b68当时固定0条；f9b17dc3不在当时目标清单。不能将“上次完成”解释为当前所有buffer已清空。20条旧未知项仍独立隔离，未重投。本次没有点击保存积压按钮或启动新后台job。

## 按钮布局

定向几何检查先红：重新读取X1049.40/W81.60，对比核对保存结果X1023.61/W107.39，都是24高；右边界相同但左边界不同，未保存徽标也随右侧有无按钮移动。下一步固定状态列、操作列及按钮宽高，空操作保留列宽，读取中禁用并给出反馈；待整理查看按钮沿同一列。

已完成：状态列112、操作列112×24，空操作保留列宽，按钮居中；两种窗口尺寸几何检查通过。实际Native核对保存结果／重新读取／查看同列，旧20条保留，读取异常文案可见。没有点击保存、暂停或核对这些未知条目。

## 用户追加的自动处理要求

用户明确问“积压的信息为什么不是自动处理，而必须我来手动处理”，将本次范围扩为默认日常自动续存。复用现有后台Backlog与官方buffer；仅在当前can_capture／write允许、主体未退役时处理确定未发送条目，unknown不重发。后台不依赖窗口，正常新尾部自动续接；人工停止／失败未知保留暂停，不靠新operation绕过。启用auto_process的会话在保存后自动提交官方整理，合并短时间新增材料，不把提交当产出。

预计影响：project_backlog.py生命周期、worker逐条授权复核、自动任务元数据及Native文案；不改正文存储或创建历史采集器。先连续测试新增尾部／unknown／暂停／退役／权限变化／重启，再安全加载与真实现有buffer验证。模型使用原当前配置；已有失败停止计数不清零。

dcim09:51活动核对：官方improve operation `81e4a10b-6ef7-4ea1-9688-7fce82ef7c85` outcome=succeeded，结束09:51:05；目标是保存验证会话f30da90e...，tokens_in/out均0，embedding_tokens642；官方processing-status仅1份原始材料且completed。没有知识条目，首页显示已启动不是仍在运行的证据，需要修正operation终态映射。

已修正并实窗核对：具有可解析ended_at的成功后台operation显示已完成；只有提交、没有结束证据时仍显示已启动，错误不被结束时间覆盖。

## 自动后台实现及验证

- 复用Backlog、受管helper及官方buffer。服务启动即运行只读扫描，每10秒轮换一个项目；后台发送器只有一个，与正常hook共用发送锁。仅选当前ready、write与can_capture允许的绑定，跳过agent_retirements、归档主体、blocked材料和未知记录。
- 每项目一个持久自动operation，完成批次进入watching，后续尾部复核后继续，累计保存数量保持。人工暂停持久保留；任务错误／未知不换ID重试。正常退出与人工暂停分开，重启只从当前buffer重新检查，不恢复旧token清单；已发未确认仍是unknown。
- worker逐条复核write和capture，先标记原官方journal，成功回执后才移除，不另存正文。
- 保存后合并30秒内新增材料，最长等待5分钟，按can_auto_process提交官方improve；先记submitting，未知提交不重发。提交回执不解释为知识产出。
- Native由真实automatic_enabled回执显示自动模式及暂停入口；旧未知计数不因本批次没包含而隐藏。
- 连续检查覆盖新尾部无需手动启动、自动operation累计、unknown不入发送集合、退役／关闭capture拒绝、人工暂停跨重启保持、正常重启复核fresh buffer、未知整理提交不重复。7项Backlog检查通过；与OpenCode分发共14项，11通过、3项需额外组件的宿主检查跳过。
- Go最新全量3.192s及vet通过，布局／自动模式／官方结束时间有覆盖。日常预检包含原模型缓存7/7链接、原库、无活动pipeline／模型请求、旧job completed后正常dev重启。
- 未点击手动启动，后台自动生成operation `2841513d-b165-48b5-9c5e-2498119c9571`。已观察保存2→24→108→185→224→258→315，余量相应减少；切到dcim天气正文期间继续。349为首批快照，不代表所有未来新增。
- Native已实看原天气完整一轮及来源链接，列表标明TraeX；原QA字段time=09:43:02、prompt_time=09:42:04，列表“最近活动11:24”为本次入库活动时间，未改原QA时间。dcim知识仍0，不伪造内容。

问题计数：QUEUE-ACTION-ALIGN01红1轮、修复后通过；AUTO-SAVE-CONTINUATION01用户反馈1轮，新行为已进入真实后台验收；DCIM-ACTIVITY-END01发现1轮、终态映射通过。原20条未知及DCIM-SYNC01旧证据与计数保持。

最终时点：首批349条均取得成功回执、remaining0，自动operation进入watching；3个会话自动整理中，1个submitted、2个queued，未称整理完成或知识生成通过。原20条的只读helper仍返回pending20／unknown20／blocked write_result_unknown，未进入自动发送目标。服务及自动监控保留运行，不为收尾停任务。[小型核验回执](evidence/dcim-weather-auto-save-20261115/verification.json)。
