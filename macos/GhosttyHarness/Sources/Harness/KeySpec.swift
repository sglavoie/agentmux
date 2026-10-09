import AppKit

/// One physical key press, parsed from a driver spec such as `shift+enter`,
/// `ctrl+c`, `alt+left`, or `x`, carrying what a US keyboard's NSEvent would.
struct KeySpec {
    let keyCode: UInt16
    let flags: NSEvent.ModifierFlags
    let characters: String
    let charactersIgnoringModifiers: String

    /// ANSI-layout virtual key codes and the unmodified character each types.
    private static let keys: [String: (code: UInt16, char: String)] = {
        var table: [String: (UInt16, String)] = [
            "enter": (36, "\r"), "tab": (48, "\t"), "space": (49, " "),
            "backspace": (51, "\u{7f}"), "escape": (53, "\u{1b}"),
            "left": (123, fn(NSLeftArrowFunctionKey)), "right": (124, fn(NSRightArrowFunctionKey)),
            "down": (125, fn(NSDownArrowFunctionKey)), "up": (126, fn(NSUpArrowFunctionKey)),
            "pageup": (116, fn(NSPageUpFunctionKey)), "pagedown": (121, fn(NSPageDownFunctionKey)),
            "home": (115, fn(NSHomeFunctionKey)), "end": (119, fn(NSEndFunctionKey)),
        ]
        let letters: [Character: UInt16] = [
            "a": 0, "s": 1, "d": 2, "f": 3, "h": 4, "g": 5, "z": 6, "x": 7, "c": 8, "v": 9,
            "b": 11, "q": 12, "w": 13, "e": 14, "r": 15, "y": 16, "t": 17, "o": 31, "u": 32,
            "i": 34, "p": 35, "l": 37, "j": 38, "k": 40, "n": 45, "m": 46,
            "1": 18, "2": 19, "3": 20, "4": 21, "6": 22, "5": 23, "9": 25, "7": 26, "8": 28, "0": 29,
            "-": 27, "/": 44, ".": 47, ",": 43, "[": 33, "]": 30,
        ]
        for (char, code) in letters {
            table[String(char)] = (code, String(char))
        }
        return table
    }()

    private static func fn(_ key: Int) -> String {
        String(Character(UnicodeScalar(UInt16(key))!))
    }

    init?(_ spec: String) {
        var parts = spec.lowercased().split(separator: "+").map(String.init)
        guard let name = parts.popLast(), let key = Self.keys[name] else { return nil }
        var flags: NSEvent.ModifierFlags = []
        for mod in parts {
            switch mod {
            case "shift": flags.insert(.shift)
            case "ctrl": flags.insert(.control)
            case "alt", "opt": flags.insert(.option)
            case "cmd": flags.insert(.command)
            default: return nil
            }
        }
        if Self.isFunctionKey(key.char) {
            flags.insert([.function, .numericPad])
        }
        self.init(code: key.code, char: key.char, flags: flags)
    }

    /// The key that types `character` unmodified or with Shift, or nil when
    /// the US layout has no single key for it.
    init?(character: Character) {
        let lower = Character(character.lowercased())
        guard let key = Self.keys[String(lower)] ?? (character == " " ? Self.keys["space"] : nil) else { return nil }
        let flags: NSEvent.ModifierFlags = lower != character ? [.shift] : []
        self.init(code: key.code, char: key.char, flags: flags)
    }

    private init(code: UInt16, char: String, flags: NSEvent.ModifierFlags) {
        keyCode = code
        self.flags = flags
        var typed = char
        if flags.contains(.shift), char.count == 1, char.first!.isLetter {
            typed = char.uppercased()
        }
        if flags.contains(.control), char.count == 1, let scalar = char.unicodeScalars.first,
           scalar.isASCII, char.first!.isLetter || char == "[" || char == "]" {
            // Ctrl turns a letter into its C0 control character, as AppKit reports it.
            typed = String(UnicodeScalar(UInt8(scalar.value) & 0x1f))
        }
        characters = typed
        charactersIgnoringModifiers = flags.contains(.shift) ? typed.uppercased() : char
    }

    private static func isFunctionKey(_ char: String) -> Bool {
        guard let scalar = char.unicodeScalars.first else { return false }
        return scalar.value >= 0xF700 && scalar.value <= 0xF8FF
    }
}
