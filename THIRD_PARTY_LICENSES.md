# 第三方开源组件与模型许可声明

Memlane 基于多个开源项目与预训练模型构建。本文件按核心直接依赖清单列出关键组件、版本、许可协议及上游来源；完整传递依赖版本分别锁定于 [`go.mod`](go.mod) 与 [`core/setup/requirements.lock`](core/setup/requirements.lock)。

## 1. Go 与原生桌面依赖（`go.mod`）

| 组件 | 锁定版本 | 许可协议 | 用途说明 |
| --- | --- | --- | --- |
| `github.com/egoist/mygo` | `v0.7.4` | MIT | macOS 原生桌面界面与窗口/托盘运行时框架（主界面纯自绘，图谱画布使用沙箱官方 WebView） |
| `github.com/ebitengine/purego` | `v0.11.1` | Apache-2.0 | 免 CGO 调用 macOS 原生系统框架 |
| `github.com/go-text/typesetting` | `v0.3.5` | Unlicense / MIT / BSD | 原生文本排版与字体塑形引擎 |
| `golang.org/x/image` | `v0.46.0` | BSD-3-Clause | 图像解码与绘制原语 |
| `golang.org/x/net` | `v0.63.0` | BSD-3-Clause | HTTP / 网络协议辅助库 |
| `golang.org/x/text` | `v0.44.0` | BSD-3-Clause | Unicode 文本规范化与编码支持 |

## 2. 首次安装引导工具与原生运行依赖（`engine/*.json`）

| 组件 | 锁定版本 | 许可协议 | 用途与来源说明 |
| --- | --- | --- | --- |
| `uv`（`astral-sh/uv`） | `0.12.3` | Apache-2.0 / MIT | 隔离虚拟环境创建与 `requirements.lock` 哈希强制同步工具 |
| `python-build-standalone` / CPython | `3.12.12`（`build 20260211`） | PSF-2.0 | 独立隔离运行的 Python 3.12 解释器 |
| OpenSSL（Homebrew bottle `arm64`） | `3.5.9` | Apache-2.0 | 运行库所需的独立密码学动态库 |

## 3. 本地记忆引擎与 Python 核心依赖（`engine/coding-memory-components.json` / `requirements.lock`）

| 组件 | 锁定版本 | 许可协议 | 用途说明 |
| --- | --- | --- | --- |
| `cognee`（上游固定源码提交 `ec6f408`） | `1.6.7` | Apache-2.0 | 核心知识图谱构建、记忆提炼与检索引擎 |
| `cognee-integrations`（上游固定提交 `a176248`） | `1.8.0` | Apache-2.0 | 官方宿主 Agent 接入插件基础源码（通过外置声明式适配与网关约束加载） |
| `lancedb` | `0.39.0` | Apache-2.0 | 嵌入式本地向量数据库 |
| `ladybug`（`real-ladybug`） | `0.19.0` | MIT | 嵌入式本地属性图数据库 |
| `fastembed` | `0.9.1` | Apache-2.0 | 本地文本向量编码接口 |
| `onnxruntime` | `1.31.1` | MIT | 本地 ONNX 推理运行时 |
| `fastapi` | `0.162.0` | MIT | 本地服务 HTTP API 框架 |
| `uvicorn` | `0.54.0` | BSD-3-Clause | ASGI 本地服务容器 |
| `aiohttp` | `3.14.5` | Apache-2.0 | 异步 HTTP 客户端与服务端传输 |
| `sqlalchemy` | `2.0.57` | MIT | 关系型元数据与状态持久化 ORM |
| `pydantic` | `2.14.3` | MIT | 数据结构校验与序列化 |
| `litellm` | `1.108.2` | MIT | 统一 LLM Provider 协议适配 |
| `tiktoken` | `0.14.0` | MIT | 文本 Token 计数工具 |

## 4. 本地向量模型（`engine/embedding-components.json`）

| 模型标识 | 来源仓库与提交 | 许可协议 | 说明 |
| --- | --- | --- | --- |
| `BAAI/bge-small-zh-v1.5`（512 维 ONNX） | `Qdrant/bge-small-zh-v1.5` (`46fbe35fd4374a00fee7de77dfddaeb6dd6a2c59`) & `BAAI/bge-small-zh-v1.5` (`7999e1d3359715c523056ef9478215996d62a620`) | MIT | 完全本地离线运行的中文嵌入向量模型与官方分词配置 |

## 5. 内嵌官方图谱前端依赖（`web/official-graph/`）

官方图谱局部资源随桌面端二进制嵌入（仅运行于无网络、无凭证的私有协议沙箱 WebView）。上游完整许可正文与全部传递前端依赖声明见 [`web/official-graph/dist/COGNEE-LICENSE`](web/official-graph/dist/COGNEE-LICENSE) 与 [`web/official-graph/dist/THIRD-PARTY-LICENSES.txt`](web/official-graph/dist/THIRD-PARTY-LICENSES.txt)，锁定版本见 [`web/official-graph/pnpm-lock.yaml`](web/official-graph/pnpm-lock.yaml)。

| 组件 | 锁定版本 | 许可协议 | 用途说明 |
| --- | --- | --- | --- |
| `cognee` 官方图谱前端模块（上游固定提交 `8853378eae5366addb064e758e560a456146ae1a`） | `8853378` | Apache-2.0 | 知识图谱 Canvas 渲染、力导向布局、命中测试与节点详情面板 |
| `react` / `react-dom` | `19.2.4` | MIT | 官方图谱局部前端组件运行时 |
| `@mantine/core` / `@mantine/hooks` | `8.3.14` | MIT | 官方图谱面板与交互控件基础组件库 |
| `d3`（含 `d3-array` `3.2.4`、`d3-color` `3.1.0`、`d3-force` `3.0.0`、`d3-polygon` `3.0.1`） | `7.9.0` | ISC | 图谱力导向模拟、缩放相机与几何计算 |
| `react-remove-scroll-bar`（传递依赖） | `2.3.8` | MIT | Mantine 浮层滚动锁定辅助（已随附上游 MIT 许可正文） |
| `use-composed-ref`（传递依赖） | `1.4.0` | MIT | Mantine Ref 组合辅助（已随附上游声明 MIT 许可正文） |
