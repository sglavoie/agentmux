import AppKit

extension AppDelegate {
    @objc func showBriefing(_ sender: Any?) {
        presentBriefing()
    }

    func presentBriefing(flight: FlightSession? = nil, kind: String = "all") {
        let controller = briefingWindow ?? WorkspaceBriefingWindow(model: WorkspaceCatchUpModel(
            briefing: WorkspaceBriefingModel(command: { [weak self] in self?.command }),
            activity: WorkspaceActivityModel(command: { [weak self] in self?.command }, defaults: .standard),
            summary: WorkspaceReturnSummary(command: { [weak self] in self?.command })))
        briefingWindow = controller
        trackVisibility(of: controller, \.briefingVisible)
        controller.model.briefing.onFocus = { [weak self] key in
            guard let self, self.store.feed == .live else { return }
            guard let session = self.store.managedSessions.first(where: { AttentionCycler.key($0) == key }) else {
                self.refuse("This session is no longer live. Refresh the briefing or read its conversation."); return
            }
            self.focus(.focusing(session, title: MenuBuilder.name(of: session)))
        }
        controller.model.briefing.onConversation = { [weak self, weak controller] identity, index in
            self?.openHistory(conversation: identity, recapMessage: index, returnTo: controller)
        }
        controller.model.briefing.onHandoff = { [weak self, weak controller] conversation in
            self?.showHandoff(source: conversation.identity, profile: conversation.profile, on: controller?.window, taskName: conversation.title)
        }
        controller.model.briefing.onContinue = { [weak self] conversation, done in
            guard let self else { done("The workspace is unavailable."); return }
            self.openHistoryConversation(conversation, fork: false, completion: done)
        }
        controller.model.briefing.onAnswer = { [weak self] answer in self?.workspaceAnswer(answer) }
        controller.model.briefing.onReview = { [weak self] key in
            guard let self, self.store.feed == .live,
                  let session = self.store.managedSessions.first(where: { AttentionCycler.key($0) == key && $0.iconState == "stop" }) else { return }
            self.focus(.focusing(session, title: MenuBuilder.name(of: session)))
            self.workspacePerform(.splitChanges)
        }
        controller.model.briefing.onFork = { [weak self] conversation, done in
            guard let self else { done("The workspace is unavailable."); return }
            self.openHistoryConversation(conversation, fork: true, completion: done)
        }
        controller.model.activity.onConversation = { [weak self, weak controller] identity, at in
            self?.openHistory(conversation: identity, eventTime: at, returnTo: controller)
        }
        controller.model.activity.onFocus = { [weak self] entry in self?.focusActivity(entry) }
        refreshBriefing()
        refreshProjects()
        if let flight { controller.showFlight(flight, kind: kind) }
        else { controller.show() }
    }

    /// Snapshots keep live state current without refreshing transcript history
    /// or generating summaries in the background.
    func refreshBriefing() {
        briefingWindow?.model.update(projects: projects.state.value ?? [], sessions: store.managedSessions,
            asl: store.snapshot?.asl.waiting ?? [],
            live: store.feed == .live, order: attentionOrder, pendingAnswers: Array(workspace?.inspector.sentAnswers.values ?? [:].values))
    }
}
