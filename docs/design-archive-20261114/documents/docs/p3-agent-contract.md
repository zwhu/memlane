# P3-A 日常 Agent 接入契约

2026-11-08。A1a traex本机实现与验收已通过，见 [验证](../../../p3-agent-validation.md)；A1b Codex新工具/A2 IDE仍待验收。复用 [日常工具契约](agent-contract.md)、[使用契约](p3-usage-contract.md) 和 [初始化契约](p3-onboarding-contract.md)，不把 Skill/IDE/CLI/后台执行器互相替代。先完成 A1 traex CLI 与 Codex 新工具，再完成 A2 TRAE IDE；pi RPC 单独 R。

## 版本、发现与配置

只探测 PATH、已知本机位置和用户所选路径，版本/渠道来自实际安装输出。当前已只读发现 TRAE CN 3.4.14、TRAE SOLO CN 0.1.19 和 traex/traecli 0.214.1 internal；均不是接通证明，国际版/公开CLI不推断兼容。探测在新临时目录，关闭用户配置/上下文读取，不调用模型或升级Agent。

traex CLI 的项目配置是用户所选workspace下 `.trae/traecli.toml`，与共享TRAE_HOME及私有TRAECLI_HOME区分。默认优先项目项，明确绑定该workspace；worktree仍按服务Git common directory映射。Codex使用已有 `.codex/config.toml`。安装/范围编辑/断开只增量修改自己的Memlane区块，先一致性读取并备份，保留其他配置；相同名称被其他程序占用或已被修改时拒绝覆盖。配置写入失败按原回执恢复，不擅自替换用户日常配置。验收只用新合成root/workspace/Agent目录。

TRAE IDE 的配置文件/重载入口必须以已安装版本的实际实现确认。项目MCP可明确传workspace，不能把stdio进程cwd当当前聊天项目；未知配置位置时提供具体可审查项，不猜测全局路径。界面“已发现/待连接/待重载/可用/异常”与真实回执对应。

## 工具与授权

四类Agent共用受信服务校验scope/generation/workspace，工具入口不授予DB句柄、审批、覆盖、删除或移动能力。成功保存、读取、检索返回与明确采用按原契约分别记录；保存返回persisted而索引独立，当前版本与来源由服务读取，失败不能当空命中。其他项目与全局范围仍需明确授权，连接变更或断开使旧代次失效。

新初始化/采用工具在支持端真实验收，Codex此前通过的普通保存/查询不代表新工具已经通过。初始化隔离模式只含三个批次工具；普通连接支持新工具但不因连接成功声称初始化成功。收费模型不在发现、安装、复制prompt时自动调用；用户明确“验证读写”允许合成工具回执验证，并沿用Agent自己认证/模型选择。

CLI验收独立配置目录及临时会话，只链接由Agent读取自己的认证文件，不提取令牌，不加载用户聊天、插件、Hook、技能或其他MCP。禁用shell/file/command内置工具，显式Memlane MCP清单与工具过滤；隔离必须通过本机实际prompt-input与真实调用验证。开发会话权限不作为子进程权限。TRAE IDE验收使用独立profile/workspace，不拷贝聊天库或用户全配置；若缺隔离登录则保留具体外部阻塞，不以CLI替代IDE验收。

## 验收与交付

A1真实App连接/范围/安装/重载/断开，实际traex与Codex保存/get/search/status/采用以及初始化新工具，真实Rust当前版本/来源。合成数据明确隔离；scope/workspace/代次/重放/配置保留及服务失联错误用标注协议/FS夹具补足。A2另外核对实际TRAE IDE的重载和工具使用，不从安装JSON/文档默认宣称可用。旧schema11非空WAL迁移仅在实际功能需要新表时进行，新增Agent字符串本身不扩充schema。

证据 `build/p3-agents/`；检查/review/实际窗口/完整包/阶段handoff/提交后继续R/G/F，正式分发缺必需项时总Goal仍未完成。

## A1 工程收口补充

默认模型仅解析共享与所选workspace项目TOML的标量选择，项目优先；显式App模型选择优先。当前隔离内部CLI路线使用内置trae provider；遇到默认自定义provider明确要求另行适配，不能默默替换或复制配置中的凭据。实际CLI返回事件未报告模型时，证据记录selected_model与未知actual_model_reported分开。

每次验证拥有独立进程组，取消关闭CLI与MCP后代，stdout继承有等待上限；正常结束也清理组，退出未确认不能显示验证成功。真实sh/sleep后代持pipe检查是明确OS故障夹具，真实模型正常/取消另验。debug prompt-input显式只读sandbox并复制相关配置覆盖，仅证明当前上下文/命名空间，不宣称与exec的所有ignore-rules/strict/ephemeral启动语义等价。

配置断开包含服务代次撤销与文件清理两步；验收必须等完整UI回执、安装metadata消失与原配置保留，再显示通过。原地重建/同identity换路径的首次helper启动曾超时，后续同包启动健康；该未解释故障保留F1，正常重试通过不能宣称移动/更新问题已修复。
