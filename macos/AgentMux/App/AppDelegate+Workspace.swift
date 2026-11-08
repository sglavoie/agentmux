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
            revealIfAsked(runtimeSessionID, in: controller)
        } else {
            controller.show()
            // A restore under way, or an agent said to be gone, is a place.
            if !controller.state.rows.isEmpty { controller.showDashboardUnlessPlaced() }
        }
    }

    /// Reveals the session Next Waiting Session went to once the workspace
    /// shows it. Any other session shown from outside drops the request.
    private func revealIfAsked(_ runtimeSessionID: String, in controller: WorkspaceWindowController) {
        guard let wanted = revealOnShow else { return }
        revealOnShow = nil
        if wanted == runtimeSessionID { controller.revealInSidebar() }
    }

    func workspaceController() -> WorkspaceWindowController {
        if let workspace { return workspace }
        let controller = WorkspaceWindowController(delegate: self)
        controller.confirmBeforeClosing = preferencesModel.preferences.confirmBeforeClosing
        controller.welcome.isEnabled = preferencesModel.preferences.showWelcomeBack
        controller.setSidebarZoom(percent: preferencesModel.preferences.sidebarZoomPercent)
        controller.setCompactSidebar(preferencesModel.preferences.compactSidebar)
        controller.setShowOverviewInSidebar(preferencesModel.preferences.showOverviewInSidebar)
        controller.setSidebarGrouping(preferencesModel.preferences.sidebarGrouping)
        controller.setSidebarSort(preferencesModel.preferences.sidebarSort)
        controller.onSidebarZoomChanged = { [weak self] percent in
            self?.preferencesModel.preferences.sidebarZoomPercent = percent
        }
        controller.readConfirmation = { [weak self] done in self?.readWorkspaceConfirmation(done) }
        workspace = controller
        if !Self.isRunningTests {
            controller.onLayoutChanged = { WorkspaceRestoration.saveLayout($0) }
            controller.onGroupContextsChanged = { WorkspaceRestoration.saveGroupContexts($0) }
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
            controller.restoreContext(restoration)
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
            revealIfAsked(request.runtimeSessionID, in: controller)
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
        let previous = NSApp.activationPolicy()
        NSApp.setActivationPolicy(Self.activationPolicy(workspace: workspaceVisible || historyVisible || changesVisible || runsVisible || activityVisible || flightLogVisible || briefingVisible, settings: settingsVisible))
        if previous != .regular, NSApp.activationPolicy() == .regular {
            // AppKit keeps a badge set while accessory, but does not publish
            // it when the Dock icon appears. Reassigning the same string is
            // a no-op: clear it first so the Dock receives the current count.
            refreshDockBadge(force: true)
        }
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
        let muted = Self.terminalNoticeMuted(notice.origin, in: store.managedSessions)
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

    /// Pins a listed session with `amx pin-session`, or unpins it. The pin
    /// is the ★ in the session's title, so the dashboard lists it the same
    /// way. Like a mute, it is shown at once, so a second click before the
    /// daemon publishes it does not undo the first.
    func workspaceSetPinned(_ row: WorkspaceRow, _ pinned: Bool) {
        guard let command else {
            return actionFailed(Self.pinFailedTitle, AMXWording.unavailable(notFound: preferences.binaryMenuProblem), banner: false)
        }
        let key = row.key
        let arguments: [String]
        switch row.target {
        case let .embedded(id):
            arguments = PinSessionResult.arguments(runtimeSessionID: id, pinned: pinned)
        }
        store.expectPin(key, pinned: pinned)
        amxRunner.run({ command.json(PinSessionResult.self, arguments) }, onMain: { [weak self] result in
            guard let self else { return }
            switch result {
            case let .success(value), let .partial(value, _):
                Self.logger.notice("pin-session \(arguments.dropFirst().joined(separator: " "), privacy: .public): pinned=\(value.pinned)")
                // A window that is not a managed session changes nothing.
                if value.pinned == pinned {
                    self.store.expectPin(key, pinned: value.pinned)
                } else {
                    self.store.dropExpectedPin(key)
                }
            case let .failure(error):
                Self.logger.error("pin-session \(arguments.dropFirst().joined(separator: " "), privacy: .public): \(error.message, privacy: .public)")
                self.store.dropExpectedPin(key)
                self.actionFailed(Self.pinFailedTitle, error.message, banner: false)
            }
        })
    }

    static let pinFailedTitle = "Could not pin the session"
}
