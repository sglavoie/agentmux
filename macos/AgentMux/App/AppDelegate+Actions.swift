import AppKit

// What the menu, the shortcuts, and the banners ask for: focusing, launching,
// continuing, muting, and the answer panels, and how a failure is reported.
extension AppDelegate {
    // MARK: Focus

    /// Focuses a session where it is: an embedded one in the workspace, a
    /// Kitty one in Kitty, which is then brought forward.
    func focus(_ row: MenuSessionRow) {
        if !row.runtimeSessionID.isEmpty {
            focusSession(row.runtimeSessionID)
            return
        }
        let candidates = KittyEndpoint.candidates(
            override: preferencesModel.preferences.kittySocketOverride,
            rowSocket: row.kittySocket,
            snapshotSocket: store.snapshot?.kitty.socket ?? "",
            kittyPIDs: KittyEndpoint.runningKittyPIDs()
        )
        guard !candidates.isEmpty else {
            return report(KittyEndpoint.listenOnMissing)
        }
        focusRunner.run({ () -> (FocusOutcome, String) in
            let client = DaemonClient()
            var outcome = FocusOutcome.unknownSocket
            var used = ""
            for socket in candidates {
                outcome = client.focusWindow(socket: socket, windowID: row.windowID)
                used = socket
                if outcome != .unknownSocket { break }
            }
            return (outcome, used)
        }, onMain: { [weak self] answer in
            self?.finishFocus(row, outcome: answer.0, socket: answer.1)
        })
    }

    /// Shows a session that has no Kitty window. The daemon is asked
    /// (`focus_session`) and answers this workspace with a view frame; when it
    /// cannot be asked, or shows nothing, the workspace shows the session
    /// itself, as it did before the daemon knew the request.
    func focusSession(_ runtimeSessionID: String) {
        focusRunner.run({ DaemonClient().focusSession(runtimeSessionID: runtimeSessionID) }, onMain: { [weak self] outcome in
            guard let self, outcome != .shown else { return }
            Self.logger.notice("focus_session \(runtimeSessionID, privacy: .public): \(String(describing: outcome), privacy: .public); showing it here")
            self.showWorkspace(focusing: runtimeSessionID)
        })
    }

    func finishFocus(_ row: MenuSessionRow, outcome: FocusOutcome, socket: String) {
        switch outcome {
        case .focused:
            if !KittyActivator.activate(socket: socket) {
                report("Kitty is not running.")
            }
        case .staleTarget:
            store.markStale(socket: row.kittySocket, windowID: row.windowID)
            statusItem?.rebuildMenu()
        case .unknownSocket:
            report("The daemon does not know this session's Kitty socket.")
        case .unsupported:
            report("Daemon too old to focus sessions — \(MenuBuilder.restartHint)")
        case .notRunning:
            store.apply(health: .notRunning)
            if workspaceIsKey { report(DaemonPhrase.notConnected + ".") }
        case let .failed(message):
            Self.logger.error("focus_window \(row.windowID): \(message, privacy: .public)")
            report("Could not focus the session: \(message)")
        }
    }

    // MARK: Menu actions

    func perform(_ action: MenuAction) {
        switch action {
        case .startDaemon:
            startDaemon()
        case .restartDaemon:
            restartDaemon()
        case .openWorkspace:
            showWorkspace()
        case .openDashboard:
            openDashboard()
        case .launchSettings:
            showPreferences(nil)
            preferencesModel.launchSettingsRequest += 1
        case .nextAttention:
            focusNextAttention()
        case let .launchProject(project):
            launch(project)
        case let .launchProjectInFolder(project):
            launchInChosenFolder(project)
        case let .continueConversation(conversation):
            continueConversation(conversation)
        case let .snooze(choice):
            snooze(choice)
        case let .answerASL(target):
            answer(target)
        }
    }

    // MARK: ASL questions

    /// Answer… on a menu row or a banner: opens the answer panel on that run.
    func answer(_ target: ASLAnswerTarget) {
        let controller = aslPanel ?? ASLQuestionPanelController()
        if aslPanel == nil {
            controller.onFinish = { [weak self] notice in self?.finishAnswer(notice) }
            controller.workspaceFrame = { [weak self] in self?.openWorkspaceFrame }
            aslPanel = controller
        }
        Self.logger.notice("answer panel: run \(target.runID, privacy: .public)")
        controller.show(target)
    }

    /// The workspace window's frame while it is on screen; nil otherwise,
    /// without building a window that was never opened.
    var openWorkspaceFrame: CGRect? {
        guard let workspace, workspace.isOpen, workspace.window.isVisible else { return nil }
        return workspace.window.frame
    }

    /// The panel closed itself, its answer accepted; the notice says so. The
    /// menu is rebuilt now; the snapshot that drops the ❓ follows from the
    /// daemon.
    func finishAnswer(_ notice: String?) {
        if let notice { store.show(notice: notice) }
        statusItem?.rebuildMenu()
    }

    // MARK: Actions

    /// Remembers the Kitty that comes to the front, which is where Launch
    /// Project and Recent open their tabs.
    func trackActiveKitty() {
        if let front = NSWorkspace.shared.frontmostApplication, front.bundleIdentifier == KittyEndpoint.bundleIdentifier {
            lastActiveKittyPID = front.processIdentifier
        }
        activationObserver = NSWorkspace.shared.notificationCenter.addObserver(
            forName: NSWorkspace.didActivateApplicationNotification, object: nil, queue: .main
        ) { [weak self] notification in
            let app = notification.userInfo?[NSWorkspace.applicationUserInfoKey] as? NSRunningApplication
            guard app?.bundleIdentifier == KittyEndpoint.bundleIdentifier, let pid = app?.processIdentifier else { return }
            MainActor.assumeIsolated { self?.lastActiveKittyPID = pid }
        }
    }

    var gate: ActionGate {
        ActionGate(
            capabilities: preferences.capabilities,
            problem: command == nil ? preferences.binaryMenuProblem : lookup.capabilityProblem,
            binaryPath: command?.binary.path ?? "amx",
            resolved: command != nil
        )
    }

    /// The sidebar's arrangement, which both the shortcut and the menu's
    /// Needs attention follow.
    var attentionOrder: WorkspaceOrder {
        workspace?.order ?? (Self.isRunningTests ? WorkspaceOrder() : .load())
    }

    var actionsInput: MenuBuilder.ActionsInput {
        let live = store.feed == .live
        let prefs = preferencesModel.preferences
        return MenuBuilder.ActionsInput(
            gate: gate,
            dashboardPlace: prefs.dashboardPlace,
            launchBackend: prefs.launchBackend,
            cliLaunchBackend: preferences.backend.cliLaunchBackend,
            cliLaunchBackendSource: preferences.backend.cliLaunchBackendSource,
            // Only a shortcut the system took: one refused, taken by another
            // app, or set aside while a recorder listens does nothing.
            dashboardHotKey: dashboardHotKey?.registration.registeredCombo,
            nextAttentionHotKey: attentionHotKey?.registration.registeredCombo,
            feedLive: live,
            waiting: live ? AttentionCycler.ordered(store.sessions).count : 0,
            projects: projects.state,
            missingProjectDirectories: projects.missingDirectories,
            recent: recent.state,
            recentDays: prefs.recentDays,
            recentLimit: prefs.recentLimit,
            liveConversations: store.sessions.compactMap(\.conversation).filter { !$0.sessionID.isEmpty },
            notification: live ? store.snapshot?.notification : nil,
            snoozeSupported: snoozes.supported,
            daemonBusy: daemonBusy,
            configPath: AMXConfigPath.resolve(command?.launchEnvironment ?? lookup.detectedEnvironment),
            now: Date()
        )
    }

    /// The menu opens: what it lists is brought up to date. The project list
    /// is otherwise read again only when `config.toml` changes
    /// (`watchConfig`), and the notification permission when the app comes
    /// forward; nothing polls either.
    func menuWillOpen() {
        lookAgainForAmx()
        notifications?.refreshAuthorization()
        refreshProjects()
        surveyProjectDirectories()
        refreshRecent()
    }

    /// Watches the `config.toml` the amx in use reads (`AMX_CONFIG`, else
    /// `~/.config/agent-mux/config.toml`), so a project added or removed
    /// there is listed without waiting for the menu to open.
    func watchConfig() {
        guard !Self.isRunningTests, let command else {
            configWatcher?.stop()
            return
        }
        let path = AMXConfigPath.expanded(command.launchEnvironment)
        let watcher = configWatcher ?? ConfigWatcher { [weak self] in
            Self.logger.notice("config.toml changed; listing the projects again")
            self?.refreshProjects(force: true)
        }
        configWatcher = watcher
        watcher.watch(path)
    }

    func refreshProjects(force: Bool = false) {
        guard let command, gate.unavailableReason(.project) == nil,
              (force || projects.needsRefresh(now: Date())), projects.state.begin() else { return }
        listRunner.run({ () -> (Result<[ProjectEntry], AMXCommandError>, Set<String>?) in
            let result = ProjectCatalog.fetch(command)
            // Surveyed before it is shown, so a fresh list never offers a
            // launch into a directory that is not there.
            return (result, (try? result.get()).map { ProjectCatalog.survey($0) })
        }, onMain: { [weak self] answer in
            let (result, missing) = answer
            guard let self, self.command?.binary == command.binary else { return }
            if case let .failure(error) = result {
                Self.logger.error("project list: \(error.message, privacy: .public)")
            }
            self.projects.state.finish(result, at: Date())
            if let missing { self.projects.missingDirectories = missing }
            self.updateWorkspaceProjectMenu()
            self.workspace?.refreshCommands()
            self.statusItem?.refreshSubmenus()
        })
    }

    /// Re-checks the cached projects' directories each time the menu opens,
    /// so a checkout cloned or removed since the last list is reflected
    /// without waiting for the list itself to go stale.
    func surveyProjectDirectories() {
        guard let list = projects.state.value, !list.isEmpty, !projects.surveying else { return }
        projects.surveying = true
        listRunner.run({ ProjectCatalog.survey(list) }, onMain: { [weak self] missing in
            guard let self else { return }
            self.projects.surveying = false
            guard self.projects.missingDirectories != missing else { return }
            self.projects.missingDirectories = missing
            self.statusItem?.refreshSubmenus()
        })
    }

    func refreshRecent() {
        let days = preferencesModel.preferences.recentDays
        recent.follow(days: days)
        guard let command, gate.unavailableReason(.sessions) == nil,
              recent.needsRefresh(now: Date()), recent.state.begin() else { return }
        statusItem?.refreshSubmenus()
        listRunner.run({ RecentSessions.fetch(command, days: days) }, onMain: { [weak self] result in
            // A list for another Recent days preference is not this one.
            guard let self, self.command?.binary == command.binary, self.recent.days == days else { return }
            if case let .failure(error) = result {
                Self.logger.error("session list: \(error.message, privacy: .public)")
            }
            self.recent.state.finish(result, at: Date())
            self.workspace?.refreshCommands()
            self.statusItem?.refreshSubmenus()
        })
    }

    /// The sockets a command that opens a new tab tries, the Kitty used last
    /// first.
    func newTabCandidates() -> [String] {
        KittyEndpoint.actionCandidates(
            override: preferencesModel.preferences.kittySocketOverride,
            lastActiveKittyPID: lastActiveKittyPID,
            snapshot: store.snapshot,
            kittyPIDs: KittyEndpoint.runningKittyPIDs()
        )
    }

    /// Read the same resolved default as the CLI at the moment of the action.
    /// A failed read never guesses a destination or starts an agent.
    func resolveLaunchDestination(_ command: AMXCommand, completion: @escaping @MainActor (LaunchBackendPreference) -> Void) {
        let generation = lookup.generation
        store.show(notice: "Checking launch destination…")
        launchRunner.run({ BackendStatus.fetch(command) }, onMain: { [weak self] result in
            guard let self, generation == self.lookup.generation else { return }
            switch result {
            case let .success(status):
                self.preferences.backend = .status(status)
                self.publishPreferences()
                self.statusItem?.refreshSubmenus()
                guard let choice = LaunchBackendPreference.followCLI.resolved(cliBackend: status.launchBackend) else {
                    return self.reportFailure(title: Self.launchFailedTitle, "amx did not report a launch destination. Check amx backend status or choose Workspace or Kitty in Settings.")
                }
                completion(choice)
            case let .failure(error):
                self.reportFailure(title: Self.launchFailedTitle, "Could not read the CLI launch destination: \(error.message)")
            }
        })
    }

    /// Launch Project: the project launches on tmux and the workspace shows
    /// it. Only when Preferences names Kitty is it `amx project launch` in
    /// the Kitty used last, which is then brought forward on the new tab. A
    /// `directory` replaces the project's configured one for this launch.
    func launch(_ project: ProjectEntry, directory: String? = nil, destination: LaunchBackendPreference? = nil) {
        guard let command else {
            return reportFailure(title: Self.launchFailedTitle, AMXWording.unavailable(notFound: preferences.binaryMenuProblem))
        }
        if let reason = gate.unavailableReason(.project) { return reportFailure(title: Self.launchFailedTitle, reason) }
        let choice = destination ?? preferencesModel.preferences.launchBackend
        if choice == .followCLI {
            resolveLaunchDestination(command) { [weak self] resolved in
                self?.launch(project, directory: directory, destination: resolved)
            }
            return
        }
        if choice == .workspace {
            return launchInWorkspace(project, directory: directory, command: command)
        }
        let candidates = newTabCandidates()
        let arguments = ProjectCatalog.kittyLaunchArguments(for: project, directory: directory)
        let progress = Self.launchingNotice(project.title)
        store.show(notice: progress)
        let change = beginChange(Self.launchingChange(project.title))
        launchRunner.run({
            KittyTargetedCommand.run(candidates: candidates) { socket in
                command.json(ProjectLaunchResult.self, arguments, kittyListenOn: socket,
                             timeout: AMXCommand.launchTimeout, mutates: true)
            }
        }, onMain: { [weak self] outcome in
            self?.endChange(change)
            self?.finishOpening(outcome, label: "launch \(project.profile)/\(project.title)", progress: progress) { $0.windowID }
        })
    }

    /// Launch Project's ⌥ alternate: asks for the folder to launch in,
    /// starting from the project's own, and launches there. Cancelling
    /// launches nothing.
    func launchInChosenFolder(_ project: ProjectEntry) {
        let panel = NSOpenPanel()
        panel.canChooseFiles = false
        panel.canChooseDirectories = true
        panel.canCreateDirectories = true
        panel.allowsMultipleSelection = false
        panel.prompt = "Launch"
        panel.message = "Choose the folder to launch \(project.title) in."
        if !project.directory.isEmpty {
            panel.directoryURL = URL(fileURLWithPath: ProjectCatalog.expandingTilde(project.directory), isDirectory: true)
        }
        NSApp.activate()
        guard panel.runModal() == .OK, let url = panel.url else { return }
        launch(project, directory: url.path)
    }

    /// Launches a project on the private tmux server and shows the workspace
    /// on it: the session's own view as soon as the daemon lists it, and the
    /// window meanwhile.
    func launchInWorkspace(_ project: ProjectEntry, directory: String?, command: AMXCommand) {
        let arguments = ProjectCatalog.workspaceLaunchArguments(for: project, directory: directory)
        let label = "launch \(project.profile)/\(project.title)"
        let progress = Self.launchingNotice(project.title)
        store.show(notice: progress)
        let change = beginChange(Self.launchingChange(project.title))
        launchRunner.run({
            command.json(ProjectLaunchResult.self, arguments, timeout: AMXCommand.launchTimeout, mutates: true)
        }, onMain: { [weak self] result in
            self?.endChange(change)
            self?.finishOpeningInWorkspace(result.map(\.runtimeSessionID), label: label, progress: progress)
        })
    }

    /// Shows the workspace on the runtime session a tmux launch or continue
    /// reports: its own view as soon as the daemon lists it, and the window
    /// meanwhile.
    func finishOpeningInWorkspace(_ result: AMXCommandResult<String>, label: String, progress: String) {
        store.replace(notice: progress, with: nil)
        switch result {
        case let .failure(error):
            Self.logger.error("\(label, privacy: .public) on tmux: \(error.message, privacy: .public)")
            reportFailure(title: "Could not \(label)", error.message)
        case let .success(runtimeSessionID), let .partial(runtimeSessionID, _):
            Self.logger.notice("\(label, privacy: .public) on tmux as \(runtimeSessionID, privacy: .public)")
            let controller = workspaceController()
            controller.show()
            guard !runtimeSessionID.isEmpty else { return }
            controller.open(WorkspaceViewRequest(
                kind: WorkspaceViewRequest.sessionKind, runtimeSessionID: runtimeSessionID, awaited: true
            ))
        }
    }

    /// Recent: a conversation a live session runs is focused where it runs,
    /// in the workspace or whichever Kitty that is; any other goes to
    /// `amx session continue`, which resumes it on tmux for the workspace,
    /// or, when Preferences names Kitty, in the Kitty used last. amx sees only
    /// the Kitty it is pointed at, so asking it to continue a conversation
    /// live in another instance would resume a second copy instead of
    /// focusing the first.
    func continueConversation(_ conversation: RecentConversation, destination: LaunchBackendPreference? = nil) {
        if let live = store.sessions.first(where: { $0.conversation == conversation.identity }) {
            focus(.focusing(live, title: conversation.displayName))
            return
        }
        guard let command else {
            return reportFailure(title: Self.launchFailedTitle, AMXWording.unavailable(notFound: preferences.binaryMenuProblem))
        }
        let choice = destination ?? preferencesModel.preferences.launchBackend
        if choice == .followCLI {
            resolveLaunchDestination(command) { [weak self] resolved in
                self?.continueConversation(conversation, destination: resolved)
            }
            return
        }
        // The operator reads the name; the selector goes to the log only.
        Self.logger.notice("continuing \(conversation.selector, privacy: .public)")
        let label = "continue \(conversation.displayName)"
        let progress = Self.launchingNotice(conversation.displayName)
        store.show(notice: progress)
        let change = beginChange(Self.launchingChange(conversation.displayName))
        if choice == .workspace {
            let arguments = RecentSessions.workspaceContinueArguments(for: conversation)
            launchRunner.run({
                command.json(SessionContinueResult.self, arguments, timeout: AMXCommand.launchTimeout, mutates: true)
            }, onMain: { [weak self] result in
                self?.endChange(change)
                self?.finishOpeningInWorkspace(result.map(\.runtimeSessionID), label: label, progress: progress)
            })
            return
        }
        let candidates = newTabCandidates()
        let arguments = RecentSessions.continueArguments(for: conversation) + ["--backend", "kitty"]
        launchRunner.run({
            KittyTargetedCommand.run(candidates: candidates) { socket in
                command.json(SessionContinueResult.self, arguments, kittyListenOn: socket,
                             timeout: AMXCommand.launchTimeout, mutates: true)
            }
        }, onMain: { [weak self] outcome in
            self?.endChange(change)
            self?.finishOpening(outcome, label: label, progress: progress) { $0.windowID }
        })
    }

    /// Brings Kitty forward after a launch or continue, on the reported window
    /// when amx named one and the daemon knows that Kitty.
    func finishOpening<T>(_ outcome: KittyTargetedCommand.Outcome<T>, label: String, progress: String,
                          windowID: @escaping (T) -> Int) {
        store.replace(notice: progress, with: nil)
        switch outcome {
        case .noSocket:
            reportFailure(title: "Could not \(label)", KittyEndpoint.listenOnMissing)
        case let .unreachable(message):
            reportFailure(title: "Could not \(label)", "Could not reach Kitty: \(message)")
        case let .done(result, socket):
            switch result {
            case let .failure(error):
                Self.logger.error("\(label, privacy: .public): \(error.message, privacy: .public)")
                reportFailure(title: "Could not \(label)", error.message)
            case let .success(value):
                Self.logger.notice("\(label, privacy: .public) via \(socket, privacy: .public)")
                bringForward(socket: socket, windowID: windowID(value))
            case let .partial(value, status):
                Self.logger.notice("\(label, privacy: .public) via \(socket, privacy: .public) exited \(status)")
                report("\(label) finished, but a later step failed.")
                bringForward(socket: socket, windowID: windowID(value))
            }
        }
    }

    func bringForward(socket: String, windowID: Int) {
        focusRunner.run({
            if windowID > 0 { _ = DaemonClient().focusWindow(socket: socket, windowID: windowID) }
        }, onMain: { [weak self] in
            if !KittyActivator.activate(socket: socket) { self?.report("Kitty is not running.") }
        })
    }

    /// ⌥ on a session row: `amx mute-toggle` on the row's runtime session
    /// when the workspace embeds it, else against that row's own Kitty.
    /// Window IDs mean something only inside the instance that issued them,
    /// so no other socket is tried.
    func toggleMute(_ row: MenuSessionRow) { toggleMute(row, banner: false) }

    /// `banner`: a notification's Mute, whose failures are reported where
    /// the operator sees them now rather than only on the menu line.
    func toggleMute(_ row: MenuSessionRow, banner: Bool) {
        guard let command else {
            return actionFailed(Self.muteFailedTitle, AMXWording.unavailable(notFound: preferences.binaryMenuProblem), banner: banner)
        }
        if let reason = gate.unavailableReason(.muteToggle) { return actionFailed(Self.muteFailedTitle, reason, banner: banner) }
        guard row.runtimeSessionID.isEmpty else { return toggleMute(runtimeSessionID: row.runtimeSessionID, banner: banner) }
        let socket = row.kittySocket.isEmpty ? store.snapshot?.kitty.socket ?? "" : row.kittySocket
        guard !socket.isEmpty else { return actionFailed(Self.muteFailedTitle, KittyEndpoint.listenOnMissing, banner: banner) }
        // Shown at once, so a menu reopened before the daemon publishes the
        // change does not offer the toggle the click already made.
        store.expectMute(socket: socket, windowID: row.windowID, muted: !row.muted)
        amxRunner.run({
            command.json(MuteToggleResult.self, MuteToggleResult.arguments(windowID: row.windowID), kittyListenOn: socket)
        }, onMain: { [weak self] result in
            guard let self else { return }
            switch result {
            case let .success(value):
                Self.logger.notice("mute-toggle window \(row.windowID): muted=\(value.muted)")
                self.settleExpectedMute(value, socket: socket, windowID: row.windowID)
            case let .partial(value, _):
                self.settleExpectedMute(value, socket: socket, windowID: row.windowID)
                self.report(value.muted ? "Muted, but the tab title was not updated." : "Unmuted, but the tab title was not updated.")
            case let .failure(error):
                Self.logger.error("mute-toggle window \(row.windowID): \(error.message, privacy: .public)")
                self.store.dropExpectedMute(socket: socket, windowID: row.windowID)
                self.actionFailed(Self.muteFailedTitle, error.message, banner: banner)
            }
        })
    }

    /// Replaces the guess `toggleMute` showed with what `amx` reports it did;
    /// a toggle that changed nothing (the window was gone) shows nothing.
    func settleExpectedMute(_ value: MuteToggleResult, socket: String, windowID: Int) {
        let socket = socket.isEmpty ? store.snapshot?.kitty.socket ?? "" : socket
        settleExpectedMute(value, key: AttentionCycler.Key(socket: socket, windowID: windowID))
    }

    func settleExpectedMute(_ value: MuteToggleResult, key: AttentionCycler.Key) {
        if value.changed {
            store.expectMute(key, muted: value.muted)
        } else {
            store.dropExpectedMute(key)
        }
    }

    // MARK: Reporting

    /// Something asked for could not run: the menu shows why the next time
    /// it opens, and while the workspace has the keyboard its pane says so
    /// at once, where the operator is looking.
    func report(_ message: String) {
        Self.logger.notice("action: \(message, privacy: .public)")
        store.show(notice: message)
        if workspaceIsKey { workspace?.showNotice(message) }
    }

    /// A mute, snooze, or answer that failed: one a notification's button
    /// asked for is reported as `reportFailure` does, since no menu is open
    /// to show it; one from the menu or a shortcut on the menu line.
    func actionFailed(_ title: String, _ message: String, banner: Bool) {
        if banner {
            reportFailure(title: title, message)
        } else {
            report(Self.failureLine(title: title, message: message))
        }
    }

    /// A launch, continue, or restart that failed after the menu closed: the
    /// menu line says why, and the operator hears of it at once, by a
    /// notification when app notifications are on and allowed, else by a
    /// beep and the icon blinking.
    func reportFailure(title: String, _ message: String) {
        let line = Self.failureLine(title: title, message: message)
        report(line)
        if notifications?.presentApp(title: title, body: message) != true {
            NSSound.beep()
            statusItem?.flash()
        }
    }

    /// A failure of something asked for in the workspace: an alert there,
    /// with amx's own words (`details`, whole, for Copy Details) and Start
    /// Daemon or Open Settings when either would fix it. With the workspace
    /// not in front, the menu line says it instead.
    func reportError(title: String, message: String, details: String = "") {
        guard workspaceIsKey, let workspace else { return report(Self.failureLine(title: title, message: message)) }
        Self.logger.notice("action: \(title, privacy: .public): \(message, privacy: .public)")
        store.show(notice: Self.failureLine(title: title, message: message))
        workspace.showError(title: title, message: message, details: details)
    }

    /// Open Settings, on an error that says amx was not found: Settings on
    /// Advanced, at the amx binary.
    func openAMXSettings() {
        showPreferences(nil)
        preferencesModel.amxBinarySettingsRequest += 1
    }

    static let launchFailedTitle = "Could not launch"

    /// The menu line while a launch or continue runs.
    nonisolated static func launchingNotice(_ name: String) -> String {
        name.isEmpty ? "Launching…" : "Launching \(name)…"
    }

    /// A launch or continue, as the quit confirmation names it.
    nonisolated static func launchingChange(_ name: String) -> String {
        name.isEmpty ? "launching a session" : "launching \(name)"
    }

    /// The menu line for a failure: the message, after the title unless the
    /// message already says it.
    nonisolated static func failureLine(title: String, message: String) -> String {
        if message.isEmpty { return title }
        if message.hasPrefix("Could not") { return message }
        return "\(title): \(message)"
    }
}
