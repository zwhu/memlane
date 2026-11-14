# Codex 文件修改证据与知识位置事实

关联MA01／MA05及 **MA-CODEX-FACT01**，原累计失败1轮保留。目标是普通Codex编码的官方知识正确表达真实源码位置，并被新会话采用；输入渲染通过不能代替模型质量通过。原错误知识及旧会话材料保留，不手动改知识、补采或重放历史。

最新检查点二十三：目标位置关系的真实复验通过，历史失败1轮保留。旧错误正文已在替换前留存取证；本轮官方状态更新替换旧Data后，其原ID读取404，不能继续称旧Data仍在当前库中。下方检查点二十的未结案状态保持为当时事实。

## 已证事实与诊断边界

固定官方Codex插件把FileChange的unified_diff放入Edit输入，但返回值仅为`completed`。固定Cognee的紧凑Edit输入只处理old_string／new_string，不渲染diff。复现原插入形状，经实际准备插件及固定SDK渲染后，整行只剩`Edit retry_config.py [success] | output: completed`，新增函数与相邻函数均消失。记录中的错误“after describe_retry”已经出现在官方workflow候选，早于curator／writer；以上只能确定修改证据缺失，尚不能认定它是模型错误的唯一原因。

定位反馈命令：`python3 scripts/task.py -- .local/cognee-python/bin/python -m unittest discover -s engine/python -p 'test_codex_trace_patch.py'`。修复前1项失败（7.554秒），明确断言`retry_indices(value)`不在实际渲染行；第一版修复后1项通过（7.304秒），后续加入边界检查。此本地输入回归不调用模型，不增加一次真实知识质量失败轮次。

## 实现边界

在新不可变Codex安装代际叠加严格锚点补丁：FileChange更新结果的status与原始unified_diff作为工具返回结果保留，输入diff仍逐字不变。原事件ID、每文件一次映射、状态、隐私过滤及4KB输入／8KB返回值上限保持。未知、空diff、增删文件继续原处理；多段diff原样保留，不拼造old_string／new_string、不拆出虚构工具调用、不读取当前磁盘去猜历史文件。

只修改派生Codex插件，补丁摘要进入generation与receipt核验；固定官方输入、官方引擎archive和提示词不改，旧启用缓存不原地替换，其他宿主的转换保持。实际SDK仍对工具返回值截取600字符，较长diff可能只部分呈现，不能承诺任意修改都完整进入模型输入。

## 连续验收与保护

先冻结源码检查，再正常停止已结束任务的自有Codex后台／监视器，确认旧消费者退出；原库pending0后正常重启控制进程加载新构造器，经原生安装器准备、审阅、启用新代际。随后普通新会话完成等价复杂度的新增函数和测试任务；核对实际FileChange→官方trace→知识中的源码位置、行为、测试事实，再由新会话自动采用。每次实际知识质量失败继续同一编号计数，达到4轮即停止，不靠换profile／模型／操作ID重置。

原错误Data `ca3ba590-cebb-5c15-a0dd-0b53d305b347`、旧采集水位、待发buffer、子主体、ACL、原日常auth／config和当前DeepSeek处理配置保留。用户已撤销重置卡授权，由用户手动重置；不为验证自动切模型。下方额度条件仅为历史执行状态。Cursor、原生三类供应商、Desktop及已停止旧helper／图谱问题各按原边界保留。

## 检查点二十结果与未结案项

最终7项渲染／边界回归进入后端全量140项检查，127.441秒通过。覆盖实际Codex调用入口、输入不变、多文件／多hunk、错误状态、缺失diff／增删、原生事件ID、隐私过滤在字节截断前执行、固定输入不变和其他宿主转换保持；本地渲染子进程禁止网络。正常服务重启及原生插件准备／审阅／启用后，真实Codex新增retry_index_pairs，独立25项测试通过，官方保存QA1／trace5；实际官方trace返回值完整保留原diff，固定SDK渲染确实显示新函数及后继first_retry_index。

官方生成3条知识，函数所在文件／复用关系和25测试基线与源码一致。全新Codex会话的原生记录及官方记录均为工具0次，自动注入含新函数及25项基线；回答正确采用项目默认3而非通用5，并对知识没有提供的相邻顺序回答不知道。[完整脱敏证据](evidence/multi-agent-development-20261114/codex-file-change-evidence.json)。没有手动保存、更正或删除知识。

**原MA-CODEX-FACT01仍未结案，累计失败1轮。** 此新样本没有输出相邻函数顺序，不能把省略错误表述当作原相邻关系的正面复验。已确定的输入丢失子问题修复与原模型位置错误分开：旧错误Data正文摘要完全相同，仍保留；没有完成针对原关系的新验收轮次，不能将它改为通过，也不因新样本／session／处理协议而清零。后续需要直接覆盖原位置事实；当前证据不证明模型错误唯一由此缺口引起。

两项执行方法纠正留痕：先前按mtime选中了空会话，前置检查失败后停止步骤仍执行；随后核对明确任务ID，所有已执行任务均task_complete，没有发现被中止的在途任务，后续门禁改为明确ID及成功返回码。另一次“未触发整理”的早期快照判断被完整日志纠正：前台退出的官方钩子01:06:39已发起improve，后来01:07:59停止自有后台、官方退出监视器01:08:03命中相同去重marker，没有第二次整理。两者是方法问题，不增加知识质量失败计数；未重投QA或trace。

最后主项目37项处理均完成、pending0，原主体／ACL／旧session映射及日常auth／config保持。原服务、Native、当前Codex后台消费者及所需插件缓存保留；本轮没有Go/UI改动、release或重置卡使用。实测周剩余额度3%，未达到严格低于2%的条件。

## 检查点二十三：直接位置关系复验

在原profile／主体／处理配置与固定派生插件上执行新的真实编码任务：把现有retry_indices从retry_limit之后移到retry_options紧后、describe_retry紧前，函数AST保持不变。新增一条实际AST相邻顺序测试，原25项保持，独立完整回归26项通过。官方保存QA1／trace7，实际Edit返回diff保留删除与新插入hunk；未重放旧材料或手动保存知识。

官方自动整理生成3条新知识，其中同一`footprint:retry-indices`状态明确为“between retry_options and describe_retry”，测试基线更新为26。官方替换旧位置与旧测试基线两条Data，旧ID均404；原错误正文在事前私有回执中按原SHA保存，没有手动编辑／删除／恢复知识。新会话问题不提供预期邻居，原生和官方均工具0次，自动注入新位置并正确回答文件、前后邻居、26项及命令；原错误位置文本未被注入。[脱敏证据](evidence/multi-agent-development-20261114/codex-position-regression.json)。

本轮编码关联的全部pipeline最终succeeded，记录时处理快照40／40、pending0；该快照不承诺后续问答投影永远无后台活动。原主体、项目／通用配置、旧session映射、处理generation与日常auth／config摘要保持。前端0.167.2、后台0.169.0仍属混合版本；前端升级已跳过，没有重置卡、模型切换、引擎／提示词改动或release。只针对已确认task_complete的自有profile正常停止过后台，最终新会话消费者及依赖保留。

方法纠正保留：误用前端不支持的`daemon stop`返回用法错误2，未停止；核实受管0.169.0帮助后以`app-server daemon stop`正常返回stopped。首次原生回执断言失败，随后按同一rollout最终assistant response_item及developer记忆块核对；未重发问题，不推定为事件落盘时机所致。系统Python的只读SQLite打开失败，项目Python的相同只读查询成功，无DB写入。完整日志显示前台关闭整理01:36:54已claim、01:36:57 improve一次，稍后daemon停止的监视器命中去重；不将正常stop误述为唯一整理触发。

**MA-CODEX-FACT01目标关系复验通过，历史失败1轮保留。** 此为真实新调整的限定样本，不证明输入缺口是原模型错误的唯一原因或任意事实都正确。其他较旧footprint仍可能以changed-since警示注入，本轮没有验证其全量新鲜度；真实Desktop、Cursor和三类原生Provider账号仍是独立未完成门槛。
