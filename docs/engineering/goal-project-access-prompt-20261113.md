# 项目权限管理 Goal 执行 Prompt

目标 session：`01a1d30a-d776-7f92-a7e9-9424e5fabc13`。工作目录：`/Users/bytedance/work/AI/memlane`。

用户已明确授权你使用 Goal 模式，按下列文档实施项目权限管理这一整组功能。本消息是实施启动指令，覆盖这些文档此前“仅文档／未启动”的阶段状态；不是恢复历史全量 Goal，不进入 release。直接开始并持续推进到本范围自验完成，不停留在计划或仅写代码。

最新总览交接：总览session已提交 `4db3b8b` 并明确释放 `app.go`／`client.go`／`scripts/dev.py` 源码所有权；UI与项目之间仍按本prompt顺序交接。保留 `overview*.go`、新增配置字段及失败诊断逻辑。实际记录见 `docs/engineering/overview-execution-20261113.md`。OVR01是后台stderr失效已定位未修，后台未重启；现有UI watcher只是附着，不证明后台业务恢复。文中旧PID／工具session仅作线索，接手时重新核对。

## 1. 启动 Goal

先向用户用一行说明当前动作，读取 AGENTS.md 与工作树。调用 `get_goal` 核对当前线程：无未完成 Goal 时，调用 `create_goal`，objective为“实现并自验Memlane项目自动同步、新建／关联、Agent项目权限、默认策略与覆盖、自动采集／处理控制及通用知识管理，按PA规格真实闭环交付，dev模式，不做release”。不要设置token_budget，用户未指定预算。

已有本范围同一 Goal 则继续；不得恢复旧全量／其他范围 Goal。若线程已有不兼容的未完成 Goal，保留其状态并报告具体阻塞，不绕过工具创建重复目标。不要在任务未完成时标为complete；Goal状态更新遵循系统规则。

## 2. 需求及依据

按顺序完整阅读：

1. `docs/project-access-product-plan-20261113.md`
2. `docs/project-access-spec-20261113.md`
3. `docs/project-access-technical-plan-20261113.md`
4. `docs/research-project-knowledge-official-capabilities-20261113.md`（重点第8节）
5. `docs/project-knowledge-feasibility-20261113.md`（两部分入口）
6. `docs/product.md`、`docs/design.md`、`docs/development.md`，以及当前交付、错误索引及同版JSON。

上述三份新文档是本次实施范围，旧文档中“移除权限／同一owner／仅手动项目关联”等冲突描述是旧范围，不能据此拒绝新需求。用户此轮按文档启动，采用新文档中明确列出的产品与技术提案，包括维护权限含官方delete、控制进程、固定插件小补丁等；不要额外要求逐条确认。出现证据证明方案不可行时，先核对事实并修订本范围方案，必要的产品范围变化再清楚提出，不能静默缩减。

必须使用本仓库 `plan-change-impact`、`verify-continuous-scenarios` Skill。先记录连续主流程、已知失败边界和实施顺序到 `docs/engineering/project-access-execution-20261113.md`，再集中开发。已有官方源码事实无需重做泛化可行性调研；围绕实际契约实现与验证。

## 3. 不能漏掉的行为

- 新项目自动继承用户预设默认策略，无需先添加或逐项目授权；具体项目字段覆盖优先，自动发现不重置限制。
- 项目独立官方dataset，首次绑定前路由；默认授权由管理侧完成，Agent不能通过自建dataset成为owner绕过项目权限。
- 新建／关联与自动发现共用身份和幂等登记；同名不同路径不误合并；代码图保持显式开启。
- 官方Agent独立主体、有效ACL核对，禁用共享广泛授权和owner／CLI fallback；不能只隐藏界面。
- 可写并维护 = read/write/delete，不授share，明确删除含义；不能承诺原生coding整理“只写不删”。
- 自动capture在正文读取前门禁；403后既有buffer blocked，不反复重试；主动保存明确拒绝。恢复不补采暂停期，按技术方案推进无正文游标；旧合法待发材料仅明确选择补交／丢弃。
- 自动处理所有触发点受控，已有任务与部分写入真实报告；不伪造取消结果。
- 通用知识独立官方存储、管理CRUD、分类和来源；在现有单一coding注入入口合并项目与可读通用候选，空项目也可用，不新增第二次注入器。
- 原memlane与正在分析item-fe的会话／数据不静默迁移、补采、重放或删除。

## 4. 并行所有权与顺序

用户同时启动UI阅读优化 session `01a1d30a-f0f6-7bc2-a0a9-14e3c819845f`；总览 session为 `01a1d2d2-da26-7561-8cde-ff417eb20b5f`，还可能在收口。方案发起者为 `01a1d2cc-3ede-7e52-985d-ee29705cc8b7`。

开始即向这两个实施session发简短所有权消息，核对当前文件和运行环境，取得共享文件交接回执；不要让所有人同时修改同一文件。安排如下：

- 你先拥有独立 `project_control.py`、`project_policy.py`、`project_access*.go`、插件受管patch及其独立测试／本范围工程文档。
- UI session先从总览取得 `app.go`／`client.go` 的共享接线窗口，完成阅读改动和必要Native自验后显式释放；你收到UI释放回执后再接项目设置、配置／鉴权等共享修改。等待时继续独立模块，不覆盖其修改。
- `scripts/dev.py` 及组件输入修改先向总览确认其已释放，再由你操作；不把总览未提交改动当作自己的基线直接覆盖。
- 本 session不重做知识标题、正文包装、技术详情、出处／复制的UI方案；必要公共类型与UI协调，由一个明确所有者落地。
- Native窗口、MyGo watcher、官方服务／插件身份切换排他操作。需要启停或变更凭据前先核对当前owner、依赖和使用中的session，向总览与UI取得运行交接；等待时继续源码和隔离协议检查，不擅自停共享服务。

已知交接线索：总览session只读发现官方服务PID69478启动图worker时出现multiprocessing刷新继承stderr的BrokenPipeError，尚未由本session核实。不要将此当本需求失败，不重放模型／索引或直接重启；先读取总览最新账本并取得问题／运行所有权交接，沿具体缺陷记录处理。

## 5. 数据、Git与执行保护

当前多session共享工作树，包含未提交总览源码及本轮文档。先核对 `git status`／diff和各自所有权，保留全部无关修改。不得reset、clean、全量stash或`git add .`。需要提交或源码checkpoint时仅纳入明确属于本任务且已交接的路径，不把他人源码、凭据、聊天或数据库提交。若工程规则要求快照提交与tag，应先取得涉及文件的所有权再按当前规则执行，已有旧tag不等于新改动已保护。

使用 `python3 scripts/dev.py`、`sh scripts/go.sh`；其他检查沿 `python3 scripts/task.py -- ...`。至少100GiB，运行中遵守105GiB门禁；不得清理未知依赖、build工作树或日常官方库。保留原包资源。只开发调试，不构建完整App／DMG，不安装helper、不做签名发布。

## 6. 完成定义

完整实现PA01–PA14，按PA-A至PA-H连续验收。先完成源代码／协议检查，再验证真实独立Agent主体允许／拒绝、真实新仓库自动采集与正确归属、默认继承与覆盖、禁写／恢复、通用知识真实保存和另一会话采用；Native设置及阅读连续操作必须实际打开窗口留证。夹具／HTTP200／接受请求不替代真实内容保存和使用。

真实任务使用无敏感材料和明确归属，记录官方operation／dataset／session身份及实际终态，未知不重试；禁止为了补材料重复运行模型或索引。同一具体问题累计第4次验收仍失败立即停止该问题、保存证据并汇报，不换路径重置次数；不依赖该问题的工作继续。

完成后更新本范围工程账本、相关规格／开发说明中因本实现已改变的规则（修改共享文档前协调所有权），记录文件、命令、真实证据、数据／进程去向及未证明边界。向UI与总览回报共享资源释放和依赖变更；真正达到完成定义才将本Goal complete，并给用户可直接操作的验收入口。不要把开发实现称release交付。
