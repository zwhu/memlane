# 精简版旧 Go 源码退役记录

日期：2026-11-13。状态：下列源码已实际通过 `git rm` 从当前工作树及索引退役；本记录不表示全部运行数据、旧 Python/Rust 链或日常用户流程已完成清理／验收。

## 依据与边界

用户本轮明确授权删除已替代的旧实现、专属测试和夹具。删除前可恢复基线为本地注释 tag `pre-simplification-20261113-000958`，commit `559eab851ec306d44ed2549e4ad575b035b829b6`；没有新快照分支、独立 index 或工作树。历史缺陷和累计失败次数保持原事实，不把旧窄窗问题写成修复通过。

实际检查新 `cmd/memlane-app/main.go` 仅导入 `internal/simpledesktop`；后者不导入任何其他 Memlane 内部包。Native 实施 Agent 已确认新图宿主及 HTTP DTO 均在 `simpledesktop` 内，不依赖旧 `officialgraph`、`platform`、`records`、`agents`。全部已跟踪 Go 文件都在 `cmd` 或 `internal`。删除的每个目录在操作前都没有独立未提交修改；保留当前 Native Agent 的新目录及入口编辑。

## 实际删除清单

| 目录 | 删除路径数 | 其中专属测试或夹具路径数 |
| --- | ---: | ---: |
| `cmd/c-project-agent-check` | 1 | 0 |
| `cmd/memlane` | 1 | 0 |
| `cmd/memlane-graph-dev` | 3 | 0 |
| `cmd/memlaned` | 1 | 0 |
| `cmd/p0-component-check` | 1 | 0 |
| `cmd/p0-memory-probe` | 1 | 0 |
| `cmd/p0-worker-client` | 1 | 0 |
| `cmd/p2-agent-check` | 1 | 0 |
| `cmd/p3-pi-rpc-check` | 1 | 0 |
| `cmd/p3-runner-check` | 1 | 0 |
| `cmd/p3-ui-fault` | 1 | 0 |
| `cmd/p4-code-check` | 1 | 0 |
| `cmd/p4-component-check` | 1 | 0 |
| `cmd/p4-restore-replay-check` | 2 | 1 |
| `cmd/pi-model-bridge-check` | 1 | 0 |
| `internal/agentbridge` | 16 | 8 |
| `internal/agents` | 117 | 56 |
| `internal/codegraph` | 9 | 4 |
| `internal/components` | 6 | 3 |
| `internal/credentials` | 4 | 1 |
| `internal/daemon` | 88 | 48 |
| `internal/desktop` | 245 | 120 |
| `internal/engine` | 6 | 2 |
| `internal/memory` | 2 | 1 |
| `internal/modelbridge` | 3 | 1 |
| `internal/official` | 43 | 22 |
| `internal/officialgraph` | 4 | 1 |
| `internal/platform` | 2 | 0 |
| `internal/processing` | 19 | 10 |
| `internal/records` | 50 | 4 |
| `internal/recoveryfiles` | 4 | 2 |
| `internal/serviceipc` | 33 | 1 |
| `internal/store` | 159 | 74 |

合计删除 33 个旧源码目录、828 个已跟踪路径。按职责退役，没有按修改日期批量删除。完整逐文件列表可从 `git diff --name-status pre-simplification-20261113-000958 -- cmd internal` 获取，删除项标记为 `D`。

## 退出的职责与调用链

- `cmd/memlaned` 和 `internal/daemon`：旧业务 daemon、受管任务与自动处理编排、权限、双库同步、outbox、旧图索引调度及服务端包装。
- `cmd/memlane`、`internal/serviceipc`、`internal/agentbridge`、`internal/agents`：自有内容 MCP/IPC、采集 hooks、pi 自有扩展、二次注入、官方插件再包装与旧 Codex/pi 桥。新的 Python completion 端点独立使用固定协议，不导入这些 Go 包。
- `internal/store`、`records`、`memory`、`engine`、`recoveryfiles`：客户端 SQLite 内容、版本、审核、权限及任务状态、独立原文索引和备份恢复业务。
- `internal/desktop`：旧 UI、手动内容编辑／恢复／审核／计划、多模型配置账本、权限界面、窄窗口修补、旧刷新与迟到结果路径及其专属测试／夹具。新只读界面在 `simpledesktop`，由 Native 执行者做针对性检查与实际窗口观察。
- `internal/codegraph`、`officialgraph` 和 `cmd/memlane-graph-dev`：旧自绘／宿主与自有代码图任务驱动。保留 `web/official-graph` 的固定官方 vendor、许可证及当前 Native 实际使用的资源。
- `internal/components`、`credentials`、`modelbridge`、`processing`、`platform`、`official`：只服务旧 daemon／UI 的组件管理、配置账本、多处理路线、旧服务调用包装与旧宿主。当前启动器直接使用固定官方 API 和 Python completion；未保留可重新开启的旧兼容命令。
- 所有表中 `*-check`、`p0-*`、`p2-*`、`p3-*`、`p4-*` 和旧 worker/probe 命令随被取消职责退出。删除旧试验入口不代表重跑或推翻历史试验结果。

## 保留与后续所有权

- 保留 `cmd/memlane-app`、`internal/simpledesktop`、官方图 Web 组件及本次 Python completion／官方原始服务启动模块。
- 未操作 `build/`、`.local/`、Agent 本机配置、真实数据库、官方数据快照、共享模型或组件；没有删除任何运行包、资源、二进制或签名。源码退役不解除真实运行依赖保护。
- 保留全部历史缺陷 `docs`、停止问题证据及源码 tag；只新增本记录。
- 主执行者拥有 `scripts` 及旧 Python/Rust 入口退役、真实运行切换和内容数据删除；它们不因本次 Go 删除自动记为完成。主执行者最终执行 `go mod tidy` 收口旧 SQLite/MCP 等依赖。
- Native 执行者负责新入口必要编译与针对性测试；不运行已取消功能的全仓旧验收矩阵。源码删除证据不能代替 D01–D08 的真实用户流程。

## 当前消费者夹具调整

新 Python completion 的 Schema 转换检查仍消费原 `internal/agents/testdata/coding-memory-schemas-ec6f408` 中五类固定官方 Schema。已将五个 JSON Schema 和 manifest 共六个小文件从基线提取到 `engine/python/testdata/coding-memory-schemas-ec6f408`，逐一校验 manifest SHA256，并将新测试改指该目录；未恢复旧 `agents` 实现。文件仅为既有合成官方请求的结构，不含提示、响应、聊天或凭据，manifest 保留原说明和固定官方版本。

`internal/desktop/build/graph-submission-20261111/headless` 仍有六张 Git 忽略的历史截图，保留为既有证据；目录不再包含旧源码。没有因源码删除一并移除这些历史失败／观察材料。

夹具移动后执行 `python3 scripts/task.py -- .local/cognee-python/bin/python -m unittest discover -s engine/python -p test_codex_completion_endpoint.py -v`，10项通过（包括新位置五类官方 Schema、合成stdio及HTTP生命周期）。未调用真实模型或日常服务；新Go入口的编译与界面检查仍由Native执行者负责。
