# P3-A 日常 Agent 验证

2026-11-08。A1a traex 日常接入本机已通过，契约见 [A契约](p3-agent-contract.md)。Codex新采用/初始化工具、A2 TRAE IDE、R pi RPC、G代码图谱和F正式分发继续；这份记录不把本机internal CLI当作IDE或公开版支持。

## A1a 实现与真实结果

发现PATH/已知位置/用户选择的实际traex/traecli，记录版本与渠道。所选项目 `.trae/traecli.toml` 只增补自己的Memlane区块，实际备份、外部修改保留和撤销沿用已有Codex配置模块；不升级Agent、不改日常配置。新Agent字符串使用已有connections表，schema11/protocol1不扩表。

实际CLI为traecli0.214.1、internal，App使用独立identity/root及两个合成Git项目。CLI自己读取认证文件链接；验证运行不读取用户聊天/插件/Hook/技能，只显示Memlane MCP命名空间。默认模型只读共享和项目标量选择，项目优先；显式App选择优先，默认自定义provider明确拒绝，不默默换模型或复制凭据。真实无模型项目GPT-5.4覆盖共享GPT-6.1-Sol，见 `project-model-precedence.json`。`prompt-inspection-check.json`绑定当前根/连接/代次/内容hash/实际版本与selected_model；debug明确只证明当前上下文，不保证exec全部启动语义等价。

`build/p3-agents/real-tools-report.json`、`traex-real-tools.json`和`target-replay-check.json`：真实模型运行使用选定GPT-6.1-Sol，实际CLI未报告实际模型字段，因此保留unknown。memory_scopes/save/status/get/search/adopt六工具通过；目标v1精确正文/来源、Rust检索与两次完全相同采用参数/回执，实际采用事件1。重放绑定本次save operation、memory/scope/revision与首次get的曝光回执，不能拿其他知识的采用当证明。

`real-app-final.json`引用 `real-app.json` 的真实App模型部分和 `disconnect-app.json` 的完整配置回执：App“验证读写”基于真实保存/状态/搜索；初始化只暴露批准来源/状态/提交三工具，两份真实快照新增两条imported知识，具体SHA/摘录由服务校验，Rust返回当前v1。改变连接代次后旧调用reload_required；实际新代次raw保存后用户取消验证，未验证状态保留，重新验证成功。断开等待服务撤销、metadata移除、原TOML和完整UI回执，原用户哨兵配置保留。最终五张实际系统图已打开对照设计图，见 `build/p3-agents-screenshots/`。

验证取消和正常结束清理独立进程组及MCP后代，未确认组退出不能显示成功。`process-fault-fixture.log`用真实sh/sleep后代持stdout检查取消/Wait上限/group gone/child ESRCH；这是明确OS故障夹具，未替代上述真实模型。

## 保留失败与界限

首次同identity换App路径时helper启动超时，模型未开始，保留 `app-startup-failure/`。同包无模型启动复现第二次3.52秒ready，但根因未解决，`startup-diagnosis.json`仍标F1 unresolved；不把重试正常当作移动/更新通过。

原全App驱动最终在服务已撤销、文件异步清理未完成时提前断言失败，原报告 `real-app.json`继续保留failed。驱动已修完整等待条件；额外真实App重新连接/断开并核对metadata/原配置/原初始化回执，`disconnect-app.json`通过，明确未重跑模型。聚合结果引用原失败及补验，不删除历史或假称原命令全通过。

范围/工作区/代次/撤销与当前来源的额外私有IPC验证明确标为无模型协议夹具，`scope-contract.json`不当作新的真实Agent调用。原Codex/pi协议兼容由受影响测试/审查核对，新的Codex模型仍需A1b实测。当前版本/渠道之外不推断支持。

## 检查与包

全仓test/vet、store/daemon/agents/desktop race、三组产品/来源/范围/版本/配置/并发/取消审查和完整UI/helper/LaunchAgent/桥包见 `build/p3-agents/final-checks.json`；审查发现与修复保留 `review/`。实际生产验证包 `build/p3-agents-production/`，主DMG15,263,001字节，App普通文件34,270,879字节；首次未变Rust/E5组件归档285,752,286字节，App加准备根526,328,311字节，不含工作区/Agent自身配置及开发工具链。APFS分配不等于可释放空间。更新下载字节未知。

签名完整性仅本机ad-hoc，Developer ID/公证/Gatekeeper、正式源/干净macOS15+arm64未通过。阶段提交与普通推送SHA、临时handoff见delivery.json，随后继续剩余切片，总Goal未完成。
