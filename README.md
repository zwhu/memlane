# Memlane

面向程序员的 macOS 记忆客户端，让编码 Agent 共用项目知识、保留可追溯出处，并提供独立阅读与管理界面。

## 当前架构

- **MyGo Native**：桌面入口为 `cmd/memlane-app/main.go`，默认与最小窗口为 `1200×820`（允许放大），围绕总览、通用记忆、用户画像、项目记忆／原始记录、搜索与设置展开。
- **官方 Cognee（零侵入外置扩展）**：从固定上游源码载入官方 API（版本由 `engine/coding-memory-components.json` 校验），通过外置扩展网关 `engine/python/cognee_extension_gateway.py` 与外置日期归因存储 `engine/python/provenance_store.py` 对齐来源日期与图谱开关，统一连接同一套日常存储 `.local/daily/official-store`。
- **多 Agent 接入与记忆处理**：支持多类本机编码 Agent 的受管插件连接、有效模型继承、Bridge 端点（Codex / pi / TraeX）与直连 LLM Provider 切换，以及统一 CLI 提示词用户画像导出（`engine/python/user_profiles/`）。
- **开发启动器（`scripts/dev.py`）**：获取日常数据单实例锁后启动 completion、官方 Cognee、`project_control_server` 与 MyGo dev；退出时校验进程身份并清理所属子进程。

## 本地开发

从仓库根目录用 `runtime.json` 的 `python` 字段指定的受管 Python 3.12 运行日常服务命令。下方示例使用日常配置对应的 `.local/cognee-python/bin/python`。省略 `--config` 时默认读取 `.local/daily/runtime.json`。

启动预检由 `scripts/dev.py` 自己执行，先于服务子进程启动。裸 `python3` 可能选中 Python 3.9，导致读取 Codex 配置时缺少 `tomllib`。直接用受管 Python 启动，预检与服务子进程就使用同一个环境。首次准备受管环境见[开发说明](docs/architecture/development.md)。

启动日常服务和桌面窗口。

```sh
.local/cognee-python/bin/python scripts/dev.py start --config /绝对路径/runtime.json
```

仅启动后台服务（不拉起 UI 窗口）：

```sh
.local/cognee-python/bin/python scripts/dev.py start --no-ui --config /绝对路径/runtime.json
```

常用运维与检查命令：

```sh
.local/cognee-python/bin/python scripts/dev.py status --config /绝对路径/runtime.json
.local/cognee-python/bin/python scripts/dev.py open --config /绝对路径/runtime.json
.local/cognee-python/bin/python scripts/dev.py stop --config /绝对路径/runtime.json
.local/cognee-python/bin/python scripts/dev.py recover --config /绝对路径/runtime.json
python3 scripts/task.py -- python3 scripts/lint.py
python3 scripts/task.py -- python3 -m unittest discover -s scripts -p test_lint.py
sh scripts/go.sh test ./...
```

| 动作 | 行为 |
| --- | --- |
| `start` | 独占日常官方存储，启动后台服务与默认 UI；已有实例运行时不重复启动。 |
| `status` | 校验数据锁、启动器 PID、进程启动时间与配置路径并输出状态摘要。 |
| `open` | 重开 MyGo dev watcher（不重启后台服务）。 |
| `stop` | 校验实例身份，停止所属进程组及子进程并释放日常数据锁。 |
| `recover` | 异常退出确认进程组与端口释放后安全归档遗留锁记录。 |

## 私有配置与安全边界

- 实际运行配置、token、服务环境变量与本机 Agent 配置保存于 Git 跟踪范围之外的私有位置（`.local/`，`0600` 权限）；不要将密钥、真实记忆库、聊天记录或本机 Agent 配置提交到仓库。
- 当前处于 `dev` 阶段，**不构建完整安装包**（不做 DMG、签名、公证或 helper 安装）；受管开发任务经由 `scripts/task.py` 执行并保留至少 `50 GiB` 可用磁盘空间（运行中低于 `55 GiB` 自动停止）。详细配置说明见[开发说明](docs/architecture/development.md)。

## 主要参考文档

- [文档总目录](docs/README.md)
- [系统架构总览](docs/architecture/overview.md)
- [产品定义](docs/product/product.md)
- [设计决策](docs/design/design.md)
- [开发说明](docs/architecture/development.md)
- [工程规范与失败停止规则](docs/engineering/workflow.md)
- [架构改造总览](docs/architecture/architecture-refactor-20261120.md)

![V4 首页设计参考图](docs/images/visual-v4-20261120/overview-light.png)
