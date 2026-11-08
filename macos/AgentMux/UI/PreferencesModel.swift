import AppKit
import Combine
import SwiftUI

/// What Preferences reports about the running app: which amx it runs, why
/// that one, and what it can do.
struct PreferencesSnapshot: Equatable {
    var binary: AMXBinaryResolver.Resolution?
    var binaryProblem: String?
    /// `binaryProblem` in one line, for the menu and every other notice.
    var binaryMenuProblem: String?
    var binaryVersion: AMXVersion?
    var capabilities: AMXCapabilities?
    var daemonStatus: DaemonStatus?
    /// What the login shell reported at launch, before any override.
    var detectedEnvironment: LaunchEnvironment?
    /// What amx reports about the tmux backend.
    var backend: BackendSetup = .unknown

    var lines: [(String, String)] {
        var rows: [(String, String)] = []
        if let binary {
            rows.append(("amx", binary.url.path))
            rows.append(("Chosen as", Self.describe(binary.source)))
        } else {
            rows.append(("amx", binaryProblem ?? "resolving…"))
        }
        rows.append(("Version", binaryVersion?.line ?? "unknown"))
        rows.append(("Daemon", DaemonStatusLine.text(for: daemonStatus)))
        if let capabilities {
            let missing = [
                capabilities.project ? nil : "project",
                capabilities.openDashboard ? nil : "open-dashboard",
                capabilities.notificationsSnooze ? nil : "notifications snooze",
                capabilities.muteToggleJSON ? nil : "mute-toggle --json",
            ].compactMap { $0 }
            rows.append(("Missing commands", missing.isEmpty ? "none" : missing.joined(separator: ", ")))
        }
        return rows
    }

    static func describe(_ source: AMXBinaryResolver.Source) -> String {
        switch source {
        case .userDefaultsOverride: return "the \(AMXBinaryResolver.overrideDefaultsKey) override"
        case .bundled: return "the copy bundled in the app"
        case .localBin: return "~/.local/bin/amx"
        case .localBinMatchingDaemon: return "~/.local/bin/amx, the build the daemon runs"
        case .newerLocalBin: return "~/.local/bin/amx, newer than the bundled copy"
        case .sameBuildLocalBin: return "~/.local/bin/amx, the same build as the bundled copy"
        case .path: return "the first amx on PATH"
        }
    }
}

/// One of Settings' toolbar tabs, in the order the toolbar shows them.
enum PreferencesTab: String, CaseIterable {
    case general
    case shortcuts
    case appearance
    case workspace
    case advanced

    var title: String {
        switch self {
        case .general: return "General"
        case .shortcuts: return "Shortcuts"
        case .appearance: return "Appearance"
        case .workspace: return "Workspace"
        case .advanced: return "Advanced"
        }
    }

    /// The SF Symbol the toolbar shows above the title.
    var symbolName: String {
        switch self {
        case .general: return "gearshape"
        case .shortcuts: return "keyboard"
        case .appearance: return "paintpalette"
        case .workspace: return "macwindow"
        case .advanced: return "gearshape.2"
        }
    }

    /// Where the tab last shown is remembered, so Settings opens on it again.
    static let defaultsKey = "amxSettingsTab"
}

/// Everything Preferences edits. Each change is saved to `UserDefaults` as it
/// is made, and `onChange` hands the app the old and new values so it can
/// apply the live ones at once. The amx binary path and the environment are
/// the exceptions: they are typed into `binaryPathDraft` and
/// `environmentDrafts` and reach `preferences` only when a field is submitted
/// or loses focus, so a half-typed path or PATH never replaces the amx the
/// app runs or the environment it runs in.
@MainActor
final class PreferencesModel: ObservableObject {
    let daemonSettings = DaemonSettingsModel()
    /// The tab Settings shows, remembered across launches.
    @Published var selectedTab: PreferencesTab {
        didSet {
            guard selectedTab != oldValue else { return }
            defaults.set(selectedTab.rawValue, forKey: PreferencesTab.defaultsKey)
        }
    }
    /// Counts requests to show the launch destination (Workspace › Launch
    /// projects in); each one selects Workspace.
    @Published var launchSettingsRequest = 0 {
        didSet { if launchSettingsRequest > oldValue { selectedTab = .workspace } }
    }
    /// Counts requests to show the amx binary setting (Advanced › Command
    /// Line Tool), such as an alert's Open Settings when amx was not found;
    /// each one selects Advanced.
    @Published var amxBinarySettingsRequest = 0 {
        didSet { if amxBinarySettingsRequest > oldValue { selectedTab = .advanced } }
    }
    let defaults: UserDefaults

    @Published var preferences: AppPreferences {
        didSet {
            guard preferences != oldValue else { return }
            if preferences.binaryPath != oldValue.binaryPath {
                binaryPathDraft = preferences.binaryPath
            }
            if preferences.environment != oldValue.environment {
                environmentDrafts = preferences.environment
            }
            preferences.save(to: defaults)
            onChange?(oldValue, preferences)
        }
    }

    /// The amx binary field as typed; `commitBinaryPath` makes it the
    /// preference.
    @Published var binaryPathDraft: String
    /// The Environment fields as typed, by name; `commitEnvironment` makes
    /// them the overrides.
    @Published var environmentDrafts: [String: String]

    /// Read-only facts about the running app.
    @Published var snapshot = PreferencesSnapshot()
    /// The config's effective size while no preference overrides it.
    @Published var configFontSize: Float = 13
    /// The Ghostty themes a terminal can use, read again on `refresh()`.
    @Published var availableThemes: [String]
    /// The daemon's own snooze, from the latest snapshot; nil when the feed is
    /// not live.
    @Published var daemonNotification: CompanionNotification?
    /// Why each shortcut is not working, or what it collides with, by its
    /// defaults key.
    @Published var hotKeyProblems: [String: String] = [:]
    /// Whether macOS lets the app show notifications, as last read.
    @Published var notificationAuthorization: NotificationAuthorization = .notDetermined

    @Published private(set) var launchAtLogin = false
    @Published private(set) var launchAtLoginNote: String?

    var onChange: ((AppPreferences, AppPreferences) -> Void)?
    /// True while a recorder listens, so the live shortcuts step aside and can
    /// be recorded like any other key.
    var onRecordingChange: ((Bool) -> Void)?
    /// Asks the app to clear the daemon's snooze.
    var onResumeSnooze: (() -> Void)?
    /// Asks the app to read the notification authorization again.
    var onRefreshAuthorization: (() -> Void)?
    /// Asks the app to read the tmux backend's status again, which only it
    /// can do, off the main thread.
    var onRefreshBackend: (() -> Void)?

    init(defaults: UserDefaults = .standard) {
        self.defaults = defaults
        let stored = AppPreferences.load(from: defaults)
        selectedTab = defaults.string(forKey: PreferencesTab.defaultsKey).flatMap(PreferencesTab.init(rawValue:)) ?? .general
        preferences = stored
        binaryPathDraft = stored.binaryPath
        environmentDrafts = stored.environment
        availableThemes = TerminalTheme.available()
    }

    /// The binary field was submitted or left: what it holds is now the
    /// override, and the app resolves amx again if that changed it.
    func commitBinaryPath() {
        preferences.binaryPath = binaryPathDraft
    }

    /// An Environment field was submitted or left: what the fields hold are
    /// now the overrides, an empty one removing its variable.
    func commitEnvironment() {
        preferences.environment = environmentDrafts.filter { !$0.value.isEmpty }
    }

    /// Re-reads what may have changed elsewhere: the login item in System
    /// Settings, a `defaults write`, or the tmux backend.
    func refresh() {
        let stored = AppPreferences.load(from: defaults)
        if stored != preferences {
            // Assigning saves the normalized values back, which is harmless.
            preferences = stored
        }
        availableThemes = TerminalTheme.available()
        refreshLoginItem()
        onRefreshAuthorization?()
        onRefreshBackend?()
        daemonSettings.refresh()
    }

    func refreshLoginItem() {
        launchAtLogin = LoginItem.isEnabled
        launchAtLoginNote = LoginItem.note
    }

    /// Registers or unregisters the login item and records the choice;
    /// returns the failure, which the note also shows.
    @discardableResult
    func setLaunchAtLogin(_ enabled: Bool) -> String? {
        do {
            try LoginItem.setEnabled(enabled)
            preferences.launchAtLogin = enabled
            refreshLoginItem()
            return nil
        } catch {
            refreshLoginItem()
            let message = "Open at Login could not be changed: \(error.localizedDescription)"
            launchAtLoginNote = message
            return message
        }
    }

    /// The theme in light mode, or every mode; nil uses the Ghostty config's
    /// and also drops a Dark Mode theme.
    var themeName: String? {
        get { preferences.terminalTheme?.light }
        set {
            preferences.terminalTheme = newValue.flatMap { TerminalTheme($0, dark: preferences.terminalTheme?.dark) }
        }
    }

    /// Whether Dark Mode has a theme of its own. Turning it on starts from
    /// the theme in use, so nothing changes until another is picked.
    var separateDarkTheme: Bool {
        get { preferences.terminalTheme?.dark != nil }
        set {
            guard var theme = preferences.terminalTheme else { return }
            theme.dark = newValue ? theme.light : nil
            preferences.terminalTheme = theme
        }
    }

    /// The theme in Dark Mode.
    var darkThemeName: String {
        get { preferences.terminalTheme.map { $0.dark ?? $0.light } ?? "" }
        set {
            guard let light = preferences.terminalTheme?.light,
                  let theme = TerminalTheme(light, dark: newValue) else { return }
            preferences.terminalTheme = theme
        }
    }

    /// What the theme pickers list: the available themes, plus any chosen
    /// one that is not among them (removed, or set with `defaults write`),
    /// so a picker never shows a blank selection.
    var themeChoices: [String] {
        var choices = availableThemes
        for name in [preferences.terminalTheme?.light, preferences.terminalTheme?.dark].compactMap({ $0 })
        where !choices.contains(name) {
            choices.append(name)
        }
        return choices
    }

    /// Why the binary override in use will not work, checked the way the
    /// resolver checks it.
    var binaryOverrideProblem: String? {
        let trimmed = preferences.binaryPath.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else { return nil }
        let path = (trimmed as NSString).expandingTildeInPath
        return AMXBinaryResolver.isExecutableRegularFile(path) ? nil : "Not an executable file."
    }

    /// Which amx runs and its version.
    var runningBinaryNote: String? {
        guard let binary = snapshot.binary else { return snapshot.binaryProblem }
        let version = snapshot.binaryVersion?.line ?? "version unknown"
        return "Running \(Self.abbreviated(binary.url.path)) (\(PreferencesSnapshot.describe(binary.source))): \(version)"
    }

    /// The daemon's status as Settings' Daemon section shows it.
    var daemonStatusLine: String {
        DaemonStatusLine.text(for: snapshot.daemonStatus)
    }

    /// The snooze as Preferences shows it: the daemon's when the feed is live,
    /// else what the app last asked for.
    var snoozeDescription: String {
        let now = Date()
        if let notification = daemonNotification {
            return SnoozeState(notification).description(now: now)
        }
        if let state = pendingSnooze(now: now) {
            return state.description(now: now) + " (last requested; re-applied when the daemon is back)"
        }
        return "Not snoozed"
    }

    /// True while there is a snooze to clear: the daemon's, or the one the
    /// app would re-apply when the daemon is back.
    var isSnoozed: Bool {
        let now = Date()
        if let notification = daemonNotification {
            return SnoozeState(notification).isSnoozed(now: now)
        }
        return pendingSnooze(now: now) != nil
    }

    /// Clears the snooze `snoozeDescription` shows. The daemon's is the
    /// app's to clear, through `onResumeSnooze`; with no live feed there is
    /// only the stored one, and forgetting it is what stops it being
    /// re-applied to a restarted daemon. The resume is also queued, so the
    /// same daemon, still snoozed when the feed is back, is asked to resume
    /// rather than having its snooze adopted again.
    func resumeSnooze() {
        if daemonNotification != nil {
            onResumeSnooze?()
        } else {
            defaults.set(true, forKey: SnoozeMirror.resumeQueuedKey)
            preferences.snoozeUntil = nil
        }
    }

    /// The stored snooze, while it has not run out.
    private func pendingSnooze(now: Date) -> SnoozeState? {
        guard let until = preferences.snoozeUntil, until > now else { return nil }
        return until == .distantFuture
            ? SnoozeState(indefinite: true)
            : SnoozeState(snoozedUntil: Int64(until.timeIntervalSince1970))
    }

    /// What Settings says under Use app notifications when macOS does not let
    /// the app show them; nil while it does or has not been asked yet.
    static func notificationPermissionNote(_ authorization: NotificationAuthorization) -> String? {
        authorization == .denied ? "Not allowed — macOS blocks Agent Mux's notifications." : nil
    }

    /// System Settings › Notifications.
    static let notificationSettingsURL = URL(string: "x-apple.systempreferences:com.apple.Notifications-Settings.extension")!

    /// Why Show the working count is greyed out; nil when it is not.
    static func workingCountNote(_ style: StatusTitleStyle) -> String? {
        style == .glyphs ? nil : "Only the “\(StatusTitleStyle.glyphs.label)” status shows a working count."
    }

    /// `Recent: last day`, `Recent: last 7 days`.
    static func recentDaysLabel(_ days: Int) -> String {
        days == 1 ? "Recent: last day" : "Recent: last \(days) days"
    }

    /// `Recent: at most 1 conversation`, `Recent: at most 10 conversations`.
    static func recentLimitLabel(_ limit: Int) -> String {
        limit == 1 ? "Recent: at most 1 conversation" : "Recent: at most \(limit) conversations"
    }

    static func time(_ date: Date) -> String {
        DateFormatter.localizedString(from: date, dateStyle: .none, timeStyle: .short)
    }

    static func abbreviated(_ path: String) -> String {
        let home = NSHomeDirectory()
        return path.hasPrefix(home + "/") ? "~" + path.dropFirst(home.count) : path
    }
}
