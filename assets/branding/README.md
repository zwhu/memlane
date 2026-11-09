# Memlane 图标

2026-11-08 选定第 02 版「记忆年轮」，黑底白标。

- `memlane-mark.svg`：保留原始路径的单色标志，以 currentColor 着色。
- `memlane-icon.svg`：黑色圆角底、白色标志，外部透明。
- `memlane-icon.png`：1024 × 1024 像素；MyGo 打包自动生成多尺寸 ICNS。

在仓库根目录重新生成 PNG：

```sh
swift -module-cache-path .cache/swift-icon scripts/render-app-icon.swift
```

构建配置在 `mygo.json`；`scripts/build-client.py` 将资源路径解析到仓库根目录，确保隔离打包目录也能找到图标。
