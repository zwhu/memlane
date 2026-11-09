// 用 macOS AppKit 将选定 SVG 渲染为透明边缘的 1024px App 图标。
// 在仓库根目录运行：swift -module-cache-path .cache/swift-icon scripts/render-app-icon.swift
import AppKit

let input = "assets/branding/memlane-icon.svg"
let output = "assets/branding/memlane-icon.png"
guard let image = NSImage(contentsOfFile: input) else {
    fatalError("无法读取图标 SVG：\(input)")
}
let size = 1024
guard let bitmap = NSBitmapImageRep(
    bitmapDataPlanes: nil, pixelsWide: size, pixelsHigh: size,
    bitsPerSample: 8, samplesPerPixel: 4, hasAlpha: true,
    isPlanar: false, colorSpaceName: .deviceRGB, bytesPerRow: 0, bitsPerPixel: 0
), let context = NSGraphicsContext(bitmapImageRep: bitmap) else {
    fatalError("无法创建图标画布")
}
NSGraphicsContext.saveGraphicsState()
NSGraphicsContext.current = context
image.draw(in: NSRect(x: 0, y: 0, width: size, height: size),
           from: .zero, operation: .copy, fraction: 1)
NSGraphicsContext.restoreGraphicsState()
guard let png = bitmap.representation(using: .png, properties: [:]) else {
    fatalError("无法编码图标 PNG")
}
try png.write(to: URL(fileURLWithPath: output), options: .atomic)
print(output)
