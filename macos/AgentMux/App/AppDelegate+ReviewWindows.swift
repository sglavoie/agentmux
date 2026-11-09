import AppKit

extension AppDelegate {
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
                  let session = self.store.managedSessions.first(where: { $0.aslRunID == row.runID }) else {
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
            let live = Self.session(runtimeSessionID: entry.runtimeSessionID, in: self.store.managedSessions)
                ?? self.store.managedSessions.first { entry.conversation != nil && $0.conversation == entry.conversation }
            guard let live else { return self.refuse("No live session matches this recorded event. Open its conversation to inspect or resume it.") }
            self.focus(.focusing(live, title: entry.title))
        }
        activityWindow = controller
        controller.model.clearFlightFilter()
        controller.show()
    }

    @objc func showFlightLog(_ sender: Any?) {
        let controller = flightLogWindow ?? WorkspaceFlightLogWindow(model: WorkspaceFlightLogModel(command: { [weak self] in self?.command }))
        trackVisibility(of: controller, \.flightLogVisible)
        controller.model.onConversation = { [weak self] identity, at in self?.openHistory(conversation: identity, eventTime: at) }
        controller.model.onActivity = { [weak self] session, kind in
            self?.showActivity(nil)
            self?.activityWindow?.model.filterFlightSession(session, kind: kind)
        }
        flightLogWindow = controller
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

    func workspaceRecapCommand() -> AMXCommand? { command }

    func workspaceRecapEvidence(_ identity: ConversationIdentity, message: Int) {
        openHistory(conversation: identity, recapMessage: message)
    }

    func openHistory(conversation: ConversationIdentity?, eventTime: String? = nil, query: String = "", searchNote: String? = nil, recapMessage: Int? = nil, returnTo: ReviewWindowController? = nil) {
        conversationSearchSequence += 1
        let workspace = workspaceController()
        let returnResponder: NSResponder? = searchNote == nil ? workspace.window.firstResponder : workspace.shownTerminalView
        workspace.show()
        let controller = historyWindow ?? WorkspaceHistoryWindow(model: WorkspaceHistoryModel(command: { [weak self] in self?.command }, defaults: .standard, feedbackHistory: reviewFeedbackHistory))
        trackVisibility(of: controller, \.historyVisible) { [weak returnResponder, weak returnTo] app in
            if let window = returnTo?.window, window.isVisible {
                window.makeKeyAndOrderFront(nil)
            } else if eventTime != nil, let activity = app.activityWindow?.window, activity.isVisible {
                activity.makeKeyAndOrderFront(nil)
            } else if app.workspaceVisible {
                app.workspace?.window.makeKeyAndOrderFront(nil)
                if let view = returnResponder as? NSView, view.window === app.workspace?.window,
                   !view.isHiddenOrHasHiddenAncestor { app.workspace?.window.makeFirstResponder(view) }
            }
        }
        controller.model.onHandoff = { [weak self, weak controller] conversation in
            self?.showHandoff(source: conversation.identity, profile: conversation.profile, on: controller?.window, taskName: conversation.title)
        }
        controller.model.sourceOrigin = { [weak self] identity in self?.handoffOrigins.origin(conversation: identity)?.identity }
        controller.model.onSource = { [weak self] identity in self?.openHistory(conversation: identity) }
        controller.model.onAdvanced = { [weak self] identity, query in
            var view = WorkspaceUtilityView(kind: .history, target: identity.encoded, title: "Conversation Actions")
            view.historyQuery = query
            self?.showWorkspaceView(view)
        }
        controller.model.onAction = { [weak self] conversation, fork, completion in
            self?.openHistoryConversation(conversation, fork: fork, completion: completion)
        }
        controller.model.liveConversations = Set(store.feed == .live ? store.managedSessions.compactMap { $0.conversation?.encoded } : [])
        controller.model.workingConversations = Set(store.managedSessions.filter(\.isWorking).compactMap { $0.conversation?.encoded })
        historyWindow = controller
        controller.model.searchEntryNote = searchNote
        if conversation != nil {
            controller.model.query = query
            controller.model.scope = .conversation
            controller.model.includeAutomated = true
        }
        controller.show(project: controller.model.project.isEmpty ? (workspace.shownSessionRow?.project ?? "") : controller.model.project,
                        conversation: conversation ?? controller.model.currentConversation ?? workspace.shownSessionRow?.conversation, eventTime: eventTime, recapMessage: recapMessage)
    }

    func openHistoryConversation(_ conversation: RecentConversation, fork: Bool, completion: @escaping (String?) -> Void) {
        if !fork, store.feed == .live, let live = store.managedSessions.first(where: { $0.conversation == conversation.identity }) {
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
}
