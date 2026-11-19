# 剩余Native验收接续

2026-11-19。用户再次要求继续推进。原源码提交3c4c43e、2f7d8ac及验证记录890f723已推送；本轮没有新增生产代码或重跑已通过矩阵，不进入release。当前只补V3剩余实窗连续操作，其他会话Gemini桥保留其所有权和未完成边界。

## 本轮核对

- 当前工具list_apps定位窗口323748，get_app_state实际读取PID97697，逻辑1200×820。画面为设置记忆处理页，实际使用Codex；新增Gemini候选来自另一会话当前工作树，不能当作本轮新任务验证。
- 按最新树点击通用记忆，仍被`Computer Use is not prepared`拒绝，未到点击handler。旧CU-PREP01及后续失败保留，用户恢复授权不重置计数。本轮1次准备门禁拒绝，不累计为日期、分类或切换产品缺陷。
- 当前调用写入`mac-cpu-mcp-49710.log`。其工具wrapper49666、Host48980；旧日志明确保留knownPids=[68101]，当前窗口所属PID97697。get_app_state按显式窗口成功，checkAppPrepared重新通过listRunningApps的首个名称/bundle匹配获取旧身份，造成门禁身份不一致。两个其他wrapper及其日志没有本轮调用，不向它们发信号。
- 官方CLI的mcp命令只有配置/启用/禁用/登录等，没有重连当前已加载客户端的入口；插件代理只在子进程exit后退出，不自动spawn新客户端。停掉当前MCP会导致Transport closed，原历史已有此证据，本轮不重复该动作。
- 日常服务当前owner158（13:40:21启动）ready，children160/161/367，同一`.local/daily/official-store`及existing-codex revision2。本轮未启停服务，先前owner86475保留为历史时点。不能将其他会话的服务更新推断成崩溃。

本轮私有截图、操作摘要和9个相关源码SHA保存在`.local/design-v3-acceptance-20261119/native-resume-20261119/`，不提交真实界面内容或配置。实际截图已查看，不能以可截图代替可点击。

## 恢复入口

退出当前TraeX，然后从终端执行已用当前版本help核对的命令：

```sh
traex resume --no-daemon 01a1eed7-55ca-7c63-9c2b-9acdc8129524 -C /Users/bytedance/work/AI/memlane
```

`--no-daemon`建立新的嵌入Host，避免复用当前48980中的已加载MCP。该命令仅为已核对接续入口，当前尚未执行；不能预先宣称连接恢复。无需重启日常Cognee服务或重新下载模型。

## 接续操作

1. 新Host重新发现工具、list_apps/get_app_state，核对当前实际窗口；先确认一次总览/通用导航动作真正通过，不沿用323748或旧控件ID。
2. 源码与同库前置核对后，仅补来源→返回详情→返回日期/切日、滚动与位置恢复、搜索提交/类型过滤/返回、通用分类/阅读、处理候选取消和画像仅浏览。主流程见[V3执行记录](design-v3-execution-20261119.md)。
3. 设置取消前后读取原管理active回执；只浏览和取消，不新增模型验证或实际处理切换。空通用分类如无真实内容，仅验准确空态；有内容/读取失败继续使用现有HTTP夹具边界，不清库造样本。
4. 保存实际操作和截图，并更新[V3报告](design-v3-acceptance-20261119.md)。源码未改变就复用已通过候选检查；若其他会话改动影响本用例，则只补受影响检查。不给旧工具计数清零。

Computer Use技能要求“Stop and report persistent Code Mode, transport, permission, screenshot, or signing errors.”；当前依该要求停止重复操作，不使用其他UI自动化绕过。技能入口见[SKILL.md](/Users/bytedance/.trae/plugins/cache/traex-bd-plugins/computer-use/0.3.2/skills/computer-use/SKILL.md)。
