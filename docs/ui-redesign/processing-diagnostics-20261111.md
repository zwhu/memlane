# 真实验证依赖诊断 · 2026-11-11

集中包001实机发现：当前使用处理修订仍引用连接代次1，而该隔离根连接已经是代次3；计划保存被`connection_changed`拒绝，不能替用户自动换执行者。重新选择当前pi卡片并保存的新修订引用代次3。第一份只读检查时新修订尚无验证行，不能推断为superseded；第二次Native实际获得`dependency_changed`失败回执。根目录、ServiceRoot、安装回执代次／真实验证、扩展哈希和模型选择的限定元数据均匹配；没有读取auth内容或用户历史聊天。

旧Owner把排队后或模型结束后ResolvePi的所有错误，以及实际依赖摘要变化，都折叠为`dependency_changed`，且没有持久阶段，因此单凭001回执无法判断具体来源。不放宽任何依赖、代次、配置或来源校验；下一集中包通过结构化安全诊断定位。

schema26新增`processing_validation_diagnostics`，与失败／取消验证终态在同一事务提交。`ProcessingValidation.Diagnostics`仅包括`Stage`、`ResolveCode`和`ChangedNames`；阶段限定queue/preflight/model/postflight。真正摘要变化保留`dependency_changed`，仅记录发生变化的依赖名称；ResolvePi返回的真实AccessError code直接保留，不再被通用码覆盖。普通错误只给固定安全码，不持久化error文本。阶段与名称可随失败回执重开读取。

名称严格限定为settings.json、models.json、auth.json、adapter_protocol、processing_configuration、agent_version、agent_executable、agent_configuration_directory、helper_executable、extension、connection_verification。各依赖摘要只保留在服务内存且标记不参与JSON序列化；数据库诊断不保存内容、路径、密钥或摘要。无新增可由IPC伪造验证终态的入口。

迁移沿用现有迁移前副本；旧验证事实原样保留，不补造历史诊断。备份／恢复保留诊断历史，但依然按原契约退休成功授权和活动验证。定向测试覆盖真实SQLite终态与诊断原子保存、无效名称拒绝且保持原验证状态、旧schema25迁移与备份、排队撤权保留原connection_changed、模型结束后变化名称，以及配置／状态回读一致。

本批不宣称001依赖变化根因已定位，不以合成协议测试代替下一集中包002真实验证；未单独完整打包或操作Native。

定向命令：`sh scripts/go.sh test ./internal/processing ./internal/store -run 'TestValidationOwner|TestProcessing|TestRestore|TestIndexRebuildRestore' -count=1`。9272输出processing 0.843s、store 2.208s均ok；本批`git diff --check`通过。统一全量复验和002完整包由主Agent执行。
