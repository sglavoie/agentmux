import AppKit
import Combine
import SwiftUI

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
                note("The dashboard opens in the workspace.")
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

            Section("Sidebar appearance") {
                Toggle("Show Overview in sidebar", isOn: $model.preferences.showOverviewInSidebar)
                note("Adds Overview beside Dashboard. Hidden by default; the right sidebar shows session details and activity.")
                Picker("Group sessions by", selection: $model.preferences.sidebarGrouping) {
                    ForEach(WorkspaceSidebarGrouping.allCases, id: \.self) { Text($0.title).tag($0) }
                }
                note(model.preferences.sidebarGrouping.detail)
                Picker("Sort sessions by", selection: $model.preferences.sidebarSort) {
                    ForEach(WorkspaceSidebarSort.allCases, id: \.self) { Text($0.title).tag($0) }
                }
                note("Recently visited sorts sessions within each group by when you opened them. Choose Manual order to drag sessions into place.")
                Toggle("Compact sidebar", isOn: $model.preferences.compactSidebar)
                note("Shows terminals beside their session, like tabs, to fit more sessions in the sidebar.")
                LabeledContent("Sidebar zoom") {
                    HStack {
                        if model.preferences.sidebarZoomPercent != 100 {
                            Button("Reset") { model.preferences.sidebarZoomPercent = 100 }
                        }
                        Stepper("\(model.preferences.sidebarZoomPercent)%",
                                value: $model.preferences.sidebarZoomPercent,
                                in: AppPreferences.sidebarZoomRange,
                                step: AppPreferences.sidebarZoomStep)
                    }
                }
                note("Scales the workspace's sidebar and inspector, separately from the terminals. "
                    + "⌥+ and ⌥− change it and ⌥0 returns to 100%; either way it is kept for the next launch.")
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
                    Toggle("Show return summaries", isOn: $model.preferences.showWelcomeBack)
                    note("Shows recent outcomes and waiting questions after two minutes away, plus unread activity at launch. The title-bar summary button is always available. Off by default.")
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
