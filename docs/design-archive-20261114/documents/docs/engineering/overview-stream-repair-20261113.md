# OVR01 标准流修复记录

同一问题沿用总览账本 OVR01，不创建新编号，不重置失败计数。原始事实见 `overview-execution-20261113.md` 的 OVR01 段落。当前是源码修复及独立回归，真实服务恢复仍未验收。

## 目标与依据

长期官方服务不依赖启动终端的管道读端；图 worker 启动前刷新 stderr 不应因观察者退出而失败。总览已证明官方进程与 launcher 继承同一失效 PIPE，异常链为 `Process.start → popen_fork → multiprocessing.util._flush_std_streams → stderr.flush → BrokenPipeError(errno32)`。管道读端由谁、何时关闭，以及旧 Native watcher 是否同因退出，仍未知。

诊断假设排序：失效 stderr 继承（已有真实日志、FD 和 stdlib 复现支持）；业务正文或模型导致（不解释 worker 启动前 flush 失败）；服务健康接口可替代业务健康（已被实际失败否定）。没有为此重发原召回、整理或索引任务。

## 红证据与修复

`scripts/test_dev_streams.py` 在 launcher 的 stderr 接入已关闭读端的 OS pipe，再通过实际 `launch_owned` seam 启动只调用 stdlib flush 的子进程。修复前输出 `BROKEN_PIPE_STDERR_FLUSH`、worker_exit=17，预期 0 的断言失败。夹具没有 Cognee、正文、数据库或模型调用。

`scripts/dev.py` 现在要求长期子进程使用明确日志文件：0600、O_APPEND、O_NOFOLLOW、常规文件检查，stdout 与 stderr 共用独立文件 FD，Popen 后父进程关闭自己的 FD。官方、completion、project_control 各有日志。超过 20 MiB 时留末尾 20 MiB 为 previous，截短同一 inode，保留运行子进程的写入位置安全；主循环和附着 UI 等待循环执行边界检查。生命周期提示捕获 BrokenPipeError，观察终端退出不传播成后台退出。

同一测试修复后 worker_exit=0，日志同时含 pending stderr 和 worker_flush_ok。另核对 0600、运行 FD 指向原 inode、截短后可继续追加，以及通知读端缺失。系统 Python 启动器检查共 15 项通过。

## 真实运行边界、计数与待办

本次没有停止／启动日常服务，没有切换 Agent 凭据，没有重放未知任务。旧进程仍运行旧 FD，源码通过不能证明它已经修复。修复前红测试及其后定向检查属于同一源码诊断轮，未新增真实业务验收轮次；原总览和阅读依赖的失败记录保留，不能以日志中的多个调用推定四轮验收。

运行授权来自任务附件第 4 节，明确要求后台启停排他交接。总览 session 已退出且未交接后台启停；已向用户提交同库重启交接问题，未收到答复前不实施。待交接后重新核对 PID／启动时间／锁／原库与凭据引用，使用 dev.py 应用修复和新控制进程；先查原任务，再执行一次有明确身份的真实链路验证。不得为补证据盲重发原任务。

## 收到交接后的恢复证据

用户明确答复“交接并同库重启”。旧launcher69475经统一入口停止并确认原库释放；原runtime所有字段保持，新增控制配置，原配置私有备份。新launcher15986（13:16:28）、completion15990、official16019、control16202，phase=ready；原数据samefile、owner引用不变、已配置dataset仍在，控制登记旧会话数为0。三个服务FD1/2经lsof均为REG，路径为私有runtime-logs。

新建一次只读CHUNKS查询（非重放旧请求）于原项目返回200、list长度1、耗时1.678秒，回执 `.local/project-access-evidence/ovr01-restart-search.json`。本次恢复检查通过；不据此外推整理／模型质量通过。旧失败、未知整理任务保留。恢复信息已通知阅读session；Native窗口仍由PA持有，未授权其自动恢复Goal。
