import AppKit

// What the menu, the shortcuts, and the banners ask for: focusing, launching,
// continuing, muting, and the answer panels, and how a failure is reported.
extension AppDelegate {
    // MARK: Focus

    /// Focuses a tmux session in the workspace.
    func focus(_ row: MenuSessionRow) {
        guard !row.runtimeSessionID.isEmpty else { return report("This session is no longer available.") }
        focusSession(row.runtimeSessionID)
    }

    /// Shows a runtime session. The daemon is asked
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
        let question = workspace?.allWaitingRows.first {
            if case let .asl(answer)? = $0.answer { return answer.runID == target.runID }
            return false
        }
        controller.flow.onAnswerSent = { [weak self] run in self?.workspaceAnswerSent(runID: run, question: question) }
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

    func workspaceAnswerSent(runtimeSessionID: String? = nil, runID: String? = nil, question: WorkspaceWaitingRow? = nil) {
        if let session = store.managedSessions.first(where: {
            (runtimeSessionID != nil && $0.embeddedRuntimeSessionID == runtimeSessionID)
                || (runID?.isEmpty == false && $0.aslRunID == runID)
        }) {
            cycler.focused(session, in: store.managedSessions, order: attentionOrder,
                           grouping: preferencesModel.preferences.sidebarGrouping, sorting: preferencesModel.preferences.sidebarSort)
        }
        guard let workspace, workspace.isOpen else { return }
        if let row = workspace.allWaitingRows.first(where: { row in
            guard row.answer != nil, question?.sameContext(as: row) == true else { return false }
            if let runtimeSessionID { return row.key.runtimeSessionID == runtimeSessionID }
            if case let .asl(target)? = row.answer { return target.runID == runID }
            return false
        }) { workspace.inspector.markAnswerSent(row) }
        workspace.welcome.pendingAnswers = workspace.inspector.sentAnswers.count
        workspace.welcome.answerSent()
        workspace.showNotice("Answer sent")
    }

    // MARK: Actions

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
            launchBackend: prefs.launchBackend,
            cliLaunchBackend: preferences.backend.cliLaunchBackend,
            cliLaunchBackendSource: preferences.backend.cliLaunchBackendSource,
            // Only a shortcut the system took: one refused, taken by another
            // app, or set aside while a recorder listens does nothing.
            dashboardHotKey: dashboardHotKey?.registration.registeredCombo,
            nextAttentionHotKey: attentionHotKey?.registration.registeredCombo,
            feedLive: live,
            waiting: live ? AttentionCycler.ordered(store.managedSessions).count : 0,
            projects: projects.state,
            projectGroup: workspace?.selectedGroup,
            sidebarGrouping: prefs.sidebarGrouping,
            sidebarSort: prefs.sidebarSort,
            missingProjectDirectories: projects.missingDirectories,
            recent: recent.state,
            recentDays: prefs.recentDays,
            recentLimit: prefs.recentLimit,
            liveConversations: store.managedSessions.compactMap(\.conversation).filter { !$0.sessionID.isEmpty },
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
            self.refreshBriefing()
            if let missing { self.projects.missingDirectories = missing }
            if self.projects.state.errorCode == "config_missing", !self.hasOfferedInitialSetup {
                self.hasOfferedInitialSetup = true
                self.showWorkspace()
            }
            self.workspace?.reloadSidebar(allowReorder: true)
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
                    return self.reportFailure(title: Self.launchFailedTitle, "amx did not report a launch destination. Check amx backend status or choose Workspace in Settings.")
                }
                completion(choice)
            case let .failure(error):
                self.reportFailure(title: Self.launchFailedTitle, "Could not read the CLI launch destination: \(error.message)")
            }
        })
    }

    /// Launch Project starts the project on tmux and shows it in the workspace.
    /// A directory replaces the project's configured one for this launch.
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
        launchInWorkspace(project, directory: directory, command: command)
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
        case let .success(runtimeSessionID):
            Self.logger.notice("\(label, privacy: .public) on tmux as \(runtimeSessionID, privacy: .public)")
            showInWorkspace(runtimeSessionID)
        case let .partial(runtimeSessionID, status):
            Self.logger.notice("\(label, privacy: .public) on tmux as \(runtimeSessionID, privacy: .public) exited \(status)")
            report("\(label) finished, but a later step failed.")
            showInWorkspace(runtimeSessionID)
        }
    }

    private func showInWorkspace(_ runtimeSessionID: String) {
        let controller = workspaceController()
        controller.show()
        guard !runtimeSessionID.isEmpty else { return }
        controller.open(WorkspaceViewRequest(
            kind: WorkspaceViewRequest.sessionKind, runtimeSessionID: runtimeSessionID, awaited: true
        ))
    }

    /// Recent focuses a live conversation or resumes it on tmux in the workspace.
    func continueConversation(_ conversation: RecentConversation, destination: LaunchBackendPreference? = nil) {
        if let live = store.managedSessions.first(where: { $0.conversation == conversation.identity }) {
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
        let arguments = RecentSessions.workspaceContinueArguments(for: conversation)
        launchRunner.run({
            command.json(SessionContinueResult.self, arguments, timeout: AMXCommand.launchTimeout, mutates: true)
        }, onMain: { [weak self] result in
            self?.endChange(change)
            self?.finishOpeningInWorkspace(result.map(\.runtimeSessionID), label: label, progress: progress)
        })
    }

    /// ⌥ on a session row toggles mute on its runtime session.
    func toggleMute(_ row: MenuSessionRow) { toggleMute(row, banner: false) }

    /// `banner`: a notification's Mute, whose failures are reported where
    /// the operator sees them now rather than only on the menu line.
    func toggleMute(_ row: MenuSessionRow, banner: Bool) {
        guard command != nil else {
            return actionFailed(Self.muteFailedTitle, AMXWording.unavailable(notFound: preferences.binaryMenuProblem), banner: banner)
        }
        if let reason = gate.unavailableReason(.muteToggle) { return actionFailed(Self.muteFailedTitle, reason, banner: banner) }
        guard !row.runtimeSessionID.isEmpty else {
            return actionFailed(Self.muteFailedTitle, "This session is no longer available.", banner: banner)
        }
        toggleMute(runtimeSessionID: row.runtimeSessionID, banner: banner)
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
    /// notification when app notifications are on and allowed, else (or
    /// when the notification could not be added) by a beep and the icon
    /// blinking.
    func reportFailure(title: String, _ message: String) {
        let line = Self.failureLine(title: title, message: message)
        report(line)
        let fallBack = { [weak self] in
            NSSound.beep()
            self?.statusItem?.flash()
        }
        if notifications?.presentApp(title: title, body: message, failed: fallBack) != true {
            fallBack()
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
