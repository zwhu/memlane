# 架构四阶段改造与文档/插件收口记录（2026-11-20）

## 1. 背景与范围

在完成 agy 终端聊天采集路径修复（`f1ea860`）后，针对全仓结构与运行稳定性推进四阶段重构，并按确认完成本机线上 `memlane-cognee` 插件升级、过时/冗余文档清理与日常入口兼容性修复。当前仍处于 `dev` 阶段，未执行 `release` 打包，未触碰 `build/`、`~/.local/daily/official-store` 数据主体或远端推送。

## 2. 改造内容总览

### Phase 1：止血修复与 Antigravity Hook 可观测性
- **缓存渲染期阅读身份**：在 [app.go](../../internal/simpledesktop/app.go)、[reading_titles.go](../../internal/simpledesktop/reading_titles.go)、[reading_loading.go](../../internal/simpledesktop/reading_loading.go)、[knowledge_unread.go](../../internal/simpledesktop/knowledge_unread.go)、[project_queue_badge.go](../../internal/simpledesktop/project_queue_badge.go) 中缓存 `readingIdentityPair`，避免渲染帧重复读取凭证并计算 SHA256；补充 [reading_identity_cache_test.go](../../internal/simpledesktop/reading_identity_cache_test.go)。
- **并发数据竞争与环境切换修复**：修复 [integrations.go](../../internal/simpledesktop/integrations.go) 后台 goroutine 读取共享配置及 [overview_ui_test.go](../../internal/simpledesktop/overview_ui_test.go) 测试夹具回调竞态；修复 [generation_handoff_runtime.py](../../engine/python/generation_handoff_runtime.py) 离开 Ollama 时清除残留 `OLLAMA_API_BASE`（回归见 [test_generation_handoff.py](../../engine/python/test_generation_handoff.py)）。
- **Lint 与内嵌资源护栏**：加固 [scripts/lint.py](../../scripts/lint.py)（`ML004`/`ML005` 丢失目标即失败并扫描 `scripts/*.py`，单测 [test_lint.py](../../scripts/test_lint.py)）；补齐 [runtime_assets.go](../../runtime_assets.go) 内嵌 `dev_recovery.py`/`local_prepare.py` 并新增 [runtime_assets_test.go](../../runtime_assets_test.go)。
- **Antigravity Hook 失败留痕**：在 [antigravity_hook_policy.py](../../engine/python/antigravity_hook_policy.py)、[antigravity_plugin.py](../../engine/python/antigravity_plugin.py)、[antigravity_connection.py](../../engine/python/antigravity_connection.py) 与 [agent_capture_ui.go](../../internal/simpledesktop/agent_capture_ui.go) 接入 `last-hook-error.json`（私有 `0600` 仅元数据环形留痕），单测见 [test_antigravity_connection.py](../../engine/python/test_antigravity_connection.py) 与 [agent_capture_test.go](../../internal/simpledesktop/agent_capture_test.go)。

### Phase 2：Cognee 与控制面三层运行时监控
- **控制面采集与接口**：新增 [service_monitor.py](../../engine/python/service_monitor.py) 并在 [project_control_server.py](../../engine/python/project_control_server.py) 挂载管理端 `GET /v1/runtime/monitor`，覆盖进程存活/RSS/CPU 与收据检查、防抖 `/health` 状态、磁盘余量（50/55 GiB）、脱敏尾日志及业务摘要；单测见 [test_service_monitor.py](../../engine/python/test_service_monitor.py)。
- **桌面端监控与设置页呈现**：新增 [service_monitor.go](../../internal/simpledesktop/service_monitor.go)、[service_monitor_ui.go](../../internal/simpledesktop/service_monitor_ui.go) 与 [service_monitor_test.go](../../internal/simpledesktop/service_monitor_test.go)，接入 [app.go](../../internal/simpledesktop/app.go)、[overview_navigation.go](../../internal/simpledesktop/overview_navigation.go)、[settings_connection_cards.go](../../internal/simpledesktop/settings_connection_cards.go)，保留侧栏 `"服务 · 已连接"` 与服务页边界、崩溃不静默自启且不清空正文。

### Phase 3：Go 与 Python 模块边界收口
- **Go 传输层、路由与异步操作日志**：新增 [http_transport.go](../../internal/simpledesktop/http_transport.go)、[http_transport_test.go](../../internal/simpledesktop/http_transport_test.go) 与 [route.go](../../internal/simpledesktop/route.go)，收敛 [client.go](../../internal/simpledesktop/client.go)、[common_knowledge.go](../../internal/simpledesktop/common_knowledge.go)、[project_access.go](../../internal/simpledesktop/project_access.go)、[project_create.go](../../internal/simpledesktop/project_create.go)、[reading_presentation.go](../../internal/simpledesktop/reading_presentation.go)、[project_directory.go](../../internal/simpledesktop/project_directory.go)，将 `rememberKnowledgeDays` 移出传输层（显式保留在 `client.go` 与 [overview.go](../../internal/simpledesktop/overview.go)），并将操作记录读写剥离到 [generation_operations.go](../../internal/simpledesktop/generation_operations.go)。
- **Python 公共基建**：新增 [\_runtime_common.py](../../engine/python/_runtime_common.py) 与 [test_runtime_common.py](../../engine/python/test_runtime_common.py)，统一 `private_write`、`CachedSecretReader`、`bytes_sha256`/`file_sha256`、`replace_exact` 及历史插件清单匹配至 [project_control.py](../../engine/python/project_control.py)、[first_install_store.py](../../engine/python/first_install_store.py)、[project_capture_patch.py](../../engine/python/project_capture_patch.py)、[project_control_server.py](../../engine/python/project_control_server.py)、[project_installation.py](../../engine/python/project_installation.py) 与 [dev.py](../../scripts/dev.py)。

### Phase 4：Go 原生进程守护（`internal/supervisor`）与开发工具兼容性
- 新增纯标准库包 [internal/supervisor](../../internal/supervisor/supervisor.go)：包含 `.service-owner` flock 单实例锁与死 owner 安全恢复（[owner.go](../../internal/supervisor/owner.go)）、保留活 inode 的 20 MiB 日志截尾与脱敏（[log.go](../../internal/supervisor/log.go)）、`Setsid` 独立进程组启动/逆序 `SIGTERM -> SIGKILL` 清理与退出捕获（[process.go](../../internal/supervisor/process.go)），及防抖健康监控（[supervisor_test.go](../../internal/supervisor/supervisor_test.go)，含与 `bootstrap.FirstRuntimeReady` 跨包兼容性验证）。
- 在 [deps/cognee-rs/README.md](../../deps/cognee-rs/README.md) 顶部标明旧 Rust 构建脚本已退役。
- 修复 [scripts/dev.py](../../scripts/dev.py#L540-L543) 在非英文 locale 终端下对英文 `lstart` 启动时间的校验失败（兼容默认输出与 `LC_ALL=C`）。

## 3. 本机线上 Antigravity 插件升级与文档清理

- **本机插件升级**：重启日常服务后（新 owner PID `92190`、`GET /v1/runtime/monitor` 返回 `state: "ready"`），按受管流程依次执行 `review` -> `prepare` -> `activate`（管理修订号 `192 -> 198`）。升级后 `~/.gemini/config/plugins/memlane-cognee/scripts/agy_hook.py` 与 `_memlane_antigravity.py` 已包含 `record_hook_error` 留痕，`.memlane-installation.json` 中首次边界 `activated_at: 1795061757.052684` 保持不变，`/v1/connections` 状态为 `configured_pending_new_session` 且 `last_hook_error: null`。
- **文档与冗余目录清理**：移除已重复的历史设计归档 `docs/design-archive-20261114/`、空壳/遗留产物 `internal/desktop/`、`output/`、`internal/simpledesktop/docs/`、`docs/.DS_Store`，删除 5 份已过期一次性协调/排查短文档并精简 `graph-page-20261115/` 与 `tool-graph-compact-20261116/` 中的重复全量夹具截图；将 `memlane-design-spec-v3.html` 与 `research-environment-primary-sources-20261117.md` 归位并同步链接。

## 4. 验证与已知边界

- 已执行并全部通过本轮全部静态门禁与单元测试，最终提交前复跑 `scripts/lint.py`、`scripts/test_dev*.py` 与 `sh scripts/go.sh test ./...`；`-race`（`memlane`、`bootstrap`、`supervisor`、`simpledesktop` 0 竞争）于 Phase 4 收口时通过。
- **未覆盖边界**：本轮未在真实桌面窗口上执行 Native 视觉对比验收，也未擅自修改与本轮无关的 2 个旧 Python 测试失配（`test_project_tool_patch.py:168`、`test_project_plugin_patch.py:572`）。
