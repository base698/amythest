// Built by the installer. No browser or developer tools are needed at runtime.
import AppKit

struct Drawing: Decodable {
    struct File: Decodable { let token: String; let path: String }
    var svg: String
    let files: [File]
}

func fail(_ message: String) -> NSError {
    NSError(domain: "canvas-png", code: 1, userInfo: [NSLocalizedDescriptionKey: message])
}

func main() throws {
    let args = Array(CommandLine.arguments.dropFirst())
    guard let input = args.first else { throw fail("Give a drawing JSON file") }
    // Optional named pasteboard lets regression checks avoid the user's clipboard.
    guard args.count == 1 || (args.count == 3 && args[1] == "--pasteboard") else {
        throw fail("Usage: copy-png drawing.json [--pasteboard NAME]")
    }
    let url = URL(fileURLWithPath: input)
    defer { try? FileManager.default.removeItem(at: url) }
    var drawing = try JSONDecoder().decode(Drawing.self, from: Data(contentsOf: url))
    for file in drawing.files {
        guard let image = NSImage(contentsOfFile: file.path), let tiff = image.tiffRepresentation,
              let bitmap = NSBitmapImageRep(data: tiff), let png = bitmap.representation(using: .png, properties: [:]) else {
            throw fail("Could not read an image in the drawing")
        }
        drawing.svg = drawing.svg.replacingOccurrences(of: "href=\"\(file.token)\"", with: "href=\"data:image/png;base64,\(png.base64EncodedString())\"")
    }
    guard let image = NSImage(data: Data(drawing.svg.utf8)), image.size.width > 0, image.size.height > 0 else {
        throw fail("Could not render the drawing")
    }
    // Retina detail for ordinary drawings, with a bounded allocation for large ones.
    let scale = min(2, 8192 / max(image.size.width, image.size.height), sqrt(16_000_000 / (image.size.width * image.size.height)))
    let width = max(1, Int(ceil(image.size.width * scale))), height = max(1, Int(ceil(image.size.height * scale)))
    guard let bitmap = NSBitmapImageRep(bitmapDataPlanes: nil, pixelsWide: width, pixelsHigh: height,
                                       bitsPerSample: 8, samplesPerPixel: 4, hasAlpha: true, isPlanar: false,
                                       colorSpaceName: .deviceRGB, bytesPerRow: 0, bitsPerPixel: 0),
          let context = NSGraphicsContext(bitmapImageRep: bitmap) else { throw fail("Could not allocate PNG") }
    NSGraphicsContext.saveGraphicsState()
    NSGraphicsContext.current = context
    image.draw(in: NSRect(x: 0, y: 0, width: width, height: height))
    NSGraphicsContext.restoreGraphicsState()
    guard let png = bitmap.representation(using: .png, properties: [:]) else { throw fail("Could not encode PNG") }
    let board = args.count == 3 ? NSPasteboard(name: NSPasteboard.Name(args[2])) : NSPasteboard.general
    board.clearContents()
    guard board.setData(png, forType: .png) else { throw fail("Could not write PNG to the clipboard") }
    print("PNG copied (\(width) × \(height))")
}

do { try main() } catch {
    FileHandle.standardError.write(Data((error.localizedDescription + "\n").utf8))
    exit(1)
}
