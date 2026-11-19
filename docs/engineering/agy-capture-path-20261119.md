# agy 终端聊天采集路径修复

用户报告“我刚用 agy 聊天了，但是没有记录”，并确认入口为终端直接运行agy。当前同库、dev及其他会话修改保持；不恢复历史Goal或release，不回扫/重放原聊天。

## 实际复现与归因

原生agy加载4组官方Hook、插件enabled，当前memlane工作区；管理侧该主体无host回执/绑定。端点及原控制token只读核对可用。起初按旧文档使用transcript.jsonl的元数据探针可以到达bind，属于错误的载荷假设，不能当作真实宿主通过。

随后执行一次独立原生print，仅请求回答OK；临时sitecustomize按owned子进程环境记录字段名、会话/路径、错误类型，不记录聊天正文。实际运行32.944秒、退出0，5次Hook调用均在prepare_payload第20行被native_transcript_identity_required拒绝。真实transcriptPath为brain/<conversationId>/.system_generated/logs/transcript_full.jsonl，代码只接受transcript.jsonl。根因GA-CAPTURE-PATH01是本项目接入校验的文件名约束，与官方Key筛选、账号地区及保存队列问题分别计数；实际失败1次，同一次会话中的5个Hook不重复计次。

## 影响与连续验收

只将已证实的transcript_full.jsonl加入同一原生路径校验，保留绝对路径、UUID目录、符号链接、首次激活时间、工作区与项目控制身份核对，不修改官方采集允许/拒绝规则或另建采集器。现有Antigravity已激活后禁止直接prepare；必要的修复更新沿明确review原安装→准备新候选→核对并启用原生插件，保留旧安装到候选启用，首次激活边界跨修复更新保持，不因修复把用户当前正常会话误作安装前历史。只核对该Agent，其他插件、登录、处理来源及数据保留。

连续用例：实际完整文件名→接入→本轮输入/回复配对→原编号保存→同库正式回执→原队列0；标准文件名继续接受；任意文件名/错UUID/相对路径/符号链接/旧会话仍拒绝。更新先review已登记原产物，候选准备不覆盖实际active安装；未review、输入变化及不明安装继续拒绝；新候选启用保持独立主体、其他插件与首次激活时间；中断按原操作恢复，不重装或重投未知。

## 测试技能状态与范围

bits-unit-test-gen Step1/2完成，LANG=python、EXEC_SOURCE为空、现有unittest与临时真实官方制品；TMP_ROOT=/var/folders/nv/g6m5_3nx1r9_v9dk3zh_5zw40000gn/T/tmp.fffz9Ry7Kn。无新依赖、无CI覆盖率门禁；命令经task.py。先列范围和缺陷，再生成回归，不用模型反复试错。

TARGETS（non_diff）：antigravity_hook_policy.py:prepare_payload（真实宿主文件名接入）；antigravity_connection.py:prepare/activate/review（本次修复所必需的受管更新与既有激活保护）。同族NativeConnection.prepare只作为直接被调依赖，不改其他宿主。

BUG_MAP：GA-CAPTURE-PATH01 / Business Gaps / P1，正常CLI载荷transcript_full.jsonl在绑定前被拒绝，所有记录未保存；期望同一安全校验接受两种已证原生文件名，非法路径与旧材料继续拒绝。活跃更新禁止本身是现有保护，不反向认定为第二个产品缺陷；本轮只补明确review后的修复更新必要路径并验证其保护。

## 生成、验证与修复

路径目标新增1个入口、10个场景；首次回归在full场景明确失败native_transcript_identity_required，修复后10场景通过。正常/完整文件名、其他文件名、错UUID、相对路径、文件/父目录链接、两种旧文件与无工作区分别断言；实际prepare到bind前，外部HTTP/保存副作用受控，不模拟被测路径判定。

更新目标新增3个入口、5个场景：完整review→准备→启用、配置/产物变动拒绝、journal/原生标记写入中断后不重装恢复。准备不改变active安装、错误候选不启用、准备态review不启用、主体和其他插件保留、采集首次边界保持。首轮夹具漏sha导入；次轮两个场景之间遗留上一场景的connections恢复账本；均属测试方法错误，保留日志，不计产品失败。第三轮12个入口（含8个历史入口）17.040秒通过。

部署盘点还确认连接activated_at与原生安装首次标记不是同一时刻；准备时记录原生active_boundary_at，启用/中断恢复使用这份精确首次标记，避免被管理侧后续确认时间替代。夹具用222与111两个时间核对该边界；最终受影响Antigravity/Connections/项目控制/采集HTTP合计77个入口，31.229秒通过。原生插件替换另用临时宿主执行真实两次agy plugin install，均退出0且文件实际更新；未调用模型或触碰日常库。静态lint通过。

测试阶段按技能仅处理测试；生产修复依据用户当前未记录问题授权独立实施，不以测试技能禁止改生产的角色边界停在已确认缺陷。Step1–5完成；Step6按条件跳过，EXEC_SOURCE为空、cov_config=null、CHECK_COV_MODE=skip，无新增覆盖率依赖或虚构覆盖率值；Step7通过utree flush落盘。新增15个场景与历史入口分开统计，回归失败原日志保留。

## 归因纠正

此前文件名判断来自已读调研的普通transcript.jsonl样例；接入当时真实模型任务又被账号地区资格阻断，未取得终端实际载荷，却把样例写成唯一接入条件。既有夹具重复该假设，造成验收缺口。当前已从独立原生Hook元数据确认实际CLI使用transcript_full.jsonl；不能把前置阻断或安装成功解释为该聊天入口已验证。本问题属于本项目实现及验收，不归因用户。

## 同库部署与真实保存通过

原owner62711在bridge/官方后台/图任务均0时正常stop并确认日常库释放，原registry/jobs/incremental/原队列共7个私有文件已备份。新owner56502及56591/56595/56962同一runtime与official-store ready，当前处理者仍existing-codex revision2，未改变账号/模型/其他插件。既有用户agy终端进程独立保留。

只对原agy主体执行管理review/prepare/activate，revision188→190→192；候选manifest5c6ce7ddf656c3f0c7012b78c4ec8c9362861c24f7259c478193a2e7af62ca5b。prepare阶段原active安装不变；最终原生目录仍~/.gemini/config/plugins/memlane-cognee，首次标记1795061757.052684逐字保持，官方_capture_policy SHA894e13e3…不变。旧源制品和小型原生安装备份保留作回滚，未生成第二运行库。

修复后独立原生print请求52.091秒退出0，5次Hook均admitted、没有接入异常；官方同一主体/dataset/session的capture-status得到1条QA、0条trace（请求明确不调用工具）。原message_id e52ff7b0…凭证通过只读cache_kv及正式GET双重核对为saved，不是只看空队列或进程退出。

用户实际原会话ea42c062…随后正常Hook也已进入同库，host antigravity-0ea1372f…，QA1、trace0，原message_id760313f3…正式回执saved。此次对该会话只有只读核对，没有手动重发、回扫或补采；不声称之前全部遗漏轮次已恢复。当前这两条会话的原buffer均pending0/unknown0/rejected0/deferred0，管理采集状态healthy、环境回执true。原Codex20条unknown及SHA b9b8c618…5885保持，其他历史提醒及失败计数不变。

诊断前后两个owned agy进程组均确认退出。临时sitecustomize与模型探针已归档为.txt，并删除其__pycache__，只保留小型元数据/原失败日志；未在生产模块遗留诊断trace或修改当前用户进程的环境。源码回归、原生安装协议、实际CLI保存分别有证据；普通Gemini App聊天与模型质量不由本次CLI问答外推。

Native窗口327853/57016实际截图显示服务已连接及原始记录总数71；设置点击仍被Computer Use的not prepared门禁拒绝，刷新可读取窗口。保持既有工具停止边界，不改用另一种UI自动化；没有把实际详情页/Agent页对照记为通过。此方法边界独立于已通过的实际保存回执。

回执位于私有.local/daily/save-processing-recovery-20261119/agy-fix、agy-probe、agy-probe-after、agy-after-original-receipts.json、agy-user-session-receipts.json及agy-final-queues.json。本轮新增15个场景全部通过；77项受影响回归与最终lint通过、utree flush完成。当前dev修复已启用，源码未在本轮自动commit或push；上次队列提交0f0cb01仍保留。
