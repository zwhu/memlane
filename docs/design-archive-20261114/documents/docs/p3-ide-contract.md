# P3-A2 TRAE IDE 接入契约

2026-11-08。A2a配置与外部回执等待已实现，真实IDE模型验收待隔离登录。实际安装TRAE CN3.4.14（bundle cn.trae.app，commit5d9926ed75dce7fa36dff87dbef5fb53e06b790a）的workbench源码确认：项目 `.trae/mcp.json`、`mcpServers`对象，stdio entry为command/args/env/cwd/disabled，支持JSON注释及尾逗号。另有TRAE SOLO CN0.1.19，仅已发现；本切片不将它或国际版默认标支持。证据 `build/p3-agents/trae-mcp-loader-snippets.json`、`trae-mcp-schema-snippets.json`及`trae-cn-cli-help.txt`。

## 安装、范围与状态

在App用户明确选择安装、项目/workspace与允许范围后，仅增补该项目的Memlane entry，不修改用户全局IDE配置。保留其他entry、注释/尾逗号与可识别格式，先备份；同名未拥有/外部改动冲突拒绝覆盖，撤销只移除自己的entry。工作区参数由明确项目配置提供，不能把stdio启动cwd当真实聊天项目，服务沿用Git common directory、scope/generation/workspace栅栏。

发现安装/配置写入/MCP握手分别不代表可用。TRAE IDE不启动CLI代替验证、不把IDE当后台整理执行器。App提供验证prompt，用户到自己的IDE执行；复制仅开始等待回执，不自动调用收费模型。若项目MCP需要在IDE启用或重载，显示待重载/实际步骤。没有当前连接代次/workspace的实际工具回执时，不显示可用。

## 实际工具回执与验证

验证新增一个合成raw challenge，稳定operation/marker绑定当前连接、代次和明确workspace。prompt要求memory_save持久回执、memory_status独立索引完成、memory_get正文/来源以及memory_search实际返回同一当前版本。受信服务依据已有保存/版本/usage账本核对挑战，不能由用户勾选或模型文本宣称通过；有必要的私有typed IPC只返回验证状态，不新增批准/写数据库工具。

App安装记录仅保存必要挑战ID/创建时间/待验证状态与真实完成回执；挑战24小时有效，重复复制/重启复用；取消后新nonce使旧结果无法消费。status回执仅归连接验证，混合search的各通道在一个短事务原子记账；GUI用读取序列拒绝取消/配置变更前的旧快照。重复检查不重复保存/活动，连接变化/取消/断开使旧挑战不能标记新代次成功。错误与工具未接入保持待重载或异常。验证raw和其实际读取/返回归于连接验证通道，不计日常知识或采用，不能补造历史。

日常仍使用共享memory_get/search/save/status/adopt与初始化契约；只新增知识/原始轨迹，已有改写/删除/合并/移动需App用户审核。模型沿用IDE自身认证/选择；不拷贝聊天、登录数据库或提取凭据给Cognee。

## 验收与外部条件

使用该实际安装的`--user-data-dir`、独立extensions目录和合成workspace隔离运行，明确空profile，不使用日常配置。若缺隔离登录或实际重载支持，记录具体阻塞；不拿App JSON/源码/CLI通过替代IDE真实保存/读取/检索与撤销。只给实际版本/渠道标支持。

正常、范围/工作区/代次拒绝、配置注释与外部变化保留、原文/来源/当前版本、失败与断开分别有真实证据。迁移只随实际需要引入，已有usage账本可复用时不预建平台表。输出/修改路径在App选择和文档中明确；完整包、实际窗口、检查/review/handoff/提交后继续R/G/F，正式分发缺项不标总Goal完成。

实际验证分界、保留失败与登录阻塞见 [IDE验证](../../../p3-ide-validation.md)。
