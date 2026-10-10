// Real pointer input at screen points (top-left origin), posted at the HID
// tap as the hardware would.
//   mouse click X Y [mods]        left click
//   mouse dclick X Y              double click
//   mouse rclick X Y              right click
//   mouse drag X1 Y1 X2 Y2 [mods] [STEPS]  press, move in STEPS moves, release
//   mouse scroll X Y LINES        wheel; positive scrolls up (toward history)
//   mouse move X Y [mods]         move only
//   mouse state                   prints the modifiers the system reports held
// Every action ends by releasing what it pressed and posting an unflagged
// move, then exits 9 if the system still reports a modifier held.
// mods: a comma list of cmd, shift, opt, ctrl.
import CoreGraphics
import Foundation

func fail(_ message: String) -> Never {
    FileHandle.standardError.write((message + "\n").data(using: .utf8)!)
    exit(2)
}

let args = Array(CommandLine.arguments.dropFirst())
guard let verb = args.first else { fail("usage: mouse click|dclick|rclick|drag|scroll|move ...") }
func num(_ i: Int) -> Double {
    guard i < args.count, let v = Double(args[i]) else { fail("missing number at argument \(i)") }
    return v
}
func mods(_ i: Int) -> CGEventFlags {
    guard i < args.count else { return [] }
    var flags: CGEventFlags = []
    for name in args[i].split(separator: ",") {
        switch name {
        case "cmd": flags.insert(.maskCommand)
        case "shift": flags.insert(.maskShift)
        case "opt": flags.insert(.maskAlternate)
        case "ctrl": flags.insert(.maskControl)
        default: fail("unknown modifier \(name)")
        }
    }
    return flags
}

// A private source: the modifier flags these events carry are theirs alone.
// Posted from the HID-system source, one Shift-flagged mouse event makes the
// whole machine believe Shift is held until something clears it.
let source = CGEventSource(stateID: .privateState)

/// The modifier keys the system believes are held, by name.
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

/// Where the pointer is now, for the closing unflagged event.
func pointerLocation() -> CGPoint { CGEvent(source: nil)?.location ?? .zero }

/// Leaves nothing held: releases the buttons this tool may have pressed and
/// posts one unflagged move where the pointer already is.
func neutralize() {
    let here = pointerLocation()
    for (type, button) in [(CGEventType.leftMouseUp, CGMouseButton.left), (.rightMouseUp, .right)] where pressed.contains(button.rawValue) {
        if let event = CGEvent(mouseEventSource: source, mouseType: type, mouseCursorPosition: here, mouseButton: button) {
            event.flags = []
            event.post(tap: .cghidEventTap)
        }
    }
    pressed.removeAll()
    if let event = CGEvent(mouseEventSource: source, mouseType: .mouseMoved, mouseCursorPosition: here, mouseButton: .left) {
        event.flags = []
        event.post(tap: .cghidEventTap)
    }
    Thread.sleep(forTimeInterval: 0.05)
}

/// Buttons currently down by this tool's doing.
var pressed: Set<UInt32> = []

// Killed midway, the tool still lets go of what it holds.
for sig in [SIGINT, SIGTERM, SIGHUP] {
    signal(sig) { _ in
        neutralize()
        _exit(130)
    }
}

/// Every run ends here: nothing held by the tool, and the machine's own
/// modifier state checked. A modifier still reported held is an error.
func finish() -> Never {
    neutralize()
    let held = heldModifiers()
    if !held.isEmpty {
        FileHandle.standardError.write("mouse: the system reports \(held.joined(separator: "+")) held after this action\n".data(using: .utf8)!)
        exit(9)
    }
    exit(0)
}

func post(_ type: CGEventType, _ point: CGPoint, button: CGMouseButton = .left, flags: CGEventFlags = [], clicks: Int64 = 1) {
    guard let event = CGEvent(mouseEventSource: source, mouseType: type, mouseCursorPosition: point, mouseButton: button) else { exit(1) }
    event.flags = flags
    event.setIntegerValueField(.mouseEventClickState, value: clicks)
    switch type {
    case .leftMouseDown, .rightMouseDown: pressed.insert(button.rawValue)
    case .leftMouseUp, .rightMouseUp: pressed.remove(button.rawValue)
    default: break
    }
    event.post(tap: .cghidEventTap)
    Thread.sleep(forTimeInterval: 0.05)
}

switch verb {
case "click":
    let p = CGPoint(x: num(1), y: num(2)), f = mods(3)
    post(.mouseMoved, p, flags: f)
    Thread.sleep(forTimeInterval: 0.15)
    post(.leftMouseDown, p, flags: f)
    post(.leftMouseUp, p, flags: f)
case "dclick":
    let p = CGPoint(x: num(1), y: num(2))
    post(.mouseMoved, p)
    post(.leftMouseDown, p, clicks: 1)
    post(.leftMouseUp, p, clicks: 1)
    post(.leftMouseDown, p, clicks: 2)
    post(.leftMouseUp, p, clicks: 2)
case "rclick":
    let p = CGPoint(x: num(1), y: num(2))
    post(.mouseMoved, p)
    post(.rightMouseDown, p, button: .right)
    post(.rightMouseUp, p, button: .right)
case "drag":
    let a = CGPoint(x: num(1), y: num(2)), b = CGPoint(x: num(3), y: num(4)), f = mods(5)
    post(.mouseMoved, a, flags: f)
    Thread.sleep(forTimeInterval: 0.15)
    post(.leftMouseDown, a, flags: f)
    Thread.sleep(forTimeInterval: 0.1)
    // A hand moves the pointer a point or two at a time; few coarse jumps
    // would put the first move a whole cell away from the press.
    let steps = args.count > 6 ? max(1, Int(args[6]) ?? 60) : 60
    for i in 1...steps {
        let t = Double(i) / Double(steps)
        post(.leftMouseDragged, CGPoint(x: a.x + (b.x - a.x) * t, y: a.y + (b.y - a.y) * t), flags: f)
    }
    Thread.sleep(forTimeInterval: 0.15)
    post(.leftMouseUp, b, flags: f)
case "scroll":
    let p = CGPoint(x: num(1), y: num(2))
    post(.mouseMoved, p)
    let lines = Int32(num(3))
    let step: Int32 = lines > 0 ? 1 : -1
    for _ in 0..<abs(lines) {
        guard let event = CGEvent(scrollWheelEvent2Source: source, units: .line, wheelCount: 1, wheel1: step, wheel2: 0, wheel3: 0) else { exit(1) }
        event.location = p
        event.post(tap: .cghidEventTap)
        Thread.sleep(forTimeInterval: 0.03)
    }
case "move":
    post(.mouseMoved, CGPoint(x: num(1), y: num(2)), flags: mods(3))
case "state":
    // Only reports: prints what the system believes is held.
    let held = heldModifiers()
    print(held.isEmpty ? "none" : held.joined(separator: "+"))
    exit(held.isEmpty ? 0 : 9)
default:
    fail("unknown verb \(verb)")
}
finish()
