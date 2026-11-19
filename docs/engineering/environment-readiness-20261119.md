# 环境准备现有能力复核

2026-11-19。用户授权继续环境准备与依赖复用、安装确认流程；当前只dev，唯一日常库保持，不另建业务库，不为验收下载依赖或重建索引。

## 当前能力

旧 `local-prepare-20261117.md` 的末尾是当时时点，不能推断当前仍只有CLI。现有 initial_setup、initial_setup_layout、environment_setup_ui 与 bootstrap 已实施正式入口，本轮复用并核对，没有重复开发新安装器。

- 自动发现已有组件和Agent；兼容项复用，外部路径可明确指定。环境检测与Agent排查不要求官方引擎已ready。
- 缺失或不兼容项先形成具体计划，包含来源、下载量及位置；安装操作与 Plan.Digest 绑定。计划改变后原确认失效，必须重新确认。
- 安装前确认与拒绝状态保持；不自动下载。已完成阶段可以保留并继续，不把损坏已有库当成新安装，不覆盖原配置／原库。
- 1200×820确认操作可见，诊断与Agent排查有现有入口。此为组件／行为检查结论，本轮没有新开首次安装Native实例。

## 本轮证据与边界

`sh scripts/go.sh test ./internal/bootstrap ./internal/simpledesktop -run 'TestEnvironment|TestFirstUse|TestReuseAndChangedExternalDependency|TestPlanChangeRequiresNewConsent|Test.*FirstRuntime' -count=1` 退出0，两个包通过；task.py记录可用空间约80 GiB，按50/55 GiB门禁运行。

随后完整Go test/vet与Python lint通过；独立暂存候选检查另留回执。日志在 build/diagnostics/roadmap-reliability-20261119/consolidation-environment-contracts.log 及 consolidation-final-*.log。

本轮确认的是当前源码的复用、确认与拒绝契约；没有执行新依赖下载、严格无缓存冷启动、完整包或release验收。这些边界不能由日常已ready服务代替。当前既有模型、组件、官方库和用户登录保持。
