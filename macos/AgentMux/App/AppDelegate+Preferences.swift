import AppKit

// Settings, the global shortcuts, and the dashboard shortcut's destination.
extension AppDelegate {
    // MARK: Preferences

    func setUpPreferences() {
        let model = preferencesModel
        // The login item is SMAppService's to keep; the stored choice mirrors
        // it, so one removed in System Settings is not silently re-added.
        model.refreshLoginItem()
        if model.preferences.launchAtLogin != model.launchAtLogin {
            model.preferences.launchAtLogin = model.launchAtLogin
        }
        applyMenuBarPreferences(model.preferences)
        model.onChange = { [weak self] old, new in self?.preferencesChanged(from: old, to: new) }
        model.onResumeSnooze = { [weak self] in self?.snooze(.resume) }
        model.onRefreshAuthorization = { [weak self] in self?.notifications?.refreshAuthorization() }
        model.onRefreshBackend = { [weak self] in self?.refreshBackendStatus() }
        model.daemonSettings.command = { [weak self] in self?.command }
        model.daemonSettings.restart = { [weak self] done in
            guard let self else { done(false); return }
            self.restartDaemon(completion: done)
        }
        model.onRecordingChange = { [weak self] recording in
            guard let self else { return }
            self.isRecordingHotKey = recording
            if recording {
                self.dashboardHotKey?.unregister()
                self.attentionHotKey?.unregister()
            } else {
                self.registerHotKeys()
            }
        }
    }

    func preferencesChanged(from old: AppPreferences, to new: AppPreferences) {
        workspace?.confirmBeforeClosing = new.confirmBeforeClosing
        workspace?.setSidebarZoom(percent: new.sidebarZoomPercent)
        if old.confirmationPolicy != new.confirmationPolicy {
            do { try workspaceConfirmationFile.write(new.confirmationPolicy) }
            catch {
                try? FileManager.default.removeItem(at: workspaceConfirmationFile.url)
                Self.logger.error("Could not update workspace confirmation policy: \(error.localizedDescription, privacy: .public)")
            }
        }
        switch Self.terminalReload(from: old, to: new) {
        case .resetFontSize: applyDefaultFontSize()
        case .keepZoom: terminals?.reloadConfig()
        case nil: break
        }
        // While a recorder listens the shortcuts stay unregistered; it
        // registers them once, with what it recorded, when it stops.
        if !isRecordingHotKey,
           old.hotKeyDashboard != new.hotKeyDashboard || old.hotKeyNextAttention != new.hotKeyNextAttention {
            registerHotKeys()
        }
        if old.workspaceShortcuts != new.workspaceShortcuts || old.hotKeyDashboard != new.hotKeyDashboard || old.hotKeyNextAttention != new.hotKeyNextAttention,
           let menu = NSApp.mainMenu {
            WorkspaceShortcut.apply(new, to: menu)
        }
        applyMenuBarPreferences(new)
        if recent.follow(days: new.recentDays) {
            statusItem?.refreshSubmenus()
        }
        if old.binaryPath != new.binaryPath || old.environment != new.environment {
            lookup.scheduleResolve()
        }
        notifications?.enabled = new.useAppNotifications
    }

    /// What Settings forces on every terminal's Ghostty config.
    nonisolated static func terminalOverrides(_ preferences: AppPreferences) -> Ghostty.ConfigOverrides {
        Ghostty.ConfigOverrides(fontSize: preferences.defaultFontSize, theme: preferences.terminalTheme?.configValue)
    }

    enum TerminalReload: Equatable {
        /// A new default zoom: reload, then reset every terminal to it.
        case resetFontSize
        /// A new theme: every open terminal, shown or hidden, takes the new
        /// colors and keeps its own zoom.
        case keepZoom
    }

    /// How open terminals take a Settings change; nil when it is not theirs.
    nonisolated static func terminalReload(from old: AppPreferences, to new: AppPreferences) -> TerminalReload? {
        if old.defaultFontSize != new.defaultFontSize { return .resetFontSize }
        if old.terminalTheme != new.terminalTheme { return .keepZoom }
        return nil
    }

    /// Reload first so ⌘⇧- uses the new default, then reset even manually
    /// zoomed terminals, including the workspace's hidden session views.
    func applyDefaultFontSize() {
        terminals?.reloadConfig(resetFontSize: true)
        workspace?.forgetFontSizes()
        refreshConfigFontSize()
    }

    /// Reads the font size the Ghostty config sets, for Settings to show
    /// while no preference overrides it. With terminals running their config
    /// is already parsed; without, parsing the config file is done off the
    /// main thread, and a live config read meanwhile wins.
    func refreshConfigFontSize() {
        guard preferencesModel.preferences.defaultFontSize == nil else { return }
        if let config = terminals?.config {
            return publishConfigFontSize(config.get("font-size", as: Float.self, initial: 0))
        }
        listRunner.run({ Ghostty.Config().get("font-size", as: Float.self, initial: 0) }, onMain: { [weak self] size in
            guard let self, self.terminals == nil else { return }
            self.publishConfigFontSize(size)
        })
    }

    private func publishConfigFontSize(_ size: Float?) {
        guard let size, size.isFinite, size > 0, preferencesModel.configFontSize != size else { return }
        preferencesModel.configFontSize = size
    }

    // MARK: Global shortcuts

    func setUpHotKeys() {
        let dashboard = HotKeyCenter()
        dashboard.onPress = { [weak self] in MainActor.assumeIsolated { self?.openDashboard() } }
        dashboardHotKey = dashboard
        let attention = HotKeyCenter()
        attention.onPress = { [weak self] in MainActor.assumeIsolated { self?.focusNextAttention() } }
        attentionHotKey = attention
        registerHotKeys()
    }

    /// Registers both shortcuts from the current preferences, so a change in
    /// Preferences takes effect at once. The dashboard's wins a tie.
    func registerHotKeys() {
        guard let dashboardHotKey, let attentionHotKey else { return }
        let preferences = preferencesModel.preferences
        var problems: [String: String] = [:]
        problems[HotKeyPreference.dashboard.defaultsKey] = HotKeyConflicts.problem(for: dashboardHotKey.register(preferences.hotKeyDashboard))
        let earlier = [(label: HotKeyPreference.dashboard.label, combo: preferences.hotKeyDashboard)]
        if let duplicate = HotKeyConflicts.duplicate(preferences.hotKeyNextAttention, earlier: earlier) {
            attentionHotKey.unregister()
            problems[HotKeyPreference.nextAttention.defaultsKey] = duplicate
        } else {
            problems[HotKeyPreference.nextAttention.defaultsKey] = HotKeyConflicts.problem(for: attentionHotKey.register(preferences.hotKeyNextAttention))
        }
        let menuKeys = HotKeyConflicts.menuKeys(NSApp.mainMenu ?? WorkspaceMenu.make(target: self))
        for (preference, combo) in [(HotKeyPreference.dashboard, preferences.hotKeyDashboard),
                                    (HotKeyPreference.nextAttention, preferences.hotKeyNextAttention)]
        where problems[preference.defaultsKey] == nil {
            problems[preference.defaultsKey] = HotKeyConflicts.menuCollision(combo, menuKeys: menuKeys)
        }
        for problem in problems.values {
            Self.logger.notice("hotkey: \(problem, privacy: .public)")
        }
        preferencesModel.hotKeyProblems = problems
    }

    /// ⌃⌥⌘A: shows the workspace's Dashboard, or, when Preferences sends it
    /// to Kitty, focuses the dashboard running there or opens one, then
    /// brings that Kitty forward. With no Kitty to ask, the dashboard is the
    /// workspace's own either way.
    func openDashboard() {
        guard preferencesModel.preferences.dashboardPlace == .kitty else {
            showWorkspaceView(.dashboard)
            return
        }
        guard let command else {
            refuse(AMXWording.unavailable(notFound: preferences.binaryMenuProblem))
            return
        }
        if let capabilities = preferences.capabilities, !capabilities.openDashboard {
            refuse("\(command.binary.path) has no open-dashboard command; run just install.")
            return
        }
        let candidates = DashboardOpener.candidates(
            override: preferencesModel.preferences.kittySocketOverride,
            snapshot: store.snapshot,
            kittyPIDs: KittyEndpoint.runningKittyPIDs()
        )
        amxRunner.run({
            DashboardOpener.open(candidates: candidates) { socket in
                command.json(OpenDashboardResult.self, ["open-dashboard"], kittyListenOn: socket)
            }
        }, onMain: { [weak self] outcome in
            self?.finishOpenDashboard(outcome)
        })
    }

    func finishOpenDashboard(_ outcome: DashboardOpener.Outcome) {
        switch DashboardOpener.destination(for: outcome) {
        case let .kitty(result, socket):
            Self.logger.notice("dashboard \(result.opened ? "opened" : "focused", privacy: .public) (window \(result.windowID))")
            if !KittyActivator.activate(socket: socket) {
                refuse("Kitty is not running.")
            }
        case .workspace:
            Self.logger.notice("dashboard: no Kitty socket to ask; showing the workspace's own")
            showWorkspaceView(.dashboard)
        case let .failed(message):
            refuse("Could not open the dashboard: \(message)")
        }
    }

    /// ⌃⌥⌘N: focuses the first waiting session in the workspace sidebar's
    /// order, then the next on each press, across every Kitty instance and
    /// the workspace's own sessions.
    func focusNextAttention() {
        guard store.feed == .live, let snapshot = store.snapshot else {
            refuse(DaemonPhrase.notConnected + ".")
            return
        }
        guard let session = cycler.advance(in: store.sessions, snapshotSocket: snapshot.kitty.socket, order: attentionOrder) else {
            Self.logger.notice("next attention: nothing is waiting")
            NSSound.beep()
            return
        }
        Self.logger.notice("next attention: \(MenuBuilder.place(of: session), privacy: .public) (\(session.iconState, privacy: .public))")
        revealOnShow = session.embeddedRuntimeSessionID
        focus(.focusing(session))
    }

    /// A shortcut that could not act: the beep says so now, and the
    /// workspace's pane says why while it has the keyboard; the menu shows
    /// it the next time it opens.
    func refuse(_ message: String) {
        Self.logger.notice("shortcut: \(message, privacy: .public)")
        report(message)
        NSSound.beep()
    }

    // MARK: StatusItemDelegate

    @objc func showPreferences(_ sender: Any?) {
        let controller = preferencesController ?? PreferencesWindowController(model: preferencesModel)
        if preferencesController == nil {
            controller.onVisibilityChanged = { [weak self] visible in
                self?.settingsVisible = visible
                self?.syncActivationPolicy()
            }
        }
        preferencesController = controller
        publishPreferences()
        controller.show()
        refreshConfigFontSize()
    }
}
