# Memlane

面向程序员的 macOS 记忆客户端，让编码 Agent 共用项目知识，减少重复交代背景。

当前精简版采用 **MyGo Native + 固定版本官方 Cognee 唯一内容源 + Python Codex completion 端点**，由 `scripts/dev.py` 统一启动。正在开发中，尚未完成全部真实流程验收；最新证据见[精简版执行记录](docs/engineering/client-simplification-execution-20261113.md)。

## 当前架构

- **MyGo Native**：桌面入口为 `cmd/memlane-app/main.go`，使用 `internal/simpledesktop` 直连官方服务，默认及最小窗口为 1200×820。首版围绕知识、原始记录、搜索、出处与官方图谱展开。
- **官方 Cognee**：从固定上游原始源码载入官方 API，版本依据 `engine/coding-memory-components.json` 校验。知识、原始记录、检索和图谱共用同一份官方日常数据；开发启动不会另建空库、独立全文库或第二套索引。
- **Python Codex completion 端点**：由 `engine/python/codex_completion_endpoint.py` 提供，供官方 Cognee 调用 Codex 订阅推理；embedding 复用已有组件。当前生成通道固定为 Codex。
- **开发启动器**：先取得现有官方数据根的单实例锁，再启动 completion、官方服务和 MyGo dev。拒绝占用端口，退出时核对并收尾自身所属进程。Agent 采集与召回复用官方插件，并指向同一服务、官方身份与项目 dataset。

## 本地开发

在仓库根目录使用已有的私有运行配置：

```sh
python3 scripts/dev.py start --config /绝对路径/runtime.json
```

`start` 保持前台运行，服务就绪后默认打开 MyGo dev。省略动作等同 `start`；省略 `--config` 时使用仓库内 `.local/daily/runtime.json`。配置必须已经存在。仅启动后台可使用：

```sh
python3 scripts/dev.py start --config /绝对路径/runtime.json --no-ui
```

在另一个终端使用同一配置管理该实例：

```sh
python3 scripts/dev.py status --config /绝对路径/runtime.json
python3 scripts/dev.py open --config /绝对路径/runtime.json
python3 scripts/dev.py stop --config /绝对路径/runtime.json
```

| 动作 | 行为 |
| --- | --- |
| `start` | 独占现有官方日常数据，启动后台与默认 UI；已有实例运行时不重复启动。 |
| `status` | 校验数据锁、启动器 PID、进程启动时间与配置路径后输出所有权记录；不执行完整业务健康验收。 |
| `open` | 请求已运行的启动器重新启动 MyGo dev watcher；已有 watcher 会先停止，官方服务和 completion 保持运行。 |
| `stop` | 校验实例身份，明确停止所属进程组与已跟踪子进程；确认收尾后释放日常数据锁。 |

关闭桌面窗口或 MyGo 源码重载不会停止官方服务和 completion。启动器终端中的 **Ctrl+C 会明确停止所属服务**，因此需要保持该启动器运行。`open` 依赖已有启动器，不会启动后台。

电脑重启后手动执行 `start`。官方服务或 completion 异常退出时不自动重启；服务或模型配置变更后，先用原配置明确 `stop` 并确认退出，再修改配置并 `start`。UI 源码由 MyGo dev 增量重载。不注册 helper，不自动恢复业务任务；若提示遗留所有权记录或停止未确认，应先核对原实例，不直接删锁或另起一套数据。

## 私有配置与数据

运行配置明确指定已有 `official_data`、固定源码 `official_source`、归档 `official_archive`、`service_commit`、已有 Python 环境 `python`、本机回环地址 `service_url` / `completion_url`、凭据文件 `token_file` / `completion_token_file`、`service_environment_file`、`codex_executable`、`auth_home`、`model` 及项目关联 `projects`。

实际配置、凭据和账号资料保存在 Git 跟踪范围之外的私有文件中；上述两个 token 文件和服务环境文件须为 `0600`。不将真实路径、密钥、记忆库、聊天记录或本机 Agent 配置提交到仓库。官方连接认证继续保留，项目与 dataset 用于内容归属及检索分组。

开发和日常阅读使用同一官方存储，不能用临时空库替代真实接管；数据即使位于临时目录也须保护。历史运行资源只有确认无消费者依赖后才能清理，不能因新 UI 可打开就删除。

开发阶段仅做源码检查、必要测试、真实链路和 Native 界面验收；**不构建完整运行包**，不进行 DMG、签名、公证、更新或 helper 安装。启动器自动经过 `scripts/task.py` 磁盘门禁，开发至少保留 100 GiB；Go 检查沿用 `sh scripts/go.sh` 和仓库内工具链。详细要求见[开发说明](docs/development.md)。

## 实施与验收状态

截至 2026-11-13，[执行记录](docs/engineering/client-simplification-execution-20261113.md)已记录：新启动器在同一官方日常数据上完成启动、明确停止及同配置重启；服务和 completion 就绪；MyGo dev 打开 Native 窗口并连接服务；官方 memlane dataset 已创建，官方插件已安装启用。completion 的合成协议及 HTTP 检查通过，不代表真实模型生成验收通过。

仍待收口：新会话 hook 信任、正常编码产生的真实采集、官方知识生成与保存、Native 阅读／搜索／出处的连续流程、新会话真实召回并实际采用，以及真实内容图谱和手动代码索引。当前空组 0 条、图组件 0 节点及连接成功均不能代替这些证据；Native 实际视觉验收与 D01–D09 全部完成也尚未确认。

历史实施和验收记录保留原有事实，不作为本版已交付能力清单。当前范围与设计依据：

- [精简版规格](docs/client-simplification-spec-20261112.md)
- [精简版技术方案](docs/client-simplification-technical-plan-20261112.md)
- [当前设计目录](docs/ui-redesign/design-catalog.md)
- [工程规范与失败停止规则](docs/engineering/workflow.md)

![当前首页设计稿（示例内容，非真实运行验收）](docs/images/ui-redesign/client-simplification-20261112/01-home.png)
