// A real left click at a screen point (top-left origin), posted as the
// hardware would: mouse moved, down, up. usage: click X Y
import CoreGraphics
import Foundation

let args = CommandLine.arguments
guard args.count == 3, let x = Double(args[1]), let y = Double(args[2]) else {
    FileHandle.standardError.write("usage: click X Y\n".data(using: .utf8)!)
    exit(2)
}
let point = CGPoint(x: x, y: y)
let source = CGEventSource(stateID: .hidSystemState)
for (type, pause) in [(CGEventType.mouseMoved, 0.05), (.leftMouseDown, 0.05), (.leftMouseUp, 0.05)] {
    guard let event = CGEvent(mouseEventSource: source, mouseType: type, mouseCursorPosition: point, mouseButton: .left) else { exit(1) }
    event.post(tap: .cghidEventTap)
    Thread.sleep(forTimeInterval: pause)
}
