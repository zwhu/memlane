# Memlane 文档总目录

按职责分类收纳当前有效文档、专项调研、工程规范与历史归档。术语与分层定义见仓库根目录 [CONTEXT.md](../CONTEXT.md)。

## 1. 产品定义与路线 (`product/`)

| 文档 | 说明 |
| --- | --- |
| [product.md](product/product.md) | Memlane 产品定位、用户场景、核心页面与功能约束（含依赖复用与排查原则） |
| [content-model.md](product/content-model.md) | 通用记忆、项目记忆、原始记录与用户画像的内容分层与读写归属 |
| [roadmap.md](product/roadmap.md) | 后续演进方向与待办记录 |
| [deployment-options-20261117.md](product/deployment-options-20261117.md) | 单人记忆库在本地、Docker 与远程部署的演进方向与兼容评估 |
| [release-v0.1.0-beta.1-20261121.md](product/release-v0.1.0-beta.1-20261121.md) | v0.1.0-beta.1 小范围公测发版边界、国内镜像源实测调研与 M1–M4 实施清单 |
| [beta-install-guide.md](product/beta-install-guide.md) | v0.1.0-beta.1 公测安装、SHA256 校验、Gatekeeper 放行、首次下载源与安全卸载指南 |
| [THIRD_PARTY_LICENSES.md](../THIRD_PARTY_LICENSES.md) | 第三方开源组件、本地运行依赖与向量模型许可说明 |

## 2. 系统架构与技术契约 (`architecture/`)

| 文档 | 说明 |
| --- | --- |
| [overview.md](architecture/overview.md) | 当前系统分层、源码模块映射、核心数据流与架构全景图 |
| [development.md](architecture/development.md) | 日常开发入口 `scripts/dev.py`、运行配置字段、凭据隔离与多 Agent/Provider 调试说明 |
| [architecture-refactor-20261120.md](architecture/architecture-refactor-20261120.md) | 零侵入外置网关、三层服务监控、模块收口与 Go 原生 Supervisor 四阶段改造记录 |
| [agent-connection-contract.md](architecture/agent-connection-contract.md) | 电脑级宿主发现、官方插件优先接入与项目隔离规则 |
| [agent-effective-model-contract-20261118.md](architecture/agent-effective-model-contract-20261118.md) | 多 Agent 有效模型读取、Bridge 与 Provider 互斥激活及调用契约 |
| [queue-isolation-20261119.md](architecture/queue-isolation-20261119.md) | 单条拒收、回执未知隔离与保存队列阻塞排查契约 |
| [environment-readiness-reliability-20261117.html](architecture/environment-readiness-reliability-20261117.html) | 环境依赖检测、先复用后安装确认及 Agent 辅助排查技术报告 |

## 3. 界面、视觉与动效设计 (`design/`)

| 文档 | 说明 |
| --- | --- |
| [design.md](design/design.md) | 桌面端交互约束、页面结构与历次界面修订裁决 |
| [visual-standards-v4-20261120.md](design/visual-standards-v4-20261120.md) | 现行 V4 视觉规范（浅/深双外观令牌、字体层级、三栏布局与动效参数） |
| [motion-implementation.md](design/motion-implementation.md) | MyGo 原生 UI 动效实施与复用契约（减少动态效果、帧率与内存守护） |
| [visual-v4-implementation-20261120.md](design/visual-v4-implementation-20261120.md) | 视觉 V4 在 Native 客户端的分阶段实施账本与证据 |
| [visual-v4-prototype/](design/visual-v4-prototype/) | V4 可交互 HTML 原型与自查截图脚本 |

## 4. 专题技术调研 (`research/`)

| 文档 | 说明 |
| --- | --- |
| [agent-driving-protocols-20261118.md](research/agent-driving-protocols-20261118.md) | 多宿主 Coding Agent 驱动协议与接入参考 |
| [hindsight-vs-memlane-20261117.md](research/hindsight-vs-memlane-20261117.md) | Hindsight 能力边界与替换可行性审计 |
| [macos-updater-20261119.md](research/macos-updater-20261119.md) | macOS 应用更新机制调研与方案建议 |
| [trae-ide-memory-processing-20261119.md](research/trae-ide-memory-processing-20261119.md) | TRAE IDE 场景下记忆整理模型执行入口调研 |
| [native-first-screen-performance-20261122.md](research/native-first-screen-performance-20261122.md) | Native 客户端首屏 20× 性能基线拆解、启动并发风暴归因与优化路线研究 |

## 5. 工程规范、复盘与模板 (`engineering/`)

| 文档 | 说明 |
| --- | --- |
| [workflow.md](engineering/workflow.md) | 工程执行节奏、Go/MyGo 检查要求、模型参数留证契约与“四次失败停止”规则 |
| [beta-install-issues.md](engineering/beta-install-issues.md) | Beta 安装问题、根因、修复与 UTM 复验边界 |
| [runtime-package-cleanup-incident-20261111.md](engineering/runtime-package-cleanup-incident-20261111.md) | 运行中后台所属运行包被误清理复盘与依赖保护规则 |
| [task-brief-template.md](engineering/templates/task-brief-template.md) | 跨模块任务影响范围与验证说明填写模板 |
| [testcase-template.md](engineering/templates/testcase-template.md) | 连续主流程集成/验收用例模板 |
| [incident-template.md](engineering/templates/incident-template.md) | 线上/验收问题复盘记录模板 |

## 6. 图表与参考截图 (`images/`)

- [memlane-architecture-20261120.svg](images/memlane-architecture-20261120.svg)：系统架构与五条动态数据流全景图（SVG 流动箭头版）
- [visual-v4-20261120/](images/visual-v4-20261120/)：V4 原型浅色/深色关键界面参考截图

## 7. 历史归档 (`archive/`)

已被 V4 或新工程流程取代的历史设计稿、旧规范及退役执行说明，仅供追溯，不作为新改动依据：

- [memlane-design-spec-v3.html](archive/memlane-design-spec-v3.html)
- [visual-standards-20261113.md](archive/visual-standards-20261113.md)
- [scenario-runner.md](archive/scenario-runner.md)
- [client-simplification-20261112/](archive/client-simplification-20261112/)

## 8. 过程文档与验收媒体 (`process/`)

「过程文档」指仅服务于当次需求探索、调试排查、评审或验收留证、不构成项目长期基线的临时文档、草稿、界面截图及录屏视频等（任务完成后删掉不影响理解当前系统设计与契约）。所有过程产物统一存放在 [process/](process/README.md) 目录（完整正反边界见该说明）；仓库根目录 `.gitignore` 已配置忽略该目录下除 `README.md` 外的全部文件，不提交入库。
