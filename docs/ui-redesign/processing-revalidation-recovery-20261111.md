# 记忆处理重开与显式重验恢复 · 2026-11-11

## 现场依据

003隔离实窗首页曾显示“记忆处理未配置”。读取 `build/goal-concentrated-20261111-003/state-native-project-retry.json`：保存修订 `04601e32-8413-485f-bd5f-73af5cdbf380`、验证 `89370ee3-c77a-42ee-af15-ac2b5d94de23`（attempt2、succeeded）、当前选择generation2／active=true仍在账本。002／003运行配置使用同一隔离client-responsive根，但独立包与helper路径不同。

这些只证明历史成功和选择存在，不能仅由首页零值证明依赖失效。源码中 `processing_get` 原来只返回持久账本；首页首次配置读取失败后不主动重读，未读取与读取失败都可能落入“未配置”零值文案。精确当前依赖是否一致，须由daemon重新解析并比较原DependencyDigest，不能根据“包更新了”直接重用或否定旧成功。

## 实施

- daemon processing owner新增只读Configuration投影：保留原账本的成功回执、选择和generation，同时按当前真实依赖给各修订返回ready／dependency_changed／verification_required／unavailable。此观察不启动模型验证，不写成功，不启用配置。
- 旧succeeded修订可通过“重新验证”显式提交新的operation和attempt。未保存草稿、运行中验证、不可用连接仍保留原门禁；不能把一次验证读取成功当成重新验证。
- 重验当前正在使用的修订时，Begin事务先停用当前选择并递增generation，再保存新验证attempt。验证成功只产生新的成功回执，当前选择保持停用；必须再明确点击“设为当前使用”，旧generation不能重放启用。重验其他候选不改变已在使用的另一修订。
- UI启用与初始化完成还需daemon给出的当前runtime.ready，历史succeeded或raw ledger.active单独不足以放行。重验提示明确说明会先停用当前方式；失败、取消、中断都不自动恢复使用。
- 验证终态后重新读取owner的选择generation与runtime观察，才开放显式启用；不将旧UI generation或旧成功回执继续用于启用。读失败可明确重读，不冒充未配置。
- 首页区分“待读取／读取失败／确认未配置／已保存待启用／当前方式／依赖变化需重验”，以5秒限频重新读取，保留未知与失败状态。共享toolbar与配置卡的既有正常文案保持。
- 配置读取、保存、验证、取消和启用使用捕获的原root/context及UI序列；切换或恢复库后旧回调不能覆盖新状态。ConfigureLibrary重置处理UI并递增序列。

## 定向证据与边界

使用真实SQLite和明确标注的runner协议夹具，覆盖：旧成功后依赖变化、只读观察不改原回执且不调用模型、旧成功不能启用新依赖、重新验证产生新attempt／digest、Begin停用增代、新成功仍不能执行、旧generation启用被拒绝、最终显式启用成功，以及历史验证保持。UI定向覆盖旧成功重验按钮、当前依赖变化、未读取／读失败不冒充未配置、验证终态刷新与启用分离。

本批未调用真实模型、未操作Native、未完整构建。Owner只读投影在真实运行时会检查已连接pi与依赖（含本地版本探测），不等于真实推理证据。003现场是否存在当前digest差异仍应由下一集中包的owner投影与实窗记录证明；不能用本批夹具替代该证据。
