# Memlane 图标

2026-11-21 选定「融合 H1 · 暖褐满幅探头左歪抱石」海獭品牌体系：

- `memlane-icon.png`：1024 × 1024 透明边缘 macOS 超椭圆主图标（深炭磨砂底 + 哑光软陶海獭 + 怀中发光 `#7C86E8` 记忆卵石）；MyGo 打包及 `scripts/dev.py` 自动生成多尺寸 `AppIcon.icns`。
- `internal/simpledesktop/brand_icons/sidebar_brand.png` (`D1`)：64 × 64 透明去底抠图版「融合 H1」，用于主窗口左上角侧栏（`20×20`）与托盘弹层顶栏（`18×18`）。
- `internal/simpledesktop/brand_icons/tray_*.png` (`H1-C`)：5 套 32 × 32 (`@2x` Retina) 清瘦歪头海獭剪影（正常态为纯黑模板图，提醒/异常态保持本体纯色并在右下角带透明隔离环琥珀/红状态圆点）。
- `memlane-mark.svg`：单色海獭矢量参考标志（`currentColor` 着色）。

构建配置在 `mygo.json`；`scripts/build-client.py` 与 `scripts/dev.py` 均从 `memlane-icon.png` 生成应用包图标。
