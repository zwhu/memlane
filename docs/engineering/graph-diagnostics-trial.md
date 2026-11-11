# 图谱失败诊断试运行

## 先列用例

| 用例 | 预期 | 当前证据 |
| --- | --- | --- |
| 合法请求在Owner Acquire／ResolveTask失败 | 一条request_rejected、固定阶段安全码；completion不调用、无伪造Process | 实际handler红95846→绿61627；race6382 |
| 不合法cap／schema或预算外请求 | 不扩张任务账本、不泄密、不绕过原128／16限制 | gateway定向测试 |
| Adapter HTTP409、超时、解析失败 | 有限安全分类，不复制响应或异常原文；原预算不变 | 9项8红→11绿63392 |
| 新包与新官方组件实际安装后执行 | 新失败有准确边界；成功仍需来源与语义逐项核验 | 待执行；015构建中 |

## 复盘

当时目标是让合成README形成可追溯实体／关系。710e任务在SDK cognify约5.634秒失败，留material_model_failed、executions=null、无新审计／发布关系。原选择依赖模型完成后的receipt解释故障，隐含假设是进入SDK模型阶段的失败都会留下completion记录；该假设来自既有完成路径封存，忽略Acquire／ResolveTask可在append之前拒绝，Adapter又统一抹平HTTP／transport／解析错误。

已证实的是可观测盲区，未证实710e具体根因。不能以5秒接近Probe预算判定Probe timeout，也不能把executions=null写成零模型调用或已回收。修复补有限阶段观察和安全Adapter码，没有加重试、放宽schema／授权或增加期限。旧任务和历史输出保留。

源代码集中修复后015全量test/vet已通过，854输入冻结核对一致；新v12组件已冻结并由本机50759端口提供。完整015 App构建尚在进行，活014未替换。后续只在核对安装版本与当前处理门禁后启动一次新意图，不通过自动重试掩盖失败。

防复发：前置拒绝纳入handler用例；consumer区分completion/request_rejected/legacy_unknown，缺失Process与模型次数保持未知。独立汇总器证据见build/goal-015-observation-consumers-20261111。统一跨层Harness与真实运行验证仍未完成。

## 实际包试运行结果

015完整构建通过，但同身份helper替换被系统Launch Constraint拒绝（EX_CONFIG78），新v12尚未安装，图谱新任务未启动。已保留实际Native失败截图与系统日志并恢复014文件，运行恢复仍须验证。受保护五表摘要完全一致。见build/goal-015-helper-replacement-20261111/RESULT.md。该失败属于运行包门禁，不能把安全诊断源码绿标成生产完成。

## 最终状态纠正

014文件恢复后的实际Resume同样触发Launch Constraint拒绝，不能称回退健康。再次Native停止服务并退出后，launch job不存在、socket不接受连接、App/helper均已退出；final-state.json已核对。当前运行阻断保留，原六条链路只保留历史通过证据，不表示当前App可用。

## 同身份移动015包已就绪

固定路径014→015更新仍失败（精确LS唯一URL仍c5p1m1e0），未算维护10通过。为继续功能验收，将015原样同卷移动到goal-native-runtime015-20261111，同身份而非新身份；实际App75559/helper76077/worker76080/v12official76867健康。assert-runtime正例通过，官方Ed25519签名feed与installed manifest一致，受保护五表不变。当前Native已触发真实pi重新验证但尚未显式启用，尚未启动新图谱任务。
