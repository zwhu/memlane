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

## P1.2–P1.4

尚待实现和验收。真实保存、分页列表、版本冲突、搜索、首次可用活动与重启读取均不能由 P1.1 视觉夹具证明。
