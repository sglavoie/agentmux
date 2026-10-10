// Real HID-level input on the MacBook, for the three things its screen
// sharing cannot deliver from this driver: wheel turns, keys that must pass
// through an input method, and a global shortcut. Run there over SSH; needs
// the Accessibility grant described in README.md.
//
//   hid scroll X Y LINES          wheel at a screen point (points, top-left
//                                 origin); positive scrolls toward history
//   hid keys CODE[:MODS] ...      key presses by virtual key code, MODS a
//                                 comma list of cmd, shift, opt, ctrl
//   hid state                     prints the modifiers the system reports held
//
// Events come from a private event source, so the flags they carry are
// theirs alone: from the HID-system source, one flagged event leaves that
// modifier held for the whole machine. Every run ends by checking that the
// system reports no modifier held, and exits 9 otherwise.
import CoreGraphics
import Foundation

func fail(_ message: String) -> Never {
    FileHandle.standardError.write((message + "\n").data(using: .utf8)!)
    exit(2)
}

let source = CGEventSource(stateID: .privateState)

func heldModifiers() -> [String] {
    var held: Set<String> = []
    for state in [CGEventSourceStateID.hidSystemState, .combinedSessionState] {
        let flags = CGEventSource.flagsState(state)
        if flags.contains(.maskShift) { held.insert("shift") }
        if flags.contains(.maskControl) { held.insert("control") }
        if flags.contains(.maskAlternate) { held.insert("option") }
        if flags.contains(.maskCommand) { held.insert("command") }
    }
    return held.sorted()
}

func flags(_ text: Substring?) -> CGEventFlags {
    var out: CGEventFlags = []
    for name in (text ?? "").split(separator: ",") {
        switch name {
        case "cmd": out.insert(.maskCommand)
        case "shift": out.insert(.maskShift)
        case "opt": out.insert(.maskAlternate)
        case "ctrl": out.insert(.maskControl)
        default: fail("unknown modifier \(name)")
        }
    }
    return out
}

let args = Array(CommandLine.arguments.dropFirst())
switch args.first {
case "scroll":
    guard args.count == 4, let x = Double(args[1]), let y = Double(args[2]), let lines = Int32(args[3]) else {
        fail("usage: hid scroll X Y LINES")
    }
    let point = CGPoint(x: x, y: y)
    if let move = CGEvent(mouseEventSource: source, mouseType: .mouseMoved, mouseCursorPosition: point, mouseButton: .left) {
        move.post(tap: .cghidEventTap)
    }
    Thread.sleep(forTimeInterval: 0.2)
    let step: Int32 = lines > 0 ? 1 : -1
    for _ in 0..<abs(lines) {
        guard let event = CGEvent(scrollWheelEvent2Source: source, units: .line, wheelCount: 1, wheel1: step, wheel2: 0, wheel3: 0) else { exit(1) }
        event.location = point
        event.post(tap: .cghidEventTap)
        Thread.sleep(forTimeInterval: 0.04)
    }
case "keys":
    for word in args.dropFirst() {
        let parts = word.split(separator: ":", maxSplits: 1)
        guard let code = UInt16(parts[0]) else { fail("not a key code: \(word)") }
        let held = flags(parts.count > 1 ? parts[1] : nil)
        for down in [true, false] {
            guard let event = CGEvent(keyboardEventSource: source, virtualKey: code, keyDown: down) else { exit(1) }
            event.flags = held
            event.post(tap: .cghidEventTap)
            Thread.sleep(forTimeInterval: 0.06)
        }
        Thread.sleep(forTimeInterval: 0.12)
    }
case "state":
    break
default:
    fail("usage: hid scroll X Y LINES | keys CODE[:MODS]... | state")
}
Thread.sleep(forTimeInterval: 0.1)
let held = heldModifiers()
print(held.isEmpty ? "none" : held.joined(separator: "+"))
exit(held.isEmpty ? 0 : 9)
