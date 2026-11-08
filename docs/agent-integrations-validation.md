# 官方 Agent／Cognee 本机验证

2026-11-08。独立验证切片，开始基线为 `main@0a82942ad8fab48ecba664c7fb0797b911deded0`。旧全量工程 Goal 仍暂停；本记录不选择日常自动记忆策略，也不收口 [Q1–Q4](product-questions.md)。证据只包含本轮合成材料，位于 `build/agent-integrations-validation/`，不进入提交。

## 当前状态

全链路尚未通过。已核对基线、隔离安装官方插件，并解决当前模型与 PATH CLI 的版本不兼容。官方任务采集、持久同步、Memlane 转换、真实 App、Rust 当前索引和新会话召回仍需实测。

| 对象 | 本机事实 | 证据与边界 |
| --- | --- | --- |
| Memlane | 开始时本地 HEAD 与 origin/main 均为 `0a82942` | `baseline.json`；F1e、图标、文档等既有修改包含暂存内容，本切片按文件提交，不混入 |
| 官方上游 | `997fd6f72253aa97b49996eb1b032793f0257935` | 本轮真实远端查询与隔离 checkout；`versions-and-install.json` |
| Codex Cognee 插件 | `1.7.7`，官方 marketplace 安装 | `plugin-install.json`；安装文件逐项匹配固定上游，安装不代表 hooks 触发或记忆成功 |
| PATH Codex | `0.147.0`，ChatGPT 认证，选择 `gpt-6-astra` | `model-preflight.jsonl`：真实 HTTP 400，要求新版 Codex；探测关闭 hooks，仅核对模型条件 |
| 现有 Codex App CLI | `0.167.0-alpha.3.2` | 已安装于 Codex App 的 `codex-cli/CodexCLI.app/Contents/MacOS/codex`；`app-codex-preflight.jsonl` 保持同模型成功；没有升级或改日常模型 |
| 参考页面 | `http://localhost:3000/integrations` 返回 200 | 相邻源码 `e93a4f0c76e6af27142c25189f14c200e1749d51`；仅参考，不作为写入目标 |
| 相邻 Cognee | 源码版本 `1.5.4`，其 venv 实际安装 `1.5.3` | 版本声明和安装版本分别核对，不能据源码版本声明实际服务已升级 |
| 本轮独立 Cognee | 复用仓库已有 Python `1.6.7` / FastEmbed `0.9.1` / Uvicorn `0.54.0` | 独立管理的 `http://127.0.0.1:18011`，新合成数据根；与插件自举固定 `1.6.5` 区分，兼容性仍按实际请求验证 |

## 验收矩阵

| 切片 | 所需证明 | 当前状态 |
| --- | --- | --- |
| 官方接入 | 官方安装、真实加载、实际 hooks 和服务回执 | 安装通过，其余待验证 |
| 采集与同步 | 正常任务的 QA／工具轨迹、实际结束操作、持久记录与处理状态 | 待验证 |
| Memlane 衔接 | 受信接收、来源／项目／具体版本／稳定标识；保存与索引分别核对 | 待实现／验证 |
| App 体验 | 完整包、真实记录／原始出处／会话／范围／保存和索引状态、系统截图 | 待验证；设计图已实际打开，不能据此宣称实际窗口通过 |
| Rust 当前检索 | 实际转换后的当前知识 revision 经本轮 worker 查询 | 待验证 |
| 新会话召回 | 无 resume、无首会话历史和答案文件，正式 recall 的请求／来源／实际回答 | 待验证 |
| 重放与隔离 | 同步重放不重复知识／版本／活动；两个真实合成工作区和独立范围 | 待验证 |
| 取消与故障 | 真实取消、撤销后迟到拒绝、已提交事实保留；故障缓冲与有限重试 | 待验证；故障夹具另行标注 |
| 清理与保留 | 结束测试采集进程，日常配置保持原样，保留有效体验数据并提供启停命令 | 待验证 |

## 已核对的数据流与最小衔接缺口

官方 `hooks.json` 绑定 SessionStart、UserPromptSubmit、PostToolUse、Stop、PreCompact 和 SessionEnd。Codex 的 prompt 先暂存到插件自己的 pending 状态，Stop 将 prompt 与实际回答配成 QA；工具轨迹另行提交。真实调用入口为 `POST /api/v1/remember/entry`，包含 dataset／session 和 typed entry；正式召回为 `POST /api/v1/recall`。服务端具体 entry ID 与持久状态仍需取得本轮回执。

结束同步走官方 `POST /api/v1/improve` 的 session-aware 路径。Python 1.6.7 的实际安装源码分别处理会话 QA、工具轨迹、上下文、提炼、偏好等阶段；有写入副作用，仅允许在本轮隔离材料与数据中执行。HTTP 200、后台 running 或插件一行 success 不能代替阶段结果、数据库记录及正式 recall。会话层采用 SQLite 时也有 TTL，持久会话缓冲、图谱持久化、提炼与可检索必须分开报告。

Memlane 的 `memory_save` 已由受信服务检查连接代次／范围／真实工作区，按连接和稳定 operation ID 去重，保存不可变版本／原始来源与索引队列。官方会话／处理结果目前没有现成同步入口；普通 Memlane MCP 保存验证不能替代官方集成。

拟采用最小转换：首先保留官方实际服务记录与稳定出处，原始 QA／轨迹作为 raw；只有可追溯具体原始记录的知识进入 knowledge 和当前版本索引。知识摘要需复用既有派生来源契约，同源转述不增加证据根。外部 dataset／session／entry／data 标识与 Memlane project／memory／revision 的绑定必须经过受信服务，取消和范围撤销后复核；不能由模型指定项目，也不能只在客户端做去重。该段是待实施契约，尚无实际转换／Rust／App 通过证据；取得官方实际产物后再确定必要增量，避免预建整个平台。

## 隔离和失败历史

`fixture-path.txt` 指向本轮新合成根，含两个 Git 仓库、新 Cognee 数据目录以及仅复用已验收组件缓存的空 Memlane 数据环境。`isolation-settings.json` 记录服务和插件配置路径。Codex 只在本轮 HOME／配置目录安装插件，认证文件只由 Codex 读取自己的临时链接。Cognee 服务读取既有 Cognee 模型设置到进程环境，不获取 Agent 登录凭据，不输出密钥。代码自动索引关闭，跨 Agent 共享范围关闭；不修改日常采集策略。

`setup-failures.json` 保留源码下载超时和一次本地临时 launcher 路径错误。固定 1.6.5 的额外安装／下载已停止，没有作为当前服务或已完成源码验收；改为复用实际已安装的 1.6.7。`model-preflight.jsonl` 保留旧 PATH CLI 的真实模型失败，不用后续成功覆盖历史。

本轮有效 App、截图、Rust／正式 recall、取消和故障证据齐备前，不提供空壳 App 作为体验成果。最终启停命令、数据保留方式、提交／远端与临时 handoff 回执在后续实际验收后补充。
