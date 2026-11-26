# Memlane × Cognee 3D / 2D 记忆流转图解

本目录提供面向非技术与跨职能读者的**离线交互式动画讲解页**，同源提供两套 60fps 连续时间轴版本，且均支持**浅色工作室/插画风**与**深空科技发光风**一键实时切换，并可在右上角一键互切（自动保留当前风格与播放时间点）：

- **3D WebGL 版（[index.html](index.html)）**：基于离线 Three.js `r149`，以立体工坊、3D 翻转分词积木、立体 Ladybug 图剧场与透明玻璃 LanceDB 向量星图呈现空间感。
- **2D Canvas 版（[2d.html](2d.html)）**：零 WebGL / 零 Three.js 依赖，纯 High-DPI 2D `<canvas>` 绘制 Kurzgesagt 风格 2.5D 厚边插画、横向 `scaleX(cos θ)` 翻牌分词、视差向量星图与同心雷达波，弱 GPU 或远程桌面也能满帧运行。

设计方法、双风格光影配方及 2D / 3D 工程取舍已沉淀至 [技术原理动画制作经验与 2D / 3D 工程指南](../../design/technical-explainer-animation.md)。

## 打开方式

零外部网络依赖，两种方式均可直接打开：

```sh
# 方式 1：浏览器直接离线打开
open docs/architecture/memory-flow-3d/index.html   # 3D WebGL 版（默认浅色黏土风）
open docs/architecture/memory-flow-3d/2d.html      # 2D Canvas 版（零 WebGL，默认浅色插画风）

# 直接以深空科技发光风打开（URL 参数或锚点均可）
# file:///.../docs/architecture/memory-flow-3d/index.html?theme=space
# file:///.../docs/architecture/memory-flow-3d/2d.html?theme=space

# 方式 2：本地静态服务打开
python3 -m http.server 18796 --bind 127.0.0.1
# 3D 版：http://127.0.0.1:18796/docs/architecture/memory-flow-3d/
# 2D 版：http://127.0.0.1:18796/docs/architecture/memory-flow-3d/2d.html
```

支持 URL 锚点直达指定风格、时间或章节，例如 `#theme=space&t=41.8`（深空分词翻牌特写）或 `#c=7`（大模型抽图）。

## 交互控制

- **2D / 3D 一键互切**：点右上角「🎨 切到 2D 版」或「🧊 切到 3D 版」，携带当前主题与播放进度无缝跳转对比。
- **双风格实时切换**：点右上角「🌌 深空科技 / ☀️ 浅色黏土（插画）」在浅色与深空两种美术风格间无损切换。
- **连续视频进度条**：支持播放/暂停（`Space`）、上一幕/下一幕（`←` / `→`）、`0.75× / 1× / 1.5×` 倍速，以及按住进度条任意拖动快进或倒放。
- **自由观察视角**：3D 版左键拖拽旋转 + 滚轮缩放，2D 版左键拖拽平移画布 + 滚轮缩放；点右上角「视角复位」或按 `R` 回到导演机位。
- **源码对应开关**：右上角开启「源码对应」后，字幕下方同步展示对应的 Memlane / Cognee 1.6.7 实现入口。

## 12 幕内容概览

| 阶段 | 幕次 | 演示内容 |
| --- | --- | --- |
| **开场** | 00 全景导览 | 俯瞰 Codex、Memlane 邮筒、Cognee 加工厂、悬浮 GPT 大模型（Memlane 代理）及三座数据库（SQLite / Ladybug / LanceDB） |
| **记下来** | 01–02 对话与寄信排队 | Codex 对话打字机呈现 → 折成 3D 纸飞机飞入 Memlane 邮筒排队（本机 SQLite 收件箱，毫秒级放行） |
| **记下来** | 03 原话存档 | 对话砖滑上 Cognee 传送带，激光扫描原样存档进关系数据库 SQLite |
| **记下来** | 04 大模型提炼 | Memlane 代理调用悬浮 3D「GPT 大模型」在提炼炉产出橙色「经验晶体」；副本沿光轨飞入 SQLite 登记，主晶体继续前进 |
| **记下来** | 05 切段（Chunking） | 黄铜闸刀按标点与长度规则将长对话切为独立小段（程序规则切，不调 AI） |
| **记下来** | 06 分词（Tokenization） | 微距特写：中文裂开成彩色 3D 词元积木 `[清理][旧][文件][误删][程序]`，空中翻转 180° 露出词表数字 ID |
| **记下来** | 07 大模型抽图 | 切好的段落送进上方「GPT 大模型（Memlane 代理）」，读出实体绿球与动词关系飞入 Ladybug 3D 图数据库连成关系网 |
| **记下来** | 08 向量入库 | 分词积木与经验晶体汇入蓝色向量透镜，沿 XYZ 坐标射入 LanceDB 3D 玻璃星图，按语义聚簇落位 |
| **想起来** | 09–11 零大模型召回 | 新提问换种说法 → Memlane 绿盾限定项目范围 → 同款向量模型编码（GPT 舱保持休眠，全程 0 次大模型）→ 3D 雷达球波在 LanceDB 命中近邻并联动 Ladybug / SQLite → 光带送回 Codex 作答 |

## 与工程实现的口径说明

- **存入两条线的合并展示**：Cognee 1.6.7 `improve()` 真实执行顺序为阶段 2 `persist_session_qa` 先对原话做切分/抽图，阶段 5 `distill_sessions` 再由大模型提炼经验条目并同样走切分/抽图；动画为避免重复演示两条相同流水线，将「提炼炉」前置并让原话砖与经验晶体并排前进。
- **召回路径说明**：后半段展示的是 Cognee 官方混合检索的完整原理（`HYBRID_COMPLETION + only_context=True`，零 LLM 生成回答，同时联合 LanceDB 向量与 Ladybug 图关系）；Memlane 当前 `/recall` 封装（[engine_api.py](../../../engines/cognee/engine_api.py)）为追求亚秒级响应默认使用 `SearchType.CHUNKS`（查向量段落并附带图库冲突状态）。

## 目录文件与第三方资源说明

| 文件 | 说明 |
| --- | --- |
| [index.html](index.html) | 单页 3D WebGL 场景、双风格切换、连续时间轴与播放器逻辑 |
| [2d.html](2d.html) | 单页纯 2D High-DPI `<canvas>` 2.5D 厚边插画场景（零 WebGL 依赖，同源 12 幕与双风格） |
| `vendor/three.min.js` | Three.js `r149`（MIT License），仅用于 3D 版离线渲染 |
| `vendor/icons.js` | 内嵌产品图标资产（Memlane、Codex、OpenAI、Cognee、SQLite、Ladybug、LanceDB），供 3D / 2D 共用 |
