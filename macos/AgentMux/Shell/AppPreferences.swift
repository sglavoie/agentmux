import Foundation
import ServiceManagement

/// Every setting the app keeps in `UserDefaults` (domain
/// `io.github.sglavoie.agentmux`), read with its default. An absent, mistyped,
/// or out-of-range value reads as the default, so a stray `defaults write`
/// can never leave the app in a state Preferences cannot show.
struct AppPreferences: Equatable {
    enum Key {
        static let binaryPath = AMXBinaryResolver.overrideDefaultsKey
        static let launchAtLogin = "amxLaunchAtLogin"
        static let workspaceShortcuts = "amxWorkspaceShortcuts"
        static let hotKeyDashboard = HotKeyPreference.dashboard.defaultsKey
        static let hotKeyNextAttention = HotKeyPreference.nextAttention.defaultsKey
        static let statusStyle = "amxStatusStyle"
        static let showWorkingCount = "amxShowWorkingCount"
        static let recentDays = "amxRecentDays"
        static let recentLimit = "amxRecentLimit"
        static let useAppNotifications = "amxUseAppNotifications"
        static let snoozeUntil = "amxSnoozeUntil"
        static let kittySocketOverride = "amxKittySocketOverride"
        static let environment = "amxEnvironment"
        static let launchBackend = "amxLaunchBackend"
        static let openWorkspaceAtLaunch = "amxOpenWorkspaceAtLaunch"
        static let defaultFontSize = "amxDefaultFontSize"
        static let dashboardPlace = "amxDashboardPlace"
        static let terminalTheme = "amxTerminalTheme"

        static let all = [
            binaryPath, launchAtLogin, hotKeyDashboard, hotKeyNextAttention, statusStyle, showWorkingCount,
            recentDays, recentLimit, useAppNotifications, snoozeUntil, kittySocketOverride, environment,
            launchBackend, openWorkspaceAtLaunch, defaultFontSize, dashboardPlace, terminalTheme, workspaceShortcuts,
        ]
    }

    static let recentDaysRange = 1...90
    static let recentLimitRange = 1...50

    /// `amxBinaryPath`: the amx to run instead of the resolver's choice; empty
    /// is automatic.
    var binaryPath = ""
    /// `amxLaunchAtLogin`: whether the login item is on. The registration
    /// itself lives with `SMAppService`; this mirrors it, and the app sets it
    /// to match at launch rather than re-adding an item removed elsewhere.
    var launchAtLogin = false
    /// Absent uses the command default; an empty string clears its binding.
    var workspaceShortcuts: [String: String] = [:]
    var hotKeyDashboard: HotKeyCombo? = HotKeyPreference.dashboard.default
    var hotKeyNextAttention: HotKeyCombo? = HotKeyPreference.nextAttention.default
    var statusStyle: StatusTitleStyle = .glyphs
    var showWorkingCount = true
    /// How far back, and how many, closed conversations the Recent submenu offers.
    var recentDays = 7
    var recentLimit = 15
    /// Whether the app offers itself as the daemon's notification backend.
    var useAppNotifications = true
    /// When the snooze the app last asked for runs out; nil when it asked for
    /// none. The daemon owns the snooze; this only remembers the request.
    var snoozeUntil: Date?
    /// A Kitty socket tried before any the snapshot names; empty is none.
    var kittySocketOverride = ""
    /// Values amx runs with in place of the login shell's.
    var environment: [String: String] = [:]
    /// `amxLaunchBackend`: where Launch Project starts an agent.
    var launchBackend: LaunchBackendPreference = .workspace
    /// `amxOpenWorkspaceAtLaunch`: show the workspace window as soon as the
    /// app starts, even when it was closed last time.
    var openWorkspaceAtLaunch = false
    /// The terminal size at launch and on ⌘⇧-; nil uses the Ghostty config.
    var defaultFontSize: Float?
    /// `amxDashboardPlace`: where the dashboard shortcut shows the dashboard.
    var dashboardPlace: DashboardPlacePreference = .workspace
    /// `amxTerminalTheme`: the Ghostty theme every terminal uses; nil uses
    /// the Ghostty config's.
    var terminalTheme: TerminalTheme?

    init() {}

    static func load(from defaults: UserDefaults = .standard) -> AppPreferences {
        var preferences = AppPreferences()
        preferences.binaryPath = trimmed(defaults.string(forKey: Key.binaryPath))
        preferences.launchAtLogin = bool(defaults, Key.launchAtLogin) ?? false
        preferences.workspaceShortcuts = defaults.dictionary(forKey: Key.workspaceShortcuts)?.compactMapValues { $0 as? String } ?? [:]
        preferences.hotKeyDashboard = HotKeyPreference.dashboard.load(from: defaults)
        preferences.hotKeyNextAttention = HotKeyPreference.nextAttention.load(from: defaults)
        preferences.statusStyle = defaults.string(forKey: Key.statusStyle).flatMap(StatusTitleStyle.init(rawValue:)) ?? .glyphs
        preferences.showWorkingCount = bool(defaults, Key.showWorkingCount) ?? true
        preferences.recentDays = int(defaults, Key.recentDays, in: recentDaysRange) ?? 7
        preferences.recentLimit = int(defaults, Key.recentLimit, in: recentLimitRange) ?? 15
        preferences.useAppNotifications = bool(defaults, Key.useAppNotifications) ?? true
        preferences.snoozeUntil = defaults.object(forKey: Key.snoozeUntil) as? Date
        preferences.kittySocketOverride = trimmed(defaults.string(forKey: Key.kittySocketOverride))
        preferences.environment = EnvironmentOverrides.load(from: defaults)
        preferences.launchBackend = LaunchBackendPreference.stored(defaults.string(forKey: Key.launchBackend))
        preferences.openWorkspaceAtLaunch = bool(defaults, Key.openWorkspaceAtLaunch) ?? false
        preferences.defaultFontSize = DefaultFontSize.load(from: defaults)
        preferences.dashboardPlace = defaults.string(forKey: Key.dashboardPlace)
            .flatMap(DashboardPlacePreference.init(rawValue:)) ?? .workspace
        // Only a stored string: `defaults.string` would read a stray true as "1".
        preferences.terminalTheme = (defaults.object(forKey: Key.terminalTheme) as? String)
            .flatMap(TerminalTheme.init(configValue:))
        return preferences
    }

    /// Writes every value; a value equal to its default is still written,
    /// except that empty strings and an absent snooze remove their key.
    func save(to defaults: UserDefaults = .standard) {
        Self.setString(binaryPath, Key.binaryPath, defaults)
        defaults.set(launchAtLogin, forKey: Key.launchAtLogin)
        defaults.set(workspaceShortcuts, forKey: Key.workspaceShortcuts)
        HotKeyPreference.dashboard.save(hotKeyDashboard, to: defaults)
        HotKeyPreference.nextAttention.save(hotKeyNextAttention, to: defaults)
        defaults.set(statusStyle.rawValue, forKey: Key.statusStyle)
        defaults.set(showWorkingCount, forKey: Key.showWorkingCount)
        defaults.set(Self.clamp(recentDays, Self.recentDaysRange), forKey: Key.recentDays)
        defaults.set(Self.clamp(recentLimit, Self.recentLimitRange), forKey: Key.recentLimit)
        defaults.set(useAppNotifications, forKey: Key.useAppNotifications)
        if let snoozeUntil {
            defaults.set(snoozeUntil, forKey: Key.snoozeUntil)
        } else {
            defaults.removeObject(forKey: Key.snoozeUntil)
        }
        Self.setString(kittySocketOverride, Key.kittySocketOverride, defaults)
        EnvironmentOverrides.save(environment, to: defaults)
        defaults.set(launchBackend.rawValue, forKey: Key.launchBackend)
        defaults.set(openWorkspaceAtLaunch, forKey: Key.openWorkspaceAtLaunch)
        DefaultFontSize.save(defaultFontSize, to: defaults)
        defaults.set(dashboardPlace.rawValue, forKey: Key.dashboardPlace)
        if let terminalTheme {
            defaults.set(terminalTheme.configValue, forKey: Key.terminalTheme)
        } else {
            defaults.removeObject(forKey: Key.terminalTheme)
        }
    }

    static func clamp(_ value: Int, _ range: ClosedRange<Int>) -> Int {
        min(max(value, range.lowerBound), range.upperBound)
    }

    private static func trimmed(_ value: String?) -> String {
        value?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
    }

    private static func setString(_ value: String, _ key: String, _ defaults: UserDefaults) {
        let value = trimmed(value)
        if value.isEmpty {
            defaults.removeObject(forKey: key)
        } else {
            defaults.set(value, forKey: key)
        }
    }

    /// Only a stored boolean counts; `defaults.bool` would read a stray
    /// string as false.
    private static func bool(_ defaults: UserDefaults, _ key: String) -> Bool? {
        guard let number = defaults.object(forKey: key) as? NSNumber,
              CFGetTypeID(number) == CFBooleanGetTypeID() else { return nil }
        return number.boolValue
    }

    private static func int(_ defaults: UserDefaults, _ key: String, in range: ClosedRange<Int>) -> Int? {
        guard let number = defaults.object(forKey: key) as? NSNumber,
              CFGetTypeID(number) != CFBooleanGetTypeID() else { return nil }
        let value = number.intValue
        return range.contains(value) ? value : nil
    }
}

/// Persistent default zoom. Runtime zoom belongs to each terminal separately.
enum DefaultFontSize {
    static let range: ClosedRange<Float> = 6...72

    static func load(from defaults: UserDefaults) -> Float? {
        guard let number = defaults.object(forKey: AppPreferences.Key.defaultFontSize) as? NSNumber,
              CFGetTypeID(number) != CFBooleanGetTypeID(),
              range.contains(number.floatValue) else { return nil }
        return number.floatValue
    }

    static func save(_ points: Float?, to defaults: UserDefaults) {
        if let points, points.isFinite {
            defaults.set(min(max(points, range.lowerBound), range.upperBound),
                         forKey: AppPreferences.Key.defaultFontSize)
        } else {
            defaults.removeObject(forKey: AppPreferences.Key.defaultFontSize)
        }
    }
}

/// A Ghostty theme by name, or one for each system appearance. Persisted as
/// the `theme` value Ghostty reads: `Name`, or `light:Light,dark:Dark`.
struct TerminalTheme: Equatable {
    /// The theme in light mode, and in dark mode too when `dark` is nil.
    var light: String
    /// The theme in dark mode, when it differs by appearance.
    var dark: String?

    /// Nil for a name Ghostty's `theme` syntax cannot carry.
    init?(_ light: String, dark: String? = nil) {
        guard let light = Self.name(light) else { return nil }
        self.light = light
        if let dark {
            guard let dark = Self.name(dark) else { return nil }
            self.dark = dark
        }
    }

    /// Reads `Name` or `light:A,dark:B` in either order; anything else is nil.
    init?(configValue: String) {
        let parts = configValue.split(separator: ",", omittingEmptySubsequences: false).map {
            $0.trimmingCharacters(in: .whitespaces)
        }
        switch parts.count {
        case 1:
            self.init(parts[0])
        case 2:
            var named: [String: String] = [:]
            for part in parts {
                guard let colon = part.firstIndex(of: ":") else { return nil }
                named[String(part[..<colon])] = String(part[part.index(after: colon)...])
            }
            guard named.count == 2, let light = named["light"], let dark = named["dark"] else { return nil }
            self.init(light, dark: dark)
        default:
            return nil
        }
    }

    var configValue: String {
        guard let dark else { return light }
        return "light:\(light),dark:\(dark)"
    }

    /// A theme name, trimmed, or nil when it is empty or holds what Ghostty
    /// reads as syntax: a comma, a colon, or a line break.
    static func name(_ raw: String) -> String? {
        let name = raw.trimmingCharacters(in: .whitespaces)
        guard !name.isEmpty, Ghostty.isSingleLine(name),
              name.rangeOfCharacter(from: CharacterSet(charactersIn: ",:")) == nil else { return nil }
        return name
    }

    /// The themes a terminal can use: those the app bundles and any in
    /// `~/.config/ghostty/themes`, which Ghostty also searches, sorted and
    /// without duplicates.
    static func available(
        bundled: URL? = Bundle.main.resourceURL?.appendingPathComponent("ghostty/themes"),
        user: URL? = FileManager.default.homeDirectoryForCurrentUser.appendingPathComponent(".config/ghostty/themes")
    ) -> [String] {
        let names = [bundled, user].compactMap { $0 }.flatMap { dir in
            (try? FileManager.default.contentsOfDirectory(atPath: dir.path)) ?? []
        }
        return Set(names.filter { !$0.hasPrefix(".") }.compactMap(name))
            .sorted { $0.localizedStandardCompare($1) == .orderedAscending }
    }
}

/// Values amx runs with in place of what the login shell reports, for `PATH`
/// and the variables `LaunchEnvironment` forwards. Persisted in `amxEnvironment`
/// as a dictionary; an absent or blank value uses the shell's.
enum EnvironmentOverrides {
    static let defaultsKey = AppPreferences.Key.environment

    /// What Preferences always lists, in order; any other forwarded name the
    /// shell set is listed after these.
    static let commonNames = ["PATH", "AMX_CONFIG", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME"]

    static func names(detected: LaunchEnvironment?) -> [String] {
        let extra = (detected?.forwarded.keys).map(Array.init) ?? []
        return commonNames + extra.filter { !commonNames.contains($0) }.sorted()
    }

    static func isOverridable(_ name: String) -> Bool {
        name == "PATH" || LaunchEnvironment.isForwarded(name)
    }

    static func load(from defaults: UserDefaults = .standard) -> [String: String] {
        sanitized(defaults.dictionary(forKey: defaultsKey)?.compactMapValues { $0 as? String } ?? [:])
    }

    static func save(_ overrides: [String: String], to defaults: UserDefaults = .standard) {
        let clean = sanitized(overrides)
        if clean.isEmpty {
            defaults.removeObject(forKey: defaultsKey)
        } else {
            defaults.set(clean, forKey: defaultsKey)
        }
    }

    /// Overridable names only, trimmed, single-line, and non-empty.
    static func sanitized(_ overrides: [String: String]) -> [String: String] {
        var clean: [String: String] = [:]
        for (name, value) in overrides where isOverridable(name) {
            let value = value.trimmingCharacters(in: .whitespacesAndNewlines)
            guard !value.isEmpty, !value.contains(where: \.isNewline) else { continue }
            clean[name] = value
        }
        return clean
    }
}

extension LaunchEnvironment {
    /// This environment with `overrides` in place of the shell's values.
    func applying(_ overrides: [String: String]) -> LaunchEnvironment {
        var copy = self
        for (name, value) in EnvironmentOverrides.sanitized(overrides) {
            if name == "PATH" {
                copy.path = value
            } else {
                copy.forwarded[name] = value
            }
        }
        return copy
    }

    /// One variable as a child amx would see it, for Preferences' placeholders.
    func value(of name: String) -> String? {
        name == "PATH" ? path : forwarded[name]
    }
}

/// Launch at Login through `SMAppService.mainApp`: the app itself is the login
/// item, with no helper bundle, so the registration survives a logout and
/// follows the app if it moves.
enum LoginItem {
    static var status: SMAppService.Status { SMAppService.mainApp.status }

    /// Registered, or registered and waiting for the operator to allow it in
    /// System Settings › General › Login Items.
    static var isEnabled: Bool { status == .enabled || status == .requiresApproval }

    static func setEnabled(_ enabled: Bool) throws {
        if enabled {
            try SMAppService.mainApp.register()
        } else {
            try SMAppService.mainApp.unregister()
        }
    }

    /// A note for Preferences, when the state needs one.
    static var note: String? {
        status == .requiresApproval ? approvalNote : nil
    }

    static let approvalNote = "Allow Agent Mux in System Settings › General › Login Items to finish turning this on."

    /// Opens System Settings on Login Items, where the approval is given.
    static func openSettings() {
        SMAppService.openSystemSettingsLoginItems()
    }
}
