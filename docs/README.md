# Memlane 文档总目录

按职责分类收纳当前有效文档、专项调研、工程规范与过程产物说明。本仓库**不设 `docs/archive/`**，已淘汰或已完工的历史文档按[文档生命周期与清理标准](engineering/workflow.md#文档生命周期与清理标准2026-11-26-起执行)直接删除并依靠 Git 历史追溯；术语与分层定义见仓库根目录 [CONTEXT.md](../CONTEXT.md)。

## 1. 产品定义与路线 (`product/`)

| 文档 | 说明 |
| --- | --- |
| [product.md](product/product.md) | Memlane 产品定位、内容分层与唯一真相、核心页面交互与依赖复用原则（已合并原 `content-model.md`） |
| [roadmap.md](product/roadmap.md) | 后续演进方向与待办记录（含首次启动向导专注式重设计与 `v1.0` 正式版增量） |
| [beta-install-guide.md](product/beta-install-guide.md) | `v0.1.0-beta.3` 内测安装、Gatekeeper 放行、零 Skill Setup CLI、首次下载源与安全卸载指南 |
| [THIRD_PARTY_LICENSES.md](../THIRD_PARTY_LICENSES.md) | 第三方开源组件、本地运行依赖与向量模型许可说明 |

## 2. 系统架构与技术契约 (`architecture/`)

| 文档 | 说明 |
| --- | --- |
| [overview.md](architecture/overview.md) | 现行系统分层、Go 单二进制 + `kit/*`/`core/*`/`app/*`/`engines/cognee` 源码映射、五条核心数据流与架构全景图 |
| [boundaries.md](architecture/boundaries.md) | 现行生效架构边界契约（Go 单二进制收口、Python ≤3.5k 无状态引擎薄壳、方案 C `memlane hook` 与三级冲突策略、6 原语 `MemoryEngine`、`kit/*` 跨 App 复用边界与机械门禁） |
| [mcp.md](architecture/mcp.md) | MCP 在方案 C 架构中的职责定位、`memlane mcp` stdio 桥接与网关 `/mcp` 两段式架构、4 项工具契约（`remember` 快路径/收件箱回退、`remember_status`、`recall` 故障隔离配额、`health`）与安全边界 |
| [development.md](architecture/development.md) | 日常开发入口 `cmd/memlane dev`、统一门禁（`check.py` / `lint.py` / `diff_guard.py`）、运行配置与凭据隔离、多 Agent/Provider 调试及国内镜像源手工终端兜底 |
| [agent-effective-model-contract-20261118.md](architecture/agent-effective-model-contract-20261118.md) | 多 Agent 有效模型读取、Bridge 与 Provider 互斥激活及调用契约 |
| [memory-flow-3d/](architecture/memory-flow-3d/) | Memlane × Cognee 3D / 2D 记忆流转交互动画图解（离线 WebGL 3D + 零依赖 2D Canvas 双版本一键互切，均支持浅色/深空双风格，含切段/分词、GPT 代理提炼、三库与零大模型召回演示） |

## 3. 界面、视觉与动效设计 (`design/`)

| 文档 | 说明 |
| --- | --- |
| [visual-standards-v4-20261120.md](design/visual-standards-v4-20261120.md) | 现行 V4 视觉与核心交互规范（浅/深双外观令牌、字体层级、三栏布局、动效参数、§7.7 首次启动专注式向导及交互不变量；已合并原 `design.md`） |
| [motion-implementation.md](design/motion-implementation.md) | MyGo 原生 UI 动效实施与复用契约（减少动态效果、帧率与内存守护） |
| [technical-explainer-animation.md](design/technical-explainer-animation.md) | 技术原理动画制作经验与 2D / 3D 工程指南（叙事原则、60fps 连续时间轴、3D 浅色黏土/深空发光双配方与 2D Canvas 替代方案） |
| [visual-v4-prototype/](design/visual-v4-prototype/) | V4 可交互 HTML 原型与自查截图脚本 |
| [onboarding-prototype/](design/onboarding-prototype/) | 首次启动向导（2026-11-25 专注式重设计）可交互 HTML 原型与截图脚本 |

## 4. 专题技术调研 (`research/`)

| 文档 | 说明 |
| --- | --- |
| [agent-driving-protocols-20261118.md](research/agent-driving-protocols-20261118.md) | 多宿主 Coding Agent 驱动协议与接入参考 |
| [hindsight-vs-memlane-20261117.md](research/hindsight-vs-memlane-20261117.md) | Hindsight 能力边界与替换可行性审计 |
| [macos-updater-20261119.md](research/macos-updater-20261119.md) | macOS 应用更新机制调研与方案建议 |
| [trae-ide-memory-processing-20261119.md](research/trae-ide-memory-processing-20261119.md) | TRAE IDE 场景下记忆整理模型执行入口调研 |
| [native-first-screen-performance-20261122.md](research/native-first-screen-performance-20261122.md) | Native 客户端首屏 20× 性能基线拆解、启动并发风暴归因与优化路线研究 |
| [install-dependency-slimming-20261125.md](research/install-dependency-slimming-20261125.md) | 首次安装 7 项依赖体积实测、171 包导入链路与零侵入分级瘦身研究 |
| [docker-deployment-architecture-20261125.md](research/docker-deployment-architecture-20261125.md) | 本机/远程 Docker 部署边界选型、安全穿透、数据卷锁语义与分阶段落地路线图 |

## 5. 工程规范、复盘与模板 (`engineering/`)

| 文档 | 说明 |
| --- | --- |
| [workflow.md](engineering/workflow.md) | 工程执行节奏、任务分级与垂直切片、双审 2+1 收敛、Go/MyGo 检查、Level 0/1/2a/2b 发版门禁、模型参数留证契约、“四次失败停止”规则与文档生命周期清理标准 |
| [architecture-governance-and-py-freeze.md](engineering/architecture-governance-and-py-freeze.md) | 架构守护硬门禁（`diff_guard.py` `DG001–DG006`、`arch_guard_test.go`、`ML023` 无状态 `engines/cognee` 护栏）、Wave 0–4 收口状态及定时 Loop 巡检规划 |
| [agent-workflow-efficiency-retrospective-20261125.md](engineering/agent-workflow-efficiency-retrospective-20261125.md) | `v0.1.0-beta.3` 多 Agent 研发、双审与 UTM 验收耗时复盘及提速改进方案 |
| [beta-install-issues.md](engineering/beta-install-issues.md) | Beta 安装问题、根因、修复与 UTM 复验边界 |
| [defect-postmortems.md](engineering/defect-postmortems.md) | 近期关键缺陷复盘汇总（登录启动窗、画像文档误入项目队列、Codex 重试通知） |
| [runtime-package-cleanup-incident-20261111.md](engineering/runtime-package-cleanup-incident-20261111.md) | 运行中后台所属运行包被误清理复盘与依赖保护规则 |
| [task-brief-template.md](engineering/templates/task-brief-template.md) | 跨模块任务影响范围与验证说明填写模板 |
| [testcase-template.md](engineering/templates/testcase-template.md) | 连续主流程集成/验收用例模板 |
| [review-ledger-template.md](engineering/templates/review-ledger-template.md) | 双独立代码审核 Prompt、已确认契约例外账本与 2+1 收敛裁决模板 |
| [incident-template.md](engineering/templates/incident-template.md) | 线上/验收问题复盘记录模板 |

## 6. 图表与参考截图 (`images/`)

- [memlane-architecture-20261126.svg](images/memlane-architecture-20261126.svg)：系统架构与五条动态数据流全景图（Wave 0–4 收口版）
- [visual-v4-20261120/](images/visual-v4-20261120/)：V4 原型浅色/深色关键界面参考截图
- [memlane-gatekeeper-open-anyway-20261123.jpg](images/memlane-gatekeeper-open-anyway-20261123.jpg)：macOS Gatekeeper 首次放行操作示意

## 7. 过程文档与验收媒体 (`process/`)

「过程文档」指仅服务于当次需求探索、调试排查、评审或验收留证、不构成项目长期基线的临时文档、草稿、界面截图及录屏视频等（任务完成后删掉不影响理解当前系统设计与契约）。所有过程产物统一存放在 [process/](process/README.md) 目录（完整正反边界见该说明）；仓库根目录 `.gitignore` 已配置忽略该目录下除 `README.md` 外的全部文件，不提交入库。
