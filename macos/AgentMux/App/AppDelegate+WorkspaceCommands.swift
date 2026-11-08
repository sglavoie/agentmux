import AppKit

extension AppDelegate {
    @objc func openWorkspace(_ sender: Any?) { showWorkspace() }
    @objc func nextAttention(_ sender: Any?) { focusNextAttention() }
    @objc func selectNextSession(_ sender: Any?) { followingFocus { $0.selectNeighbor(1) } }
    @objc func selectPreviousSession(_ sender: Any?) { followingFocus { $0.selectNeighbor(-1) } }
    @objc func selectNextProjectTab(_ sender: Any?) { followingFocus { $0.selectProjectTab(1) } }
    @objc func selectPreviousProjectTab(_ sender: Any?) { followingFocus { $0.selectProjectTab(-1) } }
    /// ⌘1 to ⌘0: the item's tag is the session's place in the sidebar.
    @objc func goToSession(_ sender: Any?) {
        guard let item = sender as? NSMenuItem else { return }
        followingFocus { $0.selectSession(at: item.tag) }
    }

    /// A key that moves the pane to another session or terminal reveals
    /// where it went, as ⌘J does, so the sidebar follows the keyboard. A
    /// key that went nowhere reveals nothing.
    func followingFocus(_ step: (WorkspaceWindowController) -> Void) {
        guard let workspace else { return }
        let before = workspace.place
        step(workspace)
        if workspace.place != before { workspace.revealInSidebar() }
    }
    /// The workspace's own Dashboard: the main menu's, as opposed to the
    /// global shortcut, which goes where `amxDashboardPlace` says.
    @objc func openDashboardFromMenu(_ sender: Any?) { showWorkspaceView(.dashboard) }
    @objc func openOverviewFromMenu(_ sender: Any?) { showWorkspaceView(.overview) }
    /// ⌘Y: Past Sessions over every conversation, with no Dashboard behind it.
    @objc func openPastSessionsFromMenu(_ sender: Any?) { openHistory(conversation: nil) }
    @objc func newShellFromMenu(_ sender: Any?) {
        let home = command?.launchEnvironment.home ?? NSHomeDirectory()
        let controller = workspaceController()
        controller.show()
        controller.openNewShell(in: home)
    }
    /// ⌘↩: a shell beside the session the workspace is on.
    @objc func newSessionTerminal(_ sender: Any?) {
        guard let workspace, workspace.shownSessionRow != nil else { return refuse(Self.noSessionShown) }
        if !workspace.openTerminalBesideSession() { refuse(WorkspaceWindowController.terminalNeedsEmbedded) }
    }

    @objc func newSessionBelow(_ sender: Any?) {
        guard let row = workspace?.shownSessionRow else { return refuse(Self.noSessionShown) }
        workspaceNewSession(below: row)
    }

    func workspaceNewSession(below row: WorkspaceRow) {
        guard row.embeddedRuntimeSessionID != nil, let workspace else { return }
        if let reason = gate.unavailableReason(.project) { return refuse(reason) }
        guard workspace.window.attachedSheet == nil else { return }
        let model = WorkspaceNewSessionModel(source: row, command: { [weak self] in self?.command })
        let panel = WorkspaceNewSessionPanel(model: model)
        newSessionPanel = panel
        model.onLaunched = { [weak self, weak panel, weak workspace] id in
            panel?.close()
            self?.newSessionPanel = nil
            workspace?.openNewSession(id, below: row.key)
        }
        panel.show(on: workspace.window)
    }
    /// ⌘W: closes what the workspace's pane is on, as the × on its row does,
    /// and never the workspace itself. Another window that has the keyboard,
    /// such as Preferences, closes as it would.
    @objc func closeWorkspaceView(_ sender: Any?) {
        if let key = NSApp.keyWindow, workspace?.isOpen != true || key !== workspace?.window {
            key.performClose(sender)
            return
        }
        if workspace?.closeShown() != true { NSSound.beep() }
    }
    /// What ⌘W closes: a view, a session attachment, or another window.
    var closeWorkspaceViewTitle: String {
        guard let workspace, workspace.isOpen, let key = NSApp.keyWindow, key === workspace.window else {
            return WorkspaceWindowController.closeTitle
        }
        return workspace.closeShownTitle
    }
    @objc func endWorkspaceSession(_ sender: Any?) {
        guard let workspace, let row = workspace.shownSessionRow, row.embeddedRuntimeSessionID != nil else { return refuse(Self.noSessionShown) }
        workspace.closeSession(row.key)
    }
    /// ⌘R: names the session or terminal the workspace's pane is on.
    @objc func nameSession(_ sender: Any?) {
        guard let workspace else { return refuse(Self.noSessionShown) }
        if !workspace.nameShownSession() { refuse(WorkspaceWindowController.namingNeedsEmbedded) }
    }
    /// Pins the session the workspace's pane is on above every project, or
    /// unpins it.
    @objc func togglePinSession(_ sender: Any?) {
        if workspace?.togglePinOfShownSession() != true { refuse(Self.noSessionShown) }
    }
    @objc func toggleSessionAccounted(_ sender: Any?) {
        guard workspaceIsKey else { return }
        workspace?.toggleShownSessionAccounted()
    }
    @objc func toggleWorkspaceSidebar(_ sender: Any?) { workspace?.toggleSidebar() }
    @objc func toggleWorkspaceInspector(_ sender: Any?) { workspace?.toggleInspector() }
    @objc func revealInWorkspaceSidebar(_ sender: Any?) {
        if workspace?.revealInSidebar() != true { NSSound.beep() }
    }
    @objc func biggerWorkspaceSidebars(_ sender: Any?) { workspace?.zoomSidebars(.bigger) }
    @objc func smallerWorkspaceSidebars(_ sender: Any?) { workspace?.zoomSidebars(.smaller) }
    @objc func resetWorkspaceSidebars(_ sender: Any?) { workspace?.zoomSidebars(.reset) }
    @objc func toggleRecentPlace(_ sender: Any?) {
        var moved = false
        followingFocus { moved = $0.toggleRecentPlace() }
        if !moved { NSSound.beep() }
    }
    /// ⌘P: the list of sessions and views to go to by name.
    @objc func showSwitcher(_ sender: Any?) {
        refreshProjects()
        refreshRecent()
        workspace?.toggleSwitcher()
    }

    @objc func showCommandPalette(_ sender: Any?) {
        refreshProjects()
        refreshRecent()
        workspace?.toggleSwitcher(actions: true)
    }

    @objc func splitWorkspaceShell(_ sender: Any?) {
        if workspace?.splitWithShell() != true { refuse("Select an embedded session to split with a shell.") }
    }

    @objc func focusOtherWorkspacePane(_ sender: Any?) { _ = workspace?.focusOtherPane() }
    @objc func swapWorkspacePanes(_ sender: Any?) { workspace?.swapPanes() }
    @objc func collapseWorkspacePane(_ sender: Any?) { workspace?.collapseOtherPane() }

    @objc func splitWorkspaceChanges(_ sender: Any?) {
        guard let workspace, let id = workspace.shownSessionRow?.embeddedRuntimeSessionID else {
            return refuse("Select an embedded session to review its checkout alongside it.")
        }
        guard paneChanges?.landing != true else { return refuse("Wait for the worktree landing to finish.") }
        let model = makeChangesModel()
        model.onOpenFile = { [weak self] path in
            self?.showWorkspaceView(WorkspaceUtilityView(kind: .editor, target: path, title: (path as NSString).lastPathComponent))
        }
        let view = WorkspaceReviewHost(rootView: WorkspaceChangesView(model: model))
        guard workspace.splitWithReview(view, canClose: { !model.landing }, onClose: { model.close() }) else { return }
        paneChanges = model
        workspace.reviewDirectory = { model.changes?.root }
        model.open(runtimeSessionID: id)
    }

    var workspaceCommands: [WorkspaceSwitcher.Entry] {
        func entry(_ command: WorkspaceCommand, _ title: String, _ detail: String = "", reason: String? = nil) -> WorkspaceSwitcher.Entry {
            WorkspaceSwitcher.Entry(place: .command(command), glyph: "›", title: title, detail: detail, unavailableReason: reason)
        }
        /// The key the command has now, as Settings may have changed it.
        func key(_ shortcut: WorkspaceShortcut) -> String {
            shortcut.combo(in: preferencesModel.preferences)?.displayString ?? ""
        }
        var entries = [
            entry(.newTerminal, "New Terminal Here", "⌘↩", reason: workspace?.canOpenTerminalBesideSession == true ? nil : "Select an embedded session first"),
            entry(.handoff, WorkspaceHandoffModel.title, reason: gate.unavailableReason(.sessions) ?? (workspace?.shownSessionRow?.conversation != nil ? nil : "Select a session with a conversation first")),
            entry(.newSession, WorkspaceWindowController.newSessionTitle, "⌘⇧↩", reason: gate.unavailableReason(.project) ?? (workspace?.canOpenTerminalBesideSession == true ? nil : "Select an embedded session first")),
            entry(.newShell, "New Shell", "⌘T"),
            entry(.splitShell, WorkspaceShortcut.splitShell.title, reason: workspace?.splitUnavailableReason ?? (workspace?.canOpenTerminalBesideSession == true ? nil : "Select an embedded session first")),
            entry(.splitChanges, WorkspaceShortcut.splitChanges.title, reason: workspace?.splitUnavailableReason ?? (workspace?.shownSessionRow?.embeddedRuntimeSessionID != nil ? nil : "Select an embedded session first")),
            entry(.focusReview, "Focus Checkout Review", reason: workspace?.hasReview == true ? nil : "Open checkout review in a pane first"),
            entry(.focusOtherPane, WorkspaceShortcut.focusOtherPane.title, reason: workspace?.isSplit == true ? nil : "Open a second pane first"),
            entry(.swapPanes, WorkspaceShortcut.swapPanes.title, reason: workspace?.isSplit == true ? nil : "Open a second pane first"),
            entry(.collapsePane, WorkspaceShortcut.hideOtherPane.title, reason: workspace?.isSplit == true ? (workspace?.canHideOtherPane == true ? nil : "Wait for the checkout operation to finish") : "Open a second pane first"),
            entry(.activity, WorkspaceShortcut.activity.title, key(.activity)),
            entry(.flightLog, WorkspaceShortcut.flightLog.title, key(.flightLog)),
            entry(.briefing, WorkspaceShortcut.briefing.title, key(.briefing)),
            entry(.runs, WorkspaceShortcut.runs.title, key(.runs)),
            entry(.runPlan, WorkspaceShortcut.runPlan.title, key(.runPlan), reason: command == nil ? AMXWording.unavailable(notFound: preferences.binaryMenuProblem) : nil),
            entry(.changes, WorkspaceShortcut.changes.title, key(.changes)),
            entry(.dashboard, "Open Dashboard", "⌘D"),
            entry(.overview, "Open Overview", "⌘O"),
            entry(.pastSessions, "Find or Resume a Conversation", "⌘Y"),
            entry(.nextWaiting, AttentionCycler.commandTitle, "⌘⇧A", reason: store.feed != .live ? "Waiting for a live session list" : (AttentionCycler.ordered(store.managedSessions).isEmpty ? "No sessions are waiting" : nil)),
            entry(.settings, "Settings…", "⌘,"),
        ]
        entries += (workspace?.state.rows ?? []).filter { $0.embeddedRuntimeSessionID != nil }.map { row in
            entry(.splitSession(row.key), "Split with \(row.displayName)", row.project,
                  reason: workspace?.splitUnavailableReason ?? (workspace?.state.selection == row.key ? "Already in the active pane" : nil))
        }
        entries += (projects.state.value ?? []).map { project in
            entry(.launch(project), "Launch \(project.title)", "\(project.profile) · \(project.directory)",
                  reason: gate.unavailableReason(.project) ?? (projects.missingDirectories.contains(project.directory) ? "Project directory is missing" : nil))
        }
        entries += (recent.state.value ?? []).map { conversation in
            let live = store.feed == .live && store.managedSessions.contains { $0.conversation == conversation.identity }
            return entry(.resume(conversation), "\(live ? "Focus" : "Resume") \(conversation.displayName)",
                         "\(conversation.harness) · \(conversation.snippet)", reason: gate.unavailableReason(.sessions))
        }
        return entries
    }

    var workspaceLaunchDescription: String {
        let choice = preferencesModel.preferences.launchBackend
        return "Launch in \(choice.destination(cliBackend: preferences.backend.cliLaunchBackend)) · \(choice.source(cliSource: preferences.backend.cliLaunchBackendSource))"
    }

    var workspaceOffersSetup: Bool {
        ProjectCatalog.offersSetup(projects.state, unavailable: gate.unavailableReason(.project))
    }

    func workspaceSetupFinished() {
        refreshProjects(force: true)
        refreshBackendStatus()
    }

    var workspaceProjectGuidance: String {
        ProjectCatalog.guidance(projects.state, unavailable: gate.unavailableReason(.project))
    }

    var workspaceProjects: [ProjectEntry] { projects.state.value ?? [] }
    var workspaceProjectsLoaded: Bool { projects.state.value != nil && projects.state.error == nil }

    func workspaceGroupChanged(_ group: String?) {
        if !Self.isRunningTests, !isTerminating { WorkspaceRestoration.saveGroup(group) }
        updateWorkspaceProjectMenu()
        statusItem?.refreshSubmenus()
    }

    func workspaceLaunchProjects(from view: NSView) {
        refreshProjects()
        let menu = NSMenu(title: "Launch Project")
        menu.autoenablesItems = false
        workspaceProjectMenu = menu
        updateWorkspaceProjectMenu()
        menu.popUp(positioning: nil, at: NSPoint(x: 0, y: view.bounds.height), in: view)
        workspaceProjectMenu = nil
    }

    func updateWorkspaceProjectMenu() {
        guard let menu = workspaceProjectMenu else { return }
        menu.removeAllItems()
        let entries = gate.unavailableReason(.project).map { [MenuEntry.info($0), .action(.launchSettings, "Launch Settings…")] }
            ?? MenuBuilder.entries(for: .projects, actionsInput)
        MenuBuilder.items(for: entries, target: self, sessionAction: #selector(workspaceLaunchChosen(_:)),
                          actionSelector: #selector(workspaceLaunchChosen(_:))).forEach(menu.addItem)
    }

    @objc func workspaceLaunchChosen(_ sender: NSMenuItem) {
        guard let box = sender.representedObject as? MenuBuilder.ActionBox else { return }
        perform(box.action)
    }

    func workspaceContext(_ row: WorkspaceWaitingRow, completion: @escaping (WorkspaceWaitingContext.Result) -> Void) {
        guard store.feed == .live else { completion(.unavailable(SecretText("Waiting for a live session list."))); return }
        let command = self.command
        let unavailable = AMXWording.unavailable(notFound: preferences.binaryMenuProblem)
        listRunner.run({
            WorkspaceWaitingContext.read(row, asl: DaemonClient()) { id in
                guard let command else {
                    return .failure(.launchFailed(binary: "amx", reason: unavailable))
                }
                return command.json(PromptLook.self, PromptLook.arguments(runtimeSessionID: id))
            }
        }, then: completion)
    }

    func workspacePerform(_ action: WorkspaceCommand) {
        // Entries may have changed while the palette was open.
        guard let entry = workspaceCommands.first(where: { $0.place == .command(action) }) else { return }
        if let reason = entry.unavailableReason { return refuse(reason) }
        switch action {
        case let .splitSession(key): _ = workspace?.split(with: key)
        case .splitShell: splitWorkspaceShell(nil)
        case .splitChanges: splitWorkspaceChanges(nil)
        case .focusOtherPane: focusOtherWorkspacePane(nil)
        case .focusReview: _ = workspace?.focusReview()
        case .swapPanes: swapWorkspacePanes(nil)
        case .collapsePane: collapseWorkspacePane(nil)
        case let .launch(project): launch(project)
        case let .resume(conversation): continueConversation(conversation)
        case .newTerminal: newSessionTerminal(nil)
        case .handoff: continueWithAnotherAgent(nil)
        case .newSession: newSessionBelow(nil)
        case .newShell: newShellFromMenu(nil)
        case .dashboard: openDashboardFromMenu(nil)
        case .overview: openOverviewFromMenu(nil)
        case .pastSessions: openPastSessionsFromMenu(nil)
        case .nextWaiting: nextAttention(nil)
        case .settings: showPreferences(nil)
        case .activity: showActivity(nil)
        case .flightLog: showFlightLog(nil)
        case .briefing: showBriefing(nil)
        case .history: showHistory(nil)
        case .runs: showRuns(nil)
        case .runPlan: runPlan(nil)
        case .changes: showChanges(nil)
        }
    }
}
