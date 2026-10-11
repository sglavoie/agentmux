import AppKit
import SwiftUI

/// What Preferences reports about the running app: which amx it runs, why
/// that one, and what it can do.
struct PreferencesSnapshot: Equatable {
    var binary: AMXBinaryResolver.Resolution?
    var binaryProblem: String?
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

/// Everything Preferences edits. Each change is saved to `UserDefaults` as it
/// is made, and `onChange` hands the app the old and new values so it can
/// apply the live ones at once.
@MainActor
final class PreferencesModel: ObservableObject {
    let defaults: UserDefaults

    @Published var preferences: AppPreferences {
        didSet {
            guard preferences != oldValue else { return }
            preferences.save(to: defaults)
            onChange?(oldValue, preferences)
        }
    }

    /// Read-only facts about the running app.
    @Published var snapshot = PreferencesSnapshot()
    /// The config's effective size while no preference overrides it.
    @Published var configFontSize: Float = 13
    /// The daemon's own snooze, from the latest snapshot; nil when the feed is
    /// not live.
    @Published var daemonNotification: CompanionNotification?
    /// Why each shortcut is not working, or what it collides with, by its
    /// defaults key.
    @Published var hotKeyProblems: [String: String] = [:]

    @Published private(set) var launchAtLogin = false
    @Published private(set) var launchAtLoginNote: String?

    var onChange: ((AppPreferences, AppPreferences) -> Void)?
    /// True while a recorder listens, so the live shortcuts step aside and can
    /// be recorded like any other key.
    var onRecordingChange: ((Bool) -> Void)?

    init(defaults: UserDefaults = .standard) {
        self.defaults = defaults
        preferences = AppPreferences.load(from: defaults)
    }

    /// Re-reads what may have changed elsewhere: the login item in System
    /// Settings, or a `defaults write`.
    func refresh() {
        let stored = AppPreferences.load(from: defaults)
        if stored != preferences {
            // Assigning saves the normalized values back, which is harmless.
            preferences = stored
        }
        refreshLoginItem()
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
            let message = "Launch at Login could not be changed: \(error.localizedDescription)"
            launchAtLoginNote = message
            return message
        }
    }

    /// Why the binary override will not work, checked the way the resolver
    /// checks it.
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

    /// The snooze as Preferences shows it: the daemon's when the feed is live,
    /// else what the app last asked for.
    var snoozeDescription: String {
        let now = Date()
        if let notification = daemonNotification {
            return SnoozeState(notification).description(now: now)
        }
        if let until = preferences.snoozeUntil, until > now {
            let state = until == .distantFuture
                ? SnoozeState(indefinite: true)
                : SnoozeState(snoozedUntil: Int64(until.timeIntervalSince1970))
            return state.description(now: now) + " (last requested; re-applied when the daemon is back)"
        }
        return "Not snoozed"
    }

    static func time(_ date: Date) -> String {
        DateFormatter.localizedString(from: date, dateStyle: .none, timeStyle: .short)
    }

    static func abbreviated(_ path: String) -> String {
        let home = NSHomeDirectory()
        return path.hasPrefix(home + "/") ? "~" + path.dropFirst(home.count) : path
    }
}

/// The Preferences window: SwiftUI hosted in an NSWindow.
@MainActor
final class PreferencesWindowController: NSObject, NSWindowDelegate {
    let model: PreferencesModel
    let dashboardRecorder = HotKeyRecorder()
    let attentionRecorder = HotKeyRecorder()
    private(set) lazy var window: NSWindow = makeWindow()
    /// Kept clear between the window and the screen's visible edges.
    static let screenMargin: CGFloat = 60

    init(model: PreferencesModel) {
        self.model = model
        super.init()
    }

    func show() {
        model.refresh()
        // An accessory app is not active by default; bring it forward so the
        // window takes key focus.
        NSApp.activate(ignoringOtherApps: true)
        window.makeKeyAndOrderFront(nil)
    }

    private func makeWindow() -> NSWindow {
        let view = PreferencesView(model: model, dashboardRecorder: dashboardRecorder, attentionRecorder: attentionRecorder)
        // The grouped form's ideal height is all of its content.
        let contentHeight = NSHostingView(rootView: view.fixedSize(horizontal: false, vertical: true)).fittingSize.height
        let window = NSWindow(contentViewController: NSHostingController(rootView: view))
        window.title = "Agent Mux Preferences"
        window.styleMask = [.titled, .closable, .resizable]
        window.isReleasedWhenClosed = false
        window.delegate = self
        // Open as tall as the content wants, within the screen less a margin;
        // the form scrolls whatever does not fit.
        if let visible = NSScreen.main?.visibleFrame {
            let chrome = window.frame.height - window.contentLayoutRect.height
            let height = min(contentHeight + chrome, visible.height - Self.screenMargin)
            var frame = window.frame
            frame.size.height = max(height, window.minSize.height)
            window.setFrame(frame, display: false)
        }
        window.center()
        return window
    }

    /// A closed window records nothing.
    func windowWillClose(_ notification: Notification) {
        dashboardRecorder.stop()
        attentionRecorder.stop()
    }
}

struct PreferencesView: View {
    @ObservedObject var model: PreferencesModel
    @ObservedObject var dashboardRecorder: HotKeyRecorder
    @ObservedObject var attentionRecorder: HotKeyRecorder

    var body: some View {
        Form {
            Section("Shortcuts") {
                LabeledContent(HotKeyPreference.dashboard.label) {
                    HotKeyRecorderView(combo: $model.preferences.hotKeyDashboard, model: model,
                                       recorder: dashboardRecorder, other: attentionRecorder)
                }
                problem(for: HotKeyPreference.dashboard)
                note("Brings Kitty forward on the Agent Mux dashboard tab, opening one when none is running.")
                LabeledContent(HotKeyPreference.nextAttention.label) {
                    HotKeyRecorderView(combo: $model.preferences.hotKeyNextAttention, model: model,
                                       recorder: attentionRecorder, other: dashboardRecorder)
                }
                problem(for: HotKeyPreference.nextAttention)
                note("Focuses the session that has waited longest on a stop, key, error, or question; "
                    + "each press moves to the next. Both work from any app.")
            }

            Section("Menu bar") {
                Picker("Status", selection: $model.preferences.statusStyle) {
                    ForEach(StatusTitleStyle.allCases, id: \.self) { Text($0.label).tag($0) }
                }
                Toggle("Show the working count", isOn: $model.preferences.showWorkingCount)
                    .disabled(model.preferences.statusStyle != .glyphs)
                Stepper("Recent: last \(model.preferences.recentDays) days",
                        value: $model.preferences.recentDays, in: AppPreferences.recentDaysRange)
                Stepper("Recent: at most \(model.preferences.recentLimit) conversations",
                        value: $model.preferences.recentLimit, in: AppPreferences.recentLimitRange)
            }

            Section("General") {
                Toggle("Launch at Login", isOn: Binding(
                    get: { model.launchAtLogin },
                    set: { model.setLaunchAtLogin($0) }
                ))
                if let loginNote = model.launchAtLoginNote {
                    note(loginNote, warning: true)
                }
                Toggle("Use app notifications", isOn: $model.preferences.useAppNotifications)
                note("Delivers the daemon's notifications through Agent Mux; off leaves them to Alerter.")
                LabeledContent("Snooze") { Text(model.snoozeDescription).foregroundStyle(.secondary) }
            }

            Section("Workspace") {
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
                    + "⌘0 returns to this size. New terminals start at this size.")
                Picker("Launch projects in", selection: $model.preferences.launchBackend) {
                    ForEach(LaunchBackendPreference.allCases, id: \.self) { Text($0.label).tag($0) }
                }
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

            Section("amx") {
                LabeledContent("amx binary") {
                    HStack {
                        TextField("", text: $model.preferences.binaryPath, prompt: Text("Automatic"))
                            .labelsHidden()
                            .frame(minWidth: 220)
                        Button("Choose…", action: chooseBinary)
                    }
                }
                if let problem = model.binaryOverrideProblem {
                    note(problem, warning: true)
                }
                if let running = model.runningBinaryNote {
                    note(running)
                }
                note("Leave empty to use the bundled amx, ~/.local/bin/amx, then PATH.")
                ForEach(model.snapshot.lines.filter { $0.0 == "Daemon" || $0.0 == "Missing commands" }, id: \.0) { row in
                    LabeledContent(row.0) { Text(row.1).foregroundStyle(.secondary).textSelection(.enabled) }
                }
                LabeledContent("Kitty socket") {
                    TextField("", text: $model.preferences.kittySocketOverride, prompt: Text("From the daemon"))
                        .labelsHidden()
                        .font(.system(.body, design: .monospaced))
                        .frame(minWidth: 220)
                }
                if let problem = model.kittySocketProblem {
                    note(problem, warning: true)
                }
                note("Tried first when focusing a session or opening the dashboard.")
            }

            Section("Environment") {
                ForEach(EnvironmentOverrides.names(detected: model.snapshot.detectedEnvironment), id: \.self) { name in
                    LabeledContent(name) {
                        let detected = model.snapshot.detectedEnvironment?.value(of: name)
                        TextField("", text: environmentBinding(name), prompt: Text(detected ?? "not set"))
                            .labelsHidden()
                            .font(.system(.body, design: .monospaced))
                            .frame(minWidth: 260)
                            .help(detected.map { "From your login shell: \($0)" } ?? "Your login shell does not set \(name).")
                    }
                }
                note("Gray values are what your login shell sets. Type one to give amx another; "
                    + "clear it to go back. The next amx run uses it.")
            }
        }
        .formStyle(.grouped)
        // The grouped form scrolls, so the window can stay shorter than its
        // content rather than run off a small screen.
        .frame(width: 560)
        .frame(minHeight: 320)
    }

    @ViewBuilder
    private func problem(for hotKey: HotKeyPreference) -> some View {
        if let problem = model.hotKeyProblems[hotKey.defaultsKey] {
            note(problem, warning: true)
        }
    }

    private func note(_ text: String, warning: Bool = false) -> some View {
        Text(text)
            .font(.callout)
            .foregroundStyle(warning ? Color.orange : Color.secondary)
            .fixedSize(horizontal: false, vertical: true)
    }

    /// One variable's override; an empty field removes it.
    private func environmentBinding(_ name: String) -> Binding<String> {
        Binding(
            get: { model.preferences.environment[name] ?? "" },
            set: { value in
                var overrides = model.preferences.environment
                overrides[name] = value.isEmpty ? nil : value
                model.preferences.environment = overrides
            }
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

extension StatusTitleStyle {
    var label: String {
        switch self {
        case .glyphs: return "Glyphs and counts"
        case .compact: return "Attention total"
        case .iconOnly: return "Icon only"
        }
    }
}

/// A button that records the next shortcut typed. Esc cancels; the shortcut
/// must use Control, Option, or Command, unless it is a function key.
struct HotKeyRecorderView: View {
    @Binding var combo: HotKeyCombo?
    @ObservedObject var model: PreferencesModel
    @ObservedObject var recorder: HotKeyRecorder
    /// The other shortcut's recorder, stopped when this one starts.
    let other: HotKeyRecorder

    var body: some View {
        HStack {
            Button(action: toggleRecording) {
                Text(label)
                    .frame(minWidth: 140)
                    .foregroundStyle(recorder.isRecording ? Color.accentColor : Color.primary)
            }
            if combo != nil, !recorder.isRecording {
                Button("Clear") { combo = nil }
            }
        }
        .onDisappear { recorder.stop() }
    }

    private var label: String {
        if recorder.isRecording {
            return recorder.hint ?? "Type a shortcut…"
        }
        return combo?.displayString ?? "Record Shortcut"
    }

    private func toggleRecording() {
        if recorder.isRecording {
            recorder.stop()
            return
        }
        other.stop()
        let binding = $combo
        recorder.start(
            onStateChange: { [weak model] recording in model?.onRecordingChange?(recording) },
            onRecord: { binding.wrappedValue = $0 }
        )
    }
}

/// Local key monitors run on the main thread, which is where it is used.
final class HotKeyRecorder: ObservableObject {
    @Published private(set) var isRecording = false
    @Published private(set) var hint: String?
    private var monitor: Any?
    private var onStateChange: ((Bool) -> Void)?

    func start(onStateChange: @escaping (Bool) -> Void, onRecord: @escaping (HotKeyCombo) -> Void) {
        stop()
        self.onStateChange = onStateChange
        isRecording = true
        hint = nil
        onStateChange(true)
        monitor = NSEvent.addLocalMonitorForEvents(matching: .keyDown) { [weak self] event in
            guard let self else { return event }
            let chord = event.modifierFlags.intersection([.command, .control, .option, .shift])
            if Int(event.keyCode) == HotKeyRecorder.escapeKeyCode, chord.isEmpty {
                self.stop()
            } else if let combo = HotKeyCombo(event: event) {
                self.stop()
                onRecord(combo)
            } else {
                self.hint = "Add ⌃, ⌥, or ⌘…"
            }
            return nil
        }
    }

    func stop() {
        if let monitor {
            NSEvent.removeMonitor(monitor)
            self.monitor = nil
        }
        guard isRecording else { return }
        isRecording = false
        hint = nil
        onStateChange?(false)
        onStateChange = nil
    }

    private static let escapeKeyCode = 0x35
}
