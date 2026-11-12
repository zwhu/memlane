# SDK结构验证触发默认日志轮转

## 目标、现场与影响

2026-11-12，本轮Goal在核对Cognee1.6.7真实SessionAgentTraceEntry的1000字符／20项限制。受管命令会话93100只构造合成Pydantic对象，没有调用会话写入、模型或正式App。命令只设置临时SYSTEM_ROOT_DIRECTORY，却继承当前HOME和默认日志设置。

实际输出明确记录：上游导入初始化删除`/Users/bytedance/.cognee/logs/2026-08-13_02-23-24.log`，并创建`/Users/bytedance/.cognee/logs/2026-11-12_04-49-22.log`。已观察到的越界影响为一份旧日志被删和一份新日志创建；旧日志内容没有提前备份，当前无法恢复，不能假称无用户文件变化。命令退出0，不代表隔离成立。数据库配置日志指向本次临时目录；本验证没有显式打开日常库或旧31条业务库，不据此外推SDK所有导入副作用均为零。

## 已证原因

本机实际`cognee/shared/logging_utils.py`使用独立的BaseConfig.logs_root_directory（支持COGNEE_LOGS_DIR），并不由SYSTEM_ROOT_DIRECTORY替代。默认开启文件日志时，导入配置会调用cleanup_old_logs，MAX_LOG_FILES=10，按mtime删除超限旧日志。这里只隔离数据库根而误认为整个SDK导入无外部文件影响，是本次操作的错误假设。

## 修正与验证要求

停止继续使用继承默认HOME的SDK导入验证。不清理用户日志目录、不扫描日志正文找“可删”文件，也不通过再次触发轮转复现。下一次真实SDK结构核对由stdlib父进程启动独立子进程：专用HOME／cwd／SYSTEM_ROOT_DIRECTORY／COGNEE_LOGS_DIR，COGNEE_LOG_FILE=false，不带LOG_FILE_NAME、Agent配置或凭据；结束后只清理该临时目录，保留小型结果与源码摘要。先读固定版本日志配置再验证，不能把“只构造模型”当纯函数保证。

这属于验证隔离缺陷，发生1次；不是旧hook桥接或UI失败，未并入其停止计数。用户已在本轮commentary获知。旧日志未恢复是保留影响；修正后是否实际阻止默认目录修改须另留隔离验证证据，本文不把实施意图写为已通过。

## 隔离修正后的验证

受管会话10447在独立子进程HOME／cwd／数据库／日志根、COGNEE_LOG_FILE=false、`-I -B`且没有LOG_FILE_NAME或Agent凭据的环境中复验真实SDK模型。Go待发字段与SDK缓存模型完全一致，1000字符／20项边界和error文本规范均核对；默认用户日志目录仅做名称／大小／mtime只读前后盘点，metadata未变。临时根已退出清理，证据`pi-capture-broker-001/sdk-cache-contract.json`。这证明该隔离复验未观测到默认日志变化，不恢复先前被删日志，也不外推为用户全部文件的完整审计。原31条业务源库SHA另行只读核对未变。
