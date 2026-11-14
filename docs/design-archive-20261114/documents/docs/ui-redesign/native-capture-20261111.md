# 当前实窗内容截图导出

为最终交付保留可查看的实际截图，验证构建新增显式菜单动作，普通生产构建不包含此菜单或AppKit导出代码。

- `--automation`仅对验证UI启用`e2e`和CGO，helper/CLI继续纯Go；`CGO_ENABLED=1 sh scripts/go.sh test -tags=e2e ./cmd/memlane-app`已通过编译。
- 设置`MEMLANE_NATIVE_CAPTURE_DIR`并要求其上级有`synthetic_only=true`的`source-info.json`；缺任一条件不显示菜单。
- 只能在当前可见窗口，通过菜单“文件 → 保存当前窗口验证截图”显式操作。函数读取当前NSWindow的既有contentView，通过AppKit bitmap cache写独立PNG与边界/方法回执；不调用ui.Tester、fixture或CapturePage的重新构造渲染。
- 输出是当前AppKit窗口内容，不包含系统窗口阴影/装饰；不能代替系统截图、键盘、滚动或真实回执验收。实际导出后须用CUA当前截图与本地PNG交叉检查，空白/缺层/尺寸错误不记为完成。
- `prepare-native-launch.py --app-name <name> --reuse-pi --capture-native`仅在现有标记的临时验证根准备独立副本，复用隔离pi，不写用户Agent配置。原配置hash另存用于前后核对。

前序CUA截图仍在工具回执。系统截图快捷键和Screenshot.app尝试未取得本地PNG，失败边界保留；新增导出尚待004真实菜单操作验证，未提前声称已有截图文件。

## 004实际失败与下一批修正

004菜单确实导出2400×1640 PNG和1200×820窗口边界回执，但实际打开文件只有灰色背景；contentView bitmap未包含GPU内容，因此该PNG不作为视觉验收证据。失败记录保存在004的`native/capture-verification.json`，上文contentView方案只代表已失败的第一版。

下一批代码改为仅请求本进程当前NSWindow编号的CoreGraphics图像，不枚举其他窗口、桌面或显示器。采用动态符号查找以兼容SDK，缺符号或图像即报失败，不降级为夹具。Apple在[WWDC2019窗口捕获说明](https://developer.apple.com/videos/play/wwdc2019/701/)中说明自有窗口的捕获边界。修正后CGO/e2e定向编译35724通过；尚未进入新完整包，也尚未取得有效实际PNG，不能把编译通过当截图通过。

随后当前004进程仍存活，但CUA对App与Finder的窗口动作均返回`cgWindowNotFound`，App库存及进程检查仍确认004运行。没有据此重启服务、结束组件所有者或重复启动真实任务；继续源代码集中实现，待窗口接口恢复后补实际核验。
