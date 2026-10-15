// Ported and trimmed from Ghostty's macOS app (MIT); see macos/NOTICE-ghostty.
import Foundation
import AppKit
import GhosttyKit

extension Ghostty {
    /// Settings Agent Mux.app forces on every configuration, applied after the
    /// user's file so no user value can override them. What a terminal runs is
    /// not among them: each surface names its own command and environment
    /// (see `Ghostty.SurfaceConfiguration`).
    struct ConfigOverrides: Equatable {
        /// The font size in points, or nil to keep the user config's.
        var fontSize: Float?
        /// A `theme` value (`Name` or `light:A,dark:B`), or nil to keep the
        /// user config's. Colors the user config sets one by one still win,
        /// as they do over any theme in Ghostty.
        var theme: String?
        /// Raw `key = value` lines appended last, for settings with no field
        /// of their own. A line holding a newline is dropped.
        var settings: [String] = []

        /// The override file's lines, in the order Ghostty reads them.
        var lines: [String] {
            // Ghostty only reports whether a surface's program would be lost
            // (`ghostty_surface_needs_confirm_quit`); the host decides whether
            // to ask, and never does for an attachment, whose close only
            // detaches from its session.
            var lines = ["confirm-close-surface = true"]
            // ⌘0 and ⌘⇧0 are the tenth session's in the main menu. With
            // fewer sessions that item is disabled, which leaves a key
            // Ghostty binds to it (`menuMayClaim`): Ghostty's reset_font_size
            // would run behind a menu that says otherwise. Actual Size is ⌘_.
            lines += Self.menuOwnedKeys.map { "keybind = \($0)=unbind" }
            // The workspace reads a surface's zoom back through what a new
            // window would inherit (`SurfaceView.fontSize`), to give it to
            // the terminal it makes for the same session later.
            lines.append("window-inherit-font-size = true")
            if let fontSize {
                lines.append("font-size = \(Self.number(fontSize))")
            }
            if let theme, Ghostty.isSingleLine(theme), !theme.isEmpty {
                lines.append("theme = \(theme)")
            }
            return lines + settings.filter(Ghostty.isSingleLine)
        }

        /// Ghostty's default bindings for keys the main menu owns outright.
        static let menuOwnedKeys = ["super+0", "super+shift+0"]

        /// A whole number without its ".0", so `14` rather than `14.0`.
        static func number(_ value: Float) -> String {
            value == value.rounded() ? String(Int(value)) : String(value)
        }
    }

    static func isSingleLine(_ s: String) -> Bool {
        s.rangeOfCharacter(from: .newlines) == nil && !s.contains("\0")
    }

    /// Maps to a finalized `ghostty_config_t`, built in three layers: Ghostty's
    /// built-in defaults, the optional Agent Mux config file, then the forced
    /// overrides. Ghostty.app's own config is not read; the Agent Mux file may
    /// `config-file = ~/.config/ghostty/config` to share it.
    final class Config {
        /// `~/.config/agent-mux/ghostty.conf`, relative to a home directory:
        /// beside amx's own `config.toml`.
        static func userConfigURL(home: URL = FileManager.default.homeDirectoryForCurrentUser) -> URL {
            home.appendingPathComponent(".config/agent-mux/ghostty.conf")
        }

        private(set) var config: ghostty_config_t? {
            didSet {
                guard let old = oldValue else { return }
                ghostty_config_free(old)
            }
        }

        var loaded: Bool { config != nil }

        /// Diagnostics Ghostty recorded while loading, in its own words.
        var diagnostics: [String] {
            guard let config else { return [] }
            return (0..<ghostty_config_diagnostics_count(config)).map { i in
                String(cString: ghostty_config_get_diagnostic(config, i).message)
            }
        }

        init(userConfig: URL? = Config.userConfigURL(), overrides: ConfigOverrides = ConfigOverrides()) {
            config = Self.load(userConfig: userConfig, overrides: overrides)
        }

        deinit {
            config = nil
        }

        /// Reads a value through `ghostty_config_get`; nil when the key is
        /// unknown to the C API or unset.
        func get<T>(_ key: String, as _: T.Type, initial: T) -> T? {
            guard let config else { return nil }
            var value = initial
            let ok = withUnsafeMutablePointer(to: &value) { ptr in
                ghostty_config_get(config, ptr, key, UInt(key.utf8.count))
            }
            return ok ? value : nil
        }

        func bool(_ key: String) -> Bool? { get(key, as: Bool.self, initial: false) }

        /// Enum-valued keys come back as their tag name.
        func string(_ key: String) -> String? {
            guard let config else { return nil }
            var ptr: UnsafePointer<CChar>?
            guard ghostty_config_get(config, &ptr, key, UInt(key.utf8.count)), let ptr else { return nil }
            return String(cString: ptr)
        }

        /// What a bell does (`bell-features`), packed as Ghostty packs it.
        var bellFeatures: BellFeatures {
            self.get("bell-features", as: CUnsignedInt.self, initial: 0).map(BellFeatures.init(rawValue:)) ?? []
        }

        /// Whether a terminal holding its own history shows a scrollbar:
        /// `scrollbar = system` (the default) does, `never` does not.
        var scrollbarAllowed: Bool { string("scrollbar") != "never" }

        /// Whether programs' progress reports (OSC 9;4) are shown
        /// (`progress-style`, on by default).
        var progressAllowed: Bool { bool("progress-style") ?? true }

        /// The configured default background, before any program changes it.
        var backgroundColor: NSColor? {
            self.get("background", as: ghostty_config_color_s.self, initial: ghostty_config_color_s()).map(Ghostty.color)
        }

        private static func load(userConfig: URL?, overrides: ConfigOverrides) -> ghostty_config_t? {
            guard Ghostty.initialized else { return nil }
            // A new config already holds Ghostty's built-in defaults.
            guard let cfg = ghostty_config_new() else {
                Ghostty.logger.critical("ghostty_config_new failed")
                return nil
            }

            if let userConfig, FileManager.default.fileExists(atPath: userConfig.path) {
                ghostty_config_load_file(cfg, userConfig.path)
                ghostty_config_load_recursive_files(cfg)
            }

            // Ghostty has no setter in its C API, so the overrides travel as a
            // file of their own, read last and removed once read.
            let overrideFile = FileManager.default.temporaryDirectory
                .appendingPathComponent("agent-mux-ghostty-overrides-\(UUID().uuidString).conf")
            do {
                try (overrides.lines.joined(separator: "\n") + "\n")
                    .write(to: overrideFile, atomically: true, encoding: .utf8)
                ghostty_config_load_file(cfg, overrideFile.path)
                try? FileManager.default.removeItem(at: overrideFile)
            } catch {
                Ghostty.logger.critical("writing config overrides failed: \(error.localizedDescription, privacy: .public)")
                ghostty_config_free(cfg)
                return nil
            }

            ghostty_config_finalize(cfg)
            return cfg
        }
    }
}

extension Ghostty {
    /// `bell-features`, in the order of Ghostty's packed struct.
    struct BellFeatures: OptionSet, Equatable {
        let rawValue: CUnsignedInt
        /// The system beep.
        static let system = BellFeatures(rawValue: 1 << 0)
        static let audio = BellFeatures(rawValue: 1 << 1)
        static let attention = BellFeatures(rawValue: 1 << 2)
        static let title = BellFeatures(rawValue: 1 << 3)
        static let border = BellFeatures(rawValue: 1 << 4)
    }

    static func color(_ c: ghostty_config_color_s) -> NSColor {
        NSColor(srgbRed: CGFloat(c.r) / 255, green: CGFloat(c.g) / 255, blue: CGFloat(c.b) / 255, alpha: 1)
    }
}
