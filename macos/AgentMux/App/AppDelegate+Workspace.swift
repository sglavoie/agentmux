import AppKit

// The workspace window: making it, what it asks the app (WorkspaceDelegate),
// the main menu's workspace items, and the review windows it opens.
extension AppDelegate {
    // MARK: Workspace

    /// Shows the workspace window, on `runtimeSessionID`'s terminal when one
    /// is named. An empty workspace offers launch and history directly.
    func showWorkspace(focusing runtimeSessionID: String? = nil) {
        let controller = workspaceController()
        refreshProjects()
        if let runtimeSessionID {
            controller.focus(runtimeSessionID: runtimeSessionID)
        } else {
            controller.show()
            // A restore under way, or an agent said to be gone, is a place.
            if !controller.state.rows.isEmpty { controller.showDashboardUnlessPlaced() }
        }
    }

    func workspaceController() -> WorkspaceWindowController {
        if let workspace { return workspace }
        let controller = WorkspaceWindowController(delegate: self)
        controller.confirmBeforeClosing = preferencesModel.preferences.confirmBeforeClosing
        controller.readConfirmation = { [weak self] done in self?.readWorkspaceConfirmation(done) }
        workspace = controller
        if !Self.isRunningTests {
            controller.onLayoutChanged = { WorkspaceRestoration.saveLayout($0) }
            controller.makeRestoredReview = { [weak self] directory in
                guard let self else { return nil }
                let model = self.makeChangesModel()
                model.onOpenFile = { [weak self] path in
                    self?.showWorkspaceView(WorkspaceUtilityView(kind: .editor, target: path, title: (path as NSString).lastPathComponent))
                }
                self.paneChanges = model
                model.open(directory: directory)
                return .init(view: WorkspaceReviewHost(rootView: WorkspaceChangesView(model: model)),
                             canClose: { !model.landing }, close: { model.close() }, directory: { model.changes?.root ?? directory })
            }
            let restoration = WorkspaceRestoration.load()
            // Before anything makes the window, which opens at these widths.
            controller.sidebarWidth = restoration.sidebarWidth.map { CGFloat($0) }
            controller.inspectorWidth = restoration.inspectorWidth.map { CGFloat($0) }
            controller.setSidebarCollapsed(restoration.sidebarCollapsed)
            controller.setInspectorTab(restoration.inspectorTab)
            controller.setInspectorCollapsed(!restoration.inspectorShown)
            controller.order = WorkspaceOrder.load()
        }
        // The terminals offer their shortcuts to the main menu first. Launch
        // has installed it already; a hosted test run has not.
        WorkspaceMenu.install(target: self)
        return controller
    }

    /// A click on a utility view's terminal notice: opens the workspace on
    /// that view, or where the workspace was when the view is gone. Only
    /// navigation: a view never counts toward attention.
    func showWorkspace(onView id: String) {
        let controller = workspaceController()
        controller.show()
        guard !controller.showView(id) else { return }
        Self.logger.notice("terminal notice from view \(id, privacy: .public): the view is gone")
        controller.showDashboardUnlessPlaced()
    }

    /// Shows one of the workspace's utility views, opening the window for it.
    func showWorkspaceView(_ view: WorkspaceUtilityView) {
        let controller = workspaceController()
        controller.show()
        controller.open(view)
    }

    /// A view a surface the workspace embeds asked for, forwarded by the
    /// daemon. Such a surface lives in the workspace window, so a request
    /// that arrives while the window is closed is from nothing on screen and
    /// is dropped rather than raising the app.
    func handle(_ request: WorkspaceViewRequest) {
        if request.raise, request.kind == WorkspaceViewRequest.sessionKind {
            // The operator's own click, by way of focus_session: unlike a
            // surface's request it opens the window.
            let controller = workspaceController()
            controller.show()
            controller.open(request)
            return
        }
        guard let workspace, workspace.isOpen else {
            Self.logger.notice("workspace view request (\(request.kind, privacy: .public)) with no workspace open; ignored")
            return
        }
        workspace.open(request)
    }

    func makeUtilityTerminal(_ view: WorkspaceUtilityView) -> Result<WorkspaceTerminal, WorkspaceTerminalProblem> {
        guard let configuration = utilityConfiguration(for: view) else {
            return .failure(WorkspaceTerminalProblem(message: AMXWording.unavailable(notFound: preferences.binaryMenuProblem)))
        }
        guard let runtime = terminalRuntime() else {
            return .failure(WorkspaceTerminalProblem(message: "The embedded terminal could not start. See the log for why."))
        }
        guard let surface = runtime.makeSurface(configuration) else {
            return .failure(WorkspaceTerminalProblem(message: "The terminal could not be created."))
        }
        surface.delegate = workspace
        return .success(WorkspaceTerminal(view: surface, close: { surface.close() }, needsConfirm: { surface.needsConfirmQuit }))
    }

    func makeTerminal(runtimeSessionID: String, fontSize: Float?) -> Result<WorkspaceTerminal, WorkspaceTerminalProblem> {
        guard let configuration = attachmentConfiguration(runtimeSessionID: runtimeSessionID, fontSize: fontSize) else {
            return .failure(WorkspaceTerminalProblem(message: AMXWording.unavailable(notFound: preferences.binaryMenuProblem)))
        }
        guard let runtime = terminalRuntime() else {
            return .failure(WorkspaceTerminalProblem(message: "The embedded terminal could not start. See the log for why."))
        }
        guard let surface = runtime.makeSurface(configuration) else {
            return .failure(WorkspaceTerminalProblem(message: "The terminal could not be created."))
        }
        surface.delegate = workspace
        return .success(WorkspaceTerminal(view: surface, close: { surface.close() }, fontSize: { surface.fontSize }))
    }

    func focusExternally(_ row: WorkspaceRow) {
        guard case let .kitty(socket, windowID) = row.target else { return }
        focus(MenuSessionRow(title: row.project, windowID: windowID, kittySocket: socket, toolTip: ""))
    }

    /// While the workspace is open the app is a regular one, with a Dock icon
    /// and a menu bar, so its window takes the keyboard like any other; with
    /// it closed the app is the menu bar accessory again.
    func workspaceVisibilityChanged(_ visible: Bool) {
        workspaceVisible = visible
        syncActivationPolicy()
        guard !Self.isRunningTests, !isTerminating else { return }
        WorkspaceRestoration.saveWindowOpen(visible)
    }

    /// Regular while the workspace or Settings is open, so either can be
    /// found again from the Dock and ⌘Tab once it falls behind another
    /// app's window; the menu bar accessory otherwise. The answer panels
    /// float above every window and need neither.
    func syncActivationPolicy() {
        NSApp.setActivationPolicy(Self.activationPolicy(workspace: workspaceVisible || historyVisible || changesVisible || runsVisible || activityVisible, settings: settingsVisible))
    }

    nonisolated static func activationPolicy(workspace: Bool, settings: Bool) -> NSApplication.ActivationPolicy {
        workspace || settings ? .regular : .accessory
    }

    func workspacePlaceChanged(_ place: WorkspaceRestoration.Place?) {
        guard !Self.isRunningTests, !isTerminating else { return }
        Self.logger.notice("workspace: now on \(place?.encoded ?? "nothing", privacy: .public)")
        WorkspaceRestoration.savePlace(place)
    }

    func workspaceConversationChanged(_ conversation: ConversationIdentity?) {
        guard !Self.isRunningTests, !isTerminating else { return }
        WorkspaceRestoration.saveConversation(conversation)
    }

    func workspaceSidebarCollapsedChanged(_ collapsed: Bool) {
        guard !Self.isRunningTests, !isTerminating else { return }
        WorkspaceRestoration.saveSidebarCollapsed(collapsed)
    }

    func workspaceFullScreenChanged(_ fullScreen: Bool) {
        guard !Self.isRunningTests, !isTerminating else { return }
        WorkspaceRestoration.saveFullScreen(fullScreen)
    }

    func workspaceInspectorChanged(collapsed: Bool, tab: WorkspaceInspectorTab) {
        guard !Self.isRunningTests, !isTerminating else { return }
        WorkspaceRestoration.saveInspector(shown: !collapsed, tab: tab)
    }

    func workspaceLayoutChanged(sidebarWidth: CGFloat?, inspectorWidth: CGFloat?) {
        guard !Self.isRunningTests, !isTerminating else { return }
        WorkspaceRestoration.saveWidths(sidebar: sidebarWidth.map(Double.init), inspector: inspectorWidth.map(Double.init))
    }

    /// The inspector's buttons do what ⌘R and the session function keys do.
    /// Answer… in the inspector: the panels the menu and the banners open.
    /// Whether anything may be sent is still each panel's to find out.
    func workspaceAnswer(_ answer: WorkspaceWaitingAnswer) {
        switch answer {
        case let .asl(target): self.answer(target)
        case let .prompt(runtimeSessionID): answerPrompt(PromptAnswerTarget(runtimeSessionID: runtimeSessionID))
        }
    }

    func workspaceInspectorAction(_ action: WorkspaceInspectorAction) {
        switch action {
        case .name: nameSession(nil)
        case .color: pickSessionColor(nil)
        case .icon: pickSessionIcon(nil)
        case .mute: toggleSessionMute(nil)
        case .clearState: clearSessionState(nil)
        case .markActive: markSessionActive(nil)
        case .reviewChanges: showChanges(nil)
        }
    }

    func workspaceOrderChanged(_ order: WorkspaceOrder) {
        guard !Self.isRunningTests, !isTerminating else { return }
        order.save()
    }

    /// Asks amx what the registry and the private tmux server say about a
    /// session. `amx attach --json` checks and reports; it attaches nothing.
    func sessionStatus(runtimeSessionID: String, completion: @escaping @MainActor (WorkspaceSessionStatus) -> Void) {
        guard let command else {
            // At launch the workspace can ask before amx is found. The
            // question waits for it instead of being answered "unknown".
            if let problem = preferences.binaryMenuProblem {
                completion(.unknown(problem))
            } else {
                statusQuestions.append((runtimeSessionID, completion))
            }
            return
        }
        amxRunner.run({ WorkspaceSessionStatus.from(command.json(AttachCheck.self, ["attach", runtimeSessionID])) },
                      onMain: { status in
            Self.logger.notice("session \(runtimeSessionID, privacy: .public): \(String(describing: status), privacy: .public)")
            completion(status)
        })
    }

    /// Ends a session's agent with `amx close --yes`. The workspace has
    /// already asked the operator when there was something to ask.
    func closeSession(runtimeSessionID: String, completion: @escaping @MainActor (String?) -> Void) {
        guard let command else {
            completion(AMXWording.unavailable(notFound: preferences.binaryMenuProblem))
            return
        }
        let change = beginChange("ending a session")
        controlRunner.run({
            SessionClose.problem(command.json(
                SessionClose.self, ["close", runtimeSessionID, "--yes"], timeout: AMXCommand.actionTimeout, mutates: true
            ))
        }, onMain: { [weak self] problem in
            self?.endChange(change)
            Self.logger.notice("close \(runtimeSessionID, privacy: .public): \(problem ?? "closed", privacy: .public)")
            completion(problem)
        })
    }

    func workspaceStartDaemon() {
        startDaemon()
    }

    func workspaceOpenAMXSettings() {
        openAMXSettings()
    }

    var workspaceDaemonIsDown: Bool { daemonIsDown }

    func workspaceTerminalNotification(_ notice: TerminalNotice, onScreen: Bool) {
        let snoozed = Self.terminalNoticesSnoozed(
            live: store.feed == .live ? store.snapshot.map { SnoozeState($0.notification) } : nil,
            mirrored: preferencesModel.preferences.snoozeUntil,
            now: Date()
        )
        let muted = Self.terminalNoticeMuted(notice.origin, in: store.sessions)
        let outcome = notifications?.presentTerminal(notice, snoozed: snoozed, muted: muted, onScreen: onScreen) ?? .unavailable
        if outcome != .shown {
            Self.logger.notice("terminal notification from \(notice.source, privacy: .public) not shown: \(String(describing: outcome), privacy: .public)")
        }
    }

    func workspaceShowing(_ origin: TerminalNotice.Origin) {
        notifications?.withdrawTerminal(origin)
    }

    /// Whether everything is snoozed, for a terminal notice: the daemon's
    /// snooze while the feed is live, else the one mirrored into
    /// `amxSnoozeUntil`.
    nonisolated static func terminalNoticesSnoozed(live: SnoozeState?, mirrored: Date?, now: Date) -> Bool {
        if let live { return live.isSnoozed(now: now) }
        guard let mirrored else { return false }
        return mirrored > now
    }

    /// Whether a terminal notice's session is muted. A view is never a
    /// session and is never muted.
    nonisolated static func terminalNoticeMuted(_ origin: TerminalNotice.Origin, in sessions: [CompanionSession]) -> Bool {
        guard case let .session(id) = origin else { return false }
        return session(runtimeSessionID: id, in: sessions)?.muted ?? false
    }

    /// The listed session the workspace embeds under a runtime session.
    nonisolated static func session(runtimeSessionID id: String, in sessions: [CompanionSession]) -> CompanionSession? {
        guard !id.isEmpty else { return nil }
        return sessions.first { $0.embeddedRuntimeSessionID == id }
    }

    /// Names a session with `amx name-session`; an empty name removes the
    /// one it has. The sidebar shows the change once the daemon has read it.
    func nameSession(runtimeSessionID: String, name: String, completion: @escaping @MainActor (String?) -> Void) {
        guard let command else {
            completion(AMXWording.unavailable(notFound: preferences.binaryMenuProblem))
            return
        }
        amxRunner.run({
            command.json(NameSessionResult.self, NameSessionResult.arguments(runtimeSessionID: runtimeSessionID, name: name))
        }, onMain: { result in
            switch result {
            case .success, .partial:
                Self.logger.notice("name-session \(runtimeSessionID, privacy: .public): done")
                completion(nil)
            case let .failure(error):
                Self.logger.error("name-session \(runtimeSessionID, privacy: .public): \(error.message, privacy: .public)")
                completion(error.message)
            }
        })
    }

    @objc func openWorkspace(_ sender: Any?) { showWorkspace() }
    @objc func nextAttention(_ sender: Any?) { focusNextAttention() }
    @objc func selectNextSession(_ sender: Any?) { workspace?.selectNeighbor(1) }
    @objc func selectPreviousSession(_ sender: Any?) { workspace?.selectNeighbor(-1) }
    @objc func selectNextProjectTab(_ sender: Any?) { workspace?.selectProjectTab(1) }
    @objc func selectPreviousProjectTab(_ sender: Any?) { workspace?.selectProjectTab(-1) }
    @objc func showNextSessionInKitty(_ sender: Any?) { workspace?.selectNeighbor(1, raising: true) }
    @objc func showPreviousSessionInKitty(_ sender: Any?) { workspace?.selectNeighbor(-1, raising: true) }
    /// ⌘1 to ⌘0: the item's tag is the session's place in the sidebar.
    @objc func goToSession(_ sender: Any?) {
        guard let item = sender as? NSMenuItem else { return }
        workspace?.selectSession(at: item.tag)
    }
    /// ⌘⇧1 to ⌘⇧0.
    @objc func showSessionInKitty(_ sender: Any?) {
        guard let item = sender as? NSMenuItem else { return }
        workspace?.selectSession(at: item.tag, raising: true)
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
    @objc func toggleWorkspaceSidebar(_ sender: Any?) { workspace?.toggleSidebar() }
    @objc func toggleWorkspaceInspector(_ sender: Any?) { workspace?.toggleInspector() }
    @objc func biggerWorkspaceSidebars(_ sender: Any?) { workspace?.zoomSidebars(.bigger) }
    @objc func smallerWorkspaceSidebars(_ sender: Any?) { workspace?.zoomSidebars(.smaller) }
    @objc func resetWorkspaceSidebars(_ sender: Any?) { workspace?.zoomSidebars(.reset) }
    @objc func toggleRecentPlace(_ sender: Any?) {
        if workspace?.toggleRecentPlace() != true { NSSound.beep() }
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

    func makeChangesModel() -> WorkspaceChangesModel {
        let model = WorkspaceChangesModel(command: { [weak self] in self?.command }, defaults: .standard, feedbackHistory: reviewFeedbackHistory)
        model.onFeedbackRecovery = { [weak self] directory in self?.inspectFeedbackCheckout(directory) }
        return model
    }

    func inspectSavedFeedback(_ destination: WorkspaceFeedbackDestination) {
        switch destination {
        case let .conversation(encoded):
            guard let identity = ConversationIdentity(encoded: encoded) else { return }
            openHistory(conversation: identity)
        case let .checkout(directory): inspectFeedbackCheckout(directory)
        case let .runs(rowID, runID):
            openRuns(launch: false)
            runsWindow?.model.inspectFeedback(rowID: rowID, runID: runID)
        case let .plan(saved):
            openRuns(launch: true)
            guard let model = runsWindow?.plan, !model.launching, !model.checking else { return }
            model.plan = saved.plan; model.project = saved.project; model.profile = saved.profile
            model.harness = saved.harness; model.reviewHarness = saved.reviewHarness; model.commitMode = saved.commitMode
            model.check()
        }
    }

    func inspectFeedbackCheckout(_ directory: String) {
        let controller = changesWindow ?? WorkspaceChangesWindow(model: makeChangesModel())
        guard !controller.model.landing else { controller.show(); return }
        trackVisibility(of: controller, \.changesVisible)
        controller.model.onOpenFile = { [weak self] path in
            self?.showWorkspaceView(WorkspaceUtilityView(kind: .editor, target: path, title: (path as NSString).lastPathComponent))
        }
        changesWindow = controller
        controller.model.open(directory: directory)
        controller.show()
    }

    @objc func showChanges(_ sender: Any?) {
        let workspace = workspaceController()
        workspace.show()
        let controller = changesWindow ?? WorkspaceChangesWindow(model: makeChangesModel())
        guard !controller.model.landing else { controller.show(); return }
        trackVisibility(of: controller, \.changesVisible)
        controller.model.onOpenFile = { [weak self] path in
            self?.showWorkspaceView(WorkspaceUtilityView(kind: .editor, target: path, title: (path as NSString).lastPathComponent))
        }
        if let id = workspace.shownSessionRow?.embeddedRuntimeSessionID {
            controller.model.open(runtimeSessionID: id)
        } else {
            let picker = NSOpenPanel()
            picker.canChooseFiles = false
            picker.canChooseDirectories = true
            picker.allowsMultipleSelection = false
            picker.message = "Choose the checkout to review. Changes include every session using this directory."
            picker.prompt = "Review"
            guard picker.runModal() == .OK, let url = picker.url else { return }
            controller.model.open(directory: url.path)
        }
        changesWindow = controller
        controller.show()
    }

    /// Every review window counts toward the activation policy while it is
    /// open, and closing one returns to the workspace, or wherever `onHide`
    /// says.
    func trackVisibility(of controller: ReviewWindowController, _ flag: ReferenceWritableKeyPath<AppDelegate, Bool>,
                                 onHide: ((AppDelegate) -> Void)? = nil) {
        controller.onVisibilityChanged = { [weak self] visible in
            guard let self else { return }
            self[keyPath: flag] = visible
            self.syncActivationPolicy()
            guard !visible else { return }
            if let onHide {
                onHide(self)
            } else if self.workspaceVisible {
                self.workspace?.window.makeKeyAndOrderFront(nil)
            }
        }
    }

    @objc func showRuns(_ sender: Any?) { openRuns(launch: false) }
    @objc func runPlan(_ sender: Any?) { openRuns(launch: true) }

    func openRuns(launch: Bool) {
        let controller = runsWindow ?? WorkspaceRunsWindow(
            model: WorkspaceRunsModel(command: { [weak self] in self?.command }, feedbackHistory: reviewFeedbackHistory, defaults: .standard),
            plan: WorkspaceRunPlanModel(command: { [weak self] in self?.command }, feedbackHistory: reviewFeedbackHistory))
        trackVisibility(of: controller, \.runsVisible)
        controller.model.onAnswer = { [weak self] target in self?.answer(target) }
        controller.model.onFocus = { [weak self] row in
            guard let self else { return }
            guard self.store.feed == .live,
                  let session = self.store.sessions.first(where: { $0.aslRunID == row.runID }) else {
                return self.refuse("No live session is associated with this ASL run in the current session list.")
            }
            self.focus(.focusing(session, title: row.displayLabel))
        }
        controller.plan.onStarted = { [weak self] id in
            self?.finishOpeningInWorkspace(.success(id), label: "start supervised plan", progress: "")
        }
        runsWindow = controller
        controller.show(launch: launch)
    }

    @objc func showActivity(_ sender: Any?) {
        let controller = activityWindow ?? WorkspaceActivityWindow(model: WorkspaceActivityModel(command: { [weak self] in self?.command }, defaults: .standard))
        trackVisibility(of: controller, \.activityVisible)
        controller.model.onConversation = { [weak self] identity, at in self?.openHistory(conversation: identity, eventTime: at) }
        controller.model.onFocus = { [weak self] entry in
            guard let self else { return }
            guard self.store.feed == .live else { return self.refuse("Waiting for a live session list.") }
            let live = Self.session(runtimeSessionID: entry.runtimeSessionID, in: self.store.sessions)
                ?? self.store.sessions.first { entry.conversation != nil && $0.conversation == entry.conversation }
            guard let live else { return self.refuse("No live session matches this recorded event. Open its conversation to inspect or resume it.") }
            self.focus(.focusing(live, title: entry.title))
        }
        activityWindow = controller
        controller.show()
    }

    @objc func showHistory(_ sender: Any?) { openHistory(conversation: nil) }

    @objc func searchConversation(_ sender: Any?) {
        guard workspaceIsKey, let row = workspace?.shownSessionRow, let identity = row.conversation else { return }
        conversationSearchSequence += 1
        let request = conversationSearchSequence
        guard !row.key.runtimeSessionID.isEmpty, let command else { openHistory(conversation: identity); return }
        listRunner.run({
            command.json(PromptLook.self, PromptLook.arguments(runtimeSessionID: row.key.runtimeSessionID) + ["--search-context"])
        }, onMain: { [weak self] result in
            guard let self, self.conversationSearchSequence == request, self.workspaceIsKey,
                  self.workspace?.shownSessionRow?.key == row.key,
                  self.workspace?.shownSessionRow?.conversation == identity else { return }
            let query = result.value?.search?.query.reveal() ?? ""
            let note: String
            if case .failure = result {
                note = "Terminal search could not be read. Search this saved conversation by typing a query."
            } else {
                note = "Searching saved conversation prose, including older messages. Words and quoted phrases are supported; terminal regular expressions are not used here."
            }
            self.openHistory(conversation: identity, query: query, searchNote: note)
        })
    }

    func workspaceResumeConversation(_ identity: ConversationIdentity) {
        let workspace = workspaceController()
        guard workspace.window.attachedSheet == nil else { return }
        let model = WorkspaceResumeModel(identity: identity, command: { [weak self] in self?.command }, feedbackHistory: reviewFeedbackHistory)
        model.onResume = { [weak self] conversation, done in
            guard let self else { done("The workspace is unavailable."); return }
            self.openHistoryConversation(conversation, fork: false, completion: done)
        }
        let panel = WorkspaceResumePanel(model: model)
        resumePanel = panel
        panel.show(on: workspace.window)
    }

    func workspaceSearchConversation() { searchConversation(nil) }

    func openHistory(conversation: ConversationIdentity?, eventTime: String? = nil, query: String = "", searchNote: String? = nil) {
        conversationSearchSequence += 1
        let workspace = workspaceController()
        let returnResponder: NSResponder? = searchNote == nil ? workspace.window.firstResponder : workspace.shownTerminalView
        workspace.show()
        let controller = historyWindow ?? WorkspaceHistoryWindow(model: WorkspaceHistoryModel(command: { [weak self] in self?.command }, defaults: .standard, feedbackHistory: reviewFeedbackHistory))
        trackVisibility(of: controller, \.historyVisible) { [weak returnResponder] app in
            if eventTime != nil, let activity = app.activityWindow?.window, activity.isVisible {
                activity.makeKeyAndOrderFront(nil)
            } else if app.workspaceVisible {
                app.workspace?.window.makeKeyAndOrderFront(nil)
                if let view = returnResponder as? NSView, view.window === app.workspace?.window,
                   !view.isHiddenOrHasHiddenAncestor { app.workspace?.window.makeFirstResponder(view) }
            }
        }
        controller.model.onAdvanced = { [weak self] identity, query in
            var view = WorkspaceUtilityView(kind: .history, target: identity.encoded, title: "Conversation Actions")
            view.historyQuery = query
            self?.showWorkspaceView(view)
        }
        controller.model.onAction = { [weak self] conversation, fork, completion in
            self?.openHistoryConversation(conversation, fork: fork, completion: completion)
        }
        controller.model.liveConversations = Set(store.feed == .live ? store.sessions.compactMap { $0.conversation?.encoded } : [])
        historyWindow = controller
        controller.model.searchEntryNote = searchNote
        if conversation != nil {
            controller.model.query = query
            controller.model.scope = .conversation
            controller.model.includeAutomated = true
        }
        controller.show(project: controller.model.project.isEmpty ? (workspace.shownSessionRow?.project ?? "") : controller.model.project,
                        conversation: conversation ?? controller.model.currentConversation ?? workspace.shownSessionRow?.conversation, eventTime: eventTime)
    }

    func openHistoryConversation(_ conversation: RecentConversation, fork: Bool, completion: @escaping (String?) -> Void) {
        if !fork, store.feed == .live, let live = store.sessions.first(where: { $0.conversation == conversation.identity }) {
            focus(.focusing(live, title: conversation.displayName))
            completion(nil)
            return
        }
        guard let command else { completion(AMXWording.unavailable(notFound: preferences.binaryMenuProblem)); return }
        if let reason = gate.unavailableReason(.sessions) { completion(reason); return }
        let args = ["session", fork ? "fork" : "continue", "--backend", "tmux", "--json", "--", conversation.selector]
        launchRunner.run({
            command.json(SessionContinueResult.self, args, timeout: AMXCommand.launchTimeout, mutates: true)
        }, onMain: { [weak self] result in
            switch result {
            case let .failure(error): completion(error.message)
            case let .success(value):
                self?.finishOpeningInWorkspace(.success(value.runtimeSessionID), label: fork ? "fork conversation" : "resume conversation", progress: "")
                completion(nil)
            case let .partial(value, _):
                self?.finishOpeningInWorkspace(.success(value.runtimeSessionID), label: "open conversation", progress: "")
                completion("The conversation changed, but opening it did not fully complete. Check the workspace before retrying.")
            }
        })
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
            entry(.newShell, "New Shell", "⌘T"),
            entry(.splitShell, "Split with Shell", reason: workspace?.splitUnavailableReason ?? (workspace?.canOpenTerminalBesideSession == true ? nil : "Select an embedded session first")),
            entry(.splitChanges, "Split with Checkout Changes", reason: workspace?.splitUnavailableReason ?? (workspace?.shownSessionRow?.embeddedRuntimeSessionID != nil ? nil : "Select an embedded session first")),
            entry(.focusReview, "Focus Checkout Review", reason: workspace?.hasReview == true ? nil : "Open checkout review in a pane first"),
            entry(.focusOtherPane, "Focus Other Pane", reason: workspace?.isSplit == true ? nil : "Open a second pane first"),
            entry(.swapPanes, "Swap Panes", reason: workspace?.isSplit == true ? nil : "Open a second pane first"),
            entry(.collapsePane, "Hide Other Pane", reason: workspace?.isSplit == true ? (workspace?.canHideOtherPane == true ? nil : "Wait for the checkout operation to finish") : "Open a second pane first"),
            entry(.activity, "Since Last Looked", key(.activity)),
            entry(.runs, "Open ASL Runs", key(.runs)),
            entry(.runPlan, "Run a Plan…", key(.runPlan), reason: command == nil ? AMXWording.unavailable(notFound: preferences.binaryMenuProblem) : nil),
            entry(.changes, "Checkout Changes", key(.changes)),
            entry(.dashboard, "Open Dashboard", "⌘D"),
            entry(.overview, "Open Overview", "⌘O"),
            entry(.pastSessions, "Find or Resume a Conversation", "⌘Y"),
            entry(.nextWaiting, "Next Waiting Session", "⌘⇧A", reason: store.feed != .live ? "Waiting for a live session list" : (AttentionCycler.ordered(store.sessions).isEmpty ? "No sessions are waiting" : nil)),
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
            let live = store.feed == .live && store.sessions.contains { $0.conversation == conversation.identity }
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
        case .newShell: newShellFromMenu(nil)
        case .dashboard: openDashboardFromMenu(nil)
        case .overview: openOverviewFromMenu(nil)
        case .pastSessions: openPastSessionsFromMenu(nil)
        case .nextWaiting: nextAttention(nil)
        case .settings: showPreferences(nil)
        case .activity: showActivity(nil)
        case .history: showHistory(nil)
        case .runs: showRuns(nil)
        case .runPlan: runPlan(nil)
        case .changes: showChanges(nil)
        }
    }

}
