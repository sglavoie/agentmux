#!/usr/bin/env swift
//
// Renders Agent Mux.app's icons into macos/AgentMux/Resources/Assets.xcassets:
//
//   AppIcon.appiconset     the full-color Finder icon, every mac size
//   StatusIcon.imageset    the monochrome menu-bar template image
//
// The PNGs are committed, so this runs only when the artwork changes:
//
//   swift macos/scripts/make-icons.swift
//
// Everything is drawn from code (no source image), on the macOS icon grid: an
// 824-point rounded square centered in a 1024 canvas. The glyph is a stack of
// three tabs, one of them lit, over `amx`: the app watches many agent sessions
// and points at the one that needs you. Sizes of 32 pixels and under keep only
// the tabs, which is what still reads at that scale.

import AppKit
import CoreText

let scriptURL = URL(fileURLWithPath: CommandLine.arguments[0]).standardizedFileURL
let macosDir = scriptURL.deletingLastPathComponent().deletingLastPathComponent()
let assets = macosDir.appendingPathComponent("AgentMux/Resources/Assets.xcassets")

// MARK: - Drawing helpers

func rgb(_ hex: UInt32, _ alpha: CGFloat = 1) -> CGColor {
    CGColor(
        srgbRed: CGFloat((hex >> 16) & 0xFF) / 255,
        green: CGFloat((hex >> 8) & 0xFF) / 255,
        blue: CGFloat(hex & 0xFF) / 255,
        alpha: alpha)
}

func bitmap(_ pixels: Int, _ draw: (CGContext) -> Void) -> Data {
    let space = CGColorSpace(name: CGColorSpace.sRGB)!
    let ctx = CGContext(
        data: nil, width: pixels, height: pixels, bitsPerComponent: 8, bytesPerRow: 0,
        space: space, bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue)!
    ctx.interpolationQuality = .high
    ctx.setShouldAntialias(true)
    draw(ctx)
    let rep = NSBitmapImageRep(cgImage: ctx.makeImage()!)
    return rep.representation(using: .png, properties: [:])!
}

/// A monospaced line of text as a path, so it can be filled, measured, and
/// centered without a text layout pass.
func textPath(_ text: String, size: CGFloat, weight: NSFont.Weight) -> CGPath {
    let font = NSFont.monospacedSystemFont(ofSize: size, weight: weight) as CTFont
    let path = CGMutablePath()
    var x: CGFloat = 0
    for scalar in text.unicodeScalars {
        var char = UniChar(scalar.value)
        var glyph = CGGlyph()
        CTFontGetGlyphsForCharacters(font, &char, &glyph, 1)
        if let outline = CTFontCreatePathForGlyph(font, glyph, nil) {
            path.addPath(outline, transform: CGAffineTransform(translationX: x, y: 0))
        }
        var advance = CGSize.zero
        CTFontGetAdvancesForGlyphs(font, .horizontal, &glyph, &advance, 1)
        x += advance.width
    }
    return path
}

func centered(_ path: CGPath, in rect: CGRect, dx: CGFloat = 0, dy: CGFloat = 0) -> CGPath {
    let box = path.boundingBoxOfPath
    var move = CGAffineTransform(
        translationX: rect.midX - box.midX + dx, y: rect.midY - box.midY + dy)
    return path.copy(using: &move)!
}

// MARK: - App icon

/// Draws the app icon on a 1024 canvas scaled to `pixels`.
func appIcon(pixels: Int) -> Data {
    bitmap(pixels) { ctx in
        let scale = CGFloat(pixels) / 1024
        ctx.scaleBy(x: scale, y: scale)
        let detailed = pixels > 32

        // Tile: 824 on the 1024 grid, corner radius 185, with a soft drop shadow.
        let tile = CGRect(x: 100, y: 100, width: 824, height: 824)
        let shape = CGPath(roundedRect: tile, cornerWidth: 185, cornerHeight: 185, transform: nil)
        ctx.saveGState()
        ctx.setShadow(offset: CGSize(width: 0, height: -10), blur: 28, color: rgb(0x000000, 0.35))
        ctx.addPath(shape)
        ctx.setFillColor(rgb(0x1B2030))
        ctx.fillPath()
        ctx.restoreGState()

        ctx.saveGState()
        ctx.addPath(shape)
        ctx.clip()
        let gradient = CGGradient(
            colorsSpace: CGColorSpace(name: CGColorSpace.sRGB),
            colors: [rgb(0x3A4460), rgb(0x1B2030)] as CFArray, locations: [0, 1])!
        ctx.drawLinearGradient(
            gradient, start: CGPoint(x: 512, y: 924), end: CGPoint(x: 512, y: 100), options: [])
        // A faint title-bar band with three window dots, read as "terminal".
        if detailed {
            ctx.setFillColor(rgb(0xFFFFFF, 0.06))
            ctx.fill(CGRect(x: 100, y: 800, width: 824, height: 124))
            for (i, color) in [0xFF6B6B, 0xFFC857, 0x7FD8A6].enumerated() {
                ctx.setFillColor(rgb(UInt32(color), 0.9))
                ctx.fillEllipse(in: CGRect(x: 186 + CGFloat(i) * 62, y: 843, width: 38, height: 38))
            }
        }
        ctx.restoreGState()

        // Hairline edge so the tile holds on light and dark backgrounds.
        ctx.addPath(shape)
        ctx.setStrokeColor(rgb(0xFFFFFF, 0.10))
        ctx.setLineWidth(4)
        ctx.strokePath()

        // Three session tabs, the middle one lit as "needs attention".
        let tabs: [(CGFloat, UInt32, CGFloat)] = detailed
            ? [(600, 0x7F8BAA, 0.55), (470, 0xFFC857, 1), (340, 0x7F8BAA, 0.55)]
            : [(640, 0x7F8BAA, 0.7), (450, 0xFFC857, 1), (260, 0x7F8BAA, 0.7)]
        let tabHeight: CGFloat = detailed ? 96 : 150
        for (y, color, alpha) in tabs {
            let bar = CGRect(x: 220, y: y, width: 584, height: tabHeight)
            ctx.addPath(CGPath(roundedRect: bar, cornerWidth: tabHeight / 2.4, cornerHeight: tabHeight / 2.4, transform: nil))
            ctx.setFillColor(rgb(color, alpha * 0.35))
            ctx.fillPath()
            ctx.setFillColor(rgb(color, alpha))
            let dot = tabHeight * 0.42
            ctx.fillEllipse(in: CGRect(x: bar.minX + tabHeight * 0.36, y: bar.midY - dot / 2, width: dot, height: dot))
        }
        if detailed {
            let word = centered(
                textPath("amx", size: 150, weight: .bold),
                in: CGRect(x: 100, y: 150, width: 824, height: 150))
            ctx.addPath(word)
            ctx.setFillColor(rgb(0xA6E3B8))
            ctx.fillPath()
        }
    }
}

// MARK: - Status icon

/// The menu-bar template: three stacked tabs, the middle one filled, black on
/// clear. AppKit tints it for light, dark, and highlighted menu bars.
func statusIcon(pixels: Int) -> Data {
    bitmap(pixels) { ctx in
        let scale = CGFloat(pixels) / 18
        ctx.scaleBy(x: scale, y: scale)
        ctx.setStrokeColor(rgb(0x000000))
        ctx.setFillColor(rgb(0x000000))
        ctx.setLineWidth(1.25)
        for (index, y) in [12.5, 7.5, 2.5].enumerated() {
            let bar = CGRect(x: 2, y: CGFloat(y), width: 14, height: 3.5)
            let path = CGPath(roundedRect: bar, cornerWidth: 1.5, cornerHeight: 1.5, transform: nil)
            ctx.addPath(path)
            if index == 1 {
                ctx.fillPath()
            } else {
                ctx.strokePath()
            }
        }
    }
}

// MARK: - Output

func write(_ data: Data, _ url: URL) {
    try! data.write(to: url, options: .atomic)
}

func writeJSON(_ object: Any, _ url: URL) {
    let data = try! JSONSerialization.data(
        withJSONObject: object, options: [.prettyPrinted, .sortedKeys])
    write(data + Data("\n".utf8), url)
}

let fm = FileManager.default

let appIconDir = assets.appendingPathComponent("AppIcon.appiconset")
try! fm.createDirectory(at: appIconDir, withIntermediateDirectories: true)
var appImages: [[String: String]] = []
for points in [16, 32, 128, 256, 512] {
    for scale in [1, 2] {
        let name = scale == 1 ? "icon_\(points)x\(points).png" : "icon_\(points)x\(points)@2x.png"
        write(appIcon(pixels: points * scale), appIconDir.appendingPathComponent(name))
        appImages.append([
            "filename": name, "idiom": "mac", "scale": "\(scale)x", "size": "\(points)x\(points)",
        ])
    }
}
writeJSON(
    ["images": appImages, "info": ["author": "xcode", "version": 1]],
    appIconDir.appendingPathComponent("Contents.json"))

let statusDir = assets.appendingPathComponent("StatusIcon.imageset")
try! fm.createDirectory(at: statusDir, withIntermediateDirectories: true)
var statusImages: [[String: String]] = []
for scale in [1, 2] {
    let name = scale == 1 ? "status_18.png" : "status_18@2x.png"
    write(statusIcon(pixels: 18 * scale), statusDir.appendingPathComponent(name))
    statusImages.append(["filename": name, "idiom": "mac", "scale": "\(scale)x"])
}
writeJSON(
    [
        "images": statusImages, "info": ["author": "xcode", "version": 1],
        "properties": ["template-rendering-intent": "template"],
    ],
    statusDir.appendingPathComponent("Contents.json"))

print("Wrote \(appIconDir.path) and \(statusDir.path)")
