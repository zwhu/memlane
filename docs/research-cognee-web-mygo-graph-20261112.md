# Cognee 官方图谱与 MyGo 复用调研

日期：2026-11-12。范围：源码与官方上游只读调研，不启动 Web 服务、不运行索引或模型、不改用户配置、历史数据或 UI 文件。当前用户决定优先使用 Cognee 官方现成图谱，保留其样式与交互，不继续自绘 Go 图，不做性能或 Token 测试。

## 结论

**最新官方 Cognee Web 已有明确的代码节点展示支持，不只是通用知识实体图。可优先复用官方图谱页面内容，由 MyGo WebView 承载。** 不必把图谱重写成 Go 绘图。整套 Next 应用包含租户、认证、跨数据集入口及治理操作，不宜直接原封不动放进项目页；最薄适配是保留官方画布、工具栏和所需详情面板，固定当前项目的数据集范围，接入 Memlane 的只读数据与选中事件桥。

源码证明“有此能力”，尚未证明当前运行环境与该版本的代码图谱已直接兼容；尤其不能把 GitHub connector 的覆盖范围等同于所有 Enola 节点均已覆盖。下述缺口是数据适配与集成工作，不是重新开发 renderer 的理由。

## 版本与证据边界

- 本地 Cognee：`/Users/bytedance/work/AI/cognee`，HEAD `e93a4f0c76e6af27142c25189f14c200e1749d51`，提交日期 `2026-09-06T04:56:17-07:00`。开始检查时仅有未跟踪 `COGNEE_NOTES.md`、`docs/research/`，未修改它们。
- 官方远端只读查询：main `8853378eae5366addb064e758e560a456146ae1a`，dev `5ea4f65ebe06cfd5b99e93a3c79b3b6b8730e52e`。本文最新 Web 结论固定到 main SHA，不代表本地旧 checkout 已拥有这些变更。
- SDK 草案固定 `ec6f40859e5f2df9e7862dde27d580ac3268ed43`；该版本 Enola 映射另行读取核对，不将 main UI 与草案 SDK 混为一个已验证组合。
- 本次未查看真实 Web 窗口、未构建前端、未验证 WebView 中的交互。官方测试文件仅作实现意图证据，未执行。

## 官方 Web 的真实入口与代码节点

当前 `/knowledge-graph` 路由直接渲染 `BusinessPage`，后者通过租户实例进入 `BusinessView`；它不是旧 `LegacyMindmapPage` 的 HTML iframe。入口见[固定版本路由](https://github.com/topoteretes/cognee/blob/8853378eae5366addb064e758e560a456146ae1a/cognee-frontend/src/app/%28app%29/knowledge-graph/page.tsx#L1-L7)、[BusinessPage](https://github.com/topoteretes/cognee/blob/8853378eae5366addb064e758e560a456146ae1a/cognee-frontend/src/app/%28app%29/business/BusinessPage.tsx#L1-L22)。本地旧 checkout 也能在 `cognee-frontend/src/app/(app)/knowledge-graph/page.tsx:3`、`:6` 看到这条路由，但不能据此推断新归一化模块已存在。

官方 [`codeGraph.ts`](https://github.com/topoteretes/cognee/blob/8853378eae5366addb064e758e560a456146ae1a/cognee-frontend/src/modules/business/codeGraph.ts#L1-L108) 明确识别十类代码节点：`CodeSymbol`、`ExternalDependency`、`CodeFileReference`、`CodeTestReference`、`CodeModule`、`ApiEndpoint`、`CodeInsight`、`CodeRepository`、`CodeExtractionAccount`、`StorageResource`。其中：

- `normalizeCodeNodes` 将后端暂归 `other` 的代码节点提升为画布 `entity`，保留节点标识与来源；`sourceFields`/`repositoryOf` 使用已有 node set 或仓库归属。
- `codeDisplayName` 缩短函数等限定名称供标签展示，保留文件名扩展；原始名称仍用于标识和搜索。
- 文件注释说明该映射来自 GitHub connector 真实数据集；不是声称覆盖任意代码图 schema。

[`computeBrainState.ts:241-273`](https://github.com/topoteretes/cognee/blob/8853378eae5366addb064e758e560a456146ae1a/cognee-frontend/src/modules/business/computeBrainState.ts#L241-L273) 确实调用这层归一化，并将实体间合格的关系交给画布。官方还有[专门映射测试](https://github.com/topoteretes/cognee/blob/8853378eae5366addb064e758e560a456146ae1a/cognee-frontend/src/modules/business/__tests__/codeGraph.test.ts)。因此可确认最新 Web 在同一图谱体验中支持知识实体与上述代码节点；不能进一步声称它已经是完整 IDE 调用图或能显示所有运行时关系。

## 与 Enola 代码图谱是否相容

main 的 [`extract_code_graph.py:51-65`](https://github.com/topoteretes/cognee/blob/8853378eae5366addb064e758e560a456146ae1a/cognee/tasks/code_graph/extract_code_graph.py#L51-L65) 与[草案 SDK 同一位置](https://github.com/topoteretes/cognee/blob/ec6f40859e5f2df9e7862dde27d580ac3268ed43/cognee/tasks/code_graph/extract_code_graph.py#L51-L65) 均将 Enola `symbol` 映射成 `CodeSymbol`、`module` 映射成 `CodeModule`、`file_ref` 映射成 `CodeFileReference`，还有 `route`→`ApiEndpoint`、`test_ref`→`CodeTestReference` 等。因此主要类别与 Web 并非完全不同的 schema。

但映射还产生 `CodeService`、`CodeFlow`、`CodeIntent`、`CodeAssociation`、`CodeLintFinding`，这五类不在上述 Web 十类集合中。若它们经过可视化预处理仍为 `other`，现有归一化不会把它们提升为实体。最薄适配需明确补齐类别或接受官方当前覆盖范围，并检查关系是否保留。不能直接承诺所有 Enola 产物零适配显示。

节点源模型本身保留 `file_path`、`line`、`end_line`、`repo`、`symbol_kind` 等字段，见[代码图谱模型](https://github.com/topoteretes/cognee/blob/8853378eae5366addb064e758e560a456146ae1a/cognee/tasks/code_graph/models.py#L1-L140)。是否在官方详情面板全部展示，仍需逐面板核对；不能把模型字段存在当成点击打开代码已实现。

## 复用边界：保留官方绘图与交互，适配数据和宿主

官方完整 [`BusinessView`](https://github.com/topoteretes/cognee/blob/8853378eae5366addb064e758e560a456146ae1a/cognee-frontend/src/app/%28app%29/business/BusinessView.tsx) 组合画布、节点详情、来源、类型、Brain 切换、操作与实时事件。为了尽快保留官方现成体验，优先裁掉宿主导航与无关治理入口，保留需要的官方图谱组件及样式，而不是只拿算法重做界面。

较清晰的组件边界是 [`BusinessGraphCanvas`](https://github.com/topoteretes/cognee/blob/8853378eae5366addb064e758e560a456146ae1a/cognee-frontend/src/modules/business/canvas/BusinessGraphCanvas.tsx)，其内部 [`BusinessCanvas`](https://github.com/topoteretes/cognee/blob/8853378eae5366addb064e758e560a456146ae1a/cognee-frontend/src/modules/business/canvas/BusinessCanvas.tsx) 接收 `BrainState`、颜色模式、选择状态、聚焦集合及事件回调。它使用 Canvas 2D，配套 camera、hit-test、draw、minimap、tooltip、source layer、scene types、颜色编码和状态计算模块；这不是复制单个 TSX 即可运行的独立 npm 组件。

已直接检查的 `BusinessGraphCanvas`、`BusinessCanvas`、[`simulation/useBusinessSimulation`](https://github.com/topoteretes/cognee/blob/8853378eae5366addb064e758e560a456146ae1a/cognee-frontend/src/modules/business/simulation/useBusinessSimulation.ts) 核心使用 React、DOM/Canvas、d3 camera 和 `d3-force`，没有直接的 API fetch、Next 路由依赖、WASM 或 Worker 初始化。`BusinessCanvas` 的 `"use client"` 是 Next 客户端声明，不是必须运行 Next server 的证据。此结论限所读核心，尚未完成全部依赖闭包审计。源码中还保留旧 `react-force-graph-2d` 页面；不能因为 package.json 有该依赖就认定当前官方画布使用它。

完整前端采用 Next 16.3.8、React 19、Mantine 8、React Query 5、d3 7，见[固定 package.json](https://github.com/topoteretes/cognee/blob/8853378eae5366addb064e758e560a456146ae1a/cognee-frontend/package.json)。若提取局部页面，可沿用官方 React 组件并预构建静态资源；是否能完全移除 Next、需要多少共享 UI 依赖，必须以实际 imports/build 结果确认。不要把“可提取”写成“现成独立组件包已存在”。

本地对应核心可定位 `cognee-frontend/src/modules/business/canvas/BusinessCanvas.tsx:258` 的 `getContext("2d")` 与 `:431` 的 RAF；但引入时应固定新版本完整相关组件，不能把新 `codeGraph.ts` 随意拼到旧 UI 后直接宣布兼容。

## 数据接口、项目范围与认证

官方画布不是只吃任意 `{nodes, edges}`。其 [`VisualizationPayload`](https://github.com/topoteretes/cognee/blob/8853378eae5366addb064e758e560a456146ae1a/cognee-frontend/src/modules/business/types.ts) 使用 `nodes`、`links`、`color_maps`；节点还带 `stage`、`importance`、来源、类型等，边带 `relation` 与 `edge_class`。`computeBrainState` 会筛选实体端点与语义边；未经分类的原始边可能不可见。[后端预处理器](https://github.com/topoteretes/cognee/blob/8853378eae5366addb064e758e560a456146ae1a/cognee/modules/visualization/preprocessor.py#L55-L71) 的 stage 表也说明了为什么需要前端代码节点提升。

[`getBrainGraph.ts`](https://github.com/topoteretes/cognee/blob/8853378eae5366addb064e758e560a456146ae1a/cognee-frontend/src/modules/business/getBrainGraph.ts) 按 `dataset_id` 请求 `/v1/visualize/json`，新版本默认 `max_nodes=1000`，支持 SSE 与 JSON 回退。本地旧文件 `:15` 为 500，不能混记。`brains-summary` 只给概览，不包含完整节点边。代码搜索结果、原始 dataset graph DTO 与可视化 payload 也不能不经适配互换。

整页查询还涉及 Brain 概览、治理索引、可读数据集、实时事件与详情补取，不能仅隐藏 BrainSwitcher 就视为已保证项目隔离。最小嵌入契约应由宿主固定当前项目允许的数据集；所有读取与详情回调继承该范围，项目切换/权限撤销时清空旧数据并取消旧请求。代码索引只由用户在该项目主动开启，打开图谱本身不触发索引。

[`localFetch.ts`](https://github.com/topoteretes/cognee/blob/8853378eae5366addb064e758e560a456146ae1a/cognee-frontend/src/modules/instances/localFetch.ts) 按 `NEXT_PUBLIC_LOCAL_API_URL`（默认 localhost:8000）请求，添加 `X-Api-Key`，带 cookie credentials，把 `/v1` 重写成 `/api/v1`，认证失败走统一错误处理。这些是完整 Web 的运行环境假设。MyGo 嵌入不宜直接复制认证和租户系统；可让现有 Go/daemon 权限与请求适配向局部官方页面供数，保留后端凭据的现有边界。当前尚未验证 WebView 跨源、SSE、认证状态与本地资源加载。

## 许可与仍需验证的最小项

Cognee 此固定版本根 [LICENSE](https://github.com/topoteretes/cognee/blob/8853378eae5366addb064e758e560a456146ae1a/LICENSE) 为 Apache-2.0；提取代码时应保留相应版权、许可和 NOTICE，并记录修改。第三方依赖、字体和品牌素材需按实际引入范围核对；本次没有做完整依赖许可审计。

进入实现后只需围绕功能补齐验收：官方组件静态构建可嵌入；当前项目真实知识图可显示并选中详情；启用代码图谱后的实际 CodeSymbol/CodeModule/文件与关系可见；未启用项目不会触发索引；切换项目不会串数据。保留官方样式交互，暂不开展性能优化与美化，也不运行 Token 消耗测试。

## MyGo 承载评估

Memlane 的 `go.mod` 已固定 MyGo v0.7.4。该版本[官方 Web view 文档](https://github.com/egoist/mygo/blob/v0.7.4/docs/ui/web-view.md)和 [`webview.go`](https://github.com/egoist/mygo/blob/v0.7.4/webview.go) 明确支持：在 `WindowOptions.Content` 的原生窗口中通过 `Window.NewWebView` 创建系统网页视图，再以 `ui.WebView` 放入局部区域；macOS 使用系统 WKWebView。技术上无需为此先升级 MyGo，也不必把整个 App 换成网页窗口。

WebView 页面可加载本地打包的前端资源，使用 Go 绑定和页面事件交换数据、选中和导航。现有 Memlane 是无前端构建目录的原生 App，`mygo.json` 尚未配置 `frontendDist`；实施需要接入官方 React 组件的静态资源构建／加载以及薄桥。React/Canvas 组件不能直接放入 Go 的 `ui.Painter`，采用局部 WebView 才能保留官方绘图代码。

已核对本地锁版本源码：`.local/gopath/pkg/mod/github.com/egoist/mygo@v0.7.4/webview.go:10`、`ui/webview.go:16`、`docs/ui/web-view.md:194`。WebView 应创建一次并复用；隐藏时页面仍保留，项目切换或撤权需要宿主同步清除旧图与拒绝旧范围回调，不能只把控件藏起来。官方页面不需要自行持有 Cognee 账号／密钥；现有 Go/daemon 只给它当前项目允许的可视化数据和受控详情读取能力。

MyGo 也有 Canvas/Painter，Memlane 当前 `internal/desktop/browsing.go:679` 起已用它自绘实体和材料关系图。但现有实现最多选取 12 个实体、16 条材料，属于简单局部布局，不能把这一画布当成官方代码图谱已经复用。用户已经决定放弃该自绘路线，本期不追加拖拽布局、缩放绘制等原生重写。

功能验收应包括实际系统窗口、图内点击、菜单覆盖、窗口缩放和切项目。MyGo 文档明确指出 `Window.CapturePage` 不包含嵌入网页像素，需使用 WebView 自身截图并配合系统整窗证据，不能拿原生截图的空洞当图已呈现。此处只是验收方法要求，本轮没有构建或运行 WebView，不做性能测试。

## 本轮采用决定与最小接入

用户已明确要求放弃 Memlane 自绘图，直接使用 Cognee 官方现成图、先有功能。采用：**现有 MyGo 原生项目页 + 局部 WebView 内官方图谱内容 + 现有 Go/daemon 数据与权限适配。** 保留官方画布及相关面板的样式交互；剥离与宿主冲突的登录／全局导航／跨项目入口，不重新实现布局算法或渲染器。

最小实施内容：固定官方前端代码与配套 SDK → 静态打包所需官方图谱组件 → 当前项目按 dataset 供数 → 节点／来源事件回到 Memlane → 实测知识实体及已开启的代码图。未开启项目不索引，不因浏览官方图改变用户的开启规则。仍需验证实际版本组合和类别覆盖，不把源码能力写成日常环境已交付。相应要求已纳入 [接入规格](coding-memory-codex-spec.md)。
