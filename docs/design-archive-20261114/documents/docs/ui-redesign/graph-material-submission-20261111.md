# 图材料正式提交适配（2026-11-11，源码切片）

设计沿用 `graph-presentation-20261111.md` 与 I01–I06。此批没有 Native 操作、真实模型调用、正式图质量验收或新组件打包，不能称主线 9 已完成。

## 已落源码

新增 `CogneeSubmissionRequest`，只接受明确 project/dataset、最多16个确切 memory/revision、当前 processing revision/selection generation 和操作 UUID。daemon 只从有效当前正文构造材料；不接受前端正文、endpoint、凭据或来源推断。公开 IPC 为 `cognee_submission_start/get/cancel`；开始和取消需 UserConfirmed。选择范围和 Token 确认的 Native 按钮仍待主 Agent 接入，本切片不新增默认自动提交。

schema28 将提交与旧导入分开：稳定 library identity、immutable material/processing snapshot、官方 document identity、receipt 与执行证据独立落盘。材料保留原 memory ID/revision/origin/source_detail，不复制成知识、不重写原文链。官方 document UUID 包含 library/service/dataset/project/memory/revision/body digest 和 processing revision/dependency digest。正式回执必须完全匹配。

新绑定显式 `material_submission=true`，新授权从可信 workspace 解析当前 pi/Codex 项目授权，不把电脑级空 ContextProject 伪造为项目。旧 session/import 仍保留原 Codex 门禁。新绑定不得读取旧 cognee-import-v1 图材料；旧投影过滤未放宽。新投影另行要求当前库、同版本/正文摘要、有效派生状态、真实 quote、UUID chunk、已完成 task 和已封非空成功执行证据。GraphSource.Origin 由当前本地版本赋值，原始记录不得显示为知识。UI 按此字段的承接由主 Agent/content 整合。

恢复备份会删提交/任务、旋转 library identity，并保留原有内容与版本；旧 owner 回执不能跨恢复生效。服务重开将未封任务标 interrupted，不自动恢复付费推理。取消等待 Python 同鉴权 cancel ACK 与 gateway.Close；确认不完整时显示 interrupted/material_cleanup_unconfirmed 或 executor_reap_failed，不能当取消成功。

## 官方执行与模型隔离

Python 同 Cognee 服务进程新增 `POST /api/v1/memlane/material-submission`；身份接口增加 `material_submission_v1` capability，旧组件会在模型调用前被拒绝。单项 ingestion 复用官方 resolve_data_directories/ingest_data Task，以带固定 .txt 名的 BytesIO + DataItem 传入，正文即使像 URL/path 仍只是文本。抽取复用 installed Cognee 1.6.7 的 get_default_tasks 与 run_pipeline(data=[确切 Data],datasets=[确切 UUID])，无空 data 回退、无整个 pending dataset 提交。cognify incremental_loading=False/data_cache=False，不能用旧 processing 的缓存冒充新执行。

每任务 schema gateway 由 daemon processing.Owner 拥有，冻结已明确启用的处理方式、依赖摘要；每次模型前后 ResolveTask。Python LLMConfig 只留在 ContextVar，三个 stage 的模型/endpoint/key 明确绑定本次私有 gateway，不修改进程全局模型环境，不接用户 Python/provider 配置。

Memlane 安装一次专用 SDK dispatcher，仅 alias `openai/memlane-processing` 经当前 ContextVar 进入严格 schema adapter；其他模型仍走原 SDK。该适配直接 httpx(trust_env=False、不跟重定向)，不经 LiteLLM fallback/retry；transport/provider 细节在官方 pipeline 可能持久化异常前转静态错误。临时 token 不进入 task/metadata/source/log。官方 pipeline 原生 LiteLLM 的异常路径不能据此宣称同等保证。

Gateway.Close 后持久化真实安全 Receipts（模型选择、成功/错误码、HTTP 或进程回收事实），不持久化 prompt、输出、endpoint、token。无 gateway 调用或失败调用不能封 task completed，不能投影新材料。

## 验证与交付边界

- usage harness：`scripts/verify-daily-agent-usage.py`，准备和受控执行见 `daily-agent-usage-harness-20261111.md`，7模拟项46894 exit0。
- Python材料模拟：67558三项 exit0，单文档、literal URL、processing 身份及私有 gateway。
- installed SDK：`python3 scripts/task.py -- .local/cognee-python/bin/python scripts/verify-cognee-processing-adapter-contract.py`；14556/68558 exit0，实际 installed Task/run_tasks_base → LLMGateway → dispatcher，两并行 ContextVar 不串 token/endpoint，错误只有一次 loopback 调用；ASGI取消先等待 finally，旧 operation replay409，临时日志不含合成 token/provider原错。此项未执行完整实体图抽取。
- store/daemon普通定向76473 exit0：实际 SQLite、严格投影、restore身份退休；daemon fake Cognee → 真 gateway → loopback HTTP fixture，并确认取消 ACK。13271因loopback测试fixture未读request body导致清理等待，已定点终止后修fixture，不计通过。
- 最新针对原始记录来源与并发回归继续以本批最终会话记录为准。

仍待：Native 明确选材/Token入口，原始记录/知识图来源UI投影，重新冻结的真实 Cognee 组件、当前已启用真实处理方式、完整实体图→同版本知识→原始记录往返和质量；本切片的 fake Cognee/provider 不是这些验收的替代。

## 下一组件构建要求

004旧组件不能冒充含新 helper。当前归档 `1.6.7-memlane.7` 不含本次完整适配；后续由主 Agent统一准备新版本（建议 `1.6.7-memlane.8`），不要覆盖旧 archive/owner，也不复制重复完整环境。builder现记录四个Python文件的source_sha256并检查构建期间漂移，后续报告须保存全部源hash。

源码状态哈希清单可保存到 `build/graph-material-submission-next/source-info.json`。准备命令仍为受管 `python3 scripts/task.py -- python3 scripts/build-cognee-service.py --out <新组件构建目录> --report-out <小型证据目录>`，本批未运行。之后须用新component manifest/hash走既有组件准备、私有根启动/health/capability/取消/回收与真实模型验收，再统一完整包。

当前 official/runtime.go 的 verifyModel+旧 ModelBridge gate 仅认可 `.5/.6`；`.7`本就受此限制，不能为通过本批而扩白名单。新架构的 current-processing gateway 需以真实 capability/schema/依赖和生命周期证据完成新组件接入，再由主 Agent统一处理兼容门禁。source-only `material_submission_v1` 不意味着既有二进制已具备能力。

## 补充收口：本机项目授权与重开回执

新增 `CogneeProjectAuthorization{Operation,Project,ConnectionID,Generation}` / `Library.AuthorizeProjectCogneeMaterial` / `cognee_project_authorize`。服务端从已注册项目解析 workspace，支持实际授权 pi/Codex；不让 Native 填 endpoint/dataset UUID/密钥。official helper只接受已运行且命令/配置/组件路径属于 App 私有根的服务，检查 health、instance 与 material capability，再用 OwnerToken 和项目私有 principal/API key 创建或读取 `memlane_materials_<library>_<project>`。不启动旧 Codex 首任务、不安装插件、不读用户 Agent auth；未准备组件明确失败。

`cognee_submission_latest` / `Library.LatestCogneeSubmission(ctx,project)` 从当前库读取最新任务，无任务返回 state=none。任务返回原冻结 Sources（ID/revision/origin/title）与原 processing revision/mode/model 摘要，适合重开恢复上下文，不带正文、endpoint 或凭据。忙时新 Start 不落假 prepared 行；旧 operation 的原请求可在源版本或当前 processing 设置变化后只读回放，仍检验当前项目授权，改同 operation 请求冲突，不能重跑付费。

最终三包 targeted race 会话 **6297 exit0**：official 私有认证/dataset 契约、store来源/恢复/重连/旧门禁、daemon真实 gateway→loopback HTTP/取消与lost-receipt。原始记录 Origin/source_detail/版本不被改成知识。78623发现的是测试第二次明确启用遗漏 ExpectedGeneration，已补当前代次后63869及最终6297通过，没有放松CAS。

已用004停服合成根跑通usage默认准备：76215 exit0，`build/daily-agent-usage-next/prepare.json`，候选0805dd31-8fd1-52da-aa98-5058da16cc28@1；未联系daemon/模型，执行前仍需复核active/indexed/current。停服库metadata选取采用immutable只读，正式durable证据仍读取活动WAL，不采用immutable。

主 Agent 已另行启动唯一新组件冻结17658，报告目录 `build/goal-component-materials-20261111-001`，输出原scratch `service-dist-material-v8`；本记录仅证明源码交接与命令，不提前认定构建、Prepared runtime或真实图通过。四Python源已冻结，Go主实现在6297通过后停笔。后续实际组件/Native/模型证据由主 Agent集中补充。
