# Memlane 文档总目录

按职责分类收纳当前有效文档、专项调研、工程规范与历史归档。术语与分层定义见仓库根目录 [CONTEXT.md](../CONTEXT.md)。

## 1. 产品定义与路线 (`product/`)

| 文档 | 说明 |
| --- | --- |
| [product.md](product/product.md) | Memlane 产品定位、内容分层与唯一真相、核心页面交互与依赖复用原则（已合并原 `content-model.md`） |
| [roadmap.md](product/roadmap.md) | 后续演进方向与待办记录（含 2026-11-25 首次启动向导专注式重设计与 `v1.0` 正式版增量） |
| [beta-install-guide.md](product/beta-install-guide.md) | `v0.1.0-beta.3` 内测安装、Gatekeeper 放行、零 Skill Setup CLI、首次下载源与安全卸载指南 |
| [THIRD_PARTY_LICENSES.md](../THIRD_PARTY_LICENSES.md) | 第三方开源组件、本地运行依赖与向量模型许可说明 |

## 2. 系统架构与技术契约 (`architecture/`)

| 文档 | 说明 |
| --- | --- |
| [overview.md](architecture/overview.md) | 当前系统分层、源码模块映射、核心数据流与架构全景图 |
| [boundaries.md](architecture/boundaries.md) | 目标架构边界契约（Go 单二进制收口、Python ≤3.5k 引擎薄壳、方案 C `memlane hook` 与冲突策略、6 原语可替换引擎接口、`kit/*` 跨 App 复用预留与 5 万行预算红线） |
| [development.md](architecture/development.md) | 日常开发入口 `scripts/dev.py`、统一门禁、运行配置与凭据隔离、多 Agent/Provider 调试及国内镜像源手工终端兜底 |
| [agent-connection-contract.md](architecture/agent-connection-contract.md) | 电脑级 5 宿主发现、官方插件优先接入与零项目授权契约 |
| [agent-effective-model-contract-20261118.md](architecture/agent-effective-model-contract-20261118.md) | 多 Agent 有效模型读取、Bridge 与 Provider 互斥激活及调用契约 |
| [queue-isolation-20261119.md](architecture/queue-isolation-20261119.md) | QI01–QI06 异常隔离规则、per-entry buffer 兼容契约与回归边界 |

## 3. 界面、视觉与动效设计 (`design/`)

| 文档 | 说明 |
| --- | --- |
| [visual-standards-v4-20261120.md](design/visual-standards-v4-20261120.md) | 现行 V4 视觉与核心交互规范（浅/深双外观令牌、字体层级、三栏布局、动效参数、§7.7 首次启动专注式向导及交互不变量；已合并原 `design.md`） |
| [motion-implementation.md](design/motion-implementation.md) | MyGo 原生 UI 动效实施与复用契约（减少动态效果、帧率与内存守护） |
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
| [workflow.md](engineering/workflow.md) | 工程执行节奏、任务分级与垂直切片、双审 2+1 收敛、Go/MyGo 检查、Level 0/1/2a/2b 发版门禁、模型参数留证契约与“四次失败停止”规则 |
| [architecture-governance-and-py-freeze.md](engineering/architecture-governance-and-py-freeze.md) | 防过度防御硬门禁（`diff_guard.py`）、非 Cognee Python 新增冻结（`DG005`）、旧回退清理清单及定时 Loop 架构/僵尸规则巡检规划 |
| [architecture-boundary-and-loc-facts-20261125.md](engineering/architecture-boundary-and-loc-facts-20261125.md) | `b1a82e0` 基线全仓代码行数、19 日增长曲线、目录扁平度、Python 外部依赖分级与现行架构边界客观事实记录 |
| [agent-workflow-efficiency-retrospective-20261125.md](engineering/agent-workflow-efficiency-retrospective-20261125.md) | `v0.1.0-beta.3` 多 Agent 研发、双审与 UTM 验收耗时复盘及提速改进方案 |
| [beta-install-issues.md](engineering/beta-install-issues.md) | Beta 安装问题、根因、修复与 UTM 复验边界 |
| [defect-postmortems.md](engineering/defect-postmortems.md) | 近期关键缺陷复盘汇总（登录启动窗、画像文档误入项目队列、Codex 重试通知） |
| [runtime-package-cleanup-incident-20261111.md](engineering/runtime-package-cleanup-incident-20261111.md) | 运行中后台所属运行包被误清理复盘与依赖保护规则 |
| [task-brief-template.md](engineering/templates/task-brief-template.md) | 跨模块任务影响范围与验证说明填写模板 |
| [testcase-template.md](engineering/templates/testcase-template.md) | 连续主流程集成/验收用例模板 |
| [review-ledger-template.md](engineering/templates/review-ledger-template.md) | 双独立代码审核 Prompt、已确认契约例外账本与 2+1 收敛裁决模板 |
| [incident-template.md](engineering/templates/incident-template.md) | 线上/验收问题复盘记录模板 |

## 6. 图表与参考截图 (`images/`)

- [memlane-architecture-20261120.svg](images/memlane-architecture-20261120.svg)：系统架构与五条动态数据流全景图（SVG 流动箭头版）
- [visual-v4-20261120/](images/visual-v4-20261120/)：V4 原型浅色/深色关键界面参考截图

## 7. 历史归档 (`archive/`)

已被 V4、合并后的当前基准或新工程流程取代的历史设计稿、旧规范、阶段改造记录及退役执行说明，仅供追溯，不作为新改动依据：

- [memlane-design-spec-v3.html](archive/memlane-design-spec-v3.html)
- [visual-standards-20261113.md](archive/visual-standards-20261113.md)
- [scenario-runner.md](archive/scenario-runner.md)
- [client-simplification-20261112/](archive/client-simplification-20261112/)
- [deployment-options-20261117.md](archive/deployment-options-20261117.md)（核心边界已并入 `research/docker-deployment-architecture-20261125.md`）
- [release-v0.1.0-beta.1-20261121.md](archive/release-v0.1.0-beta.1-20261121.md)（国内镜像源实测与终端兜底已并入 `architecture/development.md`，`v1.0` 增量已并入 `product/roadmap.md`）
- [architecture-refactor-20261120.md](archive/architecture-refactor-20261120.md)（四阶段架构改造阶段记录）
- [environment-readiness-reliability-20261117.html](archive/environment-readiness-reliability-20261117.html)（早期环境准备调研报告）
- [visual-v4-implementation-20261120.md](archive/visual-v4-implementation-20261120.md)（视觉 V4 实施账本）

## 8. 过程文档与验收媒体 (`process/`)

「过程文档」指仅服务于当次需求探索、调试排查、评审或验收留证、不构成项目长期基线的临时文档、草稿、界面截图及录屏视频等（任务完成后删掉不影响理解当前系统设计与契约）。所有过程产物统一存放在 [process/](process/README.md) 目录（完整正反边界见该说明）；仓库根目录 `.gitignore` 已配置忽略该目录下除 `README.md` 外的全部文件，不提交入库。
