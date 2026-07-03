// genicons.swift: アプリのアイコン一式を生成する(手描き PNG を持たずコードで管理する)。
//
//   swift build/genicons.swift        リポジトリルートで実行する
//
// 生成物:
//   internal/trayicon/icons/idle.png    メニューバー待機(吹き出し+流れの軌跡)
//   internal/trayicon/icons/listen.png  メニューバーリッスン中(吹き出し+音波)
//   build/AppIcon.icns                  Finder/配布用アプリアイコン(iconutil で合成)
//
// モチーフは「流れるコメントの吹き出し」で統一する(アプリの顔)。
import AppKit

// ---- 描画ヘルパー -----------------------------------------------------------

/// px 四方の透明キャンバスに draw を実行して PNG を書き出す。
/// draw は左下原点・引数 scale 倍で描く(1 倍座標で書けるように)。
func renderPNG(px: Int, to path: String, scale: CGFloat = 1, draw: () -> Void) {
    guard let rep = NSBitmapImageRep(
        bitmapDataPlanes: nil, pixelsWide: px, pixelsHigh: px,
        bitsPerSample: 8, samplesPerPixel: 4, hasAlpha: true, isPlanar: false,
        colorSpaceName: .deviceRGB, bytesPerRow: 0, bitsPerPixel: 0
    ) else { fatalError("bitmap rep 生成失敗") }
    NSGraphicsContext.saveGraphicsState()
    let ctx = NSGraphicsContext(bitmapImageRep: rep)!
    NSGraphicsContext.current = ctx
    ctx.cgContext.scaleBy(x: scale, y: scale)
    draw()
    NSGraphicsContext.restoreGraphicsState()
    try! rep.representation(using: .png, properties: [:])!
        .write(to: URL(fileURLWithPath: path))
    print("wrote \(path)")
}

func bar(_ x: CGFloat, _ y: CGFloat, _ w: CGFloat, _ h: CGFloat) -> NSBezierPath {
    NSBezierPath(roundedRect: NSRect(x: x, y: y, width: w, height: h), xRadius: h / 2, yRadius: h / 2)
}

// ---- メニューバー(44px・モノクロテンプレート) -------------------------------

/// 吹き出し本体(テキスト行は透明で抜く。テンプレートなのでいちばん濃い形で描き、
/// キャンバスいっぱい(縦 1..43)を使ってメニューバーで小さく見えないようにする)。
func drawBubble(cg: CGContext, x: CGFloat) {
    NSColor.black.setFill()
    let bubble = NSBezierPath(roundedRect: NSRect(x: x, y: 11, width: 32, height: 32), xRadius: 8, yRadius: 8)
    let tail = NSBezierPath()
    tail.move(to: NSPoint(x: x + 7, y: 13))
    tail.line(to: NSPoint(x: x + 3, y: 1))
    tail.line(to: NSPoint(x: x + 16, y: 12))
    tail.close()
    bubble.append(tail)
    bubble.fill()
    cg.setBlendMode(.clear)
    bar(x + 6, 31, 21, 4.8).fill()
    bar(x + 6, 20.5, 13, 4.8).fill()
    cg.setBlendMode(.normal)
}

/// idle: 吹き出し+右側に流れの軌跡(ニコニコ風に右→左へ流れるコメント)。
func drawIdle(cg: CGContext) {
    drawBubble(cg: cg, x: 0)
    NSColor.black.setFill()
    bar(35, 32.5, 9, 4.8).fill()
    bar(37, 22.5, 7, 4.8).fill()
}

/// listen: 吹き出し+右側に音波(聞き取り中)。
func drawListen(cg: CGContext) {
    drawBubble(cg: cg, x: 0)
    NSColor.black.setStroke()
    for r: CGFloat in [6.5, 12] {
        let arc = NSBezierPath()
        arc.appendArc(withCenter: NSPoint(x: 30, y: 27), radius: r,
                      startAngle: -52, endAngle: 52)
        arc.lineWidth = 3.8
        arc.lineCapStyle = .round
        arc.stroke()
    }
}

// ---- アプリアイコン(1024 座標で描き、各サイズへ縮小) ------------------------

func rgb(_ hex: UInt32, _ alpha: CGFloat = 1) -> NSColor {
    NSColor(srgbRed: CGFloat((hex >> 16) & 0xFF) / 255,
            green: CGFloat((hex >> 8) & 0xFF) / 255,
            blue: CGFloat(hex & 0xFF) / 255, alpha: alpha)
}

/// macOS 標準の角丸スクエア(1024 中 824、マージン 100)に、
/// 暗い画面+流れるコメントバー+白い吹き出し、を描く。
func drawAppIcon() {
    let plate = NSBezierPath(roundedRect: NSRect(x: 100, y: 100, width: 824, height: 824),
                             xRadius: 186, yRadius: 186)
    NSGradient(starting: rgb(0x2E2E3A), ending: rgb(0x14141A))!
        .draw(in: plate, angle: -90)

    // 以降は角丸の内側だけに描く(コメントバーが縁からはみ出さないように)。
    NSGraphicsContext.current!.cgContext.saveGState()
    plate.addClip()

    // 流れるコメント(ニコニコ風の配色。右端から左へ流れている途中の図)。
    rgb(0xFFCC00, 0.95).setFill(); bar(560, 760, 260, 58).fill()
    rgb(0x66CCFF, 0.95).setFill(); bar(660, 640, 264, 58).fill()
    rgb(0x99FF99, 0.90).setFill(); bar(700, 300, 224, 58).fill()
    rgb(0xFFFFFF, 0.85).setFill(); bar(620, 180, 240, 58).fill()

    // 主役の白い吹き出し(テキスト行は背景色で描く)。
    NSColor.white.setFill()
    let bubble = NSBezierPath(roundedRect: NSRect(x: 180, y: 360, width: 440, height: 340), xRadius: 96, yRadius: 96)
    let tail = NSBezierPath()
    tail.move(to: NSPoint(x: 300, y: 390))
    tail.line(to: NSPoint(x: 258, y: 240))
    tail.line(to: NSPoint(x: 430, y: 372))
    tail.close()
    bubble.append(tail)
    bubble.fill()
    rgb(0x1C1C26).setFill()
    bar(268, 560, 280, 62).fill()
    bar(268, 442, 190, 62).fill()

    NSGraphicsContext.current!.cgContext.restoreGState()
}

// ---- 生成 -------------------------------------------------------------------

let fm = FileManager.default
guard fm.fileExists(atPath: "internal/trayicon/icons") else {
    fatalError("リポジトリルートで実行してください: swift build/genicons.swift")
}

renderPNG(px: 44, to: "internal/trayicon/icons/idle.png") {
    drawIdle(cg: NSGraphicsContext.current!.cgContext)
}
renderPNG(px: 44, to: "internal/trayicon/icons/listen.png") {
    drawListen(cg: NSGraphicsContext.current!.cgContext)
}

// AppIcon.iconset → iconutil で AppIcon.icns に合成する。
let iconset = NSTemporaryDirectory() + "AppIcon.iconset"
try? fm.removeItem(atPath: iconset)
try! fm.createDirectory(atPath: iconset, withIntermediateDirectories: true)
let entries: [(String, Int)] = [
    ("icon_16x16", 16), ("icon_16x16@2x", 32),
    ("icon_32x32", 32), ("icon_32x32@2x", 64),
    ("icon_128x128", 128), ("icon_128x128@2x", 256),
    ("icon_256x256", 256), ("icon_256x256@2x", 512),
    ("icon_512x512", 512), ("icon_512x512@2x", 1024),
]
for (name, px) in entries {
    renderPNG(px: px, to: "\(iconset)/\(name).png", scale: CGFloat(px) / 1024) {
        drawAppIcon()
    }
}
let p = Process()
p.executableURL = URL(fileURLWithPath: "/usr/bin/iconutil")
p.arguments = ["-c", "icns", iconset, "-o", "build/AppIcon.icns"]
try! p.run()
p.waitUntilExit()
guard p.terminationStatus == 0 else { fatalError("iconutil 失敗") }
try? fm.removeItem(atPath: iconset)
print("wrote build/AppIcon.icns")
