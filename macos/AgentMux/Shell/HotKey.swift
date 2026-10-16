import AppKit
import Carbon
import os

/// One shortcut: a virtual key code and Carbon modifier bits, as
/// `RegisterEventHotKey` takes them. Its stored spelling is lowercase tokens
/// joined by `+`, modifiers first in the order macOS displays them, e.g.
/// `ctrl+opt+cmd+t`.
struct HotKeyCombo: Equatable, Hashable {
    enum Scope { case global, application }
    let keyCode: UInt32
    /// Carbon bits: `controlKey`, `optionKey`, `shiftKey`, `cmdKey`.
    let modifiers: UInt32

    static let modifierMask = UInt32(controlKey | optionKey | shiftKey | cmdKey)

    /// Global shortcuts use stricter rules than application menu equivalents.
    init?(keyCode: UInt32, modifiers: UInt32, scope: Scope = .global) {
        guard Self.refusal(keyCode: keyCode, modifiers: modifiers, scope: scope) == nil else { return nil }
        self.keyCode = keyCode
        self.modifiers = modifiers & Self.modifierMask
    }

    /// Why a key cannot be a global shortcut, in the recorder's words; nil
    /// when it can. A global shortcut is taken from every app, so it may not
    /// swallow ordinary typing (it needs Control, Option, or Command, unless
    /// the key is a function key), may not be Command alone (with or without
    /// Shift, that is every app's own menu keys), and may not be a bare F1 to
    /// F11, which the workspace binds.
    static func refusal(keyCode: UInt32, modifiers: UInt32, scope: Scope = .global) -> String? {
        guard keys[keyCode] != nil else { return "That key cannot be a shortcut" }
        let chord = modifiers & UInt32(controlKey | optionKey | cmdKey)
        if scope == .application {
            return chord != 0 || isFunctionKey(keyCode) ? nil : "Add ⌃, ⌥, or ⌘…"
        }
        if chord == 0 {
            guard let number = functionKeyNumber(keyCode) else { return "Add ⌃, ⌥, or ⌘…" }
            if number <= workspaceFunctionKeys { return "F1–F11 are the workspace's; add ⌃, ⌥, or ⌘…" }
            return nil
        }
        if chord == UInt32(cmdKey) { return "⌘ alone is for apps' own menus; add ⌃ or ⌥…" }
        return nil
    }

    /// F1 to this one are the workspace's keys (`WorkspaceMenu.functionKeys`).
    static let workspaceFunctionKeys = 11

    /// A key pressed in the recorder.
    init?(event: NSEvent, scope: Scope = .global) {
        self.init(keyCode: UInt32(event.keyCode), modifiers: Self.carbonModifiers(event.modifierFlags), scope: scope)
    }

    /// Why the recorder refuses `event`; nil when it is a shortcut.
    static func refusal(event: NSEvent, scope: Scope = .global) -> String? {
        refusal(keyCode: UInt32(event.keyCode), modifiers: carbonModifiers(event.modifierFlags), scope: scope)
    }

    /// Parses the stored spelling; modifier aliases (`control`, `option`,
    /// `alt`, `command`) and any order or case are accepted.
    init?(string: String, scope: Scope = .global) {
        let tokens = string.lowercased().split(separator: "+", omittingEmptySubsequences: false)
            .map { $0.trimmingCharacters(in: .whitespaces) }
        guard let keyName = tokens.last, !keyName.isEmpty,
              let keyCode = Self.keyCodesByName[keyName] else { return nil }
        var modifiers: UInt32 = 0
        for token in tokens.dropLast() {
            guard let bit = Self.modifierAliases[token], modifiers & bit == 0 else { return nil }
            modifiers |= bit
        }
        self.init(keyCode: keyCode, modifiers: modifiers, scope: scope)
    }

    /// The canonical stored spelling.
    var string: String {
        let names = Self.modifierOrder.filter { modifiers & $0.bit != 0 }.map(\.name)
        return (names + [Self.keys[keyCode]?.name ?? ""]).joined(separator: "+")
    }

    /// The menu-style label, e.g. `⌃⌥⌘T`, with the key the current keyboard
    /// layout puts on that key code (`⌃⌥⌘Q` on AZERTY for the US A key).
    var displayString: String { displayString(keyLabel: Self.layoutLabel) }

    /// The label with `keyLabel` naming character keys; a nil from it falls
    /// back to the US name.
    func displayString(keyLabel: (UInt32) -> String?) -> String {
        Self.modifierOrder.filter { modifiers & $0.bit != 0 }.map(\.symbol).joined()
            + (keyLabel(keyCode) ?? Self.keys[keyCode]?.symbol ?? "?")
    }

    /// What an `NSMenuItem` takes to show this combo as its key equivalent:
    /// the key as the current layout types it (or the function key's
    /// character) and the modifiers. Nil for keys a menu cannot spell this way.
    var menuKeyEquivalent: (key: String, modifiers: NSEvent.ModifierFlags)? {
        var flags: NSEvent.ModifierFlags = []
        if modifiers & UInt32(controlKey) != 0 { flags.insert(.control) }
        if modifiers & UInt32(optionKey) != 0 { flags.insert(.option) }
        if modifiers & UInt32(shiftKey) != 0 { flags.insert(.shift) }
        if modifiers & UInt32(cmdKey) != 0 { flags.insert(.command) }
        if let number = Self.functionKeyNumber(keyCode),
           let scalar = UnicodeScalar(NSF1FunctionKey + number - 1) {
            return (String(Character(scalar)), flags)
        }
        let named: [String: UInt32] = [
            "return": 13, "tab": 9, "space": 32, "delete": 8, "forwarddelete": UInt32(NSDeleteFunctionKey),
            "escape": 27, "left": UInt32(NSLeftArrowFunctionKey), "right": UInt32(NSRightArrowFunctionKey),
            "up": UInt32(NSUpArrowFunctionKey), "down": UInt32(NSDownArrowFunctionKey),
            "home": UInt32(NSHomeFunctionKey), "end": UInt32(NSEndFunctionKey),
            "pageup": UInt32(NSPageUpFunctionKey), "pagedown": UInt32(NSPageDownFunctionKey),
        ]
        if let name = Self.keys[keyCode]?.name, let value = named[name], let scalar = UnicodeScalar(value) {
            return (String(Character(scalar)), flags)
        }
        guard let label = Self.layoutLabel(keyCode) ?? (Self.characterKeyCodes.contains(keyCode) ? Self.keys[keyCode]?.symbol : nil)
        else { return nil }
        return (label.lowercased(), flags)
    }

    static func carbonModifiers(_ flags: NSEvent.ModifierFlags) -> UInt32 {
        var bits: UInt32 = 0
        if flags.contains(.control) { bits |= UInt32(controlKey) }
        if flags.contains(.option) { bits |= UInt32(optionKey) }
        if flags.contains(.shift) { bits |= UInt32(shiftKey) }
        if flags.contains(.command) { bits |= UInt32(cmdKey) }
        return bits
    }

    static func isFunctionKey(_ keyCode: UInt32) -> Bool {
        functionKeyNumber(keyCode) != nil
    }

    /// 5 for F5; nil for any other key.
    static func functionKeyNumber(_ keyCode: UInt32) -> Int? {
        guard let name = keys[keyCode]?.name, name.hasPrefix("f") else { return nil }
        return Int(name.dropFirst())
    }

    /// The character the current keyboard layout types on a character key,
    /// uppercased as a menu shows it (`UCKeyTranslate`); nil for named keys
    /// (Return, arrows, function keys) and when the layout cannot say.
    static func layoutLabel(_ keyCode: UInt32) -> String? {
        guard characterKeyCodes.contains(keyCode),
              let source = TISCopyCurrentKeyboardLayoutInputSource()?.takeRetainedValue(),
              let raw = TISGetInputSourceProperty(source, kTISPropertyUnicodeKeyLayoutData) else { return nil }
        let data = Unmanaged<CFData>.fromOpaque(raw).takeUnretainedValue() as Data
        var deadKeyState: UInt32 = 0
        var characters = [UniChar](repeating: 0, count: 4)
        var length = 0
        let status = data.withUnsafeBytes { buffer -> OSStatus in
            guard let layout = buffer.baseAddress?.assumingMemoryBound(to: UCKeyboardLayout.self) else {
                return OSStatus(paramErr)
            }
            return UCKeyTranslate(
                layout, UInt16(keyCode), UInt16(kUCKeyActionDisplay), 0, UInt32(LMGetKbdType()),
                OptionBits(kUCKeyTranslateNoDeadKeysMask), &deadKeyState, characters.count, &length, &characters
            )
        }
        guard status == noErr, length > 0 else { return nil }
        let label = String(utf16CodeUnits: characters, count: length).trimmingCharacters(in: .whitespacesAndNewlines)
        guard !label.isEmpty, label.unicodeScalars.allSatisfy({ !CharacterSet.controlCharacters.contains($0) }) else {
            return nil
        }
        return label.uppercased()
    }

    // MARK: Tables

    private static let modifierOrder: [(bit: UInt32, name: String, symbol: String)] = [
        (UInt32(controlKey), "ctrl", "⌃"),
        (UInt32(optionKey), "opt", "⌥"),
        (UInt32(shiftKey), "shift", "⇧"),
        (UInt32(cmdKey), "cmd", "⌘"),
    ]

    private static let modifierAliases: [String: UInt32] = [
        "ctrl": UInt32(controlKey), "control": UInt32(controlKey),
        "opt": UInt32(optionKey), "option": UInt32(optionKey), "alt": UInt32(optionKey),
        "shift": UInt32(shiftKey),
        "cmd": UInt32(cmdKey), "command": UInt32(cmdKey),
    ]

    /// The keys that type a character, which the layout names.
    static let characterKeyCodes: Set<UInt32> = Set(characterKeys.keys.map { UInt32($0) })

    private static let characterKeys: [Int: (String, String)] = [
        kVK_ANSI_A: ("a", "A"), kVK_ANSI_B: ("b", "B"), kVK_ANSI_C: ("c", "C"),
        kVK_ANSI_D: ("d", "D"), kVK_ANSI_E: ("e", "E"), kVK_ANSI_F: ("f", "F"),
        kVK_ANSI_G: ("g", "G"), kVK_ANSI_H: ("h", "H"), kVK_ANSI_I: ("i", "I"),
        kVK_ANSI_J: ("j", "J"), kVK_ANSI_K: ("k", "K"), kVK_ANSI_L: ("l", "L"),
        kVK_ANSI_M: ("m", "M"), kVK_ANSI_N: ("n", "N"), kVK_ANSI_O: ("o", "O"),
        kVK_ANSI_P: ("p", "P"), kVK_ANSI_Q: ("q", "Q"), kVK_ANSI_R: ("r", "R"),
        kVK_ANSI_S: ("s", "S"), kVK_ANSI_T: ("t", "T"), kVK_ANSI_U: ("u", "U"),
        kVK_ANSI_V: ("v", "V"), kVK_ANSI_W: ("w", "W"), kVK_ANSI_X: ("x", "X"),
        kVK_ANSI_Y: ("y", "Y"), kVK_ANSI_Z: ("z", "Z"),
        kVK_ANSI_0: ("0", "0"), kVK_ANSI_1: ("1", "1"), kVK_ANSI_2: ("2", "2"),
        kVK_ANSI_3: ("3", "3"), kVK_ANSI_4: ("4", "4"), kVK_ANSI_5: ("5", "5"),
        kVK_ANSI_6: ("6", "6"), kVK_ANSI_7: ("7", "7"), kVK_ANSI_8: ("8", "8"),
        kVK_ANSI_9: ("9", "9"),
        kVK_ANSI_Minus: ("minus", "-"), kVK_ANSI_Equal: ("equal", "="),
        kVK_ANSI_LeftBracket: ("leftbracket", "["), kVK_ANSI_RightBracket: ("rightbracket", "]"),
        kVK_ANSI_Quote: ("quote", "'"), kVK_ANSI_Semicolon: ("semicolon", ";"),
        kVK_ANSI_Backslash: ("backslash", "\\"), kVK_ANSI_Comma: ("comma", ","),
        kVK_ANSI_Slash: ("slash", "/"), kVK_ANSI_Period: ("period", "."),
        kVK_ANSI_Grave: ("grave", "`"),
    ]

    /// Keys by their US-layout virtual key code, as the hardware reports them.
    static let keys: [UInt32: (name: String, symbol: String)] = {
        var table: [Int: (String, String)] = characterKeys
        table.merge([
            kVK_Return: ("return", "↩"), kVK_Tab: ("tab", "⇥"), kVK_Space: ("space", "Space"),
            kVK_Delete: ("delete", "⌫"), kVK_ForwardDelete: ("forwarddelete", "⌦"),
            kVK_Escape: ("escape", "⎋"),
            kVK_LeftArrow: ("left", "←"), kVK_RightArrow: ("right", "→"),
            kVK_UpArrow: ("up", "↑"), kVK_DownArrow: ("down", "↓"),
            kVK_Home: ("home", "↖"), kVK_End: ("end", "↘"),
            kVK_PageUp: ("pageup", "⇞"), kVK_PageDown: ("pagedown", "⇟"),
        ]) { first, _ in first }
        let functionKeys = [
            kVK_F1, kVK_F2, kVK_F3, kVK_F4, kVK_F5, kVK_F6, kVK_F7, kVK_F8, kVK_F9, kVK_F10,
            kVK_F11, kVK_F12, kVK_F13, kVK_F14, kVK_F15, kVK_F16, kVK_F17, kVK_F18, kVK_F19, kVK_F20,
        ]
        for (index, code) in functionKeys.enumerated() {
            table[code] = ("f\(index + 1)", "F\(index + 1)")
        }
        return Dictionary(uniqueKeysWithValues: table.map { (UInt32($0.key), (name: $0.value.0, symbol: $0.value.1)) })
    }()

    private static let keyCodesByName: [String: UInt32] =
        Dictionary(uniqueKeysWithValues: keys.map { ($0.value.name, $0.key) })
}

/// One of the app's global shortcuts as a preference: absent means the
/// default combo, an empty string means turned off, and anything unparseable
/// reads as the default.
struct HotKeyPreference: Equatable {
    let defaultsKey: String
    let `default`: HotKeyCombo
    /// What Preferences, conflict warnings, and the status menu item that
    /// does the same call it.
    let label: String

    /// Shows the dashboard where `amxDashboardPlace` says: the workspace's
    /// own, or a tab in Kitty.
    static let dashboard = HotKeyPreference(
        defaultsKey: "amxHotKeyDashboard", default: HotKeyCombo(string: "ctrl+opt+cmd+a")!, label: "Open Dashboard"
    )
    /// Focuses the next session waiting on a human.
    static let nextAttention = HotKeyPreference(
        defaultsKey: "amxHotKeyNextAttention", default: HotKeyCombo(string: "ctrl+opt+cmd+n")!, label: "Next Waiting Session"
    )

    static let all = [dashboard, nextAttention]

    func load(from defaults: UserDefaults = .standard) -> HotKeyCombo? {
        guard let stored = defaults.string(forKey: defaultsKey) else { return self.default }
        if stored.isEmpty { return nil }
        return HotKeyCombo(string: stored) ?? self.default
    }

    func save(_ combo: HotKeyCombo?, to defaults: UserDefaults = .standard) {
        defaults.set(combo?.string ?? "", forKey: defaultsKey)
    }
}

/// Registers one global shortcut with Carbon's `RegisterEventHotKey`, which
/// needs no Accessibility permission, and calls `onPress` on the main thread.
final class HotKeyCenter {
    enum Registration: Equatable {
        case none
        case registered(HotKeyCombo)
        case failed(HotKeyCombo, OSStatus)

        /// The shortcut that works now; nil when there is none or the system
        /// refused it, so nothing shows a key that does nothing.
        var registeredCombo: HotKeyCombo? {
            guard case let .registered(combo) = self else { return nil }
            return combo
        }

        /// What Preferences says about it; nil when all is well.
        var problem: String? {
            guard case .failed(let combo, let status) = self else { return nil }
            switch Int(status) {
            case eventHotKeyExistsErr:
                return "\(combo.displayString) is already taken by another app. Choose another shortcut."
            case eventHotKeyInvalidErr:
                return "\(combo.displayString) cannot be used as a global shortcut."
            default:
                return "\(combo.displayString) could not be registered (error \(status))."
            }
        }
    }

    /// `'amHK'`, so events from another center's registration are ignored.
    static let signature: OSType = 0x616D_484B
    private static let logger = Logger(subsystem: "io.github.sglavoie.agentmux", category: "hotkey")
    private static var nextID: UInt32 = 1

    var onPress: (() -> Void)?
    private(set) var registration: Registration = .none
    /// What Carbon reports back on a press of this center's shortcut.
    var hotKeyID: EventHotKeyID { EventHotKeyID(signature: Self.signature, id: id) }
    private let id: UInt32
    private var hotKeyRef: EventHotKeyRef?
    private var handlerRef: EventHandlerRef?

    init() {
        id = Self.nextID
        Self.nextID += 1
        var spec = EventTypeSpec(eventClass: OSType(kEventClassKeyboard), eventKind: UInt32(kEventHotKeyPressed))
        let status = InstallEventHandler(
            GetApplicationEventTarget(),
            { _, event, userData in
                guard let event, let userData else { return OSStatus(eventNotHandledErr) }
                var hotKeyID = EventHotKeyID()
                let status = GetEventParameter(
                    event, EventParamName(kEventParamDirectObject), EventParamType(typeEventHotKeyID),
                    nil, MemoryLayout<EventHotKeyID>.size, nil, &hotKeyID
                )
                let center = Unmanaged<HotKeyCenter>.fromOpaque(userData).takeUnretainedValue()
                guard status == noErr, hotKeyID.signature == HotKeyCenter.signature,
                      hotKeyID.id == center.id else { return OSStatus(eventNotHandledErr) }
                center.onPress?()
                return noErr
            },
            1, &spec, Unmanaged.passUnretained(self).toOpaque(), &handlerRef
        )
        if status != noErr {
            Self.logger.error("installing the hotkey handler failed (\(status))")
        }
    }

    deinit {
        unregister()
        if let handlerRef {
            RemoveEventHandler(handlerRef)
        }
    }

    /// Replaces the current registration; nil turns the shortcut off.
    @discardableResult
    func register(_ combo: HotKeyCombo?) -> Registration {
        unregister()
        guard let combo else { return registration }
        var ref: EventHotKeyRef?
        let status = RegisterEventHotKey(
            combo.keyCode, combo.modifiers, hotKeyID,
            GetApplicationEventTarget(), 0, &ref
        )
        if status == noErr, let ref {
            hotKeyRef = ref
            registration = .registered(combo)
        } else {
            registration = .failed(combo, status)
        }
        return registration
    }

    func unregister() {
        if let hotKeyRef {
            UnregisterEventHotKey(hotKeyRef)
            self.hotKeyRef = nil
        }
        registration = .none
    }
}

/// Shortcuts macOS itself owns (System Settings › Keyboard › Shortcuts). Carbon
/// cannot see these as conflicts, so Preferences warns about them itself.
enum SystemHotKeys {
    static func enabledCombos() -> Set<HotKeyCombo> {
        var unmanaged: Unmanaged<CFArray>?
        guard CopySymbolicHotKeys(&unmanaged) == noErr,
              let entries = unmanaged?.takeRetainedValue() as? [[String: Any]] else { return [] }
        var combos = Set<HotKeyCombo>()
        for entry in entries {
            guard (entry[kHISymbolicHotKeyEnabled as String] as? Bool) == true,
                  let code = (entry[kHISymbolicHotKeyCode as String] as? NSNumber)?.uint32Value,
                  let modifiers = (entry[kHISymbolicHotKeyModifiers as String] as? NSNumber)?.uint32Value,
                  let combo = HotKeyCombo(keyCode: code, modifiers: modifiers) else { continue }
            combos.insert(combo)
        }
        return combos
    }

    static func conflicts(with combo: HotKeyCombo, enabled: Set<HotKeyCombo> = enabledCombos()) -> Bool {
        enabled.contains(combo)
    }
}

/// What Preferences says about one shortcut before and after Carbon has had
/// its say: the same combo as another of the app's shortcuts, a shortcut macOS
/// itself owns, or a registration Carbon refused.
enum HotKeyConflicts {
    /// A shortcut already bound to an earlier one of the app's own; nil when
    /// it is free. Earlier shortcuts win, so only the later one is refused.
    static func duplicate(_ combo: HotKeyCombo?, earlier: [(label: String, combo: HotKeyCombo?)]) -> String? {
        guard let combo else { return nil }
        guard let owner = earlier.first(where: { $0.combo == combo }) else { return nil }
        return "\(combo.displayString) is already used for “\(owner.label)”. Choose another shortcut."
    }

    /// One key equivalent of the app's own menus.
    struct MenuKey: Equatable {
        let title: String
        let key: String
        let modifiers: NSEvent.ModifierFlags
    }

    /// Every key equivalent in `menu` and its submenus.
    static func menuKeys(_ menu: NSMenu?) -> [MenuKey] {
        guard let menu else { return [] }
        return menu.items.flatMap { item -> [MenuKey] in
            var keys = menuKeys(item.submenu)
            if !item.keyEquivalent.isEmpty {
                keys.insert(MenuKey(title: item.title, key: item.keyEquivalent, modifiers: item.keyEquivalentModifierMask), at: 0)
            }
            return keys
        }
    }

    /// The warning for a shortcut that is also one of the app's own menu
    /// keys: the global shortcut wins, so the menu item's key stops working.
    static func menuCollision(_ combo: HotKeyCombo?, menuKeys: [MenuKey]) -> String? {
        guard let combo, let wanted = combo.menuKeyEquivalent else { return nil }
        let relevant: NSEvent.ModifierFlags = [.control, .option, .shift, .command]
        let owner = menuKeys.first { item in
            var modifiers = item.modifiers.intersection(relevant)
            // An uppercase key equivalent implies Shift.
            if item.key != item.key.lowercased() { modifiers.insert(.shift) }
            return item.key.lowercased() == wanted.key && modifiers == wanted.modifiers.intersection(relevant)
        }
        guard let owner else { return nil }
        return "\(combo.displayString) is also the menu key for “\(owner.title)”, which stops working while this shortcut is set."
    }

    /// The warning for a registration result; macOS's own shortcuts are
    /// checked only once Carbon accepted it, since Carbon cannot see them.
    static func problem(
        for registration: HotKeyCenter.Registration,
        systemCombos: @autoclosure () -> Set<HotKeyCombo> = SystemHotKeys.enabledCombos()
    ) -> String? {
        if let problem = registration.problem { return problem }
        guard case let .registered(combo) = registration,
              SystemHotKeys.conflicts(with: combo, enabled: systemCombos()) else { return nil }
        return "macOS also uses \(combo.displayString) (System Settings › Keyboard › Keyboard Shortcuts); "
            + "turn it off there, or choose another shortcut."
    }
}
