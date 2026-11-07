# P1 验证记录

## P1.1：设计还原与栏内状态（2026-11-07）

已实际打开 `docs/images/home.jpg` 与 `docs/images/memory-detail.jpg`，按 1200×820 内容窗口对照实际系统截图。导航 212 DIP、列表 428 DIP、右栏起点 640 DIP；文字 12/11 DIP、标题 20 DIP。首页宽幅，列表详情三栏；侧栏连续浅蓝，图标按钮单色，新增、编辑、历史与恢复确认在右栏。

验收构建仅通过 e2e 的显式 `--p1-visual-fixture` 参数加载合成视觉数据，不读或写真实记忆库；生产构建不含该夹具入口。此阶段不能声称真实保存/搜索接入。待确认整理区显示能力尚未接入的空态。

已完成实际窗口检查：首页与详情布局边界、右栏历史和恢复确认、新增与编辑草稿跨设置导航保留、未接服务时保存/确认恢复禁用。实际系统截图 `build/p1-screenshots/system-visual-home.png`、`system-visual-detail.png`；栏内截图 `visual-history.png`、`visual-restore.png`、`visual-new.png`。已用看图工具逐张检查首页、详情、新增和恢复；收紧最近新增间距后底部状态完整显示。

命令：

```sh
sh scripts/go.sh test ./internal/desktop ./cmd/memlane-app
sh scripts/go.sh vet ./internal/desktop ./cmd/memlane-app
python3 scripts/build-client.py --out build/p1-visual --name MemlaneP1Visual --identifier io.github.zwhu.memlane.p1visual --automation --skip-dmg
python3 scripts/verify-p1-visual.py
```

以上通过，实际窗口报告 `build/p1-screenshots/visual-verification.json` 为 passed。此构建包含 helper 与 LaunchAgent，未生成 DMG；完整客户端构建在 P1.4 验收。未改动的 P0 引擎、组件和系统输入法证据复用 [P0 验证记录](p0-validation.md)。

首次窗口脚本失败于对普通布局容器读取 `key`：MyGo 语义树省略普通容器，出现 StopIteration。实际截图已经取得；修正为导航按钮、列表滚动区与详情标题的已暴露边界后通过，没有降低列宽要求。系统窗口日志存在 IMKCFRunLoopWakeUpReliable 消息，但 App 流程和截图检查通过。

## P1.2：数据迁移、私有 IPC 与真实 App 读取（2026-11-07）

`PRAGMA user_version=0 → 1` 事务迁移保留 P0 正文、不可变版本、幂等操作、持久任务和绑定；新增范围、来源/原文、标题与活动表。已存在的记忆标为导入，不回填未核实的首次可用日期。项目 UUID 由服务登记并按规范绝对 Git 路径映射，全局偏好/经验各有持久 dataset。真实 Agent 工作区识别留在 P2。

UI 使用 `serviceipc.Library` 读取服务，不打开 SQLite。列表按创建时间与稳定 ID 分页，支持范围、来源、知识/原始记录和活动日期过滤；历史按 revision 分页；`get` 同时返回来源、原始正文、当前版本与索引状态。活动在首次当前版本索引完成事务中按 memory ID 去重。后台状态轮询在绘制以外运行，过期页面响应不会改写新页面；草稿和选中项保留对应范围。

服务接口验收 `build/p1/service-verification.json` 六组 passed：

- 合成旧库的两个历史版本、时间、操作 digest 和原绑定保留；旧幂等请求仍可重放；迁移后 integrity_check=ok。
- 项目规范路径与链接路径返回同一 UUID，全局范围在重启后保持。
- 13 条记录以 4 条/页读完，四页无重复遗漏；项目/来源过滤、非法分页参数验证通过。
- 实际 SIGKILL worker 后，新正文已保存但未索引；过期 revision 返回 conflict；恢复 worker 后检索仅返回当前版本与来源。恢复追加 v3，历史分页保留 v1/v2。
- 编辑、恢复、重复回执、索引重试不增加 13 条新增统计；删除后日期活动保留并标 deleted；原始记录不计数，导入另计 2 条。
- 服务重启后正文、历史、范围、活动保持，旧库版本/操作/绑定未改写。

受影响的原文事务与索引状态运行原 B1–B5 回归，结果单独写入 `build/p1/original-version-regression.json`，全部 passed；保留原 P0 报告。覆盖旧 chunk/伪造来源排除、编辑删除失效、真实失败和幂等重试、索引编辑竞争的迟到回执拒绝及重启检索。未重跑无改动的组件下载、自动更新、系统输入法或旁白。

实际 App `build/p1/library-ui-verification.json` passed：右栏登记合成项目，服务保存的条目出现在列表；最新正文、原始出处、来源和独立索引状态正确；历史两个版本可见；全局列表排除项目。系统截图 `build/p1-screenshots/system-real-library-detail.png` 已实际打开查看，历史截图 `real-library-history.png`。App 使用全新系统临时数据根，仅复用已验证的合成组件缓存；路径记录 `build/p1/fixture-path.txt`，不复制真实记忆、Agent 配置或密钥。

命令：

```sh
sh scripts/go.sh test ./...
sh scripts/go.sh vet ./internal/store ./internal/memory ./internal/serviceipc ./internal/daemon ./internal/desktop ./cmd/memlane-app ./cmd/memlaned
sh scripts/go.sh build -o build/p1/memlaned ./cmd/memlaned
python3 scripts/verify-p1-service.py
sh scripts/go.sh build -o build/p0-engine/memory-probe ./cmd/p0-memory-probe
python3 scripts/verify-memory-contract.py --report build/p1/original-version-regression.json
python3 scripts/create-p1-fixture.py
python3 scripts/verify-p1-library-ui.py
```

`create-p1-fixture.py` 生成公共配置与独立应用标识后，使用 `scripts/build-client.py --runtime-config <新根>/runtime-config.json --identifier <独立标识> --name MemlaneP1 --automation --skip-dmg --out build/p1-app` 构建。停止服务的验收根下次启动会保留停止状态，必须从设置明确启动。

## P1.3：实际 App 记忆管理闭环（2026-11-07）

保存、编辑、语义搜索、历史差异、恢复和索引重试均通过类型化私有 IPC；UI 不读数据库。保存使用稳定 operation ID 重试，编辑保存使用打开编辑器时的 revision，恢复使用进入栏内确认时的 revision。服务在单事务内保存正文/版本/任务并返回真实快照；索引稍后独立更新。服务不可用时草稿保留，过期编辑不会覆盖最新正文；选择以最新版本继续编辑是明确的栏内操作。

`build/p1/management-verification.json` 六组全部 passed，主流程由实际 MyGo 按钮与输入框完成：

- 在 App 新增合成项目记忆，v1 正文保存并在详情读取；所属 dataset 与当前登记项目一致。
- 在 App 编辑为 v2，搜索新内容并打开当前版本，原始出处保留。
- App 历史显示 v1/v2 和正文差异；进入恢复确认时仍为 v2，确认后生成 v3，v1/v2 内容不变。
- 真实退出/重开 App，读到恢复后的 v3 正文与三个历史版本；默认退出后服务继续运行。
- 用独立合成条目模拟外部编辑，旧编辑器保存被拒绝，最新正文仍为 v2；草稿保留。用户明确选择以最新版本继续编辑后，才保存新 v3。
- 从 App 主动停止服务，实际保存失败；草稿跨设置导航保留。启动服务后重试仅创建一个 v1 和一条新增活动。

主条目当前 v3 仍为恢复正文，历史顺序 [3,2,1]；实际活动总数 4（包括 P1.2 的一条合成知识及三个本阶段新增条目），连续编辑、恢复和重试没有重复计数。字段内容均为本轮合成夹具，保存在独立系统临时目录，未涉及真实 Agent。

实际系统截图 `build/p1-screenshots/system-management-restarted.png` 已打开检查；栏内确认截图 `management-confirm.png`、搜索、历史、冲突、服务中断、恢复等截图同目录。命令：

```sh
python3 scripts/verify-p1-management.py
sh scripts/go.sh test ./...
sh scripts/go.sh vet ./internal/desktop ./internal/store ./internal/daemon ./internal/serviceipc
```

首次主流程启动曾失败：组件已准备，App 65 秒等待后台服务超时，socket 不存在。保留 `build/p1/management-startup-failure.json`、`management-failed.png` 与 `startup-action.jsonl`。同包 helper 用同配置直接启动约 3 秒 ready；同 App 的独立 resume 入口返回 ready/enabled。重跑完整 App 主流程和其中的主动停止/启动均通过。没有据此宣称唯一根因，亦没有用直接 helper 代替 App 验收；正式后台注册的干净机器条件仍见 P0/P4。

## P1.4

尚待首页活动与四类真实状态的最终截图核对、完整 DMG 构建、开发文档收口和推送。
