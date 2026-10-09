// Ported and trimmed from Ghostty's macOS app (MIT); see macos/NOTICE-ghostty.
import Foundation
import AppKit
import GhosttyKit

extension Ghostty {
    /// Settings tt.app forces on every configuration, applied after the user's
    /// file so no user value can override them.
    struct ConfigOverrides: Equatable {
        /// The program the surface runs: the resolved `tt`, or tt.app's
        /// launcher wrapping it.
        var command: URL
        /// Arguments after the program.
        var arguments: [String] = []
        /// Variables handed to the command, e.g. a login-shell `PATH`. TERM and
        /// COLORTERM are deliberately absent: Ghostty sets them itself.
        var environment: [String: String] = [:]
        /// Keeps an exited surface open, so its host decides what happens to it
        /// and its last output stays readable.
        var waitAfterCommand = false
        /// The font size in points, or nil to keep the user config's.
        var fontSize: Float?
        /// Raw `key = value` lines appended last, for settings the harness
        /// pins that have no field of their own.
        var extraLines: [String] = []

        /// The override file's lines, in the order Ghostty reads them.
        var lines: [String] {
            var lines = [
                "command = \(Self.commandValue(for: command, arguments: arguments))",
                "wait-after-command = \(waitAfterCommand)",
                "confirm-close-surface = false",
            ]
            if let fontSize {
                lines.append("font-size = \(Self.number(fontSize))")
            }
            for key in environment.keys.sorted() {
                guard let value = environment[key], Self.isSingleLine(key), Self.isSingleLine(value) else {
                    continue
                }
                lines.append("env = \(key)=\(value)")
            }
            return lines + extraLines.filter(Self.isSingleLine)
        }

        /// `direct:` skips the shell round trip, but it splits naively on
        /// spaces, so any word with whitespace sends the whole command through
        /// `shell:`, quoted. Ghostty itself prefixes `exec -l` there.
        static func commandValue(for url: URL, arguments: [String] = []) -> String {
            let words = [url.path] + arguments
            if words.allSatisfy({ $0.rangeOfCharacter(from: .whitespacesAndNewlines) == nil }) {
                return "direct:" + words.joined(separator: " ")
            }
            return "shell:" + words.map(shellQuoted).joined(separator: " ")
        }

        /// A whole number without its ".0", so `14` rather than `14.0`.
        static func number(_ value: Float) -> String {
            value == value.rounded() ? String(Int(value)) : String(value)
        }

        private static func shellQuoted(_ word: String) -> String {
            "'" + word.replacingOccurrences(of: "'", with: "'\\''") + "'"
        }

        private static func isSingleLine(_ s: String) -> Bool {
            s.rangeOfCharacter(from: .newlines) == nil
        }
    }

    /// Maps to a finalized `ghostty_config_t`, built in three layers: Ghostty's
    /// built-in defaults, the optional agentmux-ghostty-harness config file, then the forced
    /// overrides. Ghostty.app's own config is not read; the agentmux-ghostty-harness file may
    /// `config-file = ~/.config/ghostty/config` to share it.
    final class Config {
        /// `~/.config/agentmux-ghostty-harness/ghostty.conf`, relative to a home directory.
        static func userConfigURL(home: URL = FileManager.default.homeDirectoryForCurrentUser) -> URL {
            home.appendingPathComponent(".config/agentmux-ghostty-harness/ghostty.conf")
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

        init(userConfig: URL? = Config.userConfigURL(), overrides: ConfigOverrides) {
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

        /// The configured default background, before any program changes it.
        var backgroundColor: NSColor? {
            self.get("background", as: ghostty_config_color_s.self, initial: ghostty_config_color_s()).map(Ghostty.color)
        }

        private static func load(userConfig: URL?, overrides: ConfigOverrides) -> ghostty_config_t? {
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
                .appendingPathComponent("agentmux-ghostty-harness-overrides-\(UUID().uuidString).conf")
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
    static func color(_ c: ghostty_config_color_s) -> NSColor {
        NSColor(srgbRed: CGFloat(c.r) / 255, green: CGFloat(c.g) / 255, blue: CGFloat(c.b) / 255, alpha: 1)
    }
}
