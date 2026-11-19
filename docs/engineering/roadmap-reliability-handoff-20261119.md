# Roadmap 五项可靠性修复接续

2026-11-19。本轮已完成五项修复与 dev 主流程核验，另已修复R19-LINK01：已有记录不需要文件夹即可关联，ml-user-profiles真实关联及同库重启保持通过。当前保留同一日常服务及 Native 开发窗口。本文替换旧等待模型下载与 Computer Use 重启交接；各轮原失败、未知边界和历史 PID 仍保留在[执行记录](roadmap-reliability-20261119.md)。历史全量 Goal 不恢复，未进入 release。

## 当前结论

| 编号 | 结论与完成范围 |
| --- | --- |
| R19-01 | 当前官方采集正常，TraeX 原文 Native 可读、Codex 保存回执、两条真实 pi 保存已核对；没有重写采集器或重放未知消息 |
| R19-02 | 登记表为权威目录、一次性接管，身份/阅读保持；实际同库重启前后 7 项目/绑定/权限一致，项目阅读与搜索返回通过 |
| R19-03 | 三服务 owner 监测与 recover 已修复；真实异常退出/收尾/同库恢复、窗口关闭后后台保持/重开通过 |
| R19-04 | 丢成功回信后的重启一致性修复及回归通过；Native 双向配置切换、浏览/取消保持、真实在途取消和同源接续成功 |
| R19-05 | 固定版本组合未复现兼容故障，真实 1000 节点及 Native 点击通过；实际空图无样本，无官方文档身份实体明确不可定位 |

新增 R19-SESSION01 修复官方 session_ids 解析；R19-CONTINUE01 修复旧停止批次范围阻挡已确认交接源。原取消 improve `9e75f2ab-08e2-4160-b68a-4757047183bf` failed/CancelledError，新 improve `32487eb6-41d2-4b7a-bff4-0432539472af` succeeded（2026-11-19 00:47:22.202018 UTC），同主体/项目/源，官方日志 documents=3。实际处理者 Codex，材料主体 pi。旧 stopped、unknown/submitting 和失败回执未覆盖。

## 模型及日常运行

用户已授权并完成恢复原 BAAI/bge-small-zh-v1.5，94,781,076 字节，SHA-256 `1294ea4b6331115a353d81f96b85e8c8d7fdcc284453d5b2fab5b016230aad38`。离线实际 probe 为有限值/512 维；持久根 `.local/daily/components/cognee-bge-zh/1.5`，私有 HF_HOME 指向该根，HF_HUB_OFFLINE=1。未换模型或重建原索引。

同库配置 `.local/daily/runtime.json`，业务根 `.local/daily/official-store`。最后核对 owner 94208 ready（内核启动 Thu Nov 19 08:42:03 2026），completion 94212、official 94263、project_control 94341。官方61255、管理18777、桥18173；管理/运行时 active 为 existing-codex revision2、next=null。Native PID97915、window321801、watcher97794保留。原模型服务 Ollama78338正常退出，50639不再需要运行。最终官方/图谱/桥在途均0。

PID仅为此次快照。后续先核对内核身份/端口/独占锁，不能直接沿PID发信号；现有后台在时不再start第二组。资源实际路径见私有 `build/diagnostics/roadmap-reliability-20261119/final-resource-receipt.json`，保留完整依赖及原库/ACL/主体/凭据，不清空build或p3-prototype。

## 证据与工作区

主证据目录 `build/diagnostics/roadmap-reliability-20261119/`：

- 原模型 embedding-restored-probe.json、embedding-files-verified.log 和失败/续传日志。
- continuation-terminal.json、continuation-after-restart.json、inflight-*：真实取消与同源接续。
- native-resumed/ 共24张真实PNG，已查看；18–24为最终搜索详情/返回/窗口重开，不提交正文截图。
- before-window-close.json、after-window-close.json、final-live-state.json、ollama-final-cleanup.json、final-resource-receipt.json：服务与收尾。
- 初始118项Python、完整Go test/vet/race；字段修复后39项generation回归；接续修复后34项与lint通过，旧失败证据保留。source-manifest-final.json为最终候选，旧manifest不覆盖。

基线 main/6c524b2，本文件随本轮源码交付归档，历史未提交事实保留，其他会话修改并存；不全量暂存/重置、不把整文件都视为本轮所有权。生产修复包括 generation_startup、generation_handoff/runtime、incremental_processing、owner_lifecycle、official_service_dev、codex_completion_endpoint、project_control/server、dev/recovery、project_access_ui/project_directory；测试与文档清单见执行记录及最终manifest。无MR、无release。

## 后续边界

五项本轮修复及 dev 主流程已经收口，无需重复模型下载、故障注入或完整测试矩阵。若继续开发，先核对当前源码与manifest、按新变动运行对应门禁。实际空图无样本、无document_id/data_id实体无法正向定位不记为通过；pi默认Flux扩展缺文件已按用户追加授权通过官方接入恢复；默认启动、默认扩展真实任务及同库保存通过，详见执行记录最后一节；其他会话V3全量视觉验收不属于本轮。旧具体失败计数保留：R19-04=1，SESSION01=1，CONTINUE01=1，EMBED-PATH01=2；DEV-STOP01历史四次及后续通过不重置。


## 当前关联修复与运行状态（覆盖上文旧PID）

R19-LINK01已实施。已有记录直接关联，不需workspace；新建仍需文件夹。ml-user-profiles登记566ea5e7-646c-54a5-bb03-2700bad05c04，原dataset7490f263-f9d6-564f-9462-582c0652df52，aliases=[]、verified。关联前后原3条记录ID/contentHash、样本正文SHA及全部Agent有效权限保持。正常重启后8项目及原7项目绑定/覆盖规则保持。31项项目控制检查、桌面关联/取消/未知回执、完整Go test/vet和lint通过。

最后同库owner84481 ready，completion84568、official84612、project_control84714；Native85873/window322535；PID只作现场快照，先核对身份再操作。日常Codex revision2和恢复的embedding持久根保持。最新证据为link-records-*.json/log及source-manifest-link-records.json，Native截图26-linked-existing-source.png；当前候选新增project_create.go/ui.go、project_access.go及project_control/server字段。最新规则见product/design最后增补。本文件随源码交付归档，保留其他会话修改，不进入release。

提交前已补实际窗口的“取消选择”和“取消”：文件夹区恢复、弹框关闭、8项目ID集合保持；截图28–30及link-records-cancel-native.json。保存结果未知的核对仅有HTTP/组件回归，不伪装日常故障注入。
