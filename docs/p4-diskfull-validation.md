# F1d 真实磁盘空间故障与恢复

2026-11-08，受限APFS真实ENOSPC、App保存/备份失败与释放后当前检索、长文展示恢复及最新完整0.0.9包独立复验已通过。本切片不代表全部F1或正式分发完成。契约见 [磁盘故障契约](p4-diskfull-contract.md)，恢复基线见 [F1c验证](p4-restore-validation.md)。

## 平台与输入

本机macOS26.6.2 arm64、Go1.27.3、MyGo0.7.4、schema14、Rust worker0.0.3/原文协议1。初始完整0.0.7 App在 `build/p4-diskfull-app/` 和 `build/p4-diskfull-large-app/`；修复后的完整0.0.9 App在 `build/p4-diskfull-preview-fixed-app/` 与 `build/p4-diskfull-final-app/`，含helper/LaunchAgent与Agent桥。本切片不调用整理模型，Rust检索使用实际本地E5模型；固定Enola组件准备不代表本轮又验收代码图谱。

只挂载自己创建的128MiB APFS image到 `memlane-p3-code-app-*` 合成根的 `client/data`，组件在卷外的同根runtime。未写满宿主磁盘，未读取用户知识或修改日常Agent配置。主包采用ad-hoc签名，外观包含保留的用户branding输入，不能称为正式发布包。

## 分层证据与失败保留

| 证据 | 实际结果与边界 |
| --- | --- |
| `build/p4-delivery/diskfull-native-preflight-v3.json` | 128MiB卷写入128,974,848 bytes后实际errno28，删除填充后回收、卸载通过；actual_app=false。早期hdiutil错误保留，未发生挂载。 |
| `diskfull-actual-app.json` / `.log` | 实际App已索引v1、创建备份，填充128,229,376 bytes至ENOSPC。50KB编辑仍成功保存v2，备份未发布。释放后检索通过，但脚本误将HistoryPage对象当列表计数而failed；停服/卸载无清理错误。原报告未覆写。 |
| `diskfull-actual-app-recovered-v2.json` | 继续同一已释放卷，真实App/Rust返回当前v2，历史分页items=[2,1]，创建新备份，停服/卸载通过。跨App重启新备份不声称同ID重放。 |
| `diskfull-large-native-preflight.json` | 创建/挂载/errno28成立；删除填充后立即读取statvfs未等到APFS回收而failed，卸载成功。脚本已改成15秒有限等待；此旧报告保留failed。 |
| `diskfull-large-actual-app.json` | 填充128,278,528 bytes后，750,046 bytes编辑失败，App保留草稿且当前v1仍indexed。App备份失败；独立真实私有IPC请求返回 `mkdirat .building-…: no space left on device`。释放后草稿保存v2，索引未在120秒内完成，整个报告failed；停服/卸载无清理错误。 |
| `diskfull-large-recovered-app-v2.json` / `diskfull-large-repaired-app-v3.json` | 两次补验因大正文读展示使capture超时而failed；各自detach busy，原报告不改passed。后续独立 `diskfull-large-cleanup-retry*.json`仅证明正常卸载重试，不替代App验收。第一次只修详情分页，列表SingleLine仍传整篇正文，第二次失败据此补上240字符预览。 |
| `diskfull-large-preview-fixed-app-v4.json` | 真实App翻页/全文剪贴板SHA、短v3/Rust、历史[3,2,1]及新备份已通过，但driver SQLite上下文未关闭句柄导致detach失败，整个报告仍failed。显式closing修复后继续同库，不造新的知识版本。 |
| `diskfull-large-final-app-v5.json` | 完整0.0.9 App实际历史v2翻页、全文复制SHA、当前v3/Rust、三版本/来源、点击后新备份ID与真实成功文案、SQLite integrity/FK、停服及卸载全部通过；cleanup_errors和detach_retries均为空。大正文索引标记false。 |
| `diskfull-final-native-preflight.json` | 最终helper真实APFS复验，errno28、释放后132,763,648 bytes可用、实际设备/dev/disk7s1、卸载与清理通过；actual_app=false。设备号仅是该报告的当时身份，不能写死用于后续卸载。 |
| `diskfull-final-actual-app.json` | 完整0.0.9包独立真实ENOSPC主流程PASS：50,046 bytes编辑失败，原草稿值/失败文案、v1 indexed与来源保持；App备份失败及独立IPC实际ENOSPC；释放后App原草稿重试v2、真实Rust、历史[2,1]、新备份、完整性、停服/卸载通过。cleanup和detach_retries均为空。 |
| `diskfull-gate-faults.json` | 八项attach返回失败/超时/mountpoint不匹配/plist损坏、旧文件/链接与独占更新夹具PASS；明确actual_app/actual_apfs=false，不替代真实ENOSPC。 |
| `diskfull-gate-faults-final.json` | 修后九项夹具PASS，补充busy后旧disk编号被其他image复用；每次重试重新核对exact image，不能卸载重新分配的旧编号。 |
| `diskfull-ownership-gate.json` | 自有实际APFS上放合成文件与IPC监听sentinel。child准确拒绝fresh-data前置条件，不删除不属其本轮的文件，不停已有监听，不卸已有attachment；owner清理通过。actual_app=false、enospc_test=false。 |
| `diskfull-ownership-gate-final.json` | 修后独立APFS负例PASS，主验收image保留，子报告使用本次新UUID；准确拒绝、无stop请求、合成文件与已有挂载保持，owner清理无错误。不是模型或ENOSPC主流程替代。 |

大正文主要为明确合成的重复负载，不能用于整理质量评估。此索引失败未证明模型质量错误，也不改写为磁盘故障索引恢复成功。后续短v3必须作为另一次用户编辑保留大v2、v1及来源，当前检索排除旧版本。

## 实际窗口

已实际打开 `build/p4-diskfull-large-actual-app-screenshots/actual-full-save-system.png` 和 `actual-full-backup-system.png`，以及 `build/p4-diskfull-large-final-app-v5-screenshots/long-v2-readable-system.png` 和 `disk-released-recovered-system.png`。失败在栏内保留草稿/原文/旧备份；修后历史v2第2/92段、完整复制和新备份实际文案有对应图与回执。连续浅蓝侧栏、单色操作与三栏结构遵守已选设计。设计图此前已实际打开；自动化截图标记 `actual_system_window=true`，未用headless fixture替代。

## 修改与适用检查

列表预览240字符与UTF-8长文阅读窗口修复真实布局卡顿；复制、编辑及恢复继续使用完整正文。新 `long_text_test.go`覆盖大中文/emoji重组无丢失、分页/上一段、全文Clipboard与短预览保持。`diskfull-final-full-tests.log`、`diskfull-final-vet.log`与`diskfull-final-desktop-race.log`均exit0；无生产服务、worker或schema改动，因此其他未受影响后台验收引用已有基线。

review位于 `build/p4-delivery/review-diskfull/`，分组与最终跨组active均为0，HTML/Markdown报告及历史修复记录齐全。原attach异常清理、manifest链接、旧备份误认、草稿重建掩盖、未取得归属的清理、重试设备编号复用与负例旧报告误认问题已修，原记录与修复依据保留。新完整包使用新的隔离身份，原同身份helper替换失败仍保留在 [F1诊断](p4-helper-replacement-validation.md)。

## 包与体积

`diskfull-final-package-integrity.json`记录最新App strict/deep codesign与DMG verify通过，签名仍为ad-hoc。`diskfull-final-package-sizes.json`在停服后的实际data卷只读挂载期间测量；卸载后image SHA不变。

| 指标 | bytes | 范围 |
| --- | ---: | --- |
| 主DMG下载 | 17,604,138 | 最新完整0.0.9包 |
| App普通文件逻辑大小 | 39,318,700 | 含UI/helper/LaunchAgent/桥，不含组件 |
| 首次额外组件下载 | 301,171,513 | 实际Rust/Enola/E5三份成功归档，不含HTTP/失败重试 |
| App＋实际准备目录普通文件逻辑大小 | 598,686,761 | 组件、模型、数据、设置与缓存；跨宿主/runtime和受限data卷求和 |
| 对应st_blocks分配文件指标 | 623,386,624 | 不计目录元数据；不等于删除可释放空间 |
| 额外故障image逻辑/分配大小 | 134,217,728 | 主故障image单列测试开销，不是客户端依赖；其他负例卷/源码与测试下载源也不计入准备目录 |
| 主App正式更新下载 | 未知 | F1同身份更新与F3渠道未通过 |

## 后续与限制

阶段提交与远端结果收尾继续。后台许可撤销、worker/服务真实崩溃、App路径与同身份helper替换、R/pi、A1b/Codex、A2b/TRAE及F2/F3继续独立验收。Developer ID、公证、正式更新源和干净macOS15+arm64没有本页证据；主App正式更新下载量未知。750KB重复大正文没有索引通过记录，保留为已保存历史和当前检索边界，不替换正常正文验收。
