# Provider／Magpie 真实HTTP处理接入 · 2026-11-11

本批补首版「保存配置→真实验证→明确启用→冻结材料任务」后台缺口，复用既有三协议适配和材料校验。生产源码保持MyGo／daemon／SQLite／Cognee链路，不新增供应商目录或猜测模型ID；本批没有调用真实云模型、读取用户pi密钥、改用户Agent环境或操作Native。

## 实现

Owner现在按处理方式分派pi或HTTP，共用已有单并发槽位、生命周期、两分钟验证期限、显式取消与终态持久化。新增通用Binding并保留PiBinding兼容别名；Provider／Magpie不需要先连接pi，也不会故障回退到另一Agent或供应商。

Provider仅取独立Keychain中明确版本的密钥，目标绑定供应商、完整服务地址和协议。`SaveProcessingCredential`要求明确用户操作，成功只返回引用与供应商元数据，消费后清空提交的密钥字节。新地址／协议需要新密钥版本；供应商元数据独立按revision进行CAS，多个供应商互不覆盖，多个模型复用其配置。失败只补偿本次新建且可证明未被持久引用的Keychain项；旧版本供已冻结任务使用，不自动删除。

`ProcessingSuppliers`重开可读各供应商地址／协议／凭据引用，不返回Key。`ProcessingModels`只访问用户配置的接口，返回真实ID、名称、owned_by、分页和读取时间；ID、显示名、供应商字段均有限长及UTF8／控制字符校验。目录与响应中回显的凭据原文被拒绝；上游错误body不进入诊断。HTTP禁止重定向转发凭据。

Magpie空地址／协议默认检测`http://127.0.0.1:3425/v1`的OpenAI Chat目录；只允许字面loopback HTTP，不填Key、不读取Magpie私有配置／上游Key，禁用此请求的环境代理。验证、启用和任务前后读取真实目录的选中项，以目录ID／报告供应商参与依赖冻结；消失或目录不完整不会变成「零模型成功」。这些观察不能冻结网关未公开的路由内部状态。

HTTP请求使用完整原生JSON Schema，无工具字段、无自动重试或prompt-only/json_object回退；本机仍校验完整schema。正式材料任务复用同一份组织提示与`OrganizationSnapshot.Decode`，精确来源／版本／差异／待确认门禁不变。模型只能返回候选；合并、覆盖、删除和移动仍须用户确认。计划与通用经验既有processing快照可复用此分派，仓库整理仍使用项目明确绑定的Agent，不被全局Provider配置替换。

## 回执与取消边界

HTTP单列`ProcessingHTTPReceipt`，记录请求模型、响应报告模型、Magpie报告的供应商／模型、实际请求发送、完整响应、schema校验、时间及本机取消。两种模型身份分开，不把网关回退冒称固定命中所选模型。不伪造PID、进程回收或取消ACK。

取消释放本机HTTP传输，并拒绝迟到结果；上游是否停止推理或计费没有通用证明。本机不启动Agent、不开工具／文件／聊天上下文；请求带固定内部处理标记。真实Magpie及其具体上游／插件路径的防循环、取消和计费行为仍需真实网关单独验收，loopback夹具不能证明它们。

依赖前后复核保留原错误码，区分凭据缺失／失效、认证失败、限流、无效请求、网络超时、响应不完整、工具调用和schema失败。新增安全诊断名称`independent_credential`、`gateway_model`，不保存密钥、其摘要或内容。Readiness仅观察本机依赖与Magpie公开目录，不等同持续远端账号健康保证。

## schema27与恢复

新增`processing_suppliers`及`processing_http_receipts`。HTTP验证终态与真实HTTP回执在同一事务提交；成功必须具备已发送／完整schema请求／完整响应／本机校验等证明。正常的凭据撤销可能发生在复核和发请求之间，此时保存明确失败，不捏造HTTP回执、不为普通配置错误停掉整个服务。

迁移前保留原库副本。旧schema23/24及其他手工降级夹具删除全部未来表后重建真实旧库；25/26/27新增表迁移严格创建，不靠IF NOT EXISTS掩盖失真夹具。整库恢复保留供应商引用和HTTP历史回执，退休旧验证成功、停用当前选择；不会因备份含成功记录而自动发起推理。

## 验证与未完成

- 真实loopback HTTP三协议验证、明确启用、精确模型目录、独立凭据隔离、Magpie免密／报告实际路由、HTTP取消与迟到拒绝。
- 正式材料冻结模型后切换全局选择仍用原模型；schema合法但伪造出处被业务校验拒绝；取消不发表候选。
- 真实私有Unix IPC→Owner→HTTP→SQLite材料审核：覆盖建议先保持review_pending／原文v1，用户明确确认后才产生v2；HTTP租约PID0且不声称回收了进程。
- 凭据排队撤销、发请求前撤销、供应商CAS、失败的新Key引用补偿、多供应商重开元数据、敏感回显拒绝及HTTP错误分层。
- schema26→27原样迁移及副本、HTTP回执原子性、恢复后历史保留与成功授权退休；旧迁移专项回归。

51798相关定向全部通过；83534含真实私有IPC审核通过；88134受影响race最终exit0（processing6.998s／daemon2.752s）。schema27专测42908另登记；统一全量test／vet／race、004完整包与Native由主Agent集中执行。

**仍未验收：**真实独立Provider账号／模型、真实Magpie网关及具体上游隔离、真实Keychain输入到新Native页的完整交互、普通／窄窗与初始化复用。没有显式独立凭据时不发真实云请求，不用httptest成功代替这些证据。Codex处理桥未因HTTP分派而实现。

## 协议依据

复用既有适配的原生schema位置，并核对[OpenAI Structured Outputs](https://developers.openai.com/api/docs/guides/structured-outputs)与[Anthropic Structured Outputs](https://platform.claude.com/docs/en/build-with-claude/structured-outputs)；不因此宣称任意兼容供应商或模型支持完整schema。

Magpie默认loopback、普通模型目录及实际路由报告依据固定提交的[官方参考](https://github.com/yetone/magpie/blob/ed0ab567cba78fae8ea9d9c9a07a7446e315b787/docs/reference.md)。实际模型只使用API返回ID，不使用Codex专用目录或显示名拼接路由。

schema27专项42908最终exit0（store2.340s）；只格式化该新增测试文件的47450不改生产源码。小型汇总`build/goal-provider-http-20261111-001/report.json`区分真实loopback/IPC/SQLite与未执行的真实Provider／Magpie／Native／Keychain写入。生产源码稳定点已交主Agent集中检查。
