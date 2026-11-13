# 多 Agent／Provider 设置的实际 Native 连续核对

2026-11-14，dev。使用当前Go源码调试编译、现有MyGo调试资源的独立验收窗口，bundle为`io.github.zwhu.memlane.validation20261114`；不是release或完整引擎运行包。窗口1200×820 DIP，CUA截图2400×1640像素；实际查看表单、滚动、状态和按钮，截图为本轮CUA会话内图片，未导出仓库PNG。原Memlane Dev窗口与日常服务保持运行。

## 问题与修复

| 编号 | 第一轮实际问题 | 修复与第二轮 |
| --- | --- | --- |
| MA-CONNECTION-API01 | API模式runtime没有模型桥auth_home／codex_executable时，连接列表遗漏已安装Codex | 电脑级连接独立读取agent_hosts.codex，兼容旧字段并探测默认宿主；新profile无须登录或预先创建，发现不写文件。缺可执行文件显示禁用项，异常目标按Agent单独报需核对。旧Codex ID、旧连接API及原配置保留；Native出现Codex，尚未连接状态正确 |
| MA-UI-VALIDATION-RESTORE01 | 官方已有失败验证，但重开编辑页清空验证状态和操作编号，无法直接看到原错误 | 按配置digest及登记revision恢复唯一最新原回执，不调用模型。Native重开后显示“验证未通过／API密钥无效”，核对原验证后仍只有原1次请求 |

两问题分别累计失败1轮、复验通过，失败数不清零，不与原密码框MA-UI-KEY01或其他停止项合并。没有重建主体、迁移数据或重跑相同模型来恢复画面。

## 连续结果

1. 原隔离库17条Data处理完成、pending0；正常dev stop确认退出，再正常启动加载管理候选。只改本轮隔离配置，不启停日常服务。
2. 实际连接页：Codex可选、pi／TraeX为已有新会话回执且不可重复提交；Claude尚未连接，Cursor未安装。Codex目标为本轮隔离且未创建的profile；发现后该目录仍不存在，runtime依然没有桥接auth_home，未触碰真实Codex配置。
3. 添加兼容Provider，地址为自有合成401端点、密钥为明确合成字符串。保存后AX密码值为空，配置已保存、active仍为DeepSeek，未验证候选不能设next。
4. 明确点击验证，固定官方adapter实际访问合成端点一次，返回401。失败候选的next不可用；源码修复后用同一个原验证操作重开编辑页，显示固定中文错误。点击核对只GET回执，端点计数保持1；没有真实供应商模型调用或重验证。
5. 打开原DeepSeek配置，密钥仍不回填，恢复其已有通过记录；点击“下次启动使用”，顶部分别显示当前与next，原active未变。**这里选择的是同一已生效配置**，不把它当不同Provider重启切换证据；不同候选启动的既有实链另循检查点五及启动门禁测试。

方法错误：第一次Cmd+Q后紧接CUA AX观察，工具重新启动调试bundle但没有dev环境变量，出现“运行配置不可用”。已查新PID确无MEMLANE_DEV_RUNTIME_CONFIG；正常退出这个自有进程，并按自有dev launcher携带原配置重开。它不是原配置丢失或服务失败，不计产品恢复失败。后续退出用进程所有权与正常退出核对，避免AX观察隐式重启。

## 检查、资源与边界

连接18项、Go定向通过；全量后端113项、Go全量与vet通过。全量之后又将异常Codex目标的检查放到view／prepare前置，确保其他Agent仍可显示、且创建主体前拒绝坏目录；最终18项定向通过，未重跑无关矩阵。这个异常目录扩展为源码／定向证据，不冒充新的Native坏目录场景。

[脱敏回执](evidence/multi-agent-development-20261114/native-settings-checkpoint.json)。CUA只控制独立验收窗口。合成401端点确认完成1次请求后按所属PID停止，组不存在，一次性脚本已移除；保留小型计数回执。独立Native窗口与最新调试资源保留给后续批量接入／未知操作场景。隔离服务仍使用原库、两独立主体、现有embedding，留给后续原Codex回归；真实私有凭据只在保护根，不提交。

本轮未验证：Native批量取消／部分失败、实际未知写入重开恢复、项目checkbox连续性、Codex CLI／Desktop采集召回、其余供应商与宿主。原Codex产物manifest及数据未改，不恢复任何历史达到停止阈值的样本。
