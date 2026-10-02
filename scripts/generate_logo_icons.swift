// Run from the repository root: swift scripts/generate_logo_icons.swift
// Packages the approved artwork at platform icon sizes without changing it.
import AppKit
import Foundation
import ImageIO
import UniformTypeIdentifiers

let root = URL(fileURLWithPath: FileManager.default.currentDirectoryPath)
let source = root.appendingPathComponent("app/assets/branding/logo.png")
guard let logo = NSImage(contentsOf: source) else { fatalError("Missing logo: \(source.path)") }

func png(size: Int, scale: CGFloat = 0.94, opaque: Bool = true) -> Data {
    let alpha: CGImageAlphaInfo = opaque ? .noneSkipLast : .premultipliedLast
    let context = CGContext(data: nil, width: size, height: size, bitsPerComponent: 8,
                            bytesPerRow: size * 4, space: CGColorSpace(name: CGColorSpace.sRGB)!,
                            bitmapInfo: alpha.rawValue)!
    if opaque {
        context.setFillColor(CGColor(red: 26.0 / 255, green: 29.0 / 255, blue: 33.0 / 255, alpha: 1))
        context.fill(CGRect(x: 0, y: 0, width: size, height: size))
    }
    context.interpolationQuality = .high
    let edge = CGFloat(size) * scale
    let inset = (CGFloat(size) - edge) / 2
    context.draw(logo.cgImage(forProposedRect: nil, context: nil, hints: nil)!,
                 in: CGRect(x: inset, y: inset, width: edge, height: edge))
    let data = NSMutableData()
    let destination = CGImageDestinationCreateWithData(data, UTType.png.identifier as CFString, 1, nil)!
    CGImageDestinationAddImage(destination, context.makeImage()!, nil)
    precondition(CGImageDestinationFinalize(destination), "PNG encoding failed")
    return data as Data
}

func write(_ data: Data, _ path: String) throws {
    let url = root.appendingPathComponent(path)
    try FileManager.default.createDirectory(at: url.deletingLastPathComponent(), withIntermediateDirectories: true)
    try data.write(to: url)
}

for (density, size) in [("mdpi", 48), ("hdpi", 72), ("xhdpi", 96), ("xxhdpi", 144), ("xxxhdpi", 192)] {
    try write(png(size: size), "app/android/app/src/main/res/mipmap-\(density)/ic_launcher.png")
}
try write(png(size: 432, scale: 0.74, opaque: false), "app/android/app/src/main/res/drawable/ic_launcher_foreground.png")

for platform in ["ios", "macos"] {
    let directory = "app/\(platform)/Runner/Assets.xcassets/AppIcon.appiconset"
    let contents = try Data(contentsOf: root.appendingPathComponent("\(directory)/Contents.json"))
    let manifest = try JSONSerialization.jsonObject(with: contents) as! [String: Any]
    for entry in manifest["images"] as! [[String: Any]] {
        guard let filename = entry["filename"] as? String else { continue }
        let points = Double((entry["size"] as! String).components(separatedBy: "x")[0])!
        let multiplier = Double((entry["scale"] as! String).replacingOccurrences(of: "x", with: ""))!
        try write(png(size: Int((points * multiplier).rounded())), "\(directory)/\(filename)")
    }
}
try write(png(size: 32), "app/web/favicon.png")
for size in [192, 512] {
    try write(png(size: size), "app/web/icons/Icon-\(size).png")
    try write(png(size: size, scale: 0.74), "app/web/icons/Icon-maskable-\(size).png")
}

// Windows ICO: directory entries followed by PNG payloads at each resolution.
func littleEndian<T: FixedWidthInteger>(_ value: T) -> Data {
    var value = value.littleEndian
    return withUnsafeBytes(of: &value) { Data($0) }
}
let sizes = [16, 32, 48, 64, 128, 256]
let payloads = sizes.map { png(size: $0) }
var ico = Data([0, 0, 1, 0])
ico.append(littleEndian(UInt16(sizes.count)))
var offset = 6 + sizes.count * 16
for (size, data) in zip(sizes, payloads) {
    let dimension = size == 256 ? UInt8(0) : UInt8(size)
    ico.append(contentsOf: [dimension, dimension, 0, 0])
    ico.append(littleEndian(UInt16(1)))
    ico.append(littleEndian(UInt16(32)))
    ico.append(littleEndian(UInt32(data.count)))
    ico.append(littleEndian(UInt32(offset)))
    offset += data.count
}
for data in payloads { ico.append(data) }
try write(ico, "app/windows/runner/resources/app_icon.ico")
print("Generated launcher icons for Android, iOS, macOS, Windows, and web.")
