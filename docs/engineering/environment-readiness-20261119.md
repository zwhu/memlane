# 环境准备现有能力复核

2026-11-19。用户授权继续环境准备与依赖复用、安装确认流程；当前只dev，唯一日常库保持，不另建业务库，不为验收下载依赖或重建索引。

## 当前能力

旧 `local-prepare-20261117.md` 的末尾是当时时点，不能推断当前仍只有CLI。现有 initial_setup、initial_setup_layout、environment_setup_ui 与 bootstrap 已实施正式入口，本轮复用并核对，没有重复开发新安装器。

- 自动发现已有组件和Agent；兼容项复用，外部路径可明确指定。环境检测与Agent排查不要求官方引擎已ready。
- 缺失或不兼容项先形成具体计划，包含来源、下载量及位置；安装操作与 Plan.Digest 绑定。计划改变后原确认失效，必须重新确认。
- 安装前确认与拒绝状态保持；不自动下载。已完成阶段可以保留并继续，不把损坏已有库当成新安装，不覆盖原配置／原库。
- 1200×820确认操作可见，诊断与Agent排查有现有入口。此为组件／行为检查结论，本轮没有新开首次安装Native实例。

## 当前补验状态

用户截图后的服务页重设计已完成源码实现及当前日常Native验收。下方保留改造前的组件检查事实；缺项安装、严格冷启动和完整包仍未新增验收。

## 用户截图后的局部重设计（已实现并实窗验证）

用户针对服务页展开态明确要求“丑爆了，重新优化”。本会话接管Computer Use及本局部实施，保留另一会话的记忆处理/Agent改动。范围限设置“运行环境”列表、位置详情、诊断操作和已有路径编辑；不改依赖检测/安装确认、运行服务、原库、初始三步结构或模型配置。旧视觉验收事实保留，新候选单独对照实际窗口。

布局：名称/版本/状态按列对齐；实际位置使用紧凑次级区，可复制完整路径；诊断操作横排；已有路径编辑用固定SDK Modal和独立草稿，取消/关闭不影响原路径，不在弹框中安装或启用。首次环境详情的既有布局暂保持，检测和选择持久逻辑继续复用。

连续用例：服务页→展开位置→复制一项→收起；重新检测→真实状态→已有路径弹框→修改草稿→取消→再次打开核对原值；诊断导出原生保存框→取消。实际1200×820窗口核对内容、布局、操作可达与取消边界。无真实缺项时不造缺项或下载来补覆盖。

生产文件所有权为environment_setup_ui.go及必要局部帮助文件，精确原文件备份在 `.local/design-v3-acceptance-20261119/environment-redesign-20261119/`。既有停止次数保留；本轮旧窗口ID失效是热重载前置问题，不计为环境布局产品失败。

### 最终实现

- 五项依赖统一名称、版本、状态三列，沿用已有品牌图标；版本与状态来自真实检测。
- 展开位置使用独立的紧凑区，Python和运行库共用实际路径时合并一行；显示用~缩短HOME前缀，Tooltip和复制保留完整路径。
- 复制诊断材料/导出诊断包横向左对齐，指定已有路径放右侧，不再把三个入口居中垂直堆叠。
- 设置路径编辑复用MyGo Modal，六个字段、关闭、取消及检测动作均在窗口内；PathDraft独立于当前选择，取消/关闭清除草稿，明确检测才走原选择持久及检测流程。
- 保持原安装确认/拒绝/保护库规则；初始化详情布局和另一会话记忆处理/Agent源码没有纳入本轮改造。

### Computer Use实际验收

本会话01a1f008执行，日常配置 `.local/daily/runtime.json`，同一 `.local/daily/official-store`；最终Native93168/window325121，逻辑1200×820，输出图1124×768。已实际打开和审阅截图，未以合成图代替。

| 场景 | 结果 | 证据 |
| --- | --- | --- |
| 默认/展开环境页 | 名称版本对齐、四行真实位置、横向操作、服务状态可达 | native/02-expanded、09-default-final、12-final-expanded |
| 复制位置 | AXPress实际复制，剪贴板只做布尔校验，完整Python绝对路径匹配配置，不是~缩略值 | native/03-copy-location；复制核对命令返回True |
| 路径草稿取消 | 输入/tmp/memlane-path-draft-cancel，取消后重新打开恢复原Python路径 | native/04-path-dialog、05-path-edited-draft、06-cancelled、07-reopened-original |
| Esc关闭与重新检测 | 弹框关闭回原页，收起详情后重新检测；五项依赖仍可用/已就绪 | native/08-escape-closed、10-redetect、11-redetect-ready |
| 诊断导出取消 | 原生“导出安装诊断”保存sheet实际出现，点击Cancel后回服务页，未确认导出 | native/13-export-save-dialog、14-export-cancelled；13图仅捕获主窗变暗，sheet字段和Cancel由实际AX树证明 |
| 配置保持 | runtime及service_environment_file两份文件摘要前后一致 | config-check.json unchanged=true |

私有截图和完整实际控件回执共14组保存在 `.local/design-v3-acceptance-20261119/environment-redesign-20261119/native/`，不提交本机配置或界面内容。

- [改造前的展开态](../../.local/design-v3-acceptance-20261119/environment-native/02-environment-details.png)
- [改造后的展开态](../../.local/design-v3-acceptance-20261119/environment-redesign-20261119/native/12-final-expanded.png)
- [新的已有路径弹框](../../.local/design-v3-acceptance-20261119/environment-redesign-20261119/native/04-path-dialog.png)

### 检查和问题归属

定向环境/初始化检查及路径输入→取消/关闭→重开回归通过，最终全Go test退出0、Go vet退出0、Python参数lint退出0。日志targeted.log、targeted-dialog-fixed.log、go-all-final.log、go-vet.log、lint.log及三份目标文件source-final.json在上述私有证据根。本轮最终检查后3文件摘要无漂移；不将整个共享工作区视作冻结候选。

最初新增输入测试点中了同名静态标签，没有编辑输入框，属于方法问题；输入框可访问名称改为“输入…”后通过。原targeted-with-dialog.log及同期go-all.log保留，不把这两份同基线佐证重复计成两轮产品失败。中间旧窗口ID失效、旧手动交互通知及准备状态拒绝单独保留，既有CU-PREP01不重置；通过官方已知应用路径open_app重新定位当前已运行窗口后实际点击成功，没有结束MCP或日常服务。

本轮界面不要求新安装、配置应用或模型任务，未确认下载/安装/生成，未改日常服务。真实缺项的安装确认/安装成功及首装页面不属于本轮新增Native证据，继续按原检查/历史证据边界。当前只dev，未提交本轮源码或进入release。

## 重设计前的检查证据（历史）

`sh scripts/go.sh test ./internal/bootstrap ./internal/simpledesktop -run 'TestEnvironment|TestFirstUse|TestReuseAndChangedExternalDependency|TestPlanChangeRequiresNewConsent|Test.*FirstRuntime' -count=1` 退出0，两个包通过；task.py记录可用空间约80 GiB，按50/55 GiB门禁运行。

随后完整Go test/vet与Python lint通过；独立暂存候选检查另留回执。日志在 build/diagnostics/roadmap-reliability-20261119/consolidation-environment-contracts.log 及 consolidation-final-*.log。

本轮确认的是当前源码的复用、确认与拒绝契约；没有执行新依赖下载、严格无缓存冷启动、完整包或release验收。这些边界不能由日常已ready服务代替。当前既有模型、组件、官方库和用户登录保持。
