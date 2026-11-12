# 官方图谱局部资源

复用 Cognee `8853378eae5366addb064e758e560a456146ae1a` 的官方 Canvas、模拟、相机、命中测试、节点／来源面板；`vendor/cognee` 的42份模块保持原字节，摘要与入口见 `upstream.json`，根许可随附。宿主适配只负责数据注入、当前范围、少量导航和选中回调，不引入整个 Next 登录／租户／治理页面。

React／Mantine／d3及类型版本沿用该提交的 `package-lock.json`；构建工具另行固定，实际解析见本目录 `pnpm-lock.yaml`。不影响根目录Go／Python依赖。

从仓库根执行：

```sh
python3 scripts/task.py -- pnpm --dir web/official-graph install --frozen-lockfile --ignore-scripts
python3 scripts/task.py -- pnpm --dir web/official-graph check
python3 scripts/task.py -- pnpm --dir web/official-graph build
```

`dist` 是可再生的局部静态资源，不是完整App包。构建校验固定官方源码摘要及实际导入范围，输出源／模块清单和第三方声明。Mantine缺失于npm包的许可文件已按发布gitHead补取，见 `licenses/manifest.json`；`react-remove-scroll-bar@2.3.8` 与 `use-composed-ref@1.4.0` npm包未附许可正文，保留MIT声明／仓库来源，列于 `dist/build.json` 的 `missingLicenseTexts`，正式分发前须处理，不能将开发资源构建描述为分发许可核对完成。

## Native接入契约

- 页面没有fetch、后端地址、Cognee令牌、项目文件读取或索引入口；CSP拒绝网络连接、外部脚本和表单。只能由可信宿主供给已经过daemon授权的官方payload。
- 页面就绪调用 `mygo.call('OfficialGraph.Ready')`。宿主以 `Page.Eval` 调用 `window.memlaneOfficialGraph.apply({epoch,project,instance,dataset,payload})`；payload使用006的官方结构，不使用旧Memlane投影DTO。
- epoch由宿主单调递增，表示一次当前范围／数据发布。`apply`拒绝相同或较旧epoch；同范围刷新复用相机和仍存在的选中ID，换范围重建视图。权限失效、离开页面或切项目时先隐藏该WebView并调用 `clear(nextEpoch)`，同步卸载原画布／面板；迟到旧payload不能恢复旧图。
- 点击实体仅调用 `OfficialGraph.Select`，传当前epoch／project／instance／dataset／node ID。宿主必须核对可信CallerPage、当前epoch、该节点确实属于当前已发布payload，并重新确认项目／dataset权限后才解析来源导航；不能相信网页自填路径或将ID当文件／URL。
- Go宿主已实现于 `internal/officialgraph`，实际MyGo／WKWebView合成来源验证通过；**尚未接入现有项目页或真实官方图数据**。不能把本目录作为代码图谱授权门禁。知识／代码数据集范围、代码主动开启和索引仍按规格单独实现；仅打开此页不扫描。

宿主接法：启动前 `LoadBundle(绝对dist路径)` 校验 `assets.json` 所列资源并读入只读内存，再 `Register(bundle)` 注册私有静态协议与仅有Ready／Select的facade。窗口就绪后 `registry.Attach(window, serviceipc.Library{Root: 服务根}, onSelect)` 创建一次WebView；现有页面的原图区域调用 `host.View(c)`，范围变化后调用 `host.Load(ctx, query)`，离开该区域调用 `host.Clear()`，关闭时 `host.Close()`（窗口关闭也会自动清理）。不要把整个Host注册成网页服务。

Load的context只控制本次读取；成功显示后的权限观察由宿主拥有，直到Clear／Close／下一次Load。读取、发布前后、选中时核对同一binding；可见期间每秒经daemon探测客户端授权与官方读取ACL，失败即隐藏并清空。旧加载、选中和页面更新均受epoch约束，第一次发布没有收到页面接受回执时不能显示成功。该探测不读取图／材料，不调用模型／索引；要求包含008新端点的组件，已准备的v13不具备它。

## 开发检查

```sh
python3 scripts/task.py -- node web/official-graph/verify.mjs /absolute/path/to/installed/chromium /absolute/private/output
```

使用已安装浏览器，不下载新浏览器。验证在临时loopback静态服务器上使用合成图，包含实际Canvas绘制、真实指针选中／Shift路径、Records切换、缩放窗口、清空和迟到数据拒绝及零外部请求；浏览器／服务器在finally关闭。Chromium证据不替代macOS WKWebView、真实官方SDK图、日常项目或Native整窗验收。

独立Native开发验证（单一调试可执行文件，不构建完整App包、不触碰现有后台）：

```sh
CGO_ENABLED=1 sh scripts/go.sh build -tags memlane_graph_e2e -o build/memlane-graph-dev ./cmd/memlane-graph-dev
python3 scripts/task.py -- build/memlane-graph-dev --synthetic-only --assets /absolute/dist --out /fresh/private/evidence
```

输出须为尚不存在的目录。工具实际加载WKWebView，验证可信Go回调／未知节点拒绝／撤权清空，保存网页与本进程系统整窗截图后退出；其中绑定回调通过网页JS发起，不冒称操作系统鼠标／键盘／菜单验收。只承载合成Source，不安装helper、不打开Cognee或数据库。
