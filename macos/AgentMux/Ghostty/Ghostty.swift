// Ported and trimmed from Ghostty's macOS app (MIT); see macos/NOTICE-ghostty.
import AppKit
import GhosttyKit
import os

/// Namespace for the libghostty embedding glue, as upstream spells it.
enum Ghostty {
    static let logger = Logger(subsystem: "io.github.sglavoie.agentmux", category: "ghostty")

    /// Whether `ghostty_init` succeeded. It runs once per process, on first
    /// use, and must come before any config, app, or surface; `main.swift`
    /// asks before the application starts so the answer is settled early.
    static let initialized: Bool = {
        let ok = ghostty_init(UInt(CommandLine.argc), CommandLine.unsafeArgv) == GHOSTTY_SUCCESS
        if !ok {
            logger.critical("ghostty_init failed")
        }
        return ok
    }()
}

/// Whatever owns a `ghostty_surface_t`. A surface's userdata is an unretained
/// pointer to its host object, and the runtime callbacks that carry surface
/// userdata (clipboard, close) resolve it back through this protocol.
protocol GhosttySurfaceHost: AnyObject {
    var surface: ghostty_surface_t? { get }
}

extension Ghostty {
    /// The host behind a surface callback's userdata, or nil when the pointer
    /// is null or its object does not host a surface.
    static func surfaceHost(from userdata: UnsafeMutableRawPointer?) -> GhosttySurfaceHost? {
        guard let userdata else { return nil }
        return Unmanaged<AnyObject>.fromOpaque(userdata).takeUnretainedValue() as? GhosttySurfaceHost
    }

    /// Runtime callbacks may arrive on any thread; AppKit is main-thread only.
    static func onMain(_ work: @escaping () -> Void) {
        if Thread.isMainThread {
            work()
        } else {
            DispatchQueue.main.async(execute: work)
        }
    }
}

extension Ghostty {
    /// The embedded Ghostty's version and build mode, e.g. "1.3.1 (ReleaseFast)".
    static var versionDescription: String {
        let info = ghostty_info()
        let version: String
        if let ptr = info.version {
            let data = Data(bytes: ptr, count: Int(info.version_len))
            version = String(decoding: data, as: UTF8.self)
        } else {
            version = "unknown"
        }
        let mode: String
        switch info.build_mode {
        case GHOSTTY_BUILD_MODE_DEBUG: mode = "Debug"
        case GHOSTTY_BUILD_MODE_RELEASE_SAFE: mode = "ReleaseSafe"
        case GHOSTTY_BUILD_MODE_RELEASE_FAST: mode = "ReleaseFast"
        case GHOSTTY_BUILD_MODE_RELEASE_SMALL: mode = "ReleaseSmall"
        default: mode = "unknown build"
        }
        return "\(version) (\(mode))"
    }
}

extension Ghostty {
    /// Whether the main menu may take a key Ghostty has a binding for.
    ///
    /// A `performable` binding is one Ghostty consumes only when its action
    /// can be carried out. Its default paging and search keys are such
    /// bindings, and in a session's terminal they can never be: the
    /// scrollback is the tmux server's, not Ghostty's. The menu item for the
    /// same key knows who holds the history, and `MenuShortcut.perform` runs
    /// it only when it validates as enabled, so the menu goes first and a
    /// disabled item still leaves the key to Ghostty. An `all` binding is
    /// delivered to every surface and is never the menu's.
    static func menuMayClaim(_ flags: ghostty_binding_flags_e) -> Bool {
        flags.rawValue & GHOSTTY_BINDING_FLAGS_CONSUMED.rawValue != 0
            && flags.rawValue & GHOSTTY_BINDING_FLAGS_ALL.rawValue == 0
    }

    /// Lets the main menu claim a shortcut a Ghostty binding also owns. From
    /// sglavoie/tt's main menu, not from Ghostty.
    enum MenuShortcut {
        private static let modifiers: NSEvent.ModifierFlags = [.shift, .control, .option, .command]

        /// The item in `menu`, at any depth, whose shortcut is exactly `event`'s
        /// key and modifiers, or nil when the menu owns no such shortcut. An
        /// uppercase key equivalent carries an implied Shift, and so does a
        /// symbol only Shift types: `}` is Shift+`]`, as AppKit matches it.
        static func item(in menu: NSMenu, matching event: NSEvent) -> NSMenuItem? {
            guard let key = event.charactersIgnoringModifiers?.lowercased(), !key.isEmpty else { return nil }
            let mods = event.modifierFlags.intersection(modifiers)
            return item(in: menu, key: key, mods: mods, shiftTyped: shiftTyped(key, in: event))
        }

        /// Whether Shift is what made `event` type `key`, rather than a
        /// modifier held beside it.
        private static func shiftTyped(_ key: String, in event: NSEvent) -> Bool {
            guard event.modifierFlags.contains(.shift),
                  let plain = event.characters(byApplyingModifiers: [])?.lowercased(), !plain.isEmpty
            else { return false }
            return plain != key
        }

        private static func item(
            in menu: NSMenu, key: String, mods: NSEvent.ModifierFlags, shiftTyped: Bool
        ) -> NSMenuItem? {
            for item in menu.items {
                if let submenu = item.submenu {
                    if let found = self.item(in: submenu, key: key, mods: mods, shiftTyped: shiftTyped) { return found }
                    continue
                }
                let equivalent = item.keyEquivalent
                guard !equivalent.isEmpty, equivalent.lowercased() == key else { continue }
                var itemMods = item.keyEquivalentModifierMask.intersection(modifiers)
                if equivalent != equivalent.lowercased() || shiftTyped { itemMods.insert(.shift) }
                if itemMods == mods { return item }
            }
            return nil
        }

        /// Runs the enabled item in `menu` owning `event`'s shortcut, reporting
        /// whether one did. Validation runs first, since the surface asks
        /// before AppKit has updated the menu for this event.
        static func perform(for event: NSEvent, in menu: NSMenu) -> Bool {
            guard let item = item(in: menu, matching: event), let parent = item.menu else { return false }
            parent.update()
            guard item.isEnabled else { return false }
            let index = parent.index(of: item)
            guard index >= 0 else { return false }
            parent.performActionForItem(at: index)
            return true
        }
    }
}
