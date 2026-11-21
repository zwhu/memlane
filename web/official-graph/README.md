# 官方图谱局部资源

复用 Cognee `8853378eae5366addb064e758e560a456146ae1a` 的官方 Canvas、模拟、相机、命中测试、节点／来源面板；`vendor/cognee` 的42份模块保持原字节，摘要与入口见 `upstream.json`，根许可随附。宿主适配只负责数据注入、当前范围、少量导航和选中回调，不引入整个 Next 登录／租户／治理页面。

React／Mantine／d3及类型版本沿用该提交的 `package-lock.json`；构建工具另行固定，实际解析见本目录 `pnpm-lock.yaml`。不影响根目录Go／Python依赖。

从仓库根执行：

```sh
python3 scripts/task.py -- pnpm --dir web/official-graph install --frozen-lockfile --ignore-scripts
python3 scripts/task.py -- pnpm --dir web/official-graph check
python3 scripts/task.py -- pnpm --dir web/official-graph build
```

`dist` 是受控纳入仓库的可再生局部静态资源，供干净检出直接通过 Go `//go:embed` 构建与核验，不是完整App包。构建校验固定官方源码摘要及实际导入范围，输出精简源／模块清单（`dist/build.json`）与第三方声明（`dist/THIRD-PARTY-LICENSES.txt`）。未随 npm 包附带许可正文的上游依赖（`@mantine/core@8.3.14`、`@mantine/hooks@8.3.14`、`react-remove-scroll-bar@2.3.8`、`use-composed-ref@1.4.0`）已按上游仓库与发布 `gitHead` 补齐并经 SHA-256 校验，见 `licenses/manifest.json`，`dist/build.json` 中 `missingLicenseTexts` 须保持为空（`[]`）。

## Native接入契约

- 页面没有fetch、后端地址、Cognee令牌、项目文件读取或索引入口；CSP拒绝网络连接、外部脚本和表单。只能由可信宿主供给已经过daemon授权的官方payload。
- 页面就绪调用 `mygo.call('OfficialGraph.Ready')`。宿主以 `Page.Eval` 调用 `window.memlaneOfficialGraph.apply({epoch,project,instance,dataset,payload})`；payload使用006的官方结构，不使用旧Memlane投影DTO。
- epoch由宿主单调递增，表示一次当前范围／数据发布。`apply`拒绝相同或较旧epoch；同范围刷新复用相机和仍存在的选中ID，换范围重建视图。权限失效、离开页面或切项目时先隐藏该WebView并调用 `clear(nextEpoch)`，同步卸载原画布／面板；迟到旧payload不能恢复旧图。
- 点击实体仅调用 `OfficialGraph.Select`，传当前epoch／project／instance／dataset／node ID。宿主必须核对可信CallerPage、当前epoch、该节点确实属于当前已发布payload，并重新确认项目／dataset权限后才解析来源导航；不能相信网页自填路径或将ID当文件／URL。
- Go 宿主已接入桌面端 [`internal/simpledesktop/graph.go`](../../internal/simpledesktop/graph.go) 与 [`runtime_assets.go`](../../runtime_assets.go)（`//go:embed` 随二进制内嵌并支持外部 `graph_assets` 目录），在项目详情页与图谱视图按需加载真实官方图谱数据。本目录不承担代码图谱授权门禁；知识／代码数据集范围、代码主动开启与索引仍由控制面与策略层独立控制，仅打开图谱页不扫描代码仓库。

宿主接法：桌面端通过 `RegisterGraph(绝对dist路径)` 或 `RegisterGraphFS(memlane.RuntimeAssets)`（延迟按需 `loadEmbeddedGraphHost()`）校验 `assets.json` 全部白名单资产的 SHA-256 摘要并读入只读内存，注册 `memlanegraph://graph/*` 私有静态协议与仅暴露 `OfficialGraph.Ready` / `OfficialGraph.Select` 的绑定门面。窗口就绪后通过 `App.AttachGraph(win)` 挂载沙箱 `mygo.WebView`；权限失效、切项目或身份变更时由宿主立即调用 `clear()` 并清空 `graphSelection`，防止旧图谱画布残留。

## 开发检查

```sh
python3 scripts/task.py -- node web/official-graph/verify.mjs /absolute/path/to/installed/chromium /absolute/private/output
sh scripts/go.sh test -run TestEmbeddedGraphAssetsVerifyAndLazyLoad ./internal/simpledesktop
```

使用已安装浏览器，不下载新浏览器。`verify.mjs` 在临时 loopback 静态服务器上使用合成图验证实际 Canvas 绘制、真实指针选中／Shift 路径、Records 切换、缩放窗口、清空和迟到数据拒绝及零外部请求；浏览器／服务器在 `finally` 关闭。Chromium 证据不替代 macOS WKWebView、真实官方 SDK 图、日常项目或 Native 整窗验收。
