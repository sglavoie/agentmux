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
    /// Whether any Kitty is in use: a Kitty session is listed, or the daemon
    /// knows a Kitty socket. The app sets it; Settings shows its Kitty-only
    /// fields only then, or while a preference already names Kitty.
    @Published var kittyInUse = false
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

    /// Why the Kitty socket override cannot be what Kitty listens on.
    var kittySocketProblem: String? {
        let socket = preferences.kittySocketOverride.trimmingCharacters(in: .whitespaces)
        guard !socket.isEmpty else { return nil }
        if socket.hasPrefix("unix:@") || socket.hasPrefix("tcp:") { return nil }
        guard socket.hasPrefix("unix:") else { return "Use Kitty's address form, e.g. unix:/tmp/kitty-1234." }
        let path = String(socket.dropFirst("unix:".count))
        return FileManager.default.fileExists(atPath: path) ? nil : "No socket at \(path) right now."
    }

    /// Whether Settings shows its Kitty-only fields (the socket override and
    /// what the dashboard shortcut does in Kitty): while Kitty is in use, or
    /// while a preference still names it, so a value set earlier can be seen
    /// and undone.
    nonisolated static func showsKittySettings(kittyInUse: Bool, preferences: AppPreferences) -> Bool {
        kittyInUse
            || !preferences.kittySocketOverride.trimmingCharacters(in: .whitespaces).isEmpty
            || preferences.launchBackend == .kitty
            || preferences.dashboardPlace == .kitty
    }

    var showsKittySettings: Bool {
        Self.showsKittySettings(kittyInUse: kittyInUse, preferences: preferences)
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

/// The Settings window: a toolbar tab per `PreferencesTab`, as a Mac Settings
/// window has, each hosting its SwiftUI pane. The window keeps one width and
/// takes the height of the pane it shows, up to what the screen has room for;
/// a pane taller than that scrolls.
@MainActor
final class PreferencesWindowController: NSObject, NSWindowDelegate {
    let model: PreferencesModel
    let dashboardRecorder = HotKeyRecorder()
    let attentionRecorder = HotKeyRecorder()
    let workspaceRecorder = HotKeyRecorder()
    private(set) lazy var window: NSWindow = makeWindow()
    /// The toolbar tabs, made with the window.
    private(set) var tabViewController: PreferencesTabViewController?
    private var selection: AnyCancellable?
    /// Kept clear between the window and the screen's visible edges.
    static let screenMargin: CGFloat = 60
    /// The width every pane is laid out at.
    static let contentWidth: CGFloat = 560
    /// The height a pane is given when its own cannot be measured.
    nonisolated static let fallbackContentHeight: CGFloat = 620
    /// The shortest a pane is made, so a short one still reads as a window.
    nonisolated static let minimumContentHeight: CGFloat = 160

    init(model: PreferencesModel) {
        self.model = model
        super.init()
    }

    /// Told when the window opens and closes: while it is open the app keeps
    /// a Dock icon and a place in ⌘Tab, so the window can be found again.
    var onVisibilityChanged: (Bool) -> Void = { _ in }

    func show() {
        model.refresh()
        onVisibilityChanged(true)
        // An accessory app is not active by default; bring it forward so the
        // window takes key focus.
        NSApp.activate()
        fitToSelectedPane(animate: false)
        window.makeKeyAndOrderFront(nil)
        // The toolbar is in place once the window is on screen; the pane
        // keeps its height under it.
        fitToSelectedPane(animate: false)
    }

    private func makeWindow() -> NSWindow {
        let tabs = PreferencesTabViewController()
        tabs.tabStyle = .toolbar
        tabs.transitionOptions = []
        for tab in PreferencesTab.allCases {
            let pane = NSHostingController(rootView: pane(tab))
            // The pane fills what the window gives it and asks nothing of
            // the window's size: the controller sets that, from `idealHeight`.
            pane.sizingOptions = []
            pane.title = tab.title
            let item = NSTabViewItem(viewController: pane)
            item.identifier = tab.rawValue
            item.label = tab.title
            item.image = NSImage(systemSymbolName: tab.symbolName, accessibilityDescription: tab.title)
            tabs.addTabViewItem(item)
        }
        tabs.selectedTabViewItemIndex = PreferencesTab.allCases.firstIndex(of: model.selectedTab) ?? 0
        tabs.onSelect = { [weak self] tab in self?.didSelect(tab) }
        tabViewController = tabs

        let window = NSWindow(contentViewController: tabs)
        // A fixed width, as a Mac Settings window has; the height follows
        // the pane, set by `fitToSelectedPane` rather than by dragging.
        window.styleMask = [.titled, .closable]
        window.toolbarStyle = .preference
        window.isReleasedWhenClosed = false
        window.delegate = self
        dashboardRecorder.window = window
        attentionRecorder.window = window
        workspaceRecorder.window = window
        window.setContentSize(NSSize(width: Self.contentWidth, height: Self.fallbackContentHeight))
        fit(window, to: model.selectedTab, animate: false)
        window.center()
        // A request to show a tab (Open Settings at the amx binary, the
        // launch destination) selects it here, open or not.
        selection = model.$selectedTab.removeDuplicates().sink { [weak self] tab in
            self?.select(tab)
        }
        return window
    }

    private func pane(_ tab: PreferencesTab) -> PreferencesView {
        PreferencesView(model: model, dashboardRecorder: dashboardRecorder, attentionRecorder: attentionRecorder,
                        workspaceRecorder: workspaceRecorder, tab: tab)
    }

    /// The tab the toolbar shows.
    var selectedTab: PreferencesTab? {
        guard let tabs = tabViewController, tabs.tabViewItems.indices.contains(tabs.selectedTabViewItemIndex) else { return nil }
        return (tabs.tabViewItems[tabs.selectedTabViewItemIndex].identifier as? String).flatMap(PreferencesTab.init(rawValue:))
    }

    private func select(_ tab: PreferencesTab) {
        guard let tabs = tabViewController, let index = PreferencesTab.allCases.firstIndex(of: tab),
              tabs.selectedTabViewItemIndex != index else { return }
        tabs.selectedTabViewItemIndex = index
    }

    /// The operator, or a request, chose another tab: it is remembered, what
    /// was being typed or recorded on the last one ends, and the window takes
    /// the new pane's height.
    private func didSelect(_ tab: PreferencesTab) {
        if model.selectedTab != tab { model.selectedTab = tab }
        model.commitBinaryPath()
        model.commitEnvironment()
        dashboardRecorder.stop()
        attentionRecorder.stop()
        workspaceRecorder.stop()
        fitToSelectedPane(animate: tabViewController?.view.window?.isVisible ?? false)
    }

    private func fitToSelectedPane(animate: Bool) {
        guard let tab = selectedTab, let window = tabViewController?.view.window else { return }
        fit(window, to: tab, animate: animate)
    }

    /// Gives the window the height `tab`'s pane lays out at, keeping its top
    /// edge where it is.
    private func fit(_ window: NSWindow, to tab: PreferencesTab, animate: Bool) {
        let chrome = window.frame.height - (window.contentView?.frame.height ?? window.contentLayoutRect.height)
        let visible = (window.screen ?? NSScreen.main)?.visibleFrame
        let height = Self.contentHeight(ideal: idealHeight(of: tab),
                                        available: visible.map { $0.height - Self.screenMargin - chrome })
        var frame = window.frame
        let newHeight = height + chrome
        guard abs(frame.height - newHeight) > 0.5 || abs(window.contentLayoutRect.width - Self.contentWidth) > 0.5 else { return }
        frame.origin.y = frame.maxY - newHeight
        frame.size.height = newHeight
        frame.size.width = Self.contentWidth + (window.frame.width - (window.contentView?.frame.width ?? Self.contentWidth))
        window.setFrame(frame, display: true, animate: animate)
    }

    /// The height `tab`'s pane lays out at with nothing to scroll, measured
    /// on a copy so the pane on screen keeps its scroll position.
    func idealHeight(of tab: PreferencesTab) -> CGFloat {
        NSHostingView(rootView: pane(tab)).fittingSize.height
    }

    /// The height a pane is given: its own, at least `minimumContentHeight`,
    /// at most what `available` leaves; `fallbackContentHeight` when its own
    /// could not be measured.
    nonisolated static func contentHeight(ideal: CGFloat, available: CGFloat?) -> CGFloat {
        var height = ideal.isFinite && ideal > 0 ? ideal.rounded(.up) : fallbackContentHeight
        height = max(height, minimumContentHeight)
        if let available { height = min(height, max(available.rounded(.down), minimumContentHeight)) }
        return height
    }

    /// A closed window records nothing, and ends any editing of the binary
    /// path or the environment.
    func windowWillClose(_ notification: Notification) {
        model.commitBinaryPath()
        model.commitEnvironment()
        dashboardRecorder.stop()
        attentionRecorder.stop()
        workspaceRecorder.stop()
        onVisibilityChanged(false)
    }

    /// Coming back from System Settings: what was allowed or denied there
    /// (the login item, notifications) is read again, so a note about it
    /// does not outlive the reason for it.
    func windowDidBecomeKey(_ notification: Notification) {
        model.refreshLoginItem()
        model.onRefreshAuthorization?()
    }

    /// Recording ends when Settings loses key, so the shortcuts are back
    /// while the operator works in another window.
    func windowDidResignKey(_ notification: Notification) {
        dashboardRecorder.stop()
        attentionRecorder.stop()
        workspaceRecorder.stop()
    }
}

/// Settings' toolbar tabs, telling its window controller which one the
/// operator picked.
final class PreferencesTabViewController: NSTabViewController {
    var onSelect: ((PreferencesTab) -> Void)?

    override func tabView(_ tabView: NSTabView, didSelect tabViewItem: NSTabViewItem?) {
        super.tabView(tabView, didSelect: tabViewItem)
        guard let id = tabViewItem?.identifier as? String, let tab = PreferencesTab(rawValue: id) else { return }
        onSelect?(tab)
    }
}

/// One Settings pane, or with no `tab` the one the model selects.
struct PreferencesView: View {
    @ObservedObject var model: PreferencesModel
    @ObservedObject var dashboardRecorder: HotKeyRecorder
    @ObservedObject var attentionRecorder: HotKeyRecorder
    @ObservedObject var workspaceRecorder: HotKeyRecorder
    /// The pane shown; nil follows `model.selectedTab`.
    var tab: PreferencesTab?
    @FocusState private var launchFieldFocused: Bool
    @FocusState private var binaryFieldFocused: Bool
    @FocusState private var environmentFieldFocused: String?
    /// The last request to reveal a field this pane acted on, so coming back
    /// to the tab later does not scroll to it again.
    @State private var handledLaunchRequest = 0
    @State private var handledBinaryRequest = 0

    private var shownTab: PreferencesTab { tab ?? model.selectedTab }

    var body: some View {
        Group {
            switch shownTab {
            case .general: generalSettings
            case .shortcuts: shortcutSettings
            case .appearance: appearanceSettings
            case .workspace: workspaceSettings
            case .advanced: advancedSettings
            }
        }
        .frame(width: PreferencesWindowController.contentWidth)
        .frame(minHeight: 0, maxHeight: .infinity, alignment: .top)
    }

    private var generalSettings: some View {
        Form {
            Section("General") {
                Toggle("Open at Login", isOn: Binding(
                    get: { model.launchAtLogin },
                    set: { model.setLaunchAtLogin($0) }
                ))
                if let loginNote = model.launchAtLoginNote {
                    if loginNote == LoginItem.approvalNote {
                        HStack {
                            note(loginNote, warning: true)
                            Spacer()
                            Button("Open Login Items") { LoginItem.openSettings() }
                        }
                    } else {
                        note(loginNote, warning: true)
                    }
                }
                Toggle("Use app notifications", isOn: $model.preferences.useAppNotifications)
                note("Delivers the daemon's notifications through Agent Mux; off leaves them to Alerter.")
                if let denied = PreferencesModel.notificationPermissionNote(model.notificationAuthorization) {
                    HStack {
                        note(denied, warning: true)
                        Spacer()
                        Button("Open System Settings") { NSWorkspace.shared.open(PreferencesModel.notificationSettingsURL) }
                    }
                }
                LabeledContent("Snooze") {
                    HStack {
                        Text(model.snoozeDescription).foregroundStyle(.secondary)
                        Button(SnoozeChoice.resume.title(now: Date())) { model.resumeSnooze() }
                            .disabled(!model.isSnoozed)
                            .help(model.isSnoozed ? "" : SnoozeChoice.notSnoozed)
                    }
                }
            }

            Section("Menu bar") {
                Picker("Status", selection: $model.preferences.statusStyle) {
                    ForEach(StatusTitleStyle.allCases, id: \.self) { Text($0.label).tag($0) }
                }
                Toggle("Show the working count", isOn: $model.preferences.showWorkingCount)
                    .disabled(model.preferences.statusStyle != .glyphs)
                    .help(PreferencesModel.workingCountNote(model.preferences.statusStyle) ?? "")
                if let reason = PreferencesModel.workingCountNote(model.preferences.statusStyle) {
                    note(reason)
                }
                Stepper(PreferencesModel.recentDaysLabel(model.preferences.recentDays),
                        value: $model.preferences.recentDays, in: AppPreferences.recentDaysRange)
                Stepper(PreferencesModel.recentLimitLabel(model.preferences.recentLimit),
                        value: $model.preferences.recentLimit, in: AppPreferences.recentLimitRange)
            }
        }
        .formStyle(.grouped)
    }

    private var shortcutSettings: some View {
        Form {
            Section("Shortcuts") {
                LabeledContent(HotKeyPreference.dashboard.label) {
                    HotKeyRecorderView(combo: $model.preferences.hotKeyDashboard, model: model,
                                       recorder: dashboardRecorder, other: attentionRecorder,
                                       defaultCombo: HotKeyPreference.dashboard.default, additionalOthers: [workspaceRecorder])
                }
                problem(for: HotKeyPreference.dashboard)
                Picker("Show the dashboard in", selection: $model.preferences.dashboardPlace) {
                    ForEach(DashboardPlacePreference.allCases, id: \.self) { Text($0.label).tag($0) }
                }
                if model.showsKittySettings {
                    note("In Kitty, the shortcut brings Kitty forward on the Agent Mux dashboard tab, opening one when none is running.")
                }
                LabeledContent(HotKeyPreference.nextAttention.label) {
                    HotKeyRecorderView(combo: $model.preferences.hotKeyNextAttention, model: model,
                                       recorder: attentionRecorder, other: dashboardRecorder,
                                       defaultCombo: HotKeyPreference.nextAttention.default, additionalOthers: [workspaceRecorder])
                }
                problem(for: HotKeyPreference.nextAttention)
                note("Focuses the first session waiting on a stop, key, error, or question, in the order "
                    + "the workspace lists them; each press moves to the next. Both work from any app.")
            }

            Section("Workspace shortcuts") {
                ForEach(WorkspaceShortcut.allCases) { shortcut in
                    workspaceShortcutRow(shortcut)
                }
                note("Click a command's shortcut and press its keys. Changes apply immediately while Agent Mux is active. "
                    + "Esc cancels recording; Clear removes a binding.")
            }
        }
        .formStyle(.grouped)
    }

    /// One command's row in Workspace shortcuts: its title, its recorder,
    /// and why its binding is inactive when it is. Every row shares
    /// `workspaceRecorder`, which records for one row at a time.
    @ViewBuilder
    private func workspaceShortcutRow(_ shortcut: WorkspaceShortcut) -> some View {
        LabeledContent(shortcut.title) {
            HotKeyRecorderView(
                combo: Binding(get: { shortcut.combo(in: model.preferences) },
                               set: { model.preferences.workspaceShortcuts[shortcut.rawValue] = $0?.string ?? "" }),
                model: model, recorder: workspaceRecorder, other: dashboardRecorder,
                defaultCombo: shortcut.defaultCombo, additionalOthers: [attentionRecorder],
                scope: .application,
                validate: { shortcut.problem(for: $0, preferences: model.preferences, menu: NSApp.mainMenu) },
                owner: shortcut)
        }
        if let problem = shortcut.problem(for: shortcut.combo(in: model.preferences), preferences: model.preferences, menu: NSApp.mainMenu) {
            note(problem + " This binding is inactive until the conflict is resolved.", warning: true)
        }
    }

    private var appearanceSettings: some View {
        Form {
            Section("Terminal appearance") {
                LabeledContent("Default zoom") {
                    HStack {
                        if model.preferences.defaultFontSize != nil {
                            Button("Reset") { model.preferences.defaultFontSize = nil }
                        }
                        Stepper(
                            "\(Ghostty.ConfigOverrides.number(model.preferences.defaultFontSize ?? model.configFontSize)) pt",
                            value: Binding(
                                get: { model.preferences.defaultFontSize ?? model.configFontSize },
                                set: { model.preferences.defaultFontSize = $0.rounded() }
                            ),
                            in: DefaultFontSize.range
                        )
                    }
                }
                note((model.preferences.defaultFontSize == nil
                    ? "Uses the Ghostty config's font size. "
                    : "Overrides the Ghostty config's font-size. ")
                    + "Changing this updates all open terminals. ⌘+ and ⌘− zoom the focused terminal; "
                    + "⌘⇧- returns to this size. New terminals start at this size.")
                Picker("Theme", selection: $model.themeName) {
                    Text("From the Ghostty config").tag(String?.none)
                    Divider()
                    ForEach(model.themeChoices, id: \.self) { Text($0).tag(String?.some($0)) }
                }
                Toggle("Use another theme in Dark Mode", isOn: $model.separateDarkTheme)
                    .disabled(model.themeName == nil)
                if model.separateDarkTheme {
                    Picker("Dark Mode theme", selection: $model.darkThemeName) {
                        ForEach(model.themeChoices, id: \.self) { Text($0).tag($0) }
                    }
                }
                note((model.themeName == nil
                    ? "Uses the Ghostty config's theme. "
                    : "Overrides the Ghostty config's theme; colors it sets one by one still apply. ")
                    + "Changing this recolors all open terminals.")
            }
        }
        .formStyle(.grouped)
    }

    private var workspaceSettings: some View {
        ScrollViewReader { proxy in
            Form {
                Section("Workspace") {
                    Picker("Confirm before ending sessions or closing programs", selection: $model.preferences.confirmationPolicy) {
                        ForEach(WorkspaceConfirmationPolicy.allCases, id: \.self) { Text($0.title).tag($0) }
                    }
                    note("Applies to native controls and the embedded Dashboard, Overview, and conversation actions. "
                        + "Follow config.toml uses defaults.confirm_destructive. Always and Never override it only inside this app. "
                        + "Changes apply to open views. Detach Terminal always leaves the agent running.")
                    Picker("Launch projects in", selection: $model.preferences.launchBackend) {
                        ForEach(LaunchBackendPreference.allCases, id: \.self) { Text($0.label).tag($0) }
                    }.id("launch-destination").focused($launchFieldFocused)
                    VStack(alignment: .leading, spacing: 8) {
                        ForEach(model.preferences.launchBackend.destinationRows(cliBackend: model.snapshot.backend.cliLaunchBackend,
                                                                                cliSource: model.snapshot.backend.cliLaunchBackendSource), id: \.action) { row in
                            HStack(alignment: .firstTextBaseline) {
                                Text(row.action).frame(width: 110, alignment: .leading)
                                VStack(alignment: .leading, spacing: 2) {
                                    Text(row.destination)
                                    Text(row.source).font(.caption).foregroundStyle(.secondary)
                                }
                                Spacer(minLength: 0)
                            }
                        }
                        if model.preferences.launchBackend.differsFromCLI(model.snapshot.backend.cliLaunchBackend) {
                            HStack {
                                Text("App and CLI launches use different destinations.").font(.caption)
                                Button("Follow CLI") { model.preferences.launchBackend = .followCLI }
                            }
                        }
                    }.padding(.vertical, 4)
                    note("Agents launched in the workspace run on Agent Mux's own tmux server: they keep running "
                        + "when the window closes, when the app quits, and across app updates.")
                    Toggle("Open the workspace at launch", isOn: $model.preferences.openWorkspaceAtLaunch)
                    ForEach(model.snapshot.backend.lines, id: \.0) { row in
                        LabeledContent(row.0) { Text(row.1).foregroundStyle(.secondary).textSelection(.enabled) }
                    }
                    ForEach(model.snapshot.backend.notes, id: \.text) { item in
                        note(item.text, warning: item.warning)
                    }
                }
            }
            .formStyle(.grouped)
            .task(id: model.selectedTab == .workspace ? model.launchSettingsRequest : nil) {
                let request = model.launchSettingsRequest
                guard model.selectedTab == .workspace, request > handledLaunchRequest else { return }
                // Let the selected pane lay out before revealing the launch field.
                await Task.yield()
                guard !Task.isCancelled else { return }
                handledLaunchRequest = request
                proxy.scrollTo("launch-destination", anchor: .center)
                launchFieldFocused = true
            }
        }
    }

    private var advancedSettings: some View {
        ScrollViewReader { proxy in
            Form {
                Section("Command Line Tool") {
                    LabeledContent("amx binary") {
                        HStack {
                            TextField("", text: $model.binaryPathDraft, prompt: Text("Automatic"))
                                .labelsHidden()
                                .frame(minWidth: 220)
                                .focused($binaryFieldFocused)
                                .onSubmit { model.commitBinaryPath() }
                                .onChange(of: binaryFieldFocused) { _, focused in
                                    if !focused { model.commitBinaryPath() }
                                }
                            Button("Choose…", action: chooseBinary)
                        }
                    }.id(Self.amxBinaryAnchor)
                    if let problem = model.binaryOverrideProblem {
                        note(problem, warning: true)
                    }
                    if let running = model.runningBinaryNote {
                        note(running)
                    }
                    note("Leave empty to use the bundled amx, ~/.local/bin/amx, then PATH.")
                    ForEach(model.snapshot.lines.filter { $0.0 == "Missing commands" }, id: \.0) { row in
                        LabeledContent(row.0) { Text(row.1).foregroundStyle(.secondary).textSelection(.enabled) }
                    }
                    if model.showsKittySettings {
                        LabeledContent("Kitty socket") {
                            TextField("", text: $model.preferences.kittySocketOverride, prompt: Text("From the daemon"))
                                .labelsHidden()
                                .font(.system(.body, design: .monospaced))
                                .frame(minWidth: 220)
                        }
                        if let problem = model.kittySocketProblem {
                            note(problem, warning: true)
                        }
                        note("Tried first when focusing a session in Kitty or opening the dashboard there.")
                    }
                }

                Section("Daemon") {
                    LabeledContent("Status") {
                        Text(model.daemonStatusLine).foregroundStyle(.secondary).textSelection(.enabled)
                    }
                    DaemonSettingsView(model: model.daemonSettings)
                }

                Section("Environment") {
                    ForEach(EnvironmentOverrides.names(detected: model.snapshot.detectedEnvironment), id: \.self) { name in
                        LabeledContent(name) {
                            let detected = model.snapshot.detectedEnvironment?.value(of: name)
                            TextField("", text: environmentBinding(name), prompt: Text(detected ?? "not set"))
                                .labelsHidden()
                                .font(.system(.body, design: .monospaced))
                                .frame(minWidth: 260)
                                .focused($environmentFieldFocused, equals: name)
                                .onSubmit { model.commitEnvironment() }
                                .help(detected.map { "From your login shell: \($0)" } ?? "Your login shell does not set \(name).")
                        }
                    }
                    note("Gray values are what your login shell sets. Type one to give amx another; "
                        + "clear it to go back. The next amx run uses it.")
                }
                .onChange(of: environmentFieldFocused) { left, _ in
                    if left != nil { model.commitEnvironment() }
                }
            }
            .formStyle(.grouped)
            .task(id: model.selectedTab == .advanced ? model.amxBinarySettingsRequest : nil) {
                let request = model.amxBinarySettingsRequest
                guard model.selectedTab == .advanced, request > handledBinaryRequest else { return }
                // Let the pane lay out before revealing the binary field.
                await Task.yield()
                guard !Task.isCancelled else { return }
                handledBinaryRequest = request
                proxy.scrollTo(Self.amxBinaryAnchor, anchor: .center)
                binaryFieldFocused = true
            }
        }
    }

    /// Where `amxBinarySettingsRequest` scrolls to in Advanced.
    static let amxBinaryAnchor = "amx-binary"

    @ViewBuilder
    private func problem(for hotKey: HotKeyPreference) -> some View {
        if let problem = model.hotKeyProblems[hotKey.defaultsKey] {
            note(problem, warning: true)
        }
    }

    private func note(_ text: String, warning: Bool = false) -> some View {
        Text(text)
            .font(.callout)
            .foregroundStyle(warning ? WorkspaceReviewColors.warning : Color.secondary)
            .fixedSize(horizontal: false, vertical: true)
    }

    /// One variable's field as typed; `commitEnvironment` applies it, and an
    /// empty one then removes the override.
    private func environmentBinding(_ name: String) -> Binding<String> {
        Binding(
            get: { model.environmentDrafts[name] ?? "" },
            set: { model.environmentDrafts[name] = $0 }
        )
    }

    private func chooseBinary() {
        let panel = NSOpenPanel()
        panel.canChooseFiles = true
        panel.canChooseDirectories = false
        panel.allowsMultipleSelection = false
        panel.treatsFilePackagesAsDirectories = true
        panel.message = "Choose the amx binary Agent Mux runs."
        if panel.runModal() == .OK, let url = panel.url {
            model.preferences.binaryPath = url.path
        }
    }
}

struct DaemonSettingsView: View {
    static let checkTitle = "Check Settings"
    static let checkingTitle = "Checking…"
    static let restartTitle = "Restart Daemon to Apply"
    @ObservedObject var model: DaemonSettingsModel
    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            LabeledContent("Applied settings") {
                Text(model.status?.message ?? "Check whether saved settings are applied.")
                    .foregroundStyle(.secondary)
                    .textSelection(.enabled)
            }
            if let error = model.error { Text(error).foregroundStyle(WorkspaceReviewColors.failure).textSelection(.enabled) }
            HStack {
                Button(model.busy ? Self.checkingTitle : Self.checkTitle) { model.refresh() }.disabled(model.busy)
                if model.status?.changed.isEmpty == false {
                    Button(Self.restartTitle) { model.apply() }.disabled(!model.canRestart)
                }
            }
            Text("Restarting the daemon leaves agent processes running. The app checks the applied settings again afterward.")
                .font(.caption).foregroundStyle(.secondary)
        }
    }
}

extension StatusTitleStyle {
    var label: String {
        switch self {
        case .glyphs: return "Glyphs and counts"
        case .compact: return "Attention total"
        case .iconOnly: return "Icon only"
        }
    }
}

/// A button that records the next shortcut typed. Esc cancels; a key
/// `HotKeyCombo.refusal` refuses shows why in the button instead.
struct HotKeyRecorderView: View {
    @Binding var combo: HotKeyCombo?
    @ObservedObject var model: PreferencesModel
    @ObservedObject var recorder: HotKeyRecorder
    /// The other shortcut's recorder, stopped when this one starts.
    let other: HotKeyRecorder
    /// The shortcut the app ships with, which Restore Default puts back.
    let defaultCombo: HotKeyCombo?
    var additionalOthers: [HotKeyRecorder] = []
    var scope: HotKeyCombo.Scope = .global
    var validate: (HotKeyCombo) -> String? = { _ in nil }
    /// Which of the rows sharing `recorder` this one is: only the row the
    /// recorder records for says so. Nil for a recorder of its own.
    var owner: AnyHashable?

    /// Whether `recorder` is recording for this row.
    private var isRecording: Bool { recorder.isRecording && recorder.owner == owner }

    var body: some View {
        VStack(alignment: .trailing, spacing: 4) {
            HStack {
                Button(action: toggleRecording) {
                    Text(label)
                        .frame(minWidth: 140)
                        .foregroundStyle(isRecording ? Color.accentColor : Color.primary)
                }
                if combo != nil, !isRecording {
                    Button("Clear") { combo = nil }
                }
                if combo != defaultCombo, !isRecording {
                    Button("Restore Default") { combo = defaultCombo }
                        .help(defaultCombo.map { "Use \($0.displayString) again" } ?? "Remove the custom shortcut")
                }
            }
            if isRecording, let hint = recorder.hint {
                Text(hint).font(.caption).foregroundStyle(WorkspaceReviewColors.warning).fixedSize(horizontal: false, vertical: true)
            }
        }
        .onDisappear { if isRecording { recorder.stop() } }
    }

    private var label: String {
        if isRecording {
            return "Type a shortcut…"
        }
        return combo?.displayString ?? "Record Shortcut"
    }

    private func toggleRecording() {
        if isRecording {
            recorder.stop()
            return
        }
        other.stop()
        additionalOthers.forEach { $0.stop() }
        let binding = $combo
        recorder.start(
            scope: scope, validate: validate, owner: owner,
            onStateChange: { [weak model] recording in model?.onRecordingChange?(recording) },
            onRecord: { binding.wrappedValue = $0 }
        )
    }
}

/// Local key monitors run on the main thread, which is where it is used.
final class HotKeyRecorder: ObservableObject {
    @Published private(set) var isRecording = false
    @Published private(set) var hint: String?
    /// The row recording, when several share this recorder.
    @Published private(set) var owner: AnyHashable?
    /// The Settings window: the only one whose keys are recorded.
    weak var window: NSWindow?
    private var monitor: Any?
    private var onStateChange: ((Bool) -> Void)?
    private var onRecord: ((HotKeyCombo) -> Void)?
    private var scope = HotKeyCombo.Scope.global
    private var validate: (HotKeyCombo) -> String? = { _ in nil }
    /// How the key monitor is installed and removed; tests capture the
    /// callback the recorder registers.
    var addMonitor: (@escaping (NSEvent) -> NSEvent?) -> Any? = {
        NSEvent.addLocalMonitorForEvents(matching: .keyDown, handler: $0)
    }
    var removeMonitor: (Any) -> Void = { NSEvent.removeMonitor($0) }

    func start(scope: HotKeyCombo.Scope = .global, validate: @escaping (HotKeyCombo) -> String? = { _ in nil },
               owner: AnyHashable? = nil,
               onStateChange: @escaping (Bool) -> Void, onRecord: @escaping (HotKeyCombo) -> Void) {
        stop()
        self.owner = owner
        self.scope = scope
        self.validate = validate
        self.onStateChange = onStateChange
        isRecording = true
        hint = nil
        onStateChange(true)
        self.onRecord = onRecord
        // A recorder that is gone passes the key on; a live one's nil
        // swallows it, so it must not fall back to the event.
        monitor = addMonitor { [weak self] event in
            guard let self else { return event }
            return self.handle(event)
        }
    }

    /// What the monitor does with a key: one typed in Settings is recorded
    /// (or refused, or Esc cancels) and swallowed; any other window's key
    /// goes on to it, since the app's shortcuts are unregistered meanwhile.
    func handle(_ event: NSEvent) -> NSEvent? {
        guard isRecording, let window, event.window === window else { return event }
        let chord = event.modifierFlags.intersection([.command, .control, .option, .shift])
        if Int(event.keyCode) == HotKeyRecorder.escapeKeyCode, chord.isEmpty {
            stop()
        } else if let combo = HotKeyCombo(event: event, scope: scope) {
            if let problem = validate(combo) { hint = problem; return nil }
            // Recorded before stopping, so the shortcuts the app
            // registers again when recording ends are the new ones.
            onRecord?(combo)
            stop()
        } else {
            hint = HotKeyCombo.refusal(event: event, scope: scope)
        }
        return nil
    }

    func stop() {
        if let monitor {
            removeMonitor(monitor)
            self.monitor = nil
        }
        guard isRecording else { return }
        isRecording = false
        hint = nil
        owner = nil
        onRecord = nil
        validate = { _ in nil }
        onStateChange?(false)
        onStateChange = nil
    }

    private static let escapeKeyCode = 0x35
}
