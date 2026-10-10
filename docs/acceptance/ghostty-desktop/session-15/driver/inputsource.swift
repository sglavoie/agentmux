// The keyboard input source, for the input-method checks.
//   inputsource current          prints the selected source's ID
//   inputsource enabled          prints the enabled sources' IDs
//   inputsource select ID        selects ID, enabling it first if it is not
//                                enabled; prints "was-enabled" or "newly-enabled"
//   inputsource disable ID       removes ID from the enabled sources
import Carbon
import Foundation

func id(_ source: TISInputSource) -> String {
    guard let p = TISGetInputSourceProperty(source, kTISPropertyInputSourceID) else { return "" }
    return Unmanaged<CFString>.fromOpaque(p).takeUnretainedValue() as String
}
func flag(_ source: TISInputSource, _ key: CFString) -> Bool {
    guard let p = TISGetInputSourceProperty(source, key) else { return false }
    return CFBooleanGetValue(Unmanaged<CFBoolean>.fromOpaque(p).takeUnretainedValue())
}
func sources(all: Bool) -> [TISInputSource] {
    (TISCreateInputSourceList(nil, all).takeRetainedValue() as? [TISInputSource]) ?? []
}
func find(_ wanted: String) -> TISInputSource? { sources(all: true).first { id($0) == wanted } }

let args = Array(CommandLine.arguments.dropFirst())
switch args.first {
case "current":
    print(id(TISCopyCurrentKeyboardInputSource().takeRetainedValue()))
case "enabled":
    for s in sources(all: false) where flag(s, kTISPropertyInputSourceIsSelectCapable) { print(id(s)) }
case "select":
    guard args.count == 2, let source = find(args[1]) else { print("unknown"); exit(1) }
    let was = flag(source, kTISPropertyInputSourceIsEnabled)
    if !was, TISEnableInputSource(source) != noErr { print("cannot-enable"); exit(1) }
    if TISSelectInputSource(source) != noErr { print("cannot-select"); exit(1) }
    print(was ? "was-enabled" : "newly-enabled")
case "disable":
    guard args.count == 2, let source = find(args[1]) else { print("unknown"); exit(1) }
    exit(TISDisableInputSource(source) == noErr ? 0 : 1)
default:
    FileHandle.standardError.write("usage: inputsource current|enabled|select ID|disable ID\n".data(using: .utf8)!)
    exit(2)
}
