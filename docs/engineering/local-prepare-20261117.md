# 本地首次准备续作（2026-11-17）

用户要求阅读当日handoff并继续。承接 `two-session-handoff-20261117.md`，恢复本次本地方案，不恢复历史全量Goal、release、Docker或已停止模型实验。B的未提交发现修复及所有既有材料保留。

## 影响及连续用例

完整目标：缺少Python/Cognee/存储 → 显式准备受管组件 → 初始化唯一日常存储与owner认证 → 配置处理方式 → 正常启动 → 指定Agent实际加载、保存和新会话使用。

先实施可复用的组件准备入口；它准备独立Python、固定依赖及官方原版源码，不创建数据库、用户或记忆，不修改Agent配置。正常start继续拒绝缺库。后续首次存储初始化须单独接入官方迁移与owner/API key生成，并保护已有日常库；不能用mkdir(system,data)代替初始化。UI复用现有初始化承载，尚未更改。

| 场景 | 标准／验证方式 |
| --- | --- |
| 无Python、无Cognee | 受管uv下载固定Python 3.12.12、独立venv、hash锁定依赖和固定归档；实际空组件目录验证与源码夹具分别报告 |
| 下载或安装中断后继续 | 已完成组件逐项校验，失败步骤可重新执行；状态不提前标ready，保留已完成部分 |
| 已有未知目录／symlink／并发准备 | 拒绝接管或覆盖；准备锁覆盖整轮；不接触单一日常库 |
| 依赖完整但源码被修改 | 拒绝报告完成，不静默使用被修改源码 |
| 常规启动缺库 | 沿用dev.py原保护；prepare不将缺库变成正常空库 |

实现范围：scripts/local_prepare.py、dev.py显式prepare入口、定向用例与开发说明。本轮公共action.lock由本续作持有；不改服务／插件接入源码及其他会话材料。临时组件验证结束清理本轮独占载荷，保留小型回执，不长期留下重复环境。

## 现场

- 日常库system/data存在；原owner92567及登记子进程不在，存储锁可取得，service/completion端口未监听。未删除旧owner、未重启服务，原退出原因未知。
- 存在Codex旧exit-watcher，包括日常插件消费者；保留现场，不强杀、不绕过激活409。
- 磁盘初始约105.08GiB。已核对无Go编译进程且go-build不属于runtime依赖；清理可再生Go编译缓存，未动运行包、库、source、模型或buffer。
- INIT-PREPARE01历史失败1轮；本轮新增结果在下方记录。MA-OLLAMA-CODING-QUALITY01继续累计4轮停止。其他问题沿原账本。

## 已实施与定向检查

- `dev.py prepare --component-root ...`独立于runtime加载，使用已安装uv；固定Python 3.12.12、仓库requirements.lock哈希及service归档／源码。正常start/setup行为保留。
- 私有组件目录／固定输入标记、全程排他准备锁、逐阶段原子状态、下载摘要与安全解包；拒绝接管已有库、未知目录、symlink及日常库内部新组件路径。重跑重新校验，依赖安装在本轮独占venv内，不修改现有日常Python。
- 真实固定归档含与引擎无关的`.claude/skills`链接；解包只取cognee、cognee_db_workers、kuzu及pyproject，运行包树内的链接或任意路径穿越仍拒绝，后续使用现有官方source校验。
- 34项Python定向检查通过（1.496秒）：新准备保护／状态、dev回归、Agent发现与环境。CLI夹具的components_ready是模拟返回，不能作为真实安装通过。没有Go／UI改动，不重复Go或Native验收。

真实组件目录为`.local/local-prepare-validation-20261117`，不是第二内容库；uv下载的Python已实际创建，版本校验通过，依赖安装的最终结果待补。私有日志为`.local/official-agent-convergence-20261117/local-prepare-round2.log`。

具体问题：`INIT-PREPARE01`原缺失首次准备的1轮失败仍未闭环；本轮新增`PREP-DEPS01`1轮失败，原因已证为实现额外限制“只接受wheel”而固定langdetect1.0.9无可用wheel。保留round1状态，撤销该额外限制，安装源包仍要求原requirements哈希，build约束沿相同锁。它不是重新执行旧模型问题，也不将整个初始化页的不同缺陷合并累计。后续若再失败继续沿PREP-DEPS01计数。

当前dev入口仍依赖已安装uv；托管安装器／无uv首次引导、embedding模型准备、官方存储迁移与owner/API key、初始化UI均未完成。不将当前组件阶段当作上述目标完成。

第2次真实准备在依赖下载阶段由本轮主动中止（已核对PID15068的完整命令后仅停止该轮uv；日常服务未停）。原因是初版禁用缓存造成重复下载；已实际保留Python、失败步骤及SIGTERM回执，不把该次中止写成通过，也不认定为依赖功能再次失败。日志round2和round2-status保留。随后组件安装改为复用仓库`.cache/uv`，命令行显式带cache路径，供磁盘保护识别正在使用的缓存；固定依赖hash及构建约束不变。第3次准备是接续相同组件目录，结果另记，不清零PREP-DEPS01原失败。
